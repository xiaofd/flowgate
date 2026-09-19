package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

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

// ruleMirrors 规则集在线镜像（优先使用高速国内镜像，原站作后备）。
var ruleMirrors = map[string][]string{
	"geosite-gfw.srs": {
		"https://gh-proxy.com/https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/gfw.srs",
		"https://mirror.ghproxy.com/https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/gfw.srs",
		"https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/gfw.srs",
	},
	"geosite-cn.srs": {
		"https://gh-proxy.com/https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/cn.srs",
		"https://mirror.ghproxy.com/https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/cn.srs",
		"https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/cn.srs",
	},
	"geoip-cn.srs": {
		"https://gh-proxy.com/https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geoip/cn.srs",
		"https://mirror.ghproxy.com/https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geoip/cn.srs",
		"https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geoip/cn.srs",
	},
}

// downloadRules 在线下载规则集到 dir。
func downloadRules(dir string) error {
	client := &http.Client{Timeout: 15 * time.Second}
	for name, urls := range ruleMirrors {
		var lastErr error
		done := false
		term.Step("下载规则集: %s ...", name)
		for _, u := range urls {
			req, rerr := http.NewRequest(http.MethodGet, u, nil)
			if rerr != nil {
				lastErr = rerr
				continue
			}
			req.Header.Set("User-Agent", "curl/8.5.0")
			resp, err := client.Do(req)
			if err != nil {
				lastErr = err
				continue
			}
			if resp.StatusCode != http.StatusOK {
				lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
				resp.Body.Close()
				continue
			}
			f, err := os.Create(filepath.Join(dir, name))
			if err != nil {
				resp.Body.Close()
				return err
			}
			_, err = io.Copy(f, resp.Body)
			f.Close()
			resp.Body.Close()
			if err != nil {
				lastErr = err
				continue
			}
			done = true
			break
		}
		if !done {
			return fmt.Errorf("下载 %s 失败: %w", name, lastErr)
		}
		term.Ok("已更新 %s", name)
	}
	return nil
}
