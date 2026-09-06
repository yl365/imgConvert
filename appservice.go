package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"imgConvert/converter"
)

// ---------- 事件载荷 ----------

// ResultEvent 单个 (文件, 格式) 组合的转换结果，随 convert:result 事件下发。
type ResultEvent struct {
	JobID      string `json:"jobId"`
	Format     string `json:"format"`
	Status     string `json:"status"`
	Error      string `json:"error"`
	OutPath    string `json:"outPath"`
	InBytes    int64  `json:"inBytes"`
	OutBytes   int64  `json:"outBytes"`
	DurationMs int64  `json:"durationMs"`
	Done       int    `json:"done"`
	Total      int    `json:"total"`
}

// ProgressEvent 整体进度，随 convert:progress 事件下发。
type ProgressEvent struct {
	Running   bool  `json:"running"`
	Done      int   `json:"done"`
	Total     int   `json:"total"`
	Ok        int   `json:"ok"`
	Skip      int   `json:"skip"`
	Fail      int   `json:"fail"`
	ElapsedMs int64 `json:"elapsedMs"`
}

// SummaryEvent 批次结束汇总，随 convert:done 事件下发。
type SummaryEvent struct {
	Ok        int     `json:"ok"`
	Skip      int     `json:"skip"`
	Fail      int     `json:"fail"`
	InBytes   int64   `json:"inBytes"`
	OutBytes  int64   `json:"outBytes"`
	Ratio     float64 `json:"ratio"`
	ElapsedMs int64   `json:"elapsedMs"`
	Canceled  bool    `json:"canceled"`
}

// ErrorEvent 运行期错误提示，随 convert:error 事件下发。
type ErrorEvent struct {
	Message string `json:"message"`
}

// ---------- 前端数据模型 ----------

// FileItem 列表中的一行（一个源文件）。
type FileItem struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	Base     string  `json:"base"`
	Rel      string  `json:"rel"`
	Size     int64   `json:"size"`
	Status   string  `json:"status"` // pending / running / ok / skip / error / canceled
	Duration int64   `json:"duration"`
	OutSize  int64   `json:"outSize"`
	Ratio    float64 `json:"ratio"`
	Error    string  `json:"error"`
	// Results 转换结果（输出格式单选，最多一条）
	Results []ResultItem `json:"results"`
}

// ResultItem 单个格式的结果明细。
type ResultItem struct {
	Format   string  `json:"format"`
	Status   string  `json:"status"`
	OutPath  string  `json:"outPath"`
	OutSize  int64   `json:"outSize"`
	Ratio    float64 `json:"ratio"`
	Duration int64   `json:"duration"`
	Error    string  `json:"error"`
}

// FormatInfo 供前端渲染格式选择项。
type FormatInfo struct {
	Name string `json:"name"`
	Ext  string `json:"ext"`
	Tip  string `json:"tip"`
}

// Info 应用启动信息。
type Info struct {
	AppVersion  string       `json:"appVersion"`
	Platform    string       `json:"platform"`
	Frameless   bool         `json:"frameless"`
	CPUs        int          `json:"cpus"`
	VipsVersion string       `json:"vipsVersion"`
	VipsPath    string       `json:"vipsPath"`
	EngineError string       `json:"engineError"`
	Formats     []FormatInfo `json:"formats"`
	InputExts   []string     `json:"inputExts"`
}

// ConvertConfig 前端“开始转换”时提交的参数。
type ConvertConfig struct {
	// Format 输出格式名，单选；是否无损由各格式内置预设决定。
	Format            string `json:"format"`
	Quality           int    `json:"quality"`
	ResizeMode        string `json:"resizeMode"`
	ResizeValue       int    `json:"resizeValue"`
	Overwrite         bool   `json:"overwrite"`
	Workers           int    `json:"workers"`
	OutDir            string `json:"outDir"`
	WriteLog          bool   `json:"writeLog"`
	LogPath           string `json:"logPath"`
}

// ---------- 服务 ----------

