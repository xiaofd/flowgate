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

func TestParseRuleItems(t *testing.T) {
	input := []string{
		"*.google.com",
		".github.com",
		"openai.com",
		"https://anthropic.com/v1/models",
		"1.1.1.1",
		"2606:4700:4700::1111",
		"192.168.1.0/24",
		"http://10.0.0.1:8080/test",
	}

	domains, suffixes, ipCIDRs := parseRuleItems(input)

	// Check domain & suffixes
	expectedDomains := []string{"google.com", "github.com", "openai.com", "anthropic.com"}
	for _, exp := range expectedDomains {
		found := false
		for _, d := range domains {
			if d == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected domain %s in %v", exp, domains)
		}
	}

	// Check IP / CIDR
	expectedCIDRs := []string{"1.1.1.1/32", "2606:4700:4700::1111/128", "192.168.1.0/24", "10.0.0.1/32"}
	for _, exp := range expectedCIDRs {
		found := false
		for _, c := range ipCIDRs {
			if c == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected CIDR %s in %v", exp, ipCIDRs)
		}
	}
	_ = suffixes
}

func TestCustomListPrecedence(t *testing.T) {
	app := cfg.DefaultApp()
	app.Route.DirectList = []string{"10.0.0.0/8", "*.corp.local"}
	app.Route.ProxyList = []string{"*.openai.com", "8.8.8.8"}

	routeRules := buildRouteRules(app)

	// 规则 0, 1 应为 sniff 和 hijack-dns
	if routeRules[0].Action != "sniff" || routeRules[1].Action != "hijack-dns" {
		t.Fatalf("rules 0 and 1 must be sniff and hijack-dns")
	}

	// 规则 2, 3 必须是用户 DirectList (最高优先级 #1)
	if routeRules[2].Outbound != "direct" || routeRules[3].Outbound != "direct" {
		t.Fatalf("rules 2 and 3 must be direct, got %s and %s", routeRules[2].Outbound, routeRules[3].Outbound)
	}

	// 规则 4, 5 必须是用户 ProxyList (最高优先级 #2)
	if routeRules[4].Outbound != "warp" || routeRules[5].Outbound != "warp" {
		t.Fatalf("rules 4 and 5 must be warp, got %s and %s", routeRules[4].Outbound, routeRules[5].Outbound)
	}

	// 规则 6 必须是 IPIsPrivate
	if !routeRules[6].IPIsPrivate || routeRules[6].Outbound != "direct" {
		t.Fatalf("rule 6 must be IPIsPrivate direct, got %+v", routeRules[6])
	}
}
