// SPDX-License-Identifier: MIT

package credentialpoolstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
)

// spec: §4.9 lines 1640-1673 — per-credential revocation state and the
// startup deny-list rebuild query.

func revPool(tenant, name string, creds ...credentialpoolstore.Credential) credentialpoolstore.CredentialPool {
	return credentialpoolstore.CredentialPool{
		TenantID:    tenant,
		Name:        name,
		Provider:    "anthropic_direct",
		Credentials: creds,
	}
}

// TestCredentialIsRevoked covers the §4.9 status helper: an empty status
// reads as active, only "revoked" is revoked.
func TestCredentialIsRevoked(t *testing.T) {
	cases := []struct {
		status credentialpoolstore.CredentialStatus
		want   bool
	}{
		{"", false},
		{credentialpoolstore.CredentialActive, false},
		{credentialpoolstore.CredentialRevoked, true},
	}
	for _, c := range cases {
		if got := (credentialpoolstore.Credential{ID: "k", Status: c.status}).IsRevoked(); got != c.want {
			t.Errorf("status %q: IsRevoked = %v, want %v", c.status, got, c.want)
		}
	}
}

// TestValidateRejectsUnknownCredentialStatus covers the §4.9 status enum
// at the store validation boundary.
func TestValidateRejectsUnknownCredentialStatus(t *testing.T) {
	p := revPool("acme", "p1", credentialpoolstore.Credential{ID: "k", SecretRef: "s", Status: "disabled"})
	if err := credentialpoolstore.Validate(p); err == nil {
		t.Fatal("Validate accepted an unknown credential status")
	}
	p.Credentials[0].Status = credentialpoolstore.CredentialRevoked
	if err := credentialpoolstore.Validate(p); err != nil {
		t.Fatalf("Validate rejected a revoked credential: %v", err)
	}
}

// TestRevokedCredentialsAcrossTenants is the §4.9 startup-rebuild query:
// it returns every revoked credential across all tenants' active pools,
// in deterministic order, and excludes active credentials.
func TestRevokedCredentialsAcrossTenants(t *testing.T) {
	store := credentialpoolstore.NewMemory()
	ctx := context.Background()
	now := time.Now().UTC()
	mustCreate(t, store, revPool("acme", "claude-prod",
		credentialpoolstore.Credential{ID: "key-1", SecretRef: "s1", Status: credentialpoolstore.CredentialRevoked, RevokedAt: now},
		credentialpoolstore.Credential{ID: "key-2", SecretRef: "s2"}, // active
	))
	mustCreate(t, store, revPool("globex", "openai-prod",
		credentialpoolstore.Credential{ID: "key-x", SecretRef: "sx", Status: credentialpoolstore.CredentialRevoked, RevokedAt: now},
	))

	got, err := store.RevokedCredentials(ctx)
	if err != nil {
		t.Fatalf("RevokedCredentials: %v", err)
	}
	want := []credentialpoolstore.RevokedCredential{
		{TenantID: "acme", PoolName: "claude-prod", CredentialID: "key-1"},
		{TenantID: "globex", PoolName: "openai-prod", CredentialID: "key-x"},
	}
	if len(got) != len(want) {
		t.Fatalf("RevokedCredentials returned %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestRevokedCredentialsSkipsDeletedPools confirms the §4.9 rebuild query
// excludes soft-deleted pools: their leases are gone, so their revoked
// credentials need not be denied.
func TestRevokedCredentialsSkipsDeletedPools(t *testing.T) {
	store := credentialpoolstore.NewMemory()
	ctx := context.Background()
	mustCreate(t, store, revPool("acme", "gone",
		credentialpoolstore.Credential{ID: "key-1", SecretRef: "s1", Status: credentialpoolstore.CredentialRevoked},
	))
	if err := store.SoftDelete(ctx, "acme", "gone", time.Now().UTC()); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	got, err := store.RevokedCredentials(ctx)
	if err != nil {
		t.Fatalf("RevokedCredentials: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("RevokedCredentials on a soft-deleted pool = %+v, want none", got)
	}
}

func mustCreate(t *testing.T, store *credentialpoolstore.Memory, p credentialpoolstore.CredentialPool) {
	t.Helper()
	if err := store.Create(context.Background(), p); err != nil {
		t.Fatalf("Create %s/%s: %v", p.TenantID, p.Name, err)
	}
}
