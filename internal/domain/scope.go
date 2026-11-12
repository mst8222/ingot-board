package domain

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// 设备令牌范围（017-device-scopes）。
//
// op 与路由 operationId 一一同名：路由表的 OpID 即范围判定键，
// 避免出现第二份"操作清单"真相。缺省范围 = 013 已允许的手机人类操作。

// DeviceOp 设备令牌范围里的操作名。
type DeviceOp string

// 手机人类操作（013 清单）。
const (
	OpHealth         DeviceOp = "health"
	OpGetMeta        DeviceOp = "getMeta"
	OpGetBoard       DeviceOp = "getBoard"
	OpListTasks      DeviceOp = "listTasks"
	OpGetTask        DeviceOp = "getTask"
	OpCreateTask     DeviceOp = "createTask"
	OpUpdateTask     DeviceOp = "updateTask"
	OpTransitionTask DeviceOp = "transitionTask"
	OpAddTaskNote    DeviceOp = "addTaskNote"
	OpListTaskEvents DeviceOp = "listTaskEvents"
	OpArchiveTask    DeviceOp = "archiveTask"
	OpUnarchiveTask  DeviceOp = "unarchiveTask"
	OpListAgents     DeviceOp = "listAgents"
	OpStreamEvents   DeviceOp = "streamEvents"
	OpListWorkdirs   DeviceOp = "listWorkdirs"
)

// registeredOps 全部合法 op 注册表（签发校验用）。
var registeredOps = map[DeviceOp]bool{
	OpHealth: true, OpGetMeta: true, OpGetBoard: true, OpListTasks: true,
	OpGetTask: true, OpCreateTask: true, OpUpdateTask: true, OpTransitionTask: true,
	OpAddTaskNote: true, OpListTaskEvents: true, OpArchiveTask: true,
	OpUnarchiveTask: true, OpListAgents: true, OpStreamEvents: true,
	OpListWorkdirs: true,
	// 项目与目录管理路由（024/025）也是合法 op，但不在缺省范围里：
	// 需要时由签发方在配对码上显式放开。
	OpListProjects: true, OpCreateProject: true, OpUpdateProject: true,
	OpArchiveProject: true, OpUnarchiveProject: true, OpDeleteProject: true,
	OpListProjectWorkdirs: true, OpAddProjectWorkdir: true, OpRemoveProjectWorkdir: true,
}

// 项目与目录管理 op（024/025，缺省范围之外）。
const (
	OpListProjects         DeviceOp = "listProjects"
	OpCreateProject        DeviceOp = "createProject"
	OpUpdateProject        DeviceOp = "updateProject"
	OpArchiveProject       DeviceOp = "archiveProject"
	OpUnarchiveProject     DeviceOp = "unarchiveProject"
	OpDeleteProject        DeviceOp = "deleteProject"
	OpListProjectWorkdirs  DeviceOp = "listProjectWorkdirs"
	OpAddProjectWorkdir    DeviceOp = "addProjectWorkdir"
	OpRemoveProjectWorkdir DeviceOp = "removeProjectWorkdir"
)

// ValidOp 判断操作名是否已注册。
func ValidOp(s string) bool { return registeredOps[DeviceOp(s)] }

// DefaultOps 缺省范围：013 已允许的手机人类操作，逐条对应 spec 必须项。
// 有意不含项目/目录管理：新配对设备默认无项目授权，签发方显式放开。
func DefaultOps() []DeviceOp {
	return []DeviceOp{
		OpHealth, OpGetMeta, OpGetBoard, OpListTasks, OpGetTask,
		OpCreateTask, OpUpdateTask, OpTransitionTask, OpAddTaskNote,
		OpListTaskEvents, OpArchiveTask, OpUnarchiveTask,
		OpListAgents, OpStreamEvents, OpListWorkdirs,
	}
}

// DeviceScopes 设备令牌范围。ops 为空切片时什么都不允许；
// devices.scopes 列为 NULL（存量行）表示全量放行，由调用方区分。
type DeviceScopes struct {
	Ops        []DeviceOp `json:"ops"`
	ProjectIDs []int64    `json:"project_ids"`
}

// ParseScopes 解析 scopes 列。NULL/空串 → (nil, nil)：
// 调用方把 nil 视为"无范围字段 = 全量放行"的存量设备。
func ParseScopes(raw sql.NullString) (*DeviceScopes, error) {
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	var sc DeviceScopes
	if err := json.Unmarshal([]byte(raw.String), &sc); err != nil {
		return nil, fmt.Errorf("解析设备范围失败: %w", err)
	}
	if sc.ProjectIDs == nil {
		sc.ProjectIDs = make([]int64, 0)
	}
	return &sc, nil
}

// Marshal 序列化成 scopes 列的存储形态。
func (sc *DeviceScopes) Marshal() (string, error) {
	if sc.ProjectIDs == nil {
		sc.ProjectIDs = make([]int64, 0)
	}
	b, err := json.Marshal(sc)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// AllowsOp 判断操作是否在范围内。
func (sc *DeviceScopes) AllowsOp(op DeviceOp) bool {
	for _, o := range sc.Ops {
		if o == op {
			return true
		}
	}
	return false
}

// AllowsProject 判断项目是否在授权列表里。
func (sc *DeviceScopes) AllowsProject(id int64) bool {
	for _, p := range sc.ProjectIDs {
		if p == id {
			return true
		}
	}
	return false
}

// Validate 签发前校验：ops 非空且全部已注册，项目 id 为正数。
func (sc *DeviceScopes) Validate() error {
	if sc == nil {
		return nil
	}
	if len(sc.Ops) == 0 {
		return fmt.Errorf("%w: 范围至少要包含一个操作", ErrInvalidInput)
	}
	for _, op := range sc.Ops {
		if !ValidOp(string(op)) {
			return fmt.Errorf("%w: 未知的操作 %q", ErrInvalidInput, op)
		}
	}
	for _, id := range sc.ProjectIDs {
		if id <= 0 {
			return fmt.Errorf("%w: 非法的项目 id %d", ErrInvalidInput, id)
		}
	}
	return nil
}
