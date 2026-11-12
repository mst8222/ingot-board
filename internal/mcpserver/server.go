// Package mcpserver 实现最小 MCP（Model Context Protocol）stdio 服务，
// 把看板的 Agent 接口暴露为编辑器可直接调用的工具。
package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mst8222/ingot-board/internal/agentclient"
)

// Server 基于 stdio 的 MCP 服务。
type Server struct {
	client *agentclient.Client
	in     *bufio.Reader
	out    io.Writer
}

// New 创建 MCP 服务，默认走标准输入输出。
func New(c *agentclient.Client) *Server {
	return &Server{client: c, in: bufio.NewReaderSize(os.Stdin, 1<<20), out: os.Stdout}
}

// rpcMessage JSON-RPC 2.0 信封。
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Run 阻塞处理消息，直到 stdin EOF 或 ctx 取消。
func (s *Server) Run(ctx context.Context) error {
	dec := json.NewDecoder(s.in)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		var msg rpcMessage
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		isNotification := len(msg.ID) == 0 || string(msg.ID) == "null"
		if isNotification {
			// notifications/initialized 等无需响应。
			continue
		}
		result, callErr := s.handle(ctx, msg)
		if callErr != nil {
			s.write(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg.ID,
				"error":   map[string]any{"code": -32000, "message": callErr.Error()},
			})
			continue
		}
		s.write(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
	}
}

func (s *Server) write(v any) {
	raw, _ := json.Marshal(v)
	s.out.Write(raw)
	s.out.Write([]byte("\n"))
}

func (s *Server) handle(ctx context.Context, msg rpcMessage) (any, error) {
	switch msg.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		version := p.ProtocolVersion
		if version == "" {
			version = "2024-11-05"
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "ingot", "version": "0.1.0"},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": toolDefs()}, nil
	case "tools/call":
		return s.callTool(ctx, msg.Params)
	default:
		return nil, fmt.Errorf("未知方法: %s", msg.Method)
	}
}

func toolDefs() []map[string]any {
	str := func(s string) *string { return &s }
	_ = str
	return []map[string]any{
		{
			"name":        "list_my_tasks",
			"description":  "列出看板指派给当前 Agent 的任务，按看板列内顺序返回（第一条最优先）。默认只返回 todo 状态（可领取）；也可查询 doing/review。idea 灵感箱对 Agent 不可见。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{
						"type":        "string",
						"enum":        []string{"todo", "doing", "review"},
						"description": "可选，默认 todo",
					},
				},
			},
		},
		{
			"name":        "claim_task",
			"description":  "领取一个 todo 任务，状态原子变更为 doing。仅指派给当前 Agent 的任务可领取，成功者独占。返回任务详情与人类反馈（feedback：驳回原因与人类备注），若任务曾被驳回，必须逐条处理驳回意见。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "integer", "description": "任务 ID"},
				},
				"required": []string{"task_id"},
			},
		},
		{
			"name":        "get_task_feedback",
			"description":  "查看人类对任务的反馈（驳回原因与人类备注，按时间正序）。执行中人类可能追加备注，长任务可在阶段间隙查看。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "integer", "description": "任务 ID"},
				},
				"required": []string{"task_id"},
			},
		},
		{
			"name":        "heartbeat_task",
			"description":  "为正在执行的任务续租，建议长任务每 5 分钟调用一次；超时未续租的任务会被系统回收。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "integer"},
				},
				"required": []string{"task_id"},
			},
		},
		{
			"name":        "submit_task",
			"description":  "完成任务后提交成果说明（含改动摘要、验证方式、产出物路径），任务进入 review 等待人类评审。可可选上报实际消耗成本 cost_used。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "integer"},
					"summary": map[string]any{"type": "string", "description": "成果说明"},
					"cost_used": map[string]any{
						"type":        "number",
						"minimum":     0,
						"description": "可选：本次实际消耗的成本（credit）。不填则终审时按总管预估成本结算",
					},
				},
				"required": []string{"task_id", "summary"},
			},
		},
		{
			"name":        "release_task",
			"description":  "无法继续执行时把任务退回 todo，需说明原因。额度用尽时同时把 quota 设为 exhausted，服务会转交给仍有额度的 Agent；可选上报已消耗成本 cost_used。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "integer"},
					"reason":  map[string]any{"type": "string"},
					"quota": map[string]any{
						"type": "string",
						"enum": []string{"ok", "exhausted"},
					},
					"cost_used": map[string]any{
						"type":        "number",
						"minimum":     0,
						"description": "可选：本次已实际消耗的成本（credit）",
					},
				},
				"required": []string{"task_id", "reason"},
			},
		},
		{
			"name":        "report_quota",
			"description": "上报当前额度。没有额度时填 exhausted，然后退回任务，不要开始做。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"quota": map[string]any{
						"type": "string",
						"enum": []string{"ok", "exhausted"},
					},
				},
				"required": []string{"quota"},
			},
		},
		{
			"name":        "add_task_note",
			"description":  "向任务追加进展备注，供人类在看板时间线查看。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "integer"},
					"message": map[string]any{"type": "string"},
				},
				"required": []string{"task_id", "message"},
			},
		},
	}
}

