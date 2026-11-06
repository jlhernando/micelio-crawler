package logs

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// withLimits swaps the analysis limits for one test.
func withLimits(t *testing.T, edit func(*analysisLimits)) {
	t.Helper()
	l := limits()
	edit(&l)
	old := limits
	limits = func() analysisLimits { return l }
	t.Cleanup(func() { limits = old })
}

func combinedLine(path string, ua string) string {
	return fmt.Sprintf(`66.249.66.1 - - [01/Apr/2026:08:00:00 +0000] "GET %s HTTP/1.1" 200 100 "-" "%s"`, path, ua)
}

func TestAggregatorURLCap(t *testing.T) {
	withLimits(t, func(l *analysisLimits) { l.MaxURLs = 3 })
	a := newAggregator()
	for i := 0; i < 10; i++ {
		e, err := ParseLine(combinedLine(fmt.Sprintf("/p%d", i), "Googlebot/2.1"), FormatApacheCombined)
		if err != nil {
			t.Fatal(err)
		}
		e.Bot = IdentifyBot(e.UserAgent)
		a.add(e)
	}
	// Known URLs keep counting after the cap.
	e, _ := ParseLine(combinedLine("/p0", "Googlebot/2.1"), FormatApacheCombined)
	e.Bot = IdentifyBot(e.UserAgent)
	a.add(e)

	if len(a.urlHits) != 4 {
		t.Fatalf("urlHits = %d entries, want 3 URLs + other", len(a.urlHits))
	}
	if got := a.urlHits[OtherURLsPath].hits; got != 7 {
		t.Fatalf("other bucket hits = %d, want 7", got)
	}
	if got := a.urlHits["/p0"].hits; got != 2 {
		t.Fatalf("/p0 hits = %d, want 2", got)
	}
	stats := a.finalize()
	if stats.TotalHits != 11 {
		t.Fatalf("TotalHits = %d", stats.TotalHits)
	}
	if len(stats.Warnings) != 1 || !strings.Contains(stats.Warnings[0], "MICELIO_MAX_LOG_URLS") {
		t.Fatalf("warnings = %v", stats.Warnings)
	}
}

func TestAggregatorTimeAndStatusCaps(t *testing.T) {
	a := newAggregator()
	m := make(map[string]int64, maxTimeKeys)
	for i := 0; i < maxTimeKeys; i++ {
		m[fmt.Sprint(i)] = 1
	}
	a.incTime(m, "new")
	if _, ok := m["new"]; ok || !a.timeCapped {
		t.Fatal("new time key added past maxTimeKeys")
	}
	a.incTime(m, "5")
	if m["5"] != 2 {
		t.Fatal("existing time key not counted past the cap")
	}

	e, _ := ParseLine(combinedLine("/", "x"), FormatApacheCombined)
	e.Status = 123456789
	a.add(e)
	if _, ok := a.stats.StatusCodes[123456789]; ok || a.stats.StatusCodes[0] != 1 {
		t.Fatalf("garbage status not folded into 0: %v", a.stats.StatusCodes)
	}
}

func writeGzip(t *testing.T, path string, data []byte) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(data)
	zw.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGzipDecompressedCap(t *testing.T) {
	withLimits(t, func(l *analysisLimits) { l.MaxDecompressedBytes = 1 << 20 })
	var b strings.Builder
	for b.Len() < 4<<20 {
		b.WriteString(combinedLine("/a", "Googlebot/2.1"))
		b.WriteByte('\n')
	}
	p := filepath.Join(t.TempDir(), "bomb.log.gz")
	writeGzip(t, p, []byte(b.String()))

	_, _, err := ParseFileWithState(p, "", &ParseState{}, func(*LogEntry) {}, nil)
	if err == nil || !strings.Contains(err.Error(), "MICELIO_MAX_LOG_DECOMPRESSED_GB") {
		t.Fatalf("err = %v, want decompressed-size error", err)
	}

	// A file under the cap parses.
	small := filepath.Join(t.TempDir(), "ok.log.gz")
	writeGzip(t, small, []byte(combinedLine("/a", "Googlebot/2.1")+"\n"))
	n := 0
	if _, _, err := ParseFileWithState(small, "", &ParseState{}, func(*LogEntry) { n++ }, nil); err != nil || n != 1 {
		t.Fatalf("small gzip: err=%v entries=%d", err, n)
	}
}

