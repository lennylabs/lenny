// SPDX-License-Identifier: MIT

//go:build load_kind

// Tier-7 Postgres write-baseline tests. This file groups the §12.3
// Postgres write-ceiling scenario tests under one go-build tag so the
// per-test diagnoses can name the §12.3 SLO rather than the §12.7
// scenario index alone.
//
// The smoke profile is the same as scaffolds_test.go: 20 VUs across 25
// seconds against the Kind e2e gateway via the port-forwarded
// lenny-gateway Service. Per the established tier-7 pattern, the smoke
// gate is the assertScenarioRan error-rate budget; the IOPS-ceiling
// regression check is captured (and validated against the §12.3
// per-tier ceiling table) but not asserted on laptop Kind because the
// e2e cluster's single-replica gateway intentionally caps throughput
// below the smallest §12.3 ceiling. The cloud-small nightly and the
// pre-release runs are where the production IOPS ceilings are
// asserted.

package tier7b_load_kind_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/load"
)

// postgresWriteCeiling is the §12.3 sustained write ceiling for the
// Tier-1 reference Postgres instance class (2 vCPU / 4 GB,
// db.t3.medium). The smoke run targets Tier 1; the cloud nightly runs
// raise the ceiling to the operator-configured value via
// postgres.writeCeilingIops.
const postgresWriteCeiling = 200

// postgresBurstCeiling is the §12.3 burst ceiling: 3× the sustained
// ceiling. The spec table places Tier 1 burst at ~600 IOPS.
const postgresBurstCeiling = 3 * postgresWriteCeiling

// spec: 12.3 (Postgres HA write-baseline)
// diagnosis: the §12.3 postgres write baseline regressed or failed.
// The postgres_write_burst scenario commits one row to the Postgres
// sessions table per POST /v1/sessions, so the scenario drives the
// §12.3 write path. A failure means a write errored under the burst,
// the burst's measured IOPS exceeded the spec ceiling, or the smoke
// run's error rate exceeded the assertScenarioRan budget. Inspect the
// k6 output for the failing check and the gateway logs for the error.
func TestPostgresWriteBaseline(t *testing.T) {
	_, baseURL := prepareGateway(t)
	res := load.RunScenario(t, "postgres_write_burst", smokeOptions(baseURL, nil))
	assertScenarioRan(t, "postgres_write_burst", res)
	assertWithinWriteCeiling(t, res)
}

// assertWithinWriteCeiling captures the IOPS the smoke run produced
// and validates it against the §12.3 sustained and burst ceilings.
// Each POST /v1/sessions in postgres_write_burst commits a single row
// to the Postgres sessions table, so the scenario's HTTP throughput is
// a faithful proxy for write IOPS. Per the tier-7 smoke convention,
// the laptop Kind gateway cannot reach the spec ceilings, so a recorded
// throughput well below the sustained ceiling is the expected pass
// state; the function asserts only the upper-bound invariant (burst
// stays within 3× the ceiling) and logs the measured value so a
// regression that pushes IOPS above the ceiling is visible in the test
// output even when the smoke run still meets the error-rate budget.
func assertWithinWriteCeiling(t *testing.T, res load.Result) {
	t.Helper()
	measured := res.Throughput
	if measured > float64(postgresBurstCeiling) {
		t.Errorf("§12.3 postgres_write_burst: measured throughput %.1f IOPS exceeds the burst ceiling %d IOPS "+
			"(3× the sustained ceiling %d)", measured, postgresBurstCeiling, postgresWriteCeiling)
	}
	t.Logf("§12.3 postgres_write_burst: measured %.1f IOPS (sustained ceiling %d, burst ceiling %d); "+
		"laptop Kind throughput is below the spec ceiling by design",
		measured, postgresWriteCeiling, postgresBurstCeiling)
}
