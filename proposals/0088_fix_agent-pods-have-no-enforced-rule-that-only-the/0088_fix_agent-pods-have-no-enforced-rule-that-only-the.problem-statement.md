# Problem: Only the runtime container runs at the agent UID, and every agent-pod container sets runAsGroup

## Statement

Agent pods do not pin the process identity of their init and regular containers. No admission rule reserves the agent UID for the container named `runtime` or the adapter UID for the container named `adapter`. In the embedded model, the single `runtime` container holds the agent UID and no container holds the adapter UID. No agent-pod container and no pod-level security context sets `runAsGroup`, so the container runtime takes the primary GID from the image and can use 0. Ephemeral containers are outside this gap because `lenny-ephemeral-container-cred-guard` already reserves both UIDs on them and requires an explicit `runAsUser`, `runAsGroup`, and `supplementalGroups`.

The code reproduces the gap in three places. The `lenny-pod-security` webhook (`pkg/admission/webhook/pod_security.go`, which delegates to `pkg/podsecurity`) never reads `RunAsUser`, and its only `runAsGroup` check rejects the `lenny-cred-readers` GID on a non-credential container (`POD_SPEC_CRED_GROUP_OVERBROAD`). `basePod` in `pkg/controller/sandbox/podspec/podspec.go` sets no pod-level `runAsUser` or `runAsGroup`, and `containerSecurityContext` sets `runAsUser` without `runAsGroup`. The test-only egress-capture container runs at the agent UID.

A container that a deployer's mutating webhook injects (for example a service-mesh proxy, a Vault agent, or a log shipper), whose image `USER` or explicit `runAsUser` equals a reserved UID, is admitted. The consequence is narrower than the source finding states. Such a container cannot pass the manifest-nonce gate on `CH-MCP-PLATFORM` or `CH-MCP-CONNECTOR`, because the existing `POD_SPEC_CRED_GROUP_OVERBROAD` mount clause rejects any non-adapter, non-runtime container that mounts the credential volume or a path at or under `/run/lenny`, where the manifest lives. It does pass `SO_PEERCRED` on surfaces that carry no nonce, and it can bind abstract socket names in the shared network namespace and run processes outside the runtime container's PID namespace. Today `CH-MSGSOCK` checks no peer at all (F-4.7.25), so any container reaches it whatever its UID. The UID rule becomes the binding control once the `SO_PEERCRED` half of F-4.7.25 lands, because until the runtime-SDK nonce lands the peer-UID check is the only gate on `CH-MSGSOCK`. Draft proposal 0087's supervisor identity also assumes that no other container holds the agent UID.

The `runAsGroup` half already has spec basis. The §13.1 Pod Security control table requires `User | Non-root (specific UID/GID)`, so a pod whose image chooses the primary GID already violates current spec text, and setting `runAsGroup` on platform containers corrects code against that row. New spec text is needed for the exclusivity rule (each reserved UID held only by its named container), the admission clause covering init and regular containers, any pod-level identity default, and the preflight check. That text must reconcile with the existing §13.1 sentence that permits "`runAsGroup` for the adapter" as an equivalent form of `lenny-cred-readers` membership.

The proposal must settle one design decision rather than carry both mechanisms the source finding suggests. One option requires an explicit container-level `runAsUser` (and `runAsGroup`) on every init and regular container. The other sets a mandatory pod-level `runAsUser` and `runAsGroup` that is neither reserved UID, nor the `lenny-cred-readers` GID, nor 0, and states the reservation rule on each container's effective identity (container value, else pod value). If every container must set its own `runAsUser`, the pod-level default never takes effect. The choice decides whether injected sidecars that omit `runAsUser` are rejected or defaulted.

Constraints: keep this as one proposal covering UID and GID, because both edit the same §13.1 paragraphs, `containerSecurityContext`, `basePod`, `translateContainer`, the `pkg/podsecurity` validator, and the preflight template check. Extend the existing `lenny-pod-security` webhook, which already runs after mutating admission on Pod CREATE and UPDATE with `failurePolicy: Fail`; `lenny-webhook` already receives `--adapter-uid` and `--agent-uid` but passes neither to `PodSecurity`. Move the egress-capture container to a non-reserved UID in code, keeping its capture file readable by the `runtime` container through the GID choice. Register every rejection code the proposal reuses or adds in the §15.1 error catalog, which today lists only `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN`. Reconcile with draft 0087's staged §13.1 clause **Agent UID on a supervised pod**, its pod-builder refusal of egress capture on supervised pods, its adapter-UID init container, and its rejected alternative "New admission-webhook clauses for the agent-UID rule", whose premise that `podspec.Build` is the only producer of agent pods is false once mutating webhooks are considered. Cover the sidecar and embedded models and gVisor, Kata, and runc.

