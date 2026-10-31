// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	rlcounter "github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/tests/testinfra/gatewaypki"
)

// sendCountingExecutor counts the sends that reach the local executor.
type sendCountingExecutor struct {
	executor.Executor
	mu    sync.Mutex
	sends int
}

func (e *sendCountingExecutor) Send(ctx context.Context, id string, msgs []executor.Message) (executor.Response, error) {
	e.mu.Lock()
	e.sends++
	e.mu.Unlock()
	return e.Executor.Send(ctx, id, msgs)
}

// siteRig is an off-holder serving replica: sess-1's coordination lease
// names another replica whose forward listener is at a chosen address.
type siteRig struct {
	store *memstore.Store
	srv   *Server
	mr    *miniredis.Miniredis
	exec  *sendCountingExecutor
	waits *inputwait.Registry
	now   time.Time
}

// recoveringWindow is the session's maxResumeWindowSeconds in the rig rows,
// distinct from the deployer default so the hold's TTL discriminates.
const recoveringWindow = 120 * time.Second

func newSiteRig(t *testing.T, m *gatewaypki.Material, coordinatorAddr string, row sessionstore.Session) *siteRig {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "rt-mid", Type: runtimestore.TypeAgent,
		Capabilities: &runtimestore.RuntimeCapabilities{MidSessionUpload: true},
	}); err != nil {
		t.Fatal(err)
	}
	r := &siteRig{
		store: memstore.New(), mr: mr, waits: inputwait.NewRegistry(),
		exec: &sendCountingExecutor{Executor: executor.NewEchoExecutor()},
		now:  time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC),
	}
	row.RetryPolicy = &session.RetryPolicy{MaxResumeWindowSeconds: int(recoveringWindow / time.Second)}
	if err := r.store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r.srv = New(r.store, Options{
		DevTenantHeader:         true,
		CoordinationLeaseStore:  leasesHeldBy("gw-b.00@" + coordinatorAddr),
		ReplicaID:               "gw-a.00@127.0.0.1:1",
		HolderForwardClient:     testForwardClient(t, m, 2*time.Second),
		Executor:                r.exec,
		InputWaits:              r.waits,
		Runtimes:                runtimes,
		MidSessionUploadEnabled: true,
		Clock:                   func() time.Time { return r.now },
		Messaging: sessioninbox.NewCoordinator(sessioninbox.Config{
			Inbox: sessioninbox.NewMemoryInbox(10),
			DLQ:   sessioninbox.NewDLQ(rc, 10),
		}),
	})
	r.srv.podBinder = &podsession.Binder{}
	return r
}

func (r *siteRig) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	asOwner(r.srv.Handler()).ServeHTTP(rec, req)
	return rec
}

