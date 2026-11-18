// SPDX-License-Identifier: MIT

// Package uploadwire attaches the gateway's upload handler to a session
// server the way cmd/lenny-gateway does, for tests that serve the session
// routes and the upload routes from one handler. The upload handler reaches
// the session core only through the seam interface, so a test that builds a
// session server and exercises an upload, a blob read, or a finalize that
// closes an upload window attaches the handler here.
//
// The package is test wiring that decides no platform behavior, so it
// carries no specification annotation.
package uploadwire

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/uploadhandler"
)

// Attach builds an upload handler from opts, registers its routes with srv,
// and attaches it as srv's upload windows, before srv serves. Each seam
// field of opts left nil is filled with srv's own side of the operation, as
// the composition root fills all of them, so a test sets only the fields it
// replaces with a fake. It fails the test on a wiring error.
func Attach(t testing.TB, srv *sessionserver.Server, opts uploadhandler.Options) *uploadhandler.Handler {
	t.Helper()
	core := srv.UploadSeam()
	if opts.Sessions == nil {
		opts.Sessions = core
	}
	if opts.Identity == nil {
		opts.Identity = core
	}
	if opts.Forwarding == nil {
		opts.Forwarding = core
	}
	if opts.Workspaces == nil {
		opts.Workspaces = core
	}
	if opts.Tokens == nil {
		opts.Tokens = core
	}
	if opts.Quota == nil {
		opts.Quota = core
	}
	if opts.Audit == nil {
		opts.Audit = core
	}
	up, err := uploadhandler.New(opts)
	if err != nil {
		t.Fatalf("uploadwire: build the upload handler: %v", err)
	}
	if err := up.RegisterRoutes(srv); err != nil {
		t.Fatalf("uploadwire: register the upload routes: %v", err)
	}
	srv.SetUploadWindows(up)
	return up
}
