# Proposal: Bind a platform-tool call to the session that made it

- **Status:** **Approved (2026-08-16) by jaf sign-off.** Verified (2026-08-16). Converged after 3
  adversarial review rounds (1 finding fixed) following the scope reduction, and after 10 rounds
  (22 findings fixed) on the text that preceded it. The proposal is reduced in scope: the resume
  correction the review reshaped CODE-2
  into is withdrawn and handed to proposal 0073, which builds the slot-aware resume that correction
  assumed absent. §7 records the handoff and §9 the pass history.
- **Date:** 2026-08-13
- **Scope:** States three defects found while reviewing proposal 0069, each one a
  security-relevant decision that reads a value its decider never verified. A platform-tool handler takes
  the session it writes from the caller's own arguments; the principal those arguments are checked against
  is itself derived from an unverified request field; and a fail-closed gate reads a field one of its
  callers never populates, so it admits a resumed session onto a pod that may host other sessions' slots.
  This document stages the fix for the first, which is live and has no recovery path. §5 states what a fix
  for the second must satisfy, and §7 records that proposal 0073 corrects the third. None is caused by 0069
  and none should wait behind it.

This document stages the proposed code and specification changes. It does not modify any spec, code, or
doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 0. Context an implementor should read first

The three defects sit at three different depths of one question: what proves that a caller is the session
it says it is. All three are stated here so that the correction of the shallowest is not read as closing
that question, which it does not. Only §1.1 is corrected in this document. §1.2 is scoped for a later
proposal under decision 4, §1.3 is handed to proposal 0073 under decision 3, and §6 states what lands.

## 1. Problem

### 1.1 The tracing handler writes the session its caller names

`registerTracingTool` reads `in.SessionID` out of the tool arguments and passes it to `Store.Get` and
`Store.Update` (`pkg/gateway/mcpfabric/mcptools/mcptools_register.go:945`). Only the tenant is resolved
from the principal, through `callerTenantID` (`:933`). The handler never compares the session it is about
to write against the session the caller is.

It is the outlier among its neighbours. Eleven sibling handlers resolve through
`callerSessionID(ctx, fallback)` (`pkg/gateway/mcpfabric/mcptools/mcptools.go:1801`), which prefers the
principal's `SessionID` and falls back to the argument only when the principal carries none:
`mcptools_register.go:293, 658, 721, 1025, 1111, 1336, 1759` and `mcptools.go:846, 1158, 1214`.

The principal on this path is trustworthy, which is what makes the omission a defect rather than a
limitation. `platformToolProvider.Call` forwards the runtime's arguments verbatim but attaches
`p.sessionID` itself (`pkg/adapter/platformtoolprovider.go:37`), bound from the adapter's own
`s.sessionID` (`pkg/adapter/platformmcp.go:40-43`), which only the gateway sets at StartSession
(`pkg/adapter/session.go:89, 368`). The MCP wire between runtime and adapter carries no session field at
all (`pkg/adapter/mcp/server.go`). A runtime therefore cannot influence the principal. It can influence
only the arguments, and the arguments are what the handler reads.

The tree already names this threat and defends the other path. On the JSONL leg,
`handleSetTracingContext` injects the adapter's bound session into the forwarded arguments
(`pkg/adapter/tracingcontext.go:98-101`) and its doc comment says why
(`pkg/adapter/tracingcontext.go:70-72`): "a runtime cannot register tracing context against a session it
does not own."

**Reachability.** No concurrency is required and no integration level gates it. The platform MCP manifest,
its socket, and its nonce are written unconditionally
(`pkg/adapter/manifest.go:239-256`), and integration level is observed rather than enforced. Sibling
session ids are discoverable from inside the pod: `treeVisibility` defaults to `full`
(`pkg/gateway/mcpfabric/delegation/tree_visibility_test.go:74`), `lenny/get_task_tree` returns sibling
task ids, and a task id is its session id. The write is constrained to the caller's own tenant, because
the tenant does come from the principal.

**Consequence.** `pkg/delegation/tracing` exposes `Validate` and `Merge` and no delete
(`pkg/delegation/tracing/tracing.go:62, 110`). `Merge` never overwrites an existing key, and no operator
surface removes one. So a write to another session is permanent and has two effects worth naming
separately. A key the victim later registers itself is silently accepted and ignored, so the victim's
traces and every descendant's stitch under a value the caller chose. And `MaxEntries` is 32
(`tracing.go:24-25, 63`): filling it makes every later registration on that session, and on every child it
delegates to, fail permanently. No audit event records any of it.

The same-tenant constraint applies and does not suffice on its own. §8.3 grants write authority over a
session's tracing context to that session's runtime, and `spec/04_system-components.md:951` requires a
pod's local MCP servers never to expose other sessions. The write violates the actor model those state.

### 1.2 The principal is only as good as the field it is built from

`GatewayControl.CallPlatformTool` builds the caller's principal from `req.session_id`
(`pkg/gateway/gatewaycontrol/platformtools/platformtools.go:118-123`), and nothing binds that field to the
peer that sent it. The listener's mTLS verifier is configured with a trust domain and a deny list and no
per-pod expectation (`cmd/lenny-gateway/main.go:651-656`), so a certificate proves the peer is *an* agent
pod in the trust domain and says nothing about *which session* it serves.

