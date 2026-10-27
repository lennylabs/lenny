// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
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
	r.srv.Handler().ServeHTTP(rec, req)
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
	return sessionstore.Session{ID: "sess-1", TenantID: "acme", State: state, PodAssignment: pod}
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
// a terminate in a state with no coordinating replica, ready or a suspended
// session whose row names no pod, is written by the serving replica with no
// lease read, although suspended is in the terminate set.
//
// spec: 29.3 (Interactive message send) the terminate row, 10.1.1 (Stateless
// Replicas and Per-Session Coordination)
func TestTerminateWithoutAHolderIsWrittenLocally_spec_29_3(t *testing.T) {
	m := gatewaypki.Mint(t)
	for _, row := range []sessionstore.Session{siteRow(session.StateReady, ""), siteRow(session.StateSuspended, "")} {
		t.Run(string(row.State)+" pod "+row.PodAssignment, func(t *testing.T) {
			r := newSiteRig(t, m, closedAddr(t), row)
			leases := r.srv.leaseStore.(*countingLeaseStore)
			rec := r.do(http.MethodPost, "/v1/sessions/sess-1/terminate", "")
			if rec.Code != http.StatusOK || r.row(t).State != session.StateCompleted {
				t.Fatalf("terminate = %d %s state %q, want 200 and a local completed write", rec.Code, rec.Body.String(), r.row(t).State)
			}
			if n := leases.gets.Load(); n != 0 {
				t.Errorf("lease reads = %d, want none", n)
			}
		})
	}
}
