package tuiapp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
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
	browserType := readString(reader, "浏览器种类（chrome/edge，留空自动检测）", "")
	alias := readString(reader, "账号别名（可选，如学号）", "")

	token, err := browser.CaptureTokenByLogin(browserType)
	if err != nil {
		fmt.Printf("登录失败：%v\n", err)
		return
	}
	fmt.Println(">>> 成功捕获到 Token!")
	saveAccountTUI(token, alias, true)
}

func runAddTokenDirect(reader *bufio.Reader) {
	browserType := readString(reader, "浏览器种类（chrome/edge，留空自动检测）", "")
	alias := readString(reader, "账号别名（可选，如学号）", "")

	token, err := browser.CaptureTokenByLogin(browserType)
	if err != nil {
		fmt.Printf("addtoken 登录失败：%v\n", err)
		return
	}
	fmt.Println(">>> 成功捕获到 Token!")
	saveAccountTUI(token, alias, false)
}

// saveAccountTUI 把捕获的凭证写入统一凭证库；setPrimary 为 true 时同时设为主账号。
func saveAccountTUI(token, alias string, setPrimary bool) {
	st, err := tokenpool.LoadStore(tokenpool.DefaultAccountsFile)
	if err != nil {
		fmt.Printf("打开凭证库失败：%v\n", err)
		return
	}
	notifyMigratedTUI(st)
	name, added, err := st.Upsert(token, alias, "")
	if err != nil {
		fmt.Printf("写入凭证库失败：%v\n", err)
		return
	}
	if setPrimary {
		if err := st.SetPrimaryByToken(token); err != nil {
			fmt.Printf("设置主账号失败：%v\n", err)
			return
		}
	}
	if err := st.Save(); err != nil {
		fmt.Printf("保存凭证库失败：%v\n", err)
		return
	}
	switch {
	case added && setPrimary:
		fmt.Printf(">>> 已新增账号 %s 并设为主账号（exam 默认使用）。\n", name)
	case added:
		fmt.Printf(">>> 已新增账号 %s，可用于 collect 并发采集。\n", name)
	default:
		fmt.Printf(">>> 账号 %s 已在凭证库中，未重复写入。\n", name)
	}
}

// notifyMigratedTUI 在发生旧格式懒迁移时提示用户。
func notifyMigratedTUI(st *tokenpool.Store) {
	if st.Migrated() {
		fmt.Println(">>> 检测到旧版 .token/.tokens，已自动迁移到 accounts.json（旧文件保留，确认无误后可手动删除）")
	}
}

func runListTokensDirect(reader *bufio.Reader) {
	accountsFile := readString(reader, "凭证库文件 [accounts.json]", tokenpool.DefaultAccountsFile)
	showPlain := promptYesNoWithReader(reader, "是否显示完整 token 文本？", false)

	st, err := tokenpool.LoadStore(accountsFile)
	if err != nil {
		fmt.Printf("打开凭证库失败：%v\n", err)
		return
	}
	notifyMigratedTUI(st)

	fmt.Printf("凭证库(%s)：共 %d 个账号，主账号=%s\n", accountsFile, len(st.Accounts), st.Primary)
	for i, a := range st.Accounts {
		role := "member "
		if a.Alias == st.Primary {
			role = "primary"
		}
		fmt.Printf("%d. (%s) %-12s 添加于 %s  %s\n", i+1, role, a.Alias, a.AddedAt.Format("2006-01-02"), tokenpool.Format(a.Token, showPlain))
		if a.Note != "" {
			fmt.Printf("   备注：%s\n", a.Note)
		}
	}
}

