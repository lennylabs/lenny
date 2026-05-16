// SPDX-License-Identifier: MIT

package erasurejob_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/erasurejob"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §12.8 tenant-controlled billing erasure.

func seedTenant(t *testing.T, store *tenantstore.Memory, tn tenantstore.Tenant) {
	t.Helper()
	if err := store.Create(context.Background(), tn); err != nil {
		t.Fatalf("seed tenant %q: %v", tn.ID, err)
	}
}

func seedBilling(t *testing.T, store *billingstore.Memory, tenant, user string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := store.Append(context.Background(), billingstore.Event{
			TenantID:    tenant,
			UserID:      user,
			SessionID:   "sess",
			EventType:   billingstore.EventSessionCreated,
			TokensInput: 5,
		}); err != nil {
			t.Fatalf("seed billing event: %v", err)
		}
	}
}

func TestBillingEraserPseudonymizesEvents(t *testing.T) {
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	seedTenant(t, tenants, tenantstore.Tenant{ID: "acme"})
	billing := billingstore.NewMemory()
	seedBilling(t, billing, "acme", "alice@acme", 3)
	seedBilling(t, billing, "acme", "bob@acme", 1)

	eraser := erasurejob.NewBillingEraser(billing, tenants)
	outcome, err := eraser.Pseudonymize(ctx, "acme", "alice@acme")
	if err != nil {
		t.Fatalf("Pseudonymize: %v", err)
	}
	if outcome.Disposition != "pseudonymized" || outcome.Pseudonymized != 3 {
		t.Errorf("outcome = %+v, want disposition=pseudonymized pseudonymized=3", outcome)
	}

	if cnt, _ := billing.CountUser(ctx, "acme", "alice@acme"); cnt != 0 {
		t.Errorf("%d events still carry alice's original id, want 0", cnt)
	}
	if cnt, _ := billing.CountUser(ctx, "acme", "bob@acme"); cnt != 1 {
		t.Errorf("bob's events were affected: CountUser=%d, want 1", cnt)
	}
	// §12.8: the salt is destroyed once pseudonymization completes.
	tn, _ := tenants.Get(ctx, "acme")
	if len(tn.ErasureSalt) != 0 {
		t.Error("the erasure salt was not destroyed after pseudonymization")
	}
}

func TestBillingEraserExemptTenantSkips(t *testing.T) {
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	seedTenant(t, tenants, tenantstore.Tenant{
		ID: "acme", BillingErasurePolicy: tenantstore.BillingErasureExempt,
	})
	billing := billingstore.NewMemory()
	seedBilling(t, billing, "acme", "alice@acme", 2)

	eraser := erasurejob.NewBillingEraser(billing, tenants)
	outcome, err := eraser.Pseudonymize(ctx, "acme", "alice@acme")
	if err != nil {
		t.Fatalf("Pseudonymize: %v", err)
	}
	if outcome.Disposition != "exempt" || outcome.Pseudonymized != 0 {
		t.Errorf("outcome = %+v, want disposition=exempt pseudonymized=0", outcome)
	}
	// §12.8 Article 17(3)(b): an exempt tenant retains the original id.
	if cnt, _ := billing.CountUser(ctx, "acme", "alice@acme"); cnt != 2 {
		t.Errorf("exempt tenant's events were rewritten: CountUser=%d, want 2", cnt)
	}
}

func TestBillingEraserReusesPersistedSalt(t *testing.T) {
	ctx := context.Background()
	// A salt persisted by an interrupted attempt must be reused so the
	// resumed run derives the same pseudonyms.
	knownSalt := []byte("0123456789abcdef0123456789abcdef")
	tenants := tenantstore.NewMemory()
	seedTenant(t, tenants, tenantstore.Tenant{ID: "acme", ErasureSalt: knownSalt})
	billing := billingstore.NewMemory()
	seedBilling(t, billing, "acme", "alice@acme", 2)

	eraser := erasurejob.NewBillingEraser(billing, tenants)
	if _, err := eraser.Pseudonymize(ctx, "acme", "alice@acme"); err != nil {
		t.Fatalf("Pseudonymize: %v", err)
	}
	want := billingstore.Pseudonymize("alice@acme", knownSalt)
	events, _ := billing.Since(ctx, "acme", 0, 0)
	for _, e := range events {
		if e.UserID != want {
			t.Errorf("event %d UserID=%q, want the known-salt pseudonym %q", e.SequenceNumber, e.UserID, want)
		}
	}
}

func TestBillingEraserVerifyPassesAfterPseudonymize(t *testing.T) {
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	seedTenant(t, tenants, tenantstore.Tenant{ID: "acme"})
	billing := billingstore.NewMemory()
	seedBilling(t, billing, "acme", "alice@acme", 3)

	eraser := erasurejob.NewBillingEraser(billing, tenants)
	if _, err := eraser.Pseudonymize(ctx, "acme", "alice@acme"); err != nil {
		t.Fatalf("Pseudonymize: %v", err)
	}
	ok, err := eraser.Verify(ctx, "acme", "alice@acme")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Error("Verify should pass: the salt is destroyed and no event keeps the original user id")
	}
}

func TestBillingEraserVerifyFailsWhenSaltSurvives(t *testing.T) {
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	// A tenant whose erasure salt was never destroyed (a crashed job).
	seedTenant(t, tenants, tenantstore.Tenant{
		ID: "acme", ErasureSalt: []byte("leftover-salt-leftover-salt-1234"),
	})
	eraser := erasurejob.NewBillingEraser(billingstore.NewMemory(), tenants)

	ok, err := eraser.Verify(ctx, "acme", "alice@acme")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok {
		t.Error("Verify must fail while the erasure salt survives in the tenant registry")
	}
}

func TestBillingEraserVerifyFailsWhenEventsRemain(t *testing.T) {
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	seedTenant(t, tenants, tenantstore.Tenant{ID: "acme"})
	billing := billingstore.NewMemory()
	seedBilling(t, billing, "acme", "alice@acme", 1) // never pseudonymized

	eraser := erasurejob.NewBillingEraser(billing, tenants)
	ok, err := eraser.Verify(ctx, "acme", "alice@acme")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok {
		t.Error("Verify must fail while a billing event still carries the original user id")
	}
}

func TestBillingEraserPseudonymizeUnknownTenant(t *testing.T) {
	eraser := erasurejob.NewBillingEraser(billingstore.NewMemory(), tenantstore.NewMemory())
	if _, err := eraser.Pseudonymize(context.Background(), "ghost", "alice@acme"); err == nil {
		t.Error("Pseudonymize must error for a tenant not in the registry")
	}
}
