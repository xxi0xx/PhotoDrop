package abuse

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP trusts XFF only when the socket peer is trusted, walking right to
// left until the nearest untrusted hop. Malformed chains fall back to the peer.
// Other forwarding headers are deliberately ignored.
func ClientIP(r *http.Request, trusted []netip.Prefix) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || peer.Zone() != "" {
		return netip.Addr{}, true
	}
	peer = peer.Unmap()
	isTrusted := func(ip netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(ip) {
				return true
			}
		}
		return false
	}
	if !isTrusted(peer) {
		return peer, false
	}
	values := r.Header.Values("X-Forwarded-For")
	if len(values) == 0 {
		return peer, false
	}
	chain := strings.Join(values, ",")
	if len(chain) > 4096 {
		return peer, true
	}
	parts := strings.Split(chain, ",")
	if len(parts) > 32 {
		return peer, true
	}
	ips := make([]netip.Addr, len(parts))
	for i, p := range parts {
		ip, err := netip.ParseAddr(strings.TrimSpace(p))
		if err != nil || ip.Zone() != "" {
			return peer, true
		}
		ips[i] = ip.Unmap()
	}
	resolved := peer
	for i := len(ips) - 1; i >= 0 && isTrusted(resolved); i-- {
		resolved = ips[i]
	}
	return resolved, false
}
