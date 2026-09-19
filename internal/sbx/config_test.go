package sbx

import (
	"flowgate/internal/cfg"
	"testing"
)

func TestBuildTunAddress(t *testing.T) {
	app := cfg.DefaultApp()
	addrs := buildTunAddress(app)
	if len(addrs) == 0 || addrs[0] != "198.18.0.1/30" {
		t.Fatalf("expected 198.18.0.1/30, got %v", addrs)
	}

	app.Tun.Address = []string{"10.254.0.1/30"}
	customAddrs := buildTunAddress(app)
	if len(customAddrs) == 0 || customAddrs[0] != "10.254.0.1/30" {
		t.Fatalf("expected 10.254.0.1/30, got %v", customAddrs)
	}
}

func TestBuildDNSRulesAndRouteRules(t *testing.T) {
	app := cfg.DefaultApp()
	app.Route.ProxyDomains = []string{"huggingface.co", "cursor.com"}
	app.Route.DirectDomains = []string{"lan.local"}

	dnsRules := buildDNSRules(app)
	if len(dnsRules) < 3 {
		t.Fatalf("expected at least 3 DNS rules, got %d", len(dnsRules))
	}
	if dnsRules[0].Server != "dns-local" || dnsRules[0].DomainSuffix[0] != "lan.local" {
		t.Fatalf("expected first DNS rule direct, got %v", dnsRules[0])
	}
	if dnsRules[1].Server != "dns-warp" || dnsRules[1].DomainSuffix[0] != "huggingface.co" {
		t.Fatalf("expected second DNS rule warp, got %v", dnsRules[1])
	}

	routeRules := buildRouteRules(app)
	hasCustomProxy := false
	hasCustomDirect := false
	for _, r := range routeRules {
		if len(r.DomainSuffix) > 0 && r.DomainSuffix[0] == "huggingface.co" && r.Outbound == "warp" {
			hasCustomProxy = true
		}
		if len(r.DomainSuffix) > 0 && r.DomainSuffix[0] == "lan.local" && r.Outbound == "direct" {
			hasCustomDirect = true
		}
	}
	if !hasCustomProxy || !hasCustomDirect {
		t.Fatalf("missing custom route rules: proxy=%v, direct=%v", hasCustomProxy, hasCustomDirect)
	}
}
