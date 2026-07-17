# Proposal: Implement the §8.3 `credentialPropagation: inherit` cross-environment provider-compatibility check (`CREDENTIAL_PROVIDER_MISMATCH`) in the delegation path

- **Status:** Verified (2026-07-17). Converged after 2 adversarial review rounds (0 findings fixed); awaiting sign-off.
- **Date:** 2026-07-17.
- **Scope:** A build of a spec-complete-but-unimplemented feature. `spec/08_recursive-delegation.md` §8.3 fully specifies the cross-environment `credentialPropagation: inherit` provider-compatibility check (`:470`), its multi-hop origin-pool rule (`:472`, `:488`), and its exact rejection message; `spec/15_external-api-surface.md` registers `CREDENTIAL_PROVIDER_MISMATCH` as `POLICY`/`422` (`:1022`) and `pkg/gateway/externalapi/errorclassify/errorclassify.go:391` classifies it, yet no code path emits it. This proposal threads a `credentialPropagation` lease field through `lenny/delegate_task`, adds a closed-enum validator, persists a single origin-session column so contiguous `inherit` hops resolve the same origin pool, and inserts a pre-claim provider-compatibility check that rejects an incompatible cross-environment `inherit` delegation with `CREDENTIAL_PROVIDER_MISMATCH` before any warm pod is claimed. It stages one one-sentence normative spec clarification (the omit-default), the additive code, and the tier-1 and tier-4 tests. It changes no error taxonomy, no wire endpoint, and no finalize-time credential-assignment mechanics. The §8.3 general availability pre-check (`CREDENTIAL_POOL_EXHAUSTED`, `:468`) is a distinct gap and is out of scope.

This document stages the proposed spec, code, and test changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off, spec edit first.

## 1. Problem

The §8.3 cross-environment `credentialPropagation: inherit` provider-compatibility check is fully specified but entirely absent from the delegation path.

### The spec is complete

`spec/08_recursive-delegation.md` defines the propagation modes table `inherit`/`independent`/`deny` (`:440-443`), the per-hop model (`:445-457`), the general availability pre-check emitting `CREDENTIAL_POOL_EXHAUSTED` (`:468`, out of scope here), and the cross-environment compatibility check (`:470`). Before approving a cross-environment `delegate_task` with `inherit`, the gateway intersects the providers represented in the parent's origin credential pool with the child runtime's `supportedProviders`. A non-empty intersection proceeds, assigning a credential whose provider is in the intersection. An empty intersection rejects with `CREDENTIAL_PROVIDER_MISMATCH` before any pod allocation, with the exact message:

```
credentialPropagation: inherit is incompatible with cross-environment delegation: parent credential pool providers do not intersect with child runtime supportedProviders
```

There is no automatic fallback to `independent`, and no warm pod is claimed before rejecting. The multi-hop origin-pool rule (`:472`, `:488`) requires that an `inherit` hop forward the same origin pool, traced through contiguous `inherit` hops back to where `independent` was last used or the root, and that the check always compare the origin pool's providers against the immediate target runtime's `supportedProviders`, re-checked at each environment boundary. `spec/15_external-api-surface.md:1022` registers `CREDENTIAL_PROVIDER_MISMATCH` as category `POLICY`, HTTP 422.

### None of this exists in code

A grep for `credentialPropagation`/`CredentialPropagation` across `pkg/` returns nothing. There is no field on the `delegate_task` `InputSchema` (`pkg/gateway/mcpfabric/mcptools/mcptools_register.go:1861`), the handler input struct (`:1889-1933`), or the delegation `Request` struct (`pkg/gateway/mcpfabric/delegation/service.go:59-170`). No enum or validator mirroring `lease.ApprovalMode`/`lease.ValidateApprovalMode` (`pkg/delegation/lease/lease.go:247-311`) exists. No origin-pool concept exists: a grep for `OriginPool`/`originPool` is empty, and the session row carries no credential-pool field. `PoolRef` is the sandbox warm pool (`sessionstore.go:75-79`), and the lineage columns are `ParentSessionID` (`:143-145`) and `RootSessionID` (`:147-158`). `CREDENTIAL_PROVIDER_MISMATCH` is classified as `{CategoryPolicy, false}` at `errorclassify.go:391` but emitted by no code path.

