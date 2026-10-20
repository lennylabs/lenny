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
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
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

// heldStart is a /start held between its binding registration and its
// running commit.
type heldStart struct {
	release func()
	answer  chan int
}

// startHeldBeforeCommit finalizes a created session and starts it on r with
// its running commit held, and returns once the start registered its
// binding. The session's pre-revoke state stays ready while r binds it.
func startHeldBeforeCommit(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) *heldStart {
	t.Helper()
	if code, body := h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/finalize", nil); code != http.StatusOK {
		t.Fatalf("finalize %s = %d %v", id, code, body)
	}
	held, gate := make(chan struct{}), make(chan struct{})
	h.Trace.ArmMatching("Update", "commitStart", lifecycleharness.FaultHold, func(lifecycleharness.TraceEntry, lifecycleharness.FaultKind) {
		close(held)
		<-gate
	})
	hs := &heldStart{release: sync.OnceFunc(func() { close(gate) }), answer: make(chan int, 1)}
	go func() {
		code, _ := h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/start", nil)
		hs.answer <- code
	}()
	select {
	case <-held:
	case code := <-hs.answer:
		t.Fatalf("start of %s answered %d before its running commit", id, code)
	}
	if _, ok := r.Registry.Get(id); !ok {
		t.Fatalf("the held start of %s registered no binding", id)
	}
	if st := h.Row(id).State; st != session.StateReady {
		t.Fatalf("held start's session = %s, want ready", st)
	}
	return hs
}

// revokeRound is one round's sessions: running sessions, a created session
// with no binding, and a ready session whose start is held.
type revokeRound struct {
	running []string
	created string
	held    string
	pods    map[string]string
	before  map[string]int
}

// shutdownsSince returns the Shutdowns the session's pod received since
// the round began.
func (rr *revokeRound) shutdownsSince(h *lifecycleharness.Harness, id string) int {
	return h.Cluster.PodOf(rr.pods[id]).UnaryCalls("Shutdown") - rr.before[id]
}

// spec: §11.4 (User Invalidation), §10.1.5 (Stale Replica Behavior), §5.2
// (Recycle lifecycle), §4.6.3 (SandboxClaim: a release that ends the
// occupancy without reuse)
// diagnosis: on a recycling pool with maxConcurrentSessions: 1, full_revoke
// on the handling replica raced that replica's coordination Sweeper releasing
// the cancelled rows, and a binding was released twice (two Shutdowns), a
// by-name claim delete followed the recycle patch (the pre-running reclaim
// reached a session the replica binds between its binding registration and
// its running commit, retiring a pod the release had handed to the whole-pod
// scrub), or a created session with no binding kept its claim.
func TestFullRevokeRacesTheSweepersTerminalRelease_spec_11_4(t *testing.T) {
	const rounds, perRound = 4, 2
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 1, Pods: rounds * (perRound + 2), Recycling: true})
	r := h.Replicas[0]
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	ctx := context.Background()
	for round := 0; round < rounds; round++ {
		rr := &revokeRound{pods: map[string]string{}, before: map[string]int{}}
		for i := 0; i < perRound; i++ {
			rr.running = append(rr.running, h.StartSession(r))
		}
		rr.created, rr.held = h.CreateSession(r), h.CreateSession(r)
		for _, id := range append([]string{rr.created, rr.held}, rr.running...) {
			rr.pods[id] = h.Row(id).PodAssignment
			rr.before[id] = h.Cluster.PodOf(rr.pods[id]).UnaryCalls("Shutdown")
		}
		hs := startHeldBeforeCommit(t, h, r, rr.held)
		// A start the race never frees would hang the round; free it after
		// a bound so the assertions below report the failure instead.
		guard := time.AfterFunc(30*time.Second, hs.release)
		sweeps := 1 + rng.Intn(4)
		raceJittered(rng,
			func() { fullRevoke(t, h, r) },
			func() {
				for i := 0; i < sweeps; i++ {
					_, _ = r.Sweeper.Sweep(ctx)
					time.Sleep(time.Duration(rng.Intn(500)) * time.Microsecond)
				}
			})
		hs.release()
		guard.Stop()
		if code := <-hs.answer; code != http.StatusConflict {
			t.Fatalf("round %d: the held start answered %d after the revoke, want 409", round, code)
		}
		h.Settle()
		_, _ = r.Sweeper.Sweep(ctx)
		h.Settle()
		requireRevokeRoundReleased(t, h, r, round, rr)
	}
}

// requireRevokeRoundReleased asserts the round's release outcomes: every
// bound session's binding is gone, its pod received one Shutdown, and its
// claim is recycling, so no by-name delete followed the recycle patch; the
// created session's claim was deleted by name with no Shutdown.
func requireRevokeRoundReleased(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, round int, rr *revokeRound) {
	t.Helper()
	ctx := context.Background()
	for _, id := range append([]string{rr.held}, rr.running...) {
		if st := h.Row(id).State; st != session.StateCancelled {
			t.Fatalf("round %d: revoked session %s = %s, want cancelled", round, id, st)
		}
		if _, ok := r.Registry.Get(id); ok {
			t.Fatalf("round %d: %s still binds the revoked session %s", round, r.ID, id)
		}
		phase, exists, err := h.Cluster.ClaimPhase(ctx, rr.pods[id])
		if err != nil {
			t.Fatal(err)
		}
		if !exists || phase != string(claimstate.Recycling) {
			t.Fatalf("round %d: claim of %s (session %s) = %q (exists %v), want recycling; a by-name delete followed the recycle patch", round, rr.pods[id], id, phase, exists)
		}
		if n := rr.shutdownsSince(h, id); n != 1 {
			t.Fatalf("round %d: %s (session %s) received %d Shutdowns, want 1", round, rr.pods[id], id, n)
		}
	}
	if st := h.Row(rr.created).State; st != session.StateCancelled {
		t.Fatalf("round %d: revoked created session = %s, want cancelled", round, st)
	}
	if phase, exists, err := h.Cluster.ClaimPhase(ctx, rr.pods[rr.created]); err != nil || exists {
		t.Fatalf("round %d: claim of the created session's pod %s = %q (exists %v, err %v), want it deleted by the pre-running reclaim", round, rr.pods[rr.created], phase, exists, err)
	}
	if n := rr.shutdownsSince(h, rr.created); n != 0 {
		t.Fatalf("round %d: the created session's pod received %d Shutdowns, want none", round, n)
	}
}
