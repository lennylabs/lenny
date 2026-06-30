// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// virtualChildInterfaceFields is the closed set of keys the §8.2
// virtual MCP child interface exposes to a parent on resume. The
// spec's "What the parent never sees" guarantee (§8.2 lines 105-107:
// pod addresses, internal endpoints, raw credentials) is enforced by
// keeping the re-injected child surface to this logical schema: session
// id, state, the pending request handle, the §8.8 result body, and the
// delegation correlation handle. A pod IP, internal endpoint, or
// credential field appearing here would breach the virtual-interface
// boundary.
var virtualChildInterfaceFields = map[string]bool{
	"session_id":          true,
	"state":               true,
	"pending_request_id":  true,
	"result":              true,
	"delegation_lease_id": true,
}

// The §8.2 virtual child interface re-injected on parent resume MUST
// hide pod addresses, internal endpoints, and raw credentials. The
// children_reattached payload is the parent-visible projection of every
// active and settled child; this test pins its schema to the closed
// logical field set so a future field that leaks a pod IP, endpoint, or
// credential into the virtual interface fails the build.
//
// spec: §8.2 lines 96-107 (virtual MCP child interface; "What the
// parent never sees: Pod addresses, internal endpoints, raw
// credentials"). F-8.2.11.
func TestEmitChildrenReattachedHidesInternalEndpoints_spec_8_2(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	archive := treearchive.NewMemory()
	srv := New(store, Options{Events: bus, TreeArchive: archive})
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	mustCreate(t, store, sessionStoreRow("parent", "", session.StateAwaitingClientAction, now))
	mustCreate(t, store, sessionStoreRow("child_active", "parent", session.StateRunning, now))
	archiveNode(t, archive, "parent", "child_done", now.Add(time.Second))

	srv.emitChildrenReattached(context.Background(), "acme", "parent")

	raw := childrenReattachedRaw(t, bus, "parent")
	if len(raw) == 0 {
		t.Fatal("no children_reattached event emitted")
	}
	for i, child := range raw {
		for key := range child {
			if !virtualChildInterfaceFields[key] {
				t.Errorf("child[%d] exposes field %q outside the virtual-interface schema: the §8.2 boundary hides pod addresses, internal endpoints, and credentials", i, key)
			}
		}
	}
}

// A child that failed with a pending elicitation outstanding must have
// that elicitation held across parent failover and replayed on resume.
// The re-injected virtual child interface carries the pending request id
// so the parent can answer it; without this a parent that fails
// mid-elicitation resumes with no record of the pending request.
//
// spec: §8.2 lines 108-113 (pending elicitations held across parent
// failure, replayed via the re-injected virtual child interface on
// resume). F-8.2.11 / F-7.2.16.
func TestEmitChildrenReattachedReplaysPendingElicitation_spec_8_2(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	waits := inputwait.NewRegistry()
	srv := New(store, Options{Events: bus, InputWaits: waits})
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	mustCreate(t, store, sessionStoreRow("parent", "", session.StateAwaitingClientAction, now))
	mustCreate(t, store, sessionStoreRow("child", "parent", session.StateRunning, now))

	// The child raised a pending request before the parent pod failed;
	// it is still outstanding when the parent resumes.
	if _, err := waits.Register("child", "req-7", nil); err != nil {
		t.Fatalf("register pending input: %v", err)
	}

	srv.emitChildrenReattached(context.Background(), "acme", "parent")

	children := childrenReattachedInternal(t, bus, "parent")
	if len(children) != 1 {
		t.Fatalf("children = %d, want 1", len(children))
	}
	if children[0].PendingRequestID != "req-7" {
		t.Errorf("pending_request_id = %q, want %q (elicitation not replayed on resume)", children[0].PendingRequestID, "req-7")
	}
}

// childrenReattachedRaw decodes the children_reattached payload into
// per-child key maps so a test can assert the exposed field set without
// the typed struct silencing an unexpected key.
func childrenReattachedRaw(t *testing.T, bus *sessionevents.Bus, sessionID string) []map[string]json.RawMessage {
	t.Helper()
	for _, e := range bus.History(sessionID, 0) {
		if e.Type != "children_reattached" {
			continue
		}
		var payload struct {
			Children []map[string]json.RawMessage `json:"children"`
		}
		if err := json.Unmarshal([]byte(e.Data), &payload); err != nil {
			t.Fatalf("decode children_reattached: %v", err)
		}
		return payload.Children
	}
	return nil
}
