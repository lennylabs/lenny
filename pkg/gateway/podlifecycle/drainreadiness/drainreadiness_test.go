// SPDX-License-Identifier: MIT

package drainreadiness_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/drainreadiness"
)

// spec: §12.5 — the GET /internal/drain-readiness endpoint runs a MinIO
// liveness probe and reports whether a node drain may proceed.

func get(h *drainreadiness.Handler) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/internal/drain-readiness", nil))
	return rr
}

func TestReadyWhenProbeSucceeds(t *testing.T) {
	h := &drainreadiness.Handler{
		Prober: drainreadiness.ProberFunc(func(context.Context) error { return nil }),
	}
	rr := get(h)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ready" || body["minio"] != "healthy" {
		t.Errorf("body = %v, want the §12.5 ready response", body)
	}
}

func TestNotReadyWhenProbeFails(t *testing.T) {
	h := &drainreadiness.Handler{
		Prober: drainreadiness.ProberFunc(func(context.Context) error {
			return errors.New("HeadBucket timed out")
		}),
	}
	rr := get(h)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "not_ready" || body["minio"] != "unhealthy" {
		t.Errorf("body = %v, want the §12.5 not-ready response", body)
	}
	if body["reason"] != "HeadBucket timed out" {
		t.Errorf("reason = %q, want the probe error", body["reason"])
	}
}

func TestRejectsNonGet(t *testing.T) {
	h := &drainreadiness.Handler{
		Prober: drainreadiness.ProberFunc(func(context.Context) error { return nil }),
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/internal/drain-readiness", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

func TestProbeReceivesABoundedContext(t *testing.T) {
	var hadDeadline bool
	h := &drainreadiness.Handler{
		Timeout: 50 * time.Millisecond,
		Prober: drainreadiness.ProberFunc(func(ctx context.Context) error {
			_, hadDeadline = ctx.Deadline()
			return nil
		}),
	}
	if rr := get(h); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !hadDeadline {
		t.Error("the probe context carried no deadline — the §12.5 probe timeout was not applied")
	}
}

func TestSlowProbeTimesOutAsNotReady(t *testing.T) {
	h := &drainreadiness.Handler{
		Timeout: 20 * time.Millisecond,
		Prober: drainreadiness.ProberFunc(func(ctx context.Context) error {
			<-ctx.Done() // a hung artifact store
			return ctx.Err()
		}),
	}
	rr := get(h)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 for a probe that exceeds the timeout", rr.Code)
	}
}

// recordingAppender is a minimal AuditAppender fake.
type recordingAppender struct {
	rows []rec
	err  error
}

type rec struct {
	tenantID  string
	eventType string
	payload   string
}

func (r *recordingAppender) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, _ time.Time) (audit.Row, error) {
	if r.err != nil {
		return audit.Row{}, r.err
	}
	r.rows = append(r.rows, rec{tenantID: tenantID, eventType: eventType, payload: string(payload)})
	return audit.Row{}, nil
}

type recordingDrainMetrics struct{ outcomes []string }

func (r *recordingDrainMetrics) IncDrainReadinessCheck(outcome string) {
	r.outcomes = append(r.outcomes, outcome)
}

// TestForcedDrainHandlerAppendsAuditEvent verifies the POST endpoint
// appends a §16.7 node.drain.forced row to the §11.7 chain.
//
// spec: §12.5 line 291; §16.7 node.drain.forced.
func TestForcedDrainHandlerAppendsAuditEvent(t *testing.T) {
	app := &recordingAppender{}
	m := &recordingDrainMetrics{}
	h := &drainreadiness.ForcedDrainHandler{
		Appender:       app,
		Metrics:        m,
		PlatformTenant: "platform",
		Clock:          func() time.Time { return time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC) },
	}
	body := `{"tenant":"","podNamespace":"lenny-agents","podName":"agent-pod","nodeName":"node-1"}`
	req := httptest.NewRequest(http.MethodPost, "/internal/audit/node-drain-forced", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%q", rr.Code, rr.Body.String())
	}
	if len(app.rows) != 1 {
		t.Fatalf("appender rows = %d, want 1", len(app.rows))
	}
	row := app.rows[0]
	if row.eventType != "node.drain.forced" {
		t.Errorf("eventType = %q, want node.drain.forced", row.eventType)
	}
	if row.tenantID != "platform" {
		t.Errorf("tenantID = %q, want the platform fallback", row.tenantID)
	}
	if !strings.Contains(row.payload, `"pod_name":"agent-pod"`) || !strings.Contains(row.payload, `"node_name":"node-1"`) {
		t.Errorf("payload = %q, want the §16.7 fields", row.payload)
	}
	if len(m.outcomes) != 1 || m.outcomes[0] != "forced_audited" {
		t.Errorf("outcomes = %v, want [forced_audited]", m.outcomes)
	}
}

// TestForcedDrainHandlerReportsAppendFailure returns 503 when the
// audit chain is unreachable so the webhook can deny the eviction
// fail-closed per §11.7.
func TestForcedDrainHandlerReportsAppendFailure(t *testing.T) {
	app := &recordingAppender{err: errors.New("chain down")}
	m := &recordingDrainMetrics{}
	h := &drainreadiness.ForcedDrainHandler{Appender: app, Metrics: m}
	body := `{"podNamespace":"lenny-agents","podName":"agent-pod","nodeName":"node-1"}`
	req := httptest.NewRequest(http.MethodPost, "/internal/audit/node-drain-forced", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	if len(m.outcomes) != 1 || m.outcomes[0] != "audit_failed" {
		t.Errorf("outcomes = %v, want [audit_failed]", m.outcomes)
	}
}

// TestForcedDrainHandlerRejectsMissingFields validates that the
// handler refuses a body that omits podName or nodeName.
func TestForcedDrainHandlerRejectsMissingFields(t *testing.T) {
	h := &drainreadiness.ForcedDrainHandler{Appender: &recordingAppender{}}
	body := `{"podNamespace":"lenny-agents"}`
	req := httptest.NewRequest(http.MethodPost, "/internal/audit/node-drain-forced", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestForcedDrainHandlerRejectsGET(t *testing.T) {
	h := &drainreadiness.ForcedDrainHandler{Appender: &recordingAppender{}}
	req := httptest.NewRequest(http.MethodGet, "/internal/audit/node-drain-forced", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}
