// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 coverage for the §11.4 full_revoke lease step: every credential
// lease of every session the revoke lists is returned to its pool and then
// deny-listed before any cancel write, and the reported lease count is the
// number deny-listed.
//
// The case drives the admin router's full_revoke handler over a real
// credential-assignment service, a real lease store, and a real deny list
// behind the cross-replica revocation propagator. The lease revoker here
// composes those the way the gateway's lease step does (return every lease,
// then deny-list every lease); the gateway adapter's own order is pinned at
// tier 1 in cmd/lenny-gateway. What this case pins is the handler's side:
// which sessions reach the lease step, that it runs before the cancel
// writes, and that the response reports the deny-listed count.
//
// spec: §11.4; §4.9.
package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// returnThenDeny is the full_revoke lease step over the real services: it
// returns every enumerated lease to its pool, then deny-lists every one.
// cancelledAtStep records how many listed sessions were already cancelled
// when the step ran, which must be none.
type returnThenDeny struct {
	sessions        sessionstore.Store
	leases          *credleasestore.Store
	assign          *credassign.Service
	deny            *credrenewalprop.Propagator
	cancelledAtStep int
	denyWrites      int
}

func (r *returnThenDeny) RevokeUserLeases(tenantID, _ string, ids []string) int {
	for _, id := range ids {
		if row, err := r.sessions.Get(context.Background(), tenantID, id); err == nil && row.State == session.StateCancelled {
			r.cancelledAtStep++
		}
	}
	got := r.leases.LeasesBySession(ids)
	for _, l := range got {
		_ = r.assign.Release(l.LeaseID)
	}
	for _, l := range got {
		r.deny.Revoke(l.CredentialKey())
		r.denyWrites++
	}
	return len(got)
}

// spec: 11.4 (User Invalidation), 4.9 (Credential Leasing Service)
// diagnosis: a full_revoke left a revoked user's credential lease usable or
// misreported. A lease still recorded after the call means its pool slot
// was not returned; a lease whose credential is not on the deny list means
// the LLM proxy still accepts its token; a lease step that saw a cancelled
// row means it ran after a cancel write, when a terminal release could
// already have removed a lease it then never deny-lists; a leasesRevoked
// that differs from the deny-listed count means the response misreports
// the step.
func TestFullRevokeReturnsAndDenyListsEveryListedLease_spec_11_4(t *testing.T) {
	ctx := context.Background()
	leases := credleasestore.New()
	svc := credassign.New(leases, credcache.New())
	svc.RegisterPool(credassign.Pool{
		Name:         "claude-prod",
		Provider:     credential.ProviderAnthropicDirect,
		DeliveryMode: credential.DeliveryDirect,
		Strategy:     credential.StrategyLeastLoaded,
		Credentials: []credassign.PoolCredential{
			{ID: "key-1", APIKey: "sk-ant-1", Healthy: true},
			{ID: "key-2", APIKey: "sk-ant-2", Healthy: true},
			{ID: "key-3", APIKey: "sk-ant-3", Healthy: true},
		},
	})
	sessions := memstore.New()
	users := userstore.NewMemory()
	if err := users.Create(ctx, userstore.User{Subject: "alice@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	listed := map[string]session.State{
		"s_ready":     session.StateReady,
		"s_running":   session.StateRunning,
		"s_suspended": session.StateSuspended,
	}
	for id, st := range listed {
		if err := sessions.Create(ctx, sessionstore.Session{ID: id, TenantID: "acme", UserID: "alice@acme.com", State: st}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	if err := sessions.Create(ctx, sessionstore.Session{ID: "s_done", TenantID: "acme", UserID: "alice@acme.com", State: session.StateCompleted}); err != nil {
		t.Fatalf("seed s_done: %v", err)
	}
	var minted []credential.Lease
	for _, id := range []string{"s_ready", "s_running", "s_running", "s_suspended"} {
		l, err := svc.Assign("claude-prod", id, "", "acme", 1)
		if err != nil {
			t.Fatalf("Assign %s: %v", id, err)
		}
		minted = append(minted, l)
	}
	deny := denylist.New()
	step := &returnThenDeny{sessions: sessions, leases: leases, assign: svc, deny: credrenewalprop.New(deny, nil, nil)}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: time.Now}).
		WithUsers(users).
		WithSessions(sessions).
		WithUserRevocation(nil, step, nil)

	b, _ := json.Marshal(admin.InvalidateUserRequest{TenantID: "acme", Mode: admin.InvalidateFullRevoke})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/users/alice@acme.com/invalidate", bytes.NewReader(b))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "admin@acme.com", TenantID: "platform", Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("full_revoke: status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)

	if step.cancelledAtStep != 0 {
		t.Errorf("the lease step saw %d cancelled rows; it must run before every cancel write", step.cancelledAtStep)
	}
	for _, l := range minted {
		if _, ok := leases.GetByID(l.LeaseID); ok {
			t.Errorf("lease %s of %s is still recorded; its pool slot was not returned", l.LeaseID, l.SessionID)
		}
		if !deny.Revoked(l.CredentialKey()) {
			t.Errorf("lease %s of %s: credential %s is not deny-listed", l.LeaseID, l.SessionID, l.CredentialID)
		}
	}
	if got := resp["leasesRevoked"]; got != float64(step.denyWrites) || step.denyWrites != len(minted) {
		t.Errorf("leasesRevoked = %v with %d deny-list writes, want both %d", got, step.denyWrites, len(minted))
	}
	if resp["sessionsTerminated"] != float64(len(listed)) {
		t.Errorf("sessionsTerminated = %v, want %d", resp["sessionsTerminated"], len(listed))
	}
}
