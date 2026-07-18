// SPDX-License-Identifier: MIT

package preflight_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/preflight"
)

// lennyNetPolicy builds a Lenny-managed NetworkPolicy (carrying the
// app.kubernetes.io/name: lenny label the gatherer filters on).
func lennyNetPolicy(name string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: preflightNS,
			Labels:    map[string]string{"app.kubernetes.io/name": "lenny"},
		},
	}
}

// gatewayDeployment builds a lenny-gateway Deployment carrying the
// canonical component label and the NET-064 identity annotations.
func gatewayDeployment(namespace, trustDomain, audience string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lenny-gateway",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "lenny",
				"lenny.dev/component":    "gateway",
			},
			Annotations: map[string]string{
				"lenny.dev/spiffe-trust-domain": trustDomain,
				"lenny.dev/sa-token-audience":   audience,
			},
		},
	}
}

func TestRunPassesWithCanonicalNetworkPolicies(t *testing.T) {
	objs := allBaselineWebhooks()
	// A DNS egress policy that correctly pairs both selectors.
	dnsPolicy := lennyNetPolicy("allow-controller-egress")
	dnsPolicy.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: labelSel("kubernetes.io/metadata.name", "kube-system"),
			PodSelector:       labelSel("k8s-app", "kube-dns"),
		}},
		Ports: []networkingv1.NetworkPolicyPort{udpPort(53), tcpPort(53)},
	}}
	objs = append(objs, dnsPolicy)
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if preflight.Failed(report) {
		for _, r := range report {
			if !r.Decision.Passed {
				t.Errorf("check %q failed for a compliant cluster: %s", r.Name, r.Decision.Reason)
			}
		}
	}
}

func TestRunFailsOnDNSPolicyMissingPodSelector(t *testing.T) {
	objs := allBaselineWebhooks()
	bad := lennyNetPolicy("allow-controller-egress")
	bad.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: labelSel("kubernetes.io/metadata.name", "kube-system"),
		}},
		Ports: []networkingv1.NetworkPolicyPort{udpPort(53), tcpPort(53)},
	}}
	objs = append(objs, bad)
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if resultByName(report, "networkpolicy-dns-podselector-parity").Passed {
		t.Error("networkpolicy-dns-podselector-parity passed a DNS rule with no podSelector")
	}
	if !preflight.Failed(report) {
		t.Error("Run did not fail despite a NET-067 violation")
	}
}

func TestRunFailsOnLegacySelectorDrift(t *testing.T) {
	objs := allBaselineWebhooks()
	bad := lennyNetPolicy("allow-gateway-egress")
	bad.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{
			{PodSelector: labelSel("app", "lenny-gateway")},
		},
		Ports: []networkingv1.NetworkPolicyPort{tcpPort(50051)},
	}}
	objs = append(objs, bad)
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if resultByName(report, "networkpolicy-selector-consistency").Passed {
		t.Error("networkpolicy-selector-consistency passed a legacy app: selector")
	}
}

func TestRunFailsOnCrossFamilyIPBlock(t *testing.T) {
	objs := allBaselineWebhooks()
	bad := lennyNetPolicy("allow-gateway-egress-llm-upstream")
	bad.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{{
			IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: []string{"fc00::/7"}},
		}},
		Ports: []networkingv1.NetworkPolicyPort{tcpPort(443)},
	}}
	objs = append(objs, bad)
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if resultByName(report, "networkpolicy-ipblock-family-parity").Passed {
		t.Error("networkpolicy-ipblock-family-parity passed a cross-family ipBlock")
	}
}

func TestRunPassesNET064WhenNoExistingGateway(t *testing.T) {
	// A fresh install: no lenny-gateway Deployment exists yet, so the
	// configured identity values cannot collide.
	c := runClient(t, allBaselineWebhooks()...)

	report := preflight.Run(context.Background(), c, preflight.Config{
		Namespace:         preflightNS,
		SPIFFETrustDomain: "lenny-fresh-cluster",
		SATokenAudience:   "lenny-gateway-fresh",
	})
	if resultByName(report, "spiffe-trust-domain-uniqueness").Passed != true {
		t.Errorf("spiffe-trust-domain-uniqueness failed on a fresh install: %s",
			resultByName(report, "spiffe-trust-domain-uniqueness").Reason)
	}
	if resultByName(report, "sa-token-audience-uniqueness").Passed != true {
		t.Errorf("sa-token-audience-uniqueness failed on a fresh install: %s",
			resultByName(report, "sa-token-audience-uniqueness").Reason)
	}
}

func TestRunFailsOnNET064TrustDomainCollision(t *testing.T) {
	objs := allBaselineWebhooks()
	// An existing Lenny deployment in another namespace already uses the
	// trust domain the install is configured with.
	objs = append(objs, gatewayDeployment("lenny-other", "lenny-shared", "lenny-gateway-other"))
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{
		Namespace:         preflightNS,
		SPIFFETrustDomain: "lenny-shared",
		SATokenAudience:   "lenny-gateway-this",
	})
	if resultByName(report, "spiffe-trust-domain-uniqueness").Passed {
		t.Error("spiffe-trust-domain-uniqueness passed a colliding trust domain")
	}
	if !preflight.Failed(report) {
		t.Error("Run did not fail despite a NET-064 trust-domain collision")
	}
}

