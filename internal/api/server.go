// Package api 提供 HTTP 接口：人类端（本机免登录）、Agent 端（Bearer Token），
// 以及 SSE 实时事件流与前端静态资源。
//
// 全部路由挂在 /api/v1 前缀下，响应统一走 respond.go 的 {code,msg,data} 信封。
package api

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/mst8222/ingot-board/internal/app"
	"github.com/mst8222/ingot-board/internal/domain"
	"github.com/mst8222/ingot-board/internal/store"
	"github.com/mst8222/ingot-board/internal/sysdialog"
)

// Server HTTP 服务装配。
type Server struct {
	svc    *app.Service
	static fs.FS
	// routeTable 把已注册路由暴露为可枚举数据，供 OpenAPI 契约测试比对。
	routeTable   []route
	handler      http.Handler
	phoneHandler http.Handler
	cors         bool
	// pickDirectory 打开本机目录对话框；测试可注入，避免弹出真实 UI。
	pickDirectory func(initial string) (path string, cancelled bool, err error)
}

// Options 服务选项。
type Options struct {
	// CORS 允许跨源访问（仅调试用）。
	CORS bool
	// PickDirectory 覆盖默认的本机目录选择实现；nil 时用 sysdialog。
	PickDirectory func(initial string) (path string, cancelled bool, err error)
}

// New 创建 HTTP Server。staticFS 传 nil 时不托管前端页面。
// cors 默认关闭 —— SPA 同源，Vite dev server 已代理 /api，
// 加一层永不开的中间件不如不加。
func New(svc *app.Service, staticFS fs.FS) *Server {
	return NewWithOptions(svc, staticFS, Options{})
}

// NewWithOptions 带选项创建 Server。
func NewWithOptions(svc *app.Service, staticFS fs.FS, opt Options) *Server {
	pick := opt.PickDirectory
	if pick == nil {
		pick = sysdialog.PickDirectory
	}
	s := &Server{svc: svc, static: staticFS, cors: opt.CORS, pickDirectory: pick}
	s.routes()
	return s
}

// Handler 返回桌面（本机回环）路由处理器。
func (s *Server) Handler() http.Handler {
	if s.cors {
		return withCORS(s.handler)
	}
	return s.handler
}

// PhoneHandler 返回手机监听的处理器。除配对兑换外都要设备令牌。
func (s *Server) PhoneHandler() http.Handler {
	if s.cors {
		return withCORS(s.phoneHandler)
	}
	return s.phoneHandler
}

// ---------- 人类端 ----------

// health 健康检查。pid 供 endpoint 包判定"endpoint.json 记录的实例是否仍是本进程"。
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeOK(w, http.StatusOK, map[string]any{"status": "ok", "pid": os.Getpid()})
}

// meta 返回状态机与枚举定义。
//
// 前端由此获得状态流转白名单与中文标签，不再在 store 里各维护一份 ——
// 重构前 domain.CanTransition 与前端 DRAG_TARGETS 各写一份，
// STATUS_LABEL 与 COLUMNS[].label 又写了两遍，四份定义必然漂移。
func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	writeOK(w, http.StatusOK, s.svc.Meta())
}

type pickDirectoryReq struct {
	Initial string `json:"initial"`
}

// pickDirectoryHandler 在本机弹出原生目录选择对话框。
//
// 取消不是错误：返回 cancelled=true，前端应保持输入框原值。
func (s *Server) pickDirectoryHandler(w http.ResponseWriter, r *http.Request) {
	var req pickDirectoryReq
	// 空 body 合法（EOF）；有内容则必须是合法 JSON。
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, "请求体不是合法 JSON", err.Error()))
		return
	}
	path, cancelled, err := s.pickDirectory(strings.TrimSpace(req.Initial))
	if err != nil {
		writeErr(w, newErr(http.StatusInternalServerError, CodeInternal, "无法打开目录选择对话框"))
		return
	}
	writeOK(w, http.StatusOK, map[string]any{
		"path":      path,
		"cancelled": cancelled,
	})
}

