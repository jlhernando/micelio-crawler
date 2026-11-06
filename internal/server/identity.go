package server

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Dashboard identity.
//
// The CLI (micelio import, micelio mcp) sends crawl files and the dashboard
// token to the dashboard on this computer. Anything can listen on
// 127.0.0.1:3100, also another user's program that got there first, so
// "something answers" is no proof. The dashboard that holds the data
// folder's lock writes a random key to ~/.micelio/ui/dashboard.key, which
// only this user can read, and proves it knows the key: GET /api/identity
// ?nonce=N answers HMAC(key, N + the address the connection reached). The
// CLI checks that proof on every new connection before it sends anything,
// so an impostor gets neither the token nor the crawl, and one that relays
// the challenge to the real dashboard on another address gets a proof for
// that other address, which does not match.

// dashboardKeyFile is the key's file name inside the ui folder.
const dashboardKeyFile = "dashboard.key"

// ErrNotDashboard means something answers at the dashboard address but did
// not prove it is this user's Micelio dashboard.
var ErrNotDashboard = errors.New("not this user's Micelio dashboard")

var (
	identityMu  sync.RWMutex
	identityKey []byte // set while this process holds the ui folder lock
)

// writeDashboardKey makes a new key, writes it owner-only into uiDir and
// keeps it for HandleIdentity. It returns a function that removes it.
func writeDashboardKey(uiDir string) (func(), error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	path := filepath.Join(uiDir, dashboardKeyFile)
	tmp, err := os.CreateTemp(uiDir, ".dashboard.key-*")
	if err != nil {
		return nil, err
	}
	_ = tmp.Chmod(0o600)
	_, werr := tmp.WriteString(hex.EncodeToString(key) + "\n")
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	identityMu.Lock()
	identityKey = key
	identityMu.Unlock()
	return func() {
		identityMu.Lock()
		if hmac.Equal(identityKey, key) {
			identityKey = nil
		}
		identityMu.Unlock()
		if cur, err := readKeyFile(path); err == nil && hmac.Equal(cur, key) {
			os.Remove(path)
		}
	}, nil
}

// readKeyFile reads a dashboard key file.
func readKeyFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(key) < 32 {
		return nil, fmt.Errorf("%s is not a dashboard key", path)
	}
	return key, nil
}

// DashboardKeyPath is where the running dashboard keeps its key.
func DashboardKeyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".micelio", "ui", dashboardKeyFile), nil
}

// identityProof is the answer to a challenge: HMAC(key, nonce, addr), addr
// being the dashboard address the connection reached ("127.0.0.1:3100").
func identityProof(key []byte, nonce, addr string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("micelio-dashboard-identity\n" + nonce + "\n" + addr))
	return hex.EncodeToString(mac.Sum(nil))
}

// HandleIdentity answers GET /api/identity?nonce=N. It needs no token (the
// CLI asks before it sends one) and gives nothing away: the proof is only
// good for this dashboard's own address.
func HandleIdentity(w http.ResponseWriter, r *http.Request) {
	identityMu.RLock()
	key := identityKey
	identityMu.RUnlock()
	nonce := r.URL.Query().Get("nonce")
	local, _ := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if key == nil || local == nil || len(nonce) < 16 || len(nonce) > 128 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]string{"proof": identityProof(key, nonce, local.String())})
}

// VerifiedDial dials a dashboard on this computer and checks, on that same
// connection, that it proves it holds the key in keyPath. It returns the
// connection ready for HTTP requests. Nothing at the address returns the
// dial error; something that fails the check returns ErrNotDashboard.
func VerifiedDial(ctx context.Context, keyPath, network, addr string) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	if err := proveIdentity(conn, keyPath, addr); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func proveIdentity(conn net.Conn, keyPath, addr string) error {
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetDeadline(time.Time{})
	key, err := readKeyFile(keyPath)
	if err != nil {
		// No key: no dashboard of this user holds the folder (or it is
		// an older version that cannot prove itself).
		return fmt.Errorf("%w (%s answers, but %s cannot be read: %v)", ErrNotDashboard, addr, keyPath, err)
	}
	nb := make([]byte, 16)
	rand.Read(nb)
	nonce := hex.EncodeToString(nb)
	fmt.Fprintf(conn, "GET /api/identity?nonce=%s HTTP/1.1\r\nHost: %s\r\nAccept: application/json\r\nUser-Agent: micelio-cli\r\n\r\n", nonce, addr)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return fmt.Errorf("%w (%s: %v)", ErrNotDashboard, addr, err)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || resp.Close || br.Buffered() > 0 {
		return fmt.Errorf("%w (%s answered %s to the identity check)", ErrNotDashboard, addr, resp.Status)
	}
	var got struct {
		Proof string `json:"proof"`
	}
	json.Unmarshal(body, &got)
	want := identityProof(key, nonce, conn.RemoteAddr().String())
	if !hmac.Equal([]byte(got.Proof), []byte(want)) {
		return fmt.Errorf("%w (%s gave a wrong identity proof)", ErrNotDashboard, addr)
	}
	return nil
}

// LocalDashboardClient returns an HTTP client for a dashboard on this
// computer: every connection it opens must first pass the identity check,
// it never uses a proxy and never follows redirects. A timeout of 0 means
// none.
func LocalDashboardClient(timeout time.Duration) (*http.Client, error) {
	keyPath, err := DashboardKeyPath()
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return VerifiedDial(ctx, keyPath, network, addr)
			},
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     30 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}
