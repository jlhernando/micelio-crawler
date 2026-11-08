package logs

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAnalyzeMultiBackpressure(t *testing.T) {
	if os.Getenv("MICELIO_TEST_MULTI_CHILD") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAnalyzeMultiBackpressure$")
		cmd.Env = append(os.Environ(), "MICELIO_TEST_MULTI_CHILD=1")
		body, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("files>workers failed/hung: %v\n%s", err, body)
		}
		return
	}
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "first.log"), filepath.Join(dir, "second.log")}
	writeBenchLog(t, paths[0], 70000, 1000, false)
	writeBenchLog(t, paths[1], 17, 10, false)
	r, err := AnalyzeMulti(paths, "", 1, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Stats.TotalLines != 70017 || r.Stats.TotalHits != 70017 || len(r.Files) != 2 {
		t.Fatalf("rows=%d hits=%d files=%d", r.Stats.TotalLines, r.Stats.TotalHits, len(r.Files))
	}
	for _, f := range r.Files {
		if f.Error != "" {
			t.Fatal(f.Error)
		}
	}
}
func TestPublicAnalysisPropagatesFlushError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.log")
	writeBenchLog(t, path, 10, 10, false)
	failure := errors.New("storage offline")
	flush := func([]URLStat) error { return failure }
	_, err := AnalyzeFull(path, "", nil, flush)
	if !errors.Is(err, failure) {
		t.Fatalf("single-file error=%v", err)
	}
	_, err = AnalyzeMulti([]string{path}, "", 1, nil, flush)
	if !errors.Is(err, failure) {
		t.Fatalf("multi-file error=%v", err)
	}
}
func TestSlowFlusherAppliesBackpressureWithoutLoss(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWriter := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseWriter()
	var batches, rows, hits atomic.Int64
	a := newAggregator()
	a.urlFlushAt = 2
	a.urlFlusher = func(batch []URLStat) error {
		if batches.Add(1) == 1 {
			close(started)
			<-release
		}
		rows.Add(int64(len(batch)))
		for _, u := range batch {
			hits.Add(u.Hits)
		}
		return nil
	}
	a.startAsyncFlusher()
	bot := &BotInfo{Name: "TestBot", Category: "search_engine"}
	add := func(i int) { a.add(&LogEntry{Path: "/p/" + strconv.Itoa(i), Status: 200, Bytes: 10, Bot: bot}) }
	add(0)
	add(1)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("flusher did not start")
	}
	add(2)
	add(3)
	beforeThird := make(chan struct{})
	done := make(chan struct{})
	go func() { add(4); close(beforeThird); add(5); close(done) }()
	select {
	case <-beforeThird:
	case <-time.After(5 * time.Second):
		t.Fatal("producer did not reach the pending-write limit")
	}
	select {
	case <-done:
		releaseWriter()
		t.Fatal("producer bypassed full pending-write queue")
	case <-time.After(20 * time.Millisecond):
	}
	releaseWriter()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("producer did not resume")
	}
	if err := a.waitFlushDone(); err != nil {
		t.Fatal(err)
	}
	if batches.Load() != 3 || rows.Load() != 6 || hits.Load() != 6 {
		t.Fatalf("batches=%d rows=%d hits=%d", batches.Load(), rows.Load(), hits.Load())
	}
}
