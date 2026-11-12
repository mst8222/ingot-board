package app

import "github.com/mst8222/ingot-board/internal/domain"

// 项目用例。项目只是任务的展示分组（见 024-project），
// 不触碰状态机，也与 Agent 可见性无关，因此这里没有Actor 概念。

// CreateProject 创建项目。
func (s *Service) CreateProject(name, color string) (*domain.Project, error) {
	p, err := s.store.CreateProject(name, color)
	if err != nil {
		return nil, err
	}
	s.emit(Change{Kind: ChangeProject, Project: p})
	return p, nil
}

// ListProjects 列出项目。includeArchived=false 排除已归档。
func (s *Service) ListProjects(includeArchived bool) ([]*domain.Project, error) {
	return s.store.ListProjects(includeArchived)
}

// UpdateProject 改名与改色。
func (s *Service) UpdateProject(id int64, name, color string) (*domain.Project, error) {
	p, err := s.store.UpdateProject(id, name, color)
	if err != nil {
		return nil, err
	}
	s.emit(Change{Kind: ChangeProject, Project: p})
	return p, nil
}

// ArchiveProject 归档项目，不触碰任务。
func (s *Service) ArchiveProject(id int64) (*domain.Project, error) {
	p, err := s.store.ArchiveProject(id)
	if err != nil {
		return nil, err
	}
	s.emit(Change{Kind: ChangeProject, Project: p})
	return p, nil
}

// UnarchiveProject 取消归档。
func (s *Service) UnarchiveProject(id int64) (*domain.Project, error) {
	p, err := s.store.UnarchiveProject(id)
	if err != nil {
		return nil, err
	}
	s.emit(Change{Kind: ChangeProject, Project: p})
	return p, nil
}

// DeleteProject 删除项目。有任务引用时返回 ErrProjectInUse（API 层映射 409）。
func (s *Service) DeleteProject(id int64) error {
	if err := s.store.DeleteProject(id); err != nil {
		return err
	}
	s.emit(Change{Kind: ChangeProject, Project: &domain.Project{ID: id}})
	return nil
}
