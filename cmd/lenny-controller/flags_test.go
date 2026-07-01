// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"os"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/controller/ratelimit"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
)

// spec: 4.1 (controller composition root parses its inputs once), 4.6.1
// (the lenny-controller flag surface)
//
// TestParseFlagsPopulatesEveryControllerDefault pins the R8 composition-root
// decomposition's flag seam (proposal 0020 §4 Part A R8). parseFlags splits its
// flag definitions across the per-domain register helpers (registerServerFlags,
// registerPodIdentityFlags, registerRateLimitFlags, registerLifecycleFlags, and
// registerCertFlags). The R8 move introduced that delegation, so a flag binding
// accidentally omitted from one helper — or a helper omitted from parseFlags'
// call sequence — would silently change a runtime default: the corresponding
// controllerFlags field would stay at its Go zero value instead of the spec
// default the operator and chart depend on.
//
// This test mirrors cmd/lenny-gateway's TestParseFlagsPopulatesEveryField. It
// runs parseFlags against an isolated, empty command line (no env, no args) and
// asserts that every field with a documented non-zero default holds that
// default after the parse. Against a regression that drops a binding from a
// register helper (or drops a helper from parseFlags) the field reverts to its
// zero value and the matching assertion fails. The env-/arg-derived string
// fields (postgres-dsn, redis-url, gateway-grpc-addr, ...) legitimately default
// to empty here and are exercised by the wiring characterization rather than
// asserted as non-empty.
//
// parseFlags parses the process-global flag.CommandLine, so the test isolates
// it behind a fresh ContinueOnError set with no args and restores it on
// cleanup; no other test in this package parses the global set, so the single
// call cannot trigger a flag-redefinition panic.
func TestParseFlagsPopulatesEveryControllerDefault(t *testing.T) {
	savedCmdLine := flag.CommandLine
	savedArgs := os.Args
	t.Cleanup(func() {
		flag.CommandLine = savedCmdLine
		os.Args = savedArgs
	})
	flag.CommandLine = flag.NewFlagSet(savedArgs[0], flag.ContinueOnError)
	os.Args = []string{savedArgs[0]}
	clearControllerEnv(t)

	f := parseFlags()
	if f == nil {
		t.Fatal("parseFlags returned nil")
	}

	// Every field below carries a documented non-zero default fixed in code (a
	// constant or a literal), so the chart need not pass the flag to get the
	// spec behavior. A register helper that drops the binding leaves the Go
	// zero value, which differs from each default here and fails the
	// assertion. The defaults trace to the flag declarations in flags.go.
	checks := []struct {
		name string
		got  any
		want any
	}{
		// registerServerFlags
		{"metricsAddr", f.metricsAddr, ":8080"},
		{"probeAddr", f.probeAddr, ":8081"},
		{"leaderElect", f.leaderElect, false},
		{"leaderElectNS", f.leaderElectNS, "lenny-system"},
		// registerPodIdentityFlags (§13.1 non-root UID/GID defaults)
		{"adapterUID", f.adapterUID, podspec.AdapterUID},
		{"agentUID", f.agentUID, podspec.AgentUID},
		{"credReadersGID", f.credReadersGID, podspec.CredReadersGID},
		// registerRateLimitFlags (§4.6.1 rate-limiter + concurrency knobs)
		{"createQPS", f.createQPS, ratelimit.DefaultCreateQPS},
		{"createBurst", f.createBurst, ratelimit.DefaultCreateBurst},
		{"statusQPS", f.statusQPS, ratelimit.DefaultStatusQPS},
		{"statusBurst", f.statusBurst, ratelimit.DefaultStatusBurst},
		{"maxConcurrentReconciles", f.maxConcurrentReconciles, 1},
		// registerLifecycleFlags (§4.6.1 fill grace / dedup / GC / queue knobs)
		{"initialFillGrace", f.initialFillGrace, 120 * time.Second},
		{"statusDedupWindow", f.statusDedupWindow, 500 * time.Millisecond},
		{"claimOrphanTimeout", f.claimOrphanTimeout, 5 * time.Minute},
		{"reservedHoldGrace", f.reservedHoldGrace, 60 * time.Second},
		{"workqueueMaxDepth", f.workqueueMaxDepth, 500},
		{"devMode", f.devMode, false},
		// registerCertFlags (§4.6.1 / §10.3 idle-pod cert-expiry window)
		{"certTTL", f.certTTL, 4 * time.Hour},
		{"certExpiryThreshold", f.certExpiryThreshold, 30 * time.Minute},
		{"certIssuanceGrace", f.certIssuanceGrace, 60 * time.Second},
		{"requireCertIssuance", f.requireCertIssuance, false},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("controllerFlags.%s = %v, want %v: a register helper dropped its binding, "+
				"or parseFlags omitted the helper, so the spec default is no longer wired (proposal 0020 R8)",
				c.name, c.got, c.want)
		}
	}
}

// clearControllerEnv unsets every environment variable parseFlags reads as a
// flag default so the parse observes the in-code defaults rather than a value
// the surrounding shell happens to export. The list mirrors the os.Getenv calls
// in flags.go.
func clearControllerEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"LENNY_GATEWAY_GRPC_ADDR",
		"LENNY_SA_TOKEN_AUDIENCE",
		"LENNY_AGENT_SERVICE_ACCOUNT",
		"LENNY_DEDICATED_DNS_CLUSTER_IP",
		"LENNY_STANDARD_RUNTIME_CLASS",
		"LENNY_SANDBOXED_RUNTIME_CLASS",
		"LENNY_MICROVM_RUNTIME_CLASS",
		"LENNY_ADAPTER_UID",
		"LENNY_AGENT_UID",
		"LENNY_CRED_READERS_GID",
		"LENNY_EGRESS_CAPTURE_IMAGE",
		"LENNY_POSTGRES_DSN",
		"LENNY_AGENT_NAMESPACES",
		"LENNY_REDIS_URL",
		"LENNY_REDIS_PASSWORD",
		"LENNY_DEV_MODE",
	} {
		t.Setenv(key, "")
	}
}
