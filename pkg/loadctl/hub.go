// SPDX-License-Identifier: MIT

package loadctl

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

// Hub is the WebSocket telemetry hub. Each run-id has a fan-out
// channel; subscribers join via ServeWebSocket and receive every
// Event the run publishes through Publish until the hub closes the
// run.
type Hub struct {
	mu       sync.Mutex
	channels map[string]*runChannel
}

type runChannel struct {
	mu          sync.Mutex
	subscribers []chan Event
	closed      bool
	backlog     []Event
}

// Event is one telemetry payload.
type Event struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// NewHub returns an empty Hub.
func NewHub() *Hub {
	return &Hub{channels: make(map[string]*runChannel)}
}

// Publish emits e to every subscriber on runID and retains it in
// the run's backlog so a late subscriber receives the recent history
// on join (bounded to the last 64 events per run).
func (h *Hub) Publish(runID string, e Event) {
	ch := h.openChannel(runID)
	ch.mu.Lock()
	if ch.closed {
		ch.mu.Unlock()
		return
	}
	if len(ch.backlog) >= 64 {
		ch.backlog = ch.backlog[1:]
	}
	ch.backlog = append(ch.backlog, e)
	subs := make([]chan Event, len(ch.subscribers))
	copy(subs, ch.subscribers)
	ch.mu.Unlock()
	for _, s := range subs {
		select {
		case s <- e:
		default:
			// Slow subscriber: drop the event rather than block the
			// hub. The subscriber catches up via the backlog on
			// reconnect.
		}
	}
}

// Close marks the run channel terminal. Subscribers receive a
// final close frame and the channel is removed from the hub.
func (h *Hub) Close(runID string) {
	h.mu.Lock()
	ch, ok := h.channels[runID]
	if ok {
		delete(h.channels, runID)
	}
	h.mu.Unlock()
	if ch == nil {
		return
	}
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.closed = true
	for _, s := range ch.subscribers {
		close(s)
	}
	ch.subscribers = nil
}

func (h *Hub) openChannel(runID string) *runChannel {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch, ok := h.channels[runID]
	if !ok {
		ch = &runChannel{}
		h.channels[runID] = ch
	}
	return ch
}

func (h *Hub) subscribe(runID string) (<-chan Event, []Event, func()) {
	ch := h.openChannel(runID)
	ch.mu.Lock()
	defer ch.mu.Unlock()
	out := make(chan Event, 16)
	if ch.closed {
		close(out)
		return out, append([]Event{}, ch.backlog...), func() {}
	}
	ch.subscribers = append(ch.subscribers, out)
	backlog := append([]Event{}, ch.backlog...)
	unsub := func() {
		ch.mu.Lock()
		defer ch.mu.Unlock()
		for i, s := range ch.subscribers {
			if s == out {
				ch.subscribers = append(ch.subscribers[:i], ch.subscribers[i+1:]...)
				return
			}
		}
	}
	return out, backlog, unsub
}

// ServeWebSocket handles a /api/v1/runs/{id}/metrics:stream upgrade.
func (h *Hub) ServeWebSocket(w http.ResponseWriter, r *http.Request, runID string) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	events, backlog, unsub := h.subscribe(runID)
	defer unsub()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Push the backlog so late joiners see recent history.
	for _, e := range backlog {
		if err := writeJSONMessage(ctx, conn, e); err != nil {
			return
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-events:
			if !ok {
				_ = writeJSONMessage(ctx, conn, Event{Type: "end"})
				return
			}
			if err := writeJSONMessage(ctx, conn, e); err != nil {
				return
			}
		}
	}
}

func writeJSONMessage(ctx context.Context, conn *websocket.Conn, e Event) error {
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, body)
}
