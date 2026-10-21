// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 coverage for the §11.4 full_revoke lease step: every credential
// lease of every session the revoke lists is returned to its pool and then
// deny-listed before any cancel write, and the reported lease count is the
// number deny-listed.
//
// The case drives the admin router's full_revoke handler and the gateway's
// lease step (leaserevoke.Revoker) over a real credential-assignment
// service, a real lease store, and a real deny list behind the
// cross-replica revocation propagator. The assigner the step drives is
// observed, so the case pins which sessions reach the lease step, that
// every Release runs before the first deny-list write and before any cancel
// write, and that the response reports the deny-listed count read back
// from the deny list.
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
	"github.com/lennylabs/lenny/pkg/gateway/credentials/leaserevoke"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// observedAssigner is the credential assigner the production lease step
// drives. For each Release it records the lease and whether the deny list
// already held an entry or a listed row was already cancelled, which must
// both be never.
type observedAssigner struct {
	sessions  sessionstore.Store
	assign    *credassign.Service
	deny      *denylist.DenyList
	ids       []string
	released  map[string]int
	afterDeny int
	// cancelledAtStep counts the Releases that ran after a listed row's
	// cancel write.
	cancelledAtStep int
}

func (o *observedAssigner) Release(leaseID string) error {
	o.released[leaseID]++
	if o.deny.Len() != 0 {
		o.afterDeny++
	}
	for _, id := range o.ids {
		if row, err := o.sessions.Get(context.Background(), "acme", id); err == nil && row.State == session.StateCancelled {
			o.cancelledAtStep++
			break
		}
	}
	return o.assign.Release(leaseID)
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
	observed := &observedAssigner{sessions: sessions, assign: svc, deny: deny, ids: []string{"s_ready", "s_running", "s_suspended"}, released: map[string]int{}}
	step := leaserevoke.New(leases, observed, credrenewalprop.New(deny, nil, nil), nil)
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

	if observed.cancelledAtStep != 0 || observed.afterDeny != 0 {
		t.Errorf("%d Releases ran after a cancel write and %d after a deny-list write; the lease step must return every lease first",
			observed.cancelledAtStep, observed.afterDeny)
	}
	for _, l := range minted {
		if _, ok := leases.GetByID(l.LeaseID); ok {
			t.Errorf("lease %s of %s is still recorded; its pool slot was not returned", l.LeaseID, l.SessionID)
		}
		if n := observed.released[l.LeaseID]; n != 1 {
			t.Errorf("lease %s of %s was released %d times, want once", l.LeaseID, l.SessionID, n)
		}
		if !deny.Revoked(l.CredentialKey()) {
			t.Errorf("lease %s of %s: credential %s is not deny-listed", l.LeaseID, l.SessionID, l.CredentialID)
		}
	}
	keys := map[credential.CredentialKey]bool{}
	for _, l := range minted {
		keys[l.CredentialKey()] = true
	}
	if got := resp["leasesRevoked"]; got != float64(len(minted)) || len(observed.released) != len(minted) {
		t.Errorf("leasesRevoked = %v with %d leases released, want both %d", got, len(observed.released), len(minted))
	}
	if deny.Len() != len(keys) {
		t.Errorf("the deny list holds %d entries, want the %d credentials the leases name", deny.Len(), len(keys))
	}
	if resp["sessionsTerminated"] != float64(len(listed)) {
		t.Errorf("sessionsTerminated = %v, want %d", resp["sessionsTerminated"], len(listed))
	}
}
