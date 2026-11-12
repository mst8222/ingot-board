// Package endpoint 管理数据目录下的 endpoint.json。
//
// 看板服务（ingot 或桌面端）启动后把实际监听地址写进去，
// CLI / MCP / runner 据此发现服务 —— 桌面端用随机端口，
// 不靠这个文件，编辑器里配置的 MCP 每次重启都会连错端口。
//
// 同一份文件也是"一个数据目录只允许一个服务实例"的依据：
// 两个实例共用一个 data.db 时，SSE 推送与待评审通知都只在各自进程内流转，
// 连在 A 上的 Agent 提交了任务，B 的界面不会刷新、也不会弹通知。
package endpoint

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// FileName endpoint 文件名，固定位于数据目录根部。
const FileName = "endpoint.json"

// Info endpoint.json 的内容。
type Info struct {
	URL  string `json:"url"`
	Addr string `json:"addr"`
	PID  int    `json:"pid"`
}

// ErrNotRunning 数据目录下没有存活的看板服务。
var ErrNotRunning = errors.New("看板服务未运行")

// Path 返回数据目录对应的 endpoint 文件路径。
func Path(dataDir string) string { return filepath.Join(dataDir, FileName) }

// Write 把监听地址写入 endpoint 文件。
func Write(path, addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	// 监听在 0.0.0.0 / :: 时不能把该地址直接给客户端，否则会拿通配
	// 地址去连本机，实际连到自己都连不上。统一回退成回环地址。
	if host == "" || host == "0.0.0.0" || host == "::" {
		if ip := net.ParseIP(addr); ip != nil && ip.To4() == nil {
			host = "::1"
		} else {
			host = "127.0.0.1"
		}
	}
	info := Info{
		URL:  "http://" + net.JoinHostPort(host, addrPort(addr)),
		Addr: addr,
		PID:  os.Getpid(),
	}
	buf, err := json.Marshal(info)
	if err != nil {
		return err
	}
	// 先写临时文件再原子改名：客户端可能在任意时刻读取，
	// 半截 JSON 会让它解析失败并误判为"服务没起"。
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(buf, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Read 读取 endpoint 文件。文件不存在时返回 ErrNotRunning。
func Read(path string) (*Info, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotRunning
	}
	if err != nil {
		return nil, err
	}
	var info Info
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	return &info, nil
}

// Alive 判定 endpoint 记录的实例是否仍在服务。
//
// 只看端口能连上不够：进程崩溃后端口可能被另一个数据目录的实例复用，
// 所以要求 /health 返回的 pid 与文件记录一致。
func Alive(info *Info) bool {
	cl := &http.Client{Timeout: 2 * time.Second}
	resp, err := cl.Get(info.URL + "/api/v1/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var env struct {
		Data struct {
			PID int `json:"pid"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return false
	}
	return env.Data.PID == info.PID
}

// Discover 返回数据目录下存活实例的服务地址。
func Discover(dataDir string) (string, error) {
	path := Path(dataDir)
	info, err := Read(path)
	if err != nil {
		return "", err
	}
	if !Alive(info) {
		return "", fmt.Errorf("%w：%s 记录的实例 %s (PID %d) 已无应答", ErrNotRunning, path, info.URL, info.PID)
	}
	return info.URL, nil
}

func addrPort(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return port
}
