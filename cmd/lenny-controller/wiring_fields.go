// SPDX-License-Identifier: MIT

package main

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"

	agentpodstatepg "github.com/lennylabs/lenny/pkg/agentpodstate/pgstore"
	"github.com/lennylabs/lenny/pkg/controller/controllermetrics"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/resourceclass"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/events"
	experimentstorepg "github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore/pgstore"
	poolstorepg "github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore/pgstore"
	runtimepg "github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore/pgstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// controllerWiringFields holds the components the lenny-controller composition
// root threads between build steps. A field is set by the step that constructs
// the component and read by the steps that wire it, mirroring the gatewayWiring
// and opsWiring accumulators (proposal 0020 §4 Part A R1 / R4 / R8). The field
// set is populated from the cross-step locals the former monolithic main
// threaded inline.
//
// spec: §4.6.1 — the WarmPoolController and its sibling reconcilers/runnables
// share the manager, the bounded work-queue factory, and the §16.6 ops emitter
// the composition root constructs; §4.1 — the composition root threads its
// inputs and constructed subsystems through the per-subsystem builders in
// dependency order.
type controllerWiringFields struct {
	// §4.6.1 manager setup.
	traceShutdown         func(context.Context) error
	restCfg               *rest.Config
	mgr                   ctrl.Manager
	runtimeClassOverrides map[isolation.Profile]string
	resourceClasses       resourceclass.Registry

	// §4.6.1 Postgres-backed stores and ops emitter.
	pgPool                 *pgxpool.Pool
	redisClient            *redis.Client
	mirror                 *agentpodstatepg.Store
	sessionLookup          warmpool.SessionLookup
	runtimeRegistry        *runtimepg.Store
	poolScalingPools       *poolstorepg.Store
	poolScalingExperiments *experimentstorepg.Store
	opsEmitter             events.EventEmitter
	queueFactory           controllermetrics.QueueFactory

	// §4.6.1 agent namespaces resolved from --agent-namespaces, shared by the
	// leader-elected mirror/GC, pool-scaling, and CIDR-drift runnables.
	agentNamespaces []string
}
