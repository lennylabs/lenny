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
		case "escalation-flush", "idempotency-cleanup", "lock-epoch-reconcile", "drift-snapshot-validate":
			t.Errorf("unwired reconciler %q produced a loop", name)
		}
	}
}

// stoppedElector is a never-leader elector for the loop-projection tests.
type stoppedElector struct{}

func (stoppedElector) Run(ctx context.Context, _ func(context.Context), _ func()) { <-ctx.Done() }
func (stoppedElector) IsLeader() bool                                             { return false }