func runSetPrimaryDirect(reader *bufio.Reader) {
	accountsFile := readString(reader, "凭证库文件 [accounts.json]", tokenpool.DefaultAccountsFile)

	st, err := tokenpool.LoadStore(accountsFile)
	if err != nil {
		fmt.Printf("打开凭证库失败：%v\n", err)
		return
	}
	notifyMigratedTUI(st)
	if len(st.Accounts) == 0 {
		fmt.Println("凭证库为空，请先登录添加账号")
		return
	}

	for i, a := range st.Accounts {
		role := "member "
		if a.Alias == st.Primary {
			role = "primary"
		}
		fmt.Printf("%d. (%s) %-12s %s\n", i+1, role, a.Alias, tokenpool.Format(a.Token, false))
	}

	sel := readString(reader, "输入编号或完整 token", "")
	var target string
	if n, err := strconv.Atoi(sel); err == nil && n >= 1 && n <= len(st.Accounts) {
		target = st.Accounts[n-1].Token
	} else {
		target = sel
	}
	if err := st.SetPrimaryByToken(target); err != nil {
		fmt.Printf("设置主账号失败：%v\n", err)
		return
	}
	if err := st.Save(); err != nil {
		fmt.Printf("保存凭证库失败：%v\n", err)
		return
	}
	fmt.Printf(">>> 已设置主账号(primary): %s，exam 默认使用该账号。\n", st.Primary)
}

func runCollectDirect(reader *bufio.Reader) {
	baseCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(baseCtx)
	defer cancel()

	dbPath := readString(reader, "数据库路径 [hduwords.db]", "hduwords.db")
	tokenURL := promptTokenURL(reader)
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
	notifyMigratedTUI(acctStore)

	specs := engine.BuildWorkers(acctStore.Tokens(), resolveURLForTUI(tokenURL), workers, sklclient.Options{
		BaseUserAgent: ua,
		Timeout:       timeout,
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
		Cooldown: cooldown,
		Retry:    retryCfg,
		Log:      collectLog,
	})
	fmt.Println("收集已停止，返回主菜单")
}

func runExamDirect(reader *bufio.Reader) {
	dbPath := readString(reader, "数据库路径 [hduwords.db]", "hduwords.db")
	tokenURL := promptTokenURL(reader)
	waitBeforeSubmit := readDuration(reader, "交卷前等待时长 [30s]", 30*time.Second)
	score := readInt(reader, "目标得分百分比 [-1]", -1)
	dryRun := promptYesNoWithReader(reader, "是否 dry-run？", false)
	rate := readFloat(reader, "请求速率 [2]", 2)
	timeout := readDuration(reader, "超时 [15s]", 15*time.Second)
	submitRetries := readInt(reader, "提交 403 重试次数 [3]", 3)
	submitRetryInt := readDuration(reader, "提交 403 重试间隔 [10s]", 10*time.Second)
	retryCfg := engine.SubmitRetryConfig{MaxRetries: submitRetries, Interval: submitRetryInt}.Normalized()

	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Printf("打开数据库失败：%v\n", err)
		return
	}
	defer st.Close()

	cl, err := sklclient.NewFromTokenURL(resolveURLForTUI(tokenURL), sklclient.Options{BaseUserAgent: sklclient.ExamMobileUserAgent, Timeout: timeout, MaxRPS: rate})
	if err != nil {
		fmt.Printf("初始化客户端失败：%v\n", err)
		return
	}

	if err := engine.RunExam(context.Background(), engine.ExamOptions{
		Client:           cl,
		Store:            st,
		WaitBeforeSubmit: waitBeforeSubmit,
		TargetScore:      score,
		DryRun:           dryRun,
		Retry:            retryCfg,
		Log:              collectLog,
	}); err != nil {
		fmt.Printf("考试失败：%v\n", err)
	}
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

func promptTokenURL(reader *bufio.Reader) string {
	tokenURL, _ := readLine(reader, "token URL（留空则使用凭证库主账号）")
	return strings.TrimSpace(tokenURL)
}

// primaryTokenTUI 返回凭证库主账号 token；无凭证库或无主账号时返回空串。
func primaryTokenTUI() string {
	st, err := tokenpool.LoadStore(tokenpool.DefaultAccountsFile)
	if err != nil {
		return ""
	}
	return st.PrimaryToken()
}

func resolveURLForTUI(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw != "" {
		return raw
	}
	if token := primaryTokenTUI(); token != "" {
		return sklclient.TokenURL(token)
	}
	return ""
}
