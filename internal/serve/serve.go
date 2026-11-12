// Package serve 装配并启动看板服务，供 Web 主程序与 Wails 桌面端复用。
package serve

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"log"

	"github.com/mst8222/ingot-board/internal/api"
	"github.com/mst8222/ingot-board/internal/app"
	"github.com/mst8222/ingot-board/internal/endpoint"
	"github.com/mst8222/ingot-board/internal/phone"
	"github.com/mst8222/ingot-board/internal/push"
	"github.com/mst8222/ingot-board/internal/store"
)

// DefaultAddr 默认监听地址（仅本机）。
const DefaultAddr = "127.0.0.1:18721"

// Options 启动选项。
//
// 服务启动后总会把实际地址写入 <DataDir>/endpoint.json（见 internal/endpoint），
// CLI / MCP / runner 靠它发现服务；同一数据目录已有存活实例时拒绝启动。
type Options struct {
	Addr    string // 监听地址，空则用 DefaultAddr；端口 0 表示由系统分配
	DataDir string // 数据目录，空则使用 %AppData%/IngotBoard
	Static  fs.FS  // 前端静态资源，nil 时仅提供 API
}

// Instance 运行中的服务实例。
type Instance struct {
	Service  *app.Service
	Addr     string // 实际监听地址（端口 0 时解析出真实端口）
	shutdown func(context.Context) error
}

// DefaultDataDir 返回默认数据目录。
func DefaultDataDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "IngotBoard")
}

// LegacyDataDirName 改名前的旧数据目录名，只用于一次性迁移。
const LegacyDataDirName = "AgentTodoList"

// MigrateLegacyDataDir 把同级的旧目录整体改名为 target。
//
// 只在 target 不存在且旧目录存在时执行，其余情况一律不动：
// 目标已存在说明用户已经在用新目录，全新安装则根本没有旧目录。
// 用整体改名而不是复制，避免半途中断后留下两份残缺数据。
// 迁移失败不算致命错误，调用方记日志后照常用 target 继续。
func MigrateLegacyDataDir(target string) (bool, error) {
	if target == "" {
		return false, nil
	}
	if _, err := os.Stat(target); err == nil {
		return false, nil
	}
	legacy := filepath.Join(filepath.Dir(target), LegacyDataDirName)
	if legacy == target {
		return false, nil
	}
	info, err := os.Stat(legacy)
	if err != nil || !info.IsDir() {
		return false, nil
	}
	if err := os.Rename(legacy, target); err != nil {
		return false, err
	}
	return true, nil
}

// Run 打开数据库、启动 HTTP 服务与租约回收。
func Run(ctx context.Context, opt Options) (*Instance, error) {
	if opt.Addr == "" {
		opt.Addr = DefaultAddr
	}
	dataDir := opt.DataDir
	if dataDir == "" {
		dataDir = DefaultDataDir()
		if moved, err := MigrateLegacyDataDir(dataDir); err != nil {
			log.Printf("旧数据目录迁移失败（继续使用 %s）: %v", dataDir, err)
		} else if moved {
			log.Printf("已把旧数据目录 %s 迁移到 %s", LegacyDataDirName, dataDir)
		}
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	epPath := endpoint.Path(dataDir)
	if info, err := endpoint.Read(epPath); err == nil && endpoint.Alive(info) {
		return nil, fmt.Errorf("数据目录 %s 已有看板服务在运行：%s (PID %d)，请直接使用它或先将其退出",
			dataDir, info.URL, info.PID)
	}

	st, err := store.Open(filepath.Join(dataDir, "data.db"))
	if err != nil {
		return nil, err
	}
	svc := app.New(st)
	svc.UseNetworkRouting()

	notifier, err := push.New(dataDir, st)
	if err != nil {
		st.Close()
		return nil, err
	}
	svc.SetVAPIDPublicKey(notifier.PublicKey())
	svc.SetReviewNotifier(notifier.NotifyReview)

	httpAPI := api.New(svc, opt.Static)
	hub := phone.NewHub(dataDir, httpAPI.PhoneHandler())
	svc.SetPhoneApply(func(settings app.PhoneSettings) (app.PhoneRuntime, error) {
		return hub.Apply(context.Background(), settings)
	})
	if saved, err := svc.PhoneStatus(); err == nil && (saved.Settings.LANEnabled || saved.Settings.RemoteEnabled) {
		applied, err := hub.Apply(ctx, saved.Settings)
		if err != nil {
			log.Printf("恢复手机监听失败: %v", err)
			svc.SetPhoneRuntime(app.PhoneRuntime{
				Settings:           saved.Settings,
				RemoteError:        err.Error(),
				TailscaleInstalled: phone.NewCLI().Installed(),
			})
		} else {
			svc.SetPhoneRuntime(applied)
			_ = st.SavePhoneSettings(applied.Settings)
		}
	}

	ln, err := net.Listen("tcp", opt.Addr)
	if err != nil {
		st.Close()
		return nil, err
	}
	// 写不出 endpoint 文件就无法被发现、也无法守住单实例，视为启动失败。
	if err := endpoint.Write(epPath, ln.Addr().String()); err != nil {
		ln.Close()
		st.Close()
		return nil, fmt.Errorf("写入 %s 失败: %w", epPath, err)
	}

	srv := &http.Server{
		Handler:           httpAPI.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// 桌面端场景下由调用方记录日志；这里兜底打印。
			os.Stderr.WriteString("HTTP 服务异常退出: " + err.Error() + "\n")
		}
	}()

	reaperCtx, stopReaper := context.WithCancel(ctx)
	svc.StartReaper(reaperCtx, 30*time.Second)
	// 031：与租约回收共用生命周期，单实例内只有一个调度 goroutine。
	svc.StartScheduler(reaperCtx, app.DefaultSchedulerInterval)

	ins := &Instance{
		Service: svc,
		Addr:    ln.Addr().String(),
		shutdown: func(ctx context.Context) error {
			// 先撤掉 endpoint，客户端不再被引向即将关闭的地址。
			_ = os.Remove(epPath)
			stopReaper()
			hub.Close(ctx)
			err1 := srv.Shutdown(ctx)
			err2 := svc.Close()
			if err1 != nil {
				return err1
			}
			return err2
		},
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = ins.shutdown(shutCtx)
	}()
	return ins, nil
}

// Shutdown 主动停止服务。
func (i *Instance) Shutdown(ctx context.Context) error { return i.shutdown(ctx) }
