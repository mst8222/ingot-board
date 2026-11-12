package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/mst8222/ingot-board/internal/domain"
)

// 031 定时计划：计划只是「人预先授权的代派发」，实例化仍走 029 模板与 032 派发链路。

const (
	ScheduleModeInherit = "inherit"
	ScheduleModeObserve = "observe"
	ScheduleModeAuto    = "auto"
)

func ValidScheduleMode(m string) bool {
	return m == ScheduleModeInherit || m == ScheduleModeObserve || m == ScheduleModeAuto
}

type Schedule struct {
	ID              int64   `json:"id"`
	Name            string  `json:"name"`
	TemplateID      int64   `json:"template_id"`
	DispatchMode    string  `json:"dispatch_mode"`
	IntervalMinutes *int64  `json:"interval_minutes"`
	DailyAt         *string `json:"daily_at"`
	Enabled         bool    `json:"enabled"`
	NextRunAt       string  `json:"next_run_at"`
	LastFiredAt     *string `json:"last_fired_at"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

// SchedulePatch 的 nil 表示不改；IntervalMinutes 指向 0、DailyAt 指向空串表示清空。
type SchedulePatch struct {
	Name            *string `json:"name"`
	TemplateID      *int64  `json:"template_id"`
	DispatchMode    *string `json:"dispatch_mode"`
	IntervalMinutes *int64  `json:"interval_minutes"`
	DailyAt         *string `json:"daily_at"`
	Enabled         *bool   `json:"enabled"`
	NextRunAt       *string `json:"next_run_at"`
}

const scheduleColumns = `id, name, template_id, dispatch_mode, interval_minutes, daily_at,
	enabled, next_run_at, last_fired_at, created_at, updated_at`

func scanSchedule(row interface{ Scan(dest ...any) error }) (*Schedule, error) {
	var sc Schedule
	var interval sql.NullInt64
	var daily, lastFired sql.NullString
	var enabled int
	if err := row.Scan(&sc.ID, &sc.Name, &sc.TemplateID, &sc.DispatchMode,
		&interval, &daily, &enabled, &sc.NextRunAt, &lastFired,
		&sc.CreatedAt, &sc.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	if interval.Valid {
		v := interval.Int64
		sc.IntervalMinutes = &v
	}
	if daily.Valid && daily.String != "" {
		v := daily.String
		sc.DailyAt = &v
	}
	if lastFired.Valid && lastFired.String != "" {
		v := lastFired.String
		sc.LastFiredAt = &v
	}
	sc.Enabled = enabled != 0
	return &sc, nil
}

func validDailyAt(v string) bool {
	if len(v) != 5 || v[2] != ':' {
		return false
	}
	_, err := time.Parse("15:04", v)
	return err == nil
}

// validateSchedule 校验周期与模板；模板必须存在且未归档，避免计划变成空转。
func (s *Store) validateSchedule(sc *Schedule) error {
	sc.Name = strings.TrimSpace(sc.Name)
	if sc.Name == "" {
		return errors.Join(domain.ErrInvalidInput, errors.New("计划名称不能为空"))
	}
	if sc.TemplateID <= 0 {
		return errors.Join(domain.ErrInvalidInput, errors.New("template_id 必须是正整数"))
	}
	if sc.DispatchMode == "" {
		sc.DispatchMode = ScheduleModeInherit
	}
	if !ValidScheduleMode(sc.DispatchMode) {
		return errors.Join(domain.ErrInvalidInput, errors.New("dispatch_mode 只能是 inherit / observe / auto"))
	}
	hasInterval := sc.IntervalMinutes != nil && *sc.IntervalMinutes > 0
	hasDaily := sc.DailyAt != nil && *sc.DailyAt != ""
	if hasInterval == hasDaily {
		return errors.Join(domain.ErrInvalidInput,
			errors.New("interval_minutes 与 daily_at 必须恰选其一，且间隔须为正整数分钟"))
	}
	if sc.IntervalMinutes != nil && *sc.IntervalMinutes <= 0 {
		return errors.Join(domain.ErrInvalidInput, errors.New("interval_minutes 必须是正整数"))
	}
	if hasDaily && !validDailyAt(*sc.DailyAt) {
		return errors.Join(domain.ErrInvalidInput, errors.New("daily_at 必须是 HH:MM"))
	}
	tpl, err := s.GetTemplate(sc.TemplateID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return errors.Join(domain.ErrInvalidInput, errors.New("绑定的模板不存在"))
		}
		return err
	}
	if tpl.IsArchived() {
		return errors.Join(domain.ErrInvalidInput, errors.New("绑定的模板已归档"))
	}
	if sc.NextRunAt == "" {
		sc.NextRunAt = formatTime(nowUTC())
	}
	return nil
}

func (s *Store) CreateSchedule(sc *Schedule) (*Schedule, error) {
	if err := s.validateSchedule(sc); err != nil {
		return nil, err
	}
	now := formatTime(nowUTC())
	var interval, daily any
	if sc.IntervalMinutes != nil {
		interval = *sc.IntervalMinutes
	}
	if sc.DailyAt != nil {
		daily = *sc.DailyAt
	}
	// 新建一律启用；停用只能通过 PATCH，避免调用方零值让计划静默空转。
	res, err := s.db.Exec(
		`INSERT INTO schedules(name, template_id, dispatch_mode, interval_minutes, daily_at,
			enabled, next_run_at, created_at, updated_at) VALUES(?,?,?,?,?,1,?,?,?)`,
		sc.Name, sc.TemplateID, sc.DispatchMode, interval, daily,
		sc.NextRunAt, now, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetSchedule(id)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Store) GetSchedule(id int64) (*Schedule, error) {
	return scanSchedule(s.db.QueryRow(`SELECT `+scheduleColumns+` FROM schedules WHERE id=?`, id))
}

func (s *Store) ListSchedules() ([]Schedule, error) {
	rows, err := s.db.Query(`SELECT ` + scheduleColumns + ` FROM schedules ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Schedule, 0)
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sc)
	}
	return out, rows.Err()
}

