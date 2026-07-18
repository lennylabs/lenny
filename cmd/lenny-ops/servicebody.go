// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/alerting/alertingmetrics"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// buildServiceBody constructs the §25.4 service body: the bundle-rules
// reconciler and the opsservice.Service that runs leader election, the
// §25.11 scheduled-backup and §25.8 platform-upgrade-check cron jobs, the
// §25.4 leader-only reconciliation goroutines, and the §25.4 self-health
// monitor. spec: §25.4.
func (w *opsWiring) buildServiceBody() {
	// §25.4 line 1339 — the bundleRules reconciler. §25.13 line 4816
	// makes the bundled alerting rules static manifests rendered at Helm
	// install/upgrade time with no runtime mutation, so the leader-only
	// reconciler does not re-render rules; it keeps the §25.13 bundled-rules
	// observability gauges (lenny_alerting_rules_bundled{format} and
	// lenny_alerting_rule_overrides) current on the lenny-ops /metrics
	// surface from the chart-supplied bundle format + override count.
	alertingMx, err := alertingmetrics.New(prometheus.DefaultRegisterer)
	if err != nil {
		log.Fatalf("lenny-ops: register §25.13 alerting metrics: %v", err)
	}
	w.bundleRulesReconcile = bundleRulesReconciler(alertingMx, splitAndTrim(*w.f.alertingBundleFormats), *w.f.alertingOverrideCount)

	// The §25.4 service body: leader election plus the background loops.
	// The §25.11 scheduled-backup cron jobs and the §25.8
	// platform-upgrade-check cron register here; the §25.4 leader-only
	// reconciliation goroutines (escalation flush, idempotency cleanup,
	// lock outage-epoch reconcile, drift snapshot validation) register via
	// Reconcilers. F-25.4.16.
	svc, err := opsservice.New(opsservice.Config{
		ReplicaID: w.replicaID,
		Elector:   w.elector,
		Webhook:   w.webhook,
		CronJobs:  w.cronJobs,
		// §25.4 line 1337: the leader-only reconciliation goroutines. Each
		// runs only on the replica holding the lenny-ops-leader Lease, so a
		// multi-replica deployment drives one flush/cleanup/reconcile loop,
		// not one per replica.
		Reconcilers: opsservice.Reconcilers{
			// §25.4 lines 2407-2415: drain the in-memory escalation buffer up
			// to a recovered durable tier (preserving the authoring timestamp
			// and the emitted flag). F-25.4.7.
			EscalationFlush: func(ctx context.Context) error {
				n, err := w.escalationSvc.Flush(ctx)
				if err == nil && n > 0 {
					log.Printf("lenny-ops: escalation flush promoted %d buffered escalation(s) to durable storage", n)
				}
				return err
			},
			// §25.4 lines 2404, 2429: re-attempt the escalation_created
			// publish for any record whose emitted flag is still false, so an
			// escalation created during a dual Redis-plus-gateway-buffer
			// outage is emitted once a destination recovers. F-REL-1.
			EscalationEmissionRetry: func(ctx context.Context) error {
				if n := w.escalationSvc.RetryEmission(ctx); n > 0 {
					log.Printf("lenny-ops: escalation emission retry published %d previously-unemitted escalation(s)", n)
				}
				return nil
			},
			// §25.4 lines 2070-2072, 2127: remove idempotency keys past their
			// TTL so the ops_idempotency_keys table does not grow unbounded.
			IdempotencyCleanup: func(ctx context.Context) error {
				n, err := w.idemStore.PruneExpired(ctx, time.Now().UTC())
				if err == nil && n > 0 {
					log.Printf("lenny-ops: idempotency cleanup removed %d expired key(s)", n)
				}
				return err
			},
			// §25.4 lines 2226-2267: resolve remediation locks orphaned by a
			// storage outage — bring the Postgres epoch up to MAX, copy the
			// Redis locks in, and apply the deterministic split-brain
			// resolution rule. F-25.4.6.
			LockEpochReconcile: func(ctx context.Context) error {
				return w.lockSvc.Reconcile(ctx)
			},
			// §25.4 line 1337: validate that the stored desired-state snapshot
			// is current, warning when it drifts past the staleness threshold.
			DriftSnapshotValidate: func(ctx context.Context) error {
				fresh, err := w.driftSvc.SnapshotFreshness(ctx)
				if err != nil {
					return err
				}
				switch {
				case !fresh.Present:
					log.Printf("lenny-ops: drift snapshot validation: no live bootstrap_seed_snapshot present")
				case fresh.Stale:
					log.Printf("lenny-ops: drift snapshot validation: live snapshot is stale (age %ds)", fresh.AgeSeconds)
				}
				return nil
			},
			// §25.2 lines 399-401: scan the Operations Inventory to maintain
			// the lenny_ops_operations_stalled gauge and emit
			// operation_progressed on step transitions and percent-threshold
			// crossings. Leader-only so a multi-replica deployment emits one
			// stream. F-25.2.7 / F-25.2.14.
			OperationsObserve: w.operationsObserver.Tick,
			// §25.4 line 1339: the bundleRules reconciler. Leader-only so a
			// multi-replica deployment re-asserts the §25.13 bundled-rules
			// gauges from one replica. F-25.4.17.
			BundleRulesReconcile: w.bundleRulesReconcile,
			// §25.4 line 2280: the Postgres-Redis clock-skew sampler. Reads
			// both dependency clocks and publishes the absolute skew on
			// lenny_ops_clock_skew_seconds so the OpsClockSkewExceeded alert
			// has a producer. Leader-only so a multi-replica deployment
			// publishes one skew sample. nil sampler (single-process degraded
			// mode) disables the loop. F-SH-1.
			ClockSkewSample: clockSkewSampleReconciler(w.clockSkewSampler),
		},
		SelfHealthChecks:   w.selfChecks,
		SelfHealthInterval: *w.f.selfHealthInterval,
		OnSelfHealthChange: func(prev, next opsservice.SelfHealthReport) {
			// §25.5 line 2590: lenny-ops emits ops_health_status_changed
			// — one of the signals it originates itself — onto the same
			// ops:events:stream the gateway writes to, so subscribers,
			// pollers, and SSE clients on any replica observe the
			// transition. The local opsstream.Service buffer always
			// receives it; the Redis write is best-effort (logged on
			// failure) per the §25.5 buffer-fallback model.
			log.Printf("lenny-ops: self-health %s -> %s (replica %s)",
				prev.StatusText, next.StatusText, w.replicaID)
			fields := selfHealthChangePayload(w.replicaID, prev, next)
			// §11.7: commit the durable ops_health_status_changed audit row
			// (logged only in the degraded no-Postgres mode). F-25.4.22.
			w.auditRecorder.Record(string(events.EventOpsHealthStatusChanged), fields, time.Now())
			payload, _ := json.Marshal(fields)
			if err := w.opsEmitter.Emit(w.ctx, events.OperationalEvent{
				Type:            events.EventOpsHealthStatusChanged.CloudEventsType(),
				Subject:         "ops/" + w.replicaID,
				Severity:        selfHealthEventSeverity(next.StatusText),
				DataContentType: "application/json",
				Data:            payload,
			}); err != nil {
				log.Printf("lenny-ops: emit ops_health_status_changed: %v", err)
			}
		},
		// §16.8 line 704 — publish lenny_ops_self_health_status{check} on
		// every evaluation so the §16.9 /metrics scrape reflects the live
		// per-check status, not only the last transition.
		OnSelfHealthSample: publishSelfHealthMetric,
	})
	if err != nil {
		log.Fatalf("lenny-ops: build service: %v", err)
	}
	w.svc = svc
}

