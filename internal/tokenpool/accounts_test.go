package tokenpool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpsert_AutoAliasAndDedupe(t *testing.T) {
	s := &Store{Version: 1}

	a1, added, err := s.Upsert("tok-a", "", "")
	if err != nil || !added || a1 != "acct-1" {
		t.Fatalf("expected acct-1 added, got alias=%q added=%v err=%v", a1, added, err)
	}
	// 重复 token 不新增
	a2, added, err := s.Upsert("tok-a", "", "")
	if err != nil || added || a2 != "acct-1" {
		t.Fatalf("expected dedupe to acct-1, got alias=%q added=%v err=%v", a2, added, err)
	}
	a3, added, err := s.Upsert("tok-b", "my-alias", "学号123")
	if err != nil || !added || a3 != "my-alias" {
		t.Fatalf("expected custom alias, got alias=%q added=%v err=%v", a3, added, err)
	}
	// alias 冲突报错
	if _, _, err := s.Upsert("tok-c", "my-alias", ""); err == nil {
		t.Fatal("expected alias conflict error")
	}
	if len(s.Accounts) != 2 || s.Accounts[1].Note != "学号123" {
		t.Fatalf("unexpected accounts: %+v", s.Accounts)
	}
}

func TestSetPrimaryByToken_AndPrimaryToken(t *testing.T) {
	s := &Store{Version: 1}
	_, _, _ = s.Upsert("tok-a", "", "")
	_, _, _ = s.Upsert("tok-b", "", "")

	if err := s.SetPrimaryByToken("tok-b"); err != nil {
		t.Fatal(err)
	}
	if s.PrimaryToken() != "tok-b" {
		t.Fatalf("expected tok-b primary, got %q", s.PrimaryToken())
	}
	if err := s.SetPrimaryByToken("nope"); err == nil {
		t.Fatal("expected error for unknown token")
	}

	s.Primary = "ghost"
	if s.PrimaryToken() != "" {
		t.Fatal("missing alias should yield empty primary token")
	}
}

func TestSaveAndReload_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	s := &Store{Version: 1, path: path}
	_, _, _ = s.Upsert("tok-a", "", "")
	_ = s.SetPrimaryByToken("tok-a")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PrimaryToken() != "tok-a" || len(loaded.Tokens()) != 1 {
		t.Fatalf("unexpected reload: %+v", loaded)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 perms, got %v", fi.Mode().Perm())
	}
}

func TestFormat_Masked(t *testing.T) {
	tok := "abcdefghijklmnopqrstuv"
	got := Format(tok, false)
	if !strings.Contains(got, "...") {
		t.Fatalf("expected masked token, got %q", got)
	}
	if Format(tok, true) != tok {
		t.Fatal("expected plain token when plain is true")
	}
}

func TestLoginPrimary_ReplaceSemantics(t *testing.T) {
	s := &Store{Version: 1}
	_, _, _ = s.Upsert("tok-a", "main", "")
	_ = s.SetPrimaryByToken("tok-a")
	_, _, _ = s.Upsert("tok-b", "", "")

	// 1. token 已存在 → 仅切换主账号
	alias, action, err := s.LoginPrimary("tok-b", "")
	if err != nil || alias != "acct-2" || action != LoginSwitched {
		t.Fatalf("switch: alias=%q action=%q err=%v", alias, action, err)
	}
	if s.PrimaryToken() != "tok-b" || len(s.Accounts) != 2 {
		t.Fatalf("switch state: primary=%q accounts=%d", s.Primary, len(s.Accounts))
	}

	// 2. alias 命中 → 原地替换该账号 token
	alias, action, err = s.LoginPrimary("tok-b-new", "acct-2")
	if err != nil || alias != "acct-2" || action != LoginRefreshed {
		t.Fatalf("refresh: alias=%q action=%q err=%v", alias, action, err)
	}
	if s.PrimaryToken() != "tok-b-new" || len(s.Accounts) != 2 {
		t.Fatalf("refresh state: primary=%q accounts=%d", s.Primary, len(s.Accounts))
	}

	// 3. 未指定 alias → 原地替换主账号 token（不新增条目）
	alias, action, err = s.LoginPrimary("tok-b-rotated", "")
	if err != nil || alias != "acct-2" || action != LoginReplacedPrimary {
		t.Fatalf("replace-primary: alias=%q action=%q err=%v", alias, action, err)
	}
	if s.PrimaryToken() != "tok-b-rotated" || len(s.Accounts) != 2 {
		t.Fatalf("replace-primary state: primary=%q accounts=%d", s.Primary, len(s.Accounts))
	}

	// 4. 空库 + 新 token → 追加并设主账号
	empty := &Store{Version: 1}
	alias, action, err = empty.LoginPrimary("tok-x", "")
	if err != nil || alias != "acct-1" || action != LoginAdded {
		t.Fatalf("add: alias=%q action=%q err=%v", alias, action, err)
	}
	if empty.PrimaryToken() != "tok-x" {
		t.Fatalf("add state: primary=%q", empty.Primary)
	}
}

func TestRemoveAccount(t *testing.T) {
	s := &Store{Version: 1}
	_, _, _ = s.Upsert("tok-a", "a", "")
	_, _, _ = s.Upsert("tok-b", "b", "")
	_ = s.SetPrimaryByToken("tok-a")

	name, err := s.RemoveAccount("b")
	if err != nil || name != "b" {
		t.Fatalf("remove member: name=%q err=%v", name, err)
	}
	if len(s.Accounts) != 1 || s.PrimaryToken() != "tok-a" {
		t.Fatalf("state after member removal: %+v", s)
	}

	// 删除主账号 → 清空主账号指向
	name, err = s.RemoveAccount("tok-a")
	if err != nil || name != "a" {
		t.Fatalf("remove primary: name=%q err=%v", name, err)
	}
	if len(s.Accounts) != 0 || s.Primary != "" || s.PrimaryToken() != "" {
		t.Fatalf("state after primary removal: %+v", s)
	}

	if _, err := s.RemoveAccount("missing"); err == nil {
		t.Fatal("expected error for unknown account")
	}
}

func TestPrintAccounts(t *testing.T) {
	s := &Store{Version: 1}
	_, _, _ = s.Upsert("abcdefghijklmnopqrstuv", "main", "学号123")
	_ = s.SetPrimaryByToken("abcdefghijklmnopqrstuv")

	var b strings.Builder
	s.PrintAccounts(&b, false)
	out := b.String()
	if !strings.Contains(out, "(primary)") || !strings.Contains(out, "main") || !strings.Contains(out, "abcdef...qrstuv") || !strings.Contains(out, "学号123") {
		t.Fatalf("unexpected output: %q", out)
	}
}
