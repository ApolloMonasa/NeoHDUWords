package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"hduwords/internal/browser"
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
	if err := tokenpool.SaveMain(tokenpool.DefaultMainFile, token); err != nil {
		fatalf("保存 token 失败: %v", err)
	}
	if err := tokenpool.SetPrimary(tokenpool.DefaultPoolFile, token); err != nil {
		fmt.Printf(">>> 警告: 同步 .tokens 主账号标识失败: %v\n", err)
	}
	fmt.Println(">>> 已保存 Token 到本地 .token 文件，后续命令无需再提供 --url 参数。")
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
	added, err := tokenpool.Append(tokenpool.DefaultPoolFile, token)
	if err != nil {
		fatalf("写入 token 池失败: %v", err)
	}
	if added {
		fmt.Println(">>> 已新增到 .tokens，可用于 collect 多账号并发采集。")
	} else {
		fmt.Println(">>> .tokens 中已存在该 token，未重复写入。")
	}
}

func listTokensCmd(args []string) {
	fs := flag.NewFlagSet("listtokens", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	poolFile := fs.String("pool-file", tokenpool.DefaultPoolFile, "token pool file path")
	showPlain := fs.Bool("show-plain", false, "show full token text")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	mainToken, _ := tokenpool.LoadMain(tokenpool.DefaultMainFile)
	pool, err := tokenpool.Load(*poolFile)
	if err != nil {
		fatalf("读取 token 池失败: %v", err)
	}

	fmt.Printf("主账号(.token): %s\n", tokenpool.Format(mainToken, *showPlain))
	fmt.Printf("token池(%s): 共 %d 个\n", *poolFile, len(pool.Tokens))
	for i, tk := range pool.Tokens {
		role := "member"
		if pool.Primary != "" && tk == pool.Primary {
			role = "primary"
		}
		bind := ""
		if mainToken != "" && tk == mainToken {
			bind = " [= .token]"
		}
		fmt.Printf("%d. (%s)%s %s\n", i+1, role, bind, tokenpool.Format(tk, *showPlain))
	}
}

func setPrimaryCmd(args []string) {
	fs := flag.NewFlagSet("setprimary", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	poolFile := fs.String("pool-file", tokenpool.DefaultPoolFile, "token pool file path")
	token := fs.String("token", "", "token to set as primary")
	syncLogin := fs.Bool("sync-login", true, "sync primary token to .token for exam/test")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	tk := strings.TrimSpace(*token)
	if tk == "" {
		var err error
		tk, err = tokenpool.LoadMain(tokenpool.DefaultMainFile)
		if err != nil || tk == "" {
			fatalf("未提供 --token 且本地 .token 不可用")
		}
	}

	if err := tokenpool.SetPrimary(*poolFile, tk); err != nil {
		fatalf("设置主账号失败: %v", err)
	}
	if *syncLogin {
		if err := tokenpool.SaveMain(tokenpool.DefaultMainFile, tk); err != nil {
			fatalf("保存 token 失败: %v", err)
		}
	}
	fmt.Printf(">>> 已设置主账号(primary): %s\n", tokenpool.Format(tk, false))
	if *syncLogin {
		fmt.Println(">>> 已同步 .token，exam/test 将使用该账号。")
	}
}

func getFinalTokenURL(rawURL string) string {
	if rawURL != "" {
		return rawURL
	}
	token, err := tokenpool.LoadMain(tokenpool.DefaultMainFile)
	if err != nil || token == "" {
		fatalf("未提供 --url 且本地无有效的 .token 文件，请先运行 hduwords login 或提供 --url 参数")
	}
	return fmt.Sprintf("https://skl.hdu.edu.cn/?type=6&token=%s#/english/list", token)
}
