// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §15.1 REST session lifecycle. These
// tests drive pkg/gateway/sessionserver via httptest. Every test
// covers an assertion from the §15.1 spec text — the happy path,
// the state-machine precondition table, the §15.1 error envelope.
//
// The test harness uses the in-memory session store from
// pkg/gateway/sessionstore/memstore. When the Postgres-backed store
// ships, these tests run unchanged against the production handler
// (the handler is store-agnostic).

package rest_sessions_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// newTestServer returns a running httptest.Server backed by a fresh
// memstore. Caller is responsible for calling Close().
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// do sends a request to ts with the given method, path, and body,
// returning the response and decoded JSON body.
func do(t *testing.T, ts *httptest.Server, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, ts.URL+path, reqBody)
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
	respBody, _ := io.ReadAll(resp.Body)
	if len(respBody) == 0 {
		return resp, nil
	}
	var parsed map[string]any
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		t.Fatalf("response not JSON: %v\nbody: %s", err, respBody)
	}
	return resp, parsed
}

// createSession is a helper that calls POST /v1/sessions and returns
// the session_id. Fails the test on any error.
func createSession(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	resp, body := do(t, ts, "POST", "/v1/sessions", map[string]any{
		"runtimeRef": "claude-code",
		"userId":     "alice@acme.com",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/sessions: want 201, got %d (body %v)", resp.StatusCode, body)
	}
	id, ok := body["id"].(string)
	if !ok || id == "" {
		t.Fatalf("create response missing id field; got %v", body)
	}
	return id
}

// transition is a helper that calls a state-mutating endpoint.
func transition(t *testing.T, ts *httptest.Server, id, endpoint string) (*http.Response, map[string]any) {
	t.Helper()
	return do(t, ts, "POST", fmt.Sprintf("/v1/sessions/%s/%s", id, endpoint), nil)
}

// ─── happy-path coverage ──────────────────────────────────────────

// spec: 15.1 (POST /v1/sessions: 201 Created with created state)
// diagnosis: Created session row missing fields or state. Inspect
//
//	sessionserver.createSession and the response writer.
func TestCreateSessionReturnsCreatedRow(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, ts, "POST", "/v1/sessions", map[string]any{
		"runtimeRef": "claude-code",
		"userId":     "alice@acme.com",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status: want 201, got %d", resp.StatusCode)
	}
	if got := body["state"]; got != "created" {
		t.Errorf("state: want created, got %v", got)
	}
	if got := body["tenantId"]; got != "acme" {
		t.Errorf("tenantId: want acme, got %v", got)
	}
	if got := body["runtimeRef"]; got != "claude-code" {
		t.Errorf("runtimeRef: want claude-code, got %v", got)
	}
}

// spec: 15.1 (runtimeRef is required at session creation)
// diagnosis: Missing runtimeRef did not return 400 VALIDATION_ERROR.
//
//	Inspect the request-body validator in sessionserver.
func TestCreateRejectsMissingRuntimeRef(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, ts, "POST", "/v1/sessions", map[string]any{"userId": "alice"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing runtimeRef: want 400, got %d", resp.StatusCode)
	}
	if err, ok := body["error"].(map[string]any); !ok || err["code"] != "VALIDATION_ERROR" {
		t.Errorf("error envelope: want VALIDATION_ERROR, got %v", body)
	}
}

// spec: 15.1 (GET /v1/sessions/{id} returns the stored row)
// diagnosis: GET returned a session with the wrong id, or 404. The
//
//	store path or the id parser may have drifted.
func TestGetSessionReturnsRow(t *testing.T) {
	ts := newTestServer(t)
	id := createSession(t, ts)
	resp, body := do(t, ts, "GET", "/v1/sessions/"+id, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET: want 200, got %d", resp.StatusCode)
	}
	if got := body["id"]; got != id {
		t.Errorf("id: want %q, got %v", id, got)
	}
}

// spec: 15.1 (GET on a non-existent session returns 404 RESOURCE_NOT_FOUND)
// diagnosis: A non-existent id returned a different status or code.
//
//	The store must surface ErrNotFound which maps to 404.
func TestGetSessionMissingReturns404(t *testing.T) {
	ts := newTestServer(t)
	resp, body := do(t, ts, "GET", "/v1/sessions/sess_missing", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing session: want 404, got %d", resp.StatusCode)
	}
	if err, ok := body["error"].(map[string]any); !ok || err["code"] != "RESOURCE_NOT_FOUND" {
		t.Errorf("error envelope: want RESOURCE_NOT_FOUND, got %v", body)
	}
}

// spec: 15.1 + 4.2 (cross-tenant GET returns 404 to avoid existence leak)
// diagnosis: A 403 leaked existence. The store's tenant filter must
//
//	return ErrNotFound for the other tenant's id.
func TestGetSessionCrossTenantReturns404(t *testing.T) {
	ts := newTestServer(t)
	id := createSession(t, ts) // created under tenant "acme"
	// Switch tenant header to globex.
	req, _ := http.NewRequest("GET", ts.URL+"/v1/sessions/"+id, nil)
	req.Header.Set("X-Lenny-Tenant-ID", "globex")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("cross-tenant GET: want 404 (no existence leak), got %d", resp.StatusCode)
	}
}

