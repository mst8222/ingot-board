// Package webassets 内嵌前端构建产物（web/dist）。
// 仓库中保留占位 index.html 以保证未构建前端时 Go 仍可编译；
// 执行 web 目录下的 npm run build 后产物会覆盖该占位文件。
package webassets

import "embed"

// Dist 前端静态资源文件系统。
//
//go:embed all:dist
var Dist embed.FS
