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

	// Proposal 0023: the §8.6 budget-exhaustion lease-extension episode —
	// concurrent exhausting sessions in one tree join one per-tree episode
	// and the per-session fan-out raises or terminates each, goroutine-leak
	// free and -race clean.
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/extension_episode_fanout"

	// Proposal 0023 S3/S4: the §11.2/§8.6 sessionbudget.Enforcer under
	// concurrent Record/Allow/RaiseBudget/TerminateSession/Forget — the
	// out-of-band SessionReclaimer fan-out races the in-path record/gate,
	// with no deadlock, no lost deny-flag clear, and budget monotonicity,
	// -race clean.
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/raisebudget_enforcer_race"

	// Proposal 0023 S4: the §11.2/§8.6 gateway LLM Proxy write path under
	// concurrency — many in-flight ServeHTTP requests read the shared
	// deny-next-request flag through the handler's pre-flight BudgetGate.Allow
	// gate while the out-of-band episode fan-out mutates it (RaiseBudget /
	// TerminateSession), with no torn read of the deny flag and no request
	// admitted after its session terminated, -race clean.
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/budget_gate_deny_flag_race"

	// Proposal 0034 (F-5.2.32): the §5.2 step 7 recycle boundary under
	// concurrent scrub reports — a clean scrub on a vm-restart pool must
	// retire-and-reprovision (draining, ReasonVMRestartReprovision) rather
	// than reuse the pod (a fail-open that returns it to cross-tenant service
	// without a fresh guest), and a standard pool reuses, with the shared
	// per-pod occupancy state raced across reports, -race clean.
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/vm_restart_recycle_disposition"

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

	// Proposal 0007 eager-claim: concurrent creates against a finite pool
	// (fail-fast exhaustion at /create) and coordinator handoff during the
	// create → finalize → start window (binding reconstructed, no pod/lease leak).
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/create_finalize_start_handoff"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/create_pool_exhaustion"
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

	// Wave 8: resiliency scenarios (load shedding, retry storms,
	// cascading failure, bulkhead isolation, graceful shutdown,
	// degraded provider, timeout propagation, KMS outage, slow
	// loris, HOL blocking, client disconnect, reconnect backoff,
	// partial-response retry, breaker recovery, oversized burst,
	// header cap, auth failure storm, conn exhaustion + recovery,
	// audit disk full, low-resource startup).
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/auth_failure_storm"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/bulkhead_thread_pool_isolation"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/cascading_failure_isolation"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/client_disconnect_mid_stream"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/connection_exhaustion_recovery"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/degraded_llm_provider"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/disk_full_audit_handling"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/gateway_load_shedding"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/graceful_shutdown_drain"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/head_of_line_blocking_isolation"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/header_size_cap"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/high_error_rate_circuit_open"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/kms_outage_session_continuation"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/low_resource_startup"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/oversized_request_rejection_recovery"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/partial_response_retry_idempotency"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/retry_storm_dampening"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/slow_loris_protection"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/streaming_reconnect_backoff"
	_ "github.com/lennylabs/lenny/tests/tier7a_load_local/scenarios/timeout_propagation"
)
