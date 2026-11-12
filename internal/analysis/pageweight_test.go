package analysis

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

func hostOf(u string) string {
	h := strings.TrimPrefix(u, "https://")
	return h[:strings.IndexByte(h, '/')]
}

func TestComputePageWeightPerHostLimit(t *testing.T) {
	refs := map[string][]types.ResourceEntry{}
	var entries []types.ResourceEntry
	for i := 0; i < 20; i++ {
		entries = append(entries, types.ResourceEntry{URL: fmt.Sprintf("https://cdn.example.com/r%d.js", i), Type: "script"})
		entries = append(entries, types.ResourceEntry{URL: fmt.Sprintf("https://img.example.com/r%d.png", i), Type: "image"})
	}
	refs["https://example.com/"] = entries
	pages := []*types.PageData{{URL: "https://example.com/", BodySize: 100}}

	limit := map[string]int{"cdn.example.com": 2, "img.example.com": 5}
	var mu sync.Mutex
	inFlight := map[string]int{}
	maxInFlight := map[string]int{}
	var calls atomic.Int32
	sizer := ResourceSizer{
		Size: func(ctx context.Context, u string) SizeResult {
			host := hostOf(u)
			mu.Lock()
			inFlight[host]++
			maxInFlight[host] = max(maxInFlight[host], inFlight[host])
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			inFlight[host]--
			mu.Unlock()
			calls.Add(1)
			return SizeResult{Size: 10, OK: true}
		},
		PerHost: func(h string) int { return limit[h] },
	}

	sum := ComputePageWeight(context.Background(), pages, refs, nil, PageWeightOptions{Concurrency: 16, Sizer: sizer})

	if calls.Load() != 40 || sum.Requests != 40 || sum.Sized != 40 {
		t.Fatalf("calls %d, summary %+v; want 40", calls.Load(), sum)
	}
	for host, m := range maxInFlight {
		if m > limit[host] {
			t.Errorf("host %s had %d concurrent requests, limit %d", host, m, limit[host])
		}
	}
	if maxInFlight["img.example.com"] < 2 {
		t.Errorf("img host never ran in parallel (max %d): per-host limit not used", maxInFlight["img.example.com"])
	}
	if pages[0].PageWeight == nil || pages[0].PageWeight.TotalBytes != 100+40*10 {
		t.Fatalf("unexpected page weight: %+v", pages[0].PageWeight)
	}
}

// Each unique resource URL is asked once, however many pages use it, and
// the weights are the same as building them from the sizes by hand.
func TestComputePageWeightDedupesAndKeepsWeights(t *testing.T) {
	sizes := map[string]int64{
		"https://site.test/app.css":     5000,
		"https://site.test/app.js":      20000,
		"https://cdn.test/logo.png":     3000,
		"https://cdn.test/hero.jpg":     150000,
		"https://cdn.test/missing.png":  -1, // 404: no size
		"https://other.test/font.woff2": 40000,
	}
	page := func(n int, extra ...string) []types.ResourceEntry {
		rs := []types.ResourceEntry{
			{URL: "https://site.test/app.css", Type: "stylesheet"},
			{URL: "https://site.test/app.js", Type: "script"},
			{URL: "https://cdn.test/logo.png", Type: "image"},
		}
		for _, e := range extra {
			rs = append(rs, types.ResourceEntry{URL: e, Type: "image"})
		}
		return rs
	}
	refs := map[string][]types.ResourceEntry{
		"https://site.test/":  page(0, "https://cdn.test/hero.jpg"),
		"https://site.test/a": page(1, "https://cdn.test/missing.png"),
		"https://site.test/b": append(page(2), types.ResourceEntry{URL: "https://other.test/font.woff2", Type: "font"}),
	}
	transfer := map[string]int64{"https://site.test/": 12000}
	pages := []*types.PageData{
		{URL: "https://site.test/", BodySize: 50000, StatusCode: 200},
		{URL: "https://site.test/a", BodySize: 8000, StatusCode: 200},
		{URL: "https://site.test/b", BodySize: 9000, StatusCode: 200},
	}

	var mu sync.Mutex
	asked := map[string]int{}
	sizer := ResourceSizer{Size: func(ctx context.Context, u string) SizeResult {
		mu.Lock()
		asked[u]++
		mu.Unlock()
		if s := sizes[u]; s >= 0 {
			return SizeResult{Size: s, OK: true}
		}
		return SizeResult{}
	}}
	sum := ComputePageWeight(context.Background(), pages, refs, transfer, PageWeightOptions{Concurrency: 4, Sizer: sizer})

	if len(asked) != len(sizes) {
		t.Fatalf("asked %d URLs, want %d", len(asked), len(sizes))
	}
	for u, n := range asked {
		if n != 1 {
			t.Errorf("%s asked %d times", u, n)
		}
	}
	if sum.Total != 6 || sum.Sized != 5 || sum.Unsized != 1 || sum.Skipped != 0 {
		t.Fatalf("summary %+v", sum)
	}
	want := map[string]int64{
		"https://site.test/":  12000 + 5000 + 20000 + 3000 + 150000, // transfer size wins over body
		"https://site.test/a": 8000 + 5000 + 20000 + 3000 + 0,       // unsized counts 0
		"https://site.test/b": 9000 + 5000 + 20000 + 3000 + 40000,
	}
	for _, p := range pages {
		if p.PageWeight == nil || p.PageWeight.TotalBytes != want[p.URL] {
			t.Errorf("%s weight %+v, want %d", p.URL, p.PageWeight, want[p.URL])
		}
	}
	if got := pages[2].PageWeight.ByType["font"]; got.Count != 1 || got.Bytes != 40000 {
		t.Errorf("font summary %+v", got)
	}
	st := BuildPageWeightStats(pages)
	sum.Annotate(st)
	if st.ResourcesTotal != 6 || st.ResourcesSized != 5 || st.ResourcesUnsized != 1 {
		t.Errorf("stats annotation %+v", st)
	}
}

