package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mst8222/ingot-board/internal/domain"
)

// 项目 ↔ 工作目录绑定用例（025-project-workdirs）。
// 目录集合只用于聚合视图与 017 授权判定，不自动改写任何任务。

// ListProjectWorkdirs 列出项目绑定的目录。
func (s *Service) ListProjectWorkdirs(projectID int64) ([]*domain.ProjectWorkdir, error) {
	return s.store.ListProjectWorkdirs(projectID)
}

// AddProjectWorkdir 绑定目录到项目。
// 目录必须在本机存在（os.Stat，025 spec 必须项）；落库前 filepath.Clean
// 规范化，保证 017 的授权比较键稳定。重复绑定 409（ErrWorkdirBound）。
func (s *Service) AddProjectWorkdir(projectID int64, workdir string) (*domain.ProjectWorkdir, error) {
	cleaned := filepath.Clean(workdir)
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return nil, errors.Join(domain.ErrInvalidInput, errors.New("目录路径不能为空或根目录"))
	}
	info, err := os.Stat(cleaned)
	if err != nil {
		return nil, errors.Join(domain.ErrInvalidInput, fmt.Errorf("目录在本机不存在: %s", cleaned))
	}
	if !info.IsDir() {
		return nil, errors.Join(domain.ErrInvalidInput, fmt.Errorf("路径不是目录: %s", cleaned))
	}
	w, err := s.store.AddProjectWorkdir(projectID, cleaned)
	if err != nil {
		return nil, err
	}
	s.emit(Change{Kind: ChangeProject, Project: &domain.Project{ID: projectID}})
	return w, nil
}

// RemoveProjectWorkdir 解绑。重复解绑 ErrNotFound（API 层 404）。
func (s *Service) RemoveProjectWorkdir(projectID, wid int64) error {
	if err := s.store.RemoveProjectWorkdir(projectID, wid); err != nil {
		return err
	}
	s.emit(Change{Kind: ChangeProject, Project: &domain.Project{ID: projectID}})
	return nil
}

// RecentWorkdirsWithProjects 最近目录 + 所属项目 id（GET /workdirs）。
// projectFilter 非 nil 时只返回绑定到该项目的目录。
func (s *Service) RecentWorkdirsWithProjects(limit int, projectFilter *int64) ([]*domain.WorkdirRef, error) {
	return s.store.RecentWorkdirsWithProjects(limit, projectFilter)
}
