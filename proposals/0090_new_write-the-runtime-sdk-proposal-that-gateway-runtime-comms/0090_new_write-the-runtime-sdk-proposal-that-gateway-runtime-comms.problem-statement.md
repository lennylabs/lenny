# Problem: Runtime SDKs serve one session per process while the runtime process is kept across sessions

## Statement

The Go, Python, and TypeScript runtime SDKs under `sdks/runtime/` serve one session's context per process. Each SDK reads the adapter manifest and the credential file once when its process starts and invokes the create handler (`OnCreate`, `on_create`, or `onCreate`) once. The Go SDK also fills `Message.SessionID` and `Message.TaskID` from that startup manifest rather than from the inbound envelope, while its response frames echo the envelope's `sessionId`. Frame addressing is therefore already per session, and the context the handler sees is per process.

The platform no longer gives a runtime process one session. On a sidecar pod the adapter keeps one `CH-MSGSOCK` connection for the pod's life. `Close` and `Interrupt` are no-ops, `CloseListener` is the only close, and the adapter sends the runtime no shutdown frame and no session boundary signal. Concurrent pools (`maxConcurrentSessions > 1`) have multiplexed every slot over that one connection since commit `f54fbf400` (2026-06-17). Proposal 0079 (implemented) extended the same reuse to sequential sessions on acknowledged recycling pools up to `maxSessionsPerPod`. The root cause is that the SDK contract was never multi-session; 0079 made the defect reachable for sequential reuse as well as concurrent reuse.

A kept runtime therefore serves every later session under the context it loaded at process start: the first session's `SessionID`, `TaskID`, credentials path, cached credential bundle, MCP nonce, and session variables. On sidecar pods today that context is usually absent even for the first session, because the runtime reads the manifest before the adapter writes it (BUILD-GAPS F-4.7.26, owned by draft 0087's supervisor). Two further symptoms follow from the same cause. The adapter regenerates the intra-pod MCP nonce and re-arms the MCP servers per session while the SDK dials MCP once, and the adapter's pod-global MCP surfaces fail closed for every session once the process has been given a second one.

This proposal owns the single runtime lifetime contract that the owner decided on 2026-10-02 (`gateway-runtime-comms-remediation.md` section 10.3). A runtime process may serve any number of sessions, one after another and, on concurrent pools, at once. Every session-scoped frame carries `sessionId`. Per-session context arrives in a session-start frame on `CH-MSGSOCK`, and a session-end frame releases it. No runtime relies on its process exiting at session end. The `restart` lifetime of draft 0087 adds a guarantee and imposes no different requirement.

The contract applies to `type: agent` runtimes in the sidecar model. A `type: mcp` runtime is driven by `MCPRuntime` as an MCP client and never speaks the `CH-MSGSOCK` JSONL protocol, so the proposal states how the contract covers it or excludes it explicitly. The embedded model already runs one runtime loop per session in the adapter process (`InProcessRuntime` is bound to one session), so the proposal states that the contract holds there rather than reworking it.

Scope:

