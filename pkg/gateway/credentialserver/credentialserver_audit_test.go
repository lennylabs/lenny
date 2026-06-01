// SPDX-License-Identifier: MIT

package credentialserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentialserver"
	"github.com/lennylabs/lenny/pkg/gateway/credentialstore"
)

// recordingAuditSink captures the §4.9.2 credential audit events the
// server emits so a test can assert the event type and field set.
type recordingAuditSink struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	eventType string
	detail    map[string]any
}

func (s *recordingAuditSink) EmitCredentialEvent(_ context.Context, eventType string, detail map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, recordedEvent{eventType: eventType, detail: detail})
}

func (s *recordingAuditSink) only(t *testing.T) recordedEvent {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) != 1 {
		t.Fatalf("want exactly one audit event, got %d: %+v", len(s.events), s.events)
	}
	return s.events[0]
}

func auditedServer(t *testing.T) (*credentialserver.Server, *recordingAuditSink) {
	t.Helper()
	store := credentialstore.NewMemory(nil)
	sink := &recordingAuditSink{}
	return credentialserver.New(store).WithAudit(sink), sink
}

func registerOne(t *testing.T, srv *credentialserver.Server) string {
	t.Helper()
	body, _ := json.Marshal(credentialserver.RegisterRequest{
		Provider: string(credential.ProviderAnthropicDirect),
		Secret:   "sk-secret-value",
	})
	req := asUser(httptest.NewRequest(http.MethodPost, "/v1/credentials", bytes.NewReader(body)), "acme", "alice")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("register: %d body=%s", rr.Code, rr.Body.String())
	}
	var resp credentialserver.CredentialPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	return resp.Ref
}

// spec: §4.9.2 — credential.registered carries tenant_id, user_id,
// provider, credential_ref.
func TestRegisterEmitsCredentialRegistered_F491(t *testing.T) {
	srv, sink := auditedServer(t)
	ref := registerOne(t, srv)

	ev := sink.only(t)
	if ev.eventType != credential.AuditCredentialRegistered.String() {
		t.Fatalf("event type = %q, want credential.registered", ev.eventType)
	}
	assertField(t, ev, "tenant_id", "acme")
	assertField(t, ev, "user_id", "alice")
	assertField(t, ev, "provider", string(credential.ProviderAnthropicDirect))
	assertField(t, ev, "credential_ref", ref)
}

// spec: §4.9.2 — credential.rotated carries active_leases_rotated; no
// user-backed lease is rotated by this path today so the count is 0.
func TestRotateEmitsCredentialRotated_F491(t *testing.T) {
	srv, sink := auditedServer(t)
	ref := registerOne(t, srv)

	body, _ := json.Marshal(credentialserver.RotateRequest{Secret: "sk-new-value"})
	req := asUser(httptest.NewRequest(http.MethodPut, "/v1/credentials/"+ref, bytes.NewReader(body)), "acme", "alice")
	req.SetPathValue("credential_ref", ref)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("rotate: %d body=%s", rr.Code, rr.Body.String())
	}

	// The register event is first; the rotate event is the second.
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.events) != 2 {
		t.Fatalf("want register+rotate events, got %d", len(sink.events))
	}
	ev := sink.events[1]
	if ev.eventType != credential.AuditCredentialRotated.String() {
		t.Fatalf("event type = %q, want credential.rotated", ev.eventType)
	}
	assertField(t, ev, "credential_ref", ref)
	if got, ok := ev.detail["active_leases_rotated"]; !ok || got != 0 {
		t.Errorf("active_leases_rotated = %v (ok=%v), want 0", got, ok)
	}
}

