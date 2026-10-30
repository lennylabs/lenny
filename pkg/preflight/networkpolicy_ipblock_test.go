// SPDX-License-Identifier: MIT

package preflight_test

import (
	"strings"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"

	"github.com/lennylabs/lenny/pkg/preflight"
)

// ipBlockEgress adds an egress rule with a single ipBlock peer.
func ipBlockEgress(p *networkingv1.NetworkPolicy, cidr string, except ...string) {
	p.Spec.Egress = append(p.Spec.Egress, networkingv1.NetworkPolicyEgressRule{
		To: []networkingv1.NetworkPolicyPeer{{
			IPBlock: &networkingv1.IPBlock{CIDR: cidr, Except: except},
		}},
		Ports: []networkingv1.NetworkPolicyPort{tcpPort(443)},
	})
}

func TestCheckIPBlockFamilyParityPassesOnFamilyMatchedBlocks(t *testing.T) {
	// The §13.2 dual-family idiom: one IPv4 peer and one IPv6 peer, each
	// carrying only same-family except entries.
	p := netPolicy("allow-gateway-egress-llm-upstream")
	ipBlockEgress(p, "0.0.0.0/0", "10.0.0.0/8", "169.254.169.254/32")
	ipBlockEgress(p, "::/0", "fc00::/7", "fd00:ec2::254/128")
	d := preflight.CheckIPBlockFamilyParity([]networkingv1.NetworkPolicy{*p})
	if !d.Passed {
		t.Errorf("check failed a correctly partitioned dual-family policy: %s", d.Reason)
	}
}

func TestCheckIPBlockFamilyParityFailsOnIPv6ExceptInIPv4Block(t *testing.T) {
	// NET-062: an IPv6 except entry inside a 0.0.0.0/0 block is dropped
	// silently by lenient CNIs, producing an SSRF hole.
	p := netPolicy("allow-gateway-egress-llm-upstream")
	ipBlockEgress(p, "0.0.0.0/0", "10.0.0.0/8", "fc00::/7")
	d := preflight.CheckIPBlockFamilyParity([]networkingv1.NetworkPolicy{*p})
	if d.Passed {
		t.Fatal("check passed an IPv6 except entry inside a 0.0.0.0/0 ipBlock")
	}
	if !strings.Contains(d.Reason, "NET-062") {
		t.Errorf("reason %q does not cite NET-062", d.Reason)
	}
}

func TestCheckIPBlockFamilyParityFailsOnIPv4ExceptInIPv6Block(t *testing.T) {
	p := netPolicy("lenny-ops-egress")
	ipBlockEgress(p, "::/0", "169.254.169.254/32")
	d := preflight.CheckIPBlockFamilyParity([]networkingv1.NetworkPolicy{*p})
	if d.Passed {
		t.Fatal("check passed an IPv4 except entry inside a ::/0 ipBlock")
	}
	if !strings.Contains(d.Reason, "FAMILY_MISMATCH") {
		t.Errorf("reason %q does not report a family mismatch", d.Reason)
	}
}

func TestCheckIPBlockFamilyParityFailsOnInvalidCIDR(t *testing.T) {
	p := netPolicy("allow-gateway-egress-llm-upstream")
	ipBlockEgress(p, "0.0.0.0/0", "not-a-cidr")
	d := preflight.CheckIPBlockFamilyParity([]networkingv1.NetworkPolicy{*p})
	if d.Passed {
		t.Fatal("check passed an unparseable except entry")
	}
	if !strings.Contains(d.Reason, "INVALID_CIDR") {
		t.Errorf("reason %q does not report an invalid CIDR", d.Reason)
	}
}

func TestCheckIPBlockFamilyParityPassesOnPolicyWithoutIPBlocks(t *testing.T) {
	// A selector-only policy carries no ipBlock; the check is a no-op.
	p := netPolicy("allow-token-service")
	p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{
			{PodSelector: labelSel("lenny.dev/component", "pgbouncer")},
		},
		Ports: []networkingv1.NetworkPolicyPort{tcpPort(5432)},
	}}
	d := preflight.CheckIPBlockFamilyParity([]networkingv1.NetworkPolicy{*p})
	if !d.Passed {
		t.Errorf("check flagged a policy with no ipBlock peers: %s", d.Reason)
	}
}
