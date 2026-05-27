// SPDX-License-Identifier: MIT

package recover_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	recovermw "github.com/lennylabs/lenny/pkg/gateway/middleware/recover"
)

type captureLogger struct {
	lines []string
}

func (c *captureLogger) Printf(format string, args ...any) {
	c.lines = append(c.lines, fmt.Sprintf(format, args...))
}

// spec: §10.4 line 377 — a recovered panic must surface as a 500
// response instead of the net/http default of silent truncation.
// F-10.4.9.
func TestMiddlewareRecoversPanicAsInternalError_spec_10_4(t *testing.T) {
	logger := &captureLogger{}
	h := recovermw.MiddlewareWithLogger(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}), logger)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want 500", rec.Code)
	}
	body, _ := io.ReadAll(rec.Result().Body)
	if !strings.Contains(string(body), "INTERNAL_ERROR") {
		t.Errorf("body should carry INTERNAL_ERROR envelope, got %s", body)
	}
	if len(logger.lines) != 1 || !strings.Contains(logger.lines[0], "recovered handler panic") {
		t.Errorf("expected one panic log line, got %v", logger.lines)
	}
}

// spec: §10.4 line 377 — when the handler already wrote a status
// (e.g. an SSE stream that panicked mid-body), the middleware MUST
// NOT rewrite the header so the existing framing stays valid.
// F-10.4.9.
func TestMiddlewareDoesNotRewriteHeaderWhenInnerHandlerStartedBody_spec_10_4(t *testing.T) {
	h := recovermw.MiddlewareWithLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("mid-stream")
	}), &captureLogger{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/abc/events", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d want 200 (header already written before panic)", rec.Code)
	}
	body, _ := io.ReadAll(rec.Result().Body)
	if string(body) != "partial" {
		t.Errorf("body: got %q want \"partial\"", body)
	}
}

// http.ErrAbortHandler is the standard library's documented way to
// abort a handler silently without logging a recovered panic — e.g. a
// streaming handler that detected the client disconnected. The
// middleware must re-panic so the runtime closes the connection per
// net/http contract. F-10.4.9.
func TestMiddlewareRePanicsOnErrAbortHandler_spec_10_4(t *testing.T) {
	defer func() {
		rec := recover()
		if rec != http.ErrAbortHandler {
			t.Errorf("expected re-panic with http.ErrAbortHandler, got %v", rec)
		}
	}()
	h := recovermw.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	h.ServeHTTP(rec, req)
}

// Happy-path: a handler that returns cleanly is unaffected by the
// middleware.
func TestMiddlewarePassesThroughCleanRequest_spec_10_4(t *testing.T) {
	logger := &captureLogger{}
	h := recovermw.MiddlewareWithLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ok"))
	}), logger)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status: got %d want 202", rec.Code)
	}
	if len(logger.lines) != 0 {
		t.Errorf("logger should be silent for a clean request, got %v", logger.lines)
	}
}
