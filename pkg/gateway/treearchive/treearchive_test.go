// SPDX-License-Identifier: MIT

package treearchive_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
)

func node(root, id, state string, settledAt time.Time) treearchive.ArchivedNode {
	return treearchive.ArchivedNode{
		TenantID:        "acme",
		RootSessionID:   root,
		NodeSessionID:   id,
		ParentSessionID: root,
		State:           state,
		Result:          `{"taskId":"` + id + `"}`,
		SettledAt:       settledAt,
	}
}

func TestArchiveAndReplayInSettlementOrder(t *testing.T) {
	store := treearchive.NewMemory()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Archive out of settlement order.
	_ = store.Archive(context.Background(), node("root", "c2", "completed", base.Add(2*time.Minute)))
	_ = store.Archive(context.Background(), node("root", "c1", "completed", base.Add(1*time.Minute)))
	_ = store.Archive(context.Background(), node("root", "c3", "failed", base.Add(3*time.Minute)))

	got, err := store.Replay(context.Background(), "acme", "root")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	want := []string{"c1", "c2", "c3"}
	if len(got) != len(want) {
		t.Fatalf("Replay returned %d nodes, want %d", len(got), len(want))
	}
	for i, n := range got {
		if n.NodeSessionID != want[i] {
			t.Errorf("Replay[%d] = %q, want %q — settlement order", i, n.NodeSessionID, want[i])
		}
	}
}

func TestArchiveIsIdempotent(t *testing.T) {
	store := treearchive.NewMemory()
	now := time.Now()
	_ = store.Archive(context.Background(), node("root", "c1", "completed", now))
	_ = store.Archive(context.Background(), node("root", "c1", "completed", now))

	got, err := store.Replay(context.Background(), "acme", "root")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("re-archiving the same node produced %d entries, want 1", len(got))
	}
}

func TestGetReturnsTheArchivedNode(t *testing.T) {
	store := treearchive.NewMemory()
	now := time.Now()
	_ = store.Archive(context.Background(), node("root", "c1", "failed", now))

	got, err := store.Get(context.Background(), "acme", "root", "c1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != "failed" || got.Result != `{"taskId":"c1"}` {
		t.Errorf("Get returned %+v, want the archived failed node", got)
	}
	if got.ArchivedAt.IsZero() {
		t.Error("Archive did not stamp ArchivedAt")
	}
}

func TestGetUnknownReturnsNotFound(t *testing.T) {
	store := treearchive.NewMemory()
	if _, err := store.Get(context.Background(), "acme", "root", "absent"); !errors.Is(err, treearchive.ErrNotFound) {
		t.Errorf("Get of an unknown node err = %v, want ErrNotFound", err)
	}
}

func TestGetByNodeResolvesWithoutTheRoot(t *testing.T) {
	store := treearchive.NewMemory()
	now := time.Now()
	_ = store.Archive(context.Background(), node("root", "c1", "completed", now))

	got, err := store.GetByNode(context.Background(), "acme", "c1")
	if err != nil {
		t.Fatalf("GetByNode: %v", err)
	}
	if got.RootSessionID != "root" || got.NodeSessionID != "c1" {
		t.Errorf("GetByNode returned %+v, want the c1 node", got)
	}
	if _, err := store.GetByNode(context.Background(), "acme", "absent"); !errors.Is(err, treearchive.ErrNotFound) {
		t.Errorf("GetByNode of an unknown node err = %v, want ErrNotFound", err)
	}
	// Cross-tenant GetByNode finds nothing.
	if _, err := store.GetByNode(context.Background(), "globex", "c1"); !errors.Is(err, treearchive.ErrNotFound) {
		t.Errorf("cross-tenant GetByNode err = %v, want ErrNotFound", err)
	}
}

func TestReplayScopedByRoot(t *testing.T) {
	store := treearchive.NewMemory()
	now := time.Now()
	_ = store.Archive(context.Background(), node("root-a", "c1", "completed", now))
	_ = store.Archive(context.Background(), node("root-b", "c2", "completed", now))

	got, err := store.Replay(context.Background(), "acme", "root-a")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(got) != 1 || got[0].NodeSessionID != "c1" {
		t.Errorf("Replay(root-a) = %+v, want only c1", got)
	}
}

func TestTenantIsolation(t *testing.T) {
	store := treearchive.NewMemory()
	now := time.Now()
	_ = store.Archive(context.Background(), node("root", "c1", "completed", now))

	// A different tenant sees neither the node nor the tree.
	if _, err := store.Get(context.Background(), "globex", "root", "c1"); !errors.Is(err, treearchive.ErrNotFound) {
		t.Errorf("cross-tenant Get err = %v, want ErrNotFound", err)
	}
	got, err := store.Replay(context.Background(), "globex", "root")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("cross-tenant Replay returned %d nodes, want 0", len(got))
	}
}
