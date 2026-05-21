// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 e2e_cloud package entry point. Each test now lives in a
// dedicated file:
//
//   aws_resources_test.go      — TestCloudKMS, TestCloudCSI
//                                (cloud-adapter round-trips against
//                                 the Terraform-provisioned S3 bucket
//                                 + KMS key).
//   cluster_assertions_test.go — TestGvisorIsolation, TestKataIsolation,
//                                TestMultiZoneDR, TestManagedIngress,
//                                TestCloudOIDC, TestCloudSecretStore,
//                                TestMultiAZMinIO, TestCloudObservability,
//                                TestCloudBillingExport
//                                (post-install chart-state assertions
//                                 against the EKS cluster).
//
// This file keeps the shared requireCloud guard the other files use.
// scripts/cloud/aws/run-e2e.sh wires the env vars (LENNY_CLOUD_PROVIDER,
// LENNY_AWS_KMS_KEY_ARN, LENNY_AWS_ARTIFACT_BUCKET) the tests read.

package tier6_e2e_cloud_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/cloud"
)

// requireCloud is the cloud-guard every tier-6 test starts with. It
// skips when LENNY_CLOUD_PROVIDER is unset, the provider CLI is
// missing, or the CLI is unauthenticated.
func requireCloud(t *testing.T) cloud.Provider {
	t.Helper()
	p := cloud.FromEnv()
	cloud.SkipUnlessAvailable(t, p)
	return p
}
