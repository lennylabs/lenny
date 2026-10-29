# Deviations — 0107_fix_the-delegation-tree-s-budget-lifecycle-is-under-specified

The implementor owns this file, and it stays empty until an implementation records a departure from what the proposal states.

## Proposed: BUDGET_KEYS_EXPIRED root write calls OnSessionTerminal inline

**Status:** resolved: step S15 (CODE-3) made `OnSessionTerminal` in `pkg/gateway/sessionserver/usage.go` open its own terminal claim and run the pipeline on a goroutine over a detached context, so the inline call in `failBudgetKeysExpired` now returns without waiting for the hook, as CODE-2 item 2 states (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S11 (CODE-2: `BUDGET_KEYS_EXPIRED` cleanup handler and extracted cancel helper).

**What the proposal says.** CODE-2 item 2 and CODE-3 item 6 (hook dispatch) state that the `BUDGET_KEYS_EXPIRED` root write reaches the hook through `OnSessionTerminal`, which is "detached, by that row", and that the claim ends when `OnSessionTerminal` returns.

**What landed instead.** `failBudgetKeysExpired` in `pkg/gateway/sessionserver/budget_keys_expired.go` calls `s.OnSessionTerminal` inline and ends its claim in a deferred call when that call returns. `OnSessionTerminal` is still synchronous in the tree at this step.

**Why.** The detached dispatch belongs to `OnSessionTerminal` itself, and CODE-3, a later step, changes it. This writer already holds its own claim and ends it when `OnSessionTerminal` returns, so it becomes detached when CODE-3 lands without a change to this file.

**What a later reader would otherwise get wrong.** A reader of the tree between S11 and CODE-3 would assume the hook runs detached from the root write. Until CODE-3 lands, the hook runs synchronously on the handler's goroutine.

## Proposed: carrier of the delegation.budget_keys_expired event

**Status:** accepted: §8.3 names a critical structured event without a catalogue entry, the `slog.Error` line with `event=delegation.budget_keys_expired` is that event, and the optional operational event adds a countable channel without altering the §16.6 catalogue transcription (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S11.

**What the proposal says.** CODE-2 item 3(a) states that the applied writer "emits delegation.budget_keys_expired with root_session_id and tenant_id". It does not name the carrier.

**What landed instead.** `emitBudgetKeysExpired` in `budget_keys_expired.go` writes a `slog.Error` structured log line with `event=delegation.budget_keys_expired`, `root_session_id`, and `tenant_id`. When an `OpsEmitter` is wired, it also emits an operational event of type `dev.lenny.delegation.budget_keys_expired` with severity `critical`. The type is a constant local to `sessionserver` and is absent from the §16.6 events catalogue.

**Why.** §8.3 calls the event a "critical structured event", and §16.6 does not list it. Adding it to the `pkg/events` catalogue would break the test that checks the catalogue transcribes §16.6. The operational event gives tests and operations agents a countable channel.

**What a later reader would otherwise get wrong.** A reader searching `pkg/events` or §16.6 for this event type would conclude it is never emitted. It is emitted as a structured log line and, when wired, as an operational event whose type is defined in `pkg/gateway/sessionserver`.

## Proposed: concurrent-detection tests omit budget returns

**Status:** resolved: step S15 added the budget return, and `tests/tier7a_load_local/budget_keys_expired_cleanup_race_test.go` now runs each member's return against the production return script on miniredis through `orphanfixture.NewReturnCounter`, asserting one return per member and no partial keys recreated (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S11.

**What the proposal says.** Testing item 2 states that the concurrent-detection case runs "with the sweep's own cancels running their returns".

**What landed instead.** The tier 1 and tier 7a concurrent cases check one applied root write, one event, and one metric increment per detection, at most one write and one hook per member, and every member terminal. They do not check budget returns.

**Why.** The budget return in `afterTerminalWrite` arrives in a later step (CODE-3, together with CODE-1's script). Until those steps land, the cancels have no return to run.

**What a later reader would otherwise get wrong.** A reader would take the S11 concurrent tests as covering the interaction between the sweep and the budget return. That coverage is absent at S11 and is owed by the step that adds the return.

## Proposed: metric pool label when the root row cannot be read

**Status:** accepted: §16.1 counts every detection, so an increment under an empty pool label is the only way to keep a detection whose root read failed visible to the alert (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S11.

**What the proposal says.** CODE-2 item 1 states that the metric's pool is the root row's pool. It does not say what happens when the root row cannot be read.

**What landed instead.** `countBudgetKeysExpired` reads the root with `store.Get`. If the read fails, it counts the detection under an empty pool label.

**Why.** The detection happened regardless of the read result, and §16.1 states that the counter increments on each detection. Dropping the increment would hide the alert.

**What a later reader would otherwise get wrong.** A reader would expect every increment of the counter to carry a pool label. An increment with an empty pool label records a detection whose root row read failed.

## Proposed: handler detaches its context before the root write and sweep

**Status:** accepted: the root write and the sweep are gateway-decided terminal writes, and `failFinalizing` and `failStarting` already detach for the same reason (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S11.

**What the proposal says.** CODE-2 does not state which context the handler runs on.

**What landed instead.** `HandleBudgetKeysExpired` wraps its context with `context.WithoutCancel` before the root write and the sweep.

**Why.** The root write and the sweep are terminal writes that the gateway decides, and `failFinalizing` and `failStarting` detach their contexts for the same reason. A `delegate_task` request cancelled partway through must leave the tree fully swept.

**What a later reader would otherwise get wrong.** A reader would assume that cancelling the `delegate_task` request stops the cleanup. The root write and the sweep run to completion after the request context is cancelled.

## Proposed: delegate_task expired-keys parity compared against a REST reference triple

**Status:** accepted: `lenny/delegate_task` has no REST route, so the test pins the MCP triple to `errorclassify.Classify` and to the REST triple of the same error code, which is the parity the tree can express (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S13 (CODE-1 and CODE-4: scripts and `delegate_task` side).

**What the proposal says.** Testing item 4 (non-spec changes) asks for a tier 3 case that pins REST and MCP parity for the `delegate_task` expired-keys error. The step brief asks for that parity in the (code, category, retryable) form that `consistency_test.go` uses.

**What landed instead.** `lenny/delegate_task` is MCP-only and has no REST route. `tests/tier3_contract/rest_mcp_consistency/delegate_budget_error_parity_test.go` therefore compares the MCP triple against two references: `errorclassify.Classify`, and a REST `INVALID_STATE_TRANSITION` triple produced by `POST /interrupt` on an `input_required` session (`newParitySurface`). `DELEGATION_BUDGET_UNAVAILABLE` is pinned against `errorclassify` only.

**Why.** No REST surface performs delegation, so a parity check on the same operation cannot be built. The closest available check compares the shared error code's wire triple on both surfaces.

**What a later reader would otherwise get wrong.** A reader would take the test as proof that a REST delegation route returns the same triple as MCP. No such route exists. The test pins the MCP triple to the code's classification and to the triple a different REST operation returns for the same code, and `DELEGATION_BUDGET_UNAVAILABLE` has no REST comparison at all.

## Proposed: BUDGET_KEYS_EXPIRED mapping carries a details reason

**Status:** resolved: commit `7958f098b` removed `details.reason`, so `mcptools_register.go` maps `delegation.ErrBudgetKeysExpired` to `INVALID_STATE_TRANSITION` with no details, as CODE-4 item 1 states, and the unit mapping test and the tier 3 parity test assert that the envelope carries no details object (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S13.

**What the proposal says.** CODE-4 item 1 states that a second `ErrBudgetKeysExpired` returns `delegation.ErrBudgetKeysExpired`, which the handler maps to `INVALID_STATE_TRANSITION` as it maps `errSessionTerminalState`. `errSessionTerminalState` carries no details.

**What landed instead.** `pkg/gateway/mcpfabric/mcptools/mcptools_register.go` maps it to `INVALID_STATE_TRANSITION` with details `{"reason": "BUDGET_KEYS_EXPIRED"}`.

**Why.** The details field lets a client distinguish this cause from other `INVALID_STATE_TRANSITION` causes. The code, the category, and the retryable flag are unchanged.

**What a later reader would otherwise get wrong.** A reader would expect this error to be byte-identical to the `errSessionTerminalState` mapping. The response carries a `details.reason` of `BUDGET_KEYS_EXPIRED` that the terminal-state error does not carry.

## Proposed: tree grant reader source when the GatewayControl listener is off

**Status:** accepted: §8.3 requires the reservation to read the `delegation_tree_budget` row whenever the base cap is finite, and one `newLeaseDenialStore` instance backing both the budget source and `treeGrantReader` keeps the recorded grant and the read cap one value (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S13.

**What the proposal says.** CODE-4 Targets states that the grant reader is wired into `delegation.Options` in `mcpsurface.go`. The proposal does not say where the reader comes from when the GatewayControl listener is off, in which case `leaseBudgets` is nil because `--grpc-addr` is unset.

**What landed instead.** `cmd/lenny-gateway/lease_budget_source.go` adds `newLeaseDenialStore`, which returns `denialpg` when Postgres is configured and `MemoryDenialStore` otherwise. The store is built unconditionally and kept as `w.leaseDenials`. That single instance backs both the `StoreBudgetSource` and the `treeGrantReader` adapter passed as `delegation.Options.TreeGrants`. A nil `TreeGrants` in `delegation.Service` reads no grant.

**Why.** §8.3 requires the reservation to read the `delegation_tree_budget` row whenever the base cap is finite, including when the listener is off. Sharing one store instance means a grant the source records is the value the cap reads.

**What a later reader would otherwise get wrong.** A reader would assume the grant reader exists only when the GatewayControl listener is enabled, or that the budget source and the grant reader use separate stores. The denial store is built on every gateway start, without Postgres it is in-memory, and one instance serves both consumers.

## Proposed: lease elicitor moved out of cmd/lenny-gateway into an exported package

**Status:** accepted: tier 3 and tier 4 cases must run the shipped elicitor, package `main` cannot be imported from `tests/`, and `code-best-practices.md` keeps reusable logic out of `cmd/`; the proposal text is a historical record and is not corrected (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S16.

**What the proposal says.** CODE-7 Targets name `cmd/lenny-gateway/lease_elicit.go` and `cmd/lenny-gateway/lease_elicit_test.go` as the elicitor files to change.

**What landed instead.** The elicitor moved with `git mv` to `pkg/gateway/mcpfabric/delegationtree/leasecontrol/leaseelicit/leaseelicit.go` as the exported `leaseelicit.Elicitor`, built through `leaseelicit.New(Config)`. Its reason constants are exported as `ReasonRequesterTerminal` and `ReasonTimeout`. `cmd/lenny-gateway/controlserver.go` builds the elicitor through `leaseelicit.New`.

**Why.** The step brief requires a tier 4 case in `tests/tier4_integration` and a tier 3 case that run the production elicitor against real stores. The integration tier runs only `./tests/tier4_integration/...`, and that directory cannot import package `main`. A test-local copy of the elicitor would not test the shipped code. The code-best-practices rule also keeps reusable logic out of `cmd/`.

**What a later reader would otherwise get wrong.** A reader following the CODE-7 Targets would look for the elicitor in `cmd/lenny-gateway/lease_elicit.go`, which no longer exists. The elicitor and its tests live in the `leaseelicit` package, and its reason constants are exported identifiers.

## Proposed: requester-terminal dismissal asserted on the stored interaction record

**Status:** accepted: the specification defines no client-facing field for an elicitation's dismissal reason, so the stored §9.2 record and the `INTERACTION_ALREADY_RESOLVED` refusal are the observable surfaces, and no unspecified wire field was added (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S16.

**What the proposal says.** The step brief, rather than the proposal, asks the tier 4 case to assert that "the client-visible interaction carries that reason (read through the existing interactions/elicitation surface served by mcptools_register.go's PhaseDismissed path)". It asks a tier 3 case to pin `LEASE_EXTENSION_REQUESTER_TERMINAL` "as a dismissal reason value in the client-facing interaction response".

**What landed instead.** No client-facing response in the tree carries an interaction's dismissal reason. The `PhaseDismissed` branch in `mcptools_register.go` serves `lenny/request_elicitation`, which is the runtime's blocking call. It returns `{elicitationId, dismissed:true}` with no reason, and a lease elicitation is never raised through it. The REST and MCP respond and dismiss endpoints return only `{id, phase, resolvedAt}`, or `INTERACTION_ALREADY_RESOLVED` for a resolved interaction. The tier 3 and tier 4 cases therefore assert three things. The stored §9.2 interaction record is `PhaseDismissed` with reason `LEASE_EXTENSION_REQUESTER_TERMINAL`, read from the Postgres interaction store at tier 4. A client respond through `mcptools.ResolveElicitation` is refused with `ErrAlreadyResolved` (`INTERACTION_ALREADY_RESOLVED`). The `elicitation_request` payload keys are unchanged, which the tier 3 case pins. No new wire field was added.

**Why.** The specification defines no client-facing field that carries an elicitation's dismissal reason. Adding one would be an unspecified wire change, which `spec-driven-development.md` forbids without a proposal.

**What a later reader would otherwise get wrong.** A reader of the step brief would expect a client-facing response to expose `LEASE_EXTENSION_REQUESTER_TERMINAL`. The reason exists only on the stored interaction record, and a client observes the dismissal as an `INTERACTION_ALREADY_RESOLVED` refusal.

## Proposed: requester-terminal dismissal runs only while the interaction is pending

**Status:** accepted: §8.6 elicitation flow item 2 dismisses only before the user decides, and `ApplyGrant`'s terminal check refuses any grant a decision recorded after the requester ended leads to (2026-10-29, orchestrator under the owner's delegation)

**Reported by:** step S16.

**What the proposal says.** CODE-7 item 1 states that on a poll tick, a terminal requester row or `ErrNotFound` sets the interaction to `PhaseDismissed` with `LEASE_EXTENSION_REQUESTER_TERMINAL` and returns `ErrRequesterEnded`.

**What landed instead.** In `dismissForEndedRequester` in `leaseelicit.go`, the dismissal runs only while the interaction is still pending. If the user recorded a decision between the poll and the dismissal, the elicitor returns that decision, and `ApplyGrant`'s terminal check then refuses any grant the decision leads to. A failed dismissal write still returns `ErrRequesterEnded`, wrapped with the store error. Each poll checks the interaction's own phase before it re-reads the requester.

**Why.** The proposal leaves open the case in which the requester ends while the user is deciding. §8.6 step 2 dismisses the elicitation only "before the user decides". Overwriting a recorded decision would contradict that rule, and the terminal requester still receives no grant.

**What a later reader would otherwise get wrong.** A reader would expect every terminal-requester poll tick to end in `PhaseDismissed` and `ErrRequesterEnded`. An interaction the user has already decided keeps its decision and returns it, a grant from that decision is refused at `ApplyGrant`, and a failed dismissal write surfaces as a wrapped `ErrRequesterEnded`.
