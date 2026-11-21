package types

import "fmt"

// PrepareNativeChrome validates the combinations this transport supports.
// Limits hold for CLI, HTTP, saved crawls and schedules alike.
func (c *CrawlConfig) PrepareNativeChrome() error {
	if !c.NativeChrome {
		return nil
	}
	var unsupported string
	switch {
	case (c.Mode != "" && c.Mode != ModeSpider) || len(c.URLs) > 0 || len(c.SitemapURLs) > 0:
		unsupported = "only Spider mode is supported"
	case !c.RespectRobots:
		unsupported = "robots.txt must be respected"
	case c.HeadOnly:
		unsupported = "HEAD-only mode"
	case c.Device != nil || c.CompareDevice != nil:
		unsupported = "device emulation or comparison"
	case c.UserAgent != "" && c.UserAgent != "Micelio/1.0":
		unsupported = "a custom User-Agent"
	case len(c.CustomHeaders) > 0 || c.Cookies != "":
		unsupported = "manual headers or cookies"
	case c.Stealth:
		unsupported = "the stealth HTTP client"
	case len(c.SnippetPaths) > 0:
		unsupported = "custom JavaScript snippets"
	case c.DiscoverSitemaps:
		unsupported = "sitemap discovery"
	case c.CheckExternal:
		unsupported = "external link checks"
	case c.PageWeight || c.FullPageWeight:
		unsupported = "page-weight HEAD requests"
	}
	if unsupported != "" {
		return fmt.Errorf("native Chrome does not support %s; use the standard transport or disable this option", unsupported)
	}
	c.Concurrency = 1
	c.DelayMs = max(c.DelayMs, 1000)
	c.JSRendering, c.RenderBlockRes = false, false
	c.NoSoft404Probe, c.NoAgentCheck = true, true
	return nil
}
