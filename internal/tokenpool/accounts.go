// Package tokenpool 管理本地登录凭证。
//
// 存储格式（accounts.json，权限 0600）是唯一真值源：
//
//	{
//	  "version": 1,
//	  "primary": "acct-1",            // 主账号的 alias（exam 默认使用）
//	  "accounts": [
//	    {"alias": "acct-1", "token": "...", "added_at": "...", "note": "学号"}
//	  ]
//	}
//
// 从旧版（.token/.tokens 时代）升级不迁移凭证：重新 login 一次即可。
package tokenpool

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// DefaultAccountsFile 是统一凭证库文件。
const DefaultAccountsFile = "accounts.json"

// Account 是凭证库中的一个账号。
type Account struct {
	Alias   string    `json:"alias"`
	Token   string    `json:"token"`
	AddedAt time.Time `json:"added_at"`
	Note    string    `json:"note,omitempty"`
}

// Store 是统一凭证库。
type Store struct {
	Version  int       `json:"version"`
	Primary  string    `json:"primary"` // 主账号的 alias
	Accounts []Account `json:"accounts"`

	path string
}

// LoadStore 读取凭证库；accounts.json 不存在时返回空库（首次 login/addtoken 时才落盘）。
func LoadStore(path string) (*Store, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		var s Store
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("解析凭证库 %s 失败: %w（该文件损坏时可直接删除后重新 login）", path, err)
		}
		if s.Version == 0 {
			s.Version = 1
		}
		s.path = path
		return &s, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	return &Store{Version: 1, path: path}, nil
}

// Save 以 0600 权限原子写回凭证库（先写临时文件再改名）。
func (s *Store) Save() error {
	if s.path == "" {
		return fmt.Errorf("store has no path")
	}
	s.Version = 1
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Upsert 把 token 加入凭证库。token 已存在时返回既有 alias 与 added=false；
// alias 留空时自动生成 acct-N；alias 与其他账号冲突时报错。
func (s *Store) Upsert(token, alias, note string) (string, bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false, fmt.Errorf("empty token")
	}
	for i, a := range s.Accounts {
		if a.Token == token {
			if note != "" && a.Note == "" {
				s.Accounts[i].Note = note
			}
			return a.Alias, false, nil
		}
	}
	if strings.TrimSpace(alias) == "" {
		alias = fmt.Sprintf("acct-%d", len(s.Accounts)+1)
	}
	for _, a := range s.Accounts {
		if a.Alias == alias {
			return "", false, fmt.Errorf("账号别名 %q 已被占用", alias)
		}
	}
	s.Accounts = append(s.Accounts, Account{Alias: alias, Token: token, AddedAt: time.Now(), Note: note})
	return alias, true, nil
}

// SetPrimaryByToken 把指定 token 的账号设为主账号。
func (s *Store) SetPrimaryByToken(token string) error {
	token = strings.TrimSpace(token)
	for _, a := range s.Accounts {
		if a.Token == token {
			s.Primary = a.Alias
			return nil
		}
	}
	return fmt.Errorf("凭证库中没有该 token（%s）", Format(token, false))
}

// PrimaryToken 返回主账号的 token；未设置或账号不存在时返回空串。
func (s *Store) PrimaryToken() string {
	for _, a := range s.Accounts {
		if a.Alias == s.Primary {
			return a.Token
		}
	}
	return ""
}

// Tokens 返回全部账号的 token 列表（供 collect 并发使用）。
func (s *Store) Tokens() []string {
	out := make([]string, 0, len(s.Accounts))
	for _, a := range s.Accounts {
		out = append(out, a.Token)
	}
	return out
}

// LoginAction 描述 LoginPrimary 对凭证库做了什么，供前端输出准确提示。
type LoginAction string

