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

// seedConversation creates session id continuing from parentID (empty for the
// first session of a conversation) and appends each turn to the session's
// transcript, recording one completed response per turn whose EndSeq is the
// Seq Append returned for that turn. A nil turn records a completed response
// with no transcript entries (EndSeq 0).
func seedConversation(t *testing.T, sessions sessionstore.Store, transcripts transcriptstore.Store,
	tenantID, id, parentID string, turns ...[]transcriptstore.Entry,
) {
	t.Helper()
	ctx := context.Background()
	var responses []sessionstore.ConversationResponse
	for _, turn := range turns {
		var endSeq uint64
		if len(turn) > 0 {
			var err error
			if endSeq, err = transcripts.Append(ctx, tenantID, id, turn...); err != nil {
				t.Fatalf("seed transcript %s: %v", id, err)
			}
		}
		responses = append(responses, sessionstore.ConversationResponse{EndSeq: endSeq, Completed: true})
	}
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: id, TenantID: tenantID, State: session.StateRunning, RuntimeRef: "echo",
		ContinuationParentID: parentID, Responses: responses,
	}); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
}

func turn(entries ...transcriptstore.Entry) []transcriptstore.Entry { return entries }

func userMsg(content string) transcriptstore.Entry {
	return transcriptstore.Entry{Role: "user", Content: content}
}

func assistantMsg(content string) transcriptstore.Entry {
	return transcriptstore.Entry{Role: "assistant", Content: content}
}

// TestContinuityRehydrateWalksLineageRootFirst_spec_15 pins the conversation
// context of a response: its session's transcript up to the response's end
// sequence, preceded by the context of the response the session's first turn
// continued from, back to the conversation's first session. The walk reads
// the parent only up to the named ordinal's end sequence, so a later turn on
// the parent session (a fork point) stays out of the child's context.
//
// spec: §15 (Open Responses conversation sessions)
func TestContinuityRehydrateWalksLineageRootFirst_spec_15(t *testing.T) {
	c, sessions, transcripts := newContinuity()
	const tenant = "acme"

	// root holds two turns; mid forks from root.1, so root's second turn is
	// not part of mid's context. ref continues from mid.2.
	seedConversation(t, sessions, transcripts, tenant, "root", "",
		turn(userMsg("one"), assistantMsg("answer one")),
		turn(userMsg("root later"), assistantMsg("root later answer")))
	seedConversation(t, sessions, transcripts, tenant, "mid", "root.1",
		turn(userMsg("two"), assistantMsg("answer two")),
		turn(userMsg("three"), assistantMsg("answer three")))
	seedConversation(t, sessions, transcripts, tenant, "ref", "mid.2",
		turn(userMsg("four"), assistantMsg("answer four")))

	got, err := c.rehydrate(context.Background(), tenant, "ref.1")
	if err != nil {
		t.Fatalf("rehydrate: %v", err)
	}
	assertMessages(t, got, []executor.Message{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "answer one"},
		{Role: "user", Content: "two"},
		{Role: "assistant", Content: "answer two"},
		{Role: "user", Content: "three"},
		{Role: "assistant", Content: "answer three"},
		{Role: "user", Content: "four"},
		{Role: "assistant", Content: "answer four"},
	})

	got, err = c.rehydrate(context.Background(), tenant, "mid.1")
	if err != nil {
		t.Fatalf("rehydrate mid.1: %v", err)
	}
	assertMessages(t, got, []executor.Message{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "answer one"},
		{Role: "user", Content: "two"},
		{Role: "assistant", Content: "answer two"},
	})
}

