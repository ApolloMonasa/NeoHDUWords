# HDU Words

> 自动化"我爱记单词"答题工具，支持题库收集与正式考试。提供 **TUI（推荐）** 和 CLI 双入口。
<img width="1094" height="722" alt="image" src="https://github.com/user-attachments/assets/ceef34a6-363d-45c4-b623-84283061e7e7" />

## 下载

[![GitHub Release](https://img.shields.io/github/v/release/ApolloMonasa/NeoHDUWords?label=latest)](https://github.com/ApolloMonasa/NeoHDUWords/releases/latest)

从 [Releases 页面](https://github.com/ApolloMonasa/NeoHDUWords/releases) 下载对应平台的二进制文件：

| 平台 | TUI | CLI |
|------|-----|-----|
| Windows | `tui-windows-*.exe` | `cli-windows-*.exe` |
| Linux | `tui-linux-*` | `cli-linux-*` |
| macOS | `tui-darwin-*` | `cli-darwin-*` |

下载后直接运行即可，无需安装依赖。

---

## 快速上手（TUI，推荐）

双击 `tui.exe`（Windows）或在终端 `./tui` 启动。

```
主菜单
  1. 登录          — 浏览器认证，自动保存 token
  2. 收集          — 循环收集题库，支持多账号并发
  3. 考试          — 正式考试作答，支持控分
  4. 数据库        — 统计、导出、更新题库
  5. 账号管理      — 多账号 token 池管理
  0. 退出
```

**首次使用三步走：**

```
1 → 登录（浏览器完成学校统一认证）
4 → update（下载最新题库数据库）
2 → 收集（或 3 → 考试）
```

TUI 启动时会自动检查更新，检测到新版本会提示下载安装。

---

## CLI 快速上手

```bash
# 1. 登录
./cli login

# 2. 下载最新题库
./cli db update

# 3. 收集题库（循环）
./cli collect --db mywords.db

# 4. 正式考试（等待 30 秒交卷，目标 100 分）
./cli exam --db mywords.db --time 30s --score 100
```

CLI 自更新：
```bash
./cli update              # 检查并安装更新
./cli update --check-only # 仅检查
```

---

## 账号与 Token 池

| 文件 | 用途 |
|------|------|
| `.token` | 主账号，exam 默认使用 |
| `.tokens` | 采集账号池，collect 可并发使用，以 `*` 开头的行为 primary |

```bash
./cli login                        # 登录主账号
./cli addtoken                     # 追加采集账号
./cli listtokens                   # 查看账号列表
./cli setprimary --token <token>   # 设置 primary
```

---

## 数据库管理

```bash
./cli db update                     # 从 GitHub Release 下载最新题库
./cli db stats --db mywords.db      # 查看统计
./cli db export --db mywords.db     # 导出 JSON
./cli db markdown --db mywords.db   # 导出 Markdown
```

---

## 命令参考

### 收集

```bash
./cli collect --db mywords.db [--cooldown 5m] [--workers 0] [--pool-file .tokens]
```

- 优先新建试卷，失败时回退活跃试卷
- 提交 403 自动重试（默认 3 次，间隔 10s）
- 提交后回写官方正确答案到数据库

### 考试

```bash
./cli exam --db mywords.db [--time 30s] [--score 100] [--dry-run]
```

- `--time`：交卷前等待时长，默认 0s
- `--score`：目标得分百分比（0-100），默认不启用
- `--dry-run`：只演练不提交
- 每次新建试卷，未知题随机作答
- 完成后自动回收答案入库

### 通用参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--db` | `hduwords.db` | 数据库路径 |
| `--rate` | `2` | 请求速率 |
| `--timeout` | `15s` | 请求超时 |
| `--url` | 自动读取 `.token` | Token URL |
| `--submit-retries` | `3` | 403 重试次数 |
| `--submit-retry-interval` | `10s` | 403 重试间隔 |

---

## 常见疑问

### 数据库从哪来？
运行 `./cli db update`（或 TUI 中选 数据库 → update）自动从 GitHub Release 下载最新题库。也可以从零开始用 collect 模式积累。

### 为什么 db export 输出很少或为空？
检查 `--db` 参数是否正确指向你采集用的数据库文件。

### exam 和 collect 的区别？
- **collect**：type=0，循环收集，适合长期积累题库
- **exam**：type=1，每次新建试卷，支持控分和延迟交卷

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
| `cmd/tui/` | TUI 独立入口 |
| `internal/tuiapp/` | TUI 菜单、交互与流程 |
| `internal/browser/` | Chrome/Edge 浏览器自动检测与 Token 捕获 |
| `internal/sklclient/` | API 客户端 |
| `internal/store/` | SQLite 题库存储 |
| `internal/updatecheck/` | GitHub Release 更新检查与安装 |
| `internal/match/` | 题目匹配哈希 |

---

> 免责声明：本工具仅用于经授权的测试、验收与回归场景。禁止用于未授权环境。
