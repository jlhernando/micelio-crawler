package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// errUIDirBusy means another `micelio ui` already serves this data folder.
var errUIDirBusy = errors.New("ui folder in use")

// lockUIDir takes the dashboard's exclusive lock on uiDir (server.lock) and
// writes owner (pid and address) into it. Two dashboards on one folder would
// mark each other's running crawls as failed at start, delete files and run
// every schedule twice, so the second one must not start. The OS drops the
// lock when the process exits, even after a crash.
func lockUIDir(uiDir, owner string) (release func(), err error) {
	path := filepath.Join(uiDir, "server.lock")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	_ = f.Chmod(0o600) // also for a lock file made by an older version
	if err := tryLockFile(f); err != nil {
		b, _ := os.ReadFile(path)
		f.Close()
		holder := strings.TrimSpace(string(b))
		if holder == "" {
			holder = "unknown process"
		}
		return nil, fmt.Errorf("%w: another micelio ui (%s) already uses %s; stop it first, or start this one with another HOME", errUIDirBusy, holder, uiDir)
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(owner+"\n"), 0)
	// Only the lock holder proves to the CLI that it is the dashboard.
	removeKey, err := writeDashboardKey(uiDir)
	if err != nil {
		_ = f.Truncate(0)
		unlockFile(f)
		f.Close()
		return nil, fmt.Errorf("write dashboard key: %w", err)
	}
	return func() {
		removeKey()
		_ = f.Truncate(0)
		unlockFile(f)
		f.Close()
	}, nil
}
