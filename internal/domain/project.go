package domain

import (
	"fmt"
	"strings"
	"time"
)

// Project 任务的项目归属。任务可以不挂任何项目（project_id 为 null）。
// 多用户/多看板不在本产品范围（见 000-product），projects 只有展示属性。
type Project struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Color      string     `json:"color"`
	ArchivedAt *time.Time `json:"archived_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// IsArchived 判断项目是否已归档。
func (p *Project) IsArchived() bool { return p.ArchivedAt != nil }

// MaxProjectNameLen 项目名长度上限。看板 chip 与卡片色点提示都要放得下。
const MaxProjectNameLen = 50

// ValidProjectName 校验项目名：去首尾空白后非空且不超过上限。
// 返回去空白后的名字，供调用方直接落库。
func ValidProjectName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("%w: 项目名不能为空", ErrInvalidInput)
	}
	if len([]rune(trimmed)) > MaxProjectNameLen {
		return "", fmt.Errorf("%w: 项目名不能超过 %d 个字符", ErrInvalidInput, MaxProjectNameLen)
	}
	return trimmed, nil
}

// ValidColor 校验项目颜色：空字符串表示无颜色，其余必须是 #rgb 或 #rrggbb。
func ValidColor(color string) error {
	if color == "" {
		return nil
	}
	if len(color) != 4 && len(color) != 7 {
		return fmt.Errorf("%w: 颜色必须是 #rgb 或 #rrggbb", ErrInvalidInput)
	}
	if color[0] != '#' {
		return fmt.Errorf("%w: 颜色必须以 # 开头", ErrInvalidInput)
	}
	for _, c := range color[1:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return fmt.Errorf("%w: 颜色只能包含十六进制字符", ErrInvalidInput)
		}
	}
	return nil
}

// ProjectWorkdir 项目与工作目录的绑定关系（project_workdirs 行）。
// 目录集合只用于聚合视图与 017 的授权判定，绝不反写任务的 workdir 或 project_id。
type ProjectWorkdir struct {
	ID        int64     `json:"id"`
	ProjectID int64     `json:"project_id"`
	Workdir   string    `json:"workdir"`
	CreatedAt time.Time `json:"created_at"`
}

// WorkdirRef 最近目录反查：目录 + 其所属项目 id 列表（GET /workdirs 的条目）。
type WorkdirRef struct {
	Workdir    string  `json:"workdir"`
	ProjectIDs []int64 `json:"project_ids"`
}
