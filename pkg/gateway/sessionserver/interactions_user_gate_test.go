// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// spec: §11.4 — hard_disable "reject[s] pending delegation approvals
// for the user". A tool-use approval is the in-code form of a pending
// delegation approval; a disabled user may not resolve it. F-11.4.2.
func TestToolUseApproveDeniedForDisabledUser_spec_11_4(t *testing.T) {
	us := userstore.NewMemory()
	if err := us.Create(context.Background(), userstore.User{
		Subject: "alice", TenantID: "acme", Disabled: true,
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	store := interactionstore.NewMemory()
	_ = store.Put(context.Background(), interactionstore.Interaction{
		ID: "tc_1", Kind: interactionstore.KindToolUse,
		SessionID: "sess_1", TenantID: "acme", UserID: "alice",
	})
	srv := sessionserver.New(memstore.New(), sessionserver.Options{DevTenantHeader: true, Interactions: store, Users: us})

	req := asAlice(httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/tool-use/tc_1/approve", nil))
	rr := httptest.NewRecorder()
	asOwner(srv.Handler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("disabled user approve: %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "USER_INVALIDATED") {
		t.Errorf("denial should carry USER_INVALIDATED: %s", rr.Body.String())
	}
	// The interaction must remain unresolved (still pending).
	got, _ := store.Get(context.Background(), "acme", "sess_1", "alice", "tc_1")
	if got.Phase == interactionstore.PhaseApproved {
		t.Errorf("a denied resolution must not approve the interaction: phase=%q", got.Phase)
	}
}

// spec: §11.4 — an active user resolves interactions normally; the gate
// only blocks invalidated users. F-11.4.2.
func TestElicitationRespondAllowedForActiveUser_spec_11_4(t *testing.T) {
	us := userstore.NewMemory()
	if err := us.Create(context.Background(), userstore.User{
		Subject: "alice", TenantID: "acme",
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	store := interactionstore.NewMemory()
	_ = store.Put(context.Background(), interactionstore.Interaction{
		ID: "el_1", Kind: interactionstore.KindElicitation,
		SessionID: "sess_1", TenantID: "acme", UserID: "alice",
	})
	srv := sessionserver.New(memstore.New(), sessionserver.Options{DevTenantHeader: true, Interactions: store, Users: us})

	req := asAlice(httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/elicitations/el_1/respond",
		strings.NewReader(`{"response":"yes"}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	asOwner(srv.Handler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("active user respond: %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

// TestInteractionResolutionCallerCheck_spec_12_8 pins the caller check of
// every REST interaction-resolution endpoint against each principal
// state: an invalidated caller answers USER_INVALIDATED, a restricted
// caller answers ERASURE_IN_PROGRESS with userId and jobId, a caller that
// is both answers USER_INVALIDATED, a caller with no registry row is
// admitted, and a read error answers 500. Each refusal names interaction
// resolution and leaves the interaction pending.
// spec: 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18)), 11.4 (User Invalidation)
func TestInteractionResolutionCallerCheck_spec_12_8(t *testing.T) {
	endpoints := []struct {
		path string
		kind interactionstore.Kind
		body string
	}{
		{"/v1/sessions/sess_1/tool-use/ix_1/approve", interactionstore.KindToolUse, ""},
		{"/v1/sessions/sess_1/tool-use/ix_1/deny", interactionstore.KindToolUse, `{}`},
		{"/v1/sessions/sess_1/elicitations/ix_1/respond", interactionstore.KindElicitation, `{"response":"yes"}`},
		{"/v1/sessions/sess_1/elicitations/ix_1/dismiss", interactionstore.KindElicitation, `{}`},
	}
	states := []struct {
		name   string
		user   *userstore.User
		users  func(*userstore.Memory) userstore.Store
		status int
		code   string
	}{
		{name: "restricted", user: &userstore.User{Subject: "alice", TenantID: "acme", ProcessingRestricted: true, ErasureJobID: "job-4"}, status: http.StatusForbidden, code: "ERASURE_IN_PROGRESS"},
		{name: "invalidated", user: &userstore.User{Subject: "alice", TenantID: "acme", Disabled: true}, status: http.StatusForbidden, code: "USER_INVALIDATED"},
		{name: "both", user: &userstore.User{Subject: "alice", TenantID: "acme", Disabled: true, ProcessingRestricted: true}, status: http.StatusForbidden, code: "USER_INVALIDATED"},
		{name: "no registry row", status: http.StatusOK},
		{name: "read error", users: func(m *userstore.Memory) userstore.Store { return erroringUsers{m} }, status: http.StatusInternalServerError, code: "INTERNAL_ERROR"},
	}
	for _, ep := range endpoints {
		for _, st := range states {
			t.Run(ep.path+"/"+st.name, func(t *testing.T) {
				mem := userstore.NewMemory()
				if st.user != nil {
					if err := mem.Create(context.Background(), *st.user); err != nil {
						t.Fatalf("seed user: %v", err)
					}
				}
				var us userstore.Store = mem
				if st.users != nil {
					us = st.users(mem)
				}
				ix := interactionstore.NewMemory()
				_ = ix.Put(context.Background(), interactionstore.Interaction{
					ID: "ix_1", Kind: ep.kind, SessionID: "sess_1", TenantID: "acme", UserID: "alice",
					Phase: interactionstore.PhasePending,
				})
				srv := sessionserver.New(memstore.New(), sessionserver.Options{DevTenantHeader: true, Interactions: ix, Users: us})
				req := asAlice(httptest.NewRequest(http.MethodPost, ep.path, strings.NewReader(ep.body)))
				req.Header.Set("Content-Type", "application/json")
				rr := httptest.NewRecorder()
				asOwner(srv.Handler()).ServeHTTP(rr, req)
				if st.status == http.StatusOK {
					if rr.Code >= 400 {
						t.Fatalf("status = %d, want success; body %s", rr.Code, rr.Body.String())
					}
					return
				}
				if rr.Code != st.status {
					t.Fatalf("status = %d, want %d; body %s", rr.Code, st.status, rr.Body.String())
				}
				code, msg, details := decodeError(t, rr)
				if code != st.code {
					t.Fatalf("code = %s, want %s", code, st.code)
				}
				if st.status == http.StatusForbidden && !strings.Contains(msg, "resolve interactions") {
					t.Errorf("message %q does not name interaction resolution", msg)
				}
				if code == "ERASURE_IN_PROGRESS" && (details["userId"] != "alice" || details["jobId"] != "job-4") {
					t.Errorf("details = %v, want userId and jobId", details)
				}
				got, _ := ix.Get(context.Background(), "acme", "sess_1", "alice", "ix_1")
				if got.Phase != interactionstore.PhasePending {
					t.Errorf("a refused resolution changed the phase to %q", got.Phase)
				}
			})
		}
	}
}