// AppService 是主窗口与设置窗口共用的后端服务。
type AppService struct {
	app *application.App

	mu       sync.Mutex
	files    []*FileItem
	byPath   map[string]*FileItem
	byID     map[string]*FileItem
	running  bool
	cancelFn context.CancelFunc

	vips   *converter.Vips
	eng    *converter.Engine
	engErr string

	settings *Settings
	logMu    sync.Mutex
	logFile  *os.File
}

var idSeq int64

// ServiceStartup 在应用启动时初始化引擎与配置。
func (s *AppService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.app = application.Get()
	s.byPath = map[string]*FileItem{}
	s.byID = map[string]*FileItem{}
	s.settings = LoadSettings()

	v, err := converter.NewVips()
	if err != nil {
		s.engErr = err.Error()
		s.app.Logger.Error("引擎不可用", "error", err)
		return nil // 不让应用退出，前端显示引擎错误提示
	}
	s.vips = v
	s.eng = converter.NewEngine(v)
	return nil
}

// ServiceShutdown 清理资源。
func (s *AppService) ServiceShutdown() error {
	s.mu.Lock()
	if s.cancelFn != nil {
		s.cancelFn()
		s.cancelFn = nil
	}
	s.mu.Unlock()
	s.closeLog()
	return nil
}

// ---------- 查询 ----------

// GetInfo 返回应用与引擎信息。
func (s *AppService) GetInfo() Info {
	info := Info{
		AppVersion: appVersion,
		Platform:   runtime.GOOS,
		Frameless:  useCustomTitleBar(),
		CPUs:       runtime.NumCPU(),
		Formats:    make([]FormatInfo, 0, len(converter.Formats)),
	}
	for _, f := range converter.Formats {
		info.Formats = append(info.Formats, FormatInfo{Name: f.Name, Ext: f.Ext, Tip: f.Tip})
	}
	if s.vips != nil {
		info.VipsVersion = s.vips.Version()
		info.VipsPath = s.vips.Path()
		info.InputExts = s.vips.InputExts()
	} else {
		info.EngineError = s.engErr
	}
	return info
}

// ---------- 文件管理 ----------

// SelectFiles 弹出多选文件对话框，返回选中的绝对路径。
func (s *AppService) SelectFiles() []string {
	dlg := s.app.Dialog.OpenFile().
		SetTitle("选择图片文件").
		AddFilter("图片文件", s.imageFilterPattern())
	files, err := dlg.PromptForMultipleSelection()
	if err != nil {
		s.emitError("选择文件失败: " + err.Error())
		return nil
	}
	return files
}

// SelectFolder 弹出目录选择对话框。
func (s *AppService) SelectFolder() string {
	dir, err := s.app.Dialog.OpenFile().
		SetTitle("选择文件夹").
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		PromptForSingleSelection()
	if err != nil {
		s.emitError("选择文件夹失败: " + err.Error())
		return ""
	}
	return dir
}

// SelectOutputDir 弹出输出目录选择对话框。
func (s *AppService) SelectOutputDir() string {
	dir, err := s.app.Dialog.OpenFile().
		SetTitle("选择输出目录").
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		PromptForSingleSelection()
	if err != nil {
		s.emitError("选择输出目录失败: " + err.Error())
		return ""
	}
	return dir
}

// AddPaths 把文件/目录展开为列表项并追加到列表（内部去重）。
func (s *AppService) AddPaths(paths []string) []FileItem {
	if s.eng == nil {
		s.emitError("引擎不可用，无法添加文件：" + s.engErr)
		return nil
	}
	s.mu.Lock()
	outDir := s.settings.OutDir()
	s.mu.Unlock()

	exclude := []string{}
	if outDir != "" {
		exclude = append(exclude, outDir)
	}
	jobs := s.eng.ExpandPaths(paths, exclude)
	if len(jobs) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	added := make([]FileItem, 0, len(jobs))
	for _, j := range jobs {
		key := strings.ToLower(j.Path)
		if _, ok := s.byPath[key]; ok {
			continue
		}
		item := &FileItem{
			ID:      fmt.Sprintf("%d", atomic.AddInt64(&idSeq, 1)),
			Name:    filepath.Base(j.Path),
			Path:    j.Path,
			Base:    j.Base,
			Rel:     j.Rel,
			Size:    j.Size,
			Status:  "pending",
			Results: []ResultItem{},
		}
		s.byPath[key] = item
		s.byID[item.ID] = item
		s.files = append(s.files, item)
		added = append(added, *item)
	}
	if len(added) > 0 && s.app != nil {
		s.app.Event.Emit("files:added", added)
	}
	return added
}

