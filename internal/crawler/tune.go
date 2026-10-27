package crawler

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Live tuning: delay, concurrency and the page limit of a running crawl can
// change without stopping it. Filters, depth, robots and rendering cannot:
// pages crawled under different rules would not analyse coherently.

// Tuning is the part of a crawl's settings that can change while it runs.
// Zero fields mean "unchanged" in Apply.
type Tuning struct {
	DelayMs     int `json:"delay"`
	Concurrency int `json:"concurrency"`
	MaxPages    int `json:"limit"`
}

// Tuner changes a running crawl. It is handed out once the crawl session is
// set up (see CrawlWithTuner).
type Tuner struct {
	mu sync.Mutex
	s  *crawlSession
	t  Tuning
}

// Limits for live changes.
const (
	maxLiveConcurrency = 100
	maxLiveDelayMs     = 60_000
)

// Get returns the current values.
func (tu *Tuner) Get() Tuning {
	tu.mu.Lock()
	defer tu.mu.Unlock()
	return tu.t
}

// Apply changes the non-zero fields and returns the new values. A delay of
// -1 means "no delay" (0 ms; the per-host floor and robots.txt Crawl-delay
// still apply).
func (tu *Tuner) Apply(n Tuning) (Tuning, error) {
	if n.Concurrency < 0 || n.Concurrency > maxLiveConcurrency {
		return tu.Get(), errors.New("concurrency must be between 1 and 100")
	}
	if n.DelayMs < -1 || n.DelayMs > maxLiveDelayMs {
		return tu.Get(), errors.New("delay must be between 0 and 60000 ms")
	}
	if n.MaxPages < 0 {
		return tu.Get(), errors.New("limit must be positive")
	}
	tu.mu.Lock()
	defer tu.mu.Unlock()
	s := tu.s
	if n.Concurrency > 0 {
		tu.t.Concurrency = n.Concurrency
		s.sem.setLimit(n.Concurrency)
	}
	if n.DelayMs != 0 {
		ms := max(n.DelayMs, 0)
		tu.t.DelayMs = ms
		d := time.Duration(ms) * time.Millisecond
		if s.adaptiveRL != nil {
			s.adaptiveRL.SetBase(d)
		}
		if s.hostRL != nil {
			s.hostRL.SetBase(d)
		}
	}
	if n.MaxPages > 0 {
		tu.t.MaxPages = n.MaxPages
		s.queue.SetMaxPages(n.MaxPages)
	}
	return tu.t, nil
}

// dynSem is a semaphore whose limit can change while it is in use.
type dynSem struct {
	mu    sync.Mutex
	n     int
	limit int
	wake  chan struct{}
}

func newDynSem(limit int) *dynSem {
	return &dynSem{limit: max(limit, 1), wake: make(chan struct{})}
}

// acquire waits for a free slot; false if ctx ends first.
func (d *dynSem) acquire(ctx context.Context) bool {
	for {
		d.mu.Lock()
		if d.n < d.limit {
			d.n++
			d.mu.Unlock()
			return true
		}
		w := d.wake
		d.mu.Unlock()
		select {
		case <-w:
		case <-ctx.Done():
			return false
		}
	}
}

func (d *dynSem) release() {
	d.mu.Lock()
	d.n--
	d.signalLocked()
	d.mu.Unlock()
}

// setLimit changes the limit. Running holders keep their slot; a lower limit
// takes effect as they finish.
func (d *dynSem) setLimit(limit int) {
	d.mu.Lock()
	d.limit = max(limit, 1)
	d.signalLocked()
	d.mu.Unlock()
}

func (d *dynSem) signalLocked() {
	close(d.wake)
	d.wake = make(chan struct{})
}
