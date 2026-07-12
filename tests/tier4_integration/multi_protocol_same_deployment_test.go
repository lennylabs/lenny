// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §15.2.1 REST/MCP consistency
// contract driven against ONE real deployment. §15.2.1 rule 5 requires
// contract tests that "call the REST endpoint and every built-in
// external adapter (MCP, OpenAI Completions, Open Responses) for every
// overlapping operation and assert both structural and behavioral
// equivalence of responses", and rule 1 requires REST and MCP to
// "return semantically identical responses" because "both API surfaces
// share a common service layer in the gateway".
//
// The tier-3 rest_mcp_consistency, rest_openai_chat, rest_openai_
// responses, and multiprotocol_journey suites each assert equivalence
// per operation, or one end-to-end journey per protocol, across
// separate in-process httptest servers that merely share an in-memory
// store. None of them drives all four client-facing protocols against a
// single running cmd/lenny-gateway process — the ExternalAdapterRegistry
// deployment that mounts REST, MCP, OpenAI Chat Completions, and Open
// Responses on one mux over one shared session store, executor, and
// §11.7 audit chain. This test closes that gap: it boots one gateway,
// runs create/prompt/read once per protocol against the same tenant,
// and asserts (a) every protocol's session is a semantically identical
// row readable back through the one deployment, (b) the mutations land
// in a single intact audit chain, and (c) the same invalid input is
// rejected consistently, with identical (code, category, retryable) on
// the two surfaces that share the §16.3 error taxonomy.
package tier4_integration_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: 15.2.1, 15.1, 15.2, 11.7
// diagnosis: the single cmd/lenny-gateway deployment failed to serve
// all four §15 client-facing protocols (REST, MCP, OpenAI Chat
// Completions, Open Responses) against shared session and audit state.
// A failure means either a protocol adapter cannot create/prompt/read a
// session on the one shared service layer (§15.2.1 rule 1), the four
// protocols did not converge on semantically identical session rows,
// the cross-protocol mutations did not land in one intact §11.7 audit
// chain, or the same invalid input was not rejected consistently across
// surfaces (§15.2.1 rule 5). The per-operation tier-3 consistency and
// fidelity suites cannot see this because they wire a separate
// httptest server per protocol rather than one running gateway.
func TestMultiProtocolSameDeploymentSharedState(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	gw := gateway.StartWith(t, "--dev-mode")
	base := gw.BaseURL()
	// The platform MCP tools dispatch against the gateway's fixed
	// `default` MCP tenant (see elicitation_test.go mcpClient), so every
	// surface drives the same tenant by pinning X-Lenny-Tenant-ID:
	// default. That single tenant is what makes the shared-session-state
	// and single-audit-chain assertions meaningful.
	c := mcpClient{t: t, base: base}

	// Register the tenant and the echo runtime with injection support so
	// the REST and MCP create paths clear the §15.1 admission gates and
	// the mid-session /messages injection is accepted. The OpenAI Chat
	// and Open Responses translators create their session rows directly,
	// so they do not depend on this registration; running it once keeps
	// all four protocols on the identical runtimeRef.
	adminBootstrap(t, base)

	const prompt = "multi protocol same deployment"

	// --- Protocol 1: REST — POST /v1/sessions/start, POST .../messages ---
	restSID := restCreatePromptRead(t, c, prompt)

	// --- Protocol 2: MCP — lenny/create_and_start_session, lenny/send_message ---
	mcpSID := mcpCreatePromptRead(t, c, prompt)

	// --- Protocol 3: OpenAI Chat Completions — POST /v1/chat/completions ---
	chatSID := openAIChatCreatePrompt(t, base, prompt)

	// --- Protocol 4: Open Responses — POST /v1/responses ---
	respSID := openResponsesCreatePrompt(t, base, prompt)

	// (a) Shared session state / semantically identical rows. Every
	// protocol's session, whichever surface created it, is readable back
	// through the one deployment's REST GET and carries the identical
	// tenant and runtime identity. This is the §15.2.1 rule-1 promise
	// that the four adapters route to one shared service and store.
	for _, tc := range []struct {
		protocol string
		sid      string
	}{
		{"rest", restSID},
		{"mcp", mcpSID},
		{"openai-chat", chatSID},
		{"open-responses", respSID},
	} {
		if tc.sid == "" {
			t.Errorf("%s: no session id captured", tc.protocol)
			continue
		}
		code, row := c.rest(http.MethodGet, "/v1/sessions/"+tc.sid, nil)
		if code != http.StatusOK {
			t.Errorf("%s: GET /v1/sessions/%s through the shared deployment: status %d (%v)",
				tc.protocol, tc.sid, code, row)
			continue
		}
		if got, _ := row["tenantId"].(string); got != mcpTenant {
			t.Errorf("%s: session tenantId = %q, want %q (all protocols share one tenant)",
				tc.protocol, got, mcpTenant)
		}
		if got, _ := row["runtimeRef"].(string); got != "echo" {
			t.Errorf("%s: session runtimeRef = %q, want echo (identical runtime identity across protocols)",
				tc.protocol, got)
		}
	}

	// The four sessions are distinct rows coexisting in the one store.
	seen := map[string]bool{}
	for _, sid := range []string{restSID, mcpSID, chatSID, respSID} {
		if sid != "" && seen[sid] {
			t.Errorf("session id %q reused across protocols; each create must mint a distinct session", sid)
		}
		seen[sid] = true
	}

	// (b) Single shared audit chain. §11.7 hash-chains the tenant's
	// mutations; §25.9 rides chain integrity on the list response's
	// chainIntegrityReport. After all four protocols drove traffic
	// through the one deployment, the tenant has exactly one intact
	// (broken == 0) verified chain, proving the surfaces share one audit
	// path rather than forking per protocol.
	report := adminAuditChainReport(t, base, mcpTenant)
	if report == nil {
		t.Fatalf("audit list for tenant %q carried no chainIntegrityReport", mcpTenant)
	}
	if broken, _ := report["broken"].(float64); broken != 0 {
		t.Errorf("audit chain integrity: %v broken rows; the shared chain must be intact across all four protocols", broken)
	}
	if verified, _ := report["verified"].(float64); verified < 1 {
		t.Errorf("audit chain integrity: want >= 1 verified row in the shared chain, got %v", verified)
	}

	// (c) Equivalent error handling. The same invalid input (create with
	// the required runtimeRef / messages / input omitted) is rejected
	// with a client 4xx on every surface — no surface silently accepts
	// what another rejects. On the two surfaces that share the §16.3
	// lenny error taxonomy (REST and MCP), the (code, category,
	// retryable) triple is identical per §15.2.1 rule 5(d). The OpenAI
	// Chat and Open Responses adapters emit the native OpenAI error
	// envelope ({error:{type,message}}) by design, so the cross-surface
	// assertion for them is the shared reject-the-same-input behavior,
	// not envelope-field identity.
	assertEquivalentErrors(t, c, base)
}

