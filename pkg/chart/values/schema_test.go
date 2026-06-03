// SPDX-License-Identifier: MIT

package values

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// repoRoot walks up from this test file until it finds the module's
// go.mod so tests can read committed chart artifacts regardless of the
// package working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from " + file)
		}
		dir = parent
	}
}

// TestSchemaIsCommitted is the build-time drift guard: the committed
// charts/lenny/values.schema.json must byte-match the generator output.
// CI runs `go run ./cmd/lenny-chart-schema-gen -check` for the same
// invariant. spec: §17.6 line 655.
func TestSchemaIsCommitted_spec_17_6_655(t *testing.T) {
	got, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	path := filepath.Join(repoRoot(t), "charts", "lenny", "values.schema.json")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read committed schema: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("committed %s is stale; run `go run ./cmd/lenny-chart-schema-gen`", path)
	}
}

// TestChartValuesYAMLConformsToSchema asserts the chart's own default
// values.yaml validates against the generated schema. A regression here
// means Helm would reject `helm install` with stock defaults. spec: §17.6
// line 653.
func TestChartValuesYAMLConformsToSchema_spec_17_6_653(t *testing.T) {
	schema, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	path := filepath.Join(repoRoot(t), "charts", "lenny", "values.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read values.yaml: %v", err)
	}
	if err := ValidateYAML(schema, data); err != nil {
		t.Fatalf("default values.yaml does not conform to its own schema: %v", err)
	}
}

// TestPresetsConformToSchema lints every shipped tier preset against the
// schema. The presets are chart-values fragments layered with `-f`, so
// they must validate directly. spec: §17.9.2 line 1374.
func TestPresetsConformToSchema_spec_17_9_2_1374(t *testing.T) {
	schema, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	root := repoRoot(t)
	presets, err := filepath.Glob(filepath.Join(root, "charts", "lenny", "presets", "*.yaml"))
	if err != nil || len(presets) == 0 {
		t.Fatalf("no presets found: %v", err)
	}
	for _, p := range presets {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		if err := ValidateYAML(schema, data); err != nil {
			t.Errorf("preset %s does not conform: %v", filepath.Base(p), err)
		}
	}
}

func mustSchema(t *testing.T) []byte {
	t.Helper()
	s, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return s
}

// TestRejectsUnknownTopLevelKey is the operator-typo guard: a values
// document with a key not in Root is rejected (root
// additionalProperties:false). spec: §17.6 line 653.
func TestRejectsUnknownTopLevelKey(t *testing.T) {
	err := ValidateYAML(mustSchema(t), []byte("gatway:\n  replicas: 2\n"))
	if err == nil {
		t.Fatal("expected an unknown top-level key to be rejected")
	}
}

// TestRejectsDevTenantIDPattern enforces the §17.6 line 373 schema
// constraint: devTenantId must match the canonical tenant_id regex.
func TestRejectsDevTenantIDPattern_spec_17_6_373(t *testing.T) {
	schema := mustSchema(t)
	if err := ValidateYAML(schema, []byte("playground:\n  devTenantId: \"bad.tenant\"\n")); err == nil {
		t.Error("expected devTenantId with a '.' to be rejected by the pattern")
	}
	if err := ValidateYAML(schema, []byte("playground:\n  devTenantId: \"bad tenant\"\n")); err == nil {
		t.Error("expected devTenantId with whitespace to be rejected by the pattern")
	}
	if err := ValidateYAML(schema, []byte("playground:\n  devTenantId: acme\n")); err != nil {
		t.Errorf("expected a valid devTenantId to pass: %v", err)
	}
}

// TestRejectsNoEnvironmentPolicyEnum enforces the §17.6 line 365
// security-affecting enum.
func TestRejectsNoEnvironmentPolicyEnum_spec_17_6_365(t *testing.T) {
	schema := mustSchema(t)
	if err := ValidateYAML(schema, []byte("global:\n  noEnvironmentPolicy: permissive\n")); err == nil {
		t.Error("expected an out-of-enum noEnvironmentPolicy to be rejected")
	}
	for _, ok := range []string{"deny-all", "allow-all"} {
		if err := ValidateYAML(schema, []byte("global:\n  noEnvironmentPolicy: "+ok+"\n")); err != nil {
			t.Errorf("expected %q to pass: %v", ok, err)
		}
	}
}

// TestRejectsBearerTTLOutOfRange enforces the §27.3 60..3600 bound the
// gateway also clamps.
func TestRejectsBearerTTLOutOfRange(t *testing.T) {
	schema := mustSchema(t)
	if err := ValidateYAML(schema, []byte("playground:\n  bearerTtlSeconds: 5\n")); err == nil {
		t.Error("expected bearerTtlSeconds below 60 to be rejected")
	}
	if err := ValidateYAML(schema, []byte("playground:\n  bearerTtlSeconds: 99999\n")); err == nil {
		t.Error("expected bearerTtlSeconds above 3600 to be rejected")
	}
	if err := ValidateYAML(schema, []byte("playground:\n  bearerTtlSeconds: 900\n")); err != nil {
		t.Errorf("expected an in-range bearerTtlSeconds to pass: %v", err)
	}
}

// TestAcceptsEmptyDeploymentTier guards the deliberate choice to leave
// deploymentTier a free string: the chart sets it to "" to omit the
// deployment_tier metric relabel, and the schema must not reject that.
func TestAcceptsEmptyDeploymentTier(t *testing.T) {
	if err := ValidateYAML(mustSchema(t), []byte("global:\n  deploymentTier: \"\"\n")); err != nil {
		t.Errorf("expected an empty deploymentTier to pass: %v", err)
	}
}

// TestRejectsWrongType enforces the "wrong types" half of §17.6 line 653:
// a scalar where the schema expects an object is rejected.
func TestRejectsWrongType_spec_17_6_653(t *testing.T) {
	if err := ValidateYAML(mustSchema(t), []byte("global: not-an-object\n")); err == nil {
		t.Error("expected a scalar for an object-typed key to be rejected")
	}
}
