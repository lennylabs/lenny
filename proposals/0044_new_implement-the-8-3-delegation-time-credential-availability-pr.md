# Proposal: Implement the §8.3 delegation-time credential-availability pre-check (`CREDENTIAL_POOL_EXHAUSTED`) in the `delegate_task` path

- **Status:** Approved (2026-07-18). Converged after 4 adversarial review rounds (4 findings fixed); both §10 open decisions resolved at sign-off. Sequencing: land the gate now (the recommended option, matching 0043's sibling cross-environment gate on the same insertion point with the same guards-a-not-yet-built-downstream property). Racy tier-4 test: remove the racy variant and re-file its one-winner/N-1 and pod-release coverage against the future §8.2 delegate-path pod-claim cluster (TEST-2, the recommended option).
- **Date:** 2026-07-17.
- **Scope:** A build of a spec-complete-but-unimplemented behavior. `spec/08_recursive-delegation.md:470` mandates that a `delegate_task` call with `credentialPropagation: inherit` or `independent` runs the §4.9 pre-claim credential-availability check before claiming a warm pod and rejects with `CREDENTIAL_POOL_EXHAUSTED` when no credential is available. That gate is absent from the delegation path. `CREDENTIAL_POOL_EXHAUSTED` is already registered `POLICY`/`503` (`spec/15_external-api-surface.md:992`) and classified `{CategoryPolicy, true}` (`pkg/gateway/externalapi/errorclassify/errorclassify.go:186`), so no error taxonomy or wire surface changes. This proposal adds one consumer-side interface on the already-injected `*sessionserver.Server` so the delegate handler can reach the existing §4.9 engine, inserts the availability gate immediately after 0043's cross-environment `inherit` provider-compatibility gate, and re-scopes the skipped tier-4 test to a deterministic pre-check assertion. It changes no spec text. The post-pod-claim assignment race, pod release, and one-winner/N-1 clauses of `spec/08:470` are coupled to a delegate-path pod-claim step that does not exist and are out of scope.

This document stages the proposed code and test changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

The §8.3 delegation-time credential-availability pre-check is fully specified but entirely absent from the delegation path.

### The spec is complete

`spec/08_recursive-delegation.md:470` requires that when the gateway processes a `delegate_task` call with `credentialPropagation: inherit` or `independent`, it runs the same pre-claim credential-availability check as §4.9 before claiming a warm pod, and rejects with `CREDENTIAL_POOL_EXHAUSTED` before pod allocation so no warm pod is wasted. The check is mode-specific. For `inherit` the gateway verifies the origin credential pool has at least one assignable slot (`active leases < maxConcurrentSessions` for at least one credential in the pool). For `independent` the gateway checks the intersection of the child runtime's `supportedProviders` and the tenant's `credentialPolicy.providerPools`. The spec states this is a point-in-time check rather than a reservation, and its second and third clauses describe a post-pod-claim assignment race in which the loser's pod is released back to the warm pool and one delegation of N wins.

`CREDENTIAL_POOL_EXHAUSTED` is registered `POLICY`/`503` at `spec/15_external-api-surface.md:992` and classified `{CategoryPolicy, true}` at `pkg/gateway/externalapi/errorclassify/errorclassify.go:186`.

### None of this exists on the delegation path

The only credential gate in the delegate handler is 0043's cross-environment `inherit` provider-compatibility check (`crossEnvInheritMismatch`, `pkg/gateway/mcpfabric/mcptools/mcptools.go:1613`, wired at `pkg/gateway/mcpfabric/mcptools/mcptools_register.go:2046`). That check fires only on `viaCrossEnv && inherit`, computes a provider-set intersection, and returns `CREDENTIAL_PROVIDER_MISMATCH` (`POLICY`/`422`). It is a narrower sibling that never reads credential availability and never returns `CREDENTIAL_POOL_EXHAUSTED`. A grep for `PreClaim`, `credrouter`, or `CREDENTIAL_POOL_EXHAUSTED` across `pkg/gateway/mcpfabric` returns nothing on the delegation path. The tier-4 race test that would pin the behavior is skipped (`tests/tier4_integration/delegation_credential_pool_race_test.go:54`).

### The §4.9 engine is unreachable from the delegate handler

`credrouter.PreClaim` and its inputs (`s.credRouter`, `s.credPools`, `s.userCredChecker`) plus the `PreClaimInput` builder live only on `*sessionserver.Server` through the unexported `resolveCredentialPools` (`pkg/gateway/sessionserver/start.go:1295-1388`). Neither `mcptools.Deps` nor `delegation.Service` carries any credential router, pool store, or lease store. The concrete `*sessionserver.Server` that owns them is already injected into `mcptools.Deps` as `SessionCreator` and `SessionService` (`pkg/gateway/mcpfabric/mcptools/mcptools.go:294-320`, wired at `cmd/lenny-gateway/mcpsurface.go:230,234`), so the engine can be reached through a new consumer-side interface on that same object without threading raw credential dependencies into the delegation packages.

### The delegate path claims no pod and assigns no lease

`Service.Delegate` (`pkg/gateway/mcpfabric/delegation/service.go:797`) runs validation, the cycle and depth gates, and `insertChildSession`, which ends at `s.store.Create(ctx, child)` with `State: session.StateCreated` (`service.go:1199,1425`) and no `PodAssignment`. The §8.2 delegation flow allocates the child pod at step 5 and assigns the credential lease at pod claim, but that pod-claim, assignment, and release machinery does not exist on the delegate path. It exists only on the session create-to-finalize path (`pkg/gateway/sessionserver/finalize.go:246`, `pkg/gateway/sessionserver/start.go:150-165`). Consequently the `spec/08:470` pre-check is a point-in-time availability read the delegate path can and must add before admitting a delegation, while the second and third clauses of `spec/08:470` (the post-pod-claim assignment race, the pod release, and the one-winner/N-1 outcome) depend on a delegate-path pod-claim step that is not implemented and are therefore not buildable as part of this pre-check.

## 2. Decisions

- **No spec edit is needed.** `spec/08:470` fully specifies the delegation-time pre-check with mode-specific semantics. `CREDENTIAL_POOL_EXHAUSTED` is registered `POLICY`/`503` (`spec/15:992`) and classified (`errorclassify.go:186`). This is a code build of a spec-complete-but-unimplemented behavior. `kind=new` because it adds a capability the implementation lacks, matching the sibling build 0043.
- **Reuse the §4.9 engine in place through a new consumer-side interface on the already-injected `*sessionserver.Server`.** The alternatives are to extract the unexported `PreClaimInput` builder into a new shared package (a refactor with its own cost) or to wire raw credential dependencies into `mcptools.Deps` and duplicate the builder (which duplicates §4.9 semantics and violates 0043's landed decision to keep `pkg/gateway/llmproxy/credrouter` out of the delegation and mcptools packages). The chosen approach adds one interface method, keeps `resolveCredentialPools` as the single source of truth for the §4.9 and §8.3 pre-claim semantics, and keeps `credrouter` behind the interface so the no-`llmproxy`-import boundary holds.
- **Route both modes through the one engine via a synthetic prospective-child row.** The `Server` method builds a `sessionstore.Session` carrying the would-be child's `TenantID`, `UserID`, `RuntimeRef` (the child runtime), and `CredentialOriginSessionID` (the parent's origin for `inherit`, resolved by the same rule the delegation service uses at `service.go` child-row construction; empty for `independent`). It then calls `resolveCredentialPools` and maps the engine's two typed pre-claim outcomes, `credrouter.ErrNoCredentialAvailable` to the exhaustion failure and `credrouter.ErrUserCredentialNotFound` to the user-credential-missing failure, exactly as session start does. For `independent` this yields the child-`supportedProviders` ∩ tenant-`providerPools` intersection plus the assignable-slot check. For `inherit` it yields the origin-pool-constrained intersection (the constraint 0043 landed at `start.go:1333-1343`) plus the assignable-slot check. One mechanism serves both modes with the exact §4.9 semantics and no builder extraction.
- **Place the gate in the delegate handler before `deps.Delegation.Delegate`, immediately after 0043's `crossEnvInheritMismatch` block (`mcptools_register.go:2050`).** For a cross-environment `inherit` hop the more specific `CREDENTIAL_PROVIDER_MISMATCH` (0043) is evaluated first, then the availability gate. Same-environment `inherit` and every `independent` hop reach only the availability gate. `deny` mode skips the check, since `spec/08:470` triggers only on `inherit` and `independent`. A nil checker skips, mirroring `crossEnvInheritMismatch`'s nil-registry fall-through (`mcptools.go:1614`).
- **The check is point-in-time and claims no pod and reserves no lease.** This is faithful both to `spec/08:470`, which states the check is a point-in-time read rather than a reservation, and to the current delegate architecture, which claims no pod on this path. It rejects before the child row is created (before `deps.Delegation.Delegate` commits it at `service.go:1199`), the earliest and only pod-preceding gate the delegate path has.
- **The exact `inherit` assignable-slot semantics are bounded by the deferred lease-utilization reader.** `poolDescriptor` reports `HasCapacity: true` whenever the pool holds any non-revoked credential (`start.go`), a refinement shared with session-start §4.9 and deferred until a lease-utilization reader is wired. Until it lands, the `inherit` availability check rejects only when the origin pool has no usable credential at all, and the at-capacity case is not enforced. This build wires the check to the shared descriptor and inherits the refinement automatically when the reader is built. This build does not build the reader.
- **The `spec/08:470` post-pod-claim assignment-race, pod-release, and one-winner/N-1 behavior is out of scope,** because the delegate path performs no pod claim or lease assignment (`service.go:1199-1221`). Those clauses belong to a distinct delegate-path pod-claim and assignment flow (spec §8.2 steps 5-9) that this proposal does not build. The skipped racy tier-4 test therefore cannot pass as written and is re-scoped to a deterministic pre-check assertion (TEST-2).

## 3. Routing both modes through one engine

The gate does not re-derive intersection or assignable-slot logic. It constructs a synthetic prospective-child `sessionstore.Session` and hands it to the existing `resolveCredentialPools`, which already runs the §4.9 pre-claim engine and already carries 0043's `inherit` origin-pool constraint at `start.go:1333-1343`.

The synthetic row carries four fields:

```
TenantID                  = the delegating caller's tenant
UserID                    = the child's effective credential user (the parent session's user)
RuntimeRef                = the resolved child runtime (targetRef)
CredentialOriginSessionID = for inherit: the parent's stored origin (parent.CredentialOriginSessionID, else parent.ID)
                            for independent: empty
```

For `independent` the empty `CredentialOriginSessionID` leaves `resolveCredentialPools` on its unconstrained path, so the eligible set is `childRuntime.supportedProviders` ∩ tenant `credentialPolicy.providerPools`. For `inherit` the non-empty ancestor `CredentialOriginSessionID` triggers the origin-constrained branch (`row.CredentialOriginSessionID != "" && row.CredentialOriginSessionID != row.ID` at `start.go:1333`), so the eligible set is additionally intersected with the origin runtime's eligible set, live-resolved at this hop. `resolveCredentialPools` then runs `credrouter.PreClaim` over that eligible set, which returns one of two typed pre-claim outcomes on failure: `credrouter.ErrNoCredentialAvailable` when no provider has an assignable credential, and `credrouter.ErrUserCredentialNotFound` when every eligible provider is user-only and the user has no registered credential (`preclaim.go:107-110`). The gate maps the first to `CREDENTIAL_POOL_EXHAUSTED` and the second to `USER_CREDENTIAL_NOT_FOUND`, the same two codes session start draws from the identical engine (`start.go:148,154`); any other error propagates unchanged; a nil error admits the delegation.

## 4. Why clauses 2 and 3 are not buildable here

`spec/08:470`'s second and third clauses describe behavior after a warm pod is claimed: a concurrent assignment race in which the losing delegation's pod is released back to the warm pool and exactly one of N racing delegations wins. The delegate path claims no warm pod. `Service.Delegate` commits the child in `session.StateCreated` with no `PodAssignment` (`service.go:1199,1425`) and invokes no warm-pool claim, no credential assignment, and no §4.9 engine. The pod-claim and lease-assignment step that these clauses observe exists only on the session create-to-finalize path (`finalize.go:246`), which delegated children do not traverse today. The pre-check this proposal adds is the point-in-time read that precedes that step; the race, the release, and the one-winner outcome belong to the delegate-path pod-claim flow (spec §8.2 steps 5-9), which is a separate, unbuilt concern (§6, Non-goals).

## 5. Proposed changes

### CODE-1. Add a credential-availability checker interface and implement it on `*sessionserver.Server`

**Target:** `pkg/gateway/mcpfabric/mcptools/mcptools.go` (the `CredentialAvailabilityChecker` interface and the `CredAvailability` `Deps` field, adjacent to `SessionCreator`/`SessionService`, `:294-320`), and `pkg/gateway/sessionserver/start.go` (the `DelegationCredentialQuery` struct, the `ErrDelegationCredentialUnavailable` sentinel, and a new exported method on `*Server` next to `resolveCredentialPools`, `:1295`). Wiring: `cmd/lenny-gateway/mcpsurface.go` (`:230,234`, where `sessionSrv` is already assigned to `SessionCreator`/`SessionService`).

**Rationale:** The §4.9 engine (`resolveCredentialPools` → `credrouter.PreClaim`) is the single source of truth for pre-claim availability and already carries 0043's `inherit` origin constraint. The delegate handler sits outside `*sessionserver.Server` and cannot call the unexported method. A small consumer-side interface on the already-injected server reaches the engine without extracting the builder into a shared package and without importing `credrouter` into the delegation or mcptools packages (Decisions §2).

**Change (staged description):**

1. In `pkg/gateway/mcpfabric/mcptools/mcptools.go`, add the consumer-side interface and a `Deps` field. The interface references the query struct and the exhaustion sentinel defined in the `sessionserver` package (step 2), following the existing `SessionCreator` precedent (`mcptools.go:294`), whose method references `sessionserver.CreateSessionRequest`/`CreateSessionResponse` so that `*sessionserver.Server` satisfies it without `sessionserver` importing `mcptools`. `DelegationCredentialQuery` carries only what the engine needs; the mode is pre-resolved by the handler into `CredentialOriginSessionID` (set for `inherit`, empty for `independent`), so the interface stays mode-agnostic:

```go
// CredentialAvailabilityChecker runs the §8.3 delegation-time pre-claim
// credential-availability check (the same §4.9 engine session start
// runs) against a prospective delegated child. It returns nil when a
// credential is assignable, sessionserver.ErrDelegationCredentialUnavailable
// when the pool is exhausted, sessionserver.ErrDelegationUserCredentialNotFound
// when a user-only policy has no registered credential (the same
// distinction session start draws), and any other error unchanged.
// *sessionserver.Server implements it. A nil checker skips the gate.
// spec: §8.3 line 470.
type CredentialAvailabilityChecker interface {
	CheckDelegationCredentialAvailability(ctx context.Context, q sessionserver.DelegationCredentialQuery) error
}
```

Add `CredAvailability CredentialAvailabilityChecker` to `Deps` with a doc comment noting it is optional and that a nil value skips the gate (the minimal in-process gateway and the unit suite leave it nil).

2. In `pkg/gateway/sessionserver/start.go`, define the query struct and the exhaustion sentinel in the `sessionserver` package, then implement the method on `*Server` next to `resolveCredentialPools`. The struct and sentinel live in `sessionserver` (the engine's package), so the `*Server` method signature and body reference only `sessionserver`-local types and `sessionserver` does not import `mcptools`. The method builds the synthetic row (§3), calls the existing engine, and maps the router's typed exhaustion error:

```go
// DelegationCredentialQuery describes the prospective delegated child
// whose credential availability the §8.3 pre-check evaluates before the
// delegation is admitted. CredentialOriginSessionID is the parent's
// resolved origin for an inherit hop and empty for an independent hop,
// so the checker constrains the eligible provider set to the origin pool
// exactly as a finalized inherit child would be. spec: §8.3.
type DelegationCredentialQuery struct {
	TenantID                  string
	UserID                    string
	ChildRuntimeRef           string
	CredentialOriginSessionID string
}

// ErrDelegationCredentialUnavailable is the typed exhaustion result the
// §8.3 pre-check returns when no credential is assignable for the
// prospective delegated child. The delegate handler branches on it with
// errors.Is rather than a string match. spec: §8.3 line 470.
var ErrDelegationCredentialUnavailable = errors.New("delegation credential unavailable")

// ErrDelegationUserCredentialNotFound is the typed result the §8.3
// pre-check returns when the tenant credentialPolicy is user-only for
// every provider in the eligible set and no pre-registered user
// credential exists. It mirrors the distinct outcome session start draws
// for the identical §4.9 engine error (credrouter.ErrUserCredentialNotFound
// at start.go:148), so the delegate handler can surface the same
// USER_CREDENTIAL_NOT_FOUND code rather than an opaque internal error.
// spec: §8.3 line 470; §4.9 line 1364.
var ErrDelegationUserCredentialNotFound = errors.New("delegation user credential not found")

// CheckDelegationCredentialAvailability runs the §8.3 delegation-time
// pre-claim credential-availability check for a prospective delegated
// child. It reuses resolveCredentialPools (the §4.9 engine, including the
// inherit origin-pool constraint) against a synthetic child row and maps
// the engine's two typed pre-claim outcomes to sentinels: an exhausted
// pool (credrouter.ErrNoCredentialAvailable) to
// ErrDelegationCredentialUnavailable, and a user-only policy with no
// registered credential (credrouter.ErrUserCredentialNotFound) to
// ErrDelegationUserCredentialNotFound. It claims no pod and reserves no
// lease: this is the point-in-time read spec/08:470 requires before pod
// allocation. spec: §8.3 line 470; §4.9.
func (s *Server) CheckDelegationCredentialAvailability(ctx context.Context, q DelegationCredentialQuery) error {
	row := sessionstore.Session{
		TenantID:                  q.TenantID,
		UserID:                    q.UserID,
		RuntimeRef:                q.ChildRuntimeRef,
		CredentialOriginSessionID: q.CredentialOriginSessionID,
	}
	if _, _, _, err := s.resolveCredentialPools(ctx, row); err != nil {
		switch {
		case errors.Is(err, credrouter.ErrNoCredentialAvailable):
			return ErrDelegationCredentialUnavailable
		case errors.Is(err, credrouter.ErrUserCredentialNotFound):
			return ErrDelegationUserCredentialNotFound
		default:
			return err
		}
	}
	return nil
}
```

The synthetic row leaves `ID` empty, so the `CredentialOriginSessionID != "" && != row.ID` guard at `start.go:1333` selects the origin-constrained branch whenever the handler set an origin (`inherit`) and the unconstrained branch when it did not (`independent`). The import direction runs `mcptools` → `sessionserver`: `mcptools` already imports `sessionserver` (`mcptools.go:77`, `mcptools_register.go:38`, `client_tools.go:14`) and `sessionserver` imports no `mcptools` file (a `grep` for `mcpfabric/mcptools` under `pkg/gateway/sessionserver` returns nothing). Placing `DelegationCredentialQuery` and `ErrDelegationCredentialUnavailable` in `sessionserver` keeps the `*Server` method free of any `mcptools` reference, so no cycle forms, exactly as `CreateSessionRequest`/`CreateSessionResponse` live in `sessionserver` and let `*sessionserver.Server` satisfy the `mcptools.SessionCreator` interface without importing `mcptools`.

3. In `cmd/lenny-gateway/mcpsurface.go`, set `CredAvailability: sessionSrv` in the same `Deps` literal that already assigns `SessionCreator: sessionSrv` and `SessionService: sessionSrv` (`:230,234`).

### CODE-2. Add the pre-claim availability gate in the delegate handler

**Target:** `pkg/gateway/mcpfabric/mcptools/mcptools_register.go`, inserted immediately after 0043's `crossEnvInheritMismatch` block (`:2046-2050`) and before the pod-claiming `deps.Delegation.Delegate` call (`:2217`).

**Rationale:** This is the behavior the proposal implements. A `delegate_task` with `inherit` or `independent` whose pool is exhausted must reject with `CREDENTIAL_POOL_EXHAUSTED` before the child row is created, and an available one must proceed. `deps.Delegation.Delegate` at `:2217` is the earliest commit gate on the delegate path (`service.go:1199`), so inserting the check before it is genuinely pre-admission. The already-resolved `viaCrossEnv`, `targetRef`, and `in.CredentialPropagation` are in scope at the insertion point.

**Change (staged description):** After the `crossEnvInheritMismatch` block at `:2050`, add:

```go
// §8.3 line 470: run the pre-claim credential-availability check for an
// inherit or independent delegation before the child row is created. A
// deny hop needs no credential and is skipped; a nil checker skips
// (the minimal in-process gateway wires none). For inherit, resolve the
// parent's origin so the check constrains to the origin pool exactly as
// finalize would; for independent, leave the origin empty so the check
// evaluates the child runtime supportedProviders ∩ tenant providerPools.
// The check claims no pod: it rejects before Delegate commits the child.
mode := lease.CredentialPropagation(in.CredentialPropagation)
if deps.CredAvailability != nil &&
	(mode == lease.CredentialPropagationInherit || mode == lease.CredentialPropagationIndependent ||
		mode == "" /* omitted defaults to independent per §8.3 */) {
	originID := ""
	if mode == lease.CredentialPropagationInherit && deps.Store != nil {
		parent, err := deps.Store.Get(ctx, tenant, in.ParentSessionID)
		if err != nil {
			// Fail closed: an inherit hop whose parent origin cannot be
			// resolved must not silently downgrade to an unconstrained
			// (independent-equivalent) availability check. Credential
			// handling denies on doubt (code-best-practices), so the
			// lookup error propagates rather than admitting the hop with
			// an empty origin. spec: §8.3 line 470.
			return mcp.ToolResult{}, err
		}
		originID = parent.CredentialOriginSessionID
		if originID == "" {
			originID = parent.ID
		}
	}
	q := sessionserver.DelegationCredentialQuery{
		TenantID:                  tenant,
		UserID:                    callerUserID, // the parent session's effective user
		ChildRuntimeRef:           targetRef,
		CredentialOriginSessionID: originID,
	}
	if err := deps.CredAvailability.CheckDelegationCredentialAvailability(ctx, q); err != nil {
		switch {
		case errors.Is(err, sessionserver.ErrDelegationCredentialUnavailable):
			// spec: §15.2.1 — surface the canonical lenny code via
			// *mcp.ToolError so the REST and MCP envelopes share the
			// same POLICY / 503 (category, retryable) pair registered at
			// errorclassify.go.
			return mcp.ToolResult{}, mcp.NewToolError("CREDENTIAL_POOL_EXHAUSTED",
				"credential pool exhausted: no assignable credential for the delegated child at delegation time",
				map[string]any{"childRuntime": targetRef, "credentialPropagation": string(mode)})
		case errors.Is(err, sessionserver.ErrDelegationUserCredentialNotFound):
			// spec: §15.2.1, §4.9 line 1364 — the same user-only-policy
			// condition session start surfaces as USER_CREDENTIAL_NOT_FOUND
			// (PERMANENT / 404, registered at errorclassify.go:191). Return
			// the classified *mcp.ToolError so the delegate path emits the
			// same actionable code rather than the INTERNAL_ERROR fallback a
			// bare error would produce at mcptools_register.go:2306.
			return mcp.ToolResult{}, mcp.NewToolError("USER_CREDENTIAL_NOT_FOUND",
				"no pre-registered credential found for the delegated child's user and provider; "+
					"register one via POST /v1/credentials or configure pool fallback",
				map[string]any{"childRuntime": targetRef, "credentialPropagation": string(mode)})
		default:
			return mcp.ToolResult{}, err
		}
	}
}
```

The `callerUserID` accessor is indicative and is reconciled to however the handler already resolves the parent session's owning user (the same value the §11.4 revocation gate reads). On a nil error, execution falls through to the existing `Delegate` path unchanged. The gate reuses `deps.Store`, already present on `Deps`, only to resolve the `inherit` origin id. The `inherit` origin lookup fails closed: when `deps.Store.Get` returns an error the handler propagates it rather than admitting the hop with an empty origin, because an empty origin would run the unconstrained (independent-equivalent) check and downgrade the `inherit` hop. This follows the credential-handling deny-on-doubt principle in `code-best-practices.md`.

## 6. Non-goals

- **No implementation of the delegate-path pod-claim and credential-lease-assignment flow (spec §8.2 steps 5-9).** The `delegate_task` path commits the child in `session.StateCreated` with no `PodAssignment` (`service.go:1199-1221,1425`). This build adds only the pre-admission availability gate and does not build pod allocation, credential assignment, or a virtual MCP child interface for delegated children.
- **No lease-utilization reader.** The exact `inherit` `active leases < maxConcurrentSessions` assignable-slot refinement is deferred behind the `HasCapacity` stub in `poolDescriptor`, a gap shared with session-start §4.9. Until it lands, the `inherit` gate rejects only a fully unusable origin pool. This build wires the check to the shared descriptor and inherits the refinement when the reader is built.
- **No post-pod-claim assignment-race, pod-release, or one-winner/N-1 behavior (`spec/08:470` clauses 2-3).** Those are coupled to a delegate-path pod-claim step that does not exist. The racy tier-4 variant is removed and re-filed against the future §8.2 delegate-path pod-claim build (TEST-2).
- **No new error code, RPC, endpoint, or wire field.** `CREDENTIAL_POOL_EXHAUSTED` is already registered (`spec/15:992`) and classified (`errorclassify.go:186`). The gate is one additive check on the existing `lenny/delegate_task` path.
- **No spec edit.** `spec/08:470` is complete for the pre-check. The only genuine ambiguity in the sibling area, the omitted-`credentialPropagation` default, was resolved by 0043 (`spec/08:445`).
- **No change to 0043's landed behavior.** The cross-environment `inherit` `CREDENTIAL_PROVIDER_MISMATCH` gate, the origin-pool constraint at finalize, and the `credentialPropagation` enum and validator are reused without modification.
- **No `deny`-mode credential suppression.** That is a distinct §8.3 follow-up.
- **Considered and not taken: defer the entire gate until the delegate-path pod-claim flow exists, then fold the availability read into it as a direct `resolveCredentialPools` call on the `Server` rather than adding an interface now.** The argument for deferral is that `resolveCredentialPools` already runs the §4.9 availability engine and already rejects `CREDENTIAL_POOL_EXHAUSTED` at the point a pod is actually claimed on the session-start path (`start.go:1697,1871,2036`), that the delegate path claims no pod so the pre-check's stated pod-waste-avoidance benefit is currently null, and that when the §8.2 delegate-path pod-claim flow is built the availability read can be folded into it with no new interface, `Deps` field, query struct, or sentinel. This proposal does not take that route. The pre-check is a self-contained, spec-named behavior with its own registered error code and its own deterministic test, and 0043 already landed the sibling cross-environment gate on the same insertion point with the same property of guarding a not-yet-built downstream. Landing the availability gate now keeps the two `spec/08:470` gates together at one insertion point and lets the deterministic pre-check coverage exist before the racy flow is buildable. The sequencing question is recorded as an open decision (§10) for sign-off to confirm.

## 7. Testing

The change reaches tier 0, tier 1, and tier 4 for `pkg/gateway/sessionserver`, `pkg/gateway/mcpfabric/mcptools`, and `tests/tier4_integration`, per `.claude/rules/test-coverage.md`. Each test below covers a non-happy path (empty intersection, exhausted pool, the mode-skip boundary, or the spec-named failure) and carries a `// spec: 8.3` annotation; the tier-4 test keeps its `// diagnosis:` comment.

### TEST-1. Unit-test the `Server` checker method's new mapping and the handler gate branches

**Target:** `pkg/gateway/sessionserver/start_preclaim_internal_test.go` (the `CheckDelegationCredentialAvailability` field mapping and error propagation) and `pkg/gateway/mcpfabric/mcptools/*_test.go` (the delegate-handler gate branches with a fake `CredentialAvailabilityChecker` and a spy delegation service).

**Rationale:** `test-coverage.md` requires tiers 0 and 1 plus the non-happy paths. The handler-gate branches introduced by CODE-2 are new behavior with no existing coverage. The `Server` method is a thin wrapper over `resolveCredentialPools`, whose intersection, inherit-to-origin, fail-closed-on-missing-origin, and nil-registries semantics are already pinned exhaustively through the identical code path by `start_preclaim_internal_test.go` (`TestResolveCredentialPoolsIntersectionNarrows` at `:114`, and the `InheritNarrowsToOrigin`, `InheritFailsClosedOnMissingOrigin`, `InheritDisjointDenies`, `Exhausted`, `NoRegistries`, and `UnconfiguredPolicy` cases). Re-running that full semantics matrix through the wrapper would duplicate existing coverage, so the wrapper test pins only what the wrapper newly adds.

**Change (staged description):**

- **`Server` method mapping (tier 1, single test):** assert that `CheckDelegationCredentialAvailability` maps `DelegationCredentialQuery` fields into the synthetic row (`ChildRuntimeRef` → `RuntimeRef`, and `CredentialOriginSessionID`, `TenantID`, `UserID` verbatim) and propagates `resolveCredentialPools`'s result, returning `ErrDelegationCredentialUnavailable` when the engine returns `credrouter.ErrNoCredentialAvailable`, `ErrDelegationUserCredentialNotFound` when the engine returns `credrouter.ErrUserCredentialNotFound` (a user-only policy with no registered credential), and nil when it succeeds. Do not re-cover intersection narrowing, inherit-to-origin, fail-closed-on-missing-origin, or nil-registries semantics; those are already pinned through the identical code path.
- **Handler gate branches (tier 1), with a fake `CredentialAvailabilityChecker` and a spy on `deps.Delegation.Delegate`:** assert (a) an `inherit` or `independent` exhaustion returns `CREDENTIAL_POOL_EXHAUSTED` and never calls `Delegate`; (b) an available delegation proceeds to `Delegate`; (c) a `deny` hop skips the check and calls `Delegate`; (d) a nil checker skips the check and calls `Delegate`; (e) a cross-environment `inherit` case still evaluates `CREDENTIAL_PROVIDER_MISMATCH` (0043) before the availability gate; and (f) an omitted `credentialPropagation` (empty `in.CredentialPropagation`) runs the check as `independent`: an exhausted pool returns `CREDENTIAL_POOL_EXHAUSTED` and never calls `Delegate`, and an available pool proceeds to `Delegate`. Case (f) pins the `mode == ""` clause of the gate condition (CODE-2, the omitted default that `spec/08:445` and `lease.go:339` resolve to `independent`); a regression dropping that clause would silently disable the pre-check for the common default-mode delegation while cases (a)-(e) still pass. Case (g): a user-only tenant `credentialPolicy` with no registered user credential (the fake checker returns `ErrDelegationUserCredentialNotFound`) returns the classified `USER_CREDENTIAL_NOT_FOUND` tool error (`PERMANENT`/`404`, `errorclassify.go:191`) and never calls `Delegate`. Case (g) pins the second engine outcome the delegate path must classify the same way session start does; without the mapping the same reachable policy condition would fall through to the generic `INTERNAL_ERROR` the delegate dispatch returns for a bare error (`mcptools_register.go:2306`).
- **Query-contents assertions (tier 1), capturing the `sessionserver.DelegationCredentialQuery` the fake checker receives:** assert that an `inherit` hop passes the parent's resolved origin (`parent.CredentialOriginSessionID`, or `parent.ID` when that field is empty), and that an `independent` hop and an omitted-default hop both pass an empty `CredentialOriginSessionID`. Add a case for the parent-lookup-error path (`deps.Store.Get` returns an error) asserting the handler fails closed: it returns the lookup error and neither calls the checker with an empty origin nor calls `Delegate`, so an `inherit` hop is not silently downgraded to an unconstrained check. These pin the mode-distinguishing origin derivation (CODE-2 resolving `originID` from `parent.CredentialOriginSessionID`/`parent.ID` and propagating the lookup error), which the field-mapping wrapper test does not exercise because it takes `CredentialOriginSessionID` as a verbatim input.

`// spec: 8.3` on each.

### TEST-2. Re-scope the skipped tier-4 credential-pool test to a deterministic pre-check assertion; remove the racy variant

**Target:** `tests/tier4_integration/delegation_credential_pool_race_test.go` (`:54` `t.Skip` and the intended-shape comment `:56-77`).

**Rationale:** The skipped test asserts a one-winner/N-1 outcome plus pod release, which requires the delegate path to claim a pod and assign a credential lease. That machinery does not exist on the delegate path (`service.go:1199-1221,1425` commits `StateCreated` only and claims no warm pod). `spec/08:470` states the pre-check is a point-in-time read rather than a reservation, so the gate this proposal adds cannot produce the racy outcome. The gate does make a distinct deterministic assertion true, which is the correct tier-4 coverage for this build. The existing skip note is factually stale: it claims there is no `CREDENTIAL_PROVIDER_MISMATCH` rejection at delegation time, but 0043 landed exactly that (`mcptools.go:1613`, wired at `mcptools_register.go:2046`).

**Change (staged description):**

- **Keep the deterministic case:** seed an `independent` delegation whose tenant `credentialPolicy.providerPools` are disjoint from the child runtime's `supportedProviders` (or a pool with no usable credential), start a parent session under that tenant, call `lenny/delegate_task`, and assert the tool result carries `CREDENTIAL_POOL_EXHAUSTED` and that no child row exists in the actual session store. The absence of a child row is an integration check the TEST-1 spy-based unit test cannot make, so it is not redundant with TEST-1.
- **Drop the "no warm pod claimed / warm-pool counters unchanged" assertion.** The delegate path never claims a pod on any code path (`service.go:1425`), so this assertion is vacuously true whether or not the gate fires and evidences nothing about the "before pod allocation" guarantee.
- **Remove the permanently-skipped racy variant** rather than keeping a dead skipped test whose one-winner/N-1 and pod-release behavior is architecturally unreachable on the delegate path. Re-file the race and pod-release coverage as a TEST-GAPS finding tied to the future §8.2 delegate-path pod-claim and lease-assignment build, so the tree carries no dead skipped test for an unbuilt mode.

`// spec: 8.3 (delegation-time credential-availability pre-check, CREDENTIAL_POOL_EXHAUSTED)`.

## 8. Findings closed on application

This proposal implements the `spec/08:470` delegation-time credential-availability pre-check for `inherit` and `independent`, resolving the availability-pre-check follow-up tracked as out of scope by 0043. It re-scopes `tests/tier4_integration/delegation_credential_pool_race_test.go` to a deterministic assertion and re-files the racy one-winner/N-1 and pod-release coverage against the future §8.2 delegate-path pod-claim build. It leaves the delegate-path pod-claim and lease-assignment flow, the lease-utilization reader, and `deny`-mode suppression open as distinct gaps.

## 9. Resolved in adversarial review

Subsequent adversarial review rounds populate this section. The challenge-round revisions already carried in the draft tightened TEST-1 and TEST-2. TEST-1 was trimmed: the `Server` method is a thin wrapper over `resolveCredentialPools`, and its intersection, inherit-to-origin, fail-closed, exhaustion, and nil-registries semantics are already pinned exhaustively by `start_preclaim_internal_test.go` through the identical code path, so the wrapper test pins only the new `DelegationCredentialQuery`-to-row field mapping and error propagation, while the non-redundant handler-gate branch tests are kept. TEST-2 was corrected on two points. The proposed "no warm pod claimed" assertion is vacuous because the delegate path never claims a pod on any path (`service.go:1425`), so it was dropped in favor of the `CREDENTIAL_POOL_EXHAUSTED` tool result and the absence of a child row as the load-bearing assertions. The permanently-skipped racy variant, whose one-winner/N-1 and pod-release behavior depends on an unbuilt delegate-path pod-claim flow, was removed and re-filed as a TEST-GAPS finding rather than kept as a dead skipped test.

### Pass 1 (2026-07-18, automated)

- **CODE-1 import cycle corrected.** The staged sketch placed `DelegationCredentialQuery` and `ErrDelegationCredentialUnavailable` in `mcptools` and had the `*sessionserver.Server` method reference them, which would force `sessionserver` to import `mcptools` and produce a compile-breaking cycle, since `mcptools` already imports `sessionserver` (`mcptools.go:77`, `mcptools_register.go:38`, `client_tools.go:14`) and `sessionserver` imports no `mcptools` file. CODE-1, CODE-2, and §11 now define the query struct and the sentinel in `sessionserver`, keep the `CredentialAvailabilityChecker` interface in `mcptools` referencing `sessionserver.DelegationCredentialQuery`, and cite the existing `SessionCreator`/`CreateSessionRequest` precedent. The false "sessionserver already depends on mcptools transitively" claim was removed.
- **TEST-1 omitted-default gate case added.** Added handler-gate branch (f): an omitted `credentialPropagation` (empty `in.CredentialPropagation`) runs the pre-check as `independent`, returning `CREDENTIAL_POOL_EXHAUSTED` with no `Delegate` call when the pool is exhausted and proceeding when available. This pins the `mode == ""` clause (`spec/08:445`, `lease.go:339` resolve omitted to `independent`), which none of branches (a)-(e) exercised.
- **TEST-1 origin-derivation coverage added, and CODE-2 fails closed on the parent-lookup error.** Added query-contents assertions that an `inherit` hop passes the parent's resolved origin (`parent.CredentialOriginSessionID`, else `parent.ID`) while `independent` and omitted-default hops pass an empty origin, plus a parent-lookup-error case. CODE-2 now propagates the `deps.Store.Get` error rather than silently falling through to an empty origin, so an `inherit` hop cannot be downgraded to an unconstrained check, following the credential-handling deny-on-doubt principle.

### Pass 2 (2026-07-18, automated)

- **Second §4.9 engine outcome now classified instead of escaping as `INTERNAL_ERROR`.** The staged `CheckDelegationCredentialAvailability` mapped only `credrouter.ErrNoCredentialAvailable` and propagated every other error verbatim, but the §4.9 engine it reuses returns two typed pre-claim outcomes: `credrouter.ErrNoCredentialAvailable` and `credrouter.ErrUserCredentialNotFound` (`preclaim.go:107-110`), the latter reachable for an `inherit` or `independent` delegation under a user-only tenant `credentialPolicy` with no registered credential. Session start maps that second error to the distinct `USER_CREDENTIAL_NOT_FOUND` / 404 (`start.go:148`), separate from `CREDENTIAL_POOL_EXHAUSTED` / 503 (`start.go:154`), so under the original mapping the delegate path would return the raw error and the dispatch would surface it as a generic `INTERNAL_ERROR` (`mcptools_register.go:2306`) for a reachable, actionable policy condition. CODE-1 now adds the `ErrDelegationUserCredentialNotFound` sentinel and maps both engine outcomes; CODE-2 adds the handler branch that surfaces `USER_CREDENTIAL_NOT_FOUND` (`PERMANENT`/`404`, `errorclassify.go:191`) via `*mcp.ToolError`, matching session start for the identical engine error. The interface doc, §3 and §2 mapping prose, and TEST-1 (wrapper mapping plus handler-gate case (g)) were updated to cover the second outcome.

## 10. Open decisions for review

- **Sequencing.** The availability pre-check guards admission of a delegation whose downstream pod-claim and credential-lease-assignment flow (spec §8.2 steps 5-9) is itself unimplemented on the delegate path. Land the gate now, consistent with 0043 (which landed the sibling cross-environment gate on the same insertion point with the same guards-a-not-yet-built-downstream property), or sequence it after the delegate-path pod-claim flow so the pre-check, the post-claim assignment race, the pod release, and the one-winner/N-1 behavior all land together as one coherent `spec/08:470` unit? Recommendation: land the gate now. It is a self-contained, spec-named behavior with its own error code and its own deterministic test. **Resolved at sign-off: land the gate now.** The delegate-path pod-claim flow (spec §8.2 steps 5-9) is filed as a separate cluster; this pre-check becomes its true pre-allocation fail-fast when that cluster lands.
- **Racy tier-4 test disposition.** Remove `tests/tier4_integration/delegation_credential_pool_race_test.go`'s racy variant and re-file the one-winner/N-1 and pod-release coverage against the future §8.2 delegated-child pod-claim build (recommended, TEST-2), or keep it skipped with a corrected note pointing at the delegate-path pod-claim gap and the deferred lease-utilization reader? **Resolved at sign-off: remove the racy variant and re-file its coverage against the future §8.2 delegate-path pod-claim cluster (TEST-2).**

## 11. Files touched on application

- `pkg/gateway/mcpfabric/mcptools/mcptools.go`: CODE-1 (the `CredentialAvailabilityChecker` interface, whose method references the `sessionserver`-defined query struct, and the `CredAvailability` `Deps` field, adjacent to `:294-320`).
- `pkg/gateway/sessionserver/start.go`: CODE-1 (the `DelegationCredentialQuery` struct, the `ErrDelegationCredentialUnavailable` and `ErrDelegationUserCredentialNotFound` sentinels, and the `CheckDelegationCredentialAvailability` method on `*Server`, adjacent to `resolveCredentialPools` at `:1295`). The struct and sentinel live in `sessionserver` so the `*Server` method references no `mcptools` type and no import cycle forms.
- `cmd/lenny-gateway/mcpsurface.go`: CODE-1 (`CredAvailability: sessionSrv` in the `Deps` literal at `:230,234`).
- `pkg/gateway/mcpfabric/mcptools/mcptools_register.go`: CODE-2 (the pre-claim availability gate between `:2050` and `:2217`).
- `pkg/gateway/sessionserver/start_preclaim_internal_test.go`, `pkg/gateway/mcpfabric/mcptools/*_test.go`: TEST-1 (the wrapper field-mapping test and the handler-gate branch tests).
- `tests/tier4_integration/delegation_credential_pool_race_test.go`: TEST-2 (re-scope to the deterministic `CREDENTIAL_POOL_EXHAUSTED` assertion, remove the racy variant, and re-file its coverage as a TEST-GAPS finding).
</content>
</invoke>
