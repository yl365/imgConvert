package converter

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Vips 封装随包 vips.exe 的定位与自检结果。
type Vips struct {
	exePath string
	// headerPath 随包 vipsheader.exe 的完整路径（固定宽模式读取源图尺寸用）。
	headerPath string
	version    string
	// loaders: 该 vips 构建实际可用（出现在 vips -l 中）的输入扩展名集合（小写、无点）
	loaders map[string]bool
}

var (
	// 识别 "vips -l" 输出中的 loader 行：VipsForeignLoadXxx (alias), load ...
	reLoaderLine = regexp.MustCompile(`^\s*VipsForeignLoad\w*\s*\(\w+\)\s*,\s*load\b`)
	// 行内所有括号组，用于挑选真正的扩展名列表
	reParenGroup = regexp.MustCompile(`\(([^)]*)\)`)
	// 排除明显不属于“普通图像”的扩展名（文档/医学/科研/源码类）
	nonImageExts = map[string]bool{
		"csv": true, "tsv": true, "mat": true, "pdf": true, "ps": true, "eps": true,
		"dcm": true, "dicom": true, "fits": true, "fit": true, "fts": true,
		"nii": true, "nia": true, "img": true, "hdr": true,
		"svs": true, "ndpi": true, "vms": true, "vmu": true, "scn": true,
		"mrxs": true, "svslide": true, "bif": true, "gz": true, "bz2": true,
	}
	// "vipsheader -a" 输出中的字段行（整行锚定，避免误配 exif-ifd0-Orientation 等前缀行）
	reHdrWidth  = regexp.MustCompile(`(?m)^width:\s*(\d+)\s*$`)
	reHdrHeight = regexp.MustCompile(`(?m)^height:\s*(\d+)\s*$`)
	reHdrOrient = regexp.MustCompile(`(?m)^orientation:\s*(\d+)\s*$`)
)

