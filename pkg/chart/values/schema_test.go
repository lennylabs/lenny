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
// invariant. spec: §17.6.
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
// means Helm would reject `helm install` with stock defaults. spec: §17.6.
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
// they must validate directly. spec: §17.9.2.
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

// TestCatalogAnswerFilesConformToSchema lints every shipped §17.9.2
// answer file against the schema and asserts the full curated catalog is
// present. Answer files are chart-values fragments layered with `-f`, so
// each must validate directly; a fragment that drifts out of sync with
// the chart values (a renamed key, a removed top-level section) fails
// here rather than at `helm install`. spec: §17.9.2.
// F-17.9.1.
func TestCatalogAnswerFilesConformToSchema_spec_17_9_2_1374(t *testing.T) {
	schema, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	root := repoRoot(t)
	dir := filepath.Join(root, "charts", "lenny", "answers", "catalog")
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		t.Fatalf("glob catalog: %v", err)
	}
	got := map[string]bool{}
	for _, f := range files {
		base := filepath.Base(f)
		got[base] = true
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", base, err)
		}
		if err := ValidateYAML(schema, data); err != nil {
			t.Errorf("catalog answer file %s does not conform: %v", base, err)
		}
	}
	// spec: §17.9.2 — the curated catalog enumerates these
	// nine files. A missing file means the documented composition is not
	// reachable from the shipped chart.
	for _, want := range []string{
		"laptop.yaml",
		"docker-compose.yaml",
		"eks-small-team.yaml",
		"eks-production.yaml",
		"gke-production.yaml",
		"aks-production.yaml",
		"openshift-self-managed.yaml",
		"bare-metal-self-managed.yaml",
		"airgap-self-managed.yaml",
	} {
		if !got[want] {
			t.Errorf("§17.9.2 catalog is missing %s", want)
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
// additionalProperties:false). spec: §17.6.
func TestRejectsUnknownTopLevelKey(t *testing.T) {
	err := ValidateYAML(mustSchema(t), []byte("gatway:\n  replicas: 2\n"))
	if err == nil {
		t.Fatal("expected an unknown top-level key to be rejected")
	}
}

// TestRejectsDevTenantIDPattern enforces the §17.6 schema
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

// TestRejectsNoEnvironmentPolicyEnum enforces the §17.6
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

// TestRejectsCapabilityTokenTTLOutOfRange_spec_10_2 enforces the §10.2
// 120..3600 bound on auth.capabilityTokenTtlSeconds, which the gateway and
// the Token Service also refuse at startup, and pins the removal of the
// retired playground.bearerTtlSeconds key (no alias is kept).
//
// spec: 10.2 (Authentication), 27 (Web Playground)
func TestRejectsCapabilityTokenTTLOutOfRange_spec_10_2(t *testing.T) {
	schema := mustSchema(t)
	cases := []struct {
		name  string
		yaml  string
		valid bool
	}{
		{"below floor", "auth:\n  capabilityTokenTtlSeconds: 119\n", false},
		{"at floor", "auth:\n  capabilityTokenTtlSeconds: 120\n", true},
		{"default", "auth:\n  capabilityTokenTtlSeconds: 900\n", true},
		{"at ceiling", "auth:\n  capabilityTokenTtlSeconds: 3600\n", true},
		{"above ceiling", "auth:\n  capabilityTokenTtlSeconds: 3601\n", false},
		{"siblings stay open", "auth:\n  multiTenant: true\n  capabilityTokenTtlSeconds: 900\n", true},
		{"retired playground key", "playground:\n  bearerTtlSeconds: 900\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateYAML(schema, []byte(tc.yaml))
			if tc.valid && err != nil {
				t.Errorf("expected %q to pass: %v", tc.yaml, err)
			}
			if !tc.valid && err == nil {
				t.Errorf("expected %q to be rejected", tc.yaml)
			}
		})
	}
}

// TestKMSSignQuotaPerSecondMinimum_spec_17_8_2 enforces the §17.8.2
// minimum of 1 on kms.signQuotaPerSecond while leaving the other kms
// sub-keys free-form. The required-on-cloud rule is a render guard in the
// chart (charts/lenny/tests/kms-guard_test.yaml).
//
// spec: 17.8.2 (Capacity Tier Reference), 10.2 (Authentication)
func TestKMSSignQuotaPerSecondMinimum_spec_17_8_2(t *testing.T) {
	schema := mustSchema(t)
	if err := ValidateYAML(schema, []byte("kms:\n  signQuotaPerSecond: 0\n")); err == nil {
		t.Error("expected signQuotaPerSecond 0 to be rejected")
	}
	if err := ValidateYAML(schema, []byte("kms:\n  provider: aws\n  aliases: \"\"\n  signQuotaPerSecond: 1\n")); err != nil {
		t.Errorf("expected signQuotaPerSecond 1 with sibling keys to pass: %v", err)
	}
}

// TestDeploymentTierClosedSet_spec_16_1_1 pins global.deploymentTier to the
// §16.1.1 closed set: tier1, tier2, and tier3 pass, and every other value,
// including the empty string, a case variant, and a typo, is refused. An
// omitted key passes and keeps the values.yaml default tier1. The value
// also renders the lenny-ops --platform-tier flag, where an off-set value
// would drop the §25.4 Tier 2/3 required Idempotency-Key. F-25.4.38.
//
// spec: 16.1.1 (Attribute Naming) deployment_tier, 25.4 (The lenny-ops Service) Idempotency-Key, 17.6 (Packaging and Installation)
func TestDeploymentTierClosedSet_spec_16_1_1(t *testing.T) {
	schema := mustSchema(t)
	for _, tier := range []string{"tier1", "tier2", "tier3"} {
		if err := ValidateYAML(schema, []byte("global:\n  deploymentTier: "+tier+"\n")); err != nil {
			t.Errorf("expected deploymentTier %q to pass: %v", tier, err)
		}
	}
	for _, tier := range []string{`""`, "TIER2", "Tier3", "tier4", "tier0", "teir2", "2", `" tier2"`} {
		if err := ValidateYAML(schema, []byte("global:\n  deploymentTier: "+tier+"\n")); err == nil {
			t.Errorf("expected deploymentTier %s to be rejected", tier)
		}
	}
	if err := ValidateYAML(schema, []byte("global:\n  devMode: false\n")); err != nil {
		t.Errorf("expected an omitted deploymentTier to pass: %v", err)
	}
}

// TestRejectsWrongType enforces the "wrong types" half of §17.6:
// a scalar where the schema expects an object is rejected.
func TestRejectsWrongType_spec_17_6_653(t *testing.T) {
	if err := ValidateYAML(mustSchema(t), []byte("global: not-an-object\n")); err == nil {
		t.Error("expected a scalar for an object-typed key to be rejected")
	}
}
