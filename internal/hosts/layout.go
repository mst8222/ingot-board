// Package hosts 声明本机可接入的执行端注册表。
//
// 这是「支持哪些 Agent 对接」的唯一数据源：connect 的登记与配置写入、
// runner 的适配器分派、HTTP 支持列表、README 清单都从这里读。
// 本包只依赖 internal/domain，不能 import cli/runner/api/app，否则会形成
// runner → cli → runner 的循环依赖。
package hosts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Layout 把配置写入与可执行文件探测限制在指定目录。
// 测试注入 ProbeFn，本机探测结果就不会影响断言。
type Layout struct {
	Home    string
	AppData string
	Exe     string
	ProbeFn func(string) bool
}

// Probe 判定一个可执行文件是否存在。为空时：带分隔符的按路径 stat，其余按 PATH 查找。
func (l Layout) Probe(name string) bool {
	if l.ProbeFn != nil {
		return l.ProbeFn(name)
	}
	if strings.ContainsAny(name, `/\`) {
		info, err := os.Stat(name)
		return err == nil && !info.IsDir()
	}
	_, err := exec.LookPath(name)
	return err == nil
}

// LiveLayout 是服务端运行时按当前用户构造的探测布局。
func LiveLayout() Layout {
	home, _ := os.UserHomeDir()
	appData := os.Getenv("APPDATA")
	if appData == "" {
		if d, err := os.UserConfigDir(); err == nil {
			appData = d
		}
	}
	return Layout{Home: home, AppData: appData}
}

// FindHarnessMCP 只在 DeepSeek Harness 已经生成过 MCP 配置时返回路径，不凭空造配置。
func FindHarnessMCP(appData string) string {
	candidates := []string{
		filepath.Join(appData, "DeepSeek Harness", "mcp.json"),
		filepath.Join(appData, "DeepSeekHarness", "mcp.json"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}