Out of scope: the `SO_PEERCRED` code fix of F-4.7.25 (spec §4.7.11 item 1 already requires it), the `CH-MSGSOCK` nonce (owned by the runtime-SDK proposal), and the unenforced code comment claiming that `lenny-pod-security` rejects the egress-capture container in production, which is a separate defect to record rather than absorb.

## Evidence

- verified: `spec/13_security-model.md` §13.1 control table row `User | Non-root (specific UID/GID)`.
- verified: `spec/13_security-model.md` §13.1 **Cross-UID file delivery without `CAP_CHOWN` (fsGroup-based)**, including "or, equivalently, `runAsGroup` for the adapter and `supplementalGroups` for the agent", and **lenny-cred-readers membership boundary** with `POD_SPEC_CRED_GROUP_OVERBROAD` and the ephemeral cred-guard conditions (i) to (iv).
- verified: `spec/17_deployment-topology.md` §17.2 admission-policies item 13, `lenny-ephemeral-container-cred-guard`, rejecting with `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN`.
- verified: `spec/04_system-components.md` §4.7.11 item 1 **Separate UIDs and connection authentication** (SO_PEERCRED UID matching the pod-spec `runAsUser`, plus a manifest-nonce handshake independent of the UID check).
- verified: `spec/28_communication-channels.md` §28.5.3 `CH-MSGSOCK` card (SO_PEERCRED agent-UID check and manifest nonce).
- refuted as cited: `spec/15_external-api-surface.md` §15.1 registers only `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN`. `POD_SPEC_CRED_GROUP_OVERBROAD` and `POD_SPEC_CRED_FSGROUP_MISSING` appear only in §13.1 prose, and no `POD_SPEC_*` code appears in `schemas/`.
- verified: `pkg/admission/webhook/pod_security.go` (`translateContainer` copies `RunAsGroup` and never `RunAsUser`; init, regular, and ephemeral containers are flattened into the validator).
- corrected path: the validator is `pkg/podsecurity`, not `pkg/admission/podsecurity`, which does not exist. `pkg/podsecurity/podsecurity.go` checks `RunAsGroup` only against the cred-readers GID and rejects credential-volume and `/run/lenny` mounts on non-credential containers.
- verified: `pkg/controller/sandbox/podspec/podspec.go` (`basePod` pod context has `RunAsNonRoot`, `FSGroup`, `SupplementalGroups`, and seccomp only; `containerSecurityContext` sets `RunAsUser` only; adapter at `adapterUID`, sidecar and embedded `runtime` at `agentUID`; `injectEgressCaptureSidecar` at `agentUID`; no init containers emitted; defaults `AdapterUID` 65532, `AgentUID` 65533, `CredReadersGID` 65534).
- verified: `pkg/adapter/peercred.go` and `pkg/adapter/peercred_linux.go` (`peerCheckedListener`); `pkg/adapter/socketruntime.go` accepts `CH-MSGSOCK` connections on a plain `net.Listen` listener with no peer check; `pkg/adapter/connectormcp.go` and `pkg/adapter/mcp/server.go` authenticate every MCP connection with the manifest nonce.
- verified: `cmd/lenny-webhook/main.go` takes `--adapter-uid` and `--agent-uid` and calls `webhook.PodSecurity(credReadersGID, podspec.CredVolumeName, rcPolicy)` without them.
- verified: `charts/lenny/templates/admission-policies/pod-security-webhook.yaml` matches Pod CREATE and UPDATE and `pods/ephemeralcontainers` UPDATE with `failurePolicy: Fail`.
- verified: `BUILD-GAPS.md` F-13.1.23 (High, OPEN), F-13.1.24 (Medium, OPEN), and F-4.7.25 (High, OPEN); `gateway-runtime-comms-remediation.md` §10.2 Phase 0 pod identity hardening.
- verified: proposal 0087 status Draft; its summary lists "New admission-webhook clauses for the agent-UID rule" as a rejected alternative because "`podspec.Build` is the only producer of agent pods".

## Who observes it

