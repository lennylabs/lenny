// SPDX-License-Identifier: MIT

package translator_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/translator"
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

func TestOpenAIChatRejectsStream(t *testing.T) {
	h, _ := newOpenAIHandler(t)
	rr := openaiPost(t, h.Handler(), translator.OpenAIChatCompletionsRequest{
		Model:    "echo",
		Stream:   true,
		Messages: []translator.OpenAIChatMessage{{Role: "user", Content: "x"}},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("stream=true: got %d, want 400", rr.Code)
	}
	var env translator.OpenAIError
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if env.Error.Type != "unsupported_value" {
		t.Errorf("error type: %q", env.Error.Type)
	}
}

func TestOpenAIChatDefaultsRuntimeWhenModelEmpty(t *testing.T) {
	store := memstore.New()
	h := translator.NewOpenAIChatHandler(store, executor.NewEchoExecutor(), translator.OpenAIChatOptions{
		IDFunc: func() string { return "sess_def" },
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
