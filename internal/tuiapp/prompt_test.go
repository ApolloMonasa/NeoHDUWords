package tuiapp

import (
	"bufio"
	"strings"
	"testing"
	"time"
)

func newTestReader(input string) *bufio.Reader {
	return bufio.NewReader(strings.NewReader(input))
}

func TestReadString(t *testing.T) {
	r := newTestReader("\nhello\n")
	if got := readString(r, "提示", "def"); got != "def" {
		t.Fatalf("empty input should use default, got %q", got)
	}
	if got := readString(r, "提示", "def"); got != "hello" {
		t.Fatalf("expected hello, got %q", got)
	}
}

func TestReadInt(t *testing.T) {
	r := newTestReader("\nabc\n42\n")
	if got := readInt(r, "n", 7); got != 7 {
		t.Fatalf("empty input should use default, got %d", got)
	}
	if got := readInt(r, "n", 7); got != 7 {
		t.Fatalf("unparseable input should fall back to default, got %d", got)
	}
	if got := readInt(r, "n", 7); got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}

func TestReadFloat(t *testing.T) {
	r := newTestReader("\nxx\n2.5\n")
	if got := readFloat(r, "f", 2); got != 2 {
		t.Fatalf("empty input should use default, got %g", got)
	}
	if got := readFloat(r, "f", 2); got != 2 {
		t.Fatalf("unparseable input should fall back to default, got %g", got)
	}
	if got := readFloat(r, "f", 2); got != 2.5 {
		t.Fatalf("expected 2.5, got %g", got)
	}
}

func TestReadDuration(t *testing.T) {
	r := newTestReader("\nzz\n5m\n")
	if got := readDuration(r, "d", 15*time.Second); got != 15*time.Second {
		t.Fatalf("empty input should use default, got %v", got)
	}
	if got := readDuration(r, "d", 15*time.Second); got != 15*time.Second {
		t.Fatalf("unparseable input should fall back to default, got %v", got)
	}
	if got := readDuration(r, "d", 15*time.Second); got != 5*time.Minute {
		t.Fatalf("expected 5m, got %v", got)
	}
}