No exploit is reachable from this defect alone today. `CH-MSGSOCK` accepts any peer regardless of UID until F-4.7.25 lands, and the MCP sockets require the manifest nonce, which an injected container cannot read. Reaching the defect requires an injected container that already passes Lenny's per-container baseline (`readOnlyRootFilesystem`, all capabilities dropped, no privilege escalation, non-root) and the cosign image-verification webhook, and whose UID collides with a reserved UID. The realistic route is an accidental collision or a compromised deployer-trusted sidecar. The adapter UID default of 65532 equals the distroless `nonroot` user, so an accidental collision is more likely at the adapter UID than at the agent UID (65533). The `CredReadersGID` default of 65534 is the conventional `nogroup` GID, so an image running as `nobody` with no `runAsGroup` takes `lenny-cred-readers` as its primary GID. The egress-capture container is injected only when the controller has an egress-capture image configured and the Sandbox carries the capture annotation, and no chart value sets that image.

## What breaks if nothing changes

- Once the `SO_PEERCRED` half of F-4.7.25 lands, a container injected at the agent UID passes the only gate on `CH-MSGSOCK` until the runtime-SDK nonce lands, and it passes the peer-UID check on `CH-RUNTIMEOPS`, which carries no manifest nonce.
- An injected container at a reserved UID can bind abstract socket names in the pod's shared network namespace and runs outside the runtime container's PID namespace, so no process sweep in that container reaches it.
- Draft 0087's `CH-SUPERVISE` identity relies on agent-UID uniqueness, and in nonce-only mode ordering is its only identity control.
- The image author chooses the primary GID of every platform container, including 0, against the §13.1 `specific UID/GID` row. Today this gives the runtime image author nothing new, because shared volumes are fsGroup-managed. The stated harm of F-13.1.24 lands when a platform process such as 0087's supervisor runs inside the runtime container and inherits that GID.
- Adding any agent-UID exclusivity clause without moving the egress-capture container would reject every egress-capture test pod.

## Findings this unblocks

- BUILD-GAPS F-13.1.23 and F-13.1.24.
- The `SO_PEERCRED` half of F-4.7.25, whose peer-UID check is meaningful only when the agent UID is unique among init and regular containers.
- Draft 0087's supervisor identity, once the two proposals reconcile their §13.1 clauses.

## Prior art considered

- `lenny-ephemeral-container-cred-guard` (spec §13.1 conditions (i) to (iv), §17.2 item 13, `pkg/admission/webhook/ephemeral_container_cred_guard.go`) already reserves the adapter and agent UIDs and requires explicit `runAsUser`, `runAsGroup`, and `supplementalGroups` on ephemeral containers. This proposal does not restate it.
- `POD_SPEC_CRED_GROUP_OVERBROAD` in `pkg/podsecurity` already rejects a non-credential container that declares the cred-readers GID in `runAsGroup` or mounts the credential volume or `/run/lenny`. That clause contains the fsGroup credential-read exposure regardless of UID, so UID uniqueness does not change that exposure.
- The F-13.1.16 lockstep work already wires `--adapter-uid` and `--agent-uid` into `lenny-webhook` from the same chart values as the controller. No new webhook, flag, or chart value is needed.
- `pkg/preflight/credfsgroup.go` validates the credential `fsGroup` on agent-pod templates and is the pattern the preflight half extends.
- Draft 0087 SPEC-6 **Agent UID on a supervised pod** states a narrower rule (supervised pods only, enforced in the pod builder only) and adds an adapter-UID init container. No other landed or draft proposal stages a pod-wide UID rule or a `runAsGroup` requirement.
- A smaller alternative extends nonce authentication to every adapter socket. It removes socket impersonation but leaves abstract-name binding and 0087's dependency, so the UID rule is still required.

## Validated premises

