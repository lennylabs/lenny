// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §8.10 line 1062 — when the resumed parent re-issues
// lenny/await_children the gateway streams settled child results "in
// original-settlement order". collectChildResults must therefore order
// the `all` / `settled` result set by each child's settle witness, not
// by the caller-supplied childIDs order. F-8.10.4.
func TestCollectChildResultsOrdersBySettlement_spec_8_10_1062(t *testing.T) {
	store := memstore.New()
	base := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)

	// childIDs order is [a, b, c]; settle order is [c, a, b] by UpdatedAt.
	seedSettled(t, store, "a", base.Add(2*time.Second))
	seedSettled(t, store, "b", base.Add(3*time.Second))
	seedSettled(t, store, "c", base.Add(1*time.Second))

	got, done, err := collectChildResults(context.Background(), store, nil, nil, "acme", []string{"a", "b", "c"}, "all")
	if err != nil {
		t.Fatalf("collectChildResults: %v", err)
	}
	if !done {
		t.Fatal("all children terminal but settle condition not met")
	}
	order := []string{got[0].TaskID, got[1].TaskID, got[2].TaskID}
	want := []string{"c", "a", "b"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("result order = %v, want %v (settlement order)", order, want)
		}
	}
}

// Children sharing a settle instant fall back to a stable childIDs-order
// tie-break so a repeated poll yields a deterministic frame.
func TestCollectChildResultsStableTieBreak_spec_8_10_1062(t *testing.T) {
	store := memstore.New()
	at := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)
	seedSettled(t, store, "x", at)
	seedSettled(t, store, "y", at)

	got, done, err := collectChildResults(context.Background(), store, nil, nil, "acme", []string{"y", "x"}, "settled")
	if err != nil || !done {
		t.Fatalf("collectChildResults: done=%v err=%v", done, err)
	}
	if got[0].TaskID != "y" || got[1].TaskID != "x" {
		t.Fatalf("tie-break order = [%s %s], want [y x] (childIDs order)", got[0].TaskID, got[1].TaskID)
	}
}

func seedSettled(t *testing.T, store sessionstore.Store, id string, settledAt time.Time) {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateCompleted,
		ParentSessionID: "parent",
		CreatedAt:       settledAt.Add(-time.Minute), UpdatedAt: settledAt,
	}); err != nil {
		t.Fatalf("seed settled %s: %v", id, err)
	}
}
