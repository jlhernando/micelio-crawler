// Package envfile loads MICELIO_* settings from a local file outside any git
// repository (default ~/.micelio/micelio.env), so tokens, chat IDs and
// webhook URLs can be kept on the machine without being committed.
package envfile

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultPath is ~/.micelio/micelio.env.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".micelio", "micelio.env")
}

// Load reads KEY=VALUE lines from path and sets the MICELIO_* variables that
// are not already set in the environment (the environment wins). Blank lines
// and # comments are ignored; values may be quoted. A missing file is not an
// error. It returns the keys it set and a warning when the file can be read
// by other users.
func Load(path string) (set []string, warning string, err error) {
	if path == "" {
		return nil, "", nil
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Mode().Perm()&0o077 != 0 {
		warning = fmt.Sprintf("%s can be read by other users; run: chmod 600 %s", path, path)
	}
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		text = strings.TrimPrefix(text, "export ")
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			return set, warning, fmt.Errorf("%s:%d: expected KEY=VALUE", path, line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if !strings.HasPrefix(key, "MICELIO_") {
			continue // only Micelio's own settings
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		os.Setenv(key, value)
		set = append(set, key)
	}
	return set, warning, sc.Err()
}
