package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "micelio.env")
	os.WriteFile(path, []byte(`# local secrets
MICELIO_TELEGRAM_TOKEN="123:abc"
export MICELIO_TELEGRAM_CHAT=42
MICELIO_EMAIL_TO = 'me@example.com'
HOME=/nope
MICELIO_ALREADY=file
`), 0o600)
	t.Setenv("MICELIO_ALREADY", "env")
	for _, k := range []string{"MICELIO_TELEGRAM_TOKEN", "MICELIO_TELEGRAM_CHAT", "MICELIO_EMAIL_TO"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	home := os.Getenv("HOME")

	set, warning, err := Load(path)
	if err != nil || warning != "" {
		t.Fatalf("err=%v warning=%q", err, warning)
	}
	if len(set) != 3 {
		t.Errorf("set = %v", set)
	}
	for k, want := range map[string]string{
		"MICELIO_TELEGRAM_TOKEN": "123:abc", "MICELIO_TELEGRAM_CHAT": "42",
		"MICELIO_EMAIL_TO": "me@example.com", "MICELIO_ALREADY": "env", "HOME": home,
	} {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	os.Chmod(path, 0o644)
	if _, warning, _ := Load(path); warning == "" {
		t.Error("a world-readable file should warn")
	}
	if set, _, err := Load(filepath.Join(dir, "missing.env")); err != nil || set != nil {
		t.Errorf("missing file: %v %v", set, err)
	}
}
