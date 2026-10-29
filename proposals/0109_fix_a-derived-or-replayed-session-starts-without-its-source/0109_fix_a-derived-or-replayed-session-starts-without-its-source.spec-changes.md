# Spec changes — A derived or replayed session starts without its source workspace

## Design (as the spec must state it)

A session that `POST /v1/sessions/{id}/derive` or `POST /v1/sessions/{id}/replay` creates starts at its first bind from the snapshot the derive copy wrote, delivered on `FinalizeWorkspace` in a new `restore_chunks` field.

The normative statement of the mechanism is SPEC-1, the new §7.1 **Derive restore semantics:** paragraph. Every other edit below points at it and adds only what its own section owns: the RPC row and pairing rule (SPEC-2), the cross-tier materialization order (SPEC-3), the §19 pointer that retires the `uploadArchive` delivery model (SPEC-4), the capability scope (SPEC-5), the CH-OBJSTORE carrier and failure semantics (SPEC-6), and the SDK-warm demotion rule (SPEC-7).

## Edge cases and accepted failure modes

- A restore failure fails the bind and leaves `current` unchanged (SPEC-1). A later bind attempt mints fresh capabilities.
- A `FinalizeWorkspace` marked `mid_session` that carries `restore_chunks` is refused under rule 1 (SPEC-2).
- A source that overwrites a restored path wins and emits no `workspace_plan_path_collision` warning (SPEC-1).
- The bundle's session-file tree is never restored (SPEC-1).
- A preConnect session with a restore and a non-empty `sdkWarmBlockingPaths` list is demoted (SPEC-7).

## Staged edits

### SPEC-1 · spec/07_session-lifecycle.md § 7.1 Normal Flow, and § 7.4 Upload Safety; spec/15_external-api-surface.md § 15.1 REST API

**(a)** Insert a new paragraph immediately after the paragraph that begins `**Derive copy semantics:** \`POST /v1/sessions/{id}/derive\` always performs a full copy` and before the paragraph that begins `**Derive session semantics:**`.

```markdown
**Derive restore semantics:** A session that `POST /v1/sessions/{id}/derive` or `POST /v1/sessions/{id}/replay` creates starts from the snapshot the derive copy wrote (**Derive copy semantics:** above). It is the only kind of session whose record carries a workspace snapshot before its first bind. At that bind the gateway resolves the derived manifest row the session's `WorkspaceSnapshot.Ref` names, using the contiguity check and per-index presign of [Section 10.1](10_gateway-internals.md#101-horizontal-scaling) **Reassembly on resume.**, without its generation selection, partial-recovery threshold, or full-checkpoint fallback, which do not apply to the complete derived row. The gateway mints one single-key `GET` capability per chunk immediately before the `FinalizeWorkspace` call of [Section 4.7.9](04_system-components.md#479-startup-sequence-for-type-agent-runtimes) step 5 and passes them on that call in `restore_chunks`; every bind attempt mints its own. The adapter fetches the chunks in ascending index order, decodes the concatenated stream by the manifest's `chunk_encoding`, and extracts only the bundle's workspace tree into the build tree before it materializes the bound plan's `sources` array. The sources then apply over the restored tree in declaration order under [Section 14.1](14_workspace-plan-schema.md#141-workspaceplan-schema-versioning) **Path collision rule (within `sources` array).**, and the build tree replaces `/workspace/slots/{sessionId}/current` in one promotion. A source that overwrites a restored path emits no `workspace_plan_path_collision` warning, because that warning's index fields name only `sources` entries. The bundle's session-file tree is not restored, because the new session starts its runtime fresh, and a replayed session may run a different runtime ([Section 15.1](15_external-api-surface.md#151-rest-api) **Session Replay Semantics**). The restored snapshot is platform output that the derive copy wrote. It is not an `uploadArchive` source, and the [Section 7.4](#74-upload-safety) archive limits do not apply to it; the adapter's extraction enforces path containment and its own size cap. The restore has no fallback. When the gateway cannot resolve the row, the chunks are not contiguous, the row has no chunk, a capability cannot be signed, or the adapter cannot fetch, decode, or extract a chunk, the `FinalizeWorkspace` stage of [Section 4.7.9](04_system-components.md#479-startup-sequence-for-type-agent-runtimes) step 5 fails, and the bind fails as any other failed bind does, including **Pod-side reclaim on a failed bind.** above; `/workspace/slots/{sessionId}/current` is left unchanged.
```

