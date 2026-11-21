package browser

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
)

// startBrowser initializes the lifetime context, never an operation's context.
// NewContext on an uninitialized allocator would otherwise launch Chrome for
// each page. A caller cancelling its operation must not own this process.
func (r *Renderer) startBrowser() {
	expired := make(chan struct{})
	timer := time.AfterFunc(r.config.Timeout, func() {
		r.lifetimeCancel()
		close(expired)
	})
	err := chromedp.Do(r.browserCtx)
	if !timer.Stop() {
		<-expired
		err = context.DeadlineExceeded
	}
	if err == nil {
		err = r.browserCtx.Err()
	}
	if err != nil {
		r.lifetimeCancel()
	}
	r.startErr = err
	close(r.startReady)
}

// newTask returns an operation bounded by both the request and its timeout.
// The shared browser starts once, including when first calls arrive together.
// Every tab has its own incognito browser context, disposed when it closes.
func (r *Renderer) newTask(request context.Context, timeout time.Duration) (context.Context, context.CancelFunc, error) {
	waiting, waitCancel := context.WithTimeout(request, timeout)
	defer waitCancel()
	if err := waiting.Err(); err != nil {
		return nil, nil, err
	}
	if err := r.browserCtx.Err(); err != nil {
		return nil, nil, err
	}
	r.startOnce.Do(func() { go r.startBrowser() })
	select {
	case <-waiting.Done():
		return nil, nil, waiting.Err()
	case <-r.startReady:
	}
	if r.startErr != nil {
		return nil, nil, fmt.Errorf("start Chrome: %w", r.startErr)
	}
	if err := r.browserCtx.Err(); err != nil {
		return nil, nil, err
	}
	tab, tabCancel := chromedp.NewContext(r.browserCtx, chromedp.WithNewBrowserContext())
	deadline, _ := waiting.Deadline()
	operation, operationCancel := context.WithDeadline(tab, deadline)
	stop := context.AfterFunc(request, operationCancel)
	return operation, func() {
		stop()
		operationCancel()
		tabCancel()
	}, nil
}
