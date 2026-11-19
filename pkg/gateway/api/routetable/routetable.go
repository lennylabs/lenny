// SPDX-License-Identifier: MIT

// Package routetable is the API front's route registration: the table through
// which every gateway part registers its HTTP routes, and the muxes of the
// listeners the API front serves.
//
// A route whose handler authenticates the request itself is mounted on its
// listener's mux when it registers, unless its path lies under the session
// paths of its listener. A route whose access check reads the tenant, the
// session, or a permission, and a route under the session paths, is held
// for the request pipeline (pkg/gateway/api/pipeline), which reads it
// through PipelineRoutes and mounts it behind its check on the session
// paths. The client and internal listeners share one mux, served on one
// port behind one request pipeline; the forward listener has its own.
package routetable

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// sessionPaths holds the session paths of each listener: the mux patterns
// under which the request pipeline mounts its handler. A pattern ending in
// a slash covers every path under it.
var sessionPaths = map[seam.Listener][]string{
	seam.ListenerClient:  {"/v1/sessions", "/v1/sessions/", "/v1/blobs/", "/v1/runtimes", "/v1/runtimes/", "/v1/models"},
	seam.ListenerForward: {"/v1/sessions", "/v1/sessions/"},
}

// SessionPaths returns the session paths of listener l, which the request
// pipeline serves, in the order it mounts them.
//
// spec: GWSEAM-HandleRoute
func SessionPaths(l seam.Listener) []string {
	return append([]string(nil), sessionPaths[l]...)
}

// underSessionPaths reports whether path, a route's path pattern, lies
// under a session path of listener l.
func underSessionPaths(l seam.Listener, path string) bool {
	for _, p := range sessionPaths[l] {
		if path == p || (strings.HasSuffix(p, "/") && strings.HasPrefix(path, p)) {
			return true
		}
	}
	return false
}

// held reports whether the request pipeline, rather than the listener's
// mux, mounts r.
func held(r seam.Route) bool {
	return r.Access != seam.AccessHandler || underSessionPaths(r.Listener, r.Pattern)
}

// Table is the API front's route table. It serves seam.Routes to every part
// and the routes it holds to the request pipeline. The zero value is not
// ready; use New.
type Table struct {
	mu sync.Mutex
	// muxes holds the mux of each listener the API front serves.
	muxes map[seam.Listener]*http.ServeMux
	// mounted holds the mux patterns registered on each mux.
	mounted map[*http.ServeMux]map[string]bool
	// routes holds every registered route, in registration order.
	routes []seam.Route
	// sealed marks each listener whose held routes PipelineRoutes has
	// returned.
	sealed map[seam.Listener]bool
}

// New returns an empty table with the muxes of the client, internal, and
// forward listeners.
func New() *Table {
	shared := http.NewServeMux()
	forward := http.NewServeMux()
	return &Table{
		muxes: map[seam.Listener]*http.ServeMux{
			seam.ListenerClient:   shared,
			seam.ListenerInternal: shared,
			seam.ListenerForward:  forward,
		},
		mounted: map[*http.ServeMux]map[string]bool{shared: {}, forward: {}},
		sealed:  map[seam.Listener]bool{},
	}
}

// HandleRoute registers r. It refuses, with seam.ErrInvalidRoute, a route
// that seam.Route.Validate refuses, a route of the pod proxy listener, a
// pattern that does not begin with a slash, such as one that names a host,
// a route of the internal listener under a session path of the client
// listener, which shares its mux and would take precedence over the
// request pipeline there, a route whose mux pattern its mux already holds, and a route the request
// pipeline mounts once PipelineRoutes has sealed its listener, so no route
// is silently left unmounted. A route whose handler authenticates the
// request itself and whose path lies outside the session paths is mounted
// on its listener's mux before HandleRoute returns.
//
// spec: GWSEAM-HandleRoute
func (t *Table) HandleRoute(r seam.Route) error {
	if err := r.Validate(); err != nil {
		return err
	}
	mux, ok := t.muxes[r.Listener]
	if !ok {
		return fmt.Errorf("%w: %s %s: the API front serves no route of the pod proxy listener", seam.ErrInvalidRoute, r.Method, r.Pattern)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !strings.HasPrefix(r.Pattern, "/") {
		return fmt.Errorf("%w: %s %s: a pattern begins with a slash and names no host", seam.ErrInvalidRoute, r.Method, r.Pattern)
	}
	if r.Listener == seam.ListenerInternal && underSessionPaths(seam.ListenerClient, r.Pattern) {
		return fmt.Errorf("%w: %s %s: the internal listener shares the client listener's mux, whose session paths the request pipeline serves", seam.ErrInvalidRoute, r.Method, r.Pattern)
	}
	pattern := r.MuxPattern()
	if t.mounted[mux][pattern] {
		return fmt.Errorf("%w: %s is already registered on a listener sharing the mux of listener %d", seam.ErrInvalidRoute, pattern, r.Listener)
	}
	if held(r) && t.sealed[r.Listener] {
		return fmt.Errorf("%w: %s registered after the request pipeline mounted the routes of listener %d", seam.ErrInvalidRoute, pattern, r.Listener)
	}
	if !held(r) {
		if err := mount(mux, pattern, r.Handler); err != nil {
			return fmt.Errorf("%w: %s: %v", seam.ErrInvalidRoute, pattern, err)
		}
	}
	t.mounted[mux][pattern] = true
	t.routes = append(t.routes, r)
	return nil
}

// mount mounts h on mux, turning the panic net/http.ServeMux raises for a
// malformed or conflicting pattern into an error.
func mount(mux *http.ServeMux, pattern string, h http.Handler) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
	}()
	mux.Handle(pattern, h)
	return nil
}

// PipelineRoutes returns the routes of listener l that the request pipeline
// mounts, in the order they registered, and seals l against more of them.
//
// spec: GWSEAM-HandleRoute
func (t *Table) PipelineRoutes(l seam.Listener) []seam.Route {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sealed[l] = true
	var out []seam.Route
	for _, r := range t.routes {
		if r.Listener == l && held(r) {
			out = append(out, r)
		}
	}
	return out
}

// MountSessionPaths mounts h, the request pipeline's handler of listener l,
// on l's mux under each session path of l. It refuses a listener the API
// front does not serve and a session path the mux already holds.
//
// spec: GWSEAM-HandleRoute
func (t *Table) MountSessionPaths(l seam.Listener, h http.Handler) error {
	mux, ok := t.muxes[l]
	if !ok {
		return fmt.Errorf("%w: the API front serves no listener %d", seam.ErrInvalidRoute, l)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, p := range sessionPaths[l] {
		if t.mounted[mux][p] {
			return fmt.Errorf("%w: session path %s is already registered on listener %d", seam.ErrInvalidRoute, p, l)
		}
		if err := mount(mux, p, h); err != nil {
			return fmt.Errorf("%w: %s: %v", seam.ErrInvalidRoute, p, err)
		}
		t.mounted[mux][p] = true
	}
	return nil
}

// Handler returns the mux of listener l: the shared mux of the client and
// internal listeners, the forward listener's mux, or nil for a listener the
// API front does not serve. The caller wraps the mux in its listener's
// request pipeline.
func (t *Table) Handler(l seam.Listener) *http.ServeMux {
	return t.muxes[l]
}

// Routes returns every registered route, in registration order.
func (t *Table) Routes() []seam.Route {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]seam.Route(nil), t.routes...)
}
