//go:build !windows

package cli

import (
	"os/exec"
	"runtime"
)

// openBrowser 在非 Windows 平台调起默认浏览器。
func openBrowser(url string) error {
	open := "xdg-open"
	if runtime.GOOS == "darwin" {
		open = "open"
	}
	return exec.Command(open, url).Start()
}
