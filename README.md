# 我爱记单词？？？
# 我不爱记单词！！！


---

# HDU Words

> 杭电"我爱记单词"自动答题工具：先把题目和正确答案收集成本地题库，考试时自动从题库里找答案作答。提供 **TUI（推荐，跟着提示选数字就行）** 和 CLI 两种用法。
<img width="1094" height="722" alt="image" src="https://github.com/user-attachments/assets/ceef34a6-363d-45c4-b623-84283061e7e7" />

## 下载

[![GitHub Release](https://img.shields.io/github/v/release/ApolloMonasa/NeoHDUWords?label=latest)](https://github.com/ApolloMonasa/NeoHDUWords/releases/latest)

从 [Releases 页面](https://github.com/ApolloMonasa/NeoHDUWords/releases) 下载对应平台的程序，下载后直接运行，无需安装任何依赖：

| 平台 | TUI（推荐） | CLI |
|------|-----|-----|
| Windows | `tui-windows-amd64.exe` | `cli-windows-amd64.exe` |
| Linux | `tui-linux-amd64` | `cli-linux-amd64` |
| macOS（Intel） | `tui-darwin-amd64` | `cli-darwin-amd64` |
| macOS（M1/M2/M3/M4） | `tui-darwin-arm64` | `cli-darwin-arm64` |

> macOS 第一次打开如果提示"无法验证开发者"：在终端执行 `xattr -d com.apple.quarantine /下载路径/tui-darwin-arm64`（换成你下载的文件名），之后就能正常运行了。

## 使用前的准备

- **电脑上需要装有 Chrome 或 Edge 浏览器**（二选一即可）：登录功能会自动调起浏览器完成学校统一认证，工具会自动检测（优先找 Chrome，找不到再找 Edge）。
- 建议把程序放进一个**单独的文件夹**再运行：登录凭证（`.token`）、题库（`hduwords.db`）等文件都会生成在运行目录里，放在一起好找、好备份。

---

## 快速上手（TUI，推荐）

双击 `tui.exe`（Windows），或在终端执行 `./tui`（Linux/macOS）。

```
主菜单
  1. 登录          — 自动打开浏览器，登录学校账号后自动保存凭证
  2. 收集          — 循环刷题攒题库，支持多账号同时采集
  3. 考试          — 正式自动考试，支持控分、定时交卷
  4. 数据库        — 统计、导出、下载最新题库
  5. 账号管理      — 管理多个账号的登录凭证
  0. 退出
```

**第一次使用三步走：**

```
1 → 登录     会弹出浏览器，完成学校统一身份认证，
             成功后工具自动保存凭证，全程不用复制任何东西
4 → update   下载现成的最新题库（推荐，直接就有题可答）
2 → 收集     或 3 → 考试
```

小提示：

- 过程中所有提问（数据库路径、速率等）**直接回车就是默认值**，不确定就一路回车。
- 收集是无限循环，想停按 **Ctrl+C**（TUI 中会自动回到主菜单）。
- TUI 每次启动会自动检查更新，有新版本会询问你是否下载安装。

---

## CLI 快速上手

> Windows 用户把下文的 `./cli` 换成 `.\cli.exe` 即可。

```bash
# 1. 登录（弹出浏览器完成认证）
./cli login

# 2. 下载最新题库
./cli db update

# 3. 收集题库（无限循环，Ctrl+C 停止）
./cli collect --db mywords.db

# 4. 正式考试（等 30 秒交卷，目标 100 分）
./cli exam --db mywords.db --time 30s --score 100
```

程序自更新：

```bash
./cli update              # 检查并安装更新
./cli update --check-only # 仅检查，不安装
```

---

## 账号与 Token

登录成功后，运行目录下会生成两个文件：

| 文件 | 用途 |
|------|------|
| `.token` | 主账号的登录凭证，考试（exam）默认使用它 |
| `.tokens` | 凭证池：收集（collect）可以用池里所有账号并发刷题；`*` 开头的那一行是主账号 |

```bash
./cli login                        # 登录主账号（会同时写入 .tokens 并标记为主账号）
./cli addtoken                     # 再登录一个账号，追加进凭证池（用于多账号并发收集）
./cli listtokens                   # 查看已保存的账号列表
./cli listtokens --show-plain      # 显示完整凭证文本（默认打码显示）
./cli setprimary --token <token>   # 把某个账号设为主账号（默认同步到 .token）
```

---

## 数据库管理

```bash
./cli db update                     # 下载最新题库（保存到当前目录的 hduwords.db）
./cli db stats --db mywords.db      # 查看统计：题目数 / 答案数 / 冲突数
./cli db export --db mywords.db     # 导出为 JSON
./cli db markdown --db mywords.db   # 导出为 Markdown（方便阅读、打印）
```

TUI 中对应：主菜单 `4. 数据库`。

---

## 命令参考

### 登录

```bash
./cli login [--browser chrome|edge]
./cli addtoken [--browser chrome|edge]
```

- `--browser`：留空自动检测（优先 Chrome，其次 Edge），一般不用填

### 收集

```bash
./cli collect [--db mywords.db] [--cooldown 5m] [--workers 0] [--pool-file .tokens]
```

- 题库里有答案的题照常作答，没见过的题空着提交；交卷后把**官方正确答案**回写进数据库，所以每刷一轮题库就变厚一点
- 优先新建试卷，失败时自动回退到未完成的活跃试卷
- 提交遇到 403 会自动重试（默认 3 次、间隔 10 秒），仍失败则换一张新试卷重来
- `--workers`：并发账号数，`0` = 自动（凭证池里有几个账号就开几个）

### 考试

```bash
./cli exam [--db mywords.db] [--time 30s] [--score 100] [--dry-run]
```

- 每次新建一张正式试卷；题库里有答案的题答对，没见过的题随机选
- `--time`：交卷前等待时长，默认 `0s`（答完立即交卷）；TUI 里考试默认等 30 秒
- `--score`：目标得分百分比（0-100），**默认不启用**（即尽力全对）
- `--dry-run`：只演练一遍，不提交
- 完成后自动把本次的官方答案回收入库

### 通用参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--db` | `hduwords.db` | 题库路径（相对当前运行目录） |
| `--url` | 读取 `.token` | 手动提供带 token 的网址（一般用不上） |
| `--rate` | `2` | 请求速率，一般不用改 |
| `--timeout` | `15s` | 单次请求超时 |
| `--submit-retries` | `3` | 提交 403 重试次数 |
| `--submit-retry-interval` | `10s` | 提交 403 重试间隔 |

### 程序自更新

```bash
./cli update [--check-only] [--yes] [--repo owner/name] [--updates-dir .updates]
```

- CLI 和 TUI 各自独立检查、独立更新自己
- 更新包下载后会校验 SHA256（老版本发行版没有校验文件时自动跳过）
- TUI 无需手动操作，启动时自动检查

---

## 常见疑问

### 数据库从哪来？
两种方式：`./cli db update` 直接下载现成题库（推荐）；或者用收集模式自己慢慢攒，两者可以叠加。

### 登录时没弹出浏览器？
检查电脑是否装了 Chrome 或 Edge；也可以手动指定：`./cli login --browser edge`（或 `chrome`）。

### 题库下载到哪了？
`db update` 会把 `hduwords.db` 下载到**当前运行目录**，收集/考试也默认在当前目录找它——只要你在程序所在的文件夹里运行，一切自动就对上了。如果你习惯在别的目录启动程序，记得用 `--db` 指定题库路径。

### 为什么 db export 输出很少或为空？
多半是 `--db` 没指向你实际在用的那个题库文件，检查一下路径。

### exam 和 collect 的区别？
- **collect**：练习模式，无限循环，目的是攒题库（答不出来的题空着交，交卷后回收官方答案）
- **exam**：正式考试，每次一张新卷子，支持控分（`--score`）和延迟交卷（`--time`）

### 想用多个账号一起收集怎么做？
每个账号执行一次 `./cli addtoken` 加入凭证池，之后 `./cli collect` 会自动并发使用池里所有账号。

---

## 开发

```bash
git clone https://github.com/ApolloMonasa/NeoHDUWords.git
cd NeoHDUWords
go build -o cli ./cmd/hduwords
go build -o tui ./cmd/tui
go test ./...
```

### 项目结构

| 目录 | 说明 |
|------|------|
| `cmd/hduwords/` | CLI 入口与命令编排 |
| `cmd/tui/` | TUI 入口（兼作自更新安装助手） |
| `internal/tuiapp/` | TUI 菜单、交互与流程 |
| `internal/browser/` | Chrome/Edge 自动检测与登录凭证捕获 |
| `internal/sklclient/` | 平台 API 客户端 |
| `internal/store/` | SQLite 题库存储 |
| `internal/match/` | 题目匹配哈希 |
| `internal/updatecheck/` | GitHub Release 更新检查与安装 |
| `internal/buildinfo/` | 版本号（编译时注入） |

更新机制的详细设计见 [UPDATE.md](UPDATE.md)。

---

> 免责声明：本工具仅用于经授权的测试、验收与回归场景。禁止用于未授权环境。
