package browser

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestRendererCloseLetsChromeExitNormally(t *testing.T) {
	lifetime, lifetimeCancel := context.WithCancel(context.Background())
	var forced atomic.Bool
	var command *exec.Cmd
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts, chromedp.Flag("no-sandbox", true), chromedp.ModifyCmdFunc(func(cmd *exec.Cmd) {
		command = cmd
		kill := cmd.Cancel
		cmd.Cancel = func() error {
			forced.Store(true)
			return kill()
		}
	}))
	alloc, allocCancel := chromedp.NewExecAllocator(lifetime, opts...)
	browser, browserCancel := chromedp.NewContext(alloc)
	r := &Renderer{browserCtx: browser, browserCancel: browserCancel, lifetimeCancel: lifetimeCancel, allocCancel: allocCancel, startReady: make(chan struct{}), config: RenderConfig{Timeout: 15 * time.Second}}
	t.Cleanup(r.Close)
	if _, err := r.RunSnippet(context.Background(), "about:blank", "", "1", nil); err != nil {
		if strings.Contains(err.Error(), "executable file not found") {
			t.Skip("Chrome not installed")
		}
		t.Fatal(err)
	}
	r.Close()
	if forced.Load() {
		t.Error("Close killed Chrome instead of letting it finish shutdown")
	}
	if command == nil || command.ProcessState == nil || !command.ProcessState.Success() {
		t.Fatalf("Chrome did not exit normally: %+v", command)
	}
}

func TestRendererCloseForcesUnresponsiveChrome(t *testing.T) {
	if _, err := exec.LookPath("kill"); err != nil {
		t.Skip("process signals unavailable")
	}
	r := newTestRenderer(t, RenderConfig{Timeout: 15 * time.Second})
	process := rendererBrowser(t, r).Process()
	if err := exec.Command("kill", "-STOP", strconv.Itoa(process.Pid)).Run(); err != nil {
		t.Fatalf("pause owned Chrome: %v", err)
	}
	// If the test fails before Close kills it, let cleanup reach the browser.
	t.Cleanup(func() { _ = exec.Command("kill", "-CONT", strconv.Itoa(process.Pid)).Run() })
	done := make(chan struct{})
	go func() { r.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung on unresponsive Chrome")
	}
	if process.Signal(syscall.Signal(0)) == nil {
		t.Fatal("unresponsive Chrome remained alive")
	}
}

type shutdownProcess struct {
	parent int
	rss    int
	name   string
}

func shutdownProcesses(t *testing.T) map[int]shutdownProcess {
	t.Helper()
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,rss=,comm=").Output()
	if err != nil {
		t.Fatalf("read process tree: %v", err)
	}
	rows := make(map[int]shutdownProcess)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, e1 := strconv.Atoi(fields[0])
		parent, e2 := strconv.Atoi(fields[1])
		rss, e3 := strconv.Atoi(fields[2])
		if e1 != nil || e2 != nil || e3 != nil {
			t.Fatalf("invalid process row: %q", line)
		}
		rows[pid] = shutdownProcess{parent, rss, strings.Join(fields[3:], " ")}
	}
	return rows
}

// Checking only Chrome's main process misses helpers orphaned by a hard kill.
// Capture descendants while they still have a parent, then check immediately
// after Close, as the benchmark supervisor does when the crawl driver exits.
func TestRendererCloseJoinsBrowserHelpers(t *testing.T) {
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("process inspection unavailable")
	}
	for i := 0; i < 5; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			r := newTestRenderer(t, RenderConfig{Timeout: 15 * time.Second})
			if _, err := r.RunSnippet(context.Background(), "about:blank", "", "document.body.innerHTML = '<canvas></canvas>'; document.querySelector('canvas').getContext('2d').fillRect(0, 0, 10, 10); 'ready'", nil); err != nil {
				t.Fatal(err)
			}
			rows := shutdownProcesses(t)
			owned := map[int]bool{rendererBrowser(t, r).Process().Pid: true}
			for changed := true; changed; {
				changed = false
				for pid, row := range rows {
					if owned[row.parent] && !owned[pid] {
						owned[pid], changed = true, true
					}
				}
			}
			if len(owned) < 2 {
				t.Fatal("no Chrome helpers observed; shutdown test would be incomplete")
			}
			r.Close()
			remaining := shutdownProcesses(t)
			for pid := range owned {
				if row, ok := remaining[pid]; ok && row.rss > 0 {
					t.Errorf("owned process survived Close: pid=%d parent=%d rssKiB=%d command=%s", pid, row.parent, row.rss, row.name)
				}
			}
		})
	}
}
