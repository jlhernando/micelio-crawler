package server

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/micelio/micelio/internal/crawler"
	"github.com/micelio/micelio/internal/integration"
	"github.com/micelio/micelio/internal/netguard"
	"github.com/micelio/micelio/internal/types"
)

// Core Web Vitals (Google, needs a CrUX or PageSpeed key from Settings) and
// the CDN probe run when a report is exported, so any crawl gets them. The
// answers are kept for an hour so the PDF and the CSV agree and repeated
// exports do not spend API quota.

const webCheckTTL = time.Hour

type webCheck struct {
	at  time.Time
	cwv *types.CWVReport
	cdn *types.CDNReport
}

var webCache = struct {
	sync.Mutex
	m        map[string]webCheck
	inflight map[string]*webCall // one lookup per crawl at a time
}{m: map[string]webCheck{}, inflight: map[string]*webCall{}}

// webCall is a lookup in progress; concurrent exports of the same crawl
// wait for it instead of spending API quota again.
type webCall struct {
	done chan struct{}
	cwv  *types.CWVReport
	cdn  *types.CDNReport
}

// fetchWebChecks is swapped in tests.
var fetchWebChecks = func(ctx context.Context, client *http.Client, cruxKey, psiKey, seedURL string) (*types.CWVReport, *types.CDNReport) {
	var cwv *types.CWVReport
	var cdn *types.CDNReport
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); cwv = integration.FetchCWV(ctx, client, cruxKey, psiKey, seedURL) }()
	go func() { defer wg.Done(); cdn = crawler.ProbeCDN(ctx, client, seedURL) }()
	wg.Wait()
	return cwv, cdn
}

func webChecks(ctx context.Context, crawlID, seedURL string, store *UiStore, manager *CrawlManager) (*types.CWVReport, *types.CDNReport) {
	webCache.Lock()
	if c, ok := webCache.m[crawlID]; ok && time.Since(c.at) < webCheckTTL {
		webCache.Unlock()
		return c.cwv, c.cdn
	}
	if call, ok := webCache.inflight[crawlID]; ok {
		webCache.Unlock()
		select {
		case <-call.done:
			return call.cwv, call.cdn
		case <-ctx.Done():
			return nil, nil
		}
	}
	call := &webCall{done: make(chan struct{})}
	webCache.inflight[crawlID] = call
	webCache.Unlock()
	defer func() {
		webCache.Lock()
		delete(webCache.inflight, crawlID)
		webCache.Unlock()
		close(call.done)
	}()
	// The lookup is shared: one caller hanging up must not cut it short
	// for the others waiting on it. The timeout below still bounds it.
	ctx = context.WithoutCancel(ctx)

	var cruxKey, psiKey string
	if settings, err := store.GetSettings(); err == nil {
		applyEnvFallbacks(settings)
		cruxKey, _ = settings["cruxKey"].(string)
		psiKey, _ = settings["psiKey"].(string)
	}
	client := netguard.HTTPClient(60 * time.Second)
	if manager.SkipSSRF {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	ctx, cancel := context.WithTimeout(ctx, 75*time.Second)
	defer cancel()
	cwv, cdn := fetchWebChecks(ctx, client, strings.TrimSpace(cruxKey), strings.TrimSpace(psiKey), seedURL)
	call.cwv, call.cdn = cwv, cdn
	if ctx.Err() != nil {
		return cwv, cdn // cut short: do not keep a partial answer
	}
	webCache.Lock()
	webCache.m[crawlID] = webCheck{at: time.Now(), cwv: cwv, cdn: cdn}
	webCache.Unlock()
	return cwv, cdn
}
