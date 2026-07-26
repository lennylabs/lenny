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

func newResponsesHandler(t *testing.T) (*translator.OpenResponsesHandler, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	h := translator.NewOpenResponsesHandler(store, executor.NewEchoExecutor(), translator.OpenResponsesOptions{
		Clock:  func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc: func() string { return "resp_1" },
	})
	return h, store
}

func respPost(t *testing.T, h http.Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(buf))
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
	if resp.ID != "resp_1" || resp.Object != "response" {
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
}

func TestOpenResponsesGet(t *testing.T) {
	h, store := newResponsesHandler(t)
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "resp_1", TenantID: "acme", State: session.StateCompleted,
		RuntimeRef: "echo",
		CreatedAt:  time.Now(), UpdatedAt: time.Now(),
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_1", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp translator.OpenResponsesResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Status != "completed" || resp.Model != "echo" {
		t.Errorf("resp: %+v", resp)
	}
}

func TestOpenResponsesGetMissing(t *testing.T) {
	h, _ := newResponsesHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/responses/missing", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing: %d", rr.Code)
	}
}

func TestOpenResponsesDelete(t *testing.T) {
	h, store := newResponsesHandler(t)
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "resp_1", TenantID: "acme",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	req := httptest.NewRequest(http.MethodDelete, "/v1/responses/resp_1", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("delete: %d", rr.Code)
	}
	if _, err := store.Get(context.Background(), "acme", "resp_1"); err == nil {
		t.Error("response should be deleted")
	}
}

// TestOpenResponsesAdapterCapabilities pins §9.1 line 35 + §15.1
// line 575: the Open Responses adapter publishes its own capability
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
		Clock:       func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:      func() string { n++; return fmt.Sprintf("resp_%d", n) },
		Transcripts: ts,
	})
	return h, store
}

func respPostTenant(t *testing.T, h http.Handler, tenant string, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(buf))
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
		"previous_response_id": "resp_missing",
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
	// acme references globex's resp_1: cross-tenant, must fail closed.
	rr := respPostTenant(t, h.Handler(), "acme", map[string]any{
		"model": "echo", "input": "hi", "previous_response_id": "resp_1",
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant status: got %d want 404; body=%s", rr.Code, rr.Body.String())
	}
	if len(exec.calls) != callsBefore {
		t.Errorf("cross-tenant continuation dispatched %d turns; want 0", len(exec.calls)-callsBefore)
	}
}

