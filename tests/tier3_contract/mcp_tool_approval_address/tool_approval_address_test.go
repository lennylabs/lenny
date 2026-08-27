// SPDX-License-Identifier: MIT

//go:build contract

// Package mcp_tool_approval_address_test is the Tier 3 contract suite for
// the address the §7.2 `tool_use_requested` SSE payload carries and for
// the §15.2 MCP projection that reads it.
//
// Every session is bound to a slot on every pod and a session-mode slot's
// identifier is its session's identifier, so the payload names one
// address: the session its stream belongs to. The producer
// (sessionserver.ToolApprovalGate) and the consumer (the MCP
// tool-approval projection) are exercised together over a real event bus
// and a real Streamable HTTP SSE attach, so neither side can keep a slot
// address the other no longer speaks.
package mcp_tool_approval_address_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/toolapproval"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

const (
	approvalTenant  = "acme"
	approvalSession = "sess_toolapproval"
	approvalCallID  = "tc-1"
)

// TestToolUseRequestedAddressesSessionOnly_spec_7_2 round-trips a live
// `tool_use_requested` event from the §7.2 approval gate through the
// §15.2 MCP projection and pins the address both halves speak. The
// published payload's key set is exactly the §7.2 triple (tool_call_id,
// tool, args) with no slot key, and the projected elicitation names the
// session in its `_meta` and carries no slot key anywhere in the frame.
//
// diagnosis: a failure means the producer and the MCP consumer of the
// tool-approval payload disagree about the call's address. A slot key on
// the published payload means the gate reintroduced the duplicate
// address the identity invariant retired; a slot key in the projected
// frame, or a missing `lenny/sessionId`, means the projection is naming
// the call by something other than its session.
//
// spec: §7.2 (tool_use_requested); §15.2 (per-kind MCP projection);
// §5.2 (identity invariant).
func TestToolUseRequestedAddressesSessionOnly_spec_7_2(t *testing.T) {
	bus := sessionevents.NewBus(64)
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: approvalSession, TenantID: approvalTenant, UserID: "alice@acme.com",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	waits := toolapproval.NewRegistry()
	gate := sessionserver.NewToolApprovalGate(
		store, interactionstore.NewMemory(), bus, waits,
		func() time.Time { return time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC) },
		0,
	)

	// The bus subscription observes the producer's payload verbatim; the
	// MCP attach below observes the consumer's projection of that same
	// event.
	sub, err := bus.SubscribeForTenant(approvalTenant, approvalSession, 0, 16)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()

	srv := mcp.NewServer()
	srv.SetAttach(mcp.AttachConfig{
		Events:            bus,
		TenantFromRequest: func(*http.Request) string { return approvalTenant },
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancelCall := context.WithCancel(context.Background())
	defer cancelCall()
	go func() {
		_, _ = gate.AwaitApproval(ctx, approvalTenant, approvalSession, executor.PendingToolCall{
			ID:        approvalCallID,
			Name:      "lenny/deploy",
			Arguments: json.RawMessage(`{"target":"prod"}`),
		})
	}()

	assertPublishedPayload(t, sub)
	assertProjectedElicitation(t, ts.URL)
}

// assertPublishedPayload pins the producer half: the §7.2 event data
// decodes to exactly {tool_call_id, tool, args}.
func assertPublishedPayload(t *testing.T, sub *sessionevents.Subscription) {
	t.Helper()
	select {
	case ev := <-sub.Events():
		if ev.Type != "tool_use_requested" {
			t.Fatalf("event type = %q, want tool_use_requested", ev.Type)
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
			t.Fatalf("event data not JSON: %v", err)
		}
		for _, want := range []string{"tool_call_id", "tool", "args"} {
			if _, ok := payload[want]; !ok {
				t.Errorf("published payload is missing %q: %s", want, ev.Data)
			}
		}
		for k := range payload {
			switch k {
			case "tool_call_id", "tool", "args":
			default:
				t.Errorf("published payload carries the unexpected key %q: %s", k, ev.Data)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no tool_use_requested event published within 5s")
	}
}

// assertProjectedElicitation pins the consumer half: the §15.2 MCP
// projection of the same event is a tool-approval elicitation that names
// the session and carries no slot key at any depth of the frame.
func assertProjectedElicitation(t *testing.T, baseURL string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      mcp.AttachToolName,
			"arguments": map[string]any{"sessionId": approvalSession},
		},
	})
	if err != nil {
		t.Fatalf("marshal attach request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build attach request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	frames, cancel := openSSE(t, req)
	defer cancel()

	frame, seen, ok := waitForFrame(t, frames, 5*time.Second, func(data string) bool {
		return strings.Contains(data, "toolapprove:"+approvalCallID)
	})
	if !ok {
		t.Fatalf("attach stream never replayed the tool-approval elicitation; frames seen: %v", seen)
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(frame), &m); err != nil {
		t.Fatalf("projected frame not JSON: %v; frame=%s", err, frame)
	}
	if m["method"] != "elicitation/create" {
		t.Errorf("method = %v, want elicitation/create; frame=%s", m["method"], frame)
	}
	params, _ := m["params"].(map[string]any)
	meta, _ := params["_meta"].(map[string]any)
	if meta["lenny/sessionId"] != approvalSession {
		t.Errorf("_meta lenny/sessionId = %v, want %q; frame=%s", meta["lenny/sessionId"], approvalSession, frame)
	}
	if meta["lenny/toolCallId"] != approvalCallID {
		t.Errorf("_meta lenny/toolCallId = %v, want %q; frame=%s", meta["lenny/toolCallId"], approvalCallID, frame)
	}
	for _, k := range slotKeys(m) {
		t.Errorf("projected frame carries the retired slot key %q: %s", k, frame)
	}
}

// slotKeys walks a decoded JSON-RPC frame and reports every object key
// naming a slot address. The retired key is not addressable any more, so
// its reappearance at any depth is the defect this suite catches.
func slotKeys(v any) []string {
	var found []string
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			if strings.Contains(strings.ToLower(k), "slot") {
				found = append(found, k)
			}
			found = append(found, slotKeys(sub)...)
		}
	case []any:
		for _, sub := range t {
			found = append(found, slotKeys(sub)...)
		}
	}
	return found
}

// openSSE issues req and returns a channel of `data:` payloads plus a
// cancel func. The attach handler holds the connection open after
// replaying the backlog, so the caller must cancel.
func openSSE(t *testing.T, req *http.Request) (<-chan string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open SSE stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()
		t.Fatalf("SSE stream status: %d, body=%s", resp.StatusCode, b)
	}
	out := make(chan string, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "data: ") {
				out <- strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return out, cancel
}

// waitForFrame reads payloads until pred matches one or timeout elapses.
func waitForFrame(t *testing.T, ch <-chan string, timeout time.Duration, pred func(string) bool) (string, []string, bool) {
	t.Helper()
	deadline := time.After(timeout)
	var seen []string
	for {
		select {
		case f, ok := <-ch:
			if !ok {
				return "", seen, false
			}
			seen = append(seen, f)
			if pred(f) {
				return f, seen, true
			}
		case <-deadline:
			return "", seen, false
		}
	}
}
