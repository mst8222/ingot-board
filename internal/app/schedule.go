package app

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/mst8222/ingot-board/internal/dispatcher"
	"github.com/mst8222/ingot-board/internal/store"
)

// 031 定时派发：单进程内扫描 next_run_at，不引第三方 cron、不新起监听。
// 服务关闭期间不执行；重启时的首次 tick 对每条到期计划最多补偿一次。

const DefaultSchedulerInterval = 30 * time.Second

const scheduleTimeLayout = "2006-01-02T15:04:05.000Z"

func formatScheduleTime(t time.Time) string { return t.UTC().Format(scheduleTimeLayout) }

func (s *Service) Schedules() ([]store.Schedule, error) { return s.store.ListSchedules() }

func (s *Service) Schedule(id int64) (*store.Schedule, error) { return s.store.GetSchedule(id) }

func (s *Service) CreateSchedule(sc *store.Schedule) (*store.Schedule, error) {
	if sc.NextRunAt == "" {
		sc.NextRunAt = nextRunAt(*sc, time.Now().UTC())
	}
	return s.store.CreateSchedule(sc)
}

// UpdateSchedule 在周期被改动时重算 next_run_at，避免旧时刻立刻又触发一次。
func (s *Service) UpdateSchedule(id int64, p store.SchedulePatch) (*store.Schedule, error) {
	if p.IntervalMinutes != nil || p.DailyAt != nil {
		cur, err := s.store.GetSchedule(id)
		if err != nil {
			return nil, err
		}
		merged := *cur
		if p.IntervalMinutes != nil {
			if *p.IntervalMinutes <= 0 {
				merged.IntervalMinutes = nil
			} else {
				v := *p.IntervalMinutes
				merged.IntervalMinutes = &v
			}
		}
		if p.DailyAt != nil {
			if *p.DailyAt == "" {
				merged.DailyAt = nil
			} else {
				v := *p.DailyAt
				merged.DailyAt = &v
			}
		}
		next := nextRunAt(merged, time.Now().UTC())
		p.NextRunAt = &next
	}
	return s.store.UpdateSchedule(id, p)
}

func (s *Service) DeleteSchedule(id int64) error { return s.store.DeleteSchedule(id) }

// nextRunAt 从「当前时刻」往后算下一个周期，不回放错过的历史区间。
// interval_minutes 直接加分钟；daily_at 取当天或次日的 HH:MM（UTC）。
func nextRunAt(sc store.Schedule, from time.Time) string {
	from = from.UTC()
	if sc.IntervalMinutes != nil && *sc.IntervalMinutes > 0 {
		return formatScheduleTime(from.Add(time.Duration(*sc.IntervalMinutes) * time.Minute))
	}
	if sc.DailyAt != nil && len(*sc.DailyAt) == 5 {
		hour, errH := strconv.Atoi((*sc.DailyAt)[:2])
		minute, errM := strconv.Atoi((*sc.DailyAt)[3:])
		if errH == nil && errM == nil && hour >= 0 && hour < 24 && minute >= 0 && minute < 60 {
			candidate := time.Date(from.Year(), from.Month(), from.Day(),
				hour, minute, 0, 0, time.UTC)
			if !candidate.After(from) {
				candidate = candidate.AddDate(0, 0, 1)
			}
			return formatScheduleTime(candidate)
		}
	}
	return formatScheduleTime(from.Add(DefaultSchedulerInterval))
}

func (s *Service) StartScheduler(ctx context.Context, interval time.Duration) {
	s.schedulerOnce.Do(func() {
		if s.schedulerLoopStartHook != nil {
			s.schedulerLoopStartHook()
		}
		if interval <= 0 {
			interval = DefaultSchedulerInterval
		}
		go s.schedulerLoop(ctx, interval)
	})
}

func (s *Service) schedulerLoop(ctx context.Context, interval time.Duration) {
	defer func() {
		if s.schedulerDone != nil {
			close(s.schedulerDone)
		}
	}()
	// 启动即补偿一次：错过的多个周期只按 last_fired_at 锚点补偿一回。
	s.schedulerTick(time.Now().UTC())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.schedulerTick(time.Now().UTC())
		}
	}
}

func (s *Service) schedulerTick(now time.Time) {
	due, err := s.store.DueSchedules(formatScheduleTime(now))
	if err != nil {
		log.Printf("定时计划扫描失败: %v", err)
		return
	}
	for _, sc := range due {
		s.fireSchedule(sc, now)
	}
}

// fireSchedule 先抢 (schedule_id, next_run_at) 幂等槽，再推进 next_run_at，
// 最后才实例化。顺序保证失败或重启都不会反复生成任务。
func (s *Service) fireSchedule(sc store.Schedule, now time.Time) {
	claimed, err := s.store.ClaimScheduleFire(sc.ID, sc.NextRunAt, now)
	if err != nil {
		log.Printf("定时计划 %d 抢触发槽失败: %v", sc.ID, err)
		return
	}
	if !claimed {
		return
	}
	if err := s.store.AdvanceSchedule(sc.ID, nextRunAt(sc, now), now); err != nil {
		log.Printf("定时计划 %d 推进失败: %v", sc.ID, err)
		return
	}
	mode := sc.DispatchMode
	if mode == store.ScheduleModeInherit || mode == "" {
		mode = s.currentMode()
	}
	// inherit 解析后若不是显式 auto，一律按最保守的 observe 处理。
	if mode != dispatcher.ModeAuto {
		mode = dispatcher.ModeObserve
	}
	tpl, err := s.store.GetTemplate(sc.TemplateID)
	if err != nil || tpl.IsArchived() {
		log.Printf("定时计划 %d 的模板不可用，跳过本次触发: %v", sc.ID, err)
		return
	}
	task, err := s.InstantiateTemplateForSchedule(context.Background(), sc.TemplateID, mode, sc.ID)
	if err != nil {
		log.Printf("定时计划 %d 实例化失败: %v", sc.ID, err)
		return
	}
	if err := s.store.SetScheduleFireTask(sc.ID, sc.NextRunAt, task.ID); err != nil {
		log.Printf("定时计划 %d 回填触发槽失败: %v", sc.ID, err)
	}
}
