//go:build !windows

package converter

import "os/exec"

// newCmd 创建子进程命令。非 Windows 平台无控制台窗口问题。
func newCmd(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}