### The behavior is unpinned

The tier-4 test that would pin the behavior is skipped (`tests/tier4_integration/cross_environment_delegation_test.go:41-62`, `t.Skip` at `:42`), its comment already documenting both the admit and reject journeys.

### The insertion point already exists

The cross-environment signal is computed in the delegate handler (`mcptools_register.go:1997-2009`, `viaCrossEnv`) and is currently consumed only as an audit field (`:2241`). The pod-claiming `deps.Delegation.Delegate` call is at `:2186`, and the service documents admission as running before pod allocation (`service.go:906`). A pre-claim check inserted between `:2009` and `:2186`, using the already-resolved `viaCrossEnv` and `targetRef`, is genuinely pre-claim.

## 2. Decisions

- **No behavioral spec edit is required for the check itself.** `spec/08:470` (intersection, proceed-on-nonempty, reject-on-empty with `CREDENTIAL_PROVIDER_MISMATCH` before pod allocation, the exact message, no fallback, no warm pod claimed) and `spec/08:472`, `:488` (the multi-hop origin-pool rule) are complete, and `spec/15:1022` registers the code as `POLICY`/`422`, matching `errorclassify.go:391`. This is a build of a spec-complete-but-unimplemented feature.
- **Default an omitted `credentialPropagation` to `independent`.** The check fires only on `inherit` (`spec/08:470`), so the omit-default determines whether omitted delegations are subjected to it. The spec is silent on the omit-default. The `independent` at `:267` is a fully-populated lease example, `:490` is an orchestrator-preset note, and the preset YAML at `:494-513` sets no `credentialPropagation` key. `independent` is the safe default: it is consistent with both cited examples, matches the orchestrator preset, and avoids silently routing every omitted cross-environment delegation into the `inherit`-only compatibility gate. A one-sentence normative clarification is staged in SPEC-1 so the default is documented rather than implicit.
- **Resolve "the providers represented in the parent's origin credential pool" operationally.** The implementation persists no per-session single-provider credential-pool binding on the session row, and credentials are assigned at finalize from the tenant `credentialPolicy.providerPools` intersected with the runtime's `supportedProviders` (the §4.9 pattern; `credrouter.Intersection` at `credrouter.go:171` already computes `policy.Providers()` ∩ `supportedProviders`, and `credentialpoolstore.CredentialPool` carries a single `Provider` at `credentialpoolstore.go:91`). The origin pool's provider set is therefore the origin session's eligible provider set. The cross-environment check reduces to `policy.providers` ∩ `originRuntime.supportedProviders` ∩ `childRuntime.supportedProviders` being non-empty.
- **Add the `CredentialPropagation` enum and validator to `pkg/delegation/lease`,** mirroring `lease.ApprovalMode` and `lease.ValidateApprovalMode` (`lease.go:247-311`). Validate the closed enum (`inherit`/`independent`/`deny`, empty meaning the default) at the MCP boundary with `INVALID_LEASE_FIELD` before the parent lookup, exactly as `approvalMode` is validated at `mcptools_register.go:1950-1954`, and repeat the check in the service as defence-in-depth.
- **Thread an origin-pool reference through contiguous `inherit` hops.** Stamp the resolved credential-origin session id onto the child session row when the delegation service creates it. A hop using `independent`, or a root or top-level session, establishes a new origin equal to itself; a hop using `inherit` copies the parent's origin. At a delegate hop the handler reads the delegating parent's stored origin in O(1) and resolves the origin runtime's `supportedProviders`, satisfying `spec/08:472`, `:488`.
- **Insert the pre-claim check in the delegate handler,** between `viaCrossEnv` resolution (`mcptools_register.go:2009`) and the pod-claiming `Delegate` call (`:2186`), gated on `viaCrossEnv && mode == inherit`. On an empty intersection, return `mcp.NewToolError("CREDENTIAL_PROVIDER_MISMATCH", <exact spec message>, ...)` before `Delegate` is called, so no warm pod is claimed. Surfacing the error via `*mcp.ToolError` gives the REST and MCP envelopes the same `POLICY`/`422` classification registered at `errorclassify.go:391`, matching the `TARGET_NOT_IN_SCOPE` / `ISOLATION_MONOTONICITY_VIOLATED` pattern already in the handler.
- **Keep the intersection logic local to the delegation path.** Do not import `pkg/gateway/llmproxy/credrouter` into the delegation or mcptools packages. The intersection is a small set operation; add a provider-set intersection helper next to the lease enum or on `credential.CredentialPolicy` rather than pulling `llmproxy` into the delegation layer.
- **Keep the §8.3 general availability pre-check out of scope.** `CREDENTIAL_POOL_EXHAUSTED` (`spec/08:468`) applies to all `inherit`/`independent` delegations regardless of cross-environment status and is tested by the separately-skipped `delegation_credential_pool_race_test.go`. It shares the pre-claim insertion point but is a distinct gap; conflating the two would over-scope the build.

