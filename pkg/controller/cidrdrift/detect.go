// SPDX-License-Identifier: MIT

// Package cidrdrift holds the §13.2 NET-022 cluster-CIDR drift
// detector. The detector is a leader-elected goroutine that
// periodically compares the cluster's actual pod CIDRs (aggregated
// from Node.spec.podCIDR / podCIDRs) against the `except` ipBlock
// entries installed on the broad-internet egress NetworkPolicies in
// agent namespaces.
//
// When the installed `except` exclusions do not cover an actual
// cluster pod CIDR, agent pods granted broad egress can reach internal
// cluster IPs, which is a lateral-movement and internal-service
// discovery risk (§13.2 NET-002/NET-022). The detector makes the drift
// observable by incrementing lenny_network_policy_cidr_drift_total and
// letting the NetworkPolicyCIDRDrift alert fire. It does not auto-patch
// NetworkPolicies: §13.2 deliberately keeps NetworkPolicy write RBAC
// off the controller, so remediation stays a `helm upgrade` the
// operator runs.
//
// This file holds the pure comparison logic. detector.go holds the
// controller-runtime adapter that lists Nodes and NetworkPolicies and
// drives the metric.
package cidrdrift

import (
	"fmt"
	"net"
)

// broadIPv4CIDR and broadIPv6CIDR are the two `cidr` values that mark
// an ipBlock peer as a broad-internet egress rule. §13.2 NET-062
// renders broad egress as two parallel peers, one per address family;
// either one obliges the policy to carry the cluster-CIDR exclusions.
const (
	broadIPv4CIDR = "0.0.0.0/0"
	broadIPv6CIDR = "::/0"
)

// IPBlockPeer is one ipBlock egress peer reduced to the two fields the
// drift comparison needs: the peer's own CIDR and its `except` list.
// It mirrors networkingv1.IPBlock without binding the comparison logic
// to the Kubernetes API types, so the comparison is unit-testable in
// isolation.
type IPBlockPeer struct {
	// CIDR is the ipBlock peer's `cidr`, for example "0.0.0.0/0".
	CIDR string
	// Except is the ipBlock peer's `except` list — the CIDRs carved out
	// of CIDR. A broad-internet peer must list every cluster pod CIDR
	// here so agent pods cannot reach in-cluster IPs.
	Except []string
}

// PolicyEgress is one NetworkPolicy reduced to its identity and the
// ipBlock peers across all of its egress rules.
type PolicyEgress struct {
	// Namespace is the NetworkPolicy's namespace.
	Namespace string
	// Name is the NetworkPolicy's name.
	Name string
	// IPBlocks is every ipBlock peer found across the policy's egress
	// rules. Peers that are pod or namespace selectors carry no ipBlock
	// and are absent here.
	IPBlocks []IPBlockPeer
}

// IsBroadInternetPeer reports whether an ipBlock peer is a broad
// public-internet egress rule — the kind §13.2 NET-022 requires to
// carry cluster-CIDR exclusions. A peer qualifies when its CIDR is
// 0.0.0.0/0 or ::/0.
func IsBroadInternetPeer(p IPBlockPeer) bool {
	return p.CIDR == broadIPv4CIDR || p.CIDR == broadIPv6CIDR
}

// HasBroadInternetEgress reports whether any of the policy's ipBlock
// peers is a broad public-internet egress rule. A policy with no broad
// egress is not subject to the NET-022 cluster-CIDR audit.
func HasBroadInternetEgress(pe PolicyEgress) bool {
	for _, b := range pe.IPBlocks {
		if IsBroadInternetPeer(b) {
			return true
		}
	}
	return false
}

// Finding is one detected drift: a cluster pod CIDR that is not
// covered by the `except` block of a broad-internet egress
// NetworkPolicy. Each Finding maps directly to one increment of
// lenny_network_policy_cidr_drift_total.
type Finding struct {
	// Namespace is the drifting NetworkPolicy's namespace.
	Namespace string
	// Policy is the drifting NetworkPolicy's name.
	Policy string
	// ClusterCIDR is the actual cluster pod CIDR that the policy's
	// `except` block fails to cover.
	ClusterCIDR string
	// Family is "ipv4" or "ipv6" — the address family of ClusterCIDR.
	// It selects which parallel ipBlock peer should have carried the
	// exclusion (§13.2 NET-062).
	Family string
}

