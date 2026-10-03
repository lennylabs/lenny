// SPDX-License-Identifier: MIT

package podspec

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

// DNSPolicyOptOutLabel is the §13.2 pod label whose presence (value
// `cluster-default`) signals the pool opted out of the dedicated CoreDNS
// instance. The pod builder leaves such a pod at the Kubernetes default
// ClusterFirst behavior so it resolves through kube-system CoreDNS.
const (
	DNSPolicyOptOutLabel    = "lenny.dev/dns-policy"
	DNSPolicyClusterDefault = "cluster-default"
)

// applyDedicatedDNS points the agent pod's resolver at the dedicated
// lenny-system CoreDNS instance. spec: §13.2 —
// the agent-namespace NetworkPolicy routes DNS only to the dedicated
// instance and blocks kube-system kube-dns, so a pod left at the
// Kubernetes default ClusterFirst policy (resolver pointed at
// kube-system kube-dns) would have every name lookup blackholed. Setting
// `dnsPolicy: None` plus an explicit `dnsConfig` makes the pod query the
// dedicated instance instead. The search domains and ndots mirror the
// standard ClusterFirst behavior so in-cluster Service short-names still
// resolve.
//
// A pool that opts out via the `lenny.dev/dns-policy: cluster-default`
// label keeps the Kubernetes default behavior (resolving through
// kube-system CoreDNS); the builder makes no change in that case. The
// builder also makes no change when no dedicated ClusterIP is configured
// (dev or a deployment without the dedicated instance).
func applyDedicatedDNS(in Inputs, pod *corev1.Pod) {
	if in.DedicatedDNSClusterIP == "" {
		return
	}
	if in.Labels[DNSPolicyOptOutLabel] == DNSPolicyClusterDefault {
		return
	}
	pod.Spec.DNSPolicy = corev1.DNSNone
	searches := []string{"svc.cluster.local", "cluster.local"}
	if in.ReleaseNamespace != "" {
		searches = append([]string{in.ReleaseNamespace + ".svc.cluster.local"}, searches...)
	}
	pod.Spec.DNSConfig = &corev1.PodDNSConfig{
		Nameservers: []string{in.DedicatedDNSClusterIP},
		Searches:    searches,
		Options:     []corev1.PodDNSConfigOption{{Name: "ndots", Value: ptr.To("5")}},
	}
}
