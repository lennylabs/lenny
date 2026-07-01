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
// session.retry_attempted audit row on every recovery bump. The
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

// Nil hooks degrade to a no-op so the caller (bumpRecoveryGeneration)
// never has to guard.
func TestRecordSessionRetryWithoutHooksIsNoOp_F_7_3_10_F_7_3_18(t *testing.T) {
	srv := New(memstore.New(), Options{})
	srv.recordSessionRetry(context.Background(), sessionstore.Session{
		ID:       "sess_nohook",
		TenantID: "acme",
		State:    session.StateRunning,
	})
}
