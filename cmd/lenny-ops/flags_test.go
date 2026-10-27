// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/kms/providerflags"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// spec: 4.1 (gateway and sibling-binary subsystem seams), 25.4 (lenny-ops
// flag surface)
//
// TestParseFlags pins the R4 composition-root decomposition (proposal 0020
// §4 Part A R4): parseFlags splits its flag definitions across the per-domain
// register helpers (registerCoreFlags, registerBackupFlags,
// registerEventFlags, registerUpgradeFlags, registerObservabilityFlags,
// registerAuthFlags, registerLockFlags, registerWebhookFlags, and
// registerGatewayClientFlags) and assembles them into one opsFlags value the
// runOps composition root threads to every build step. Against the pre-R4
// code the flags were defined as locals inside main, so opsFlags did not
// exist; this test pins the post-refactor structure and the §25.4 defaults
// the behavior-preserving block move had to keep intact.
//
// parseFlags mutates the process-global flag.CommandLine, so it must be
// called exactly once per process. This is the only test in the package that
// calls it (and no other test defines flags on the global set), so the single
// call cannot trigger a flag-redefinition panic. The two sub-checks reuse the
// one parseFlags result.
func TestParseFlags(t *testing.T) {
	f := parseFlags()
	if f == nil {
		t.Fatal("parseFlags returned nil")
	}

	// Every flag-pointer field must be non-nil: a dropped flag, or a register
	// helper omitted from parseFlags' call sequence, leaves its field nil and
	// the build steps under runOps would dereference a flag the composition
	// root never registered.
	v := reflect.ValueOf(f).Elem()
	tp := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		name := tp.Field(i).Name
		if field.Kind() == reflect.Pointer && field.IsNil() {
			t.Errorf("opsFlags.%s is nil after parseFlags: a register helper "+
				"dropped its flag, or parseFlags omitted the helper", name)
		}
	}

	// A representative default from each register group must read back
	// unchanged. A default silently altered during the move (a wrong addr, a
	// flipped boolean, a dropped envOr fallback) fails here.
	checks := []struct {
		name string
		got  any
		want any
	}{
		// registerCoreFlags
		{"addr", *f.addr, ":8090"},
		{"leaderElectNS", *f.leaderElectNS, "lenny-system"},
		{"agentNamespace", *f.agentNamespace, "lenny-system"},
		{"runbookDir", *f.runbookDir, "docs/runbooks"},
		{"shutdownTimeout", *f.shutdownTimeout, 10 * time.Second},
		// registerBackupFlags
		{"doctorFixTimeout", *f.doctorFixTimeout, 120},
		{"doctorRenderDir", *f.doctorRenderDir, ""},
		{"backupMinIOBucket", *f.backupMinIOBucket, "lenny-backups"},
		// registerEventFlags
		{"selfHealthInterval", *f.selfHealthInterval, 10 * time.Second},
		{"webhookRetentionDays", *f.webhookRetentionDays, 7},
		// registerUpgradeFlags
		{"registryURL", *f.registryURL, "ghcr.io/lennylabs"},
		{"opsRollTimeout", *f.opsRollTimeout, 600},
		{"gatewayRollTimeout", *f.gatewayRollTimeout, 1200},
		{"controllerRollTimeout", *f.controllerRollTimeout, 600},
		// registerObservabilityFlags
		{"pgauditTenantID", *f.pgauditTenantID, "platform"},
		{"driftHelmValuesKey", *f.driftHelmValuesKey, "values.yaml"},
		// registerAuthFlags
		{"escalationReconcileWPS", *f.escalationReconcileWPS, 20},
		{"rateLimitRPS", *f.rateLimitRPS, opsserver.DefaultRateLimitRPS},
		{"rateLimitBurst", *f.rateLimitBurst, opsserver.DefaultRateLimitBurst},
		// registerLockFlags
		{"locksMemoryTier", *f.locksMemoryTier, string(coordination.MemoryTierSingleReplicaOnly)},
		{"opsServiceName", *f.opsServiceName, "lenny-ops"},
		// registerWebhookFlags
		{"webhookAllowHTTP", *f.webhookAllowHTTP, false},
		// registerGatewayClientFlags
		{"gatewayTLSPort", *f.gatewayTLSPort, 8443},
		{"gatewayPlaintextPort", *f.gatewayPlaintextPort, 8080},
		{"gatewayFanOutTimeout", *f.gatewayFanOutTimeout, 2 * time.Second},
		{"gatewayBreakerThreshold", *f.gatewayBreakerThreshold, 3},
		{"gatewayBreakerResetAfter", *f.gatewayBreakerResetAfter, 60 * time.Second},
		{"gatewaySATokenFile", *f.gatewaySATokenFile, "/var/run/secrets/lenny/gateway/token"},
		{"gatewayTokenRefreshBefore", *f.gatewayTokenRefreshBefore, 5 * time.Minute},
		{"alertingBundleFormats", *f.alertingBundleFormats, "prometheusrule"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("parseFlags default for %s = %v, want %v", c.name, c.got, c.want)
		}
	}

	// §17.9.1: production mode has one carrier, --environment. lenny-ops
	// registers no separate production flag that could disagree with it.
	if fl := flag.Lookup("production"); fl != nil {
		t.Errorf("lenny-ops registers --production; production mode comes only from --environment (§17.9.1)")
	}
	if f.productionMode() {
		t.Errorf("productionMode() = true with --environment unset, want false")
	}
	if f.idempotencyKeyRequired() {
		t.Errorf("idempotencyKeyRequired() = true with --platform-tier unset, want false")
	}
}

