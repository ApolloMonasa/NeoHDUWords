package updatecheck

import (
	"os/exec"
	"strings"
	"testing"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{
		"-C", dir,
		"-c", "user.email=test@example.com",
		"-c", "user.name=test",
	}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func isHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return len(s) > 0
}

func TestLocalHead_ReadsWorktreeSHA(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-b", "main")
	gitRun(t, dir, "commit", "--allow-empty", "-m", "init")

	sha, branch, err := localHead(dir)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "main" {
		t.Fatalf("expected branch main, got %q", branch)
	}
	if len(sha) != 40 || !isHex(strings.ToLower(sha)) {
		t.Fatalf("expected 40-hex sha, got %q", sha)
	}
}

func TestLocalHead_NotARepo(t *testing.T) {
	if _, _, err := localHead(t.TempDir()); err == nil {
		t.Fatal("expected error outside a git repo")
	}
}
