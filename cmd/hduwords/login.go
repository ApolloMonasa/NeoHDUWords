package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"hduwords/internal/browser"
	"hduwords/internal/sklclient"
	"hduwords/internal/tokenpool"
)

func loginCmd(args []string) {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	browserType := fs.String("browser", "", "浏览器种类: chrome|edge（留空自动检测）")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	token, err := browser.CaptureTokenByLogin(*browserType)
	if err != nil {
		fatalf("登录失败: %v", err)
	}
	fmt.Println(">>> 成功捕获到 Token!")

	st, err := tokenpool.LoadStore(tokenpool.DefaultAccountsFile)
	if err != nil {
		fatalf("打开凭证库失败: %v", err)
	}
	action, err := st.LoginPrimary(token)
	if err != nil {
		fatalf("写入凭证库失败: %v", err)
	}
	if err := st.Save(); err != nil {
		fatalf("保存凭证库失败: %v", err)
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

func addTokenCmd(args []string) {
	fs := flag.NewFlagSet("addtoken", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	browserType := fs.String("browser", "", "浏览器种类: chrome|edge（留空自动检测）")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	token, err := browser.CaptureTokenByLogin(*browserType)
	if err != nil {
		fatalf("addtoken 登录失败: %v", err)
	}
	fmt.Println(">>> 成功捕获到 Token!")

	st, err := tokenpool.LoadStore(tokenpool.DefaultAccountsFile)
	if err != nil {
		fatalf("打开凭证库失败: %v", err)
	}
	added, err := st.Upsert(token)
	if err != nil {
		fatalf("写入凭证库失败: %v", err)
	}
	if err := st.Save(); err != nil {
		fatalf("保存凭证库失败: %v", err)
	}
	if added {
		fmt.Println(">>> 已新增凭证，可用于 collect 多账号并发采集。")
	} else {
		fmt.Println(">>> 该凭证已在凭证库中，未重复写入。")
	}
}

func listTokensCmd(args []string) {
	fs := flag.NewFlagSet("listtokens", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	showPlain := fs.Bool("show-plain", false, "show full token text")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	st, err := tokenpool.LoadStore(tokenpool.DefaultAccountsFile)
	if err != nil {
		fatalf("打开凭证库失败: %v", err)
	}

	primary := "未设置"
	if p := st.PrimaryToken(); p != "" {
		primary = tokenpool.Format(p, *showPlain)
	}
	fmt.Printf("凭证库：共 %d 条凭证，主账号：%s\n", len(st.Tokens), primary)
	st.PrintAccounts(os.Stdout, *showPlain)
}

func setPrimaryCmd(args []string) {
	fs := flag.NewFlagSet("setprimary", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	token := fs.String("token", "", "token to set as primary")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	st, err := tokenpool.LoadStore(tokenpool.DefaultAccountsFile)
	if err != nil {
		fatalf("打开凭证库失败: %v", err)
	}

	tk := strings.TrimSpace(*token)
	if tk == "" {
		st.PrintAccounts(os.Stdout, false)
		fatalf("请通过 --token 指定要设为主账号的凭证")
	}
	if err := st.SetPrimary(tk); err != nil {
		fatalf("设置主账号失败: %v", err)
	}
	if err := st.Save(); err != nil {
		fatalf("保存凭证库失败: %v", err)
	}
	fmt.Println(">>> 已设置主账号，exam 默认使用该凭证。")
}

func rmTokenCmd(args []string) {
	fs := flag.NewFlagSet("rmtoken", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	token := fs.String("token", "", "token of the account to remove")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	tk := strings.TrimSpace(*token)
	if tk == "" {
		fatalf("请通过 --token 指定要删除的凭证（可用 listtokens --show-plain 查看完整 token）")
	}

	st, err := tokenpool.LoadStore(tokenpool.DefaultAccountsFile)
	if err != nil {
		fatalf("打开凭证库失败: %v", err)
	}
	wasPrimary := st.PrimaryToken() == tk
	if err := st.RemoveToken(tk); err != nil {
		fatalf("删除凭证失败: %v", err)
	}
	if err := st.Save(); err != nil {
		fatalf("保存凭证库失败: %v", err)
	}
	fmt.Println(">>> 已删除该凭证。")
	if wasPrimary {
		fmt.Println(">>> 注意：删除的是主账号，请重新 login 或 setprimary 设置新的主账号。")
	}
}

// getFinalTokenURL 解析 --url 或凭证库主账号。
// 返回 (入口 URL, 来源凭证 token)；--url 手动指定时第二值为空串。
func getFinalTokenURL(rawURL string) (string, string) {
	if rawURL != "" {
		return rawURL, ""
	}
	st, err := tokenpool.LoadStore(tokenpool.DefaultAccountsFile)
	if err != nil {
		fatalf("打开凭证库失败: %v", err)
	}
	token := st.PrimaryToken()
	if token == "" {
		fatalf("不存在主账户，请先执行 login")
	}
	return sklclient.TokenURL(token), token
}
