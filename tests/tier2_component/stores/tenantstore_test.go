//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the platform tenant registry, exercising the
// Postgres-backed pkg/gateway/tenantstore/pgstore against a real
// container. Covers CRUD, id validation, soft-delete (and its
// idempotency), the IncludeDeleted list filter, and the
// auth.TenantRegistry IsRegistered probe.
package stores_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	tenantpg "github.com/lennylabs/lenny/pkg/gateway/tenantstore/pgstore"
)

func tenantID(t *testing.T) string {
	t.Helper()
	return "acme-" + newUUID(t)[:8]
}

// jsonRawEqual reports whether two JSON documents are semantically
// equal, ignoring the whitespace and key-order differences a jsonb
// round-trip introduces. Unparseable input compares false.
func jsonRawEqual(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	ab, _ := json.Marshal(av)
	bb, _ := json.Marshal(bv)
	return string(ab) == string(bb)
}

// spec: 15.1, 10.2
// diagnosis: the Postgres-backed tenant registry in
// pkg/gateway/tenantstore/pgstore did not behave as specified. Create
// and Get must round-trip a tenant, id validation must reject
// duplicates and malformed ids, Update must re-apply and advance
// updated_at, the soft-delete lifecycle must honor the IncludeDeleted
// list filter, and the auth.TenantRegistry IsRegistered probe must
// track the tenant lifecycle.
func TestTenantStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := tenantpg.New(pg.Pool)
	ctx := context.Background()

	t.Run("create and get round-trip", func(t *testing.T) {
		want := tenantstore.Tenant{
			ID:                  tenantID(t),
			DisplayName:         "Acme Corporation",
			ComplianceProfile:   "soc2",
			DataResidencyRegion: "us-east-1",
			// spec: §12.9 line 1048 / migration 0103 — workspace_tier is a
			// closed enum {'', 'T3', 'T4'}; T3 is the lowest classified tier.
			WorkspaceTier:         "T3",
			MaxConcurrentSessions: 25,
			StorageQuotaBytes:     1 << 30,
			// spec: §11.2 — the token budget and per-tenant reset period
			// persist through the Postgres tenant store (migration 0101).
			TokenQuotaPerWindow: 1_000_000,
			QuotaResetPeriod:    "monthly",
		}
		if err := store.Create(ctx, want); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, want.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.DisplayName != want.DisplayName || got.ComplianceProfile != want.ComplianceProfile ||
			got.DataResidencyRegion != want.DataResidencyRegion || got.WorkspaceTier != want.WorkspaceTier ||
			got.MaxConcurrentSessions != want.MaxConcurrentSessions ||
			got.StorageQuotaBytes != want.StorageQuotaBytes ||
			got.TokenQuotaPerWindow != want.TokenQuotaPerWindow ||
			got.QuotaResetPeriod != want.QuotaResetPeriod {
			t.Errorf("field mismatch:\n got %+v\nwant %+v", got, want)
		}
		if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
			t.Error("Create did not default the audit timestamps")
		}
		if !got.IsActive() {
			t.Error("freshly created tenant reports inactive")
		}
	})

	// spec: §10.6 line 665 — the rbac_config jsonb column persists the
	// identityProvider, tokenPolicy, capabilities, and mcpAnnotationMapping
	// fields through Create and Update. F-10.6.6.
	t.Run("rbac config round-trip", func(t *testing.T) {
		id := tenantID(t)
		want := tenantstore.RBACConfig{
			IdentityProvider:     tenantstore.IdentityProvider{Type: "oidc", IntrospectionEnabled: true},
			TokenPolicy:          json.RawMessage(`{"accessTtlSeconds":900}`),
			Capabilities:         []string{"search", "summarize"},
			MCPAnnotationMapping: map[string][]string{"dangerous_tool": {"read", "write"}},
		}
		if err := store.Create(ctx, tenantstore.Tenant{ID: id, RBACConfig: want}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.RBACConfig.IdentityProvider != want.IdentityProvider {
			t.Errorf("identityProvider: got %+v, want %+v", got.RBACConfig.IdentityProvider, want.IdentityProvider)
		}
		// tokenPolicy persists in the rbac_config jsonb column, so the
		// store returns Postgres's canonical serialization (a space after
		// each colon) rather than the input bytes. Compare semantically so
		// the assertion pins the persisted value, not jsonb whitespace.
		if !jsonRawEqual(got.RBACConfig.TokenPolicy, want.TokenPolicy) {
			t.Errorf("tokenPolicy: got %s, want %s", got.RBACConfig.TokenPolicy, want.TokenPolicy)
		}
		if strings.Join(got.RBACConfig.Capabilities, ",") != "search,summarize" {
			t.Errorf("capabilities: got %+v", got.RBACConfig.Capabilities)
		}
		if caps := got.RBACConfig.MCPAnnotationMapping["dangerous_tool"]; strings.Join(caps, ",") != "read,write" {
			t.Errorf("mcpAnnotationMapping: got %+v", got.RBACConfig.MCPAnnotationMapping)
		}

		// Update replaces the config.
		if _, err := store.Update(ctx, id, func(tn *tenantstore.Tenant) error {
			tn.RBACConfig.IdentityProvider.IntrospectionEnabled = false
			tn.RBACConfig.Capabilities = []string{"search"}
			return nil
		}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		reloaded, _ := store.Get(ctx, id)
		if reloaded.RBACConfig.IdentityProvider.IntrospectionEnabled {
			t.Error("Update did not persist the cleared introspectionEnabled")
		}
		if len(reloaded.RBACConfig.Capabilities) != 1 {
			t.Errorf("Update did not persist capabilities: %+v", reloaded.RBACConfig.Capabilities)
		}
	})

	// spec: §15.1 line 823,824 — the PUT
	// /v1/admin/tenants/{id}/elicitation-content-integrity records mode,
	// justification, changedAt, and changedBy on the tenant, and the GET
	// returns them. Migration 0172 adds the three provenance columns; the
	// pgstore Create/Update/Get paths must round-trip them. ADM-3.
	// diagnosis: the elicitation_content_integrity_justification,
	// _changed_at, and _changed_by columns the §15.1 GET body reads are
	// not persisted by pkg/gateway/tenantstore/pgstore — a tenant-admin
	// GET after a PUT would return empty provenance.
	t.Run("elicitation integrity provenance round-trip", func(t *testing.T) {
		id := tenantID(t)
		changedAt := time.Now().UTC().Truncate(time.Microsecond)
		want := tenantstore.Tenant{
			ID:                                       id,
			ElicitationContentIntegrity:              "detect-only",
			ElicitationContentIntegrityJustification: "staging tenant, integrity tooling offline",
			ElicitationContentIntegrityChangedAt:     changedAt,
			ElicitationContentIntegrityChangedBy:     "alice@acme.com",
		}
		if err := store.Create(ctx, want); err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := store.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.ElicitationContentIntegrity != "detect-only" {
			t.Errorf("stored mode = %q, want detect-only", got.ElicitationContentIntegrity)
		}
		if got.ElicitationContentIntegrityJustification != want.ElicitationContentIntegrityJustification {
			t.Errorf("justification = %q, want %q",
				got.ElicitationContentIntegrityJustification, want.ElicitationContentIntegrityJustification)
		}
		if !got.ElicitationContentIntegrityChangedAt.Equal(changedAt) {
			t.Errorf("changedAt = %v, want %v", got.ElicitationContentIntegrityChangedAt, changedAt)
		}
		if got.ElicitationContentIntegrityChangedBy != "alice@acme.com" {
			t.Errorf("changedBy = %q, want alice@acme.com", got.ElicitationContentIntegrityChangedBy)
		}

		// Update must replace the provenance alongside the mode.
		next := time.Now().UTC().Truncate(time.Microsecond).Add(time.Second)
		if _, err := store.Update(ctx, id, func(tn *tenantstore.Tenant) error {
			tn.ElicitationContentIntegrity = "off"
			tn.ElicitationContentIntegrityJustification = "decommissioned"
			tn.ElicitationContentIntegrityChangedAt = next
			tn.ElicitationContentIntegrityChangedBy = "bob@acme.com"
			return nil
		}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		reloaded, _ := store.Get(ctx, id)
		if reloaded.ElicitationContentIntegrity != "off" ||
			reloaded.ElicitationContentIntegrityJustification != "decommissioned" ||
			!reloaded.ElicitationContentIntegrityChangedAt.Equal(next) ||
			reloaded.ElicitationContentIntegrityChangedBy != "bob@acme.com" {
			t.Errorf("Update did not persist the provenance: %+v", reloaded)
		}

		// A tenant that never set a mode reads zero provenance (the
		// "never set" semantics the §15.1 GET body distinguishes).
		blankID := tenantID(t)
		if err := store.Create(ctx, tenantstore.Tenant{ID: blankID}); err != nil {
			t.Fatalf("Create blank: %v", err)
		}
		blank, _ := store.Get(ctx, blankID)
		if blank.ElicitationContentIntegrityJustification != "" ||
			!blank.ElicitationContentIntegrityChangedAt.IsZero() ||
			blank.ElicitationContentIntegrityChangedBy != "" {
			t.Errorf("unset tenant carries provenance: %+v", blank)
		}
	})

	t.Run("duplicate and invalid ids are rejected", func(t *testing.T) {
		id := tenantID(t)
		if err := store.Create(ctx, tenantstore.Tenant{ID: id}); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		if err := store.Create(ctx, tenantstore.Tenant{ID: id}); !errors.Is(err, tenantstore.ErrAlreadyExists) {
			t.Errorf("duplicate Create: got %v, want ErrAlreadyExists", err)
		}
		if err := store.Create(ctx, tenantstore.Tenant{ID: "bad id with spaces"}); err == nil {
			t.Error("Create with an invalid tenant id should be rejected")
		}
	})

	t.Run("get missing returns ErrNotFound", func(t *testing.T) {
		if _, err := store.Get(ctx, tenantID(t)); !errors.Is(err, tenantstore.ErrNotFound) {
			t.Errorf("Get missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("update mutates and advances updated_at", func(t *testing.T) {
		id := tenantID(t)
		if err := store.Create(ctx, tenantstore.Tenant{ID: id, DisplayName: "Before"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		before, _ := store.Get(ctx, id)
		updated, err := store.Update(ctx, id, func(tn *tenantstore.Tenant) error {
			tn.DisplayName = "After"
			tn.ComplianceProfile = "hipaa"
			tn.MaxConcurrentSessions = 50
			tn.StorageQuotaBytes = 2 << 30
			return nil
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if updated.DisplayName != "After" || updated.ComplianceProfile != "hipaa" ||
			updated.MaxConcurrentSessions != 50 || updated.StorageQuotaBytes != 2<<30 {
			t.Errorf("Update result not applied: %+v", updated)
		}
		if !updated.UpdatedAt.After(before.UpdatedAt) {
			t.Errorf("UpdatedAt did not advance: before=%v after=%v", before.UpdatedAt, updated.UpdatedAt)
		}
		got, _ := store.Get(ctx, id)
		if got.DisplayName != "After" {
			t.Errorf("persisted DisplayName = %q, want After", got.DisplayName)
		}
		if _, err := store.Update(ctx, tenantID(t), func(*tenantstore.Tenant) error {
			return nil
		}); !errors.Is(err, tenantstore.ErrNotFound) {
			t.Errorf("Update missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("soft delete is idempotent and reflected in list", func(t *testing.T) {
		marker := newUUID(t)[:8]
		base := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
		ids := []string{marker + "-a", marker + "-b", marker + "-c"}
		for i, id := range ids {
			if err := store.Create(ctx, tenantstore.Tenant{
				ID: id, CreatedAt: base.Add(time.Duration(i) * time.Minute),
			}); err != nil {
				t.Fatalf("Create %s: %v", id, err)
			}
		}
		countMarked := func(filter tenantstore.ListFilter) int {
			t.Helper()
			all, err := store.List(ctx, filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			n := 0
			for _, tn := range all {
				if strings.HasPrefix(tn.ID, marker) {
					n++
				}
			}
			return n
		}
		if n := countMarked(tenantstore.ListFilter{}); n != 3 {
			t.Fatalf("List before delete: %d marked tenants, want 3", n)
		}

		at := time.Now().UTC().Truncate(time.Microsecond)
		if err := store.SoftDelete(ctx, ids[0], at); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		// Idempotent: a second soft-delete is a no-op success.
		if err := store.SoftDelete(ctx, ids[0], at.Add(time.Minute)); err != nil {
			t.Errorf("idempotent SoftDelete: %v", err)
		}
		if err := store.SoftDelete(ctx, tenantID(t), at); !errors.Is(err, tenantstore.ErrNotFound) {
			t.Errorf("SoftDelete missing: got %v, want ErrNotFound", err)
		}

		deleted, err := store.Get(ctx, ids[0])
		if err != nil {
			t.Fatalf("Get soft-deleted: %v", err)
		}
		if deleted.IsActive() || deleted.DeletedAt.IsZero() {
			t.Errorf("soft-deleted tenant still active: %+v", deleted)
		}
		if n := countMarked(tenantstore.ListFilter{}); n != 2 {
			t.Errorf("List default after delete: %d marked, want 2", n)
		}
		if n := countMarked(tenantstore.ListFilter{IncludeDeleted: true}); n != 3 {
			t.Errorf("List IncludeDeleted after delete: %d marked, want 3", n)
		}
	})

	t.Run("is registered tracks tenant lifecycle", func(t *testing.T) {
		id := tenantID(t)
		if reg, err := store.IsRegistered(id); err != nil || reg {
			t.Errorf("IsRegistered before create: got %v,%v want false,nil", reg, err)
		}
		if err := store.Create(ctx, tenantstore.Tenant{ID: id}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if reg, err := store.IsRegistered(id); err != nil || !reg {
			t.Errorf("IsRegistered after create: got %v,%v want true,nil", reg, err)
		}
		if err := store.SoftDelete(ctx, id, time.Now().UTC()); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		if reg, err := store.IsRegistered(id); err != nil || reg {
			t.Errorf("IsRegistered after soft-delete: got %v,%v want false,nil", reg, err)
		}
	})
}
