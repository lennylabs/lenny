// SPDX-License-Identifier: MIT

package translator

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// newContinuity builds a continuity helper over fresh in-memory stores.
func newContinuity() (*continuity, sessionstore.Store, transcriptstore.Store) {
	sessions := memstore.New()
	transcripts := transcriptstore.NewMemory()
	return &continuity{sessions: sessions, transcripts: transcripts}, sessions, transcripts
}

// seedTurn creates a response session row that chains from parentID (empty for
// a chain root) and records its own single-turn transcript bucket.
func seedTurn(t *testing.T, sessions sessionstore.Store, transcripts transcriptstore.Store,
	tenantID, id, parentID string, entries ...transcriptstore.Entry,
) {
	t.Helper()
	ctx := context.Background()
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: id, TenantID: tenantID, State: session.StateCompleted, RuntimeRef: "echo",
		ContinuationParentID: parentID,
	}); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
	if len(entries) > 0 {
		if err := transcripts.Append(ctx, tenantID, id, entries...); err != nil {
			t.Fatalf("seed transcript %s: %v", id, err)
		}
	}
}

func userMsg(content string) transcriptstore.Entry {
	return transcriptstore.Entry{Role: "user", Content: content}
}

func assistantMsg(content string) transcriptstore.Entry {
	return transcriptstore.Entry{Role: "assistant", Content: content}
}

// spec: §15 (server-side previous_response_id continuation, chain-walk
// rehydration, full-history replay). A continuation walks the
// ContinuationParentID chain across per-response single-turn buckets and
// reassembles the prior conversation root-first, with each bucket holding only
// its own turn (no copy-forward duplication).
func TestContinuityRehydrateChainWalkOrdering(t *testing.T) {
	c, sessions, transcripts := newContinuity()
	const tenant = "acme"

	// Three-turn chain: root -> mid -> ref. Each bucket holds only its turn.
	seedTurn(t, sessions, transcripts, tenant, "root", "",
		userMsg("turn one"), assistantMsg("answer one"))
	seedTurn(t, sessions, transcripts, tenant, "mid", "root",
		userMsg("turn two"), assistantMsg("answer two"))
	seedTurn(t, sessions, transcripts, tenant, "ref", "mid",
		userMsg("turn three"), assistantMsg("answer three"))

	got, err := c.rehydrate(context.Background(), tenant, "ref")
	if err != nil {
		t.Fatalf("rehydrate: %v", err)
	}
	want := []executor.Message{
		{Role: "user", Content: "turn one"},
		{Role: "assistant", Content: "answer one"},
		{Role: "user", Content: "turn two"},
		{Role: "assistant", Content: "answer two"},
		{Role: "user", Content: "turn three"},
		{Role: "assistant", Content: "answer three"},
	}
	assertMessages(t, got, want)

	// Each per-response bucket holds only its own single turn (no copy-forward).
	for _, tc := range []struct {
		id   string
		want int
	}{{"root", 2}, {"mid", 2}, {"ref", 2}} {
		entries, err := transcripts.Get(context.Background(), tenant, tc.id)
		if err != nil {
			t.Fatalf("bucket %s: %v", tc.id, err)
		}
		if len(entries) != tc.want {
			t.Errorf("bucket %s holds %d entries, want %d (copy-forward would balloon)",
				tc.id, len(entries), tc.want)
		}
	}
}

// spec: §15 (fail-closed continuation resolution); §4.2 (session-store tenant
// isolation). An unknown or cross-tenant referenced previous_response_id
// resolves to errContinuationNotFound so the handler fails closed with a 404.
func TestContinuityRehydrateFailsClosed(t *testing.T) {
	c, sessions, transcripts := newContinuity()

	t.Run("unknown referenced id", func(t *testing.T) {
		_, err := c.rehydrate(context.Background(), "acme", "does-not-exist")
		if !errors.Is(err, errContinuationNotFound) {
			t.Fatalf("rehydrate unknown id: got %v, want errContinuationNotFound", err)
		}
	})

	t.Run("cross-tenant referenced id", func(t *testing.T) {
		// A valid response owned by globex must not resolve for acme.
		seedTurn(t, sessions, transcripts, "globex", "globex-resp", "",
			userMsg("secret"), assistantMsg("private"))
		_, err := c.rehydrate(context.Background(), "acme", "globex-resp")
		if !errors.Is(err, errContinuationNotFound) {
			t.Fatalf("rehydrate cross-tenant id: got %v, want errContinuationNotFound", err)
		}
	})
}

