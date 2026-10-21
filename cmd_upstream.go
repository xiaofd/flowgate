package main

import (
	"fmt"
	"strings"

	"flowgate/internal/cfg"
	"flowgate/internal/parser"
	"flowgate/internal/sbx"
	"flowgate/internal/svc"
	"flowgate/internal/term"
	"flowgate/internal/warp"
)

// cmdUpstream 管理上游出站后端（WARP 客户端 / WireGuard / 自定义分享链接）。
func cmdUpstream(p cfg.Paths, args []string) error {
	if len(args) > 0 {
		return setCustomNode(p, args[0])
	}

	for {
		app, err := cfg.LoadApp(p.AppConfig)
		if err != nil {
			return err
		}

		term.Plain("")
		term.Step("上游出站后端管理 (Upstream Backend)")
		term.Dim("  当前后端: %s", backendText(app))
		if app.Warp.Backend == cfg.BackendCustom && app.Warp.NodeURL != "" {
			if parsed, perr := parser.ParseNodeURL(app.Warp.NodeURL, "warp"); perr == nil {
				proto := parsed.Outbound["type"]
				server := parsed.Outbound["server"]
				port := parsed.Outbound["server_port"]
				term.Dim("  当前节点: [%v] %v:%v", proto, server, port)
			}
		}
		term.Plain("  ─────────────────────────────────────────────")
		term.Plain("   1. 填入/更换自定义分享链接 (VLESS/Reality/Vision/HY2/TUIC/VMess/SS/Trojan/Socks5/HTTP)")
		term.Plain("   2. 切换为官方 WARP 客户端 (MASQUE 隧道，本地 SOCKS5)")
		term.Plain("   3. 切换为内置 WireGuard 后端 (直连 Cloudflare WARP)")
		term.Plain("   4. 查看当前节点配置详情")
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
			promptCustomNode(p)
		case "2":
			switchBackend(p, cfg.BackendClient)
		case "3":
			switchBackend(p, cfg.BackendWireguard)
		case "4":
			showNodeDetail(p)
		default:
			term.Warn("无效选项: %s", choice)
		}
	}
}

func promptCustomNode(p cfg.Paths) {
	term.Plain("")
	term.Info("支持协议: vless:// (含 Vision / Reality), hy2:// / hysteria2://, tuic://, vmess://, ss://, trojan://, socks5://, http://")
	term.Plain("请输入或粘贴节点分享链接: ")
	raw, ok := term.ReadLine()
	if !ok || strings.TrimSpace(raw) == "" {
		term.Warn("已取消输入")
		return
	}
	if err := setCustomNode(p, raw); err != nil {
		term.Fail("%v", err)
	}
}

func setCustomNode(p cfg.Paths, raw string) error {
	raw = strings.TrimSpace(raw)
	parsed, err := parser.ParseNodeURL(raw, "warp")
	if err != nil {
		return fmt.Errorf("节点链接解析失败: %w", err)
	}

	app, err := cfg.LoadApp(p.AppConfig)
	if err != nil {
		return err
	}

	app.Warp.Backend = cfg.BackendCustom
	app.Warp.NodeURL = raw
	if err := cfg.SaveApp(p.AppConfig, app); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}

	proto := parsed.Outbound["type"]
	server := parsed.Outbound["server"]
	port := parsed.Outbound["server_port"]
	term.Ok("已成功保存自定义节点: [%v] %v:%v", proto, server, port)

	if err := regenerateAndReload(p, app); err != nil {
		term.Warn("重载核心配置提示: %v", err)
	}
	return nil
}

func switchBackend(p cfg.Paths, targetBackend string) {
	app, err := cfg.LoadApp(p.AppConfig)
	if err != nil {
		term.Fail("读取配置失败: %v", err)
		return
	}

	if targetBackend == cfg.BackendCustom && app.Warp.NodeURL == "" {
		term.Warn("当前未配置自定义节点链接，请先选择选项 1 填入链接")
		return
	}

	app.Warp.Backend = targetBackend
	if err := cfg.SaveApp(p.AppConfig, app); err != nil {
		term.Fail("保存配置失败: %v", err)
		return
	}

	term.Ok("已切换上游后端为: %s", backendText(app))
	if err := regenerateAndReload(p, app); err != nil {
		term.Warn("重载核心配置提示: %v", err)
	}
}

func showNodeDetail(p cfg.Paths) {
	app, err := cfg.LoadApp(p.AppConfig)
	if err != nil {
		term.Fail("读取配置失败: %v", err)
		return
	}

	term.Plain("")
	term.Step("当前上游配置")
	term.KV("后端模式", app.Warp.Backend)
	if app.Warp.Backend == cfg.BackendCustom {
		term.KV("节点链接", app.Warp.NodeURL)
		if parsed, err := parser.ParseNodeURL(app.Warp.NodeURL, "warp"); err == nil {
			term.KV("协议类型", fmt.Sprintf("%v", parsed.Outbound["type"]))
			term.KV("服务器", fmt.Sprintf("%v", parsed.Outbound["server"]))
			term.KV("端口", fmt.Sprintf("%v", parsed.Outbound["server_port"]))
			if sec, ok := parsed.Outbound["security"]; ok {
				term.KV("安全层", fmt.Sprintf("%v", sec))
			}
			if trans, ok := parsed.Outbound["transport"]; ok {
				term.KV("传输层", fmt.Sprintf("%v", trans))
			}
		}
	} else if app.Warp.Backend == cfg.BackendWireguard {
		term.KV("接入点", app.Warp.Endpoint)
		term.KV("MTU", fmt.Sprintf("%d", app.Warp.MTU))
	} else {
		term.KV("本地SOCKS5代理", app.Warp.ClientProxy)
	}
	term.Plain("")
}

func regenerateAndReload(p cfg.Paths, app cfg.App) error {
	var acc *warp.Account
	if app.Warp.Backend == cfg.BackendWireguard {
		acc, _ = warp.Load(p.WarpAccount)
	}

	if err := sbx.BuildConfig(p, app, acc); err != nil {
		return fmt.Errorf("重新生成核心配置失败: %w", err)
	}
	if err := sbx.CheckConfig(p); err != nil {
		return fmt.Errorf("核心配置校验不通过: %w", err)
	}
	term.Ok("sing-box 分流配置已重新生成并校验通过")

	// 如果服务正在运行，执行热重启或重启服务
	s, _ := svc.New(flagHome)
	if st, serr := s.Status(); serr == nil && st == 1 { // 1 = running
		term.Step("检测到系统服务正在运行，正在重启服务以应用新配置 ...")
		if rerr := s.Restart(); rerr != nil {
			term.Warn("重启服务失败: %v，请手动执行 flowgate restart", rerr)
		} else {
			term.Ok("服务已重启生效")
		}
	} else if _, running := sbx.RunningPid(p.PidFile); running {
		term.Info("检测到前台核心正在运行，请重启前台进程以生效")
	}
	return nil
}
