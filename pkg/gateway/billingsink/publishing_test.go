// SPDX-License-Identifier: MIT

package billingsink

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
)

// spec: §11.2.1 line 137 — the decorator publishes a sealed event to the
// sinks only after the synchronous primary write confirms.
func TestPublishing_PublishesAfterAppend_spec_11_2_1_137(t *testing.T) {
	rec := &recordSink{name: "webhook"}
	pub := NewPublisher([]Sink{rec}, nil)
	store := NewPublishing(billingstore.NewMemory(), pub, PublishingOptions{Sync: true})

	sealed, err := store.Append(context.Background(), billingstore.Event{
		TenantID:  "acme",
		EventType: billingstore.EventSessionCreated,
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if sealed.SequenceNumber == 0 {
		t.Fatalf("Append did not seal a sequence number")
	}
	if rec.calls != 1 {
		t.Fatalf("sink calls = %d, want 1 after a successful Append", rec.calls)
	}
}

// A failed primary write must not publish — delivery happens only after
// the durable write confirms.
func TestPublishing_DoesNotPublishOnAppendError_spec_11_2_1_137(t *testing.T) {
	rec := &recordSink{name: "webhook"}
	pub := NewPublisher([]Sink{rec}, nil)
	store := NewPublishing(failingStore{}, pub, PublishingOptions{Sync: true})

	if _, err := store.Append(context.Background(), billingstore.Event{TenantID: "acme", EventType: billingstore.EventSessionCreated}); err == nil {
		t.Fatalf("Append returned nil, want the underlying error")
	}
	if rec.calls != 0 {
		t.Fatalf("sink calls = %d, want 0 when the primary write failed", rec.calls)
	}
}

// When no sink is configured the decorator returns the store unwrapped so
// the billing write path is untouched.
func TestNewPublishing_NoSinksReturnsUnwrapped(t *testing.T) {
	mem := billingstore.NewMemory()
	got := NewPublishing(mem, NewPublisher(nil, nil), PublishingOptions{})
	if got != billingstore.Store(mem) {
		t.Fatalf("NewPublishing wrapped the store despite an empty publisher")
	}
}

// failingStore is a billingstore.Store whose Append always errors.
type failingStore struct{ billingstore.Store }

func (failingStore) Append(context.Context, billingstore.Event) (billingstore.Event, error) {
	return billingstore.Event{}, errors.New("primary write failed")
}
