// Package updater checks GitHub releases for newer Micelio binaries and
// installs them in place. It is invoked from the web UI via the /api/update
// endpoints.
package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// CacheTTL is how long a successful release-metadata fetch is reused before
// hitting the GitHub API again.
const CacheTTL = 24 * time.Hour

// Status is the JSON-serialisable view returned to the UI.
type Status struct {
	Current         string    `json:"current"`
	Latest          string    `json:"latest,omitempty"`
	UpdateAvailable bool      `json:"updateAvailable"`
	IsDevBuild      bool      `json:"isDevBuild"`
	Downloadable    bool      `json:"downloadable"`
	AssetName       string    `json:"assetName,omitempty"`
	ReleaseURL      string    `json:"releaseUrl,omitempty"`
	ReleaseName     string    `json:"releaseName,omitempty"`
	PublishedAt     time.Time `json:"publishedAt,omitempty"`
	LastCheckedAt   time.Time `json:"lastCheckedAt,omitempty"`
	Repo            string    `json:"repo"`
	Platform        string    `json:"platform"`
	// Notes captures non-fatal reasons for the current state, e.g. "no asset
	// for this platform" or "GitHub API unreachable". Useful in the UI.
	Notes string `json:"notes,omitempty"`
}

// Updater coordinates release lookup and binary installation.
type Updater struct {
	repo       string
	current    string // version with leading "v" stripped
	binaryPath string
	stateDir   string
	httpClient *http.Client

	mu         sync.Mutex
	cached     *Status
	cachedAsset string // download URL parallel to cached.Latest, not exposed
	installing bool
}

// New constructs an Updater. repo is "owner/name", current is the binary's
// embedded version (e.g. "1.2.3" or "v1.2.3-5-gabc-dirty"), binaryPath is
// the file to replace on install, and stateDir is where the cache lives.
func New(repo, current, binaryPath, stateDir string) *Updater {
	return &Updater{
		repo:       repo,
		current:    strings.TrimPrefix(current, "v"),
		binaryPath: binaryPath,
		stateDir:   stateDir,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// Status returns the current update status. If force is false and a recent
// (<CacheTTL) successful check exists, the cached result is returned without
// hitting the network.
func (u *Updater) Status(ctx context.Context, force bool) (*Status, error) {
	u.mu.Lock()
	if !force && u.cached != nil && time.Since(u.cached.LastCheckedAt) < CacheTTL {
		s := *u.cached
		u.mu.Unlock()
		return &s, nil
	}
	u.mu.Unlock()

	st := u.baseStatus()

	if isDev(u.current) {
		st.IsDevBuild = true
		st.Notes = "running a development build — auto-update disabled"
		st.LastCheckedAt = time.Now()
		u.mu.Lock()
		u.cached = &st
		u.cachedAsset = ""
		u.mu.Unlock()
		s := st
		return &s, nil
	}

	rel, err := u.fetchLatestRelease(ctx)
	if err != nil {
		st.Notes = fmt.Sprintf("could not reach GitHub: %v", err)
		st.LastCheckedAt = time.Now()
		// Don't cache on failure — retry next call.
		s := st
		return &s, err
	}

	latest := strings.TrimPrefix(rel.TagName, "v")
	st.Latest = latest
	st.ReleaseURL = rel.HTMLURL
	st.ReleaseName = rel.Name
	st.PublishedAt = rel.PublishedAt
	st.LastCheckedAt = time.Now()

	asset := matchAsset(rel.Assets)
	if asset == nil {
		st.Notes = "no compatible asset for " + st.Platform + " in latest release"
	} else {
		st.AssetName = asset.Name
		st.Downloadable = true
	}

	if latest != "" && latest != u.current {
		st.UpdateAvailable = true
	}

	u.mu.Lock()
	u.cached = &st
	if asset != nil {
		u.cachedAsset = asset.BrowserDownloadURL
	} else {
		u.cachedAsset = ""
	}
	u.mu.Unlock()

	s := st
	return &s, nil
}

// Install downloads the latest release asset and atomically replaces the
// running binary. Returns the resulting Status (with UpdateAvailable=false on
// success). The caller is responsible for telling the user to restart.
func (u *Updater) Install(ctx context.Context) (*Status, error) {
	u.mu.Lock()
	if u.installing {
		u.mu.Unlock()
		return nil, errors.New("update already in progress")
	}
	u.installing = true
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.installing = false
		u.mu.Unlock()
	}()

	// Always re-check before installing — don't trust stale cache.
	st, err := u.Status(ctx, true)
	if err != nil {
		return nil, err
	}
	if st.IsDevBuild {
		return nil, errors.New("cannot auto-update a development build")
	}
	if !st.UpdateAvailable {
		return st, nil
	}
	if !st.Downloadable {
		return nil, fmt.Errorf("no downloadable asset for %s in release %s", st.Platform, st.Latest)
	}

	u.mu.Lock()
	url := u.cachedAsset
	u.mu.Unlock()
	if url == "" {
		return nil, errors.New("internal error: no cached asset URL")
	}

	if err := u.downloadAndSwap(ctx, url); err != nil {
		return nil, err
	}

	// Update succeeded — refresh status so the UI reflects the new state.
	u.mu.Lock()
	if u.cached != nil {
		updated := *u.cached
		updated.Current = st.Latest
		updated.UpdateAvailable = false
		u.current = strings.TrimPrefix(st.Latest, "v")
		u.cached = &updated
	}
	out := *u.cached
	u.mu.Unlock()
	return &out, nil
}

func (u *Updater) baseStatus() Status {
	return Status{
		Current:  u.current,
		Repo:     u.repo,
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
}

func (u *Updater) downloadAndSwap(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/octet-stream")
	dlClient := &http.Client{Timeout: 5 * time.Minute}
	resp, err := dlClient.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	dir := filepath.Dir(u.binaryPath)
	tmp, err := os.CreateTemp(dir, ".micelio-update-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		cleanup()
		return fmt.Errorf("chmod: %w", err)
	}
	// Atomic rename within same dir. On macOS replacing a running binary is
	// safe — the running process keeps its inode, the new file takes effect
	// on next exec.
	if err := os.Rename(tmpPath, u.binaryPath); err != nil {
		cleanup()
		return fmt.Errorf("replace binary: %w", err)
	}
	return nil
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

func (u *Updater) fetchLatestRelease(ctx context.Context) (*ghRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", u.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "micelio-updater")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("no releases published yet")
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("github API: HTTP %d", resp.StatusCode)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	return &rel, nil
}

// matchAsset finds the release asset that matches the running OS/arch using
// the naming scheme produced by `make release`:
//
//	micelio-{os}-{arch}        (linux, darwin)
//	micelio-{os}-{arch}.exe    (windows)
func matchAsset(assets []ghAsset) *ghAsset {
	want := assetName(runtime.GOOS, runtime.GOARCH)
	for i := range assets {
		if assets[i].Name == want {
			return &assets[i]
		}
	}
	return nil
}

func assetName(goos, goarch string) string {
	name := fmt.Sprintf("micelio-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// devVersionPattern matches non-release versions like "dev", "ac6a19e",
// "ac6a19e-dirty", or "v1.2.3-5-gabc1234". Anything that's not a clean
// semver-ish "X.Y.Z" string is treated as a development build and excluded
// from auto-update prompts.
var devVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[.-][A-Za-z0-9.]+)?$`)

func isDev(v string) bool {
	if v == "" || v == "dev" {
		return true
	}
	if strings.Contains(v, "-dirty") || strings.Contains(v, "-g") {
		return true
	}
	return !devVersionPattern.MatchString(v)
}
