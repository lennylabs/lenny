// SPDX-License-Identifier: MIT

// Package apifront gives a test's session server the API front, as
// cmd/lenny-gateway does: the route table through which the server and
// every part a test attaches register their routes, and the request
// pipeline that mounts those routes behind their access checks.
//
// The package is test wiring that decides no platform behavior, so it
// carries no specification annotation.
package apifront

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/lennylabs/lenny/pkg/gateway/api/pipeline"
	"github.com/lennylabs/lenny/pkg/gateway/api/routetable"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// Core is what the API front reads of a test's session server, which
// *sessionserver.Server satisfies.
type Core interface {
	seam.RouteProvider
	SetServiceHandler(h http.Handler)
	RequestChecks() (seam.Sessions, seam.Permissions)
	DevTenantHeader() bool
}

// front is the API front of one session server.
type front struct {
	table   *routetable.Table
	once    sync.Once
	pipe    *pipeline.Pipeline
	mounted sync.Once
}

// fronts holds the API front of each session server a test used.
var fronts sync.Map

// frontOf returns srv's API front, creating it on the first call. Creating
// it hands srv a service handler that builds the request pipeline on its
// first in-process call, so a test whose session server serves an
// overlapping operation in-process, such as a platform tool, reaches the
// pipeline without building the handlers first.
func frontOf(srv Core) *front {
	v, loaded := fronts.LoadOrStore(srv, &front{table: routetable.New()})
	if !loaded {
		srv.SetServiceHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Pipeline(srv).Handler(seam.ListenerClient).ServeHTTP(w, r)
		}))
	}
	return v.(*front)
}

// Routes returns the route table of srv's API front, on which a part a test
// attaches registers its routes. Call it before srv's handlers are built.
func Routes(srv Core) *routetable.Table {
	return frontOf(srv).table
}

// Pipeline returns the request pipeline of srv's API front. The first call
// registers srv's own routes and builds the pipeline over the routes
// registered so far; srv's in-process service calls run through the client
// listener's handler from then on. It panics when the routes or the pipeline are
// refused, so a wiring error stops the test.
func Pipeline(srv Core) *pipeline.Pipeline {
	f := frontOf(srv)
	f.once.Do(func() {
		if err := srv.RegisterRoutes(f.table); err != nil {
			panic(fmt.Sprintf("apifront: register the session routes: %v", err))
		}
		sessions, permissions := srv.RequestChecks()
		p, err := pipeline.New(pipeline.Config{
			Table: f.table, Sessions: sessions, Permissions: permissions,
			DevTenantHeader: srv.DevTenantHeader(),
		})
		if err != nil {
			panic(fmt.Sprintf("apifront: build the request pipeline: %v", err))
		}
		f.pipe = p
	})
	return f.pipe
}

// Handler returns the handler of the client listener's session paths that
// the API front's request pipeline builds for srv.
func Handler(srv Core) http.Handler {
	return Pipeline(srv).Handler(seam.ListenerClient)
}

// ForwardHandler returns the handler of the forward listener's session
// paths that the API front's request pipeline builds for srv.
func ForwardHandler(srv Core) http.Handler {
	return Pipeline(srv).Handler(seam.ListenerForward)
}

// Listener returns the mux of listener l of srv's API front, with the
// request pipeline mounted on its session paths, as the composition root
// serves it: the session paths and every route registered on l that
// leaves authentication to its handler, such as the internal runtime
// metadata route. Register every part's routes before the first call.
func Listener(srv Core, l seam.Listener) http.Handler {
	f := frontOf(srv)
	p := Pipeline(srv)
	f.mounted.Do(func() {
		if err := p.Mount(); err != nil {
			panic(fmt.Sprintf("apifront: mount the request pipeline: %v", err))
		}
	})
	return f.table.Handler(l)
}