// A 429/503 is "slow down", not a missing size: the URL is asked again after
// the pause and gets its size.
func TestFetchResourceSizesRetriesThrottled(t *testing.T) {
	old := pageWeightBaseWait
	pageWeightBaseWait = 20 * time.Millisecond
	defer func() { pageWeightBaseWait = old }()

	refs := map[string][]types.ResourceEntry{}
	for i := 0; i < 10; i++ {
		refs["https://site.test/"] = append(refs["https://site.test/"], types.ResourceEntry{URL: fmt.Sprintf("https://cdn.test/%d.png", i)})
	}
	var mu sync.Mutex
	var throttles int
	sizer := ResourceSizer{
		Size: func(ctx context.Context, u string) SizeResult {
			mu.Lock()
			first := throttles < 3
			if first {
				throttles++
			}
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
			if first {
				return SizeResult{Throttled: true}
			}
			return SizeResult{Size: 7, OK: true}
		},
		PerHost: func(string) int { return 4 },
	}
	start := time.Now()
	sizes, sum := FetchResourceSizes(context.Background(), refs, PageWeightOptions{Sizer: sizer})
	if len(sizes) != 10 || sum.Sized != 10 || sum.RateLimited != 0 {
		t.Fatalf("sizes %d, summary %+v", len(sizes), sum)
	}
	if sum.Throttles != 3 || sum.Requests != 13 {
		t.Errorf("throttles %d requests %d, want 3 and 13", sum.Throttles, sum.Requests)
	}
	if time.Since(start) < pageWeightBaseWait {
		t.Errorf("no pause after 429 (%s)", time.Since(start))
	}
}

// A host that answers 429 now and then but sizes resources in between is
// never given up: answers after a pause clear the strikes. (A 10K Wikipedia
// crawl left 9,089 thumbnails unsized when strikes only added up.)
func TestFetchResourceSizesIntermittentThrottleKeepsGoing(t *testing.T) {
	old := pageWeightBaseWait
	pageWeightBaseWait = time.Millisecond
	defer func() { pageWeightBaseWait = old }()

	refs := map[string][]types.ResourceEntry{}
	for i := 0; i < 100; i++ {
		refs["https://site.test/"] = append(refs["https://site.test/"], types.ResourceEntry{URL: fmt.Sprintf("https://cdn.test/%d.png", i)})
	}
	var n atomic.Int32
	sizer := ResourceSizer{Size: func(ctx context.Context, u string) SizeResult {
		time.Sleep(time.Millisecond)
		if n.Add(1)%5 == 0 {
			return SizeResult{Throttled: true}
		}
		return SizeResult{Size: 3, OK: true}
	}}
	sizes, sum := FetchResourceSizes(context.Background(), refs, PageWeightOptions{Sizer: sizer})
	if len(sizes) != 100 || sum.RateLimited != 0 {
		t.Fatalf("sized %d/100, summary %+v", len(sizes), sum)
	}
	if sum.Throttles <= pageWeightMaxStrikes {
		t.Fatalf("only %d throttles: test does not exceed the strike limit", sum.Throttles)
	}
}

