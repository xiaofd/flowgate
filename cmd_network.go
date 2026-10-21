package main

import (
	"strings"

	"flowgate/internal/cfg"
	"flowgate/internal/term"
)

// cmdNetwork 网络栈与 DNS 策略管理（IPv6 开关、DNS 策略、本地/远程 DNS）。
func cmdNetwork(p cfg.Paths) error {
	for {
		app, err := cfg.LoadApp(p.AppConfig)
		if err != nil {
			return err
		}

		term.Plain("")
		term.Step("网络与 DNS 配置 (Network & DNS)")
		term.Dim("  当前 IPv6 出栈: %s", ipv6StatusText(app.Tun.IPv6))
		term.Dim("  当前 DNS 解析策略: %s", dnsStrategyText(app.DNS.Strategy, app.Tun.IPv6))
		term.Dim("  国内直连 DNS: %s", app.DNS.Local)
		term.Dim("  分流远程 DNS: %s", app.DNS.Remote)
		term.Plain("  ─────────────────────────────────────────────")
		term.Plain("   1. 切换 IPv6 出栈状态 (开启 / 关闭)")
		term.Plain("   2. 切换 DNS 域名解析策略 (ipv4_only / prefer_ipv4 / prefer_ipv6 / ipv6_only)")
		term.Plain("   3. 修改国内直连 DNS 服务器")
		term.Plain("   4. 修改分流远程 DNS 服务器")
		term.Plain("   0. 返回上级菜单")
		term.Plain("  ─────────────────────────────────────────────")
		term.Plain("")
		term.Plain("  请输入编号并回车: ")

		choice, ok := term.ReadLine()
		if !ok || choice == "0" || choice == "q" || choice == "Q" {
			return nil
		}

		switch choice {
		case "1":
			toggleIPv6(p, &app)
		case "2":
			selectDNSStrategy(p, &app)
		case "3":
			modifyLocalDNS(p, &app)
		case "4":
			modifyRemoteDNS(p, &app)
		default:
			term.Warn("无效选项: %s", choice)
		}
	}
}

func ipv6StatusText(enabled bool) string {
	if enabled {
		return "已开启 (双栈虚拟网卡，支持 IPv6 分流)"
	}
	return "已关闭 (纯 IPv4 模式，防止无公共 IPv6 时直连超时)"
}

func dnsStrategyText(strategy string, ipv6 bool) string {
	if strategy == "" {
		if ipv6 {
			return "prefer_ipv4 (默认自适应)"
		}
		return "ipv4_only (默认自适应)"
	}
	switch strategy {
	case "ipv4_only":
		return "ipv4_only (仅解析 IPv4 A 记录)"
	case "prefer_ipv4":
		return "prefer_ipv4 (优先使用 IPv4，兼顾 IPv6)"
	case "prefer_ipv6":
		return "prefer_ipv6 (优先使用 IPv6，兼顾 IPv4)"
	case "ipv6_only":
		return "ipv6_only (仅解析 IPv6 AAAA 记录)"
	default:
		return strategy
	}
}

func toggleIPv6(p cfg.Paths, app *cfg.App) {
	app.Tun.IPv6 = !app.Tun.IPv6
	if app.Tun.IPv6 {
		// 开启 IPv6 时，若 DNS 策略还是 ipv4_only，自动优化为 prefer_ipv4 兼顾双栈
		if app.DNS.Strategy == "ipv4_only" || app.DNS.Strategy == "" {
			app.DNS.Strategy = "prefer_ipv4"
		}
		term.Ok("已开启 IPv6 出栈（已自动将 DNS 策略调整为 prefer_ipv4 兼顾解析）")
	} else {
		// 关闭 IPv6 时，若 DNS 策略是 prefer_ipv6/ipv6_only，自动降级为 ipv4_only
		if app.DNS.Strategy == "prefer_ipv6" || app.DNS.Strategy == "ipv6_only" {
			app.DNS.Strategy = "ipv4_only"
		}
		term.Ok("已关闭 IPv6 出栈（运行于纯 IPv4 稳定模式）")
	}

	if err := cfg.SaveApp(p.AppConfig, *app); err != nil {
		term.Fail("保存配置失败: %v", err)
		return
	}
	if err := regenerateAndReload(p, *app); err != nil {
		term.Warn("重载核心配置提示: %v", err)
	}
}

