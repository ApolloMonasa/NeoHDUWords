package updatecheck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shaTestServer 起一个托管 asset 文件与 SHA256SUMS 的测试服务器。
func shaTestServer(t *testing.T, files map[string]string, sums map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for name, content := range files {
		mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, content)
		})
	}
	mux.HandleFunc("/"+SHA256SUMSName, func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		for name, sum := range sums {
			fmt.Fprintf(&b, "%s  %s\n", sum, name)
		}
		fmt.Fprint(w, b.String())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func fileSHA256(t *testing.T, content string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func TestVerifyAssetChecksum_Match(t *testing.T) {
	content := "binary-bytes"
	srv := shaTestServer(t,
		map[string]string{"tui-linux-amd64": content},
		map[string]string{"tui-linux-amd64": fileSHA256(t, content)},
	)

	local := filepath.Join(t.TempDir(), "tui-linux-amd64")
	if err := os.WriteFile(local, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	rel := Release{Assets: []ReleaseAsset{
		{Name: "tui-linux-amd64", URL: srv.URL + "/tui-linux-amd64"},
		{Name: SHA256SUMSName, URL: srv.URL + "/" + SHA256SUMSName},
	}}
	if err := VerifyAssetChecksum(context.Background(), rel, "tui-linux-amd64", local); err != nil {
		t.Fatalf("expected match, got %v", err)
	}
}

func TestVerifyAssetChecksum_Mismatch(t *testing.T) {
	srv := shaTestServer(t,
		map[string]string{"tui-linux-amd64": "actual-bytes"},
		map[string]string{"tui-linux-amd64": fileSHA256(t, "declared-bytes")},
	)

	local := filepath.Join(t.TempDir(), "tui-linux-amd64")
	if err := os.WriteFile(local, []byte("actual-bytes"), 0o755); err != nil {
		t.Fatal(err)
	}

	rel := Release{Assets: []ReleaseAsset{
		{Name: "tui-linux-amd64", URL: srv.URL + "/tui-linux-amd64"},
		{Name: SHA256SUMSName, URL: srv.URL + "/" + SHA256SUMSName},
	}}
	err := VerifyAssetChecksum(context.Background(), rel, "tui-linux-amd64", local)
	if err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("expected mismatch error, got %v", err)
	}
}

func TestVerifyAssetChecksum_NoSumsAsset(t *testing.T) {
	rel := Release{Assets: []ReleaseAsset{{Name: "tui-linux-amd64"}}}
	err := VerifyAssetChecksum(context.Background(), rel, "tui-linux-amd64", "whatever")
	if !errors.Is(err, ErrNoSumsAsset) {
		t.Fatalf("expected ErrNoSumsAsset, got %v", err)
	}
}

func TestVerifyAssetChecksum_AssetNotListed(t *testing.T) {
	srv := shaTestServer(t,
		map[string]string{"other-asset": "x"},
		map[string]string{"other-asset": fileSHA256(t, "x")},
	)

	rel := Release{Assets: []ReleaseAsset{
		{Name: "tui-linux-amd64", URL: srv.URL + "/tui-linux-amd64"},
		{Name: SHA256SUMSName, URL: srv.URL + "/" + SHA256SUMSName},
	}}
	err := VerifyAssetChecksum(context.Background(), rel, "tui-linux-amd64", "whatever")
	if !errors.Is(err, ErrAssetNotInSums) {
		t.Fatalf("expected ErrAssetNotInSums, got %v", err)
	}
}
