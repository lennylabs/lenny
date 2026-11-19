// SPDX-License-Identifier: MIT

// Package streamwire attaches the gateway's stream proxy to a session server
// the way cmd/lenny-gateway does, for tests that serve the session routes
// and the session event and log streams from one handler. The stream proxy
// reaches the session core only through the seam interface, so a test that
// builds a session server and reads GET /v1/sessions/{id}/events or
// GET /v1/sessions/{id}/logs attaches the proxy here.
//
// The package is test wiring that decides no platform behavior, so it
// carries no specification annotation.
package streamwire

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/api/pipeline"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/streamproxy"
	"github.com/lennylabs/lenny/tests/testinfra/apifront"
)

// Attach builds a stream proxy from opts and registers its routes through
// the API front route table srv mounts, before srv serves. Each seam field
// of opts left nil is filled with srv's own side of the operation, as the
// composition root fills all of them, so a test sets Events, the bus the
// session server publishes on, and only the fields it replaces with a fake.
// It fails the test on a wiring error.
func Attach(t testing.TB, srv *sessionserver.Server, opts streamproxy.Options) *streamproxy.Handler {
	t.Helper()
	core := srv.StreamSeam()
	if opts.Sessions == nil {
		opts.Sessions = core
	}
	if opts.Frames == nil {
		opts.Frames = core
	}
	if opts.Identity == nil {
		opts.Identity = pipeline.Identity{DevTenantHeader: srv.DevTenantHeader()}
	}
	streams, err := streamproxy.New(opts)
	if err != nil {
		t.Fatalf("streamwire: build the stream proxy: %v", err)
	}
	if err := streams.RegisterRoutes(apifront.Routes(srv)); err != nil {
		t.Fatalf("streamwire: register the stream routes: %v", err)
	}
	return streams
}
