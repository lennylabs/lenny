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

// objectStoreInClusterPolicy builds an allow-pod-egress-objectstore
// NetworkPolicy carrying the NET-071 in-cluster arm: an egress peer
// selecting the self-managed MinIO pod on the TLS port.
func objectStoreInClusterPolicy() *networkingv1.NetworkPolicy {
	p := netPolicy("allow-pod-egress-objectstore")
	p.Namespace = "lenny-agents"
	p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: labelSel("kubernetes.io/metadata.name", "lenny-system"),
			PodSelector:       labelSel("lenny.dev/component", "minio"),
		}},
		Ports: []networkingv1.NetworkPolicyPort{tcpPort(9443)},
	}}
	return p
}

// objectStoreCloudPolicy builds an allow-pod-egress-objectstore
// NetworkPolicy carrying the NET-071 cloud-managed arm: an ipBlock peer
// from a non-empty objectStorage.egressCIDRs list on TCP 443.
func objectStoreCloudPolicy() *networkingv1.NetworkPolicy {
	p := netPolicy("allow-pod-egress-objectstore")
	p.Namespace = "lenny-agents"
	p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{{
			IPBlock: &networkingv1.IPBlock{CIDR: "203.0.113.0/24"},
		}},
		Ports: []networkingv1.NetworkPolicyPort{tcpPort(443)},
	}}
	return p
}

// minioIngressWithAgentClause builds an allow-minio NetworkPolicy that
// carries the NET-071 agent-namespace ingress clause paired with the
// in-cluster arm, alongside the always-present gateway ingress.
func minioIngressWithAgentClause() *networkingv1.NetworkPolicy {
	p := netPolicy("allow-minio")
	p.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{
		{
			From:  []networkingv1.NetworkPolicyPeer{{PodSelector: labelSel("lenny.dev/component", "gateway")}},
			Ports: []networkingv1.NetworkPolicyPort{tcpPort(9443)},
		},
		{
			From: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: labelSel("lenny.dev/agent-namespace", "true"),
				PodSelector:       labelSel("lenny.dev/managed", "true"),
			}},
			Ports: []networkingv1.NetworkPolicyPort{tcpPort(9443)},
		},
	}
	return p
}

// minioIngressGatewayOnly builds an allow-minio NetworkPolicy with only
// the gateway ingress clause and no NET-071 agent-namespace clause (the
// cloud-managed / no-MinIO render).
func minioIngressGatewayOnly() *networkingv1.NetworkPolicy {
	p := netPolicy("allow-minio")
	p.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{
		From:  []networkingv1.NetworkPolicyPeer{{PodSelector: labelSel("lenny.dev/component", "gateway")}},
		Ports: []networkingv1.NetworkPolicyPort{tcpPort(9443)},
	}}
	return p
}

// TestCheckObjectStoreEgressParityPassesOnMatchedPair pins the §13.2
// NET-071 both-or-neither invariant: an in-cluster object-store egress
// arm paired with the matching MinIO agent-namespace ingress clause passes.
//
// spec: §13.2 line 209; §17.6 (NET-071).
func TestCheckObjectStoreEgressParityPassesOnMatchedPair(t *testing.T) {
	d := preflight.CheckObjectStoreEgressParity([]networkingv1.NetworkPolicy{
		*objectStoreInClusterPolicy(),
		*minioIngressWithAgentClause(),
	})
	if !d.Passed {
		t.Errorf("matched in-cluster egress / MinIO ingress pair failed parity: %s", d.Reason)
	}
}

// TestCheckObjectStoreEgressParityPassesOnCloudManagedArm pins that a
// cloud-managed arm (an ipBlock peer from a non-empty egressCIDRs list)
// with no in-cluster arm and no MinIO agent-namespace ingress clause
// passes: the cloud-managed profile has no MinIO pod and no ingress side.
//
// spec: §13.2 line 209; §17.6 (NET-071).
func TestCheckObjectStoreEgressParityPassesOnCloudManagedArm(t *testing.T) {
	d := preflight.CheckObjectStoreEgressParity([]networkingv1.NetworkPolicy{
		*objectStoreCloudPolicy(),
		*minioIngressGatewayOnly(),
	})
	if !d.Passed {
		t.Errorf("cloud-managed arm with non-empty CIDRs failed parity: %s", d.Reason)
	}
}

