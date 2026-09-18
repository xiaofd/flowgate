package sbx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"flowgate/internal/cfg"
	"flowgate/internal/warp"
)

// probeConfig 探测用最小 sing-box 配置：WireGuard endpoint + clash-api 延迟测试。
type probeConfig struct {
	Log        probeLog        `json:"log"`
	DNS        probeDNS        `json:"dns"`
	Endpoints  []sbWireguardEndpoint `json:"endpoints"`
	Outbounds  []sbDirectOut   `json:"outbounds"`
	Route      probeRoute      `json:"route"`
	Experimental probeExp      `json:"experimental"`
}

type probeLog struct {
	Level string `json:"level"`
}

type probeDNS struct {
	Servers  []sbDNSServer `json:"servers"`
	Final    string        `json:"final"`
	Strategy string        `json:"strategy"`
}

type probeRoute struct {
	Final                 string            `json:"final"`
	AutoDetectInterface   bool              `json:"auto_detect_interface"`
	DefaultDomainResolver *sbDomainResolver `json:"default_domain_resolver,omitempty"`
}

type probeExp struct {
	ClashAPI probeClashAPI `json:"clash_api"`
}

type probeClashAPI struct {
	ExternalController string `json:"external_controller"`
}

// ProbeWarp 用指定 endpoint 启动临时核心，通过 clash-api 延迟测试验证
// WireGuard 隧道端到端可用性（握手 + DNS + HTTP），无需管理员权限。
func ProbeWarp(p cfg.Paths, acc *warp.Account, endpoint string, apiPort int, timeout time.Duration) (time.Duration, error) {
	host, port := splitEndpoint(endpoint)
	res := acc.Reserved()
	// FG_PROBE_DEBUG=1 时输出 debug 日志并在失败时附带核心日志
	logLevel := "error"
	debugLog := os.Getenv("FG_PROBE_DEBUG") != ""

	workDir, err := os.MkdirTemp("", "fg-probe-*")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(workDir)

	confPath := filepath.Join(workDir, "probe.json")
	c := probeConfig{
		Log: probeLog{Level: logLevel},
		DNS: probeDNS{
			Servers: []sbDNSServer{
				{Type: "udp", Tag: "dns-direct", Server: "223.5.5.5"},
				{Type: "udp", Tag: "dns-warp", Server: "1.1.1.1", Detour: "warp"},
			},
			Final:    "dns-warp",
			Strategy: "prefer_ipv4",
		},
		Endpoints: []sbWireguardEndpoint{{
			Type:           "wireguard",
			Tag:            "warp",
			MTU:            1280,
			Address:        acc.LocalAddresses(),
			PrivateKey:     acc.PrivateKey,
			DomainResolver: "dns-direct",
			Peers: []sbWgPeer{{
				Address:    host,
				Port:       port,
				PublicKey:  acc.PeerPublicKey,
				AllowedIPs: []string{"0.0.0.0/0", "::/0"},
				Reserved:   []int{int(res[0]), int(res[1]), int(res[2])},
			}},
		}},
		Outbounds: []sbDirectOut{{Type: "direct", Tag: "direct"}},
		Route: probeRoute{
			Final:                 "direct",
			AutoDetectInterface:   true,
			DefaultDomainResolver: &sbDomainResolver{Server: "dns-warp"},
		},
		Experimental: probeExp{ClashAPI: probeClashAPI{
			ExternalController: "127.0.0.1:" + strconv.Itoa(apiPort),
		}},
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(confPath, b, 0o600); err != nil {
		return 0, err
	}

	// 启动临时核心
	cmd := exec.Command(p.CorePath, "run", "-c", confPath)
	cmd.Dir = workDir
	var errBuf strings.Builder
	cmd.Stdout = &errBuf
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("启动核心失败: %w", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
	}()

	// 等待 clash-api 就绪
	api := "http://127.0.0.1:" + strconv.Itoa(apiPort)
	deadline := time.Now().Add(3 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		resp, err := http.Get(api + "/version")
		if err == nil {
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !ready {
		return 0, fmt.Errorf("核心未就绪: %s", truncateStr(strings.TrimSpace(errBuf.String()), 200))
	}

	// 延迟测试（经 warp 隧道访问 GFW 站点，端到端验证）
	q := url.Values{}
	q.Set("timeout", strconv.Itoa(int(timeout.Milliseconds())))
	q.Set("url", "http://www.google.com/generate_204")
	client := &http.Client{Timeout: timeout + 3*time.Second}
	resp, err := client.Get(api + "/proxies/warp/delay?" + q.Encode())
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var out struct {
		Delay  int    `json:"delay"`
		Message string `json:"message"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusOK || out.Delay <= 0 {
		msg := out.Message
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		if debugLog {
			time.Sleep(1200 * time.Millisecond) // 等握手重试日志刷出
			// 先终止核心再读取日志缓冲，避免并发读写竞争
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-done
			return 0, fmt.Errorf("%s\n核心日志:\n%s", msg, truncateStr(strings.TrimSpace(errBuf.String()), 1200))
		}
		return 0, fmt.Errorf("%s", msg)
	}
	return time.Duration(out.Delay) * time.Millisecond, nil
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
