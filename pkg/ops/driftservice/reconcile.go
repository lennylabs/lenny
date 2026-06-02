// SPDX-License-Identifier: MIT

package driftservice

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/drift"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/operations"
)

// ErrCodeReconcileUnavailable is DRIFT_RECONCILE_UNAVAILABLE: a confirm
// reconcile was requested but no gateway-side resource applier is
// configured, so the platform cannot apply the desired state. §25.10
// line 3842 — reconciliation calls the gateway admin API; without that
// client the service can preview (dry-run) but not apply.
const ErrCodeReconcileUnavailable = "DRIFT_RECONCILE_UNAVAILABLE"

// ResourceApplier applies one drifted resource's desired state through
// the gateway admin API (§25.10 line 3842: "calls admin API PUT
// endpoints via GatewayClient"). resourceType is the resource
// collection (pools, runtimes, tenants, credential-pools, ...);
// resourceID is the resource name (empty for a singleton like the
// controller config). desired is the desired-state subtree to PUT. A
// non-nil error records the resource as a §25.10 line 3852 reconcile
// failure. F-25.10.1.
type ResourceApplier interface {
	Apply(ctx context.Context, resourceType, resourceID string, desired map[string]any) error
}

// ProgressEmitter emits the §25.10 line 3844 operation_progressed event
// on each resource reconciliation. F-25.10.1.
type ProgressEmitter interface {
	Progressed(ctx context.Context, info ProgressInfo)
}

// ProgressInfo is one §25.10 line 3844 operation_progressed payload.
type ProgressInfo struct {
	OperationID    string
	Kind           string
	TotalSteps     int
	CompletedSteps int
	CurrentStep    string
	StartedBy      string
}

// ReconcileParams is the §25.10 POST /v1/admin/drift/reconcile request.
type ReconcileParams struct {
	// Scope is "all" (reconcile every drifted resource) or "resources"
	// (reconcile only Resources). Empty defaults to "all".
	Scope string
	// Resources is the "{resourceType}:{resourceId}" list reconciled when
	// Scope=="resources" (a singleton resource is named by type alone).
	Resources []string
	// Confirm follows the §25.2 dry-run/confirm pattern: false returns a
	// preview, true applies the desired state.
	Confirm bool
	// Desired, when non-nil, is the caller-supplied desired state used in
	// place of the stored live snapshot — the §25.10 line 3852 Postgres-
	// outage reconcile path.
	Desired map[string]any
	// StartedBy is the caller identity recorded on the operation and the
	// audit events; the HTTP layer fills it.
	StartedBy string
}

// ReconcileResource is one resource in a §25.10 reconcile result.
type ReconcileResource struct {
	ResourceType string `json:"resourceType"`
	ResourceID   string `json:"resourceId,omitempty"`
	DriftCount   int    `json:"driftCount"`
	// Outcome is "preview" (dry-run), "applied", or "failed".
	Outcome string `json:"outcome"`
	Error   string `json:"error,omitempty"`
}

// ReconcileResult is the §25.10 POST /v1/admin/drift/reconcile response.
type ReconcileResult struct {
	OperationID    string              `json:"operationId"`
	DryRun         bool                `json:"dryRun"`
	Scope          string              `json:"scope"`
	Resources      []ReconcileResource `json:"resources"`
	TotalResources int                 `json:"totalResources"`
	Reconciled     int                 `json:"reconciled"`
	Failed         int                 `json:"failed"`
	// ErrorCode is DRIFT_RECONCILE_PARTIAL when some resources failed; the
	// HTTP layer maps it to 207. Empty on a clean run or a dry-run.
	ErrorCode string `json:"errorCode,omitempty"`
}

// reconcileTarget is one resource to reconcile: its identity, drift
// count, and desired-state subtree.
type reconcileTarget struct {
	rtype string
	rid   string
	count int
	sub   map[string]any
}

// label is the §25.10 "{resourceType}:{resourceId}" form (the type
// alone for a singleton resource).
func (t reconcileTarget) label() string { return resourceLabel(t.rtype, t.rid) }

func resourceLabel(rtype, rid string) string {
	if rid == "" {
		return rtype
	}
	return rtype + ":" + rid
}

