# Iter3 SES Review

## Regression check of iter2 fixes (commit 2a46fb6)

- **SES-004 (resumeMode enum parity):** FIXED. §7.2 line 126 now declares
  `resumeMode` as `full | conversation_only | partial_workspace` with an
  optional `workspaceRecoveryFraction` 0.0-1.0. §10.1 line 122 uses
  `partial_workspace` and §4.4 line 263 uses `conversation_only`. Aligned.
- **SES-005 (starting -> resume_pending):** FIXED. §7.2 lines 149-150 add
  `starting -> resume_pending` and `starting -> failed`; §6.2 lines 89-90 add
  `starting_session -> resume_pending` and `starting_session -> failed` with
  the pre-attached-vs-post-attached distinction correctly narrated in
  §6.2 line 278 ("sole exception") and §7.2 line 186.
- **MSG-004 (path 5 receipt):** FIXED. §7.2 line 284 path 5 now reads
  "Delivery receipt status: `queued`".
- **concurrent-stateless residualStateWarning:** FIXED. §7.1 line 73
  describes shared process space / network stack / /tmp / page cache with
  "no per-request scrub" and pins both concurrent variants to `true`.
- **SEC-001 derive/replay isolation gate:** FIXED. §7.1 line 98 references
  the `platform-admin`-gated `allowIsolationDowngrade: true` and ties the
  replay path to the same rule; §15.1 line 514 mirrors it for replay.

## Outstanding iter2 findings still unresolved

- **SES-006** (`resuming -> cancelled/completed` for client actions): no
  change in §6.2 lines 116-120. See SES-010 below.
- **SES-007** (derive pre-running -> failed contradicts §7.1 atomicity):
  no change at §7.1 line 92. See SES-009 below.
- **SES-008** (SSE reconnect rate limit / replay cap): no change at §7.2
  line 324 or §15.4. See SES-012 below.
- **SES-001/002/003** (iter1 low-severity items): unchanged; de-prioritized.

---

### SES-009 Derive Pre-Running `failed` Persistence Still Contradicts §7.1 Atomicity [Medium]
**Files:** `07_session-lifecycle.md:28`, `07_session-lifecycle.md:92`, `15_external-api-surface.md:410`

The iter2 patch for SES-001/007 added the text at §7.1 line 92:

> "any partially written destination object for the derived session is
> deleted by the gateway and the derived session record is marked `failed`."

