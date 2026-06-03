// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
)

// TestStoreDeliveryRecorderWritesRow is the §25.5 lines 2701-2713
// contract: a recorded delivery is persisted with the right status and
// an expires_at that is exactly the retention TTL past the row's
// creation.
func TestStoreDeliveryRecorderWritesRow_spec_25_5_2701(t *testing.T) {
	store := eventsubscription.NewMemoryStore()
	rec := NewStoreDeliveryRecorder(store, RetentionPolicy{Retention: 7 * 24 * time.Hour}, nil)
	rec.RecordDelivery(context.Background(), "sub-1", "evt-1", 2, false)

	got, err := store.ListDeliveries(context.Background(), "sub-1", 10)
	if err != nil {
		t.Fatalf("ListDeliveries: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(got))
	}
	d := got[0]
	if d.Status != eventsubscription.DeliveryDelivered || d.Attempts != 2 {
		t.Errorf("delivery = status %q attempts %d, want delivered/2", d.Status, d.Attempts)
	}
	if ttl := d.ExpiresAt.Sub(d.CreatedAt); ttl != 7*24*time.Hour {
		t.Errorf("expires_at TTL = %v, want 7d", ttl)
	}
}

// TestStoreDeliveryRecorderFailuresOnlyRetention confirms a failed
// delivery uses the longer failures-only retention window. spec: §25.5
// line 2663.
func TestStoreDeliveryRecorderFailuresOnlyRetention_spec_25_5_2663(t *testing.T) {
	store := eventsubscription.NewMemoryStore()
	rec := NewStoreDeliveryRecorder(store, RetentionPolicy{
		Retention:             7 * 24 * time.Hour,
		FailuresOnlyRetention: 30 * 24 * time.Hour,
	}, nil)
	rec.RecordDelivery(context.Background(), "sub-1", "evt-1", 3, true)
	rec.RecordDelivery(context.Background(), "sub-1", "evt-2", 1, false)

	got, _ := store.ListDeliveries(context.Background(), "sub-1", 10)
	if len(got) != 2 {
		t.Fatalf("deliveries = %d, want 2", len(got))
	}
	byEvent := map[string]eventsubscription.Delivery{}
	for _, d := range got {
		byEvent[d.EventID] = d
	}
	if ttl := byEvent["evt-1"].ExpiresAt.Sub(byEvent["evt-1"].CreatedAt); ttl != 30*24*time.Hour {
		t.Errorf("failed-delivery TTL = %v, want 30d", ttl)
	}
	if ttl := byEvent["evt-2"].ExpiresAt.Sub(byEvent["evt-2"].CreatedAt); ttl != 7*24*time.Hour {
		t.Errorf("successful-delivery TTL = %v, want 7d", ttl)
	}
}

// TestStoreDeliveryRecorderDefaultRetention confirms a zero policy
// defaults to the 7-day Tier-2 retention rather than a zero TTL.
func TestStoreDeliveryRecorderDefaultRetention(t *testing.T) {
	store := eventsubscription.NewMemoryStore()
	rec := NewStoreDeliveryRecorder(store, RetentionPolicy{}, nil)
	rec.RecordDelivery(context.Background(), "sub-1", "evt-1", 1, false)
	got, _ := store.ListDeliveries(context.Background(), "sub-1", 10)
	if len(got) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(got))
	}
	if ttl := got[0].ExpiresAt.Sub(got[0].CreatedAt); ttl != 7*24*time.Hour {
		t.Errorf("default TTL = %v, want 7d", ttl)
	}
}