// RemoveFiles 按 ID 移除列表项。
func (s *AppService) RemoveFiles(ids []string) {
	if len(ids) == 0 {
		return
	}
	drop := make(map[string]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.files[:0]
	for _, it := range s.files {
		if drop[it.ID] {
			delete(s.byPath, strings.ToLower(it.Path))
			delete(s.byID, it.ID)
			continue
		}
		kept = append(kept, it)
	}
	s.files = kept
}

// ClearFiles 清空列表。
func (s *AppService) ClearFiles() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files = nil
	s.byPath = map[string]*FileItem{}
	s.byID = map[string]*FileItem{}
}

// ---------- 转换 ----------

// Start 校验参数后异步开始转换。
func (s *AppService) Start(cfg ConvertConfig) error {
	if s.eng == nil {
		return fmt.Errorf("引擎不可用：%s", s.engErr)
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("转换正在进行中")
	}
	items := append([]*FileItem(nil), s.files...)
	s.mu.Unlock()

	if len(items) == 0 {
		return fmt.Errorf("请先添加文件或文件夹")
	}

	f, ok := converter.FindFormat(cfg.Format)
	if !ok {
		return fmt.Errorf("未知输出格式 %q，可选: %s", cfg.Format, converter.FormatNames())
	}
	if cfg.Quality < 0 || cfg.Quality > 100 {
		return fmt.Errorf("质量必须在 0-100（0 表示使用格式默认档）")
	}

	switch cfg.ResizeMode {
	case "", "fw", "fh", "fl", "pct":
	default:
		return fmt.Errorf("未知输出尺寸模式 %q", cfg.ResizeMode)
	}
	if cfg.ResizeValue < 0 {
		return fmt.Errorf("输出尺寸值必须为正整数（固定档为像素、等比缩放为百分比）")
	}
	if cfg.ResizeMode == "" || cfg.ResizeValue == 0 {
		cfg.ResizeMode, cfg.ResizeValue = "", 0
	}

	// 重置每一行
	s.mu.Lock()
	for _, it := range items {
		it.Status = "pending"
		it.Results = []ResultItem{}
		it.OutSize, it.Ratio, it.Duration, it.Error = 0, 0, 0, ""
	}
	s.running = true
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelFn = cancel
	s.mu.Unlock()

	if cfg.WriteLog {
		if err := s.openLog(cfg.LogPath); err != nil {
			s.emitError("无法打开日志文件: " + err.Error())
		}
	}

	go s.run(ctx, items, f, cfg)
	return nil
}

// Cancel 请求取消当前批次。
func (s *AppService) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelFn != nil {
		s.cancelFn()
	}
}

