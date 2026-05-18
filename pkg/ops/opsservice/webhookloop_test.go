// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/webhookdelivery"
)

// fakeSink is an in-process webhook receiver standing in for an HTTP
// transport. It records every delivery attempt and returns a scripted
// sequence of outcomes, so retry behavior is exercised without a real
// network.
type fakeSink struct {
	mu       sync.Mutex
	attempts []webhookdelivery.Delivery
	outcomes []webhookdelivery.Outcome // consumed in order; last repeats
}

func (s *fakeSink) Deliver(_ context.Context, d webhookdelivery.Delivery) webhookdelivery.Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, d)
	idx := len(s.attempts) - 1
	if idx >= len(s.outcomes) {
		idx = len(s.outcomes) - 1
	}
	return s.outcomes[idx]
}

func (s *fakeSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.attempts)
}

// staticEvents is a one-shot EventSource: it returns its events on the
// first Poll and nothing after, so a worker tick delivers a fixed set.
type staticEvents struct {
	mu     sync.Mutex
	events []WebhookEvent
	polled bool
}

func (e *staticEvents) Poll(context.Context) ([]WebhookEvent, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.polled {
		return nil, nil
	}
	e.polled = true
	return e.events, nil
}

// staticSubs is a fixed SubscriptionSource.
type staticSubs []WebhookSubscription

func (s staticSubs) Subscriptions() []WebhookSubscription { return s }

// recordingRecorder captures the delivery outcomes the worker persists.
type recordingRecorder struct {
	mu      sync.Mutex
	records []struct {
		subID, eventID string
		attempts       int
		failed         bool
	}
}

func (r *recordingRecorder) RecordDelivery(_ context.Context, subID, eventID string, attempts int, failed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, struct {
		subID, eventID string
		attempts       int
		failed         bool
	}{subID, eventID, attempts, failed})
}

// newWorker builds a WebhookWorker over the fakes with sleep stubbed
// out so the §25.5 backoff does not slow the test.
func newWorker(t *testing.T, cfg WebhookWorkerConfig) *WebhookWorker {
	t.Helper()
	w := NewWebhookWorker(cfg)
	w.sleep = func(time.Duration) {} // skip the 1s/5s/30s backoff waits
	return w
}

// TestWebhookWorkerDeliversMatchingEvent is the §25.5 delivery
// contract: the worker POSTs an event to a subscription whose type
// filter matches, signs it, and records the success.
func TestWebhookWorkerDeliversMatchingEvent(t *testing.T) {
	sink := &fakeSink{outcomes: []webhookdelivery.Outcome{{StatusCode: 200}}}
	rec := &recordingRecorder{}
	w := newWorker(t, WebhookWorkerConfig{
		Events: &staticEvents{events: []WebhookEvent{
			{ID: "evt-1", Type: "dev.lenny.alert_fired", Body: []byte(`{"id":"evt-1"}`)},
		}},
		Subscriptions: staticSubs{{
			ID: "sub-1", CallbackURL: "https://hook.example.com",
			Secret: []byte("whsec_x"), Types: []string{"dev.lenny.alert_fired"},
		}},
		Transport:    sink,
		Recorder:     rec,
		TrackingMode: webhookdelivery.TrackingFull,
	})

	if err := w.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := sink.count(); got != 1 {
		t.Fatalf("delivery attempts = %d, want 1", got)
	}
	d := sink.attempts[0]
	if d.EventID != "evt-1" || d.Attempt != 1 {
		t.Errorf("delivery = id %q attempt %d, want evt-1 attempt 1", d.EventID, d.Attempt)
	}
	if len(rec.records) != 1 || rec.records[0].failed {
		t.Errorf("recorded %+v, want one successful delivery", rec.records)
	}
}

// TestWebhookWorkerSkipsNonMatchingSubscription confirms the §25.5
// type filter: an event is not delivered to a subscription whose type
// set excludes it.
func TestWebhookWorkerSkipsNonMatchingSubscription(t *testing.T) {
	sink := &fakeSink{outcomes: []webhookdelivery.Outcome{{StatusCode: 200}}}
	w := newWorker(t, WebhookWorkerConfig{
		Events: &staticEvents{events: []WebhookEvent{
			{ID: "evt-1", Type: "dev.lenny.alert_fired", Body: []byte(`{}`)},
		}},
		Subscriptions: staticSubs{{
			ID: "sub-pool", CallbackURL: "https://hook.example.com",
			Secret: []byte("s"), Types: []string{"dev.lenny.pool_state_changed"},
		}},
		Transport: sink,
	})
	if err := w.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := sink.count(); got != 0 {
		t.Errorf("delivery attempts = %d, want 0 (type filter excludes the event)", got)
	}
}

