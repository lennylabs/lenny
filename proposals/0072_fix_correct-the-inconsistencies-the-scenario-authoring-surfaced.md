# Proposal: Correct the inconsistencies the scenario authoring surfaced

- **Status:** Draft for review.
- **Date:** 2026-08-13
- **Scope:** Twelve pre-existing inconsistencies were flagged while authoring `spec/29` and set aside
  rather than corrected, because they lay outside that section. Each has now been revalidated against the
  tree. Seven hold as recorded, two hold in a narrower or different form than recorded, two were wrong as
  recorded and concealed a different defect, and one has since been fixed by other work. The revalidation
  surfaced five further defects, one of them in a register this branch landed. This proposal corrects what
  can be corrected and states what cannot.

This document stages the proposed specification, code, and register changes. It does not modify any spec,
code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 0. Context an implementor should read first

The flags were raised as asides during other work and never verified at the time. Revalidating them found
that five of twelve were wrong in some respect, so the record below is the verified statement rather than
the original. Where the original flag and the verified defect differ, §1 says so, because the difference is
usually the more interesting half.

## 1. Problem

### 1.1 A checkpoint timeout with no defined start

`spec/04_system-components.md:251` states the 60-second checkpoint timeout is "measured from the initial
quiescence request to completion" and applies to "all checkpoint paths". It then elaborates two: the
Full-level `CH-RUNTIMEOPS` path and the embedded adapter path. `spec/04_system-components.md:248` puts
Basic and Standard runtimes on a "best-effort snapshot without pausing", which issues no quiescence
request, so those paths have no defined instant at which the deadline starts.

The claim is restated at `spec/29_communication-scenarios.md:1396`, and the code reproduces the ambiguity
rather than resolving it: `pkg/checkpoint/checkpoint.go:197-199` declares
`CheckpointTimeout = 60 * time.Second` and comments that it "Applies to every checkpoint path". Two
implementations of the best-effort path can therefore disagree on whether a slow snapshot has timed out.

### 1.2 Two sections disagree on whether a Basic runtime checkpoints at all

`spec/15_external-api-surface.md:1799`, the Level Comparison Matrix, gives the Basic column "No checkpoint
support; pod failure loses in-flight context", and `:1813` lists checkpoint and restore among the
capabilities "unavailable at Basic level" with "no fallback". `spec/04_system-components.md:248` groups
Basic with Standard and gives both a best-effort snapshot tagged `consistency: best-effort`. Two further
§4.4 sites depend on Basic producing one (`:261`, `:273`).

The code sides with §4.4: `ConsistencyForLevel` (`pkg/checkpoint/checkpoint.go:64-73`) returns
`ConsistencyBestEffort` for `LevelBasic` rather than rejecting the level. §15.4.3 is the side the shipped
code contradicts, and a runtime author reading it will neither implement nor expect snapshotting that the
gateway performs.

### 1.3 A restore that lands where nothing reads it

`spec/10_gateway-internals.md:171` states the reassembly pipeline writes into `/workspace/current.partial`
and renames atomically onto `/workspace/current`. The same paragraph selects the manifest by
`(session_id, slot_id)`, where `:157` defines `slot_id` as carried "when the pool sets
`sessionPolicy.maxConcurrentSessions > 1`". So §10.1.7 restores a slot-scoped manifest into a pod-global
directory, against `spec/06_warm-pod-model.md:392`: "The runtime MUST NOT assume a global
`/workspace/current` path when `maxConcurrentSessions > 1`."

The defect has propagated: `spec/29_communication-scenarios.md:1094-1095` mirrors the unqualified wording,
while the capture leg of the same document qualifies correctly at `:834-835`. Other sections qualify the
path properly, so this is specific rather than a repo-wide omission
(`spec/15_external-api-surface.md:2267`).

It is reachable rather than latent, and it fails silently. Capture is slot-scoped:
`pkg/adapter/checkpoint.go:100` resolves `checkpointRootsForSlot`, which swaps in
`/workspace/slots/{slotId}/current` (`pkg/adapter/slot.go:147`). Restore is not:
`pkg/adapter/resume.go:179` calls `ExtractTree(s.checkpointRoots(), pr)`, and `checkpointRoots()`
(`pkg/adapter/checkpoint.go:28`) always returns `/workspace/current`. A slot's checkpoint therefore
extracts into a directory no slot's runtime reads, no error is returned, and the resumed agent sees an
empty workspace while `resumeMode` reports a normal full restore. On a `maxConcurrentSessions > 1` pool, a
pod loss loses every slot's workspace.

