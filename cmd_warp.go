package main

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"flowgate/internal/cfg"
	"flowgate/internal/sbx"
	"flowgate/internal/term"
	"flowgate/internal/warp"
	"flowgate/internal/wclient"
)

type scanResult struct {
	endpoint string
	latency  time.Duration
	err      error
}

// warpScanCandidates 优先探测当前接入点与官方域名，再尝试注册接口返回的
// v4/v6 端点与官方端口组合（国内网络下 IPv6 常可绕过 UDP 封锁）。
func warpScanCandidates(current string, acc *warp.Account) []string {
	var list []string
	add := func(s string) {
		for _, e := range list {
			if e == s {
				return
			}
		}
		list = append(list, s)
	}
	add(current)
	add(warp.DefaultEndpoint)

	ports := acc.EndpointPorts
	if len(ports) == 0 {
		ports = []int{2408, 500, 1701, 4500}
	}
	// 注册接口返回的专属端点（v4 与 v6）
	if acc.EndpointV4 != "" {
		for _, p := range ports {
			add(fmt.Sprintf("%s:%d", acc.EndpointV4, p))
		}
	}
	if acc.EndpointV6 != "" {
		for _, p := range ports {
			add(fmt.Sprintf("[%s]:%d", acc.EndpointV6, p))
		}
	}
	// 社区发现的 WARP anycast 扩展端口（国内 ISP 常封锁 500/1701/2408/4500 等
	// VPN 特征端口，但 WARP 接受大量其他端口，可借此绕过）
	extendedPorts := []int{
		2408, 500, 1701, 4500, // 官方端口
		854, 859, 864, 878, 880, 890, 891, 894, 903, 908, 928, 934,
		939, 942, 943, 945, 946, 955, 968, 987, 988, 1002, 1010, 1014,
		1018, 1070, 1074, 1180, 1387, 1843, 2371, 2506, 3138, 3476, 3581,
		3854, 4177, 4198, 4233, 5279,
	}
	for _, ip := range []string{acc.EndpointV4, "162.159.192.1", "188.114.96.1", "188.114.97.1"} {
		if ip == "" {
			continue
		}
		for _, p := range extendedPorts {
			add(fmt.Sprintf("%s:%d", ip, p))
		}
	}
	// IPv6 端点（官方端口）
	if acc.EndpointV6 != "" {
		for _, p := range ports {
			add(fmt.Sprintf("[%s]:%d", acc.EndpointV6, p))
		}
	}
	return list
}

// cmdWarpScan 扫描可用的 WARP 接入点并择优写入配置（仅 WireGuard 后端）。
func cmdWarpScan(p cfg.Paths) error {
	app, err := cfg.LoadApp(p.AppConfig)
	if err != nil {
		return err
	}
	if app.Warp.Backend != cfg.BackendWireguard {
		term.Info("当前后端为官方客户端（MASQUE），接入点由客户端自动选择，无需扫描")
		term.Dim("如需改用内置 WireGuard 后端：编辑 %s 将 warp.backend 改为 wireguard 后执行 flowgate setup", p.AppConfig)
		return nil
	}
	acc, err := warp.Load(p.WarpAccount)
	if err != nil {
		return err
	}
	if acc == nil {
		return fmt.Errorf("尚未注册 WARP 账号，请先执行: flowgate setup")
	}
	if err := p.EnsureDirs(); err != nil {
		return err
	}

	candidates := warpScanCandidates(app.Warp.Endpoint, acc)
	term.Step("扫描 WARP 接入点（%d 个候选，并行探测）...", len(candidates))

	results := make([]scanResult, len(candidates))
	const workers = 8
	var wg sync.WaitGroup
	jobs := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				ep := candidates[i]
				// 每次探测向 OS 申请空闲端口，避免固定端口被占用导致误报
				apiPort := wclient.FreePort()
				if apiPort == 0 {
					apiPort = 19090 + i
				}
				lat, err := sbx.ProbeWarp(p, acc, ep, apiPort, 3500*time.Millisecond)
				results[i] = scanResult{endpoint: ep, latency: lat, err: err}
				if err == nil {
					term.Ok("%-32s %v", ep, lat)
				} else {
					term.Dim("%-32s 不可用（%v）", ep, err)
				}
			}
		}()
	}
	for i := range candidates {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	// 汇总：按延迟排序
	var ok []scanResult
	for _, r := range results {
		if r.err == nil {
			ok = append(ok, r)
		}
	}
	if len(ok) == 0 {
		term.Fail("所有候选接入点均不可用")
		term.Plain("")
		term.Warn("可能原因：本机网络封锁了对 Cloudflare WARP 的 UDP 出站，或运营商 QoS 限速")
		term.Warn("可尝试：更换网络环境，或修改 config.json 中 warp.endpoint 后重新 flowgate setup")
		return fmt.Errorf("无可用 WARP 接入点")
	}
	sort.Slice(ok, func(i, j int) bool { return ok[i].latency < ok[j].latency })

	term.Plain("")
	term.Step("可用接入点（按延迟排序）")
	for i, r := range ok {
		mark := ""
		if r.endpoint == app.Warp.Endpoint {
			mark = "（当前）"
		}
		term.Plain("  %d. %-32s %v%s", i+1, r.endpoint, r.latency, mark)
	}

	best := ok[0]
	if best.endpoint == app.Warp.Endpoint {
		term.Ok("当前接入点已是最优，无需修改")
		return nil
	}

	// 写入最优接入点并重新生成配置
	app.Warp.Endpoint = best.endpoint
	if err := cfg.SaveApp(p.AppConfig, app); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}
	if err := sbx.BuildConfig(p, app, acc); err != nil {
		return err
	}
	if err := sbx.CheckConfig(p); err != nil {
		return err
	}
	term.Ok("已切换接入点 → %s（延迟 %v）", best.endpoint, best.latency)
	term.Dim("如服务正在运行，请执行 flowgate restart 生效")
	return nil
}
