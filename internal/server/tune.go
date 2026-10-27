package server

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/micelio/micelio/internal/crawler"
)

// TuneCrawl changes delay, concurrency and the page limit of the running
// crawl. The change is kept in the job config (so a resume uses it) and
// recorded as a crawl event.
func (m *CrawlManager) TuneCrawl(crawlID string, n crawler.Tuning) (crawler.Tuning, error) {
	m.mu.RLock()
	active := m.active
	m.mu.RUnlock()
	if active == nil || active.ID != crawlID {
		return crawler.Tuning{}, fmt.Errorf("no active crawl with id %s", crawlID)
	}
	m.mu.RLock()
	tuner := active.Tuner
	m.mu.RUnlock()
	if tuner == nil {
		return crawler.Tuning{}, fmt.Errorf("crawl %s is still starting, try again in a moment", crawlID)
	}
	before := tuner.Get()
	after, err := tuner.Apply(n)
	if err != nil {
		return before, err
	}

	var changes []string
	if after.DelayMs != before.DelayMs {
		changes = append(changes, fmt.Sprintf("delay %d→%d ms", before.DelayMs, after.DelayMs))
	}
	if after.Concurrency != before.Concurrency {
		changes = append(changes, fmt.Sprintf("concurrency %d→%d", before.Concurrency, after.Concurrency))
	}
	if after.MaxPages != before.MaxPages {
		changes = append(changes, fmt.Sprintf("limit %d→%d", before.MaxPages, after.MaxPages))
	}
	if len(changes) == 0 {
		return after, nil
	}

	// Copy the config: other goroutines may be reading the old map.
	m.mu.Lock()
	cfg := make(map[string]interface{}, len(active.Config)+4)
	for k, v := range active.Config {
		cfg[k] = v
	}
	cfg["delayMs"], cfg["delay"] = float64(after.DelayMs), float64(after.DelayMs)
	cfg["concurrency"] = float64(after.Concurrency)
	cfg["maxPages"], cfg["limit"] = float64(after.MaxPages), float64(after.MaxPages)
	active.Config = cfg
	m.mu.Unlock()

	if err := m.store.UpdateCrawlJobConfig(crawlID, cfg); err != nil {
		log.Printf("store: update crawl %s config: %v", crawlID, err)
	}
	note := strings.Join(changes, ", ")
	m.store.AddCrawlEvent(crawlID, "tuned", note)
	log.Printf("crawl %s tuned: %s", crawlID, note)
	m.emit(ProgressEvent{CrawlID: crawlID, Type: "tuned", Data: map[string]interface{}{
		"crawlId": crawlID, "delay": after.DelayMs, "concurrency": after.Concurrency, "limit": after.MaxPages,
	}})
	return after, nil
}

// CrawlTuning returns the live values of the running crawl.
func (m *CrawlManager) CrawlTuning(crawlID string) (crawler.Tuning, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil || m.active.ID != crawlID || m.active.Tuner == nil {
		return crawler.Tuning{}, fmt.Errorf("no active crawl with id %s", crawlID)
	}
	return m.active.Tuner.Get(), nil
}

// phaseTimer logs how long each post-crawl step takes, so a slow Stop &
// Analyze points at its cause ("[timing] crawl X: report 1.2s, ...").
type phaseTimer struct {
	crawlID string
	last    time.Time
	start   time.Time
	parts   []string
}

func newPhaseTimer(crawlID string) *phaseTimer {
	now := time.Now()
	return &phaseTimer{crawlID: crawlID, last: now, start: now}
}

// mark records the time since the previous mark under name.
func (t *phaseTimer) mark(name string) {
	now := time.Now()
	t.parts = append(t.parts, fmt.Sprintf("%s %s", name, now.Sub(t.last).Round(time.Millisecond)))
	t.last = now
}

func (t *phaseTimer) log() {
	log.Printf("[timing] crawl %s: %s (total %s)", t.crawlID, strings.Join(t.parts, ", "), time.Since(t.start).Round(time.Millisecond))
}
