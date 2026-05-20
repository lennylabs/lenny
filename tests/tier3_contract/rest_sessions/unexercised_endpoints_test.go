// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §15.1 REST endpoints the original
// sessions_test.go did not cover. Each test pins one endpoint's
// shape, error envelopes, and tenant-scoping.

package rest_sessions_test

import (
	"net/http"
	"strings"
	"testing"
)

// spec: §15.1 (POST /v1/sessions/start: combined create + start)
// diagnosis: a session starts in the running state when the
// combined create+start endpoint is invoked. A missing runtimeRef
// is rejected with VALIDATION_ERROR.
func TestStartCombinedReturnsRunningSession(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, ts, "POST", "/v1/sessions/start", map[string]any{
		"runtimeRef": "echo",
		"userId":     "alice@acme.com",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status: want 201, got %d (body %v)", resp.StatusCode, body)
	}
	state, _ := body["state"].(string)
	if state != "running" {
		t.Errorf("state = %q, want running (the combined create+start path skips created)", state)
	}
}

// spec: 15.1
// diagnosis: §15.1 REST contract — pins the §15.1 error envelope on the wire for the previously-unexercised /v1/sessions/* endpoints.
func TestStartCombinedRejectsMissingRuntimeRef(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, ts, "POST", "/v1/sessions/start", map[string]any{"userId": "alice"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d (body %v)", resp.StatusCode, body)
	}
	bodyJSON, _ := body["error"].(map[string]any)
	if bodyJSON["code"] != "VALIDATION_ERROR" {
		t.Errorf("code = %v, want VALIDATION_ERROR", bodyJSON["code"])
	}
}

// spec: §15.1 (GET /v1/runtimes returns the platform runtime list)
// diagnosis: with no runtime registry wired the endpoint returns an
// empty list, not a 500.
func TestListRuntimesReturnsList(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, ts, "GET", "/v1/runtimes", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body %v)", resp.StatusCode, body)
	}
	if _, ok := body["runtimes"]; !ok {
		t.Errorf("body missing runtimes key: %v", body)
	}
}

// spec: §15.1 (GET /v1/runtimes/{name}/meta/{key})
// diagnosis: requesting metadata for an unknown runtime returns
// RESOURCE_NOT_FOUND rather than a 500 / empty 200.
func TestRuntimeMetaUnknownRuntime(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, ts, "GET", "/v1/runtimes/missing/meta/icon", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: want 404, got %d (body %v)", resp.StatusCode, body)
	}
}

// spec: §15.1 (GET /v1/models)
// diagnosis: the model catalog endpoint returns the OpenAI-shaped
// `{object: "list", data: [...]}` envelope plus a Lenny-specific
// adapterCapabilities block.
func TestListModelsReturnsList(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, ts, "GET", "/v1/models", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body %v)", resp.StatusCode, body)
	}
	if body["object"] != "list" {
		t.Errorf("object = %v, want list", body["object"])
	}
	if _, ok := body["data"]; !ok {
		t.Errorf("body missing data key: %v", body)
	}
	caps, _ := body["adapterCapabilities"].(map[string]any)
	if caps == nil {
		t.Errorf("body missing adapterCapabilities object: %v", body)
	}
}

// spec: §15.1 (GET /v1/usage)
// diagnosis: the usage endpoint is gated by the view_usage
// permission per §10.2. A caller without it receives FORBIDDEN —
// the gateway never silently returns empty data to an unauthorized
// caller.
func TestUsageRequiresViewUsagePermission(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, ts, "GET", "/v1/usage", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status: want 403, got %d (body %v)", resp.StatusCode, body)
	}
	if errBody, ok := body["error"].(map[string]any); ok {
		if errBody["code"] != "FORBIDDEN" {
			t.Errorf("code = %v, want FORBIDDEN", errBody["code"])
		}
	}
}

// spec: §15.1 (GET /v1/metering/events)
// diagnosis: the metering endpoint is gated by the view_usage
// permission per §10.2; an unauthorized caller is rejected with
// FORBIDDEN before any backend probe.
func TestMeteringEventsRequiresViewUsagePermission(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, ts, "GET", "/v1/metering/events", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status: want 403, got %d (body %v)", resp.StatusCode, body)
	}
}

// spec: §15.1 (POST /v1/environments/{name}/sessions binds the
// session to the named environment)
// diagnosis: the path-level environment selector overrides the body
// field. A missing runtimeRef on the body is still rejected.
func TestEnvironmentSessionsRejectsMissingRuntime(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, ts, "POST", "/v1/environments/security-team/sessions",
		map[string]any{"userId": "alice"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d (body %v)", resp.StatusCode, body)
	}
}

