// Package netguard keeps crawls away from this computer and its networks
// (SSRF protection). It has no dependencies so every package that makes
// requests to crawled URLs can use it.
package netguard

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// AllowFakeIPEnv names the environment variable that lets crawls reach the
// benchmarking range 198.18.0.0/15. Fake-IP proxies (Clash, Surge,
// Shadowrocket, sing-box in fake-IP DNS/TUN mode) answer every DNS query
// with an address from it, so without this opt-in every crawl is refused.
// Only that range is affected. Mirrors src/net-guard.ts.
const AllowFakeIPEnv = "MICELIO_ALLOW_FAKE_IP"

// fakeIPRange is the benchmarking range fake-IP proxies hand out.
var fakeIPRange = mustCIDR("198.18.0.0/15")

var (
	fakeIPOnce    sync.Once
	fakeIPAllowed atomic.Bool
)

// AllowFakeIP reports whether MICELIO_ALLOW_FAKE_IP is set to a true value
// ("1", "true"). The variable is read once, on first use, so a value from
// ~/.micelio/micelio.env (loaded at CLI start) counts.
func AllowFakeIP() bool {
	fakeIPOnce.Do(func() {
		v, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv(AllowFakeIPEnv)))
		fakeIPAllowed.Store(v)
	})
	return fakeIPAllowed.Load()
}

// SetAllowFakeIP overrides MICELIO_ALLOW_FAKE_IP (tests).
func SetAllowFakeIP(allow bool) {
	fakeIPOnce.Do(func() {})
	fakeIPAllowed.Store(allow)
}

// FakeIPHint explains a refused address in 198.18.0.0/15 (the range of
// fake-IP proxies) and how to allow it. It returns "" for any other address.
func FakeIPHint(addr string) string {
	ip := net.ParseIP(strings.Trim(addr, "[]"))
	if ip == nil || !fakeIPRange.Contains(ip) {
		return ""
	}
	return fmt.Sprintf("%s is in the benchmarking range 198.18.0.0/15, used by fake-IP proxies (Clash, Surge, Shadowrocket, sing-box). "+
		"If you use one, set %s=1 or switch it to redir-host/real-IP mode", ip, AllowFakeIPEnv)
}

// specialV4 are the IPv4 special-purpose ranges that are not globally
// reachable (IANA IPv4 Special-Purpose Address Registry, RFC 6890 and
// updates). Kept in sync with SPECIAL_V4 in src/net-guard.ts.
var specialV4 = []*net.IPNet{
	mustCIDR("0.0.0.0/8"),       // "this network"
	mustCIDR("10.0.0.0/8"),      // RFC 1918
	mustCIDR("100.64.0.0/10"),   // shared CGNAT space, also Tailscale
	mustCIDR("127.0.0.0/8"),     // loopback
	mustCIDR("169.254.0.0/16"),  // link-local, cloud metadata
	mustCIDR("172.16.0.0/12"),   // RFC 1918
	mustCIDR("192.0.0.0/24"),    // IETF protocol assignments
	mustCIDR("192.0.2.0/24"),    // documentation TEST-NET-1
	mustCIDR("192.88.99.0/24"),  // deprecated 6to4 relay anycast
	mustCIDR("192.168.0.0/16"),  // RFC 1918
	mustCIDR("198.18.0.0/15"),   // benchmarking
	mustCIDR("198.51.100.0/24"), // documentation TEST-NET-2
	mustCIDR("203.0.113.0/24"),  // documentation TEST-NET-3
	mustCIDR("224.0.0.0/3"),     // multicast 224/4, reserved 240/4, broadcast
}

