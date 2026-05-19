// SPDX-License-Identifier: MIT

package preflight

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
)

// The §13.2 NetworkPolicy names the SSRF-parity audits resolve. The
// gateway external-HTTPS egress and the operability-plane webhook
// egress are the two lenny-system surfaces that initiate outbound
// HTTPS to tenant-influenced URLs and therefore share one SSRF
// boundary (NET-057).
const (
	gatewayLLMUpstreamPolicy = "allow-gateway-egress-llm-upstream"
	opsEgressPolicy          = "lenny-ops-egress"
)

// privateRangeCIDRs is the §13.2 NET-057 shared private-range exclusion
// set: the RFC1918 ranges, IPv4 link-local, IPv6 ULA, and IPv6
// link-local CIDRs that every lenny-system NetworkPolicy with a broad
// public-internet egress rule must carry under `except`. It is the
// default of the egressCIDRs.excludePrivate Helm value.
var privateRangeCIDRs = map[string]bool{
	"10.0.0.0/8":     true,
	"172.16.0.0/12":  true,
	"192.168.0.0/16": true,
	"169.254.0.0/16": true,
	"fc00::/7":       true,
	"fe80::/10":      true,
}

// imdsCIDRs is the §13.2 NET-044/NET-065 cloud instance-metadata
// exclusion set: the AWS/GCP/Azure IPv4 IMDS address, the Alibaba Cloud
// IMDS address, and the AWS IPv6 IMDS address. It is the default of the
// egressCIDRs.excludeIMDS Helm value.
var imdsCIDRs = map[string]bool{
	"169.254.169.254/32": true,
	"100.100.100.200/32": true,
	"fd00:ec2::254/128":  true,
}

// cidrCanonical returns the canonical string form of a CIDR so that
// except-block comparisons are insensitive to spacing or
// non-canonical host bits. It returns the empty string when the CIDR
// does not parse.
func cidrCanonical(cidr string) string {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return ""
	}
	return prefix.Masked().String()
}

// canonicalSet maps each member of a known CIDR set to its canonical
// form, so membership tests survive non-canonical input.
func canonicalSet(known map[string]bool) map[string]bool {
	out := make(map[string]bool, len(known))
	for c := range known {
		if cc := cidrCanonical(c); cc != "" {
			out[cc] = true
		}
	}
	return out
}

// findPolicy returns the Lenny-rendered NetworkPolicy with the given
// name, or nil when it is absent. Names are unique per namespace; the
// SSRF surfaces this resolves all render into the release namespace.
func findPolicy(policies []networkingv1.NetworkPolicy, name string) *networkingv1.NetworkPolicy {
	for i := range policies {
		if policies[i].Name == name {
			return &policies[i]
		}
	}
	return nil
}

// broadEgressExcepts returns the `except` entries of every broad
// public-internet ipBlock egress peer of a policy, partitioned by
// address family. A broad peer is one whose cidr is 0.0.0.0/0 or ::/0.
func broadEgressExcepts(p *networkingv1.NetworkPolicy) (ipv4, ipv6 map[string]bool) {
	ipv4, ipv6 = map[string]bool{}, map[string]bool{}
	for _, rule := range p.Spec.Egress {
		for _, peer := range rule.To {
			if peer.IPBlock == nil {
				continue
			}
			cc := cidrCanonical(peer.IPBlock.CIDR)
			switch cc {
			case "0.0.0.0/0":
				for _, e := range peer.IPBlock.Except {
					if c := cidrCanonical(e); c != "" {
						ipv4[c] = true
					}
				}
			case "::/0":
				for _, e := range peer.IPBlock.Except {
					if c := cidrCanonical(e); c != "" {
						ipv6[c] = true
					}
				}
			}
		}
	}
	return ipv4, ipv6
}

// CheckSSRFPrivateRangeParity verifies the §13.2 NET-057 SSRF
// private-range parity invariant: every private-range CIDR in
// egressCIDRs.excludePrivate must appear in the `except` block of the
// same-family broad-internet ipBlock peer on both
// allow-gateway-egress-llm-upstream and lenny-ops-egress.
//
// The two rules are not required set-equal — the gateway legitimately
// adds cluster pod/service CIDR and IMDS exclusions — so the check is
// limited to private-range membership on both sides. It guards against
// a future edit that weakens only one of the two SSRF surfaces. When
// neither policy is rendered (a chart slice before either egress rule
// lands) the check passes; when exactly one is rendered the check still
// fails, because a single rendered surface cannot be in parity.
func CheckSSRFPrivateRangeParity(policies []networkingv1.NetworkPolicy) Decision {
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
			"NETWORK_POLICY_SSRF_PARITY_MISSING: NetworkPolicy %q is rendered but its "+
				"SSRF-parity counterpart %q is absent; §13.2 (NET-057) requires both "+
				"lenny-system outbound-HTTPS surfaces to render the shared private-range "+
				"except list, so the two cannot be in parity",
			present, missing,
		)}
	}

	want := canonicalSet(privateRangeCIDRs)
	gwV4, gwV6 := broadEgressExcepts(gw)
	opsV4, opsV6 := broadEgressExcepts(ops)

	for cidr := range want {
		fam := cidrFamily(cidr)
		gwSet, opsSet := gwV4, opsV4
		if fam == "IPv6" {
			gwSet, opsSet = gwV6, opsV6
		}
		if !gwSet[cidr] {
			return ssrfDriftDecision(cidr, fam, gatewayLLMUpstreamPolicy)
		}
		if !opsSet[cidr] {
			return ssrfDriftDecision(cidr, fam, opsEgressPolicy)
		}
	}
	return Decision{Passed: true}
}

// ssrfDriftDecision builds the NET-057 drift failure for a private-range
// CIDR missing from one of the two SSRF surfaces.
func ssrfDriftDecision(cidr, family, ruleName string) Decision {
	return Decision{Passed: false, Reason: fmt.Sprintf(
		"NETWORK_POLICY_SSRF_PARITY_DRIFT: SSRF private-range except list drift detected: "+
			"%s from egressCIDRs.excludePrivate is missing from %q's except block on the "+
			"%s ipBlock peer. Both %q and %q MUST render every private-range entry into "+
			"the same-family peer (§13.2 NET-057, NET-062)",
		cidr, ruleName, family, gatewayLLMUpstreamPolicy, opsEgressPolicy,
	)}
}

// sortedCIDRs returns the members of a CIDR set in a stable order, used
// only to make audit messages deterministic.
func sortedCIDRs(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
