# Review Findings — Iteration 8, Perspective 11: Session Lifecycle & State Management

**Date:** 2026-04-07
**Spec:** `/Users/joan/projects/lenny/docs/technical-design.md` (8,649 lines)
**Prior SLC findings:** SLC-001 through SLC-030 (iterations 1–7)
**Category start:** SLC-031

---

## SLC-031 `finalizing` State Classified as `TARGET_TERMINAL` in Dead-Letter Routing Table [Medium]

**Location:** §7.2, dead-letter routing table (line ~2637)

**Problem:**

The dead-letter routing table for inter-session messages (`lenny/send_message`) contains this row:

| Target state | Behavior |
|---|---|
| `finalizing` | Message is rejected with `TARGET_TERMINAL` — session is transitioning to a completed terminal state and no further messages can be delivered. |

This is factually wrong on two levels:

1. **`finalizing` is not a terminal state.** §15.1's external state table explicitly lists `finalizing` with Terminal? = **No** ("Workspace materialization and setup commands in progress"). Terminal states are `completed`, `failed`, `cancelled`, `expired`. `finalizing` is a transient pre-running state.

2. **The error code is wrong.** `TARGET_TERMINAL` is defined in the §15.1 error catalog as: "Target task or session is in a terminal state." Returning `TARGET_TERMINAL` for a `finalizing` session tells the sender the session is permanently done, which is incorrect — the session may proceed to `running` within seconds. A sender that trusts this response will not retry even though a retry seconds later would succeed.

3. **The description in the row is wrong.** "transitioning to a completed terminal state" is not a valid description of `finalizing`. Workspace materialization and setup commands are not a completion sequence; they are a startup sequence.

**Correct behavior:** A `finalizing` session has not yet entered `running` state and has no inbox. It belongs in the `Pre-running` row alongside `created`, `ready`, and `starting`, which correctly returns `TARGET_NOT_READY` with the guidance "Client should retry after the session transitions to `running`."

**Impact:** Any sender that calls `lenny/send_message` targeting a session in `finalizing` state receives a `TARGET_TERMINAL` error and believes the session is permanently done. If the sender is a delegation parent or sibling, it may incorrectly fail or cancel its own coordination logic based on a false terminal report. The session in fact continues to `running` normally, but messages sent during the `finalizing` window are silently lost rather than being retried.

**Fix:** Remove the `finalizing` row from the dead-letter table. Add `finalizing` to the `Pre-running` row so it reads:

> Pre-running (`created`, `ready`, `starting`, `finalizing`) — Message is rejected with `TARGET_NOT_READY` — session has not yet entered `running` state and has no inbox. Client should retry after the session transitions to `running`.

---

## SLC-032 `maxSessionAge` Timer Table References Internal-Only `attached` State, Omits External `starting` State [Medium]

**Location:** §6.2, `maxSessionAge` timer behavior table (line ~2251)

**Problem:**

The `maxSessionAge` timer table includes a row for `attached`:

> `attached` | **Running.** The session has just attached and is about to transition to `running`; timer runs.

But §15.1 explicitly classifies internal-only pod states that are "never returned in external API responses." The external state table (§15.1) does not include `attached`. `attached` is a pod-level concept from §6.2's pod state machine — it appears as the root node of session transitions in the pod state diagram, not as a named session state visible to clients or tracked in the session timer logic.

Meanwhile, `starting` is listed in the external state table as a real externally visible state ("Agent runtime is launching", Terminal? = No) but has **no row** in the `maxSessionAge` timer table.

This creates two concrete problems:

1. **`starting` timer behavior is undefined.** A session in `starting` state has no specified `maxSessionAge` behavior. Implementors must guess whether the timer runs (counting toward the session age budget) or is paused. Given that `starting` represents the agent runtime launching — active gateway work — it should presumably run, but this is not stated.

2. **`attached` in the timer table creates a layer confusion.** The timer table purports to define session-layer timer behavior, but `attached` is a pod-layer state. Including it in the session timer table implies it is a distinct session state with its own timer semantics, which contradicts the state visibility model in §15.1.

