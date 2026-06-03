// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gwevents "github.com/lennylabs/lenny/pkg/gateway/events"
	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// flushRecorder is a flushable ResponseRecorder so the SSE handler does
// not bail out with "streaming unsupported".
type flushRecorder struct{ *httptest.ResponseRecorder }

func (flushRecorder) Flush() {}

// spec: §25.5 lines 2562-2563, §25.17 Step 1 — the lenny-ops mux serves
// GET /v1/admin/events/stream (SSE) and GET /v1/admin/events (polling)
// when an EventStream service is wired. Closes F-25.5.2 / F-25.5.3 /
// F-25.17.1 at the route-registration boundary.
func TestEventStreamRoutesRegistered_spec_25_5_2562(t *testing.T) {
	stream := opsstream.New(opsstream.Options{})
	stream.Publish(context.Background(), gwevents.OperationalEvent{Type: "alert_fired"})
	srv := opsserver.New(opsserver.Options{EventStream: stream})

	// Polling endpoint returns the canonical envelope.
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("poll status = %d, want 200", rec.Code)
	}
	var page opsstream.EventPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode poll envelope: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("poll items = %d, want 1", len(page.Items))
	}
	if page.Pagination.CursorKind != "buffer" {
		t.Errorf("cursorKind = %q, want buffer", page.Pagination.CursorKind)
	}

	// SSE endpoint emits a text/event-stream backlog frame.
	sse := flushRecorder{httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/events/stream", nil)
	ctx, cancel := context.WithCancel(req.Context())
	go func() { cancel() }()
	srv.ServeHTTP(sse, req.WithContext(ctx))
	if ct := sse.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("SSE Content-Type = %q, want text/event-stream", ct)
	}
}

// spec: §25.5 — without an EventStream service the routes stay unmapped
// so the mux returns 404 rather than panicking.
func TestEventStreamRoutesAbsentWhenUnwired(t *testing.T) {
	srv := opsserver.New(opsserver.Options{})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/events", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when EventStream is nil", rec.Code)
	}
}
