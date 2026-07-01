// SPDX-License-Identifier: MIT

package main

import (
	"reflect"
	"testing"
	"time"

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
		{"production", *f.production, false},
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
}
