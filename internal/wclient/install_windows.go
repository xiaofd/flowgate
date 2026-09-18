//go:build windows

package wclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func cliCandidates() []string {
	return []string{
		filepath.Join(`C:\Program Files (x86)\Cloudflare\Cloudflare WARP`, "warp-cli.exe"),
		filepath.Join(`C:\Program Files\Cloudflare\Cloudflare WARP`, "warp-cli.exe"),
	}
}

var osStat = os.Stat

// 官方安装包下载地址（主/备）
const (
	msiURL    = "https://downloads.cloudflareclient.com/v1/download/windows/ga"
	msiURLAlt = "https://1111-releases.cloudflareclient.com/windows/Cloudflare_WARP_Release-x64.msi"
)

// Install 下载并静默安装官方 WARP 客户端（需要管理员权限）。
func Install(print func(string, ...any)) error {
	tmp := filepath.Join(os.TempDir(), "Cloudflare_WARP_x64.msi")
	print("下载官方 WARP 客户端安装包（约 70MB）...")
	if err := downloadFile(msiURL, tmp); err != nil {
		print("主下载地址失败（%v），尝试备用地址 ...", err)
		if err2 := downloadFile(msiURLAlt, tmp); err2 != nil {
			return fmt.Errorf("下载安装包失败: %v / %v", err, err2)
		}
	}
	defer os.Remove(tmp)
	print("静默安装官方 WARP 客户端（约 1-2 分钟）...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "msiexec", "/i", tmp, "/qn", "/norestart")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("安装失败: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func downloadFile(url, dst string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}
