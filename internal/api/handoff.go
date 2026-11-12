package api

import (
	"net/http"
	"strings"
)

// 028 人类端转交入口。Agent 端零新增端点。

func (s *Server) handoffTask(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var req struct {
		TargetAgentID int64  `json:"target_agent_id"`
		Reason        string `json:"reason"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	t, err := s.svc.Handoff(id, req.TargetAgentID, strings.TrimSpace(req.Reason))
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, t)
}

func (s *Server) listHandoffCandidates(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	items, err := s.svc.HandoffCandidates(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]any{"items": items})
}