const (
	// LoginSwitched：token 已在库中，仅把主账号切过去。
	LoginSwitched LoginAction = "switched"
	// LoginRefreshed：alias 命中已有账号，原地替换其 token（会话轮换后的刷新）。
	LoginRefreshed LoginAction = "refreshed"
	// LoginReplacedPrimary：未指定 alias，原地替换主账号的 token（最常见的"重新登录"）。
	LoginReplacedPrimary LoginAction = "replaced-primary"
	// LoginAdded：追加了新账号并设为主账号。
	LoginAdded LoginAction = "added"
)

// LoginPrimary 实现 login 的替换语义，返回受影响账号的别名与动作：
//  1. token 已在库中 → 只切换主账号指向；
//  2. alias 命中已有账号 → 原地替换该账号的 token 并设为主账号；
//  3. 未指定 alias 且已有主账号 → 原地替换主账号的 token（重新登录同一账号）；
//  4. 其余情况 → 追加新账号（alias 自动编号）并设为主账号。
//
// 想新增一个不同的账号做主账号：先 addtoken 再 setprimary。
func (s *Store) LoginPrimary(token, alias string) (string, LoginAction, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", "", fmt.Errorf("empty token")
	}
	alias = strings.TrimSpace(alias)

	// 1. token 已存在：切换主账号
	for _, a := range s.Accounts {
		if a.Token == token {
			s.Primary = a.Alias
			return a.Alias, LoginSwitched, nil
		}
	}

	// 2. alias 命中：原地替换该账号的 token
	if alias != "" {
		for i, a := range s.Accounts {
			if a.Alias == alias {
				s.Accounts[i].Token = token
				s.Primary = alias
				return alias, LoginRefreshed, nil
			}
		}
	}

	// 3. 未指定 alias 且已有主账号：原地替换主账号的 token
	if alias == "" && s.Primary != "" {
		for i, a := range s.Accounts {
			if a.Alias == s.Primary {
				s.Accounts[i].Token = token
				return s.Primary, LoginReplacedPrimary, nil
			}
		}
	}

	// 4. 追加新账号并设为主账号
	name, _, err := s.Upsert(token, alias, "")
	if err != nil {
		return "", "", err
	}
	s.Primary = name
	return name, LoginAdded, nil
}

// RemoveAccount 按 alias 或 token 删除账号，返回被删账号的别名。
// 删除的是主账号时同时清空主账号指向（需要重新 login 或 setprimary）。
func (s *Store) RemoveAccount(aliasOrToken string) (string, error) {
	aliasOrToken = strings.TrimSpace(aliasOrToken)
	if aliasOrToken == "" {
		return "", fmt.Errorf("需要 --alias 或 --token")
	}
	for i, a := range s.Accounts {
		if a.Alias == aliasOrToken || a.Token == aliasOrToken {
			s.Accounts = append(s.Accounts[:i], s.Accounts[i+1:]...)
			if s.Primary == a.Alias {
				s.Primary = ""
			}
			return a.Alias, nil
		}
	}
	return "", fmt.Errorf("凭证库中没有匹配 %q 的账号", Format(aliasOrToken, false))
}

// PrintAccounts 把账号列表打印到 w（含主账号标记、添加时间与打码凭证）。
func (s *Store) PrintAccounts(w io.Writer, plain bool) {
	for i, a := range s.Accounts {
		role := "member "
		if a.Alias == s.Primary {
			role = "primary"
		}
		fmt.Fprintf(w, "%d. (%s) %-12s 添加于 %s  %s\n", i+1, role, a.Alias, a.AddedAt.Format("2006-01-02"), Format(a.Token, plain))
		if a.Note != "" {
			fmt.Fprintf(w, "   备注：%s\n", a.Note)
		}
	}
}

// Path 返回凭证库文件路径。
func (s *Store) Path() string { return s.path }

// Format 打码显示凭证；plain 为 true 或凭证长度不超过 12 时原样返回。
func Format(token string, plain bool) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "(empty)"
	}
	if plain || len(token) <= 12 {
		return token
	}
	return token[:6] + "..." + token[len(token)-6:]
}