// TestWebhookWorkerRetriesTransientFailure is the §25.5 retry-budget
// contract: a transient failure is retried up to 3 attempts; a success
// on the second attempt stops the retries.
func TestWebhookWorkerRetriesTransientFailure(t *testing.T) {
	sink := &fakeSink{outcomes: []webhookdelivery.Outcome{
		{StatusCode: 503}, // attempt 1 — transient, retried
		{StatusCode: 200}, // attempt 2 — succeeds
	}}
	rec := &recordingRecorder{}
	w := newWorker(t, WebhookWorkerConfig{
		Events: &staticEvents{events: []WebhookEvent{
			{ID: "evt-1", Type: "dev.lenny.alert_fired", Body: []byte(`{}`)},
		}},
		Subscriptions: staticSubs{{ID: "sub-1", CallbackURL: "https://h", Secret: []byte("s")}},
		Transport:     sink,
		Recorder:      rec,
		TrackingMode:  webhookdelivery.TrackingFull,
	})
	if err := w.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := sink.count(); got != 2 {
		t.Fatalf("delivery attempts = %d, want 2 (retry then success)", got)
	}
	if len(rec.records) != 1 || rec.records[0].failed || rec.records[0].attempts != 2 {
		t.Errorf("recorded %+v, want one success at attempt 2", rec.records)
	}
}

// TestWebhookWorkerExhaustsBudgetAndEmitsFailure is the §25.5
// exhausted-budget contract: after 3 failed attempts the delivery is
// marked failed and event_delivery_failed emission is triggered.
func TestWebhookWorkerExhaustsBudgetAndEmitsFailure(t *testing.T) {
	sink := &fakeSink{outcomes: []webhookdelivery.Outcome{{StatusCode: 500}}} // always 500
	rec := &recordingRecorder{}
	var failedSub, failedEvent string
	w := newWorker(t, WebhookWorkerConfig{
		Events: &staticEvents{events: []WebhookEvent{
			{ID: "evt-9", Type: "dev.lenny.alert_fired", Body: []byte(`{}`)},
		}},
		Subscriptions: staticSubs{{ID: "sub-1", CallbackURL: "https://h", Secret: []byte("s")}},
		Transport:     sink,
		Recorder:      rec,
		TrackingMode:  webhookdelivery.TrackingFull,
		EmitFailure: func(subID, eventID string) {
			failedSub, failedEvent = subID, eventID
		},
	})
	if err := w.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := sink.count(); got != webhookdelivery.MaxAttempts {
		t.Fatalf("delivery attempts = %d, want %d (full retry budget)", got, webhookdelivery.MaxAttempts)
	}
	if len(rec.records) != 1 || !rec.records[0].failed {
		t.Fatalf("recorded %+v, want one failed delivery", rec.records)
	}
	if failedSub != "sub-1" || failedEvent != "evt-9" {
		t.Errorf("event_delivery_failed emitted for sub %q event %q, want sub-1/evt-9", failedSub, failedEvent)
	}
}

// TestWebhookWorkerStopsOnPermanentFailure confirms a §25.5 PERMANENT
// failure (a 4xx) is not retried.
func TestWebhookWorkerStopsOnPermanentFailure(t *testing.T) {
	sink := &fakeSink{outcomes: []webhookdelivery.Outcome{{StatusCode: 404}}}
	w := newWorker(t, WebhookWorkerConfig{
		Events: &staticEvents{events: []WebhookEvent{
			{ID: "evt-1", Type: "dev.lenny.alert_fired", Body: []byte(`{}`)},
		}},
		Subscriptions: staticSubs{{ID: "sub-1", CallbackURL: "https://h", Secret: []byte("s")}},
		Transport:     sink,
	})
	if err := w.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := sink.count(); got != 1 {
		t.Errorf("delivery attempts = %d, want 1 (a 404 is permanent, not retried)", got)
	}
}

// TestWebhookWorkerFailuresOnlyTrackingSkipsSuccess confirms the §25.5
// failures-only tracking mode persists a failed delivery but not a
// successful one.
func TestWebhookWorkerFailuresOnlyTrackingSkipsSuccess(t *testing.T) {
	sink := &fakeSink{outcomes: []webhookdelivery.Outcome{{StatusCode: 200}}}
	rec := &recordingRecorder{}
	w := newWorker(t, WebhookWorkerConfig{
		Events: &staticEvents{events: []WebhookEvent{
			{ID: "evt-1", Type: "dev.lenny.alert_fired", Body: []byte(`{}`)},
		}},
		Subscriptions: staticSubs{{ID: "sub-1", CallbackURL: "https://h", Secret: []byte("s")}},
		Transport:     sink,
		Recorder:      rec,
		TrackingMode:  webhookdelivery.TrackingFailuresOnly,
	})
	if err := w.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(rec.records) != 0 {
		t.Errorf("recorded %d deliveries under failures-only for a success, want 0", len(rec.records))
	}
}

// TestWebhookWorkerBacklogClearsAfterTick confirms the §25.4
// webhook_backlog gauge returns to zero once a tick's deliveries
// complete, so the self-health check does not report a stale backlog.
func TestWebhookWorkerBacklogClearsAfterTick(t *testing.T) {
	sink := &fakeSink{outcomes: []webhookdelivery.Outcome{{StatusCode: 200}}}
	w := newWorker(t, WebhookWorkerConfig{
		Events: &staticEvents{events: []WebhookEvent{
			{ID: "evt-1", Type: "dev.lenny.alert_fired", Body: []byte(`{}`)},
		}},
		Subscriptions: staticSubs{{ID: "sub-1", CallbackURL: "https://h", Secret: []byte("s")}},
		Transport:     sink,
	})
	if err := w.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := w.Backlog(); got != 0 {
		t.Errorf("Backlog() = %d after the tick completed, want 0", got)
	}
}
