package cimd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(body []byte, h http.Header) *http.Response {
	if h == nil {
		h = make(http.Header)
	}
	h.Set("Content-Type", "application/json; charset=utf-8")
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(string(body)))}
}

func testResolver(t *testing.T, handler roundTripFunc) *Resolver {
	t.Helper()
	r := New()
	r.client.Transport = handler
	r.rate = rate.NewLimiter(rate.Inf, 1)
	return r
}

func TestResolverFreshnessAndChanges(t *testing.T) {
	now := time.Now()
	doc := metadataDocument()
	var requests int
	fail := false
	r := testResolver(t, func(req *http.Request) (*http.Response, error) {
		requests++
		if fail {
			return nil, fmt.Errorf("private infrastructure failure")
		}
		return response(documentJSON(t, doc), http.Header{"Cache-Control": {"max-age=60"}}), nil
	})
	r.now = func() time.Time { return now }
	client, err := r.ResolveOAuthClient(context.Background(), testID)
	require.NoError(t, err)
	client.RedirectURIs[0] = "https://attacker.example/cb"
	client, err = r.ResolveOAuthClient(context.Background(), testID)
	require.NoError(t, err)
	require.Equal(t, "https://client.example/callback", client.RedirectURIs[0], "callers cannot mutate cached redirect binding")
	require.Equal(t, 1, requests)
	now = now.Add(time.Minute)
	doc["redirect_uris"] = []string{"https://client.example/new-callback"}
	client, err = r.ResolveOAuthClient(context.Background(), testID)
	require.NoError(t, err)
	require.Equal(t, []string{"https://client.example/new-callback"}, client.RedirectURIs)
	require.Equal(t, 2, requests)
	now = now.Add(time.Minute)
	fail = true
	_, err = r.ResolveOAuthClient(context.Background(), testID)
	require.Error(t, err)
	require.Empty(t, r.cache, "failed re-fetch must never fall back to old metadata")
	_, err = r.ResolveOAuthClient(context.Background(), testID)
	require.Error(t, err)
	require.Equal(t, 4, requests, "errors must not be cached")
	fail = false
	doc["client_id"] = "https://example.com/changed-identity"
	_, err = r.ResolveOAuthClient(context.Background(), testID)
	require.Error(t, err)
	require.Empty(t, r.cache)
}