1. Spec. State the lifetime contract in §4.7 (adapter-runtime contract), §15.4 (runtime adapter specification and its message and lifecycle sections), §15.7 (runtime SDK, including the `CreateRequest` `SessionID` and `TaskID` comments that 0079 handed to this proposal), §7.2, the §28.5.3 `CH-MSGSOCK` card (its Preconditions "spawns the runtime binary" and its Timing shutdown-and-exit wording), and the §29 scenarios that describe per-process sessions. Define the session-start and session-end frames (fields, ordering, and error and timeout handling) and add them to `schemas/lenny-adapter-jsonl.schema.json`. Reconcile the §15.4.6 Full-level conformance category **deadline signal handling**, whose runtime exits on a deadline signal, with the no-exit rule; 0079 assigned that category to this contract. Reconcile the `llm.apiKeyEnv` manifest rule, under which the runtime sets the lease token as an environment variable in its own process, with sessions of different users on one process. Remove every remaining per-process-session statement.
2. Session-start payload. Task ID (equal to the session id under §15.7), the credentials path (`/run/lenny/slots/{sessionId}/credentials.json`), and the workspace path (`/workspace/slots/{sessionId}/current/`, which §6.4 requires the runtime to derive) are already functions of `sessionId`. The frame carries the session variables that today live only in the pod-global manifest (`runtimeOptions`, `workspacePlan`, experiment and tracing context, `llm`, and the MCP nonce or connection data the session needs), and is kept as small as that allows.
3. Adapter. Emit session-start before a session's first message and session-end after its last, on every path that starts or ends a session (start, resume, slot release, terminate, interrupt, hold timeout, and pod exit).
4. SDKs. Serve sessions keyed by `sessionId`, with per-session context from the frames, a per-session create, message, and terminate handler (or the language-appropriate equivalent), concurrent sessions where the pool allows, and no reliance on process exit. Include the Go `Handler` doc-comment correction and the `types.go` comments moved out of 0079, and their Python and TypeScript counterparts. Absorb proposal 0080 §1.9: the Python and TypeScript SDKs model no `status` frame and so carry no `sessionId` on it. `cmd/runtimes/echo-concurrent` is the existing pattern for per-session state keyed by `sessionId`.
5. Runtime authentication, runtime side. Implement the `CH-MSGSOCK` manifest-nonce handshake the card requires (BUILD-GAPS F-4.7.25, nonce half; the `SO_PEERCRED` half is done) and the §4.7.11 nonce-only HMAC challenge on `CH-MSGSOCK`, reusing the challenge already in `pkg/adapter/mcp/challenge.go`. Implement the `CH-RUNTIMEOPS` manifest-nonce handshake (BUILD-GAPS F-4.7.30 resolution). Implement the SDK client half of security review finding 4: the SDKs answer a server-first challenge and use the current generation's nonce on `CH-MSGSOCK`, the intra-pod MCP servers, and `CH-RUNTIMEOPS`. The intra-pod MCP listener half of finding 4 (close and rebind after END and before LAUNCH, per-generation rotation, and challenge-first on the server) belongs to proposal 0084. The per-generation nonce supply and the `CH-MSGSOCK` rebind before LAUNCH belong to proposal 0087.
6. Tests. A tier-10 conformance case that serves two sequential sessions with different contexts on one runtime process, and two concurrent sessions on a concurrent pool, extending `tests/tier10_conformance/concurrent_slot_conformance_test.go`, plus tier-1, tier-3, and tier-4 tests across the SDKs and the adapter.

Dependencies and sequencing the proposal states:

- The `CH-MSGSOCK` nonce handshake needs the nonce in the manifest before the runtime dials, and a later manifest write (the INIT placeholder's replacement, or a concurrent start's write) can replace the nonce the runtime read before the adapter accepts. "Runtime generation" is defined by 0087's `restart` lifetime; in today's tree the code's "runtime generation" counts the sessions given to the process. The proposal defines the term once and does not reuse it ambiguously.
- The current drafts of 0084 and 0087 reject the mechanisms item 5 assumes. Draft 0084 makes `mcpNonce` pod-scoped and rejects a `CH-MSGSOCK` start frame carrying the nonce or connector set. Draft 0087 rejects a per-process launch nonce on `CH-MSGSOCK` and relies on its D-STALE property instead. The section 10.3 owner decision says the supervisor supplies the per-generation nonce. The ownership and mechanism of finding 4 need an owner decision, or a re-baseline of 0084 and 0087, before any proposal stages it. Until then this proposal stages only the SDK client half that holds under either mechanism.
- The concurrent Standard-level conformance case depends on 0084's per-session MCP attribution (`_lennySessionId`). Without it, the adapter refuses `tools/list` and `tools/call` once more than one session is live, so the case is either sequenced after 0084 or scoped to Basic level.
- 0087 and this proposal both edit §4.7, §15.4, and §15.7, so their spec steps are serialized.

Constraints: the platform is pre-deployment, so the proposal adds no backward-compatibility shim or dual mode. There is no runtime capability declaration for sequential sessions (owner decision). The first-session manifest-ordering defect (F-4.7.26) is owned by 0087's supervisor; this proposal records that its frames remove the per-session dependence on the manifest file. R17 has not enabled `CH-RUNTIMEOPS` in deployed pods. Keep the change as small as the contract allows.

## Evidence

