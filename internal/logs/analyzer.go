package logs

import (
	"hash/maphash"
	"log"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"sync"
)

const maxTopURLs = 500
const maxBotURLs = 100_000
const maxBotIPs = 10_000

// URLStatsFlusher is called by the aggregator to persist URL stats in batches.
// Called whenever the in-memory URL map exceeds a threshold, and once at the
// end for any remaining URLs. The aggregator clears its map after each call.
type URLStatsFlusher func(batch []URLStat) error

// AnalysisResult holds the output of log analysis including data needed for verification.
type AnalysisResult struct {
	Stats      *LogStats
	Format     Format
	BotIPs     map[string]map[string]struct{}     // bot name -> set of IPs
	AllURLs    []URLStat                          // legacy: materialized URL list
	StreamURLs func(fn func(URLStat) error) error // streams sorted bot URLs without materializing
}

// AnalyzeFull parses a log file and returns the full analysis result including bot IPs for verification.
// If flusher is non-nil, per-URL stats are flushed incrementally (batches of ~500K URLs)
// during parsing instead of accumulating in memory.
func AnalyzeFull(path string, formatHint Format, onProgress ProgressFunc, flusher URLStatsFlusher) (*AnalysisResult, error) {
	prevGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(prevGC)
	a := newAggregator()
	a.urlFlusher = flusher
	format, totalLines, err := ParseFileWithFormat(path, formatHint, a.add, onProgress)
	if err != nil {
		return &AnalysisResult{Format: format}, err
	}
	// Drain any remaining URLs.
	if flusher != nil {
		a.flushURLs()
	}
	stats := a.finalize()
	stats.TotalLines = totalLines
	res := &AnalysisResult{Stats: stats, Format: format, BotIPs: a.botIPs}
	if flusher == nil {
		urlHits := a.urlHits // capture for closure
		res.StreamURLs = func(fn func(URLStat) error) error {
			return StreamBotURLsSorted(urlHits, fn)
		}
	}
	return res, nil
}

type aggregator struct {
	stats       LogStats
	urlHits     map[string]*urlAccum
	botURLSets  map[string]map[uint64]struct{} // path hash → struct{} (saves ~100 bytes/entry vs full string)
	botURLCaps  map[string]bool                // true if exceeded cap
	pathHasher  maphash.Hash                   // reusable hasher for URL path dedup
	mobileBots  int64
	aiBots      int64
	errorHits   int64
	firstTime   string
	lastTime    string
	botIPs      map[string]map[string]struct{} // bot name -> set of IPs (for verification)
	botIPCaps   map[string]bool
	botHeatmaps map[string]*[7][24]int64 // per-bot heatmap data
	urlFlusher  URLStatsFlusher          // if set, flush URL batches incrementally
	urlFlushAt  int                      // flush threshold (default 500_000)
	urlsFlushed int                      // total URLs flushed so far
	// Async flush: batches are sent to flushCh and written by a background
	// goroutine so the aggregator core isn't blocked on SQLite I/O.
	flushCh   chan []URLStat
	flushDone chan struct{}
	flushErr  error // first error from async flusher, if any
}

type urlAccum struct {
	hits       int64
	botHits    int64
	humanHits  int64
	bytes      int64
	topBot     string // tracked via heavy-hitter algorithm (approximate)
	topBotHits int64  // internal counter for the heavy-hitter tracker
	status     int
}

func newAggregator() *aggregator {
	return &aggregator{
		stats: LogStats{
			BotHits:            make(map[string]*BotStats),
			StatusCodes:        make(map[int]int64),
			StatusGroups:       make(map[string]int64),
			DailyHits:          make(map[string]int64),
			BotDailyHits:       make(map[string]map[string]int64),
			BotHourlyHits:      make(map[string]map[string]int64),
			HourlyHitsTimeline: make(map[string]int64),
		},
		urlHits:     make(map[string]*urlAccum),
		botURLSets:  make(map[string]map[uint64]struct{}),
		botURLCaps:  make(map[string]bool),
		botIPs:      make(map[string]map[string]struct{}),
		botIPCaps:   make(map[string]bool),
		botHeatmaps: make(map[string]*[7][24]int64),
		urlFlushAt:  500_000, // small flushes overlap with parsing via async flusher; GC off prevents scan cost
	}
}