The tree documents this plainly at
`pkg/gateway/mcpfabric/delegationtree/leasecontrol/auth.go:29-33`: each handler "resolves the caller's
tenant from the session_id in the request body and has no other proof of identity, so an unauthenticated
peer must never reach it."

That comment describes the interceptor's job accurately, and the interceptor does it: it fails closed on
an unverified peer. What it cannot do is distinguish two verified peers. Every session's adapter presents
a certificate the verifier accepts, so any of them can name any session id.

§1.1's fix is necessary without being sufficient. Routing the tracing handler through
`callerSessionID` makes it read the principal, and on the adapter path the principal is the adapter's own
bound session, so the fix holds there. It holds because of a property of the adapter rather than because
of a property the gateway verified. A compromised or buggy adapter, or any process reaching the port with
a trust-domain certificate, still names what it likes, for every platform tool rather than this one alone.

**Reachability is lower than §1.1 and should not be overstated.** Reaching the GatewayControl port
requires the adapter's key material and service-account token, which are UID-separated from the runtime
(`pkg/adapter/peercred_linux.go:20-30`). This is a missing defence in depth rather than a live path from
an agent runtime.

### 1.3 A fail-closed gate reads a field one caller never sets

`PodExecutor.streamFor` refuses to open an Attach stream for a concurrent-pool session that resolved no
slot (`pkg/gateway/session/executor/pod.go:147`):

    if bind.MaxConcurrentSessions > 1 && bind.SlotID == "" { return nil, ErrSlotIDRequired }

The gate exists because such a stream reads the pod's shared runtime output unfiltered
(`pkg/adapter/attach.go:68-73` leaves `out := rawOut` when the slot id is empty), so it would observe
every slot's frames.

`Binder.Resume` returns a `BindResult` carrying `SessionID`, `TenantID`, `SandboxName`, `PodIP`, `Adapter`,
and `WorkspaceRoot`, and neither `SlotID` nor `MaxConcurrentSessions`
(`pkg/gateway/podlifecycle/podsession/binder.go:1608-1616`). On that path the gate evaluates `0 > 1` and
does nothing.

The gate is correct and the struct literal is incomplete. The invariant lives in the gap between them, and
a zero value is indistinguishable from a deliberate 1.

**The gate is defeated on the resume path, and the resumed bind is not exclusive.** `Resume` claims a
whole pod and resolves no slot (`pkg/gateway/podlifecycle/podsession/binder.go:1652-1670`), and a whole-pod
claim does not exclude slot placement on the same pod. `Claimer.Claim` creates the per-pod occupancy
`SandboxClaim` carrying only `sandboxRef` and `tenantId`
(`pkg/gateway/podlifecycle/podclaim/claimer.go:294-298`) and reserves no §5.2 slot counter. `ClaimSlot`
Pass 1 lands a slot on any pod that holds a live, non-terminal per-pod claim pinned to the same tenant and
has free counter capacity (`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:411-460`), and it reads no
marker separating a whole-pod claim from a slot-hosting one, because the claim record carries none. A pod a
resume holds presents exactly that. The adapter does not separate them either: `startSessionSlot` checks
only the per-slot state (`pkg/adapter/slotsession.go:40-43`) while `Resume` claims the pod-global session
(`pkg/adapter/resume.go:47`), so the resumed session's slot-less Attach takes the unfiltered branch
`out := rawOut` (`pkg/adapter/attach.go:70-72`) and observes every co-located slot's frames. That is the
misdelivery the gate's own comment names (`pkg/gateway/session/executor/pod.go:140-147`).

Concurrent-workspace pools reach this path. Checkpoints on such a pool are taken and recorded per slot
(`pkg/gateway/checkpoint/checkpointer/checkpointer.go:444`, with the per-(session, slot) retention at
`:471`), which §5.2 states is the defined behaviour for `maxConcurrentSessions > 1`; `resumeOnPod` branches
into `Binder.Resume` on the presence of `WorkspaceSnapshot.Ref` alone, with no test of the pool's
concurrency (`pkg/gateway/sessionserver/start.go:3836, 3898`); and it publishes the returned bind directly
(`:3936`). On a concurrent pool the unset zero therefore produces the answer the gate exists to refuse.

## 2. Decisions

1. **A handler resolves the session it writes from the principal rather than from its arguments.**
   §1.1's handler joins the eleven that already do. The fallback in `callerSessionID` is retained rather
   than bypassed, because the external `/mcp` edge relies on it and the siblings depend on that behaviour.

2. **The argument is removed from the tool's schema rather than ignored.** The spec signature is
   `lenny/set_tracing_context(context)` with no session parameter (`spec/08_recursive-delegation.md:540`),
   and the sibling schemas document theirs as a transport fallback the principal overrides. This handler
   alone marks `sessionId` required. Leaving a required argument the handler ignores invites a caller to
   believe it means something.

