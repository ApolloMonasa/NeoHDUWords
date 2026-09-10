package tuiapp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"hduwords/internal/browser"
	"hduwords/internal/engine"
	"hduwords/internal/sklclient"
	"hduwords/internal/store"
	"hduwords/internal/tokenpool"
	"hduwords/internal/updatecheck"
)

func runLoginDirect(reader *bufio.Reader) {
	browserType, _ := readLine(reader, "浏览器种类（chrome/edge，留空自动检测）")
	browserType = strings.TrimSpace(browserType)

	token, err := browser.CaptureTokenByLogin(browserType)
	if err != nil {
		fmt.Printf("登录失败：%v\n", err)
		return
	}
	fmt.Println(">>> 成功捕获到 Token!")
	_ = tokenpool.SaveMain(tokenpool.DefaultMainFile, token)
	if err := tokenpool.SetPrimary(tokenpool.DefaultPoolFile, token); err != nil {
		fmt.Printf(">>> 警告: 同步 .tokens 主账号标识失败: %v\n", err)
	}
	fmt.Println(">>> 已保存 Token 到本地 .token 文件。")
}

func runAddTokenDirect(reader *bufio.Reader) {
	browserType, _ := readLine(reader, "浏览器种类（chrome/edge，留空自动检测）")
	browserType = strings.TrimSpace(browserType)

	token, err := browser.CaptureTokenByLogin(browserType)
	if err != nil {
		fmt.Printf("addtoken 登录失败：%v\n", err)
		return
	}
	added, err := tokenpool.Append(tokenpool.DefaultPoolFile, token)
	if err != nil {
		fmt.Printf("写入 token 池失败：%v\n", err)
		return
	}
	if added {
		fmt.Println(">>> 已新增到 .tokens，可用于 collect 多账号并发采集。")
	} else {
		fmt.Println(">>> .tokens 中已存在该 token，未重复写入。")
	}
}

func runListTokensDirect(reader *bufio.Reader) {
	poolFile, _ := readLine(reader, "token 池文件 [.tokens]")
	if strings.TrimSpace(poolFile) == "" {
		poolFile = ".tokens"
	}
	showPlain := promptYesNoWithReader(reader, "是否显示完整 token 文本？", false)

	mainToken, _ := tokenpool.LoadMain(tokenpool.DefaultMainFile)
	pool, err := tokenpool.Load(poolFile)
	if err != nil {
		fmt.Printf("读取 token 池失败：%v\n", err)
		return
	}
	fmt.Printf("主账号(.token): %s\n", tokenpool.Format(mainToken, showPlain))
	fmt.Printf("token池(%s): 共 %d 个\n", poolFile, len(pool.Tokens))
	for i, tk := range pool.Tokens {
		role := "member"
		if pool.Primary != "" && tk == pool.Primary {
			role = "primary"
		}
		bind := ""
		if mainToken != "" && tk == mainToken {
			bind = " [= .token]"
		}
		fmt.Printf("%d. (%s)%s %s\n", i+1, role, bind, tokenpool.Format(tk, showPlain))
	}
}

func runSetPrimaryDirect(reader *bufio.Reader) {
	poolFile, _ := readLine(reader, "token 池文件 [.tokens]")
	if strings.TrimSpace(poolFile) == "" {
		poolFile = ".tokens"
	}
	tk, _ := readLine(reader, "请输入要设置为主账号的 token（留空则读取 .token）")
	tk = strings.TrimSpace(tk)
	if tk == "" {
		var err error
		tk, err = tokenpool.LoadMain(tokenpool.DefaultMainFile)
		if err != nil || tk == "" {
			fmt.Println("未提供 token 且本地 .token 不可用")
			return
		}
	}
	if err := tokenpool.SetPrimary(poolFile, tk); err != nil {
		fmt.Printf("设置主账号失败：%v\n", err)
		return
	}
	if promptYesNoWithReader(reader, "是否同步写入 .token（供 exam 默认使用）？", true) {
		_ = tokenpool.SaveMain(tokenpool.DefaultMainFile, tk)
		fmt.Println(">>> 已同步 .token，exam 将使用该账号。")
	}
	fmt.Printf(">>> 已设置主账号(primary): %s\n", tokenpool.Format(tk, false))
}