func (r *siteRig) row(t *testing.T) sessionstore.Session {
	t.Helper()
	row, err := r.store.Get(context.Background(), "acme", "sess-1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return row
}

// dlqScores returns the scores of the DLQ entries of sess-1.
func (r *siteRig) dlqScores(t *testing.T) []float64 {
	t.Helper()
	key := sessioninbox.DLQKey("acme", "sess-1")
	if !r.mr.Exists(key) {
		return nil
	}
	members, err := r.mr.ZMembers(key)
	if err != nil {
		t.Fatalf("ZMembers: %v", err)
	}
	out := make([]float64, 0, len(members))
	for _, m := range members {
		score, _ := r.mr.ZScore(key, m)
		out = append(out, score)
	}
	return out
}

// assertNoLocalEffect checks the serving replica wrote no state, sent nothing
// to the executor, and held nothing.
func (r *siteRig) assertNoLocalEffect(t *testing.T, want session.State) {
	t.Helper()
	row := r.row(t)
	if row.State != want || !row.TerminatedAt.IsZero() {
		t.Errorf("row = %q (terminated %v), want %q unwritten", row.State, row.TerminatedAt, want)
	}
	if r.exec.sends != 0 {
		t.Errorf("local executor sends = %d, want 0", r.exec.sends)
	}
	if n := len(r.dlqScores(t)); n != 0 {
		t.Errorf("DLQ entries = %d, want 0", n)
	}
}

// siteCase is one forwarded route with the session state it is reached in.
type siteCase struct {
	name, method, path, body string
	row                      sessionstore.Session
}

func siteRow(state session.State, pod string) sessionstore.Session {
	return sessionstore.Session{UserID: testOwner, ID: "sess-1", TenantID: "acme", State: state, PodAssignment: pod}
}

func forwardedSites() []siteCase {
	running := siteRow(session.StateRunning, "pod-1")
	upload := running
	upload.RuntimeRef = "rt-mid"
	return []siteCase{
		{"terminate in running", http.MethodPost, "/v1/sessions/sess-1/terminate", "", running},
		{"terminate in input_required", http.MethodPost, "/v1/sessions/sess-1/terminate", "", siteRow(session.StateInputRequired, "pod-1")},
		{"terminate in resuming", http.MethodPost, "/v1/sessions/sess-1/terminate", "", siteRow(session.StateResuming, "pod-1")},
		{"terminate in held-pod suspended", http.MethodPost, "/v1/sessions/sess-1/terminate", "", siteRow(session.StateSuspended, "pod-1")},
		{"delete in running", http.MethodDelete, "/v1/sessions/sess-1", "", running},
		{"delete in input_required", http.MethodDelete, "/v1/sessions/sess-1", "", siteRow(session.StateInputRequired, "pod-1")},
		{"delete in resuming", http.MethodDelete, "/v1/sessions/sess-1", "", siteRow(session.StateResuming, "pod-1")},
		{"delete in held-pod suspended", http.MethodDelete, "/v1/sessions/sess-1", "", siteRow(session.StateSuspended, "pod-1")},
		{"terminate in starting", http.MethodPost, "/v1/sessions/sess-1/terminate", "", siteRow(session.StateStarting, "pod-1")},
		{"terminate in resume_pending", http.MethodPost, "/v1/sessions/sess-1/terminate", "", siteRow(session.StateResumePending, "")},
		{"terminate in awaiting_client_action", http.MethodPost, "/v1/sessions/sess-1/terminate", "", siteRow(session.StateAwaitingClientAction, "")},
		{"interrupt in running", http.MethodPost, "/v1/sessions/sess-1/interrupt", "", running},
		{"upload-to-session in running", http.MethodPost, "/v1/sessions/sess-1/upload-to-session", `{"files":[{"path":"a.txt","content":"YQ=="}]}`, upload},
		{"held-pod resume", http.MethodPost, "/v1/sessions/sess-1/resume", "", siteRow(session.StateSuspended, "pod-1")},
		{"message in running", http.MethodPost, "/v1/sessions/sess-1/messages", `{"messages":[{"role":"user","content":"hi"}]}`, running},
		{"message in input_required", http.MethodPost, "/v1/sessions/sess-1/messages", `{"messages":[{"role":"user","content":"hi"}]}`, siteRow(session.StateInputRequired, "pod-1")},
		{"inReplyTo reply in input_required", http.MethodPost, "/v1/sessions/sess-1/messages", `{"messages":[{"role":"user","content":"yes","inReplyTo":"req-1"}]}`, siteRow(session.StateInputRequired, "pod-1")},
	}
}

// TestOffHolderRoutesForwardBeforeAnyLocalEffect_spec_29_3 pins every wired
// REST site: off the holder, each forwarded route in each gated state reaches
// the coordinator with the client's method, path, and body unchanged, and
// answers the coordinator's response. The serving replica writes no state,
// sends nothing to the pod, and holds nothing. Before the forward was wired
// these routes wrote the terminal state on the serving replica, answered 409
// on a held-pod resume, or delivered to the serving replica's executor.
//
// spec: 29.3 (Interactive message send) Forwarding rules 1 and 2, 15.1 (REST
// API), 7.2 (Interactive Session Model)
func TestOffHolderRoutesForwardBeforeAnyLocalEffect_spec_29_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	for _, tc := range forwardedSites() {
		t.Run(tc.name, func(t *testing.T) {
			got := make(chan capturedRequest, 1)
			addr := startTLSServer(t, serverTLSFor(t, m, gatewayLeaf(m)), recordingCoordinator(got))
			r := newSiteRig(t, m, addr, tc.row)
			if _, err := r.waits.Register("sess-1", "req-1", nil); err != nil {
				t.Fatalf("register input wait: %v", err)
			}
			rec := r.do(tc.method, tc.path, tc.body)
			if rec.Code != http.StatusCreated || rec.Header().Get("X-Coordinator") != "b" {
				t.Fatalf("response = %d %s, want the coordinator's 201", rec.Code, rec.Body.String())
			}
			req := <-got
			if req.method != tc.method || req.uri != tc.path || req.body != tc.body {
				t.Errorf("forwarded %s %s %q, want %s %s %q", req.method, req.uri, req.body, tc.method, tc.path, tc.body)
			}
			r.assertNoLocalEffect(t, tc.row.State)
			if !r.waits.Pending("sess-1", "req-1") {
				t.Error("the serving replica resolved the pending input wait; the forward must run before the inReplyTo resolution")
			}
		})
	}
}

