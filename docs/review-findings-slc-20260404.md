# Session Lifecycle & State Management Review Findings — 2026-04-04

**Document reviewed:** `docs/technical-design.md`
**Perspective:** 11. Session Lifecycle & State Management
**Category code:** SLC
**Reviewer focus:** Session and pod state machine correctness, generation counter split-brain prevention, checkpoint failure mid-SIGSTOP, session derive (workspace snapshot and credential state), SSE buffer overflow semantics.

---

## Summary

| Severity | Count |
|----------|-------|
| Critical | 2 |
| High     | 5 |
| Medium   | 6 |
| Low      | 3 |
| Info     | 1 |

---

## Critical

### SLC-001 `resuming` State Has No Failure Transition — Potential Deadlock [Critical]
**Section:** 6.2

The pod/session state machine diagram shows `resuming` as a transient state that leads only to `attached`. There is no defined transition from `resuming` to `resume_pending`, `awaiting_client_action`, or any failure state. This means that if a pod failure, gRPC error, or workspace restoration hang occurs while the gateway is attempting to restore a session onto a new pod, the session is stuck in `resuming` with no documented path forward. The spec defines a timeout for `resume_pending` and `resuming` (SES-011 in the existing findings) as a gap but does not specify the failure transition itself — even if a timeout is added, the destination state on timeout is undefined.

Unlike `resuming`, every pre-attached pod state (`warming`, `sdk_connecting`, `receiving_uploads`, `running_setup`, `finalizing_workspace`, `starting_session`) has an explicit `→ failed` edge. The asymmetry leaves `resuming` as a potential deadlock sink: a session that enters `resuming` on retry #2 and then encounters another pod failure has nowhere to go.

**Recommendation:** Add an explicit `resuming → resume_pending` transition (triggers another retry attempt if `retryCount < maxRetries`) and a `resuming → awaiting_client_action` transition (triggers when retries are exhausted while in `resuming`). Add a `resuming` timeout (recommended: 300s, the same as setup command timeout) that fires both paths. Document the state machine transition in Section 6.2 with the same notation used for pre-attached failures.

---

### SLC-002 Generation Counter Increment Committed Before `CoordinatorFence` — Window for Stale Coordinator to Proceed [Critical]
**Section:** 10.1

The coordinator handoff protocol (Section 10.1) requires:
1. Atomically increment `coordination_generation` in Postgres.
2. Send `CoordinatorFence(session_id, new_generation)` RPC to the pod.
3. Begin coordination.

There is no specified behavior if step 2 fails — either the pod is unreachable, the RPC returns an error, or the pod rejects the fence. In that scenario the generation has already been incremented in Postgres (step 1 is committed), but the pod still accepts RPCs from the previous coordinator's generation, because the fence was never delivered. The new replica holds the lease and the new generation, but cannot proceed because the pod rejected or never received the fence. The old coordinator is now stale relative to Postgres but the pod still accepts its RPCs because the pod's locally-recorded generation is the old value (the fence never arrived). This is a real split-brain window: both replicas can send RPCs to the pod simultaneously and the pod will accept RPCs from the old coordinator until it detects the generation mismatch organically, which requires the old coordinator to send a generation-bearing RPC that the pod rejects.

The stale replica behavior section (Section 10.1) describes what the old coordinator should do once it *receives* a generation-stale rejection, but that rejection can only happen if the old coordinator sends an RPC *after* the fence is delivered. Before the fence delivery is confirmed, the old coordinator has no signal to stop. If the `CoordinatorFence` RPC is delayed (network partition, pod backpressure), the window can be several seconds.

**Recommendation:** Specify that step 2 (fence delivery) must complete successfully before the new coordinator sends any operational RPCs. If `CoordinatorFence` fails or times out (recommended: 5s timeout), the new coordinator must either retry the fence or relinquish the lease (by not extending the Redis TTL and releasing the Postgres lock). Add this to the handoff protocol as an atomic precondition: "fence must be acknowledged before coordination begins." The pod's fence acknowledgement response to the gateway closes the split-brain window. Without this, the generation increment provides eventual-consistency protection but not immediate protection during the fence delivery gap.

---

## High

### SLC-003 SIGSTOP Watchdog (60s) Overlaps With Storage Retry Window (~5s) — Gap Not Documented [High]
**Section:** 4.4

