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
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
)

// TestRuntimeProcessSessionPolicyPgRoundTrip_spec_5_2 brings up an embedded
// Postgres, applies the sandbox_warm_pools migration chain, and proves that
// sessionPolicy.recycle.runtimeProcess round-trips through the
// session_policy JSONB column on the INSERT, SELECT, UPDATE, and List paths,
// and that a pool without the field reads it back empty. A key dropped from
// the column's JSON would make every reusing recycling pool fail the
// required-field rule on its next update and the WarmPoolController create
// pods without the pool's lifetime. It downloads the PostgreSQL bundle, so
// it is skipped under -short.
//
// spec: §5.2 (Runtime-process lifetime)
//
// diagnosis: a failure means recycle.runtimeProcess is not persisted in
// session_policy on create or update or is not read back on scan.
func TestRuntimeProcessSessionPolicyPgRoundTrip_spec_5_2(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15549,
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

	recycling := func(lifetime runtimestore.RuntimeProcessLifetime, maxSessions int) *runtimestore.SessionPolicy {
		return &runtimestore.SessionPolicy{
			AcknowledgeProcessLevelIsolation: true,
			Recycle: &runtimestore.RecyclePolicy{
				Enabled: true, AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod: maxSessions, RuntimeProcess: lifetime,
			},
		}
	}
	if err := s.Create(ctx, poolstore.Pool{Name: "keep", RuntimeRef: "rt", SessionPolicy: recycling(runtimestore.RuntimeProcessKeep, 5)}); err != nil {
		t.Fatalf("create keep pool: %v", err)
	}
	if err := s.Create(ctx, poolstore.Pool{Name: "single", RuntimeRef: "rt", SessionPolicy: recycling("", 1)}); err != nil {
		t.Fatalf("create one-session pool: %v", err)
	}
	got, err := s.Get(ctx, "keep")
	if err != nil {
		t.Fatalf("get keep pool: %v", err)
	}
	if got.SessionPolicy == nil || got.SessionPolicy.Recycle == nil ||
		got.SessionPolicy.Recycle.RuntimeProcess != runtimestore.RuntimeProcessKeep {
		t.Fatalf("runtimeProcess lost through INSERT/SELECT: %+v", got.SessionPolicy)
	}
	single, err := s.Get(ctx, "single")
	if err != nil {
		t.Fatalf("get one-session pool: %v", err)
	}
	if single.SessionPolicy.Recycle.RuntimeProcess != "" {
		t.Errorf("a pool without the field read back %q", single.SessionPolicy.Recycle.RuntimeProcess)
	}

	// An UPDATE that takes the pool off reuse must drop the field, which the
	// presence rule requires, and the change must persist.
	if _, err := s.Update(ctx, "keep", func(p *poolstore.Pool) error {
		p.SessionPolicy.Recycle.MaxSessionsPerPod = 1
		p.SessionPolicy.Recycle.RuntimeProcess = ""
		return nil
	}); err != nil {
		t.Fatalf("update keep pool: %v", err)
	}
	if _, err := s.Update(ctx, "single", func(p *poolstore.Pool) error {
		p.SessionPolicy.Recycle.MaxSessionsPerPod = 3
		p.SessionPolicy.Recycle.RuntimeProcess = runtimestore.RuntimeProcessKeep
		return nil
	}); err != nil {
		t.Fatalf("update one-session pool: %v", err)
	}
	pools, err := s.List(ctx, poolstore.ListFilter{})
	if err != nil {
		t.Fatalf("list pools: %v", err)
	}
	byName := map[string]poolstore.Pool{}
	for _, p := range pools {
		byName[p.Name] = p
	}
	if byName["keep"].SessionPolicy.Recycle.RuntimeProcess != "" {
		t.Errorf("UPDATE did not clear runtimeProcess: %+v", byName["keep"].SessionPolicy.Recycle)
	}
	if byName["single"].SessionPolicy.Recycle.RuntimeProcess != runtimestore.RuntimeProcessKeep {
		t.Errorf("UPDATE did not persist runtimeProcess: %+v", byName["single"].SessionPolicy.Recycle)
	}
}
