package converter

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// 转换状态
const (
	StatusOK    = "ok"
	StatusSkip  = "skip"
	StatusError = "error"
)

// Options 一次转换任务的参数。
type Options struct {
	// Quality 等效 JPEG 质量 1-100；0 表示使用各格式默认档。
	Quality int
	// Lossless 无损编码（webp/avif/jxl 生效；png/bmp/tiff 天然无损，忽略此标志；
	// jpeg/gif 不支持无损，忽略此标志）。
	Lossless bool
	// ResizeMode 输出尺寸模式："" 不缩放；"fw"/"fh"/"fl" 固定宽/高/长边
	// （等比缩放，可放大可缩小）；"pct" 按百分比等比缩放（100 = 原尺寸）；
	// "w"/"l" 限制宽/长边（仅缩小，CLI -width 使用）。
	ResizeMode string
	// ResizeValue 限制/固定边的像素值（<=0 时等效不缩放）。
	ResizeValue int
	// Overwrite 覆盖已存在的输出文件（默认跳过，便于断点续转）。
	Overwrite bool
	// Workers 并发 vips 进程数；<=0 表示 CPU 核数。
	Workers int
	// OutDir 输出根目录；空字符串表示输出到原目录（与源文件同级）。
	OutDir string
}

// WantSourceSize 需要源图显示尺寸的模式：固定宽/高/长边与百分比缩放
// 都要先读源图尺寸，才能算出精确的目标边或百分比目标。
func (o Options) WantSourceSize() bool {
	switch o.ResizeMode {
	case "fw", "fh", "fl", "pct":
		return o.ResizeValue > 0
	}
	return false
}

func (o Options) workers() int {
	n := o.Workers
	if n <= 0 {
		n = runtime.NumCPU()
	}
	if n > 256 {
		n = 256
	}
	return n
}

// Job 一个待转换的输入文件。
type Job struct {
	// JobID 调用方自定义的标识，原样回填到 Result.JobID（GUI 用于定位列表行）。
	JobID string
	// Path 源文件绝对路径。
	Path string
	// Base 相对路径的基准目录（拖入目录时为该目录，拖入文件时为文件所在目录）。
	Base string
	// Rel 相对 Base 的路径，决定输出文件的子目录结构。
	Rel string
	// Size 源文件字节数（读取失败时为 0）。
	Size int64
}

// Result 一个 (文件, 格式) 组合的转换结果。
type Result struct {
	// JobID 由调用方携带的标识（GUI 用于回写列表行）。
	JobID string `json:"jobId"`
	// Format 目标格式名。
	Format string `json:"format"`
	// Src 源文件绝对路径。
	Src string `json:"src"`
	// Dst 输出文件绝对路径。
	Dst string `json:"dst"`
	// Status 取值 StatusOK / StatusSkip / StatusError。
	Status string `json:"status"`
	// Error 失败原因（Status 为 error 时有效）。
	Error string `json:"error"`
	// InBytes / OutBytes 输入与输出字节数。
	InBytes  int64 `json:"inBytes"`
	OutBytes int64 `json:"outBytes"`
	// DurationMs 本次 vips 进程耗时（毫秒）。
	DurationMs int64 `json:"durationMs"`
}

// Engine 基于 vips 的批量转换引擎。
type Engine struct {
	v *Vips
}

// NewEngine 创建引擎。
func NewEngine(v *Vips) *Engine { return &Engine{v: v} }

// OutputPath 计算输出文件绝对路径。
//
//	OutDir 为空 → <Base>/<RelDir>/<stem><ext>（与原文件同级）
//	OutDir 非空 → <OutDir>/<Base 目录名>/<RelDir>/<stem><ext>
func (o Options) OutputPath(j Job, f *Format) string {
	relDir := filepath.Dir(j.Rel)
	stem := strings.TrimSuffix(filepath.Base(j.Rel), filepath.Ext(j.Rel))
	root := filepath.Join(j.Base, relDir)
	if o.OutDir != "" {
		root = filepath.Join(o.OutDir, filepath.Base(j.Base), relDir)
	}
	return filepath.Join(root, stem+f.Ext)
}

// ExpandPaths 把用户拖入/选择的文件与目录展开为 Job 列表。
//
// exclude 中的目录会被跳过，避免把上一轮的输出产物再次当作输入。
// 结果按路径排序，保证稳定。
func (e *Engine) ExpandPaths(paths []string, exclude []string) []Job {
	var jobs []Job
	seen := map[string]bool{}
	for _, p := range paths {
		p = strings.Trim(strings.TrimSpace(p), `"`)
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		abs = filepath.Clean(abs)
		info, err := os.Stat(abs)
		if err != nil {
			continue
		}
		if info.IsDir() {
			jobs = append(jobs, e.scanDir(abs, exclude, seen)...)
			continue
		}
		if !e.isImage(abs) || UnderAny(abs, exclude) {
			continue
		}
		if seen[strings.ToLower(abs)] {
			continue
		}
		seen[strings.ToLower(abs)] = true
		jobs = append(jobs, Job{Path: abs, Base: filepath.Dir(abs), Rel: filepath.Base(abs), Size: info.Size()})
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Path < jobs[j].Path })
	return jobs
}

