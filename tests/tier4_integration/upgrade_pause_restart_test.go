//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test for the §25.8 durability-across-restart and
// no-auto-resume guarantee of a paused platform upgrade. The upgrade
// state lives in the platform_upgrade_state Postgres row, so a lenny-ops
// restart or leader-election handoff during a paused upgrade is harmless:
// a fresh orchestrator built over the same durable pool models the new
// leader, reads the persisted phase, waits indefinitely without
// auto-resuming across an arbitrarily long clock advance, and advances
// only when an explicit proceed arrives. The existing pgstore round-trip
// test covers only the storage layer; this test drives the guarantee
// through the real upgradeservice.Service across a simulated leader
// handoff.
package tier4_integration_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	upgradepgstore "github.com/lennylabs/lenny/pkg/ops/upgradeservice/pgstore"
	"github.com/lennylabs/lenny/pkg/upgrade"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// mutableClock is a hand-advanced clock so the test can simulate an
// arbitrarily long pause (hours to days) without wall-clock waiting. It
// is safe for concurrent use, though the test drives it sequentially.
type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// spec: §25.8 line 3564 ("Behavior across long pauses (hours to days).
// The upgrade state lives in the platform_upgrade_state Postgres row, not
// in process memory — a lenny-ops restart or leader-election change
// during a paused upgrade is harmless. When a new leader takes over
// (after lease expiry following a pod restart), it reads the current
// phase and resumes from there only when an explicit proceed is received.
// Long pauses do NOT auto-resume — the state machine waits
// indefinitely.").
//
// diagnosis: a failure means a paused platform upgrade does not survive a
// lenny-ops restart / leader handoff across durable Postgres, or the new
// leader auto-resumes the state machine without an explicit proceed.
// Either the phase was not read back from platform_upgrade_state by a
// fresh orchestrator (durability broken), the paused phase advanced on
// its own across a long clock advance (no-auto-resume invariant broken),
// or the explicit proceed did not resume from the persisted phase. Any of
// these violates the §25.8 pause-and-resume contract an operator relies
// on when parking an upgrade between phases for hours to days.
//
// TestUpgradePauseSurvivesLeaderHandoff drives a real upgradeservice.Service
// over a durable Postgres pool to a mid-upgrade paused phase, discards
// that orchestrator to model a lenny-ops restart, builds a fresh
// orchestrator over the same pool as the new leader, asserts it reports
// the persisted paused phase, advances the injected clock far past any
// pause horizon and asserts the phase does not auto-resume, then confirms
// an explicit proceed resumes the state machine from the persisted phase.
func TestUpgradePauseSurvivesLeaderHandoff(t *testing.T) {
	ctx := context.Background()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	pool := pg.Pool

	clock := &mutableClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}

	// The outgoing leader: drive the upgrade to a mid-flight phase and pause
	// it. Every mutation is persisted to the platform_upgrade_state
	// singleton through the durable pgstore.
	outgoing := upgradeservice.New(upgradeservice.Options{
		Store: upgradepgstore.New(pool),
		Now:   clock.Now,
		NewID: func() string { return "upgrade-pause-handoff" },
	})
	if _, err := outgoing.Start(ctx, upgradeservice.StartRequest{TargetVersion: "1.6.0"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st, err := outgoing.Proceed(ctx); err != nil { // Preflight -> OpsRoll
		t.Fatalf("Proceed to OpsRoll: %v", err)
	} else if st.Phase != upgrade.OpsRoll {
		t.Fatalf("phase after first Proceed = %s, want OpsRoll", st.Phase)
	}
	if st, err := outgoing.Proceed(ctx); err != nil { // OpsRoll -> CRDUpdate
		t.Fatalf("Proceed to CRDUpdate: %v", err)
	} else if st.Phase != upgrade.CRDUpdate {
		t.Fatalf("phase after second Proceed = %s, want CRDUpdate", st.Phase)
	}
	const pauseReason = "parked for the maintenance window"
	if st, err := outgoing.Pause(ctx, pauseReason); err != nil {
		t.Fatalf("Pause: %v", err)
	} else if !st.Paused || st.Phase != upgrade.CRDUpdate {
		t.Fatalf("state after Pause = phase %s paused=%v, want CRDUpdate paused", st.Phase, st.Paused)
	}

	// Model a lenny-ops restart / leader-election change: drop the outgoing
	// orchestrator entirely and build a fresh one over the same durable
	// pool. The new leader shares no in-process state with the old one.
	outgoing = nil
	newLeader := upgradeservice.New(upgradeservice.Options{
		Store: upgradepgstore.New(pool),
		Now:   clock.Now,
		// A distinct NewID: if the new leader wrongly minted a new upgrade
		// instead of reading the persisted one, the operation id would change.
		NewID: func() string { return "upgrade-should-not-be-used" },
	})

	// Durability: the new leader reads the paused phase from Postgres.
	st, ok, err := newLeader.Status(ctx)
	if err != nil || !ok {
		t.Fatalf("new leader Status = (ok=%v, err=%v), want a persisted upgrade", ok, err)
	}
	if st.Phase != upgrade.CRDUpdate {
		t.Fatalf("new leader phase = %s, want CRDUpdate (paused phase must survive the handoff)", st.Phase)
	}
	if !st.Paused {
		t.Fatalf("new leader paused = false, want true (paused state must survive the handoff)")
	}
	if st.OperationID != "upgrade-pause-handoff" {
		t.Fatalf("new leader operation id = %q, want the persisted upgrade-pause-handoff", st.OperationID)
	}
	if st.Reason != pauseReason {
		t.Fatalf("new leader pause reason = %q, want %q", st.Reason, pauseReason)
	}

	// No auto-resume: advance the clock far past any pause horizon (30 days,
	// well beyond the hours-to-days the spec names and the 7d idempotency
	// window). The state machine must wait indefinitely — the phase does not
	// advance on its own.
	clock.advance(30 * 24 * time.Hour)
	st, ok, err = newLeader.Status(ctx)
	if err != nil || !ok {
		t.Fatalf("Status after clock advance = (ok=%v, err=%v)", ok, err)
	}
	if st.Phase != upgrade.CRDUpdate || !st.Paused {
		t.Fatalf("after a 30-day pause the upgrade auto-resumed: phase %s paused=%v, want CRDUpdate paused", st.Phase, st.Paused)
	}

	// Resume only on an explicit proceed: the new leader advances the state
	// machine from the persisted phase, not from the beginning.
	resumed, err := newLeader.Proceed(ctx) // CRDUpdate -> SchemaMigration
	if err != nil {
		t.Fatalf("Proceed on the new leader: %v", err)
	}
	if resumed.Phase != upgrade.SchemaMigration {
		t.Fatalf("phase after explicit proceed = %s, want SchemaMigration (resume from the persisted phase)", resumed.Phase)
	}
	if resumed.OperationID != "upgrade-pause-handoff" {
		t.Fatalf("resumed operation id = %q, want the persisted upgrade-pause-handoff", resumed.OperationID)
	}
}
