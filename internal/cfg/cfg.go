// Package cfg 负责 FlowGate 的目录布局与用户配置管理。
package cfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"flowgate/internal/assets"
	"flowgate/internal/warp"
)

// Paths FlowGate 的全部关键路径。
type Paths struct {
	Home        string // 用户指定的根目录（可为空）
	ConfigDir   string // 用户配置目录
	DataDir     string // 数据目录（核心程序、规则、生成的 sing-box 配置）
	LogDir      string // 日志目录
	RulesDir    string // 分流规则集目录
	BinDir      string // 核心程序目录
	CorePath    string // fg-core 可执行文件
	WintunPath  string // wintun.dll（仅 Windows）
	SBConfig    string // 生成的 sing-box 配置
	AppConfig   string // 用户配置 config.json
	WarpAccount string // WARP 账号 warp.json
	CoreLog     string // 核心日志
	SvcLog      string // 守护进程日志
	PidFile     string // 核心 PID 文件
}

// Resolve 解析目录布局。home 为空时依次尝试 FG_HOME 环境变量、系统默认位置。
func Resolve(home string) Paths {
	if home == "" {
		home = os.Getenv("FG_HOME")
	}
	var p Paths
	p.Home = home
	switch {
	case home != "":
		p.ConfigDir = home
		p.DataDir = filepath.Join(home, "data")
		p.LogDir = filepath.Join(home, "logs")
	case runtime.GOOS == "windows":
		base := os.Getenv("ProgramData")
		if base == "" {
			base = `C:\ProgramData`
		}
		p.ConfigDir = filepath.Join(base, "FlowGate")
		p.DataDir = p.ConfigDir
		p.LogDir = filepath.Join(p.ConfigDir, "logs")
	default:
		p.ConfigDir = "/etc/flowgate"
		p.DataDir = "/var/lib/flowgate"
		p.LogDir = "/var/log/flowgate"
	}
	p.RulesDir = filepath.Join(p.DataDir, "rules")
	p.BinDir = filepath.Join(p.DataDir, "bin")
	p.CorePath = filepath.Join(p.BinDir, assets.CoreName())
	p.WintunPath = filepath.Join(p.BinDir, "wintun.dll")
	p.SBConfig = filepath.Join(p.DataDir, "sing-box.json")
	p.AppConfig = filepath.Join(p.ConfigDir, "config.json")
	p.WarpAccount = filepath.Join(p.ConfigDir, "warp.json")
	p.CoreLog = filepath.Join(p.LogDir, "core.log")
	p.SvcLog = filepath.Join(p.LogDir, "flowgate.log")
	p.PidFile = filepath.Join(p.DataDir, "core.pid")
	return p
}

// EnsureDirs 创建全部所需目录。
func (p Paths) EnsureDirs() error {
	for _, d := range []string{p.ConfigDir, p.DataDir, p.RulesDir, p.BinDir, p.LogDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", d, err)
		}
	}
	return nil
}

// App 用户可调配置（config.json）。
type App struct {
	Warp struct {
		Backend     string `json:"backend"`      // "client"=官方客户端(MASQUE，默认) | "wireguard"=内置 WireGuard | "custom"=自定义分享链接节点
		NodeURL     string `json:"node_url,omitempty"` // 自定义节点分享链接（支持 vless://, vmess://, ss://, trojan://, socks5://, http://）
		ClientProxy string `json:"client_proxy"` // 官方客户端本地 SOCKS5 地址
		Endpoint    string `json:"endpoint"`     // WireGuard 后端的接入点
		MTU         int    `json:"mtu"`
	} `json:"warp"`
	Tun struct {
		Name        string   `json:"name"`
		MTU         int      `json:"mtu"`
		StrictRoute bool     `json:"strict_route"`
		Address     []string `json:"address,omitempty"` // TUN 虚拟网卡 IP（避开 Docker 172.17~31 网段）
	} `json:"tun"`
	DNS struct {
		Local  string `json:"local"`  // 国内直连 DNS
		Remote string `json:"remote"` // 经 WARP 的远程 DNS
	} `json:"dns"`
	Route struct {
		ProxyList     []string `json:"proxy_list,omitempty"`     // 自定义走 WARP 清单（支持 *.domain.com, domain.com, 1.2.3.4, 10.0.0.0/24）
		DirectList    []string `json:"direct_list,omitempty"`    // 自定义直连清单（同上）
		ProxyDomains  []string `json:"proxy_domains,omitempty"`  // 向下兼容旧配置
		DirectDomains []string `json:"direct_domains,omitempty"` // 向下兼容旧配置
	} `json:"route,omitempty"`
	AutoUpdate struct {
		Enabled  bool `json:"enabled"`   // 是否开启规则定时自动更新（默认 true）
		Interval int  `json:"interval"`  // 自动更新检查间隔（单位：小时，默认 24 小时）
	} `json:"auto_update"`
	LogLevel string `json:"log_level"`
}

