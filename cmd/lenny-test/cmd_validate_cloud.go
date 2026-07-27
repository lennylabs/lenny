// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// The tier-6 cloud suite runs the whole package once per provider
// listed in LENNY_CLOUD_PROVIDERS, and each test narrows itself to
// the provider it was written for. That per-provider narrowing is
// what makes a provider with no tests of its own indistinguishable
// from a provider whose tests all passed: every test in the package
// steps aside and the run reports green. The check below is the
// static counterweight. It reads the provider list the parity matrix
// declares and confirms the suite carries at least one
// managed-service test written for each of them.

// parityProviderCloudID maps the managed-Kubernetes identifiers the
// parity matrix uses to the cloud-broad identifiers
// LENNY_CLOUD_PROVIDERS carries and tests/testinfra/cloud.Provider
// parses. The matrix names the Kubernetes service (gke, eks, aks);
// the suite selects on the cloud account (gcp, aws, azure), because
// the managed services under test (Cloud SQL, RDS, Azure Database)
// are not Kubernetes services.
var parityProviderCloudID = map[string]string{
	"gke": "gcp",
	"eks": "aws",
	"aks": "azure",
}

// cloudProviderEnvPrefix is the env-var namespace each provider's
// bring-up script (scripts/cloud/<provider>/up.sh) emits for the
// resources it provisions. A managed-service test written for a
// provider reads at least one of them.
var cloudProviderEnvPrefix = map[string]string{
	"gcp":   "LENNY_GCP_",
	"aws":   "LENNY_AWS_",
	"azure": "LENNY_AZURE_",
}

// validateCloudManagedServiceCoverage enforces the tier-6
// per-provider exercise contract stated in
// tests/tier6_e2e_cloud/scaffolds_test.go: "tier-6 fails (not skip,
// not vacuous-pass) when no provider is configured, and fails
// per-provider when a configured provider isn't reachable."
//
// The suite's per-provider narrowing satisfies that contract only
// while every configured provider has tests of its own. A provider
// listed in LENNY_CLOUD_PROVIDERS with no provider-specific test in
// the package produces a run in which every test narrows itself away
// and the provider reports green having asserted nothing.
//
// LENNY_CLOUD_PROVIDERS is an operator-supplied env var and cannot be
// read statically, so the check uses the parity matrix's `providers`
// list, which is the in-tree declaration of the providers the suite
// claims to validate (TESTING.md §12.6).
//
// A provider-specific test is identified by the string literals in
// its file: the provider id it narrows on (`p != "gcp"`) or the
// provider's env-var namespace (`LENNY_GCP_...`). Literals are read
// from the parsed AST so a provider named only in a comment does not
// count as coverage. The inventory is the `managed_*_test.go` files,
// which are the tests that drive the providers' managed data services.
func validateCloudManagedServiceCoverage(parityMatrixPath, cloudDir string) checkResult {
	const label = "cloud managed-service coverage"

	providers, err := readParityProviders(parityMatrixPath)
	if err != nil {
		if os.IsNotExist(err) {
			return newResult(label, true, "parity matrix absent; no provider claims to check")
		}
		return newResult(label, false, err.Error())
	}
	if len(providers) == 0 {
		return newResult(label, false, "parity matrix declares no providers")
	}

	files, err := filepath.Glob(filepath.Join(cloudDir, "managed_*_test.go"))
	if err != nil {
		return newResult(label, false, fmt.Sprintf("glob %s: %v", cloudDir, err))
	}
	sort.Strings(files)

	// coverage maps a cloud-broad provider id to the managed-service
	// test files written for it.
	coverage := map[string][]string{}
	var problems []string
	for _, f := range files {
		targets, hasTest, err := cloudProvidersTargetedBy(f)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", filepath.Base(f), err))
			continue
		}
		if !hasTest {
			continue
		}
		for _, p := range targets {
			coverage[p] = append(coverage[p], filepath.Base(f))
		}
	}

	for _, matrixProvider := range providers {
		cloudID, ok := parityProviderCloudID[matrixProvider]
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"provider %q has no cloud id; extend parityProviderCloudID so the coverage check can resolve it",
				matrixProvider,
			))
			continue
		}
		if len(coverage[cloudID]) == 0 {
			problems = append(problems, fmt.Sprintf(
				"provider %s (%s): no managed_*_test.go in %s is written for it, so a tier-6 run with LENNY_CLOUD_PROVIDERS=%s passes without asserting anything",
				matrixProvider, cloudID, cloudDir, cloudID,
			))
		}
	}

	if len(problems) > 0 {
		return newResult(label, false, summarizeProblems(problems))
	}
	covered := make([]string, 0, len(providers))
	for _, matrixProvider := range providers {
		cloudID := parityProviderCloudID[matrixProvider]
		covered = append(covered, fmt.Sprintf("%s: %d", matrixProvider, len(coverage[cloudID])))
	}
	sort.Strings(covered)
	return newResult(label, true, fmt.Sprintf(
		"%d managed-service test file(s); every parity-matrix provider is exercised (%s)",
		len(files), strings.Join(covered, ", "),
	))
}

// readParityProviders returns the parity matrix's top-level provider
// list. A missing file yields an os.IsNotExist error so the caller
// can apply the same absent-is-acceptable convention the matrix
// validator uses.
func readParityProviders(path string) ([]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Providers []string `yaml:"providers"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parity matrix %s: invalid YAML: %w", filepath.Base(path), err)
	}
	return doc.Providers, nil
}

// cloudProvidersTargetedBy reports which cloud providers a tier-6
// test file is written for, and whether it declares any test at all.
// A file targets a provider when one of its string literals is the
// provider id (the value the suite narrows on) or carries the
// provider's env-var namespace. Only literals reachable in the AST
// count, so a provider mentioned in a comment or a doc block is not
// coverage.
func cloudProvidersTargetedBy(path string) (providers []string, hasTest bool, err error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, false, fmt.Errorf("parse: %w", err)
	}
	found := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			if node.Recv == nil && strings.HasPrefix(node.Name.Name, "Test") {
				hasTest = true
			}
		case *ast.BasicLit:
			if node.Kind != token.STRING {
				return true
			}
			lit, uerr := strconv.Unquote(node.Value)
			if uerr != nil {
				return true
			}
			for cloudID, prefix := range cloudProviderEnvPrefix {
				if lit == cloudID || strings.HasPrefix(lit, prefix) {
					found[cloudID] = true
				}
			}
		}
		return true
	})
	for cloudID := range found {
		providers = append(providers, cloudID)
	}
	sort.Strings(providers)
	return providers, hasTest, nil
}
