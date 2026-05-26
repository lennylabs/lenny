// SPDX-License-Identifier: MIT

package preflight_test

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/preflight"
)

func ptrInt64(v int64) *int64 { return &v }

// TestCheckTerminationGracePeriods_spec_5_2_516 covers the §5.2 line 516
// node-drain-timeout warning: only pools above 600s warn, the 600s
// boundary does not, an unset grace period is ignored, and the check is
// always advisory (never fails the install).
func TestCheckTerminationGracePeriods_spec_5_2_516(t *testing.T) {
	cases := []struct {
		name     string
		pools    []preflight.PoolGracePeriod
		wantWarn bool
		wantSubs []string
	}{
		{name: "no pools", pools: nil, wantWarn: false},
		{
			name:     "unset grace ignored",
			pools:    []preflight.PoolGracePeriod{{Pool: "p1", TerminationGracePeriodSeconds: nil}},
			wantWarn: false,
		},
		{
			name:     "exactly 600 does not warn",
			pools:    []preflight.PoolGracePeriod{{Pool: "p1", TerminationGracePeriodSeconds: ptrInt64(600)}},
			wantWarn: false,
		},
		{
			name:     "601 warns",
			pools:    []preflight.PoolGracePeriod{{Pool: "p1", TerminationGracePeriodSeconds: ptrInt64(601)}},
			wantWarn: true,
			wantSubs: []string{"pool 'p1'", "601s", "600s"},
		},
		{
			name:     "840 warns",
			pools:    []preflight.PoolGracePeriod{{Pool: "big", TerminationGracePeriodSeconds: ptrInt64(840)}},
			wantWarn: true,
			wantSubs: []string{"pool 'big'", "840s"},
		},
		{
			name: "multiple over warn, all listed",
			pools: []preflight.PoolGracePeriod{
				{Pool: "a", TerminationGracePeriodSeconds: ptrInt64(700)},
				{Pool: "ok", TerminationGracePeriodSeconds: ptrInt64(120)},
				{Pool: "b", TerminationGracePeriodSeconds: ptrInt64(900)},
			},
			wantWarn: true,
			wantSubs: []string{"pool 'a'", "pool 'b'"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := preflight.CheckTerminationGracePeriods(tc.pools)
			// Advisory: the check never fails the install.
			if !d.Passed {
				t.Fatalf("CheckTerminationGracePeriods must always pass; got Passed=false reason=%q", d.Reason)
			}
			hasWarn := strings.Contains(d.Reason, "WARNING")
			if hasWarn != tc.wantWarn {
				t.Fatalf("warning present = %v, want %v (reason=%q)", hasWarn, tc.wantWarn, d.Reason)
			}
			for _, sub := range tc.wantSubs {
				if !strings.Contains(d.Reason, sub) {
					t.Errorf("reason %q missing %q", d.Reason, sub)
				}
			}
		})
	}
}

// lennyClient builds a fake client with both the clientgo and lenny.dev/v1
// schemes registered so SandboxTemplate listing works.
func lennyClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("clientgo AddToScheme: %v", err)
	}
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("lennyv1 AddToScheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
}

func sandboxTemplate(name string, grace *int64) *lennyv1.SandboxTemplate {
	return &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "lenny-agents"},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:                    "echo",
			TerminationGracePeriodSeconds: grace,
		},
	}
}

// TestRun_TerminationGraceWarning_spec_5_2_516 verifies Run surfaces the
// advisory warning for an over-600s pool without failing the install, and
// passes cleanly when no pool exceeds the threshold.
func TestRun_TerminationGraceWarning_spec_5_2_516(t *testing.T) {
	baseline := append(allBaselineWebhooks(), phaseStampCM(map[string]string{}))
	cfg := preflight.Config{Namespace: preflightNS}

	// A 900s pool warns but does not fail the report.
	withBig := append([]client.Object{sandboxTemplate("big", ptrInt64(900))}, baseline...)
	rep := preflight.Run(context.Background(), lennyClient(t, withBig...), cfg)
	d := findCheck(t, rep, "pool-termination-grace-period")
	if !d.Passed {
		t.Fatalf("grace-period check must be advisory (pass); got Passed=false")
	}
	if !strings.Contains(d.Reason, "WARNING") || !strings.Contains(d.Reason, "big") {
		t.Fatalf("expected advisory warning naming 'big', got %q", d.Reason)
	}

	// No over-threshold pool: clean pass, no warning.
	withOK := append([]client.Object{sandboxTemplate("ok", ptrInt64(120))}, baseline...)
	rep = preflight.Run(context.Background(), lennyClient(t, withOK...), cfg)
	d = findCheck(t, rep, "pool-termination-grace-period")
	if !d.Passed || strings.Contains(d.Reason, "WARNING") {
		t.Fatalf("expected clean pass for 120s pool, got Passed=%v reason=%q", d.Passed, d.Reason)
	}
}

// TestRun_TerminationGraceListErrorAdvisory_spec_5_2_516 verifies a list
// failure (here the lenny.dev scheme is unregistered) is surfaced as an
// advisory pass rather than blocking the install.
func TestRun_TerminationGraceListErrorAdvisory_spec_5_2_516(t *testing.T) {
	baseline := append(allBaselineWebhooks(), phaseStampCM(map[string]string{}))
	// runClient registers only clientgoscheme, so listing SandboxTemplates
	// fails; the check must still pass advisory-only.
	rep := preflight.Run(context.Background(), runClient(t, baseline...), preflight.Config{Namespace: preflightNS})
	d := findCheck(t, rep, "pool-termination-grace-period")
	if !d.Passed {
		t.Fatalf("list error must not block the install; got Passed=false reason=%q", d.Reason)
	}
}

// findCheck returns the named check's decision or fails the test.
func findCheck(t *testing.T, report []preflight.CheckResult, name string) preflight.Decision {
	t.Helper()
	for _, r := range report {
		if r.Name == name {
			return r.Decision
		}
	}
	t.Fatalf("check %q not found in report", name)
	return preflight.Decision{}
}
