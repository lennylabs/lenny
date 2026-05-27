// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
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

func TestResumeIncrementsSessionResumeAttemptsTotal_F_7_3_10(t *testing.T) {
	store := memstore.New()
	counter := &resumeCounter{}
	srv := sessionserver.New(store, sessionserver.Options{
		IncSessionResumeAttempt: counter.record,
	})
	seedAwaitingParent(t, store, "sess_metric")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_metric/resume")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if len(counter.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(counter.entries))
	}
	if counter.entries[0].outcome != "success" {
		t.Errorf("outcome = %q, want success", counter.entries[0].outcome)
	}
}

// spec: §11.7 / §16.7 — every successful resume appends a
// session.resumed audit row. F-7.3.18.
func TestResumeEmitsSessionResumedAuditRow_F_7_3_18(t *testing.T) {
	store := memstore.New()
	sink := &lifecycleSink{}
	srv := sessionserver.New(store, sessionserver.Options{LifecycleAuditSink: sink})
	seedAwaitingParent(t, store, "sess_audit")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_audit/resume")
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
	srv := sessionserver.New(store, sessionserver.Options{})
	seedAwaitingParent(t, store, "sess_noaudit")
	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_noaudit/resume")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume status = %d, body=%s", rr.Code, rr.Body.String())
	}
}

// spec: §16.1 catalog — without the IncSessionResumeAttempt option the
// resume call still succeeds. F-7.3.10.
func TestResumeWithoutMetricsHookSucceeds_F_7_3_10(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})
	seedAwaitingParent(t, store, "sess_nometric")
	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_nometric/resume")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume status = %d, body=%s", rr.Code, rr.Body.String())
	}
}
