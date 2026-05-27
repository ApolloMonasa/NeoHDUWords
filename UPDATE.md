# 内置更新机制

## 通俗版：它是怎么工作的

整个更新机制可以分成四步：

1. **编译时打标签** — 每次 GitHub Actions 编译二进制时，会用 `-ldflags` 把版本号（如 `v1.2.3`）和 Git commit SHA 写进二进制文件里。这样每个 `.exe` 文件都"知道自己是谁"。

2. **启动时查更新** — CLI 和 TUI 启动后，会拿自己的版本号去问 GitHub："你们仓库最新的 Release 是哪个？" 如果自己的版本号和远端不一样，就提示用户有新版本可用。

3. **下载新版本** — 用户确认更新后，程序从 GitHub Release 页面下载匹配当前操作系统和 CPU 架构的二进制文件，保存到 `.updates/` 目录。

4. **替换自身** — 一个运行中的程序不能直接覆盖自己，所以它会把自身复制到一个临时目录，用那个副本启动一个"安装助手"进程，传给它两个路径：下载的新文件在哪、要覆盖的原文件在哪。安装助手启动后，原进程退出，助手把新文件拷过去，完成替换。

整个过程不需要用户手动去 GitHub 下载任何东西。

---

## 技术细节

### 1. 版本信息注入

文件：[internal/buildinfo/buildinfo.go](internal/buildinfo/buildinfo.go)

```go
var (
    Version = "dev"
    Commit  = "unknown"
)
```

这两个变量在源码里是默认值，真正的版本号通过 `go build -ldflags` 在编译时注入：

```bash
go build -ldflags "
  -X hduwords/internal/buildinfo.Version=v1.2.3
  -X hduwords/internal/buildinfo.Commit=abc1234
" -o cli ./cmd/hduwords
```

- CI 构建（[.github/workflows/ci.yml](.github/workflows/ci.yml)）：`Version` 固定为 `"dev"`，`Commit` 为当前提交 SHA。因为 CI 构建不用于分发，标记为 `dev` 意味着"开发版，不走版本号比对"。
- Release 构建（[.github/workflows/release.yml](.github/workflows/release.yml)）：`Version` 为 git tag（如 `v1.2.3`），`Commit` 为当前提交 SHA。

### 2. 发行版构建与资产命名