3. **§1.3 is stated and handed to proposal 0073 rather than corrected here.** The gate's predicate is
   correct as it stands and is left alone. Zero is the ordinary value of `MaxConcurrentSessions` on the
   exclusive whole-pod path rather than a missing value: the whole-pod `Bind` return literal
   (`pkg/gateway/podlifecycle/podsession/binder.go:1021-1035`), the gateway's re-adopt literal
   (`cmd/lenny-gateway/coordination_seams.go:242-248`), and `foldPoolPolicy` for a pool with no §5.2
   policy mirror row (`pkg/gateway/podlifecycle/podsession/resolve.go:359-369`) all leave it zero, and
   `Registry.Put` normalizes nothing (`pkg/gateway/podlifecycle/podsession/registry.go:25-29`). Refusing a
   zero at the gate would refuse the first `Send` or Attach on every exclusive session and every
   re-adopted session. §5.2's minimum of 1 is a CEL rule on the CRD field
   (`spec/04_system-components.md:426`) and says nothing about this Go struct, whose zero value records
   the absence of a gateway-side mirror.

   What remains is that `Binder.Resume` reports neither the concurrency nor a slot, so on the resume path
   the gate evaluates `0 > 1` and never fires. Correcting that means either forbidding the resume or
   giving it a slot, and 0073 gives it a slot. §7 states the handoff and what 0073 must carry.

4. **The unverified `session_id` is stated, scoped, and not patched here.** Binding a platform-tool call to
   its peer's mesh identity requires a pod-to-session mapping the gateway does not currently consult, and
   choosing where that mapping lives is a design question rather than a correction. §5 states what a
   solution must satisfy so the next proposal does not restate the analysis.

5. **CODE-1 and SPEC-1 land together as one revertible change, and are the whole of what this document
   applies.** §6 states it. §1.2 and §1.3 are stated here and corrected elsewhere, under decisions 4 and 3.

## 3. Proposed changes

### CODE-1. Resolve the tracing session from the principal

In `pkg/gateway/mcpfabric/mcptools/mcptools_register.go`, `registerTracingTool` resolves
`sessionID := callerSessionID(ctx, in.SessionID)` before the store lookup at `:945` and uses it for the
`Store.Get`, the terminal-state check, the `Store.Update`, and the response body.

The argument-level `in.SessionID == ""` validation at `:941-944` is replaced by the siblings'
post-resolution guard rather than deleted. `callerSessionID` resolves to the empty string when the
principal carries no `SessionID` and the caller passed no `sessionId` argument, which is reachable on the
gateway edge that decision 1 keeps the fallback for. Without a guard the handler would call
`deps.Store.Get(ctx, tenant, "")` and reclassify a missing session from `VALIDATION_ERROR` to a
session-lookup failure. The replacement is the form the eleven siblings already carry
(`mcptools_register.go:657-663, 1025-1029, 1110-1114` and `mcptools.go:1161, 1217`):

    sessionID := callerSessionID(ctx, in.SessionID)
    if sessionID == "" {
        return mcp.ToolResult{}, mcp.NewToolError("VALIDATION_ERROR",
            "caller session is unbound (no principal SessionID, no sessionId arg)", nil)
    }

A caller with no principal session and no `sessionId` argument therefore still receives a
`VALIDATION_ERROR`, with the siblings' message in place of "sessionId is required".

The tool's input schema drops `sessionId` from `required` and documents it as a transport fallback the
principal overrides, matching the wording its siblings already carry.

### SPEC-1. State the binding rule §1.1 breaks

`spec/08_recursive-delegation.md` §8.3 states the rule CODE-1 makes true, qualified to the caller it holds
for: the gateway resolves the session a tracing registration writes from the authenticated caller's
principal whenever that principal carries a session id, so a session-bound caller cannot register tracing
context against another session. The same sentence states the caller-supplied `sessionId` as the fallback
for a transport that binds no session-scoped principal, and states that it never overrides a session-bound
principal.

The qualification is required rather than stylistic. Decision 1 retains the `callerSessionID` fallback for
the `/mcp` edge (`pkg/gateway/mcpfabric/mcptools/mcptools.go:1801-1806`) and §4 pins it with a tier-1 case,
so an unconditional "never from the call's arguments" rule would declare that retained behaviour a spec
defect in the change that preserves it. The spec is silent on the fallback today, so stating it here is
what makes the landed §8.3 text and the shipped gateway agree.

The platform-tool row at `:540` keeps its signature, which already omits the session parameter.

## 4. Testing

**Tier 9, `tests/tier9_security`.** The case runs two sessions in one tenant. A caller authenticated as
session A invokes `lenny/set_tracing_context` naming session B: B's `tracingContext` is unchanged and A's
carries the identifiers. This is the regression test for §1.1 and it fails against the current tree. A
second case drives the same call with no `sessionId` argument at all and asserts it registers against A,
pinning the handler's resolution of the session from the principal when the argument is absent.

**Tier 1, `pkg/gateway/mcpfabric/mcptools/schema_alignment_test.go`.** A case in the existing alignment
family reads `lenny/set_tracing_context` off the live `tools/list` surface through `toolListedSchema`
(`schema_alignment_test.go:30`) and asserts that `required` is exactly `["context"]` and that
`properties.sessionId` carries the siblings' transport-fallback description (the wording at
`mcptools_register.go:1011` and `:1096`). It carries a `// spec: §8.3` annotation and fails against the
current tree, whose schema literal lists `sessionId` in `required` (`mcptools_register.go:929`). This is
the only case that observes decision 2's schema edit, because no dispatch path validates a `tools/call`
against a tool's declared `InputSchema`: `handleToolCall` unmarshals `name` and `arguments` and passes the
raw bytes to the handler (`pkg/gateway/mcpfabric/mcp/mcp.go:345-350, :402`), and `InputSchema` is
serialized only into the `tools/list` catalog (`mcp.go:131`). Every sibling tool that dropped `sessionId`
from `required` already carries such a case (`schema_alignment_test.go:87, 128, 142, 162`), and the
tier-3 descriptor test checks well-formedness generically without pinning any tool's `required` list
(`tests/tier3_contract/ops_endpoints/mcp_schema_conformance_test.go:74-97`).