// board 看板数据：按状态分组 + 每列真实计数。
//
// 为什么看板要单独的接口而不是复用 /tasks：
//   - 语义不同：这里是"每列最多 N 条 + 真实总数"，不是分页；
//   - 查询形状不同：需要 GROUP BY 计数；
//   - 前端 queryKey 必须分开，否则 SSE invalidate 会误伤列表缓存。
//
// 折叠条上的数字取自 counts（真实总数），不受 limit 影响。
func (s *Server) board(w http.ResponseWriter, r *http.Request) {
	f, err := buildTaskFilter(r)
	if err != nil {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, err.Error()))
		return
	}
	// 看板不翻页：忽略 page/page_size，改用 per_column 控制每列条数。
	f.Limit, f.Offset = 0, 0
	perColumn := store.MaxPerColumn
	if raw := r.URL.Query().Get("per_column"); raw != "" {
		n, cerr := parseInt(raw)
		if cerr != nil || n < 1 || n > store.MaxPerColumn {
			writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, "非法 per_column"))
			return
		}
		perColumn = n
	}
	res, err := s.svc.Board(f, perColumn)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, res)
}

type listTasksData struct {
	Items    []*domain.Task `json:"items"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
}

// listTasks 分页任务列表。
func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	f, err := buildTaskFilter(r)
	if err != nil {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, err.Error()))
		return
	}
	tasks, total, err := s.svc.ListTasks(f)
	if err != nil {
		fail(w, err)
		return
	}
	page, _ := parsePage(r.URL.Query())
	writeOK(w, http.StatusOK, listTasksData{
		Items: tasks, Total: total, Page: page.Page, PageSize: page.Limit,
	})
}

type createTaskReq struct {
	Title              string `json:"title"`
	Description        string `json:"description"`
	AcceptanceCriteria string `json:"acceptance_criteria"`
	Workdir            string `json:"workdir"`
	Priority           string `json:"priority"`
	AssigneeAgentID    *int64 `json:"assignee_agent_id"`
	// ProjectID 可空：null/省略表示无项目；指向不存在或已归档的项目返回错误。
	ProjectID *int64 `json:"project_id"`
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskReq
	if !decodeBody(w, r, &req) {
		return
	}
	t, err := s.svc.CreateTask(r.Context(), app.CreateTaskInput{
		Title:              strings.TrimSpace(req.Title),
		Description:        req.Description,
		AcceptanceCriteria: req.AcceptanceCriteria,
		Workdir:            req.Workdir,
		Priority:           req.Priority,
		AssigneeAgentID:    req.AssigneeAgentID,
		ProjectID:          req.ProjectID,
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusCreated, t)
}

// getTask 返回任务详情。
//
// ⚠️ 不再内联 events —— 重构前 detail 与时间线绑在一个响应里，
// 导致打开详情必须等全量时间线查完，而事件是 append-only 会无限增长。
// 时间线改走独立的可分页接口。
func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	t, err := s.svc.GetTask(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, t)
}

type updateTaskReq struct {
	Title              string `json:"title"`
	Description        string `json:"description"`
	AcceptanceCriteria string `json:"acceptance_criteria"`
	Workdir            string `json:"workdir"`
	Priority           string `json:"priority"`
	AssigneeAgentID    *int64 `json:"assignee_agent_id"`
	Version            int64  `json:"version"`
	Category           string `json:"category"`
	// ProjectID 全量覆盖：null 表示清空项目归属（与 assignee 同语义）。
	ProjectID *int64 `json:"project_id"`
}

// updateTask 编辑任务内容。
//
// ⚠️ 这是**全量覆盖**而非部分更新：struct 无 omitempty，缺失字段解码为零值。
// 省略 assignee_agent_id（nil）等于清空指派，省略 priority 直接 400。
// 前端必须提交完整表单。
func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var req updateTaskReq
	if !decodeBody(w, r, &req) {
		return
	}
	t, err := s.svc.UpdateTask(r.Context(), id, app.UpdateTaskInput{
		Title:              strings.TrimSpace(req.Title),
		Description:        req.Description,
		AcceptanceCriteria: req.AcceptanceCriteria,
		Workdir:            req.Workdir,
		Priority:           req.Priority,
		AssigneeAgentID:    req.AssigneeAgentID,
		Version:            req.Version,
		Category:           req.Category,
		ProjectID:          req.ProjectID,
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, t)
}

type transitionReq struct {
	To      string `json:"to"`
	Comment string `json:"comment"`
	// ToIndex 落位下标（0 基）。省略或为 null 表示追加到目标列末尾。
	// 与 move 端点分开是因为：跨列要改状态（同列不用），
	// 混在一个请求里会出现"to 指向当前状态"这种无意义的请求。
	ToIndex *int `json:"to_index"`
}

func (s *Server) humanTransition(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var req transitionReq
	if !decodeBody(w, r, &req) {
		return
	}
	if !domain.IsValidStatus(req.To) {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, "非法目标状态"))
		return
	}
	toIndex := store.AppendToEnd
	if req.ToIndex != nil {
		if *req.ToIndex < 0 {
			writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, "to_index 不能为负"))
			return
		}
		toIndex = *req.ToIndex
	}
	t, err := s.svc.HumanTransition(id, domain.Status(req.To), req.Comment, toIndex)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, t)
}

type moveReq struct {
	// ToIndex 目标下标（0 基）。必填 —— 与"追加到末尾"的歧义在这里消失：
	// 想追加末尾就传当前列长度，前端直接知道列长度。
	ToIndex int   `json:"to_index"`
	Version int64 `json:"version"`
}

// moveTask 调整同列内的顺序。
//
// 不改 status，因此不走状态机、不写 task_events。
func (s *Server) moveTask(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var req moveReq
	if !decodeBody(w, r, &req) {
		return
	}
	if req.ToIndex < 0 {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, "to_index 不能为负"))
		return
	}
	if req.Version <= 0 {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, "缺少 version"))
		return
	}
	t, err := s.svc.MoveTask(id, req.ToIndex, req.Version)
	if err != nil {
		fail(w, err)
		return
	}
	// 整列顺序变了，本条的实体已不足以让其他客户端重排 —— 必须广播整列。
	s.svc.PublishColumn(string(t.Status))
	writeOK(w, http.StatusOK, t)
}

type noteReq struct {
	Content string `json:"content"`
}

func (s *Server) humanNote(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var req noteReq
	if !decodeBody(w, r, &req) {
		return
	}
	t, err := s.svc.AddHumanNote(id, strings.TrimSpace(req.Content))
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, t)
}

type listEventsData struct {
	Items    []*domain.TaskEvent `json:"items"`
	Total    int                 `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
	HasMore  bool                `json:"has_more"`
}

