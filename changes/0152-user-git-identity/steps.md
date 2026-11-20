# Steps: 0152 User-scoped repository access and pull request authoring

## Dependencies

| Other design | Interaction |
| :-- | :-- |
| [`0142-security-oversight`](../0142-security-oversight/change.md) | [SO-2](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture) gains the guardrail rows for `userAuthorization`, and [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) adds `requestRules.requiredPresets`, `tokenTrust.allowedRecords`, `userCredential.file` (the value of the `credentialDelivery.forbid` guardrail that forbids user credentials under `delivery: file`), and `allowRuntimeHeldCredentials`. [SO-4a](../0142-security-oversight/change.md#so-4a-mirror-to-the-tenant-chain)'s `impersonated_by` feeds "Identity cases". [SO-5b](../0142-security-oversight/change.md#so-5b-repository-write-approvals) and its [decision 25](../0142-security-oversight/decisions.md#decisions) apply unchanged, and it now states them for this design. |
| [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md) | [TS-1](../0149-tenant-scoping-identity/change.md#ts-1-environment-scoping) scopes `link_providers`, types, and `userCredential` bindings. [TS-4](../0149-tenant-scoping-identity/change.md#user-lifecycle-ts-4)'s upsert, which creates an unknown user's row in the invalidated state when that user is invalidated, and its refusal apply, and §11.4 gains the revocation step. It now cross-references this. |
| [`0148-tenant-outbound-connector`](../0148-tenant-outbound-connector/change.md) | [OR-3](../0148-tenant-outbound-connector/change.md#requirements) covers the Token Service's calls, and a VCS host behind an outbound connector follows [C6](../0148-tenant-outbound-connector/change.md#security-properties), [C8](../0148-tenant-outbound-connector/change.md#security-properties), [C9](../0148-tenant-outbound-connector/change.md#security-properties), and [C10](../0148-tenant-outbound-connector/change.md#security-properties). [`0129-github-credential-addon`](../0129-github-credential-addon/change.md) states the GHES case. |
| [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/change.md) | The `quarantined` binding state, which the forensics design sets on a pod under quarantine, stops child mints with no new work. [FE-5](../0128-forensics-evidence-preservation/change.md#fe-5-capture-runbook-and-abuse-workflow)'s "Rotate credentials" step gains removing a user's link, which revokes its grant, because revoking a child needs the token value and no route takes a hash. The forensics design's reconciler rule R-USER-CREDENTIAL-POD compares mints with the pod model. It now states these, with delivered tokens in evidence. |
| [`0145-session-isolation`](../0145-session-isolation/change.md), [`0153-workspace-materialization`](../0153-workspace-materialization/change.md), and [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) | SI-3's slot release, SI-10's shared pod state, and the per-slot scopes of the egress design's decision 60, with its union fallback, are the grounds of "Pod models" and decision 52. The helper of the materialization design runs every git step on a slot repository after the check at bind. TSS-6's `placement.allowSharedPod` and `placement.allowReusedPod` are tenant-wide settings beside the refusals here. |
| [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) | The mechanism this document configures. It owns the binding, the sources, the link flow, `delivery: file`, request rules, the preset format, and the add-on model. |
| [`0129-github-credential-addon`](../0129-github-credential-addon/change.md) | The GitHub add-on, whose [GH-1](../0129-github-credential-addon/change.md#gh-1-the-github-and-github-enterprise-kinds) to [GH-3](../0129-github-credential-addon/change.md#gh-3-the-github-deny-preset) depend on UG-1 to UG-3. |
| [`0130-gitlab-credential-addon`](../0130-gitlab-credential-addon/change.md) | The GitLab add-on, whose [GL-1](../0130-gitlab-credential-addon/change.md#gl-1-the-gitlab-kind) to [GL-3](../0130-gitlab-credential-addon/change.md#gl-3-the-gitlab-deny-preset) depend on UG-1 to UG-3. |

No proposal in the [egress design](../0127-egress-and-git/change.md) depends on one here.

## Steps

### 0152.S1 VCS link kinds
- status: Planned
- aliases: [UG-1]
- lanes: [specification, code, documentation]
- depends-on: [0151.S2, 0151.S7, 0127.S1, 0127.S2, 0127.S9]

The design is in [UG-1](change.md#ug-1-vcs-link-kinds). The dependencies cover the link flow and the kind registry, VCS pool hosts, and the single write path.

### 0152.S2 Repository authorization
- status: Planned
- aliases: [UG-2]
- lanes: [specification, code, documentation]
- depends-on: [0152.S1, 0151.S3, 0151.S7, 0127.S1, 0127.S2, 0127.S3, 0127.S5, 0149.S4, 0141.S1, 0151.S2, 0143.S1]

The design is in [UG-2](change.md#ug-2-repository-authorization). It needs the release check point of 0151.S7 (UC-7), 0127.S5 (EG-5) for mirror rules, and 0149.S4 (TS-4) for the lifecycle hook, the user invalidation level the mint checks read. It needs the interceptor contract proposal of [`0141-runtime-routing`](../0141-runtime-routing/steps.md#steps), step 0141.S1, for the post-chain point, and 0151.S2 (UC-2) for the combined missing-links response, which CA-4 defined before the removal of connectors withdrew it. The dependencies cover the session input check and `retireOnRelease`, the mint path, `pkg/binding`, the pod credential API, writable entries, the mirror, and the point and response at create.

It needs 0143.S1 (SA-6) because a `serviceIdentities` entry must name a subject in the `~svc~` namespace, which 0143.S1 introduces; landed first, the step would refuse every automated session on a VCS pool with `userAuthorization: required`. Whether 0143.S1 or 0149.S6 owns the resolver part of SA-6, and so which of them this edge names, is open decision 59, which mirrors `0143-service-account-credentials` open decision O9.

The step's pin wiring needs 0127.S12 (EG-12), which makes a VCS pool with `userAuthorization: required` a source of pins. The step does not wait for it, and the residual stands until then. Whichever of 0152.S2 and 0127.S12 lands second builds the wiring: the VCS pool as a pin source, `pinHosts`, the pin exception of "Other hosts", **Pinned hosts**, and the suspension a later pin applies there, with their tests.

The repository check on each tenant entry of a pool's warm list, the withholding of a store, and `withheldWarmEntries` need the warm list of 0147.S8 ([`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/steps.md#steps)). Whichever of 0152.S2 and 0147.S8 lands second wires the check onto the warm list, with its tests. The check on a derive runs at the post-chain point of the derive route that 0144.S1 ([`0144-session-derive`](../0144-session-derive/steps.md#steps)) builds; whichever of the two lands second calls the check there.

The `userAuthorization.require` and `userAuthorization.serviceIdentities` guardrails and the posture rows of this step land in 0142.S2 ([`0142-security-oversight`](../0142-security-oversight/steps.md#steps)), which depends on this step in part, rather than in this step.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-CreateSession`, whose refusals gain `USER_REPOSITORY_ACCESS_DENIED`, `USER_AUTHORIZATION_UNAVAILABLE`, and `USER_AUTHORIZATION_UNSUPPORTED_POD` for a body declaring a repository under a VCS pool with `userAuthorization: required`, and `GWSEAM-MaterializeChild` and `GWSEAM-CheckChildCredentials`, whose `returns` lists gain the refusals of a delegated child's repository check and of an unsupported pod. The step records this addition to decision 8's list in its digest, as the amendment of 2026-11-19 states.

### 0152.S3 Pull request authoring
- status: Planned
- aliases: [UG-3]
- lanes: [specification, code, documentation]
- depends-on: [0152.S2, 0151.S2, 0151.S3, 0151.S4, 0151.S5, 0151.S7, 0127.S3]

The design is in [UG-3](change.md#ug-3-pull-request-authoring). It needs 0127.S3 (EG-3) with git path T. The dependencies cover the binding, the template and preset mechanisms, the attach and host policy points, and `gh`'s remotes. Each add-on's authoring step, 0129.S2 ([GH-2](../0129-github-credential-addon/change.md#gh-2-pull-request-authoring-on-github)) and 0130.S2 ([GL-2](../0130-gitlab-credential-addon/change.md#gl-2-user-authored-merge-requests)), implements it for its provider.

It needs 0151.S5 (UC-5, file delivery), because the runtime-held attach, its `handling` value and mint refusal, `422 USER_CREDENTIAL_BINDING_SUSPENDED`, and the owner's rule on a binding with `delivery: file` beside a VCS pool all extend UC-5. The refusal at release of a binding on a VCS pool's API host whose flavour has no registered default preset lands here.

The refusal of a default clone's `write: always` for a repository under a VCS pool with `prAuthoring: user` needs the default clones of 0147.S7 ([`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/steps.md#steps)); whichever of 0152.S3 and 0147.S7 lands second adds the refusal and its test.

The `userAuthorization.forbidRuntimeHeld` guardrail and the posture rows of this step land in 0142.S2 ([`0142-security-oversight`](../0142-security-oversight/steps.md#steps)), which depends on this step in part, rather than in this step.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-MaterializeChild`, under which a delegated child receives the pool's `userCredential` binding only when the delegation lease names it. The step records this addition to decision 8's list in its digest, as the amendment of 2026-11-19 states.

### 0152.S4 Independent approval check
- status: Planned
- aliases: [UG-4]
- lanes: [specification, code, documentation]
- depends-on: [0151.S2, 0127.S3, 0127.S10]

The design is in [UG-4](change.md#ug-4-independent-approval-check). It needs 0151.S2 (UC-2) with a link provider `delivery` ceiling of `identifier`. The dependencies cover `git.operation` and `credential.mint`. The step is independent of 0152.S3. It builds the VCS pool's `approvalCheck` setting, and the validation of `pushIdentity: app` against it lands with whichever of 0152.S3 and 0152.S4 lands second. The webhook route waits on decision 56, and the handling of a pushing session whose user has no link waits on decision 55.

## Validation owed

The spikes against GitHub and GitLab, and the stub capture, are under "Validation owed" in [`0129-github-credential-addon`](../0129-github-credential-addon/steps.md#validation-owed) and [`0130-gitlab-credential-addon`](../0130-gitlab-credential-addon/steps.md#validation-owed), which keep their S-numbers. This layer owes the git path and the runtime-held token rows.

| Spike | Question | Decides | State |
| :-- | :-- | :-- | :-- |
| S18 | The git path: candidates L, T, and P, which "Git remotes and client tools" defines | UR-5 | **Closed on the stub [measured], and T is settled**, as "Git remotes and client tools" lists. Not run: macOS, Windows, amd64, the .NET SDK image, a `glab` fork flow, read after push, git against `ghe.com` beyond the stub, a T and P hybrid, and a decision service, which ran as a stand-in. |
| V4 | The git path T column of "Git and hosts", and `allowRuntimeHeldCredentials` | The git path | Column **[measured]**, on a stub. The runtime-held attach and each 401 state are **[measured]**. A decision service, the fall-through between a tenant's route lists, and the Token Service's assertion of the expected route on its CONNECT have not run, and `netagent`'s route assertion ran on a stand-in. A pin exception, a guardrail, and posture have no prototype. |
| V5 | Whether `netagent` attributes each loopback connection to its slot by the connecting socket's owner, through `sock_diag` or `/proc/net/tcp`, on runc with user namespaces, on gVisor, and in Kata's guest, as the egress design's decision 60 relies on | On which runtime classes the `sessionPolicy.maxConcurrentSessionsPerPod > 1` refusal lifts under decision 52 | Measured on 2026-11-14. The owner asked on 2026-11-13 for a spike to measure it ("run a spike to test this"), and the AWS spike of 2026-11-14 found that runc answers `sock_diag` and gVisor and Kata answer only `/proc/net/tcp`, with each connection's own slot UID from every answering reader, under user namespaces as well ([its report, "Test 1: per-slot attribution"](../0118-agent-pod-layout/evidence/attribution-and-quota-spike/RESULTS.md#test-1-per-slot-attribution); [`0127-egress-and-git`, "Slot attribution and fetch grants"](../0127-egress-and-git/change.md#slot-attribution-and-fetch-grants), revision 83) **[measured]**. The production `netagent` under a burst and the managed runtimes stay owed by the egress design. |
