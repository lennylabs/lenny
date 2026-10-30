// SPDX-License-Identifier: MIT

package preflight

import (
	"fmt"
	"net/netip"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
)

// cidrFamily classifies a CIDR string as IPv4 or IPv6 for the §13.2
// NET-062 family-parity audits. It returns "IPv4", "IPv6", or an empty
// string when the CIDR does not parse.
func cidrFamily(cidr string) string {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return ""
	}
	if prefix.Addr().Is4() {
		return "IPv4"
	}
	return "IPv6"
}

// CheckIPBlockFamilyParity verifies the §13.2 NET-062 ipBlock
// family-uniformity invariant: in every Lenny-rendered NetworkPolicy,
// each `except` entry of an `ipBlock` peer must share the address
// family of its enclosing `cidr` (IPv4 entries only on
// 0.0.0.0/0-rooted blocks, IPv6 entries only on ::/0-rooted blocks).
//
// Kubernetes NetworkPolicySpec.egress[].to[].ipBlock requires per-block
// family uniformity: strict CNIs (Cilium, Calico) reject a cross-family
// manifest at admission, while lenient CNIs silently drop the off-family
// entries and admit the manifest — producing a silent SSRF hole. The
// check is fail-closed because the lenient-CNI outcome is the more
// dangerous one.
func CheckIPBlockFamilyParity(policies []networkingv1.NetworkPolicy) Decision {
	for i := range policies {
		p := &policies[i]
		for _, block := range ipBlocksOf(p) {
			if d := auditIPBlockFamily(p, block); !d.Passed {
				return d
			}
		}
	}
	return Decision{Passed: true}
}

// auditIPBlockFamily checks one ipBlock for cross-family except entries.
func auditIPBlockFamily(p *networkingv1.NetworkPolicy, block *networkingv1.IPBlock) Decision {
	cidrFam := cidrFamily(block.CIDR)
	if cidrFam == "" {
		return Decision{Passed: false, Reason: fmt.Sprintf(
			"NETWORK_POLICY_IPBLOCK_INVALID_CIDR: NetworkPolicy %q has an ipBlock whose "+
				"cidr %q is not a valid CIDR (§13.2 NET-062)",
			policyRef(p), block.CIDR,
		)}
	}
	for _, except := range block.Except {
		exceptFam := cidrFamily(except)
		if exceptFam == "" {
			return Decision{Passed: false, Reason: fmt.Sprintf(
				"NETWORK_POLICY_IPBLOCK_INVALID_CIDR: NetworkPolicy %q has an ipBlock "+
					"except entry %q that is not a valid CIDR (§13.2 NET-062)",
				policyRef(p), except,
			)}
		}
		if exceptFam != cidrFam {
			return Decision{Passed: false, Reason: fmt.Sprintf(
				"NETWORK_POLICY_IPBLOCK_FAMILY_MISMATCH: NetworkPolicy %q contains an "+
					"ipBlock whose cidr %q is %s but except entry %q is %s; §13.2 "+
					"(NET-062) requires every except entry to share the enclosing cidr's "+
					"address family. Split the rule into two parallel ipBlock peers, one "+
					"per family, with family-matching except entries on each",
				policyRef(p), block.CIDR, cidrFam, except, exceptFam,
			)}
		}
	}
	return Decision{Passed: true}
}

// ipBlocksOf returns every ipBlock peer in a NetworkPolicy, across both
// ingress and egress rules.
func ipBlocksOf(p *networkingv1.NetworkPolicy) []*networkingv1.IPBlock {
	var out []*networkingv1.IPBlock
	for _, rule := range p.Spec.Ingress {
		for _, peer := range rule.From {
			if peer.IPBlock != nil {
				out = append(out, peer.IPBlock)
			}
		}
	}
	for _, rule := range p.Spec.Egress {
		for _, peer := range rule.To {
			if peer.IPBlock != nil {
				out = append(out, peer.IPBlock)
			}
		}
	}
	return out
}
