// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	tenantpg "github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// resumeCapFixture is one gateway session server over compose Postgres
// (sessions and tenants) and Redis (the session DLQ), for tenant acme with
// a concurrent-session quota of one.
type resumeCapFixture struct {
	srv     *sessionserver.Server
	store   *sessionpg.Store
	coord   *sessioninbox.Coordinator
	recheck *sessionserver.BacklogRechecker
}

func newResumeCapFixture(t *testing.T) *resumeCapFixture {
	t.Helper()
	ctx := context.Background()
	pg := sharedDatabase(t)
	rd := containers.StartRedis(t, containers.RedisOptions{})
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce, max_concurrent_sessions) VALUES ('acme', '\x00', 1)`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	store := sessionpg.New(pg.Pool)
	coord := sessioninbox.NewCoordinator(sessioninbox.Config{
		Inbox: sessioninbox.NewRedisInbox(rd.Client, 10),
		DLQ:   sessioninbox.NewDLQ(rd.Client, 10),
	})
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true,
		Tenants:         tenantpg.New(pg.Pool),
		Messaging:       coord,
		Executor:        executor.NewEchoExecutor(),
		// A wired binder gives a suspended row without a pod its podless
		// meaning; no restore runs here, because no watchdog ticks.
		PodBinder:   &podsession.Binder{},
		PodRegistry: podsession.NewRegistry(),
	})
	t.Cleanup(srv.Close)
	return &resumeCapFixture{
		srv: srv, store: store, coord: coord,
		recheck: &sessionserver.BacklogRechecker{
			Server: srv, Lister: store,
			Tenants: func(context.Context) ([]string, error) { return []string{"acme"}, nil },
		},
	}
}

// seedPodlessSuspended writes a podless idle-suspended row.
func (f *resumeCapFixture) seedPodlessSuspended(t *testing.T, id string) {
	t.Helper()
	since := time.Now().UTC().Add(-time.Minute)
	if err := f.store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", UserID: "alice", RuntimeRef: "echo", State: session.StateSuspended,
		SuspendedReason: sessionstore.SuspendedReasonIdle, SuspendedAt: since, CreatedAt: since, UpdatedAt: since,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// complete writes a session terminal, freeing its slot.
func (f *resumeCapFixture) complete(t *testing.T, id string) {
	t.Helper()
	if _, err := f.store.Update(context.Background(), "acme", id, func(r *sessionstore.Session) error {
		r.State = session.StateCompleted
		return nil
	}); err != nil {
		t.Fatalf("complete %s: %v", id, err)
	}
}

// do sends one request as acme and decodes the JSON body.
func (f *resumeCapFixture) do(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	// spec: §10.2 (Session ownership) — the requests run as the rows' owner.
	ownerprincipal.Wrap(f.srv.Handler(), "alice").ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (f *resumeCapFixture) state(t *testing.T, id string) session.State {
	t.Helper()
	row, err := f.store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return row.State
}

func (f *resumeCapFixture) held(t *testing.T, id string) int {
	t.Helper()
	n, err := f.coord.HeldLen(context.Background(), "acme", id, true, time.Time{})
	if err != nil {
		t.Fatalf("held length of %s: %v", id, err)
	}
	return n
}

// assertResumeCapRefusal asserts a 429 QUOTA_EXCEEDED body naming limit 1 and
// one counted session.
func assertResumeCapRefusal(t *testing.T, what string, code int, body map[string]any) {
	t.Helper()
	e, _ := body["error"].(map[string]any)
	details, _ := e["details"].(map[string]any)
	if code != http.StatusTooManyRequests || e["code"] != "QUOTA_EXCEEDED" ||
		details["limit"] != float64(1) || details["active"] != float64(1) {
		t.Fatalf("%s = %d %v, want 429 QUOTA_EXCEEDED with limit 1 and active 1", what, code, body)
	}
}

// spec: 11.1 (Concurrent-session count), 11.1 (Resume admission), 7.2
// (Message delivery routing — seven paths, path 6)
// diagnosis: on the compose Postgres and Redis stores a suspended session
// held a concurrent-session slot, a message or POST /resume to a suspended
// session at a full tenant quota was admitted or was held anyway, a slot
// freed by the other session's end did not admit the resume, or the gateway
// leader's re-check resumed a podless row with a held message while the
// quota was full. Check the counted-session predicate in the pgstore counts
// and migration 0185, and the admission check in sessionserver quota.go.
func TestSuspendedSessionResumeAdmissionOnCompose_spec_11_1(t *testing.T) {
	t.Parallel()
	const (
		idleID    = "11111111-1111-4111-8111-111111111111"
		backlogID = "22222222-2222-4222-8222-222222222222"
	)
	f := newResumeCapFixture(t)
	ctx := context.Background()
	f.seedPodlessSuspended(t, idleID)
	f.seedPodlessSuspended(t, backlogID)
	if _, err := f.coord.EnqueueDLQ(ctx, "acme", backlogID, sessioninbox.Message{
		MessageID: "m-backlog", Payload: []byte(`{"role":"user","content":"x"}`), EnqueuedAt: time.Now().UTC(),
	}, 0); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// The running session fills the quota of one. The two suspended rows
	// hold no slot, so each refusal below counts one active session.
	const other = "33333333-3333-4333-8333-333333333333"
	if err := f.store.Create(ctx, sessionstore.Session{
		ID: other, TenantID: "acme", UserID: "bob", RuntimeRef: "echo", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("seed the running session: %v", err)
	}

	code, body := f.do(t, http.MethodPost, "/v1/sessions/"+idleID+"/messages", `{"messages":[{"role":"user","content":"hi"}]}`)
	assertResumeCapRefusal(t, "message at the quota", code, body)
	code, body = f.do(t, http.MethodPost, "/v1/sessions/"+idleID+"/resume", "")
	assertResumeCapRefusal(t, "POST /resume at the quota", code, body)
	if st, n := f.state(t, idleID), f.held(t, idleID); st != session.StateSuspended || n != 0 {
		t.Fatalf("refused session = %q with %d held, want suspended with nothing held", st, n)
	}
	if n := f.recheck.Pass(ctx); n != 0 || f.state(t, backlogID) != session.StateSuspended {
		t.Fatalf("re-check at the quota resumed %d (row %q), want 0 and suspended", n, f.state(t, backlogID))
	}

	// The other session ends, freeing the slot.
	f.complete(t, other)
	code, body = f.do(t, http.MethodPost, "/v1/sessions/"+idleID+"/messages", `{"messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK {
		t.Fatalf("message after the slot freed = %d %v, want 200", code, body)
	}
	if st, n := f.state(t, idleID), f.held(t, idleID); st != session.StateResumePending || n != 1 {
		t.Fatalf("resumed session = %q with %d held, want resume_pending holding the message", st, n)
	}
	// The resumed session now counts, so the backlog row waits on the next free slot.
	if n := f.recheck.Pass(ctx); n != 0 || f.state(t, backlogID) != session.StateSuspended {
		t.Fatalf("re-check with the quota refilled resumed %d, want 0", n)
	}
	f.complete(t, idleID)
	if n := f.recheck.Pass(ctx); n != 1 || f.state(t, backlogID) != session.StateResumePending {
		t.Fatalf("re-check after the slot freed resumed %d (row %q), want 1 and resume_pending", n, f.state(t, backlogID))
	}
}
