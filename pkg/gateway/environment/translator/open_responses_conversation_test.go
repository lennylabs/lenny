// SPDX-License-Identifier: MIT

package translator_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// convBinder creates a running session row for each bind, named sess_1,
// sess_2, ..., with the recovery generation gen, and reports hold. When
// rewriteRuntime is set the row's RuntimeRef is rewritten to it, as §10.7
// experiment routing rewrites it.
type convBinder struct {
	store          sessionstore.Store
	hold           bool
	gen            int64
	rewriteRuntime string

	mu    sync.Mutex
	binds int
}

func (b *convBinder) BindSingleShot(ctx context.Context, spec translator.SingleShotSpec) (translator.SingleShotBinding, error) {
	b.mu.Lock()
	b.binds++
	id := fmt.Sprintf("sess_%d", b.binds)
	b.mu.Unlock()
	rt := spec.RuntimeRef
	if b.rewriteRuntime != "" {
		rt = b.rewriteRuntime
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := b.store.Create(ctx, sessionstore.Session{
		ID: id, TenantID: spec.TenantID, UserID: spec.UserID, RuntimeRef: rt, Environment: spec.Environment,
		State: session.StateRunning, RecoveryGeneration: b.gen, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return translator.SingleShotBinding{}, err
	}
	return translator.SingleShotBinding{SessionID: id, RecoveryGeneration: b.gen, Hold: b.hold}, nil
}

func (b *convBinder) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.binds
}

// sendCall is one Send the scripted executor received.
type sendCall struct {
	sessionID string
	msgs      []executor.Message
}

// convExecutor answers each Send with "replyN" unless hook returns an error
// for that call, and records every release disposition.
type convExecutor struct {
	mu    sync.Mutex
	calls []sendCall
	disps []executor.Disposition
	// hook runs before Send answers; a non-nil error is Send's error.
	hook func(ctx context.Context, call int, sessionID string) error
}

func (e *convExecutor) Send(ctx context.Context, sessionID string, msgs []executor.Message) (executor.Response, error) {
	e.mu.Lock()
	e.calls = append(e.calls, sendCall{sessionID: sessionID, msgs: append([]executor.Message(nil), msgs...)})
	n := len(e.calls)
	hook := e.hook
	e.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, n, sessionID); err != nil {
			return executor.Response{}, err
		}
	}
	return executor.Response{Parts: []executor.MessagePart{{Type: "text", Text: fmt.Sprintf("reply%d", n)}}}, nil
}

func (e *convExecutor) Close(context.Context, string) error { return nil }

func (e *convExecutor) Release(_ context.Context, _ string, d executor.Disposition) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.disps = append(e.disps, d)
	return nil
}

func (e *convExecutor) releases() []executor.Disposition {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]executor.Disposition(nil), e.disps...)
}

func (e *convExecutor) call(t *testing.T, i int) sendCall {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if i >= len(e.calls) {
		t.Fatalf("executor received %d Send calls, want at least %d", len(e.calls), i+1)
	}
	return e.calls[i]
}

// stubTerminal records the terminal claims and post-terminal hook calls of
// endHeldSession. Its hook releases nothing, so an executor release a test
// observes was made by the handler itself.
type stubTerminal struct {
	mu        sync.Mutex
	begins    int
	ends      int
	hooks     []sessionstore.Session
	hookFrom  []session.State
	hookCtxOK []bool
}

func (s *stubTerminal) BeginTerminal(string) func() {
	s.mu.Lock()
	s.begins++
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		s.ends++
		s.mu.Unlock()
	}
}

func (s *stubTerminal) AfterTerminalWrite(ctx context.Context, from session.State, sess sessionstore.Session,
	_ func(context.Context, sessionstore.Session),
) sessionstore.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks = append(s.hooks, sess)
	s.hookFrom = append(s.hookFrom, from)
	s.hookCtxOK = append(s.hookCtxOK, ctx.Err() == nil)
	return sess
}

func (s *stubTerminal) snapshot() (begins, ends, hooks int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.begins, s.ends, len(s.hooks)
}

// stubForwarder is a scripted HolderForwarder. forwarded is what Forwarded
// reports; local is ForwardResponsesTurn's answer. When refuseState is set
// and local is false, it calls refuse with that state; when writeStatus is
// set and local is false, it writes that status itself.
type stubForwarder struct {
	forwarded   bool
	local       bool
	refuseState string
	writeStatus int

	mu    sync.Mutex
	calls int
	// onCall runs at each ForwardResponsesTurn call.
	onCall func()
}

func (f *stubForwarder) ForwardResponsesTurn(w http.ResponseWriter, _ *http.Request, _ sessionstore.Session, _ []byte,
	refuse func(http.ResponseWriter, string),
) bool {
	f.mu.Lock()
	f.calls++
	onCall := f.onCall
	f.mu.Unlock()
	if onCall != nil {
		onCall()
	}
	if f.local {
		return true
	}
	if f.refuseState != "" {
		refuse(w, f.refuseState)
	}
	if f.writeStatus != 0 {
		w.WriteHeader(f.writeStatus)
		_, _ = w.Write([]byte(`{"from":"coordinator"}`))
	}
	return false
}