// TestCheckObjectStoreEgressParityPassesWhenNeitherRenders pins that a
// deployment resolving the in-memory or filesystem store (no object-store
// egress policy, no agent-namespace MinIO ingress clause) passes.
//
// spec: §13.2 line 209; §17.6 (NET-071).
func TestCheckObjectStoreEgressParityPassesWhenNeitherRenders(t *testing.T) {
	d := preflight.CheckObjectStoreEgressParity([]networkingv1.NetworkPolicy{
		*minioIngressGatewayOnly(),
	})
	if !d.Passed {
		t.Errorf("neither-rendered case failed parity: %s", d.Reason)
	}
}

// TestCheckObjectStoreEgressParityFailsInClusterArmWithoutMinIOIngress
// pins the NET-071 fail-closed gate: an in-cluster object-store egress
// arm rendered without the paired MinIO agent-namespace ingress clause
// leaves the checkpoint chunk return path dropped by the lenny-system
// default-deny, so the install fails closed. This asserts the corrected
// gate rejects; before this check existed the one-sided render passed
// preflight and silently broke checkpoint transfer at runtime.
//
// spec: §13.2 line 209; §17.6 (NET-071).
func TestCheckObjectStoreEgressParityFailsInClusterArmWithoutMinIOIngress(t *testing.T) {
	d := preflight.CheckObjectStoreEgressParity([]networkingv1.NetworkPolicy{
		*objectStoreInClusterPolicy(),
		*minioIngressGatewayOnly(),
	})
	if d.Passed {
		t.Fatal("in-cluster arm without the paired MinIO ingress clause passed parity")
	}
	if !strings.Contains(d.Reason, "NETWORK_POLICY_OBJECTSTORE_PARITY") {
		t.Errorf("reason %q does not carry the NET-071 parity error code", d.Reason)
	}
}

// TestCheckObjectStoreEgressParityFailsMinIOIngressWithoutInClusterArm
// pins the reverse NET-071 fail-closed gate: a MinIO agent-namespace
// ingress clause rendered with no object-store egress policy fails closed.
//
// spec: §13.2 line 209; §17.6 (NET-071).
func TestCheckObjectStoreEgressParityFailsMinIOIngressWithoutInClusterArm(t *testing.T) {
	d := preflight.CheckObjectStoreEgressParity([]networkingv1.NetworkPolicy{
		*minioIngressWithAgentClause(),
	})
	if d.Passed {
		t.Fatal("MinIO agent-namespace ingress clause without an object-store egress policy passed parity")
	}
	if !strings.Contains(d.Reason, "NETWORK_POLICY_OBJECTSTORE_PARITY") {
		t.Errorf("reason %q does not carry the NET-071 parity error code", d.Reason)
	}
}

// TestCheckObjectStoreEgressParityFailsEmptyCloudCIDRs pins the NET-071
// non-empty-CIDR gate: an object-store egress rule that renders no peers
// admits every destination, the fail-open outcome of an empty
// objectStorage.egressCIDRs list under a cloud-managed provider. The
// check fails it closed.
//
// spec: §13.2 line 209; §17.6 (NET-071).
func TestCheckObjectStoreEgressParityFailsEmptyCloudCIDRs(t *testing.T) {
	p := netPolicy("allow-pod-egress-objectstore")
	p.Namespace = "lenny-agents"
	p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To:    nil,
		Ports: []networkingv1.NetworkPolicyPort{tcpPort(443)},
	}}
	d := preflight.CheckObjectStoreEgressParity([]networkingv1.NetworkPolicy{*p})
	if d.Passed {
		t.Fatal("object-store egress rule with no peers (empty egressCIDRs) passed parity")
	}
	if !strings.Contains(d.Reason, "NETWORK_POLICY_OBJECTSTORE_PARITY") {
		t.Errorf("reason %q does not carry the NET-071 parity error code", d.Reason)
	}
}