// TestOpenResponsesChainWalkPrependsPriorConversation pins the full-history
// replay: across a three-turn chain the continuation's dispatch receives the
// prior conversation prepended ahead of the new turn in chronological order
// (root first), assembled by walking the ContinuationParentID chain across the
// per-response single-turn buckets, and each bucket holds only its own turn
// (no copy-forward).
// spec: §15 (server-side previous_response_id continuation, full-history replay).
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
		"model": "echo", "input": "two", "previous_response_id": "resp_1",
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
		"model": "echo", "input": "three", "previous_response_id": "resp_2",
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
// referenced response exists but recorded no transcript dispatches with an
// empty prior history (no prepend) and returns 200, rather than a 404.
// spec: §15 (continuation resolution); transcriptstore ErrNotFound tolerated.
func TestOpenResponsesContinuationEmptyTranscript_spec_15(t *testing.T) {
	exec := &captureExecutor{}
	ts := transcriptstore.NewMemory()
	h, store := newContinuityHandler(t, exec, ts)
	// A referenced response row that exists but has no recorded transcript.
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "resp_seed", TenantID: "acme", State: session.StateCompleted,
		RuntimeRef: "echo", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := respPostTenant(t, h.Handler(), "acme", map[string]any{
		"model": "echo", "input": "hi", "previous_response_id": "resp_seed",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
	assertMessages(t, exec.lastCall(t), []executor.Message{
		{Role: "user", Content: "hi"},
	})
}

// TestOpenResponsesRecordsChainRootTurn pins that a chain-root turn carrying
// no previous_response_id is still recorded on its own session id (so a later
// continuation can rehydrate it), and that a best-effort transcript append
// failure does not fail the response (it still returns 200).
// spec: §15 (continuation lineage persisted); §15.1 (best-effort transcript write).
func TestOpenResponsesRecordsChainRootTurn_spec_15_1(t *testing.T) {
	// Chain-root turn is recorded.
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
		t.Errorf("chain-root bucket: %+v", entries)
	}

	// Best-effort append failure still returns 200.
	failExec := &captureExecutor{}
	failStore := memstore.New()
	var n int
	failH := translator.NewOpenResponsesHandler(failStore, failExec, translator.OpenResponsesOptions{
		Clock:       func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:      func() string { n++; return fmt.Sprintf("resp_%d", n) },
		Transcripts: appendFailTranscripts{Store: transcriptstore.NewMemory()},
	})
	rr := respPostTenant(t, failH.Handler(), "acme", map[string]any{
		"model": "echo", "input": "one",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("append-failure response: got %d want 200; body=%s", rr.Code, rr.Body.String())
	}
}

// appendFailTranscripts is a transcript store whose Append always fails, used
// to exercise the best-effort record path: a transcript write failure must not
// fail the response.
type appendFailTranscripts struct {
	transcriptstore.Store
}

func (a appendFailTranscripts) Append(context.Context, string, string, ...transcriptstore.Entry) error {
	return errors.New("transcript append failed")
}

// getFailStore wraps a session store and makes Get return a non-ErrNotFound
// infrastructure error, so a continuation's resolve maps to a 500 rather than
// a fail-closed 404 (which is reserved for unknown or cross-tenant ids).
type getFailStore struct {
	sessionstore.Store
	err error
}

func (g getFailStore) Get(context.Context, string, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, g.err
}

// TestOpenResponsesContinuationResolveErrorIs500 pins that an infrastructure
// error resolving previous_response_id (a non-ErrNotFound session-store
// failure) surfaces as a native 500 server_error with no dispatch, distinct
// from the fail-closed 404 an unknown or cross-tenant id yields.
// spec: §15 (continuation resolution).
func TestOpenResponsesContinuationResolveErrorIs500_spec_15(t *testing.T) {
	exec := &captureExecutor{}
	store := getFailStore{Store: memstore.New(), err: errors.New("session store unavailable")}
	var n int
	h := translator.NewOpenResponsesHandler(store, exec, translator.OpenResponsesOptions{
		Clock:       func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:      func() string { n++; return fmt.Sprintf("resp_%d", n) },
		Transcripts: transcriptstore.NewMemory(),
	})
	rr := respPostTenant(t, h.Handler(), "acme", map[string]any{
		"model": "echo", "input": "hi", "previous_response_id": "resp_prev",
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
// empty ContinuationParentID and echoes empty. This replaces the earlier
// marker test that pinned the deferred behavior (empty GET echo).
// spec: §15 (continuation lineage persisted, GET echo).
func TestOpenResponsesGetEchoesLineage_spec_15(t *testing.T) {
	exec := &captureExecutor{}
	h, store := newContinuityHandler(t, exec, transcriptstore.NewMemory())

	// Chain root: no previous_response_id.
	if rr := respPostTenant(t, h.Handler(), "acme", map[string]any{
		"model": "echo", "input": "one",
	}); rr.Code != http.StatusOK {
		t.Fatalf("root: %d, body=%s", rr.Code, rr.Body.String())
	}
	// Continuation off resp_1.
	if rr := respPostTenant(t, h.Handler(), "acme", map[string]any{
		"model": "echo", "input": "two", "previous_response_id": "resp_1",
	}); rr.Code != http.StatusOK {
		t.Fatalf("continuation: %d, body=%s", rr.Code, rr.Body.String())
	}

	rootRow, _ := store.Get(context.Background(), "acme", "resp_1")
	if rootRow.ContinuationParentID != "" {
		t.Errorf("chain-root ContinuationParentID: got %q, want empty", rootRow.ContinuationParentID)
	}
	contRow, _ := store.Get(context.Background(), "acme", "resp_2")
	if contRow.ContinuationParentID != "resp_1" {
		t.Errorf("continuation ContinuationParentID: got %q, want %q", contRow.ContinuationParentID, "resp_1")
	}

	// GET echoes the persisted lineage; a chain-root row echoes empty.
	if got := getPreviousResponseID(t, h, "resp_2"); got != "resp_1" {
		t.Errorf("GET resp_2 previous_response_id: got %q, want %q", got, "resp_1")
	}
	if got := getPreviousResponseID(t, h, "resp_1"); got != "" {
		t.Errorf("GET resp_1 previous_response_id: got %q, want empty", got)
	}
}

func getPreviousResponseID(t *testing.T, h *translator.OpenResponsesHandler, id string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/responses/"+id, nil)
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
