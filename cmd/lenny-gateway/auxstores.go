// SPDX-License-Identifier: MIT

package main

import (
	"log"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/connectorsecret"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	credentialpoolpg "github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	customrolepg "github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	environmentpg "github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantaccessstore"
	tenantaccesspg "github.com/lennylabs/lenny/pkg/gateway/environment/tenantaccessstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/gateway/resultrollup"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionusage"
	sessionusagepg "github.com/lennylabs/lenny/pkg/gateway/session/sessionusage/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
	usagepg "github.com/lennylabs/lenny/pkg/gateway/usagestore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/vcscred"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// buildAuxStores is the §4.1 composition-root build step (R1) for the
// auxiliary registries the §4.8 policy chain and the request subsystems share.
// It selects the in-memory or Postgres backend for the §10.6 environment
// store, the §4 tenant-access registry, the §4.9 credential-pool registry, the
// §10.2 custom-role registry, the usage store, and the §8.8 session-usage
// store; constructs the §25.3/§25.5 operational-event emitter and its buffer
// (via buildOpsEvents) and wires the §16.7 ops-stream escalation hook onto the
// durable audit Store; builds the §14 VCS credential resolver and threads it to
// the pod binder; and builds the §8.8 shared usage Builder. It records the
// stores, the ops-event emitter and buffer, the VCS resolver, and the usage
// Builder on the accumulator so the build steps below read them back.
//
// spec: §4.1 gateway subsystem seams; §10.6 / §4.9 / §10.2 / §8.8 stores;
// §25.3 / §25.5 operational events; §14 VCS credentials.
func (w *gatewayWiring) buildAuxStores() {
	// environments backs the §10.6 admin environment CRUD, the
	// transparent filtering on lenny/discover_agents, and the §9.1
	// GET /v1/runtimes discovery surface.
	var environments environmentstore.Store = environmentstore.NewMemory()
	if w.pgPool != nil {
		environments = environmentpg.New(w.pgPool)
	}
	// §4 runtime tenant-access registry, shared by the admin
	// tenant-access endpoints and the §5.1 internal meta-fetch endpoint.
	var tenantAccess tenantaccessstore.Store = tenantaccessstore.NewMemory()
	if w.pgPool != nil {
		tenantAccess = tenantaccesspg.New(w.pgPool)
	}

	w.buildOpsEvents()

	// §4.9 credential-pool registry, shared by the admin credential-pool
	// CRUD and the §14 gitClone auth host-to-pool binding check.
	var credentialPools credentialpoolstore.Store = credentialpoolstore.NewMemory()
	if w.pgPool != nil {
		credentialPools = credentialpoolpg.New(w.pgPool)
	}

	// §14 line 95: the VCS credential resolver materializes a gitClone
	// source's short-lived token on the gateway (binding the URL host to
	// one of the tenant's VCS credential pools, then reading the token
	// from the credential's Kubernetes Secret) so the ref-pinning
	// ls-remote and the clone authenticate without the pod ever seeing
	// the raw credential. It needs a cluster client to read Secrets; in a
	// dev/in-memory posture (no cluster client) it stays nil and an
	// authenticated gitClone fails with a clear "no resolver wired"
	// error. The Secret key defaults to `token`, the §4.9 github
	// materializedConfig field.
	var vcsCreds vcscred.Resolver
	if w.clusterClient != nil {
		vcsCreds = &vcscred.StoreResolver{
			Pools:   credentialPools,
			Secrets: connectorsecret.NewKubeResolver(w.clusterClient, "token"),
		}
		if w.podBinder != nil {
			w.podBinder.VCSCreds = vcsCreds
		}
	}

	// §10.2 tenant custom-role registry, shared by the admin custom-role
	// CRUD and the §10.2 session-endpoint authorization gate (so a
	// custom role granting manage_own_sessions / read_own_sessions is
	// honored on the session endpoints as well as the admin surface).
	var customRoles customrolestore.Store = customrolestore.NewMemory()
	if w.pgPool != nil {
		customRoles = customrolepg.New(w.pgPool)
	}

	var usage usagestore.Store = usagestore.NewMemory()
	if w.pgPool != nil {
		usage = usagepg.New(w.pgPool, usagepg.WithReadPool(w.readPool))
	}

	// spec: §8.8 lines 897-917 — the per-session token accumulator the
	// §4.9 proxy folds proxy-extracted counts into; the §8.8 TaskResult
	// usage / treeUsage rollups read it at settle time.
	var sessionUsage sessionusage.Store = sessionusage.NewMemory()
	if w.pgPool != nil {
		sessionUsage = sessionusagepg.New(w.pgPool, sessionusagepg.WithReadPool(w.readPool))
	}
	// spec: §8.8 lines 897-917 — the shared usage Builder both the
	// sessionserver materialization path and the MCP lenny/await_children
	// path use to stamp usage / treeUsage on every TaskResult.
	taskUsageBuilder := resultrollup.New(w.sessions, sessionUsage, w.treeArchive, clockinject.Now)

	w.environments = environments
	w.tenantAccess = tenantAccess
	w.credentialPools = credentialPools
	w.vcsCreds = vcsCreds
	w.customRoles = customRoles
	w.usage = usage
	w.sessionUsage = sessionUsage
	w.taskUsageBuilder = taskUsageBuilder
}

// buildOpsEvents constructs the §25.3/§25.5 operational-event emitter and its
// buffer, the gateway-shared event source emitting subsystems write to. It
// always keeps a local buffer so the §25.3 buffer endpoint and the §25.5
// fall-back path serve the same in-process ring when Redis is unreachable;
// when Redis is wired every emit also lands on the §25.5 platform-scoped
// stream so lenny-ops and the controllers share the same logical event source.
// It wires the §16.7 ops-stream escalation hook onto the durable audit Store
// and records the emitter and the buffer on the accumulator.
//
// spec: §25.3 / §25.5 operational events; §16.7 ops-stream escalation.
func (w *gatewayWiring) buildOpsEvents() {
	opsNonceCheckpointPath := w.f.opsNonceCheckpointPath

	// §25.3 lines 705-710 / 766-772: the operational-event emission and
	// buffer metrics, registered on the gateway's Prometheus registry.
	opsEventsMetrics, err := events.NewMetrics(w.gwMetrics.Registerer())
	if err != nil {
		log.Fatalf("lenny-gateway: §25.3 ops-events metrics: %v", err)
	}
	// §25.3 line 748: the optional on-disk nonce checkpoint so the
	// eventKey stays unique across a restart with a pinned replica id.
	var nonceCheckpoint *events.NonceCheckpoint
	if *opsNonceCheckpointPath != "" {
		nonceCheckpoint = &events.NonceCheckpoint{Path: *opsNonceCheckpointPath}
	}
	opsEmitErrLogger := func(emitErr error) {
		log.Printf("lenny-gateway: §25.3 ops-event emit: %v", emitErr)
	}
	opsEventBuffer := eventbuffer.NewEventBuffer(0, eventbuffer.WithBufferMetrics(opsEventsMetrics))
	emitterOpts := []eventbuffer.EmitterOption{
		eventbuffer.WithMetrics(opsEventsMetrics),
		eventbuffer.WithEmitErrorLogger(opsEmitErrLogger),
	}
	if nonceCheckpoint != nil {
		emitterOpts = append(emitterOpts, eventbuffer.WithNonceCheckpoint(*nonceCheckpoint))
	}
	var opsEmitter events.EventEmitter = eventbuffer.NewEmitter(opsEventBuffer, w.replica, emitterOpts...)
	if w.redisClient != nil {
		opsEmitter = eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
			// §12.4 Cache/Pub-Sub concern: ops event stream fan-out.
			Client:          w.concernRedis.For(storerouter.RedisConcernCachePubSub),
			Buffer:          opsEventBuffer,
			Source:          "//lenny.dev/gateway/" + w.replica,
			ReplicaID:       w.replica,
			Metrics:         opsEventsMetrics,
			NonceCheckpoint: nonceCheckpoint,
			OnError:         opsEmitErrLogger,
		})
		log.Printf("lenny-gateway: §25.5 operational events streaming to Redis %s", eventbuffer.DefaultStreamKey)
	}
	// Wire the §16.7 / §25.5 operational-event escalation path: the
	// durable audit Store routes the §16.7 ops-stream subset of audit
	// events onto the operational event stream as audit-bearing
	// CloudEvents (datacontenttype application/ocsf+json). F-25.5.18.
	if w.auditOpsStore != nil {
		w.auditOpsStore.SetOpsStreamEmitter(opsEmitter, w.replica)
	}

	w.opsEmitter = opsEmitter
	w.opsEventBuffer = opsEventBuffer
}
