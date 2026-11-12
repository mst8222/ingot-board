// 这是一个空的嵌套模块，唯一作用是让根模块的 `./...` 不再递归进 web/。
//
// 背景：web/node_modules 里某些依赖自带 .go 文件（flatted 就在其中），
// 根模块的 `go build ./...` / `go test ./...` 会把它们当成本项目包一起编译。
// 目前能编过，但代价是变慢，且哪天某个依赖升级后带进一个编译不过的
// .go 文件，整个 Go 项目就构建失败 —— 与我们的代码毫无关系却要我们买单。
//
// Go 的模块解析规则：某个目录树里存在 go.mod 时，它就是独立模块，
// 根模块的 ./... 不会跨进去。node_modules 在 web/ 下，于是整个 web/
// （包括 src、dist、node_modules）都被排除。
//
// 注意本模块不参与任何实际构建：前端产物由 internal/webassets 通过
// //go:embed dist 嵌入，与这里的 go.mod 无关。
module ingotboard/web

go 1.26.0
