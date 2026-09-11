// Package updater 承载 CLI 与 TUI 共用的自更新交互流程：
// 检查版本 → 确认 → 下载 → SHA256 校验 → 安装（自替换）。
// 两入口只负责提供参数（二进制名、安装助手参数风格）与承接返回值。
package updater

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"hduwords/internal/buildinfo"
	"hduwords/internal/ui"
	"hduwords/internal/updatecheck"
)

// Options 是交互式更新流程的参数。
type Options struct {
	Repo       updatecheck.Repo
	BinaryName string // "cli" 或 "tui"，用于匹配当前平台的发布资产
	UpdatesDir string // 更新包下载目录，默认 .updates
	StartDir   string // 工作目录（dev 模式下读取本地 git 信息用）
	Reader     *bufio.Reader
	AutoYes    bool // 跳过所有确认（CLI --yes）
	CheckOnly  bool // 仅检查不安装
}

// Run 执行完整的更新交互流程。
// 返回 installed=true 表示安装助手已启动、调用方应退出进程；
// 返回的 error 由调用方决定按致命还是提示处理。
func Run(ctx context.Context, opts Options) (installed bool, err error) {
	if opts.StartDir == "" {
		opts.StartDir, _ = os.Getwd()
	}

	checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	status, err := updatecheck.Check(checkCtx, opts.Repo, opts.StartDir)
	if err != nil {
		return false, err
	}
	ShowStatus(status)
	if !status.Available {
		fmt.Println("已是最新版本")
		return false, nil
	}
	if opts.CheckOnly {
		fmt.Println("检测到有更新（check-only）")
		return false, nil
	}

	// 单次确认：同意后自动完成下载 → 校验 → 安装（--yes 跳过询问）
	if !opts.AutoYes && !ui.PromptYesNo(opts.Reader, "检测到更新，是否下载并安装？", false) {
		fmt.Println("已取消更新")
		return false, nil
	}

	releaseCtx, releaseCancel := context.WithTimeout(ctx, 20*time.Second)
	release, err := updatecheck.LatestRelease(releaseCtx, opts.Repo)
	releaseCancel()
	if err != nil {
		return false, err
	}
	asset, ok := release.AssetForCurrentPlatform(opts.BinaryName)
	if !ok {
		return false, fmt.Errorf("最新发行版 %s 没有匹配当前平台的 %s 资产", release.TagName, opts.BinaryName)
	}

	downloadDir := strings.TrimSpace(opts.UpdatesDir)
	if downloadDir == "" {
		downloadDir = ".updates"
	}
	if err := os.MkdirAll(downloadDir, 0o755); err != nil {
		return false, err
	}
	dest := filepath.Join(downloadDir, asset.Name)
	if abs, err := filepath.Abs(dest); err == nil {
		dest = abs
	}
	written, err := updatecheck.DownloadAsset(ctx, asset, dest)
	if err != nil {
		return false, err
	}
	fmt.Printf("更新包已下载：%s (%d bytes)\n", dest, written)

	if verr := updatecheck.VerifyAssetChecksum(ctx, release, asset.Name, dest); verr != nil {
		if errors.Is(verr, updatecheck.ErrNoSumsAsset) {
			fmt.Println("当前发行版未提供 SHA256SUMS，跳过完整性校验")
		} else if errors.Is(verr, updatecheck.ErrAssetNotInSums) {
			fmt.Printf("警告：%v，跳过完整性校验\n", verr)
		} else {
			return false, fmt.Errorf("更新包完整性校验失败: %w", verr)
		}
	} else {
		fmt.Println("更新包完整性校验通过")
	}

	if err := InstallSelfUpdate(dest); err != nil {
		return false, err
	}
	fmt.Println("更新已启动安装，程序将退出。")
	return true, nil
}

// ShowStatus 打印本地/远端版本状态。
func ShowStatus(status updatecheck.Status) {
	if status.LocalVersion != "" {
		if status.LocalSHA != "" {
			fmt.Printf("当前版本：%s (%s)\n", status.LocalVersion, shortSHA(status.LocalSHA))
		} else {
			fmt.Printf("当前版本：%s\n", status.LocalVersion)
		}
	} else if status.LocalSHA == "" {
		fmt.Println("当前版本：无法读取本地 Git 信息")
	} else {
		fmt.Printf("当前版本：%s (%s)\n", shortSHA(status.LocalSHA), status.LocalBranch)
	}
	if status.RemoteSHA == "" {
		fmt.Println("远端版本：无法获取")
		return
	}
	fmt.Printf("远端版本：%s (%s)\n", shortSHA(status.RemoteSHA), status.RemoteBranch)
	if status.Available {
		fmt.Println("状态：有更新")
	} else {
		fmt.Println("状态：已是最新")
	}
}

// InstallSelfUpdate 把自身复制到临时目录并以安装助手子进程执行替换：
// 运行中的进程不能直接覆写自身（Windows 文件锁），由副本进程完成拷贝。
// 助手协议统一为 "apply-update --source <新文件> --target <原文件>"（CLI/TUI 一致）。
// Start 成功即返回；调用方随后退出。
func InstallSelfUpdate(sourcePath string) error {
	selfExe, err := os.Executable()
	if err != nil {
		return err
	}
	helperDir, err := os.MkdirTemp("", "hduwords-updater-*")
	if err != nil {
		return err
	}
	helperPath := filepath.Join(helperDir, filepath.Base(selfExe))
	if err := copyLocalFile(selfExe, helperPath); err != nil {
		return err
	}
	cmd := exec.Command(helperPath, "apply-update", "--source", sourcePath, "--target", selfExe)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Start()
}

func copyLocalFile(srcPath, dstPath string) error {
	input, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	return os.WriteFile(dstPath, input, 0o755)
}

func shortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// VersionString 返回用于启动横幅的版本串（dev 构建返回空）。
func VersionString() string {
	if v := strings.TrimSpace(buildinfo.Version); v != "" && v != "dev" {
		if c := strings.TrimSpace(buildinfo.Commit); c != "" && c != "unknown" {
			return fmt.Sprintf("%s (%s)", v, shortSHA(c))
		}
		return v
	}
	return ""
}