// restCreatePromptRead drives the REST create+start and prompt, asserts
// the echo executor returned the prompt, and returns the session id.
func restCreatePromptRead(t *testing.T, c mcpClient, prompt string) string {
	t.Helper()
	code, created := c.rest(http.MethodPost, "/v1/sessions/start", map[string]any{
		"runtimeRef": "echo",
		"userId":     "alice@acme.com",
	})
	if code != http.StatusCreated {
		t.Fatalf("REST create+start: status %d (%v)", code, created)
	}
	sid, _ := created["id"].(string)
	if sid == "" {
		t.Fatal("REST create+start returned no session id")
	}
	code, msg := c.rest(http.MethodPost, "/v1/sessions/"+sid+"/messages", map[string]any{
		"messages": []map[string]any{{"role": "user", "content": prompt}},
	})
	if code != http.StatusOK {
		t.Fatalf("REST prompt: status %d (%v)", code, msg)
	}
	if !jsonOutputEchoes(msg["output"], prompt) {
		t.Errorf("REST prompt output does not echo %q: %v", prompt, msg["output"])
	}
	return sid
}

// mcpCreatePromptRead drives the MCP create+start and send_message
// tools, asserts the echo, cross-checks that lenny/get_session_status
// and the REST GET agree on the state (§15.2.1 rule 5(e)), and returns
// the session id.
func mcpCreatePromptRead(t *testing.T, c mcpClient, prompt string) string {
	t.Helper()
	createArgs, _ := json.Marshal(map[string]any{"runtimeRef": "echo", "userId": "alice@acme.com"})
	createRPC := c.callTool("lenny/create_and_start_session", string(createArgs))
	createText, isErr := toolResultText(t, createRPC)
	if isErr {
		t.Fatalf("lenny/create_and_start_session failed: %s", createText)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(createText), &created); err != nil {
		t.Fatalf("decode create_and_start_session result: %v; body=%s", err, createText)
	}
	if created.ID == "" {
		t.Fatalf("lenny/create_and_start_session returned no id: %s", createText)
	}

	sendArgs, _ := json.Marshal(map[string]any{"to": created.ID, "message": prompt})
	sendRPC := c.callTool("lenny/send_message", string(sendArgs))
	sendText, isErr := toolResultText(t, sendRPC)
	if isErr {
		t.Fatalf("lenny/send_message failed: %s", sendText)
	}
	var send struct {
		DeliveryReceipt struct {
			Status string `json:"status"`
		} `json:"deliveryReceipt"`
		Output []struct {
			Text string `json:"text"`
		} `json:"output"`
	}
	if err := json.Unmarshal([]byte(sendText), &send); err != nil {
		t.Fatalf("decode send_message result: %v; body=%s", err, sendText)
	}
	if send.DeliveryReceipt.Status != "delivered" {
		t.Errorf("MCP send_message delivery status: got %q, want delivered", send.DeliveryReceipt.Status)
	}
	sawEcho := false
	for _, p := range send.Output {
		if strings.Contains(p.Text, prompt) {
			sawEcho = true
		}
	}
	if !sawEcho {
		t.Errorf("MCP send_message output does not echo %q: %s", prompt, sendText)
	}

	// §15.2.1 rule 5(e): GET /v1/sessions/{id} (REST) and
	// lenny/get_session_status (MCP) must report the same session state
	// for the same session on the one deployment.
	statusArgs, _ := json.Marshal(map[string]any{"sessionId": created.ID})
	statusRPC := c.callTool("lenny/get_session_status", string(statusArgs))
	statusText, isErr := toolResultText(t, statusRPC)
	if isErr {
		t.Fatalf("lenny/get_session_status failed: %s", statusText)
	}
	var mcpStatus struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(statusText), &mcpStatus); err != nil {
		t.Fatalf("decode get_session_status result: %v; body=%s", err, statusText)
	}
	code, restRow := c.rest(http.MethodGet, "/v1/sessions/"+created.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("REST read of MCP-created session: status %d (%v)", code, restRow)
	}
	if restState, _ := restRow["state"].(string); restState != mcpStatus.State {
		t.Errorf("state parity for MCP-created session: REST=%q, MCP get_session_status=%q", restState, mcpStatus.State)
	}
	return created.ID
}

