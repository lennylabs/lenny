// SPDX-License-Identifier: MIT

package seam

import "net/http"

// Routes is served by the API front to every part: the registration of an
// HTTP route behind the API front's request pipeline. Each part registers
// its own routes, so the API front imports no part.
type Routes interface {
	// HandleRoute registers one route.
	HandleRoute(r Route)
}

// RouteProvider is served by each part that has HTTP routes.
type RouteProvider interface {
	// RegisterRoutes registers every route of the part through routes.
	RegisterRoutes(routes Routes)
}

// Route is one HTTP route a part registers.
type Route struct {
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
)
