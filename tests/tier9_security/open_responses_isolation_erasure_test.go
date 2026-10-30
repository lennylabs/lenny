// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security tests for Open Responses response resolution: tenant
// isolation of GET /v1/responses/{id} and of previous_response_id, and the
// behaviour of the conversation-context walk after a §12.8 erasure removes
// a session row or its transcript.
//
// spec: §15 (Open Responses conversation sessions), §4.2 (session-store
// tenant isolation), §12.8 (Compliance Interfaces)
package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// orRecordingExecutor records every message slice it is sent and answers
// each Send with a distinct reply.
type orRecordingExecutor struct {
	mu    sync.Mutex
	calls [][]executor.Message
}

func (e *orRecordingExecutor) Send(_ context.Context, _ string, msgs []executor.Message) (executor.Response, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, append([]executor.Message(nil), msgs...))
	return executor.Response{Parts: []executor.MessagePart{{Type: "text", Text: fmt.Sprintf("reply%d", len(e.calls))}}}, nil
}

func (e *orRecordingExecutor) Close(context.Context, string) error { return nil }

func (e *orRecordingExecutor) sends() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

func (e *orRecordingExecutor) last() []executor.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls[len(e.calls)-1]
}

type orFixture struct {
	handler     http.Handler
	sessions    sessionstore.Store
	transcripts *transcriptstore.Memory
	exec        *orRecordingExecutor
}

func newORFixture(t *testing.T) *orFixture {
	t.Helper()
	f := &orFixture{sessions: memstore.New(), transcripts: transcriptstore.NewMemory(), exec: &orRecordingExecutor{}}
	f.handler = translator.NewOpenResponsesHandler(f.sessions, f.exec, translator.OpenResponsesOptions{
		DevTenantHeader: true,
		Clock:           func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Transcripts:     f.transcripts,
	}).Handler()
	return f
}

func (f *orFixture) do(t *testing.T, method, path, tenant string, body any) (int, []byte) {
	t.Helper()
	var buf []byte
	if body != nil {
		buf, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(buf))
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	return rr.Code, rr.Body.Bytes()
}

// post creates a response and returns its id.
func (f *orFixture) post(t *testing.T, tenant, input, prev string) string {
	t.Helper()
	body := map[string]any{"model": "echo", "input": input}
	if prev != "" {
		body["previous_response_id"] = prev
	}
	code, raw := f.do(t, http.MethodPost, "/v1/responses", tenant, body)
	if code != http.StatusOK {
		t.Fatalf("POST %q (prev %q): %d, body=%s", input, prev, code, raw)
	}
	var out struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &out)
	return out.ID
}

func orSessionOf(t *testing.T, responseID string) string {
	t.Helper()
	for i := len(responseID) - 1; i > 0; i-- {
		if responseID[i] == '.' {
			return responseID[:i]
		}
	}
	t.Fatalf("response id %q has no separator", responseID)
	return ""
}

// spec: §15 (Open Responses conversation sessions), §4.2 (session-store tenant isolation)
// diagnosis: a cross-tenant GET /v1/responses/{id} or previous_response_id
// that answers anything other than the byte-identical native 404 an unknown
// id gets, or that dispatches the other tenant's conversation to the
// runtime, is a tenant-isolation breach in response resolution
// (pkg/gateway/environment/translator/continuity.go resolve).
func TestOpenResponsesCrossTenantResponseIsIndistinguishableFromUnknown_spec_15(t *testing.T) {
	f := newORFixture(t)
	owned := f.post(t, "globex", "globex secret", "")
	unknown := orSessionOf(t, owned) + "0.1"

	ownerCode, _ := f.do(t, http.MethodGet, "/v1/responses/"+owned, "globex", nil)
	if ownerCode != http.StatusOK {
		t.Fatalf("owner GET of its own response: %d", ownerCode)
	}

	crossCode, crossBody := f.do(t, http.MethodGet, "/v1/responses/"+owned, "acme", nil)
	unknownCode, unknownBody := f.do(t, http.MethodGet, "/v1/responses/"+unknown, "acme", nil)
	if crossCode != http.StatusNotFound || unknownCode != http.StatusNotFound {
		t.Fatalf("GET cross-tenant = %d, unknown = %d, want 404 for both", crossCode, unknownCode)
	}
	if !bytes.Equal(crossBody, unknownBody) {
		t.Errorf("cross-tenant GET body differs from unknown-id body:\n%s\n%s", crossBody, unknownBody)
	}
	if bytes.Contains(crossBody, []byte("globex secret")) {
		t.Errorf("cross-tenant GET leaked the owner's input: %s", crossBody)
	}

	sendsBefore := f.exec.sends()
	crossCode, crossBody = f.do(t, http.MethodPost, "/v1/responses", "acme",
		map[string]any{"model": "echo", "input": "hi", "previous_response_id": owned})
	unknownCode, unknownBody = f.do(t, http.MethodPost, "/v1/responses", "acme",
		map[string]any{"model": "echo", "input": "hi", "previous_response_id": unknown})
	if crossCode != http.StatusNotFound || unknownCode != http.StatusNotFound {
		t.Fatalf("POST cross-tenant = %d, unknown = %d, want 404 for both", crossCode, unknownCode)
	}
	if !bytes.Equal(crossBody, unknownBody) {
		t.Errorf("cross-tenant continuation body differs from unknown-id body:\n%s\n%s", crossBody, unknownBody)
	}
	if got := f.exec.sends() - sendsBefore; got != 0 {
		t.Errorf("a rejected continuation dispatched %d turns, want 0", got)
	}
}

