// Package sbx 负责生成 sing-box（fg-core）配置并守护其进程。
package sbx

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"flowgate/internal/cfg"
	"flowgate/internal/warp"
)

// 分流策略（sing-box 1.14 配置格式）：
//   - GFW 名单域名（geosite-gfw）→ WARP WireGuard endpoint 转发，DNS 亦经 WARP 防污染
//   - 国内域名/IP（geosite-cn / geoip-cn）、局域网 → 直连
//   - 其余流量（P2P、普通浏览等）默认直连，不经 WARP
type sbConfig struct {
	Log       sbLog                 `json:"log"`
	DNS       sbDNS                 `json:"dns"`
	Endpoints []sbWireguardEndpoint `json:"endpoints,omitempty"`
	Inbounds  []sbTun               `json:"inbounds"`
	Outbounds []any                  `json:"outbounds"`
	Route     sbRoute                `json:"route"`
}

type sbLog struct {
	Level     string `json:"level"`
	Timestamp bool   `json:"timestamp"`
}

type sbDNS struct {
	Servers  []sbDNSServer `json:"servers"`
	Rules    []sbDNSRule   `json:"rules"`
	Final    string        `json:"final"`
	Strategy string        `json:"strategy"`
}

type sbDNSServer struct {
	Type   string `json:"type"`
	Tag    string `json:"tag"`
	Server string `json:"server,omitempty"`
	Detour string `json:"detour,omitempty"`
}

type sbDNSRule struct {
	RuleSet []string `json:"rule_set,omitempty"`
	Server  string   `json:"server"`
}

type sbTun struct {
	Type          string   `json:"type"`
	Tag           string   `json:"tag"`
	InterfaceName string   `json:"interface_name"`
	Address       []string `json:"address"`
	MTU           int      `json:"mtu"`
	Stack         string   `json:"stack"`
	AutoRoute     bool     `json:"auto_route"`
	StrictRoute   bool     `json:"strict_route"`
}

// sbWireguardEndpoint WARP 的 WireGuard 接入（sing-box 1.11+ endpoint 结构）。
type sbWireguardEndpoint struct {
	Type           string     `json:"type"`
	Tag            string     `json:"tag"`
	MTU            int        `json:"mtu,omitempty"`
	Address        []string   `json:"address"`
	PrivateKey     string     `json:"private_key"`
	Peers          []sbWgPeer `json:"peers"`
	DomainResolver string     `json:"domain_resolver,omitempty"`
}

type sbWgPeer struct {
	Address    string   `json:"address"`
	Port       int      `json:"port"`
	PublicKey  string   `json:"public_key"`
	AllowedIPs []string `json:"allowed_ips"`
	Reserved   []int    `json:"reserved"`
}

type sbDirectOut struct {
	Type string `json:"type"`
	Tag  string `json:"tag"`
}

// sbSocksOut 官方 WARP 客户端（MASQUE）的本地 SOCKS5 出站。
type sbSocksOut struct {
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
	Version    string `json:"version"`
}

type sbDomainResolver struct {
	Server string `json:"server"`
}

type sbRoute struct {
	Rules                 []sbRouteRule      `json:"rules"`
	RuleSet               []sbRuleSet        `json:"rule_set"`
	Final                 string             `json:"final"`
	AutoDetectInterface   bool               `json:"auto_detect_interface"`
	DefaultDomainResolver *sbDomainResolver  `json:"default_domain_resolver,omitempty"`
}

type sbRouteRule struct {
	Action      string   `json:"action,omitempty"`
	Protocol    string   `json:"protocol,omitempty"`
	IPIsPrivate bool     `json:"ip_is_private,omitempty"`
	RuleSet     []string `json:"rule_set,omitempty"`
	Outbound    string   `json:"outbound,omitempty"`
}

type sbRuleSet struct {
	Type   string `json:"type"`
	Tag    string `json:"tag"`
	Format string `json:"format"`
	Path   string `json:"path"`
}

