//go:build contract

// SPDX-License-Identifier: MIT

package crdschema_test

import (
	"slices"
	"testing"
)

// TestSandboxTemplateServedSchemaCarriesRuntimeProcessEnum pins
// spec.sessionPolicy.recycle.runtimeProcess on the committed SandboxTemplate
// CRD: a string whose enum is exactly keep and restart.
//
// spec: §5.2 (Runtime-process lifetime)
// diagnosis: the committed SandboxTemplate CRD no longer carries
// sessionPolicy.recycle.runtimeProcess or its enum, so the API server prunes
// the lifetime the PoolScalingController mirrors or admits a value the
// WarmPoolController cannot render. Regenerate the CRD from the Go type and
// re-apply the post-generation markers.
func TestSandboxTemplateServedSchemaCarriesRuntimeProcessEnum(t *testing.T) {
	crd := loadChartCRD(t, "lenny.dev_sandboxtemplates.yaml")
	sp := specProperty(t, crd, "sessionPolicy")
	recycle, ok := sp.Properties["recycle"]
	if !ok {
		t.Fatal("spec.sessionPolicy has no recycle object")
	}
	rp, ok := recycle.Properties["runtimeProcess"]
	if !ok {
		t.Fatal("spec.sessionPolicy.recycle has no runtimeProcess")
	}
	if rp.Type != "string" {
		t.Errorf("runtimeProcess served as type %q, want string", rp.Type)
	}
	var got []string
	for _, v := range rp.Enum {
		got = append(got, string(v.Raw))
	}
	slices.Sort(got)
	if want := []string{`"keep"`, `"restart"`}; !slices.Equal(got, want) {
		t.Errorf("runtimeProcess enum = %v, want %v", got, want)
	}
}
