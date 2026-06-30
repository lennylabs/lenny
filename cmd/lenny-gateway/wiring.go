// SPDX-License-Identifier: MIT

package main

// gatewayWiring is the §4.1 gateway composition-root accumulator. It
// carries the parsed flags plus the subsystem components each build step
// constructs, so the per-subsystem build steps hand their outputs to the
// steps that wire them. runGateway is the composition root: it parses its
// inputs once and then runs an ordered sequence of per-subsystem build-step
// calls. The build steps live in subsystem-named sibling files under
// cmd/lenny-gateway/ (proposal 0020 §4 Part A R1, "Helpers land in new
// sibling files under cmd/lenny-gateway/ grouped by subsystem"):
//
//   - stores.go: the §4.2/§4.4/§4.5 persistence and §4.3/§10.2/§10.3
//     credential/signing surfaces (buildStores and its per-concern sub-steps
//     buildPersistenceStores, buildRedisAndQuota, buildStoreRouterAndSecurityBus,
//     buildBillingPipeline, buildTokenSigningStores, buildExecutorAndCredentials,
//     buildPodLifecycle, and buildSessionMessaging).
//   - policychain.go: the §4.8 policy interceptor chain and the §11.2/§12.4
//     quota surfaces (buildPolicyChain).
//   - sessionsrv.go: the §4.2 session server, which realizes the §4.1 Stream
//     Proxy and Upload Handler behind the sessionserver interfaces
//     (buildSessionServer).
//   - mcpsurface.go: the §9.1 MCP fabric — the §8.2 delegation service, the
//     MCP server, and the registered tool families (buildMCPSurface).
//   - adminrouter.go: the §15.1 admin REST router (buildAdminRouter).
//   - httpsurface.go: the REST mux and HTTP server (buildHTTPSurface).
//   - llmproxy.go: the §4.9 LLM reverse proxy (buildLLMProxy).
//   - controlserver.go: the §8.6 GatewayControl gRPC server and the §6.2 /
//     §11.3 session watchdog (buildControlServer).
//   - workers.go: the §4.1 background-worker launch (startBackgroundWorkers
//     and its per-group sub-steps startReconcilerWorkers,
//     startBillingAndSecurityWorkers, and startLeaderElectedSweeps).
//   - runserver.go: the §17 run-and-shutdown loop (runServers).
//
// Decomposition mechanism (ratifies proposal 0020 §7 "Helper homes" open
// decision): the per-subsystem build steps use this shared accumulator (and,
// where a step has a small fixed output set, an explicit return) rather than
// the illustrative (component, error) return signature the proposal's §4
// Part A R1 sketches. The gateway composition root threads ~140 cross-step
// locals between subsystems (the §4.1 stores feed the admin router, the LLM
// proxy, the session server, and the background workers), so a
// (component, error) constructor per subsystem would carry a 20-to-30-value
// return surface and the caller would re-thread every value by hand. The
// accumulator keeps each step a focused unit that reads the inputs earlier
// steps recorded and records the outputs later steps consume, which is the
// behavior-preserving mechanism R1's intent ("reduce the composition root to
// an ordered call sequence") requires here.
//
// Error handling stays as the inline log.Fatalf the original composition
// root used: a gateway that cannot construct a subsystem at startup must
// abort the process, not return an error to a caller that has no recovery
// path. The §4.3 token-service connection close, the §6.2 watchdog-context
// cancel, and the §3.2/§3.4 coordinator Stops stay deferred in runGateway
// (not the build steps that dial or construct them) so they run at process
// shutdown rather than when the build step returns.
//
// Each build step reads the flags it needs from the embedded *gatewayFlags
// (re-aliased to the original local names at the top of the step so the
// moved construct-and-wire blocks read unchanged) and records the
// components later steps consume on this value. A field is set by the step
// that constructs the component and read by the steps that wire it.
//
// spec: §4.1 — the gateway is one component internally partitioned into
// subsystem boundaries (Go interfaces within a single binary); this value
// threads the composition-root inputs and the constructed subsystems
// through the per-subsystem builders in dependency order. The field set is
// kept in wiring_fields.go, populated from the cross-step locals the
// former monolithic composition root threaded inline.
type gatewayWiring struct {
	f *gatewayFlags

	gatewayWiringFields
}
