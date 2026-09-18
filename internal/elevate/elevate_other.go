//go:build !windows

// Package elevate 处理权限提升：类 Unix 平台通过 sudo 重新拉起自身。
package elevate

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// IsAdmin 判断当前是否为 root。
func IsAdmin() bool {
	return os.Geteuid() == 0
}

// RelaunchElevatedAndWait 以 sudo 重新启动自身（携带给定参数）并等待其结束。
func RelaunchElevatedAndWait(args []string) error {
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return fmt.Errorf("需要 root 权限，请手动执行: sudo %s %s",
			os.Args[0], strings.Join(args, " "))
	}
	cmd := exec.Command(sudo, append([]string{os.Args[0]}, args...)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
