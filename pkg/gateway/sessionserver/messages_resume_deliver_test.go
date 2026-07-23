// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/codes"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// spec: §7.2 path 6 (lines 326-330) — a `delivery: "immediate"` message to a
// suspended, pod-held session atomically resumes and delivers; on a resume or a
// post-resume delivery failure the coordinator fails closed to inbox buffering
// (`queued`) so line 330's "the message is not silently dropped" holds.
//
// These are the handler-level fail-closed paths the resumeHeldPod primitive
// tests (resume_held_pod_test.go) cannot reach: they drive the whole
// deliverMessageBatch switch through the real POST /messages handler, exercising
// the ActionResumeAndDeliver case's two queued fallbacks (a post-resume
// executor.Send failure, and a resume failure) end to end.

// sendErrorExecutor is a fake executor whose Send always fails, modelling a
// runtime that rejected the prompt after the session resumed. Its Close is a
// no-op. It lets a component test drive the §7.2 path-6 post-resume delivery
// failure the always-ready echo executor cannot produce.
type sendErrorExecutor struct {
	err   error
	sends int
}

func (e *sendErrorExecutor) Send(_ context.Context, _ string, _ []executor.Message) (executor.Response, error) {
	e.sends++
	return executor.Response{}, e.err
}

func (e *sendErrorExecutor) Close(context.Context, string) error { return nil }

// updateFailStore wraps a real store and forces every Update to fail, modelling
// a store fault (or a lost terminal-transition race) on the resumeHeldPod
// suspended → running write. Get and the other reads delegate to the embedded
// store, so loadMessageTarget still observes the seeded suspended row and the
// router still selects ActionResumeAndDeliver; only the resume write fails.
type updateFailStore struct {
	sessionstore.Store
	err error
}

func (s *updateFailStore) Update(context.Context, string, string, func(*sessionstore.Session) error) (sessionstore.Session, error) {
	return sessionstore.Session{}, s.err
}

// inboxServer wires a gateway with the given executor and store plus a real
// §7.2 inbox coordinator (miniredis-backed DLQ, memory inbox) so the buffered
// `queued` fallbacks enqueue against real machinery and the test can assert the
// inbox depth. It returns the server and the inbox so the caller can read depth.
func inboxServer(t *testing.T, store sessionstore.Store, exec executor.Executor) (*sessionserver.Server, *sessioninbox.MemoryInbox) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	inbox := sessioninbox.NewMemoryInbox(10)
	coord := sessioninbox.NewCoordinator(sessioninbox.Config{
		Inbox: inbox,
		DLQ:   sessioninbox.NewDLQ(rc, 10),
	})
	srv := sessionserver.New(store, sessionserver.Options{
		Executor:    exec,
		Transcripts: transcriptstore.NewMemory(),
		Messaging:   coord,
	})
	return srv, inbox
}

func seedSuspendedSession(t *testing.T, store sessionstore.Store, id, pod string) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", UserID: "alice", RuntimeRef: "echo",
		State: session.StateSuspended, PodAssignment: pod,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed suspended %s: %v", id, err)
	}
}

func immediateMessage(text string) sessionserver.MessageRequest {
	return sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{
			Role:     "user",
			Content:  sessionrecord.MessageContentFromText(text),
			Delivery: "immediate",
		}},
	}
}

