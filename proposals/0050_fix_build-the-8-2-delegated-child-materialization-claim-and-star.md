# Proposal: Build the §8.2 delegated-child materialization: claim-and-start a StateCreated delegated child through the shared session-start engine

- **Status:** Applied to spec (2026-07-22). Option (i) synchronous materialization within `delegate_task` selected; the implementation reuses the shared warm-pool claim and release primitives (`claimAtCreate`, `startOnPod`, `rollbackClaim`, `rollbackBinding`) as a pure consumer, so no shared-primitive edit and no Integrator serialization against C-22. Converged after 5 adversarial review rounds (7 findings fixed).
- **Date:** 2026-07-19.
- **Scope:** A code-first build of the §8.2 delegation flow steps 5-9 (`spec/08_recursive-delegation.md:93-97`), which the implementation does not perform today. A new `MaterializeDelegatedChild` entry point on `*sessionserver.Server` (`pkg/gateway/sessionserver/start.go`) composes the existing decomposed create-and-start helpers to claim a warm pod, assign the credential lease, stream the stamped `WorkspacePlan`, launch, and transition an existing `StateCreated` child to `running`. The `lenny/delegate_task` handler (`pkg/gateway/mcpfabric/mcptools/mcptools_register.go`) drives the child through it via a consumer-side `ChildMaterializer` seam, the gateway assembly wires the seam (`cmd/lenny-gateway/mcpsurface.go`), and the `taskHandle.State` doc comment (`pkg/gateway/mcpfabric/mcptools/mcptools.go`) is corrected. A single spec touch qualifies the §8.8 and §15.1 `created`-state pod-claim notes for a delegated child. This closes T-8.2.17 (Medium) by building the materialization rather than reconciling the spec to a deferred model, and unblocks T-ADV.14 (Low) by giving the post-pod-claim assignment race a code path. It adds no new pod-claim or credential-release primitive, no new wire field, and no dual mode. It supersedes the earlier spec-only option-(b) draft (queued as `in-review:0050` in `PROPOSAL-QUEUE.md`).

This document stages the proposed spec, code, and test changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off, spec edit first.

## 1. Problem

§8.2 defines a nine-step delegation flow (`spec/08_recursive-delegation.md:56-97`). Steps 5-9 allocate the child pod (`:93`), stream the rebased exported files into it (`:94`), start it (`:95`), and expose a virtual MCP child interface to the parent (`:96-97`). The implementation performs only steps 1-4.

`Service.Delegate` runs the validate, cycle, depth, and insert stages and returns (`pkg/gateway/mcpfabric/delegation/service.go`, `Delegate`). `insertChildSession` commits the child via `store.Create` in `session.StateCreated` with no `PodAssignment`, and stamps the exported-file upload sources on the child `WorkspacePlan`. A `StateCreated` delegated child then traverses no session-start path: it never claims a warm pod, is never assigned a credential lease, never launches, and never leaves `created`, so delegated children do not run today. Such a child also cannot be interacted with, because `PodExecutor.streamFor` rejects any session not bound to a pod (`pkg/gateway/session/executor/pod.go:136`: `"podexec: session %s is not bound to a pod"`), and the delegate handler delivers task input through `Executor.Send` (`pkg/gateway/mcpfabric/mcptools/mcptools_register.go:2595`).

The `taskHandle.State` doc comment records the materialization as unbuilt: it states v1 returns `created` "because §8.2 step 7 (pod allocation + workspace materialization) is unbuilt" and predicts the value becoming `submitted` or `running` once the allocation flow lands (`pkg/gateway/mcpfabric/mcptools/mcptools.go:918-928`).

Two enabling pieces are already built and closed. The exported-file stream is stamped on the child `WorkspacePlan` for the §6.3 binder (F-8.2.4). The parent-facing virtual MCP child interface and the resume-time `children_reattached` reconstruction exist (F-8.2.11; `emitChildrenReattached` at `pkg/gateway/sessionserver/start.go:3217`). The residual unbuilt work is the claim-and-start trigger for steps 5-9.

A single canonical claim-and-start engine already drives the top-level create-and-start path: `handleCreateAndStart` (`start.go:423`) calls `mintClaimStartPersist` (`start.go:687`), which claims a warm pod through `claimAtCreate` (`start.go:1754`), resolves and assigns the credential lease through `resolveCredentialPools` (`start.go:1362`) plus `credrouter.PreClaim`, launches through `startOnPod` (`start.go:1938`), and releases a claimed pod on failure through `rollbackClaim` (`start.go:2742`). The post-pod-claim assignment-race outcome is already mapped at `start.go:159-166` (the credential-assignment-race typed error matched via `errors.As(err, &credAssign)`, which writes `assignment_race`), a distinct branch from the pre-claim exhaustion case at `start.go:155` (`credrouter.ErrNoCredentialAvailable`), where no pod is claimed and the writer emits `pre_claim`. These decomposed helpers claim, stream, launch, and transition an existing row, so a delegated child that is already in `StateCreated` can reuse them without a second INSERT and without a parallel path.