// listEvents 分页取任务事件时间线。
func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	page, err := parsePage(r.URL.Query())
	if err != nil {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, err.Error()))
		return
	}
	size := page.Limit
	if size > store.MaxEventPageSize {
		size = store.MaxEventPageSize
	}
	items, total, err := s.svc.ListEventsPage(store.EventFilter{
		TaskID:        id,
		Page:          page.Page,
		Size:          size,
		Desc:          true, // 新事件在前，配合"加载更早"翻页
		WithHeartbeat: parseBoolTrue(r.URL.Query().Get("with_heartbeat")),
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, listEventsData{
		Items: items, Total: total, Page: page.Page, PageSize: size,
		HasMore: page.Page*size < total,
	})
}

// archiveTask 归档任务。
func (s *Server) archiveTask(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	t, err := s.svc.ArchiveTask(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, t)
}

// unarchiveTask 取消归档。
func (s *Server) unarchiveTask(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	t, err := s.svc.UnarchiveTask(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, t)
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.svc.ListAgents()
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]any{"items": agents})
}

type registerAgentReq struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

func (s *Server) registerAgent(w http.ResponseWriter, r *http.Request) {
	var req registerAgentReq
	if !decodeBody(w, r, &req) {
		return
	}
	a, token, err := s.svc.RegisterAgent(strings.TrimSpace(req.Name), strings.TrimSpace(req.Kind))
	if err != nil {
		fail(w, err)
		return
	}
	// token 明文仅此一次返回。
	writeOK(w, http.StatusCreated, map[string]any{"agent": a, "token": token})
}

