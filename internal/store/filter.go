package store

import (
	"errors"
	"strings"

	"github.com/mst8222/ingot-board/internal/domain"
)

// 分页与看板的硬上限。防止误传一个巨大的 page_size 把进程内存打爆。
const (
	MaxPageSize  = 500
	MaxPerColumn = 500
)

// ArchivedMode 归档过滤模式。
type ArchivedMode uint8

const (
	// ArchivedExclude 默认：只看未归档。这是绝大多数请求想要的语义。
	ArchivedExclude ArchivedMode = iota
	// ArchivedOnly 只看已归档（"归档箱"视图）。
	ArchivedOnly
	// ArchivedAny 不过滤。
	ArchivedAny
)

// ArchivedModeFromQuery 把 archived 查询参数解析为模式。
// 取值："exclude"(默认) / "only" / "any"。
func ArchivedModeFromQuery(raw string) (ArchivedMode, error) {
	switch strings.TrimSpace(raw) {
	case "", "exclude":
		return ArchivedExclude, nil
	case "only":
		return ArchivedOnly, nil
	case "any":
		return ArchivedAny, nil
	default:
		return ArchivedExclude, errors.Join(domain.ErrInvalidInput, errors.New("非法 archived 取值: "+raw))
	}
}

// SortField 排序字段白名单成员。
type SortField string

const (
	// SortManual 手动顺序 —— 默认排序，也是**唯一允许同列拖拽重排**的排序。
	SortManual    SortField = "manual"
	SortIDDesc    SortField = "id_desc"
	SortPriority  SortField = "priority"
	SortUpdatedAt SortField = "updated_at"
	SortLease     SortField = "lease"
	SortIDAsc     SortField = "id_asc"
)

// AllowsManualOrder 报告该排序方式下同列重排是否有意义。
//
// 为什么其他排序下要禁止：sort_order 只有在 manual 排序下才被读取。
// 用户在"按优先级"视图里拖动一条卡片，服务端照存不误，但刷新后顺序
// 又回到优先级顺序 —— 表现为"拖了没反应"，正是本次要修的那类静默失败。
// 与其静默无效，不如明确拒绝并告知该切回哪个排序。
func AllowsManualOrder(f SortField) bool {
	// 空值走默认，也就是 manual。
	return f == "" || f == SortManual
}

// sortExprs 是**唯一**允许拼进 ORDER BY 的地方。
// key 全部来自常量，value 是硬编码字面量 —— 用户输入永远到不了这里。
// 这张表由 TestSortExprWhitelist 强制：未知值必须回退默认，且 SQL 中不得出现输入。
var sortExprs = map[SortField]string{
	SortManual: "sort_order ASC, id ASC",
	SortIDDesc: "id DESC",
	SortIDAsc:  "id ASC",
	// 紧急在前。用 CASE 而不是按字典序，因为字典序会排成 high < low < normal < urgent。
	SortPriority: "CASE priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 " +
		"WHEN 'normal' THEN 2 ELSE 3 END ASC, id DESC",
	SortUpdatedAt: "updated_at DESC, id DESC",
	// 有租约的排前面，且近到期的排前面（最需要关注的）。
	SortLease: "(lease_until IS NULL) ASC, lease_until ASC, id DESC",
}

// ValidSort 判断排序字段是否合法。
func ValidSort(s string) bool {
	_, ok := sortExprs[SortField(s)]
	return ok
}

// sortExpr 返回 ORDER BY 片段。未知值一律回退默认，绝不返回用户输入。
func (f TaskFilter) sortExpr() string {
	if e, ok := sortExprs[f.Sort]; ok {
		return e
	}
	return sortExprs[SortManual]
}

// TaskFilter 任务查询过滤器。所有字段均为值类型，零值表示"不过滤"。
//
// ⚠️ Assignee 是 *int64：nil 表示不过滤；指向 0 **不是**"未指派"，
// 那是一个合法的 agent id 过滤（虽然通常无命中）。筛"未指派"必须用 Unassigned。
// 这个反直觉的区分由 TestAssigneeZeroIsNotUnassigned 强制。
type TaskFilter struct {
	Statuses   []domain.Status
	Priorities []string
	Assignee   *int64
	Unassigned bool
	// ProjectIDs 项目筛选（CSV 多选）。空切片表示不过滤。
	ProjectIDs []int64
	// NoProject 筛"无项目"任务。与 ProjectIDs 可叠加（AND 语义下的并集结果）。
	NoProject bool
	// Query 模糊匹配 title / description / workdir（大小写不敏感）。
	Query    string
	Archived ArchivedMode
	Sort     SortField
	Limit    int // 0 = 不限；会被 clamp 到 MaxPageSize
	Offset   int
}

