# Problem: Pod-security specification defects left unstaged by proposal 0088

## Statement

Proposal 0088 lists three pod-security defects under "Defects in the shipped tree that this proposal does not stage". This proposal is one draft that covers all three. It is structured as three independent deliverable groups that converge separately. Group C builds on 0088. Groups A and B do not depend on 0088 and can land in any order.

Group C assumes that proposal 0088 is implemented. Under that assumption, the §13.1 paragraph **Container identity.** has landed as 0088's spec-changes.md stages it. Every agent-pod container sets an explicit nonzero runAsUser and runAsGroup. The adapter UID is reserved for the regular container named `adapter`, and the agent UID is reserved for the regular container named `runtime`. lenny-pod-security enforces the reservation. Proposal 0088 is at status Reviewed today. The UID-reservation half of the group C gap therefore does not yet exist in the tree. Only the OVERBROAD half is live.

**A. setgroups guidance in §13.1 (spec-only).** The **`lenny-cred-readers` membership boundary.** paragraph says runtime authors MUST either "(a) avoid spawning subprocesses that should not see credentials, or (b) invoke `setgroups(0, NULL)` in a pre-exec step to drop the supplementary group before `execve`". Option (b) cannot succeed. setgroups(2) requires CAP_SETGID, and every agent-pod container drops ALL capabilities (§13.1 control table, "Capabilities | All dropped"). The missing capability is the operative reason. allowPrivilegeEscalation false is not the reason. That setting comes from pkg/podsecurity and the builder rather than from §13.1, and Kata pods are exempt from it, but option (b) still fails on Kata because the capabilities stay dropped. In the embedded model the runtime owns the credential file, so dropping the group would not remove read access there in any case. The correction leaves option (a) as the only stated mechanism and states plainly that this is the only compliant path for runtimes that spawn user shells. The same paragraph also calls the membership "exactly two UIDs", while the paragraph's own fsGroup text and the validator comment say that fsGroup makes every container a member. The correction can restate the boundary as the credential-volume mount plus the group in the same pass.

**B. Admission-webhook rejection labels.** POD_SPEC_HOST_SHARING_FORBIDDEN, POD_SPEC_CRED_FSGROUP_MISSING, and POD_SPEC_CRED_GROUP_OVERBROAD appear in §13.1 prose but are absent from the §15.1 error catalog. The webhook emits them only as substrings of a free-text admission denial with HTTP 403. The rejected caller is the Sandbox controller creating the Pod, so no §15 REST client ever receives them. They belong to a wider class of admission rejection labels that are also absent from §15.1: IMAGE_SIGNATURE_INVALID, POD_IMAGE_DIGEST_REQUIRED, and T4_NODE_ISOLATION_VIOLATION. The one registered member of the class, EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN, is cataloged in §15.1 and docs/reference/error-catalog.md as PERMANENT/422, while its guard emits HTTP 403. That precedent cannot fix an HTTP status for the others. Group B states one convention for every admission-webhook rejection label: either the label is a reason inside the Kubernetes denial message, or it is a registered API code with a stated category and status. Group B then applies the convention to every label in the class (or scopes the non-POD_SPEC labels out explicitly), reconciles EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN and its errorclassify entry, and aligns every carrier. The carriers include §13.1, §17.2 admission-policies item 3 (which uses POD_SPEC_HOST_SHARING_FORBIDDEN as a policy name), §18, pkg/podsecurity, the webhook, pkg/preflight, the Kyverno shareProcessNamespace chart policy, and the registry-digest guard comment. The §13.1 sentence saying the webhook "rejects any CR that would produce a pod spec" implies a CR or admin-API admission path that the pod webhook does not implement, and group B corrects that sentence. The analysis favors the webhook-internal-reason convention. A new deployment-model rejection from group C uses whatever label convention group B fixes.

**C. Embedded-model name keying.** The webhook adds every regular container named `adapter` or `runtime` to the credential-container set by name alone. The validator exempts that set from the runAsGroup clause and from the credential-volume and /run/lenny mount clauses of POD_SPEC_CRED_GROUP_OVERBROAD. 0088's reservation keys the same way. An embedded pod has a single regular container named `runtime`, so the name `adapter` is free. An injected regular container named `adapter` can then mount the credential volume and read the credential file and the manifest nonce. In a sidecar pod the name collides with the builder's adapter container and Kubernetes rejects the pod, so the gap is specific to the embedded model. The operative exemption is the volume-mount clause. The runAsGroup clause adds nothing, because fsGroup already makes every container a cred-readers member.