func selectDNSStrategy(p cfg.Paths, app *cfg.App) {
	term.Plain("")
	term.Step("选择 DNS 域名解析策略")
	term.Plain("  1. ipv4_only   - 仅解析 IPv4（推荐纯 IPv4 环境，彻底避免 IPv6 路由黑洞）")
	term.Plain("  2. prefer_ipv4  - 优先 IPv4（推荐双栈环境，解析双栈地址但优先连接 IPv4）")
	term.Plain("  3. prefer_ipv6  - 优先 IPv6（推荐公网 IPv6 优质环境，优先连接 IPv6）")
	term.Plain("  4. ipv6_only   - 仅解析 IPv6（仅用于特殊纯 IPv6 网络）")
	term.Plain("  0. 取消")
	term.Plain("请输入编号: ")

	choice, ok := term.ReadLine()
	if !ok || choice == "0" {
		return
	}

	var newStrategy string
	switch choice {
	case "1":
		newStrategy = "ipv4_only"
	case "2":
		newStrategy = "prefer_ipv4"
	case "3":
		newStrategy = "prefer_ipv6"
	case "4":
		newStrategy = "ipv6_only"
	default:
		term.Warn("无效编号，已取消")
		return
	}

	if (newStrategy == "prefer_ipv6" || newStrategy == "ipv6_only") && !app.Tun.IPv6 {
		term.Warn("当前未开启 IPv6 出栈（tun.ipv6: false），建议先开启 IPv6 再设置此策略")
	}

	app.DNS.Strategy = newStrategy
	if err := cfg.SaveApp(p.AppConfig, *app); err != nil {
		term.Fail("保存配置失败: %v", err)
		return
	}
	term.Ok("已更新 DNS 解析策略为: %s", newStrategy)
	if err := regenerateAndReload(p, *app); err != nil {
		term.Warn("重载核心配置提示: %v", err)
	}
}

func modifyLocalDNS(p cfg.Paths, app *cfg.App) {
	term.Plain("")
	term.Plain("请输入新的国内直连 DNS 服务器 IP（当前: %s，回车保持不变）: ", app.DNS.Local)
	input, ok := term.ReadLine()
	if !ok || strings.TrimSpace(input) == "" {
		return
	}
	app.DNS.Local = strings.TrimSpace(input)
	if err := cfg.SaveApp(p.AppConfig, *app); err != nil {
		term.Fail("保存配置失败: %v", err)
		return
	}
	term.Ok("国内直连 DNS 已修改为: %s", app.DNS.Local)
	if err := regenerateAndReload(p, *app); err != nil {
		term.Warn("重载核心配置提示: %v", err)
	}
}

func modifyRemoteDNS(p cfg.Paths, app *cfg.App) {
	term.Plain("")
	term.Plain("请输入新的分流远程 DNS 服务器 IP（当前: %s，回车保持不变）: ", app.DNS.Remote)
	input, ok := term.ReadLine()
	if !ok || strings.TrimSpace(input) == "" {
		return
	}
	app.DNS.Remote = strings.TrimSpace(input)
	if err := cfg.SaveApp(p.AppConfig, *app); err != nil {
		term.Fail("保存配置失败: %v", err)
		return
	}
	term.Ok("分流远程 DNS 已修改为: %s", app.DNS.Remote)
	if err := regenerateAndReload(p, *app); err != nil {
		term.Warn("重载核心配置提示: %v", err)
	}
}
