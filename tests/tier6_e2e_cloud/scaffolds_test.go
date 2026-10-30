// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 e2e_cloud package entry point. Each test lives in a
// dedicated file (aws_resources_test.go, cluster_assertions_test.go,
// behavior_test.go, eks_platform_test.go, managed_elasticache_test.go,
// managed_rds_test.go, session_lifecycle_test.go).
//
// # Multi-provider iteration
//
// The package's TestMain (below) reads LENNY_CLOUD_PROVIDERS
// (comma-separated `aws,gcp,azure`) and runs the full tier-6 suite
// once per provider. Each iteration sets LENNY_CLOUD_PROVIDER for
// the duration of that run, so individual tests pick it up via
// cloud.FromEnv. Per-provider env bundles (LENNY_AWS_KMS_KEY_ARN,
// LENNY_GCP_PROJECT, etc.) are provisioned by
// scripts/cloud/<provider>/up.sh and sourced by the operator before
// running the suite; the operator provides only the cloud-auth
// credentials (AWS_PROFILE / gcloud auth / az login).
//
// When LENNY_CLOUD_PROVIDERS is unset, TestMain runs the suite
// once with an empty provider; cloud.SkipUnlessAvailable then
// fails each test with the documented "configure a provider"
// diagnosis. The user-facing contract: tier-6 fails (not skip,
// not vacuous-pass) when no provider is configured, and fails
// per-provider when a configured provider isn't reachable.

package tier6_e2e_cloud_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/cloud"
)

// requireCloud is the cloud-guard every tier-6 test starts with. It
// fails the test when LENNY_CLOUD_PROVIDER is unset (set by
// TestMain for each iteration), the provider CLI is missing, or
// the CLI is unauthenticated.
func requireCloud(t *testing.T) cloud.Provider {
	t.Helper()
	p := cloud.FromEnv()
	cloud.SkipUnlessAvailable(t, p)
	return p
}

// TestMain iterates the tier-6 suite once per provider listed in
// LENNY_CLOUD_PROVIDERS. The non-zero exit codes across iterations
// are OR'd so a single bad provider fails the run.
func TestMain(m *testing.M) {
	providers := cloud.ConfiguredProviders()
	if len(providers) == 0 {
		// No provider configured — let SkipUnlessAvailable inside
		// each test report the missing-provider diagnosis as a
		// FAIL (the helper is fail-closed since the operator owns
		// the contract).
		os.Exit(m.Run())
	}
	overallCode := 0
	for _, p := range providers {
		_ = os.Setenv("LENNY_CLOUD_PROVIDER", string(p))
		fmt.Fprintf(os.Stderr, "tier-6: running suite against LENNY_CLOUD_PROVIDER=%s (out of %d configured)\n", p, len(providers))
		if code := m.Run(); code != 0 {
			overallCode = code
		}
	}
	os.Exit(overallCode)
}
