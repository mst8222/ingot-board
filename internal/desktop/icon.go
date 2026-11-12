package desktop

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"

	"golang.org/x/image/vector"
)

// 图标设计（在 256×256 的设计坐标系里描述，按目标尺寸等比缩放）：
//   - 底：圆角方形，左上 todo 蓝 → 右下 review 紫的对角渐变，与看板状态色同源；
//   - 主体：前后错开的两张任务卡片，后卡半透明，表达"一叠待办"；
//   - 前卡上一枚 approved 绿的对勾，表达"做完并通过验收"；
//   - 右下一颗白色四角星，表达"由 AI Agent 执行"。
//
// 运行时用矢量光栅化绘制而不是内嵌位图：仓库不携带二进制资源，
// 托盘、窗口、exe 资源三处尺寸各异，同一份矢量描述按需出图，不会彼此漂移。
const designSize = 256

var (
	iconGradFrom = color.NRGBA{0x25, 0x63, 0xEB, 0xFF}
	iconGradTo   = color.NRGBA{0x7C, 0x3A, 0xED, 0xFF}
	iconCard     = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}
	iconCardBack = color.NRGBA{0xFF, 0xFF, 0xFF, 0x55}
	iconCheck    = color.NRGBA{0x16, 0xA3, 0x4A, 0xFF}
	iconSpark    = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}
)

// icoSizes 是写进 exe 资源的尺寸档位，覆盖资源管理器各视图与高 DPI 任务栏。
var icoSizes = []int{16, 24, 32, 48, 64, 128, 256}

// renderIcon 按 size 像素绘制图标。
func renderIcon(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	p := painter{dst: img, k: float32(size) / designSize}

	p.fill(gradient{from: iconGradFrom, to: iconGradTo, size: float32(size)}, func(z *pathBuilder) {
		z.roundedRect(0, 0, 256, 256, 56)
	})
	p.fill(image.NewUniform(iconCardBack), func(z *pathBuilder) {
		z.roundedRect(84, 40, 216, 172, 26)
	})
	p.fill(image.NewUniform(iconCard), func(z *pathBuilder) {
		z.roundedRect(40, 84, 172, 216, 26)
	})
	p.stroke(image.NewUniform(iconCheck), 22, [][2]float32{{72, 152}, {98, 178}, {144, 124}})
	p.fill(image.NewUniform(iconSpark), func(z *pathBuilder) {
		z.sparkle(208, 208, 30)
	})
	return img
}

