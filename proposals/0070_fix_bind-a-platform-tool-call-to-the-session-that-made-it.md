# Proposal: Bind a platform-tool call to the session that made it

- **Status:** Draft for review.
- **Date:** 2026-08-13
- **Scope:** Three defects found while reviewing proposal 0069, each one a security-relevant decision that
  reads a value its decider never verified. A platform-tool handler takes the session it writes from the
  caller's own arguments; the principal those arguments are checked against is itself derived from an
  unverified request field; and a fail-closed gate reads a field one of its callers never populates. The
  first is live and has no recovery path. None is caused by 0069 and none should wait behind it.

This document stages the proposed code and specification changes. It does not modify any spec, code, or
doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 0. Context an implementor should read first

The three defects sit at three different depths of the same question: *what proves that a caller is the
session it says it is?* They are proposed together because fixing only the shallowest would read as
closing the question, and it does not. They are separable in application, and §6 states the order.

## 1. Problem

### 1.1 The tracing handler writes the session its caller names

`registerTracingTool` reads `in.SessionID` out of the tool arguments and passes it to `Store.Get` and
`Store.Update` (`pkg/gateway/mcpfabric/mcptools/mcptools_register.go:945`). Only the tenant is resolved
from the principal, through `callerTenantID` (`:933`). The handler never compares the session it is about
to write against the session the caller is.

It is the sole outlier among its neighbours. Eleven sibling handlers resolve through
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

The tree already names this threat and defends the other path. `pkg/adapter/tracingcontext.go:33-35`, on
the JSONL leg, injects the adapter's bound session and says why: "a runtime cannot register tracing
context against a session it does not own."

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

Same-tenant is a real constraint and an insufficient one. §8.3 grants write authority over a session's
tracing context to that session's runtime, and `spec/04_system-components.md:951` requires a pod's local
MCP servers never to expose other sessions. The write violates the actor model those state.

### 1.2 The principal is only as good as the field it is built from

`GatewayControl.CallPlatformTool` builds the caller's principal from `req.session_id`
(`pkg/gateway/gatewaycontrol/platformtools/platformtools.go:118-123`), and nothing binds that field to the
peer that sent it. The listener's mTLS verifier is configured with a trust domain and a deny list and no
per-pod expectation (`cmd/lenny-gateway/main.go:651-656`), so a certificate proves the peer is *an* agent
pod in the trust domain, never *which session* it serves.

The tree documents this plainly at
`pkg/gateway/mcpfabric/delegationtree/leasecontrol/auth.go:29-33`: each handler "resolves the caller's
tenant from the session_id in the request body and has no other proof of identity, so an unauthenticated
peer must never reach it."

That comment describes the interceptor's job accurately, and the interceptor does it: it fails closed on
an unverified peer. What it cannot do is distinguish two verified peers. Every session's adapter presents
a certificate the verifier accepts, so any of them can name any session id.

This is why §1.1's fix is necessary and not sufficient. Routing the tracing handler through
`callerSessionID` makes it read the principal, and on the adapter path the principal is the adapter's own
bound session, so the fix is real. It is real because of a property of the adapter, not because of a
property the gateway verified. A compromised or buggy adapter, or any process reaching the port with a
trust-domain certificate, still names what it likes — for every platform tool, not only this one.

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

Neither site is wrong when read alone. The gate is correct and the struct literal is merely incomplete.
The invariant lives in the gap between them, and a zero value is indistinguishable from a deliberate 1.

**The exploit chain appears latent today**, and the reason is a property of a different subsystem rather
than of this gate: `pkg/gateway/sessionserver/finalize.go:238` returns early for a pool with
`MaxConcurrentSessions > 1`, so a concurrent-pool session prepares no workspace at finalize and has no
snapshot to resume from. If concurrent-pool checkpointing ever lands, the hole opens with no test failing.

## 2. Decisions

1. **A handler resolves the session it writes from the principal, never from its arguments.**
   §1.1's handler joins the eleven that already do. The fallback in `callerSessionID` is retained rather
   than bypassed, because the external `/mcp` edge relies on it and the siblings depend on that behaviour.

2. **The argument is removed from the tool's schema, not merely ignored.** The spec signature is
   `lenny/set_tracing_context(context)` with no session parameter (`spec/08_recursive-delegation.md:540`),
   and the sibling schemas document theirs as a transport fallback the principal overrides. This handler
   alone marks `sessionId` required. Leaving a required argument the handler ignores invites a caller to
   believe it means something.

3. **A fail-closed gate treats an unset value as unsafe.** §1.3 is fixed at the gate rather than only at
   the caller, so a future `BindResult` that omits the field fails rather than passes. `Binder.Resume` also
   populates both fields, so the gate has something true to read.

4. **The unverified `session_id` is stated, scoped, and not patched here.** Binding a platform-tool call to
   its peer's mesh identity requires a pod-to-session mapping the gateway does not currently consult, and
   choosing where that mapping lives is a design question rather than a correction. §5 states what a
   solution must satisfy so the next proposal does not restate the analysis.

5. **The three land in severity order and are independently revertible.** §6 states it. A reviewer who
   accepts only the first change should be able to take it alone.

## 3. Proposed changes

### CODE-1. Resolve the tracing session from the principal