## 3. Origin-pool resolution

`spec/08:472`, `:488` require an `inherit` hop to forward the same origin pool, traced through contiguous `inherit` hops to the session where `independent` was last used, or the root when the chain reaches the root, and require the compatibility check to compare that origin pool's providers against the immediate target runtime's `supportedProviders` at each boundary.

`RootSessionID` (`sessionstore.go:147-158`) is the delegation-tree apex, but the origin pool is not the root whenever an intermediate hop uses `independent`: `spec/08:488` states that an `independent` hop "establishes a new origin pool for its subtree." `ParentSessionID` plus `RootSessionID` cannot locate the origin across an `independent` break without per-hop mode history. The session row records no credential-origin state today, so a grandchild `inherit` hop cannot resolve the same origin as its parent without new persisted threading.

The build persists exactly one new column, `CredentialOriginSessionID`, computed at child-row creation from the request's mode and the parent's stored origin. For `inherit`, the child copies `parent.CredentialOriginSessionID` (or `parent.ID` when the parent has none). For `independent`, `deny`, or an omitted value, the child uses its own id. The compatibility check reads `parent.CredentialOriginSessionID` in O(1) and re-resolves the origin runtime's `supportedProviders` live at each hop, reflecting `spec/08:472` ("each hop re-checks the still-inherited origin pool at delegation time"). The incoming hop mode is carried on the request (CODE-2) and is never re-read from the row, so no second per-row column is added.

## 4. The compatibility check

The check computes two intersections over the origin session's runtime, the child runtime, and the tenant credential policy:

```
originProviders := IntersectProviders(policy.Providers(), originRuntime.SupportedProviders)
compat          := IntersectProviders(originProviders, childRuntime.SupportedProviders)
```

A non-empty `compat` proceeds to the existing `Delegate` path. An empty `compat` returns `CREDENTIAL_PROVIDER_MISMATCH` before `Delegate` is called, so no warm pod is claimed and no child row is created. The check runs only when `viaCrossEnv && mode == inherit`; a same-environment `inherit` delegation and any `independent`/`deny` delegation fall through unchanged. When the runtime store, tenant store, or session store dependency is unwired, the check is skipped, matching the nil-registry behavior of `crossEnvReachable` (`mcptools.go:1553`).

## 5. Proposed changes

### SPEC-1. State that an omitted `credentialPropagation` defaults to `independent`

**Target:** `spec/08_recursive-delegation.md` §8.3, the `credentialPropagation` modes prose immediately after the modes table (`:440-457`).

**Anchor:** After the `inherit`/`independent`/`deny` table (`:439-443`), before the `**credentialPropagation: inherit multi-hop semantics:**` paragraph (`:445`).

