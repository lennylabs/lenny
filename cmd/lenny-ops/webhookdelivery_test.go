// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
)

// TestDeliveryRetentionJobPurgesExpired is the §25.5 lines 2649-2664
// contract: the leader-only delivery-retention cron deletes expired
// ops_event_deliveries rows and leaves future-dated rows intact. The job
// is scheduled at the off-peak 03:45 UTC slot.
func TestDeliveryRetentionJobPurgesExpired_spec_25_5_2661(t *testing.T) {
	store := eventsubscription.NewMemoryStore()
	now := time.Now().UTC()
	for _, exp := range []time.Time{now.Add(-2 * time.Hour), now.Add(-1 * time.Hour)} {
		if _, err := store.RecordDelivery(context.Background(), eventsubscription.Delivery{
			SubscriptionID: "sub-1", EventID: "old", Status: eventsubscription.DeliveryFailed, ExpiresAt: exp,
		}); err != nil {
			t.Fatalf("RecordDelivery: %v", err)
		}
	}
	if _, err := store.RecordDelivery(context.Background(), eventsubscription.Delivery{
		SubscriptionID: "sub-1", EventID: "fresh", Status: eventsubscription.DeliveryDelivered,
		ExpiresAt: now.Add(48 * time.Hour),
	}); err != nil {
		t.Fatalf("RecordDelivery: %v", err)
	}

	job := deliveryRetentionJob(store)
	if job.Name != "delivery-retention" || job.Expression != "45 3 * * *" {
		t.Fatalf("job = %q @ %q, want delivery-retention @ 45 3 * * *", job.Name, job.Expression)
	}
	if err := job.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	left, _ := store.ListDeliveries(context.Background(), "sub-1", 100)
	if len(left) != 1 || left[0].EventID != "fresh" {
		t.Fatalf("after retention sweep = %+v, want only the fresh row", left)
	}
}
