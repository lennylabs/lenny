// SPDX-License-Identifier: MIT

package preflight

import (
	"fmt"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ruleKind names the rule direction for an audit failure message.
type ruleKind string

const (
	ingressRule ruleKind = "ingress"
	egressRule  ruleKind = "egress"
)

// CheckSelectorConsistency verifies the §13.2 NET-047/NET-050 and
// NET-068 canonical-selector invariant over every Lenny-rendered
// NetworkPolicy. For each ingress `from:` peer and egress `to:` peer it
// asserts:
//
//	(a) a podSelector that resolves to a lenny-system platform
//	    component (gateway, token-service, controller, pgbouncer,
//	    minio, admission-webhook, coredns) uses the canonical
//	    lenny.dev/component key — never a legacy app: lenny-<component>
//	    key (NET-047/NET-050);
//	(c) an ingress-side podSelector carries no additive per-pod key
//	    beyond the canonical lenny.dev/component label — the additive
//	    lenny.dev/webhook-name key (NET-068) is permitted in egress
//	    allow-lists only, and any additive egress key must be paired
//	    with the canonical key.
//
// The check is fail-closed: a silently non-matching NetworkPolicy is
// more dangerous than a missing one (§13.2 line 209).
//
// TODO(NET-050): §13.2 part (b) of the NET-047/NET-050 audit — "any
// selector matches zero pods for a component that is expected to be
// running given the rendered Helm values" — is not implemented here.
// The rule is ambiguous to implement faithfully: the lenny-preflight
// Job runs as a pre-install/pre-upgrade hook BEFORE the chart's main
// phase applies the platform Deployments (the same ordering the
// admission-webhook-inventory check accounts for), so on a fresh
// install every component selector legitimately resolves to zero pods.
// "Expected to be running given the rendered Helm values" further
// depends on the deployment profile (the §13.2 PgBouncer and MinIO
// rows are absent on cloud-managed deployments) and on which optional
// components a given values file enables — none of which is available
// to a pure selector-string audit. Implementing part (b) requires the
// live pod inventory plus the full set of rendered Helm values that
// determine the expected component set; that pairing is not currently
// passed to preflight. Parts (a) and (c), which need only the rendered
// NetworkPolicy manifests, are enforced in full below.
func CheckSelectorConsistency(policies []networkingv1.NetworkPolicy) Decision {
	for i := range policies {
		p := &policies[i]
		for _, rule := range p.Spec.Ingress {
			for _, peer := range rule.From {
				if d := auditPeerSelector(p, ingressRule, peer.PodSelector); !d.Passed {
					return d
				}
			}
		}
		for _, rule := range p.Spec.Egress {
			for _, peer := range rule.To {
				if d := auditPeerSelector(p, egressRule, peer.PodSelector); !d.Passed {
					return d
				}
			}
		}
	}
	return Decision{Passed: true}
}

// auditPeerSelector applies the NET-047/NET-050/NET-068 rules to a
// single peer's podSelector.
func auditPeerSelector(p *networkingv1.NetworkPolicy, kind ruleKind, sel *metav1.LabelSelector) Decision {
	labels := selectorMatchLabels(sel)
	if len(labels) == 0 {
		return Decision{Passed: true}
	}

	// (a) A selector that resolves to a platform component via a legacy
	// app: lenny-<component> key is selector drift (NET-047/NET-050).
	if v, ok := labels["app"]; ok {
		if _, isPlatform := targetsPlatformComponent(sel); isPlatform {
			if _, hasCanonical := labels[canonicalComponentLabel]; !hasCanonical {
				return Decision{Passed: false, Reason: fmt.Sprintf(
					"NETWORK_POLICY_SELECTOR_DRIFT: NetworkPolicy %q has a %s peer selecting "+
						"lenny-system platform component via the legacy key app=%q; §13.2 "+
						"(NET-047/NET-050) requires the canonical %s key",
					policyRef(p), kind, v, canonicalComponentLabel,
				)}
			}
		}
	}

	_, hasCanonical := labels[canonicalComponentLabel]

	// (c) The additive lenny.dev/webhook-name key is permitted only in
	// egress rules, and only paired with the canonical component key
	// (NET-068). Any other additive key on a platform-component selector
	// is forbidden on both sides.
	for key := range labels {
		if key == canonicalComponentLabel || key == "app" {
			continue
		}
		if key == webhookNameLabel {
			if kind == ingressRule {
				return Decision{Passed: false, Reason: fmt.Sprintf(
					"NETWORK_POLICY_SELECTOR_DRIFT: NetworkPolicy %q has an ingress peer carrying "+
						"the additive key %q; §13.2 (NET-068) permits the additive per-pod key in "+
						"egress allow-lists only, never on the ingress side",
					policyRef(p), webhookNameLabel,
				)}
			}
			if !hasCanonical {
				return Decision{Passed: false, Reason: fmt.Sprintf(
					"NETWORK_POLICY_SELECTOR_DRIFT: NetworkPolicy %q has an egress peer carrying "+
						"the additive key %q without the canonical %s key; §13.2 (NET-068) requires "+
						"the canonical key to remain present alongside any additive key",
					policyRef(p), webhookNameLabel, canonicalComponentLabel,
				)}
			}
			continue
		}
		// An unknown additive key alongside the canonical component key
		// on a platform-component selector is not sanctioned by §13.2.
		if hasCanonical {
			return Decision{Passed: false, Reason: fmt.Sprintf(
				"NETWORK_POLICY_SELECTOR_DRIFT: NetworkPolicy %q has a %s peer whose "+
					"podSelector carries the unsanctioned additive key %q alongside the "+
					"canonical %s key; §13.2 (NET-047/NET-050, NET-068) permits only "+
					"%s as an additive key, and egress-only",
				policyRef(p), kind, key, canonicalComponentLabel, webhookNameLabel,
			)}
		}
	}
	return Decision{Passed: true}
}

// CheckDNSPodSelectorParity verifies the §13.2 NET-067 DNS-egress-peer
// invariant: every Lenny-rendered NetworkPolicy egress rule whose ports
// list contains UDP/53 or TCP/53 must pair each `to:` peer's
// namespaceSelector with a destination podSelector matching a concrete
// DNS-server label (k8s-app: kube-dns for the kube-system CoreDNS, or
// lenny.dev/component: coredns for the dedicated lenny-system CoreDNS).
//
// A namespace-only selector on kube-system reaches every pod in that
// namespace (metrics-server, kube-proxy, CSI drivers, cloud-provider
// controllers) on UDP/TCP 53. The check is fail-closed.
func CheckDNSPodSelectorParity(policies []networkingv1.NetworkPolicy) Decision {
	for i := range policies {
		p := &policies[i]
		for _, rule := range p.Spec.Egress {
			if !rulePermitsDNSPort(rule) {
				continue
			}
			for _, peer := range rule.To {
				// An ipBlock peer is not a DNS-by-selector peer; the
				// NET-067 rule governs namespace/pod selector peers.
				if peer.IPBlock != nil {
					continue
				}
				if !dnsPeerHasServerSelector(peer) {
					return Decision{Passed: false, Reason: fmt.Sprintf(
						"NETWORK_POLICY_DNS_SELECTOR_MISSING: NetworkPolicy %q has a DNS egress "+
							"rule (UDP/TCP 53) whose peer omits a destination podSelector matching "+
							"a concrete DNS-server label; §13.2 (NET-067) requires k8s-app: kube-dns "+
							"for the kube-system CoreDNS or %s: coredns for the dedicated CoreDNS",
						policyRef(p), canonicalComponentLabel,
					)}
				}
			}
		}
	}
	return Decision{Passed: true}
}

// rulePermitsDNSPort reports whether an egress rule lists UDP/53 or
// TCP/53. A rule with no ports permits every port and therefore also
// permits DNS, so it counts.
func rulePermitsDNSPort(rule networkingv1.NetworkPolicyEgressRule) bool {
	if len(rule.Ports) == 0 {
		return true
	}
	for _, port := range rule.Ports {
		if port.Port != nil && port.Port.IntValue() == 53 {
			return true
		}
	}
	return false
}

// dnsPeerHasServerSelector reports whether a DNS egress peer pairs a
// namespaceSelector with a podSelector matching a concrete DNS-server
// label (NET-067).
func dnsPeerHasServerSelector(peer networkingv1.NetworkPolicyPeer) bool {
	if !peerHasNamespaceSelector(peer) || !peerHasPodSelector(peer) {
		return false
	}
	labels := selectorMatchLabels(peer.PodSelector)
	for key, want := range dnsServerSelectors {
		if labels[key] == want {
			return true
		}
	}
	return false
}
