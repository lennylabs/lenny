// SPDX-License-Identifier: MIT

package kind

import (
	"fmt"
	"net"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// EgressAdmitters returns, in input order, the names of the policies that
// select a pod carrying podLabels and admit its TCP egress to ip on port.
// A test uses it to prove which NetworkPolicy a live connection depends on,
// because a successful dial cannot tell one admitting policy from another.
func EgressAdmitters(policies []networkingv1.NetworkPolicy, podLabels map[string]string, ip net.IP, port int32) ([]string, error) {
	var names []string
	for i := range policies {
		policy := &policies[i]
		selector, err := metav1.LabelSelectorAsSelector(&policy.Spec.PodSelector)
		if err != nil {
			return nil, fmt.Errorf("policy %s selector: %w", policy.Name, err)
		}
		if !selector.Matches(labels.Set(podLabels)) || !governsEgress(policy) {
			continue
		}
		admits, err := policyAdmits(policy, ip, port)
		if err != nil {
			return nil, err
		}
		if admits {
			names = append(names, policy.Name)
		}
	}
	return names, nil
}

// IsEgressDefaultDeny reports whether policy selects every pod in its
// namespace and admits no egress.
func IsEgressDefaultDeny(policy *networkingv1.NetworkPolicy) bool {
	empty := len(policy.Spec.PodSelector.MatchLabels) == 0 && len(policy.Spec.PodSelector.MatchExpressions) == 0
	return empty && governsEgress(policy) && len(policy.Spec.Egress) == 0
}

func governsEgress(policy *networkingv1.NetworkPolicy) bool {
	for _, kind := range policy.Spec.PolicyTypes {
		if kind == networkingv1.PolicyTypeEgress {
			return true
		}
	}
	return false
}

func policyAdmits(policy *networkingv1.NetworkPolicy, ip net.IP, port int32) (bool, error) {
	for i := range policy.Spec.Egress {
		rule := &policy.Spec.Egress[i]
		if !portAdmitted(rule.Ports, port) {
			continue
		}
		admits, err := peerAdmitted(rule.To, ip)
		if err != nil {
			return false, fmt.Errorf("policy %s: %w", policy.Name, err)
		}
		if admits {
			return true, nil
		}
	}
	return false, nil
}

// portAdmitted applies the NetworkPolicy rule that an empty list admits every
// port. A named port cannot be resolved here, so it admits nothing.
func portAdmitted(ports []networkingv1.NetworkPolicyPort, port int32) bool {
	if len(ports) == 0 {
		return true
	}
	for i := range ports {
		p := &ports[i]
		if p.Port == nil || (p.Protocol != nil && *p.Protocol != corev1.ProtocolTCP) {
			continue
		}
		low, high := p.Port.IntVal, p.Port.IntVal
		if p.EndPort != nil {
			high = *p.EndPort
		}
		if low != 0 && low <= port && port <= high {
			return true
		}
	}
	return false
}

// peerAdmitted applies the NetworkPolicy rule that an empty list admits every
// destination. A pod or namespace peer never names a node address.
func peerAdmitted(peers []networkingv1.NetworkPolicyPeer, ip net.IP) (bool, error) {
	if len(peers) == 0 {
		return true, nil
	}
	for i := range peers {
		if peers[i].IPBlock == nil {
			continue
		}
		admits, err := ipBlockHolds(peers[i].IPBlock, ip)
		if err != nil {
			return false, err
		}
		if admits {
			return true, nil
		}
	}
	return false, nil
}

func ipBlockHolds(block *networkingv1.IPBlock, ip net.IP) (bool, error) {
	_, cidr, err := net.ParseCIDR(block.CIDR)
	if err != nil {
		return false, fmt.Errorf("ipBlock cidr: %w", err)
	}
	if !cidr.Contains(ip) {
		return false, nil
	}
	for _, except := range block.Except {
		_, excluded, err := net.ParseCIDR(except)
		if err != nil {
			return false, fmt.Errorf("ipBlock except: %w", err)
		}
		if excluded.Contains(ip) {
			return false, nil
		}
	}
	return true, nil
}
