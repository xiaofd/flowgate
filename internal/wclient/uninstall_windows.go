//go:build windows

package wclient

import (
	"fmt"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Uninstall 通过注册表定位官方客户端的 MSI 卸载入口并静默卸载（需要管理员权限）。
func Uninstall() error {
	bases := []string{
		`SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`,
		`SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`,
	}
	for _, base := range bases {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, base,
			registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		names, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, name := range names {
			sk, err := registry.OpenKey(registry.LOCAL_MACHINE, base+`\`+name, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			dn, _, _ := sk.GetStringValue("DisplayName")
			us, _, usErr := sk.GetStringValue("UninstallString")
			sk.Close()
			if !strings.Contains(dn, "Cloudflare WARP") || usErr != nil {
				continue
			}
			// UninstallString 形如 "MsiExec.exe /I{GUID}"
			start := strings.Index(us, "{")
			end := strings.LastIndex(us, "}")
			if start < 0 || end <= start {
				continue
			}
			product := us[start : end+1]
			cmd := exec.Command("msiexec", "/x", product, "/qn", "/norestart")
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("卸载官方客户端失败: %w\n%s", err, strings.TrimSpace(string(out)))
			}
			return nil
		}
	}
	return fmt.Errorf("注册表中未找到 Cloudflare WARP 的卸载信息")
}
