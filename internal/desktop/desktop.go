// Package desktop 是 Ingot Board 的 Wails v3 桌面壳。
//
// 它不重写任何业务逻辑：进程内直接启动 internal/serve 的看板服务
// （随机本地端口 + 内嵌前端产物），再把系统 webview 指向该地址。
// 于是桌面端与浏览器端共用同一套前端、同一份 SQLite 数据、同一个 API，
// 不存在"桌面版单独维护一份代码"的分叉。
//
// Wails 在这里只负责三件事：窗口、托盘、系统通知。
package desktop

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"

	"github.com/mst8222/ingot-board/internal/app"
	"github.com/mst8222/ingot-board/internal/serve"
	"github.com/mst8222/ingot-board/internal/webassets"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

const (
	// AppName 应用名，同时用于窗口标题、托盘提示与 Windows Toast 的 AppID。
	AppName = "Ingot Board"
	// windowName 主窗口名，托盘菜单按它找回窗口。
	windowName = "main"
)

// Options 桌面端启动选项。
type Options struct {
	// Addr 服务监听地址；空值时随机端口（dev 模式改用默认端口以配合 vite 代理）。
	Addr string
	// DataDir 数据目录；空值用默认目录。
	DataDir string
	// DevURL 非空时窗口加载前端 dev server（如 http://localhost:5173），
	// 看板服务仍由本进程提供，只是静态资源交给 vite。
	DevURL string
	// Notify 是否开启"任务进入待评审"的系统通知。
	Notify bool
	// OnReady 服务就绪回调（测试与集成用）：参数是窗口最终加载的地址。
	OnReady func(url string)
}

// Run 启动内嵌服务并运行桌面窗口，阻塞直到应用退出。
//
// 退出路径有三条：托盘菜单退出、窗口关闭后进程被系统终止、ctx 被取消。
// 前两条都通过 OnShutdown 把 ctx 取消掉，服务随之优雅关闭（含 SQLite 落盘）。
func Run(ctx context.Context, opt Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	dataDir := opt.DataDir
	if dataDir == "" {
		dataDir = serve.DefaultDataDir()
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}

	ins, err := serve.Run(ctx, serve.Options{
		Addr:    serveAddr(opt.Addr, opt.DevURL),
		DataDir: dataDir,
		Static:  webassets.Dist,
	})
	if err != nil {
		return err
	}

	target := windowTarget(ins.Addr, opt.DevURL)
	if opt.OnReady != nil {
		opt.OnReady(target)
	}
	return runShell(ctx, cancel, ins.Service, target, opt.Notify)
}

// serveAddr 决定内嵌服务的监听地址。
//
// dev 模式不能用随机端口：vite 的 /api 代理写死指向默认端口
// （见 web/vite.config.ts），随机端口会让前端代理打空。
// 正式模式反过来必须随机：用户很可能已经开着 ingot 开发实例，
// 抢 18721 会让其中一个起不来。
func serveAddr(addr, devURL string) string {
	if addr != "" {
		return addr
	}
	if devURL != "" {
		return serve.DefaultAddr
	}
	return "127.0.0.1:0"
}

// windowTarget 决定窗口最终加载的地址：dev 模式下交给前端 dev server，
// 正式模式直接加载内嵌服务（静态资源也由它托管）。
func windowTarget(insAddr, devURL string) string {
	if devURL != "" {
		return devURL
	}
	return "http://" + insAddr
}

// runShell 装配 Wails 应用：窗口 + 托盘 + 待评审通知。
func runShell(ctx context.Context, onQuit func(), svc *app.Service, target string, notify bool) error {
	ns := notifications.New()

	a := application.New(application.Options{
		Name:        AppName,
		Description: "面向本机 AI Agent 的任务看板",
		Icon:        IconPNG(256),
		Services:    []application.Service{application.NewService(ns)},
		// 页面由内嵌 HTTP 服务提供，Wails 的 asset server 不承载任何内容。
		// 仍给一个 handler：窗口加载的是外部 URL，这条路径只在 Wails 内部
		// 探测运行时资源时才会被碰到，返回 404 比 nil handler 安全。
		Assets: application.AssetOptions{
			Handler: assetHandler(),
		},
		OnShutdown: onQuit,
	})

	win := a.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             windowName,
		Title:            AppName,
		Width:            1360,
		Height:           900,
		MinWidth:         880,
		MinHeight:        560,
		URL:              target,
		BackgroundColour: application.NewRGB(248, 250, 252),
	})

	// 关窗口 ≠ 退出：看板是常驻后台的服务（Agent 还在提交任务、SSE 还在推），
	// 关掉窗口只是收起来，真正退出走托盘菜单。
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		win.Hide()
		e.Cancel()
	})

	showBoard := func() {
		win.Show()
		win.Focus()
	}

	tray := a.SystemTray.New()
	// 给 64px 让系统按 DPI 缩到托盘小图标尺寸，高分屏不糊
	tray.SetIcon(IconPNG(64))
	tray.SetTooltip(AppName)

	menu := a.NewMenu()
	menu.Add("显示看板").OnClick(func(*application.Context) { showBoard() })
	menu.Add("在浏览器中打开").OnClick(func(*application.Context) { openURL(target) })
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(*application.Context) { a.Quit() })
	tray.SetMenu(menu)

	if notify {
		stop := watchReviews(ctx, svc, func(n reviewNotice) {
			if err := ns.SendNotification(notifications.NotificationOptions{
				ID:       fmt.Sprintf("review-%d-%d", n.TaskID, n.EventID),
				Title:    "有任务等待评审",
				Subtitle: n.Actor,
				Body:     n.Title,
			}); err != nil {
				// 通知失败不该影响看板：只写 stderr，不打断事件循环。
				fmt.Fprintf(os.Stderr, "发送待评审通知失败: %v\n", err)
			}
		})
		defer stop()
	}

	return a.Run()
}

// assetHandler Wails asset server 的占位实现，见 runShell 注释。
func assetHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "桌面端页面由进程内看板服务提供，此处不托管静态资源", http.StatusNotFound)
	})
}

// openURL 用系统默认浏览器打开地址。
func openURL(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	_ = cmd.Start()
}
