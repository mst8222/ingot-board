// Package runner 是 Agent 无人值守执行器：定时轮询指派给本机 Agent 的 todo 任务，
// 原子领取后用 ACP 拉起 OpenCode 或 Pi，执行期间维持心跳，成功则提交，失败则退回。
package runner

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mst8222/ingot-board/internal/acp"
	"github.com/mst8222/ingot-board/internal/agentclient"
	"github.com/mst8222/ingot-board/internal/domain"
	"github.com/mst8222/ingot-board/internal/hosts"
)

const (
	// MaxConsecutiveFailures 同一任务连续执行失败的上限。
	//
	// 失败退回后任务仍指派给本 Agent，不设上限的话下一轮会立刻再领，
	// 环境性问题（工作目录不存在、依赖缺失）会变成无休止的"领取→失败→退回"。
	MaxConsecutiveFailures = 3

	// PiModel 是 Pi 必须锁定的模型。真实值由执行端注册表声明，
	// 这里保留别名，既有测试与调用方不必改动。
	PiModel = hosts.PiModel
)

// Config 执行器配置。
type Config struct {
	Client       *agentclient.Client
	Adapter      string        // 适配器类型：opencode 或 pi
	Bin          string        // 适配器可执行文件，空则按 Adapter 推断
	Workdir      string        // 默认工作目录（任务未指定 workdir 时使用）
	PollInterval time.Duration // 轮询间隔
	Heartbeat    time.Duration // 心跳间隔
	// Env 附加给 ACP 子进程。测试用它选择假进程，正式启动留空。
	Env []string
}

// Runner 轮询执行器。
type Runner struct {
	cfg Config
	// failures 任务连续失败次数，成功提交后清零。
	failures map[int64]int
	// parked 达到失败上限、暂停自动领取的任务 → 退回时的 version。
	// 人类修改任务、重新派发都会抬升 version，届时自动恢复领取。
	parked map[int64]int64
}

// New 创建执行器。
func New(cfg Config) *Runner {
	if cfg.Adapter == "" {
		cfg.Adapter = "opencode"
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 30 * time.Second
	}
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = 5 * time.Minute
	}
	return &Runner{cfg: cfg, failures: map[int64]int{}, parked: map[int64]int64{}}
}

// Run 阻塞运行直到 ctx 取消。
func (r *Runner) Run(ctx context.Context) error {
	// 适配器与可执行文件在启动时校验：放到执行阶段才发现的话，
	// 每个任务都会被领取后立即失败退回。
	bin, err := r.resolveBin()
	if err != nil {
		return err
	}
	r.cfg.Bin = bin

	me, err := r.cfg.Client.Me(ctx)
	if err != nil {
		return fmt.Errorf("Agent 身份校验失败: %w", err)
	}
	log.Printf("runner 已启动：agent=%s kind=%s 适配器=%s(%s) 轮询=%s",
		me.Name, me.Kind, r.cfg.Adapter, bin, r.cfg.PollInterval)

	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()
	if err := r.tick(ctx); err != nil {
		log.Printf("轮询出错: %v", err)
	}
	for {
		select {
		case <-ctx.Done():
			log.Println("runner 退出")
			return nil
		case <-ticker.C:
			if err := r.tick(ctx); err != nil {
				log.Printf("轮询出错: %v", err)
			}
		}
	}
}

func (r *Runner) resolveBin() (string, error) {
	spec, ok := hosts.Lookup(r.cfg.Adapter)
	if !ok {
		return "", fmt.Errorf("不支持的适配器: %s（当前实现 opencode 与 pi）", r.cfg.Adapter)
	}
	bin := r.cfg.Bin
	if bin == "" {
		bin = spec.AdapterBin
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return "", fmt.Errorf("找不到适配器可执行文件 %q: %w", bin, err)
	}
	// npm 全局命令在 Windows 上可能是 .ps1，直接拉起会失败。旁边的 .cmd 才能当进程跑。
	if strings.HasSuffix(strings.ToLower(path), ".ps1") {
		cmdPath := strings.TrimSuffix(path, filepath.Ext(path)) + ".cmd"
		if _, statErr := os.Stat(cmdPath); statErr == nil {
			return cmdPath, nil
		}
	}
	return path, nil
}

