package crawler

import (
	"net"
	"time"

	"github.com/micelio/micelio/internal/netguard"
)

// blockedIP reports whether ip is an address crawls must never reach.
func blockedIP(ip net.IP) bool { return netguard.BlockedIP(ip) }

// SafeDialer returns a dialer that refuses to connect to a blocked address,
// checked after DNS resolution (see netguard.Dialer).
func SafeDialer(timeout time.Duration) *net.Dialer { return netguard.Dialer(timeout) }
