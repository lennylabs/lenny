---
layout: default
title: "Forward compatibility"
parent: "Testing"
nav_order: 7
description: §23 surfaces — what's deliberately out of scope for v1 so test authors don't bake v1-only assumptions into the harness.
---

# Forward compatibility

§23 of TESTING.md lists areas that are explicitly **out of scope** for v1. The test infrastructure carries interface stubs and unimplemented suites for each, so v2 can land without rewriting the harness.

When writing a new test, check this list to make sure you're not baking in a v1-only assumption that will break the next version.

## Out-of-scope surfaces

### A2A (Agent-to-Agent) adapters

The current §9 delegation model uses Lenny's MCP fabric. The future A2A protocol (under draft at agent2agent.com) is **not** wired into the test surface; existing tests should not assume agent-to-agent traffic happens over MCP forever.

Forward-compat hook: the `pkg/delegation/` interfaces are protocol-neutral. The cycle detector and lease validator operate on identity tuples, not on the wire shape.

### AP (Agent Protocol) adapters

Similar to A2A — the IETF Agent Protocol working group's surface is not yet stable. The platform's tier-3 contract tests (`tests/tier3_contract/`) are organised by surface so AP can ship as a parallel directory (`tests/tier3_contract/ap/`).

### Multi-cluster federation

§4 talks about a single Lenny cluster. Multi-cluster (federated gateways, cross-cluster session migration) is v2. Don't assume a session lives on one cluster for its lifetime — tests should treat the cluster id as part of the session identity.

### SSH git URLs

`workspaceplan.gitClone` accepts HTTPS only today. SSH (and `git://`) are documented as future. Tests under `tests/tier3_contract/workspaceplan/` already reject SSH URLs; that rejection is part of the v1 contract.

### Pluggable storage backends

§12.2.1 names MemoryStore + SemanticCache as pluggable in v1, but the rest of the stores (SessionStore, etc.) are Postgres-only. The Store interfaces are designed so v2 can swap backends without rewriting the test surface.

### Future scaling interfaces

The PoolScalingController in v1 uses a fixed formula (§4.6.2). v2 may grow custom strategy plugins. Tests should drive the documented inputs (warm count, queue depth, isolation profile) — not the formula constants.

### Cross-environment delegation

§10.6 ships single-environment delegation; cross-environment is documented but deferred. The `tests/tier5_e2e_kind/scaffolds_test.go::TestCrossEnvironmentDelegation` scaffold names the v2 invariant.

### Long-running batch jobs

The current §6 TaskRecord is request/response with checkpoints. Hours-long batch jobs are v2; tests should not assume tasks complete within a single session lifetime.

### Custom policy rules

The §11 policy engine ships a fixed set of evaluators. Pluggable policy is v2.

## Spec-map exceptions

The `tests/spec-map-exceptions.yaml` file lists every section explicitly exempt from the "every leaf section has at least one test" rule. Most exceptions are non-normative sections (executive summary, goals, architecture diagram). The forward-compat sections are marked `reason: forward-compat-deferred-to-v2` so the exception model surfaces them.

## When to update this page

Add an entry when:
- A new spec section is added that explicitly defers behaviour to v2.
- A test surface explicitly tests a v1-only invariant (so v2 contributors know to revisit).
- A `spec-map-exceptions.yaml` entry is added with `reason: forward-compat-deferred-to-v2`.

Remove an entry when v2 ships the feature and the corresponding tests land.

## Cross-references

- TESTING.md §23 — the canonical list
- `tests/spec-map-exceptions.yaml` — machine-readable exemptions
- spec/ — the design documents that motivate each exemption