// run 按单一目标格式执行一次批量转换。
func (s *AppService) run(ctx context.Context, items []*FileItem, f *converter.Format, cfg ConvertConfig) {
	started := time.Now()

	baseJobs := make([]converter.Job, 0, len(items))
	for _, it := range items {
		// 严格按源目录结构输出：Rel 原样透传，保留子目录层级，
		// 不同子目录中的同名文件因此落在不同输出路径，互不覆盖。
		baseJobs = append(baseJobs, converter.Job{JobID: it.ID, Path: it.Path, Base: it.Base, Rel: it.Rel, Size: it.Size})
	}

	// 源扩展名与目标格式相同（如 jpg→jpg、png→png）也不跳过：尺寸等
	// 参数可能不同，照样重新编码。输出到不同目录时直接生成；输出与源文件
	// 同路径时需用户开启“覆盖已存在文件”（由引擎按 Overwrite 判定）。
	total := len(baseJobs)

	// 质量由用户给定；有损/无损不做配置，取该格式的内置预设。
	opts := converter.Options{
		Quality:     cfg.Quality,
		Lossless:    f.DefaultLossless(),
		ResizeMode:  cfg.ResizeMode,
		ResizeValue: cfg.ResizeValue,
		Overwrite:   cfg.Overwrite,
		Workers:     cfg.Workers,
		OutDir:      cfg.OutDir,
	}

	stat := struct {
		sync.Mutex
		done, ok, skip, fail int
		inBytes, outBytes    int64
		canceled             bool
		// lastEmit 上次发出 progress 事件的时间。emit 回调会被多个 worker
		// 并发调用，必须与其余统计字段一样在锁内读写。
		lastEmit time.Time
	}{lastEmit: time.Now()}

	emit := func(r converter.Result) {
		s.applyResult(r)

		stat.Lock()
		stat.done++
		switch r.Status {
		case converter.StatusOK:
			stat.ok++
			stat.inBytes += r.InBytes
			stat.outBytes += r.OutBytes
		case converter.StatusSkip:
			stat.skip++
		default:
			stat.fail++
		}
		st := ProgressEvent{
			Running:   true,
			Done:      stat.done,
			Total:     total,
			Ok:        stat.ok,
			Skip:      stat.skip,
			Fail:      stat.fail,
			ElapsedMs: time.Since(started).Milliseconds(),
		}
		// 高频结果下做一次节流，避免事件刷屏（收尾一次必发）。
		emitProgress := time.Since(stat.lastEmit) >= 80*time.Millisecond || stat.done == total
		if emitProgress {
			stat.lastEmit = time.Now()
		}
		stat.Unlock()

		s.writeLog(r)
		s.app.Event.Emit("convert:result", ResultEvent{
			JobID:      r.JobID,
			Format:     r.Format,
			Status:     r.Status,
			Error:      r.Error,
			OutPath:    r.Dst,
			InBytes:    r.InBytes,
			OutBytes:   r.OutBytes,
			DurationMs: r.DurationMs,
			Done:       st.Done,
			Total:      total,
		})
		if emitProgress {
			s.app.Event.Emit("convert:progress", st)
		}
	}

	s.eng.Convert(ctx, baseJobs, []*converter.Format{f}, opts, emit)

	if ctx.Err() != nil {
		stat.Lock()
		stat.canceled = true
		stat.Unlock()
		s.markCanceled(items)
	}

	stat.Lock()
	summary := SummaryEvent{
		Ok:        stat.ok,
		Skip:      stat.skip,
		Fail:      stat.fail,
		InBytes:   stat.inBytes,
		OutBytes:  stat.outBytes,
		Ratio:     ratioPct(stat.inBytes, stat.outBytes),
		ElapsedMs: time.Since(started).Milliseconds(),
		Canceled:  stat.canceled,
	}
	stat.Unlock()

	s.mu.Lock()
	s.running = false
	s.cancelFn = nil
	s.mu.Unlock()
	s.closeLog()

	s.app.Event.Emit("convert:progress", ProgressEvent{Total: total, Done: summary.Ok + summary.Skip + summary.Fail})
	s.app.Event.Emit("convert:done", summary)
}

func (s *AppService) applyResult(r converter.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it := s.byID[r.JobID]
	if it == nil {
		return
	}
	it.Results = append(it.Results, ResultItem{
		Format:   r.Format,
		Status:   r.Status,
		OutPath:  r.Dst,
		OutSize:  r.OutBytes,
		Ratio:    ratioPct(r.InBytes, r.OutBytes),
		Duration: r.DurationMs,
		Error:    r.Error,
	})
	it.Duration += r.DurationMs
	it.OutSize += r.OutBytes
	it.Ratio = ratioPct(it.Size*int64(len(it.Results)), it.OutSize)
	switch {
	case r.Status == converter.StatusError:
		it.Status, it.Error = "error", r.Error
	case it.Status != "error":
		it.Status = r.Status
	}
}

func (s *AppService) markCanceled(items []*FileItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range items {
		if it.Status == "pending" {
			it.Status = "canceled"
		}
	}
}

// ---------- 设置 ----------

// GetSettings 返回当前高级设置。
func (s *AppService) GetSettings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.settings
}

