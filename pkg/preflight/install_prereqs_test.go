// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// spec: §17.6 line 503. F-17.6.1.
func TestKubernetesVersionCheck_spec_17_6_503(t *testing.T) {
	tests := []struct {
		name       string
		version    string
		wantPassed bool
		wantSubstr string
	}{
		{"empty skips", "", true, "SKIPPED"},
		{"unparseable is advisory", "garbage", true, "WARNING"},
		{"below minimum fails", "v1.26.9", false, "minimum 1.27"},
		{"exactly minimum passes", "v1.27.0", true, "satisfies"},
		{"above minimum passes", "v1.29.4", true, "satisfies"},
		{"distro suffix on minor parses", "v1.27.6+vmware.1", true, "satisfies"},
		{"gke plus-suffix minor parses", "v1.28.5-gke.1217000", true, "satisfies"},
		{"major below one fails", "v0.99.0", false, "unsupported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := KubernetesVersionCheck{Version: tt.version}.Decide()
			if d.Passed != tt.wantPassed {
				t.Fatalf("Passed=%v want %v (%s)", d.Passed, tt.wantPassed, d.Reason)
			}
			if tt.wantSubstr != "" && !strings.Contains(d.Reason, tt.wantSubstr) {
				t.Fatalf("reason %q missing %q", d.Reason, tt.wantSubstr)
			}
		})
	}
}

// spec: §17.6 line 517. F-17.6.1.
func TestSIEMEndpointCheck_spec_17_6_517(t *testing.T) {
	// Production with no endpoint warns (still passes — non-blocking).
	d := SIEMEndpointCheck{Environment: "prod", SIEMEndpoint: ""}.Decide()
	if !d.Passed || !strings.Contains(d.Reason, "audit.siem.endpoint is not configured") {
		t.Fatalf("prod-no-siem: want non-blocking warning, got passed=%v reason=%q", d.Passed, d.Reason)
	}
	// "production" spelling matches LENNY_ENV=production too.
	if d := (SIEMEndpointCheck{Environment: "production"}).Decide(); d.Reason == "" {
		t.Fatalf("production spelling should warn")
	}
	// Production with an endpoint: silent pass.
	if d := (SIEMEndpointCheck{Environment: "prod", SIEMEndpoint: "https://siem.acme:443"}).Decide(); !d.Passed || d.Reason != "" {
		t.Fatalf("prod-with-siem: want silent pass, got reason=%q", d.Reason)
	}
	// Non-production: no warning regardless of endpoint.
	if d := (SIEMEndpointCheck{Environment: "dev"}).Decide(); !d.Passed || d.Reason != "" {
		t.Fatalf("dev: want silent pass, got reason=%q", d.Reason)
	}
}

// spec: §17.6 line 504; §12.6. F-17.6.1.
func TestStorageRouterRegionCoverageCheck_spec_17_6_504(t *testing.T) {
	if d := (StorageRouterRegionCoverageCheck{}).Decide(); !d.Passed || !strings.Contains(d.Reason, "SKIPPED") {
		t.Fatalf("empty regions should skip, got passed=%v reason=%q", d.Passed, d.Reason)
	}
	complete := StorageRouterRegionCoverageCheck{Regions: []StorageRouterRegion{
		{Name: "us-east-1", HasPostgres: true, HasObjectStorage: true},
		{Name: "eu-west-1", HasPostgres: true, HasObjectStorage: true},
	}}
	if d := complete.Decide(); !d.Passed {
		t.Fatalf("complete regions should pass: %s", d.Reason)
	}
	partial := StorageRouterRegionCoverageCheck{Regions: []StorageRouterRegion{
		{Name: "us-east-1", HasPostgres: true, HasObjectStorage: false},
	}}
	d := partial.Decide()
	if d.Passed || !strings.Contains(d.Reason, "us-east-1") || !strings.Contains(d.Reason, "object storage") {
		t.Fatalf("partial region should fail naming the gap, got passed=%v reason=%q", d.Passed, d.Reason)
	}
}

