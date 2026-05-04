// Package updater checks GitHub releases for newer Micelio binaries and
// installs them in place. It is invoked from the web UI via the /api/update
// endpoints.
//
// Based on estevecastells' original implementation with security hardening:
// SHA256 checksum verification, HTTPS-only downloads, rollback support,
// permission preservation, and semver comparison.
package updater

import (
	"bufio"
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
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CacheTTL is how long a successful release-metadata fetch is reused before
// hitting the GitHub API again.
const CacheTTL = 24 * time.Hour

// maxDownloadSize caps binary downloads to prevent disk exhaustion (500 MB).
const maxDownloadSize = 500 << 20

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
	CanRollback     bool      `json:"canRollback"`
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
	httpClient *http.Client   // for metadata/checksum fetches (short timeout)
	dlClient   *http.Client   // for binary downloads (long timeout); nil = default

	mu          sync.Mutex
	cached      *Status
	cachedAsset string // download URL parallel to cached.Latest, not exposed
	installing  bool
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
		s.CanRollback = u.hasBackup()
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

	if latest != "" && semverNewer(latest, u.current) {
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

// Install downloads the latest release asset, verifies its SHA256 checksum,
// backs up the current binary, and atomically replaces it. Returns the
// resulting Status (with UpdateAvailable=false on success). The caller is
// responsible for telling the user to restart.
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
	assetName := ""
	if u.cached != nil {
		assetName = u.cached.AssetName
	}
	u.mu.Unlock()
	if url == "" {
		return nil, errors.New("internal error: no cached asset URL")
	}

	// Validate HTTPS before downloading.
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("refusing non-HTTPS download URL: %s", url)
	}

	if err := u.downloadVerifyAndSwap(ctx, url, assetName); err != nil {
		return nil, err
	}

	// Update succeeded — refresh status so the UI reflects the new state.
	u.mu.Lock()
	if u.cached != nil {
		updated := *u.cached
		updated.Current = st.Latest
		updated.UpdateAvailable = false
		updated.CanRollback = u.hasBackup()
		u.current = strings.TrimPrefix(st.Latest, "v")
		u.cached = &updated
	}
	out := *u.cached
	u.mu.Unlock()
	return &out, nil
}

// Rollback restores the previous binary from the .bak file.
func (u *Updater) Rollback() error {
	bakPath := u.binaryPath + ".bak"
	if _, err := os.Stat(bakPath); err != nil {
		return errors.New("no backup file found — nothing to roll back to")
	}
	if err := os.Rename(bakPath, u.binaryPath); err != nil {
		return fmt.Errorf("rollback failed: %w", err)
	}
	// Reset cached status so next check reflects the restored version.
	u.mu.Lock()
	u.cached = nil
	u.mu.Unlock()
	return nil
}

func (u *Updater) baseStatus() Status {
	return Status{
		Current:     u.current,
		Repo:        u.repo,
		Platform:    runtime.GOOS + "/" + runtime.GOARCH,
		CanRollback: u.hasBackup(),
	}
}

func (u *Updater) hasBackup() bool {
	_, err := os.Stat(u.binaryPath + ".bak")
	return err == nil
}

