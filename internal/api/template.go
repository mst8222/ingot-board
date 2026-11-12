package api

import (
	"net/http"

	"github.com/mst8222/ingot-board/internal/store"
)

// 029 人类端任务模板。Agent 端零新增端点。

func parseTemplateID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := parseIDRaw(r)
	if err != nil {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, "非法的模板 ID"))
		return 0, false
	}
	return id, true
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.Templates(r.URL.Query().Get("include_archived") == "1")
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"name"`
		SourceTaskID int64  `json:"source_task_id"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.SourceTaskID <= 0 {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, "source_task_id 必须是正整数"))
		return
	}
	t, err := s.svc.SaveAsTemplate(req.SourceTaskID, req.Name)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusCreated, t)
}

func (s *Server) updateTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTemplateID(w, r)
	if !ok {
		return
	}
	var p store.TemplatePatch
	if !decodeBody(w, r, &p) {
		return
	}
	t, err := s.svc.UpdateTemplate(id, p)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, t)
}

func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTemplateID(w, r)
	if !ok {
		return
	}
	if err := s.svc.DeleteTemplate(id); err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) instantiateTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTemplateID(w, r)
	if !ok {
		return
	}
	t, err := s.svc.InstantiateTemplate(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusCreated, t)
}
