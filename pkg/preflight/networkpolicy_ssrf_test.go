// SPDX-License-Identifier: MIT

package preflight_test

import (
	"strings"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"

	"github.com/lennylabs/lenny/pkg/preflight"
)

// fullPrivateV4 and fullPrivateV6 are the §13.2 NET-057 default
// egressCIDRs.excludePrivate entries, partitioned by family.
var (
	fullPrivateV4 = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"}
	fullPrivateV6 = []string{"fc00::/7", "fe80::/10"}
)

// ssrfPolicy builds an outbound-HTTPS NetworkPolicy with the §13.2
// dual-family ipBlock idiom: a 0.0.0.0/0 peer carrying v4Except and a
// ::/0 peer carrying v6Except.
func ssrfPolicy(name string, v4Except, v6Except []string) networkingv1.NetworkPolicy {
	p := netPolicy(name)
	ipBlockEgress(p, "0.0.0.0/0", v4Except...)
	ipBlockEgress(p, "::/0", v6Except...)
	return *p
}

func TestCheckSSRFPrivateRangeParityPassesWhenBothRulesCarryFullList(t *testing.T) {
	gw := ssrfPolicy("allow-gateway-egress-llm-upstream",
		append(append([]string{}, fullPrivateV4...), "169.254.169.254/32", "100.100.100.200/32"),
		append(append([]string{}, fullPrivateV6...), "fd00:ec2::254/128"))
	ops := ssrfPolicy("lenny-ops-egress",
		append(append([]string{}, fullPrivateV4...), "169.254.169.254/32", "100.100.100.200/32"),
		append(append([]string{}, fullPrivateV6...), "fd00:ec2::254/128"))
	d := preflight.CheckSSRFPrivateRangeParity([]networkingv1.NetworkPolicy{gw, ops})
	if !d.Passed {
		t.Errorf("check failed though both surfaces carry the full private-range list: %s", d.Reason)
	}
}

func TestCheckSSRFPrivateRangeParityPassesWhenNeitherRendered(t *testing.T) {
	// A chart slice before either egress rule lands: nothing to compare.
	other := netPolicy("allow-controller-egress")
	d := preflight.CheckSSRFPrivateRangeParity([]networkingv1.NetworkPolicy{*other})
	if !d.Passed {
		t.Errorf("check failed though neither SSRF surface is rendered: %s", d.Reason)
	}
}

func TestCheckSSRFPrivateRangeParityFailsWhenGatewayDropsAnEntry(t *testing.T) {
	// NET-057: the gateway rule is missing 192.168.0.0/16 — an edit that
	// weakened only one surface.
	gw := ssrfPolicy("allow-gateway-egress-llm-upstream",
		[]string{"10.0.0.0/8", "172.16.0.0/12", "169.254.0.0/16"}, fullPrivateV6)
	ops := ssrfPolicy("lenny-ops-egress", fullPrivateV4, fullPrivateV6)
	d := preflight.CheckSSRFPrivateRangeParity([]networkingv1.NetworkPolicy{gw, ops})
	if d.Passed {
		t.Fatal("check passed though the gateway rule dropped a private-range CIDR")
	}
	if !strings.Contains(d.Reason, "NET-057") || !strings.Contains(d.Reason, "192.168.0.0/16") {
		t.Errorf("reason %q does not report the missing 192.168.0.0/16 (NET-057)", d.Reason)
	}
}

func TestCheckSSRFPrivateRangeParityFailsWhenOnlyOneSurfaceRendered(t *testing.T) {
	// NET-057: a single rendered surface cannot be in parity.
	gw := ssrfPolicy("allow-gateway-egress-llm-upstream", fullPrivateV4, fullPrivateV6)
	d := preflight.CheckSSRFPrivateRangeParity([]networkingv1.NetworkPolicy{gw})
	if d.Passed {
		t.Fatal("check passed with only the gateway SSRF surface rendered")
	}
	if !strings.Contains(d.Reason, "lenny-ops-egress") {
		t.Errorf("reason %q does not name the missing counterpart", d.Reason)
	}
}