// openAIChatCreatePrompt drives a single POST /v1/chat/completions
// (the OpenAI create+prompt-in-one), asserts the assistant message
// echoes the prompt, and returns the underlying session id decoded from
// the completion id (the translator sets id = "chatcmpl-" + sessionID).
func openAIChatCreatePrompt(t *testing.T, base, prompt string) string {
	t.Helper()
	resp, raw := postProtoJSON(t, base+"/v1/chat/completions", map[string]any{
		"model":    "echo",
		"messages": []map[string]any{{"role": "user", "content": prompt}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OpenAI chat create+prompt: status %d, body=%s", resp.StatusCode, raw)
	}
	var out struct {
		ID      string `json:"id"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode chat completion: %v; body=%s", err, raw)
	}
	if len(out.Choices) == 0 || !strings.Contains(out.Choices[0].Message.Content, prompt) {
		t.Errorf("OpenAI chat content does not echo %q: %s", prompt, raw)
	}
	sid := strings.TrimPrefix(out.ID, "chatcmpl-")
	if sid == "" || sid == out.ID {
		t.Fatalf("OpenAI chat completion id %q lacks the chatcmpl- session prefix", out.ID)
	}
	return sid
}

// openResponsesCreatePrompt drives a single POST /v1/responses (the
// Open Responses create+prompt-in-one) and returns the session id (the
// translator sets the response id to the session id). The Open
// Responses envelope carries metadata only (no echoed output text by
// design), so the prompt-acceptance assertion is the 200 status plus the
// session being readable back through the shared deployment.
func openResponsesCreatePrompt(t *testing.T, base, prompt string) string {
	t.Helper()
	resp, raw := postProtoJSON(t, base+"/v1/responses", map[string]any{
		"model": "echo",
		"input": prompt,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Open Responses create+prompt: status %d, body=%s", resp.StatusCode, raw)
	}
	var out struct {
		ID     string `json:"id"`
		Object string `json:"object"`
		Model  string `json:"model"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode responses envelope: %v; body=%s", err, raw)
	}
	if out.Object != "response" {
		t.Errorf("Open Responses object: got %q, want response", out.Object)
	}
	if out.ID == "" {
		t.Fatalf("Open Responses envelope carried no id: %s", raw)
	}
	return out.ID
}

// assertEquivalentErrors triggers the same class of invalid input (a
// create with the required field omitted) on each protocol and asserts
// consistent rejection, with identical error triples across REST and
// MCP per §15.2.1 rule 5(d).
func assertEquivalentErrors(t *testing.T, c mcpClient, base string) {
	t.Helper()

	// REST: POST /v1/sessions with runtimeRef omitted -> 400 with the
	// lenny error envelope.
	code, restBody := c.rest(http.MethodPost, "/v1/sessions", map[string]any{})
	if code != http.StatusBadRequest {
		t.Fatalf("REST invalid create: status %d, want 400 (%v)", code, restBody)
	}
	restErr, _ := restBody["error"].(map[string]any)
	if restErr == nil {
		t.Fatalf("REST invalid create carried no error envelope: %v", restBody)
	}
	restCode, _ := restErr["code"].(string)
	restCategory, _ := restErr["category"].(string)
	restRetryable, _ := restErr["retryable"].(bool)
	if restCode == "" {
		t.Errorf("REST error envelope carried no code: %v", restErr)
	}

	// MCP: lenny/create_session with no args -> tool error carrying the
	// same lenny/error envelope.
	mcpRPC := c.callTool("lenny/create_session", "{}")
	mcpCode, mcpCategory, mcpRetryable := mcpErrorTriple(t, mcpRPC)
	if mcpCode != restCode {
		t.Errorf("error code parity REST vs MCP: REST=%q, MCP=%q", restCode, mcpCode)
	}
	if mcpCategory != restCategory {
		t.Errorf("error category parity REST vs MCP: REST=%q, MCP=%q", restCategory, mcpCategory)
	}
	if mcpRetryable != restRetryable {
		t.Errorf("error retryable parity REST vs MCP: REST=%v, MCP=%v", restRetryable, mcpRetryable)
	}

	// OpenAI Chat: POST /v1/chat/completions with no messages -> 4xx.
	resp, raw := postProtoJSON(t, base+"/v1/chat/completions", map[string]any{"model": "echo"})
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Errorf("OpenAI chat invalid input: status %d, want a 4xx client rejection; body=%s", resp.StatusCode, raw)
	}

	// Open Responses: POST /v1/responses with no input -> 4xx.
	resp, raw = postProtoJSON(t, base+"/v1/responses", map[string]any{"model": "echo"})
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Errorf("Open Responses invalid input: status %d, want a 4xx client rejection; body=%s", resp.StatusCode, raw)
	}
}

