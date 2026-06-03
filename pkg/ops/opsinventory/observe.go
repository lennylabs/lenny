// SPDX-License-Identifier: MIT

package opsinventory

import (
	"context"
	"sync"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/operations"
)

// Lister is the §25.4 Operations Inventory read surface the Observer
// scans. *operations.Inventory satisfies it.
type Lister interface {
	List(ctx context.Context, filter operations.Filter, limit int) operations.Page
}

// ProgressUpdate is one §25.2 line 401 operation_progressed signal the
// Observer raises when an in-flight operation advances a step or crosses
// a named percent threshold.
type ProgressUpdate struct {
	OperationID       string
	Kind              string
	Percent           *float64
	CompletedSteps    *int
	TotalSteps        *int
	CurrentStep       string
	CurrentStepDetail string
	// CrossedThresholds are the §25.2 named percent thresholds (10, 25,
	// 50, 75, 90, 95, 99) the operation crossed since the prior tick.
	CrossedThresholds []int
	// StepTransition reports that currentStep changed since the prior tick.
	StepTransition bool
}

// Observer implements the §25.2 operations-observe loop: on each tick it
// scans the Operations Inventory, maintains the
// lenny_ops_operations_stalled gauge (the OperationStalled alert
// backing, §25.2 line 399), and raises operation_progressed updates on
// step transitions and percent-threshold crossings (§25.2 line 401).
//
// It runs leader-only so a multi-replica deployment emits one stream of
// progress events and one authoritative gauge value, not one per replica.
//
// spec: §25.2 lines 399-401.
type Observer struct {
	inv        Lister
	setStalled func(float64)
	emit       func(ctx context.Context, ev ProgressUpdate)

	mu       sync.Mutex
	lastSeen map[string]seenProgress
}

type seenProgress struct {
	percent float64 // -1 when the op had no percent basis
	step    string
}

// NewObserver returns an Observer over inv. setStalled receives the count
// of stalled in-flight operations on every tick (nil disables the gauge
// update); emit receives one ProgressUpdate per advancing operation (nil
// disables event emission).
func NewObserver(inv Lister, setStalled func(float64), emit func(ctx context.Context, ev ProgressUpdate)) *Observer {
	return &Observer{inv: inv, setStalled: setStalled, emit: emit, lastSeen: map[string]seenProgress{}}
}

// Tick performs one §25.2 observation pass. It lists the in-flight
// operations (the §25.4 default status set), updates the stalled gauge,
// and emits operation_progressed for each operation that advanced since
// the prior tick.
func (o *Observer) Tick(ctx context.Context) error {
	page := o.inv.List(ctx, operations.Filter{Statuses: operations.DefaultStatuses}, conventions.MaxPageLimit)

	o.mu.Lock()
	defer o.mu.Unlock()

	stalled := 0
	present := make(map[string]bool, len(page.Operations))
	for _, op := range page.Operations {
		present[op.OperationID] = true
		pr := op.Progress
		if pr != nil && pr.StalledForSeconds != nil && *pr.StalledForSeconds > 0 {
			stalled++
		}
		o.observe(ctx, op)
	}
	// Drop the last-seen entries of operations that left the in-flight set
	// so the map does not grow without bound.
	for id := range o.lastSeen {
		if !present[id] {
			delete(o.lastSeen, id)
		}
	}

	if o.setStalled != nil {
		o.setStalled(float64(stalled))
	}
	return nil
}

// observe compares one operation against its prior reading and emits an
// operation_progressed update on a step transition or a percent-threshold
// crossing. The first sighting of an operation only records the baseline
// so the loop's startup does not replay historical crossings.
func (o *Observer) observe(ctx context.Context, op operations.Operation) {
	if op.Progress == nil {
		return
	}
	cur := seenProgress{percent: -1, step: op.Progress.CurrentStep}
	if op.Progress.Percent != nil {
		cur.percent = *op.Progress.Percent
	}

	prev, known := o.lastSeen[op.OperationID]
	o.lastSeen[op.OperationID] = cur
	if !known {
		return
	}

	stepTransition := cur.step != "" && cur.step != prev.step
	crossed := operations.CrossedThresholds(prev.percent, cur.percent)
	if !stepTransition && len(crossed) == 0 {
		return
	}
	if o.emit != nil {
		o.emit(ctx, ProgressUpdate{
			OperationID:       op.OperationID,
			Kind:              string(op.Kind),
			Percent:           op.Progress.Percent,
			CompletedSteps:    op.Progress.CompletedSteps,
			TotalSteps:        op.Progress.TotalSteps,
			CurrentStep:       op.Progress.CurrentStep,
			CurrentStepDetail: op.Progress.CurrentStepDetail,
			CrossedThresholds: crossed,
			StepTransition:    stepTransition,
		})
	}
}
