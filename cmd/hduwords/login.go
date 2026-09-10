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
	saveAccount(token, *alias, true)
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
	saveAccount(token, *alias, false)
}

// saveAccount 把捕获的凭证写入统一凭证库 accounts.json；
// setPrimary 为 true 时（login）同时设为主账号。
func saveAccount(token, alias string, setPrimary bool) {
	st, err := tokenpool.LoadStore(tokenpool.DefaultAccountsFile)
	if err != nil {
		fatalf("打开凭证库失败: %v", err)
	}
	notifyMigrated(st)
	name, added, err := st.Upsert(token, alias, "")
	if err != nil {
		fatalf("写入凭证库失败: %v", err)
	}
	if setPrimary {
		if err := st.SetPrimaryByToken(token); err != nil {
			fatalf("设置主账号失败: %v", err)
		}
	}
	if err := st.Save(); err != nil {
		fatalf("保存凭证库失败: %v", err)
	}
	switch {
	case added && setPrimary:
		fmt.Printf(">>> 已新增账号 %s 并设为主账号（exam 默认使用）。\n", name)
	case added:
		fmt.Printf(">>> 已新增账号 %s，可用于 collect 多账号并发采集。\n", name)
	default:
		fmt.Printf(">>> 账号 %s 已在凭证库中，未重复写入。\n", name)
	}
}

// notifyMigrated 在发生旧格式懒迁移时提示用户。
func notifyMigrated(st *tokenpool.Store) {
	if st.Migrated() {
		fmt.Println(">>> 检测到旧版 .token/.tokens，已自动迁移到 accounts.json（旧文件保留，确认无误后可手动删除）")
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
	notifyMigrated(st)

	fmt.Printf("凭证库(%s)：共 %d 个账号，主账号=%s\n", *accountsFile, len(st.Accounts), st.Primary)
	for i, a := range st.Accounts {
		role := "member "
		if a.Alias == st.Primary {
			role = "primary"
		}
		fmt.Printf("%d. (%s) %-12s 添加于 %s  %s\n", i+1, role, a.Alias, a.AddedAt.Format("2006-01-02"), tokenpool.Format(a.Token, *showPlain))
		if a.Note != "" {
			fmt.Printf("   备注：%s\n", a.Note)
		}
	}
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
	notifyMigrated(st)

	tk := strings.TrimSpace(*token)
	if tk == "" {
		for i, a := range st.Accounts {
			role := "member "
			if a.Alias == st.Primary {
				role = "primary"
			}
			fmt.Printf("%d. (%s) %-12s %s\n", i+1, role, a.Alias, tokenpool.Format(a.Token, false))
		}
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

func getFinalTokenURL(rawURL string) string {
	if rawURL != "" {
		return rawURL
	}
	st, err := tokenpool.LoadStore(tokenpool.DefaultAccountsFile)
	if err != nil {
		fatalf("打开凭证库失败: %v", err)
	}
	notifyMigrated(st)
	token := st.PrimaryToken()
	if token == "" {
		fatalf("凭证库中没有主账号，请先执行 login 或通过 --url 提供带 token 的网址")
	}
	return sklclient.TokenURL(token)
}