// IPv6 ranges that carry an IPv4 address inside, and special-purpose blocks
// inside global unicast 2000::/3 that are not globally reachable.
var (
	globalUnicast = mustCIDR("2000::/3")
	ipv4Compat    = mustCIDR("::/96")           // deprecated IPv4-compatible (:: and ::1 too)
	ipv4Transl    = mustCIDR("::ffff:0:0:0/96") // RFC 2765: IPv4-translatable
	nat64         = mustCIDR("64:ff9b::/96")    // RFC 6052: IPv4 in the last 4 bytes
	sixToFour     = mustCIDR("2002::/16")       // RFC 3056: IPv4 in bytes 2-5
	teredo        = mustCIDR("2001::/32")       // RFC 4380: client IPv4 XORed in the last 4 bytes
	ietfProto     = mustCIDR("2001::/23")       // IETF protocol assignments (benchmarking, ORCHID...)
	docV6         = mustCIDR("2001:db8::/32")   // documentation
	docV6New      = mustCIDR("3fff::/20")       // RFC 9637 documentation
)

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// BlockedIP reports whether ip is an address crawls must never reach: this
// computer, private and link-local networks (cloud metadata included),
// shared CGNAT space, unspecified, multicast or broadcast addresses, and
// every other special-purpose range that is not globally reachable
// (benchmarking, documentation, reserved). IPv6 forms that embed an IPv4
// address (mapped, translatable, compatible, NAT64, 6to4, Teredo) are
// judged by that IPv4 address. Other IPv6 addresses are allowed only inside
// global unicast 2000::/3, minus its special-purpose blocks, so local-use
// NAT64, discard 100::/64, ULA, link-local, site-local and multicast are
// always blocked. With MICELIO_ALLOW_FAKE_IP, plain (or IPv4-mapped)
// addresses in 198.18.0.0/15 are allowed; see AllowFakeIPEnv.
func BlockedIP(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		return blockedV4(ip4, AllowFakeIP())
	}
	if len(ip) != net.IPv6len {
		return true // nil or malformed: fail closed
	}
	// Transition forms never get the fake-IP allowance: fake-IP proxies
	// answer with plain IPv4 addresses.
	switch {
	case ipv4Compat.Contains(ip), ipv4Transl.Contains(ip), nat64.Contains(ip):
		return blockedV4(net.IP(ip[12:16]), false)
	case sixToFour.Contains(ip):
		return blockedV4(net.IP(ip[2:6]), false)
	case teredo.Contains(ip):
		v4 := make(net.IP, 4)
		for i := range v4 {
			v4[i] = ip[12+i] ^ 0xff
		}
		return blockedV4(v4, false)
	}
	return !globalUnicast.Contains(ip) || ietfProto.Contains(ip) ||
		docV6.Contains(ip) || docV6New.Contains(ip)
}

func blockedV4(ip4 net.IP, allowFakeIP bool) bool {
	if allowFakeIP && fakeIPRange.Contains(ip4) {
		return false
	}
	for _, n := range specialV4 {
		if n.Contains(ip4) {
			return true
		}
	}
	return false
}

// BlockedError is returned when a connection to a blocked address is refused.
type BlockedError struct{ Addr string }

func (e *BlockedError) Error() string {
	msg := fmt.Sprintf("blocked connection to private address %s", e.Addr)
	if hint := FakeIPHint(e.Addr); hint != "" {
		msg += ": " + hint
	}
	return msg
}

// Dialer returns a dialer that refuses to connect to a blocked address.
// The check runs on the address really dialled, after DNS resolution, so
// it covers host names, redirects and any HTTP stack that takes a dialer.
func Dialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if BlockedIP(net.ParseIP(host)) {
				return &BlockedError{Addr: host}
			}
			return nil
		},
	}
}

// ProxyDialer returns a dialer for the connection to a proxy the local user
// configured: CLI --proxy (Tor included), HTTP(S)_PROXY, or the dashboard's
// --trusted-proxy. Unlike Dialer it accepts loopback and private addresses,
// because a local proxy (Tor on 127.0.0.1:9050, a forward proxy on the
// LAN) is the usual case and the user chose it. Only use it for proxy
// URLs that come from the local user, never from a crawl config sent over
// HTTP. Targets reached through the proxy are still checked and pinned
// (DialThrough).
func ProxyDialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
}
