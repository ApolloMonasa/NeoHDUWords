package browser

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

type BrowserType int

const (
	Chrome BrowserType = iota
	Edge
)

func (b BrowserType) String() string {
	switch b {
	case Chrome:
		return "Chrome"
	case Edge:
		return "Edge"
	default:
		return "Unknown"
	}
}

func ParseBrowserType(s string) (BrowserType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "chrome":
		return Chrome, nil
	case "edge":
		return Edge, nil
	default:
		return Chrome, fmt.Errorf("unknown browser type %q, valid: chrome, edge", s)
	}
}

func Resolve(specifiedType string) (BrowserType, string, error) {
	if strings.TrimSpace(specifiedType) != "" {
		bt, err := ParseBrowserType(specifiedType)
		if err != nil {
			return Chrome, "", err
		}
		path := findBrowser(bt)
		if path == "" {
			return Chrome, "", fmt.Errorf("当前平台未找到 %s 浏览器，请尝试其他浏览器或检查安装", bt)
		}
		return bt, path, nil
	}

	for _, candidate := range []BrowserType{Chrome, Edge} {
		if path := findBrowser(candidate); path != "" {
			return candidate, path, nil
		}
	}
	return Chrome, "", fmt.Errorf("未找到任何支持的浏览器（Chrome/Edge），请使用 --browser 指定")
}

func findBrowser(bt BrowserType) string {
	for _, c := range searchPaths(bt) {
		if isFullPath(c) {
			if pathExists(c) {
				return c
			}
		} else {
			if p, err := exec.LookPath(c); err == nil {
				return p
			}
		}
	}
	return ""
}

func isFullPath(p string) bool {
	return strings.Contains(p, string(os.PathSeparator)) ||
		(runtime.GOOS == "windows" && strings.Contains(p, "\\"))
}

func pathExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

func searchPaths(bt BrowserType) []string {
	switch bt {
	case Chrome:
		return chromePaths()
	case Edge:
		return edgePaths()
	}
	return nil
}

func chromePaths() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{
			"chrome.exe",
			filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
		}
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	default:
		return []string{
			"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
		}
	}
}

func edgePaths() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{
			"msedge.exe",
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
		}
	case "darwin":
		return []string{
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	default:
		return []string{
			"microsoft-edge", "microsoft-edge-stable", "edge",
		}
	}
}

const targetURL = "https://skl.hdu.edu.cn/"

// CaptureTokenByLogin launches a browser and captures the auth token.
func CaptureTokenByLogin(browserType string) (string, error) {
	_, path, err := Resolve(browserType)
	if err != nil {
		return "", fmt.Errorf("resolve browser: %w", err)
	}
	return captureWithChromium(path)
}

// captureWithChromium uses chromedp ExecAllocator for Chrome/Edge.
func captureWithChromium(execPath string) (string, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(execPath),
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-gpu", false),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

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
			if token := extractTokenFromURL(ev.Request.URL); token != "" {
				select {
				case tokenChan <- token:
				default:
				}
			}
		}
	})

	if err := chromedp.Run(taskCtx,
		chromedp.Navigate(targetURL),
	); err != nil {
		return "", fmt.Errorf("启动浏览器失败: %w", err)
	}

	select {
	case token := <-tokenChan:
		return token, nil
	case <-ctx.Done():
		return "", fmt.Errorf("等待登录超时或已取消")
	}
}

func extractTokenFromURL(rawURL string) string {
	if !strings.Contains(rawURL, "skl.hdu.edu.cn") || !strings.Contains(rawURL, "token=") {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("token")
}