// Normalize 校验并修正非法组合，错误由 API 层映射为 400。
func (f *TaskFilter) Normalize() error {
	for _, st := range f.Statuses {
		if !domain.IsValidStatus(string(st)) {
			return errors.Join(domain.ErrInvalidInput, errors.New("非法状态: "+string(st)))
		}
	}
	for _, p := range f.Priorities {
		if !domain.ValidPriority(p) {
			return errors.Join(domain.ErrInvalidInput, errors.New("非法优先级: "+p))
		}
	}
	if f.Assignee != nil && *f.Assignee <= 0 {
		return errors.Join(domain.ErrInvalidInput, errors.New("非法 assignee"))
	}
	for _, id := range f.ProjectIDs {
		if id <= 0 {
			return errors.Join(domain.ErrInvalidInput, errors.New("非法 projects 取值"))
		}
	}
	if f.Sort != "" && !ValidSort(string(f.Sort)) {
		return errors.Join(domain.ErrInvalidInput, errors.New("非法排序字段: "+string(f.Sort)))
	}
	if f.Limit < 0 {
		f.Limit = 0
	}
	if f.Limit > MaxPageSize {
		f.Limit = MaxPageSize
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	return nil
}

// buildTaskWhere 把过滤器编译成 WHERE 子句与参数列表。
//
// 防注入约定（由 TestBuildTaskWhere 强制）：
//   - 任何用户输入只以 ? 占位符出现，绝不拼进 SQL 文本；
//   - 唯一的字符串拼接是 placeholders(n)（n 由切片长度决定，不含输入）
//     与 statusLabels 这类常量表查表，两者都不含用户输入。
func buildTaskWhere(f TaskFilter) (string, []any) {
	var conds []string
	var args []any

	if len(f.Statuses) > 0 {
		conds = append(conds, `status IN (`+placeholders(len(f.Statuses))+`)`)
		for _, st := range f.Statuses {
			args = append(args, string(st))
		}
	}
	if len(f.Priorities) > 0 {
		conds = append(conds, `priority IN (`+placeholders(len(f.Priorities))+`)`)
		for _, p := range f.Priorities {
			args = append(args, p)
		}
	}

	// Unassigned 与 Assignee 互斥，Unassigned 优先。
	// 单独判 Unassigned 是因为 assignee_agent_id IS NULL 无法用 =? 表达。
	switch {
	case f.Unassigned:
		conds = append(conds, `assignee_agent_id IS NULL`)
	case f.Assignee != nil:
		conds = append(conds, `assignee_agent_id=?`)
		args = append(args, *f.Assignee)
	}

	// 项目筛选。NoProject 单独成条件（project_id IS NULL 无法用 =? 表达），
	// 与 ProjectIDs 是 OR 之外的叠加：`projects=1` + `no_project=1` 表示
	// 「项目 1 或无项目」各任务都命中——两个条件各自为独立谓词时会 AND 成交集，
	// 所以这里把两者合并进同一个括号谓词：(in (...) OR is null)。
	if len(f.ProjectIDs) > 0 && f.NoProject {
		conds = append(conds, `(`+
			`project_id IN (`+placeholders(len(f.ProjectIDs))+`) OR project_id IS NULL)`)
		for _, id := range f.ProjectIDs {
			args = append(args, id)
		}
	} else if len(f.ProjectIDs) > 0 {
		conds = append(conds, `project_id IN (`+placeholders(len(f.ProjectIDs))+`)`)
		for _, id := range f.ProjectIDs {
			args = append(args, id)
		}
	} else if f.NoProject {
		conds = append(conds, `project_id IS NULL`)
	}

	switch f.Archived {
	case ArchivedOnly:
		conds = append(conds, `archived_at IS NOT NULL`)
	case ArchivedAny:
		// 不加条件
	default: // ArchivedExclude
		conds = append(conds, `archived_at IS NULL`)
	}

	if q := strings.TrimSpace(f.Query); q != "" {
		// 用 instr 而非 LIKE：LIKE 会把用户输入里的 % 和 _ 当通配符，
		// instr 做的是字面包含匹配，行为可预期。
		conds = append(conds, `(instr(lower(title), lower(?))>0`+
			` OR instr(lower(description), lower(?))>0`+
			` OR instr(lower(workdir), lower(?))>0)`)
		args = append(args, q, q, q)
	}

	if len(conds) == 0 {
		return "1=1", nil
	}
	return strings.Join(conds, " AND "), args
}

// BoardResult 看板数据。
//
// 为什么把 Counts 与 Columns 分开：Counts 是**过滤后的真实总数**，
// 不受 perColumn 截断影响。这样"已完成 (23)"折叠条上的数字永远正确，
// 即使该列只返回了前 50 条。
type BoardResult struct {
	Columns map[string][]*domain.Task `json:"columns"`
	Counts  map[string]int            `json:"counts"`
	// Truncated 标记某列的实际条数超过返回条数，前端应在列底显示"还有 N 条"。
	Truncated map[string]bool `json:"truncated"`
}

// BoardResult 的 map key 用 string 而非 domain.Status，
// 因为 JSON 对象的 key 必须是字符串，且 domain.Status 是 string 别名，序列化行为一致。

// ListBoard 返回看板数据：按状态分组 + 每列真实计数。
//
// perColumn 为每列最多返回的条数；Counts 不受其影响。
func (s *Store) ListBoard(f TaskFilter, perColumn int) (*BoardResult, error) {
	if err := f.Normalize(); err != nil {
		return nil, err
	}
	if perColumn <= 0 || perColumn > MaxPerColumn {
		perColumn = MaxPerColumn
	}
	// 看板不需要 Offset（前端不知道第几页），并且必须忽略 Limit，
	// 否则「显示已完成」展开时会因为 Limit 而少数据。
	f.Limit, f.Offset = 0, 0

	where, args := buildTaskWhere(f)

	// ⚠️ db.SetMaxOpenConns(1)：同一时刻只能有一条 rows 打开。
	// 两次查询必须顺序执行 —— 先把第一条的 rows 读完并关闭，再开第二条，
	// 否则第二个 Query 会一直等连接，形成自死锁。
	groupCounts, err := s.countByStatus(where, args)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(
		`SELECT `+taskColumns+` FROM tasks WHERE `+where+` ORDER BY `+f.sortExpr(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	all := domain.ValidStatuses()
	columns := make(map[string][]*domain.Task, len(all))
	counts := make(map[string]int, len(all))
	truncated := make(map[string]bool, len(all))
	// make 而非 var：nil 切片会被 JSON 序列化成 null，前端 .map 直接崩。
	// Counts 也必须为每个状态预置 0 —— GROUP BY 只会返回有数据的行，
	// 空列若不预置，前端读 counts[status] 会得到 undefined，
	// 折叠条上的计数就显示成 NaN 或空白。
	for _, st := range all {
		columns[string(st)] = []*domain.Task{}
		counts[string(st)] = 0
		truncated[string(st)] = false
	}
	// 把 GROUP BY 的结果覆盖到预置的 0 上。key 用字符串直传，
	// 未知 status（手工改库）也能落到 map 里，不会静默丢数据。
	for key, n := range groupCounts {
		counts[key] = n
	}

	seen := make(map[string]int, len(all))
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		key := string(t.Status)
		// 防御：后端若返回了未知 status，原实现会 out[t.status].push(t) 直接崩。
		// 这里用 seen 计数天然容忍未知值（它不在 columns 里，被 seen 累加后丢弃）。
		seen[key]++
		if seen[key] <= perColumn {
			if _, ok := columns[key]; ok {
				columns[key] = append(columns[key], t)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, st := range all {
		truncated[string(st)] = counts[string(st)] > perColumn
	}
	// 后端若存有未知 status（例如手工改库），为其补一个空列，
	// 保证 Columns 与 Counts 的 key 集合一致，前端遍历时不会漏列或渲染 undefined。
	for key := range counts {
		if _, ok := columns[key]; !ok {
			columns[key] = []*domain.Task{}
			truncated[key] = false
		}
	}
	return &BoardResult{Columns: columns, Counts: counts, Truncated: truncated}, nil
}

// countByStatus 统计每个状态的命中数。结果集很小（最多几个状态），很快。
func (s *Store) countByStatus(where string, args []any) (map[string]int, error) {
	rows, err := s.db.Query(
		`SELECT status, COUNT(*) FROM tasks WHERE `+where+` GROUP BY status`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// AgentTaskQuery Agent 端列表查询参数。
// 注意这里**没有** Assignee 字段：Agent 只能看自己的任务，
// 指派人由服务端从 Token 身份注入，不接受客户端传入。
type AgentTaskQuery struct {
	Statuses   []domain.Status
	Priorities []string
	Query      string
	Sort       SortField
	Limit      int
	Offset     int
}

// ErrAgentStatusForbidden 构造 Agent 请求非法状态的错误。
func ErrAgentStatusForbidden() error {
	return errors.Join(domain.ErrInvalidInput, errors.New("Agent 不可查询 idea/approved 状态"))
}
