package tuiapp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
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

	dbPath := readString(reader, "数据库路径 [hduwords.db]", "hduwords.db")
	tokenURL := promptTokenURL(reader)
	rate := readFloat(reader, "请求速率 [2]", 2)
	timeout := readDuration(reader, "超时 [15s]", 15*time.Second)
	ua := readString(reader, "UA [默认桌面 Chrome]", sklclient.DefaultUserAgent)
	cooldown := readDuration(reader, "冷却时间 [5m]", 5*time.Minute)
	poolFile := readString(reader, "token 池文件 [.tokens]", tokenpool.DefaultPoolFile)
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

	pool, err := tokenpool.Load(poolFile)
	if err != nil {
		fmt.Printf("加载 token 池失败：%v\n", err)
		return
	}

	specs := engine.BuildWorkers(pool.Tokens, resolveURLForTUI(tokenURL), workers, sklclient.Options{
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
	tokenURL, _ := readLine(reader, "token URL（留空则使用 .token）")
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
