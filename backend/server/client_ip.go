package server

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

func parseTrustedProxies(value string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	if strings.TrimSpace(value) == "" {
		return prefixes, nil
	}
	for _, cidr := range strings.Split(value, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
		if err != nil {
			return nil, fmt.Errorf("invalid TRUSTED_PROXY_CIDRS entry %q", cidr)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func requestClientIP(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown"
	}
	peer = peer.Unmap()
	isTrusted := func(ip netip.Addr) bool {
		for _, prefix := range trusted {
			if prefix.Contains(ip) {
				return true
			}
		}
		return false
	}
	if !isTrusted(peer) {
		return peer.String()
	}
	forwarded := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(forwarded) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(forwarded[i]))
		if err != nil {
			return peer.String()
		}
		ip = ip.Unmap()
		if !isTrusted(ip) {
			return ip.String()
		}
	}
	return peer.String()
}
