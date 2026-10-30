# Summary: Pod-security specification defects left unstaged by proposal 0088

## Summary

**Problem statement.** Proposal 0088 records three pod-security defects that it does not stage, and this proposal fixes all three as independent groups. Group A: the §13.1 **`lenny-cred-readers` membership boundary.** paragraph offers runtime authors a `setgroups(0, NULL)` option that cannot succeed with every capability dropped, and it calls the membership "exactly two UIDs" although the pod-level `fsGroup` makes every container a member. Group B: admission rejection labels such as `POD_SPEC_HOST_SHARING_FORBIDDEN` have no stated convention, one of them (`EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN`) is cataloged in §15.1 with a status its guard never emits, §17.2 item 3 uses a label as a policy name, and §13.1 describes a CR admission path that does not exist. Group C: `lenny-pod-security` keys the credential containers by name, so in an embedded-model pod an injected regular container named `adapter` passes the check and can mount the credential volume, which §13.1 does not say.

**What changes.**
- §13.1 membership paragraph: the boundary is restated as the credential-volume mount together with the group for both deployment models, and the `setgroups` option is deleted (SPEC-A).
- §13.1 membership paragraph: two sentences state the embedded-model name-keying residual and why Lenny accepts it (SPEC-C).
- §17.2: a new **Rejection labels.** paragraph states the convention, and items 3 and 13 follow it (SPEC-B1).
- §13.1 and §15.1: the host-sharing sentence names `lenny-pod-security` and cites the convention, the ephemeral-guard clause cites it, and the §15.1 catalog row for `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` is deleted (SPEC-B2).
- Code and docs: the `errorclassify` entry, the comments that call the ephemeral label a §15.1 code, the registry-digest convention citation, and the docs error-catalog row follow the convention (CODE-B, DOCS-B).

**Decisions.**
1. Every group takes the smallest change that makes the specification true. No group adds a field, label, flag, error code, webhook clause, or state.
2. Group A deletes option (b) because `setgroups(2)` needs `CAP_SETGID`, which every agent-pod container drops. A capability drop also clears the bounding set, so Kata's `allowPrivilegeEscalation` relaxation cannot restore the capability. File ownership in the embedded model and `allowPrivilegeEscalation` are not cited as reasons.
3. Group A forbids declaring the GID in container-level `runAsGroup` only. Kubernetes has no container-level `supplementalGroups` field, and the pod-level list must contain the GID (`POD_SPEC_CRED_FSGROUP_MISSING`). The fix removes that wording from the sentence SPEC-A rewrites; the cred-guard and `kubectl debug` wording elsewhere in the paragraph stays as it is (see Defects not staged).
4. Group A names the embedded model's single `runtime` container as writer and reader, so the restated boundary holds for both models.
5. Group C accepts the name-keying residual and states it. Only an actor that controls agent-pod CREATE can add a regular container, and that actor already sets the `runtime` container's command and environment, so no escalation path closes. The owner chose this option on 2026-10-03 (OD-1).
6. Group C leaves 0088's **Container identity.** paragraph and the §4.7.11 **Activation.** embedded no-peer-check posture (owner decision in commit 6b94bc6e5) unchanged.
7. Group B states one convention: a rejection label is a reason inside the Kubernetes admission denial message and is absent from the §15.1 catalog. No §15 client receives one, because every emitter places it in a Kubernetes admission denial. The one cataloged label is deleted from §15.1, the docs catalog, and `errorclassify` rather than reconciled to 403.
8. The convention lives in §17.2, which owns the admission inventory, and its scope names `lenny-pod-security`, `lenny-cosign-verify`, and `lenny-registry-digest`, which the numbered list does not enumerate.
9. The convention covers only the gates that validate an agent pod's spec or an ephemeral container added to one. `lenny-pool-config-validator` and `lenny-data-residency-validator` put §15.1 codes in their denials, and several other webhooks use other conventions or no label.
10. The convention says what a label is when a gate emits one. It does not require every rejection to carry a label, because 0088's **Container identity.** rejections carry none, and it does not fix the label's position in the message, because `lenny-pod-security` and the Kyverno policy append it while the other gates prefix it.
11. The convention fixes no HTTP status. The ephemeral guard keeps 403, which its unit test pins.
12. §13.1's host-sharing sentence names `lenny-pod-security`. §17.2 item 3 is the Kyverno validation policy, a different gate.
13. The groups are textually independent of 0088 and land after 0088 is implemented on this branch.