// mcpErrorTriple extracts (code, category, retryable) from an MCP tool
// result flagged isError, reading the lenny/error content block the
// §15.2.1 rule 5(d) contract requires.
func mcpErrorTriple(t *testing.T, rpc map[string]any) (string, string, bool) {
	t.Helper()
	result, ok := rpc["result"].(map[string]any)
	if !ok {
		t.Fatalf("MCP create_session response carried no result: %v", rpc)
	}
	if result["isError"] != true {
		t.Fatalf("MCP create_session with no args did not flag isError=true: %v", result)
	}
	contents, _ := result["content"].([]any)
	for _, ce := range contents {
		block, _ := ce.(map[string]any)
		if block["type"] != "lenny/error" {
			continue
		}
		text, _ := block["text"].(string)
		var env struct {
			Code      string `json:"code"`
			Category  string `json:"category"`
			Retryable bool   `json:"retryable"`
		}
		if err := json.Unmarshal([]byte(text), &env); err != nil {
			t.Fatalf("decode MCP lenny/error block: %v; body=%s", err, text)
		}
		return env.Code, env.Category, env.Retryable
	}
	t.Fatalf("MCP create_session error result carried no lenny/error content block: %v", contents)
	return "", "", false
}

// adminBootstrap registers the shared tenant and the echo runtime (with
// injection support) as a platform-admin scoped to the same tenant,
// mirroring the bootstrap in gateway_full_e2e_test.go.
func adminBootstrap(t *testing.T, base string) {
	t.Helper()
	code, body := adminReq(t, base, http.MethodPost, "/v1/admin/bootstrap", map[string]any{
		"tenants": []map[string]any{{"id": mcpTenant, "displayName": "Default"}},
		"runtimes": []map[string]any{{
			"name":   "echo",
			"image":  "lenny/echo@sha256:abc",
			"labels": map[string]string{"tier": "test"},
			"capabilities": map[string]any{
				"injection": map[string]any{
					"supported": true,
					"modes":     []string{"immediate", "queued"},
				},
			},
		}},
	})
	// Bootstrap is idempotent: 200 (all applied) or 207 (multi-status)
	// are both success.
	if code != http.StatusOK && code != http.StatusMultiStatus {
		t.Fatalf("bootstrap tenant/runtime: status %d (%v)", code, body)
	}
}

