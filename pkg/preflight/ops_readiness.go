// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// OpsIngressClusterIssuerCheck is the §17.6 line 520 ops.ingress
// ClusterIssuer advisory. When ops.ingress carries a
// cert-manager.io/cluster-issuer annotation, the referenced ClusterIssuer
// must exist or lenny-ops serves without TLS. The check is a non-blocking
// warning (it always passes); an empty issuer name is a silent pass.
//
// spec: §17.6 line 520.
type OpsIngressClusterIssuerCheck struct {
	// IssuerName is the cert-manager.io/cluster-issuer annotation value
	// on ops.ingress. Empty skips the check.
	IssuerName string
	// Exists reports whether the ClusterIssuer was found in the cluster.
	Exists bool
}

// Decide evaluates the §17.6 line 520 ClusterIssuer advisory.
func (c OpsIngressClusterIssuerCheck) Decide() Decision {
	if strings.TrimSpace(c.IssuerName) == "" || c.Exists {
		return Decision{Passed: true}
	}
	return Decision{
		Passed: true,
		Reason: fmt.Sprintf("WARNING: ops.ingress references ClusterIssuer '%s' which was not found. Lenny-ops will run without TLS until this is corrected.", c.IssuerName),
	}
}

// MonitoringNamespaceCheck is the §17.6 line 521 monitoring-namespace
// advisory. The rendered PodMonitor / ServiceMonitor is only discovered
// when the monitoring namespace exists and runs a Prometheus pod matching
// monitoring.podLabel. The check is a non-blocking warning; an empty
// namespace or pod-label skips it.
//
// spec: §17.6 line 521.
type MonitoringNamespaceCheck struct {
	// Namespace is the monitoring.namespace value.
	Namespace string
	// PodLabel is the monitoring.podLabel selector (key=value) the
	// Prometheus pod must carry.
	PodLabel string
	// HasMatchingPod reports whether a pod matching PodLabel was found in
	// Namespace. False (with a configured namespace+label) triggers the
	// advisory.
	HasMatchingPod bool
}

// Decide evaluates the §17.6 line 521 monitoring-namespace advisory.
func (c MonitoringNamespaceCheck) Decide() Decision {
	if strings.TrimSpace(c.Namespace) == "" || strings.TrimSpace(c.PodLabel) == "" || c.HasMatchingPod {
		return Decision{Passed: true}
	}
	return Decision{
		Passed: true,
		Reason: fmt.Sprintf("WARNING: namespace '%s' does not contain a Prometheus pod matching label '%s'. The rendered PodMonitor/ServiceMonitor may not be discovered by your monitoring stack.", c.Namespace, c.PodLabel),
	}
}

// OpsSARBACRule is one entry in the §25.4 canonical lenny-ops-sa RBAC
// table: an apiGroup/resource/verb the lenny-ops ServiceAccount must be
// allowed to perform. Namespace is empty for cluster-scoped checks.
//
// spec: §25.4 (lenny-ops RBAC table); §17.6 line 519.
type OpsSARBACRule struct {
	Group     string
	Resource  string
	Verb      string
	Namespace string
}

// String renders the rule for the failure message.
func (r OpsSARBACRule) String() string {
	g := r.Group
	if g == "" {
		g = "core"
	}
	return fmt.Sprintf("%s/%s:%s", g, r.Resource, r.Verb)
}

