//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §16.7 deployment_config_state baseline. Exercises
// pkg/gateway/deploymentconfigstore/pgstore against a real Postgres
// container with the production migrations (including 0163) applied:
// the not-found-before-first-Put contract, the Get/Put round-trip, the
// singleton upsert (a second Put overwrites the same row rather than
// inserting a second), and the scope CHECK that pins the table to one row.
// F-8.2.5, F-9.2.10, F-17.2.8.
package stores_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/deploymentconfigstore"
	deploymentconfigpg "github.com/lennylabs/lenny/pkg/gateway/deploymentconfigstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func newDeploymentConfigStore(t *testing.T) (*deploymentconfigpg.Store, *containers.Postgres) {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	return deploymentconfigpg.New(pg.Pool), pg
}

// spec: §16.7 — the baseline survives a gateway restart so the audit
// emitter diffs against a real prior value rather than re-emitting every
// transition as a first install.
func TestDeploymentConfigStorePgRoundTrip_spec_16_7(t *testing.T) {
	store, pg := newDeploymentConfigStore(t)
	ctx := context.Background()

	if _, found, err := store.Get(ctx); err != nil || found {
		t.Fatalf("fresh schema: found=%v err=%v, want found=false", found, err)
	}

	want := deploymentconfigstore.Config{
		CycleDetectionMode: "warn", AllowSelfRecursion: "yes",
		DefaultMaxDepth: 12, ElicitationFloor: "enforce", LastRevision: 3,
	}
	if err := store.Put(ctx, want); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, found, err := store.Get(ctx)
	if err != nil || !found {
		t.Fatalf("after put: found=%v err=%v", found, err)
	}
	if got != want {
		t.Errorf("round-trip = %+v, want %+v", got, want)
	}

	// A second Put upserts the single row (no duplicate insert).
	next := want
	next.ElicitationFloor = "detect-only"
	next.LastRevision = 4
	if err := store.Put(ctx, next); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got, _, _ := store.Get(ctx); got != next {
		t.Errorf("after upsert = %+v, want %+v", got, next)
	}

	var rows int
	if err := pg.Pool.QueryRow(ctx,
		`SELECT count(*) FROM deployment_config_state`).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Errorf("deployment_config_state holds %d rows, want the singleton 1", rows)
	}
}
