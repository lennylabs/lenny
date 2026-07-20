// SPDX-License-Identifier: MIT

package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	es "github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	"github.com/lennylabs/lenny/pkg/ops/eventsubscription/pgstore"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
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
	deliveries, _, err := s.ListDeliveries(ctx, "sub_1", "", 100)
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
	if rows, _, _ := s.ListDeliveries(ctx, "sub_1", "", 100); len(rows) != 2 {
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
	if rows, _, _ := s.ListDeliveries(ctx, "sub_1", "", 100); len(rows) != 0 {
		t.Errorf("deliveries survived subscription delete: %d", len(rows))
	}
}

// TestPgStoreListDeliveriesKeysetPagination_spec_25_5 pins the §25.5
// deliveries keyset cursor end to end against the durable store: the
// first page (empty cursor) returns the newest limit rows with a
// continuation cursor and hasMore, the continuation cursor returns the
// adjacent page in id-DESC order with no overlap, the final page reports
// hasMore:false with an empty cursor, and a cursor whose row has aged out
// below the oldest retained delivery reports gapDetected with
// oldestAvailableCursor rather than a silently empty page. It downloads
// the PostgreSQL bundle, so it is skipped under -short.
//
// spec: §25.5 (deliveries keyset pagination, gap on aged-out cursor).
// diagnosis: a failure means the durable deliveries keyset walk regressed
// (wrong page boundary, cursor, hasMore, or gap handling), so the
// paginated GET /v1/admin/event-subscriptions/{id}/deliveries endpoint
// returns overlapping, missing, or silently-truncated delivery history.
func TestPgStoreListDeliveriesKeysetPagination_spec_25_5(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15522,
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
	if err := s.Create(ctx, es.Record{
		ID: "sub_1", CallbackURL: "https://acme.example/hook",
		Types:      []string{"dev.lenny.alert_fired"},
		SecretHash: es.HashSecret(secret), SecretFingerprint: es.Fingerprint(secret),
		CreatedBy: "alice@acme.com", TenantFilter: es.TenantFilterAll,
		CreatedAt: now, UpdatedAt: now, Active: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Record five deliveries; the first four expire in the past so the
	// retention sweep can age them out, the fifth survives. The BIGSERIAL
	// ids are assigned 1..5 in insertion order.
	for i := 0; i < 5; i++ {
		exp := now.Add(-1 * time.Hour)
		if i == 4 {
			exp = now.Add(24 * time.Hour)
		}
		if _, err := s.RecordDelivery(ctx, es.Delivery{
			SubscriptionID: "sub_1", EventID: "e", EventType: "dev.lenny.alert_fired",
			Status: es.DeliveryDelivered, ExpiresAt: exp,
		}); err != nil {
			t.Fatalf("RecordDelivery %d: %v", i, err)
		}
	}

	// Walk the keyset in pages of two.
	page1, meta1, err := s.ListDeliveries(ctx, "sub_1", "", 2)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(page1) != 2 || page1[0].ID != 5 || page1[1].ID != 4 {
		t.Fatalf("first page ids = %v, want [5 4]", deliveryIDs(page1))
	}
	if !meta1.HasMore || meta1.Cursor != "4" || meta1.CursorKind != es.CursorKindPK {
		t.Fatalf("first page meta = %+v, want hasMore, cursor 4, cursorKind pk", meta1)
	}

	page2, meta2, err := s.ListDeliveries(ctx, "sub_1", meta1.Cursor, 2)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(page2) != 2 || page2[0].ID != 3 || page2[1].ID != 2 {
		t.Fatalf("second page ids = %v, want [3 2]", deliveryIDs(page2))
	}

	page3, meta3, err := s.ListDeliveries(ctx, "sub_1", meta2.Cursor, 2)
	if err != nil {
		t.Fatalf("final page: %v", err)
	}
	if len(page3) != 1 || page3[0].ID != 1 || meta3.HasMore || meta3.Cursor != "" {
		t.Fatalf("final page = %v meta %+v, want [1] with no more", deliveryIDs(page3), meta3)
	}

	// Age out the four expired rows; the oldest retained delivery is now id
	// 5. A continuation cursor of "4" (its row purged, below the retention
	// floor) reports a gap toward the oldest retained cursor.
	if purged, err := s.DeleteExpired(ctx, now, 100); err != nil || purged != 4 {
		t.Fatalf("DeleteExpired = %d (%v), want 4", purged, err)
	}
	aged, metaAged, err := s.ListDeliveries(ctx, "sub_1", "4", 2)
	if err != nil {
		t.Fatalf("aged-out page: %v", err)
	}
	if len(aged) != 0 || !metaAged.GapDetected || metaAged.OldestAvailableCursor != "5" {
		t.Fatalf("aged-out = %v meta %+v, want gapDetected with oldestAvailableCursor 5", deliveryIDs(aged), metaAged)
	}

	// A malformed (non-numeric) cursor cannot be honored. It reports a gap
	// toward the oldest retained delivery (id 5, the sole survivor) rather than
	// a silently empty page, so the caller resyncs from the retention floor.
	badCur, metaBad, err := s.ListDeliveries(ctx, "sub_1", "not-a-cursor", 2)
	if err != nil {
		t.Fatalf("malformed-cursor page: %v", err)
	}
	if len(badCur) != 0 || !metaBad.GapDetected || metaBad.OldestAvailableCursor != "5" {
		t.Fatalf("malformed cursor = %v meta %+v, want gapDetected with oldestAvailableCursor 5", deliveryIDs(badCur), metaBad)
	}

	// A malformed cursor for a subscription with no retained deliveries has no
	// retention floor to point at, so it reports no gap and an empty page rather
	// than a spurious gap signal.
	if err := s.Create(ctx, es.Record{
		ID: "sub_empty", CallbackURL: "https://acme.example/hook2",
		Types:      []string{"dev.lenny.alert_fired"},
		SecretHash: es.HashSecret(secret), SecretFingerprint: es.Fingerprint(secret),
		CreatedBy: "alice@acme.com", TenantFilter: es.TenantFilterAll,
		CreatedAt: now, UpdatedAt: now, Active: true,
	}); err != nil {
		t.Fatalf("Create(sub_empty): %v", err)
	}
	empties, metaEmpty, err := s.ListDeliveries(ctx, "sub_empty", "not-a-cursor", 2)
	if err != nil {
		t.Fatalf("malformed-cursor empty-subscription page: %v", err)
	}
	if len(empties) != 0 || metaEmpty.GapDetected || metaEmpty.OldestAvailableCursor != "" {
		t.Fatalf("malformed cursor over an empty subscription = %v meta %+v, want no gap", deliveryIDs(empties), metaEmpty)
	}
}

func deliveryIDs(ds []es.Delivery) []int64 {
	ids := make([]int64, len(ds))
	for i, d := range ds {
		ids[i] = d.ID
	}
	return ids
}
