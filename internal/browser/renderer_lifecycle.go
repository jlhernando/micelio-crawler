package browser

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
)

func newRendererProcess(opts []chromedp.ExecAllocatorOption) *rendererProcess {
	lifetime, lifetimeCancel := context.WithCancel(context.Background())
	allocator, allocCancel := chromedp.NewExecAllocator(lifetime, opts...)
	browser, browserCancel := chromedp.NewContext(allocator)
	return &rendererProcess{browserCtx: browser, browserCancel: browserCancel,
		lifetimeCancel: lifetimeCancel, allocCancel: allocCancel, startReady: make(chan struct{})}
}

// startBrowser initializes the process lifetime, never a caller's operation.
func (r *rendererProcess) startBrowser(timeout time.Duration) {
	expired := make(chan struct{})
	timer := time.AfterFunc(timeout, func() {
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
// Each shared process starts once, including when callers recover after a crash.
// Every tab has its own incognito browser context, disposed when it closes.
func (r *Renderer) newTask(request context.Context, timeout time.Duration) (context.Context, context.CancelFunc, error) {
	waiting, waitCancel := context.WithTimeout(request, timeout)
	defer waitCancel()
	if err := waiting.Err(); err != nil {
		return nil, nil, err
	}
	r.lifecycleMu.Lock()
	if r.closed || waiting.Err() != nil {
		r.lifecycleMu.Unlock()
		if err := waiting.Err(); err != nil {
			return nil, nil, err
		}
		return nil, nil, context.Canceled
	}
	process := r.rendererProcess
	if process.browserCtx.Err() != nil && r.newProcess != nil {
		select {
		case <-process.startReady:
			// A browser that started successfully may die independently of its
			// callers. Reap it before replacing it. Startup/configuration errors
			// stay visible rather than launching a new broken process per page.
			if process.startErr == nil {
				process.close()
				process = r.newProcess()
				r.rendererProcess = process
			}
		default:
		}
	}
	process.startOnce.Do(func() { go process.startBrowser(r.config.Timeout) })
	r.lifecycleMu.Unlock()
	select {
	case <-waiting.Done():
		return nil, nil, waiting.Err()
	case <-process.startReady:
	}
	if process.startErr != nil {
		return nil, nil, fmt.Errorf("start Chrome: %w", process.startErr)
	}
	if err := process.browserCtx.Err(); err != nil {
		return nil, nil, err
	}
	tab, tabCancel := chromedp.NewContext(process.browserCtx, chromedp.WithNewBrowserContext())
	deadline, _ := waiting.Deadline()
	operation, operationCancel := context.WithDeadline(tab, deadline)
	stop := context.AfterFunc(request, operationCancel)
	return operation, func() {
		stop()
		operationCancel()
		tabCancel()
	}, nil
}
