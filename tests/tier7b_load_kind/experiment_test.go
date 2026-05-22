// SPDX-License-Identifier: MIT

//go:build load_kind

// Tier-7 Phase 16.5 incremental load re-baseline (experiments). The
// §13.34 phase gate baselines the §10.7 ExperimentRouter hot path —
// every POST /v1/sessions/start with a runtimeRef equal to the active
// experiment's baseRuntime runs through the §10.7 percentage-mode
// HMAC-SHA256 bucketing — and the PoolScalingController's variant-pool
// `minWarm` coupling at the §16.5 release gate. The §13.34 phase gate
// re-runs the §12.7 pod_claim_latency scenario with an active
// experiment seeded so the bucketing path is on every claim, and the
// per-variant claim P99 is compared against the §12.7 pod_claim_latency
// pre-experiment baseline.
//
// The PR-cadence Kind smoke runs against an e2e install that ships
// without an active experiment, an active baseRuntime registered in
// the runtime store, or the active variant pools registered in the
// warm-pool store; seeding the §10.7 admission-time isolation
// monotonicity check requires every variant pool be at least as
// restrictive as the experiment's base runtime, which the smoke fixture
// does not configure. The experiment_load scenario is the §13.34
// substrate the cloud tier runs.

package tier7b_load_kind_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/load"
)

// spec: 13.34 (Phase 16.5 — experiment load re-baseline)
// diagnosis: the §13.34 experiment_load run has no active experiment
// to exercise on the e2e Kind build. The §10.7 ExperimentRouter
// bucketing path runs only when an experiment is active for the
// caller's tenant whose baseRuntime equals the request's runtimeRef;
// the e2e gateway ships with the in-memory runtime, pool, and
// experiment stores empty, so the bucketing path has nothing to
// exercise. The experiment_load scenario is the §13.34 substrate the
// cloud tier runs; the Kind smoke skips honestly until the e2e
// overlay seeds an active experiment whose variants reference
// registered pools.
func TestExperimentLoad(t *testing.T) {
	kind.InstallLenny(t)
	load.SkipUnlessAvailable(t)
	t.Skip("phase-gated: the §13.34 experiment_load run measures the §10.7 " +
		"ExperimentRouter bucketing overhead with an active experiment seeded; the " +
		"PR-cadence Kind e2e install starts with an empty experiment store, an empty " +
		"runtime store, and an empty pool store, so the bucketing path has nothing " +
		"to exercise on the smoke run. The cloud tier seeds the §10.7 experiment, " +
		"the baseRuntime, and the variant pools, then runs the strict §13.34 " +
		"regression comparison")
}