func (a *aggregator) add(e *LogEntry) {
	a.stats.TotalHits++

	// Use pre-formatted timestamp from parser goroutine (avoids Format() on this core).
	ts := e.TSFormatted
	if ts == "" {
		ts = e.Timestamp.Format("2006-01-02T15:04:05Z")
	}
	day := ts[:10] // "2006-01-02"
	if a.firstTime == "" || ts < a.firstTime {
		a.firstTime = ts
	}
	if ts > a.lastTime {
		a.lastTime = ts
	}

	// Status codes
	a.stats.StatusCodes[e.Status]++
	a.stats.StatusGroups[statusGroup(e.Status)]++

	// Bytes
	a.stats.TotalBytes += e.Bytes

	// Hourly + heatmap — use pre-computed values from parser goroutines.
	hour := e.Hour
	weekday := e.Weekday
	if hour == 0 && weekday == 0 && !e.Timestamp.IsZero() {
		hour = e.Timestamp.Hour()
		weekday = int(e.Timestamp.Weekday())
	}
	a.stats.HourlyHits[hour]++
	a.stats.Heatmap[weekday][hour]++

	// Daily
	a.stats.DailyHits[day]++

	// Hourly timeline (format "2026-04-07T14") — total hits per hour-stamp.
	// Matches ts[:13] which is day + "T" + HH.
	if len(ts) >= 13 {
		hourKey := ts[:13]
		a.stats.HourlyHitsTimeline[hourKey]++
	}

	// URL tracking
	a.trackURL(e)

	if e.Bot != nil {
		a.addBot(e, ts, day)
	} else {
		a.stats.HumanHits++
	}
}

func (a *aggregator) addBot(e *LogEntry, ts, day string) {
	// Cache bot name once — avoids 20+ redundant map key hash computations.
	botName := e.Bot.Name

	bs, ok := a.stats.BotHits[botName]
	if !ok {
		// Batch-allocate all nested maps for this bot on first encounter.
		bs = &BotStats{
			StatusCodes: make(map[int]int64),
			Category:    e.Bot.Category,
			Mobile:      e.Bot.Mobile,
		}
		a.stats.BotHits[botName] = bs
		a.botURLSets[botName] = make(map[uint64]struct{})
		a.botIPs[botName] = make(map[string]struct{})
		a.botHeatmaps[botName] = &[7][24]int64{}
		a.stats.BotDailyHits[botName] = make(map[string]int64)
		a.stats.BotHourlyHits[botName] = make(map[string]int64)
	}
	bs.Hits++
	bs.Bytes += e.Bytes
	bs.StatusCodes[e.Status]++

	if bs.FirstSeen == "" || ts < bs.FirstSeen {
		bs.FirstSeen = ts
	}
	if ts > bs.LastSeen {
		bs.LastSeen = ts
	}

	// Track unique URLs per bot (capped). Uses path hash (8 bytes) instead of
	// full string (~100 bytes) to reduce memory from ~11.6MB to ~800KB per bot.
	if !a.botURLCaps[botName] {
		a.pathHasher.Reset()
		a.pathHasher.WriteString(e.Path)
		h := a.pathHasher.Sum64()
		s := a.botURLSets[botName]
		s[h] = struct{}{}
		if len(s) > maxBotURLs {
			a.botURLCaps[botName] = true
		}
	}

	// Per-bot heatmap — use pre-computed hour/weekday when available.
	hour := e.Hour
	weekday := e.Weekday
	if hour == 0 && weekday == 0 && !e.Timestamp.IsZero() {
		hour = e.Timestamp.Hour()
		weekday = int(e.Timestamp.Weekday())
	}
	a.botHeatmaps[botName][weekday][hour]++

	// Track bot IPs for verification (cap at 10K per bot)
	if !a.botIPCaps[botName] && e.IP != "" {
		ips := a.botIPs[botName]
		ips[e.IP] = struct{}{}
		if len(ips) > maxBotIPs {
			a.botIPCaps[botName] = true
		}
	}

	// Bot daily + hourly hits (maps pre-allocated above)
	a.stats.BotDailyHits[botName][day]++
	if len(ts) >= 13 {
		a.stats.BotHourlyHits[botName][ts[:13]]++
	}

	if e.Bot.Mobile {
		a.mobileBots++
	}
	cat := e.Bot.Category
	if cat == "ai_training" || cat == "ai_search" || cat == "ai_user" {
		a.aiBots++
		if a.stats.AIBotTrends == nil {
			a.stats.AIBotTrends = make(map[string]map[string]int64)
		}
		if a.stats.AIBotTrends[botName] == nil {
			a.stats.AIBotTrends[botName] = make(map[string]int64)
		}
		a.stats.AIBotTrends[botName][day]++
	}
	if e.Status >= 400 {
		a.errorHits++
	}
}

