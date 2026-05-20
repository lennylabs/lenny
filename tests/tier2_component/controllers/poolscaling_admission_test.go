//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §4.6.2 / §12.2.4 Pool Scaling Controller
// admission-retry harness and the §16.5 PoolScalingAdmissionStuck
// alert wiring. Exercises pkg/controller/poolscaling at the public
// API surface: confirm the catalog row binds the alert PromQL to the
// counter the retry harness writes.
package controllers_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/alerting/rules"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
)

// spec: §4.6.2 / §16.5
// diagnosis: §16.5 binds the PoolScalingAdmissionStuck alert to the
// lenny_pool_scaling_admission_denied_total counter the §4.6.2
// admission-retry harness increments on every admission rejection.
// This test asserts the catalog row exists, references the counter,
// and that the harness exposes the retry ceiling so downstream
// alerting integrations cannot silently break the wiring.
func TestPoolScalingController(t *testing.T) {
	t.Parallel()

	var stuck *rules.Rule
	for i := range rules.Catalog() {
		r := rules.Catalog()[i]
		if r.Name == "PoolScalingAdmissionStuck" {
			stuck = &r
			break
		}
	}
	if stuck == nil {
		t.Fatalf("PoolScalingAdmissionStuck rule missing from §16.5 catalog")
	}
	if !strings.Contains(stuck.Expr, "lenny_pool_scaling_admission_denied_total") {
		t.Errorf("rule Expr = %q, must reference lenny_pool_scaling_admission_denied_total", stuck.Expr)
	}
	if stuck.SpecRef != "§16.5" {
		t.Errorf("rule SpecRef = %q, want §16.5", stuck.SpecRef)
	}
	if stuck.Description == "" {
		t.Errorf("rule Description is empty; on-call needs the stuck-pool context")
	}
	if stuck.Severity != rules.SeverityWarning && stuck.Severity != rules.SeverityCritical {
		t.Errorf("rule Severity = %q, want warning or critical", stuck.Severity)
	}

	// The retry ceiling defaults to a non-zero value so a
	// misconfigured runner cannot silently disable the gate. The
	// scaling formula and the per-pool unit suites (admission_retry_test.go,
	// strategy/*_test.go) pin the underlying behavior; this component
	// row pins the public defaults the catalog row depends on.
	if poolscaling.DefaultAdmissionDeniedRetryCeiling <= 0 {
		t.Errorf("DefaultAdmissionDeniedRetryCeiling = %d, want > 0", poolscaling.DefaultAdmissionDeniedRetryCeiling)
	}
}
