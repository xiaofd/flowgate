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
	resp, err := client.Get(url)
	if err != nil {
		return Result{Err: err}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	lat := time.Since(start).Round(time.Millisecond)
	if resp.StatusCode == http.StatusNoContent {
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
