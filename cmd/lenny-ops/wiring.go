// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"time"
)

// runOps wires and starts every lenny-ops subsystem from the parsed flags,
// then serves the §25.4 operability API until shutdown. It is the lenny-ops
// composition root: a flat ordered sequence of per-subsystem build-step
// calls (each defined in a subsystem-named sibling file and documented
// there), terminating in the signal-driven graceful shutdown. No subsystem
// is constructed inline here; every build step records its outputs on the
// opsWiring accumulator.
//
// This is the ordered call sequence proposal 0020 §4 Part A R4 specifies, the
// same archetype as R1 at smaller scale. The deferred shutdown closes that
// the former monolithic main ran on return stay deferred here so they run at
// process shutdown in the same order: the §16.3 trace provider shutdown, the
// §25.5 subscription-cache stop, the signal-context cancel, and the Postgres
// pool / Redis client closes.
//
// spec: §4.1 — the gateway and its sibling binaries are each one component
// whose composition root constructs each subsystem in dependency order;
// §25.4 — the lenny-ops service body is the HTTP surface plus the
// leader-elected background loops.
func runOps(f *opsFlags) {
	w := &opsWiring{f: f}

	// §25.4 process-wide setup: TTL bounds, replica identity, structured
	// logging, the SIGTERM/SIGINT root context, and the §16.3 tracer.
	w.buildProcessSetup()
	defer w.stop()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = w.traceShutdown(shutdownCtx)
	}()

	// §25.4 dependencies: Postgres, Redis, the §12.6 store router and §11.7
	// audit recorder, the Kubernetes clients, probes, runbooks, and the
	// leader elector.
	w.buildDependencies()
	if w.pgPool != nil {
		defer w.pgPool.Close()
	}
	if w.redisClient != nil {
		defer func() { _ = w.redisClient.Close() }()
	}

	// §25.5 event stream + webhook delivery, then the §25.4 gateway link.
	w.buildEventStreamAndWebhooks()
	defer w.subscriptionCache.Stop()
	w.buildGatewayLink()

	// §25.4 self-health and locks, §25 operability services, §25.8 upgrade
	// subsystem, and the §25.4 inventory / idempotency / bundle-rules.
	w.buildSelfHealthAndLocks()
	w.buildOpsServices()
	w.buildUpgradeSubsystem()
	w.buildInventoryAndIdempotency()

	// §25.4 service body (leader election + background loops) and the §25.4
	// HTTP surface.
	w.buildServiceBody()
	w.buildHTTPSurface()

	// Serve the §25.4 operability API until shutdown, then drain the
	// background loops.
	w.runServer()
}

// opsWiring is the §25.4 lenny-ops composition-root accumulator. It carries
// the parsed flags plus the subsystem components each build step
// constructs, so the per-subsystem build steps hand their outputs to the
// steps that wire them. runOps is the composition root: it parses its inputs
// once (via parseFlags, in main) and then runs an ordered sequence of
// per-subsystem build-step calls. The build steps live in subsystem-named
// sibling files under cmd/lenny-ops/ (proposal 0020 §4 Part A R4, the same
// archetype as R1 at smaller scale):
//
//   - deps_setup.go: the §25.4 process-wide setup (TTL bounds, replica
//     identity, logging, signal context, tracing) and the §25.4 dependency
//     construction (Postgres, Redis, the §12.6 store router and §11.7 audit
//     recorder, the Kubernetes clients, probes, the runbook index, and the
//     leader elector) — buildProcessSetup and buildDependencies.
//   - events_wiring.go: the §25.5 operational-event stream and emitter plus
//     the webhook delivery worker, subscription cache, and gateway admin-API
//     client — buildEventStreamAndWebhooks and buildGatewayClient.
//   - services_wiring.go: the §25.4 self-health checks, the tiered
//     remediation-lock and clock-skew samplers, the §25.11 backup service,
//     the §25.4 escalation / §25.10 drift / §25.6 diagnostics and doctor
//     services, the §25.8 release-channel / upgrade / registry / version
//     subsystems, and the §25.4 operations inventory, idempotency store, and
//     bundle-rules reconciler — buildSelfHealthAndLocks, buildOpsServices,
//     buildUpgradeSubsystem, and buildInventoryAndIdempotency.
//   - servicebody.go: the §25.4 service body (opsservice.New with the
//     leader-elected cron jobs and reconciliation loops) — buildServiceBody.
//   - httpsurface.go: the §25.13 alert evaluator, the §25.4 OIDC auth gate,
//     the §25.4 HTTP surface (opsserver.New), and the §25.5/§25.4 periodic
//     gauge-refresh and reap goroutines plus the §4.4 pgaudit shipper —
//     buildHTTPSurface and runServer.
//
// Decomposition mechanism (mirrors the gatewayWiring accumulator decision in
// cmd/lenny-gateway/wiring.go): the per-subsystem build steps use this shared
// accumulator rather than a (component, error) return per subsystem, because
// the lenny-ops composition root threads many cross-step locals between
// subsystems (the dependencies feed the event stream, the lock service, the
// backup service, the inventory, the service body, and the HTTP surface), so
// a constructor-per-subsystem would carry a large return surface the caller
// re-threads by hand. The accumulator keeps each step a focused unit that
// reads the inputs earlier steps recorded and records the outputs later steps
// consume.
//
// Error handling stays as the inline log.Fatalf the original composition root
// used: a lenny-ops replica that cannot construct a subsystem at startup must
// abort the process rather than return an error to a caller with no recovery
// path. The deferred shutdown closes (the §16.3 trace provider shutdown, the
// §25.5 subscription-cache stop, the signal-context cancel, and the Postgres
// pool / Redis client closes) stay deferred in runOps so they run at process
// shutdown rather than when the build step returns.
//
// spec: §4.1 — the gateway and its sibling binaries are each one component
// whose composition root threads its inputs and constructed subsystems
// through the per-subsystem builders in dependency order; §25.4 — the
// lenny-ops service body is the HTTP surface plus the leader-elected
// background loops.
type opsWiring struct {
	f *opsFlags

	opsWiringFields
}