**Rationale:** The one genuine ambiguity the investigation surfaced is that the spec never states the value when the field is omitted from a `delegate_task` call. The `independent` values at `:267` (a fully-populated lease example) and `:490` (an orchestrator-preset note) are configuration-specific rather than a normative omit-default, and the preset YAML at `:494-513` sets no `credentialPropagation` key. Documenting the default keeps code and spec in lockstep and makes explicit that an omitted field does not route into the `inherit`-only compatibility gate.

**Change (staged text):** Insert one sentence after the modes table:

```
When `credentialPropagation` is omitted from a `delegate_task` call, the hop defaults to `independent`: the child receives its own credential lease based on the tenant `credentialPolicy` and the child runtime's `supportedProviders`, and the cross-environment `inherit` compatibility check below does not apply.
```

Change no other spec text. The check, the message, the origin-pool rule, and the error registration are already complete.

### CODE-1. Add the `CredentialPropagation` enum, validator, and provider-set intersection helper

**Target:** `pkg/delegation/lease/lease.go` (adjacent to `ApprovalMode` / `ValidateApprovalMode`, `:247-311`).

**Rationale:** The field needs a closed-enum type and validator so the MCP boundary and the service can reject a malformed value with `INVALID_LEASE_FIELD` before any side effect, exactly as `approvalMode` does. Keeping the provider-set intersection helper next to the enum avoids importing `llmproxy/credrouter` into the delegation path.

**Change (staged description):** Add a `CredentialPropagation string` type with constants `CredentialPropagationInherit = "inherit"`, `CredentialPropagationIndependent = "independent"`, and `CredentialPropagationDeny = "deny"`, each with a doc comment citing `// spec: §8.3`. Add `ValidateCredentialPropagation(m CredentialPropagation) error` that accepts the empty string (the SPEC-1 default) and the three constants, returning a typed `*InvalidCredentialPropagationError` (mirroring `InvalidApprovalModeError`) for any other value. Add `IntersectProviders(a, b []string) []string` returning the order-stable intersection of two provider slices (used by CODE-4), with a doc comment stating it is the `spec/08:470` provider intersection primitive.

### CODE-2. Thread `credentialPropagation` through the `delegate_task` `InputSchema`, handler struct, and delegation `Request`

**Target:** `pkg/gateway/mcpfabric/mcptools/mcptools_register.go` (`InputSchema` `:1861`; handler input struct `:1889-1933`; boundary validation after the `approvalMode` check `:1950-1954`; `Delegate` call `:2186`; audit payload `:2241`) and `pkg/gateway/mcpfabric/delegation/service.go` (`Request` struct `:59-170`).

**Rationale:** The field must exist on every leg of the delegation path before the check can read it. This mirrors the existing `approvalMode` plumbing so the new field carries the same validation and audit treatment.

**Change (staged description):**

1. In the `InputSchema` JSON at `:1861`, add a `credentialPropagation` property alongside `approvalMode`:

```json
"credentialPropagation":{"type":"string","enum":["inherit","independent","deny"],"description":"§8.3 credential propagation mode on the delegation lease. Omit for the default (independent)."}
```

2. Add a field to the handler input struct in the `:1889-1933` region, adjacent to `ApprovalMode`:

```go
// CredentialPropagation is the §8.3 credential propagation mode
// forwarded onto the delegation Request. Empty defaults to
// independent (SPEC-1). On a cross-environment inherit hop the
// handler runs the provider-compatibility check before claiming a
// pod and rejects an empty intersection with
// CREDENTIAL_PROVIDER_MISMATCH.
CredentialPropagation string `json:"credentialPropagation,omitempty"`
```

3. After the `approvalMode` validation at `:1954`, add:

```go
// §8.3: validate the closed enum at the MCP boundary so a malformed
// value is rejected with INVALID_LEASE_FIELD before the parent
// lookup runs. The service repeats the check as defence-in-depth.
if err := lease.ValidateCredentialPropagation(lease.CredentialPropagation(in.CredentialPropagation)); err != nil {
	return mcp.ToolResult{}, mcp.NewToolError("INVALID_LEASE_FIELD",
		err.Error(),
		map[string]any{"field": "credentialPropagation", "value": in.CredentialPropagation})
}
```

