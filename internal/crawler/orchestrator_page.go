package crawler

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/browser"
	"github.com/micelio/micelio/internal/extract"
	"github.com/micelio/micelio/internal/types"
)

// processEntry handles a single crawl queue entry. Called as a goroutine.
func (s *crawlSession) processEntry(ctx context.Context, e types.QueueEntry) {
	// Path budget check (before any fetch)
	if exceeded, rule := s.checkPathBudget(e.URL); exceeded {
		s.queue.RecordExclusion("path_budget", rule, e.URL)
		s.addToDeadLetter(e, 0, "path budget exceeded: "+rule)
		return
	}

	// Robots.txt check (per origin: seed, post-redirect host, allowed domains)
	if s.robots != nil && !s.robots.IsAllowed(ctx, e.URL) {
		s.queue.RecordExclusion("robots", "", e.URL)
		s.handleRobotsBlocked(e)
		return
	}

	// Rate limiting: per-host token bucket ensures even spacing across all
	// workers, never below the configured delay / robots Crawl-delay, and is
	// paused for everyone when the server sends 429/503 + Retry-After.
	if s.hostRL != nil {
		s.hostRL.Wait(ctx, e.URL)
	} else if s.delay > 0 {
		sleepCtx(ctx, jitter(s.delay))
	}
	if ctx.Err() != nil {
		// Cancelled while waiting: put the URL back so a resume picks it up.
		s.queue.Requeue(e)
		return
	}

	// Fetch (with conditional headers if cached)
	result := s.fetchWithCache(ctx, e)
	if ctx.Err() != nil && result.StatusCode == 0 {
		// Cancelled mid-request: not a real failure, keep it for resume.
		s.queue.Requeue(e)
		return
	}

	// Record response for adaptive rate limiting (every attempt, including
	// 429/503 and network errors, so the limiter slows down).
	if s.adaptiveRL != nil {
		s.recordAdaptiveResponse(e.URL, result)
	}

	// Same-host redirect: mark the target as seen so it is not fetched a
	// second time when some page links to it directly.
	if result.FinalURL != "" && result.FinalURL != e.URL && result.StatusCode > 0 &&
		extractDomain(result.FinalURL) == extractDomain(e.URL) {
		s.queue.MarkSeen(result.FinalURL)
	}

	// Tor circuit management: count requests, rotate on errors
	if s.torManager != nil {
		s.torManager.RecordRequest()
		if result.StatusCode == 403 || result.StatusCode == 429 {
			s.torManager.RecordError(result.StatusCode)
		}
	}

	// Handle 304 Not Modified
	if result.NotModified {
		s.handle304(e, result)
		return
	}

	// 429/503/network errors: pause the host for everyone and re-queue.
	if s.handleRetryable(e, result) {
		return
	}
	// DLQ: HTTP failures after retry exhaustion. Page is still stored below
	// with its status so it appears in results; DLQ provides the structured
	// failure record.
	if result.StatusCode > 0 && isRetryableResult(result) {
		s.addToDeadLetter(e, result.StatusCode, fmt.Sprintf("HTTP %d after %d retries", result.StatusCode, s.config.RetriesPerURL()))
	}
	// DLQ: network-level failures (no HTTP response). Page is still stored below
	// as an error entry so it appears in results alongside the DLQ record.
	if result.Error != "" && result.StatusCode == 0 {
		s.addToDeadLetter(e, 0, result.Error)
	}

	// Extract page data
	page := s.extractPage(e, result)

	// Content change detection (compare with previous crawl's hash)
	if s.prevContentHashes != nil && page.ContentHash != "" {
		if prevHash, ok := s.prevContentHashes[page.URL]; ok {
			changed := page.ContentHash != prevHash
			page.ContentChanged = &changed
		}
	}

	// Per-page enrichments (returns rendered HTML if JS rendering produced it)
	renderedHTML := s.enrichPage(ctx, e, result, &page)

	// Enqueue discovered links (spider mode)
	s.enqueueLinks(e, &page)

	// Track errors
	if page.Error != "" || page.StatusCode >= 400 {
		s.errorCount.Add(1)
	}

	// Collect post-crawl data to disk accumulators
	s.collectPostCrawlData(e, result, &page)

	// HTML source and/or rendered DOM to store (if configured and DB
	// available). Compressed by storePage and written with the page row.
	var srcHTML, renHTML string
	if s.crawlDB != nil && (s.config.SaveHTML || s.config.SaveRendered) {
		if s.config.SaveHTML {
			srcHTML = result.HTML
		}
		if s.config.SaveRendered {
			renHTML = renderedHTML
		}
	}

	// Release HTML strings early to reduce memory pressure.
	// After this point only PageData (extracted metadata) is needed.
	result.HTML = ""
	renderedHTML = ""

	// Stream internal links to disk before storing (storePage may nil InternalLinks).
	if s.intLinksDisk != nil && len(page.InternalLinks) > 0 {
		s.pagesMu.Lock()
		s.intLinksDisk.AddPage(e.URL, page.InternalLinks)
		s.pagesMu.Unlock()
	}

	// Store result and report progress
	s.storePage(e, &page, srcHTML, renHTML)
	srcHTML, renHTML = "", ""

	// Release InternalLinks from the in-memory page to save ~5KB per page.
	// Links are already streamed to disk (above) and written to JSONL/DB (storePage).
	if s.intLinksDisk != nil {
		page.InternalLinkCount = len(page.InternalLinks)
		page.InternalLinks = nil
	}
}