// spec: §4.9.2 — credential.user_revoked carries reason and
// active_leases_terminated.
func TestRevokeEmitsUserRevoked_F491(t *testing.T) {
	srv, sink := auditedServer(t)
	ref := registerOne(t, srv)

	body, _ := json.Marshal(credentialserver.RevokeRequest{Reason: "suspected_exfiltration"})
	req := asUser(httptest.NewRequest(http.MethodPost, "/v1/credentials/"+ref+"/revoke", bytes.NewReader(body)), "acme", "alice")
	req.SetPathValue("credential_ref", ref)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("revoke: %d body=%s", rr.Code, rr.Body.String())
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	ev := sink.events[len(sink.events)-1]
	if ev.eventType != credential.AuditCredentialUserRevoked.String() {
		t.Fatalf("event type = %q, want credential.user_revoked", ev.eventType)
	}
	assertField(t, ev, "reason", "suspected_exfiltration")
	if got, ok := ev.detail["active_leases_terminated"]; !ok || got != 0 {
		t.Errorf("active_leases_terminated = %v (ok=%v), want 0", got, ok)
	}
}

// An absent revoke body is valid and records an empty reason (spec
// §4.9.2 — reason is a recorded field; the body is optional).
func TestRevokeWithoutBodyEmitsEmptyReason_F491(t *testing.T) {
	srv, sink := auditedServer(t)
	ref := registerOne(t, srv)

	req := asUser(httptest.NewRequest(http.MethodPost, "/v1/credentials/"+ref+"/revoke", nil), "acme", "alice")
	req.SetPathValue("credential_ref", ref)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("revoke: %d body=%s", rr.Code, rr.Body.String())
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	ev := sink.events[len(sink.events)-1]
	assertField(t, ev, "reason", "")
}

// spec: §4.9.2 — credential.deleted carries provider resolved from the
// pre-delete read.
func TestDeleteEmitsCredentialDeleted_F491(t *testing.T) {
	srv, sink := auditedServer(t)
	ref := registerOne(t, srv)

	req := asUser(httptest.NewRequest(http.MethodDelete, "/v1/credentials/"+ref, nil), "acme", "alice")
	req.SetPathValue("credential_ref", ref)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: %d body=%s", rr.Code, rr.Body.String())
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	ev := sink.events[len(sink.events)-1]
	if ev.eventType != credential.AuditCredentialDeleted.String() {
		t.Fatalf("event type = %q, want credential.deleted", ev.eventType)
	}
	assertField(t, ev, "provider", string(credential.ProviderAnthropicDirect))
	assertField(t, ev, "credential_ref", ref)
}

// A failed mutation emits no audit event — only successful lifecycle
// transitions are recorded.
func TestFailedRegisterEmitsNoEvent_F491(t *testing.T) {
	srv, sink := auditedServer(t)
	body, _ := json.Marshal(credentialserver.RegisterRequest{Provider: "not_a_provider", Secret: "x"})
	req := asUser(httptest.NewRequest(http.MethodPost, "/v1/credentials", bytes.NewReader(body)), "acme", "alice")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for bad provider, got %d", rr.Code)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.events) != 0 {
		t.Fatalf("a rejected register must emit no audit event, got %d", len(sink.events))
	}
}

// A server without a wired sink mutates the store and does not panic
// (nil-sink contract).
func TestNilAuditSinkIsNoOp_F491(t *testing.T) {
	store := credentialstore.NewMemory(nil)
	srv := credentialserver.New(store) // no WithAudit
	body, _ := json.Marshal(credentialserver.RegisterRequest{
		Provider: string(credential.ProviderAnthropicDirect),
		Secret:   "sk-secret-value",
	})
	req := asUser(httptest.NewRequest(http.MethodPost, "/v1/credentials", bytes.NewReader(body)), "acme", "alice")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("register without audit sink: %d", rr.Code)
	}
}

func assertField(t *testing.T, ev recordedEvent, key, want string) {
	t.Helper()
	got, ok := ev.detail[key].(string)
	if !ok || got != want {
		t.Errorf("detail[%q] = %v (ok=%v), want %q", key, ev.detail[key], ok, want)
	}
}
