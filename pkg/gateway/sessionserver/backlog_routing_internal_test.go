// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// postMessage sends one REST message and returns the receipt status.
func postMessage(t *testing.T, s *Server, id string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+id+"/messages",
		strings.NewReader(`{"messages":[{"role":"user","content":"x"}]}`))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	asOwner(s.Handler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST messages = %d: %s", rr.Code, rr.Body.String())
	}
	var resp MessageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return string(resp.DeliveryReceipt.Status)
}

func (r *suspensionRig) seedPodless(t *testing.T, id string) sessionstore.Session {
	t.Helper()
	since := r.now.Add(-time.Hour)
	row := sessionstore.Session{
		UserID: testOwner,
		ID:     id, TenantID: "acme", State: session.StateSuspended, SuspendedAt: since,
		SuspendedReason: sessionstore.SuspendedReasonIdle, CreatedAt: since, UpdatedAt: since,
	}
	if err := r.store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	stored, _ := r.store.Get(context.Background(), "acme", id)
	return stored
}

func (r *suspensionRig) dlqLen(t *testing.T, id string) int {
	t.Helper()
	n, err := r.coord.HeldLen(context.Background(), "acme", id, false, time.Time{})
	if err != nil {
		t.Fatalf("HeldLen: %v", err)
	}
	return n
}

// TestPodlessSuspendedMessageHoldsAndResumes_spec_7_2 pins §7.2 path 6 on a
// podless suspended REST target: the message is held in the DLQ, answered
// queued, and the session moves to resume_pending; the exported MCP seam
// holds the same way.
//
// spec: 7.2 (Message delivery routing — seven paths, path 6)
func TestPodlessSuspendedMessageHoldsAndResumes_spec_7_2(t *testing.T) {
	r := newSuspensionRig(t, true)
	r.seedPodless(t, "s1")
	if got := postMessage(t, r.srv, "s1"); got != string(session.DeliveryStatusQueued) {
		t.Fatalf("receipt = %q, want queued", got)
	}
	if r.dlqLen(t, "s1") != 1 || r.get(t, "s1").State != session.StateResumePending {
		t.Fatalf("DLQ %d state %q, want 1 and resume_pending", r.dlqLen(t, "s1"), r.get(t, "s1").State)
	}

	row := r.seedPodless(t, "s2")
	outcome, _, err := r.srv.HoldMessage(context.Background(), "acme", "s2", sessioninbox.Message{MessageID: "m", Payload: []byte(`{}`), EnqueuedAt: r.now})
	if err != nil || outcome != HoldQueued || r.dlqLen(t, "s2") != 1 {
		t.Fatalf("HoldMessage = %v, %v; DLQ %d", outcome, err, r.dlqLen(t, "s2"))
	}
	if err := r.srv.ResumeSuspended(context.Background(), row); err != nil {
		t.Fatalf("ResumeSuspended: %v", err)
	}
	if got := r.get(t, "s2").State; got != session.StateResumePending {
		t.Fatalf("state after ResumeSuspended = %q, want resume_pending", got)
	}
	unlock, err := r.srv.LockDelivery(context.Background(), "s2")
	if err != nil {
		t.Fatalf("LockDelivery: %v", err)
	}
	unlock()
}

// TestSealedRefusalReroutesRESTMessage_spec_7_2 pins that a REST batch the
// adapter refuses as sealed, on a row the idle suspension moved to podless
// suspended, is held in the DLQ and answered queued rather than failed.
//
// spec: 7.2 (Message delivery routing — seven paths, path 6), 6.2 (Pod State
// Machine)
func TestSealedRefusalReroutesRESTMessage_spec_7_2(t *testing.T) {
	r := newSuspensionRig(t, true)
	r.seedRunning(t, "s1")
	r.exec.sendErr = func() error {
		_, _ = r.store.Update(context.Background(), "acme", "s1", func(s *sessionstore.Session) error {
			s.State = session.StateSuspended
			s.SuspendedReason = sessionstore.SuspendedReasonIdle
			s.SuspendedAt = r.now
			s.PodAssignment = ""
			return nil
		})
		return &executor.SealedError{}
	}
	if got := postMessage(t, r.srv, "s1"); got != string(session.DeliveryStatusQueued) {
		t.Fatalf("receipt = %q, want queued", got)
	}
	if r.dlqLen(t, "s1") != 1 {
		t.Fatalf("DLQ = %d, want 1", r.dlqLen(t, "s1"))
	}
	if got := r.get(t, "s1").State; got != session.StateResumePending {
		t.Fatalf("state = %q, want resume_pending", got)
	}
}

