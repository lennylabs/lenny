//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §4.9 openai_direct translator, driven by the
// canonical request/response corpus under tests/testdata/openai_chat.
// The translator passes the OpenAI Chat Completions request body
// through unchanged, points the upstream URL at /v1/chat/completions
// on api.openai.com, injects Authorization: Bearer, and extracts the
// authoritative token usage from the upstream response. The simple
// scenario covers the non-streaming path; the streaming scenario
// holds an SSE byte stream pinned at byte granularity for the
// pass-through assertion the proxy's SSE relay performs upstream.
package translators_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
)

// openAIChatCorpusDir returns tests/testdata/openai_chat resolved from
// the repository root, walking up from the test's working directory.
func openAIChatCorpusDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for d := wd; ; {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return filepath.Join(d, "tests", "testdata", "openai_chat")
		}
		parent := filepath.Dir(d)
		if parent == d {
			t.Fatalf("no go.mod above %s", wd)
		}
		d = parent
	}
}

// spec: 4.9
// diagnosis: the OpenAIDirectTranslator passes the OpenAI Chat
// Completions request body through verbatim and points the upstream
// URL at api.openai.com/v1/chat/completions. The canonical corpus
// fixtures pin both behaviors at byte granularity.
func TestLLMProxyTranslatorOpenAIChatCompletions(t *testing.T) {
	dir := openAIChatCorpusDir(t)
	tr := &llmproxy.OpenAIDirectTranslator{}
	for _, scenario := range listScenarios(t, dir) {
		if _, err := os.Stat(filepath.Join(dir, scenario, "request.json")); err != nil {
			continue
		}
		t.Run(scenario+"/request", func(t *testing.T) {
			body := readFixture(t, dir, scenario, "request")
			up, err := tr.TranslateRequest(llmproxy.Request{
				Dialect: llmproxy.DialectOpenAI,
				Body:    body,
			}, "sk-openai-corpus")
			if err != nil {
				t.Fatalf("TranslateRequest: %v", err)
			}
			if string(up.Body) != string(body) {
				t.Errorf("upstream body diverged from corpus:\n got %s\nwant %s", up.Body, body)
			}
			if up.URL != "https://api.openai.com/v1/chat/completions" {
				t.Errorf("URL = %q, want the OpenAI Chat Completions endpoint", up.URL)
			}
			if up.Header["authorization"] != "Bearer sk-openai-corpus" {
				t.Errorf("authorization = %q, want the injected upstream credential", up.Header["authorization"])
			}
		})

		respPath := filepath.Join(dir, scenario, "response.json")
		if _, err := os.Stat(respPath); err != nil {
			continue
		}
		t.Run(scenario+"/response", func(t *testing.T) {
			body := readFixture(t, dir, scenario, "response")
			resp, err := tr.TranslateResponse(llmproxy.DialectOpenAI,
				llmproxy.UpstreamResponse{StatusCode: 200, Body: body})
			if err != nil {
				t.Fatalf("TranslateResponse: %v", err)
			}
			if string(resp.Body) != string(body) {
				t.Errorf("pod-facing body diverged from upstream:\n got %s\nwant %s",
					resp.Body, body)
			}

			// §4.9 normalizes OpenAI Chat Completions usage from
			// prompt_tokens/completion_tokens to canonical
			// input_tokens/output_tokens; the fixture pins the
			// per-provider field names the translator must extract.
			var want struct {
				Usage struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(body, &want); err != nil {
				t.Fatalf("parse fixture usage: %v", err)
			}
			got := llmproxy.Usage{
				InputTokens:  want.Usage.PromptTokens,
				OutputTokens: want.Usage.CompletionTokens,
			}
			if !reflect.DeepEqual(resp.Usage, got) {
				t.Errorf("usage = %+v, want %+v from fixture", resp.Usage, got)
			}
		})
	}
}

// spec: 4.9
// diagnosis: the streaming corpus holds an SSE byte stream. The
// proxy passes SSE bytes through unchanged after a successful
// translator-time validation of the request; this test only validates
// that the corpus's request.json round-trips through the translator
// with the stream flag preserved, leaving the SSE relay to assert
// byte-level pass-through in pkg/gateway/llmproxy.SSE tests.
func TestLLMProxyTranslatorOpenAIChatCompletionsStreamingRequest(t *testing.T) {
	dir := openAIChatCorpusDir(t)
	scenario := "streaming"
	if _, err := os.Stat(filepath.Join(dir, scenario, "request.json")); err != nil {
		t.Skipf("no streaming scenario at %s", filepath.Join(dir, scenario))
	}
	tr := &llmproxy.OpenAIDirectTranslator{}
	body := readFixture(t, dir, scenario, "request")
	up, err := tr.TranslateRequest(llmproxy.Request{
		Dialect: llmproxy.DialectOpenAI,
		Body:    body,
	}, "sk-openai-corpus")
	if err != nil {
		t.Fatalf("TranslateRequest: %v", err)
	}
	if string(up.Body) != string(body) {
		t.Errorf("streaming request body rewritten by translator:\n got %s\nwant %s", up.Body, body)
	}

	// The events.sse byte stream sits adjacent to the request and is
	// pinned for the SSE relay's byte-level pass-through tests; the
	// translator does not touch it.
	if _, err := os.Stat(filepath.Join(dir, scenario, "events.sse")); err != nil {
		t.Errorf("streaming corpus is missing events.sse: %v", err)
	}
}
