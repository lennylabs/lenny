// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// canonicalComponentLabel is the §13.2 normative selector key (NET-047,
// NET-050). Every NetworkPolicy clause that targets a lenny-system
// platform component must select it through this key with a
// component-specific value, e.g. lenny.dev/component: gateway.
const canonicalComponentLabel = "lenny.dev/component"

// webhookNameLabel is the §13.2 additive per-pod key (NET-068) that
// narrows an admission-webhook egress allow-list to a single webhook,
// e.g. lenny.dev/webhook-name: drain-readiness. It is permitted only in
// egress rules and only alongside the canonical component label.
const webhookNameLabel = "lenny.dev/webhook-name"

// platformComponents is the set of lenny-system platform components
// §13.2 (NET-047/NET-050) governs: a NetworkPolicy clause targeting any
// of these must use the canonical lenny.dev/component key. The
// operability plane (lenny-ops, lenny-backup) is excluded because §13.2
// line 201 Exception 1 permits it to select on the app: key.
var platformComponents = map[string]bool{
	"gateway":           true,
	"token-service":     true,
	"controller":        true,
	"pgbouncer":         true,
	"minio":             true,
	"admission-webhook": true,
	"coredns":           true,
}

// opsAppValues is the set of app: label values §13.2 line 201
// Exception 1 reserves for the operability plane. A NetworkPolicy
// clause may select these through the app: key without that being
// counted as selector drift.
var opsAppValues = map[string]bool{
	"lenny-ops":    true,
	"lenny-backup": true,
}

// storageMonitoringAppValues is the set of app: label values §13.2
// line 201 / NET-061 permits for storage and monitoring destinations
// rendered by upstream subcharts. Operability-plane egress clauses may
// target these through the app: key without that being selector drift.
var storageMonitoringAppValues = map[string]bool{
	"redis":      true,
	"prometheus": true,
	"postgres":   true,
	"minio":      true,
}

// dnsServerSelectors maps a concrete DNS-server pod label key to the
// value that identifies a DNS server (NET-067). A DNS egress peer's
// podSelector must match one of these: k8s-app: kube-dns for the
// kube-system CoreDNS, or lenny.dev/component: coredns for the
// dedicated lenny-system CoreDNS.
var dnsServerSelectors = map[string]string{
	"k8s-app":               "kube-dns",
	canonicalComponentLabel: "coredns",
}

// objectStoreEgressPolicyName is the §13.2 NET-071 supplemental
// agent-egress NetworkPolicy that re-admits agent-pod egress to object
// storage for checkpoint chunk PUT/GET against gateway-minted presigned
// capabilities. The chart renders one per agent namespace.
const objectStoreEgressPolicyName = "allow-pod-egress-objectstore"

// minioIngressPolicyName is the §13.2 lenny-system NetworkPolicy whose
// agent-namespace ingress clause admits the checkpoint chunk return path
// on the MinIO TLS port, paired one-for-one with the object-store egress
// policy's in-cluster arm (NET-071).
const minioIngressPolicyName = "allow-minio"

// agentNamespaceLabel is the namespace label the chart stamps on every
// agent namespace. The MinIO agent-namespace ingress clause selects the
// checkpoint chunk sources by pairing this namespaceSelector with the
// managed-pod podSelector.
const agentNamespaceLabel = "lenny.dev/agent-namespace"

// managedPodLabel is the label the WarmPoolController stamps on every
// agent pod. The object-store egress policy targets these pods, and the
// MinIO agent-namespace ingress clause admits them as chunk sources.
const managedPodLabel = "lenny.dev/managed"

// minioComponentValue is the canonical-component value of the self-managed
// MinIO pod the object-store egress policy's in-cluster arm selects.
const minioComponentValue = "minio"

// gatherNetworkPolicies lists the Lenny-rendered NetworkPolicies across
// every namespace. The chart labels each managed resource with
// app.kubernetes.io/name: lenny, so the selector-consistency and
// parity audits scan only those, ignoring third-party policies a
// cluster may already carry.
func gatherNetworkPolicies(ctx context.Context, reader client.Reader) ([]networkingv1.NetworkPolicy, error) {
	var list networkingv1.NetworkPolicyList
	if err := reader.List(ctx, &list, lennyManagedLabel); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// policyRef labels one NetworkPolicy for an audit failure message.
func policyRef(p *networkingv1.NetworkPolicy) string {
	if p.Namespace == "" {
		return p.Name
	}
	return p.Namespace + "/" + p.Name
}

// selectorMatchLabels returns the matchLabels of a LabelSelector, or an
// empty map when the selector is nil. The audits inspect matchLabels
// only; the chart does not render matchExpressions on these rules.
func selectorMatchLabels(sel *metav1.LabelSelector) map[string]string {
	if sel == nil {
		return nil
	}
	return sel.MatchLabels
}

// targetsPlatformComponent reports whether a podSelector resolves to a
// lenny-system platform component governed by NET-047/NET-050. A
// selector is considered to target a platform component when it
// carries the canonical key with a known component value, OR when it
// carries a legacy app: lenny-<component> key for one of those
// components (the drift case the audit must catch).
func targetsPlatformComponent(sel *metav1.LabelSelector) (component string, isPlatform bool) {
	labels := selectorMatchLabels(sel)
	if v, ok := labels[canonicalComponentLabel]; ok && platformComponents[v] {
		return v, true
	}
	if v, ok := labels["app"]; ok {
		// app: lenny-gateway and siblings are the legacy keys §13.2
		// (NET-047) forbids. app: lenny-ops / app: lenny-backup are the
		// permitted operability-plane exception and are not platform
		// components.
		if strings.HasPrefix(v, "lenny-") && !opsAppValues[v] {
			c := strings.TrimPrefix(v, "lenny-")
			if platformComponents[c] {
				return c, true
			}
		}
	}
	return "", false
}

// peerHasNamespaceSelector reports whether a NetworkPolicyPeer carries
// a namespaceSelector.
func peerHasNamespaceSelector(peer networkingv1.NetworkPolicyPeer) bool {
	return peer.NamespaceSelector != nil
}

// peerHasPodSelector reports whether a NetworkPolicyPeer carries a
// podSelector.
func peerHasPodSelector(peer networkingv1.NetworkPolicyPeer) bool {
	return peer.PodSelector != nil
}