Two further deltas against the same paragraph: the `/workspace/current.partial` staging directory and the
atomic rename do not exist. `ExtractTree` (`pkg/adapter/workspace/tree.go:145-170`) writes members
straight into the named roots, so a mid-stream failure leaves partially-extracted files visible, which
step (4) forbids in terms.

### 1.4 A headroom argument measured against the wrong pod

`spec/04_system-components.md:274` justifies the agent pod's 30-second eviction retry budget by citing
`terminationGracePeriodSeconds` at "240s at Tier 1/2, 300s at Tier 3 — see §17.8" as "ample headroom".
`spec/17_deployment-topology.md:971` labels that row `terminationGracePeriodSeconds (gateway pod)`. The
agent pod's default is 120s: `spec/04_system-components.md:499`, `spec/10_gateway-internals.md:130-138`,
and `pkg/controller/sandbox/podspec/podspec.go:75`.

The correction is not cosmetic. `spec/10_gateway-internals.md:130-138` computes the agent-pod grace floor
as `1 × 90 + 30 = 120s`, which equals the agent default, so the budget is already fully consumed and there
is no headroom for the 30-second retry budget §4.4 claims is amply covered. The same misattribution
appears a second time at `spec/04_system-components.md:288`, where a 60-second Postgres fallback budget is
said to fit "comfortably" inside the same gateway-pod numbers.

### 1.5 A closed enumeration that is not closed

`spec/07_session-lifecycle.md:431` states sessions enter `awaiting_client_action` "in two ways": auto-retry
exhaustion and a `resume_pending` timeout. §7.2's transition list carries four causes across two source
states (`:192`, `:196`), adding a `resuming` watchdog fire and a non-retryable error.
`spec/06_warm-pod-model.md:230-232` corroborates §7.2, and the code implements it:
`pkg/gateway/sessionserver/failure.go:191-220` routes both retryable-exhausted and non-retryable from
`resuming`, and `pkg/session/state/state.go:102,106` carries both legal edges.

A client built from §7.3's enumeration treats the state as "retries exhausted or pool starved" and will
retry where retrying is futile.

### 1.6 A documented upload path that rejects the documented use

`spec/15_external-api-surface.md:628` lists `POST /v1/sessions/{id}/upload` for "pre-start or mid-session
if enabled", and `:645` assigns it the `running` state when the runtime declares
`capabilities.midSessionUpload`. The route exists (`pkg/gateway/sessionserver/sessionserver.go:2060`), but
`handleUpload` calls `session.Validate` without the capability bit
(`pkg/gateway/sessionserver/upload.go:309-312`), so a `running` session is rejected on the precondition.
Mid-session upload works only through `handleUploadToSession`
(`pkg/gateway/sessionserver/upload_to_session.go:92-93`), whose route is published in the served OpenAPI
document at `pkg/gateway/externalapi/openapi/openapi.json:857` and appears nowhere in `spec/` or `docs/`.

A client following the specification calls a path that rejects it; the path that works is undocumented.

### 1.7 A metric label absent from the inventory

`pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_sessionlifecycle.go:285-288` emits
`lenny_checkpoint_storage_failure_total` with four labels including `reason`, and
`spec/04_system-components.md:273` documents it. The inventory at `spec/16_observability.md:201` and the
published reference at `docs/reference/metrics.md:191` both list three. An operator building from the
documented surface cannot distinguish `reason="kms_unavailable"`, which no retry clears, from ordinary
retry exhaustion.

### 1.8 A declared encoding that does not match the bytes

The gateway declares `chunk_encoding: tar` by default (`chunkEncoding()`,
`pkg/gateway/checkpoint/checkpointer/uploaddriver.go:149-156`; the production literal at
`cmd/lenny-gateway/stores.go:2156-2202` sets no override), and `ArchiveTree`
(`pkg/adapter/workspace/tree.go:45-58`) gzips unconditionally with no non-gzip branch.

In-band restore works, because `ExtractTree` gunzips unconditionally too, so the mismatch is invisible
until someone reads the declaration. `spec/04_system-components.md:290`'s manual-recovery procedure
directs an operator to decode from the logged `chunk_encoding` and forbids inferring from the object-key
suffix, so following the specification during a recovery runs `tar -x` against gzip bytes.

### 1.9 A jitter that is configured and never applied

