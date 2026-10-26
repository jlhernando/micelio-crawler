package crawler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"

	"github.com/micelio/micelio/internal/extract"
	"github.com/micelio/micelio/internal/types"
)

// Soft404ProbeMetaKey is the crawl DB metadata key holding the probes, so
// `micelio import` can use them too.
const Soft404ProbeMetaKey = "soft404Probes"

// setupSoft404Probe requests one URL on the seed host that cannot exist. A
// real 404 means the site reports missing pages correctly; a 200 means it
// serves soft 404s, and the page's fingerprint lets analysis find crawled
// pages that look the same. One request per crawl, robots.txt respected.
func (s *crawlSession) setupSoft404Probe(ctx context.Context) {
	if s.config.NoSoft404Probe || s.config.HeadOnly {
		return
	}
	seed, err := url.Parse(s.seedURL)
	if err != nil || seed.Host == "" {
		return
	}
	token := make([]byte, 6)
	rand.Read(token)
	probeURL := seed.Scheme + "://" + seed.Host + "/micelio-404-check-" + hex.EncodeToString(token)
	probe := types.Soft404Probe{Host: seed.Host, URL: probeURL}

	if s.robots != nil && !s.robots.IsAllowed(ctx, probeURL) {
		probe.Error = "blocked by robots.txt"
	} else {
		if s.hostRL != nil {
			s.hostRL.Wait(ctx, probeURL) // counts against the host's delay like any request
		}
		res := FetchPage(ctx, probeURL, s.fetchOpts)
		probe.StatusCode, probe.FinalURL, probe.Error = res.StatusCode, res.FinalURL, res.Error
		if res.StatusCode == 200 && res.HTML != "" {
			page := extract.ExtractPageData(res.HTML, extract.ExtractionOptions{
				PageURL: probeURL, FinalURL: res.FinalURL, StatusCode: res.StatusCode, Headers: res.Headers,
			})
			if page.Title != nil {
				probe.Title = page.Title.Text
			}
			probe.WordCount, probe.DOMFingerprint = page.WordCount, page.DOMFingerprint
		}
	}
	switch {
	case probe.Error != "":
		fmt.Fprintf(os.Stderr, "  [soft404] check skipped: %s\n", probe.Error)
	case probe.StatusCode == 200 && probe.FinalURL != "" && probe.FinalURL != probeURL:
		fmt.Fprintf(os.Stderr, "  [soft404] %s redirects a page that does not exist to %s (soft 404s likely)\n", seed.Host, probe.FinalURL)
	case probe.StatusCode == 200:
		fmt.Fprintf(os.Stderr, "  [soft404] %s answers 200 for a page that does not exist (soft 404s likely)\n", seed.Host)
	default:
		fmt.Fprintf(os.Stderr, "  [soft404] %s answers %d for a page that does not exist\n", seed.Host, probe.StatusCode)
	}
	s.soft404Probes = append(s.soft404Probes, probe)
	if saver, ok := s.crawlDB.(interface{ SaveMeta(key, value string) error }); ok {
		if data, err := json.Marshal(s.soft404Probes); err == nil {
			saver.SaveMeta(Soft404ProbeMetaKey, string(data))
		}
	}
}
