//go:build contract

// SPDX-License-Identifier: MIT

// Package interceptor_immutable_envelope_test is the Tier 3 contract suite
// for the §15.1 error envelope the session-create/route surface writes when
// an external interceptor MODIFY alters an immutable field. The envelope is
// the wire contract a caller (or a SIEM consuming the audit stream) decodes
// to distinguish a deployer-supplied interceptor tampering with the
// authenticated tenant_id from an ordinary policy REJECT. §15.1 fixes the
// code as INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION, category POLICY,
// non-retryable, HTTP 400, carrying details.interceptor_ref, details.phase,
// and details.violated_fields. These tests drive the production route
// surface over httptest and assert the raw JSON the client receives, so a
// drift in the top-level code, the classification, the status, or the
// details key set surfaces here independent of the internal envelope struct.
package interceptor_immutable_envelope_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/errorclassify"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// immutableCode is the §15.1 catalog code the route surface must surface for
// an immutable-field violation.
const immutableCode = "INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION"

// tenantRewriteInterceptor is an external (priority > 100) PreRoute
// interceptor that returns a MODIFY rewriting the authenticated tenant_id,
// the §4.8 immutable field, so the chain rejects it with the immutable-field
// violation code and the route surface writes the §15.1 400 envelope.
type tenantRewriteInterceptor struct{}

func (tenantRewriteInterceptor) Name() string                       { return "tenant-rewriter" }
func (tenantRewriteInterceptor) Priority() int32                    { return 150 }
func (tenantRewriteInterceptor) Builtin() bool                      { return false }
func (tenantRewriteInterceptor) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (tenantRewriteInterceptor) Timeout() time.Duration             { return 0 }
func (tenantRewriteInterceptor) Intercept(_ context.Context, req interceptor.Request) (interceptor.Result, error) {
	var m map[string]any
	if err := json.Unmarshal(req.Content, &m); err != nil {
		return interceptor.Result{}, err
	}
	m["tenant_id"] = "globex"
	out, err := json.Marshal(m)
	if err != nil {
		return interceptor.Result{}, err
	}
	return interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: out}, nil
}

// genericRejectInterceptor is an external PreRoute interceptor that returns a
// deliberate REJECT that is not an immutable-field violation. It exercises
// the generic-reject branch the immutable-violation envelope must not steal.
type genericRejectInterceptor struct{}

func (genericRejectInterceptor) Name() string                       { return "policy-blocker" }
func (genericRejectInterceptor) Priority() int32                    { return 150 }
func (genericRejectInterceptor) Builtin() bool                      { return false }
func (genericRejectInterceptor) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (genericRejectInterceptor) Timeout() time.Duration             { return 0 }
func (genericRejectInterceptor) Intercept(context.Context, interceptor.Request) (interceptor.Result, error) {
	return interceptor.Result{Action: interceptor.ActionReject, Reason: "denied by policy"}, nil
}

// serveCreateStart drives POST /v1/sessions/start against a route surface
// whose PreRoute chain is ic and returns the HTTP status and the decoded
// error envelope (the raw JSON body parsed into nested maps so the test
// asserts on the wire keys the client actually receives).
func serveCreateStart(t *testing.T, ic interceptor.Interceptor) (int, map[string]any) {
	t.Helper()
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreRoute, ic); err != nil {
		t.Fatalf("register interceptor: %v", err)
	}
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Interceptors: chain})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	body, err := json.Marshal(map[string]any{"runtimeRef": "claude-code", "userId": "alice@acme.com"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/sessions/start", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("response not JSON: %v\nbody: %s", err, raw)
	}
	return resp.StatusCode, parsed
}

// errorObject extracts the top-level `error` object from a decoded §15.1
// envelope, failing when the wire shape lacks it.
func errorObject(t *testing.T, env map[string]any) map[string]any {
	t.Helper()
	obj, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("envelope has no `error` object: %v", env)
	}
	return obj
}