// Blank and comment lines before the first data rows are counted but not
// kept; the line count and the entries stay the same as before.
func TestCommentLinesBeforeDetection(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 50_000; i++ {
		b.WriteString("# comment\n\n")
	}
	for i := 0; i < 30; i++ {
		b.WriteString(combinedLine(fmt.Sprintf("/p%d", i), "Googlebot/2.1") + "\n")
	}
	p := filepath.Join(t.TempDir(), "comments.log.gz")
	writeGzip(t, p, []byte(b.String()))
	n := 0
	format, lines, err := ParseFileWithState(p, "", &ParseState{}, func(*LogEntry) { n++ }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatApacheCombined && format != FormatNginxCombined || n != 30 || lines != 100_030 {
		t.Fatalf("format=%s entries=%d lines=%d", format, n, lines)
	}
}

// Two analyses that overlap must leave GC as it was (with plain save and
// restore the one finishing last could put back the other's GOGC=-1).
func TestConcurrentAnalysesRestoreGC(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(100))
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 4; i++ {
		var b strings.Builder
		for j := 0; j < 1000*(i+1); j++ {
			b.WriteString(combinedLine(fmt.Sprintf("/p%d", j), "Googlebot/2.1") + "\n")
		}
		p := filepath.Join(dir, fmt.Sprintf("a%d.log", i))
		os.WriteFile(p, []byte(b.String()), 0o644)
		paths = append(paths, p)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// AnalyzeMulti only: AnalyzeFull shares one package ParseState.
			AnalyzeMulti(paths[i%4:i%4+1], "", 1, nil, nil)
		}(i)
	}
	wg.Wait()
	if v := debug.SetGCPercent(100); v != 100 {
		t.Fatalf("GOGC = %d after concurrent analyses, want 100", v)
	}
}

// --- Bot verification ---

type fakeResolver struct {
	ptr      map[string]string   // ip -> hostname
	ptrs     map[string][]string // ip -> several PTR names (wins over ptr)
	fwd      map[string][]string // hostname -> ips
	delay    time.Duration
	block    bool // wait for ctx
	calls    atomic.Int64
	inFlight atomic.Int64
	peak     atomic.Int64
	peakG    atomic.Int64
}

func (f *fakeResolver) enter(ctx context.Context) error {
	f.calls.Add(1)
	n := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if g := int64(runtime.NumGoroutine()); g > f.peakG.Load() {
		f.peakG.Store(g)
	}
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return nil
}

func (f *fakeResolver) LookupAddr(ctx context.Context, ip string) ([]string, error) {
	if err := f.enter(ctx); err != nil {
		return nil, err
	}
	if hs, ok := f.ptrs[ip]; ok {
		return hs, nil
	}
	if h, ok := f.ptr[ip]; ok {
		return []string{h + "."}, nil
	}
	return nil, errors.New("no PTR")
}

func (f *fakeResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	if err := f.enter(ctx); err != nil {
		return nil, err
	}
	if ips, ok := f.fwd[host]; ok {
		return ips, nil
	}
	return nil, errors.New("no A")
}

func testVerifier(r dnsResolver, concurrency, maxIPs int) *BotVerifier {
	v := NewBotVerifier()
	v.resolver = r
	v.concurrency = concurrency
	v.sem = make(chan struct{}, concurrency)
	v.maxIPs = maxIPs
	return v
}

func TestVerifyRejectsGoogleusercontent(t *testing.T) {
	r := &fakeResolver{
		ptr: map[string]string{
			"34.1.2.3":    "3.2.1.34.bc.googleusercontent.com",
			"66.249.66.1": "crawl-66-249-66-1.googlebot.com",
		},
		fwd: map[string][]string{
			"3.2.1.34.bc.googleusercontent.com": {"34.1.2.3"},
			"crawl-66-249-66-1.googlebot.com":   {"66.249.66.1"},
		},
	}
	v := testVerifier(r, 4, 100)
	if res := v.VerifyBotIP("34.1.2.3", "Googlebot"); res.Verified || !res.SpoofDetected {
		t.Fatalf("Google Cloud address accepted as Googlebot: %+v", res)
	}
	if res := v.VerifyBotIP("66.249.66.1", "Googlebot"); !res.Verified {
		t.Fatalf("real Googlebot rejected: %+v", res)
	}
	// The cache is per bot: a real Googlebot IP is not a real Bingbot.
	if res := v.VerifyBotIP("66.249.66.1", "Bingbot"); res.Verified {
		t.Fatalf("Googlebot IP verified as Bingbot from cache: %+v", res)
	}
}

