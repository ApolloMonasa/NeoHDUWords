package browser

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestParseBrowserType_Valid(t *testing.T) {
	tests := []struct {
		input    string
		expected BrowserType
	}{
		{"chrome", Chrome},
		{"Chrome", Chrome},
		{"CHROME", Chrome},
		{"  chrome  ", Chrome},
		{"edge", Edge},
		{"Edge", Edge},
		{"EDGE", Edge},
	}
	for _, tc := range tests {
		got, err := ParseBrowserType(tc.input)
		if err != nil {
			t.Errorf("ParseBrowserType(%q) unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.expected {
			t.Errorf("ParseBrowserType(%q) = %v, want %v", tc.input, got, tc.expected)
		}
	}
}

func TestParseBrowserType_Invalid(t *testing.T) {
	_, err := ParseBrowserType("opera")
	if err == nil {
		t.Error("expected error for invalid browser type")
	}
}

func TestBrowserType_String(t *testing.T) {
	tests := []struct {
		bt   BrowserType
		want string
	}{
		{Chrome, "Chrome"},
		{Edge, "Edge"},
		{BrowserType(99), "Unknown"},
	}
	for _, tc := range tests {
		got := tc.bt.String()
		if got != tc.want {
			t.Errorf("BrowserType(%d).String() = %q, want %q", tc.bt, got, tc.want)
		}
	}
}

func TestResolve_InvalidType(t *testing.T) {
	_, _, err := Resolve("opera")
	if err == nil {
		t.Error("expected error for invalid browser type")
	}
	if !strings.Contains(err.Error(), "unknown browser type") {
		t.Errorf("expected 'unknown browser type' error, got: %v", err)
	}
}

func TestResolve_AutoDetect(t *testing.T) {
	bt, path, err := Resolve("")
	// Auto-detect should find at least one browser on a typical desktop.
	if err != nil {
		t.Skipf("no browser found on this system: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty path")
	}
	if bt != Chrome && bt != Edge {
		t.Errorf("unexpected browser type: %v", bt)
	}
}

func TestResolve_SpecifiedNotFound(t *testing.T) {
	// Test that specifying a browser that isn't installed returns an error.
	path := findBrowser(Edge)
	if path != "" {
		t.Skip("Edge found on system, skipping not-found test")
	}
	_, _, err := Resolve("edge")
	if err == nil {
		t.Error("expected error when Edge is specified but not installed")
	}
}

func TestPathExists(t *testing.T) {
	if pathExists("") {
		t.Error("empty path should return false")
	}
	root := "/"
	if runtime.GOOS == "windows" {
		root = os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\`
		}
	}
	if !pathExists(root) {
		t.Errorf("%q should exist", root)
	}
}

func TestIsFullPath(t *testing.T) {
	fullPath := "/usr/bin/chromium"
	if runtime.GOOS == "windows" {
		fullPath = `C:\Program Files\chromium.exe`
	}
	if !isFullPath(fullPath) {
		t.Errorf("%q should be a full path", fullPath)
	}
	if isFullPath("chromium") {
		t.Error("'chromium' should not be a full path")
	}
}

func TestExtractTokenFromURL(t *testing.T) {
	// Valid token URL
	tok := extractTokenFromURL("https://skl.hdu.edu.cn/?type=6&token=abc123#/english/list")
	if tok != "abc123" {
		t.Errorf("expected 'abc123', got %q", tok)
	}

	// No token parameter
	tok = extractTokenFromURL("https://skl.hdu.edu.cn/api/something")
	if tok != "" {
		t.Errorf("expected empty, got %q", tok)
	}

	// Not skl domain
	tok = extractTokenFromURL("https://other.com/?token=xyz")
	if tok != "" {
		t.Errorf("expected empty for non-skl domain, got %q", tok)
	}

	// Empty URL
	tok = extractTokenFromURL("")
	if tok != "" {
		t.Errorf("expected empty for empty URL, got %q", tok)
	}
}