func (u *Updater) downloadVerifyAndSwap(ctx context.Context, url, assetName string) error {
	dir := filepath.Dir(u.binaryPath)

	// 1. Download binary to temp file.
	tmpPath, err := u.downloadToTemp(ctx, url, dir)
	if err != nil {
		return err
	}
	cleanup := func() { _ = os.Remove(tmpPath) }

	// 2. Verify SHA256 checksum against checksums.txt from the same release.
	if err := u.verifyChecksum(ctx, tmpPath, assetName); err != nil {
		cleanup()
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	// 3. Preserve original file permissions.
	mode := os.FileMode(0o755)
	if info, err := os.Stat(u.binaryPath); err == nil {
		mode = info.Mode()
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		cleanup()
		return fmt.Errorf("chmod: %w", err)
	}

	// 4. Back up current binary.
	bakPath := u.binaryPath + ".bak"
	if _, err := os.Stat(u.binaryPath); err == nil {
		if err := copyFile(u.binaryPath, bakPath); err != nil {
			cleanup()
			return fmt.Errorf("backup current binary: %w", err)
		}
	}

	// 5. Atomic rename within same dir.
	if err := os.Rename(tmpPath, u.binaryPath); err != nil {
		cleanup()
		return fmt.Errorf("replace binary: %w", err)
	}
	return nil
}

func (u *Updater) downloadToTemp(ctx context.Context, url, dir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/octet-stream")
	dlClient := u.dlClient
	if dlClient == nil {
		dlClient = &http.Client{Timeout: 5 * time.Minute}
	}
	resp, err := dlClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp(dir, ".micelio-update-*")
	if err != nil {
		return "", fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()

	// Limit download size to prevent disk exhaustion.
	reader := io.LimitReader(resp.Body, maxDownloadSize+1)
	n, err := io.Copy(tmp, reader)
	if err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("write temp: %w", err)
	}
	if n > maxDownloadSize {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("download exceeds %d MB size limit", maxDownloadSize>>20)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close temp: %w", err)
	}
	return tmpPath, nil
}

// verifyChecksum downloads checksums.txt from the same release and verifies
// the downloaded file's SHA256 matches. Format: "<hex>  <filename>" per line
// (standard shasum -a 256 output).
func (u *Updater) verifyChecksum(ctx context.Context, filePath, assetName string) error {
	checksumsURL := fmt.Sprintf("https://github.com/%s/releases/latest/download/checksums.txt", u.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumsURL, nil)
	if err != nil {
		return err
	}
	resp, err := u.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch checksums.txt: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// No checksums.txt in release — warn but allow (for backwards compat
		// with releases created before checksum generation was added).
		return nil
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("fetch checksums.txt: HTTP %d", resp.StatusCode)
	}

	// Parse checksums.txt for the matching asset.
	expectedHash, err := parseChecksumFor(resp.Body, assetName)
	if err != nil {
		return err
	}

	// Compute SHA256 of the downloaded file.
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash file: %w", err)
	}
	actualHash := hex.EncodeToString(h.Sum(nil))

	if actualHash != expectedHash {
		return fmt.Errorf("SHA256 mismatch: expected %s, got %s", expectedHash, actualHash)
	}
	return nil
}

// parseChecksumFor scans checksums.txt content for a line matching the given
// asset name and returns the hex-encoded SHA256.
func parseChecksumFor(r io.Reader, assetName string) (string, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Format: "<hash>  <filename>" or "<hash> <filename>"
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[len(parts)-1] == assetName {
			hash := parts[0]
			if len(hash) != 64 {
				return "", fmt.Errorf("invalid SHA256 length for %s: %d", assetName, len(hash))
			}
			return strings.ToLower(hash), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read checksums: %w", err)
	}
	return "", fmt.Errorf("no checksum entry found for %s", assetName)
}

// copyFile copies src to dst, creating or truncating dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
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

// semverNewer returns true if a is newer than b using numeric semver comparison.
// Both should be stripped of leading "v". Falls back to string inequality if
// either is not a clean X.Y.Z semver.
func semverNewer(a, b string) bool {
	av, aok := parseSemver(a)
	bv, bok := parseSemver(b)
	if !aok || !bok {
		return a != b
	}
	if av[0] != bv[0] {
		return av[0] > bv[0]
	}
	if av[1] != bv[1] {
		return av[1] > bv[1]
	}
	return av[2] > bv[2]
}

// parseSemver extracts major, minor, patch from "X.Y.Z" or "X.Y.Z-suffix".
func parseSemver(v string) ([3]int, bool) {
	// Strip any suffix after a hyphen (e.g. "1.2.3-rc1" -> "1.2.3").
	if idx := strings.IndexByte(v, '-'); idx >= 0 {
		v = v[:idx]
	}
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}
