# 内置更新机制

## 通俗版：它是怎么工作的

整个更新机制可以分成四步：

1. **编译时打标签** — 每次 GitHub Actions 编译二进制时，会用 `-ldflags` 把版本号（如 `v1.2.3`）和 Git commit SHA 写进二进制文件里。这样每个 `.exe` 文件都"知道自己是谁"。

2. **启动时查更新** — CLI 和 TUI 启动后，会拿自己的版本号去问 GitHub："你们仓库最新的 Release 是哪个？" 如果自己的版本号和远端不一样，就提示用户有新版本可用。

3. **下载新版本** — 用户确认更新后，程序从 GitHub Release 页面下载匹配当前操作系统和 CPU 架构的二进制文件，保存到 `.updates/` 目录。

4. **校验并替换自身** — 下载完成后先做 SHA256 完整性校验（如果该发行版提供了 SHA256SUMS 文件），然后通过"安装助手"进程完成自替换。一个运行中的程序不能直接覆盖自己，所以它会把自身复制到一个临时目录，用那个副本启动"安装助手"，传给它两个路径：下载的新文件在哪、要覆盖的原文件在哪。安装助手启动后，原进程退出，助手把新文件拷过去，完成替换。

整个过程不需要用户手动去 GitHub 下载任何东西。

---

## 技术细节

### 1. 版本信息注入

文件：`internal/buildinfo/buildinfo.go`

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

- CI 构建（`.github/workflows/ci.yml`）：`Version` 固定为 `"dev"`，`Commit` 为当前提交 SHA。CI 构建不用于分发，标记为 `dev` 意味着"开发版，不走版本号比对"。
- Release 构建（`.github/workflows/release.yml`）：`Version` 为 git tag（如 `v1.2.3`），`Commit` 为当前提交 SHA。

### 2. 发行版构建与资产命名

Release 流程由推 `v*` 标签触发，先跑 `go test ./...` 作为发布门禁，再交叉编译出 4 个平台 × 2 个入口 = 8 个二进制文件：

| 文件 | 平台 |
|------|------|
| `cli-linux-amd64` / `tui-linux-amd64` | Linux x86_64 |
| `cli-windows-amd64.exe` / `tui-windows-amd64.exe` | Windows x86_64 |
| `cli-darwin-amd64` / `tui-darwin-amd64` | macOS Intel |
| `cli-darwin-arm64` / `tui-darwin-arm64` | macOS Apple Silicon |

文件命名规则：`{binary}-{os}-{arch}[.exe]`。构建完成后生成 `SHA256SUMS`（对 dist 下所有文件做 sha256），与二进制一起上传为 Release 资产。资产命名是 `AssetForCurrentPlatform()` 的匹配依据，不可改变格式。

### 3. 更新检查流程

入口在 `internal/updatecheck/updatecheck.go` 的 `Check()` 函数。

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

本质上：**正式发布用 tag 比，开发版用 git commit 比。**

### 4. 资产匹配与下载

`internal/updatecheck/release.go`：

- `AssetForCurrentPlatform(binaryName)`：按 `{binaryName}-{GOOS}-{GOARCH}` 在 Release 资产中大小写不敏感匹配（带或不带 `.exe`）。CLI 传 `"cli"`，TUI 传 `"tui"`。
- `DownloadAsset()`：用 asset 的 `browser_download_url` 下载到 `.updates/` 目录（TUI 可用 `--updates-dir` 自定义）。

### 5. SHA256 完整性校验

`internal/updatecheck/release.go` 的 `VerifyAssetChecksum()`：

- 从同一 Release 下载 `SHA256SUMS`，解析出 `文件名 → sha256` 映射，与本地下载文件的实际哈希比对。
- 返回值区分三种"无法校验"的情况（哨兵错误，调用方决定放行）：
  - `ErrNoSumsAsset`：该 Release 没有附带 SHA256SUMS（历史版本）→ 提示后跳过校验
  - `ErrAssetNotInSums`：SUMS 里没有该资产条目 → 提示后跳过
  - 哈希不匹配 → **中止安装**（真错误）
- 注意信任模型：SUMS 与二进制同源同渠道，校验防的是**传输损坏**，不是供应链投毒（攻击者可同时替换两者）。身份验证需要签名，目前未实现。

### 6. 自替换（安装）

这是最精巧的部分。运行中的进程不能覆写自己的可执行文件（Windows 会锁文件，Linux 虽允许但行为不安全）。

`internal/updater/updater.go` 的 `InstallSelfUpdate()`：

```
1. os.Executable()  → 拿到自己的路径，比如 /usr/local/bin/cli
2. os.MkdirTemp()   → 创建临时目录，比如 /tmp/hduwords-updater-xxxx/
3. 把自己拷贝到临时目录 → /tmp/hduwords-updater-xxxx/cli
4. exec.Command(临时副本, "apply-update", "--source", <新文件>, "--target", <原文件>) 启动安装助手
5. 原进程返回并退出
```

安装助手协议在 CLI 与 TUI 间统一：`apply-update --source <新文件> --target <原文件>` 子命令
（分别由 `cmd/hduwords` 与 `cmd/tui` 处理）。自更新时执行助手的是当前进程的临时副本，
协议只会"新配新"，因此调整协议不影响线上旧版本。

**Windows 特殊处理**（`internal/updatecheck/install.go` 的 `installBinaryWindows`）：exe 被运行时无法直接覆盖，最多重试 20 次（每 250ms）删除旧文件后再拷贝新文件。POSIX 直接"写临时文件 + rename"原子替换。

### 7. 共用编排层

`internal/updater` 包承载两入口共用的完整交互流程（`Run()`）：

```
检查版本（20s 超时）→ 打印状态
  → 无更新：结束
  → 确认下载并安装？（--yes 跳过询问）
  → 下载资产 → SHA256 校验
  → 确认安装？
  → InstallSelfUpdate → 原进程退出
```

CLI 的 `update` 子命令和 TUI 启动时的自动检查都只是给它传参数（二进制名、安装助手参数风格、确认策略）并承接结果，不再各自维护一份流程。

### 8. 数据流总结

```
push v* tag
    │
    ▼
GitHub Actions (release.yml)
    │
    ├─ go test ./...（发布门禁）
    ├─ 交叉编译 8 个平台二进制
    ├─ ldflags 注入 Version + Commit
    └─ 生成 SHA256SUMS，全部上传为 Release 资产
           │
           ▼
    用户运行 cli / tui
           │
           ▼
    updatecheck.Check()
    （版本模式: Version vs tag_name；开发模式: git HEAD vs 远端 HEAD）
           │
           ▼ (Available = true)
    updater.Run()：确认 → 下载 → SHA256 校验
           │
           ▼
    InstallSelfUpdate() → 临时副本 apply-update → 替换自身
```

### 9. 关键设计决策

- **CLI 和 TUI 独立更新**：两个入口各自检查、各自下载、各自替换。CLI 不会更新 TUI，反之亦然。
- **预编译二进制而非源码快照**：用户不需要本地 Go 环境。
- **不需要 GitHub Token**：只读访问公开 Release，不调用需要认证的 API。
- **校验可选、失败即停**：没有 SHA256SUMS 的历史发行版自动跳过校验（向后兼容）；有而不匹配则坚决不装。
- **安装助手模式**：通过临时副本进程执行替换，比下载独立 installer 脚本更简洁，也不依赖外部工具。
