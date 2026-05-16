// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"sort"

	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// applyExperimentRouting runs the §10.7 ExperimentRouter over a
// session at creation. The tenant's experiments whose baseRuntime
// matches the session's requested runtime are evaluated in created_at
// order; the first one that buckets the session to a non-control
// variant enrolls it. On enrollment the session's runtime and pool are
// rewritten to the variant's and the §10.7 experimentContext is
// recorded. A session that no experiment enrolls is left unchanged and
// runs the base runtime.
func (s *Server) applyExperimentRouting(ctx context.Context, row *sessionstore.Session) {
	if s.experiments == nil {
		return
	}
	all, err := s.experiments.List(ctx, row.TenantID)
	if err != nil || len(all) == 0 {
		return
	}
	candidates := make([]experimentstore.Experiment, 0, len(all))
	for _, e := range all {
		if e.BaseRuntime == row.RuntimeRef {
			candidates = append(candidates, e)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
	})
	defs := make([]experiment.Definition, len(candidates))
	for i, e := range candidates {
		defs[i] = e.Definition()
	}
	assignment := experiment.Route(defs, row.UserID, row.ID)
	if assignment.ExperimentID == "" {
		return
	}
	row.ExperimentContext = &sessionstore.ExperimentContext{
		ExperimentID: assignment.ExperimentID,
		VariantID:    assignment.VariantID,
	}
	// Route the session onto the assigned variant's runtime and pool.
	for _, e := range candidates {
		if e.ID != assignment.ExperimentID {
			continue
		}
		for _, v := range e.Variants {
			if v.ID != assignment.VariantID {
				continue
			}
			if v.Runtime != "" {
				row.RuntimeRef = v.Runtime
			}
			if v.Pool != "" {
				row.PoolRef = v.Pool
			}
		}
	}
}
