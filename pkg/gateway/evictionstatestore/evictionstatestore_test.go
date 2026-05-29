// SPDX-License-Identifier: MIT

package evictionstatestore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/evictionstatestore"
)

// spec: §12.2 (Put + Get round-trip)
// diagnosis: Put persists the record and Get reads it back with the
// CreatedAt and UpdatedAt timestamps the store stamped.
func TestPutAndGet(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	store := evictionstatestore.NewMemoryStore(func() time.Time { return now })

	r := evictionstatestore.Record{
		TenantID:           "acme",
		SessionID:          "sess_42",
		LastMessageContext: []byte(`{"cursor":7,"text":"hi"}`),
	}
	if err := store.Put(context.Background(), r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := store.Get(context.Background(), "acme", "sess_42")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.LastMessageContext) != `{"cursor":7,"text":"hi"}` {
		t.Errorf("LastMessageContext = %q, want JSON cursor", string(got.LastMessageContext))
	}
	if !got.CreatedAt.Equal(now) || !got.UpdatedAt.Equal(now) {
		t.Errorf("timestamps = (%v, %v), want both %v", got.CreatedAt, got.UpdatedAt, now)
	}
}

// spec: §12.2 (upsert preserves CreatedAt and bumps UpdatedAt)
// diagnosis: a second Put under the same composite key updates the
// LastMessageContext, preserves CreatedAt, and advances UpdatedAt.
func TestPutPreservesCreatedAtOnUpdate(t *testing.T) {
	clock := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	store := evictionstatestore.NewMemoryStore(func() time.Time { return clock })

	_ = store.Put(context.Background(), evictionstatestore.Record{
		TenantID: "acme", SessionID: "sess_42", LastMessageContext: []byte("first"),
	})
	clock = clock.Add(time.Hour)
	_ = store.Put(context.Background(), evictionstatestore.Record{
		TenantID: "acme", SessionID: "sess_42", LastMessageContext: []byte("second"),
	})

	got, _ := store.Get(context.Background(), "acme", "sess_42")
	if string(got.LastMessageContext) != "second" {
		t.Errorf("LastMessageContext = %q, want second after upsert", string(got.LastMessageContext))
	}
	original := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if !got.CreatedAt.Equal(original) {
		t.Errorf("CreatedAt = %v, want preserved %v", got.CreatedAt, original)
	}
	if !got.UpdatedAt.After(got.CreatedAt) {
		t.Errorf("UpdatedAt = %v should be after CreatedAt = %v", got.UpdatedAt, got.CreatedAt)
	}
}

