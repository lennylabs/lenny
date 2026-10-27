//go:build contract

// SPDX-License-Identifier: MIT

package crdschema_test

import (
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// specSchemaRules returns the CEL rules on the served Runtime spec object.
func specSchemaRules(t *testing.T, crd apiextensionsv1.CustomResourceDefinition) apiextensionsv1.ValidationRules {
	t.Helper()
	return crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].XValidations
}

// hasRule reports whether rules carries a rule whose expression contains
// every fragment.
func hasRule(rules apiextensionsv1.ValidationRules, fragments ...string) bool {
	for _, r := range rules {
		match := true
		for _, f := range fragments {
			if !strings.Contains(r.Rule, f) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// TestRuntimeServedSchemaCarriesCommandWithRules pins the §5.1 command
// field on the committed Runtime CRD: a non-empty array of non-empty
// strings whose first element must be absolute, with the spec-level CEL
// rule that requires it on a type: agent sidecar runtime and forbids it on
// every other runtime.
//
// spec: §5.1 (command).
// diagnosis: the committed Runtime CRD no longer carries spec.command or
// one of its §5.1 validation rules, so the API server would admit a sidecar
// agent Runtime with no argv for the runtime supervisor to launch, or one
// whose argv is relative. Regenerate the CRD from the Go type and re-apply
// the post-generation markers.
func TestRuntimeServedSchemaCarriesCommandWithRules(t *testing.T) {
	crd := loadChartCRD(t, "lenny.dev_runtimes.yaml")
	cmd := specProperty(t, crd, "command")
	if cmd.Type != "array" {
		t.Fatalf("spec.command served as type %q, want array", cmd.Type)
	}
	if cmd.MinItems == nil || *cmd.MinItems != 1 {
		t.Errorf("spec.command minItems = %v, want 1", cmd.MinItems)
	}
	if cmd.Items == nil || cmd.Items.Schema == nil || cmd.Items.Schema.Type != "string" {
		t.Fatalf("spec.command items are not strings: %+v", cmd.Items)
	}
	if cmd.Items.Schema.MinLength == nil || *cmd.Items.Schema.MinLength != 1 {
		t.Errorf("spec.command items minLength = %v, want 1", cmd.Items.Schema.MinLength)
	}
	if !hasRule(cmd.XValidations, "self[0].startsWith('/')") {
		t.Errorf("spec.command carries no absolute-first-element rule: %+v", cmd.XValidations)
	}
	if !hasRule(specSchemaRules(t, crd), "has(self.command)", "self.type == 'agent'", "deploymentModel") {
		t.Errorf("spec carries no rule requiring command on a sidecar agent runtime: %+v", specSchemaRules(t, crd))
	}
}

// TestRuntimeServedSchemaCarriesEnvWithRules pins the §5.1 env field on
// the committed Runtime CRD: a map-type list keyed by name, so a duplicate
// name is rejected, whose name matches the POSIX variable-name pattern and
// may not begin with LENNY_, with the spec-level CEL rule that forbids env
// wherever command is forbidden.
//
// spec: §5.1 (env).
// diagnosis: the committed Runtime CRD no longer carries spec.env or one of
// its §5.1 validation rules, so the API server would admit a duplicate or
// platform-reserved variable name, or env on a runtime the supervisor does
// not launch. Regenerate the CRD from the Go type with
// `make generate`.
func TestRuntimeServedSchemaCarriesEnvWithRules(t *testing.T) {
	crd := loadChartCRD(t, "lenny.dev_runtimes.yaml")
	env := specProperty(t, crd, "env")
	if env.Type != "array" {
		t.Fatalf("spec.env served as type %q, want array", env.Type)
	}
	if env.XListType == nil || *env.XListType != "map" {
		t.Errorf("spec.env list type = %v, want map", env.XListType)
	}
	if len(env.XListMapKeys) != 1 || env.XListMapKeys[0] != "name" {
		t.Errorf("spec.env list map keys = %v, want [name]", env.XListMapKeys)
	}
	if env.Items == nil || env.Items.Schema == nil {
		t.Fatal("spec.env has no item schema")
	}
	item := env.Items.Schema
	name, ok := item.Properties["name"]
	if !ok {
		t.Fatal("spec.env items expose no name")
	}
	if name.Pattern != "^[A-Za-z_][A-Za-z0-9_]*$" {
		t.Errorf("spec.env[].name pattern = %q, want the POSIX variable-name pattern", name.Pattern)
	}
	if !hasRule(name.XValidations, "startsWith('LENNY_')") {
		t.Errorf("spec.env[].name carries no LENNY_ reservation rule: %+v", name.XValidations)
	}
	if value, ok := item.Properties["value"]; !ok || value.Type != "string" {
		t.Errorf("spec.env items expose no string value: %+v", item.Properties["value"])
	}
	requiredName := false
	for _, r := range item.Required {
		if r == "name" {
			requiredName = true
		}
	}
	if !requiredName {
		t.Errorf("spec.env items do not require name: %v", item.Required)
	}
	if !hasRule(specSchemaRules(t, crd), "!has(self.env)") {
		t.Errorf("spec carries no rule forbidding env outside sidecar agent runtimes: %+v", specSchemaRules(t, crd))
	}
}
