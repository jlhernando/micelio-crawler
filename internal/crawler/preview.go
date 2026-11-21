package crawler

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/micelio/micelio/internal/browser"
	"github.com/micelio/micelio/internal/types"
)

// PreviewHomepage uses the crawl's HTTP client, robots policy and renderer,
// without creating a crawl, discovering links or saving results. Chrome closes
// before the frozen image is returned. SkipSSRF remains a test-only option.
func PreviewHomepage(ctx context.Context, config types.CrawlConfig) (*browser.PreviewFrame, error) {
	u, err := url.Parse(strings.TrimSpace(config.SeedURL))
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("a public http or https URL without credentials is required")
	}
	u.Path, u.RawPath, u.RawQuery, u.Fragment, u.ForceQuery = "/", "", "", "", false
	config.SeedURL = u.String()
	config.Mode = types.ModeSpider
	if err := config.PrepareNativeChrome(); err != nil {
		return nil, err
	}
	s := &crawlSession{config: config, seedURL: config.SeedURL}
	s.applyConfigDefaults()
	if err := s.setupHTTPClient(); err != nil {
		return nil, err
	}
	client := s.fetchOpts.Client
	defer client.CloseIdleConnections()
	s.setupBrowser()
	defer func() {
		if s.renderer != nil {
			s.renderer.Close()
		}
		if s.native != nil {
			s.native.Close()
		}
	}()
	if config.NativeChrome {
		if err := s.setupNative(ctx); err != nil {
			return nil, err
		}
	}
	s.setupRobotsAndDelay(ctx)
	if s.robots != nil && !s.robots.IsAllowed(ctx, s.seedURL) {
		if s.robotsChecker != nil && s.robotsChecker.IsUnavailable() {
			if s.native != nil {
				return nil, fmt.Errorf("native Chrome could not read robots.txt (%s); homepage was not requested", s.native.AuxiliaryError())
			}
			return nil, errors.New("the crawler cannot read robots.txt; homepage was not requested")
		}
		return nil, errors.New("robots.txt disallows this homepage for the selected crawler; homepage was not requested")
	}
	result := s.fetchWithCache(ctx, types.QueueEntry{URL: s.seedURL})
	if result.Error != "" {
		return nil, fmt.Errorf("homepage request failed: %s", result.Error)
	}
	if !strings.Contains(strings.ToLower(result.ContentType), "html") {
		return nil, fmt.Errorf("homepage returned HTTP %d with non-HTML content; no visual preview is available", result.StatusCode)
	}
	var frame *browser.PreviewFrame
	mode := "html"
	if s.native != nil {
		frame, err = s.native.Snapshot(ctx)
		mode = "native"
	} else {
		// The crawler renders HTTP 200 documents only. Error responses remain
		// visible, but their scripts must not imply successful JS rendering.
		javascript := s.config.JSRendering && result.StatusCode == 200
		if javascript {
			mode = "javascript"
		}
		if s.renderer == nil {
			s.renderer = browser.NewRenderer(s.config.Proxy, browser.RenderConfig{
				Timeout: 30 * time.Second, GuardNetwork: !s.config.SkipSSRF, Device: s.config.Device,
				BlockImages: !s.config.DownloadImages, Pace: s.fetchOpts.Pace,
			})
		}
		rendered, renderErr := s.renderer.PreviewPage(ctx, result.FinalURL, s.config.UserAgent,
			&browser.Prefetched{HTML: result.HTML, Headers: result.Headers}, javascript)
		if renderErr != nil {
			return nil, renderErr
		}
		frame = rendered.Preview
	}
	if err != nil {
		return nil, err
	}
	frame.URL, frame.StatusCode, frame.Mode, frame.UserAgent = result.FinalURL, result.StatusCode, mode, s.config.UserAgent
	return frame, nil
}
