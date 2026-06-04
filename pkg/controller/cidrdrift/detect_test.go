// SPDX-License-Identifier: MIT

package cidrdrift_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/controller/cidrdrift"
)

func TestIsBroadInternetPeer(t *testing.T) {
	cases := []struct {
		cidr string
		want bool
	}{
		{"0.0.0.0/0", true},
		{"::/0", true},
		{"10.0.0.0/8", false},
		{"10.244.0.0/16", false},
		{"fd00::/8", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := cidrdrift.IsBroadInternetPeer(cidrdrift.IPBlockPeer{CIDR: tc.cidr}); got != tc.want {
			t.Errorf("IsBroadInternetPeer(%q) = %v, want %v", tc.cidr, got, tc.want)
		}
	}
}

func TestDetectFindsDriftWhenExceptMissesNodeCIDR(t *testing.T) {
	// The installed except block covers RFC1918 aggregates but the
	// cluster's actual pod CIDR is in the CGNAT range, which the
	// except block does not list — this is the §13.2 NET-022 drift.
	policies := []cidrdrift.PolicyEgress{{
		Namespace: "lenny-agents",
		Name:      "allow-pod-egress-internet",
		IPBlocks: []cidrdrift.IPBlockPeer{{
			CIDR:   "0.0.0.0/0",
			Except: []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
		}},
	}}
	clusterCIDRs := []string{"100.64.1.0/24"}

	findings := cidrdrift.Detect(clusterCIDRs, policies)
	if len(findings) != 1 {
		t.Fatalf("Detect found %d drift findings, want 1: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Policy != "allow-pod-egress-internet" || f.ClusterCIDR != "100.64.1.0/24" {
		t.Errorf("finding = %+v, want policy allow-pod-egress-internet / cidr 100.64.1.0/24", f)
	}
	if f.Family != "ipv4" {
		t.Errorf("finding family = %q, want ipv4", f.Family)
	}
}

func TestDetectNoDriftWhenExceptExactlyCoversNodeCIDR(t *testing.T) {
	policies := []cidrdrift.PolicyEgress{{
		Namespace: "lenny-agents",
		Name:      "allow-pod-egress-internet",
		IPBlocks: []cidrdrift.IPBlockPeer{{
			CIDR:   "0.0.0.0/0",
			Except: []string{"10.244.0.0/16", "169.254.169.254/32"},
		}},
	}}
	clusterCIDRs := []string{"10.244.0.0/16"}

	if findings := cidrdrift.Detect(clusterCIDRs, policies); len(findings) != 0 {
		t.Errorf("Detect found drift when except exactly covers the node CIDR: %+v", findings)
	}
}

func TestDetectNoDriftWhenExceptSupernetCoversNodeCIDR(t *testing.T) {
	// The except block lists a wide 10.0.0.0/8 aggregate; the actual
	// per-node CIDR is a 10.244.0.0/24 subnet fully inside it. A
	// supernet exclusion covers the narrower cluster CIDR.
	policies := []cidrdrift.PolicyEgress{{
		Namespace: "lenny-agents",
		Name:      "allow-pod-egress-internet",
		IPBlocks: []cidrdrift.IPBlockPeer{{
			CIDR:   "0.0.0.0/0",
			Except: []string{"10.0.0.0/8"},
		}},
	}}
	clusterCIDRs := []string{"10.244.0.0/24", "10.244.1.0/24"}

	if findings := cidrdrift.Detect(clusterCIDRs, policies); len(findings) != 0 {
		t.Errorf("Detect found drift when a supernet except covers the node CIDRs: %+v", findings)
	}
}

func TestDetectDriftWhenExceptBlockEmpty(t *testing.T) {
	// A broad-internet egress rule with an empty except block leaves
	// agent pods able to reach every cluster IP — drift on every CIDR.
	policies := []cidrdrift.PolicyEgress{{
		Namespace: "lenny-agents",
		Name:      "allow-pod-egress-internet",
		IPBlocks:  []cidrdrift.IPBlockPeer{{CIDR: "0.0.0.0/0"}},
	}}
	clusterCIDRs := []string{"10.244.0.0/16", "10.245.0.0/16"}

	findings := cidrdrift.Detect(clusterCIDRs, policies)
	if len(findings) != 2 {
		t.Fatalf("Detect found %d findings for an empty except block, want 2: %+v", len(findings), findings)
	}
}

func TestDetectSkipsPoliciesWithoutBroadEgress(t *testing.T) {
	// allow-pod-egress-base is allowlist-only (no 0.0.0.0/0 peer); it
	// is not subject to the cluster-CIDR audit even when a node CIDR
	// is otherwise uncovered.
	policies := []cidrdrift.PolicyEgress{{
		Namespace: "lenny-agents",
		Name:      "allow-pod-egress-base",
		IPBlocks: []cidrdrift.IPBlockPeer{{
			CIDR: "10.96.0.1/32",
		}},
	}}
	clusterCIDRs := []string{"10.244.0.0/16"}

	if findings := cidrdrift.Detect(clusterCIDRs, policies); len(findings) != 0 {
		t.Errorf("Detect flagged an allowlist-only policy: %+v", findings)
	}
}

func TestDetectIPv6DriftRequiresSameFamilyExcept(t *testing.T) {
	// §13.2 NET-062: an IPv6 cluster CIDR must be excepted on the ::/0
	// peer. An IPv4-only except block does not cover an IPv6 cluster
	// CIDR even if a ::/0 peer is present.
	policies := []cidrdrift.PolicyEgress{{
		Namespace: "lenny-agents",
		Name:      "allow-pod-egress-internet",
		IPBlocks: []cidrdrift.IPBlockPeer{
			{CIDR: "0.0.0.0/0", Except: []string{"10.244.0.0/16"}},
			{CIDR: "::/0", Except: []string{}},
		},
	}}
	clusterCIDRs := []string{"fd00:10:244::/64"}

	findings := cidrdrift.Detect(clusterCIDRs, policies)
	if len(findings) != 1 {
		t.Fatalf("Detect found %d findings for an uncovered IPv6 CIDR, want 1: %+v", len(findings), findings)
	}
	if findings[0].Family != "ipv6" {
		t.Errorf("finding family = %q, want ipv6", findings[0].Family)
	}
}

func TestDetectIPv6NoDriftWhenIPv6ExceptCovers(t *testing.T) {
	policies := []cidrdrift.PolicyEgress{{
		Namespace: "lenny-agents",
		Name:      "allow-pod-egress-internet",
		IPBlocks: []cidrdrift.IPBlockPeer{
			{CIDR: "0.0.0.0/0", Except: []string{"10.244.0.0/16"}},
			{CIDR: "::/0", Except: []string{"fd00:10:244::/64"}},
		},
	}}
	clusterCIDRs := []string{"10.244.0.0/16", "fd00:10:244::/64"}

	if findings := cidrdrift.Detect(clusterCIDRs, policies); len(findings) != 0 {
		t.Errorf("Detect found drift when both families are covered: %+v", findings)
	}
}

func TestDetectCrossFamilyExceptDoesNotCover(t *testing.T) {
	// An IPv4 except entry mistakenly placed on the ::/0 peer must not
	// be credited as covering an IPv4 cluster CIDR.
	policies := []cidrdrift.PolicyEgress{{
		Namespace: "lenny-agents",
		Name:      "allow-pod-egress-internet",
		IPBlocks: []cidrdrift.IPBlockPeer{
			{CIDR: "0.0.0.0/0", Except: []string{}},
			{CIDR: "::/0", Except: []string{"10.244.0.0/16"}},
		},
	}}
	clusterCIDRs := []string{"10.244.0.0/16"}

	findings := cidrdrift.Detect(clusterCIDRs, policies)
	if len(findings) != 1 {
		t.Fatalf("Detect found %d findings, want 1 (cross-family except must not count): %+v", len(findings), findings)
	}
}

func TestNormalizeCIDR(t *testing.T) {
	got, err := cidrdrift.NormalizeCIDR("10.244.5.3/16")
	if err != nil {
		t.Fatalf("NormalizeCIDR returned error: %v", err)
	}
	if got != "10.244.0.0/16" {
		t.Errorf("NormalizeCIDR(10.244.5.3/16) = %q, want 10.244.0.0/16", got)
	}
	if _, err := cidrdrift.NormalizeCIDR("not-a-cidr"); err == nil {
		t.Error("NormalizeCIDR should reject a non-CIDR string")
	}
}

// spec: §13.2 NET-022 — Detect stamps pod_cidr and DetectServiceCIDRs
// stamps service_cidr on the Finding.Field so the metric records the
// right drift category.
func TestDetectStampsField(t *testing.T) {
	policies := []cidrdrift.PolicyEgress{{
		Namespace: "lenny-agents",
		Name:      "allow-pod-egress-internet",
		IPBlocks:  []cidrdrift.IPBlockPeer{{CIDR: "0.0.0.0/0"}},
	}}
	pod := cidrdrift.Detect([]string{"100.64.0.0/24"}, policies)
	if len(pod) != 1 || pod[0].Field != cidrdrift.FieldPodCIDR {
		t.Fatalf("Detect field = %+v, want one finding with field pod_cidr", pod)
	}
	svc := cidrdrift.DetectServiceCIDRs([]string{"100.64.0.1/32"}, policies)
	if len(svc) != 1 || svc[0].Field != cidrdrift.FieldServiceCIDR {
		t.Fatalf("DetectServiceCIDRs field = %+v, want one finding with field service_cidr", svc)
	}
}

// spec: §13.2 NET-022 — the `policy` metric label collapses the three
// audited surfaces to internet | gateway-llm-upstream | ops-egress and
// leaves any other policy name unchanged.
func TestCanonicalPolicyLabel(t *testing.T) {
	cases := map[string]string{
		"allow-gateway-egress-llm-upstream": "gateway-llm-upstream",
		"lenny-ops-egress":                  "ops-egress",
		"allow-pod-egress-internet":         "internet",
		"some-bespoke-broad-egress":         "some-bespoke-broad-egress",
	}
	for name, want := range cases {
		if got := cidrdrift.CanonicalPolicyLabel(name); got != want {
			t.Errorf("CanonicalPolicyLabel(%q) = %q, want %q", name, got, want)
		}
	}
}