// spec: §17.6 line 505; §12.8 Phase 3.5. F-17.6.1.
func TestLegalHoldEscrowCheck_spec_17_6_505(t *testing.T) {
	if d := (LegalHoldEscrowCheck{Enabled: false}).Decide(); !d.Passed || !strings.Contains(d.Reason, "SKIPPED") {
		t.Fatalf("disabled should skip, got %q", d.Reason)
	}
	if d := (LegalHoldEscrowCheck{Enabled: true}).Decide(); !d.Passed {
		t.Fatalf("no regions should skip-pass: %s", d.Reason)
	}
	ok := LegalHoldEscrowCheck{
		Enabled:       true,
		Regions:       []string{"us-east-1", "eu-west-1"},
		EscrowRegions: map[string]bool{"us-east-1": true, "eu-west-1": true},
	}
	if d := ok.Decide(); !d.Passed {
		t.Fatalf("all regions covered should pass: %s", d.Reason)
	}
	missing := LegalHoldEscrowCheck{
		Enabled:       true,
		Regions:       []string{"us-east-1", "eu-west-1"},
		EscrowRegions: map[string]bool{"us-east-1": true},
	}
	d := missing.Decide()
	if d.Passed || !strings.Contains(d.Reason, "eu-west-1") {
		t.Fatalf("missing escrow region should fail naming it, got passed=%v reason=%q", d.Passed, d.Reason)
	}
}

type fakePgBouncerProber struct {
	mode     string
	sentinel bool
	err      error
}

func (f fakePgBouncerProber) ProbePgBouncer(context.Context) (string, bool, error) {
	return f.mode, f.sentinel, f.err
}

// spec: §17.6 lines 487-488. F-17.6.1.
func TestPgBouncerConfigCheck_spec_17_6_487(t *testing.T) {
	ctx := context.Background()
	if d := (PgBouncerConfigCheck{}).Decide(ctx); !d.Passed || !strings.Contains(d.Reason, "SKIPPED") {
		t.Fatalf("nil prober should skip, got %q", d.Reason)
	}
	if d := (PgBouncerConfigCheck{Prober: fakePgBouncerProber{mode: "transaction", sentinel: true}}).Decide(ctx); !d.Passed {
		t.Fatalf("transaction+sentinel should pass: %s", d.Reason)
	}
	if d := (PgBouncerConfigCheck{Prober: fakePgBouncerProber{mode: "session", sentinel: true}}).Decide(ctx); d.Passed || !strings.Contains(d.Reason, "pool_mode") {
		t.Fatalf("session pool_mode should fail, got %q", d.Reason)
	}
	if d := (PgBouncerConfigCheck{Prober: fakePgBouncerProber{mode: "transaction", sentinel: false}}).Decide(ctx); d.Passed || !strings.Contains(d.Reason, "sentinel") {
		t.Fatalf("missing sentinel should fail, got %q", d.Reason)
	}
	if d := (PgBouncerConfigCheck{Prober: fakePgBouncerProber{err: errors.New("conn refused")}}).Decide(ctx); d.Passed || !strings.Contains(d.Reason, "conn refused") {
		t.Fatalf("prober error should fail, got %q", d.Reason)
	}
}

type fakeBillingProber struct {
	enabled bool
	err     error
}

func (f fakeBillingProber) ProbeBillingTriggers(context.Context) (bool, error) {
	return f.enabled, f.err
}

// spec: §17.6 line 489. F-17.6.1.
func TestBillingTriggerCheck_spec_17_6_489(t *testing.T) {
	ctx := context.Background()
	if d := (BillingTriggerCheck{}).Decide(ctx); !d.Passed || !strings.Contains(d.Reason, "SKIPPED") {
		t.Fatalf("nil prober should skip, got %q", d.Reason)
	}
	if d := (BillingTriggerCheck{Prober: fakeBillingProber{enabled: true}}).Decide(ctx); !d.Passed {
		t.Fatalf("enabled trigger should pass: %s", d.Reason)
	}
	if d := (BillingTriggerCheck{Prober: fakeBillingProber{enabled: false}}).Decide(ctx); d.Passed || !strings.Contains(d.Reason, "disabled") {
		t.Fatalf("disabled trigger should fail, got %q", d.Reason)
	}
	if d := (BillingTriggerCheck{Prober: fakeBillingProber{err: errors.New("no perms")}}).Decide(ctx); d.Passed || !strings.Contains(d.Reason, "no perms") {
		t.Fatalf("prober error should fail, got %q", d.Reason)
	}
}
