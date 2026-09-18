// FlowGate —— 智能分流网关（单文件 CLI）
//
// 仅将 GFW 名单与开发站点流量经 Cloudflare WARP 转发（默认走官方 1.1.1.1
// 客户端的 MASQUE 隧道，可选内置 WireGuard 后端），
// 国内流量、P2P、普通浏览等其余流量一律直连。
// 内嵌更名后的 sing-box 核心（fg-core），支持 Windows / Linux 一键运行。
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"

	"flowgate/internal/cfg"
	"flowgate/internal/term"
)

// Version 当前版本。
const Version = "1.0.0"

var (
	flagHome      string
	flagYes       bool
	flagNoElevate bool
	flagPurge     bool
	flagRunSecs   int
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		term.Fail("%v", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--home" || a == "-home":
			if i+1 >= len(args) {
				return errors.New("--home 需要一个目录参数")
			}
			flagHome = args[i+1]
			i++
		case a == "--yes" || a == "-y":
			flagYes = true
		case a == "--no-elevate":
			flagNoElevate = true
		case a == "--purge":
			flagPurge = true
		case a == "--run-seconds":
			if i+1 >= len(args) {
				return errors.New("--run-seconds 需要一个秒数参数")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return errors.New("--run-seconds 需要一个正整数秒数")
			}
			flagRunSecs = n
			i++
		case a == "-h" || a == "--help" || a == "help":
			usage()
			return nil
		default:
			rest = append(rest, a)
		}
	}

	var cmd string
	var cmdArgs []string
	if len(rest) > 0 {
		cmd = rest[0]
		cmdArgs = rest[1:]
	}
	p := cfg.Resolve(flagHome)

	switch cmd {
	case "":
		return interactive(p)
	case "setup":
		return cmdSetup(p)
	case "run":
		return cmdRun(p)
	case "install":
		return cmdInstall(p)
	case "uninstall":
		return cmdUninstall(p)
	case "start":
		return cmdServiceCtl(p, "start")
	case "stop":
		return cmdServiceCtl(p, "stop")
	case "restart":
		return cmdServiceCtl(p, "restart")
	case "status":
		return cmdStatus(p)
	case "logs":
		return cmdLogs(p, cmdArgs)
	case "doctor":
		return cmdDoctor(p)
	case "update-rules":
		return cmdUpdateRules(p, cmdArgs)
	case "update":
		return cmdUpdate(p)
	case "warp-scan":
		return cmdWarpScan(p)
	case "version":
		fmt.Printf("flowgate %s (%s/%s, core: sing-box 1.14.1)\n", Version, runtime.GOOS, runtime.GOARCH)
		return nil
	case "--svc":
		return runService()
	default:
		usage()
		return fmt.Errorf("未知命令: %s", cmd)
	}
}

func banner(p cfg.Paths) {
	term.Plain("")
	term.Plain("  ┌─┌─┐┌─┐┌┬┐┌┬┐┬ ┬┬┌─┐  FlowGate v%s", Version)
	term.Plain("  ├─└─┐├┤  │  │ │ ││└─┐  GFW 流量 → WARP，其余全部直连")
	term.Plain("  ┴ └─┘└─┘ ┴  ┴ └─┘┴└─┘")
	term.Plain("")
	term.Dim("  数据目录: %s", p.DataDir)
	term.Dim("  配置目录: %s", p.ConfigDir)
	term.Plain("")
	showQuickStatus(p)
	term.Plain("")
}

func usage() {
	term.Plain("用法: flowgate <命令> [参数]（不带参数运行进入交互控制台）")
	term.Plain("")
	term.Plain("命令:")
	term.Plain("  setup          初始化：释放核心、准备 WARP 上游、生成并校验配置")
	term.Plain("  run            前台运行（首次自动初始化；需要管理员/root）")
	term.Plain("  install        安装为系统服务并启动（开机自启、崩溃自动重启）")
	term.Plain("  uninstall      停止并卸载系统服务（--purge 同时删除数据）")
	term.Plain("  start/stop     启动 / 停止服务")
	term.Plain("  restart        重启服务")
	term.Plain("  status         查看服务、核心与两条链路的状态")
	term.Plain("  logs [-f] [-n] 查看核心日志（-f 跟随，-n 行数，默认 50）")
	term.Plain("  doctor         全面自检")
	term.Plain("  update         一键更新：刷新分流规则 + 升级官方 WARP 客户端")
	term.Plain("  update-rules   仅刷新分流规则（--online 从镜像在线更新）")
	term.Plain("  warp-scan      扫描可用接入点并择优（仅 wireguard 后端）")
	term.Plain("  version        版本信息")
	term.Plain("")
	term.Plain("上游后端（config.json 中 warp.backend）:")
	term.Plain("  client     官方 1.1.1.1 客户端（MASQUE 隧道，本地 SOCKS5，默认）")
	term.Plain("  wireguard  内置 WireGuard 直连 WARP（单文件、无外部依赖）")
	term.Plain("")
	term.Plain("全局参数:")
	term.Plain("  --home DIR     指定数据根目录（默认 Windows: %%ProgramData%%\\FlowGate, Linux: /etc/flowgate 等）")
	term.Plain("  --yes / -y     跳过确认")
	term.Plain("")
}
