package server

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKeyedRateLimiter(t *testing.T) {
	now := time.Now()
	l := newKeyedRateLimiter(60, 2)
	l.now = func() time.Time { return now }
	l.maxKeys = 2
	require.Zero(t, l.allow("alice"))
	require.Zero(t, l.allow("alice"))
	require.Equal(t, time.Second, l.allow("alice"))
	require.Zero(t, l.allow("bob"))
	require.Positive(t, l.allow("carol"))
	now = now.Add(time.Second)
	require.Zero(t, l.allow("alice"))
	require.Positive(t, l.allow("alice"))
	now = now.Add(l.idle)
	require.Zero(t, l.allow("carol"))
	require.Len(t, l.entries, 1)
}

func TestKeyedRateLimiterConcurrentBurst(t *testing.T) {
	l := newKeyedRateLimiter(60, 10)
	now := time.Now()
	l.now = func() time.Time { return now }
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			if l.allow("alice") == 0 {
				admitted.Add(1)
			}
		})
	}
	wg.Wait()
	require.EqualValues(t, 10, admitted.Load())
}

func TestMCPHTTPRateLimit(t *testing.T) {
	handler := newMCPHTTPTestHandler(&mcpUserSQLExecutor{})
	for range 10 {
		w, _ := serveMCPJSON(t, handler, newMCPHTTPRequest(t, "2026-07-28", "tools/list", nil))
		require.Equal(t, http.StatusOK, w.Code)
	}
	// Removing modern headers or switching protocol must not bypass the cap.
	req := newMCPHTTPRequest(t, "2025-11-25", "tools/list", nil)
	w, _ := serveMCPJSON(t, handler, req)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.NotEmpty(t, w.Header().Get("Retry-After"))
	require.Empty(t, w.Header().Get("WWW-Authenticate"))
	req = newMCPHTTPRequest(t, "2026-07-28", "tools/list", nil)
	req.Header.Set("Authorization", "Bearer bob-token")
	w, _ = serveMCPJSON(t, handler, req)
	require.Equal(t, http.StatusOK, w.Code)
}
