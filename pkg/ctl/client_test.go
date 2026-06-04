// SPDX-License-Identifier: MIT

package ctl_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ctl"
)

func TestDoSendsBearer(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "yes"})
	}))
	defer ts.Close()

	c := ctl.New(ctl.Options{BaseURL: ts.URL, Bearer: "tok-123"})
	var out map[string]string
	if err := c.Do(context.Background(), http.MethodGet, "/v1/admin/tenants", nil, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("Authorization: %q", gotAuth)
	}
	if out["ok"] != "yes" {
		t.Errorf("response not decoded: %+v", out)
	}
}

func TestDoSendsDevHeaders(t *testing.T) {
	var tenant, roles string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant = r.Header.Get("X-Lenny-Tenant-ID")
		roles = r.Header.Get("X-Lenny-Roles")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := ctl.New(ctl.Options{BaseURL: ts.URL, DevTenant: "platform", DevRoles: "platform-admin"})
	if err := c.Do(context.Background(), http.MethodGet, "/v1/admin/tenants", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if tenant != "platform" || roles != "platform-admin" {
		t.Errorf("dev headers: tenant=%q roles=%q", tenant, roles)
	}
}

func TestDoBearerWinsOverDevHeaders(t *testing.T) {
	var gotAuth, gotRoles string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotRoles = r.Header.Get("X-Lenny-Roles")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := ctl.New(ctl.Options{BaseURL: ts.URL, Bearer: "tok", DevTenant: "x", DevRoles: "platform-admin"})
	_ = c.Do(context.Background(), http.MethodGet, "/x", nil, nil)
	if gotAuth != "Bearer tok" {
		t.Errorf("bearer not sent: %q", gotAuth)
	}
	if gotRoles != "" {
		t.Errorf("dev roles should not be sent when bearer is set: %q", gotRoles)
	}
}

func TestDoDecodesAPIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "FORBIDDEN", "message": "nope"},
		})
	}))
	defer ts.Close()

	c := ctl.New(ctl.Options{BaseURL: ts.URL})
	err := c.Do(context.Background(), http.MethodGet, "/v1/admin/tenants", nil, nil)
	var apiErr *ctl.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if apiErr.Status != http.StatusForbidden || apiErr.Code != "FORBIDDEN" {
		t.Errorf("APIError: %+v", apiErr)
	}
}

func TestDoSendsJSONBody(t *testing.T) {
	var gotBody map[string]string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type: %q", ct)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	c := ctl.New(ctl.Options{BaseURL: ts.URL})
	err := c.Do(context.Background(), http.MethodPost, "/v1/admin/tenants",
		map[string]string{"id": "acme"}, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotBody["id"] != "acme" {
		t.Errorf("body not sent: %+v", gotBody)
	}
}

// TestInsecureSkipVerifyAcceptsSelfSignedTLS asserts the §24.16
// `--insecure-skip-verify` option lets the client talk to a TLS server
// with an untrusted (self-signed httptest) certificate, while a client
// without the option fails the handshake.
func TestInsecureSkipVerifyAcceptsSelfSignedTLS(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "yes"})
	}))
	defer ts.Close()

	// Without the flag, the self-signed cert fails verification.
	secure := ctl.New(ctl.Options{BaseURL: ts.URL})
	if err := secure.Do(context.Background(), http.MethodGet, "/healthz", nil, nil); err == nil {
		t.Fatal("expected TLS verification error without InsecureSkipVerify")
	}

	// With the flag, the request succeeds.
	insecure := ctl.New(ctl.Options{BaseURL: ts.URL, InsecureSkipVerify: true})
	var out map[string]string
	if err := insecure.Do(context.Background(), http.MethodGet, "/healthz", nil, &out); err != nil {
		t.Fatalf("Do with InsecureSkipVerify: %v", err)
	}
	if out["ok"] != "yes" {
		t.Errorf("response not decoded: %+v", out)
	}
}

// TestStreamCopiesSSEBody verifies the §25.14 SSE tail surface: Stream
// opens a GET, sends the Accept: text/event-stream header, carries the
// bearer, and copies the response body verbatim to the writer until the
// server closes the stream. spec: §25.14 line 4920 (events tail).
func TestStreamCopiesSSEBody(t *testing.T) {
	const frames = "id: 1\ndata: {\"type\":\"ops.health_status_changed\"}\n\nid: 2\ndata: {\"type\":\"ops.escalation_created\"}\n\n"
	var sawAccept, sawBearer string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAccept = r.Header.Get("Accept")
		sawBearer = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(frames))
	}))
	defer ts.Close()

	c := ctl.New(ctl.Options{BaseURL: ts.URL, Bearer: "tok-stream"})
	var buf bytes.Buffer
	if err := c.Stream(context.Background(), "/v1/admin/events/stream", &buf); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if buf.String() != frames {
		t.Errorf("stream body: got %q, want %q", buf.String(), frames)
	}
	if sawAccept != "text/event-stream" {
		t.Errorf("Accept header: got %q", sawAccept)
	}
	if sawBearer != "Bearer tok-stream" {
		t.Errorf("Authorization header: got %q", sawBearer)
	}
}

