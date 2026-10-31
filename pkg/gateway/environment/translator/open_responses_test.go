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

	"github.com/lennylabs/lenny/tests/testinfra/responsesstub"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

func newResponsesHandler(t *testing.T) (*translator.OpenResponsesHandler, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	h := translator.NewOpenResponsesHandler(store, executor.NewEchoExecutor(), translator.OpenResponsesOptions{
		HolderForwarder: responsesstub.LocalForwarder{},
		SessionAccess:   ownerprincipal.SessionAccess{},
		Terminal:        responsesstub.ReleasingTerminal{Exec: nil, Store: store},
		DevTenantHeader: true,
		Clock:           func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:          func() string { return "resp_1" },
	})
	return h, store
}

func respPost(t *testing.T, h http.Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(buf))
	req = req.WithContext(ownerprincipal.Context(req.Context(), ownerprincipal.Alice))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestOpenResponsesHappyPath(t *testing.T) {
	h, store := newResponsesHandler(t)
	rr := respPost(t, h.Handler(), map[string]any{
		"model": "echo",
		"input": "hello",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp translator.OpenResponsesResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.ID != "resp_1.1" || resp.Object != "response" {
		t.Errorf("envelope: %+v", resp)
	}
	if resp.Status != "completed" {
		t.Errorf("status: %q", resp.Status)
	}
	if len(resp.Output) != 1 || len(resp.Output[0].Content) != 1 {
		t.Fatalf("output: %+v", resp.Output)
	}
	if !strings.Contains(resp.Output[0].Content[0].Text, "hello") {
		t.Errorf("content: %q", resp.Output[0].Content[0].Text)
	}
	row, _ := store.Get(context.Background(), "acme", "resp_1")
	if row.State != session.StateCompleted {
		t.Errorf("session state: %q", row.State)
	}
	if len(row.Responses) != 1 || !row.Responses[0].Completed {
		t.Errorf("session responses: %+v, want one completed record", row.Responses)
	}
}

func TestOpenResponsesAcceptsArrayInput(t *testing.T) {
	h, _ := newResponsesHandler(t)
	rr := respPost(t, h.Handler(), map[string]any{
		"model": "echo",
		"input": []map[string]any{
			{"role": "user", "content": "hi"},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestOpenResponsesRejectsMissingInput(t *testing.T) {
	h, _ := newResponsesHandler(t)
	rr := respPost(t, h.Handler(), map[string]any{"model": "echo"})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("missing input: got %d", rr.Code)
	}
}

func TestOpenResponsesStreamEmitsTypedEvents(t *testing.T) {
	h, _ := newResponsesHandler(t)
	rr := respPost(t, h.Handler(), map[string]any{
		"model":  "echo",
		"input":  "hello stream",
		"stream": true,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("stream status: %d, body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type: %q", ct)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"event: response.created",
		"event: response.output_text.delta",
		"event: response.completed",
		"hello",
		"stream",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("stream body missing %q; body:\n%s", want, body)
		}
	}
	// Both typed envelope events carry the response id <session>.1.
	for _, ev := range []string{"response.created", "response.completed"} {
		if id := streamEventResponseID(t, body, ev); id != "resp_1.1" {
			t.Errorf("%s response id = %q, want resp_1.1", ev, id)
		}
	}
}

// streamEventResponseID returns response.id from the data line of the named
// SSE event in body.
func streamEventResponseID(t *testing.T, body, event string) string {
	t.Helper()
	marker := "event: " + event + "\ndata: "
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("stream has no %s event: %s", event, body)
	}
	line := body[i+len(marker):]
	line = line[:strings.IndexByte(line, '\n')]
	var payload struct {
		Response struct {
			ID string `json:"id"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		t.Fatalf("decode %s data %q: %v", event, line, err)
	}
	return payload.Response.ID
}

// TestOpenResponsesGet pins GET /v1/responses/{id} on a completed response:
// 200 with status completed, the response id echoed, and the model.
//
// spec: §15 (Open Responses conversation sessions)
func TestOpenResponsesGet(t *testing.T) {
	h, store := newResponsesHandler(t)
	seedResponses(t, store, "acme", "resp_1", "", true)
	rr := getResponse(t, h, "acme", "resp_1.1")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp translator.OpenResponsesResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.ID != "resp_1.1" || resp.Status != "completed" || resp.Model != "echo" {
		t.Errorf("resp: %+v", resp)
	}
}

// TestOpenResponsesGetMissing pins the native 404 for an unknown response,
// both in the response-id form and as a bare session id.
//
// spec: §15 (Open Responses conversation sessions)
func TestOpenResponsesGetMissing(t *testing.T) {
	h, store := newResponsesHandler(t)
	seedResponses(t, store, "acme", "resp_1", "", true)
	for _, id := range []string{"missing.1", "missing", "resp_1"} {
		if rr := getResponse(t, h, "acme", id); rr.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", id, rr.Code)
		}
	}
}

// TestOpenResponsesGetAnswersCompletedResponsesOnly_spec_15 pins the GET
// contract over each kind of id: a completed response answers 200 with its
// id and previous_response_id (the preceding ordinal on the same session, or
// for ordinal 1 the response the session's first turn continued from), and
// an id that names no completed response of the caller's tenant (not
// completed, past the newest ordinal, unknown, cross-tenant, malformed, or a
// non-canonical leading-zero spelling of an issued id) answers the native 404
// not_found_error.
//
// spec: §15 (Open Responses conversation sessions); §4.2 (session-store
// tenant isolation)
func TestOpenResponsesGetAnswersCompletedResponsesOnly_spec_15(t *testing.T) {
	h, store := newResponsesHandler(t)
	seedResponses(t, store, "acme", "S", "P.4", true, true, false)
	seedResponses(t, store, "globex", "G", "", true)

	for _, tc := range []struct {
		id       string
		wantCode int
		wantPrev string
	}{
		{id: "S.1", wantCode: http.StatusOK, wantPrev: "P.4"},
		{id: "S.2", wantCode: http.StatusOK, wantPrev: "S.1"},
		{id: "S.3", wantCode: http.StatusNotFound},
		{id: "S.4", wantCode: http.StatusNotFound},
		{id: "unknown.1", wantCode: http.StatusNotFound},
		{id: "G.1", wantCode: http.StatusNotFound},
		{id: "S.0", wantCode: http.StatusNotFound},
		{id: "S.one", wantCode: http.StatusNotFound},
		{id: "S", wantCode: http.StatusNotFound},
		{id: "S.01", wantCode: http.StatusNotFound},
		{id: "S.02", wantCode: http.StatusNotFound},
	} {
		t.Run(tc.id, func(t *testing.T) {
			rr := getResponse(t, h, "acme", tc.id)
			if rr.Code != tc.wantCode {
				t.Fatalf("GET %s: %d, want %d; body=%s", tc.id, rr.Code, tc.wantCode, rr.Body.String())
			}
			if tc.wantCode == http.StatusNotFound {
				var env struct {
					Error struct {
						Type string `json:"type"`
					} `json:"error"`
				}
				_ = json.Unmarshal(rr.Body.Bytes(), &env)
				if env.Error.Type != "not_found_error" {
					t.Errorf("GET %s error.type = %q, want not_found_error", tc.id, env.Error.Type)
				}
				return
			}
			var resp translator.OpenResponsesResponse
			_ = json.Unmarshal(rr.Body.Bytes(), &resp)
			if resp.ID != tc.id || resp.Status != "completed" || resp.PreviousResponseID != tc.wantPrev {
				t.Errorf("GET %s = id %q status %q previous %q, want id %q completed previous %q",
					tc.id, resp.ID, resp.Status, resp.PreviousResponseID, tc.id, tc.wantPrev)
			}
		})
	}
}

// seedResponses creates session id in tenant with one response record per
// completed flag and the given first-turn continuation parent.
func seedResponses(t *testing.T, store sessionstore.Store, tenant, id, parent string, completed ...bool) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var responses []sessionstore.ConversationResponse
	for _, c := range completed {
		responses = append(responses, sessionstore.ConversationResponse{CreatedAt: now, Completed: c})
	}
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: tenant, UserID: ownerprincipal.Alice, State: session.StateRunning, RuntimeRef: "echo",
		ContinuationParentID: parent, Responses: responses, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func getResponse(t *testing.T, h *translator.OpenResponsesHandler, tenant, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/responses/"+id, nil)
	req = req.WithContext(ownerprincipal.Context(req.Context(), ownerprincipal.Alice))
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	rr := httptest.NewRecorder()
	h.Handler().ServeHTTP(rr, req)
	return rr
}

// TestOpenResponsesDelete pins that the adapter serves no delete operation: a
// DELETE on /v1/responses/{id} answers 405 with an Allow header naming GET, and
// the conversation session record behind the response survives it.
// spec: §15 (Open Responses conversation sessions), §15.1 (REST API)
func TestOpenResponsesDelete(t *testing.T) {
	h, store := newResponsesHandler(t)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "resp_1", TenantID: "acme",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/v1/responses/resp_1", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE /v1/responses/{id}: status %d, want 405", rr.Code)
	}
	if allow := rr.Header().Get("Allow"); !strings.Contains(allow, http.MethodGet) {
		t.Errorf("Allow header %q does not name GET", allow)
	}
	if _, err := store.Get(context.Background(), "acme", "resp_1"); err != nil {
		t.Errorf("session record behind the response must survive a DELETE: %v", err)
	}
}

// TestOpenResponsesAdapterCapabilities pins §9.1 + §15.1: the Open Responses adapter publishes its own capability
// block. previous_response_id threads a multi-turn conversation
// through the same Lenny session, so the adapter reports session
// continuity; delegation, elicitation, and interrupt are not part
// of the Open Responses envelope. F-9.1.6 / F-9.1.8.
func TestOpenResponsesAdapterCapabilities_spec_9_1_35(t *testing.T) {
	caps := translator.OpenResponsesAdapterCapabilities()
	if caps.PathPrefix != "/v1/responses" {
		t.Errorf("PathPrefix: %q", caps.PathPrefix)
	}
	if caps.Protocol != "open-responses" {
		t.Errorf("Protocol: %q", caps.Protocol)
	}
	if !caps.SupportsSessionContinuity {
		t.Error("Open Responses threads previous_response_id and must report continuity")
	}
	if caps.SupportsDelegation || caps.SupportsElicitation || caps.SupportsInterrupt {
		t.Errorf("Open Responses adapter exposes no Lenny surfaces: %+v", caps)
	}
}

// captureExecutor is a fake Executor that records the message slice each
// Send receives so a test can assert the rehydrated prior conversation is
// prepended ahead of the new turn in chronological order. Each Send returns a
// distinct assistant text ("reply1", "reply2", ...) so a transcript records a
// per-turn assistant line. sendErr, when set, makes every Send fail.
type captureExecutor struct {
	mu      sync.Mutex
	calls   [][]executor.Message
	replies int
	sendErr error
}

func (c *captureExecutor) Send(_ context.Context, _ string, messages []executor.Message) (executor.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sendErr != nil {
		return executor.Response{}, c.sendErr
	}
	cp := make([]executor.Message, len(messages))
	copy(cp, messages)
	c.calls = append(c.calls, cp)
	c.replies++
	return executor.Response{Parts: []executor.MessagePart{{
		Type: "text",
		Text: fmt.Sprintf("reply%d", c.replies),
	}}}, nil
}

func (c *captureExecutor) Close(context.Context, string) error { return nil }

func (c *captureExecutor) lastCall(t *testing.T) []executor.Message {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) == 0 {
		t.Fatal("executor received no Send calls")
	}
	return c.calls[len(c.calls)-1]
}

// newContinuityHandler builds a handler wired to a capturing executor and a
// Memory transcript store, with a counter IDFunc so each POST claims a
// distinct response id (resp_1, resp_2, ...) and a chain of continuations can
// be driven through the real HTTP path.
func newContinuityHandler(t *testing.T, exec executor.Executor, ts transcriptstore.Store) (*translator.OpenResponsesHandler, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	var n int
	h := translator.NewOpenResponsesHandler(store, exec, translator.OpenResponsesOptions{
		HolderForwarder: responsesstub.LocalForwarder{},
		SessionAccess:   ownerprincipal.SessionAccess{},
		Terminal:        responsesstub.ReleasingTerminal{Exec: exec, Store: store},
		DevTenantHeader: true,
		Clock:           func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:          func() string { n++; return fmt.Sprintf("resp_%d", n) },
		Transcripts:     ts,
	})
	return h, store
}

func respPostTenant(t *testing.T, h http.Handler, tenant string, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(buf))
	req = req.WithContext(ownerprincipal.Context(req.Context(), ownerprincipal.Alice))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func assertMessages(t *testing.T, got []executor.Message, want []executor.Message) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("dispatched %d messages, want %d: got %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Errorf("message[%d]: got {%q,%q}, want {%q,%q}",
				i, got[i].Role, got[i].Content, want[i].Role, want[i].Content)
		}
	}
}

// TestOpenResponsesContinuationUnknownIDFailsClosed pins that a continuation
// naming an unknown previous_response_id resolves fail-closed to a native 404
// with no dispatch, rather than silently dispatching only the new turn onto a
// pod with empty runtime memory. The freshly-claimed pod is still drained by
// the deferred release.
// spec: §15 (fail-closed continuation resolution); §4.2 (session-store tenant isolation).
func TestOpenResponsesContinuationUnknownIDFailsClosed_spec_15(t *testing.T) {
	exec := &captureExecutor{}
	h, _ := newContinuityHandler(t, exec, transcriptstore.NewMemory())
	rr := respPostTenant(t, h.Handler(), "acme", map[string]any{
		"model":                "echo",
		"input":                "hi",
		"previous_response_id": "resp_missing.1",
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404; body=%s", rr.Code, rr.Body.String())
	}
	if len(exec.calls) != 0 {
		t.Errorf("exec.Send called %d times; want 0 (fail-closed, no dispatch)", len(exec.calls))
	}
}

// TestOpenResponsesContinuationCrossTenantFailsClosed pins that a
// previous_response_id owned by another tenant is indistinguishable from an
// unknown id under the tenant-scoped session-store isolation model, so it
// resolves fail-closed to a 404 with no cross-tenant rehydration and no
// dispatch.
// spec: §15 (fail-closed continuation resolution); §4.2 (session-store tenant isolation).
func TestOpenResponsesContinuationCrossTenantFailsClosed_spec_4_2(t *testing.T) {
	exec := &captureExecutor{}
	h, _ := newContinuityHandler(t, exec, transcriptstore.NewMemory())
	// globex creates resp_1 with its own transcript.
	if rr := respPostTenant(t, h.Handler(), "globex", map[string]any{
		"model": "echo", "input": "globex secret",
	}); rr.Code != http.StatusOK {
		t.Fatalf("seed globex: %d, body=%s", rr.Code, rr.Body.String())
	}
	callsBefore := len(exec.calls)
	// acme references globex's resp_1.1: cross-tenant, must fail closed.
	rr := respPostTenant(t, h.Handler(), "acme", map[string]any{
		"model": "echo", "input": "hi", "previous_response_id": "resp_1.1",
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant status: got %d want 404; body=%s", rr.Code, rr.Body.String())
	}
	if len(exec.calls) != callsBefore {
		t.Errorf("cross-tenant continuation dispatched %d turns; want 0", len(exec.calls)-callsBefore)
	}
}

// TestOpenResponsesChainWalkPrependsPriorConversation pins the cold
// continuation's conversation context: across a three-turn lineage the
// dispatch receives the prior conversation prepended ahead of the new input in
// chronological order, assembled by walking ContinuationParentID across the
// sessions' transcripts, and each session's transcript holds only its own
// turns (no copy-forward).
// spec: §15 (Open Responses conversation sessions).
func TestOpenResponsesChainWalkPrependsPriorConversation_spec_15(t *testing.T) {
	exec := &captureExecutor{}
	ts := transcriptstore.NewMemory()
	h, _ := newContinuityHandler(t, exec, ts)

	// Turn 1: chain root.
	if rr := respPostTenant(t, h.Handler(), "acme", map[string]any{
		"model": "echo", "input": "one",
	}); rr.Code != http.StatusOK {
		t.Fatalf("root: %d, body=%s", rr.Code, rr.Body.String())
	}
	// Turn 2: continuation off resp_1.
	if rr := respPostTenant(t, h.Handler(), "acme", map[string]any{
		"model": "echo", "input": "two", "previous_response_id": "resp_1.1",
	}); rr.Code != http.StatusOK {
		t.Fatalf("turn2: %d, body=%s", rr.Code, rr.Body.String())
	}
	assertMessages(t, exec.calls[1], []executor.Message{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "reply1"},
		{Role: "user", Content: "two"},
	})
	// Turn 3: continuation off resp_2 rehydrates the full two-turn history.
	if rr := respPostTenant(t, h.Handler(), "acme", map[string]any{
		"model": "echo", "input": "three", "previous_response_id": "resp_2.1",
	}); rr.Code != http.StatusOK {
		t.Fatalf("turn3: %d, body=%s", rr.Code, rr.Body.String())
	}
	assertMessages(t, exec.calls[2], []executor.Message{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "reply1"},
		{Role: "user", Content: "two"},
		{Role: "assistant", Content: "reply2"},
		{Role: "user", Content: "three"},
	})

	// Each bucket holds only its own turn: no copy-forward duplication.
	for id, want := range map[string][]string{
		"resp_1": {"one", "reply1"},
		"resp_2": {"two", "reply2"},
	} {
		entries, err := ts.Get(context.Background(), "acme", id)
		if err != nil {
			t.Fatalf("transcript %s: %v", id, err)
		}
		if len(entries) != len(want) {
			t.Fatalf("%s bucket has %d entries, want %d: %+v", id, len(entries), len(want), entries)
		}
		for i, c := range want {
			if entries[i].Content != c {
				t.Errorf("%s entry[%d] content: got %q want %q", id, i, entries[i].Content, c)
			}
		}
	}
}

// TestOpenResponsesContinuationEmptyTranscript pins that a continuation whose
// referenced response completed but whose session has no transcript (erased
// under a surviving row) dispatches with an empty prior history and returns
// 200, rather than a 404.
// spec: §15 (Open Responses conversation sessions); §12.8 (erasure).
func TestOpenResponsesContinuationEmptyTranscript_spec_15(t *testing.T) {
	exec := &captureExecutor{}
	ts := transcriptstore.NewMemory()
	h, store := newContinuityHandler(t, exec, ts)
	// A referenced response row that exists but has no recorded transcript.
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "resp_seed", TenantID: "acme", UserID: ownerprincipal.Alice, State: session.StateCompleted,
		RuntimeRef: "echo", CreatedAt: now, UpdatedAt: now,
		Responses: []sessionstore.ConversationResponse{{CreatedAt: now, Completed: true}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := respPostTenant(t, h.Handler(), "acme", map[string]any{
		"model": "echo", "input": "hi", "previous_response_id": "resp_seed.1",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	assertMessages(t, exec.lastCall(t), []executor.Message{
		{Role: "user", Content: "hi"},
	})
}

// TestOpenResponsesRecordsChainRootTurn pins that a turn carrying no
// previous_response_id is recorded on its own session, so a later
// continuation can rehydrate it, and that the record is required: a
// transcript append failure answers a native 500, leaves the session with no
// completed response, and releases the pod with the failed disposition,
// rather than returning a 200 for a response no later continuation could
// rehydrate.
// spec: §15 (Open Responses conversation sessions); §15.1 (session transcript).
func TestOpenResponsesRecordsChainRootTurn_spec_15_1(t *testing.T) {
	exec := &captureExecutor{}
	ts := transcriptstore.NewMemory()
	h, _ := newContinuityHandler(t, exec, ts)
	if rr := respPostTenant(t, h.Handler(), "acme", map[string]any{
		"model": "echo", "input": "one",
	}); rr.Code != http.StatusOK {
		t.Fatalf("root: %d, body=%s", rr.Code, rr.Body.String())
	}
	entries, err := ts.Get(context.Background(), "acme", "resp_1")
	if err != nil {
		t.Fatalf("chain-root turn not recorded: %v", err)
	}
	if len(entries) != 2 || entries[0].Content != "one" || entries[1].Content != "reply1" {
		t.Errorf("chain-root transcript: %+v", entries)
	}

	failExec := &dispositionExecutor{}
	failStore := memstore.New()
	failH := translator.NewOpenResponsesHandler(failStore, failExec, translator.OpenResponsesOptions{
		HolderForwarder: responsesstub.LocalForwarder{},
		SessionAccess:   ownerprincipal.SessionAccess{},
		Terminal:        responsesstub.ReleasingTerminal{Exec: failExec, Store: failStore},
		DevTenantHeader: true,
		Clock:           func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:          func() string { return "resp_fail" },
		Transcripts:     appendFailTranscripts{Store: transcriptstore.NewMemory()},
	})
	rr := respPostTenant(t, failH.Handler(), "acme", map[string]any{
		"model": "echo", "input": "one",
	})
	assertTurnFailed(t, rr, failStore, failExec, "resp_fail")
}

// assertTurnFailed checks the outcome of a turn whose record or completion
// write failed: a native 500 server_error, no completed response on the
// session, and one executor release with the failed disposition.
func assertTurnFailed(t *testing.T, rr *httptest.ResponseRecorder, store sessionstore.Store,
	exec *dispositionExecutor, sessionID string,
) {
	t.Helper()
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want 500; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if env.Error.Type != "server_error" {
		t.Errorf("error.type = %q, want server_error", env.Error.Type)
	}
	row, err := store.Get(context.Background(), "acme", sessionID)
	if err != nil {
		t.Fatalf("read row: %v", err)
	}
	for _, r := range row.Responses {
		if r.Completed {
			t.Errorf("a failed turn left a completed response: %+v", row.Responses)
		}
	}
	if row.State == session.StateCompleted {
		t.Errorf("a failed turn wrote the completed state")
	}
	if got := exec.dispositions(); len(got) != 1 || got[0] != executor.DispositionFailed {
		t.Errorf("executor releases = %v, want one DispositionFailed", got)
	}
}

// dispositionExecutor is the echo executor with a SessionReleaser that
// records the disposition of each release.
type dispositionExecutor struct {
	mu    sync.Mutex
	disps []executor.Disposition
}

func (d *dispositionExecutor) Send(ctx context.Context, id string, m []executor.Message) (executor.Response, error) {
	return executor.NewEchoExecutor().Send(ctx, id, m)
}

func (d *dispositionExecutor) Close(context.Context, string) error { return nil }

func (d *dispositionExecutor) Release(_ context.Context, _ string, disp executor.Disposition) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.disps = append(d.disps, disp)
	return nil
}

func (d *dispositionExecutor) dispositions() []executor.Disposition {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]executor.Disposition(nil), d.disps...)
}

// genBinder creates the session row as the no-op binder does, with a
// non-zero recovery generation, so a test can tell the generation the
// completion write captured from the zero value.
type genBinder struct {
	store sessionstore.Store
	id    string
	gen   int64
}

func (b genBinder) BindSingleShot(ctx context.Context, spec translator.SingleShotSpec) (translator.SingleShotBinding, error) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return translator.SingleShotBinding{SessionID: b.id, RecoveryGeneration: b.gen}, b.store.Create(ctx, sessionstore.Session{
		ID: b.id, TenantID: spec.TenantID, UserID: ownerprincipal.Subject(ctx), RuntimeRef: spec.RuntimeRef,
		State: session.StateRunning, RecoveryGeneration: b.gen, CreatedAt: now, UpdatedAt: now,
	})
}

// TestOpenResponsesCommitsOneCompletedFirstResponse_spec_15 pins the
// completion write of a turn that creates its session: the row carries
// exactly one response record, ordinal 1, completed, whose end sequence is
// the Seq of the last transcript entry the turn appended and whose recovery
// generation is the row's, and the answer's id is <session>.1.
//
// spec: §15 (Open Responses conversation sessions)
func TestOpenResponsesCommitsOneCompletedFirstResponse_spec_15(t *testing.T) {
	store := memstore.New()
	ts := transcriptstore.NewMemory()
	// An entry already on the session's transcript makes the end sequence
	// differ from the turn's own entry count.
	if _, err := ts.Append(context.Background(), "acme", "sess_commit", transcriptstore.Entry{Role: "user", Content: "earlier"}); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h := translator.NewOpenResponsesHandler(store, &captureExecutor{}, translator.OpenResponsesOptions{
		HolderForwarder:  responsesstub.LocalForwarder{},
		SessionAccess:    ownerprincipal.SessionAccess{},
		Terminal:         responsesstub.ReleasingTerminal{Exec: nil, Store: store},
		DevTenantHeader:  true,
		Clock:            func() time.Time { return created },
		SingleShotBinder: genBinder{store: store, id: "sess_commit", gen: 3},
		Transcripts:      ts,
	})
	rr := respPostTenant(t, h.Handler(), "acme", map[string]any{"model": "echo", "input": "hello"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var resp translator.OpenResponsesResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.ID != "sess_commit.1" {
		t.Errorf("response id = %q, want sess_commit.1", resp.ID)
	}

	entries, err := ts.Get(context.Background(), "acme", "sess_commit")
	if err != nil {
		t.Fatal(err)
	}
	lastSeq := entries[len(entries)-1].Seq
	row, err := store.Get(context.Background(), "acme", "sess_commit")
	if err != nil {
		t.Fatal(err)
	}
	if len(row.Responses) != 1 {
		t.Fatalf("responses = %+v, want exactly one record", row.Responses)
	}
	got := row.Responses[0]
	if !got.Completed || got.EndSeq != lastSeq || got.RecoveryGeneration != 3 || !got.CreatedAt.Equal(created) {
		t.Errorf("record = %+v, want completed with EndSeq %d, RecoveryGeneration 3, CreatedAt %v", got, lastSeq, created)
	}
	if lastSeq != 3 {
		t.Errorf("last appended Seq = %d, want 3 (one earlier entry plus the turn's two)", lastSeq)
	}
}

// updateFailStore fails every Update, so the completion write of a turn
// fails after the transcript record succeeded.
type updateFailStore struct {
	sessionstore.Store
}

func (updateFailStore) Update(context.Context, string, string, func(*sessionstore.Session) error) (sessionstore.Session, error) {
	return sessionstore.Session{}, errors.New("session store unavailable")
}

// TestOpenResponsesCommitFailureIs500_spec_15 pins that a failed completion
// write answers a native 500 rather than returning a response whose record
// was never committed, and releases the pod with the failed disposition.
//
// spec: §15 (Open Responses conversation sessions)
func TestOpenResponsesCommitFailureIs500_spec_15(t *testing.T) {
	inner := memstore.New()
	exec := &dispositionExecutor{}
	h := translator.NewOpenResponsesHandler(updateFailStore{Store: inner}, exec, translator.OpenResponsesOptions{
		HolderForwarder: responsesstub.LocalForwarder{},
		SessionAccess:   ownerprincipal.SessionAccess{},
		Terminal:        responsesstub.ReleasingTerminal{Exec: exec, Store: updateFailStore{Store: inner}},
		DevTenantHeader: true,
		Clock:           func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:          func() string { return "sess_nocommit" },
		Transcripts:     transcriptstore.NewMemory(),
	})
	rr := respPostTenant(t, h.Handler(), "acme", map[string]any{"model": "echo", "input": "hello"})
	assertTurnFailed(t, rr, inner, exec, "sess_nocommit")
}

// appendFailTranscripts is a transcript store whose Append always fails, used
// to exercise the required record: a transcript write failure fails the turn.
type appendFailTranscripts struct {
	transcriptstore.Store
}

func (a appendFailTranscripts) Append(context.Context, string, string, ...transcriptstore.Entry) (uint64, error) {
	return 0, errors.New("transcript append failed")
}

// getFailStore wraps a session store and makes Get and GetPrimary return a
// non-ErrNotFound infrastructure error, so a continuation's resolve maps to a 500 rather than
// a fail-closed 404 (which is reserved for unknown or cross-tenant ids).
type getFailStore struct {
	sessionstore.Store
	err error
}

func (g getFailStore) Get(context.Context, string, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, g.err
}

func (g getFailStore) GetPrimary(context.Context, string, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, g.err
}

// TestOpenResponsesContinuationResolveErrorIs500 pins that an infrastructure
// error resolving previous_response_id (a non-ErrNotFound session-store
// failure) surfaces as a native 500 server_error with no dispatch, distinct
// from the fail-closed 404 an unknown or cross-tenant id yields.
// spec: §15 (Open Responses conversation sessions).
func TestOpenResponsesContinuationResolveErrorIs500_spec_15(t *testing.T) {
	exec := &captureExecutor{}
	store := getFailStore{Store: memstore.New(), err: errors.New("session store unavailable")}
	var n int
	h := translator.NewOpenResponsesHandler(store, exec, translator.OpenResponsesOptions{
		HolderForwarder: responsesstub.LocalForwarder{},
		SessionAccess:   ownerprincipal.SessionAccess{},
		Terminal:        responsesstub.ReleasingTerminal{Exec: exec, Store: store},
		DevTenantHeader: true,
		Clock:           func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:          func() string { n++; return fmt.Sprintf("resp_%d", n) },
		Transcripts:     transcriptstore.NewMemory(),
	})
	rr := respPostTenant(t, h.Handler(), "acme", map[string]any{
		"model": "echo", "input": "hi", "previous_response_id": "resp_prev.1",
	})
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want 500; body=%s", rr.Code, rr.Body.String())
	}
	if len(exec.calls) != 0 {
		t.Errorf("exec.Send called %d times; want 0 (no dispatch on resolve error)", len(exec.calls))
	}
}

// TestOpenResponsesGetEchoesLineage pins that a continuation persists
// ContinuationParentID on its session row so GET /v1/responses/{id} echoes the
// originating previous_response_id, and that a chain-root response persists an
// empty ContinuationParentID and echoes empty.
// spec: §15 (Open Responses conversation sessions).
func TestOpenResponsesGetEchoesLineage_spec_15(t *testing.T) {
	exec := &captureExecutor{}
	h, store := newContinuityHandler(t, exec, transcriptstore.NewMemory())

	// Chain root: no previous_response_id.
	if rr := respPostTenant(t, h.Handler(), "acme", map[string]any{
		"model": "echo", "input": "one",
	}); rr.Code != http.StatusOK {
		t.Fatalf("root: %d, body=%s", rr.Code, rr.Body.String())
	}
	// Continuation off resp_1.1.
	if rr := respPostTenant(t, h.Handler(), "acme", map[string]any{
		"model": "echo", "input": "two", "previous_response_id": "resp_1.1",
	}); rr.Code != http.StatusOK {
		t.Fatalf("continuation: %d, body=%s", rr.Code, rr.Body.String())
	}

	rootRow, _ := store.Get(context.Background(), "acme", "resp_1")
	if rootRow.ContinuationParentID != "" {
		t.Errorf("chain-root ContinuationParentID: got %q, want empty", rootRow.ContinuationParentID)
	}
	contRow, _ := store.Get(context.Background(), "acme", "resp_2")
	if contRow.ContinuationParentID != "resp_1.1" {
		t.Errorf("continuation ContinuationParentID: got %q, want %q", contRow.ContinuationParentID, "resp_1.1")
	}

	// GET echoes the persisted lineage; a chain-root response echoes empty.
	if got := getPreviousResponseID(t, h, "resp_2.1"); got != "resp_1.1" {
		t.Errorf("GET resp_2.1 previous_response_id: got %q, want %q", got, "resp_1.1")
	}
	if got := getPreviousResponseID(t, h, "resp_1.1"); got != "" {
		t.Errorf("GET resp_1.1 previous_response_id: got %q, want empty", got)
	}
}

func getPreviousResponseID(t *testing.T, h *translator.OpenResponsesHandler, id string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/responses/"+id, nil)
	req = req.WithContext(ownerprincipal.Context(req.Context(), ownerprincipal.Alice))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s status: %d", id, rr.Code)
	}
	var got translator.OpenResponsesResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	return got.PreviousResponseID
}

// TestOpenResponsesGetInProgressTurnIs404_spec_15 pins that GET answers
// completed responses only: a session waiting in input_required, a §7.2
// sub-state of running, whose turn has not completed has no completed record
// at that ordinal, so GET answers the native 404 rather than in_progress, and
// a bare session id is not a response id.
//
// spec: §15 (Open Responses conversation sessions), §7.2 (Session state machine)
func TestOpenResponsesGetInProgressTurnIs404_spec_15(t *testing.T) {
	h, store := newResponsesHandler(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "resp_wait", TenantID: "acme", State: session.StateInputRequired,
		RuntimeRef: "echo", CreatedAt: now, UpdatedAt: now,
		Responses: []sessionstore.ConversationResponse{{CreatedAt: now}},
	})
	for _, id := range []string{"resp_wait.1", "resp_wait"} {
		if rr := getResponse(t, h, "acme", id); rr.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 for a turn with no completed record", id, rr.Code)
		}
	}
}