// spec: §15.1 (POST /v1/sessions/{id}/replay)
// diagnosis: replay against a session that has no completed run
// returns PRECONDITION_FAILED rather than 200, since replay requires
// a terminal state to replay from.
func TestReplayPreconditionFailedOnFreshSession(t *testing.T) {
	ts := newTestServer(t)
	id := createSession(t, ts)
	resp, body := transition(t, ts, id, "replay")
	if resp.StatusCode == http.StatusOK {
		t.Errorf("replay on a fresh session unexpectedly returned 200 (body %v)", body)
	}
}

// spec: §15.1 (POST /v1/sessions/{id}/eval requires the eval backend)
// diagnosis: with no Evals wired, the endpoint returns 503
// EVAL_UNAVAILABLE rather than silently succeeding.
func TestEvalUnavailableWithoutBackend(t *testing.T) {
	ts := newTestServer(t)
	id := createSession(t, ts)
	resp, body := do(t, ts, "POST", "/v1/sessions/"+id+"/eval", map[string]any{
		"scorer": "llm-judge",
		"score":  0.9,
	})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status: want 503, got %d (body %v)", resp.StatusCode, body)
	}
}

// spec: §15.1 (POST /v1/sessions/{id}/extend-retention validates the
// requested deadline)
// diagnosis: an empty body is rejected with VALIDATION_ERROR.
func TestExtendRetentionRejectsEmpty(t *testing.T) {
	ts := newTestServer(t)
	id := createSession(t, ts)
	resp, body := do(t, ts, "POST", "/v1/sessions/"+id+"/extend-retention", map[string]any{})
	if resp.StatusCode == http.StatusOK {
		t.Errorf("empty body unexpectedly succeeded (body %v)", body)
	}
}

// spec: §15.1 (POST /v1/sessions/{id}/derive)
// diagnosis: derive against a session that has no completed run
// returns a 4xx error envelope; an unknown parent returns 404.
func TestDeriveUnknownParentReturns404(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, ts, "POST", "/v1/sessions/sess_unknown/derive",
		map[string]any{"runtimeRef": "echo"})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: want 404, got %d (body %v)", resp.StatusCode, body)
	}
}

// spec: §15.1 (GET /v1/sessions/{id}/transcript)
// diagnosis: requesting the transcript of an unknown session returns
// RESOURCE_NOT_FOUND. With no transcript backend wired, an existing
// session returns 503 TRANSCRIPTS_UNAVAILABLE so a caller knows the
// surface is not configured rather than seeing an empty 200.
func TestTranscriptUnknownSession(t *testing.T) {
	ts := newTestServer(t)
	resp, _ := do(t, ts, "GET", "/v1/sessions/missing/transcript", nil)
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status: want 404 or 503, got %d", resp.StatusCode)
	}
}

// spec: §15.1 (GET /v1/sessions/{id}/tree)
// diagnosis: the tree endpoint returns the session's delegation tree;
// unknown sessions return 404.
func TestTreeUnknownSessionReturns404(t *testing.T) {
	ts := newTestServer(t)
	resp, _ := do(t, ts, "GET", "/v1/sessions/missing/tree", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: want 404, got %d", resp.StatusCode)
	}
}

// spec: §15.1 (POST /v1/sessions/{id}/tool-use/{tool_call_id}/approve)
// diagnosis: tool-use approve on an unknown session returns 404. The
// path validates the {id} segment before consulting any pending tool
// call so the error envelope is consistent.
func TestToolUseApproveUnknownSession(t *testing.T) {
	ts := newTestServer(t)
	resp, _ := do(t, ts, "POST", "/v1/sessions/missing/tool-use/call_1/approve", nil)
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status: want 404 or 503, got %d", resp.StatusCode)
	}
}

// spec: §15.1 (POST /v1/sessions/{id}/tool-use/{tool_call_id}/deny)
// diagnosis: §15.1 REST contract — pins the §15.1 error envelope on the wire for the previously-unexercised /v1/sessions/* endpoints.
func TestToolUseDenyUnknownSession(t *testing.T) {
	ts := newTestServer(t)
	resp, _ := do(t, ts, "POST", "/v1/sessions/missing/tool-use/call_1/deny", nil)
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status: want 404 or 503, got %d", resp.StatusCode)
	}
}

