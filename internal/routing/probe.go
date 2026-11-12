package routing

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ProbeDeepSeek 发一次极短补全，判断 DeepSeek 额度。
// 返回 ok、exhausted，或在网络失败时返回 unknown。
func ProbeDeepSeek(ctx context.Context) string {
	key := os.Getenv("DEEPSEEK_API_KEY")
	if key == "" {
		key = piDeepSeekKey()
	}
	if key == "" {
		return domainQuotaUnknown()
	}
	client := &http.Client{Timeout: 8 * time.Second}
	_, err := postJSON(ctx, client, "https://api.deepseek.com/v1/chat/completions", key, map[string]any{
		"model": "deepseek-flash",
		"messages": []map[string]string{{
			"role":    "user",
			"content": "ping",
		}},
		"max_tokens": 1,
	})
	if err == nil {
		return "ok"
	}
	if err != nil && (contains(err.Error(), "402") || contains(err.Error(), "401") || contains(err.Error(), "额度不足")) {
		return "exhausted"
	}
	return domainQuotaUnknown()
}

func domainQuotaUnknown() string { return "unknown" }

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexFold(s, sub) >= 0
}

func piDeepSeekKey() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "auth.json"))
	if err != nil {
		return ""
	}
	var doc struct {
		DeepSeek struct {
			Key string `json:"key"`
		} `json:"deepseek"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	return doc.DeepSeek.Key
}
