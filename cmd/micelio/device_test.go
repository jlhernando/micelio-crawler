package main

import (
	"strings"
	"testing"
)

func TestDeviceFlags(t *testing.T) {
	// No device flag: the old behaviour (Micelio/1.0, no device).
	got := mustRun(t, crawlCmd(), "https://example.com", "--no-db")
	if got.config.Device != nil || got.config.UserAgent != "Micelio/1.0" {
		t.Errorf("default: device %+v, UA %q", got.config.Device, got.config.UserAgent)
	}

	// --device: the device's User-Agent is used (UserAgent left empty for it).
	got = mustRun(t, crawlCmd(), "https://example.com", "--no-db", "--device", "googlebot-smartphone")
	d := got.config.Device
	if d == nil || d.ID != "googlebot-smartphone" || !d.Mobile || !strings.Contains(d.UserAgent, "Googlebot") || got.config.UserAgent != "" {
		t.Errorf("--device: device %+v, UA %q", d, got.config.UserAgent)
	}

	// --user-agent typed wins over the device's.
	got = mustRun(t, listCmd(), urlListFile(t, "https://example.com/"), "--no-db", "--device", "mobile", "--user-agent", "Custom/1")
	if got.config.UserAgent != "Custom/1" || got.config.Device.ID != "mobile" {
		t.Errorf("--user-agent: UA %q device %+v", got.config.UserAgent, got.config.Device)
	}

	// Custom screen.
	got = mustRun(t, crawlCmd(), "https://example.com", "--no-db", "--viewport", "360x780", "--dpr", "3", "--mobile")
	d = got.config.Device
	if d == nil || d.ID != "custom" || d.Width != 360 || d.Height != 780 || d.Scale != 3 || !d.Mobile || !d.Touch {
		t.Errorf("custom: %+v", d)
	}

	// Landscape tablet.
	got = mustRun(t, crawlCmd(), "https://example.com", "--no-db", "--device", "tablet", "--landscape")
	if w, h := got.config.Device.Viewport(); w != 1180 || h != 820 {
		t.Errorf("landscape tablet = %dx%d", w, h)
	}

	for _, bad := range [][]string{{"--device", "toaster"}, {"--viewport", "wide"}, {"--viewport", "0x10"}} {
		if _, err := runCaptured(t, crawlCmd(), append([]string{"https://example.com", "--no-db"}, bad...)...); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