func runCollectDirect(reader *bufio.Reader) {
	baseCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(baseCtx)
	defer cancel()

	dbPath, _ := readLine(reader, "数据库路径 [hduwords.db]")
	if strings.TrimSpace(dbPath) == "" {
		dbPath = "hduwords.db"
	}
	tokenURL := promptTokenURL(reader, false)
	rateStr, _ := readLine(reader, "请求速率 [2]")
	if strings.TrimSpace(rateStr) == "" {
		rateStr = "2"
	}
	rate, _ := strconv.ParseFloat(strings.TrimSpace(rateStr), 64)
	timeoutStr, _ := readLine(reader, "超时 [15s]")
	if strings.TrimSpace(timeoutStr) == "" {
		timeoutStr = "15s"
	}
	ua, _ := readLine(reader, "UA [默认桌面 Chrome]")
	if strings.TrimSpace(ua) == "" {
		ua = sklclient.DefaultUserAgent
	}
	cooldownStr, _ := readLine(reader, "冷却时间 [5m]")
	if strings.TrimSpace(cooldownStr) == "" {
		cooldownStr = "5m"
	}
	poolFile, _ := readLine(reader, "token 池文件 [.tokens]")
	if strings.TrimSpace(poolFile) == "" {
		poolFile = tokenpool.DefaultPoolFile
	}
	workersStr, _ := readLine(reader, "worker 数 [0]")
	if strings.TrimSpace(workersStr) == "" {
		workersStr = "0"
	}
	workers, _ := strconv.Atoi(strings.TrimSpace(workersStr))
	submitRetriesStr, _ := readLine(reader, "提交 403 重试次数 [3]")
	if strings.TrimSpace(submitRetriesStr) == "" {
		submitRetriesStr = "3"
	}
	submitRetryIntStr, _ := readLine(reader, "提交 403 重试间隔 [10s]")
	if strings.TrimSpace(submitRetryIntStr) == "" {
		submitRetryIntStr = "10s"
	}
	submitRetries, _ := strconv.Atoi(strings.TrimSpace(submitRetriesStr))

	retryCfg := engine.SubmitRetryConfig{MaxRetries: submitRetries, Interval: mustDuration(submitRetryIntStr, 10*time.Second)}.Normalized()
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Printf("打开数据库失败：%v\n", err)
		return
	}
	defer st.Close()

	pool, err := tokenpool.Load(poolFile)
	if err != nil {
		fmt.Printf("加载 token 池失败：%v\n", err)
		return
	}

	specs := engine.BuildWorkers(pool.Tokens, resolveURLForTUI(tokenURL), workers, sklclient.Options{
		BaseUserAgent: ua,
		Timeout:       mustDuration(timeoutStr, 15*time.Second),
		MaxRPS:        rate,
	}, collectLog)
	if len(specs) == 0 {
		fmt.Println("可用 token 数为 0")
		return
	}

	fmt.Println("按 Ctrl+C 返回主菜单")
	engine.RunCollectPool(ctx, engine.CollectPoolOptions{
		Workers:  specs,
		Store:    st,
		Cooldown: mustDuration(cooldownStr, 5*time.Minute),
		Retry:    retryCfg,
		Log:      collectLog,
	})
	fmt.Println("收集已停止，返回主菜单")
}

func runExamDirect(reader *bufio.Reader) {
	runExamLikeDirect(reader)
}

