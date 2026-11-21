package browser

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNativeProfileIsolationAndLock(t *testing.T) {
	base := filepath.Join(t.TempDir(), "profiles")
	a, err := nativeProfileAt(base, "https://example.com/page")
	if err != nil {
		t.Fatal(err)
	}
	same, _ := nativeProfileAt(base, "https://example.com/another")
	other, _ := nativeProfileAt(base, "https://other.example/page")
	if a != same || a == other {
		t.Fatal("native profiles must be shared within an origin and isolated across origins")
	}
	release, err := lockNativeProfile(a)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockNativeProfile(a); err == nil {
		second()
		release()
		t.Fatal("concurrent profile use allowed")
	}
	release()
	release2, err := lockNativeProfile(a)
	if err != nil {
		t.Fatal("profile lock not released", err)
	}
	release2()
	if info, err := os.Stat(a); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0700) {
		t.Fatalf("native profile permissions: %v %v", info, err)
	}
}
