// Package parser 负责解析常见节点分享链接（VLESS, VMess, Shadowsocks, Trojan, SOCKS5, HTTP），
// 并将其转换为 sing-box 1.14 规范的 Outbound 配置。
package parser

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ParsedOutbound 包含生成的出站配置和显示名
type ParsedOutbound struct {
	Tag      string
	Outbound map[string]any
}

// ParseNodeURL 解析分享链接为 sing-box outbound 结构
func ParseNodeURL(raw string, tag string) (*ParsedOutbound, error) {
	raw = strings.TrimSpace(raw)
	if tag == "" {
		tag = "warp"
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("URL 格式不合法: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "vless":
		return parseVLESS(u, tag)
	case "vmess":
		return parseVMess(raw, tag)
	case "hy2", "hysteria2":
		return parseHysteria2(u, tag)
	case "tuic":
		return parseTUIC(u, tag)
	case "ss":
		return parseShadowsocks(u, tag)
	case "trojan":
		return parseTrojan(u, tag)
	case "socks", "socks5":
		return parseSocks(u, tag)
	case "http", "https":
		return parseHTTP(u, tag)
	default:
		return nil, fmt.Errorf("不支持的链接协议: %s (支持 vless://, vmess://, hy2://, tuic://, ss://, trojan://, socks5://, http://)", scheme)
	}
}

// parseVLESS 解析 vless://uuid@host:port?params#name
func parseVLESS(u *url.URL, tag string) (*ParsedOutbound, error) {
	uuid := u.User.Username()
	if uuid == "" {
		return nil, fmt.Errorf("vless 链接缺少 uuid")
	}
	host, port, err := splitHostPort(u.Host, 443)
	if err != nil {
		return nil, err
	}

	q := u.Query()
	out := map[string]any{
		"type":        "vless",
		"tag":         tag,
		"server":      host,
		"server_port": port,
		"uuid":        uuid,
	}

	if flow := q.Get("flow"); flow != "" {
		out["flow"] = flow
	}

	// TLS / Reality 处理
	sec := strings.ToLower(q.Get("security"))
	if sec == "tls" || sec == "reality" {
		tlsMap := map[string]any{
			"enabled": true,
		}
		if sni := q.Get("sni"); sni != "" {
			tlsMap["server_name"] = sni
		}
		if alpn := q.Get("alpn"); alpn != "" {
			tlsMap["alpn"] = strings.Split(alpn, ",")
		}
		if insecure := q.Get("allowInsecure"); insecure == "1" || insecure == "true" {
			tlsMap["insecure"] = true
		}

		if sec == "reality" {
			realityMap := map[string]any{
				"enabled": true,
			}
			if pbk := q.Get("pbk"); pbk != "" {
				realityMap["public_key"] = pbk
			}
			if sid := q.Get("sid"); sid != "" {
				realityMap["short_id"] = sid
			}
			tlsMap["reality"] = realityMap

			// reality 强制要求开启 uTLS
			fp := q.Get("fp")
			if fp == "" {
				fp = "chrome"
			}
			tlsMap["utls"] = map[string]any{
				"enabled":     true,
				"fingerprint": fp,
			}
		} else if fp := q.Get("fp"); fp != "" {
			tlsMap["utls"] = map[string]any{
				"enabled":     true,
				"fingerprint": fp,
			}
		}
		out["tls"] = tlsMap
	}

	// Transport 处理 (ws, grpc, httpupgrade)
	network := strings.ToLower(q.Get("type"))
	if network == "" {
		network = strings.ToLower(q.Get("network"))
	}
	if network != "" && network != "tcp" {
		trans := map[string]any{
			"type": network,
		}
		switch network {
		case "ws":
			if p := q.Get("path"); p != "" {
				trans["path"] = p
			}
			if h := q.Get("host"); h != "" {
				trans["headers"] = map[string]string{"Host": h}
			}
		case "grpc":
			if sn := q.Get("serviceName"); sn != "" {
				trans["service_name"] = sn
			}
		case "http", "httpupgrade":
			trans["type"] = "httpupgrade"
			if p := q.Get("path"); p != "" {
				trans["path"] = p
			}
			if h := q.Get("host"); h != "" {
				trans["host"] = h
			}
		}
		out["transport"] = trans
	}

	return &ParsedOutbound{Tag: tag, Outbound: out}, nil
}

// parseVMess 解析 vmess://base64(json)
func parseVMess(raw string, tag string) (*ParsedOutbound, error) {
	b64 := strings.TrimPrefix(raw, "vmess://")
	b64 = strings.TrimPrefix(b64, "VMESS://")
	if i := strings.Index(b64, "#"); i >= 0 {
		b64 = b64[:i]
	}

	decoded, err := decodeBase64(b64)
	if err != nil {
		return nil, fmt.Errorf("vmess base64 解码失败: %w", err)
	}

	var meta struct {
		Add  string `json:"add"`
		Port any    `json:"port"`
		ID   string `json:"id"`
		Aid  any    `json:"aid"`
		Net  string `json:"net"`
		Type string `json:"type"`
		Host string `json:"host"`
		Path string `json:"path"`
		TLS  string `json:"tls"`
		SNI  string `json:"sni"`
		Alpn string `json:"alpn"`
	}
	if err := json.Unmarshal(decoded, &meta); err != nil {
		return nil, fmt.Errorf("解析 vmess json 失败: %w", err)
	}

	port := 443
	switch v := meta.Port.(type) {
	case float64:
		port = int(v)
	case string:
		if p, err := strconv.Atoi(v); err == nil {
			port = p
		}
	}

	out := map[string]any{
		"type":        "vmess",
		"tag":         tag,
		"server":      meta.Add,
		"server_port": port,
		"uuid":        meta.ID,
		"security":    "auto",
	}

	if meta.TLS == "tls" {
		tlsMap := map[string]any{"enabled": true}
		if meta.SNI != "" {
			tlsMap["server_name"] = meta.SNI
		} else if meta.Host != "" {
			tlsMap["server_name"] = meta.Host
		}
		if meta.Alpn != "" {
			tlsMap["alpn"] = strings.Split(meta.Alpn, ",")
		}
		out["tls"] = tlsMap
	}

	if meta.Net != "" && meta.Net != "tcp" {
		trans := map[string]any{"type": meta.Net}
		if meta.Net == "ws" {
			if meta.Path != "" {
				trans["path"] = meta.Path
			}
			if meta.Host != "" {
				trans["headers"] = map[string]string{"Host": meta.Host}
			}
		}
		out["transport"] = trans
	}

	return &ParsedOutbound{Tag: tag, Outbound: out}, nil
}

// parseShadowsocks 解析 ss://base64(method:password@host:port)#name 或 ss://base64(method:password)@host:port#name
func parseShadowsocks(u *url.URL, tag string) (*ParsedOutbound, error) {
	var method, password, host string
	var port int

	if u.User != nil {
		// 形如 ss://base64(method:password)@host:port
		userPart := u.User.Username()
		if pass, ok := u.User.Password(); ok {
			// 明文 method:password
			method = userPart
			password = pass
		} else {
			// base64 编码的 method:password
			dec, err := decodeBase64(userPart)
			if err != nil {
				return nil, fmt.Errorf("ss base64 用户名解码失败: %w", err)
			}
			parts := strings.SplitN(string(dec), ":", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("ss 用户认证信息格式错误")
			}
			method = parts[0]
			password = parts[1]
		}
		var err error
		host, port, err = splitHostPort(u.Host, 8388)
		if err != nil {
			return nil, err
		}
	} else {
		// 全体 base64: ss://base64(method:password@host:port)
		dec, err := decodeBase64(u.Host)
		if err != nil {
			return nil, fmt.Errorf("ss base64 解码失败: %w", err)
		}
		full := string(dec)
		atIdx := strings.LastIndex(full, "@")
		if atIdx < 0 {
			return nil, fmt.Errorf("ss 格式错误: 缺少 @ 符号")
		}
		credParts := strings.SplitN(full[:atIdx], ":", 2)
		if len(credParts) != 2 {
			return nil, fmt.Errorf("ss 凭据格式错误")
		}
		method = credParts[0]
		password = credParts[1]
		var err2 error
		host, port, err2 = splitHostPort(full[atIdx+1:], 8388)
		if err2 != nil {
			return nil, err2
		}
	}

	out := map[string]any{
		"type":        "shadowsocks",
		"tag":         tag,
		"server":      host,
		"server_port": port,
		"method":      method,
		"password":    password,
	}
	return &ParsedOutbound{Tag: tag, Outbound: out}, nil
}

// parseTrojan 解析 trojan://password@host:port?sni=...#name
func parseTrojan(u *url.URL, tag string) (*ParsedOutbound, error) {
	password := u.User.Username()
	if password == "" {
		return nil, fmt.Errorf("trojan 缺少密码")
	}
	host, port, err := splitHostPort(u.Host, 443)
	if err != nil {
		return nil, err
	}

	q := u.Query()
	out := map[string]any{
		"type":        "trojan",
		"tag":         tag,
		"server":      host,
		"server_port": port,
		"password":    password,
	}

	tlsMap := map[string]any{"enabled": true}
	if sni := q.Get("sni"); sni != "" {
		tlsMap["server_name"] = sni
	} else {
		tlsMap["server_name"] = host
	}
	if alpn := q.Get("alpn"); alpn != "" {
		tlsMap["alpn"] = strings.Split(alpn, ",")
	}
	if insecure := q.Get("allowInsecure"); insecure == "1" || insecure == "true" {
		tlsMap["insecure"] = true
	}
	out["tls"] = tlsMap

	if network := q.Get("type"); network != "" && network != "tcp" {
		trans := map[string]any{"type": network}
		if network == "ws" {
			if p := q.Get("path"); p != "" {
				trans["path"] = p
			}
			if h := q.Get("host"); h != "" {
				trans["headers"] = map[string]string{"Host": h}
			}
		}
		out["transport"] = trans
	}

	return &ParsedOutbound{Tag: tag, Outbound: out}, nil
}

// parseSocks 解析 socks5://user:pass@host:port
func parseSocks(u *url.URL, tag string) (*ParsedOutbound, error) {
	host, port, err := splitHostPort(u.Host, 1080)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"type":        "socks",
		"tag":         tag,
		"server":      host,
		"server_port": port,
		"version":     "5",
	}
	if u.User != nil {
		out["username"] = u.User.Username()
		if pass, ok := u.User.Password(); ok {
			out["password"] = pass
		}
	}
	return &ParsedOutbound{Tag: tag, Outbound: out}, nil
}

