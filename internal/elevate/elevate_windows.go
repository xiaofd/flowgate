//go:build windows

// Package elevate 处理权限提升：Windows 通过 UAC 重新拉起自身。
package elevate

import (
	"errors"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// IsAdmin 判断当前进程是否具有管理员权限。
func IsAdmin() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      uintptr
	hkeyClass    uintptr
	dwHotKey     uint32
	hIconMonitor uintptr
	hProcess     uintptr
}

var shellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// RelaunchElevatedAndWait 以管理员身份重新启动自身（携带给定参数）并等待其结束。
func RelaunchElevatedAndWait(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	const seeMaskNocloseProcess = 0x40
	const swShow = 1

	info := shellExecuteInfo{
		cbSize:       uint32(unsafe.Sizeof(shellExecuteInfo{})),
		fMask:        seeMaskNocloseProcess,
		lpVerb:       windows.StringToUTF16Ptr("runas"),
		lpFile:       windows.StringToUTF16Ptr(exe),
		lpParameters: windows.StringToUTF16Ptr(joinArgs(args)),
		lpDirectory:  windows.StringToUTF16Ptr(cwd),
		nShow:        swShow,
	}
	ret, _, _ := shellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
	if ret == 0 {
		return errors.New("提权失败或被用户取消，请右键以管理员身份运行")
	}
	if info.hProcess != 0 {
		_, _ = windows.WaitForSingleObject(windows.Handle(info.hProcess), windows.INFINITE)
		_ = windows.CloseHandle(windows.Handle(info.hProcess))
	}
	return nil
}

// joinArgs 拼接命令行参数，含空白的参数加引号。
func joinArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t\"") {
			out[i] = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		} else {
			out[i] = a
		}
	}
	return strings.Join(out, " ")
}