// resourceSizer returns a page-weight sizer that HEAD-fetches resources with
// the crawl client and waits on the per-host rate limiter. Cookies and custom
// headers are only sent to the crawled site's own hosts, never to third-party
// CDNs.
func (s *crawlSession) resourceSizer() analysis.ResourceSizer {
	opts := s.fetchOpts
	opts.NoRetry = true
	opts.Referrer = ""
	opts.IfNoneMatch = ""
	opts.IfModifiedSince = ""
	thirdParty := opts
	thirdParty.Cookies = ""
	thirdParty.CustomHeaders = nil

	internal := map[string]bool{}
	if s.queue != nil {
		internal[strings.ToLower(s.queue.SeedDomain())] = true
	}
	for _, d := range s.config.AllowedDomains {
		internal[strings.ToLower(d)] = true
	}
	hostRL := s.hostRL

	return func(ctx context.Context, u string) (int64, bool) {
		if hostRL != nil {
			hostRL.Wait(ctx, u)
		}
		if ctx.Err() != nil {
			return 0, false
		}
		o := thirdParty
		if p, err := url.Parse(u); err == nil && internal[strings.ToLower(p.Hostname())] {
			o = opts
		}
		r := FetchHead(ctx, u, o)
		if r.StatusCode == 429 || r.StatusCode == 503 {
			if hostRL != nil {
				pause, ok := RetryAfterDuration(r.Headers["retry-after"], MaxServerRetryAfter)
				if !ok {
					pause = retryBackoff(0)
				}
				hostRL.Pause(u, pause)
			}
			return 0, false
		}
		if r.Error != "" || r.StatusCode >= 400 || r.ContentLength == nil {
			return 0, false
		}
		return *r.ContentLength, true
	}
}

// retryBackoff is the host pause used when a 429/503/network error carries
// no Retry-After header: 1s, 2s, 4s... by attempt.
func retryBackoff(attempt int) time.Duration {
	return backoffDelay(attempt)
}

// isRetryableResult reports whether a fetch failed in a way worth trying
// again: network errors and timeouts, 408, 429 and any 5xx. Other 4xx
// (404, 410, 403...) are real answers and are not retried.
func isRetryableResult(result types.FetchResult) bool {
	status := result.StatusCode
	if status == 0 {
		return result.Error != "" && result.Retryable
	}
	return status == 408 || status == 429 || status >= 500
}

