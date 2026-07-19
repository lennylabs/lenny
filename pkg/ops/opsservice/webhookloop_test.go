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
			TenantFilter: "*",
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
			TenantFilter: "*",
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

// TestWebhookWorkerFiltersByTenant is the §25.5 delivery-time
// tenant-isolation contract: an event is delivered to a subscription only
// when the subscription's tenantFilter admits the event's tenant label. A
// tenant-scoped subscription receives its own tenant's events, does not
// receive another tenant's events, and does not receive platform-scoped
// (no-label) events; a wildcard subscription receives all three. Against
// the pre-fix type-only matcher every subscription received every event,
// so the cross-tenant and platform-scoped assertions below fail without
// the tenant predicate. spec: 25.5 (delivery-time tenantFilter,
// platform-scoped rule)
func TestWebhookWorkerFiltersByTenant(t *testing.T) {
	cases := []struct {
		name         string
		tenantFilter string
		eventTenant  string
		wantDeliver  bool
	}{
		{"tenant sub gets its own tenant", "acme", "acme", true},
		{"tenant sub drops another tenant", "acme", "globex", false},
		{"tenant sub drops platform-scoped", "acme", "", false},
		{"wildcard sub gets a tenant event", "*", "acme", true},
		{"wildcard sub gets platform-scoped", "*", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := &fakeSink{outcomes: []webhookdelivery.Outcome{{StatusCode: 200}}}
			w := newWorker(t, WebhookWorkerConfig{
				Events: &staticEvents{events: []WebhookEvent{
					{ID: "evt-1", Type: "dev.lenny.alert_fired", TenantID: tc.eventTenant, Body: []byte(`{}`)},
				}},
				Subscriptions: staticSubs{{
					ID: "sub-1", CallbackURL: "https://hook.example.com",
					Secret: []byte("s"), TenantFilter: tc.tenantFilter,
				}},
				Transport: sink,
			})
			if err := w.Tick(context.Background()); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			want := 0
			if tc.wantDeliver {
				want = 1
			}
			if got := sink.count(); got != want {
				t.Errorf("delivery attempts = %d, want %d (filter %q, event tenant %q)",
					got, want, tc.tenantFilter, tc.eventTenant)
			}
			// The backlog gauge must drain to zero whether or not the event
			// was delivered, so the tenant-filtered pairs do not leak.
			if got := w.Backlog(); got != 0 {
				t.Errorf("Backlog() = %d after the tick, want 0", got)
			}
		})
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
		Subscriptions: staticSubs{{ID: "sub-1", CallbackURL: "https://h", Secret: []byte("s"), TenantFilter: "*"}},
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
		Subscriptions: staticSubs{{ID: "sub-1", CallbackURL: "https://h", Secret: []byte("s"), TenantFilter: "*"}},
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
		Subscriptions: staticSubs{{ID: "sub-1", CallbackURL: "https://h", Secret: []byte("s"), TenantFilter: "*"}},
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
		Subscriptions: staticSubs{{ID: "sub-1", CallbackURL: "https://h", Secret: []byte("s"), TenantFilter: "*"}},
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

// recordingMetrics captures the §25.5 delivery instruments the worker
// emits.
type recordingMetrics struct {
	mu      sync.Mutex
	records []struct {
		subID, status string
		latency       time.Duration
	}
}

func (m *recordingMetrics) ObserveDelivery(subID, status string, latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, struct {
		subID, status string
		latency       time.Duration
	}{subID, status, latency})
}

// genSubs is a SubscriptionSource that also implements GenerationChecker.
// current maps subscription id to the generation it considers live; an
// id absent from current is treated as deleted.
type genSubs struct {
	subs    []WebhookSubscription
	current map[string]int64
}

func (g genSubs) Subscriptions() []WebhookSubscription { return g.subs }

func (g genSubs) Current(subID string, generation int64) bool {
	live, ok := g.current[subID]
	return ok && live == generation
}