// boundOpsFlags parses args through the providerflags carrier on a fresh
// flag set, runs its finalizer, and returns the opsFlags that the
// production-mode and required-key readers consult.
func boundOpsFlags(t *testing.T, tier string, args ...string) *opsFlags {
	t.Helper()
	fs := flag.NewFlagSet("lenny-ops", flag.ContinueOnError)
	opts, finalize := providerflags.Bind(fs, func(string) string { return "" }, providerflags.Options{
		Provider: providerflags.ProviderLocal,
	})
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	if err := finalize(); err != nil {
		t.Fatalf("finalize %v: %v", args, err)
	}
	return &opsFlags{kmsOpts: opts, platformTier: &tier}
}

// spec: §17.9.1 (Composition Dimensions) "Production mode"; §25.4 (The lenny-ops Service) Idempotency-Key; §25.11
//
// TestOpsProductionModeAndRequiredKeyAreIndependent_spec_17_9_1 pins that
// lenny-ops derives production mode from the finalized --environment value
// alone and the §25.4 required Idempotency-Key from --platform-tier alone.
// A tier2 install outside production mode requires the key, and a tier1
// install in production mode does not; staging is not production mode.
func TestOpsProductionModeAndRequiredKeyAreIndependent_spec_17_9_1(t *testing.T) {
	for _, tc := range []struct {
		name         string
		tier         string
		args         []string
		wantProd     bool
		wantRequired bool
	}{
		{"prod tier1", "tier1", []string{"--environment=prod"}, true, false},
		{"staging tier3", "tier3", []string{"--environment=staging"}, false, true},
		{"dev tier2", "tier2", []string{"--environment=dev"}, false, true},
		{"local", "", []string{"--environment=local"}, false, false},
		{"unset environment and tier", "", nil, false, false},
		{"off-set tier reads as tier1", "Tier2", []string{"--environment=prod"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := boundOpsFlags(t, tc.tier, tc.args...)
			if got := f.productionMode(); got != tc.wantProd {
				t.Errorf("productionMode() = %v, want %v", got, tc.wantProd)
			}
			if got := f.idempotencyKeyRequired(); got != tc.wantRequired {
				t.Errorf("idempotencyKeyRequired() = %v, want %v", got, tc.wantRequired)
			}
		})
	}
}

// spec: §17.9.1 (Composition Dimensions) "Production mode"; §25.4 (The lenny-ops Service) Idempotency-Key; §25.11; §17.4 "Dev mode guard rails"
//
// TestOpsProductionGatesReadFinalizedEnvironment_spec_17_9_1 pins the wiring
// of every lenny-ops production-keyed behavior to productionMode() and of the
// §25.4 required-key posture to idempotencyKeyRequired(). A call site that
// feeds a gate another value fails here; the reader tables above cannot
// detect that.
func TestOpsProductionGatesReadFinalizedEnvironment_spec_17_9_1(t *testing.T) {
	space := regexp.MustCompile(`\s+`)
	for file, wants := range map[string][]string{
		"httpsurface.go": {
			"production: w.f.productionMode(),",
			"Production: w.f.productionMode(),",
			"IdempotencyKeyRequired: w.f.idempotencyKeyRequired(),",
		},
		"services_wiring.go": {
			"buildBackupService(w.f.productionMode(),",
		},
		"keyset.go": {
			"checkDevTrustPosture(*w.f.devMode, w.f.productionMode(),",
		},
	} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src := space.ReplaceAllString(string(b), " ")
		for _, want := range wants {
			if !strings.Contains(src, want) {
				t.Errorf("%s: production-keyed behavior not wired as expected (%q missing)", file, want)
			}
		}
		if strings.Contains(src, `"LENNY_PRODUCTION"`) {
			t.Errorf("%s reads LENNY_PRODUCTION; production mode comes from --environment", file)
		}
	}
}
