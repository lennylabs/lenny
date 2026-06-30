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
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
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

// captureInteractionAudit records every §7.2 / §11.7 interaction
// resolution audit event so tests can assert emission. F-7.2.8.
type captureInteractionAudit struct {
	events []sessionserver.InteractionResolutionEvent
}

func (c *captureInteractionAudit) EmitInteractionResolution(_ context.Context, ev sessionserver.InteractionResolutionEvent) {
	c.events = append(c.events, ev)
}

// TestInteractionResolutionAudit_spec_7_2_8 asserts that each §15.1
// resolution endpoint (tool-use approve/deny, elicitation
// respond/dismiss) writes one §11.7 audit row carrying the resolving
// user, the interaction id, the new phase, and the optional dismissal
// reason. The audit row is the post-incident link from a state change
// back to the user who authorized it; §11.7 mandates a row per
// state-changing user decision.
// spec: §7.2 table lines 124-127; §11.7; §16.7. F-7.2.8.
func TestInteractionResolutionAudit_spec_7_2_8(t *testing.T) {
	cases := []struct {
		name           string
		kind           interactionstore.Kind
		path           string
		body           string
		wantEventType  string
		wantPhase      interactionstore.Phase
		wantReason     string
		wantHasReason  bool
		interactionID  string
		urlSegment     string // "tool-use" or "elicitations"
		resolverAction string // approve/deny/respond/dismiss
	}{
		{
			name: "tool_use_approved", kind: interactionstore.KindToolUse,
			path:          "/v1/sessions/sess_a/tool-use/tc_1/approve",
			body:          "",
			wantEventType: "tool_use.approved",
			wantPhase:     interactionstore.PhaseApproved,
			interactionID: "tc_1",
		},
		{
			name: "tool_use_denied", kind: interactionstore.KindToolUse,
			path:          "/v1/sessions/sess_a/tool-use/tc_1/deny",
			body:          `{"reason":"unsafe"}`,
			wantEventType: "tool_use.denied",
			wantPhase:     interactionstore.PhaseDenied,
			wantReason:    "unsafe", wantHasReason: true,
			interactionID: "tc_1",
		},
		{
			name: "elicitation_responded", kind: interactionstore.KindElicitation,
			path:          "/v1/sessions/sess_a/elicitations/el_1/respond",
			body:          `{"response":"yes"}`,
			wantEventType: "elicitation.responded",
			wantPhase:     interactionstore.PhaseResponded,
			interactionID: "el_1",
		},
		{
			name: "elicitation_dismissed", kind: interactionstore.KindElicitation,
			path:          "/v1/sessions/sess_a/elicitations/el_1/dismiss",
			body:          "",
			wantEventType: "elicitation.dismissed",
			wantPhase:     interactionstore.PhaseDismissed,
			interactionID: "el_1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			interStore := interactionstore.NewMemory()
			sink := &captureInteractionAudit{}
			srv := sessionserver.New(memstore.New(), sessionserver.Options{
				Interactions:         interStore,
				InteractionAuditSink: sink,
			})
			if err := interStore.Put(context.Background(), interactionstore.Interaction{
				ID: c.interactionID, Kind: c.kind,
				SessionID: "sess_a", TenantID: "acme", UserID: "alice",
			}); err != nil {
				t.Fatalf("seed interaction: %v", err)
			}
			var body *strings.Reader
			if c.body != "" {
				body = strings.NewReader(c.body)
			} else {
				body = strings.NewReader("")
			}
			req := asAlice(httptest.NewRequest(http.MethodPost, c.path, body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
			}
			if len(sink.events) != 1 {
				t.Fatalf("audit emission count = %d, want 1; events=%+v", len(sink.events), sink.events)
			}
			ev := sink.events[0]
			if ev.EventType != c.wantEventType {
				t.Errorf("event_type = %q, want %q", ev.EventType, c.wantEventType)
			}
			if ev.Phase != string(c.wantPhase) {
				t.Errorf("phase = %q, want %q", ev.Phase, c.wantPhase)
			}
			if ev.SessionID != "sess_a" {
				t.Errorf("session_id = %q, want sess_a", ev.SessionID)
			}
			if ev.UserID != "alice" {
				t.Errorf("user_sub = %q, want alice (the §15.1 resolving user)", ev.UserID)
			}
			if ev.InteractionID != c.interactionID {
				t.Errorf("interaction_id = %q, want %q", ev.InteractionID, c.interactionID)
			}
			if ev.TenantID != "acme" {
				t.Errorf("tenant_id = %q, want acme", ev.TenantID)
			}
			if c.wantHasReason && ev.Reason != c.wantReason {
				t.Errorf("reason = %q, want %q", ev.Reason, c.wantReason)
			}
			if !c.wantHasReason && ev.Reason != "" {
				t.Errorf("reason = %q, want empty for %s", ev.Reason, c.name)
			}
			if ev.At.IsZero() {
				t.Error("event timestamp is zero; the §11.7 audit row needs a resolved-at instant")
			}
		})
	}
}

// TestInteractionResolutionAuditNotEmittedOnFailure asserts that a
// failed resolution (not-found, wrong-user, kind mismatch) writes no
// audit row — a 404 reply must not leak the existence of a foreign
// interaction via an audit-side channel. F-7.2.8.
func TestInteractionResolutionAuditNotEmittedOnFailure(t *testing.T) {
	interStore := interactionstore.NewMemory()
	sink := &captureInteractionAudit{}
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		Interactions:         interStore,
		InteractionAuditSink: sink,
	})
	// No interaction seeded.
	req := asAlice(httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_a/tool-use/tc_x/approve", nil))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	if len(sink.events) != 0 {
		t.Errorf("audit emission count = %d on a failed resolution, want 0", len(sink.events))
	}
}

// TestInteractionResolutionAuditNilSinkIsSafe asserts the resolution
// path still proceeds with no sink wired — audit emission is
// best-effort per the §11.7 sink contract. F-7.2.8.
func TestInteractionResolutionAuditNilSinkIsSafe(t *testing.T) {
	interStore := interactionstore.NewMemory()
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Interactions: interStore})
	if err := interStore.Put(context.Background(), interactionstore.Interaction{
		ID: "tc_1", Kind: interactionstore.KindToolUse,
		SessionID: "sess_a", TenantID: "acme", UserID: "alice",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	req := asAlice(httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_a/tool-use/tc_1/approve", nil))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("nil sink: status = %d, want 200", rr.Code)
	}
}
