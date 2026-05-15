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
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §15.1 GET /v1/sessions/{id}/events SSE stream.

func TestEventsStreamReceivesMessageEvents(t *testing.T) {
	store := memstore.New()
	bus := events.NewBus(0)
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
	srv := sessionserver.New(store, sessionserver.Options{Events: events.NewBus(0)})
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
	bus := events.NewBus(0)
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
