package tokenpool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppend_DedupeAndPrimaryMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".tokens")

	if _, err := Append(path, "tok-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(path, "tok-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(path, "tok-b"); err != nil {
		t.Fatal(err)
	}
	if err := SetPrimary(path, "tok-b"); err != nil {
		t.Fatal(err)
	}

	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Primary != "tok-b" {
		t.Fatalf("expected primary tok-b, got %q", p.Primary)
	}
	if len(p.Tokens) != 2 {
		t.Fatalf("expected 2 unique tokens, got %d", len(p.Tokens))
	}
	if !Contains(p.Tokens, "tok-a") || !Contains(p.Tokens, "tok-b") {
		t.Fatalf("missing expected tokens: %+v", p.Tokens)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 || string(b[:1]) == "" {
		t.Fatal("expected non-empty pool file")
	}
}

func TestSetPrimary_ChangesPrimary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".tokens")

	if _, err := Append(path, "tok-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := Append(path, "tok-b"); err != nil {
		t.Fatal(err)
	}
	if err := SetPrimary(path, "tok-a"); err != nil {
		t.Fatal(err)
	}
	if err := SetPrimary(path, "tok-b"); err != nil {
		t.Fatal(err)
	}

	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Primary != "tok-b" {
		t.Fatalf("expected primary tok-b, got %q", p.Primary)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	lines := strings.Split(content, "\n")
	marked := 0
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "*") {
			marked++
		}
	}
	if marked != 1 {
		t.Fatalf("expected exactly one primary marker, got content=%q", content)
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

func TestLoad_MissingFileReturnsEmptyPool(t *testing.T) {
	p, err := Load(filepath.Join(t.TempDir(), ".tokens"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Primary != "" || len(p.Tokens) != 0 {
		t.Fatalf("expected empty pool, got %+v", p)
	}
}
