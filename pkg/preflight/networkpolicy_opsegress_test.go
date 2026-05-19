// SPDX-License-Identifier: MIT

package preflight_test

import (
	"strings"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"

	"github.com/lennylabs/lenny/pkg/preflight"
)

func TestCheckOpsEgressSelectorParityPassesOnTwoSelectorClauses(t *testing.T) {
	// lenny-ops-egress with every storage destination pairing a
	// namespaceSelector and a podSelector (NET-061).
	p := netPolicy("lenny-ops-egress")
	p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{
		{
			To: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: labelSel("kubernetes.io/metadata.name", "lenny-system"),
				PodSelector:       labelSel("lenny.dev/component", "gateway"),
			}},
			Ports: []networkingv1.NetworkPolicyPort{tcpPort(8443)},
		},
		{
			To: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: labelSel("kubernetes.io/metadata.name", "lenny-system"),
				PodSelector:       labelSel("lenny.dev/component", "pgbouncer"),
			}},
			Ports: []networkingv1.NetworkPolicyPort{tcpPort(5432)},
		},
		{
			To: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: labelSel("kubernetes.io/metadata.name", "monitoring"),
				PodSelector:       labelSel("app", "prometheus"),
			}},
			Ports: []networkingv1.NetworkPolicyPort{tcpPort(9090)},
		},
	}
	d := preflight.CheckOpsEgressSelectorParity([]networkingv1.NetworkPolicy{*p})
	if !d.Passed {
		t.Errorf("check failed a lenny-ops-egress policy with two-selector clauses: %s", d.Reason)
	}
}

func TestCheckOpsEgressSelectorParityIgnoresIPBlockAndDNS(t *testing.T) {
	// The kube-apiserver ipBlock peer and the DNS peer (governed by
	// NET-067 separately) are not NET-061 two-selector violations.
	p := netPolicy("lenny-ops-egress")
	p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{
		{
			To: []networkingv1.NetworkPolicyPeer{{
				IPBlock: &networkingv1.IPBlock{CIDR: "10.96.0.0/12"},
			}},
			Ports: []networkingv1.NetworkPolicyPort{tcpPort(443)},
		},
		{
			To: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: labelSel("kubernetes.io/metadata.name", "kube-system"),
				PodSelector:       labelSel("k8s-app", "kube-dns"),
			}},
			Ports: []networkingv1.NetworkPolicyPort{udpPort(53), tcpPort(53)},
		},
	}
	d := preflight.CheckOpsEgressSelectorParity([]networkingv1.NetworkPolicy{*p})
	if !d.Passed {
		t.Errorf("check flagged the kube-apiserver ipBlock or the DNS peer: %s", d.Reason)
	}
}

func TestCheckOpsEgressSelectorParityFailsOnNamespaceOnlyClause(t *testing.T) {
	// NET-061: a storage clause with a namespaceSelector but no
	// podSelector permits egress to every pod in the namespace.
	p := netPolicy("lenny-ops-egress")
	p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: labelSel("kubernetes.io/metadata.name", "lenny-system"),
		}},
		Ports: []networkingv1.NetworkPolicyPort{tcpPort(5432)},
	}}
	d := preflight.CheckOpsEgressSelectorParity([]networkingv1.NetworkPolicy{*p})
	if d.Passed {
		t.Fatal("check passed a namespace-only storage clause in lenny-ops-egress")
	}
	if !strings.Contains(d.Reason, "NET-061") {
		t.Errorf("reason %q does not cite NET-061", d.Reason)
	}
}

func TestCheckOpsEgressSelectorParityFailsOnLegacyAppKeyForPlatformComponent(t *testing.T) {
	// NET-061: a Lenny-rendered platform component must use the canonical
	// key; app: lenny-gateway is drift.
	p := netPolicy("lenny-ops-egress")
	p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: labelSel("kubernetes.io/metadata.name", "lenny-system"),
			PodSelector:       labelSel("app", "lenny-gateway"),
		}},
		Ports: []networkingv1.NetworkPolicyPort{tcpPort(8443)},
	}}
	d := preflight.CheckOpsEgressSelectorParity([]networkingv1.NetworkPolicy{*p})
	if d.Passed {
		t.Fatal("check passed a platform-component clause keyed on app: lenny-gateway")
	}
	if !strings.Contains(d.Reason, "NET-061") {
		t.Errorf("reason %q does not cite NET-061", d.Reason)
	}
}

func TestCheckOpsEgressSelectorParityAllowsBackupJobAppSelectors(t *testing.T) {
	// lenny-backup-job legitimately selects storage workloads through
	// the app: key (app: postgres, app: minio) per the §13.2 exception.
	p := netPolicy("lenny-backup-job")
	p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{
		{
			To: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: labelSel("kubernetes.io/metadata.name", "lenny-system"),
				PodSelector:       labelSel("app", "postgres"),
			}},
			Ports: []networkingv1.NetworkPolicyPort{tcpPort(5432)},
		},
		{
			To: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: labelSel("kubernetes.io/metadata.name", "lenny-system"),
				PodSelector:       labelSel("app", "minio"),
			}},
			Ports: []networkingv1.NetworkPolicyPort{tcpPort(9443)},
		},
	}
	d := preflight.CheckOpsEgressSelectorParity([]networkingv1.NetworkPolicy{*p})
	if !d.Passed {
		t.Errorf("check rejected the sanctioned app: storage selectors in lenny-backup-job: %s", d.Reason)
	}
}

func TestCheckOpsEgressSelectorParityIgnoresNonOperabilityPolicies(t *testing.T) {
	// A non-operability NetworkPolicy with a namespace-only egress peer
	// is outside the NET-061 audit scope.
	p := netPolicy("allow-gateway-egress")
	p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: labelSel("kubernetes.io/metadata.name", "lenny-system"),
		}},
		Ports: []networkingv1.NetworkPolicyPort{tcpPort(5432)},
	}}
	d := preflight.CheckOpsEgressSelectorParity([]networkingv1.NetworkPolicy{*p})
	if !d.Passed {
		t.Errorf("check flagged a non-operability-plane policy: %s", d.Reason)
	}
}
