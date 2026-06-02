// SPDX-License-Identifier: MIT

package preflight

import (
	"fmt"
	"net"
)

// ClusterCIDRConfig carries the §13.2 NET-040 / NET-022 CIDR values the
// chart configures so the preflight check can validate them against the
// cluster's actual addressing.
type ClusterCIDRConfig struct {
	// KubeAPIServerCIDR is the chart kubeApiServerCIDR value: the CIDR
	// the gateway is allowed to reach the kube-apiserver Service on. It
	// is always the cluster service CIDR (§13.2 NET-040).
	KubeAPIServerCIDR string
	// ExcludeClusterServiceCIDR is the chart
	// egressCIDRs.excludeClusterServiceCIDR value: the cluster service
	// CIDR excluded from the broad external-HTTPS egress (§13.2 NET-022).
	ExcludeClusterServiceCIDR string
	// ExcludeClusterPodCIDR is the chart egressCIDRs.excludeClusterPodCIDR
	// value: the cluster pod CIDR excluded from the broad external-HTTPS
	// egress (§13.2 NET-022).
	ExcludeClusterPodCIDR string
}

// CheckClusterCIDRDiscovery validates the §13.2 NET-040 / NET-022
// cluster-CIDR invariants against the discovered cluster addressing.
// It enforces, fail-closed:
//
//   - The kube-apiserver Service ClusterIP falls within the configured
//     kubeApiServerCIDR (NET-040). A mismatch means the gateway's
//     allow-egress-to-apiserver rule would not admit the apiserver, so
//     the gateway cannot reach the control plane after install.
//   - The same ClusterIP falls within egressCIDRs.excludeClusterServiceCIDR
//     (NET-022). A mismatch means the broad external-HTTPS egress does
//     not exclude the service CIDR, so a tenant-influenced URL resolving
//     to an in-cluster Service IP could reach platform pods (SSRF).
//
// It additionally validates, as a warning, that every node-reported pod
// CIDR falls within egressCIDRs.excludeClusterPodCIDR. Managed clusters
// frequently do not populate node.spec.podCIDR (the pod CIDR lives in
// the CNI, not the Node object); when no node reports a pod CIDR the
// check passes with an advisory note rather than failing, mirroring the
// CIDR drift detector's tolerance for that topology.
//
// spec: §13.2 lines 230-262 (kubeApiServerCIDR ClusterIP validation),
// §13.2 lines 416/446-450 (excludeClusterPodCIDR / excludeClusterServiceCIDR
// must match the discovered cluster CIDRs). F-13.2.13.
func CheckClusterCIDRDiscovery(apiServerClusterIP string, nodePodCIDRs []string, cfg ClusterCIDRConfig) Decision {
	ip := net.ParseIP(apiServerClusterIP)
	if ip == nil {
		return Decision{Passed: false, Reason: fmt.Sprintf(
			"CLUSTER_CIDR_DISCOVERY_FAILED: the kubernetes.default Service ClusterIP %q is not a valid IP; "+
				"§13.2 (NET-040) requires the kube-apiserver Service ClusterIP to validate against kubeApiServerCIDR",
			apiServerClusterIP,
		)}
	}

	for _, c := range []struct {
		cidr  string
		field string
		netID string
	}{
		{cfg.KubeAPIServerCIDR, "kubeApiServerCIDR", "NET-040"},
		{cfg.ExcludeClusterServiceCIDR, "egressCIDRs.excludeClusterServiceCIDR", "NET-022"},
	} {
		_, network, err := net.ParseCIDR(c.cidr)
		if err != nil {
			return Decision{Passed: false, Reason: fmt.Sprintf(
				"CLUSTER_CIDR_DISCOVERY_FAILED: %s value %q is not a valid CIDR; §13.2 (%s)",
				c.field, c.cidr, c.netID,
			)}
		}
		if !network.Contains(ip) {
			return Decision{Passed: false, Reason: fmt.Sprintf(
				"CLUSTER_CIDR_MISMATCH: the kubernetes.default Service ClusterIP %s is not within %s=%q; "+
					"§13.2 (%s) requires the kube-apiserver Service ClusterIP to fall within the configured "+
					"service CIDR — discover it with `kubectl get svc kubernetes -n default -o "+
					"jsonpath='{.spec.clusterIP}'` and set the value to the full cluster service CIDR",
				ip, c.field, c.cidr, c.netID,
			)}
		}
	}

	// Pod-CIDR membership is advisory: a managed cluster may report no
	// node pod CIDR, in which case there is nothing to validate.
	if len(nodePodCIDRs) == 0 {
		return Decision{Passed: true, Reason: "WARNING: no node reports a pod CIDR (managed CNI); " +
			"egressCIDRs.excludeClusterPodCIDR could not be validated against the discovered pod CIDR (§13.2 NET-022)"}
	}
	_, podExclude, err := net.ParseCIDR(cfg.ExcludeClusterPodCIDR)
	if err != nil {
		return Decision{Passed: false, Reason: fmt.Sprintf(
			"CLUSTER_CIDR_DISCOVERY_FAILED: egressCIDRs.excludeClusterPodCIDR value %q is not a valid CIDR; §13.2 (NET-022)",
			cfg.ExcludeClusterPodCIDR,
		)}
	}
	for _, pc := range nodePodCIDRs {
		if !cidrWithin(pc, podExclude) {
			return Decision{Passed: false, Reason: fmt.Sprintf(
				"CLUSTER_CIDR_MISMATCH: node pod CIDR %s is not within egressCIDRs.excludeClusterPodCIDR=%q; "+
					"§13.2 (NET-022) requires the broad external-HTTPS egress to exclude the cluster pod CIDR, "+
					"otherwise a tenant-influenced URL resolving to an in-cluster pod IP could reach platform pods",
				pc, cfg.ExcludeClusterPodCIDR,
			)}
		}
	}
	return Decision{Passed: true}
}

// cidrWithin reports whether the child CIDR is fully contained in
// parent: the child's network address must fall inside parent and the
// child prefix must be at least as specific as parent's. An unparseable
// child is treated as not contained (the caller fails the check).
func cidrWithin(child string, parent *net.IPNet) bool {
	childIP, childNet, err := net.ParseCIDR(child)
	if err != nil {
		return false
	}
	if !parent.Contains(childIP) {
		return false
	}
	childOnes, childBits := childNet.Mask.Size()
	parentOnes, parentBits := parent.Mask.Size()
	if childBits != parentBits {
		return false // different address family
	}
	return childOnes >= parentOnes
}
