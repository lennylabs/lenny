// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/credential"
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/leaserevoke"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// observedReleaser wraps the harness's credential service as the lease
// step's assigner and records, for each Release, the lease and what the
// step had done when it ran: how many deny-list entries existed and whether
// any listed session's row was already terminal. The production lease step
// drives it, so the order and the count the test asserts are the step's.
type observedReleaser struct {
	h        *lifecycleharness.Harness
	deny     *denylist.DenyList
	sessions []string
	releases map[string]int
	// denyBefore and cancelledBefore count the Releases that ran after a
	// deny-list write or after a listed session's cancel write.
	denyBefore, cancelledBefore int
}

func (o *observedReleaser) Release(leaseID string) error {
	o.releases[leaseID]++
	if o.deny.Len() != 0 {
		o.denyBefore++
	}
	for _, id := range o.sessions {
		if session.IsTerminal(o.h.Row(id).State) {
			o.cancelledBefore++
			break
		}
	}
	return o.h.Creds.Release(leaseID)
}

// harnessPodTerminator is the full_revoke binding release across the
// harness's replicas: the handling replica releases the bindings it holds
// through its revoked-binding releaser, and the request then reaches every
// peer, which releases the bindings it holds the same way, as the gateway's
// cross-replica fan-out delivers it. Only the handling replica's releases
// are reported.
type harnessPodTerminator struct {
	h        *lifecycleharness.Harness
	handling *lifecycleharness.Replica
}

func (p harnessPodTerminator) TerminateUserSessions(ctx context.Context, tenantID, _ string, ids []string) admin.UserTerminationResult {
	res := terminateOn(ctx, p.handling, tenantID, ids)
	for _, r := range p.h.Surviving() {
		if r != p.handling {
			_ = terminateOn(ctx, r, tenantID, ids)
		}
	}
	return res
}

// terminateOn releases the bindings of ids that r holds.
func terminateOn(ctx context.Context, r *lifecycleharness.Replica, tenantID string, ids []string) admin.UserTerminationResult {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var res admin.UserTerminationResult
	for _, b := range r.Registry.Snapshot() {
		if !want[b.SessionID] {
			continue
		}
		released, err := r.Server.ReleaseRevokedBinding(ctx, tenantID, b.SessionID)
		switch {
		case err != nil:
			res.FailedSessions = append(res.FailedSessions, b.SessionID)
		case released:
			res.PodsTerminated++
		}
	}
	return res
}

// spec: §11.4 (User Invalidation), §5.2 (Slot assignment atomicity), §4.9
// (Credential Leasing Service)
// diagnosis: a full_revoke handled on one replica, for sessions bound on that
// replica, on its peer, and not yet started, left a pod, a slot member, or a
// credential lease behind, or released one twice; or its response counted a
// different number of leases than it deny-listed.
func TestFullRevokeAcrossTwoReplicasReleasesEachSessionOnce_spec_11_4(t *testing.T) {
	ctx := context.Background()
	h := newConcurrentCompose(t, lifecycleharness.Options{})
	a, b := h.Replicas[0], h.Replicas[1]
	ids := []string{h.StartSession(a), h.StartSession(a), h.StartSession(b), h.CreateSession(b)}
	pods := map[string]string{}
	for _, id := range ids {
		pods[id] = h.Row(id).PodAssignment
	}
	// The harness runtime declares no providers, so the binds minted no
	// lease; each session leases one credential from the harness pool here.
	for _, id := range ids {
		if _, err := h.Creds.Assign(lifecycleharness.ProxyPool().Name, id, "", lifecycleharness.Tenant, h.Row(id).CoordinationGeneration); err != nil {
			t.Fatalf("lease a credential for %s: %v", id, err)
		}
	}
	listed := h.CredLeases.LeasesBySession(ids)
	if len(listed) != len(ids) {
		t.Fatalf("the listed sessions hold %d credential leases, want %d", len(listed), len(ids))
	}

	users := userstore.NewMemory()
	if err := users.Create(ctx, userstore.User{Subject: "alice@acme.com", TenantID: lifecycleharness.Tenant, Roles: []pkgauth.Role{pkgauth.RoleUser}}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	deny := denylist.New()
	releaser := &observedReleaser{h: h, deny: deny, sessions: ids, releases: map[string]int{}}
	step := leaserevoke.New(h.CredLeases, releaser, credrenewalprop.New(deny, nil, nil), nil)
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: h.Clock.Now}).
		WithUsers(users).
		WithSessions(h.Store).
		WithRevokeHook(a.Server).
		WithUserRevocation(harnessPodTerminator{h: h, handling: a}, step, nil)
	body, _ := json.Marshal(admin.InvalidateUserRequest{TenantID: lifecycleharness.Tenant, Mode: admin.InvalidateFullRevoke})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/users/alice@acme.com/invalidate", bytes.NewReader(body))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "admin@acme.com", TenantID: "platform", Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke = %d %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)

	h.RunToQuiescence()
	requireLeaseStepAccounting(t, resp, deny, releaser, listed)
	if n := len(h.CredLeases.LeasesBySession(ids)); n != 0 {
		t.Errorf("%d credential leases remain after full_revoke, want none", n)
	}
	for _, id := range ids {
		if st := h.Row(id).State; st != session.StateCancelled {
			t.Errorf("session %s = %s, want cancelled", id, st)
		}
		if n := h.Slots.Removals(pods[id], id); n != 1 {
			t.Errorf("removals of %s's member on %s = %d, want 1", id, pods[id], n)
		}
	}
	for _, pod := range pods {
		requireMembers(t, h, pod)
		requireClaim(t, h, pod, "")
	}
	h.Check(t)
}

// requireLeaseStepAccounting asserts the production lease step's accounting
// against independent observations: the response's leasesRevoked equals the
// leases enumerated before the revoke and the leases the credential service
// saw released, each exactly once; the deny list read back holds every
// credential those leases name; and every Release ran before the first
// deny-list write and before any listed session's cancel write.
func requireLeaseStepAccounting(t *testing.T, resp map[string]any, deny *denylist.DenyList, r *observedReleaser, listed []credential.Lease) {
	t.Helper()
	keys := map[credential.CredentialKey]bool{}
	for _, l := range listed {
		keys[l.CredentialKey()] = true
	}
	if got := resp["leasesRevoked"]; got != float64(len(listed)) || len(r.releases) != len(listed) {
		t.Errorf("leasesRevoked = %v with %d leases released, want both %d", got, len(r.releases), len(listed))
	}
	if deny.Len() != len(keys) {
		t.Errorf("the deny list holds %d entries, want the %d credentials the leases name", deny.Len(), len(keys))
	}
	for _, l := range listed {
		if n := r.releases[l.LeaseID]; n != 1 {
			t.Errorf("lease %s of %s was released %d times, want once", l.LeaseID, l.SessionID, n)
		}
		if !deny.Revoked(l.CredentialKey()) {
			t.Errorf("lease %s's credential is not on the deny list", l.LeaseID)
		}
	}
	if r.denyBefore != 0 || r.cancelledBefore != 0 {
		t.Errorf("%d Releases ran after a deny-list write and %d after a cancel write, want every Release first", r.denyBefore, r.cancelledBefore)
	}
}
