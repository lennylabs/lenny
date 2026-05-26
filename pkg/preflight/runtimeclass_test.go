// SPDX-License-Identifier: MIT

package preflight_test

import (
	"context"
	"strings"
	"testing"

	nodev1 "k8s.io/api/node/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/preflight"
)

// TestCheckRuntimeClasses_spec_5_3_676 covers the §5.3 line 676 required
// RuntimeClass presence decision: all present passes, any absent fails
// fail-closed with the actionable message, and the failure list is
// deduplicated.
func TestCheckRuntimeClasses_spec_5_3_676(t *testing.T) {
	cases := []struct {
		name       string
		required   []preflight.RuntimeClassRequirement
		existing   map[string]bool
		wantPassed bool
		wantSubs   []string
	}{
		{
			name:       "all present",
			required:   []preflight.RuntimeClassRequirement{{Profile: "sandboxed", Name: "gvisor"}},
			existing:   map[string]bool{"gvisor": true},
			wantPassed: true,
		},
		{
			name:       "missing gvisor fails closed",
			required:   []preflight.RuntimeClassRequirement{{Profile: "sandboxed", Name: "gvisor"}},
			existing:   map[string]bool{"runc": true},
			wantPassed: false,
			wantSubs:   []string{"RuntimeClass 'gvisor' not found", "isolation profile 'sandboxed'"},
		},
		{
			name: "multiple missing both listed",
			required: []preflight.RuntimeClassRequirement{
				{Profile: "sandboxed", Name: "gvisor"},
				{Profile: "microvm", Name: "kata"},
			},
			existing:   map[string]bool{},
			wantPassed: false,
			wantSubs:   []string{"gvisor", "kata"},
		},
		{
			name: "duplicate requirement deduped",
			required: []preflight.RuntimeClassRequirement{
				{Profile: "sandboxed", Name: "gvisor"},
				{Profile: "sandboxed", Name: "gvisor"},
			},
			existing:   map[string]bool{},
			wantPassed: false,
			wantSubs:   []string{"gvisor"},
		},
		{
			name:       "empty name skipped",
			required:   []preflight.RuntimeClassRequirement{{Profile: "standard", Name: ""}},
			existing:   map[string]bool{},
			wantPassed: true,
		},
		{
			name:       "no requirements pass",
			required:   nil,
			existing:   map[string]bool{},
			wantPassed: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := preflight.CheckRuntimeClasses(tc.required, tc.existing)
			if d.Passed != tc.wantPassed {
				t.Fatalf("Passed = %v, want %v (reason=%q)", d.Passed, tc.wantPassed, d.Reason)
			}
			for _, sub := range tc.wantSubs {
				if !strings.Contains(d.Reason, sub) {
					t.Errorf("reason %q missing %q", d.Reason, sub)
				}
			}
			if tc.name == "duplicate requirement deduped" &&
				strings.Count(d.Reason, "gvisor") != 1 {
				t.Errorf("expected one gvisor mention, got %q", d.Reason)
			}
		})
	}
}

// TestRun_RuntimeClassPresence_spec_5_3_676 verifies the check is wired
// into Run: a missing required RuntimeClass fails the report fail-closed,
// and an install whose RuntimeClasses all exist passes the check.
func TestRun_RuntimeClassPresence_spec_5_3_676(t *testing.T) {
	rc := func(name string) *nodev1.RuntimeClass {
		return &nodev1.RuntimeClass{ObjectMeta: metav1.ObjectMeta{Name: name}, Handler: name}
	}

	objs := append(allBaselineWebhooks(), phaseStampCM(map[string]string{}))
	cfg := preflight.Config{
		Namespace:              preflightNS,
		RequiredRuntimeClasses: []preflight.RuntimeClassRequirement{{Profile: "sandboxed", Name: "gvisor"}},
	}

	// Missing gvisor: the runtimeclass-presence check fails the report.
	missingCl := runClient(t, objs...)
	rep := preflight.Run(context.Background(), missingCl, cfg)
	if !findCheckFailed(t, rep, "runtimeclass-presence") {
		t.Fatalf("expected runtimeclass-presence to fail when gvisor absent: %+v", rep)
	}
	if !preflight.Failed(rep) {
		t.Fatalf("expected overall report to fail fail-closed on missing RuntimeClass")
	}

	// gvisor present: the check passes.
	presentObjs := append([]client.Object{rc("gvisor")}, objs...)
	presentCl := runClient(t, presentObjs...)
	rep = preflight.Run(context.Background(), presentCl, cfg)
	if findCheckFailed(t, rep, "runtimeclass-presence") {
		t.Fatalf("runtimeclass-presence should pass when gvisor present: %+v", rep)
	}
}

// findCheckFailed reports whether the named check is present and failed.
func findCheckFailed(t *testing.T, report []preflight.CheckResult, name string) bool {
	t.Helper()
	for _, r := range report {
		if r.Name == name {
			return !r.Decision.Passed
		}
	}
	t.Fatalf("check %q not found in report", name)
	return false
}
