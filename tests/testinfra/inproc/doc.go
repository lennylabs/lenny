// SPDX-License-Identifier: MIT

// Package inproc boots a single-binary Lenny in-process against
// in-memory state for tier-7a multi-component scenarios.
//
// Scenarios that need more than a single component use inproc.Env to
// bring everything up in one goroutine pool with:
//
//   - miniredis backing the §11.1 admission counter the gateway
//     transacts against on every session create
//   - the in-memory sessionstore adapter, behind the same
//     sessionstore.Store contract the Postgres adapter implements
//   - the fakekube package for the Kubernetes API surface
//   - the runtime stub for adapter calls
//   - the §15.1 REST session surface from pkg/gateway/sessionserver,
//     wrapped in the §11.5 idempotency middleware from
//     pkg/gateway/middleware/idempotency, bound to a loopback port
//     inside the test process
//
// The HTTP surface is assembled from the packages cmd/lenny-gateway
// assembles its own surface from, so a scenario driving
// Env.GatewayURL() exercises Lenny code: the §15.1 state machine and
// precondition table, the §15.1 error envelope, the §11.5
// claim/replay/in-flight contract, and the §11.1 Redis-backed
// admission counter.
//
// Used by tier-7a multi-component scenarios under
// tests/tier7a_load_local/scenarios/.
package inproc