In `pkg/gateway/mcpfabric/mcptools/mcptools_register.go`, `registerTracingTool` resolves
`sessionID := callerSessionID(ctx, in.SessionID)` before the store lookup at `:945` and uses it for the
`Store.Get`, the terminal-state check, the `Store.Update`, and the response body. The
`in.SessionID == ""` validation at `:941-944` is removed, because the principal supplies the value on both
the adapter path and the gateway edge.

The tool's input schema drops `sessionId` from `required` and documents it as a transport fallback the
principal overrides, matching the wording its siblings already carry.

### CODE-2. Make the slot gate fail closed on an unset value

`BindResult` gains no field. `pkg/gateway/podlifecycle/podsession/binder.go:1608-1616` populates
`MaxConcurrentSessions` and `SlotID` from the resume path's resolved pool and claim, so the values it
returns are true.

`pkg/gateway/session/executor/pod.go:147` refuses a bind whose `MaxConcurrentSessions` is zero, on the
ground that zero is not a pool configuration any caller should produce: §5.2's minimum is 1 and
`spec/04_system-components.md:426` states the CEL rule. The refusal carries `ErrSlotIDRequired`'s sibling,
a new `ErrConcurrencyUnset`, so the two failures are distinguishable in a log.

### SPEC-1. State the binding rule §1.1 breaks

`spec/08_recursive-delegation.md` §8.3 states that a runtime registers tracing context against its own
session and that the gateway resolves that session from the authenticated caller rather than from the
call's arguments. The platform-tool row at `:540` keeps its signature, which already omits the session
parameter and is what CODE-1 makes true.

## 4. Testing

**Tier 9, `tests/tier9_security`.** Two sessions in one tenant. A caller authenticated as session A
invokes `lenny/set_tracing_context` naming session B: B's `tracingContext` is unchanged and A's carries
the identifiers. This is the regression test for §1.1 and it fails against the current tree. A second case
drives the same call with no `sessionId` argument at all and asserts it registers against A, pinning
decision 2.

**Tier 1, `pkg/gateway/mcpfabric/mcptools`.** `callerSessionID` is preferred over a conflicting argument;
the argument is still honoured when the principal carries no session, which is the gateway-edge fallback
the siblings depend on.

**Tier 1, `pkg/gateway/session/executor`.** A `BindResult` with `MaxConcurrentSessions` zero is refused
with `ErrConcurrencyUnset`; one with `MaxConcurrentSessions > 1` and an empty `SlotID` is refused with
`ErrSlotIDRequired`; a single-session bind with an empty `SlotID` and `MaxConcurrentSessions` 1 is
admitted. The first case fails against the current tree.

**Tier 1, `pkg/gateway/podlifecycle/podsession`.** `Binder.Resume` returns both fields populated for a
concurrent pool and for a single-session pool.

**Tier 11.** The §8.3 sentence SPEC-1 adds resolves against the tool signature at `:540`.

## 5. Out of scope: what a fix for §1.2 must satisfy

Stated so the next proposal starts from the constraint rather than the symptom.

- The gateway must resolve the caller's session from the peer's verified identity, not from the request
  body. The peer certificate identifies a pod; the mapping from pod to its bound sessions exists in the
  session store and the claim records but is not consulted on this path.
- A pod serving several slots holds several sessions at once, so the mapping is one-to-many and the
  request must still name which of the caller's own sessions it means. The check is membership, not
  equality.
- `RequireVerifiedPeerInterceptor` (`pkg/gateway/mcpfabric/delegationtree/leasecontrol/auth.go`) is the
  natural place for the check and currently only proves a peer was verified at all.
- The local-development plaintext path passes every call through unchanged when mTLS is unconfigured, so
  a membership check must degrade the same way rather than break `make run`.
- The fix covers every `GatewayControl` operation — platform tools, connector tools, and scrub reports —
  rather than the tracing tool alone.

## 6. Application order

CODE-1 and SPEC-1 land first and alone: the defect is live, permanent, and needs no recovery migration
because the fix prevents new writes rather than repairing old ones. CODE-2 lands second and is independent
of it. §1.2 follows in its own proposal.

There is no remediation for tracing contexts already written by this path, because the tree has no delete
surface for them. Whether one is needed is a question for the operator-facing side and is not staged here.

## 7. Non-goals

Adding a delete or repair surface for `tracingContext`. Changing `Merge`'s no-overwrite rule or the
32-entry bound, both of which are §8.3 requirements and correct. Binding `req.session_id` to mesh identity,
per §5. Any change to the JSONL leg, which already injects the adapter's bound session and is the subject
of proposal 0069.

## 8. Files touched on application

- `pkg/gateway/mcpfabric/mcptools/mcptools_register.go` — CODE-1, the handler and the tool schema.
- `pkg/gateway/podlifecycle/podsession/binder.go` and `pkg/gateway/session/executor/pod.go` — CODE-2, with
  the new `ErrConcurrencyUnset`.
- `spec/08_recursive-delegation.md` §8.3 — SPEC-1.
- `tests/tier9_security`, `pkg/gateway/mcpfabric/mcptools`, `pkg/gateway/session/executor`, and
  `pkg/gateway/podlifecycle/podsession` — the cases in §4, and their `tests/spec-map.json` entries.
