// SPDX-License-Identifier: MIT

//go:build security

package tier9_security_test

import (
	"bytes"
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/testinfra/responsesstub"
)

// orCountingForwarder reports every turn local, as the session server's
// forwarder does on the coordinating replica, and counts each call so a test
// can assert that a rejected id reached neither forward decision.
type orCountingForwarder struct {
	forwards  atomic.Int64
	forwarded atomic.Int64
}

func (f *orCountingForwarder) ForwardResponsesTurn(http.ResponseWriter, *http.Request, sessionstore.Session, []byte,
	func(http.ResponseWriter, string),
) bool {
	f.forwards.Add(1)
	return true
}

func (f *orCountingForwarder) Forwarded(context.Context) bool {
	f.forwarded.Add(1)
	return false
}

// newHeldORFixture builds the handler over a binder that holds every session
// it binds, so each first turn leaves a running session whose newest
// response a continuation would serve warm.
func newHeldORFixture(t *testing.T) (*orFixture, *orHoldingBinder, *orCountingForwarder) {
	t.Helper()
	store := memstore.New()
	exec := &orRecordingExecutor{}
	binder := &orHoldingBinder{store: store}
	fwd := &orCountingForwarder{}
	f := &orFixture{sessions: store, transcripts: transcriptstore.NewMemory(), exec: exec}
	f.handler = translator.NewOpenResponsesHandler(store, exec, translator.OpenResponsesOptions{
		HolderForwarder:  fwd,
		Terminal:         responsesstub.ReleasingTerminal{Exec: exec, Store: store},
		DevTenantHeader:  true,
		SingleShotBinder: binder,
		Transcripts:      f.transcripts,
	}).Handler()
	return f, binder, fwd
}

// binds reports how many sessions the binder created.
func (b *orHoldingBinder) binds() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.users)
}

// spec: §15 (Open Responses conversation sessions), §4.2 (session-store tenant isolation), §29.3 (Interactive message send) Forwarding
// diagnosis: a previous_response_id or GET naming another tenant's held
// session, or a held session whose row was erased, answered other than the
// byte-identical native 404 an unknown id gets, or reached the binder or the
// holder forwarder before failing. Resolution precedes every bind and
// forward, and it reads only the caller's tenant, so a rejected id has no
// side effect and reveals nothing about the other tenant's conversation.
// Check resolve in pkg/gateway/environment/translator/continuity.go and the
// order in handleCreate in open_responses.go.
func TestRejectedHeldResponseIDMakesNoBindAndNoForward_spec_15(t *testing.T) {
	f, binder, fwd := newHeldORFixture(t)
	owned := f.post(t, "globex", "globex secret", "")
	held := orSessionOf(t, owned)
	erased := f.post(t, "acme", "acme turn", "")
	if err := f.sessions.Delete(context.Background(), "acme", orSessionOf(t, erased)); err != nil {
		t.Fatal(err)
	}
	unknown := held + "0.1"
	_, unknownBody := f.do(t, http.MethodPost, "/v1/responses", "acme",
		map[string]any{"model": "echo", "input": "probe", "previous_response_id": unknown})
	binds, sends := binder.binds(), f.exec.sends()
	forwards, forwarded := fwd.forwards.Load(), fwd.forwarded.Load()

	for _, tc := range []struct{ name, id string }{
		{"cross-tenant held session", owned},
		{"erased held session", erased},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := f.do(t, http.MethodPost, "/v1/responses", "acme",
				map[string]any{"model": "echo", "input": "probe", "previous_response_id": tc.id})
			if code != http.StatusNotFound || !bytes.Equal(body, unknownBody) {
				t.Errorf("continuation = %d %s, want the unknown id's 404 %s", code, body, unknownBody)
			}
			if code, _ := f.do(t, http.MethodGet, "/v1/responses/"+tc.id, "acme", nil); code != http.StatusNotFound {
				t.Errorf("GET = %d, want 404", code)
			}
			if bytes.Contains(body, []byte("globex secret")) {
				t.Errorf("rejection leaked the owner's input: %s", body)
			}
		})
	}
	if got := binder.binds() - binds; got != 0 {
		t.Errorf("rejected ids bound %d sessions, want none", got)
	}
	if got := f.exec.sends() - sends; got != 0 {
		t.Errorf("rejected ids dispatched %d turns, want none", got)
	}
	if got := fwd.forwards.Load() - forwards; got != 0 {
		t.Errorf("rejected ids reached the holder forwarder %d times, want none", got)
	}
	if got := fwd.forwarded.Load() - forwarded; got != 0 {
		t.Errorf("rejected ids asked whether the request was forwarded %d times, want none", got)
	}
	row, err := f.sessions.Get(context.Background(), "globex", held)
	if err != nil {
		t.Fatal(err)
	}
	if len(row.Responses) != 1 {
		t.Errorf("the other tenant's held session carries %d response records, want only its first", len(row.Responses))
	}
}