func (f *stubForwarder) Forwarded(context.Context) bool { return f.forwarded }

func (f *stubForwarder) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// probeStore wraps a session store, counts Update calls, records whether each
// Update's context was live, and fails the Updates whose 1-based index
// failUpdate reports true for. before, when set, runs ahead of each Update.
type probeStore struct {
	sessionstore.Store

	mu         sync.Mutex
	updates    int
	updateCtx  []bool
	failUpdate func(n int) bool
	before     func(n int)
}

func (p *probeStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	p.mu.Lock()
	p.updates++
	n := p.updates
	p.updateCtx = append(p.updateCtx, ctx.Err() == nil)
	fail, before := p.failUpdate, p.before
	p.mu.Unlock()
	if before != nil {
		before(n)
	}
	if fail != nil && fail(n) {
		return sessionstore.Session{}, errors.New("session store unavailable")
	}
	return p.Store.Update(ctx, tenantID, id, mutate)
}

func (p *probeStore) updateCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.updates
}

func (p *probeStore) lastUpdateCtxLive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.updateCtx) > 0 && p.updateCtx[len(p.updateCtx)-1]
}

// convFixture is one Open Responses handler with its scripted dependencies.
type convFixture struct {
	inner     sessionstore.Store
	store     *probeStore
	binder    *convBinder
	exec      *convExecutor
	terminal  *stubTerminal
	forwarder *stubForwarder
	ts        transcriptstore.Store
	h         *translator.OpenResponsesHandler
}

// convOptions adjusts a fixture before its handler is built.
type convOptions struct {
	hold           bool
	rewriteRuntime string
	transcripts    transcriptstore.Store
}

func newConvFixture(t *testing.T, o convOptions) *convFixture {
	t.Helper()
	inner := memstore.New()
	f := &convFixture{
		inner:     inner,
		store:     &probeStore{Store: inner},
		exec:      &convExecutor{},
		terminal:  &stubTerminal{},
		forwarder: &stubForwarder{local: true},
		ts:        o.transcripts,
	}
	if f.ts == nil {
		f.ts = transcriptstore.NewMemory()
	}
	f.binder = &convBinder{store: inner, hold: o.hold, rewriteRuntime: o.rewriteRuntime}
	f.h = translator.NewOpenResponsesHandler(f.store, f.exec, translator.OpenResponsesOptions{
		DevTenantHeader:  true,
		Clock:            func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		SingleShotBinder: f.binder,
		Transcripts:      f.ts,
		HolderForwarder:  f.forwarder,
		Terminal:         f.terminal,
		ReleaseTimeout:   time.Second,
	})
	return f
}

// post sends a POST /v1/responses with body on ctx as tenant acme.
func (f *convFixture) post(t *testing.T, ctx context.Context, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(buf)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	f.h.Handler().ServeHTTP(rr, req)
	return rr
}

// turn posts input continuing prev (empty for a root request) as user alice
// on the echo runtime and returns the recorder.
func (f *convFixture) turn(t *testing.T, prev, input string) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{"model": "echo", "input": input, "user": "alice"}
	if prev != "" {
		body["previous_response_id"] = prev
	}
	return f.post(t, context.Background(), body)
}

// mustTurn runs turn and fails unless it answered 200; it returns the
// response id.
func (f *convFixture) mustTurn(t *testing.T, prev, input string) string {
	t.Helper()
	rr := f.turn(t, prev, input)
	if rr.Code != http.StatusOK {
		t.Fatalf("turn %q after %q: status %d, body %s", input, prev, rr.Code, rr.Body.String())
	}
	var resp translator.OpenResponsesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.ID
}

func (f *convFixture) row(t *testing.T, id string) sessionstore.Session {
	t.Helper()
	row, err := f.inner.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return row
}

// mutate applies m to the stored row id.
func (f *convFixture) mutate(t *testing.T, id string, m func(*sessionstore.Session)) {
	t.Helper()
	if _, err := f.inner.Update(context.Background(), "acme", id, func(s *sessionstore.Session) error {
		m(s)
		return nil
	}); err != nil {
		t.Fatalf("mutate %s: %v", id, err)
	}
}

