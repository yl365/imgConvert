// Package converter 是 imgConvert 的转换引擎：负责定位随包 vips、自检可用编解码能力，
// 以及并发驱动 vips 完成图片格式转换。
//
// CLI（imgConvert）与 GUI（Wails 应用）共用本包，保证两边行为一致。
package converter

import (
	"math"
	"strconv"
	"strings"
)

// Format 描述一种可输出的目标格式及其 vips 编码参数构造方式。
type Format struct {
	Name             string // 格式名（avif/webp/jpeg/jxl/png/gif/tiff）
	Ext              string // 输出文件扩展名（含点）
	Op               string // 非缩放路径使用的 vips 保存操作名
	DefaultQ         int    // 未显式指定质量时的默认质量（lossless 时忽略）
	LosslessCapable  bool   // 是否支持无损编码（webp/avif/jxl）
	LossyCapable     bool   // 是否支持有损质量参数（png 不支持）
	NeedsAv1Compress bool   // 保存时是否需要显式 --compression av1
	Tip              string // 前端格式选择项的提示文案
}

// Formats 全部可选输出格式。
//
// DefaultQ 取值：体积优先的“照片良好画质”档，按同一客观口径（PSNR ≈ 45 dB、
// SSIM ≈ 0.99，肉眼基本无差）实测对齐。Q 刻度跨编码器不可比，勿跨格式用同一数值。
var Formats = []Format{
	{Name: "avif", Ext: ".avif", Op: "heifsave", DefaultQ: 62, LosslessCapable: true, LossyCapable: true, NeedsAv1Compress: true, Tip: "新一代高效格式，同等画质下体积通常比 JPEG 小 30–50%"},
	{Name: "webp", Ext: ".webp", Op: "webpsave", DefaultQ: 80, LosslessCapable: true, LossyCapable: true, Tip: "谷歌推出的通用格式，支持有损/无损与透明，Web 首选"},
	{Name: "jpeg", Ext: ".jpg", Op: "jpegsave", DefaultQ: 80, LosslessCapable: false, LossyCapable: true, Tip: "兼容性最广的有损格式，适合照片但不支持透明"},
	{Name: "jxl", Ext: ".jxl", Op: "jxlsave", DefaultQ: 80, LosslessCapable: true, LossyCapable: true, Tip: "最先进的下一代格式，高压缩率并支持无损/动画/透明"},
	{Name: "png", Ext: ".png", Op: "pngsave", DefaultQ: 0, LosslessCapable: true, LossyCapable: false, Tip: "无损压缩，支持透明通道，适合图标与截图"},
	{Name: "gif", Ext: ".gif", Op: "gifsave", DefaultQ: 0, LosslessCapable: false, LossyCapable: false, Tip: "支持简单动画与透明，仅 256 色，体积通常较大"},
	{Name: "tiff", Ext: ".tiff", Op: "tiffsave", DefaultQ: 0, LosslessCapable: true, LossyCapable: false, Tip: "专业印刷与存档常用，支持无损多页，兼容性较好"},
}

// FindFormat 按名字查找格式；jpg 归一为 jpeg。
func FindFormat(name string) (*Format, bool) {
	switch name {
	case "jpg":
		name = "jpeg"
	case "tif":
		name = "tiff"
	}
	for i := range Formats {
		if Formats[i].Name == name {
			return &Formats[i], true
		}
	}
	return nil, false
}

// FormatNames 返回全部格式名，空格分隔（用于错误提示）。
func FormatNames() string {
	names := make([]string, 0, len(Formats))
	for _, f := range Formats {
		names = append(names, f.Name)
	}
	return strings.Join(names, " ")
}

// QualityFor 返回实际用于有损编码的质量。
//
// png 天然无损、无质量刻度，恒返回 100（仅用于展示，编码路径不使用该值）。
// 其余格式：显式质量按“等效 JPEG 质量刻度”解释（jpeg/webp/jxl 恒等，avif 经
// avifCalib 换算）；0 表示使用各格式默认档。
func (f *Format) QualityFor(userQ int) int {
	if !f.LossyCapable {
		return 100
	}
	if userQ > 0 {
		return f.mapFromJpeg(userQ)
	}
	return f.DefaultQ
}

