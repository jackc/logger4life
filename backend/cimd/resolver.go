package cimd

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/domain"
	"golang.org/x/time/rate"
)

const (
	fetchTimeout         = 5 * time.Second
	maxDocumentBytes     = 5 << 10
	maxCacheEntries      = 1024
	maxConcurrentFetches = 8
)

type cacheEntry struct {
	client  core.OAuthClient
	expires time.Time
}

type pendingFetch struct {
	done   chan struct{}
	client core.OAuthClient
	err    error
}

type Resolver struct {
	client  *http.Client
	now     func() time.Time
	mu      sync.Mutex
	cache   map[string]cacheEntry
	pending map[string]*pendingFetch
	rate    *rate.Limiter
}

var _ core.OAuthClientMetadataResolver = (*Resolver)(nil)

func New() *Resolver {
	return &Resolver{client: newHTTPClient(), now: time.Now,
		cache: make(map[string]cacheEntry), pending: make(map[string]*pendingFetch),
		rate: rate.NewLimiter(rate.Every(time.Second), 10)}
}

func cloneClient(client core.OAuthClient) core.OAuthClient {
	client.RedirectURIs = slices.Clone(client.RedirectURIs)
	return client
}

func (r *Resolver) ResolveOAuthClient(ctx context.Context, id string) (core.OAuthClient, error) {
	if !domain.ValidClientMetadataURL(id) {
		return core.OAuthClient{}, errMetadata
	}
	if err := ctx.Err(); err != nil {
		return core.OAuthClient{}, err
	}
	r.mu.Lock()
	if entry, ok := r.cache[id]; ok && r.now().Before(entry.expires) {
		r.mu.Unlock()
		return cloneClient(entry.client), nil
	}
	delete(r.cache, id) // No stale fallback, including after network errors.
	if pending, ok := r.pending[id]; ok {
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return core.OAuthClient{}, ctx.Err()
		case <-pending.done:
			return cloneClient(pending.client), pending.err
		}
	}
	if len(r.pending) >= maxConcurrentFetches || !r.rate.Allow() {
		r.mu.Unlock()
		return core.OAuthClient{}, errors.New("client metadata fetch capacity reached")
	}
	pending := &pendingFetch{done: make(chan struct{})}
	r.pending[id] = pending
	r.mu.Unlock()

	fetchCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	client, expires, err := r.fetch(fetchCtx, id)
	cancel()
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil && r.now().Before(expires) {
		if len(r.cache) >= maxCacheEntries {
			// Evict the entry expiring first; stale entries are removed first.
			var oldest string
			var earliest time.Time
			for key, entry := range r.cache {
				if oldest == "" || entry.expires.Before(earliest) {
					oldest, earliest = key, entry.expires
				}
			}
			delete(r.cache, oldest)
		}
		r.cache[id] = cacheEntry{client: cloneClient(client), expires: expires}
	}
	// In-flight callers share the result, but errors and uncacheable
	// documents are never retained for subsequent authorization requests.
	pending.client, pending.err = client, err
	delete(r.pending, id)
	close(pending.done)
	return cloneClient(client), err
}

func (r *Resolver) fetch(ctx context.Context, id string) (core.OAuthClient, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, id, nil)
	if err != nil {
		return core.OAuthClient{}, time.Time{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Logger4Life-CIMD/1")
	started := r.now()
	resp, err := r.client.Do(req)
	if err != nil {
		return core.OAuthClient{}, time.Time{}, err
	}
	defer resp.Body.Close()
	contentType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode != http.StatusOK || err != nil || contentType != "application/json" ||
		resp.Header.Get("Content-Encoding") != "" || resp.ContentLength > maxDocumentBytes {
		return core.OAuthClient{}, time.Time{}, errMetadata
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDocumentBytes+1))
	if err != nil || len(body) > maxDocumentBytes {
		return core.OAuthClient{}, time.Time{}, errMetadata
	}
	client, err := parseMetadata(id, body)
	if err != nil {
		return core.OAuthClient{}, time.Time{}, err
	}
	return client, cacheExpiry(resp.Header, started, r.now()), nil
}
