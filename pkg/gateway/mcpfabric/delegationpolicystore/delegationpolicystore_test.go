// SPDX-License-Identifier: MIT

package delegationpolicystore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
)

// spec: §8.3 DelegationPolicy as a first-class resource.

// spec: §4.2 line 172 — delegation policies are tenant-scoped. Tests
// use the built-in `acme` tenant; multi-tenant isolation is exercised
// at the bottom of this file.
const testTenantID = "acme"

func samplePolicy(name string) delegationpolicystore.DelegationPolicy {
	return delegationpolicystore.DelegationPolicy{
		TenantID: testTenantID,
		Name:     name,
		Rules: []delegationpolicystore.Rule{
			{
				Target: delegationpolicystore.Target{
					MatchLabels: map[string]string{"team": "platform"},
					Types:       []string{"agent"},
				},
				Allow: true,
			},
			{
				Target: delegationpolicystore.Target{
					IDs:   []string{"github", "jira"},
					Types: []string{"connector"},
				},
				Allow: true,
			},
		},
		ContentPolicy: delegationpolicystore.ContentPolicy{
			MaxInputSize:        131072,
			MaxExportedFileSize: 10485760,
		},
	}
}

func TestCreateAndGet(t *testing.T) {
	ctx := context.Background()
	store := delegationpolicystore.NewMemory()
	if err := store.Create(ctx, samplePolicy("orchestrator-policy")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(ctx, testTenantID, "orchestrator-policy")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Rules) != 2 {
		t.Fatalf("got %d rules, want 2", len(got.Rules))
	}
	if got.Rules[0].Target.MatchLabels["team"] != "platform" || !got.Rules[0].Allow {
		t.Errorf("rule 0 = %+v, want team=platform allow=true", got.Rules[0])
	}
	if got.ContentPolicy.MaxInputSize != 131072 {
		t.Errorf("MaxInputSize = %d, want 131072", got.ContentPolicy.MaxInputSize)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("Create must stamp CreatedAt and UpdatedAt")
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	ctx := context.Background()
	store := delegationpolicystore.NewMemory()
	if err := store.Create(ctx, samplePolicy("p1")); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if err := store.Create(ctx, samplePolicy("p1")); !errors.Is(err, delegationpolicystore.ErrAlreadyExists) {
		t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
	}
}

func TestCreateRejectsInvalidName(t *testing.T) {
	ctx := context.Background()
	store := delegationpolicystore.NewMemory()
	for _, name := range []string{"", "With Space", "UPPER", "-leading"} {
		p := samplePolicy(name)
		if err := store.Create(ctx, p); err == nil {
			t.Errorf("Create with name %q: expected a validation error", name)
		}
	}
}

func TestApplyDefaults(t *testing.T) {
	// A zero content policy receives the §8.3 defaults.
	var p delegationpolicystore.DelegationPolicy
	delegationpolicystore.ApplyDefaults(&p)
	if p.ContentPolicy.MaxInputSize != delegationpolicystore.DefaultMaxInputSize {
		t.Errorf("MaxInputSize = %d, want default %d",
			p.ContentPolicy.MaxInputSize, delegationpolicystore.DefaultMaxInputSize)
	}
	if p.ContentPolicy.MaxExportedFileSize != delegationpolicystore.DefaultMaxExportedFileSize {
		t.Errorf("MaxExportedFileSize = %d, want default %d",
			p.ContentPolicy.MaxExportedFileSize, delegationpolicystore.DefaultMaxExportedFileSize)
	}
	// Explicit values are preserved.
	explicit := delegationpolicystore.DelegationPolicy{
		ContentPolicy: delegationpolicystore.ContentPolicy{MaxInputSize: 4096, MaxExportedFileSize: 8192},
	}
	delegationpolicystore.ApplyDefaults(&explicit)
	if explicit.ContentPolicy.MaxInputSize != 4096 || explicit.ContentPolicy.MaxExportedFileSize != 8192 {
		t.Errorf("ApplyDefaults overwrote explicit values: %+v", explicit.ContentPolicy)
	}
}

func TestCreateRejectsScanWithoutInterceptor(t *testing.T) {
	ctx := context.Background()
	store := delegationpolicystore.NewMemory()
	p := samplePolicy("scan-policy")
	p.ContentPolicy.ScanExportedFiles = true // no InterceptorRef
	if err := store.Create(ctx, p); !errors.Is(err, delegationpolicystore.ErrScanRequiresInterceptor) {
		t.Errorf("scanExportedFiles without interceptorRef: got %v, want ErrScanRequiresInterceptor", err)
	}
}

func TestCreateAllowsScanWithInterceptor(t *testing.T) {
	ctx := context.Background()
	store := delegationpolicystore.NewMemory()
	p := samplePolicy("scan-policy")
	p.ContentPolicy.ScanExportedFiles = true
	p.ContentPolicy.InterceptorRef = "pii-scanner"
	if err := store.Create(ctx, p); err != nil {
		t.Errorf("scanExportedFiles with interceptorRef: got %v, want nil", err)
	}
}

func TestCreateRejectsNegativeContentSizes(t *testing.T) {
	ctx := context.Background()
	store := delegationpolicystore.NewMemory()
	p := samplePolicy("neg")
	p.ContentPolicy.MaxInputSize = -1
	if err := store.Create(ctx, p); err == nil {
		t.Error("negative maxInputSize: expected a validation error")
	}
}

func TestUpdateMutatesPolicy(t *testing.T) {
	ctx := context.Background()
	store := delegationpolicystore.NewMemory()
	if err := store.Create(ctx, samplePolicy("p1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	updated, err := store.Update(ctx, testTenantID, "p1", func(p *delegationpolicystore.DelegationPolicy) error {
		p.AllowSelfRecursion = true
		p.ContentPolicy.MaxInputSize = 4096
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !updated.AllowSelfRecursion || updated.ContentPolicy.MaxInputSize != 4096 {
		t.Errorf("updated = %+v, want allowSelfRecursion=true maxInputSize=4096", updated)
	}
	if !updated.UpdatedAt.After(updated.CreatedAt) {
		t.Error("Update must advance UpdatedAt past CreatedAt")
	}
}

func TestUpdateRejectsScanInvariantViolation(t *testing.T) {
	ctx := context.Background()
	store := delegationpolicystore.NewMemory()
	if err := store.Create(ctx, samplePolicy("p1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Turning on scanExportedFiles without an interceptor must be
	// rejected by the same invariant the create path enforces.
	_, err := store.Update(ctx, testTenantID, "p1", func(p *delegationpolicystore.DelegationPolicy) error {
		p.ContentPolicy.ScanExportedFiles = true
		return nil
	})
	if !errors.Is(err, delegationpolicystore.ErrScanRequiresInterceptor) {
		t.Errorf("Update violating the scan invariant: got %v, want ErrScanRequiresInterceptor", err)
	}
}

func TestUpdateMissing(t *testing.T) {
	store := delegationpolicystore.NewMemory()
	_, err := store.Update(context.Background(), testTenantID, "ghost", func(*delegationpolicystore.DelegationPolicy) error {
		return nil
	})
	if !errors.Is(err, delegationpolicystore.ErrNotFound) {
		t.Errorf("Update missing policy: got %v, want ErrNotFound", err)
	}
}

func TestSoftDeleteHidesFromList(t *testing.T) {
	ctx := context.Background()
	store := delegationpolicystore.NewMemory()
	if err := store.Create(ctx, samplePolicy("p1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.SoftDelete(ctx, testTenantID, "p1", time.Now().UTC()); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	active, _ := store.List(ctx, testTenantID, delegationpolicystore.ListFilter{})
	if len(active) != 0 {
		t.Errorf("List default: got %d policies, want 0 (soft-deleted excluded)", len(active))
	}
	all, _ := store.List(ctx, testTenantID, delegationpolicystore.ListFilter{IncludeDeleted: true})
	if len(all) != 1 {
		t.Errorf("List includeDeleted: got %d policies, want 1", len(all))
	}
	// Get still returns the soft-deleted row.
	got, err := store.Get(ctx, testTenantID, "p1")
	if err != nil {
		t.Fatalf("Get after SoftDelete: %v", err)
	}
	if got.IsActive() {
		t.Error("a soft-deleted policy must report IsActive() == false")
	}
}

func TestSoftDeleteMissing(t *testing.T) {
	store := delegationpolicystore.NewMemory()
	if err := store.SoftDelete(context.Background(), testTenantID, "ghost", time.Now()); !errors.Is(err, delegationpolicystore.ErrNotFound) {
		t.Errorf("SoftDelete missing: got %v, want ErrNotFound", err)
	}
}

func TestStoredPolicyIsIsolatedFromCallerMutation(t *testing.T) {
	ctx := context.Background()
	store := delegationpolicystore.NewMemory()
	if err := store.Create(ctx, samplePolicy("p1")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, _ := store.Get(ctx, testTenantID, "p1")
	// Mutating the returned copy must not reach the stored policy.
	got.Rules[0].Target.MatchLabels["team"] = "tampered"
	got.Rules[0].Allow = false

	fresh, _ := store.Get(ctx, testTenantID, "p1")
	if fresh.Rules[0].Target.MatchLabels["team"] != "platform" || !fresh.Rules[0].Allow {
		t.Errorf("stored policy was mutated through a returned copy: %+v", fresh.Rules[0])
	}
}

// spec: §4.2 line 172 — two tenants may register a policy with the
// same logical name without colliding, and one tenant cannot read
// another tenant's policies through the per-tenant Get/List path.
func TestTenantIsolationAndAllSentinel(t *testing.T) {
	ctx := context.Background()
	store := delegationpolicystore.NewMemory()

	a := samplePolicy("orchestrator")
	a.TenantID = "acme"
	b := samplePolicy("orchestrator")
	b.TenantID = "globex"
	if err := store.Create(ctx, a); err != nil {
		t.Fatalf("Create acme: %v", err)
	}
	if err := store.Create(ctx, b); err != nil {
		t.Fatalf("Create globex: %v", err)
	}

	// acme sees only its row; globex too.
	rowsA, _ := store.List(ctx, "acme", delegationpolicystore.ListFilter{})
	if len(rowsA) != 1 || rowsA[0].TenantID != "acme" {
		t.Errorf("acme list: %+v", rowsA)
	}
	rowsB, _ := store.List(ctx, "globex", delegationpolicystore.ListFilter{})
	if len(rowsB) != 1 || rowsB[0].TenantID != "globex" {
		t.Errorf("globex list: %+v", rowsB)
	}

	// AllTenantsSentinel returns both rows.
	all, _ := store.List(ctx, delegationpolicystore.AllTenantsSentinel, delegationpolicystore.ListFilter{})
	if len(all) != 2 {
		t.Errorf("__all__ list: got %d, want 2", len(all))
	}
}

// spec: §4.2 line 172 — Create rejects an empty TenantID.
func TestCreateRejectsEmptyTenant(t *testing.T) {
	store := delegationpolicystore.NewMemory()
	p := samplePolicy("orchestrator")
	p.TenantID = ""
	if err := store.Create(context.Background(), p); err == nil {
		t.Error("Create with empty TenantID should be rejected")
	}
}

// spec: §4.2 line 172 — writes reject the AllTenantsSentinel value
// (the sentinel is reads-only).
func TestWritesRejectSentinel(t *testing.T) {
	ctx := context.Background()
	store := delegationpolicystore.NewMemory()
	_ = store.Create(ctx, samplePolicy("p1"))
	if _, err := store.Update(ctx, delegationpolicystore.AllTenantsSentinel, "p1",
		func(p *delegationpolicystore.DelegationPolicy) error { return nil }); err == nil {
		t.Error("Update with sentinel must be rejected")
	}
	if err := store.SoftDelete(ctx, delegationpolicystore.AllTenantsSentinel, "p1", time.Now()); err == nil {
		t.Error("SoftDelete with sentinel must be rejected")
	}
}