// TestWebhookWorkerObservesDeliveryMetrics is the §25.5 lines 2790-2791
// contract: the worker reports a delivered status and a measured latency
// to the metrics observer for a successful delivery, and a failed status
// when the budget is exhausted.
func TestWebhookWorkerObservesDeliveryMetrics_spec_25_5_2790(t *testing.T) {
	sink := &fakeSink{outcomes: []webhookdelivery.Outcome{{StatusCode: 200}}}
	met := &recordingMetrics{}
	// A clock that advances 250ms each read so the measured latency is
	// non-zero (start vs end of deliver).
	var ticks int64
	clock := func() time.Time {
		ticks++
		return time.Unix(0, ticks*int64(250*time.Millisecond))
	}
	w := newWorker(t, WebhookWorkerConfig{
		Events: &staticEvents{events: []WebhookEvent{
			{ID: "evt-1", Type: "dev.lenny.alert_fired", Body: []byte(`{}`)},
		}},
		Subscriptions: staticSubs{{ID: "sub-1", CallbackURL: "https://h", Secret: []byte("s"), TenantFilter: "*"}},
		Transport:     sink,
		Metrics:       met,
		Now:           clock,
	})
	if err := w.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(met.records) != 1 {
		t.Fatalf("metrics records = %d, want 1", len(met.records))
	}
	r := met.records[0]
	if r.subID != "sub-1" || r.status != "delivered" {
		t.Errorf("metric = sub %q status %q, want sub-1/delivered", r.subID, r.status)
	}
	if r.latency != 250*time.Millisecond {
		t.Errorf("latency = %v, want 250ms", r.latency)
	}
}

// TestWebhookWorkerMetricsFailedStatus confirms an exhausted delivery is
// reported with the failed status. spec: §25.5 line 2790.
func TestWebhookWorkerMetricsFailedStatus_spec_25_5_2790(t *testing.T) {
	sink := &fakeSink{outcomes: []webhookdelivery.Outcome{{StatusCode: 500}}}
	met := &recordingMetrics{}
	w := newWorker(t, WebhookWorkerConfig{
		Events: &staticEvents{events: []WebhookEvent{
			{ID: "evt-9", Type: "dev.lenny.alert_fired", Body: []byte(`{}`)},
		}},
		Subscriptions: staticSubs{{ID: "sub-1", CallbackURL: "https://h", Secret: []byte("s"), TenantFilter: "*"}},
		Transport:     sink,
		Metrics:       met,
	})
	if err := w.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(met.records) != 1 || met.records[0].status != "failed" {
		t.Errorf("metrics = %+v, want one failed delivery", met.records)
	}
}

// TestWebhookWorkerSkipsStaleGeneration is the §25.5 line 2751 contract:
// a subscription whose generation no longer matches the cache (deleted
// or updated since the tick snapshot) is not delivered to.
func TestWebhookWorkerSkipsStaleGeneration_spec_25_5_2751(t *testing.T) {
	sink := &fakeSink{outcomes: []webhookdelivery.Outcome{{StatusCode: 200}}}
	subs := genSubs{
		subs: []WebhookSubscription{
			{ID: "live", CallbackURL: "https://h", Secret: []byte("s"), TenantFilter: "*", Generation: 3},
			{ID: "stale", CallbackURL: "https://h", Secret: []byte("s"), TenantFilter: "*", Generation: 1},
		},
		// "live" is still at generation 3; "stale" advanced to 2 (or was
		// deleted) so its snapshot generation 1 no longer matches.
		current: map[string]int64{"live": 3, "stale": 2},
	}
	w := newWorker(t, WebhookWorkerConfig{
		Events: &staticEvents{events: []WebhookEvent{
			{ID: "evt-1", Type: "dev.lenny.alert_fired", Body: []byte(`{}`)},
		}},
		Subscriptions: subs,
		Transport:     sink,
	})
	if err := w.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := sink.count(); got != 1 {
		t.Fatalf("delivery attempts = %d, want 1 (only the live subscription)", got)
	}
	if sink.attempts[0].CallbackURL == "" {
		t.Fatal("expected the live subscription's delivery")
	}
	if got := w.Backlog(); got != 0 {
		t.Errorf("Backlog() = %d after a skipped stale delivery, want 0", got)
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
		Subscriptions: staticSubs{{ID: "sub-1", CallbackURL: "https://h", Secret: []byte("s"), TenantFilter: "*"}},
		Transport:     sink,
	})
	if err := w.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := w.Backlog(); got != 0 {
		t.Errorf("Backlog() = %d after the tick completed, want 0", got)
	}
}