// addressFamily returns "ipv4" or "ipv6" for a parsed CIDR, or "" when
// the string is not a valid CIDR.
func addressFamily(cidr string) string {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return ""
	}
	if ip.To4() != nil {
		return "ipv4"
	}
	return "ipv6"
}

// cidrCoveredByExcept reports whether the cluster pod CIDR `want` is
// covered by the same-family `except` block of a broad-internet peer.
// Coverage holds when `want` equals an `except` entry exactly, or when
// an `except` entry is a supernet that fully contains `want`. The
// supernet case handles a chart that excludes a wide aggregate (for
// example 10.0.0.0/8) that subsumes the per-node /24 CIDRs.
//
// Only `except` entries on a same-family broad peer count: §13.2
// NET-062 forbids cross-family `except` entries, so an IPv4 cluster
// CIDR must be covered by an `except` on the 0.0.0.0/0 peer, never by
// one on the ::/0 peer.
func cidrCoveredByExcept(want string, peers []IPBlockPeer) bool {
	wantIP, wantNet, err := net.ParseCIDR(want)
	if err != nil {
		return false
	}
	wantFamily := addressFamily(want)
	wantOnes, _ := wantNet.Mask.Size()

	for _, peer := range peers {
		if !IsBroadInternetPeer(peer) {
			continue
		}
		if addressFamily(peer.CIDR) != wantFamily {
			continue
		}
		for _, ex := range peer.Except {
			exIP, exNet, err := net.ParseCIDR(ex)
			if err != nil {
				continue
			}
			if addressFamily(ex) != wantFamily {
				continue
			}
			// Exact match: the except entry is the cluster CIDR itself.
			if exIP.Equal(wantIP) {
				exOnes, _ := exNet.Mask.Size()
				if exOnes == wantOnes {
					return true
				}
			}
			// Supernet match: the except entry is a wider block that
			// fully contains the cluster CIDR.
			exOnes, _ := exNet.Mask.Size()
			if exOnes <= wantOnes && exNet.Contains(wantIP) {
				return true
			}
		}
	}
	return false
}

// Detect compares the actual cluster pod CIDRs against the installed
// broad-internet egress NetworkPolicies and returns one Finding for
// every (policy, cluster CIDR) pair where the policy's `except` block
// fails to cover the CIDR.
//
// Inputs:
//
//   - clusterPodCIDRs is the deduplicated set of pod CIDRs aggregated
//     from every Node's spec.podCIDR / podCIDRs.
//   - policies is every NetworkPolicy the detector read from agent
//     namespaces. Policies with no broad-internet egress peer are
//     skipped — they are allowlist-only and carry no `except` block.
//
// A policy with broad egress but an empty `except` block drifts on
// every cluster CIDR: the audit treats a missing exclusion exactly
// like a stale one.
func Detect(clusterPodCIDRs []string, policies []PolicyEgress) []Finding {
	var findings []Finding
	for _, pe := range policies {
		if !HasBroadInternetEgress(pe) {
			continue
		}
		for _, cidr := range clusterPodCIDRs {
			fam := addressFamily(cidr)
			if fam == "" {
				// A node reporting an unparseable CIDR is a cluster-side
				// fault, not policy drift; skip it rather than flag a
				// false positive against the policy.
				continue
			}
			if !cidrCoveredByExcept(cidr, pe.IPBlocks) {
				findings = append(findings, Finding{
					Namespace:   pe.Namespace,
					Policy:      pe.Name,
					ClusterCIDR: cidr,
					Family:      fam,
				})
			}
		}
	}
	return findings
}

// NormalizeCIDR validates a CIDR string and returns it in canonical
// masked form, so that 10.1.2.3/24 and 10.1.2.0/24 compare equal when
// aggregating node CIDRs. It returns an error when the string is not a
// valid CIDR.
func NormalizeCIDR(cidr string) (string, error) {
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("parse CIDR %q: %w", cidr, err)
	}
	return n.String(), nil
}
