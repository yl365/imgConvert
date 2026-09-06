//go:build windows

package converter

import (
	"os/exec"
	"syscall"
)

// createNoWindow 等价于 CREATE_NO_WINDOW（0x08000000），让子进程不创建控制台窗口。
const createNoWindow = 0x08000000

// newCmd 创建子进程命令。vips.exe / vipsheader.exe 是控制台程序，
// 直接启动会弹出黑色命令行窗口；Windows 下用 CREATE_NO_WINDOW
// 让其在后台运行（输出仍通过管道捕获）。
func newCmd(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	return cmd
}
