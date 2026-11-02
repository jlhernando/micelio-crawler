package browser

import (
	"context"
	"strings"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"

	"github.com/micelio/micelio/internal/types"
)

// emulateDevice sets the screen Chrome renders: size, pixel ratio, mobile
// and touch. Without a device it is the old 1920x1080 desktop.
func emulateDevice(d *types.DeviceProfile) chromedp.Action[chromedp.Void] {
	if d == nil {
		return chromedp.EmulateViewport(types.DefaultViewportWidth, types.DefaultViewportHeight)
	}
	w, h := d.Viewport()
	opts := []chromedp.EmulateViewportOption{chromedp.EmulateScale(d.Scale)}
	if d.Mobile {
		opts = append(opts, chromedp.EmulateMobile)
	}
	if d.Touch {
		opts = append(opts, chromedp.EmulateTouch)
	}
	if d.Kind != types.DeviceDesktop {
		if w > h {
			opts = append(opts, chromedp.EmulateLandscape)
		} else {
			opts = append(opts, chromedp.EmulatePortrait)
		}
	}
	return chromedp.EmulateViewport(int64(w), int64(h), opts...)
}

// overrideUserAgent sets the User-Agent and, for Chrome-like agents, the
// client hints (navigator.userAgentData, Sec-CH-UA-Mobile/Platform) so a
// site that reads them sees the same device as the viewport.
func overrideUserAgent(ua string, d *types.DeviceProfile) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		if ua == "" {
			return nil
		}
		_, err := cdp.Call(ctx, t, emulation.SetUserAgentOverride, emulation.SetUserAgentOverrideParams{
			UserAgent: ua, UserAgentMetadata: userAgentMetadata(ua, d),
		})
		return err
	})
}

// userAgentMetadata describes ua as client hints, or nil when ua is not a
// Chrome-based browser (Safari, Firefox and bots send no client hints).
func userAgentMetadata(ua string, d *types.DeviceProfile) *emulation.UserAgentMetadata {
	i := strings.Index(ua, "Chrome/")
	if i < 0 || strings.Contains(ua, "compatible;") {
		return nil
	}
	full := ua[i+len("Chrome/"):]
	if j := strings.IndexAny(full, " ;)"); j >= 0 {
		full = full[:j]
	}
	major := full
	if j := strings.IndexByte(full, '.'); j >= 0 {
		major = full[:j]
	}
	mobile := strings.Contains(ua, " Mobile")
	if d != nil {
		mobile = d.Mobile
	}
	md := &emulation.UserAgentMetadata{
		Brands: []*emulation.UserAgentBrandVersion{
			{Brand: "Chromium", Version: major},
			{Brand: "Google Chrome", Version: major},
			{Brand: "Not.A/Brand", Version: "99"},
		},
		FullVersionList: []*emulation.UserAgentBrandVersion{
			{Brand: "Chromium", Version: full},
			{Brand: "Google Chrome", Version: full},
			{Brand: "Not.A/Brand", Version: "99.0.0.0"},
		},
		Mobile:       mobile,
		Architecture: "x86",
		Bitness:      "64",
	}
	switch {
	case strings.Contains(ua, "Android"):
		md.Platform, md.PlatformVersion, md.Architecture, md.Bitness = "Android", "14.0.0", "", ""
		if k := strings.Index(ua, "; "); k >= 0 {
			if e := strings.Index(ua[k+2:], ")"); e >= 0 {
				parts := strings.Split(ua[k+2:k+2+e], "; ")
				md.Model = strings.TrimSpace(strings.Split(parts[len(parts)-1], " Build/")[0])
			}
		}
	case strings.Contains(ua, "Windows"):
		md.Platform, md.PlatformVersion = "Windows", "15.0.0"
	case strings.Contains(ua, "Macintosh"):
		md.Platform, md.PlatformVersion, md.Architecture = "macOS", "14.5.0", "arm"
	case strings.Contains(ua, "CrOS"):
		md.Platform, md.PlatformVersion = "Chrome OS", "16000.0.0"
	default:
		md.Platform, md.PlatformVersion = "Linux", ""
	}
	if d != nil && d.Kind == types.DeviceTablet {
		md.FormFactors = []string{"Tablet"}
	} else if mobile {
		md.FormFactors = []string{"Mobile"}
	} else {
		md.FormFactors = []string{"Desktop"}
	}
	return md
}
