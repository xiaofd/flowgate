package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"flowgate/internal/cfg"
	"flowgate/internal/sbx"
	"flowgate/internal/svc"
	"flowgate/internal/term"
	"flowgate/internal/warp"
)

// cmdRules 进入自定义分流清单交互管理模式。
func cmdRules(p cfg.Paths) error {
	for {
		term.Plain("")
		term.Step("自定义分流清单管理（最高优先级匹配）")
		term.Dim("  在此配置的域名与 IP 拥有绝对最高优先级，碾压一切内置规则集")
		term.Plain("  ─────────────────────────────────────────────")
		term.Plain("   1. 查看当前分流清单")
		term.Plain("   2. 添加到【走 WARP 清单】 (支持 *.domain.com, domain.com, 1.2.3.4, 10.0.0.0/24)")
		term.Plain("   3. 添加到【强制直连清单】 (支持 *.domain.com, domain.com, 1.2.3.4, 10.0.0.0/24)")
		term.Plain("   4. 删除规则（按序号）")
		term.Plain("   5. 清空指定清单")
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
			showCustomRules(p)
		case "2":
			addCustomRule(p, true)
		case "3":
			addCustomRule(p, false)
		case "4":
			deleteCustomRule(p)
		case "5":
			clearCustomRules(p)
		default:
			term.Warn("无效选项: %s", choice)
		}
		term.Plain("")
		term.Pause()
	}
}

func showCustomRules(p cfg.Paths) {
	app, err := cfg.LoadApp(p.AppConfig)
	if err != nil {
		term.Fail("读取配置失败: %v", err)
		return
	}
	proxies := app.AllProxyList()
	directs := app.AllDirectList()

	term.Plain("")
	term.Step("当前自定义分流清单")
	term.Plain("  【走 WARP 清单】(共 %d 条，防污染 + 经 WARP 转发):", len(proxies))
	if len(proxies) == 0 {
		term.Dim("    (空)")
	} else {
		for i, item := range proxies {
			term.Plain("    %2d. %s", i+1, item)
		}
	}

	term.Plain("")
	term.Plain("  【强制直连清单】(共 %d 条，国内直连 DNS + 直连出站):", len(directs))
	if len(directs) == 0 {
		term.Dim("    (空)")
	} else {
		for i, item := range directs {
			term.Plain("    %2d. %s", i+1, item)
		}
	}
}

func addCustomRule(p cfg.Paths, isProxy bool) {
	targetName := "【走 WARP 清单】"
	if !isProxy {
		targetName = "【强制直连清单】"
	}

	term.Plain("")
	term.Plain("请输入要加入 %s 的域名或 IP：", targetName)
	term.Dim("支持格式: *.domain.com, domain.com, 1.2.3.4, 10.0.0.0/24 (可直接粘贴完整 URL)")
	term.Plain("> ")

	input, ok := term.ReadLine()
	if !ok || strings.TrimSpace(input) == "" {
		term.Warn("输入为空，已取消操作")
		return
	}

	cleaned, err := cleanRuleInput(input)
	if err != nil {
		term.Fail("输入不合法: %v", err)
		return
	}

	app, err := cfg.LoadApp(p.AppConfig)
	if err != nil {
		term.Fail("读取配置失败: %v", err)
		return
	}

	if isProxy {
		for _, existing := range app.AllProxyList() {
			if existing == cleaned {
				term.Warn("该规则已存在于走 WARP 清单: %s", cleaned)
				return
			}
		}
		app.Route.ProxyList = append(app.Route.ProxyList, cleaned)
	} else {
		for _, existing := range app.AllDirectList() {
			if existing == cleaned {
				term.Warn("该规则已存在于直连清单: %s", cleaned)
				return
			}
		}
		app.Route.DirectList = append(app.Route.DirectList, cleaned)
	}

	if err := saveAndApplyConfig(p, app); err != nil {
		term.Fail("%v", err)
		return
	}

	term.Ok("已成功将 %s 加入 %s 并更新分流配置", cleaned, targetName)
	checkAndPromptRestart(p)
}

func deleteCustomRule(p cfg.Paths) {
	app, err := cfg.LoadApp(p.AppConfig)
	if err != nil {
		term.Fail("读取配置失败: %v", err)
		return
	}
	proxies := app.AllProxyList()
	directs := app.AllDirectList()
	total := len(proxies) + len(directs)
	if total == 0 {
		term.Info("当前清单为空，无需删除")
		return
	}

	term.Plain("")
	term.Step("请选择要删除的规则序号：")
	idx := 1
	var mapping []ruleRef
	for _, it := range proxies {
		term.Plain("  [%2d] (走 WARP) %s", idx, it)
		mapping = append(mapping, ruleRef{isProxy: true, value: it})
		idx++
	}
	for _, it := range directs {
		term.Plain("  [%2d] (直连)    %s", idx, it)
		mapping = append(mapping, ruleRef{isProxy: false, value: it})
		idx++
	}

	term.Plain("")
	term.Plain("请输入要删除的序号 (输入 0 取消): ")
	selStr, ok := term.ReadLine()
	if !ok || selStr == "0" || selStr == "" {
		return
	}
	sel, err := strconv.Atoi(selStr)
	if err != nil || sel < 1 || sel > len(mapping) {
		term.Warn("无效序号: %s", selStr)
		return
	}

	chosen := mapping[sel-1]
	if chosen.isProxy {
		app.Route.ProxyList = removeItem(app.Route.ProxyList, chosen.value)
		app.Route.ProxyDomains = removeItem(app.Route.ProxyDomains, chosen.value)
	} else {
		app.Route.DirectList = removeItem(app.Route.DirectList, chosen.value)
		app.Route.DirectDomains = removeItem(app.Route.DirectDomains, chosen.value)
	}

	if err := saveAndApplyConfig(p, app); err != nil {
		term.Fail("%v", err)
		return
	}

	term.Ok("已成功删除规则: %s", chosen.value)
	checkAndPromptRestart(p)
}

