// SPDX-License-Identifier: MIT

//go:build load_local

// Package scenarios pulls in every tier-7a scenario subpackage so the
// scaffolds_test.go in the parent directory can iterate the loadgen
// Registry. Each blank import below triggers the scenario subpackage's
// init(), which registers a factory against loadgen.DefaultRegistry().
//
// TESTING.md §12.7.a catalogues the full scenario list. Wave 3 adds
// to this import set as scenarios land.
package scenarios

import (
	// Wave 2: first scenario end-to-end.
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/slot_counter_race"

	// Wave 3: §3.4 regression scenarios.
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/audit_chain_concurrent"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/circuit_breaker_state_machine"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/idempotency_replay_race"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/lease_extension_race"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/quota_decrement_race"

	// Wave 3: §3.5 component-isolated benches.
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/auth_jwt_verify_throughput"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/experiment_bucket_determinism"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/sessionstore_write_amplification"

	// Wave 3: §3.5 multi-component scenarios.
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/clock_skew_admission"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/oversized_payload_rejection"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/redis_disconnect_midflight"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/runtime_adapter_slow_response"

	// Wave 3 follow-up: §3.5 multi-component scenarios that drive
	// the inproc gateway HTTP listener landed in W2/W3 follow-up.
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/crd_watch_event_flood"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/error_injection_matrix"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/idempotency_cache_eviction"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/streaming_reconnect_storm"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/tenant_isolation_load"

	// Wave 7 follow-up: scenarios closing the deferred §3.4 and §3.5
	// catalogue against the now-available fakekube SSA semantics and
	// the broader inproc surface.
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/audit_sink_backpressure"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/checkpointer_concurrent"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/claim_admission_ordering"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/clientgo_throttle_floor"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/connector_oauth_refresh_race"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/controller_reconcile_rate"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/credassign_lease_rotation"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/delegation_depth_n"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/goroutine_leak_long_run"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/large_workspace_upload"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/memory_leak_long_run"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/mixed_workload"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/pg_pool_exhaustion"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/pgtenant_rls_isolation_load"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/pubsub_fanout"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/pubsub_slow_consumer"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/terminate_path_branching"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/tokenservice_issue_burst"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/webhook_admission_latency"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/webhook_tls_rotation_under_load"
)