// An address with several PTR records is verified when any in-domain name
// forward-confirms, not only the first; IPv6 spellings compare by value.
func TestVerifyMultiplePTRAndIPv6(t *testing.T) {
	r := &fakeResolver{
		ptrs: map[string][]string{
			"66.249.66.9":           {"proxy.example.net.", "crawl-66-249-66-9.googlebot.com."},
			"2001:4860:4801:10::2a": {"crawl-v6.googlebot.com."},
		},
		fwd: map[string][]string{
			"crawl-66-249-66-9.googlebot.com": {"66.249.66.9"},
			"crawl-v6.googlebot.com":          {"2001:4860:4801:0010:0000:0000:0000:002A"},
		},
	}
	v := testVerifier(r, 4, 100)
	if res := v.VerifyBotIP("66.249.66.9", "Googlebot"); !res.Verified || res.Hostname != "crawl-66-249-66-9.googlebot.com" {
		t.Fatalf("second PTR name not checked: %+v", res)
	}
	if res := v.VerifyBotIP("2001:4860:4801:10::2a", "Googlebot"); !res.Verified {
		t.Fatalf("expanded IPv6 forward record not matched: %+v", res)
	}
}

func TestVerifyInvalidIPNoDNS(t *testing.T) {
	r := &fakeResolver{}
	v := testVerifier(r, 4, 100)
	if res := v.VerifyBotIP("not-an-ip", "Googlebot"); !res.SpoofDetected || res.Verified {
		t.Fatalf("invalid IP: %+v", res)
	}
	if r.calls.Load() != 0 {
		t.Fatalf("DNS called %d times for a non-IP", r.calls.Load())
	}
}

func manyIPs(n int, prefix string) map[string]struct{} {
	m := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		m[fmt.Sprintf("%s.%d.%d", prefix, i/256, i%256)] = struct{}{}
	}
	return m
}

// One goroutine per IP was started before; now a fixed pool runs and at
// most maxIPs are checked.
func TestVerifyBoundedPoolAndCap(t *testing.T) {
	r := &fakeResolver{delay: time.Millisecond}
	v := testVerifier(r, 4, 300)
	botIPs := map[string]map[string]struct{}{
		"Googlebot": manyIPs(2000, "10.1"),
		"Bingbot":   manyIPs(2000, "10.2"),
		"AhrefsBot": manyIPs(2000, "10.3"), // no domains: never checked
	}
	base := runtime.NumGoroutine()
	stats, _ := v.VerifyBotIPs(botIPs)
	if r.calls.Load() != 300 { // no PTR: one lookup per IP
		t.Fatalf("DNS calls = %d, want 300", r.calls.Load())
	}
	if p := r.peak.Load(); p > 4 {
		t.Fatalf("%d lookups in flight, want <= 4", p)
	}
	if g := r.peakG.Load(); g > int64(base)+10 {
		t.Fatalf("peak goroutines %d (base %d): pool not bounded", g, base)
	}
	for _, bot := range []string{"Googlebot", "Bingbot"} {
		s := stats[bot]
		if s == nil || s.TotalIPs != 150 || s.Skipped != 1850 {
			t.Fatalf("%s stats = %+v, want 150 checked / 1850 skipped", bot, s)
		}
	}
	if _, ok := stats["AhrefsBot"]; ok {
		t.Fatal("bot without verification domains was checked")
	}
}

func TestVerifyBatchTimeout(t *testing.T) {
	r := &fakeResolver{block: true}
	v := testVerifier(r, 2, 100)
	v.batchTimeout = 50 * time.Millisecond
	start := time.Now()
	stats, _ := v.VerifyBotIPs(map[string]map[string]struct{}{"Googlebot": manyIPs(20, "10.9")})
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("batch took %v, want it cut at the batch timeout", d)
	}
	if s := stats["Googlebot"]; s == nil || s.Skipped != 20 || s.TotalIPs != 0 {
		t.Fatalf("stats = %+v, want all 20 skipped", s)
	}
}

// Overlapping requests for one job share one run.
func TestVerifyJobDedupes(t *testing.T) {
	r := &fakeResolver{delay: 5 * time.Millisecond}
	v := testVerifier(r, 4, 1000)
	botIPs := map[string]map[string]struct{}{"Googlebot": manyIPs(40, "10.4")}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stats, _ := v.VerifyJob("job-1", botIPs)
			if stats["Googlebot"].TotalIPs != 40 {
				t.Errorf("stats = %+v", stats["Googlebot"])
			}
		}()
	}
	wg.Wait()
	v.VerifyJob("job-1", botIPs) // cached
	if c := r.calls.Load(); c != 40 {
		t.Fatalf("DNS calls = %d, want 40 (one run for 11 requests)", c)
	}
}
