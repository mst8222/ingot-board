package phone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Status 本机 Tailscale 节点。
type Status struct {
	DNSName string
	IPv4    string
	Online  bool
}

// Controller 打开证书和 Funnel。测试注入假实现。
type Controller interface {
	Installed() bool
	Status(ctx context.Context) (Status, error)
	Cert(ctx context.Context, host, certFile, keyFile string) error
	Funnel(ctx context.Context, port int, on bool) error
}

// CLI 调用本机 tailscale 可执行文件。
type CLI struct {
	Bin string
}

// NewCLI 使用 PATH 里的 tailscale。找不到时 Installed 为 false。
func NewCLI() CLI {
	bin, err := exec.LookPath("tailscale")
	if err != nil {
		return CLI{}
	}
	return CLI{Bin: bin}
}

// Installed 表示本机能启动 tailscale。
func (c CLI) Installed() bool { return c.Bin != "" }

// Status 读取本节点的 MagicDNS 名和 IPv4。
func (c CLI) Status(ctx context.Context) (Status, error) {
	if c.Bin == "" {
		return Status{}, errors.New("本机没有 tailscale，无法对手机开放远程连接")
	}
	out, err := exec.CommandContext(ctx, c.Bin, "status", "--json").Output()
	if err != nil {
		return Status{}, fmt.Errorf("读取 Tailscale 状态失败: %w", err)
	}
	var raw struct {
		BackendState string `json:"BackendState"`
		Self         struct {
			DNSName      string   `json:"DNSName"`
			Online       bool     `json:"Online"`
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Self"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return Status{}, fmt.Errorf("解析 Tailscale 状态失败: %w", err)
	}
	var ip4 string
	for _, ip := range raw.Self.TailscaleIPs {
		if !strings.Contains(ip, ":") {
			ip4 = ip
			break
		}
	}
	dns := strings.TrimSuffix(raw.Self.DNSName, ".")
	if ip4 == "" || dns == "" || raw.BackendState != "Running" || !raw.Self.Online {
		return Status{}, errors.New("Tailscale 未登录或还没有 IPv4 地址")
	}
	return Status{DNSName: dns, IPv4: ip4, Online: true}, nil
}

// Cert 把节点证书写到指定文件。
func (c CLI) Cert(ctx context.Context, host, certFile, keyFile string) error {
	if c.Bin == "" {
		return errors.New("本机没有 tailscale，无法对手机开放远程连接")
	}
	cmd := exec.CommandContext(ctx, c.Bin, "cert", "--cert-file", certFile, "--key-file", keyFile, host)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("申请 Tailscale 证书失败: %s", msg)
	}
	return nil
}

// Funnel 把公网 HTTPS 转到本机的手机端口，或关掉这一条。
func (c CLI) Funnel(ctx context.Context, port int, on bool) error {
	if c.Bin == "" {
		return errors.New("本机没有 tailscale，无法对手机开放远程连接")
	}
	var cmd *exec.Cmd
	if on {
		cmd = exec.CommandContext(ctx, c.Bin, "funnel", "--bg", fmt.Sprintf("%d", port))
	} else {
		cmd = exec.CommandContext(ctx, c.Bin, "funnel", fmt.Sprintf("--https=%d", port), "off")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("设置 Tailscale Funnel 失败: %s", msg)
	}
	return nil
}
