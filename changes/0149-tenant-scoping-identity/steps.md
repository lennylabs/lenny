# Steps: 0149 Tenant scoping and identity

## Steps

### 0149.S1 Environment scoping
- status: Planned
- aliases: [TS-1]
- lanes: [specification, code, documentation]
- depends-on: [0127.S1, 0127.S2, 0127.S3, 0127.S4, 0127.S9, 0149.S3, 0127.S5 (part), 0127.S6 (part), 0127.S8 (part), 0127.S10 (part)]

The fields, the formulas, enforcement in the gateway, the decision service, the Token Service, and the mirror, the refusal rules including the post-chain checks on the final row, the binding host validator, the delete guard for references, shadow policy evaluation, and the read filters.

It needs 0127.S1, 0127.S2, 0127.S3, 0127.S4, and 0127.S9 for the objects it scopes, the resolver, and the git inputs, and 0149.S3 for a dependable session environment. Its mirror steps need [0127.S5](../0127-egress-and-git/steps.md#steps) (EG-5) for pool selection and the fill request. Its route step needs [0127.S6](../0127-egress-and-git/steps.md#steps) (EG-6) for the upstreams routes name. Its read filters step needs [0127.S8](../0127-egress-and-git/steps.md#steps) (EG-8) and the envelope field in [0127.S10](../0127-egress-and-git/steps.md#steps) (EG-10) for the reads it extends.

Its post-chain step, shared with 0149.S3, depends on the interceptor contract proposal of [`0141-runtime-routing`](../0141-runtime-routing/steps.md#steps), 0141.S1, which lands [RT-2](../0141-runtime-routing/change.md#rt-2-the-interceptor-contract), and whichever of the two lands second moves the checks to the post-chain point, at which the environment rules run on the final row. Until 0141.S1 lands, the rules run after the experiment rewrite, as "A session's environment" under TS-1 states.

The guardrails of [SO-2](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture) (0142.S2) act on this step's writes through the single write path for tenant opt-ins that [0127.S9](../0127-egress-and-git/steps.md#steps) (EG-9) adds, and they can forbid an environment's use of an opt-in with no TS change. [UC-1](../0151-user-credential-delivery/change.md#uc-1-credential-types-and-the-registered-source) (0151.S1) and [UC-2](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking) (0151.S2) of the user credential design define types and link providers. This step adds their `environments` field when both have landed, and neither waits.

### 0149.S2 Identity model and platform boundary
- status: Planned
- aliases: [TS-2]
- lanes: [specification, code, documentation]
- closes: [F-10.2.38, F-10.2.39, F-10.2.40, F-13.3.27, F-15.1.74]

The specification statement, the broker contract, the deployment protected resource metadata route, the claim allowlist, RSA and token size bounds, the IdP role boundary, the operator tenant and the chart's refusal of `auth.platformAdminTenant: platform`, the playground-origin `subject_token` refusal, claim names, and the §15.1 listing of `/v1/admin/users`.

It has no dependency. It can land at any point, and it should land early because it closes a High defect. The claim allowlist lands early, because a stored format, a registered URI, or a shipped enforcement point would otherwise need rework.

### 0149.S3 Session environment context
- status: Planned
- aliases: [TS-3]
- lanes: [specification, code, documentation]
- depends-on: [0127.S10 (part)]
- closes: [F-8.2.34 (part), F-8.2.35, F-8.3.12, F-9.1.13, F-10.6.15, F-10.6.16, F-10.6.17, F-10.6.18, F-10.6.19, F-10.6.20, F-10.6.23, F-10.6.24]

Derive and delegation propagation, the session-based delegation gate, cross-environment sharing with its opt-ins, the check, no second crossing, both environments on records, the client delegation limits with one reading of `maxDepth` and a stored value on every root creator, depth from the parent's row, the calling-session parent rule, the Bridge allow-list, and `DELEGATION_DISABLED`, groups on exchanged tokens, the active-session delete guard, the transparent create check, and the naming correction. It closes F-8.2.34 apart from `delegationPolicyRef`. It closes F-10.6.17 with its rules running at the post-chain point of [RT-2](../0141-runtime-routing/change.md#rt-2-the-interceptor-contract).

Its evidence field step needs the envelope field in [0127.S10](../0127-egress-and-git/steps.md#steps) (EG-10) to put both environments on `credential.mint` and evidence. The rest of the step has no dependency and can land at any point.

Its guardrail case depends on [SO-2](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture) (0142.S2), whichever lands second adding the case, for `crossEnvironmentSharing: forbid` and the delay. Its lock case depends on [UC-5](../0151-user-credential-delivery/change.md#uc-5-delivery-file) (0151.S5), whichever lands second adding the case, because check 2 reads the lock UC-5 adds. Its post-chain step, shared with 0149.S1, depends on 0141.S1 in the same way, as 0149.S1 states.

### 0149.S4 User lifecycle path
- status: Planned
- aliases: [TS-4]
- lanes: [specification, code, documentation]
- closes: [F-11.4.19, F-11.4.20, F-11.4.21, F-10.6.21 (part), F-10.6.22 (part)]

Upsert on invalidate, holds per caller class, refusal at authentication including deleted rows, the impersonation cascade on disables, reinstate as the only reversal, ordering, the permission, and `identityProvider` hardening. It also closes the read half of F-10.6.21 and the address check of F-10.6.22.

It has no dependency. 0149.S2's contract refers to it, and either order works. Holds land early, because a stored format, a registered URI, or a shipped enforcement point would otherwise need rework.

[UG-2](../0152-user-git-identity/change.md#ug-2-repository-authorization) (0152.S2, repository authorization) and [UC-3](../0151-user-credential-delivery/change.md#uc-3-the-usercredential-binding) (0151.S3, the `userCredential` binding) read this step's invalidation level through `pkg/binding`, which returns it from [0127.S2](../0127-egress-and-git/steps.md#steps) (EG-2).

### 0149.S5 Delegated environment administration
- status: Planned
- aliases: [TS-5]
- lanes: [specification, code, documentation]
- depends-on: [0149.S1]

Environment `admin` writes to its own environment's scoped objects. Decision 7 defers it.

It needs 0149.S1 for the scoped fields.

### 0149.S6 Identity sources and the verifier registry
- status: Planned
- aliases: [TS-6]
- lanes: [specification, code, documentation]
- depends-on: [0149.S2, 0149.S4]

The operator tenant warning in preflight and posture, `identity.sharedIssuers` with the pin fields, selection after verification, and the domain pin's lapse, the record and its state machine, including `suspended` and reactivation, the activation rule and preflight, retirement with `tokens_not_before`, reactivation, `pending_version` and the approval digest, the deployment settings row, the client secret, the registry and active-source check, per-issuer caches, claim mapping, canonical subjects and the group form, per-source callbacks and the shared resolver, the pin and proofs, the address checker's use on issuers, and the Token Service's egress rules and chart checks.

It needs 0149.S2 for the role boundary and 0149.S4 for the shared address checker. Per-source callbacks, the deployment settings row, the group form, `suspended`, and reactivation land early, because a stored format, a registered URI, or a shipped enforcement point would otherwise need rework.

Its activation under a delay depends on [SO-1](../0142-security-oversight/change.md#so-1-security-roles) (0142.S1) and [SO-2](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture) (0142.S2), whichever lands second adding the case, for `security-admin` approval and the weakening delay. [`0142-security-oversight`](../0142-security-oversight/change.md) narrows the question of SO-1 (security roles): under `securityRoleSource: managed`, a `security-*` claim is ignored from any issuer. A customer source's `role_map` cannot name one, so `securityRoleSource: oidc` needs SO's own rule to read such a claim, which this step leaves to whichever lands second.

UC-2's start page uses this step's callback and resolver, so [UC-2](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking) (0151.S2) depends on this step's login path on every source, and a broker's URI is registered once.

### 0149.S7 Login routing
- status: Planned
- aliases: [TS-7]
- lanes: [specification, code, documentation]
- depends-on: [0149.S2, 0149.S6]

The `/t/{tenantId}` prefix, per-tenant metadata and challenges, the playground's per-tenant pages, chooser, cookie scope, and mint under the prefix, and email domain routing.

It needs 0149.S2 for the deployment route and 0149.S6 for the sources it names.

### 0149.S8 SCIM server
- status: Planned
- aliases: [TS-8]
- lanes: [specification, code, documentation]
- depends-on: [0149.S4, 0149.S6]

The surface, credentials and quotas, the retirement step that revokes credentials, subject matching, deprovisioning into TS-4, user rows, and the conformance corpus.

It needs 0149.S4 for invalidation levels and the permission, and 0149.S6 for source identifiers. Its retirement step also needs 0149.S6, because retirement names the credentials.

### 0149.S9 Directory groups
- status: Planned
- aliases: [TS-9]
- lanes: [specification, code, documentation]
- depends-on: [0149.S3, 0149.S8]

Group tables, `group_source`, the group reader ahead of `envaccess`, the access report's expansion, and erasure and classification.

It needs 0149.S3 for groups on exchanged tokens and 0149.S8 for directory data.

### Notes on the steps

Dependencies are stated in each step's fields and in the other design documents. Steps from different designs can interleave as long as these hold.

No proposal of the egress design (`EG-`), the forensics design (`FE-`), or the outbound connector design (`OC-`) depends on a TS step. The security oversight design's [SO-2](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture) (0142.S2) reports in posture the records that 0149.S3, 0149.S6, 0149.S8, and 0149.S9 add, which are sharing paths, identity sources, SCIM credentials, and directory groups, and those fields report `unknown` until the TS step that adds each record lands, as that design's dependency table states.

The hooks of this design sit inside the egress design's proposals [EG-1](../0127-egress-and-git/steps.md#steps) (0127.S1, git prerequisites), [EG-2](../0127-egress-and-git/steps.md#steps) (0127.S2, the pod network agent and proxied egress), [EG-5](../0127-egress-and-git/steps.md#steps) (0127.S5, the git mirror and warm fill), [EG-9](../0127-egress-and-git/steps.md#steps) (0127.S9, egress policy objects), and [EG-10](../0127-egress-and-git/steps.md#steps) (0127.S10, evidence ingest), so those steps do not wait.

Routes to an outbound connector under [OC-2](../0148-tenant-outbound-connector/steps.md#steps) (0148.S2, the connector's hosts and policy) can appear under `environmentPolicies` with no OC change.

## Validation owed

- **Slot attribution per runtime class (decision 31).** Measured. The owner asked on 2026-11-13 for a spike that measures which runtime classes answer slot attribution, which this item then gave as runc by design, Kata likely and unmeasured, and gVisor unknown. The AWS spike of 2026-11-14 measured it ([its report, "Test 1: per-slot attribution"](../0118-agent-pod-layout/evidence/attribution-and-quota-spike/RESULTS.md#test-1-per-slot-attribution)): runc answers `sock_diag`, gVisor with either network mode and Kata with either volume mode answer only `/proc/net/tcp`, and every answering reader gave each connection its own slot's UID, so no measured runtime class is a fallback pool, as [`0127-egress-and-git`, "Slot attribution and fetch grants"](../0127-egress-and-git/change.md#slot-attribution-and-fetch-grants) states from its revision 83 **[measured]**. User credential bindings, `lockEgressToHosts`, and a user git identity on a pod with several slots follow each pool's attribution status. What stays owed, the production `netagent` under a burst of connections and the managed runtimes of EKS, GKE Sandbox, and AKS, is listed under **Slot attribution** in that design's ["Validation still owed"](../0127-egress-and-git/steps.md#validation-still-owed).
- **Ref resolution after the post-chain point.** Confirm in the tree that the `kind: derive` and `kind: delegate` paths can resolve `gitClone` refs after the post-chain point of [RT-2](../0141-runtime-routing/change.md#rt-2-the-interceptor-contract) and before the pod claim. Today `POST /v1/sessions` and `POST /v1/sessions/start` resolve refs in `resolveSubmittedPlan` before the `PreRoute` chain and before the session row exists. [`0127-egress-and-git`, "Validation still owed"](../0127-egress-and-git/steps.md#validation-still-owed) carries the same item.
