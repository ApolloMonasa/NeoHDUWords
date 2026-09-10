// Package tokenpool 管理本地凭证文件：.token（主账号）与 .tokens（凭证池）。
package tokenpool

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

const (
	// DefaultMainFile 主账号凭证文件，exam 默认使用。
	DefaultMainFile = ".token"
	// DefaultPoolFile 凭证池文件，collect 可并发使用池内全部账号。
	DefaultPoolFile = ".tokens"
)

// SaveMain 把主账号凭证写入 path（权限 0600）。
func SaveMain(path, token string) error {
	return os.WriteFile(path, []byte(token), 0o600)
}

// LoadMain 读取并去除首尾空白后返回主账号凭证；文件不存在或读取失败时返回错误。
func LoadMain(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// Pool 是凭证池内容：Tokens 为去重后的全部凭证，Primary 为带 * 标记的主账号。
type Pool struct {
	Primary string
	Tokens  []string
}

// Load 解析凭证池文件；文件不存在时返回空池。
// 每行一个 token，* 开头的行是 primary，# 开头与空行忽略，重复行去重。
func Load(path string) (Pool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Pool{}, nil
		}
		return Pool{}, err
	}
	defer f.Close()

	out := Pool{}
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		isPrimary := strings.HasPrefix(line, "*")
		if isPrimary {
			line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		}
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			if isPrimary && out.Primary == "" {
				out.Primary = line
			}
			continue
		}
		seen[line] = struct{}{}
		out.Tokens = append(out.Tokens, line)
		if isPrimary && out.Primary == "" {
			out.Primary = line
		}
	}
	if err := scanner.Err(); err != nil {
		return Pool{}, err
	}
	if out.Primary != "" && !Contains(out.Tokens, out.Primary) {
		out.Tokens = append(out.Tokens, out.Primary)
	}
	return out, nil
}

// Save 重写凭证池文件（权限 0600），primary 行以 * 开头置顶。
func Save(path string, p Pool) error {
	var b strings.Builder
	b.WriteString("# token pool; prefix '*' means primary token\n")
	if p.Primary != "" {
		b.WriteString("*" + p.Primary + "\n")
	}
	for _, tk := range p.Tokens {
		if tk == "" || tk == p.Primary {
			continue
		}
		b.WriteString(tk + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// Append 把 token 追加进凭证池；已存在时不重复写入。返回是否实际新增。
func Append(path, token string) (bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return false, fmt.Errorf("empty token")
	}
	pool, err := Load(path)
	if err != nil {
		return false, err
	}
	if Contains(pool.Tokens, token) {
		return false, nil
	}
	pool.Tokens = append(pool.Tokens, token)
	return true, Save(path, pool)
}

// SetPrimary 把 token 标记为池中的主账号；token 不在池中时先追加。
func SetPrimary(path, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("empty token")
	}
	p, err := Load(path)
	if err != nil {
		return err
	}
	if !Contains(p.Tokens, token) {
		p.Tokens = append(p.Tokens, token)
	}
	p.Primary = token
	return Save(path, p)
}

// Contains 判断 tokens 是否包含 token。
func Contains(tokens []string, token string) bool {
	for _, t := range tokens {
		if t == token {
			return true
		}
	}
	return false
}

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
