// SPDX-License-Identifier: MIT

package loadctl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestHubFanOut(t *testing.T) {
	h := NewHub()
	defer h.Close("r1")
	ch1, _, unsub1 := h.subscribe("r1")
	ch2, _, unsub2 := h.subscribe("r1")
	defer unsub1()
	defer unsub2()
	h.Publish("r1", Event{Type: "status", Payload: "RUNNING"})

	expect := func(c <-chan Event, label string) {
		select {
		case e := <-c:
			if e.Type != "status" || e.Payload != "RUNNING" {
				t.Errorf("%s: unexpected event %+v", label, e)
			}
		case <-time.After(time.Second):
			t.Errorf("%s: timeout waiting for event", label)
		}
	}
	expect(ch1, "sub1")
	expect(ch2, "sub2")
}

func TestHubReplaysBacklogToLateJoiner(t *testing.T) {
	h := NewHub()
	defer h.Close("r2")
	h.Publish("r2", Event{Type: "status", Payload: "PENDING"})
	h.Publish("r2", Event{Type: "status", Payload: "RUNNING"})
	_, backlog, unsub := h.subscribe("r2")
	defer unsub()
	if len(backlog) != 2 {
		t.Errorf("backlog=%d want 2", len(backlog))
	}
}

func TestHubCloseTerminatesSubscribers(t *testing.T) {
	h := NewHub()
	ch, _, unsub := h.subscribe("r3")
	defer unsub()
	h.Close("r3")
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel closed signal")
		}
	case <-time.After(time.Second):
		t.Error("subscriber did not unblock on Close")
	}
}

func TestServeWebSocketDeliversEvent(t *testing.T) {
	h := NewHub()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeWebSocket(w, r, "r4")
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Give the server time to register the subscription.
	time.Sleep(50 * time.Millisecond)
	h.Publish("r4", Event{Type: "metric", Payload: 42.0})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var e Event
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if e.Type != "metric" {
		t.Errorf("unexpected event: %+v", e)
	}
}
