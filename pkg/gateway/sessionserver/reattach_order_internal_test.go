// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
)

// spec: §8.10 line 1062 — the resumed parent's children_reattached event
// streams already-settled child results "in original-settlement order",
// then the still-running children. F-8.10.4.
func TestEmitChildrenReattachedStreamsArchiveInSettlementOrder_spec_8_10_1062(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	archive := treearchive.NewMemory()
	srv := New(store, Options{Events: bus, TreeArchive: archive})
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	mustCreate(t, store, sessionStoreRow("parent", "", session.StateAwaitingClientAction, now))
	// One still-running child lives in the store; it must trail the
	// settled children in the payload.
	mustCreate(t, store, sessionStoreRow("child_active", "parent", session.StateRunning, now))

	// Settle order is the reverse of session-id order: child_z reached a
	// terminal state first (T+1s), child_a second (T+2s). A correct
	// implementation orders by SettledAt, not by row/id order.
	archiveNode(t, archive, "parent", "child_z", now.Add(1*time.Second))
	archiveNode(t, archive, "parent", "child_a", now.Add(2*time.Second))

	srv.emitChildrenReattached(context.Background(), "acme", "parent")

	ev := childrenReattachedInternal(t, bus, "parent")
	if ev == nil {
		t.Fatal("no children_reattached event emitted")
	}
	got := make([]string, len(ev))
	for i, c := range ev {
		got[i] = c.SessionID
	}
	want := []string{"child_z", "child_a", "child_active"}
	if len(got) != len(want) {
		t.Fatalf("children = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("children order = %v, want %v (settlement order then active)", got, want)
		}
	}
	// The settled entries carry the archived §8.8 result body verbatim.
	if len(ev[0].Result) == 0 {
		t.Error("settled child child_z carried no result body from the archive")
	}
	if ev[2].State != string(session.StateRunning) {
		t.Errorf("trailing child state = %q, want running", ev[2].State)
	}
}

// When no child is still active the event is suppressed: a resumed
// parent with only settled children has nothing to re-await.
func TestEmitChildrenReattachedSuppressedWhenNoneActive_spec_8_10_1062(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	archive := treearchive.NewMemory()
	srv := New(store, Options{Events: bus, TreeArchive: archive})
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	mustCreate(t, store, sessionStoreRow("p", "", session.StateAwaitingClientAction, now))
	archiveNode(t, archive, "p", "c1", now.Add(time.Second))

	srv.emitChildrenReattached(context.Background(), "acme", "p")

	if ev := childrenReattachedInternal(t, bus, "p"); ev != nil {
		t.Errorf("event emitted with no active children: %v", ev)
	}
}

func sessionStoreRow(id, parent string, state session.State, now time.Time) sessionstore.Session {
	return sessionstore.Session{ID: id, TenantID: "acme", State: state, ParentSessionID: parent, CreatedAt: now, UpdatedAt: now}
}

func childrenReattachedInternal(t *testing.T, bus *sessionevents.Bus, sessionID string) []reattachedChild {
	t.Helper()
	for _, e := range bus.History(sessionID, 0) {
		if e.Type != "children_reattached" {
			continue
		}
		var payload struct {
			Children []reattachedChild `json:"children"`
		}
		if err := json.Unmarshal([]byte(e.Data), &payload); err != nil {
			t.Fatalf("decode children_reattached: %v", err)
		}
		return payload.Children
	}
	return nil
}

func archiveNode(t *testing.T, archive *treearchive.Memory, parent, node string, settledAt time.Time) {
	t.Helper()
	result, _ := json.Marshal(map[string]any{"taskId": node, "schemaVersion": 1, "state": "completed"})
	if err := archive.Archive(context.Background(), treearchive.ArchivedNode{
		TenantID:        "acme",
		RootSessionID:   parent, // parent is the tree root in these single-level trees
		NodeSessionID:   node,
		ParentSessionID: parent,
		State:           string(session.StateCompleted),
		Result:          string(result),
		SettledAt:       settledAt,
	}); err != nil {
		t.Fatalf("archive node %s: %v", node, err)
	}
}