Release 流程由推 `v*` 标签触发（[release.yml:5-6](.github/workflows/release.yml#L5-L6)），在 GitHub Actions 的 ubuntu-latest 跑机上交叉编译出 4 个平台 × 2 个入口 = 8 个二进制文件：

| 文件 | 平台 |
|------|------|
| `cli-linux-amd64` | Linux x86_64 CLI |
| `tui-linux-amd64` | Linux x86_64 TUI |
| `cli-windows-amd64.exe` | Windows x86_64 CLI |
| `tui-windows-amd64.exe` | Windows x86_64 TUI |
| `cli-darwin-amd64` | macOS Intel CLI |
| `tui-darwin-amd64` | macOS Intel TUI |
| `cli-darwin-arm64` | macOS Apple Silicon CLI |
| `tui-darwin-arm64` | macOS Apple Silicon TUI |

文件命名规则：`{binary}-{os}-{arch}[.exe]`，例如 `tui-darwin-arm64` 表示 macOS ARM64 平台的 TUI。

所有文件通过 `softprops/action-gh-release@v2` 上传为 GitHub Release 的附件资产（asset）。

### 3. 更新检查流程

入口在 [internal/updatecheck/updatecheck.go](internal/updatecheck/updatecheck.go) 的 `Check()` 函数。

**版本模式（有正式版本号，即 `Version != "dev"` 且非空）：**

```
本地 Version  vs  远端 Latest Release TagName
     |                    |
     v                    v
   "v1.2.3"    vs      "v1.3.0"    → 字符串不相等 → Available = true
```

- 通过 GitHub API `GET /repos/{owner}/{repo}/releases/latest` 取最新 Release 的 `tag_name`
- 直接比较本地 `Version` 字符串和远端 `tag_name`（大小写不敏感）
- 简单、可靠，不依赖本地 git 仓库

**开发模式（`Version == "dev"` 或为空）：**

```
本地 HEAD SHA  vs  远端默认分支 HEAD SHA
     |                    |
     v                    v
   读 .git/HEAD       GitHub API:
                     ① GET /repos/{owner}/{repo}  → 取 default_branch
                     ② GET /repos/{owner}/{repo}/commits/{branch} → 取 sha
```

- 从本地 `.git` 目录读取当前 HEAD 的 commit SHA（支持 worktree、packed-refs）
- 从 GitHub API 先查到默认分支名，再查该分支最新 commit SHA
- 两个 SHA 不同则为有更新

本质上：**正式发布用 tag 比，开发版用 git commit 比。**

### 4. 资产匹配

函数：[internal/updatecheck/release.go](internal/updatecheck/release.go) `AssetForCurrentPlatform()`

```go
goos := strings.ToLower(runtime.GOOS)   // "linux" / "windows" / "darwin"
goarch := strings.ToLower(runtime.GOARCH) // "amd64" / "arm64"
needle := fmt.Sprintf("%s-%s-%s", binaryName, goos, goarch)
// 例如: "cli-linux-amd64"
```

遍历 Release 的所有 asset，去掉扩展名后与 `needle` 做大小写不敏感比较。传递的 `binaryName` 参数：
- CLI 更新传 `"cli"`
- TUI 更新传 `"tui"`

如果找不到匹配当前平台的资产，更新中止并提示用户。

### 5. 下载

函数：[internal/updatecheck/release.go](internal/updatecheck/release.go) `DownloadAsset()`

- 用 asset 的 `browser_download_url` 发起 HTTP GET
- 写入到 `.updates/` 目录（可通过 `--updates-dir` 自定义）
- 返回写入的字节数

### 6. 自替换（安装）

这是最精巧的部分。一个正在运行的进程不能直接覆写自己的可执行文件（Windows 会锁文件，Linux 虽然允许但行为不安全）。

**CLI 的安装流程**（[cmd/hduwords/main.go:217-235](cmd/hduwords/main.go#L217-L235)）：

```
1. os.Executable()  → 拿到自己的路径，比如 /usr/local/bin/cli
2. os.MkdirTemp()   → 创建临时目录，比如 /tmp/hduwords-updater-xxxx/
3. 把自己拷贝到临时目录 → /tmp/hduwords-updater-xxxx/cli
4. exec.Command(临时副本, "apply-update",
       "--source", "下载的新文件",
       "--target", "自己的原路径").Start()
5. 原进程 os.Exit()
```

```
  ┌──────────┐    spawn    ┌──────────────┐    copy    ┌──────────┐
  │  原进程   │ ────────→  │  临时副本进程  │ ────────→  │  新二进制  │
  │  (退出)   │            │  apply-update │            │  (替换)   │
  └──────────┘            └──────────────┘            └──────────┘
```

**TUI 的安装流程**（[cmd/tui/main.go:17-22](cmd/tui/main.go#L17-L22)）：

TUI 的入口本身就是 `--apply-update` 开关。安装时同样：
1. 把自己拷贝到临时目录
2. 用 `exec.Command(临时副本, "--apply-update", "--source", ..., "--target", ...)` 启动
3. 原进程退出

**Windows 特殊处理**（[internal/updatecheck/install.go:26-37](internal/updatecheck/install.go#L26-L37)）：

```go
func installBinaryWindows(srcPath, targetPath string) error {
    for i := 0; i < 20; i++ {
        os.Remove(targetPath)  // 尝试删除被锁的旧文件
        time.Sleep(250ms)
    }
    copyFile(srcPath, targetPath)
}
```

Windows 上 exe 文件被运行时无法直接覆盖，所以最多重试 20 次（共等待 5 秒）删除旧文件，然后再拷贝新文件。

**POSIX（Linux/macOS）**：直接 `copyFile` + `os.Chmod(0o755)` 即可，没有文件锁问题。

### 7. 两个入口的更新触发方式

| 入口 | 触发方式 |
|------|----------|
| CLI `./cli update` | 用户主动执行 `update` 子命令 |
| TUI `./tui` | 启动时自动检查，检测到更新后询问用户 |

两者核心逻辑相同，只是用户交互方式不同（CLI 用命令行参数 `--yes` / `--check-only`，TUI 用交互式 yes/no 提示）。

CLI 支持纯检查模式：
```bash
./cli update --check-only    # 只报告不安装
./cli update --yes           # 跳过确认直接安装
```

### 8. 数据流总结

```
push v* tag
    │
    ▼
GitHub Actions (release.yml)
    │
    ├─ 交叉编译 8 个平台二进制
    ├─ ldflags 注入 Version + Commit
    └─ 上传为 GitHub Release assets
           │
           ▼
    用户运行 cli / tui
           │
           ▼
    updatecheck.Check()
           │
           ├─ 版本模式: 比较 Version vs Release tag_name
           └─ 开发模式: 比较 git HEAD vs 远端 HEAD
                  │
                  ▼ (Available = true)
            询问用户是否更新
                  │
                  ▼ (yes)
    updatecheck.LatestRelease()
                  │
                  ▼
    AssetForCurrentPlatform("cli"|"tui")
                  │
                  ▼
    DownloadAsset() → .updates/
                  │
                  ▼
    installSelfUpdate() → 临时副本 apply-update → 替换自身
```

### 9. 关键设计决策

- **CLI 和 TUI 独立更新**：两个入口各自检查、各自下载、各自替换。CLI 不会更新 TUI，反之亦然。
- **不使用源码快照**：老版本可能用过 `DownloadSnapshot()` 下载 zip 源码，当前版本已改为下载预编译二进制，不再需要用户本地有 Go 环境。
- **不需要 GitHub Token**：只读访问公开 Release，不调用需要认证的 API。
- **安装助手模式**：通过临时副本进程执行 `apply-update` 解决"自己替换自己"的问题，比下载独立 installer 脚本更简洁，也不依赖外部工具。
