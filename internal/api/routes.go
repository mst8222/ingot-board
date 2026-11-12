package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
)

// route 是路由表的一行。
//
// 为什么要把路由变成数据：net/http.ServeMux 不暴露已注册路由表
// （对未匹配路径 mux.Handler() 返回 NotFoundHandler），所以想写
// "代码里的路由集合 == OpenAPI 里的 operationId 集合"的契约测试，
// 必须先把路由表本身变成可枚举的数据结构。
type route struct {
	Method  string
	Pattern string
	Handler http.HandlerFunc
	// Auth 表示该路由需要 Agent 的 Bearer Token。
	Auth bool
	// DeviceAuth 表示这条路由只挂在手机监听上，OpenAPI 要声明设备令牌。
	// 手机监听上的其它人类路由也校验设备令牌，但同一条路由在本机回环上不需要。
	DeviceAuth bool
	// OpID 是 OpenAPI 的 operationId。契约测试按它双向比对。
	// 空值会被契约测试判为失败，避免新增路由时忘记同步契约。
	OpID string
}

func (s *Server) routes() {
	desktop := append(s.humanRoutes(), s.agentRoutes()...)
	phoneOnly := s.phoneOnlyRoutes()
	s.routeTable = append(append([]route{}, desktop...), phoneOnly...)
	s.handler = s.mount(desktop, false)
	s.phoneHandler = s.mount(append(s.phoneSharedRoutes(), phoneOnly...), true)
}

// mount 把路由装进一个 mux。deviceGate 为真时，除配对兑换外都要设备令牌。
func (s *Server) mount(routes []route, deviceGate bool) http.Handler {
	mux := http.NewServeMux()
	for _, r := range routes {
		var h http.Handler = r.Handler
		if r.Auth {
			h = s.agentAuth(h)
		}
		if deviceGate && r.OpID != "pairDevice" {
			h = s.deviceAuth(h, r.OpID)
		}
		mux.Handle(r.Method+" "+r.Pattern, h)
	}
	if s.static != nil {
		mux.Handle("/", s.spaHandler())
	} else {
		// 即使不托管前端（桌面端 / 仅 API 模式），未知 /api 路径也必须返回
		// JSON 404 —— 否则调用方拿到 "404 page not found" 纯文本，
		// JSON 解析直接失败，报错信息完全指不到问题上。
		mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeErr(w, newErr(http.StatusNotFound, CodeRouteNotFound, "接口不存在"))
		}))
	}
	return mux
}

