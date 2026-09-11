// Package ui 提供 CLI 与 TUI 共用的终端输出与交互原语：
// 带时间戳/级别/颜色的日志行，以及 y/n 确认对话。
package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

var useColor = shouldUseColor()

// Log 输出带时间戳与级别前缀的日志行（INFO/WARN/ERROR/OK/ROUND，支持 NO_COLOR）。
// 签名与 engine.LogFunc 兼容，可直接注入引擎。
// 进度日志统一走 stdout，避免与 fmt 输出交错；错误输出仍由各前端走 stderr。
func Log(level, format string, args ...any) {
	ts := time.Now().Format("15:04:05")
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("[%s] [%s] %s", ts, level, msg)
	if useColor {
		line = colorizeLine(level, line)
	}
	fmt.Println(line)
}

func colorizeLine(level, line string) string {
	var color string
	switch level {
	case "OK":
		color = "32"
	case "WARN":
		color = "33"
	case "ERROR":
		color = "31"
	case "ROUND":
		color = "36"
	}
	if color == "" {
		return line
	}
	return "\x1b[" + color + "m" + line + "\x1b[0m"
}

// UseColor 报告当前终端是否启用彩色输出（NO_COLOR / TERM=dumb 时为 false）。
func UseColor() bool { return shouldUseColor() }

func shouldUseColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	term := strings.ToLower(strings.TrimSpace(os.Getenv("TERM")))
	return term != "dumb"
}

// PromptYesNo 输出 prompt 并从 reader 读取 y/n；空输入返回默认值。
func PromptYesNo(reader *bufio.Reader, prompt string, defaultYes bool) bool {
	defaultLabel := "y/N"
	if defaultYes {
		defaultLabel = "Y/n"
	}
	fmt.Printf("%s [%s]: ", prompt, defaultLabel)
	line, _ := reader.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return defaultYes
	}
	return line == "y" || line == "yes" || line == "1" || line == "true"
}
