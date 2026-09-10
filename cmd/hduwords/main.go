package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"hduwords/internal/engine"
	"hduwords/internal/sklclient"
	"hduwords/internal/store"
	"hduwords/internal/tokenpool"
	"hduwords/internal/updatecheck"
)

const defaultUpdateRepo = "ApolloMonasa/NeoHDUWords"

func main() {
	log.SetFlags(0)

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	switch cmd {
	case "login":
		loginCmd(os.Args[2:])
	case "addtoken":
		addTokenCmd(os.Args[2:])
	case "listtokens":
		listTokensCmd(os.Args[2:])
	case "setprimary":
		setPrimaryCmd(os.Args[2:])
	case "collect":
		collectCmd(os.Args[2:])
	case "exam":
		runExamCmd(os.Args[2:])
	case "db":
		dbCmd(os.Args[2:])
	case "update":
		updateCmd(os.Args[2:])
	case "apply-update":
		applyUpdateCmd(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fatalf("unknown command: %s", cmd)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `cli - HDU 我爱记单词 CLI

Usage:
	hduwords login    [--browser chrome|edge]
	hduwords addtoken [--browser chrome|edge]
	hduwords listtokens [--pool-file .tokens] [--show-plain]
	hduwords setprimary [--token <token>] [--pool-file .tokens] [--sync-login=true]
	hduwords collect [--url <token_url>] --db <path> [--rate 2] [--timeout 15s] [--ua <ua>] [--cooldown 5m] [--pool-file .tokens] [--workers 0] [--submit-retries 3] [--submit-retry-interval 10s]
	hduwords exam    [--url <token_url>] --db <path> [--rate 2] [--timeout 15s] [--time 30s] [--score 100] [--dry-run] [--submit-retries 3] [--submit-retry-interval 10s]
	hduwords update  [--repo owner/name] [--updates-dir .updates] [--yes] [--check-only]
	hduwords db stats --db <path>
	hduwords db export --db <path> [--format json|markdown] [--out <file>]
	hduwords db update [--out <file>]

Commands:
	login      自动打开浏览器，完成统一身份认证后后台自动捕获 Token 并保存至本地
	addtoken   自动打开浏览器，登录后将 token 追加写入 .tokens（用于 collect 多账号并发）
	listtokens 查看 .token 与 .tokens 的账号列表及 primary 标识
	setprimary 设置 .tokens 的 primary 标识；默认同步到 .token 供 exam 使用
	collect    收集题库：支持 token 池并发采集；收集与练习统一使用 type=0
	exam       正式自动考试：基于本地题库进行正式考试作答
	update     检查并安装最新 CLI 发行版（二进制更新）
	db stats    查看本地题库统计信息（题目数、答案数、冲突数）
	db export   导出完整题库（包含题干、选项、正确答案），可用于还原官方题库
	db markdown, export-md, md 导出题库为 markdown 格式

Options:
	Login:
		--browser            浏览器种类: chrome|edge（留空自动检测，优先级 Chrome→Edge）

	Common:
		--url                如不提供，则默认从 'hduwords login' 生成的本地 .token 文件中读取。也可手动提供带有 token 的网址
		--db                 数据库路径，默认 hduwords.db
		--rate               请求速率，默认 2
		--timeout            请求超时，默认 15s
		--ua                 自定义 UA；默认是真实浏览器风格的 Windows Chrome UA（exam 模式会强制覆盖为移动端 UA）

	Exam only:
		--time               交卷前等待时长，默认 0s
		--score              目标得分百分比，默认 -1（不启用）
		--dry-run            只演练不提交
		--submit-retries     提交 403 重试次数，默认 3
		--submit-retry-interval 提交 403 重试间隔，默认 10s

	Collect only:
		--cooldown           每轮冷却时间，默认 5m
		--pool-file          token 池文件，默认 .tokens
		--workers            并发 worker 数，默认自动

	Update only:
		--repo               发布仓库，默认 ApolloMonasa/NeoHDUWords
		--updates-dir        更新包下载目录，默认 .updates
		--yes                跳过确认，直接安装
		--check-only         只检查是否有更新，不安装
`)
}

func updateCmd(args []string) {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	repoFlag := fs.String("repo", defaultUpdateRepo, "github repo owner/name")
	updatesDirFlag := fs.String("updates-dir", ".updates", "download directory for update archives")
	yesFlag := fs.Bool("yes", false, "auto confirm install")
	checkOnlyFlag := fs.Bool("check-only", false, "only check for updates")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	repo, err := updatecheck.ParseRepo(*repoFlag)
	if err != nil {
		fatalErr(err)
	}

	startDir, err := os.Getwd()
	if err != nil {
		fatalErr(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	status, err := updatecheck.Check(ctx, repo, startDir)
	if err != nil {
		fatalErr(err)
	}

	showUpdateStatusCLI(status)
	if !status.Available {
		fmt.Println("已是最新版本")
		return
	}
	if *checkOnlyFlag {
		fmt.Println("检测到有更新（check-only）")
		return
	}

	if !*yesFlag && !promptYesNoCLI("检测到更新，是否下载并安装？", false) {
		fmt.Println("已取消更新")
		return
	}

	releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 20*time.Second)
	release, err := updatecheck.LatestRelease(releaseCtx, repo)
	releaseCancel()
	if err != nil {
		fatalErr(err)
	}

	asset, ok := release.AssetForCurrentPlatform("cli")
	if !ok {
		fatalf("最新发行版 %s 没有匹配当前平台的 cli 资产", release.TagName)
	}

	downloadDir := strings.TrimSpace(*updatesDirFlag)
	if downloadDir == "" {
		downloadDir = ".updates"
	}
	if err := os.MkdirAll(downloadDir, 0o755); err != nil {
		fatalErr(err)
	}

	dest := filepath.Join(downloadDir, asset.Name)
	if abs, err := filepath.Abs(dest); err == nil {
		dest = abs
	}
	written, err := updatecheck.DownloadAsset(context.Background(), asset, dest)
	if err != nil {
		fatalErr(err)
	}
	fmt.Printf("更新包已下载：%s (%d bytes)\n", dest, written)

	if err := installSelfUpdateCLI(dest); err != nil {
		fatalErr(err)
	}
	fmt.Println("更新已启动安装，程序将退出。")
}

func applyUpdateCmd(args []string) {
	fs := flag.NewFlagSet("apply-update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	sourcePath := fs.String("source", "", "downloaded update source path")
	targetPath := fs.String("target", "", "target executable path")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if strings.TrimSpace(*sourcePath) == "" || strings.TrimSpace(*targetPath) == "" {
		fatalf("apply-update 需要 --source 和 --target")
	}
	if err := updatecheck.InstallBinary(*sourcePath, *targetPath); err != nil {
		fatalErr(err)
	}
}

func installSelfUpdateCLI(sourcePath string) error {
	selfExe, err := os.Executable()
	if err != nil {
		return err
	}
	helperDir, err := os.MkdirTemp("", "hduwords-updater-*")
	if err != nil {
		return err
	}
	helperPath := filepath.Join(helperDir, filepath.Base(selfExe))
	if err := copyLocalFileCLI(selfExe, helperPath); err != nil {
		return err
	}
	cmd := exec.Command(helperPath, "apply-update", "--source", sourcePath, "--target", selfExe)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Start()
}

func copyLocalFileCLI(srcPath, dstPath string) error {
	input, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	return os.WriteFile(dstPath, input, 0o755)
}

func promptYesNoCLI(prompt string, defaultYes bool) bool {
	defaultLabel := "y/N"
	if defaultYes {
		defaultLabel = "Y/n"
	}
	fmt.Printf("%s [%s]: ", prompt, defaultLabel)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return defaultYes
	}
	return line == "y" || line == "yes" || line == "1" || line == "true"
}

func showUpdateStatusCLI(status updatecheck.Status) {
	if status.LocalVersion != "" {
		if status.LocalSHA != "" {
			fmt.Printf("当前版本：%s (%s)\n", status.LocalVersion, shortSHACLI(status.LocalSHA))
		} else {
			fmt.Printf("当前版本：%s\n", status.LocalVersion)
		}
	} else if status.LocalSHA == "" {
		fmt.Println("当前版本：无法读取本地 Git 信息")
	} else {
		fmt.Printf("当前版本：%s (%s)\n", shortSHACLI(status.LocalSHA), status.LocalBranch)
	}
	if status.RemoteSHA == "" {
		fmt.Println("远端版本：无法获取")
		return
	}
	fmt.Printf("远端版本：%s (%s)\n", shortSHACLI(status.RemoteSHA), status.RemoteBranch)
	if status.Available {
		fmt.Println("状态：有更新")
	} else {
		fmt.Println("状态：已是最新")
	}
}

func shortSHACLI(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func runExamCmd(args []string) {
	fs := flag.NewFlagSet("exam", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		rawURL         = fs.String("url", "", "token url")
		dbPath         = fs.String("db", "hduwords.db", "sqlite db path")
		rate           = fs.Float64("rate", 2, "max requests per second")
		timeout        = fs.Duration("timeout", 15*time.Second, "http timeout")
		ua             = fs.String("ua", sklclient.DefaultUserAgent, "user-agent")
		examTime       = fs.Duration("time", 0, "wait before submitting")
		examScore      = fs.Int("score", -1, "target score percentage 0-100")
		dryRun         = fs.Bool("dry-run", false, "print decisions without submitting")
		unknownPolicy  = fs.String("unknown-policy", "random", "abort|skip|random")
		submitRetries  = fs.Int("submit-retries", 3, "retry count for 403 on save/submit before creating new paper")
		submitRetryInt = fs.Duration("submit-retry-interval", 10*time.Second, "wait duration between 403 retries on save/submit")
	)

	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	paperType := 1
	*ua = sklclient.ExamMobileUserAgent

	finalURL := getFinalTokenURL(*rawURL)

	policy, err := parseUnknownPolicy(*unknownPolicy)
	if err != nil {
		fatalErr(err)
	}
	if policy != unknownRandom {
		collectLog("WARN", "exam 模式未知题策略强制 random (忽略 --unknown-policy=%s)", *unknownPolicy)
		policy = unknownRandom
	}
	retryCfg := engine.SubmitRetryConfig{MaxRetries: *submitRetries, Interval: *submitRetryInt}.Normalized()

	collectLog("INFO", "exam 模式启动: type=%d db=%s dryRun=%v submitRetries=%d retryInterval=%v", paperType, *dbPath, *dryRun, retryCfg.MaxRetries, retryCfg.Interval)
	collectLog("INFO", "exam 参数: time=%v score=%d", *examTime, *examScore)

	contextTimeout := *examTime + 15*time.Minute
	if contextTimeout < 20*time.Minute {
		contextTimeout = 20 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), contextTimeout)
	defer cancel()

	st, err := store.Open(*dbPath)
	if err != nil {
		fatalErr(err)
	}
	defer st.Close()

	cl, err := sklclient.NewFromTokenURL(finalURL, sklclient.Options{
		BaseUserAgent: *ua,
		Timeout:       *timeout,
		MaxRPS:        *rate,
	})
	if err != nil {
		fatalErr(err)
	}

	var retryPaper *sklclient.Paper
	for attempt := 0; attempt < 2; attempt++ {
		var paper sklclient.Paper
		if retryPaper != nil {
			paper = *retryPaper
			retryPaper = nil
		} else {
			paper, err = cl.CreateExamPaper(ctx, paperType)
		}
		if err != nil {
			fatalErr(fmt.Errorf("CreateExamPaper(exam): %w", err))
		}

		detail, err := cl.PaperDetail(ctx, paper.PaperID)
		if err != nil {
			if attempt == 0 && engine.IsForbiddenAPIError(err) {
				collectLog("WARN", "PaperDetail 返回 403，尝试新建试卷重试")
				paper, err = cl.CreateExamPaper(ctx, paperType)
				if err != nil {
					fatalErr(err)
				}
				tmp := paper
				retryPaper = &tmp
				continue
			}
			fatalErr(err)
		}

		targetCorrect := -1
		correctAssigned := 0
		if *examScore >= 0 {
			if *examScore > 100 {
				fatalf("--score must be between 0 and 100")
			}
			targetCorrect = (len(detail.List)*(*examScore) + 50) / 100
			if targetCorrect > len(detail.List) {
				targetCorrect = len(detail.List)
			}
			collectLog("INFO", "exam 目标得分=%d%%，目标正确题数=%d/%d", *examScore, targetCorrect, len(detail.List))
		}

		submission := make([]sklclient.Question, 0, len(detail.List))
		hit, miss := 0, 0
		for _, q := range detail.List {
			stem := q.Title
			opts := q.Options()

			var input string
			correctText, ok, err := st.FindAnswerText(ctx, stem, opts)
			if err != nil {
				fatalErr(err)
			}
			if *examScore >= 0 {
				useCorrect := ok && correctAssigned < targetCorrect
				if useCorrect {
					idx := -1
					for j, opt := range opts {
						if opt == correctText {
							idx = j
							break
						}
					}
					if idx != -1 {
						hit++
						correctAssigned++
						input = sklclient.IndexToChoice(idx)
						q.Input = input
						t := true
						q.Right = &t
						q.Answer = input
					} else {
						miss++
						input = chooseWrongChoice(correctText, opts)
						q.Input = input
						f := false
						q.Right = &f
					}
				} else {
					miss++
					input = chooseWrongChoice(correctText, opts)
					q.Input = input
					f := false
					q.Right = &f
				}
			} else if ok {
				idx := -1
				for j, opt := range opts {
					if opt == correctText {
						idx = j
						break
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
				}
			} else {
				miss++
			}

			if !ok || input == "" {
				switch policy {
				case unknownAbort:
					fatalf("unknown question (no db match): %q", stem)
				case unknownSkip:
					input = ""
					q.Input = input
					f := false
					q.Right = &f
				case unknownRandom:
					if len(opts) > 0 {
						input = sklclient.IndexToChoice(rand.IntN(len(opts)))
					} else {
						input = ""
					}
					q.Input = input
					f := false
					q.Right = &f
				default:
					fatalf("unknown policy: %v", policy)
				}
			}

			if !*dryRun && input != "" {
				submission = append(submission, q)
			}
		}

		if *examScore >= 0 {
			collectLog("INFO", "exam 评分控制: 已保留正确=%d 目标正确=%d 总题=%d", correctAssigned, targetCorrect, len(detail.List))
		}

		collectLog("INFO", "试卷=%s 总题=%d 命中=%d 未命中=%d", paper.PaperID, len(detail.List), hit, miss)

		if *dryRun {
			collectLog("INFO", "dry-run 已开启，不提交答案")
			return
		}

		if *examTime > 0 {
			collectLog("INFO", "exam 模式进度条等待 %v 后交卷", *examTime)
			if err := waitWithProgressBar(ctx, *examTime, "等待交卷"); err != nil {
				fatalErr(err)
			}
		}

		if len(submission) > 0 {
			if err := engine.RetryForbiddenSubmit(ctx, "", "PaperSave", retryCfg, collectLog, func() error {
				return cl.PaperSave(ctx, paper.PaperID, submission)
			}); err != nil {
				if attempt == 0 && engine.IsForbiddenAPIError(err) {
					collectLog("WARN", "PaperSave 返回 403，尝试新建试卷重试")
					newPaper, nerr := cl.CreateExamPaper(ctx, paperType)
					if nerr != nil {
						fatalErr(fmt.Errorf("PaperSave: %w; CreateExamPaper(retry): %w", err, nerr))
					}
					tmp := newPaper
					retryPaper = &tmp
					continue
				}
				fatalErr(err)
			}
		}

		if err := engine.RetryForbiddenSubmit(ctx, "", "PaperSubmit", retryCfg, collectLog, func() error {
			return cl.PaperSubmit(ctx, paper.PaperID)
		}); err != nil {
			if attempt == 0 && engine.IsForbiddenAPIError(err) {
				collectLog("WARN", "PaperSubmit 返回 403，尝试新建试卷重试")
				newPaper, nerr := cl.CreateExamPaper(ctx, paperType)
				if nerr != nil {
					fatalErr(fmt.Errorf("PaperSubmit: %w; CreateExamPaper(retry): %w", err, nerr))
				}
				tmp := newPaper
				retryPaper = &tmp
				continue
			}
			fatalErr(err)
		}

		res, err := cl.PaperDetail(ctx, paper.PaperID)
		if err != nil {
			fatalErr(err)
		}

		if ok, listErr := paperInList(ctx, cl, paper.PaperID, paperType); listErr != nil {
			collectLog("WARN", "exam 结果校验失败: %v", listErr)
		} else if !ok {
			collectLog("WARN", "exam 试卷未出现在列表中: %s", paper.PaperID)
		}

		added, updated, skipped, err := engine.UpsertCollectedAnswers(ctx, st, res)
		if err != nil {
			fatalErr(err)
		}
		collectLog("OK", "题目回收: 试卷=%s 入库[新增=%d 更新=%d 跳过=%d]", res.PaperID, added, updated, skipped)

		if res.EndTime != nil {
			collectLog("OK", "提交完成: 得分=%d endTime=%s", res.Mark, res.EndTime.Format(time.RFC3339))
		} else {
			collectLog("OK", "提交完成: 得分=%d", res.Mark)
		}
		return
	}

	fatalf("执行失败：重试后仍未完成")
}

func chooseWrongChoice(correct string, options []string) string {
	for _, opt := range options {
		if opt != "" && opt != correct {
			return opt
		}
	}
	return ""
}

var collectUseColor = shouldUseColor()

func shouldUseColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	term := strings.ToLower(strings.TrimSpace(os.Getenv("TERM")))
	if term == "dumb" {
		return false
	}
	return true
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
	default:
		color = ""
	}
	if color == "" {
		return line
	}
	return "\x1b[" + color + "m" + line + "\x1b[0m"
}

func collectCmd(args []string) {
	fs := flag.NewFlagSet("collect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		rawURL         = fs.String("url", "", "token url")
		dbPath         = fs.String("db", "hduwords.db", "sqlite db path")
		rate           = fs.Float64("rate", 2, "max requests per second")
		timeout        = fs.Duration("timeout", 15*time.Second, "http timeout")
		ua             = fs.String("ua", sklclient.DefaultUserAgent, "user-agent")
		cooldown       = fs.Duration("cooldown", 5*time.Minute, "cooldown between rounds")
		poolFile       = fs.String("pool-file", tokenpool.DefaultPoolFile, "token pool file path")
		workers        = fs.Int("workers", 0, "collect workers: 0=auto (pool size, or 1 when pool empty), >0=min(n, available tokens)")
		submitRetries  = fs.Int("submit-retries", 3, "retry count for 403 on save/submit before creating new paper")
		submitRetryInt = fs.Duration("submit-retry-interval", 10*time.Second, "wait duration between 403 retries on save/submit")
	)

	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	// collect mode is a long-running process
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	st, err := store.Open(*dbPath)
	if err != nil {
		fatalErr(err)
	}
	defer st.Close()

	retryCfg := engine.SubmitRetryConfig{MaxRetries: *submitRetries, Interval: *submitRetryInt}.Normalized()

	pool, err := tokenpool.Load(*poolFile)
	if err != nil {
		fatalErr(fmt.Errorf("load token pool: %w", err))
	}

	workerSpecs := engine.BuildWorkers(pool.Tokens, getFinalTokenURL(*rawURL), *workers, sklclient.Options{
		BaseUserAgent: *ua,
		Timeout:       *timeout,
		MaxRPS:        *rate,
	}, collectLog)
	if len(workerSpecs) == 0 {
		fatalf("可用 token 数为 0，请先执行 hduwords addtoken 或提供 --url")
	}

	engine.RunCollectPool(ctx, engine.CollectPoolOptions{
		Workers:  workerSpecs,
		Store:    st,
		Cooldown: *cooldown,
		Retry:    retryCfg,
		Log:      collectLog,
	})
}

func paperInList(ctx context.Context, cl *sklclient.Client, paperID string, paperType int) (bool, error) {
	list, err := cl.PaperList(ctx, paperType)
	if err != nil {
		return false, err
	}
	for _, item := range list {
		if item.PaperID == paperID {
			return true, nil
		}
	}
	return false, nil
}

func waitWithProgressBar(ctx context.Context, d time.Duration, label string) error {
	if d <= 0 {
		return nil
	}
	deadline := time.Now().Add(d)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	renderProgressBar(label, d, d)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			fmt.Println()
			return nil
		}
		select {
		case <-ctx.Done():
			fmt.Print("\n")
			return ctx.Err()
		case <-ticker.C:
			elapsed := d - remaining
			renderProgressBar(label, elapsed, d)
		}
	}
}