// IconPNG 返回 size×size 的 PNG 字节。
func IconPNG(size int) []byte {
	var buf bytes.Buffer
	// 内存 NRGBA 编码 PNG 只会因写入失败而出错，bytes.Buffer 不会写失败
	if err := png.Encode(&buf, renderIcon(size)); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// IconICO 返回多尺寸 ICO 字节（每档内嵌 PNG，Vista 起系统均支持），
// 供构建脚本写成 .ico 再打进 exe 资源。
func IconICO() []byte {
	var buf bytes.Buffer
	n := len(icoSizes)
	images := make([][]byte, n)
	for i, s := range icoSizes {
		images[i] = IconPNG(s)
	}

	// ICONDIR：reserved=0, type=1(图标), count
	binary.Write(&buf, binary.LittleEndian, [3]uint16{0, 1, uint16(n)})
	offset := uint32(6 + 16*n)
	for i, s := range icoSizes {
		// 宽高字段只有一个字节，256 记作 0
		dim := uint8(s % 256)
		binary.Write(&buf, binary.LittleEndian, struct {
			W, H, Colors, Reserved uint8
			Planes, BitCount       uint16
			Size, Offset           uint32
		}{dim, dim, 0, 0, 1, 32, uint32(len(images[i])), offset})
		offset += uint32(len(images[i]))
	}
	for _, img := range images {
		buf.Write(img)
	}
	return buf.Bytes()
}

// painter 把设计坐标系里的形状光栅化到目标图上，k 为缩放系数。
type painter struct {
	dst *image.NRGBA
	k   float32
}

// fill 用 src 填充 build 描述的闭合路径（与已有像素做 Over 混合）。
func (p painter) fill(src image.Image, build func(z *pathBuilder)) {
	b := p.dst.Bounds()
	z := vector.NewRasterizer(b.Dx(), b.Dy())
	z.DrawOp = draw.Over
	build(&pathBuilder{z: z, k: p.k})
	z.Draw(p.dst, b, src, image.Point{})
}

// stroke 画一条圆头圆角折线：每段一个矩形 + 每个顶点一个圆。
// 逐个形状单独光栅化再 Over 叠加，避免同一路径内自交导致的绕数抵消。
func (p painter) stroke(src image.Image, width float32, pts [][2]float32) {
	r := width / 2
	for i := 0; i+1 < len(pts); i++ {
		a, b := pts[i], pts[i+1]
		p.fill(src, func(z *pathBuilder) { z.segment(a, b, r) })
	}
	for _, pt := range pts {
		p.fill(src, func(z *pathBuilder) { z.circle(pt[0], pt[1], r) })
	}
}

// pathBuilder 接收设计坐标，缩放后写入光栅器。
type pathBuilder struct {
	z *vector.Rasterizer
	k float32
}

func (b *pathBuilder) moveTo(x, y float32) { b.z.MoveTo(x*b.k, y*b.k) }
func (b *pathBuilder) lineTo(x, y float32) { b.z.LineTo(x*b.k, y*b.k) }
func (b *pathBuilder) quadTo(cx, cy, x, y float32) {
	b.z.QuadTo(cx*b.k, cy*b.k, x*b.k, y*b.k)
}
func (b *pathBuilder) cubeTo(c1x, c1y, c2x, c2y, x, y float32) {
	b.z.CubeTo(c1x*b.k, c1y*b.k, c2x*b.k, c2y*b.k, x*b.k, y*b.k)
}

// arcK 是用三次贝塞尔逼近四分之一圆时控制点到端点的比例。
const arcK = 0.5523

func (b *pathBuilder) roundedRect(x0, y0, x1, y1, r float32) {
	c := r * arcK
	b.moveTo(x0+r, y0)
	b.lineTo(x1-r, y0)
	b.cubeTo(x1-r+c, y0, x1, y0+r-c, x1, y0+r)
	b.lineTo(x1, y1-r)
	b.cubeTo(x1, y1-r+c, x1-r+c, y1, x1-r, y1)
	b.lineTo(x0+r, y1)
	b.cubeTo(x0+r-c, y1, x0, y1-r+c, x0, y1-r)
	b.lineTo(x0, y0+r)
	b.cubeTo(x0, y0+r-c, x0+r-c, y0, x0+r, y0)
	b.z.ClosePath()
}

func (b *pathBuilder) circle(cx, cy, r float32) {
	c := r * arcK
	b.moveTo(cx, cy-r)
	b.cubeTo(cx+c, cy-r, cx+r, cy-c, cx+r, cy)
	b.cubeTo(cx+r, cy+c, cx+c, cy+r, cx, cy+r)
	b.cubeTo(cx-c, cy+r, cx-r, cy+c, cx-r, cy)
	b.cubeTo(cx-r, cy-c, cx-c, cy-r, cx, cy-r)
	b.z.ClosePath()
}

// segment 画以 a、b 为端点、半宽 r 的矩形（不含端帽）。
func (b *pathBuilder) segment(a, c [2]float32, r float32) {
	dx, dy := c[0]-a[0], c[1]-a[1]
	l := sqrt32(dx*dx + dy*dy)
	nx, ny := -dy/l*r, dx/l*r
	b.moveTo(a[0]+nx, a[1]+ny)
	b.lineTo(c[0]+nx, c[1]+ny)
	b.lineTo(c[0]-nx, c[1]-ny)
	b.lineTo(a[0]-nx, a[1]-ny)
	b.z.ClosePath()
}

// sparkle 画四角星：四个尖端之间用向内收的二次曲线连接。
func (b *pathBuilder) sparkle(cx, cy, r float32) {
	w := r * 0.18
	b.moveTo(cx, cy-r)
	b.quadTo(cx+w, cy-w, cx+r, cy)
	b.quadTo(cx+w, cy+w, cx, cy+r)
	b.quadTo(cx-w, cy+w, cx-r, cy)
	b.quadTo(cx-w, cy-w, cx, cy-r)
	b.z.ClosePath()
}

func sqrt32(v float32) float32 { return float32(math.Sqrt(float64(v))) }

// gradient 是左上 → 右下的线性渐变源图。
type gradient struct {
	from, to color.NRGBA
	size     float32
}

func (g gradient) ColorModel() color.Model { return color.NRGBAModel }
func (g gradient) Bounds() image.Rectangle {
	return image.Rect(-1e9, -1e9, 1e9, 1e9)
}
func (g gradient) At(x, y int) color.Color {
	t := (float32(x) + float32(y)) / (2 * g.size)
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	lerp := func(a, b uint8) uint8 { return uint8(float32(a) + (float32(b)-float32(a))*t + 0.5) }
	return color.NRGBA{lerp(g.from.R, g.to.R), lerp(g.from.G, g.to.G), lerp(g.from.B, g.to.B), 0xFF}
}
