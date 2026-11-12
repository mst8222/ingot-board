package hosts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mst8222/ingot-board/internal/domain"
)

const (
	KindRunner = "runner"
	KindMCP    = "mcp"

	WriterJSON = "json"
	WriterTOML = "toml"

	// PiModel 是 Pi 必须锁定的模型。低于这一档不能执行编程任务。
	PiModel = "deepseek/deepseek-flash"
)

// Spec 是一个执行端的全部元数据。
//
// 通道相关的三项是函数字段而不是字符串：MCP 配置路径、ACP 可执行文件候选
// 都要按当前用户目录与环境变量在运行时求值，静态化会丢掉 Windows 上的
// %APPDATA% / %LOCALAPPDATA%。把函数放在数据里，消费方就不用再按名字写 switch。
type Spec struct {
	Name     string
	Kind     string
	Serves   string
	CostRank int
	// Label 报告里用的名字，省略时用 Name。
	Label string

	MCPPaths      func(Layout) []string
	BinCandidates func(Layout) []string
	// MissingHint 只在 mcp 通道不可用时给出针对性的缺失说明。
	MissingHint func(Layout) string
	// AlwaysOn 表示无需本机配置入口也视为可用（Codex 走 TOML 合并写入）。
	AlwaysOn bool

	AdapterBin   string
	LaunchArgs   []string
	SessionModel string

	// Writer 决定 connect 往哪种配置里合并：json=MCP 配置，toml=Codex config.toml。
	Writer string

	DisplayName string
	DocAnchor   string
	Guide       string
	// Capabilities 静态能力标签，供 032 总管的硬门禁按任务类别过滤。
	// 不写进 agents 表：注册表是执行端能力的单一数据源，避免两处漂移。
	Capabilities []string
}

var (
	allCaps   = []string{domain.CategoryCodeTool, domain.CategoryCodeGame, domain.CategoryCopy, domain.CategoryLocalOps}
	floorCaps = []string{domain.CategoryCodeTool, domain.CategoryCopy, domain.CategoryLocalOps}
	cheapCaps = []string{domain.CategoryCopy, domain.CategoryLocalOps}
)