**Tier 1, `pkg/gateway/mcpfabric/mcptools`.** `callerSessionID` is preferred over a conflicting argument;
the argument is still honoured when the principal carries no session, which is the gateway-edge fallback
the siblings depend on; and a caller with neither a principal session nor a `sessionId` argument is
refused with `VALIDATION_ERROR`, pinning the guard CODE-1 substitutes. The existing table case
`set_tracing_missing_sessionid` (`pkg/gateway/mcpfabric/mcptools/mcptools_test.go:620`) is retained
unchanged: it drives the tool with no principal and no `sessionId`, and the replacement guard returns the
same `VALIDATION_ERROR` code and `PERMANENT` category it already asserts, with a different message the
case does not read.

**Tier 1, `pkg/gateway/session/executor`.** The gate's predicate is unchanged, so its existing cases
stand: a bind with `MaxConcurrentSessions > 1` and an empty `SlotID` is refused with `ErrSlotIDRequired`,
and an exclusive bind with an empty `SlotID` and `MaxConcurrentSessions` 1 or 0 is admitted
(`pkg/gateway/session/executor/pod_test.go:286` and `:344`). No case is retired and none of these fails
against the current tree.

**Tier 11.** The §8.3 sentence SPEC-1 adds resolves against the tool signature at `:540`.

## 5. Out of scope: what a fix for §1.2 must satisfy

This section states what a fix must satisfy so the next proposal starts from the constraint rather than
the symptom.

- The gateway must resolve the caller's session from the peer's verified identity rather than from the
  request body. The peer certificate identifies a pod; the mapping from pod to its bound sessions exists
  in the session store and the claim records but is not consulted on this path.
- A pod serving several slots holds several sessions at once, so the mapping is one-to-many and the
  request must still name which of the caller's own sessions it means. The check is membership rather
  than equality.
- `RequireVerifiedPeerInterceptor` (`pkg/gateway/mcpfabric/delegationtree/leasecontrol/auth.go`) is the
  natural place for the check and currently only proves a peer was verified at all.
- The local-development plaintext path passes every call through unchanged when mTLS is unconfigured, so
  a membership check must degrade the same way rather than break `make run`.
- The fix covers every `GatewayControl` operation (platform tools, connector tools, and scrub reports)
  rather than the tracing tool alone.

## 6. Application order

CODE-1 and SPEC-1 land together and are the whole of this proposal. The defect is live and permanent, and
the fix needs no recovery migration because it prevents new writes rather than repairing old ones. There
is no remediation for tracing contexts already written by this path, because the tree has no delete
surface for them. Whether one is needed is a question for the operator-facing side and is not staged here.

§1.2 and §1.3 are stated here and corrected elsewhere. §5 says what a fix for §1.2 must satisfy. §1.3 is
handed to proposal 0073, for the reason §7 gives.

## 7. Non-goals

This proposal does not add a delete or repair surface for `tracingContext`. It does not change `Merge`'s
no-overwrite rule or the 32-entry bound, both of which are §8.3 requirements and correct. It does not bind
`req.session_id` to mesh identity, per §5. It stages no change to the JSONL leg, which already injects the
adapter's bound session and is the subject of proposal 0069.

**It does not correct §1.3, which proposal 0073 owns.** An earlier revision of this document refused a
checkpoint-restore resume onto a concurrent-workspace pool, on the ground that no slot-aware resume path
exists and a resume that cannot reserve a slot should fail loudly rather than serve a session a pod whose
sibling slots it can read. That refusal is withdrawn here because 0073 builds the path it assumed absent:
0073 makes restore resolve against the slot root and carries a slot identifier on every session-scoped
message, so the resume that this document would have forbidden becomes the resume that works. Landing a
client-visible `422` and its specification text, and removing both one proposal later, costs more than
waiting. What 0073 must therefore also carry, and what this document hands it, is the gateway half: a
resumed session reserves a slot, and the resume `BindResult` reports the concurrency of the pod it holds
rather than a zero, so the §7.2 gate at `pkg/gateway/session/executor/pod.go:146` evaluates a true value
for the first time on that path. Until 0073 lands, the behaviour §1.3 describes stands unchanged.

It does not mark the per-pod occupancy claim as whole-pod, which is the other way to make a resumed pod
exclusive and which would add a field to the `SandboxClaim` CRD and a placement filter to `ClaimSlot`
Pass 1.

## 8. Files touched on application

- `pkg/gateway/mcpfabric/mcptools/mcptools_register.go` (CODE-1, the handler and the tool schema).
- `spec/08_recursive-delegation.md` §8.3 (SPEC-1).
- `tests/tier9_security` and `pkg/gateway/mcpfabric/mcptools`, including
  `pkg/gateway/mcpfabric/mcptools/schema_alignment_test.go` for the descriptor case, each as described in
  §4, and their `tests/spec-map.json` entries. The existing `set_tracing_missing_sessionid` case is
  retained as it stands, and the existing cases in `pkg/gateway/session/executor` are untouched, since
  the gate's predicate is unchanged.

