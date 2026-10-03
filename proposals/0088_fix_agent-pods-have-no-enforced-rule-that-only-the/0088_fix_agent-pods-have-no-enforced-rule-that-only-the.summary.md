# Summary: Only the runtime container runs at the agent UID, and every agent-pod container sets runAsGroup

## Summary

**Problem statement.** Agent pods do not pin the process identity of their init and regular containers. No rule reserves the adapter UID for the `adapter` container or the agent UID for the `runtime` container, so a container that a deployer's mutating webhook injects is admitted at a reserved UID. No platform container sets `runAsGroup`, so the image chooses the primary GID, which can be 0 or the `lenny-cred-readers` GID. The §4.7.11 `SO_PEERCRED` check, which §13.1 names as the primary adapter-agent control, identifies the agent only if the agent UID belongs to one container. The test-only egress-capture container runs at the agent UID and would collide with any such rule.

**What changes.**

- SPEC-1 adds the §13.1 paragraph **Container identity.** to `spec/13_security-model.md`.
- CODE-1 adds the identity clause to the `ValidateAgentPod` per-container loop in `pkg/podsecurity`.
- CODE-2 passes the adapter and agent UIDs that `cmd/lenny-webhook` already parses into the `lenny-pod-security` webhook in `pkg/admission/webhook`.
- CODE-3 sets `runAsGroup` in the pod builder's `containerSecurityContext` and moves egress capture to a non-reserved UID in `pkg/controller/sandbox/podspec` and `cmd/lenny-egress-capture`.
- TEST-1 adds the Kind and security-tier cases and gives every hand-written agent-namespace test pod an explicit container identity.
- DOCS-1 adds a policy item to `docs/operator-guide/namespace-and-isolation.md`.

**Decisions.**

- D1. Every container sets an explicit container-level `runAsUser` and `runAsGroup`, both nonzero. No pod-level identity default exists.
- D2. The reservation is name-keyed and one-directional, and only regular containers named `adapter` or `runtime` can own a reserved UID.
- D3. The rule applies uniformly to init (including native sidecars), regular, and ephemeral containers, with no container-kind marker or `restartPolicy` exemption.
- D4. The builder sets each platform container's `runAsGroup` equal to its `runAsUser`.
- D5. (Withdrawn: owner decision 2026-10-03.)
- D6. The §13.1 sentence permitting "`runAsGroup` for the adapter" stays unedited.
- D7. The rule adds no error code, no §15.1 row, no flag, no chart value, no webhook, and no preflight check.
- D8. Egress capture moves to a code constant UID and writes its capture file with mode 0640.
- D9. The draft-0087 reconciliation is recorded under **Impacts on other proposals** and is not staged as edits to 0087.

Reasons for each decision follow.

- D1. Admission cannot see an image's `USER`, so explicit values make the identity the webhook reads the identity that runs. Every injected container that passes today's baseline already writes a container `securityContext`, because `allowPrivilegeEscalation`, `readOnlyRootFilesystem`, and `capabilities.drop` have no pod-level form. Cred-guard condition (iii) already applies the same rule to ephemeral containers, so the pod gets one rule. A pod-level default needs a new operator-tunable UID and silently overrides an injected sidecar's image `USER`, which turns an admission rejection into a runtime crash.
- D2. The apiserver keeps container names unique across the three lists, and `translatePodSpec` fills the credential set from regular containers only, so an init or ephemeral container named `adapter` cannot own a reserved UID. A bidirectional binding (requiring `adapter` to run at the adapter UID) protects function rather than isolation, and only the builder writes those values. The embedded model meets the rule by construction.
- D3. For ephemeral containers the clause overlaps cred-guard (i) and (iii) and adds the nonzero condition. An init-container exemption would let a container create files owned by a reserved UID on shared volumes.
- D4. GID equal to UID reuses the existing tunable UIDs and adds no value or flag.
- D5. (Withdrawn: owner decision 2026-10-03.)
- D6. The sentence stays true: the new rule constrains `runAsGroup` only to "set and nonzero", and `POD_SPEC_CRED_GROUP_OVERBROAD` still allows the GID on the adapter.
- D7. Existing `User`-row and per-container baseline violations reject with a cited row and no code, and nothing branches on a code. The new violations follow that precedent. The fail-closed webhook validates every Pod CREATE and UPDATE after mutation, and no deployments predate the rule, so a preflight audit adds nothing.
- D8. The container is absent from the spec, so the move is code-only. The capture emptyDir is fsGroup-managed, so the file's group is `lenny-cred-readers`, which `runtime` holds as a supplementary group. A collision between the constant and an operator-chosen `security.podUIDs` value fails closed at the webhook.
- D9. 0087 is a Draft whose next change-proposal round authors its own changes.
- The no-change stance is rejected. Its factual points hold: the spec already requires the manifest nonce on `CH-RUNTIMEOPS` as well as `CH-MSGSOCK`, abstract-name squatting does not depend on UID, and an injected container sits outside the runtime's PID namespace at every UID. §13.1 nevertheless names the `SO_PEERCRED` UID check as the primary control, running without it is a degraded mode gated behind `SecurityDegradedMode=True`, and §4.7.11 item 1 ties the expected UID to the pod-spec `runAsUser`. A primary control whose premise nothing enforces is a spec gap. The GID half conforms code to the existing §13.1 `User` row and rides here because it edits the same `containerSecurityContext` line.

