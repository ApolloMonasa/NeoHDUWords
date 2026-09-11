package tuiapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"hduwords/internal/browser"
	"hduwords/internal/engine"
	"hduwords/internal/sklclient"
	"hduwords/internal/store"
	"hduwords/internal/tokenpool"
	"hduwords/internal/updatecheck"
	"hduwords/internal/updater"
)

func runLoginDirect(reader *bufio.Reader) {
	browserType := readString(reader, "浏览器种类（chrome/edge，留空自动检测）", "")

	token, err := browser.CaptureTokenByLogin(browserType)
	if err != nil {
		fmt.Printf("登录失败：%v\n", err)
		return
	}
	fmt.Println(">>> 成功捕获到 Token!")

	st, err := tokenpool.LoadStore(tokenpool.DefaultAccountsFile)
	if err != nil {
		fmt.Printf("打开凭证库失败：%v\n", err)
		return
	}
	action, err := st.LoginPrimary(token)
	if err != nil {
		fmt.Printf("写入凭证库失败：%v\n", err)
		return
	}
	if err := st.Save(); err != nil {
		fmt.Printf("保存凭证库失败：%v\n", err)
		return
	}
	switch action {
	case tokenpool.LoginSwitched:
		fmt.Println(">>> 该凭证已在凭证库中，已标记为主账号（exam 默认使用）。")
	case tokenpool.LoginReplacedPrimary:
		fmt.Println(">>> 已刷新主账号凭证（exam 默认使用）。")
	default:
		fmt.Println(">>> 已新增凭证并标记为主账号（exam 默认使用）。")
	}
}

func runAddTokenDirect(reader *bufio.Reader) {
	browserType := readString(reader, "浏览器种类（chrome/edge，留空自动检测）", "")

	token, err := browser.CaptureTokenByLogin(browserType)
	if err != nil {
		fmt.Printf("addtoken 登录失败：%v\n", err)
		return
	}
	fmt.Println(">>> 成功捕获到 Token!")

	st, err := tokenpool.LoadStore(tokenpool.DefaultAccountsFile)
	if err != nil {
		fmt.Printf("打开凭证库失败：%v\n", err)
		return
	}
	added, err := st.Upsert(token)
	if err != nil {
		fmt.Printf("写入凭证库失败：%v\n", err)
		return
	}
	if err := st.Save(); err != nil {
		fmt.Printf("保存凭证库失败：%v\n", err)
		return
	}
	if added {
		fmt.Println(">>> 已新增凭证，可用于 collect 并发采集。")
	} else {
		fmt.Println(">>> 该凭证已在凭证库中，未重复写入。")
	}
}

func runListTokensDirect(reader *bufio.Reader) {
	accountsFile := readString(reader, "凭证库文件 [accounts.json]", tokenpool.DefaultAccountsFile)
	showPlain := updater.PromptYesNo(reader, "是否显示完整 token 文本？", false)

	st, err := tokenpool.LoadStore(accountsFile)
	if err != nil {
		fmt.Printf("打开凭证库失败：%v\n", err)
		return
	}

	primary := "未设置"
	if p := st.PrimaryToken(); p != "" {
		primary = tokenpool.Format(p, showPlain)
	}
	fmt.Printf("凭证库(%s)：共 %d 条凭证，主账号：%s\n", accountsFile, len(st.Tokens), primary)
	st.PrintAccounts(os.Stdout, showPlain)
}

func runSetPrimaryDirect(reader *bufio.Reader) {
	accountsFile := readString(reader, "凭证库文件 [accounts.json]", tokenpool.DefaultAccountsFile)

	st, err := tokenpool.LoadStore(accountsFile)
	if err != nil {
		fmt.Printf("打开凭证库失败：%v\n", err)
		return
	}
	if len(st.Tokens) == 0 {
		fmt.Println("凭证库为空，请先登录添加凭证")
		return
	}

	st.PrintAccounts(os.Stdout, false)

	sel := readString(reader, "输入编号或完整 token", "")
	var target string
	if n, err := strconv.Atoi(sel); err == nil && n >= 1 && n <= len(st.Tokens) {
		target = st.Tokens[n-1].Token
	} else {
		target = sel
	}
	if err := st.SetPrimary(target); err != nil {
		fmt.Printf("设置主账号失败：%v\n", err)
		return
	}
	if err := st.Save(); err != nil {
		fmt.Printf("保存凭证库失败：%v\n", err)
		return
	}
	fmt.Println(">>> 已设置主账号，exam 默认使用该凭证。")
}

