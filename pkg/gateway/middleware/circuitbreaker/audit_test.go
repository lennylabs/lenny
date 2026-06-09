// SPDX-License-Identifier: MIT

package circuitbreaker_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	cb "github.com/lennylabs/lenny/pkg/circuitbreaker"
	cbmw "github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker"
)

// capturedRow records one Append call.
type capturedRow struct {
	tenantID  string
	eventType string
	payload   map[string]any
}

// fakeAppender captures appended audit rows.
type fakeAppender struct {
	rows []capturedRow
	err  error
}

func (f *fakeAppender) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, _ time.Time) (audit.Row, error) {
	var m map[string]any
	_ = json.Unmarshal(payload, &m)
	f.rows = append(f.rows, capturedRow{tenantID: tenantID, eventType: eventType, payload: m})
	return audit.Row{}, f.err
}

// fakeMetrics counts the §11.6 rejection counters and the §16.1
// cache-stale-serve counter (by outcome).
type fakeMetrics struct {
	total       int
	suppressed  int
	staleServes map[string]int
}

func (m *fakeMetrics) RecordCircuitBreakerRejection(string, string, string)            { m.total++ }
func (m *fakeMetrics) RecordCircuitBreakerRejectionSuppressed(string, string, string)  { m.suppressed++ }
func (m *fakeMetrics) RecordCircuitBreakerCacheStaleServe(outcome string) {
	if m.staleServes == nil {
		m.staleServes = map[string]int{}
	}
	m.staleServes[outcome]++
}

func openBreaker() cb.Breaker {
	return cb.Breaker{
		Name:      "session-storm",
		State:     cb.StateOpen,
		Reason:    "too many failed creations",
		OpenedAt:  time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC),
		LimitTier: cb.TierOperationType,
		Scope:     cb.Scope{OperationType: cb.OpSessionCreation},
	}
}

// spec: §16.7 — the audit row carries the mandatory payload fields and
// the authenticated caller identity from the snapshot.
func TestReportWritesFullAuditRow(t *testing.T) {
	app := &fakeAppender{}
	met := &fakeMetrics{}
	r := cbmw.NewAuditReporter(app, met, "gw-7", func() time.Time { return time.Unix(0, 0) })

	r.Report(context.Background(), openBreaker(), cbmw.RejectionSnapshot{
		CallerSub:      "alice",
		CallerTenantID: "acme",
		Runtime:        "rt-1",
		Pool:           "pool-a",
		SessionID:      "sess-1",
	})

	if len(app.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(app.rows))
	}
	row := app.rows[0]
	if row.eventType != cbmw.EventAdmissionCircuitBreakerRejected {
		t.Errorf("eventType = %q", row.eventType)
	}
	if row.tenantID != "acme" {
		t.Errorf("tenantID = %q, want acme", row.tenantID)
	}
	for _, k := range []string{"circuit_name", "reason", "opened_at", "limit_tier",
		"replica_service_instance_id", "caller_sub", "caller_tenant_id", "runtime", "pool", "session_id"} {
		if _, ok := row.payload[k]; !ok {
			t.Errorf("payload missing %q: %v", k, row.payload)
		}
	}
	if row.payload["caller_sub"] != "alice" || row.payload["replica_service_instance_id"] != "gw-7" {
		t.Errorf("identity fields wrong: %v", row.payload)
	}
	if met.total != 1 || met.suppressed != 0 {
		t.Errorf("metrics total=%d suppressed=%d, want 1/0", met.total, met.suppressed)
	}
}

// spec: §8.3 delegation child — parent_session_id and delegation_depth
// are recorded when admitting a delegation child.
func TestReportDelegationSnapshot(t *testing.T) {
	app := &fakeAppender{}
	r := cbmw.NewAuditReporter(app, nil, "gw-1", func() time.Time { return time.Unix(0, 0) })
	r.Report(context.Background(), openBreaker(), cbmw.RejectionSnapshot{
		CallerTenantID:  "acme",
		ParentSessionID: "parent-1",
		DelegationDepth: 3,
	})
	row := app.rows[0]
	if row.payload["parent_session_id"] != "parent-1" {
		t.Errorf("parent_session_id = %v", row.payload["parent_session_id"])
	}
	if d, _ := row.payload["delegation_depth"].(float64); d != 3 {
		t.Errorf("delegation_depth = %v, want 3", row.payload["delegation_depth"])
	}
}

// spec: §11.6 line 331 — the first rejection per (tenant, circuit,
// caller) in a 10s window is written; later rejections in the window
// are suppressed but still counted in rejections_total.
func TestReportSamplesPerTupleWithinWindow(t *testing.T) {
	app := &fakeAppender{}
	met := &fakeMetrics{}
	now := time.Unix(100, 0)
	r := cbmw.NewAuditReporter(app, met, "gw-1", func() time.Time { return now })
	snap := cbmw.RejectionSnapshot{CallerSub: "alice", CallerTenantID: "acme"}

	r.Report(context.Background(), openBreaker(), snap) // written
	r.Report(context.Background(), openBreaker(), snap) // suppressed
	r.Report(context.Background(), openBreaker(), snap) // suppressed

	if len(app.rows) != 1 {
		t.Fatalf("rows = %d, want 1 (rest suppressed)", len(app.rows))
	}
	if met.total != 3 || met.suppressed != 2 {
		t.Errorf("metrics total=%d suppressed=%d, want 3/2", met.total, met.suppressed)
	}

	// A different caller opens its own window.
	r.Report(context.Background(), openBreaker(), cbmw.RejectionSnapshot{CallerSub: "bob", CallerTenantID: "acme"})
	if len(app.rows) != 2 {
		t.Fatalf("rows = %d, want 2 (bob is a distinct tuple)", len(app.rows))
	}

	// After the window elapses, alice's tuple is written again.
	now = now.Add(11 * time.Second)
	r.Report(context.Background(), openBreaker(), snap)
	if len(app.rows) != 3 {
		t.Fatalf("rows = %d, want 3 (window elapsed)", len(app.rows))
	}
}

// A nil appender records metrics only and never writes.
func TestReportNilAppenderMetricsOnly(t *testing.T) {
	met := &fakeMetrics{}
	r := cbmw.NewAuditReporter(nil, met, "gw-1", nil)
	r.Report(context.Background(), openBreaker(), cbmw.RejectionSnapshot{CallerTenantID: "acme"})
	if met.total != 1 || met.suppressed != 0 {
		t.Errorf("metrics total=%d suppressed=%d, want 1/0", met.total, met.suppressed)
	}
}

// spec: §11.6 line 327 — on a breaker match the middleware emits the
// audit row before returning 503.
func TestMiddlewareEmitsAuditOnMatch(t *testing.T) {
	app := &fakeAppender{}
	r := cbmw.NewAuditReporter(app, &fakeMetrics{}, "gw-1", func() time.Time { return time.Unix(0, 0) })
	reg := cbmw.NewMemoryRegistry()
	reg.Set([]cb.Breaker{openBreaker()})

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := cbmw.Wrap(inner, reg, cbmw.Options{
		Extract: func(*http.Request) cb.Request {
			return cb.Request{OperationType: cb.OpSessionCreation}
		},
		Audit:    r,
		Snapshot: func(*http.Request) cbmw.RejectionSnapshot { return cbmw.RejectionSnapshot{CallerSub: "alice", CallerTenantID: "acme"} },
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/sessions", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if len(app.rows) != 1 || app.rows[0].payload["caller_sub"] != "alice" {
		t.Errorf("expected one audit row for alice, got %v", app.rows)
	}
}
