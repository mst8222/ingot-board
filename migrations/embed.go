// Package migrations 内嵌 SQL 迁移文件，供 store 层在单二进制内执行。
package migrations

import "embed"

// FS 迁移文件只读文件系统。
//
//go:embed *.sql
var FS embed.FS