`spec/04_system-components.md:267` states each session's first periodic checkpoint is scheduled at
`periodicCheckpointIntervalSeconds + random(0, interval × periodicCheckpointJitterFraction)`. The value
agrees everywhere — 0.2 in the spec, in `DefaultJitterFraction`
(`pkg/gateway/checkpoint/checkpointer/checkpointer.go:67`), in the flag default, and in
`charts/lenny/values.yaml`.

It is never used. `FirstCheckpointDelay` (`checkpointer.go:311`) has no caller outside its own test.
`Run` (`:287-296`) is a plain ticker and `Sweep` (`:339-345`) iterates every binding on the same tick, so
every session on a gateway checkpoints simultaneously — the thundering herd the jitter exists to prevent.
`JitterFraction` is assigned at `cmd/lenny-gateway/stores.go:2160` and read nowhere in production.

### 1.10 A naming lint that cannot see half its own rule

`spec/28_communication-channels.md:21-40` (N3) reserves two words from standing as a bare noun phrase
naming a conversation, and delegates the literal spellings to the lint's matcher. That matcher
(`scripts/specshift/name/phrase.go:35-37`) reserves `lifecycle|control` only when followed by
`channel` or `channels`.

`spec/15_external-api-surface.md:1722` and `docs/reference/adapter-contract.md:426` both write "control
stream". The lint passes over the live tree because "stream" is not an alternative head noun, so this is a
gap in the matcher rather than a registered exception.

Those two sentences carry a second defect independent of the naming rule: they describe an `AdapterInit`
message and an `AdapterInitAck` reply, and neither exists in `schemas/lenny-adapter.proto`. The version
handshake there is `NegotiateVersion` (`:210`) and the adapter-to-gateway stream is `AdapterEvents`
(`:227`). The prose predates the current contract, and because it names no identifier a reader cannot
search the channel register for what it means.

### 1.11 A register row that contradicts its neighbour

`tests/claim-map.json` carries `"Checkpoint restore onto a concurrent pod"` as `ABSENT` with surface
"no `slot_id` on `ResumeRequest`", and `"ResumeRequest.slot_id"` as `UNWIRED` with surface
`schemas/lenny-adapter.proto`. §28.4 defines `UNWIRED` as implemented with no production caller and
`ABSENT` as specified and not implemented. The field is on no message in that file
(`ResumeRequest` carries fields 1 through 13 and no `slot_id`), so the row's status is wrong and it
contradicts the row above it.

The row was seeded by this branch. Its status was inferred from the deferral step's scope note, which says
the proto fields land in an earlier step, rather than read off the tree. A validator that trusts `UNWIRED`
to mean the field exists reads the register wrong.

## 2. Decisions

1. **The specification is corrected to what the code does, except where the code is the defect.** §1.2,
   §1.4, §1.5, §1.6 and §1.7 are spec-side corrections. §1.8 and §1.9 are code-side. §1.3 is both, and §3
   splits it.

2. **§1.3's code half is deferred, and its spec half is not.** Slot-aware restore is already owned:
   `TEST-GAPS.md:692` carries T-4.4.21 OPEN, `PROPOSAL-QUEUE.md:570-576` files cluster C-53, and
   `tests/claim-map.json` carries an `ABSENT` row deferred to R22. Neither C-53 nor R22 covers the
   *specification text*, which states a pod-global extraction root today and will still state it when the
   code lands. The text is corrected here so the two halves cannot diverge further.

3. **§1.8 is fixed by making the declaration true rather than by changing the bytes.** The archive format
   is gzip on both legs and has always been; only the declared value is wrong. Changing `ArchiveTree` would
   break every existing checkpoint, while changing the default makes the manual-recovery procedure correct
   for objects already written.

4. **§1.9 is fixed by calling the helper that already exists.** `FirstCheckpointDelay` implements the
   specified formula and is tested; what is missing is per-session scheduling in `Run`.

5. **§1.10's matcher gains the missing head noun; the two prose sites are corrected separately.** The
   matcher gap is a lint defect. The prose defect is that the sentences describe messages the contract does
   not carry, and correcting them requires knowing what the handshake is now, which §3 stages against
   `NegotiateVersion` rather than guessing at a channel identifier.

6. **The register row is corrected to `ABSENT` and the seeding script's inference is removed.** A status
   read off a plan's scope note rather than off the tree is the defect, and the script that produced it
   would reproduce it.

