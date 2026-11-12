// 独立模块：长轮询 Telegram，把待评审出站转成消息，并把四条中文指令发给看板已有的人类接口。
// 不进入 ingot 的启动路径。
//
//	TELEGRAM_BOT_TOKEN        机器人令牌
//	TELEGRAM_CHAT_ID          可选。留空时用第一条进来的消息确定聊天
//	INGOT_URL             看板地址，默认 http://127.0.0.1:18721
//	INGOT_DEVICE_TOKEN    可选。不在本机时填 atd_ 设备令牌
//	LISTEN                    接收待评审出站的地址，默认 127.0.0.1:18731
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"
)

func main() {
	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if token == "" {
		log.Fatal("缺少 TELEGRAM_BOT_TOKEN")
	}
	base := strings.TrimSpace(os.Getenv("INGOT_URL"))
	if base == "" {
		base = "http://127.0.0.1:18721"
	}
	listen := strings.TrimSpace(os.Getenv("LISTEN"))
	if listen == "" {
		listen = "127.0.0.1:18731"
	}
	bot := &telegram{
		token:  token,
		chatID: strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID")),
		board: Board{
			BaseURL: base,
			Token:   strings.TrimSpace(os.Getenv("INGOT_DEVICE_TOKEN")),
		},
		client: &http.Client{Timeout: 35 * time.Second},
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	mux := http.NewServeMux()
	mux.HandleFunc("/", bot.receiveReview)
	srv := &http.Server{Addr: listen, Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	go bot.poll(ctx)

	log.Printf("Telegram 适配器监听 %s，看板 %s", listen, base)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

type telegram struct {
	token  string
	chatID string
	mu     sync.Mutex
	board  Board
	client *http.Client
}

func (t *telegram) receiveReview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只接受 POST", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		TaskID int64  `json:"task_id"`
		Title  string `json:"title"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "载荷不是 JSON", http.StatusBadRequest)
		return
	}
	if body.Status != "review" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	text := fmt.Sprintf("待评审 #%d\n%s\n通过 %d\n驳回 %d 原因\n备注 %d 文本",
		body.TaskID, body.Title, body.TaskID, body.TaskID, body.TaskID)
	if err := t.send(r.Context(), text); err != nil {
		log.Printf("转发待评审失败: %v", err)
		http.Error(w, "转发失败", http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (t *telegram) poll(ctx context.Context) {
	var offset int64
	for ctx.Err() == nil {
		updates, err := t.getUpdates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("读取 Telegram 更新失败: %v", stripURL(err))
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		for _, up := range updates {
			if up.UpdateID >= offset {
				offset = up.UpdateID + 1
			}
			if up.Message.Text == "" {
				continue
			}
			t.remember(up.Message.Chat.ID)
			t.handle(ctx, up.Message.Text)
		}
	}
}

func (t *telegram) handle(ctx context.Context, text string) {
	action, err := ParseCommand(text)
	if err != nil {
		_ = t.send(ctx, err.Error())
		return
	}
	if err := t.board.Do(ctx, action); err != nil {
		_ = t.send(ctx, err.Error())
		return
	}
	_ = t.send(ctx, "已处理")
}

func (t *telegram) remember(id int64) {
	t.mu.Lock()
	if t.chatID == "" {
		t.chatID = strconv.FormatInt(id, 10)
	}
	t.mu.Unlock()
}

func (t *telegram) chat() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.chatID
}

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  struct {
		Text string `json:"text"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message"`
}

func (t *telegram) getUpdates(ctx context.Context, offset int64) ([]tgUpdate, error) {
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?timeout=25&offset=%d", t.token, offset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var env struct {
		OK     bool       `json:"ok"`
		Result []tgUpdate `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, err
	}
	if !env.OK {
		return nil, errors.New("Telegram 拒绝了 getUpdates")
	}
	return env.Result, nil
}

func (t *telegram) send(ctx context.Context, text string) error {
	chatID := t.chat()
	if chatID == "" {
		return errors.New("还没有聊天对象，先给机器人发一条消息")
	}
	buf, err := json.Marshal(map[string]string{"chat_id": chatID, "text": text})
	if err != nil {
		return err
	}
	endpoint := "https://api.telegram.org/bot" + t.token + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return stripURL(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("Telegram 返回 %d", resp.StatusCode)
	}
	return nil
}

func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
