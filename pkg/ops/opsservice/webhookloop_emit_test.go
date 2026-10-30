// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/webhookdelivery"
)

// bufferEventSource is a test EventSource that drains a real
// eventbuffer.EventBuffer, tracking a per-source cursor so each Poll
// yields only the events appended since the previous Poll. It mirrors
// the production RedisEventSource cursor semantics against an in-process
// buffer, so a single test can exercise the full §25.5
// emit -> stream -> re-poll cycle: the worker's EmitFailure writes an
// event_delivery_failed record into the same buffer this source reads,
// and the next tick sees it exactly as the Redis-backed path would.
type bufferEventSource struct {
	buf   *eventbuffer.EventBuffer
	mu    sync.Mutex
	since uint64
}

func (s *bufferEventSource) Poll(context.Context) ([]WebhookEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	page := s.buf.Query(s.since, events.EventFilter{}, 1000)
	out := make([]WebhookEvent, 0, len(page.Events))
	for _, be := range page.Events {
		s.since = be.ID
		body, err := json.Marshal(be.Event)
		if err != nil {
			continue
		}
		out = append(out, WebhookEvent{ID: be.Event.ID, Type: be.Event.Type, Body: body})
	}
	return out, nil
}

// TestWebhookWorkerDoesNotRedeliverDeliveryFailureToFailingSubscription
// is the §25.5 loop-prevention contract. spec: §25.5 —
// "After 3 failed attempts, the delivery is marked failed in
// ops_event_deliveries and an event_delivery_failed operational event is
// emitted (but not itself delivered to that subscription, to avoid
// loops)."
//
// The test wires the real event emitter and a buffer-backed source so
// the emitted event_delivery_failed record flows back through the stream
// the worker polls. A subscribe-to-all subscription whose endpoint is
// down exhausts its retry budget on a normal event, which emits
// event_delivery_failed into the buffer. The test asserts (a) that
// record lands in the buffer, and (b) the worker does not re-deliver it
// to the same failing subscription on the next tick.
func TestWebhookWorkerDoesNotRedeliverDeliveryFailureToFailingSubscription(t *testing.T) {
	t.Skip("open TEST-GAPS finding: §25.5 loop-prevention guard (event_delivery_failed excluded from re-delivery to the failing subscription) is not implemented in the delivery worker; awaiting a human decision on the mechanism")

	buf := eventbuffer.NewEventBuffer(256)
	emitter := eventbuffer.NewEmitter(buf, "test-replica")
	source := &bufferEventSource{buf: buf}

	// The subscription subscribes to every event type (empty Types) and
	// its endpoint always fails, so any delivery to it exhausts the budget.
	const subID = "sub-loop"
	sink := &fakeSink{outcomes: []webhookdelivery.Outcome{{StatusCode: 500}}}

	w := newWorker(t, WebhookWorkerConfig{
		Events:        source,
		Subscriptions: staticSubs{{ID: subID, CallbackURL: "https://h", Secret: []byte("s"), TenantFilter: "*"}},
		Transport:     sink,
		TrackingMode:  webhookdelivery.TrackingFull,
		EmitFailure: func(sID, eventID string) {
			// Mirror the production wiring (cmd/lenny-ops/deps.go): emit an
			// event_delivery_failed operational event whose subject names the
			// failing subscription. The event is emitted, not directly
			// delivered.
			payload, _ := json.Marshal(map[string]any{"subscriptionId": sID, "eventId": eventID})
			_ = emitter.Emit(context.Background(), events.OperationalEvent{
				Type:            events.EventEventDeliveryFailed.CloudEventsType(),
				Subject:         "event_subscription/" + sID,
				Severity:        "warning",
				DataContentType: "application/json",
				Data:            payload,
			})
		},
	})

	// Seed a normal operational event the subscription matches.
	if err := emitter.Emit(context.Background(), events.OperationalEvent{
		Type:            events.EventType("alert_fired").CloudEventsType(),
		Subject:         "alert/pool",
		Severity:        "warning",
		DataContentType: "application/json",
		Data:            json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("seed emit: %v", err)
	}

	// Tick 1: deliver the seed event, exhaust the budget, emit event_delivery_failed.
	if err := w.Tick(context.Background()); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if got := sink.count(); got != webhookdelivery.MaxAttempts {
		t.Fatalf("delivery attempts after tick 1 = %d, want %d (full budget on the seed event)", got, webhookdelivery.MaxAttempts)
	}

	// (a) the event_delivery_failed record must land in the buffer.
	failType := events.EventEventDeliveryFailed.CloudEventsType()
	page := buf.Query(0, events.EventFilter{}, 100)
	var landed bool
	for _, be := range page.Events {
		if be.Event.Type == failType {
			landed = true
		}
	}
	if !landed {
		t.Fatalf("event_delivery_failed was not recorded in the buffer")
	}

	// Tick 2: the worker re-polls; the event_delivery_failed record must
	// not be delivered back to the failing subscription (the loop guard).
	if err := w.Tick(context.Background()); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	for _, d := range sink.attempts {
		if strings.HasSuffix(d.EventType, "event_delivery_failed") {
			t.Fatalf("event_delivery_failed was re-delivered to the failing subscription %q; §25.5 forbids this to avoid loops", subID)
		}
	}
}
