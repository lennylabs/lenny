// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NamespaceGovernanceStatus reports the §17.2 resource-governance posture
// of one agent namespace: whether the namespace already exists, and
// whether it carries a ResourceQuota and a LimitRange.
//
// On a fresh install the agent namespaces and their ResourceQuota /
// LimitRange objects are applied by the chart's main release phase, which
// runs after the pre-install preflight hook, so they do not yet exist
// when the preflight runs. The checks therefore evaluate only namespaces
// that already exist (an upgrade, or an operator-managed namespace);
// a not-yet-created namespace is skipped because the chart creates the
// namespace and its governance objects together.
//
// spec: §17.6 lines 501-502; §17.2. F-17.6.1.
type NamespaceGovernanceStatus struct {
	Name             string
	Exists           bool
	HasResourceQuota bool
	HasLimitRange    bool
}

// CheckNamespaceResourceQuotas is the §17.6 line 501 ResourceQuota
// presence audit. An existing agent namespace with no ResourceQuota
// fails the install fail-closed so an unbounded agent namespace cannot
// exhaust the cluster.
//
// spec: §17.6 line 501; §17.2.
func CheckNamespaceResourceQuotas(statuses []NamespaceGovernanceStatus) Decision {
	var missing []string
	for _, s := range statuses {
		if s.Exists && !s.HasResourceQuota {
			missing = append(missing, fmt.Sprintf("ResourceQuota missing in namespace '%s'; required to bound agent pod count (Section 17.2)", s.Name))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return Decision{Passed: false, Reason: strings.Join(missing, "; ")}
	}
	return Decision{Passed: true}
}

// CheckNamespaceLimitRanges is the §17.6 line 502 LimitRange presence
// audit. An existing agent namespace with no LimitRange fails the install
// fail-closed because a container that omits resource requests would be
// scheduled BestEffort.
//
// spec: §17.6 line 502; §17.2.
func CheckNamespaceLimitRanges(statuses []NamespaceGovernanceStatus) Decision {
	var missing []string
	for _, s := range statuses {
		if s.Exists && !s.HasLimitRange {
			missing = append(missing, fmt.Sprintf("LimitRange missing in namespace '%s'; required to prevent BestEffort pods (Section 17.2)", s.Name))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return Decision{Passed: false, Reason: strings.Join(missing, "; ")}
	}
	return Decision{Passed: true}
}

// gatherNamespaceGovernance reads the §17.2 resource-governance posture
// of each agent namespace: whether it exists and whether it carries a
// ResourceQuota and a LimitRange. A not-yet-created namespace is reported
// with Exists=false so the checks skip it.
//
// spec: §17.6 lines 501-502; §17.2.
func gatherNamespaceGovernance(ctx context.Context, reader client.Reader, namespaces []string) ([]NamespaceGovernanceStatus, error) {
	out := make([]NamespaceGovernanceStatus, 0, len(namespaces))
	for _, ns := range namespaces {
		ns = strings.TrimSpace(ns)
		if ns == "" {
			continue
		}
		var nsObj corev1.Namespace
		if err := reader.Get(ctx, client.ObjectKey{Name: ns}, &nsObj); err != nil {
			if apierrors.IsNotFound(err) {
				out = append(out, NamespaceGovernanceStatus{Name: ns, Exists: false})
				continue
			}
			return nil, err
		}
		status := NamespaceGovernanceStatus{Name: ns, Exists: true}

		var quotas corev1.ResourceQuotaList
		if err := reader.List(ctx, &quotas, client.InNamespace(ns)); err != nil {
			return nil, err
		}
		status.HasResourceQuota = len(quotas.Items) > 0

		var limits corev1.LimitRangeList
		if err := reader.List(ctx, &limits, client.InNamespace(ns)); err != nil {
			return nil, err
		}
		status.HasLimitRange = len(limits.Items) > 0

		out = append(out, status)
	}
	return out, nil
}
