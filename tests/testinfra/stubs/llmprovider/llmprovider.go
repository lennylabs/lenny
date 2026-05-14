// SPDX-License-Identifier: MIT

// Package llmprovider is a mock LLM-provider HTTP server. The LLM
// Proxy translates incoming requests into the canonical Anthropic
// or OpenAI shape; this stub stands in for the upstream service
// while the gateway is being exercised.
//
// The stub records every inbound request so a test can assert the
// translator produced the expected wire format. It supports
// non-streaming and SSE-streaming responses. The response body is
// deterministic per request (echoes the last user message) so tests
// can pin against golden files.
//
// Usage:
//
//	llm := llmprovider.New(t)
//	t.Setenv("ANTHROPIC_API_BASE", llm.URL)
//	// ... drive the gateway ...
//	req := llm.Requests()[0]
//	assertions.JSONEqual(t, expected, req.Body)
package llmprovider

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Stub is a recorded HTTP server.
type Stub struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []Request
	responseOverride func(req Request) (status int, body string, headers map[string]string)
}

// Request captures the relevant bits of an inbound request.
type Request struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

// New starts the stub and registers a t.Cleanup that closes it.
func New(t testing.TB) *Stub {
	t.Helper()
	s := &Stub{}
	s.server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.server.Close)
	return s
}

// URL is the stub's base URL.
func (s *Stub) URL() string { return s.server.URL }

// Requests returns a snapshot of every recorded request, in order.
func (s *Stub) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Request, len(s.requests))
	copy(out, s.requests)
	return out
}

// LastRequest returns the most recent request, or false when none.
func (s *Stub) LastRequest() (Request, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return Request{}, false
	}
	return s.requests[len(s.requests)-1], true
}

// SetResponseOverride installs a custom response shaper. When nil
// the stub uses its default echo behavior.
func (s *Stub) SetResponseOverride(fn func(Request) (int, string, map[string]string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responseOverride = fn
}

func (s *Stub) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	req := Request{
		Method: r.Method,
		Path:   r.URL.Path,
		Header: r.Header.Clone(),
		Body:   body,
	}

	s.mu.Lock()
	s.requests = append(s.requests, req)
	override := s.responseOverride
	s.mu.Unlock()

	if override != nil {
		status, payload, headers := override(req)
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, payload)
		return
	}

	switch {
	case strings.Contains(r.URL.Path, "/v1/messages"):
		s.handleAnthropic(w, r, body)
	case strings.Contains(r.URL.Path, "/v1/chat/completions"):
		s.handleOpenAIChat(w, r, body)
	case strings.Contains(r.URL.Path, "/v1/responses"):
		s.handleOpenAIResponses(w, r, body)
	default:
		http.NotFound(w, r)
	}
}

// handleAnthropic echoes the last user message in the Anthropic
// /v1/messages shape. Streaming is enabled when ?stream=true (or
// stream: true in body).
func (s *Stub) handleAnthropic(w http.ResponseWriter, r *http.Request, body []byte) {
	streaming := isStreaming(r, body)
	echo := extractLastUserMessage(body)
	if streaming {
		writeSSE(w, []string{
			fmt.Sprintf(`event: message_start
data: {"type":"message_start","message":{"id":"msg-1","type":"message","role":"assistant","content":[],"model":"claude-stub","stop_reason":null,"usage":{"input_tokens":1,"output_tokens":0}}}`),
			fmt.Sprintf(`event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
			fmt.Sprintf(`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, echo),
			`event: content_block_stop
data: {"type":"content_block_stop","index":0}`,
			`event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
			`event: message_stop
data: {"type":"message_stop"}`,
		})
		return
	}
	resp := map[string]any{
		"id":      "msg-1",
		"type":    "message",
		"role":    "assistant",
		"content": []map[string]any{{"type": "text", "text": echo}},
		"model":   "claude-stub",
		"stop_reason": "end_turn",
		"usage":   map[string]any{"input_tokens": 1, "output_tokens": 1},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Stub) handleOpenAIChat(w http.ResponseWriter, r *http.Request, body []byte) {
	echo := extractLastUserMessage(body)
	resp := map[string]any{
		"id":      "chatcmpl-1",
		"object":  "chat.completion",
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": echo},
			"finish_reason": "stop",
		}},
		"model": "gpt-stub",
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Stub) handleOpenAIResponses(w http.ResponseWriter, r *http.Request, body []byte) {
	echo := extractLastUserMessage(body)
	resp := map[string]any{
		"id":     "resp-1",
		"object": "response",
		"output": []map[string]any{{
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{{"type": "output_text", "text": echo}},
		}},
		"model": "gpt-responses-stub",
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeSSE(w http.ResponseWriter, events []string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	for _, e := range events {
		_, _ = io.WriteString(w, e+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func isStreaming(r *http.Request, body []byte) bool {
	if r.URL.Query().Get("stream") == "true" {
		return true
	}
	var probe map[string]any
	if err := json.Unmarshal(body, &probe); err != nil {
		return false
	}
	if v, ok := probe["stream"].(bool); ok && v {
		return true
	}
	return false
}

// extractLastUserMessage walks a request body and returns the last
// user-role content; falls back to a fixed string when the body
// can't be parsed.
func extractLastUserMessage(body []byte) string {
	var probe map[string]any
	if err := json.Unmarshal(body, &probe); err != nil {
		return "stub-echo"
	}
	if messages, ok := probe["messages"].([]any); ok {
		for i := len(messages) - 1; i >= 0; i-- {
			m, _ := messages[i].(map[string]any)
			if m == nil {
				continue
			}
			if m["role"] == "user" {
				switch c := m["content"].(type) {
				case string:
					return c
				case []any:
					var sb strings.Builder
					for _, part := range c {
						if pm, ok := part.(map[string]any); ok {
							if t, _ := pm["text"].(string); t != "" {
								sb.WriteString(t)
							}
						}
					}
					if sb.Len() > 0 {
						return sb.String()
					}
				}
			}
		}
	}
	if input, ok := probe["input"].(string); ok {
		return input
	}
	return "stub-echo"
}

// requireFlusher is a guard that callers can use to ensure the
// underlying writer supports flushing for SSE.
func requireFlusher(w http.ResponseWriter) (http.Flusher, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("response writer does not support flushing")
	}
	return f, nil
}

// readSSE parses an SSE byte stream into events, for tests that
// want to assert on the streaming envelope. Each event is one
// `event:` + `data:` pair separated by a blank line.
func ReadSSE(r io.Reader) ([]SSEEvent, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	out := []SSEEvent{}
	var cur SSEEvent
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if cur.Event != "" || cur.Data != "" {
				out = append(out, cur)
				cur = SSEEvent{}
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			cur.Event = strings.TrimSpace(line[len("event:"):])
		}
		if strings.HasPrefix(line, "data:") {
			cur.Data = strings.TrimSpace(line[len("data:"):])
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if cur.Event != "" || cur.Data != "" {
		out = append(out, cur)
	}
	return out, nil
}

// SSEEvent is a single parsed SSE event.
type SSEEvent struct {
	Event string
	Data  string
}

// Ensure bytes import isn't trimmed by goimports.
var _ = bytes.NewReader

// Guard against unused-function lint while keeping requireFlusher
// reachable as an internal helper.
var _ = requireFlusher
