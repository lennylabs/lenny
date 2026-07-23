// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind conformance tests for the two built-in OpenAI-dialect
// adapters (§15 "Built-in adapter inventory": OpenAICompletionsAdapter
// at /v1/chat/completions, OpenResponsesAdapter at /v1/responses; both
// status V1, "Always available"). The tier-3 contract suite
// (tests/tier3_contract/rest_openai_chat, rest_openai_responses) already
// validates each adapter's wire body against the vendored published
// OpenAI schema, but only against an in-process httptest server wired
// directly to a memstore and an EchoExecutor — it never drives a
// deployed gateway binary through its real HTTP listener, its auth
// middleware, or its adapter-registry mounting. This file closes that
// gap by driving both adapters through the port-forwarded lenny-gateway
// Service on the e2e Kind cluster and validating each response against
// the same vendored schema the tier-3 suite uses.

package tier5_e2e_kind_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// openAIConformanceTenant is the synthetic tenant the conformance tests
// bootstrap. sessiondriver.Close removes it in best-effort cleanup.
const openAIConformanceTenant = "scaffold-openai-conformance-tenant"

// t5PostJSON issues a POST against the port-forwarded gateway with the
// dev-mode identity headers sessiondriver.New's own methods use
// (X-Lenny-Tenant-ID / X-Lenny-Roles / X-Lenny-User-ID), returning the
// decoded status and raw body. The tier-5 Driver type does not expose a
// generic-path request method (its methods cover the §15.1 session
// surface only), so the adapter-conformance tests build the request
// directly against d.BaseURL().
func t5PostJSON(t *testing.T, baseURL, path, tenant string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request for %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	req.Header.Set("X-Lenny-User-ID", "alice")
	hc := &http.Client{Timeout: 30 * time.Second}
	res, err := hc.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read response body for %s: %v", path, err)
	}
	return res.StatusCode, raw
}

// t5ReadFixture reads a tests/testdata fixture relative to the repo
// root the same way the tier-3 contract suites do.
func t5ReadFixture(t *testing.T, rel string) []byte {
	t.Helper()
	path := filepath.Join(repoRoot(t), "tests", "testdata", rel)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return body
}

// spec: 15 ("Built-in adapter inventory": OpenAICompletionsAdapter at
// `/v1/chat/completions`, protocol "OpenAI Chat Completions", status
// V1; "Built-in (compiled in): MCP, OpenAI Completions, Open Responses.
// Always available, configurable via admin API."; "Built-in adapter
// single-shot compute model": the request claims a warm pod, dispatches
// the turn, and releases the pod within one HTTP call), 15.2.1 (REST/MCP
// Consistency Contract: the shared session-creation service through
// which the implicit single-shot session runs)
// diagnosis: a failure here means the deployed gateway's
// /v1/chat/completions adapter is unreachable through its real HTTP
// listener and auth middleware, cannot bind a warm pod through the
// single-shot session-creation path on the standard chart's
// PodExecutor wiring, or its non-streaming response no longer validates
// against OpenAI's own published CreateChatCompletionResponse schema.
// The tier-3 contract suite (tests/tier3_contract/rest_openai_chat)
// pins the same schema but only against an in-process httptest server
// wired to an EchoExecutor; it cannot catch a defect in how the adapter
// is mounted on the real gateway binary (path registration,
// auth-middleware wrapping, dev-header identity resolution) or in the
// pod-binding path against a real PodExecutor-backed gateway. A
// conforming external client (a Stainless-generated OpenAI SDK, for
// example) driving the built-in adapter inventory the spec promises
// "Always available" would fail here first.
func TestOpenAIChatCompletionsConformsToPublishedSchemaOnDeployedGateway(t *testing.T) {
	d := sessiondriver.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := d.BootstrapTenant(ctx, openAIConformanceTenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	sch := schematest.Compile(t, "tests/testdata/openai_chat/schema/create-chat-completion-response.schema.json")

	body := t5ReadFixture(t, "openai_chat/simple/request.json")
	status, raw := t5PostJSON(t, d.BaseURL(), "/v1/chat/completions", openAIConformanceTenant, body)
	if status != http.StatusOK {
		t.Fatalf("POST /v1/chat/completions on the deployed gateway: status %d, body=%s", status, raw)
	}

	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, raw)
	}
	if err := sch.Validate(instance); err != nil {
		t.Errorf("the deployed gateway's /v1/chat/completions response does not validate against "+
			"the published OpenAI Chat Completions schema: %v", err)
	}

	// A minimal functional check alongside the schema check: the
	// EchoExecutor the e2e gateway wires by default (§17.4 zero-
	// credential mode) echoes the last user message verbatim, so the
	// completion content is observable and not just well-formed.
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode choices: %v", err)
	}
	if len(decoded.Choices) == 0 {
		t.Fatalf("response carries no choices; body=%s", raw)
	}
	if decoded.Choices[0].Message.Content == "" {
		t.Errorf("expected a non-empty echoed completion; got an empty message content")
	}
}

// spec: 15 ("Built-in adapter inventory": OpenResponsesAdapter at
// `/v1/responses`, protocol "Open Responses Specification", status V1;
// "OpenResponsesAdapter covers both Open Responses-compliant clients
// and OpenAI Responses API clients ... OpenAI's Responses API is a
// proper superset of Open Responses."; "Built-in adapter single-shot
// compute model": the request claims a warm pod, dispatches the turn,
// and releases the pod within one HTTP call), 15.2.1 (REST/MCP
// Consistency Contract: the shared session-creation service through
// which the implicit single-shot session runs)
// diagnosis: a failure here means the deployed gateway's /v1/responses
// adapter is unreachable through its real HTTP listener and auth
// middleware, cannot bind a warm pod through the single-shot
// session-creation path on the standard chart's PodExecutor wiring, or
// its non-streaming response no longer validates against OpenAI's own
// published Response object schema. As with the Chat Completions test
// above, the tier-3 contract suite
// (tests/tier3_contract/rest_openai_responses) pins the same schema but
// only in-process against an EchoExecutor; this test is the only one
// that exercises the adapter as mounted on the real gateway binary and
// bound to a real pod.
func TestOpenAIResponsesConformsToPublishedSchemaOnDeployedGateway(t *testing.T) {
	d := sessiondriver.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := d.BootstrapTenant(ctx, openAIConformanceTenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	sch := schematest.Compile(t, "tests/testdata/openai_responses/schema/response.schema.json")

	body := t5ReadFixture(t, "openai_responses/simple/request.json")
	status, raw := t5PostJSON(t, d.BaseURL(), "/v1/responses", openAIConformanceTenant, body)
	if status != http.StatusOK {
		t.Fatalf("POST /v1/responses on the deployed gateway: status %d, body=%s", status, raw)
	}

	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, raw)
	}
	if err := sch.Validate(instance); err != nil {
		t.Errorf("the deployed gateway's /v1/responses response does not validate against "+
			"the published OpenAI Responses schema: %v", err)
	}
}
