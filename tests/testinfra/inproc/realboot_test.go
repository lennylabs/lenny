// SPDX-License-Identifier: MIT

package inproc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// postJSON issues a JSON request against the harness gateway and
// returns the status and decoded body.
func postJSON(t *testing.T, method, url, body string) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) == 0 {
		return resp.StatusCode, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s %s: response not JSON: %v\nbody: %s", method, url, err, raw)
	}
	return resp.StatusCode, out
}

// TestHarnessBootsRealLennyGateway pins the TESTING.md §12.7.a
// requirement that tier-7a multi-component scenarios run against an
// in-process harness that boots a single-binary Lenny with miniredis
// and a fake Kubernetes API surface, rather than against a
// harness-local mock of the gateway.
//
// It asserts three things a hand-written listener would not produce:
// the §15.1 create envelope (a `state` of "created", the resolved
// tenant, the echoed runtimeRef); the §15.1 precondition table
// rejecting start-from-created with 409 INVALID_STATE_TRANSITION; and
// a non-empty embedded-Redis keyspace after the create, which only
// appears when the §11.1 Redis-backed admission counter really ran.
//
// spec: TESTING.md §12.7.a ("Every component is exercised either as a
// per-package bench ... or through an in-process multi-component
// harness (tests/testinfra/inproc) that boots a single-binary Lenny
// with miniredis, an embedded Postgres adapter, and a fake Kubernetes
// API surface (tests/testinfra/fakekube)."); §15.1 (session lifecycle
// precondition table); §11.1 (per-runtime admission rate limit)
func TestHarnessBootsRealLennyGateway(t *testing.T) {
	env := New(Config{})
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()

	gw := env.GatewayURL()
	status, body := postJSON(t, http.MethodPost, gw+"/v1/sessions", `{"runtimeRef":"echo","userId":"alice@acme.com"}`)
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/sessions: status=%d want 201 (body %v)", status, body)
	}
	if got := body["state"]; got != "created" {
		t.Errorf("§15.1 create envelope: state=%v want \"created\"", got)
	}
	if got := body["tenantId"]; got != "acme" {
		t.Errorf("§15.1 create envelope: tenantId=%v want \"acme\"", got)
	}
	if got := body["runtimeRef"]; got != "echo" {
		t.Errorf("§15.1 create envelope: runtimeRef=%v want \"echo\"", got)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("§15.1 create envelope: missing id (body %v)", body)
	}

	// §15.1 precondition table: start requires state=ready, so a start
	// from created is rejected with 409 INVALID_STATE_TRANSITION.
	status, body = postJSON(t, http.MethodPost, gw+"/v1/sessions/"+id+"/start", "")
	if status != http.StatusConflict {
		t.Fatalf("POST /v1/sessions/{id}/start from created: status=%d want 409 (body %v)", status, body)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("§15.1 error envelope missing from precondition failure: %v", body)
	}
	if errObj["code"] != "INVALID_STATE_TRANSITION" {
		t.Errorf("§15.1 precondition failure: code=%v want INVALID_STATE_TRANSITION", errObj["code"])
	}

	// §11.1: the per-runtime admission counter is Redis-backed, so the
	// create above wrote a window key into the harness's embedded
	// miniredis. An empty keyspace means the request never reached the
	// gateway's admission path.
	if keys := env.redis.Keys(); len(keys) == 0 {
		t.Fatal("embedded Redis keyspace empty after a session create: the §11.1 admission counter did not run, so the harness is not driving the gateway's real admission path")
	}

	// The §15.1 DELETE transition moves the session to cancelled and
	// returns the updated envelope.
	status, body = postJSON(t, http.MethodDelete, gw+"/v1/sessions/"+id, "")
	if status != http.StatusOK {
		t.Fatalf("DELETE /v1/sessions/{id}: status=%d want 200 (body %v)", status, body)
	}
	if got := body["state"]; got != "cancelled" {
		t.Errorf("§15.1 delete: state=%v want \"cancelled\"", got)
	}
}

// TestHarnessEnforcesIdempotencyContract pins that the harness runs the
// §11.5 Idempotency-Key middleware from pkg/gateway/middleware/idempotency
// rather than a harness-local cache: a repeated POST with the same key
// and body replays the cached response without creating a second
// session, and Env.IdempotencyHits observes the replay.
//
// spec: TESTING.md §12.7.a (in-process multi-component harness boots a
// single-binary Lenny); §11.5 (Idempotency-Key: the retry returns the
// original response without re-executing the operation)
func TestHarnessEnforcesIdempotencyContract(t *testing.T) {
	env := New(Config{})
	if err := env.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = env.Stop(context.Background()) }()

	gw := env.GatewayURL()
	post := func() (int, map[string]any) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, gw+"/v1/sessions",
			bytes.NewReader([]byte(`{"runtimeRef":"echo"}`)))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("Idempotency-Key", "replay-key")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /v1/sessions: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return resp.StatusCode, out
	}

	status1, body1 := post()
	if status1 != http.StatusCreated {
		t.Fatalf("first POST: status=%d want 201 (body %v)", status1, body1)
	}
	status2, body2 := post()
	if status2 != http.StatusCreated {
		t.Fatalf("replayed POST: status=%d want 201 (body %v)", status2, body2)
	}
	if body1["id"] != body2["id"] {
		t.Errorf("§11.5 replay returned a different session: %v vs %v", body1["id"], body2["id"])
	}
	if env.SessionCount() != 1 {
		t.Errorf("§11.5: %d sessions created for one idempotency key, want 1", env.SessionCount())
	}
	if env.IdempotencyHits() == 0 {
		t.Error("§11.5: the second POST was not served from the idempotency cache")
	}
}
