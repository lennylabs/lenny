// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ConversionWebhookName is the §17.2 item-12 baseline CRD conversion
// webhook. It runs the same lenny-webhook image as the validating
// webhooks (serving /crd-conversion) and is co-managed with them for
// lifecycle purposes. Unlike the validating webhooks it is wired into a
// CRD's spec.conversion.webhook rather than a ValidatingWebhookConfiguration,
// so the admission-webhook-inventory check excludes it and it is verified
// here instead.
//
// spec: §17.2 line 53 (baseline item 12), §15.5 line 2438 (preflight
// "validates conversion webhook availability ... will fail the upgrade if
// the webhook Service is absent or not ready"). F-15.5.3 / F-17.2.4 /
// F-10.5.6.
const ConversionWebhookName = "lenny-crd-conversion"

// ConversionWebhookState captures the cluster presence and readiness of
// the lenny-crd-conversion workload the conversion-webhook-availability
// check evaluates. It is the projection gatherConversionWebhook reads so
// the decision logic stays a pure, table-testable function.
type ConversionWebhookState struct {
	// ServicePresent is true when the lenny-crd-conversion Service exists
	// in the release namespace.
	ServicePresent bool
	// DeploymentPresent is true when the lenny-crd-conversion Deployment
	// exists in the release namespace.
	DeploymentPresent bool
	// DeploymentReady is true when the Deployment reports at least one
	// ready replica.
	DeploymentReady bool
}

// CheckConversionWebhook reports the §15.5 line 2438 preflight outcome for
// the CRD conversion webhook. A missing Deployment is treated as
// "not yet deployed" and passes, matching the install-time semantics of
// CheckAdmissionWebhooks: on a fresh install the workload is applied in
// the chart's main phase after this pre-hook, so its absence here is not a
// fail-open gap. Once the Deployment exists (every upgrade), the Service
// MUST be present and the Deployment MUST have a ready replica or the
// upgrade is aborted, because a missing or unready conversion webhook
// makes every multi-version CRD operation fail at the API server.
//
// spec: §15.5 line 2438; §17.2 line 58. F-15.5.3 / F-17.2.4 / F-10.5.6.
func CheckConversionWebhook(state ConversionWebhookState) Decision {
	if !state.DeploymentPresent {
		return Decision{Passed: true, Reason: fmt.Sprintf(
			"%s not yet deployed; applied in the chart's main phase after this pre-hook", ConversionWebhookName,
		)}
	}
	if !state.ServicePresent {
		return Decision{Reason: fmt.Sprintf(
			"%s Service is absent; the CRD conversion webhook is unreachable and every multi-version CRD operation fails at the API server",
			ConversionWebhookName,
		)}
	}
	if !state.DeploymentReady {
		return Decision{Reason: fmt.Sprintf(
			"%s Deployment has no ready replicas; the CRD conversion webhook is not ready and the upgrade is aborted",
			ConversionWebhookName,
		)}
	}
	return Decision{Passed: true, Reason: fmt.Sprintf(
		"%s conversion webhook present and ready", ConversionWebhookName,
	)}
}

// gatherConversionWebhook reads the lenny-crd-conversion Service and
// Deployment in the release namespace and projects them onto a
// ConversionWebhookState. It lists rather than gets so the existing
// list-scoped preflight RBAC (apps/deployments list cluster-wide, the
// release-namespace services list) covers it without an additional get
// verb.
func gatherConversionWebhook(ctx context.Context, reader client.Reader, namespace string) (ConversionWebhookState, error) {
	var st ConversionWebhookState
	var svcs corev1.ServiceList
	if err := reader.List(ctx, &svcs, client.InNamespace(namespace)); err != nil {
		return ConversionWebhookState{}, err
	}
	for i := range svcs.Items {
		if svcs.Items[i].Name == ConversionWebhookName {
			st.ServicePresent = true
			break
		}
	}
	var deps appsv1.DeploymentList
	if err := reader.List(ctx, &deps, client.InNamespace(namespace)); err != nil {
		return ConversionWebhookState{}, err
	}
	for i := range deps.Items {
		if deps.Items[i].Name == ConversionWebhookName {
			st.DeploymentPresent = true
			st.DeploymentReady = deps.Items[i].Status.ReadyReplicas >= 1
			break
		}
	}
	return st, nil
}