// TestOffHolderInterruptInInputRequiredIsRefusedWithoutForward_spec_15_1 pins
// the §15.1 interrupt row on the off-holder path: the row admits running
// alone, so an interrupt of a session waiting in input_required is refused on
// the serving replica with 409 INVALID_STATE_TRANSITION before any forward,
// and the serving replica writes nothing and holds nothing. Before the
// interrupt row dropped input_required, the serving replica forwarded this
// interrupt to the coordinator.
//
// spec: 15.1 (REST API), 7.2 (Session state machine), 29.3 (Interactive
// message send)
func TestOffHolderInterruptInInputRequiredIsRefusedWithoutForward_spec_15_1(t *testing.T) {
	m := gatewaypki.Mint(t)
	got := make(chan capturedRequest, 1)
	addr := startTLSServer(t, serverTLSFor(t, m, gatewayLeaf(m)), recordingCoordinator(got))
	row := siteRow(session.StateInputRequired, "pod-1")
	r := newSiteRig(t, m, addr, row)
	rec := r.do(http.MethodPost, "/v1/sessions/sess-1/interrupt", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "INVALID_STATE_TRANSITION") {
		t.Fatalf("response = %d %s, want 409 INVALID_STATE_TRANSITION", rec.Code, rec.Body.String())
	}
	select {
	case req := <-got:
		t.Errorf("the serving replica forwarded %s %s; the precondition must refuse before any forward", req.method, req.uri)
	default:
	}
	r.assertNoLocalEffect(t, row.State)
}

// TestUndeliveredOffHolderRoutesFailClosed_spec_29_3 pins rules 6 and 7 on
// every wired site other than a message carrying no inReplyTo: with the
// coordinator unreachable, and with a forward whose connection fails after it
// is established, the route answers 409 TARGET_NOT_READY carrying
// currentState and the serving replica writes nothing and holds nothing.
//
// spec: 29.3 (Interactive message send) Forwarding rules 6 and 7
func TestUndeliveredOffHolderRoutesFailClosed_spec_29_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	failures := map[string]func(t *testing.T) string{
		"coordinator unreachable": closedAddr,
		"connection fails after it is established": func(t *testing.T) string {
			return startTLSServer(t, serverTLSFor(t, m, gatewayLeaf(m)), closingCoordinator())
		},
	}
	for fname, addrFor := range failures {
		for _, tc := range forwardedSites() {
			if tc.path == "/v1/sessions/sess-1/messages" && !strings.Contains(tc.body, "inReplyTo") && fname == "coordinator unreachable" {
				continue
			}
			t.Run(fname+"/"+tc.name, func(t *testing.T) {
				r := newSiteRig(t, m, addrFor(t), tc.row)
				rec := r.do(tc.method, tc.path, tc.body)
				assertTargetNotReady(t, rec, string(tc.row.State))
				r.assertNoLocalEffect(t, tc.row.State)
			})
		}
	}
}

