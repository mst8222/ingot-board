package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// SystemOne 是一个决策模型的 System One 端点。
type SystemOne struct {
	Name   string
	URL    string
	Model  string
	APIKey string
}

// Chat 是 OpenAI 兼容的对话分类端点，只在决策模型都没连上时使用。
type Chat struct {
	Name      string
	BaseURL   string
	Model     string
	APIKey    string
	MaxTokens int
	// RequireModels 为真时先 GET {BaseURL}/models，失败就跳过，不再发分类请求。
	RequireModels bool
	// Timeout 覆盖本次对话请求的超时。探测仍用 Config.HTTP 的短超时。
	Timeout time.Duration
}

// Config 分类时要试的端点，顺序就是经济顺序。
type Config struct {
	SystemOne []SystemOne
	Chat      []Chat
	HTTP      *http.Client
}

// Decide 依次试决策模型、对话小模型，最后用关键词。
func Decide(ctx context.Context, title, description, workdir string, cfg Config) Decision {
	state := strings.TrimSpace(title + "\n" + description + "\n" + workdir)
	client := cfg.HTTP
	if client == nil {
		client = &http.Client{Timeout: 800 * time.Millisecond}
	}
	for _, ep := range cfg.SystemOne {
		cat, tier, band, err := askSystemOne(ctx, client, ep, state)
		if err != nil {
			continue
		}
		d, err := Normalize(cat, tier)
		if err != nil {
			continue
		}
		d.Reason = "分类器 " + ep.Name
		d.CostBand = normalizeBand(band)
		return d
	}
	for _, ep := range cfg.Chat {
		if ep.RequireModels && !modelsUp(ctx, client, ep.BaseURL) {
			continue
		}
		chatClient := client
		if ep.Timeout > 0 {
			chatClient = &http.Client{Timeout: ep.Timeout}
		}
		cat, tier, band, err := askChat(ctx, chatClient, ep, state)
		if err != nil {
			continue
		}
		d, err := Normalize(cat, tier)
		if err != nil {
			continue
		}
		d.Reason = "分类器 " + ep.Name
		d.CostBand = normalizeBand(band)
		return d
	}
	return Keywords(title + "\n" + description)
}

// CostBand 是 032 总管唯一允许模型参与预算决策的字段。
// 模型只能给 low/mid/high，不能给 Agent 名，也不能直接改档位。
const (
	CostBandLow  = "low"
	CostBandMid  = "mid"
	CostBandHigh = "high"
)

func normalizeBand(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case CostBandLow, CostBandMid, CostBandHigh:
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ""
	}
}

func askSystemOne(ctx context.Context, client *http.Client, ep SystemOne, state string) (string, string, string, error) {
	body := map[string]any{
		"model": ep.Model,
		"state": state,
		"questions": map[string]any{
			"category": map[string]any{
				"type":         "choice",
				"instructions": "What kind of work is this task?",
				"criteria": map[string]any{
					"code_tool": "Build or fix a software tool, library, script, or bug",
					"code_game": "Program a game",
					"copy":      "Write copy, docs, or localization text",
					"local_ops": "Install, configure, or tidy files on this machine",
				},
			},
			"tier": map[string]any{
				"type":         "choice",
				"instructions": "Which execution tier fits? floor means DeepSeek V4.1 Flash. large means a stronger model. small is only for copy or short local ops.",
				"criteria": map[string]any{
					"small": "Copy or a short local operation",
					"floor": "Ordinary programming on DeepSeek V4.1 Flash",
					"large": "A large repository, architecture, or a game system",
				},
			},
			"cost_band": map[string]any{
				"type":         "choice",
				"instructions": "How expensive will this task be? Only estimate cost, never name an agent.",
				"criteria": map[string]any{
					"low":  "Short, single-file, few tool calls",
					"mid":  "A normal multi-step change",
					"high": "A large refactor, long investigation or many tool calls",
				},
			},
		},
	}
	raw, err := postJSON(ctx, client, ep.URL, ep.APIKey, body)
	if err != nil {
		return "", "", "", err
	}
	cat, tier := choice(raw, "category"), choice(raw, "tier")
	if cat == "" || tier == "" {
		return "", "", "", fmt.Errorf("决策响应缺少 choice")
	}
	return cat, tier, choice(raw, "cost_band"), nil
}

func choice(raw []byte, name string) string {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return ""
	}
	answers, _ := root["answers"].(map[string]any)
	if answers == nil {
		if data, ok := root["data"].(map[string]any); ok {
			answers, _ = data["answers"].(map[string]any)
		}
	}
	item, _ := answers[name].(map[string]any)
	got, _ := item["choice"].(string)
	return got
}