func renderProgressBar(label string, elapsed, total time.Duration) {
	if total <= 0 {
		total = time.Second
	}
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed > total {
		elapsed = total
	}
	const barWidth = 24
	filled := int(float64(barWidth) * float64(elapsed) / float64(total))
	if filled > barWidth {
		filled = barWidth
	}
	percent := int(float64(elapsed) * 100 / float64(total))
	if percent > 100 {
		percent = 100
	}
	bar := strings.Repeat("#", filled) + strings.Repeat("-", barWidth-filled)
	fmt.Printf("\r\x1b[2K%s [%s] %3d%%", label, bar, percent)
}

func dbCmd(args []string) {
	if len(args) < 1 {
		fatalf("db subcommand required (stats|export|markdown|update)")
	}
	switch args[0] {
	case "stats":
		dbStatsCmd(args[1:])
	case "export":
		dbExportCmd(args[1:])
	case "markdown", "export-md", "md":
		dbMarkdownCmd(args[1:])
	case "update":
		dbUpdateCmd(args[1:])
	default:
		fatalf("unknown db subcommand: %s", args[0])
	}
}
func dbUpdateCmd(args []string) {
	fs := flag.NewFlagSet("db update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	outFile := fs.String("out", "", "output file path (default next to executable)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	dest := strings.TrimSpace(*outFile)
	if dest == "" {
		exe, err := os.Executable()
		if err != nil {
			exe = "hduwords"
		}
		dest = filepath.Join(filepath.Dir(exe), "hduwords.db")
	}

	asset := updatecheck.ReleaseAsset{
		Name: "hduwords.db",
		URL:  "https://github.com/ApolloMonasa/NeoHDUWords/releases/download/Data/hduwords.db",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fmt.Printf("正在下载数据库 %s ...\n", asset.URL)
	written, err := updatecheck.DownloadAsset(ctx, asset, dest)
	if err != nil {
		fatalErr(fmt.Errorf("下载数据库失败: %w", err))
	}
	fmt.Printf("数据库已保存至 %s (%d bytes)\n", dest, written)

	// Remove stale WAL/SHM files so subsequent opens read the fresh db.
	os.Remove(dest + "-wal")
	os.Remove(dest + "-shm")
}

func dbMarkdownCmd(args []string) {
	fs := flag.NewFlagSet("db markdown", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dbPath := fs.String("db", "hduwords.db", "sqlite db path")
	outFile := fs.String("out", "", "output file path (default stdout)")

	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st, err := store.Open(*dbPath)
	if err != nil {
		fatalErr(err)
	}
	defer st.Close()

	items, err := st.Export(ctx)
	if err != nil {
		fatalErr(err)
	}

	out := os.Stdout
	if *outFile != "" {
		f, err := os.Create(*outFile)
		if err != nil {
			fatalErr(err)
		}
		defer f.Close()
		out = f
	}

	fmt.Fprintf(out, "# HDU Words 题库导出\n\n共 %d 题\n\n", len(items))
	for i, item := range items {
		fmt.Fprintf(out, "### %d. %s\n\n", i+1, item.Stem)
		for j, opt := range item.Options {
			prefix := "- [ ]"
			if j == item.CorrectIndex {
				prefix = "- [x]"
			}
			fmt.Fprintf(out, "%s %s. %s\n", prefix, string(rune('A'+j)), opt)
		}
		fmt.Fprintln(out)
	}
}

func dbStatsCmd(args []string) {
	fs := flag.NewFlagSet("db stats", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dbPath := fs.String("db", "hduwords.db", "sqlite db path")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st, err := store.Open(*dbPath)
	if err != nil {
		fatalErr(err)
	}
	defer st.Close()

	s, err := st.Stats(ctx)
	if err != nil {
		fatalErr(err)
	}
	fmt.Printf("items=%d answers=%d conflicts=%d\n", s.Items, s.Answers, s.Conflicts)
}

func dbExportCmd(args []string) {
	fs := flag.NewFlagSet("db export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dbPath := fs.String("db", "hduwords.db", "sqlite db path")
	format := fs.String("format", "json", "export format: json|markdown")
	outFile := fs.String("out", "", "output file path (default stdout)")

	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st, err := store.Open(*dbPath)
	if err != nil {
		fatalErr(err)
	}
	defer st.Close()

	items, err := st.Export(ctx)
	if err != nil {
		fatalErr(err)
	}

	out := os.Stdout
	if *outFile != "" {
		f, err := os.Create(*outFile)
		if err != nil {
			fatalErr(err)
		}
		defer f.Close()
		out = f
	}

	switch *format {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(items); err != nil {
			fatalErr(err)
		}
	case "markdown":
		fmt.Fprintf(out, "# HDU Words 题库导出\n\n共 %d 题\n\n", len(items))
		for i, item := range items {
			fmt.Fprintf(out, "### %d. %s\n\n", i+1, item.Stem)
			for j, opt := range item.Options {
				prefix := "- [ ]"
				if j == item.CorrectIndex {
					prefix = "- [x]"
				}
				fmt.Fprintf(out, "%s %s. %s\n", prefix, string(rune('A'+j)), opt)
			}
			fmt.Fprintln(out)
		}
	default:
		fatalf("unsupported format: %s", *format)
	}
}

type unknownPolicy int

const (
	unknownAbort unknownPolicy = iota
	unknownSkip
	unknownRandom
)

func parseUnknownPolicy(s string) (unknownPolicy, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "abort":
		return unknownAbort, nil
	case "skip":
		return unknownSkip, nil
	case "random":
		return unknownRandom, nil
	default:
		return 0, fmt.Errorf("invalid --unknown-policy: %q", s)
	}
}

func fatalf(format string, args ...any) {
	log.Printf("error: "+format, args...)
	os.Exit(1)
}

func fatalErr(err error) {
	if err == nil {
		return
	}
	var e *sklclient.APIError
	if errors.As(err, &e) {
		log.Printf("error: %s", e.Error())
		os.Exit(1)
	}
	log.Printf("error: %v", err)
	os.Exit(1)
}
