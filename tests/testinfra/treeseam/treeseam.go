// SPDX-License-Identifier: MIT

// Package treeseam adapts the tree budget fakes the session core's tests
// use to the platform tools seam's tree budget operations, which the session
// core holds as seam.TreeBudgets. A test fake that records only the budget
// return, only the re-arm, or only the high-watermark read is wrapped in
// Budgets, and the parts it leaves nil do nothing.
//
// It is test infrastructure and implements no platform behavior, so it
// carries no specification annotation.
package treeseam

import (
	"context"

	"github.com/lennylabs/lenny/pkg/gateway/platformtools"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// Returner is a fake of the budget return.
type Returner interface {
	Return(ctx context.Context, r treebudget.Reservation) error
}

// Rearmer is a fake of the budget key re-arm.
type Rearmer interface {
	RearmTree(ctx context.Context, rootSessionID string, parentIDs []string) error
}

// HighWatermarks is a fake of the parallel-children high-watermark read.
type HighWatermarks interface {
	ObserveHighWatermark(ctx context.Context, rootSessionID string) (int64, bool, error)
}

// Budgets serves seam.TreeBudgets over the fakes it holds.
type Budgets struct {
	Returner       Returner
	Rearmer        Rearmer
	HighWatermarks HighWatermarks
}

var _ seam.TreeBudgets = Budgets{}

// ReturnBudget hands the reservation the return gives back to Returner.
func (b Budgets) ReturnBudget(ctx context.Context, r seam.BudgetReturn) error {
	if b.Returner == nil {
		return nil
	}
	return b.Returner.Return(ctx, platformtools.Reservation(r))
}

// RearmBudget hands the re-arm to Rearmer.
func (b Budgets) RearmBudget(ctx context.Context, rootSessionID string, parentSessionIDs []string) error {
	if b.Rearmer == nil {
		return nil
	}
	return b.Rearmer.RearmTree(ctx, rootSessionID, parentSessionIDs)
}

// ObserveHighWatermark hands the read to HighWatermarks, or reports none.
func (b Budgets) ObserveHighWatermark(ctx context.Context, rootSessionID string) (int64, bool, error) {
	if b.HighWatermarks == nil {
		return 0, false, nil
	}
	return b.HighWatermarks.ObserveHighWatermark(ctx, rootSessionID)
}
