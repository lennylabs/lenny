// SPDX-License-Identifier: MIT

package credentialpoolstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
)

// spec: §4.9 CredentialPool registry.

func samplePool(tenant, name string) credentialpoolstore.CredentialPool {
	return credentialpoolstore.CredentialPool{
		TenantID: tenant,
		Name:     name,
		Provider: "anthropic_direct",
		Credentials: []credentialpoolstore.Credential{
			{ID: "key-1", SecretRef: "lenny-system/anthropic-key-1"},
			{ID: "key-2", SecretRef: "lenny-system/anthropic-key-2"},
		},
		AssignmentStrategy:         "least-loaded",
		MaxConcurrentSessions:      10,
		CooldownOnRateLimitSeconds: 60,
	}
}

// TestValidateCacheScope covers the §4.9 cacheScope enum on the pool
// model: empty and the three named scopes are accepted, anything else
// is rejected by Validate (and so by Create/Update on every backend).
func TestValidateCacheScope(t *testing.T) {
	for _, scope := range []string{"", "per-user", "per-session", "tenant"} {
		p := samplePool("acme", "pool-cs")
		p.CacheScope = scope
		if err := credentialpoolstore.Validate(p); err != nil {
			t.Errorf("Validate cacheScope %q: %v, want nil", scope, err)
		}
	}
	p := samplePool("acme", "pool-bad")
	p.CacheScope = "global"
	if err := credentialpoolstore.Validate(p); err == nil {
		t.Error("Validate accepted an out-of-enum cacheScope")
	}
}

// TestValidateProxyFields covers the §4.9 proxy-mode pool fields:
// deliveryMode and proxyDialect are closed enums (empty accepted), and
// proxyEndpoint must use the https:// scheme.
//
// spec: §4.9 lines 1481 (dialects), 1503-1511 (pool example), 1513
// (InvalidProxyEndpointScheme).
func TestValidateProxyFields(t *testing.T) {
	for _, m := range []string{"", "proxy", "direct"} {
		p := samplePool("acme", "pool-dm")
		p.DeliveryMode = m
		if err := credentialpoolstore.Validate(p); err != nil {
			t.Errorf("Validate deliveryMode %q: %v, want nil", m, err)
		}
	}
	// spec: §4.9 lines 1473-1476 launch dialects (anthropic, openai);
	// §26.5 / §26.8 / §26.9 add `google`; §26.6 line 297 adds `cursor`.
	for _, d := range []string{"", "openai", "anthropic", "google", "cursor"} {
		p := samplePool("acme", "pool-pd")
		p.ProxyDialect = d
		if err := credentialpoolstore.Validate(p); err != nil {
			t.Errorf("Validate proxyDialect %q: %v, want nil", d, err)
		}
	}
	bad := samplePool("acme", "pool-bad-dm")
	bad.DeliveryMode = "passthrough"
	if err := credentialpoolstore.Validate(bad); err == nil {
		t.Error("Validate accepted an out-of-enum deliveryMode")
	}
	badDialect := samplePool("acme", "pool-bad-pd")
	badDialect.ProxyDialect = "grpc"
	if err := credentialpoolstore.Validate(badDialect); err == nil {
		t.Error("Validate accepted an out-of-enum proxyDialect")
	}
}

// TestValidateProxyEndpointScheme covers the §4.9 line 1513 rule: a
// proxyEndpoint must use https://; http:// and other schemes are
// rejected with ErrInvalidProxyEndpointScheme. Empty inherits the
// gateway default.
func TestValidateProxyEndpointScheme(t *testing.T) {
	for _, ep := range []string{"", "https://gateway-internal:8443/llm-proxy", "HTTPS://gw/llm"} {
		p := samplePool("acme", "pool-ep")
		p.ProxyEndpoint = ep
		if err := credentialpoolstore.Validate(p); err != nil {
			t.Errorf("Validate proxyEndpoint %q: %v, want nil", ep, err)
		}
	}
	for _, ep := range []string{"http://gateway-internal:8080/llm-proxy", "ws://gw", "gateway:8443"} {
		p := samplePool("acme", "pool-ep-bad")
		p.ProxyEndpoint = ep
		err := credentialpoolstore.Validate(p)
		if !errors.Is(err, credentialpoolstore.ErrInvalidProxyEndpointScheme) {
			t.Errorf("Validate proxyEndpoint %q = %v, want ErrInvalidProxyEndpointScheme", ep, err)
		}
	}
}