// BuildConfig 依据用户配置生成 sing-box 配置文件。
// backend=client: GFW 流量经官方 WARP 客户端（MASQUE）的本地 SOCKS5 转发；
// backend=wireguard: GFW 流量经内置 WireGuard endpoint 直连 WARP。
func BuildConfig(p cfg.Paths, app cfg.App, acc *warp.Account) error {
	backend := app.Warp.Backend
	if backend == "" {
		backend = cfg.BackendClient
	}

	var dnsWarp sbDNSServer
	var warpOutbound any
	var endpoints []sbWireguardEndpoint

	switch backend {
	case cfg.BackendWireguard:
		if acc == nil {
			return fmt.Errorf("WireGuard 后端需要已注册的 WARP 账号")
		}
		host, port := splitEndpoint(acc.Endpoint)
		res := acc.Reserved()
		dnsWarp = sbDNSServer{Type: "udp", Tag: "dns-warp", Server: app.DNS.Remote, Detour: "warp"}
		warpOutbound = nil // warp 由 endpoint 承担
		endpoints = []sbWireguardEndpoint{{
			Type:           "wireguard",
			Tag:            "warp",
			MTU:            app.Warp.MTU,
			Address:        acc.LocalAddresses(),
			PrivateKey:     acc.PrivateKey,
			DomainResolver: "dns-local",
			Peers: []sbWgPeer{{
				Address:    host,
				Port:       port,
				PublicKey:  acc.PeerPublicKey,
				AllowedIPs: []string{"0.0.0.0/0", "::/0"},
				Reserved:   []int{int(res[0]), int(res[1]), int(res[2])},
			}},
		}}
	default: // client
		proxyHost, proxyPort := splitEndpoint(app.Warp.ClientProxy)
		dnsWarp = sbDNSServer{Type: "https", Tag: "dns-warp", Server: app.DNS.Remote, Detour: "warp"}
		warpOutbound = sbSocksOut{
			Type:       "socks",
			Tag:        "warp",
			Server:     proxyHost,
			ServerPort: proxyPort,
			Version:    "5",
		}
	}

	c := sbConfig{
		Log: sbLog{Level: app.LogLevel, Timestamp: true},
		DNS: sbDNS{
			Servers: []sbDNSServer{
				// 注：direct 为空出站时不可作为 detour（sing-box 会拒绝），
				// dns-local 不设 detour 即默认走 direct 出站。
				{Type: "udp", Tag: "dns-local", Server: app.DNS.Local},
				dnsWarp,
			},
			// GFW 域名与开发站点（pypi/npm/docker 镜像等）的解析必须走 WARP 通道，
			// 避免 DNS 污染与解析劣化；其余域名走国内 DNS。
			Rules: []sbDNSRule{
				{RuleSet: []string{"geosite-gfw", "geosite-dev"}, Server: "dns-warp"},
			},
			Final:    "dns-local",
			Strategy: "prefer_ipv4",
		},
		Endpoints: endpoints,
		Inbounds: []sbTun{{
			Type:          "tun",
			Tag:           "tun-in",
			InterfaceName: app.Tun.Name,
			Address:       []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"},
			MTU:           app.Tun.MTU,
			// 最小化构建不含 gVisor，显式使用 system 协议栈（经典模式，稳定）
			Stack:       "system",
			AutoRoute:   true,
			StrictRoute: app.Tun.StrictRoute,
		}},
		Outbounds: buildOutbounds(warpOutbound),
		Route: sbRoute{
			Rules: []sbRouteRule{
				{Action: "sniff"},
				{Protocol: "dns", Action: "hijack-dns"},
				{IPIsPrivate: true, Outbound: "direct"},
				// GFW 名单 + 开发站点名单（pypi/npm/ghcr/quay/k8s 等）→ WARP
				{RuleSet: []string{"geosite-gfw", "geosite-dev"}, Outbound: "warp"},
				{RuleSet: []string{"geosite-cn", "geoip-cn"}, Outbound: "direct"},
			},
			RuleSet: []sbRuleSet{
				localRuleSet("geosite-gfw", p.RulesDir),
				localRuleSet("geosite-dev", p.RulesDir),
				localRuleSet("geosite-cn", p.RulesDir),
				localRuleSet("geoip-cn", p.RulesDir),
			},
			Final:                 "direct",
			AutoDetectInterface:   true,
			DefaultDomainResolver: &sbDomainResolver{Server: "dns-local"},
		},
	}

	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.SBConfig), 0o755); err != nil {
		return err
	}
	// 配置内含 WARP 私钥，仅限所有者读取
	return os.WriteFile(p.SBConfig, append(b, '\n'), 0o600)
}

func buildOutbounds(warpOutbound any) []any {
	out := []any{}
	if warpOutbound != nil {
		out = append(out, warpOutbound)
	}
	return append(out, sbDirectOut{Type: "direct", Tag: "direct"})
}

// CheckConfig 调用核心的 check 子命令校验生成的配置。
func CheckConfig(p cfg.Paths) error {
	out, err := runCore(p, "check", "-c", p.SBConfig)
	if err != nil {
		return fmt.Errorf("核心配置校验失败: %v\n%s", err, out)
	}
	return nil
}

func localRuleSet(tag, rulesDir string) sbRuleSet {
	return sbRuleSet{
		Type:   "local",
		Tag:    tag,
		Format: "binary",
		Path:   filepath.Join(rulesDir, tag+".srs"),
	}
}

func splitEndpoint(endpoint string) (string, int) {
	host, portStr, err := net.SplitHostPort(endpoint)
	if err != nil {
		return endpoint, 2408
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		port = 2408
	}
	return host, port
}