func TestCheckSSRFPrivateRangeParityFailsWhenIPv6EntryMissing(t *testing.T) {
	// NET-062: the IPv6 entry must be on the ::/0 peer, not the v4 peer.
	gw := ssrfPolicy("allow-gateway-egress-llm-upstream", fullPrivateV4, []string{"fc00::/7"})
	ops := ssrfPolicy("lenny-ops-egress", fullPrivateV4, fullPrivateV6)
	d := preflight.CheckSSRFPrivateRangeParity([]networkingv1.NetworkPolicy{gw, ops})
	if d.Passed {
		t.Fatal("check passed though the gateway ::/0 peer is missing fe80::/10")
	}
	if !strings.Contains(d.Reason, "fe80::/10") {
		t.Errorf("reason %q does not report the missing IPv6 entry", d.Reason)
	}
}

func TestCheckClusterCIDRSymmetryPassesWhenIMDSSymmetric(t *testing.T) {
	// Both surfaces carry an identical cluster-CIDR + IMDS except set.
	gw := ssrfPolicy("allow-gateway-egress-llm-upstream",
		append(append([]string{}, fullPrivateV4...), "100.64.0.0/10", "169.254.169.254/32"),
		append(append([]string{}, fullPrivateV6...), "fd00:ec2::254/128"))
	ops := ssrfPolicy("lenny-ops-egress",
		append(append([]string{}, fullPrivateV4...), "100.64.0.0/10", "169.254.169.254/32"),
		append(append([]string{}, fullPrivateV6...), "fd00:ec2::254/128"))
	d := preflight.CheckClusterCIDRSymmetry([]networkingv1.NetworkPolicy{gw, ops})
	if !d.Passed {
		t.Errorf("check failed though cluster-CIDR and IMDS exclusions are symmetric: %s", d.Reason)
	}
}

func TestCheckClusterCIDRSymmetryFailsWhenOpsMissesClusterCIDR(t *testing.T) {
	// NET-065: the gateway carries the CGNAT pod CIDR but lenny-ops-egress
	// does not — a webhook URL resolving to an in-cluster pod IP would be
	// reachable from the operability plane.
	gw := ssrfPolicy("allow-gateway-egress-llm-upstream",
		append(append([]string{}, fullPrivateV4...), "100.64.0.0/10", "169.254.169.254/32"),
		fullPrivateV6)
	ops := ssrfPolicy("lenny-ops-egress",
		append(append([]string{}, fullPrivateV4...), "169.254.169.254/32"),
		fullPrivateV6)
	d := preflight.CheckClusterCIDRSymmetry([]networkingv1.NetworkPolicy{gw, ops})
	if d.Passed {
		t.Fatal("check passed though lenny-ops-egress is missing the cluster pod CIDR")
	}
	if !strings.Contains(d.Reason, "NET-065") || !strings.Contains(d.Reason, "100.64.0.0/10") {
		t.Errorf("reason %q does not report the asymmetric 100.64.0.0/10 (NET-065)", d.Reason)
	}
}

func TestCheckClusterCIDRSymmetryFailsWhenIMDSAsymmetric(t *testing.T) {
	// NET-065: the IMDS exclusions must be identical across the surfaces.
	gw := ssrfPolicy("allow-gateway-egress-llm-upstream",
		append(append([]string{}, fullPrivateV4...), "169.254.169.254/32", "100.100.100.200/32"),
		fullPrivateV6)
	ops := ssrfPolicy("lenny-ops-egress",
		append(append([]string{}, fullPrivateV4...), "169.254.169.254/32"),
		fullPrivateV6)
	d := preflight.CheckClusterCIDRSymmetry([]networkingv1.NetworkPolicy{gw, ops})
	if d.Passed {
		t.Fatal("check passed though the Alibaba IMDS exclusion is on the gateway only")
	}
	if !strings.Contains(d.Reason, "100.100.100.200/32") {
		t.Errorf("reason %q does not report the asymmetric IMDS entry", d.Reason)
	}
}

func TestCheckClusterCIDRSymmetryPassesWhenNeitherRendered(t *testing.T) {
	other := netPolicy("allow-pgbouncer")
	d := preflight.CheckClusterCIDRSymmetry([]networkingv1.NetworkPolicy{*other})
	if !d.Passed {
		t.Errorf("check failed though neither SSRF surface is rendered: %s", d.Reason)
	}
}
