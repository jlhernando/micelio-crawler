package browser

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// NativeProfileDirectory returns only a server-owned, per-origin profile.
// No API or CLI option accepts arbitrary browser profile paths.
func NativeProfileDirectory(site string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return nativeProfileAt(filepath.Join(home, ".micelio", "native-chrome"), site)
}

func nativeProfileAt(base, site string) (string, error) {
	u, err := url.Parse(site)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return "", fmt.Errorf("invalid native profile origin")
	}
	origin := strings.ToLower(u.Scheme + "://" + u.Host)
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(origin)))
	profile := filepath.Join(base, key)
	for _, dir := range []string{base, profile} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", err
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("native profile directory must not be a symlink")
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return "", err
		}
	}
	return profile, nil
}

func lockNativeProfile(path string) (func(), error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := tryNativeProfileLock(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("native Chrome profile is in use by another crawl")
	}
	return func() { unlockNativeProfile(f); f.Close() }, nil
}
