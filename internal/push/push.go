// Package push 在任务进入待评审时向已配对手机发 Web Push。
package push

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/mst8222/ingot-board/internal/store"
)

// Sender 持有 VAPID 密钥并读取订阅。
type Sender struct {
	dir   string
	store *store.Store
	pub   string
	priv  string
}

type vapidFile struct {
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

// New 读取或生成数据目录里的 VAPID 密钥。
func New(dir string, st *store.Store) (*Sender, error) {
	path := filepath.Join(dir, "vapid.json")
	raw, err := os.ReadFile(path)
	var keys vapidFile
	if err == nil {
		if err := json.Unmarshal(raw, &keys); err != nil {
			return nil, err
		}
	} else {
		priv, pub, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			return nil, err
		}
		keys = vapidFile{PublicKey: pub, PrivateKey: priv}
		buf, err := json.Marshal(keys)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, buf, 0o600); err != nil {
			return nil, err
		}
	}
	return &Sender{dir: dir, store: st, pub: keys.PublicKey, priv: keys.PrivateKey}, nil
}

// PublicKey 给浏览器订阅用。
func (s *Sender) PublicKey() string { return s.pub }

// NotifyReview 向全部订阅发送一次待评审通知。没有订阅时直接返回。
func (s *Sender) NotifyReview(taskID int64, title string) {
	subs, err := s.store.ListPushSubscriptions()
	if err != nil {
		log.Printf("读取推送订阅失败: %v", err)
		return
	}
	if len(subs) == 0 {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"title":   "待评审",
		"body":    title,
		"task_id": taskID,
	})
	for _, sub := range subs {
		resp, err := webpush.SendNotification(payload, &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
		}, &webpush.Options{
			Subscriber:      "mailto:phone@ingotboard.local",
			VAPIDPublicKey:  s.pub,
			VAPIDPrivateKey: s.priv,
			TTL:             60,
		})
		if err != nil {
			log.Printf("发送待评审通知失败: %v", err)
			continue
		}
		if resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound {
			_ = s.store.DeletePushSubscriptionByEndpoint(sub.Endpoint)
		}
		resp.Body.Close()
	}
}