**Watch out for.**

- CODE-1's nonzero clause rejects every pod the current builder produces, because no container sets `runAsGroup` today. CODE-3 lands before CODE-1.
- The CODE-2 lockstep test's egress-capture variant passes only after CODE-3 moves capture off the agent UID.
- Changing the `PodSecurity` signature breaks the build of `tests/integration` (build tag `integration`), which `go build ./...` without tags does not reveal. CODE-2 updates that call site.
- Once a webhook image built from S3 is loaded into a Kind cluster, every hand-written agent-namespace test pod without explicit container identities fails admission. Do not run tier 5, 8, or 9 between S3 and S4.
- A pod-level `runAsUser` on a test fixture looks compliant and is rejected. Set identities on each container.
- A fixture at UID 65532 collides with the default adapter UID, which is also the distroless `nonroot` UID.

## Goals

- §13.1 states that every agent-pod container carries an explicit nonzero UID and GID, and that each reserved UID belongs to one named container.
- The `lenny-pod-security` webhook enforces that rule on init, regular, and ephemeral containers, after mutating admission, under every RuntimeClass, in the sidecar and embedded models.
- Every container the pod builder renders carries an explicit `runAsGroup`, and no platform container other than `adapter` and `runtime` holds a reserved UID.
- The operator guide documents the rule for deployers who run sidecar-injecting webhooks.

## Non-goals

- A mandatory pod-level `runAsUser` and `runAsGroup` default with the reservation stated on effective identity (rejected under D1).
- Carrying both explicit per-container identity and a pod-level default. The default would never take effect.
- A new rejection code (such as `POD_SPEC_CONTAINER_IDENTITY_INVALID`), its §15.1 row, an `errorclassify` entry, or a docs error-catalog row. Reusing `POD_SPEC_CRED_GROUP_OVERBROAD` or `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` would misstate the cause.
- A `lenny-preflight` live-pod identity audit with new `--adapter-uid` and `--agent-uid` preflight flags. The precedent clause `POD_SPEC_CRED_GROUP_OVERBROAD` has no preflight half.
- Narrowing the `lenny-pod-security` Pod rule from CREATE and UPDATE to CREATE only. Pods built before the change exist only on dev and e2e clusters, which the TEST-1 preflight item clears, and the change exceeds the validated problem.
- A `ContainerKind` marker, or an exemption for non-restartable init containers.
- Excluding ephemeral containers from the new clause.
- A bidirectional binding that requires `adapter` to run at the adapter UID and `runtime` at the agent UID.
- Narrowing the §13.1 sentence "or, equivalently, `runAsGroup` for the adapter".
- Moving `POD_SPEC_CRED_GROUP_OVERBROAD` to the effective (pod-level-inherited) `runAsGroup`. Under explicit per-container identity, no container inherits a pod-level value.
- An edit to §17.2 admission-policies item 3. §17.2 lists neither the fsGroup clause nor the OVERBROAD clause that `lenny-pod-security` already enforces.
- A citation sentence in §4.7.11 item 1. SPEC-1 states the dependency once.
- Setting the runtime's `runAsGroup` to the `lenny-cred-readers` GID, or adding a GID constant or chart value for platform containers.
- Reserving GIDs per container, or giving injected containers distinct GIDs.
- Making the egress-capture UID overridable.
- Keeping egress capture at the agent UID with a name exemption, or moving it to the adapter UID.
- A new dedicated webhook, or extending `lenny-ephemeral-container-cred-guard` to regular and init containers. `lenny-pod-security` already runs post-mutation with `failurePolicy: Fail` and flattens every container.
- Staging edits to draft 0087's files.
- Extending manifest-nonce authentication to every adapter socket in place of the UID rule. It leaves the §13.1 primary `SO_PEERCRED` control without its premise.
- Retiring this proposal as a code-only fix.
- The `SO_PEERCRED` code fix of F-4.7.25 and the `CH-MSGSOCK` nonce.

