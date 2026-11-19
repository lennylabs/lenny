// SPDX-License-Identifier: MIT

package sessionserver

import (
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// SetRouteTable sets the API front's route table, from which the session
// handlers read the routes other parts registered with an access check the
// request pipeline applies. The session server holds that pipeline until
// the API front holds it. Call it before Handler, ForwardHandler, or
// RegisterRoutes.
func (s *Server) SetRouteTable(t seam.RouteTable) {
	s.routeTable = t
}

// RouteTable returns the route table SetRouteTable set, or nil.
func (s *Server) RouteTable() seam.RouteTable {
	return s.routeTable
}

// RegisterRoutes registers the session handlers on the API front: the
// session, blob, runtime, and model paths on the client listener, the
// internal runtime metadata path on the internal listener, and the session
// paths on the forward listener. Each takes no check by the API front,
// because the handler applies the tenant check itself and mounts each part
// route behind the check it names. Each path is a prefix served for any
// method, which the handler's own mux dispatches. It builds the handlers,
// so every part's routes register before it runs.
//
// spec: GWSEAM-RegisterRoutes
func (s *Server) RegisterRoutes(routes seam.Routes) error {
	client, forward := s.Handler(), s.ForwardHandler()
	for _, r := range []seam.Route{
		{Listener: seam.ListenerClient, Pattern: "/v1/sessions", Handler: client},
		{Listener: seam.ListenerClient, Pattern: "/v1/sessions/", Handler: client},
		{Listener: seam.ListenerClient, Pattern: "/v1/blobs/", Handler: client},
		// §9.1 / §15.1 runtime discovery and the model list. The session
		// handler serves GET /v1/runtimes, /v1/runtimes/{name}/meta/{key},
		// and /v1/models, each identity-filtered by §10.6 environment
		// access, and the §5.1 internal metadata fetch.
		{Listener: seam.ListenerClient, Pattern: "/v1/runtimes", Handler: client},
		{Listener: seam.ListenerClient, Pattern: "/v1/runtimes/", Handler: client},
		{Listener: seam.ListenerClient, Pattern: "/v1/models", Handler: client},
		{Listener: seam.ListenerInternal, Pattern: "/internal/runtimes/", Handler: client},
		{Listener: seam.ListenerForward, Pattern: "/v1/sessions", Handler: forward},
		{Listener: seam.ListenerForward, Pattern: "/v1/sessions/", Handler: forward},
	} {
		r.Method, r.Access = seam.MethodAny, seam.AccessHandler
		if err := routes.HandleRoute(r); err != nil {
			return err
		}
	}
	return nil
}

// mountPartRoutes mounts on mux, each behind its access check, the routes
// other parts registered on listener l for the request pipeline, and closes
// their registration on l.
func (s *Server) mountPartRoutes(mux *recordingMux, l seam.Listener) {
	if s.routeTable == nil {
		return
	}
	for _, r := range s.routeTable.ListenerRoutes(l) {
		mux.HandleFunc(r.MuxPattern(), s.routeAccess(r.Access, r.Handler.ServeHTTP))
		mux.patterns[len(mux.patterns)-1] += " [" + accessName(r.Access) + "]"
	}
}

// accessName names a route access check in the session handler's route
// patterns.
func accessName(a seam.RouteAccess) string {
	switch a {
	case seam.AccessTenant:
		return "tenant"
	case seam.AccessSessionRead:
		return "session-read"
	case seam.AccessSessionManage:
		return "session-manage"
	case seam.AccessSessionEval:
		return "session-eval"
	case seam.AccessUsageView:
		return "usage-view"
	case seam.AccessHandler:
		return "handler"
	}
	return "unknown"
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

// sessionStateOf returns the client API state of a seam session record's
// state. The two types spell the same states.
func sessionStateOf(st state.State) session.State {
	return session.State(st)
}
