// SPDX-License-Identifier: MIT

// In-package unit tests for the §13.2 agent-pod egress and DNS labels the
// WarmPoolController stamps onto every Sandbox it warms. The labels
// propagate to the agent pod, where the chart-rendered NetworkPolicies
// select them; the controller never creates NetworkPolicies (spec: §13.2
// line 424). Covers F-13.2.1 (delivery-mode), F-13.2.11 (egress-profile),
// and F-13.2.4 (dns-policy opt-out).
package warmpool

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
)

func labelsTestPool() *lennyv1.SandboxWarmPool {
	return &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-worker-small", Namespace: "lenny-agents"},
	}
}

func labelsTestTemplate(spec lennyv1.SandboxTemplateSpec) *lennyv1.SandboxTemplate {
	return &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-worker-small", Namespace: "lenny-agents"},
		Spec:       spec,
	}
}

// spec: §13.2 lines 118, 130 — the delivery-mode label is set with value
// `proxy` only on proxy-mode pools so the allow-pod-egress-llm-proxy
// policy admits the LLM proxy port. F-13.2.1.
func TestSandboxLabelsDeliveryModeProxyOnly(t *testing.T) {
	proxy := sandboxLabels(labelsTestPool(), labelsTestTemplate(lennyv1.SandboxTemplateSpec{DeliveryMode: "proxy"}))
	if got := proxy[LabelDeliveryMode]; got != "proxy" {
		t.Fatalf("proxy pool: lenny.dev/delivery-mode = %q, want %q", got, "proxy")
	}

	direct := sandboxLabels(labelsTestPool(), labelsTestTemplate(lennyv1.SandboxTemplateSpec{DeliveryMode: "direct"}))
	if _, ok := direct[LabelDeliveryMode]; ok {
		t.Fatalf("direct pool must not carry lenny.dev/delivery-mode; got %q", direct[LabelDeliveryMode])
	}

	none := sandboxLabels(labelsTestPool(), labelsTestTemplate(lennyv1.SandboxTemplateSpec{}))
	if _, ok := none[LabelDeliveryMode]; ok {
		t.Fatalf("pool with no delivery mode must not carry lenny.dev/delivery-mode")
	}
}

// spec: §13.2 lines 424-432 — every managed pod carries the resolved
// egress profile; an empty SandboxTemplate.spec.egressProfile resolves to
// the default `restricted`. F-13.2.11.
func TestSandboxLabelsEgressProfileResolved(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", EgressProfileRestricted},
		{"restricted", "restricted"},
		{"provider-direct", "provider-direct"},
		{"internet", "internet"},
	}
	for _, tc := range cases {
		got := sandboxLabels(labelsTestPool(), labelsTestTemplate(lennyv1.SandboxTemplateSpec{EgressProfile: tc.in}))
		if got[LabelEgressProfile] != tc.want {
			t.Errorf("egressProfile %q: lenny.dev/egress-profile = %q, want %q", tc.in, got[LabelEgressProfile], tc.want)
		}
	}
}

// spec: §13.2 lines 470-490 — the dns-policy label is set with value
// `cluster-default` only on pools that opt out of the dedicated CoreDNS
// instance; pods in all other pools do not receive it. F-13.2.4.
func TestSandboxLabelsDNSPolicyOptOutOnly(t *testing.T) {
	optOut := sandboxLabels(labelsTestPool(), labelsTestTemplate(lennyv1.SandboxTemplateSpec{DNSPolicy: DNSPolicyClusterDefault}))
	if got := optOut[LabelDNSPolicy]; got != DNSPolicyClusterDefault {
		t.Fatalf("opt-out pool: lenny.dev/dns-policy = %q, want %q", got, DNSPolicyClusterDefault)
	}

	dedicated := sandboxLabels(labelsTestPool(), labelsTestTemplate(lennyv1.SandboxTemplateSpec{}))
	if _, ok := dedicated[LabelDNSPolicy]; ok {
		t.Fatalf("dedicated-DNS pool must not carry lenny.dev/dns-policy")
	}
}

// The pool and managed labels are always present so the per-pool List and
// §17.2 admission targeting keep working alongside the new labels.
func TestSandboxLabelsAlwaysCarriesPoolAndManaged(t *testing.T) {
	labels := sandboxLabels(labelsTestPool(), labelsTestTemplate(lennyv1.SandboxTemplateSpec{DeliveryMode: "proxy"}))
	if labels[LabelPool] != "claude-worker-small" {
		t.Errorf("lenny.dev/pool = %q, want %q", labels[LabelPool], "claude-worker-small")
	}
	if labels[LabelManaged] != "true" {
		t.Errorf("lenny.dev/managed = %q, want %q", labels[LabelManaged], "true")
	}
}

// A nil template (defensive) yields only the pool and managed labels.
func TestSandboxLabelsNilTemplate(t *testing.T) {
	labels := sandboxLabels(labelsTestPool(), nil)
	if len(labels) != 2 {
		t.Fatalf("nil template: got %d labels, want 2 (pool, managed): %v", len(labels), labels)
	}
}
