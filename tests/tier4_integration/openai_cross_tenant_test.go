// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the TESTING.md §12.9.1 cross-tenant isolation
// matrix as it applies to the OpenAI-compatible external protocol
// surfaces (OpenAI Chat Completions, Open Responses). The matrix
// requires every store/operation/client-path combination — including
// these two adapters — to reject a cross-tenant attempt with the
// documented isolation error rather than leak the other tenant's
// data. This file drives both adapters against the real
// cmd/lenny-gateway binary (gateway.StartWith), which wires both
// translators onto the same session store the REST surface uses, so
// the isolation guard exercised here is the same one the store-level
// unit tests pin (memstore.Get / sessionstore.ErrNotFound), observed
// end to end over the wire.
package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// crossTenantOwner holds the resource under test; crossTenantAttacker
// is the peer tenant that attempts the cross-tenant read/write.
const (
	crossTenantOwner    = "globex"
	crossTenantAttacker = "acme"
)

// crossTenantSubject is the principal subject both tenants present. The
// create paths refuse a request without a subject, and the attacker uses
// the same subject as the owner so that a refused cross-tenant read is
// attributable to tenant isolation alone rather than to session ownership.
// spec: §10.2 Session ownership.
const crossTenantSubject = "alice"

// spec: TESTING.md §12.9.1 ("A composed adversarial scenario: seed tenants A and
// B with rich state on every store, then for each store and each
// operation, attempt cross-tenant reads and writes through every code
// path (REST, MCP, OpenAI Completions, OpenAI Responses, admin API,
// MCP management server, audit query, drift detection, lenny-ops
// endpoints). Every attempt must fail with the documented isolation
// error.")
// spec: §15.1 (REST API): the Open Responses adapter serves no delete
// operation on /v1/responses/{id}.
// spec: §15 (Open Responses conversation sessions): GET /v1/responses/{id}
// answers a completed response of the caller's tenant by its <session>.<n> id.
// diagnosis: a cross-tenant GET of an Open Responses resource, or a
// cross-tenant REST read of a session created through POST
// /v1/chat/completions, that returns anything other than the
// documented RESOURCE_NOT_FOUND / not-found-shaped isolation error is
// a tenant-isolation breach on the OpenAI-compatible surfaces the
// TESTING.md §12.9.1 matrix names. An owner-tenant DELETE on
// /v1/responses/{id} that answers anything other than 405, or a
// resource the owner can no longer read afterwards, means a delete
// route came back on the adapter and can remove the implicit session
// record behind a response.
func TestOpenAICompatibleSurfacesCrossTenantIsolation(t *testing.T) {
	t.Parallel()
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all", "--dev-mode")
	base := gw.BaseURL()

	t.Run("open_responses_cross_tenant_get_and_no_delete", func(t *testing.T) {
		testOpenResponsesCrossTenant(t, base)
	})
	t.Run("chat_completions_write_then_cross_tenant_rest_read", func(t *testing.T) {
		testChatCompletionsCrossTenant(t, base)
	})
}

// crossTenantRequest issues method against base+path with the given
// tenant stamped on the dev-mode X-Lenny-Tenant-ID header and
// crossTenantSubject on the dev-mode X-Lenny-User-ID header, and
// returns the status code, the decoded JSON body (nil on decode
// failure), and the raw bytes for substring checks.
func crossTenantRequest(t *testing.T, base, method, path, tenant string, body []byte) (int, map[string]any, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	req.Header.Set("X-Lenny-User-ID", crossTenantSubject)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s body: %v", method, path, err)
	}
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out, raw
}

// errorType reads the OpenAI-compatible error envelope's
// error.type field (the OpenAI wire form has no top-level "code" on
// these two adapters' not-found envelope; "type" is the field the
// wire body carries).
func errorType(body map[string]any) string {
	errObj, _ := body["error"].(map[string]any)
	typ, _ := errObj["type"].(string)
	return typ
}

// openaiErrorCode reads the REST error envelope's error.code field.
func openaiErrorCode(body map[string]any) string {
	errObj, _ := body["error"].(map[string]any)
	code, _ := errObj["code"].(string)
	return code
}

