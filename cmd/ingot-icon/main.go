// ingot-icon 把 internal/desktop 里的矢量图标导出为文件：
// 构建脚本用 -ico 生成 exe 资源所需的多尺寸 .ico；-png 用于预览单一尺寸。
package main

import (
	"flag"
	"log"
	"os"

	"github.com/mst8222/ingot-board/internal/desktop"
)

func main() {
	ico := flag.String("ico", "", "输出多尺寸 .ico 路径")
	pngOut := flag.String("png", "", "输出 PNG 路径")
	size := flag.Int("size", 256, "PNG 边长（像素）")
	flag.Parse()

	if *ico == "" && *pngOut == "" {
		log.Fatal("至少指定 -ico 或 -png")
	}
	if *ico != "" {
		if err := os.WriteFile(*ico, desktop.IconICO(), 0o644); err != nil {
			log.Fatal(err)
		}
	}
	if *pngOut != "" {
		if err := os.WriteFile(*pngOut, desktop.IconPNG(*size), 0o644); err != nil {
			log.Fatal(err)
		}
	}
}
