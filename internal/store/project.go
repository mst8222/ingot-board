package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/mst8222/ingot-board/internal/domain"
)

// projectColumns 是所有读项目的 SQL 共享的列清单。
const projectColumns = `id, name, color, archived_at, created_at, updated_at`

func scanProject(row interface {
	Scan(dest ...any) error
}) (*domain.Project, error) {
	var p domain.Project
	var archived sql.NullString
	var rawCreated, rawUpdated string
	if err := row.Scan(&p.ID, &p.Name, &p.Color, &archived, &rawCreated, &rawUpdated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	if archived.Valid && archived.String != "" {
		at, err := parseTime(archived.String)
		if err != nil {
			return nil, err
		}
		p.ArchivedAt = &at
	}
	var err error
	if p.CreatedAt, err = parseTime(rawCreated); err != nil {
		return nil, err
	}
	if p.UpdatedAt, err = parseTime(rawUpdated); err != nil {
		return nil, err
	}
	return &p, nil
}

// CreateProject 创建项目。重名走 mapWriteError 映射为 ErrInvalidInput。
func (s *Store) CreateProject(name, color string) (*domain.Project, error) {
	trimmed, err := domain.ValidProjectName(name)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidColor(color); err != nil {
		return nil, err
	}
	res, err := s.db.Exec(
		`INSERT INTO projects(name, color) VALUES(?,?)`, trimmed, color)
	if err != nil {
		return nil, mapWriteError(err)
	}
	id, _ := res.LastInsertId()
	return s.GetProject(id)
}

// ListProjects 返回项目，按 id 排序。includeArchived=false 时排除已归档。
func (s *Store) ListProjects(includeArchived bool) ([]*domain.Project, error) {
	where := ""
	if !includeArchived {
		where = " WHERE archived_at IS NULL"
	}
	rows, err := s.db.Query(`SELECT ` + projectColumns + ` FROM projects` + where + ` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// make 而非 var：nil 切片序列化成 null，前端 .map 直接崩。
	out := make([]*domain.Project, 0, 8)
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProject 按 id 读项目。
func (s *Store) GetProject(id int64) (*domain.Project, error) {
	return scanProject(s.db.QueryRow(
		`SELECT `+projectColumns+` FROM projects WHERE id=?`, id))
}

// queryProjectTx 在事务内读项目。
// ⚠️ 事务持有唯一连接（SetMaxOpenConns(1)），事务内绝不能再走 s.db ——
// 那会等一个永远不会释放的连接，形成自死锁。
func queryProjectTx(tx *sql.Tx, id int64) (*domain.Project, error) {
	return scanProject(tx.QueryRow(
		`SELECT `+projectColumns+` FROM projects WHERE id=?`, id))
}

// UpdateProject 改名与改色。项目 CRUD 低频，不做乐观锁（见 024 spec 禁止项）。
func (s *Store) UpdateProject(id int64, name, color string) (*domain.Project, error) {
	trimmed, err := domain.ValidProjectName(name)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidColor(color); err != nil {
		return nil, err
	}
	res, err := s.db.Exec(
		`UPDATE projects SET name=?, color=?, updated_at=? WHERE id=?`,
		trimmed, color, formatTime(nowUTC()), id)
	if err != nil {
		return nil, mapWriteError(err)
	}
	if err := requireRow(res); err != nil {
		return nil, err
	}
	return s.GetProject(id)
}

// ArchiveProject 归档项目。幂等：已归档再归档直接返回当前态。
// 不触碰任务：任务的 project_id 保留（见 024 spec 必须项）。
func (s *Store) ArchiveProject(id int64) (*domain.Project, error) {
	if _, err := s.GetProject(id); err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(
		`UPDATE projects SET archived_at=? WHERE id=? AND archived_at IS NULL`,
		formatTime(nowUTC()), id); err != nil {
		return nil, err
	}
	return s.GetProject(id)
}

// UnarchiveProject 取消归档。幂等同 ArchiveProject。
func (s *Store) UnarchiveProject(id int64) (*domain.Project, error) {
	if _, err := s.GetProject(id); err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(
		`UPDATE projects SET archived_at=NULL, updated_at=? WHERE id=?`,
		formatTime(nowUTC()), id); err != nil {
		return nil, err
	}
	return s.GetProject(id)
}

// DeleteProject 删除项目。仅当无任何任务（含归档任务）引用时允许：
// SET NULL 会把任务静默变成"无项目"，用户不可见地丢归属；
// 拒绝删除让用户先清任务，行为可预期。由 TestDeleteProjectRefusesWhenReferenced 强制。
func (s *Store) DeleteProject(id int64) error {
	if _, err := s.GetProject(id); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var refs int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE project_id=?`, id).Scan(&refs); err != nil {
		return err
	}
	if refs > 0 {
		return errors.Join(domain.ErrProjectInUse,
			fmt.Errorf("项目仍被 %d 条任务（含归档）引用，请先移出或删除任务", refs))
	}
	res, err := tx.Exec(`DELETE FROM projects WHERE id=?`, id)
	if err != nil {
		return err
	}
	if err := requireRow(res); err != nil {
		return err
	}
	return tx.Commit()
}
