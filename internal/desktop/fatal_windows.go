//go:build windows

package desktop

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// ReportFatal 报告启动失败。
//
// 正式版以 -H windowsgui 构建，没有控制台，写 stderr 用户看不到——
// 双击后什么都不发生。所以同时弹一个系统消息框。
func ReportFatal(err error) {
	msg := fmt.Sprintf("桌面端启动失败: %v", err)
	fmt.Fprintln(os.Stderr, msg)
	text, _ := syscall.UTF16PtrFromString(msg)
	title, _ := syscall.UTF16PtrFromString(AppName)
	const mbIconError = 0x10
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(
		0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), mbIconError)
}