// DefaultLossless 返回该格式默认是否走无损编码（无损/有损不对外提供配置）。
//
// 无质量刻度的格式（png）天然无损；其余格式默认走有损，质量由调用方给的质量值决定。
func (f *Format) DefaultLossless() bool {
	return !f.LossyCapable
}

// avifCalib AVIF 的“等效 JPEG 质量 qj -> AVIF 的 Q”锚点表（锚点间线性插值）。
// 差值非常数、呈钟形：q≤45 时与 JPEG 同刻度，65~85 段最大（-15~-19），q→100 收窄到 -1。
var avifCalib = []struct {
	qj, qf int
}{
	{1, 1}, {45, 45}, {55, 50}, {65, 52}, {70, 53},
	{80, 62}, {85, 67}, {90, 78}, {95, 88}, {100, 99},
}

func (f *Format) mapFromJpeg(q int) int {
	if q <= 0 {
		return 0
	}
	switch f.Name {
	case "avif":
		return interpCalib(avifCalib, q)
	}
	// jpeg/webp/jxl 与 JPEG 同刻度；未来新增的有损格式若未单独校准，也按同刻度直传。
	return q
}

// interpCalib 在锚点表（按 qj 升序）上线性插值；q 越界钳制到端点。
func interpCalib(pts []struct {
	qj, qf int
}, q int) int {
	if q <= pts[0].qj {
		return pts[0].qf
	}
	last := pts[len(pts)-1]
	if q >= last.qj {
		return last.qf
	}
	for i := 1; i < len(pts); i++ {
		if q <= pts[i].qj {
			a, b := pts[i-1], pts[i]
			t := float64(q-a.qj) / float64(b.qj-a.qj)
			return int(math.Round(float64(a.qf) + t*float64(b.qf-a.qf)))
		}
	}
	return last.qf
}

// saveFlags 保存选项的 CLI 参数（非缩放路径：vips <op> in out --xxx ...）
func (f *Format) saveFlags(q int, lossless bool) []string {
	var flags []string
	if f.NeedsAv1Compress {
		flags = append(flags, "--compression", "av1")
	}
	// --lossless 仅对“可无损也有损”的格式有意义；png 天然无损，vips pngsave 不接受该参数
	if lossless && f.LosslessCapable && f.LossyCapable {
		flags = append(flags, "--lossless")
	} else if f.LossyCapable {
		flags = append(flags, "--Q", strconv.Itoa(q))
	}
	// vips jpegsave 的 optimize-coding 默认关，开启 Huffman 优化约省 5-10% 体积、画质不变
	if f.Name == "jpeg" {
		flags = append(flags, "--optimize-coding")
	}
	// tiff 默认不压缩（文件偏大），改用 deflate 无损压缩：体积显著更小且画质不变
	if f.Name == "tiff" {
		flags = append(flags, "--compression", "deflate")
	}
	return flags
}

// saveBracket 保存选项的内嵌语法（缩放路径：vips thumbnail in "out[opt=val,...]" width --size down）
func (f *Format) saveBracket(q int, lossless bool) string {
	var opts []string
	if f.NeedsAv1Compress {
		opts = append(opts, "compression=av1")
	}
	if lossless && f.LosslessCapable && f.LossyCapable {
		opts = append(opts, "lossless=true")
	} else if f.LossyCapable {
		opts = append(opts, "Q="+strconv.Itoa(q))
	}
	if f.Name == "jpeg" {
		opts = append(opts, "optimize-coding=true")
	}
	if f.Name == "tiff" {
		opts = append(opts, "compression=deflate")
	}
	if len(opts) == 0 {
		return ""
	}
	return "[" + strings.Join(opts, ",") + "]"
}

