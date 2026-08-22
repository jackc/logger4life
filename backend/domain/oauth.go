package domain

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/url"
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
// Fragments are never allowed.
func ValidRedirectURI(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Fragment != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1") {
		return true
	}
	return false
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

// SameCanonicalURL compares two canonical URL strings for OAuth audience
// matching, tolerating trailing slashes and case differences in scheme/host.
func SameCanonicalURL(a, b string) bool {
	return strings.EqualFold(strings.TrimRight(a, "/"), strings.TrimRight(b, "/"))
}
