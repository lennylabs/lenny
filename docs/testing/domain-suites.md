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

- **Component**: `tests/component/rls/` — direct Postgres queries with `SET LOCAL app.current_tenant`.
- **Security**: `tests/security/scaffolds_test.go::TestTenantIsolationCrossStore` — adversarial across every store, REST, MCP, OpenAI Completions, OpenAI Responses.
- Helpers: `testinfra/fixtures` defines `TenantAcme` / `TenantGlobex` / `TenantInitech`; `testinfra/containers` boots Postgres with the migration set.

## TESTING.md §14.2 Workspace plan and source handling

- **Static**: schema validation of `schemas/workspaceplan-v1.json`.
- **Contract**: `tests/contract/workspaceplan/sources_test.go` — every source type with adversarial paths, modes, gitClone shapes.
- **load_kind**: `tests/load_kind/scenarios/checkpoint_duration/` for archive-bomb scenarios.

## TESTING.md §14.3 Credential leasing lifecycle

- **Unit**: `pkg/credential/` ships every primitive.
- **Contract**: `tests/contract/oauth_token/` covers §13.3 RFC 8693.
- **Integration**: `tests/integration/scaffolds_test.go::TestCredentialLifecycle` (skip-bearing until Phase 12a wires the Token Service).
- **load_kind**: `tests/load_kind/scenarios/credential_rotation_under_load/`.

## TESTING.md §14.4 Delegation and the task tree

- **Unit**: `pkg/delegation/cycle/` + `pkg/delegation/lease/`.
- **Integration**: `TestDelegation`, `TestDelegationRecovery`, `TestDelegationSelfRecursion` (scaffolds today).
- **e2e_kind**: `tests/e2e_kind/scaffolds_test.go::TestCrossEnvironmentDelegation`.
- **load_kind**: `tests/load_kind/scenarios/delegation_fanout/`.

## TESTING.md §14.5 MCP elicitation chain

- **The unit tier**: `pkg/elicitation/` ships the canonicalised SHA-256 digest + tamper detector.
- **The contract tier**: `tests/contract/rest_mcp_consistency/consistency_test.go::TestRESTMCPElicitation`.
- **The integration tier**: `TestMCPElicitationChain`, `TestMCPProvenance` (scaffolds).
- **The security tier**: security scaffolds cover the tamper / floor enforcement cases.

## TESTING.md §14.6 Operability surface

- **Component**: `tests/component/gateway_subsystems/scaffolds_test.go::TestAdminPlane`.
- **The integration tier**: `TestLLMProxyAnthropic`, admin endpoints scaffolds.
- **The e2e_kind tier**: `tests/e2e_kind/scaffolds_test.go::TestLennyOpsFirstDeploy`.

## TESTING.md §14.7 Multi-protocol gateway

- **The contract tier**: every contract surface lives at `tests/contract/rest_*/` and `tests/contract/sdks/`.
- **The contract tier SDK harness**: `tests/contract/sdks/harness/` cross-validates Go / Python / TypeScript clients against the same matrix.

## TESTING.md §14.8 The 13-phase request interceptor chain

- Composed across the component tier (per-interceptor) and the integration tier (composed). Today only the unit-tier enums (`pkg/circuitbreaker`, `pkg/idempotency`, `pkg/elicitation`, etc.) are shipped.

## TESTING.md §14.9 Pool and warm-pod lifecycle

- **The e2e_kind tier**: `TestWarmPool`, `TestPodClaim`, `TestPodLifecycle`, `TestPoolUpgrade` — all skip until Kind cluster bring-up lands.

## TESTING.md §14.10 Compliance and erasure

- **The component tier**: every store's mandatory `DeleteByUser` / `DeleteByTenant` (per TESTING.md §12.2.1).
- **The integration tier**: `TestErasureJobFailureMidSequence` scaffold.
- **The e2e_kind tier**: `TestAdmissionDataResidency`, `TestAdmissionT4NodeIsolation`.

## TESTING.md §14.11 Data residency and T4 controls

- **The e2e_kind tier / the security tier**: residency-validator + T4-node-isolation admission webhooks.
- **Chaos**: `TestKMSUnavailable`, `TestKMSKeyProbeStale` for the T4 KMS probe.

## TESTING.md §14.12 Web playground

- **The e2e_kind tier**: `playground.enabled: true` chart bring-up (deferred until chart lands).
- **The security tier**: CSP / cookie / bearer-revocation scaffolds.

## TESTING.md §14.13 Language SDKs

- **Contract harness**: `tests/contract/sdks/harness/` is the shared driver. Each language SDK ships a `test-helper` binary that the harness drives over stdin/stdout.
- **Per-language scaffolds**: `tests/contract/sdks/{go_client,python_client,typescript_client,runtime_sdk}_test.go`.
- **Reference echo helper**: `tests/testinfra/sdkhelper/echo/` demonstrates the protocol.

## Where to start

When you're writing a new test in any of these domains:

1. Identify the matching TESTING.md §14.N section.
2. Find the tier — most cross-tier suites have a unit / contract anchor that's the easiest place to start.
3. Use the helper packages listed above; never bypass `testinfra/fixtures` for tenant or user identifiers.
4. Add the §17.2 `// spec:` and `// diagnosis:` annotations.
5. Add the test path to `tests/spec-map.json` under the right section.
