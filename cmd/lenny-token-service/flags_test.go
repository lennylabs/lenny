// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"os"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/kms/providerflags"
)

// spec: 4.1 (the Token Service composition root parses its inputs once),
// 13.3 (the lenny-token-service flag surface)
//
// TestParseFlagsPopulatesEveryDefault pins the R11 composition-root
// decomposition's flag seam (proposal 0020 §4 Part A R11). parseFlags splits its
// flag definitions across the per-domain register helpers (registerListenerFlags,
// registerIssuerAndStateFlags, registerRateLimitFlags, registerTLSFlags, and
// registerKMSFlags). The R11 move introduced that delegation, so a flag binding
// accidentally omitted from one helper — or a helper omitted from parseFlags'
// call sequence — would silently change a runtime default: the corresponding
// tokenServiceFlags field would stay nil (a never-bound pointer) or, for a bound
// flag, hold its Go zero value instead of the §13.3 default the operator and
// chart depend on.
//
// This test mirrors cmd/lenny-controller's TestParseFlagsPopulatesEveryControllerDefault
// and cmd/lenny-ops's TestParseFlags. It runs parseFlags against an isolated,
// empty command line (no env, no args) and asserts that every flag pointer is
// non-nil and that every field with a documented non-zero default holds that
// default after the parse. Against a regression that drops a binding from a
// register helper (or drops a helper from parseFlags) the pointer stays nil and
// the nil-pointer check fails, or the value reverts to its zero value and the
// matching default assertion fails. The env-/arg-derived string fields
// (postgres-dsn, redis-url, tls-cert, ...) legitimately default to empty here
// and are exercised by the binary characterization (main_test.go) rather than
// asserted as non-empty.
//
// parseFlags parses the process-global flag.CommandLine, so the test isolates
// it behind a fresh ContinueOnError set with no args and restores it on
// cleanup; no other test in this package parses the global set, so the single
// call cannot trigger a flag-redefinition panic.
func TestParseFlagsPopulatesEveryDefault(t *testing.T) {
	savedCmdLine := flag.CommandLine
	savedArgs := os.Args
	t.Cleanup(func() {
		flag.CommandLine = savedCmdLine
		os.Args = savedArgs
	})
	flag.CommandLine = flag.NewFlagSet(savedArgs[0], flag.ContinueOnError)
	os.Args = []string{savedArgs[0]}
	clearTokenServiceEnv(t)

	f := parseFlags()
	if f == nil {
		t.Fatal("parseFlags returned nil")
	}

	// Every flag pointer must be non-nil: a register helper dropped from
	// parseFlags, or a binding omitted from a helper, leaves the pointer nil,
	// which the composition root would dereference (panic) or read as the zero
	// value. The KMS option set and finalize hook are bound by
	// registerKMSFlags via providerflags.Bind.
	nilChecks := []struct {
		name  string
		isNil bool
	}{
		{"addr", f.addr == nil},
		{"grpcAddr", f.grpcAddr == nil},
		{"metricsAddr", f.metricsAddr == nil},
		{"issuer", f.issuer == nil},
		{"postgresDSN", f.postgresDSN == nil},
		{"redisURL", f.redisURL == nil},
		{"redisPassword", f.redisPassword == nil},
		{"rlCallerPerSec", f.rlCallerPerSec == nil},
		{"rlCallerPerMin", f.rlCallerPerMin == nil},
		{"rlTenantPerSec", f.rlTenantPerSec == nil},
		{"rlSampleWindow", f.rlSampleWindow == nil},
		{"tlsCert", f.tlsCert == nil},
		{"tlsKey", f.tlsKey == nil},
		{"tlsCA", f.tlsCA == nil},
		{"secretNamespace", f.secretNamespace == nil},
		{"kmsOpts", f.kmsOpts == nil},
		{"kmsFinalize", f.kmsFinalize == nil},
	}
	for _, c := range nilChecks {
		if c.isNil {
			t.Errorf("tokenServiceFlags.%s is nil: a register helper dropped its binding, "+
				"or parseFlags omitted the helper (proposal 0020 R11)", c.name)
		}
	}

	// Every field below carries a documented non-zero default fixed in code (a
	// constant or a literal), so the chart need not pass the flag to get the
	// §13.3 behavior. A register helper that drops the binding leaves the Go
	// zero value, which differs from each default here and fails the
	// assertion. The defaults trace to the flag declarations in flags.go.
	checks := []struct {
		name string
		got  any
		want any
	}{
		// registerListenerFlags
		{"addr", *f.addr, ":8081"},
		{"grpcAddr", *f.grpcAddr, ""},
		{"metricsAddr", *f.metricsAddr, ""},
		// registerIssuerAndStateFlags (§13.3 issuer, §4.9 probe namespace)
		{"issuer", *f.issuer, "https://lenny.dev.local/token"},
		{"secretNamespace", *f.secretNamespace, "lenny-system"},
		// registerRateLimitFlags (§13.3 line 607 normative limits)
		{"rlCallerPerSec", *f.rlCallerPerSec, 10},
		{"rlCallerPerMin", *f.rlCallerPerMin, 300},
		{"rlTenantPerSec", *f.rlTenantPerSec, 100},
		{"rlSampleWindow", *f.rlSampleWindow, 10 * time.Second},
		// registerKMSFlags (§4 / §17.5 KMS provider selector; local default)
		{"kmsProvider", f.kmsOpts.Provider, providerflags.ProviderLocal},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("tokenServiceFlags.%s = %v, want %v: a register helper dropped its binding, "+
				"or parseFlags omitted the helper, so the §13.3 default is no longer wired (proposal 0020 R11)",
				c.name, c.got, c.want)
		}
	}
}

// clearTokenServiceEnv unsets every environment variable parseFlags reads as a
// flag default so the parse observes the in-code defaults rather than a value
// the surrounding shell happens to export. The list mirrors the os.Getenv calls
// in flags.go (and the providerflags env reads registerKMSFlags passes through).
func clearTokenServiceEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"LENNY_TOKEN_ISSUER",
		"LENNY_POSTGRES_DSN",
		"LENNY_REDIS_URL",
		"LENNY_REDIS_PASSWORD",
		"LENNY_OAUTH_RL_CALLER_PER_SECOND",
		"LENNY_OAUTH_RL_CALLER_PER_MINUTE",
		"LENNY_OAUTH_RL_TENANT_PER_SECOND",
		"LENNY_OAUTH_RL_SAMPLE_WINDOW",
		"LENNY_TOKEN_SERVICE_TLS_CERT",
		"LENNY_TOKEN_SERVICE_TLS_KEY",
		"LENNY_TOKEN_SERVICE_CA",
		"POD_NAMESPACE",
		// providerflags.Bind env reads (registerKMSFlags).
		"LENNY_KMS_PROVIDER",
		"LENNY_ENV",
	} {
		t.Setenv(key, "")
	}
}
