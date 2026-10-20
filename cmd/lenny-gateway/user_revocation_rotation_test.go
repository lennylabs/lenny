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

// perLeaseRevoker is the interleaving the lease step must not use: it
// returns and deny-lists one lease at a time.
type perLeaseRevoker struct {
	leases   userLeaseStore
	assigner userLeaseReleaser
	denyList credentialDenyList
}

func (p *perLeaseRevoker) RevokeUserLeases(_, _ string, sessionIDs []string) int {
	got := p.leases.LeasesBySession(sessionIDs)
	for _, l := range got {
		_ = p.assigner.Release(l.LeaseID)
		p.denyList.Revoke(l.CredentialKey())
	}
	return len(got)
}

// TestFullRevokeLeaseStepRotatesNoRevokedSession_spec_11_4 runs the lease
// step over a real credential-assignment service and the credential-lease
// propagator carrying the direct-mode rotator's revoke hook, for two revoked
// sessions each holding a direct-delivery lease on one pool credential. The
// lease step returns both leases before the first deny-list write, so the
// rotator finds no lease to rotate and pushes no fresh key to a revoked
// session. The per-lease interleaving rotates the second session's lease,
// which is what the case discriminates.
//
// spec: §11.4 step 1 (User Invalidation), §4.9 (Credential Leasing Service)
func TestFullRevokeLeaseStepRotatesNoRevokedSession_spec_11_4(t *testing.T) {
	for _, tc := range []struct {
		name        string
		perLease    bool
		wantRotated int
	}{
		{name: "return_all_then_deny", wantRotated: 0},
		{name: "per_lease_interleaving", perLease: true, wantRotated: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, leases := singleCredentialPool(t)
			for _, id := range []string{"s_1", "s_2"} {
				if _, err := svc.Assign("claude-prod", id, "", "acme", 1); err != nil {
					t.Fatalf("seed Assign %s: %v", id, err)
				}
			}
			var mu sync.Mutex
			var rotated []string
			rotator := &directModeRevocationRotator{
				leases:      leases,
				markRevoked: svc.RevokeCredential,
				rotate: func(l credential.Lease, _ string, _ credential.RotationTrigger) {
					mu.Lock()
					defer mu.Unlock()
					rotated = append(rotated, l.SessionID)
				},
				dispatch: func(fn func()) { fn() },
			}
			prop := credrenewalprop.New(denylist.New(), nil, nil, credrenewalprop.WithRevokeHook(rotator.onRevoke))
			var step interface {
				RevokeUserLeases(tenantID, userID string, sessionIDs []string) int
			} = &userLeaseRevoker{leases: leases, assigner: svc, denyList: prop}
			if tc.perLease {
				step = &perLeaseRevoker{leases: leases, assigner: svc, denyList: prop}
			}
			if n := step.RevokeUserLeases("acme", "alice@acme.com", []string{"s_1", "s_2"}); n != 2 {
				t.Fatalf("lease step counted %d, want 2", n)
			}
			if len(rotated) != tc.wantRotated {
				t.Errorf("rotated sessions = %v, want %d", rotated, tc.wantRotated)
			}
			if got := leases.LeasesBySession([]string{"s_1", "s_2"}); len(got) != 0 {
				t.Errorf("lease records left after the lease step: %d", len(got))
			}
		})
	}
}

// TestFallbackMintAfterTheLeaseStepIsReturnedByTheSessionRelease_spec_11_4
// covers a lease minted for a listed running session after the lease step's
// enumeration, through a §4.9 fallback rotation whose upstream request the
// LLM proxy admitted before the step. The lease step neither deny-lists nor
// counts it; the session's own release returns it, because the fallback
// mint copies the faulted lease's coordination generation and the pod
// release returns the leases at or below its binding's stamp.
//
// spec: §11.4 step 1 (User Invalidation), §7.3 (Credential leases on the
// replacement pod), §4.9 (Credential Leasing Service)
func TestFallbackMintAfterTheLeaseStepIsReturnedByTheSessionRelease_spec_11_4(t *testing.T) {
	svc, leases := singleCredentialPool(t)
	faulted, err := svc.Assign("claude-prod", "s_1", "", "acme", 2)
	if err != nil {
		t.Fatalf("seed Assign: %v", err)
	}
	prop, deny := newLocalRevocationDenyList()
	revoker := &userLeaseRevoker{leases: leases, assigner: svc, denyList: prop}
	if n := revoker.RevokeUserLeases("acme", "alice@acme.com", []string{"s_1"}); n != 1 || deny.Len() != 1 {
		t.Fatalf("lease step counted %d with %d deny-list entries, want 1 and 1", n, deny.Len())
	}
	// The fallback mint lands after the enumeration.
	proxyFallbackRotator{assign: svc}.Rotate(faulted, "claude-prod", credential.TriggerFaultRateLimited)
	if got := leases.LeasesBySession([]string{"s_1"}); len(got) != 1 || got[0].CoordinationGeneration != 2 {
		t.Fatalf("fallback-minted leases = %+v, want one at generation 2", got)
	}
	if deny.Len() != 1 {
		t.Errorf("deny list holds %d entries, want the lease step's 1", deny.Len())
	}
	// The session's binding release returns it.
	if err := svc.ReleaseThrough("s_1", 2); err != nil {
		t.Fatalf("ReleaseThrough: %v", err)
	}
	if got := leases.LeasesBySession([]string{"s_1"}); len(got) != 0 {
		t.Errorf("leases after the session release = %+v, want none", got)
	}
}
