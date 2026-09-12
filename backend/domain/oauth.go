package domain

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// HashToken applies SHA-256 to an opaque OAuth token. The hash is what gets
// persisted — the plaintext token never lands in the database.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// ValidRedirectURI reports whether a redirect URI is one we are willing to
// register: https anywhere, or plain http on loopback for native clients.
// Require an absolute hierarchical URL with a host, no credentials or
// fragments, and a usable port. Redirect matching still uses the original
// string: parsing here must not relax the registered callback binding.
func ValidRedirectURI(s string) bool {
	u, ok := parseOAuthURL(s)
	return ok && (u.Scheme == "https" || oauthLoopbackHost(u.Hostname()))
}

// ValidClientMetadataURL validates a CIMD identifier without normalizing its
// identity. Resolution must additionally enforce a public network destination.
func ValidClientMetadataURL(s string) bool {
	u, ok := parseOAuthURL(s)
	if !ok || u.Scheme != "https" || u.Path == "" || len(s) > 2048 {
		return false
	}
	// Check decoded segments too, so escaped dots cannot bypass this rule.
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func parseOAuthURL(s string) (*url.URL, bool) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") ||
		u.Opaque != "" || u.Hostname() == "" || u.User != nil ||
		strings.ContainsAny(s, "#\\ \t\r\n") {
		return nil, false
	}
	host := u.Hostname()
	if strings.HasPrefix(u.Host, "[") {
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.Is6() || ip.Is4In6() || ip.Zone() != "" {
			return nil, false
		}
	} else {
		// Reject malformed IP literals and host characters that browsers
		// reinterpret. International domain names must use their ASCII form.
		for _, ch := range host {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
				(ch >= '0' && ch <= '9') || ch == '-' || ch == '.' || ch == '_') {
				return nil, false
			}
		}
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil, false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, false
		}
	}
	return u, true
}

func oauthLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Zone() == "" && ip.IsLoopback()
}

// OAuthCanonicalOrigin validates the public MCP origin. A single root slash
// is accepted for convenience; paths, queries (even empty), and fragments
// are not. HTTP is only supported for local development on loopback hosts.
// The returned browser origin is used unchanged for discovery and iss.
func OAuthCanonicalOrigin(s string) (string, bool) {
	u, ok := parseOAuthURL(s)
	if !ok || (u.Scheme != "https" && !oauthLoopbackHost(u.Hostname())) ||
		(u.EscapedPath() != "" && u.EscapedPath() != "/") || u.RawQuery != "" || u.ForceQuery {
		return "", false
	}
	return u.Scheme + "://" + oauthAuthority(u), true
}

// oauthAuthority normalizes only the scheme-dependent default port and host.
func oauthAuthority(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	if ip, err := netip.ParseAddr(host); err == nil {
		host = ip.String()
	}
	port := u.Port()
	if port != "" {
		n, _ := strconv.Atoi(port) // parseOAuthURL already validated the port.
		port = strconv.Itoa(n)
	}
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		return net.JoinHostPort(host, port)
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

// RedirectURIRegistered reports whether uri exactly matches one of a client's
// registered redirect URIs. OAuth 2.1 requires exact string matching.
func RedirectURIRegistered(registered []string, uri string) bool {
	for _, u := range registered {
		if u == uri {
			return true
		}
	}
	return false
}

// VerifyPKCE recomputes the S256 challenge from the verifier and compares it
// to the challenge the client committed to at /authorize. Constant-time
// compare to avoid leaking timing info on the verifier.
func VerifyPKCE(challenge, method, verifier string) bool {
	if method != "S256" {
		return false
	}
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// SameCanonicalURL compares OAuth resource URLs. Only scheme/host, default
// ports, and an empty root path are normalized. Paths (including escapes)
// and queries remain case-sensitive. This is never an issuer comparison.
func SameCanonicalURL(a, b string) bool {
	ua, ok := parseOAuthURL(a)
	if !ok {
		return false
	}
	ub, ok := parseOAuthURL(b)
	if !ok {
		return false
	}
	path := func(u *url.URL) string {
		if p := u.EscapedPath(); p != "" {
			return p
		}
		return "/"
	}
	return ua.Scheme == ub.Scheme && oauthAuthority(ua) == oauthAuthority(ub) &&
		path(ua) == path(ub) && ua.RawQuery == ub.RawQuery && ua.ForceQuery == ub.ForceQuery
}