// parseHTTP 解析 http://user:pass@host:port
func parseHTTP(u *url.URL, tag string) (*ParsedOutbound, error) {
	host, port, err := splitHostPort(u.Host, 8080)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"type":        "http",
		"tag":         tag,
		"server":      host,
		"server_port": port,
	}
	if u.User != nil {
		out["username"] = u.User.Username()
		if pass, ok := u.User.Password(); ok {
			out["password"] = pass
		}
	}
	return &ParsedOutbound{Tag: tag, Outbound: out}, nil
}

// parseHysteria2 解析 hy2://password@host:port?sni=...&insecure=1
func parseHysteria2(u *url.URL, tag string) (*ParsedOutbound, error) {
	password := u.User.Username()
	if password == "" {
		if pass, ok := u.User.Password(); ok {
			password = pass
		}
	}
	host, port, err := splitHostPort(u.Host, 443)
	if err != nil {
		return nil, err
	}

	q := u.Query()
	out := map[string]any{
		"type":        "hysteria2",
		"tag":         tag,
		"server":      host,
		"server_port": port,
	}
	if password != "" {
		out["password"] = password
	}

	tlsMap := map[string]any{
		"enabled": true,
	}
	if sni := q.Get("sni"); sni != "" {
		tlsMap["server_name"] = sni
	} else {
		tlsMap["server_name"] = host
	}
	if alpn := q.Get("alpn"); alpn != "" {
		tlsMap["alpn"] = strings.Split(alpn, ",")
	}
	if insecure := q.Get("insecure"); insecure == "1" || insecure == "true" {
		tlsMap["insecure"] = true
	}
	out["tls"] = tlsMap

	if up := q.Get("up"); up != "" {
		if v, err := strconv.Atoi(up); err == nil && v > 0 {
			out["up_mbps"] = v
		}
	}
	if down := q.Get("down"); down != "" {
		if v, err := strconv.Atoi(down); err == nil && v > 0 {
			out["down_mbps"] = v
		}
	}
	if obfs := q.Get("obfs"); obfs != "" {
		obfsMap := map[string]any{"type": obfs}
		if p := q.Get("obfs-password"); p != "" {
			obfsMap["password"] = p
		}
		out["obfs"] = obfsMap
	}

	return &ParsedOutbound{Tag: tag, Outbound: out}, nil
}

