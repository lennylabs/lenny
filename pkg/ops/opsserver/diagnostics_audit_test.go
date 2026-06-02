// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/auditrate"
	"github.com/lennylabs/lenny/pkg/ops/diagnostics"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// captureSink records the coalesced diagnostic audit events the limiter
// emits, for assertion. It is safe for concurrent use.
type captureSink struct {
	mu     sync.Mutex
	events []auditrate.Event
}

func (c *captureSink) emit(e auditrate.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *captureSink) snapshot() []auditrate.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]auditrate.Event, len(c.events))
	copy(out, c.events)
	return out
}

func okDiagSource() fakeDiagSource {
	return fakeDiagSource{
		session: diagnostics.SessionRecord{SessionID: "sess-1", State: "failed", Found: true},
		pool:    diagnostics.PoolRecord{Name: "p-1", Found: true},
		creds:   diagnostics.CredentialPoolRecord{Name: "c-1", Found: true},
	}
}

// TestDiagnosticAuditEmittedOnWindowClose confirms the §25.9 diagnostics
// audit event is emitted (once) when the coalescing window closes, and
// that the §16.7 event type is the session-diagnosed type. F-25.9.15.
func TestDiagnosticAuditEmittedOnWindowClose_spec_25_9_3699(t *testing.T) {
	sink := &captureSink{}
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	srv := opsserver.New(opsserver.Options{
		Diagnostics: diagnostics.NewService(okDiagSource()),
		DiagnosticsAudit: &opsserver.DiagnosticsAuditConfig{
			Emit: sink.emit,
			Now:  func() time.Time { return now },
		},
	})
	rec, _ := doJSON(t, srv, http.MethodGet, "/v1/admin/diagnostics/sessions/sess-1", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// The window is open: nothing emitted yet.
	if got := sink.snapshot(); len(got) != 0 {
		t.Fatalf("emitted %d events before the window closed, want 0", len(got))
	}
	// Past the 60s window, a sweep flushes the single coalesced event.
	srv.SweepDiagnosticsAudit(now.Add(61 * time.Second))
	got := sink.snapshot()
	if len(got) != 1 {
		t.Fatalf("emitted %d events after sweep, want 1", len(got))
	}
	if got[0].EventType != "diagnostics.session_diagnosed" || got[0].InvocationCount != 1 {
		t.Errorf("event = %+v, want session_diagnosed with invocationCount 1", got[0])
	}
	if got[0].ResourceType != "session" || got[0].ResourceID != "sess-1" {
		t.Errorf("event resource = %s/%s, want session/sess-1", got[0].ResourceType, got[0].ResourceID)
	}
}

// TestDiagnosticAuditCoalescesRepeats confirms repeated calls for the
// same session within the 60s window emit one audit event carrying the
// incremented invocationCount. F-25.9.15.
func TestDiagnosticAuditCoalescesRepeats_spec_25_9_3699(t *testing.T) {
	sink := &captureSink{}
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	srv := opsserver.New(opsserver.Options{
		Diagnostics: diagnostics.NewService(okDiagSource()),
		DiagnosticsAudit: &opsserver.DiagnosticsAuditConfig{
			Emit: sink.emit,
			Now:  func() time.Time { return now },
		},
	})
	for i := 0; i < 3; i++ {
		rec, _ := doJSON(t, srv, http.MethodGet, "/v1/admin/diagnostics/sessions/sess-1", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d status = %d, want 200", i, rec.Code)
		}
	}
	srv.FlushDiagnosticsAudit()
	got := sink.snapshot()
	if len(got) != 1 {
		t.Fatalf("emitted %d events, want 1 (coalesced)", len(got))
	}
	if got[0].InvocationCount != 3 {
		t.Errorf("invocationCount = %d, want 3", got[0].InvocationCount)
	}
}

// TestDiagnosticAuditRateLimitDrops confirms the per-service-account rate
// cap drops the excess distinct diagnostic event and fires the
// rate-limited counter callback. F-25.9.15.
func TestDiagnosticAuditRateLimitDrops_spec_25_9_3700(t *testing.T) {
	sink := &captureSink{}
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	var dropped []string
	srv := opsserver.New(opsserver.Options{
		Diagnostics: diagnostics.NewService(okDiagSource()),
		DiagnosticsAudit: &opsserver.DiagnosticsAuditConfig{
			Emit:          sink.emit,
			RatePerMinute: 1, // one distinct diagnostic event per minute
			RateLimited:   func(eventType, sa string) { dropped = append(dropped, eventType) },
			Now:           func() time.Time { return now },
		},
	})
	// Two distinct resources (a pool and a credential pool) by the same
	// anonymous service account: the first emits, the second is dropped.
	if rec, _ := doJSON(t, srv, http.MethodGet, "/v1/admin/diagnostics/pools/p-1", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("pool status = %d, want 200", rec.Code)
	}
	if rec, _ := doJSON(t, srv, http.MethodGet, "/v1/admin/diagnostics/credential-pools/c-1", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("credential-pool status = %d, want 200", rec.Code)
	}
	if len(dropped) != 1 || dropped[0] != "diagnostics.credential_pool_diagnosed" {
		t.Fatalf("rate-limited drops = %v, want one credential_pool_diagnosed drop", dropped)
	}
	// The dropped event must not flush as an audit event.
	srv.FlushDiagnosticsAudit()
	if got := sink.snapshot(); len(got) != 1 {
		t.Fatalf("emitted %d events, want 1 (the dropped one is not emitted)", len(got))
	}
}

// TestDiagnosticAuditDisabledByDefault confirms that without the config
// the diagnostic endpoints serve without emitting audit events (and the
// sweep/flush helpers are no-ops). F-25.9.15.
func TestDiagnosticAuditDisabledByDefault(t *testing.T) {
	srv := opsserver.New(opsserver.Options{Diagnostics: diagnostics.NewService(okDiagSource())})
	if rec, _ := doJSON(t, srv, http.MethodGet, "/v1/admin/diagnostics/sessions/sess-1", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// No panic on the no-op sweep/flush.
	srv.SweepDiagnosticsAudit(time.Now())
	srv.FlushDiagnosticsAudit()
}