// TestStreamSurfacesAPIError verifies a non-2xx stream open returns the
// decoded §15.1 error envelope rather than copying an error body.
func TestStreamSurfacesAPIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"FORBIDDEN","message":"no"}}`))
	}))
	defer ts.Close()

	c := ctl.New(ctl.Options{BaseURL: ts.URL})
	var buf bytes.Buffer
	err := c.Stream(context.Background(), "/v1/admin/events/stream", &buf)
	var apiErr *ctl.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		t.Fatalf("Stream error: got %v, want APIError 403", err)
	}
	if buf.Len() != 0 {
		t.Errorf("error stream should not copy a body: %q", buf.String())
	}
}

// TestGetCopiesTextBody verifies the §24.15 pod-log proxy primitive: Get
// opens a bounded GET, carries the bearer, and copies the text/plain
// response body verbatim to the writer. spec: §24.15 line 192; §25.4.
func TestGetCopiesTextBody(t *testing.T) {
	const logLines = "2026-06-04T00:00:00Z line one\n2026-06-04T00:00:01Z line two\n"
	var sawBearer string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawBearer = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(logLines))
	}))
	defer ts.Close()

	c := ctl.New(ctl.Options{BaseURL: ts.URL, Bearer: "tok-logs"})
	var buf bytes.Buffer
	if err := c.Get(context.Background(), "/v1/admin/logs/pods/ns/pod", &buf); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if buf.String() != logLines {
		t.Errorf("body: got %q, want %q", buf.String(), logLines)
	}
	if sawBearer != "Bearer tok-logs" {
		t.Errorf("Authorization header: got %q", sawBearer)
	}
}

// TestGetSurfacesAPIError verifies a non-2xx Get returns the decoded
// §15.1 error envelope (e.g. the §25.4 404 POD_NOT_FOUND) and copies no
// body to the writer.
func TestGetSurfacesAPIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"POD_NOT_FOUND","message":"no pod ns/ghost"}}`))
	}))
	defer ts.Close()

	c := ctl.New(ctl.Options{BaseURL: ts.URL})
	var buf bytes.Buffer
	err := c.Get(context.Background(), "/v1/admin/logs/pods/ns/ghost", &buf)
	var apiErr *ctl.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound || apiErr.Code != "POD_NOT_FOUND" {
		t.Fatalf("Get error: got %v, want APIError 404 POD_NOT_FOUND", err)
	}
	if buf.Len() != 0 {
		t.Errorf("error response should not copy a body: %q", buf.String())
	}
}

// TestStreamCancelledContextReturnsNil verifies an operator interrupt
// (cancelled ctx) is the normal tail-exit path and surfaces as success.
func TestStreamCancelledContextReturnsNil(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": open\n\n"))
		if fl != nil {
			fl.Flush()
		}
		<-r.Context().Done()
	}))
	defer ts.Close()

	c := ctl.New(ctl.Options{BaseURL: ts.URL})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if err := c.Stream(ctx, "/v1/admin/events/stream", &bytes.Buffer{}); err != nil {
		t.Errorf("cancelled stream should return nil, got %v", err)
	}
}

// spec: §15.1 lines 1207-1213 — PutIfMatch fetches the resource's ETag via a
// GET, then PUTs it back as If-Match (the documented read-modify-write).
func TestPutIfMatchReadModifyWrite(t *testing.T) {
	var gotIfMatch string
	var sawGet, sawPut bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			sawGet = true
			w.Header().Set("ETag", `"7"`)
			_ = json.NewEncoder(w).Encode(map[string]string{"name": "p1"})
		case http.MethodPut:
			sawPut = true
			gotIfMatch = r.Header.Get("If-Match")
			_ = json.NewEncoder(w).Encode(map[string]string{"name": "p1"})
		}
	}))
	defer ts.Close()

	c := ctl.New(ctl.Options{BaseURL: ts.URL})
	var out map[string]string
	if err := c.PutIfMatch(context.Background(), "/v1/admin/pools/p1", "/v1/admin/pools/p1",
		map[string]any{"name": "p1"}, &out); err != nil {
		t.Fatalf("PutIfMatch: %v", err)
	}
	if !sawGet || !sawPut {
		t.Fatalf("expected GET then PUT: get=%v put=%v", sawGet, sawPut)
	}
	if gotIfMatch != `"7"` {
		t.Errorf("If-Match = %q, want %q", gotIfMatch, `"7"`)
	}
}

// A stale If-Match surfaces the gateway's 412 ETAG_MISMATCH as an APIError so
// the operator sees the precondition failure rather than a silent overwrite.
func TestPutIfMatchSurfacesMismatch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("ETag", `"7"`)
			_ = json.NewEncoder(w).Encode(map[string]string{"name": "p1"})
			return
		}
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"error":{"code":"ETAG_MISMATCH","message":"stale"}}`))
	}))
	defer ts.Close()

	c := ctl.New(ctl.Options{BaseURL: ts.URL})
	err := c.PutIfMatch(context.Background(), "/v1/admin/pools/p1", "/v1/admin/pools/p1",
		map[string]any{"name": "p1"}, nil)
	var apiErr *ctl.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "ETAG_MISMATCH" {
		t.Fatalf("PutIfMatch error = %v, want APIError ETAG_MISMATCH", err)
	}
}