// TestUndeliveredMessageIsHeldWithTheRecoveringTTL_spec_29_3 pins rule 6 on
// the REST message site: a batch none of whose messages carries inReplyTo,
// whose forward is not delivered, is held in the session's DLQ with the TTL
// of the dead-letter table's recovering-state row and answered queued; a
// batch in which one message of several carries inReplyTo fails closed and
// holds nothing.
//
// spec: 29.3 (Interactive message send) Forwarding rule 6, 7.2 (Interactive
// Session Model) dead-letter handling
func TestUndeliveredMessageIsHeldWithTheRecoveringTTL_spec_29_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	for _, state := range []session.State{session.StateRunning, session.StateInputRequired} {
		t.Run(string(state), func(t *testing.T) {
			r := newSiteRig(t, m, closedAddr(t), siteRow(state, "pod-1"))
			rec := r.do(http.MethodPost, "/v1/sessions/sess-1/messages", `{"messages":[{"role":"user","content":"a"},{"role":"user","content":"b"}]}`)
			var resp MessageResponse
			_ = json.Unmarshal(rec.Body.Bytes(), &resp)
			if rec.Code != http.StatusOK || resp.DeliveryReceipt.Status != session.DeliveryStatusQueued {
				t.Fatalf("response = %d %s, want 200 queued", rec.Code, rec.Body.String())
			}
			scores := r.dlqScores(t)
			want := float64(r.now.Add(recoveringWindow).UnixMilli())
			if len(scores) != 2 {
				t.Fatalf("DLQ entries = %d, want 2", len(scores))
			}
			for _, s := range scores {
				if s < want || s > want+1 {
					t.Errorf("DLQ entry expires at %v, want the recovering-row TTL expiry %v", s, want)
				}
			}
			if r.exec.sends != 0 || r.row(t).State != state {
				t.Errorf("sends %d state %q, want no local delivery or write", r.exec.sends, r.row(t).State)
			}
		})
	}
	t.Run("batch with one inReplyTo", func(t *testing.T) {
		r := newSiteRig(t, m, closedAddr(t), siteRow(session.StateInputRequired, "pod-1"))
		rec := r.do(http.MethodPost, "/v1/sessions/sess-1/messages", `{"messages":[{"role":"user","content":"a"},{"role":"user","content":"b","inReplyTo":"req-9"}]}`)
		assertTargetNotReady(t, rec, string(session.StateInputRequired))
		r.assertNoLocalEffect(t, session.StateInputRequired)
	})
}

// TestTerminateWithoutAHolderIsWrittenLocally_spec_29_3 pins the state gate:
// a terminate or a delete in a state with no coordinating replica, created,
// finalizing, ready, or a suspended session whose row names no pod, is
// written by the serving replica with no lease read and no 409, although
// suspended is in the terminate set.
//
// spec: 29.3 (Interactive message send) the terminate row, 10.1.1 (Stateless
// Replicas and Per-Session Coordination), 15.1 (REST API)
func TestTerminateWithoutAHolderIsWrittenLocally_spec_29_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	rows := []sessionstore.Session{
		siteRow(session.StateCreated, ""), siteRow(session.StateFinalizing, ""),
		siteRow(session.StateReady, ""), siteRow(session.StateSuspended, ""),
	}
	routes := []struct{ name, method, path string }{
		{"terminate", http.MethodPost, "/v1/sessions/sess-1/terminate"},
		{"delete", http.MethodDelete, "/v1/sessions/sess-1"},
	}
	for _, row := range rows {
		for _, route := range routes {
			t.Run(route.name+" in "+string(row.State), func(t *testing.T) {
				r := newSiteRig(t, m, closedAddr(t), row)
				leases := r.srv.leaseStore.(*countingLeaseStore)
				rec := r.do(route.method, route.path, "")
				if rec.Code >= http.StatusBadRequest || !session.IsTerminal(r.row(t).State) {
					t.Fatalf("%s = %d %s state %q, want a local terminal write", route.name, rec.Code, rec.Body.String(), r.row(t).State)
				}
				if n := leases.gets.Load(); n != 0 {
					t.Errorf("lease reads = %d, want none", n)
				}
			})
		}
	}
}

// completingLeaseStore completes sess-1's row when the forward reads the
// lease, so the session ends between the forward decision and the hold's
// re-read, as a terminal transition racing the enqueue does.
type completingLeaseStore struct {
	*countingLeaseStore
	store *memstore.Store
}

func (c *completingLeaseStore) Get(ctx context.Context, tenantID, sessionID string) (leasestore.Lease, error) {
	_, _ = c.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		from := row.State
		row.State = session.StateCompleted
		sessionstore.MarkTerminated(row, from, time.Now())
		return nil
	})
	return c.countingLeaseStore.Get(ctx, tenantID, sessionID)
}

// decodeReceipt decodes a message answer's delivery receipt.
func decodeReceipt(t *testing.T, rec *httptest.ResponseRecorder) session.DeliveryReceipt {
	t.Helper()
	var resp MessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %d %s: %v", rec.Code, rec.Body.String(), err)
	}
	return resp.DeliveryReceipt
}

