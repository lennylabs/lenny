// SPDX-License-Identifier: MIT

// Package inproc boots a single-binary Lenny in-process against
// in-memory state for tier-7a multi-component scenarios.
//
// Scenarios that need more than a single component (gateway plus
// admission, gateway plus controller, gateway plus token-service)
// use inproc.Env to bring everything up in one goroutine pool with:
//
//   - miniredis for the slot counter and idempotency store
//   - an embedded Postgres-compatible adapter for sessionstore and
//     auditstore
//   - the fakekube package for the Kubernetes API surface
//   - the runtime stub for adapter calls
//   - the gateway, controller, and admission webhook bound to
//     loopback ports inside the test process
//
// The Wave 2 cut exposes the configuration knobs and the Env type;
// the Lenny boot path is wired in Wave 3 as the first scenario
// that needs it lands.
//
// Used by tier-7a multi-component scenarios under
// tests/tier7a_load_local/scenarios/.
package inproc