func (e *Engine) scanDir(root string, exclude []string, seen map[string]bool) []Job {
	var jobs []Job
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 跳过无法访问的条目
		}
		if d.IsDir() {
			return nil // 始终递归进入子目录
		}
		if UnderAny(path, exclude) || !e.isImage(path) {
			return nil
		}
		if seen[strings.ToLower(path)] {
			return nil
		}
		seen[strings.ToLower(path)] = true
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = d.Name()
		}
		var size int64
		if fi, err := d.Info(); err == nil {
			size = fi.Size()
		}
		jobs = append(jobs, Job{Path: path, Base: root, Rel: rel, Size: size})
		return nil
	})
	return jobs
}

func (e *Engine) isImage(path string) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	return ext != "" && e.v.SupportsInput(ext)
}

// UnderAny 判断 path 是否位于 dirs 中任一目录内（目录本身也算）。
// 比较忽略大小写（Windows 路径不区分大小写）。
func UnderAny(path string, dirs []string) bool {
	lp := strings.ToLower(path)
	for _, d := range dirs {
		if d == "" {
			continue
		}
		rel, err := filepath.Rel(strings.ToLower(d), lp)
		if err != nil {
			continue
		}
		if rel == "." {
			return true
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

var tmpSeq int64

// Convert 并发转换 jobs × fmts，并通过 onResult 回调逐个上报结果。
//
// ctx 取消后不再下发新任务，已启动的 vips 进程会尽快收尾。
// onResult 可能被并发调用，回调实现需自行保证线程安全。
func (e *Engine) Convert(ctx context.Context, jobs []Job, fmts []*Format, opts Options, onResult func(Result)) {
	if len(jobs) == 0 || len(fmts) == 0 || onResult == nil {
		return
	}

	jobsCh := make(chan Job)
	go func() {
		defer close(jobsCh)
		for _, j := range jobs {
			select {
			case jobsCh <- j:
			case <-ctx.Done():
				return
			}
		}
	}()

	workers := opts.workers()
	if workers > len(jobs) {
		workers = len(jobs)
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobsCh {
				select {
				case <-ctx.Done():
					return
				default:
				}
				// 缩放目标只与（文件、模式）有关、与格式无关，每文件只算一次；
				// 精确缩放档（固定宽/高/长边、百分比）需先读源图旋转后的显示尺寸。
				var rt ResizeTarget
				if opts.WantSourceSize() {
					w, h, err := e.v.ImageSize(j.Path)
					if err != nil {
						for _, f := range fmts {
							dst := opts.OutputPath(j, f)
							onResult(Result{JobID: j.JobID, Format: f.Name, Src: j.Path,
								Dst: dst, InBytes: j.Size, Status: StatusError,
								Error: "读取源图尺寸失败（此缩放模式需要源图尺寸）: " + err.Error()})
						}
						continue
					}
					rt = TargetFor(opts.ResizeMode, opts.ResizeValue, w, h)
				} else {
					rt = TargetFor(opts.ResizeMode, opts.ResizeValue, 0, 0)
				}
				for _, f := range fmts {
					q := f.QualityFor(opts.Quality)
					dst := opts.OutputPath(j, f)
					onResult(e.convertOne(j.JobID, f, j, dst, q, opts, rt))
				}
			}
		}()
	}
	wg.Wait()
}

// convertOne 转换单个文件到单个目标格式。
// 先写临时文件再改名，避免中断留下半截文件；失败时清理临时文件。
func (e *Engine) convertOne(id string, f *Format, j Job, dst string, q int, opts Options, rt ResizeTarget) Result {
	r := Result{JobID: id, Format: f.Name, Src: j.Path, Dst: dst, InBytes: j.Size, Status: StatusOK}

	// 输出与源文件同路径 = 原地重编码（同格式输出到源目录，会覆盖原文件）。
	// 先写临时文件再替换，中断不会损坏原图；但覆盖源文件必须由用户显式
	// 开启 Overwrite 授权，否则提示改用其它输出目录。
	if strings.EqualFold(j.Path, dst) {
		if !opts.Overwrite {
			r.Status, r.Error = StatusError,
				"输出与源文件相同（同格式原地重编码将覆盖原文件）：请在高级设置开启“覆盖已存在文件”，或改用其它输出目录"
			return r
		}
	} else if !opts.Overwrite {
		if fi, err := os.Stat(dst); err == nil && fi.Size() > 0 {
			r.Status = StatusSkip
			return r
		}
	}

	outDir := filepath.Dir(dst)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		r.Status, r.Error = StatusError, fmt.Sprintf("创建输出目录失败: %v", err)
		return r
	}

	// 临时文件名必须保留目标扩展名：vips 靠扩展名选择编码器，
	// 所以把唯一标记放在扩展名之前（如 a.13.avif）。
	ext := filepath.Ext(dst)
	tmp := fmt.Sprintf("%s.%d%s", strings.TrimSuffix(dst, ext), atomic.AddInt64(&tmpSeq, 1), ext)
	// moveFileOverwrite 彻底失败时保留 tmp（原地覆盖场景下它可能是唯一的内容
	// 副本），并在错误信息里给出路径交给用户抢救；其余失败路径照常清理。
	keepTmp := false
	defer func() {
		if !keepTmp {
			_ = os.Remove(tmp)
		}
	}()

	start := time.Now()
	args := f.BuildVipsArgs(j.Path, tmp, q, opts.Lossless, rt)
	out, err := e.v.run(args...)
	r.DurationMs = time.Since(start).Milliseconds()
	if err != nil {
		r.Status, r.Error = StatusError, fmt.Sprintf("vips 退出码异常: %v | %s", err, OneLine(out))
		return r
	}
	fi, err := os.Stat(tmp)
	if err != nil {
		r.Status, r.Error = StatusError, fmt.Sprintf("未生成输出文件: %v | %s", err, OneLine(out))
		return r
	}
	if fi.Size() == 0 {
		r.Status, r.Error = StatusError, fmt.Sprintf("输出文件为空 | %s", OneLine(out))
		return r
	}
	if err := moveFileOverwrite(tmp, dst); err != nil {
		keepTmp = true
		r.Status, r.Error = StatusError, fmt.Sprintf("%v；输出内容暂存于 %s", err, tmp)
		return r
	}
	r.OutBytes = fi.Size()
	return r
}

// moveFileOverwrite 将 src 移动到 dst，必要时覆盖已存在的 dst。
// Windows 下目标文件可能被杀毒软件、索引服务或资源管理器缩略图缓存短暂锁定，
// 或目标为只读属性，二者都会在重命名时报“拒绝访问（Access is denied）”。
// 这里用“去掉只读位 + 删除 + 重试”的组合消解这类瞬时/属性问题：
// 锁定通常是毫秒级的，重试几次后即可成功；只读位先 chmod 去掉再删/移。
// 若仍失败（如跨设备），回退为复制后删除源文件。
func moveFileOverwrite(src, dst string) error {
	const maxRetry = 6
	var lastErr error
	for i := 0; i < maxRetry; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * 20 * time.Millisecond)
		}
		if _, err := os.Stat(dst); err == nil {
			_ = os.Chmod(dst, 0o644) // 去掉只读位：否则 Windows 覆盖只读文件会 Access Denied
			_ = os.Remove(dst)
		}
		err := os.Rename(src, dst)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	// rename 失败（如跨设备）时回退为复制；复制成功即视为成功
	if copyErr := copyFile(src, dst); copyErr == nil {
		_ = os.Remove(src)
		return nil
	}
	// 删掉复制到一半的目标文件，避免留下半截产物；src（临时文件）不删：
	// 原地覆盖场景下它可能是唯一的内容副本，由调用方把路径报给用户抢救。
	_ = os.Remove(dst)
	return fmt.Errorf("写入输出文件失败（重命名被拒绝访问，可能目标被其它程序占用或为只读）: %w", lastErr)
}

// copyFile 将 src 复制到 dst（不保留额外元数据）。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// OneLine 把多行输出压成一行，并截断到 300 字符（按 UTF-8 边界截，避免中文乱码）。
func OneLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 300 {
		s = trimPartialRuneTail(s[:300]) + "..."
	}
	return s
}

// trimPartialRuneTail 去掉结尾不完整的 UTF-8 序列（按字节截断会把多字节字符切成一半）。
func trimPartialRuneTail(s string) string {
	for len(s) > 0 {
		r, size := utf8.DecodeLastRuneInString(s)
		if r != utf8.RuneError || size != 1 {
			return s
		}
		s = s[:len(s)-1]
	}
	return s
}

// trimPartialRuneHead 去掉开头不完整的 UTF-8 序列。
func trimPartialRuneHead(s string) string {
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		if r != utf8.RuneError || size != 1 {
			return s
		}
		s = s[size:]
	}
	return s
}
