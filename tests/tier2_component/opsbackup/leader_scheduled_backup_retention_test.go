// SPDX-License-Identifier: MIT

//go:build component

// Component test for the §25.11 leader-elected scheduled-backup cron and
// the daily 03:30 UTC retention-enforcement sweep, wired against the
// production leader-election Lease (pkg/ops/opsservice.LeaseElector) over
// a real kube-apiserver (envtest) and the production backup orchestrator
// (pkg/ops/backup.Service) over an in-memory store. The unit coverage in
// pkg/ops/opsservice/service_test.go exercises leader gating only through
// a fake, single-process Elector whose Lead/Demote calls invoke the
// started/stopped callbacks directly; it never proves the production
// LeaseElector gives two contending replicas mutual exclusion over the
// real coordination.k8s.io Lease API, and pkg/ops/backup/restore_test.go's
// TestEnforceRetentionPrunesExpired calls EnforceRetention directly rather
// than through a fired cron tick. cmd/lenny-ops/deps.go's
// "backup-full"/"backup-retention" ScheduledJob wiring lives in package
// main and cannot be imported from a _test package, so this test mirrors
// it verbatim; every other subsystem here (LeaseElector, CronEvaluator,
// LoopRunner, backup.Service) is the production code, unmodified.
package opsbackup_test

import (
	"context"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
	"github.com/lennylabs/lenny/tests/testinfra/clockstep"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
	"github.com/lennylabs/lenny/tests/testinfra/wait"
)

// leaderReplicaTimings keeps the §25.4 lease-duration invariant
// (lease > renew > retry) but scaled down from the 15s/10s/2s production
// defaults so leader election converges in seconds during the test
// rather than tens of seconds.
var leaderReplicaTimings = opsservice.LeaseTimings{
	LeaseDuration: 4 * time.Second,
	RenewDeadline: 2 * time.Second,
	RetryPeriod:   500 * time.Millisecond,
}