The embedded adapter path sends SIGSTOP to quiesce the agent, then:
- Runs `tar` of `/workspace/current`
- Uploads to MinIO with exponential backoff (initial 200ms, factor 2x, up to ~5 seconds total)

The watchdog timer fires at 60 seconds and sends SIGCONT unconditionally. The spec says the watchdog "starts when SIGSTOP is sent." For 500MB workspaces the tar creation alone can take 5–10 seconds (Section 4.4, duration SLO section), meaning:

`tar_time (5–10s) + upload_retries (~5s) = 10–15s` under normal conditions. This is well within the 60s window.

However, the spec does not document what happens if MinIO is returning slowly (not timing out, just slow) and the tar + all upload attempts together approach 60 seconds. In that case the watchdog fires mid-upload — `SIGCONT` is sent, the agent resumes, the checkpoint is marked failed, but the incomplete tar archive may have already been partially written to MinIO. The spec says "checkpoint is discarded" on watchdog fire, but does not specify whether the partial MinIO upload is cleaned up, or whether the partial object could be mistaken for a valid checkpoint if the metadata write races with the cleanup.

There is a separate atomicity guarantee ("metadata record in Postgres references both artifacts and is written only after both are successfully uploaded"), which would protect against mistaking a partial upload for a valid checkpoint — but this requires the Postgres metadata write to not have happened yet, which is the expected case if the upload is still in progress. The spec is correct on atomicity but does not explicitly state that the partial MinIO object is deleted on watchdog-triggered abort.

**Recommendation:** Add an explicit statement to the checkpoint failure recovery path (Section 4.4): "When the watchdog fires and the checkpoint is aborted, any partially uploaded MinIO objects for that checkpoint attempt are deleted using the MinIO abort-multipart-upload API or by deleting the partially written object. The checkpoint metadata record in Postgres is never written for an aborted checkpoint." This closes the ambiguity about partial object cleanup and makes the atomicity guarantee complete.

---

### SLC-004 `POST /v1/sessions/{id}/derive` Allowed on Active Sessions — Credential and Connector State Undefined [High]
**Section:** 19 #12, 7.1

The `POST /v1/sessions/{id}/derive` endpoint is described as creating a new session "pre-populated with this session's workspace snapshot." The spec does not define:

1. **Which source session states permit derive.** The endpoint is listed in the REST API table without a state pre-condition. A client can call it on a `running`, `suspended`, `resume_pending`, `resuming`, or `awaiting_client_action` session. The behavior for `running` is especially concerning: the workspace snapshot used would be the last *checkpointed* snapshot, not the current live workspace (which may have diverged significantly). The client has no indication that the derived session starts from stale workspace state. For `failed` sessions, it is not clear whether derive uses the last successful checkpoint or the failed-session workspace.

