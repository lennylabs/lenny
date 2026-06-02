// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// newMCPWithPools registers the §9.2 elicitation MCP surface with a pool
// registry so the dispatch path resolves the per-pool elicitation policy
// from the raising session's pool (F-9.2.12). The Deps elicitation
// fields are intentionally left at their zero value so a passing test
// proves the configuration came from the pool, not from the Register-
// time platform default.
func newMCPWithPools(t *testing.T, pools poolstore.Store, timeout time.Duration) (*mcp.Server, sessionstore.Store, interactionstore.Store) {
	t.Helper()
	store := memstore.New()
	interactions := interactionstore.NewMemory()
	srv := mcp.NewServer()
	if timeout == 0 {
		timeout = 2 * time.Second
	}
	mcptools.Register(srv, mcptools.Deps{
		Store:              store,
		Interactions:       interactions,
		Pools:              pools,
		ElicitationTimeout: timeout,
		IDFunc:             func() string { return "elic_gen" },
		TenantID:           "acme",
	})
	return srv, store, interactions
}

// mkPooledSession seeds a session bound to poolRef so the §9.2 dispatch
// path can resolve the per-pool elicitation policy. spec: §9.2 lines 86,
// 90-98; F-9.2.12.
func mkPooledSession(t *testing.T, store sessionstore.Store, id, user, parent, poolRef string) {
	t.Helper()
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", UserID: user, State: session.StateRunning,
		ParentSessionID: parent, PoolRef: poolRef, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
}

func seedPool(t *testing.T, pools poolstore.Store, p poolstore.Pool) {
	t.Helper()
	if err := pools.Create(context.Background(), p); err != nil {
		t.Fatalf("seed pool %s: %v", p.Name, err)
	}
}

// TestRequestElicitationPerPoolURLModeAllowsConfiguredDomain_spec_9_2_F_9_2_12
// proves the §9.2 line 86 per-pool agent-initiated url-mode allowlist is
// resolved from the raising session's pool: a session in a pool that
// allowlists accounts.example.com may raise a url-mode elicitation to
// that domain even though the Register-time Deps allowlist is empty.
func TestRequestElicitationPerPoolURLModeAllowsConfiguredDomain_spec_9_2_F_9_2_12(t *testing.T) {
	pools := poolstore.NewMemory()
	seedPool(t, pools, poolstore.Pool{
		Name: "pool-oauth",
		URLModeElicitation: elicitation.URLModeAllowlist{
			Enabled:         true,
			DomainAllowlist: []string{"accounts.example.com"},
		},
	})
	srv, store, interactions := newMCPWithPools(t, pools, 2*time.Second)
	mkPooledSession(t, store, "sess_root", "alice", "", "pool-oauth")
	h := srv.Handler()

	go func() {
		_ = call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_root","message":"sign in","schema":{},"url":"https://accounts.example.com/oauth/authorize","elicitationId":"elic_x"}`)
	}()
	// A url-mode elicitation permitted by the pool's allowlist forwards up
	// the chain and is recorded for resolution.
	waitElicitationFor(t, interactions, "sess_root", "alice", "elic_x")
}

// TestRequestElicitationPerPoolURLModeBlocksUnconfiguredPool_spec_9_2_F_9_2_12
// proves the §9.2 default holds per pool: a session in a pool that does
// not allowlist url-mode is blocked from raising a url-mode elicitation
// even when another pool would allow it.
func TestRequestElicitationPerPoolURLModeBlocksUnconfiguredPool_spec_9_2_F_9_2_12(t *testing.T) {
	pools := poolstore.NewMemory()
	seedPool(t, pools, poolstore.Pool{
		Name: "pool-oauth",
		URLModeElicitation: elicitation.URLModeAllowlist{
			Enabled:         true,
			DomainAllowlist: []string{"accounts.example.com"},
		},
	})
	seedPool(t, pools, poolstore.Pool{Name: "pool-default"})
	srv, store, interactions := newMCPWithPools(t, pools, time.Second)
	// The raising session is in pool-default, which does not allowlist
	// url-mode, so the elicitation must be blocked.
	mkPooledSession(t, store, "sess_root", "alice", "", "pool-default")

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_root","message":"sign in","schema":{},"url":"https://accounts.example.com/oauth/authorize","elicitationId":"elic_x"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a url-mode elicitation in a non-allowlisting pool should be blocked: %+v", resp)
	}
	if _, err := interactions.Get(context.Background(), "acme", "sess_root", "alice", "elic_x"); err == nil {
		t.Error("a blocked url-mode elicitation was recorded in the interaction store")
	}
}

