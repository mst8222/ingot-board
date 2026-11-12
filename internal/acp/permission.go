package acp

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PermissionChoice 是 ACP 权限请求里的一个选项。
type PermissionChoice struct {
	ID   string
	Kind string
}

// SelectPermission 在工作目录内的读、写、执行上选择允许一次。
// 其他种类、目录外路径或没有 allow_once 时返回错误，调用方应拒绝并退回任务。
func SelectPermission(workdir, toolKind string, paths []string, options []PermissionChoice) (string, error) {
	if !allowedToolKind(toolKind) {
		return "", fmt.Errorf("不允许的工具种类 %s", toolKind)
	}
	absWork, err := filepath.Abs(workdir)
	if err != nil || strings.TrimSpace(workdir) == "" {
		return "", fmt.Errorf("工作目录无效")
	}
	for _, path := range paths {
		if !pathInside(absWork, path) {
			return "", fmt.Errorf("路径超出工作目录: %s", path)
		}
	}
	for _, opt := range options {
		if opt.Kind == "allow_once" && opt.ID != "" {
			return opt.ID, nil
		}
	}
	return "", fmt.Errorf("没有允许一次的选项")
}

func allowedToolKind(kind string) bool {
	switch kind {
	case "read", "search", "edit", "delete", "move", "execute":
		return true
	default:
		return false
	}
}

func pathInside(absWork, path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(absWork, candidate)
	}
	absPath, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absWork, absPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
