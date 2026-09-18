// Package warp 实现 Cloudflare WARP 免费账号的注册与凭据管理。
// 注册流程与 wgcf 等开源实现一致：生成 X25519 密钥对，
// 调用 api.cloudflareclient.com 的设备注册接口获取 WireGuard 接入参数。
package warp

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	apiBase        = "https://api.cloudflareclient.com"
	regPath        = "/v0a2158/reg"
	peerPublicKey  = "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=" // WARP 服务端公钥（全局固定）
	DefaultEndpoint = "engage.cloudflareclient.com:2408"
)

// Account WARP 接入凭据（WireGuard 协议参数）。
type Account struct {
	PrivateKey    string    `json:"private_key"`
	PublicKey     string    `json:"public_key"`
	ClientID      string    `json:"client_id"`
	IPv4          string    `json:"ipv4"`
	IPv6          string    `json:"ipv6"`
	Token         string    `json:"token,omitempty"`
	AccountID     string    `json:"account_id,omitempty"`
	License       string    `json:"license,omitempty"`
	Endpoint      string    `json:"endpoint"`
	PeerPublicKey string    `json:"peer_public_key"`
	// 注册接口返回的接入点信息（用于 warp-scan）
	EndpointV4    string    `json:"endpoint_v4,omitempty"`    // 如 162.159.192.5
	EndpointV6    string    `json:"endpoint_v6,omitempty"`    // 如 2606:4700:d0::a29f:c005
	EndpointPorts []int     `json:"endpoint_ports,omitempty"` // 官方支持的 UDP 端口
	RegisteredAt  time.Time `json:"registered_at"`
}

// Reserved 将 client_id 解码为 WireGuard 的 reserved 字段。
func (a *Account) Reserved() [3]byte {
	var r [3]byte
	raw, err := base64.StdEncoding.DecodeString(a.ClientID)
	if err == nil {
		copy(r[:], raw)
	}
	return r
}

// LocalAddresses 返回 WireGuard local_address 列表。
func (a *Account) LocalAddresses() []string {
	out := []string{a.IPv4 + "/32"}
	if a.IPv6 != "" {
		out = append(out, a.IPv6+"/128")
	}
	return out
}

// Load 读取已保存的账号；文件不存在或无效时返回 (nil, nil)。
func Load(path string) (*Account, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var a Account
	if err := json.Unmarshal(stripBOM(b), &a); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	if a.PrivateKey == "" || a.IPv4 == "" || a.ClientID == "" {
		return nil, nil
	}
	if a.Endpoint == "" {
		a.Endpoint = DefaultEndpoint
	}
	if a.PeerPublicKey == "" {
		a.PeerPublicKey = peerPublicKey
	}
	return &a, nil
}

// Save 保存账号（0600，仅本机管理员/所有者可读）。
func Save(path string, a *Account) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// Register 生成新密钥并注册一个免费 WARP 账号。
func Register(endpoint string) (*Account, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("生成密钥失败: %w", err)
	}
	privB64 := base64.StdEncoding.EncodeToString(priv.Bytes())
	pubB64 := base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes())

	serial := make([]byte, 16)
	if _, err := rand.Read(serial); err != nil {
		return nil, err
	}
	payload := map[string]any{
		"key":           pubB64,
		"install_id":    "",
		"fcm_token":     "",
		"tos":           "2024-01-01T00:00:00.000Z",
		"model":         "PC",
		"serial_number": hex.EncodeToString(serial),
		"locale":        "en_US",
		"type":          "Android",
		"zone":          "US",
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodPost, apiBase+regPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "curl/8.5.0")
	req.Header.Set("CF-Client-Version", "a-7.21-0728")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法连接 WARP 注册接口（api.cloudflareclient.com）: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("WARP 注册被拒绝 (HTTP %d): %s", resp.StatusCode, truncate(string(rb), 300))
	}

	var out struct {
		ID      string `json:"id"`
		Token   string `json:"token"`
		Account struct {
			ID      string `json:"id"`
			License string `json:"license"`
		} `json:"account"`
		Config struct {
			ClientID  string `json:"client_id"`
			Peers     []struct {
				PublicKey string `json:"public_key"`
				Endpoint  struct {
					V4    string `json:"v4"`
					V6    string `json:"v6"`
					Host  string `json:"host"`
					Ports []int  `json:"ports"`
				} `json:"endpoint"`
			} `json:"peers"`
			Interface struct {
				Addresses struct {
					V4 string `json:"v4"`
					V6 string `json:"v6"`
				} `json:"addresses"`
			} `json:"interface"`
		} `json:"config"`
	}
	if err := json.Unmarshal(rb, &out); err != nil {
		return nil, fmt.Errorf("解析注册响应失败: %w", err)
	}
	if out.Config.ClientID == "" || out.Config.Interface.Addresses.V4 == "" {
		return nil, fmt.Errorf("注册响应缺少必要字段: %s", truncate(string(rb), 300))
	}
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	peerKey := peerPublicKey
	epV4, epV6 := "", ""
	ports := []int{}
	if len(out.Config.Peers) > 0 {
		if out.Config.Peers[0].PublicKey != "" {
			peerKey = out.Config.Peers[0].PublicKey
		}
		epV4 = hostOf(out.Config.Peers[0].Endpoint.V4)
		epV6 = hostOf(out.Config.Peers[0].Endpoint.V6)
		ports = out.Config.Peers[0].Endpoint.Ports
	}
	if len(ports) == 0 {
		ports = []int{2408, 500, 1701, 4500}
	}
	return &Account{
		PrivateKey:    privB64,
		PublicKey:     pubB64,
		ClientID:      out.Config.ClientID,
		IPv4:          out.Config.Interface.Addresses.V4,
		IPv6:          out.Config.Interface.Addresses.V6,
		Token:         out.Token,
		AccountID:     out.Account.ID,
		License:       out.Account.License,
		Endpoint:      endpoint,
		PeerPublicKey: peerKey,
		EndpointV4:    epV4,
		EndpointV6:    epV6,
		EndpointPorts: ports,
		RegisteredAt:  time.Now(),
	}, nil
}

// hostOf 从 "ip:port" / "[v6]:port" 中提取主机部分（端口为 0 时也剥离）。
func hostOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return strings.Trim(addr, "[]")
	}
	return host
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// stripBOM 去除 UTF-8 BOM（EF BB BF）。
func stripBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}
