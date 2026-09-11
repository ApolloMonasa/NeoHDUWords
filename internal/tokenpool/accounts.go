// Package tokenpool 管理本地登录凭证（accounts.json，权限 0600）。
//
// 设计为最简扁平存储：所有 token 放在同一张列表里，不带别名等附加概念，
// 主账号用 primary 布尔列标记（至多一个），供 exam 使用；列表中全部 token
// 都可用于 collect 并发。凭证被判定失效（登录过期）时由调用方直接删除记录：
//
//	{
//	  "version": 2,
//	  "tokens": [
//	    {"token": "...", "primary": true,  "added_at": "..."},
//	    {"token": "...", "primary": false, "added_at": "..."}
//	  ]
//	}
//
// 旧版带别名的 v1 格式在读取时自动升级为 v2（同一文件内，别名直接丢弃）。
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

// Account 是凭证库中的一条凭证记录。
type Account struct {
	Token   string    `json:"token"`
	Primary bool      `json:"primary"`
	AddedAt time.Time `json:"added_at"`
}

// Store 是统一凭证库。
type Store struct {
	Version int       `json:"version"`
	Tokens  []Account `json:"tokens"`

	path string
}

// v1Store 是带别名的旧格式（仅用于读取升级）。
type v1Store struct {
	Version  int    `json:"version"`
	Primary  string `json:"primary"`
	Accounts []struct {
		Alias   string    `json:"alias"`
		Token   string    `json:"token"`
		AddedAt time.Time `json:"added_at"`
	} `json:"accounts"`
}

// LoadStore 读取凭证库；v1（带别名）自动升级为 v2 并原位写回。
// 文件不存在时返回空库（首次 login/addtoken 时才落盘）。
func LoadStore(path string) (*Store, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Store{Version: 2, path: path}, nil
		}
		return nil, err
	}

	var s Store
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("解析凭证库 %s 失败: %w（该文件损坏时可直接删除后重新 login）", path, err)
	}
	if s.Tokens != nil {
		s.Version = 2
		s.path = path
		return &s, nil
	}

	// v1 格式：丢弃别名，按旧 primary 别名保留主账号标记
	var old v1Store
	if err := json.Unmarshal(b, &old); err != nil || old.Accounts == nil {
		return nil, fmt.Errorf("解析凭证库 %s 失败: 无法识别的格式（可直接删除后重新 login）", path)
	}
	s = Store{Version: 2, path: path}
	for _, a := range old.Accounts {
		s.Tokens = append(s.Tokens, Account{Token: a.Token, Primary: a.Alias == old.Primary, AddedAt: a.AddedAt})
	}
	if err := s.Save(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Save 以 0600 权限原子写回凭证库（先写临时文件再改名）。
func (s *Store) Save() error {
	if s.path == "" {
		return fmt.Errorf("store has no path")
	}
	s.Version = 2
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

// Upsert 把 token 追加进凭证库（普通成员，不标记 primary）；
// 已存在时不重复写入。返回是否实际新增。
func (s *Store) Upsert(token string) (bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return false, fmt.Errorf("empty token")
	}
	for _, a := range s.Tokens {
		if a.Token == token {
			return false, nil
		}
	}
	s.Tokens = append(s.Tokens, Account{Token: token, AddedAt: time.Now()})
	return true, nil
}

// LoginAction 描述 LoginPrimary 对凭证库做了什么，供前端输出准确提示。
type LoginAction string

const (
	// LoginSwitched：token 已在库中，仅把主账号标记移过去。
	LoginSwitched LoginAction = "switched"
	// LoginReplacedPrimary：原地替换主账号的 token（会话轮换后的重新登录）。
	LoginReplacedPrimary LoginAction = "replaced-primary"
	// LoginAdded：追加了新凭证并标记为主账号。
	LoginAdded LoginAction = "added"
)

// LoginPrimary 实现 login 语义：
//  1. token 已在库中 → 把主账号标记移到它；
//  2. 已有主账号 → 原地替换主账号的 token（重新登录同一账号）；
//  3. 其余（无主账号/空库）→ 追加新凭证并标记为主账号。
func (s *Store) LoginPrimary(token string) (LoginAction, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("empty token")
	}

	// 1. token 已存在：切换主账号标记
	for i, a := range s.Tokens {
		if a.Token == token {
			for j := range s.Tokens {
				s.Tokens[j].Primary = j == i
			}
			return LoginSwitched, nil
		}
	}

	// 2. 已有主账号：原地替换其 token
	for i, a := range s.Tokens {
		if a.Primary {
			s.Tokens[i] = Account{Token: token, Primary: true, AddedAt: time.Now()}
			return LoginReplacedPrimary, nil
		}
	}

	// 3. 追加并标记为主账号
	s.Tokens = append(s.Tokens, Account{Token: token, Primary: true, AddedAt: time.Now()})
	return LoginAdded, nil
}

// PrimaryToken 返回主账号 token；不存在主账号时返回空串。
func (s *Store) PrimaryToken() string {
	for _, a := range s.Tokens {
		if a.Primary {
			return a.Token
		}
	}
	return ""
}

// SetPrimary 把主账号标记移到指定 token 上（只允许在已有凭证中切换）。
func (s *Store) SetPrimary(token string) error {
	token = strings.TrimSpace(token)
	found := false
	for i, a := range s.Tokens {
		if a.Token == token {
			found = true
			s.Tokens[i].Primary = true
		} else {
			s.Tokens[i].Primary = false
		}
	}
	if !found {
		return fmt.Errorf("凭证库中没有该 token（%s）", Format(token, false))
	}
	return nil
}

// AllTokens 返回全部凭证的 token 列表（供 collect 并发使用）。
func (s *Store) AllTokens() []string {
	out := make([]string, 0, len(s.Tokens))
	for _, a := range s.Tokens {
		out = append(out, a.Token)
	}
	return out
}

// RemoveToken 删除指定 token 的记录；删除主账号时主账号标记随之消失
// （此后 exam 会报"不存在主账户"）。token 不存在时返回 nil（幂等）。
func (s *Store) RemoveToken(token string) error {
	token = strings.TrimSpace(token)
	for i, a := range s.Tokens {
		if a.Token == token {
			s.Tokens = append(s.Tokens[:i], s.Tokens[i+1:]...)
			return nil
		}
	}
	return nil
}

// RemoveTokenPersistent 从凭证库文件中删除指定 token（重新加载后删除再写回，
// 避免与内存中的其他修改互相覆盖）。用于"凭证失效即删除"的自动清理。
func RemoveTokenPersistent(path, token string) error {
	st, err := LoadStore(path)
	if err != nil {
		return err
	}
	if err := st.RemoveToken(token); err != nil {
		return err
	}
	return st.Save()
}

// PrintAccounts 把凭证列表打印到 w（含主账号标记、添加时间与打码凭证）。
func (s *Store) PrintAccounts(w io.Writer, plain bool) {
	for i, a := range s.Tokens {
		role := "member "
		if a.Primary {
			role = "primary"
		}
		added := "未知"
		if !a.AddedAt.IsZero() {
			added = a.AddedAt.Format("2006-01-02")
		}
		fmt.Fprintf(w, "%d. (%s) %s  添加于 %s\n", i+1, role, Format(a.Token, plain), added)
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
