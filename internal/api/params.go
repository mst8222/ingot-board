package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mst8222/ingot-board/internal/domain"
	"github.com/mst8222/ingot-board/internal/store"
)

// ---------- 基础解析 ----------

func parsePositiveInt(raw string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n <= 0 {
		return 0, errors.New("不是合法的正整数: " + raw)
	}
	return n, nil
}

func parseInt(raw string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(raw))
}

// parseCSV 拆分逗号分隔参数，丢弃空段。
func parseCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseStatuses(raw string) ([]domain.Status, error) {
	parts := parseCSV(raw)
	if len(parts) == 0 {
		return nil, nil
	}
	out := make([]domain.Status, 0, len(parts))
	for _, p := range parts {
		if !domain.IsValidStatus(p) {
			return nil, fmt.Errorf("非法状态: %s", p)
		}
		out = append(out, domain.Status(p))
	}
	return out, nil
}

func parsePriorities(raw string) ([]string, error) {
	parts := parseCSV(raw)
	if len(parts) == 0 {
		return nil, nil
	}
	for _, p := range parts {
		if !domain.ValidPriority(p) {
			return nil, fmt.Errorf("非法优先级: %s", p)
		}
	}
	return parts, nil
}

// parseProjectIDs 解析 projects 查询参数（CSV 正整数）。
func parseProjectIDs(raw string) ([]int64, error) {
	parts := parseCSV(raw)
	if len(parts) == 0 {
		return nil, nil
	}
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		id, err := parsePositiveInt(p)
		if err != nil {
			return nil, fmt.Errorf("非法 projects 取值: %s", p)
		}
		out = append(out, id)
	}
	return out, nil
}

// parseSort 解析 sort 查询参数。
//
// 默认值必须是 store.SortManual（手动顺序），与 sortExprs 的回退、
// meta.sort_fields 的首项、OpenAPI 的 default 四处一致。
// 这四处此前分别是 id_desc / id_desc / updated_desc / updated_desc ——
// "看板默认按什么排"竟然有四个答案。
func parseSort(raw string) (store.SortField, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return store.SortManual, nil
	}
	if !store.ValidSort(raw) {
		return "", fmt.Errorf("非法排序字段: %s", raw)
	}
	return store.SortField(raw), nil
}

// parseBoolTrue 解析形如 "1"/"true" 的开关，只认真值。
func parseBoolTrue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// ---------- 分页 ----------

// pageParams 是分页请求的公共部分。
type pageParams struct {
	Page  int
	Limit int
}

func parsePage(q map[string][]string) (pageParams, error) {
	var p pageParams
	p.Page, p.Limit = 1, store.MaxPageSize
	if raw := firstOf(q, "page"); raw != "" {
		n, err := parseInt(raw)
		if err != nil || n < 1 {
			return p, fmt.Errorf("非法 page: %s", raw)
		}
		p.Page = n
	}
	if raw := firstOf(q, "page_size"); raw != "" {
		n, err := parseInt(raw)
		if err != nil || n < 1 {
			return p, fmt.Errorf("非法 page_size: %s", raw)
		}
		if n > store.MaxPageSize {
			return p, fmt.Errorf("page_size 不得超过 %d", store.MaxPageSize)
		}
		p.Limit = n
	}
	return p, nil
}

func (p pageParams) offset() int { return (p.Page - 1) * p.Limit }

func firstOf(q map[string][]string, key string) string {
	if v, ok := q[key]; ok && len(v) > 0 {
		return v[0]
	}
	return ""
}

// ---------- 任务过滤器 ----------

// buildTaskFilter 从查询参数构造过滤器。
//
// ⚠️ assignee 与 unassigned 是两个独立参数：
//   - assignee=3 → 指派给 id=3 的 Agent
//   - unassigned=1 → 未指派（assignee_agent_id IS NULL）
//
// 早期实现只有 assignee，且 assignee=0 会被当成"未指派"的隐式写法，
// 实际上它是一个永远无命中的 agent id 过滤。这个坑由
// TestAssigneeZeroIsNotUnassigned 固定下来。
func buildTaskFilter(r *http.Request) (store.TaskFilter, error) {
	q := r.URL.Query()
	var f store.TaskFilter

	statuses, err := parseStatuses(firstOf(q, "status"))
	if err != nil {
		return f, err
	}
	f.Statuses = statuses

	prios, err := parsePriorities(firstOf(q, "priority"))
	if err != nil {
		return f, err
	}
	f.Priorities = prios

	if raw := firstOf(q, "assignee"); raw != "" {
		id, err := parsePositiveInt(raw)
		if err != nil {
			return f, errors.New("非法 assignee")
		}
		f.Assignee = &id
	}
	f.Unassigned = parseBoolTrue(firstOf(q, "unassigned"))

	projectIDs, err := parseProjectIDs(firstOf(q, "projects"))
	if err != nil {
		return f, err
	}
	f.ProjectIDs = projectIDs
	f.NoProject = parseBoolTrue(firstOf(q, "no_project"))

	f.Query = strings.TrimSpace(firstOf(q, "q"))

	archived, err := store.ArchivedModeFromQuery(firstOf(q, "archived"))
	if err != nil {
		return f, err
	}
	f.Archived = archived

	sortField, err := parseSort(firstOf(q, "sort"))
	if err != nil {
		return f, err
	}
	f.Sort = sortField

	page, err := parsePage(q)
	if err != nil {
		return f, err
	}
	f.Limit = page.Limit
	f.Offset = page.offset()

	return f, nil
}

// buildAgentTaskQuery 构造 Agent 端的查询参数。
// 注意这里没有 assignee —— Agent 只能看自己的任务，指派由服务端注入。
func buildAgentTaskQuery(r *http.Request) (store.AgentTaskQuery, error) {
	q := r.URL.Query()
	var out store.AgentTaskQuery

	statuses, err := parseStatuses(firstOf(q, "status"))
	if err != nil {
		return out, err
	}
	out.Statuses = statuses

	prios, err := parsePriorities(firstOf(q, "priority"))
	if err != nil {
		return out, err
	}
	out.Priorities = prios

	out.Query = strings.TrimSpace(firstOf(q, "q"))

	sortField, err := parseSort(firstOf(q, "sort"))
	if err != nil {
		return out, err
	}
	out.Sort = sortField

	page, err := parsePage(q)
	if err != nil {
		return out, err
	}
	out.Limit = page.Limit
	out.Offset = page.offset()
	return out, nil
}
