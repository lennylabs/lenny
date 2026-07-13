// SPDX-License-Identifier: MIT

package registryservice_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	"github.com/lennylabs/lenny/pkg/ops/registryservice"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
)

// startRegistryPG brings up an embedded Postgres, applies migration 0135
// (the platform_registry_config singleton), and returns a live pool. The
// caller drives the durable PgStore against it. It downloads the
// PostgreSQL bundle, so callers skip under -short.
func startRegistryPG(t *testing.T) (context.Context, string) {
	t.Helper()
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         0, // ephemeral; §17.4 forbids hardcoded ports and they collide under parallel tests
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	t.Cleanup(func() { _ = pg.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	// platform_registry_config is platform-scoped (no FK / role deps), so
	// apply just the 0135 up migration directly.
	up, err := migrations.FS.ReadFile("0135_platform_registry_config.up.sql")
	if err != nil {
		t.Fatalf("read 0135: %v", err)
	}
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply 0135: %v", err)
	}
	return ctx, pg.DSN()
}

// TestPgStoreRoundTrip_spec_25_8 exercises the durable §25.8 registry
// override store. A PUT persists to Postgres and survives a restart: after
// saving an override the store loads every field back, a fresh PgStore
// over a new pool (the restart / leader-handoff case) still reads it, and
// a second Save upserts the same singleton row rather than accumulating
// rows. This pins the §25.8 line 3362 contract that a runtime registry
// update is "stored in Postgres" and is "a restart-free setting". It
// downloads the PostgreSQL bundle, so it is skipped under -short.
//
// spec: §25.8 line 3362 — "PUT /v1/admin/platform/registry updates the
// registry URL and overrides at runtime (stored in Postgres, takes effect
// on next image resolution). This is a restart-free setting."
func TestPgStoreRoundTrip_spec_25_8(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	ctx, dsn := startRegistryPG(t)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	store := registryservice.NewPgStore(pool)

	// Before any PUT the singleton is absent: Load is (zero, false, nil),
	// not an error, so Effective falls back to the chart base.
	if _, ok, err := store.Load(ctx); err != nil || ok {
		t.Fatalf("empty Load = (ok=%v, err=%v), want (false, nil)", ok, err)
	}

	updated := time.Date(2026, 6, 4, 12, 30, 0, 0, time.UTC)
	want := registryservice.Override{
		URL:            "my-registry.internal/lenny",
		Overrides:      map[string]string{"gateway": "mirror.internal/gw:pinned"},
		PullSecretName: "internal-pull",
		RequireDigest:  true,
		UpdatedAt:      updated,
		UpdatedBy:      "alice@acme.com",
	}
	if err := store.Save(ctx, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok, err := store.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("Load after Save = (ok=%v, err=%v), want (true, nil)", ok, err)
	}
	assertOverride(t, "same-pool Load", got, want)

	// Restart / leader handoff: a brand-new PgStore over a fresh pool sees
	// the same override. This is the durability the chart base cannot give.
	pool2, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer pool2.Close()
	reopened := registryservice.NewPgStore(pool2)
	got2, ok, err := reopened.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("Load after reconnect = (ok=%v, err=%v), want (true, nil)", ok, err)
	}
	assertOverride(t, "reopened Load", got2, want)

	// A second PUT upserts the singleton in place rather than inserting a
	// second row: Load returns the newer values and the table holds one row.
	next := registryservice.Override{
		URL:           "other-registry.internal/lenny",
		Overrides:     nil, // clearing overrides must round-trip as empty, not stale
		RequireDigest: false,
		UpdatedAt:     updated.Add(time.Hour),
		UpdatedBy:     "bob@acme.com",
	}
	if err := store.Save(ctx, next); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	got3, _, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load after second Save: %v", err)
	}
	if got3.URL != next.URL || got3.RequireDigest || got3.UpdatedBy != "bob@acme.com" {
		t.Errorf("upsert Load = %+v, want the second override", got3)
	}
	if len(got3.Overrides) != 0 {
		t.Errorf("cleared overrides = %v, want empty after upsert", got3.Overrides)
	}
	var rows int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM platform_registry_config").Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Errorf("row count = %d, want 1 (the singleton is upserted, not appended)", rows)
	}
}

