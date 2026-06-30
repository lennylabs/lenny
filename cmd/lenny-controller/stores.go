// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	agentpodstatepg "github.com/lennylabs/lenny/pkg/agentpodstate/pgstore"
	"github.com/lennylabs/lenny/pkg/controller/controllermetrics"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	experimentstorepg "github.com/lennylabs/lenny/pkg/gateway/experimentstore/pgstore"
	poolstorepg "github.com/lennylabs/lenny/pkg/gateway/poolstore/pgstore"
	runtimepg "github.com/lennylabs/lenny/pkg/gateway/runtimestore/pgstore"
	sessionstorepg "github.com/lennylabs/lenny/pkg/gateway/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/redisconn"
)

// buildStores constructs the §4.6.1 Postgres-backed stores, the §16.6 / §25.5
// operational-event emitter, and the bounded reconciliation work-queue factory,
// recording them on the accumulator. It also resolves --agent-namespaces once
// so the leader-elected runnables share the parsed list.
//
// The Postgres-backed stores (the agent_pod_state mirror, the orphan-claim
// session lookup, the §5.1 Runtime CRD registry, and the §10.7 pool-scaling
// pool/experiment registries) are constructed only under --postgres-dsn; when
// the flag is empty they stay nil and the dependent controllers skip
// registration in the steps that follow.
//
// spec: §4.6.1 — the agent_pod_state mirror, the orphan-claim GC session
// lookup, and the bounded work-queue factory; §5.1 — the Runtime CRD registry;
// §10.7 — the pool-scaling pool/experiment registries; §16.6 / §25.5 — the
// operational-event emitter (Redis-backed under --redis-url, otherwise the
// controller-local in-memory ring buffer).
func (w *controllerWiring) buildStores() {
	f := w.f

	w.agentNamespaces = splitNamespaces(f.agentNSList)

	// The §4.6.1 agent_pod_state mirror is durable under --postgres-dsn:
	// the WarmPoolController writes the Postgres-side copy of Sandbox
	// status that the gateway's fallback claim path reads. When the flag
	// is empty the mirror store is left nil and the controller skips
	// mirroring.
	//
	// §10.7 PoolScalingController inputs: the §5.2 pool registry is the
	// source of truth for pool definitions, and the cross-tenant
	// experiment registry drives the variant-pool lifecycle. Both require
	// Postgres, so the controller is wired only under --postgres-dsn.
	if f.postgresDSN != "" {
		pgPool, err := pgxpool.New(context.Background(), f.postgresDSN)
		if err != nil {
			log.Fatalf("lenny-controller: postgres: %v", err)
		}
		w.pgPool = pgPool
		w.mirror = agentpodstatepg.New(pgPool)
		// The §4.6.1 orphan-claim GC checks Postgres for an active session
		// backing each candidate claim before reclaiming it.
		w.sessionLookup = &sessionActiveLookup{store: sessionstorepg.New(pgPool)}
		// §5.1 Runtime CRD mirror: the RuntimeReconciler writes the
		// declarative Runtime resources into the same runtime_definitions
		// registry the gateway reads at session creation. Requires the
		// Postgres registry, so it is wired only under --postgres-dsn.
		w.runtimeRegistry = runtimepg.New(pgPool)
		w.poolScalingPools = poolstorepg.New(pgPool)
		w.poolScalingExperiments = experimentstorepg.New(pgPool)
	}

	w.opsEmitter = w.buildOpsEmitter()

	// §4.6.1 bounded, depth-instrumented reconciliation work queue. The
	// same factory is shared by both controllers; each queue is named after
	// its controller, so the lenny_controller_workqueue_depth gauge is
	// labeled per controller.
	w.queueFactory = controllermetrics.NewQueueFactory(f.workqueueMaxDepth)
}

// buildOpsEmitter constructs the §16.6 / §25.5 operational-event emitter the
// controllers emit pool_state_changed events through. When --redis-url is set,
// every emit also lands on the platform-scoped ops:events:stream alongside the
// gateway-emitted events; lenny-ops reads from that one stream. When Redis is
// not configured the emitter writes only to the controller-local in-memory ring
// buffer — the §25.5 per-replica buffer fall-back.
//
// spec: §16.6 — the pool_state_changed event vocabulary; §25.5 — the
// platform-scoped ops:events:stream fan-out with the per-replica in-memory
// buffer fall-back.
func (w *controllerWiring) buildOpsEmitter() events.EventEmitter {
	f := w.f

	controllerReplicaID := os.Getenv("HOSTNAME")
	if controllerReplicaID == "" {
		controllerReplicaID = "controller"
	}
	opsEventBuffer := events.NewEventBuffer(0)
	var opsEmitter events.EventEmitter = events.NewEmitter(opsEventBuffer, controllerReplicaID)
	if f.redisURL != "" {
		redisClient, err := redisconn.NewClient(redisconn.Config{URL: f.redisURL, Password: f.redisPassword})
		if err != nil {
			log.Fatalf("lenny-controller: redis client: %v", err)
		}
		// The Redis client lives for the process lifetime: the §25.5 stream
		// emitter holds it for every emit. It is recorded on the accumulator so
		// runController can defer its close at process shutdown, preserving the
		// close the former monolithic main deferred inline.
		w.redisClient = redisClient
		opsEmitter = events.NewStreamEmitter(events.StreamEmitterOptions{
			Client:    redisClient,
			Buffer:    opsEventBuffer,
			Source:    "//lenny.dev/controller/" + controllerReplicaID,
			ReplicaID: controllerReplicaID,
		})
		log.Printf("lenny-controller: §25.5 operational events streaming to Redis %s", events.DefaultStreamKey)
	}
	return opsEmitter
}