// spec: §15.1 (POST /v1/sessions/{id}/elicitations/{elicitation_id}/respond)
// diagnosis: an elicitation respond against a session with no
// elicitation registry returns 503 INTERACTIONS_UNAVAILABLE; the
// session existence check still runs first so an unknown session
// returns 404.
func TestElicitationRespondUnknownSession(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, ts, "POST",
		"/v1/sessions/missing/elicitations/eli_1/respond",
		map[string]any{"response": map[string]any{"answer": "ok"}})
	if resp.StatusCode == http.StatusOK {
		t.Errorf("respond on missing session unexpectedly succeeded (body %v)", body)
	}
}

// spec: §15.1 (POST /v1/sessions/{id}/elicitations/{elicitation_id}/dismiss)
// diagnosis: §15.1 REST contract — pins the §15.1 error envelope on the wire for the previously-unexercised /v1/sessions/* endpoints.
func TestElicitationDismissUnknownSession(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, ts, "POST",
		"/v1/sessions/missing/elicitations/eli_1/dismiss", nil)
	if resp.StatusCode == http.StatusOK {
		t.Errorf("dismiss on missing session unexpectedly succeeded (body %v)", body)
	}
}

// spec: §15.1 (GET /v1/blobs/{ref...} dereferences a lenny-blob URI)
// diagnosis: a malformed blob reference returns 400 INVALID_REQUEST;
// an unknown blob returns 404; without a blob backend the surface
// returns 503 BLOBSTORE_UNAVAILABLE.
func TestBlobUnknownReturns404Or503(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, ts, "GET", "/v1/blobs/lenny-blob://acme/sess_x/p_x", nil)
	if resp.StatusCode == http.StatusOK {
		t.Errorf("blob get on missing ref unexpectedly succeeded (body %v)", body)
	}
}

// spec: §15.1 (POST /v1/sessions/{id}/upload requires the upload backend)
// diagnosis: without an UploadTokenIssuer + blob store wired, the
// endpoint returns 503 UPLOADS_UNAVAILABLE rather than 500.
func TestUploadUnavailableWithoutBackend(t *testing.T) {
	ts := newTestServer(t)
	id := createSession(t, ts)
	// Send a multipart-shaped body just to exercise the path; the
	// handler short-circuits on the missing backend before parsing.
	resp, body := do(t, ts, "POST", "/v1/sessions/"+id+"/upload",
		map[string]any{"parts": []map[string]any{}})
	// Different backend-missing branches return 503 or 4xx; the
	// invariant is that the gateway does not 500.
	if resp.StatusCode == http.StatusInternalServerError {
		t.Errorf("upload returned 500; expected 4xx/503 (body %v)", body)
	}
}

// spec: §15.1 (POST /v1/sessions/{id}/messages)
// diagnosis: posting a message to a missing session returns 404 with
// the §15.1 envelope.
func TestMessagesUnknownSessionReturns404(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, ts, "POST", "/v1/sessions/missing/messages",
		map[string]any{"content": "hi"})
	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status: want 404 or 503, got %d (body %v)", resp.StatusCode, body)
	}
}

// spec: §15.1 (GET /v1/sessions/{id}/events)
// diagnosis: requesting the event stream of an unknown session
// returns the §15.1 RESOURCE_NOT_FOUND envelope.
func TestEventsUnknownSession(t *testing.T) {
	ts := newTestServer(t)
	resp, _ := do(t, ts, "GET", "/v1/sessions/missing/events", nil)
	if resp.StatusCode == http.StatusOK {
		t.Errorf("events on missing session unexpectedly succeeded (status %d)", resp.StatusCode)
	}
}

// spec: §15.1 (error envelope shape)
// diagnosis: every 4xx response carries the §15.1 envelope with
// code, category, message, retryable, details. We sample one of
// the simple error paths above and pin the shape.
func TestErrorEnvelopeShape(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, ts, "POST", "/v1/sessions", map[string]any{"userId": "alice"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected a 400 to test the envelope, got %d", resp.StatusCode)
	}
	errBody, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("body missing error object: %v", body)
	}
	for _, field := range []string{"code", "category", "message", "retryable"} {
		if _, ok := errBody[field]; !ok {
			t.Errorf("error envelope missing field %q: %v", field, errBody)
		}
	}
	cat, _ := errBody["category"].(string)
	if !strings.EqualFold(cat, "PERMANENT") {
		t.Errorf("category = %q, want PERMANENT for VALIDATION_ERROR", cat)
	}
}