// Reconcile runs the §25.10 POST /v1/admin/drift/reconcile flow: it
// computes the current drift against the live snapshot (or a caller-
// supplied desired state), groups the drifted fields into resources,
// and — when Confirm is set — applies each resource's desired state
// through the gateway admin API.
//
// Following the §25.2 dry-run/confirm pattern, an unconfirmed call
// returns a preview without applying anything. A confirmed call emits
// the §25.10 line 3871 reconciliation_started / resource_reconciled /
// reconciliation_completed audit events, increments lenny_drift_-
// reconciled_total per resource, fires operation_progressed on each
// reconciliation, and surfaces the in-flight operation in the §25.4
// Operations Inventory. Any per-resource apply failure yields the
// §25.10 line 3852 partial result (DRIFT_RECONCILE_PARTIAL → HTTP 207).
// F-25.10.1.
func (s *Service) Reconcile(ctx context.Context, p ReconcileParams) (*ReconcileResult, error) {
	scope := p.Scope
	if scope == "" {
		scope = "all"
	}
	if scope != "all" && scope != "resources" {
		return nil, &Error{Code: ErrCodeInvalid, Message: `scope must be "all" or "resources"`}
	}
	if scope == "resources" && len(p.Resources) == 0 {
		return nil, &Error{Code: ErrCodeInvalid, Message: `scope "resources" requires a non-empty resources list`}
	}

	desired, err := s.reconcileDesired(ctx, p.Desired)
	if err != nil {
		return nil, err
	}
	// §25.10 line 3824: reconciliation always reads fresh running state so
	// it never acts on a stale cache entry.
	running, err := s.collectRunning(ctx, "all", true)
	if err != nil {
		return nil, err
	}

	targets := groupTargets(drift.Diff(desired, running), desired)
	if scope == "resources" {
		targets = filterTargets(targets, p.Resources)
	}

	operationID := s.reconcileOperationID()
	result := &ReconcileResult{
		OperationID:    operationID,
		Scope:          scope,
		DryRun:         !p.Confirm,
		TotalResources: len(targets),
	}

	// §25.2 dry-run: list the resources that would reconcile without
	// applying or emitting any state-changing audit event.
	if !p.Confirm {
		for _, t := range targets {
			result.Resources = append(result.Resources, ReconcileResource{
				ResourceType: t.rtype, ResourceID: t.rid, DriftCount: t.count, Outcome: "preview",
			})
		}
		return result, nil
	}

	// §25.10 line 3842: a confirmed reconcile applies the desired state
	// through the gateway admin API. Without that client the platform
	// cannot apply — fail closed rather than report a clean no-op.
	if s.applier == nil {
		return nil, &Error{
			Code:       ErrCodeReconcileUnavailable,
			Message:    "no gateway resource applier configured; reconcile can preview but not confirm",
			HTTPStatus: 503,
		}
	}

	s.runConfirmedReconcile(ctx, operationID, scope, p.StartedBy, targets, result)
	if result.Failed > 0 {
		result.ErrorCode = ErrCodeReconcilePartial
	}
	return result, nil
}