// spec: 15.1 (full state machine: created → ready → running → suspended → completed)
// diagnosis: A transition returned the wrong state. Inspect the
//
//	transition table in pkg/api/v1/session and the dispatch
//	in sessionserver.
func TestHappyPathLifecycle(t *testing.T) {
	ts := newTestServer(t)
	id := createSession(t, ts)

	steps := []struct {
		endpoint  string
		wantState string
	}{
		{"finalize", "ready"},
		{"start", "running"},
		{"interrupt", "suspended"},
		{"terminate", "completed"},
	}
	for _, step := range steps {
		resp, body := transition(t, ts, id, step.endpoint)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /v1/sessions/{id}/%s: want 200, got %d (body %v)", step.endpoint, resp.StatusCode, body)
		}
		if got := body["state"]; got != step.wantState {
			t.Errorf("after /%s: want state %q, got %v", step.endpoint, step.wantState, got)
		}
	}
}

// ─── §15.1 precondition table coverage ─────────────────────────────

// spec: 15.1 (start requires state=ready)
// diagnosis: A start from created returned non-409. The transition
//
//	validator skipped the precondition check.
func TestStartRejectsFromCreated(t *testing.T) {
	ts := newTestServer(t)
	id := createSession(t, ts)
	resp, body := transition(t, ts, id, "start")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("start-from-created: want 409, got %d", resp.StatusCode)
	}
	assertInvalidStateTransition(t, body, "created", []string{"ready"})
}

// spec: 15.1 (interrupt requires state=running)
// diagnosis: Interrupt from created returned non-409. Inspect the
//
//	allowedStates table.
func TestInterruptRejectsFromCreated(t *testing.T) {
	ts := newTestServer(t)
	id := createSession(t, ts)
	resp, body := transition(t, ts, id, "interrupt")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("interrupt-from-created: want 409, got %d", resp.StatusCode)
	}
	assertInvalidStateTransition(t, body, "created", []string{"running"})
}

// spec: 15.1 (finalize requires state=created)
// diagnosis: Finalize from ready returned non-409. The transition
//
//	table accepted a forbidden transition.
func TestFinalizeRejectsFromReady(t *testing.T) {
	ts := newTestServer(t)
	id := createSession(t, ts)
	transition(t, ts, id, "finalize") // → ready
	resp, body := transition(t, ts, id, "finalize")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("finalize-from-ready: want 409, got %d", resp.StatusCode)
	}
	assertInvalidStateTransition(t, body, "ready", []string{"created"})
}

// spec: 15.1 (resume requires state=awaiting_client_action)
// diagnosis: Resume from running returned non-409. The transition
//
//	table is wrong, or the resume endpoint is dispatching
//	without checking state.
func TestResumeRejectsFromRunning(t *testing.T) {
	ts := newTestServer(t)
	id := createSession(t, ts)
	transition(t, ts, id, "finalize")
	transition(t, ts, id, "start")
	resp, body := transition(t, ts, id, "resume")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("resume-from-running: want 409, got %d", resp.StatusCode)
	}
	assertInvalidStateTransition(t, body, "running", []string{"awaiting_client_action"})
}

// spec: 15.1 (terminate is admitted from every non-terminal state)
// diagnosis: Terminate from created returned non-200. The transition
//
//	table is missing created→completed via terminate.
func TestTerminateAdmitsEveryNonTerminal(t *testing.T) {
	ts := newTestServer(t)
	id := createSession(t, ts)
	// terminate from created is admitted per §15.1.
	resp, body := transition(t, ts, id, "terminate")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("terminate-from-created: want 200, got %d (body %v)", resp.StatusCode, body)
	}
}

// spec: 15.1 (terminate is rejected from terminal states: completed, failed, etc.)
// diagnosis: Terminate from completed returned non-409. The terminal
//
//	state check is missing or wrongly placed.
func TestTerminateRejectsTerminal(t *testing.T) {
	ts := newTestServer(t)
	id := createSession(t, ts)
	// drive into completed
	transition(t, ts, id, "terminate")
	// second terminate is rejected — completed is terminal
	resp, body := transition(t, ts, id, "terminate")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("terminate-from-completed: want 409, got %d", resp.StatusCode)
	}
	if err, ok := body["error"].(map[string]any); ok {
		if err["code"] != "INVALID_STATE_TRANSITION" {
			t.Errorf("error code: want INVALID_STATE_TRANSITION, got %v", err["code"])
		}
	}
}

// spec: 15.1 (DELETE /v1/sessions/{id} cancels the session)
// diagnosis: DELETE returned a session whose state is not "cancelled".
//
//	The DELETE handler must move the session to cancelled.
func TestDeleteCancelsSession(t *testing.T) {
	ts := newTestServer(t)
	id := createSession(t, ts)
	resp, body := do(t, ts, "DELETE", "/v1/sessions/"+id, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE: want 200, got %d (body %v)", resp.StatusCode, body)
	}
	if got := body["state"]; got != "cancelled" {
		t.Errorf("after DELETE: want state cancelled, got %v", got)
	}
}

