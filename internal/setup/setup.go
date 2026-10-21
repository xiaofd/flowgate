// Package setup 编排初始化流程：释放资产 → 准备 WARP 上游 → 生成并校验配置。
package setup

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"flowgate/internal/assets"
	"flowgate/internal/cfg"
	"flowgate/internal/parser"
	"flowgate/internal/sbx"
	"flowgate/internal/warp"
	"flowgate/internal/wclient"
)

// Printer 进度输出函数（CLI 传彩色输出，服务模式传日志）。
type Printer func(format string, v ...any)

// RuleMirrors 规则集在线镜像（优先使用高速国内镜像，原站作后备）。
var RuleMirrors = map[string][]string{
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

// DownloadRules 在线下载规则集到 dir。
func DownloadRules(dir string, print Printer) error {
	if print == nil {
		print = func(string, ...any) {}
	}
	client := &http.Client{Timeout: 15 * time.Second}
	for name, urls := range RuleMirrors {
		var lastErr error
		done := false
		print("下载规则集: %s ...", name)
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
		print("已更新 %s", name)
	}
	return nil
}

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
	case cfg.BackendCustom:
		if strings.TrimSpace(app.Warp.NodeURL) == "" {
			return fmt.Errorf("自定义节点后端模式下，尚未配置节点链接，请执行 flowgate node 或修改 config.json 设置 warp.node_url")
		}
		print("校验自定义节点链接 ...")
		if _, err := parser.ParseNodeURL(app.Warp.NodeURL, "warp"); err != nil {
			return fmt.Errorf("自定义节点链接无效: %w", err)
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