4. Add `CredentialPropagation lease.CredentialPropagation` to `delegation.Request` (`service.go:59-170`) with a `// spec: §8.3` doc comment, and pass `lease.CredentialPropagation(in.CredentialPropagation)` at the `:2186` `Delegate` call.

5. Add the declared mode to the `delegation.isolation_violation` audit payload at `:2241` alongside `approval_mode`, for parity.

### CODE-3. Persist and resolve the origin credential pool through contiguous `inherit` hops

**Target:** `pkg/gateway/session/sessionstore/sessionstore.go` (add one session-row field near the lineage fields `:143-158`), `pkg/gateway/mcpfabric/delegation/service.go` (child-row construction, around `:1379+`), and the session-store Postgres migration under `migrations/` that backs the new column, plus its `pgstore`/`memstore` bind and scan wiring.

**Rationale:** `spec/08:472`, `:488` require an `inherit` hop to forward the same origin pool, traced through contiguous `inherit` hops to where `independent` was last used or the root, and re-checked against the immediate target at each boundary. The session row records `RuntimeRef`, `ParentSessionID`, and `RootSessionID` but no credential-origin state, so a grandchild `inherit` hop cannot resolve the same origin as its parent without new persisted threading (see §3).

**Change (staged description):** Persist a single new session column, `CredentialOriginSessionID`, rather than two. Compute it at child-row construction in `delegation/service.go` from the request's mode and the parent's stored origin: for `inherit`, copy `parent.CredentialOriginSessionID` (else `parent.ID`); for `independent`, `deny`, or empty, use the child's own id. The check (CODE-4) reads `parent.CredentialOriginSessionID` in O(1). Do not add a per-row `CredentialPropagation` column: the mode is already carried on the request (CODE-2) and is never re-read from the row, so a persisted mode column would have no consumer, and each lineage column is a real Postgres column added via a numbered migration with INSERT-bind and scan wiring in both `pgstore` and `memstore` (as `delegation_depth` and `delegation_lease` are, `pgstore.go:127-128,192-193,281,986-1108`). One column, one migration delta, and one set of `pgstore`/`memstore` bindings satisfy `spec/08:472`, `:488` identically.

### CODE-4. Add the pre-claim cross-environment `inherit` provider-compatibility check emitting `CREDENTIAL_PROVIDER_MISMATCH`

**Target:** `pkg/gateway/mcpfabric/mcptools/mcptools_register.go` (insert between `viaCrossEnv` resolution `:2009` and the `Delegate` call `:2186`), and optionally the `pkg/gateway/mcpfabric/delegation/service.go` admission path as a defence-in-depth mirror before pod allocation. Reads: `pkg/gateway/runtime/runtimestore` (child and origin runtime `SupportedProviders`), `pkg/gateway/environment/tenantstore` (tenant `CredentialPolicy`, `:152`), and `pkg/gateway/session/sessionstore` (origin session lookup).

**Rationale:** This is the behavior the proposal implements. A cross-environment `inherit` delegation with no shared provider must reject deterministically before any warm pod is claimed, and a compatible one must proceed. The signals are already in scope at the insertion point (`viaCrossEnv`, `targetRef`), and `Delegate` at `:2186` is the pod-claiming gate (`service.go:906` documents admission as running before pod allocation), so inserting the check before `:2186` is genuinely pre-claim.

**Change (staged description):** After `:2009`, add:

```go
if viaCrossEnv && lease.CredentialPropagation(in.CredentialPropagation) == lease.CredentialPropagationInherit {
	// §8.3 cross-environment inherit provider-compatibility check.
	// Resolve the parent's origin session, intersect the tenant
	// policy providers with the origin runtime and the child
	// runtime supportedProviders, and reject an empty intersection
	// before any warm pod is claimed. Skipped when a dependency is
	// unwired, matching crossEnvReachable's nil-registry behavior.
	parent, ok := lookupSession(ctx, deps, tenant, in.ParentSessionId)
	if ok && deps.Runtimes != nil && deps.Tenants != nil {
		originID := parent.CredentialOriginSessionID
		if originID == "" {
			originID = parent.ID
		}
		origin, _ := deps.Store.Get(ctx, tenant, originID)
		originRuntime, _ := deps.Runtimes.Get(ctx, tenant, origin.RuntimeRef)
		childRuntime, _ := deps.Runtimes.Get(ctx, tenant, targetRef)
		policy := deps.Tenants.CredentialPolicy(ctx, tenant)
		originProviders := lease.IntersectProviders(policy.Providers(), originRuntime.SupportedProviders)
		compat := lease.IntersectProviders(originProviders, childRuntime.SupportedProviders)
		if len(compat) == 0 {
			return mcp.ToolResult{}, mcp.NewToolError("CREDENTIAL_PROVIDER_MISMATCH",
				"credentialPropagation: inherit is incompatible with cross-environment delegation: parent credential pool providers do not intersect with child runtime supportedProviders",
				map[string]any{"originRuntime": origin.RuntimeRef, "childRuntime": targetRef})
		}
	}
}
```

The exact `CREDENTIAL_PROVIDER_MISMATCH` message is copied verbatim from `spec/08:470`. On a non-empty intersection, execution falls through to the existing `Delegate` path unchanged. Optionally repeat the same computation inside the service admission path as defence-in-depth for any non-MCP caller. The accessor names above (`lookupSession`, `deps.Runtimes.Get`, `deps.Tenants.CredentialPolicy`, `SupportedProviders`, `Providers`) are indicative and are reconciled to the real store signatures at implementation.

## 6. Non-goals

- **No implementation of the §8.3 general credential-availability pre-check** (`CREDENTIAL_POOL_EXHAUSTED`, `spec/08:468`) that applies to all `inherit`/`independent` delegations regardless of cross-environment status. It is a distinct root gap sharing the same insertion point, tested by the separately-skipped `tests/tier4_integration/delegation_credential_pool_race_test.go`, and is explicitly out of scope.
- **No change to how `independent` or `deny` assign or suppress credentials at finalize.** The build adds the field, its validator, origin threading, and the `inherit` cross-environment compatibility gate. The finalize-time credential-assignment mechanics (the §4.9 policy ∩ runtime intersection already run at session creation) are unchanged. In particular, the concrete same-physical-pool credential sharing for `inherit` beyond the compatibility gate is not built here (see Open decisions).
- **No automatic fallback from `inherit` to `independent`** on an empty intersection. The rejection is deterministic per `spec/08:470`; the delegating session must explicitly pass `credentialPropagation: independent`.
- **No new RPC, endpoint, or wire surface.** `credentialPropagation` is one additive lease field on the existing `lenny/delegate_task` tool and the existing delegation `Request`. `CREDENTIAL_PROVIDER_MISMATCH` is an already-registered error code (`errorclassify.go:391`, `spec/15:1022`).
- **No change to the error taxonomy, category, or HTTP status of `CREDENTIAL_PROVIDER_MISMATCH`.** It remains `POLICY`/`422` as registered.
- **No second persisted session column for the incoming hop mode.** CODE-3 persists only `CredentialOriginSessionID`; the mode rides the request. A per-row `credentialPropagation` column would have no reader and is dropped (see Resolved in adversarial review).
- **No published-docs edit beyond the existing error catalog.** `docs/reference/error-catalog.md` already describes `CREDENTIAL_PROVIDER_MISMATCH`. The only spec change is the one-sentence omit-default clarification in SPEC-1.

## 7. Testing