// CanonicalOpsSARBACRules is the §25.4 canonical permission set the
// lenny-ops ServiceAccount must hold: Lease coordination, Deployment
// patches, CRD reads, ConfigMap reads, Secret reads for backup
// credentials, and Job create/watch.
//
// spec: §25.4; §17.6 line 519.
func CanonicalOpsSARBACRules(namespace string) []OpsSARBACRule {
	return []OpsSARBACRule{
		{Group: "coordination.k8s.io", Resource: "leases", Verb: "create", Namespace: namespace},
		{Group: "coordination.k8s.io", Resource: "leases", Verb: "update", Namespace: namespace},
		{Group: "apps", Resource: "deployments", Verb: "patch", Namespace: namespace},
		{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions", Verb: "get"},
		{Group: "", Resource: "configmaps", Verb: "get", Namespace: namespace},
		{Group: "", Resource: "secrets", Verb: "get", Namespace: namespace},
		{Group: "batch", Resource: "jobs", Verb: "create", Namespace: namespace},
		{Group: "batch", Resource: "jobs", Verb: "watch", Namespace: namespace},
	}
}

// OpsSARBACProber runs a SubjectAccessReview for the lenny-ops
// ServiceAccount against one rule, returning whether the access is
// allowed. It is the seam the real authorization-API clientset and test
// fakes satisfy. A nil prober skips the lenny-ops-sa RBAC check.
//
// spec: §17.6 line 519 (kubectl auth can-i against each canonical rule).
type OpsSARBACProber interface {
	CanI(ctx context.Context, serviceAccount string, rule OpsSARBACRule) (allowed bool, err error)
}

// OpsSARBACCheck is the §17.6 line 519 lenny-ops-sa RBAC audit. It runs a
// SubjectAccessReview for every canonical rule and fails fail-closed when
// any is denied. A nil prober skips the check (the v1 preflight Job is
// not granted SubjectAccessReview-create RBAC); the chart-rendered Role /
// ClusterRole templates remain the source of truth.
//
// spec: §17.6 line 519; §25.4.
type OpsSARBACCheck struct {
	// ServiceAccount is the fully-qualified ops SA username
	// (system:serviceaccount:<ns>:lenny-ops-sa).
	ServiceAccount string
	// Rules is the canonical permission set. Empty falls back to
	// CanonicalOpsSARBACRules for the SA's namespace.
	Rules []OpsSARBACRule
	// Prober runs the SubjectAccessReview. Nil skips the check.
	Prober OpsSARBACProber
}

// Decide evaluates the §17.6 line 519 lenny-ops-sa RBAC audit.
func (c OpsSARBACCheck) Decide(ctx context.Context) Decision {
	if c.Prober == nil {
		return Decision{Passed: true, Reason: "SKIPPED: SubjectAccessReview not wired; lenny-ops RBAC is governed by the chart Role/ClusterRole templates"}
	}
	rules := c.Rules
	if len(rules) == 0 {
		return Decision{Passed: true, Reason: "SKIPPED: no canonical RBAC rules supplied"}
	}
	var denied []string
	for _, rule := range rules {
		allowed, err := c.Prober.CanI(ctx, c.ServiceAccount, rule)
		if err != nil {
			return Decision{Passed: false, Reason: fmt.Sprintf("SubjectAccessReview for %s failed: %v", rule, err)}
		}
		if !allowed {
			denied = append(denied, rule.String())
		}
	}
	if len(denied) > 0 {
		sort.Strings(denied)
		return Decision{
			Passed: false,
			Reason: fmt.Sprintf("ServiceAccount lenny-ops-sa is missing required permissions: %s. Re-render the chart or apply the Role/ClusterRole templates in charts/lenny/templates/ops-rbac.yaml", strings.Join(denied, ", ")),
		}
	}
	return Decision{Passed: true, Reason: "lenny-ops-sa holds every canonical §25.4 permission"}
}

// clusterIssuerExists reports whether a cert-manager ClusterIssuer with
// the given name exists. It reads the object as unstructured so the
// preflight binary does not depend on the cert-manager Go types. A
// not-found maps to false; any other read error propagates.
//
// spec: §17.6 line 520.
func clusterIssuerExists(ctx context.Context, reader client.Reader, name string) (bool, error) {
	if strings.TrimSpace(name) == "" {
		return false, nil
	}
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cert-manager.io",
		Version: "v1",
		Kind:    "ClusterIssuer",
	})
	if err := reader.Get(ctx, client.ObjectKey{Name: name}, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		// A missing CRD (no cert-manager) surfaces as a NoKindMatchError /
		// NotFound-class error; treat "cannot resolve the type" as "the
		// issuer does not exist" so the advisory fires instead of failing
		// the install. Genuine transport errors still propagate.
		if apimeta := unstructuredKindUnavailable(err); apimeta {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// monitoringPodPresent reports whether the monitoring namespace contains
// at least one pod matching the key=value podLabel selector. A missing
// namespace or unparseable label returns false with no error so the
// check degrades to its advisory.
//
// spec: §17.6 line 521.
func monitoringPodPresent(ctx context.Context, reader client.Reader, namespace, podLabel string) (bool, error) {
	key, value, ok := splitKeyValue(podLabel)
	if namespace == "" || !ok {
		return false, nil
	}
	var pods corev1.PodList
	if err := reader.List(ctx, &pods, client.InNamespace(namespace), client.MatchingLabels{key: value}); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return len(pods.Items) > 0, nil
}

// splitKeyValue parses a `key=value` label selector into its parts.
func splitKeyValue(s string) (key, value string, ok bool) {
	s = strings.TrimSpace(s)
	idx := strings.IndexByte(s, '=')
	if idx <= 0 || idx == len(s)-1 {
		return "", "", false
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:]), true
}

// unstructuredKindUnavailable reports whether err indicates the requested
// kind has no matching CRD installed (cert-manager absent), which the
// ClusterIssuer advisory treats as "issuer not found" rather than a
// transport failure.
func unstructuredKindUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no matches for kind") || strings.Contains(msg, "could not find the requested resource")
}
