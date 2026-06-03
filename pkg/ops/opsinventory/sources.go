// SPDX-License-Identifier: MIT

// Package opsinventory adapts the lenny-ops subsystem services onto the
// §25.4 Operations Inventory Source contract (pkg/ops/operations). Each
// adapter projects one subsystem's live records onto the canonical
// operations.Operation envelope so the unified GET /v1/admin/operations
// scatter-gather can merge them.
//
// The adapters own no state. A subsystem whose backing store is
// unreachable returns the store error from List; the Inventory turns
// that into a §25.4 degradation warning rather than failing the request.
//
// spec: §25.4 "Operations Inventory" (lines 1646-1786).
package opsinventory

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
	"github.com/lennylabs/lenny/pkg/ops/operations"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/upgrade"
)

// jsonMetadata marshals a §25.4 per-operation metadata block. A marshal
// failure (which the small string-keyed maps below never trigger) yields
// a nil RawMessage so the field is simply omitted.
func jsonMetadata(m map[string]any) json.RawMessage {
	if len(m) == 0 {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return b
}

// LockSource projects held §25.4 remediation locks onto the Operations
// Inventory. Every non-expired lock is an operation of kind
// remediation_lock in status held; its natural key is the lock ID (which
// already carries the canonical "lock-" prefix), so operationId == ID.
type LockSource struct {
	svc coordination.RemediationLockService
}

// NewLockSource adapts a remediation-lock service. A nil service yields
// no operations.
func NewLockSource(svc coordination.RemediationLockService) *LockSource {
	return &LockSource{svc: svc}
}

// Kinds reports the remediation_lock kind.
func (s *LockSource) Kinds() []operations.Kind {
	return []operations.Kind{operations.KindRemediationLock}
}

// List projects every held lock. The §25.4 resources block exposes the
// lock's get/extend/release/steal endpoints; the lock scope and the
// operation it guards land in metadata.
func (s *LockSource) List(ctx context.Context, _ operations.Filter) ([]operations.Operation, error) {
	if s.svc == nil {
		return nil, nil
	}
	locks, err := s.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]operations.Operation, 0, len(locks))
	for _, l := range locks {
		expires := l.ExpiresAt
		base := "/v1/admin/remediation-locks/" + l.ID
		out = append(out, operations.Operation{
			OperationID: l.ID,
			Kind:        operations.KindRemediationLock,
			Status:      operations.StatusHeld,
			StartedBy:   l.AcquiredBy,
			StartedAt:   l.AcquiredAt,
			TimeoutAt:   &expires,
			Progress:    nil,
			Resources: map[string]string{
				"get":     "GET " + base,
				"extend":  "PATCH " + base,
				"release": "DELETE " + base,
				"steal":   "POST " + base + "/steal",
				"audit":   "GET /v1/admin/audit-events?operationId=" + l.ID,
			},
			Cancellable: true,
			Metadata: jsonMetadata(map[string]any{
				"scope":     l.Scope,
				"operation": l.Operation,
			}),
		})
	}
	return out, nil
}

var _ operations.Source = (*LockSource)(nil)

// EscalationSource projects §25.4 escalations onto the Operations
// Inventory. A buffered-memory escalation awaiting flush is kind
// escalation_buffered in status awaiting_flush; a durable open escalation
// is kind escalation_open in status in_progress; a resolved escalation is
// completed (excluded by the default status filter).
type EscalationSource struct {
	svc *escalation.Service
}

// NewEscalationSource adapts an escalation service. A nil service yields
// no operations.
func NewEscalationSource(svc *escalation.Service) *EscalationSource {
	return &EscalationSource{svc: svc}
}

// Kinds reports the two escalation kinds.
func (s *EscalationSource) Kinds() []operations.Kind {
	return []operations.Kind{operations.KindEscalationOpen, operations.KindEscalationBuffered}
}

// List projects every escalation from the highest reachable tier. The
// escalation natural key already carries the canonical "esc-" prefix, so
// operationId == ID.
func (s *EscalationSource) List(ctx context.Context, _ operations.Filter) ([]operations.Operation, error) {
	if s.svc == nil {
		return nil, nil
	}
	// An empty filter returns every escalation; the Inventory applies its
	// own status filter over the merged result. MaxPageLimit bounds the
	// projection.
	escs, err := s.svc.List(ctx, escalation.Filter{}, maxEscalations)
	if err != nil {
		return nil, err
	}
	out := make([]operations.Operation, 0, len(escs))
	for _, e := range escs {
		kind, status := escalationKindStatus(e)
		out = append(out, operations.Operation{
			OperationID: e.ID,
			Kind:        kind,
			Status:      status,
			StartedBy:   e.Source,
			StartedAt:   e.CreatedAt,
			Progress:    nil,
			Resources: map[string]string{
				"list":   "GET /v1/admin/escalations",
				"update": "PUT /v1/admin/escalations/" + e.ID,
				"audit":  "GET /v1/admin/audit-events?operationId=" + e.ID,
			},
			// Escalations are records to acknowledge or resolve, not
			// cancellable in-flight work.
			Cancellable: false,
			Metadata: jsonMetadata(map[string]any{
				"severity":    e.Severity,
				"summary":     e.Summary,
				"persistence": e.Persistence,
			}),
		})
	}
	return out, nil
}

