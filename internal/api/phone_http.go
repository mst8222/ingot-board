package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"

	"github.com/mst8222/ingot-board/internal/app"
	"github.com/mst8222/ingot-board/internal/domain"
)

type deviceCtxKey struct{}

func withDevice(ctx context.Context, dev *domain.Device) context.Context {
	return app.WithDevice(ctx, dev)
}

func deviceFromContext(ctx context.Context) *domain.Device {
	return app.DeviceFromContext(ctx)
}

// deviceAuth 校验 atd_ 设备令牌。来源依次是 Authorization、Cookie、access_token。
// Cookie 给浏览器里的 EventSource 用，它带不上自定义头。
//
// op 是该路由的 operationId（017）：认证通过后再判操作范围，
// 设备 Scopes 非 nil 且不含该 op → 403 FORBIDDEN。
// 存量设备（0008 之前配对，Scopes==nil）视为全量放行。
func (s *Server) deviceAuth(next http.Handler, op string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			if c, err := r.Cookie("atd_device"); err == nil {
				token = c.Value
			}
		}
		if token == "" {
			token = r.URL.Query().Get("access_token")
		}
		dev, err := s.svc.AuthenticateDevice(token)
		if err != nil {
			writeErr(w, newErr(http.StatusUnauthorized, CodeUnauthorized, "需要手机设备令牌"))
			return
		}
		if dev.Scopes != nil && !dev.Scopes.AllowsOp(domain.DeviceOp(op)) {
			writeErr(w, newErr(http.StatusForbidden, CodeForbidden,
				"设备令牌没有这项权限"))
			return
		}
		next.ServeHTTP(w, r.WithContext(withDevice(r.Context(), dev)))
	})
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(auth, prefix))
}

func (s *Server) listWorkdirs(w http.ResponseWriter, r *http.Request) {
	// 025 反查契约：items 为 {workdir, project_ids} 对象；可选 project_id 过滤。
	var pf *int64
	if raw := firstOf(r.URL.Query(), "project_id"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v <= 0 {
			writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, "非法的 project_id"))
			return
		}
		pf = &v
	}
	items, err := s.svc.RecentWorkdirsWithProjects(20, pf)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]any{"items": items})
}

type phoneStatusBody struct {
	LANEnabled         bool   `json:"lan_enabled"`
	RemoteEnabled      bool   `json:"remote_enabled"`
	LANURL             string `json:"lan_url"`
	RemoteURL          string `json:"remote_url"`
	RemoteError        string `json:"remote_error"`
	TailscaleInstalled bool   `json:"tailscale_installed"`
}

func phoneStatusJSON(rt app.PhoneRuntime) phoneStatusBody {
	return phoneStatusBody{
		LANEnabled:         rt.Settings.LANEnabled,
		RemoteEnabled:      rt.Settings.RemoteEnabled,
		LANURL:             rt.LANURL,
		RemoteURL:          rt.RemoteURL,
		RemoteError:        rt.RemoteError,
		TailscaleInstalled: rt.TailscaleInstalled,
	}
}

func (s *Server) phoneStatus(w http.ResponseWriter, r *http.Request) {
	rt, err := s.svc.PhoneStatus()
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, phoneStatusJSON(rt))
}

func (s *Server) updatePhoneSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LANEnabled    bool `json:"lan_enabled"`
		RemoteEnabled bool `json:"remote_enabled"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	rt, err := s.svc.UpdatePhoneSettings(app.PhoneSettings{
		LANEnabled:    req.LANEnabled,
		RemoteEnabled: req.RemoteEnabled,
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, phoneStatusJSON(rt))
}

func (s *Server) createPairCode(w http.ResponseWriter, r *http.Request) {
	rt, err := s.svc.PhoneStatus()
	if err != nil {
		fail(w, err)
		return
	}
	if rt.LANURL == "" && rt.RemoteURL == "" {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, "请先开启局域网或远程连接"))
		return
	}
	// 017：body 可选携带 scopes 收窄范围；空体/无 scopes 走缺省范围。
	// 容忍解析：空体不是错误（桌面签发侧可以完全不传）。
	var req struct {
		Scopes *domain.DeviceScopes `json:"scopes"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	code, expires, err := s.svc.IssuePairCode(time.Now().UTC(), req.Scopes)
	if err != nil {
		fail(w, err)
		return
	}
	type link struct {
		Kind  string `json:"kind"`
		URL   string `json:"url"`
		QRPNG string `json:"qr_png"`
	}
	links := make([]link, 0, 2)
	add := func(kind, base string) {
		if base == "" {
			return
		}
		u := strings.TrimRight(base, "/") + "/?code=" + code
		links = append(links, link{Kind: kind, URL: u, QRPNG: qrDataURL(u)})
	}
	add("remote", rt.RemoteURL)
	add("lan", rt.LANURL)
	writeOK(w, http.StatusCreated, map[string]any{
		"code":       code,
		"expires_at": expires,
		"links":      links,
	})
}

func qrDataURL(text string) string {
	png, err := qrcode.Encode(text, qrcode.Medium, 256)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}

func (s *Server) listPhoneDevices(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.ListDevices()
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) revokePhoneDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := s.svc.RevokeDevice(id); err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) pairDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	dev, token, err := s.svc.PairDevice(req.Code, req.Name)
	if err != nil {
		fail(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "atd_device",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeOK(w, http.StatusCreated, map[string]any{"device": dev, "token": token})
}

func (s *Server) vapidPublicKey(w http.ResponseWriter, r *http.Request) {
	key := s.svc.VAPIDPublicKey()
	if key == "" {
		writeErr(w, newErr(http.StatusInternalServerError, CodeInternal, "推送密钥尚未生成"))
		return
	}
	writeOK(w, http.StatusOK, map[string]string{"public_key": key})
}

func (s *Server) putPushSubscription(w http.ResponseWriter, r *http.Request) {
	dev := deviceFromContext(r.Context())
	if dev == nil {
		writeErr(w, newErr(http.StatusUnauthorized, CodeUnauthorized, "需要手机设备令牌"))
		return
	}
	var req struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := s.svc.SavePushSubscription(dev.ID, req.Endpoint, req.Keys.P256dh, req.Keys.Auth); err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) deletePushSubscription(w http.ResponseWriter, r *http.Request) {
	dev := deviceFromContext(r.Context())
	if dev == nil {
		writeErr(w, newErr(http.StatusUnauthorized, CodeUnauthorized, "需要手机设备令牌"))
		return
	}
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := s.svc.DeletePushSubscription(dev.ID, req.Endpoint); err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]bool{"ok": true})
}