// TestImmutableViolationEnvelopeWireContract pins the full §15.1 wire
// envelope the route surface writes when a PreRoute MODIFY alters the
// immutable tenant_id: HTTP 400, top-level code
// INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION, category POLICY, retryable false,
// and a details object whose key set is exactly interceptor_ref, phase, and
// violated_fields, with violated_fields naming tenant_id. It also confirms
// the (category, retryable) the envelope carries is the same pair the shared
// §25.2 errorclassify catalog assigns the code at status 400, so the wire
// classification cannot drift from the catalog.
//
// spec: 15.1 (INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION error response envelope
// and catalog row: POLICY, 400, details.violated_fields)
//
// diagnosis: the session-create/route surface no longer writes the §15.1
// immutable-field-violation envelope on the wire. The top-level code, the
// POLICY category, the non-retryable flag, the 400 status, or one of the
// mandated details keys (interceptor_ref, phase, violated_fields) drifted, so
// a caller or SIEM can no longer distinguish a deployer-supplied interceptor
// tampering with the authenticated tenant_id from an ordinary policy reject.
func TestImmutableViolationEnvelopeWireContract(t *testing.T) {
	status, env := serveCreateStart(t, tenantRewriteInterceptor{})

	if status != http.StatusBadRequest {
		t.Fatalf("HTTP status = %d, want 400 (§15.1 immutable-field violation)", status)
	}
	obj := errorObject(t, env)

	if got := obj["code"]; got != immutableCode {
		t.Errorf("error.code = %v, want %s", got, immutableCode)
	}
	if got := obj["category"]; got != string(errorclassify.CategoryPolicy) {
		t.Errorf("error.category = %v, want %s", got, errorclassify.CategoryPolicy)
	}
	if got, ok := obj["retryable"].(bool); !ok || got {
		t.Errorf("error.retryable = %v, want false", obj["retryable"])
	}

	// The wire classification must match the shared §25.2 catalog verdict for
	// this code at status 400, so REST, MCP, and the retry path agree.
	cat, retryable := errorclassify.ClassifyStatus(immutableCode, http.StatusBadRequest)
	if string(cat) != obj["category"] || retryable != obj["retryable"] {
		t.Errorf("wire (category=%v, retryable=%v) disagrees with catalog (category=%v, retryable=%v)",
			obj["category"], obj["retryable"], cat, retryable)
	}

	details, ok := obj["details"].(map[string]any)
	if !ok {
		t.Fatalf("error.details is not an object: %v", obj["details"])
	}
	for _, key := range []string{"interceptor_ref", "phase", "violated_fields"} {
		if _, present := details[key]; !present {
			t.Errorf("error.details missing mandated key %q: %v", key, details)
		}
	}
	// The details object carries exactly the three §15.1 keys, so no extra
	// key leaks and none is dropped.
	if len(details) != 3 {
		t.Errorf("error.details has %d keys, want exactly 3 (interceptor_ref, phase, violated_fields): %v", len(details), details)
	}
	if got := details["phase"]; got != string(interceptor.PhasePreRoute) {
		t.Errorf("details.phase = %v, want PreRoute", got)
	}
	if got := details["interceptor_ref"]; got != "tenant-rewriter" {
		t.Errorf("details.interceptor_ref = %v, want tenant-rewriter", got)
	}
	if !violatedFieldsContain(details["violated_fields"], "tenant_id") {
		t.Errorf("details.violated_fields = %v, want to contain tenant_id", details["violated_fields"])
	}
}

// TestGenericRejectDoesNotDriftIntoImmutableEnvelope pins the negative half
// of the contract: a deliberate PreRoute REJECT that is not an
// immutable-field violation must not be promoted into the §15.1
// immutable-field-violation envelope. It surfaces as the generic 403
// INTERCEPTOR_REJECTED envelope with no violated_fields key and a non-400
// status, so the immutable-violation branch does not steal ordinary policy
// rejects (the drifted-envelope failure the fix must not introduce).
//
// spec: 15.1 (INTERCEPTOR_REJECTED generic reject vs the distinct
// INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION catalog row)
//
// diagnosis: the route surface promoted an ordinary policy REJECT into the
// immutable-field-violation envelope. A generic reject now returns 400
// INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION with violated_fields instead of 403
// INTERCEPTOR_REJECTED, so the distinct-code signal an immutable violation
// carries is diluted and a caller can no longer trust the code.
func TestGenericRejectDoesNotDriftIntoImmutableEnvelope(t *testing.T) {
	status, env := serveCreateStart(t, genericRejectInterceptor{})

	if status == http.StatusBadRequest {
		t.Fatalf("generic REJECT wrongly returned HTTP 400 (the immutable-violation status)")
	}
	if status != http.StatusForbidden {
		t.Fatalf("HTTP status = %d, want 403 (generic INTERCEPTOR_REJECTED)", status)
	}
	obj := errorObject(t, env)
	if got := obj["code"]; got == immutableCode {
		t.Fatalf("generic REJECT wrongly surfaced code %s", immutableCode)
	}
	if got := obj["code"]; got != "INTERCEPTOR_REJECTED" {
		t.Errorf("error.code = %v, want INTERCEPTOR_REJECTED", got)
	}
	if details, ok := obj["details"].(map[string]any); ok {
		if _, present := details["violated_fields"]; present {
			t.Errorf("generic REJECT envelope carried violated_fields: %v", details)
		}
	}
}

// violatedFieldsContain reports whether the decoded details.violated_fields
// value (a JSON array decoded into []any) contains want.
func violatedFieldsContain(v any, want string) bool {
	fields, ok := v.([]any)
	if !ok {
		return false
	}
	for _, f := range fields {
		if s, ok := f.(string); ok && s == want {
			return true
		}
	}
	return false
}