// ─── §15.1 listing ────────────────────────────────────────────────

// spec: 15.1 (GET /v1/sessions returns the tenant's session list)
// diagnosis: List returned the wrong count or missing ids. The store
//
//	List path or the response writer dropped entries.
func TestListReturnsCreatedSessions(t *testing.T) {
	ts := newTestServer(t)
	id1 := createSession(t, ts)
	id2 := createSession(t, ts)
	resp, body := do(t, ts, "GET", "/v1/sessions", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: want 200, got %d", resp.StatusCode)
	}
	sessions, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("expected sessions array, got %v", body)
	}
	if len(sessions) != 2 {
		t.Errorf("list count: want 2, got %d", len(sessions))
	}
	seen := map[string]bool{}
	for _, s := range sessions {
		m := s.(map[string]any)
		seen[m["id"].(string)] = true
	}
	if !seen[id1] || !seen[id2] {
		t.Errorf("list missing created sessions: %v", seen)
	}
}

// spec: 15.1 (GET /v1/sessions?state=X filters by session state)
// diagnosis: ?state=running returned the wrong sessions. The state
//
//	filter is not being applied in the store List path.
func TestListFiltersByState(t *testing.T) {
	ts := newTestServer(t)
	idA := createSession(t, ts)
	_ = createSession(t, ts) // idB stays in created
	transition(t, ts, idA, "finalize")
	transition(t, ts, idA, "start")
	// idA → running, idB → created
	resp, body := do(t, ts, "GET", "/v1/sessions?state=running", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list filter: want 200, got %d", resp.StatusCode)
	}
	sessions := body["items"].([]any)
	if len(sessions) != 1 || sessions[0].(map[string]any)["id"] != idA {
		t.Errorf("filter ?state=running: want [idA], got %v", sessions)
	}
}

// ─── helpers ─────────────────────────────────────────────────────

func assertInvalidStateTransition(t *testing.T, body map[string]any, wantCurrent string, wantAllowedContains []string) {
	t.Helper()
	envelope, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error envelope: %v", body)
	}
	if envelope["code"] != "INVALID_STATE_TRANSITION" {
		t.Errorf("error.code: want INVALID_STATE_TRANSITION, got %v", envelope["code"])
	}
	details, ok := envelope["details"].(map[string]any)
	if !ok {
		t.Fatalf("missing error.details: %v", envelope)
	}
	if details["currentState"] != wantCurrent {
		t.Errorf("details.currentState: want %q, got %v", wantCurrent, details["currentState"])
	}
	allowed, _ := details["allowedStates"].([]any)
	for _, want := range wantAllowedContains {
		found := false
		for _, a := range allowed {
			if a == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("details.allowedStates: want to contain %q, got %v", want, allowed)
		}
	}
}

// spec: §4.2 line 156 — the session envelope carries the §4.2 record
// fields the spec lists for client visibility: recoveryGeneration
// (counter visible per spec line 156), schemaVersion, and the optional
// cwd and podAssignment when present. The counter and the schema
// version are emitted on every response so clients can read them
// uniformly across created / running / terminal states.
// diagnosis: the §4.2 wire-level fields are missing from the
// SessionResponse envelope. Recheck pkg/gateway/sessionserver
// SessionResponse and toResponse for the camelCase JSON tags
// (recoveryGeneration, schemaVersion, cwd, podAssignment).
func TestSessionEnvelopeCarriesSpec42Fields(t *testing.T) {
	ts := newTestServer(t)
	id := createSession(t, ts)

	resp, body := do(t, ts, "GET", "/v1/sessions/"+id, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET: want 200, got %d", resp.StatusCode)
	}

	// recoveryGeneration starts at 0 and is always present (the
	// counter is visible to clients per spec line 156 and the JSON
	// tag omits the empty-flag so a fresh session reports zero).
	got, ok := body["recoveryGeneration"]
	if !ok {
		t.Errorf("envelope missing 'recoveryGeneration' field (§4.2 line 156)")
	}
	// JSON numbers round-trip as float64 in map[string]any.
	if f, _ := got.(float64); f != 0 {
		t.Errorf("recoveryGeneration: want 0 on a fresh session, got %v", got)
	}

	// schemaVersion: v1 sessions report schema_version=1.
	gotSV, ok := body["schemaVersion"]
	if !ok {
		t.Errorf("envelope missing 'schemaVersion' field (§4.2 line 156)")
	}
	if f, _ := gotSV.(float64); f != 1 {
		t.Errorf("schemaVersion: want 1 on a v1 session, got %v", gotSV)
	}

	// cwd / podAssignment are omitempty (a fresh `created` session has
	// neither). The envelope must not include them when empty so the
	// minimal wire form stays compact.
	if _, ok := body["cwd"]; ok {
		t.Errorf("envelope must omit 'cwd' when empty (§4.2 omitempty contract)")
	}
	if _, ok := body["podAssignment"]; ok {
		t.Errorf("envelope must omit 'podAssignment' when empty (§4.2 omitempty contract)")
	}
}
