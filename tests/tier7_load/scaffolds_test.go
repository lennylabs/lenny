// SPDX-License-Identifier: MIT

//go:build load

// Tier-7 load test scaffolds. Each test corresponds to a TESTING.md-
// named scenario that requires the production stack (gateway,
// stores, runtimes, load harness with k6 / vegeta) before it can
// run. Today each calls t.Skip with the diagnosis naming the
// missing implementation. When the load harness lands, the skip
// flips off and the scenario records its baseline JSON under
// tests/tier7_load/baselines/.

package tier7_load_test

import "testing"

// §13.17 — Streaming reconnect load.
func TestStreamingReconnectLoad(t *testing.T) {
	t.Skip("not implemented: §10.4 stream reconnect load — requires the gateway stream proxy + streaming-echo runtime + a load harness that opens/closes streams at the §13.17 target rate")
}

// §13.21 — Delegation fan-out load.
func TestDelegationFanoutLoad(t *testing.T) {
	t.Skip("not implemented: §8 delegation fan-out load — requires the §8 delegation tree implementation in the gateway + Redis budget Lua scripts + delegation-echo Standard-level runtime")
}

// §13.24 — Credential rotation load.
func TestCredentialRotationUnderLoad(t *testing.T) {
	t.Skip("not implemented: §4.9 credential rotation load — requires Token Service Postgres-backed pool + KMS signer + rotation-emitting Fallback Flow")
}

// §13.29 — Full-system pre-hardening load baseline.
func TestFullSystemLoadBaseline(t *testing.T) {
	t.Skip("not implemented: full-system load baseline — requires the entire production stack and the §17.8.2 capacity-tier targets that the baseline is measured against")
}

// §13.31 — Post-hardening SLO re-validation.
func TestFullSystemWithHardeningLoad(t *testing.T) {
	t.Skip("not implemented: post-hardening SLO re-validation — requires the §14 security hardening complete (image signing, NetworkPolicy refinement, seccomp profiles) and the Phase 13.5 baseline to compare against")
}

// §13.34 — Experiment routing load.
func TestExperimentActiveUnderLoad(t *testing.T) {
	t.Skip("not implemented: §10.7 experiment routing load — requires the ExperimentRouter interceptor + variant-pool sizing in the PoolScalingController + the bucketing hot path under load")
}

// §17a — time-to-hello-world bound. The TTHW scenario measures how
// fast a fresh deployer can reach a successful session — a
// community-launch SLO for §13.35.
func TestTimeToHelloWorld(t *testing.T) {
	t.Skip("not implemented: §17a TTHW scenario — requires the lenny-ctl bootstrap surface, the bundled chat runtime, and a measurement harness that replays the install script end-to-end")
}
