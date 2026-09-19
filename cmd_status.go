package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/kardianos/service"

	"flowgate/internal/assets"
	"flowgate/internal/cfg"
	"flowgate/internal/elevate"
	"flowgate/internal/netcheck"
	"flowgate/internal/sbx"
	"flowgate/internal/svc"
	"flowgate/internal/term"
	"flowgate/internal/warp"
	"flowgate/internal/wclient"
)

// requireAdmin 确保管理员/root 权限，否则自动提权重放当前命令。
// 交互模式下（双击运行）os.Args 为空，需用 interactiveCmd 重放指定命令，
// 避免提权窗口再次进入交互模式。
func requireAdmin() error {
	if elevate.IsAdmin() {
		return nil
	}
	if flagNoElevate {
		return fmt.Errorf("需要管理员/root 权限（当前已通过 --no-elevate 禁止自动提权）")
	}
	full := os.Args[1:]
	if interactiveCmd != "" {
		full = []string{interactiveCmd}
		if flagPurge {
			full = append(full, "--purge")
		}
		if flagHome != "" {
			full = append([]string{"--home", flagHome}, full...)
		}
	}
	term.Info("需要管理员/root 权限，正在请求提权 ...")
	return elevate.RelaunchElevatedAndWait(full)
}

// cmdSetup 初始化。
func cmdSetup(p cfg.Paths) error {
	term.Step("FlowGate 初始化")
	// Linux 配置目录位于 /etc，直接要求 root；Windows 写 ProgramData 无需提权，
	// 但自动安装官方 WARP 客户端（msiexec）需要管理员权限
	if runtime.GOOS != "windows" {
		if err := requireAdmin(); err != nil {
			return err
		}
	} else if app, _ := cfg.LoadApp(p.AppConfig); app.Warp.Backend != cfg.BackendWireguard && !wclient.Installed() {
		if err := requireAdmin(); err != nil {
			return err
		}
	}
	if err := ensureSetup(p); err != nil {
		return err
	}
	term.Ok("初始化完成")
	term.Plain("")
	term.Dim("  前台体验: flowgate run")
	term.Dim("  安装服务: flowgate install（开机自启）")
	return nil
}

func ensureSetup(p cfg.Paths) error {
	return setupEnsure(p)
}

// cmdRun 前台运行。
func cmdRun(p cfg.Paths) error {
	if err := requireAdmin(); err != nil {
		return err
	}
	term.Step("检查运行环境 ...")
	if err := setupEnsure(p); err != nil {
		return err
	}
	lg := newForegroundLogger(p)
	core := sbx.NewCore(p, lg, true)

	go func() {
		// 等核心起来后做一次链路自检
		time.Sleep(3 * time.Second)
		if _, running := sbx.RunningPid(p.PidFile); running {
			reportConnectivity()
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		term.Warn("收到停止信号，正在停止核心 ...")
		core.Shutdown()
	}()

	// 定时自停（冒烟测试用）
	if flagRunSecs > 0 {
		go func() {
			time.Sleep(time.Duration(flagRunSecs) * time.Second)
			term.Warn("定时运行 %d 秒已到，正在停止核心 ...", flagRunSecs)
			core.Shutdown()
		}()
	}

	term.Ok("核心已启动，前台运行中（Ctrl+C 停止）")
	err := core.Run()
	term.Info("核心已退出")
	return err
}

// cmdInstall 安装系统服务。
func cmdInstall(p cfg.Paths) error {
	if !flagYes && !elevate.IsAdmin() {
		if !term.Confirm("将安装系统服务 FlowGate（开机自启），是否继续") {
			return nil
		}
	}
	if err := requireAdmin(); err != nil {
		return err
	}
	term.Step("准备运行环境 ...")
	if err := setupEnsure(p); err != nil {
		return err
	}
	term.Step("安装系统服务 ...")
	s, err := svc.New(flagHome)
	if err != nil {
		return err
	}
	if err := s.Install(); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already exists") ||
			strings.Contains(err.Error(), "已存在") {
			term.Warn("服务已存在，跳过安装")
		} else {
			return fmt.Errorf("安装服务失败: %w", err)
		}
	}
	if err := svc.PostInstall(); err != nil {
		term.Warn("设置崩溃恢复策略失败: %v", err)
	}
	if err := s.Start(); err != nil {
		return fmt.Errorf("启动服务失败: %w", err)
	}
	term.Ok("服务已安装并启动（开机自启、崩溃自动重启）")
	term.Plain("")
	time.Sleep(2 * time.Second)
	return cmdStatus(p)
}

