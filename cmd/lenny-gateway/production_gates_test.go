// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// spec: §17.9.1 (Composition Dimensions) "Production mode"; §11.7 (Audit Logging)
//
// TestSIEMAndPgauditGateFatalOnlyOnMessageInProductionMode_spec_11_7 pins the
// regulated-tenant SIEM and pgaudit refusals: each is fatal only in production
// mode and only for its own startup message. A non-matching error (a failed
// tenant listing) under production is not fatal, which keeps the "could not
// list tenants" branch.
func TestSIEMAndPgauditGateFatalOnlyOnMessageInProductionMode_spec_11_7(t *testing.T) {
	listing := errors.New("tenant store unavailable")
	for _, g := range []struct {
		name string
		fn   func(bool, error) bool
		msg  string
	}{
		{"siemGateFatal", siemGateFatal, admin.SIEMStartupFatalMessage},
		{"pgauditGateFatal", pgauditGateFatal, admin.PgauditStartupFatalMessage},
	} {
		for _, tc := range []struct {
			production bool
			err        error
			want       bool
		}{
			{true, errors.New(g.msg), true},
			{false, errors.New(g.msg), false},
			{true, listing, false},
			{false, listing, false},
			{true, nil, false},
			{false, nil, false},
		} {
			if got := g.fn(tc.production, tc.err); got != tc.want {
				t.Errorf("%s(%v, %v) = %v, want %v", g.name, tc.production, tc.err, got, tc.want)
			}
		}
	}
	// Each gate matches only its own message.
	if siemGateFatal(true, errors.New(admin.PgauditStartupFatalMessage)) {
		t.Error("siemGateFatal is fatal on the pgaudit message")
	}
	if pgauditGateFatal(true, errors.New(admin.SIEMStartupFatalMessage)) {
		t.Error("pgauditGateFatal is fatal on the SIEM message")
	}
}

// spec: §17.9.1 (Composition Dimensions) "Production mode"; §11.7 (Audit Logging)
//
// TestPgauditPreflightFatalNeedsProductionAndRegulatedTenant_spec_11_7 pins
// the pgaudit extension preflight refusal: fatal only in production mode with
// a regulated tenant present.
func TestPgauditPreflightFatalNeedsProductionAndRegulatedTenant_spec_11_7(t *testing.T) {
	for _, tc := range []struct {
		production, regulatedPresent, want bool
	}{
		{true, true, true},
		{true, false, false},
		{false, true, false},
		{false, false, false},
	} {
		if got := pgauditPreflightFatal(tc.production, tc.regulatedPresent); got != tc.want {
			t.Errorf("pgauditPreflightFatal(%v, %v) = %v, want %v", tc.production, tc.regulatedPresent, got, tc.want)
		}
	}
}

// spec: §17.9.1 (Composition Dimensions) "Production mode"; §11.7 (Audit Logging); §12.3 (Postgres HA Requirements)
//
// TestAuditIntegrityFatalNeedsProductionAndError_spec_12_3 pins the
// append-only ledger integrity refusal: fatal only in production mode with a
// verification error; a nil error under production starts.
func TestAuditIntegrityFatalNeedsProductionAndError_spec_12_3(t *testing.T) {
	violation := errors.New("ledger trigger missing")
	for _, tc := range []struct {
		production bool
		err        error
		want       bool
	}{
		{true, violation, true},
		{true, nil, false},
		{false, violation, false},
		{false, nil, false},
	} {
		if got := auditIntegrityFatal(tc.production, tc.err); got != tc.want {
			t.Errorf("auditIntegrityFatal(%v, %v) = %v, want %v", tc.production, tc.err, got, tc.want)
		}
	}
}

// spec: §17.9.1 (Composition Dimensions) "Production mode"; §11.7 (Audit Logging); §12.3 (Postgres HA Requirements); §16.4 (Logging); §16.5 (Alerting Rules and SLOs)
//
// TestGatewayProductionGatesReadFinalizedEnvironment_spec_17_9_1 pins the
// wiring of every gateway production gate to the finalized --environment
// value: each build step computes production through deployenv.IsProduction
// and passes it as the first argument of each gate, and no build step reads
// LENNY_ENV directly. A call site that drops the gate or feeds it another
// value fails here; the decision-function tables cannot detect that.
func TestGatewayProductionGatesReadFinalizedEnvironment_spec_17_9_1(t *testing.T) {
	const decl = "production := deployenv.IsProduction(f.kmsOpts.Environment)"
	space := regexp.MustCompile(`\s+`)
	for file, calls := range map[string][]string{
		"billing_pipeline.go": {
			"siemGateFatal(production, err)",
			"pgauditGateFatal(production, err)",
			"pgauditPreflightFatal(production, regulatedPresent)",
		},
		"stores.go": {
			"auditIntegrityFatal(production, err)",
		},
		"metricsbackfill.go": {
			"gwMetrics.SetEnvProduction(production)",
			"auditBatchingNoSIEM(production, *auditBatchingEnabled, *auditSIEMEndpoint != \"\")",
		},
	} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src := space.ReplaceAllString(string(b), " ")
		for _, want := range append([]string{decl}, calls...) {
			if !strings.Contains(src, want) {
				t.Errorf("%s: production gate not wired to the finalized environment (%q missing)", file, want)
			}
		}
		if strings.Contains(src, `"LENNY_ENV"`) {
			t.Errorf("%s reads LENNY_ENV directly; production mode comes from the finalized --environment value", file)
		}
	}
}
