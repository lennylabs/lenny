//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §11.2.1 billing event ledger, exercising the
// Postgres-backed pkg/gateway/billingstore/pgstore against a real
// container. Covers the append/round-trip path, the monotonic
// per-tenant sequence_number, the Since replay query with its limit,
// cross-tenant isolation, and the minimum-field validation.
package stores_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	billingpg "github.com/lennylabs/lenny/pkg/gateway/billingstore/pgstore"
)

// spec: 11.2.1
// diagnosis: the Postgres-backed billing event ledger in
// pkg/gateway/billingstore/pgstore did not behave as specified. Append
// must round-trip an event, the per-tenant sequence_number must be
// monotonic, Since must replay events after a sequence and respect its
// limit, the ledger must be isolated per tenant, and an event with no
// event type must be rejected.
func TestBillingStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := billingpg.New(pg.Router(t))
	ctx := context.Background()

	t.Run("append and round-trip an event", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		want := billingstore.Event{
			TenantID:     tenant,
			UserID:       "alice@acme.com",
			SessionID:    newUUID(t),
			ExperimentID: "exp-1",
			VariantID:    "variant-a",
			EventType:    billingstore.EventSessionCreated,
			TokensInput:  120,
			TokensOutput: 45,
		}
		committed, err := store.Append(ctx, want)
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		if committed.SequenceNumber != 1 {
			t.Errorf("first event sequence: got %d, want 1", committed.SequenceNumber)
		}
		got, err := store.Since(ctx, tenant, 0, 0)
		if err != nil {
			t.Fatalf("Since: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("Since: got %d events, want 1", len(got))
		}
		e := got[0]
		if e.UserID != want.UserID || e.SessionID != want.SessionID ||
			e.ExperimentID != want.ExperimentID || e.VariantID != want.VariantID ||
			e.EventType != want.EventType || e.TokensInput != want.TokensInput ||
			e.TokensOutput != want.TokensOutput {
			t.Errorf("event mismatch:\n got %+v\nwant %+v", e, want)
		}
		if e.SchemaVersion != 1 {
			t.Errorf("SchemaVersion: got %d, want 1", e.SchemaVersion)
		}
		if e.CreatedAt.IsZero() {
			t.Error("CreatedAt was not persisted")
		}
	})

	t.Run("sequence is monotonic and per-tenant", func(t *testing.T) {
		tenantA := freshTenant(t, ctx, pg)
		tenantB := freshTenant(t, ctx, pg)
		for want := uint64(1); want <= 3; want++ {
			got, err := store.Append(ctx, billingstore.Event{
				TenantID: tenantA, EventType: billingstore.EventSessionCreated,
			})
			if err != nil {
				t.Fatalf("Append: %v", err)
			}
			if got.SequenceNumber != want {
				t.Errorf("tenant A event: seq %d, want %d", got.SequenceNumber, want)
			}
		}
		first, err := store.Append(ctx, billingstore.Event{
			TenantID: tenantB, EventType: billingstore.EventSessionCreated,
		})
		if err != nil {
			t.Fatalf("Append tenant B: %v", err)
		}
		if first.SequenceNumber != 1 {
			t.Errorf("tenant B's first event: seq %d, want 1 (sequence is per-tenant)", first.SequenceNumber)
		}
	})

	t.Run("Since returns events after a sequence", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		for i := 0; i < 5; i++ {
			if _, err := store.Append(ctx, billingstore.Event{
				TenantID: tenant, EventType: billingstore.EventSessionCreated,
			}); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}
		got, err := store.Since(ctx, tenant, 2, 0)
		if err != nil {
			t.Fatalf("Since: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("Since(2): got %d events, want 3", len(got))
		}
		for i, e := range got {
			if e.SequenceNumber != uint64(i+3) {
				t.Errorf("event %d: seq %d, want %d", i, e.SequenceNumber, i+3)
			}
		}
	})

	t.Run("Since respects the limit", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		for i := 0; i < 6; i++ {
			if _, err := store.Append(ctx, billingstore.Event{
				TenantID: tenant, EventType: billingstore.EventSessionCreated,
			}); err != nil {
				t.Fatalf("Append %d: %v", i, err)
			}
		}
		got, err := store.Since(ctx, tenant, 0, 2)
		if err != nil {
			t.Fatalf("Since: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("Since with limit 2: got %d events, want 2", len(got))
		}
	})

	t.Run("the ledger is isolated per tenant", func(t *testing.T) {
		tenantA := freshTenant(t, ctx, pg)
		tenantB := freshTenant(t, ctx, pg)
		if _, err := store.Append(ctx, billingstore.Event{
			TenantID: tenantA, EventType: billingstore.EventSessionCreated,
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		got, err := store.Since(ctx, tenantB, 0, 0)
		if err != nil {
			t.Fatalf("Since: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("tenant B must not see tenant A's events, got %d", len(got))
		}
	})

	t.Run("append rejects an event with no event type", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		_, err := store.Append(ctx, billingstore.Event{TenantID: tenant})
		if !errors.Is(err, billingstore.ErrInvalidEvent) {
			t.Errorf("append without an event type: got %v, want ErrInvalidEvent", err)
		}
	})

	// spec: §11.2.1 — Event schema (all events): the event-type-specific
	// conditional fields round-trip through the conditional_fields JSONB
	// column, and an event that carries none reads back nil (the
	// null/absent contract). F-11.2.12.
	t.Run("conditional_fields round-trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		old := false
		neu := true
		want := billingstore.Event{
			TenantID:  tenant,
			SessionID: newUUID(t),
			EventType: billingstore.EventType("delegation_policy.export_scan_strengthened"),
			Conditional: &billingstore.Conditional{
				PolicyName:           "export-policy",
				TransitionTS:         "2026-06-02T00:00:00Z",
				OldScanExportedFiles: &old,
				NewScanExportedFiles: &neu,
				AffectedPolicyNames:  []string{"p1", "p2"},
			},
		}
		if _, err := store.Append(ctx, want); err != nil {
			t.Fatalf("Append with conditional: %v", err)
		}
		// A second event with no conditional block coexists with the first.
		if _, err := store.Append(ctx, billingstore.Event{
			TenantID: tenant, EventType: billingstore.EventSessionCreated,
		}); err != nil {
			t.Fatalf("Append plain: %v", err)
		}
		got, err := store.Since(ctx, tenant, 0, 0)
		if err != nil {
			t.Fatalf("Since: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("Since: got %d events, want 2", len(got))
		}
		c := got[0].Conditional
		if c == nil {
			t.Fatal("conditional_fields dropped on round-trip")
		}
		if c.PolicyName != "export-policy" || c.TransitionTS != "2026-06-02T00:00:00Z" {
			t.Errorf("conditional scalar mismatch: %+v", c)
		}
		if c.OldScanExportedFiles == nil || *c.OldScanExportedFiles != false ||
			c.NewScanExportedFiles == nil || *c.NewScanExportedFiles != true {
			t.Errorf("conditional boolean transitions mismatch: %+v", c)
		}
		if len(c.AffectedPolicyNames) != 2 {
			t.Errorf("affectedPolicyNames mismatch: %+v", c.AffectedPolicyNames)
		}
		if got[1].Conditional != nil {
			t.Errorf("plain event must read back nil Conditional, got %+v", got[1].Conditional)
		}
	})
}
