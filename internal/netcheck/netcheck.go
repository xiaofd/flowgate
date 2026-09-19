// Package netcheck 提供直连与 WARP 两条链路的连通性探测。
// 探测流量本身会经过 TUN 分流，因此结果反映真实分流效果。
package netcheck

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// Result 探测结果。
type Result struct {
	OK      bool
	Code    int
	Latency time.Duration
	Err     error
}

var client = &http.Client{
	Timeout: 8 * time.Second,
	// 显式禁用环境代理，确保流量走系统路由（即 TUN）
	Transport: &http.Transport{Proxy: nil},
}

func probe(url string) Result {
	start := time.Now()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return Result{Err: err}
	}
	req.Header.Set("User-Agent", "curl/8.5.0")
	resp, err := client.Do(req)
	if err != nil {
		return Result{Err: err}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	lat := time.Since(start).Round(time.Millisecond)
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return Result{OK: true, Code: resp.StatusCode, Latency: lat}
	}
	return Result{
		Code:    resp.StatusCode,
		Latency: lat,
		Err:     fmt.Errorf("HTTP %d", resp.StatusCode),
	}
}

// CheckDirect 国内直连链路探测（应始终可用）。
func CheckDirect() Result {
	return probe("http://connect.rom.miui.com/generate_204")
}

// CheckWarp GFW 名单链路探测（应经 WARP 转发后可用）。
func CheckWarp() Result {
	return probe("http://www.google.com/generate_204")
}

// CheckDev 关键开发资源链路探测（GitHub API，经分流规则走 WARP）。
func CheckDev() Result {
	return probe("https://api.github.com/zen")
}
