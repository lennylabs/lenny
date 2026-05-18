// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"

	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// noopElector is the §25.4 Elector used when lenny-ops has no
// Kubernetes connection. It never grants leadership, so the
// leader-only background loops stay idle and the replica serves only
// the read-only HTTP surface. This keeps single-process local
// development working without a cluster.
type noopElector struct{}

// Run blocks until ctx is cancelled without ever invoking the
// leader-acquired callback.
func (noopElector) Run(ctx context.Context, _ func(context.Context), _ func()) {
	<-ctx.Done()
}

// IsLeader always reports false: a replica with no cluster connection
// never holds the lenny-ops-leader Lease.
func (noopElector) IsLeader() bool { return false }

// emptyEventSource is the §25.5 EventSource used until the Redis
// ops:events:stream consumer is wired. It yields no events, so the
// webhook delivery worker runs its loop and delivers nothing — the
// correct behavior before an event source exists.
type emptyEventSource struct{}

// Poll returns no events.
func (emptyEventSource) Poll(context.Context) ([]opsservice.WebhookEvent, error) {
	return nil, nil
}

// emptySubscriptionSource is the §25.5 SubscriptionSource used until
// the ops_event_subscriptions cache is wired. It yields no
// subscriptions; §25.5 cold-start behavior is that no webhook delivery
// occurs until the cache is populated.
type emptySubscriptionSource struct{}

// Subscriptions returns no subscriptions.
func (emptySubscriptionSource) Subscriptions() []opsservice.WebhookSubscription {
	return nil
}

// buildBackupService constructs the §25.11 BackupService and the
// scheduled-backup cron jobs the §25.4 cron evaluator runs.
//
// The §25.11 ops_backups Postgres store and the Kubernetes Job launcher
// are documented seams: the schema migration and the client-go Job
// launcher are wired as those land. Until then lenny-ops runs the
// in-memory store and launcher, so the §25.11 endpoints serve and an
// agent can exercise the API surface in a single-process degraded
// mode. The scheduled-backup loop runs only on the leader replica.
func buildBackupService(production bool) (*backup.Service, []opsservice.ScheduledJob) {
	store := backup.NewMemStore()
	svc, err := backup.NewService(backup.Config{
		Store:    store,
		Launcher: backup.NewFakeLauncher(),
		Locker:   backup.NewMemLocker(),
	})
	if err != nil {
		// NewService fails only on a missing dependency, all supplied
		// here; a failure is a programming error.
		log.Fatalf("lenny-ops: build backup service: %v", err)
	}

	// The §25.11 scheduled-backup cron jobs: a full backup daily at
	// 02:00 UTC, a Postgres backup every 6 hours, and the retention-
	// enforcement sweep daily at 03:30 UTC. The cron evaluator fires
	// these on the leader replica; each creates a Kubernetes Job through
	// the BackupService.
	jobs := []opsservice.ScheduledJob{
		{
			Name:       "backup-full",
			Expression: "0 2 * * *",
			Run: func(ctx context.Context) error {
				_, err := svc.CreateBackup(ctx, backup.BackupRequest{
					Type: string(backup.TypeFull), Confirm: true, Production: production,
				})
				return err
			},
		},
		{
			Name:       "backup-postgres",
			Expression: "0 */6 * * *",
			Run: func(ctx context.Context) error {
				_, err := svc.CreateBackup(ctx, backup.BackupRequest{
					Type: string(backup.TypePostgres),
				})
				return err
			},
		},
		{
			Name:       "backup-retention",
			Expression: "30 3 * * *",
			Run: func(ctx context.Context) error {
				pruned, err := svc.EnforceRetention(ctx)
				if err == nil && len(pruned) > 0 {
					log.Printf("lenny-ops: retention enforcement pruned %d backups", len(pruned))
				}
				return err
			},
		},
	}
	return svc, jobs
}
