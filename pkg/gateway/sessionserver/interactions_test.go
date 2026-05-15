// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §15.1 tool-use approve/deny + elicitation respond/dismiss.

func newInteractionServer(t *testing.T) (*sessionserver.Server, *interactionstore.Memory) {
	t.Helper()
	store := interactionstore.NewMemory()
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Interactions: store})
	return srv, store
}

func asAlice(req *http.Request) *http.Request {
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "alice", TenantID: "acme",
		Roles: []pkgauth.Role{pkgauth.RoleUser},
	})
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	return req.WithContext(ctx)
}

func TestToolUseApprove(t *testing.T) {
	srv, store := newInteractionServer(t)
	_ = store.Put(context.Background(), interactionstore.Interaction{
		ID: "tc_1", Kind: interactionstore.KindToolUse,
		SessionID: "sess_1", TenantID: "acme", UserID: "alice",
	})
	req := asAlice(httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/tool-use/tc_1/approve", nil))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("approve: %d, body=%s", rr.Code, rr.Body.String())
	}
	got, _ := store.Get(context.Background(), "acme", "sess_1", "alice", "tc_1")
	if got.Phase != interactionstore.PhaseApproved {
		t.Errorf("phase: %q", got.Phase)
	}
}

func TestToolUseDenyWithReason(t *testing.T) {
	srv, store := newInteractionServer(t)
	_ = store.Put(context.Background(), interactionstore.Interaction{
		ID: "tc_1", Kind: interactionstore.KindToolUse,
		SessionID: "sess_1", TenantID: "acme", UserID: "alice",
	})
	req := asAlice(httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/tool-use/tc_1/deny",
		strings.NewReader(`{"reason":"unsafe"}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("deny: %d", rr.Code)
	}
	got, _ := store.Get(context.Background(), "acme", "sess_1", "alice", "tc_1")
	if got.Phase != interactionstore.PhaseDenied || got.Reason != "unsafe" {
		t.Errorf("denied: %+v", got)
	}
}

func TestElicitationRespond(t *testing.T) {
	srv, store := newInteractionServer(t)
	_ = store.Put(context.Background(), interactionstore.Interaction{
		ID: "el_1", Kind: interactionstore.KindElicitation,
		SessionID: "sess_1", TenantID: "acme", UserID: "alice",
	})
	req := asAlice(httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/elicitations/el_1/respond",
		strings.NewReader(`{"response":"yes"}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("respond: %d, body=%s", rr.Code, rr.Body.String())
	}
	got, _ := store.Get(context.Background(), "acme", "sess_1", "alice", "el_1")
	if got.Phase != interactionstore.PhaseResponded || got.Response != "yes" {
		t.Errorf("responded: %+v", got)
	}
}

func TestElicitationDismiss(t *testing.T) {
	srv, store := newInteractionServer(t)
	_ = store.Put(context.Background(), interactionstore.Interaction{
		ID: "el_1", Kind: interactionstore.KindElicitation,
		SessionID: "sess_1", TenantID: "acme", UserID: "alice",
	})
	req := asAlice(httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/elicitations/el_1/dismiss", nil))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("dismiss: %d", rr.Code)
	}
	got, _ := store.Get(context.Background(), "acme", "sess_1", "alice", "el_1")
	if got.Phase != interactionstore.PhaseDismissed {
		t.Errorf("dismissed: %q", got.Phase)
	}
}

func TestToolUseApproveUnknownReturns404(t *testing.T) {
	srv, _ := newInteractionServer(t)
	req := asAlice(httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/tool-use/tc_missing/approve", nil))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown tool call: %d, want 404", rr.Code)
	}
}

func TestElicitationRespondWrongUserReturns404(t *testing.T) {
	srv, store := newInteractionServer(t)
	// Interaction is directed at alice; bob must not resolve it.
	_ = store.Put(context.Background(), interactionstore.Interaction{
		ID: "el_1", Kind: interactionstore.KindElicitation,
		SessionID: "sess_1", TenantID: "acme", UserID: "alice",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/elicitations/el_1/respond",
		strings.NewReader(`{"response":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{Subject: "bob", TenantID: "acme"})
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("wrong-user respond: %d, want 404", rr.Code)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if env.Error.Code != "ELICITATION_NOT_FOUND" {
		t.Errorf("error code: %q, want ELICITATION_NOT_FOUND", env.Error.Code)
	}
}

func TestDoubleResolveReturns409(t *testing.T) {
	srv, store := newInteractionServer(t)
	_ = store.Put(context.Background(), interactionstore.Interaction{
		ID: "tc_1", Kind: interactionstore.KindToolUse,
		SessionID: "sess_1", TenantID: "acme", UserID: "alice",
	})
	for i, want := range []int{http.StatusOK, http.StatusConflict} {
		req := asAlice(httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/tool-use/tc_1/approve", nil))
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != want {
			t.Errorf("resolve #%d: got %d, want %d", i, rr.Code, want)
		}
	}
}

func TestKindMismatchReturns404(t *testing.T) {
	srv, store := newInteractionServer(t)
	// An elicitation resolved via the tool-use endpoint is not found.
	_ = store.Put(context.Background(), interactionstore.Interaction{
		ID: "el_1", Kind: interactionstore.KindElicitation,
		SessionID: "sess_1", TenantID: "acme", UserID: "alice",
	})
	req := asAlice(httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/tool-use/el_1/approve", nil))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("kind mismatch: %d, want 404", rr.Code)
	}
}

func TestInteractionsUnavailableWhenUnwired(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{})
	req := asAlice(httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/tool-use/tc_1/approve", nil))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("unwired: %d, want 503", rr.Code)
	}
}
