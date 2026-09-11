package tuiapp

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"hduwords/internal/ui"
	"hduwords/internal/updatecheck"
	"hduwords/internal/updater"
)

func Run(args []string) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	repoFlag := fs.String("repo", updatecheck.DefaultRepo, "github repo owner/name")
	updatesDirFlag := fs.String("updates-dir", ".updates", "download directory for update archives")
	noUpdateCheck := fs.Bool("no-update-check", false, "skip startup update check")
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

	if !*noUpdateCheck {
		installed, err := updater.Run(context.Background(), updater.Options{
			Repo:       repo,
			BinaryName: "tui",
			UpdatesDir: *updatesDirFlag,
			Reader:     reader,
		})
		if err != nil {
			fmt.Printf("\n自动更新出错（忽略并进入主菜单）：%v\n", err)
		} else if installed {
			return nil
		}
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
	if ui.UseColor() {
		fmt.Print("\x1b[94m")
	}
	for _, line := range banner {
		fmt.Println(line)
	}
	if ui.UseColor() {
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

func runDatabaseWizard(reader *bufio.Reader) {
	fmt.Println("数据库菜单")
	fmt.Println("  1. 统计")
	fmt.Println("  2. 导出 JSON")
	fmt.Println("  3. 导出 Markdown")
	fmt.Println("  4. 更新题库")
	fmt.Println("  5. 答案冲突")
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
	case "5":
		runDBConflictsDirect(reader)
	default:
		fmt.Println("无效选择")
	}
}

func runTokenWizard(reader *bufio.Reader) {
	fmt.Println("账号菜单")
	fmt.Println("  1. 查看账号")
	fmt.Println("  2. 添加账号")
	fmt.Println("  3. 设置主账号")
	fmt.Println("  4. 删除账号")
	choice, _ := readLine(reader, "请选择")
	switch strings.TrimSpace(choice) {
	case "1":
		runListTokensDirect(reader)
	case "2":
		runAddTokenDirect(reader)
	case "3":
		runSetPrimaryDirect(reader)
	case "4":
		runRemoveTokenDirect(reader)
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
