// SPDX-License-Identifier: MIT

package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore/pgstore"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
)

// warmPoolMigrations is the ordered sandbox_warm_pools schema chain plus its
// prerequisites (the initial schema and the RLS roles the 0033 GRANT
// references). Applying the subset rather than the full FS keeps the test off
// the pgvector-dependent migrations the embedded bundle lacks.
var warmPoolMigrations = []string{
	"0001_initial_schema.up.sql",
	"0002_rls_immutability_roles.up.sql",
	// 0022 adds runtime_definitions.task_policy, which 0167 renames to
	// session_policy alongside the pool column.
	"0022_runtime_task_policy.up.sql",
	"0033_sandbox_warm_pools.up.sql",
	"0040_warm_pool_concurrency.up.sql",
	"0079_sandbox_warm_pool_egress_profile.up.sql",
	"0083_pool_config_generation.up.sql",
	"0085_pool_task_policy.up.sql",
	"0110_pool_elicitation_policy.up.sql",
	"0120_pool_draining_since.up.sql",
	"0137_pool_bootstrap_min_warm.up.sql",
	"0151_pool_reconciliation_resume_epoch.up.sql",
	"0155_pool_sdk_warm_config.up.sql",
	"0157_pool_concurrent_max_pod_uptime.up.sql",
	"0167_runtime_definitions_execution_mode_service.up.sql",
	"0170_sandbox_warm_pools_acknowledge_nonce_only_auth.up.sql",
	"0171_sandbox_warm_pools_dns_policy.up.sql",
	"0177_sandbox_warm_pools_delivery_mode.up.sql",
}

// TestDeliveryModeFieldsPgRoundTrip_spec_4_9 brings up an embedded Postgres,
// applies the sandbox_warm_pools migration chain through 0177, and proves the
// §4.9 credential-delivery combination fields (delivery_mode, spiffe_binding,
// and the two deployer opt-in acknowledgments) round-trip through the
// Postgres-backed store's INSERT, SELECT, and UPDATE paths, including the
// empty / false defaults for a pool that sets none. A field dropped from the
// selectList, INSERT, UPDATE, or scan would make the pool-registration and
// admission layers read a stale or empty combination. It downloads the
// PostgreSQL bundle, so it is skipped under -short.
//
// spec: §4.9 (warm-pool admin/store model carries the credential-delivery
// combination).
//
// diagnosis: a failure means a credential-delivery field is not persisted on
// the create or update path or is not read back on scan, so the §4.9
// isolation-combination checks read a stale or empty value.
func TestDeliveryModeFieldsPgRoundTrip_spec_4_9(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15547,
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	defer func() { _ = pg.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	for _, name := range warmPoolMigrations {
		sql, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}

	s := pgstore.New(pool)

	// A pool that sets none of the four fields reads back with the empty /
	// false defaults the migration declares.
	if err := s.Create(ctx, poolstore.Pool{Name: "bare", RuntimeRef: "rt"}); err != nil {
		t.Fatalf("create bare pool: %v", err)
	}
	bare, err := s.Get(ctx, "bare")
	if err != nil {
		t.Fatalf("get bare pool: %v", err)
	}
	if bare.DeliveryMode != "" || bare.SpiffeBinding != "" ||
		bare.AllowDirectModeStandardIsolation || bare.AllowProxyModeSpiffeBindingDisabled {
		t.Errorf("bare pool carried non-default credential-delivery fields: %+v", bare)
	}

	// A pool that sets all four fields preserves every value through the
	// INSERT and SELECT.
	if err := s.Create(ctx, poolstore.Pool{
		Name:                                "set",
		RuntimeRef:                          "rt",
		DeliveryMode:                        "proxy",
		SpiffeBinding:                       "disabled",
		AllowDirectModeStandardIsolation:    true,
		AllowProxyModeSpiffeBindingDisabled: true,
	}); err != nil {
		t.Fatalf("create set pool: %v", err)
	}
	got, err := s.Get(ctx, "set")
	if err != nil {
		t.Fatalf("get set pool: %v", err)
	}
	if got.DeliveryMode != "proxy" || got.SpiffeBinding != "disabled" ||
		!got.AllowDirectModeStandardIsolation || !got.AllowProxyModeSpiffeBindingDisabled {
		t.Errorf("credential-delivery fields lost through INSERT/SELECT: %+v", got)
	}

	// UPDATE must persist a changed combination.
	updated, err := s.Update(ctx, "set", func(p *poolstore.Pool) error {
		p.DeliveryMode = "direct"
		p.SpiffeBinding = "enabled"
		p.AllowDirectModeStandardIsolation = false
		p.AllowProxyModeSpiffeBindingDisabled = false
		return nil
	})
	if err != nil {
		t.Fatalf("update set pool: %v", err)
	}
	if updated.DeliveryMode != "direct" || updated.SpiffeBinding != "enabled" ||
		updated.AllowDirectModeStandardIsolation || updated.AllowProxyModeSpiffeBindingDisabled {
		t.Errorf("credential-delivery fields not persisted through UPDATE: %+v", updated)
	}
	reGot, err := s.Get(ctx, "set")
	if err != nil {
		t.Fatalf("re-get set pool: %v", err)
	}
	if reGot.DeliveryMode != "direct" || reGot.SpiffeBinding != "enabled" {
		t.Errorf("updated credential-delivery fields not readable after UPDATE: %+v", reGot)
	}

	// List surfaces the fields on every row.
	pools, err := s.List(ctx, poolstore.ListFilter{})
	if err != nil {
		t.Fatalf("list pools: %v", err)
	}
	byName := map[string]poolstore.Pool{}
	for _, p := range pools {
		byName[p.Name] = p
	}
	if byName["set"].DeliveryMode != "direct" || byName["set"].SpiffeBinding != "enabled" {
		t.Errorf("list dropped credential-delivery fields: %+v", byName["set"])
	}
	if byName["bare"].DeliveryMode != "" || byName["bare"].SpiffeBinding != "" {
		t.Errorf("list surfaced non-default fields for the bare pool: %+v", byName["bare"])
	}
}