func runExamLikeDirect(reader *bufio.Reader) {
	dbPath, _ := readLine(reader, "数据库路径 [hduwords.db]")
	if strings.TrimSpace(dbPath) == "" {
		dbPath = "hduwords.db"
	}
	tokenURL := promptTokenURL(reader, false)
	paperType := 1
	timeWait, _ := readLine(reader, "交卷前等待时长 [30s]")
	if strings.TrimSpace(timeWait) == "" {
		timeWait = "30s"
	}
	waitBeforeSubmit := mustDuration(timeWait, 30*time.Second)
	scoreStr, _ := readLine(reader, "目标得分百分比 [-1]")
	if strings.TrimSpace(scoreStr) == "" {
		scoreStr = "-1"
	}
	score, _ := strconv.Atoi(strings.TrimSpace(scoreStr))
	dryRun := promptYesNoWithReader(reader, "是否 dry-run？", false)
	rateStr, _ := readLine(reader, "请求速率 [2]")
	if strings.TrimSpace(rateStr) == "" {
		rateStr = "2"
	}
	rate, _ := strconv.ParseFloat(strings.TrimSpace(rateStr), 64)
	timeoutStr, _ := readLine(reader, "超时 [15s]")
	if strings.TrimSpace(timeoutStr) == "" {
		timeoutStr = "15s"
	}
	timeout := mustDuration(timeoutStr, 15*time.Second)
	unknownPolicy := "random"
	unknownPolicyInput, _ := readLine(reader, fmt.Sprintf("未知题策略 [abort|skip|random] (%s)", unknownPolicy))
	if strings.TrimSpace(unknownPolicyInput) != "" {
		unknownPolicy = strings.TrimSpace(unknownPolicyInput)
	}
	ua := sklclient.ExamMobileUserAgent
	submitRetriesStr, _ := readLine(reader, "提交 403 重试次数 [3]")
	if strings.TrimSpace(submitRetriesStr) == "" {
		submitRetriesStr = "3"
	}
	submitRetryIntStr, _ := readLine(reader, "提交 403 重试间隔 [10s]")
	if strings.TrimSpace(submitRetryIntStr) == "" {
		submitRetryIntStr = "10s"
	}
	submitRetries, _ := strconv.Atoi(strings.TrimSpace(submitRetriesStr))
	retryCfg := engine.SubmitRetryConfig{MaxRetries: submitRetries, Interval: mustDuration(submitRetryIntStr, 10*time.Second)}.Normalized()

	contextTimeout := waitBeforeSubmit + 15*time.Minute
	if contextTimeout < 20*time.Minute {
		contextTimeout = 20 * time.Minute
	}
	reqCtx, cancel := context.WithTimeout(context.Background(), contextTimeout)
	defer cancel()

	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Printf("打开数据库失败：%v\n", err)
		return
	}
	defer st.Close()

	cl, err := sklclient.NewFromTokenURL(resolveURLForTUI(tokenURL), sklclient.Options{BaseUserAgent: ua, Timeout: timeout, MaxRPS: rate})
	if err != nil {
		fmt.Printf("初始化客户端失败：%v\n", err)
		return
	}

	policy, err := parseUnknownPolicy(unknownPolicy)
	if err != nil {
		fmt.Printf("unknown-policy 错误：%v\n", err)
		return
	}

	runPaperFlow(reqCtx, st, cl, paperType, waitBeforeSubmit, score, dryRun, policy, retryCfg)
}

