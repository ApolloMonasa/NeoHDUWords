package tokenpool

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUpsert_Dedupe(t *testing.T) {
	s := &Store{Version: 2}

	if added, err := s.Upsert("tok-a"); err != nil || !added {
		t.Fatalf("expected add, got added=%v err=%v", added, err)
	}
	if added, err := s.Upsert("tok-a"); err != nil || added {
		t.Fatalf("expected dedupe, got added=%v err=%v", added, err)
	}
	if added, err := s.Upsert("tok-b"); err != nil || !added {
		t.Fatalf("expected add, got added=%v err=%v", added, err)
	}
	if len(s.Tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(s.Tokens))
	}
	// 新增不标记 primary
	if s.PrimaryToken() != "" {
		t.Fatalf("plain upsert must not set primary, got %q", s.PrimaryToken())
	}
}

func TestLoginPrimary_Semantics(t *testing.T) {
	s := &Store{Version: 2}

	// 空库 → 追加并标记主账号
	if action, err := s.LoginPrimary("tok-a"); err != nil || action != LoginAdded {
		t.Fatalf("empty store: action=%q err=%v", action, err)
	}
	if s.PrimaryToken() != "tok-a" || len(s.Tokens) != 1 {
		t.Fatalf("state: primary=%q tokens=%d", s.PrimaryToken(), len(s.Tokens))
	}

	// 会话轮换后重新登录 → 原地替换主账号 token，不新增记录
	if action, err := s.LoginPrimary("tok-a-new"); err != nil || action != LoginReplacedPrimary {
		t.Fatalf("replace: action=%q err=%v", action, err)
	}
	if s.PrimaryToken() != "tok-a-new" || len(s.Tokens) != 1 {
		t.Fatalf("replace state: primary=%q tokens=%d", s.PrimaryToken(), len(s.Tokens))
	}

	// 库里另一 token 登录 → 主账号标记移过去
	if _, err := s.Upsert("tok-b"); err != nil {
		t.Fatal(err)
	}
	if action, err := s.LoginPrimary("tok-b"); err != nil || action != LoginSwitched {
		t.Fatalf("switch: action=%q err=%v", action, err)
	}
	if s.PrimaryToken() != "tok-b" || len(s.Tokens) != 2 {
		t.Fatalf("switch state: primary=%q tokens=%d", s.PrimaryToken(), len(s.Tokens))
	}
	// 主账号标记唯一
	primaries := 0
	for _, a := range s.Tokens {
		if a.Primary {
			primaries++
		}
	}
	if primaries != 1 {
		t.Fatalf("expected exactly one primary, got %d", primaries)
	}
}

func TestRemoveToken_SequenceAndPrimary(t *testing.T) {
	s := &Store{Version: 2}
	_, _ = s.LoginPrimary("tok-a")
	_, _ = s.Upsert("tok-b")

	// 删除普通成员
	if err := s.RemoveToken("tok-b"); err != nil {
		t.Fatal(err)
	}
	if len(s.Tokens) != 1 || s.PrimaryToken() != "tok-a" {
		t.Fatalf("after member removal: %+v", s)
	}

	// 删除后继续新增（v2 无编号，天然不会撞名）
	if added, err := s.Upsert("tok-c"); err != nil || !added {
		t.Fatalf("re-add after removal: added=%v err=%v", added, err)
	}

	// 删除主账号 → 主账号标记消失（exam 应报不存在主账户）
	if err := s.RemoveToken("tok-a"); err != nil {
		t.Fatal(err)
	}
	if s.PrimaryToken() != "" {
		t.Fatalf("primary should be gone, got %q", s.PrimaryToken())
	}

	// 幂等：删除不存在的 token 不报错
	if err := s.RemoveToken("missing"); err != nil {
		t.Fatalf("remove unknown should be idempotent, got %v", err)
	}
}

func TestRemoveTokenPersistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	s := &Store{Version: 2, path: path}
	_, _ = s.LoginPrimary("tok-a")
	_, _ = s.Upsert("tok-b")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	// 独立加载后删除（模拟运行中另一处清理）
	if err := RemoveTokenPersistent(path, "tok-a"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.PrimaryToken() != "" || len(reloaded.Tokens) != 1 || reloaded.Tokens[0].Token != "tok-b" {
		t.Fatalf("unexpected reload: %+v", reloaded.Tokens)
	}

	// 不存在的 token 幂等
	if err := RemoveTokenPersistent(path, "nope"); err != nil {
		t.Fatal(err)
	}
}

