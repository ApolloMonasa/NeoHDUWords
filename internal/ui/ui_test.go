package ui

import (
	"bufio"
	"strings"
	"testing"
)

func TestPromptYesNo(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\ny\nn\n"))
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

func TestLog_DoesNotPanic(t *testing.T) {
	Log("INFO", "hello %s", "world")
	Log("OK", "done")
}