// handleRetryable deals with failures isRetryableResult accepts. It pauses
// the host's shared bucket (Retry-After, capped, or exponential backoff) so
// every worker waits, then re-queues the URL while it has retries left
// (config.RetriesPerURL). Returns true when the entry was re-queued and
// must not be stored yet.
func (s *crawlSession) handleRetryable(e types.QueueEntry, result types.FetchResult) bool {
	if !isRetryableResult(result) {
		return false
	}
	status := result.StatusCode
	retries := s.config.RetriesPerURL()

	pause, hasRetryAfter := RetryAfterDuration(result.Headers["retry-after"], MaxServerRetryAfter)
	if !hasRetryAfter {
		pause = retryBackoff(e.Requeues)
	}
	if s.hostRL != nil {
		s.hostRL.Pause(e.URL, pause)
	}

	if e.Requeues >= retries {
		return false
	}
	e.Requeues++
	s.queue.Requeue(e)
	label := strconv.Itoa(status)
	if status == 0 {
		label = "network error"
	}
	fmt.Fprintf(os.Stderr, "\n  [%s] host paused %v, re-queued %s (attempt %d/%d)\n", label, pause.Round(time.Millisecond), e.URL, e.Requeues+1, retries+1)
	return true
}

func (s *crawlSession) handleRobotsBlocked(e types.QueueEntry) {
	if !s.config.ShowBlockedInternal {
		return
	}
	page := types.PageData{
		URL:           e.URL,
		FinalURL:      e.URL,
		Depth:         e.Depth,
		RobotsBlocked: true,
		CrawledAt:     time.Now().UTC(),
	}
	s.pagesMu.Lock()
	if !s.canReloadFromDisk {
		s.pages = append(s.pages, &page)
	}
	s.pagesMu.Unlock()
	s.emitPage(&page, "", "")
}

// emitPage hands a page to the output sink (JSONL/CSV file and crawl DB).
// Encoding and compression run here, in the calling worker; out must not be
// modified afterwards.
func (s *crawlSession) emitPage(out *types.PageData, sourceHTML, renderedHTML string) {
	if s.sink == nil {
		return
	}
	s.sink.put(s.sink.encode(out, sourceHTML, renderedHTML))
}

func (s *crawlSession) fetchWithCache(ctx context.Context, e types.QueueEntry) types.FetchResult {
	reqOpts := s.fetchOpts
	if e.Referrer != nil {
		reqOpts.Referrer = *e.Referrer
	}
	if s.cacheHeaders != nil {
		if ch, ok := s.cacheHeaders[e.URL]; ok {
			reqOpts.IfNoneMatch = ch.ETag
			reqOpts.IfModifiedSince = ch.LastModified
		}
	}
	return FetchPage(ctx, e.URL, reqOpts)
}

func (s *crawlSession) recordAdaptiveResponse(entryURL string, result types.FetchResult) {
	rlRemaining := 0
	rlReset := 0
	if result.Headers != nil {
		if v, err := strconv.Atoi(result.Headers["x-ratelimit-remaining"]); err == nil {
			rlRemaining = v
		}
		if v, err := strconv.Atoi(result.Headers["x-ratelimit-reset"]); err == nil {
			rlReset = v
		}
	}
	status := result.StatusCode
	if status == 0 && !result.Retryable {
		return // SSRF block / cancelled: says nothing about server load
	}
	s.adaptiveRL.RecordResponse(entryURL, status, result.ResponseTimeMs, rlRemaining, rlReset)
	// Sync the host's token bucket with the updated per-domain delay
	// (hostRL clamps it to the configured delay / Crawl-delay floor).
	if s.hostRL != nil {
		s.hostRL.SetInterval(entryURL, s.adaptiveRL.CurrentDelay(entryURL))
	}
}

