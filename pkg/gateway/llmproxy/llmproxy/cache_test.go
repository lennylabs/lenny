// SPDX-License-Identifier: MIT

package llmproxy_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
)

// spec: §4.9 lines 1542-1556 — the proxy consults the per-pool semantic
// cache on the non-streaming path: a hit replays the cached response
// without an upstream call, a miss forwards and records, and a streaming
// request bypasses the cache entirely.

// fakeCache records its calls. A non-nil hit makes Lookup a hit.
type fakeCache struct {
	hit     []byte
	lookups int
	stored  [][2]string
}

func (c *fakeCache) Lookup(_ context.Context, _ credential.Lease, _ []byte) ([]byte, bool) {
	c.lookups++
	if c.hit != nil {
		return c.hit, true
	}
	return nil, false
}

func (c *fakeCache) Store(_ context.Context, _ credential.Lease, reqBody, respBody []byte) {
	c.stored = append(c.stored, [2]string{string(reqBody), string(respBody)})
}

// TestHandlerServesCacheHitWithoutUpstream covers the §4.9 cache-hit
// path: the cached body is returned and no upstream call is made.
func TestHandlerServesCacheHitWithoutUpstream(t *testing.T) {
	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-hit")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	cache := &fakeCache{hit: []byte(`{"id":"cached_msg"}`)}
	h.handler.Cache = cache

	rr := post(h.handler, "lt-hit", messagesBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"id":"cached_msg"`) {
		t.Errorf("body = %q, want the cached response", rr.Body.String())
	}
	if cache.lookups != 1 {
		t.Errorf("Lookup called %d times, want 1", cache.lookups)
	}
	if *h.gotKey != "" {
		t.Error("upstream was called on a cache hit; it must be served from cache")
	}
}

// TestHandlerStoresOnCacheMiss covers the §4.9 cache-miss path: the proxy
// forwards upstream and records the translated response for a later hit.
func TestHandlerStoresOnCacheMiss(t *testing.T) {
	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-miss")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	cache := &fakeCache{} // miss
	h.handler.Cache = cache

	rr := post(h.handler, "lt-miss", messagesBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if *h.gotKey == "" {
		t.Error("upstream was not called on a cache miss")
	}
	if len(cache.stored) != 1 {
		t.Fatalf("Store called %d times, want 1", len(cache.stored))
	}
	if cache.stored[0][0] != messagesBody {
		t.Errorf("stored request = %q, want the proxied request body", cache.stored[0][0])
	}
	if !strings.Contains(cache.stored[0][1], "msg_1") {
		t.Errorf("stored response = %q, want the translated upstream response", cache.stored[0][1])
	}
}

// TestHandlerStreamingBypassesCache covers the §4.9 rule that only
// non-streaming requests are cached: a streaming request never consults
// the cache.
func TestHandlerStreamingBypassesCache(t *testing.T) {
	h := newProxyHarness(t)
	if err := h.leases.Put(handlerLease("lt-stream")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	cache := &fakeCache{hit: []byte(`{"id":"cached_msg"}`)}
	h.handler.Cache = cache

	rr := post(h.handler, "lt-stream", `{"model":"claude-3-5-sonnet","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if cache.lookups != 0 {
		t.Errorf("Lookup called %d times on a streaming request, want 0", cache.lookups)
	}
	if len(cache.stored) != 0 {
		t.Errorf("Store called %d times on a streaming request, want 0", len(cache.stored))
	}
}

// TestHandlerNilCacheProxiesNormally confirms a nil Cache leaves the
// proxy path unchanged (the §4.9 caching-off default).
func TestHandlerNilCacheProxiesNormally(t *testing.T) {
	h := newProxyHarness(t) // Cache is nil
	if err := h.leases.Put(handlerLease("lt-nilcache")); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	rr := post(h.handler, "lt-nilcache", messagesBody)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if *h.gotKey == "" {
		t.Error("upstream was not called with a nil cache")
	}
}
