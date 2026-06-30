// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
)

// runController wires and starts the lenny-controller control plane from the
// parsed flags, then blocks on the §4.6.1 manager run loop until the process
// receives SIGTERM/SIGINT. It is the lenny-controller composition root: a flat
// ordered sequence of per-subsystem build-step calls (each defined in a
// subsystem-named sibling file and documented there), terminating in
// mgr.Start. No subsystem is constructed inline here; every build step records
// its outputs on the controllerWiring accumulator.
//
// This is the ordered call sequence proposal 0020 §4 Part A R8 specifies, the
// same archetype as R1 (cmd/lenny-gateway) and R4 (cmd/lenny-ops) at smaller
// scale. The deferred closes that the former monolithic main ran on return stay
// deferred here so they run at process shutdown in the same order: the §16.3
// trace provider shutdown and the Postgres pool close.
//
// spec: §4.1 — the gateway and its sibling binaries are each one component
// whose composition root constructs each subsystem in dependency order; §4.6.1
// — the lenny-controller hosts the WarmPoolController, the Sandbox reconciler,
// and the leader-elected mirror / GC / pool-scaling / CIDR-drift runnables.
func runController(f *controllerFlags) {
	w := &controllerWiring{f: f}

	// §4.6.1 manager setup: the §16.3 tracer, the §17.5 RuntimeClass-name
	// overrides, the §5.2/§6.4 resource-class registry, the §4.6.1 rate-limited
	// REST config, the controller-runtime manager, and the §10 line 437 CRD
	// schema-version self-check.
	w.buildManagerSetup()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = w.traceShutdown(shutdownCtx)
	}()

	// §4.6.1 Postgres-backed stores, the §16.6 / §25.5 ops emitter, and the
	// bounded reconciliation work-queue factory. The Postgres pool and the
	// §25.5 Redis client are closed at process shutdown in the same order the
	// former monolithic main deferred them: the Postgres close registers before
	// the Redis close, so the Redis client closes first (defers run LIFO).
	w.buildStores()
	if w.pgPool != nil {
		defer w.pgPool.Close()
	}
	if w.redisClient != nil {
		defer func() { _ = w.redisClient.Close() }()
	}

	// §4.6.1 controller and leader-elected runnable registration.
	w.registerCoreControllers()
	w.registerOptionalControllers()
	w.registerLeaderRunnables()

	// §4.6.1 health and readiness probes, then run the manager until shutdown.
	w.registerProbes()
	w.runManager()
}

// runManager starts the §4.6.1 controller-runtime manager and blocks until the
// signal handler cancels its context, then logs the exit reason. A manager that
// exits with an error aborts the process so the Deployment restarts the replica.
//
// spec: §4.6.1 — only the leader-elected replica reconciles; the manager's
// signal handler drives the graceful shutdown.
func (w *controllerWiring) runManager() {
	log.Printf("lenny-controller: starting manager (leader-election=%t)", w.f.leaderElect)
	if err := w.mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Fatalf("lenny-controller: manager exited: %v", err)
	}
}

// controllerWiring is the §4.6.1 lenny-controller composition-root accumulator.
// It carries the parsed flags plus the subsystem components each build step
// constructs, so the per-subsystem build steps hand their outputs to the steps
// that wire them. runController is the composition root: it parses its inputs
// once (via parseFlags, in main) and then runs an ordered sequence of
// per-subsystem build-step calls. The build steps live in subsystem-named
// sibling files under cmd/lenny-controller/ (proposal 0020 §4 Part A R8, the
// same archetype as R1 / R4 at smaller scale):
//
//   - setup.go: the §4.6.1 manager setup (the §16.3 tracer, the §17.5
//     RuntimeClass-name overrides, the §5.2/§6.4 resource-class registry, the
//     §4.6.1 rate-limited REST config, the controller-runtime manager, and the
//     §10 line 437 CRD schema-version self-check) — buildManagerSetup.
//   - stores.go: the §4.6.1 Postgres-backed stores (the agent_pod_state mirror,
//     the session lookup, the Runtime CRD registry, and the §10.7 pool-scaling
//     pool/experiment registries), the §16.6 / §25.5 ops emitter, and the
//     bounded reconciliation work-queue factory — buildStores.
//   - controllers.go: the §4.6.1 WarmPoolController, Sandbox reconciler, per-pod
//     reconciler, and occupancy-projection reconciler (always registered), the
//     §5.1 RuntimeReconciler (Postgres-gated), and the leader-elected §4.6.1
//     mirror / orphan-claim GC, §4.6.2/§10.7 PoolScalingController, §13.2 CIDR
//     drift detector, and §4.6.1 leader-lease monitor runnables —
//     registerCoreControllers, registerOptionalControllers, and
//     registerLeaderRunnables.
//   - probes.go: the §4.6.1 healthz/readyz probe registration — registerProbes.
//
// Decomposition mechanism (mirrors the gatewayWiring and opsWiring accumulator
// decision in cmd/lenny-gateway/wiring.go and cmd/lenny-ops/wiring.go): the
// per-subsystem build steps use this shared accumulator rather than a
// (component, error) return per subsystem, because the composition root threads
// many cross-step locals between subsystems (the manager feeds every
// controller; the stores feed the warm-pool mirror, the orphan-claim GC, the
// RuntimeReconciler, and the PoolScalingController; the queue factory feeds
// every controller). The accumulator keeps each step a focused unit that reads
// the inputs earlier steps recorded and records the outputs later steps consume.
//
// Error handling stays as the inline log.Fatalf the original composition root
// used: a controller replica that cannot construct a subsystem or register a
// controller at startup must abort the process rather than return an error to a
// caller with no recovery path. The deferred closes (the §16.3 trace provider
// shutdown and the Postgres pool close) stay deferred in runController so they
// run at process shutdown rather than when the build step returns.
//
// spec: §4.6.1 — the WarmPoolController and its sibling reconcilers/runnables
// share the manager, the bounded work-queue factory, and the §16.6 ops emitter;
// §4.1 — the composition root threads its inputs and constructed subsystems
// through the per-subsystem builders in dependency order.
type controllerWiring struct {
	f *controllerFlags

	controllerWiringFields
}
