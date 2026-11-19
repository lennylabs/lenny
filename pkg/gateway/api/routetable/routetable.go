// SPDX-License-Identifier: MIT

// Package routetable is the API front's route registration: the table through
// which every gateway part registers its HTTP routes, and the muxes of the
// listeners the API front serves.
//
// A route whose handler authenticates the request itself is mounted on its
// listener's mux when it registers. A route whose access check reads the
// tenant or the session is held for the request pipeline, which reads it
// through ListenerRoutes and mounts it behind that check on the session
// paths of its listener. The client and internal listeners share one mux,
// served on one port behind one request pipeline; the forward listener has
// its own.
package routetable

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// Table is the API front's route table. It serves seam.Routes to every part
// and seam.RouteTable to the request pipeline. The zero value is not ready;
// use New.
type Table struct {
	mu sync.Mutex
	// muxes holds the mux of each listener the API front serves.
	muxes map[seam.Listener]*http.ServeMux
	// mounted holds the mux patterns registered on each mux.
	mounted map[*http.ServeMux]map[string]bool
	// routes holds every registered route, in registration order.
	routes []seam.Route
	// sealed marks each listener whose pipeline routes ListenerRoutes has
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
// route that repeats the method and pattern of a route on a listener sharing
// its mux, and a route for the request pipeline once ListenerRoutes has
// returned the pipeline routes of its listener, so no route is silently
// left unmounted. A route whose handler authenticates the request itself is
// mounted on its listener's mux before HandleRoute returns.
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
	pattern := r.MuxPattern()
	if t.mounted[mux][pattern] {
		return fmt.Errorf("%w: %s is already registered on a listener sharing the mux of listener %d", seam.ErrInvalidRoute, pattern, r.Listener)
	}
	if r.Access != seam.AccessHandler && t.sealed[r.Listener] {
		return fmt.Errorf("%w: %s registered after the request pipeline mounted the routes of listener %d", seam.ErrInvalidRoute, pattern, r.Listener)
	}
	if r.Access == seam.AccessHandler {
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

// ListenerRoutes returns, in registration order, the routes registered on
// listener l whose access check the request pipeline applies, and closes the
// registration of such routes on l.
//
// spec: GWSEAM-ListenerRoutes
func (t *Table) ListenerRoutes(l seam.Listener) []seam.Route {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sealed[l] = true
	var out []seam.Route
	for _, r := range t.routes {
		if r.Listener == l && r.Access != seam.AccessHandler {
			out = append(out, r)
		}
	}
	return out
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