// cmdUninstall 卸载系统服务。
func cmdUninstall(p cfg.Paths) error {
	if !flagYes && !term.Confirm("将停止并卸载 FlowGate 服务，是否继续") {
		return nil
	}
	if err := requireAdmin(); err != nil {
		return err
	}
	s, err := svc.New(flagHome)
	if err != nil {
		return err
	}
	_ = s.Stop()
	if err := s.Uninstall(); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not exist") {
			term.Warn("服务未安装")
		} else {
			return fmt.Errorf("卸载服务失败: %w", err)
		}
	} else {
		term.Ok("服务已卸载")
	}
	if flagPurge {
		if !term.Confirm("确认删除全部数据（配置、WARP 账号、核心）") {
			return nil
		}
		for _, d := range []string{p.ConfigDir, p.DataDir, p.LogDir} {
			if d == "" || d == "/" {
				continue
			}
			_ = os.RemoveAll(d)
		}
		term.Ok("数据已清除")
		// 官方 WARP 客户端为系统级产品，单独确认后一并卸载
		if wclient.Installed() && term.Confirm("是否同时卸载官方 WARP 客户端（Cloudflare WARP）") {
			if err := wclient.Uninstall(); err != nil {
				term.Warn("卸载官方客户端失败: %v", err)
			} else {
				term.Ok("官方 WARP 客户端已卸载")
			}
		}
	}
	return nil
}

// cmdServiceCtl 服务启停。
func cmdServiceCtl(p cfg.Paths, action string) error {
	if err := requireAdmin(); err != nil {
		return err
	}
	s, err := svc.New(flagHome)
	if err != nil {
		return err
	}
	var ctlErr error
	switch action {
	case "start":
		ctlErr = s.Start()
	case "stop":
		ctlErr = s.Stop()
	case "restart":
		ctlErr = s.Restart()
	}
	if ctlErr != nil {
		return fmt.Errorf("服务 %s 失败: %w", action, ctlErr)
	}
	term.Ok("服务已执行: %s", action)
	if action != "stop" {
		time.Sleep(2 * time.Second)
		return cmdStatus(p)
	}
	return nil
}

// runService 由系统服务管理器调用（--svc）。
func runService() error {
	s, err := svc.New(flagHome)
	if err != nil {
		return err
	}
	return s.Run()
}

// showQuickStatus 横幅中的简要状态。
func showQuickStatus(p cfg.Paths) {
	app, _ := cfg.LoadApp(p.AppConfig)
	_, coreRunning := sbx.RunningPid(p.PidFile)
	s, _ := svc.New(flagHome)
	st, serr := s.Status()
	term.KV("服务状态", serviceStatusText(st, serr))
	if coreRunning {
		term.KV("核心进程", "运行中")
	} else {
		term.KV("核心进程", "未运行")
	}
	term.KV("TUN 接口", tunStateText(tunPresent(app.Tun.Name), app.Tun.Name))
}

// cmdStatus 状态总览。
func cmdStatus(p cfg.Paths) error {
	app, _ := cfg.LoadApp(p.AppConfig)

	s, _ := svc.New(flagHome)
	st, serr := s.Status()
	svcLine := serviceStatusText(st, serr)

	pid, coreRunning := sbx.RunningPid(p.PidFile)
	tunUp := tunPresent(app.Tun.Name)

	term.Step("FlowGate 状态")
	term.KV("系统服务", svcLine)
	if coreRunning {
		term.KV("核心进程", fmt.Sprintf("fg-core (PID %d) 运行中", pid))
	} else {
		term.KV("核心进程", "未运行")
	}
	term.KV("TUN 接口", tunStateText(tunUp, app.Tun.Name))
	term.KV("上游后端", backendText(app))
	if app.Warp.Backend == cfg.BackendWireguard {
		if acc, _ := warp.Load(p.WarpAccount); acc != nil {
			term.KV("WARP 账号", "已注册（"+acc.IPv4+"）")
		} else {
			term.KV("WARP 账号", "未注册（执行 flowgate setup）")
		}
	} else if cli, err := wclient.FindCLI(); err == nil {
		if ok, desc := wclient.Status(cli); ok {
			term.KV("WARP 客户端", "已连接（MASQUE）")
		} else {
			term.KV("WARP 客户端", "未连接: "+firstLine(desc))
		}
	} else {
		term.KV("WARP 客户端", "未安装（执行 flowgate setup）")
	}
	term.Plain("")
	if coreRunning {
		reportConnectivity()
		term.Plain("")
		term.Dim("  分流策略: GFW 名单 → WARP | 国内/P2P/其余 → 直连")
		term.Dim("  查看日志: flowgate logs -f")
	}
	return nil
}

