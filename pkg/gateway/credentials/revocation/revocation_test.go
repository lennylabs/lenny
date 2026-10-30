// SPDX-License-Identifier: MIT

package revocation

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
)

type fakeSource map[string][]issuedtokenstore.IssuedToken

func (f fakeSource) ListRevoked(_ context.Context, tenantID string) ([]issuedtokenstore.IssuedToken, error) {
	return f[tenantID], nil
}

type errSource struct{ err error }

func (e errSource) ListRevoked(context.Context, string) ([]issuedtokenstore.IssuedToken, error) {
	return nil, e.err
}

type fakeLister []string

func (f fakeLister) ListTenants(context.Context) ([]string, error) { return []string(f), nil }

func TestCacheRevokeAndIsRevoked(t *testing.T) {
	c := NewCache()
	if c.IsRevoked("jti-1") {
		t.Error("a fresh cache should report nothing revoked")
	}
	c.Revoke("jti-1")
	if !c.IsRevoked("jti-1") {
		t.Error("jti-1 should be revoked after Revoke")
	}
	if c.IsRevoked("jti-2") {
		t.Error("jti-2 was never revoked")
	}
	if c.IsRevoked("") {
		t.Error("an empty jti is never revoked")
	}
	c.Revoke("")
	if c.Len() != 1 {
		t.Errorf("Len = %d, want 1 (the empty jti is not stored)", c.Len())
	}
}

func TestRehydrateLoadsEveryTenant(t *testing.T) {
	c := NewCache()
	src := fakeSource{
		"acme":   {{JTI: "jti-a1"}, {JTI: "jti-a2"}},
		"globex": {{JTI: "jti-g1"}},
	}
	if err := c.Rehydrate(context.Background(), fakeLister{"acme", "globex"}, src); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	for _, jti := range []string{"jti-a1", "jti-a2", "jti-g1"} {
		if !c.IsRevoked(jti) {
			t.Errorf("%s should be revoked after rehydration", jti)
		}
	}
	if c.Len() != 3 {
		t.Errorf("Len = %d, want 3", c.Len())
	}
}

// revokingSource returns rows from rows and, during its ListRevoked
// call, revokes each jti in during on cache, standing in for a
// token.revoked event that arrives while the read is in flight.
type revokingSource struct {
	cache  *Cache
	rows   fakeSource
	during []string
}

func (r revokingSource) ListRevoked(ctx context.Context, tenantID string) ([]issuedtokenstore.IssuedToken, error) {
	for _, jti := range r.during {
		r.cache.Revoke(jti)
	}
	return r.rows.ListRevoked(ctx, tenantID)
}

// TestRehydrateUnionsLocalRevocations pins the swap rule: a successful
// read keeps the rows it returned and the jtis Revoke added after the
// read began, and drops a jti revoked before the read that the index
// does not return. The previous swap carried every prior jti forward, so
// a jti absent from the index was never dropped.
//
// spec: 13.3 (Credential Flow: Authoritative durability for revocation), 25.4 (The lenny-ops Service: Authentication)
func TestRehydrateUnionsLocalRevocations(t *testing.T) {
	c := NewCache()
	c.Revoke("jti-before-read")
	src := revokingSource{
		cache:  c,
		rows:   fakeSource{"acme": {{JTI: "jti-indexed"}}},
		during: []string{"jti-during-read"},
	}
	if err := c.Rehydrate(context.Background(), fakeLister{"acme"}, src); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if !c.IsRevoked("jti-during-read") {
		t.Error("a jti revoked while the read ran must survive the swap")
	}
	if !c.IsRevoked("jti-indexed") {
		t.Error("an indexed revocation should be loaded")
	}
	if c.IsRevoked("jti-before-read") {
		t.Error("a jti revoked before the read that the index does not return must be dropped")
	}
	if c.Len() != 2 {
		t.Errorf("Len = %d, want 2", c.Len())
	}
}

// TestRehydrateFailureKeepsSetAndDuringReadRevocations pins that a failed
// read leaves the set unchanged, a jti revoked while it ran included,
// and that a later read no longer carries that jti unless the index
// returns it.
//
// spec: 13.3 (Credential Flow: Authoritative durability for revocation), 25.4 (The lenny-ops Service: Authentication)
func TestRehydrateFailureKeepsSetAndDuringReadRevocations(t *testing.T) {
	c := NewCache()
	if err := c.Rehydrate(context.Background(), fakeLister{"acme"}, fakeSource{"acme": {{JTI: "jti-a"}}}); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	failing := failingRevokingSource{cache: c, jti: "jti-during-failed-read"}
	if err := c.Rehydrate(context.Background(), fakeLister{"acme"}, failing); err == nil {
		t.Fatal("Rehydrate over a failing source returned nil")
	}
	for _, jti := range []string{"jti-a", "jti-during-failed-read"} {
		if !c.IsRevoked(jti) {
			t.Errorf("%s must stay revoked after a failed read", jti)
		}
	}
	if err := c.Rehydrate(context.Background(), fakeLister{"acme"}, fakeSource{"acme": {{JTI: "jti-a"}}}); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if c.IsRevoked("jti-during-failed-read") {
		t.Error("a jti the next successful read does not return must be dropped")
	}
}

// failingRevokingSource revokes jti on cache and then fails the read.
type failingRevokingSource struct {
	cache *Cache
	jti   string
}

func (f failingRevokingSource) ListRevoked(context.Context, string) ([]issuedtokenstore.IssuedToken, error) {
	f.cache.Revoke(f.jti)
	return nil, errors.New("postgres unavailable")
}

func TestRehydratePropagatesSourceError(t *testing.T) {
	c := NewCache()
	want := errors.New("postgres unavailable")
	err := c.Rehydrate(context.Background(), fakeLister{"acme"}, errSource{err: want})
	if !errors.Is(err, want) {
		t.Errorf("Rehydrate error = %v, want the source error", err)
	}
}