func (s *crawlSession) handle304(e types.QueueEntry, result types.FetchResult) {
	page := types.PageData{
		URL:            e.URL,
		FinalURL:       result.FinalURL,
		StatusCode:     304,
		RedirectChain:  result.RedirectChain,
		ResponseTimeMs: result.ResponseTimeMs,
		Depth:          e.Depth,
		NotModified:    true,
		CrawledAt:      time.Now().UTC(),
	}
	n := s.crawled.Add(1)
	s.pagesMu.Lock()
	if !s.canReloadFromDisk {
		s.pages = append(s.pages, &page)
	}
	s.pagesMu.Unlock()
	out := page // the sink reads its own copy
	s.emitPage(&out, "", "")
	if s.onProgress != nil {
		elapsed := time.Since(s.rateStart).Seconds()
		rate := 0.0
		if elapsed > 0 {
			rate = float64(n) / elapsed
		}
		excTotal, excStats := s.queue.ExcludedSnapshot()
		s.onProgress(CrawlProgress{
			Crawled:        int(n),
			Queued:         s.queue.Size(),
			TotalSeen:      s.queue.TotalSeen(),
			Coverage:       s.queue.CoverageSnapshot(),
			Excluded:       excTotal,
			ExcludedStats:  excStats,
			CurrentURL:     e.URL,
			StatusCode:     304,
			ResponseTimeMs: result.ResponseTimeMs,
			Rate:           rate,
		})
	}
}

func (s *crawlSession) extractPage(e types.QueueEntry, result types.FetchResult) types.PageData {
	var page types.PageData
	if result.HTML != "" && result.Error == "" {
		page = extract.ExtractPageData(result.HTML, extract.ExtractionOptions{
			PageURL:           e.URL,
			FinalURL:          result.FinalURL,
			StatusCode:        result.StatusCode,
			Headers:           result.Headers,
			Depth:             e.Depth,
			CustomExtractions: s.config.CustomExtractions,
			CustomSearches:    s.config.CustomSearches,
		})
	} else {
		page = types.PageData{
			URL:            e.URL,
			FinalURL:       result.FinalURL,
			StatusCode:     result.StatusCode,
			RedirectChain:  result.RedirectChain,
			ResponseTimeMs: result.ResponseTimeMs,
			Depth:          e.Depth,
			Error:          result.Error,
		}
	}
	page.RedirectChain = result.RedirectChain
	page.ResponseTimeMs = result.ResponseTimeMs
	page.BodySize = result.BodySize
	page.CrawledAt = time.Now().UTC()

	// Capture caching headers for conditional re-crawl
	if result.Headers != nil {
		if etag := result.Headers["etag"]; etag != "" {
			page.ETag = etag
		}
		if lm := result.Headers["last-modified"]; lm != "" {
			page.LastModified = lm
		}
	}
	return page
}

