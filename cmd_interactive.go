package main

import (
	"flowgate/internal/cfg"
	"flowgate/internal/term"
)

// interactiveCmd 交互模式下当前执行的命令名。
// requireAdmin 提权重放时用它替代原始 os.Args（双击场景 os.Args 为空，
// 直接重放会导致提权窗口再次进入交互模式而非执行指定命令）。
var interactiveCmd string

// interactive 无参数运行（如双击）时进入的中文交互控制台。
func interactive(p cfg.Paths) error {
	term.Plain("")
	term.Info("已进入交互模式（命令行参数方式见 flowgate --help）")
	for {
		banner(p)
		term.Plain("  ─────────────────────────────────────────────")
		term.Plain("   1. 初始化 / 修复环境      (setup)")
		term.Plain("   2. 前台运行               (run)")
		term.Plain("   3. 安装为系统服务         (install)")
		term.Plain("   4. 启动服务               (start)")
		term.Plain("   5. 停止服务               (stop)")
		term.Plain("   6. 重启服务               (restart)")
		term.Plain("   7. 查看状态               (status)")
		term.Plain("   8. 查看最近日志           (logs)")
		term.Plain("   9. 一键更新               (update)")
		term.Plain("  10. 全面自检               (doctor)")
		term.Plain("  11. 卸载服务 / 退出清理    (uninstall)")
		term.Plain("   0. 退出")
		term.Plain("  ─────────────────────────────────────────────")
		term.Plain("")
		term.Plain("  请输入编号并回车: ")

		choice, ok := term.ReadLine()
		if !ok {
			// 输入流结束（如管道输入耗尽）→ 退出
			term.Plain("")
			term.Info("已退出，再见")
			return nil
		}
		var err error
		switch choice {
		case "1":
			interactiveCmd = "setup"
			err = cmdSetup(p)
		case "2":
			interactiveCmd = "run"
			err = cmdRun(p)
		case "3":
			interactiveCmd = "install"
			err = cmdInstall(p)
		case "4":
			interactiveCmd = "start"
			err = cmdServiceCtl(p, "start")
		case "5":
			interactiveCmd = "stop"
			err = cmdServiceCtl(p, "stop")
		case "6":
			interactiveCmd = "restart"
			err = cmdServiceCtl(p, "restart")
		case "7":
			err = cmdStatus(p)
		case "8":
			err = cmdLogs(p, []string{"-n", "40"})
		case "9":
			err = cmdUpdate(p)
		case "10":
			err = cmdDoctor(p)
		case "11":
			interactiveCmd = "uninstall"
			err = cmdUninstall(p)
		case "0", "q", "Q", "exit", "quit", "退出":
			term.Plain("")
			term.Info("已退出，再见")
			return nil
		case "":
			continue // 直接回车 → 刷新菜单与状态
		default:
			term.Warn("无效选项: %s（请输入 0-11）", choice)
			continue
		}
		interactiveCmd = "" // 命令执行完毕，清除重放上下文
		if err != nil {
			term.Fail("%v", err)
		}
		term.Plain("")
		term.Pause()
	}
}
