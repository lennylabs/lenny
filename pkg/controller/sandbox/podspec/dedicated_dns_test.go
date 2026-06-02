// SPDX-License-Identifier: MIT

package podspec_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
)

// spec: §13.2 lines 470-490 (K8S-033) — when a dedicated CoreDNS ClusterIP
// is configured the pod builder stamps dnsPolicy:None plus a dnsConfig
// targeting it, because the agent-namespace NetworkPolicy blocks the
// kube-system kube-dns the default ClusterFirst policy would resolve
// through. F-13.2.4.
func TestBuildSetsDedicatedDNSConfig(t *testing.T) {
	in := inputs()
	in.DedicatedDNSClusterIP = "10.96.0.53"
	in.ReleaseNamespace = "lenny-system"
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if pod.Spec.DNSPolicy != corev1.DNSNone {
		t.Fatalf("dnsPolicy = %q, want %q", pod.Spec.DNSPolicy, corev1.DNSNone)
	}
	if pod.Spec.DNSConfig == nil {
		t.Fatal("dnsConfig is nil; want the dedicated-CoreDNS nameserver config")
	}
	if got := pod.Spec.DNSConfig.Nameservers; len(got) != 1 || got[0] != "10.96.0.53" {
		t.Fatalf("dnsConfig.nameservers = %v, want [10.96.0.53]", got)
	}
	wantSearch := []string{"lenny-system.svc.cluster.local", "svc.cluster.local", "cluster.local"}
	if len(pod.Spec.DNSConfig.Searches) != len(wantSearch) {
		t.Fatalf("dnsConfig.searches = %v, want %v", pod.Spec.DNSConfig.Searches, wantSearch)
	}
	for i, s := range wantSearch {
		if pod.Spec.DNSConfig.Searches[i] != s {
			t.Errorf("dnsConfig.searches[%d] = %q, want %q", i, pod.Spec.DNSConfig.Searches[i], s)
		}
	}
	if len(pod.Spec.DNSConfig.Options) != 1 || pod.Spec.DNSConfig.Options[0].Name != "ndots" || *pod.Spec.DNSConfig.Options[0].Value != "5" {
		t.Fatalf("dnsConfig.options = %v, want a single ndots:5 option", pod.Spec.DNSConfig.Options)
	}
}

// spec: §13.2 line 484 — a pool that opts out via the
// lenny.dev/dns-policy: cluster-default label keeps the Kubernetes default
// ClusterFirst behavior even when a dedicated ClusterIP is configured.
func TestBuildOmitsDNSConfigForClusterDefaultOptOut(t *testing.T) {
	in := inputs()
	in.DedicatedDNSClusterIP = "10.96.0.53"
	in.ReleaseNamespace = "lenny-system"
	in.Labels = map[string]string{
		"lenny.dev/pool":       "claude-worker",
		"lenny.dev/dns-policy": "cluster-default",
	}
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if pod.Spec.DNSPolicy == corev1.DNSNone {
		t.Fatal("opt-out pod must keep the Kubernetes default dnsPolicy, not None")
	}
	if pod.Spec.DNSConfig != nil {
		t.Fatalf("opt-out pod must not carry a dnsConfig; got %+v", pod.Spec.DNSConfig)
	}
}

// With no dedicated ClusterIP configured (dev / no dedicated instance) the
// pod is left at the Kubernetes default DNS behavior.
func TestBuildLeavesDefaultDNSWhenNoDedicatedClusterIP(t *testing.T) {
	pod, err := podspec.Build(inputs())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if pod.Spec.DNSPolicy == corev1.DNSNone {
		t.Fatal("without a dedicated ClusterIP the pod must keep the default dnsPolicy")
	}
	if pod.Spec.DNSConfig != nil {
		t.Fatalf("without a dedicated ClusterIP the pod must not carry a dnsConfig; got %+v", pod.Spec.DNSConfig)
	}
}

// With no ReleaseNamespace the namespace-scoped search domain is omitted
// but the cluster search domains remain.
func TestBuildDedicatedDNSWithoutReleaseNamespace(t *testing.T) {
	in := inputs()
	in.DedicatedDNSClusterIP = "10.96.0.53"
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := []string{"svc.cluster.local", "cluster.local"}
	if len(pod.Spec.DNSConfig.Searches) != len(want) {
		t.Fatalf("dnsConfig.searches = %v, want %v", pod.Spec.DNSConfig.Searches, want)
	}
}
