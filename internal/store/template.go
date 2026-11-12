package store

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/mst8222/ingot-board/internal/domain"
)

// 029 任务模板：只存派发期素材，实例出来的任务是独立副本。
// 删除或归档模板不影响已实例化的任务（tasks 表不反向引用模板）。

type Template struct {
	ID                 int64   `json:"id"`
	Name               string  `json:"name"`
	Title              string  `json:"title"`
	Description        string  `json:"description"`
	AcceptanceCriteria string  `json:"acceptance_criteria"`
	Workdir            string  `json:"workdir"`
	Priority           string  `json:"priority"`
	Category           string  `json:"category"`
	Tier               string  `json:"tier"`
	AssigneeAgentID    *int64  `json:"assignee_agent_id"`
	ProjectID          *int64  `json:"project_id"`
	ArchivedAt         *string `json:"archived_at"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
}

func (t *Template) IsArchived() bool { return t.ArchivedAt != nil }

// TemplatePatch 是人类端 PATCH 的可改字段；nil 表示不修改。
type TemplatePatch struct {
	Name               *string `json:"name"`
	Title              *string `json:"title"`
	Description        *string `json:"description"`
	AcceptanceCriteria *string `json:"acceptance_criteria"`
	Workdir            *string `json:"workdir"`
	Priority           *string `json:"priority"`
	Category           *string `json:"category"`
	Tier               *string `json:"tier"`
	AssigneeAgentID    *int64  `json:"assignee_agent_id"`
	ProjectID          *int64  `json:"project_id"`
	Archived           *bool   `json:"archived"`
}

const templateColumns = `id, name, title, description, acceptance_criteria, workdir,
	priority, category, tier, assignee_agent_id, project_id, archived_at, created_at, updated_at`

func scanTemplate(row interface{ Scan(dest ...any) error }) (*Template, error) {
	var t Template
	var assignee, project sql.NullInt64
	var archived sql.NullString
	if err := row.Scan(&t.ID, &t.Name, &t.Title, &t.Description, &t.AcceptanceCriteria,
		&t.Workdir, &t.Priority, &t.Category, &t.Tier, &assignee, &project,
		&archived, &t.CreatedAt, &t.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	if assignee.Valid {
		v := assignee.Int64
		t.AssigneeAgentID = &v
	}
	if project.Valid {
		v := project.Int64
		t.ProjectID = &v
	}
	if archived.Valid && archived.String != "" {
		v := archived.String
		t.ArchivedAt = &v
	}
	return &t, nil
}

// CreateTemplateFromTask 只允许 approved 且未归档的任务作为来源，且必须由人显式调用。
func (s *Store) CreateTemplateFromTask(taskID int64, name string) (*Template, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.Join(domain.ErrInvalidInput, errors.New("模板名称不能为空"))
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	t, err := queryTaskTx(tx, taskID)
	if err != nil {
		return nil, err
	}
	if t.Status != domain.StatusApproved || t.IsArchived() {
		return nil, errors.Join(domain.ErrInvalidStatus,
			errors.New("只有已完成且未归档的任务能存为模板"))
	}
	now := formatTime(nowUTC())
	res, err := tx.Exec(
		`INSERT INTO task_templates(name, title, description, acceptance_criteria, workdir,
			priority, category, tier, assignee_agent_id, project_id, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		name, t.Title, t.Description, t.AcceptanceCriteria, t.Workdir, t.Priority,
		t.Category, t.Tier, nullableID(t.AssigneeAgentID), nullableID(t.ProjectID), now, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetTemplate(id)
}

func (s *Store) GetTemplate(id int64) (*Template, error) {
	return scanTemplate(s.db.QueryRow(`SELECT `+templateColumns+` FROM task_templates WHERE id=?`, id))
}

func (s *Store) ListTemplates(includeArchived bool) ([]Template, error) {
	q := `SELECT ` + templateColumns + ` FROM task_templates`
	if !includeArchived {
		q += ` WHERE archived_at IS NULL`
	}
	q += ` ORDER BY id DESC`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Template, 0)
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// UpdateTemplate 只改请求里显式给出的字段；archived 通过 archived_at 落库。
func (s *Store) UpdateTemplate(id int64, p TemplatePatch) (*Template, error) {
	cur, err := s.GetTemplate(id)
	if err != nil {
		return nil, err
	}
	apply := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	apply(&cur.Name, p.Name)
	apply(&cur.Title, p.Title)
	apply(&cur.Description, p.Description)
	apply(&cur.AcceptanceCriteria, p.AcceptanceCriteria)
	apply(&cur.Workdir, p.Workdir)
	apply(&cur.Priority, p.Priority)
	apply(&cur.Category, p.Category)
	apply(&cur.Tier, p.Tier)
	if p.AssigneeAgentID != nil {
		v := *p.AssigneeAgentID
		cur.AssigneeAgentID = &v
	}
	if p.ProjectID != nil {
		v := *p.ProjectID
		cur.ProjectID = &v
	}
	archived := cur.ArchivedAt
	if p.Archived != nil {
		if *p.Archived {
			v := formatTime(nowUTC())
			archived = &v
		} else {
			archived = nil
		}
	}
	if strings.TrimSpace(cur.Name) == "" || strings.TrimSpace(cur.Title) == "" {
		return nil, errors.Join(domain.ErrInvalidInput, errors.New("模板名称与标题不能为空"))
	}
	res, err := s.db.Exec(
		`UPDATE task_templates SET name=?, title=?, description=?, acceptance_criteria=?,
			workdir=?, priority=?, category=?, tier=?, assignee_agent_id=?, project_id=?,
			archived_at=?, updated_at=? WHERE id=?`,
		cur.Name, cur.Title, cur.Description, cur.AcceptanceCriteria, cur.Workdir,
		cur.Priority, cur.Category, cur.Tier, nullableID(cur.AssigneeAgentID),
		nullableID(cur.ProjectID), nullableStringPtr(archived), formatTime(nowUTC()), id)
	if err != nil {
		return nil, err
	}
	if err := requireRow(res); err != nil {
		return nil, err
	}
	return s.GetTemplate(id)
}

func (s *Store) DeleteTemplate(id int64) error {
	res, err := s.db.Exec(`DELETE FROM task_templates WHERE id=?`, id)
	if err != nil {
		return err
	}
	return requireRow(res)
}

// SetTaskTemplateMeta 让实例化出来的 idea 任务带上模板的分类与档位，
// 之后 routeIdea 会照旧按它决定类别，不绕过既有派发链路。
func (s *Store) SetTaskTemplateMeta(id int64, category, tier string) error {
	_, err := s.db.Exec(`UPDATE tasks SET category=?, tier=? WHERE id=?`, category, tier, id)
	return err
}

func nullableStringPtr(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}
