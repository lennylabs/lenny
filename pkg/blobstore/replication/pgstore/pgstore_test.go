// SPDX-License-Identifier: MIT

package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	"github.com/lennylabs/lenny/pkg/blobstore/replication"
	"github.com/lennylabs/lenny/pkg/blobstore/replication/pgstore"
	embpostgres "github.com/lennylabs/lenny/pkg/embedded/postgres"
)

// TestReplicationStateRoundTrip brings up an embedded Postgres, applies
// migration 0126, and exercises the §25.11 ops_artifact_replication_state
// upsert/read lifecycle, including the suspended_residency_violation state
// that must survive a restart. It downloads the PostgreSQL bundle, so it is
// skipped under -short.
//
// spec: §25.11 lines 4073-4098.
func TestReplicationStateRoundTrip_spec_25_11(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15519,
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
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	up, err := migrations.FS.ReadFile("0126_ops_artifact_replication_state.up.sql")
	if err != nil {
		t.Fatalf("read 0126: %v", err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply 0126: %v", err)
	}

	s := pgstore.New(pool)

	// An absent region reads (zero, false, nil).
	if _, ok, err := s.GetReplicationState(ctx, "us-east-1"); err != nil || ok {
		t.Fatalf("GetReplicationState(absent) = ok=%v err=%v, want false/nil", ok, err)
	}

	// An active region with no suspension (zero suspended_since → NULL).
	active := replication.RegionState{
		Region:                     "us-east-1",
		State:                      replication.StateActive,
		LastPreflightAt:            time.Date(2026, 6, 3, 1, 0, 0, 0, time.UTC),
		LastPreflightResult:        "ok",
		DestinationEndpoint:        "minio-dr:9000",
		DestinationBucket:          "lenny-dr",
		DestinationJurisdictionTag: "us-east-1",
		ReplicationLagSeconds:      12,
	}
	if err := s.PutReplicationState(ctx, active); err != nil {
		t.Fatalf("PutReplicationState(active): %v", err)
	}
	got, ok, err := s.GetReplicationState(ctx, "us-east-1")
	if err != nil || !ok {
		t.Fatalf("GetReplicationState(active) = ok=%v err=%v", ok, err)
	}
	if got.State != replication.StateActive || got.ReplicationLagSeconds != 12 || got.DestinationBucket != "lenny-dr" {
		t.Errorf("active round-trip = %+v", got)
	}
	if !got.SuspendedSince.IsZero() {
		t.Errorf("active suspendedSince = %v, want zero", got.SuspendedSince)
	}
	if !got.LastPreflightAt.Equal(active.LastPreflightAt) {
		t.Errorf("lastPreflightAt round-trip = %v", got.LastPreflightAt)
	}

	// A fail-closed suspension upserts the same region; it must persist.
	suspended := active
	suspended.State = replication.StateSuspendedResidencyViolation
	suspended.LastPreflightResult = "jurisdiction tag mismatch"
	suspended.SuspendedSince = time.Date(2026, 6, 3, 2, 0, 0, 0, time.UTC)
	if err := s.PutReplicationState(ctx, suspended); err != nil {
		t.Fatalf("PutReplicationState(suspended): %v", err)
	}
	got2, _, _ := s.GetReplicationState(ctx, "us-east-1")
	if got2.State != replication.StateSuspendedResidencyViolation {
		t.Errorf("suspended state = %q, want suspended_residency_violation", got2.State)
	}
	if got2.SuspendedSince.IsZero() || !got2.SuspendedSince.Equal(suspended.SuspendedSince) {
		t.Errorf("suspendedSince round-trip = %v", got2.SuspendedSince)
	}
}
