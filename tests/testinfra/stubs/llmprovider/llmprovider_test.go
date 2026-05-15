// SPDX-License-Identifier: MIT

package llmprovider_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/stubs/llmprovider"
)

// spec: 12.2.3 (mock LLM provider returns canonical Anthropic shape)
// diagnosis: Non-streaming Anthropic echo failed. The mock provider's
//
//	/v1/messages handler is wrong or extractLastUserMessage
//	missed the user content.
func TestAnthropicEchoesUser(t *testing.T) {
	t.Parallel()
	llm := llmprovider.New(t)

	body, _ := json.Marshal(map[string]any{
		"model": "claude-3-opus",
		"messages": []map[string]any{
			{"role": "user", "content": "hello world"},
		},
	})
	resp, err := http.Post(llm.URL()+"/v1/messages", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, raw)
	}
	content, _ := out["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content length: want 1, got %d", len(content))
	}
	first, _ := content[0].(map[string]any)
	if first["text"] != "hello world" {
		t.Errorf("echo: want %q, got %v", "hello world", first["text"])
	}
}

// spec: 12.2.3 (streaming Anthropic emits the documented SSE sequence)
// diagnosis: The streaming response did not produce the expected SSE
//
//	event sequence. Either the body isn't being marked as a
//	stream, or writeSSE skipped frames.
func TestAnthropicStreaming(t *testing.T) {
	t.Parallel()
	llm := llmprovider.New(t)

	body, _ := json.Marshal(map[string]any{
		"model":    "claude-3-opus",
		"stream":   true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	resp, err := http.Post(llm.URL()+"/v1/messages", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	events, err := llmprovider.ReadSSE(resp.Body)
	if err != nil {
		t.Fatalf("read SSE: %v", err)
	}
	want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if len(events) != len(want) {
		t.Fatalf("event count: want %d, got %d (%+v)", len(want), len(events), events)
	}
	for i, w := range want {
		if events[i].Event != w {
			t.Errorf("event[%d]: want %q, got %q", i, w, events[i].Event)
		}
	}
}

// spec: 12.2.3 (request recording is faithful)
// diagnosis: The recorded body did not match the inbound request. The
//
//	stub's request recorder is dropping bytes or normalising.
func TestRequestRecording(t *testing.T) {
	t.Parallel()
	llm := llmprovider.New(t)
	body, _ := json.Marshal(map[string]any{"messages": []map[string]any{{"role": "user", "content": "x"}}})
	resp, err := http.Post(llm.URL()+"/v1/messages", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	last, ok := llm.LastRequest()
	if !ok {
		t.Fatal("no recorded request")
	}
	if !strings.Contains(string(last.Body), `"role":"user"`) {
		t.Errorf("recorded body lost user role: %s", last.Body)
	}
}

// spec: 15.3 (Google / Gemini provider in the multi-provider LLM proxy)
// diagnosis: A request to the Gemini :generateContent endpoint
//
//	must return the documented candidates[].content.parts[].text
//	shape with the echo of the user message.
func TestGoogleEchoesUser(t *testing.T) {
	t.Parallel()
	llm := llmprovider.New(t)

	body, _ := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{
				"role":  "user",
				"parts": []map[string]any{{"text": "hello gemini"}},
			},
		},
	})
	resp, err := http.Post(llm.URL()+"/v1/models/gemini-1.5-pro:generateContent",
		"application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, raw)
	}
	cands, _ := out["candidates"].([]any)
	if len(cands) != 1 {
		t.Fatalf("candidates length: want 1, got %d", len(cands))
	}
	cand, _ := cands[0].(map[string]any)
	content, _ := cand["content"].(map[string]any)
	parts, _ := content["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("parts length: want 1, got %d", len(parts))
	}
	first, _ := parts[0].(map[string]any)
	if first["text"] != "hello gemini" {
		t.Errorf("echo: want %q, got %v", "hello gemini", first["text"])
	}
	if cand["finishReason"] != "STOP" {
		t.Errorf("finishReason: want STOP, got %v", cand["finishReason"])
	}
}

// spec: 15.3 (Google streaming endpoint)
// diagnosis: :streamGenerateContent must emit NDJSON candidate
//
//	updates the Gemini SDK can parse.
func TestGoogleStreaming(t *testing.T) {
	t.Parallel()
	llm := llmprovider.New(t)

	body, _ := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{"role": "user", "parts": []map[string]any{{"text": "stream me"}}},
		},
	})
	resp, err := http.Post(llm.URL()+"/v1/models/gemini-1.5-pro:streamGenerateContent",
		"application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), `"text":"stream me"`) {
		t.Errorf("streaming body missing echoed text: %s", raw)
	}
	if !strings.Contains(string(raw), `"finishReason":"STOP"`) {
		t.Errorf("streaming body missing terminal frame: %s", raw)
	}
}

// spec: 12.2.3 (response override drives error / non-default paths)
// diagnosis: SetResponseOverride did not intercept; the override
//
//	hook is not being consulted on every request.
func TestResponseOverride(t *testing.T) {
	t.Parallel()
	llm := llmprovider.New(t)
	llm.SetResponseOverride(func(req llmprovider.Request) (int, string, map[string]string) {
		return http.StatusTooManyRequests, `{"error":"rate_limit"}`, map[string]string{"Retry-After": "5"}
	})
	resp, err := http.Post(llm.URL()+"/v1/messages", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status: want 429, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != "5" {
		t.Errorf("retry-after: want 5, got %q", got)
	}
}