func (s *crawlSession) enrichPage(ctx context.Context, e types.QueueEntry, result types.FetchResult, page *types.PageData) string {
	var renderedHTML string

	// JS rendering
	if s.renderer != nil && result.HTML != "" && result.Error == "" && page.StatusCode == 200 {
		s.browserSem <- struct{}{}
		renderURL := e.URL
		if result.FinalURL != "" {
			renderURL = result.FinalURL
		}
		renderResult, renderErr := s.renderer.RenderPage(ctx, renderURL, s.config.UserAgent)
		<-s.browserSem
		if renderErr != nil {
			fmt.Fprintf(os.Stderr, "\n  [warn] render failed for %s: %v\n", e.URL, renderErr)
		} else if renderResult != nil && renderResult.HTML != "" {
			renderedHTML = renderResult.HTML
			diffs, diffErr := browser.CompareRender(result.HTML, renderResult.HTML)
			if diffErr == nil {
				page.JSRendered = true
				if len(diffs) > 0 {
					page.RenderDiffs = diffs
				}
			}
			if len(renderResult.JSErrors) > 0 {
				page.JSErrors = renderResult.JSErrors
			}
		}
	}

	// PageSpeed Insights
	if s.psiClient != nil && page.StatusCode == 200 {
		<-s.psiLimiter.C
		psiURL := e.URL
		if result.FinalURL != "" {
			psiURL = result.FinalURL
		}
		psiData, psiErr := s.psiClient.Fetch(ctx, psiURL)
		if psiErr != nil {
			fmt.Fprintf(os.Stderr, "\n  [warn] PSI failed for %s: %v\n", e.URL, psiErr)
		} else {
			page.Pagespeed = psiData
		}
	}

	// AI analysis
	if s.aiCfg != nil && page.StatusCode == 200 {
		<-s.aiLimiter.C
		title := ""
		if page.Title != nil {
			title = page.Title.Text
		}
		desc := ""
		if page.MetaDescription != nil {
			desc = page.MetaDescription.Text
		}
		h1 := ""
		if len(page.Headings.H1) > 0 {
			h1 = page.Headings.H1[0]
		}
		aiResult, aiErr := analysis.AnalyzeWithAI(ctx, *s.aiCfg, analysis.PageContext{
			URL:             e.URL,
			Title:           title,
			MetaDescription: desc,
			H1:              h1,
			BodyText:        page.BodyText,
		})
		if aiErr != nil {
			fmt.Fprintf(os.Stderr, "\n  [warn] AI analysis failed for %s: %v\n", e.URL, aiErr)
		} else {
			page.AIAnalysis = &aiResult
		}
	}

	// URL structure analysis
	if page.StatusCode > 0 && page.StatusCode < 400 {
		finalURL := page.URL
		if page.FinalURL != "" {
			finalURL = page.FinalURL
		}
		urlStruct := analysis.AnalyzeURLStructure(finalURL)
		page.URLStructure = &urlStruct
		page.TemplateType = analysis.DetectTemplateType(page, s.seedURL)
	}

	// JS snippet execution
	if s.renderer != nil && len(s.config.SnippetPaths) > 0 && page.StatusCode == 200 {
		snippetResults := make(map[string]any, len(s.config.SnippetPaths))
		pageURL := e.URL
		if result.FinalURL != "" {
			pageURL = result.FinalURL
		}
		for _, sp := range s.config.SnippetPaths {
			if ctx.Err() != nil {
				break
			}
			code, readErr := os.ReadFile(sp)
			if readErr != nil {
				fmt.Fprintf(os.Stderr, "\n  [warn] snippet read %s: %v\n", sp, readErr)
				continue
			}
			s.browserSem <- struct{}{}
			val, runErr := s.renderer.RunSnippet(ctx, pageURL, s.config.UserAgent, string(code))
			<-s.browserSem
			if runErr != nil {
				fmt.Fprintf(os.Stderr, "\n  [warn] snippet %s on %s: %v\n", sp, e.URL, runErr)
			} else {
				snippetResults[filepath.Base(sp)] = val
			}
		}
		if len(snippetResults) > 0 {
			page.SnippetResults = snippetResults
		}
	}

	// Cross-domain redirect detection for seed URL
	if e.URL == s.seedURL && result.FinalURL != "" {
		finalP, _ := url.Parse(result.FinalURL)
		if finalP != nil && finalP.Hostname() != s.queue.SeedDomain() {
			s.queue.UpdateSeedDomain(finalP.Hostname())
			fmt.Fprintf(os.Stderr, "  [redirect] Seed domain updated to %s\n", finalP.Hostname())
		}
	}

	return renderedHTML
}

func (s *crawlSession) enqueueLinks(e types.QueueEntry, page *types.PageData) {
	if s.config.Mode != types.ModeSpider && s.config.Mode != "" {
		return
	}
	for _, link := range page.InternalLinks {
		parsed, err := url.Parse(link)
		if err != nil {
			continue
		}
		if parsed.Scheme != "" && parsed.Scheme != "http" && parsed.Scheme != "https" {
			continue
		}
		ref := e.URL
		s.queue.Enqueue(link, e.Depth+1, &ref)
	}
}

