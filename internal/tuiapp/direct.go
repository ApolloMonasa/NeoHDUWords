package tuiapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"hduwords/internal/sklclient"
	"hduwords/internal/store"
)

func runLoginDirect(reader *bufio.Reader) {
	token, err := captureTokenByLogin()
	if err != nil {
		fmt.Printf("登录失败：%v\n", err)
		return
	}
	fmt.Println(">>> 成功捕获到 Token!")
	saveToken(token)
	if err := setPrimaryTokenInPool(".tokens", token); err != nil {
		fmt.Printf(">>> 警告: 同步 .tokens 主账号标识失败: %v\n", err)
	}
	fmt.Println(">>> 已保存 Token 到本地 .token 文件。")
}

func runAddTokenDirect(reader *bufio.Reader) {
	token, err := captureTokenByLogin()
	if err != nil {
		fmt.Printf("addtoken 登录失败：%v\n", err)
		return
	}
	added, err := appendPoolToken(".tokens", token)
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

	mainToken, _ := loadToken()
	pool, err := loadTokenPool(poolFile)
	if err != nil {
		fmt.Printf("读取 token 池失败：%v\n", err)
		return
	}
	fmt.Printf("主账号(.token): %s\n", formatToken(mainToken, showPlain))
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
		fmt.Printf("%d. (%s)%s %s\n", i+1, role, bind, formatToken(tk, showPlain))
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
		tk, err = loadToken()
		if err != nil || tk == "" {
			fmt.Println("未提供 token 且本地 .token 不可用")
			return
		}
	}
	if err := setPrimaryTokenInPool(poolFile, tk); err != nil {
		fmt.Printf("设置主账号失败：%v\n", err)
		return
	}
	if promptYesNoWithReader(reader, "是否同步写入 .token（供 exam/test 默认使用）？", true) {
		saveToken(tk)
		fmt.Println(">>> 已同步 .token，exam/test 将使用该账号。")
	}
	fmt.Printf(">>> 已设置主账号(primary): %s\n", formatToken(tk, false))
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
	paperType := 0
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
		ua = defaultUserAgent
	}
	cooldownStr, _ := readLine(reader, "冷却时间 [5m]")
	if strings.TrimSpace(cooldownStr) == "" {
		cooldownStr = "5m"
	}
	poolFile, _ := readLine(reader, "token 池文件 [.tokens]")
	if strings.TrimSpace(poolFile) == "" {
		poolFile = ".tokens"
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
	submitRetryInt := mustDuration(submitRetryIntStr, 10*time.Second)

	retryCfg := submitRetryConfig{MaxRetries: submitRetries, Interval: submitRetryInt}.normalized()
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Printf("打开数据库失败：%v\n", err)
		return
	}
	defer st.Close()

	poolTokens, err := loadPoolTokens(poolFile)
	if err != nil {
		fmt.Printf("加载 token 池失败：%v\n", err)
		return
	}
	workerURLs := make([]string, 0)
	if len(poolTokens) > 0 {
		for _, tk := range poolTokens {
			workerURLs = append(workerURLs, tokenToURL(tk))
		}
	} else if tokenURL != "" {
		workerURLs = append(workerURLs, tokenURL)
	} else {
		workerURLs = append(workerURLs, getFinalTokenURL(""))
	}

	workerCount := len(workerURLs)
	if workers > 0 && workers < workerCount {
		workerCount = workers
	}
	if workerCount <= 0 {
		fmt.Println("可用 token 数为 0")
		return
	}
	collectLog("INFO", "进入收集模式：workers=%d tokenPool=%d cooldown=%v submitRetries=%d retryInterval=%v", workerCount, len(workerURLs), cooldownStr, retryCfg.MaxRetries, retryCfg.Interval)

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		workerTag := fmt.Sprintf("w%02d", i+1)
		workerURL := workerURLs[i]
		wg.Add(1)
		go func(tag, raw string) {
			defer wg.Done()
			cl, err := sklclient.NewFromTokenURL(raw, sklclient.Options{BaseUserAgent: ua, Timeout: mustDuration(timeoutStr, 15*time.Second), MaxRPS: rate})
			if err != nil {
				fmt.Printf("[%s] 初始化客户端失败：%v\n", tag, err)
				return
			}
			runCollectLoop(ctx, tag, cl, st, paperType, mustDuration(cooldownStr, 5*time.Minute), retryCfg)
		}(workerTag, workerURL)
	}
	fmt.Println("按 Ctrl+C 返回主菜单")
	<-ctx.Done()
	wg.Wait()
	fmt.Println("收集已停止，返回主菜单")
}

func runTestDirect(reader *bufio.Reader) {
	runExamLikeDirect(reader, false)
}

func runExamDirect(reader *bufio.Reader) {
	runExamLikeDirect(reader, true)
}