func (s *Server) callTool(ctx context.Context, raw json.RawMessage) (any, error) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}

	textResult := func(v any) any {
		data, _ := json.MarshalIndent(v, "", "  ")
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(data)}},
		}
	}
	errResult := func(err error) any {
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": "错误: " + err.Error()}},
			"isError": true,
		}
	}

	switch p.Name {
	case "list_my_tasks":
		var args struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(p.Arguments, &args)
		status := strings.TrimSpace(args.Status)
		if status == "" {
			status = "todo"
		}
		tasks, err := s.client.ListTasks(ctx, status)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(map[string]any{"count": len(tasks), "tasks": tasks}), nil

	case "claim_task":
		var args struct {
			TaskID int64 `json:"task_id"`
		}
		if err := json.Unmarshal(p.Arguments, &args); err != nil || args.TaskID <= 0 {
			return nil, fmt.Errorf("task_id 必须为正整数")
		}
		t, err := s.client.Claim(ctx, args.TaskID)
		if err != nil {
			return errResult(err), nil
		}
		feedback, err := s.client.Feedback(ctx, args.TaskID)
		if err != nil {
			// 任务已领到手，反馈读不到时如实告知，由 Agent 用 get_task_feedback 重试。
			return errResult(fmt.Errorf("任务 %d 已领取，但读取人类反馈失败（请调用 get_task_feedback 重试）: %w", args.TaskID, err)), nil
		}
		return textResult(map[string]any{"task": t, "feedback": feedback}), nil

	case "get_task_feedback":
		var args struct {
			TaskID int64 `json:"task_id"`
		}
		if err := json.Unmarshal(p.Arguments, &args); err != nil || args.TaskID <= 0 {
			return nil, fmt.Errorf("task_id 必须为正整数")
		}
		feedback, err := s.client.Feedback(ctx, args.TaskID)
		if err != nil {
			return errResult(err), nil
		}
		return textResult(map[string]any{"count": len(feedback), "feedback": feedback}), nil

	case "heartbeat_task":
		var args struct {
			TaskID int64 `json:"task_id"`
		}
		if err := json.Unmarshal(p.Arguments, &args); err != nil || args.TaskID <= 0 {
			return nil, fmt.Errorf("task_id 必须为正整数")
		}
		if err := s.client.Heartbeat(ctx, args.TaskID); err != nil {
			return errResult(err), nil
		}
		return textResult(map[string]string{"status": "ok"}), nil

	case "submit_task":
		var args struct {
			TaskID   int64    `json:"task_id"`
			Summary  string   `json:"summary"`
			CostUsed *float64 `json:"cost_used"`
		}
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return nil, err
		}
		if args.TaskID <= 0 || strings.TrimSpace(args.Summary) == "" {
			return nil, fmt.Errorf("task_id 与 summary 必填")
		}
		if err := s.client.SubmitWithCost(ctx, args.TaskID, args.Summary, args.CostUsed); err != nil {
			return errResult(err), nil
		}
		return textResult(map[string]string{"status": "submitted"}), nil

	case "release_task":
		var args struct {
			TaskID   int64    `json:"task_id"`
			Reason   string   `json:"reason"`
			Quota    string   `json:"quota"`
			CostUsed *float64 `json:"cost_used"`
		}
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return nil, err
		}
		if args.TaskID <= 0 {
			return nil, fmt.Errorf("task_id 必须为正整数")
		}
		if _, err := s.client.ReleaseWithQuotaAndCost(ctx, args.TaskID, args.Reason, args.Quota, args.CostUsed); err != nil {
			return errResult(err), nil
		}
		return textResult(map[string]string{"status": "released"}), nil

	case "report_quota":
		var args struct {
			Quota string `json:"quota"`
		}
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return nil, err
		}
		if err := s.client.ReportQuota(ctx, args.Quota); err != nil {
			return errResult(err), nil
		}
		return textResult(map[string]string{"status": "ok"}), nil

	case "add_task_note":
		var args struct {
			TaskID  int64  `json:"task_id"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(p.Arguments, &args); err != nil || args.TaskID <= 0 {
			return nil, fmt.Errorf("task_id 必须为正整数")
		}
		if err := s.client.AddNote(ctx, args.TaskID, args.Message); err != nil {
			return errResult(err), nil
		}
		return textResult(map[string]string{"status": "noted"}), nil

	default:
		return nil, fmt.Errorf("未知工具: %s", p.Name)
	}
}
