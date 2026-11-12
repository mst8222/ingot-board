package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Board 调用看板上已有的人类接口。Token 非空时带 atd_ 设备令牌。
type Board struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// Do 把指令译成建任务、流转或备注。
func (b *Board) Do(ctx context.Context, action Action) error {
	switch action.Kind {
	case "approve":
		return b.post(ctx, fmt.Sprintf("/api/v1/tasks/%d/transition", action.TaskID), map[string]string{"to": "approved"})
	case "reject":
		return b.post(ctx, fmt.Sprintf("/api/v1/tasks/%d/transition", action.TaskID), map[string]string{
			"to": "todo", "comment": action.Text,
		})
	case "note":
		return b.post(ctx, fmt.Sprintf("/api/v1/tasks/%d/notes", action.TaskID), map[string]string{"content": action.Text})
	case "create":
		return b.post(ctx, "/api/v1/tasks", map[string]string{"title": action.Text, "priority": "normal"})
	default:
		return errors.New("未知指令")
	}
}

func (b *Board) post(ctx context.Context, path string, body any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(b.BaseURL, "/")+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if b.Token != "" {
		req.Header.Set("Authorization", "Bearer "+b.Token)
	}
	client := b.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var env struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("看板返回 %d", resp.StatusCode)
	}
	if resp.StatusCode >= 300 || env.Code != "OK" {
		if env.Msg == "" {
			return fmt.Errorf("看板返回 %d", resp.StatusCode)
		}
		return errors.New(env.Msg)
	}
	return nil
}
