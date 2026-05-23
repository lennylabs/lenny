// SPDX-License-Identifier: MIT

//go:build load_kind

// Tier-7 Phase 14.5 incremental load test (post-hardening SLO
// re-validation). The §13.31 phase gate re-runs the §13.29
// pre-hardening pod_claim_latency baseline with the §14 security
// hardening enabled — cosign image-signature verification at
// admission, the §13.1 pod-security admission webhook, NetworkPolicy
// refinement, and gateway mTLS — and compares against the §13.5
// baseline JSON. The delta is the §14 hardening overhead, documented
// under tests/tier7b_load_kind/baselines/.
//
// The §14.5 phase gate runs at the §14.5 release gate against a
// cluster the §14 hardening is applied to. The PR-cadence Kind smoke
// runs against an install that ships with §13.1 pod-security defaults
// but without the cosign verification webhook, the §10.3 NetworkPolicy
// refinement, or the gateway mTLS posture; the §14 hardening overhead
// it would measure is not the production overhead the §13.31 gate
// asserts.
//
// The companion §13.31 TestFullSystemWithHardeningLoad in
// scaffolds_test.go is the §13.31 composite (every §13.29 scenario
// re-run); this test is the §13.31 sub-scenario for the
// pod_claim_latency hot path, isolated so a regression on the claim
// path can be diagnosed without re-running every other §13.29
// scenario.

package tier7b_load_kind_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/load"
)

// spec: 13.31 (Phase 14.5 — post-hardening SLO re-validation)
// diagnosis: the §13.31 post-hardening SLO re-validation is a
// phase-gated composite. The §14 security hardening (cosign image
// verification, the §13.1 pod-security admission webhook,
// NetworkPolicy refinement, gateway mTLS) is enabled at the §14.5
// release gate against a cluster the hardening is applied to; the
// PR-cadence Kind e2e install does not apply that hardening, so the
// overhead the §13.31 gate measures has no surface on the smoke run.
// The post_hardening_slo scenario is the §13.31 substrate the cloud
// tier runs; the Kind smoke skips honestly until the e2e overlay
// applies the §14 hardening.
func TestPostHardeningSLO(t *testing.T) {
	kind.InstallLenny(t)
	load.SkipUnlessAvailable(t)
	t.Skip("phase-gated: the §13.31 post-hardening SLO re-validation runs at the " +
		"Phase 14.5 release gate against a cluster the §14 security hardening is " +
		"applied to (cosign image verification, the §13.1 pod-security admission " +
		"webhook, NetworkPolicy refinement, gateway mTLS); the PR-cadence Kind e2e " +
		"install does not apply that hardening, so the overhead the §13.31 gate " +
		"measures has no surface on the smoke run")
}
