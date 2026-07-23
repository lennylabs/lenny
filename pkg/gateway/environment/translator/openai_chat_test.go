// SPDX-License-Identifier: MIT

package translator_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

func newOpenAIHandler(t *testing.T) (*translator.OpenAIChatHandler, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	h := translator.NewOpenAIChatHandler(store, executor.NewEchoExecutor(), translator.OpenAIChatOptions{
		Clock:  func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc: func() string { return "sess_oa_1" },
	})
	return h, store
}

func openaiPost(t *testing.T, h http.Handler, body translator.OpenAIChatCompletionsRequest) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestOpenAIChatHappyPath(t *testing.T) {
	h, store := newOpenAIHandler(t)
	rr := openaiPost(t, h.Handler(), translator.OpenAIChatCompletionsRequest{
		Model: "echo",
		Messages: []translator.OpenAIChatMessage{
			{Role: "user", Content: "hello"},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp translator.OpenAIChatCompletionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Object != "chat.completion" {
		t.Errorf("object: %q", resp.Object)
	}
	if resp.Model != "echo" {
		t.Errorf("model echo: got %q", resp.Model)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Role != "assistant" {
		t.Fatalf("choices: %+v", resp.Choices)
	}
	if !strings.Contains(resp.Choices[0].Message.Content, "hello") {
		t.Errorf("content does not echo input: %q", resp.Choices[0].Message.Content)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason: %q", resp.Choices[0].FinishReason)
	}

	// Session row should be persisted as terminal `completed` after
	// the round-trip.
	row, err := store.Get(context.Background(), "acme", "sess_oa_1")
	if err != nil {
		t.Fatalf("session not stored: %v", err)
	}
	if row.State != session.StateCompleted {
		t.Errorf("session state: got %q, want completed", row.State)
	}
}

func TestOpenAIChatRejectsEmptyMessages(t *testing.T) {
	h, _ := newOpenAIHandler(t)
	rr := openaiPost(t, h.Handler(), translator.OpenAIChatCompletionsRequest{
		Model: "echo",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("empty messages: got %d, want 400", rr.Code)
	}
	var env translator.OpenAIError
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if env.Error.Type != "invalid_request_error" {
		t.Errorf("error type: %q", env.Error.Type)
	}
}

func TestOpenAIChatStreamEmitsSSEChunks(t *testing.T) {
	h, _ := newOpenAIHandler(t)
	rr := openaiPost(t, h.Handler(), translator.OpenAIChatCompletionsRequest{
		Model:    "echo",
		Stream:   true,
		Messages: []translator.OpenAIChatMessage{{Role: "user", Content: "hello world"}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("stream status: %d, body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type: %q, want text/event-stream", ct)
	}
	body := rr.Body.String()
	// First frame carries the assistant role.
	if !strings.Contains(body, `"role":"assistant"`) {
		t.Error("stream missing assistant role delta")
	}
	// Content frames carry the echoed input tokens.
	if !strings.Contains(body, "hello") || !strings.Contains(body, "world") {
		t.Errorf("stream content missing echoed tokens; body:\n%s", body)
	}
	// Final frame carries finish_reason=stop and the [DONE] sentinel.
	if !strings.Contains(body, `"finish_reason":"stop"`) {
		t.Error("stream missing finish_reason=stop")
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Error("stream missing [DONE] sentinel")
	}
}

func TestOpenAIChatDefaultsRuntimeWhenModelEmpty(t *testing.T) {
	store := memstore.New()
	h := translator.NewOpenAIChatHandler(store, executor.NewEchoExecutor(), translator.OpenAIChatOptions{
		IDFunc:         func() string { return "sess_def" },
		DefaultRuntime: "echo",
	})
	rr := openaiPost(t, h.Handler(), translator.OpenAIChatCompletionsRequest{
		Messages: []translator.OpenAIChatMessage{{Role: "user", Content: "x"}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp translator.OpenAIChatCompletionsResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Model != "echo" {
		t.Errorf("default model: got %q", resp.Model)
	}
}

func TestOpenAIChatRejectsMalformedJSON(t *testing.T) {
	h, _ := newOpenAIHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte("not-json")))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("malformed: got %d, want 400", rr.Code)
	}
}

func TestOpenAIChatRoundTripsMultipleMessages(t *testing.T) {
	h, _ := newOpenAIHandler(t)
	rr := openaiPost(t, h.Handler(), translator.OpenAIChatCompletionsRequest{
		Model: "echo",
		Messages: []translator.OpenAIChatMessage{
			{Role: "system", Content: "you are an echo"},
			{Role: "user", Content: "hello"},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp translator.OpenAIChatCompletionsResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if !strings.Contains(resp.Choices[0].Message.Content, "hello") {
		t.Errorf("content: %q", resp.Choices[0].Message.Content)
	}
}

func TestOpenAIChatDisabledWhenExecutorMissing(t *testing.T) {
	store := memstore.New()
	h := translator.NewOpenAIChatHandler(store, nil, translator.OpenAIChatOptions{})
	rr := openaiPost(t, h.Handler(), translator.OpenAIChatCompletionsRequest{
		Model:    "echo",
		Messages: []translator.OpenAIChatMessage{{Role: "user", Content: "x"}},
	})
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("no executor: got %d, want 503", rr.Code)
	}
}

// stubSingleShotBinder is a translator.SingleShotBinder that returns a
// fixed session id, or a fixed error, without claiming a pod. It fabricates
// the binder failure the adapter maps into its native error envelope so the
// tier-1 mapping can be exercised in-process.
type stubSingleShotBinder struct {
	id  string
	err error
}

func (b stubSingleShotBinder) BindSingleShot(_ context.Context, _ translator.SingleShotSpec) (string, error) {
	if b.err != nil {
		return "", b.err
	}
	return b.id, nil
}

// recordingStore wraps a sessionstore.Store and captures the State of the
// first row passed to Create, so a test can observe the state the no-op
// binder persisted (running) before the handler updates it to completed.
type recordingStore struct {
	sessionstore.Store
	createdState session.State
	created      bool
}

func (s *recordingStore) Create(ctx context.Context, sess sessionstore.Session) error {
	if !s.created {
		s.createdState = sess.State
		s.created = true
	}
	return s.Store.Create(ctx, sess)
}

// TestOpenAIChatSingleShotBinderErrorEnvelope pins the binder-error-to-envelope
// mapping: a retryable pool-claim exhaustion surfaces its status, error.code,
// and a Retry-After header; a non-retryable credential-pool exhaustion surfaces
// its code with no Retry-After header; an admission-gate rejection surfaces the
// gate's own status and code; and any non-typed failure falls back to a
// 500 server_error with no code. The adapter fails closed without dispatching.
// spec: §15 (retryable claim failure mapping); §7.1; §4.9.
func TestOpenAIChatSingleShotBinderErrorEnvelope(t *testing.T) {
	cases := []struct {
		name           string
		err            error
		wantStatus     int
		wantCode       string
		wantType       string
		wantRetryAfter string
	}{
		{
			name:           "pool_claim_exhaustion_retryable",
			err:            &translator.SingleShotError{HTTPStatus: 503, Code: "SESSION_CREATION_FAILED", Message: "pool exhausted", RetryAfterSeconds: 7, Retryable: true},
			wantStatus:     http.StatusServiceUnavailable,
			wantCode:       "SESSION_CREATION_FAILED",
			wantType:       "server_error",
			wantRetryAfter: "7",
		},
		{
			name:           "credential_pool_exhaustion_no_retry_after",
			err:            &translator.SingleShotError{HTTPStatus: 503, Code: "CREDENTIAL_POOL_EXHAUSTED", Message: "no credential", RetryAfterSeconds: 0},
			wantStatus:     http.StatusServiceUnavailable,
			wantCode:       "CREDENTIAL_POOL_EXHAUSTED",
			wantType:       "server_error",
			wantRetryAfter: "",
		},
		{
			name:           "admission_gate_rejection",
			err:            &translator.SingleShotError{HTTPStatus: 403, Code: "ENVIRONMENT_ADMISSION_DENIED", Message: "denied"},
			wantStatus:     http.StatusForbidden,
			wantCode:       "ENVIRONMENT_ADMISSION_DENIED",
			wantType:       "invalid_request_error",
			wantRetryAfter: "",
		},
		{
			name:           "opaque_failure_falls_back_to_server_error",
			err:            errors.New("boom"),
			wantStatus:     http.StatusInternalServerError,
			wantCode:       "",
			wantType:       "server_error",
			wantRetryAfter: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			h := translator.NewOpenAIChatHandler(store, executor.NewEchoExecutor(), translator.OpenAIChatOptions{
				SingleShotBinder: stubSingleShotBinder{err: tc.err},
			})
			rr := openaiPost(t, h.Handler(), translator.OpenAIChatCompletionsRequest{
				Model:    "echo",
				Messages: []translator.OpenAIChatMessage{{Role: "user", Content: "x"}},
			})
			if rr.Code != tc.wantStatus {
				t.Fatalf("status: got %d, want %d, body=%s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			var env translator.OpenAIError
			if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if env.Error.Code != tc.wantCode {
				t.Errorf("error.code: got %q, want %q", env.Error.Code, tc.wantCode)
			}
			if env.Error.Type != tc.wantType {
				t.Errorf("error.type: got %q, want %q", env.Error.Type, tc.wantType)
			}
			if got := rr.Header().Get("Retry-After"); got != tc.wantRetryAfter {
				t.Errorf("Retry-After: got %q, want %q", got, tc.wantRetryAfter)
			}
		})
	}
}

// TestOpenAIChatNoopBinderEchoFallback pins the default no-op binder: a
// handler constructed with no injected SingleShotBinder persists a running
// row through its own store (replicating the inline store.Create the handler
// performed before the single-shot path existed) and round-trips the echo
// response on the one code path, so the in-memory §17.4 behavior is unchanged.
// spec: §17.4; §15.
func TestOpenAIChatNoopBinderEchoFallback(t *testing.T) {
	store := &recordingStore{Store: memstore.New()}
	h := translator.NewOpenAIChatHandler(store, executor.NewEchoExecutor(), translator.OpenAIChatOptions{
		Clock:  func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc: func() string { return "sess_noop_1" },
	})
	rr := openaiPost(t, h.Handler(), translator.OpenAIChatCompletionsRequest{
		Model:    "echo",
		Messages: []translator.OpenAIChatMessage{{Role: "user", Content: "hello"}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp translator.OpenAIChatCompletionsResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if !strings.Contains(resp.Choices[0].Message.Content, "hello") {
		t.Errorf("echo content: %q", resp.Choices[0].Message.Content)
	}
	// The no-op binder created the row as running before the handler's
	// completion update flipped it to completed.
	if store.createdState != session.StateRunning {
		t.Errorf("no-op binder created state: got %q, want running", store.createdState)
	}
	row, err := store.Get(context.Background(), "acme", "sess_noop_1")
	if err != nil {
		t.Fatalf("session not persisted by no-op binder: %v", err)
	}
	if row.State != session.StateCompleted {
		t.Errorf("final session state: got %q, want completed", row.State)
	}
}

// TestOpenAIChatAdapterCapabilities pins §9.1 line 35 + §15.1 line 575:
// the OpenAI Chat Completions adapter publishes its own capability
// block. The OpenAI envelope is stateless and exposes none of the
// Lenny session-continuity, delegation, elicitation, or interrupt
// surfaces. F-9.1.6 / F-9.1.8.
func TestOpenAIChatAdapterCapabilities_spec_9_1_35(t *testing.T) {
	caps := translator.OpenAIChatAdapterCapabilities()
	if caps.PathPrefix != "/v1/chat/completions" {
		t.Errorf("PathPrefix: %q", caps.PathPrefix)
	}
	if caps.Protocol != "openai-completions" {
		t.Errorf("Protocol: %q", caps.Protocol)
	}
	if caps.SupportsSessionContinuity {
		t.Error("OpenAI Chat does not thread Lenny session continuity across calls")
	}
	if caps.SupportsDelegation || caps.SupportsElicitation || caps.SupportsInterrupt {
		t.Errorf("OpenAI Chat exposes no Lenny surfaces: %+v", caps)
	}
}
