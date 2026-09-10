package updatecheck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Release struct {
	TagName string
	Name    string
	HTMLURL string
	Assets  []ReleaseAsset
}

type ReleaseAsset struct {
	Name string
	URL  string
	Size int64
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

func LatestRelease(ctx context.Context, repo Repo) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repo.Owner, repo.Name), nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "HDU-Words-CLI")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return Release{}, fmt.Errorf("github latest release: http=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var raw ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return Release{}, err
	}
	out := Release{TagName: raw.TagName, Name: raw.Name, HTMLURL: raw.HTMLURL}
	for _, a := range raw.Assets {
		out.Assets = append(out.Assets, ReleaseAsset{Name: a.Name, URL: a.BrowserDownloadURL, Size: a.Size})
	}
	return out, nil
}

// DBAsset 返回默认发布仓库（Data tag）下的题库数据库资产。
func DBAsset() ReleaseAsset {
	return ReleaseAsset{
		Name: "hduwords.db",
		URL:  "https://github.com/ApolloMonasa/NeoHDUWords/releases/download/Data/hduwords.db",
	}
}

// SHA256SUMSName 是发布资产中校验和文件的标准名。
const SHA256SUMSName = "SHA256SUMS"

var (
	// ErrNoSumsAsset 表示该 release 未附带 SHA256SUMS（历史版本），无法校验。
	ErrNoSumsAsset = errors.New("release has no SHA256SUMS asset")
	// ErrAssetNotInSums 表示 SHA256SUMS 中没有该资产的条目。
	ErrAssetNotInSums = errors.New("asset not listed in SHA256SUMS")
)

// FindSHA256SUMS 在 release 资产中查找校验和文件。
func (r Release) FindSHA256SUMS() (ReleaseAsset, bool) {
	for _, a := range r.Assets {
		if strings.EqualFold(a.Name, SHA256SUMSName) {
			return a, true
		}
	}
	return ReleaseAsset{}, false
}

// fetchSHA256SUMS 下载并解析 SHA256SUMS，返回 资产名(小写) -> sha256(hex) 映射。
func fetchSHA256SUMS(ctx context.Context, asset ReleaseAsset) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "HDU-Words-CLI")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download SHA256SUMS: http=%d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	sums := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		sums[strings.ToLower(filepath.Base(fields[1]))] = fields[0]
	}
	return sums, nil
}

// VerifyAssetChecksum 下载 release 附带的 SHA256SUMS，校验 localPath 的
// SHA256 是否与资产 assetName 的记录一致；一致返回 nil。
// release 无 SUMS 文件或条目缺失时分别返回 ErrNoSumsAsset / ErrAssetNotInSums，
// 由调用方决定是否放行。
func VerifyAssetChecksum(ctx context.Context, rel Release, assetName, localPath string) error {
	sumsAsset, ok := rel.FindSHA256SUMS()
	if !ok {
		return ErrNoSumsAsset
	}
	sums, err := fetchSHA256SUMS(ctx, sumsAsset)
	if err != nil {
		return err
	}
	want, ok := sums[strings.ToLower(filepath.Base(assetName))]
	if !ok {
		return ErrAssetNotInSums
	}

	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s: want %s got %s", assetName, want, got)
	}
	return nil
}

func (r Release) AssetForCurrentPlatform(binaryName string) (ReleaseAsset, bool) {
	goos := strings.ToLower(runtime.GOOS)
	goarch := strings.ToLower(runtime.GOARCH)
	needle := fmt.Sprintf("%s-%s-%s", binaryName, goos, goarch)
	needleWithExe := needle + ".exe"
	for _, a := range r.Assets {
		name := strings.ToLower(a.Name)
		base := strings.TrimSuffix(name, filepath.Ext(name))
		if base == strings.ToLower(needle) || name == strings.ToLower(needleWithExe) {
			return a, true
		}
	}
	return ReleaseAsset{}, false
}

func DownloadAsset(ctx context.Context, asset ReleaseAsset, destPath string) (int64, error) {
	if strings.TrimSpace(asset.URL) == "" {
		return 0, errors.New("empty asset url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "HDU-Words-CLI")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return 0, fmt.Errorf("download asset: http=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if err := ensureParentDir(destPath); err != nil {
		return 0, err
	}
	written, err := writeFileFromReader(destPath, resp.Body)
	if err != nil {
		return written, err
	}
	return written, nil
}

func ensureParentDir(destPath string) error {
	parent := filepath.Dir(destPath)
	if parent == "." || parent == "" {
		return nil
	}
	return os.MkdirAll(parent, 0o755)
}

func writeFileFromReader(destPath string, r io.Reader) (int64, error) {
	f, err := os.Create(destPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(f, r)
}
