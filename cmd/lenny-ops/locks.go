// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/coordination/pgstore"
	"github.com/lennylabs/lenny/pkg/ops/coordination/redisstore"
	"github.com/lennylabs/lenny/pkg/ops/opsaudit"
)

// buildLockService constructs the §25.4 tiered remediation-lock service:
// the Postgres ops_remediation_locks store (Tier 1, migration 0121) when a
// pool is available, then the Redis ops:lock:{scope} store (Tier 2) when a
// Redis client is available, over the always-present in-memory Tier 3
// store. The gate enforces the ops.locks.memoryTier policy on the Tier 3
// fall-through; the metrics adapter publishes the §25.4 lock gauges and
// counters; the audit emitter routes the lock lifecycle events onto the
// operational-event stream.
//
// spec: §25.4 lines 2083-2271.
func buildLockService(pgPool *pgxpool.Pool, redisClient redis.UniversalClient,
	gate *coordination.CoordinationGate, reg prometheus.Registerer,
	emitter events.EventEmitter, recorder *opsaudit.Recorder, replicaID string) *coordination.Service {
	opts := coordination.ServiceOptions{
		Gate:    gate,
		Metrics: newLockMetrics(reg),
		Audit:   lockAuditEmitter{emitter: emitter, recorder: recorder, source: "//lenny.dev/ops/" + replicaID},
	}
	if pgPool != nil {
		opts.Postgres = pgstore.New(pgPool)
	}
	if redisClient != nil {
		opts.Redis = redisstore.New(redisClient)
	}
	switch {
	case pgPool != nil && redisClient != nil:
		log.Printf("lenny-ops: §25.4 remediation locks: Postgres (Tier 1) + Redis (Tier 2) + in-memory (Tier 3)")
	case pgPool != nil:
		log.Printf("lenny-ops: §25.4 remediation locks: Postgres (Tier 1) + in-memory (Tier 3)")
	case redisClient != nil:
		log.Printf("lenny-ops: §25.4 remediation locks: Redis (Tier 2) + in-memory (Tier 3)")
	default:
		log.Printf("lenny-ops: §25.4 remediation locks: in-memory (Tier 3) only — single-process degraded mode")
	}
	return coordination.NewService(opts)
}

// lockMetrics is the Prometheus-backed coordination.Metrics for the §25.4
// remediation-lock signals. The gauges and counters are registered on the
// process registry so the §16.9 /metrics exposition scrapes them.
//
// spec: §25.4 lines 2328-2336.
type lockMetrics struct {
	active     *prometheus.GaugeVec
	outage     prometheus.Gauge
	splitBrain *prometheus.CounterVec
	steal      *prometheus.CounterVec
	clockSkew  *prometheus.GaugeVec
}

// lockStoreTiers is the closed set of lockStore labels so SetActiveStore
// can clear the inactive tiers to 0 (a gauge that is 1 for exactly one
// tier).
var lockStoreTiers = []string{coordination.StorePostgres, coordination.StoreRedis, coordination.StoreMemory}

// newLockMetrics registers the §25.4 lock instruments on reg and returns
// the adapter.
func newLockMetrics(reg prometheus.Registerer) *lockMetrics {
	m := &lockMetrics{
		active: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lenny_ops_lock_store_active",
			Help: "§25.4 1 for the currently active remediation-lock store tier, 0 for the others.",
		}, []string{"store"}),
		outage: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "lenny_ops_lock_outage_epoch",
			Help: "§25.4 current remediation-lock outage epoch.",
		}),
		splitBrain: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lenny_ops_lock_split_brain_detected_total",
			Help: "§25.4 split-brain events detected during remediation-lock reconciliation.",
		}, []string{"scope_pattern"}),
		steal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lenny_ops_lock_steal_total",
			Help: "§25.4 explicit remediation-lock steals.",
		}, []string{"scope_pattern"}),
		clockSkew: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "lenny_ops_clock_skew_seconds",
			Help: "§25.4 measured clock skew in seconds between dependency clocks.",
		}, []string{"pair"}),
	}
	reg.MustRegister(m.active, m.outage, m.splitBrain, m.steal, m.clockSkew)
	return m
}

// SetActiveStore raises lenny_ops_lock_store_active{store} for the serving
// tier and clears the others.
func (m *lockMetrics) SetActiveStore(store string) {
	for _, tier := range lockStoreTiers {
		v := 0.0
		if tier == store {
			v = 1.0
		}
		m.active.WithLabelValues(tier).Set(v)
	}
}

func (m *lockMetrics) SetOutageEpoch(epoch uint64)         { m.outage.Set(float64(epoch)) }
func (m *lockMetrics) SplitBrainDetected(pattern string)   { m.splitBrain.WithLabelValues(pattern).Inc() }
func (m *lockMetrics) StealDone(pattern string)            { m.steal.WithLabelValues(pattern).Inc() }
func (m *lockMetrics) SetClockSkew(pair string, s float64) { m.clockSkew.WithLabelValues(pair).Set(s) }

// lockAuditEmitter routes the §25.4 remediation-lock audit events onto two
// destinations: the durable §11.7 platform audit chain (via recorder, the
// audit trail the §25.9 query API reads — "which platform-admin stole
// which lock when?") and the operational-event stream (for SSE subscribers
// and webhook delivery). Every lock event is committed durably, including
// remediation.lock_extended, which has no operational-event counterpart in
// the §25.5 catalog and so reaches only the durable chain.
//
// spec: §25.4 lines 2338-2340 (audit events); §11.7 line 435 (ops_event.*
// route to the platform tenant).
type lockAuditEmitter struct {
	emitter  events.EventEmitter
	recorder *opsaudit.Recorder
	source   string
}

func (e lockAuditEmitter) LockEvent(ctx context.Context, event string, lock coordination.Lock, detail map[string]any) {
	payload := map[string]any{
		"event":      event,
		"lockId":     lock.ID,
		"scope":      lock.Scope,
		"acquiredBy": lock.AcquiredBy,
		"epoch":      lock.Epoch,
	}
	for k, v := range detail {
		payload[k] = v
	}
	// §11.7: commit the durable audit row for every lock event, including
	// remediation.lock_extended, which the §25.5 operational-event stream
	// below does not carry.
	e.recorder.Record(event, payload, time.Now())

	var et events.EventType
	switch event {
	case coordination.AuditLockAcquired:
		et = events.EventRemediationLockAcquired
	case coordination.AuditLockReleased:
		et = events.EventRemediationLockReleased
	case coordination.AuditLockExpired:
		et = events.EventRemediationLockExpired
	case coordination.AuditLockStolen:
		et = events.EventRemediationLockStolen
	case coordination.AuditLockSplitBrainDetected:
		et = events.EventRemediationLockSplitBrainDetected
	default:
		return
	}
	blob, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if err := e.emitter.Emit(ctx, events.OperationalEvent{
		Type:            et.CloudEventsType(),
		Source:          e.source,
		Subject:         "remediation-lock/" + lock.Scope,
		Severity:        "info",
		DataContentType: "application/json",
		Data:            blob,
	}); err != nil {
		log.Printf("lenny-ops: emit %s for %s: %v", event, lock.Scope, err)
	}
}

// Compile-time guards.
var (
	_ coordination.Metrics   = (*lockMetrics)(nil)
	_ coordination.AuditSink = lockAuditEmitter{}
)
