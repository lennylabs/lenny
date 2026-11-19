// SPDX-License-Identifier: MIT

// Package toolwire attaches the platform tools seam's client routes, the
// memory routes, the tool-use and elicitation resolution routes, and the
// delegation tree read, to a session server the way cmd/lenny-gateway does,
// for tests that serve the session routes and these routes from one
// handler. The routes reach the session core only through the seam
// interface, so a test that builds a session server and exercises one of
// them attaches the handler here.
//
// The package is test wiring that decides no platform behavior, so it
// carries no specification annotation.
package toolwire

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/core/toolops"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// Attach builds the platform tools seam's route handler from opts and
// registers its routes with srv, before srv serves. Each seam field of opts
// left nil is filled with srv's own side of the operation, and the caller
// identity with the request plumbing's, as the composition root fills them,
// so a test sets only the fields it replaces with a fake. opts.Store is the
// session store srv was built over, which the tree read walks. It fails the
// test on a wiring error.
func Attach(t testing.TB, srv *sessionserver.Server, opts platformtools.Options) *platformtools.Handler {
	t.Helper()
	core := srv.ToolsSeam()
	if opts.Sessions == nil {
		opts.Sessions = core
	}
	if opts.Identity == nil {
		opts.Identity = core
	}
	if opts.ActiveCallers == nil {
		opts.ActiveCallers = core
	}
	if opts.Callers == nil {
		opts.Callers = toolops.Callers{}
	}
	h, err := platformtools.New(opts)
	if err != nil {
		t.Fatalf("toolwire: build the platform tools routes: %v", err)
	}
	if err := h.RegisterRoutes(srv); err != nil {
		t.Fatalf("toolwire: register the platform tools routes: %v", err)
	}
	return h
}
