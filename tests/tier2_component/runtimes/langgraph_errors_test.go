// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component negative-path tests for the §26.8 langgraph reference
// runtime's bootstrap sequence: the adapter imports the module specified
// by runtimeOptions.graphModule, invokes .compile() on the graph, and
// (per runtimeOptions.recursionLimit, spec/14_workspace-plan-schema.md:184)
// bounds how many super-steps a compiled graph may take before LangGraph's
// own recursion-limit error fires. This file isolates the misconfiguration
// and adversarial-input axis of that sequence (an unresolvable module path,
// and a graph that runs past its configured recursionLimit) so a regression
// that turned either failure into a pod crash instead of a client-visible
// structured error would surface here, independent of the happy-path
// bootstrap flow in tests/tier5_e2e_kind/framework_runtime_langgraph_test.go.
package runtimes_test

import "testing"

// spec: §14 ("\"recursionLimit\": { \"type\": \"integer\", \"minimum\": 1,
// \"maximum\": 500, \"default\": 25 }", spec/14_workspace-plan-schema.md:184)
// read together with §26.8 ("adapter imports the module specified by
// runtimeOptions.graphModule, invokes .compile() on the graph, ... [and]
// invokes graph.ainvoke / graph.astream depending on the graph's declared
// output style", spec/26_reference-runtime-catalog.md:388).
//
// diagnosis: once unskipped, a failure here means a langgraph session
// whose compiled graph exceeds the session's configured recursionLimit
// does not surface a structured error to the client (a delivery failure
// or an error event on the events stream) and instead crashes the runtime
// pod or hangs past the graph's own recursion bound.
func TestLanggraphAdapterSurfacesRecursionLimitOverrunAsStructuredError(t *testing.T) {
	// §14 fixes recursionLimit's schema (integer, 1-500, default 25) but
	// neither §14 nor §26.8 says what happens when a compiled graph's
	// super-step count exceeds that configured value: LangGraph's own
	// `.ainvoke`/`.astream` raises a GraphRecursionError in that case, but
	// §26.8's bootstrap sentence stops at "invokes graph.ainvoke /
	// graph.astream" and does not say whether the adapter catches that
	// error and reports it as a structured client-visible failure (the
	// behavior this finding is written to pin) or lets it propagate
	// unhandled into the adapter process. This test would exercise a
	// fixture LangGraph graph built to loop past a small recursionLimit
	// (for example a two-node cycle with no exit condition, with
	// runtimeOptions.recursionLimit set to 1) via a live langgraph
	// session and assert the delivery receipt or events stream reports a
	// structured error rather than the pod terminating or the request
	// hanging past the graph's own bound.
	//
	// The langgraph reference runtime (github.com/lennylabs/runtime-langgraph)
	// is not vendored in this repo: tests/spec-map.json marks §26.8
	// blocked_until_phase 11, cmd/runtimes/langgraph does not exist, and
	// no in-repo runtime adapter performs the graphModule import/compile/
	// ainvoke bootstrap sequence this test would need to drive (see the
	// sibling skip on TestLanggraphRuntimeSessionBootstrapsAndStreamsResponse
	// in tests/tier5_e2e_kind/framework_runtime_langgraph_test.go for the
	// same missing-adapter reasoning applied to the happy-path bootstrap
	// flow). There is therefore no adapter to compile a recursion-overrun
	// fixture graph against and no pod to observe crashing or not.
	//
	// Unskip once a runnable langgraph image or an in-repo adapter
	// implementing the graphModule/compile/ainvoke bootstrap contract
	// exists, and a fixture graph module that exceeds a small
	// recursionLimit is available under tests/testdata.
	t.Skip("no runnable langgraph reference-runtime image or in-repo adapter implementing the §26.8 graphModule/compile/ainvoke bootstrap contract exists yet, so a recursionLimit overrun cannot be driven against a live compiled graph")
}

// spec: §26.8 ("adapter imports the module specified by
// runtimeOptions.graphModule, invokes .compile() on the graph",
// spec/26_reference-runtime-catalog.md:388) read together with §14's
// requirement that graphModule is a required runtimeOptions field
// (spec/14_workspace-plan-schema.md:184, "`graphModule` (required)" per
// §26.8's line 378 cross-reference).
//
// diagnosis: once unskipped, a failure here means a langgraph session
// configured with a graphModule path that does not resolve, or a module
// whose graph object fails .compile(), does not surface a structured
// error to the client and instead crashes the runtime pod or leaves the
// session stuck with no delivery receipt.
func TestLanggraphAdapterRejectsUnresolvableGraphModule(t *testing.T) {
	// §26.8's bootstrap sentence describes only the success path: "adapter
	// imports the module specified by runtimeOptions.graphModule, invokes
	// .compile() on the graph". Neither §26.8 nor §14 states what the
	// adapter reports when the import step itself fails (a graphModule
	// path with no matching file, or a module that raises on import) or
	// when the imported object's .compile() call raises (an invalid
	// LangGraph graph definition). This test would exercise a live
	// langgraph session started with a runtimeOptions.graphModule value
	// pointing at a nonexistent fixture module path and assert the
	// resulting delivery receipt or events stream reports a structured
	// bootstrap error rather than the pod crashing or the session hanging
	// with no observable failure.
	//
	// As with the recursion-limit case above, the langgraph reference
	// runtime is not vendored in this repo (same §26.8 Phase-11 deferral,
	// tests/spec-map.json blocked_until_phase 11, no cmd/runtimes/langgraph
	// package), so there is no adapter to attempt the import/compile
	// sequence against an unresolvable module path and no pod to observe.
	//
	// Unskip once a runnable langgraph image or an in-repo adapter
	// implementing the graphModule import/.compile() bootstrap step
	// exists, and a fixture graphModule path known not to resolve (or a
	// fixture module that fails .compile()) is available under
	// tests/testdata.
	t.Skip("no runnable langgraph reference-runtime image or in-repo adapter implementing the §26.8 graphModule import/.compile() bootstrap step exists yet, so an unresolvable graphModule path cannot be driven against a live adapter")
}
