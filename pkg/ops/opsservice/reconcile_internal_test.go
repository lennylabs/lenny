// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"testing"
)

// spec: §25.4 lines 2404, 2429 — the escalation emission-retry loop runs
// leader-only, so a multi-replica deployment re-publishes a record left
// unemitted by a dual-destination outage from exactly one replica rather
// than once per replica. This is the tier-7a (ordering/singleton)
// regression for F-REL-1: the loop must carry LeaderOnly so the retry is
// a singleton behavior, matching the other §25.4 reconciliation loops.
func TestEscalationEmissionRetryLoopIsLeaderOnly_spec_25_4_F_REL_1(t *testing.T) {
	noop := func(context.Context) error { return nil }
	loops := Reconcilers{EscalationEmissionRetry: noop}.loops()
	var found *Loop
	for i := range loops {
		if loops[i].Name == "escalation-emission-retry" {
			found = &loops[i]
		}
	}
	if found == nil {
		t.Fatalf("loops missing escalation-emission-retry: %+v", loops)
	}
	if !found.LeaderOnly {
		t.Error("escalation-emission-retry loop is not leader-only; a multi-replica deployment would retry emission once per replica")
	}
	if found.Interval != EscalationEmissionRetryInterval {
		t.Errorf("interval = %v, want %v (§25.4 fixes the retry period at 30s)", found.Interval, EscalationEmissionRetryInterval)
	}
}