// adminAuditChainReport reads the §25.9 chainIntegrityReport from the
// admin audit-events list for the tenant.
func adminAuditChainReport(t *testing.T, base, tenant string) map[string]any {
	t.Helper()
	code, body := adminReq(t, base, http.MethodGet, "/v1/admin/audit-events?tenantId="+tenant, nil)
	if code != http.StatusOK {
		t.Fatalf("audit list: status %d (%v)", code, body)
	}
	report, _ := body["chainIntegrityReport"].(map[string]any)
	return report
}

// adminReq issues an admin request with the platform-admin dev headers
// scoped to the shared tenant and returns the status and decoded body.
func adminReq(t *testing.T, base, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, base+path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", mcpTenant)
	req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

// postProtoJSON POSTs a JSON body to url under the shared tenant and
// returns the response and buffered body. Used for the OpenAI Chat and
// Open Responses surfaces, which are not routed through mcpClient.rest.
func postProtoJSON(t *testing.T, url string, body any) (*http.Response, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", mcpTenant)
	req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

// jsonOutputEchoes reports whether any {text} part in a decoded output
// array contains want.
func jsonOutputEchoes(output any, want string) bool {
	parts, ok := output.([]any)
	if !ok {
		return false
	}
	for _, p := range parts {
		m, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if text, _ := m["text"].(string); strings.Contains(text, want) {
			return true
		}
	}
	return false
}
