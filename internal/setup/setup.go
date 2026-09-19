// Package setup 编排初始化流程：释放资产 → 准备 WARP 上游 → 生成并校验配置。
package setup

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"flowgate/internal/assets"
	"flowgate/internal/cfg"
	"flowgate/internal/sbx"
	"flowgate/internal/warp"
	"flowgate/internal/wclient"
)

// Printer 进度输出函数（CLI 传彩色输出，服务模式传日志）。
type Printer func(format string, v ...any)

// Ensure 执行完整初始化（幂等，可重复执行）。
func Ensure(p cfg.Paths, print Printer) error {
	if print == nil {
		print = func(string, ...any) {}
	}
	if err := p.EnsureDirs(); err != nil {
		return err
	}

	print("释放核心程序 fg-core ...")
	if _, err := assets.ExtractCore(p.CorePath, false); err != nil {
		return fmt.Errorf("释放核心失败: %w", err)
	}
	if runtime.GOOS == "windows" {
		if _, err := assets.ExtractWintun(p.WintunPath, false); err != nil {
			return fmt.Errorf("释放 wintun.dll 失败: %w", err)
		}
	}

	print("释放分流规则集（GFW 名单 / 国内名单）...")
	if _, err := assets.ExtractRules(p.RulesDir, false); err != nil {
		return fmt.Errorf("释放规则集失败: %w", err)
	}

	app, err := cfg.LoadApp(p.AppConfig)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(p.AppConfig); errors.Is(statErr, os.ErrNotExist) {
		if err := cfg.SaveApp(p.AppConfig, app); err != nil {
			return fmt.Errorf("写入默认配置失败: %w", err)
		}
	}

	backend := app.Warp.Backend
	if backend == "" {
		backend = cfg.BackendClient
	}

	var acc *warp.Account
	switch backend {
	case cfg.BackendWireguard:
		acc, err = ensureWireguard(p, app, print)
		if err != nil {
			return err
		}
	default:
		proxyAddr, cerr := ensureClient(app, print)
		if cerr != nil {
			return cerr
		}
		// 端口被占用时已自动更换，持久化到配置
		if proxyAddr != app.Warp.ClientProxy {
			app.Warp.ClientProxy = proxyAddr
			if err := cfg.SaveApp(p.AppConfig, app); err != nil {
				return fmt.Errorf("保存配置失败: %w", err)
			}
		}
	}

	print("生成 sing-box 分流配置 ...")
	if err := sbx.BuildConfig(p, app, acc); err != nil {
		return fmt.Errorf("生成配置失败: %w", err)
	}

	print("校验核心配置 ...")
	if err := sbx.CheckConfig(p); err != nil {
		return err
	}
	return nil
}

// ensureClient 确保官方 WARP 客户端（MASQUE）已安装、配置为代理模式并连接。
// 返回实际可用的本地代理地址（端口被占用时可能已自动更换为随机端口）。
func ensureClient(app cfg.App, print Printer) (string, error) {
	print("检查官方 WARP 客户端（1.1.1.1，MASQUE）...")
	cli, err := wclient.FindCLI()
	if err != nil {
		print("未检测到官方客户端，开始自动安装 ...")
		if err := wclient.Install(print); err != nil {
			return "", fmt.Errorf("安装官方 WARP 客户端失败: %w", err)
		}
		cli, err = wclient.FindCLI()
		if err != nil {
			return "", fmt.Errorf("安装后仍未找到 warp-cli: %w", err)
		}
	}
	print("配置官方客户端（注册 + 代理模式 + 端口 + 连接）...")
	var cfgErr error
	for i := 0; i < 3; i++ {
		cfgErr = wclient.Configure(cli, app.Warp.ClientProxy)
		if cfgErr == nil {
			break
		}
		// 刚安装完服务可能尚未就绪，稍等重试
		time.Sleep(3 * time.Second)
	}
	if cfgErr != nil {
		return "", cfgErr
	}

	// 校验本地代理（SOCKS5 握手），若目标端口被占或未就绪自动切换空闲端口
	print("校验本地代理（SOCKS5 握手）...")
	addr, err := wclient.EnsureProxy(cli, app.Warp.ClientProxy)
	if err != nil {
		return "", err
	}
	if addr != app.Warp.ClientProxy {
		print("端口 %s 不可用，已自动切换至 %s", app.Warp.ClientProxy, addr)
	}
	return addr, nil
}

// ensureWireguard 确保内置 WireGuard 后端的 WARP 账号就绪。
func ensureWireguard(p cfg.Paths, app cfg.App, print Printer) (*warp.Account, error) {
	print("检查 WARP 账号 ...")
	acc, err := warp.Load(p.WarpAccount)
	if err != nil {
		return nil, err
	}
	if acc == nil {
		print("注册新的免费 WARP 账号（1.1.1.1）...")
		var rerr error
		for i := 0; i < 3; i++ {
			acc, rerr = warp.Register(app.Warp.Endpoint)
			if rerr == nil {
				break
			}
			if i < 2 {
				time.Sleep(2 * time.Second)
			}
		}
		if rerr != nil {
			return nil, fmt.Errorf("WARP 注册失败: %w\n请确认本机可访问 api.cloudflareclient.com 后重试", rerr)
		}
		if err := warp.Save(p.WarpAccount, acc); err != nil {
			return nil, fmt.Errorf("保存 WARP 账号失败: %w", err)
		}
		print("WARP 账号注册成功（IPv4: %s）", acc.IPv4)
	}
	return acc, nil
}