// TestOpenResponsesHeldSessionServesWarmTurns_spec_15 pins the held session:
// a cold turn on a binder that holds leaves the row running with no release
// and answers S.1, and a continuation of S.1 runs on the same session with no
// second bind, sends only the new input, and answers S.2, in both the JSON and
// the stream form. In the stream form the response.created and
// response.completed events carry the response id, and the warm turn's
// response.completed carries previous_response_id S.1.
//
// spec: §15 (Open Responses conversation sessions)
func TestOpenResponsesHeldSessionServesWarmTurns_spec_15(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			f := newConvFixture(t, convOptions{hold: true})
			post := func(prev, input string) *httptest.ResponseRecorder {
				body := map[string]any{"model": "echo", "input": input, "user": "alice", "stream": stream}
				if prev != "" {
					body["previous_response_id"] = prev
				}
				rr := f.post(t, context.Background(), body)
				if rr.Code != http.StatusOK {
					t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
				}
				return rr
			}
			idOf := func(rr *httptest.ResponseRecorder) string {
				if stream {
					created := streamEventResponseID(t, rr.Body.String(), "response.created")
					completed := streamEventResponseID(t, rr.Body.String(), "response.completed")
					if created != completed {
						t.Errorf("response.created id %q != response.completed id %q", created, completed)
					}
					return completed
				}
				var resp translator.OpenResponsesResponse
				_ = json.Unmarshal(rr.Body.Bytes(), &resp)
				return resp.ID
			}

			first := post("", "one")
			if got := idOf(first); got != "sess_1.1" {
				t.Fatalf("first response id = %q, want sess_1.1", got)
			}
			if row := f.row(t, "sess_1"); row.State != session.StateRunning {
				t.Errorf("held session state = %q, want running", row.State)
			}
			if got := f.exec.releases(); len(got) != 0 {
				t.Errorf("held session released: %v", got)
			}

			second := post("sess_1.1", "two")
			if got := idOf(second); got != "sess_1.2" {
				t.Fatalf("warm response id = %q, want sess_1.2", got)
			}
			if stream {
				if prev := streamCompletedPrevious(t, second.Body.String()); prev != "sess_1.1" {
					t.Errorf("warm response.completed previous_response_id = %q, want sess_1.1", prev)
				}
			}
			if f.binder.count() != 1 {
				t.Errorf("binds = %d, want 1 (the warm turn binds nothing)", f.binder.count())
			}
			warm := f.exec.call(t, 1)
			if warm.sessionID != "sess_1" {
				t.Errorf("warm turn sent to %q, want sess_1", warm.sessionID)
			}
			assertMessages(t, warm.msgs, []executor.Message{{Role: "user", Content: "two"}})
			row := f.row(t, "sess_1")
			if len(row.Responses) != 2 || !row.Responses[1].Completed || row.State != session.StateRunning {
				t.Errorf("row after warm turn: state %q, responses %+v; want running with two completed", row.State, row.Responses)
			}
			if got := f.exec.releases(); len(got) != 0 {
				t.Errorf("held session released after the warm turn: %v", got)
			}
		})
	}
}

// streamCompletedPrevious returns the previous_response_id of the stream's
// response.completed event.
func streamCompletedPrevious(t *testing.T, body string) string {
	t.Helper()
	for _, block := range strings.Split(body, "\n\n") {
		if !strings.Contains(block, "event: response.completed") {
			continue
		}
		for _, line := range strings.Split(block, "\n") {
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				var ev struct {
					Response struct {
						PreviousResponseID string `json:"previous_response_id"`
					} `json:"response"`
				}
				if err := json.Unmarshal([]byte(data), &ev); err != nil {
					t.Fatalf("decode response.completed: %v", err)
				}
				return ev.Response.PreviousResponseID
			}
		}
	}
	t.Fatal("no response.completed event")
	return ""
}

// TestOpenResponsesSessionNotHeldIsReleased_spec_15 pins the release: a cold
// turn on a binder that does not hold writes the session completed, releases
// its binding once with the completed disposition, and a continuation of its
// response goes cold onto a new session.
//
// spec: §15 (Open Responses conversation sessions)
func TestOpenResponsesSessionNotHeldIsReleased_spec_15(t *testing.T) {
	f := newConvFixture(t, convOptions{hold: false})
	id := f.mustTurn(t, "", "one")
	if row := f.row(t, "sess_1"); row.State != session.StateCompleted {
		t.Errorf("state = %q, want completed", row.State)
	}
	if got := f.exec.releases(); len(got) != 1 || got[0] != executor.DispositionCompleted {
		t.Errorf("releases = %v, want one DispositionCompleted", got)
	}
	if next := f.mustTurn(t, id, "two"); next != "sess_2.1" {
		t.Errorf("continuation id = %q, want sess_2.1 on a new session", next)
	}
	if f.binder.count() != 2 {
		t.Errorf("binds = %d, want 2", f.binder.count())
	}
	if row := f.row(t, "sess_2"); row.ContinuationParentID != id || row.RequestedRuntimeRef != "echo" {
		t.Errorf("cold row: parent %q, requested runtime %q; want %q and echo", row.ContinuationParentID, row.RequestedRuntimeRef, id)
	}
}