7. **One flag is recorded as fixed and not restaged.** The retry-budget classes now cover the drain-driver
   checkpoint: proposal 0037 (`a474c89f`) collapsed both drain finalisations onto the `eviction` trigger,
   which the enum closes over (`spec/16_observability.md:196`) and `RetryBudgetFor` resolves
   (`pkg/checkpoint/checkpoint.go:157-172`). What remains is the rationale, in §3's SPEC-4.

## 3. Proposed changes

### SPEC-1. Give the checkpoint timeout a defined start on every path (§1.1)

`spec/04_system-components.md:251` states the start per path rather than universally: the quiescence
request on the Full-level `CH-RUNTIMEOPS` path, the `SIGSTOP` on the embedded adapter path, and the first
byte read on the Basic and Standard best-effort path, which pauses nothing. The restatement at
`spec/29_communication-scenarios.md:1396` is brought into line, and the comment at
`pkg/checkpoint/checkpoint.go:197-199` names the three starts rather than "every checkpoint path".

### SPEC-2. Reconcile §15.4.3 with §4.4 on Basic-level checkpoints (§1.2)

`spec/15_external-api-surface.md:1799` and `:1813` are corrected to §4.4's statement, which the code
implements: a Basic-level runtime produces a best-effort snapshot tagged `consistency: best-effort`, with
no runtime pause and no consistency guarantee. The Basic-level limitations list keeps checkpointing but
states the limitation accurately, which is the absence of a *consistent* checkpoint rather than of any
checkpoint. The Credential-rotation row's conditional "If checkpoint unsupported" is removed, being dead
under §4.4.

### SPEC-3. Qualify the restore extraction root (§1.3, spec half)

`spec/10_gateway-internals.md:171` states the extraction root as `/workspace/current` on a pod serving one
session and `/workspace/slots/{slotId}/current/` on a pod whose pool sets
`sessionPolicy.maxConcurrentSessions > 1`, with the staging directory and atomic rename stated per root.
`spec/29_communication-scenarios.md:1094-1095` takes the same qualification, matching the capture leg at
`:834-835`.

The paragraph also gains a sentence recording that the slot-aware restore path is not implemented, naming
the claim-register row that tracks it, so a reader of §10.1.7 is not left to infer that the qualified text
describes shipped behaviour.

### SPEC-4. Measure the eviction budget against the agent pod (§1.4)

`spec/04_system-components.md:274` cites the agent pod's `terminationGracePeriodSeconds` default of 120s
(§4.6.1) rather than the gateway pod's 240s and 300s, and states the headroom that default actually
leaves against the §10.1 grace floor of `slots × 90 + 30`. Where the floor consumes the default, the text
says so rather than claiming ample headroom. `spec/04_system-components.md:288` takes the same correction
for the Postgres fallback budget.

The rationale sentence is corrected in the same edit: the "pod is terminating regardless" premise does not
hold on the barrier-drain path, where the gateway replica drains and the agent pod continues
(`spec/10_gateway-internals.md:185`).

### SPEC-5. Close §7.3's enumeration (§1.5)

`spec/07_session-lifecycle.md:431` enumerates all four causes §7.2 states, or cites §7.2 as the owner
rather than restating a partial list. §7.2 remains the owner.

### SPEC-6. Document the working mid-session upload route (§1.6)

`spec/15_external-api-surface.md:628` and the precondition table at `:645` name
`POST /v1/sessions/{id}/upload-to-session` as the mid-session surface, with `/upload` restricted to the
pre-start use its handler implements. §7.4's `upload_to_session` MCP tool gains the REST route as its
sibling surface.

### SPEC-7. Add the `reason` label to the inventory (§1.7)

`spec/16_observability.md:201` lists `reason` alongside `pool`, `level`, and `trigger`, and `:308` adds it
to the documented domain labels with its values. `docs/reference/metrics.md:191` takes the same label.

### CODE-1. Declare the encoding the gateway writes (§1.8)

`chunkEncoding()` (`pkg/gateway/checkpoint/checkpointer/uploaddriver.go:149-156`) defaults to
`ChunkEncodingTarGz`. No archive code changes: `ArchiveTree` already gzips and `ExtractTree` already
gunzips. Objects already written are unaffected, since the read path selects its decoder from the manifest
and both now say gzip.

### CODE-2. Apply the jitter the specification states (§1.9)

`Checkpointer.Run` (`pkg/gateway/checkpoint/checkpointer/checkpointer.go:287-296`) schedules each
session's first checkpoint through `FirstCheckpointDelay`, which already implements the formula, rather
than sweeping every binding on one tick.

### CODE-3. Give the naming matcher its missing head noun (§1.10)

