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

func TestRehydrateUnionsLocalRevocations(t *testing.T) {
	c := NewCache()
	// A revocation applied locally (e.g. via the admin endpoint) must
	// survive a rehydration whose index snapshot predates it.
	c.Revoke("jti-local")
	src := fakeSource{"acme": {{JTI: "jti-indexed"}}}
	if err := c.Rehydrate(context.Background(), fakeLister{"acme"}, src); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if !c.IsRevoked("jti-local") {
		t.Error("a locally-revoked jti must survive rehydration")
	}
	if !c.IsRevoked("jti-indexed") {
		t.Error("an indexed revocation should be loaded")
	}
}

func TestRehydratePropagatesSourceError(t *testing.T) {
	c := NewCache()
	want := errors.New("postgres unavailable")
	err := c.Rehydrate(context.Background(), fakeLister{"acme"}, errSource{err: want})
	if !errors.Is(err, want) {
		t.Errorf("Rehydrate error = %v, want the source error", err)
	}
}
