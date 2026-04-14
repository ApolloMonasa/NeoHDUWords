package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

func loginCmd(args []string) {
	token, err := captureTokenByLogin()
	if err != nil {
		fatalf("登录失败: %v", err)
	}
	fmt.Println(">>> 成功捕获到 Token!")
	saveToken(token)
	if err := setPrimaryTokenInPool(".tokens", token); err != nil {
		fmt.Printf(">>> 警告: 同步 .tokens 主账号标识失败: %v\n", err)
	}
	fmt.Println(">>> 已保存 Token 到本地 .token 文件，后续命令无需再提供 --url 参数。")
}

func addTokenCmd(args []string) {
	token, err := captureTokenByLogin()
	if err != nil {
		fatalf("addtoken 登录失败: %v", err)
	}
	fmt.Println(">>> 成功捕获到 Token!")
	added, err := appendPoolToken(".tokens", token)
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
	poolFile := fs.String("pool-file", ".tokens", "token pool file path")
	showPlain := fs.Bool("show-plain", false, "show full token text")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	mainToken, _ := loadToken()
	pool, err := loadTokenPool(*poolFile)
	if err != nil {
		fatalf("读取 token 池失败: %v", err)
	}

	fmt.Printf("主账号(.token): %s\n", formatToken(mainToken, *showPlain))
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
		fmt.Printf("%d. (%s)%s %s\n", i+1, role, bind, formatToken(tk, *showPlain))
	}
}

func setPrimaryCmd(args []string) {
	fs := flag.NewFlagSet("setprimary", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	poolFile := fs.String("pool-file", ".tokens", "token pool file path")
	token := fs.String("token", "", "token to set as primary")
	syncLogin := fs.Bool("sync-login", true, "sync primary token to .token for exam/test")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	tk := strings.TrimSpace(*token)
	if tk == "" {
		var err error
		tk, err = loadToken()
		if err != nil || tk == "" {
			fatalf("未提供 --token 且本地 .token 不可用")
		}
	}

	if err := setPrimaryTokenInPool(*poolFile, tk); err != nil {
		fatalf("设置主账号失败: %v", err)
	}
	if *syncLogin {
		saveToken(tk)
	}
	fmt.Printf(">>> 已设置主账号(primary): %s\n", formatToken(tk, false))
	if *syncLogin {
		fmt.Println(">>> 已同步 .token，exam/test 将使用该账号。")
	}
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

	err := chromedp.Run(taskCtx,
		chromedp.Navigate("https://skl.hdu.edu.cn/"),
	)
	if err != nil {
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
	err := os.WriteFile(".token", []byte(token), 0600)
	if err != nil {
		fatalf("保存 token 失败: %v", err)
	}
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
	if err := saveTokenPool(path, pool); err != nil {
		return false, err
	}
	return true, nil
}

type tokenPool struct {
	Primary string
	Tokens  []string
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
	return os.WriteFile(path, []byte(b.String()), 0600)
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
		fatalf("未提供 --url 且本地无有效的 .token 文件，请先运行 hduwords login 或提供 --url 参数")
	}
	return fmt.Sprintf("https://skl.hdu.edu.cn/?type=6&token=%s#/english/list", token)
}
