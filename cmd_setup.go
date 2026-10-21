package main

import (
	"io"
	"log"
	"os"

	"flowgate/internal/cfg"
	"flowgate/internal/setup"
	"flowgate/internal/term"
)

// setupEnsure 以带进度输出的方式执行初始化。
func setupEnsure(p cfg.Paths) error {
	return setup.Ensure(p, func(format string, v ...any) {
		term.Step(format, v...)
	})
}

// newForegroundLogger 前台运行时的守护日志：控制台 + 文件双写。
func newForegroundLogger(p cfg.Paths) *log.Logger {
	_ = os.MkdirAll(p.LogDir, 0o755)
	f, err := os.OpenFile(p.SvcLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return log.New(os.Stderr, "", log.LstdFlags)
	}
	return log.New(io.MultiWriter(os.Stderr, f), "", log.LstdFlags)
}

// downloadRules 在线下载规则集到 dir。
func downloadRules(dir string) error {
	return setup.DownloadRules(dir, func(format string, v ...any) {
		term.Step(format, v...)
	})
}
