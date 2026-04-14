package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
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

func (r Release) AssetForCurrentPlatform(binaryName string) (ReleaseAsset, bool) {
	goos := strings.ToLower(runtime.GOOS)
	goarch := strings.ToLower(runtime.GOARCH)
	needle := fmt.Sprintf("%s-%s-%s", binaryName, goos, goarch)
	needleWithExe := needle + ".exe"
	for _, a := range r.Assets {
		name := strings.ToLower(a.Name)
		base := strings.TrimSuffix(name, path.Ext(name))
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
	parent := path.Dir(destPath)
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
