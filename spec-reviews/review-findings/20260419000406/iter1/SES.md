### SES-001 Derive Lock Release Timing Vulnerability [Medium]
**Files:** `07_session-lifecycle.md` (line 92), `04_system-components.md` (checkpoint atomicity sections)

**Description:**
The derive endpoint acquires an advisory lock on the source session's workspace snapshot reference to prevent torn reads during concurrent checkpoints (§7.1, line 92: "Redis `SETNX` on key `derive_lock:{source_session_id}`, TTL 30 seconds"). The spec correctly states the lock is released immediately after reading the snapshot reference, before the actual copy begins. However, the specification does not explicitly cover the race condition window between:

1. Lock released (derive has the MinIO key)
2. Derive issues the MinIO copy request
3. New checkpoint completes and attempts to write a new reference to the session record

The spec states: "each checkpoint write creates a new MinIO object at a new path — it never overwrites the previously stored object in place. Once the lock has been released after reading the snapshot reference, that reference resolves to a stable, immutable MinIO object key that cannot be mutated by any concurrent checkpoint" (§7.1).

**The inconsistency:** While the statement about "new paths" is true for the MinIO object itself, the spec does not explicitly guarantee that the **session record's snapshot reference field** (which points to which MinIO object is the "current" checkpoint) is not updated by a concurrent checkpoint during the derive copy phase. If a concurrent checkpoint completes, updates the session record with a new reference, and the old snapshot is garbage-collected before the derive copy completes, the derive could fail with `503 DERIVE_SNAPSHOT_UNAVAILABLE` (acknowledged as possible in the spec). However, the spec is ambiguous about whether this race is **acceptable failure semantics** (best-effort derive with retry) vs. **lost derive data** (partial copy with orphaned MinIO objects).

**Cross-section gap:** Section 12.5 (Artifact Store) and Section 4.4 (checkpoint atomicity) do not explicitly state whether GC considers "in-flight derive copies" when deciding to delete snapshots, or whether derives must complete within a TTL bounded by periodic checkpoint intervals.

**Recommendation:** 
Clarify one of two positions:
1. **Explicit acceptance:** "Derives may fail transiently if the source snapshot's reference is updated by a concurrent checkpoint after the lock is released but before the derive copy completes. Clients MUST retry the entire derive operation; the gateway does not resume partial copies."
2. **Strong guarantee:** "The gateway must NOT garbage-collect a snapshot object that is concurrently being derived. A snapshot is considered 'in-flight' if a derive operation has read its reference within the last `<maxDeriveTimeoutSeconds>` (e.g., 600s). GC skips in-flight snapshots until the timeout elapses."

Recommend position 1 (transient failure acceptable) + explicit guidance in the derive response to clients that `503 DERIVE_SNAPSHOT_UNAVAILABLE` is retriable and does not indicate corruption.

---

### SES-002 SIGSTOP Checkpoint Mid-Interrupt Race Not Fully Characterized [Low]
**Files:** `04_system-components.md` (lines 242-250), `06_warm-pod-model.md` (lines 187-201)

**Description:**
The spec describes checkpoint/interrupt mutual exclusion (§4.7, line 614-618): the adapter maintains a per-session operation lock serializing `Checkpoint` and `Interrupt` RPCs. However, the spec does not fully characterize the SIGSTOP path's interaction with interrupt-induced state changes. Specifically:

- In embedded adapter mode, `SIGSTOP` directly freezes the agent process without a handshake
- If a lifecycle-channel `interrupt_request` is in flight but not yet acknowledged when `SIGSTOP` is issued, the agent is frozen mid-interrupt-acknowledgment, in an undefined state
- The spec states (§4.4, line 242) that SIGSTOP is "not supported under gVisor or Kata", which implies it's only used in embedded mode with runc
- But the spec does not explicitly state whether the operation lock (checkpoint/interrupt mutual exclusion) protects against concurrent `SIGSTOP` from two different actors (e.g., periodic checkpoint + eviction checkpoint) on the same session

**Cross-section gap:** Section 6.2 (pod state machine) shows `input_required` can transition to various states including `resume_pending` on pod crash. If a pod crash occurs during SIGCONT confirmation polling (§4.4, line 246), it's unclear whether the process is left in state `T` (stopped) when the pod restarts, or whether restart always clears the stopped state.

**Recommendation:**
Add a sentence: "In embedded adapter mode, the operation lock ensures that at most one checkpoint or interrupt operation is in flight at a time. A `SIGSTOP` for periodic checkpoint cannot race with an interrupt-acknowledgment because the checkpoint queuing discipline (max one queued operation) prevents `SIGSTOP` from being issued until any in-flight interrupt settles. If a pod crash occurs while `SIGCONT` confirmation polling is in progress, the new pod always starts the agent process in the running state (not stopped), so no process-state cleanup is required on pod restart."

---

### SES-003 SSE Buffer Overflow & Slow-Client Semantics Ambiguity [Low]
**Files:** `07_session-lifecycle.md` (line 317), `15_external-api-surface.md` (lines 122-163)

**Description:**
The spec defines SSE back-pressure policy (§7.2, line 317): "bounded-error `OutboundChannel` policy" with non-blocking write timeout. If write would block, the connection is closed and the client must reconnect with last-seen cursor. The spec states missed events are replayed from EventStore "if within the replay window" (line 303-315).

However, the spec does not clarify:
1. **Buffer size semantics:** When exactly is the buffer "full"? Is it per-message count, per-byte size, or per-goroutine work queue depth?
2. **Closed-loop feedback:** If a slow client causes SSE buffer to fill and the connection is closed, what prevents the same client from immediately reconnecting and causing the same overload? There's no rate-limiting on reconnects documented.
3. **Interaction with inbox overflow:** A slow SSE subscriber causes buffer overflow → connection close → reconnect. Meanwhile, the session's inbox may also overflow (§7.2, line 236: "oldest message dropped"). Are the overflow thresholds coordinated?

**Cross-section inconsistency:** The spec states OutboundChannel implementations "MUST NOT block the caller for more than MaxOutboundSendTimeoutMs (default: 100 ms)" (§15.4, line 146-147) but also that the buffer can hold messages (§15.4, line 127: "in-memory event buffer with a maximum depth"). If the buffer is bounded and has a max depth, the timeout can be hit before any Send returns — this is consistent. But the spec does not state whether MessageEnvelope events for a single logical session event (e.g., a streaming `agent_output` with multiple parts) are atomic or can be interleaved if the buffer fills mid-event.

**Recommendation:**
Clarify:
1. Buffer capacity units: "The buffer depth limit applies per OutboundChannel instance (per session), measured in event count (not byte size). Default: 1000 events per session."
2. Reconnect protection: "Clients that reconnect immediately after SSE closure inherit the same OutboundChannel instance (if the session is still in-progress) or a fresh one (if resuming). The gateway does not rate-limit reconnects, but the EventStore replay window (default 1200s) caps the amount of history a single reconnect can cause to be replayed; multiple rapid reconnects from the same client do not cause cascading replays of the same events."
3. Event atomicity: "Session events that consist of multiple streaming frames (e.g., multi-part `agent_output`) are NOT atomic with respect to buffer overflow. If the buffer fills mid-frame, subsequent frames may be dropped or the connection closed. Clients MUST implement deduplication based on event ID to detect and recover from partial events on reconnect."

