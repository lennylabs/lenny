// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"log/slog"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
)

// RetentionPolicy is the §25.5 delivery-tracking retention configuration
// the recorder uses to stamp each row's expires_at. spec: §25.5 lines
// 2649-2664.
type RetentionPolicy struct {
	// Retention is the lifetime of a recorded delivery row (the chart's
	// ops.webhooks.deliveryRetentionDays). A non-positive value defaults
	// to 7 days, the Tier-2 default.
	Retention time.Duration
	// FailuresOnlyRetention, when positive, is the lifetime applied to
	// failed deliveries (the chart's ops.webhooks.failuresOnlyRetentionDays).
	// Failure rows are kept for investigation, so this is typically longer
	// than Retention. A non-positive value falls back to Retention.
	FailuresOnlyRetention time.Duration
}

func (p RetentionPolicy) ttl(failed bool) time.Duration {
	retention := p.Retention
	if retention <= 0 {
		retention = 7 * 24 * time.Hour
	}
	if failed && p.FailuresOnlyRetention > 0 {
		return p.FailuresOnlyRetention
	}
	return retention
}

// StoreDeliveryRecorder is the production §25.5 DeliveryRecorder: it
// writes each terminal webhook delivery to ops_event_deliveries via the
// eventsubscription.Store, stamping expires_at from the retention
// policy so the §25.5 retention cron can purge it. spec: §25.5 lines
// 2701-2713, 2649-2664.
type StoreDeliveryRecorder struct {
	store  eventsubscription.Store
	policy RetentionPolicy
	now    func() time.Time
	logger *slog.Logger
}

// NewStoreDeliveryRecorder builds a recorder over store with the given
// retention policy. A nil logger uses slog.Default.
func NewStoreDeliveryRecorder(store eventsubscription.Store, policy RetentionPolicy, logger *slog.Logger) *StoreDeliveryRecorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &StoreDeliveryRecorder{store: store, policy: policy, now: time.Now, logger: logger}
}

// RecordDelivery persists one terminal delivery outcome. spec: §25.5
// line 2713 — a delivery exhausting its retries is marked failed.
func (r *StoreDeliveryRecorder) RecordDelivery(ctx context.Context, subID, eventID string, attempts int, failed bool) {
	if r == nil || r.store == nil {
		return
	}
	now := r.now().UTC()
	status := eventsubscription.DeliveryDelivered
	if failed {
		status = eventsubscription.DeliveryFailed
	}
	if _, err := r.store.RecordDelivery(ctx, eventsubscription.Delivery{
		SubscriptionID: subID,
		EventID:        eventID,
		Status:         status,
		Attempts:       attempts,
		LastAttemptAt:  now,
		CreatedAt:      now,
		ExpiresAt:      now.Add(r.policy.ttl(failed)),
	}); err != nil {
		// §25.5 line 2774: delivery tracking is best-effort. A failed
		// write must not stop delivery, so it is logged, not returned.
		r.logger.Warn("ops webhook delivery tracking write failed",
			"subscription_id", subID, "event_id", eventID, "error", err)
	}
}

var _ DeliveryRecorder = (*StoreDeliveryRecorder)(nil)
