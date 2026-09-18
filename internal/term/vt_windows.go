//go:build windows

package term

import (
	"os"

	"golang.org/x/sys/windows"
)

func init() {
	// 控制台切换为 UTF-8，避免中文乱码
	_ = windows.SetConsoleOutputCP(65001)
	// 启用 VT 转义序列支持
	var mode uint32
	h := windows.Handle(os.Stdout.Fd())
	if err := windows.GetConsoleMode(h, &mode); err == nil {
		_ = windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
}