// TestProxyFieldsRoundTrip confirms the new proxy fields survive a
// Create/Get cycle on the in-memory store.
func TestProxyFieldsRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	p := samplePool("acme", "claude-direct-prod")
	p.DeliveryMode = "proxy"
	p.ProxyDialect = "anthropic"
	p.ProxyEndpoint = "https://gateway-internal:8443/llm-proxy"
	if err := store.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(ctx, "acme", "claude-direct-prod")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DeliveryMode != "proxy" || got.ProxyDialect != "anthropic" ||
		got.ProxyEndpoint != "https://gateway-internal:8443/llm-proxy" {
		t.Errorf("proxy fields not round-tripped: %+v", got)
	}
}

func TestCacheScopeRoundTrips(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	p := samplePool("acme", "pool-rt")
	p.CacheScope = "tenant"
	if err := store.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(ctx, "acme", "pool-rt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CacheScope != "tenant" {
		t.Errorf("CacheScope = %q, want tenant", got.CacheScope)
	}
}

// TestValidateCachePolicy covers the §4.9 CachePolicy structural
// invariants (spec lines 1542-1556): a nil policy is valid (caching
// off); strategy and backend are closed enums (empty accepted); ttl is
// non-negative; similarityThreshold is in [0, 1].
func TestValidateCachePolicy(t *testing.T) {
	// A nil policy is the caching-off default and always valid.
	if err := credentialpoolstore.Validate(samplePool("acme", "pool-nocache")); err != nil {
		t.Fatalf("Validate nil cachePolicy: %v, want nil", err)
	}
	valid := []credentialpoolstore.CachePolicy{
		{Enabled: true},
		{Enabled: true, Strategy: "semantic", Backend: "redis", TTLSeconds: 300, SimilarityThreshold: 0.92},
		{Enabled: false, Strategy: "semantic", Backend: "memory", SimilarityThreshold: 1},
		{Enabled: true, SimilarityThreshold: 0},
	}
	for i, cp := range valid {
		p := samplePool("acme", "pool-cp-ok")
		c := cp
		p.CachePolicy = &c
		if err := credentialpoolstore.Validate(p); err != nil {
			t.Errorf("valid cachePolicy[%d] %+v: %v, want nil", i, cp, err)
		}
	}
	invalid := []credentialpoolstore.CachePolicy{
		{Enabled: true, Strategy: "exact"},
		{Enabled: true, Backend: "memcached"},
		{Enabled: true, TTLSeconds: -1},
		{Enabled: true, SimilarityThreshold: 1.5},
		{Enabled: true, SimilarityThreshold: -0.1},
	}
	for i, cp := range invalid {
		p := samplePool("acme", "pool-cp-bad")
		c := cp
		p.CachePolicy = &c
		if err := credentialpoolstore.Validate(p); err == nil {
			t.Errorf("Validate accepted invalid cachePolicy[%d] %+v", i, cp)
		}
	}
}

// TestCachePolicyRoundTrips confirms a CachePolicy survives a Create/Get
// cycle and that a pool without one reads back nil (caching off).
func TestCachePolicyRoundTrips(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	p := samplePool("acme", "pool-cp-rt")
	p.CachePolicy = &credentialpoolstore.CachePolicy{
		Enabled: true, Strategy: "semantic", Backend: "redis",
		TTLSeconds: 120, SimilarityThreshold: 0.88,
	}
	if err := store.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(ctx, "acme", "pool-cp-rt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CachePolicy == nil {
		t.Fatalf("CachePolicy = nil, want round-tripped policy")
	}
	if *got.CachePolicy != *p.CachePolicy {
		t.Errorf("CachePolicy = %+v, want %+v", *got.CachePolicy, *p.CachePolicy)
	}

	none, err := store.Get(ctx, "acme", "pool-cp-rt-absent")
	if err == nil {
		t.Fatalf("expected ErrNotFound for absent pool, got %+v", none)
	}
}