2. **Credential lease for derived session.** The spec (Section 19 #12, gap SES-006) notes this is undefined. Specifically: if the source session holds LLM provider credentials from a credential pool, does the derived session get a new independent lease from the same pool? What if the pool is exhausted? Does the derive operation fail, or does it succeed but the derived session gets no credentials? The `CredentialPolicy` evaluation that normally happens at session creation (step 6 in Section 7.1) is not mentioned in the derive flow.

3. **Connector OAuth tokens for derived session.** If the source session had active connector MCP servers with valid OAuth tokens, the derived session gets the same workspace files but what about connector authorization? The MCP connector servers are per-session. The derived session must go through the full connector authorization flow independently. The spec does not state this explicitly, which could lead implementers to assume connector state is inherited.

**Recommendation:** Add a `POST /v1/sessions/{id}/derive` specification section covering: (a) allowed source states (`completed` recommended; `failed` with explicit note that workspace is from last checkpoint; `running`/`suspended` explicitly rejected or clearly documented as checkpoint-based); (b) credential lease handling (derive triggers a fresh `CredentialPolicy` evaluation; fails with `CREDENTIAL_POOL_EXHAUSTED` if unavailable); (c) connector state (not inherited — derived session starts with no active connector tokens; connector re-authorization required by client).

---

### SLC-005 `suspended → resume_pending` Transition Missing [High]
**Section:** 6.2, 7.2

The session state machine (Section 6.2) shows `suspended → running` and `suspended → completed`, and Section 7.2 adds `suspended → cancelled` and `suspended → expired`. But there is no `suspended → resume_pending` transition.

A pod eviction, node drain, or runtime crash can occur while a session is in the `suspended` state (the pod is held but the agent is paused). When this happens the pod enters a failed state at the Kubernetes level, but the session state machine has no edge from `suspended` to `resume_pending`. The spec describes `resume_pending` as the state entered when a pod failure is detected for an `attached` session, but does not extend this to the `suspended` state. Implementations could either: (a) silently transition to `failed` (discarding the suspension context and losing the checkpoint from which resume should proceed), or (b) attempt to resume directly to `running` bypassing `suspended`, losing the interrupt context.

The `suspended` state preserves the `maxSessionAge` timer pause and the intent for the runtime to resume from an interrupted position. A clean resume should return to `suspended` (or at minimum `running` if the runtime cannot distinguish), not silently terminate.

**Recommendation:** Add `suspended → resume_pending` as an explicit transition triggered by pod failure detection while in `suspended` state. On successful recovery (`resuming → attached`), transition directly to `running` (the interrupt context cannot be recovered, but the session and workspace can). Document the loss of interrupt context as an expected limitation.

---

### SLC-006 `heartbeat_timeout` Referenced But Undefined — Dangling Section Reference [High]
**Section:** 10.1

Section 10.1 states: "the pod's `heartbeat_timeout` (Section 7.3) will fire, and the pod will enter a hold state awaiting a new coordinator." Section 7.3 is "Retry and Resume" and does not define `heartbeat_timeout` or a pod "hold state." No section in the spec defines:
- The gateway-to-pod heartbeat mechanism (separate from the agent-binary-level `heartbeat`/`heartbeat_ack` protocol in Section 15.4.1 which is an adapter-to-binary mechanism)
- The `heartbeat_timeout` value
- What "hold state" means for the pod (does it stop accepting RPCs? queue them? terminate the session?)
- How the pod signals that it is in a hold state back to the gateway

Without this, the behavior of active sessions during a gateway crash + dual-store-unavailability scenario is unspecified. A pod that loses its gateway coordinator and cannot receive a new `CoordinatorFence` has undefined behavior.

**Recommendation:** Define a gateway-to-pod heartbeat mechanism in Section 4.7 (Runtime Adapter) or 10.1: the adapter pings the gateway every N seconds (recommended: 15s) over the gRPC control channel; if no response within M seconds (recommended: 45s, i.e., 3 missed heartbeats), the adapter enters a "coordinator-lost" hold state where it continues running the agent but queues all outbound events and blocks all inbound operational RPCs (checkpoint, interrupt, terminate). Define `heartbeat_timeout` as the adapter configuration field controlling M. Fix the Section 7.3 reference to point to wherever `heartbeat_timeout` is defined (likely Section 4.7). Specify hold-state egress: if a new `CoordinatorFence` arrives before `maxHoldSeconds` (recommended: same as `maxResumeWindowSeconds`, 900s), the adapter flushes the queued events and resumes; if not, the adapter terminates the session and marks the pod as failed.

---

### SLC-007 `retry_exhausted` in State Machine Diagram Is Not a Defined State [High]
**Section:** 6.2, 7.2, 7.3

The state machine diagram in Section 6.2 shows `retry_exhausted / expired` as a named state that transitions to `draining`. However:

1. The terminal states listed in Section 7.2 are: `completed`, `failed`, `cancelled`, `expired`. `retry_exhausted` is not in this list.
2. Section 7.3 uses `awaiting_client_action` as the post-retry-exhaustion state, not `retry_exhausted`.
3. The canonical task state machine in Section 8.9 does not include `retry_exhausted` at all.
4. `draining` appears in the pod state label table (Section 6.2) but is not in the session state machine — it is a pod-level state, not a session-level state.

The diagram conflates pod-level and session-level states. The `awaiting_client_action` state is entirely absent from the diagram. `retry_exhausted` appears only in the diagram and is inconsistent with the prose. The `draining` state in the diagram is a pod lifecycle concept, not a session concept.

**Recommendation:** Redraw the session state machine in Section 6.2 to:
- Replace `retry_exhausted` with `awaiting_client_action` (consistent with Section 7.3 prose)
- Remove `draining` from the session state machine (it belongs in the pod state machine)
- Add `cancelled` as a terminal state reachable from `running`, `suspended`, and `resume_pending`
- Add `resume_pending → awaiting_client_action` for retry exhaustion
- Ensure the diagram is consistent with the terminal state list in Section 7.2

---

## Medium

### SLC-008 SSE Buffer Overflow Drop Is Silent — Client Cannot Distinguish From Network Loss [Medium]
**Section:** 7.2

When the gateway drops an SSE connection due to a full buffer, the client receives a connection close with no indication that the close was intentional. The client SDK must reconnect and supply its last-seen cursor to replay missed events, but without a prior signal it cannot know whether the connection was lost due to: (a) gateway restart/failover, (b) network interruption, (c) buffer overflow. The behavior on reconnect differs for case (c): in the buffer overflow case, the events are definitively in the EventStore up to the buffer's high-watermark, so replay is reliable. In cases (a) and (b), events between the last cursor and the current time may or may not be in the EventStore.

The spec states "events beyond the buffer are replayed from the EventStore on reconnect (if within the checkpoint window)" — this is the correct behavior, but a client that handles connection drops aggressively (with short backoff and immediate reconnect) will work correctly. A client that relies on the drop signal to distinguish recovery paths (e.g., a mobile client that throttles reconnects) has no information to act on.

The spec does not define: what `checkpoint_boundary` looks like as a schema (it is mentioned but not specified), whether the EventStore replay covers the full buffer or only events after the cursor, or how the client should handle the case where the cursor is beyond the EventStore window.

**Recommendation:** (a) Specify that the gateway sends a final `{"type": "buffer_overflow", "cursor": "<last_persisted_cursor>"}` event on the SSE stream before dropping the connection. This event is best-effort (the buffer is full so it may not be sendable, but the attempt should be documented). (b) Define the `checkpoint_boundary` event schema: `{"type": "checkpoint_boundary", "lastAvailableCursor": "<cursor>", "sessionState": "<current_state>"}`. (c) Specify that on reconnect with a cursor older than the EventStore window, the client receives the `checkpoint_boundary` event and then the full current session state — not a replay of unavailable events.

---

### SLC-009 Full-Tier Checkpoint: Runtime Autonomously Resumes But Gateway Not Notified [Medium]
**Section:** 4.4

The Full-tier lifecycle channel path specifies: "if the runtime sends `checkpoint_ready` but does not receive `checkpoint_complete` within 60 seconds, it MUST autonomously resume normal operation." This autonomous resume is entirely internal to the runtime — the gateway/adapter is not specified to receive any signal. The adapter may be in an indeterminate state: it sent `checkpoint_request`, received `checkpoint_ready`, is currently uploading the snapshot, and then the runtime autonomously resumes. The adapter now has a running runtime and an in-flight snapshot upload. The spec does not specify:

1. Whether the adapter aborts the in-flight upload and marks the checkpoint failed when the runtime resumes.
2. Whether the adapter continues the upload (completing it after the runtime has resumed, potentially creating an inconsistent checkpoint that future recovery might use).
3. Whether the runtime emits any signal (`checkpoint_timeout` is a log message per the spec, not a lifecycle channel message) that the adapter can act on.

If the adapter continues the upload after the runtime resumes, the resulting checkpoint is taken at time T but the workspace may have been modified between T and upload completion, violating the atomicity guarantee.

**Recommendation:** Add a `checkpoint_timed_out` lifecycle message from the runtime to the adapter (Runtime → Adapter direction in the lifecycle channel) that fires when the 60-second timeout triggers and the runtime autonomously resumes. On receiving this message, the adapter MUST abort the in-flight snapshot upload, discard all partially collected snapshot data, and mark the checkpoint as failed. This closes the window where an invalid post-resume snapshot could be persisted as a checkpoint.

---

### SLC-010 `awaiting_client_action` — Resume Override Semantics Unspecified [Medium]
**Section:** 7.3

Section 7.3 lists "Resume anyway (explicit override)" as a client action after retry exhaustion. This creates an `awaiting_client_action → resuming` transition. However, the spec does not define:

1. **Which checkpoint is used.** Is it the last successful checkpoint before the failures? Or the last checkpoint before retry exhaustion? If multiple failed retry attempts each produced partial checkpoints (e.g., the eviction checkpoint upload failed per Section 4.4), what does "resume from checkpoint" mean?
2. **Whether the resume override resets the retry counter.** If the client explicitly resumes and the pod fails again, does it get another 2 retries or is it immediately `awaiting_client_action` again?
3. **Interaction with `maxSessionAge`.** The `maxSessionAge` timer presumably continued running during the `awaiting_client_action` period (Section 6.2 says the timer is only paused during `suspended`). If the client resumes after 1800s of `awaiting_client_action` and `maxSessionAge` is 7200s, the resumed session has only 5400s of budget remaining. This should be explicitly documented.

**Recommendation:** Add to Section 7.3: (a) explicit override uses the last successful checkpoint (identified by the checkpoint record in Postgres — `checkpoint_failed` records are skipped); (b) explicit override resets retry count to 0 for the new attempt (the human override implies fresh consent to retry); (c) `maxSessionAge` continues during `awaiting_client_action` and the resumed session's remaining age is `maxSessionAge − elapsed_total_wall_clock`.

---

### SLC-011 Workspace Snapshot Used by `derive` Is Checkpoint-Based, Not Live [Medium]
**Section:** 7.1, Section 19 #12

`POST /v1/sessions/{id}/derive` uses "this session's workspace snapshot." For completed sessions, this is the sealed workspace (the final state — correct). For sessions in any other state, the workspace snapshot is the last *checkpoint* snapshot, which may be arbitrarily stale compared to the live pod workspace.

The spec says in Section 4.5: "workspace snapshots are full workspace captures at checkpoint or seal time." This means a `running` session with a 30-minute-old checkpoint and significant subsequent file mutations would produce a derived session with a 30-minute-stale workspace. The client is not informed of the checkpoint timestamp, creating a confusing experience where the derived session appears to "miss" recent work.

**Recommendation:** For `derive` called on a non-terminal session: (a) the response should include `workspaceSnapshotTimestamp` so the client knows the age of the snapshot, (b) optionally, the gateway can trigger an on-demand checkpoint before materializing the derive snapshot (similar to the pre-scale-down checkpoint trigger), with a response parameter `checkpointBeforeDerive: boolean` (default: false, to avoid unexpected pauses on Full-tier runtimes). Document clearly that for non-terminal sessions, derive is always based on the most recent successful checkpoint.

---

### SLC-012 SDK-Warm State Machine Missing Failure Transition for `sdk_connecting → claimed` on Demotion [Medium]
**Section:** 6.2

The SDK-warm pod state machine shows:
```
warming → sdk_connecting → idle → claimed → receiving_uploads → finalizing_workspace → attached
```

And separately shows that `sdk_connecting → failed` is a pre-attached failure transition. However, the demotion path (Section 6.1) is: a claimed SDK-warm pod receives `DemoteSDK` RPC, tears down the pre-connected SDK process, and "transitions the pod back to `idle`." After demotion, the pod follows the pod-warm path: `receiving_uploads → finalizing_workspace → running_setup → starting_session → attached`.

The issue is: the pod state machine diagram for the SDK-warm path does not include `running_setup` or `starting_session`. After demotion, the pod is on the pod-warm path and must pass through states that the SDK-warm diagram omits. The `DemoteSDK` RPC also has a failure mode (`UNIMPLEMENTED` per Section 6.1) and a timeout (10s), but neither has a documented state transition. A demotion failure leaves the pod in `claimed` with an unknown state — neither the SDK-warm path nor the pod-warm path applies.

**Recommendation:** In Section 6.2, add an explicit demotion failure transition: `claimed (post-demotion-failure) → failed`. Add `claimed → idle (demotion-initiated) → receiving_uploads → running_setup → starting_session → attached` as the post-demotion recovery path on the SDK-warm diagram. Clarify that after successful demotion, the pod follows the pod-warm path from `receiving_uploads` onward.

---

### SLC-013 Concurrent-Mode Slot Failure Does Not Define Session-Level Impact [Medium]
**Section:** 5.2

Section 5.2 (concurrent mode) describes slot-level failure isolation: "the adapter marks that `slotId` as `failed`." However, the session-level state machine has no `slot_failed` state or event. The spec does not define:

1. How many slot failures constitute a session-level failure.
2. Whether the gateway's session state changes when a slot fails (or whether it remains `attached` indefinitely until all slots are occupied by failed tasks).
3. How `cascadeOnFailure` interacts with slot failures — does a failed slot trigger `cancel_all` across all other slots?
4. Whether a session in `attached` state with all slots failed is equivalent to a `failed` session for client-facing purposes.

**Recommendation:** Add to Section 5.2: a session with all slots in `failed` state transitions to `failed` session state. A session with some slots `failed` and some `running` remains `attached` but emits a `slot_failed` event per failed slot. `cascadeOnFailure` applies at the session level when the session itself enters `failed`, not on individual slot failures. The gateway's `child_failed` event (Section 8.11) does not apply within a single session's slots — slots are not separate sessions.

---

## Low

### SLC-014 `maxSessionAge` Timer Paused During `suspended` But Resume Window Not Constrained [Low]
**Section:** 6.2

Section 6.2 states "pod held, workspace preserved, `maxSessionAge` timer paused while suspended." This means a session can remain in `suspended` indefinitely without consuming its age budget. The spec does not define a maximum suspend duration. An agent that is interrupted and never resumed consumes a pod (held state) and a credential lease (Section 7.1 step 23 shows the lease is released after `completed`/termination, not on `suspended` → `running`). The credential lease holds pool capacity indefinitely during `suspended` state.

**Recommendation:** Add a `maxSuspendedTime` parameter (default: 1800s, deployer-configurable) to the session lifecycle configuration. A session that remains in `suspended` beyond this duration transitions to `expired`. This mirrors the `awaiting_client_action` expiry pattern and prevents indefinite resource hold.

---

### SLC-015 No Event for `resume_pending → resuming` Transition [Low]
**Section:** 7.2

The session event stream (Section 7.2) includes `status_change(state)` for session state transitions including `suspended`. However, `resume_pending` and `resuming` are defined in the pod/session state machine (Section 6.2) but are not listed as values for `status_change`. A client watching the event stream during a session recovery would see the stream go silent (disconnection) and then either reconnect to a `running` session (recovery succeeded) or reconnect to an `awaiting_client_action` session (retries exhausted). The intermediate `resume_pending` and `resuming` states are invisible to the client, which may lead to unnecessary session abandonment or UI confusion during recovery.

**Recommendation:** Add `resume_pending` and `resuming` to the set of states emitted by `status_change` events. Since the SSE stream may be disconnected during recovery, these events should also be replayable from the EventStore (they should be persisted, not only emitted in-stream). This gives clients polling `GET /v1/sessions/{id}` the ability to observe the recovery progression without relying on the live event stream.

---

### SLC-016 `draining` Pod State Has No Defined Timeout — Indefinite Resource Hold [Low]
**Section:** 7.1

Section 7.1 states: "If export fails, the pod is held in `draining` state with a retry." Section SES-014 in the existing findings identifies unbounded retries as a gap. Even if retry bounds are added (recommended in SES-014), the spec does not define what happens after retry exhaustion in `draining` state. The pod cannot be returned to the warm pool (the workspace is not cleanly exported). The pod cannot transition to `failed` in the pod state machine (that state is reserved for pre-attached failures). The session cannot be marked `completed` until the workspace is sealed. This creates a potential indefinite resource hold at the pod level during a sustained MinIO outage.

**Recommendation:** Add: after retry exhaustion during `draining`, the pod transitions to a `seal_failed` terminal pod state (separate from `failed` which is pre-attached). The session record is updated with `seal_failed: true` and a `workspace_export_failed` artifact flag. The session state transitions to `completed` with a warning (the session completed but workspace export failed). Clients can discover this via `GET /v1/sessions/{id}/artifacts` returning an error artifact. This prevents indefinite pod hold while preserving the observable failure.

---

## Info

### SLC-017 Section Reference `(Section 7.3)` for `heartbeat_timeout` Is Incorrect [Info]
**Section:** 10.1

Section 10.1 references "the pod's `heartbeat_timeout` (Section 7.3)" but Section 7.3 is "Retry and Resume" and contains no definition of `heartbeat_timeout`. This appears to be a stale reference that was not updated when the document was reorganized. The mechanism is also not defined in Section 4.7 (Runtime Adapter), 15.4 (Runtime Adapter Specification), or elsewhere in the document.

**Recommendation:** Either (a) define the gateway-to-pod heartbeat timeout and pod hold state in Section 4.7 and update the reference in Section 10.1 to point there, or (b) if `heartbeat_timeout` is intended to be the adapter-to-binary `heartbeat` in Section 15.4.1 (10-second ack timeout), clarify that the pod enters a hold state when the adapter-level heartbeat to the gateway fails (which is a different mechanism from the agent-level heartbeat). The reference should be precise.
