package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mst8222/ingot-board/internal/app"
)

// sseHeartbeatInterval 服务端心跳间隔。
// 必须显著小于任何可能存在的反向代理空闲超时，否则连接会被中间层悄悄掐断，
// 而前端把它当成正常断开，触发无谓的重连与全量刷新。
const sseHeartbeatInterval = 25 * time.Second

// sseWriteTimeout 单次 SSE 写入超时，防止慢客户端把 handler 永久挂住。
const sseWriteTimeout = 10 * time.Second

// stream SSE 实时事件流。
//
// 重构前的两个问题：
//  1. publish 在订阅者缓冲满时**静默丢弃**事件，注释写"由前端自行全量刷新兜底"，
//     但那层兜底并不存在 —— 断线或慢客户端会永久丢事件，看板与服务端逐渐漂移。
//  2. 不支持 Last-Event-ID，断线后只能全量刷新或（更糟）什么都不做。
//
// 现在：每条变更带单调递增的 seq，事件写入环形缓冲。
// 断线重连时浏览器自动带上 Last-Event-ID，服务端按 seq 精确补发；
// 补不上（落后超出缓冲窗口）时显式发 resync 通知前端全量刷新。
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, newErr(http.StatusInternalServerError, CodeInternal, "服务端不支持 SSE"))
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	// 让中间的反向代理不要缓冲这个流。
	h.Set("X-Accel-Buffering", "no")

	lastSeq := parseLastEventID(r)

	// ⚠️ 顺序至关重要：先注册订阅，再取回放缓冲。
	// 若反过来，注册与取快照之间产生的变更既不在快照里、也不会被回放覆盖，
	// 中间这段窗口的事件就永久丢失了。
	subID, ch := s.svc.Subscribe()
	defer s.svc.Unsubscribe(subID)

	replay, needResync := s.svc.ReplaySince(lastSeq)
	seq := s.svc.CurrentSeq()

	// ready 帧携带服务端当前 seq，并带上 id:，这样"连上即断"的客户端
	// 也持有一个可续传的位置。
	fmt.Fprintf(w, "retry: 3000\nid: %d\nevent: ready\ndata: {\"seq\":%d}\n\n", seq, seq)
	if needResync {
		// 缓冲覆盖不到客户端要的区间，必须让它全量刷新。
		fmt.Fprintf(w, "event: resync\ndata: {\"reason\":\"buffer_gap\",\"last_event_id\":%d}\n\n", lastSeq)
	} else {
		for _, c := range replay {
			writeChange(w, c)
		}
	}
	flusher.Flush()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case change, open := <-ch:
			if !open {
				return
			}
			writeChange(w, change)
			flusher.Flush()

			// 本轮事件已投递：若此前标记过慢消费者丢失，通知前端全量刷新。
			// 必须在事件投递**之后**检查，因为只有此刻客户端才真正落后了。
			if s.svc.DrainOverflow(subID) {
				if _, err := fmt.Fprint(w,
					"event: resync\ndata: {\"reason\":\"slow_consumer\"}\n\n"); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}

// writeChange 输出单条变更。
//
// id: 必须是十进制 seq 且不含空格与换行 —— 这是 SSE 规范里
// Last-Event-ID 能被正确回传的前提。
func writeChange(w http.ResponseWriter, c app.Change) {
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "id: %d\nevent: change\ndata: %s\n\n", c.Seq, data)
}

// parseLastEventID 读取断点续传位置。
//
// 优先用标准 header（EventSource 重连时由浏览器自动携带，无法用 JS 设置）；
// 同时支持 last_event_id 查询参数，方便 curl 与手工调试。
func parseLastEventID(r *http.Request) int64 {
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("last_event_id")
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