// TestUndeliveredMessageHoldAnswersEachDLQOutcome_spec_29_3 pins the receipts
// of the rule-6 hold on the REST message site beyond queued: an enqueue that
// evicts an older entry under the DLQ cap answers dropped with dlq_overflow,
// a DLQ write error answers error with inbox_unavailable and holds nothing,
// and an enqueue racing a terminal transition removes the entry it enqueued
// and answers TARGET_TERMINAL. In every case the serving replica sends
// nothing to the pod.
//
// spec: 29.3 (Interactive message send) Forwarding rule 6, 7.2 (Message
// delivery routing — seven paths, path 6) Enqueue racing a terminal
// transition, dead-letter handling
func TestUndeliveredMessageHoldAnswersEachDLQOutcome_spec_29_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	const body = `{"messages":[{"role":"user","content":"late"}]}`
	t.Run("overflow", func(t *testing.T) {
		r := newSiteRig(t, m, closedAddr(t), siteRow(session.StateInputRequired, "pod-1"))
		for i := 0; i < 10; i++ {
			msg := sessioninbox.Message{MessageID: fmt.Sprintf("msg_old%d", i), Payload: []byte(`{}`), EnqueuedAt: r.now.Add(-time.Minute)}
			if _, _, err := r.srv.HoldMessage(context.Background(), "acme", "sess-1", msg); err != nil {
				t.Fatalf("fill the DLQ: %v", err)
			}
		}
		rec := r.do(http.MethodPost, "/v1/sessions/sess-1/messages", body)
		got := decodeReceipt(t, rec)
		if got.Status != session.DeliveryStatusDropped || got.Reason != session.DeliveryReasonDLQOverflow {
			t.Errorf("receipt = %s/%s, want dropped/dlq_overflow", got.Status, got.Reason)
		}
		if n := len(r.dlqScores(t)); n != 10 {
			t.Errorf("DLQ entries = %d, want the cap of 10 with the new message held", n)
		}
		if r.exec.sends != 0 {
			t.Errorf("local sends = %d, want 0", r.exec.sends)
		}
	})
	t.Run("DLQ write error", func(t *testing.T) {
		r := newSiteRig(t, m, closedAddr(t), siteRow(session.StateRunning, "pod-1"))
		r.mr.Close()
		rec := r.do(http.MethodPost, "/v1/sessions/sess-1/messages", body)
		got := decodeReceipt(t, rec)
		if got.Status != session.DeliveryStatusError || got.Reason != session.DeliveryReasonInboxUnavailable {
			t.Errorf("receipt = %s/%s, want error/inbox_unavailable", got.Status, got.Reason)
		}
		if r.exec.sends != 0 || r.row(t).State != session.StateRunning {
			t.Errorf("sends %d state %q, want no local delivery or write", r.exec.sends, r.row(t).State)
		}
	})
	t.Run("enqueue racing a terminal transition", func(t *testing.T) {
		r := newSiteRig(t, m, closedAddr(t), siteRow(session.StateRunning, "pod-1"))
		r.srv.leaseStore = &completingLeaseStore{countingLeaseStore: r.srv.leaseStore.(*countingLeaseStore), store: r.store}
		rec := r.do(http.MethodPost, "/v1/sessions/sess-1/messages", body)
		if rec.Code != http.StatusConflict || responseCode(t, rec) != "TARGET_TERMINAL" {
			t.Fatalf("response = %d %s, want 409 TARGET_TERMINAL", rec.Code, rec.Body.String())
		}
		if n := len(r.dlqScores(t)); n != 0 {
			t.Errorf("DLQ entries = %d after the terminal re-read, want the enqueued entry removed", n)
		}
		if r.exec.sends != 0 {
			t.Errorf("local sends = %d, want 0", r.exec.sends)
		}
	})
}

// idempotentSite wraps the rig's session handler in the auth and §11.5
// idempotency middleware, as the public REST listener serves it.
func idempotentSite(r *siteRig) (http.Handler, *idemmw.MemoryStore) {
	store := idemmw.NewMemoryStore()
	dev := authmw.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true}
	return authmw.Wrap(idemmw.Wrap(asOwner(r.srv.Handler()), store, idemmw.Options{}), dev), store
}