func backendText(app cfg.App) string {
	if app.Warp.Backend == cfg.BackendWireguard {
		return "内置 WireGuard（" + app.Warp.Endpoint + "）"
	}
	return "官方客户端 MASQUE（" + app.Warp.ClientProxy + "）"
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func serviceStatusText(st service.Status, err error) string {
	if err != nil {
		return "未安装"
	}
	switch st {
	case service.StatusRunning:
		return "运行中"
	case service.StatusStopped:
		return "已停止"
	default:
		return "未知"
	}
}

func tunStateText(up bool, name string) string {
	if up {
		return name + " 已启用"
	}
	return name + " 未启用"
}

func tunPresent(name string) bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, i := range ifaces {
		if i.Name == name {
			return true
		}
	}
	return false
}

// reportConnectivity 探测直连与 WARP 两条链路。
func reportConnectivity() {
	d := netcheck.CheckDirect()
	if d.OK {
		term.KV("直连链路", fmt.Sprintf("正常（%v）", d.Latency))
	} else {
		term.KV("直连链路", "异常: "+errText(d.Err))
	}
	w := netcheck.CheckWarp()
	if w.OK {
		term.KV("WARP 链路", fmt.Sprintf("正常（%v，GFW 流量经 WARP）", w.Latency))
	} else {
		term.KV("WARP 链路", "异常: "+errText(w.Err))
		if app, _ := cfg.LoadApp(cfg.Resolve(flagHome).AppConfig); app.Warp.Backend == cfg.BackendWireguard {
			term.Dim("  提示: 执行 flowgate warp-scan 可扫描备用接入点")
		} else {
			term.Dim("  提示: 执行 warp-cli status 检查官方客户端，或 flowgate doctor 全面自检")
		}
	}
	dev := netcheck.CheckDev()
	if dev.OK {
		term.KV("开发加速", fmt.Sprintf("正常（GitHub API %v）", dev.Latency))
	} else {
		term.KV("开发加速", "检测中/暂不可达（"+errText(dev.Err)+"）")
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 80 {
		msg = msg[:80] + "..."
	}
	return msg
}

// cmdDoctor 全面自检。
func cmdDoctor(p cfg.Paths) error {
	term.Step("FlowGate 自检")
	app, _ := cfg.LoadApp(p.AppConfig)

	// 1. 权限
	if elevate.IsAdmin() {
		term.Ok("权限: 管理员/root")
	} else {
		term.Warn("权限: 普通用户（run/install 需要提权）")
	}

	// 2. 平台与核心
	term.Ok("平台: %s/%s", runtime.GOOS, runtime.GOARCH)
	if !assets.CoreAvailable() {
		term.Fail("核心: 当前平台无内嵌核心")
	} else if _, err := os.Stat(p.CorePath); err == nil {
		out, err := sbx.RunCoreVersion(p)
		if err == nil && out != "" {
			term.Ok("核心: %s", out)
		} else {
			term.Ok("核心: fg-core 已就位")
		}
	} else {
		term.Warn("核心: 未释放（执行 flowgate setup）")
	}

	// 3. 规则集
	if names := assets.RuleNames(); len(names) > 0 {
		term.Ok("内嵌规则集: %s", strings.Join(names, ", "))
	} else {
		term.Fail("内嵌规则集: 缺失")
	}
	if _, err := os.Stat(p.RulesDir); err == nil {
		term.Ok("规则目录: %s", p.RulesDir)
	}

	// 4. WARP 上游
	if app.Warp.Backend == cfg.BackendWireguard {
		if acc, _ := warp.Load(p.WarpAccount); acc != nil {
			term.Ok("WARP 账号: 已注册（%s，注册于 %s）", acc.IPv4, acc.RegisteredAt.Format("2006-01-02"))
		} else {
			term.Warn("WARP 账号: 未注册")
		}
	} else {
		if cli, err := wclient.FindCLI(); err == nil {
			if ok, desc := wclient.Status(cli); ok {
				term.Ok("WARP 客户端: 已连接（MASQUE，%s）", app.Warp.ClientProxy)
			} else {
				term.Warn("WARP 客户端: %s", firstLine(desc))
			}
			term.Ok("客户端版本: %s", wclient.Version(cli))
			if wclient.ProxyReady(app.Warp.ClientProxy) {
				term.Ok("本地代理端口: %s 可用", app.Warp.ClientProxy)
			} else {
				term.Warn("本地代理端口: %s 不可拨", app.Warp.ClientProxy)
			}
		} else {
			term.Warn("WARP 客户端: 未安装（执行 flowgate setup 自动安装）")
		}
	}

	// 5. 配置校验
	if _, err := os.Stat(p.SBConfig); err == nil {
		if err := sbx.CheckConfig(p); err == nil {
			term.Ok("核心配置: 校验通过")
		} else {
			term.Fail("核心配置: %v", err)
		}
	} else {
		term.Warn("核心配置: 未生成")
	}

	// 6. TUN 支持
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/dev/net/tun"); err == nil {
			term.Ok("TUN 设备: /dev/net/tun 可用")
		} else {
			term.Fail("TUN 设备: /dev/net/tun 不存在（容器环境需启用 TUN）")
		}
	} else if _, err := os.Stat(p.WintunPath); err == nil {
		term.Ok("TUN 驱动: wintun.dll 已就位")
	} else {
		term.Warn("TUN 驱动: wintun.dll 未释放")
	}

	// 7. 服务状态
	s, _ := svc.New(flagHome)
	st, serr := s.Status()
	term.Ok("系统服务: %s", serviceStatusText(st, serr))

	// 8. 运行状态与连通性
	if pid, running := sbx.RunningPid(p.PidFile); running {
		term.Ok("核心进程: 运行中 (PID %d)", pid)
		reportConnectivity()
	} else {
		term.Warn("核心进程: 未运行（flowgate start 或 flowgate run）")
	}
	return nil
}

