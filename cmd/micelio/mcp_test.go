package main

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestDashboardPort(t *testing.T) {
	for in, want := range map[string]string{
		"http://127.0.0.1:3100":  "3100",
		"http://localhost:4000/": "4000",
		"http://127.0.0.1":       "80",
	} {
		got, err := dashboardPort(in)
		if err != nil || got != want {
			t.Errorf("dashboardPort(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	// Only a dashboard on this computer can be started.
	if _, err := dashboardPort("http://example.com:3100"); err == nil {
		t.Error("remote host accepted for autostart")
	}
}

// With --no-autostart and nothing listening, the error says what to do.
func TestEnsureDashboardNoAutostart(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close() // a free port: nothing answers there

	err = ensureDashboard(context.Background(), "http://"+addr, "", false)
	if err == nil {
		t.Fatal("want an error when no dashboard answers")
	}
	if !strings.Contains(err.Error(), "micelio ui") || !strings.Contains(err.Error(), addr) {
		t.Errorf("error = %q, want a hint to run micelio ui on %s", err, addr)
	}
}

// An IPv6 loopback dashboard is started listening on that address.
func TestDashboardHost(t *testing.T) {
	for base, want := range map[string]string{
		"http://127.0.0.1:3100": "127.0.0.1",
		"http://localhost:3100": "127.0.0.1",
		"http://[::1]:3100":     "::1",
	} {
		host, _, err := dashboardAddr(base)
		if err != nil || host != want {
			t.Errorf("dashboardAddr(%q) host = %q, %v; want %q", base, host, err, want)
		}
	}
}