`pkg/gateway/session/executor/pod.go`, `pkg/gateway/podlifecycle/podsession/`,
`pkg/gateway/sessionserver/start.go`, and `pkg/gateway/externalapi/errorclassify/` are not edited. An
earlier revision staged a resume refusal across them; §7 records why it was withdrawn and what proposal
0073 carries in its place.

## 9. Resolved in adversarial review

### Pass 1 (2026-08-16, automated)

- CODE-2 proposed refusing a `BindResult` whose `MaxConcurrentSessions` is zero, which would have refused
  every exclusive whole-pod bind. Zero is the ordinary exclusive value: the whole-pod `Bind` literal
  (`pkg/gateway/podlifecycle/podsession/binder.go:1021-1035`), the re-adopt literal
  (`cmd/lenny-gateway/coordination_seams.go:242-248`), and `foldPoolPolicy` with no §5.2 mirror row
  (`pkg/gateway/podlifecycle/podsession/resolve.go:359-369`) all leave it unset, and `Registry.Put`
  normalizes nothing. The refusal and the new `ErrConcurrencyUnset` are dropped, the gate's predicate at
  `pkg/gateway/session/executor/pod.go:146` is left unchanged, and decision 3 records why.
- CODE-2 claimed `Binder.Resume` could populate `MaxConcurrentSessions` and `SlotID` from the resume
  path's resolved pool and claim. `ResumeRequest` carries neither field
  (`pkg/gateway/podlifecycle/podsession/binder.go:611-659`), and `Resume` acquires its pod through a
  whole-pod `podclaim.ClaimRequest` that resolves no slot (`binder.go:1652-1670`). CODE-2 now states the
  plumbing: a `MaxConcurrentSessions` field on `ResumeRequest`, populated at
  `pkg/gateway/sessionserver/start.go:3898` from the already-resolved `PoolMatch` normalized to a minimum
  of 1, with `SlotID` left empty so a resumed concurrent-pool session fails closed at the existing gate.
- CODE-1 removed the empty-session validation on the premise that the principal always supplies the
  value, which decision 1 contradicts by retaining the `callerSessionID` fallback for the `/mcp` edge.
  The removal is replaced by the post-resolution guard the eleven siblings carry
  (`mcptools_register.go:657-663`), so an unbound caller still receives `VALIDATION_ERROR` rather than a
  session lookup on the empty string, and §4 records that the existing
  `set_tracing_missing_sessionid` case is retained.
- §1.1 cited `pkg/adapter/tracingcontext.go:33-35` for the JSONL leg's session injection and its
  rationale. That range is part of the `tracingFrameAddressesStream` doc comment. The citation now names
  the injection at `:98-101` and the quoted sentence at `:70-72`.
- CODE-2's account of the resume path cited `binder.go:1578` for the `b.connect` call. That line is part
  of the `Resume` doc comment. The call is at `binder.go:1581`, and the citation now names it.

### Pass 2 (2026-08-16, automated)

- CODE-2 called the slot-aware bind path "a different type (`podclaim.SlotClaimer` and `SlotBinder`,
  `slotbinder.go:305-331`)". No `SlotBinder` type exists in production code; the only matches are test
  doubles and the consumer-side `slotBinder` interface at `pkg/gateway/sessionserver/start.go:2678`, and
  the cited range is body code inside `BindSlot`. The slot-aware bind is `Binder.BindSlot`
  (`pkg/gateway/podlifecycle/podsession/slotbinder.go:128`), a method on the same `*Binder` that carries
  `Resume` (`binder.go:1580`). CODE-2 now names the method, the `podclaim.SlotClaimer` reservation, and
  the `BindResult` literal that carries `SlotID` and `MaxConcurrentSessions` (`slotbinder.go:319-331`).
- CODE-2 normalized the resume bind's `MaxConcurrentSessions` to the resolved pool's bound, which would
  have refused the first Attach of every resumed session on a concurrent-workspace pool. §1.3 justified
  that refusal by asserting such sessions hold no checkpoint, citing
  `pkg/gateway/sessionserver/finalize.go:238`. That line governs finalize-time workspace preparation and
  its own comment states the concurrent path materializes and launches at `/start` through
  `BindReservedSlot`. Checkpoints on a concurrent-workspace pool are taken and recorded per slot
  (`pkg/gateway/checkpoint/checkpointer/checkpointer.go:444`, with the per-(session, slot) retention at
  `:471`), which §5.2 states is the defined behaviour, and `resumeOnPod` branches into `Binder.Resume` on
  `WorkspaceSnapshot.Ref` alone (`pkg/gateway/sessionserver/start.go:3837`). CODE-2 now sets
  `MaxConcurrentSessions: 1` directly in the `Resume` return literal, which is what a whole-pod claim
  produces (`binder.go:1652-1670`) and what `BindResult`'s own field documentation calls an exclusive bind
  (`binder.go:495-504`). The `ResumeRequest` field and the `pkg/gateway/sessionserver/start.go` edit are
  dropped, since the caller would have had nothing to pass but the constant. §1.3's latency paragraph and
  the §4 case that asserted `ErrSlotIDRequired` on a resumed pool of 4 are replaced accordingly, and §8
  drops `pkg/gateway/sessionserver` from the files touched.

### Pass 3 (2026-08-16, automated)

