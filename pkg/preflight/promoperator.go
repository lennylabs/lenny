// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"fmt"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PrometheusOperatorCRDNames are the monitoring.coreos.com CRDs the §16.9
// ServiceMonitor, PodMonitor, and PrometheusRule manifests require. The
// chart renders those manifests only when these CRDs are present; the
// preflight check below names which ones are missing so an operator who
// expected operator-managed scrape targets learns why the chart fell back
// to a ConfigMap.
//
// spec: §16.9 R8. F-16.9.4.
var PrometheusOperatorCRDNames = []string{
	"podmonitors.monitoring.coreos.com",
	"prometheusrules.monitoring.coreos.com",
	"servicemonitors.monitoring.coreos.com",
}

// PrometheusOperatorCRDCheck runs the §16.9 R8 CRD-presence preflight:
// when monitoring.format selects the Prometheus Operator CRDs
// (prometheusrule or both), the operator's CRDs MUST be installed for the
// chart to render its operator-managed manifests. The check is advisory by
// construction (Passed is always true): the chart already degrades the
// format to configmap and skips the scrape monitors at render time when
// the CRDs are absent, so a hard failure would block an otherwise-valid
// install. The advisory tells the operator the fallback happened so an
// intended operator-managed scrape is not silently dropped.
//
// spec: §16.9 R8. F-16.9.4.
type PrometheusOperatorCRDCheck struct {
	// Format is the monitoring.format chart value
	// (prometheusrule | configmap | both). An empty or configmap value
	// does not depend on the operator, so the check passes silently.
	Format string
}

// Decide reads the Prometheus Operator CRDs through reader and reports the
// advisory. Reuses the apiextensions CRD read the crd-schema-version check
// already requires, so no extra RBAC is needed.
//
// spec: §16.9 R8. F-16.9.4.
func (c PrometheusOperatorCRDCheck) Decide(ctx context.Context, reader client.Reader) Decision {
	format := strings.TrimSpace(c.Format)
	if format != "prometheusrule" && format != "both" {
		return Decision{Passed: true}
	}
	var missing []string
	for _, name := range PrometheusOperatorCRDNames {
		var crd apiextensionsv1.CustomResourceDefinition
		if err := reader.Get(ctx, client.ObjectKey{Name: name}, &crd); err != nil {
			if apierrors.IsNotFound(err) {
				missing = append(missing, name)
				continue
			}
			// A read error other than NotFound leaves the posture
			// indeterminate; surface it as a non-blocking advisory rather
			// than aborting the install for an advisory check.
			return Decision{Passed: true, Reason: fmt.Sprintf(
				"WARNING: could not determine Prometheus Operator CRD presence for monitoring.format=%q: get CRD %q: %v. "+
					"If the operator is absent the chart falls back to a ConfigMap rule file (§16.9).",
				format, name, err,
			)}
		}
	}
	if len(missing) == 0 {
		return Decision{Passed: true, Reason: fmt.Sprintf(
			"all %d Prometheus Operator CRDs present for monitoring.format=%q",
			len(PrometheusOperatorCRDNames), format,
		)}
	}
	return Decision{Passed: true, Reason: fmt.Sprintf(
		"WARNING: monitoring.format=%q selects the Prometheus Operator CRDs but the following are not installed: %s. "+
			"The chart falls back to a ConfigMap rule file and skips the ServiceMonitor and PodMonitor (§16.9 R8). "+
			"Install the Prometheus Operator (kube-prometheus-stack, OpenShift Monitoring) or set monitoring.format=configmap.",
		format, strings.Join(missing, ", "),
	)}
}