// spec: §12.2 (Get on a missing row returns ErrNotFound)
func TestGetMissingReturnsNotFound(t *testing.T) {
	store := evictionstatestore.NewMemoryStore(nil)
	_, err := store.Get(context.Background(), "acme", "missing-session")
	if !errors.Is(err, evictionstatestore.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// spec: §12.2 (Delete is idempotent)
// diagnosis: a missing row does not error so the terminal-state
// cleanup path can replay deletes after a partial failure.
func TestDeleteIsIdempotent(t *testing.T) {
	store := evictionstatestore.NewMemoryStore(nil)
	_ = store.Put(context.Background(), evictionstatestore.Record{TenantID: "acme", SessionID: "x", LastMessageContext: []byte("a")})

	if err := store.Delete(context.Background(), "acme", "x"); err != nil {
		t.Errorf("first delete: %v", err)
	}
	if err := store.Delete(context.Background(), "acme", "x"); err != nil {
		t.Errorf("replay delete: %v", err)
	}
}

// spec: §12.8 (DeleteByUser removes the listed session ids)
// diagnosis: the orchestrator passes the user's session ids; the
// store removes only those rows.
func TestDeleteByUserScopes(t *testing.T) {
	store := evictionstatestore.NewMemoryStore(nil)
	_ = store.Put(context.Background(), evictionstatestore.Record{TenantID: "acme", SessionID: "a", LastMessageContext: []byte("a")})
	_ = store.Put(context.Background(), evictionstatestore.Record{TenantID: "acme", SessionID: "b", LastMessageContext: []byte("b")})
	_ = store.Put(context.Background(), evictionstatestore.Record{TenantID: "acme", SessionID: "c", LastMessageContext: []byte("c")})

	if err := store.DeleteByUser(context.Background(), "acme", "alice", []string{"a", "c"}); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if _, err := store.Get(context.Background(), "acme", "a"); !errors.Is(err, evictionstatestore.ErrNotFound) {
		t.Error("session 'a' should be deleted")
	}
	if _, err := store.Get(context.Background(), "acme", "b"); err != nil {
		t.Errorf("session 'b' should survive: %v", err)
	}
	if _, err := store.Get(context.Background(), "acme", "c"); !errors.Is(err, evictionstatestore.ErrNotFound) {
		t.Error("session 'c' should be deleted")
	}
}

// spec: §12.8 (DeleteByTenant removes every row in the tenant)
func TestDeleteByTenantSweepsAll(t *testing.T) {
	store := evictionstatestore.NewMemoryStore(nil)
	_ = store.Put(context.Background(), evictionstatestore.Record{TenantID: "acme", SessionID: "a", LastMessageContext: []byte("a")})
	_ = store.Put(context.Background(), evictionstatestore.Record{TenantID: "acme", SessionID: "b", LastMessageContext: []byte("b")})
	_ = store.Put(context.Background(), evictionstatestore.Record{TenantID: "globex", SessionID: "z", LastMessageContext: []byte("z")})

	if err := store.DeleteByTenant(context.Background(), "acme"); err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if _, err := store.Get(context.Background(), "acme", "a"); !errors.Is(err, evictionstatestore.ErrNotFound) {
		t.Error("acme/a should be deleted")
	}
	if _, err := store.Get(context.Background(), "acme", "b"); !errors.Is(err, evictionstatestore.ErrNotFound) {
		t.Error("acme/b should be deleted")
	}
	if _, err := store.Get(context.Background(), "globex", "z"); err != nil {
		t.Errorf("globex/z should survive a delete on acme: %v", err)
	}
}

// spec: §12.2 (Put rejects empty composite-key inputs)
// diagnosis: an empty tenant or session id is a programming error;
// rejecting at the store boundary prevents a malformed row from
// landing in storage.
func TestPutRejectsEmptyIDs(t *testing.T) {
	store := evictionstatestore.NewMemoryStore(nil)
	cases := []evictionstatestore.Record{
		{SessionID: "x", LastMessageContext: []byte("a")},
		{TenantID: "acme", LastMessageContext: []byte("a")},
		{LastMessageContext: []byte("a")},
	}
	for _, r := range cases {
		if err := store.Put(context.Background(), r); err == nil {
			t.Errorf("Put accepted record with empty ids: %+v", r)
		}
	}
}

// spec: §12.5 (IsMinIOKey marks large contexts for the GC sweep)
// diagnosis: the GC sweep keys off IsMinIOKey to decide whether the
// row removal needs a MinIO delete. The flag must round-trip on
// Put + Get.
func TestIsMinIOKeyRoundTrip(t *testing.T) {
	store := evictionstatestore.NewMemoryStore(nil)
	_ = store.Put(context.Background(), evictionstatestore.Record{
		TenantID:           "acme",
		SessionID:          "sess_big",
		LastMessageContext: []byte("/acme/eviction/sess_big/context"),
		IsMinIOKey:         true,
	})
	got, _ := store.Get(context.Background(), "acme", "sess_big")
	if !got.IsMinIOKey {
		t.Errorf("IsMinIOKey did not round-trip: %+v", got)
	}
}

// spec: §4.4 lines 268–273 (eviction-state record carries the
// §4.2 generations, conversation cursor, evicted_at timestamp, and the
// workspace_lost / context_truncated flags so the §7.2 resume path
// can fence coordinator handoffs and surface workspaceLost: true).
// diagnosis: extending the Record without honoring the new fields in
// the store would leave the §4.4 fallback writer silently dropping
// generations and cursor data, breaking the §7.2 resume contract.
func TestRecordCarriesEvictionFallbackFields(t *testing.T) {
	store := evictionstatestore.NewMemoryStore(nil)
	when := time.Date(2026, 5, 22, 13, 0, 0, 0, time.UTC)
	want := evictionstatestore.Record{
		TenantID:               "acme",
		SessionID:              "sess_42",
		RecoveryGeneration:     7,
		CoordinationGeneration: 3,
		ConversationCursor:     "evt:42",
		LastMessageContext:     []byte("inline-context"),
		EvictedAt:              when,
		WorkspaceLost:          true,
		ContextTruncated:       true,
	}
	if err := store.Put(context.Background(), want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := store.Get(context.Background(), "acme", "sess_42")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RecoveryGeneration != 7 {
		t.Errorf("RecoveryGeneration = %d, want 7", got.RecoveryGeneration)
	}
	if got.CoordinationGeneration != 3 {
		t.Errorf("CoordinationGeneration = %d, want 3", got.CoordinationGeneration)
	}
	if got.ConversationCursor != "evt:42" {
		t.Errorf("ConversationCursor = %q, want evt:42", got.ConversationCursor)
	}
	if !got.EvictedAt.Equal(when) {
		t.Errorf("EvictedAt = %v, want %v", got.EvictedAt, when)
	}
	if !got.WorkspaceLost {
		t.Error("WorkspaceLost did not round-trip")
	}
	if !got.ContextTruncated {
		t.Error("ContextTruncated did not round-trip")
	}
}
