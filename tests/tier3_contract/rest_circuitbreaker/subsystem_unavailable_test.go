// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract test for the §4.1 partial-degradation contract:
// when a single gateway subsystem's per-replica circuit breaker is
// open, that subsystem's endpoint returns 503 SUBSYSTEM_UNAVAILABLE
// on the wire while the other subsystems on the same gateway keep
// serving. This pins the wire mapping (open Upload Handler breaker →
// 503 SUBSYSTEM_UNAVAILABLE envelope with retryable=true) and the
// "others continue serving" half (a Stream Proxy attach returns 200).
//
// It differs from circuitbreaker_test.go in the same package, which
// covers the §11.6 operator-managed, Redis-backed breakers. This file
// covers the §4.1 per-subsystem in-memory breakers wired through
// pkg/gateway/core/subsystem and pkg/gateway/sessionserver/upload.go.

package rest_circuitbreaker_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// openUploadBreaker returns a §4.1 per-subsystem breaker already in the
// open state. It trips a fresh breaker by recording one failure against
// a one-failure threshold, then pins a long cooldown so the breaker
// stays open (does not admit a half-open probe) for the duration of the
// test. This is the "force a single subsystem breaker open" hook the
// partial-degradation contract needs, expressed against the breaker's
// own public state machine rather than a test-only backdoor.
func openUploadBreaker(t *testing.T) *subsystem.Breaker {
	t.Helper()
	b := &subsystem.Breaker{FailureThreshold: 1, Cooldown: time.Hour}
	b.RecordFailure()
	if got := b.State(); got != subsystem.StateOpen {
		t.Fatalf("breaker setup: want state %q, got %q", subsystem.StateOpen, got)
	}
	return b
}

// spec: §4.1 (Per-subsystem isolation guarantees) — "The Upload Handler
// can trip to half-open or open state — returning 503 for uploads —
// while the Stream Proxy and MCP Fabric continue serving normally. This
// is the primary mechanism for partial gateway degradation."
func TestOpenUploadSubsystemBreakerReturns503WhileStreamProxyServes(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(64)
	// A blob store must be configured so the upload handler reaches the
	// §4.1 subsystem gate: the handler rejects a nil blob store with
	// BLOBSTORE_UNAVAILABLE before it evaluates the breaker. Production
	// always wires one, so this is a test-harness prerequisite rather
	// than a behavior under test.
	srv := sessionserver.New(store, sessionserver.Options{
		Events: bus,
		Blobs:  blobstore.NewMemoryStore(time.Now),
		UploadSubsystem: &subsystem.Subsystem{
			Name:    "upload_handler",
			Breaker: openUploadBreaker(t),
		},
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Create a session so the Stream Proxy attach below targets a real
	// row. The create path is not gated by the Upload Handler subsystem,
	// so it succeeds even though the upload breaker is open.
	id := createSession(t, ts)

	// The Upload Handler subsystem's breaker is open: a new upload must
	// shed with the §15.1 503 SUBSYSTEM_UNAVAILABLE envelope. The
	// subsystem gate is evaluated at handler entry, ahead of upload-token
	// and session validation, so the open breaker short-circuits the
	// request regardless of the body.
	resp, body := doRequest(t, ts, http.MethodPost, "/v1/sessions/"+id+"/upload", "text/plain", "payload", nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("open upload breaker: want 503, got %d (body %v)", resp.StatusCode, body)
	}
	envelope, _ := body["error"].(map[string]any)
	if envelope == nil {
		t.Fatalf("open upload breaker: missing error envelope (body %v)", body)
	}
	if envelope["code"] != "SUBSYSTEM_UNAVAILABLE" {
		t.Errorf("error code: want SUBSYSTEM_UNAVAILABLE, got %v", envelope["code"])
	}
	// §4.1 line 115 / §15.2.1: the per-replica breaker is transient — the
	// client should retry so the load balancer distributes the retry
	// across replicas. The 503 envelope therefore carries retryable=true.
	if envelope["retryable"] != true {
		t.Errorf("error retryable: want true, got %v", envelope["retryable"])
	}

	// The "others continue serving" half: the Stream Proxy subsystem
	// (session attachment / event relay) on the same gateway serves a
	// 200 while the Upload Handler breaker is open. The JSON list form of
	// the event stream returns the §15.1 pagination envelope without
	// holding the connection open.
	streamResp, _ := doRequest(t, ts, http.MethodGet, "/v1/sessions/"+id+"/events", "", "",
		map[string]string{"Accept": "application/json"})
	if streamResp.StatusCode != http.StatusOK {
		t.Fatalf("stream proxy attach while upload breaker open: want 200, got %d", streamResp.StatusCode)
	}
}

// createSession creates a session via POST /v1/sessions and returns its
// id, failing the test on any non-201 response.
func createSession(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	resp, body := doRequest(t, ts, http.MethodPost, "/v1/sessions", "application/json",
		`{"runtimeRef":"claude-code","userId":"alice@acme.com"}`, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create session: want 201, got %d (body %v)", resp.StatusCode, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("create session: empty id (body %v)", body)
	}
	return id
}

// doRequest issues an HTTP request with the dev-header tenant every
// request in this suite authenticates as, and decodes the JSON response
// body into a map (empty for a streaming or empty body). It returns the
// response and the decoded body.
func doRequest(t *testing.T, ts *httptest.Server, method, path, contentType, reqBody string, headers map[string]string) (*http.Response, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if reqBody != "" {
		rdr = strings.NewReader(reqBody)
	}
	req, err := http.NewRequest(method, ts.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp, out
}
