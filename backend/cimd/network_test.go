package cimd

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublicIP(t *testing.T) {
	for _, address := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		require.True(t, publicIP(netip.MustParseAddr(address)), address)
	}
	for _, prefix := range specialNetworks {
		require.False(t, publicIP(prefix.Addr()), prefix.String())
		require.False(t, publicIP(prefix.Addr().Next()), prefix.String())
	}
	for _, address := range []string{"::", "::1", "::ffff:8.8.8.8", "::ffff:127.0.0.1", "64:ff9b::808:808", "64:ff9b:1::1",
		"100::1", "100:0:0:1::1", "fc00::1", "fe80::1", "fec0::1", "ff02::1", "5f00::1", "2001:4860::1%eth0"} {
		require.False(t, publicIP(netip.MustParseAddr(address)), address)
	}
}

func TestSafeDialer(t *testing.T) {
	for _, addresses := range [][]netip.Addr{
		nil, {netip.MustParseAddr("127.0.0.1")},
		{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("10.1.2.3")},
		{netip.MustParseAddr("2606:4700:4700::1111"), netip.MustParseAddr("fc00::1")},
		make([]netip.Addr, 17),
	} {
		d := safeDialer{
			lookup: func(context.Context, string, string) ([]netip.Addr, error) { return addresses, nil },
			dial: func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("dialed prohibited DNS result")
				return nil, nil
			},
		}
		_, err := d.DialContext(context.Background(), "tcp", "example.com:443")
		require.Error(t, err)
	}
	lookups := 0
	var dialed []string
	d := safeDialer{
		lookup: func(context.Context, string, string) ([]netip.Addr, error) {
			lookups++
			if lookups > 1 {
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("1.1.1.1")}, nil
		},
		dial: func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return nil, errors.New("test dial failure")
		},
	}
	_, err := d.DialContext(context.Background(), "tcp", "example.com:8443")
	require.Error(t, err)
	require.Equal(t, []string{"8.8.8.8:8443", "1.1.1.1:8443"}, dialed, "dial validated literals, never resolve again during connection")
	_, err = d.DialContext(context.Background(), "tcp", "example.com:8443")
	require.Error(t, err)
	require.Len(t, dialed, 2, "rebinding on a subsequent request must fail")
	_, err = d.DialContext(context.Background(), "tcp", "127.0.0.1:443")
	require.Error(t, err)
	require.Equal(t, 2, lookups, "IP literals do not require DNS")
}

func TestHTTPSFetch(t *testing.T) {
	hits := 0
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits++
		require.Equal(t, "example.com", req.Host)
		require.Equal(t, "example.com", req.TLS.ServerName)
		require.Equal(t, "application/json", req.Header.Get("Accept"))
		require.Empty(t, req.Header.Get("Cookie"))
		require.Empty(t, req.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(documentJSON(t, metadataDocument()))
	}))
	defer ts.Close()
	r := New()
	transport := r.client.Transport.(*http.Transport)
	require.Nil(t, transport.Proxy)
	require.Nil(t, r.client.Jar)
	d := safeDialer{
		lookup: func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		},
		dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			require.Equal(t, "8.8.8.8:443", address)
			// Test-only routing after production DNS/IP validation, with the
			// original SNI, Host header, and certificate checks still in use.
			return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(ts.URL, "https://"))
		},
	}
	transport.DialContext = d.DialContext
	transport.TLSClientConfig = ts.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	_, err := r.ResolveOAuthClient(context.Background(), testID)
	require.NoError(t, err)
	require.Equal(t, 1, hits)
	transport.CloseIdleConnections()
	// Untrusted certificates must still fail, even for a validated public IP.
	transport.TLSClientConfig = nil
	_, err = r.ResolveOAuthClient(context.Background(), testID+"?uncached")
	require.Error(t, err)
	require.Equal(t, 1, hits)
}