**Watch out for.**
- 0088 SPEC-1 and draft 0087 SPEC-6 locate their insertion by the membership paragraph's bold label and its closing text `"Deployer acknowledgment (runtime process kept across sessions)").`. Keep both byte-identical.
- The §13.1 membership paragraph and every §15.1 catalog row are single physical lines. Keep them that way.
- SPEC-C anchors on the sentence SPEC-A writes. Land SPEC-A first.
- SPEC-B1's new paragraph sits between the numbered list and **High-availability requirement applies to every entry above.**, whose "entry above" and "this list" still mean the numbered list. Do not edit that paragraph.
- 0088 TEST-1 rewrites comments in `tests/tier9_security/admission_ephemeral_test.go`. Locate CODE-B's edit there by the `ephemeralCredRejectionCode` constant.
- `errorclassify.go` carries many other `// spec: 15:NNNN` line citations. CODE-B removes only the deleted entry's.

## Goals

- §13.1 states a credential-file boundary that the validator enforces and that holds in both deployment models.
- §13.1 mandates no subprocess mechanism that cannot work under the capability posture.
- §13.1 states the embedded-model name-keying residual and the reason Lenny accepts it.
- One stated convention governs the rejection labels of the agent-pod spec gates, and every specification, code, and docs carrier follows it.

## Non-goals

- A structural rule that makes a regular `adapter` credential-exempt only when the `runtime` container's credential mounts are read-only. It guards only against the actor Decision 5 names, and it adds a `VolumeMount.ReadOnly` field, a classification rule, a builder mount-flag security contract, fixture changes, and tier 1 and tier 9 tests. It remains the owner's fallback.
- A controller-stamped deployment-model label read by `lenny-pod-security`. The actor that controls CREATE writes the label too, and the label-immutability webhook guards UPDATE only, so pods created before the stamp would either be stranded or admitted fail-open.
- The rule "a regular `adapter` is exempt only at the adapter UID beside `runtime` at the agent UID". It does not discriminate, because the embedded builder runs `runtime` at the agent UID.
- Other structural discriminators (gRPC port, args, or the runtime-socket environment variable). They are not specification-normative, so a builder refactor would change classification silently.
- A Pool or Runtime lookup from the webhook to read the deployment model. It adds an API server read dependency to a fail-closed webhook that reads only the pod object today.
- Editing 0088's **Container identity.** reservation for embedded pods. The reservation holds as worded.
- Any `SO_PEERCRED` check on the embedded `@lenny-platform-mcp` socket. It would reverse §4.7.11 **Activation.** and the owner decision in commit 6b94bc6e5, and it protects only against the actor the residual names.
- Leaving group C unstated. Once SPEC-A states the mount boundary, §13.1 would be untrue for embedded pods.
- Group A without the boundary restatement. "Exactly two UIDs" contradicts the paragraph's own `fsGroup` text.
- Any in-container group-drop replacement (unprivileged user namespaces, `prctl`, or a privileged helper). None works with every capability dropped, and adding a capability weakens the posture.
- Registering the `POD_SPEC_*` labels, `IMAGE_SIGNATURE_INVALID`, `POD_IMAGE_DIGEST_REQUIRED`, and `T4_NODE_ISOLATION_VIOLATION` as §15.1 codes. No §15 client observes them.
- Keeping `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` in §15.1 with status 403, or changing the guard to emit 422. Either keeps two conventions for one class of labels.
- Stating an HTTP status in the convention, per-label specification text for each code-only label, or a required machine-parseable `CODE: detail` message prefix.
- Placing the convention inside the **Admission policy manifests** lead-in scoped to "this list", which would exclude the webhooks the numbered list does not enumerate.
- A tier 1 test asserting that `errorclassify` does not know the deleted code. It pins the absence of one string and encodes no specification behavior.
- A test that admits an embedded pod with an injected regular `adapter`. It would encode an accepted gap as required behavior.
- Edits to the §18 build-sequence text, the §13.1 `POD_SPEC_CRED_FSGROUP_MISSING` sentence, `pkg/preflight`, the Kyverno `shareProcessNamespace` message, `pkg/admission/cosign_verify`, `pkg/admission/t4_node_isolation`, and the `pkg/podsecurity` package comment. Each already uses its label as a reason inside a message.
- The `CH-RUNTIMEOPS` peer check and the code-comment hygiene items (hardening branch), and the `CH-MSGSOCK` and `CH-RUNTIMEOPS` nonce (runtime-SDK proposal).

