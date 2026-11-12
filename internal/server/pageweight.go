package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

// pageWeightJob is the resource size check of a finished dashboard crawl.
// It runs in the background after the results are shown: on an image-heavy
// site it is one HEAD per unique resource (188,080 for a 25K-page Wikipedia
// crawl), which used to keep the results hidden for hours.
type pageWeightJob struct {
	refs          map[string][]types.ResourceEntry // page URL -> resources
	transferSizes map[string]int64                 // page URL -> HTML bytes
	sizer         analysis.ResourceSizer
	concurrency   int
}

// pageWeightResult is a finished (or cancelled) size check.
type pageWeightResult struct {
	sizes map[string]int64
	sum   analysis.PageWeightSummary
}

// apply writes the page weights onto pages and returns their stats. The
// caller holds the lock of the result that owns pages.
func (j *pageWeightJob) apply(pages []*types.PageData, r pageWeightResult) *types.PageWeightStats {
	analysis.ApplyResourceSizes(pages, j.refs, j.transferSizes, r.sizes)
	st := analysis.BuildPageWeightStats(pages)
	r.sum.Annotate(st)
	return st
}

// startPageWeight sizes the job's resources in a goroutine. Progress goes to
// /stats (pageWeight) and to the Results page as analysis_progress phase
// "page_weight". The channel receives one result, also when ctx ends.
func (m *CrawlManager) startPageWeight(ctx context.Context, crawlID string, job *pageWeightJob) <-chan pageWeightResult {
	out := make(chan pageWeightResult, 1)
	var (
		emitMu   sync.Mutex
		lastEmit time.Time
	)
	emitPhase := func(done, total int) {
		if ctx.Err() != nil {
			return // a restarted run with the same ID owns the progress now
		}
		m.mu.RLock()
		res := m.results[crawlID]
		m.mu.RUnlock()
		if res != nil {
			res.pageWeightDone.Store(int64(done))
			res.pageWeightTotal.Store(int64(total))
		}
		m.emit(ProgressEvent{
			CrawlID: crawlID, Type: "analysis_progress",
			Data: map[string]interface{}{
				"crawlId": crawlID,
				"phase":   "page_weight",
				"loaded":  done,
				"total":   total,
			},
		})
	}
	go func() {
		sizes, sum := analysis.FetchResourceSizes(ctx, job.refs, analysis.PageWeightOptions{
			Concurrency: job.concurrency,
			Sizer:       job.sizer,
			OnProgress: func(done, total int) {
				emitMu.Lock()
				defer emitMu.Unlock()
				if done == 0 || done == total || time.Since(lastEmit) > 500*time.Millisecond {
					lastEmit = time.Now()
					emitPhase(done, total)
				}
			},
		})
		log.Printf("[page-weight] crawl %s: %s", crawlID, sum)
		out <- pageWeightResult{sizes: sizes, sum: sum}
	}()
	return out
}

// pageWeightNote says what Stop & Analyze or cancel cut short of the size
// check ("" when it ran to the end).
func pageWeightNote(net context.Context, sum analysis.PageWeightSummary) string {
	if net.Err() == nil || sum.Skipped == 0 {
		return ""
	}
	return fmt.Sprintf("page weight %v after %d of %d resources (the rest count as 0 bytes)",
		context.Cause(net), sum.Total-sum.Skipped, sum.Total)
}

// finishPageWeight waits for the size check when the rest of the deferred
// analysis finished first, then publishes the page weights: on the cached
// pages, on a copy reloaded meanwhile, and in the saved stats. crawlStats
// are the final deferred stats. applied is true when runDeferredAnalysis
// already published them; only the progress is cleared then.
//
// The check runs on the network context of ctx: Stop & Analyze and cancel
// end it, and the sizes known by then are published with a note. Delete
// and restart publish nothing.
func (m *CrawlManager) finishPageWeight(ctx context.Context, crawlID string, own *CrawlResults, job *pageWeightJob, ch <-chan pageWeightResult, applied bool, crawlStats types.CrawlStats) {
	if !applied {
		r := <-ch // ends soon after ctx: the check runs on a child of it
		if ctx.Err() != nil && !errors.Is(context.Cause(ctx), errAnalysisCancelled) {
			return // deleted or restarted: publish nothing
		}
		if note := pageWeightNote(netContext(ctx), r.sum); note != "" {
			if crawlStats.AnalysisInterrupted != "" {
				crawlStats.AnalysisInterrupted += "; "
			}
			crawlStats.AnalysisInterrupted += note
			event := "stopped"
			if errors.Is(context.Cause(ctx), errAnalysisCancelled) {
				event = "cancelled"
			}
			m.store.AddCrawlEvent(crawlID, event, note)
			log.Printf("crawl %s: %s", crawlID, note)
		}
		own.mu.Lock()
		crawlStats.PageWeightStats = job.apply(own.Pages, r)
		dropResourceDetail(own.Pages)
		own.mu.Unlock()
		m.mu.RLock()
		res := m.results[crawlID]
		m.mu.RUnlock()
		if res != nil && res != own { // evicted and reloaded meanwhile
			res.mu.Lock()
			if res.PagesReady {
				job.apply(res.Pages, r)
				dropResourceDetail(res.Pages)
			}
			res.mu.Unlock()
		}
		m.publishStats(crawlID, crawlStats)
	}
	m.mu.RLock()
	if res := m.results[crawlID]; res != nil {
		res.pageWeightTotal.Store(0)
	}
	m.mu.RUnlock()
	m.emit(ProgressEvent{
		CrawlID: crawlID, Type: "analysis_progress",
		Data: map[string]interface{}{"crawlId": crawlID, "phase": "page_weight_done"},
	})
}

// dropResourceDetail keeps the page weight summary and drops the
// per-resource list, as stripPagesForCache does. Caller holds the lock.
func dropResourceDetail(pages []*types.PageData) {
	for _, p := range pages {
		if p.PageWeight != nil && p.PageWeight.Resources != nil {
			p.PageWeight.Resources = nil
		}
	}
}
