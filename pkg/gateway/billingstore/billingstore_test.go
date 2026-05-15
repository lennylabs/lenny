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
