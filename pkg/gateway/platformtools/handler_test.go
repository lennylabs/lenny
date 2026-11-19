// SPDX-License-Identifier: MIT

package platformtools_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/api/pipeline"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/apifront"
)

// recordedRoutes is a seam.Routes that records each route it is handed.
type recordedRoutes struct{ routes []seam.Route }

func (r *recordedRoutes) HandleRoute(route seam.Route) error {
	if err := route.Validate(); err != nil {
		return err
	}
	r.routes = append(r.routes, route)
	return nil
}

// activeCallers is a seam.ActiveCallers that answers err.
type activeCallers struct{ err error }

func (a activeCallers) AdmitActiveCaller(context.Context, string) error { return a.err }

// TestNewRefusesOptionsWithoutARequiredOperation checks that New refuses
// options that lack the session lookup, the request tenant, the caller
// identity, the user lifecycle check, or the session store.
//
// spec: GWSEAM-RegisterRoutes
func TestNewRefusesOptionsWithoutARequiredOperation(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{})
	core := srv.ToolsSeam()
	full := platformtools.Options{
		Sessions: core, Identity: core, Callers: pipeline.Callers{}, ActiveCallers: core, Store: memstore.New(),
	}
	if _, err := platformtools.New(full); err != nil {
		t.Fatalf("New with every required operation: %v", err)
	}
	for name, drop := range map[string]func(*platformtools.Options){
		"sessions":       func(o *platformtools.Options) { o.Sessions = nil },
		"identity":       func(o *platformtools.Options) { o.Identity = nil },
		"callers":        func(o *platformtools.Options) { o.Callers = nil },
		"active callers": func(o *platformtools.Options) { o.ActiveCallers = nil },
		"store":          func(o *platformtools.Options) { o.Store = nil },
	} {
		opts := full
		drop(&opts)
		if _, err := platformtools.New(opts); !errors.Is(err, platformtools.ErrIncomplete) {
			t.Errorf("New without %s = %v, want ErrIncomplete", name, err)
		}
	}
}

// TestRegisterRoutesDeclaresEachRouteWithItsAccessCheck checks that the
// platform tools seam registers its client routes on the client listener
// with the access check each had on the session server's mux.
//
// spec: GWSEAM-RegisterRoutes
func TestRegisterRoutesDeclaresEachRouteWithItsAccessCheck(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{})
	core := srv.ToolsSeam()
	h, err := platformtools.New(platformtools.Options{
		Sessions: core, Identity: core, Callers: pipeline.Callers{}, ActiveCallers: core, Store: memstore.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recordedRoutes{}
	if err := h.RegisterRoutes(rec); err != nil {
		t.Fatal(err)
	}
	want := map[string]seam.RouteAccess{
		"POST /v1/sessions/{id}/memory":                               seam.AccessSessionManage,
		"GET /v1/sessions/{id}/memory":                                seam.AccessSessionRead,
		"DELETE /v1/sessions/{id}/memory/{memoryId}":                  seam.AccessSessionManage,
		"GET /v1/sessions/{id}/tree":                                  seam.AccessSessionRead,
		"POST /v1/sessions/{id}/tool-use/{toolCallId}/approve":        seam.AccessSessionManage,
		"POST /v1/sessions/{id}/tool-use/{toolCallId}/deny":           seam.AccessSessionManage,
		"POST /v1/sessions/{id}/elicitations/{elicitationId}/respond": seam.AccessSessionManage,
		"POST /v1/sessions/{id}/elicitations/{elicitationId}/dismiss": seam.AccessSessionManage,
	}
	if len(rec.routes) != len(want) {
		t.Fatalf("registered %d routes, want %d", len(rec.routes), len(want))
	}
	for _, r := range rec.routes {
		key := r.Method + " " + r.Pattern
		access, ok := want[key]
		if !ok {
			t.Errorf("unexpected route %s", key)
			continue
		}
		if r.Access != access || r.Listener != seam.ListenerClient {
			t.Errorf("%s: listener %d access %d, want client listener and access %d", key, r.Listener, r.Access, access)
		}
	}
}

// TestTheUserLifecycleCheckAnswersEachOutcome checks how an interaction
// resolution answers the session core's user lifecycle check: a refusal
// is a 403 with the refusal's code, message, and details, and a failure to
// read the user is a 500 INTERNAL_ERROR with the core's message, and in
// neither case is the interaction resolved.
//
// spec: GWSEAM-AdmitActiveCaller
func TestTheUserLifecycleCheckAnswersEachOutcome(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		status   int
		code     string
		message  string
		hasJobID bool
	}{
		{
			name:     "refusal",
			err:      &seam.Refusal{Code: "ERASURE_IN_PROGRESS", Message: "pending erasure", Details: map[string]any{"jobId": "job-1"}},
			status:   http.StatusForbidden,
			code:     "ERASURE_IN_PROGRESS",
			message:  "pending erasure",
			hasJobID: true,
		},
		{
			name:    "read failure",
			err:     errors.New("user-invalidation check failed: user registry unavailable"),
			status:  http.StatusInternalServerError,
			code:    "INTERNAL_ERROR",
			message: "user-invalidation check failed: user registry unavailable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := interactionstore.NewMemory()
			_ = store.Put(context.Background(), interactionstore.Interaction{
				ID: "tc_1", Kind: interactionstore.KindToolUse,
				SessionID: "sess_1", TenantID: "acme", UserID: "alice",
			})
			srv := newServer(t, aliceSessions(t), sessionserver.Options{DevTenantHeader: true},
				platformtools.Options{Interactions: store, ActiveCallers: activeCallers{err: tc.err}})
			req := asAlice(httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/tool-use/tc_1/approve", nil))
			rr := httptest.NewRecorder()
			apifront.Handler(srv).ServeHTTP(rr, req)
			if rr.Code != tc.status {
				t.Fatalf("status %d, want %d; body=%s", rr.Code, tc.status, rr.Body.String())
			}
			code, msg, details := decodeError(t, rr)
			if code != tc.code || msg != tc.message {
				t.Errorf("envelope %s %q, want %s %q", code, msg, tc.code, tc.message)
			}
			if _, ok := details["jobId"]; ok != tc.hasJobID {
				t.Errorf("details %v, want jobId %v", details, tc.hasJobID)
			}
			got, _ := store.Get(context.Background(), "acme", "sess_1", "alice", "tc_1")
			if got.Phase != interactionstore.PhasePending {
				t.Errorf("phase %q after a refused resolution, want pending", got.Phase)
			}
		})
	}
}
