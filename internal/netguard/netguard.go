// Package netguard keeps crawls away from this computer and its networks
// (SSRF protection). It has no dependencies so every package that makes
// requests to crawled URLs can use it.
package netguard

import (
	"fmt"
	"net"
	"syscall"
	"time"
)

// cgnat is 100.64.0.0/10, shared address space used by carrier NAT and by
// private overlay networks such as Tailscale.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// IPv6 ranges that carry an IPv4 address inside (NAT64, 6to4, Teredo), and
// deprecated site-local addresses, which still route inside some networks.
var (
	nat64      = mustCIDR("64:ff9b::/96")   // RFC 6052: IPv4 in the last 4 bytes
	nat64Local = mustCIDR("64:ff9b:1::/48") // RFC 8215: local-use NAT64
	sixToFour  = mustCIDR("2002::/16")      // RFC 3056: IPv4 in bytes 2-5
	teredo     = mustCIDR("2001::/32")      // RFC 4380: client IPv4 XORed in the last 4 bytes
	siteLocal  = mustCIDR("fec0::/10")      // RFC 3879, deprecated
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
// shared CGNAT space, and unspecified, multicast or broadcast addresses.
// IPv6 forms that embed an IPv4 address (NAT64, 6to4, Teredo) are judged by
// that IPv4 address; local-use NAT64 and site-local IPv6 are always blocked.
func BlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		// 0.0.0.0/8 ("this network") and the broadcast address.
		return ip4[0] == 0 || ip4.Equal(net.IPv4bcast) || cgnat.Contains(ip4)
	}
	switch {
	case nat64Local.Contains(ip), siteLocal.Contains(ip):
		return true
	case nat64.Contains(ip):
		return BlockedIP(net.IP(ip[12:16]))
	case sixToFour.Contains(ip):
		return BlockedIP(net.IP(ip[2:6]))
	case teredo.Contains(ip):
		v4 := make(net.IP, 4)
		for i := range v4 {
			v4[i] = ip[12+i] ^ 0xff
		}
		return BlockedIP(v4)
	}
	return false
}

// BlockedError is returned when a connection to a blocked address is refused.
type BlockedError struct{ Addr string }

func (e *BlockedError) Error() string {
	return fmt.Sprintf("blocked connection to private address %s", e.Addr)
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
