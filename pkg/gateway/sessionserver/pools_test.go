// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §15.1 line 703 — GET /v1/pools session-facing pool discovery.

type poolListResponse struct {
	Items   []sessionserver.PoolDiscoveryEntry `json:"items"`
	Cursor  string                             `json:"cursor"`
	HasMore bool                               `json:"hasMore"`
}

func getPools(t *testing.T, srv *sessionserver.Server, target string) poolListResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp poolListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	return resp
}

func poolNames(items []sessionserver.PoolDiscoveryEntry) map[string]sessionserver.PoolDiscoveryEntry {
	out := make(map[string]sessionserver.PoolDiscoveryEntry, len(items))
	for _, p := range items {
		out[p.Name] = p
	}
	return out
}

// A pool whose runtimeRef the caller can discover is surfaced with its
// warm-count and runtime ref; a pool backing a runtime that is not in the
// caller's discovery set is hidden, so the endpoint never widens runtime
// visibility. spec: §15.1 line 703.
func TestListPoolsVisibilityScoping_spec_15_1_703(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "claude-agent", Type: runtimestore.TypeAgent})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "gemini-agent", Type: runtimestore.TypeAgent})

	pools := poolstore.NewMemory()
	_ = pools.Create(context.Background(), poolstore.Pool{Name: "pool-claude", RuntimeRef: "claude-agent", WarmCount: 3})
	_ = pools.Create(context.Background(), poolstore.Pool{Name: "pool-gemini", RuntimeRef: "gemini-agent", WarmCount: 1})
	// Backs a runtime that is not registered, so it is invisible to the caller.
	_ = pools.Create(context.Background(), poolstore.Pool{Name: "pool-orphan", RuntimeRef: "ghost-agent", WarmCount: 9})

	srv := sessionserver.New(memstore.New(), sessionserver.Options{Runtimes: runtimes, Pools: pools})
	resp := getPools(t, srv, "/v1/pools")

	if len(resp.Items) != 2 {
		t.Fatalf("want 2 visible pools, got %d: %+v", len(resp.Items), resp.Items)
	}
	byName := poolNames(resp.Items)
	if _, ok := byName["pool-orphan"]; ok {
		t.Errorf("pool backing an undiscoverable runtime must be hidden")
	}
	if got := byName["pool-claude"]; got.WarmCount != 3 || got.RuntimeRef != "claude-agent" {
		t.Errorf("pool-claude projection wrong: %+v", got)
	}
	if got := byName["pool-gemini"]; got.ExecutionMode != "session" {
		t.Errorf("default execution mode should surface as session, got %q", got.ExecutionMode)
	}
}

// With pools wired but no runtime registry the §10.6 visibility set is
// empty, so the pool list fails closed to empty rather than leaking every
// pool. spec: §15.1 line 703; §10.6 transparent filter.
func TestListPoolsFailsClosedWithoutRuntimes_spec_10_6(t *testing.T) {
	pools := poolstore.NewMemory()
	_ = pools.Create(context.Background(), poolstore.Pool{Name: "pool-claude", RuntimeRef: "claude-agent", WarmCount: 3})

	srv := sessionserver.New(memstore.New(), sessionserver.Options{Pools: pools})
	resp := getPools(t, srv, "/v1/pools")
	if len(resp.Items) != 0 {
		t.Errorf("pool discovery without a runtime registry must be empty: %+v", resp.Items)
	}
}

// Without a pool registry the discovery list is empty (and well-formed).
func TestListPoolsEmptyWhenPoolsUnwired(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "claude-agent", Type: runtimestore.TypeAgent})

	srv := sessionserver.New(memstore.New(), sessionserver.Options{Runtimes: runtimes})
	resp := getPools(t, srv, "/v1/pools")
	if len(resp.Items) != 0 {
		t.Errorf("want empty list, got %+v", resp.Items)
	}
	if resp.HasMore {
		t.Errorf("empty list must not report hasMore")
	}
}

// A concurrent pool surfaces its §5.2 concurrencyStyle and maxConcurrent;
// a session pool omits them. spec: §5.2 line 703.
func TestListPoolsConcurrentFieldsSurface_spec_5_2(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "claude-agent", Type: runtimestore.TypeAgent})

	pools := poolstore.NewMemory()
	_ = pools.Create(context.Background(), poolstore.Pool{Name: "pool-session", RuntimeRef: "claude-agent", WarmCount: 2})
	_ = pools.Create(context.Background(), poolstore.Pool{
		Name:             "pool-concurrent",
		RuntimeRef:       "claude-agent",
		ExecutionMode:    runtimestore.ExecutionModeConcurrent,
		ConcurrencyStyle: poolstore.ConcurrencyStyleStateless,
		MaxConcurrent:    8,
		WarmCount:        1,
	})

	srv := sessionserver.New(memstore.New(), sessionserver.Options{Runtimes: runtimes, Pools: pools})
	byName := poolNames(getPools(t, srv, "/v1/pools").Items)

	if c := byName["pool-concurrent"]; c.ConcurrencyStyle != "stateless" || c.MaxConcurrent != 8 {
		t.Errorf("concurrent pool must surface concurrencyStyle/maxConcurrent: %+v", c)
	}
	if sPool := byName["pool-session"]; sPool.ConcurrencyStyle != "" || sPool.MaxConcurrent != 0 {
		t.Errorf("session pool must omit concurrency sub-fields: %+v", sPool)
	}
}

// The list honours the canonical §15.1 cursor-paginated envelope: a
// ?limit=1 request returns one item with hasMore set and a cursor that
// pages to the remaining pool. spec: §15.1 line 1228.
func TestListPoolsPaginationEnvelope_spec_15_1_1228(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "claude-agent", Type: runtimestore.TypeAgent})

	base := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	pools := poolstore.NewMemory()
	_ = pools.Create(context.Background(), poolstore.Pool{Name: "pool-older", RuntimeRef: "claude-agent", WarmCount: 1, CreatedAt: base})
	_ = pools.Create(context.Background(), poolstore.Pool{Name: "pool-newer", RuntimeRef: "claude-agent", WarmCount: 1, CreatedAt: base.Add(time.Hour)})

	srv := sessionserver.New(memstore.New(), sessionserver.Options{Runtimes: runtimes, Pools: pools})

	page1 := getPools(t, srv, "/v1/pools?limit=1")
	if len(page1.Items) != 1 || !page1.HasMore || page1.Cursor == "" {
		t.Fatalf("page 1: want 1 item, hasMore, cursor; got %+v", page1)
	}
	// Default sort is created_at:desc, so the newest pool comes first.
	if page1.Items[0].Name != "pool-newer" {
		t.Errorf("page 1 should hold the newest pool, got %q", page1.Items[0].Name)
	}

	page2 := getPools(t, srv, "/v1/pools?limit=1&cursor="+page1.Cursor)
	if len(page2.Items) != 1 || page2.HasMore {
		t.Fatalf("page 2: want 1 item and no more; got %+v", page2)
	}
	if page2.Items[0].Name != "pool-older" {
		t.Errorf("page 2 should hold the older pool, got %q", page2.Items[0].Name)
	}
}