// runConfirmedReconcile applies each target through the applier,
// emitting the §25.10 audit events, metrics, progress events, and
// Operations Inventory updates. F-25.10.1.
func (s *Service) runConfirmedReconcile(ctx context.Context, operationID, scope, startedBy string, targets []reconcileTarget, result *ReconcileResult) {
	total := len(targets)
	s.reconciles.start(operationID, scope, startedBy, total, s.now())
	defer s.reconciles.finish(operationID)

	s.audit.Emit(AuditEvent{
		Type:  EventReconciliationStarted,
		Actor: startedBy,
		Details: map[string]any{
			"operationId":    operationID,
			"scope":          scope,
			"totalResources": total,
		},
	})

	for i, t := range targets {
		step := t.label()
		applyErr := s.applier.Apply(ctx, t.rtype, t.rid, t.sub)
		rr := ReconcileResource{ResourceType: t.rtype, ResourceID: t.rid, DriftCount: t.count}
		outcome := "applied"
		if applyErr != nil {
			outcome = "failed"
			rr.Error = applyErr.Error()
			result.Failed++
		} else {
			result.Reconciled++
		}
		rr.Outcome = outcome
		result.Resources = append(result.Resources, rr)

		// §25.10 line 3859: lenny_drift_reconciled_total{resource_type,
		// outcome}.
		s.metrics.Reconciled(t.rtype, outcome)
		s.audit.Emit(AuditEvent{
			Type:  EventResourceReconciled,
			Actor: startedBy,
			Details: map[string]any{
				"operationId":  operationID,
				"resourceType": t.rtype,
				"resourceId":   t.rid,
				"outcome":      outcome,
			},
		})

		// §25.10 line 3844: the operation advances by one resource and the
		// operation_progressed event fires on every reconciliation.
		completed := i + 1
		s.reconciles.progress(operationID, completed, step)
		if s.progress != nil {
			s.progress.Progressed(ctx, ProgressInfo{
				OperationID:    operationID,
				Kind:           string(operations.KindDriftReconciliation),
				TotalSteps:     total,
				CompletedSteps: completed,
				CurrentStep:    step,
				StartedBy:      startedBy,
			})
		}
	}

	s.audit.Emit(AuditEvent{
		Type:  EventReconciliationComplete,
		Actor: startedBy,
		Details: map[string]any{
			"operationId": operationID,
			"reconciled":  result.Reconciled,
			"failed":      result.Failed,
		},
	})
}

// reconcileDesired resolves the desired state for a reconcile: the
// caller-supplied body when present, else the stored live snapshot. The
// cold-start (404) and Postgres-down (503) cases mirror Report.
func (s *Service) reconcileDesired(ctx context.Context, callerDesired map[string]any) (map[string]any, error) {
	if callerDesired != nil {
		return callerDesired, nil
	}
	snap, ok, err := s.snapshots.Get(ctx, SnapshotLive)
	if err != nil {
		return nil, &Error{Code: ErrCodeDesiredStateMissing, Message: "snapshot store unavailable: " + err.Error()}
	}
	if !ok {
		return nil, &Error{
			Code:       ErrCodeDesiredStateMissing,
			Message:    "no bootstrap_seed_snapshot and no caller-supplied desired state",
			HTTPStatus: 404,
		}
	}
	return snap.DesiredState, nil
}

// reconcileOperationID forms the canonical §25.4 operationId for a drift
// reconciliation: the drift_reconciliation kind prefix plus a clock-
// derived natural key. F-25.10.1.
func (s *Service) reconcileOperationID() string {
	return fmt.Sprintf("%s-%d", operations.KindPrefix(operations.KindDriftReconciliation), s.now().UnixNano())
}

// groupTargets folds the per-field drift changes into per-resource
// reconcile targets. A change at "pools.chat.image" groups under
// resource (pools, chat) with the desired subtree desired[pools][chat];
// a change at a singleton object ("controller.maxConcurrency") groups
// under (controller, "") with the desired subtree desired[controller].
// A change whose top-level desired value is not an object cannot be
// reconciled as a resource and is skipped. The result is ordered by
// resource label so the reconcile and its progress are deterministic.
func groupTargets(changes []drift.Change, desired map[string]any) []reconcileTarget {
	index := make(map[string]*reconcileTarget)
	var order []string
	for _, c := range changes {
		rtype, rid, sub, ok := resourceKeyOf(c.Path, desired)
		if !ok {
			continue
		}
		key := resourceLabel(rtype, rid)
		t, seen := index[key]
		if !seen {
			t = &reconcileTarget{rtype: rtype, rid: rid, sub: sub}
			index[key] = t
			order = append(order, key)
		}
		t.count++
	}
	sort.Strings(order)
	out := make([]reconcileTarget, 0, len(order))
	for _, key := range order {
		out = append(out, *index[key])
	}
	return out
}

// filterTargets keeps only the targets whose label is in the requested
// set (§25.10 scope="resources"). A requested label that is not drifted
// is silently dropped — there is nothing to reconcile for it.
func filterTargets(targets []reconcileTarget, requested []string) []reconcileTarget {
	want := make(map[string]bool, len(requested))
	for _, r := range requested {
		want[r] = true
	}
	out := make([]reconcileTarget, 0, len(targets))
	for _, t := range targets {
		if want[t.label()] {
			out = append(out, t)
		}
	}
	return out
}

