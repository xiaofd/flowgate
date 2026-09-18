// Package wclient 管理官方 Cloudflare WARP 客户端（1.1.1.1）：
// 检测、安装、配置为本地代理模式（MASQUE 隧道 + SOCKS5 127.0.0.1:40000）。
package wclient

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// FindCLI 定位 warp-cli 可执行文件。
func FindCLI() (string, error) {
	if p, err := exec.LookPath("warp-cli"); err == nil {
		return p, nil
	}
	for _, p := range cliCandidates() {
		if st, err := osStat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("未找到 warp-cli（官方 WARP 客户端未安装）")
}

// Installed 官方客户端是否已安装。
func Installed() bool {
	_, err := FindCLI()
	return err == nil
}

// Run 执行 warp-cli 子命令，返回合并输出（带超时防止服务卡死时挂起）。
func Run(cli string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cli, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Configure 将客户端配置为代理模式并连接（幂等）。
func Configure(cli string) error {
	// 1. 注册（已注册时报错，忽略）
	_, err := Run(cli, "--accept-tos", "registration", "new")
	if err != nil {
		_, _ = Run(cli, "--accept-tos", "register") // 旧版语法
	}
	// 2. 切换到本地代理模式（不接管系统路由，由 fg-core 的 TUN 负责）
	if _, err := Run(cli, "--accept-tos", "mode", "proxy"); err != nil {
		if _, err2 := Run(cli, "--accept-tos", "set-mode", "proxy"); err2 != nil {
			return fmt.Errorf("设置代理模式失败: %w", err)
		}
	}
	// 3. 尽力强制 MASQUE 隧道协议（不支持该子命令的版本忽略）
	_, _ = Run(cli, "--accept-tos", "tunnel", "protocol", "set", "MASQUE")
	// 4. 连接
	if _, err := Run(cli, "--accept-tos", "connect"); err != nil {
		return fmt.Errorf("warp-cli connect 失败: %w", err)
	}
	return nil
}

// Status 返回 (是否已连接, 状态描述)。
func Status(cli string) (bool, string) {
	out, err := Run(cli, "status")
	if err != nil {
		return false, strings.TrimSpace(out)
	}
	return strings.Contains(out, "Connected"), strings.TrimSpace(out)
}

// EnsureConnected 确保客户端处于连接状态。
func EnsureConnected(cli string) error {
	if ok, _ := Status(cli); ok {
		return nil
	}
	_, err := Run(cli, "--accept-tos", "connect")
	return err
}

// ProxyReady 本地 SOCKS 端口是否可拨。
func ProxyReady(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ProxyHandshake 验证本地端口是可用的 SOCKS5 代理（完整握手，区分被其他程序占用的情况）。
func ProxyHandshake(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	// SOCKS5 问候: VER=5, 1 个方法, 无认证
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return false
	}
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return false
	}
	return buf[0] == 0x05 && buf[1] == 0x00
}

// FreePort 向操作系统申请一个空闲 TCP 端口。
func FreePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// EnsureProxy 确保本地 SOCKS5 代理真正可用；若端口被其他程序占用则自动
// 换随机端口并重连，返回实际可用的代理地址。
func EnsureProxy(cli, current string) (string, error) {
	// 仅当客户端已连接且 SOCKS5 握手成功时才信任当前端口
	// （防止其他程序占用端口且恰好也是 SOCKS5 服务时误用）
	if ok, _ := Status(cli); ok && ProxyHandshake(current) {
		return current, nil
	}
	// 端口被外部程序占用（TCP 可拨但非 SOCKS5）或未就绪 → 换随机端口
	var lastErr error
	for i := 0; i < 3; i++ {
		port := FreePort()
		if port == 0 {
			continue
		}
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		if _, err := Run(cli, "--accept-tos", "proxy", "port", strconv.Itoa(port)); err != nil {
			_, _ = Run(cli, "--accept-tos", "set-proxy-port", strconv.Itoa(port)) // 旧版语法
		}
		if _, err := Run(cli, "--accept-tos", "connect"); err != nil {
			lastErr = err
			continue
		}
		if WaitForProxy(addr, 15*time.Second) && ProxyHandshake(addr) {
			return addr, nil
		}
		lastErr = fmt.Errorf("端口 %s 握手失败", addr)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("无法分配可用的本地代理端口")
	}
	return "", lastErr
}

// Version 返回客户端版本描述。
func Version(cli string) string {
	out, err := Run(cli, "--version")
	if err != nil || out == "" {
		return "未知版本"
	}
	if i := strings.Index(out, "\n"); i >= 0 {
		out = out[:i]
	}
	return out
}

// WaitForProxy 等待本地代理端口就绪。
func WaitForProxy(addr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ProxyReady(addr) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}