// TestResumeAndDeliverPostResumeSendFailureBuffersQueuedSessionRunning pins the
// §7.2 path-6 post-resume delivery fail-closed path: a `delivery: "immediate"`
// message to a suspended session resumes the session (suspended → running), and
// when executor.Send then fails the handler buffers the message to the inbox
// with a `queued` receipt and leaves the session running (its inbox drains on
// the next ready_for_input) rather than dropping the message or returning a 500.
//
// spec: 7.2 (path 6 line 330 — the message is not silently dropped)
//
// diagnosis: a failure means the coordinator did not fail closed after a
// committed resume — a post-resume executor.Send error dropped the message,
// returned a 5xx, or rolled the session back off running instead of buffering
// it to a `queued` inbox for FIFO redelivery.
func TestResumeAndDeliverPostResumeSendFailureBuffersQueuedSessionRunning(t *testing.T) {
	store := memstore.New()
	seedSuspendedSession(t, store, "s-send-fail", "pod-1")
	srv, inbox := inboxServer(t, store, &sendErrorExecutor{err: errors.New("runtime rejected prompt")})

	rr := sendMessageRequest(t, srv.Handler(), "s-send-fail", immediateMessage("wake up"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s (a post-resume delivery failure must fail closed to a 200 queued receipt, not a 5xx)", rr.Code, rr.Body.String())
	}
	var resp sessionserver.MessageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DeliveryReceipt.Status != session.DeliveryStatusQueued {
		t.Errorf("status = %q, want queued (post-resume Send failure fails closed to the inbox)", resp.DeliveryReceipt.Status)
	}
	// The resume committed, so the session is running; a running session's
	// inbox drains on the next ready_for_input.
	row, _ := store.Get(context.Background(), "acme", "s-send-fail")
	if row.State != session.StateRunning {
		t.Errorf("state = %q, want running (the resume committed before the delivery failure)", row.State)
	}
	if n, _ := inbox.Len(context.Background(), "acme", "s-send-fail"); n != 1 {
		t.Errorf("inbox depth = %d, want 1 (the message must be buffered, not dropped)", n)
	}
}

// TestResumeAndDeliverResumeFailureBuffersQueuedRowUnchanged pins the §7.2
// path-6 resume fail-closed path: when the resumeHeldPod suspended → running
// write fails (a store fault or a lost terminal-transition race), the handler
// leaves the row unchanged and buffers the message to the inbox with a `queued`
// receipt rather than dropping it. The router selected ActionResumeAndDeliver
// because the row read suspended; the write failure then routes to the fallback.
//
// spec: 7.2 (path 6 line 330 — the message is not silently dropped)
//
// diagnosis: a failure means a resumeHeldPod write error did not fail closed —
// the handler dropped the message, returned a 5xx, or transitioned the row
// despite the failed resume instead of buffering to a `queued` inbox and leaving
// the session suspended.
func TestResumeAndDeliverResumeFailureBuffersQueuedRowUnchanged(t *testing.T) {
	inner := memstore.New()
	seedSuspendedSession(t, inner, "s-resume-fail", "pod-2")
	store := &updateFailStore{Store: inner, err: errors.New("store update failed")}
	// The echo executor must never be reached: the resume failed before delivery.
	srv, inbox := inboxServer(t, store, executor.NewEchoExecutor())

	rr := sendMessageRequest(t, srv.Handler(), "s-resume-fail", immediateMessage("wake up"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s (a resume failure must fail closed to a 200 queued receipt, not a 5xx)", rr.Code, rr.Body.String())
	}
	var resp sessionserver.MessageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DeliveryReceipt.Status != session.DeliveryStatusQueued {
		t.Errorf("status = %q, want queued (a failed resume fails closed to the inbox)", resp.DeliveryReceipt.Status)
	}
	// The resume write failed, so the row is untouched: still suspended.
	row, _ := inner.Get(context.Background(), "acme", "s-resume-fail")
	if row.State != session.StateSuspended {
		t.Errorf("state = %q, want suspended unchanged (the resume write failed, so no transition)", row.State)
	}
	if n, _ := inbox.Len(context.Background(), "acme", "s-resume-fail"); n != 1 {
		t.Errorf("inbox depth = %d, want 1 (the message must be buffered, not dropped)", n)
	}
}

// TestResumeAndDeliverResumeFailureRecordsTransientCategory pins the §16.3
// error-taxonomy bucket the coordinator stamps on the session.prompt span when
// resumeHeldPod fails. The taxonomy defines only TRANSIENT, PERMANENT, POLICY,
// and UPSTREAM; a resume fault is a store-write failure (or a lost
// terminal-transition race the suspended-guard rejects) that the `queued`
// fallback recovers from, so it is TRANSIENT, matching the create-path
// convention for a store-write failure. This locks the choice so the category
// cannot silently drift to UPSTREAM (the post-resume executor.Send bucket) or a
// non-existent constant.
//
// spec: §16.3 (error taxonomy — TRANSIENT for a recoverable store-write fault),
// 7.2 (path 6 line 330 — the message is not silently dropped)
//
// diagnosis: a failure means the resume-fault span was tagged with the wrong
// §16.3 category — an UPSTREAM/pod fault or a non-taxonomy value — instead of
// TRANSIENT, so operators would misclassify a recoverable coordinator-internal
// resume fault as an external dependency failure.
func TestResumeAndDeliverResumeFailureRecordsTransientCategory(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	inner := memstore.New()
	seedSuspendedSession(t, inner, "s-resume-cat", "pod-4")
	store := &updateFailStore{Store: inner, err: errors.New("store update failed")}
	srv, _ := inboxServer(t, store, executor.NewEchoExecutor())

	rr := sendMessageRequest(t, srv.Handler(), "s-resume-cat", immediateMessage("wake up"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s (a resume failure must fail closed to a 200 queued receipt)", rr.Code, rr.Body.String())
	}

	span := findSpan(rec.Ended(), "session.prompt")
	if span == nil {
		t.Fatalf("session.prompt span not recorded on the resume-failure path")
	}
	if span.Status().Code != codes.Error {
		t.Errorf("span status = %v, want codes.Error on a resume failure", span.Status().Code)
	}
	var got string
	for _, kv := range span.Attributes() {
		if string(kv.Key) == tracing.AttrErrorCategory {
			got = kv.Value.AsString()
		}
	}
	if got != string(tracing.CategoryTransient) {
		t.Errorf("error.category = %q, want %q (a recoverable coordinator-internal resume fault is TRANSIENT)", got, tracing.CategoryTransient)
	}
}

// TestResumeAndDeliverHappyPathDeliversAndRuns is the handler-level happy path:
// a `delivery: "immediate"` message to a suspended session resumes it
// (suspended → running) and delivers through the echo executor, returning a
// `delivered` receipt with echo output. It guards the extraction of the shared
// response-recording helper and confirms the ActionResumeAndDeliver success arm
// records the response the same way the direct-delivery path does.
//
// spec: 7.2 (path 6 lines 326-327 — atomic resume-and-deliver returns delivered)
//
// diagnosis: a failure means the resume-and-deliver success path stopped
// delivering or recording the response — the shared recordDeliveredResponse
// helper regressed, or the ActionResumeAndDeliver arm no longer stamps
// delivered after a successful resume and Send.
func TestResumeAndDeliverHappyPathDeliversAndRuns(t *testing.T) {
	store := memstore.New()
	seedSuspendedSession(t, store, "s-happy", "pod-3")
	srv, _ := inboxServer(t, store, executor.NewEchoExecutor())

	rr := sendMessageRequest(t, srv.Handler(), "s-happy", immediateMessage("hello again"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.MessageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DeliveryReceipt.Status != session.DeliveryStatusDelivered {
		t.Errorf("status = %q, want delivered (a pod-held immediate message resumes and delivers)", resp.DeliveryReceipt.Status)
	}
	if resp.DeliveryReceipt.DeliveredAt.IsZero() {
		t.Error("deliveredAt is zero for a delivered resume-and-deliver receipt")
	}
	if len(resp.Output) == 0 {
		t.Error("output is empty; the resumed session must deliver and record the echo response")
	}
	row, _ := store.Get(context.Background(), "acme", "s-happy")
	if row.State != session.StateRunning {
		t.Errorf("state = %q, want running (the atomic resume transitioned it)", row.State)
	}
}
