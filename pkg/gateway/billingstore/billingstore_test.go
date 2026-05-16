// SPDX-License-Identifier: MIT

package billingstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
)

// spec: §11.2.1 billing event stream.

func sessionCreated(tenant, session string) billingstore.Event {
	return billingstore.Event{
		TenantID:  tenant,
		UserID:    "alice@" + tenant,
		SessionID: session,
		EventType: billingstore.EventSessionCreated,
	}
}

func TestAppendAssignsPerTenantSequence(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()

	for want := uint64(1); want <= 3; want++ {
		got, err := store.Append(ctx, sessionCreated("acme", "sess"))
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		if got.SequenceNumber != want {
			t.Errorf("sequence number: got %d, want %d", got.SequenceNumber, want)
		}
	}
}

func TestAppendSequenceIsPerTenant(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()

	a, _ := store.Append(ctx, sessionCreated("acme", "s1"))
	b, _ := store.Append(ctx, sessionCreated("globex", "s2"))
	if a.SequenceNumber != 1 || b.SequenceNumber != 1 {
		t.Errorf("each tenant's sequence starts at 1: acme=%d globex=%d",
			a.SequenceNumber, b.SequenceNumber)
	}
	a2, _ := store.Append(ctx, sessionCreated("acme", "s3"))
	if a2.SequenceNumber != 2 {
		t.Errorf("acme's second event: got seq %d, want 2", a2.SequenceNumber)
	}
}

func TestAppendStampsSchemaVersionAndTimestamp(t *testing.T) {
	store := billingstore.NewMemory()
	got, err := store.Append(context.Background(), sessionCreated("acme", "s1"))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if got.SchemaVersion != 1 {
		t.Errorf("SchemaVersion: got %d, want 1", got.SchemaVersion)
	}
	if got.CreatedAt.IsZero() {
		t.Error("Append must stamp CreatedAt")
	}
}

func TestAppendRejectsInvalidEvent(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()

	if _, err := store.Append(ctx, billingstore.Event{EventType: billingstore.EventSessionCreated}); !errors.Is(err, billingstore.ErrInvalidEvent) {
		t.Errorf("missing tenant id: got %v, want ErrInvalidEvent", err)
	}
	if _, err := store.Append(ctx, billingstore.Event{TenantID: "acme"}); !errors.Is(err, billingstore.ErrInvalidEvent) {
		t.Errorf("missing event type: got %v, want ErrInvalidEvent", err)
	}
}

func TestSinceReturnsEventsAfterSequence(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := store.Append(ctx, sessionCreated("acme", "s")); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := store.Since(ctx, "acme", 2, 0)
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
}

func TestSinceRespectsLimit(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		store.Append(ctx, sessionCreated("acme", "s"))
	}

	got, err := store.Since(ctx, "acme", 0, 4)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("Since with limit 4: got %d events, want 4", len(got))
	}
	if got[0].SequenceNumber != 1 {
		t.Errorf("limited page should start at the lowest sequence, got %d", got[0].SequenceNumber)
	}
}

func TestSinceUnknownTenantIsEmpty(t *testing.T) {
	store := billingstore.NewMemory()
	got, err := store.Since(context.Background(), "ghost", 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("unknown tenant: got %d events, want 0", len(got))
	}
}

// spec: §12.8 tenant-controlled billing erasure.

// erasureSalt is a 32-byte (256-bit) fixed salt for the pseudonymize
// tests, standing in for the per-tenant crypto/rand salt.
var erasureSalt = []byte("0123456789abcdef0123456789abcdef")

// billed builds a billing event carrying a cost dimension so the
// pseudonymize tests can assert the cost columns survive.
func billed(tenant, user string, tokensIn uint64) billingstore.Event {
	return billingstore.Event{
		TenantID:    tenant,
		UserID:      user,
		SessionID:   "sess",
		EventType:   billingstore.EventSessionCreated,
		TokensInput: tokensIn,
	}
}

