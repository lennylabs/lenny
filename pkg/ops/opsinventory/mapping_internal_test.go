// SPDX-License-Identifier: MIT

package opsinventory

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/escalation"
	"github.com/lennylabs/lenny/pkg/ops/operations"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/upgrade"
)

// spec §25.4 lines 1665-1672: a buffered-memory escalation awaiting flush
// is escalation_buffered/awaiting_flush; a durable open escalation is
// escalation_open/in_progress; a resolved escalation is completed.
func TestEscalationKindStatus(t *testing.T) {
	resolved := time.Now()
	cases := []struct {
		name       string
		esc        escalation.Escalation
		wantKind   operations.Kind
		wantStatus operations.Status
	}{
		{
			name:       "buffered-memory awaits flush",
			esc:        escalation.Escalation{Persistence: escalation.PersistenceBufferedMemory},
			wantKind:   operations.KindEscalationBuffered,
			wantStatus: operations.StatusAwaitingFlush,
		},
		{
			name:       "durable-postgres is open in progress",
			esc:        escalation.Escalation{Persistence: escalation.PersistenceDurablePostgres},
			wantKind:   operations.KindEscalationOpen,
			wantStatus: operations.StatusInProgress,
		},
		{
			name:       "durable-redis is open in progress",
			esc:        escalation.Escalation{Persistence: escalation.PersistenceDurableRedis},
			wantKind:   operations.KindEscalationOpen,
			wantStatus: operations.StatusInProgress,
		},
		{
			name:       "resolved is completed",
			esc:        escalation.Escalation{Persistence: escalation.PersistenceBufferedMemory, ResolvedAt: &resolved},
			wantKind:   operations.KindEscalationOpen,
			wantStatus: operations.StatusCompleted,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, st := escalationKindStatus(tc.esc)
			if k != tc.wantKind || st != tc.wantStatus {
				t.Fatalf("escalationKindStatus = (%s,%s), want (%s,%s)", k, st, tc.wantKind, tc.wantStatus)
			}
		})
	}
}

// spec §25.4 lines 1707-1716: paused takes precedence; a rolled-back
// upgrade is failed; complete is completed; an in-flight upgrade is
// in_progress.
func TestUpgradeStatus(t *testing.T) {
	cases := []struct {
		name string
		st   upgradeservice.State
		want operations.Status
	}{
		{"paused", upgradeservice.State{Phase: upgrade.GatewayRoll, Paused: true}, operations.StatusPaused},
		{"rolled back is failed", upgradeservice.State{Phase: upgrade.RolledBack}, operations.StatusFailed},
		{"complete", upgradeservice.State{Phase: upgrade.Complete}, operations.StatusCompleted},
		{"in flight", upgradeservice.State{Phase: upgrade.GatewayRoll}, operations.StatusInProgress},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := upgradeStatus(tc.st); got != tc.want {
				t.Fatalf("upgradeStatus = %s, want %s", got, tc.want)
			}
		})
	}
}

// spec §25.2: the upgrade progress envelope carries the step counts and
// the human-readable step detail with no ETA computation.
func TestUpgradeProgress(t *testing.T) {
	st := upgradeservice.State{Phase: upgrade.GatewayRoll, Paused: true}
	p := upgradeProgress(st)
	if p == nil {
		t.Fatal("upgradeProgress = nil")
	}
	if p.TotalSteps == nil || *p.TotalSteps != upgrade.TotalSteps {
		t.Errorf("TotalSteps = %v, want %d", p.TotalSteps, upgrade.TotalSteps)
	}
	if p.CompletedSteps == nil {
		t.Error("CompletedSteps must be set")
	}
	if p.CurrentStepDetail == "" {
		t.Error("CurrentStepDetail must be set")
	}
}
