package types

import (
	"strconv"
	"strings"
)

// DeviceProfile is the device a crawl pretends to be: the User-Agent and
// client hints every request sends, and the screen headless Chrome emulates
// when JS rendering is on. Width and Height are CSS pixels in portrait for
// phones and tablets; Landscape swaps them.
type DeviceProfile struct {
	ID        string  `json:"id"`
	Label     string  `json:"label,omitempty"`
	Kind      string  `json:"kind,omitempty"` // desktop, mobile or tablet
	Width     int     `json:"width,omitempty"`
	Height    int     `json:"height,omitempty"`
	Scale     float64 `json:"scale,omitempty"` // device pixel ratio
	Mobile    bool    `json:"mobile,omitempty"`
	Touch     bool    `json:"touch,omitempty"`
	Landscape bool    `json:"landscape,omitempty"`
	UserAgent string  `json:"userAgent,omitempty"`
}

// Device kinds.
const (
	DeviceDesktop = "desktop"
	DeviceMobile  = "mobile"
	DeviceTablet  = "tablet"
)

// DefaultViewport is the screen Chrome renders when no device is chosen.
const (
	DefaultViewportWidth  = 1920
	DefaultViewportHeight = 1080
)

const chromeVersion = "136.0.0.0"

var deviceProfiles = []DeviceProfile{
	{ID: "desktop", Label: "Desktop", Kind: DeviceDesktop, Width: 1920, Height: 1080, Scale: 1,
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + chromeVersion + " Safari/537.36"},
	{ID: "laptop", Label: "Laptop", Kind: DeviceDesktop, Width: 1366, Height: 768, Scale: 1,
		UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + chromeVersion + " Safari/537.36"},
	{ID: "tablet", Label: "Tablet", Kind: DeviceTablet, Width: 820, Height: 1180, Scale: 2, Mobile: true, Touch: true,
		UserAgent: "Mozilla/5.0 (iPad; CPU OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"},
	{ID: "mobile", Label: "Mobile", Kind: DeviceMobile, Width: 412, Height: 915, Scale: 2.625, Mobile: true, Touch: true,
		UserAgent: "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + chromeVersion + " Mobile Safari/537.36"},
	{ID: "iphone", Label: "iPhone", Kind: DeviceMobile, Width: 390, Height: 844, Scale: 3, Mobile: true, Touch: true,
		UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"},
	{ID: "googlebot-smartphone", Label: "Googlebot Smartphone", Kind: DeviceMobile, Width: 412, Height: 732, Scale: 2.625, Mobile: true, Touch: true,
		UserAgent: "Mozilla/5.0 (Linux; Android 6.0.1; Nexus 5X Build/MMB29P) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + chromeVersion + " Mobile Safari/537.36 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"},
	{ID: "googlebot-desktop", Label: "Googlebot Desktop", Kind: DeviceDesktop, Width: 1024, Height: 768, Scale: 1,
		UserAgent: "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Googlebot/2.1; +http://www.google.com/bot.html) Chrome/" + chromeVersion + " Safari/537.36"},
	{ID: "bingbot", Label: "Bingbot", Kind: DeviceDesktop, Width: 1366, Height: 768, Scale: 1,
		UserAgent: "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm) Chrome/" + chromeVersion + " Safari/537.36"},
}

// DeviceProfiles returns the built-in device profiles.
func DeviceProfiles() []DeviceProfile {
	return append([]DeviceProfile(nil), deviceProfiles...)
}

// LookupDevice returns the built-in profile with this id.
func LookupDevice(id string) (DeviceProfile, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, d := range deviceProfiles {
		if d.ID == id {
			return d, true
		}
	}
	return DeviceProfile{}, false
}

// ResolveDevice fills a profile from its built-in defaults: a config may
// carry only {"id":"mobile"} or {"id":"mobile","landscape":true}. Fields it
// sets win. Unknown ids without a size get a desktop screen. Nil stays nil.
func ResolveDevice(d *DeviceProfile) *DeviceProfile {
	if d == nil {
		return nil
	}
	out := *d
	if base, ok := LookupDevice(d.ID); ok {
		out.ID = base.ID
		if out.Label == "" {
			out.Label = base.Label
		}
		if out.Kind == "" {
			out.Kind = base.Kind
		}
		if out.Width <= 0 || out.Height <= 0 {
			out.Width, out.Height = base.Width, base.Height
		}
		if out.Scale <= 0 {
			out.Scale = base.Scale
		}
		if !d.Mobile && !d.Touch && d.Kind == "" {
			out.Mobile, out.Touch = base.Mobile, base.Touch
		}
		if out.UserAgent == "" {
			out.UserAgent = base.UserAgent
		}
	}
	if out.ID == "" {
		out.ID = "custom"
	}
	if out.Kind == "" {
		out.Kind = DeviceDesktop
		if out.Mobile {
			out.Kind = DeviceMobile
		}
	}
	if out.Width <= 0 || out.Height <= 0 {
		out.Width, out.Height = DefaultViewportWidth, DefaultViewportHeight
	}
	out.Width, out.Height = min(max(out.Width, 200), 7680), min(max(out.Height, 200), 7680)
	if out.Scale <= 0 {
		out.Scale = 1
	}
	out.Scale = min(out.Scale, 4)
	return &out
}

// Viewport is the screen to emulate. Phones and tablets are portrait
// unless Landscape is set; desktops keep their size.
func (d *DeviceProfile) Viewport() (width, height int) {
	if d == nil {
		return DefaultViewportWidth, DefaultViewportHeight
	}
	w, h := d.Width, d.Height
	if d.Kind == DeviceDesktop {
		return w, h
	}
	if d.Landscape {
		return max(w, h), min(w, h)
	}
	return min(w, h), max(w, h)
}

// Summary is a short label like "Mobile 412×915" or "Tablet 1180×820 landscape".
func (d *DeviceProfile) Summary() string {
	if d == nil {
		return ""
	}
	w, h := d.Viewport()
	s := d.Label
	if s == "" {
		s = d.ID
	}
	s += " " + strconv.Itoa(w) + "×" + strconv.Itoa(h)
	if d.Landscape && d.Kind != DeviceDesktop {
		s += " landscape"
	}
	return s
}
