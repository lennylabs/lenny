// SPDX-License-Identifier: MIT

package correlation

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/observability/correlation"
	"github.com/lennylabs/lenny/pkg/observability/logging"
)

func jsonLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(logging.NewJSONHandler(buf, logging.Options{DefaultComponent: "gateway"}))
}

func lastLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
		t.Fatalf("log line is not JSON: %v\nline: %s", err, lines[len(lines)-1])
	}
	return m
}

// spec: §16.4 line 372 / §25.4 — the middleware reads X-Lenny-Operation-ID
// and X-Lenny-Agent-Name and makes them visible on the request context to
// downstream handlers. F-16.4.3.
func TestWrapReadsOperationAndAgentHeadersIntoContext_spec_16_4_372(t *testing.T) {
	var gotOp, gotAgent string
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		f := correlation.From(r.Context())
		gotOp = f.OperationID
		gotAgent = f.AgentName
	})
	h := Wrap(inner, Options{Logger: jsonLogger(&bytes.Buffer{})})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set(correlation.HeaderOperationID, "op-123")
	req.Header.Set(correlation.HeaderAgentName, "alice-agent")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotOp != "op-123" {
		t.Errorf("operation_id in context = %q, want op-123", gotOp)
	}
	if gotAgent != "alice-agent" {
		t.Errorf("agent_name in context = %q, want alice-agent", gotAgent)
	}
}

// spec: §16.4 line 372 — the correlation fields appear on the structured
// request-completion log line. F-16.4.2.
func TestWrapEmitsCorrelationFieldsOnLogLine_spec_16_4_372(t *testing.T) {
	var buf bytes.Buffer
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}), Options{Logger: jsonLogger(&buf)})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	req.Header.Set(correlation.HeaderOperationID, "op-7")
	req.Header.Set(correlation.HeaderAgentName, "bob-agent")
	req.Header.Set(correlation.HeaderSessionID, "sess-9")
	req.Header.Set(correlation.HeaderTenantID, "acme")
	req.Header.Set(correlation.HeaderTraceParent, "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	h.ServeHTTP(httptest.NewRecorder(), req)

	rec := lastLine(t, &buf)
	cases := map[string]string{
		"msg":          "http_request",
		"method":       "POST",
		"path":         "/v1/sessions",
		"operation_id": "op-7",
		"agent_name":   "bob-agent",
		"session_id":   "sess-9",
		"tenant_id":    "acme",
		"trace_id":     "0123456789abcdef0123456789abcdef",
		"span_id":      "0123456789abcdef",
		"component":    "gateway",
	}
	for k, want := range cases {
		if got, _ := rec[k].(string); got != want {
			t.Errorf("log field %q = %v, want %q", k, rec[k], want)
		}
	}
	if status, ok := rec["status"].(float64); !ok || int(status) != http.StatusCreated {
		t.Errorf("log status = %v, want 201", rec["status"])
	}
}

// An implicit 200 (handler writes a body without calling WriteHeader) is
// captured as 200 on the completion line.
func TestWrapDefaultsStatusTo200(t *testing.T) {
	var buf bytes.Buffer
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}), Options{Logger: jsonLogger(&buf)})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	rec := lastLine(t, &buf)
	if status, ok := rec["status"].(float64); !ok || int(status) != http.StatusOK {
		t.Errorf("implicit status = %v, want 200", rec["status"])
	}
	if b, ok := rec["bytes"].(float64); !ok || int(b) != 2 {
		t.Errorf("bytes = %v, want 2", rec["bytes"])
	}
}

// Probe paths in SkipPaths attach the context but emit no completion line.
func TestWrapSkipsProbePaths(t *testing.T) {
	var buf bytes.Buffer
	seen := false
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = correlation.From(r.Context()).OperationID == "op-skip"
		w.WriteHeader(http.StatusOK)
	}), Options{Logger: jsonLogger(&buf), SkipPaths: map[string]bool{"/healthz": true}})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set(correlation.HeaderOperationID, "op-skip")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if buf.Len() != 0 {
		t.Errorf("probe path must not emit an access log line, got %q", buf.String())
	}
	if !seen {
		t.Errorf("correlation context must still be attached for skipped paths")
	}
}

// flushRecorder is an httptest.ResponseRecorder that also records Flush
// calls, so the test can assert the wrapper forwards Flush for SSE.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed int
}

func (f *flushRecorder) Flush() { f.flushed++ }

// spec: §15.1 SSE — the outermost wrapper must forward Flush so streaming
// handlers keep working through it.
func TestCaptureRWForwardsFlush(t *testing.T) {
	fr := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("captureRW must implement http.Flusher")
		}
		f.Flush()
		f.Flush()
	}), Options{Logger: jsonLogger(&bytes.Buffer{})})
	h.ServeHTTP(fr, httptest.NewRequest(http.MethodGet, "/v1/sessions/x/events", nil))

	if fr.flushed != 2 {
		t.Errorf("Flush forwarded %d times, want 2", fr.flushed)
	}
}