// spec: §15 (Open Responses conversation sessions), §12.8 (Compliance Interfaces)
// diagnosis: after an erasure, a continuation that still dispatches the
// erased session's turns, that fails instead of stopping at the gap, or a
// head whose row was erased that answers anything but the native 404, means
// the lineage walk in pkg/gateway/environment/translator/continuity.go
// rehydrate reads past §12.8 erasure or fails open on the head.
func TestOpenResponsesErasureBoundsConversationContext_spec_12_8(t *testing.T) {
	ctx := context.Background()
	msgs := func(contents ...string) []executor.Message {
		out := make([]executor.Message, 0, len(contents))
		for i, c := range contents {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			out = append(out, executor.Message{Role: role, Content: c})
		}
		return out
	}
	assertSent := func(t *testing.T, got, want []executor.Message) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("dispatched %+v, want %+v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("dispatched %+v, want %+v", got, want)
			}
		}
	}

	t.Run("erased ancestor session stops the walk at the gap", func(t *testing.T) {
		f := newORFixture(t)
		root := f.post(t, "acme", "one", "")
		mid := f.post(t, "acme", "two", root)
		rootSession := orSessionOf(t, root)
		if err := f.sessions.Delete(ctx, "acme", rootSession); err != nil {
			t.Fatal(err)
		}
		if _, err := f.transcripts.DeleteBySession(ctx, "acme", rootSession); err != nil {
			t.Fatal(err)
		}
		f.post(t, "acme", "three", mid)
		assertSent(t, f.exec.last(), msgs("two", "reply2", "three"))
	})

	t.Run("erased head session answers 404 without dispatch", func(t *testing.T) {
		f := newORFixture(t)
		head := f.post(t, "acme", "one", "")
		if err := f.sessions.Delete(ctx, "acme", orSessionOf(t, head)); err != nil {
			t.Fatal(err)
		}
		sends := f.exec.sends()
		code, raw := f.do(t, http.MethodPost, "/v1/responses", "acme",
			map[string]any{"model": "echo", "input": "two", "previous_response_id": head})
		if code != http.StatusNotFound {
			t.Fatalf("continuation of an erased head: %d, want 404; body=%s", code, raw)
		}
		if f.exec.sends() != sends {
			t.Errorf("continuation of an erased head dispatched a turn")
		}
		if code, _ := f.do(t, http.MethodGet, "/v1/responses/"+head, "acme", nil); code != http.StatusNotFound {
			t.Errorf("GET of an erased head: %d, want 404", code)
		}
	})

	t.Run("transcript erased under a surviving row contributes nothing and the walk continues", func(t *testing.T) {
		f := newORFixture(t)
		root := f.post(t, "acme", "one", "")
		mid := f.post(t, "acme", "two", root)
		if _, err := f.transcripts.DeleteBySession(ctx, "acme", orSessionOf(t, mid)); err != nil {
			t.Fatal(err)
		}
		f.post(t, "acme", "three", mid)
		assertSent(t, f.exec.last(), msgs("one", "reply1", "three"))
	})
}
