//go:build windows

package cli

import (
	"os/exec"
	"syscall"
)

// openBrowser 在 Windows 上用 rundll32 调起默认浏览器，窗口独立不残留控制台。
func openBrowser(url string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}
