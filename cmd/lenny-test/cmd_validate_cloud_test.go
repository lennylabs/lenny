// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tier-6 cloud suite runs once per configured provider and every
// test narrows itself to the provider it was written for, so a
// provider with no tests of its own reports green having asserted
// nothing. These cases pin the static gate that rejects that
// outcome, per the tier-6 contract in
// tests/tier6_e2e_cloud/scaffolds_test.go ("tier-6 fails (not skip,
// not vacuous-pass) when no provider is configured, and fails
// per-provider when a configured provider isn't reachable") and the
// provider parity matrix in TESTING.md §12.6.

// writeCloudSuite materializes a fake tier-6 directory whose files
// map basename to contents, and returns the directory.
func writeCloudSuite(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// threeProviderMatrix is the provider list the checked-in matrix
// declares.
const threeProviderMatrix = `
version: 1
providers: [gke, eks, aks]
capabilities:
  - name: cloud_kms
    status:
      gke: planned
      eks: planned
      aks: planned
`

// awsTestFile narrows on the AWS env namespace the way
// managed_rds_test.go does.
const awsTestFile = `package tier6_e2e_cloud_test

import (
	"os"
	"testing"
)

func TestCloudRDSTLSRequired(t *testing.T) {
	if os.Getenv("LENNY_AWS_RDS_ENDPOINT") == "" {
		return
	}
}
`

// gcpTestFile narrows on the provider id the way
// managed_cloudsql_test.go does.
const gcpTestFile = `package tier6_e2e_cloud_test

import "testing"

func TestCloudSQLTLSRequired(t *testing.T) {
	p := provider()
	if p != "gcp" {
		t.Skipf("not gcp")
	}
}
`

const azureTestFile = `package tier6_e2e_cloud_test

import "testing"

func TestCloudAzureRedisTLSRequired(t *testing.T) {
	p := provider()
	if p != "azure" {
		t.Skipf("not azure")
	}
}
`

// A suite with a managed-service test for every provider the matrix
// declares satisfies the gate.
//
// spec: TESTING.md §12.6 (tier 6 provider parity matrix)
func TestValidateCloudManagedServiceCoverageHappy(t *testing.T) {
	matrix := writeYAML(t, "parity-matrix.yaml", threeProviderMatrix)
	dir := writeCloudSuite(t, map[string]string{
		"managed_rds_test.go":        awsTestFile,
		"managed_cloudsql_test.go":   gcpTestFile,
		"managed_azurecache_test.go": azureTestFile,
	})
	r := validateCloudManagedServiceCoverage(matrix, dir)
	expectPass(t, r)
	for _, want := range []string{"gke: 1", "eks: 1", "aks: 1"} {
		if !strings.Contains(r.detail, want) {
			t.Errorf("detail missing %q: %s", want, r.detail)
		}
	}
}

// A matrix that claims a provider the suite has no test for is the
// vacuous-pass case: the tier-6 run against that provider would skip
// every test and report green. The gate rejects it.
//
// spec: TESTING.md §12.6 (tier 6 provider parity matrix)
func TestValidateCloudManagedServiceCoverageRejectsUnexercisedProvider(t *testing.T) {
	matrix := writeYAML(t, "parity-matrix.yaml", threeProviderMatrix)
	dir := writeCloudSuite(t, map[string]string{
		"managed_rds_test.go":      awsTestFile,
		"managed_cloudsql_test.go": gcpTestFile,
	})
	expectFail(t, validateCloudManagedServiceCoverage(matrix, dir), "aks", "azure")
}

// An empty suite fails for every declared provider rather than
// passing on an empty inventory.
//
// spec: TESTING.md §12.6 (tier 6 provider parity matrix)
func TestValidateCloudManagedServiceCoverageRejectsEmptySuite(t *testing.T) {
	matrix := writeYAML(t, "parity-matrix.yaml", threeProviderMatrix)
	dir := writeCloudSuite(t, nil)
	r := validateCloudManagedServiceCoverage(matrix, dir)
	expectFail(t, r, "gke", "eks", "aks")
	if !strings.Contains(r.detail, "3 issue(s)") {
		t.Errorf("expected one problem per provider: %s", r.detail)
	}
}

// A provider named only in a comment or a doc block is not coverage.
// The literals are read from the parsed AST for exactly this case:
// otherwise a file that mentions Azure in prose would satisfy the
// gate for AKS.
//
// spec: TESTING.md §12.6 (tier 6 provider parity matrix)
func TestValidateCloudManagedServiceCoverageIgnoresComments(t *testing.T) {
	matrix := writeYAML(t, "parity-matrix.yaml", threeProviderMatrix)
	dir := writeCloudSuite(t, map[string]string{
		"managed_rds_test.go":      awsTestFile,
		"managed_cloudsql_test.go": gcpTestFile,
		// Mentions azure and LENNY_AZURE_ in prose only.
		"managed_azuredb_test.go": `package tier6_e2e_cloud_test

// The azure equivalent reads LENNY_AZURE_FLEXIBLE_POSTGRES_HOST.
// Not implemented yet.
`,
	})
	expectFail(t, validateCloudManagedServiceCoverage(matrix, dir), "aks", "azure")
}

// A file carrying the provider's literals but declaring no test
// function is not coverage either.
//
// spec: TESTING.md §12.6 (tier 6 provider parity matrix)
func TestValidateCloudManagedServiceCoverageRequiresATestFunc(t *testing.T) {
	matrix := writeYAML(t, "parity-matrix.yaml", threeProviderMatrix)
	dir := writeCloudSuite(t, map[string]string{
		"managed_rds_test.go":      awsTestFile,
		"managed_cloudsql_test.go": gcpTestFile,
		"managed_azuredb_test.go": `package tier6_e2e_cloud_test

func azureHost() string { return "LENNY_AZURE_FLEXIBLE_POSTGRES_HOST" }
`,
	})
	expectFail(t, validateCloudManagedServiceCoverage(matrix, dir), "aks", "azure")
}

// A provider id the check cannot map to a cloud account fails rather
// than silently counting as covered.
//
// spec: TESTING.md §12.6 (tier 6 provider parity matrix)
func TestValidateCloudManagedServiceCoverageUnknownProvider(t *testing.T) {
	matrix := writeYAML(t, "parity-matrix.yaml", `
version: 1
providers: [gke, eks, aks, doks]
capabilities:
  - name: cloud_kms
    status:
      gke: planned
`)
	dir := writeCloudSuite(t, map[string]string{
		"managed_rds_test.go":        awsTestFile,
		"managed_cloudsql_test.go":   gcpTestFile,
		"managed_azurecache_test.go": azureTestFile,
	})
	expectFail(t, validateCloudManagedServiceCoverage(matrix, dir), "doks")
}

// A matrix with an empty providers list is a defect rather than a
// vacuously satisfied gate.
//
// spec: TESTING.md §12.6 (tier 6 provider parity matrix)
func TestValidateCloudManagedServiceCoverageEmptyProviders(t *testing.T) {
	matrix := writeYAML(t, "parity-matrix.yaml", "version: 1\ncapabilities: []\n")
	dir := writeCloudSuite(t, map[string]string{"managed_rds_test.go": awsTestFile})
	expectFail(t, validateCloudManagedServiceCoverage(matrix, dir), "no providers")
}

// An absent matrix follows the same convention the matrix validator
// applies: nothing is claimed, so nothing is checked.
//
// spec: TESTING.md §12.6 (tier 6 provider parity matrix)
func TestValidateCloudManagedServiceCoverageAbsentMatrix(t *testing.T) {
	r := validateCloudManagedServiceCoverage(filepath.Join(t.TempDir(), "missing.yaml"), t.TempDir())
	expectPass(t, r)
	if !strings.Contains(r.detail, "absent") {
		t.Errorf("detail should say absent: %s", r.detail)
	}
}

// The checked-in tier-6 suite covers every provider the checked-in
// matrix declares, so the tier-0 gate is green on a clean tree. This
// is the case that regresses when a provider's managed-service tests
// are removed or a provider is added to the matrix ahead of its
// tests.
//
// spec: TESTING.md §12.6 (tier 6 provider parity matrix)
func TestValidateCloudManagedServiceCoverageRepoTreeCovered(t *testing.T) {
	root := repoRoot()
	_, _, _, _, parityMatrix := yamlPaths(root)
	expectPass(t, validateCloudManagedServiceCoverage(parityMatrix, filepath.Join(root, cloudTestDir)))
}
