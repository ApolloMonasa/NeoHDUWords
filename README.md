# HDU Words
>本项目源自作者对新我爱记单词网站Auth机制的探索，当前该工具维护测试网站是可行的，用于其他场景不保证没有bug，若有，可开Issue或邮箱联系。

这是当前发布的 CLI + TUI 双入口版本，用于自动化执行“我爱记单词”答题链路，并把官方正确答案沉淀到本地 SQLite 题库。TUI 已经拆成独立程序，可以双击进入黑窗口，不再和 CLI 混在一起。
CLI 与 TUI 功能等价。TUI 的交互表单已覆盖 CLI 主要参数（如 rate/timeout/unknown-policy/submit-retries/submit-retry-interval 等）。

> 免责声明：本工具仅用于经授权的测试、验收与回归场景。禁止用于未授权环境。

## 一、先看三件事

1. 账号来源
- login 维护主账号（写入 .token）
- addtoken 维护采集账号池（写入 .tokens）

2. 数据库来源
- 不传 --db 时，默认使用当前目录下的 hduwords.db
- 推荐显式传 --db mywords.db，避免和默认库混淆

3. 三种模式分工
- collect：循环收集题库（支持 token 池并发，固定 type=0）
- test：练习回归（固定 type=0）
- exam：正式考试回归（固定 type=1）

## 二、快速上手

### 1. 编译

```bash
git clone https://github.com/ApolloMonasa/NeoHDUWords.git
cd NeoHDUWords
go mod tidy
go build -o cli.exe ./cmd/hduwords
go build -o tui.exe ./cmd/tui
```

说明：TUI 的自动更新依赖 GitHub Release 提供的二进制资产，不再下载源码快照。

### 2. 登录主账号

```bash
./cli.exe login
```

执行后会自动打开浏览器，完成登录后把 token 保存到 .token。

### 3. 开始收集

```bash
./cli.exe collect --db mywords.db --cooldown 5m
```

### 4. 进入 TUI

```bash
./tui.exe
```

进入后会先显示发行信息、标题 banner 和仓库地址，然后自动检查仓库更新；如检测到更新，会先询问是否下载更新包，再进入功能菜单。

TUI 发现更新时会优先下载并安装对应平台的 Release 二进制；若当前 Release 缺少匹配资产，会提示用户无法自动更新。

## 三、账号与 Token 池

### 主账号与池账号规则

- .token：主账号。test/exam 默认只用它（除非显式传 --url）
- .tokens：采集账号池。collect 可并发使用
- .tokens 里以 * 开头的行表示 primary，例如 *token_xxx

### 常用命令

追加一个采集账号：

```bash
./cli.exe addtoken
```

查看账号列表（默认掩码展示）：

```bash
./cli.exe listtokens
```

设置主账号并同步到 .token：

```bash
./cli.exe setprimary --token <token>
```

只改池内 primary，不改 .token：

```bash
./cli.exe setprimary --token <token> --sync-login=false
```

## 四、按场景使用

### collect：题库收集（支持并发）

```bash
./cli.exe collect --db mywords.db --cooldown 5m
```

说明：
- 会优先新建试卷；失败时回退活跃试卷
- 提交遇到 403：先按默认 10 秒的间隔重试，再新建试卷兜底；日志会标注具体 worker
- 收集结束后会回写官方答案到数据库

### test：练习回归（type=0）

```bash
./cli.exe test --db mywords.db --dry-run
./cli.exe test --db mywords.db
```

### exam：正式考试回归

```bash
./cli.exe exam --db mywords.db --time 30s --score 100
```

说明：
- mode 已固定类型：collect/test 自动使用 type=0，exam 自动使用 type=1；可用 --time 控制交卷前等待时长，--score 控制目标得分百分比
- exam 每次都会新建试卷，不复用活跃试卷
- 遇到未知题会按 random 策略继续
- 提交后也会自动回收答案入库
- 提交完成后会额外校验该试卷是否出现在考试列表中，便于排查记录页不显示的问题

## 五、题库导出与统计

导出 JSON：

```bash
./cli.exe db export --db mywords.db --out export.json
```

导出 Markdown（推荐）：

```bash
./cli.exe db markdown --db mywords.db --out export.md
```

说明：db markdown 等价于 db export --format markdown。

查看统计：

```bash
./cli.exe db stats --db mywords.db
```

## 六、关键参数速查

通用（collect/test/exam）：
- --db：数据库路径，默认 hduwords.db
- --rate：请求速率，默认 2
- --timeout：请求超时，默认 15s
- --ua：自定义 UA；默认是真实浏览器风格的 Windows Chrome UA（exam 模式会强制覆盖为移动端 UA）
- --submit-retries：提交 403 重试次数，默认 3
- --submit-retry-interval：提交 403 重试间隔，默认 10s

test/exam：
- --unknown-policy abort|skip|random（exam 会强制 random）

collect 专属：
- --cooldown：每轮冷却，默认 5m
- --pool-file：token 池文件，默认 .tokens
- --workers：并发 worker 数
  - 0（默认，自动）
    - .tokens 有 token：worker = token 数
    - .tokens 为空：worker = 1（走 --url 或 .token）
  - >0：worker = min(n, 可用 token 数)

## 七、常见疑问

### 1) 为什么 db export 输出是空或很小？

- 先确认你是否用了正确的 --db
- 默认库是 hduwords.db，可能和你实际采集库不同
- 导出的是可用题库条目（题干+选项+正确索引），不是整个 SQLite 原始数据

### 2) type 怎么选？

- 不需要手动选择。collect/test/exam 模式已自动绑定对应 type

## 八、开发与测试

```bash
go test -v ./...
```

项目结构：
- cmd/hduwords/main.go：命令入口与流程编排
- cmd/hduwords/login.go：登录、token 池管理
- cmd/tui/main.go：TUI 独立入口与隐藏安装模式
- internal/tuiapp：TUI 启动、更新检查与菜单流程
- internal/updatecheck：GitHub Release 更新检查与安装辅助
- internal/sklclient：API 客户端
- internal/store：SQLite 题库存储
- internal/match：题目匹配哈希

## 九、Push 前检查

建议在 push 前执行：

```bash
go test ./...
go build ./cmd/hduwords
go build ./cmd/tui
```

可选（检查当前改动）：

```bash
git status
git diff --stat
```
