package alerts

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// fakeSMTP is a minimal SMTP server that records one session.
type fakeSMTP struct {
	addr     string
	starttls bool
	mu       sync.Mutex
	auth     string
	from     string
	to       []string
	data     string
}

func startFakeSMTP(t *testing.T, starttls bool) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f := &fakeSMTP{addr: ln.Addr().String(), starttls: starttls}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	say := func(s string) { fmt.Fprintf(c, "%s\r\n", s) }
	say("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			if f.starttls {
				say("250-fake")
				say("250-STARTTLS")
			} else {
				say("250-fake")
			}
			say("250 AUTH PLAIN")
		case strings.HasPrefix(cmd, "AUTH PLAIN"):
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN"):]))
			f.mu.Lock()
			f.auth = string(raw)
			f.mu.Unlock()
			say("235 ok")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			f.mu.Lock()
			f.from = strings.Trim(line[len("MAIL FROM:"):], "<> ")
			f.mu.Unlock()
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			f.mu.Lock()
			f.to = append(f.to, strings.Trim(line[len("RCPT TO:"):], "<> "))
			f.mu.Unlock()
			say("250 ok")
		case cmd == "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func (f *fakeSMTP) port() int {
	_, p, _ := net.SplitHostPort(f.addr)
	var n int
	fmt.Sscan(p, &n)
	return n
}

func TestSendEmail(t *testing.T) {
	srv := startFakeSMTP(t, false)
	cfg := EmailConfig{Host: "127.0.0.1", Port: srv.port(), Username: "u@example.com", Password: "s3cret",
		From: "micelio@example.com", To: ParseRecipients("a@example.com, b@example.com"), Security: "none"}
	if err := SendEmail(cfg, "[Micelio] shop: 2 alerts, +3 soft-404", "plain body", "<p>html</p>"); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.auth != "\x00u@example.com\x00s3cret" {
		t.Errorf("auth = %q", srv.auth)
	}
	if srv.from != "micelio@example.com" || len(srv.to) != 2 {
		t.Errorf("envelope from=%q to=%v", srv.from, srv.to)
	}
	for _, want := range []string{"Subject: [Micelio] shop: 2 alerts", "multipart/alternative", "text/plain", "text/html", "plain body"} {
		if !strings.Contains(srv.data, want) {
			t.Errorf("message lacks %q", want)
		}
	}
}

func TestSendEmailRefusesPlainWhenSTARTTLSExpected(t *testing.T) {
	srv := startFakeSMTP(t, false) // offers no STARTTLS
	cfg := EmailConfig{Host: "127.0.0.1", Port: srv.port(), Username: "u", Password: "p",
		From: "m@example.com", To: []string{"a@example.com"}} // default security: starttls
	err := SendEmail(cfg, "s", "t", "")
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("err = %v, want a STARTTLS refusal", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.auth != "" {
		t.Error("the password was sent over a plain connection")
	}
}

func TestSendEmailRejectsHeaderInjection(t *testing.T) {
	cfg := EmailConfig{Host: "127.0.0.1", Port: 1, From: "m@example.com", To: []string{"a@example.com\r\nBcc: x@evil"}}
	if err := SendEmail(cfg, "s", "t", ""); err == nil || !strings.Contains(err.Error(), "invalid address") {
		t.Fatalf("err = %v", err)
	}
}

func statsFor(pages, s404, idx, nonIdx int, soft404 []string) *types.CrawlStats {
	st := &types.CrawlStats{TotalPages: pages, StatusCodes: map[int]int{200: pages - s404, 404: s404}}
	st.IndexabilityStats.Indexable, st.IndexabilityStats.NonIndexable = idx, nonIdx
	if soft404 != nil {
		st.Soft404Stats = &types.Soft404Stats{ByConfidence: map[string]int{}}
		for _, u := range soft404 {
			st.Soft404Stats.Candidates = append(st.Soft404Stats.Candidates, types.Soft404Candidate{URL: u, Confidence: types.Soft404High})
		}
	}
	return st
}

func TestCompareAndShouldEmail(t *testing.T) {
	prev := statsFor(1000, 5, 950, 50, []string{"https://s/a"})
	same := statsFor(1000, 5, 950, 50, []string{"https://s/a"})
	if ch := CompareCrawls(CrawlSnapshot{Stats: prev}, CrawlSnapshot{Stats: same}); len(ch) != 0 {
		t.Errorf("identical crawls changed: %+v", ch)
	}
	quiet := RunInfo{Status: "completed", HasPrevious: true, PrevAlerts: map[string]bool{}}
	if ShouldEmail(quiet, false) {
		t.Error("a stable crawl should not email")
	}
	if !ShouldEmail(quiet, true) {
		t.Error("\"always\" should email")
	}

	worse := statsFor(1000, 40, 900, 100, []string{"https://s/a", "https://s/b", "https://s/c"})
	ch := CompareCrawls(
		CrawlSnapshot{Stats: prev, URLs: []string{"https://s/x", "https://s/y"}},
		CrawlSnapshot{Stats: worse, URLs: []string{"https://s/x"}},
	)
	labels := map[string]Change{}
	for _, c := range ch {
		labels[c.Label] = c
	}
	if c := labels["New soft-404 candidates"]; !c.Important || len(c.Examples) != 2 {
		t.Errorf("soft-404 change = %+v", c)
	}
	if c := labels["Indexable pages"]; !c.Important || c.Before != "95.0%" || c.After != "90.0%" {
		t.Errorf("indexable change = %+v", c)
	}
	if c := labels["Error responses (4xx/5xx)"]; !c.Important {
		t.Errorf("errors change = %+v", c)
	}
	if c := labels["URLs crawled last time but not this time"]; len(c.Examples) != 1 || c.Important {
		t.Errorf("missing URLs = %+v (1 of 2 is below the 10-URL floor)", c)
	}
	run := RunInfo{Name: "shop · daily", SeedURL: "https://s/", Status: "completed", Pages: 1000, Errors: 40,
		HasPrevious: true, PrevAlerts: map[string]bool{}, Changes: ch, Link: "http://127.0.0.1:3100/#/results/x"}
	if !ShouldEmail(run, false) {
		t.Error("important changes should email")
	}
	subject, text, html := RenderEmail(run)
	if subject != "[Micelio] shop · daily: +2 soft-404" {
		t.Errorf("subject = %q", subject)
	}
	for _, want := range []string{"Indexable pages: 95.0% -> 90.0%", "https://s/b", "Open the crawl:"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q", want)
		}
	}
	if !strings.Contains(html, "&rarr;") || strings.Contains(html, "<script") {
		t.Error("html body not rendered")
	}

	// Skipped runs always email; a persisting warning alone does not.
	if !ShouldEmail(RunInfo{Status: "skipped", Reason: "network check failed"}, false) {
		t.Error("skipped run should email")
	}
	persisting := RunInfo{Status: "completed", HasPrevious: true, PrevAlerts: map[string]bool{"duplicate-titles": true},
		Alerts: &types.AlertSummary{Alerts: []types.AlertResult{{ID: "duplicate-titles", Severity: "warning"}}}}
	if ShouldEmail(persisting, false) {
		t.Error("an alert already present last time should not email again")
	}
	persisting.Alerts.Alerts[0].ID = "soft-404-high"
	if !ShouldEmail(persisting, false) {
		t.Error("a new alert should email")
	}
	_ = time.Second
}
