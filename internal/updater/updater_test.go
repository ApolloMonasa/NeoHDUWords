package updater

import (
	"bufio"
	"strings"
	"testing"
)

func newTestReader(input string) *bufio.Reader {
	return bufio.NewReader(strings.NewReader(input))
}

func TestPromptYesNo(t *testing.T) {
	r := newTestReader("\ny\nn\n")
	if !PromptYesNo(r, "q", true) {
		t.Fatal("empty input should return default true")
	}
	if !PromptYesNo(r, "q", false) {
		t.Fatal("y should be true")
	}
	if PromptYesNo(r, "q", true) {
		t.Fatal("n should be false")
	}
}

func TestVersionString_DevBuildEmpty(t *testing.T) {
	if VersionString() != "" {
		t.Fatalf("dev build (no ldflags) should yield empty version string, got %q", VersionString())
	}
}