// One URL answered 429 every time (Wikimedia does that for a thumbnail it
// cannot render) is left unsized after pageWeightMaxAttempts; the rest of
// the host is still sized. On a 10K Wikipedia crawl one such thumbnail made
// the whole host (9,089 resources) unsized.
func TestFetchResourceSizesOneRefusedURLDoesNotSinkHost(t *testing.T) {
	old := pageWeightBaseWait
	pageWeightBaseWait = time.Millisecond
	defer func() { pageWeightBaseWait = old }()

	refs := map[string][]types.ResourceEntry{}
	for i := 0; i < 50; i++ {
		refs["https://site.test/"] = append(refs["https://site.test/"], types.ResourceEntry{URL: fmt.Sprintf("https://thumb.test/%02d.png", i)})
	}
	var badCalls atomic.Int32
	sizer := ResourceSizer{Size: func(ctx context.Context, u string) SizeResult {
		if u == "https://thumb.test/07.png" || u == "https://thumb.test/31.png" {
			badCalls.Add(1)
			return SizeResult{Throttled: true}
		}
		return SizeResult{Size: 9, OK: true}
	}}
	sizes, sum := FetchResourceSizes(context.Background(), refs, PageWeightOptions{Sizer: sizer})
	if len(sizes) != 48 || sum.RateLimited != 0 || sum.Refused != 2 || sum.Unsized != 2 {
		t.Fatalf("sized %d/48, summary %+v", len(sizes), sum)
	}
	if n := badCalls.Load(); n != 2*pageWeightMaxAttempts {
		t.Errorf("refused URLs asked %d times, want %d", n, 2*pageWeightMaxAttempts)
	}
}

// A big site (6 requests at once) whose 6 requests in flight all come back
// 429 together got one pause, not 6 strikes: before, the 5th refusal gave
// the whole host up and left its queue unsized.
func TestFetchResourceSizesBurstOfRefusalsIsOneStrike(t *testing.T) {
	old := pageWeightBaseWait
	pageWeightBaseWait = time.Millisecond
	defer func() { pageWeightBaseWait = old }()

	const perHost = 6
	refs := map[string][]types.ResourceEntry{}
	for i := 0; i < 30; i++ {
		refs["https://site.test/"] = append(refs["https://site.test/"], types.ResourceEntry{URL: fmt.Sprintf("https://upload.test/%02d.png", i)})
	}
	var calls atomic.Int32
	burst := make(chan struct{})
	sizer := ResourceSizer{
		Size: func(ctx context.Context, u string) SizeResult {
			n := calls.Add(1)
			if n <= perHost {
				// Hold the first 6 until all are in flight, then refuse
				// them together.
				if n == perHost {
					close(burst)
				}
				select {
				case <-burst:
				case <-time.After(5 * time.Second):
					t.Error("the 6 slots were never in flight together")
				}
				return SizeResult{Throttled: true}
			}
			return SizeResult{Size: 5, OK: true}
		},
		PerHost: func(string) int { return perHost },
	}
	sizes, sum := FetchResourceSizes(context.Background(), refs, PageWeightOptions{Sizer: sizer})
	if len(sizes) != 30 || sum.Sized != 30 || sum.RateLimited != 0 || len(sum.RateLimitedHosts) != 0 || sum.Refused != 0 {
		t.Fatalf("sized %d/30, summary %+v", len(sizes), sum)
	}
	if sum.Throttles != perHost {
		t.Errorf("throttles %d, want %d", sum.Throttles, perHost)
	}
}

