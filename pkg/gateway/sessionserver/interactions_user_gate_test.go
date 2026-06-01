// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
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
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Interactions: store, Users: us})

	req := asAlice(httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/tool-use/tc_1/approve", nil))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
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
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Interactions: store, Users: us})

	req := asAlice(httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/elicitations/el_1/respond",
		strings.NewReader(`{"response":"yes"}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("active user respond: %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}