## Open decisions for human to make

None. OD-1 (accept or close the embedded-model `adapter` name-keying residual) was answered by the owner on 2026-10-03: option 1, accept the residual and state it in §13.1 (Decision 5).

## Defects in the shipped tree that this proposal does not stage

- Cred-guard condition (ii) in §13.1 and in §17.2 item 13, and the `kubectl debug` sentence in §13.1, refer to a container-level `securityContext.supplementalGroups` field, which Kubernetes does not define. The fix is outside the validated problem.
- `lenny-direct-mode-isolation` emits CamelCase codes, `lenny-label-immutability` emits a lowercase label or none, and `lenny-sandboxclaim-guard`, `lenny-drain-readiness`, and `lenny-sandboxtemplate-deletion-guard` emit no label. The **Rejection labels.** convention does not cover these webhooks, and aligning them is a separate change.
- §17.2 item 3 says the host-sharing validation policy rejects all four host-sharing flags, while the Kyverno chart policy checks `shareProcessNamespace` only and `lenny-pod-security` checks all four. The inventory text is outside this proposal's scope.
- `lenny-pod-security`, `lenny-cosign-verify`, `lenny-registry-digest`, and `lenny-sandboxtemplate-deletion-guard` are absent from the §17.2 numbered admission inventory. **Rejection labels.** covers the first three by name, and the inventory drift is outside the validated problem.
- `pkg/gateway/externalapi/errorclassify/errorclassify.go` carries `// spec: 15:NNNN` line citations on many other entries, against `spec-citations.md`. CODE-B removes only the one on the entry it deletes.
- Four defects that 0088 also records stay in the tree. The `CH-RUNTIMEOPS` listener binds a plain `net.Listen("unix", socketPath)` (`pkg/adapter/runtimeops.go:135`) and accepts without a peer check or a manifest-nonce check (`pkg/adapter/runtimeops.go:181`). The `podspec.go` and `flags.go` comments claim that `lenny-pod-security` rejects the egress-capture container in production (`pkg/controller/sandbox/podspec/podspec.go:299-301`, `cmd/lenny-controller/flags.go:143`). The `pkg/podsecurity/podsecurity.go` comment cites the specification as "Line 25" (`pkg/podsecurity/podsecurity.go:262`). The runtime side of the `CH-MSGSOCK` and `CH-RUNTIMEOPS` manifest nonce is owned by the runtime-SDK proposal (`gateway-runtime-comms-remediation.md:2305`, `gateway-runtime-comms-remediation.md:2415-2416`). This proposal does not stage these fixes because no staged deliverable touches or relies on any of them: the peer check and the hygiene items are fixed on the hardening branch, and the nonce belongs to the runtime-SDK proposal.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0087 | Draft | SPEC-6 inserts a paragraph after the membership paragraph. This proposal keeps that paragraph's bold label and closing text byte-identical, so the anchor still resolves. | Nothing, unless a later revision of SPEC-6 quotes text from the body of the membership paragraph, which then has to match the text SPEC-A and SPEC-C stage. |

## Deliverable index

- SPEC-A — `spec/13_security-model.md` — restate the membership boundary as the credential-volume mount plus the group and delete the `setgroups` option.
- SPEC-C — `spec/13_security-model.md` — state the embedded-model name-keying residual.
- SPEC-B1 — `spec/17_deployment-topology.md` — add **Rejection labels.** and align admission-policies items 3 and 13.
- SPEC-B2 — `spec/13_security-model.md`, `spec/15_external-api-surface.md` — apply the convention to the §13.1 carriers and delete the §15.1 catalog row.
- CODE-B — `pkg/gateway/externalapi/errorclassify/errorclassify.go`, `pkg/admission/ephemeral_container_cred_guard/guard.go`, `pkg/admission/ephemeral_container_cred_guard/guard_test.go`, `pkg/admission/registry_digest/guard.go`, `tests/tier9_security/admission_ephemeral_test.go` — delete the classification entry and correct the comments that contradict the convention.
- DOCS-B — `docs/reference/error-catalog.md` — delete the `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` row.
