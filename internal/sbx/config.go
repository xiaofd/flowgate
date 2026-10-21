// Package sbx 负责生成 sing-box（fg-core）配置并守护其进程。
package sbx

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"flowgate/internal/cfg"
	"flowgate/internal/parser"
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
	Domain       []string `json:"domain,omitempty"`
	DomainSuffix []string `json:"domain_suffix,omitempty"`
	RuleSet      []string `json:"rule_set,omitempty"`
	Server       string   `json:"server"`
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
	Action       string   `json:"action,omitempty"`
	Protocol     string   `json:"protocol,omitempty"`
	IPIsPrivate  bool     `json:"ip_is_private,omitempty"`
	Domain       []string `json:"domain,omitempty"`
	DomainSuffix []string `json:"domain_suffix,omitempty"`
	IPCIDR       []string `json:"ip_cidr,omitempty"`
	RuleSet      []string `json:"rule_set,omitempty"`
	Outbound     string   `json:"outbound,omitempty"`
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
	case cfg.BackendCustom:
		if strings.TrimSpace(app.Warp.NodeURL) == "" {
			return fmt.Errorf("自定义节点后端需要提供有效的节点分享链接 (node_url)")
		}
		parsed, err := parser.ParseNodeURL(app.Warp.NodeURL, "warp")
		if err != nil {
			return fmt.Errorf("解析自定义节点链接失败: %w", err)
		}
		warpOutbound = parsed.Outbound
		dnsWarp = sbDNSServer{Type: "https", Tag: "dns-warp", Server: app.DNS.Remote, Detour: "warp"}
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
			Rules:    buildDNSRules(app),
			Final:    "dns-local",
			Strategy: "prefer_ipv4",
		},
		Endpoints: endpoints,
		Inbounds: []sbTun{{
			Type:          "tun",
			Tag:           "tun-in",
			InterfaceName: app.Tun.Name,
			Address:       buildTunAddress(app),
			MTU:           app.Tun.MTU,
			// 最小化构建不含 gVisor，显式使用 system 协议栈（经典模式，稳定）
			Stack:       "system",
			AutoRoute:   true,
			StrictRoute: app.Tun.StrictRoute,
		}},
		Outbounds: buildOutbounds(warpOutbound),
		Route: sbRoute{
			Rules: buildRouteRules(app),
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

func buildTunAddress(app cfg.App) []string {
	if len(app.Tun.Address) > 0 {
		return app.Tun.Address
	}
	// 默认使用 198.18.0.1/30（RFC 2544 / 5737），避开 Docker 默认 172.17~31 网段
	return []string{"198.18.0.1/30", "fdfe:dcba:9876::1/126"}
}

// parseRuleItems 智能解析自定义清单项，将通配符域名、普通域名、单 IP、CIDR 网段分别归类。
func parseRuleItems(items []string) (domains []string, suffixes []string, ipCIDRs []string) {
	for _, raw := range items {
		item := strings.TrimSpace(strings.ToLower(raw))
		if item == "" {
			continue
		}
		// 剥离可能误贴的 URL 协议头 (https://, http://)
		if i := strings.Index(item, "://"); i >= 0 {
			item = item[i+3:]
		}
		// 截断路径，但需避开 CIDR 格式（如 192.168.1.0/24）
		if _, _, err := net.ParseCIDR(item); err == nil {
			ipCIDRs = append(ipCIDRs, item)
			continue
		}
		if i := strings.Index(item, "/"); i >= 0 {
			item = item[:i]
		}
		// 剥离可能携带的端口（如 example.com:443，避开 IPv6 如 [::1]:80）
		if strings.Contains(item, ":") && !strings.Contains(item, "::") {
			if h, _, err := net.SplitHostPort(item); err == nil {
				item = h
			}
		}

		// 单 IP 检测（IPv4 / IPv6）
		if ip := net.ParseIP(item); ip != nil {
			if ip.To4() != nil {
				ipCIDRs = append(ipCIDRs, ip.String()+"/32")
			} else {
				ipCIDRs = append(ipCIDRs, ip.String()+"/128")
			}
			continue
		}

		// 通配符域名处理（*.example.com 或 .example.com）
		if strings.HasPrefix(item, "*.") {
			clean := strings.TrimPrefix(item, "*.")
			suffixes = append(suffixes, clean)
			domains = append(domains, clean)
			continue
		}
		if strings.HasPrefix(item, ".") {
			clean := strings.TrimPrefix(item, ".")
			suffixes = append(suffixes, clean)
			domains = append(domains, clean)
			continue
		}

		// 普通域名：既支持精确匹配，也支持后缀匹配以覆盖全量子域名
		suffixes = append(suffixes, item)
		domains = append(domains, item)
	}
	return deduplicate(domains), deduplicate(suffixes), deduplicate(ipCIDRs)
}

func deduplicate(list []string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, s := range list {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

func buildDNSRules(app cfg.App) []sbDNSRule {
	rules := []sbDNSRule{}

	// === 最高优先级 #1：用户自定义直连域名解析 ===
	dirDomains, dirSuffixes, _ := parseRuleItems(app.AllDirectList())
	if len(dirDomains) > 0 || len(dirSuffixes) > 0 {
		rules = append(rules, sbDNSRule{Domain: dirDomains, DomainSuffix: dirSuffixes, Server: "dns-local"})
	}

	// === 最高优先级 #2：用户自定义走 WARP 域名解析（防污染） ===
	proxyDomains, proxySuffixes, _ := parseRuleItems(app.AllProxyList())
	if len(proxyDomains) > 0 || len(proxySuffixes) > 0 {
		rules = append(rules, sbDNSRule{Domain: proxyDomains, DomainSuffix: proxySuffixes, Server: "dns-warp"})
	}

	// === 次优先级：系统内置规则集 ===
	rules = append(rules, sbDNSRule{RuleSet: []string{"geosite-gfw", "geosite-dev"}, Server: "dns-warp"})
	return rules
}

func buildRouteRules(app cfg.App) []sbRouteRule {
	rules := []sbRouteRule{
		{Action: "sniff"},
		{Protocol: "dns", Action: "hijack-dns"},
	}

	// === 最高优先级 #1：用户自定义直连清单（强制直连，碾压一切）===
	dirDomains, dirSuffixes, dirIPs := parseRuleItems(app.AllDirectList())
	if len(dirDomains) > 0 || len(dirSuffixes) > 0 {
		rules = append(rules, sbRouteRule{Domain: dirDomains, DomainSuffix: dirSuffixes, Outbound: "direct"})
	}
	if len(dirIPs) > 0 {
		rules = append(rules, sbRouteRule{IPCIDR: dirIPs, Outbound: "direct"})
	}

	// === 最高优先级 #2：用户自定义走 WARP 清单（强制走 WARP，碾压内置直连规则）===
	proxyDomains, proxySuffixes, proxyIPs := parseRuleItems(app.AllProxyList())
	if len(proxyDomains) > 0 || len(proxySuffixes) > 0 {
		rules = append(rules, sbRouteRule{Domain: proxyDomains, DomainSuffix: proxySuffixes, Outbound: "warp"})
	}
	if len(proxyIPs) > 0 {
		rules = append(rules, sbRouteRule{IPCIDR: proxyIPs, Outbound: "warp"})
	}

	// === 次优先级：系统默认规则 ===
	rules = append(rules,
		sbRouteRule{IPIsPrivate: true, Outbound: "direct"},
		sbRouteRule{RuleSet: []string{"geosite-gfw", "geosite-dev"}, Outbound: "warp"},
		sbRouteRule{RuleSet: []string{"geosite-cn", "geoip-cn"}, Outbound: "direct"},
	)
	return rules
}