Premise lens (verdict: revise).
- Holds: the webhook never reads `RunAsUser`; the validator has no UID check.
- Holds: `basePod` sets no pod-level `runAsUser` or `runAsGroup`; `containerSecurityContext` sets no `runAsGroup`; the egress-capture container runs at the agent UID and is test-only.
- Holds: `CH-MSGSOCK` has no peer check; only the MCP listeners use `peerCheckedListener`.
- Nuance: the UID rule is one of two layers. The manifest nonce is the other, and the credential-mount clause keeps it from injected containers.
- Refuted (load-bearing): "the spec states no runAsGroup requirement" and "these rules therefore need spec text before code". The §13.1 `specific UID/GID` row already requires an explicit GID, so the `runAsGroup` half corrects code against existing spec text. Only the exclusivity rule, the admission clause, the pod-level default, and the preflight check need new text.
- Corrected: the webhook already checks `runAsGroup` against the cred-readers GID, so the work extends an existing check. The spec permits `runAsGroup` equal to cred-readers on the adapter, and a new rule must not forbid it.
- Holds: ephemeral containers are already covered; the gap is init and regular containers and the pod-level context.
- Refuted: the three error codes are not all in §15.1; only `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` is.
- Refuted: 0087 does not add a supervisor on every sidecar pod; it covers only runtimes that declare `command`, and it stages its own §13.1 clause that this proposal must subsume or coordinate with.
- Scope note: the pod builder emits no init containers, and "sidecar init containers" do not exist. Init-container coverage targets deployer-injected containers and future platform init containers such as 0087's stager.
- Holds: the Phase 0 scheduling and the BUILD-GAPS entries exist as described.

Evidence lens (verdict: stands).
- Verified every spec, code, and BUILD-GAPS citation listed under Evidence, except the two corrections below.
- Drifted: `pkg/admission/podsecurity` does not exist; the package is `pkg/podsecurity`.
- Partly false: the §15.1 citation for the `POD_SPEC_*` codes; neither is registered in §15.1 or `schemas/`, so a reused or new code sets the precedent for registration.

Prior-art lens (verdict: revise).
- Holds: the problem is novel apart from draft 0087's narrower SPEC-6.
- Refuted (load-bearing): the statement that 0087's `CH-SUPERVISE` authentication "relies on the pod-wide rule this proposal states". 0087 relies on `podspec.Build` refusing egress capture on supervised pods and rejects webhook clauses. 0088 must subsume or supersede SPEC-6, retire that refusal once egress capture leaves the agent UID, and correct 0087's rejected-alternative premise. "Must not preclude 0087" understates this.
- Holds: ephemeral-container coverage, the OVERBROAD credential-read containment, the existing webhook plumbing, and the post-mutation ordering are all prior art.
- Holds: the egress-capture container is absent from the spec, so moving it is code-only.
- Holds: §4.7.11 item 1 ties the expected UID to the pod-spec `runAsUser`, which presumes an explicit value.

Scope lens (verdict: revise).
- Holds (load-bearing): UID and GID are one problem with shared edit sites and belong in one proposal.
- Revised: the rule must reserve the adapter UID for `adapter` as well as the agent UID for `runtime`, matching cred-guard condition (i) and the BUILD-GAPS default.
- Revised: the explicit-`runAsUser` requirement and the pod-level default conflict; the proposal must state the invariant on effective identity and choose one control.
- Holds: the egress-capture move belongs here and is coupled to the GID choice through the shared capture file.
- Separated: the unenforced comment claiming production rejection of the egress-capture container is a distinct defect.
- Holds: the exclusions (F-4.7.25 `SO_PEERCRED` fix, `CH-MSGSOCK` nonce) are drawn correctly.

Impact lens (verdict: revise).
- Holds: the code facts behind F-13.1.23.
- Refuted (load-bearing): an agent-UID collision does not open `CH-MCP-PLATFORM` or `CH-MCP-CONNECTOR`, because both require the manifest nonce and the mount clause keeps it from injected containers.
- Refuted: the fsGroup cred-readers membership is independent of UID and already contained by the mount clause, so it is not a consequence of F-13.1.23.
- Holds (load-bearing): the binding impact is prospective. It arrives with the `SO_PEERCRED` half of F-4.7.25 and with 0087's supervisor.
- Holds: reaching the defect requires an injected container that passes the baseline and cosign checks and collides on a reserved UID.
- Holds: F-13.1.24 is mostly hardening today, and its stated harm depends on 0087.

Alternatives lens (verdict: revise).
- Holds: a no-change reading is not defensible; the spec gap for init and regular containers is real.
- Revised: the consequences are narrower than "every SO_PEERCRED check"; nonce extension alone does not cover abstract-name binding or 0087.
- Proposed (load-bearing, unresolved): a mandatory pod-level `runAsUser` and `runAsGroup` plus name-keyed reservation of container-level values is smaller than requiring an explicit `runAsUser` on every container, and it avoids rejecting injected sidecars that leave `runAsUser` unset. This is recorded as the design decision in the Statement.
- Holds: reserve both UIDs; the adapter UID default equals the distroless `nonroot` user.
- Holds: the egress-capture move is code-only; embedded mode satisfies the name-keyed rule by construction.