// 成本越小越先被选。Pi 是编程底线，排在编辑器前面。
var registry = []Spec{
	{
		Name: "pi", Kind: KindRunner, Serves: domain.ServesFloor, CostRank: 10,
		Capabilities: floorCaps,
		BinCandidates: piBins, AdapterBin: "pi-acp", SessionModel: PiModel,
		Writer: WriterJSON, DisplayName: "Pi Agent", DocAnchor: "channel-b",
		Guide: "Runner 通道：PATH 上需有 pi-acp，用 ingot-agent runner --adapter pi 启动",
	},
	{
		Name: "opencode", Kind: KindRunner, Serves: domain.ServesSmall, CostRank: 15,
		Capabilities: cheapCaps,
		BinCandidates: opencodeBins, AdapterBin: "opencode", LaunchArgs: []string{"acp"},
		Writer: WriterJSON, DisplayName: "OpenCode", DocAnchor: "channel-b",
		Guide: "Runner 通道：需 opencode-cli.exe，用 ingot-agent runner --adapter opencode 启动",
	},
	{
		Name: "cursor", Kind: KindMCP, Serves: domain.ServesLarge, CostRank: 40,
		Capabilities: allCaps,
		MCPPaths: homePaths(".cursor", "mcp.json"), Writer: WriterJSON,
		DisplayName: "Cursor", DocAnchor: "channel-a",
		Guide: "MCP 通道：connect 写入 ~/.cursor/mcp.json，并生成 .cursor/rules/ingot.mdc",
	},
	{
		Name: "trae", Kind: KindMCP, Serves: domain.ServesLarge, CostRank: 40,
		Capabilities: allCaps,
		MCPPaths: appDataPaths("Trae CN", "User", "mcp.json"), Writer: WriterJSON,
		DisplayName: "Trae CN", DocAnchor: "channel-a",
		Guide: "MCP 通道：connect 写入 %APPDATA%/Trae CN/User/mcp.json",
	},
	{
		Name: "qoder", Kind: KindMCP, Serves: domain.ServesLarge, CostRank: 40,
		Capabilities: allCaps,
		MCPPaths: appDataPaths("Qoder", "SharedClientCache", "mcp.json"), Writer: WriterJSON,
		DisplayName: "Qoder", DocAnchor: "channel-a",
		Guide: "MCP 通道：connect 写入 %APPDATA%/Qoder/SharedClientCache/mcp.json",
	},
	{
		Name: "workbuddy", Kind: KindMCP, Serves: domain.ServesLarge, CostRank: 40,
		Capabilities: allCaps,
		MCPPaths: func(l Layout) []string {
			// .workbuddy 是当前客户端读取的目录，.codebuddy 是旧版遗留，两个都写。
			return []string{
				filepath.Join(l.Home, ".workbuddy", "mcp.json"),
				filepath.Join(l.Home, ".codebuddy", "mcp.json"),
			}
		},
		Writer: WriterJSON, DisplayName: "WorkBuddy", DocAnchor: "channel-a",
		Guide: "MCP 通道：connect 同时写 ~/.workbuddy/mcp.json 与旧版 ~/.codebuddy/mcp.json",
	},
	{
		Name: "harness", Kind: KindMCP, Serves: domain.ServesLarge, CostRank: 40,
		Capabilities: allCaps,
		Label: "DeepSeek Harness",
		MCPPaths: func(l Layout) []string {
			if path := FindHarnessMCP(l.AppData); path != "" {
				return []string{path}
			}
			return nil
		},
		MissingHint: func(l Layout) string {
			return fmt.Sprintf("未找到可写的 MCP 配置（期望 %s）",
				filepath.Join(l.AppData, "DeepSeek Harness", "mcp.json"))
		},
		Writer: WriterJSON, DisplayName: "DeepSeek Harness", DocAnchor: "channel-a",
		Guide: "MCP 通道：需先存在 %APPDATA%/DeepSeek Harness/mcp.json，否则登记后禁用",
	},
	{
		Name: "claude", Kind: KindMCP, Serves: domain.ServesLarge, CostRank: 80,
		Capabilities: allCaps,
		MCPPaths: homePaths(".claude.json"), Writer: WriterJSON,
		DisplayName: "Claude Code", DocAnchor: "channel-a",
		Guide: "MCP 通道：connect 写入 ~/.claude.json，规则合并进 ~/.claude/CLAUDE.md",
	},
	{
		Name: "codex", Kind: KindMCP, Serves: domain.ServesLarge, CostRank: 80,
		Capabilities: allCaps,
		AlwaysOn: true, Writer: WriterTOML,
		DisplayName: "Codex", DocAnchor: "channel-a",
		Guide: "MCP 通道：connect 合并写入 ~/.codex/config.toml 与 ~/.codex/AGENTS.md",
	},
}

func homePaths(parts ...string) func(Layout) []string {
	return func(l Layout) []string {
		return []string{filepath.Join(append([]string{l.Home}, parts...)...)}
	}
}

func appDataPaths(parts ...string) func(Layout) []string {
	return func(l Layout) []string {
		return []string{filepath.Join(append([]string{l.AppData}, parts...)...)}
	}
}

func opencodeBins(_ Layout) []string {
	return []string{
		`F:\Programs\OpenCode\opencode-cli.exe`,
		filepath.Join(os.Getenv("LOCALAPPDATA"), "OpenCode", "opencode-cli.exe"),
		"opencode",
	}
}

func piBins(_ Layout) []string { return []string{"pi-acp"} }

// All 返回注册表副本，顺序即成本序。
func All() []Spec {
	out := make([]Spec, len(registry))
	copy(out, registry)
	return out
}