// TestUnboundSendReroutesWhenTheSessionLeftRunning_spec_6_2 pins the
// not-bound send: when this replica holds no binding, nothing was sent, so a
// batch whose session an idle suspension moved to podless suspended after
// the classification read is held in the DLQ and answered queued; a session
// still running answers EXECUTOR_FAILURE, because the serving replica does
// not coordinate it.
//
// spec: 6.2 (maxClientIdleSeconds clock behavior across states), 7.2
// (Message delivery routing — seven paths, path 6)
func TestUnboundSendReroutesWhenTheSessionLeftRunning_spec_6_2(t *testing.T) {
	r := newSuspensionRig(t, true)
	r.seedRunning(t, "s1")
	r.exec.sendErr = func() error {
		_, _ = r.store.Update(context.Background(), "acme", "s1", func(s *sessionstore.Session) error {
			s.State = session.StateSuspended
			s.SuspendedReason = sessionstore.SuspendedReasonIdle
			s.SuspendedAt = r.now
			s.PodAssignment = ""
			return nil
		})
		return fmt.Errorf("podexec: session s1: %w", executor.ErrNotBound)
	}
	if got := postMessage(t, r.srv, "s1"); got != string(session.DeliveryStatusQueued) {
		t.Fatalf("receipt = %q, want queued", got)
	}
	if r.dlqLen(t, "s1") != 1 || r.get(t, "s1").State != session.StateResumePending {
		t.Fatalf("DLQ %d state %q, want 1 and resume_pending", r.dlqLen(t, "s1"), r.get(t, "s1").State)
	}

	r.seedRunning(t, "s2")
	r.exec.sendErr = func() error { return fmt.Errorf("podexec: session s2: %w", executor.ErrNotBound) }
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/s2/messages",
		strings.NewReader(`{"messages":[{"role":"user","content":"x"}]}`))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	asOwner(r.srv.Handler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError || !strings.Contains(rr.Body.String(), "EXECUTOR_FAILURE") {
		t.Fatalf("unbound send on a running session = %d %s, want 500 EXECUTOR_FAILURE", rr.Code, rr.Body.String())
	}
	if r.dlqLen(t, "s2") != 0 {
		t.Fatalf("DLQ = %d for a running session, want nothing held", r.dlqLen(t, "s2"))
	}
}

// TestUnboundSendWhoseReReadFailsHolds_spec_6_2 pins the not-bound send
// whose primary re-read fails on REST: the idle suspension released the pod
// after the classification read, so the batch reached no runtime, and a
// failed re-read holds it in the DLQ and answers queued, as a sealed refusal
// whose re-read fails does, rather than refusing it with EXECUTOR_FAILURE.
//
// spec: 6.2 (Idle suspension), 7.2 (Message delivery routing — seven paths,
// path 6)
func TestUnboundSendWhoseReReadFailsHolds_spec_6_2(t *testing.T) {
	r := newSuspensionRig(t, true)
	r.seedRunning(t, "s1")
	primary := &flakyPrimary{Store: r.store}
	r.srv.store = primary
	sends := 0
	r.exec.sendErr = func() error {
		sends++
		_, _ = r.store.Update(context.Background(), "acme", "s1", func(s *sessionstore.Session) error {
			s.State = session.StateSuspended
			s.SuspendedReason = sessionstore.SuspendedReasonIdle
			s.SuspendedAt = r.now
			s.PodAssignment = ""
			return nil
		})
		primary.fails.Store(1)
		return fmt.Errorf("podexec: session s1: %w", executor.ErrNotBound)
	}
	if got := postMessage(t, r.srv, "s1"); got != string(session.DeliveryStatusQueued) {
		t.Fatalf("receipt = %q, want queued", got)
	}
	if sends != 1 {
		t.Fatalf("sends = %d, want the one not-bound send and no re-routed send", sends)
	}
	if r.dlqLen(t, "s1") != 1 {
		t.Fatalf("DLQ = %d, want the batch held", r.dlqLen(t, "s1"))
	}
	if got := r.get(t, "s1").State; got != session.StateResumePending {
		t.Fatalf("state = %q, want resume_pending from the guarded resume", got)
	}
}

