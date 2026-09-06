package main

import (
	"embed"
	"log"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const (
	appName    = "imgConvert"
	appVersion = "1.0.0"
)

// Wails uses Go's `embed` package to embed the frontend files into the binary.
// Any files in the frontend/dist folder will be embedded into the binary and
// made available to the frontend.
// See https://pkg.go.dev/embed for more information.

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	// 注册自定义事件，绑定生成器会据此产出带类型的 JS/TS API。
	application.RegisterEvent[ResultEvent]("convert:result")
	application.RegisterEvent[ProgressEvent]("convert:progress")
	application.RegisterEvent[SummaryEvent]("convert:done")
	application.RegisterEvent[ErrorEvent]("convert:error")
	application.RegisterEvent[Settings]("settings:changed")
	application.RegisterEvent[[]FileItem]("files:added")
	application.RegisterEvent[bool]("window:maximise")
}

// useCustomTitleBar 报告是否使用自绘标题栏（Windows 下走无边框窗口）。
func useCustomTitleBar() bool { return runtime.GOOS == "windows" }

func main() {
	svc := &AppService{}

	app := application.New(application.Options{
		Name:        appName,
		Description: "图片格式转换工具",
		Services: []application.Service{
			application.NewService(svc),
		},
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		OnShutdown: func() {
			_ = svc.ServiceShutdown()
		},
	})

	mainWindow := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "imgConvert — 图片格式转换",
		Width:            1024,
		Height:           768,
		MinWidth:         1024,
		MinHeight:        768,
		Frameless:        useCustomTitleBar(),
		EnableFileDrop:   true,
		BackgroundColour: application.NewRGB(15, 16, 21),
		URL:              "/",
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 46,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
	})
	mainWindow.Center()

	// 将系统的最大化/还原状态同步给前端，用于切换自绘标题栏按钮图标。
	// 覆盖点击按钮、双击标题栏、拖拽到屏幕顶部、Win+方向键等所有路径。
	setMaximised := func(maximised bool) {
		app.Event.Emit("window:maximise", maximised)
	}
	mainWindow.OnWindowEvent(events.Windows.WindowMaximise, func(e *application.WindowEvent) {
		setMaximised(true)
	})
	mainWindow.OnWindowEvent(events.Windows.WindowRestore, func(e *application.WindowEvent) {
		setMaximised(false)
	})
	mainWindow.OnWindowEvent(events.Windows.WindowUnMaximise, func(e *application.WindowEvent) {
		setMaximised(false)
	})

	// 拖放：直接交给服务展开为列表项（目录会递归扫描）。
	mainWindow.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		svc.AddPaths(e.Context().DroppedFiles())
	})

	err := app.Run()
	if err != nil {
		log.Fatal(err)
	}
}