// testOpenResponsesCrossTenant creates an Open Responses resource
// under crossTenantOwner and asserts that crossTenantAttacker cannot
// read it: GET /v1/responses/{id} under the attacker's tenant must
// reject with the not-found isolation error. It then asserts that the
// adapter serves no delete operation: a DELETE /v1/responses/{id}
// under the owner's own tenant answers 405 from the router's method
// mismatch, which carries a plain-text body rather than the OpenAI
// error envelope, and the resource remains readable by its owner.
func testOpenResponsesCrossTenant(t *testing.T, base string) {
	const secret = "globex confidential response payload"
	reqBody, err := json.Marshal(map[string]any{
		"model": "echo",
		"input": secret,
	})
	if err != nil {
		t.Fatalf("marshal responses request: %v", err)
	}
	status, created, _ := crossTenantRequest(t, base, http.MethodPost, "/v1/responses", crossTenantOwner, reqBody)
	if status != http.StatusOK {
		t.Fatalf("create Open Responses resource for %s: status %d, body=%v", crossTenantOwner, status, created)
	}
	id, _ := created["id"].(string)
	// The response id is <session>.1: the first turn of the session the
	// POST created.
	if dot := strings.LastIndex(id, "."); dot <= 0 || id[dot+1:] != "1" {
		t.Fatalf("Open Responses create response id %q is not <session>.1: %v", id, created)
	}

	// Cross-tenant GET must reject, not leak the owner's resource.
	getStatus, getBody, getRaw := crossTenantRequest(t, base, http.MethodGet, "/v1/responses/"+id, crossTenantAttacker, nil)
	if getStatus != http.StatusNotFound {
		t.Errorf("%s GET %s's Open Responses resource: got status %d, want 404 (cross-tenant isolation); body=%s",
			crossTenantAttacker, crossTenantOwner, getStatus, getRaw)
	}
	if typ := errorType(getBody); typ != "not_found_error" {
		t.Errorf("cross-tenant GET error.type = %q, want not_found_error; body=%s", typ, getRaw)
	}
	if strings.Contains(string(getRaw), secret) {
		t.Errorf("cross-tenant GET response leaked the owner's payload: %s", getRaw)
	}

	// The adapter serves no delete operation, so even the owner's own
	// DELETE is refused by the router's method mismatch.
	delStatus, _, delRaw := crossTenantRequest(t, base, http.MethodDelete, "/v1/responses/"+id, crossTenantOwner, nil)
	if delStatus != http.StatusMethodNotAllowed {
		t.Errorf("owner %s DELETE of its own Open Responses resource: got status %d, want 405 (no delete operation); body=%s",
			crossTenantOwner, delStatus, delRaw)
	}

	// The resource must survive both the cross-tenant read and the
	// owner's DELETE: its owner can still read it back with the same
	// id. This rules out a DELETE that removed the implicit session
	// record behind the response while answering an unexpected status.
	ownerStatus, ownerBody, ownerRaw := crossTenantRequest(t, base, http.MethodGet, "/v1/responses/"+id, crossTenantOwner, nil)
	if ownerStatus != http.StatusOK {
		t.Fatalf("owner %s lost its own Open Responses resource after the cross-tenant read and the DELETE: status %d, body=%s",
			crossTenantOwner, ownerStatus, ownerRaw)
	}
	if gotID, _ := ownerBody["id"].(string); gotID != id {
		t.Errorf("owner's Open Responses resource id after the cross-tenant read and the DELETE: got %q, want %q", gotID, id)
	}
	if st, _ := ownerBody["status"].(string); st != "completed" {
		t.Errorf("owner's GET of its own response: status %q, want completed", st)
	}
}

// testChatCompletionsCrossTenant creates a session through POST
// /v1/chat/completions under crossTenantOwner (an adapter with no
// native GET-by-id of its own) and asserts that the session it wrote
// is invisible to crossTenantAttacker through the REST surface — the
// isolation boundary a client-path with no native read still has to
// hold on the write side.
func testChatCompletionsCrossTenant(t *testing.T, base string) {
	const secret = "globex confidential chat payload"
	reqBody, err := json.Marshal(map[string]any{
		"model":    "echo",
		"messages": []map[string]any{{"role": "user", "content": secret}},
	})
	if err != nil {
		t.Fatalf("marshal chat completions request: %v", err)
	}
	status, created, raw := crossTenantRequest(t, base, http.MethodPost, "/v1/chat/completions", crossTenantOwner, reqBody)
	if status != http.StatusOK {
		t.Fatalf("create chat completion for %s: status %d, body=%s", crossTenantOwner, status, raw)
	}
	chatID, _ := created["id"].(string)
	const prefix = "chatcmpl-"
	if !strings.HasPrefix(chatID, prefix) {
		t.Fatalf("chat completion id %q does not carry the chatcmpl- prefix", chatID)
	}
	sessionID := strings.TrimPrefix(chatID, prefix)

	// The underlying session must be invisible to the attacker tenant
	// through the REST surface: this is the same session row the
	// OpenAI Chat Completions POST wrote, read back through a
	// different client-path.
	getStatus, getBody, getRaw := crossTenantRequest(t, base, http.MethodGet, "/v1/sessions/"+sessionID, crossTenantAttacker, nil)
	if getStatus != http.StatusNotFound {
		t.Errorf("%s REST-read of %s's chat-completions session: got status %d, want 404 (cross-tenant isolation); body=%s",
			crossTenantAttacker, crossTenantOwner, getStatus, getRaw)
	}
	if code := openaiErrorCode(getBody); code != "RESOURCE_NOT_FOUND" {
		t.Errorf("cross-tenant REST session read error.code = %q, want RESOURCE_NOT_FOUND; body=%s", code, getRaw)
	}
	if strings.Contains(string(getRaw), secret) {
		t.Errorf("cross-tenant REST session read leaked the owner's chat payload: %s", getRaw)
	}

	// Control: the owner tenant can read its own session back, ruling
	// out a false positive where the 404 above came from the session
	// never having been created at all.
	ownerStatus, ownerBody, ownerRaw := crossTenantRequest(t, base, http.MethodGet, "/v1/sessions/"+sessionID, crossTenantOwner, nil)
	if ownerStatus != http.StatusOK {
		t.Fatalf("owner %s cannot REST-read its own chat-completions session: status %d, body=%s",
			crossTenantOwner, ownerStatus, ownerRaw)
	}
	if gotID, _ := ownerBody["id"].(string); gotID != sessionID {
		t.Errorf("owner's chat-completions session id via REST: got %q, want %q", gotID, sessionID)
	}
}