- No listed test observed CODE-1's schema edit, and §4 credited a tier-9 case with pinning it. The MCP
  server validates no `tools/call` against a tool's declared `InputSchema`: `handleToolCall` unmarshals
  `name` and `arguments` and dispatches the raw bytes (`pkg/gateway/mcpfabric/mcp/mcp.go:345-350, :402`),
  and `InputSchema` is serialized only into the `tools/list` catalog (`mcp.go:131`), so the tier-9 case
  passes with the schema edit reverted. §4 gains a descriptor case in
  `pkg/gateway/mcpfabric/mcptools/schema_alignment_test.go` that reads the tool off `tools/list` through
  `toolListedSchema` (`:30`) and asserts `required` is exactly `["context"]` and that
  `properties.sessionId` carries the siblings' transport-fallback description, matching the case every
  sibling that dropped `sessionId` already carries (`:87, 128, 142, 162`). The tier-9 second case is now
  described as pinning the handler's resolution when the argument is absent, and §8 lists the test file.
- SPEC-1 staged a §8.3 sentence stating unconditionally that the gateway resolves the session from the
  authenticated caller rather than from the call's arguments, which the shipped gateway contradicts on the
  `/mcp` edge, where `callerSessionID` returns the caller's `sessionId` argument when the principal carries
  no session (`pkg/gateway/mcpfabric/mcptools/mcptools.go:1801-1806`). Decision 1 retains that fallback and
  §4 pins it, so the sentence would have declared a deliberately preserved behaviour a defect. SPEC-1 now
  states the rule for a session-bound caller and states the caller-supplied `sessionId` as the fallback for
  a transport that binds no session-scoped principal, which never overrides a session-bound principal.
- CODE-2 asserted that `Binder.Resume` produces an exclusive bind by construction and staged
  `MaxConcurrentSessions: 1` on it. A whole-pod claim does not exclude slot placement: the per-pod
  occupancy claim carries only `sandboxRef` and `tenantId`
  (`pkg/gateway/podlifecycle/podclaim/claimer.go:294-298`), and `ClaimSlot` Pass 1 lands a slot on any pod
  holding a live, non-terminal same-tenant claim with free counter capacity
  (`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:411-460`), with no marker to skip a whole-pod one. The
  resumed session's slot-less Attach then reads the pod's output unfiltered
  (`pkg/adapter/attach.go:70-72`), which is the misdelivery the gate refuses. The constant is dropped.
  CODE-2 now carries the resolved pool's bound onto the resume bind through a `MaxConcurrentSessions` field
  on `ResumeRequest`, populated at `pkg/gateway/sessionserver/start.go:3898` from the `PoolMatch` resolved
  at `:3876`, so a resume onto a concurrent-workspace pool is refused with `ErrSlotIDRequired`, which is
  the outcome §7.2 defines. §1.3's exclusivity paragraph, decision 3, the §4 podsession case, §6, §7, and
  §8 are updated to the same predicate, and the slot-aware resume that would let those resumes succeed
  again is stated as a non-goal.

### Pass 4 (2026-08-16, automated)

- CODE-2 placed its refusal at the §7.2 executor gate, which is not on the resume path. That gate lives
  in `PodExecutor.streamFor` (`pkg/gateway/session/executor/pod.go:128, 146`) and runs on the first `Send`
  or Attach, so `resumeOnPod` would still have published the binding
  (`pkg/gateway/sessionserver/start.go:3936`), bumped the recovery generation (`:3938`), fenced the pod
  (`:3943`), and returned a mode, after which `handleResume` transitions the row to `running` (`:3416-3418`),
  records `outcome="success"` (`:3429`), writes the `session.resumed` audit row (`:3437-3446`), and returns
  200. The refused resume would have reported success while holding a whole-pod claim nothing releases and
  failing every later message. CODE-2 is now split: CODE-2a refuses in `resumeOnPod` between the pool
  resolution at `:3876` and the `Resume` call at `:3898`, where no pod has been claimed and the error
  travels the existing failure branch (`:3388-3411`) to `holdOrFailOnResumeError` (`:3402`) and the
  `outcome="failure"` counter (`:3394`). The refusal is a new typed
  `podsession.ConcurrentPoolResumeUnsupported` surfaced as `422 CONCURRENT_POOL_RESUME_UNSUPPORTED` through
  a `writePodClaimError` case (`start.go:86`), following the permanent-cause convention already beside it
  (`:97-137`) rather than the retryable `RESUME_FAILED` 503, which would pair a `Retry-After` envelope with
  the terminal `failed` row the non-transient branch writes. CODE-2b keeps the `ResumeRequest`
  plumbing and the unchanged gate as the backstop. Decision 3, §4, §6, §7, and §8 state the same outcome.
- No listed test observed the `resumeOnPod` call-site edit, so CODE-2 could have landed inert with the
  podsession and executor cases green: both construct their own `ResumeRequest` or `BindResult` and cannot
  see whether the sessionserver populates the field from the resolved `PoolMatch`. §4 gains a tier-1
  `pkg/gateway/sessionserver` case, feasible because the internal tests already build a live
  `podsession.Binder` over envtest (`pkg/gateway/sessionserver/start_preclaim_internal_test.go:779`). It
  drives `resumeOnPod` and the handler for a row with a `WorkspaceSnapshot.Ref` against a pool whose §5.2
  mirror row sets `maxConcurrentSessions: 4` and asserts the refusal, the envelope, the terminal row, the
  failure counter, the absent audit row, and the unclaimed pod, with the single-session counterpart
  asserting the `ResumeRequest` carries 1. §8 lists the package.