**(b)** In §7.4 Upload Safety, in the bullet that begins `Archive extraction is especially strict.`, replace

```markdown
All archive extraction runs inside the gateway's Upload Handler subsystem
```

with

```markdown
All extraction of client-uploaded archives and delegation file exports runs inside the gateway's Upload Handler subsystem
```

The rest of the bullet is unchanged.

**(c)** In §7.1 derive rule 2, **Concurrent derive serialization.**, replace

```markdown
The only scenario where the copy could fail is if the referenced object has been deleted (e.g., by a GC bug or premature TTL expiry);
```

with

```markdown
The only scenario where the copy could fail is if the source's snapshot ref names no manifest row with at least one chunk, or a chunk object it names has been deleted (e.g., by a GC bug or premature TTL expiry);
```

The rest of the rule is unchanged.

**(d)** In §15.1, under **Error code catalog:**, in the description cell of the `DERIVE_SNAPSHOT_UNAVAILABLE` row, replace

```markdown
`POST /v1/sessions/{id}/derive` failed because the referenced workspace snapshot object was not found in object storage (e.g., deleted by a GC bug or premature TTL expiry).
```

with

```markdown
`POST /v1/sessions/{id}/derive` or `POST /v1/sessions/{id}/replay` failed because the source's workspace snapshot was not found ([Section 7.1](07_session-lifecycle.md#71-normal-flow) **Concurrent derive serialization.**).
```

and in the same cell replace `` `details.snapshotRef` includes the missing object path. `` with `` `details.snapshotRef` carries the source's snapshot ref. ``. The rest of the cell is unchanged, and the row stays one physical line.

### SPEC-2 · spec/04_system-components.md § 4.7.1 Role and Gateway RPC Contract

**(a)** In the Gateway → Adapter RPC table, replace the `FinalizeWorkspace` row. The row stays one physical line; pad the cells to the table's existing width or leave them unpadded, as the neighbouring rows allow.

```markdown
| `FinalizeWorkspace`  | Validate. When the request carries `restore_chunks`, extract the starting snapshot's workspace tree into the build tree first ([Section 7.1](07_session-lifecycle.md#71-normal-flow) **Derive restore semantics:**). Then materialize the plan's sources over it and promote to `/workspace/slots/{sessionId}/current`. |
```

**(b)** In rule 1, **The pairing rule.**, replace the sentence

```markdown
A request other than a `Shutdown` that carries `bind_attempt`, whose `bind_attempt` is empty and which is not marked `mid_session`, and one marked `mid_session` whose `bind_attempt` is non-empty, are each answered `INVALID_ARGUMENT`, and the adapter creates, resolves, stamps, and changes nothing, even when it holds no entry for that slot.
```

with

```markdown
A request other than a `Shutdown` that carries `bind_attempt`, whose `bind_attempt` is empty and which is not marked `mid_session`, one marked `mid_session` whose `bind_attempt` is non-empty, and a `FinalizeWorkspace` marked `mid_session` that carries `restore_chunks`, are each answered `INVALID_ARGUMENT`, and the adapter creates, resolves, stamps, and changes nothing, even when it holds no entry for that slot.
```

The carriage table and rule 9 are unchanged.

### SPEC-3 · spec/05_runtime-registry-and-pool-model.md § 5.1 Derived Runtime Instantiation, and spec/14_workspace-plan-schema.md § 14.1

**(a)** In §5.1 **Derived Runtime Instantiation**, replace the sentence

```markdown
Workspace materialization order: base defaults → derived defaults → client uploads → file exports from parent delegation.
```

with

```markdown
Workspace materialization order: source workspace snapshot (a derived or replayed session only, [Section 7.1](07_session-lifecycle.md#71-normal-flow) **Derive restore semantics:**) → base defaults → derived defaults → client uploads → file exports from parent delegation.
```