// TestOpenResponsesWarmConditionsGoCold_spec_15 breaks each warm condition in
// turn and asserts the continuation of S.1 goes cold: a second bind, a new
// session whose ContinuationParentID is S.1, and a dispatch that carries the
// rehydrated conversation ahead of the new input.
//
// spec: §15 (Open Responses conversation sessions)
func TestOpenResponsesWarmConditionsGoCold_spec_15(t *testing.T) {
	cases := []struct {
		name     string
		breakRow func(*sessionstore.Session)
		body     map[string]any
	}{
		{name: "suspended", breakRow: func(s *sessionstore.Session) { s.State = session.StateSuspended }},
		{name: "resume_pending", breakRow: func(s *sessionstore.Session) { s.State = session.StateResumePending }},
		{name: "awaiting_client_action", breakRow: func(s *sessionstore.Session) { s.State = session.StateAwaitingClientAction }},
		{name: "completed", breakRow: func(s *sessionstore.Session) { s.State = session.StateCompleted }},
		{name: "not the newest response", breakRow: func(s *sessionstore.Session) {
			s.Responses = append(s.Responses, sessionstore.ConversationResponse{EndSeq: 99, Completed: true})
		}},
		{name: "newest response reserved and not completed", breakRow: func(s *sessionstore.Session) {
			s.Responses = append(s.Responses, sessionstore.ConversationResponse{})
		}},
		{name: "recovery generation advanced", breakRow: func(s *sessionstore.Session) { s.RecoveryGeneration++ }},
		{name: "different user", body: map[string]any{"model": "echo", "user": "bob"}},
		{name: "different runtime", body: map[string]any{"model": "other", "user": "alice"}},
		{name: "different environment", body: map[string]any{"model": "environments/prod/echo", "user": "alice"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newConvFixture(t, convOptions{hold: true})
			head := f.mustTurn(t, "", "one")
			if tc.breakRow != nil {
				f.mutate(t, "sess_1", tc.breakRow)
			}
			body := map[string]any{"model": "echo", "user": "alice"}
			for k, v := range tc.body {
				body[k] = v
			}
			body["input"] = "two"
			body["previous_response_id"] = head
			rr := f.post(t, context.Background(), body)
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
			}
			if f.binder.count() != 2 {
				t.Fatalf("binds = %d, want 2 (cold)", f.binder.count())
			}
			if row := f.row(t, "sess_2"); row.ContinuationParentID != head {
				t.Errorf("cold session parent = %q, want %q", row.ContinuationParentID, head)
			}
			cold := f.exec.call(t, 1)
			assertMessages(t, cold.msgs, []executor.Message{
				{Role: "user", Content: "one"}, {Role: "assistant", Content: "reply1"}, {Role: "user", Content: "two"},
			})
			if f.forwarder.count() != 0 {
				t.Errorf("forwarder calls = %d, want 0 on an unforwarded cold continuation", f.forwarder.count())
			}
		})
	}
}

// TestOpenResponsesWarmComparesTheRequestedRuntime_spec_15 pins that the warm
// runtime condition compares the request's runtime with the runtime the
// creating request asked for: a session whose RuntimeRef creation rewrote is
// still continued warm by a request naming the original model.
//
// spec: §15 (Open Responses conversation sessions)
func TestOpenResponsesWarmComparesTheRequestedRuntime_spec_15(t *testing.T) {
	f := newConvFixture(t, convOptions{hold: true, rewriteRuntime: "echo-experiment-b"})
	head := f.mustTurn(t, "", "one")
	if row := f.row(t, "sess_1"); row.RuntimeRef != "echo-experiment-b" || row.RequestedRuntimeRef != "echo" {
		t.Fatalf("row runtime %q requested %q, want the rewritten runtime and echo", row.RuntimeRef, row.RequestedRuntimeRef)
	}
	if next := f.mustTurn(t, head, "two"); next != "sess_1.2" {
		t.Errorf("continuation id = %q, want sess_1.2 (warm)", next)
	}
	if f.binder.count() != 1 {
		t.Errorf("binds = %d, want 1", f.binder.count())
	}
}

// TestOpenResponsesReservationRecheckGoesCold_spec_15 pins the reservation's
// re-check under the row lock: when the head advances between the read and
// the reservation write, the write changes nothing and the turn goes cold.
//
// spec: §15 (Open Responses conversation sessions)
func TestOpenResponsesReservationRecheckGoesCold_spec_15(t *testing.T) {
	f := newConvFixture(t, convOptions{hold: true})
	head := f.mustTurn(t, "", "one")
	base := f.store.updateCount()
	f.store.before = func(n int) {
		if n == base+1 {
			f.mutate(t, "sess_1", func(s *sessionstore.Session) {
				s.Responses = append(s.Responses, sessionstore.ConversationResponse{EndSeq: 50, Completed: true})
			})
		}
	}
	if next := f.mustTurn(t, head, "two"); next != "sess_2.1" {
		t.Errorf("continuation id = %q, want sess_2.1 (cold)", next)
	}
	if row := f.row(t, "sess_1"); len(row.Responses) != 2 || row.Responses[1].EndSeq != 50 {
		t.Errorf("held row responses = %+v, want only the concurrent advance", row.Responses)
	}
}

