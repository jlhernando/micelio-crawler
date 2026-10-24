package scheduler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIPInRanges(t *testing.T) {
	for _, c := range []struct {
		ip     string
		ranges []string
		want   bool
	}{
		{"10.1.2.3", []string{"10.0.0.0/8"}, true},
		{"8.8.8.8", []string{"10.0.0.0/8"}, false},
		{"203.0.113.7", []string{"203.0.113.7"}, true},
		{"2001:db8::1", []string{"2001:db8::/32"}, true},
		{"nonsense", []string{"10.0.0.0/8"}, false},
	} {
		if got := ipInRanges(c.ip, c.ranges); got != c.want {
			t.Errorf("ipInRanges(%s, %v) = %v", c.ip, c.ranges, got)
		}
	}
	if err := (&Precheck{EgressCIDRs: []string{"not-a-range"}}).Validate(); err == nil {
		t.Error("invalid range accepted")
	}
}

func TestPrecheckRun(t *testing.T) {
	oldRun, oldURLs := runCommand, egressIPURLs
	defer func() { runCommand, egressIPURLs = oldRun, oldURLs }()
	runCommand = func(ctx context.Context, name string, args ...string) (string, error) {
		if args[len(args)-1] == "exit 1" {
			return "nope", errors.New("exit status 1")
		}
		return "", nil
	}
	ipSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("203.0.113.9\n")) }))
	defer ipSrv.Close()
	egressIPURLs = []string{ipSrv.URL}

	if r := (&Precheck{EgressCIDRs: []string{"203.0.113.0/24"}}).Run(context.Background()); !r.OK || r.EgressIP != "203.0.113.9" {
		t.Errorf("allowed egress = %+v", r)
	}
	if r := (&Precheck{EgressCIDRs: []string{"10.0.0.0/8"}}).Run(context.Background()); r.OK {
		t.Errorf("wrong egress passed: %+v", r)
	}
	if r := (&Precheck{Command: "exit 1"}).Run(context.Background()); r.OK {
		t.Errorf("failing command passed: %+v", r)
	}
	if r := (&Precheck{Command: "true"}).Run(context.Background()); !r.OK {
		t.Errorf("passing command failed: %+v", r)
	}
}

// fakeRunner records pause/resume signals until the context ends or it is told to finish.
type fakeRunner struct{ events chan string }

func (f fakeRunner) Run(ctx context.Context, cfg Config, ctl *RunControl) RunResult {
	for {
		select {
		case <-ctx.Done():
			return RunResult{Status: "success"}
		case <-ctl.Changed():
			paused, _ := ctl.Paused()
			if paused {
				f.events <- "paused"
			} else {
				f.events <- "resumed"
				return RunResult{Status: "success", Pages: 1}
			}
		}
	}
}

func TestMonitorPausesAndResumes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	old := minCheckInterval
	minCheckInterval = 50 * time.Millisecond
	defer func() { minCheckInterval = old }()
	flag := filepath.Join(t.TempDir(), "network-ok")
	os.WriteFile(flag, nil, 0o644)
	ev := make(chan string, 4)
	s, err := New(Config{URL: "https://example.com/", CronExpr: "@daily", OutputDir: t.TempDir(),
		Precheck: &Precheck{Command: "test -f " + flag, IntervalSec: 1}, Runner: fakeRunner{ev}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.runOnce(); close(done) }()
	time.Sleep(100 * time.Millisecond)
	os.Remove(flag) // network check starts failing
	if got := waitEvent(t, ev); got != "paused" {
		t.Fatalf("event = %s", got)
	}
	if st := s.GetState(); !st.Paused || !st.Running {
		t.Errorf("state = %+v", st)
	}
	os.WriteFile(flag, nil, 0o644) // passes again
	if got := waitEvent(t, ev); got != "resumed" {
		t.Fatalf("event = %s", got)
	}
	<-done
	if st := s.GetState(); st.LastStatus != "success" || st.Running || st.Paused {
		t.Errorf("final state = %+v", st)
	}
}

func TestRunSkippedWhenCheckFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, err := New(Config{URL: "https://example.com/", CronExpr: "@daily", OutputDir: t.TempDir(),
		Precheck: &Precheck{Command: "exit 3"}, Runner: fakeRunner{make(chan string, 1)}})
	if err != nil {
		t.Fatal(err)
	}
	s.runOnce()
	if st := s.GetState(); st.LastStatus != "skipped" || st.TotalRuns != 0 || st.LastReason == "" {
		t.Errorf("state = %+v", st)
	}
}

func waitEvent(t *testing.T, ev chan string) string {
	select {
	case e := <-ev:
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for monitor")
		return ""
	}
}

func TestDescribeCronMinutes(t *testing.T) {
	if got := DescribeCron("* * * * *"); got != "Every minute" {
		t.Errorf("got %q", got)
	}
	if got := DescribeCron("*/15 * * * *"); got != "Every 15 minutes" {
		t.Errorf("got %q", got)
	}
}