func TestResolverRejectsFetchFailures(t *testing.T) {
	valid := documentJSON(t, metadataDocument())
	for _, tc := range []struct {
		name   string
		change func(*http.Response)
	}{
		{"redirect", func(r *http.Response) { r.StatusCode = 302; r.Header.Set("Location", "https://127.0.0.1/internal") }},
		{"not found", func(r *http.Response) { r.StatusCode = 404 }},
		{"not modified without stored validator", func(r *http.Response) { r.StatusCode = 304 }},
		{"partial", func(r *http.Response) { r.StatusCode = 206 }},
		{"wrong content type", func(r *http.Response) { r.Header.Set("Content-Type", "text/html") }},
		{"missing content type", func(r *http.Response) { r.Header.Del("Content-Type") }},
		{"compressed", func(r *http.Response) { r.Header.Set("Content-Encoding", "gzip") }},
		{"oversized length", func(r *http.Response) { r.ContentLength = maxDocumentBytes + 1 }},
		{"oversized stream", func(r *http.Response) {
			r.Body = io.NopCloser(strings.NewReader(string(valid) + strings.Repeat(" ", maxDocumentBytes)))
		}},
		{"invalid JSON", func(r *http.Response) { r.Body = io.NopCloser(strings.NewReader("{}")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			r := testResolver(t, func(req *http.Request) (*http.Response, error) {
				requests++
				require.Equal(t, testID, req.URL.String(), "never follow redirects")
				resp := response(valid, nil)
				tc.change(resp)
				return resp, nil
			})
			for range 2 {
				_, err := r.ResolveOAuthClient(context.Background(), testID)
				require.Error(t, err)
			}
			require.Equal(t, 2, requests)
			require.Empty(t, r.cache)
		})
	}
	for _, policy := range []string{"no-store", "no-cache", "private", "max-age=0"} {
		t.Run(policy, func(t *testing.T) {
			requests := 0
			r := testResolver(t, func(*http.Request) (*http.Response, error) {
				requests++
				return response(valid, http.Header{"Cache-Control": {policy}}), nil
			})
			for range 2 {
				_, err := r.ResolveOAuthClient(context.Background(), testID)
				require.NoError(t, err)
			}
			require.Equal(t, 2, requests)
			require.Empty(t, r.cache)
		})
	}
}

func TestResolverBoundsAndCancellation(t *testing.T) {
	t.Run("cache", func(t *testing.T) {
		r := testResolver(t, func(req *http.Request) (*http.Response, error) {
			doc := metadataDocument()
			doc["client_id"] = req.URL.String()
			return response(documentJSON(t, doc), nil), nil
		})
		for i := range maxCacheEntries + 5 {
			_, err := r.ResolveOAuthClient(context.Background(), fmt.Sprintf("%s?client=%d", testID, i))
			require.NoError(t, err)
		}
		require.Len(t, r.cache, maxCacheEntries)
	})
	t.Run("concurrency", func(t *testing.T) {
		started := make(chan struct{}, maxConcurrentFetches)
		r := testResolver(t, func(req *http.Request) (*http.Response, error) {
			deadline, ok := req.Context().Deadline()
			require.True(t, ok)
			require.LessOrEqual(t, time.Until(deadline), fetchTimeout)
			started <- struct{}{}
			<-req.Context().Done()
			return nil, req.Context().Err()
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var wg sync.WaitGroup
		for i := range maxConcurrentFetches {
			wg.Go(func() { _, _ = r.ResolveOAuthClient(ctx, fmt.Sprintf("%s?client=%d", testID, i)) })
		}
		for range maxConcurrentFetches {
			<-started
		}
		_, err := r.ResolveOAuthClient(ctx, testID+"?excess")
		require.ErrorContains(t, err, "capacity")
		waitCtx, waitCancel := context.WithCancel(ctx)
		waitCancel()
		_, err = r.ResolveOAuthClient(waitCtx, testID+"?client=0")
		require.ErrorIs(t, err, context.Canceled)
		cancel()
		wg.Wait()
		require.Empty(t, r.pending)
		require.Empty(t, r.cache)
	})
	t.Run("coalescing", func(t *testing.T) {
		var requests atomic.Int32
		body := documentJSON(t, metadataDocument())
		r := testResolver(t, func(*http.Request) (*http.Response, error) {
			requests.Add(1)
			return response(body, nil), nil
		})
		var wg sync.WaitGroup
		for range 30 {
			wg.Go(func() { _, err := r.ResolveOAuthClient(context.Background(), testID); require.NoError(t, err) })
		}
		wg.Wait()
		require.EqualValues(t, 1, requests.Load())
	})
	t.Run("rate", func(t *testing.T) {
		r := testResolver(t, func(*http.Request) (*http.Response, error) { t.Fatal("rate-limited request fetched"); return nil, nil })
		r.rate = rate.NewLimiter(0, 1)
		require.True(t, r.rate.Allow())
		_, err := r.ResolveOAuthClient(context.Background(), testID)
		require.ErrorContains(t, err, "capacity")
	})
}

func TestCacheExpiry(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name   string
		header http.Header
		ttl    time.Duration
	}{
		{"default", nil, 5 * time.Minute},
		{"max age", http.Header{"Cache-Control": {"max-age=60"}}, time.Minute},
		{"upper bound", http.Header{"Cache-Control": {"max-age=86400"}}, time.Hour},
		{"shared max age", http.Header{"Cache-Control": {"max-age=60, s-maxage=30"}}, 30 * time.Second},
		{"age", http.Header{"Cache-Control": {`max-age="60"`}, "Age": {"50"}}, 10 * time.Second},
		{"old date", http.Header{"Cache-Control": {"max-age=60"}, "Date": {now.Add(-50 * time.Second).Format(http.TimeFormat)}}, 10 * time.Second},
		{"expired", http.Header{"Cache-Control": {"max-age=60"}, "Age": {"60"}}, 0},
		{"expires", http.Header{"Expires": {now.Add(time.Minute).Format(http.TimeFormat)}}, time.Minute},
		{"private", http.Header{"Cache-Control": {"max-age=60, private"}}, 0},
		{"no store", http.Header{"Cache-Control": {"max-age=60", "NO-STORE"}}, 0},
		{"revalidate", http.Header{"Cache-Control": {"max-age=60, no-cache"}}, 0},
		{"duplicate", http.Header{"Cache-Control": {"max-age=60, max-age=30"}}, 0},
		{"malformed", http.Header{"Cache-Control": {"max-age=xyz"}}, 0},
		{"overflow", http.Header{"Cache-Control": {"max-age=9999999999999999"}}, 0},
		{"bad age", http.Header{"Age": {"-1"}}, 0},
		{"bad expires", http.Header{"Expires": {"0"}}, 0},
		{"vary", http.Header{"Vary": {"*"}}, 0},
		{"pragma", http.Header{"Pragma": {"no-cache"}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expiry := cacheExpiry(tc.header, now, now)
			if tc.ttl == 0 {
				require.True(t, expiry.IsZero())
			} else {
				require.Equal(t, now.Add(tc.ttl), expiry)
			}
		})
	}
	require.Equal(t, now.Add(time.Minute-time.Second), cacheExpiry(http.Header{"Cache-Control": {"max-age=60"}}, now.Add(-time.Second), now))
}