// parseTUIC 解析 tuic://uuid:password@host:port?congestion_control=bbr&sni=...
func parseTUIC(u *url.URL, tag string) (*ParsedOutbound, error) {
	uuid := u.User.Username()
	password, _ := u.User.Password()
	host, port, err := splitHostPort(u.Host, 443)
	if err != nil {
		return nil, err
	}

	q := u.Query()
	out := map[string]any{
		"type":        "tuic",
		"tag":         tag,
		"server":      host,
		"server_port": port,
		"uuid":        uuid,
	}
	if password != "" {
		out["password"] = password
	}

	cc := q.Get("congestion_control")
	if cc == "" {
		cc = "bbr"
	}
	out["congestion_control"] = cc

	tlsMap := map[string]any{"enabled": true}
	if sni := q.Get("sni"); sni != "" {
		tlsMap["server_name"] = sni
	} else {
		tlsMap["server_name"] = host
	}
	if alpn := q.Get("alpn"); alpn != "" {
		tlsMap["alpn"] = strings.Split(alpn, ",")
	}
	if insecure := q.Get("allowInsecure"); insecure == "1" || insecure == "true" {
		tlsMap["insecure"] = true
	}
	out["tls"] = tlsMap

	return &ParsedOutbound{Tag: tag, Outbound: out}, nil
}

func splitHostPort(raw string, defaultPort int) (string, int, error) {
	if strings.Contains(raw, ":") && !strings.HasSuffix(raw, "]") {
		h, pStr, err := net.SplitHostPort(raw)
		if err == nil {
			p, err := strconv.Atoi(pStr)
			if err == nil && p > 0 {
				return h, p, nil
			}
		}
	}
	clean := strings.Trim(raw, "[]")
	return clean, defaultPort, nil
}

func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	// 补齐 padding
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}
