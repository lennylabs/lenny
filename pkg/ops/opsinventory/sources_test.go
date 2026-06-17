// SPDX-License-Identifier: MIT

package opsinventory_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
	"github.com/lennylabs/lenny/pkg/ops/operations"
	"github.com/lennylabs/lenny/pkg/ops/opsinventory"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
)

// fakeLockService is a coordination.RemediationLockService stub whose
// List returns a fixed slice or error.
type fakeLockService struct {
	locks []coordination.Lock
	err   error
}

func (f fakeLockService) Acquire(context.Context, coordination.LockRequest) (*coordination.Lock, error) {
	return nil, nil
}

func (f fakeLockService) List(context.Context) ([]coordination.Lock, error) { return f.locks, f.err }

func (f fakeLockService) Get(context.Context, string) (*coordination.Lock, error) {
	return nil, nil
}

func (f fakeLockService) Extend(context.Context, string, int) (*coordination.Lock, error) {
	return nil, nil
}
func (f fakeLockService) Release(context.Context, string) error { return nil }
func (f fakeLockService) Steal(context.Context, string, coordination.StealRequest) (*coordination.Lock, error) {
	return nil, nil
}

// spec §25.4 lines 1697-1709: a held lock is remediation_lock/held with
// the operationId == lock ID and the get/extend/release/steal resources.
func TestLockSourceProjectsHeldLock(t *testing.T) {
	now := time.Now()
	src := opsinventory.NewLockSource(fakeLockService{locks: []coordination.Lock{{
		ID:         "lock-abc123",
		Scope:      "pool:default-gvisor",
		Operation:  "scale",
		AcquiredBy: "sa-watchdog",
		AcquiredAt: now,
		ExpiresAt:  now.Add(5 * time.Minute),
	}}})
	ops, err := src.List(context.Background(), operations.Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("len(ops) = %d, want 1", len(ops))
	}
	op := ops[0]
	if op.OperationID != "lock-abc123" {
		t.Errorf("operationId = %q, want lock-abc123", op.OperationID)
	}
	if op.Kind != operations.KindRemediationLock || op.Status != operations.StatusHeld {
		t.Errorf("kind/status = %s/%s, want remediation_lock/held", op.Kind, op.Status)
	}
	if op.TimeoutAt == nil {
		t.Error("timeoutAt must be set for a TTL-bounded lock")
	}
	if !strings.Contains(op.Resources["release"], "/v1/admin/remediation-locks/lock-abc123") {
		t.Errorf("release resource = %q", op.Resources["release"])
	}
	if op.Resources["steal"] == "" {
		t.Error("steal resource must be present")
	}
}

// spec §25.4 line 1750: a source whose backing store is unreachable
// propagates the error so the Inventory turns it into a degradation
// warning rather than failing the request.
func TestLockSourcePropagatesError(t *testing.T) {
	src := opsinventory.NewLockSource(fakeLockService{err: errors.New("postgres unreachable")})
	_, err := src.List(context.Background(), operations.Filter{})
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

// A nil service yields no operations rather than panicking (the dev path
// where a subsystem is unwired).
func TestSourcesNilServiceEmpty(t *testing.T) {
	for _, src := range []operations.Source{
		opsinventory.NewLockSource(nil),
		opsinventory.NewEscalationSource(nil),
		opsinventory.NewUpgradeSource(nil),
	} {
		ops, err := src.List(context.Background(), operations.Filter{})
		if err != nil || len(ops) != 0 {
			t.Errorf("%T: ops=%v err=%v, want empty/nil", src, ops, err)
		}
	}
}

// spec §25.4: a buffered-memory escalation (the only tier in this
// single-store service) projects as escalation_buffered/awaiting_flush.
func TestEscalationSourceProjectsBuffered(t *testing.T) {
	svc := escalation.NewService(nil)
	if _, err := svc.Create(context.Background(), escalation.CreateRequest{
		Severity: "warning", Summary: "disk pressure", Source: "sa-watchdog",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	src := opsinventory.NewEscalationSource(svc)
	ops, err := src.List(context.Background(), operations.Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("len(ops) = %d, want 1", len(ops))
	}
	op := ops[0]
	if op.Kind != operations.KindEscalationBuffered || op.Status != operations.StatusAwaitingFlush {
		t.Errorf("kind/status = %s/%s, want escalation_buffered/awaiting_flush", op.Kind, op.Status)
	}
	if !strings.HasPrefix(op.OperationID, "esc-") {
		t.Errorf("operationId = %q, want esc- prefix", op.OperationID)
	}
	if op.StartedBy != "sa-watchdog" {
		t.Errorf("startedBy = %q, want sa-watchdog", op.StartedBy)
	}
}

// spec §25.4: a started upgrade projects as a platform_upgrade operation
// (it begins paused at Preflight awaiting the first proceed) with the
// status/proceed/pause/rollback resources; no upgrade ever recorded
// yields an empty slice.
func TestUpgradeSourceProjectsStartedUpgrade(t *testing.T) {
	svc := upgradeservice.New(upgradeservice.Options{Store: upgradeservice.NewMemoryStore()})
	src := opsinventory.NewUpgradeSource(svc)

	ops, err := src.List(context.Background(), operations.Filter{})
	if err != nil {
		t.Fatalf("List (no upgrade): %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("len(ops) = %d, want 0 before any upgrade", len(ops))
	}

	if _, err := svc.Start(context.Background(), upgradeservice.StartRequest{
		TargetVersion: "1.6.0", StartedBy: "sa-deploy",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ops, err = src.List(context.Background(), operations.Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("len(ops) = %d, want 1", len(ops))
	}
	op := ops[0]
	if op.Kind != operations.KindPlatformUpgrade {
		t.Errorf("kind = %s, want platform_upgrade", op.Kind)
	}
	if op.Status != operations.StatusInProgress && op.Status != operations.StatusPaused {
		t.Errorf("status = %s, want in_progress or paused", op.Status)
	}
	if op.StartedBy != "sa-deploy" {
		t.Errorf("startedBy = %q, want sa-deploy", op.StartedBy)
	}
	if !strings.HasPrefix(op.OperationID, "upgrade-") {
		t.Errorf("operationId = %q, want upgrade- prefix", op.OperationID)
	}
	if op.Resources["rollback"] == "" || op.Progress == nil {
		t.Errorf("upgrade op must carry rollback resource and progress: %+v", op)
	}
}
