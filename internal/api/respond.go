// Package api 的响应信封与错误映射。
//
// 重构动机：原实现有 4 种响应形态混用（裸实体 / {tasks:[]} / {task,events} / {ok:true}），
// 且 writeError 把 4 个语义完全不同的错误都映射到 409，前端只能拿到一串中文文案，
// 无法区分"状态不允许流转" / "先指派 Agent" / "不是租约持有者" / "版本冲突"。
// 多 Agent 并发下版本冲突是高频场景，用户无法据此恢复。
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/mst8222/ingot-board/internal/domain"
)

// envelope 是 /api/v1 的统一响应包装。
type envelope struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data,omitempty"`
	// Details 承载 errors.Join 产生的其余行。业务层约定是
	// errors.Join(sentinel, 具体原因)，Error() 用 \n 拼成
	// "请求参数不合法\n标题不能为空"；最具体的一行提升为 Msg，
	// sentinel 与其他补充行归入这里，前端可逐行展示。
	Details []string `json:"details,omitempty"`
}

// 机器可读错误码。前端据此分支处理，**不要匹配中文文案**。
const (
	CodeOK                = "OK"
	CodeInvalidInput      = "INVALID_INPUT"
	CodeNotFound          = "NOT_FOUND"
	CodeInvalidTransition = "INVALID_TRANSITION"
	CodeTaskConflict      = "TASK_CONFLICT"
	CodeTaskNotAssigned   = "TASK_NOT_ASSIGNED"
	CodeNotLeaseHolder    = "NOT_LEASE_HOLDER"
	CodeAgentDisabled     = "AGENT_DISABLED"
	CodeUnauthorized      = "UNAUTHORIZED"
	CodeForbidden         = "FORBIDDEN"
	CodeProjectInUse      = "PROJECT_IN_USE"
	CodeWorkdirBound      = "WORKDIR_BOUND"
	CodeRouteNotFound     = "ROUTE_NOT_FOUND"
	CodeInternal          = "INTERNAL"
	CodeInsufficientBudget = "INSUFFICIENT_BUDGET"
	CodeSuggestionStale   = "SUGGESTION_STALE"
	CodeHandoffNotAllowed = "HANDOFF_NOT_ALLOWED"
)

// apiErr 携带 HTTP 状态与机器可读错误码。
type apiErr struct {
	HTTP    int
	Code    string
	Msg     string
	Details []string
}

func (e *apiErr) Error() string { return e.Msg }

func newErr(httpStatus int, code, msg string, details ...string) *apiErr {
	if len(details) == 0 {
		details = nil
	}
	return &apiErr{HTTP: httpStatus, Code: code, Msg: msg, Details: details}
}

// toAPIError 把 domain sentinel 映射为 (HTTP 状态, 机器可读 code, 明细)。
//
// 关键改动：原本共用 409 的四个 sentinel 现在各自有独立 code，
// 前端可以据此给出针对性的恢复动作（冲突→提示刷新并重载任务）。
//
// msg 取「最具体的那一行」而不是第一行。业务层的约定是
// errors.Join(sentinel, 具体原因)，Error() 拼成
// "请求参数不合法\n标题不能为空"。若把第一行当 msg，前端主提示就变成
// 千篇一律的"请求参数不合法"，真正可执行的"标题不能为空"被降级进 details；
// sentinel 已由 code 表达，重复展示没有信息量。因此这里取末行做 msg，
// 其余行（含 sentinel）归入 details。
func toAPIError(err error) *apiErr {
	if err == nil {
		return nil
	}
	parts := strings.Split(err.Error(), "\n")

	var httpStatus int
	var code string
	switch {
	case errors.Is(err, domain.ErrNotFound):
		httpStatus, code = http.StatusNotFound, CodeNotFound
	case errors.Is(err, domain.ErrInvalidInput):
		httpStatus, code = http.StatusBadRequest, CodeInvalidInput
	case errors.Is(err, domain.ErrInvalidStatus):
		httpStatus, code = http.StatusConflict, CodeInvalidTransition
	case errors.Is(err, domain.ErrConflict):
		httpStatus, code = http.StatusConflict, CodeTaskConflict
	case errors.Is(err, domain.ErrNotAssigned):
		httpStatus, code = http.StatusConflict, CodeTaskNotAssigned
	case errors.Is(err, domain.ErrNotLeaseHolder):
		httpStatus, code = http.StatusConflict, CodeNotLeaseHolder
	case errors.Is(err, domain.ErrAgentDisabled):
		httpStatus, code = http.StatusForbidden, CodeAgentDisabled
	case errors.Is(err, domain.ErrUnauthorized):
		httpStatus, code = http.StatusUnauthorized, CodeUnauthorized
	case errors.Is(err, domain.ErrForbidden):
		httpStatus, code = http.StatusForbidden, CodeForbidden
	case errors.Is(err, domain.ErrProjectInUse):
		httpStatus, code = http.StatusConflict, CodeProjectInUse
	case errors.Is(err, domain.ErrWorkdirBound):
		httpStatus, code = http.StatusConflict, CodeWorkdirBound
	case errors.Is(err, domain.ErrInsufficientBudget):
		httpStatus, code = http.StatusConflict, CodeInsufficientBudget
	case errors.Is(err, domain.ErrSuggestionStale):
		httpStatus, code = http.StatusConflict, CodeSuggestionStale
	case errors.Is(err, domain.ErrHandoffNotAllowed):
		httpStatus, code = http.StatusConflict, CodeHandoffNotAllowed
	default:
		httpStatus, code = http.StatusInternalServerError, CodeInternal
	}

	msg := parts[len(parts)-1]
	var details []string
	if len(parts) > 1 {
		details = append(details, parts[:len(parts)-1]...)
	}
	// 单行错误（如 errors.New("任务不存在")）不该产生 details 噪声。
	if len(details) == 1 && details[0] == msg {
		details = nil
	}
	if len(details) == 0 {
		details = nil
	}
	return &apiErr{HTTP: httpStatus, Code: code, Msg: msg, Details: details}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeOK 输出成功信封。data 为 nil 时不写 data 字段。
func writeOK(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, envelope{Code: CodeOK, Msg: "ok", Data: data})
}

// writeErr 输出错误信封。
func writeErr(w http.ResponseWriter, e *apiErr) {
	writeJSON(w, e.HTTP, envelope{Code: e.Code, Msg: e.Msg, Details: e.Details})
}

// fail 把任意 error 转成信封输出，供 handler 的 `return fail(w, err)` 简写。
func fail(w http.ResponseWriter, err error) {
	writeErr(w, toAPIError(err))
}

// ---------- 请求解析辅助 ----------

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, "请求体不是合法 JSON", err.Error()))
		return false
	}
	return true
}

// parseID 解析路径中的任务 ID。
func parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := parseIDRaw(r)
	if err != nil {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, "非法的任务 ID"))
		return 0, false
	}
	return id, true
}

// parseAgentID 解析路径中的 Agent ID。
func parseAgentID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := parseIDRaw(r)
	if err != nil {
		writeErr(w, newErr(http.StatusBadRequest, CodeInvalidInput, "非法的 Agent ID"))
		return 0, false
	}
	return id, true
}

func parseIDRaw(r *http.Request) (int64, error) {
	return parsePositiveInt(r.PathValue("id"))
}