// ResizeTarget 描述一次等比缩放目标（翻译成 vips thumbnail 参数）。
// Size 为空表示不缩放（走非缩放保存路径）。
type ResizeTarget struct {
	Size string // thumbnail 的宽度参数："<px>"、目标宽整数或 "<px>x<px>"（长边盒）
	// Height >0 时追加 "--height <h>"：与 Size 构成等比的两维目标，libvips
	// 8.18 实测输出一边恰好等于给定值、另一边按源图同比例得出；fw/fh/fl 用。
	Height int
	// Down 追加 "--size down"（仅缩小不放大；CLI -width 的 w/l 档使用）。
	Down bool
}

// TargetFor 把“缩放模式 + 数值（+ 源图旋转后显示尺寸）”翻译成 ResizeTarget。
//
// GUI 五档（等比缩放，可放大可缩小；除原尺寸外都需要 srcW/srcH 精确推算）：
//
//	fw:  固定宽 —— 输出宽恰好 = px。
//	fh:  固定高 —— 输出高恰好 = px。
//	fl:  固定长边 —— 输出长边恰好 = px（宽、高中较长的一边）。
//	pct: 等比缩放 —— 输出为源图的 px%：按源图宽算目标宽（四舍五入）后以
//	     单边驱动 thumbnail 同比例缩放；px = 100 时直接不缩放。
//
// CLI / 历史档位（仅缩小不放大，不需要源图尺寸）：
//
//	w:  限制宽度 —— 宽 ≤ px。
//	l:  限制长边 —— 长边 ≤ px（等比放入 px×px 方框）。
//
// px <= 0、模式未知、或需要源图尺寸的模式缺少 srcW/srcH 时返回空目标（不缩放）。
func TargetFor(mode string, px, srcW, srcH int) ResizeTarget {
	if px <= 0 {
		return ResizeTarget{}
	}
	switch mode {
	case "w":
		return ResizeTarget{Size: strconv.Itoa(px), Down: true}
	case "l":
		return ResizeTarget{Size: strconv.Itoa(px) + "x" + strconv.Itoa(px), Down: true}
	case "fw", "fh", "fl":
		if srcW <= 0 || srcH <= 0 {
			return ResizeTarget{}
		}
		if mode == "fw" || (mode == "fl" && srcW >= srcH) {
			// 固定（长）边 = 宽：另一维取上整，保证等比后宽度恰好为 px。
			return ResizeTarget{Size: strconv.Itoa(px), Height: fixedPair(px, srcH, srcW)}
		}
		// 固定（长）边 = 高：另一维取上整，保证等比后高度恰好为 px。
		return ResizeTarget{Size: strconv.Itoa(fixedPair(px, srcW, srcH)), Height: px}
	case "pct":
		if srcW <= 0 || srcH <= 0 || px == 100 {
			return ResizeTarget{}
		}
		// 以“目标宽 = round(源宽 × px%)”单边驱动，两维按同一比例缩放最干净。
		return ResizeTarget{Size: strconv.Itoa(scalePct(srcW, px))}
	}
	return ResizeTarget{}
}

// fixedPair 按与固定边 px 相同的比例计算另一边，取上整保证固定边恰好为 px。
func fixedPair(px, other, fixed int) int {
	if v := (px*other + fixed - 1) / fixed; v >= 1 {
		return v
	}
	return 1
}

// scalePct 四舍五入地计算 n × px%，结果至少为 1。
func scalePct(n, px int) int {
	if v := (n*px + 50) / 100; v >= 1 {
		return v
	}
	return 1
}

// BuildVipsArgs 构造一次 vips 进程的完整参数。
// rt.Size 非空（见 TargetFor）时走 vips thumbnail 一步完成“解码+等比缩放+保存”
// （thumbnail 默认按 EXIF 方向自动旋转；不带 --size down 时允许放大）；
// 否则走各格式专用保存操作。
func (f *Format) BuildVipsArgs(in, out string, q int, lossless bool, rt ResizeTarget) []string {
	if rt.Size != "" {
		args := []string{"thumbnail", in, out + f.saveBracket(q, lossless), rt.Size}
		if rt.Height > 0 {
			args = append(args, "--height", strconv.Itoa(rt.Height))
		}
		if rt.Down {
			args = append(args, "--size", "down")
		}
		return args
	}
	args := []string{f.Op, in, out}
	return append(args, f.saveFlags(q, lossless)...)
}
