// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 coverage for the §11.4 full_revoke pre-running reclaim of a session
// in starting. A /start writes starting before its launch, so a revoked
// user's session can be in starting when the revoke cancels it. The revoke's
// pre-running reclaim covers starting alongside created, finalizing, and
// ready, so the revoked user's claimed pod returns to the pool by name, and
// another user's starting session keeps its row and its claim.
//
// spec: §11.4 (User Invalidation), §7.2 (Session state machine: starting),
// §4.6 (durable binding)
package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

const revokeReclaimNS = "lenny-agents"

// revokeReclaimClaimPresent reports whether pod's per-pod claim exists.
func revokeReclaimClaimPresent(t *testing.T, c client.Client, pod string) bool {
	t.Helper()
	err := c.Get(context.Background(), client.ObjectKey{Namespace: revokeReclaimNS, Name: podclaim.ClaimName(pod)}, &lennyv1.SandboxClaim{})
	return err == nil
}

// spec: §11.4 (User Invalidation), §7.2 (Session state machine: starting),
// §4.6 (durable binding)
// diagnosis: a full_revoke of alice left the claim of her starting session's
// pod in place, so the pod stays held for a cancelled session until the
// orphan-claim GC, or the revoke reached bob's starting session (cancelled
// it or deleted its claim). starting is missing from the revoke's
// pre-running states, or the reclaim crossed users.
func TestFullRevokeReclaimsARevokedUsersStartingSession_spec_11_4(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := lennyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&lennyv1.SandboxClaim{ObjectMeta: metav1.ObjectMeta{Name: podclaim.ClaimName("pod-alice"), Namespace: revokeReclaimNS}},
		&lennyv1.SandboxClaim{ObjectMeta: metav1.ObjectMeta{Name: podclaim.ClaimName("pod-alice-ready"), Namespace: revokeReclaimNS}},
		&lennyv1.SandboxClaim{ObjectMeta: metav1.ObjectMeta{Name: podclaim.ClaimName("pod-bob"), Namespace: revokeReclaimNS}},
	).Build()
	sessions := memstore.New()
	for _, row := range []sessionstore.Session{
		{ID: "s_alice_starting", UserID: "alice@acme.com", State: session.StateStarting, PodAssignment: "pod-alice"},
		{ID: "s_alice_ready", UserID: "alice@acme.com", State: session.StateReady, PodAssignment: "pod-alice-ready"},
		{ID: "s_bob_starting", UserID: "bob@acme.com", State: session.StateStarting, PodAssignment: "pod-bob"},
	} {
		row.TenantID, row.RuntimeRef = "acme", "echo"
		if err := sessions.Create(ctx, row); err != nil {
			t.Fatalf("seed %s: %v", row.ID, err)
		}
	}
	users := userstore.NewMemory()
	for _, u := range []string{"alice@acme.com", "bob@acme.com"} {
		if err := users.Create(ctx, userstore.User{Subject: u, TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}}); err != nil {
			t.Fatalf("seed user %s: %v", u, err)
		}
	}
	srv := sessionserver.New(sessions, sessionserver.Options{
		PodBinder:      &podsession.Binder{Client: kube, Namespace: revokeReclaimNS},
		PodRegistry:    podsession.NewRegistry(),
		AgentNamespace: revokeReclaimNS,
	})
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: time.Now}).
		WithUsers(users).
		WithSessions(sessions).
		WithRevokeHook(srv)

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

	for _, id := range []string{"s_alice_starting", "s_alice_ready"} {
		row, err := sessions.Get(ctx, "acme", id)
		if err != nil || row.State != session.StateCancelled {
			t.Errorf("%s = %q (%v), want cancelled", id, row.State, err)
		}
	}
	if revokeReclaimClaimPresent(t, kube, "pod-alice") {
		t.Error("the claim of alice's starting session is still present, want the pre-running reclaim to delete it")
	}
	if revokeReclaimClaimPresent(t, kube, "pod-alice-ready") {
		t.Error("the claim of alice's ready session is still present, want the pre-running reclaim to delete it")
	}
	bob, err := sessions.Get(ctx, "acme", "s_bob_starting")
	if err != nil || bob.State != session.StateStarting {
		t.Errorf("bob's session = %q (%v), want starting untouched", bob.State, err)
	}
	if !revokeReclaimClaimPresent(t, kube, "pod-bob") {
		t.Error("the claim of bob's starting session was deleted by alice's revoke")
	}
}
