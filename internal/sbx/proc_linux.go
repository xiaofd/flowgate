//go:build linux

package sbx

import (
	"os/exec"
	"syscall"
	"time"
)

// procHandle Linux 平台通过 Pdeathsig 保证守护进程死亡时核心收到 SIGTERM，
// 让 sing-box 有机会清理 auto_route 路由规则。
type procHandle struct{}

func newProc() *procHandle { return &procHandle{} }

func (h *procHandle) beforeStart(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGTERM,
		Setpgid:   true,
	}
}

func (h *procHandle) afterStart(*exec.Cmd) {}

func (h *procHandle) onStop(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// 先 SIGTERM 让 sing-box 清理路由，超时再强杀
	_ = cmd.Process.Signal(syscall.SIGTERM)
	go func() {
		time.Sleep(10 * time.Second)
		_ = cmd.Process.Kill()
	}()
}

func (h *procHandle) onClose() {}

// pidAlive 判断指定 PID 的进程是否存活。
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