func (s *crawlSession) collectPostCrawlData(e types.QueueEntry, result types.FetchResult, page *types.PageData) {
	// Extract resource refs outside the lock (CPU-bound HTML parsing).
	var refs []types.ResourceEntry
	if s.resRefsDisk != nil && result.HTML != "" {
		pageURL := e.URL
		if result.FinalURL != "" {
			pageURL = result.FinalURL
		}
		refs = extract.ExtractResourceRefs(result.HTML, pageURL)
	}

	// Parse content-length outside the lock.
	var transferSize int64
	hasTransferSize := false
	if s.resRefsDisk != nil {
		if cl, ok := result.Headers["content-length"]; ok {
			if size, err := parseInt64(cl); err == nil {
				transferSize = size
				hasTransferSize = true
			}
		}
	}

	// Single lock acquisition for all shared state writes.
	needsLock := (s.extLinksDisk != nil && len(page.ExternalLinks) > 0) || len(refs) > 0 || hasTransferSize
	if needsLock {
		s.pagesMu.Lock()
		if s.extLinksDisk != nil {
			for _, extLink := range page.ExternalLinks {
				s.extLinksDisk.Add(extLink, e.URL)
			}
		}
		if len(refs) > 0 {
			s.resRefsDisk.Add(e.URL, refs)
		}
		if hasTransferSize {
			s.transferSizes[e.URL] = transferSize
		}
		s.pagesMu.Unlock()
	}
}

func (s *crawlSession) storePage(e types.QueueEntry, page *types.PageData, sourceHTML, renderedHTML string) {
	// Aggregate anchor stats while Anchors still exist (has its own lock).
	s.anchorAgg.AddPage(page.URL, page.StatusCode, page.Anchors)

	// Create stripped shallow copy for output
	stripped := *page
	if !s.config.FullAnchors {
		stripped.Anchors = nil
	}
	if !s.config.FullPageWeight && stripped.PageWeight != nil {
		pw := *stripped.PageWeight
		pw.Resources = nil
		stripped.PageWeight = &pw
	}

	// Encoding, compression and n-gram tokenising run here in the worker,
	// outside any shared lock; the sink goroutine only writes bytes and
	// commits batched SQL.
	s.emitPage(&stripped, sourceHTML, renderedHTML)
	if s.ngramAnalyzer != nil && page.StatusCode == 200 && page.BodyText != "" {
		s.ngramAnalyzer.AddPrepared(s.ngramAnalyzer.PreparePage(page.BodyText))
	}

	s.pagesMu.Lock()
	if !s.canReloadFromDisk {
		s.pages = append(s.pages, page)
	}
	s.pagesMu.Unlock()
	if !s.canReloadFromDisk && !s.config.Embeddings {
		page.BodyText = ""
	}

	// Progress
	n := int(s.crawled.Add(1))
	if s.onProgress != nil {
		elapsed := time.Since(s.rateStart).Seconds()
		rate := 0.0
		if elapsed > 0 {
			rate = float64(int32(n)-s.resumeOffset) / elapsed
		}
		excTotal, excStats := s.queue.ExcludedSnapshot()
		s.onProgress(CrawlProgress{
			Crawled:        n,
			Queued:         s.queue.Size(),
			TotalSeen:      s.queue.TotalSeen(),
			Coverage:       s.queue.CoverageSnapshot(),
			Excluded:       excTotal,
			ExcludedStats:  excStats,
			CurrentURL:     e.URL,
			Rate:           rate,
			Errors:         int(s.errorCount.Load()),
			StatusCode:     page.StatusCode,
			ResponseTimeMs: page.ResponseTimeMs,
			PageError:      page.Error,
		})
	}
}

func parseInt64(s string) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(s), 10, 64)
}
