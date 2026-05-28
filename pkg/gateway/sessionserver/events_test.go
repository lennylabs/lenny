// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/pagination"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// paginationMint is a one-liner around pagination.MintCursor so test
// callers can stay typo-free without importing the package directly in
// every helper.
func paginationMint(field, direction, key, tiebreak string, issued time.Time) string {
	return pagination.MintCursor(pagination.Sort{Field: field, Direction: direction},
		key, tiebreak, issued)
}

// spec: §15.1 GET /v1/sessions/{id}/events SSE stream.

func TestEventsStreamReceivesMessageEvents(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	srv := sessionserver.New(store, sessionserver.Options{
		Executor: executor.NewEchoExecutor(),
		Events:   bus,
	})
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_ev", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Open the SSE stream.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/sessions/sess_ev/events", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE status: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type: %q", ct)
	}

	// Publish events by injecting a message in a goroutine.
	go func() {
		time.Sleep(50 * time.Millisecond)
		bus.Publish("sess_ev", "message_delivered", `{"content":"hello"}`, time.Now())
		bus.Publish("sess_ev", "response", `{"text":"echo hello"}`, time.Now())
	}()

	// Read SSE frames until we see the response event.
	scanner := bufio.NewScanner(resp.Body)
	sawMessage, sawResponse := false, false
	deadline := time.After(3 * time.Second)
	lines := make(chan string, 64)
	go func() {
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	for !sawResponse {
		select {
		case <-deadline:
			t.Fatalf("timed out; sawMessage=%v sawResponse=%v", sawMessage, sawResponse)
		case line, ok := <-lines:
			if !ok {
				t.Fatal("SSE stream closed early")
			}
			if strings.Contains(line, "message_delivered") {
				sawMessage = true
			}
			if strings.Contains(line, "event: response") {
				sawResponse = true
			}
		}
	}
	if !sawMessage {
		t.Error("did not observe the message_delivered event")
	}
}

func TestEventsStreamMissingSession(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{Events: sessionevents.NewBus(0)})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_missing/events", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing session: got %d, want 404", rr.Code)
	}
}

func TestEventsStreamUnavailableWhenBusUnwired(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_x", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	srv := sessionserver.New(store, sessionserver.Options{}) // no Events bus
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_x/events", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("no event bus: got %d, want 503", rr.Code)
	}
}

func TestEventsStreamReplaysBacklogWithCursor(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	srv := sessionserver.New(store, sessionserver.Options{Events: bus})
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_bk", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	// Pre-publish 3 events.
	bus.Publish("sess_bk", "e1", `{}`, now)
	bus.Publish("sess_bk", "e2", `{}`, now)
	bus.Publish("sess_bk", "e3", `{}`, now)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	// Reconnect with Last-Event-ID: 1 → backlog replays e2, e3.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/sessions/sess_bk/events", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Last-Event-ID", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	seen := map[string]bool{}
	lines := make(chan string, 64)
	go func() {
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	deadline := time.After(3 * time.Second)
	for !seen["e3"] {
		select {
		case <-deadline:
			t.Fatalf("backlog not fully replayed: %v", seen)
		case line, ok := <-lines:
			if !ok {
				t.Fatal("stream closed early")
			}
			if strings.Contains(line, "event: e2") {
				seen["e2"] = true
			}
			if strings.Contains(line, "event: e3") {
				seen["e3"] = true
			}
			if strings.Contains(line, "event: e1") {
				t.Error("e1 should NOT be replayed (cursor was 1)")
			}
		}
	}
}

// spec: §7.2 line 143 + lines 349-361 — when the client's cursor falls
// below the oldest retained event, the SSE stream emits gap_detected
// AND checkpoint_boundary markers ahead of the backlog so the client
// can render a gap warning and count of events lost.
func TestEventsStreamEmitsGapAndCheckpointMarkers_spec_7_2(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(3) // small replay buffer
	srv := sessionserver.New(store, sessionserver.Options{Events: bus})
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_gap", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	// Publish 10 events; maxHistory=3 keeps seqs 8,9,10.
	for i := 0; i < 10; i++ {
		bus.Publish("sess_gap", "e", `{}`, now)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	// Reconnect with cursor=2 → oldest retained is 8, so 5 events lost (3..7).
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/sessions/sess_gap/events", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Last-Event-ID", "2")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	lines := make(chan string, 64)
	go func() {
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	deadline := time.After(3 * time.Second)
	var sawGap, sawGapData, sawCheckpoint, sawCheckpointData bool
	for !(sawGap && sawGapData && sawCheckpoint && sawCheckpointData) {
		select {
		case <-deadline:
			t.Fatalf("gap markers not observed: gap=%v gapData=%v cb=%v cbData=%v",
				sawGap, sawGapData, sawCheckpoint, sawCheckpointData)
		case line, ok := <-lines:
			if !ok {
				t.Fatal("stream closed early")
			}
			switch {
			case line == "event: gap_detected":
				sawGap = true
			case sawGap && !sawGapData && strings.HasPrefix(line, "data: "):
				if !strings.Contains(line, `"lastSeenSeq":2`) || !strings.Contains(line, `"nextSeq":8`) {
					t.Errorf("gap_detected payload: %q", line)
				}
				sawGapData = true
			case line == "event: checkpoint_boundary":
				sawCheckpoint = true
			case sawCheckpoint && !sawCheckpointData && strings.HasPrefix(line, "data: "):
				if !strings.Contains(line, `"events_lost":5`) ||
					!strings.Contains(line, `"reason":"replay_window_exceeded"`) ||
					!strings.Contains(line, `"cursor":8`) ||
					!strings.Contains(line, `"checkpoint_timestamp":`) {
					t.Errorf("checkpoint_boundary payload: %q", line)
				}
				sawCheckpointData = true
			}
		}
	}
}

// spec: §7.2 line 143 — no gap markers when the cursor is current
// (within the retained window).
func TestEventsStreamOmitsGapMarkersWhenCursorWithinBuffer_spec_7_2(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(10)
	srv := sessionserver.New(store, sessionserver.Options{Events: bus})
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_ok", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	for i := 0; i < 5; i++ {
		bus.Publish("sess_ok", "e", `{}`, now)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/sessions/sess_ok/events", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Last-Event-ID", "2")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	lines := make(chan string, 64)
	go func() {
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	// Drain a few lines; ensure no gap_detected / checkpoint_boundary appears.
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case <-deadline:
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			if strings.Contains(line, "gap_detected") || strings.Contains(line, "checkpoint_boundary") {
				t.Errorf("unexpected gap marker on in-buffer cursor: %q", line)
			}
		}
	}
}