// launchArgs 是 ACP 进程的参数。提示词不进参数。
func (r *Runner) launchArgs() []string {
	spec, ok := hosts.Lookup(r.cfg.Adapter)
	if !ok {
		return nil
	}
	return append([]string(nil), spec.LaunchArgs...)
}

func (r *Runner) sessionModel() string {
	spec, ok := hosts.Lookup(r.cfg.Adapter)
	if !ok {
		return ""
	}
	return spec.SessionModel
}

func (r *Runner) commandEnv() []string {
	if len(r.cfg.Env) == 0 {
		return nil
	}
	return append(os.Environ(), r.cfg.Env...)
}

// tick 执行一轮：按看板列内顺序取第一条可领的 todo，领取并执行。
func (r *Runner) tick(ctx context.Context) error {
	tasks, err := r.cfg.Client.ListTasks(ctx, "todo")
	if err != nil {
		return err
	}
	task := r.pick(tasks)
	if task == nil {
		return nil
	}

	// 领取前先读反馈：读失败就本轮不领，避免领到手却拿不到驳回原因。
	feedback, err := r.cfg.Client.Feedback(ctx, task.ID)
	if err != nil {
		return fmt.Errorf("读取任务 %d 的人类反馈失败: %w", task.ID, err)
	}

	claimed, err := r.cfg.Client.Claim(ctx, task.ID)
	if err != nil {
		// 被其他执行器抢先或状态变化，下轮再试。
		log.Printf("任务 %d 领取失败: %v", task.ID, err)
		return nil
	}
	log.Printf("已领取任务 #%d: %s", claimed.ID, claimed.Title)
	r.execute(ctx, claimed, feedback)
	return nil
}

// pick 选出本轮要执行的任务：列表已按看板手动顺序排列，取第一条未被暂停的。
func (r *Runner) pick(tasks []*domain.Task) *domain.Task {
	for _, t := range tasks {
		if v, ok := r.parked[t.ID]; ok {
			if v == t.Version {
				continue
			}
			// 人类动过这条任务，恢复自动领取并重新计数。
			delete(r.parked, t.ID)
			delete(r.failures, t.ID)
			log.Printf("任务 #%d 已被修改，恢复自动领取", t.ID)
		}
		return t
	}
	return nil
}

func (r *Runner) execute(ctx context.Context, task *domain.Task, feedback []*domain.TaskEvent) {
	// 租约丢失时 runCtx 被取消：先发 session/cancel，再结束进程。
	// 结果已无法提交，继续跑只是白白占用机器。
	runCtx, cancelRun := context.WithCancelCause(ctx)
	defer cancelRun(nil)
	go r.heartbeatLoop(runCtx, task.ID, cancelRun)

	workdir := strings.TrimSpace(task.Workdir)
	if workdir == "" {
		workdir = r.cfg.Workdir
	}
	assistant, runErr := r.runACP(runCtx, workdir, buildPrompt(task, feedback))
	tail := tailOutput(assistant, 6000)

	if cause := context.Cause(runCtx); cause != nil && !errors.Is(cause, context.Canceled) {
		log.Printf("任务 #%d 已中止: %v", task.ID, cause)
		return
	}
	if ctx.Err() != nil {
		// runner 自身退出：把任务交还，而不是让它挂着等租约超时。
		r.release(task.ID, "runner 退出，执行被中断")
		return
	}
	if runErr != nil {
		r.fail(task.ID, fmt.Sprintf("执行失败: %v\n工作目录: %s\n输出摘录:\n%s", runErr, workdir, tail))
		return
	}

	summary := fmt.Sprintf("已由 %s 适配器自动完成。\n工作目录: %s\n输出摘录:\n%s",
		r.cfg.Adapter, workdir, tail)
	if err := r.cfg.Client.Submit(ctx, task.ID, summary); err != nil {
		log.Printf("任务 #%d 提交失败: %v", task.ID, err)
		return
	}
	delete(r.failures, task.ID)
	log.Printf("任务 #%d 已提交，等待人类评审", task.ID)
}

