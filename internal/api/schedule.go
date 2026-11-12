package api

import (
	"net/http"

	"github.com/mst8222/ingot-board/internal/store"
)

// 031 人类端定时计划。Agent 端零新增端点，也不进手机共享白名单。

func parseScheduleID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := parseIDRaw(r)
	if err != nil {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, "非法的计划 ID"))
		return 0, false
	}
	return id, true
}

type scheduleReq struct {
	Name            string  `json:"name"`
	TemplateID      int64   `json:"template_id"`
	DispatchMode    string  `json:"dispatch_mode"`
	IntervalMinutes *int64  `json:"interval_minutes"`
	DailyAt         *string `json:"daily_at"`
}

// schedulePatchReq 不暴露 next_run_at：下次触发时刻由服务端按周期重算。
type schedulePatchReq struct {
	Name            *string `json:"name"`
	TemplateID      *int64  `json:"template_id"`
	DispatchMode    *string `json:"dispatch_mode"`
	IntervalMinutes *int64  `json:"interval_minutes"`
	DailyAt         *string `json:"daily_at"`
	Enabled         *bool   `json:"enabled"`
}

func (s *Server) listSchedules(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.Schedules()
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) {
	var req scheduleReq
	if !decodeBody(w, r, &req) {
		return
	}
	sc, err := s.svc.CreateSchedule(&store.Schedule{
		Name:            req.Name,
		TemplateID:      req.TemplateID,
		DispatchMode:    req.DispatchMode,
		IntervalMinutes: req.IntervalMinutes,
		DailyAt:         req.DailyAt,
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusCreated, sc)
}

func (s *Server) updateSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := parseScheduleID(w, r)
	if !ok {
		return
	}
	var req schedulePatchReq
	if !decodeBody(w, r, &req) {
		return
	}
	p := store.SchedulePatch{
		Name: req.Name, TemplateID: req.TemplateID,
		DispatchMode: req.DispatchMode, IntervalMinutes: req.IntervalMinutes,
		DailyAt: req.DailyAt, Enabled: req.Enabled,
	}
	sc, err := s.svc.UpdateSchedule(id, p)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, sc)
}

func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := parseScheduleID(w, r)
	if !ok {
		return
	}
	if err := s.svc.DeleteSchedule(id); err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]bool{"ok": true})
}