## Open decisions for human to make

None.

## Defects in the shipped tree that this proposal does not stage

- The `CH-RUNTIMEOPS` listener in `pkg/adapter/runtimeops.go` performs neither a peer check nor a nonce check, against its card in the communication-channels section. It is an untracked code defect to record as its own BUILD-GAPS finding.
- In an embedded pod, an injected regular container named `adapter` passes both the existing OVERBROAD exemption and the new reservation, because the name keying does not distinguish the deployment model. This design does not widen the gap. Record it separately.
- The comments in `pkg/controller/sandbox/podspec/podspec.go` and `cmd/lenny-controller/flags.go` claiming that `lenny-pod-security` rejects the egress-capture container in production are inaccurate. They are a separate defect.
- The comment above the supplementalGroups check in `pkg/podsecurity/podsecurity.go` cites the specification by line ("Line 25"), against `spec-citations.md`.
- `POD_SPEC_HOST_SHARING_FORBIDDEN`, `POD_SPEC_CRED_FSGROUP_MISSING`, and `POD_SPEC_CRED_GROUP_OVERBROAD` appear in §13.1 prose but are not registered in the §15.1 error catalog. This proposal neither adds nor reuses them.
- The §13.1 **`lenny-cred-readers` membership boundary.** paragraph requires runtime authors to "(b) invoke `setgroups(0, NULL)` in a pre-exec step", which cannot succeed under the §13.1 control-table row "Capabilities | All dropped" with `allowPrivilegeEscalation: false`, because `setgroups` needs `CAP_SETGID`. SPEC-1 does not rely on the sentence and this proposal does not edit it. Record it as a separate specification finding.
- The `CH-MSGSOCK` listener performs no `SO_PEERCRED` peer check, which §4.7.11 item 1 requires. `NewSocketRuntimeProcess` binds a plain `net.Listen("unix", socket)` listener (`pkg/adapter/socketruntime.go:179`) and accepts the first connection without inspecting the peer (`pkg/adapter/socketruntime.go:326`). This proposal does not stage the fix because the specification already requires the check, so the fix needs no spec change, and BUILD-GAPS already tracks it as F-4.7.25 (`BUILD-GAPS.md:1983`). The UID rule this proposal stages supplies the premise that the check relies on.
- The `lenny-pod-security` chart template's header comment justifies matching Pod UPDATE on inaccurate grounds. CODE-2 edits that comment only to describe the identity clause.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0087 | Draft | Subsumes SPEC-6 **Agent UID on a supervised pod.** with §13.1 **Container identity.** | Drop SPEC-6 and cite **Container identity.** |
| 0087 | Draft | Rejects the `lenny-supervisor-stage` init container at the adapter UID or the agent UID, and without an explicit nonzero `runAsUser` and `runAsGroup`. | Give the stager an explicit nonzero identity outside both reserved UIDs. 0087 chooses the UID. |
| 0087 | Draft | Moves egress capture off the agent UID, which removes the basis for the `in.EgressCapture != nil` refusal in `Build` and the D-IDENTITY clause that rests on it. | Retire the refusal and the clause, or restate a basis for them. |
| 0087 | Draft | Falsifies the premise of the rejected alternative "New admission-webhook clauses for the agent-UID rule", that `podspec.Build` is the only producer of agent pods. | Correct or remove that rejected alternative. |
| 0087 | Draft | Orders the two proposals. | Land after 0088. |

## Deliverable index

- SPEC-1 — `spec/13_security-model.md` §13.1 — the new **Container identity.** paragraph.
- CODE-1 — `pkg/podsecurity/podsecurity.go` — identity clause in the `ValidateAgentPod` per-container loop.
- CODE-2 — `pkg/admission/webhook/pod_security.go`, `cmd/lenny-webhook/main.go` — pass the reserved UIDs to `PodSecurity` and copy `RunAsUser`.
- CODE-3 — `pkg/controller/sandbox/podspec/podspec.go`, `cmd/lenny-egress-capture/main.go` — builder `runAsGroup` and the egress-capture UID move.
- TEST-1 — `tests/` — Kind and security-tier cases and the agent-namespace fixture sweep.
- DOCS-1 — `docs/operator-guide/namespace-and-isolation.md` — policy item for the container identity check.
