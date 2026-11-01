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

// BlockedIP reports whether ip is an address crawls must never reach: this
// computer, private and link-local networks (cloud metadata included),
// shared CGNAT space, and unspecified, multicast or broadcast addresses.
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
