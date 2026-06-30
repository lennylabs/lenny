// SPDX-License-Identifier: MIT

package main

// gatewayWiring is the §4.1 gateway composition-root accumulator. It
// carries the parsed flags plus the subsystem components each build step
// constructs, so the per-subsystem build steps (buildStores, buildLLMProxy,
// startBackgroundWorkers, and runServers) hand their outputs to the steps
// that wire them and runGateway stays an ordered call sequence over those
// steps interleaved with the remaining inline wiring.
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
