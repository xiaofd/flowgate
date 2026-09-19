package cfg

import (
	"path/filepath"
	"testing"
)

func TestDefaultApp(t *testing.T) {
	app := DefaultApp()
	if app.Warp.Backend != BackendClient {
		t.Fatalf("expected backend %s, got %s", BackendClient, app.Warp.Backend)
	}
	if len(app.Tun.Address) == 0 || app.Tun.Address[0] != "198.18.0.1/30" {
		t.Fatalf("expected TUN address 198.18.0.1/30, got %v", app.Tun.Address)
	}
}

func TestLoadSaveAppCustomRoute(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	app := DefaultApp()
	app.Route.ProxyDomains = []string{"custom-ai.org", "docker.io"}
	app.Route.DirectDomains = []string{"corp.internal"}

	if err := SaveApp(path, app); err != nil {
		t.Fatalf("SaveApp failed: %v", err)
	}

	loaded, err := LoadApp(path)
	if err != nil {
		t.Fatalf("LoadApp failed: %v", err)
	}

	if len(loaded.Route.ProxyDomains) != 2 || loaded.Route.ProxyDomains[0] != "custom-ai.org" {
		t.Fatalf("unexpected ProxyDomains: %v", loaded.Route.ProxyDomains)
	}
	if len(loaded.Route.DirectDomains) != 1 || loaded.Route.DirectDomains[0] != "corp.internal" {
		t.Fatalf("unexpected DirectDomains: %v", loaded.Route.DirectDomains)
	}
	if loaded.Tun.Address[0] != "198.18.0.1/30" {
		t.Fatalf("unexpected Tun.Address: %v", loaded.Tun.Address)
	}
}
