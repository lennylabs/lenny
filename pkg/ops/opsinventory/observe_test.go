// SPDX-License-Identifier: MIT

package opsinventory_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/operations"
	"github.com/lennylabs/lenny/pkg/ops/opsinventory"
)

type fakeLister struct{ page operations.Page }

func (f *fakeLister) List(context.Context, operations.Filter, int) operations.Page { return f.page }

func opWithProgress(id string, status operations.Status, pr *conventions.Progress) operations.Operation {
	return operations.Operation{OperationID: id, Kind: operations.KindPlatformUpgrade, Status: status, Progress: pr}
}

func ptrF(v float64) *float64 { return &v }
func ptrI(v int) *int         { return &v }

// spec §25.2 line 399: the observe loop sets lenny_ops_operations_stalled
// to the count of in-flight operations with stalledForSeconds > 0.
func TestObserverCountsStalled(t *testing.T) {
	stalled := -1.0
	lister := &fakeLister{page: operations.Page{Operations: []operations.Operation{
		opWithProgress("upgrade-1", operations.StatusInProgress, &conventions.Progress{StalledForSeconds: ptrI(120)}),
		opWithProgress("upgrade-2", operations.StatusInProgress, &conventions.Progress{StalledForSeconds: nil}),
		opWithProgress("upgrade-3", operations.StatusInProgress, &conventions.Progress{StalledForSeconds: ptrI(5)}),
	}}}
	obs := opsinventory.NewObserver(lister, func(n float64) { stalled = n }, nil)
	if err := obs.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if stalled != 2 {
		t.Errorf("stalled gauge = %v, want 2", stalled)
	}
}

// spec §25.2 line 399: with no stalled operations the gauge clears to 0.
func TestObserverClearsStalledGauge(t *testing.T) {
	stalled := -1.0
	lister := &fakeLister{page: operations.Page{Operations: []operations.Operation{
		opWithProgress("upgrade-1", operations.StatusInProgress, &conventions.Progress{StalledForSeconds: nil}),
	}}}
	obs := opsinventory.NewObserver(lister, func(n float64) { stalled = n }, nil)
	_ = obs.Tick(context.Background())
	if stalled != 0 {
		t.Errorf("stalled gauge = %v, want 0", stalled)
	}
}

// spec §25.2 line 401: the first sighting only establishes the baseline;
// a subsequent advance emits operation_progressed with the crossed
// thresholds and the step transition.
func TestObserverEmitsOnAdvance(t *testing.T) {
	var got []opsinventory.ProgressUpdate
	lister := &fakeLister{page: operations.Page{Operations: []operations.Operation{
		opWithProgress("upgrade-1", operations.StatusInProgress, &conventions.Progress{
			Percent: ptrF(5), CurrentStep: "preflight",
		}),
	}}}
	obs := opsinventory.NewObserver(lister, nil, func(_ context.Context, ev opsinventory.ProgressUpdate) {
		got = append(got, ev)
	})

	// First tick: baseline only, no emit.
	_ = obs.Tick(context.Background())
	if len(got) != 0 {
		t.Fatalf("first tick emitted %d, want 0 (baseline)", len(got))
	}

	// Advance to 55% and a new step.
	lister.page = operations.Page{Operations: []operations.Operation{
		opWithProgress("upgrade-1", operations.StatusInProgress, &conventions.Progress{
			Percent: ptrF(55), CurrentStep: "migrating",
		}),
	}}
	_ = obs.Tick(context.Background())
	if len(got) != 1 {
		t.Fatalf("second tick emitted %d, want 1", len(got))
	}
	ev := got[0]
	if !ev.StepTransition {
		t.Errorf("StepTransition = false, want true")
	}
	if len(ev.CrossedThresholds) != 3 || ev.CrossedThresholds[0] != 10 {
		t.Errorf("CrossedThresholds = %v, want [10 25 50]", ev.CrossedThresholds)
	}

	// No further change -> no emit.
	got = nil
	_ = obs.Tick(context.Background())
	if len(got) != 0 {
		t.Errorf("third tick emitted %d, want 0 (no change)", len(got))
	}
}
