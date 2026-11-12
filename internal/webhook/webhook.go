// Package webhook 在任务进入待评审时，向用户配置的地址 POST 一次。
// 各家 IM 的协议转换留在进程外，这里只发出统一 JSON。
package webhook

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mst8222/ingot-board/internal/domain"
)

// postTimeout 单次出站的上限。超时只记日志，不碰任务状态。
const postTimeout = 10 * time.Second

var client = &http.Client{
	Timeout: postTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

type payload struct {
	TaskID int64  `json:"task_id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// Validate 检查用户写下的通知地址。
// 空字符串表示关闭。远程地址必须是 https。本机回环允许 http，给参考适配器收同一条出站。
func Validate(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return "", invalidURL()
	}
	switch u.Scheme {
	case "https":
		return raw, nil
	case "http":
		host := strings.ToLower(u.Hostname())
		if host == "127.0.0.1" || host == "localhost" {
			return raw, nil
		}
	}
	return "", invalidURL()
}

func invalidURL() error {
	return errors.Join(domain.ErrInvalidInput, errors.New("待评审通知地址必须是 https，或本机回环的 http"))
}

// Post 向 rawURL 发送一次待评审通知。失败只记日志。
func Post(rawURL string, taskID int64, title string) {
	body, err := json.Marshal(payload{TaskID: taskID, Title: title, Status: "review"})
	if err != nil {
		log.Printf("待评审通知编码失败 task=%d: %v", taskID, err)
		return
	}
	req, err := http.NewRequest(http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		log.Printf("待评审通知构造失败 task=%d: %v", taskID, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("待评审通知发送失败 task=%d: %v", taskID, netErr(err))
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("待评审通知被拒绝 task=%d status=%d", taskID, resp.StatusCode)
	}
}

// netErr 去掉错误里的请求 URL，避免把带令牌的地址打进日志。
func netErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
