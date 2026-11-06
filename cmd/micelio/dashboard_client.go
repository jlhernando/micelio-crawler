package main

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/micelio/micelio/internal/server"
)

// dashState is what answers at a dashboard address.
type dashState int

const (
	dashAbsent     dashState = iota // nothing answers
	dashVerified                    // this user's dashboard (or a remote one over https)
	dashUnverified                  // something answers but did not prove it is the dashboard
)

// isLocalHTTP reports whether base is a plain-http dashboard on this
// computer: the case where any local program could sit on the port.
func isLocalHTTP(base string) bool {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" {
		return false
	}
	h := u.Hostname()
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// dashboardClient returns the HTTP client the CLI uses to talk to the
// dashboard at base. On this computer every connection must pass the
// identity check (server.LocalDashboardClient) before a byte of a request,
// token or crawl file is sent. Another computer is reached as configured:
// over https its certificate proves who it is.
func dashboardClient(base string, timeout time.Duration) (*http.Client, error) {
	if isLocalHTTP(base) {
		return server.LocalDashboardClient(timeout)
	}
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// probeDashboard reports what answers at base. err explains dashUnverified.
func probeDashboard(base string) (dashState, error) {
	base = strings.TrimRight(base, "/")
	client, err := dashboardClient(base, 3*time.Second)
	if err != nil {
		return dashAbsent, err
	}
	resp, err := client.Get(base + "/api/update/status")
	if err != nil {
		if errors.Is(err, server.ErrNotDashboard) {
			return dashUnverified, err
		}
		return dashAbsent, nil
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return dashAbsent, nil
	}
	// 401 still means a dashboard is there (it needs --auth-token).
	return dashVerified, nil
}

// innerErr drops the *url.Error wrapper ("Get \"http://...\": ...") so a
// message names the problem, not the request.
func innerErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
