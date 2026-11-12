package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/mst8222/ingot-board/internal/domain"
)

// project_workdirs 的读写（025-project-workdirs）。
// 关联表由 0007 建好：UNIQUE(project_id, workdir) + ON DELETE CASCADE。
// 这里只做数据面：绑定/解绑/反查；目录存在性校验在 app 层（025 spec）。

const projectWorkdirColumns = `id, project_id, workdir, created_at`

func scanProjectWorkdir(row interface {
	Scan(dest ...any) error
}) (*domain.ProjectWorkdir, error) {
	var w domain.ProjectWorkdir
	var rawCreated string
	if err := row.Scan(&w.ID, &w.ProjectID, &w.Workdir, &rawCreated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	var err error
	if w.CreatedAt, err = parseTime(rawCreated); err != nil {
		return nil, err
	}
	return &w, nil
}

// GetProjectWorkdir 按关联行 id 读取。
func (s *Store) GetProjectWorkdir(id int64) (*domain.ProjectWorkdir, error) {
	return scanProjectWorkdir(s.db.QueryRow(
		`SELECT `+projectWorkdirColumns+` FROM project_workdirs WHERE id=?`, id))
}

// ListProjectWorkdirs 列出项目绑定的目录，按目录名排序。
// 项目不存在时返回 ErrNotFound（与项目路由语义一致）。
func (s *Store) ListProjectWorkdirs(projectID int64) ([]*domain.ProjectWorkdir, error) {
	if _, err := s.GetProject(projectID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(
		`SELECT `+projectWorkdirColumns+` FROM project_workdirs
		 WHERE project_id=? ORDER BY workdir`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*domain.ProjectWorkdir, 0, 4)
	for rows.Next() {
		w, err := scanProjectWorkdir(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// AddProjectWorkdir 绑定目录到项目。重复绑定映射为 ErrWorkdirBound（API 层 409）。
// 目录规范化（filepath.Clean）由 app 层完成后传入，这里按字面落库。
func (s *Store) AddProjectWorkdir(projectID int64, workdir string) (*domain.ProjectWorkdir, error) {
	if _, err := s.GetProject(projectID); err != nil {
		return nil, err
	}
	res, err := s.db.Exec(
		`INSERT INTO project_workdirs(project_id, workdir) VALUES(?,?)`,
		projectID, workdir)
	if err != nil {
		// UNIQUE(project_id, workdir) 冲突：与 mapWriteError 的笼统 INVALID_INPUT
		// 不同，重复绑定有独立的业务语义（409），需要独立 sentinel。
		if contains(err.Error(), "UNIQUE") {
			return nil, errors.Join(domain.ErrWorkdirBound,
				fmt.Errorf("目录已绑定到该项目: %s", workdir))
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetProjectWorkdir(id)
}

// RemoveProjectWorkdir 解绑。wid 是关联行 id 且必须属于该项目：
// 双条件 DELETE 保证跨项目解绑无效，0 行影响返回 ErrNotFound（重复解绑 404）。
func (s *Store) RemoveProjectWorkdir(projectID, wid int64) error {
	res, err := s.db.Exec(
		`DELETE FROM project_workdirs WHERE id=? AND project_id=?`, wid, projectID)
	if err != nil {
		return err
	}
	return requireRow(res)
}

// WorkdirProjects 反查目录所属的项目 id 列表（025 反查与 017 授权共用）。
func (s *Store) WorkdirProjects(workdir string) ([]int64, error) {
	rows, err := s.db.Query(
		`SELECT project_id FROM project_workdirs WHERE workdir=? ORDER BY project_id`, workdir)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]int64, 0, 2)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// WorkdirBound 判断目录是否绑定了某项目（017 按项目授权的判定查询）。
func (s *Store) WorkdirBound(projectID int64, workdir string) (bool, error) {
	var one int
	err := s.db.QueryRow(
		`SELECT 1 FROM project_workdirs WHERE project_id=? AND workdir=? LIMIT 1`,
		projectID, workdir).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// RecentWorkdirsWithProjects 最近目录 + 各自所属项目 id（GET /workdirs 的新响应）。
// 目录集合仍来自 tasks 的历史（013 语义不变），项目归属来自 project_workdirs。
// projectFilter 非 nil 时只返回绑定到该项目的目录。
func (s *Store) RecentWorkdirsWithProjects(limit int, projectFilter *int64) ([]*domain.WorkdirRef, error) {
	if limit <= 0 {
		limit = 20
	}
	filterJoin := ""
	var args []any
	if projectFilter != nil {
		filterJoin = ` WHERE t.workdir IN (SELECT workdir FROM project_workdirs WHERE project_id=?)`
		args = append(args, *projectFilter)
	}
	args = append(args, limit)
	rows, err := s.db.Query(`
		SELECT t.workdir, pw.project_id
		FROM (SELECT workdir, MAX(updated_at) AS u FROM tasks WHERE workdir<>'' GROUP BY workdir) t
		LEFT JOIN project_workdirs pw ON pw.workdir = t.workdir`+
		filterJoin+`
		ORDER BY t.u DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*domain.WorkdirRef, 0, 8)
	index := make(map[string]int, 8)
	for rows.Next() {
		var workdir string
		var pid sql.NullInt64
		if err := rows.Scan(&workdir, &pid); err != nil {
			return nil, err
		}
		i, ok := index[workdir]
		if !ok {
			// make 而非 var：无绑定时序列化成 []，前端 .map 不崩。
			out = append(out, &domain.WorkdirRef{Workdir: workdir, ProjectIDs: make([]int64, 0)})
			i = len(out) - 1
			index[workdir] = i
		}
		if pid.Valid {
			out[i].ProjectIDs = append(out[i].ProjectIDs, pid.Int64)
		}
	}
	return out, rows.Err()
}
