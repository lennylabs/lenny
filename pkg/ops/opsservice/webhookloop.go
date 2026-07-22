// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	"github.com/lennylabs/lenny/pkg/webhookdelivery"
)

// WebhookEvent is one operational event the §25.5 webhook delivery
// worker may deliver: the serialized CloudEvents JSON record plus the
// type and id the worker puts in the X-Lenny-* headers and uses for
// subscription matching.
type WebhookEvent struct {
	// ID is the CloudEvents id attribute (the eventKey).
	ID string
	// Type is the CloudEvents type attribute (e.g. dev.lenny.alert_fired).
	Type string
	// TenantID is the lennytenantid CloudEvents extension: the tenant the
	// event is labeled with. An empty value denotes a platform-scoped
	// event (no tenant label), which only a tenantFilter: "*" subscription
	// receives. spec: 25.5
	TenantID string
	// Body is the serialized CloudEvents JSON record posted to the
	// callback URL.
	Body []byte
}

// WebhookSubscription is an active §25.5 event subscription the worker
// delivers matching events to.
type WebhookSubscription struct {
	// ID is the subscription identifier.
	ID string
	// CallbackURL is the webhook endpoint.
	CallbackURL string
	// Secret is the HMAC signing secret.
	Secret []byte
	// Types, when non-empty, restricts delivery to events whose type is
	// in the set. An empty set matches every event type.
	Types []string
	// TenantFilter is the §25.5 tenant-isolation gate. The wildcard "*"
	// matches every event, including platform-scoped (no-label) events; a
	// concrete tenant id matches only events labeled with that tenant.
	// spec: 25.5
	TenantFilter string
	// Generation is the §25.5 line 2751 per-subscription invalidation
	// counter. The worker re-checks it against the cache immediately
	// before each delivery so a subscription deleted or updated since the
	// tick's snapshot was taken is skipped (or not delivered with stale
	// settings) rather than receiving one more event.
	Generation int64
}

// matches reports whether the subscription wants the given event type.
func (s WebhookSubscription) matches(eventType string) bool {
	if len(s.Types) == 0 {
		return true
	}
	for _, t := range s.Types {
		if t == eventType {
			return true
		}
	}
	return false
}

// matchesTenant reports whether the subscription's tenantFilter admits an
// event carrying tenantID. A platform-scoped event (empty tenantID, no
// tenant label) is matched only by the wildcard filter; a labeled event
// is matched by the wildcard filter or by a filter equal to the event's
// tenant. Any other filter, including an empty one, fails closed on the
// tenant-isolation boundary. spec: 25.5 — event delivery filters each
// event against the subscription's tenantFilter; platform-scoped events
// reach only tenantFilter: "*" subscriptions.
func (s WebhookSubscription) matchesTenant(tenantID string) bool {
	if s.TenantFilter == eventsubscription.TenantFilterAll {
		return true
	}
	if tenantID == "" {
		return false
	}
	return s.TenantFilter == tenantID
}

// EventSource yields the operational events the §25.5 worker delivers.
// The production implementation reads the Redis ops:events:stream (or
// the gateway buffer when Redis is down); tests supply a fixed slice.
type EventSource interface {
	// Poll returns the events that arrived since the previous Poll. An
	// empty slice means no new events.
	Poll(ctx context.Context) ([]WebhookEvent, error)
}

// SubscriptionSource yields the active §25.5 webhook subscriptions. The
// production implementation is the in-memory subscription cache backed
// by ops_event_subscriptions; tests supply a fixed slice.
type SubscriptionSource interface {
	// Subscriptions returns every active subscription.
	Subscriptions() []WebhookSubscription
}

// GenerationChecker, when implemented by a SubscriptionSource, lets the
// worker verify a subscription is still current immediately before
// delivery. spec: §25.5 line 2751 — "Delivery goroutines check
// generation before each delivery; a deleted-but-not-yet-invalidated
// subscription will be skipped because its generation mismatches."
type GenerationChecker interface {
	// Current reports whether subID is still an active subscription at
	// the given generation. It returns false when the subscription was
	// deleted or updated (its generation advanced) since the worker read
	// the tick snapshot.
	Current(subID string, generation int64) bool
}

