// SPDX-License-Identifier: MIT

package proxycache_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/proxycache"
	"github.com/lennylabs/lenny/pkg/gateway/semanticcache"
)

// spec: §4.9 lines 1542-1556 — the SemanticCache wiring on the LLM proxy
// path: per-pool opt-in, the §12.4 (tenant, scope, model, provider) key
// space, and per-user keying on the session's owning user.

// fakePools answers Get with one pool. A nil pool returns ErrNotFound.
type fakePools struct {
	pool *credentialpoolstore.CredentialPool
}

func (f fakePools) Get(_ context.Context, tenantID, name string) (credentialpoolstore.CredentialPool, error) {
	if f.pool == nil || f.pool.TenantID != tenantID || f.pool.Name != name {
		return credentialpoolstore.CredentialPool{}, credentialpoolstore.ErrNotFound
	}
	return *f.pool, nil
}

// fakeUsers resolves every session to a fixed user, or to a miss.
type fakeUsers struct {
	uid string
	ok  bool
}

func (f fakeUsers) UserID(context.Context, string, string) (string, bool) { return f.uid, f.ok }

func poolWith(scope string, cp *credentialpoolstore.CachePolicy) *credentialpoolstore.CredentialPool {
	return &credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: "claude-prod", Provider: "anthropic_direct",
		CacheScope: scope, CachePolicy: cp,
	}
}

func lease() credential.Lease {
	return credential.Lease{
		LeaseID: "cl_1", SessionID: "s_1", TenantID: "acme",
		Provider: credential.ProviderAnthropicDirect, Source: credential.SourcePool,
		PoolID: "claude-prod", CredentialID: "key-1", DeliveryMode: credential.DeliveryProxy,
	}
}

func newStore(t *testing.T) semanticcache.Store {
	t.Helper()
	// A nil embedder selects the hashing embedder, so exact-text repeats
	// hit; a threshold of 0 lets the round-trip assertions hold.
	return semanticcache.NewInMemory(nil, 0, 0, nil)
}

const (
	reqA = `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hi"}]}`
	resp = `{"id":"msg_1","content":"hello"}`
)

// TestDisabledPoolIsUncached covers §4.9 line 1549: caching is disabled
// by default and opt-in per pool. A nil CachePolicy, or one with Enabled
// false, never stores or hits.
func TestDisabledPoolIsUncached(t *testing.T) {
	ctx := context.Background()
	for name, cp := range map[string]*credentialpoolstore.CachePolicy{
		"nil policy":      nil,
		"disabled policy": {Enabled: false, Strategy: "semantic"},
	} {
		a := proxycache.New(fakePools{poolWith("per-user", cp)}, newStore(t), fakeUsers{uid: "alice", ok: true})
		a.Store(ctx, lease(), []byte(reqA), []byte(resp))
		if _, hit := a.Lookup(ctx, lease(), []byte(reqA)); hit {
			t.Errorf("%s: Lookup hit, want miss (caching off)", name)
		}
	}
}

// TestEnabledPerUserHitAfterStore covers the §4.9 default per-user scope:
// once stored, the same request from the same user hits.
func TestEnabledPerUserHitAfterStore(t *testing.T) {
	ctx := context.Background()
	cp := &credentialpoolstore.CachePolicy{Enabled: true, Strategy: "semantic"}
	a := proxycache.New(fakePools{poolWith("per-user", cp)}, newStore(t), fakeUsers{uid: "alice", ok: true})
	if _, hit := a.Lookup(ctx, lease(), []byte(reqA)); hit {
		t.Fatal("Lookup hit before any Store")
	}
	a.Store(ctx, lease(), []byte(reqA), []byte(resp))
	got, hit := a.Lookup(ctx, lease(), []byte(reqA))
	if !hit {
		t.Fatal("Lookup miss after Store, want hit")
	}
	if string(got) != resp {
		t.Errorf("cached response = %q, want %q", got, resp)
	}
}

// TestPerUserUnkeyableWhenUserUnresolved covers the §4.9 rule that a
// per-user-scoped request whose user cannot be resolved is left uncached
// rather than keyed without a user id.
func TestPerUserUnkeyableWhenUserUnresolved(t *testing.T) {
	ctx := context.Background()
	cp := &credentialpoolstore.CachePolicy{Enabled: true}
	// A user-lookup miss and a nil lookup both leave it uncached.
	for name, users := range map[string]proxycache.SessionUserLookup{
		"user miss":  fakeUsers{ok: false},
		"nil lookup": nil,
	} {
		a := proxycache.New(fakePools{poolWith("per-user", cp)}, newStore(t), users)
		a.Store(ctx, lease(), []byte(reqA), []byte(resp))
		if _, hit := a.Lookup(ctx, lease(), []byte(reqA)); hit {
			t.Errorf("%s: Lookup hit, want miss (no user id to key on)", name)
		}
	}
}

// TestPerSessionScopeKeysOnSession covers the per-session scope: it keys
// on the lease's session id and does not consult the user lookup.
func TestPerSessionScopeKeysOnSession(t *testing.T) {
	ctx := context.Background()
	cp := &credentialpoolstore.CachePolicy{Enabled: true}
	a := proxycache.New(fakePools{poolWith("per-session", cp)}, newStore(t), nil)
	a.Store(ctx, lease(), []byte(reqA), []byte(resp))
	if _, hit := a.Lookup(ctx, lease(), []byte(reqA)); !hit {
		t.Error("per-session Lookup miss after Store, want hit (keyed on session)")
	}
}

// TestTenantScopeNeedsNoUser covers the tenant scope: cross-user sharing
// within the tenant, keyed without a user id.
func TestTenantScopeNeedsNoUser(t *testing.T) {
	ctx := context.Background()
	cp := &credentialpoolstore.CachePolicy{Enabled: true}
	a := proxycache.New(fakePools{poolWith("tenant", cp)}, newStore(t), nil)
	a.Store(ctx, lease(), []byte(reqA), []byte(resp))
	if _, hit := a.Lookup(ctx, lease(), []byte(reqA)); !hit {
		t.Error("tenant-scope Lookup miss after Store, want hit")
	}
}

// TestModelIsAKeyDimension covers the §4.9 (model, provider) key space: a
// response cached for one model is not returned for another.
func TestModelIsAKeyDimension(t *testing.T) {
	ctx := context.Background()
	cp := &credentialpoolstore.CachePolicy{Enabled: true}
	a := proxycache.New(fakePools{poolWith("tenant", cp)}, newStore(t), nil)
	a.Store(ctx, lease(), []byte(`{"model":"claude-3-5-sonnet","messages":[]}`), []byte(resp))
	if _, hit := a.Lookup(ctx, lease(), []byte(`{"model":"claude-3-opus","messages":[]}`)); hit {
		t.Error("Lookup hit across different models, want miss (model is a key dimension)")
	}
}

// TestUnknownPoolIsUncached covers a lease whose pool no longer resolves:
// it is a miss, never an error.
func TestUnknownPoolIsUncached(t *testing.T) {
	ctx := context.Background()
	a := proxycache.New(fakePools{pool: nil}, newStore(t), fakeUsers{uid: "alice", ok: true})
	a.Store(ctx, lease(), []byte(reqA), []byte(resp))
	if _, hit := a.Lookup(ctx, lease(), []byte(reqA)); hit {
		t.Error("Lookup hit for an unknown pool, want miss")
	}
}
