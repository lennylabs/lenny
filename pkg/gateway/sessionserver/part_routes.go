// SPDX-License-Identifier: MIT

package sessionserver

import (
	"fmt"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// HandleRoute registers a route of another gateway part, such as the stream
// proxy, on the session server's mux, behind the access check the route
// names. The session server serves the route registration interface while
// it holds the request pipeline that the API front takes over, and the
// handler it builds is the one the client and forward listeners serve.
//
// It refuses a route that Route.Validate refuses, a route on a listener
// other than the client listener, a route whose access check leaves
// authentication to its handler, because every route of this mux runs
// behind its tenant check, a route that repeats a registered method and
// pattern, and any route once Handler has built the mux, so no route is
// silently left unmounted.
//
// spec: GWSEAM-HandleRoute
func (s *Server) HandleRoute(r seam.Route) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if r.Listener != seam.ListenerClient {
		return fmt.Errorf("%w: %s %s: the session server serves only the client listener", seam.ErrInvalidRoute, r.Method, r.Pattern)
	}
	if r.Access == seam.AccessHandler {
		return fmt.Errorf("%w: %s %s: every session server route runs behind its tenant check", seam.ErrInvalidRoute, r.Method, r.Pattern)
	}
	s.routesMu.Lock()
	defer s.routesMu.Unlock()
	if s.routesServed {
		return fmt.Errorf("%w: %s %s: registered after the session server began serving", seam.ErrInvalidRoute, r.Method, r.Pattern)
	}
	for _, have := range s.partRoutes {
		if have.Method == r.Method && have.Pattern == r.Pattern {
			return fmt.Errorf("%w: %s %s is already registered", seam.ErrInvalidRoute, r.Method, r.Pattern)
		}
	}
	s.partRoutes = append(s.partRoutes, r)
	return nil
}

// mountPartRoutes mounts the routes HandleRoute registered on mux, each
// behind its access check, and closes registration.
func (s *Server) mountPartRoutes(mux *http.ServeMux) {
	s.routesMu.Lock()
	s.routesServed = true
	routes := append([]seam.Route(nil), s.partRoutes...)
	s.routesMu.Unlock()
	for _, r := range routes {
		mux.HandleFunc(r.Method+" "+r.Pattern, s.routeAccess(r.Access, r.Handler.ServeHTTP))
	}
}

// routeAccess wraps a part's handler in the session route wrapper of its
// access check: a resolved tenant needs no wrapper, because Handler runs
// the whole mux behind its tenant check; read and manage access take the
// session routes' permission and ownership wrappers; the evaluation and
// usage checks take the permission gate of their permission.
//
// spec: §10.2 (Session ownership) rules 5, 6, and 7.
func (s *Server) routeAccess(access seam.RouteAccess, next http.HandlerFunc) http.HandlerFunc {
	switch access {
	case seam.AccessSessionRead:
		return s.readSessionRoute(next)
	case seam.AccessSessionManage:
		return s.manageSessionRoute(next)
	case seam.AccessSessionEval:
		return s.requireSessionPermission(auth.PermSessionEvalWrite, next)
	case seam.AccessUsageView:
		return s.requireSessionPermission(auth.PermViewUsage, next)
	default:
		return next
	}
}