The pod object carries no deployment-model signal. The model exists only as a builder input (podspec Inputs). The builder stamps no deployment-model label or annotation, and the webhook reads only the pod object. A model-keyed rule therefore needs a chosen carrier, and group C must pick one of the following and specify it:

- A controller-stamped deployment-model label that the webhook reads. If it is a label, the lenny-label-immutability webhook (§17.2 item 5) must also protect it.
- A structural rule that needs no carrier, for example "a regular `adapter` container is credential-exempt only when it runs at the adapter UID beside a `runtime` container at the agent UID".
- An accepted residual stated in §13.1.

Only an actor that controls the agent pod's CREATE request can inject a regular container. That actor is the pod creator, a cluster mutating webhook, or a holder of pods/create RBAC granted beyond the chart. Such an actor already controls the runtime container's command, args, and env, which lenny-pod-security does not constrain. The fix therefore makes the stated §13.1 membership boundary true for embedded pods rather than closing an escalation path, and the proposal justifies it on that basis.

The embedded agent-socket question resolves inside group C as a bounded decision. The default is to keep the §4.7.11 posture ("the embedded model is a single trusted process with no adapter-agent socket boundary") with no peer check. The embedded platform MCP socket is the abstract socket `@lenny-platform-mcp`. Every container in the pod can connect to it, because abstract sockets live in the pod's network namespace. The manifest nonce on the credential volume is the only authenticator. Once group C closes the name-keying gap, only the runtime process and its same-UID subprocesses can read the nonce, and an own-UID SO_PEERCRED check would admit those subprocesses too. The repository owner already declined an own-UID embedded peer check in commit 6b94bc6e5. A runtime-UID peer check would add protection only against a principal the reservation fix already excludes. If the proposal adds one, it records the change as an optional code-level hardening and justifies reopening the owner's decision.

Out of scope: the CH-RUNTIMEOPS peer check and the code-comment hygiene items that 0088 also recorded, which are fixed on the Phase 0 hardening branch; the CH-MSGSOCK and CH-RUNTIMEOPS nonce, which the runtime-SDK proposal owns (gateway-runtime-comms-remediation.md §10.2); and anything 0088 stages. Keep each change minimal. Draft 0087 SPEC-6 inserts a paragraph immediately after the membership-boundary paragraph that group A edits, so the two proposals must coordinate the edit site.

## Evidence