// resourceKeyOf resolves a drifted field path into its reconcile
// resource identity and desired-state subtree. ok is false when the
// top-level desired value is not an object (the field cannot be applied
// as a resource PUT body).
func resourceKeyOf(path string, desired map[string]any) (rtype, rid string, sub map[string]any, ok bool) {
	segs := strings.Split(path, ".")
	rtype = segs[0]
	top, isMap := desired[rtype].(map[string]any)
	if !isMap {
		return "", "", nil, false
	}
	if len(segs) >= 2 {
		if child, childMap := top[segs[1]].(map[string]any); childMap {
			return rtype, segs[1], child, true
		}
	}
	return rtype, "", top, true
}

// ReconcileTracker projects in-flight §25.10 drift reconciliations onto
// the §25.4 Operations Inventory. It implements operations.Source so the
// unified inventory surfaces each reconcile with kind
// drift_reconciliation and the canonical progress envelope. F-25.10.1.
type ReconcileTracker struct {
	mu  sync.Mutex
	ops map[string]*operations.Operation
}

// NewReconcileTracker returns an empty tracker.
func NewReconcileTracker() *ReconcileTracker {
	return &ReconcileTracker{ops: make(map[string]*operations.Operation)}
}

// Kinds reports the §25.4 kind this source produces.
func (t *ReconcileTracker) Kinds() []operations.Kind {
	return []operations.Kind{operations.KindDriftReconciliation}
}

// List projects the in-flight reconciliations onto Operation envelopes.
// The inventory applies the canonical filter pass, so the tracker
// returns every active operation. F-25.10.1.
func (t *ReconcileTracker) List(_ context.Context, _ operations.Filter) ([]operations.Operation, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]operations.Operation, 0, len(t.ops))
	for _, op := range t.ops {
		out = append(out, cloneOperation(op))
	}
	return out, nil
}

// start registers a new in-flight reconciliation with a zero-progress
// envelope (totalSteps known, currentStep empty until the first
// resource applies).
func (t *ReconcileTracker) start(operationID, scope, startedBy string, total int, now time.Time) {
	completed := 0
	totalSteps := total
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ops[operationID] = &operations.Operation{
		OperationID: operationID,
		Kind:        operations.KindDriftReconciliation,
		Status:      operations.StatusInProgress,
		StartedBy:   startedBy,
		StartedAt:   now,
		Progress: &conventions.Progress{
			TotalSteps:     &totalSteps,
			CompletedSteps: &completed,
			// §25.10 line 3844: drift reconciliation uses linear
			// extrapolation over the resources-to-reconcile count.
			EtaMethod: conventions.EtaLinearExtrapolation,
		},
		Resources:   map[string]string{"scope": scope},
		Cancellable: false,
	}
}

// progress advances an in-flight reconciliation's progress envelope.
func (t *ReconcileTracker) progress(operationID string, completed int, currentStep string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	op, ok := t.ops[operationID]
	if !ok {
		return
	}
	c := completed
	op.Progress.CompletedSteps = &c
	op.Progress.CurrentStep = currentStep
}

// finish removes a completed reconciliation from the in-flight set.
// §25.4 surfaces in-flight reconciliations; a completed one drops out of
// the default inventory view.
func (t *ReconcileTracker) finish(operationID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.ops, operationID)
}

// cloneOperation deep-copies the mutable progress pointers so a List
// caller never observes a concurrent progress update mid-read.
func cloneOperation(op *operations.Operation) operations.Operation {
	out := *op
	if op.Progress != nil {
		p := *op.Progress
		if op.Progress.TotalSteps != nil {
			v := *op.Progress.TotalSteps
			p.TotalSteps = &v
		}
		if op.Progress.CompletedSteps != nil {
			v := *op.Progress.CompletedSteps
			p.CompletedSteps = &v
		}
		out.Progress = &p
	}
	return out
}

// Compile-time guard that ReconcileTracker is an Operations Inventory
// source.
var _ operations.Source = (*ReconcileTracker)(nil)
