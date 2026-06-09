// SPDX-License-Identifier: MIT

package usercreds_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentialstore"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/usercreds"
)

// recordingRevoker captures the §4.9 deny-list keys RevokeUser propagates.
type recordingRevoker struct {
	keys []credential.CredentialKey
}

func (r *recordingRevoker) Revoke(key credential.CredentialKey) { r.keys = append(r.keys, key) }

const testProxyURL = "https://lenny-proxy.svc:8443"

func newMaterializer(t *testing.T, proxyURL string) (*usercreds.Materializer, credentialstore.Store, *credleasestore.Store, *credcache.Cache) {
	t.Helper()
	store := credentialstore.NewMemory(func() time.Time { return time.Unix(1700000000, 0).UTC() })
	leases := credleasestore.New()
	creds := credcache.New()
	m := usercreds.New(usercreds.Config{
		Store:    store,
		Leases:   leases,
		Creds:    creds,
		ProxyURL: proxyURL,
		Now:      func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	return m, store, leases, creds
}

func register(t *testing.T, store credentialstore.Store, provider credential.Provider, secret string) credentialstore.Credential {
	t.Helper()
	c, err := store.Register(context.Background(), "acme", "alice", provider, "", secret)
	if err != nil {
		t.Fatalf("register %s: %v", provider, err)
	}
	return c
}

// TestAvailable_spec_4_9_1347 covers the §4.9 userCredChecker decision:
// an active credential on a dialect-mapped provider with the proxy
// configured is available; every negative case reports unavailable.
func TestAvailable_spec_4_9_1347(t *testing.T) {
	m, store, _, _ := newMaterializer(t, testProxyURL)
	register(t, store, credential.ProviderAnthropicDirect, "sk-ant-xyz")
	ctx := context.Background()

	if !m.Available(ctx, "acme", "alice", string(credential.ProviderAnthropicDirect)) {
		t.Fatal("active anthropic_direct credential should be available")
	}
	// Unregistered provider for the same user.
	if m.Available(ctx, "acme", "alice", string(credential.ProviderVertexAI)) {
		t.Fatal("unregistered provider must not be available")
	}
	// Different user — scoped lookup miss.
	if m.Available(ctx, "acme", "bob", string(credential.ProviderAnthropicDirect)) {
		t.Fatal("another user's credential must not be available")
	}
	// Provider with no canonical proxy dialect (non-LLM github).
	register(t, store, credential.ProviderGitHub, "ghp_xyz")
	if m.Available(ctx, "acme", "alice", string(credential.ProviderGitHub)) {
		t.Fatal("github (no canonical dialect) must not be deliverable in proxy mode")
	}
}

// TestAvailable_revoked_spec_4_9_1379 confirms a revoked credential is
// treated as not-found per §4.9 line 1379.
func TestAvailable_revoked_spec_4_9_1379(t *testing.T) {
	m, store, _, _ := newMaterializer(t, testProxyURL)
	c := register(t, store, credential.ProviderAnthropicDirect, "sk-ant-xyz")
	if _, err := store.Revoke(context.Background(), "acme", c.Ref); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if m.Available(context.Background(), "acme", "alice", string(credential.ProviderAnthropicDirect)) {
		t.Fatal("a revoked credential must be reported unavailable")
	}
}

// TestAvailable_noProxyURL_spec_4_9_1347 confirms that without a configured
// LLM proxy URL no user credential is deliverable.
func TestAvailable_noProxyURL_spec_4_9_1347(t *testing.T) {
	m, store, _, _ := newMaterializer(t, "")
	register(t, store, credential.ProviderAnthropicDirect, "sk-ant-xyz")
	if m.Available(context.Background(), "acme", "alice", string(credential.ProviderAnthropicDirect)) {
		t.Fatal("no proxy URL: user credentials must be reported unavailable")
	}
}

// TestMintProto_spec_4_9_1347 mints a proxy-mode user lease and asserts the
// lease is Source:user, proxy delivery, recorded in the lease store, its
// secret cached, last_used_at stamped, and the wire payload carries the
// uniform proxy materializedConfig with no upstream secret.
func TestMintProto_spec_4_9_1347(t *testing.T) {
	m, store, leases, creds := newMaterializer(t, testProxyURL)
	register(t, store, credential.ProviderAnthropicDirect, "sk-ant-secret")
	ctx := context.Background()

	proto, err := m.MintProto(ctx, "acme", "alice", "sess-1", "spiffe://acme/pod/p1", string(credential.ProviderAnthropicDirect))
	if err != nil {
		t.Fatalf("MintProto: %v", err)
	}
	if proto.LeaseId == "" {
		t.Fatal("proto lease has no leaseId")
	}
	if proto.Provider != string(credential.ProviderAnthropicDirect) {
		t.Fatalf("proto provider = %q, want anthropic_direct", proto.Provider)
	}
	lease, ok := leases.GetByID(proto.LeaseId)
	if !ok {
		t.Fatal("minted lease not recorded in the lease store")
	}
	if lease.Source != credential.SourceUser {
		t.Fatalf("lease source = %q, want user", lease.Source)
	}
	if lease.DeliveryMode != credential.DeliveryProxy {
		t.Fatalf("lease delivery = %q, want proxy", lease.DeliveryMode)
	}
	if lease.TenantID != "acme" || lease.CredentialRef == "" {
		t.Fatalf("user lease missing tenant/ref: %+v", lease)
	}
	if lease.SpiffeURI != "spiffe://acme/pod/p1" {
		t.Fatalf("lease spiffeURI = %q, want pod identity", lease.SpiffeURI)
	}
	// The upstream secret is cached gateway-side under the lease's key.
	secret, ok := creds.UpstreamCredential(lease)
	if !ok || secret != "sk-ant-secret" {
		t.Fatalf("upstream secret = %q,%v; want sk-ant-secret cached", secret, ok)
	}
	// The wire payload carries only the uniform proxy config — never the
	// upstream secret.
	var payload struct {
		DeliveryMode       string            `json:"deliveryMode"`
		MaterializedConfig map[string]string `json:"materializedConfig"`
	}
	if err := json.Unmarshal(proto.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.DeliveryMode != "proxy" {
		t.Fatalf("payload deliveryMode = %q, want proxy", payload.DeliveryMode)
	}
	if payload.MaterializedConfig["proxyUrl"] != testProxyURL {
		t.Fatalf("payload proxyUrl = %q, want %q", payload.MaterializedConfig["proxyUrl"], testProxyURL)
	}
	if payload.MaterializedConfig["proxyDialect"] != string(credential.ProxyDialectAnthropic) {
		t.Fatalf("payload proxyDialect = %q, want anthropic", payload.MaterializedConfig["proxyDialect"])
	}
	if payload.MaterializedConfig["leaseToken"] == "" {
		t.Fatal("payload missing leaseToken")
	}
	for k, v := range payload.MaterializedConfig {
		if v == "sk-ant-secret" {
			t.Fatalf("upstream secret leaked into payload field %q", k)
		}
	}
	// last_used_at stamped on resolution.
	got, err := store.Get(ctx, "acme", lease.CredentialRef)
	if err != nil {
		t.Fatalf("get credential: %v", err)
	}
	if got.LastUsedAt.IsZero() {
		t.Fatal("MintProto did not stamp last_used_at")
	}
}

// TestMintProto_errors_spec_4_9_1379 covers the materialization refusals:
// revoked, missing, no-dialect provider, and an unconfigured proxy URL.
func TestMintProto_errors_spec_4_9_1379(t *testing.T) {
	ctx := context.Background()

	t.Run("revoked", func(t *testing.T) {
		m, store, _, _ := newMaterializer(t, testProxyURL)
		c := register(t, store, credential.ProviderAnthropicDirect, "sk")
		if _, err := store.Revoke(ctx, "acme", c.Ref); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if _, err := m.MintProto(ctx, "acme", "alice", "s", "", string(credential.ProviderAnthropicDirect)); err == nil {
			t.Fatal("MintProto on a revoked credential should error")
		}
	})
	t.Run("missing", func(t *testing.T) {
		m, _, _, _ := newMaterializer(t, testProxyURL)
		if _, err := m.MintProto(ctx, "acme", "alice", "s", "", string(credential.ProviderAnthropicDirect)); err == nil {
			t.Fatal("MintProto on a missing credential should error")
		}
	})
	t.Run("no-dialect", func(t *testing.T) {
		m, store, _, _ := newMaterializer(t, testProxyURL)
		register(t, store, credential.ProviderGitHub, "ghp")
		if _, err := m.MintProto(ctx, "acme", "alice", "s", "", string(credential.ProviderGitHub)); err == nil {
			t.Fatal("MintProto on a no-dialect provider should error")
		}
	})
	t.Run("no-proxy-url", func(t *testing.T) {
		m, store, _, _ := newMaterializer(t, "")
		register(t, store, credential.ProviderAnthropicDirect, "sk")
		if _, err := m.MintProto(ctx, "acme", "alice", "s", "", string(credential.ProviderAnthropicDirect)); err != usercreds.ErrProxyUnavailable {
			t.Fatalf("MintProto with no proxy URL err = %v, want ErrProxyUnavailable", err)
		}
	})
}

// TestRotateUser_spec_4_9_1350 confirms a rotation re-caches the new secret
// for every active lease and returns the count, leaving the lease token
// untouched so running sessions are not interrupted.
func TestRotateUser_spec_4_9_1350(t *testing.T) {
	m, store, leases, creds := newMaterializer(t, testProxyURL)
	c := register(t, store, credential.ProviderAnthropicDirect, "sk-old")
	ctx := context.Background()
	proto, err := m.MintProto(ctx, "acme", "alice", "sess-1", "", string(credential.ProviderAnthropicDirect))
	if err != nil {
		t.Fatalf("MintProto: %v", err)
	}
	lease, _ := leases.GetByID(proto.LeaseId)

	// Operator/user rotates the registry secret first (the handler does
	// this), then the materializer propagates it.
	if _, err := store.Rotate(ctx, "acme", c.Ref, "sk-new"); err != nil {
		t.Fatalf("rotate store: %v", err)
	}
	n, err := m.RotateUser(ctx, "acme", c.Ref)
	if err != nil {
		t.Fatalf("RotateUser: %v", err)
	}
	if n != 1 {
		t.Fatalf("RotateUser count = %d, want 1", n)
	}
	// The proxy now resolves the new secret under the same (unchanged) key.
	secret, ok := creds.UpstreamCredential(lease)
	if !ok || secret != "sk-new" {
		t.Fatalf("cached secret = %q,%v; want sk-new", secret, ok)
	}
	// The lease (and its token) is still present and unchanged.
	if _, ok := leases.GetByID(proto.LeaseId); !ok {
		t.Fatal("rotation must not drop the lease")
	}
}

// TestRotateUser_noLeases_spec_4_9_1350 returns zero when no session holds
// a lease for the credential.
func TestRotateUser_noLeases_spec_4_9_1350(t *testing.T) {
	m, store, _, _ := newMaterializer(t, testProxyURL)
	c := register(t, store, credential.ProviderAnthropicDirect, "sk")
	n, err := m.RotateUser(context.Background(), "acme", c.Ref)
	if err != nil || n != 0 {
		t.Fatalf("RotateUser with no leases = %d,%v; want 0,nil", n, err)
	}
}

// TestRevokeUser_spec_4_9_1351 confirms a revoke deny-lists the user-shaped
// key, drops the active leases, evicts the cached secret, and counts the
// terminated leases.
func TestRevokeUser_spec_4_9_1351(t *testing.T) {
	m, store, leases, creds := newMaterializer(t, testProxyURL)
	rev := &recordingRevoker{}
	m.SetRevoker(rev)
	c := register(t, store, credential.ProviderAnthropicDirect, "sk")
	ctx := context.Background()
	proto, err := m.MintProto(ctx, "acme", "alice", "sess-1", "", string(credential.ProviderAnthropicDirect))
	if err != nil {
		t.Fatalf("MintProto: %v", err)
	}
	lease, _ := leases.GetByID(proto.LeaseId)

	n, err := m.RevokeUser(ctx, "acme", c.Ref)
	if err != nil {
		t.Fatalf("RevokeUser: %v", err)
	}
	if n != 1 {
		t.Fatalf("RevokeUser count = %d, want 1", n)
	}
	// Cross-replica deny-list propagation with the user-shaped key.
	if len(rev.keys) != 1 {
		t.Fatalf("revoker got %d keys, want 1", len(rev.keys))
	}
	want := credential.CredentialKey{Source: credential.SourceUser, TenantID: "acme", CredentialRef: c.Ref}
	if rev.keys[0] != want {
		t.Fatalf("deny-list key = %+v, want %+v", rev.keys[0], want)
	}
	// Lease dropped and cached secret evicted on this replica.
	if _, ok := leases.GetByID(proto.LeaseId); ok {
		t.Fatal("RevokeUser must drop the lease")
	}
	if _, ok := creds.UpstreamCredential(lease); ok {
		t.Fatal("RevokeUser must evict the cached upstream secret")
	}
}

// TestRevokeUser_nilRevoker_spec_4_9_1351 confirms revoke still drops the
// local leases without a cross-replica propagator (single-replica posture).
func TestRevokeUser_nilRevoker_spec_4_9_1351(t *testing.T) {
	m, store, leases, _ := newMaterializer(t, testProxyURL)
	c := register(t, store, credential.ProviderAnthropicDirect, "sk")
	ctx := context.Background()
	proto, err := m.MintProto(ctx, "acme", "alice", "sess-1", "", string(credential.ProviderAnthropicDirect))
	if err != nil {
		t.Fatalf("MintProto: %v", err)
	}
	n, err := m.RevokeUser(ctx, "acme", c.Ref)
	if err != nil || n != 1 {
		t.Fatalf("RevokeUser nil-revoker = %d,%v; want 1,nil", n, err)
	}
	if _, ok := leases.GetByID(proto.LeaseId); ok {
		t.Fatal("RevokeUser must drop the lease even with no revoker")
	}
}
