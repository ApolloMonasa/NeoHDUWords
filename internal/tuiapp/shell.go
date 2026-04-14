package tuiapp

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"hduwords/internal/buildinfo"
	"hduwords/internal/sklclient"
	"hduwords/internal/store"
	"hduwords/internal/updatecheck"
)

const defaultTUIRepo = "ApolloMonasa/NeoHDUWords"
const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
const examMobileUserAgent = "Mozilla/5.0 (Linux; Android 13; M2102J2SC Build/TKQ1.221114.001; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/124.0.0.0 Mobile Safari/537.36"

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

	startDir, err := os.Getwd()
	if err != nil {
		return err
	}

	clearScreen()
	printSplash(repo)
	reader := bufio.NewReader(os.Stdin)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	status, err := updatecheck.Check(ctx, repo, startDir)
	if err != nil {
		fmt.Printf("\n更新检查失败：%v\n", err)
	} else {
		showUpdateStatus(status)
		if status.Available && promptYesNoWithReader(reader, "检测到仓库有更新，是否下载并安装？", false) {
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 20*time.Second)
			release, rerr := updatecheck.LatestRelease(releaseCtx, repo)
			releaseCancel()
			if rerr != nil {
				fmt.Printf("获取最新发行版失败：%v\n", rerr)
			} else {
				asset, ok := release.AssetForCurrentPlatform("tui")
				if !ok {
					fmt.Printf("最新发行版 %s 没有匹配当前平台的 tui 资产\n", release.TagName)
				} else {
					downloadDir := strings.TrimSpace(*updatesDirFlag)
					if downloadDir == "" {
						downloadDir = ".updates"
					}
					if err := os.MkdirAll(downloadDir, 0o755); err != nil {
						fmt.Printf("创建更新目录失败：%v\n", err)
					} else {
						dest := filepath.Join(downloadDir, asset.Name)
						written, derr := updatecheck.DownloadAsset(context.Background(), asset, dest)
						if derr != nil {
							fmt.Printf("下载更新失败：%v\n", derr)
						} else {
							fmt.Printf("更新包已下载：%s (%d bytes)\n", dest, written)
							if promptYesNoWithReader(reader, "是否立即安装更新？", true) {
								if ierr := installSelfUpdate(dest); ierr != nil {
									fmt.Printf("安装更新失败：%v\n", ierr)
								} else {
									fmt.Println("更新已启动安装，程序将退出。")
									return nil
								}
							}
						}
					}
				}
			}
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
	if shouldUseColor() {
		fmt.Print("\x1b[94m")
	}
	for _, line := range banner {
		fmt.Println(line)
	}
	if shouldUseColor() {
		fmt.Print("\x1b[0m")
	}
	if v := versionString(); v != "" {
		fmt.Printf("  version: %s\n", v)
	}
	fmt.Printf("  %s\n", repo.URL())
	fmt.Printf("  platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Println()
}

func versionString() string {
	if v := strings.TrimSpace(buildinfo.Version); v != "" && v != "dev" {
		if c := strings.TrimSpace(buildinfo.Commit); c != "" && c != "unknown" {
			return fmt.Sprintf("%s (%s)", v, shortSHA(c))
		}
		return v
	}
	return ""
}

func showUpdateStatus(status updatecheck.Status) {
	if status.LocalVersion != "" {
		if status.LocalSHA != "" {
			fmt.Printf("当前版本：%s (%s)\n", status.LocalVersion, shortSHA(status.LocalSHA))
		} else {
			fmt.Printf("当前版本：%s\n", status.LocalVersion)
		}
	} else if status.LocalSHA == "" {
		fmt.Println("当前版本：无法读取本地 Git 信息")
	} else {
		fmt.Printf("当前版本：%s (%s)\n", shortSHA(status.LocalSHA), status.LocalBranch)
	}
	if status.RemoteSHA == "" {
		fmt.Println("远端版本：无法获取")
		return
	}
	fmt.Printf("远端版本：%s (%s)\n", shortSHA(status.RemoteSHA), status.RemoteBranch)
	if status.Available {
		fmt.Println("状态：有更新")
	} else {
		fmt.Println("状态：已是最新")
	}
}

func menuLoop(reader *bufio.Reader) {
	for {
		fmt.Println()
		fmt.Println("主菜单")
		fmt.Println("  1. 登录")
		fmt.Println("  2. 收集")
		fmt.Println("  3. 测试")
		fmt.Println("  4. 考试")
		fmt.Println("  5. 数据库")
		fmt.Println("  6. 账号管理")
		fmt.Println("  0. 退出")
		choice, _ := readLine(reader, "请选择")
		switch strings.TrimSpace(choice) {
		case "1":
			runLoginDirect(reader)
		case "2":
			runCollectDirect(reader)
		case "3":
			runTestDirect(reader)
		case "4":
			runExamDirect(reader)
		case "5":
			runDatabaseWizard(reader)
		case "6":
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
	log.Println(line)
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

func runCollectLoop(ctx context.Context, workerTag string, cl *sklclient.Client, st *store.Store, paperType int, cooldown time.Duration, retryCfg submitRetryConfig) {
	round := 1
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		log.Println()
		collectLog("ROUND", "[%s] 第 %d 轮开始", workerTag, round)
		err := runCollectRound(ctx, workerTag, cl, st, paperType, retryCfg)
		if err != nil {
			var apiErr *sklclient.APIError
			waitTime := cooldown
			errText := err.Error()
			shouldDynamicCooldown := strings.Contains(errText, "上次申请时间") || strings.Contains(errText, "短时间重试")
			if errors.As(err, &apiErr) {
				if apiErr.Code == 2 || strings.Contains(apiErr.Msg, "短时间重试") || strings.Contains(apiErr.Msg, "失败") {
					shouldDynamicCooldown = true
				}
			}
			if shouldDynamicCooldown {
				waitTime = calcDynamicCooldown(errText, cooldown)
				collectLog("WARN", "[%s] 频率限制或创建失败，动态冷却=%v，原因=%v", workerTag, waitTime, err)
			} else {
				collectLog("ERROR", "[%s] 本轮失败: %v", workerTag, err)
			}
			collectLog("INFO", "[%s] 冷却等待 %v", workerTag, waitTime)
			if err := waitWithContext(ctx, waitTime); err != nil {
				return
			}
		} else {
			collectLog("OK", "[%s] 本轮完成，等待 %v 后进入下一轮", workerTag, cooldown)
			if err := waitWithContext(ctx, cooldown); err != nil {
				return
			}
		}
		round++
	}
}

func runCollectRound(ctx context.Context, workerTag string, cl *sklclient.Client, st *store.Store, paperType int, retryCfg submitRetryConfig) error {
	paper, err := cl.CreateFreshPaper(ctx, paperType)
	if err != nil {
		var apiErr *sklclient.APIError
		if errors.As(err, &apiErr) && (apiErr.Code == 2 || strings.Contains(apiErr.Msg, "短时间重试") || strings.Contains(apiErr.Msg, "上次申请时间")) {
			return fmt.Errorf("PaperNew(fresh): %w", err)
		}
		collectLog("WARN", "[%s] 新建试卷失败，回退活跃试卷: %v", workerTag, err)
		paper, err = cl.GetOrCreateActivePaper(ctx, paperType)
		if err != nil {
			return fmt.Errorf("GetOrCreateActivePaper(fallback): %w", err)
		}
	} else {
		collectLog("INFO", "[%s] 已新建试卷: id=%s week=%d", workerTag, paper.PaperID, paper.Week)
	}

	var res sklclient.PaperDetail
	for attempt := 0; attempt < 2; attempt++ {
		detail, err := cl.PaperDetail(ctx, paper.PaperID)
		if err != nil {
			return fmt.Errorf("PaperDetail(fetch): %w", err)
		}

		submission := make([]sklclient.Question, 0, len(detail.List))
		hit, miss := 0, 0
		for _, q := range detail.List {
			stem := q.Title
			opts := q.Options()

			var input string
			correctText, ok, err := st.FindAnswerText(ctx, stem, opts)
			if err != nil {
				return fmt.Errorf("FindAnswerText: %w", err)
			}

			idx := -1
			if ok {
				for j, opt := range opts {
					if opt == correctText {
						idx = j
						break
					}
				}
			}

			if idx != -1 {
				hit++
				input = sklclient.IndexToChoice(idx)
				q.Input = input
				t := true
				q.Right = &t
				q.Answer = input
			} else {
				miss++
				input = ""
				q.Input = input
				f := false
				q.Right = &f
			}

			if input != "" {
				submission = append(submission, q)
			}
		}

		collectLog("INFO", "[%s] 答题统计: 命中=%d 跳过=%d，准备交卷", workerTag, hit, miss)

		if len(submission) > 0 {
			if err := retryForbiddenSubmit(ctx, workerTag, "PaperSave", retryCfg, func() error {
				return cl.PaperSave(ctx, paper.PaperID, submission)
			}); err != nil {
				if attempt == 0 && isForbiddenAPIError(err) {
					collectLog("WARN", "[%s] PaperSave 返回 403，当前试卷可能失效，尝试新建试卷重试", workerTag)
					newPaper, nerr := cl.CreateFreshPaper(ctx, paperType)
					if nerr != nil {
						return fmt.Errorf("PaperSave(submit): %w; PaperNew(retry): %w", err, nerr)
					}
					paper = newPaper
					collectLog("INFO", "[%s] 重试改用新试卷: id=%s week=%d", workerTag, paper.PaperID, paper.Week)
					continue
				}
				return fmt.Errorf("PaperSave(submit): %w", err)
			}
		}

		if err := retryForbiddenSubmit(ctx, workerTag, "PaperSubmit", retryCfg, func() error {
			return cl.PaperSubmit(ctx, paper.PaperID)
		}); err != nil {
			if attempt == 0 && isForbiddenAPIError(err) {
				collectLog("WARN", "[%s] PaperSubmit 返回 403，当前试卷可能失效，尝试新建试卷重试", workerTag)
				newPaper, nerr := cl.CreateFreshPaper(ctx, paperType)
				if nerr != nil {
					return fmt.Errorf("PaperSubmit: %w; PaperNew(retry): %w", err, nerr)
				}
				paper = newPaper
				collectLog("INFO", "[%s] 重试改用新试卷: id=%s week=%d", workerTag, paper.PaperID, paper.Week)
				continue
			}
			return fmt.Errorf("PaperSubmit: %w", err)
		}

		res, err = cl.PaperDetail(ctx, paper.PaperID)
		if err != nil {
			return fmt.Errorf("PaperDetail(result): %w", err)
		}

		break
	}

	added, updated, skipped, err := upsertCollectedAnswers(ctx, st, res)
	if err != nil {
		return err
	}

	collectLog("OK", "[%s] 收集结果: 得分=%d 试卷=%s 入库[新增=%d 更新=%d 跳过=%d]",
		workerTag, res.Mark, res.PaperID, added, updated, skipped)
	return nil
}

func tokenToURL(token string) string {
	return fmt.Sprintf("https://skl.hdu.edu.cn/?type=6&token=%s#/english/list", token)
}

func runDatabaseWizard(reader *bufio.Reader) {
	fmt.Println("数据库菜单")
	fmt.Println("  1. stats")
	fmt.Println("  2. export json")
	fmt.Println("  3. export markdown")
	choice, _ := readLine(reader, "请选择")
	switch strings.TrimSpace(choice) {
	case "1":
		runDBStatsDirect(reader)
	case "2":
		runDBExportDirect(reader, false)
	case "3":
		runDBExportDirect(reader, true)
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

func installSelfUpdate(sourcePath string) error {
	selfExe, err := os.Executable()
	if err != nil {
		return err
	}
	helperDir, err := os.MkdirTemp("", "hduwords-updater-*")
	if err != nil {
		return err
	}
	helperPath := filepath.Join(helperDir, filepath.Base(selfExe))
	if err := copyLocalFile(selfExe, helperPath); err != nil {
		return err
	}
	cmd := exec.Command(helperPath, "--apply-update", "--source", sourcePath, "--target", selfExe)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	return nil
}

func copyLocalFile(srcPath, dstPath string) error {
	input, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	return os.WriteFile(dstPath, input, 0o755)
}

func clearScreen() {
	fmt.Print("\033[2J\033[H")
}

func shortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func readLine(reader *bufio.Reader, prompt string) (string, error) {
	fmt.Printf("%s: ", prompt)
	line, err := reader.ReadString('\n')
	if err != nil {
		return strings.TrimSpace(line), err
	}
	return strings.TrimSpace(line), nil
}

func promptYesNoWithReader(reader *bufio.Reader, prompt string, defaultYes bool) bool {
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
