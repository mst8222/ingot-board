// Package cli 是 ingot / ingot-agent 的命令行入口，
// 提供服务启动、Agent 与 Token 管理、MCP stdio、runner sidecar 子命令。
package cli

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mst8222/ingot-board/internal/agentclient"
	"github.com/mst8222/ingot-board/internal/app"
	"github.com/mst8222/ingot-board/internal/mcpserver"
	"github.com/mst8222/ingot-board/internal/runner"
	"github.com/mst8222/ingot-board/internal/serve"
	"github.com/mst8222/ingot-board/internal/store"
	"github.com/mst8222/ingot-board/internal/webassets"
)

// Main 命令行主入口。defaultServe=true 时（ingot）无子命令默认启动服务。
func Main(args []string, defaultServe bool) int {
	if len(args) == 0 {
		if defaultServe {
			return runServe(args)
		}
		printUsage(os.Stdout)
		return 0
	}
	switch args[0] {
	case "-h", "--help", "help":
		printUsage(os.Stdout)
		return 0
	case "serve":
		return runServe(args[1:])
	case "agent", "agents":
		return runAgent(args[1:])
	case "mcp":
		return runMCP(args[1:])
	case "runner":
		return runRunner(args[1:])
	case "rules":
		return runRules(args[1:])
	case "connect":
		return runConnect(args[1:])
	default:
		if defaultServe {
			return runServe(args)
		}
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n", args[0])
		printUsage(os.Stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `Ingot Board - 面向本机 AI Agent 的任务看板

用法:
  ingot [serve] [--addr 127.0.0.1:18721] [--data-dir DIR] [--no-browser]
  ingot-agent agent list [--data-dir DIR]
  ingot-agent agent add --name <名称> [--kind mcp|runner] [--data-dir DIR]
  ingot-agent agent token --id <Agent ID> [--data-dir DIR]
  ingot-agent agent enable|disable --id <Agent ID> [--data-dir DIR]
  ingot-agent mcp --token <Agent Token> [--url URL | --data-dir DIR]
  ingot-agent runner --token <Token> [--url URL | --data-dir DIR] [--workdir DIR]
                     [--adapter opencode|pi] [--interval 30s] [--bin PATH]
  ingot-agent rules [--format md|mdc] [--out PATH]
  ingot-agent connect [--data-dir DIR] [--exe PATH]

说明:
  serve   启动看板服务并托管 Web GUI
  agent   登记/管理 Agent 身份与访问 Token
  mcp     以 stdio MCP 服务运行，供编辑器作为 MCP Server 接入
  runner  无人值守轮询，用 ACP 拉起 opencode acp 或 pi-acp 执行任务
  rules   输出编辑器规则模板，指示 Agent 何时扫描看板、如何领取与提交

mcp / runner 省略 --url 时，从数据目录的 endpoint.json 自动发现服务地址
（ingot 与桌面端启动时都会写入），桌面端随机端口重启后也能连上。
`)
}

// cliDataDir 解析数据目录。未指定时取默认目录，并顺带完成旧目录的一次性迁移，
// 这样改名后的第一次运行仍然读到原来的 data.db。
func cliDataDir(v string) string {
	if v != "" {
		return v
	}
	dir := serve.DefaultDataDir()
	if moved, err := serve.MigrateLegacyDataDir(dir); err != nil {
		fmt.Fprintf(os.Stderr, "旧数据目录迁移失败（继续使用 %s）: %v\n", dir, err)
	} else if moved {
		fmt.Printf("已把旧数据目录 %s 迁移到 %s\n", serve.LegacyDataDirName, dir)
	}
	return dir
}

// ---------- serve ----------

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", serve.DefaultAddr, "监听地址")
	dataDir := fs.String("data-dir", "", "数据目录（默认 %AppData%/IngotBoard）")
	noBrowser := fs.Bool("no-browser", false, "不自动打开浏览器")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ins, err := serve.Run(ctx, serve.Options{
		Addr:    *addr,
		DataDir: *dataDir,
		Static:  webassets.Dist,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "服务启动失败: %v\n", err)
		return 1
	}
	url := "http://" + ins.Addr
	fmt.Printf("Ingot Board 服务已启动: %s\n按 Ctrl+C 退出。\n", url)
	if !*noBrowser {
		if err := openBrowser(url); err != nil {
			fmt.Fprintf(os.Stderr, "打开浏览器失败: %v\n", err)
		}
	}
	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = ins.Shutdown(shutCtx)
	return 0
}