func (a *aggregator) trackURL(e *LogEntry) {
	u, ok := a.urlHits[e.Path]
	if !ok {
		u = &urlAccum{}
		a.urlHits[e.Path] = u
	}
	u.hits++
	u.bytes += e.Bytes
	u.status = e.Status
	if e.Bot != nil {
		u.botHits++
		// Heavy-hitter tracking (Misra-Gries): correctly identifies the top bot
		// when it has >50% of hits (the common case in SEO — Googlebot dominates).
		if e.Bot.Name == u.topBot {
			u.topBotHits++
		} else if u.topBotHits == 0 {
			u.topBot = e.Bot.Name
			u.topBotHits = 1
		} else {
			u.topBotHits--
		}
	} else {
		u.humanHits++
	}
	// Batch-flush: when the map exceeds the threshold, drain to SQLite via
	// the callback. This caps memory at ~500K URLs × ~80 bytes = ~40 MB
	// regardless of how many unique URLs exist in the logs.
	if a.urlFlusher != nil && len(a.urlHits) >= a.urlFlushAt {
		a.flushURLs()
	}
}

// startAsyncFlusher spawns a background goroutine that processes URL batches
// from flushCh. Call waitFlushDone() to drain remaining batches.
func (a *aggregator) startAsyncFlusher() {
	a.flushCh = make(chan []URLStat, 8) // buffer 8 batches ahead to avoid blocking aggregator
	a.flushDone = make(chan struct{})
	go func() {
		defer close(a.flushDone)
		for batch := range a.flushCh {
			if err := a.urlFlusher(batch); err != nil && a.flushErr == nil {
				a.flushErr = err
			}
		}
	}()
}

// waitFlushDone closes the flush channel and blocks until all pending
// batches have been written to SQLite. Returns the first error, if any.
func (a *aggregator) waitFlushDone() error {
	if a.flushCh != nil {
		close(a.flushCh)
		<-a.flushDone
		a.flushCh = nil // prevent double-close panic
		return a.flushErr
	}
	return nil
}

// flushURLs drains the URL map into a batch and sends it to the async
// writer. Only URLs with bot traffic are persisted.
func (a *aggregator) flushURLs() {
	batch := make([]URLStat, 0, len(a.urlHits))
	for path, u := range a.urlHits {
		if u.botHits == 0 {
			continue // skip human-only URLs
		}
		batch = append(batch, URLStat{
			Path:      path,
			Hits:      u.hits,
			BotHits:   u.botHits,
			HumanHits: u.humanHits,
			TopBot:    u.topBot,
			Status:    u.status,
			Bytes:     u.bytes,
		})
	}
	if len(batch) > 0 {
		if a.flushCh != nil {
			a.flushCh <- batch // async: background goroutine writes to SQLite
		} else {
			_ = a.urlFlusher(batch) // sync fallback
		}
	}
	a.urlsFlushed += len(batch)
	// Reset map, keeping a reasonable pre-alloc.
	a.urlHits = make(map[string]*urlAccum, a.urlFlushAt)
}

