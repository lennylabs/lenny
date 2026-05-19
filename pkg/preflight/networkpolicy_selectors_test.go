// SPDX-License-Identifier: MIT

package preflight_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/lennylabs/lenny/pkg/preflight"
)

// labelSel builds a matchLabels-only LabelSelector for test peers.
func labelSel(kv ...string) *metav1.LabelSelector {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return &metav1.LabelSelector{MatchLabels: m}
}

// netPolicy builds a named Lenny-rendered NetworkPolicy in lenny-system.
func netPolicy(name string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "lenny-system"},
	}
}

// tcpPort builds a TCP NetworkPolicyPort on the given numeric port.
func tcpPort(port int) networkingv1.NetworkPolicyPort {
	p := intstr.FromInt(port)
	proto := corev1.ProtocolTCP
	return networkingv1.NetworkPolicyPort{Protocol: &proto, Port: &p}
}

// udpPort builds a UDP NetworkPolicyPort on the given numeric port.
func udpPort(port int) networkingv1.NetworkPolicyPort {
	p := intstr.FromInt(port)
	proto := corev1.ProtocolUDP
	return networkingv1.NetworkPolicyPort{Protocol: &proto, Port: &p}
}

func TestCheckSelectorConsistencyPassesOnCanonicalSelectors(t *testing.T) {
	p := netPolicy("allow-token-service")
	p.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{
		From: []networkingv1.NetworkPolicyPeer{
			{PodSelector: labelSel("lenny.dev/component", "gateway")},
		},
		Ports: []networkingv1.NetworkPolicyPort{tcpPort(50052)},
	}}
	p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{
			{PodSelector: labelSel("lenny.dev/component", "pgbouncer")},
		},
		Ports: []networkingv1.NetworkPolicyPort{tcpPort(5432)},
	}}
	d := preflight.CheckSelectorConsistency([]networkingv1.NetworkPolicy{*p})
	if !d.Passed {
		t.Errorf("check failed for a policy using only canonical selectors: %s", d.Reason)
	}
}

func TestCheckSelectorConsistencyAllowsAdditiveEgressKey(t *testing.T) {
	// NET-068: the additive webhook-name key is permitted in an egress
	// rule when paired with the canonical component key.
	p := netPolicy("allow-admission-webhooks")
	p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{
			{PodSelector: labelSel(
				"lenny.dev/component", "admission-webhook",
				"lenny.dev/webhook-name", "drain-readiness",
			)},
		},
		Ports: []networkingv1.NetworkPolicyPort{tcpPort(8080)},
	}}
	d := preflight.CheckSelectorConsistency([]networkingv1.NetworkPolicy{*p})
	if !d.Passed {
		t.Errorf("check rejected a sanctioned additive egress key: %s", d.Reason)
	}
}

func TestCheckSelectorConsistencyFailsOnLegacyAppKey(t *testing.T) {
	// NET-047: a platform component selected via the legacy app: key is
	// selector drift.
	p := netPolicy("allow-gateway-egress")
	p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{
			{PodSelector: labelSel("app", "lenny-gateway")},
		},
		Ports: []networkingv1.NetworkPolicyPort{tcpPort(50051)},
	}}
	d := preflight.CheckSelectorConsistency([]networkingv1.NetworkPolicy{*p})
	if d.Passed {
		t.Fatal("check passed a policy selecting a platform component via app: lenny-gateway")
	}
	if !strings.Contains(d.Reason, "NET-047") {
		t.Errorf("reason %q does not cite NET-047/NET-050", d.Reason)
	}
}

func TestCheckSelectorConsistencyFailsOnAdditiveIngressKey(t *testing.T) {
	// NET-068: the additive webhook-name key must never appear on the
	// ingress side.
	p := netPolicy("allow-gateway-ingress")
	p.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{
		From: []networkingv1.NetworkPolicyPeer{
			{PodSelector: labelSel(
				"lenny.dev/component", "admission-webhook",
				"lenny.dev/webhook-name", "drain-readiness",
			)},
		},
		Ports: []networkingv1.NetworkPolicyPort{tcpPort(8080)},
	}}
	d := preflight.CheckSelectorConsistency([]networkingv1.NetworkPolicy{*p})
	if d.Passed {
		t.Fatal("check passed an ingress rule carrying the additive webhook-name key")
	}
	if !strings.Contains(d.Reason, "NET-068") {
		t.Errorf("reason %q does not cite NET-068", d.Reason)
	}
}

func TestCheckDNSPodSelectorParityPassesWithServerSelector(t *testing.T) {
	p := netPolicy("allow-controller-egress")
	p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: labelSel("kubernetes.io/metadata.name", "kube-system"),
			PodSelector:       labelSel("k8s-app", "kube-dns"),
		}},
		Ports: []networkingv1.NetworkPolicyPort{udpPort(53), tcpPort(53)},
	}}
	d := preflight.CheckDNSPodSelectorParity([]networkingv1.NetworkPolicy{*p})
	if !d.Passed {
		t.Errorf("check failed a DNS rule that pairs a k8s-app: kube-dns podSelector: %s", d.Reason)
	}
}

func TestCheckDNSPodSelectorParityPassesForDedicatedCoreDNS(t *testing.T) {
	p := netPolicy("allow-pod-egress-base")
	p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: labelSel("kubernetes.io/metadata.name", "lenny-system"),
			PodSelector:       labelSel("lenny.dev/component", "coredns"),
		}},
		Ports: []networkingv1.NetworkPolicyPort{udpPort(53), tcpPort(53)},
	}}
	d := preflight.CheckDNSPodSelectorParity([]networkingv1.NetworkPolicy{*p})
	if !d.Passed {
		t.Errorf("check failed a DNS rule pointing at the dedicated CoreDNS: %s", d.Reason)
	}
}

func TestCheckDNSPodSelectorParityFailsOnNamespaceOnlyPeer(t *testing.T) {
	// NET-067: a DNS egress peer with a namespaceSelector but no
	// podSelector reaches every pod in kube-system on port 53.
	p := netPolicy("allow-controller-egress")
	p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: labelSel("kubernetes.io/metadata.name", "kube-system"),
		}},
		Ports: []networkingv1.NetworkPolicyPort{udpPort(53), tcpPort(53)},
	}}
	d := preflight.CheckDNSPodSelectorParity([]networkingv1.NetworkPolicy{*p})
	if d.Passed {
		t.Fatal("check passed a DNS egress peer with no destination podSelector")
	}
	if !strings.Contains(d.Reason, "NET-067") {
		t.Errorf("reason %q does not cite NET-067", d.Reason)
	}
}

func TestCheckDNSPodSelectorParityIgnoresNonDNSRules(t *testing.T) {
	// A namespace-only egress peer on a non-DNS port is not a NET-067
	// violation; the check must scope to UDP/TCP 53 rules only.
	p := netPolicy("allow-gateway-egress")
	p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: labelSel("kubernetes.io/metadata.name", "lenny-system"),
		}},
		Ports: []networkingv1.NetworkPolicyPort{tcpPort(5432)},
	}}
	d := preflight.CheckDNSPodSelectorParity([]networkingv1.NetworkPolicy{*p})
	if !d.Passed {
		t.Errorf("check flagged a non-DNS rule: %s", d.Reason)
	}
}