// keyedResume posts a held-pod resume with an Idempotency-Key through h.
func keyedResume(h http.Handler, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess-1/resume", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("X-Lenny-User-ID", testOwner)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestWiredResumeForwardRefusalsStayOutOfTheIdempotencyCache_spec_11_5 runs
// the idempotency cases of the forward helper against the wired held-pod
// resume route: a keyed resume whose forward is not delivered, has an
// unknown outcome, or reaches a replica that no longer coordinates the
// session answers TARGET_NOT_READY, stores no idempotency row, and a
// same-key retry reaches the forward again with no internal mark on the
// client response. A delivered coordinator 409 without the mark is stored
// and replayed.
//
// spec: 11.5 (Idempotency) Mechanism, 29.3 (Interactive message send)
// Forwarding rules 5, 6, and 7
func TestWiredResumeForwardRefusalsStayOutOfTheIdempotencyCache_spec_11_5(t *testing.T) {
	m := gatewaypki.Mint(t)
	row := siteRow(session.StateSuspended, "pod-1")
	targets := []struct {
		name string
		addr func(t *testing.T) string
	}{
		{"not delivered", closedAddr},
		{"unknown outcome", func(t *testing.T) string {
			return startTLSServer(t, serverTLSFor(t, m, gatewayLeaf(m)), closingCoordinator())
		}},
		{"refused by a non-holder", func(t *testing.T) string { return nonHolderCoordinator(t, m, row) }},
	}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			r := newSiteRig(t, m, target.addr(t), row)
			h, store := idempotentSite(r)
			leases := r.srv.leaseStore.(*countingLeaseStore)
			for i := 0; i < 2; i++ {
				rec := keyedResume(h, "resume-key")
				if code := responseCode(t, rec); code != "TARGET_NOT_READY" {
					t.Fatalf("attempt %d answered %d %s, want TARGET_NOT_READY", i, rec.Code, rec.Body.String())
				}
				if rec.Header().Get(forwardNoStoreHeader) != "" {
					t.Errorf("attempt %d carries the internal no-store mark", i)
				}
			}
			if n := leases.gets.Load(); n != 2 {
				t.Errorf("forward reached %d times, want 2: a same-key retry after a refusal runs again", n)
			}
			if _, ok, _ := store.Get(context.Background(), "acme", "resume-key"); ok {
				t.Error("the refusal was stored in the idempotency cache")
			}
			r.assertNoLocalEffect(t, session.StateSuspended)
		})
	}
	t.Run("a delivered coordinator 409 without the mark is stored", func(t *testing.T) {
		var reached atomic.Int64
		conflict := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reached.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"TARGET_NOT_READY","message":"not suspended","details":{"currentState":"running"}}}`))
		})
		listener := NewHolderForwardHandler(conflict, authmw.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true}, callingSessions{})
		r := newSiteRig(t, m, startTLSServer(t, serverTLSFor(t, m, gatewayLeaf(m)), listener), row)
		h, store := idempotentSite(r)
		for i := 0; i < 2; i++ {
			if rec := keyedResume(h, "delivered-409"); rec.Code != http.StatusConflict {
				t.Fatalf("attempt %d answered %d, want the coordinator's 409", i, rec.Code)
			}
		}
		if n := reached.Load(); n != 1 {
			t.Errorf("coordinator reached %d times, want 1: its own refusal is stored and replayed", n)
		}
		if _, ok, _ := store.Get(context.Background(), "acme", "delivered-409"); !ok {
			t.Error("the coordinator's own 409 was not stored")
		}
	})
}

// TestForwardedCreateAndStartKeepsTheAdmissionRateLimit_spec_29_3 pins that
// a session create made while serving a forwarded request is still admitted
// under the §11.1 per-runtime request-rate limit. Rule 4 exempts the
// forwarded request itself from §11.1, but an Open Responses turn that goes
// cold on the coordinator creates a new session, and that create is admitted
// as an unmarked one is.
//
// spec: 29.3 (Interactive message send) Forwarding rule 4, 11.1 (Admission and Fairness), 15 (Open Responses conversation sessions)
func TestForwardedCreateAndStartKeepsTheAdmissionRateLimit_spec_29_3(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name string
		ctx  context.Context
	}{
		{"unmarked", context.Background()},
		{"forwarded", WithForwarded(context.Background())},
	} {
		t.Run(c.name, func(t *testing.T) {
			counter := rlcounter.NewMemory()
			if _, err := counter.Incr(context.Background(), "rt:acme:echo", now); err != nil {
				t.Fatal(err)
			}
			s := New(memstore.New(), Options{
				AdmissionRateLimitCounter: counter,
				PerRuntimePerMinute:       1,
				Clock:                     func() time.Time { return now },
			})
			_, svcErr := s.CreateAndStartService(ownerCtx(c.ctx), "acme", CreateSessionRequest{RuntimeRef: "echo"})
			if svcErr == nil || svcErr.Code != "RATE_LIMITED" || svcErr.HTTPStatus != http.StatusTooManyRequests {
				t.Fatalf("CreateAndStartService = %+v, want 429 RATE_LIMITED once the per-runtime limit is exhausted", svcErr)
			}
		})
	}
}
