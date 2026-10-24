package scheduler

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Precheck is a condition that must hold before a scheduled crawl starts and
// while it runs, e.g. "only crawl from an allowed network". All configured
// checks must pass.
type Precheck struct {
	// EgressCIDRs requires the public egress IP to be inside one of these
	// ranges, e.g. your office network's public addresses.
	EgressCIDRs []string `json:"egressCidrs,omitempty"`
	// Command must exit 0. Only settable from the CLI, never from the HTTP
	// API (it runs an arbitrary shell command).
	Command string `json:"command,omitempty"`
	// IntervalSec re-runs the checks while a crawl runs; a failure pauses
	// the crawl until they pass again. 0 means 60 seconds.
	IntervalSec int `json:"intervalSec,omitempty"`
}

// Enabled reports whether any check is configured.
func (p *Precheck) Enabled() bool {
	return p != nil && (len(p.EgressCIDRs) > 0 || p.Command != "")
}

// Interval returns how often checks repeat during a crawl.
func (p *Precheck) Interval() time.Duration {
	if p == nil || p.IntervalSec <= 0 {
		return time.Minute
	}
	return max(minCheckInterval, time.Duration(p.IntervalSec)*time.Second)
}

// Validate checks the configuration (CIDRs parse).
func (p *Precheck) Validate() error {
	if p == nil {
		return nil
	}
	for _, c := range p.EgressCIDRs {
		if _, err := parseCIDR(c); err != nil {
			return fmt.Errorf("invalid egress range %q: %w", c, err)
		}
	}
	return nil
}

// PrecheckResult is the outcome of one check run.
type PrecheckResult struct {
	OK       bool   `json:"ok"`
	Reason   string `json:"reason,omitempty"`
	EgressIP string `json:"egressIp,omitempty"` // public IP, when checked
	At       string `json:"at"`
}

// Hooks for tests.
var (
	minCheckInterval = 10 * time.Second
	egressIPURLs = []string{"https://api.ipify.org", "https://ifconfig.me/ip", "https://icanhazip.com"}
	runCommand   = func(ctx context.Context, name string, args ...string) (string, error) {
		out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		return string(out), err
	}
)

// Run executes every configured check, stopping at the first failure.
func (p *Precheck) Run(ctx context.Context) PrecheckResult {
	res := PrecheckResult{OK: true, At: time.Now().UTC().Format(time.RFC3339)}
	if !p.Enabled() {
		return res
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	fail := func(format string, a ...any) PrecheckResult {
		res.OK = false
		res.Reason = fmt.Sprintf(format, a...)
		return res
	}
	if len(p.EgressCIDRs) > 0 {
		ip, err := egressIP(ctx)
		res.EgressIP = ip
		if err != nil {
			return fail("egress IP: %v", err)
		}
		if !ipInRanges(ip, p.EgressCIDRs) {
			return fail("egress IP %s is outside the allowed ranges", ip)
		}
	}
	if p.Command != "" {
		out, err := runCommand(ctx, shell(), shellFlag(), p.Command)
		if err != nil {
			msg := strings.TrimSpace(out)
			if len(msg) > 200 {
				msg = msg[:200]
			}
			return fail("check command failed: %v %s", err, msg)
		}
	}
	return res
}

func egressIP(ctx context.Context) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	var lastErr error
	for _, u := range egressIPURLs {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		resp.Body.Close()
		ip := strings.TrimSpace(string(body))
		if net.ParseIP(ip) != nil {
			return ip, nil
		}
		lastErr = fmt.Errorf("unexpected answer from %s", u)
	}
	return "", lastErr
}

func parseCIDR(s string) (*net.IPNet, error) {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "/") {
		if ip := net.ParseIP(s); ip != nil {
			if ip.To4() != nil {
				s += "/32"
			} else {
				s += "/128"
			}
		}
	}
	_, n, err := net.ParseCIDR(s)
	return n, err
}

func ipInRanges(ip string, ranges []string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, r := range ranges {
		if n, err := parseCIDR(r); err == nil && n.Contains(parsed) {
			return true
		}
	}
	return false
}

func shell() string {
	if _, err := os.Stat("/bin/sh"); err == nil {
		return "/bin/sh"
	}
	return "cmd"
}

func shellFlag() string {
	if shell() == "cmd" {
		return "/C"
	}
	return "-c"
}

// NewRunControlForTest returns a RunControl for runner tests in other packages.
func NewRunControlForTest() *RunControl { return newRunControl() }
