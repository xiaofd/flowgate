//go:build linux

package wclient

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
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

// shStream 执行系统命令，将输出实时回显到标准输出/错误，避免用户在终端看到“假死”现象。
// 同时在内存中缓冲输出，以便在出错时提供完整的失败日志。
func shStream(print func(string, ...any), name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &buf)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s 失败: %w\n%s", name, err, strings.TrimSpace(buf.String()))
	}
	return nil
}

// getAptCodename 从 /etc/os-release 安全读取系统发行版代号，无需依赖外部 lsb_release 命令。
func getAptCodename() string {
	b, err := os.ReadFile("/etc/os-release")
	if err == nil {
		lines := strings.Split(string(b), "\n")
		var versionCodename, ubuntuCodename string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "VERSION_CODENAME=") {
				versionCodename = strings.Trim(strings.TrimPrefix(line, "VERSION_CODENAME="), `"'`)
			} else if strings.HasPrefix(line, "UBUNTU_CODENAME=") {
				ubuntuCodename = strings.Trim(strings.TrimPrefix(line, "UBUNTU_CODENAME="), `"'`)
			}
		}
		if versionCodename != "" {
			return versionCodename
		}
		if ubuntuCodename != "" {
			return ubuntuCodename
		}
	}

	// 备选尝试 lsb_release -cs
	if out, err := exec.Command("lsb_release", "-cs").Output(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return s
		}
	}

	// 尝试 /etc/debian_version
	if b, err := os.ReadFile("/etc/debian_version"); err == nil {
		s := strings.TrimSpace(string(b))
		if strings.HasPrefix(s, "12") {
			return "bookworm"
		} else if strings.HasPrefix(s, "11") {
			return "bullseye"
		} else if strings.HasPrefix(s, "10") {
			return "buster"
		}
	}

	// 最终合理兜底
	return "bookworm"
}

// saveCloudflareKey 纯 Go 下载并解析 Cloudflare GPG 公钥，完全不依赖外部 gpg 命令行工具。
func saveCloudflareKey(print func(string, ...any)) error {
	_ = os.MkdirAll("/usr/share/keyrings", 0o755)
	keyURL := "https://pkg.cloudflareclient.com/pubkey.gpg"

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, keyURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "curl/8.5.0")

	resp, err := client.Do(req)
	var keyData []byte
	if err == nil && resp.StatusCode == http.StatusOK {
		keyData, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
	} else {
		// HTTP 请求失败时尝试 curl 兜底
		if resp != nil {
			resp.Body.Close()
		}
		out, err2 := exec.Command("curl", "-fsSL", keyURL).Output()
		if err2 != nil {
			return fmt.Errorf("下载 Cloudflare 软件源公钥失败: %v / %v", err, err2)
		}
		keyData = out
	}

	// 将 OpenPGP ASCII Armor 解码为二进制 GPG Keyring
	binaryKey, dearmorErr := dearmorOpenPGP(keyData)
	dstGpg := "/usr/share/keyrings/cloudflare-warp-archive-keyring.gpg"
	dstAsc := "/usr/share/keyrings/cloudflare-warp-archive-keyring.asc"

	// 无论二进制解密是否成功，均写出供新旧 apt 调用的 keyring 文件
	if dearmorErr == nil && len(binaryKey) > 0 {
		_ = os.WriteFile(dstGpg, binaryKey, 0o644)
	} else {
		_ = os.WriteFile(dstGpg, keyData, 0o644)
	}
	_ = os.WriteFile(dstAsc, keyData, 0o644)

	return nil
}

// dearmorOpenPGP 解析 ASCII-armored PGP 公钥块并提取二进制内容。
func dearmorOpenPGP(data []byte) ([]byte, error) {
	s := string(data)
	if !strings.Contains(s, "-----BEGIN PGP PUBLIC KEY BLOCK-----") {
		return data, nil
	}
	lines := strings.Split(s, "\n")
	var b64 strings.Builder
	inBody := false
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if !inBody {
			if l == "" {
				inBody = true
			}
			continue
		}
		if strings.HasPrefix(l, "=") || strings.HasPrefix(l, "-----END") {
			break
		}
		b64.WriteString(l)
	}
	return base64.StdEncoding.DecodeString(b64.String())
}

func installApt(print func(string, ...any)) error {
	print("准备基础环境（ca-certificates、curl）...")
	_ = shStream(print, "apt-get", "install", "-y", "--no-install-recommends", "ca-certificates", "curl")

	print("配置 Cloudflare 软件源公钥...")
	if err := saveCloudflareKey(print); err != nil {
		return err
	}

	codename := getAptCodename()
	listContent := fmt.Sprintf("deb [signed-by=/usr/share/keyrings/cloudflare-warp-archive-keyring.gpg] https://pkg.cloudflareclient.com/ %s main\n", codename)
	if err := os.WriteFile("/etc/apt/sources.list.d/cloudflare-client.list", []byte(listContent), 0o644); err != nil {
		return fmt.Errorf("写入软件源列表失败: %w", err)
	}

	print("更新软件源 (apt-get update) ...")
	if err := shStream(print, "apt-get", "update", "-y"); err != nil {
		return err
	}

	print("安装 cloudflare-warp（实时显示下载与安装进度）...")
	return shStream(print, "apt-get", "install", "-y", "cloudflare-warp")
}

func installDnf(print func(string, ...any)) error {
	print("添加 Cloudflare 软件源（dnf）...")
	repo := "[cloudflare-warp]\nname=Cloudflare WARP\nbaseurl=https://pkg.cloudflareclient.com/$basearch\nenabled=1\ntype=rpm\ngpgcheck=1\ngpgkey=https://pkg.cloudflareclient.com/pubkey.gpg\n"
	if err := os.WriteFile("/etc/yum.repos.d/cloudflare-warp.repo", []byte(repo), 0o644); err != nil {
		return err
	}
	print("安装 cloudflare-warp（实时显示进度）...")
	return shStream(print, "dnf", "-y", "install", "cloudflare-warp")
}

func installYum(print func(string, ...any)) error {
	print("添加 Cloudflare 软件源（yum）...")
	repo := "[cloudflare-warp]\nname=Cloudflare WARP\nbaseurl=https://pkg.cloudflareclient.com/$basearch\nenabled=1\ntype=rpm\ngpgcheck=1\ngpgkey=https://pkg.cloudflareclient.com/pubkey.gpg\n"
	if err := os.WriteFile("/etc/yum.repos.d/cloudflare-warp.repo", []byte(repo), 0o644); err != nil {
		return err
	}
	print("安装 cloudflare-warp（实时显示进度）...")
	return shStream(print, "yum", "-y", "install", "cloudflare-warp")
}
