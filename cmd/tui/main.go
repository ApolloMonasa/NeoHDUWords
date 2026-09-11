package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"hduwords/internal/tuiapp"
	"hduwords/internal/updatecheck"
)

func main() {
	// 安装助手子命令：与 CLI 相同的协议（自更新时由旧进程的临时副本调用）
	if len(os.Args) > 1 && os.Args[1] == "apply-update" {
		fs := flag.NewFlagSet("apply-update", flag.ExitOnError)
		sourcePath := fs.String("source", "", "downloaded update source path")
		targetPath := fs.String("target", "", "target executable path")
		_ = fs.Parse(os.Args[2:])
		if strings.TrimSpace(*sourcePath) == "" || strings.TrimSpace(*targetPath) == "" {
			fmt.Fprintln(os.Stderr, "apply-update 需要 --source 和 --target")
			os.Exit(1)
		}
		if err := updatecheck.InstallBinary(*sourcePath, *targetPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if err := tuiapp.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
