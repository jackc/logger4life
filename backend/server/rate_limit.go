package server

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// keyedRateLimiter bounds both traffic and bookkeeping. Full buckets may be
// forgotten after an idle interval; a full map rejects new keys instead of
// evicting an active key and resetting its allowance.
type keyedRateLimiter struct {
	mu                        sync.Mutex
	entries                   map[string]*rateEntry
	perMinute, burst, maxKeys int
	idle                      time.Duration
	nextSweep                 time.Time
	now                       func() time.Time
}

type rateEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newKeyedRateLimiter(perMinute, burst int) *keyedRateLimiter {
	idle := max(10*time.Minute, time.Duration(math.Ceil(float64(burst)/float64(perMinute)*60))*time.Second)
	return &keyedRateLimiter{entries: make(map[string]*rateEntry), perMinute: perMinute, burst: burst, maxKeys: 10000, idle: idle, now: time.Now}
}

// allow returns zero when admitted, otherwise the delay before retrying.
func (l *keyedRateLimiter) allow(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if !now.Before(l.nextSweep) {
		for key, entry := range l.entries {
			if now.Sub(entry.lastSeen) >= l.idle {
				delete(l.entries, key)
			}
		}
		l.nextSweep = now.Add(time.Minute)
	}
	entry := l.entries[key]
	if entry == nil {
		if len(l.entries) >= l.maxKeys {
			return time.Minute
		}
		entry = &rateEntry{limiter: rate.NewLimiter(rate.Limit(float64(l.perMinute)/60), l.burst)}
		l.entries[key] = entry
	}
	entry.lastSeen = now
	if entry.limiter.AllowN(now, 1) {
		return 0
	}
	return time.Duration(math.Ceil((1 - entry.limiter.TokensAt(now)) * 60 / float64(l.perMinute) * float64(time.Second)))
}

func writeRateLimit(w http.ResponseWriter, retry time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.Seconds())))))
	writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limit_exceeded", "error_description": "too many requests; retry later"})
}
