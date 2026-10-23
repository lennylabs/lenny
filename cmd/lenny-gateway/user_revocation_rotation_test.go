// SPDX-License-Identifier: MIT

package main

import (
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/leaserevoke"
)

// singleCredentialPool registers a direct-delivery pool with one
// credential, so every lease the test mints is on the same credential.
func singleCredentialPool(t *testing.T) (*credassign.Service, *credleasestore.Store) {
	t.Helper()
	leases := credleasestore.New()
	svc := credassign.New(leases, credcache.New())
	svc.RegisterPool(credassign.Pool{
		Name:         "claude-prod",
		Provider:     credential.ProviderAnthropicDirect,
		DeliveryMode: credential.DeliveryDirect,
		Strategy:     credential.StrategyLeastLoaded,
		Credentials: []credassign.PoolCredential{
			{ID: "key-1", APIKey: "sk-ant-1", Healthy: true},
		},
	})
	return svc, leases
}

// denyListingLeaseStep is the lease step the gateway must not run: it
// returns every listed lease and then deny-lists each lease's credential.
// On a shared pool credential the deny-list write reaches every lease on
// that credential, including other users'.
type denyListingLeaseStep struct {
	leases   leaserevoke.LeaseLister
	assigner leaserevoke.Releaser
	denyList credentialDenyList
}

func (d *denyListingLeaseStep) RevokeUserLeases(_, _ string, sessionIDs []string) int {
	got := d.leases.LeasesBySession(sessionIDs)
	for _, l := range got {
		_ = d.assigner.Release(l.LeaseID)
	}
	for _, l := range got {
		d.denyList.Revoke(l.CredentialKey())
	}
	return len(got)
}

// TestFullRevokeLeaseStepLeavesOtherUsersOnThePoolCredential_spec_11_4 runs
// the lease step over a real credential-assignment service and the
// credential-lease propagator carrying the direct-mode rotator's revoke
// hook. alice's two revoked sessions and bob's live session each hold a
// direct-delivery lease on the one pool credential. The production step
// returns alice's leases and writes no deny-list entry, so the credential
// is never marked revoked and bob's lease is neither rotated nor removed.
// A step that deny-lists the shared credential rotates bob's session and
// marks the credential unselectable, which is what the case discriminates.
//
// spec: §11.4 (User Invalidation: Full revoke propagation mechanism), §4.9 (Credential Leasing Service)
func TestFullRevokeLeaseStepLeavesOtherUsersOnThePoolCredential_spec_11_4(t *testing.T) {
	for _, tc := range []struct {
		name        string
		denyListing bool
		// wantBobHit is true when bob's session is rotated and the shared
		// credential marked revoked, which only a deny-list write causes.
		wantBobHit bool
		wantDenied int
	}{
		{name: "production_lease_step"},
		{name: "deny_listing_lease_step", denyListing: true, wantBobHit: true, wantDenied: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, leases := singleCredentialPool(t)
			for _, id := range []string{"s_1", "s_2", "s_bob"} {
				if _, err := svc.Assign("claude-prod", id, "", "acme", 1); err != nil {
					t.Fatalf("seed Assign %s: %v", id, err)
				}
			}
			var mu sync.Mutex
			var rotated []string
			markings := 0
			rotator := &directModeRevocationRotator{
				leases: leases,
				markRevoked: func(poolID, credentialID string) {
					markings++
					svc.RevokeCredential(poolID, credentialID)
				},
				rotate: func(l credential.Lease, _ string, _ credential.RotationTrigger) {
					mu.Lock()
					defer mu.Unlock()
					rotated = append(rotated, l.SessionID)
				},
				dispatch: func(fn func()) { fn() },
			}
			deny := denylist.New()
			prop := credrenewalprop.New(deny, nil, nil, credrenewalprop.WithRevokeHook(rotator.onRevoke))
			var step interface {
				RevokeUserLeases(tenantID, userID string, sessionIDs []string) int
			} = leaserevoke.New(leases, svc, nil)
			if tc.denyListing {
				step = &denyListingLeaseStep{leases: leases, assigner: svc, denyList: prop}
			}
			if n := step.RevokeUserLeases("acme", "alice@acme.com", []string{"s_1", "s_2"}); n != 2 {
				t.Fatalf("lease step counted %d, want 2", n)
			}
			if got := leases.LeasesBySession([]string{"s_1", "s_2"}); len(got) != 0 {
				t.Errorf("alice's lease records left after the lease step: %d", len(got))
			}
			if got := leases.LeasesBySession([]string{"s_bob"}); len(got) != 1 {
				t.Errorf("bob's lease records = %d, want his one lease kept", len(got))
			}
			if deny.Len() != tc.wantDenied {
				t.Errorf("deny list holds %d entries, want %d", deny.Len(), tc.wantDenied)
			}
			if (markings > 0) != tc.wantBobHit {
				t.Errorf("the shared pool credential was marked revoked %d times, want marked = %v", markings, tc.wantBobHit)
			}
			bobRotated := false
			for _, id := range rotated {
				if id != "s_bob" {
					t.Errorf("rotated sessions = %v; only bob's live session still holds a lease", rotated)
				}
				bobRotated = true
			}
			if bobRotated != tc.wantBobHit {
				t.Errorf("rotated sessions = %v, want bob's session rotated = %v", rotated, tc.wantBobHit)
			}
		})
	}
}

// TestFallbackMintAfterTheLeaseStepIsReturnedByTheSessionRelease_spec_11_4
// covers a lease minted for a listed running session after the lease step's
// enumeration, through a §4.9 fallback rotation whose upstream request the
// LLM proxy admitted before the step. The lease step does not count it and
// deny-lists nothing; the session's own release returns it, because the fallback
// mint copies the faulted lease's coordination generation and the pod
// release returns the leases at or below its binding's stamp.
//
// spec: §11.4 (User Invalidation: Full revoke propagation mechanism), §7.3 (Credential leases on the
// replacement pod), §4.9 (Credential Leasing Service)
func TestFallbackMintAfterTheLeaseStepIsReturnedByTheSessionRelease_spec_11_4(t *testing.T) {
	svc, leases := singleCredentialPool(t)
	faulted, err := svc.Assign("claude-prod", "s_1", "", "acme", 2)
	if err != nil {
		t.Fatalf("seed Assign: %v", err)
	}
	revoker := leaserevoke.New(leases, svc, nil)
	if n := revoker.RevokeUserLeases("acme", "alice@acme.com", []string{"s_1"}); n != 1 {
		t.Fatalf("lease step counted %d, want 1", n)
	}
	// The fallback mint lands after the enumeration.
	proxyFallbackRotator{assign: svc}.Rotate(faulted, "claude-prod", credential.TriggerFaultRateLimited)
	if got := leases.LeasesBySession([]string{"s_1"}); len(got) != 1 || got[0].CoordinationGeneration != 2 {
		t.Fatalf("fallback-minted leases = %+v, want one at generation 2", got)
	}
	// The session's binding release returns it.
	if err := svc.ReleaseThrough("s_1", 2); err != nil {
		t.Fatalf("ReleaseThrough: %v", err)
	}
	if got := leases.LeasesBySession([]string{"s_1"}); len(got) != 0 {
		t.Errorf("leases after the session release = %+v, want none", got)
	}
}
