// SPDX-License-Identifier: MIT

package preflight

import (
	"fmt"

	networkingv1 "k8s.io/api/networking/v1"
)

// CheckClusterCIDRSymmetry verifies the §13.2 NET-065 cluster-CIDR and
// IMDS symmetry invariant across the rendered lenny-system
// outbound-HTTPS surfaces — allow-gateway-egress-llm-upstream and
// lenny-ops-egress. The two surfaces face the same SSRF threat model,
// so the cluster pod/service CIDR exclusions and the IMDS exclusions
// MUST be identical on both: a tenant-influenced URL resolving to an
// in-cluster pod IP would otherwise let one surface dial
// gateway/controller/token-service pods directly on clusters that use
// CGNAT-range or other non-RFC1918 pod CIDRs.
//
// The check is a symmetry audit over the rendered manifests: every
// IMDS-or-cluster-CIDR-shaped `except` entry present on one surface
// must also appear on the other, partitioned by address family. It
// needs no live-cluster discovery — the NET-022 "Internet egress CIDR
// exclusions" check separately validates the discovered cluster CIDRs
// against the Helm values. This audit guards against a future edit
// that adds or removes a cluster/IMDS exclusion on only one surface.
//
// When neither policy is rendered the check passes; when exactly one
// is rendered it fails, for the same reason as the NET-057 parity
// check — a single surface cannot be in symmetry.
func CheckClusterCIDRSymmetry(policies []networkingv1.NetworkPolicy) Decision {
	gw := findPolicy(policies, gatewayLLMUpstreamPolicy)
	ops := findPolicy(policies, opsEgressPolicy)
	if gw == nil && ops == nil {
		return Decision{Passed: true}
	}
	if gw == nil || ops == nil {
		present, missing := gatewayLLMUpstreamPolicy, opsEgressPolicy
		if gw == nil {
			present, missing = opsEgressPolicy, gatewayLLMUpstreamPolicy
		}
		return Decision{Passed: false, Reason: fmt.Sprintf(
			"NETWORK_POLICY_CLUSTER_CIDR_ASYMMETRY: NetworkPolicy %q is rendered but its "+
				"cluster-CIDR/IMDS symmetry counterpart %q is absent; §13.2 (NET-065) "+
				"requires both lenny-system outbound-HTTPS surfaces to render an identical "+
				"cluster-CIDR and IMDS except list",
			present, missing,
		)}
	}

	private := canonicalSet(privateRangeCIDRs)
	gwV4, gwV6 := broadEgressExcepts(gw)
	opsV4, opsV6 := broadEgressExcepts(ops)

	// The cluster-CIDR/IMDS exclusions are every `except` entry that is
	// not a shared private-range CIDR — i.e. the IMDS addresses plus the
	// discovered cluster pod/service CIDRs. §13.2 (NET-065) requires
	// these to be symmetric across the two surfaces.
	for _, fam := range []struct {
		name   string
		gwSet  map[string]bool
		opsSet map[string]bool
	}{
		{"IPv4", gwV4, opsV4},
		{"IPv6", gwV6, opsV6},
	} {
		gwClusterIMDS := nonPrivateExcepts(fam.gwSet, private)
		opsClusterIMDS := nonPrivateExcepts(fam.opsSet, private)
		if cidr, ok := firstMissing(gwClusterIMDS, opsClusterIMDS); ok {
			return clusterCIDRAsymmetryDecision(cidr, fam.name, gatewayLLMUpstreamPolicy, opsEgressPolicy)
		}
		if cidr, ok := firstMissing(opsClusterIMDS, gwClusterIMDS); ok {
			return clusterCIDRAsymmetryDecision(cidr, fam.name, opsEgressPolicy, gatewayLLMUpstreamPolicy)
		}
	}
	return Decision{Passed: true}
}

// nonPrivateExcepts returns the members of an except set that are not
// shared private-range CIDRs — the cluster-CIDR and IMDS exclusions
// NET-065 governs.
func nonPrivateExcepts(set, private map[string]bool) map[string]bool {
	out := make(map[string]bool)
	for cidr := range set {
		if !private[cidr] {
			out[cidr] = true
		}
	}
	return out
}

// firstMissing returns a CIDR present in want but absent from have, in
// a deterministic order, and whether such a CIDR exists.
func firstMissing(want, have map[string]bool) (string, bool) {
	for _, cidr := range sortedCIDRs(want) {
		if !have[cidr] {
			return cidr, true
		}
	}
	return "", false
}

// clusterCIDRAsymmetryDecision builds the NET-065 failure for a
// cluster-CIDR or IMDS exclusion present on one surface but missing
// from the other.
func clusterCIDRAsymmetryDecision(cidr, family, presentRule, missingRule string) Decision {
	return Decision{Passed: false, Reason: fmt.Sprintf(
		"NETWORK_POLICY_CLUSTER_CIDR_ASYMMETRY: cluster-CIDR/IMDS except entry %s is "+
			"present on %q but missing from %q on the %s ipBlock peer; §13.2 (NET-065) "+
			"requires the cluster pod/service CIDR and IMDS exclusions to be identical "+
			"across %q and %q so neither outbound-HTTPS surface can dial in-cluster pods",
		cidr, presentRule, missingRule, family, gatewayLLMUpstreamPolicy, opsEgressPolicy,
	)}
}