// Unwrap returns the wrapped writer so net/http's ResponseController can
// traverse this layer.
func TestCaptureRWUnwrap(t *testing.T) {
	base := httptest.NewRecorder()
	cw := &captureRW{ResponseWriter: base}
	if cw.Unwrap() != base {
		t.Errorf("Unwrap did not return the wrapped writer")
	}
}

// An inbound traceparent already on the context survives when the request
// carries no header (Merge does not clobber with empty values).
func TestWrapMergePreservesExistingContextFields(t *testing.T) {
	var buf bytes.Buffer
	var gotTenant string
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotTenant = correlation.From(r.Context()).TenantID
	})
	h := Wrap(inner, Options{Logger: jsonLogger(&buf)})

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	// Seed a tenant on the context; the request sets no tenant header.
	req = req.WithContext(correlation.With(req.Context(), correlation.Fields{TenantID: "globex"}))
	req.Header.Set(correlation.HeaderOperationID, "op-merge")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotTenant != "globex" {
		t.Errorf("pre-seeded tenant_id = %q, want globex (Merge clobbered it)", gotTenant)
	}
}

// spec: §16.4 line 375 — an error response carries error_code,
// error_category, and retryable on the completion line, classified through
// the shared §15.2.1 classifier so a SIEM can bin the line by category.
// F-16.4.12.
func TestWrapEmitsErrorCategoryOnLogLine_spec_16_4_375(t *testing.T) {
	var buf bytes.Buffer
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"CIRCUIT_BREAKER_OPEN","message":"open"}}`))
	}), Options{Logger: jsonLogger(&buf)})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/sessions", nil))

	rec := lastLine(t, &buf)
	if got, _ := rec["error_code"].(string); got != "CIRCUIT_BREAKER_OPEN" {
		t.Errorf("error_code = %v, want CIRCUIT_BREAKER_OPEN", rec["error_code"])
	}
	// CIRCUIT_BREAKER_OPEN classifies as POLICY / not retryable.
	if got, _ := rec["error_category"].(string); got != "POLICY" {
		t.Errorf("error_category = %v, want POLICY", rec["error_category"])
	}
	if got, ok := rec["retryable"].(bool); !ok || got {
		t.Errorf("retryable = %v, want false", rec["retryable"])
	}
}

// The classifier is the source of truth: a bare middleware envelope that
// omits the category from the body still produces the correct category on
// the log line. spec: §16.4 line 375. F-16.4.12.
func TestWrapClassifiesCodeWhenBodyOmitsCategory_spec_16_4_375(t *testing.T) {
	var buf bytes.Buffer
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Mirrors pkg/gateway/middleware/environment / circuitbreaker, which
		// write {error:{code,message}} with no category field.
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"RATE_LIMITED","message":"slow down"}}`))
	}), Options{Logger: jsonLogger(&buf)})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/sessions", nil))

	rec := lastLine(t, &buf)
	if got, _ := rec["error_category"].(string); got != "POLICY" {
		t.Errorf("error_category = %v, want POLICY (classifier, not body)", rec["error_category"])
	}
	if got, ok := rec["retryable"].(bool); !ok || !got {
		t.Errorf("retryable = %v, want true (RATE_LIMITED is retryable)", rec["retryable"])
	}
}

// A 2xx response carries no error fields on the completion line.
// spec: §16.4 line 375. F-16.4.12.
func TestWrapOmitsErrorFieldsOnSuccess_spec_16_4_375(t *testing.T) {
	var buf bytes.Buffer
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		// A 200 body that happens to contain an "error" key must not be
		// mined for a category — only error statuses are sniffed.
		_, _ = w.Write([]byte(`{"error":{"code":"VALIDATION_ERROR"}}`))
	}), Options{Logger: jsonLogger(&buf)})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	rec := lastLine(t, &buf)
	for _, k := range []string{"error_code", "error_category", "retryable"} {
		if _, ok := rec[k]; ok {
			t.Errorf("success line carries %q = %v, want absent", k, rec[k])
		}
	}
}

// A non-JSON error body (a plaintext 500, a streamed dump) leaves the error
// fields absent rather than emitting a garbage code. spec: §16.4 line 375.
func TestWrapOmitsErrorFieldsOnNonEnvelopeBody_spec_16_4_375(t *testing.T) {
	var buf bytes.Buffer
	h := Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal failure\n"))
	}), Options{Logger: jsonLogger(&buf)})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/sessions", nil))

	rec := lastLine(t, &buf)
	if _, ok := rec["error_code"]; ok {
		t.Errorf("non-envelope 500 carries error_code = %v, want absent", rec["error_code"])
	}
	if status, ok := rec["status"].(float64); !ok || int(status) != http.StatusInternalServerError {
		t.Errorf("status = %v, want 500", rec["status"])
	}
}