func TestSaveAndReload_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	s := &Store{Version: 2, path: path}
	_, _ = s.LoginPrimary("tok-a")
	_, _ = s.Upsert("tok-b")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != 2 || loaded.PrimaryToken() != "tok-a" || len(loaded.AllTokens()) != 2 {
		t.Fatalf("unexpected reload: %+v", loaded)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows 不表示 POSIX 权限位（0600 落地为 0666，访问控制由 ACL 负责），
	// 仅在 Unix 上校验文件权限。
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 perms, got %v", fi.Mode().Perm())
	}
}

func TestLoadStore_UpgradesV1InPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	// v1 格式：带别名，primary 指向 acct-1
	v1 := `{
  "version": 1,
  "primary": "acct-1",
  "accounts": [
    {"alias": "acct-1", "token": "token-aaa", "added_at": "2026-09-10T00:00:00Z"},
    {"alias": "acct-2", "token": "token-bbb", "added_at": "2026-09-10T00:00:00Z"}
  ]
}`
	if err := os.WriteFile(path, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Version != 2 || len(s.Tokens) != 2 {
		t.Fatalf("unexpected upgrade result: %+v", s)
	}
	if s.PrimaryToken() != "token-aaa" {
		t.Fatalf("expected token-aaa primary after upgrade, got %q", s.PrimaryToken())
	}

	// 原位写回后是纯 v2，再次加载不再走升级分支
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "alias") {
		t.Fatalf("v1 alias should be dropped after upgrade:\n%s", b)
	}
	reloaded, err := LoadStore(path)
	if err != nil || reloaded.PrimaryToken() != "token-aaa" {
		t.Fatalf("reload after upgrade: err=%v primary=%q", err, reloaded.PrimaryToken())
	}
}

func TestLoadStore_MissingAndCorrupt(t *testing.T) {
	// 不存在 → 空库
	s, err := LoadStore(filepath.Join(t.TempDir(), "accounts.json"))
	if err != nil || len(s.Tokens) != 0 || s.PrimaryToken() != "" {
		t.Fatalf("expected empty store, got %+v err=%v", s, err)
	}

	// 损坏 → 带自愈提示的错误
	path := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = LoadStore(path)
	if err == nil || !strings.Contains(err.Error(), "重新 login") {
		t.Fatalf("expected self-heal hint error, got %v", err)
	}
}

func TestPrintAccounts(t *testing.T) {
	s := &Store{Version: 2}
	_, _ = s.LoginPrimary("abcdefghijklmnopqrstuv")
	_, _ = s.Upsert("tok-b")

	var b strings.Builder
	s.PrintAccounts(&b, false)
	out := b.String()
	if !strings.Contains(out, "(primary)") || !strings.Contains(out, "abcdef...qrstuv") || !strings.Contains(out, "member") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestFormat_Masked(t *testing.T) {
	tok := "abcdefghijklmnopqrstuv"
	if got := Format(tok, false); !strings.Contains(got, "...") {
		t.Fatalf("expected masked token, got %q", got)
	}
	if Format(tok, true) != tok {
		t.Fatal("expected plain token when plain is true")
	}
}

func TestSetPrimary_OnlyAmongExisting(t *testing.T) {
	s := &Store{Version: 2}
	_, _ = s.LoginPrimary("tok-a")
	_, _ = s.Upsert("tok-b")

	if err := s.SetPrimary("tok-b"); err != nil {
		t.Fatal(err)
	}
	if s.PrimaryToken() != "tok-b" {
		t.Fatalf("expected tok-b primary, got %q", s.PrimaryToken())
	}
	// 唯一性
	primaries := 0
	for _, a := range s.Tokens {
		if a.Primary {
			primaries++
		}
	}
	if primaries != 1 {
		t.Fatalf("expected one primary, got %d", primaries)
	}
	// 不存在的 token 报错（不允许凭空替换）
	if err := s.SetPrimary("missing"); err == nil {
		t.Fatal("expected error for unknown token")
	}
}
