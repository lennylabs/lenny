// SPDX-License-Identifier: MIT

package main

import (
	"log"

	"github.com/lennylabs/lenny/pkg/gateway/api/pipeline"
	"github.com/lennylabs/lenny/pkg/gateway/api/routetable"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// mountRequestPipeline builds the API front's request pipeline over the
// routes registered on front, with the session core's session checks and
// permission gate, mounts it on the session paths of the REST and forward
// listeners, and hands the session core the client listener's handler for
// its in-process service calls. Every route the pipeline mounts must be
// registered before it runs.
//
// spec: GWSEAM-HandleRoute
func mountRequestPipeline(front *routetable.Table, sessionSrv *sessionserver.Server) *pipeline.Pipeline {
	sessions, permissions := sessionSrv.RequestChecks()
	p, err := pipeline.New(pipeline.Config{
		Table: front, Sessions: sessions, Permissions: permissions,
		DevTenantHeader: sessionSrv.DevTenantHeader(),
	})
	if err != nil {
		log.Fatalf("lenny-gateway: request pipeline: %v", err)
	}
	if err := p.Mount(); err != nil {
		log.Fatalf("lenny-gateway: request pipeline: %v", err)
	}
	sessionSrv.SetServiceHandler(p.Handler(seam.ListenerClient))
	return p
}
