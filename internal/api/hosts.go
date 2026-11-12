package api

import (
	"net/http"

	"github.com/mst8222/ingot-board/internal/hosts"
)

// listHosts 返回本机支持的执行端清单与当前可用状态。
//
// 可用状态不是缓存值：每次请求都按当前用户目录、PATH 与已登记执行端的
// 启用状态重算，这样用户装好某个编辑器后重跑 connect 就能立刻看到变化。
func (s *Server) listHosts(w http.ResponseWriter, r *http.Request) {
	agents, err := s.svc.ListAgents()
	if err != nil {
		writeErr(w, newErr(http.StatusInternalServerError, CodeInternal, "读取执行端状态失败"))
		return
	}
	enabled := make(map[string]bool, len(agents))
	for _, a := range agents {
		enabled[a.Name] = a.Enabled
	}
	writeOK(w, http.StatusOK, map[string]any{
		"items": hosts.HostInfoList(hosts.LiveLayout(), enabled),
	})
}
