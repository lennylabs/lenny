// SPDX-License-Identifier: MIT

package leaserevoke_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/leaserevoke"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

var _ admin.UserLeaseRevoker = (*leaserevoke.Revoker)(nil)

// newLocalDenyList builds the cross-replica deny list the Revoker requires:
// a credential-lease revocation propagator over a fresh DenyList with a nil
// bus, which applies locally and still carries the cross-replica marker.
// The DenyList is returned for assertions.
func newLocalDenyList() (*credrenewalprop.Propagator, *denylist.DenyList) {
	dl := denylist.New()
	return credrenewalprop.New(dl, nil, nil), dl
}

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

// orderedLeaseStep records, in one sequence, every Release and every
// deny-list write, so a test can assert that each Release precedes the
// first deny-list write. Release removes the lease record, as both
// assigners do, and fails for the leases named in failIDs.
type orderedLeaseStep struct {
	leases  *credleasestore.Store
	failIDs map[string]bool
	events  []string
}

func (o *orderedLeaseStep) Release(leaseID string) error {
	o.events = append(o.events, "release:"+leaseID)
	o.leases.Remove(leaseID)
	if o.failIDs[leaseID] {
		return errors.New("token service: revoke credentials: unavailable")
	}
	return nil
}

func (o *orderedLeaseStep) Revoke(key credential.CredentialKey) {
	o.events = append(o.events, "deny:"+key.CredentialID)
}

func (o *orderedLeaseStep) IsCrossReplicaPropagator() {}

// spec: §11.4 (User Invalidation), §4.9 (Credential Leasing Service)
func TestRevokerReturnsAndDenyListsTheListedSessionsLeases_spec_11_4(t *testing.T) {
	leases := credleasestore.New()
	prop, deny := newLocalDenyList()
	_ = leases.Put(poolLease("l1", "run_a"))
	_ = leases.Put(poolLease("l2", "run_a"))
	_ = leases.Put(poolLease("l4", "run_c"))
	_ = leases.Put(poolLease("l3", "run_b")) // a session not being revoked
	step := &orderedLeaseStep{leases: leases, failIDs: map[string]bool{"l2": true}}

	n := leaserevoke.New(leases, step, prop, nil).RevokeUserLeases("acme", "alice@acme.com", []string{"run_a", "run_c"})
	if n != 3 {
		t.Fatalf("RevokeUserLeases counted %d leases, want 3 (a failed Release still counts)", n)
	}
	for _, id := range []string{"l1", "l2", "l4"} {
		if _, ok := leases.GetByID(id); ok {
			t.Errorf("returned lease %s still in the store", id)
		}
		if !deny.Revoked(poolLease(id, "").CredentialKey()) {
			t.Errorf("lease %s's credential is not on the deny list", id)
		}
	}
	if _, ok := leases.GetByID("l3"); !ok {
		t.Error("lease l3 for an unrevoked session was dropped")
	}
	if deny.Revoked(poolLease("l3", "run_b").CredentialKey()) {
		t.Error("lease l3's credential was wrongly added to the deny list")
	}
	if deny.Len() != n {
		t.Errorf("deny list holds %d entries, want the counted %d", deny.Len(), n)
	}
}

// spec: §11.4 (User Invalidation)
func TestRevokerReturnsEveryLeaseBeforeTheFirstDenial_spec_11_4(t *testing.T) {
	leases := credleasestore.New()
	_ = leases.Put(poolLease("l1", "run_a"))
	_ = leases.Put(poolLease("l2", "run_b"))
	_ = leases.Put(poolLease("l3", "run_b"))
	step := &orderedLeaseStep{leases: leases, failIDs: map[string]bool{"l1": true}}
	if n := leaserevoke.New(leases, step, step, nil).RevokeUserLeases("acme", "alice@acme.com", []string{"run_a", "run_b"}); n != 3 {
		t.Fatalf("RevokeUserLeases counted %d, want 3", n)
	}
	firstDeny, lastRelease, denies := -1, -1, 0
	for i, e := range step.events {
		switch {
		case strings.HasPrefix(e, "deny:"):
			denies++
			if firstDeny < 0 {
				firstDeny = i
			}
		case strings.HasPrefix(e, "release:"):
			lastRelease = i
		}
	}
	if denies != 3 || lastRelease < 0 || firstDeny < lastRelease {
		t.Errorf("events = %v, want three releases before three deny-list writes", step.events)
	}
}

// spec: §11.4 (User Invalidation)
func TestRevokerWithNoLeasesDenyListsNothing_spec_11_4(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sessions []string
	}{
		{name: "sessions_with_no_leases", sessions: []string{"run_x"}},
		{name: "empty_session_set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leases := credleasestore.New()
			_ = leases.Put(poolLease("l1", "run_a"))
			prop, deny := newLocalDenyList()
			step := &orderedLeaseStep{leases: leases}
			if n := leaserevoke.New(leases, step, prop, nil).RevokeUserLeases("acme", "bob@acme.com", tc.sessions); n != 0 {
				t.Errorf("RevokeUserLeases revoked %d, want 0", n)
			}
			if deny.Len() != 0 || len(step.events) != 0 {
				t.Errorf("deny list gained %d entries and the step recorded %v, want none", deny.Len(), step.events)
			}
			if _, ok := leases.GetByID("l1"); !ok {
				t.Error("an unrelated lease was dropped")
			}
		})
	}
}

// spec: §11.4 (User Invalidation)
func TestRevokerRequiresTheCrossReplicaPropagator_spec_11_4(t *testing.T) {
	var _ leaserevoke.DenyList = (*credrenewalprop.Propagator)(nil)
	var d any = denylist.New()
	if _, ok := d.(leaserevoke.DenyList); ok {
		t.Fatal("a bare *denylist.DenyList satisfies leaserevoke.DenyList; the cross-replica marker is missing, so the full_revoke deny-list write would stay on one replica")
	}
}