The sentence that follows, which begins `Before materializing, the adapter MUST remove all files`, is unchanged. If proposal 0086 has rewritten the earlier sentences of this paragraph, apply the replacement to the `Workspace materialization order:` sentence wherever it now stands.

**(b)** In §14.1 **Path collision rule (within `sources` array).**, delete the parenthetical that follows the Section 5.1 link, so that

```markdown
[Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime) (base defaults → derived defaults → client uploads → file exports from parent delegation), where each tier overwrites
```

reads

```markdown
[Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime), where each tier overwrites
```

No other text in §14.1 changes.

### SPEC-4 · spec/19_resolved-decisions.md § 19 Resolved Decisions, row 12

In row 12 (Session forking), keep the manual recipe (1) through (3) and replace the sentence

```markdown
The gateway also provides a convenience endpoint `POST /v1/sessions/{id}/derive` that performs steps 1-3 atomically — it creates a new session pre-populated with the previous session's workspace snapshot.
```

with

```markdown
The gateway also provides `POST /v1/sessions/{id}/derive`, which creates a new session that starts from the previous session's workspace snapshot without the client downloading or uploading it ([Section 7.1](07_session-lifecycle.md#71-normal-flow) **Derive copy semantics:** and **Derive restore semantics:**).
```

The row stays one physical line.

### SPEC-5 · spec/13_security-model.md § 13.1, § 13.2.2, § 13.2.3, § 13.3, and spec/04_system-components.md § 4.4.3 Checkpoint Quiescence, Timeout, and Abort Handling

**(a)** §13.2.3 Checkpoint Capability Model, bullet beginning `The pod holds no \`LIST\`, \`DELETE\`, or multipart capability`. Replace

```markdown
The `GET` capabilities minted on resume name only the chunk keys of the pod's own session, which the same pod authored, so they grant no read the pod did not already have; the gateway resolves the manifest row, checks contiguity, and signs, so the pod chooses no key.
```

with

```markdown
The `GET` capabilities minted on resume name only the chunk keys of the pod's own session, which the same pod authored, so they grant no read the pod did not already have; the `GET` capabilities minted for a derived or replayed session's first bind ([Section 7.1](07_session-lifecycle.md#71-normal-flow) **Derive restore semantics:**) name only the chunk keys the derive copy wrote under that session's own prefix in the same tenant, so they grant read of the snapshot the session starts from and of no other key; the gateway resolves the manifest row, checks contiguity, and signs, so the pod chooses no key.
```

**(b)** §13.1 Pod Security table, `Checkpoint transfer` row. Replace

```markdown
The agent pod `PUT`s checkpoint chunks to, and on resume `GET`s them from, object storage
```

with

```markdown
The agent pod `PUT`s checkpoint chunks to, and on resume and at a derived or replayed session's first bind `GET`s them from, object storage
```

The row stays one physical line.

**(c)** §13.2.2, the paragraph under **Allow object-store egress for checkpoint transfer — supplemental policy (NET-071):**. Replace

```markdown
Pods in pools that write checkpoints need egress to object storage so the agent pod can `PUT` checkpoint chunks and, on resume, `GET` them back.
```

with

```markdown
Agent pods need egress to object storage so the agent pod can `PUT` checkpoint chunks and, on resume and at a derived or replayed session's first bind, `GET` them back.
```

In the §13.2.2 base-policy paragraph under **Allow pod-to-gateway and DNS — base policy**, replace `object-store egress for checkpoint chunk transfer is conditionally added by` with `object-store egress for checkpoint chunk transfer is added for every agent pod by`.

In the `allow-pod-egress-objectstore` YAML block under §13.2.3, delete the line `# Only rendered for pools that write checkpoints`.

**(d)** §13.3 Credential Flow. Replace

```markdown
The pod `PUT`s checkpoint chunks and, on resume, `GET`s them against these capabilities (NET-071, §13.2).
```

with

```markdown
The pod `PUT`s checkpoint chunks and, on resume and at a derived or replayed session's first bind, `GET`s them against these capabilities (NET-071, §13.2).
```

**(e)** spec/04_system-components.md §4.4.3 **Checkpoint abort cleanup:**. Replace `it receives single-key \`PUT\` and, on resume, single-key \`GET\` capabilities only` with `it receives single-key \`PUT\` and single-key \`GET\` capabilities only`. The paragraph stays one physical line.