The change reaches tier 0, tier 1, and tier 4 for `pkg/delegation/lease`, `pkg/gateway/mcpfabric/mcptools`, `pkg/gateway/mcpfabric/delegation`, and `pkg/gateway/session/sessionstore`, per `.claude/rules/test-coverage.md`. Each test below covers a non-happy path (empty, disjoint, boundary, or the spec-named failure) and carries a `// spec: 8.3` annotation; tier-4 tests keep the existing `// diagnosis:` comment.

### TEST-1. Un-skip and implement the tier-4 cross-environment credential-compatibility test (admit + reject + multi-hop)

**Target:** `tests/tier4_integration/cross_environment_delegation_test.go` (remove the `t.Skip` at `:42`).

The skipped test already documents both journeys (`:44-61`). `spec/08:470` mandates the admit path (non-empty intersection proceeds) and the reject path (empty intersection rejects with `CREDENTIAL_PROVIDER_MISMATCH` before any pod or child row), so a happy-path-only test does not satisfy the change.

**Change (staged description):** Build the fixture the comment describes: two §10.6 environments A and B with a `crossEnvironmentDelegation` rule A→B; a tenant `credentialPolicy` with `providerPools` spanning provider set P; a parent runtime admitted in A whose `supportedProviders` make the origin pool providers equal P; and two child runtimes in B, one whose `supportedProviders` intersect P (admit) and one disjoint from P (reject). Run a parent session in A and call `lenny/delegate_task` with `credentialPropagation: inherit` targeting each child.

- **Admit (spec-named happy boundary):** the call succeeds and the child appears in the parent's task tree.
- **Reject (spec-named failure):** the call returns `CREDENTIAL_PROVIDER_MISMATCH` (`POLICY`/`422`), no child session row exists, and no warm pod was claimed, asserted via the session store and warm-pool counters.
- **Multi-hop (spec/08:488 boundary):** add an in-tree grandchild `inherit` hop and assert the origin pool P is re-checked against the grandchild's runtime, not the intermediate parent's.

`// spec: 8.3 (cross-environment inherit provider-compatibility check, CREDENTIAL_PROVIDER_MISMATCH)`.

### TEST-2. Unit-test the validator, intersection helper, origin resolution, and handler branches

**Target:** `pkg/delegation/lease/*_test.go`, `pkg/gateway/mcpfabric/delegation/*_test.go`, `pkg/gateway/mcpfabric/mcptools/*_test.go`.

`test-coverage.md` requires tiers 0 and 1 plus the reached tiers with non-happy paths covered. The determinism of the enum validation, the intersection, and origin resolution lives at the unit level and is pinned independently of the tier-4 flow.

**Change (staged description):**

- **Validator (boundary + error):** table test accepting `""`, `inherit`, `independent`, and `deny`, and rejecting a typo, a casing variant, and a post-v1 value with the typed error.
- **`IntersectProviders` (empty, disjoint, overlap, order-stable):** assert an empty result on disjoint inputs, the shared subset on overlap, and stable ordering.
- **Origin resolution (mixed-mode trees):** three-level trees where an `independent` hop breaks the chain, contiguous `inherit` hops share the same origin, and a top-level session is its own origin.
- **Handler branches:** with fake `Runtimes`/`Tenants`/`Store`, assert (a) `viaCrossEnv && inherit &&` empty intersection returns `CREDENTIAL_PROVIDER_MISMATCH` and never calls `Delegate` (spy on the delegation service), (b) a non-empty intersection calls `Delegate`, (c) `viaCrossEnv == false` with `inherit` skips the check, and (d) cross-environment with `independent` skips the check.

`// spec: 8.3` on each.

## 8. Findings closed on application

This proposal implements the §8.3 cross-environment `credentialPropagation: inherit` provider-compatibility check and un-skips `tests/tier4_integration/cross_environment_delegation_test.go`, resolving the open TEST-GAPS finding recorded in that test's skip note. It leaves the §8.3 general availability pre-check (`CREDENTIAL_POOL_EXHAUSTED`) finding open as a distinct out-of-scope gap.

## 9. Resolved in adversarial review

