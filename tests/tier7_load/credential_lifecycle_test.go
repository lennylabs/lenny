// SPDX-License-Identifier: MIT

//go:build load

// Tier-7 Phase 11.5 incremental load test (credential lifecycle). The
// §13.24 phase gate baselines the §4.9 / §15.1 end-user credential
// lifecycle — register → list → rotate → revoke → delete — under a
// rate-bounded executor. The PR-cadence smoke form drives the
// credential_lifecycle k6 scenario through the e2e Kind gateway and
// asserts the run completed within the smoke health gate.
//
// The companion §12.7 credential_rotation_under_load scenario (in
// scaffolds_test.go) holds a sustained session-creation load with a
// periodic credential-pool read; the §11.5 scenario exercises the
// write-side lifecycle, so the §4.9 register / rotate / revoke /
// delete path is baselined alongside the read path.

package tier7_load_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/load"
)

// spec: 13.24 (Phase 11.5 — incremental load test, credential lifecycle)
// diagnosis: the §13.24 credential_lifecycle smoke run regressed or
// failed. The scenario plays the full §4.9 lifecycle through
// /v1/credentials against the e2e gateway; a failure means a
// register, rotate, revoke, or delete errored under load or its
// latency regressed beyond the baseline budget. The §11.5 phase gate
// compares the smoke run against the committed baseline JSON. Inspect
// the k6 output for the failing check and the gateway logs for the
// credential-store error.
func TestCredentialLifecycle(t *testing.T) {
	_, baseURL := prepareGateway(t)
	res := load.RunScenario(t, "credential_lifecycle", smokeOptions(baseURL, nil))
	assertScenarioRan(t, "credential_lifecycle", res)
}
