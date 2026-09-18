//go:build windows

package svc

import (
	"os/exec"
)

// PostInstall Windows 安装后追加崩溃自动重启恢复策略。
func PostInstall() error {
	return exec.Command("sc", "failure", Name,
		"reset=", "0",
		"actions=", "restart/5000/restart/5000/restart/5000").Run()
}