type deleteAgentReq struct {
	// Force 在 Agent 名下仍有待执行/执行中任务时，把它们退回灵感箱后再删除。
	// 不加这个开关时接口会拒绝删除并告知有多少任务受影响。
	Force bool `json:"force"`
}

// deleteAgent 删除 Agent。
func (s *Server) deleteAgent(w http.ResponseWriter, r *http.Request) {
	id, ok := parseAgentID(w, r)
	if !ok {
		return
	}
	var req deleteAgentReq
	if r.ContentLength > 0 {
		if !decodeBody(w, r, &req) {
			return
		}
	}
	res, err := s.svc.DeleteAgent(id, req.Force)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, res)
}

func (s *Server) rotateToken(w http.ResponseWriter, r *http.Request) {
	id, ok := parseAgentID(w, r)
	if !ok {
		return
	}
	token, err := s.svc.RotateToken(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]string{"token": token})
}

type enabledReq struct {
	Enabled bool `json:"enabled"`
}

type quotaReq struct {
	Quota string `json:"quota"`
}

func (s *Server) setAgentQuota(w http.ResponseWriter, r *http.Request) {
	id, ok := parseAgentID(w, r)
	if !ok {
		return
	}
	var req quotaReq
	if !decodeBody(w, r, &req) {
		return
	}
	a, err := s.svc.SetAgentQuota(id, strings.TrimSpace(req.Quota))
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, a)
}