// runACP 用 ACP 跑完一轮提示。模型锁定失败时不会发送提示词。
func (r *Runner) runACP(ctx context.Context, workdir, prompt string) (string, error) {
	dir := strings.TrimSpace(workdir)
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = cwd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	sess, err := acp.Start(ctx, acp.Options{
		Bin:   r.cfg.Bin,
		Args:  r.launchArgs(),
		Dir:   abs,
		Model: r.sessionModel(),
		Env:   r.commandEnv(),
	})
	if err != nil {
		return "", err
	}
	defer sess.Close()
	return sess.Prompt(ctx, prompt)
}

// fail 记录一次失败并退回任务；达到上限时暂停自动领取，并在退回原因里写明。
func (r *Runner) fail(taskID int64, reason string) {
	r.failures[taskID]++
	n := r.failures[taskID]
	if n >= MaxConsecutiveFailures {
		reason = fmt.Sprintf("连续失败 %d 次，本 runner 暂停自动领取该任务；"+
			"修改任务内容或重新派发后自动恢复。\n%s", n, reason)
	}
	t := r.release(taskID, reason)
	if t != nil && n >= MaxConsecutiveFailures {
		r.parked[taskID] = t.Version
		log.Printf("任务 #%d 连续失败 %d 次，已暂停自动领取", taskID, n)
	}
}

// release 退回任务。用独立的超时上下文：runner 退出时外层 ctx 已取消，
// 沿用它的话退回请求根本发不出去，任务只能挂到租约超时。
func (r *Runner) release(taskID int64, reason string) *domain.Task {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t, err := r.cfg.Client.Release(ctx, taskID, reason)
	if err != nil {
		log.Printf("任务 #%d 退回失败: %v", taskID, err)
		return nil
	}
	return t
}

// heartbeatLoop 定期续租。
//
// 服务端明确拒绝（APIError，如租约已被回收）说明任务已不归本 runner，
// 立即中止执行；网络类错误只记录，下个周期再试 —— 租约 15 分钟、心跳 5 分钟，
// 偶发一两次失败不会丢租约。
func (r *Runner) heartbeatLoop(ctx context.Context, taskID int64, abort context.CancelCauseFunc) {
	ticker := time.NewTicker(r.cfg.Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := r.cfg.Client.Heartbeat(ctx, taskID)
			if err == nil {
				continue
			}
			var apiErr *agentclient.APIError
			if errors.As(err, &apiErr) {
				abort(fmt.Errorf("租约已失效: %w", err))
				return
			}
			log.Printf("任务 #%d 心跳失败（下个周期重试）: %v", taskID, err)
		}
	}
}

// buildPrompt 把任务字段与人类反馈组装成 Agent 可直接执行的提示词。
func buildPrompt(t *domain.Task, feedback []*domain.TaskEvent) string {
	var b strings.Builder
	b.WriteString("请完成 Ingot Board 看板指派给你的任务。\n\n")
	fmt.Fprintf(&b, "任务标题：%s\n", t.Title)
	if t.Description != "" {
		fmt.Fprintf(&b, "\n任务描述：\n%s\n", t.Description)
	}
	if t.AcceptanceCriteria != "" {
		fmt.Fprintf(&b, "\n验收标准：\n%s\n", t.AcceptanceCriteria)
	}
	if t.Workdir != "" {
		fmt.Fprintf(&b, "\n工作目录：%s\n", t.Workdir)
	}
	if len(feedback) > 0 {
		b.WriteString("\n人类反馈（按时间顺序；驳回意见必须逐条处理）：\n")
		for _, ev := range feedback {
			label := "备注"
			if ev.Type == domain.EventReject {
				label = "驳回"
			}
			fmt.Fprintf(&b, "- [%s %s] %s\n", label, ev.CreatedAt.Local().Format("2006-01-02 15:04"), ev.Message)
		}
	}
	b.WriteString("\n要求：在对应工作目录内完成修改并自行验证；完成后不要自行宣告结束，")
	b.WriteString("由外部执行器统一提交结果。")
	return b.String()
}

func tailOutput(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return "……（前文省略）……\n" + s[len(s)-max:]
}
