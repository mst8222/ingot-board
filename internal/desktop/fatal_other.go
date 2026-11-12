//go:build !windows

package desktop

import (
	"fmt"
	"os"
)

// ReportFatal 报告启动失败。
func ReportFatal(err error) {
	fmt.Fprintf(os.Stderr, "桌面端启动失败: %v\n", err)
}
