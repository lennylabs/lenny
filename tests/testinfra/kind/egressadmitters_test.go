// SPDX-License-Identifier: MIT

package kind

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/yaml"
)

var (
	tokenServiceLabels = map[string]string{"app.kubernetes.io/name": "lenny", "lenny.dev/component": "token-service"}
	gatewayLabels      = map[string]string{"app.kubernetes.io/name": "lenny", "lenny.dev/component": "gateway"}
	apiEndpoint        = net.ParseIP("172.19.0.3")
)

func tcpPorts(ports ...int) []networkingv1.NetworkPolicyPort {
	out := make([]networkingv1.NetworkPolicyPort, 0, len(ports))
	for _, port := range ports {
		value := intstr.FromInt32(int32(port))
		out = append(out, networkingv1.NetworkPolicyPort{Port: &value})
	}
	return out
}

func egressPolicy(name string, selector map[string]string, rules ...networkingv1.NetworkPolicyEgressRule) networkingv1.NetworkPolicy {
	return networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: selector},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress:      rules,
		},
	}
}

func cidrRule(cidr string, ports ...int) networkingv1.NetworkPolicyEgressRule {
	return networkingv1.NetworkPolicyEgressRule{
		To:    []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: cidr}}},
		Ports: tcpPorts(ports...),
	}
}

// spec: §13.2.4 (Token Service kube-apiserver egress)
func TestEgressAdmitters(t *testing.T) {
	tokenService := map[string]string{"lenny.dev/component": "token-service"}
	udp := corev1.ProtocolUDP
	udpRule := cidrRule("172.16.0.0/12", 6443)
	udpRule.Ports[0].Protocol = &udp
	exceptRule := cidrRule("172.16.0.0/12", 6443)
	exceptRule.To[0].IPBlock.Except = []string{"172.19.0.0/16"}
	rangeRule := cidrRule("172.16.0.0/12", 6000)
	end := int32(7000)
	rangeRule.Ports[0].EndPort = &end
	ingressOnly := egressPolicy("ingress-only", tokenService, cidrRule("172.16.0.0/12", 6443))
	ingressOnly.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	podPeer := networkingv1.NetworkPolicyEgressRule{
		To:    []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{}}},
		Ports: tcpPorts(6443),
	}

	cases := []struct {
		name   string
		policy networkingv1.NetworkPolicy
		want   []string
	}{
		{"rule covers the endpoint", egressPolicy("p", tokenService, cidrRule("172.16.0.0/12", 443, 6443)), []string{"p"}},
		{"rule names the service CIDR", egressPolicy("p", tokenService, cidrRule("10.96.0.0/12", 443, 6443)), nil},
		{"rule names another port", egressPolicy("p", tokenService, cidrRule("172.16.0.0/12", 443)), nil},
		{"policy selects another pod", egressPolicy("p", map[string]string{"lenny.dev/component": "gateway"}, cidrRule("172.16.0.0/12", 6443)), nil},
		{"policy has no egress rule", egressPolicy("p", tokenService), nil},
		{"policy governs ingress alone", ingressOnly, nil},
		{"rule names UDP", egressPolicy("p", tokenService, udpRule), nil},
		{"except removes the endpoint", egressPolicy("p", tokenService, exceptRule), nil},
		{"port range holds the endpoint port", egressPolicy("p", tokenService, rangeRule), []string{"p"}},
		{"pod peer never names a node", egressPolicy("p", tokenService, podPeer), nil},
		{"empty rule admits everything", egressPolicy("p", tokenService, networkingv1.NetworkPolicyEgressRule{}), []string{"p"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EgressAdmitters([]networkingv1.NetworkPolicy{tc.policy}, tokenServiceLabels, apiEndpoint, 6443)
			if err != nil {
				t.Fatalf("EgressAdmitters: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("EgressAdmitters = %v, want %v", got, tc.want)
			}
		})
	}
}

// spec: §13.2.4 (Token Service kube-apiserver egress)
func TestEgressAdmittersRefusesMalformedCIDR(t *testing.T) {
	policy := egressPolicy("p", nil, cidrRule("not-a-cidr", 6443))
	if _, err := EgressAdmitters([]networkingv1.NetworkPolicy{policy}, tokenServiceLabels, apiEndpoint, 6443); err == nil {
		t.Fatal("EgressAdmitters accepted a malformed ipBlock")
	}
}

// spec: §13.2 (lenny-system default-deny)
func TestIsEgressDefaultDeny(t *testing.T) {
	deny := egressPolicy("deny", nil)
	selective := egressPolicy("selective", map[string]string{"a": "b"})
	admitting := egressPolicy("admitting", nil, cidrRule("0.0.0.0/0", 443))
	ingress := egressPolicy("ingress", nil)
	ingress.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	for policy, want := range map[*networkingv1.NetworkPolicy]bool{
		&deny: true, &selective: false, &admitting: false, &ingress: false,
	} {
		if got := IsEgressDefaultDeny(policy); got != want {
			t.Errorf("IsEgressDefaultDeny(%s) = %t, want %t", policy.Name, got, want)
		}
	}
}

// TestE2EApiServerEgressLeavesTokenServiceToTheChartRule pins the harness
// inputs the tier-9 egress test depends on. If the e2e policy selected the
// Token Service, a dial would succeed with the chart's allow-token-service rule
// deleted. If the overlay left kubeApiServerCIDR on the service CIDR, the chart
// rule would not match kindnet's post-DNAT destination.
//
// spec: §13.2.4 (Token Service kube-apiserver egress)
func TestE2EApiServerEgressLeavesTokenServiceToTheChartRule(t *testing.T) {
	var policy networkingv1.NetworkPolicy
	readYAML(t, "apiserver-egress.yaml", &policy)
	var overlay struct {
		KubeAPIServerCIDR string `json:"kubeApiServerCIDR"`
	}
	readYAML(t, "e2e-values.yaml", &overlay)

	policies := []networkingv1.NetworkPolicy{policy}
	if got, err := EgressAdmitters(policies, tokenServiceLabels, apiEndpoint, 6443); err != nil || got != nil {
		t.Fatalf("e2e policy admitters for the Token Service = %v, %v; want none", got, err)
	}
	got, err := EgressAdmitters(policies, gatewayLabels, apiEndpoint, 6443)
	if err != nil || !reflect.DeepEqual(got, []string{policy.Name}) {
		t.Fatalf("e2e policy admitters for the gateway = %v, %v; want %s", got, err, policy.Name)
	}

	chartRule := egressPolicy("allow-token-service", map[string]string{"lenny.dev/component": "token-service"},
		cidrRule(overlay.KubeAPIServerCIDR, 443, 6443))
	got, err = EgressAdmitters([]networkingv1.NetworkPolicy{chartRule}, tokenServiceLabels, apiEndpoint, 6443)
	if err != nil || !reflect.DeepEqual(got, []string{chartRule.Name}) {
		t.Fatalf("kubeApiServerCIDR %q does not cover the kind apiserver endpoint: %v, %v",
			overlay.KubeAPIServerCIDR, got, err)
	}
}

func readYAML(t *testing.T, name string, out any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if err := yaml.Unmarshal(raw, out); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
}