// TestPgStoreDrivesImageResolution_spec_25_8 confirms the durable override
// "takes effect on next image resolution": a Service backed by the Postgres
// store resolves component images through the persisted override URL rather
// than the chart base, without a restart.
//
// spec: §25.8 line 3362 — a runtime registry update is "stored in Postgres,
// takes effect on next image resolution".
func TestPgStoreDrivesImageResolution_spec_25_8(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	ctx, dsn := startRegistryPG(t)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	svc := registryservice.New(registryservice.Options{
		Base: registryservice.EffectiveConfig{
			URL:            "ghcr.io/lennylabs",
			PullSecretName: "lenny-pull",
		},
		Store: registryservice.NewPgStore(pool),
		Now:   func() time.Time { return time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC) },
	})

	// Before the PUT the plan resolves against the chart base.
	plan, err := svc.ResolveImagePlan(ctx, "1.6.0", nil)
	if err != nil {
		t.Fatalf("base ResolveImagePlan: %v", err)
	}
	if plan["ops"] != "ghcr.io/lennylabs/lenny-ops:1.6.0" {
		t.Fatalf("base plan[ops] = %q", plan["ops"])
	}

	// A runtime PUT persists to Postgres; the next resolution reads it.
	if _, err := svc.Update(ctx, registryservice.UpdateRequest{
		URL:   "mirror.internal/lenny",
		Actor: "alice@acme.com",
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	plan2, err := svc.ResolveImagePlan(ctx, "1.6.0", nil)
	if err != nil {
		t.Fatalf("override ResolveImagePlan: %v", err)
	}
	if plan2["ops"] != "mirror.internal/lenny/lenny-ops:1.6.0" {
		t.Errorf("override plan[ops] = %q, want the mirror base", plan2["ops"])
	}

	// The override is durable: a Service rebuilt over a fresh pool (a
	// restart) resolves through the mirror without a re-PUT.
	pool2, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer pool2.Close()
	svc2 := registryservice.New(registryservice.Options{
		Base:  registryservice.EffectiveConfig{URL: "ghcr.io/lennylabs"},
		Store: registryservice.NewPgStore(pool2),
	})
	cfg, err := svc2.Effective(ctx)
	if err != nil {
		t.Fatalf("restarted Effective: %v", err)
	}
	if cfg.URL != "mirror.internal/lenny" || cfg.Source != "postgres" {
		t.Errorf("restarted effective = %+v, want the durable postgres override", cfg)
	}
}

func assertOverride(t *testing.T, where string, got, want registryservice.Override) {
	t.Helper()
	if got.URL != want.URL {
		t.Errorf("%s URL = %q, want %q", where, got.URL, want.URL)
	}
	if got.PullSecretName != want.PullSecretName {
		t.Errorf("%s PullSecretName = %q, want %q", where, got.PullSecretName, want.PullSecretName)
	}
	if got.RequireDigest != want.RequireDigest {
		t.Errorf("%s RequireDigest = %v, want %v", where, got.RequireDigest, want.RequireDigest)
	}
	if got.UpdatedBy != want.UpdatedBy {
		t.Errorf("%s UpdatedBy = %q, want %q", where, got.UpdatedBy, want.UpdatedBy)
	}
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("%s UpdatedAt = %v, want preserved %v", where, got.UpdatedAt, want.UpdatedAt)
	}
	if len(got.Overrides) != len(want.Overrides) {
		t.Errorf("%s Overrides = %v, want %v", where, got.Overrides, want.Overrides)
	}
	for k, v := range want.Overrides {
		if got.Overrides[k] != v {
			t.Errorf("%s Overrides[%s] = %q, want %q", where, k, got.Overrides[k], v)
		}
	}
}
