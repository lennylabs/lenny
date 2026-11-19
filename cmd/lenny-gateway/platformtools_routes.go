// SPDX-License-Identifier: MIT

package main

import (
	"log"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/api/pipeline"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/toolapproval"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// attachToolRoutes builds the platform tools seam's client routes and
// registers them through the API front's route table front, from which the
// API front's request pipeline mounts them behind their access checks. The
// routes reach the session core through its side of the seam interface,
// resolve the request's tenant through the API front's request identity,
// read the caller identity through the request plumbing (pipeline.Callers),
// and count tree cycles on the seam's own metric set.
// No memory store is wired, as the session server had none, so the memory
// routes answer MEMORY_UNAVAILABLE. It is a free function rather than a
// method of the wiring accumulator, so the startup order guard is
// unchanged.
func attachToolRoutes(srv *sessionserver.Server, front seam.Routes, sessions sessionstore.Store, interactions interactionstore.Store, waits *toolapproval.Registry, auditAppender audit.Appender, treeCycles mcptools.TreeCycleObserver) {
	core := srv.ToolsSeam()
	routes, err := platformtools.New(platformtools.Options{
		Sessions:         core,
		Identity:         pipeline.Identity{DevTenantHeader: srv.DevTenantHeader()},
		Callers:          pipeline.Callers{},
		ActiveCallers:    core,
		Store:            sessions,
		Interactions:     interactions,
		Approvals:        platformtools.NewToolApprovals(waits),
		InteractionAudit: interactionResolutionAuditor{appender: auditAppender},
		TreeCycles:       treeCycles,
	})
	if err != nil {
		log.Fatalf("lenny-gateway: platform tools routes: %v", err)
	}
	if err := routes.RegisterRoutes(front); err != nil {
		log.Fatalf("lenny-gateway: platform tools routes: %v", err)
	}
}
