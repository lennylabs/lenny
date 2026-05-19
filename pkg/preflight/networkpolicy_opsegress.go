// SPDX-License-Identifier: MIT

package preflight

import (
	"fmt"

	networkingv1 "k8s.io/api/networking/v1"
)

// operabilityEgressPolicies is the set of Lenny-rendered NetworkPolicy
// names that originate from the operability plane and are subject to
// the §13.2 NET-061 two-selector requirement. It mirrors the chart's
// operabilityEgressPolicies list: the lenny-ops Deployment egress and
// the lenny-backup Job egress.
var operabilityEgressPolicies = map[string]bool{
	opsEgressPolicy:    true,
	"lenny-backup-job": true,
}

// CheckOpsEgressSelectorParity verifies the §13.2 NET-061
// operability-plane two-selector invariant. Every `to:` clause in an
// operability-plane NetworkPolicy (lenny-ops-egress, lenny-backup-job,
// and any future policy in the chart's operabilityEgressPolicies list)
// that targets a storage, monitoring, or in-cluster platform-component
// destination MUST pair a namespaceSelector with a podSelector:
//
//   - a namespace/pod-selector peer that carries a namespaceSelector
//     but omits the podSelector is rejected — a namespace-only selector
//     permits egress to every pod in the destination namespace, which
//     in a co-located install exposes gateway, token-service,
//     controller, admission-webhook, and CoreDNS pods to the
//     operability plane on storage-shaped ports;
//   - a podSelector that resolves to a Lenny-rendered platform
//     component MUST use the canonical lenny.dev/component key. The
//     app: key is permitted only for storage/monitoring destinations
//     rendered by upstream subcharts (app: redis, app: prometheus,
//     app: postgres, app: minio) or where the operability plane's own
//     identity is the selector target (app: lenny-ops / lenny-backup).
//
// The check is fail-closed: a silently over-broad operability-plane
// egress rule is strictly more dangerous than a missing one. An
// ipBlock peer (the kube-apiserver CIDR rule) and a peer that carries
// only a podSelector are not namespace-scoped destination clauses and
// are not subject to the two-selector rule. The NET-067 DNS-peer rule
// is enforced separately by CheckDNSPodSelectorParity.
func CheckOpsEgressSelectorParity(policies []networkingv1.NetworkPolicy) Decision {
	for i := range policies {
		p := &policies[i]
		if !operabilityEgressPolicies[p.Name] {
			continue
		}
		for _, rule := range p.Spec.Egress {
			isDNS := rulePermitsDNSPort(rule)
			for _, peer := range rule.To {
				if d := auditOpsEgressPeer(p, peer, isDNS); !d.Passed {
					return d
				}
			}
		}
	}
	return Decision{Passed: true}
}

// auditOpsEgressPeer applies the NET-061 two-selector rule to one
// egress peer of an operability-plane NetworkPolicy.
func auditOpsEgressPeer(p *networkingv1.NetworkPolicy, peer networkingv1.NetworkPolicyPeer, isDNSRule bool) Decision {
	// An ipBlock peer (the kube-apiserver CIDR rule and the broad
	// webhook-delivery rules) is not a namespace-scoped destination
	// clause; the two-selector rule does not apply to it.
	if peer.IPBlock != nil {
		return Decision{Passed: true}
	}
	// A peer with neither selector targets nothing; the chart never
	// renders an empty peer, so leave it to the API server.
	if !peerHasNamespaceSelector(peer) && !peerHasPodSelector(peer) {
		return Decision{Passed: true}
	}

	// A namespace-scoped destination clause MUST carry a podSelector
	// (NET-061). The DNS rule is held to the same standard by NET-067,
	// reported there; flagging it here too would be redundant, so a DNS
	// peer that already pairs both selectors passes and a DNS peer
	// missing the podSelector is left to CheckDNSPodSelectorParity.
	if peerHasNamespaceSelector(peer) && !peerHasPodSelector(peer) {
		if isDNSRule {
			return Decision{Passed: true}
		}
		return Decision{Passed: false, Reason: fmt.Sprintf(
			"NETWORK_POLICY_OPS_EGRESS_SELECTOR_MISSING: operability-plane NetworkPolicy "+
				"%q has a to: clause carrying a namespaceSelector but no podSelector; §13.2 "+
				"(NET-061) requires every storage/monitoring/platform-component destination "+
				"clause to pair both selectors — a namespace-only selector permits egress to "+
				"every pod in the destination namespace",
			policyRef(p),
		)}
	}

	// The podSelector must use the canonical key for a Lenny-rendered
	// platform component (NET-047/NET-050). The app: key is permitted
	// only for storage/monitoring destinations and the operability
	// plane's own identity.
	labels := selectorMatchLabels(peer.PodSelector)
	if v, ok := labels["app"]; ok {
		if _, hasCanonical := labels[canonicalComponentLabel]; !hasCanonical {
			if !storageMonitoringAppValues[v] && !opsAppValues[v] {
				return Decision{Passed: false, Reason: fmt.Sprintf(
					"NETWORK_POLICY_OPS_EGRESS_SELECTOR_DRIFT: operability-plane NetworkPolicy "+
						"%q has a to: clause whose podSelector uses the key app=%q; §13.2 "+
						"(NET-061) permits the app: key only for storage/monitoring destinations "+
						"(redis, prometheus, postgres, minio) or the operability plane's own "+
						"identity — a Lenny-rendered platform component MUST use the canonical "+
						"%s key",
					policyRef(p), v, canonicalComponentLabel,
				)}
			}
		}
	}
	return Decision{Passed: true}
}
