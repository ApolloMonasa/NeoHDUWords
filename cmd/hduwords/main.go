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
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"hduwords/internal/sklclient"
	"hduwords/internal/store"
	"hduwords/internal/updatecheck"
)

const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
const examMobileUserAgent = "Mozilla/5.0 (Linux; Android 13; M2102J2SC Build/TKQ1.221114.001; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/124.0.0.0 Mobile Safari/537.36"
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
	case "test":
		runCmd("test", os.Args[2:])
	case "exam":
		runCmd("exam", os.Args[2:])
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
	hduwords login
	hduwords addtoken
	hduwords listtokens [--pool-file .tokens] [--show-plain]
	hduwords setprimary [--token <token>] [--pool-file .tokens] [--sync-login=true]
	hduwords collect [--url <token_url>] --db <path> [--rate 2] [--timeout 15s] [--ua <ua>] [--cooldown 5m] [--pool-file .tokens] [--workers 0] [--submit-retries 3] [--submit-retry-interval 10s]
	hduwords test    [--url <token_url>] --db <path> [--rate 2] [--timeout 15s] [--ua <ua>] [--dry-run] [--unknown-policy abort|skip|random] [--submit-retries 3] [--submit-retry-interval 10s]
	hduwords exam    [--url <token_url>] --db <path> [--rate 2] [--timeout 15s] [--time 30s] [--score 100] [--dry-run] [--submit-retries 3] [--submit-retry-interval 10s]
	hduwords update  [--repo owner/name] [--updates-dir .updates] [--yes] [--check-only]
	hduwords db stats --db <path>
	hduwords db export --db <path> [--format json|markdown] [--out <file>]

Commands:
	login      自动打开浏览器，完成统一身份认证后后台自动捕获 Token 并保存至本地
	addtoken   自动打开浏览器，登录后将 token 追加写入 .tokens（用于 collect 多账号并发）
	listtokens 查看 .token 与 .tokens 的账号列表及 primary 标识
	setprimary 设置 .tokens 的 primary 标识；默认同步到 .token 供 exam/test 使用
	collect    收集题库：支持 token 池并发采集；收集与练习统一使用 type=0
	test       练习答题测试：基于本地题库进行练习(type=0)作答
	exam       正式自动考试：基于本地题库进行正式考试作答
	update     检查并安装最新 CLI 发行版（二进制更新）
	db stats    查看本地题库统计信息（题目数、答案数、冲突数）
	db export   导出完整题库（包含题干、选项、正确答案），可用于还原官方题库
	db markdown, export-md, md 导出题库为 markdown 格式

Options:
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
		--unknown-policy     未知题处理：abort|skip|random（exam 会强制 random）
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