func (s *Store) UpdateSchedule(id int64, p SchedulePatch) (*Schedule, error) {
	cur, err := s.GetSchedule(id)
	if err != nil {
		return nil, err
	}
	if p.Name != nil {
		cur.Name = *p.Name
	}
	if p.TemplateID != nil {
		cur.TemplateID = *p.TemplateID
	}
	if p.DispatchMode != nil {
		if !ValidScheduleMode(*p.DispatchMode) {
			return nil, errors.Join(domain.ErrInvalidInput, errors.New("dispatch_mode 只能是 inherit / observe / auto"))
		}
		cur.DispatchMode = *p.DispatchMode
	}
	if p.IntervalMinutes != nil {
		if *p.IntervalMinutes <= 0 {
			cur.IntervalMinutes = nil
		} else {
			v := *p.IntervalMinutes
			cur.IntervalMinutes = &v
		}
	}
	if p.DailyAt != nil {
		if *p.DailyAt == "" {
			cur.DailyAt = nil
		} else {
			v := *p.DailyAt
			cur.DailyAt = &v
		}
	}
	if p.Enabled != nil {
		cur.Enabled = *p.Enabled
	}
	if p.NextRunAt != nil && *p.NextRunAt != "" {
		cur.NextRunAt = *p.NextRunAt
	}
	// 校验会重新检查模板可用性；归档模板不能通过 PATCH 复活计划。
	if err := s.validateSchedule(cur); err != nil {
		return nil, err
	}
	var interval, daily any
	if cur.IntervalMinutes != nil {
		interval = *cur.IntervalMinutes
	}
	if cur.DailyAt != nil {
		daily = *cur.DailyAt
	}
	res, err := s.db.Exec(
		`UPDATE schedules SET name=?, template_id=?, dispatch_mode=?, interval_minutes=?,
			daily_at=?, enabled=?, next_run_at=?, updated_at=? WHERE id=?`,
		cur.Name, cur.TemplateID, cur.DispatchMode, interval, daily,
		boolToInt(cur.Enabled), cur.NextRunAt, formatTime(nowUTC()), id)
	if err != nil {
		return nil, err
	}
	if err := requireRow(res); err != nil {
		return nil, err
	}
	return s.GetSchedule(id)
}

func (s *Store) DeleteSchedule(id int64) error {
	res, err := s.db.Exec(`DELETE FROM schedules WHERE id=?`, id)
	if err != nil {
		return err
	}
	return requireRow(res)
}

// DueSchedules 取已到期且启用的计划。时间戳定长毫秒，字符串比较等价于时间比较。
func (s *Store) DueSchedules(now string) ([]Schedule, error) {
	rows, err := s.db.Query(
		`SELECT `+scheduleColumns+` FROM schedules WHERE enabled=1 AND next_run_at<=? ORDER BY id`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Schedule, 0)
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sc)
	}
	return out, rows.Err()
}

// ClaimScheduleFire 用主键抢触发槽；重复抢返回 false 而不是主键冲突错误。
func (s *Store) ClaimScheduleFire(scheduleID int64, nextRunAt string, now time.Time) (bool, error) {
	res, err := s.db.Exec(
		`INSERT OR IGNORE INTO schedule_fires(schedule_id, next_run_at, fired_at) VALUES(?,?,?)`,
		scheduleID, nextRunAt, formatTime(now))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (s *Store) SetScheduleFireTask(scheduleID int64, nextRunAt string, taskID int64) error {
	_, err := s.db.Exec(
		`UPDATE schedule_fires SET task_id=? WHERE schedule_id=? AND next_run_at=?`,
		taskID, scheduleID, nextRunAt)
	return err
}

func (s *Store) CountScheduleFires(scheduleID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM schedule_fires WHERE schedule_id=?`, scheduleID).Scan(&n)
	return n, err
}

// AdvanceSchedule 不管实例化成功与否都推进到下一个未来时刻，避免失败计划反复重试。
func (s *Store) AdvanceSchedule(id int64, nextRunAt string, now time.Time) error {
	res, err := s.db.Exec(
		`UPDATE schedules SET next_run_at=?, last_fired_at=?, updated_at=? WHERE id=?`,
		nextRunAt, formatTime(now), formatTime(now), id)
	if err != nil {
		return err
	}
	return requireRow(res)
}