func clearCustomRules(p cfg.Paths) {
	term.Plain("")
	term.Plain("请选择要清空的清单:")
	term.Plain("  1. 仅清空【走 WARP 清单】")
	term.Plain("  2. 仅清空【强制直连清单】")
	term.Plain("  3. 清空全部自定义清单")
	term.Plain("  0. 取消")
	term.Plain("请输入编号: ")

	choice, ok := term.ReadLine()
	if !ok || choice == "0" || choice == "" {
		return
	}

	app, err := cfg.LoadApp(p.AppConfig)
	if err != nil {
		term.Fail("读取配置失败: %v", err)
		return
	}

	switch choice {
	case "1":
		if term.Confirm("确认清空走 WARP 清单") {
			app.Route.ProxyList = nil
			app.Route.ProxyDomains = nil
		} else {
			return
		}
	case "2":
		if term.Confirm("确认清空直连清单") {
			app.Route.DirectList = nil
			app.Route.DirectDomains = nil
		} else {
			return
		}
	case "3":
		if term.Confirm("确认清空全部自定义规则清单") {
			app.Route.ProxyList = nil
			app.Route.ProxyDomains = nil
			app.Route.DirectList = nil
			app.Route.DirectDomains = nil
		} else {
			return
		}
	default:
		term.Warn("无效选项")
		return
	}

	if err := saveAndApplyConfig(p, app); err != nil {
		term.Fail("%v", err)
		return
	}
	term.Ok("清单已清空并重新生成分流配置")
	checkAndPromptRestart(p)
}

type ruleRef struct {
	isProxy bool
	value   string
}

func removeItem(list []string, item string) []string {
	var out []string
	for _, s := range list {
		if s != item {
			out = append(out, s)
		}
	}
	return out
}

func cleanRuleInput(s string) (string, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return "", fmt.Errorf("内容不能为空")
	}
	// 自动剥离 URL 前缀
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// 截断路径 (如 example.com/abc -> example.com)，排除 CIDR
	if _, _, err := net.ParseCIDR(s); err != nil {
		if i := strings.Index(s, "/"); i >= 0 {
			s = s[:i]
		}
	}
	// 剥离端口
	if strings.Contains(s, ":") && !strings.Contains(s, "::") {
		if h, _, err := net.SplitHostPort(s); err == nil {
			s = h
		}
	}

	// 环回与保留 IP 校验（防止死循环）
	if ip := net.ParseIP(s); ip != nil {
		if ip.IsLoopback() {
			return "", fmt.Errorf("不可添加本地环回地址 (%s)", s)
		}
	}
	if _, ipNet, err := net.ParseCIDR(s); err == nil {
		if ipNet.IP.IsLoopback() {
			return "", fmt.Errorf("不可添加本地环回网段 (%s)", s)
		}
	}

	return s, nil
}

func saveAndApplyConfig(p cfg.Paths, app cfg.App) error {
	if err := cfg.SaveApp(p.AppConfig, app); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}
	var acc *warp.Account
	if app.Warp.Backend == cfg.BackendWireguard {
		acc, _ = warp.Load(p.WarpAccount)
	}
	if err := sbx.BuildConfig(p, app, acc); err != nil {
		return fmt.Errorf("生成核心配置失败: %w", err)
	}
	if err := sbx.CheckConfig(p); err != nil {
		return fmt.Errorf("配置校验失败: %w", err)
	}
	return nil
}

func checkAndPromptRestart(p cfg.Paths) {
	_, running := sbx.RunningPid(p.PidFile)
	if !running {
		s, _ := svc.New(flagHome)
		st, _ := s.Status()
		running = (st == 1) // running
	}
	if running {
		term.Plain("")
		if term.Confirm("检测到服务/核心正在运行，是否立即重启以使新规则生效") {
			term.Step("正在重启服务 ...")
			if err := cmdServiceCtl(p, "restart"); err != nil {
				term.Warn("重启服务失败: %v，请手动执行 flowgate restart", err)
			}
		} else {
			term.Dim("提示: 可在稍后执行 flowgate restart 使新规则生效")
		}
	}
}