func runRemoveTokenDirect(reader *bufio.Reader) {
	accountsFile := readString(reader, "凭证库文件 [accounts.json]", tokenpool.DefaultAccountsFile)

	st, err := tokenpool.LoadStore(accountsFile)
	if err != nil {
		fmt.Printf("打开凭证库失败：%v\n", err)
		return
	}
	if len(st.Tokens) == 0 {
		fmt.Println("凭证库为空")
		return
	}

	wasPrimaryHint := st.PrimaryToken()
	st.PrintAccounts(os.Stdout, false)

	sel := readString(reader, "输入要删除凭证的编号或完整 token", "")
	var target string
	if n, err := strconv.Atoi(sel); err == nil && n >= 1 && n <= len(st.Tokens) {
		target = st.Tokens[n-1].Token
	} else {
		target = sel
	}
	wasPrimary := target == wasPrimaryHint
	if err := st.RemoveToken(target); err != nil {
		fmt.Printf("删除凭证失败：%v\n", err)
		return
	}
	if err := st.Save(); err != nil {
		fmt.Printf("保存凭证库失败：%v\n", err)
		return
	}
	fmt.Println(">>> 已删除该凭证。")
	if wasPrimary {
		fmt.Println(">>> 注意：删除的是主账号，请重新登录或设置新的主账号。")
	}
}

func runCollectDirect(reader *bufio.Reader) {
	baseCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(baseCtx)
	defer cancel()

	dbPath := readString(reader, "数据库路径 [hduwords.db]", "hduwords.db")
	rate := readFloat(reader, "请求速率 [2]", 2)
	timeout := readDuration(reader, "超时 [15s]", 15*time.Second)
	ua := readString(reader, "UA [默认桌面 Chrome]", sklclient.DefaultUserAgent)
	cooldown := readDuration(reader, "冷却时间 [5m]", 5*time.Minute)
	accountsFile := readString(reader, "凭证库文件 [accounts.json]", tokenpool.DefaultAccountsFile)
	workers := readInt(reader, "worker 数 [0]", 0)
	submitRetries := readInt(reader, "提交 403 重试次数 [3]", 3)
	submitRetryInt := readDuration(reader, "提交 403 重试间隔 [10s]", 10*time.Second)

	retryCfg := engine.SubmitRetryConfig{MaxRetries: submitRetries, Interval: submitRetryInt}.Normalized()
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Printf("打开数据库失败：%v\n", err)
		return
	}
	defer st.Close()

	acctStore, err := tokenpool.LoadStore(accountsFile)
	if err != nil {
		fmt.Printf("加载凭证库失败：%v\n", err)
		return
	}

	specs := engine.BuildWorkers(acctStore.AllTokens(), resolvePrimaryURL(), workers, sklclient.Options{
		BaseUserAgent: ua,
		Timeout:       timeout,
		MaxRPS:        rate,
	}, collectLog)
	if len(specs) == 0 {
		fmt.Println("凭证库为空，请先登录（主菜单 1）添加账号")
		return
	}

	fmt.Println("按 Ctrl+C 返回主菜单")
	engine.RunCollectPool(ctx, engine.CollectPoolOptions{
		Workers:  specs,
		Store:    st,
		Cooldown: cooldown,
		Retry:    retryCfg,
		Log:      collectLog,
		OnTokenInvalid: func(token string) {
			if err := tokenpool.RemoveTokenPersistent(accountsFile, token); err != nil {
				collectLog("WARN", "删除失效凭证失败: %v", err)
			}
		},
	})
	fmt.Println("收集已停止，返回主菜单")
}