func TestPseudonymizeIsDeterministicAndSalted(t *testing.T) {
	got := billingstore.Pseudonymize("alice@acme", erasureSalt)
	if got != billingstore.Pseudonymize("alice@acme", erasureSalt) {
		t.Error("Pseudonymize must be deterministic for the same user id and salt")
	}
	if got == "alice@acme" {
		t.Error("the pseudonym must not equal the plaintext user id")
	}
	if len(got) != 64 {
		t.Errorf("a SHA-256 hex pseudonym is 64 chars, got %d", len(got))
	}
	if billingstore.Pseudonymize("alice@acme", []byte("ffffffffffffffffffffffffffffffff")) == got {
		t.Error("a different salt must produce a different pseudonym")
	}
	if billingstore.Pseudonymize("bob@acme", erasureSalt) == got {
		t.Error("a different user id must produce a different pseudonym")
	}
}

func TestPseudonymizeUserRewritesOnlyTheTargetUser(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := store.Append(ctx, billed("acme", "alice@acme", 10)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if _, err := store.Append(ctx, billed("acme", "bob@acme", 20)); err != nil {
		t.Fatalf("Append: %v", err)
	}

	n, err := store.PseudonymizeUser(ctx, "acme", "alice@acme", erasureSalt)
	if err != nil {
		t.Fatalf("PseudonymizeUser: %v", err)
	}
	if n != 3 {
		t.Errorf("rewrote %d events, want 3 (alice's events only)", n)
	}

	want := billingstore.Pseudonymize("alice@acme", erasureSalt)
	events, _ := store.Since(ctx, "acme", 0, 0)
	for _, e := range events {
		if e.SequenceNumber == 4 { // bob's event
			if e.UserID != "bob@acme" {
				t.Errorf("bob's event was rewritten: UserID=%q", e.UserID)
			}
			continue
		}
		if e.UserID != want { // alice's events
			t.Errorf("event %d not pseudonymized: UserID=%q, want %q", e.SequenceNumber, e.UserID, want)
		}
		// §12.8: sequence number and cost dimensions survive intact.
		if e.TenantID != "acme" || e.TokensInput != 10 {
			t.Errorf("event %d lost a retained field: tenant=%q tokensIn=%d", e.SequenceNumber, e.TenantID, e.TokensInput)
		}
	}
}

func TestPseudonymizeUserIsIdempotent(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()
	if _, err := store.Append(ctx, billed("acme", "alice@acme", 10)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := store.PseudonymizeUser(ctx, "acme", "alice@acme", erasureSalt); err != nil {
		t.Fatalf("first PseudonymizeUser: %v", err)
	}
	n, err := store.PseudonymizeUser(ctx, "acme", "alice@acme", erasureSalt)
	if err != nil {
		t.Fatalf("second PseudonymizeUser: %v", err)
	}
	if n != 0 {
		t.Errorf("a re-run rewrote %d events; the original user id should no longer be present", n)
	}
}

func TestPseudonymizeUserRejectsEmptyArgs(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()
	cases := []struct {
		name         string
		tenant, user string
		salt         []byte
	}{
		{"empty tenant", "", "alice@acme", erasureSalt},
		{"empty user", "acme", "", erasureSalt},
		{"empty salt", "acme", "alice@acme", nil},
	}
	for _, tc := range cases {
		if _, err := store.PseudonymizeUser(ctx, tc.tenant, tc.user, tc.salt); !errors.Is(err, billingstore.ErrPseudonymizeArg) {
			t.Errorf("%s: got %v, want ErrPseudonymizeArg", tc.name, err)
		}
	}
}

func TestPseudonymizeUserScopedToTenant(t *testing.T) {
	store := billingstore.NewMemory()
	ctx := context.Background()
	// The same user-id string appears in two tenants.
	if _, err := store.Append(ctx, billed("acme", "alice", 10)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := store.Append(ctx, billed("globex", "alice", 10)); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if _, err := store.PseudonymizeUser(ctx, "acme", "alice", erasureSalt); err != nil {
		t.Fatalf("PseudonymizeUser: %v", err)
	}

	globex, _ := store.Since(ctx, "globex", 0, 0)
	if len(globex) != 1 || globex[0].UserID != "alice" {
		t.Errorf("globex's event must be untouched, got %+v", globex)
	}
}