This directly contradicts §7.1 line 28 ("There is no `created -> failed`
transition because the session is never persisted in the `created` state
until all preconditions are satisfied"). The derive partial-copy path
therefore persists a session row in a state (`failed`) that the
documented state machine cannot reach from a pre-running state. Callers
that pass the session-atomicity rule through the API observer
(`GET /v1/sessions/{id}` in §15.1 lines 415-429, which lists only
`created, finalizing, ready, starting, running, suspended,
resume_pending, awaiting_client_action` as non-terminal entry points to
`failed`) cannot explain where the derived `failed` session came from.

Secondary ambiguity the iter2 patch left open:
1. What does `POST /v1/sessions/{id}/derive` return to the client? A
   `session_id` already in `failed` (meaning the client must poll to
   discover it), or an error response? The iter2 text says "clients MUST
   retry the entire `POST /v1/sessions/{id}/derive` call" but does not
   say whether the original response carried a `session_id`.
2. The `DERIVE_SNAPSHOT_UNAVAILABLE` error is classified `TRANSIENT` and
   503, implying the client never received a `session_id`. But the text
   also says the derived record is marked `failed`, implying a row
   exists. These two are mutually exclusive.

**Recommendation:** Pick one model and commit to it:

1. **No persistence on derive failure.** If the MinIO copy fails, do
   NOT persist a `failed` session record. Delete any partial destination
   object and return `503 DERIVE_SNAPSHOT_UNAVAILABLE` (or
   `500 DERIVE_COPY_FAILED` for non-snapshot errors). No `session_id`
   is ever returned to the client. This preserves §7.1 atomicity for
   derive identically to create.
2. **Persist, but reach `failed` through a documented transition.** Add
   `created -> failed` to §7.2 as an explicit derive-only path, update
   §15.1 preconditions to list `failed` as reachable from `created`,
   and spell out that `POST /derive` returns the `session_id` with
   `state: failed` in the response body so clients can inspect it via
   `GET /v1/sessions/{id}`.

Option 1 is simpler and consistent with the rest of the spec's "the
session is either fully ready or reported as a creation error"
philosophy. It also matches the `SESSION_CREATION_FAILED` model in
§7.1 atomicity.

---

### SES-010 `resuming -> cancelled` / `resuming -> completed` Still Undefined [Medium]
**Files:** `06_warm-pod-model.md:116-120`, `15_external-api-surface.md:407, 411`

SES-006 (iter2) flagged that client actions during `resuming` are
unspecified. §15.1 line 407 says `POST /terminate` is valid in any
non-terminal state including `resume_pending`, and line 411 says
`DELETE` is valid in any non-terminal state. §6.2 lines 116-120 list
only `resuming -> resume_pending`, `resuming -> awaiting_client_action`,
and `resuming -> attached` (via line 101). There is no explicit
`resuming -> cancelled` or `resuming -> completed` transition.

`resuming` is specifically the window where the gateway is actively
streaming a workspace tar to a replacement pod. Client cancellation
during this window raises two open questions:

1. **Tar-extract interrupt point.** Does the gateway abort the in-flight
   MinIO read / pod tar-extract stream on cancel? If yes, the pod ends up
   with a partial workspace on tmpfs; §5.2 scrub rules for session-mode
   pods say the pod is terminated anyway, so the partial workspace is
   acceptable - but this is implicit, not stated.
2. **Generation counters.** `recovery_generation` was incremented when
   the session entered `resume_pending`. Does cancel during `resuming`
   still commit the generation bump, or does the cancel roll it back?
   §4.2 distinguishes `recovery_generation` from `coordination_generation`
   but does not address rollback on cancel.

**Recommendation:** Add to §6.2 resuming-failure table:

```
resuming -> cancelled  (client issues DELETE /v1/sessions/{id} or parent cancels;
                        gateway aborts in-flight tar extraction on replacement pod,
                        terminates the replacement pod, and commits the
                        recovery_generation bump unchanged)
resuming -> completed  (client issues POST /terminate; same cleanup path,
                        terminal state is completed rather than cancelled per
                        §15.1 semantics)
```

And a one-sentence note: "Partial workspace extraction on the
replacement pod is discarded when the pod is terminated after cancel.
The `recovery_generation` counter is not rolled back - subsequent
resume attempts after the cancel (impossible for cancelled/completed
sessions) would see the bumped counter, preserving fencing invariants."

---

### SES-011 `starting -> resume_pending` Not Reflected in §15.1 Preconditions [Low]
**Files:** `15_external-api-surface.md:405, 408`, `07_session-lifecycle.md:149`

The iter2 SES-005 fix added `starting -> resume_pending` to §7.2 line 149
and §6.2 line 90, but §15.1 line 405 still says:

> `POST /v1/sessions/{id}/start` | `ready` | `starting` -> `running`

Nothing in the §15.1 preconditions table mentions that a
`POST /v1/sessions/{id}/start` call can result in a session visible in
`resume_pending` rather than `running`. A client following only §15.1
would see a session in `resume_pending` after calling `/start` and
conclude the gateway violated the documented transition contract.
`POST /start` is not fire-and-forget (it returns synchronously) but the
post-return streaming events can emit `status_change(state:
resume_pending)` per §7.2 line 125.

**Recommendation:** Extend §15.1 row for `POST /v1/sessions/{id}/start`
resulting-transition column to:

> `starting -> running` (normal), or `starting -> resume_pending ->
> running` on pod crash with retries remaining, or
> `starting -> failed` on retry exhaustion / `STARTING_TIMEOUT`.

Add a footnote: "Clients observing `resume_pending` after
`POST /start` should wait for subsequent `status_change` events or
`GET /v1/sessions/{id}` rather than treating `resume_pending` as a
precondition violation."

---

### SES-012 SSE Reconnect Storm Still Unmitigated (iter2 SES-008) [Low]
**Files:** `07_session-lifecycle.md:310-324`, `15_external-api-surface.md:137-143`

SES-008 recommended a per-session reconnect rate limit or per-reconnect
replay event cap. Neither has been added. §7.2 line 324 still only
describes the bounded-error `OutboundChannel` close behavior; §15.4 does
not document any server-side reconnect throttling. A slow client at
Tier 3 can sustain a replay-loop of up to 1200 s of EventStore history
per reconnect cycle, multiplied by consecutive cycles. This is not a
correctness issue (no data is corrupted) but it amplifies EventStore
load beyond what the capacity plan in §17.8 assumes for replay traffic.

**Recommendation (from iter2, restated):** add either
`maxReplayEventsPerReconnect` (e.g., 5000) with `checkpoint_boundary`
emission on cap, or `maxReconnectsPerMinute` (e.g., 10) with 429
`RECONNECT_RATE_LIMITED` response. Document the chosen option in §7.2
reconnect semantics and §15.4 SSE policy.

---

### SES-013 `resuming` Failure-Transition Inconsistency Between §7.2 and §6.2 [Medium]
**Files:** `07_session-lifecycle.md:177`, `06_warm-pod-model.md:116-120`

The iter2 rewrite of §7.2's session state machine (line 177) says:

```
resuming -> failed  (re-attach fails after retries exhausted)
```

But §6.2 lines 116-120 — which were authored as the authoritative
resuming-failure table — say:

```
resuming -> resume_pending        (pod crash / gRPC error during resume, retryCount < maxRetries)
resuming -> awaiting_client_action (pod crash / gRPC error during resume, retries exhausted)
resuming -> awaiting_client_action (resuming timeout: 300s, retries exhausted)
resuming -> resume_pending        (resuming timeout: 300s, retryCount < maxRetries)
```

Retries-exhausted in §6.2 goes to `awaiting_client_action`, not `failed`
(consistent with the normative text §7.3 line 370 "If retries exhausted
-> state becomes `awaiting_client_action`"). §7.2 line 177's
`resuming -> failed` is unreachable under normal semantics and
contradicts both §6.2 and §7.3.

There is one arguably-correct narrow path to `failed` from `resuming`
— the "Non-retryable errors during `resuming`" case in §6.2 line 227:
"transition directly to `awaiting_client_action` regardless of retry
count". But even that goes to `awaiting_client_action`, not `failed`.

**Recommendation:** Either:
1. Remove `resuming -> failed` from §7.2 line 177 entirely (retries
   exhausted path is already covered by
   `resume_pending -> awaiting_client_action` visible to clients).
2. If truly wanting to expose a `failed` exit from `resuming`, specify
   the distinct trigger (e.g., workspace checkpoint corruption with
   `cascadeOnFailure: failed`) and align §6.2 and §7.3 accordingly.

Option 1 is safer - the state machine should have a single rule.

---

### SES-014 `awaiting_client_action` Expiry Trigger Is Under-Specified [Low]
**Files:** `07_session-lifecycle.md:181, 383`

§7.2 line 181 declares:
```
awaiting_client_action -> expired  (lease/budget/deadline exhausted while awaiting client action)
```

§7.3 line 383 narrows this to `maxAwaitingClientActionSeconds` (default
900s) "This timer starts fresh on entry to `awaiting_client_action`".
But §6.2 line 263 in the `maxSessionAge` timer table says
`awaiting_client_action` is "Paused" - the session's age budget does
not advance. And `maxIdleTimeSeconds` is also paused (§6.2 line 262).

The "lease/budget/deadline exhausted" qualifier in §7.2 line 181 is
therefore ambiguous:
- `maxSessionAge`: paused, cannot fire
- `maxIdleTimeSeconds`: paused, cannot fire
- `perChildMaxAge` (delegation lease, wall-clock): can fire - this
  matches §6.2 line 269's "suspended -> expired trigger mechanism"
  which uses the same `perChildMaxAge` path
- `maxAwaitingClientActionSeconds`: can fire per §7.3 line 383

Only the last two can actually drive the transition. A reader looking
at §7.2 line 181 would reasonably assume `maxSessionAge` exhaustion
fires it, which it cannot.

**Recommendation:** Rewrite §7.2 line 181 as:
```
awaiting_client_action -> expired  (maxAwaitingClientActionSeconds fires,
                                    or delegation perChildMaxAge fires for
                                    delegation child sessions -- maxSessionAge
                                    and maxIdleTimeSeconds remain paused and
                                    cannot trigger this transition; see §6.2
                                    maxSessionAge timer table)
```

---

## Summary

- 3 iter2 regressions detected (SES-013 state machine contradiction between
  §7.2 and §6.2 for `resuming -> failed`)
- 3 missed items carried forward from iter2 (SES-006 -> SES-010, SES-007 ->
  SES-009, SES-008 -> SES-012)
- 2 new minor inconsistencies surfaced by iter2 additions (SES-011, SES-014)

No PARTIAL or SKIPPED areas. All session-lifecycle transition paths were
traced against §7.2, §6.2, §7.3, §15.1 preconditions, §4.4 eviction
fallback, §4.2 generation counters, and §10.1 partial-manifest path.