// TestContinuityRehydrateBoundsByEndSeqWithInterleavedEntries_spec_15 pins the
// end-sequence bound: entries a REST delivery appended to the session between
// two turns fall after the first response's end sequence and before the
// second's, so they appear in the context of S.2 and not of S.1. A per-turn
// entry count would misplace them.
//
// spec: §15 (Open Responses conversation sessions)
func TestContinuityRehydrateBoundsByEndSeqWithInterleavedEntries_spec_15(t *testing.T) {
	c, sessions, transcripts := newContinuity()
	const tenant = "acme"
	ctx := context.Background()

	end1, err := transcripts.Append(ctx, tenant, "S", userMsg("one"), assistantMsg("answer one"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transcripts.Append(ctx, tenant, "S", userMsg("injected over REST")); err != nil {
		t.Fatal(err)
	}
	end2, err := transcripts.Append(ctx, tenant, "S", userMsg("two"), assistantMsg("answer two"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transcripts.Append(ctx, tenant, "S", userMsg("after the head")); err != nil {
		t.Fatal(err)
	}
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: "S", TenantID: tenant, State: session.StateRunning,
		Responses: []sessionstore.ConversationResponse{
			{EndSeq: end1, Completed: true}, {EndSeq: end2, Completed: true},
		},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := c.rehydrate(ctx, tenant, "S.1")
	if err != nil {
		t.Fatalf("rehydrate S.1: %v", err)
	}
	assertMessages(t, got, []executor.Message{
		{Role: "user", Content: "one"}, {Role: "assistant", Content: "answer one"},
	})
	got, err = c.rehydrate(ctx, tenant, "S.2")
	if err != nil {
		t.Fatalf("rehydrate S.2: %v", err)
	}
	assertMessages(t, got, []executor.Message{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "answer one"},
		{Role: "user", Content: "injected over REST"},
		{Role: "user", Content: "two"},
		{Role: "assistant", Content: "answer two"},
	})
}

// TestContinuityResolveFailsClosed_spec_15 pins first-hop resolution: an id
// that names no completed response of a session in the caller's tenant
// resolves to errContinuationNotFound, so a continuation and a GET answer the
// native 404. A resolvable id returns the row and the ordinal.
//
// spec: §15 (Open Responses conversation sessions); §4.2 (session-store
// tenant isolation)
func TestContinuityResolveFailsClosed_spec_15(t *testing.T) {
	c, sessions, transcripts := newContinuity()
	ctx := context.Background()
	seedConversation(t, sessions, transcripts, "acme", "S", "",
		turn(userMsg("one"), assistantMsg("answer one")))
	seedConversation(t, sessions, transcripts, "globex", "G", "",
		turn(userMsg("secret"), assistantMsg("private")))
	if _, err := sessions.Update(ctx, "acme", "S", func(s *sessionstore.Session) error {
		s.Responses = append(s.Responses, sessionstore.ConversationResponse{})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"missing.1", "G.1", "S", "S.0", "S.x", "S.3", "S.2"} {
		t.Run(id, func(t *testing.T) {
			if _, _, err := c.resolve(ctx, "acme", id); !errors.Is(err, errContinuationNotFound) {
				t.Errorf("resolve(%q) = %v, want errContinuationNotFound", id, err)
			}
			if _, err := c.rehydrate(ctx, "acme", id); !errors.Is(err, errContinuationNotFound) {
				t.Errorf("rehydrate(%q) = %v, want errContinuationNotFound", id, err)
			}
		})
	}

	row, n, err := c.resolve(ctx, "acme", "S.1")
	if err != nil || row.ID != "S" || n != 1 {
		t.Fatalf("resolve(S.1) = (%q, %d, %v), want (S, 1, nil)", row.ID, n, err)
	}
}

// TestContinuityResolveStoreErrorIsNotNotFound_spec_15 pins that an
// infrastructure failure reading the session is returned wrapped rather than
// folded into errContinuationNotFound, so the handler answers 500 rather than
// a 404 that would claim the response does not exist.
//
// spec: §15 (Open Responses conversation sessions)
func TestContinuityResolveStoreErrorIsNotNotFound_spec_15(t *testing.T) {
	boom := errors.New("store unavailable")
	c := &continuity{sessions: failingGetStore{Store: memstore.New(), err: boom}}
	_, _, err := c.resolve(context.Background(), "acme", "S.1")
	if !errors.Is(err, boom) || errors.Is(err, errContinuationNotFound) {
		t.Fatalf("resolve store error = %v, want wrapped %v", err, boom)
	}
}

// failingGetStore fails both the read-pool Get and the primary read with err.
type failingGetStore struct {
	sessionstore.Store
	err error
}

func (f failingGetStore) Get(context.Context, string, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, f.err
}

func (f failingGetStore) GetPrimary(context.Context, string, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, f.err
}

// TestContinuityRehydrateErasedTranscriptContributesNothing_spec_12_8 pins
// that a hop whose transcript was erased under a surviving row contributes no
// entries and the walk continues to the parent, and that a referenced
// response with no entries rehydrates as empty context.
//
// spec: §15 (Open Responses conversation sessions); §12.8 (erasure)
func TestContinuityRehydrateErasedTranscriptContributesNothing_spec_12_8(t *testing.T) {
	c, sessions, transcripts := newContinuity()
	const tenant = "acme"
	seedConversation(t, sessions, transcripts, tenant, "root", "",
		turn(userMsg("one"), assistantMsg("answer one")))
	seedConversation(t, sessions, transcripts, tenant, "mid", "root.1", nil)
	seedConversation(t, sessions, transcripts, tenant, "ref", "mid.1",
		turn(userMsg("three"), assistantMsg("answer three")))

	got, err := c.rehydrate(context.Background(), tenant, "ref.1")
	if err != nil {
		t.Fatalf("rehydrate: %v", err)
	}
	assertMessages(t, got, []executor.Message{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "answer one"},
		{Role: "user", Content: "three"},
		{Role: "assistant", Content: "answer three"},
	})

	seedConversation(t, sessions, transcripts, tenant, "empty", "", nil)
	got, err = c.rehydrate(context.Background(), tenant, "empty.1")
	if err != nil || len(got) != 0 {
		t.Fatalf("rehydrate empty.1 = (%+v, %v), want empty context", got, err)
	}
}

// TestContinuityRehydrateStopsAtLineageGap_spec_15 pins that a later hop that
// cannot be resolved (an erased ancestor session, an unparsable parent id, an
// ordinal past the parent's newest response, or a parent response that never
// completed) ends the walk at the gap and keeps the newer context, distinct
// from the first-hop 404.
//
// spec: §15 (Open Responses conversation sessions); §12.8 (erasure)
func TestContinuityRehydrateStopsAtLineageGap_spec_15(t *testing.T) {
	for _, tc := range []struct {
		name   string
		parent string
	}{
		{"erased ancestor session", "gone.1"},
		{"unparsable parent", "root"},
		{"ordinal past the newest", "root.5"},
		{"parent response not completed", "root.2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, sessions, transcripts := newContinuity()
			const tenant = "acme"
			seedConversation(t, sessions, transcripts, tenant, "root", "",
				turn(userMsg("one"), assistantMsg("answer one")))
			if _, err := sessions.Update(context.Background(), tenant, "root", func(s *sessionstore.Session) error {
				s.Responses = append(s.Responses, sessionstore.ConversationResponse{})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			seedConversation(t, sessions, transcripts, tenant, "ref", tc.parent,
				turn(userMsg("three"), assistantMsg("answer three")))

			got, err := c.rehydrate(context.Background(), tenant, "ref.1")
			if err != nil {
				t.Fatalf("rehydrate must not fail closed on a later-hop gap: %v", err)
			}
			assertMessages(t, got, []executor.Message{
				{Role: "user", Content: "three"}, {Role: "assistant", Content: "answer three"},
			})
		})
	}
}

// TestContinuityRehydrateVisitedGuardKeyedBySession_spec_15 pins the visited
// guard: a lineage that returns to a session already walked ends there, even
// when it names a different ordinal of that session, so a pointer cycle never
// loops and a session's entries are never repeated.
//
// spec: §15 (Open Responses conversation sessions)
func TestContinuityRehydrateVisitedGuardKeyedBySession_spec_15(t *testing.T) {
	c, sessions, transcripts := newContinuity()
	const tenant = "acme"
	seedConversation(t, sessions, transcripts, tenant, "a", "b.1",
		turn(userMsg("a in"), assistantMsg("a out")),
		turn(userMsg("a2 in"), assistantMsg("a2 out")))
	seedConversation(t, sessions, transcripts, tenant, "b", "a.2",
		turn(userMsg("b in"), assistantMsg("b out")))

	got, err := c.rehydrate(context.Background(), tenant, "a.1")
	if err != nil {
		t.Fatalf("rehydrate with cycle: %v", err)
	}
	assertMessages(t, got, []executor.Message{
		{Role: "user", Content: "b in"},
		{Role: "assistant", Content: "b out"},
		{Role: "user", Content: "a in"},
		{Role: "assistant", Content: "a out"},
	})
}

// TestContinuityRecordReturnsEndSeq_spec_15 pins that record appends only the
// turn's own input and text output and returns the Seq Append assigned to its
// last entry, which is the response's end sequence, across successive turns
// on one session.
//
// spec: §15 (Open Responses conversation sessions); §15.1 (session transcript)
func TestContinuityRecordReturnsEndSeq_spec_15(t *testing.T) {
	c, _, transcripts := newContinuity()
	const tenant = "acme"
	ctx := context.Background()

	in := []executor.Message{{Role: "user", Content: "hello"}}
	out := []executor.MessagePart{
		{Type: "text", Text: "hi there"},
		{Type: "tool_call", Text: "ignored"},
	}
	end1, err := c.record(ctx, tenant, "S", in, out)
	if err != nil || end1 != 2 {
		t.Fatalf("first record = (%d, %v), want (2, nil)", end1, err)
	}
	end2, err := c.record(ctx, tenant, "S", in, out)
	if err != nil || end2 != 4 {
		t.Fatalf("second record = (%d, %v), want (4, nil)", end2, err)
	}

	got, err := transcripts.Get(ctx, tenant, "S")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 4 || got[0].Content != "hello" || got[1].Content != "hi there" || got[1].Role != "assistant" {
		t.Errorf("recorded entries: %+v", got)
	}
	if got[len(got)-1].Seq != end2 {
		t.Errorf("end sequence %d is not the last appended Seq %d", end2, got[len(got)-1].Seq)
	}
}

// TestContinuityRecordIsRequired_spec_15 pins that a transcript append error
// is returned to the caller rather than swallowed, so the turn fails instead
// of completing a response with no transcript entries. A turn with nothing
// to record, and a helper with no transcript store, return end sequence 0
// with no error.
//
// spec: §15 (Open Responses conversation sessions)
func TestContinuityRecordIsRequired_spec_15(t *testing.T) {
	ctx := context.Background()
	in := []executor.Message{{Role: "user", Content: "x"}}

	failing := &continuity{sessions: memstore.New(), transcripts: appendErrTranscripts{Store: transcriptstore.NewMemory()}}
	if end, err := failing.record(ctx, "acme", "S", in, nil); err == nil || end != 0 {
		t.Fatalf("record over a failing Append = (%d, %v), want (0, error)", end, err)
	}

	c, _, transcripts := newContinuity()
	end, err := c.record(ctx, "acme", "S", nil, []executor.MessagePart{{Type: "tool_call", Text: "x"}})
	if err != nil || end != 0 {
		t.Fatalf("empty record = (%d, %v), want (0, nil)", end, err)
	}
	if _, err := transcripts.Get(ctx, "acme", "S"); !errors.Is(err, transcriptstore.ErrNotFound) {
		t.Errorf("empty record wrote a transcript: %v", err)
	}

	nilC := &continuity{sessions: memstore.New()}
	if end, err := nilC.record(ctx, "acme", "S", in, nil); err != nil || end != 0 {
		t.Fatalf("nil-store record = (%d, %v), want (0, nil)", end, err)
	}
	// Without a transcript store, resolution still reads the session store
	// and fails closed on an unknown id.
	if _, err := nilC.rehydrate(ctx, "acme", "missing.1"); !errors.Is(err, errContinuationNotFound) {
		t.Errorf("nil-store rehydrate of an unknown id = %v, want errContinuationNotFound", err)
	}
}

type appendErrTranscripts struct {
	transcriptstore.Store
}

func (appendErrTranscripts) Append(context.Context, string, string, ...transcriptstore.Entry) (uint64, error) {
	return 0, errors.New("append failed")
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
