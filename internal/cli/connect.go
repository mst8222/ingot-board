package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mst8222/ingot-board/internal/app"
	"github.com/mst8222/ingot-board/internal/hosts"
	"github.com/mst8222/ingot-board/internal/store"
)

// ConnectLayout 把写入限制在指定目录，测试不会碰到真实用户配置。
// 执行端元数据不再放在本文件，统一由 internal/hosts 声明。
type ConnectLayout struct {
	Home    string
	AppData string
	Exe     string
	// Probe 判定一个可执行文件是否存在。为空时：带分隔符的按路径 stat，其余按 PATH 查找。
	// 测试注入它，本机探测结果就不会影响断言。
	Probe func(string) bool
}

// hostsLayout 把 CLI 布局适配成注册表布局。ConnectLayout 的字段保持不变，
// connect_test.go 注入 Probe 的写法才能继续编译。
func (l ConnectLayout) hostsLayout() hosts.Layout {
	return hosts.Layout{Home: l.Home, AppData: l.AppData, Exe: l.Exe, ProbeFn: l.Probe}
}

// ConnectReport 是 connect 写了什么、跳过了什么。
type ConnectReport struct {
	Lines []string
}

func (r ConnectReport) String() string { return strings.Join(r.Lines, "\n") }

// ConnectLocalAgents 登记执行端，并把 MCP 与规则写进已有配置。
func ConnectLocalAgents(st *store.Store, layout ConnectLayout) (ConnectReport, error) {
	var report ConnectReport
	svc := app.New(st)
	l := layout.hostsLayout()
	tokens := map[string]string{}
	bins := map[string]string{}
	for _, host := range hosts.All() {
		ok, reason := hosts.DefaultChannel(l, host)
		token, err := ensureHost(svc, st, host, ok)
		if err != nil {
			return report, err
		}
		tokens[host.Name] = token
		line := fmt.Sprintf("已登记 %s（%s，档位 %s）", hosts.LabelOf(host), host.Kind, host.Serves)
		if !ok {
			line += "；未启用：" + reason
			report.Lines = append(report.Lines, line)
			continue
		}
		report.Lines = append(report.Lines, line)
		if host.Kind == hosts.KindRunner {
			bins[host.Name] = hosts.ResolveBin(l, host)
		}
		if host.Writer == hosts.WriterTOML {
			if err := mergeCodex(filepath.Join(layout.Home, ".codex", "config.toml"), exeOf(layout), token); err != nil {
				return report, err
			}
			report.Lines = append(report.Lines, "Codex MCP 已写入 config.toml")
			continue
		}
		for _, path := range hosts.MCPPathsFor(l, host) {
			if err := mergeMCP(path, exeOf(layout), token); err != nil {
				return report, err
			}
			report.Lines = append(report.Lines, "MCP 已写入 "+path)
		}
	}

	rule := agentRule
	writes := []struct {
		path, body string
	}{
		{filepath.Join(layout.Home, ".cursor", "rules", "ingot.mdc"), mdcHeader + rule},
		{filepath.Join(layout.Home, ".trae-cn", "user_rules", "ingot.md"), rule},
	}
	for _, w := range writes {
		if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
			return report, err
		}
		if err := os.WriteFile(w.path, []byte(w.body), 0o644); err != nil {
			return report, err
		}
		report.Lines = append(report.Lines, "规则已写入 "+w.path)
	}
	if err := mergeMarked(filepath.Join(layout.Home, ".claude", "CLAUDE.md"), rule); err != nil {
		return report, err
	}
	if err := mergeMarked(filepath.Join(layout.Home, ".codex", "AGENTS.md"), rule); err != nil {
		return report, err
	}
	report.Lines = append(report.Lines, "规则已写入 Claude 与 Codex")

	if bin := bins["pi"]; bin != "" {
		report.Lines = append(report.Lines,
			fmt.Sprintf("Pi 底线：ingot-agent runner --token %s --adapter pi --bin %s", tokens["pi"], bin))
	} else {
		report.Lines = append(report.Lines, "Pi 底线未就绪：runner 需要 PATH 上的 pi-acp，补上后重跑 connect 即可启用")
	}
	if bin := bins["opencode"]; bin != "" {
		report.Lines = append(report.Lines,
			fmt.Sprintf("OpenCode：ingot-agent runner --token %s --adapter opencode --bin %s", tokens["opencode"], bin))
	} else {
		report.Lines = append(report.Lines, "OpenCode: 未找到 opencode-cli.exe，runner 需自行指定 --bin")
	}
	return report, nil
}

// exeOf 是写进 MCP 配置的 ingot-agent 可执行文件；省略时用当前程序。
func exeOf(layout ConnectLayout) string {
	if layout.Exe != "" {
		return layout.Exe
	}
	exe, _ := os.Executable()
	return exe
}

func ensureHost(svc *app.Service, st *store.Store, host hosts.Spec, enabled bool) (string, error) {
	existing, err := st.GetAgentByName(host.Name)
	var token string
	if err == nil {
		token, err = svc.RotateToken(existing.ID)
		if err != nil {
			return "", err
		}
		existing, err = st.GetAgent(existing.ID)
		if err != nil {
			return "", err
		}
	} else {
		created, plain, err := svc.RegisterAgent(host.Name, host.Kind)
		if err != nil {
			return "", err
		}
		token = plain
		existing = created
	}
	if err := st.SetAgentProfile(existing.ID, host.Serves, host.CostRank); err != nil {
		return "", err
	}
	if err := svc.SetAgentEnabled(existing.ID, enabled); err != nil {
		return "", err
	}
	return token, nil
}

func mergeMCP(path, exe, token string) error {
	doc := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("解析 %s: %w", path, err)
		}
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers["ingot"] = map[string]any{
		"command": exe,
		"args":    []string{"mcp", "--token", token},
	}
	doc["mcpServers"] = servers
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

func mergeCodex(path, exe, token string) error {
	const begin = "# ingot:begin"
	const end = "# ingot:end"
	block := begin + "\n[mcp_servers.ingot]\ncommand = " + quoteTOML(exe) +
		"\nargs = [\"mcp\", \"--token\", " + quoteTOML(token) + "]\n" + end + "\n"
	existing := ""
	if raw, err := os.ReadFile(path); err == nil {
		existing = string(raw)
	}
	next := replaceMarked(existing, begin, end, block)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(next), 0o644)
}

func mergeMarked(path, body string) error {
	const begin = "<!-- ingot:begin -->"
	const end = "<!-- ingot:end -->"
	block := begin + "\n" + body + "\n" + end + "\n"
	existing := ""
	if raw, err := os.ReadFile(path); err == nil {
		existing = string(raw)
	}
	next := replaceMarked(existing, begin, end, block)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(next), 0o644)
}

func replaceMarked(existing, begin, end, block string) string {
	start := strings.Index(existing, begin)
	stop := strings.Index(existing, end)
	if start >= 0 && stop > start {
		stop += len(end)
		for stop < len(existing) && (existing[stop] == '\n' || existing[stop] == '\r') {
			stop++
		}
		return existing[:start] + block + existing[stop:]
	}
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}
	return existing + block
}

func quoteTOML(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
