// SPDX-License-Identifier: MIT

package main

// gatewayWiring is the §4.1 gateway composition-root accumulator. It
// carries the parsed flags plus the subsystem components each build step
// constructs, so the per-subsystem build steps (buildStores and its
// per-concern sub-steps buildBillingPipeline and buildTokenSigningStores;
// buildAdminRouter; buildHTTPSurface; buildLLMProxy; startBackgroundWorkers
// and its per-group sub-steps startReconcilerWorkers,
// startBillingAndSecurityWorkers, and startLeaderElectedSweeps; and
// runServers) hand their outputs to the steps that wire them, and
// runGateway stays an ordered call sequence over those steps interleaved
// with the remaining inline wiring (the §4.8 policy interceptor chain, the
// §4.2 session server, and the §9.1 MCP fabric, whose construct-and-wire
// blocks each reference too many cross-step locals to extract behavior-
// preservingly without a 30-parameter signature).
//
// Decomposition mechanism (ratifies proposal 0020 §7 "Helper homes" open
// decision): the per-subsystem build steps use this shared accumulator
// rather than the illustrative (component, error) return signature the
// proposal's §4 Part A R1 sketches. The gateway composition root threads
// ~140 cross-step locals between subsystems (the §4.1 stores feed the
// admin router, the LLM proxy, the session server, and the background
// workers), so a (component, error) constructor per subsystem would carry a
// 20-to-30-value return surface and the caller would re-thread every value
// by hand. The accumulator keeps each step a focused unit that reads the
// inputs earlier steps recorded and records the outputs later steps consume,
// which is the behavior-preserving mechanism R1's intent ("reduce the
// composition root to an ordered call sequence") actually requires here.
// Error handling stays as the inline log.Fatalf the original composition
// root used: a gateway that cannot construct a subsystem at startup must
// abort the process, not return an error to a caller that has no recovery
// path. The §4.3 token-service connection close stays deferred in runGateway
// (not the build step that dials it) so the connection lives for the process
// lifetime.
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
