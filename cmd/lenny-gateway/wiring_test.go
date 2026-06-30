// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"net/http"
	"os"
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
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
// §4.3/§10.2/§10.3 credential surfaces (buildStores), the §4.8 policy engine
// (buildPolicyChain), the §4.2 session server (buildSessionServer), the §9.1
// MCP fabric (buildMCPSurface), the §15.1 admin router (buildAdminRouter), the
// REST HTTP surface (buildHTTPSurface), the §4.9 LLM reverse proxy
// (buildLLMProxy), the §8.6/§6.2 control server and watchdog
// (buildControlServer), the §4.1 background-worker launch
// (startBackgroundWorkers), and the §17 run-and-shutdown loop (runServers).
// runGateway must not carry any named subsystem inline; it is a short ordered
// sequence over these build steps.
func TestGatewayWiringExposesPerSubsystemBuildSteps(t *testing.T) {
	w := &gatewayWiring{}

	// Each method value below is a compile-time reference: if a build step is
	// removed (the body re-inlined into runGateway), this file stops
	// compiling, which the test tier reports as a failure of this test.
	steps := map[string]any{
		"buildStores":            w.buildStores,
		"buildPolicyChain":       w.buildPolicyChain,
		"buildSessionServer":     w.buildSessionServer,
		"buildMCPSurface":        w.buildMCPSurface,
		"buildAdminRouter":       w.buildAdminRouter,
		"buildHTTPSurface":       w.buildHTTPSurface,
		"buildLLMProxy":          w.buildLLMProxy,
		"buildControlServer":     w.buildControlServer,
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

	// buildSessionServer returns the §4.2 session server (its component), so
	// the composition root threads one constructed *sessionserver.Server to the
	// MCP fabric, the admin router, the HTTP surface, and the watchdog.
	if got := reflect.TypeOf(w.buildSessionServer).Out(0); got != reflect.TypeOf((*sessionserver.Server)(nil)) {
		t.Errorf("buildSessionServer returns %s, want *sessionserver.Server (its §4.2 component)", got)
	}
}

// spec: 4.1 (gateway subsystem seams)
//
// TestRegisterOpsFlagsDoesNotParse pins the R1 mechanical-move guarantee that
// the flag-registration extraction kept the single flag.Parse() in the
// composition root (parseFlags) and did not strand a second parse inside the
// last registrar. The original gateway main called flag.Parse() exactly once;
// the divergence this step corrects left registerOpsFlags ending in its own
// flag.Parse() while parseFlags also parsed, so the command line was parsed
// twice. A registrar only registers flags, so after registerOpsFlags runs on a
// fresh, unparsed flag set the set must still report Parsed() == false. Against
// the pre-fix code (registerOpsFlags called flag.Parse()) this assertion fails:
// the fresh set would report Parsed() == true. parseFlags' own single Parse is
// covered by TestParseFlagsPopulatesEveryField.
func TestRegisterOpsFlagsDoesNotParse(t *testing.T) {
	// Isolate the global flag.CommandLine (the register helpers register on it)
	// behind a fresh ContinueOnError set with no command-line args, so the call
	// neither redefines a flag on the production set nor exits the test binary.
	savedCmdLine := flag.CommandLine
	savedArgs := os.Args
	t.Cleanup(func() {
		flag.CommandLine = savedCmdLine
		os.Args = savedArgs
	})
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	os.Args = []string{savedArgs[0]}

	f := &gatewayFlags{}
	f.registerOpsFlags()

	if flag.CommandLine.Parsed() {
		t.Fatal("registerOpsFlags parsed the command line: a registrar must only " +
			"register flags; the single flag.Parse() belongs in parseFlags so the " +
			"command line is parsed exactly once (proposal 0020 R1 mechanical move)")
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
