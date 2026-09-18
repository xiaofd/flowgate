//go:build linux

package wclient

import (
	"fmt"
	"os/exec"
	"strings"
)

// Upgrade 通过包管理器升级官方客户端（需要 root）。
func Upgrade(cli string) (string, error) {
	var cmd *exec.Cmd
	switch {
	case lookup("apt-get"):
		cmd = exec.Command("apt-get", "install", "-y", "--only-upgrade", "cloudflare-warp")
	case lookup("dnf"):
		cmd = exec.Command("dnf", "update", "-y", "cloudflare-warp")
	case lookup("yum"):
		cmd = exec.Command("yum", "update", "-y", "cloudflare-warp")
	default:
		return "", fmt.Errorf("未识别的包管理器，请手动升级 cloudflare-warp 包")
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("升级失败: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return "官方客户端已升级（" + Version(cli) + "）", nil
}
