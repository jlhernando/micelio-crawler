package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// realDashboard starts a server that answers /api/identity like the
// dashboard, with a key written into a temporary ui folder.
func realDashboard(t *testing.T) (srv *httptest.Server, keyPath string) {
	t.Helper()
	dir := t.TempDir()
	remove, err := writeDashboardKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(remove)
	keyPath = filepath.Join(dir, dashboardKeyFile)
	if fi, err := os.Stat(keyPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v, mode %v; want 0600", err, fi.Mode().Perm())
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/identity", HandleIdentity)
	mux.HandleFunc("/api/ok", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, keyPath
}

func dialAddr(srv *httptest.Server) string { return srv.Listener.Addr().String() }

func TestVerifiedDialAcceptsTheDashboard(t *testing.T) {
	srv, keyPath := realDashboard(t)
	conn, err := VerifiedDial(context.Background(), keyPath, "tcp", dialAddr(srv))
	if err != nil {
		t.Fatalf("real dashboard refused: %v", err)
	}
	conn.Close()

	// The same connection then carries normal requests.
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, n, a string) (net.Conn, error) { return VerifiedDial(ctx, keyPath, n, a) },
	}}
	resp, err := client.Get(srv.URL + "/api/ok")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "ok" {
		t.Fatalf("body %q", b)
	}
}

// Anything else on the port gets nothing: a plain 401/404 answer, a made-up
// proof, a missing key file, or a relay of the challenge to the real
// dashboard on another address.
func TestVerifiedDialRefusesImpostors(t *testing.T) {
	real, keyPath := realDashboard(t)

	gotAuth := make(chan string, 10)
	impostor := func(h http.HandlerFunc) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if a := r.Header.Get("Authorization"); a != "" {
				gotAuth <- a
			}
			h(w, r)
		}))
		t.Cleanup(s.Close)
		return s
	}
	cases := map[string]*httptest.Server{
		"401": impostor(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }),
		"404": impostor(http.NotFound),
		"fake proof": impostor(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"proof":"00"}`)
		}),
		"relay": impostor(func(w http.ResponseWriter, r *http.Request) {
			resp, err := http.Get(real.URL + r.URL.RequestURI())
			if err != nil {
				w.WriteHeader(502)
				return
			}
			defer resp.Body.Close()
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
		}),
	}
	for name, s := range cases {
		_, err := VerifiedDial(context.Background(), keyPath, "tcp", dialAddr(s))
		if !errors.Is(err, ErrNotDashboard) {
			t.Errorf("%s: err = %v, want ErrNotDashboard", name, err)
		}
		// Through the client: the request with the token is never sent.
		client := &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, n, a string) (net.Conn, error) { return VerifiedDial(ctx, keyPath, n, a) },
		}}
		req, _ := http.NewRequest("GET", s.URL+"/api/crawls", nil)
		req.Header.Set("Authorization", "Bearer SECRET")
		if _, err := client.Do(req); !errors.Is(err, ErrNotDashboard) {
			t.Errorf("%s: client err = %v", name, err)
		}
	}
	select {
	case a := <-gotAuth:
		t.Fatalf("an impostor received Authorization %q", a)
	default:
	}

	// No key file (no dashboard of this user holds the folder).
	if _, err := VerifiedDial(context.Background(), filepath.Join(t.TempDir(), "none"), "tcp", dialAddr(real)); !errors.Is(err, ErrNotDashboard) {
		t.Errorf("missing key: err = %v", err)
	}
	// Nothing listening: the dial error, not ErrNotDashboard.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	free := l.Addr().String()
	l.Close()
	if _, err := VerifiedDial(context.Background(), keyPath, "tcp", free); err == nil || errors.Is(err, ErrNotDashboard) {
		t.Errorf("closed port: err = %v, want a dial error", err)
	}
}

// The dashboard writes its key when it takes the folder lock and removes it
// when it stops.
func TestDashboardKeyFollowsTheLock(t *testing.T) {
	dir := t.TempDir()
	release, err := lockUIDir(dir, "pid 1, http://127.0.0.1:3100")
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, dashboardKeyFile)
	if _, err := readKeyFile(key); err != nil {
		t.Fatalf("key after lock: %v", err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "server.lock")); fi.Mode().Perm() != 0o600 {
		t.Errorf("server.lock mode %v, want 0600", fi.Mode().Perm())
	}
	// A second dashboard that cannot take the lock does not replace the key.
	before, _ := os.ReadFile(key)
	if _, err := lockUIDir(dir, "pid 2"); err == nil {
		t.Fatal("second lock succeeded")
	}
	if after, _ := os.ReadFile(key); string(after) != string(before) {
		t.Error("the key changed when a second dashboard failed to start")
	}
	release()
	if _, err := os.Stat(key); !os.IsNotExist(err) {
		t.Errorf("key still there after release: %v", err)
	}
}

// A dashboard with a token answers the identity check without one (the
// CLI checks before it sends the token); everything else still needs it.
func TestIdentityRouteNeedsNoToken(t *testing.T) {
	_, keyPath := realDashboard(t)
	h, _ := guardServer(ServerOptions{Host: "127.0.0.1", Port: 3100, AuthToken: "s3cret-token-0123456789"}, false)
	srv := httptest.NewServer(h)
	defer srv.Close()
	conn, err := VerifiedDial(context.Background(), keyPath, "tcp", dialAddr(srv))
	if err != nil {
		t.Fatalf("identity check through the full handler: %v", err)
	}
	conn.Close()
	resp, err := http.Get(srv.URL + "/api/identityx")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/api/identityx: status %d, want 401", resp.StatusCode)
	}
}