// TestOpenResponsesExecutorRefusalBeforeDeliveryGoesCold_spec_15 pins that a
// warm dispatch the executor refuses before any input reaches the runtime
// goes cold in the same request, the reservation stays not completed, and a
// later continuation of the old head goes cold too.
//
// spec: §15 (Open Responses conversation sessions), §4.7.1 (Session seal)
func TestOpenResponsesExecutorRefusalBeforeDeliveryGoesCold_spec_15(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"not bound", fmt.Errorf("podexec: %w", executor.ErrNotBound)},
		{"sealed before delivery", &executor.SealedError{Delivered: 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newConvFixture(t, convOptions{hold: true})
			head := f.mustTurn(t, "", "one")
			f.exec.hook = func(_ context.Context, call int, _ string) error {
				if call == 2 {
					return tc.err
				}
				return nil
			}
			if next := f.mustTurn(t, head, "two"); next != "sess_2.1" {
				t.Errorf("continuation id = %q, want sess_2.1 (cold in the same request)", next)
			}
			row := f.row(t, "sess_1")
			if len(row.Responses) != 2 || row.Responses[1].Completed || row.State != session.StateRunning {
				t.Errorf("held row: state %q responses %+v; want running with the reservation not completed", row.State, row.Responses)
			}
			if _, _, hooks := f.terminal.snapshot(); hooks != 0 {
				t.Errorf("a refusal before delivery ended a session (%d hooks)", hooks)
			}
			if next := f.mustTurn(t, head, "three"); next != "sess_3.1" {
				t.Errorf("later continuation id = %q, want sess_3.1 (cold)", next)
			}
		})
	}
}

// failureCase drives one failed turn on a fixture and reports the session
// whose turn failed.
type failureCase struct {
	name string
	// arrange prepares the fixture after the held first turn (head is its
	// response id) and returns the request context and the failing
	// session's id.
	arrange func(t *testing.T, f *convFixture, head string) (ctx context.Context, failing string)
	// cold runs the failing request as a root turn rather than a
	// continuation of head.
	cold bool
}

// TestOpenResponsesFailureExitEndsTheSession_spec_15 pins the one failure
// exit: a post-bind failure on the cold path and a post-delivery failure on
// the warm path each answer the native 500, leave the response not
// completed, write the session failed, run the post-terminal hook once
// under one terminal claim, and make no executor release of their own. On
// a cancelled request context, the failed write and the hook still run on a
// live context.
//
// spec: §15 (Open Responses conversation sessions), §7.2 (Interactive Session Model)
func TestOpenResponsesFailureExitEndsTheSession_spec_15(t *testing.T) {
	sendFails := func(onCall int, err error) func(*testing.T, *convFixture, string) (context.Context, string) {
		return func(_ *testing.T, f *convFixture, _ string) (context.Context, string) {
			f.exec.hook = func(_ context.Context, call int, _ string) error {
				if call == onCall {
					return err
				}
				return nil
			}
			return context.Background(), "sess_1"
		}
	}
	cases := []failureCase{
		{name: "cold send error", cold: true, arrange: func(t *testing.T, f *convFixture, h string) (context.Context, string) {
			ctx, _ := sendFails(2, errors.New("adapter stream reset"))(t, f, h)
			return ctx, "sess_2"
		}},
		{name: "warm send error after partial delivery", arrange: sendFails(2, &executor.SealedError{Delivered: 1})},
		{name: "warm send error", arrange: sendFails(2, errors.New("adapter stream reset"))},
		{name: "warm send cancelled with the request context", arrange: func(_ *testing.T, f *convFixture, _ string) (context.Context, string) {
			ctx, cancel := context.WithCancel(context.Background())
			f.exec.hook = func(_ context.Context, call int, _ string) error {
				if call == 2 {
					cancel()
					return context.Canceled
				}
				return nil
			}
			return ctx, "sess_1"
		}},
		{name: "warm record error", arrange: func(t *testing.T, f *convFixture, _ string) (context.Context, string) {
			f.ts.(*flakyTranscripts).failFrom = 2
			return context.Background(), "sess_1"
		}},
		{name: "cold record error", cold: true, arrange: func(_ *testing.T, f *convFixture, _ string) (context.Context, string) {
			f.ts.(*flakyTranscripts).failFrom = 2
			return context.Background(), "sess_2"
		}},
		{name: "warm commit error", arrange: func(_ *testing.T, f *convFixture, _ string) (context.Context, string) {
			commit := f.store.updateCount() + 2 // reservation, then commit
			f.store.failUpdate = func(n int) bool { return n == commit }
			return context.Background(), "sess_1"
		}},
		{name: "cold commit error", cold: true, arrange: func(_ *testing.T, f *convFixture, _ string) (context.Context, string) {
			commit := f.store.updateCount() + 1
			f.store.failUpdate = func(n int) bool { return n == commit }
			return context.Background(), "sess_2"
		}},
		{name: "generation advanced between send and commit", arrange: func(t *testing.T, f *convFixture, _ string) (context.Context, string) {
			f.exec.hook = func(_ context.Context, call int, _ string) error {
				if call == 2 {
					f.mutate(t, "sess_1", func(s *sessionstore.Session) { s.RecoveryGeneration++ })
				}
				return nil
			}
			return context.Background(), "sess_1"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newConvFixture(t, convOptions{hold: true, transcripts: &flakyTranscripts{Store: transcriptstore.NewMemory()}})
			head := f.mustTurn(t, "", "one")
			ctx, failing := tc.arrange(t, f, head)
			body := map[string]any{"model": "echo", "input": "two", "user": "alice"}
			if !tc.cold {
				body["previous_response_id"] = head
			}
			rr := f.post(t, ctx, body)
			assertFailedExit(t, f, rr, failing)
			if !f.store.lastUpdateCtxLive() {
				t.Error("the failed write ran on a cancelled context")
			}
			f.terminal.mu.Lock()
			hookCtxOK := append([]bool(nil), f.terminal.hookCtxOK...)
			from := append([]session.State(nil), f.terminal.hookFrom...)
			f.terminal.mu.Unlock()
			if len(hookCtxOK) != 1 || !hookCtxOK[0] || from[0] != session.StateRunning {
				t.Errorf("hook calls: live ctx %v, from %v; want one on a live context from running", hookCtxOK, from)
			}
		})
	}
}

