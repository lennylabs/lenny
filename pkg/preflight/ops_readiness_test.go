// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// spec: §17.6 line 520. F-17.6.1.
func TestOpsIngressClusterIssuerCheck_spec_17_6_520(t *testing.T) {
	// No issuer annotation: silent pass.
	if d := (OpsIngressClusterIssuerCheck{}).Decide(); !d.Passed || d.Reason != "" {
		t.Fatalf("empty issuer should be silent pass, got %q", d.Reason)
	}
	// Issuer present and found: silent pass.
	if d := (OpsIngressClusterIssuerCheck{IssuerName: "letsencrypt", Exists: true}).Decide(); !d.Passed || d.Reason != "" {
		t.Fatalf("found issuer should be silent pass, got %q", d.Reason)
	}
	// Issuer referenced but missing: non-blocking warning.
	d := OpsIngressClusterIssuerCheck{IssuerName: "letsencrypt", Exists: false}.Decide()
	if !d.Passed {
		t.Fatalf("missing issuer must be non-blocking")
	}
	if !strings.Contains(d.Reason, "letsencrypt") || !strings.Contains(d.Reason, "without TLS") {
		t.Fatalf("warning should name the issuer and TLS impact: %q", d.Reason)
	}
}

// spec: §17.6 line 521. F-17.6.1.
func TestMonitoringNamespaceCheck_spec_17_6_521(t *testing.T) {
	// Missing namespace or label: silent pass.
	if d := (MonitoringNamespaceCheck{PodLabel: "app=prometheus"}).Decide(); d.Reason != "" {
		t.Fatalf("empty namespace should be silent pass, got %q", d.Reason)
	}
	if d := (MonitoringNamespaceCheck{Namespace: "monitoring"}).Decide(); d.Reason != "" {
		t.Fatalf("empty label should be silent pass, got %q", d.Reason)
	}
	// Pod present: silent pass.
	if d := (MonitoringNamespaceCheck{Namespace: "monitoring", PodLabel: "app=prometheus", HasMatchingPod: true}).Decide(); d.Reason != "" {
		t.Fatalf("present pod should be silent pass, got %q", d.Reason)
	}
	// No matching pod: non-blocking warning.
	d := MonitoringNamespaceCheck{Namespace: "monitoring", PodLabel: "app=prometheus", HasMatchingPod: false}.Decide()
	if !d.Passed || !strings.Contains(d.Reason, "monitoring") || !strings.Contains(d.Reason, "app=prometheus") {
		t.Fatalf("missing pod should warn naming ns+label, got passed=%v reason=%q", d.Passed, d.Reason)
	}
}

type fakeSARProber struct {
	deny map[string]bool
	err  error
}

func (f fakeSARProber) CanI(_ context.Context, _ string, rule OpsSARBACRule) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return !f.deny[rule.String()], nil
}

// spec: §17.6 line 519; §25.4. F-17.6.1.
func TestOpsSARBACCheck_spec_17_6_519(t *testing.T) {
	ctx := context.Background()
	rules := CanonicalOpsSARBACRules("lenny-system")
	if len(rules) == 0 {
		t.Fatal("canonical rules must be non-empty")
	}

	// Nil prober skips.
	if d := (OpsSARBACCheck{Rules: rules}).Decide(ctx); !d.Passed || !strings.Contains(d.Reason, "SKIPPED") {
		t.Fatalf("nil prober should skip, got %q", d.Reason)
	}

	// All allowed passes.
	if d := (OpsSARBACCheck{Rules: rules, Prober: fakeSARProber{}}).Decide(ctx); !d.Passed {
		t.Fatalf("all-allowed should pass: %s", d.Reason)
	}

	// A denied rule fails fail-closed and names the rule.
	denied := fakeSARProber{deny: map[string]bool{"apps/deployments:patch": true}}
	d := OpsSARBACCheck{ServiceAccount: "system:serviceaccount:lenny-system:lenny-ops-sa", Rules: rules, Prober: denied}.Decide(ctx)
	if d.Passed || !strings.Contains(d.Reason, "apps/deployments:patch") || !strings.Contains(d.Reason, "lenny-ops-sa is missing") {
		t.Fatalf("denied rule should fail naming it, got passed=%v reason=%q", d.Passed, d.Reason)
	}

	// A SubjectAccessReview transport error fails closed.
	d = OpsSARBACCheck{Rules: rules, Prober: fakeSARProber{err: errors.New("forbidden")}}.Decide(ctx)
	if d.Passed || !strings.Contains(d.Reason, "forbidden") {
		t.Fatalf("SAR error should fail closed, got passed=%v reason=%q", d.Passed, d.Reason)
	}
}

func TestSplitKeyValue(t *testing.T) {
	cases := []struct {
		in     string
		k, v   string
		wantOK bool
	}{
		{"app=prometheus", "app", "prometheus", true},
		{" app = prometheus ", "app", "prometheus", true},
		{"noequals", "", "", false},
		{"=value", "", "", false},
		{"key=", "", "", false},
	}
	for _, c := range cases {
		k, v, ok := splitKeyValue(c.in)
		if ok != c.wantOK || (ok && (k != c.k || v != c.v)) {
			t.Fatalf("splitKeyValue(%q)=(%q,%q,%v) want (%q,%q,%v)", c.in, k, v, ok, c.k, c.v, c.wantOK)
		}
	}
}
