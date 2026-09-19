//go:build linux

package wclient

import (
	"fmt"
)

// Upgrade 通过包管理器升级官方客户端（需要 root）。
func Upgrade(cli string) (string, error) {
	var err error
	switch {
	case lookup("apt-get"):
		err = shStream(nil, "apt-get", "install", "-y", "--only-upgrade", "cloudflare-warp")
	case lookup("dnf"):
		err = shStream(nil, "dnf", "update", "-y", "cloudflare-warp")
	case lookup("yum"):
		err = shStream(nil, "yum", "update", "-y", "cloudflare-warp")
	default:
		return "", fmt.Errorf("未识别的包管理器，请手动升级 cloudflare-warp 包")
	}
	if err != nil {
		return "", fmt.Errorf("升级失败: %w", err)
	}
	return "官方客户端已升级（" + Version(cli) + "）", nil
}
