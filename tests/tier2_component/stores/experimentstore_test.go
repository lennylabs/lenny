//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §10.7 experiment registry, exercising the
// Postgres-backed pkg/gateway/experimentstore/pgstore against a real
// container with the production migrations applied. Covers the
// create/get round-trip including the nested jsonb body, the sentinel
// errors, cross-tenant isolation, the §10.7 status-transition
// lifecycle as driven through the mutate closure, id-ascending List,
// and the hard-delete semantics.
package stores_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	experimentpg "github.com/lennylabs/lenny/pkg/gateway/experimentstore/pgstore"
)

// validExperiment builds an experiment that passes the §10.7 admission
// validation.
func validExperiment(tenant, id string) experimentstore.Experiment {
	return experimentstore.Experiment{
		ID:          id,
		TenantID:    tenant,
		Status:      experiment.StatusActive,
		BaseRuntime: "claude-worker",
		Variants: []experimentstore.Variant{
			{ID: "treatment", Runtime: "claude-worker-v2", Pool: "cw-v2-pool", Weight: 0.1, InitialMinWarm: 2},
		},
		TargetingMode: experiment.TargetingPercentage,
		Sticky:        experiment.StickyUser,
		Propagation:   experiment.PropagationInherit,
	}
}

// spec: 10.7
// diagnosis: the Postgres-backed experiment registry in
// pkg/gateway/experimentstore/pgstore did not behave as specified.
// Create and Get must round-trip an experiment including its nested
// jsonb body (variants, targeting, propagation), Create must run the
// §10.7 admission validation and reject duplicates with
// ErrAlreadyExists, cross-tenant Get must return ErrNotFound, Update
// must re-validate, advance UpdatedAt, and propagate a mutate error
// verbatim so the §10.7 status-transition lifecycle holds, List must
// return the tenant's experiments id-ascending, and Delete must
// remove the row and report ErrNotFound when absent.
func TestExperimentStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := experimentpg.New(pg.Pool)
	ctx := context.Background()

	t.Run("create and get round-trip including nested jsonb", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		want := validExperiment(tenant, "exp_round_trip")
		if err := store.Create(ctx, want); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, tenant, want.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.ID != want.ID || got.TenantID != want.TenantID ||
			got.Status != want.Status || got.BaseRuntime != want.BaseRuntime {
			t.Errorf("scalar field mismatch:\n got %+v\nwant %+v", got, want)
		}
		if got.TargetingMode != want.TargetingMode || got.Sticky != want.Sticky ||
			got.Propagation != want.Propagation {
			t.Errorf("targeting/propagation mismatch:\n got %+v\nwant %+v", got, want)
		}
		if !slices.Equal(got.Variants, want.Variants) {
			t.Errorf("Variants lost in jsonb round-trip:\n got %+v\nwant %+v",
				got.Variants, want.Variants)
		}
		if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
			t.Error("Create must stamp CreatedAt and UpdatedAt")
		}
	})

	t.Run("create runs §10.7 admission validation", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		bad := validExperiment(tenant, "exp_bad")
		bad.Variants = []experimentstore.Variant{{ID: "control", Weight: 0.1}} // reserved id
		if err := store.Create(ctx, bad); err == nil {
			t.Error("Create accepted a variant using the reserved \"control\" id")
		}
		if _, err := store.Get(ctx, tenant, "exp_bad"); !errors.Is(err, experimentstore.ErrNotFound) {
			t.Errorf("a rejected Create left a row: got %v, want ErrNotFound", err)
		}
	})

	t.Run("duplicate create is rejected", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		e := validExperiment(tenant, "exp_dup")
		if err := store.Create(ctx, e); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		if err := store.Create(ctx, e); !errors.Is(err, experimentstore.ErrAlreadyExists) {
			t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
		}
	})

	t.Run("get missing and cross-tenant return ErrNotFound", func(t *testing.T) {
		owner := freshTenant(t, ctx, pg)
		intruder := freshTenant(t, ctx, pg)
		if _, err := store.Get(ctx, owner, "absent"); !errors.Is(err, experimentstore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
		if err := store.Create(ctx, validExperiment(owner, "shared_id")); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := store.Get(ctx, intruder, "shared_id"); !errors.Is(err, experimentstore.ErrNotFound) {
			t.Errorf("cross-tenant Get: got %v, want ErrNotFound", err)
		}
	})

	t.Run("update mutates, advances updated_at, and rejects missing", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if err := store.Create(ctx, validExperiment(tenant, "exp_update")); err != nil {
			t.Fatalf("Create: %v", err)
		}
		before, _ := store.Get(ctx, tenant, "exp_update")
		updated, err := store.Update(ctx, tenant, "exp_update", func(e *experimentstore.Experiment) error {
			e.BaseRuntime = "claude-worker-next"
			e.Variants[0].Weight = 0.2
			return nil
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if updated.BaseRuntime != "claude-worker-next" || updated.Variants[0].Weight != 0.2 {
			t.Errorf("Update result not applied: %+v", updated)
		}
		if !updated.UpdatedAt.After(before.UpdatedAt) {
			t.Errorf("UpdatedAt did not advance: before=%v after=%v",
				before.UpdatedAt, updated.UpdatedAt)
		}
		persisted, _ := store.Get(ctx, tenant, "exp_update")
		if persisted.BaseRuntime != "claude-worker-next" || persisted.Variants[0].Weight != 0.2 {
			t.Errorf("Update did not persist: %+v", persisted)
		}
		if _, err := store.Update(ctx, tenant, "ghost", func(*experimentstore.Experiment) error {
			return nil
		}); !errors.Is(err, experimentstore.ErrNotFound) {
			t.Errorf("Update missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("update re-validates and aborts the write on an invalid mutation", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if err := store.Create(ctx, validExperiment(tenant, "exp_invalid")); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := store.Update(ctx, tenant, "exp_invalid", func(e *experimentstore.Experiment) error {
			e.Variants[0].Weight = 1.5 // out of (0, 1)
			return nil
		}); err == nil {
			t.Error("Update accepted a mutation that violates the variant-weight constraint")
		}
		got, _ := store.Get(ctx, tenant, "exp_invalid")
		if got.Variants[0].Weight != 0.1 {
			t.Errorf("a rejected Update mutated stored state: weight = %g", got.Variants[0].Weight)
		}
	})

	t.Run("update propagates a mutate error verbatim", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if err := store.Create(ctx, validExperiment(tenant, "exp_mutate_err")); err != nil {
			t.Fatalf("Create: %v", err)
		}
		sentinel := errors.New("mutate boom")
		if _, err := store.Update(ctx, tenant, "exp_mutate_err", func(*experimentstore.Experiment) error {
			return sentinel
		}); !errors.Is(err, sentinel) {
			t.Errorf("Update mutate error: got %v, want sentinel", err)
		}
	})

	t.Run("status-transition lifecycle holds through the mutate closure", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if err := store.Create(ctx, validExperiment(tenant, "exp_lifecycle")); err != nil {
			t.Fatalf("Create: %v", err)
		}
		// active -> paused is a legal §10.7 transition.
		paused, err := store.Update(ctx, tenant, "exp_lifecycle", func(e *experimentstore.Experiment) error {
			if !e.Status.CanTransitionTo(experiment.StatusPaused) {
				return errors.New("illegal transition")
			}
			e.Status = experiment.StatusPaused
			return nil
		})
		if err != nil {
			t.Fatalf("active -> paused Update: %v", err)
		}
		if paused.Status != experiment.StatusPaused {
			t.Errorf("Status = %q, want paused", paused.Status)
		}
		// paused -> concluded is legal; the experiment then becomes
		// immutable.
		concluded, err := store.Update(ctx, tenant, "exp_lifecycle", func(e *experimentstore.Experiment) error {
			if !e.Status.CanTransitionTo(experiment.StatusConcluded) {
				return errors.New("illegal transition")
			}
			e.Status = experiment.StatusConcluded
			return nil
		})
		if err != nil {
			t.Fatalf("paused -> concluded Update: %v", err)
		}
		if concluded.Status != experiment.StatusConcluded {
			t.Errorf("Status = %q, want concluded", concluded.Status)
		}
		// concluded -> active is rejected: the store propagates the
		// mutate closure's transition error verbatim.
		transitionErr := errors.New("invalid transition out of concluded")
		if _, err := store.Update(ctx, tenant, "exp_lifecycle", func(e *experimentstore.Experiment) error {
			if !e.Status.CanTransitionTo(experiment.StatusActive) {
				return transitionErr
			}
			e.Status = experiment.StatusActive
			return nil
		}); !errors.Is(err, transitionErr) {
			t.Errorf("concluded -> active: got %v, want the transition error", err)
		}
		stillConcluded, _ := store.Get(ctx, tenant, "exp_lifecycle")
		if stillConcluded.Status != experiment.StatusConcluded {
			t.Errorf("a rejected transition mutated stored status: got %q, want concluded",
				stillConcluded.Status)
		}
	})

	t.Run("list is tenant-scoped and id-ascending", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		other := freshTenant(t, ctx, pg)
		for _, id := range []string{"exp_b", "exp_a", "exp_c"} {
			if err := store.Create(ctx, validExperiment(tenant, id)); err != nil {
				t.Fatalf("Create %s: %v", id, err)
			}
		}
		if err := store.Create(ctx, validExperiment(other, "exp_other")); err != nil {
			t.Fatalf("Create exp_other: %v", err)
		}
		got, err := store.List(ctx, tenant)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if want := []string{"exp_a", "exp_b", "exp_c"}; !slices.Equal(idsOf(got), want) {
			t.Errorf("List = %v, want %v id-ascending, tenant-scoped", idsOf(got), want)
		}
		// An empty tenant returns no rows and no error.
		empty := freshTenant(t, ctx, pg)
		none, err := store.List(ctx, empty)
		if err != nil {
			t.Fatalf("List empty tenant: %v", err)
		}
		if len(none) != 0 {
			t.Errorf("List empty tenant: got %d rows, want 0", len(none))
		}
	})

	t.Run("delete removes the row and reports ErrNotFound when absent", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if err := store.Create(ctx, validExperiment(tenant, "exp_delete")); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := store.Delete(ctx, tenant, "exp_delete"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := store.Get(ctx, tenant, "exp_delete"); !errors.Is(err, experimentstore.ErrNotFound) {
			t.Errorf("Get after Delete: got %v, want ErrNotFound", err)
		}
		if err := store.Delete(ctx, tenant, "exp_delete"); !errors.Is(err, experimentstore.ErrNotFound) {
			t.Errorf("Delete missing: got %v, want ErrNotFound", err)
		}
	})
}

func idsOf(es []experimentstore.Experiment) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}
