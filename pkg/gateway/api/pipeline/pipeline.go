// SPDX-License-Identifier: MIT

// Package pipeline is the API front's request pipeline of the session
// paths. For each listener that has session paths, it builds one handler
// that mounts every route the route table holds for that listener behind
// the route's access check (the tenant check, the permission gates, and the
// session read and manage checks), and it mounts that handler on the
// listener's session paths.
//
// The pipeline resolves a request's tenant itself, from the authentication
// middleware's principal, the tenant an in-process service call recorded,
// or, under the gateway's --dev-mode, the dev header. It asks the session
// core, which owns the role definitions and the session rows, to decide
// each permission gate and each session check, through the operations of
// the core's PipelineSeam.
package pipeline

import (
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/gateway/api/routetable"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// Config holds what the pipeline reads.
type Config struct {
	// Table is the API front's route table, whose held routes the pipeline
	// mounts.
	Table *routetable.Table
	// Sessions resolves the session read and manage checks
	// (GWSEAM-AuthorizeSession).
	Sessions seam.Sessions
	// Permissions resolves the permission gates
	// (GWSEAM-AuthorizePermission).
	Permissions seam.Permissions
	// DevTenantHeader resolves the tenant of a request with neither a
	// principal tenant nor a service tenant from the X-Lenny-Tenant-ID dev
	// header, or as the default tenant, as the gateway's --dev-mode does.
	DevTenantHeader bool
}

// Pipeline is the request pipeline of the session paths. It serves
// seam.RequestIdentity. Use New.
type Pipeline struct {
	cfg      Config
	mu       sync.Mutex
	handlers map[seam.Listener]*gate
}

// errMissingInput reports a Config that lacks a value the pipeline reads.
var errMissingInput = errors.New("pipeline: missing input")

// New returns a pipeline over cfg. It refuses a Config whose route table is
// nil, or whose Sessions or Permissions is nil or holds a nil pointer, so a
// gateway wired without the session core's checks fails at startup rather
// than admitting a request.
func New(cfg Config) (*Pipeline, error) {
	switch {
	case cfg.Table == nil:
		return nil, fmt.Errorf("%w: route table", errMissingInput)
	case seam.IsNil(cfg.Sessions):
		return nil, fmt.Errorf("%w: session checks (GWSEAM-AuthorizeSession)", errMissingInput)
	case seam.IsNil(cfg.Permissions):
		return nil, fmt.Errorf("%w: permission gate (GWSEAM-AuthorizePermission)", errMissingInput)
	}
	return &Pipeline{cfg: cfg, handlers: map[seam.Listener]*gate{}}, nil
}

// Mount builds the handlers of the client and forward listeners and mounts
// each on the session paths of its listener. Call it once, after every part
// has registered its routes: a route the pipeline would mount that
// registers afterwards is refused by the route table.
//
// spec: GWSEAM-HandleRoute
func (p *Pipeline) Mount() error {
	for _, l := range []seam.Listener{seam.ListenerClient, seam.ListenerForward} {
		if err := p.cfg.Table.MountSessionPaths(l, p.Handler(l)); err != nil {
			return err
		}
	}
	return nil
}

// Handler returns the handler of the session paths of listener l, building
// it on the first call, which seals l in the route table. The handler runs
// the tenant check and then the listener's routes, each behind its access
// check. The session core's in-process service calls run through the client
// listener's handler.
//
// spec: GWSEAM-HandleRoute
func (p *Pipeline) Handler(l seam.Listener) http.Handler {
	return p.handler(l)
}

// RoutePatterns returns the pattern of every route the handler of listener
// l mounts, in the order it mounts them, each followed by the name of its
// access check. The composition root's route table test reads it.
func (p *Pipeline) RoutePatterns(l seam.Listener) []string {
	return append([]string(nil), p.handler(l).patterns...)
}

// handler builds or returns the gate of listener l.
func (p *Pipeline) handler(l seam.Listener) *gate {
	p.mu.Lock()
	defer p.mu.Unlock()
	if g, ok := p.handlers[l]; ok {
		return g
	}
	g := &gate{p: p, mux: http.NewServeMux(), open: map[string]bool{}}
	for _, r := range p.cfg.Table.PipelineRoutes(l) {
		pattern := r.MuxPattern()
		g.mux.Handle(pattern, p.guard(r.Access, r.Handler))
		g.patterns = append(g.patterns, pattern+" ["+accessName(r.Access)+"]")
		if r.Access == seam.AccessHandler {
			g.open[pattern] = true
		}
	}
	p.handlers[l] = g
	return g
}

// RequestTenant is GWSEAM-RequestTenant: the tenant the request runs as,
// as the pipeline's tenant check resolves it, or the empty string when none
// resolves.
//
// spec: GWSEAM-RequestTenant
func (p *Pipeline) RequestTenant(r *http.Request) string {
	return Identity{DevTenantHeader: p.cfg.DevTenantHeader}.RequestTenant(r)
}

// Identity serves GWSEAM-RequestTenant as the pipeline's tenant check
// resolves the tenant, for a part the composition root builds before the
// pipeline, because a part takes its request identity when it is built and
// the pipeline is built once every part has registered its routes.
type Identity struct {
	// DevTenantHeader is Config.DevTenantHeader.
	DevTenantHeader bool
}

// RequestTenant is GWSEAM-RequestTenant: the tenant the request runs as,
// or the empty string when none resolves.
//
// spec: GWSEAM-RequestTenant
func (i Identity) RequestTenant(r *http.Request) string {
	tenant, _ := authmw.RequestTenant(r, i.DevTenantHeader)
	return tenant
}

var (
	_ seam.RequestIdentity = (*Pipeline)(nil)
	_ seam.RequestIdentity = Identity{}
)

// gate is the handler of one listener's session paths: the tenant check in
// front of the mux of the listener's routes.
type gate struct {
	p   *Pipeline
	mux *http.ServeMux
	// open holds the patterns of the routes that leave authentication to
	// their handler, which the tenant check passes.
	open map[string]bool
	// patterns holds each mounted route's pattern and access check name.
	patterns []string
}

// Mux returns the mux of the listener's routes, the admin.MuxUnwrapper
// convention, so route introspection in the contract and OpenAPI
// completeness tests reaches the registered patterns past the gate.
func (g *gate) Mux() *http.ServeMux { return g.mux }

// ServeHTTP runs the tenant check and then the route. A request on a route
// with a tenant, session, or permission check reaches its handler only when
// a tenant resolves for it, and is otherwise refused with the §15.1
// UNAUTHORIZED (401) code, the response the authentication middleware gives
// a request with no credentials. The middleware already refuses such a
// request outside dev mode, so this check is the pipeline's own fail-closed
// check for a misconfigured middleware chain. A path the mux does not route
// passes through to the mux's own 404 or 405.
//
// spec: §10.2 (Authentication); §15.1 (UNAUTHORIZED); §5.1
// (publishedMetadata visibility levels).
func (g *gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, pattern := g.mux.Handler(r); pattern != "" && !g.open[pattern] {
		if _, ok := authmw.RequestTenant(r, g.p.cfg.DevTenantHeader); !ok {
			writeUnauthenticated(w)
			return
		}
	}
	g.mux.ServeHTTP(w, r)
}