// DeliveryMetrics receives the §25.5 webhook delivery instruments after
// each terminal delivery: the lenny_ops_events_webhook_delivery_total
// counter (by status) and the
// lenny_ops_events_webhook_delivery_latency_seconds histogram. A nil
// observer disables the instruments. spec: §25.5 lines 2790-2791.
type DeliveryMetrics interface {
	// ObserveDelivery records one terminal delivery: its subscription,
	// "delivered" or "failed" status, and end-to-end latency.
	ObserveDelivery(subID, status string, latency time.Duration)
}

// DeliveryRecorder persists the outcome of a §25.5 webhook delivery.
// The production implementation writes ops_event_deliveries subject to
// the tracking mode; a nil recorder drops delivery history (the loop
// still delivers).
type DeliveryRecorder interface {
	// RecordDelivery stores one delivery's terminal outcome: the
	// subscription, the event, the attempt count, and whether it failed.
	RecordDelivery(ctx context.Context, subID, eventID string, attempts int, failed bool)
}

// transport abstracts a single webhook delivery attempt so the worker
// can be tested against an in-process fake.
type transport interface {
	Deliver(ctx context.Context, d webhookdelivery.Delivery) webhookdelivery.Outcome
}

// WebhookWorker is the §25.5 webhook delivery worker: the leader-only
// loop that reads operational events and POSTs each one to every
// matching subscription's callback URL. It applies the §25.5 retry
// budget (3 attempts) and exponential backoff (1s, 5s, 30s) from
// pkg/webhookdelivery, signs each delivery with HMAC-SHA256, and after
// a delivery exhausts its budget emits an event_delivery_failed
// operational event (not itself delivered, to avoid loops).
type WebhookWorker struct {
	events      EventSource
	subs        SubscriptionSource
	generations GenerationChecker
	transport   transport
	recorder    DeliveryRecorder
	tracking    webhookdelivery.TrackingMode
	emitFailure func(subID, eventID string)
	metrics     DeliveryMetrics
	sleep       func(time.Duration)
	now         func() time.Time

	backlog atomic.Int64
}

// WebhookWorkerConfig configures a §25.5 webhook delivery worker.
type WebhookWorkerConfig struct {
	// Events is the operational-event source. Required.
	Events EventSource
	// Subscriptions is the active-subscription source. Required.
	Subscriptions SubscriptionSource
	// Transport delivers one webhook attempt. When nil, an HTTP
	// transport bounded by HTTPTimeout is constructed.
	Transport transport
	// Recorder persists delivery outcomes. Optional.
	Recorder DeliveryRecorder
	// TrackingMode is the §25.5 ops.webhooks.deliveryTrackingMode that
	// decides which deliveries Recorder is asked to persist.
	TrackingMode webhookdelivery.TrackingMode
	// HTTPTimeout bounds each delivery attempt when Transport is nil.
	HTTPTimeout time.Duration
	// EmitFailure, when non-nil, is invoked after a delivery exhausts the
	// §25.5 retry budget so the service can emit event_delivery_failed.
	EmitFailure func(subID, eventID string)
	// Metrics, when non-nil, receives the §25.5 webhook delivery
	// instruments after each terminal delivery.
	Metrics DeliveryMetrics
	// Now overrides the clock used to measure delivery latency for the
	// Metrics observer. A nil value uses time.Now.
	Now func() time.Time
}