func (s *Server) setAgentEnabled(w http.ResponseWriter, r *http.Request) {
	id, ok := parseAgentID(w, r)
	if !ok {
		return
	}
	var req enabledReq
	if !decodeBody(w, r, &req) {
		return
	}
	if err := s.svc.SetAgentEnabled(id, req.Enabled); err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- Agent 端 ----------

func (s *Server) agentAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="agent"`)
			writeErr(w, newErr(http.StatusUnauthorized, CodeUnauthorized, "缺少 Bearer Token"))
			return
		}
		agent, err := s.svc.AuthenticateAgent(strings.TrimPrefix(auth, prefix))
		if err != nil {
			writeErr(w, newErr(http.StatusUnauthorized, CodeUnauthorized, "Token 无效或 Agent 已禁用"))
			return
		}
		next.ServeHTTP(w, r.WithContext(contextWithAgent(r.Context(), agent)))
	})
}

func (s *Server) agentMe(w http.ResponseWriter, r *http.Request) {
	writeOK(w, http.StatusOK, agentFromContext(r))
}

func (s *Server) agentListTasks(w http.ResponseWriter, r *http.Request) {
	me := agentFromContext(r)
	q, err := buildAgentTaskQuery(r)
	if err != nil {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, err.Error()))
		return
	}
	tasks, err := s.svc.ListAgentTasks(me.ID, q)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]any{"items": tasks})
}

func (s *Server) agentClaim(w http.ResponseWriter, r *http.Request) {
	me := agentFromContext(r)
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	t, err := s.svc.Claim(me.ID, id)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, t)
}

func (s *Server) agentHeartbeat(w http.ResponseWriter, r *http.Request) {
	me := agentFromContext(r)
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := s.svc.Heartbeat(me.ID, id); err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]bool{"ok": true})
}

type submitReq struct {
	Summary  string   `json:"summary"`
	CostUsed *float64 `json:"cost_used"`
}

func (s *Server) agentSubmit(w http.ResponseWriter, r *http.Request) {
	me := agentFromContext(r)
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var req submitReq
	if !decodeBody(w, r, &req) {
		return
	}
	t, err := s.svc.SubmitWithCost(me.ID, id, strings.TrimSpace(req.Summary), req.CostUsed)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, t)
}

type releaseReq struct {
	Reason   string   `json:"reason"`
	Quota    string   `json:"quota"`
	CostUsed *float64 `json:"cost_used"`
}

func (s *Server) agentRelease(w http.ResponseWriter, r *http.Request) {
	me := agentFromContext(r)
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var req releaseReq
	if !decodeBody(w, r, &req) {
		return
	}
	t, err := s.svc.ReleaseWithCost(me.ID, id, strings.TrimSpace(req.Reason), strings.TrimSpace(req.Quota), req.CostUsed)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, t)
}

func (s *Server) agentReportQuota(w http.ResponseWriter, r *http.Request) {
	me := agentFromContext(r)
	var req quotaReq
	if !decodeBody(w, r, &req) {
		return
	}
	a, err := s.svc.SetAgentQuota(me.ID, strings.TrimSpace(req.Quota))
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, a)
}

func (s *Server) agentNote(w http.ResponseWriter, r *http.Request) {
	me := agentFromContext(r)
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var req noteReq
	if !decodeBody(w, r, &req) {
		return
	}
	t, err := s.svc.AddAgentNote(me.ID, me.Name, id, strings.TrimSpace(req.Content))
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, t)
}

// agentListEvents Agent 读取自己任务的时间线（驳回原因、人类备注）。
// 分页与排序同人类端 listEvents，心跳事件始终过滤。
func (s *Server) agentListEvents(w http.ResponseWriter, r *http.Request) {
	me := agentFromContext(r)
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	page, err := parsePage(r.URL.Query())
	if err != nil {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, err.Error()))
		return
	}
	size := page.Limit
	if size > store.MaxEventPageSize {
		size = store.MaxEventPageSize
	}
	items, total, err := s.svc.ListAgentTaskEvents(me.ID, store.EventFilter{
		TaskID: id,
		Page:   page.Page,
		Size:   size,
		Desc:   true,
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, listEventsData{
		Items: items, Total: total, Page: page.Page, PageSize: size,
		HasMore: page.Page*size < total,
	})
}

// ---------- SPA 静态资源 ----------

func (s *Server) spaHandler() http.Handler {
	sub, err := fs.Sub(s.static, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean(r.URL.Path)
		p := strings.TrimPrefix(clean, "/")
		if p == "" || p == "." {
			p = "index.html"
		}

		if _, err := fs.Stat(sub, p); err == nil {
			switch {
			case strings.HasPrefix(p, "assets/"):
				// 内容寻址文件名，可长期缓存。
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			case p == "index.html" || p == "sw.js" || p == "manifest.webmanifest":
				// ⚠️ index.html 永远不缓存：它引用带 hash 的资源文件名，
				// 缓存住它等于把用户钉在旧版本上，资源更新了也不会生效。
				w.Header().Set("Cache-Control", "no-cache")
			}
			fileServer.ServeHTTP(w, r)
			return
		}

		// 未知 /api 路径：404 JSON，绝不回退到前端页面。
		// 否则接口错误会被 200 + HTML 掩盖，前端报出极具迷惑性的解析错误。
		if clean == "/api" || strings.HasPrefix(clean, "/api/") {
			writeErr(w, newErr(http.StatusNotFound, CodeRouteNotFound, "接口不存在"))
			return
		}

		// 只有浏览器导航请求才回退 index.html，交给前端路由。
		// 重构前所有非 /api 路径都回退，于是请求一个不存在的 .js 会拿到
		// index.html —— 这正是要修的 bug。
		if !wantsHTML(r) {
			http.NotFound(w, r)
			return
		}
		// index.html 永远不缓存，否则前端发版后用户拿到旧壳。
		w.Header().Set("Cache-Control", "no-cache")
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}

// wantsHTML 判断请求是否来自浏览器导航。
func wantsHTML(r *http.Request) bool {
	// 带扩展名 = 静态资源，永不回退。
	if path.Ext(r.URL.Path) != "" {
		return false
	}
	if r.Header.Get("Sec-Fetch-Dest") == "document" {
		return true
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// withCORS 允许本机任意端口跨源访问，仅调试用。
// 默认关闭：SPA 与 API 同源，Vite dev server 已代理 /api。
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && isLocalOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Last-Event-ID")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLocalOrigin 只放行本机来源，不接受通配。
func isLocalOrigin(origin string) bool {
	return strings.HasPrefix(origin, "http://localhost") ||
		strings.HasPrefix(origin, "http://127.0.0.1") ||
		strings.HasPrefix(origin, "http://[::1]")
}
