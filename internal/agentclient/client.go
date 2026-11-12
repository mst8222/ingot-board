// Package agentclient 是 Agent 侧访问看板服务的 HTTP 客户端，
// MCP server 与 runner sidecar 共用同一套接口语义。
package agentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mst8222/ingot-board/internal/domain"
	"github.com/mst8222/ingot-board/internal/endpoint"
)

// Client Agent 端 HTTP 客户端。
type Client struct {
	Token string
	HTTP  *http.Client
	// baseURL 每次请求前解析：桌面端每次启动端口都不同，
	// MCP 是被编辑器长期挂着的进程，启动时解析一次的话看板重启后就再也连不上。
	baseURL func() (string, error)
}

// New 创建连接固定地址的客户端，baseURL 形如 http://127.0.0.1:18721。
func New(baseURL, token string) *Client {
	u := strings.TrimRight(baseURL, "/")
	return newClient(token, func() (string, error) { return u, nil })
}

// NewDiscover 创建按数据目录自动发现服务地址的客户端（读 endpoint.json）。
func NewDiscover(dataDir, token string) *Client {
	path := endpoint.Path(dataDir)
	return newClient(token, func() (string, error) {
		info, err := endpoint.Read(path)
		if err != nil {
			return "", fmt.Errorf("%w（%s 不存在，请先启动 ingot 或桌面端）", err, path)
		}
		return strings.TrimRight(info.URL, "/"), nil
	})
}

func newClient(token string, baseURL func() (string, error)) *Client {
	return &Client{
		Token:   strings.TrimSpace(token),
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		baseURL: baseURL,
	}
}

// APIError 服务端返回的业务错误。
//
// 保留 HTTP 状态码，调用方据此区分"请求被拒绝"（4xx，重试无意义）
// 与网络抖动（无 APIError，下次可能成功）。
type APIError struct {
	Status int
	Code   string
	Msg    string
}

func (e *APIError) Error() string { return e.Msg }

// envelope 是 /api/v1 的统一响应包装，与 internal/api/respond.go 保持一致。
// 这里只声明用得到的字段，避免 client 依赖 api 包。
type envelope struct {
	Code    string          `json:"code"`
	Msg     string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
	Details []string        `json:"details"`
}

// do 发起一次请求并解包信封。
//
// 错误处理约定：服务端把最具体的一行放进 msg，其余行（含 sentinel）放 details。
// 这里全部拼回多行，让调用方（MCP / runner）的日志保留完整上下文。
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	base, err := c.baseURL()
	if err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var env envelope
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&env); err != nil {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return err
		}
		return &APIError{Status: resp.StatusCode, Msg: fmt.Sprintf("HTTP %d: 响应不是合法 JSON", resp.StatusCode)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := env.Msg
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		if len(env.Details) > 0 {
			msg += "\n" + strings.Join(env.Details, "\n")
		}
		return &APIError{Status: resp.StatusCode, Code: env.Code, Msg: msg}
	}
	if out == nil || len(env.Data) == 0 {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

// Me 返回 Token 对应的 Agent 身份。
func (c *Client) Me(ctx context.Context) (*domain.Agent, error) {
	var out domain.Agent
	if err := c.do(ctx, http.MethodGet, "/api/v1/agent/me", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListTasks 查询指派给自己的任务，statuses 为空时默认 todo。
//
// 显式按手动顺序（看板列内顺序）返回：人类拖到列首的就是最该先做的，
// Agent 按返回顺序取第一条即可，不依赖服务端默认排序。
func (c *Client) ListTasks(ctx context.Context, statuses ...string) ([]*domain.Task, error) {
	if len(statuses) == 0 {
		statuses = []string{"todo"}
	}
	path := "/api/v1/agent/tasks?sort=manual&status=" + strings.Join(statuses, ",")
	// data 里是 {"items": [...]}，与列表接口统一。
	var out struct {
		Items []*domain.Task `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// Claim 领取任务。
func (c *Client) Claim(ctx context.Context, id int64) (*domain.Task, error) {
	var out domain.Task
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/agent/tasks/%d/claim", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Heartbeat 续租。
func (c *Client) Heartbeat(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/agent/tasks/%d/heartbeat", id), nil, nil)
}

// Submit 提交成果，进入待评审。不带 cost_used，保持旧调用兼容。
func (c *Client) Submit(ctx context.Context, id int64, summary string) error {
	return c.SubmitWithCost(ctx, id, summary, nil)
}

// SubmitWithCost 提交成果并可选上报实际成本（032-B）。
func (c *Client) SubmitWithCost(ctx context.Context, id int64, summary string, costUsed *float64) error {
	body := map[string]any{"summary": summary}
	if costUsed != nil {
		body["cost_used"] = *costUsed
	}
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/agent/tasks/%d/submit", id), body, nil)
}

// Release 退回任务（执行失败/主动放弃），返回退回后的任务。
func (c *Client) Release(ctx context.Context, id int64, reason string) (*domain.Task, error) {
	return c.ReleaseWithQuota(ctx, id, reason, "")
}

// ReleaseWithQuota 退回任务，并在 quota 非空时同时上报额度。
func (c *Client) ReleaseWithQuota(ctx context.Context, id int64, reason, quota string) (*domain.Task, error) {
	return c.ReleaseWithQuotaAndCost(ctx, id, reason, quota, nil)
}

// ReleaseWithQuotaAndCost 退回任务，同时可选上报额度与实际成本（032-B）。
func (c *Client) ReleaseWithQuotaAndCost(ctx context.Context, id int64, reason, quota string, costUsed *float64) (*domain.Task, error) {
	body := map[string]any{"reason": reason}
	if quota != "" {
		body["quota"] = quota
	}
	if costUsed != nil {
		body["cost_used"] = *costUsed
	}
	var out domain.Task
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/agent/tasks/%d/release", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReportQuota 上报当前额度。
func (c *Client) ReportQuota(ctx context.Context, quota string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/agent/quota", map[string]string{"quota": quota}, nil)
}

// AddNote 追加进展备注。
func (c *Client) AddNote(ctx context.Context, id int64, message string) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/agent/tasks/%d/notes", id),
		map[string]string{"content": message}, nil)
}

// Feedback 返回人类对该任务的反馈：驳回原因与人类备注，按时间正序。
//
// 被驳回的任务回到 todo 后描述不变，要补什么只写在驳回原因里；
// 不把它交给 Agent，Agent 只会原样再做一遍。
func (c *Client) Feedback(ctx context.Context, id int64) ([]*domain.TaskEvent, error) {
	var out struct {
		Items []*domain.TaskEvent `json:"items"`
	}
	path := fmt.Sprintf("/api/v1/agent/tasks/%d/events?page_size=200", id)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	feedback := make([]*domain.TaskEvent, 0, len(out.Items))
	for _, ev := range out.Items {
		if ev.Type == domain.EventReject || (ev.Type == domain.EventNote && ev.ActorType == domain.ActorHuman) {
			feedback = append(feedback, ev)
		}
	}
	// 接口按新到旧返回，反馈按发生顺序阅读更自然。
	slices.Reverse(feedback)
	return feedback, nil
}
