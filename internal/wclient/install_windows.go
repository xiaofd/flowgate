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
	if err := downloadFile(msiURL, tmp, print); err != nil {
		print("主下载地址失败（%v），尝试备用地址 ...", err)
		if err2 := downloadFile(msiURLAlt, tmp, print); err2 != nil {
			return fmt.Errorf("下载安装包失败: %v / %v", err, err2)
		}
	}
	defer os.Remove(tmp)
	print("正在静默安装官方 WARP 客户端（约 1-2 分钟，请稍候）...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "msiexec", "/i", tmp, "/qn", "/norestart")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("安装失败: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

type progressReader struct {
	reader     io.Reader
	total      int64
	current    int64
	lastTime   time.Time
	lastBytes  int64
	print      func(string, ...any)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if n > 0 {
		pr.current += int64(n)
		now := time.Now()
		if pr.print != nil && (now.Sub(pr.lastTime) >= time.Second || err == io.EOF) {
			duration := now.Sub(pr.lastTime).Seconds()
			speed := float64(pr.current-pr.lastBytes) / duration / (1 << 20)
			pr.lastTime = now
			pr.lastBytes = pr.current
			if pr.total > 0 {
				pct := float64(pr.current) * 100 / float64(pr.total)
				pr.print("下载进度: %.1f MB / %.1f MB (%.1f%%)  速度: %.1f MB/s",
					float64(pr.current)/(1<<20), float64(pr.total)/(1<<20), pct, speed)
			} else {
				pr.print("已下载: %.1f MB  速度: %.1f MB/s", float64(pr.current)/(1<<20), speed)
			}
		}
	}
	return n, err
}

func downloadFile(url, dst string, print func(string, ...any)) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "curl/8.5.0")

	resp, err := client.Do(req)
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

	pr := &progressReader{
		reader:    resp.Body,
		total:     resp.ContentLength,
		lastTime:  time.Now(),
		print:     print,
	}
	_, err = io.Copy(f, pr)
	return err
}