func runExamLikeDirect(reader *bufio.Reader, examMode bool) {
	dbPath, _ := readLine(reader, "数据库路径 [hduwords.db]")
	if strings.TrimSpace(dbPath) == "" {
		dbPath = "hduwords.db"
	}
	tokenURL := promptTokenURL(reader, false)
	paperType := 0
	if examMode {
		paperType = 1
	}
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
	unknownPolicy := "abort"
	if examMode {
		unknownPolicy = "random"
	}
	unknownPolicyInput, _ := readLine(reader, fmt.Sprintf("未知题策略 [abort|skip|random] (%s)", unknownPolicy))
	if strings.TrimSpace(unknownPolicyInput) != "" {
		unknownPolicy = strings.TrimSpace(unknownPolicyInput)
	}
	ua := defaultUserAgent
	if examMode {
		ua = examMobileUserAgent
	} else {
		uaInput, _ := readLine(reader, "UA [默认桌面 Chrome]")
		if strings.TrimSpace(uaInput) != "" {
			ua = strings.TrimSpace(uaInput)
		}
	}
	submitRetriesStr, _ := readLine(reader, "提交 403 重试次数 [3]")
	if strings.TrimSpace(submitRetriesStr) == "" {
		submitRetriesStr = "3"
	}
	submitRetryIntStr, _ := readLine(reader, "提交 403 重试间隔 [10s]")
	if strings.TrimSpace(submitRetryIntStr) == "" {
		submitRetryIntStr = "10s"
	}
	submitRetries, _ := strconv.Atoi(strings.TrimSpace(submitRetriesStr))
	retryCfg := submitRetryConfig{MaxRetries: submitRetries, Interval: mustDuration(submitRetryIntStr, 10*time.Second)}.normalized()

	contextTimeout := 5 * time.Minute
	if examMode {
		contextTimeout = waitBeforeSubmit + 15*time.Minute
		if contextTimeout < 20*time.Minute {
			contextTimeout = 20 * time.Minute
		}
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

	runPaperFlow(reqCtx, st, cl, paperType, waitBeforeSubmit, score, dryRun, policy, examMode, retryCfg)
}

func runPaperFlow(ctx context.Context, st *store.Store, cl *sklclient.Client, paperType int, waitBeforeSubmit time.Duration, targetScore int, dryRun bool, policy unknownPolicy, examMode bool, retryCfg submitRetryConfig) {
	if examMode && policy != unknownRandom {
		policy = unknownRandom
	}

	var retryPaper *sklclient.Paper
	for attempt := 0; attempt < 2; attempt++ {
		var paper sklclient.Paper
		var err error
		if retryPaper != nil {
			paper = *retryPaper
			retryPaper = nil
		} else if examMode {
			paper, err = cl.CreateExamPaper(ctx, paperType)
		} else {
			paper, err = cl.GetOrCreateActivePaper(ctx, paperType)
		}
		if err != nil {
			fmt.Printf("获取试卷失败：%v\n", err)
			return
		}

		detail, err := cl.PaperDetail(ctx, paper.PaperID)
		if err != nil {
			if attempt == 0 && isForbiddenAPIError(err) {
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
		if examMode && targetScore >= 0 {
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
			if examMode && targetScore >= 0 {
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
		if examMode && waitBeforeSubmit > 0 {
			fmt.Printf("等待交卷：%v\n", waitBeforeSubmit)
			if err := waitWithProgressBar(ctx, waitBeforeSubmit, "等待交卷"); err != nil {
				fmt.Printf("等待被中断：%v\n", err)
				return
			}
		}

		if len(submission) > 0 {
			if err := retryForbiddenSubmit(ctx, "", "PaperSave", retryCfg, func() error { return cl.PaperSave(ctx, paper.PaperID, submission) }); err != nil {
				if attempt == 0 && isForbiddenAPIError(err) {
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
		if err := retryForbiddenSubmit(ctx, "", "PaperSubmit", retryCfg, func() error { return cl.PaperSubmit(ctx, paper.PaperID) }); err != nil {
			if attempt == 0 && isForbiddenAPIError(err) {
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
		if examMode {
			ok, listErr := paperInList(ctx, cl, paper.PaperID, paperType)
			if listErr != nil {
				fmt.Printf("exam 结果校验失败：%v\n", listErr)
			} else if !ok {
				fmt.Printf("exam 试卷未出现在列表中：%s\n", paper.PaperID)
			}
		}
		added, updated, skipped, err := upsertCollectedAnswers(ctx, st, res)
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
	token, err := loadToken()
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

func captureTokenByLogin() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-gpu", false),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	taskCtx, taskCancel := chromedp.NewContext(allocCtx)
	defer taskCancel()

	fmt.Println(">>> 正在启动浏览器...")
	fmt.Println(">>> 请在打开的浏览器中完成学校统一身份认证登录。")
	fmt.Println(">>> 登录成功后，工具会自动捕获 Token 并保存，请勿提前关闭浏览器！")

	tokenChan := make(chan string, 1)

	chromedp.ListenTarget(taskCtx, func(ev interface{}) {
		switch ev := ev.(type) {
		case *network.EventRequestWillBeSent:
			reqURL := ev.Request.URL
			if strings.Contains(reqURL, "skl.hdu.edu.cn") && strings.Contains(reqURL, "token=") {
				u, err := url.Parse(reqURL)
				if err == nil {
					if token := u.Query().Get("token"); token != "" {
						select {
						case tokenChan <- token:
						default:
						}
					}
				}
			}
		}
	})

	if err := chromedp.Run(taskCtx, chromedp.Navigate("https://skl.hdu.edu.cn/")); err != nil {
		return "", fmt.Errorf("启动浏览器失败: %w", err)
	}

	select {
	case token := <-tokenChan:
		return token, nil
	case <-ctx.Done():
		return "", fmt.Errorf("等待登录超时或已取消")
	}
}

func saveToken(token string) {
	_ = os.WriteFile(".token", []byte(token), 0o600)
}

func loadToken() (string, error) {
	b, err := os.ReadFile(".token")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func loadPoolTokens(path string) ([]string, error) {
	p, err := loadTokenPool(path)
	if err != nil {
		return nil, err
	}
	return p.Tokens, nil
}

type tokenPool struct {
	Primary string
	Tokens  []string
}

func appendPoolToken(path, token string) (bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return false, fmt.Errorf("empty token")
	}
	pool, err := loadTokenPool(path)
	if err != nil {
		return false, err
	}
	if containsToken(pool.Tokens, token) {
		return false, nil
	}
	pool.Tokens = append(pool.Tokens, token)
	return true, saveTokenPool(path, pool)
}

func setPrimaryTokenInPool(path, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("empty token")
	}
	p, err := loadTokenPool(path)
	if err != nil {
		return err
	}
	if !containsToken(p.Tokens, token) {
		p.Tokens = append(p.Tokens, token)
	}
	p.Primary = token
	return saveTokenPool(path, p)
}

func loadTokenPool(path string) (tokenPool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return tokenPool{}, nil
		}
		return tokenPool{}, err
	}
	defer f.Close()
	out := tokenPool{}
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		isPrimary := strings.HasPrefix(line, "*")
		if isPrimary {
			line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		}
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			if isPrimary && out.Primary == "" {
				out.Primary = line
			}
			continue
		}
		seen[line] = struct{}{}
		out.Tokens = append(out.Tokens, line)
		if isPrimary && out.Primary == "" {
			out.Primary = line
		}
	}
	if err := scanner.Err(); err != nil {
		return tokenPool{}, err
	}
	if out.Primary != "" && !containsToken(out.Tokens, out.Primary) {
		out.Tokens = append(out.Tokens, out.Primary)
	}
	return out, nil
}

func saveTokenPool(path string, p tokenPool) error {
	var b strings.Builder
	b.WriteString("# token pool; prefix '*' means primary token\n")
	if p.Primary != "" {
		b.WriteString("*" + p.Primary + "\n")
	}
	for _, tk := range p.Tokens {
		if tk == "" || tk == p.Primary {
			continue
		}
		b.WriteString(tk + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func containsToken(tokens []string, token string) bool {
	for _, t := range tokens {
		if t == token {
			return true
		}
	}
	return false
}

func formatToken(token string, plain bool) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "(empty)"
	}
	if plain || len(token) <= 12 {
		return token
	}
	return token[:6] + "..." + token[len(token)-6:]
}

func getFinalTokenURL(rawURL string) string {
	if rawURL != "" {
		return rawURL
	}
	token, err := loadToken()
	if err != nil || token == "" {
		return ""
	}
	return fmt.Sprintf("https://skl.hdu.edu.cn/?type=6&token=%s#/english/list", token)
}

func chooseWrongChoiceWithSeed(correct string, options []string) string {
	return chooseWrongChoice(correct, options)
}

func isForbiddenAPIError(err error) bool {
	var apiErr *sklclient.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == 403
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
		if err := waitWithContext(ctx, cfg.Interval); err != nil {
			return err
		}
		err = fn()
		if err == nil {
			return nil
		}
		if !isForbiddenAPIError(err) {
			return err
		}
	}
	return err
}

var errTimeRegexp = regexp.MustCompile(`上次申请时间(\d{2}:\d{2}:\d{2})`)

func calcDynamicCooldown(errMsg string, defaultCooldown time.Duration) time.Duration {
	m := errTimeRegexp.FindStringSubmatch(errMsg)
	if len(m) < 2 {
		return defaultCooldown
	}
	timeStr := m[1]
	now := time.Now()
	t, err := time.ParseInLocation("15:04:05", timeStr, now.Location())
	if err != nil {
		return defaultCooldown
	}
	lastReqTime := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), t.Second(), 0, now.Location())
	if lastReqTime.After(now) {
		lastReqTime = lastReqTime.Add(-24 * time.Hour)
	}
	elapsed := now.Sub(lastReqTime)
	if elapsed >= defaultCooldown {
		return 5 * time.Second
	}
	return defaultCooldown - elapsed + 2*time.Second
}
