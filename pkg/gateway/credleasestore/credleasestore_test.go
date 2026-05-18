// SPDX-License-Identifier: MIT

package credleasestore_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
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
