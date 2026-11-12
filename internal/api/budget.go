package api

import (
	"net/http"
)

// 032-B 数值预算：余额录入与流水查看只在人类端。
// Agent 端没有余额写入端点，只能通过 submit/release 可选上报 cost_used。

func (s *Server) setAgentBalance(w http.ResponseWriter, r *http.Request) {
	id, ok := parseAgentID(w, r)
	if !ok {
		return
	}
	var req struct {
		Balance *float64 `json:"balance"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	a, err := s.svc.SetAgentBalance(id, req.Balance)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, a)
}

func (s *Server) listAgentLedger(w http.ResponseWriter, r *http.Request) {
	id, ok := parseAgentID(w, r)
	if !ok {
		return
	}
	if _, err := s.svc.Store().GetAgent(id); err != nil {
		fail(w, err)
		return
	}
	entries, err := s.svc.Ledger(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]any{"items": entries})
}
