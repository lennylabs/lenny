// SPDX-License-Identifier: MIT

package seam

import (
	"errors"
	"fmt"
	"net/http"
)

// Routes is served by the API front to every part: the registration of an
// HTTP route behind the API front's request pipeline. Each part registers
// its own routes, so the API front imports no part.
type Routes interface {
	// HandleRoute registers one route. It refuses a route that
	// Route.Validate refuses and registers nothing for it.
	HandleRoute(r Route) error
}

// RouteProvider is served by each part that has HTTP routes.
type RouteProvider interface {
	// RegisterRoutes registers every route of the part through routes.
	RegisterRoutes(routes Routes) error
}

// Route is one HTTP route a part registers.
type Route struct {
	// Listener is the listener that serves the route.
	Listener Listener
	// Method is the HTTP method.
	Method string
	// Pattern is the path pattern in the form net/http.ServeMux reads,
	// such as /v1/sessions/{id}/upload.
	Pattern string
	// Access is the check the API front applies before the handler runs.
	Access RouteAccess
	// Handler serves the route.
	Handler http.Handler
}

// Listener is the listener a route is served on.
type Listener int

// The listeners.
const (
	// ListenerClient is the client-facing listener of the client and
	// admin APIs.
	ListenerClient Listener = iota + 1
	// ListenerPodProxy is the pod-facing listener of the metering proxy.
	// Only the metering proxy's own server serves it, and the API front
	// never mounts its routes on the client listener.
	ListenerPodProxy
	// ListenerForward is the listener on which another replica forwards a
	// request to the holder replica of a session.
	ListenerForward
)

// RouteAccess is the access check a route requires.
type RouteAccess int

// The route access checks.
const (
	// AccessTenant requires a resolved tenant.
	AccessTenant RouteAccess = iota + 1
	// AccessSessionRead requires read access to the session that the path
	// value "id" names.
	AccessSessionRead
	// AccessSessionManage requires manage access to the session that the
	// path value "id" names.
	AccessSessionManage
	// AccessSessionEval requires the session:eval:write permission; the
	// handler applies its own rule for the caller of the session.
	AccessSessionEval
	// AccessUsageView requires a resolved tenant and the view_usage
	// permission.
	AccessUsageView
	// AccessHandler leaves authentication to the handler, as for a route
	// that authenticates a token the request carries itself.
	AccessHandler
)

// ErrInvalidRoute reports a route that HandleRoute refuses.
var ErrInvalidRoute = errors.New("seam: invalid route")

// Validate reports a route that names no known listener, no known access
// check, no method, no pattern, or no handler. A zero listener or access
// value is refused, so a route never falls back to a default check.
func (r Route) Validate() error {
	switch {
	case r.Listener < ListenerClient || r.Listener > ListenerForward:
		return fmt.Errorf("%w: %s %s names unknown listener %d", ErrInvalidRoute, r.Method, r.Pattern, r.Listener)
	case r.Access < AccessTenant || r.Access > AccessHandler:
		return fmt.Errorf("%w: %s %s names unknown access check %d", ErrInvalidRoute, r.Method, r.Pattern, r.Access)
	case r.Method == "" || r.Pattern == "":
		return fmt.Errorf("%w: a route needs a method and a pattern", ErrInvalidRoute)
	case r.Handler == nil:
		return fmt.Errorf("%w: %s %s has no handler", ErrInvalidRoute, r.Method, r.Pattern)
	}
	return nil
}