// TestRunReportsGatewayIdentityListFailureFailClosed pins the fail-closed
// posture of the §13.2 / §10.3 NET-064 deployment-identity uniqueness
// audits: a failed lenny-gateway Deployment List surfaces as a failure on
// both the spiffe-trust-domain and sa-token-audience uniqueness checks
// rather than a silent pass. The R5 decomposition relocates this read and
// its fail-closed translation into runDeploymentIdentityChecks; this test
// guards that the relocated helper keeps failing closed on the gather error
// (the original monolithic Run had no test pinning this branch).
//
// spec: §13.2 / §10.3 NET-064 (deployment-identity uniqueness). F-13.2.13.
func TestRunReportsGatewayIdentityListFailureFailClosed(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(runScheme(t)).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				// The gateway-identity gather lists Deployments filtered by
				// the canonical gateway component label; fail only that read
				// so the rest of the report still populates and the two
				// identity checks isolate the fail-closed branch.
				if _, ok := list.(*appsv1.DeploymentList); ok {
					return errors.New("api server unavailable")
				}
				return cl.List(ctx, list, opts...)
			},
		}).
		Build()

	report := preflight.Run(context.Background(), c, preflight.Config{
		Namespace:         preflightNS,
		SPIFFETrustDomain: "lenny-this-cluster",
		SATokenAudience:   "lenny-gateway-this",
	})
	for _, name := range []string{"spiffe-trust-domain-uniqueness", "sa-token-audience-uniqueness"} {
		d := resultByName(report, name)
		if d.Passed {
			t.Errorf("%s passed despite a failed lenny-gateway Deployment List", name)
		}
		if !strings.Contains(d.Reason, "list lenny-gateway Deployments") {
			t.Errorf("%s did not surface the gather error fail-closed: %q", name, d.Reason)
		}
	}
	if !preflight.Failed(report) {
		t.Error("Run did not fail despite a gateway-identity cluster read error")
	}
}

func TestRunReportsNetworkPolicyListFailureFailClosed(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(runScheme(t)).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*networkingv1.NetworkPolicyList); ok {
					return errors.New("api server unavailable")
				}
				return cl.List(ctx, list, opts...)
			},
		}).
		Build()

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if resultByName(report, "networkpolicy-selector-consistency").Passed {
		t.Error("networkpolicy-selector-consistency passed despite a failed NetworkPolicy List")
	}
	if !preflight.Failed(report) {
		t.Error("Run did not fail despite a NetworkPolicy cluster read error")
	}
}

// TestRunFailsOnUnpairedObjectStoreEgress pins that the §13.2 NET-071
// checkpoint object-store parity check is wired into Run: an in-cluster
// object-store egress arm rendered without the paired allow-minio
// agent-namespace ingress clause fails the install fail-closed.
//
// spec: §13.2 line 209; §17.6 (NET-071).
func TestRunFailsOnUnpairedObjectStoreEgress(t *testing.T) {
	objs := allBaselineWebhooks()
	egress := lennyNetPolicy("allow-pod-egress-objectstore")
	egress.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: labelSel("kubernetes.io/metadata.name", "lenny-system"),
			PodSelector:       labelSel("lenny.dev/component", "minio"),
		}},
	}}
	objs = append(objs, egress)
	c := runClient(t, objs...)

	report := preflight.Run(context.Background(), c, preflight.Config{Namespace: preflightNS})
	if resultByName(report, "networkpolicy-objectstore-egress-parity").Passed {
		t.Error("networkpolicy-objectstore-egress-parity passed an unpaired in-cluster arm")
	}
	if !preflight.Failed(report) {
		t.Error("Run did not fail despite an unpaired object-store egress policy")
	}
}

// TestRunFailsOnT4GCSWithoutDefaultEncryption pins that the §17.6
// checkpoint T4 default-encryption gate is wired into Run: a gcs backend
// serving a workspaceTier T4 tenant with no per-tenant bucket-default
// CMEK fails the install fail-closed.
//
// spec: §12.5 line 315; §17.9.7; §17.6.
func TestRunFailsOnT4GCSWithoutDefaultEncryption(t *testing.T) {
	c := runClient(t, allBaselineWebhooks()...)

	report := preflight.Run(context.Background(), c, preflight.Config{
		Namespace:     preflightNS,
		ObjectStorage: preflight.ObjectStorageConfig{Provider: "gcs"},
		ObjectStorageT4: preflight.ObjectStorageT4Config{
			ServesT4Tenant: true,
		},
	})
	if resultByName(report, "checkpoint-t4-default-encryption").Passed {
		t.Error("checkpoint-t4-default-encryption passed a gcs T4 backend with no bucket-default CMEK")
	}
	if !preflight.Failed(report) {
		t.Error("Run did not fail despite a T4 gcs backend with no default encryption")
	}
}