// selfHealthChangePayload builds the ops_health_status_changed event
// payload for a §25.4 self-health transition. The §25.3 event catalog
// documents the payload as "Old status, new status, triggering check", so
// alongside the replica identity and the previous/current aggregate
// status the payload names the triggering check(s): the individual
// self-health checks whose status changed between the previous and the
// new report, which is what drove the aggregate transition.
// spec: §25.3 (event types — ops_health_status_changed payload)
func selfHealthChangePayload(replicaID string, prev, next opsservice.SelfHealthReport) map[string]any {
	return map[string]any{
		"replicaId":        replicaID,
		"previous":         prev.StatusText,
		"current":          next.StatusText,
		"transition":       prev.StatusText + " -> " + next.StatusText,
		"triggeringChecks": triggeringChecks(prev, next),
	}
}

// triggeringChecks returns the names of the §25.4 self-health checks whose
// individual status changed between prev and next, sorted by name. These
// are the checks that drove the aggregate self-health transition. A check
// present in next but absent from prev (for example on the first
// evaluation, whose baseline report carries no checks) counts as a trigger
// when its new status is not healthy.
// spec: §25.3 (ops_health_status_changed — triggering check)
func triggeringChecks(prev, next opsservice.SelfHealthReport) []string {
	prevStatus := make(map[string]string, len(prev.Checks))
	for _, c := range prev.Checks {
		prevStatus[c.Name] = c.StatusText
	}
	changed := make([]string, 0, len(next.Checks))
	for _, c := range next.Checks {
		before, ok := prevStatus[c.Name]
		switch {
		case ok && before != c.StatusText:
			changed = append(changed, c.Name)
		case !ok && c.Status != opsservice.StatusHealthy:
			changed = append(changed, c.Name)
		}
	}
	sort.Strings(changed)
	return changed
}
