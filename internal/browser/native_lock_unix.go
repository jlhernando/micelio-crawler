//go:build !windows

package browser

import (
	"os"
	"syscall"
)

func tryNativeProfileLock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
func unlockNativeProfile(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
