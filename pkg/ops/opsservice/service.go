// SPDX-License-Identifier: MIT

// Package opsservice is the running body of the §25.4 lenny-ops
// service. The pkg/ops/opsserver package carries the HTTP routing;
// this package carries the parts the spec's "lenny-ops Service"
// section describes around it: Kubernetes Lease-based leader election,
// the leader-only background loops (the cron evaluator, the webhook
// delivery worker, the scheduled-backup runner, and the reconciliation
// goroutines), and the self-monitor every replica runs.
//
// lenny-ops runs as a Deployment with one or more replicas. Only the
// replica holding the lenny-ops-leader Lease runs the singleton
// background loops; every replica serves the read-only HTTP surface
// and runs its own self-health monitor. The Service wires the loops to
// the leader-election callbacks: acquiring leadership starts the
// leader-only loops, losing it stops them.
package opsservice

import (
	"context"
	"log"
	"sync"
	"time"
)

// Config assembles the §25.4 lenny-ops service body. The HTTP surface
// is built separately (pkg/ops/opsserver); this Config covers leader
// election, the background loops, and self-monitoring.
type Config struct {
	// ReplicaID identifies this replica in logs and self-health events
	// (the pod name).
	ReplicaID string
	// Elector drives §25.4 leader election. Required.
	Elector Elector
	// CronJobs are the §25.4 cron evaluator's scheduled operations
	// (scheduled backups, backup verifications, platform_upgrade_check).
	CronJobs []ScheduledJob
	// CronInterval is how often the cron evaluator wakes to check
	// schedules. It must be at or below the cron resolution (one minute)
	// so no scheduled time is skipped; a zero value defaults to one
	// minute.
	CronInterval time.Duration
	// Webhook, when set, runs the §25.5 webhook delivery worker as a
	// leader-only loop.
	Webhook *WebhookWorker
	// WebhookInterval is how often the webhook worker polls for new
	// events. A zero value defaults to two seconds.
	WebhookInterval time.Duration
	// Reconcilers are the §25.4 leader-only reconciliation goroutines.
	Reconcilers Reconcilers
	// SelfHealthChecks are the §25.4 self-health checks the self-monitor
	// runs on every replica.
	SelfHealthChecks map[string]SelfCheck
	// SelfHealthInterval is the §25.4 ops.selfHealth.checkIntervalSeconds.
	// A zero value defaults to ten seconds.
	SelfHealthInterval time.Duration
	// OnSelfHealthChange, when non-nil, is invoked when the aggregate
	// self-health status transitions, so the caller can emit the §25.4
	// ops_health_status_changed event.
	OnSelfHealthChange func(prev, next SelfHealthReport)
	// Clock supplies the current time for the cron evaluator; nil uses
	// time.Now.
	Clock func() time.Time
}

// Service is the running §25.4 lenny-ops service body: leader
// election, the leader-only background loops, and the per-replica
// self-monitor.
type Service struct {
	replicaID string
	elector   Elector
	loops     *LoopRunner
	monitor   *SelfHealthMonitor

	mu      sync.Mutex
	started bool
}

// New builds the §25.4 lenny-ops service body from cfg. It assembles
// the loop set — the cron evaluator, the webhook delivery worker, the
// reconciliation goroutines, and the self-monitor — and returns an
// error when a cron expression in cfg.CronJobs does not parse.
func New(cfg Config) (*Service, error) {
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}

	monitor := NewSelfHealthMonitor(cfg.ReplicaID, cfg.SelfHealthChecks, cfg.OnSelfHealthChange)

	var loops []Loop

	// The §25.4 cron evaluator: a leader-only loop driving pkg/cron.
	if len(cfg.CronJobs) > 0 {
		evaluator, err := NewCronEvaluator(clock, cfg.CronJobs...)
		if err != nil {
			return nil, err
		}
		interval := cfg.CronInterval
		if interval <= 0 {
			interval = time.Minute
		}
		loops = append(loops, Loop{
			Name:       "cron-evaluator",
			Interval:   interval,
			Tick:       evaluator.Tick,
			LeaderOnly: true,
		})
	}

	// The §25.5 webhook delivery worker: a leader-only loop.
	if cfg.Webhook != nil {
		interval := cfg.WebhookInterval
		if interval <= 0 {
			interval = 2 * time.Second
		}
		loops = append(loops, Loop{
			Name:       "webhook-delivery",
			Interval:   interval,
			Tick:       cfg.Webhook.Tick,
			LeaderOnly: true,
		})
	}

	// The §25.4 reconciliation goroutines: leader-only loops.
	loops = append(loops, cfg.Reconcilers.loops()...)

	// The §25.4 self-monitor: every replica runs it, leader or not.
	selfInterval := cfg.SelfHealthInterval
	if selfInterval <= 0 {
		selfInterval = 10 * time.Second
	}
	loops = append(loops, Loop{
		Name:     "self-monitor",
		Interval: selfInterval,
		Tick: func(context.Context) error {
			monitor.Evaluate()
			return nil
		},
		LeaderOnly: false,
	})

	return &Service{
		replicaID: cfg.ReplicaID,
		elector:   cfg.Elector,
		loops:     NewLoopRunner(loops...),
		monitor:   monitor,
	}, nil
}

// Monitor returns the §25.4 self-health monitor so the HTTP surface
// can serve GET /v1/admin/ops/health from the latest report. The
// monitor satisfies opsserver.SelfHealthReporter.
func (s *Service) Monitor() *SelfHealthMonitor { return s.monitor }

// IsLeader reports whether this replica currently holds the §25.4
// leader-election lease.
func (s *Service) IsLeader() bool { return s.elector.IsLeader() }

// LoopNames returns the names of every background loop, for operator
// introspection.
func (s *Service) LoopNames() []string { return s.loops.Loops() }

// LeaderLoopsRunning reports whether the leader-only loops are active
// on this replica.
func (s *Service) LeaderLoopsRunning() bool { return s.loops.Running() }

// Run starts the §25.4 service body and blocks until ctx is cancelled.
// It starts the per-replica loops (the self-monitor) immediately, then
// runs leader election: the leader-only loops start when this replica
// acquires the lease and stop when it loses it. On ctx cancellation it
// stops every loop and waits for the loop goroutines to return.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()

	// The self-monitor runs on every replica regardless of leadership.
	s.loops.StartReplicaLoops(ctx)

	// Leader election gates the singleton loops: OnStartedLeading starts
	// them, OnStoppedLeading stops them and waits for them to drain so a
	// new leader does not run a singleton concurrently.
	s.elector.Run(
		ctx,
		func(leaderCtx context.Context) {
			log.Printf("lenny-ops: replica %s acquired leadership; starting %d-loop leader set",
				s.replicaID, len(leaderOnly(s.loops)))
			s.loops.StartLeaderLoops(leaderCtx)
		},
		func() {
			log.Printf("lenny-ops: replica %s lost leadership; stopping leader loops", s.replicaID)
			s.loops.StopLeaderLoops()
		},
	)

	// ctx is cancelled: election has returned. Stop any still-running
	// leader loops and wait for every loop goroutine to finish.
	s.loops.StopLeaderLoops()
	s.loops.Wait()
}

// leaderOnly counts the leader-gated loops for the startup log line.
func leaderOnly(r *LoopRunner) []string {
	var names []string
	for _, l := range r.loops {
		if l.LeaderOnly {
			names = append(names, l.Name)
		}
	}
	return names
}