// FindVips 定位随包 vips.exe：
//  1. 环境变量 imgConvert_VIPS 显式指定（开发调试用）
//  2. <exe 同目录>/vips/vips.exe        （发布形态）
//  3. <exe 同目录>/vips.exe             （vips 与工具平铺形态）
//  4. <当前目录>/vips/vips.exe          （go run 开发形态）
//  5. 从 exe 同目录 / 当前目录逐级向上（最多 6 层）找 vips/vips.exe、
//     imgConvert/vips/vips.exe          （仓库内开发形态）
//
// 不搜索系统 PATH，保证绿色免安装、不依赖环境。
func FindVips() (string, error) {
	if p := os.Getenv("imgConvert_VIPS"); p != "" {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
		return "", fmt.Errorf("环境变量 imgConvert_VIPS 指向的文件不存在: %s", p)
	}

	exe, err := os.Executable()
	if err != nil {
		exe = "."
	}
	exeDir := filepath.Dir(exe)
	cwd, _ := os.Getwd()

	seen := map[string]bool{}
	var candidates []string
	add := func(p string) {
		if p == "" {
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		key := strings.ToLower(p)
		if seen[key] {
			return
		}
		seen[key] = true
		candidates = append(candidates, p)
	}

	for _, base := range []string{exeDir, cwd} {
		add(filepath.Join(base, "tools", "vips.exe"))
		add(filepath.Join(base, "vips.exe"))
		dir := base
		for i := 0; i < 6; i++ {
			add(filepath.Join(dir, "tools", "vips.exe"))
			add(filepath.Join(dir, "imgConvert", "tools", "vips.exe"))
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf(
		"未找到 vips.exe。请在程序同目录放置随包的 tools/ 目录（含 vips.exe 与全部 DLL/插件），" +
			"或通过环境变量 imgConvert_VIPS 指定完整路径")
}

// NewVips 定位并自检 vips。成功返回的实例已解析出可用输入扩展名集合。
func NewVips() (*Vips, error) {
	path, err := FindVips()
	if err != nil {
		return nil, err
	}
	v := &Vips{exePath: path, headerPath: filepath.Join(filepath.Dir(path), "vipsheader.exe")}
	if err := v.discover(); err != nil {
		return nil, err
	}
	return v, nil
}

// Path 返回 vips.exe 的绝对路径。
func (v *Vips) Path() string { return v.exePath }

// Version 返回 "vips --version" 解析出的版本串。
func (v *Vips) Version() string { return v.version }

// InputExts 返回可被该 vips 解码的输入扩展名（小写、无点，已排序）。
func (v *Vips) InputExts() []string {
	exts := make([]string, 0, len(v.loaders))
	for e := range v.loaders {
		exts = append(exts, e)
	}
	sort.Strings(exts)
	return exts
}

// SupportsInput 判断扩展名（小写、无点）是否可被该 vips 解码。
func (v *Vips) SupportsInput(ext string) bool {
	if v.loaders == nil {
		return false
	}
	return v.loaders[ext]
}

// discover 运行 vips -l 并解析：
//   - 二进制可执行、随附 DLL 可加载（启动即报错）
//   - 可用输入 loader 扩展名集合（决定目录扫描时收哪些文件）
func (v *Vips) discover() error {
	if ver, err := v.run("--version"); err == nil {
		// 模块加载失败等情况下 vips 会先打印 WARNING，取最后一行真正的版本号
		v.version = parseVipsVersion(ver)
	}
	out, err := v.run("-l")
	if err != nil {
		return fmt.Errorf("vips.exe 启动失败（可能缺少随附 DLL 或运行库）: %v\n%s", err, tail(out))
	}
	v.loaders = parseLoaderExts(out)
	if len(v.loaders) == 0 {
		// -l 输出非预期（老版本 CLI），回退到常见光栅格式白名单
		v.loaders = fallbackRasterExts()
	}
	return nil
}

func parseVipsVersion(out string) string {
	ver := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "vips-") {
			ver = line
		}
	}
	if ver == "" {
		ver = strings.TrimSpace(out)
	}
	return ver
}

func parseLoaderExts(list string) map[string]bool {
	exts := map[string]bool{}
	for _, line := range strings.Split(list, "\n") {
		if !reLoaderLine.MatchString(line) {
			continue
		}
		group := pickExtGroup(line)
		if group == "" {
			continue // 基类 loader 行不带扩展名
		}
		for _, tok := range strings.Split(group, ",") {
			tok = strings.TrimSpace(tok)
			if !strings.HasPrefix(tok, ".") {
				continue
			}
			e := strings.ToLower(strings.TrimPrefix(tok, "."))
			if e == "" || strings.Contains(e, ".") {
				continue // 忽略 .nii.gz / .svg.gz 这类复合扩展名
			}
			if nonImageExts[e] {
				continue
			}
			exts[e] = true
		}
	}
	return exts
}

// pickExtGroup 取行内第一个“全部 token 都以点开头”的括号组。
// 形如 `load PDF from file (poppler) (.pdf)` 的行会先出现库名，必须跳过它。
func pickExtGroup(line string) string {
	for _, g := range reParenGroup.FindAllStringSubmatch(line, -1) {
		content := strings.TrimSpace(g[1])
		if content == "" {
			continue
		}
		allExt := true
		for _, tok := range strings.Split(content, ",") {
			if !strings.HasPrefix(strings.TrimSpace(tok), ".") {
				allExt = false
				break
			}
		}
		if allExt {
			return content
		}
	}
	return ""
}

// fallbackRasterExts 仅在 vips -l 无法解析时使用。
func fallbackRasterExts() map[string]bool {
	out := map[string]bool{}
	for _, e := range []string{
		"jpg", "jpeg", "jpe", "jfif", "png", "webp", "avif", "heic", "heif",
		"tif", "tiff", "gif", "jxl", "exr", "bmp",
	} {
		out[e] = true
	}
	return out
}

// ImageSize 返回图像按 EXIF 方向旋转后的显示尺寸（宽、高）。
//
// vips thumbnail 默认按 orientation 自动旋转，而 vipsheader 报告的是文件
// 原始像素尺寸；当 orientation 为 5-8（旋转 90/270 度）时交换宽高保持一致。
func (v *Vips) ImageSize(path string) (int, int, error) {
	if info, err := os.Stat(v.headerPath); err != nil || info.IsDir() {
		return 0, 0, fmt.Errorf("未找到随包的 vipsheader.exe（精确缩放模式需要它）")
	}
	out, err := v.runHeader("-a", path)
	if err != nil {
		return 0, 0, fmt.Errorf("vipsheader 失败: %v | %s", err, tail(out))
	}
	w, h, err := parseHeaderSize(out)
	if err != nil {
		return 0, 0, fmt.Errorf("解析 %s 尺寸失败: %v", path, err)
	}
	return w, h, nil
}

// parseHeaderSize 解析 "vipsheader -a" 输出的 width/height/orientation 字段。
func parseHeaderSize(out string) (int, int, error) {
	mw := reHdrWidth.FindStringSubmatch(out)
	mh := reHdrHeight.FindStringSubmatch(out)
	if mw == nil || mh == nil {
		return 0, 0, fmt.Errorf("输出缺少 width/height 字段: %s", OneLine(out))
	}
	w, errW := strconv.Atoi(mw[1])
	h, errH := strconv.Atoi(mh[1])
	if errW != nil || errH != nil || w < 1 || h < 1 {
		return 0, 0, fmt.Errorf("width/height 数值异常: %s", OneLine(out))
	}
	if mo := reHdrOrient.FindStringSubmatch(out); mo != nil {
		if o, err := strconv.Atoi(mo[1]); err == nil && o >= 5 && o <= 8 {
			w, h = h, w
		}
	}
	return w, h, nil
}

// run 执行一次 vips，捕获 stdout+stderr。
func (v *Vips) run(args ...string) (string, error) {
	cmd := newCmd(v.exePath, args...)
	cmd.Dir = filepath.Dir(v.exePath) // 保证相对模块目录/辅助文件解析稳定
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runHeader 执行一次 vipsheader（与 vips.exe 同目录随包，DLL 依赖一致）。
func (v *Vips) runHeader(args ...string) (string, error) {
	cmd := newCmd(v.headerPath, args...)
	cmd.Dir = filepath.Dir(v.headerPath)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 600 {
		s = "..." + trimPartialRuneHead(s[len(s)-600:])
	}
	if s == "" {
		s = "(无输出)"
	}
	return s
}
