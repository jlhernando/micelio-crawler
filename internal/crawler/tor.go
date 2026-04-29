package crawler

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultTorSOCKS       = "socks5://127.0.0.1:9050"
	defaultTorControlPort = "127.0.0.1:9051"
	defaultTorRotateEvery = 50
	minCircuitAge         = 10 * time.Second // Tor needs ~10s to build a new circuit
)

// TorManager manages Tor SOCKS5 proxy and circuit rotation via the control port.
type TorManager struct {
	controlAddr  string
	password     string
	rotateEvery  int
	lastRotation time.Time
	mu           sync.Mutex
	requestCount atomic.Int64
}

// NewTorManager creates a TorManager. controlAddr is "host:port" for Tor's ControlPort.
func NewTorManager(controlAddr, password string, rotateEvery int) *TorManager {
	if controlAddr == "" {
		controlAddr = defaultTorControlPort
	}
	if rotateEvery <= 0 {
		rotateEvery = defaultTorRotateEvery
	}
	return &TorManager{
		controlAddr: controlAddr,
		password:    password,
		rotateEvery: rotateEvery,
		lastRotation: time.Now(),
	}
}

// ProxyURL returns the Tor SOCKS5 proxy URL.
func (tm *TorManager) ProxyURL() *url.URL {
	u, _ := url.Parse(defaultTorSOCKS)
	return u
}

// RecordRequest increments the request counter and rotates the circuit if needed.
func (tm *TorManager) RecordRequest() {
	n := tm.requestCount.Add(1)
	if n%int64(tm.rotateEvery) == 0 {
		tm.RotateCircuit()
	}
}

// RecordError triggers an immediate circuit rotation on bot-detection responses.
func (tm *TorManager) RecordError(statusCode int) {
	if statusCode == 403 || statusCode == 429 {
		tm.RotateCircuit()
	}
}

// RotateCircuit sends SIGNAL NEWNYM to Tor's control port to get a new exit IP.
// Rate-limited to once per minCircuitAge to allow Tor to build circuits.
func (tm *TorManager) RotateCircuit() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if time.Since(tm.lastRotation) < minCircuitAge {
		return
	}
	if err := tm.sendNewnym(); err != nil {
		fmt.Fprintf(os.Stderr, "\n  [tor] circuit rotation failed: %v\n", err)
		return
	}
	tm.lastRotation = time.Now()
	fmt.Fprintf(os.Stderr, "\n  [tor] circuit rotated (request #%d)\n", tm.requestCount.Load())
}

// sendNewnym connects to the Tor control port and sends SIGNAL NEWNYM.
func (tm *TorManager) sendNewnym() error {
	conn, err := net.DialTimeout("tcp", tm.controlAddr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("connect to control port %s: %w", tm.controlAddr, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	buf := make([]byte, 512)

	// Read greeting
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("read greeting: %w", err)
	}
	if n < 3 || string(buf[:3]) != "250" {
		return fmt.Errorf("unexpected greeting: %s", string(buf[:n]))
	}

	// Authenticate
	authCmd := "AUTHENTICATE\r\n"
	if tm.password != "" {
		escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(tm.password)
		authCmd = fmt.Sprintf("AUTHENTICATE \"%s\"\r\n", escaped)
	}
	if _, err := conn.Write([]byte(authCmd)); err != nil {
		return fmt.Errorf("send auth: %w", err)
	}
	n, err = conn.Read(buf)
	if err != nil {
		return fmt.Errorf("read auth response: %w", err)
	}
	if n < 3 || string(buf[:3]) != "250" {
		return fmt.Errorf("auth failed: %s", string(buf[:n]))
	}

	// Signal NEWNYM
	if _, err := conn.Write([]byte("SIGNAL NEWNYM\r\n")); err != nil {
		return fmt.Errorf("send NEWNYM: %w", err)
	}
	n, err = conn.Read(buf)
	if err != nil {
		return fmt.Errorf("read NEWNYM response: %w", err)
	}
	if n < 3 || string(buf[:3]) != "250" {
		return fmt.Errorf("NEWNYM failed: %s", string(buf[:n]))
	}

	return nil
}

// IsTorProxy returns true if the proxy string indicates Tor usage.
func IsTorProxy(proxy string) bool {
	return proxy == "tor" || proxy == "socks5://127.0.0.1:9050" || proxy == "socks5://localhost:9050"
}
