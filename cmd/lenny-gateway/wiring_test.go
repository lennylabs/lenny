// SPDX-License-Identifier: MIT

package main

import (
	"net/http"
	"reflect"
	"testing"
)

// spec: 4.1 (gateway subsystem seams)
//
// TestGatewayWiringExposesPerSubsystemBuildSteps pins the R1 composition-root
// decomposition (proposal 0020 Part A R1). The former gateway main built and
// started every subsystem in one body; R1 reduces runGateway to flag parsing
// plus an ordered call sequence over per-subsystem build steps that each take
// the parsed gatewayFlags off the gatewayWiring accumulator and return their
// component(s). This test takes a method value for each named build step, so a
// revert that re-inlines the construct-and-wire body into a single function
// (the divergence this step corrects) deletes the method and fails to compile
// here. The build steps are unexported, so a compile-time method-value
// reference is the assertion that survives package compilation rather than a
// reflect MethodByName lookup, which only sees exported methods.
//
// The set is the named build steps R1 extracts: the §4.2/§4.4/§4.5 store and
// §4.3/§10.2/§10.3 credential surfaces (buildStores), the §4.9 LLM reverse
// proxy (buildLLMProxy), the §4.1 background-worker launch
// (startBackgroundWorkers), and the §17 run-and-shutdown loop (runServers).
func TestGatewayWiringExposesPerSubsystemBuildSteps(t *testing.T) {
	w := &gatewayWiring{}

	// Each method value below is a compile-time reference: if a build step is
	// removed (the body re-inlined into runGateway), this file stops
	// compiling, which the test tier reports as a failure of this test.
	steps := map[string]any{
		"buildStores":            w.buildStores,
		"buildLLMProxy":          w.buildLLMProxy,
		"startBackgroundWorkers": w.startBackgroundWorkers,
		"runServers":             w.runServers,
	}
	for name, fn := range steps {
		if fn == nil {
			t.Errorf("(*gatewayWiring).%s is nil: the R1 per-subsystem build step is missing", name)
		}
	}

	// buildLLMProxy returns the §4.9 LLM reverse-proxy server (its component),
	// per R1's "each returning its component" requirement. Confirm the return
	// type is the *http.Server the run loop serves.
	if got := reflect.TypeOf(w.buildLLMProxy).Out(0); got != reflect.TypeOf((*http.Server)(nil)) {
		t.Errorf("buildLLMProxy returns %s, want *http.Server (its §4.9 component)", got)
	}
}

// spec: 4.1 (gateway subsystem seams)
//
// TestGatewayWiringEmbedsParsedFlags pins that the accumulator carries the
// parsed gatewayFlags so each build step re-aliases the flags it reads,
// keeping runGateway a flag-parse-then-build sequence rather than re-binding
// every flag pointer at the top of one monolithic body.
func TestGatewayWiringEmbedsParsedFlags(t *testing.T) {
	wt := reflect.TypeOf(gatewayWiring{})
	field, ok := wt.FieldByName("f")
	if !ok {
		t.Fatal("gatewayWiring has no f field: the accumulator must carry the parsed flags")
	}
	if field.Type != reflect.TypeOf((*gatewayFlags)(nil)) {
		t.Errorf("gatewayWiring.f is %s, want *gatewayFlags", field.Type)
	}
}