// Lookup 按稳定标识查执行端。
func Lookup(name string) (Spec, bool) {
	for _, s := range registry {
		if s.Name == name {
			return s, true
		}
	}
	return Spec{}, false
}

// LabelOf 返回报告与列表里用的名字。
func LabelOf(s Spec) string {
	if s.Label != "" {
		return s.Label
	}
	return s.Name
}

// MCPPathsFor 安全取值：runner 类没有配置路径函数。
func MCPPathsFor(l Layout, s Spec) []string {
	if s.MCPPaths == nil {
		return nil
	}
	return s.MCPPaths(l)
}

// BinCandidatesFor 安全取值：mcp 类没有可执行文件候选函数。
func BinCandidatesFor(l Layout, s Spec) []string {
	if s.BinCandidates == nil {
		return nil
	}
	return s.BinCandidates(l)
}

// ResolveBin 按顺序找第一个存在的 ACP 可执行文件。
func ResolveBin(l Layout, s Spec) string {
	for _, candidate := range BinCandidatesFor(l, s) {
		if candidate != "" && l.Probe(candidate) {
			return candidate
		}
	}
	return ""
}

// DefaultChannel 判定本机有没有让这个执行端真正干活的通道，不可用时要说清缺什么。
func DefaultChannel(l Layout, s Spec) (bool, string) {
	if s.Kind == KindRunner {
		if ResolveBin(l, s) != "" {
			return true, ""
		}
		candidates := append([]string(nil), BinCandidatesFor(l, s)...)
		for i := range candidates {
			candidates[i] = strings.TrimSpace(candidates[i])
		}
		return false, fmt.Sprintf("缺少 ACP 可执行文件 %s", strings.Join(candidates, " / "))
	}
	if s.AlwaysOn {
		return true, ""
	}
	if len(MCPPathsFor(l, s)) > 0 {
		return true, ""
	}
	if s.MissingHint != nil {
		return false, s.MissingHint(l)
	}
	return false, "没有可写入的 MCP 配置入口"
}

// HostInfo 是支持列表对外的只读视图。
type HostInfo struct {
	Name          string `json:"name"`
	Label         string `json:"label"`
	DisplayName   string `json:"display_name"`
	Kind          string `json:"kind"`
	Serves        string `json:"serves"`
	CostRank      int    `json:"cost_rank"`
	Available     bool   `json:"available"`
	MissingReason string `json:"missing_reason"`
	Guide         string `json:"guide"`
	DocAnchor     string `json:"doc_anchor"`
}

// HostInfoList 每次调用都按当前本机情况重算可用性与缺失原因。
// enabled 为 nil 时只看通道，用于尚未登记执行端的场景。
func HostInfoList(l Layout, enabled map[string]bool) []HostInfo {
	out := make([]HostInfo, 0, len(registry))
	for _, s := range registry {
		ok, reason := DefaultChannel(l, s)
		available := ok
		if enabled != nil && !enabled[s.Name] {
			available = false
			if reason == "" {
				reason = "执行端未启用，重跑 ingot-agent connect 后按本机情况重新判定"
			}
		}
		out = append(out, HostInfo{
			Name: s.Name, Label: LabelOf(s), DisplayName: s.DisplayName,
			Kind: s.Kind, Serves: s.Serves, CostRank: s.CostRank,
			Available: available, MissingReason: reason,
			Guide: s.Guide, DocAnchor: s.DocAnchor,
		})
	}
	return out
}

// MarkdownTable 生成 README 支持列表标记块的内容，测试据此保证文档不漂移。
func MarkdownTable() string {
	var b strings.Builder
	b.WriteString("| 执行端 | 通道 | 档位 | 成本序 | 接入指引 |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, s := range registry {
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %s |\n",
			LabelOf(s), s.Kind, s.Serves, s.CostRank, s.Guide)
	}
	return strings.TrimRight(b.String(), "\n")
}