// maxEscalations bounds the escalation projection per List call. It
// matches the inventory's MaxPageLimit so a single page is never starved
// by the source fetch.
const maxEscalations = 1000

// escalationKindStatus maps an escalation's persistence tier and
// lifecycle onto the §25.4 inventory kind and status. A resolved
// escalation is completed; a buffered-memory escalation awaits flush; a
// durable open escalation is in progress (open, awaiting operator
// resolution). spec: §25.4 lines 1665-1672, 1750-1752.
func escalationKindStatus(e escalation.Escalation) (operations.Kind, operations.Status) {
	if e.ResolvedAt != nil {
		// A resolved escalation is terminal-success; surface it as a
		// completed open escalation so a ?status=completed history query
		// can find it.
		return operations.KindEscalationOpen, operations.StatusCompleted
	}
	if e.Persistence == escalation.PersistenceBufferedMemory {
		return operations.KindEscalationBuffered, operations.StatusAwaitingFlush
	}
	return operations.KindEscalationOpen, operations.StatusInProgress
}

var _ operations.Source = (*EscalationSource)(nil)

// UpgradeSource projects the §25.8 platform-upgrade singleton onto the
// Operations Inventory as kind platform_upgrade. The natural key is the
// upgrade's operationId (already "upgrade-" prefixed).
type UpgradeSource struct {
	svc *upgradeservice.Service
}

// NewUpgradeSource adapts a platform-upgrade service. A nil service
// yields no operations.
func NewUpgradeSource(svc *upgradeservice.Service) *UpgradeSource {
	return &UpgradeSource{svc: svc}
}

// Kinds reports the platform_upgrade kind.
func (s *UpgradeSource) Kinds() []operations.Kind {
	return []operations.Kind{operations.KindPlatformUpgrade}
}

// List projects the upgrade singleton. No upgrade ever recorded yields an
// empty slice; a recorded upgrade is mapped to in_progress / paused /
// completed / failed and carries the §25.4 status/proceed/pause/rollback
// resources block.
func (s *UpgradeSource) List(ctx context.Context, _ operations.Filter) ([]operations.Operation, error) {
	if s.svc == nil {
		return nil, nil
	}
	st, ok, err := s.svc.Status(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	op := operations.Operation{
		OperationID: st.OperationID,
		Kind:        operations.KindPlatformUpgrade,
		Status:      upgradeStatus(st),
		StartedBy:   st.StartedBy,
		StartedAt:   st.StartedAt,
		Progress:    upgradeProgress(st),
		Resources: map[string]string{
			"status":   "GET /v1/admin/platform/upgrade/status",
			"proceed":  "POST /v1/admin/platform/upgrade/proceed",
			"pause":    "POST /v1/admin/platform/upgrade/pause",
			"rollback": "POST /v1/admin/platform/upgrade/rollback",
			"audit":    "GET /v1/admin/audit-events?operationId=" + st.OperationID,
		},
		Cancellable: st.Active(),
		Metadata: jsonMetadata(map[string]any{
			"targetVersion": st.TargetVersion,
		}),
	}
	return []operations.Operation{op}, nil
}

// upgradeStatus maps an upgrade phase onto the §25.4 inventory status.
// Paused (awaiting an explicit proceed) takes precedence; a rolled-back
// upgrade is a terminal failure operators must resolve; a complete
// upgrade is completed; an in-flight upgrade is in_progress.
func upgradeStatus(st upgradeservice.State) operations.Status {
	switch {
	case st.Paused:
		return operations.StatusPaused
	case st.Phase == upgrade.RolledBack:
		return operations.StatusFailed
	case st.Phase == upgrade.Complete:
		return operations.StatusCompleted
	default:
		return operations.StatusInProgress
	}
}

// upgradeProgress projects the upgrade's uniform §25.8 progress object
// onto the §25.2 canonical progress envelope.
func upgradeProgress(st upgradeservice.State) *conventions.Progress {
	raw := st.Progress()
	p := &conventions.Progress{EtaMethod: conventions.EtaNone}
	if step, ok := raw["currentStep"].(int); ok {
		s := step
		p.CompletedSteps = &s
	}
	if total, ok := raw["totalSteps"].(int); ok {
		t := total
		p.TotalSteps = &t
	}
	if detail, ok := raw["currentStepDetail"].(string); ok {
		p.CurrentStepDetail = detail
	}
	// §25.2 lines 387-391: stamp startedAt and lastProgressAt so the
	// Inventory's progress enrichment can derive the historical_p50 ETA
	// and the cadence-relative stalledForSeconds. UpdatedAt is the time of
	// the last §25.8 phase transition, i.e. when progress last advanced.
	if !st.StartedAt.IsZero() {
		p.StartedAt = st.StartedAt.UTC().Format(time.RFC3339)
	}
	if !st.UpdatedAt.IsZero() {
		p.LastProgressAt = st.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return p
}

var _ operations.Source = (*UpgradeSource)(nil)
