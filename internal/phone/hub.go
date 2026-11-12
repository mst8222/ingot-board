package phone

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/mst8222/ingot-board/internal/app"
)

// Hub 管手机监听的生命周期。桌面回环服务不在这里。
type Hub struct {
	dataDir string
	handler http.Handler
	ts      Controller
	lanIP   func() (string, error)
	bind    func(addr string) (net.Listener, error)
	bindTLS func(addr, certFile, keyFile string) (net.Listener, error)

	mu          sync.Mutex
	servers     []*http.Server
	port        int
	certFile    string
	keyFile     string
	closeFunnel func()
}

// NewHub 使用真实网卡和 tailscale。
func NewHub(dataDir string, handler http.Handler) *Hub {
	h := &Hub{
		dataDir: dataDir,
		handler: handler,
		ts:      NewCLI(),
		lanIP:   findLANIPv4,
	}
	h.bind = func(addr string) (net.Listener, error) {
		return net.Listen("tcp", addr)
	}
	h.bindTLS = func(addr, certFile, keyFile string) (net.Listener, error) {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, err
		}
		return tls.Listen("tcp", addr, &tls.Config{Certificates: []tls.Certificate{cert}})
	}
	return h
}

// Apply 按开关重建手机监听。失败时不留下半开的端口。
func (h *Hub) Apply(ctx context.Context, in app.PhoneSettings) (app.PhoneRuntime, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopLocked(ctx)

	rt := app.PhoneRuntime{
		Settings:           in,
		TailscaleInstalled: h.ts != nil && h.ts.Installed(),
	}
	if !in.LANEnabled && !in.RemoteEnabled {
		rt.Settings.Port = h.port
		if h.port == 0 {
			rt.Settings.Port = in.Port
		}
		return rt, nil
	}

	port := in.Port
	if port == 0 {
		ln, err := h.bind("127.0.0.1:0")
		if err != nil {
			return app.PhoneRuntime{}, err
		}
		port = ln.Addr().(*net.TCPAddr).Port
		ln.Close()
	}

	var lanIP, tsIP, dns string
	if in.LANEnabled {
		ip, err := h.lanIP()
		if err != nil {
			return app.PhoneRuntime{}, err
		}
		lanIP = ip
	}
	if in.RemoteEnabled {
		if h.ts == nil || !h.ts.Installed() {
			return app.PhoneRuntime{}, errors.New("本机没有 tailscale，无法对手机开放远程连接")
		}
		st, err := h.ts.Status(ctx)
		if err != nil {
			return app.PhoneRuntime{}, err
		}
		certFile := filepath.Join(h.dataDir, "phone.crt")
		keyFile := filepath.Join(h.dataDir, "phone.key")
		if err := h.ts.Cert(ctx, st.DNSName, certFile, keyFile); err != nil {
			return app.PhoneRuntime{}, err
		}
		tsIP, dns = st.IPv4, st.DNSName
		h.certFile, h.keyFile = certFile, keyFile
	}

	plan := planPhoneListeners(port, lanIP, tsIP, dns, in.LANEnabled, in.RemoteEnabled)
	var opened []net.Listener
	closeOpened := func() {
		for _, ln := range opened {
			_ = ln.Close()
		}
	}
	open := func(addr string, tlsListen bool) error {
		var ln net.Listener
		var err error
		if tlsListen {
			ln, err = h.bindTLS(addr, h.certFile, h.keyFile)
		} else {
			ln, err = h.bind(addr)
		}
		if err != nil {
			return fmt.Errorf("监听 %s 失败: %w", addr, err)
		}
		opened = append(opened, ln)
		return nil
	}
	if plan.LANAddr != "" {
		if err := open(plan.LANAddr, false); err != nil {
			closeOpened()
			return app.PhoneRuntime{}, err
		}
	}
	if plan.FunnelAddr != "" {
		if err := open(plan.FunnelAddr, false); err != nil {
			closeOpened()
			return app.PhoneRuntime{}, err
		}
	}
	if plan.TLSAddr != "" {
		if err := open(plan.TLSAddr, true); err != nil {
			closeOpened()
			return app.PhoneRuntime{}, err
		}
	}

	h.servers = nil
	for _, ln := range opened {
		srv := &http.Server{Handler: h.handler, ReadHeaderTimeout: 10 * time.Second}
		h.servers = append(h.servers, srv)
		go func(srv *http.Server, ln net.Listener) {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("手机监听退出: %v", err)
			}
		}(srv, ln)
	}
	h.port = port
	if in.RemoteEnabled {
		if err := h.ts.Funnel(ctx, port, true); err != nil {
			h.stopLocked(ctx)
			return app.PhoneRuntime{}, err
		}
		p := port
		h.closeFunnel = func() {
			offCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := h.ts.Funnel(offCtx, p, false); err != nil {
				log.Printf("关闭 Funnel 失败: %v", err)
			}
		}
	}
	rt.Settings.Port = port
	rt.Settings.LANEnabled = in.LANEnabled
	rt.Settings.RemoteEnabled = in.RemoteEnabled
	rt.LANURL = plan.LANURL
	rt.RemoteURL = plan.PublicURL
	rt.TailscaleInstalled = h.ts != nil && h.ts.Installed()
	return rt, nil
}

// Close 关掉手机监听。
func (h *Hub) Close(ctx context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopLocked(ctx)
}

func (h *Hub) stopLocked(ctx context.Context) {
	if h.closeFunnel != nil {
		h.closeFunnel()
		h.closeFunnel = nil
	}
	for _, srv := range h.servers {
		_ = srv.Shutdown(ctx)
	}
	h.servers = nil
}
