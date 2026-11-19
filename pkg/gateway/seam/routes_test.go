// SPDX-License-Identifier: MIT

package seam

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestRouteValidateRefusesZeroAndUnknownValues checks that a route with a
// zero or unknown listener or access check, or with no method, pattern, or
// handler, is refused with ErrInvalidRoute, and that every defined
// listener and access check is accepted.
// spec: GWSEAM-HandleRoute
func TestRouteValidateRefusesZeroAndUnknownValues(t *testing.T) {
	ok := Route{
		Listener: ListenerClient, Method: http.MethodPost, Pattern: "/v1/sessions/{id}/upload",
		Access: AccessSessionManage, Handler: http.NotFoundHandler(),
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a complete route was refused: %v", err)
	}
	for _, l := range []Listener{ListenerClient, ListenerPodProxy, ListenerForward} {
		for _, a := range []RouteAccess{AccessTenant, AccessSessionRead, AccessSessionManage, AccessSessionEval, AccessUsageView, AccessHandler} {
			r := ok
			r.Listener, r.Access = l, a
			if err := r.Validate(); err != nil {
				t.Errorf("listener %d with access %d was refused: %v", l, a, err)
			}
		}
	}
	internal := ok
	internal.Listener, internal.Access, internal.Method = ListenerInternal, AccessHandler, MethodAny
	if err := internal.Validate(); err != nil {
		t.Errorf("an internal route that leaves authentication to its handler was refused: %v", err)
	}
	cases := map[string]func(*Route){
		"zero listener":    func(r *Route) { r.Listener = 0 },
		"unknown listener": func(r *Route) { r.Listener = ListenerInternal + 1 },
		"internal route with a pipeline check": func(r *Route) {
			r.Listener, r.Access = ListenerInternal, AccessTenant
		},
		"zero access":    func(r *Route) { r.Access = 0 },
		"unknown access": func(r *Route) { r.Access = AccessHandler + 1 },
		"no method":      func(r *Route) { r.Method = "" },
		"no pattern":     func(r *Route) { r.Pattern = "" },
		"no handler":     func(r *Route) { r.Handler = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := ok
			mutate(&r)
			if err := r.Validate(); !errors.Is(err, ErrInvalidRoute) {
				t.Errorf("Validate() = %v, want ErrInvalidRoute", err)
			}
		})
	}
}

// TestRouteMuxPatternJoinsTheMethodUnlessAnyMethod checks that a route
// mounts under its method and pattern, and under its pattern alone when it
// is served for any method.
// spec: GWSEAM-HandleRoute
func TestRouteMuxPatternJoinsTheMethodUnlessAnyMethod(t *testing.T) {
	if got := (Route{Method: http.MethodGet, Pattern: "/healthz"}).MuxPattern(); got != "GET /healthz" {
		t.Errorf("MuxPattern = %q, want %q", got, "GET /healthz")
	}
	if got := (Route{Method: MethodAny, Pattern: "/playground/"}).MuxPattern(); got != "/playground/" {
		t.Errorf("MuxPattern = %q, want %q", got, "/playground/")
	}
}

// TestRefusalIsRecognizedThroughWrapping checks that a refusal carries its
// client API code through wrapping and is told apart from a transient
// failure.
// spec: GWSEAM-CreateSession, GWSEAM-ForwardToolCall
func TestRefusalIsRecognizedThroughWrapping(t *testing.T) {
	refusal := &Refusal{Code: "UNAUTHORIZED", Message: "token expired", Details: map[string]any{"reason": "expired"}}
	got, ok := IsRefusal(fmt.Errorf("forward tool call: %w", refusal))
	if !ok || got.Code != "UNAUTHORIZED" || got.Details["reason"] != "expired" {
		t.Fatalf("IsRefusal = %+v, %v", got, ok)
	}
	if !strings.Contains(refusal.Error(), "UNAUTHORIZED") || !strings.Contains(refusal.Error(), "token expired") {
		t.Errorf("message %q does not carry the code and message", refusal.Error())
	}
	if _, ok := IsRefusal(errors.New("dial holder: connection refused")); ok {
		t.Error("a transient error was taken for a refusal")
	}
	if _, ok := IsRefusal(nil); ok {
		t.Error("nil was taken for a refusal")
	}
}
