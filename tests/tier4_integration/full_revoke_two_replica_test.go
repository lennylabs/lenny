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
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// harnessLeaseStep is the full_revoke lease step over the harness's
// credential service: it returns every lease of the listed sessions to its
// pool, then deny-lists every one, and counts the deny-list writes.
type harnessLeaseStep struct {
	h          *lifecycleharness.Harness
	deny       *credrenewalprop.Propagator
	denyWrites int
}

func (s *harnessLeaseStep) RevokeUserLeases(_, _ string, ids []string) int {
	leases := s.h.CredLeases.LeasesBySession(ids)
	for _, l := range leases {
		_ = s.h.Creds.Release(l.LeaseID)
	}
	for _, l := range leases {
		s.deny.Revoke(l.CredentialKey())
		s.denyWrites++
	}
	return len(leases)
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
	leasesBefore := len(h.CredLeases.LeasesBySession(ids))
	if leasesBefore != len(ids) {
		t.Fatalf("the listed sessions hold %d credential leases, want %d", leasesBefore, len(ids))
	}

	users := userstore.NewMemory()
	if err := users.Create(ctx, userstore.User{Subject: "alice@acme.com", TenantID: lifecycleharness.Tenant, Roles: []pkgauth.Role{pkgauth.RoleUser}}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	deny := denylist.New()
	step := &harnessLeaseStep{h: h, deny: credrenewalprop.New(deny, nil, nil)}
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
	if got := resp["leasesRevoked"]; got != float64(step.denyWrites) || step.denyWrites != leasesBefore {
		t.Errorf("leasesRevoked = %v with %d deny-list writes, want both %d", got, step.denyWrites, leasesBefore)
	}
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
