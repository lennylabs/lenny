// SPDX-License-Identifier: MIT

package opsservice_test

import (
	"context"
	"sort"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// spec: §25.4 line 1337 — the four leader-only reconciliation goroutines
// (escalation flush, idempotency cleanup, lock outage-epoch reconcile,
// drift snapshot validation) each project to a leader-only loop when wired.
func TestReconcilersWireFourLeaderLoops_spec_25_4(t *testing.T) {
	noop := func(context.Context) error { return nil }
	cfg := opsservice.Config{
		ReplicaID: "r1",
		Elector:   stoppedElector{},
		Reconcilers: opsservice.Reconcilers{
			EscalationFlush:       noop,
			IdempotencyCleanup:    noop,
			LockEpochReconcile:    noop,
			DriftSnapshotValidate: noop,
		},
	}
	svc, err := opsservice.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := svc.LoopNames()
	want := []string{
		"drift-snapshot-validate", "escalation-flush",
		"idempotency-cleanup", "lock-epoch-reconcile", "self-monitor",
	}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("loops = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("loops = %v, want %v", got, want)
		}
	}
}

// spec: §25.2 lines 399-401 — wiring the OperationsObserve reconciler
// produces the leader-only operations-observe loop that maintains the
// lenny_ops_operations_stalled gauge and emits operation_progressed.
func TestOperationsObserveWiresLoop_spec_25_2(t *testing.T) {
	noop := func(context.Context) error { return nil }
	svc, err := opsservice.New(opsservice.Config{
		ReplicaID:   "r1",
		Elector:     stoppedElector{},
		Reconcilers: opsservice.Reconcilers{OperationsObserve: noop},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	found := false
	for _, name := range svc.LoopNames() {
		if name == "operations-observe" {
			found = true
		}
	}
	if !found {
		t.Errorf("loops %v missing operations-observe", svc.LoopNames())
	}
}

// spec: §25.4 line 1337 — a nil reconciler contributes no loop, so a
// deployment without a Postgres-backed durable tier skips the loops it
// cannot run.
func TestNilReconcilersContributeNoLoops_spec_25_4(t *testing.T) {
	svc, err := opsservice.New(opsservice.Config{ReplicaID: "r1", Elector: stoppedElector{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, name := range svc.LoopNames() {
		switch name {
		case "escalation-flush", "escalation-emission-retry", "idempotency-cleanup", "lock-epoch-reconcile", "drift-snapshot-validate":
			t.Errorf("unwired reconciler %q produced a loop", name)
		}
	}
}

// spec: §25.4 lines 2404, 2429 — wiring the EscalationEmissionRetry
// reconciler registers an escalation-emission-retry loop, so the §25.4
// 30s emission-retry that re-publishes records left unemitted by a
// dual-destination outage has a production caller. This is the tier-7a
// (load/ordering) regression for F-REL-1: pre-fix the Reconcilers struct
// carried no emission-retry field, so RetryEmission had no production
// caller and no loop was registered. The leader-only property of the loop
// is pinned by the white-box TestEscalationEmissionRetryLoopIsLeaderOnly.
func TestEscalationEmissionRetryWiresLoop_spec_25_4_F_REL_1(t *testing.T) {
	noop := func(context.Context) error { return nil }
	svc, err := opsservice.New(opsservice.Config{
		ReplicaID:   "r1",
		Elector:     stoppedElector{},
		Reconcilers: opsservice.Reconcilers{EscalationEmissionRetry: noop},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	found := false
	for _, name := range svc.LoopNames() {
		if name == "escalation-emission-retry" {
			found = true
		}
	}
	if !found {
		t.Fatalf("loops %v missing escalation-emission-retry", svc.LoopNames())
	}
}

// stoppedElector is a never-leader elector for the loop-projection tests.
type stoppedElector struct{}

func (stoppedElector) Run(ctx context.Context, _ func(context.Context), _ func()) { <-ctx.Done() }
func (stoppedElector) IsLeader() bool                                             { return false }
