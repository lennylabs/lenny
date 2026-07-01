// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the §11.5 Idempotency-Key middleware.
// Drives pkg/gateway/middleware/idempotency wrapped around the
// §15.1 sessionserver handler via httptest.

package rest_idempotency_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})
	idem := idemmw.NewMemoryStore()
	wrapped := idemmw.Wrap(srv.Handler(), idem, idemmw.Options{})
	ts := httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)
	return ts
}

func post(t *testing.T, ts *httptest.Server, path, idemKey string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var reqBody io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reqBody = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+path, reqBody)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	if idemKey != "" {
		req.Header.Set(idemmw.HeaderName, idemKey)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) == 0 {
		return resp, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("response not JSON: %v\nbody: %s", err, raw)
	}
	return resp, out
}

// spec: 11.5 (Idempotency-Key middleware: cache-on-first-write, replay-on-second-write)
// diagnosis: Replay returned a different session id — the middleware
//
//	did not consult the store, or the response cache was not
//	written on the first call. Inspect captureWriter.flush and
//	store.Put in pkg/gateway/middleware/idempotency.
func TestIdempotencyReplayReturnsCachedResponse(t *testing.T) {
	ts := newTestServer(t)
	body := map[string]any{"runtimeRef": "claude-code", "userId": "alice"}

	// First call creates the session.
	resp1, b1 := post(t, ts, "/v1/sessions", "key-1", body)
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first call: want 201, got %d", resp1.StatusCode)
	}
	id1, _ := b1["id"].(string)

	// Replay with the same key+body must return the same session_id
	// without creating a second row.
	resp2, b2 := post(t, ts, "/v1/sessions", "key-1", body)
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("replay: want 201, got %d", resp2.StatusCode)
	}
	id2, _ := b2["id"].(string)
	if id1 != id2 {
		t.Errorf("replay must return cached id; got %q vs %q", id1, id2)
	}
}

// spec: 11.5 (IDEMPOTENCY_KEY_REUSED envelope on body-hash mismatch)
// diagnosis: Different request body under the same key returned a
//
//	status other than 422 — either the body hash was not
//	persisted, DetectReuse was not invoked, or the envelope
//	mapping in writeError dropped the code.
func TestIdempotencyDifferentBodyReturns422(t *testing.T) {
	ts := newTestServer(t)
	post(t, ts, "/v1/sessions", "key-2", map[string]any{"runtimeRef": "claude-code"})
	resp, body := post(t, ts, "/v1/sessions", "key-2", map[string]any{"runtimeRef": "DIFFERENT"})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("reuse-different-body: want 422, got %d (body %v)", resp.StatusCode, body)
	}
	envelope, _ := body["error"].(map[string]any)
	if envelope["code"] != "IDEMPOTENCY_KEY_REUSED" {
		t.Errorf("error code: want IDEMPOTENCY_KEY_REUSED, got %v", envelope["code"])
	}
}

// spec: 11.5 (key length bound: 128 octets)
// diagnosis: Oversize idempotency key not rejected with 400
//
//	INVALID_IDEMPOTENCY_KEY — Key.Validate is not running or
//	the length cap moved. Check pkg/idempotency.Key.Validate.
func TestIdempotencyOversizeKeyRejected(t *testing.T) {
	ts := newTestServer(t)
	oversize := strings.Repeat("a", 129)
	resp, body := post(t, ts, "/v1/sessions", oversize, map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("oversize key: want 400, got %d", resp.StatusCode)
	}
	envelope, _ := body["error"].(map[string]any)
	if envelope["code"] != "INVALID_IDEMPOTENCY_KEY" {
		t.Errorf("error code: want INVALID_IDEMPOTENCY_KEY, got %v", envelope["code"])
	}
}

// spec: 11.5 (Idempotency-Key is optional; no-key requests pass through)
// diagnosis: Requests without Idempotency-Key were rejected, or two
//
//	such requests collapsed to the same session. The
//	middleware must short-circuit when the header is absent.
func TestIdempotencyNoHeaderIsPassthrough(t *testing.T) {
	ts := newTestServer(t)
	// Two calls without idempotency key — both should create.
	r1, b1 := post(t, ts, "/v1/sessions", "", map[string]any{"runtimeRef": "x"})
	r2, b2 := post(t, ts, "/v1/sessions", "", map[string]any{"runtimeRef": "x"})
	if r1.StatusCode != http.StatusCreated || r2.StatusCode != http.StatusCreated {
		t.Errorf("no-key calls must succeed")
	}
	if b1["id"] == b2["id"] {
		t.Errorf("no-key calls must create distinct sessions; both got %v", b1["id"])
	}
}

// spec: 11.5 (tenant-scoped key), 4.2 (cross-tenant isolation)
// diagnosis: Same idempotency key value from a second tenant
//
//	replayed the first tenant's response. The store key must
//	include the tenant id; cross-tenant isolation requires
//	both halves of (tenant_id, key) to match.
func TestIdempotencyTenantScoped(t *testing.T) {
	ts := newTestServer(t)
	body := map[string]any{"runtimeRef": "claude-code"}

	// Tenant A creates with key-3.
	req1, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/sessions", bytes.NewReader(mustJSON(body)))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-Lenny-Tenant-ID", "acme")
	req1.Header.Set(idemmw.HeaderName, "key-3")
	resp1, err := ts.Client().Do(req1)
	if err != nil {
		t.Fatalf("tenant A do: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("tenant A first call: want 201, got %d", resp1.StatusCode)
	}

	// Tenant B with the same key+body — should NOT replay tenant A's
	// response; the §11.5 key is tenant-scoped.
	req2, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/sessions", bytes.NewReader(mustJSON(body)))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Lenny-Tenant-ID", "globex")
	req2.Header.Set(idemmw.HeaderName, "key-3")
	resp2, err := ts.Client().Do(req2)
	if err != nil {
		t.Fatalf("tenant B do: %v", err)
	}
	defer resp2.Body.Close()
	raw, _ := io.ReadAll(resp2.Body)
	var b2 map[string]any
	_ = json.Unmarshal(raw, &b2)

	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("tenant B: want 201, got %d (body %s)", resp2.StatusCode, raw)
	}
	if got := b2["tenantId"]; got != "globex" {
		t.Errorf("tenant B should see its own tenant; got %v", got)
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