// SaveSettings 保存高级设置并广播给所有窗口。
func (s *AppService) SaveSettings(st Settings) error {
	st.Normalize()
	s.mu.Lock()
	*s.settings = st
	s.mu.Unlock()
	if err := SaveSettings(st); err != nil {
		return err
	}
	s.app.Event.Emit("settings:changed", st)
	return nil
}

// ---------- 窗口 ----------

// WindowMinimise 最小化主窗口。
func (s *AppService) WindowMinimise() {
	if w, ok := s.app.Window.GetByName("main"); ok {
		w.Minimise()
	}
}

// WindowToggleMaximise 主窗口最大化/还原。
func (s *AppService) WindowToggleMaximise() {
	if w, ok := s.app.Window.GetByName("main"); ok {
		w.ToggleMaximise()
	}
}

// WindowClose 关闭主窗口。
func (s *AppService) WindowClose() {
	if w, ok := s.app.Window.GetByName("main"); ok {
		w.Close()
	}
}

// ---------- 系统 ----------

// ShowInFolder 在系统文件管理器中定位文件。
func (s *AppService) ShowInFolder(path string) {
	if path == "" {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", "/select,", filepath.Clean(path))
	case "darwin":
		cmd = exec.Command("open", "-R", path)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(path))
	}
	_ = cmd.Start()
}

// OpenPath 打开目录（不存在时尝试创建）。
func (s *AppService) OpenPath(path string) {
	if path == "" {
		return
	}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		path = filepath.Dir(path)
	}
	_ = os.MkdirAll(path, 0o755)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", filepath.Clean(path))
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	_ = cmd.Start()
}

// ---------- 内部辅助 ----------

func (s *AppService) emitError(msg string) {
	if s.app != nil {
		s.app.Event.Emit("convert:error", ErrorEvent{Message: msg})
	}
}

// imageFilterPattern 依据 vips 实际可用输入扩展名生成对话框文件过滤器。
func (s *AppService) imageFilterPattern() string {
	if s.vips == nil {
		return "*.jpg;*.jpeg;*.png;*.webp;*.avif;*.jxl;*.tif;*.tiff;*.gif;*.bmp;*.heic;*.heif"
	}
	exts := s.vips.InputExts()
	sort.Strings(exts)
	if len(exts) > 40 { // 部分平台对过滤器长度敏感，过长时退回常见格式
		exts = []string{"jpg", "jpeg", "png", "webp", "avif", "jxl", "tif", "tiff", "gif", "bmp", "heic", "heif"}
	}
	parts := make([]string, 0, len(exts))
	for _, e := range exts {
		parts = append(parts, "*."+e)
	}
	return strings.Join(parts, ";")
}

func (s *AppService) openLog(path string) error {
	if path == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			dir = "."
		}
		path = filepath.Join(dir, appName, "convert.log")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	s.logMu.Lock()
	s.logFile = f
	s.logMu.Unlock()
	return nil
}

func (s *AppService) closeLog() {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if s.logFile != nil {
		_ = s.logFile.Close()
		s.logFile = nil
	}
}

func (s *AppService) writeLog(r converter.Result) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if s.logFile == nil {
		return
	}
	kind := "成功"
	switch r.Status {
	case converter.StatusSkip:
		kind = "跳过"
	case converter.StatusError:
		kind = "失败"
	}
	msg := r.Error
	if _, err := fmt.Fprintf(s.logFile, "%s [%s] %-5s %s -> %s | %d -> %d B | %dms%s\n",
		time.Now().Format("2006-01-02 15:04:05"), kind, r.Format, r.Src, r.Dst,
		r.InBytes, r.OutBytes, r.DurationMs, prefix(msg)); err != nil {
		// 写日志失败（磁盘满等）：停用日志并提示一次，避免每个结果都刷错误
		_ = s.logFile.Close()
		s.logFile = nil
		s.emitError("写入转换日志失败，本批次后续日志已停用: " + err.Error())
	}
}

func prefix(s string) string {
	if s == "" {
		return ""
	}
	return " | " + s
}

// ratioPct 压缩率(%)：正值为压缩，负值表示体积变大。
func ratioPct(inBytes, outBytes int64) float64 {
	if inBytes <= 0 {
		return 0
	}
	return (1 - float64(outBytes)/float64(inBytes)) * 100
}
