# Review log: Sidecar runtime processes have no platform supervisor

## Standing context

### Settled

- **Re-baseline of 2026-10-22.** The draft of 2026-10-01 was written while proposal 0079 was a draft and before the owner decisions of 2026-10-01 and 2026-10-02 (plan §10.3), the security review of 2026-10-02, and proposals 0090 to 0094. It was re-baselined against the tree at `f52fc9a49` and the plan's Phase 2 entry for 0087 part 1. What changed, and why:
  - Part 2 (cross-tenant reuse, the tenant-pin reset, `allowCrossTenantReuse`) moved to proposal 0096, as the plan's entry requires.
  - Scope widened from opt-in supervision selected by an optional `command` to every sidecar pod, with `command` required, including on the admin registration payload (plan §10.3, supervisor scope).
  - The lifetime selector moved to the pool field `recycle.runtimeProcess: keep | restart`, required with no default, with `restart` refused through a mirrored `restartable` flag and exempt from `acknowledgeProcessLevelIsolation` on sequential pools (plan §10.3, 2026-10-01 and 2026-10-02). The draft's D-SELECT, D-ACK, and the non-goals that rejected a pool field were removed.
  - The supervisor became a static C binary in its own digest-pinned image with every-signal handlers and the `si_pid`/`si_code` filter (plan §10.3, gVisor spike; security review findings 1 and 2). The draft's Go `supervise` subcommand of the adapter (D-BINARY) was withdrawn.
  - The supervisor is a boundary for the reuse decision rather than "never an isolation control", and it ends the runtime before `cleanupCommands` rather than inside step 1 (security review finding 2; plan §10.3).
  - `preConnect` and service-mode runtimes are supervised rather than refused (D-SDKWARM, D-SERVICE; F-6.1.28).
  - `CH-SUPERVISE` gained a generation and request ID on every message, enumerated error codes, strict decoding, a liveness ping, mutual peer-UID authentication, the `sweep` request, and the unsolicited `exited` report (findings 3, 6, 8, 10, and 12; F-4.7.27).
  - D-STALE was rewritten: since proposal 0090 the runtime writes first on `CH-RUNTIMEOPS`, so the draft's argument that the adapter's first write rejects a stale peer is false. The adapter now closes and rebinds its runtime listeners between an attested end and the next launch (finding 4 for the adapter's own listeners; finding 11).
  - The draft's SPEC-6 (agent UID only on the runtime container) is already true: §13.1 **Container identity.** (proposal 0088) reserves the agent and adapter UIDs. The same rule forbids the draft's stage init container at the adapter UID, so the stage container runs at a dedicated UID, and SPEC-6 now states the supervisor volume's mount rule.
  - Every staged spec edit now quotes current tree text. The draft quoted 0079's staging.
  - New homes for F-4.7.26, F-4.7.27, F-5.2.36, F-5.2.37 (cleanup half), F-5.2.39 (step 3), F-5.2.46, and F-6.1.28, and for 0091's request to state the crash outcome through the §7.3 resume flow.

### Traps

- The plan's Phase 2 entry was written on 2026-10-12. Several of its statements are stale (the Ledger block below lists them). Read the tree, not the entry, for current facts.
- `scrub.DefaultOps` steps 1 and 1b exec `sh` and `ipcrm`, which the distroless adapter image does not contain.
- An admin-registered runtime has no pod path today unless a Runtime CRD of the same name exists. `command` on the admin payload feeds the registry and admission; the pod builder reads the CRD.

### Open

None. Both items open at the re-baseline were decided on 2026-10-22 (the `[delegated.2026-10-22]` ledger block).

### Deferred

- Relaunch after a crash, and pre-launching a session-mode runtime before its session arrives (plan §10.2, **Not yet scheduled**).

## Ledger

### [f1.cleanup]

FACT: Open-decisions firing 1 cleanup found the summary already carrying the listed sections in order, with an empty open-decisions section, and moved no content.

### [rebaseline.2026-10-22]

DECISION: Part 2 moved to proposal 0096 (`proposals/0096_new_cross-tenant-reuse-on-in-place-pools-by-resetting-the-tenant-pin/`); 0087 keeps a one-line pointer (D-PART2).
DECISION: `recycle.runtimeProcess` is required on every pool 0079's keep rule covers, concurrent pools included, and rejected elsewhere. The owner's decision names new sequential recycling pools; extending it to concurrent pools keeps one rule and an explicit choice everywhere a pod is reused.
DECISION: The property self-test gates on the non-dumpable mark, sender identification, and `kill(-1)` reach. PID-namespace init signal immunity is reported and does not gate, because the owner offered `restart` on gVisor, where it fails, on the ground that its failure fails closed. Plan §10.3 says "the four properties"; this reading reconciles the two owner decisions.
DECISION: The property check is a per-pod startup self-test, fail-closed on `restart` pods, plus `lenny-ctl preflight supervisor` for a named RuntimeClass, following the §4.7.11 `SO_PEERCRED` self-test precedent rather than a stored per-RuntimeClass result.
DECISION: The supervisor is freestanding C with no libc, which satisfies both readings of finding 1 ("no libc or NSS loading" in the plan, "no libc or musl" in the review).
DECISION: Image `ENTRYPOINT`, `CMD`, and `ENV` are unused on sidecar pods; the runtime gets the platform's variables and a fixed `PATH` (D-ENV). No `Runtime.spec.env` field is added. Superseded by D-RUNTIMEENV (`[delegated.2026-10-22]`).
DECISION: `restart` stays refused on `preConnect` and service-mode runtimes, as the owner's admission-predicate decision states, while both are supervised under `keep`. The plan's flag name "can be supervised" no longer fits, because every sidecar runtime is supervised; the flag is named `restartable`.
DECISION: A runtime crash fails each bound session with `runtime_crash` into proposal 0091's funnel and retires the pod at its next boundary on every pool (D-CRASH), which answers 0091's impacts row.
DECISION: F-5.2.46 is closed by marking the runtime not live on a heartbeat escalation on a `keep` pod (D-HUNG). The supervisor ping detects a dead or frozen supervisor, not a hung runtime, so it cannot close F-5.2.46 as BUILD-GAPS and 0091 state.
DECISION: 0087 does not render `--runtime-ops-socket`. R17 renders it and inherits the per-generation rebind (D-STALE).
DECISION: At pod termination the supervisor forwards the kubelet's SIGTERM at once (D-PODEXIT). The adapter shutdown and eviction proposal may replace the forward with an adapter request.
DECISION: The lifetime reaches the adapter on the boundary's `Shutdown` recycle disposition, so a pool edit applies at each pod's next boundary. Flagged for the S-2 proto window. Superseded by D-PODLIFETIME (`[delegated.2026-10-22]`).
FACT: Proposals 0090, 0091, 0092, 0093, and 0094 and the credential fail-open fix (F-4.9.36, merge `d05c25630`) have landed. P1 is 0093 and P5 is 0094, both implemented, so 0087 part 1's prerequisites in plan §10.2 are met except its own approval.
FACT: F-4.7.25 is closed by proposal 0090; `CH-MSGSOCK` and `CH-RUNTIMEOPS` authenticate a runtime-first nonce line against the `mcpNonce` published at accept time, which rotates at each session start.
FACT: §13.1 **Container identity.** reserves the agent UID for the regular container `runtime` and the adapter UID for `adapter` on every container kind, and the test-only egress-capture container runs at UID 65531.
FACT: The preStop hook execs `/usr/local/bin/lenny prestop` (F-4.6.37, closed); F-4.6.38 stays open and belongs to R16.
FACT: Stale plan statement: §10.2 Phase 0 still says the `SO_PEERCRED` half of F-4.7.25 and the hardening branch items are on branch `worktree-agent-a14863c1e63bb8ccf`, and that F-4.7.25 "stays open for the manifest nonce"; F-4.7.25 is closed.
FACT: Stale plan statement: §10.2 Phase 2's credential fail-open entry says the fix is "not yet merged"; it is merged (`d05c25630`).
FACT: Stale plan statement: §10.2 Phase 2's 0087 entry omits F-5.2.46, which BUILD-GAPS and 0091 assign to 0087 part 1, and it attributes that finding's fix to the supervisor liveness ping, which cannot detect a hung runtime.
FACT: Stale plan statement: §10.3's admission-predicate decision names the mirrored flag "can be supervised" and makes it false for `preConnect` and service-mode runtimes, while the supervisor-scope decision of the same day supervises both. The flag decides `restart`, not supervision.
FACT: Stale plan statement: §10.3 says "the four properties" gate `restart` on microVM pools and in the conformance check, while the gVisor decision offers `restart` where the signal-immunity property fails.
FACT: Stale plan statement: §10.3 states "admin-registered runtimes are all sidecars"; the tree has no pod path for an admin-only runtime, and the specification does not state the deployment model of one.
FACT: Stale plan statement: §10.6 says 0087 "supervises only restart pools" and that under `restart` "the single-session SDKs stay correct with no change"; supervision is universal, and since 0090 the SDKs are multi-session.
FACT: Stale plan statement: R17's re-derivation calls D-STALE "0087's stale-binary decision"; D-STALE concerns stale connections, and the draft's version of it was false after 0090.
FACT: Stale plan statement: §10.2's Phase 2 entry says the hardening "states the pod-wide agent-UID rule that 0087's mutual authentication on `CH-SUPERVISE` relies on" and lists "a pod-level runAsUser default that is neither the agent nor the adapter UID"; the tree sets no pod-level identity (`base_pod.go`), and §13.1 states that a pod-level value does not satisfy the rule.

### [delegated.2026-10-22]

DECISION: `cleanupCommands` run at the agent UID in the runtime container through the supervisor's forked worker on every sidecar pod, under both lifetimes, with a second `end` after them on `restart` pools (D-CLEANUP). Decided under the owner's delegation, closing open decision 1 with option (b). It closes the F-5.2.36 confused deputy on `keep` pools and the gap that the commands ran in the shell-less distroless adapter image. `CH-SUPERVISE` gains `cleanup` and `cleaned`.
DECISION: `Runtime.spec.env`, an optional list of `name` and `value` pairs with the `LENNY_` prefix reserved and refused at admission, mirrored through the registry and the admin payload as `command` is, and set by the supervisor after `clearenv()` (D-RUNTIMEENV). Decided under the owner's delegation, replacing "no `Runtime.spec.env` field".
DECISION: The pod's lifetime is fixed at pod creation and reaches the adapter by the `--runtime-process` flag, with a pod label the acquisition reads (D-PODLIFETIME). No proto field is added. The disposition field it replaces needed a proto field, and it left two holes the label closes: a pod `reserved` or `idle` after a `keep` boundary served one more session in its kept process after an edit to `restart` that removed the acknowledgment, and a pod created without `--require-properties` ran `restart` without the property gate. Decided under the owner's delegation, which preferred the no-proto alternative where it is as simple.
FACT: Plan §10.3's admission-predicate decision says `command` stays CRD-only, and the later supervisor-scope decision adds `command` to the admin registration API. The proposal follows the supervisor-scope decision (D-COMMAND), and `env` follows `command`.
FACT: The step-0 cleanup metadata is not wired in the tree (`podscrub.go` `scrubConfig` sets neither `PrevProvider` nor `PrevLeaseID`). Recorded under the summary's defects not staged.
FACT: Re-review of 2026-10-22 corrected these staged anchors against the tree at `f52fc9a49`: the hard-wrapped find strings of SPEC-2(a), SPEC-5(b), and SPEC-7(a); the non-unique `maxSessionsPerPod: 50` anchor of SPEC-5(a); the `#### Normative Merge Algorithm` heading and the first `#### Derived Runtime` heading of SPEC-1; the CH-MSGSOCK **Timing.** bullet of SPEC-8(a), whose wording differs from **Inbound: `shutdown`**; the third `RUNTIME_CRASH` site of SPEC-8(c); and the Generation bullet's citation, whose rule lives in §28.6 rather than §15.4.3. It added SPEC-2(d) for the §4.7.1 `Shutdown` row, the `keep` scoping of the occupancy-zero sentence of **Runtime process lifetime.**, and the SPEC-5(g) edits to the acknowledgment paragraph's first sentence and its edit rule, and it completed the list of `KeepsRuntime` readers.
