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
		s.addToDeadLetter(e, 0, "path budget exceeded: "+rule)
		return
	}

	// Robots.txt check
	if s.robotsChecker != nil && !s.robotsChecker.IsAllowed(e.URL, s.config.UserAgent) {
		s.handleRobotsBlocked(e)
		return
	}

	// Rate limiting: token bucket ensures even spacing across all workers.
	// Falls back to per-worker jittered sleep only when no bucket is configured.
	if s.rateBucket != nil {
		s.rateBucket.Wait(ctx)
	} else if s.delay > 0 {
		sleepCtx(ctx, jitter(s.delay))
	}

	// Fetch (with conditional headers if cached)
	result := s.fetchWithCache(ctx, e)

	// Record response for adaptive rate limiting
	if s.adaptiveRL != nil {
		s.recordAdaptiveResponse(e.URL, result)
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

	// Re-enqueue 429s for later retry (up to maxRequeues times)
	const maxRequeues = 2
	if result.StatusCode == 429 && e.Requeues < maxRequeues {
		e.Requeues++
		s.queue.Requeue(e)
		fmt.Fprintf(os.Stderr, "\n  [429] re-queued %s (attempt %d/%d)\n", e.URL, e.Requeues, maxRequeues)
		return
	}
	// DLQ: 429 after retry exhaustion. Page is still stored below with 429 status
	// so it appears in results; DLQ provides the structured failure record.
	if result.StatusCode == 429 && e.Requeues >= maxRequeues {
		s.addToDeadLetter(e, 429, "rate limited after max requeues")
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

	// Store HTML source and/or rendered DOM (if configured and DB available)
	// Written directly to disk (zstd → SQLite) without holding in memory.
	if s.crawlDB != nil && (s.config.SaveHTML || s.config.SaveRendered) {
		var srcHTML string
		if s.config.SaveHTML && result.HTML != "" {
			srcHTML = result.HTML
		}
		var renHTML string
		if s.config.SaveRendered && renderedHTML != "" {
			renHTML = renderedHTML
		}
		if srcHTML != "" || renHTML != "" {
			if err := s.crawlDB.SavePageHTML(e.URL, srcHTML, renHTML); err != nil {
				fmt.Fprintf(os.Stderr, "\n  [warn] save html: %v\n", err)
			}
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
	s.storePage(e, &page)

	// Release InternalLinks from the in-memory page to save ~5KB per page.
	// Links are already streamed to disk (above) and written to JSONL/DB (storePage).
	if s.intLinksDisk != nil {
		page.InternalLinkCount = len(page.InternalLinks)
		page.InternalLinks = nil
	}
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
	if s.writer != nil {
		if writeErr := s.writer.Write(&page); writeErr != nil {
			fmt.Fprintf(os.Stderr, "\n  [warn] write robots-blocked: %v\n", writeErr)
		}
	}
	if s.crawlDB != nil {
		if dbErr := s.crawlDB.InsertPage(&page); dbErr != nil {
			fmt.Fprintf(os.Stderr, "\n  [warn] db robots-blocked: %v\n", dbErr)
		}
	}
	s.pagesMu.Unlock()
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
	s.adaptiveRL.RecordResponse(entryURL, result.StatusCode, result.ResponseTimeMs, rlRemaining, rlReset)
	// Sync token bucket interval with the updated per-domain delay
	if s.rateBucket != nil {
		s.rateBucket.SetInterval(s.adaptiveRL.CurrentDelay(entryURL))
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
	if s.crawlDB != nil {
		s.crawlDB.InsertPage(&page)
	}
	if s.writer != nil {
		s.writer.Write(&page)
	}
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
			if diffErr == nil && len(diffs) > 0 {
				page.RenderDiffs = diffs
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

func (s *crawlSession) storePage(e types.QueueEntry, page *types.PageData) {
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

	s.pagesMu.Lock()
	if !s.canReloadFromDisk {
		s.pages = append(s.pages, page)
	}
	if s.writer != nil {
		if writeErr := s.writer.Write(&stripped); writeErr != nil {
			fmt.Fprintf(os.Stderr, "\n  [warn] write error: %v\n", writeErr)
			s.errorCount.Add(1)
		}
	}
	if s.crawlDB != nil {
		if dbErr := s.crawlDB.InsertPage(&stripped); dbErr != nil {
			fmt.Fprintf(os.Stderr, "\n  [warn] db write error: %v\n", dbErr)
		}
	}
	if s.ngramAnalyzer != nil && page.StatusCode == 200 && page.BodyText != "" {
		s.ngramAnalyzer.AddPage(page.BodyText)
	}
	if !s.canReloadFromDisk && !s.config.Embeddings {
		page.BodyText = ""
	}
	s.pagesMu.Unlock()

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
