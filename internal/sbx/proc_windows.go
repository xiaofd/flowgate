//go:build windows

package sbx

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// procHandle 持有 Windows Job Object，
// 保证守护进程退出（含被强杀）时核心子进程一并终止，避免 TUN 残留。
type procHandle struct {
	job windows.Handle
}

func newProc() *procHandle { return &procHandle{} }

func (h *procHandle) beforeStart(*exec.Cmd) {}

func (h *procHandle) afterStart(cmd *exec.Cmd) {
	// 释放上一次实例遗留的 Job Object，避免重启循环中句柄泄漏
	if h.job != 0 {
		_ = windows.CloseHandle(h.job)
		h.job = 0
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	if cmd.Process != nil {
		// 由 PID 打开子进程句柄（需 PROCESS_SET_QUOTA 才能加入 Job Object）
		ph, err := windows.OpenProcess(
			windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
			false, uint32(cmd.Process.Pid))
		if err == nil {
			if err := windows.AssignProcessToJobObject(job, ph); err == nil {
				_ = windows.CloseHandle(ph)
				h.job = job
			} else {
				_ = windows.CloseHandle(ph)
				_ = windows.CloseHandle(job)
			}
		} else {
			_ = windows.CloseHandle(job)
		}
	} else {
		_ = windows.CloseHandle(job)
	}
}

func (h *procHandle) onStop(cmd *exec.Cmd) {
	if cmd.Process != nil {
		// Windows 下进程终止即销毁 TUN 适配器与路由
		_ = cmd.Process.Kill()
	}
}

func (h *procHandle) onClose() {
	if h.job != 0 {
		_ = windows.CloseHandle(h.job)
		h.job = 0
	}
}

// pidAlive 判断指定 PID 的进程是否存活。
func pidAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}
