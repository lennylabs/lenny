# Spec changes: Pod-security specification defects left unstaged by proposal 0088

## Design (as the spec must state it)

**Credential-file boundary (SPEC-A, SPEC-C).** The `lenny-cred-readers` group and a mount of the credential tmpfs volume together form the credential-file boundary. The pod-level `fsGroup` makes every container a group member, so the mount is the part a container can be denied. In the sidecar model the `adapter` and `runtime` regular containers may mount the volume; in the embedded model the single `runtime` container may. `lenny-pod-security` keys the exempt containers by name, and §13.1 states the resulting embedded-model residual. A runtime's own subprocesses inherit the group and the mount, and no in-container mechanism can drop the group under the capability posture, so the only stated obligation is not to spawn subprocesses that should not see credentials.

**Rejection labels (SPEC-B1, SPEC-B2).** §17.2 **Rejection labels.** is the single normative statement of the convention. Every other carrier cites it by heading.

## Edge cases and accepted failure modes

- An embedded-model pod with an injected regular container named `adapter` is admitted, and that container can mount the credential volume. SPEC-C states and accepts this residual.
- A subprocess that the `runtime` container spawns keeps the `lenny-cred-readers` group and sees the mounted credential file. SPEC-A states that no process can drop the group and places the obligation on the runtime author.
- A `lenny-pod-security` rejection under 0088's **Container identity.** clause carries no rejection label. **Rejection labels.** governs a label only when a gate emits one.

## Staged edits

### SPEC-A · spec/13_security-model.md § 13.1 Pod Security

Two edits inside the paragraph that begins `**\`lenny-cred-readers\` membership boundary.**`. The paragraph stays one physical line. Its bold label, its opening clause `The \`lenny-cred-readers\` supplementary group is the credential-file read boundary inside the pod;`, the ephemeral-guard sentences, and its closing text `"Deployer acknowledgment (runtime process kept across sessions)").` stay byte-identical.

**Edit 1.** Replace the text that begins `its membership is deliberately narrow.` and ends `declares the \`lenny-cred-readers\` GID with \`POD_SPEC_CRED_GROUP_OVERBROAD\`.` with:

```markdown
the boundary is the group together with a mount of the credential tmpfs volume. Group membership alone does not confer access, because the kubelet applies the pod-level `fsGroup` as a supplementary group to every container in the pod (see **Cross-UID file delivery without `CAP_CHOWN` (fsGroup-based).** above); a container reaches the credential file only when it also mounts the credential volume. In the sidecar deployment model, two containers may mount the credential volume: the adapter container (the regular container named `adapter`), which writes the credential file, and the agent container (the regular container named `runtime`), which reads it. In the embedded deployment model ([§4.7.10](04_system-components.md#4710-deployment-model)), the single `runtime` container writes and reads the file. No other container in a Lenny-managed agent pod, whether a sidecar, an init container, an operator-injected container, or an ephemeral debug container, may mount the credential tmpfs volume or any path equal to `/run/lenny` or beginning with `/run/lenny/`, and no such container may declare the `lenny-cred-readers` GID in its container-level `runAsGroup`. The `lenny-pod-security` admission webhook enforces this by rejecting, with `POD_SPEC_CRED_GROUP_OVERBROAD`, any agent pod in which a container other than the regular containers named `adapter` and `runtime` declares the `lenny-cred-readers` GID in `runAsGroup`, mounts the credential volume by name, or mounts a path equal to `/run/lenny` or beginning with `/run/lenny/`.
```

**Edit 2.** Replace the sentence `Runtime authors MUST either (a) avoid spawning subprocesses that should not see credentials, or (b) invoke \`setgroups(0, NULL)\` in a pre-exec step to drop the supplementary group before \`execve\`.` with:

```markdown
Runtime authors MUST avoid spawning, inside the `runtime` container, subprocesses that should not see credentials. No process in an agent-pod container can drop the supplementary group, because `setgroups(2)` requires `CAP_SETGID` and every agent-pod container drops all capabilities (the Capabilities row of the control table above).
```

The sentence before it (`Within the agent container itself, ... by default.`) and the sentence after it (`The spec does not mandate a specific subprocess-isolation mechanism ...`) stay unchanged.

### SPEC-C · spec/13_security-model.md § 13.1 Pod Security

