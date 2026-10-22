// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// spec: §16.1 catalog — lenny_session_resume_attempts_total{pool, outcome}
// fires once per /resume call with outcome="success" on the happy path.
// F-7.3.10. The matching {outcome="failure"} branch requires a podBinder
// wiring and is exercised in start_pod_test.go.

type resumeCounter struct {
	mu      sync.Mutex
	entries []struct{ pool, outcome string }
}

func (c *resumeCounter) record(pool, outcome string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, struct{ pool, outcome string }{pool, outcome})
}

type lifecycleSink struct {
	mu     sync.Mutex
	events []sessionserver.SessionLifecycleEvent
}

func (s *lifecycleSink) EmitSessionLifecycle(_ context.Context, ev sessionserver.SessionLifecycleEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *lifecycleSink) eventsOf(eventType string) []sessionserver.SessionLifecycleEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]sessionserver.SessionLifecycleEvent, 0)
	for _, ev := range s.events {
		if ev.EventType == eventType {
			out = append(out, ev)
		}
	}
	return out
}

// spec: §16.1 (Metrics), §7.3 (Resume driver) — the resume-attempts counter
// counts each resuming incarnation whose entry write the attempt saw take
// effect; the binderless commit writes no resuming incarnation and records no
// count.
func TestBinderlessResumeRecordsNoResumeAttempt_spec_16_1(t *testing.T) {
	store := memstore.New()
	counter := &resumeCounter{}
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader:         true,
		IncSessionResumeAttempt: counter.record,
	})
	seedAwaitingParent(t, store, "sess_metric")

	rr := resumeAndDrive(t, srv, "sess_metric")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume status = %d, body=%s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), "acme", "sess_metric")
	if err != nil || row.State != session.StateRunning {
		t.Fatalf("row = %q (err %v), want running after the binderless commit", row.State, err)
	}
	// The counter counts resuming incarnations whose entry write took
	// effect; the binderless commit writes none, so it records nothing.
	if len(counter.entries) != 0 {
		t.Fatalf("entries = %+v, want none for a binderless commit", counter.entries)
	}
}

// spec: §11.7 / §16.7 — every successful resume appends a
// session.resumed audit row. F-7.3.18.
func TestResumeEmitsSessionResumedAuditRow_F_7_3_18(t *testing.T) {
	store := memstore.New()
	sink := &lifecycleSink{}
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true, LifecycleAuditSink: sink})
	seedAwaitingParent(t, store, "sess_audit")

	rr := resumeAndDrive(t, srv, "sess_audit")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume status = %d, body=%s", rr.Code, rr.Body.String())
	}
	rows := sink.eventsOf("session.resumed")
	if len(rows) != 1 {
		t.Fatalf("session.resumed audit rows = %d, want 1", len(rows))
	}
	if rows[0].SessionID != "sess_audit" {
		t.Errorf("SessionID = %q, want sess_audit", rows[0].SessionID)
	}
	if rows[0].TenantID != "acme" {
		t.Errorf("TenantID = %q, want acme", rows[0].TenantID)
	}
}

// spec: §11.7 / §16.7 — when no audit sink is wired the resume still
// proceeds without panic. F-7.3.18.
func TestResumeWithoutAuditSinkDoesNotPanic_F_7_3_18(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})
	seedAwaitingParent(t, store, "sess_noaudit")
	rr := resumeAndDrive(t, srv, "sess_noaudit")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume status = %d, body=%s", rr.Code, rr.Body.String())
	}
}

// spec: §16.1 catalog — without the IncSessionResumeAttempt option the
// resume call still succeeds. F-7.3.10.
func TestResumeWithoutMetricsHookSucceeds_F_7_3_10(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})
	seedAwaitingParent(t, store, "sess_nometric")
	rr := resumeAndDrive(t, srv, "sess_nometric")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume status = %d, body=%s", rr.Code, rr.Body.String())
	}
}