### SPEC-6 · spec/28_communication-channels.md § 28.5.5 Pod-egress (CH-OBJSTORE), and § 28.8 Failure and degradation matrix

The §28.5.5 text is hard-wrapped; re-wrap each edited bullet to the file's existing width. No register row changes.

**(a)** CH-OBJSTORE **Messages.** Replace

```markdown
capabilities the gateway mints and passes on the unary `Resume` call
([§10.1](10_gateway-internals.md#101-horizontal-scaling)).
```

with

```markdown
capabilities the gateway mints and passes on the unary `Resume` call
([§10.1](10_gateway-internals.md#101-horizontal-scaling)), or on `FinalizeWorkspace` at a derived or
replayed session's first bind ([§7.1](07_session-lifecycle.md#71-normal-flow) **Derive restore semantics:**).
```

**(b)** CH-OBJSTORE **Preconditions.** Replace

```markdown
restore-path capabilities are minted by the
gateway and passed on the unary `Resume` call
([§10.1](10_gateway-internals.md#101-horizontal-scaling)).
```

with

```markdown
restore-path capabilities are minted by the
gateway and passed on the unary `Resume` call
([§10.1](10_gateway-internals.md#101-horizontal-scaling)), or on `FinalizeWorkspace` at a derived or
replayed session's first bind ([§7.1](07_session-lifecycle.md#71-normal-flow) **Derive restore semantics:**).
```

In the same bullet, replace

```markdown
The egress itself is permitted only for pools
that write checkpoints, for which the supplemental `allow-pod-egress-objectstore` policy is rendered
([§13.2](13_security-model.md#132-network-isolation)).
```

with

```markdown
The egress itself is permitted by the supplemental `allow-pod-egress-objectstore` policy
([§13.2](13_security-model.md#132-network-isolation)).
```

**(c)** CH-OBJSTORE failure semantics. In the sentence that begins `On the restore path, a contiguity failure,`, replace `On the restore path,` with `On the resume restore path,`. Immediately after that sentence's closing citation `([§10.1](10_gateway-internals.md#101-horizontal-scaling)).` and before `The specification does not state a retry policy`, insert

```markdown
A derived or replayed session's bind-time restore has no fallback ([§7.1](07_session-lifecycle.md#71-normal-flow) **Derive restore semantics:**).
```

**(d)** §28.8, `CH-OBJSTORE` row, second cell. Replace `On the restore path a contiguity failure` with `On the resume restore path a contiguity failure`. Immediately after that clause's closing citation `([§4.4](04_system-components.md#44-event--checkpoint-store), [§10.1](10_gateway-internals.md#101-horizontal-scaling)).` and before `The specification does not state a retry policy`, insert

```markdown
A derived or replayed session's bind-time restore has no fallback ([§7.1](07_session-lifecycle.md#71-normal-flow) **Derive restore semantics:**).
```

The row stays one physical line.

### SPEC-7 · spec/06_warm-pod-model.md § 6.1 What a Pre-Warmed Pod Looks Like

Append to the end of the paragraph that begins `**\`sdkWarmBlockingPaths\` matching contract.**`, after `demotion is triggered even when the client uploads no files.`:

```markdown
A session whose workspace starts from a restored snapshot ([Section 7.1](07_session-lifecycle.md#71-normal-flow) **Derive restore semantics:**) matches every non-empty `sdkWarmBlockingPaths` list, because the restored paths are not known before materialization. An empty list disables the check for it as for any other session.
```

## Spec files touched

- spec/07_session-lifecycle.md (SPEC-1)
- spec/15_external-api-surface.md (SPEC-1)
- spec/04_system-components.md (SPEC-2, SPEC-5)
- spec/05_runtime-registry-and-pool-model.md (SPEC-3)
- spec/14_workspace-plan-schema.md (SPEC-3)
- spec/19_resolved-decisions.md (SPEC-4)
- spec/13_security-model.md (SPEC-5)
- spec/28_communication-channels.md (SPEC-6)
- spec/06_warm-pod-model.md (SPEC-7)
