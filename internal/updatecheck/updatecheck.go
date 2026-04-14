package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"hduwords/internal/buildinfo"
)

type Repo struct {
	Owner string
	Name  string
}

type Status struct {
	LocalVersion string
	LocalSHA     string
	LocalBranch  string
	RemoteSHA    string
	RemoteBranch string
	RepoURL      string
	Available    bool
}

func ParseRepo(raw string) (Repo, error) {
	raw = strings.TrimSpace(raw)
	parts := strings.Split(raw, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Repo{}, fmt.Errorf("invalid repo %q, want owner/name", raw)
	}
	return Repo{Owner: parts[0], Name: parts[1]}, nil
}

func (r Repo) URL() string {
	return fmt.Sprintf("https://github.com/%s/%s", r.Owner, r.Name)
}

func (r Repo) ZipURL(ref string) string {
	return fmt.Sprintf("https://github.com/%s/%s/archive/%s.zip", r.Owner, r.Name, ref)
}

func Check(ctx context.Context, repo Repo, startDir string) (Status, error) {
	localVersion := strings.TrimSpace(buildinfo.Version)
	localCommit := strings.TrimSpace(buildinfo.Commit)
	if localVersion != "" && localVersion != "dev" {
		remoteRelease, rerr := LatestRelease(ctx, repo)
		if rerr != nil {
			return Status{RepoURL: repo.URL(), LocalVersion: localVersion, LocalSHA: localCommit}, rerr
		}
		available := !strings.EqualFold(localVersion, remoteRelease.TagName)
		return Status{
			LocalVersion: localVersion,
			LocalSHA:     localCommit,
			RemoteSHA:    remoteRelease.TagName,
			RemoteBranch: remoteRelease.Name,
			RepoURL:      repo.URL(),
			Available:    available,
		}, nil
	}

	localSHA, localBranch, err := localHead(startDir)
	if err != nil {
		if localCommit != "" && localCommit != "unknown" {
			localSHA = localCommit
		}
		if localSHA == "" {
			return Status{RepoURL: repo.URL(), LocalVersion: localVersion, LocalSHA: localSHA}, err
		}
	}

	remoteBranch, remoteSHA, err := remoteHead(ctx, repo)
	if err != nil {
		return Status{RepoURL: repo.URL(), LocalVersion: localVersion, LocalSHA: localSHA, LocalBranch: localBranch}, err
	}

	available := true
	if localSHA != "" && remoteSHA != "" && strings.EqualFold(localSHA, remoteSHA) {
		available = false
	}

	return Status{
		LocalVersion: localVersion,
		LocalSHA:     localSHA,
		LocalBranch:  localBranch,
		RemoteSHA:    remoteSHA,
		RemoteBranch: remoteBranch,
		RepoURL:      repo.URL(),
		Available:    available,
	}, nil
}

func DownloadSnapshot(ctx context.Context, repo Repo, ref, destPath string) (int64, error) {
	if strings.TrimSpace(ref) == "" {
		return 0, errors.New("empty ref")
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, repo.ZipURL(ref), nil)
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
		return 0, fmt.Errorf("download snapshot: http=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	f, err := os.Create(destPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	written, err := io.Copy(f, resp.Body)
	if err != nil {
		return written, err
	}
	return written, nil
}

type gitRemoteInfo struct {
	DefaultBranch string `json:"default_branch"`
}

type gitCommitInfo struct {
	SHA string `json:"sha"`
}

func remoteHead(ctx context.Context, repo Repo) (branch string, sha string, err error) {
	infoReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.github.com/repos/%s/%s", repo.Owner, repo.Name), nil)
	if err != nil {
		return "", "", err
	}
	infoReq.Header.Set("Accept", "application/vnd.github+json")
	infoReq.Header.Set("User-Agent", "HDU-Words-CLI")

	resp, err := http.DefaultClient.Do(infoReq)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", "", fmt.Errorf("github repo info: http=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var info gitRemoteInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", "", err
	}
	if info.DefaultBranch == "" {
		return "", "", errors.New("github default branch not found")
	}

	commitReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s", repo.Owner, repo.Name, info.DefaultBranch), nil)
	if err != nil {
		return "", "", err
	}
	commitReq.Header.Set("Accept", "application/vnd.github+json")
	commitReq.Header.Set("User-Agent", "HDU-Words-CLI")

	resp, err = http.DefaultClient.Do(commitReq)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", "", fmt.Errorf("github branch commit: http=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var commit gitCommitInfo
	if err := json.NewDecoder(resp.Body).Decode(&commit); err != nil {
		return "", "", err
	}
	return info.DefaultBranch, commit.SHA, nil
}

func localHead(startDir string) (sha string, branch string, err error) {
	repoRoot, gitDir, err := findGitDir(startDir)
	if err != nil {
		return "", "", err
	}
	_ = repoRoot

	headPath := filepath.Join(gitDir, "HEAD")
	b, err := os.ReadFile(headPath)
	if err != nil {
		return "", "", err
	}
	head := strings.TrimSpace(string(b))
	if strings.HasPrefix(head, "ref:") {
		ref := strings.TrimSpace(strings.TrimPrefix(head, "ref:"))
		branch = filepath.Base(ref)
		return readRef(gitDir, ref)
	}
	return head, branch, nil
}

func findGitDir(startDir string) (string, string, error) {
	cur, err := filepath.Abs(startDir)
	if err != nil {
		return "", "", err
	}
	for {
		candidate := filepath.Join(cur, ".git")
		fi, err := os.Stat(candidate)
		if err == nil {
			if fi.IsDir() {
				return cur, candidate, nil
			}
			b, err := os.ReadFile(candidate)
			if err != nil {
				return "", "", err
			}
			line := strings.TrimSpace(string(b))
			if strings.HasPrefix(line, "gitdir:") {
				gitDir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
				if !filepath.IsAbs(gitDir) {
					gitDir = filepath.Clean(filepath.Join(cur, gitDir))
				}
				return cur, gitDir, nil
			}
			return "", "", fmt.Errorf("invalid .git file in %s", cur)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", "", errors.New("git repository not found")
		}
		cur = parent
	}
}

func readRef(gitDir, ref string) (sha string, branch string, err error) {
	branch = filepath.Base(ref)
	refPath := filepath.Join(gitDir, filepath.FromSlash(ref))
	b, err := os.ReadFile(refPath)
	if err == nil {
		return strings.TrimSpace(string(b)), branch, nil
	}
	if !os.IsNotExist(err) {
		return "", "", err
	}

	packedRefs := filepath.Join(gitDir, "packed-refs")
	b, err = os.ReadFile(packedRefs)
	if err != nil {
		return "", "", err
	}
	needle := " " + ref
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
			continue
		}
		if strings.Contains(line, needle) {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				return fields[0], branch, nil
			}
		}
	}
	return "", "", fmt.Errorf("ref not found: %s", ref)
}
