//go:build contract

// SPDX-License-Identifier: MIT

// Package crdschema_test is the Tier 3 contract suite for the lenny.dev
// CustomResourceDefinition OpenAPI schemas. A CRD schema is a wire
// contract: the kube-apiserver validates and serves objects against the
// committed/served schema, and clients (kubectl, the chart, the §4.7
// nonce-only activation path) depend on the field names and types it
// exposes. The tier-0 drift check (tests/tier0_static/crds_test.go) only
// catches divergence between the Go API types and the manifest; this
// suite pins the served field/type contract the manifest publishes, so a
// field rename or a type change that survives a coordinated Go+manifest
// edit is still caught here.
package crdschema_test

import (
	"os"
	"path/filepath"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// loadChartCRD decodes one committed chart CRD manifest into the typed
// CustomResourceDefinition the kube-apiserver serves. The chart copies
// under charts/lenny/crds/ are the source of truth for cluster installs.
func loadChartCRD(t *testing.T, filename string) apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	path := filepath.Join(schematest.RepoRoot(t), "charts", "lenny", "crds", filename)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return crd
}

// specProperty resolves the OpenAPI property schema for a spec field on
// the CRD's single served v1alpha1 version. It fails the test when the
// version, the spec object, or the property is absent.
func specProperty(t *testing.T, crd apiextensionsv1.CustomResourceDefinition, field string) apiextensionsv1.JSONSchemaProps {
	t.Helper()
	if len(crd.Spec.Versions) != 1 {
		t.Fatalf("CRD %s: serves %d versions, want exactly 1 (v1alpha1)", crd.Name, len(crd.Spec.Versions))
	}
	v := crd.Spec.Versions[0]
	if v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
		t.Fatalf("CRD %s: version %s has no OpenAPIV3Schema", crd.Name, v.Name)
	}
	specSchema, ok := v.Schema.OpenAPIV3Schema.Properties["spec"]
	if !ok {
		t.Fatalf("CRD %s: served schema has no spec object", crd.Name)
	}
	prop, ok := specSchema.Properties[field]
	if !ok {
		t.Fatalf("CRD %s: served spec schema does not expose %q (the §4.7 nonce-only field is missing from the wire contract)", crd.Name, field)
	}
	return prop
}

// assertBooleanField asserts a served spec property is exposed as an
// OpenAPI boolean. The §4.7 activation field, the resolved-decision
// carrier, and the §5.3 acknowledgment are all booleans; a non-boolean
// type would break the kubectl/chart write path and the controller read.
func assertBooleanField(t *testing.T, crd apiextensionsv1.CustomResourceDefinition, field string) {
	t.Helper()
	prop := specProperty(t, crd, field)
	if prop.Type != "boolean" {
		t.Errorf("CRD %s: spec.%s served as type %q, want boolean", crd.Name, field, prop.Type)
	}
}

// TestRuntimeServedSchemaExposesRequireSoPeercred pins the §4.7
// activation field on the served Runtime schema. The RuntimeReconciler
// reads it from the CR and mirrors it into the gateway registry; a client
// applies it through kubectl or the chart.
//
// spec: §4.7 (nonce-only activation field), §6 (CRD schema wire contract).
// diagnosis: The served Runtime CRD schema no longer exposes
// spec.requireSoPeercred as a boolean. The §4.7 nonce-only activation
// path is broken: a Runtime CR carrying requireSoPeercred is rejected or
// pruned by the kube-apiserver. Regenerate the CRD from the Go type and
// re-apply the post-generation markers.
func TestRuntimeServedSchemaExposesRequireSoPeercred(t *testing.T) {
	crd := loadChartCRD(t, "lenny.dev_runtimes.yaml")
	assertBooleanField(t, crd, "requireSoPeercred")
}

// TestSandboxServedSchemaExposesRequireSoPeercred pins the §4.7
// resolved-decision carrier on the served Sandbox schema. The
// WarmPoolController writes the resolved render decision here, and the
// pool-level SecurityDegradedMode trigger reads it back from the member
// Sandboxes.
//
// spec: §4.7 (resolved nonce-only render decision carrier), §6 (CRD
// schema wire contract).
// diagnosis: The served Sandbox CRD schema no longer exposes
// spec.requireSoPeercred as a boolean. The WarmPoolController cannot
// record the resolved nonce-only render decision, so the pool-level
// SecurityDegradedMode surfacing loses its source. Regenerate the CRD.
func TestSandboxServedSchemaExposesRequireSoPeercred(t *testing.T) {
	crd := loadChartCRD(t, "lenny.dev_sandboxes.yaml")
	assertBooleanField(t, crd, "requireSoPeercred")
}

// TestSandboxTemplateServedSchemaExposesAcknowledgeNonceOnlyAuth pins the
// §5.3 acknowledgment on the served SandboxTemplate schema. The
// PoolScalingController mirrors it from the Postgres pool definition, and
// the WarmPoolController render gate requires it before rendering
// nonce-only mode.
//
// spec: §4.7, §5.3 (nonce-only acknowledgment gate), §6 (CRD schema wire
// contract).
// diagnosis: The served SandboxTemplate CRD schema no longer exposes
// spec.acknowledgeNonceOnlyAuth as a boolean. The §4.7 render gate cannot
// read the PSC-mirrored acknowledgment, so an acknowledged pool can never
// enter nonce-only mode. Regenerate the CRD.
func TestSandboxTemplateServedSchemaExposesAcknowledgeNonceOnlyAuth(t *testing.T) {
	crd := loadChartCRD(t, "lenny.dev_sandboxtemplates.yaml")
	assertBooleanField(t, crd, "acknowledgeNonceOnlyAuth")
}
