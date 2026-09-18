// Package term 提供统一的控制台输出样式（暗色友好、带状态符号）。
package term

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

const (
	cReset  = "\x1b[0m"
	cGreen  = "\x1b[32m"
	cRed    = "\x1b[31m"
	cYellow = "\x1b[33m"
	cCyan   = "\x1b[36m"
	cBold   = "\x1b[1m"
	cDim    = "\x1b[2m"
)

var enabled = true

// Disable 关闭 ANSI 颜色（用于非终端输出）。
func Disable() { enabled = false }

func color(c string) string {
	if enabled {
		return c
	}
	return ""
}

// Ok 输出成功信息
func Ok(format string, v ...any) {
	fmt.Printf("%s✔ %s%s\n", color(cGreen), fmt.Sprintf(format, v...), color(cReset))
}

// Fail 输出错误信息（stderr）
func Fail(format string, v ...any) {
	fmt.Fprintf(os.Stderr, "%s✘ %s%s\n", color(cRed), fmt.Sprintf(format, v...), color(cReset))
}

// Warn 输出警告信息
func Warn(format string, v ...any) {
	fmt.Printf("%s⚠ %s%s\n", color(cYellow), fmt.Sprintf(format, v...), color(cReset))
}

// Info 输出提示信息
func Info(format string, v ...any) {
	fmt.Printf("%sℹ %s%s\n", color(cCyan), fmt.Sprintf(format, v...), color(cReset))
}

// Step 输出步骤信息
func Step(format string, v ...any) {
	fmt.Printf("%s▸ %s%s\n", color(cBold), fmt.Sprintf(format, v...), color(cReset))
}

// Dim 输出弱化信息
func Dim(format string, v ...any) {
	fmt.Printf("%s%s%s\n", color(cDim), fmt.Sprintf(format, v...), color(cReset))
}

// Plain 原样输出一行
func Plain(format string, v ...any) {
	fmt.Printf(format+"\n", v...)
}

// KV 输出 "  名称: 值" 形式的状态行（按显示宽度对齐，兼容中文）。
func KV(k string, v any) {
	w := displayWidth(k + ":")
	pad := 18 - w
	if pad < 1 {
		pad = 1
	}
	fmt.Printf("  %s%s%s%s%s\n",
		color(cDim), k+":", strings.Repeat(" ", pad), color(cReset), fmt.Sprint(v))
}

// displayWidth 估算字符串的终端显示宽度（CJK 字符按 2 列计）。
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if r >= 0x1100 && (r <= 0x115F || // Hangul Jamo
			r >= 0x2E80 && r <= 0xA4CF || // CJK 部首/康熙/注音等
			r >= 0xAC00 && r <= 0xD7A3 || // Hangul 音节
			r >= 0xF900 && r <= 0xFAFF || // CJK 兼容表意
			r >= 0xFE30 && r <= 0xFE4F || // CJK 兼容形式
			r >= 0xFF00 && r <= 0xFF60 || // 全角形式
			r >= 0xFFE0 && r <= 0xFFE6 ||
			r >= 0x4E00 && r <= 0x9FFF) { // CJK 统一表意
			w += 2
		} else {
			w++
		}
	}
	return w
}

// stdinReader 全局共享的标准输入读取器（交互菜单与 Confirm 共用，避免混用丢输入）。
var stdinReader = bufio.NewReader(os.Stdin)

// ReadLine 读取一行（去掉首尾空白）。第二个返回值为 false 表示已到输入流末尾（EOF）。
func ReadLine() (string, bool) {
	line, err := stdinReader.ReadString('\n')
	if err != nil {
		if line == "" {
			return "", false // EOF
		}
		return strings.TrimSpace(line), true // 最后一行（无换行符）
	}
	return strings.TrimSpace(line), true
}

// Pause 等待用户按回车继续。
func Pause() {
	fmt.Printf("%s按回车键继续 ...%s", color(cDim), color(cReset))
	_, _ = stdinReader.ReadString('\n')
}

// Confirm 询问确认，默认否
func Confirm(question string) bool {
	fmt.Printf("%s%s [y/N]: %s", color(cYellow), question, color(cReset))
	ans, _ := ReadLine()
	ans = strings.ToLower(ans)
	return ans == "y" || ans == "yes"
}
