package api

import (
	"net/http"
	"strings"
)

// 项目 CRUD handlers（见 024-project）。
// 项目路由同时在桌面与手机监听上暴露（phoneSharedRoutes allow），
// 响应信封与错误映射与任务接口一致。

type projectReq struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	includeArchived := parseBoolTrue(firstOf(r.URL.Query(), "include_archived"))
	projects, err := s.svc.ListProjects(includeArchived)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, projects)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var req projectReq
	if !decodeBody(w, r, &req) {
		return
	}
	p, err := s.svc.CreateProject(req.Name, req.Color)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusCreated, p)
}

// parseProjectID 解析路径中的项目 ID。
func parseProjectID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := parseIDRaw(r)
	if err != nil {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, "非法的项目 ID"))
		return 0, false
	}
	return id, true
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProjectID(w, r)
	if !ok {
		return
	}
	var req projectReq
	if !decodeBody(w, r, &req) {
		return
	}
	p, err := s.svc.UpdateProject(id, req.Name, req.Color)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, p)
}

func (s *Server) archiveProject(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProjectID(w, r)
	if !ok {
		return
	}
	p, err := s.svc.ArchiveProject(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, p)
}

func (s *Server) unarchiveProject(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProjectID(w, r)
	if !ok {
		return
	}
	p, err := s.svc.UnarchiveProject(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, p)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProjectID(w, r)
	if !ok {
		return
	}
	if err := s.svc.DeleteProject(id); err != nil {
		// ErrProjectInUse 由 toAPIError 映射为 409/PROJECT_IN_USE。
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, nil)
}

// ---------- 025：项目 ↔ 工作目录绑定 ----------

type addProjectWorkdirReq struct {
	Workdir string `json:"workdir"`
}

func (s *Server) listProjectWorkdirs(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProjectID(w, r)
	if !ok {
		return
	}
	items, err := s.svc.ListProjectWorkdirs(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, items)
}

func (s *Server) addProjectWorkdir(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProjectID(w, r)
	if !ok {
		return
	}
	var req addProjectWorkdirReq
	if !decodeBody(w, r, &req) {
		return
	}
	item, err := s.svc.AddProjectWorkdir(id, strings.TrimSpace(req.Workdir))
	if err != nil {
		// ErrWorkdirBound → 409/WORKDIR_BOUND；目录不存在 → 400。toAPIError 已映射。
		fail(w, err)
		return
	}
	writeOK(w, http.StatusCreated, item)
}

// parseWID 解析路径中的关联行 id（{wid} 占位符）。
func parseWID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	v, err := parsePositiveInt(r.PathValue("wid"))
	if err != nil {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, "非法的目录关联 ID"))
		return 0, false
	}
	return v, true
}

func (s *Server) removeProjectWorkdir(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProjectID(w, r)
	if !ok {
		return
	}
	wid, ok := parseWID(w, r)
	if !ok {
		return
	}
	if err := s.svc.RemoveProjectWorkdir(id, wid); err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, nil)
}
