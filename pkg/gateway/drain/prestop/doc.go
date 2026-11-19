// SPDX-License-Identifier: MIT

// Package prestop implements the §4.4 / §10.1 preStop
// drain hook the gateway exposes as an internal HTTP endpoint.
//
// The hook is invoked by Kubernetes via
// `lifecycle.preStop.httpGet` when a gateway pod is scheduled for
// termination (rolling update, HPA scale-down, kubectl drain). It
// runs three things in order:
//
//  1. Reads `terminationGracePeriodSeconds` from the deployment
//     environment to bound the total preStop budget.
//  2. Selects the §10.1 tiered checkpoint cap for every session
//     this replica coordinates per Stage 2: the cap tier is chosen
//     from `last_checkpoint_workspace_bytes` (Postgres + per-replica
//     cache). Each selection bumps `lenny_prestop_cap_selection_total`
//     labeled by source (postgres | postgres_null | cache_hit |
//     cache_miss_max_tier).
//  3. Iterates active sessions on this replica and triggers
//     synchronous eviction checkpoints via the in-process
//     checkpointer. The cap selected above is the per-session
//     deadline.
//
// The hook returns HTTP 200 with a JSON summary of the attempted,
// completed, and skipped sessions; Kubernetes ignores the body but
// the summary is useful for log scraping and ad-hoc curl probes. The
// staged drain is the one §4.4 and §10.1 describe.
//
// spec: GWDRAIN
package prestop
