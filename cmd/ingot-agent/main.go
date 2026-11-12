// Command ingot-agent 是 Ingot Board 的全功能 CLI：
// serve / agent / mcp / runner。
package main

import (
	"os"

	"github.com/mst8222/ingot-board/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], false))
}