// AllProxyList 返回合并去重后的走 WARP 自定义清单。
func (a App) AllProxyList() []string {
	seen := make(map[string]struct{})
	var out []string
	for _, item := range append(append([]string{}, a.Route.ProxyList...), a.Route.ProxyDomains...) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; !ok {
			seen[item] = struct{}{}
			out = append(out, item)
		}
	}
	return out
}

// AllDirectList 返回合并去重后的直连自定义清单。
func (a App) AllDirectList() []string {
	seen := make(map[string]struct{})
	var out []string
	for _, item := range append(append([]string{}, a.Route.DirectList...), a.Route.DirectDomains...) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; !ok {
			seen[item] = struct{}{}
			out = append(out, item)
		}
	}
	return out
}

// 后端类型常量。
const (
	BackendClient    = "client"
	BackendWireguard = "wireguard"
	BackendCustom    = "custom"
)

// DefaultApp 返回默认配置（针对中国内地网络环境优化）。
func DefaultApp() App {
	var a App
	a.Warp.Backend = BackendClient
	a.Warp.ClientProxy = "127.0.0.1:40000"
	a.Warp.Endpoint = warp.DefaultEndpoint
	a.Warp.MTU = 1280
	a.Tun.Name = "fgtun0"
	a.Tun.MTU = 9000
	// 默认使用 198.18.0.1/30（RFC 2544 / 5737），避开 Docker 默认 172.17~31 网段
	a.Tun.Address = []string{"198.18.0.1/30", "fdfe:dcba:9876::1/126"}
	// Windows 下 strict_route 会拦截核心与 WARP 客户端自身的 UDP 出站，默认关闭
	a.Tun.StrictRoute = runtime.GOOS != "windows"
	a.DNS.Local = "223.5.5.5"
	a.DNS.Remote = "1.1.1.1"
	a.AutoUpdate.Enabled = true
	a.AutoUpdate.Interval = 24
	a.LogLevel = "info"
	return a
}

// LoadApp 读取用户配置；文件不存在时返回默认配置且不报错。
// 容忍 UTF-8 BOM（如旧版记事本保存的文件）。
func LoadApp(path string) (App, error) {
	app := DefaultApp()
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return app, nil
		}
		return app, fmt.Errorf("读取配置失败: %w", err)
	}
	if err := json.Unmarshal(stripBOM(b), &app); err != nil {
		return app, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	// 为缺省字段补默认值
	d := DefaultApp()
	if app.Warp.Backend == "" {
		app.Warp.Backend = d.Warp.Backend
	}
	if app.Warp.ClientProxy == "" {
		app.Warp.ClientProxy = d.Warp.ClientProxy
	}
	if app.Warp.Endpoint == "" {
		app.Warp.Endpoint = d.Warp.Endpoint
	}
	if app.Warp.MTU <= 0 {
		app.Warp.MTU = d.Warp.MTU
	}
	if app.Tun.Name == "" {
		app.Tun.Name = d.Tun.Name
	}
	if app.Tun.MTU <= 0 {
		app.Tun.MTU = d.Tun.MTU
	}
	if len(app.Tun.Address) == 0 {
		app.Tun.Address = d.Tun.Address
	}
	if app.DNS.Local == "" {
		app.DNS.Local = d.DNS.Local
	}
	if app.DNS.Remote == "" {
		app.DNS.Remote = d.DNS.Remote
	}
	if app.AutoUpdate.Interval <= 0 {
		app.AutoUpdate.Interval = d.AutoUpdate.Interval
	}
	if app.LogLevel == "" {
		app.LogLevel = d.LogLevel
	}
	return app, nil
}

// SaveApp 写出用户配置。
func SaveApp(path string, app App) error {
	b, err := json.MarshalIndent(app, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// stripBOM 去除 UTF-8 BOM（EF BB BF）。
func stripBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}
