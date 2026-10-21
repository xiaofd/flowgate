package parser

import (
	"testing"
)

func TestParseVLESS(t *testing.T) {
	raw := "vless://a3424160-5a7a-4ec6-8c43-b952f48f3b79@1.2.3.4:443?security=reality&sni=gateway.icloud.com&pbk=abcd1234&sid=1234&type=tcp#TestNode"
	res, err := ParseNodeURL(raw, "warp")
	if err != nil {
		t.Fatalf("Parse VLESS error: %v", err)
	}
	if res.Outbound["type"] != "vless" {
		t.Fatalf("Expected type vless, got %v", res.Outbound["type"])
	}
	if res.Outbound["server"] != "1.2.3.4" || res.Outbound["server_port"] != 443 {
		t.Fatalf("Server address mismatch")
	}
}

func TestParseSS(t *testing.T) {
	raw := "ss://YWVzLTEyOC1nY206cGFzc3dvcmQ=@1.2.3.4:8388#MySS"
	res, err := ParseNodeURL(raw, "warp")
	if err != nil {
		t.Fatalf("Parse SS error: %v", err)
	}
	if res.Outbound["type"] != "shadowsocks" {
		t.Fatalf("Expected type shadowsocks, got %v", res.Outbound["type"])
	}
	if res.Outbound["method"] != "aes-128-gcm" || res.Outbound["password"] != "password" {
		t.Fatalf("Credentials mismatch")
	}
}

func TestParseSocks(t *testing.T) {
	raw := "socks5://admin:123456@127.0.0.1:10808"
	res, err := ParseNodeURL(raw, "warp")
	if err != nil {
		t.Fatalf("Parse Socks error: %v", err)
	}
	if res.Outbound["type"] != "socks" || res.Outbound["server_port"] != 10808 {
		t.Fatalf("Socks mismatch")
	}
}

func TestParseHysteria2(t *testing.T) {
	raw := "hy2://mypassword@example.com:443?sni=example.com&insecure=1&up=50&down=100#MyHy2"
	res, err := ParseNodeURL(raw, "warp")
	if err != nil {
		t.Fatalf("Parse Hy2 error: %v", err)
	}
	if res.Outbound["type"] != "hysteria2" {
		t.Fatalf("Expected type hysteria2, got %v", res.Outbound["type"])
	}
	if res.Outbound["password"] != "mypassword" || res.Outbound["up_mbps"] != 50 {
		t.Fatalf("Hy2 options mismatch")
	}
}

func TestParseTUIC(t *testing.T) {
	raw := "tuic://a3424160-5a7a-4ec6-8c43-b952f48f3b79:pass123@example.com:8443?congestion_control=bbr&sni=example.com#MyTUIC"
	res, err := ParseNodeURL(raw, "warp")
	if err != nil {
		t.Fatalf("Parse TUIC error: %v", err)
	}
	if res.Outbound["type"] != "tuic" {
		t.Fatalf("Expected type tuic, got %v", res.Outbound["type"])
	}
	if res.Outbound["uuid"] != "a3424160-5a7a-4ec6-8c43-b952f48f3b79" || res.Outbound["congestion_control"] != "bbr" {
		t.Fatalf("TUIC options mismatch")
	}
}
