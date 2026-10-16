// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §16.1 catalog (F-7.3.10) + §11.7 / §16.7 (F-7.3.18) —
// recordSessionRetry fires the retry-counter metric and appends the
// session.retry_attempted audit row on every spend edge into
// resume_pending. The
// failure_class label echoes the row's §7.1 FailureClass, falling
// through to "unknown" for a session with no recorded class.

type recordedRetry struct {
	mu      sync.Mutex
	classes []string
}

func (r *recordedRetry) record(failureClass string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.classes = append(r.classes, failureClass)
}

type retryAuditSink struct {
	mu     sync.Mutex
	events []SessionLifecycleEvent
}

func (s *retryAuditSink) EmitSessionLifecycle(_ context.Context, ev SessionLifecycleEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func TestRecordSessionRetryEmitsMetricAndAudit_F_7_3_10_F_7_3_18(t *testing.T) {
	counter := &recordedRetry{}
	audit := &retryAuditSink{}
	srv := New(memstore.New(), Options{
		IncSessionRetry:    counter.record,
		LifecycleAuditSink: audit,
		Clock:              func() time.Time { return time.Date(2026, 5, 26, 11, 0, 0, 0, time.UTC) },
	})

	row := sessionstore.Session{
		ID:           "sess_retry",
		TenantID:     "acme",
		UserID:       "alice@acme.com",
		RuntimeRef:   "claude-code",
		State:        session.StateRunning,
		FailureClass: session.FailureClassRuntime,
	}
	srv.recordSessionRetry(context.Background(), row)

	if len(counter.classes) != 1 || counter.classes[0] != string(session.FailureClassRuntime) {
		t.Errorf("metric labels = %v, want [%q]", counter.classes, session.FailureClassRuntime)
	}
	if len(audit.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(audit.events))
	}
	if audit.events[0].EventType != auditSessionRetryAttempted {
		t.Errorf("EventType = %q, want %q", audit.events[0].EventType, auditSessionRetryAttempted)
	}
	if audit.events[0].FailureClass != string(session.FailureClassRuntime) {
		t.Errorf("FailureClass = %q, want %q", audit.events[0].FailureClass, session.FailureClassRuntime)
	}
}

func TestRecordSessionRetryDefaultsFailureClassToUnknown_F_7_3_10(t *testing.T) {
	counter := &recordedRetry{}
	srv := New(memstore.New(), Options{IncSessionRetry: counter.record})
	srv.recordSessionRetry(context.Background(), sessionstore.Session{
		ID:       "sess_noclass",
		TenantID: "acme",
		State:    session.StateRunning,
	})
	if len(counter.classes) != 1 || counter.classes[0] != "unknown" {
		t.Errorf("metric labels = %v, want [\"unknown\"]", counter.classes)
	}
}

// Nil hooks make recordSessionRetry a no-op, so neither of its callers has
// to guard. Both callers are the edges that spend a retry: the guarded write
// into resume_pending in transitionToResumePending (failure.go), and the
// resuming watchdog's resuming → resume_pending write, which reaches
// recordSessionRetry through the watchdog's notifyRetryAttempt and
// OnSessionRetryAttempt (usage.go).
// spec: §7.3 (Retry and Resume) "Retry accounting."
func TestRecordSessionRetryWithoutHooksIsNoOp_F_7_3_10_F_7_3_18(t *testing.T) {
	srv := New(memstore.New(), Options{})
	srv.recordSessionRetry(context.Background(), sessionstore.Session{
		ID:       "sess_nohook",
		TenantID: "acme",
		State:    session.StateRunning,
	})
}

// retryAttemptCount returns how many session.retry_attempted rows the sink
// holds, ignoring the other lifecycle rows a failure report appends.
func (s *retryAuditSink) retryAttemptCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, ev := range s.events {
		if ev.EventType == auditSessionRetryAttempted {
			n++
		}
	}
	return n
}

// TestRetrySpentOnlyOnFailureEdgeIntoResumePending_spec_7_3 pins the §7.3
// retry accounting: a retryable failure spends one retry, the successful
// restore that follows advances recovery_generation and spends none, and a
// second failure under maxRetries 2 therefore still enters resume_pending.
// The retry metric and audit row fire once per spend edge and never on the
// restore. Against a restore that also advanced retry_count, the second
// failure finds the budget spent and enters awaiting_client_action.
//
// spec: §7.3 (Retry and Resume) "Retry accounting.", §4.2 (Session Manager)
func TestRetrySpentOnlyOnFailureEdgeIntoResumePending_spec_7_3(t *testing.T) {
	ctx := context.Background()
	counter := &recordedRetry{}
	audit := &retryAuditSink{}
	store := memstore.New()
	srv := New(store, Options{
		IncSessionRetry:    counter.record,
		LifecycleAuditSink: audit,
		RetryPolicyCaps:    session.RetryPolicyCaps{MaxRetries: 2},
		Clock:              func() time.Time { return time.Date(2026, 5, 26, 11, 0, 0, 0, time.UTC) },
	})
	if err := store.Create(ctx, sessionstore.Session{
		ID: "sess_spend", TenantID: "acme", RuntimeRef: "echo", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	fail := func(step string, wantTo session.State, wantRetries int64) {
		t.Helper()
		disp, err := srv.ReportSessionFailure(ctx, FailureReport{
			TenantID: "acme", SessionID: "sess_spend", Reason: "pod_evicted",
		})
		if err != nil {
			t.Fatalf("%s: ReportSessionFailure: %v", step, err)
		}
		if disp.To != wantTo || disp.RetryCount != wantRetries {
			t.Fatalf("%s: disposition = %q/%d, want %q/%d", step, disp.To, disp.RetryCount, wantTo, wantRetries)
		}
	}
	// restore commits the resume_pending row to running through the
	// resume driver's commit (the binderless path, which claims no pod).
	restore := func() sessionstore.Session {
		t.Helper()
		row, err := store.Get(ctx, "acme", "sess_spend")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if exit := srv.completeResume(row, 0, resumeOutcome{}); exit != resumeExitCommitLanded {
			t.Fatalf("commit exit = %v, want a landed commit", exit)
		}
		got, err := store.Get(ctx, "acme", "sess_spend")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		return got
	}

	fail("first failure", session.StateResumePending, 1)
	if len(counter.classes) != 1 || audit.retryAttemptCount() != 1 {
		t.Fatalf("after first failure: metric=%d audit=%d, want 1/1", len(counter.classes), audit.retryAttemptCount())
	}

	got := restore()
	if got.RetryCount != 1 {
		t.Errorf("RetryCount after a successful restore = %d, want 1 (a restore spends no retry)", got.RetryCount)
	}
	if got.RecoveryGeneration != 1 {
		t.Errorf("RecoveryGeneration after a restore = %d, want 1", got.RecoveryGeneration)
	}
	if len(counter.classes) != 1 || audit.retryAttemptCount() != 1 {
		t.Errorf("after restore: metric=%d audit=%d, want 1/1 (a restore fires neither)", len(counter.classes), audit.retryAttemptCount())
	}

	fail("second failure", session.StateResumePending, 2)
	if len(counter.classes) != 2 || audit.retryAttemptCount() != 2 {
		t.Errorf("after second failure: metric=%d audit=%d, want 2/2", len(counter.classes), audit.retryAttemptCount())
	}
}
