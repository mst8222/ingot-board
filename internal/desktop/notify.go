package desktop

import (
	"context"

	"github.com/mst8222/ingot-board/internal/app"
	"github.com/mst8222/ingot-board/internal/domain"
)

// reviewNotice 一次"任务进入待评审"提醒。
type reviewNotice struct {
	TaskID  int64
	Title   string
	Actor   string // 提交人（Agent 名或"人类"），做通知副标题
	EventID int64  // 事件 ID，用于通知去重
}

// reviewNoticeFrom 判断一次变更是否需要弹系统通知，返回 nil 表示不需要。
//
// 只在"刚进入 review"这一刻提醒一次：
//   - 驳回回 doing、评审通过到 approved 都不该再打扰；
//   - 同一条任务在 review 列内被拖动（重排）也不发通知 —— 那是列顺序帧，
//     不是状态流转，Kind 不是 ChangeTask，天然被第一道判断挡掉。
func reviewNoticeFrom(ch app.Change) *reviewNotice {
	if ch.Kind != app.ChangeTask || ch.Task == nil {
		return nil
	}
	if ch.Task.Status != domain.StatusReview {
		return nil
	}
	// 事件帧存在时以它为准：它显式记录了 to_status。
	// 没有事件帧（兜底路径）时退回看实体当前状态，宁可多提醒一次也不错漏。
	if ch.Event != nil && ch.Event.ToStatus != nil && *ch.Event.ToStatus != domain.StatusReview {
		return nil
	}
	actor := ""
	eventID := ch.Seq
	if ch.Event != nil {
		actor = ch.Event.ActorName
		eventID = ch.Event.ID
	}
	return &reviewNotice{
		TaskID:  ch.Task.ID,
		Title:   truncate(ch.Task.Title, 80),
		Actor:   actor,
		EventID: eventID,
	}
}

// watchReviews 订阅服务变更，把待评审事件交给 send 处理。
//
// 返回的 stop 必须由调用方调用：它会取消订阅并等待 goroutine 真正退出，
// 否则关闭窗口后 goroutine 仍持有 svc 引用，服务关闭时可能向已关闭通道写入。
func watchReviews(ctx context.Context, svc *app.Service, send func(reviewNotice)) func() {
	watchCtx, stop := context.WithCancel(ctx)
	id, ch := svc.Subscribe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer svc.Unsubscribe(id)
		for {
			select {
			case <-watchCtx.Done():
				return
			case c, ok := <-ch:
				if !ok {
					return
				}
				if n := reviewNoticeFrom(c); n != nil {
					send(*n)
				}
			}
		}
	}()
	return func() {
		stop()
		<-done
	}
}

// truncate 按 rune 截断字符串并加省略号，避免通知正文被超长标题撑爆。
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(runes[:n-1]) + "…"
}