**Impact:** Ambiguity about `starting` state timer behavior means two independent implementors of the gateway timer subsystem may choose opposite behaviors. If `starting` counts toward `maxSessionAge` and the agent binary is slow to initialize (e.g., downloading a large model), sessions can be expired before the agent is ever ready — invisible to the client who only sees the `starting → expired` transition. If `starting` is paused incorrectly, a hung `starting` state holds a warm pod and credential lease indefinitely with no age-based reclamation.

**Fix:** Replace the `attached` row with a `starting` row that specifies the timer behavior explicitly. The most consistent choice is **Running** (startup time counts toward session age, consistent with the session being dispatched and a pod actively claimed), but the spec must make this explicit. The row should read:

> `starting` | **Running.** The agent runtime is launching; elapsed time counts toward `maxSessionAge`. A session stuck in `starting` that exceeds `maxSessionAge` is transitioned to `expired`.

Apply the same fix to the `maxIdleTimeSeconds` timer table (§6.2, line ~2262), which also lacks a `starting` row.

---

## SLC-033 `ready` and `starting` External Session States Have No Bounded Lifetime [Medium]

**Location:** §6.2 timer tables; §15.1 external state table; §7.1 normal flow

**Problem:**

The `created` state has a documented TTL: `maxCreatedStateTimeoutSeconds` (default 300s), which expires the session and releases the pod claim and credential lease. The `ready` and `starting` states have no equivalent bound.

Examining the spec:
- No `maxReadyStateTimeoutSeconds` or equivalent exists.
- No `maxStartingStateTimeoutSeconds` or equivalent exists.
- Neither state appears in the `maxSessionAge` timer table (SLC-032 addresses the missing `starting` row, but even if `starting` is added as **Running**, `maxSessionAge` is measured from session creation and defaults to 2h — not a tight bound on pre-run state duration).
- The `maxIdleTimeSeconds` timer only applies to the `running` state and measures agent output silence; it does not apply to pre-running states.

**Concrete resource leak scenarios:**

1. **`ready` state leak.** A client calls `POST /v1/sessions` and `POST /v1/sessions/{id}/finalize` but then abandons the session without calling `POST /v1/sessions/{id}/start`. The session enters `ready` and stays there indefinitely. A warm pod is held claimed (unavailable to other sessions) and a credential lease is held active (consuming pool quota) for up to 2h (the `maxSessionAge` default), with no tighter reclamation. In high-throughput environments, abandoned `ready` sessions silently exhaust both pool capacity and credential pool slots.

2. **`starting` state hang.** The adapter initiates `StartSession` on the pod but the agent binary hangs during initialization (e.g., model download failure, runtime deadlock). The session enters `starting` and never transitions to `running`. Without a timeout on `starting`, the gateway has no mechanism to detect this condition. The pod-level `starting_session` state has no watchdog defined in §6.2. The orphan reconciler (§10.1) only handles `running`/`attached` states whose pod has terminated; it does not cover hanging `starting` state where the pod is alive but unresponsive.

**Contrast with `created` state:** The spec explicitly addresses the `created` state leak risk and provides `maxCreatedStateTimeoutSeconds`. The analogous risk for `ready` and `starting` is equally real (the pod and credential lease are already claimed in all three states) but goes unaddressed.

**Fix:** Define bounded lifetimes for `ready` and `starting`:

- **`ready` state:** Add `maxReadyStateTimeoutSeconds` (suggested default: 300s, same as `maxCreatedStateTimeoutSeconds`). On expiry, transition to `expired`, release the pod claim, revoke the credential lease. Document the configuring parameter in the §17.8 operational defaults table.

- **`starting` state:** Add `maxStartingStateTimeoutSeconds` (suggested default: 60s, matching the `resuming` watchdog and `sdk_connecting` watchdog defaults). On expiry, transition to `failed` (not `expired` — this is an infrastructure failure, not a client deadline), release the pod, and return a retryable error. Document in §17.8.

Both parameters should appear in the §6.2 timer tables and in the §15.1 external state table descriptions alongside the existing `created` TTL note.