- CODE-2 retired the §7.3 resume path for concurrent-workspace pools while staging no spec edit recording
  it. §7.3's resume flow (`spec/07_session-lifecycle.md:401-413`) names no concurrency condition, and
  §5.2's per-slot checkpoint paragraph (`spec/05_runtime-registry-and-pool-model.md:542`) defines the
  checkpoints whose purpose is that restore, so applying CODE-2 alone would leave both sections asserting a
  guarantee the gateway can no longer honour. SPEC-2 stages the §7.3 qualifier, the §5.2 cross-reference,
  the `CONCURRENT_POOL_RESUME_UNSUPPORTED` row in `spec/15_external-api-surface.md` beside `RESUME_FAILED`
  (`:1133`), and the mirrored row in `docs/reference/error-catalog.md` (`:157`). §4 gains the tier-11
  assertion and §8 lists the files.
- SPEC-2's §7.3 qualifier and §6's client-visible outcome stated the refusal for every resume onto a
  concurrent-workspace pool, while CODE-2a refuses only the checkpoint-restore branch. `resumeOnPod`
  routes a row with no `WorkspaceSnapshot.Ref` through `startOnPod`
  (`pkg/gateway/sessionserver/start.go:3837, 3845`), which never reaches the `ResolvePool` at `:3876` or
  the `Resume` at `:3898`; it resolves the pool itself (`:2269`) and routes a
  `maxConcurrentSessions > 1` pool through `bindConcurrentSlot` (`:2312-2314`), which mints a slot. That
  resume restores such a session onto a replacement pod with a resolved slot and returns 200 both before
  and after CODE-2, so the staged §7.3 sentence would have landed spec text the shipped gateway
  contradicts. The refusal predicate is `WorkspaceSnapshot.Ref != "" && match.MaxConcurrentSessions > 1`.
  The SPEC-2 bullet, §6, the CODE-2 and SPEC-2 headings, CODE-2's closing paragraph, and the §7 non-goal
  now scope the refusal to the checkpoint-restore resume and state that the snapshotless resume-rebuild
  is unaffected.

### Pass 5 (2026-08-16, automated)

- SPEC-2 staged three edits and omitted `spec/29_communication-scenarios.md`, whose §29.6 traces the
  client-driven `POST /v1/sessions/{id}/resume` to a session `running` again on a replacement pod with its
  workspace restored from a checkpoint (`:986-989`) and whose §29.10 states that each of §29.2 through
  §29.9 holds on a pod whose pool sets `sessionPolicy.maxConcurrentSessions` above 1 (`:1421, :1429-1430`).
  After CODE-2a that trace does not hold on such a pod, and §29.9 routes its eviction rebuild into the same
  restore (`:1411-1413`), so applying the proposal would have left §7.3's new qualifier and §29.10's
  blanket sentence giving opposite answers for one session. SPEC-2 gains a fourth edit qualifying §29.10
  and adding the §7.3 pointer in §29.6, §4's tier-11 assertion covers it, and §8 lists the file.
- §4's feasibility ground for the CODE-2a sessionserver case named
  `pkg/gateway/sessionserver/start_preclaim_internal_test.go:779` as a live `podsession.Binder` over
  envtest. That harness builds its binder over a controller-runtime fake client (`:759`), whose own doc
  comment states it cannot serve a successful slot reservation (`:741-743`), so the half of the case that
  must complete a `Binder.Resume` cannot run there, and `Server.podBinder` is the concrete
  `*podsession.Binder` with no seam to stub (`pkg/gateway/sessionserver/sessionserver.go:188`). §4 now
  cites the package's envtest harness (`podBindEnvtestClient`, `start_pod_test.go:104`, wired as
  `messages_component_test.go:118` does) for the completed-bind assertions, keeps the fake-client harness
  for the refusal that returns before any claim, and observes the populated bound through the `BindResult`
  published to the `podsession.Registry` (`pkg/gateway/podlifecycle/podsession/registry.go:33`).
- §4 labelled the sessionserver case tier 1 although it drives a binder against the kube-apiserver, which
  `.claude/rules/test-coverage.md` maps to tier 2 (envtest), and the case's own stated `// diagnosis:`
  comment is required only from tier 2 up. The package precedent is explicit
  (`pkg/gateway/sessionserver/messages_component_test.go:39` calls itself the tier-2 gateway component
  test). §4 now splits the case: a tier-1 internal case for the refusal against the fake-client harness and
  a tier-2 envtest component case for the envelope, the terminal row, the counter, the absent audit row,
  and the populated bound. §8 names both.
- SPEC-2 placed the `PERMANENT` 422 `CONCURRENT_POOL_RESUME_UNSUPPORTED` row in
  `docs/reference/error-catalog.md` beside `RESUME_FAILED` (`:157`), which sits under `## TRANSIENT errors`
  (`:133`). That catalog partitions codes into a table per category and records the category in the section
  heading rather than in a column, so the staged row would have contradicted the `PERMANENT` row the same
  change adds to `spec/15_external-api-surface.md`. The bullet now files the row in the
  `## PERMANENT errors` table (`:63`) beside `SETUP_COMMAND_FAILED` (`:129`), and §4's tier-11 assertion
  checks the docs half against the section heading.
- Decision 3 cited `pkg/gateway/sessionserver/start.go:3938` for the recovery-generation bump and `:3943`
  for the pod fence. Both are comment lines. The calls are `bumpRecoveryGeneration` at `:3941` and
  `fenceResumedPod` at `:3946`, and decision 3 now names them.
