# Spec changes: Only the runtime container runs at the agent UID, and every agent-pod container sets runAsGroup

These edits are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (as the spec must state it)

The §13.1 control table row `User | Non-root (specific UID/GID)` becomes a rule the admission webhook checks from the pod object alone. SPEC-1 is the single normative statement: a new §13.1 paragraph **Container identity.** that requires an explicit container-level identity on every agent-pod container, reserves each platform UID for one named regular container, and names `lenny-pod-security` as the enforcing webhook. The paragraph sits between the **`lenny-cred-readers` membership boundary.** paragraph and the **Per-slot credential-read scope** paragraph. It leaves the **Cross-UID file delivery without `CAP_CHOWN` (fsGroup-based).** paragraph and the `lenny-ephemeral-container-cred-guard` conditions unchanged.

## Edge cases and accepted failure modes

- An init container (including a native sidecar declared with `restartPolicy: Always`) or an ephemeral container named `adapter` or `runtime` owns no reserved UID.
- An injected sidecar that omits `runAsUser` or `runAsGroup` is rejected, including when a pod-level value would have supplied one. This rejection is accepted: the deployer configures the injector.
- Ephemeral containers fall under both this paragraph and cred-guard conditions (i) to (iv). The overlap is accepted, and each webhook rejects independently.

## Staged edits

### SPEC-1 · spec/13_security-model.md § 13.1 Pod Security

Insert a new paragraph after the paragraph that begins `**\`lenny-cred-readers\` membership boundary.**` and ends `"Deployer acknowledgment (runtime process kept across sessions)").`, and before the paragraph that begins `**Per-slot credential-read scope (\`maxConcurrentSessions > 1\`).**`. Separate it from both neighbours with one blank line. Keep the paragraph as one physical line.

```markdown
**Container identity.** Every init, regular, and ephemeral container in a Lenny-managed agent pod sets `securityContext.runAsUser` and `securityContext.runAsGroup` at container level, and neither value is 0. A pod-level `runAsUser` or `runAsGroup` does not satisfy this requirement. Each container states its own identity, as condition (iii) of `lenny-ephemeral-container-cred-guard` already requires for ephemeral containers, so a value set at the pod level never silently overrides the image `USER` of a container that a mutating webhook injects. The adapter UID is reserved for the regular container named `adapter`, and the agent UID is reserved for the regular container named `runtime`. No other container may set either reserved UID as its `runAsUser`, whether it is an init container (including a sidecar declared as an init container with `restartPolicy: Always`), a regular container, or an ephemeral container, and an init or ephemeral container named `adapter` or `runtime` holds no reservation. The `lenny-pod-security` admission webhook enforces these conditions fail-closed on every Pod CREATE and UPDATE and every `pods/ephemeralcontainers` UPDATE in an agent namespace, after mutating admission and under every RuntimeClass, so a container that a deployer's mutating webhook injects is checked as well. For ephemeral containers these conditions overlap conditions (i) and (iii) of `lenny-ephemeral-container-cred-guard`, which remain in force unchanged. The `SO_PEERCRED` check of [§4.7.11](04_system-components.md#4711-adapter-agent-security-boundary) item 1 compares a peer's UID with the `runAsUser` in the pod spec, and it identifies the agent only because this rule gives the agent UID to one container.
```

## Spec files touched

- `spec/13_security-model.md` (SPEC-1)
