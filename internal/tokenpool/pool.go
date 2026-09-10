package tokenpool

import (
	"bufio"
	"os"
	"strings"
)

// 旧版凭证文件（v2 起仅作为自动迁移的数据源读取，不再写入）。
const (
	// DefaultMainFile 旧版主账号凭证文件。
	DefaultMainFile = ".token"
	// DefaultPoolFile 旧版凭证池文件（* 前缀行为 primary）。
	DefaultPoolFile = ".tokens"
)

// LoadMain 读取旧版 .token 单凭证文件。
func LoadMain(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// legacyPool 是旧版 .tokens 文本池的解析结果。
type legacyPool struct {
	Primary string
	Tokens  []string
}

// loadLegacyPool 解析旧版 .tokens 文本池：每行一个 token，
// * 开头的行是 primary，# 开头与空行忽略，重复行去重；文件不存在返回空池。
func loadLegacyPool(path string) (legacyPool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return legacyPool{}, nil
		}
		return legacyPool{}, err
	}
	defer f.Close()

	out := legacyPool{}
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
		return legacyPool{}, err
	}
	if out.Primary != "" {
		for _, tk := range out.Tokens {
			if tk == out.Primary {
				return out, nil
			}
		}
		out.Tokens = append(out.Tokens, out.Primary)
	}
	return out, nil
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
