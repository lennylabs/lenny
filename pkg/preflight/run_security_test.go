// SPDX-License-Identifier: MIT

package preflight_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/preflight"
)

func boolP(b bool) *bool { return &b }

// compliantGatewayDeployment builds a release-namespace Lenny-managed
// Deployment whose pod template satisfies the §13.1 baseline.
func compliantGatewayDeployment() client.Object {
	d := lennyDeployment("lenny-gateway", false)
	d.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{RunAsNonRoot: boolP(true)}
	d.Spec.Template.Spec.Containers = []corev1.Container{{
		Name: "gateway",
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot:           boolP(true),
			ReadOnlyRootFilesystem: boolP(true),
			Capabilities:           &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
	}}
	return d
}

// kubernetesService builds the default/kubernetes Service with the given
// ClusterIP, the surface the §13.2 cluster-CIDR-discovery check reads.
func kubernetesService(clusterIP string) client.Object {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "kubernetes", Namespace: "default"},
		Spec:       corev1.ServiceSpec{ClusterIP: clusterIP},
	}
}

// spec: §13.1 lines 6-8 — Run wires the pod-security-baseline check; a
// release-namespace Lenny-managed Deployment that sets no securityContext
// fails the install. F-13.1.12.
func TestRunFailsOnPodSecurityBaselineViolation(t *testing.T) {
	d := lennyDeployment("lenny-gateway", false)
	// A container with no securityContext: root, capabilities retained,
	// writable root filesystem — a §13.1 baseline violation.
	d.Spec.Template.Spec.Containers = []corev1.Container{{Name: "gateway"}}
	objs := append(allBaselineWebhooks(), client.Object(d))
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if resultByName(report, "pod-security-baseline").Passed {
		t.Error("pod-security-baseline passed a Deployment with no securityContext")
	}
	if !preflight.Failed(report) {
		t.Error("Run did not fail despite a baseline violation")
	}
}

// spec: §13.1 — a compliant Deployment passes the baseline. F-13.1.12.
func TestRunPassesPodSecurityBaselineForCompliantWorkload(t *testing.T) {
	objs := allBaselineWebhooks()
	objs = append(objs, compliantGatewayDeployment())
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if d := resultByName(report, "pod-security-baseline"); !d.Passed {
		t.Errorf("pod-security-baseline failed for a compliant Deployment: %s", d.Reason)
	}
}

// spec: §13.2 NET-040 — Run wires the cluster-CIDR-discovery check only
// when kubeApiServerCIDR is configured; an apiserver ClusterIP inside the
// configured CIDRs passes. F-13.2.13.
func TestRunWiresClusterCIDRDiscovery_pass(t *testing.T) {
	objs := append(allBaselineWebhooks(), kubernetesService("10.96.0.1"))
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{
		Namespace: preflightNS,
		ClusterCIDR: preflight.ClusterCIDRConfig{
			KubeAPIServerCIDR:         "10.96.0.0/12",
			ExcludeClusterServiceCIDR: "10.96.0.0/12",
			ExcludeClusterPodCIDR:     "10.244.0.0/16",
		},
	})
	if d := resultByName(report, "cluster-cidr-discovery"); !d.Passed {
		t.Errorf("cluster-cidr-discovery failed for an in-range ClusterIP: %s", d.Reason)
	}
}

// spec: §13.2 NET-040 — an apiserver ClusterIP outside kubeApiServerCIDR
// fails the install. F-13.2.13.
func TestRunWiresClusterCIDRDiscovery_fail(t *testing.T) {
	objs := append(allBaselineWebhooks(), kubernetesService("10.96.0.1"))
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{
		Namespace: preflightNS,
		ClusterCIDR: preflight.ClusterCIDRConfig{
			KubeAPIServerCIDR:         "172.20.0.0/16",
			ExcludeClusterServiceCIDR: "172.20.0.0/16",
			ExcludeClusterPodCIDR:     "10.244.0.0/16",
		},
	})
	if resultByName(report, "cluster-cidr-discovery").Passed {
		t.Error("cluster-cidr-discovery passed an out-of-range ClusterIP")
	}
	if !preflight.Failed(report) {
		t.Error("Run did not fail despite a CIDR mismatch")
	}
}

// spec: §13.2 NET-040 — the check is skipped when kubeApiServerCIDR is
// empty (the chart always supplies it; an empty config is the unit-test
// default). F-13.2.13.
func TestRunSkipsClusterCIDRWhenUnconfigured(t *testing.T) {
	c := runClient(t, allBaselineWebhooks()...)
	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if resultByName(report, "cluster-cidr-discovery").Reason != "" {
		t.Error("cluster-cidr-discovery should be absent when kubeApiServerCIDR is empty")
	}
}

// spec: §13.2 OTLP-068 — Run wires the otlp-tls check when an endpoint is
// configured; an http:// endpoint with TLS enabled fails the install.
// F-13.2.9.
func TestRunWiresOTLPTLSCheck(t *testing.T) {
	c := runClient(t, allBaselineWebhooks()...)
	report := preflight.Run(context.Background(), c, preflight.Config{
		Namespace: preflightNS,
		OTLP:      preflight.OTLPTLSConfig{Endpoint: "http://collector:4318", TLSEnabled: true},
	})
	if resultByName(report, "otlp-tls").Passed {
		t.Error("otlp-tls passed an http:// endpoint with TLS enabled")
	}
	if !preflight.Failed(report) {
		t.Error("Run did not fail despite an http:// OTLP endpoint")
	}
}
