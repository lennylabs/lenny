//go:build contract

// SPDX-License-Identifier: MIT

package crdschema_test

import "testing"

// TestSandboxServedSchemaCarriesNoWorkspacePlan pins that the served
// Sandbox schema has no spec.workspacePlan property. The §12.6 PodSpec
// lists RuntimeDefinitionRef, IsolationProfile, ExecutionMode, and resource
// limits, and a session binds its workspace plan at creation or finalize
// (§14.1 Plan binding), so a pod is never created with a plan and the
// Sandbox has no field to record one.
//
// spec: §12.6 (Interface Design, the PodSpec row), §14.1 (WorkspacePlan
// Schema Versioning, Plan binding).
// diagnosis: The served Sandbox CRD schema exposes spec.workspacePlan
// again. A Go type or a hand edit reintroduced a pod-creation plan that
// no path writes and §12.6 does not list. Remove the field from
// SandboxSpec and regenerate the CRD with `make generate`.
func TestSandboxServedSchemaCarriesNoWorkspacePlan(t *testing.T) {
	crd := loadChartCRD(t, "lenny.dev_sandboxes.yaml")
	if len(crd.Spec.Versions) != 1 || crd.Spec.Versions[0].Schema == nil || crd.Spec.Versions[0].Schema.OpenAPIV3Schema == nil {
		t.Fatalf("CRD %s: want exactly one served version with an OpenAPIV3Schema", crd.Name)
	}
	specSchema, ok := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
	if !ok {
		t.Fatalf("CRD %s: served schema has no spec object", crd.Name)
	}
	if _, ok := specSchema.Properties["runtimeRef"]; !ok {
		t.Fatalf("CRD %s: served spec schema has no runtimeRef; the schema did not decode as the Sandbox spec", crd.Name)
	}
	if _, ok := specSchema.Properties["workspacePlan"]; ok {
		t.Errorf("CRD %s: served spec schema exposes workspacePlan; the §12.6 PodSpec carries no workspace plan", crd.Name)
	}
}