// StreamBotURLsSorted iterates bot-hit URLs in sorted order (by path) and calls
// fn for each. Sorts only the keys ([]string) instead of materializing a full
// []URLStat slice — saves ~2.5GB for 26M URLs. Sorted order enables sequential
// B-tree inserts in WITHOUT ROWID tables.
func StreamBotURLsSorted(urlHits map[string]*urlAccum, fn func(URLStat) error) error {
	// Collect bot-hit keys only (strings are 16 bytes each vs 96 for URLStat).
	keys := make([]string, 0, len(urlHits)/2)
	for path, u := range urlHits {
		if u.botHits > 0 {
			keys = append(keys, path)
		}
	}
	sort.Strings(keys)
	for _, path := range keys {
		u := urlHits[path]
		if err := fn(URLStat{
			Path:      path,
			Hits:      u.hits,
			BotHits:   u.botHits,
			HumanHits: u.humanHits,
			TopBot:    u.topBot,
			Status:    u.status,
			Bytes:     u.bytes,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (a *aggregator) finalize() *LogStats {
	a.stats.DateRange = [2]string{a.firstTime, a.lastTime}

	// Set unique URL counts on bot stats
	for name, bs := range a.stats.BotHits {
		if s, ok := a.botURLSets[name]; ok {
			bs.UniqueURLs = len(s)
		}
	}

	// Build top URLs from whatever remains in the map (may be partial if
	// batch-flush was active — caller can override from the DB afterwards).
	if len(a.urlHits) > 0 {
		type kv struct {
			path string
			u    *urlAccum
		}
		items := make([]kv, 0, len(a.urlHits))
		for k, v := range a.urlHits {
			items = append(items, kv{k, v})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].u.hits > items[j].u.hits })
		if len(items) > maxTopURLs {
			items = items[:maxTopURLs]
		}
		a.stats.TopURLs = make([]URLStat, len(items))
		for i, item := range items {
			a.stats.TopURLs[i] = URLStat{
				Path:      item.path,
				Hits:      item.u.hits,
				BotHits:   item.u.botHits,
				HumanHits: item.u.humanHits,
				TopBot:    item.u.topBot,
				Status:    item.u.status,
				Bytes:     item.u.bytes,
			}
		}
	}

	// Crawl budget
	totalBot := a.stats.TotalHits - a.stats.HumanHits
	cb := &a.stats.CrawlBudget
	cb.TotalBotHits = totalBot
	// Count unique bot-hit URLs from the urlHits map (avoids a separate set).
	botURLCount := 0
	for _, u := range a.urlHits {
		if u.botHits > 0 {
			botURLCount++
		}
	}
	cb.UniqueURLsCrawled = botURLCount
	if totalBot > 0 {
		cb.CrawlEfficiency = float64(cb.UniqueURLsCrawled) / float64(totalBot)
		cb.ErrorRate = float64(a.errorHits) / float64(totalBot)
		cb.MobileCrawlShare = float64(a.mobileBots) / float64(totalBot)
		cb.AIBotShare = float64(a.aiBots) / float64(totalBot)
	}

	// Build bot heatmaps for top 5 bots
	a.stats.BotHeatmap = a.buildBotHeatmaps()

	// Crawl waste analysis
	a.stats.Waste = a.analyzeWaste(totalBot)

	return &a.stats
}

type wasteTopURL struct {
	path    string
	hits    int64
	botHits int64
}

func (a *aggregator) analyzeWaste(totalBotHits int64) *WasteAnalysis {
	wa := &WasteAnalysis{
		TotalBotHits: totalBotHits,
		ByType:       make(map[WasteType]*WasteEntry),
	}
	topByType := make(map[WasteType][]wasteTopURL)

	for path, u := range a.urlHits {
		wt := ClassifyWaste(path)
		if wt == "" {
			continue
		}
		e, ok := wa.ByType[wt]
		if !ok {
			e = &WasteEntry{Type: wt}
			wa.ByType[wt] = e
		}
		e.Hits += u.hits
		e.BotHits += u.botHits
		e.URLs++
		wa.WasteHits += u.botHits
		topByType[wt] = addWasteTopURL(topByType[wt], wasteTopURL{path: path, hits: u.hits, botHits: u.botHits})
	}

	for wt, top := range topByType {
		e := wa.ByType[wt]
		for _, item := range top {
			e.TopURLs = append(e.TopURLs, item.path)
		}
	}
	if totalBotHits > 0 {
		wa.WasteRatio = float64(wa.WasteHits) / float64(totalBotHits)
	}
	return wa
}

func addWasteTopURL(top []wasteTopURL, item wasteTopURL) []wasteTopURL {
	top = append(top, item)
	sort.Slice(top, func(i, j int) bool {
		if top[i].botHits != top[j].botHits {
			return top[i].botHits > top[j].botHits
		}
		if top[i].hits != top[j].hits {
			return top[i].hits > top[j].hits
		}
		return top[i].path < top[j].path
	})
	if len(top) > 5 {
		top = top[:5]
	}
	return top
}

func (a *aggregator) buildBotHeatmaps() map[string][7][24]int64 {
	// Get top 5 bots by hits
	type botHit struct {
		name string
		hits int64
	}
	var bots []botHit
	for name, bs := range a.stats.BotHits {
		bots = append(bots, botHit{name, bs.Hits})
	}
	sort.Slice(bots, func(i, j int) bool { return bots[i].hits > bots[j].hits })
	if len(bots) > 5 {
		bots = bots[:5]
	}
	result := make(map[string][7][24]int64, len(bots))
	for _, b := range bots {
		if hm, ok := a.botHeatmaps[b.name]; ok {
			result[b.name] = *hm
		}
	}
	return result
}

// MultiProgress describes per-file progress during a multi-file parse.
type MultiProgress struct {
	FileIndex  int
	Filename   string
	Processed  int64 // lines processed in this file
	BytesRead  int64 // bytes read from this file
	FileSize   int64 // total size of this file
	FilesTotal int
	FilesDone  int   // files that have finished parsing
	TotalBytes int64 // aggregate bytesRead across all files
	TotalSize  int64 // aggregate fileSize across all files
	TotalLines int64 // aggregate processed lines across all files
}

// MultiProgressFunc receives per-file progress events from AnalyzeMulti.
type MultiProgressFunc func(MultiProgress)

// FileResult holds the per-file outcome of a multi-file analysis.
type FileResult struct {
	Path     string
	Filename string
	Size     int64
	Lines    int64
	Format   Format
	Error    string
}

// MultiAnalysisResult is the combined output of AnalyzeMulti.
type MultiAnalysisResult struct {
	Stats      *LogStats
	Format     Format
	Files      []FileResult
	BotIPs     map[string]map[string]struct{}
	AllURLs    []URLStat                          // populated only when no flusher was provided (legacy)
	StreamURLs func(fn func(URLStat) error) error // streams sorted bot URLs without materializing; nil if flusher was used
}

// AnalyzeMulti parses multiple log files in parallel with a shared aggregator.
// Each worker gets its own ParseState (no global contention); entries flow
// through a buffered channel to a single aggregator goroutine (which mutates
// LogStats without needing locks).
//
// workers ≤ 0 defaults to min(len(paths), GOMAXPROCS). If all files fail,
// returns the first error; partial failures surface via FileResult.Error.
func AnalyzeMulti(paths []string, formatHint Format, workers int, onProgress MultiProgressFunc, flusher URLStatsFlusher) (*MultiAnalysisResult, error) {
	if len(paths) == 0 {
		return &MultiAnalysisResult{Stats: newAggregator().finalize()}, nil
	}

	// Suppress GC during the parse-heavy phase. The aggregator's maps
	// create millions of pointer-bearing entries that dominate GC scan
	// time (58%+ CPU in profiles). With GOMEMLIMIT set, the runtime still
	// triggers GC when approaching the memory cap, preventing OOM.
	// GC is restored on return; no explicit runtime.GC() — forcing a full
	// scan of millions of map entries would block for minutes.
	prevGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(prevGC)

	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	if workers > len(paths) {
		workers = len(paths)
	}
	if workers < 1 {
		workers = 1
	}

	// Pre-stat all files so progress aggregation has a denominator.
	fileResults := make([]FileResult, len(paths))
	var totalSize int64
	for i, p := range paths {
		fi, err := statSize(p)
		fileResults[i] = FileResult{Path: p, Filename: filepath.Base(p), Size: fi}
		if err == nil {
			totalSize += fi
		}
	}

	// Per-file progress trackers (reported cumulatively across files).
	progressMu := sync.Mutex{}
	bytesPerFile := make([]int64, len(paths))
	linesPerFile := make([]int64, len(paths))
	var filesDone int

	aggregateProgress := func(mp MultiProgress) {
		progressMu.Lock()
		bytesPerFile[mp.FileIndex] = mp.BytesRead
		linesPerFile[mp.FileIndex] = mp.Processed
		var totalBytes, totalLines int64
		for i := range bytesPerFile {
			totalBytes += bytesPerFile[i]
			totalLines += linesPerFile[i]
		}
		mp.FilesTotal = len(paths)
		mp.FilesDone = filesDone
		mp.TotalBytes = totalBytes
		mp.TotalSize = totalSize
		mp.TotalLines = totalLines
		progressMu.Unlock()
		if onProgress != nil {
			onProgress(mp)
		}
	}

	// Buffered channel: parsers write entries, single aggregator consumes.
	entryCh := make(chan *LogEntry, 65536)

	// Worker semaphore bounds concurrency.
	sem := make(chan struct{}, workers)
	var workerWg sync.WaitGroup
	for i := range paths {
		workerWg.Add(1)
		sem <- struct{}{}
		go func(idx int, path string) {
			defer workerWg.Done()
			defer func() { <-sem }()

			state := &ParseState{}
			filename := filepath.Base(path)

			format, lines, err := ParseFileWithState(path, formatHint, state,
				func(e *LogEntry) {
					// Pre-compute time fields in parallel parser goroutine.
					if !e.Timestamp.IsZero() {
						e.TSFormatted = e.Timestamp.Format("2006-01-02T15:04:05Z")
						e.Hour = e.Timestamp.Hour()
						e.Weekday = int(e.Timestamp.Weekday())
					}
					entryCh <- e
				},
				func(processed, bytesRead, fileSize int64) {
					aggregateProgress(MultiProgress{
						FileIndex: idx, Filename: filename,
						Processed: processed, BytesRead: bytesRead, FileSize: fileSize,
					})
				})

			progressMu.Lock()
			filesDone++
			fileResults[idx].Format = format
			fileResults[idx].Lines = lines
			if err != nil {
				fileResults[idx].Error = err.Error()
			}
			progressMu.Unlock()

			// Final per-file progress tick with definitive byte count.
			fileSize := fileResults[idx].Size
			aggregateProgress(MultiProgress{
				FileIndex: idx, Filename: filename,
				Processed: lines, BytesRead: fileSize, FileSize: fileSize,
			})
		}(i, paths[i])
	}

	// Single aggregator goroutine — no locks needed because only one writer.
	agg := newAggregator()
	agg.urlFlusher = flusher
	if flusher != nil {
		agg.startAsyncFlusher()
	}
	aggDone := make(chan struct{})
	go func() {
		for e := range entryCh {
			agg.add(e)
		}
		close(aggDone)
	}()

	workerWg.Wait()
	close(entryCh)
	<-aggDone

	// Drain remaining URLs, wait for async writes.
	if flusher != nil {
		agg.flushURLs()
		if flushErr := agg.waitFlushDone(); flushErr != nil {
			log.Printf("[logs] async URL flush error: %v", flushErr)
		}
	}
	stats := agg.finalize()
	var totalLines int64
	var firstFormat Format = FormatUnknown
	var firstErr string
	successCount := 0
	for _, fr := range fileResults {
		totalLines += fr.Lines
		if fr.Format != "" && fr.Format != FormatUnknown && firstFormat == FormatUnknown {
			firstFormat = fr.Format
		}
		if fr.Error == "" {
			successCount++
		} else if firstErr == "" {
			firstErr = fr.Error
		}
	}
	stats.TotalLines = totalLines

	result := &MultiAnalysisResult{
		Stats:  stats,
		Format: firstFormat,
		Files:  fileResults,
		BotIPs: agg.botIPs,
	}
	// When no flusher was provided, provide a streaming function to iterate
	// sorted bot URLs without materializing a 2.5GB []URLStat slice.
	if flusher == nil {
		urlHits := agg.urlHits // capture for closure
		result.StreamURLs = func(fn func(URLStat) error) error {
			return StreamBotURLsSorted(urlHits, fn)
		}
	}
	// Only surface an error if ALL files failed — partial failures are
	// represented in FileResult.Error so the UI can highlight them.
	if successCount == 0 && firstErr != "" {
		return result, parseError(firstErr)
	}
	return result, nil
}

// mergeAggregators combines multiple per-file aggregators into one.
// Used by the parallel per-file aggregation strategy to merge results
// after all files have been parsed independently.
func mergeAggregators(aggs []*aggregator) *aggregator {
	if len(aggs) == 0 {
		return newAggregator()
	}
	if len(aggs) == 1 {
		return aggs[0]
	}
	dst := aggs[0]
	for _, src := range aggs[1:] {
		// Scalar counters
		dst.stats.TotalHits += src.stats.TotalHits
		dst.stats.HumanHits += src.stats.HumanHits
		dst.stats.TotalBytes += src.stats.TotalBytes
		dst.mobileBots += src.mobileBots
		dst.aiBots += src.aiBots
		dst.errorHits += src.errorHits

		// Date range
		if src.firstTime != "" && (dst.firstTime == "" || src.firstTime < dst.firstTime) {
			dst.firstTime = src.firstTime
		}
		if src.lastTime > dst.lastTime {
			dst.lastTime = src.lastTime
		}

		// Hourly hits (fixed array)
		for h := 0; h < 24; h++ {
			dst.stats.HourlyHits[h] += src.stats.HourlyHits[h]
		}

		// Heatmap (7×24)
		for d := 0; d < 7; d++ {
			for h := 0; h < 24; h++ {
				dst.stats.Heatmap[d][h] += src.stats.Heatmap[d][h]
			}
		}

		// Status codes
		for k, v := range src.stats.StatusCodes {
			dst.stats.StatusCodes[k] += v
		}
		for k, v := range src.stats.StatusGroups {
			dst.stats.StatusGroups[k] += v
		}

		// Daily hits
		for k, v := range src.stats.DailyHits {
			dst.stats.DailyHits[k] += v
		}

		// Hourly timeline
		for k, v := range src.stats.HourlyHitsTimeline {
			dst.stats.HourlyHitsTimeline[k] += v
		}

		// Bot stats
		for name, sbs := range src.stats.BotHits {
			dbs, ok := dst.stats.BotHits[name]
			if !ok {
				dst.stats.BotHits[name] = sbs
				continue
			}
			dbs.Hits += sbs.Hits
			dbs.Bytes += sbs.Bytes
			for sc, cnt := range sbs.StatusCodes {
				dbs.StatusCodes[sc] += cnt
			}
			if sbs.FirstSeen != "" && (dbs.FirstSeen == "" || sbs.FirstSeen < dbs.FirstSeen) {
				dbs.FirstSeen = sbs.FirstSeen
			}
			if sbs.LastSeen > dbs.LastSeen {
				dbs.LastSeen = sbs.LastSeen
			}
		}

		// Bot daily hits
		for bot, days := range src.stats.BotDailyHits {
			if dst.stats.BotDailyHits[bot] == nil {
				dst.stats.BotDailyHits[bot] = days
			} else {
				for d, v := range days {
					dst.stats.BotDailyHits[bot][d] += v
				}
			}
		}

		// Bot hourly hits
		for bot, hours := range src.stats.BotHourlyHits {
			if dst.stats.BotHourlyHits[bot] == nil {
				dst.stats.BotHourlyHits[bot] = hours
			} else {
				for h, v := range hours {
					dst.stats.BotHourlyHits[bot][h] += v
				}
			}
		}

		// Bot URL sets (merge unique URLs per bot)
		for bot, urls := range src.botURLSets {
			if dst.botURLSets[bot] == nil {
				dst.botURLSets[bot] = urls
			} else if !dst.botURLCaps[bot] {
				for u := range urls {
					dst.botURLSets[bot][u] = struct{}{}
					if len(dst.botURLSets[bot]) > maxBotURLs {
						dst.botURLCaps[bot] = true
						break
					}
				}
			}
		}

		// Bot heatmaps
		for bot, shm := range src.botHeatmaps {
			dhm := dst.botHeatmaps[bot]
			if dhm == nil {
				dst.botHeatmaps[bot] = shm
			} else {
				for d := 0; d < 7; d++ {
					for h := 0; h < 24; h++ {
						dhm[d][h] += shm[d][h]
					}
				}
			}
		}

		// Bot IPs
		for bot, ips := range src.botIPs {
			if dst.botIPs[bot] == nil {
				dst.botIPs[bot] = ips
			} else if !dst.botIPCaps[bot] {
				for ip := range ips {
					dst.botIPs[bot][ip] = struct{}{}
					if len(dst.botIPs[bot]) > maxBotIPs {
						dst.botIPCaps[bot] = true
						break
					}
				}
			}
		}

		// AI bot trends
		if src.stats.AIBotTrends != nil {
			if dst.stats.AIBotTrends == nil {
				dst.stats.AIBotTrends = src.stats.AIBotTrends
			} else {
				for bot, days := range src.stats.AIBotTrends {
					if dst.stats.AIBotTrends[bot] == nil {
						dst.stats.AIBotTrends[bot] = days
					} else {
						for d, v := range days {
							dst.stats.AIBotTrends[bot][d] += v
						}
					}
				}
			}
		}

		// URL hits — merge remaining (unflushed) URL maps.
		// Note: src pointers are consumed once and never reused after merge.
		for path, su := range src.urlHits {
			du, ok := dst.urlHits[path]
			if !ok {
				dst.urlHits[path] = su
			} else {
				// Keep status from higher-traffic source (better proxy than arbitrary last-write).
				if su.hits > du.hits {
					du.status = su.status
				}
				du.hits += su.hits
				du.botHits += su.botHits
				du.humanHits += su.humanHits
				du.bytes += su.bytes
				// Merge heavy-hitter trackers
				if su.topBotHits > du.topBotHits {
					du.topBot = su.topBot
					du.topBotHits = su.topBotHits
				}
			}
		}
		dst.urlsFlushed += src.urlsFlushed
	}
	return dst
}

func statSize(path string) (int64, error) {
	f, err := openSized(path)
	return f, err
}

func statusGroup(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	case code >= 200:
		return "2xx"
	default:
		return "other"
	}
}
