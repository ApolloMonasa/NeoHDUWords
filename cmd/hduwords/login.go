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
	alias := fs.String("alias", "", "账号别名（默认自动编号 acct-N）")
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
	name, action, err := st.LoginPrimary(token, *alias)
	if err != nil {
		fatalf("写入凭证库失败: %v", err)
	}
	if err := st.Save(); err != nil {
		fatalf("保存凭证库失败: %v", err)
	}
	switch action {
	case tokenpool.LoginSwitched:
		fmt.Printf(">>> 账号 %s 已在凭证库中，已设为主账号（exam 默认使用）。\n", name)
	case tokenpool.LoginRefreshed:
		fmt.Printf(">>> 已刷新账号 %s 的凭证并设为主账号（exam 默认使用）。\n", name)
	case tokenpool.LoginReplacedPrimary:
		fmt.Printf(">>> 已刷新主账号 %s 的凭证（exam 默认使用）。\n", name)
	default:
		fmt.Printf(">>> 已新增账号 %s 并设为主账号（exam 默认使用）。\n", name)
	}
}

func addTokenCmd(args []string) {
	fs := flag.NewFlagSet("addtoken", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	browserType := fs.String("browser", "", "浏览器种类: chrome|edge（留空自动检测）")
	alias := fs.String("alias", "", "账号别名（默认自动编号 acct-N）")
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
	name, added, err := st.Upsert(token, *alias, "")
	if err != nil {
		fatalf("写入凭证库失败: %v", err)
	}
	if err := st.Save(); err != nil {
		fatalf("保存凭证库失败: %v", err)
	}
	if added {
		fmt.Printf(">>> 已新增账号 %s，可用于 collect 多账号并发采集。\n", name)
	} else {
		fmt.Printf(">>> 账号 %s 已在凭证库中，未重复写入。\n", name)
	}
}

func listTokensCmd(args []string) {
	fs := flag.NewFlagSet("listtokens", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	accountsFile := fs.String("accounts", tokenpool.DefaultAccountsFile, "accounts store file path")
	showPlain := fs.Bool("show-plain", false, "show full token text")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	st, err := tokenpool.LoadStore(*accountsFile)
	if err != nil {
		fatalf("打开凭证库失败: %v", err)
	}

	fmt.Printf("凭证库(%s)：共 %d 个账号，主账号=%s\n", *accountsFile, len(st.Accounts), st.Primary)
	st.PrintAccounts(os.Stdout, *showPlain)
}

func setPrimaryCmd(args []string) {
	fs := flag.NewFlagSet("setprimary", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	accountsFile := fs.String("accounts", tokenpool.DefaultAccountsFile, "accounts store file path")
	token := fs.String("token", "", "token to set as primary")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	st, err := tokenpool.LoadStore(*accountsFile)
	if err != nil {
		fatalf("打开凭证库失败: %v", err)
	}

	tk := strings.TrimSpace(*token)
	if tk == "" {
		st.PrintAccounts(os.Stdout, false)
		fatalf("请通过 --token 指定要设为主账号的凭证")
	}
	if err := st.SetPrimaryByToken(tk); err != nil {
		fatalf("设置主账号失败: %v", err)
	}
	if err := st.Save(); err != nil {
		fatalf("保存凭证库失败: %v", err)
	}
	fmt.Printf(">>> 已设置主账号(primary): %s，exam 默认使用该账号。\n", st.Primary)
}

func rmTokenCmd(args []string) {
	fs := flag.NewFlagSet("rmtoken", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	accountsFile := fs.String("accounts", tokenpool.DefaultAccountsFile, "accounts store file path")
	alias := fs.String("alias", "", "alias of the account to remove")
	token := fs.String("token", "", "token of the account to remove")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	target := strings.TrimSpace(*alias)
	if target == "" {
		target = strings.TrimSpace(*token)
	}
	if target == "" {
		fatalf("请通过 --alias 或 --token 指定要删除的账号")
	}

	st, err := tokenpool.LoadStore(*accountsFile)
	if err != nil {
		fatalf("打开凭证库失败: %v", err)
	}
	name, err := st.RemoveAccount(target)
	if err != nil {
		fatalf("删除账号失败: %v", err)
	}
	if err := st.Save(); err != nil {
		fatalf("保存凭证库失败: %v", err)
	}
	fmt.Printf(">>> 已删除账号 %s。\n", name)
	if st.Primary == "" {
		fmt.Println(">>> 注意：删除的是主账号，请重新 login 或 setprimary 设置新的主账号。")
	}
}

func getFinalTokenURL(rawURL string) string {
	if rawURL != "" {
		return rawURL
	}
	st, err := tokenpool.LoadStore(tokenpool.DefaultAccountsFile)
	if err != nil {
		fatalf("打开凭证库失败: %v", err)
	}
	token := st.PrimaryToken()
	if token == "" {
		fatalf("凭证库中没有主账号，请先执行 login 或通过 --url 提供带 token 的网址")
	}
	return sklclient.TokenURL(token)
}
