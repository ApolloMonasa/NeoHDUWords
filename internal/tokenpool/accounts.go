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
// 首次加载时若发现旧版 .tokens 文本池或 .token 单账号文件，会自动迁移到
// accounts.json；旧文件原样保留（可手动删除），主账号以旧 .token 为准以保持
// exam 行为不变。
package tokenpool

import (
	"encoding/json"
	"fmt"
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

	path     string
	migrated bool
}

// LoadStore 读取凭证库；accounts.json 不存在时自动从旧版 .tokens/.token 迁移
// （迁移会立刻写出新文件）。没有任何凭证时返回空库。
func LoadStore(path string) (*Store, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		var s Store
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("解析凭证库 %s: %w", path, err)
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
	return migrateLegacy(DefaultPoolFile, DefaultMainFile, path)
}

// migrateLegacy 把旧版凭证迁移为统一凭证库。
func migrateLegacy(poolPath, mainPath, accountsPath string) (*Store, error) {
	s := &Store{Version: 1, path: accountsPath}

	pool, err := loadLegacyPool(poolPath)
	if err != nil {
		return nil, err
	}
	aliasOf := make(map[string]string)
	for _, tk := range pool.Tokens {
		alias := fmt.Sprintf("acct-%d", len(s.Accounts)+1)
		s.Accounts = append(s.Accounts, Account{Alias: alias, Token: tk, AddedAt: time.Now()})
		aliasOf[tk] = alias
		if pool.Primary != "" && tk == pool.Primary {
			s.Primary = alias
		}
	}

	// 旧 .token 是 exam 实际使用的主账号，迁移后仍以它为主
	mainTok, _ := LoadMain(mainPath)
	if mainTok != "" {
		if alias, ok := aliasOf[mainTok]; ok {
			s.Primary = alias
		} else {
			alias := fmt.Sprintf("acct-%d", len(s.Accounts)+1)
			s.Accounts = append(s.Accounts, Account{Alias: alias, Token: mainTok, AddedAt: time.Now()})
			s.Primary = alias
		}
	}

	if len(s.Accounts) == 0 {
		return s, nil
	}
	if err := s.Save(); err != nil {
		return nil, err
	}
	s.migrated = true
	return s, nil
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

// Migrated 表示本次加载发生了旧格式自动迁移。
func (s *Store) Migrated() bool { return s.migrated }

// Path 返回凭证库文件路径。
func (s *Store) Path() string { return s.path }