func TestCreateAndGet(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	if err := store.Create(ctx, samplePool("acme", "claude-direct-prod")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(ctx, "acme", "claude-direct-prod")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Provider != "anthropic_direct" || len(got.Credentials) != 2 {
		t.Errorf("got %+v, want provider=anthropic_direct with 2 credentials", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("Create must stamp CreatedAt and UpdatedAt")
	}
}

func TestGetIsTenantScoped(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	if err := store.Create(ctx, samplePool("acme", "pool-1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// The same pool name under a different tenant must not resolve.
	if _, err := store.Get(ctx, "globex", "pool-1"); !errors.Is(err, credentialpoolstore.ErrNotFound) {
		t.Errorf("cross-tenant Get: got %v, want ErrNotFound", err)
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	if err := store.Create(ctx, samplePool("acme", "pool-1")); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if err := store.Create(ctx, samplePool("acme", "pool-1")); !errors.Is(err, credentialpoolstore.ErrAlreadyExists) {
		t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
	}
	// The same name under another tenant is not a duplicate.
	if err := store.Create(ctx, samplePool("globex", "pool-1")); err != nil {
		t.Errorf("same name, different tenant: got %v, want nil", err)
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	cases := map[string]func(*credentialpoolstore.CredentialPool){
		"empty tenant":          func(p *credentialpoolstore.CredentialPool) { p.TenantID = "" },
		"invalid name":          func(p *credentialpoolstore.CredentialPool) { p.Name = "Bad Name" },
		"empty provider":        func(p *credentialpoolstore.CredentialPool) { p.Provider = "" },
		"negative concurrency":  func(p *credentialpoolstore.CredentialPool) { p.MaxConcurrentSessions = -1 },
		"credential missing id": func(p *credentialpoolstore.CredentialPool) { p.Credentials[0].ID = "" },
		"duplicate credential id": func(p *credentialpoolstore.CredentialPool) {
			p.Credentials[1].ID = p.Credentials[0].ID
		},
	}
	for name, mutate := range cases {
		p := samplePool("acme", "pool-1")
		mutate(&p)
		if err := store.Create(ctx, p); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

func TestUpdateMutatesPool(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	if err := store.Create(ctx, samplePool("acme", "pool-1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	updated, err := store.Update(ctx, "acme", "pool-1", func(p *credentialpoolstore.CredentialPool) error {
		p.MaxConcurrentSessions = 25
		p.Credentials = append(p.Credentials, credentialpoolstore.Credential{
			ID: "key-3", SecretRef: "lenny-system/anthropic-key-3",
		})
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.MaxConcurrentSessions != 25 || len(updated.Credentials) != 3 {
		t.Errorf("updated = %+v, want maxConcurrentSessions=25 with 3 credentials", updated)
	}
	if !updated.UpdatedAt.After(updated.CreatedAt) {
		t.Error("Update must advance UpdatedAt past CreatedAt")
	}
}

func TestUpdateRejectsInvalidMutation(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	if err := store.Create(ctx, samplePool("acme", "pool-1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err := store.Update(ctx, "acme", "pool-1", func(p *credentialpoolstore.CredentialPool) error {
		p.Provider = "" // a pool must keep a provider
		return nil
	})
	if err == nil {
		t.Error("Update producing an invalid pool must be rejected")
	}
}

func TestUpdateMissing(t *testing.T) {
	store := credentialpoolstore.NewMemory()
	_, err := store.Update(context.Background(), "acme", "ghost", func(*credentialpoolstore.CredentialPool) error {
		return nil
	})
	if !errors.Is(err, credentialpoolstore.ErrNotFound) {
		t.Errorf("Update missing pool: got %v, want ErrNotFound", err)
	}
}

func TestListIsTenantScopedAndExcludesDeleted(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	if err := store.Create(ctx, samplePool("acme", "pool-a")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Create(ctx, samplePool("acme", "pool-b")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Create(ctx, samplePool("globex", "pool-c")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.SoftDelete(ctx, "acme", "pool-b", time.Now().UTC()); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	active, _ := store.List(ctx, "acme", credentialpoolstore.ListFilter{})
	if len(active) != 1 || active[0].Name != "pool-a" {
		t.Errorf("List acme: got %+v, want only pool-a", active)
	}
	all, _ := store.List(ctx, "acme", credentialpoolstore.ListFilter{IncludeDeleted: true})
	if len(all) != 2 {
		t.Errorf("List acme includeDeleted: got %d pools, want 2", len(all))
	}
	globex, _ := store.List(ctx, "globex", credentialpoolstore.ListFilter{})
	if len(globex) != 1 || globex[0].Name != "pool-c" {
		t.Errorf("List globex: got %+v, want only pool-c", globex)
	}
}

func TestSoftDeleteMissing(t *testing.T) {
	store := credentialpoolstore.NewMemory()
	if err := store.SoftDelete(context.Background(), "acme", "ghost", time.Now()); !errors.Is(err, credentialpoolstore.ErrNotFound) {
		t.Errorf("SoftDelete missing: got %v, want ErrNotFound", err)
	}
}

func TestStoredPoolIsolatedFromCallerMutation(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	if err := store.Create(ctx, samplePool("acme", "pool-1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, _ := store.Get(ctx, "acme", "pool-1")
	got.Credentials[0].SecretRef = "tampered"

	fresh, _ := store.Get(ctx, "acme", "pool-1")
	if fresh.Credentials[0].SecretRef != "lenny-system/anthropic-key-1" {
		t.Errorf("stored pool was mutated through a returned copy: %q", fresh.Credentials[0].SecretRef)
	}
}