// cmdLogs 查看核心日志。
func cmdLogs(p cfg.Paths, args []string) error {
	follow := false
	n := 50
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-f" || args[i] == "--follow":
			follow = true
		case args[i] == "-n" && i+1 < len(args):
			fmt.Sscanf(args[i+1], "%d", &n)
			i++
		}
	}
	f, err := os.Open(p.CoreLog)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("暂无日志（核心尚未运行过）: %s", p.CoreLog)
		}
		return err
	}
	defer f.Close()

	// 输出末尾 n 行
	lines := tailLines(f, n)
	for _, l := range lines {
		term.Plain("%s", l)
	}
	if !follow {
		return nil
	}
	// 跟随模式
	for {
		buf := make([]byte, 32*1024)
		nr, err := f.Read(buf)
		if nr > 0 {
			os.Stdout.Write(buf[:nr])
		}
		if err == io.EOF {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if err != nil {
			return err
		}
	}
}

func tailLines(f *os.File, n int) []string {
	data, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// cmdUpdateRules 刷新规则集。
func cmdUpdateRules(p cfg.Paths, args []string) error {
	online := false
	for _, a := range args {
		if a == "--online" {
			online = true
		}
	}
	if err := p.EnsureDirs(); err != nil {
		return err
	}
	if online {
		term.Step("从镜像在线更新规则集 ...")
		if err := downloadRules(p.RulesDir); err != nil {
			term.Warn("在线更新失败: %v", err)
			term.Info("回退为内嵌规则集 ...")
			if _, err := assets.ExtractRules(p.RulesDir, true); err != nil {
				return err
			}
		} else {
			term.Ok("规则集已在线更新（保留下载的新版本）")
		}
	} else {
		term.Step("释放内嵌规则集 ...")
		written, err := assets.ExtractRules(p.RulesDir, true)
		if err != nil {
			return err
		}
		term.Ok("规则集已更新（%d 个文件）", len(written))
	}
	term.Dim("如服务正在运行，请执行 flowgate restart 生效")
	return nil
}

// cmdUpdate 一键更新：刷新分流规则 + 升级官方 WARP 客户端。
func cmdUpdate(p cfg.Paths) error {
	if err := cmdUpdateRules(p, nil); err != nil {
		return err
	}
	app, _ := cfg.LoadApp(p.AppConfig)
	if app.Warp.Backend != cfg.BackendWireguard {
		if cli, err := wclient.FindCLI(); err == nil {
			term.Step("检查官方 WARP 客户端更新 ...")
			msg, err := wclient.Upgrade(cli)
			if err != nil {
				term.Warn("%v", err)
			} else {
				term.Ok("%s", msg)
			}
		}
	}
	term.Dim("如服务正在运行，请执行 flowgate restart 生效")
	return nil
}
