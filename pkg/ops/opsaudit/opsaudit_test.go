// SPDX-License-Identifier: MIT

package opsaudit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
)

// captureAppender records every Append so a test can assert the durable
// row the Recorder committed. A non-nil failErr makes every Append fail.
type captureAppender struct {
	calls   []appendCall
	failErr error
}

type appendCall struct {
	tenant    string
	eventType string
	payload   json.RawMessage
	at        time.Time
}

func (c *captureAppender) Append(_ context.Context, tenant, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error) {
	c.calls = append(c.calls, appendCall{tenant, eventType, payload, at})
	if c.failErr != nil {
		return audit.Row{}, c.failErr
	}
	return audit.Row{TenantID: tenant, EventType: eventType, Payload: payload, Timestamp: at}, nil
}

// spec: §11.7 line 435 — ops_event.* events route to the platform tenant.
func TestRecord_CommitsUnderPlatformTenant_spec_11_7_435(t *testing.T) {
	cap := &captureAppender{}
	r := New(cap)
	if !r.Durable() {
		t.Fatal("Durable() = false with an appender wired")
	}
	at := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	r.Record("remediation.lock_acquired", map[string]any{"scope": "pool:default-gvisor", "lockId": "abc"}, at)

	if len(cap.calls) != 1 {
		t.Fatalf("Append calls = %d, want 1", len(cap.calls))
	}
	got := cap.calls[0]
	if got.tenant != PlatformTenantID {
		t.Errorf("tenant = %q, want %q", got.tenant, PlatformTenantID)
	}
	if got.eventType != "remediation.lock_acquired" {
		t.Errorf("eventType = %q", got.eventType)
	}
	if !got.at.Equal(at) {
		t.Errorf("at = %v, want %v", got.at, at)
	}
	var fields map[string]any
	if err := json.Unmarshal(got.payload, &fields); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if fields["scope"] != "pool:default-gvisor" || fields["lockId"] != "abc" {
		t.Errorf("payload = %s, missing fields", got.payload)
	}
	if r.FailedAppends() != 0 {
		t.Errorf("FailedAppends = %d, want 0", r.FailedAppends())
	}
}

// A zero event time is stamped from the wall clock so the durable row
// never carries a zero created_at.
func TestRecord_StampsZeroTime(t *testing.T) {
	cap := &captureAppender{}
	r := New(cap)
	r.Record("ops_health_status_changed", map[string]any{"current": "degraded"}, time.Time{})
	if len(cap.calls) != 1 {
		t.Fatalf("Append calls = %d, want 1", len(cap.calls))
	}
	if cap.calls[0].at.IsZero() {
		t.Error("Append at is zero; want a stamped wall-clock instant")
	}
}

// Degraded mode (no appender) logs the event and never panics; nothing is
// committed because there is no durable destination.
func TestRecord_DegradedLogsOnly(t *testing.T) {
	var logged int
	r := New(nil, WithLogger(func(string, ...any) { logged++ }))
	if r.Durable() {
		t.Fatal("Durable() = true with a nil appender")
	}
	r.Record("identity.discovered", map[string]any{"caller": "agent"}, time.Now())
	if logged != 1 {
		t.Errorf("log lines = %d, want 1", logged)
	}
	if r.FailedAppends() != 0 {
		t.Errorf("FailedAppends = %d, want 0 (degraded mode is not a failure)", r.FailedAppends())
	}
}

// A durable-append failure is logged, counted, and surfaced through the
// onError hook; it does not propagate so the originating operation
// continues. spec: §11.7 (audit-store outage must not halt the op).
func TestRecord_AppendFailureCountedAndHooked(t *testing.T) {
	cap := &captureAppender{failErr: errors.New("audit_log unreachable")}
	var hookEvent string
	var hookErr error
	var logged int
	r := New(cap,
		WithLogger(func(string, ...any) { logged++ }),
		WithOnError(func(ev string, err error) { hookEvent, hookErr = ev, err }),
	)
	r.Record("remediation.escalation_persisted", map[string]any{"id": "e1"}, time.Now())

	if r.FailedAppends() != 1 {
		t.Errorf("FailedAppends = %d, want 1", r.FailedAppends())
	}
	if logged != 1 {
		t.Errorf("log lines = %d, want 1", logged)
	}
	if hookEvent != "remediation.escalation_persisted" || hookErr == nil {
		t.Errorf("onError got (%q, %v), want the event + a non-nil error", hookEvent, hookErr)
	}
}

// WithTenant overrides the platform default so a test or a future
// scoped-event caller can target another chain.
func TestRecord_WithTenantOverride(t *testing.T) {
	cap := &captureAppender{}
	r := New(cap, WithTenant("acme"))
	r.Record("operations.inventory_queried", map[string]any{"kinds": 3}, time.Now())
	if cap.calls[0].tenant != "acme" {
		t.Errorf("tenant = %q, want acme", cap.calls[0].tenant)
	}
}

// A nil *Recorder is inert: Record and FailedAppends are safe no-ops so a
// caller that left the recorder unwired does not panic.
func TestRecord_NilRecorderSafe(t *testing.T) {
	var r *Recorder
	r.Record("remediation.lock_released", map[string]any{}, time.Now()) // must not panic
	if r.Durable() {
		t.Error("nil Recorder Durable() = true")
	}
	if r.FailedAppends() != 0 {
		t.Error("nil Recorder FailedAppends != 0")
	}
}

// An unmarshalable payload is counted as a failure rather than committed.
func TestRecord_MarshalFailureCounted(t *testing.T) {
	cap := &captureAppender{}
	r := New(cap)
	// A channel value cannot be JSON-marshalled.
	r.Record("ops_health_status_changed", map[string]any{"bad": make(chan int)}, time.Now())
	if len(cap.calls) != 0 {
		t.Errorf("Append called %d times on a marshal failure, want 0", len(cap.calls))
	}
	if r.FailedAppends() != 1 {
		t.Errorf("FailedAppends = %d, want 1", r.FailedAppends())
	}
}
