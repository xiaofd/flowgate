//go:build linux

package wclient

import (
	"fmt"
	"os/exec"
	"strings"
)

// Uninstall 通过包管理器卸载官方客户端（需要 root）。
func Uninstall() error {
	for _, pm := range [][2]string{
		{"apt-get", "remove"},
		{"dnf", "remove"},
		{"yum", "remove"},
	} {
		if _, err := exec.LookPath(pm[0]); err == nil {
			cmd := exec.Command(pm[0], pm[1], "-y", "cloudflare-warp")
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("卸载官方客户端失败: %w\n%s", err, strings.TrimSpace(string(out)))
			}
			return nil
		}
	}
	return fmt.Errorf("未识别的包管理器，请手动卸载 cloudflare-warp 包")
}
