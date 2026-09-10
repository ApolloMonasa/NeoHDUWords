package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
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
	"hduwords/internal/updater"
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
	name := filepath.Base(os.Args[0])
	fmt.Fprintf(os.Stderr, `%[1]s - HDU 我爱记单词 CLI

Usage:
	%[1]s login    [--browser chrome|edge] [--alias <name>]
	%[1]s addtoken [--browser chrome|edge] [--alias <name>]
	%[1]s listtokens [--accounts accounts.json] [--show-plain]
	%[1]s setprimary --token <token> [--accounts accounts.json]
	%[1]s collect [--url <token_url>] --db <path> [--rate 2] [--timeout 15s] [--ua <ua>] [--cooldown 5m] [--accounts accounts.json] [--workers 0] [--submit-retries 3] [--submit-retry-interval 10s]
	%[1]s exam    [--url <token_url>] --db <path> [--rate 2] [--timeout 15s] [--time 30s] [--score 100] [--dry-run] [--submit-retries 3] [--submit-retry-interval 10s]
	%[1]s update  [--repo owner/name] [--updates-dir .updates] [--yes] [--check-only]
	%[1]s db stats --db <path>
	%[1]s db export --db <path> [--format json|markdown] [--out <file>]
	%[1]s db update [--out <file>]
	%[1]s db conflicts [--db <path>] [--limit 20]

Commands:
	login      自动打开浏览器，完成统一身份认证后捕获凭证，写入凭证库并设为主账号
	addtoken   自动打开浏览器，登录后把凭证追加写入凭证库（用于 collect 多账号并发）
	listtokens 查看凭证库账号列表及主账号标识
	setprimary 设置凭证库的主账号，exam 默认使用该账号
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
		--url                如不提供，则默认从 '%[1]s login' 生成的凭证库（accounts.json）主账号中读取。也可手动提供带有 token 的网址
		--db                 数据库路径，默认当前目录下的 hduwords.db
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
		--accounts           凭证库文件，默认 accounts.json
		--workers            并发 worker 数，默认自动

	Update only:
		--repo               发布仓库，默认 ApolloMonasa/NeoHDUWords
		--updates-dir        更新包下载目录，默认 .updates
		--yes                跳过确认，直接安装
		--check-only         只检查是否有更新，不安装
`, name)
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

	if _, err := updater.Run(context.Background(), updater.Options{
		Repo:       repo,
		BinaryName: "cli",
		UpdatesDir: *updatesDirFlag,
		Reader:     bufio.NewReader(os.Stdin),
		AutoYes:    *yesFlag,
		CheckOnly:  *checkOnlyFlag,
		ApplyArgs: func(source, target string) []string {
			return []string{"apply-update", "--source", source, "--target", target}
		},
	}); err != nil {
		fatalErr(err)
	}
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
		submitRetries  = fs.Int("submit-retries", 3, "retry count for 403 on save/submit before creating new paper")
		submitRetryInt = fs.Duration("submit-retry-interval", 10*time.Second, "wait duration between 403 retries on save/submit")
	)

	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	// exam 模式强制移动端 UA
	*ua = sklclient.ExamMobileUserAgent

	finalURL := getFinalTokenURL(*rawURL)
	retryCfg := engine.SubmitRetryConfig{MaxRetries: *submitRetries, Interval: *submitRetryInt}.Normalized()

	collectLog("INFO", "exam 模式启动: type=%d db=%s dryRun=%v submitRetries=%d retryInterval=%v",
		engine.PaperTypeExam, *dbPath, *dryRun, retryCfg.MaxRetries, retryCfg.Interval)
	collectLog("INFO", "exam 参数: time=%v score=%d", *examTime, *examScore)

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

	if err := engine.RunExam(context.Background(), engine.ExamOptions{
		Client:           cl,
		Store:            st,
		WaitBeforeSubmit: *examTime,
		TargetScore:      *examScore,
		DryRun:           *dryRun,
		Retry:            retryCfg,
		Log:              collectLog,
	}); err != nil {
		fatalErr(err)
	}
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
	// 进度日志走 stdout；错误输出由 fatalf/fatalErr 走 stderr
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
		accounts       = fs.String("accounts", tokenpool.DefaultAccountsFile, "accounts store file path")
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

	acctStore, err := tokenpool.LoadStore(*accounts)
	if err != nil {
		fatalErr(fmt.Errorf("load accounts store: %w", err))
	}
	notifyMigrated(acctStore)

	workerSpecs := engine.BuildWorkers(acctStore.Tokens(), getFinalTokenURL(*rawURL), *workers, sklclient.Options{
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

func dbCmd(args []string) {
	if len(args) < 1 {
		fatalf("db subcommand required (stats|export|markdown|update|conflicts)")
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
	case "conflicts":
		dbConflictsCmd(args[1:])
	default:
		fatalf("unknown db subcommand: %s", args[0])
	}
}

func dbConflictsCmd(args []string) {
	fs := flag.NewFlagSet("db conflicts", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dbPath := fs.String("db", "hduwords.db", "sqlite db path")
	limit := fs.Int("limit", 20, "max conflicts to show")
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

	conflicts, err := st.ListConflicts(ctx, *limit)
	if err != nil {
		fatalErr(err)
	}
	if len(conflicts) == 0 {
		fmt.Println("没有答案冲突记录")
		return
	}
	fmt.Printf("共 %d 条冲突（按观测时间倒序）：\n\n", len(conflicts))
	for i, c := range conflicts {
		fmt.Printf("%d. %s\n", i+1, c.Stem)
		fmt.Printf("   旧答案 %q → 新答案 %q（当前采用 %q），观测于 %s，来源 %s\n",
			c.OldCorrect, c.NewCorrect, c.Current, c.ObservedAt, c.Source)
	}
}
func dbUpdateCmd(args []string) {
	fs := flag.NewFlagSet("db update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	outFile := fs.String("out", "", "output file path (default hduwords.db in current directory)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	dest := strings.TrimSpace(*outFile)
	if dest == "" {
		dest = "hduwords.db"
	}

	asset := updatecheck.DBAsset()

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
