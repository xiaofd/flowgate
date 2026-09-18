//go:build !windows

package svc

// PostInstall 非 Windows 平台无需额外处理（systemd 模板自带 Restart=always）。
func PostInstall() error { return nil }
