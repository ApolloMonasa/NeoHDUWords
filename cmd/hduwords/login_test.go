package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenPool_DedupeAndPrimaryMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".tokens")

	if _, err := appendPoolToken(path, "tok-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := appendPoolToken(path, "tok-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := appendPoolToken(path, "tok-b"); err != nil {
		t.Fatal(err)
	}
	if err := setPrimaryTokenInPool(path, "tok-b"); err != nil {
		t.Fatal(err)
	}

	p, err := loadTokenPool(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Primary != "tok-b" {
		t.Fatalf("expected primary tok-b, got %q", p.Primary)
	}
	if len(p.Tokens) != 2 {
		t.Fatalf("expected 2 unique tokens, got %d", len(p.Tokens))
	}
	if !containsToken(p.Tokens, "tok-a") || !containsToken(p.Tokens, "tok-b") {
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

func TestSetPrimaryTokenInPool_ChangesPrimary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".tokens")

	if _, err := appendPoolToken(path, "tok-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := appendPoolToken(path, "tok-b"); err != nil {
		t.Fatal(err)
	}
	if err := setPrimaryTokenInPool(path, "tok-a"); err != nil {
		t.Fatal(err)
	}
	if err := setPrimaryTokenInPool(path, "tok-b"); err != nil {
		t.Fatal(err)
	}

	p, err := loadTokenPool(path)
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

func TestFormatToken_Masked(t *testing.T) {
	tok := "abcdefghijklmnopqrstuv"
	got := formatToken(tok, false)
	if !strings.Contains(got, "...") {
		t.Fatalf("expected masked token, got %q", got)
	}
	if formatToken(tok, true) != tok {
		t.Fatalf("expected plain token when show-plain is true")
	}
}
