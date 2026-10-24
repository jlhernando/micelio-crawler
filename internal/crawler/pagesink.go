package crawler

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// Page sink tuning. A batch of sinkBatchSize pages is one SQLite
// transaction; sinkFlushInterval bounds how long a page can wait in a
// partial batch (so a paused crawl, or a crash, loses at most this much).
const (
	sinkBatchSize     = 200
	sinkQueueSize     = 512
	sinkFlushInterval = 500 * time.Millisecond
)

// batchPageStore is implemented by storage.CrawlStore. Stores without it
// (test fakes) get one InsertPage call per page.
type batchPageStore interface {
	InsertEncodedPages(batch []storage.EncodedPage) error
}

// sinkItem is one page ready for output. Workers fill it (JSON encoding,
// zstd compression) before handing it over, so the sink goroutine only
// writes bytes and runs SQL.
type sinkItem struct {
	page   *types.PageData      // CSV output and stores without batching
	line   []byte               // JSONL line (no newline)
	enc    *storage.EncodedPage // crawl DB record
	html   [2]string            // source/rendered HTML for stores without batching
	flushC chan struct{}        // flush marker: closed once everything before it is written
}

// pageSink is the single writer for the output file and crawl DB during a
// crawl. It replaces per-page writes under the session lock: workers no
// longer serialise on JSON encoding, compression and 1-3 SQLite commits.
type pageSink struct {
	writer    *storage.ResultWriter
	db        CrawlStore
	batchDB   batchPageStore
	errCount  *atomic.Int32
	ch        chan sinkItem
	done      chan struct{}
	closeOnce sync.Once
	batch     []storage.EncodedPage
}

func newPageSink(writer *storage.ResultWriter, db CrawlStore, errCount *atomic.Int32) *pageSink {
	if writer == nil && db == nil {
		return nil
	}
	ps := &pageSink{
		writer:   writer,
		db:       db,
		errCount: errCount,
		ch:       make(chan sinkItem, sinkQueueSize),
		done:     make(chan struct{}),
	}
	if b, ok := db.(batchPageStore); ok {
		ps.batchDB = b
	}
	go ps.run()
	return ps
}

// encode prepares a page for the sink. Runs in the caller's goroutine.
// The page must not be modified afterwards (it is read by the sink for
// CSV output); callers pass a shallow copy.
func (ps *pageSink) encode(page *types.PageData, sourceHTML, renderedHTML string) sinkItem {
	it := sinkItem{}
	needJSON := (ps.writer != nil && ps.writer.IsJSONL()) || ps.batchDB != nil
	var data []byte
	if needJSON {
		var err error
		data, err = json.Marshal(page)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\n  [warn] encode page %s: %v\n", page.URL, err)
			ps.errCount.Add(1)
			return sinkItem{page: page}
		}
	}
	if ps.writer != nil {
		if ps.writer.IsJSONL() {
			it.line = data
		} else {
			it.page = page
		}
	}
	if ps.db != nil {
		if ps.batchDB != nil {
			enc, err := storage.EncodePage(page, data)
			if err == nil && (sourceHTML != "" || renderedHTML != "") {
				err = enc.AttachHTML(sourceHTML, renderedHTML)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "\n  [warn] encode page %s: %v\n", page.URL, err)
			} else {
				it.enc = &enc
			}
		} else {
			it.page = page
			it.html = [2]string{sourceHTML, renderedHTML}
		}
	}
	return it
}

// put queues an encoded page. Blocks when the sink is sinkQueueSize behind,
// which applies back-pressure to the workers instead of growing memory.
func (ps *pageSink) put(it sinkItem) {
	ps.ch <- it
}

// Flush blocks until every page queued before the call is written and
// committed.
func (ps *pageSink) Flush() {
	if ps == nil {
		return
	}
	c := make(chan struct{})
	ps.ch <- sinkItem{flushC: c}
	<-c
}

// Close writes everything queued and stops the sink. No put may follow.
// Safe to call more than once.
func (ps *pageSink) Close() {
	if ps == nil {
		return
	}
	ps.closeOnce.Do(func() {
		close(ps.ch)
		<-ps.done
	})
}

func (ps *pageSink) run() {
	defer close(ps.done)
	ticker := time.NewTicker(sinkFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case it, ok := <-ps.ch:
			if !ok {
				ps.commit()
				ps.flushWriter()
				return
			}
			if it.flushC != nil {
				ps.commit()
				ps.flushWriter()
				close(it.flushC)
				continue
			}
			ps.write(it)
			if len(ps.batch) >= sinkBatchSize {
				ps.commit()
			}
		case <-ticker.C:
			ps.commit()
		}
	}
}

func (ps *pageSink) write(it sinkItem) {
	if ps.writer != nil {
		var err error
		if it.line != nil {
			err = ps.writer.WriteJSONLine(it.line)
		} else if it.page != nil {
			err = ps.writer.Write(it.page)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "\n  [warn] write error: %v\n", err)
			ps.errCount.Add(1)
		}
	}
	if ps.db == nil {
		return
	}
	if it.enc != nil {
		ps.batch = append(ps.batch, *it.enc)
		return
	}
	if ps.batchDB == nil && it.page != nil {
		if err := ps.db.InsertPage(it.page); err != nil {
			fmt.Fprintf(os.Stderr, "\n  [warn] db write error: %v\n", err)
		}
		if it.html[0] != "" || it.html[1] != "" {
			if err := ps.db.SavePageHTML(it.page.URL, it.html[0], it.html[1]); err != nil {
				fmt.Fprintf(os.Stderr, "\n  [warn] save html: %v\n", err)
			}
		}
	}
}

// commit writes the pending DB batch in one transaction.
func (ps *pageSink) commit() {
	if len(ps.batch) == 0 {
		return
	}
	if err := ps.batchDB.InsertEncodedPages(ps.batch); err != nil {
		fmt.Fprintf(os.Stderr, "\n  [warn] db batch write (%d pages): %v\n", len(ps.batch), err)
	}
	clear(ps.batch) // drop references to the compressed blobs
	ps.batch = ps.batch[:0]
}

func (ps *pageSink) flushWriter() {
	if ps.writer == nil {
		return
	}
	if err := ps.writer.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "\n  [warn] flush output: %v\n", err)
	}
}