Subsequent adversarial review rounds populate this section. The challenge-round revision already carried in the draft tightened CODE-3: the underlying need is real and spec-mandated (`spec/08:472`, `:488`), so CODE-3 cannot be dropped, because `RootSessionID` plus `ParentSessionID` cannot locate the origin across an `independent` break without per-hop mode history, and a grep of `pkg/` for `credentialPropagation` is empty, making this greenfield. The revision removed the second persisted session column. The original sketch added both `CredentialPropagation` (the incoming hop mode) and `CredentialOriginSessionID`, but only the origin id is ever read: origin computation at child creation branches on `req.CredentialPropagation` (the request value), and CODE-4's check reads only `parent.CredentialOriginSessionID` or `parent.ID`, so the per-row mode column had no consumer. An unread persisted column is exactly the surface the project principles cut. The staged CODE-3 persists only `CredentialOriginSessionID`, adding one column, one migration delta, and one set of `pgstore`/`memstore` bindings while satisfying `spec/08:472`, `:488` identically.

## 10. Open decisions for review

- **Persisted origin representation.** The build stamps the origin session id and re-resolves the origin runtime's `supportedProviders` live at each hop (recommended, matching `spec/08:472` "each hop re-checks the still-inherited origin pool at delegation time"), rather than snapshotting the origin provider set onto the child row at creation. The two differ only if the origin runtime's `supportedProviders` is edited mid-tree: live re-resolution reflects the edit at the next hop; the snapshot form freezes the set. Confirm the reviewer wants live re-resolution.
- **Origin-pool provider accessor.** The build reads "providers represented in the origin pool" as `Intersection(originRuntime.supportedProviders, tenant credentialPolicy.providerPools)`, mirroring the §4.9 finalize intersection, because the implementation persists no per-session single-provider pool binding. An alternative reading is `originRuntime.supportedProviders` alone, ignoring the tenant policy. The chosen reading is stricter, since a provider must also be in the tenant policy to count as in the pool. Confirm this matches the intended §8.3 semantics.
- **Admit-journey depth.** The tier-4 admit case asserts non-rejection plus child creation. Whether it must additionally assert that the child is assigned a credential whose provider is in the intersection (`spec/08:470`, "the gateway assigns a credential from the parent's pool whose provider appears in that intersection") would require wiring `inherit`'s same-pool credential assignment at finalize. The problem statement scopes this build to the compatibility check; the concrete shared-pool finalize assignment is the boundary and is tracked as follow-up.

## 11. Files touched on application

- `spec/08_recursive-delegation.md`: SPEC-1 (§8.3 omit-default clarification after the modes table, `:440-443`).
- `pkg/delegation/lease/lease.go`: CODE-1 (the `CredentialPropagation` enum, `ValidateCredentialPropagation`, `InvalidCredentialPropagationError`, and `IntersectProviders`, adjacent to `:247-311`).
- `pkg/gateway/mcpfabric/mcptools/mcptools_register.go`: CODE-2 (`InputSchema` `:1861`, handler struct field `:1889-1933`, boundary validation after `:1954`, `Delegate` call `:2186`, audit payload `:2241`) and CODE-4 (the pre-claim check between `:2009` and `:2186`).
- `pkg/gateway/mcpfabric/delegation/service.go`: CODE-2 (`Request` field `:59-170`) and CODE-3 (origin computation at child-row construction `:1379+`); optional CODE-4 defence-in-depth mirror.
- `pkg/gateway/session/sessionstore/sessionstore.go` and its `pgstore`/`memstore` and `migrations/`: CODE-3 (the `CredentialOriginSessionID` column, migration, and bind/scan wiring).
- `tests/tier4_integration/cross_environment_delegation_test.go`: TEST-1 (un-skip and implement the admit, reject, and multi-hop cases, `:41-62`).
- `pkg/delegation/lease/*_test.go`, `pkg/gateway/mcpfabric/delegation/*_test.go`, `pkg/gateway/mcpfabric/mcptools/*_test.go`: TEST-2 (validator, intersection, origin resolution, handler branches).
