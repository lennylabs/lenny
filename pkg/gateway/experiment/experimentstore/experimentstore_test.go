// SPDX-License-Identifier: MIT

package experimentstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore"
)

// spec: §10.7 experiment registry.

func validExperiment(tenant, id string) experimentstore.Experiment {
	return experimentstore.Experiment{
		ID:          id,
		TenantID:    tenant,
		Status:      experiment.StatusActive,
		BaseRuntime: "claude-worker",
		Variants: []experimentstore.Variant{
			{ID: "treatment", Runtime: "claude-worker-v2", Pool: "cw-v2-pool", Weight: 0.1},
		},
		TargetingMode: experiment.TargetingPercentage,
		Sticky:        experiment.StickyUser,
		Propagation:   experiment.PropagationInherit,
	}
}

func TestCreateAndGet(t *testing.T) {
	m := experimentstore.NewMemory()
	if err := m.Create(context.Background(), validExperiment("acme", "exp_1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := m.Get(context.Background(), "acme", "exp_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BaseRuntime != "claude-worker" || got.Status != experiment.StatusActive {
		t.Errorf("Get = %+v, want the stored experiment", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("Create must stamp CreatedAt and UpdatedAt")
	}
}

func TestCreateRejectsInvalidExperiment(t *testing.T) {
	m := experimentstore.NewMemory()
	bad := validExperiment("acme", "exp_bad")
	bad.Variants = []experimentstore.Variant{{ID: "control", Weight: 0.1}} // reserved id
	if err := m.Create(context.Background(), bad); err == nil {
		t.Error("Create accepted a variant using the reserved \"control\" id")
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	m := experimentstore.NewMemory()
	if err := m.Create(context.Background(), validExperiment("acme", "exp_dup")); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if err := m.Create(context.Background(), validExperiment("acme", "exp_dup")); !errors.Is(err, experimentstore.ErrAlreadyExists) {
		t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
	}
}

func TestGetUnknownReturnsNotFound(t *testing.T) {
	m := experimentstore.NewMemory()
	if _, err := m.Get(context.Background(), "acme", "absent"); !errors.Is(err, experimentstore.ErrNotFound) {
		t.Errorf("Get unknown: got %v, want ErrNotFound", err)
	}
}

// spec: §10.7 line 1092 — the platform-global PoolScalingController
// reads every experiment across tenants through ListAll, ordered by
// (tenantID, id), unlike the tenant-scoped List.
func TestListAllIsCrossTenantAndOrdered(t *testing.T) {
	m := experimentstore.NewMemory()
	if got, err := m.ListAll(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("empty ListAll = (%d,%v), want (0,nil)", len(got), err)
	}
	_ = m.Create(context.Background(), validExperiment("globex", "exp_b"))
	_ = m.Create(context.Background(), validExperiment("acme", "exp_b"))
	_ = m.Create(context.Background(), validExperiment("acme", "exp_a"))

	all, err := m.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	want := []struct{ tenant, id string }{{"acme", "exp_a"}, {"acme", "exp_b"}, {"globex", "exp_b"}}
	if len(all) != len(want) {
		t.Fatalf("ListAll len = %d, want %d", len(all), len(want))
	}
	for i, w := range want {
		if all[i].TenantID != w.tenant || all[i].ID != w.id {
			t.Errorf("ListAll[%d] = (%s,%s), want (%s,%s)", i, all[i].TenantID, all[i].ID, w.tenant, w.id)
		}
	}
}

func TestGetIsTenantScoped(t *testing.T) {
	m := experimentstore.NewMemory()
	_ = m.Create(context.Background(), validExperiment("acme", "shared_id"))
	if _, err := m.Get(context.Background(), "globex", "shared_id"); !errors.Is(err, experimentstore.ErrNotFound) {
		t.Error("an experiment must not be visible to another tenant under the same id")
	}
}

func TestUpdateMutatesAndAdvancesUpdatedAt(t *testing.T) {
	m := experimentstore.NewMemory()
	_ = m.Create(context.Background(), validExperiment("acme", "exp_u"))
	before, _ := m.Get(context.Background(), "acme", "exp_u")

	updated, err := m.Update(context.Background(), "acme", "exp_u", func(e *experimentstore.Experiment) error {
		e.Status = experiment.StatusPaused
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Status != experiment.StatusPaused {
		t.Errorf("Status = %q, want paused", updated.Status)
	}
	if !updated.UpdatedAt.After(before.UpdatedAt) && !updated.UpdatedAt.Equal(before.UpdatedAt) {
		t.Error("UpdatedAt should advance or hold, never regress")
	}
}

func TestUpdateRejectsInvalidMutation(t *testing.T) {
	m := experimentstore.NewMemory()
	_ = m.Create(context.Background(), validExperiment("acme", "exp_iv"))
	_, err := m.Update(context.Background(), "acme", "exp_iv", func(e *experimentstore.Experiment) error {
		e.Variants[0].Weight = 1.5 // out of (0, 1)
		return nil
	})
	if err == nil {
		t.Error("Update accepted a mutation that violates the variant-weight constraint")
	}
	got, _ := m.Get(context.Background(), "acme", "exp_iv")
	if got.Variants[0].Weight != 0.1 {
		t.Errorf("a rejected Update mutated stored state: weight = %g", got.Variants[0].Weight)
	}
}

func TestUpdateUnknownReturnsNotFound(t *testing.T) {
	m := experimentstore.NewMemory()
	_, err := m.Update(context.Background(), "acme", "absent", func(*experimentstore.Experiment) error { return nil })
	if !errors.Is(err, experimentstore.ErrNotFound) {
		t.Errorf("Update unknown: got %v, want ErrNotFound", err)
	}
}

func TestListIsTenantScopedAndSorted(t *testing.T) {
	m := experimentstore.NewMemory()
	_ = m.Create(context.Background(), validExperiment("acme", "exp_b"))
	_ = m.Create(context.Background(), validExperiment("acme", "exp_a"))
	_ = m.Create(context.Background(), validExperiment("globex", "exp_z"))

	got, err := m.List(context.Background(), "acme")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0].ID != "exp_a" || got[1].ID != "exp_b" {
		t.Errorf("List = %v, want [exp_a exp_b] id-ascending, acme-scoped", ids(got))
	}
}

func TestDelete(t *testing.T) {
	m := experimentstore.NewMemory()
	_ = m.Create(context.Background(), validExperiment("acme", "exp_d"))
	if err := m.Delete(context.Background(), "acme", "exp_d"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := m.Get(context.Background(), "acme", "exp_d"); !errors.Is(err, experimentstore.ErrNotFound) {
		t.Error("a deleted experiment is still retrievable")
	}
	if err := m.Delete(context.Background(), "acme", "exp_d"); !errors.Is(err, experimentstore.ErrNotFound) {
		t.Errorf("Delete of an absent experiment: got %v, want ErrNotFound", err)
	}
}

func TestUpdateIsolatesStoredVariants(t *testing.T) {
	m := experimentstore.NewMemory()
	_ = m.Create(context.Background(), validExperiment("acme", "exp_iso"))
	got, _ := m.Get(context.Background(), "acme", "exp_iso")
	got.Variants[0].Weight = 0.9 // mutate the returned copy
	reread, _ := m.Get(context.Background(), "acme", "exp_iso")
	if reread.Variants[0].Weight != 0.1 {
		t.Errorf("stored variant weight = %g, want 0.1 — the returned copy aliases stored state",
			reread.Variants[0].Weight)
	}
}

func ids(es []experimentstore.Experiment) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}

// spec: §12.1 line 5 — DeleteByUser on an experiment-definitions
// store is a no-op (experiments are tenant-scoped) and returns 0.
func TestDeleteByUserIsNoOp_spec_12_1(t *testing.T) {
	m := experimentstore.NewMemory()
	_ = m.Create(context.Background(), validExperiment("acme", "exp_1"))
	n, err := m.DeleteByUser(context.Background(), "acme", "alice")
	if err != nil || n != 0 {
		t.Errorf("DeleteByUser: n=%d err=%v", n, err)
	}
	if _, err := m.Get(context.Background(), "acme", "exp_1"); err != nil {
		t.Errorf("experiment should survive DeleteByUser: %v", err)
	}
}

// spec: §12.1 line 5 / §12.8 Phase 4 — DeleteByTenant removes every
// experiment belonging to the tenant.
func TestDeleteByTenantRemovesAll_spec_12_1(t *testing.T) {
	m := experimentstore.NewMemory()
	_ = m.Create(context.Background(), validExperiment("acme", "exp_a"))
	_ = m.Create(context.Background(), validExperiment("acme", "exp_b"))
	_ = m.Create(context.Background(), validExperiment("globex", "exp_a"))
	n, err := m.DeleteByTenant(context.Background(), "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if n != 2 {
		t.Errorf("DeleteByTenant should remove 2 acme experiments, got %d", n)
	}
	if _, err := m.Get(context.Background(), "acme", "exp_a"); !errors.Is(err, experimentstore.ErrNotFound) {
		t.Errorf("acme/exp_a should be gone: %v", err)
	}
	if _, err := m.Get(context.Background(), "globex", "exp_a"); err != nil {
		t.Errorf("globex/exp_a should survive: %v", err)
	}
}
