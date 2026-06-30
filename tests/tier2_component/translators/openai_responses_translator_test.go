//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §4.9 openai_responses translator, driven by
// the canonical request/response corpus under tests/testdata/openai_responses.
// The translator passes the Responses API request body through unchanged,
// points the upstream URL at /v1/responses on api.openai.com, injects
// Authorization: Bearer, and extracts the authoritative token usage
// from the upstream response (the Responses API uses input_tokens /
// output_tokens, distinct from Chat Completions' prompt_tokens /
// completion_tokens; the fixture pins the wire shape).
package translators_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
)

// openAIResponsesCorpusDir returns tests/testdata/openai_responses
// resolved from the repository root, walking up from the test's
// working directory.
func openAIResponsesCorpusDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for d := wd; ; {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return filepath.Join(d, "tests", "testdata", "openai_responses")
		}
		parent := filepath.Dir(d)
		if parent == d {
			t.Fatalf("no go.mod above %s", wd)
		}
		d = parent
	}
}

// spec: 4.9
// diagnosis: the OpenAIResponsesTranslator passes the OpenAI
// Responses API request body through verbatim, points the upstream
// URL at api.openai.com/v1/responses, and extracts the authoritative
// usage from the Responses-API-shaped envelope. The canonical corpus
// fixtures pin every behavior at byte granularity.
func TestLLMProxyTranslatorOpenAIResponses(t *testing.T) {
	dir := openAIResponsesCorpusDir(t)
	tr := &llmproxy.OpenAIResponsesTranslator{}
	for _, scenario := range listScenarios(t, dir) {
		if _, err := os.Stat(filepath.Join(dir, scenario, "request.json")); err != nil {
			continue
		}
		t.Run(scenario+"/request", func(t *testing.T) {
			body := readFixture(t, dir, scenario, "request")
			up, err := tr.TranslateRequest(llmproxy.Request{
				Dialect: llmproxy.DialectOpenAIResponses,
				Body:    body,
			}, "sk-openai-corpus")
			if err != nil {
				t.Fatalf("TranslateRequest: %v", err)
			}
			if string(up.Body) != string(body) {
				t.Errorf("upstream body diverged from corpus:\n got %s\nwant %s", up.Body, body)
			}
			if up.URL != "https://api.openai.com/v1/responses" {
				t.Errorf("URL = %q, want the OpenAI Responses endpoint", up.URL)
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
			resp, err := tr.TranslateResponse(llmproxy.DialectOpenAIResponses,
				llmproxy.UpstreamResponse{StatusCode: 200, Body: body})
			if err != nil {
				t.Fatalf("TranslateResponse: %v", err)
			}
			if string(resp.Body) != string(body) {
				t.Errorf("pod-facing body diverged from upstream:\n got %s\nwant %s",
					resp.Body, body)
			}

			// The Responses API names its usage block with the same
			// canonical pair §4.9 normalizes into (input_tokens,
			// output_tokens); the fixture pins this wire shape.
			var want struct {
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(body, &want); err != nil {
				t.Fatalf("parse fixture usage: %v", err)
			}
			got := llmproxy.Usage{
				InputTokens:  want.Usage.InputTokens,
				OutputTokens: want.Usage.OutputTokens,
			}
			if !reflect.DeepEqual(resp.Usage, got) {
				t.Errorf("usage = %+v, want %+v from fixture", resp.Usage, got)
			}

			// The Responses API surfaces a top-level `id` field on the
			// response envelope (§15.4.1's openai_responses fidelity
			// row marks it `[extended]` round-trip); the byte-passthrough
			// check above already proves the id field is preserved on
			// the wire. This sub-assertion pins the field's presence
			// in the corpus so a future translator change that strips
			// it is caught.
			var withID struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(body, &withID); err != nil || withID.ID == "" {
				t.Errorf("fixture %s/response.json missing top-level id field: err=%v", scenario, err)
			}
		})
	}
}