- The tier-2 bullet above cited `pkg/gateway/sessionserver/messages_component_test.go:40` for the package's
  tier-2 self-description. That line is a continuation of the doc comment. The characterization is on
  `:39`, and the bullet now cites that line.

### Pass 6 (2026-08-16, automated)

- CODE-2a staged `422 CONCURRENT_POOL_RESUME_UNSUPPORTED` and SPEC-2 staged a `PERMANENT` row for it,
  while no edit added the code to the shared classifier table, so the two surfaces would have disagreed.
  REST classifies through `ClassifyStatus(code, 422)` (`pkg/gateway/sessionserver/sessionserver.go:3302`,
  `pkg/gateway/externalapi/errorclassify/errorclassify.go:67-75`) and derives `PERMANENT` with
  `retryable: false` from the status. The MCP `lenny/resume_session` tool proxies the same route
  in-process (`pkg/gateway/mcpfabric/mcptools/client_tools.go:104`), discards the REST envelope's category
  and retryable fields (`client_tools.go:51-52`), and rebuilds them from the code alone through
  `Classify` (`pkg/gateway/mcpfabric/mcp/mcp.go:501, :84`), whose unknown-code fallback is
  `(CategoryTransient, true)` (`errorclassify.go:44-48`). The refusal would have carried a retry hint over
  MCP for a condition decision 3 says no retry clears, contradicting the staged `PERMANENT` row and the
  §15.2.1 rule 5(d) parity requirement (`spec/15_external-api-surface.md:1434`). CODE-2a now stages the
  entry `"CONCURRENT_POOL_RESUME_UNSUPPORTED": {CategoryPermanent, false}` beside `SETUP_COMMAND_FAILED`
  (`errorclassify.go:476`), matching every sibling permanent cause `writePodClaimError` emits, §4 gains a
  tier-1 case extending `TestClassifySessionLifecycleFallbackCodes` (`errorclassify_test.go:176-188`), and
  §8 lists the file and the test package.

### Pass 7 (2026-08-16, automated)

- §4 labelled the `pkg/gateway/podlifecycle/podsession` case tier 1 although it requires a completed
  `Binder.Resume`, which the package can only run against a real kube-apiserver. The package's sole bind
  harness is `k8sClient` (`pkg/gateway/podlifecycle/podsession/binder_test.go:174`), which starts envtest
  because the §4.6.3 SSA Apply path the claim uses is unimplemented by the fake client, and it is the
  harness `TestResumeClaimsAndRestoresTheSession` (`:483`) uses; the package's only fake-client tests
  build no bind (`crdgeneration_test.go:33, :52, :71`). `.claude/rules/test-coverage.md` maps anything
  reading or writing the kube-apiserver to tier 2 (`:34`) and requires a `// diagnosis:` comment from
  tier 2 up (`:59`), so at tier 1 the case carried no such comment and an implementor running
  `--max-tier 1` would never reach the case that pins CODE-2b's `Resume` return literal. This is the
  correction pass 5 already applied to the sibling sessionserver case. §4 now labels the case tier 2
  (envtest component), names the harness, and requires the `// diagnosis:` comment, and §8 names it as
  the package's tier-2 case.

### Pass 11 (2026-08-16, hand-authored after the scope split)

- **CODE-2 and SPEC-2 withdrawn; §1.3 handed to proposal 0073.** The convergence reshaped CODE-2 from a
  gate predicate change into a refusal of any checkpoint-restore resume onto a concurrent-workspace pool,
  carrying a new client-visible `422 CONCURRENT_POOL_RESUME_UNSUPPORTED`, its typed error, its shared
  classifier entry, and specification text in §7.3, §5.2, §29.6, §29.10, and the error-code table. That
  correction rests on there being no slot-aware resume path. Proposal 0073 builds one: it carries a slot
  identifier on every session-scoped message and resolves restore against the slot root, which turns the
  forbidden resume into a working one. Landing the refusal and its client-visible contract, then deleting
  both when 0073 lands, costs more than handing the defect over, so §1.3 stays stated here and is
  corrected there. The gateway half 0073 must now also carry is named in §7: a resumed session reserves a
  slot, and the resume `BindResult` reports its pod's concurrency, so the §7.2 gate evaluates a true value
  on that path. CODE-1 and SPEC-1 are untouched by the split and keep the review that certified them.

### Pass 12 (2026-08-16, automated)

- The Scope line, §0, and decision 5 still described three ordered staged changes that the scope reduction
  had removed. §6 states that CODE-1 and SPEC-1 land together and are the whole of this proposal, decision
  3 hands §1.3 to proposal 0073, decision 4 scopes §1.2 to a later proposal, and §8 lists one code file and
  one spec file, so there is no severity order for §6 to state and no second or third change to accept or
  revert on its own. The §0 rationale was also inverted, arguing against correcting only the shallowest
  defect, which is what the document does. The Scope line now states that the document states three defects
  and stages the fix for the first, and names §5 and §7 as the dispositions of the other two. §0 now states
  that all three are stated so the correction of the shallowest is not read as closing the question, that
  only §1.1 is corrected here, and that decisions 4 and 3 carry the other dispositions. Decision 5 now
  records that CODE-1 and SPEC-1 land together as one revertible change and are the whole of what this
  document applies.
