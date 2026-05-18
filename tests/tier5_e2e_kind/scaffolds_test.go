// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test scaffolds. Each test calls
// tests/testinfra/kind.SkipUnlessAvailable, which today skips
// universally with the "cluster bring-up not yet implemented"
// message. When the Kind cluster harness + Helm chart land, the
// skip flips off and each test asserts its named §13.X behaviour
// against a real Kind cluster.

package tier5_e2e_kind_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// §13.6 Phase 3 — pool scaling + mTLS critical path.
func TestWarmPool(t *testing.T)        { kind.SkipUnlessAvailable(t) }
func TestSandboxClaim(t *testing.T)    { kind.SkipUnlessAvailable(t) }
func TestPodLifecycle(t *testing.T)    { kind.SkipUnlessAvailable(t) }
func TestMTLSEnforcement(t *testing.T) { kind.SkipUnlessAvailable(t) }
func TestPoolUpgrade(t *testing.T)     { kind.SkipUnlessAvailable(t) }

// §13.7 Phase 3.5 — admission policies + lenny-ops first deploy.
func TestAdmissionPolicy(t *testing.T)       { kind.SkipUnlessAvailable(t) }
func TestAdmissionInventory(t *testing.T)    { kind.SkipUnlessAvailable(t) }
func TestLabelImmutability(t *testing.T)     { kind.SkipUnlessAvailable(t) }
func TestLennyOpsFirstDeploy(t *testing.T)   { kind.SkipUnlessAvailable(t) }
func TestBootstrapFirstInstall(t *testing.T) { kind.SkipUnlessAvailable(t) }

// §13.11 Phase 5.4 — etcd encryption at rest is exercised by the real
// test in etcd_encryption_test.go (TestEtcdEncryption).

// §13.15 Phase 5.8 — LLM Proxy + direct-mode isolation.
func TestLLMProxyProxyMode(t *testing.T)            { kind.SkipUnlessAvailable(t) }
func TestAdmissionDirectModeIsolation(t *testing.T) { kind.SkipUnlessAvailable(t) }

// §13.19 Phase 8 — checkpoint/resume + drain readiness.
func TestDrainReadinessWebhook(t *testing.T) { kind.SkipUnlessAvailable(t) }
func TestNodeDrain(t *testing.T)             { kind.SkipUnlessAvailable(t) }

// §13.27 Phase 12c — concurrent execution modes.
func TestConcurrentModes(t *testing.T) { kind.SkipUnlessAvailable(t) }

// §13.28 Phase 13 — full observability + audit + backup +
// compliance webhooks.
func TestAuditPipeline(t *testing.T)            { kind.SkipUnlessAvailable(t) }
func TestBackupRestore(t *testing.T)            { kind.SkipUnlessAvailable(t) }
func TestAdmissionDataResidency(t *testing.T)   { kind.SkipUnlessAvailable(t) }
func TestAdmissionT4NodeIsolation(t *testing.T) { kind.SkipUnlessAvailable(t) }

// §13.32 Phase 15 — cross-environment delegation.
func TestCrossEnvironmentDelegation(t *testing.T) { kind.SkipUnlessAvailable(t) }