// spec: §15 (continuation resolution); transcriptstore ErrNotFound tolerated.
// A referenced response that exists but recorded no transcript rehydrates as
// empty prior history rather than failing closed.
func TestContinuityRehydrateEmptyReferencedTranscript(t *testing.T) {
	c, sessions, transcripts := newContinuity()
	const tenant = "acme"
	seedTurn(t, sessions, transcripts, tenant, "ref", "") // no entries

	got, err := c.rehydrate(context.Background(), tenant, "ref")
	if err != nil {
		t.Fatalf("rehydrate: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("rehydrate empty-transcript ref: got %d messages, want 0", len(got))
	}
}

// spec: §15 (chain-walk rehydration, empty-hop tolerated); transcriptstore
// ErrNotFound tolerated. A mid-chain hop whose transcript bucket is empty
// (its best-effort record failed) contributes no messages, and the walk
// continues to its ancestors so the older history still rehydrates.
func TestContinuityRehydrateMidChainEmptyTranscriptToleratesGap(t *testing.T) {
	c, sessions, transcripts := newContinuity()
	const tenant = "acme"
	seedTurn(t, sessions, transcripts, tenant, "root", "",
		userMsg("turn one"), assistantMsg("answer one"))
	seedTurn(t, sessions, transcripts, tenant, "mid", "root") // session row, empty bucket
	seedTurn(t, sessions, transcripts, tenant, "ref", "mid",
		userMsg("turn three"), assistantMsg("answer three"))

	got, err := c.rehydrate(context.Background(), tenant, "ref")
	if err != nil {
		t.Fatalf("rehydrate: %v", err)
	}
	// Root and ref turns survive in order; the empty middle hop drops out.
	want := []executor.Message{
		{Role: "user", Content: "turn one"},
		{Role: "assistant", Content: "answer one"},
		{Role: "user", Content: "turn three"},
		{Role: "assistant", Content: "answer three"},
	}
	assertMessages(t, got, want)
}

// spec: §15 (chain-walk rehydration, erased-ancestor walk termination); §4.2
// (session-store tenant isolation). A missing mid-chain session row (an erased
// ancestor) ends the walk at the gap, dropping ancestors older than the gap
// while keeping the newer collected turns, distinct from the referenced-id
// fail-closed 404 which fires only when id == prevID.
func TestContinuityRehydrateMissingMidChainSessionTerminatesWalk(t *testing.T) {
	c, sessions, transcripts := newContinuity()
	const tenant = "acme"
	// root exists but the "mid" session row is absent; ref points at mid.
	seedTurn(t, sessions, transcripts, tenant, "root", "",
		userMsg("turn one"), assistantMsg("answer one"))
	seedTurn(t, sessions, transcripts, tenant, "ref", "mid",
		userMsg("turn three"), assistantMsg("answer three"))

	got, err := c.rehydrate(context.Background(), tenant, "ref")
	if err != nil {
		t.Fatalf("rehydrate must not fail closed on a missing ancestor: %v", err)
	}
	// Only the referenced response's own turn survives; the gap severs root.
	want := []executor.Message{
		{Role: "user", Content: "turn three"}, {Role: "assistant", Content: "answer three"},
	}
	assertMessages(t, got, want)
}

// spec: §15 (chain-walk rehydration, cycle guard). A pointer cycle in the
// ContinuationParentID chain terminates the walk rather than looping forever.
func TestContinuityRehydrateCycleGuard(t *testing.T) {
	c, sessions, transcripts := newContinuity()
	const tenant = "acme"
	// a -> b -> a forms a cycle; ref points into it at "a".
	seedTurn(t, sessions, transcripts, tenant, "a", "b", userMsg("a in"), assistantMsg("a out"))
	seedTurn(t, sessions, transcripts, tenant, "b", "a", userMsg("b in"), assistantMsg("b out"))

	got, err := c.rehydrate(context.Background(), tenant, "a")
	if err != nil {
		t.Fatalf("rehydrate with cycle: %v", err)
	}
	// The walk visits each node once (a then b) then breaks; both turns are
	// present exactly once. Order is root-first of the visited prefix (b, a).
	if len(got) != 4 {
		t.Fatalf("cycle walk produced %d messages, want 4 (each node once): %+v", len(got), got)
	}
}

// spec: §15 (continuation lineage persisted); §15.1 (best-effort transcript
// write). record writes only this response's own turn (inbound input by role
// plus assistant text), never copying prior turns forward.
func TestContinuityRecordWritesOwnTurnOnly(t *testing.T) {
	c, _, transcripts := newContinuity()
	const tenant = "acme"
	ctx := context.Background()

	in := []executor.Message{{Role: "user", Content: "hello"}}
	out := []executor.MessagePart{
		{Type: "text", Text: "hi there"},
		{Type: "tool_call", Text: "ignored"}, // non-text parts are excluded
	}
	c.record(ctx, tenant, "resp", in, out)

	got, err := transcripts.Get(ctx, tenant, "resp")
	if err != nil {
		t.Fatalf("Get recorded bucket: %v", err)
	}
	want := []transcriptstore.Entry{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	if len(got) != len(want) {
		t.Fatalf("recorded %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Errorf("entry %d = %+v, want role=%q content=%q", i, got[i], want[i].Role, want[i].Content)
		}
	}
}

// spec: §15.1 (best-effort transcript write). A record with no text output and
// no input is a no-op that writes no bucket, and a nil transcripts store never
// panics.
func TestContinuityRecordBestEffortNoOp(t *testing.T) {
	const tenant = "acme"
	ctx := context.Background()

	c, _, transcripts := newContinuity()
	c.record(ctx, tenant, "resp", nil, []executor.MessagePart{{Type: "tool_call", Text: "x"}})
	if _, err := transcripts.Get(ctx, tenant, "resp"); !errors.Is(err, transcriptstore.ErrNotFound) {
		t.Errorf("empty record wrote a bucket: %v", err)
	}

	// A nil transcripts store makes record and rehydrate no-ops.
	nilC := &continuity{sessions: memstore.New()}
	nilC.record(ctx, tenant, "resp", []executor.Message{{Role: "user", Content: "x"}}, nil)
	got, err := nilC.rehydrate(ctx, tenant, "anything")
	if err != nil || got != nil {
		t.Errorf("nil-store rehydrate: got (%v, %v), want (nil, nil)", got, err)
	}
}

func assertMessages(t *testing.T, got, want []executor.Message) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Errorf("message %d = {%q, %q}, want {%q, %q}",
				i, got[i].Role, got[i].Content, want[i].Role, want[i].Content)
		}
	}
}