func runExamDirect(reader *bufio.Reader) {
	dbPath := readString(reader, "数据库路径 [hduwords.db]", "hduwords.db")
	waitBeforeSubmit := readDuration(reader, "交卷前等待时长 [30s]", 30*time.Second)
	score := readInt(reader, "目标得分百分比 [-1]", -1)
	dryRun := updater.PromptYesNo(reader, "是否 dry-run？", false)
	rate := readFloat(reader, "请求速率 [2]", 2)
	timeout := readDuration(reader, "超时 [15s]", 15*time.Second)
	submitRetries := readInt(reader, "提交 403 重试次数 [3]", 3)
	submitRetryInt := readDuration(reader, "提交 403 重试间隔 [10s]", 10*time.Second)
	retryCfg := engine.SubmitRetryConfig{MaxRetries: submitRetries, Interval: submitRetryInt}.Normalized()

	// Ctrl+C 可中断等待与请求
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Printf("打开数据库失败：%v\n", err)
		return
	}
	defer st.Close()

	primaryTok := primaryTokenTUI()
	if primaryTok == "" {
		fmt.Println("不存在主账户，请先登录（主菜单 1）")
		return
	}
	finalURL := sklclient.TokenURL(primaryTok)
	cl, err := sklclient.NewFromTokenURL(finalURL, sklclient.Options{BaseUserAgent: sklclient.ExamMobileUserAgent, Timeout: timeout, MaxRPS: rate})
	if err != nil {
		fmt.Printf("初始化客户端失败：%v\n", err)
		return
	}

	if err := engine.RunExam(ctx, engine.ExamOptions{
		Client:           cl,
		Store:            st,
		WaitBeforeSubmit: waitBeforeSubmit,
		TargetScore:      score,
		DryRun:           dryRun,
		Retry:            retryCfg,
		Log:              collectLog,
	}); err != nil {
		if errors.Is(err, engine.ErrLoginExpired) {
			if rmErr := tokenpool.RemoveTokenPersistent(tokenpool.DefaultAccountsFile, primaryTok); rmErr != nil {
				collectLog("WARN", "删除失效凭证失败: %v", rmErr)
			}
			fmt.Println("登录过期，已删除失效的主账号凭证，请重新登录（主菜单 1）")
			return
		}
		fmt.Printf("考试失败：%v\n", err)
	}
}

func runDBStatsDirect(reader *bufio.Reader) {
	dbPath := readString(reader, "数据库路径 [hduwords.db]", "hduwords.db")
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
	dbPath := readString(reader, "数据库路径 [hduwords.db]", "hduwords.db")
	defOut := "export.json"
	if markdown {
		defOut = "export.md"
	}
	outPath := readString(reader, "输出文件", defOut)
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
		st.ExportMarkdown(f, items)
		return
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(items); err != nil {
		fmt.Printf("写入导出文件失败：%v\n", err)
	}
}

func runDBConflictsDirect(reader *bufio.Reader) {
	dbPath := readString(reader, "数据库路径 [hduwords.db]", "hduwords.db")
	limit := readInt(reader, "最多显示条数 [20]", 20)

	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Printf("打开数据库失败：%v\n", err)
		return
	}
	defer st.Close()
	conflicts, err := st.ListConflicts(context.Background(), limit)
	if err != nil {
		fmt.Printf("查询冲突失败：%v\n", err)
		return
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

func runDBUpdateDirect() {
	dest := "hduwords.db"
	asset := updatecheck.DBAsset()

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

// primaryTokenTUI 返回凭证库主账号 token；无凭证库或无主账号时返回空串。
func primaryTokenTUI() string {
	st, err := tokenpool.LoadStore(tokenpool.DefaultAccountsFile)
	if err != nil {
		return ""
	}
	return st.PrimaryToken()
}

// resolvePrimaryURL 返回凭证库主账号的平台入口 URL；无主账号时返回空串。
func resolvePrimaryURL() string {
	if token := primaryTokenTUI(); token != "" {
		return sklclient.TokenURL(token)
	}
	return ""
}
