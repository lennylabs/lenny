// SPDX-License-Identifier: MIT

package translator_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
)

// TestOpenAIChatScopedModelNamespace_spec_10_6_557 verifies the OpenAI
// Chat surface honors the §10.6 scoped model namespace: a model named
// "environments/{name}/{model}" stamps the session environment and uses
// the bare model as the runtime reference.
func TestOpenAIChatScopedModelNamespace_spec_10_6_557(t *testing.T) {
	h, store := newOpenAIHandler(t)
	rr := openaiPost(t, h.Handler(), translator.OpenAIChatCompletionsRequest{
		Model: "environments/security-team/echo",
		Messages: []translator.OpenAIChatMessage{
			{Role: "user", Content: "hi"},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), "acme", "sess_oa_1")
	if err != nil {
		t.Fatalf("session not stored: %v", err)
	}
	if row.Environment != "security-team" {
		t.Fatalf("session Environment = %q, want security-team", row.Environment)
	}
	if row.RuntimeRef != "echo" {
		t.Fatalf("session RuntimeRef = %q, want echo (bare model)", row.RuntimeRef)
	}
}

// TestOpenResponsesScopedModelNamespace_spec_10_6_557 verifies the same
// for the Open Responses surface.
func TestOpenResponsesScopedModelNamespace_spec_10_6_557(t *testing.T) {
	h, store := newResponsesHandler(t)
	rr := respPost(t, h.Handler(), map[string]any{
		"model": "environments/security-team/echo",
		"input": "hello",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), "acme", "resp_1")
	if err != nil {
		t.Fatalf("session not stored: %v", err)
	}
	if row.Environment != "security-team" {
		t.Fatalf("session Environment = %q, want security-team", row.Environment)
	}
	if row.RuntimeRef != "echo" {
		t.Fatalf("session RuntimeRef = %q, want echo (bare model)", row.RuntimeRef)
	}
}

// TestOpenAIChatPlainModelUnscoped_spec_10_6_557 confirms a plain model
// leaves the session environment empty.
func TestOpenAIChatPlainModelUnscoped_spec_10_6_557(t *testing.T) {
	h, store := newOpenAIHandler(t)
	rr := openaiPost(t, h.Handler(), translator.OpenAIChatCompletionsRequest{
		Model:    "echo",
		Messages: []translator.OpenAIChatMessage{{Role: "user", Content: "hi"}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme", "sess_oa_1")
	if row.Environment != "" {
		t.Fatalf("session Environment = %q, want empty for a plain model", row.Environment)
	}
}