- (verified) Plan: `gateway-runtime-comms-remediation.md` section 10.2 Phase 2, the unchecked "Runtime-SDK proposal (not yet written)" item, which states that the tree is not releasable until the proposal lands. Section 10.3 holds the 2026-10-02 "One runtime lifetime contract" and "No runtime capability declaration" decisions. The decision to place the end-of-session signal on `CH-MSGSOCK` is in the 2026-10-01 list. The owner decision names only the sequential tier-10 case; the concurrent case is an extension the contract wording supports.
- (verified) Plan Phase 3: proposal 0084 closes security review finding 4 for the intra-pod MCP listeners (close and rebind after END, per-generation rotation, and challenge-first).
- (verified) Proposal statuses: 0078 and 0079 are Implemented; 0084 and 0087 are Draft. 0079's spec changes (decision 7 and the deferred-work paragraph) hand this proposal the SDK change, the doc-comment corrections, the §15.7 `CreateRequest` comments, and the §15.4.6 deadline-signal-handling category.
- (verified) Draft conflicts: `proposals/0084_*/0084_*.summary.md` rejects "a new `CH-MSGSOCK` start frame or envelope fields carrying the nonce or connector set"; `proposals/0087_*/0087_*.summary.md` rejects "a per-process launch nonce on `CH-MSGSOCK`, or rotating the arming nonce" and defines D-STALE.
- (verified) Security review finding 4 (`scratchpad/supervisor-security-review/review.md`): stale buffered connections on long-lived adapter listeners cross generations. The mitigation names per-generation nonce rotation, server-sends-challenge-first, and close-and-rebind after END and before LAUNCH.
- (verified) BUILD-GAPS F-4.7.25 is open with the `SO_PEERCRED` half resolved (`5794e6923`) and the nonce half open. F-4.7.26 is open and assigned to 0087's supervisor. F-4.7.30's resolution assigns the `CH-RUNTIMEOPS` manifest-nonce handshake to this proposal.
- (verified) Spec: §4.7.10 "Deployment Model" with the bold label **Runtime process lifetime** (one connection for the pod's life, multiplexed by `sessionId`; embedded model runs one loop per session); §4.7.9; §4.7.11 item 1 and the nonce-only fallback; §6.4 (runtime MUST derive `cwd` from `sessionId`); §4.7 manifest `credentialsPath` per session and `llm.apiKeyEnv`; §7.2; §15.4.6 (deadline signal handling exits the runtime); §15.7 (`CreateRequest` "the session this runtime instance is bound to", `OnCreate` invoked once); §28.5.3 `CH-MSGSOCK` card (nonce first message, HMAC challenge, "spawns the runtime binary", shutdown-and-exit timing); §29.
- (verified) Code, SDKs: `sdks/runtime/go/runtime/runtime.go` (`Handler` doc comment says the next session gets a fresh `OnCreate` after the runtime exits; `loadManifest`, `loadCredentials`, and `invokeCreate` run once per `Run`; `handleMessage` sets `SessionID` and `TaskID` from the manifest and echoes `env.SessionID` on frames); `types.go` ("TaskID is frozen for the session's lifetime", `OnCreate` once); `sdks/runtime/go/runtime/mcp.go` (one MCP dial with the startup nonce, no redial); the Python and TypeScript SDKs (create handler once per process). No SDK answers a challenge.
- (verified) Code, adapter: `pkg/adapter/socketruntime.go` (one connection for every session, `Close` and `Interrupt` return nil, manifest-nonce handshake not performed); `pkg/adapter/peercred.go` (`SocketPeerAuth`); `pkg/adapter/runtimegeneration.go` (pod-global surfaces fail closed after a second session); `pkg/adapter/manifest.go` (manifest written per session start with a new MCP nonce); `pkg/adapter/slotsession.go` and `session.go` (MCP servers re-armed per sole-occupant claim); `pkg/adapter/mcp/challenge.go` (existing HMAC challenge); `pkg/adapter/mcpruntime.go` (`type: mcp` runtimes use MCP over stdio); `pkg/adapter/embedded.go` (`InProcessRuntime` bound to one session). `cmd/lenny-adapter/main.go` defines `--runtime-ops-socket` and no controller code renders it.
- (verified) Schema and tests: `schemas/lenny-adapter-jsonl.schema.json` is the `CH-MSGSOCK` frame schema and defines no session-start or session-end frame. `tests/tier10_conformance/concurrent_slot_conformance_test.go` (`TestConcurrentSessionDispatchConformance`) interleaves two concurrent sessions on the `echo-concurrent` reference runtime and has no sequential case and no context-difference case.
- (verified) Admission: `pkg/gateway/runtime/poolstore/poolstore.go` admits concurrent and recycling pools once `acknowledgeProcessLevelIsolation` is true, without regard to the runtime's SDK.

## Who observes it

A deployer observes it by admitting a sidecar pool with `maxConcurrentSessions > 1`, or with recycling enabled, `maxSessionsPerPod > 1`, a scrub profile other than `vm-restart`, and `acknowledgeProcessLevelIsolation: true`. The admin API exposes these fields, and pool admission does not check the runtime's SDK. Any runtime built on the Go, Python, or TypeScript SDK then serves its second and later sessions with the wrong context.

The affected authors are the SDK's primary audience. The sidecar model is the default and the only model offered to third parties, and the SDKs exist for third-party authors. The embedded model is unaffected. No in-repo production runtime imports the Go SDK; its consumers are the SDK examples and the tier-10 tests, so the current repository shows no observable failure, and the failure falls on external runtime authors and on any later first-party sidecar runtime. The platform is pre-deployment, so no live user is affected today.

## What breaks if nothing changes

- A later session on a kept runtime runs under the first session's `SessionID`, `TaskID`, credentials path, and cached credential bundle. Credential leases can be user-scoped (BYOK), and a kept process is pinned to a tenant rather than to a user, so user B's session can use user A's leased credential, or a stale or revoked one after A's lease ends. The SDK reloads credentials only on a `CH-RUNTIMEOPS` `credentials_rotated` event, which no deployed pod receives while R17 is unstarted.
- Platform and connector MCP tools stop working for later sessions. The adapter re-arms the intra-pod MCP servers with a new nonce, the SDK holds the startup nonce and does not redial, and the adapter's pod-global surfaces fail closed once a second session is given to the process.
- The recycling feature that 0079 made legal is unsafe to enable with any SDK-built runtime, and concurrent pools have the same defect. The plan records the tree as not releasable until this proposal lands.
- `CH-MSGSOCK` stays unauthenticated beyond `SO_PEERCRED`, and in nonce-only mode it has no authentication at all.

## Findings this unblocks

- BUILD-GAPS F-4.7.25 (nonce half) and the `CH-RUNTIMEOPS` handshake named in F-4.7.30's resolution.
- The per-session half of F-4.7.26; the first-session ordering half stays with 0087.
- The runtime side of security review finding 4.
- Proposal 0080 §1.9 (no `status` frame in the Python and TypeScript SDKs).

## Prior art considered

- No proposal under `proposals/` stages session-start or session-end frames, multi-session SDKs, or the lifetime contract. 0079 defers this work to this proposal, and 0087's problem statement records that no proposal covers it. The `CH-MSGSOCK` schema's frame types are `heartbeat`, `heartbeat_ack`, `message`, `response`, `set_tracing_context`, `shutdown`, `status`, `tool_call`, and `tool_result`.
- The spec already derives task ID, credentials path, and workspace path from `sessionId`, so only the session variables need a new carrier.
- `cmd/runtimes/echo-concurrent` implements per-session state keyed by `sessionId`, and the tier-10 concurrent dispatch test exercises it. Both are the starting point for the SDK rewrite and the conformance case.
- `pkg/adapter/mcp/challenge.go` implements the nonce-only HMAC challenge for the intra-pod MCP servers (16-byte challenge, 500 ms timeout) and is reusable for `CH-MSGSOCK` and `CH-RUNTIMEOPS`.
- Draft 0084 (session attribution on concurrent pools) assumes single-session SDKs bound by §15.7, makes `mcpNonce` pod-scoped, and rejects a `CH-MSGSOCK` start frame. Draft 0087 (supervisor and `restart` lifetime) rejects a per-process `CH-MSGSOCK` nonce in favour of D-STALE. Both conflict with this proposal and need re-baselining against it.
- Draft 0080 §1.9 records the missing `status` frame in the Python and TypeScript SDKs with no other owner.
- Making `restart` the universal lifetime does not remove the need for this work: it leaves concurrent pools broken, and the owner-decided admission predicate refuses `restart` for service-mode, `preConnect`, and command-less runtimes.

## Validated premises

Premise lens (verdict: revise).

- Confirmed: 0079 is implemented; the sidecar transport keeps one `CH-MSGSOCK` connection for the pod's life; the adapter sends no shutdown or session boundary frame.
- Confirmed: all three SDKs read the manifest once and call the create handler once per process; the Go `Handler` doc comment still describes a runtime exit between recycled sessions; Go `Message.SessionID` and `TaskID` come from the startup manifest while frames echo `env.SessionID`.
- Refuted: the original consequence "a kept runtime serves a later session under the first session's context". On sidecar pods the runtime reads the manifest before the adapter writes it (F-4.7.26), so the frozen context is the one loaded at process start, which is usually empty. The statement now describes context frozen at process start.
- Refuted: "`CH-MSGSOCK`, which every runtime has". `type: mcp` runtimes do not speak `CH-MSGSOCK`, and the embedded model runs one loop per session. The contract is now scoped to `type: agent` sidecar runtimes, with the other cases stated.
- Refuted: the original item 4 ownership. The plan gives the intra-pod MCP listener half of finding 4 to 0084 and only the runtime side to this proposal; the original also omitted the close-and-rebind half, which falls to 0084 and 0087.
- Confirmed: the `CH-MSGSOCK` nonce handshake depends on the manifest existing before the runtime dials, and "runtime generation" is ambiguous in today's tree.
- Confirmed: the original scope omitted the §15.4.6 deadline-signal-handling category that 0079 assigned to this contract.
- Confirmed: R17 is not started; `--runtime-ops-socket` is not rendered; the existing tier-10 concurrent test uses the reference runtime and has no sequential-context case.

Evidence lens (verdict: stands).

- Verified the plan item, the section 10.3 decisions (with the `CH-MSGSOCK` decision dated 2026-10-01), the proposal statuses, BUILD-GAPS F-4.7.25 and F-4.7.26, finding 4, the Phase 0 hardening, R17's status, and every listed adapter file.
- Drift: "§4.7.10 Runtime process lifetime" is a bold label inside §4.7.10 "Deployment Model" rather than a heading. The citation now names the label.
- Drift: no schema file is named for `CH-MSGSOCK`; the schema is `schemas/lenny-adapter-jsonl.schema.json`. The scope now names it.
- Drift: `types.go` says `TaskID` is frozen for the session's lifetime rather than the process lifetime; the per-process behaviour lives in `runtime.go`. The statement now locates it there.
- Scope extension: the concurrent tier-10 case is supported by the contract's wording, while the owner decision names only the sequential case.

Prior-art lens (verdict: revise).

- Confirmed: nothing already solves the problem.
- Revised: task ID, credentials path, and workspace path are already functions of `sessionId`, so the session-start frame carries only the session variables (scope item 2).
- Revised: the HMAC challenge exists in `pkg/adapter/mcp/challenge.go` and is reused; no SDK answers it yet.
- Revised (load-bearing): finding 4 is split by the plan among 0084, 0087, and this proposal, and the current drafts of 0084 and 0087 reject the mechanisms this proposal assumes. The statement now names the conflict and limits this proposal to the SDK client half until the owner decides.
- Added: 0080 §1.9 is absorbed. Embedded runtimes are bound to one session per `InProcessRuntime`, so for them the frames serve as a uniform SDK signal rather than a multiplexing need.

Scope lens (verdict: revise).

- Confirmed: the spec, adapter, SDK, and conformance items form one wire contract and stay together.
- Contested (load-bearing): the lens proposes cutting runtime authentication into a separate proposal, because nonce-only authentication on `CH-MSGSOCK` is a pre-existing gap that multi-session serving neither causes nor closes. The plan assigns the runtime side to this proposal and the SDK connect path is shared, so the statement keeps the runtime side (scope item 5) and marks it as separable. The split is listed for a human decision.
- Confirmed (load-bearing): finding 4's ownership is an unresolved cross-proposal decision; the statement stages only the SDK client half.
- Added: the concurrent Standard-level conformance case depends on 0084's `_lennySessionId`.
- Noted: if the proposal needs a size split, the seam is spec, adapter, Go SDK, and conformance first, then the Python and TypeScript ports.

Impact lens (verdict: stands).

- Confirmed: the defect is reachable through pool admission today, the SDK never exits or re-initializes on a kept connection, and later sessions use the first session's identity and credentials.
- Added: the credential exposure is cross-user within a tenant through user-scoped leases.
- Added: platform and connector MCP tools break for later sessions.
- Confirmed: the embedded model is unaffected, the sidecar model is the primary audience, and no in-repo production runtime uses the Go SDK.

Alternatives lens (verdict: stands).

- Confirmed: no reading needs zero change.
- Revised: the problem predates 0079 (commit `f54fbf400`, 2026-06-17, concurrent pools on one connection), so reverting or narrowing 0079 does not dissolve it. The statement now names the root cause as an SDK contract that was never multi-session.
- Confirmed: universal `restart` does not dissolve the problem.
- Revised: the embedded model needs only a statement that the contract holds.
- Revised: item 4 is restricted to the runtime side plus `CH-MSGSOCK` and `CH-RUNTIMEOPS`; per-generation rotation matters only under 0087's `restart`.
- Added: the per-session MCP nonce regeneration in the adapter conflicts with the SDK's once-per-process read and with per-generation rotation; the proposal reconciles the rule.