func runCmd(cmdName string, args []string) {
	fs := flag.NewFlagSet(cmdName, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	paperType := 0
	if cmdName == "exam" {
		paperType = 1
	}

	var (
		rawURL         = fs.String("url", "", "token url")
		dbPath         = fs.String("db", "hduwords.db", "sqlite db path")
		rate           = fs.Float64("rate", 2, "max requests per second")
		timeout        = fs.Duration("timeout", 15*time.Second, "http timeout")
		ua             = fs.String("ua", defaultUserAgent, "user-agent")
		examTime       = fs.Duration("time", 0, "exam only: wait before submitting")
		examScore      = fs.Int("score", -1, "exam only: target score percentage 0-100")
		dryRun         = fs.Bool("dry-run", false, "print decisions without submitting")
		unknownPolicy  = fs.String("unknown-policy", "abort", "abort|skip|random")
		submitRetries  = fs.Int("submit-retries", 3, "retry count for 403 on save/submit before creating new paper")
		submitRetryInt = fs.Duration("submit-retry-interval", 10*time.Second, "wait duration between 403 retries on save/submit")
	)

	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	uaFlagProvided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "ua" {
			uaFlagProvided = true
		}
	})
	if cmdName == "exam" {
		if uaFlagProvided {
			collectLog("WARN", "exam 模式强制使用移动端 UA (忽略 --ua)")
		}
		*ua = examMobileUserAgent
	}

	finalURL := getFinalTokenURL(*rawURL)

	policy, err := parseUnknownPolicy(*unknownPolicy)
	if err != nil {
		fatalErr(err)
	}
	if cmdName == "exam" && policy != unknownRandom {
		collectLog("WARN", "exam 模式未知题策略强制 random (忽略 --unknown-policy=%s)", *unknownPolicy)
		policy = unknownRandom
	}
	retryCfg := submitRetryConfig{MaxRetries: *submitRetries, Interval: *submitRetryInt}.normalized()

	collectLog("INFO", "%s 模式启动: type=%d db=%s dryRun=%v submitRetries=%d retryInterval=%v", cmdName, paperType, *dbPath, *dryRun, retryCfg.MaxRetries, retryCfg.Interval)
	if cmdName == "exam" {
		collectLog("INFO", "exam 参数: time=%v score=%d", *examTime, *examScore)
	}

	contextTimeout := 5 * time.Minute
	if cmdName == "exam" {
		contextTimeout = *examTime + 15*time.Minute
		if contextTimeout < 20*time.Minute {
			contextTimeout = 20 * time.Minute
		}
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
			if cmdName == "exam" {
				paper, err = cl.CreateExamPaper(ctx, paperType)
			} else {
				paper, err = cl.GetOrCreateActivePaper(ctx, paperType)
			}
		}
		if err != nil {
			if cmdName == "exam" {
				fatalErr(fmt.Errorf("CreateExamPaper(exam): %w", err))
			} else {
				fatalErr(err)
			}
		}

		detail, err := cl.PaperDetail(ctx, paper.PaperID)
		if err != nil {
			if attempt == 0 && isForbiddenAPIError(err) {
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
		if cmdName == "exam" && *examScore >= 0 {
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
			if cmdName == "exam" && *examScore >= 0 {
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

		if cmdName == "exam" && *examScore >= 0 {
			collectLog("INFO", "exam 评分控制: 已保留正确=%d 目标正确=%d 总题=%d", correctAssigned, targetCorrect, len(detail.List))
		}

		collectLog("INFO", "试卷=%s 总题=%d 命中=%d 未命中=%d", paper.PaperID, len(detail.List), hit, miss)

		if *dryRun {
			collectLog("INFO", "dry-run 已开启，不提交答案")
			return
		}

		if cmdName == "exam" && *examTime > 0 {
			collectLog("INFO", "exam 模式进度条等待 %v 后交卷", *examTime)
			if err := waitWithProgressBar(ctx, *examTime, "等待交卷"); err != nil {
				fatalErr(err)
			}
		}

		if len(submission) > 0 {
			if err := retryForbiddenSubmit(ctx, "", "PaperSave", retryCfg, func() error {
				return cl.PaperSave(ctx, paper.PaperID, submission)
			}); err != nil {
				if attempt == 0 && isForbiddenAPIError(err) {
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

		if err := retryForbiddenSubmit(ctx, "", "PaperSubmit", retryCfg, func() error {
			return cl.PaperSubmit(ctx, paper.PaperID)
		}); err != nil {
			if attempt == 0 && isForbiddenAPIError(err) {
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

		if cmdName == "exam" {
			if ok, listErr := paperInList(ctx, cl, paper.PaperID, paperType); listErr != nil {
				collectLog("WARN", "[%s] exam 结果校验失败: %v", cmdName, listErr)
			} else if !ok {
				collectLog("WARN", "[%s] exam 试卷未出现在列表中: %s", cmdName, paper.PaperID)
			}
		}

		if cmdName == "exam" || cmdName == "test" {
			added, updated, skipped, err := upsertCollectedAnswers(ctx, st, res)
			if err != nil {
				fatalErr(err)
			}
			collectLog("OK", "题目回收: 模式=%s 试卷=%s 入库[新增=%d 更新=%d 跳过=%d]", cmdName, res.PaperID, added, updated, skipped)
		}

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
		ua             = fs.String("ua", defaultUserAgent, "user-agent")
		cooldown       = fs.Duration("cooldown", 5*time.Minute, "cooldown between rounds")
		poolFile       = fs.String("pool-file", ".tokens", "token pool file path")
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

	paperType := 0
	retryCfg := submitRetryConfig{MaxRetries: *submitRetries, Interval: *submitRetryInt}.normalized()

	poolTokens, err := loadPoolTokens(*poolFile)
	if err != nil {
		fatalErr(fmt.Errorf("load token pool: %w", err))
	}

	workerURLs := make([]string, 0)
	if len(poolTokens) > 0 {
		for _, tk := range poolTokens {
			workerURLs = append(workerURLs, tokenToURL(tk))
		}
	} else {
		workerURLs = append(workerURLs, getFinalTokenURL(*rawURL))
	}

	workerCount := len(workerURLs)
	if *workers > 0 && *workers < workerCount {
		workerCount = *workers
	}
	if workerCount <= 0 {
		fatalf("可用 token 数为 0，请先执行 hduwords addtoken 或提供 --url")
	}

	collectLog("INFO", "进入收集模式：workers=%d tokenPool=%d cooldown=%v submitRetries=%d retryInterval=%v，按 Ctrl+C 退出",
		workerCount, len(workerURLs), *cooldown, retryCfg.MaxRetries, retryCfg.Interval)

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		workerTag := fmt.Sprintf("w%02d", i+1)
		workerURL := workerURLs[i]
		wg.Add(1)
		go func(tag, raw string) {
			defer wg.Done()
			cl, err := sklclient.NewFromTokenURL(raw, sklclient.Options{
				BaseUserAgent: *ua,
				Timeout:       *timeout,
				MaxRPS:        *rate,
			})
			if err != nil {
				collectLog("ERROR", "[%s] 初始化客户端失败: %v", tag, err)
				return
			}
			runCollectLoop(ctx, tag, cl, st, paperType, *cooldown, retryCfg)
		}(workerTag, workerURL)
	}

	<-ctx.Done()
	collectLog("INFO", "收到退出信号，等待 worker 结束")
	wg.Wait()
	collectLog("INFO", "收集结束")
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
		// 题库有则答，没有则跳过(提交空)
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

		// 提交后重新拉取明细，获取官方正确答案
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

func isForbiddenAPIError(err error) bool {
	var apiErr *sklclient.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == 403
}

type submitRetryConfig struct {
	MaxRetries int
	Interval   time.Duration
}

func (c submitRetryConfig) normalized() submitRetryConfig {
	if c.MaxRetries < 0 {
		c.MaxRetries = 0
	}
	if c.Interval <= 0 {
		c.Interval = 10 * time.Second
	}
	return c
}

func retryForbiddenSubmit(ctx context.Context, workerTag, opName string, cfg submitRetryConfig, fn func() error) error {
	err := fn()
	if err == nil || !isForbiddenAPIError(err) || cfg.MaxRetries == 0 {
		return err
	}

	for i := 1; i <= cfg.MaxRetries; i++ {
		if workerTag != "" {
			collectLog("WARN", "[%s] %s 返回 403，%v 后进行第 %d/%d 次重试", workerTag, opName, cfg.Interval, i, cfg.MaxRetries)
		} else {
			collectLog("WARN", "%s 返回 403，%v 后进行第 %d/%d 次重试", opName, cfg.Interval, i, cfg.MaxRetries)
		}
		if err := waitWithContext(ctx, cfg.Interval); err != nil {
			return err
		}
		err = fn()
		if err == nil {
			if workerTag != "" {
				collectLog("OK", "[%s] %s 403 重试成功", workerTag, opName)
			} else {
				collectLog("OK", "%s 403 重试成功", opName)
			}
			return nil
		}
		if !isForbiddenAPIError(err) {
			return err
		}
	}

	return err
}

func waitWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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

func upsertCollectedAnswers(ctx context.Context, st *store.Store, res sklclient.PaperDetail) (int, int, int, error) {
	added, updated, skipped := 0, 0, 0
	for _, q := range res.List {
		answerChoice := q.Answer
		if answerChoice == "" && q.Right != nil && *q.Right {
			answerChoice = q.Input
		}
		if answerChoice == "" {
			skipped++
			continue
		}
		cidx, ok := sklclient.ChoiceToIndex(answerChoice)
		if !ok {
			skipped++
			continue
		}
		a, u, err := st.UpsertAnswer(ctx, q.Title, q.Options(), q.Options()[cidx], "api_detail")
		if err != nil {
			return 0, 0, 0, fmt.Errorf("UpsertAnswer: %w", err)
		}
		added += a
		updated += u
	}
	return added, updated, skipped, nil
}

func dbCmd(args []string) {
	if len(args) < 1 {
		fatalf("db subcommand required (stats|export|markdown)")
	}
	switch args[0] {
	case "stats":
		dbStatsCmd(args[1:])
	case "export":
		dbExportCmd(args[1:])
	case "markdown", "export-md", "md":
		dbMarkdownCmd(args[1:])
	default:
		fatalf("unknown db subcommand: %s", args[0])
	}
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

var errTimeRegexp = regexp.MustCompile(`上次申请时间(\d{2}:\d{2}:\d{2})`)

func calcDynamicCooldown(errMsg string, defaultCooldown time.Duration) time.Duration {
	m := errTimeRegexp.FindStringSubmatch(errMsg)
	if len(m) < 2 {
		return defaultCooldown
	}
	timeStr := m[1]

	now := time.Now()
	// parse "HH:mm:ss" using current year/month/day
	layout := "15:04:05"
	t, err := time.ParseInLocation(layout, timeStr, now.Location())
	if err != nil {
		return defaultCooldown
	}

	lastReqTime := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), t.Second(), 0, now.Location())

	// if lastReqTime is in the future, it might be due to timezone/clock skew, or crossing midnight.
	// if it crosses midnight (e.g. now is 00:01 but last request was 23:58), adjust the day.
	if lastReqTime.After(now) {
		lastReqTime = lastReqTime.Add(-24 * time.Hour)
	}

	elapsed := now.Sub(lastReqTime)
	if elapsed >= defaultCooldown {
		return 5 * time.Second // 已经超过冷却时间但可能服务器时间不一致，给个最小等待时间
	}
	return defaultCooldown - elapsed + 2*time.Second // 加 2 秒冗余，避免刚卡点请求失败
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
