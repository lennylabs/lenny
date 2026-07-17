// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §4.8 external RequestInterceptor path
// through the real cmd/lenny-gateway binary. The interceptor package's
// own unit tests dial an in-process bufconn fake; nothing else exercised
// the deployer-facing contract — the gateway's --external-interceptor
// spec parsing, the gRPC dial to a service on a real TCP socket, a
// MODIFY applied to the payload after a network round-trip, and a REJECT
// that short-circuits the chain and is returned to the caller. This test
// starts a real interceptor stub (tests/testinfra/stubs/interceptor) on
// a loopback port, launches the gateway with the echo runtime and the
// interceptor registered on PostAgentOutput, sends a prompt, and asserts
// the agent output is rewritten on MODIFY and blocked on REJECT.
package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	stubinterceptor "github.com/lennylabs/lenny/tests/testinfra/stubs/interceptor"
)

// interceptorReq issues an HTTP request against a running gateway with
// the acme/alice dev-header identity and returns the status code plus the
// decoded JSON body.
func interceptorReq(t *testing.T, base string) func(method, path, roles string, body any) (int, map[string]any) {
	t.Helper()
	client := http.DefaultClient
	return func(method, path, roles string, body any) (int, map[string]any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, reader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
		if roles != "" {
			req.Header.Set("X-Lenny-Roles", roles)
		}
		resp, err := client.Do(req)
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
}

// bootstrapEchoSession bootstraps the acme tenant with the echo runtime
// and returns a started session id, ready to receive a prompt.
func bootstrapEchoSession(t *testing.T, do func(method, path, roles string, body any) (int, map[string]any)) string {
	t.Helper()
	code, _ := do(http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
		"runtimes": []map[string]any{{
			"name":   "echo",
			"image":  "lenny/echo@sha256:abc",
			"labels": map[string]string{"tier": "test"},
			"capabilities": map[string]any{
				// Mid-session message injection must be declared or the
				// gateway rejects the /messages call before it ever reaches
				// the runtime (and the PostAgentOutput chain).
				"injection": map[string]any{
					"supported": true,
					"modes":     []string{"immediate", "queued"},
				},
			},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d", code)
	}
	code, created := do(http.MethodPost, "/v1/sessions/start", "", map[string]any{
		"runtimeRef": "echo",
		"userId":     "alice@acme.com",
	})
	if code != http.StatusCreated {
		t.Fatalf("create session: %d (%v)", code, created)
	}
	sid, _ := created["id"].(string)
	if sid == "" {
		t.Fatal("session id missing")
	}
	return sid
}

// spec: §4.8 — "External interceptors are invoked via gRPC (like
// Kubernetes admission webhooks)"; "`MODIFY` results are applied to the
// payload before passing it to the next interceptor in the chain"; the
// PostAgentOutput phase carries "The agent's response output before
// delivery to the client" and MODIFY "May modify, redact, or truncate
// output content."
// diagnosis: the gateway's external-interceptor path is broken — either
// --external-interceptor spec parsing, the gRPC dial to a service on a
// real TCP socket, or applying a MODIFY payload returned over the wire.
// A tier-1 bufconn fake would not catch a regression in the subprocess
// dial or the flag wiring; this exercises the deployer-facing contract
// end to end.
func TestExternalInterceptorModifyThroughGateway_spec_4_8(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	const redacted = "<redacted by external interceptor>"
	// The PostAgentOutput content payload is a serialized []MessagePart
	// (JSON). The stub returns a well-formed replacement so the gateway's
	// MODIFY deserialization and immutable-field enforcement accept it.
	modified, _ := json.Marshal([]map[string]any{{"type": "text", "text": redacted}})
	stub := stubinterceptor.Start(t, stubinterceptor.Modify(modified))

	gw := gateway.StartWith(t, "--dev-mode", "--agent-runtime", "echo",
		"--external-interceptor=name=redactor,endpoint="+stub.Addr()+",phase=PostAgentOutput")
	do := interceptorReq(t, gw.BaseURL())
	sid := bootstrapEchoSession(t, do)

	const prompt = "interceptor modify hello"
	code, msgResp := do(http.MethodPost, "/v1/sessions/"+sid+"/messages", "", map[string]any{
		"messages": []map[string]any{{"role": "user", "content": prompt}},
	})
	if code != http.StatusOK {
		t.Fatalf("send message: %d (%v)", code, msgResp)
	}
	out, _ := msgResp["output"].([]any)
	if len(out) == 0 {
		t.Fatal("message produced no output")
	}
	first, _ := out[0].(map[string]any)
	got, _ := first["text"].(string)
	if got != redacted {
		t.Fatalf("output text = %q, want the interceptor's MODIFY replacement %q", got, redacted)
	}
	if strings.Contains(got, prompt) {
		t.Fatalf("output still contains the original echoed prompt %q; MODIFY was not applied", prompt)
	}

	// The gateway dialed the stub over the wire and forwarded the
	// PostAgentOutput payload carrying the original (pre-MODIFY) echo
	// output plus the session identity.
	reqs := stub.Requests()
	if len(reqs) == 0 {
		t.Fatal("interceptor stub received no gRPC request; the gateway did not invoke the external interceptor")
	}
	last := reqs[len(reqs)-1]
	if last.GetPhase() != "PostAgentOutput" {
		t.Errorf("forwarded phase = %q, want PostAgentOutput", last.GetPhase())
	}
	if last.GetTenantId() != "acme" {
		t.Errorf("forwarded tenant_id = %q, want acme", last.GetTenantId())
	}
	if !strings.Contains(string(last.GetContent()), prompt) {
		t.Errorf("forwarded content %q did not carry the original echoed prompt %q", last.GetContent(), prompt)
	}
}

// bootstrapEchoTenant bootstraps the acme tenant with the echo runtime
// but, unlike bootstrapEchoSession, does not create a session. A caller
// that expects the session-create path itself to be rejected uses this
// so the rejection is not masked by a session that already exists.
func bootstrapEchoTenant(t *testing.T, do func(method, path, roles string, body any) (int, map[string]any)) {
	t.Helper()
	code, _ := do(http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants":  []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
		"runtimes": []map[string]any{{"name": "echo", "image": "lenny/echo@sha256:abc", "labels": map[string]string{"tier": "test"}}},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d", code)
	}
}

// spec: §4.8 — "If any immutable field differs, the gateway rejects the
// `MODIFY` with `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` (category:
// `POLICY`, HTTP 400) ... the original pre-modification payload is
// preserved and no subsequent interceptors see the illegal modification.
// This enforcement is not bypassable by the interceptor; it runs inside
// the gateway process after the gRPC response is received." The §15.1
// error catalog fixes the surface: `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION`
// | `POLICY` | 400, with `details.interceptor_ref`, `details.phase`, and
// `details.violated_fields` included. `tenant_id` is immutable at the
// PreRoute phase (§4.8 phase table PreRoute row: "May **not** alter
// `tenant_id` or `user_id`").
// diagnosis: the gateway did not enforce per-phase immutability across
// the real gRPC boundary — a deployer-supplied external interceptor was
// able to rewrite the authenticated tenant_id via a MODIFY returned over
// the wire, or the gateway surfaced the rejection with the wrong error
// code/status. Either is an identity/tenant-isolation defect on the
// deployer-facing interceptor contract; a tier-1 in-process fake cannot
// catch a regression in the subprocess dial or the wire round-trip.
func TestExternalInterceptorModifyImmutableTenantThroughGateway_spec_4_8(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	// The default external-interceptor priority is 500, so the interceptor
	// runs in the at-or-above-ExperimentRouter PreRoute segment whose
	// content payload is the serialized routeTaskSpec carrying the
	// authenticated tenant_id and user_id. The stub returns a MODIFY that
	// rewrites tenant_id (acme -> globex) while leaving the mutable
	// requested_runtime and the immutable user_id untouched, so the only
	// immutable field that differs is tenant_id.
	modified, _ := json.Marshal(map[string]any{
		"tenant_id":         "globex",
		"user_id":           "alice@acme.com",
		"requested_runtime": "echo",
	})
	stub := stubinterceptor.Start(t, stubinterceptor.Modify(modified))

	gw := gateway.StartWith(t, "--dev-mode", "--agent-runtime", "echo",
		"--external-interceptor=name=tenant-rewriter,endpoint="+stub.Addr()+",phase=PreRoute")
	do := interceptorReq(t, gw.BaseURL())
	bootstrapEchoTenant(t, do)

	code, resp := do(http.MethodPost, "/v1/sessions/start", "", map[string]any{
		"runtimeRef": "echo",
		"userId":     "alice@acme.com",
	})
	// spec: §15.1 — INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION is HTTP 400.
	if code != http.StatusBadRequest {
		t.Fatalf("create with immutable-field MODIFY: status %d (%v), want 400", code, resp)
	}
	errBody, _ := resp["error"].(map[string]any)
	if errBody == nil {
		t.Fatalf("immutable-violation response missing error envelope: %v", resp)
	}
	if c, _ := errBody["code"].(string); c != "INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION" {
		t.Errorf("error code = %q, want INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION", c)
	}
	if cat, _ := errBody["category"].(string); cat != "POLICY" {
		t.Errorf("error category = %q, want POLICY", cat)
	}
	details, _ := errBody["details"].(map[string]any)
	if details == nil {
		t.Fatalf("immutable-violation response missing details: %v", errBody)
	}
	if got, _ := details["phase"].(string); got != "PreRoute" {
		t.Errorf("details.phase = %q, want PreRoute", got)
	}
	vf, _ := details["violated_fields"].([]any)
	foundTenant := false
	for _, f := range vf {
		if s, _ := f.(string); s == "tenant_id" {
			foundTenant = true
		}
	}
	if !foundTenant {
		t.Errorf("details.violated_fields = %v, want it to name tenant_id", details["violated_fields"])
	}

	// The gateway dialed the stub over the wire and forwarded the PreRoute
	// payload carrying the original (pre-MODIFY) authenticated tenant_id.
	// The enforcement runs inside the gateway after the gRPC response, so
	// the illegal MODIFY was rejected rather than applied.
	reqs := stub.Requests()
	if len(reqs) == 0 {
		t.Fatal("interceptor stub received no gRPC request; the gateway did not invoke the external interceptor")
	}
	last := reqs[len(reqs)-1]
	if last.GetPhase() != "PreRoute" {
		t.Errorf("forwarded phase = %q, want PreRoute", last.GetPhase())
	}
	if !strings.Contains(string(last.GetContent()), `"tenant_id":"acme"`) {
		t.Errorf("forwarded content %q did not carry the original authenticated tenant_id acme", last.GetContent())
	}
}

// spec: §4.8 — "If any interceptor returns `REJECT`, the chain
// short-circuits immediately ... The rejection reason is logged and
// returned to the caller." The PostAgentOutput phase MODIFY column adds
// that an interceptor "May **not** suppress delivery entirely (use
// `REJECT` for that)."
// diagnosis: the gateway did not honor a REJECT returned by an external
// interceptor over gRPC — it either delivered the agent output anyway or
// dropped the interceptor's rejection reason. Either is a policy-bypass
// or an audit-fidelity defect on the deployer-facing interceptor
// contract.
func TestExternalInterceptorRejectThroughGateway_spec_4_8(t *testing.T) {
	gateway.SkipUnlessAvailable(t)

	const reason = "blocked by external interceptor"
	stub := stubinterceptor.Start(t, stubinterceptor.Reject(reason))

	gw := gateway.StartWith(t, "--dev-mode", "--agent-runtime", "echo",
		"--external-interceptor=name=blocker,endpoint="+stub.Addr()+",phase=PostAgentOutput")
	do := interceptorReq(t, gw.BaseURL())
	sid := bootstrapEchoSession(t, do)

	code, msgResp := do(http.MethodPost, "/v1/sessions/"+sid+"/messages", "", map[string]any{
		"messages": []map[string]any{{"role": "user", "content": "interceptor reject hello"}},
	})
	if code != http.StatusForbidden {
		t.Fatalf("send message with REJECT interceptor: status %d (%v), want 403", code, msgResp)
	}
	errBody, _ := msgResp["error"].(map[string]any)
	if errBody == nil {
		t.Fatalf("REJECT response missing error envelope: %v", msgResp)
	}
	if c, _ := errBody["code"].(string); c != "INTERCEPTOR_REJECTED" {
		t.Errorf("error code = %q, want INTERCEPTOR_REJECTED", c)
	}
	if msg, _ := errBody["message"].(string); msg != reason {
		t.Errorf("error message = %q, want the interceptor's rejection reason %q", msg, reason)
	}
	if len(stub.Requests()) == 0 {
		t.Fatal("interceptor stub received no gRPC request; the gateway did not invoke the external interceptor")
	}
}
