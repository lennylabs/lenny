// SPDX-License-Identifier: MIT

package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	embpostgres "github.com/lennylabs/lenny/pkg/embedded/postgres"
	es "github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	"github.com/lennylabs/lenny/pkg/ops/eventsubscription/pgstore"
)

// TestPgStoreRoundTrip_spec_25_5 brings up an embedded Postgres, applies
// the §25.5 webhook-subscription schema (migration 0046 stub + the 0118
// extension), and exercises the full subscription + delivery lifecycle
// against the durable store. It downloads the PostgreSQL bundle, so it is
// skipped under -short.
//
// spec: §25.5 lines 2613-2664.
func TestPgStoreRoundTrip_spec_25_5(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15521,
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	defer func() { _ = pg.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	for _, name := range []string{
		"0046_ops_event_subscriptions.up.sql",
		"0118_ops_event_subscriptions_v25_5.up.sql",
	} {
		sql, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}

	s := pgstore.New(pool)
	secret, _ := es.GenerateSecret()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	rec := es.Record{
		ID:                "sub_1",
		CallbackURL:       "https://acme.example/hook",
		Types:             []string{"dev.lenny.alert_fired", "dev.lenny.pool_state_changed"},
		Severity:          []string{"critical"},
		Description:       "pagerduty bridge",
		SecretHash:        es.HashSecret(secret),
		SecretFingerprint: es.Fingerprint(secret),
		CreatedBy:         "alice@acme.com",
		TenantFilter:      es.TenantFilterAll,
		CreatedAt:         now,
		UpdatedAt:         now,
		Active:            true,
	}
	if err := s.Create(ctx, rec); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.Get(ctx, "sub_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SecretHash != rec.SecretHash || got.SecretFingerprint != rec.SecretFingerprint {
		t.Errorf("secret round-trip mismatch: %+v", got)
	}
	if len(got.Types) != 2 || got.Severity[0] != "critical" || got.Description != "pagerduty bridge" {
		t.Errorf("field round-trip mismatch: %+v", got)
	}
	if got.CreatedByTenantID != "" {
		t.Errorf("platform-admin row created_by_tenant_id = %q, want NULL/empty", got.CreatedByTenantID)
	}

	// Update bumps generation and rotates the secret with overlap fields.
	newSecret, _ := es.GenerateSecret()
	updated, err := s.Update(ctx, "sub_1", func(r *es.Record) error {
		r.PreviousSecretFingerprint = r.SecretFingerprint
		r.SecretHash = es.HashSecret(newSecret)
		r.SecretFingerprint = es.Fingerprint(newSecret)
		r.SecretRotatedAt = now.Add(time.Hour)
		r.Generation++
		r.Active = false
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Generation != 1 || updated.Active || updated.PreviousSecretFingerprint != rec.SecretFingerprint {
		t.Errorf("update round-trip mismatch: %+v", updated)
	}
	reread, _ := s.Get(ctx, "sub_1")
	if reread.SecretRotatedAt.IsZero() || reread.PreviousSecretFingerprint == "" {
		t.Errorf("rotation fields not persisted: %+v", reread)
	}

	// Deliveries: record two, list newest-first, confirm cascade on delete.
	for i := 0; i < 2; i++ {
		if _, err := s.RecordDelivery(ctx, es.Delivery{
			SubscriptionID: "sub_1", EventID: "evt", EventType: "dev.lenny.alert_fired",
			Status: es.DeliveryFailed, Attempts: 3, ExpiresAt: now.Add(24 * time.Hour),
		}); err != nil {
			t.Fatalf("RecordDelivery: %v", err)
		}
	}
	deliveries, err := s.ListDeliveries(ctx, "sub_1", 100)
	if err != nil || len(deliveries) != 2 {
		t.Fatalf("ListDeliveries = %d (%v), want 2", len(deliveries), err)
	}
	if deliveries[0].ID < deliveries[1].ID {
		t.Errorf("deliveries not newest-first: %+v", deliveries)
	}

	// §25.5 lines 2649-2664: DeleteExpired purges only rows whose
	// expires_at has passed, bounded by limit. Record one already-expired
	// row; a sweep at a cutoff after its expiry removes it while the two
	// future-dated rows survive.
	if _, err := s.RecordDelivery(ctx, es.Delivery{
		SubscriptionID: "sub_1", EventID: "stale", EventType: "dev.lenny.alert_fired",
		Status: es.DeliveryDelivered, Attempts: 1, ExpiresAt: now.Add(-1 * time.Hour),
	}); err != nil {
		t.Fatalf("RecordDelivery(stale): %v", err)
	}
	purged, err := s.DeleteExpired(ctx, now, 10000)
	if err != nil || purged != 1 {
		t.Fatalf("DeleteExpired = %d (%v), want 1", purged, err)
	}
	if rows, _ := s.ListDeliveries(ctx, "sub_1", 100); len(rows) != 2 {
		t.Errorf("after retention sweep deliveries = %d, want 2 (future-dated survive)", len(rows))
	}

	deleted, err := s.Delete(ctx, "sub_1")
	if err != nil || deleted.ID != "sub_1" {
		t.Fatalf("Delete = %+v (%v)", deleted, err)
	}
	if _, err := s.Get(ctx, "sub_1"); es.CodeOf(err) != es.ErrCodeNotFound {
		t.Errorf("post-delete Get err = %v, want SUBSCRIPTION_NOT_FOUND", err)
	}
	// ON DELETE CASCADE removed the delivery rows with the subscription.
	if rows, _ := s.ListDeliveries(ctx, "sub_1", 100); len(rows) != 0 {
		t.Errorf("deliveries survived subscription delete: %d", len(rows))
	}
}
