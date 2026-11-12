// Package acp 是 Runner 使用的 Agent Client Protocol stdio 客户端。
// 只实现拉起一轮会话所需的方法，不提供文件系统和终端。
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Options 是拉起一个 ACP 进程的参数。
type Options struct {
	Bin   string
	Args  []string
	Dir   string
	Model string
	Env   []string
}

// Session 是一次 ACP 会话。
type Session struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	sessionID string
	workdir   string
	nextID    int64
	assistant strings.Builder

	mu      sync.Mutex
	pending map[int64]chan rpcResult
	denied  chan error
	writeMu sync.Mutex
	enc     *json.Encoder
	once    sync.Once
	waitErr error
}

type rpcResult struct {
	result json.RawMessage
	err    error
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("ACP 错误 %d", e.Code)
	}
	return e.Message
}

// Start 拉起进程并完成 initialize、session/new，以及可选的模型锁定。
func Start(ctx context.Context, opt Options) (*Session, error) {
	cmd := exec.Command(opt.Bin, opt.Args...)
	cmd.Dir = opt.Dir
	if len(opt.Env) > 0 {
		cmd.Env = opt.Env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &Session{
		cmd:     cmd,
		stdin:   stdin,
		workdir: opt.Dir,
		pending: map[int64]chan rpcResult{},
		denied:  make(chan error, 1),
		enc:     json.NewEncoder(stdin),
	}
	go s.readLoop(stdout)
	go s.drain(stderr)
	if err := s.handshake(ctx, opt); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Session) handshake(ctx context.Context, opt Options) error {
	if _, err := s.call(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{},
		"clientInfo":         map[string]any{"name": "ingot-agent", "version": "0"},
	}); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	raw, err := s.call(ctx, "session/new", map[string]any{
		"cwd":        opt.Dir,
		"mcpServers": []any{},
	})
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	var created struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.SessionID == "" {
		return fmt.Errorf("session/new 没有返回会话")
	}
	s.sessionID = created.SessionID
	if opt.Model == "" {
		return nil
	}
	raw, err = s.call(ctx, "session/set_config_option", map[string]any{
		"sessionId": s.sessionID,
		"configId":  "model",
		"value":     opt.Model,
	})
	if err != nil {
		return fmt.Errorf("锁定模型 %s: %w", opt.Model, err)
	}
	var configured struct {
		ConfigOptions []struct {
			ID      string `json:"id"`
			Current string `json:"currentValue"`
		} `json:"configOptions"`
	}
	_ = json.Unmarshal(raw, &configured)
	for _, item := range configured.ConfigOptions {
		if item.ID == "model" && item.Current != "" && item.Current != opt.Model {
			return fmt.Errorf("模型仍是 %s，没有锁定 %s", item.Current, opt.Model)
		}
	}
	return nil
}

// Prompt 发送一轮提示并返回助手文本。只有 stopReason 为 end_turn 才成功。
func (s *Session) Prompt(ctx context.Context, text string) (string, error) {
	id := atomic.AddInt64(&s.nextID, 1)
	ch := make(chan rpcResult, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	if err := s.writeMsg("session/prompt", &id, map[string]any{
		"sessionId": s.sessionID,
		"prompt":    []any{map[string]any{"type": "text", "text": text}},
	}); err != nil {
		return "", err
	}
	select {
	case <-ctx.Done():
		_ = s.notify("session/cancel", map[string]any{"sessionId": s.sessionID})
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
		}
		return "", ctx.Err()
	case err := <-s.denied:
		_ = s.notify("session/cancel", map[string]any{"sessionId": s.sessionID})
		return "", err
	case res := <-ch:
		if res.err != nil {
			return "", res.err
		}
		var out struct {
			StopReason string `json:"stopReason"`
		}
		if err := json.Unmarshal(res.result, &out); err != nil {
			return "", err
		}
		if out.StopReason != "end_turn" {
			return "", fmt.Errorf("会话停止: %s", out.StopReason)
		}
		s.mu.Lock()
		assistant := s.assistant.String()
		s.mu.Unlock()
		return assistant, nil
	}
}

// Close 结束 ACP 进程。进程已退出时不再把退出码当作失败。
func (s *Session) Close() error {
	s.once.Do(func() {
		if s.stdin != nil {
			_ = s.stdin.Close()
		}
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		if s.cmd != nil {
			s.waitErr = s.cmd.Wait()
		}
	})
	var exitErr *exec.ExitError
	if errors.As(s.waitErr, &exitErr) {
		return nil
	}
	return s.waitErr
}

func (s *Session) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&s.nextID, 1)
	ch := make(chan rpcResult, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	if err := s.writeMsg(method, &id, params); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, ctx.Err()
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		return res.result, nil
	}
}

