// SPDX-License-Identifier: MIT

package preflight

import (
	"net"
	"strings"
	"testing"
)

func kubeadmCIDRConfig() ClusterCIDRConfig {
	return ClusterCIDRConfig{
		KubeAPIServerCIDR:         "10.96.0.0/12",
		ExcludeClusterServiceCIDR: "10.96.0.0/12",
		ExcludeClusterPodCIDR:     "10.244.0.0/16",
	}
}

// spec: §13.2 NET-040 / NET-022 — the apiserver ClusterIP falls within
// kubeApiServerCIDR and the service-CIDR exclusion, and node pod CIDRs
// fall within the pod-CIDR exclusion: the check passes. F-13.2.13.
func TestCheckClusterCIDRDiscovery_compliant_spec_13_2(t *testing.T) {
	d := CheckClusterCIDRDiscovery("10.96.0.1", []string{"10.244.0.0/24", "10.244.1.0/24"}, kubeadmCIDRConfig())
	if !d.Passed {
		t.Fatalf("compliant cluster CIDRs failed: %s", d.Reason)
	}
}

// spec: §13.2 NET-040 — an apiserver ClusterIP outside kubeApiServerCIDR
// fails the install fail-closed. F-13.2.13.
func TestCheckClusterCIDRDiscovery_apiserver_outside_kubeApiServerCIDR(t *testing.T) {
	cfg := kubeadmCIDRConfig()
	cfg.KubeAPIServerCIDR = "172.20.0.0/16" // EKS-style; ClusterIP is kubeadm-range
	d := CheckClusterCIDRDiscovery("10.96.0.1", nil, cfg)
	if d.Passed {
		t.Fatal("expected failure when ClusterIP is outside kubeApiServerCIDR")
	}
	if !strings.HasPrefix(d.Reason, "CLUSTER_CIDR_MISMATCH:") || !strings.Contains(d.Reason, "kubeApiServerCIDR") {
		t.Errorf("unexpected reason: %s", d.Reason)
	}
}

// spec: §13.2 NET-022 — an apiserver ClusterIP outside
// excludeClusterServiceCIDR fails (the broad egress would not exclude the
// service CIDR). F-13.2.13.
func TestCheckClusterCIDRDiscovery_apiserver_outside_serviceExclusion(t *testing.T) {
	cfg := kubeadmCIDRConfig()
	cfg.ExcludeClusterServiceCIDR = "172.20.0.0/16"
	d := CheckClusterCIDRDiscovery("10.96.0.1", nil, cfg)
	if d.Passed {
		t.Fatal("expected failure when ClusterIP is outside excludeClusterServiceCIDR")
	}
	if !strings.Contains(d.Reason, "excludeClusterServiceCIDR") {
		t.Errorf("unexpected reason: %s", d.Reason)
	}
}

// spec: §13.2 NET-022 — a node pod CIDR outside excludeClusterPodCIDR
// fails. F-13.2.13.
func TestCheckClusterCIDRDiscovery_podCIDR_outside_exclusion(t *testing.T) {
	d := CheckClusterCIDRDiscovery("10.96.0.1", []string{"192.168.0.0/24"}, kubeadmCIDRConfig())
	if d.Passed {
		t.Fatal("expected failure when a node pod CIDR is outside excludeClusterPodCIDR")
	}
	if !strings.Contains(d.Reason, "excludeClusterPodCIDR") {
		t.Errorf("unexpected reason: %s", d.Reason)
	}
}

// spec: §13.2 — a managed cluster reporting no node pod CIDR passes with
// an advisory warning rather than failing (the pod CIDR lives in the CNI).
// F-13.2.13.
func TestCheckClusterCIDRDiscovery_noPodCIDR_warns(t *testing.T) {
	d := CheckClusterCIDRDiscovery("10.96.0.1", nil, kubeadmCIDRConfig())
	if !d.Passed {
		t.Fatalf("missing node pod CIDR should pass with a warning, got: %s", d.Reason)
	}
	if !strings.HasPrefix(d.Reason, "WARNING:") {
		t.Errorf("expected advisory warning, got: %q", d.Reason)
	}
}

// spec: §13.2 — a malformed ClusterIP or CIDR fails fail-closed rather
// than silently passing. F-13.2.13.
func TestCheckClusterCIDRDiscovery_malformed_inputs(t *testing.T) {
	if d := CheckClusterCIDRDiscovery("not-an-ip", nil, kubeadmCIDRConfig()); d.Passed {
		t.Error("malformed ClusterIP must fail")
	}
	cfg := kubeadmCIDRConfig()
	cfg.KubeAPIServerCIDR = "garbage"
	if d := CheckClusterCIDRDiscovery("10.96.0.1", nil, cfg); d.Passed {
		t.Error("malformed kubeApiServerCIDR must fail")
	}
}

func TestCIDRWithin(t *testing.T) {
	cases := []struct {
		child  string
		parent string
		want   bool
	}{
		{"10.244.0.0/24", "10.244.0.0/16", true},
		{"10.244.0.0/16", "10.244.0.0/16", true},
		{"10.244.0.0/12", "10.244.0.0/16", false}, // less specific than parent
		{"192.168.0.0/24", "10.244.0.0/16", false},
		{"bad-cidr", "10.244.0.0/16", false},
	}
	for _, tc := range cases {
		_, parent, err := net.ParseCIDR(tc.parent)
		if err != nil {
			t.Fatalf("parse parent %q: %v", tc.parent, err)
		}
		if got := cidrWithin(tc.child, parent); got != tc.want {
			t.Errorf("cidrWithin(%q, %q) = %v, want %v", tc.child, tc.parent, got, tc.want)
		}
	}
}
