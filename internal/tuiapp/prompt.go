package tuiapp

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// readString 读取一行；留空返回默认值。
func readString(reader *bufio.Reader, prompt, def string) string {
	raw, _ := readLine(reader, prompt)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	return raw
}

// readInt 读取整数；留空用默认值，无法解析时提示并使用默认值。
func readInt(reader *bufio.Reader, prompt string, def int) int {
	raw := readString(reader, prompt, "")
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		fmt.Printf("无法解析 %q 为整数，使用默认值 %d\n", raw, def)
		return def
	}
	return v
}

// readFloat 读取浮点数；留空用默认值，无法解析时提示并使用默认值。
func readFloat(reader *bufio.Reader, prompt string, def float64) float64 {
	raw := readString(reader, prompt, "")
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		fmt.Printf("无法解析 %q 为数字，使用默认值 %g\n", raw, def)
		return def
	}
	return v
}

// readDuration 读取时长（如 15s、5m）；留空用默认值，无法解析时提示并使用默认值。
func readDuration(reader *bufio.Reader, prompt string, def time.Duration) time.Duration {
	raw := readString(reader, prompt, "")
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		fmt.Printf("无法解析 %q 为时长，使用默认值 %v\n", raw, def)
		return def
	}
	return d
}
