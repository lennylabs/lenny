// SPDX-License-Identifier: MIT

package leaserevoke_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/leaserevoke"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

var _ admin.UserLeaseRevoker = (*leaserevoke.Revoker)(nil)

// poolLease returns a pool-backed proxy lease bound to session.
func poolLease(leaseID, sessionID string) credential.Lease {
	return credential.Lease{
		LeaseID:      leaseID,
		SessionID:    sessionID,
		Provider:     credential.ProviderAnthropicDirect,
		Source:       credential.SourcePool,
		PoolID:       "claude-prod",
		CredentialID: "key-" + leaseID,
		DeliveryMode: credential.DeliveryProxy,
		IssuedAt:     time.Now(),
		ExpiresAt:    time.Now().Add(time.Hour),
		Proxy: &credential.ProxyConfig{
			ProxyURL:     "https://gateway-internal:8443/llm-proxy",
			ProxyDialect: "anthropic",
			LeaseToken:   "lt-" + leaseID,
		},
	}
}

// userLease returns a user-backed direct lease bound to session.
func userLease(leaseID, sessionID string) credential.Lease {
	return credential.Lease{
		LeaseID:       leaseID,
		SessionID:     sessionID,
		Provider:      credential.ProviderAnthropicDirect,
		Source:        credential.SourceUser,
		TenantID:      "acme",
		CredentialRef: "cred-" + leaseID,
		DeliveryMode:  credential.DeliveryDirect,
		IssuedAt:      time.Now(),
		ExpiresAt:     time.Now().Add(time.Hour),
	}
}

// recordingReleaser removes the lease record on every Release, as both
// assigners do, records the call, and fails for the leases in failIDs.
type recordingReleaser struct {
	leases   *credleasestore.Store
	failIDs  map[string]bool
	released []string
}

func (r *recordingReleaser) Release(leaseID string) error {
	r.released = append(r.released, leaseID)
	r.leases.Remove(leaseID)
	if r.failIDs[leaseID] {
		return errors.New("token service: revoke credentials: unavailable")
	}
	return nil
}

// spec: §11.4 (User Invalidation: Full revoke propagation mechanism), §4.9 (Credential Leasing Service)
func TestRevokerReturnsEveryListedLease_spec_11_4(t *testing.T) {
	for _, tc := range []struct {
		name     string
		lease    func(leaseID, sessionID string) credential.Lease
		failIDs  map[string]bool
		sessions []string
		want     []string
	}{
		{name: "pool_leases", lease: poolLease, sessions: []string{"run_a", "run_c"}, want: []string{"l1", "l2", "l4"}},
		{name: "user_leases", lease: userLease, sessions: []string{"run_a", "run_c"}, want: []string{"l1", "l2", "l4"}},
		{name: "pool_lease_with_a_failed_release", lease: poolLease, failIDs: map[string]bool{"l2": true}, sessions: []string{"run_a", "run_c"}, want: []string{"l1", "l2", "l4"}},
		{name: "user_lease_with_a_failed_release", lease: userLease, failIDs: map[string]bool{"l1": true}, sessions: []string{"run_a"}, want: []string{"l1", "l2"}},
		{name: "sessions_with_no_leases", lease: poolLease, sessions: []string{"run_x"}},
		{name: "empty_session_set", lease: userLease},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leases := credleasestore.New()
			for _, l := range []credential.Lease{tc.lease("l1", "run_a"), tc.lease("l2", "run_a"), tc.lease("l4", "run_c"), tc.lease("l3", "run_b")} {
				if err := leases.Put(l); err != nil {
					t.Fatalf("seed lease %s: %v", l.LeaseID, err)
				}
			}
			rel := &recordingReleaser{leases: leases, failIDs: tc.failIDs}

			n := leaserevoke.New(leases, rel, nil).RevokeUserLeases("acme", "alice@acme.com", tc.sessions)

			if n != len(tc.want) {
				t.Errorf("RevokeUserLeases counted %d leases, want %d (a failed Release still counts)", n, len(tc.want))
			}
			got := map[string]int{}
			for _, id := range rel.released {
				got[id]++
			}
			for _, id := range tc.want {
				if got[id] != 1 {
					t.Errorf("lease %s released %d times, want once", id, got[id])
				}
				if _, ok := leases.GetByID(id); ok {
					t.Errorf("returned lease %s is still in the store", id)
				}
			}
			if len(rel.released) != len(tc.want) {
				t.Errorf("released %v, want exactly %v", rel.released, tc.want)
			}
			if _, ok := leases.GetByID("l3"); !ok {
				t.Error("lease l3 of a session not being revoked was dropped")
			}
		})
	}
}

// TestRevokerTakesNoDenyListDependency_spec_11_4 pins that the lease step
// has no deny list to write to: New takes the lease store, the assigner,
// and a logger, and the Revoker holds no field whose type has a Revoke
// method over a credential identity.
//
// spec: §11.4 (User Invalidation: Full revoke propagation mechanism)
func TestRevokerTakesNoDenyListDependency_spec_11_4(t *testing.T) {
	newType := reflect.TypeOf(leaserevoke.New)
	if newType.NumIn() != 3 {
		t.Fatalf("leaserevoke.New takes %d parameters, want 3 (lease lister, releaser, logger)", newType.NumIn())
	}
	revokeKey := reflect.TypeOf(credential.CredentialKey{})
	rt := reflect.TypeOf(leaserevoke.Revoker{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		m, ok := f.Type.MethodByName("Revoke")
		if !ok {
			continue
		}
		mt := m.Type
		for j := 0; j < mt.NumIn(); j++ {
			if mt.In(j) == revokeKey {
				t.Errorf("Revoker field %s (%s) can write a credential deny-list entry", f.Name, f.Type)
			}
		}
	}
}
