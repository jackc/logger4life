package cimd

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

const defaultCacheTTL = 5 * time.Minute
const maxCacheTTL = time.Hour

// cacheExpiry honors freshness without retaining validators or stale bodies:
// no-cache/expired documents receive a new unconditional GET. Unrecognized
// Vary variants and private/no-store responses are not retained at all.
func cacheExpiry(h http.Header, started, now time.Time) time.Time {
	if h.Get("Vary") != "" || strings.Contains(strings.ToLower(h.Get("Pragma")), "no-cache") {
		return time.Time{}
	}
	directives := map[string]string{}
	for _, part := range strings.Split(strings.Join(h.Values("Cache-Control"), ","), ",") {
		key, value, _ := strings.Cut(strings.TrimSpace(part), "=")
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "no-store" || key == "private" || key == "no-cache" {
			return time.Time{}
		}
		if _, duplicate := directives[key]; duplicate && (key == "max-age" || key == "s-maxage") {
			return time.Time{}
		}
		directives[key] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	// Calculate corrected initial age (RFC 9111), including transport time.
	age := now.Sub(started)
	if value := h.Get("Age"); value != "" {
		seconds, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return time.Time{}
		}
		age += time.Duration(seconds) * time.Second
	}
	date, err := http.ParseTime(h.Get("Date"))
	if err == nil && now.Sub(date) > age {
		age = now.Sub(date)
	}
	lifetime := defaultCacheTTL
	value, hasMaxAge := directives["s-maxage"]
	if !hasMaxAge {
		value, hasMaxAge = directives["max-age"]
	}
	if hasMaxAge {
		seconds, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return time.Time{}
		}
		lifetime = time.Duration(seconds) * time.Second
	} else if h.Get("Expires") != "" {
		expires, err := http.ParseTime(h.Get("Expires"))
		if err != nil {
			return time.Time{}
		}
		if date.IsZero() {
			date = started
		}
		lifetime = expires.Sub(date)
	}
	remaining := min(lifetime-age, maxCacheTTL-age)
	if remaining <= 0 {
		return time.Time{}
	}
	return now.Add(remaining)
}
