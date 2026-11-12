// Command ingot 是 Ingot Board 的 Web 服务主程序，直接启动看板与 Web GUI。
package main

import (
	"os"

	"github.com/mst8222/ingot-board/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], true))
}
