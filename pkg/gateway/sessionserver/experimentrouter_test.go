// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §10.7 ExperimentRouter — variant assignment at session creation.

// routedExperiment seeds an active percentage-mode experiment whose
// single variant has a weight near 1, so every session on the base
// runtime is routed to the variant.
func routedExperiment(t *testing.T, exps experimentstore.Store, baseRuntime, variantRuntime, variantPool string) {
	t.Helper()
	if err := exps.Create(context.Background(), experimentstore.Experiment{
		ID: "exp_1", TenantID: "default", Status: experiment.StatusActive,
		BaseRuntime: baseRuntime,
		Variants: []experimentstore.Variant{
			{ID: "treatment", Runtime: variantRuntime, Pool: variantPool, Weight: 0.999999},
		},
		TargetingMode: experiment.TargetingPercentage,
		Sticky:        experiment.StickySession,
		Propagation:   experiment.PropagationInherit,
	}); err != nil {
		t.Fatalf("seed experiment: %v", err)
	}
}

func routerServer(t *testing.T, sessionID string, exps experimentstore.Store) (http.Handler, *memstore.Store) {
	t.Helper()
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		Experiments: exps,
		IDFunc:      func() string { return sessionID },
	})
	return srv.Handler(), store
}

func TestExperimentRouterEnrollsSessionAtCreation(t *testing.T) {
	exps := experimentstore.NewMemory()
	routedExperiment(t, exps, "claude-code", "claude-code-v2", "cc-v2-pool")
	h, store := routerServer(t, "sess_routed", exps)

	rr := postSession(t, h, "/v1/sessions", "alice", "default")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	got, err := store.Get(context.Background(), "default", "sess_routed")
	if err != nil {
		t.Fatalf("Get created session: %v", err)
	}
	if got.ExperimentContext == nil {
		t.Fatalf("session was not enrolled in the experiment")
	}
	if got.ExperimentContext.ExperimentID != "exp_1" || got.ExperimentContext.VariantID != "treatment" {
		t.Errorf("experimentContext = %+v, want exp_1/treatment", got.ExperimentContext)
	}
	// §10.7: an enrolled session is routed onto the variant's runtime and pool.
	if got.RuntimeRef != "claude-code-v2" || got.PoolRef != "cc-v2-pool" {
		t.Errorf("session not routed to the variant: runtime=%q pool=%q", got.RuntimeRef, got.PoolRef)
	}
}

func TestExperimentRouterSkipsNonMatchingBaseRuntime(t *testing.T) {
	exps := experimentstore.NewMemory()
	// The experiment targets a different base runtime than the session.
	routedExperiment(t, exps, "some-other-runtime", "v2", "v2-pool")
	h, store := routerServer(t, "sess_unrouted", exps)

	rr := postSession(t, h, "/v1/sessions", "alice", "default")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d", rr.Code)
	}
	got, _ := store.Get(context.Background(), "default", "sess_unrouted")
	if got.ExperimentContext != nil {
		t.Errorf("session enrolled in a non-matching experiment: %+v", got.ExperimentContext)
	}
	if got.RuntimeRef != "claude-code" {
		t.Errorf("runtime = %q, want the unchanged base runtime claude-code", got.RuntimeRef)
	}
}

func TestExperimentRouterNoExperimentsLeavesSessionUnenrolled(t *testing.T) {
	h, store := routerServer(t, "sess_none", experimentstore.NewMemory())
	rr := postSession(t, h, "/v1/sessions", "alice", "default")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d", rr.Code)
	}
	got, _ := store.Get(context.Background(), "default", "sess_none")
	if got.ExperimentContext != nil {
		t.Errorf("session enrolled with no experiments defined: %+v", got.ExperimentContext)
	}
}