// NewWebhookWorker builds a §25.5 webhook delivery worker.
func NewWebhookWorker(cfg WebhookWorkerConfig) *WebhookWorker {
	tr := cfg.Transport
	if tr == nil {
		timeout := cfg.HTTPTimeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		tr = webhookdelivery.NewTransport(timeout)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	w := &WebhookWorker{
		events:      cfg.Events,
		subs:        cfg.Subscriptions,
		transport:   tr,
		recorder:    cfg.Recorder,
		tracking:    cfg.TrackingMode,
		emitFailure: cfg.EmitFailure,
		metrics:     cfg.Metrics,
		sleep:       time.Sleep,
		now:         now,
	}
	// A SubscriptionSource that also tracks generations lets the worker
	// re-verify each subscription is still current immediately before
	// delivery (§25.5 line 2751).
	if gc, ok := cfg.Subscriptions.(GenerationChecker); ok {
		w.generations = gc
	}
	return w
}

// Backlog reports the number of webhook deliveries currently pending
// (events polled but not yet delivered to all subscriptions). The
// self-health webhook_backlog check reads it.
func (w *WebhookWorker) Backlog() int {
	return int(w.backlog.Load())
}

// Tick is the §25.5 webhook-worker loop body. It polls for new events
// and delivers each one to every matching subscription. Delivery is
// synchronous within the tick so the §25.5 retry backoff is observed;
// the loop interval bounds how often new events are picked up.
func (w *WebhookWorker) Tick(ctx context.Context) error {
	events, err := w.events.Poll(ctx)
	if err != nil {
		return err
	}
	subs := w.subs.Subscriptions()

	// The backlog gauge counts the (event, matching-subscription) pairs
	// still to deliver; it is raised by the work this tick enqueues and
	// drained one pair at a time as each delivery completes, so the
	// §25.4 webhook_backlog self-health check sees a live count.
	var pending int64
	for _, ev := range events {
		for _, sub := range subs {
			if sub.matches(ev.Type) && sub.matchesTenant(ev.TenantID) {
				pending++
			}
		}
	}
	w.backlog.Add(pending)

	for _, ev := range events {
		for _, sub := range subs {
			// §25.5: an event is delivered only when the subscription's
			// type filter and its tenantFilter both admit it. The tenant
			// predicate runs next to the type predicate so a cross-tenant
			// or platform-scoped event never reaches a tenant-scoped
			// subscription.
			if !sub.matches(ev.Type) || !sub.matchesTenant(ev.TenantID) {
				continue
			}
			if ctx.Err() != nil {
				// A cancelled tick leaves its remaining pairs undelivered;
				// drop them from the gauge so it does not leak upward.
				w.backlog.Add(-pending)
				return ctx.Err()
			}
			// §25.5 line 2751: re-check the subscription is still current
			// right before delivery. A subscription deleted or updated since
			// this tick's snapshot (its generation advanced, or it left the
			// active set) is skipped so it does not receive one more event.
			if w.generations != nil && !w.generations.Current(sub.ID, sub.Generation) {
				w.backlog.Add(-1)
				pending--
				continue
			}
			w.deliver(ctx, sub, ev)
			pending--
		}
	}
	return nil
}

// deliver attempts one event delivery to one subscription, retrying
// per the §25.5 budget and backoff. It records the terminal outcome
// and, on exhaustion, triggers event_delivery_failed emission.
func (w *WebhookWorker) deliver(ctx context.Context, sub WebhookSubscription, ev WebhookEvent) {
	defer w.backlog.Add(-1)
	start := w.now()
	var (
		attempts int
		out      webhookdelivery.Outcome
	)
	for attempt := 1; attempt <= webhookdelivery.MaxAttempts; attempt++ {
		attempts = attempt
		out = w.transport.Deliver(ctx, webhookdelivery.Delivery{
			CallbackURL: sub.CallbackURL,
			Body:        ev.Body,
			Secret:      sub.Secret,
			EventType:   ev.Type,
			EventID:     ev.ID,
			DeliveryID:  uuid.NewString(),
			Attempt:     attempt,
		})
		if out.Delivered() {
			break
		}
		if !out.Retryable() {
			break
		}
		// Wait the §25.5 backoff before the next attempt, unless this was
		// the final attempt or the context has been cancelled.
		if attempt < webhookdelivery.MaxAttempts && ctx.Err() == nil {
			if delay, ok := webhookdelivery.RetryDelay(attempt); ok {
				w.sleep(delay)
			}
		}
	}

	failed := !out.Delivered()
	if w.metrics != nil {
		status := eventsubscription.DeliveryDelivered
		if failed {
			status = eventsubscription.DeliveryFailed
		}
		w.metrics.ObserveDelivery(sub.ID, status, w.now().Sub(start))
	}
	if w.recorder != nil && webhookdelivery.ShouldRecord(w.tracking, failed) {
		w.recorder.RecordDelivery(ctx, sub.ID, ev.ID, attempts, failed)
	}
	if failed {
		log.Printf("lenny-ops: webhook delivery to subscription %s for event %s failed after %d attempts",
			sub.ID, ev.ID, attempts)
		if w.emitFailure != nil {
			// §25.5: the event_delivery_failed event is emitted but not
			// itself delivered to the subscription, to avoid loops.
			w.emitFailure(sub.ID, ev.ID)
		}
	}
}
