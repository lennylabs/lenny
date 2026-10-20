// SPDX-License-Identifier: MIT

//go:build load_local

package tier7a_load_local_test

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// localTerminator is the full_revoke binding release on the handling
// replica alone: it releases each listed binding the replica holds through
// its revoked-binding releaser.
type localTerminator struct{ r *lifecycleharness.Replica }

func (l localTerminator) TerminateUserSessions(ctx context.Context, tenantID, _ string, ids []string) admin.UserTerminationResult {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var res admin.UserTerminationResult
	for _, b := range l.r.Registry.Snapshot() {
		if !want[b.SessionID] {
			continue
		}
		released, err := l.r.Server.ReleaseRevokedBinding(ctx, tenantID, b.SessionID)
		switch {
		case err != nil:
			res.FailedSessions = append(res.FailedSessions, b.SessionID)
		case released:
			res.PodsTerminated++
		}
	}
	return res
}

// fullRevoke runs one full_revoke of alice's sessions on the replica.
func fullRevoke(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica) {
	t.Helper()
	users := userstore.NewMemory()
	if err := users.Create(context.Background(), userstore.User{Subject: "alice@acme.com", TenantID: lifecycleharness.Tenant, Roles: []pkgauth.Role{pkgauth.RoleUser}}); err != nil {
		t.Error(err)
		return
	}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: h.Clock.Now}).
		WithUsers(users).WithSessions(h.Store).WithRevokeHook(r.Server).
		WithUserRevocation(localTerminator{r: r}, nil, nil)
	body, _ := json.Marshal(admin.InvalidateUserRequest{TenantID: lifecycleharness.Tenant, Mode: admin.InvalidateFullRevoke})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/users/alice@acme.com/invalidate", bytes.NewReader(body))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "admin@acme.com", TenantID: "platform", Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("full_revoke = %d %s", rr.Code, rr.Body.String())
	}
}

// spec: §11.4 (User Invalidation), §10.1.5 (Stale Replica Behavior), §5.2
// (Recycle lifecycle)
// diagnosis: on a recycling pool with maxConcurrentSessions: 1, full_revoke
// on the handling replica raced that replica's coordination Sweeper releasing
// the cancelled rows, and a binding was released twice (two Shutdowns) or a
// by-name claim delete followed the recycle patch, retiring a pod the release
// had handed to the whole-pod scrub.
func TestFullRevokeRacesTheSweepersTerminalRelease_spec_11_4(t *testing.T) {
	const rounds, perRound = 4, 2
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 1, Pods: rounds * perRound, Recycling: true})
	r := h.Replicas[0]
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for round := 0; round < rounds; round++ {
		var ids, pods []string
		for i := 0; i < perRound; i++ {
			id := h.StartSession(r)
			ids = append(ids, id)
			pods = append(pods, h.Row(id).PodAssignment)
		}
		shutdowns := map[string]int{}
		for _, pod := range pods {
			shutdowns[pod] = h.Cluster.PodOf(pod).UnaryCalls("Shutdown")
		}
		sweeps := 1 + rng.Intn(4)
		raceJittered(rng,
			func() { fullRevoke(t, h, r) },
			func() {
				for i := 0; i < sweeps; i++ {
					_, _ = r.Sweeper.Sweep(context.Background())
					time.Sleep(time.Duration(rng.Intn(500)) * time.Microsecond)
				}
			})
		h.Settle()
		_, _ = r.Sweeper.Sweep(context.Background())
		h.Settle()
		for i, pod := range pods {
			if _, ok := r.Registry.Get(ids[i]); ok {
				t.Fatalf("round %d: %s still binds the revoked session %s", round, r.ID, ids[i])
			}
			phase, exists, err := h.Cluster.ClaimPhase(context.Background(), pod)
			if err != nil {
				t.Fatal(err)
			}
			if !exists || phase != string(claimstate.Recycling) {
				t.Fatalf("round %d: claim of %s = %q (exists %v), want recycling; a by-name delete followed the recycle patch", round, pod, phase, exists)
			}
			if n := h.Cluster.PodOf(pod).UnaryCalls("Shutdown") - shutdowns[pod]; n != 1 {
				t.Fatalf("round %d: %s received %d Shutdowns, want 1", round, pod, n)
			}
		}
	}
}
