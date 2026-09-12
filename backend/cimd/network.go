// Package cimd retrieves OAuth Client ID Metadata Documents over public HTTPS.
package cimd

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// Special-purpose ranges from the IANA IPv4/IPv6 registries (2026-09-12).
// Deny entire registry blocks, including their globally reachable exceptions.
// IPv6 is additionally restricted to allocated global unicast space, excluding
// translation, mapped, site-local, multicast, and unallocated address space.
var specialNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.31.196.0/24"), netip.MustParsePrefix("192.52.193.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("192.175.48.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/3"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("2620:4f:8000::/48"),
	netip.MustParsePrefix("3fff::/20"),
}

var globalIPv6 = netip.MustParsePrefix("2000::/3")

func publicIP(ip netip.Addr) bool {
	if !ip.IsValid() || ip.Zone() != "" || ip.Is4In6() || !ip.IsGlobalUnicast() || (ip.Is6() && !globalIPv6.Contains(ip)) {
		return false
	}
	for _, prefix := range specialNetworks {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

type safeDialer struct {
	lookup func(context.Context, string, string) ([]netip.Addr, error)
	dial   func(context.Context, string, string) (net.Conn, error)
}

func (d safeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	// net/http may detach a connection attempt from the request deadline.
	// Bound DNS and all candidate connection attempts together as well.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	var addresses []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{ip}
	} else {
		addresses, err = d.lookup(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
	}
	if len(addresses) == 0 || len(addresses) > 16 {
		return nil, errors.New("invalid number of metadata host addresses")
	}
	// Check ALL answers before dialing any, including mixed public/private
	// results. Dial the validated literal, never re-resolve the hostname.
	for _, ip := range addresses {
		if !publicIP(ip) {
			return nil, errors.New("metadata host is not public")
		}
	}
	for _, ip := range addresses {
		var conn net.Conn
		conn, err = d.dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, err
}

func newHTTPClient() *http.Client {
	dialer := safeDialer{lookup: net.DefaultResolver.LookupNetIP, dial: (&net.Dialer{Timeout: 3 * time.Second}).DialContext}
	return &http.Client{
		Timeout:       fetchTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			// No environment proxy, cookie jar, credentials, or unvalidated
			// alternate dial path. TLS still verifies the original hostname.
			DialContext:            dialer.DialContext,
			DisableKeepAlives:      true,
			DisableCompression:     true,
			TLSHandshakeTimeout:    3 * time.Second,
			ResponseHeaderTimeout:  3 * time.Second,
			MaxResponseHeaderBytes: 8 << 10,
		},
	}
}
