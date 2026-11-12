package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mst8222/ingot-board/internal/domain"
)

// 按项目授权（017-device-scopes，2026-10-28 追加）。
//
// 判定放 app 层而非路由中间件：同一条 CreateTask 在回环路由免令牌、
// 在手机监听带令牌，中间件无法表达"同用例两种语义"。
// 回环（无设备上下文）与存量设备（Scopes==nil）都直接放行。

// checkProjectScope 校验设备令牌对 (projectID, workdir) 的授权。
//   - 无设备上下文（回环）或存量设备（Scopes==nil）→ 放行（013/024 现状）；
//   - projectID 为 nil（无项目任务）→ 放行，维持 013 手填路径现状；
//   - 项目不在令牌授权列表 → ErrForbidden；
//   - workdir 不属于该项目绑定的目录集合（025 的 project_workdirs）→ ErrForbidden。
func (s *Service) checkProjectScope(ctx context.Context, projectID *int64, workdir string) error {
	if projectID == nil {
		return nil
	}
	dev := DeviceFromContext(ctx)
	if dev == nil || dev.Scopes == nil {
		return nil
	}
	if !dev.Scopes.AllowsProject(*projectID) {
		return errors.Join(domain.ErrForbidden,
			fmt.Errorf("项目 %d 不在设备授权范围内", *projectID))
	}
	// 与 AddProjectWorkdir 一致：Clean 后比较，保证判定键稳定。
	bound, err := s.store.WorkdirBound(*projectID, filepath.Clean(workdir))
	if err != nil {
		return err
	}
	if !bound {
		return errors.Join(domain.ErrForbidden,
			errors.New("工作目录不在授权项目绑定的目录集合内"))
	}
	return nil
}
