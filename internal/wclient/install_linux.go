//go:build linux

package wclient

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func cliCandidates() []string {
	return []string{"/usr/bin/warp-cli", "/usr/local/bin/warp-cli"}
}

var osStat = os.Stat

// Install 通过 Cloudflare 官方软件源安装（需要 root）。
func Install(print func(string, ...any)) error {
	switch {
	case lookup("apt-get"):
		return installApt(print)
	case lookup("dnf"):
		return installDnf(print)
	case lookup("yum"):
		return installYum(print)
	}
	return fmt.Errorf("未识别的包管理器，请手动安装官方 WARP 客户端: https://pkg.cloudflareclient.com/")
}

func lookup(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func sh(print func(string, ...any), name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s 失败: %w\n%s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func installApt(print func(string, ...any)) error {
	print("添加 Cloudflare 软件源（apt）...")
	if err := sh(print, "bash", "-c",
		"curl -fsSL https://pkg.cloudflareclient.com/pubkey.gpg | gpg --yes --dearmor -o /usr/share/keyrings/cloudflare-warp-archive-keyring.gpg"); err != nil {
		return err
	}
	if err := sh(print, "bash", "-c",
		`echo "deb [signed-by=/usr/share/keyrings/cloudflare-warp-archive-keyring.gpg] https://pkg.cloudflareclient.com/ $(lsb_release -cs) main" > /etc/apt/sources.list.d/cloudflare-client.list`); err != nil {
		return err
	}
	print("安装 cloudflare-warp ...")
	if err := sh(print, "apt-get", "update", "-y"); err != nil {
		return err
	}
	return sh(print, "apt-get", "install", "-y", "cloudflare-warp")
}

func installDnf(print func(string, ...any)) error {
	print("添加 Cloudflare 软件源（dnf）...")
	if err := sh(print, "bash", "-c",
		`echo -e "[cloudflare-warp]\nname=Cloudflare WARP\nbaseurl=https://pkg.cloudflareclient.com/\$basearch\nenabled=1\ntype=rpm\ngpgcheck=1\ngpgkey=https://pkg.cloudflareclient.com/pubkey.gpg" > /etc/yum.repos.d/cloudflare-warp.repo`); err != nil {
		return err
	}
	return sh(print, "dnf", "-y", "install", "cloudflare-warp")
}

func installYum(print func(string, ...any)) error {
	print("添加 Cloudflare 软件源（yum）...")
	if err := sh(print, "bash", "-c",
		`echo -e "[cloudflare-warp]\nname=Cloudflare WARP\nbaseurl=https://pkg.cloudflareclient.com/\$basearch\nenabled=1\ntype=rpm\ngpgcheck=1\ngpgkey=https://pkg.cloudflareclient.com/pubkey.gpg" > /etc/yum.repos.d/cloudflare-warp.repo`); err != nil {
		return err
	}
	return sh(print, "yum", "-y", "install", "cloudflare-warp")
}
