package api

import (
	"net/http"

	"github.com/mst8222/ingot-board/internal/dispatcher"
)

// 032 总管派发：全部人类端、本机回环，无登录，不进 phoneSharedRoutes。

type dispatchSettingsResp struct {
	Mode  string   `json:"mode"`
	Modes []string `json:"modes"`
}

func (s *Server) getDispatchSettings(w http.ResponseWriter, r *http.Request) {
	mode, err := s.svc.DispatchMode()
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, dispatchSettingsResp{
		Mode: mode, Modes: []string{dispatcher.ModeObserve, dispatcher.ModeAuto},
	})
}

func (s *Server) updateDispatchSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := s.svc.SetDispatchMode(req.Mode); err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, dispatchSettingsResp{
		Mode: req.Mode, Modes: []string{dispatcher.ModeObserve, dispatcher.ModeAuto},
	})
}

func (s *Server) getDispatchSuggestion(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	d, err := s.svc.Suggestion(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, d)
}

func (s *Server) acceptDispatchSuggestion(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var req struct {
		DecisionID int64 `json:"decision_id"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.DecisionID <= 0 {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, "decision_id 必须是正整数"))
		return
	}
	t, err := s.svc.AcceptSuggestion(id, req.DecisionID)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, t)
}