// ---------- agent 管理 ----------

func runAgent(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "需要子命令: list|add|token|enable|disable")
		return 2
	}
	sub := args[0]

	// 各子命令共用一个 FlagSet，--data-dir 对所有子命令都有效。
	fs := flag.NewFlagSet("agent "+sub, flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "数据目录（默认 %AppData%/IngotBoard）")
	var name, kind *string
	var id *int64
	switch sub {
	case "list":
	case "add":
		name = fs.String("name", "", "Agent 名称，如 opencode/cursor/trae")
		kind = fs.String("kind", "mcp", "类型：mcp 或 runner")
	case "token", "enable", "disable":
		id = fs.Int64("id", 0, "Agent ID")
	default:
		fmt.Fprintf(os.Stderr, "未知 agent 子命令: %s\n", sub)
		return 2
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if name != nil && *name == "" {
		fmt.Fprintln(os.Stderr, "--name 必填")
		return 2
	}
	if id != nil && *id <= 0 {
		fmt.Fprintln(os.Stderr, "--id 必填且为正整数")
		return 2
	}

	dir := cliDataDir(*dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "创建数据目录失败: %v\n", err)
		return 1
	}
	st, err := store.Open(filepath.Join(dir, "data.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "打开数据库失败: %v\n", err)
		return 1
	}
	defer st.Close()

	switch sub {
	case "list":
		agents, err := st.ListAgents()
		if err != nil {
			fmt.Fprintf(os.Stderr, "查询失败: %v\n", err)
			return 1
		}
		fmt.Printf("%-5s %-20s %-10s %-8s %s\n", "ID", "NAME", "KIND", "ENABLED", "LAST_SEEN")
		for _, a := range agents {
			last := "-"
			if a.LastSeenAt != nil {
				last = a.LastSeenAt.Local().Format("2006-01-02 15:04:05")
			}
			fmt.Printf("%-5d %-20s %-10s %-8v %s\n", a.ID, a.Name, a.Kind, a.Enabled, last)
		}
		return 0

	case "add":
		svc := app.New(st)
		a, token, err := svc.RegisterAgent(*name, *kind)
		if err != nil {
			fmt.Fprintf(os.Stderr, "登记失败: %v\n", err)
			return 1
		}
		fmt.Printf("Agent 已登记: id=%d name=%s kind=%s\n", a.ID, a.Name, a.Kind)
		fmt.Printf("访问 Token（仅显示一次，请妥善保存）:\n%s\n", token)
		return 0

	case "token":
		svc := app.New(st)
		token, err := svc.RotateToken(*id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "签发失败: %v\n", err)
			return 1
		}
		fmt.Printf("Agent %d 的新 Token（旧 Token 立即失效）:\n%s\n", *id, token)
		return 0

	default: // enable / disable
		if err := st.SetAgentEnabled(*id, sub == "enable"); err != nil {
			fmt.Fprintf(os.Stderr, "操作失败: %v\n", err)
			return 1
		}
		fmt.Printf("Agent %d 已%s\n", *id, map[bool]string{true: "启用", false: "禁用"}[sub == "enable"])
		return 0
	}
}

// ---------- mcp ----------

// agentConn 是 mcp / runner 共用的连接参数。
type agentConn struct {
	url, dataDir, token *string
}

func addAgentConnFlags(fs *flag.FlagSet) agentConn {
	return agentConn{
		url:     fs.String("url", "", "看板服务地址；省略时从数据目录的 endpoint.json 自动发现（桌面端随机端口也能连上）"),
		dataDir: fs.String("data-dir", "", "数据目录，用于自动发现服务地址（默认 %AppData%/IngotBoard）"),
		token:   fs.String("token", "", "Agent 访问 Token（也可用环境变量 INGOT_AGENT_TOKEN）"),
	}
}