// humanRoutes 人类端接口。仅监听 127.0.0.1，无登录。
func (s *Server) humanRoutes() []route {
	return []route{
		{"GET", "/api/v1/health", s.health, false, false, "health"},
		{"GET", "/api/v1/meta", s.meta, false, false, "getMeta"},
		{"POST", "/api/v1/system/pick-directory", s.pickDirectoryHandler, false, false, "pickDirectory"},
		{"GET", "/api/v1/workdirs", s.listWorkdirs, false, false, "listWorkdirs"},

		{"GET", "/api/v1/board", s.board, false, false, "getBoard"},
		{"GET", "/api/v1/tasks", s.listTasks, false, false, "listTasks"},
		{"POST", "/api/v1/tasks", s.createTask, false, false, "createTask"},
		{"GET", "/api/v1/tasks/{id}", s.getTask, false, false, "getTask"},
		{"PATCH", "/api/v1/tasks/{id}", s.updateTask, false, false, "updateTask"},
		{"POST", "/api/v1/tasks/{id}/transition", s.humanTransition, false, false, "transitionTask"},
		{"POST", "/api/v1/tasks/{id}/move", s.moveTask, false, false, "moveTask"},
		{"POST", "/api/v1/tasks/{id}/notes", s.humanNote, false, false, "addTaskNote"},
		{"GET", "/api/v1/tasks/{id}/events", s.listEvents, false, false, "listTaskEvents"},
		{"POST", "/api/v1/tasks/{id}/archive", s.archiveTask, false, false, "archiveTask"},
		{"POST", "/api/v1/tasks/{id}/unarchive", s.unarchiveTask, false, false, "unarchiveTask"},

		{"GET", "/api/v1/projects", s.listProjects, false, false, "listProjects"},
		{"POST", "/api/v1/projects", s.createProject, false, false, "createProject"},
		{"PATCH", "/api/v1/projects/{id}", s.updateProject, false, false, "updateProject"},
		{"POST", "/api/v1/projects/{id}/archive", s.archiveProject, false, false, "archiveProject"},
		{"POST", "/api/v1/projects/{id}/unarchive", s.unarchiveProject, false, false, "unarchiveProject"},
		{"DELETE", "/api/v1/projects/{id}", s.deleteProject, false, false, "deleteProject"},
		{"GET", "/api/v1/projects/{id}/workdirs", s.listProjectWorkdirs, false, false, "listProjectWorkdirs"},
		{"POST", "/api/v1/projects/{id}/workdirs", s.addProjectWorkdir, false, false, "addProjectWorkdir"},
		{"DELETE", "/api/v1/projects/{id}/workdirs/{wid}", s.removeProjectWorkdir, false, false, "removeProjectWorkdir"},

		{"GET", "/api/v1/dispatch/settings", s.getDispatchSettings, false, false, "getDispatchSettings"},
		{"PUT", "/api/v1/dispatch/settings", s.updateDispatchSettings, false, false, "updateDispatchSettings"},
		{"GET", "/api/v1/tasks/{id}/dispatch-suggestion", s.getDispatchSuggestion, false, false, "getDispatchSuggestion"},
		{"POST", "/api/v1/tasks/{id}/dispatch/accept", s.acceptDispatchSuggestion, false, false, "acceptDispatchSuggestion"},
		{"POST", "/api/v1/tasks/{id}/handoff", s.handoffTask, false, false, "handoffTask"},
		{"GET", "/api/v1/tasks/{id}/handoff-candidates", s.listHandoffCandidates, false, false, "listHandoffCandidates"},

		{"GET", "/api/v1/templates", s.listTemplates, false, false, "listTemplates"},
		{"POST", "/api/v1/templates", s.createTemplate, false, false, "createTemplate"},
		{"PATCH", "/api/v1/templates/{id}", s.updateTemplate, false, false, "updateTemplate"},
		{"DELETE", "/api/v1/templates/{id}", s.deleteTemplate, false, false, "deleteTemplate"},
		{"POST", "/api/v1/templates/{id}/instantiate", s.instantiateTemplate, false, false, "instantiateTemplate"},

		{"GET", "/api/v1/schedules", s.listSchedules, false, false, "listSchedules"},
		{"POST", "/api/v1/schedules", s.createSchedule, false, false, "createSchedule"},
		{"PATCH", "/api/v1/schedules/{id}", s.updateSchedule, false, false, "updateSchedule"},
		{"DELETE", "/api/v1/schedules/{id}", s.deleteSchedule, false, false, "deleteSchedule"},

		{"GET", "/api/v1/hosts", s.listHosts, false, false, "listHosts"},
		{"GET", "/api/v1/agents", s.listAgents, false, false, "listAgents"},
		{"POST", "/api/v1/agents", s.registerAgent, false, false, "registerAgent"},
		{"DELETE", "/api/v1/agents/{id}", s.deleteAgent, false, false, "deleteAgent"},
		{"POST", "/api/v1/agents/{id}/token", s.rotateToken, false, false, "rotateAgentToken"},
		{"POST", "/api/v1/agents/{id}/enabled", s.setAgentEnabled, false, false, "setAgentEnabled"},
		{"POST", "/api/v1/agents/{id}/quota", s.setAgentQuota, false, false, "setAgentQuota"},
		{"POST", "/api/v1/agents/{id}/balance", s.setAgentBalance, false, false, "setAgentBalance"},
		{"GET", "/api/v1/agents/{id}/ledger", s.listAgentLedger, false, false, "listAgentLedger"},

		{"GET", "/api/v1/notify/review-webhook", s.getReviewWebhook, false, false, "getReviewWebhook"},
		{"PUT", "/api/v1/notify/review-webhook", s.updateReviewWebhook, false, false, "updateReviewWebhook"},

		{"GET", "/api/v1/phone/status", s.phoneStatus, false, false, "getPhoneStatus"},
		{"PUT", "/api/v1/phone/settings", s.updatePhoneSettings, false, false, "updatePhoneSettings"},
		{"POST", "/api/v1/phone/pair-codes", s.createPairCode, false, false, "createPairCode"},
		{"GET", "/api/v1/phone/devices", s.listPhoneDevices, false, false, "listPhoneDevices"},
		{"POST", "/api/v1/phone/devices/{id}/revoke", s.revokePhoneDevice, false, false, "revokePhoneDevice"},

		{"GET", "/api/v1/stream", s.stream, false, false, "streamEvents"},
	}
}