// spec: §7.2 line 143 — a fresh connect (cursor=0) is never a gap.
func TestEventsStreamOmitsGapMarkersOnFreshConnect_spec_7_2(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(3)
	srv := sessionserver.New(store, sessionserver.Options{Events: bus})
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_fresh", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	for i := 0; i < 10; i++ {
		bus.Publish("sess_fresh", "e", `{}`, now)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	// No Last-Event-ID header → fresh connect.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/sessions/sess_fresh/events", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	lines := make(chan string, 64)
	go func() {
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case <-deadline:
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			if strings.Contains(line, "gap_detected") || strings.Contains(line, "checkpoint_boundary") {
				t.Errorf("fresh connect emitted gap marker: %q", line)
			}
		}
	}
}

// TestEventsJSONListReturnsCanonicalEnvelope_spec_15_1_1228 confirms
// that `Accept: application/json` against /v1/sessions/{id}/events
// returns the §15.1 line 1228 canonical envelope rather than the SSE
// stream. F-15.1.23.
func TestEventsJSONListReturnsCanonicalEnvelope_spec_15_1_1228(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(16)
	srv := sessionserver.New(store, sessionserver.Options{Events: bus})
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_json", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	for i := 0; i < 5; i++ {
		bus.PublishForTenant("acme", "sess_json", "response", `{"text":"x"}`, now)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_json/events", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type %q, want application/json", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"items"`) || !strings.Contains(body, `"hasMore"`) {
		t.Errorf("body missing canonical envelope keys: %s", body)
	}
	if strings.Contains(body, "event:") {
		t.Errorf("body looks like SSE: %s", body)
	}
}

// TestEventsJSONListPaginates_spec_15_1_1253 verifies cursor-based
// pagination on the JSON list view including the canonical `cursor`
// pivot. F-15.1.23 + F-15.1.20.
func TestEventsJSONListPaginates_spec_15_1_1253(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(16)
	srv := sessionserver.New(store, sessionserver.Options{Events: bus})
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_pag", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	for i := 0; i < 7; i++ {
		bus.PublishForTenant("acme", "sess_pag", "response", `{}`, now)
	}

	get := func(query string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_pag/events"+query, nil)
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("Accept", "application/json")
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		return rr.Code, rr.Body.String()
	}

	code, body := get("?limit=3")
	if code != http.StatusOK {
		t.Fatalf("page 1: %d %s", code, body)
	}
	if !strings.Contains(body, `"hasMore":true`) {
		t.Errorf("page 1 hasMore: %s", body)
	}
	if !strings.Contains(body, `"cursor"`) {
		t.Errorf("page 1 cursor missing: %s", body)
	}
}

// TestEventsJSONListRejectsExpiredCursor_spec_15_1_1253 confirms the
// shared §15.1 line 1253 24h cursor TTL applies on the JSON list path.
func TestEventsJSONListRejectsExpiredCursor_spec_15_1_1253(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(8)
	fixed := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	srv := sessionserver.New(store, sessionserver.Options{
		Events: bus,
		Clock:  func() time.Time { return fixed },
	})
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_exp", TenantID: "acme", State: session.StateRunning,
		CreatedAt: fixed, UpdatedAt: fixed,
	})
	bus.PublishForTenant("acme", "sess_exp", "response", `{}`, fixed)

	expired := paginationMint("seq", "asc", "1", "1", fixed.Add(-25*time.Hour))

	req := httptest.NewRequest(http.MethodGet,
		"/v1/sessions/sess_exp/events?cursor="+expired, nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expired cursor: %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "cursor_expired") {
		t.Errorf("expired cursor: missing cursor_expired rule: %s", rr.Body.String())
	}
}