// A host that keeps answering 429 has its remaining resources left unsized
// (reported), and stops getting requests.
func TestFetchResourceSizesGivesUpOnRateLimitedHost(t *testing.T) {
	old := pageWeightBaseWait
	pageWeightBaseWait = time.Millisecond
	defer func() { pageWeightBaseWait = old }()

	refs := map[string][]types.ResourceEntry{}
	for i := 0; i < 200; i++ {
		refs["https://site.test/"] = append(refs["https://site.test/"],
			types.ResourceEntry{URL: fmt.Sprintf("https://busy.test/%d.png", i)},
			types.ResourceEntry{URL: fmt.Sprintf("https://ok.test/%d.png", i)})
	}
	var busyCalls atomic.Int32
	sizer := ResourceSizer{Size: func(ctx context.Context, u string) SizeResult {
		if hostOf(u) == "busy.test" {
			busyCalls.Add(1)
			return SizeResult{Throttled: true}
		}
		return SizeResult{Size: 1, OK: true}
	}}
	var lastDone, lastTotal int
	sizes, sum := FetchResourceSizes(context.Background(), refs, PageWeightOptions{
		Sizer:      sizer,
		OnProgress: func(done, total int) { lastDone, lastTotal = done, total },
	})
	if len(sizes) != 200 || sum.Sized != 200 {
		t.Fatalf("ok host sized %d, summary %+v", len(sizes), sum)
	}
	// The first refused URLs are each asked pageWeightMaxAttempts times and
	// left unsized; once more than pageWeightMaxStrikes URLs in a row are
	// refused the host is given up.
	if sum.RateLimited+sum.Refused != 200 || sum.Refused > pageWeightMaxStrikes+DefaultPageWeightPerHost ||
		len(sum.RateLimitedHosts) != 1 || sum.RateLimitedHosts[0] != "busy.test" {
		t.Fatalf("rate limited %d, refused %d, hosts %v", sum.RateLimited, sum.Refused, sum.RateLimitedHosts)
	}
	if n := busyCalls.Load(); n > int32((pageWeightMaxStrikes+DefaultPageWeightPerHost)*pageWeightMaxAttempts+1) {
		t.Errorf("busy host got %d requests after refusing", n)
	}
	if lastDone != 400 || lastTotal != 400 {
		t.Errorf("progress ended at %d/%d, want 400/400", lastDone, lastTotal)
	}
}

// One host with thousands of slow resources must not hold every slot:
// another host's resources finish early.
func TestFetchResourceSizesRoundRobinsHosts(t *testing.T) {
	refs := map[string][]types.ResourceEntry{}
	for i := 0; i < 300; i++ {
		refs["https://site.test/"] = append(refs["https://site.test/"], types.ResourceEntry{URL: fmt.Sprintf("https://slow.test/%d.png", i)})
	}
	for i := 0; i < 5; i++ {
		refs["https://site.test/"] = append(refs["https://site.test/"], types.ResourceEntry{URL: fmt.Sprintf("https://fast.test/%d.png", i)})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var fastDone atomic.Int32
	allFast := make(chan struct{})
	sizer := ResourceSizer{
		Size: func(ctx context.Context, u string) SizeResult {
			if hostOf(u) == "slow.test" {
				select {
				case <-time.After(20 * time.Millisecond):
				case <-ctx.Done():
				}
				return SizeResult{Size: 1, OK: true}
			}
			if fastDone.Add(1) == 5 {
				close(allFast)
			}
			return SizeResult{Size: 1, OK: true}
		},
		PerHost: func(h string) int {
			if h == "slow.test" {
				return 32 // as many as the global slots
			}
			return 2
		},
	}
	go func() {
		select {
		case <-allFast:
		case <-time.After(5 * time.Second):
		}
		cancel()
	}()
	start := time.Now()
	_, sum := FetchResourceSizes(ctx, refs, PageWeightOptions{Sizer: sizer})
	if fastDone.Load() != 5 {
		t.Fatalf("fast host sized %d/5", fastDone.Load())
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("fast host waited %s behind the slow one", d)
	}
	if sum.Skipped == 0 {
		t.Errorf("cancel should leave slow resources skipped: %+v", sum)
	}
}

func TestComputePageWeightCancelled(t *testing.T) {
	refs := map[string][]types.ResourceEntry{}
	for i := 0; i < 50; i++ {
		refs["https://example.com/"] = append(refs["https://example.com/"], types.ResourceEntry{URL: fmt.Sprintf("https://cdn.example.com/%d", i)})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	sum := ComputePageWeight(ctx, []*types.PageData{{URL: "https://example.com/"}}, refs, nil, PageWeightOptions{Concurrency: 4, Sizer: ResourceSizer{
		Size: func(ctx context.Context, u string) SizeResult {
			calls.Add(1)
			return SizeResult{Size: 1, OK: true}
		},
	}})
	if calls.Load() > 0 {
		t.Errorf("cancelled context still issued %d requests", calls.Load())
	}
	if sum.Skipped != 50 {
		t.Errorf("skipped %d, want 50", sum.Skipped)
	}
}
