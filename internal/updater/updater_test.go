package updater

import "testing"

func TestVersionString_DevBuildEmpty(t *testing.T) {
	if VersionString() != "" {
		t.Fatalf("dev build (no ldflags) should yield empty version string, got %q", VersionString())
	}
}