// assertFailedExit checks the outcome of a failed turn on failing: a native
// 500, no completed response past the held head, a failed row, one terminal
// claim ended once, one post-terminal hook, and no executor release.
func assertFailedExit(t *testing.T, f *convFixture, rr *httptest.ResponseRecorder, failing string) {
	t.Helper()
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"type":"server_error"`) {
		t.Errorf("body %s, want a native server_error envelope", rr.Body.String())
	}
	row := f.row(t, failing)
	if row.State != session.StateFailed || row.TerminatedReason != "Failed" {
		t.Errorf("row %s state %q reason %q, want failed", failing, row.State, row.TerminatedReason)
	}
	for i, r := range row.Responses {
		if r.Completed && (failing != "sess_1" || i > 0) {
			t.Errorf("failed turn left response %d completed: %+v", i+1, row.Responses)
		}
	}
	begins, ends, hooks := f.terminal.snapshot()
	if begins != 1 || ends != 1 || hooks != 1 {
		t.Errorf("claims begun %d ended %d, hooks %d; want 1, 1, 1", begins, ends, hooks)
	}
	if got := f.exec.releases(); len(got) != 0 {
		t.Errorf("handler released the executor itself: %v; the hook releases", got)
	}
}

// flakyTranscripts fails every Append from the failFrom-th call on (1-based);
// zero never fails.
type flakyTranscripts struct {
	transcriptstore.Store
	mu       sync.Mutex
	appends  int
	failFrom int
}

func (f *flakyTranscripts) Append(ctx context.Context, tenantID, sessionID string, entries ...transcriptstore.Entry) (uint64, error) {
	f.mu.Lock()
	f.appends++
	fail := f.failFrom > 0 && f.appends >= f.failFrom
	f.mu.Unlock()
	if fail {
		return 0, errors.New("transcript append failed")
	}
	return f.Store.Append(ctx, tenantID, sessionID, entries...)
}

// TestOpenResponsesFailureOnAnEndedSessionWritesNothing_spec_15 pins that
// when a concurrent terminate has already ended the row as the turn fails,
// endHeldSession writes nothing, ends its claim once, and runs no hook; the
// row keeps the other writer's state and the handler still answers 500.
//
// spec: §15 (Open Responses conversation sessions), §7.2 (Interactive Session Model)
func TestOpenResponsesFailureOnAnEndedSessionWritesNothing_spec_15(t *testing.T) {
	f := newConvFixture(t, convOptions{hold: true})
	head := f.mustTurn(t, "", "one")
	f.exec.hook = func(_ context.Context, call int, _ string) error {
		if call == 2 {
			f.mutate(t, "sess_1", func(s *sessionstore.Session) {
				from := s.State
				s.State = session.StateCancelled
				sessionstore.MarkTerminated(s, from, time.Now())
			})
			return errors.New("adapter closed the stream")
		}
		return nil
	}
	rr := f.turn(t, head, "two")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rr.Code)
	}
	if row := f.row(t, "sess_1"); row.State != session.StateCancelled {
		t.Errorf("state = %q, want the other writer's cancelled", row.State)
	}
	begins, ends, hooks := f.terminal.snapshot()
	if begins != 1 || ends != 1 || hooks != 0 {
		t.Errorf("claims begun %d ended %d, hooks %d; want 1, 1, 0", begins, ends, hooks)
	}
	if got := f.exec.releases(); len(got) != 0 {
		t.Errorf("releases = %v, want none: the writer that ended the row releases", got)
	}
}

// TestOpenResponsesDoubleFailureReleasesOnce_spec_15 pins the fallback when
// the session's own failed write fails too: with every Update failing from
// the completion write onward, on both paths, the handler answers 500, runs
// no hook, and releases the executor binding once with the failed
// disposition.
//
// spec: §15 (Open Responses conversation sessions), §6.2 (Pod State Machine)
func TestOpenResponsesDoubleFailureReleasesOnce_spec_15(t *testing.T) {
	for _, cold := range []bool{true, false} {
		t.Run(fmt.Sprintf("cold=%v", cold), func(t *testing.T) {
			f := newConvFixture(t, convOptions{hold: true})
			head := f.mustTurn(t, "", "one")
			commit := f.store.updateCount() + 1
			prev := head
			if cold {
				prev = ""
			} else {
				commit++ // the reservation precedes the commit
			}
			f.store.failUpdate = func(n int) bool { return n >= commit }
			rr := f.turn(t, prev, "two")
			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("status %d, want 500", rr.Code)
			}
			if _, _, hooks := f.terminal.snapshot(); hooks != 0 {
				t.Errorf("hooks = %d, want 0", hooks)
			}
			if got := f.exec.releases(); len(got) != 1 || got[0] != executor.DispositionFailed {
				t.Errorf("releases = %v, want one DispositionFailed", got)
			}
		})
	}
}

// TestOpenResponsesNonLocalForwardWritesNothing_spec_29_3 pins §29.3
// Forwarding rule 1 on the adapter route: when the forwarder reports the
// turn not local, the handler makes no store write and writes nothing of its
// own, so the forwarder's response stands exactly as written; and the
// refusal the handler hands the forwarder is the native 409
// TARGET_NOT_READY carrying current_state.
//
// spec: §29.3 (Interactive message send) Forwarding, §15 (Open Responses conversation sessions)
func TestOpenResponsesNonLocalForwardWritesNothing_spec_29_3(t *testing.T) {
	t.Run("coordinator answered", func(t *testing.T) {
		f := newConvFixture(t, convOptions{hold: true})
		head := f.mustTurn(t, "", "one")
		base := f.store.updateCount()
		f.forwarder.local, f.forwarder.writeStatus = false, http.StatusOK
		rr := f.turn(t, head, "two")
		if rr.Code != http.StatusOK || rr.Body.String() != `{"from":"coordinator"}` {
			t.Errorf("response = %d %q, want the forwarder's response unchanged", rr.Code, rr.Body.String())
		}
		if got := f.store.updateCount(); got != base {
			t.Errorf("store updates after the forward = %d, want 0", got-base)
		}
		if f.binder.count() != 1 {
			t.Errorf("binds = %d, want 1", f.binder.count())
		}
	})
	t.Run("refused", func(t *testing.T) {
		f := newConvFixture(t, convOptions{hold: true})
		head := f.mustTurn(t, "", "one")
		f.forwarder.local, f.forwarder.refuseState = false, string(session.StateRunning)
		rr := f.turn(t, head, "two")
		if rr.Code != http.StatusConflict {
			t.Fatalf("status %d, want 409: %s", rr.Code, rr.Body.String())
		}
		var env translator.OpenAIError
		if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Error.Code != "TARGET_NOT_READY" || env.Error.CurrentState != "running" || env.Error.Type != "invalid_request_error" {
			t.Errorf("error body = %+v, want TARGET_NOT_READY with current_state running", env.Error)
		}
	})
}

// TestOpenResponsesForwardedRequestAlwaysGoesToTheForwarder_spec_29_3 pins
// §29.3 Forwarding rule 5 on the adapter route: a forwarded request whose
// row fails a warm condition still goes to the forwarder; a refusal answers
// 409 with no bind, and a local answer goes cold. The same row on an
// unforwarded request makes no forwarder call.
//
// spec: §29.3 (Interactive message send) Forwarding, §15 (Open Responses conversation sessions)
func TestOpenResponsesForwardedRequestAlwaysGoesToTheForwarder_spec_29_3(t *testing.T) {
	breaks := map[string]func(*sessionstore.Session){
		"suspended": func(s *sessionstore.Session) { s.State = session.StateSuspended },
		"head advanced": func(s *sessionstore.Session) {
			s.Responses = append(s.Responses, sessionstore.ConversationResponse{Completed: true})
		},
	}
	for name, brk := range breaks {
		t.Run(name+"/refused", func(t *testing.T) {
			f := newConvFixture(t, convOptions{hold: true})
			head := f.mustTurn(t, "", "one")
			f.mutate(t, "sess_1", brk)
			f.forwarder.forwarded, f.forwarder.local, f.forwarder.refuseState = true, false, "suspended"
			rr := f.turn(t, head, "two")
			if rr.Code != http.StatusConflict || f.forwarder.count() != 1 {
				t.Errorf("status %d, forwarder calls %d; want 409 and 1", rr.Code, f.forwarder.count())
			}
			if f.binder.count() != 1 {
				t.Errorf("binds = %d, want 1: a refused forwarded request creates no session", f.binder.count())
			}
		})
		t.Run(name+"/local", func(t *testing.T) {
			f := newConvFixture(t, convOptions{hold: true})
			head := f.mustTurn(t, "", "one")
			f.mutate(t, "sess_1", brk)
			f.forwarder.forwarded = true
			if next := f.mustTurn(t, head, "two"); next != "sess_2.1" || f.forwarder.count() != 1 {
				t.Errorf("id %q, forwarder calls %d; want sess_2.1 (cold) and 1", next, f.forwarder.count())
			}
		})
		t.Run(name+"/unforwarded", func(t *testing.T) {
			f := newConvFixture(t, convOptions{hold: true})
			head := f.mustTurn(t, "", "one")
			f.mutate(t, "sess_1", brk)
			f.mustTurn(t, head, "two")
			if f.forwarder.count() != 0 {
				t.Errorf("forwarder calls = %d, want 0", f.forwarder.count())
			}
		})
	}
}

// TestOpenResponsesResolutionPrecedesClaim_spec_15 pins that a
// previous_response_id that GET would answer 404 is rejected with the native
// 404 before any bind, forward, or store write.
//
// spec: §15 (Open Responses conversation sessions)
func TestOpenResponsesResolutionPrecedesClaim_spec_15(t *testing.T) {
	for _, id := range []string{"sess_1.2", "sess_1.0", "sess_1", ".1", "sess_missing.1", "sess_1.x"} {
		t.Run(id, func(t *testing.T) {
			f := newConvFixture(t, convOptions{hold: true})
			f.mustTurn(t, "", "one")
			base := f.store.updateCount()
			rr := f.turn(t, id, "two")
			if rr.Code != http.StatusNotFound {
				t.Fatalf("status %d, want 404", rr.Code)
			}
			if f.binder.count() != 1 || f.forwarder.count() != 0 || f.store.updateCount() != base {
				t.Errorf("binds %d, forwards %d, updates %d; want no side effect",
					f.binder.count(), f.forwarder.count(), f.store.updateCount()-base)
			}
		})
	}
	t.Run("not completed", func(t *testing.T) {
		f := newConvFixture(t, convOptions{hold: true})
		f.mustTurn(t, "", "one")
		f.mutate(t, "sess_1", func(s *sessionstore.Session) {
			s.Responses = append(s.Responses, sessionstore.ConversationResponse{})
		})
		if rr := f.turn(t, "sess_1.2", "two"); rr.Code != http.StatusNotFound || f.binder.count() != 1 {
			t.Errorf("status %d binds %d, want 404 and no bind", rr.Code, f.binder.count())
		}
	})
	t.Run("cross tenant", func(t *testing.T) {
		f := newConvFixture(t, convOptions{hold: true})
		head := f.mustTurn(t, "", "one")
		buf, _ := json.Marshal(map[string]any{"model": "echo", "input": "x", "previous_response_id": head})
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(buf))
		req.Header.Set("X-Lenny-Tenant-ID", "globex")
		rr := httptest.NewRecorder()
		f.h.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound || f.binder.count() != 1 || f.forwarder.count() != 0 {
			t.Errorf("status %d binds %d forwards %d, want 404 with no side effect", rr.Code, f.binder.count(), f.forwarder.count())
		}
	})
}

// TestOpenResponsesDefaultTerminalReleasesTheFailedSession_spec_15 pins the
// single-process handler built without a session terminal: a failed held
// turn writes the session failed and releases its binding once with the
// failed disposition.
//
// spec: §15 (Open Responses conversation sessions), §17.4 (in-memory mode)
func TestOpenResponsesDefaultTerminalReleasesTheFailedSession_spec_15(t *testing.T) {
	store := memstore.New()
	exec := &convExecutor{}
	binder := &convBinder{store: store, hold: true}
	h := translator.NewOpenResponsesHandler(store, exec, translator.OpenResponsesOptions{
		DevTenantHeader:  true,
		SingleShotBinder: binder,
		Transcripts:      transcriptstore.NewMemory(),
	})
	exec.hook = func(_ context.Context, call int, _ string) error {
		if call == 2 {
			return errors.New("adapter stream reset")
		}
		return nil
	}
	first := respPostTenant(t, h.Handler(), "acme", map[string]any{"model": "echo", "input": "one"})
	if first.Code != http.StatusOK {
		t.Fatalf("first turn status %d", first.Code)
	}
	rr := respPostTenant(t, h.Handler(), "acme", map[string]any{"model": "echo", "input": "two", "previous_response_id": "sess_1.1"})
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rr.Code)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_1")
	if row.State != session.StateFailed {
		t.Errorf("state = %q, want failed", row.State)
	}
	if got := exec.releases(); len(got) != 1 || got[0] != executor.DispositionFailed {
		t.Errorf("releases = %v, want one DispositionFailed", got)
	}
}