// TestUnboundSendAfterSealedRefusalHolds_spec_6_2 pins the idle-suspension
// race on REST: the adapter refuses the batch as sealed while the primary
// re-read still shows `running`, the re-routed send then finds the pod
// unbound by the release, and the batch, which never reached the runtime, is
// held in the DLQ and answered queued rather than failed with
// EXECUTOR_FAILURE.
//
// spec: 6.2 (Idle suspension), 7.2 (Message delivery routing — seven paths,
// path 6)
func TestUnboundSendAfterSealedRefusalHolds_spec_6_2(t *testing.T) {
	r := newSuspensionRig(t, true)
	r.seedRunning(t, "s1")
	sends := 0
	r.exec.sendErr = func() error {
		sends++
		if sends == 1 {
			return &executor.SealedError{}
		}
		return fmt.Errorf("podexec: session s1: %w", executor.ErrNotBound)
	}
	if got := postMessage(t, r.srv, "s1"); got != string(session.DeliveryStatusQueued) {
		t.Fatalf("receipt = %q, want queued", got)
	}
	if sends != 2 {
		t.Fatalf("sends = %d, want the refused send and one re-routed send", sends)
	}
	if r.dlqLen(t, "s1") != 1 {
		t.Fatalf("DLQ = %d, want the refused batch held", r.dlqLen(t, "s1"))
	}
}

