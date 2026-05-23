//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §8.3 DelegationPolicy registry, exercising the
// Postgres-backed pkg/gateway/delegationpolicystore/pgstore against a
// real container with the production migrations applied. Covers CRUD,
// the jsonb policy-body round-trip, the sentinel errors, the §8.3
// structural validation, the SELECT ... FOR UPDATE mutate path,
// strict-monotonic UpdatedAt, the IncludeDeleted list filter, the
// soft-delete lifecycle, and the tag-matched Evaluate decision.
package stores_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	delegationpolicypg "github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore/pgstore"
)

// policyName returns a fresh unique §8.3 policy name. The name pattern
// is ^[a-z0-9][a-z0-9_-]{0,127}$, which a UUID hex prefix satisfies.
func policyName(t *testing.T) string {
	t.Helper()
	return "dp-" + newUUID(t)[:8]
}

// sampleDelegationPolicy builds a §8.3 policy with a two-rule
// tag-matched allow set and an explicit content policy, so the jsonb
// body has nested Rules, Target maps and slices, and a ContentPolicy
// to round-trip.
func sampleDelegationPolicy(tenantID, name string) delegationpolicystore.DelegationPolicy {
	return delegationpolicystore.DelegationPolicy{
		TenantID: tenantID,
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

// spec: 8.3
// diagnosis: the Postgres-backed DelegationPolicy registry in
// pkg/gateway/delegationpolicystore/pgstore did not behave as
// specified — the Create/Get/Update/SoftDelete lifecycle, the nested
// jsonb body round-trip, §8.3 validation, the sentinel errors, or the
// loaded policy's tag-matched allow/deny Evaluate.
func TestDelegationPolicyStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := delegationpolicypg.New(pg.Pool)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)

	t.Run("create and get round-trip preserves the nested jsonb body", func(t *testing.T) {
		want := sampleDelegationPolicy(tenant, policyName(t))
		want.ContentPolicy.InterceptorRef = "pii-scanner"
		want.ContentPolicy.ScanExportedFiles = true
		want.AllowSelfRecursion = true
		if err := store.Create(ctx, want); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, tenant, want.Name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.TenantID != tenant {
			t.Errorf("TenantID round-trip: got %q, want %q", got.TenantID, tenant)
		}
		if got.Name != want.Name {
			t.Errorf("Name = %q, want %q", got.Name, want.Name)
		}
		if got.AllowSelfRecursion != want.AllowSelfRecursion {
			t.Errorf("AllowSelfRecursion = %v, want %v", got.AllowSelfRecursion, want.AllowSelfRecursion)
		}
		if got.ContentPolicy != want.ContentPolicy {
			t.Errorf("ContentPolicy mismatch:\n got %+v\nwant %+v", got.ContentPolicy, want.ContentPolicy)
		}
		if len(got.Rules) != len(want.Rules) {
			t.Fatalf("got %d rules, want %d", len(got.Rules), len(want.Rules))
		}
		if !maps.Equal(got.Rules[0].Target.MatchLabels, want.Rules[0].Target.MatchLabels) {
			t.Errorf("rule 0 MatchLabels: got %v want %v",
				got.Rules[0].Target.MatchLabels, want.Rules[0].Target.MatchLabels)
		}
		if !slices.Equal(got.Rules[0].Target.Types, want.Rules[0].Target.Types) ||
			got.Rules[0].Allow != want.Rules[0].Allow {
			t.Errorf("rule 0 mismatch:\n got %+v\nwant %+v", got.Rules[0], want.Rules[0])
		}
		if !slices.Equal(got.Rules[1].Target.IDs, want.Rules[1].Target.IDs) ||
			!slices.Equal(got.Rules[1].Target.Types, want.Rules[1].Target.Types) {
			t.Errorf("rule 1 mismatch:\n got %+v\nwant %+v", got.Rules[1], want.Rules[1])
		}
		if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
			t.Error("Create must stamp CreatedAt and UpdatedAt")
		}
		if !got.IsActive() {
			t.Error("freshly created policy reports inactive")
		}
	})

	t.Run("duplicate, invalid name, negative size, and scan-without-interceptor are rejected", func(t *testing.T) {
		p := sampleDelegationPolicy(tenant, policyName(t))
		if err := store.Create(ctx, p); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		if err := store.Create(ctx, p); !errors.Is(err, delegationpolicystore.ErrAlreadyExists) {
			t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
		}
		for _, name := range []string{"", "With Space", "UPPER", "-leading"} {
			if err := store.Create(ctx, sampleDelegationPolicy(tenant, name)); err == nil {
				t.Errorf("Create with name %q: expected a validation error", name)
			}
		}
		// §8.3: a negative content-policy size is rejected.
		neg := sampleDelegationPolicy(tenant, policyName(t))
		neg.ContentPolicy.MaxInputSize = -1
		if err := store.Create(ctx, neg); err == nil {
			t.Error("Create with a negative maxInputSize should be rejected")
		}
		// §8.3 rule 1: scanExportedFiles requires an interceptorRef.
		scan := sampleDelegationPolicy(tenant, policyName(t))
		scan.ContentPolicy.ScanExportedFiles = true // no InterceptorRef
		if err := store.Create(ctx, scan); !errors.Is(err, delegationpolicystore.ErrScanRequiresInterceptor) {
			t.Errorf("Create scanExportedFiles without interceptorRef: got %v, want ErrScanRequiresInterceptor", err)
		}
	})

	t.Run("get missing returns ErrNotFound", func(t *testing.T) {
		if _, err := store.Get(ctx, tenant, policyName(t)); !errors.Is(err, delegationpolicystore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("update mutates, advances updated_at, and re-validates", func(t *testing.T) {
		name := policyName(t)
		if err := store.Create(ctx, sampleDelegationPolicy(tenant, name)); err != nil {
			t.Fatalf("Create: %v", err)
		}
		before, _ := store.Get(ctx, tenant, name)
		updated, err := store.Update(ctx, tenant, name, func(p *delegationpolicystore.DelegationPolicy) error {
			p.AllowSelfRecursion = true
			p.ContentPolicy.MaxInputSize = 4096
			return nil
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if !updated.AllowSelfRecursion || updated.ContentPolicy.MaxInputSize != 4096 {
			t.Errorf("Update result not applied: %+v", updated)
		}
		if !updated.UpdatedAt.After(before.UpdatedAt) {
			t.Errorf("UpdatedAt did not advance: before=%v after=%v", before.UpdatedAt, updated.UpdatedAt)
		}
		persisted, _ := store.Get(ctx, tenant, name)
		if !persisted.AllowSelfRecursion || persisted.ContentPolicy.MaxInputSize != 4096 {
			t.Errorf("Update not persisted: %+v", persisted)
		}
		// §8.3 rule 1 is enforced on the update path too: turning on
		// scanExportedFiles without an interceptorRef must be rejected.
		if _, err := store.Update(ctx, tenant, name, func(p *delegationpolicystore.DelegationPolicy) error {
			p.ContentPolicy.ScanExportedFiles = true
			return nil
		}); !errors.Is(err, delegationpolicystore.ErrScanRequiresInterceptor) {
			t.Errorf("Update violating the scan invariant: got %v, want ErrScanRequiresInterceptor", err)
		}
		// A mutate error aborts the write.
		sentinel := errors.New("mutate boom")
		if _, err := store.Update(ctx, tenant, name, func(*delegationpolicystore.DelegationPolicy) error {
			return sentinel
		}); !errors.Is(err, sentinel) {
			t.Errorf("Update mutate error: got %v, want sentinel", err)
		}
		if _, err := store.Update(ctx, tenant, policyName(t), func(*delegationpolicystore.DelegationPolicy) error {
			return nil
		}); !errors.Is(err, delegationpolicystore.ErrNotFound) {
			t.Errorf("Update missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("list orders name-ascending and honors IncludeDeleted", func(t *testing.T) {
		marker := "dp-" + newUUID(t)[:8]
		names := []string{marker + "-a", marker + "-b", marker + "-c"}
		for _, name := range names {
			if err := store.Create(ctx, sampleDelegationPolicy(tenant, name)); err != nil {
				t.Fatalf("Create %s: %v", name, err)
			}
		}
		marked := func(filter delegationpolicystore.ListFilter) []delegationpolicystore.DelegationPolicy {
			t.Helper()
			all, err := store.List(ctx, tenant, filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			var out []delegationpolicystore.DelegationPolicy
			for _, p := range all {
				if len(p.Name) >= len(marker) && p.Name[:len(marker)] == marker {
					out = append(out, p)
				}
			}
			return out
		}
		all := marked(delegationpolicystore.ListFilter{})
		if len(all) != 3 || all[0].Name != names[0] || all[2].Name != names[2] {
			t.Fatalf("List: not name-ascending or wrong count: %d", len(all))
		}

		if err := store.SoftDelete(ctx, tenant, names[0], time.Now().UTC()); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		if err := store.SoftDelete(ctx, tenant, names[0], time.Now().UTC()); err != nil {
			t.Errorf("idempotent SoftDelete: %v", err)
		}
		if err := store.SoftDelete(ctx, tenant, policyName(t), time.Now().UTC()); !errors.Is(err, delegationpolicystore.ErrNotFound) {
			t.Errorf("SoftDelete missing: got %v, want ErrNotFound", err)
		}
		if n := len(marked(delegationpolicystore.ListFilter{})); n != 2 {
			t.Errorf("List default after delete: %d marked, want 2", n)
		}
		if n := len(marked(delegationpolicystore.ListFilter{IncludeDeleted: true})); n != 3 {
			t.Errorf("List IncludeDeleted after delete: %d marked, want 3", n)
		}
		// Get still returns the soft-deleted row, reporting inactive.
		deleted, err := store.Get(ctx, tenant, names[0])
		if err != nil || deleted.IsActive() {
			t.Errorf("Get soft-deleted: %+v err=%v; want inactive", deleted, err)
		}
	})

	t.Run("loaded policy reproduces the §8.3 tag-matched Evaluate decision", func(t *testing.T) {
		// §8.3: the rule set is an allow-list with deny-overrides. A
		// policy loaded from Postgres must yield the same allow/deny
		// decision as the in-memory implementation.
		name := policyName(t)
		p := delegationpolicystore.DelegationPolicy{
			TenantID: tenant,
			Name:     name,
			Rules: []delegationpolicystore.Rule{
				{Target: delegationpolicystore.Target{Types: []string{"agent"}}, Allow: true},
				{Target: delegationpolicystore.Target{IDs: []string{"untrusted"}}, Allow: false},
			},
		}
		if err := store.Create(ctx, p); err != nil {
			t.Fatalf("Create: %v", err)
		}
		loaded, err := store.Get(ctx, tenant, name)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		// An agent matched only by the allow rule is permitted.
		if !loaded.Evaluate(delegationpolicystore.Candidate{ID: "trusted", Type: "agent"}) {
			t.Error("a candidate matched by the allow rule must be permitted")
		}
		// A matching deny rule overrides the allow regardless of order.
		if loaded.Evaluate(delegationpolicystore.Candidate{ID: "untrusted", Type: "agent"}) {
			t.Error("a candidate matched by a deny rule must be denied")
		}
		// Default-deny: a candidate matched by no rule is denied.
		if loaded.Evaluate(delegationpolicystore.Candidate{ID: "github", Type: "connector"}) {
			t.Error("a candidate matched by no rule must be denied")
		}
	})
}
