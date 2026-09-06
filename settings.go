package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"imgConvert/converter"
)

// defaultFormat 默认输出格式。
const defaultFormat = "avif"

// Settings 高级设置。
type Settings struct {
	// Format 输出格式名（avif/webp/jpeg/jxl/png），单选。
	Format string `json:"format"`
	// Quality 等效 JPEG 质量 1-100。
	Quality int `json:"quality"`
	// ResizeMode 输出尺寸模式："" 原尺寸 / "fw" 固定宽 / "fh" 固定高 /
	// "fl" 固定长边（均可放大缩小，等比缩放）/ "pct" 按百分比等比缩放。
	ResizeMode string `json:"resizeMode"`
	// ResizeValue 限制/固定边的像素值。
	ResizeValue int `json:"resizeValue"`
	// Overwrite 覆盖已存在的输出文件。
	Overwrite bool `json:"overwrite"`
	// Workers 并发 vips 进程数，0 表示 CPU 核数。
	Workers int `json:"workers"`
	// OutputMode "source" 输出到原目录；"custom" 输出到 CustomDir。
	OutputMode string `json:"outputMode"`
	// CustomDir 自定义输出根目录。
	CustomDir string `json:"customDir"`
	// WriteLog 是否写入转换日志。
	WriteLog bool `json:"writeLog"`
	// LogPath 日志文件路径，空表示使用配置目录下的 convert.log。
	LogPath string `json:"logPath"`
}

// DefaultSettings 返回默认设置。
func DefaultSettings() Settings {
	return Settings{
		Format:            defaultFormat,
		Quality:           80,
		ResizeMode:        "",
		ResizeValue:       0,
		Overwrite:         false,
		Workers:           0,
		OutputMode:        "source",
		CustomDir:         "",
		WriteLog:          false,
		LogPath:           "",
	}
}

// Normalize 修正越界值与未知格式，保证设置总是可用的。
func (s *Settings) Normalize() {
	if f, ok := converter.FindFormat(s.Format); ok {
		s.Format = f.Name
	} else {
		s.Format = defaultFormat
	}
	if s.Quality < 1 {
		s.Quality = 1
	}
	if s.Quality > 100 {
		s.Quality = 100
	}
	switch s.ResizeMode {
	case "", "fw", "fh", "fl", "pct":
	default:
		// 旧版 "w"/"l"（宽 ≤ / 长边 ≤，仅缩小）与新版档位语义不同
		// （新档可放大），无法安全迁移，归为原尺寸，避免小图被意外放大。
		s.ResizeMode = ""
	}
	if s.ResizeValue < 0 {
		s.ResizeValue = 0
	}
	if s.ResizeValue > 100000 {
		s.ResizeValue = 100000
	}
	if s.ResizeMode == "" {
		s.ResizeValue = 0
	}
	if s.Workers < 0 {
		s.Workers = 0
	}
	if s.Workers > 256 {
		s.Workers = 256
	}
	if s.OutputMode != "custom" {
		s.OutputMode = "source"
	}
	s.CustomDir = strings.TrimSpace(s.CustomDir)
}

// OutDir 返回实际输出根目录：原目录模式返回空串。
func (s Settings) OutDir() string {
	if s.OutputMode != "custom" || s.CustomDir == "" {
		return ""
	}
	return s.CustomDir
}

// settingsPath 返回设置文件路径。
func settingsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, appName, "settings.json")
}

// LoadSettings 读取设置，缺失或损坏时回退到默认设置。
func LoadSettings() *Settings {
	def := DefaultSettings()
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		return &def
	}
	var st Settings
	if err := json.Unmarshal(data, &st); err != nil {
		return &def
	}
	st.Normalize()
	return &st
}

// SaveSettings 写入设置文件。
func SaveSettings(st Settings) error {
	path := settingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
