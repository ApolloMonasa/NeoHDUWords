package tuiapp

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"hduwords/internal/updatecheck"
	"hduwords/internal/updater"
)

const defaultTUIRepo = "ApolloMonasa/NeoHDUWords"

var collectUseColor = shouldUseColor()

func Run(args []string) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	repoFlag := fs.String("repo", defaultTUIRepo, "github repo owner/name")
	updatesDirFlag := fs.String("updates-dir", ".updates", "download directory for update archives")
	if err := fs.Parse(args); err != nil {
		return err
	}

	repo, err := updatecheck.ParseRepo(*repoFlag)
	if err != nil {
		return err
	}

	clearScreen()
	printSplash(repo)
	reader := bufio.NewReader(os.Stdin)

	installed, err := updater.Run(context.Background(), updater.Options{
		Repo:       repo,
		BinaryName: "tui",
		UpdatesDir: *updatesDirFlag,
		Reader:     reader,
		ApplyArgs: func(source, target string) []string {
			return []string{"--apply-update", "--source", source, "--target", target}
		},
	})
	if err != nil {
		fmt.Printf("\n更新检查失败：%v\n", err)
	} else if installed {
		return nil
	}

	menuLoop(reader)
	return nil
}

func printSplash(repo updatecheck.Repo) {
	banner := []string{
		"██╗  ██╗██████╗ ██╗   ██╗      ██╗    ██╗ ██████╗ ██████╗ ██████╗ ███████╗",
		"██║  ██║██╔══██╗██║   ██║      ██║    ██║██╔═══██╗██╔══██╗██╔══██╗██╔════╝",
		"███████║██║  ██║██║   ██║█████╗██║ █╗ ██║██║   ██║██████╔╝██║  ██║███████╗",
		"██╔══██║██║  ██║██║   ██║╚════╝██║███╗██║██║   ██║██╔══██╗██║  ██║╚════██║",
		"██║  ██║██████╔╝╚██████╔╝      ╚███╔███╔╝╚██████╔╝██║  ██║██████╔╝███████║",
		"╚═╝  ╚═╝╚═════╝  ╚═════╝        ╚══╝╚══╝  ╚═════╝ ╚═╝  ╚═╝╚═════╝ ╚══════╝",
		"      ░░░  T U I   M O D E  ░░░",
	}
	if shouldUseColor() {
		fmt.Print("\x1b[94m")
	}
	for _, line := range banner {
		fmt.Println(line)
	}
	if shouldUseColor() {
		fmt.Print("\x1b[0m")
	}
	if v := updater.VersionString(); v != "" {
		fmt.Printf("  version: %s\n", v)
	}
	fmt.Printf("  %s\n", repo.URL())
	fmt.Printf("  platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Println()
}

func menuLoop(reader *bufio.Reader) {
	for {
		fmt.Println()
		fmt.Println("主菜单")
		fmt.Println("  1. 登录")
		fmt.Println("  2. 收集")
		fmt.Println("  3. 考试")
		fmt.Println("  4. 数据库")
		fmt.Println("  5. 账号管理")
		fmt.Println("  0. 退出")
		choice, _ := readLine(reader, "请选择")
		switch strings.TrimSpace(choice) {
		case "1":
			runLoginDirect(reader)
		case "2":
			runCollectDirect(reader)
		case "3":
			runExamDirect(reader)
		case "4":
			runDatabaseWizard(reader)
		case "5":
			runTokenWizard(reader)
		case "0", "q", "quit", "exit":
			return
		default:
			fmt.Println("无效选择")
		}
	}
}

func collectLog(level, format string, args ...any) {
	ts := time.Now().Format("15:04:05")
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("[%s] [%s] %s", ts, level, msg)
	if collectUseColor {
		line = colorizeCollectLine(level, line)
	}
	// 进度日志统一走 stdout，避免与 fmt 输出交错
	fmt.Println(line)
}

func colorizeCollectLine(level, line string) string {
	color := ""
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

func shouldUseColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	term := strings.ToLower(strings.TrimSpace(os.Getenv("TERM")))
	return term != "dumb"
}

func runDatabaseWizard(reader *bufio.Reader) {
	fmt.Println("数据库菜单")
	fmt.Println("  1. stats")
	fmt.Println("  2. export json")
	fmt.Println("  3. export markdown")
	fmt.Println("  4. update")
	choice, _ := readLine(reader, "请选择")
	switch strings.TrimSpace(choice) {
	case "1":
		runDBStatsDirect(reader)
	case "2":
		runDBExportDirect(reader, false)
	case "3":
		runDBExportDirect(reader, true)
	case "4":
		runDBUpdateDirect()
	default:
		fmt.Println("无效选择")
	}
}

func runTokenWizard(reader *bufio.Reader) {
	fmt.Println("账号菜单")
	fmt.Println("  1. listtokens")
	fmt.Println("  2. addtoken")
	fmt.Println("  3. setprimary")
	choice, _ := readLine(reader, "请选择")
	switch strings.TrimSpace(choice) {
	case "1":
		runListTokensDirect(reader)
	case "2":
		runAddTokenDirect(reader)
	case "3":
		runSetPrimaryDirect(reader)
	default:
		fmt.Println("无效选择")
	}
}

func clearScreen() {
	fmt.Print("\033[2J\033[H")
}

func readLine(reader *bufio.Reader, prompt string) (string, error) {
	fmt.Printf("%s: ", prompt)
	line, err := reader.ReadString('\n')
	if err != nil {
		return strings.TrimSpace(line), err
	}
	return strings.TrimSpace(line), nil
}