`reservedExpr` (`scripts/specshift/name/phrase.go:35-37`) admits `stream` and `streams` alongside
`channel` and `channels`. The two sites the widened matcher then reports are corrected by SPEC-8 in the
same change, so the lint does not land red.

### SPEC-8. Correct the two handshake sentences (§1.10)

`spec/15_external-api-surface.md:1722` and `docs/reference/adapter-contract.md:426` describe the handshake
the contract carries — the `NegotiateVersion` RPC (`schemas/lenny-adapter.proto:210`) — and name
`CH-ADAPTEREVENTS` by its identifier where they refer to the adapter-to-gateway stream
(`schemas/lenny-adapter.proto:227`). If review establishes that these sentences describe a handshake the
contract never carried, the correct edit is deletion rather than repair, and §5 records that as the open
question.

### REG-1. Correct the claim-register row and the script that seeded it (§1.11)

`tests/claim-map.json`'s `"ResumeRequest.slot_id"` row becomes `ABSENT`, matching its neighbour and the
tree. `scripts/seed-claim-register.py` stops inferring a status from a deferral step's scope note: a row
for a field is `ABSENT` unless the field is present in the tree at seeding time.

## 4. Testing

**Tier 0.** The naming lint over the live tree, green after CODE-3 and SPEC-8 together, with a fixture case
pinning that "control stream" is now reported. The claim-register validator, unchanged, over the corrected
row.

**Tier 1, `pkg/gateway/checkpoint/checkpointer`.** `Run` schedules a session's first checkpoint within
`[interval, interval × 1.2]` and two sessions registered in the same tick do not share a first-checkpoint
instant. `chunkEncoding()` returns `tar.gz` by default and the declared value round-trips into the
manifest row.

**Tier 3, `tests/tier3_contract`.** A checkpoint manifest's `chunk_encoding` matches the bytes the adapter
produced, asserted by decoding the chunk with the declared decoder rather than by string comparison. This
is the case that would have caught §1.8.

**Tier 4.** A mid-session upload against the route SPEC-6 documents succeeds on a `running` session whose
runtime declares the capability, and against `/upload` is rejected — pinning both halves of §1.6 so the
documentation and the surface cannot drift apart again.

**Tier 11.** The §16 inventory's label list matches the labels the metric is registered with, which is the
general form of §1.7 and would have caught it.

## 5. Open questions for review

- **§1.10's prose.** Whether `AdapterInit` and `AdapterInitAck` describe a handshake the contract once
  carried and lost, or were never implemented. The answer decides whether SPEC-8 repairs or deletes.
- **§1.4's arithmetic.** Correcting the citation shows the agent pod's 120s default is exactly consumed by
  the §10.1 floor at one slot, leaving no headroom for the 30-second retry budget. Whether the remedy is a
  larger default, a smaller budget, or an accepted overrun is a decision this proposal does not make.
- **§1.3's silence.** A slot-scoped restore currently extracts into an unread directory and returns no
  error. Whether the interim behaviour should fail loudly rather than silently, before C-53 lands the real
  path, is worth deciding now.

## 6. Out of scope

Slot-aware restore itself (C-53, R22, T-4.4.21). The missing `.partial` staging directory and atomic
rename on the extract path, which are a second §10.1.7-versus-code divergence and belong with the restore
work. Any change to `Merge`'s no-overwrite rule or the retry budgets' values.

## 7. Files touched on application

- `spec/04_system-components.md` — SPEC-1, SPEC-4.
- `spec/07_session-lifecycle.md` — SPEC-5.
- `spec/10_gateway-internals.md` — SPEC-3.
- `spec/15_external-api-surface.md` — SPEC-2, SPEC-6, SPEC-8.
- `spec/16_observability.md` and `docs/reference/metrics.md` — SPEC-7.
- `spec/29_communication-scenarios.md` — SPEC-1 and SPEC-3 restatements.
- `docs/reference/adapter-contract.md` — SPEC-8.
- `pkg/gateway/checkpoint/checkpointer/uploaddriver.go` — CODE-1.
- `pkg/gateway/checkpoint/checkpointer/checkpointer.go` — CODE-2.
- `pkg/checkpoint/checkpoint.go` — the SPEC-1 comment.
- `scripts/specshift/name/phrase.go` — CODE-3.
- `tests/claim-map.json` and `scripts/seed-claim-register.py` — REG-1.
- The tier-0, tier-1, tier-3, tier-4, and tier-11 cases in §4, and their `tests/spec-map.json` entries.