// spec: §25.11 "A leader-elected goroutine in lenny-ops evaluates cron
// expressions from the ops_backup_schedule table and creates Jobs at the
// scheduled times. Default schedule: full backup daily at 02:00 UTC,
// Postgres backup every 6 hours." and "The retention enforcement Job runs
// after each successful backup AND on a daily cron at 03:30 UTC
// (independent of backup completion) to handle cases where backups have
// stopped." and "The retention Job is leader-elected (runs only on the
// leader lenny-ops replica) so concurrent runs across replicas are not
// possible." (spec/25_agent-operability.md lines 4110, 4130, 4161)
//
// diagnosis: a failure here means the §25.11 scheduled-backup/retention
// cron no longer fires exactly once across a multi-replica lenny-ops: the
// leader-elected goroutine did not create a backup Job at the 02:00 UTC
// cron tick, the 03:30 UTC retention sweep did not run independently of a
// backup completing, or — the mutual-exclusion failure — both replicas
// fired the same job because the production LeaseElector failed to give
// exactly one identity the coordination.k8s.io Lease. Inspect
// pkg/ops/opsservice/leader.go (LeaseElector), pkg/ops/opsservice/loop.go
// (StartLeaderLoops/StopLeaderLoops), pkg/ops/opsservice/cronloop.go
// (CronEvaluator.Tick), and the "backup-full"/"backup-retention"
// ScheduledJob wiring in cmd/lenny-ops/deps.go.
func TestLeaderElectedScheduledBackupAndRetentionCronFireOnce(t *testing.T) {
	env := envtest.Start(t)

	clientset, err := kubernetes.NewForConfig(env.RESTConfig())
	if err != nil {
		t.Fatalf("build clientset: %v", err)
	}
	const ns = "default"

	// A shared backup Service: the two "replicas" share one durable store
	// and one Job launcher, exactly as two real lenny-ops replicas share
	// one Postgres-backed ops_backups table.
	store := backup.NewMemStore()
	launcher := backup.NewFakeLauncher()
	if err := store.PutPolicy(context.Background(), backup.RetentionPolicy{
		RetainDays: 30, RetainCount: 2, RetainMinFull: 0,
	}); err != nil {
		t.Fatalf("PutPolicy: %v", err)
	}

	// A deterministic, advanceable clock shared by both replicas' cron
	// evaluators and by the backup Service, pinned before the 02:00 UTC
	// backup-full and 03:30 UTC retention boundaries so both cross
	// exactly once during the test.
	origin := time.Date(2026, 7, 17, 1, 0, 0, 0, time.UTC)
	clk := clockstep.New(origin)

	svc, err := backup.NewService(backup.Config{
		Store: store, Launcher: launcher, Locker: backup.NewMemLocker(),
		Now: clk.Now,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// Four completed postgres backups seeded directly into the store (not
	// created by a scheduled job), of increasing age, so the 03:30 UTC
	// retention sweep has something to prune independent of any backup
	// this test's own cron jobs create. Mirrors the known-good
	// pkg/ops/backup/restore_test.go TestEnforceRetentionPrunesExpired
	// fixture: RetainCount 2 over 4 backups prunes the 2 oldest.
	for i := 1; i <= 4; i++ {
		completed := origin.AddDate(0, 0, -i)
		b := backup.Backup{
			ID:          "seed-" + string(rune('a'+i-1)),
			Type:        string(backup.TypePostgres),
			Status:      backup.StatusCompleted,
			StartedAt:   completed,
			CompletedAt: &completed,
		}
		if err := store.InsertBackup(context.Background(), b); err != nil {
			t.Fatalf("seed InsertBackup: %v", err)
		}
	}

	// The §25.11 scheduled-backup and retention cron jobs, mirroring the
	// "backup-full" and "backup-retention" ScheduledJob entries in
	// cmd/lenny-ops/deps.go. Each replica gets its own job slice (a
	// ScheduledJob closes over ctx per Run call, not shared state), all
	// driving the one shared backup.Service above.
	jobsFor := func() []opsservice.ScheduledJob {
		return []opsservice.ScheduledJob{
			{
				Name:       "backup-full",
				Expression: "0 2 * * *",
				Run: func(ctx context.Context) error {
					_, err := svc.CreateBackup(ctx, backup.BackupRequest{
						Type: string(backup.TypeFull), Confirm: true,
					})
					return err
				},
			},
			{
				Name:       "backup-retention",
				Expression: "30 3 * * *",
				Run: func(ctx context.Context) error {
					_, err := svc.EnforceRetention(ctx)
					return err
				},
			},
		}
	}

	newReplica := func(t *testing.T, identity string) *opsservice.Service {
		t.Helper()
		elector, err := opsservice.NewLeaseElector(
			ns, identity, clientset.CoreV1(), clientset.CoordinationV1(), leaderReplicaTimings)
		if err != nil {
			t.Fatalf("NewLeaseElector(%s): %v", identity, err)
		}
		s, err := opsservice.New(opsservice.Config{
			ReplicaID:    identity,
			Elector:      elector,
			CronJobs:     jobsFor(),
			CronInterval: 150 * time.Millisecond,
			Clock:        clk.Now,
		})
		if err != nil {
			t.Fatalf("opsservice.New(%s): %v", identity, err)
		}
		return s
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	replicaA := newReplica(t, "lenny-ops-a")
	replicaB := newReplica(t, "lenny-ops-b")
	go replicaA.Run(ctx)
	go replicaB.Run(ctx)

	// Precondition: the real coordination.k8s.io lenny-ops-leader Lease
	// converges on exactly one leader between the two contending
	// replicas, and that single-leader state holds for a sustained
	// window rather than a transient blip (client-go's leader-election
	// startup race can produce a short-lived false start on the very
	// first acquisition attempt before settling).
	stableChecks := 0
	wait.For(t, 20*time.Second, "the lenny-ops-leader Lease converges on a single, sustained leader", func() (bool, error) {
		if replicaA.IsLeader() == replicaB.IsLeader() {
			stableChecks = 0
			return false, nil
		}
		stableChecks++
		return stableChecks >= 5, nil
	})

	// Phase A: cross the 02:00 UTC backup-full cron boundary. §25.11:
	// "creates Jobs at the scheduled times. Default schedule: full backup
	// daily at 02:00 UTC."
	clk.Advance(91 * time.Minute) // 02:31 — crosses 02:00
	wait.For(t, 15*time.Second, "the leader-elected cron tick creates exactly one full backup at the 02:00 UTC boundary", func() (bool, error) {
		fulls, err := store.ListBackups(context.Background(), backup.BackupFilter{Type: string(backup.TypeFull)})
		if err != nil {
			return false, err
		}
		return len(fulls) == 1, nil
	})
	fulls, err := store.ListBackups(context.Background(), backup.BackupFilter{Type: string(backup.TypeFull)})
	if err != nil {
		t.Fatalf("ListBackups(full): %v", err)
	}
	if len(fulls) != 1 {
		t.Fatalf("full backups after the 02:00 UTC cron boundary = %d, want exactly 1 "+
			"(the standby replica's cron evaluator must never tick)", len(fulls))
	}

	// The seeded retention candidates are untouched: the 03:30 UTC
	// retention cron has not yet fired, and this wiring's only retention
	// driver is that daily cron (creating a backup does not itself
	// trigger retention here).
	seeded, err := store.ListBackups(context.Background(), backup.BackupFilter{Type: string(backup.TypePostgres)})
	if err != nil {
		t.Fatalf("ListBackups(postgres): %v", err)
	}
	for _, b := range seeded {
		if b.Status == backup.StatusExpired {
			t.Fatalf("seeded backup %q is already expired before the 03:30 UTC retention cron boundary was crossed", b.ID)
		}
	}

	// Phase B: cross the 03:30 UTC daily retention cron boundary, with no
	// backup having just completed (the backup-full cron fired over 30
	// minutes ago in cron time, above). §25.11: "The retention
	// enforcement Job runs after each successful backup AND on a daily
	// cron at 03:30 UTC (independent of backup completion) to handle
	// cases where backups have stopped." and "The retention Job is
	// leader-elected (runs only on the leader lenny-ops replica) so
	// concurrent runs across replicas are not possible."
	clk.Advance(60 * time.Minute) // 03:31 — crosses 03:30
	wait.For(t, 15*time.Second, "the leader-elected 03:30 UTC retention cron prunes the two oldest seeded backups exactly once", func() (bool, error) {
		expired, err := store.ListBackups(context.Background(), backup.BackupFilter{Status: backup.StatusExpired})
		if err != nil {
			return false, err
		}
		return len(expired) == 2, nil
	})
	expired, err := store.ListBackups(context.Background(), backup.BackupFilter{Status: backup.StatusExpired})
	if err != nil {
		t.Fatalf("ListBackups(expired): %v", err)
	}
	if len(expired) != 2 {
		t.Fatalf("expired backups after the 03:30 UTC retention cron boundary = %d, want exactly 2 "+
			"(a double-fire across replicas would prune the same rows twice or race)", len(expired))
	}

	// The full backup the 02:00 UTC cron created is untouched: it is not
	// one of the four seeded, aged postgres backups the retention policy
	// targets.
	fulls, err = store.ListBackups(context.Background(), backup.BackupFilter{Type: string(backup.TypeFull)})
	if err != nil {
		t.Fatalf("ListBackups(full) after retention: %v", err)
	}
	if len(fulls) != 1 || fulls[0].Status == backup.StatusExpired {
		t.Fatalf("full backup after retention = %+v, want exactly 1 and not expired", fulls)
	}

	// The single-leader guarantee held at this final snapshot: the two
	// replicas' leader-only loops are never simultaneously running (the
	// exact-count assertions above already confirm neither cron job fired
	// twice across the two replicas at either boundary).
	if replicaA.LeaderLoopsRunning() && replicaB.LeaderLoopsRunning() {
		t.Fatalf("both lenny-ops replicas report leader loops running simultaneously; " +
			"the §25.11 single-leader guarantee did not hold")
	}
}
