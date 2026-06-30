// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §9.4 lenny/memory_write and lenny/memory_query MCP tools.

func newMCPForMemory(t *testing.T) (*mcp.Server, sessionstore.Store, memorystore.Store) {
	t.Helper()
	store := memstore.New()
	mem := memorystore.NewInMemory(0, nil)
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    store,
		Memory:   mem,
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})
	return srv, store, mem
}

func seedMemorySession(t *testing.T, store sessionstore.Store, id, user string) {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", UserID: user, RuntimeRef: "echo", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("seed session %q: %v", id, err)
	}
}

func TestMemoryWriteAndQuery(t *testing.T) {
	srv, store, _ := newMCPForMemory(t)
	seedMemorySession(t, store, "sess_1", "alice")

	w := call(t, srv.Handler(), "lenny/memory_write",
		`{"sessionId":"sess_1","content":"the deploy key lives in vault"}`)
	if got := resultText(t, w); got == "" {
		t.Fatal("memory_write returned no result")
	}
	q := call(t, srv.Handler(), "lenny/memory_query",
		`{"sessionId":"sess_1","query":"deploy"}`)
	var resp struct {
		Memories []struct {
			Content string `json:"content"`
		} `json:"memories"`
	}
	if err := json.Unmarshal([]byte(resultText(t, q)), &resp); err != nil {
		t.Fatalf("decode query result: %v", err)
	}
	if len(resp.Memories) != 1 || resp.Memories[0].Content != "the deploy key lives in vault" {
		t.Errorf("memory_query = %+v, want the written memory", resp.Memories)
	}
}

func TestMemoryQueryRecallsAcrossSessions(t *testing.T) {
	srv, store, _ := newMCPForMemory(t)
	// Two sessions owned by the same user.
	seedMemorySession(t, store, "sess_a", "alice")
	seedMemorySession(t, store, "sess_b", "alice")

	call(t, srv.Handler(), "lenny/memory_write",
		`{"sessionId":"sess_a","content":"learned in session a"}`)
	// §9.4: memory recall is user-scoped — a later session sees it.
	// spec: §8.5 line 596 — `query` is required (F-8.5.14).
	q := call(t, srv.Handler(), "lenny/memory_query", `{"sessionId":"sess_b","query":"learned"}`)
	var resp struct {
		Memories []json.RawMessage `json:"memories"`
	}
	_ = json.Unmarshal([]byte(resultText(t, q)), &resp)
	if len(resp.Memories) != 1 {
		t.Errorf("session b recalled %d memories, want 1 (cross-session user recall)", len(resp.Memories))
	}
}

func TestMemoryQueryIsUserScoped(t *testing.T) {
	srv, store, _ := newMCPForMemory(t)
	seedMemorySession(t, store, "sess_alice", "alice")
	seedMemorySession(t, store, "sess_bob", "bob")

	call(t, srv.Handler(), "lenny/memory_write",
		`{"sessionId":"sess_alice","content":"alice private note"}`)
	q := call(t, srv.Handler(), "lenny/memory_query", `{"sessionId":"sess_bob","query":"private"}`)
	var resp struct {
		Memories []json.RawMessage `json:"memories"`
	}
	_ = json.Unmarshal([]byte(resultText(t, q)), &resp)
	if len(resp.Memories) != 0 {
		t.Error("bob's memory query returned alice's memory")
	}
}

func TestMemoryWriteRejectsMissingContent(t *testing.T) {
	srv, store, _ := newMCPForMemory(t)
	seedMemorySession(t, store, "sess_1", "alice")
	resp := call(t, srv.Handler(), "lenny/memory_write", `{"sessionId":"sess_1"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("memory_write with no content should error: %+v", resp)
	}
}

func TestMemoryWriteUnknownSession(t *testing.T) {
	srv, _, _ := newMCPForMemory(t)
	resp := call(t, srv.Handler(), "lenny/memory_write",
		`{"sessionId":"ghost","content":"x"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("memory_write against an unknown session should error: %+v", resp)
	}
}