func runPaperFlow(ctx context.Context, st *store.Store, cl *sklclient.Client, paperType int, waitBeforeSubmit time.Duration, targetScore int, dryRun bool, policy unknownPolicy, retryCfg engine.SubmitRetryConfig) {
	if policy != unknownRandom {
		policy = unknownRandom
	}

	var retryPaper *sklclient.Paper
	for attempt := 0; attempt < 2; attempt++ {
		var paper sklclient.Paper
		var err error
		if retryPaper != nil {
			paper = *retryPaper
			retryPaper = nil
		} else {
			paper, err = cl.CreateExamPaper(ctx, paperType)
		}
		if err != nil {
			fmt.Printf("获取试卷失败：%v\n", err)
			return
		}

		detail, err := cl.PaperDetail(ctx, paper.PaperID)
		if err != nil {
			if attempt == 0 && engine.IsForbiddenAPIError(err) {
				fmt.Println("PaperDetail 返回 403，尝试新建试卷重试")
				newPaper, nerr := cl.CreateExamPaper(ctx, paperType)
				if nerr != nil {
					fmt.Printf("重建试卷失败：%v\n", nerr)
					return
				}
				tmp := newPaper
				retryPaper = &tmp
				continue
			}
			fmt.Printf("获取试卷详情失败：%v\n", err)
			return
		}

		targetCorrect := -1
		correctAssigned := 0
		if targetScore >= 0 {
			if targetScore > 100 {
				fmt.Println("--score 必须在 0-100 之间")
				return
			}
			targetCorrect = (len(detail.List)*targetScore + 50) / 100
		}

		submission := make([]sklclient.Question, 0, len(detail.List))
		hit, miss := 0, 0
		for _, q := range detail.List {
			stem := q.Title
			opts := q.Options()
			var input string
			correctText, ok, err := st.FindAnswerText(ctx, stem, opts)
			if err != nil {
				fmt.Printf("查询题库失败：%v\n", err)
				return
			}
			if targetScore >= 0 {
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
					fmt.Printf("未知题：%s\n", stem)
					return
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
				}
			}

			if !dryRun && input != "" {
				submission = append(submission, q)
			}
		}

		fmt.Printf("试卷=%s 总题=%d 命中=%d 未命中=%d\n", paper.PaperID, len(detail.List), hit, miss)
		if dryRun {
			fmt.Println("dry-run 已开启，不提交答案")
			return
		}
		if waitBeforeSubmit > 0 {
			fmt.Printf("等待交卷：%v\n", waitBeforeSubmit)
			if err := waitWithProgressBar(ctx, waitBeforeSubmit, "等待交卷"); err != nil {
				fmt.Printf("等待被中断：%v\n", err)
				return
			}
		}

		if len(submission) > 0 {
			if err := engine.RetryForbiddenSubmit(ctx, "", "PaperSave", retryCfg, collectLog, func() error { return cl.PaperSave(ctx, paper.PaperID, submission) }); err != nil {
				if attempt == 0 && engine.IsForbiddenAPIError(err) {
					fmt.Println("PaperSave 返回 403，尝试新建试卷重试")
					newPaper, nerr := cl.CreateExamPaper(ctx, paperType)
					if nerr != nil {
						fmt.Printf("重建试卷失败：%v\n", nerr)
						return
					}
					tmp := newPaper
					retryPaper = &tmp
					continue
				}
				fmt.Printf("提交答案失败：%v\n", err)
				return
			}
		}
		if err := engine.RetryForbiddenSubmit(ctx, "", "PaperSubmit", retryCfg, collectLog, func() error { return cl.PaperSubmit(ctx, paper.PaperID) }); err != nil {
			if attempt == 0 && engine.IsForbiddenAPIError(err) {
				fmt.Println("PaperSubmit 返回 403，尝试新建试卷重试")
				newPaper, nerr := cl.CreateExamPaper(ctx, paperType)
				if nerr != nil {
					fmt.Printf("重建试卷失败：%v\n", nerr)
					return
				}
				tmp := newPaper
				retryPaper = &tmp
				continue
			}
			fmt.Printf("交卷失败：%v\n", err)
			return
		}

		res, err := cl.PaperDetail(ctx, paper.PaperID)
		if err != nil {
			fmt.Printf("提交后拉取结果失败：%v\n", err)
			return
		}
		ok, listErr := paperInList(ctx, cl, paper.PaperID, paperType)
		if listErr != nil {
			fmt.Printf("exam 结果校验失败：%v\n", listErr)
		} else if !ok {
			fmt.Printf("exam 试卷未出现在列表中：%s\n", paper.PaperID)
		}
		added, updated, skipped, err := engine.UpsertCollectedAnswers(ctx, st, res)
		if err != nil {
			fmt.Printf("回收答案失败：%v\n", err)
			return
		}
		fmt.Printf("完成：试卷=%s 得分=%d 入库[新增=%d 更新=%d 跳过=%d]\n", res.PaperID, res.Mark, added, updated, skipped)
		return
	}

	fmt.Println("执行失败：重试后仍未完成")
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

func runDBStatsDirect(reader *bufio.Reader) {
	dbPath, _ := readLine(reader, "数据库路径 [hduwords.db]")
	if strings.TrimSpace(dbPath) == "" {
		dbPath = "hduwords.db"
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Printf("打开数据库失败：%v\n", err)
		return
	}
	defer st.Close()
	s, err := st.Stats(context.Background())
	if err != nil {
		fmt.Printf("统计失败：%v\n", err)
		return
	}
	fmt.Printf("items=%d answers=%d conflicts=%d\n", s.Items, s.Answers, s.Conflicts)
}

