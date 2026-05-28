// SPDX-License-Identifier: MIT

package crds_test

import (
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"

	embeddedcrds "github.com/lennylabs/lenny/pkg/embedded/crds"
	"github.com/lennylabs/lenny/pkg/preflight"
)

// embeddedCRDs decodes every embedded manifest into a
// CustomResourceDefinition for the parity assertions below.
func embeddedCRDs(t *testing.T) []apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	entries, err := embeddedcrds.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded CRDs: %v", err)
	}
	var out []apiextensionsv1.CustomResourceDefinition
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, err := embeddedcrds.FS.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := yaml.Unmarshal(raw, &crd); err != nil {
			t.Fatalf("decode %s: %v", e.Name(), err)
		}
		out = append(out, crd)
	}
	return out
}

// spec: §10 line 437 / line 443 — every embedded Lenny CRD MUST
// declare the `lenny.dev/schema-version` annotation. F-15.5.12.
func TestEmbeddedCRDsCarrySchemaVersionAnnotation_spec_10_437(t *testing.T) {
	crds := embeddedCRDs(t)
	if len(crds) == 0 {
		t.Fatal("expected at least one embedded CRD")
	}
	seen := map[string]bool{}
	for _, crd := range crds {
		got := strings.TrimSpace(crd.Annotations[preflight.CRDSchemaVersionAnnotation])
		if got != preflight.CurrentCRDSchemaVersion {
			t.Errorf("CRD %s: %s = %q; want %q",
				crd.Name,
				preflight.CRDSchemaVersionAnnotation, got,
				preflight.CurrentCRDSchemaVersion)
		}
		seen[crd.Name] = true
	}
	for _, expected := range preflight.LennyCRDNames {
		if !seen[expected] {
			t.Errorf("embedded CRDs missing %s", expected)
		}
	}
}

// spec: §10 line 437 — every embedded CRD MUST carry
// `x-kubernetes-preserve-unknown-fields: true` on extensible sub-objects
// (spec, status) so an older controller does not crash on fields a
// newer gateway introduces. F-15.5.12.
func TestEmbeddedCRDsPreserveUnknownFields_spec_10_437(t *testing.T) {
	for _, crd := range embeddedCRDs(t) {
		for _, v := range crd.Spec.Versions {
			if v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
				t.Errorf("CRD %s version %s: missing OpenAPIV3Schema", crd.Name, v.Name)
				continue
			}
			props := v.Schema.OpenAPIV3Schema.Properties
			for _, key := range []string{"spec", "status"} {
				p, ok := props[key]
				if !ok {
					continue
				}
				preserve := p.XPreserveUnknownFields
				if preserve == nil || !*preserve {
					t.Errorf("CRD %s version %s: properties.%s must declare x-kubernetes-preserve-unknown-fields: true",
						crd.Name, v.Name, key)
				}
			}
		}
	}
}