// TestResumeBacklogResumesAHeldPodSession_spec_7_2 pins the backlog resume:
// with a message accepted after the suspension, ResumeBacklog answers true and
// resumes the session on its pod off the caller, delivering the message; with
// no held message, or no local binding, it answers false.
//
// spec: 7.2 (Message delivery routing — seven paths, path 6)
func TestResumeBacklogResumesAHeldPodSession_spec_7_2(t *testing.T) {
	r := newSuspensionRig(t, true)
	row := r.seedSuspended(t, "s1", r.now.Add(-time.Hour))
	if r.srv.ResumeBacklog(context.Background(), row) {
		t.Fatal("ResumeBacklog answered true with no held message")
	}
	if _, err := r.coord.EnqueueDLQ(context.Background(), "acme", "s1", sessioninbox.Message{
		MessageID: "m1", Payload: []byte(`{"role":"user","content":"x"}`), EnqueuedAt: r.now,
	}, 0); err != nil {
		t.Fatal(err)
	}
	if !r.srv.ResumeBacklog(context.Background(), row) {
		t.Fatal("ResumeBacklog answered false with a held message")
	}
	deadline := time.Now().Add(5 * time.Second)
	for r.srv.backlogResumes.has("s1") {
		if time.Now().After(deadline) {
			t.Fatal("backlog resume did not finish")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := r.get(t, "s1").State; got != session.StateRunning {
		t.Fatalf("state = %q, want running", got)
	}
	r.registry.Remove("s1")
	if r.srv.ResumeBacklog(context.Background(), row) {
		t.Fatal("ResumeBacklog answered true with no local binding")
	}
}

// TestResumeAfterPodReleasedMovesToResumePending_spec_7_2 pins the POST
// /resume race: a request that read the row holding its pod, whose pod a
// suspension release cleared before the resume, answers 200 with
// resume_pending.
//
// spec: 7.2 (Message delivery routing — seven paths, path 6)
func TestResumeAfterPodReleasedMovesToResumePending_spec_7_2(t *testing.T) {
	r := newSuspensionRig(t, true)
	row := r.seedSuspended(t, "s1", r.now.Add(-time.Hour))
	_, _ = r.store.Update(context.Background(), "acme", "s1", func(s *sessionstore.Session) error {
		s.PodAssignment = ""
		return nil
	})
	rec := httptest.NewRecorder()
	r.srv.resumeHeldPodRequest(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions/s1/resume", nil), row)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), string(session.StateResumePending)) {
		t.Fatalf("resume = %d %s, want 200 resume_pending", rec.Code, rec.Body.String())
	}
}

// TestBacklogRecheckerRunPassesAtStart_spec_7_2 pins that Run passes once
// before its first interval and stops with its context.
//
// spec: 7.2 (Message delivery routing — seven paths, path 6)
func TestBacklogRecheckerRunPassesAtStart_spec_7_2(t *testing.T) {
	calls := make(chan struct{}, 4)
	rc := &BacklogRechecker{
		Server: New(nil, Options{DevTenantHeader: true}),
		Tenants: func(context.Context) ([]string, error) {
			calls <- struct{}{}
			return nil, nil
		},
		Interval: time.Hour,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rc.Run(ctx); close(done) }()
	select {
	case <-calls:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not pass at start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop with its context")
	}
}

// TestBacklogResumeCountsAMessageHeldInTheSuspensionMillisecond_spec_7_2 pins
// the backlog condition of an interrupt suspension at sub-millisecond
// resolution: a message the gateway held later in the millisecond that
// contains SuspendedAt was accepted after the session entered suspended, so
// the coordinating replica's ResumeBacklog resumes the session on its pod for
// it, and once the pod is released the leader's re-check moves the podless
// row to resume_pending. Neither evaluation may leave the message in the DLQ
// until the suspended lifetime expires the session.
//
// spec: 7.2 (Message delivery routing — seven paths, path 6)
func TestBacklogResumeCountsAMessageHeldInTheSuspensionMillisecond_spec_7_2(t *testing.T) {
	ctx := context.Background()
	suspendedAt := time.Date(2026, 10, 14, 11, 0, 0, 400_000, time.UTC)
	held := sessioninbox.Message{
		MessageID: "m1", Payload: []byte(`{"role":"user","content":"x"}`),
		EnqueuedAt: suspendedAt.Add(500 * time.Microsecond),
	}

	t.Run("coordinating replica resumes on the pod", func(t *testing.T) {
		r := newSuspensionRig(t, true)
		row := r.seedSuspended(t, "s1", suspendedAt)
		if _, err := r.coord.EnqueueDLQ(ctx, "acme", "s1", held, 0); err != nil {
			t.Fatal(err)
		}
		if n, err := r.srv.heldLen(ctx, row); err != nil || n != 1 {
			t.Fatalf("heldLen = %d, %v; want 1", n, err)
		}
		if !r.srv.ResumeBacklog(ctx, row) {
			t.Fatal("ResumeBacklog answered false for a message held in the suspension millisecond")
		}
		deadline := time.Now().Add(5 * time.Second)
		for r.srv.backlogResumes.has("s1") {
			if time.Now().After(deadline) {
				t.Fatal("backlog resume did not finish")
			}
			time.Sleep(2 * time.Millisecond)
		}
		if got := r.get(t, "s1").State; got != session.StateRunning {
			t.Fatalf("state = %q, want running", got)
		}
	})

	t.Run("leader re-check moves the podless row to resume_pending", func(t *testing.T) {
		r := newSuspensionRig(t, true)
		store := r.store.(*memstore.Store)
		if err := store.Create(ctx, sessionstore.Session{
			UserID: testOwner,
			ID:     "s2", TenantID: "acme", State: session.StateSuspended, SuspendedAt: suspendedAt,
			SuspendedReason: sessionstore.SuspendedReasonInterruptAcknowledged,
			CreatedAt:       suspendedAt, UpdatedAt: suspendedAt,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := r.coord.EnqueueDLQ(ctx, "acme", "s2", held, 0); err != nil {
			t.Fatal(err)
		}
		rc := &BacklogRechecker{
			Server:  r.srv,
			Lister:  store,
			Tenants: func(context.Context) ([]string, error) { return []string{"acme"}, nil },
		}
		if n := rc.Pass(ctx); n != 1 {
			t.Fatalf("Pass resumed %d, want 1", n)
		}
		if got := r.get(t, "s2").State; got != session.StateResumePending {
			t.Fatalf("state = %q, want resume_pending", got)
		}
	})
}