// client 构造 Agent 客户端；token 缺失时返回 nil 并已输出提示。
func (c agentConn) client() *agentclient.Client {
	token := *c.token
	if token == "" {
		token = os.Getenv("INGOT_AGENT_TOKEN")
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "需要 --token 或环境变量 INGOT_AGENT_TOKEN")
		return nil
	}
	if *c.url != "" {
		return agentclient.New(*c.url, token)
	}
	return agentclient.NewDiscover(cliDataDir(*c.dataDir), token)
}

func runMCP(args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	conn := addAgentConnFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cl := conn.client()
	if cl == nil {
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// MCP 由编辑器拉起并长期挂着，看板往往晚于编辑器启动：
	// 服务暂时连不上不退出（工具调用时会返回明确错误），只有 Token 被服务端拒绝才退出。
	if _, err := cl.Me(ctx); err != nil {
		var apiErr *agentclient.APIError
		if errors.As(err, &apiErr) {
			fmt.Fprintf(os.Stderr, "鉴权失败: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "暂时连不上看板服务，工具调用时将重试: %v\n", err)
	}
	if err := mcpserver.New(cl).Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "MCP 服务异常: %v\n", err)
		return 1
	}
	return 0
}

// ---------- runner ----------

func runRunner(args []string) int {
	fs := flag.NewFlagSet("runner", flag.ContinueOnError)
	conn := addAgentConnFlags(fs)
	workdir := fs.String("workdir", "", "默认工作目录")
	adapter := fs.String("adapter", "opencode", "执行适配器（opencode 或 pi，均走 ACP）")
	bin := fs.String("bin", "", "适配器可执行文件路径（默认从 PATH 查找）")
	interval := fs.Duration("interval", 30*time.Second, "轮询间隔")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cl := conn.client()
	if cl == nil {
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	r := runner.New(runner.Config{
		Client:       cl,
		Adapter:      *adapter,
		Bin:          *bin,
		Workdir:      *workdir,
		PollInterval: *interval,
	})
	if err := r.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "runner 失败: %v\n", err)
		return 1
	}
	return 0
}

// ---------- rules ----------

// agentRule 编辑器规则模板。MCP 通道下"定时扫描看板"靠编辑器规则驱动 Agent，
// 编辑器自身不会轮询。
//
//go:embed agent_rule.md
var agentRule string

// mdcHeader Cursor 规则（.cursor/rules/*.mdc）的头部，alwaysApply 让每次会话都加载。
const mdcHeader = "---\ndescription: Ingot Board 看板任务的扫描、领取与提交流程\nalwaysApply: true\n---\n\n"

func runRules(args []string) int {
	fs := flag.NewFlagSet("rules", flag.ContinueOnError)
	format := fs.String("format", "md", "输出格式：md（通用 Markdown，如 Trae / Qoder 的规则文件）或 mdc（Cursor 规则）")
	out := fs.String("out", "", "写入文件路径（如 .cursor/rules/ingot.mdc）；省略时输出到标准输出")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	content := agentRule
	switch *format {
	case "md":
	case "mdc":
		content = mdcHeader + agentRule
	default:
		fmt.Fprintf(os.Stderr, "不支持的格式: %s（可选 md、mdc）\n", *format)
		return 2
	}
	if *out == "" {
		fmt.Print(content)
		return 0
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "创建目录失败: %v\n", err)
		return 1
	}
	if err := os.WriteFile(*out, []byte(content), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "写入失败: %v\n", err)
		return 1
	}
	fmt.Printf("规则已写入 %s\n", *out)
	return 0
}

func runConnect(args []string) int {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "数据目录（默认 %AppData%/IngotBoard）")
	exe := fs.String("exe", "", "写入 MCP 的 ingot-agent 可执行文件；省略时用当前程序")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dir := cliDataDir(*dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "创建数据目录失败: %v\n", err)
		return 1
	}
	st, err := store.Open(filepath.Join(dir, "data.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "打开数据库失败: %v\n", err)
		return 1
	}
	defer st.Close()
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "找不到用户目录: %v\n", err)
		return 1
	}
	appData := os.Getenv("APPDATA")
	if appData == "" {
		appData = home
	}
	report, err := ConnectLocalAgents(st, ConnectLayout{Home: home, AppData: appData, Exe: *exe})
	if err != nil {
		fmt.Fprintf(os.Stderr, "接入失败: %v\n", err)
		return 1
	}
	fmt.Println(report.String())
	return 0
}
