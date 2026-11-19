// SPDX-License-Identifier: MIT

package main

import (
	"log"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/core/toolops"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/toolapproval"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// attachToolRoutes builds the platform tools seam's client routes
// and registers them with the session server, which mounts them behind the
// session routes' access checks until the API front holds the request
// pipeline. The routes reach the session core through its side of the seam
// interface, read the caller identity through the request plumbing
// (toolops.Callers), and count tree cycles on the seam's own metric set.
// No memory store is wired, as the session server had none, so the memory
// routes answer MEMORY_UNAVAILABLE. It is a free function rather than a
// method of the wiring accumulator, so the startup order guard is
// unchanged.
func attachToolRoutes(srv *sessionserver.Server, sessions sessionstore.Store, interactions interactionstore.Store, waits *toolapproval.Registry, auditAppender audit.Appender, treeCycles mcptools.TreeCycleObserver) {
	core := srv.ToolsSeam()
	routes, err := platformtools.New(platformtools.Options{
		Sessions:         core,
		Identity:         core,
		Callers:          toolops.Callers{},
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
	if err := routes.RegisterRoutes(srv); err != nil {
		log.Fatalf("lenny-gateway: platform tools routes: %v", err)
	}
}