// phoneSharedRoutes 手机与桌面都能用的人类接口。
// 手机 mux 会再包一层设备令牌；这里的 DeviceAuth 保持 false，
// 因为同一条路由在回环上不要求令牌，OpenAPI 不能把它写成必须鉴权。
func (s *Server) phoneSharedRoutes() []route {
	allow := map[string]bool{
		"health": true, "getMeta": true, "listWorkdirs": true,
		"getBoard": true, "listTasks": true, "createTask": true, "getTask": true,
		"updateTask": true, "transitionTask": true, "addTaskNote": true,
		"listTaskEvents": true, "archiveTask": true, "unarchiveTask": true,
		"listAgents": true, "streamEvents": true,
		"listProjects": true, "createProject": true, "updateProject": true,
		"archiveProject": true, "unarchiveProject": true, "deleteProject": true,
		"listProjectWorkdirs": true, "addProjectWorkdir": true, "removeProjectWorkdir": true,
	}
	var out []route
	for _, r := range s.humanRoutes() {
		if allow[r.OpID] {
			out = append(out, r)
		}
	}
	return out
}

// phoneOnlyRoutes 只出现在手机监听上的接口。
func (s *Server) phoneOnlyRoutes() []route {
	return []route{
		{"POST", "/api/v1/devices/pair", s.pairDevice, false, false, "pairDevice"},
		{"GET", "/api/v1/push/vapid-public-key", s.vapidPublicKey, false, true, "getVapidPublicKey"},
		{"PUT", "/api/v1/push/subscription", s.putPushSubscription, false, true, "putPushSubscription"},
		{"DELETE", "/api/v1/push/subscription", s.deletePushSubscription, false, true, "deletePushSubscription"},
	}
}

// agentRoutes Agent 端接口（Bearer Token 鉴权）。
func (s *Server) agentRoutes() []route {
	return []route{
		{"GET", "/api/v1/agent/me", s.agentMe, true, false, "agentMe"},
		{"GET", "/api/v1/agent/tasks", s.agentListTasks, true, false, "agentListTasks"},
		{"POST", "/api/v1/agent/tasks/{id}/claim", s.agentClaim, true, false, "agentClaim"},
		{"POST", "/api/v1/agent/tasks/{id}/heartbeat", s.agentHeartbeat, true, false, "agentHeartbeat"},
		{"POST", "/api/v1/agent/tasks/{id}/submit", s.agentSubmit, true, false, "agentSubmit"},
		{"POST", "/api/v1/agent/tasks/{id}/release", s.agentRelease, true, false, "agentRelease"},
		{"POST", "/api/v1/agent/quota", s.agentReportQuota, true, false, "agentReportQuota"},
		{"POST", "/api/v1/agent/tasks/{id}/notes", s.agentNote, true, false, "agentAddTaskNote"},
		{"GET", "/api/v1/agent/tasks/{id}/events", s.agentListEvents, true, false, "agentListTaskEvents"},
	}
}

// Routes 返回已注册路由表的副本（供契约测试与调试使用）。
func (s *Server) Routes() []RouteInfo {
	out := make([]RouteInfo, 0, len(s.routeTable))
	for _, r := range s.routeTable {
		out = append(out, RouteInfo{Method: r.Method, Pattern: r.Pattern, OpID: r.OpID, Auth: r.Auth, DeviceAuth: r.DeviceAuth})
	}
	return out
}

// RouteInfo 是路由的对外只读视图。
type RouteInfo struct {
	Method     string
	Pattern    string
	OpID       string
	Auth       bool
	DeviceAuth bool
}

// call 直接以给定请求调用某个 handler，供表驱动测试使用。
func call(h http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	w := httptest.NewRecorder()
	h(w, req)
	return w
}