Insert immediately after the sentence SPEC-A Edit 1 ends with (`... or mounts a path equal to \`/run/lenny\` or beginning with \`/run/lenny/\`.`) and before `**Ephemeral debug containers attached post-hoc via \`kubectl debug\` are not pinned`, separated by one space and inside the same physical line:

```markdown
An embedded-model pod ([§4.7.10](04_system-components.md#4710-deployment-model)) has no container named `adapter`, so this check admits a regular container named `adapter` that the pod's creator or a mutating admission webhook adds to an embedded-model pod, and that container can mount the credential volume. Lenny accepts this residual because only an actor that controls the agent pod's CREATE request can add a regular container, and that actor already sets the `runtime` container's command and environment, which this webhook does not constrain.
```

### SPEC-B1 · spec/17_deployment-topology.md § 17.2 Namespace Layout

**Edit 1, admission-policies item 3.** Replace the item's opening `3. **\`POD_SPEC_HOST_SHARING_FORBIDDEN\`** validation policy that rejects pods` with `3. **Host-sharing validation policy** that rejects pods`, and append after the item's closing `(see [Section 13.1](13_security-model.md#131-pod-security)).`, separated by one space:

```markdown
Rejections carry the rejection label `POD_SPEC_HOST_SHARING_FORBIDDEN` (see **Rejection labels.** below).
```

**Edit 2, admission-policies item 13.** Replace the sentence `Rejections emit \`EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN\` ([Section 15.1](15_external-api-surface.md#151-rest-api)).` with:

```markdown
Rejections carry the rejection label `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` (see **Rejection labels.** below).
```

**Edit 3, new paragraph.** Insert after item 13 and before the paragraph that begins `**High-availability requirement applies to every entry above.**`, separated from both by one blank line, as one physical line:

```markdown
**Rejection labels.** The admission gates that validate the spec of an agent pod, or of an ephemeral container added to one, are the host-sharing validation policy (item 3), `lenny-t4-node-isolation` (item 10), `lenny-ephemeral-container-cred-guard` (item 13), and the `lenny-pod-security`, `lenny-cosign-verify`, and `lenny-registry-digest` webhooks, which this list does not enumerate. When one of these gates names the violated rule with an uppercase rejection label, such as `POD_SPEC_HOST_SHARING_FORBIDDEN` or `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN`, the label is a reason inside the gate's Kubernetes admission denial message. The denial returns to the Kubernetes API client that submitted the pod or ephemeral-container request, such as the warm pool controller or an operator running `kubectl debug`. No Lenny REST, MCP, or admin API response carries one of these labels, so these labels are absent from the error code catalog in [Section 15.1](15_external-api-surface.md#151-rest-api) and have no error category or HTTP status of their own. Logs, Kubernetes events, and `lenny-preflight` findings match on the label.
```

The **High-availability requirement applies to every entry above.** paragraph stays unchanged.

### SPEC-B2 · spec/13_security-model.md § 13.1 Pod Security and spec/15_external-api-surface.md § 15.1 REST API

**Edit 1, §13.1 host-sharing paragraph.** Replace the sentence `The admission webhook ([§10.2](10_gateway-internals.md#102-authentication)) rejects any CR that would produce a pod spec with any of these fields set to \`true\` with \`POD_SPEC_HOST_SHARING_FORBIDDEN\` (field-level detail in the error body: which field(s) tripped the rejection).` with:

```markdown
The `lenny-pod-security` admission webhook rejects any agent pod that sets any of these fields to `true`. Its denial message carries the rejection label `POD_SPEC_HOST_SHARING_FORBIDDEN` and names the field or fields that tripped the rejection ([Section 17.2](17_deployment-topology.md#172-namespace-layout), **Rejection labels.**).
```

**Edit 2, §13.1 membership paragraph.** Replace the clause `Rejections carry \`EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN\` ([Section 15.1](15_external-api-surface.md#151-rest-api));` with the text below. The alert clause that follows (`webhook unavailability raises the \`EphemeralContainerCredGuardUnavailable\` alert ...`) stays unchanged.

```markdown
Rejections carry the rejection label `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` ([Section 17.2](17_deployment-topology.md#172-namespace-layout), **Rejection labels.**);
```

**Edit 3, §15.1 Error code catalog.** Delete the row whose first cell is `` `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` ``. Leave the neighbouring rows untouched.

## Spec files touched

- spec/13_security-model.md
- spec/15_external-api-surface.md
- spec/17_deployment-topology.md