T-8.2.17 (Medium, `TEST-GAPS.md`) records the divergence: §8.2 places pod allocation and lease assignment at step 5, but the delegate handler commits the child in `StateCreated` and defers the claim to a session-start path the child never enters. T-ADV.14 (Low, `TEST-GAPS.md`) records that the §8.3 post-pod-claim assignment race (`spec/08_recursive-delegation.md:470`: concurrent delegations each passing the point-in-time pre-check yet collectively exhausting the pool, the loser's pod released, one-winner-of-N `CREDENTIAL_POOL_EXHAUSTED`) is architecturally unreachable until the delegate path claims a pod. Finding C-42 (`PROPOSAL-QUEUE.md`) queued this work; the prior spec-only option-(b) draft reconciled the §8.2 wording to a deferred model and re-filed the materialization as a separate finding. This proposal builds the materialization instead.

## 2. Decisions

- **Reuse the single canonical claim-and-start engine rather than build a parallel path.** The materialization composes the existing decomposed helpers (`claimAtCreate`, `resolveCredentialPools` plus `credrouter.PreClaim`, `startOnPod`, the finalize-time lease assignment, and `registerBinding`) that already claim a warm pod, resolve and assign the credential lease, stream the stamped `WorkspacePlan` through the §6.3 binder, launch, transition an existing `StateCreated` row to `running` via `store.Update`, and publish the bind into the shared executor registry so the child is reachable. This honors the single-canonical-implementation and no-dual-modes rules in `code-best-practices.md`. `mintClaimStartPersist` cannot be invoked wholesale because it ends in `store.Create` (`start.go:773`) and the child already exists in `StateCreated`; the reuse boundary is its decomposed sub-phases, which the child's `StateCreated` entry state fits exactly.
- **Materialize synchronously within the `delegate_task` call (recommended option (i)).** After admission returns the `StateCreated` child, the handler drives it through claim-and-start via the shared engine and returns the `running` child plus its always-on virtual MCP child interface (F-8.2.11), so §8.2 steps 5-9 execute inline and the numbered flow stays literally true. This keeps spec churn minimal (the §8.2 numbered flow needs no deferral rewording) and matches `spec/08_recursive-delegation.md:96-97`, where the parent interacts with the child only after the child is running. Option (ii), a deferred post-admission trigger, is carried as an open decision (§9).
- **Cross the `mcpfabric` ↔ `sessionserver` package boundary with a small consumer-side interface.** The delegate handler defines a `ChildMaterializer` interface, implemented by `*sessionserver.Server`, mirroring the existing `CredAvailability` seam (`CheckDelegationCredentialAvailability` at `mcptools_register.go:2086`, `CredentialAvailabilityChecker` declared at `mcptools.go:343`, implemented by `*sessionserver.Server`). This follows accept-interfaces-return-concrete-types: the interface is defined at the consumer.
- **Surface the engine's typed credential outcomes as MCP tool errors.** The shared engine's error mapping at `start.go:145-166` writes to an `http.ResponseWriter`; the delegate path is an MCP tool. The new entry point returns the typed sentinels (`credrouter.ErrNoCredentialAvailable`, the credential-assignment-race error, the pool-warming error, and the token-service-unavailable error), and the handler maps them to the same MCP tool codes the pre-check already emits (`CREDENTIAL_POOL_EXHAUSTED`, `USER_CREDENTIAL_NOT_FOUND`), reusing the switch pattern at `mcptools_register.go:2087-2104`. On any assignment failure the child fails closed: a pod-claim or credential-assignment failure before launch releases the claimed pod via the existing `rollbackClaim` path (`start.go:2742`), and a `store.Update`-to-running failure after a successful `startOnPod` releases the bound pod and its assigned lease via the existing `rollbackBinding` path (`start.go:2666`), matching the top-level create path's two-primitive rollback (`start.go:759-761`, `start.go:773-778`).
- **Qualify the §8.8 and §15.1 `created`-state notes for a delegated child.** Both notes state that in `created` a warm pod has already been claimed (`spec/08_recursive-delegation.md:879`, `spec/15_external-api-surface.md:630`). A delegated child claims its pod during the post-admission materialization rather than at the top-level create-path `claimAtCreate`, so it passes transiently through `created` before reaching `running` within the same `delegate_task` call. The notes are qualified to state that the created-state pod-claim invariant is the top-level create-path invariant and a delegated child claims its warm pod at the §8.2 materialization step. Folding a pod claim into the delegation INSERT to make `created` uniformly pod-backed is rejected because it would build a parallel claim path.
- **Reuse rather than modify the shared warm-pool claim and release primitives.** The materialization consumes `claimAtCreate` / `startOnPod` and the existing `rollbackClaim` (claim-stage release) and `rollbackBinding` (post-launch release) primitives; it introduces no new claim or release primitive. `poolstore` and `credrouter.PreClaim` are consumed unchanged. Because C-42 shares those primitives with proposal-B's C-22 (§4.6 eviction, `PROPOSAL-QUEUE.md`), flag the Integrator inbox to serialize only if the implementation must touch a shared claim or release primitive; a pure-reuse build needs no serialization for correctness (§9).
- **Spec-first, per `spec-driven-development.md`.** Land the §8.8 and §15.1 created-state qualification (SPEC-2), then the engine entry point and the handler seam, then the wiring and the doc-comment correction, then the tests.

## 3. The delegated-child materialization path after the change

Admission is unchanged: `Service.Delegate` still commits the child in `StateCreated` with the exported-file upload sources stamped on its `WorkspacePlan` (F-8.2.4). The handler then drives the child through the shared engine:

- **The child row is loaded and materialized.** `MaterializeDelegatedChild` loads the committed `StateCreated` child, claims a warm pod through `claimAtCreate`, resolves and assigns the credential lease through `resolveCredentialPools` plus `credrouter.PreClaim`, streams the stamped `WorkspacePlan` through the §6.3 binder, launches through `startOnPod`, transitions the existing row `StateCreated` → `running` via `store.Update`, and then publishes the bind through `registerBinding` (`start.go:788`). No second INSERT is performed. `registerBinding` is the step that puts the `startOnPod` `BindResult` into the shared `*podsession.Registry` (`podRegistry.Put`, `start.go:2501`) that the executor reads (`cmd/lenny-gateway/stores.go:2091`, `sessionsrv.go:223`) and persists the pod assignment and workspace root for cross-replica recovery (`start.go:2502`, `:2509`); `startOnPod` returns the `BindResult` but does not register it, so without this step `Executor.Send` still rejects the child as unbound. As on the create path, `recordSessionCreated` and `registerLeaseTree` (§8.6 lease-extension budget) also run after the successful transition.
- **A credential-assignment failure fails closed.** The post-claim assignment race that `start.go:159-166` already maps returns the typed sentinel from the entry point, the claimed pod is released through `rollbackClaim`, and the row is left non-running. A distinct failure of the terminal `store.Update`-to-running after a successful `startOnPod` releases the bound pod and its assigned lease through `rollbackBinding`, matching the top-level create path's post-launch persist rollback (`start.go:773-778`). Mirroring the create path, `rollbackBinding` runs on the failed terminal persist before `registerBinding`, so no registry entry leaks past the failed write. The handler maps the assignment-race sentinel to `CREDENTIAL_POOL_EXHAUSTED`, consistent with the top-level create path and with `spec/08_recursive-delegation.md:470`.
- **Task input is delivered only after the child is bound.** The handler calls `Executor.Send` (`mcptools_register.go:2595`) after materialization succeeds, when `registerBinding` has published the bind so `streamFor` no longer rejects the session (`pod.go:136`), and builds the `taskHandle` from the post-materialization state that `MaterializeDelegatedChild` returns, which now reads `running`.
- **The virtual MCP child interface is unchanged.** The parent-facing interface and the `children_reattached` reconstruction (F-8.2.11, `emitChildrenReattached` at `start.go:3217`) are consumed as-is.

A minimal in-process gateway that leaves the `ChildMaterializer` seam nil falls through without materializing, mirroring the existing nil-guard on the `CredAvailability` seam (`mcptools_register.go:2065`).

## 4. Proposed changes

### SPEC-2. Qualify the §8.8 and §15.1 created-state pod-claim notes for a delegated child

**Target:** `spec/08_recursive-delegation.md` §8.8 session-level state-mapping table (`:879`) and `spec/15_external-api-surface.md` §15.1 external session-state table (`:630`).

**Rationale:** Both notes assert that in `created` a warm pod has already been claimed. That invariant holds for a top-level session, which claims its pod at the create-path `claimAtCreate` before entering `created`. A delegated child is committed to `created` by `insertChildSession` before any pod claim and claims its pod at the §8.2 materialization step (steps 5-9) built by CODE-1/CODE-2, so it passes transiently through `created` before reaching `running` within the same `delegate_task` call. The notes are qualified to name that timing without changing the top-level invariant.

**Anchor (§8.8).** In the session-level state-mapping table, replace the `Notes` cell of the `created` row (`:879`). Leave every other row and the surrounding prose unchanged.

**Change (staged text).** Replace the `created` row with:

```
| `created`                  | `submitted`                    | `submitted`                    | Session created. For a top-level session the warm pod is claimed and credential availability pre-checked before the session enters `created`; the credential lease is assigned at finalize, awaiting workspace uploads or finalization. A delegated child ([§8.2](#82-delegation-mechanism)) is committed to `created` before its warm pod is claimed and claims its pod at the §8.2 materialization step (steps 5–9), passing transiently through `created` before reaching `running` within the same `delegate_task` call (see [§15](15_external-api-surface.md#151-rest-api) for canonical description). |
```

**Anchor (§15.1).** In the external session-state table, replace the leading clause of the `created` row's Description cell (`:630`), up to and including "awaiting workspace file uploads or finalization." Preserve the `**TTL:**` sentence and everything after it, and the `Terminal?` column value (`No`).

**Change (staged text).** Replace the leading clause with:

```
Session created. For a top-level session a warm pod has been claimed and credential availability has been pre-checked (see [§7.1](07_session-lifecycle.md#71-normal-flow) steps 3–4) before the session enters `created`; the credential lease is assigned at finalize, awaiting workspace file uploads or finalization. A delegated child ([§8.2](08_recursive-delegation.md#82-delegation-mechanism)) is committed to `created` before its warm pod is claimed and claims its pod during the §8.2 materialization step, passing transiently through `created` before reaching `running` within the same `delegate_task` call.
```

**Preserved unchanged:** the `**TTL:** maxCreatedStateTimeoutSeconds` sentence and its expiry behavior, the configurability note, and the `Terminal?` column.

### CODE-1. Add the MaterializeDelegatedChild engine entry point on *sessionserver.Server

**Target:** `pkg/gateway/sessionserver/start.go` (a new exported method alongside the decomposed create-and-start helpers `claimAtCreate` at `:1754`, `resolveCredentialPools` at `:1362`, `startOnPod` at `:1938`, and `rollbackClaim` at `:2742`).

**Rationale:** The materialization must reuse the decomposed sub-phases of the create-and-start engine without the leading `store.Create` that `mintClaimStartPersist` ends in (`:773`), because the delegated child already exists in `StateCreated`. A single new entry point composes those helpers and transitions the existing row via `store.Update`, keeping one canonical claim-and-start path.

**Change (staged description).** Add a method:

```go
// MaterializeDelegatedChild runs §8.2 delegation steps 5-9 for a child
// that Service.Delegate already committed in session.StateCreated with a
// stamped WorkspacePlan (F-8.2.4) and no PodAssignment. It composes the
// decomposed create-and-start sub-phases — claimAtCreate, resolveCredentialPools
// + credrouter.PreClaim, startOnPod, and registerBinding — to claim a warm pod,
// assign the credential lease, stream the rebased exported files through the §6.3
// binder, launch, transition the existing row StateCreated -> running via
// store.Update, and publish the bind into the shared executor Registry. It
// performs no store.Create: the row already exists.
//
// registerBinding (start.go:788) is not optional: startOnPod returns a
// *podsession.BindResult but does not register it, and PodExecutor.streamFor
// resolves the session through the shared *podsession.Registry that registerBinding
// populates via podRegistry.Put (start.go:2501). Without it Executor.Send still
// rejects the child as unbound (pod.go:136). registerBinding also persists the
// pod assignment and workspace root (start.go:2502, :2509) so a fresh replica can
// recover the binding. As on the create path, recordSessionCreated and
// registerLeaseTree (§8.6 lease-extension budget) run after the successful
// transition.
//
// On success it returns the child's post-materialization state
// (session.StateRunning) so the delegate_task handler builds the taskHandle
// from the live transitioned state rather than the pre-materialization
// StateCreated snapshot.
//
// It fails closed on any failure and leaves the row non-running. On a pod-claim
// or credential-assignment failure before launch it releases the claimed pod
// via rollbackClaim and returns the router's typed sentinel
// (credrouter.ErrNoCredentialAvailable, the credential-assignment-race error,
// the pool-warming error, or the token-service-unavailable error) so the
// handler can map it to the same MCP tool codes the §8.3 pre-check emits. On a
// store.Update-to-running failure after a successful startOnPod it releases the
// bound pod and its assigned credential lease via rollbackBinding, mirroring
// the top-level create path's post-launch persist rollback (start.go:773-778).
// As on the create path, rollbackBinding runs on the failed terminal persist
// before registerBinding, so no pod, lease, or registry entry leaks past the
// failed persist. spec: §8.2 lines 93-97.
func (s *Server) MaterializeDelegatedChild(ctx context.Context, tenantID, childID string) (session.State, error)
```

Load the child via `s.store.Get(ctx, tenantID, childID)`, guard that it is `session.StateCreated` (return a typed error otherwise so a double-materialization is a caller bug rather than a silent re-claim), reuse the child's stamped `WorkspacePlan`, and drive `claimAtCreate` → `resolveCredentialPools` + `credrouter.PreClaim` → `startOnPod`, then `store.Update` the row to `running`, then `registerBinding` to publish the `startOnPod` `BindResult` into the shared `podRegistry` and persist the pod assignment and workspace root, and return `session.StateRunning`. The `registerBinding` call (`start.go:788`, `podRegistry.Put` at `:2501`) is required for reachability: `startOnPod` alone does not register the bind, and `Executor.Send` / `streamFor` reject an unregistered session (`pod.go:136`). As on the create path, run `recordSessionCreated` and `registerLeaseTree` after the successful transition. On a `startOnPod` error release the claimed pod via `rollbackClaim`; on a `store.Update`-to-running failure after a successful `startOnPod` release the bound pod and its assigned lease via `rollbackBinding` before `registerBinding` runs, mirroring the top-level create path at `start.go:773-778`, so a persist failure after launch leaks neither the pod, the lease, nor a registry entry. Reuse the existing typed-error mapping semantics at `:145-166` by returning the same sentinels rather than writing to an `http.ResponseWriter`; the HTTP mapping stays on the top-level create path. Take `context.Context` first and propagate it. Run `gofumpt` and `goimports`.

### CODE-2. Drive the delegated child through materialization in the delegate_task handler via a consumer-side seam

**Target:** `pkg/gateway/mcpfabric/mcptools/mcptools_register.go` (the `delegate_task` handler, after `deps.Delegation.Delegate` at `:2278` and before `Executor.Send` at `:2595`) and the handler deps struct that declares `CredAvailability` (`pkg/gateway/mcpfabric/mcptools/mcptools.go:336-343`).

**Rationale:** The handler owns the flow that receives the `StateCreated` child and must materialize it before delivering task input, because `Executor.Send` / `streamFor` reject an unbound session (`pod.go:136`). The `CredAvailability` seam (`CheckDelegationCredentialAvailability`, `:2086`, implemented by `*sessionserver.Server`) is the established consumer-side interface pattern to reuse.

**Change (staged description).**

1. Declare a `ChildMaterializer` interface next to `CredentialAvailabilityChecker` (`mcptools.go`), and add a `ChildMaterializer` field on the handler deps struct next to `CredAvailability` (`mcptools.go:343`):

```go
// ChildMaterializer runs §8.2 delegation steps 5-9 for a delegated child that
// Service.Delegate committed in StateCreated: it claims the warm pod, assigns
// the credential lease, streams the stamped WorkspacePlan, launches, and
// transitions the child to running. It returns the child's post-materialization
// state so the handler builds the taskHandle from the live transitioned state.
// Implemented by *sessionserver.Server via MaterializeDelegatedChild.
// spec: §8.2 lines 93-97.
type ChildMaterializer interface {
	Materialize(ctx context.Context, tenantID, childID string) (session.State, error)
}
```

2. In the handler, after `res, err := deps.Delegation.Delegate(...)` succeeds (`:2278`) and before the `Executor.Send` at `:2595`, materialize the child when the seam is wired:

```go
// §8.2 steps 5-9 — materialize the admitted StateCreated child (claim pod,
// assign lease, stream workspace, launch) so the returned handle is a running
// child the parent can interact with. Guarded like the CredAvailability seam
// (:2065) so a minimal in-process gateway that leaves it nil falls through.
// childState defaults to the pre-materialization StateCreated snapshot so a
// nil seam (minimal in-process gateway) falls through unchanged. When the seam
// is wired, the returned post-materialization state overwrites it and the
// handle reads running.
childState := res.Child.State
if deps.ChildMaterializer != nil {
	st, err := deps.ChildMaterializer.Materialize(ctx, tenant, res.Child.ID)
	if err != nil {
		// Map the engine's typed sentinels to the same MCP tool codes the
		// §8.3 pre-check emits at :2087-2104. Fail closed; the engine has
		// already released the pod (rollbackClaim before launch,
		// rollbackBinding on a post-launch persist failure).
		switch {
		case errors.Is(err, credrouter.ErrNoCredentialAvailable) /* or assignment-race */:
			return mcp.ToolResult{}, mcp.NewToolError("CREDENTIAL_POOL_EXHAUSTED", ...)
		case errors.Is(err, credrouter.ErrUserCredentialNotFound):
			return mcp.ToolResult{}, mcp.NewToolError("USER_CREDENTIAL_NOT_FOUND", ...)
		// pool-warming -> RUNTIME_UNAVAILABLE; token-service -> TOKEN_SERVICE_UNAVAILABLE
		}
	}
	childState = st
}
```

3. Only then deliver `taskInput` via `Executor.Send` (`:2595`, now valid because the child is bound) and build the `taskHandle` (`:2602-2607`) with `State: string(childState)`, which reads `running` once the seam has transitioned the child. Run `gofumpt` and `goimports`.

### CODE-3. Correct the taskHandle.State doc comment

**Target:** `pkg/gateway/mcpfabric/mcptools/mcptools.go`, `taskHandle.State` field doc comment (`:923-928`).

**Rationale:** The comment states v1 returns `created` "because §8.2 step 7 (pod allocation + workspace materialization) is unbuilt" and predicts `submitted`/`running` once the flow lands. With CODE-1/CODE-2 the materialization is built and the returned value is `running`, so the rationale is now false. The wire type is unchanged; only the value and its documented meaning change.

**Change (staged text).** Replace the `State` field comment with:

```go
// State is the child's §8.8 task state at response time. The child is
// materialized synchronously within delegate_task (§8.2 steps 5-9: pod
// allocation, workspace materialization, and launch), so the field carries
// the post-materialization state that MaterializeDelegatedChild returns,
// which reads `running` once the child launches.
State string `json:"state"`
```

No struct or wire change: `State` is a JSON string. The handler now builds it from the state the `ChildMaterializer.Materialize` seam returns (`string(childState)`) rather than from the pre-materialization `res.Child.State` snapshot, which the by-value `Result.Child` (`service.go:186-187`) never re-reads after `Delegate`.

### CODE-4. Wire the ChildMaterializer seam in the gateway assembly

**Target:** `cmd/lenny-gateway/mcpsurface.go`, the delegate-handler deps construction where `CredAvailability` is set (`:239`).

**Rationale:** The seam must be populated in production wiring so the handler drives real materialization, exactly as `CredAvailability` is populated today. `cmd` binaries stay thin and delegate to `pkg` per `code-best-practices.md`.

**Change (staged text).** In the same struct literal that sets `CredAvailability: sessionSrv`, also set:

```go
ChildMaterializer: sessionSrv,
```

`sessionSrv` (`*sessionserver.Server`) already holds the pod binder, credential router, session store, and credential-pool registries the engine needs. No new dependency is introduced.

### TEST-1. tier-4: delegated-child materialization to running and parent interaction

**Target:** `tests/tier4_integration/delegation_child_materialization_test.go` (new).

**Rationale:** The happy-path materialization is the behavior CODE-1/CODE-2 add and T-8.2.17 names. It must be pinned end to end across the delegate handler, the delegation Service, the shared engine, and the warm-pool and credential-pool stores.

**Change (staged description).**

- Build on the in-process `sessionserver.New` plus the real `delegation.Service` and warm-pool/credential-pool fixtures the sibling delegation credential tests use (`delegation_credential_pool_race_test.go`, `cross_environment_delegation_test.go`), with the `ChildMaterializer` seam wired to the server.
- **Materialization to running (the built path):** fire an `inherit`-mode (or omitted-default) `delegate_task` through the real handler with a pool that has an assignable warm pod and credential slot. Assert the returned `taskHandle.State` reads `running`, the child row carries a `PodAssignment` and an assigned credential lease, and the parent-facing virtual MCP child interface is present.
- **Interaction requires binding (the spec-named failure the fix removes):** deliver task input to the materialized child and assert `Executor.Send` succeeds, confirming `streamFor` no longer rejects the session (`pod.go:136`). The non-happy path this pins is a child left in `StateCreated` that `Executor.Send` would reject as unbound.
- **Handler fail-closed mapping (the §8.3:470 parent-observable failure):** fire a `delegate_task` whose child materialization fails on a post-claim credential-assignment race (a credential pool with an assignable warm pod but no assignable credential slot). Assert the handler returns the MCP tool error `CREDENTIAL_POOL_EXHAUSTED` to the delegating parent and that no warm pod is leaked (the warm-pod count returns to baseline). This pins the new CODE-2 sentinel-to-tool-code switch (`mcptools_register.go`) at the tier the change reaches, which TEST-3 case (b) covers only at the engine level. Carry `// spec: 8.3 (line 470 post-claim assignment race)`.
- **Handler mapping of a newly surfaced sentinel:** drive one materialization failure that maps to a code the §8.3 pre-check never emits (a pool-warming failure returning `RUNTIME_UNAVAILABLE`, or a token-service-unavailable failure returning `TOKEN_SERVICE_UNAVAILABLE`) and assert the parent receives that tool code, so the new mappings are exercised rather than assumed. Carry `// spec: 8.2 (steps 5-9 delegated-child materialization)`.
- Carry `// spec: 8.2 (steps 5-9 delegated-child materialization)` and a `// diagnosis:` comment stating that a failure means a delegated child did not claim a pod, launch, become interactable, or fail closed with the parent-observable tool code.

### TEST-2. Reconcile the existing tier-4 delegation tests to synchronous materialization

**Target:** the existing tier-4 delegation tests that run the real `cmd/lenny-gateway` binary through `gateway.StartWith(t, "--dev-mode")` and encode the pre-materialization `created`-state contract: `tests/tier4_integration/elicitation_test.go`, `tests/tier4_integration/delegation_adversarial_test.go`, and `tests/tier4_integration/delegation_await_collect_test.go`, plus their shared `mcpClient` helpers (`startSession`, `delegateChild`) defined in `elicitation_test.go`.

**Rationale:** CODE-4 sets `ChildMaterializer: sessionSrv` on the same `mcptools.Register` deps the dev-mode binary constructs (`cmd/lenny-gateway/mcpsurface.go:224-243`, alongside `CredAvailability` at `:239`), so after the change `delegate_task` returns an already-`running` child rather than a `created` one. The tests above were built on the pre-materialization contract that a delegated child lands in `created` and is advanced to running only by a separate finalize+start walk. Under synchronous materialization (option (i)) the child is already `running` when `delegate_task` returns, so the finalize+start walk fails: `handleFinalize` rejects any non-`created` state via `session.Validate(PreconditionRequest{Endpoint: EndpointFinalize, CurrentState: row.State})` (`pkg/gateway/sessionserver/sessionserver.go:3036-3042`), and `startSession`'s non-OK check reports it with `t.Fatalf` (`elicitation_test.go:152-154`). The `mode=any` `created`-state assertion (`delegation_await_collect_test.go:108`) also no longer holds. Leaving these tests unedited leaves the tier-4 suite red, so they land with CODE-4.

**Change (staged description).** The post-delegation child state is `running`: the child materializes synchronously within `delegate_task`, and `delegateChild` sends no `taskInput`, so `Executor.Send` (`mcptools_register.go:2594`) is skipped and the child stays at `running` rather than advancing to a terminal state.

- Correct the doc comments that state a delegated child "lands in the created state": the `startSession` comment in `elicitation_test.go:144-147` and the assertion comment in `delegation_await_collect_test.go:108-111`. State that a delegated child materializes synchronously within `delegate_task` and is `running` when the tool returns.
- Drop the now-invalid `startSession(child)` finalize+start walk on delegated children, because the child is already `running` and its finalize is rejected: `elicitation_test.go:174` (`c.startSession(child)`), and `delegation_adversarial_test.go:152, 154, 204` (`c.startSession(child1)`, `c.startSession(child2)`, `c.startSession(granted.ChildSessionID)`). The delegated child is running immediately after `delegateChild`, so each subsequent hop's precondition (an ancestor must be running to delegate onward) is already satisfied without the walk. The `startSession` helper stays for the top-level `runningSession` path if still used, and is removed only if it becomes dead.
- Change `delegation_await_collect_test.go:108` from asserting the awaited sibling child is `created` to asserting it is `running`. The sibling materializes on delegation, its task input is never delivered, and the test's real invariant (`mode=any` does not auto-cancel a still-running sibling) is preserved by the `running` assertion.
- Confirm `tests/tier4_integration/cross_environment_delegation_test.go` is unaffected: it constructs `mcptools.Deps` directly (`:112`) without the `ChildMaterializer` seam, so it keeps the nil-seam fall-through described in §3, and it already promotes delegated children to running by hand through `fx.store.Update` (`:347-353`). Its `:343-344` comment ("delegate_task commits a child in the created state") stays accurate under the direct-deps nil-seam wiring and needs no change under this proposal.
- `delegation_test.go:35` (asserts only that the child state field is non-empty) and the `mode_all` case (`delegation_await_collect_test.go:114-143`, which terminates both children before asserting `completed`) are unaffected and are left unchanged.

Preserve the existing `// spec:` and `// diagnosis:` annotations on each edited test; no new annotation is added.

### TEST-3. tier-1/tier-2: MaterializeDelegatedChild transition and typed credential-failure mapping

**Target:** `pkg/gateway/sessionserver` (unit/component test for `MaterializeDelegatedChild`, alongside the existing `mintClaimStartPersist` / `startOnPod` harness).

**Rationale:** CODE-1 adds a new engine entry point whose transition, rollback, and typed-error surface must be pinned below the integration tier, including the fail-closed assignment-race path that reuses the mapping at `start.go:155-166`.

**Change (staged description).** Assert `MaterializeDelegatedChild` on a `StateCreated` child row:

- **(a) happy transition (tier-1/tier-2):** claims a pod, transitions the existing row to `running` via `store.Update` with no second `store.Create`, and registers the bind so `s.podRegistry.Get(childID)` resolves the child (the reachability `Executor.Send` depends on).
- **(b) assignment-race fail-closed (spec-named failure):** a credential-assignment failure returns the assignment-race sentinel, releases the claimed pod via `rollbackClaim` (no pod leak, warm-pod count returns to baseline), and leaves the row non-running.
- **(c) pre-claim exhaustion (error path):** an empty §4.9 provider intersection returns `credrouter.ErrNoCredentialAvailable` before any pod claim.
- **(d) non-created guard (boundary):** calling it on a row that is not `StateCreated` returns the typed guard error and claims no pod.
- **(e) post-launch persist failure (spec-named failure):** a `store.Update`-to-running failure after a successful `startOnPod` (a store fault injected at the terminal write) releases the bound pod and its assigned credential lease via `rollbackBinding` (no pod or lease leak, warm-pod and lease counts return to baseline) and leaves the row non-running. This pins the second rollback primitive, which case (b) does not reach.

Carry `// spec: 8.2 (steps 5-7), 8.3 (line 470 post-claim assignment race)` and, for the tier-2 case, a `// diagnosis:` comment. Reuse the existing sessionserver test harness that exercises `mintClaimStartPersist` / `startOnPod`.

### DOC-1. Mark T-8.2.17 resolved and note T-ADV.14 unblocked on application

**Target:** `TEST-GAPS.md`, T-8.2.17 and T-ADV.14.

**Rationale:** T-8.2.17 is closed by building the materialization rather than filing it (the problem mandate). T-ADV.14's blocking dependency (the delegate path claims no pod) is removed once the child materializes through the engine, but the one-of-N concurrency race test remains owned by the ADV battery under the skip-ADV policy.

**Change (staged description).** On application, flip T-8.2.17 to resolved, referencing this proposal, the new `MaterializeDelegatedChild` entry point, and the delegate-handler wiring, and noting that the divergence was closed by building steps 5-9 (option (i)) rather than by reconciling §8.2 to a deferred model. Update T-ADV.14's Dependencies line to record that its blocking prerequisite (the delegate-path pod claim) has landed, leaving the one-of-N assignment-race test to the ADV battery. Applied at implementation time, consistent with how findings are closed.

## 5. Non-goals

- **No spec edit to the §8.2 numbered flow (steps 5-9).** Under the recommended option (i) the flow executes inline and stays literally true; the deferred-model rewording of the abandoned option-(b) draft is not adopted.
- **No retie of the §8.2 step-2a cycle-detection parenthetical** (`spec/08_recursive-delegation.md:64`, "cycle detection uses runtime identity … because the child session does not exist yet at this point (pod allocation happens in step 5)"). This edit was considered and dropped. Both clauses of that sentence are true and neither is falsified by this change: the child session id is not minted until `insertChildSession` (Stage 4 of `Delegate`), which runs after `detectCycle` (Stage 2), so the child does not exist at cycle-detection time regardless of pod-allocation timing; and under option (i) pod allocation still occurs at step 5, strictly after cycle detection. The parenthetical remains accurate and introduces no contradiction, so editing it is gratuitous churn against this proposal's minimal-spec-churn posture. Retying the anchor to the INSERT is an editorial-precision preference rather than a defect this build creates, and the strictly smaller change is to make no edit.
- **No change to the delegation admission stages** (validate, cycle, depth, insert) in `Service.Delegate`; the child is still committed in `StateCreated` by `insertChildSession`, and materialization runs after admission through the shared engine rather than being folded into the INSERT.
- **No new pod-claim or credential-release primitive.** The materialization reuses `claimAtCreate`, `resolveCredentialPools` plus `credrouter.PreClaim`, `startOnPod`, and both existing release primitives: `rollbackClaim` for a claim-stage failure and `rollbackBinding` for a post-launch persist failure. `poolstore` and `credrouter.PreClaim` are consumed unchanged.
- **No change to the §8.3 delegation-time availability pre-check** (`mcptools_register.go:2051-2104`, proposal 0044); it still rejects an exhausted pool before the child row is committed. The materialization adds the post-claim assignment-race path the pre-check cannot reach, without altering the pre-check.
- **No change to the F-8.2.4 exported-file stream stamping or the F-8.2.11 virtual MCP child interface** and `children_reattached` reconstruction (`start.go:3217`); both are consumed as-is.
- **No new wire field, RPC, or endpoint.** `taskHandle.State` stays a dynamic string and the `delegate_task` tool schema is unchanged. Only the returned value and its documented meaning change.
- **No dual mode or feature flag.** Delegated children materialize on the single canonical path; a nil `ChildMaterializer` only preserves the minimal in-process gateway fall-through, matching the existing nil-guard on the `CredAvailability` seam.
- **No concurrent one-of-N assignment-race test in this proposal.** The concurrent arbitration is the reused create-and-start engine (`claimAtCreate` → `credrouter.PreClaim` → the assignment-race sentinel mapped at `start.go:155-166`), already pinned deterministically at unit level and covered per-materialization by TEST-3 (case b). The concurrent one-of-N race test is the verbatim T-ADV.14 Suggested test, and T-ADV.14 assigns that coverage to the ADV battery under the skip-ADV policy. Placing it here would duplicate the ADV battery's finding and contradict DOC-1, so it is left to the ADV battery, which owns it. See §8.

## 6. Testing

The change reaches tier 0 (static), tier 1 (`MaterializeDelegatedChild` transition and typed-error mapping, in-process), tier 2 (the transition and pod claim through a real store / envtest harness exercising `startOnPod`), and tier 4 (the end-to-end `delegate_task` → running-child flow across the gateway, the delegation Service, the warm-pool store, and the credential-pool store) per `.claude/rules/test-coverage.md`. The concurrency tier (7a) for the one-of-N assignment race is owned by the ADV battery through T-ADV.14 and is unblocked by this build rather than written here (§5). The spec edit (SPEC-2) carries no runtime behavior and is covered by the tier-0 static suite plus spec-map validation. Each test below covers a non-happy path and carries a `// spec:` tie.

- **tier-1/tier-2 materialize transition (TEST-3, case a, boundary):** `MaterializeDelegatedChild` transitions a `StateCreated` row to `running` via `store.Update` with no second `store.Create` and registers the bind so `podRegistry.Get(childID)` resolves. The non-happy path is a second INSERT, a claim against an already-materialized row, or a transition that leaves the child unregistered and unreachable; case (d) guards a non-`StateCreated` row. `// spec: 8.2 (steps 5-7)`.
- **tier-1/tier-2 assignment-race fail-closed (TEST-3, case b, spec-named-failure):** a credential-assignment failure returns the assignment-race sentinel, releases the claimed pod via `rollbackClaim`, and leaves the row non-running. The non-happy path is a leaked warm pod on a losing assignment, which `spec/08_recursive-delegation.md:470` forbids. `// spec: 8.3 (line 470 post-claim assignment race)`.
- **tier-1/tier-2 pre-claim exhaustion (TEST-3, case c, error):** an empty §4.9 provider intersection returns `credrouter.ErrNoCredentialAvailable` before any pod claim. The non-happy path is a pod claimed against a pool that cannot supply a credential. `// spec: 8.2 (steps 5-7)`.
- **tier-1/tier-2 post-launch persist failure (TEST-3, case e, spec-named-failure):** a `store.Update`-to-running failure after a successful `startOnPod` releases the bound pod and its assigned lease via `rollbackBinding` and leaves the row non-running. The non-happy path is a bound, launched child pod and its credential lease leaked past a failed terminal persist, which the top-level create path prevents with `rollbackBinding` (`start.go:773-778`). `// spec: 8.2 (steps 5-7)`.
- **tier-4 materialization to running (TEST-1, spec-named-failure):** a real `delegate_task` call materializes the child to `running` with a `PodAssignment` and an assigned lease. The non-happy path is the divergence T-8.2.17 records: a child that stays in `created`, never claims a pod, and never runs. `// spec: 8.2 (steps 5-9)`.
- **tier-4 interaction requires binding (TEST-1, spec-named-failure):** task input delivered to the materialized child reaches it through `Executor.Send`. The non-happy path is an unbound child that `streamFor` rejects with `"session %s is not bound to a pod"` (`pod.go:136`). `// spec: 8.2 (step 9 parent interacts with running child)`.
- **tier-4 handler fail-closed mapping (TEST-1, spec-named-failure):** a `delegate_task` whose child materialization fails on a post-claim credential-assignment race returns the MCP tool error `CREDENTIAL_POOL_EXHAUSTED` to the parent with no warm pod leaked, and a materialization that surfaces a pool-warming or token-service sentinel returns `RUNTIME_UNAVAILABLE` or `TOKEN_SERVICE_UNAVAILABLE`. The non-happy path is the CODE-2 sentinel-to-tool-code switch producing the wrong code or leaking a pod, which `spec/08_recursive-delegation.md:470` forbids for the assignment race. `// spec: 8.3 (line 470 post-claim assignment race)`.
- **tier-4 existing-suite reconciliation (TEST-2, regression):** the existing tier-4 delegation tests that run the real dev-mode binary (`elicitation_test.go`, `delegation_adversarial_test.go`, `delegation_await_collect_test.go`) are updated to the synchronous-materialization contract so the suite stays green once CODE-4 wires the seam: delegated children are `running` after `delegate_task`, the finalize+start walk on a delegated child is dropped, and the `mode=any` sibling assertion reads `running` rather than `created`. The non-happy path this guards is a red tier-4 suite from a delegated child that is now `running` when a test still expects `created` or drives an invalid finalize. `// spec:` ties are carried unchanged from the edited tests.

## 7. Findings closed on application

This proposal closes T-8.2.17 (delegate path defers warm-pod claim and credential-lease assignment to session-start instead of allocating at delegation step 5, Medium) by building the §8.2 steps 5-9 materialization: the `MaterializeDelegatedChild` engine entry point, the delegate-handler `ChildMaterializer` seam, the gateway wiring, and the `taskHandle.State` doc-comment correction. It unblocks T-ADV.14 (post-pod-claim credential-assignment race, pod release, and one-winner/N-1 outcome, Low) by giving the delegate path a pod claim, and records T-ADV.14 as unblocked with its remaining concurrent race test left to the ADV battery. The changes are applied at spec-edit, code, and test time and need no operator hardware beyond the warm-pool and credential-pool fixtures the tier-4 delegation tests already use.

## 8. Resolved in adversarial review

Subsequent adversarial review rounds populate this section. Two challenge-round revisions are already folded into the staged changes above. First, the concurrent one-of-N delegated-child assignment-race test was removed from this proposal's changeset: it duplicates the verbatim T-ADV.14 Suggested test, which the finding assigns to the ADV battery under the skip-ADV policy, and it would contradict DOC-1, which records T-ADV.14 as unblocked rather than resolved. The reused create-and-start engine's assignment-race arbitration is already pinned deterministically at unit level and is covered per-materialization by TEST-3 case (b), so the proposal's mandate (build the §8.2 materialization, close T-8.2.17, unblock T-ADV.14) is served by CODE-1..4 plus TEST-1 and TEST-3 without it. Second, the §8.2 step-2a cycle-detection parenthetical retie (a candidate SPEC-1) was dropped and recorded as a Non-goal, because both clauses of `spec/08_recursive-delegation.md:64` remain true under option (i) and the change falsifies neither, so editing untouched, correct spec prose is gratuitous churn.

### Pass 1 (2026-07-19, automated)

- **`taskHandle.State` serialized `created` rather than `running`.** The `ChildMaterializer.Materialize` seam and the `MaterializeDelegatedChild` entry point returned only `error`, so the handler built the handle from the by-value `res.Child.State` snapshot captured at `Delegate` time in `StateCreated` (`service.go:186-187`, `mcptools_register.go:2602-2607`), which is never re-read. The signature now returns `(session.State, error)`; the handler captures the returned post-materialization state into a `childState` variable (defaulting to the pre-materialization snapshot for the nil-seam fall-through) and builds `taskHandle.State` from it. CODE-1, CODE-2, CODE-3, §3, and the files-touched entry are reconciled to the returning-state mechanism, and the stale claim that `State` stays `string(res.Child.State)` is removed.
- **A post-launch persist failure leaked a bound pod and its credential lease.** The proposal named only `rollbackClaim`, which releases a claim-stage pod. The reused engine releases a bound, launched pod through `rollbackBinding` when the terminal persist fails after a successful `startOnPod` (`start.go:773-778`, `rollbackBinding` at `:2666`). The materialization's terminal `store.Update`-to-running is the exact analog. CODE-1, the Decisions and Non-goal entries, §3, §9, and the files-touched entry now name both release primitives, and TEST-3 gains case (e) asserting the bound pod and lease are released on a post-launch persist fault with the row left non-running.
- **No test pinned the handler's sentinel-to-tool-code fail-closed mapping.** The listed tests covered only the engine-level rollback (TEST-3 case b, package `sessionserver`) and the happy path (TEST-1), leaving the CODE-2 switch that maps engine sentinels to `CREDENTIAL_POOL_EXHAUSTED`, `USER_CREDENTIAL_NOT_FOUND`, `RUNTIME_UNAVAILABLE`, and `TOKEN_SERVICE_UNAVAILABLE` untested at the tier the change reaches. TEST-1 gains a tier-4 case that drives a `delegate_task` to a post-claim credential-assignment race and asserts the parent receives `CREDENTIAL_POOL_EXHAUSTED` with no pod leaked, plus a case exercising a newly surfaced `RUNTIME_UNAVAILABLE` or `TOKEN_SERVICE_UNAVAILABLE` mapping. §6 and the files-touched entry are updated to match.

### Pass 2 (2026-07-19, automated)

- **The composition omitted `registerBinding`, so the materialized child stayed unreachable.** The enumerated sequence stopped at `store.Update`-to-running and never published the `startOnPod` `BindResult` into the shared executor registry. `startOnPod` returns a `*podsession.BindResult` but does not register it (`start.go:1938`); the top-level create path registers it as a separate step via `registerBinding` (`start.go:788`), which calls `podRegistry.Put` (`start.go:2501`) and persists the pod assignment and workspace root (`:2502`, `:2509`). `PodExecutor.streamFor` resolves the session through that same shared `*podsession.Registry` (`pod.go:134-136`; constructed at `cmd/lenny-gateway/stores.go:1985`, handed to the executor at `:2091`, passed as `PodRegistry` at `sessionsrv.go:223`), so without `registerBinding` a literal implementation left the child's bind absent from the registry and `Executor.Send` still failed with "is not bound to a pod", making the fix inert. CODE-1 (docstring and step list), the §2 Decisions reuse bullet, the §3 walkthrough, and the files-touched entry now name `registerBinding` (plus `recordSessionCreated` and `registerLeaseTree`) in the composition, place `rollbackBinding` before `registerBinding` on the failed terminal persist so no registry entry leaks, and TEST-3 case (a) and its §6 bullet now assert `podRegistry.Get(childID)` resolves the materialized child.
- **The §1 background misattributed the post-pod-claim assignment race to the pre-claim sentinel.** The problem statement said the race was "already mapped at `start.go:155` (`credrouter.ErrNoCredentialAvailable`)", but `start.go:155` is the pre-claim exhaustion branch where no pod is claimed (it emits `pre_claim`); the post-pod-claim race is the distinct `errors.As(err, &credAssign)` branch at `start.go:159-166` (it emits `assignment_race`). Line 19 now cites `start.go:159-166` and the `credAssign` typed error for the race and reserves `start.go:155` for the no-pod-claimed pre-claim case.
- **Spec line 470 (the assignment-race clause) was cited as §8.2 in three places while §8.3 elsewhere.** Line 470 falls inside §8.3 (Delegation Policy and Lease, `spec/08_recursive-delegation.md:131-516`); §8.2 ends at line 130. The §1 prose (line 21), the TEST-1 label and `// spec:` annotation, and the §6 test-list annotation now read §8.3, matching the spec structure and the established `// spec: §8.3 line 470` convention (`start_preclaim_internal_test.go`, `mcptools_register.go:2077`), so the assignment-race tests map to the section that defines the behavior.

### Pass 3 (2026-07-19, automated)

- **The proposal broke the existing tier-4 delegation suite and omitted those tests from the edit and test lists.** CODE-4 sets `ChildMaterializer: sessionSrv` on the same `mcptools.Register` deps the dev-mode binary constructs (`cmd/lenny-gateway/mcpsurface.go:224-243`), which the existing tier-4 delegation tests exercise through `gateway.StartWith(t, "--dev-mode")` (`tests/testinfra/gateway/gateway.go`). Under synchronous materialization (option (i)) a delegated child returns `running` rather than `created`, so the shared `startSession` finalize+start walk on a delegated child fails at `handleFinalize`'s non-`created` precondition rejection (`sessionserver.go:3036-3042`, reported by `elicitation_test.go:152-154` via `t.Fatalf`), and the `mode=any` sibling assertion of `created` (`delegation_await_collect_test.go:108`) no longer holds. The prior §10 and §6 lists named only the new test files. A new build item TEST-2 now reconciles `elicitation_test.go`, `delegation_adversarial_test.go`, and `delegation_await_collect_test.go` (correct the shared `startSession`/`delegateChild` "lands in the created state" doc comments; drop the finalize+start walk on delegated children at `elicitation_test.go:174` and `delegation_adversarial_test.go:152,154,204`; change the `mode=any` sibling assertion at `delegation_await_collect_test.go:108` from `created` to `running`), states the post-delegation child state as `running` (task input is never delivered by `delegateChild`, so `Executor.Send` at `mcptools_register.go:2594` is skipped and the child stays at `running`), and confirms `cross_environment_delegation_test.go` (direct-deps nil-seam wiring) and `delegation_test.go` are unaffected. §6 gains a matching reconciliation bullet and §10 lists the three edited test files.

## 9. Open decisions for review

### Materialization timing — option (i) synchronous versus option (ii) deferred trigger

Option (i) is recommended: materialize synchronously within `delegate_task` so §8.2 steps 5-9 run inline, the tool returns the `running` child, and spec churn is confined to the SPEC-2 created-state qualification. Option (ii) returns the child after admission (state `submitted`) and materializes it via a separate post-admission trigger, keeping allocation textually deferred and requiring §8.2 flow rewording. This is a reviewer decision because it changes the `delegate_task` blocking semantics — option (i) blocks the parent for the child's full startup (pod claim, workspace stream, launch) — and the externally observable child state at response time.

### Whether the implementation touches a shared warm-pool claim or release primitive

A pure-reuse build (consuming `claimAtCreate`, `startOnPod`, `rollbackClaim`, and `rollbackBinding` as-is) needs no Integrator serialization. If the loser-pod-release-on-assignment-race path or the post-launch persist-failure release requires modifying a shared release primitive rather than reusing `rollbackClaim` and `rollbackBinding` unchanged, flag the Integrator inbox to serialize C-42 and C-22 (§4.6 eviction) before pushing, per `PROPOSAL-QUEUE.md` and `git-workflow.md`. The reviewer confirms whether the implementer must touch a shared primitive.

## 10. Files touched on application

- `spec/08_recursive-delegation.md`: SPEC-2 (qualify the §8.8 `created`-state note at `:879`).
- `spec/15_external-api-surface.md`: SPEC-2 (qualify the §15.1 `created`-state note at `:630`).
- `pkg/gateway/sessionserver/start.go`: CODE-1 (add `MaterializeDelegatedChild` composing `claimAtCreate` at `:1754`, `resolveCredentialPools` at `:1362`, `startOnPod` at `:1938`, `registerBinding` at `:788` to publish the bind into the shared `podRegistry`, `rollbackClaim` at `:2742`, and `rollbackBinding` at `:2666`; the method returns the child's post-materialization `session.State`).
- `pkg/gateway/mcpfabric/mcptools/mcptools.go`: CODE-2 (declare the `ChildMaterializer` interface and add the deps field next to `CredAvailability` at `:343`), CODE-3 (correct the `taskHandle.State` doc comment at `:923-928`).
- `pkg/gateway/mcpfabric/mcptools/mcptools_register.go`: CODE-2 (drive materialization in the `delegate_task` handler between `:2278` and `:2595`, mapping typed sentinels to MCP tool codes).
- `cmd/lenny-gateway/mcpsurface.go`: CODE-4 (set `ChildMaterializer: sessionSrv` alongside `CredAvailability` at `:239`).
- `tests/tier4_integration/delegation_child_materialization_test.go`: TEST-1 (materialization to running, parent interaction, and the handler fail-closed sentinel-to-tool-code mapping, new file).
- `tests/tier4_integration/elicitation_test.go`, `tests/tier4_integration/delegation_adversarial_test.go`, `tests/tier4_integration/delegation_await_collect_test.go`: TEST-2 (reconcile the pre-materialization `created`-state contract these dev-mode tests encode — correct the shared `startSession`/`delegateChild` doc comments, drop the now-invalid finalize+start walk on delegated children at `elicitation_test.go:174` and `delegation_adversarial_test.go:152,154,204`, and change the `mode=any` sibling assertion at `delegation_await_collect_test.go:108` from `created` to `running`). `cross_environment_delegation_test.go` (direct-deps nil-seam wiring) and `delegation_test.go` are confirmed unaffected and left unchanged.
- `pkg/gateway/sessionserver` (unit/component test): TEST-3 (`MaterializeDelegatedChild` transition, assignment-race fail-closed, pre-claim exhaustion, non-created guard, and post-launch persist-failure `rollbackBinding`).
- `TEST-GAPS.md`: DOC-1 (mark T-8.2.17 resolved and record T-ADV.14 unblocked).
</content>
</invoke>
