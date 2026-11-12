// Package sysdialog 提供本机原生对话框能力。
//
// 看板前后端通过 HTTP 通信（桌面壳也是加载本机 HTTP 地址），
// 浏览器拿不到本机绝对路径，因此「选工作目录」必须在服务进程里弹原生对话框。
package sysdialog

import (
	"errors"

	"github.com/ncruces/zenity"
)

// PickDirectory 打开本机目录选择对话框。
//
// cancelled=true 表示用户取消；此时 path 为空且 err 为 nil。
// err 非 nil 表示对话框本身无法弹出。
func PickDirectory(initial string) (path string, cancelled bool, err error) {
	opts := []zenity.Option{
		zenity.Title("选择工作目录"),
		zenity.Directory(),
	}
	if initial != "" {
		opts = append(opts, zenity.Filename(initial))
	}
	path, err = zenity.SelectFile(opts...)
	if errors.Is(err, zenity.ErrCanceled) {
		return "", true, nil
	}
	if err != nil {
		return "", false, err
	}
	return path, false, nil
}
