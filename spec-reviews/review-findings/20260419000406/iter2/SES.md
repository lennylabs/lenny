### SES-004 `resumeMode` Enum Mismatch Between §7.2 and §10.1 [Medium]
**Files:** `07_session-lifecycle.md` (line 124), `10_gateway-internals.md` (line 120), `04_system-components.md` (line 263)

**Description:**
The `session.resumed` event schema is inconsistent across sections:

- **§7.2 line 124** (authoritative event table): `resumeMode` is `full | conversation_only`, plus boolean `workspaceLost`.
- **§4.4 line 263** (eviction fallback): `resumeMode: "conversation_only"` + `workspaceLost: true` — consistent.
- **§10.1 line 120** (partial-manifest path): `resumeMode: "partial_workspace"` + `workspaceRecoveryFraction` — a value not in the §7.2 enum and an extra field not declared in the event schema.

Clients validating strictly against the §7.2 enum will reject the partial-manifest variant.

**Recommendation:**
Extend §7.2 enum to `full | conversation_only | partial_workspace`, add optional `workspaceRecoveryFraction` (0.0–1.0) to the event schema, and cross-reference the §10.1 partial-manifest path. A distinct value is a more honest signal than overloading `full`.

---

### SES-005 `starting` Has No `resume_pending` Path for Mid-Start Pod Crashes [Medium]
**Files:** `07_session-lifecycle.md` (lines 141-177), `06_warm-pod-model.md` (lines 82-89, 235, 267-273)

**Description:**
The session-level state machine (§7.2) includes `running → resume_pending` and `input_required → resume_pending` for pod crashes, but omits `starting → resume_pending`. `starting` is externally visible (§15.1 line 232); the only documented exit is watchdog → `failed` via `STARTING_TIMEOUT`.

§6.2 line 89 shows `starting_session → failed`, and the "Pre-attached failure retry policy" (§6.2 lines 267-273) describes synchronous retry with 2 max retries on fresh pods. But:

1. §7.2's state machine exposes `starting` as a visible state with no retry-on-new-pod fork.
2. Pre-attached retry is "per client request, not per pod" — but `POST /start` is fire-and-forget; a pod crash 60s into `starting` isn't obviously covered by synchronous retry.
3. §15.1 preconditions (line 217) show only `ready → starting → running`.

This creates ambiguity around whether a pod crash during `starting` is recoverable via the same `resume_pending` path used by `running`.

**Recommendation:**
Add explicit transitions to §7.2 and §6.2:
- `starting → resume_pending` (pod crash / gRPC error during agent runtime launch, `retryCount < maxRetries`)
- `starting → failed` (retries exhausted or `STARTING_TIMEOUT`)

And state whether pre-attached retries produce a visible `resume_pending` or remain internal.

---

### SES-006 `resuming → cancelled/completed` Missing for Client Actions [Low]
**Files:** `06_warm-pod-model.md` (lines 115-119), `15_external-api-surface.md` (lines 219, 223)

**Description:**
§15.1 preconditions say `DELETE` and `/terminate` are valid in any non-terminal state. The external API reports `resuming` as `resume_pending`. Internally, however, §6.2 lists only `resuming → resume_pending` and `resuming → awaiting_client_action` as failure transitions and `resuming → attached` as success — no client-action exits.

This leaves undefined behavior if the client cancels while the gateway is actively restoring workspace on the replacement pod: does the gateway hold the cancel until `resuming → running`, or abort mid-tar-extract (leaving workspace state on the replacement pod's tmpfs)?

**Recommendation:**
Add `resuming → cancelled` and `resuming → completed` internal transitions, and specify cleanup behavior for partially-restored workspace on the replacement pod.

---

### SES-007 Derive Partial-Copy Contradicts §7.1 Atomicity Paragraph [Low]
**Files:** `07_session-lifecycle.md` (line 28, line 92)

**Description:**
SES-001 was resolved by specifying: "if the MinIO copy fails…the derived session record is marked `failed`." However, §7.1 line 28 atomicity paragraph explicitly states: "There is no `created → failed` transition because the session is never persisted in the `created` state until all preconditions are satisfied."

The derive partial-copy path persists a derived session record and then marks it `failed` — contradicting the atomicity rule. Two gaps remain:

1. **State-machine gap:** `failed` is normally reached from `running`, `input_required`, or `suspended`. Derive introduces an undocumented `pre-running → failed` path.
2. **Client response ambiguity:** When partial-copy fails, the `POST /derive` call returns — but with what body? Does the client receive a `session_id` already in `failed`, or an error response with no derived record persisted?

**Recommendation:**
Either (a) carve out the derive exception in §7.1 atomicity and add the pre-running→failed transition, or (b) delete the partial-derive record rather than persisting `failed`. Specify `POST /derive` response body for both outcomes.

---

### SES-008 SSE Reconnect Rate-Limit Still Undocumented (SES-003 Partial) [Low]
**Files:** `07_session-lifecycle.md` (line 317), `15_external-api-surface.md` (lines 137-143)

**Description:**
SES-003 flagged ambiguity around SSE reconnect throttling. The iter1 recommendation to document reconnect protection has not been incorporated. §7.2 line 317 still mentions only per-connection memory bounds; §15.4 says "The subscriber must reconnect" with no backoff requirement, no server-side reconnect rate limit, and no cap on consecutive buffer-overflow-close cycles.

At Tier 3, a misbehaving slow client can connect → fall behind → trigger 100ms bounded-error close → immediately reconnect → replay up to 1200s of EventStore history → repeat. This causes sustained EventStore read amplification disproportionate to normal slow-client degradation.

**Recommendation:**
Document one of:
1. Per-session reconnect rate limit (e.g., 10/min; 429 `RECONNECT_RATE_LIMITED` with `Retry-After`; counter resets after 60s stable).
2. Per-reconnect replay event cap (e.g., `maxReplayEventsPerReconnect: 5000`); exceeding emits `checkpoint_boundary` with `reason: replay_cap_exceeded`.

Either prevents reconnect storms from amplifying EventStore load.