// TestRequestElicitationPerPoolDepthBlockAll_spec_9_2_F_9_2_12 proves the
// §9.2 line 96 `block_all` depth policy is resolved from the raising
// session's pool: a delegated session (depth > 0) whose pool blocks all
// elicitations is suppressed.
func TestRequestElicitationPerPoolDepthBlockAll_spec_9_2_F_9_2_12(t *testing.T) {
	pools := poolstore.NewMemory()
	seedPool(t, pools, poolstore.Pool{
		Name:                   "pool-locked",
		ElicitationDepthPolicy: elicitation.DepthBlockAll,
	})
	srv, store, interactions := newMCPWithPools(t, pools, time.Second)
	// §9.2 line 96: block_all suppresses delegated sessions; the leaf is
	// at depth 1.
	mkPooledSession(t, store, "sess_root", "alice", "", "pool-locked")
	mkPooledSession(t, store, "sess_leaf", "alice", "sess_root", "pool-locked")

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_leaf","message":"ask","schema":{},"elicitationId":"elic_x"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "suppressed") {
		t.Errorf("result = %q, want a SUPPRESSED response under block_all", text)
	}
	for _, sid := range []string{"sess_leaf", "sess_root"} {
		if _, err := interactions.Get(context.Background(), "acme", sid, "alice", "elic_x"); err == nil {
			t.Errorf("a block_all-suppressed elicitation was recorded against %s", sid)
		}
	}
}

// TestRequestElicitationPerPoolAllowAllOverridesDefault_spec_9_2_F_9_2_12
// proves a pool's explicit `allow_all` overrides the §9.2 line 92
// platform default (suppress at depth 3): a depth-3 session in an
// allow_all pool is NOT suppressed, whereas the Register-time default
// (empty depth policy coerced to suppress_at_depth=3 by WalkChain) would
// suppress it.
func TestRequestElicitationPerPoolAllowAllOverridesDefault_spec_9_2_F_9_2_12(t *testing.T) {
	pools := poolstore.NewMemory()
	seedPool(t, pools, poolstore.Pool{
		Name:                   "pool-open",
		ElicitationDepthPolicy: elicitation.DepthAllowAll,
	})
	srv, store, interactions := newMCPWithPools(t, pools, 2*time.Second)
	// root(0) → a(1) → b(2) → leaf(3); the leaf raises at depth 3.
	mkPooledSession(t, store, "sess_root", "alice", "", "pool-open")
	mkPooledSession(t, store, "sess_a", "alice", "sess_root", "pool-open")
	mkPooledSession(t, store, "sess_b", "alice", "sess_a", "pool-open")
	mkPooledSession(t, store, "sess_leaf", "alice", "sess_b", "pool-open")
	h := srv.Handler()

	go func() {
		_ = call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_leaf","message":"ask","schema":{},"elicitationId":"elic_x"}`)
	}()
	// allow_all defeats the depth-3 platform default; the elicitation
	// forwards to the human-facing root and is recorded there.
	waitElicitationFor(t, interactions, "sess_root", "alice", "elic_x")
}

// TestRequestElicitationDefaultPoolSuppressesAtDepth3_spec_9_2_F_9_2_12
// is the companion to the allow_all test: a depth-3 session in a pool
// with no explicit depth policy inherits the §9.2 line 92 platform
// default and is suppressed.
func TestRequestElicitationDefaultPoolSuppressesAtDepth3_spec_9_2_F_9_2_12(t *testing.T) {
	pools := poolstore.NewMemory()
	seedPool(t, pools, poolstore.Pool{Name: "pool-default"})
	srv, store, interactions := newMCPWithPools(t, pools, time.Second)
	mkPooledSession(t, store, "sess_root", "alice", "", "pool-default")
	mkPooledSession(t, store, "sess_a", "alice", "sess_root", "pool-default")
	mkPooledSession(t, store, "sess_b", "alice", "sess_a", "pool-default")
	mkPooledSession(t, store, "sess_leaf", "alice", "sess_b", "pool-default") // depth 3

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_leaf","message":"ask","schema":{},"elicitationId":"elic_x"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "suppressed") {
		t.Errorf("result = %q, want SUPPRESSED at depth 3 under the platform default", text)
	}
	if _, err := interactions.Get(context.Background(), "acme", "sess_root", "alice", "elic_x"); err == nil {
		t.Error("a depth-3 suppressed elicitation was recorded")
	}
}