// modelsUp 判断 OpenAI 兼容服务是否在监听。BaseURL 形如 http://127.0.0.1:8080/v1。
func modelsUp(ctx context.Context, client *http.Client, baseURL string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func askChat(ctx context.Context, client *http.Client, ep Chat, state string) (string, string, string, error) {
	maxTokens := ep.MaxTokens
	if maxTokens <= 0 || maxTokens > 64 {
		maxTokens = 64
	}
	body := map[string]any{
		"model": ep.Model,
		"messages": []map[string]string{{
			"role":    "user",
			"content": "Classify the task. Reply with JSON only: {\"category\":\"code_tool|code_game|copy|local_ops\",\"tier\":\"small|floor|large\",\"cost_band\":\"low|mid|high\"}. Never name an agent.\n" + state,
		}},
		"max_tokens": maxTokens,
	}
	url := strings.TrimRight(ep.BaseURL, "/") + "/chat/completions"
	raw, err := postJSON(ctx, client, url, ep.APIKey, body)
	if err != nil {
		return "", "", "", err
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Choices) == 0 {
		return "", "", "", fmt.Errorf("对话分类没有内容")
	}
	text := parsed.Choices[0].Message.Content
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	var out struct {
		Category string `json:"category"`
		Tier     string `json:"tier"`
		CostBand string `json:"cost_band"`
		// AgentName 故意解析但绝不返回：模型即便塞进来也会被丢弃（032 硬约束）。
		AgentName string `json:"agent"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &out); err != nil {
		return "", "", "", err
	}
	return out.Category, out.Tier, out.CostBand, nil
}

func postJSON(ctx context.Context, client *http.Client, url, apiKey string, body any) ([]byte, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusPaymentRequired {
		return nil, fmt.Errorf("额度不足: %s", resp.Status)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("分类请求失败: %s", resp.Status)
	}
	return raw, nil
}

// NetworkConfig 生产环境要试的分类端点。
// 本机没开的端口会快速失败，然后试下一项。
func NetworkConfig() Config {
	cfg := Config{HTTP: &http.Client{Timeout: 800 * time.Millisecond}}
	kevURL := getenv("AGENT_TODO_KEV_URL", "http://127.0.0.1:8008/v1/systemone")
	cfg.SystemOne = append(cfg.SystemOne, SystemOne{Name: "kev-4b", URL: kevURL, Model: "kev-latest"})
	if u := os.Getenv("AGENT_TODO_D3_URL"); u != "" {
		cfg.SystemOne = append(cfg.SystemOne, SystemOne{Name: "d3-flash", URL: u, Model: "vllm-sr/d3-flash"})
	} else if key := os.Getenv("AGENT_TODO_D3_API_KEY"); key != "" {
		cfg.SystemOne = append(cfg.SystemOne, SystemOne{
			Name: "d3-flash", URL: "https://decision.vllm-sr.ai/v1/systemone",
			Model: "vllm-sr/d3-flash", APIKey: key,
		})
	} else {
		cfg.SystemOne = append(cfg.SystemOne, SystemOne{
			Name: "d3-flash", URL: "http://127.0.0.1:8096/v1/systemone", Model: "vllm-sr/d3-flash",
		})
	}
	semif := getenv("AGENT_TODO_SEMIF_URL", "http://127.0.0.1:3000/v1/systemone")
	cfg.SystemOne = append(cfg.SystemOne, SystemOne{Name: "semif", URL: semif, Model: "openjev"})
	local := getenv("AGENT_TODO_LOCAL_URL", "http://127.0.0.1:8080/v1")
	cfg.Chat = append(cfg.Chat, Chat{
		Name: "Ternary Bonsai 2 27B v3", BaseURL: local, Model: "bonsai2-27b",
		MaxTokens: 64, RequireModels: true, Timeout: 8 * time.Second,
	})
	if key := os.Getenv("AGENT_TODO_QWEN_API_KEY"); key != "" {
		cfg.Chat = append(cfg.Chat, Chat{
			Name: "qwen-portal", BaseURL: "https://portal.qwen.ai/v1",
			Model: "coder-model", APIKey: key, MaxTokens: 64,
		})
	}
	if key := os.Getenv("DEEPSEEK_API_KEY"); key != "" {
		cfg.Chat = append(cfg.Chat, Chat{
			Name: "deepseek-flash", BaseURL: "https://api.deepseek.com/v1",
			Model: "deepseek-flash", APIKey: key, MaxTokens: 64,
		})
	}
	return cfg
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
