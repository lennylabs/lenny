// SPDX-License-Identifier: MIT

package credentialserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentialserver"
	"github.com/lennylabs/lenny/pkg/gateway/credentialstore"
)

// fakeLeaseProp records the §4.9 rotate/revoke propagation calls and
// returns a fixed affected-lease count so the handler-emitted audit fields
// can be asserted.
type fakeLeaseProp struct {
	rotateCalls    []string
	revokeCalls    []string
	rotatedCount   int
	terminateCount int
}

func (f *fakeLeaseProp) RotateUser(_ context.Context, _, ref string) (int, error) {
	f.rotateCalls = append(f.rotateCalls, ref)
	return f.rotatedCount, nil
}

func (f *fakeLeaseProp) RevokeUser(_ context.Context, _, ref string) (int, error) {
	f.revokeCalls = append(f.revokeCalls, ref)
	return f.terminateCount, nil
}

func propagatingServer(t *testing.T, prop credentialserver.LeasePropagator) (*credentialserver.Server, *recordingAuditSink) {
	t.Helper()
	store := credentialstore.NewMemory(nil)
	sink := &recordingAuditSink{}
	return credentialserver.New(store).WithAudit(sink).WithLeasePropagator(prop), sink
}

// TestRotatePropagatesAndCounts_spec_4_9_1350 confirms the PUT handler
// calls RotateUser and records its non-zero count in the credential.rotated
// audit event (F-4.9.8).
func TestRotatePropagatesAndCounts_spec_4_9_1350(t *testing.T) {
	prop := &fakeLeaseProp{rotatedCount: 2}
	srv, sink := propagatingServer(t, prop)
	ref := registerOne(t, srv)

	body, _ := json.Marshal(credentialserver.RotateRequest{Secret: "sk-new"})
	req := asUser(httptest.NewRequest(http.MethodPut, "/v1/credentials/"+ref, bytes.NewReader(body)), "acme", "alice")
	req.SetPathValue("credential_ref", ref)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("rotate: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(prop.rotateCalls) != 1 || prop.rotateCalls[0] != ref {
		t.Fatalf("RotateUser calls = %v, want [%s]", prop.rotateCalls, ref)
	}
	ev := sink.events[len(sink.events)-1]
	if got := ev.detail["active_leases_rotated"]; got != 2 {
		t.Fatalf("active_leases_rotated = %v, want 2", got)
	}
}

// TestRevokePropagatesAndCounts_spec_4_9_1351 confirms the revoke handler
// calls RevokeUser and records its non-zero count in the
// credential.user_revoked audit event (F-4.9.8 / F-4.9.15).
func TestRevokePropagatesAndCounts_spec_4_9_1351(t *testing.T) {
	prop := &fakeLeaseProp{terminateCount: 3}
	srv, sink := propagatingServer(t, prop)
	ref := registerOne(t, srv)

	req := asUser(httptest.NewRequest(http.MethodPost, "/v1/credentials/"+ref+"/revoke", nil), "acme", "alice")
	req.SetPathValue("credential_ref", ref)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("revoke: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(prop.revokeCalls) != 1 || prop.revokeCalls[0] != ref {
		t.Fatalf("RevokeUser calls = %v, want [%s]", prop.revokeCalls, ref)
	}
	ev := sink.events[len(sink.events)-1]
	if got := ev.detail["active_leases_terminated"]; got != 3 {
		t.Fatalf("active_leases_terminated = %v, want 3", got)
	}
}

// TestRotateNoPropagatorIsZero_spec_4_9_1350 confirms a server with no
// propagator wired still rotates the registry record and reports a zero
// affected-lease count rather than failing.
func TestRotateNoPropagatorIsZero_spec_4_9_1350(t *testing.T) {
	store := credentialstore.NewMemory(nil)
	sink := &recordingAuditSink{}
	srv := credentialserver.New(store).WithAudit(sink) // no propagator
	ref := registerOne(t, srv)

	body, _ := json.Marshal(credentialserver.RotateRequest{Secret: "sk-new"})
	req := asUser(httptest.NewRequest(http.MethodPut, "/v1/credentials/"+ref, bytes.NewReader(body)), "acme", "alice")
	req.SetPathValue("credential_ref", ref)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("rotate: %d", rr.Code)
	}
	ev := sink.events[len(sink.events)-1]
	if got := ev.detail["active_leases_rotated"]; got != 0 {
		t.Fatalf("active_leases_rotated = %v, want 0 (no propagator)", got)
	}
	_ = credential.ProviderAnthropicDirect // keep import stable
}
