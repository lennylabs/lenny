---
layout: default
title: "Domain suites"
parent: "Testing"
nav_order: 3
description: How the §14 cross-tier domain test suites are organised and where to add a new test in each.
---

# Domain test suites

TESTING.md §14 enumerates 13 test suites that span multiple tiers. Each suite combines unit, component, contract, integration, and e2e tests around one domain. This page is the developer onramp: where the tests live and how to add one.

## §14.1 RLS and tenant isolation

The most important security suite. Every store, query path, API surface, and adapter is validated for cross-tenant isolation.

- **Tier 2 component**: `tests/tier2_component/rls/` — direct Postgres queries with `SET LOCAL app.current_tenant`.
- **Tier 9 security**: `tests/tier9_security/scaffolds_test.go::TestTenantIsolationCrossStore` — adversarial across every store, REST, MCP, OpenAI Completions, OpenAI Responses.
- Helpers: `testinfra/fixtures` defines `TenantAcme` / `TenantGlobex` / `TenantInitech`; `testinfra/containers` boots Postgres with the migration set.

## §14.2 Workspace plan and source handling

- **Tier 0 static**: schema validation of `schemas/workspaceplan-v1.json`.
- **Tier 3 contract**: `tests/tier3_contract/workspaceplan/sources_test.go` — every source type with adversarial paths, modes, gitClone shapes.
- **Tier 7 load**: `tests/tier7_load/scenarios/checkpoint_duration/` for archive-bomb scenarios.

## §14.3 Credential leasing lifecycle

- **Tier 1 unit**: `pkg/credential/` ships every primitive.
- **Tier 3 contract**: `tests/tier3_contract/oauth_token/` covers §13.3 RFC 8693.
- **Tier 4 integration**: `tests/tier4_integration/scaffolds_test.go::TestCredentialLifecycle` (skip-bearing until Phase 12a wires the Token Service).
- **Tier 7 load**: `tests/tier7_load/scenarios/credential_rotation_under_load/`.

## §14.4 Delegation and the task tree

- **Tier 1**: `pkg/delegation/cycle/` + `pkg/delegation/lease/`.
- **Tier 4**: `TestDelegation`, `TestDelegationRecovery`, `TestDelegationSelfRecursion` (scaffolds today).
- **Tier 5 e2e**: `tests/tier5_e2e_kind/scaffolds_test.go::TestCrossEnvironmentDelegation`.
- **Tier 7**: `tests/tier7_load/scenarios/delegation_fanout/`.

## §14.5 MCP elicitation chain

- **Tier 1**: `pkg/elicitation/` ships the canonicalised SHA-256 digest + tamper detector.
- **Tier 3**: `tests/tier3_contract/rest_mcp_consistency/scaffolds_test.go::TestRESTMCPElicitation`.
- **Tier 4**: `TestMCPElicitationChain`, `TestMCPProvenance` (scaffolds).
- **Tier 9**: tier-9 security scaffolds cover the tamper / floor enforcement cases.

## §14.6 Operability surface

- **Tier 2 component**: `tests/tier2_component/gateway_subsystems/scaffolds_test.go::TestAdminPlane`.
- **Tier 4**: `TestLLMProxyAnthropic`, admin endpoints scaffolds.
- **Tier 5**: `tests/tier5_e2e_kind/scaffolds_test.go::TestLennyOpsFirstDeploy`.

## §14.7 Multi-protocol gateway

- **Tier 3**: every contract surface lives at `tests/tier3_contract/rest_*/` and `tests/tier3_contract/sdks/`.
- **Tier 3 SDK harness**: `tests/tier3_contract/sdks/harness/` cross-validates Go / Python / TypeScript clients against the same matrix.

## §14.8 The 13-phase request interceptor chain

- Composed across tier-2 (per-interceptor) and tier-4 (composed). Today only the tier-1 enums (`pkg/circuitbreaker`, `pkg/idempotency`, `pkg/elicitation`, etc.) are shipped.

## §14.9 Pool and warm-pod lifecycle

- **Tier 5**: `TestWarmPool`, `TestSandboxClaim`, `TestPodLifecycle`, `TestPoolUpgrade` — all skip until Kind cluster bring-up lands.

## §14.10 Compliance and erasure

- **Tier 2**: every store's mandatory `DeleteByUser` / `DeleteByTenant` (per §12.2.1).
- **Tier 4**: `TestErasureJobFailureMidSequence` scaffold.
- **Tier 5**: `TestAdmissionDataResidency`, `TestAdmissionT4NodeIsolation`.

## §14.11 Data residency and T4 controls

- **Tier 5 / Tier 9**: residency-validator + T4-node-isolation admission webhooks.
- **Tier 8 chaos**: `TestKMSUnavailable`, `TestKMSKeyProbeStale` for the T4 KMS probe.

## §14.12 Web playground

- **Tier 5**: `playground.enabled: true` chart bring-up (deferred until chart lands).
- **Tier 9**: CSP / cookie / bearer-revocation scaffolds.

## §14.13 Language SDKs

- **Tier 3 contract harness**: `tests/tier3_contract/sdks/harness/` is the shared driver. Each language SDK ships a `test-helper` binary that the harness drives over stdin/stdout.
- **Per-language scaffolds**: `tests/tier3_contract/sdks/{go_client,python_client,typescript_client,runtime_sdk}_test.go`.
- **Reference echo helper**: `tests/testinfra/sdkhelper/echo/` demonstrates the protocol.

## Where to start

When you're writing a new test in any of these domains:

1. Identify the matching TESTING.md §14.N section.
2. Find the tier — most cross-tier suites have a unit / contract anchor that's the easiest place to start.
3. Use the helper packages listed above; never bypass `testinfra/fixtures` for tenant or user identifiers.
4. Add the §17.2 `// spec:` and `// diagnosis:` annotations.
5. Add the test path to `tests/spec-map.json` under the right section.
