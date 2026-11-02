package types

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestResolveDevice(t *testing.T) {
	if ResolveDevice(nil) != nil {
		t.Fatal("nil device must stay nil")
	}
	m := ResolveDevice(&DeviceProfile{ID: "Mobile"})
	if m.ID != "mobile" || !m.Mobile || !m.Touch || m.Kind != DeviceMobile || m.UserAgent == "" || m.Scale != 2.625 {
		t.Errorf("mobile = %+v", m)
	}
	if w, h := m.Viewport(); w != 412 || h != 915 {
		t.Errorf("mobile viewport = %dx%d", w, h)
	}
	ml := ResolveDevice(&DeviceProfile{ID: "mobile", Landscape: true})
	if w, h := ml.Viewport(); w != 915 || h != 412 {
		t.Errorf("landscape viewport = %dx%d", w, h)
	}
	if got := ml.Summary(); got != "Mobile 915×412 landscape" {
		t.Errorf("summary = %q", got)
	}
	// Desktops ignore Landscape.
	d := ResolveDevice(&DeviceProfile{ID: "desktop", Landscape: true})
	if w, h := d.Viewport(); w != 1920 || h != 1080 {
		t.Errorf("desktop viewport = %dx%d", w, h)
	}
	// Fields the config sets win over the built-in profile.
	c := ResolveDevice(&DeviceProfile{ID: "mobile", Width: 360, Height: 800, UserAgent: "X"})
	if c.Width != 360 || c.UserAgent != "X" || !c.Mobile {
		t.Errorf("override = %+v", c)
	}
	// Custom without a size gets the default desktop screen, clamped values.
	u := ResolveDevice(&DeviceProfile{Width: 50, Height: 99999, Scale: 9})
	if u.ID != "custom" || u.Width != 200 || u.Height != 7680 || u.Scale != 4 || u.Kind != DeviceDesktop {
		t.Errorf("custom = %+v", u)
	}
}

func TestDeviceProfilesAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range DeviceProfiles() {
		if seen[d.ID] || d.Label == "" || d.UserAgent == "" || d.Width == 0 || d.Height == 0 || d.Scale == 0 {
			t.Errorf("bad profile %+v", d)
		}
		seen[d.ID] = true
		if (d.Kind == DeviceDesktop) == d.Mobile {
			t.Errorf("%s: kind %s but mobile=%v", d.ID, d.Kind, d.Mobile)
		}
	}
}

// The dashboard keeps its own copy of the profiles (dashboard/src/lib/devices.ts)
// so the picker shows them without a request. They must match.
func TestDashboardDevicesMatch(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "dashboard", "src", "lib", "devices.ts"))
	if err != nil {
		t.Skip("dashboard source not present:", err)
	}
	ts := strings.ReplaceAll(string(b), "${CHROME}", chromeVersion)
	for _, d := range DeviceProfiles() {
		want := fmt.Sprintf("{ id: '%s', label: '%s', kind: '%s', width: %d, height: %d, scale: %s,",
			d.ID, d.Label, d.Kind, d.Width, d.Height, strconv.FormatFloat(d.Scale, 'f', -1, 64))
		if !strings.Contains(ts, want) {
			t.Errorf("devices.ts lacks %s", want)
		}
		if !strings.Contains(ts, d.UserAgent) {
			t.Errorf("devices.ts has another User-Agent for %s", d.ID)
		}
	}
	if n := strings.Count(ts, "{ id: '"); n != len(DeviceProfiles()) {
		t.Errorf("devices.ts has %d profiles, Go has %d", n, len(DeviceProfiles()))
	}
}
