//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §4.9 anthropic_direct translator, driven by
// the canonical request/response corpus under tests/testdata/anthropic.
// The translator passes the Anthropic Messages request body through
// unchanged and extracts the authoritative token usage from the
// upstream response, so the canonical fixtures pin both behaviors at
// byte granularity.
package translators_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
)

// anthropicCorpusDir returns tests/testdata/anthropic resolved from the
// repository root, walking up from the test's working directory.
func anthropicCorpusDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for d := wd; ; {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return filepath.Join(d, "tests", "testdata", "anthropic")
		}
		parent := filepath.Dir(d)
		if parent == d {
			t.Fatalf("no go.mod above %s", wd)
		}
		d = parent
	}
}

// listScenarios returns the scenario subdirectories under dir, each
// holding a request.json and response.json pair.
func listScenarios(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpus dir %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, e.Name())
	}
	if len(out) == 0 {
		t.Fatalf("no scenarios in %s", dir)
	}
	return out
}

// readFixture reads scenario/<name>.json. The fixture is returned as
// raw bytes so byte-equivalence assertions stay precise.
func readFixture(t *testing.T, dir, scenario, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, scenario, name+".json"))
	if err != nil {
		t.Fatalf("read %s/%s.json: %v", scenario, name, err)
	}
	return b
}

// spec: 4.9
// diagnosis: the AnthropicDirectTranslator did not preserve the
// canonical Anthropic Messages request body byte-for-byte. §4.9 makes
// the anthropic_direct translator a passthrough plus credential
// injection, so the upstream body must equal the pod body exactly and
// the upstream URL must end in /v1/messages.
func TestLLMProxyTranslatorAnthropicRequestPassthrough(t *testing.T) {
	dir := anthropicCorpusDir(t)
	tr := &llmproxy.AnthropicDirectTranslator{DefaultAnthropicVersion: "2023-06-01"}
	for _, scenario := range listScenarios(t, dir) {
		// streaming/ holds an SSE byte stream rather than a request and
		// response pair; the byte-passthrough check applies to the
		// request/response pairs.
		if _, err := os.Stat(filepath.Join(dir, scenario, "request.json")); err != nil {
			continue
		}
		t.Run(scenario, func(t *testing.T) {
			body := readFixture(t, dir, scenario, "request")
			up, err := tr.TranslateRequest(llmproxy.Request{
				Dialect: llmproxy.DialectAnthropic,
				Body:    body,
			}, "sk-ant-corpus")
			if err != nil {
				t.Fatalf("TranslateRequest: %v", err)
			}
			if string(up.Body) != string(body) {
				t.Errorf("upstream body diverged from corpus:\n got %s\nwant %s", up.Body, body)
			}
			if up.URL != "https://api.anthropic.com/v1/messages" {
				t.Errorf("URL = %q, want the Anthropic Messages endpoint", up.URL)
			}
			if up.Header["x-api-key"] != "sk-ant-corpus" {
				t.Errorf("x-api-key = %q, want the injected upstream credential", up.Header["x-api-key"])
			}
			if up.Header["anthropic-version"] != "2023-06-01" {
				t.Errorf("anthropic-version = %q, want the configured default", up.Header["anthropic-version"])
			}
		})
	}
}

// spec: 4.9
// diagnosis: the AnthropicDirectTranslator did not extract the
// authoritative input_tokens / output_tokens from a canonical Anthropic
// Messages response. §4.9 records the proxy-extracted usage as
// authoritative for quota accounting; pod-reported counts are not
// accepted in proxy mode.
func TestLLMProxyTranslatorAnthropicResponseUsage(t *testing.T) {
	dir := anthropicCorpusDir(t)
	tr := &llmproxy.AnthropicDirectTranslator{}
	for _, scenario := range listScenarios(t, dir) {
		if _, err := os.Stat(filepath.Join(dir, scenario, "response.json")); err != nil {
			continue
		}
		t.Run(scenario, func(t *testing.T) {
			body := readFixture(t, dir, scenario, "response")
			resp, err := tr.TranslateResponse(llmproxy.DialectAnthropic,
				llmproxy.UpstreamResponse{StatusCode: 200, Body: body})
			if err != nil {
				t.Fatalf("TranslateResponse: %v", err)
			}
			if string(resp.Body) != string(body) {
				t.Errorf("pod-facing body diverged from upstream:\n got %s\nwant %s",
					resp.Body, body)
			}

			// The canonical fixture pins input_tokens and output_tokens
			// in the usage block; the extracted usage must match.
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
		})
	}
}
