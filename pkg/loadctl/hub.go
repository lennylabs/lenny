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

// runChannel is the fan-out state for one run id.
//
// mu protects every field below it, and it also owns the subscriber
// channels themselves: a send on a channel in subscribers, and the
// close of that channel, both happen with mu held. That is what makes
// the closed guard meaningful. Publishing a run's event and marking
// the run terminal arrive on separate HTTP handlers (progress and ack)
// with no ordering between them, so a Publish that checked closed and
// then sent with mu released could have its subscriber closed out from
// under the in-flight send by a concurrent Close, which is a data race
// on the channel and a "send on closed channel" panic once the timing
// is unlucky.
//
// Holding mu across the sends is bounded work: every send is
// non-blocking (select with a default arm), so a slow subscriber
// cannot stall the hub while the lock is held. Keeping the backlog
// append and the fan-out under one critical section also fixes the
// order two concurrent publishers are observed in: subscribers see
// events in the order they entered the backlog.
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
	defer ch.mu.Unlock()
	if ch.closed {
		// The run is terminal. Publishing into it is a no-op.
		return
	}
	if len(ch.backlog) >= 64 {
		ch.backlog = ch.backlog[1:]
	}
	ch.backlog = append(ch.backlog, e)
	for _, s := range ch.subscribers {
		select {
		case s <- e:
		default:
			// Slow subscriber: drop the event rather than block the
			// hub. The subscriber catches up via the backlog on
			// reconnect.
		}
	}
}

// CloseAll terminates every active run channel and removes them
// from the hub. Used by Server.Shutdown so SIGTERM produces clean
// close frames on every live WebSocket subscriber.
func (h *Hub) CloseAll() {
	h.mu.Lock()
	ids := make([]string, 0, len(h.channels))
	for id := range h.channels {
		ids = append(ids, id)
	}
	h.mu.Unlock()
	for _, id := range ids {
		h.Close(id)
	}
}

// Close marks the run channel terminal. Subscribers receive a
// final close frame and the channel is removed from the hub.
//
// The subscriber channels are closed with the run channel's mu held,
// so a Publish racing this call either completes its fan-out before
// the close or observes closed and returns without sending.
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

// SubscribeForTest exposes the internal subscribe helper so tests
// can collect every Event a run emits without going through the
// WebSocket layer. Returns the live channel, the backlog snapshot,
// and an unsubscribe func.
func (h *Hub) SubscribeForTest(runID string) (<-chan Event, []Event, func()) {
	return h.subscribe(runID)
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