func (s *Session) notify(method string, params any) error {
	return s.writeMsg(method, nil, params)
}

func (s *Session) writeMsg(method string, id *int64, params any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if id != nil {
		msg["id"] = *id
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.enc.Encode(msg)
}

func (s *Session) readLoop(stdout io.Reader) {
	reader := bufio.NewReaderSize(stdout, 1024*1024)
	for {
		line, err := reader.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			s.handle(line)
		}
		if err != nil {
			s.failPending(err)
			return
		}
	}
}

func (s *Session) drain(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		log.Printf("acp: %s", scanner.Text())
	}
}

func (s *Session) failPending(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ch := range s.pending {
		select {
		case ch <- rpcResult{err: err}:
		default:
		}
		delete(s.pending, id)
	}
}

func (s *Session) handle(line []byte) {
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if json.Unmarshal(line, &msg) != nil {
		return
	}
	if msg.Method != "" && isRequestID(msg.ID) {
		s.handleRequest(msg.ID, msg.Method, msg.Params)
		return
	}
	if msg.Method != "" {
		s.handleNote(msg.Method, msg.Params)
		return
	}
	if !isRequestID(msg.ID) {
		return
	}
	var id int64
	if json.Unmarshal(msg.ID, &id) != nil {
		return
	}
	s.mu.Lock()
	ch := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if ch == nil {
		return
	}
	if msg.Error != nil {
		ch <- rpcResult{err: msg.Error}
		return
	}
	ch <- rpcResult{result: msg.Result}
}

func isRequestID(id json.RawMessage) bool {
	text := strings.TrimSpace(string(id))
	return text != "" && text != "null"
}

func (s *Session) handleNote(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	var note struct {
		Update struct {
			SessionUpdate string `json:"sessionUpdate"`
			Content       struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"update"`
	}
	if json.Unmarshal(params, &note) != nil {
		return
	}
	if note.Update.SessionUpdate != "agent_message_chunk" || note.Update.Content.Text == "" {
		return
	}
	s.mu.Lock()
	s.assistant.WriteString(note.Update.Content.Text)
	s.mu.Unlock()
}

func (s *Session) handleRequest(id json.RawMessage, method string, params json.RawMessage) {
	if strings.HasPrefix(method, "fs/") || strings.HasPrefix(method, "terminal/") {
		s.replyError(id, -32601, "方法不存在")
		s.denyOnce(fmt.Errorf("%s", method))
		return
	}
	if method != "session/request_permission" {
		s.replyError(id, -32601, "方法不存在")
		return
	}
	kind, paths, options, err := decodePermission(params)
	if err != nil {
		s.reply(id, rejectOutcome(options))
		s.denyOnce(err)
		return
	}
	choice, err := SelectPermission(s.workdir, kind, paths, options)
	if err != nil {
		s.reply(id, rejectOutcome(options))
		s.denyOnce(err)
		return
	}
	s.reply(id, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": choice}})
}

func (s *Session) denyOnce(err error) {
	select {
	case s.denied <- err:
	default:
	}
}

func (s *Session) reply(id json.RawMessage, result any) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *Session) replyError(id json.RawMessage, code int, message string) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.enc.Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   &rpcError{Code: code, Message: message},
	})
}

func rejectOutcome(options []PermissionChoice) any {
	for _, opt := range options {
		if opt.Kind == "reject_once" && opt.ID != "" {
			return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": opt.ID}}
		}
	}
	return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
}

func decodePermission(params json.RawMessage) (string, []string, []PermissionChoice, error) {
	var body struct {
		ToolCall struct {
			Kind      string `json:"kind"`
			Locations []struct {
				Path string `json:"path"`
			} `json:"locations"`
			RawInput map[string]any `json:"rawInput"`
		} `json:"toolCall"`
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	if err := json.Unmarshal(params, &body); err != nil {
		return "", nil, nil, err
	}
	var paths []string
	for _, loc := range body.ToolCall.Locations {
		if loc.Path != "" {
			paths = append(paths, loc.Path)
		}
	}
	collectPaths(body.ToolCall.RawInput, &paths)
	options := make([]PermissionChoice, 0, len(body.Options))
	for _, opt := range body.Options {
		options = append(options, PermissionChoice{ID: opt.OptionID, Kind: opt.Kind})
	}
	return body.ToolCall.Kind, paths, options, nil
}

func collectPaths(v any, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		for _, item := range t {
			collectPaths(item, out)
		}
	case []any:
		for _, item := range t {
			collectPaths(item, out)
		}
	case string:
		if filepathIsPath(t) {
			*out = append(*out, t)
		}
	}
}

func filepathIsPath(text string) bool {
	return strings.ContainsAny(text, `/\`)
}
