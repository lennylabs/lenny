// SPDX-License-Identifier: MIT

package main

import (
	"log"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/streamproxy"
	"github.com/lennylabs/lenny/pkg/gateway/streamproxy/sessionevents"
)

// attachStreamProxy builds the §4.1 stream proxy seam's event and log
// stream handler and wires it to the session core through the seam
// interface: the handler reads the session event bus, reaches the core
// through the core's stream seam operations, and registers its routes
// through the API front's route table front. It runs before the session
// server serves. A wiring failure stops the process, as every other build
// step's does.
func attachStreamProxy(srv *sessionserver.Server, front seam.Routes, bus *sessionevents.Bus) {
	core := srv.StreamSeam()
	opts := streamproxy.Options{
		Sessions: core,
		Frames:   core,
		Identity: core,
		Clock:    clockinject.Now,
	}
	// A nil bus leaves Events unset, so the endpoints answer
	// EVENT_STREAM_UNAVAILABLE rather than holding a typed nil.
	if bus != nil {
		opts.Events = bus
	}
	streams, err := streamproxy.New(opts)
	if err != nil {
		log.Fatalf("lenny-gateway: stream proxy: %v", err)
	}
	if err := streams.RegisterRoutes(front); err != nil {
		log.Fatalf("lenny-gateway: stream proxy routes: %v", err)
	}
}
