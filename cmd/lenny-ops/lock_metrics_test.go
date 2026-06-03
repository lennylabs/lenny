// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/opsaudit"
)

// spec: §25.4 line 2332 — lenny_ops_lock_store_active{store} is 1 for the
// serving tier and 0 for the others.
func TestLockMetricsActiveStore_spec_25_4(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := newLockMetrics(reg)
	m.SetActiveStore(coordination.StoreRedis)

	for _, tc := range []struct {
		store string
		want  float64
	}{
		{coordination.StoreRedis, 1},
		{coordination.StorePostgres, 0},
		{coordination.StoreMemory, 0},
	} {
		got := testutil.ToFloat64(m.active.WithLabelValues(tc.store))
		if got != tc.want {
			t.Errorf("active{store=%s} = %v, want %v", tc.store, got, tc.want)
		}
	}
}

// spec: §25.4 lines 2333-2335 — the outage-epoch gauge and the split-brain
// / steal counters carry data.
func TestLockMetricsEpochAndCounters_spec_25_4(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := newLockMetrics(reg)
	m.SetOutageEpoch(4)
	m.SplitBrainDetected("pool:{name}")
	m.StealDone("pool:{name}")
	m.StealDone("pool:{name}")

	if got := testutil.ToFloat64(m.outage); got != 4 {
		t.Errorf("outage epoch = %v, want 4", got)
	}
	if got := testutil.ToFloat64(m.splitBrain.WithLabelValues("pool:{name}")); got != 1 {
		t.Errorf("split-brain count = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.steal.WithLabelValues("pool:{name}")); got != 2 {
		t.Errorf("steal count = %v, want 2", got)
	}
}

// capturingEmitter records the operational events the lock audit emitter
// publishes.
type capturingEmitter struct{ types []string }

func (c *capturingEmitter) Emit(_ context.Context, e events.OperationalEvent) error {
	c.types = append(c.types, e.Type)
	return nil
}

// capturingAppender records every durable audit append so a test can
// assert the §11.7 platform rows the lock emitter committed.
type capturingAppender struct{ events []string }

func (c *capturingAppender) Append(_ context.Context, _, eventType string, _ json.RawMessage, _ time.Time) (audit.Row, error) {
	c.events = append(c.events, eventType)
	return audit.Row{}, nil
}

// spec: §25.4 lines 2338-2340 + §11.7 line 435 — the lock audit emitter
// routes the lock lifecycle events onto the operational-event stream and
// commits a durable §11.7 platform-audit row for every event. lock_extended
// has no operational-event counterpart so it reaches only the durable chain.
func TestLockAuditEmitter_spec_25_4(t *testing.T) {
	cap := &capturingEmitter{}
	appender := &capturingAppender{}
	em := lockAuditEmitter{
		emitter:  cap,
		recorder: opsaudit.New(appender),
		source:   "//lenny.dev/ops/r1",
	}
	lock := coordination.Lock{ID: "lock-1", Scope: "pool:p", AcquiredBy: "alice"}

	em.LockEvent(context.Background(), coordination.AuditLockAcquired, lock, nil)
	em.LockEvent(context.Background(), coordination.AuditLockStolen, lock, map[string]any{"stolenFrom": "bob"})
	em.LockEvent(context.Background(), coordination.AuditLockExtended, lock, nil) // stream-dropped

	want := []string{
		events.EventRemediationLockAcquired.CloudEventsType(),
		events.EventRemediationLockStolen.CloudEventsType(),
	}
	if len(cap.types) != len(want) {
		t.Fatalf("emitted %v, want %v (lock_extended dropped from stream)", cap.types, want)
	}
	for i := range want {
		if cap.types[i] != want[i] {
			t.Errorf("event[%d] = %q, want %q", i, cap.types[i], want[i])
		}
	}

	// Every event, including lock_extended, lands on the durable chain.
	wantDurable := []string{
		coordination.AuditLockAcquired,
		coordination.AuditLockStolen,
		coordination.AuditLockExtended,
	}
	if len(appender.events) != len(wantDurable) {
		t.Fatalf("durable rows = %v, want %v", appender.events, wantDurable)
	}
	for i := range wantDurable {
		if appender.events[i] != wantDurable[i] {
			t.Errorf("durable[%d] = %q, want %q", i, appender.events[i], wantDurable[i])
		}
	}
}
