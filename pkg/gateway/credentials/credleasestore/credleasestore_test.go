// SPDX-License-Identifier: MIT

package credleasestore_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
)

// spec: §4.9 — the gateway-replica credential-lease store the LLM proxy
// resolves a bearer lease token through.

// proxyLease returns a valid pool-backed proxy lease with the given ID
// and lease token.
func proxyLease(leaseID, token string) credential.Lease {
	return credential.Lease{
		LeaseID:      leaseID,
		SessionID:    "s_" + leaseID,
		Provider:     credential.ProviderAnthropicDirect,
		Source:       credential.SourcePool,
		PoolID:       "claude-prod",
		CredentialID: "key-1",
		DeliveryMode: credential.DeliveryProxy,
		IssuedAt:     time.Now(),
		ExpiresAt:    time.Now().Add(time.Hour),
		Proxy: &credential.ProxyConfig{
			ProxyURL:     "https://gateway-internal:8443/llm-proxy",
			ProxyDialect: "anthropic",
			LeaseToken:   token,
		},
	}
}

func TestPutAndGetByToken(t *testing.T) {
	s := credleasestore.New()
	if err := s.Put(proxyLease("cl_1", "lt-abc")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok := s.GetByToken("lt-abc")
	if !ok {
		t.Fatal("GetByToken did not resolve the stored lease token")
	}
	if got.LeaseID != "cl_1" {
		t.Errorf("resolved lease ID = %q, want cl_1", got.LeaseID)
	}
}

func TestGetByID(t *testing.T) {
	s := credleasestore.New()
	_ = s.Put(proxyLease("cl_1", "lt-abc"))
	got, ok := s.GetByID("cl_1")
	if !ok || got.Proxy.LeaseToken != "lt-abc" {
		t.Errorf("GetByID = %+v ok=%v, want the stored lease", got, ok)
	}
}

func TestPutRejectsInvalidLease(t *testing.T) {
	s := credleasestore.New()
	// A proxy lease with no materializedConfig fails Lease.Validate.
	bad := proxyLease("cl_bad", "lt-x")
	bad.Proxy = nil
	if err := s.Put(bad); err == nil {
		t.Error("Put accepted an invalid lease")
	}
	if s.Len() != 0 {
		t.Errorf("store holds %d leases after a rejected Put, want 0", s.Len())
	}
}

func TestGetByTokenMiss(t *testing.T) {
	s := credleasestore.New()
	if _, ok := s.GetByToken("lt-unknown"); ok {
		t.Error("GetByToken resolved an unknown token")
	}
}

func TestDirectLeaseHasNoTokenIndex(t *testing.T) {
	s := credleasestore.New()
	direct := credential.Lease{
		LeaseID:       "cl_direct",
		SessionID:     "s_1",
		Provider:      credential.ProviderAnthropicDirect,
		Source:        credential.SourceUser,
		TenantID:      "acme",
		CredentialRef: "cred-1",
		DeliveryMode:  credential.DeliveryDirect,
		IssuedAt:      time.Now(),
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	if err := s.Put(direct); err != nil {
		t.Fatalf("Put direct lease: %v", err)
	}
	if _, ok := s.GetByID("cl_direct"); !ok {
		t.Error("GetByID did not resolve the direct lease")
	}
	if _, ok := s.GetByToken(""); ok {
		t.Error("a direct-mode lease was indexed by an (empty) token")
	}
}

func TestRemoveDropsBothIndexes(t *testing.T) {
	s := credleasestore.New()
	_ = s.Put(proxyLease("cl_1", "lt-abc"))
	s.Remove("cl_1")
	if _, ok := s.GetByID("cl_1"); ok {
		t.Error("GetByID resolved a removed lease")
	}
	if _, ok := s.GetByToken("lt-abc"); ok {
		t.Error("GetByToken resolved a removed lease's token")
	}
	if s.Len() != 0 {
		t.Errorf("store holds %d leases after Remove, want 0", s.Len())
	}
}

func TestRemoveMissingLeaseIsNoOp(t *testing.T) {
	s := credleasestore.New()
	s.Remove("cl_absent") // must not panic
	if s.Len() != 0 {
		t.Errorf("store holds %d leases, want 0", s.Len())
	}
}

func TestPutReplacesStaleTokenIndex(t *testing.T) {
	s := credleasestore.New()
	_ = s.Put(proxyLease("cl_1", "lt-old"))
	// Re-issue the same lease ID with a rotated token.
	if err := s.Put(proxyLease("cl_1", "lt-new")); err != nil {
		t.Fatalf("re-Put: %v", err)
	}
	if _, ok := s.GetByToken("lt-old"); ok {
		t.Error("the rotated-away lease token still resolves")
	}
	got, ok := s.GetByToken("lt-new")
	if !ok || got.LeaseID != "cl_1" {
		t.Errorf("GetByToken(lt-new) = %+v ok=%v, want lease cl_1", got, ok)
	}
	if s.Len() != 1 {
		t.Errorf("store holds %d leases after re-Put, want 1", s.Len())
	}
}

// spec: §11.4 full_revoke — the credential-lease revocation step
// resolves a revoked user's sessions and collects their leases.

// sessionLease returns a valid pool-backed proxy lease bound to the
// given session.
func sessionLease(leaseID, token, sessionID string) credential.Lease {
	l := proxyLease(leaseID, token)
	l.SessionID = sessionID
	return l
}

func TestLeasesBySession(t *testing.T) {
	s := credleasestore.New()
	_ = s.Put(sessionLease("cl_1", "lt-1", "run_a"))
	_ = s.Put(sessionLease("cl_2", "lt-2", "run_a"))
	_ = s.Put(sessionLease("cl_3", "lt-3", "run_b"))
	_ = s.Put(sessionLease("cl_4", "lt-4", "run_c"))

	got := s.LeasesBySession([]string{"run_a", "run_b"})
	if len(got) != 3 {
		t.Fatalf("LeasesBySession returned %d leases, want 3 (run_a x2, run_b x1)", len(got))
	}
	ids := map[string]bool{}
	for _, l := range got {
		ids[l.LeaseID] = true
	}
	for _, want := range []string{"cl_1", "cl_2", "cl_3"} {
		if !ids[want] {
			t.Errorf("LeasesBySession missing lease %s", want)
		}
	}
	if ids["cl_4"] {
		t.Error("LeasesBySession returned a lease for an unrequested session")
	}
}

func TestLeasesBySessionEmptyRequest(t *testing.T) {
	s := credleasestore.New()
	_ = s.Put(sessionLease("cl_1", "lt-1", "run_a"))
	if got := s.LeasesBySession(nil); got != nil {
		t.Errorf("LeasesBySession(nil) = %v, want nil", got)
	}
	if got := s.LeasesBySession([]string{}); len(got) != 0 {
		t.Errorf("LeasesBySession([]) returned %d leases, want 0", len(got))
	}
}

func TestLeasesBySessionNoMatch(t *testing.T) {
	s := credleasestore.New()
	_ = s.Put(sessionLease("cl_1", "lt-1", "run_a"))
	if got := s.LeasesBySession([]string{"run_absent"}); len(got) != 0 {
		t.Errorf("LeasesBySession for an unknown session returned %d leases, want 0", len(got))
	}
}

// spec: §4.9 line 1671 — deny-list entries expire when the credential's
// natural lease TTL lapses, so the sweep deletes leases past ExpiresAt.

// expiringLease returns a valid pool-backed proxy lease with the given
// ID, token, and expiry.
func expiringLease(leaseID, token string, expiresAt time.Time) credential.Lease {
	l := proxyLease(leaseID, token)
	l.IssuedAt = expiresAt.Add(-time.Hour)
	l.ExpiresAt = expiresAt
	return l
}

func TestDeleteExpiredRemovesPastLeasesAndCounts(t *testing.T) {
	s := credleasestore.New()
	now := time.Now()
	// Two leases already past expiry, one still active.
	_ = s.Put(expiringLease("cl_old1", "lt-old1", now.Add(-time.Hour)))
	_ = s.Put(expiringLease("cl_old2", "lt-old2", now.Add(-time.Minute)))
	_ = s.Put(expiringLease("cl_live", "lt-live", now.Add(time.Hour)))

	removed, err := s.DeleteExpired(context.Background(), now)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if removed != 2 {
		t.Errorf("DeleteExpired removed %d leases, want 2", removed)
	}
	// The expired leases are gone from both the id and the token index.
	for _, id := range []string{"cl_old1", "cl_old2"} {
		if _, ok := s.GetByID(id); ok {
			t.Errorf("expired lease %s survived DeleteExpired", id)
		}
	}
	if _, ok := s.GetByToken("lt-old1"); ok {
		t.Error("expired lease's token index survived DeleteExpired")
	}
	// The active lease and its token index are retained.
	if _, ok := s.GetByID("cl_live"); !ok {
		t.Error("DeleteExpired dropped an unexpired lease")
	}
	if _, ok := s.GetByToken("lt-live"); !ok {
		t.Error("DeleteExpired dropped an unexpired lease's token index")
	}
	if s.Len() != 1 {
		t.Errorf("store holds %d leases after DeleteExpired, want 1", s.Len())
	}
}

// spec: §4.9 lines 1694-1695 — the startup rebuild seeds a deny-list
// entry only for a revoked credential that still has an active lease, so
// the existence count must exclude a lease already past its expiry and
// report a nil error the caller can distinguish from an unanswerable
// query.
func TestLeasesByCredentialCountExcludesExpired(t *testing.T) {
	s := credleasestore.New()
	now := time.Now()
	key := credential.CredentialKey{
		Source:       credential.SourcePool,
		PoolID:       "claude-prod",
		CredentialID: "key-1",
	}
	// Two active leases against the credential, one expired against it.
	_ = s.Put(expiringLease("cl_a", "lt-a", now.Add(time.Hour)))
	_ = s.Put(expiringLease("cl_b", "lt-b", now.Add(30*time.Minute)))
	_ = s.Put(expiringLease("cl_expired", "lt-e", now.Add(-time.Minute)))
	// A lease against a different credential must not be counted.
	other := expiringLease("cl_other", "lt-o", now.Add(time.Hour))
	other.CredentialID = "key-2"
	_ = s.Put(other)

	n, err := s.LeasesByCredentialCount(context.Background(), key, now)
	if err != nil {
		t.Fatalf("LeasesByCredentialCount: %v", err)
	}
	if n != 2 {
		t.Errorf("LeasesByCredentialCount = %d, want 2 active leases", n)
	}

	// A credential with no live lease reports zero with a nil error, the
	// definitive answer the fail-closed callers act on.
	absent := credential.CredentialKey{Source: credential.SourcePool, PoolID: "absent", CredentialID: "absent"}
	n, err = s.LeasesByCredentialCount(context.Background(), absent, now)
	if err != nil {
		t.Fatalf("LeasesByCredentialCount(absent): %v", err)
	}
	if n != 0 {
		t.Errorf("LeasesByCredentialCount(absent) = %d, want 0", n)
	}
}