- `verified`: proposals/0088_fix_agent-pods-have-no-enforced-rule-that-only-the, summary.md "Defects in the shipped tree that this proposal does not stage" lists all three items; spec-changes.md SPEC-1 **Container identity.**; non-spec-changes.md CODE-1 clause (b) keys the reservation on `Name` plus `credentialContainer[Name]`; status.md reads `status: Reviewed`.
- `verified`: pkg/admission/webhook/pod_security.go, the credential-container name constants (comment: "The sidecar deployment model emits both; the embedded model emits only \"runtime\"") and translatePodSpec, which appends any regular container named `adapter` or `runtime` to CredentialContainerNames; the decider denies with `Deny(http.StatusForbidden, err.Error())`.
- `verified`: pkg/podsecurity/podsecurity.go, ValidateAgentPod, where the runAsGroup clause and the credential-volume and /run/lenny mount clauses skip any name in `credentialContainer`, and the comment that fsGroup grants every container membership.
- `verified`: pkg/controller/sandbox/podspec/podspec.go, where DeploymentModel is a builder input only, buildEmbedded renders one regular container named `runtime` at the agent UID that mounts the credential volume, basePod stamps only `in.Labels`, and PlatformMCPSocketName is `@lenny-platform-mcp`. No `lenny.dev` deployment-model label exists in pkg/ or charts/.
- `verified`: spec/13_security-model.md §13.1, the control-table row "Capabilities | All dropped", the **`lenny-cred-readers` membership boundary.** paragraph with the setgroups clause (b) and "exactly two UIDs", and the host-sharing paragraph "rejects any CR that would produce a pod spec". setgroups appears nowhere else in spec/, docs/, pkg/, cmd/, or sdks/.
- `verified`: spec/04_system-components.md §4.7.11. The sentence "the embedded model is a single trusted process with no adapter-agent socket boundary" is in the nonce-only **Activation.** paragraph rather than item 1. Item 1 (Separate UIDs and connection authentication) describes the sidecar UID split only.
- `verified`: spec/15_external-api-surface.md §15.1 registers only EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN (PERMANENT, 422) and none of the POD_SPEC_* codes, IMAGE_SIGNATURE_INVALID, POD_IMAGE_DIGEST_REQUIRED, or T4_NODE_ISOLATION_VIOLATION. docs/reference/error-catalog.md also lists 422. pkg/admission/ephemeral_container_cred_guard/guard.go returns `Code: 403`.
- `verified`: further carriers of the codes, namely pkg/preflight (hostsharing.go, credfsgroup.go, podsecurity.go), charts/lenny/templates/admission-policies/kyverno-share-process-namespace-policy.yaml, pkg/admission/registry_digest/guard.go (labels described as the webhook's "machine-readable label for log and alert matching"), pkg/admission/cosign_verify/verify.go, pkg/admission/t4_node_isolation/guard.go, spec/17_deployment-topology.md §17.2 item 3, and spec/18_build-sequence.md. pkg/gateway/externalapi/errorclassify classifies only the ephemeral code.
- `verified with location qualifier`: EmbeddedPeerAuth (NoSocketBoundary, no peer check, manifest nonce only) exists in pkg/adapter/peercred.go, cmd/runtimes/echo-embedded, and cmd/runtimes/preconnect-echo on branch worktree-agent-a14863c1e63bb8ccf (commit 6b94bc6e5, whose message records the owner's decision against an own-UID embedded peer check). That branch is not an ancestor of HEAD.
- `verified`: charts/lenny/templates/controller-rbac.yaml grants pods create only to the controller, and ops-rbac.yaml grants no create on pods.

## Who observes it

- Group A: third-party runtime authors who read §13.1. No in-repo runtime or SDK calls setgroups or sets SysProcAttr, so no shipped behavior changes. A runtime that follows option (b) gets EPERM at spawn. If the runtime ignores that error, the subprocess keeps the credential group while the author believes it was dropped.
- Group B: operators and alerting that match admission denials, controller logs, events, and preflight output. No REST API client observes these codes. Spec readers see an inconsistent catalog, and an implementer who copies the EPHEMERAL precedent copies a wrong HTTP status.
- Group C: only an actor that controls agent-pod CREATE for an embedded pod. That actor already controls the full pod spec, including the runtime container's command and image.

## What breaks if nothing changes

- Group A: the spec mandates a mechanism that cannot work, which gives false assurance on a credential path.
- Group B: the error catalog and its precedent stay inconsistent with each other and with the code. Each new admission label repeats the ambiguity.
- Group C: the §13.1 membership boundary, and 0088's reservation once landed, do not hold for embedded pods as stated. A regular container named `adapter` in an embedded pod can mount the credential volume. No new escalation path opens, because the only actor able to exploit it already controls the pod spec.

## Findings this unblocks

None. Proposal 0088's unstaged-defects list is fully owned once this proposal lands. TEST-GAPS T-13.1.4 (OVERBROAD never exercised for injected containers) is adjacent and stays open, because group C states an accepted residual and adds no test for an embedded pod with an injected regular container named `adapter`.

## Prior art considered

- No landed or draft proposal other than 0089 stages any of the three changes. 0088 lists them as unstaged, and its review log defers the setgroups MUST as a separate finding. gateway-runtime-comms-remediation.md assigns exactly these three items to 0089 and the CH-RUNTIMEOPS peer check and hygiene items to the hardening branch.
- 0088's review log already established that no in-container group-drop mechanism (setgroups, unprivileged user namespaces, or prctl) works with every capability dropped. That analysis is direct prior art for group A.
- No open BUILD-GAPS or TEST-GAPS finding covers any of the three items. BUILD-GAPS F-13.1.23 and F-13.1.24 are 0088's findings. F-4.7.25 covers the CH-MSGSOCK peer check. F-4.7.28 and F-4.7.29 are closed on the hardening worktree. BUILD-PROGRESS records OVERBROAD as resolved for the sidecar case only.
- The embedded socket posture is already answered by spec text (§4.7.11 **Activation.**) and by hardening-branch code (EmbeddedPeerAuth, commit 6b94bc6e5). Adding a peer check would reverse both. It would not fill a silence.
- No existing mechanism lets lenny-pod-security distinguish embedded from sidecar pods. 0088's review log records that "the webhook cannot tell sidecar from embedded from the pod object". The §17.2 item 5 label-immutability webhook covers only managed, delivery-mode, egress-profile, and tenant-id.
- Draft 0087 SPEC-6 inserts a paragraph after the membership-boundary paragraph that group A edits.

## Validated premises

**premise lens (verdict: revise).**
- Confirmed: the OVERBROAD exemption keys only on regular-container name (load-bearing).
- Confirmed: 0088's reservation keys the same way. An embedded pod's free `adapter` name lets an injected container mount the credential volume. The harm is the credential read, and holding the adapter UID is not the source of it.
- Confirmed and added: the pod object carries no deployment-model signal, so any model-keyed rule needs a chosen carrier or a structural rule (load-bearing). The original statement omitted this.
- Confirmed: the embedded runtime binds the abstract socket `@lenny-platform-mcp`, which only the manifest nonce authenticates.
- Refuted as a separate mechanism: the socket half of item 1 reduces to the reservation fix. An own-UID peer check admits same-UID subprocesses and the owner declined it in 6b94bc6e5 (load-bearing).
- Confirmed with adjusted cause: setgroups fails because CAP_SETGID is missing, not because of allowPrivilegeEscalation, and it fails on Kata too.
- Confirmed: the POD_SPEC_* codes are unregistered and emitted only as 403 free-text substrings. The precedent is inconsistent (422 cataloged, 403 emitted). The class includes sibling labels.

**evidence lens (verdict: revise).**
- Verified: 0088's defect list, SPEC-1, and CODE-1 (b). 0088 is Reviewed, so "assume implemented" is a premise rather than a fact about the tree.
- Verified: name-only classification in the webhook, and no deployment-model signal on the pod (load-bearing).
- Drifted: the §4.7.11 quotation is in the **Activation.** paragraph, not item 1.
- Verified with location qualifier: EmbeddedPeerAuth exists only on branch worktree-agent-a14863c1e63bb8ccf.
- Drifted and corrected: "any container that can read the credential volume can reach them" conflates reaching the socket with authenticating to it. Every container can reach the abstract socket, and reading the volume yields the nonce.
- Verified: the setgroups sentence and the control table. allowPrivilegeEscalation false is not in §13.1.
- Verified and widened: the codes also appear in §17.2, §18, pkg/preflight, the Kyverno policy, and the registry-digest guard.
- Drifted: the EPHEMERAL precedent is 422 in the catalog and 403 in code.
- Verified: the out-of-scope references resolve.

**prior-art lens (verdict: stands).**
- No other proposal stages these changes, and no open finding covers them.
- The socket half is already answered by §4.7.11 and by hardening-branch code, so a peer check would be a reversal.
- No mechanism distinguishes embedded from sidecar pods at admission, and a label carrier would need label-immutability protection.
- The setgroups analysis exists in 0088's review log, and the blast radius is §13.1 alone. Draft 0087 SPEC-6 shares the edit site.
- The precedent is inconsistent, and the code carriers extend to preflight and Kyverno.

**scope lens (verdict: revise).**
- Item 2 is independent and spec-only.
- Item 1 holds two separable questions. The name-keying fix is small. The socket question is large and must be a bounded yes/no or an explicit deferral (load-bearing). Adopted: the default is to keep the no-peer-check posture.
- Item 3 is framed too narrowly. The decision covers the class of admission labels or scopes the siblings out explicitly.
- Items 1 and 3 are coupled through the label for any new rejection. Item 2 is uncoupled. Adopted: one proposal with three independently convergent groups, per the user's request.

**impact lens (verdict: revise).**
- Confirmed: the name-keying gap exists and is specific to embedded pods.
- Established: only an actor that controls pod CREATE can exploit it, and that actor already controls the runtime command. The fix makes the stated boundary hold and closes no escalation path.
- Refuted: the socket peer check protects against nothing the name fix leaves open (load-bearing).
- Confirmed: setgroups reaches third-party authors through the spec only and risks false assurance.
- Established: no REST client observes the admission codes. The class includes T4_NODE_ISOLATION_VIOLATION, and the EPHEMERAL precedent is the outlier with a wrong status (load-bearing).
- Noted: 0088 is not yet implemented.

**alternatives lens (verdict: revise).**
- Group A has no smaller alternative than deleting option (b). The "exactly two UIDs" claim contradicts the paragraph's own fsGroup text.
- Item 1 narrows to the volume-mount exemption. The runAsGroup half does not matter under fsGroup.
- Item 1 may reduce to an accepted residual or a controller-stamped label, because the only injectors already control the pod spec (load-bearing). Adopted as options for group C. A new embedded identity-reservation scheme is not required.
- An agent-UID-only peer check on the embedded MCP socket is a possible code-level hardening after 0088. Recorded as optional, against the owner's prior decision.
- Group B has a no-new-mechanism resolution (declare the labels webhook-internal). The §13.1 "rejects any CR" wording needs correction.
- Groups A and B do not depend on 0088. Only group C does.
