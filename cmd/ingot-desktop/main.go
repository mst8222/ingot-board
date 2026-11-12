// Command ingot-desktop 是 Ingot Board 的桌面客户端：
// Wails v3 窗口 + 进程内看板服务，不额外占用端口规划，双击即用。
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/mst8222/ingot-board/internal/desktop"
)

func main() {
	fs := flag.NewFlagSet("desktop", flag.ContinueOnError)
	addr := fs.String("addr", "", "看板服务监听地址（默认随机端口）")
	dataDir := fs.String("data-dir", "", "数据目录（默认 %AppData%/IngotBoard）")
	devURL := fs.String("dev-url", os.Getenv("ATD_DESKTOP_DEV_URL"),
		"加载前端 dev server（如 http://localhost:5173），服务仍由本进程提供")
	noNotify := fs.Bool("no-notify", false, "关闭待评审系统通知")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := desktop.Run(ctx, desktop.Options{
		Addr:    *addr,
		DataDir: *dataDir,
		DevURL:  *devURL,
		Notify:  !*noNotify,
	})
	if err != nil {
		desktop.ReportFatal(err)
		os.Exit(1)
	}
}
