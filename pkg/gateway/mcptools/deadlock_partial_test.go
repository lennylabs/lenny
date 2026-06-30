// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/deadlock"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// TestAwaitChildrenYieldsDeadlockDetectedPartial_spec_8_8_985 verifies
// that once the §8.8 detector has flagged a session as a deadlocked
// subtree root, its lenny/await_children poll returns the
// deadlock_detected event (carrying blockedRequests + willTimeoutAt)
// ahead of the per-child input_required partial. F-8.8.6.
func TestAwaitChildrenYieldsDeadlockDetectedPartial_spec_8_8_985(t *testing.T) {
	store := memstore.New()
	reg := inputwait.NewRegistry()
	at := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	reg.SetClock(func() time.Time { return at })
	tracker := deadlock.NewAwaitTracker()
	mgr := deadlock.NewManager(120*time.Second, nil)

	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:               store,
		Executor:            executor.NewEchoExecutor(),
		InputWaits:          reg,
		DeadlockTracker:     tracker,
		Deadlocks:           mgr,
		RequestInputTimeout: time.Second,
		Clock:               func() time.Time { return at },
		IDFunc:              func() string { return "sess_mcp" },
		TenantID:            "acme",
	})

	mkSession(t, store, "sess_p", session.StateRunning, "")
	mkSession(t, store, "sess_c", session.StateRunning, "sess_p")
	if _, err := reg.Register("sess_c", "req_dl", nil); err != nil {
		t.Fatalf("seed pending input: %v", err)
	}

	// Seed the manager with the confirmed deadlock the periodic sweep
	// produces: sess_p awaiting sess_c, which is blocked on request_input.
	snap := deadlock.Snapshot{Nodes: map[string]deadlock.Node{
		"sess_p": {SessionID: "sess_p", TenantID: "acme", State: session.StateRunning, AwaitingChildIDs: []string{"sess_c"}},
		"sess_c": {SessionID: "sess_c", TenantID: "acme", State: session.StateRunning, PendingInputs: []deadlock.PendingInput{{RequestID: "req_dl", BlockedSince: at}}},
	}}
	mgr.Observe(snap, at)

	resp := call(t, srv.Handler(), "lenny/await_children",
		`{"sessionId":"sess_p","childIds":["sess_c"],"mode":"all"}`)
	text := resultText(t, resp)

	var body struct {
		Partial  bool           `json:"partial"`
		Deadlock deadlock.Event `json:"deadlock"`
	}
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	if !body.Partial || body.Deadlock.Type != deadlock.EventType {
		t.Fatalf("want deadlock_detected partial, got %q", text)
	}
	if body.Deadlock.DeadlockedSubtreeRoot != "sess_p" {
		t.Errorf("root = %q, want sess_p", body.Deadlock.DeadlockedSubtreeRoot)
	}
	if len(body.Deadlock.BlockedRequests) != 1 ||
		body.Deadlock.BlockedRequests[0].RequestID != "req_dl" ||
		body.Deadlock.BlockedRequests[0].TaskID != "sess_c" {
		t.Errorf("blockedRequests = %+v, want one for req_dl/sess_c", body.Deadlock.BlockedRequests)
	}
	if !body.Deadlock.BlockedRequests[0].BlockedSince.Equal(at) {
		t.Errorf("blockedSince = %v, want %v", body.Deadlock.BlockedRequests[0].BlockedSince, at)
	}
	if want := at.Add(120 * time.Second); !body.Deadlock.WillTimeoutAt.Equal(want) {
		t.Errorf("willTimeoutAt = %v, want %v", body.Deadlock.WillTimeoutAt, want)
	}
}
