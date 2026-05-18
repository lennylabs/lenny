// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

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
	transport   transport
	recorder    DeliveryRecorder
	tracking    webhookdelivery.TrackingMode
	emitFailure func(subID, eventID string)
	sleep       func(time.Duration)

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
	return &WebhookWorker{
		events:      cfg.Events,
		subs:        cfg.Subscriptions,
		transport:   tr,
		recorder:    cfg.Recorder,
		tracking:    cfg.TrackingMode,
		emitFailure: cfg.EmitFailure,
		sleep:       time.Sleep,
	}
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
			if sub.matches(ev.Type) {
				pending++
			}
		}
	}
	w.backlog.Add(pending)

	for _, ev := range events {
		for _, sub := range subs {
			if !sub.matches(ev.Type) {
				continue
			}
			if ctx.Err() != nil {
				// A cancelled tick leaves its remaining pairs undelivered;
				// drop them from the gauge so it does not leak upward.
				w.backlog.Add(-pending)
				return ctx.Err()
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