func runDBExportDirect(reader *bufio.Reader, markdown bool) {
	dbPath, _ := readLine(reader, "数据库路径 [hduwords.db]")
	if strings.TrimSpace(dbPath) == "" {
		dbPath = "hduwords.db"
	}
	outPath, _ := readLine(reader, "输出文件")
	if strings.TrimSpace(outPath) == "" {
		if markdown {
			outPath = "export.md"
		} else {
			outPath = "export.json"
		}
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Printf("打开数据库失败：%v\n", err)
		return
	}
	defer st.Close()
	items, err := st.Export(context.Background())
	if err != nil {
		fmt.Printf("导出失败：%v\n", err)
		return
	}
	f, err := os.Create(outPath)
	if err != nil {
		fmt.Printf("创建输出文件失败：%v\n", err)
		return
	}
	defer f.Close()
	if markdown {
		fmt.Fprintf(f, "# HDU Words 题库导出\n\n共 %d 题\n\n", len(items))
		for i, item := range items {
			fmt.Fprintf(f, "### %d. %s\n\n", i+1, item.Stem)
			for j, opt := range item.Options {
				prefix := "- [ ]"
				if j == item.CorrectIndex {
					prefix = "- [x]"
				}
				fmt.Fprintf(f, "%s %s. %s\n", prefix, string(rune('A'+j)), opt)
			}
			fmt.Fprintln(f)
		}
		return
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(items); err != nil {
		fmt.Printf("写入导出文件失败：%v\n", err)
	}
}

func runDBUpdateDirect() {
	exe, err := os.Executable()
	if err != nil {
		exe = "hduwords"
	}
	dest := filepath.Join(filepath.Dir(exe), "hduwords.db")

	asset := updatecheck.ReleaseAsset{
		Name: "hduwords.db",
		URL:  "https://github.com/ApolloMonasa/NeoHDUWords/releases/download/Data/hduwords.db",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fmt.Printf("正在下载数据库 %s ...\n", asset.URL)
	written, err := updatecheck.DownloadAsset(ctx, asset, dest)
	if err != nil {
		fmt.Printf("下载数据库失败：%v\n", err)
		return
	}
	fmt.Printf("数据库已保存至 %s (%d bytes)\n", dest, written)
	// Remove stale WAL/SHM files so subsequent opens read the fresh db.
	os.Remove(dest + "-wal")
	os.Remove(dest + "-shm")
}

func promptTokenURL(reader *bufio.Reader, optional bool) string {
	prompt := "token URL（留空则使用 .token）"
	if optional {
		prompt += " [可空]"
	}
	tokenURL, _ := readLine(reader, prompt)
	return strings.TrimSpace(tokenURL)
}

func resolveURLForTUI(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw != "" {
		return raw
	}
	token, err := tokenpool.LoadMain(tokenpool.DefaultMainFile)
	if err != nil || token == "" {
		return getFinalTokenURL("")
	}
	return fmt.Sprintf("https://skl.hdu.edu.cn/?type=6&token=%s#/english/list", token)
}

func mustDuration(raw string, fallback time.Duration) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}

func chooseWrongChoice(correct string, options []string) string {
	for _, opt := range options {
		if opt != "" && opt != correct {
			return opt
		}
	}
	return ""
}

func getFinalTokenURL(rawURL string) string {
	if rawURL != "" {
		return rawURL
	}
	token, err := tokenpool.LoadMain(tokenpool.DefaultMainFile)
	if err != nil || token == "" {
		return ""
	}
	return fmt.Sprintf("https://skl.hdu.edu.cn/?type=6&token=%s#/english/list", token)
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

func parseUnknownPolicy(s string) (unknownPolicy, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "abort":
		return unknownAbort, nil
	case "skip":
		return unknownSkip, nil
	case "random":
		return unknownRandom, nil
	default:
		return 0, fmt.Errorf("invalid unknown policy: %q", s)
	}
}

type unknownPolicy int

const (
	unknownAbort unknownPolicy = iota
	unknownSkip
	unknownRandom
)
