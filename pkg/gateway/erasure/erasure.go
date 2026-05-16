// SPDX-License-Identifier: MIT

// Package erasure implements the §12.8 GDPR erasure orchestrator: the
// dependency-ordered DeleteByUser job that removes a user's data from
// every per-store erasure adapter.
//
// Each v1 store that holds user-scoped data exposes a DeleteByUser
// adapter (the §12.8 per-store contract). The orchestrator runs those
// adapters in a caller-supplied dependency order — a store whose rows
// reference another store's rows is erased first — and is fail-fast:
// the first store error aborts the job and is returned with the
// partial result, so a §12.8 phase-recovery retry resumes from a
// consistent point.
package erasure

import (
	"context"
	"fmt"
)

// DeleteByUserFunc is a store's §12.8 GDPR-erasure adapter: it removes
// the user's data within the tenant and returns the count deleted.
type DeleteByUserFunc func(ctx context.Context, tenantID, userID string) (int, error)

// Eraser pairs a store name with its DeleteByUser adapter. The name
// labels the store's deleted-row count in the erasure Result.
type Eraser struct {
	Name         string
	DeleteByUser DeleteByUserFunc
}

// Orchestrator runs DeleteByUser across an ordered list of stores.
type Orchestrator struct {
	erasers []Eraser
}

// New returns an Orchestrator over the erasers in dependency order —
// the first eraser is erased first.
func New(erasers ...Eraser) *Orchestrator {
	return &Orchestrator{erasers: append([]Eraser(nil), erasers...)}
}

// Result is the outcome of a DeleteByUser job.
type Result struct {
	// Deleted maps each store's name to its deleted-row count, for the
	// stores the job reached.
	Deleted map[string]int
	// Total is the sum of Deleted across stores.
	Total int
}

// DeleteByUser runs every store's erasure in dependency order. It is
// fail-fast: the first store error aborts the job and is returned
// alongside the partial Result covering the stores erased so far.
func (o *Orchestrator) DeleteByUser(ctx context.Context, tenantID, userID string) (Result, error) {
	res := Result{Deleted: map[string]int{}}
	for _, e := range o.erasers {
		n, err := e.DeleteByUser(ctx, tenantID, userID)
		if err != nil {
			return res, fmt.Errorf("erasure: store %q DeleteByUser failed: %w", e.Name, err)
		}
		res.Deleted[e.Name] = n
		res.Total += n
	}
	return res, nil
}
