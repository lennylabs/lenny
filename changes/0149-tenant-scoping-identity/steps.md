# Steps: 0149 Tenant scoping and identity

## Steps

### 0149.S1 Environment scoping
- status: Planned
- aliases: [TS-1]
- lanes: [specification, code, documentation]
- depends-on: [0127.S1, 0127.S2, 0127.S3, 0127.S4, 0127.S9, 0149.S3, 0127.S5 (part), 0127.S6 (part), 0127.S8 (part), 0127.S10 (part)]

The fields, the formulas, enforcement in the gateway, the decision service, the Token Service, and the mirror, the refusal rules including the post-chain checks on the final row, the binding host validator, the delete guard for references, shadow policy evaluation, and the read filters.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step reshapes no gateway seam operation. TS-1's scoping is enforced in the decision service, the Token Service, and the mirror, outside the seam, and `GWSEAM-ResolveEnvironments`, which resolves the caller's environment membership, changes in 0149.S3.

It needs 0127.S1, 0127.S2, 0127.S3, 0127.S4, and 0127.S9 for the objects it scopes, the resolver, and the git inputs, and 0149.S3 for a dependable session environment. Its mirror steps need [0127.S5](../0127-egress-and-git/steps.md#steps) (EG-5) for pool selection and the fill request. Its route step needs [0127.S6](../0127-egress-and-git/steps.md#steps) (EG-6) for the upstreams routes name. Its read filters step needs [0127.S8](../0127-egress-and-git/steps.md#steps) (EG-8) and the envelope field in [0127.S10](../0127-egress-and-git/steps.md#steps) (EG-10) for the reads it extends.

Its post-chain step, shared with 0149.S3, depends on the interceptor contract proposal of [`0141-runtime-routing`](../0141-runtime-routing/steps.md#steps), 0141.S1, which lands [RT-2](../0141-runtime-routing/change.md#rt-2-the-interceptor-contract), and whichever of the two lands second moves the checks to the post-chain point, at which the environment rules run on the final row. Until 0141.S1 lands, the rules run after the experiment rewrite, as "A session's environment" under TS-1 states.

The guardrails of [SO-2](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture) (0142.S2) act on this step's writes through the single write path for tenant opt-ins that [0127.S9](../0127-egress-and-git/steps.md#steps) (EG-9) adds, and they can forbid an environment's use of an opt-in with no TS change. [UC-1](../0151-user-credential-delivery/change.md#uc-1-credential-types-and-the-registered-source) (0151.S1) and [UC-2](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking) (0151.S2) of the user credential design define types and link providers. This step adds their `environments` field when both have landed, and neither waits.

Five mechanisms that "TS-1" describes land in other steps, and this step builds only the environment fields and rules they read:

- The warm-list environment predicate at write and at render, with the `environmentScoped` suspension, is built by the step that owns each declarer's entries, and this step builds the pool `environments` field the predicate reads. For tenant entries, [0147.S8](../0147-tenant-derived-runtimes/steps.md#steps) owns the write check and the render suspension, and whichever of this step and 0147.S8 lands second adds the environment case. For platform-declared entries, [0127.S5](../0127-egress-and-git/steps.md#steps) owns them, and whichever of this step and 0127.S5 lands second adds the environment case. This step does not build the write check or the render suspension itself (tenancy-security-C11).
- `placementRequirements` on the environment record, its relaxation under the weakening delay, and its join at the post-chain point land in [0136.S6](../0136-placement/steps.md#steps) (PM-4), which depends on this step and on 0142.S2 for the applier. This step adds no placement field.
- The refusal of a `gitRepository` asset write on a tenant-derived runtime with `422 INVALID_DERIVED_RUNTIME` lands with [0147.S4](../0147-tenant-derived-runtimes/steps.md#steps) and 0147.S8.
- The checks on a derive's `targetPool` land with whichever of this step and [0144.S1](../0144-session-derive/steps.md#steps) lands second.
- The refusal of `egressLockHosts` on a pod with no per-session policy reads the lock that [0151.S5](../0151-user-credential-delivery/steps.md#steps) (UC-5) adds, and whichever of this step and 0151.S5 lands second adds that case.

### 0149.S2 Identity model and platform boundary
- status: Planned
- aliases: [TS-2]
- lanes: [specification, code, documentation]
- closes: [F-10.2.38, F-10.2.39 (part), F-10.2.40, F-10.2.41, F-10.2.42, F-13.3.27, F-15.1.74, ADMIAM-I1]

The specification statement, the broker contract, the deployment protected resource metadata route, the claim allowlist, RSA and token size bounds, the IdP role boundary, the operator tenant and the chart's refusal of `auth.platformAdminTenant: platform`, the playground-origin `subject_token` refusal, claim names, and the §15.1 listing of `/v1/admin/users`. The role boundary covers every platform role, `platform-admin` and the `platform-forensics` role of [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/change.md), which 0128.S1 waits on. It closes the deployment metadata and challenge part of F-10.2.39, and [0138.S2](../0138-rest-only-client-api/steps.md#steps) closes the rest with the adapters' computed identifiers. Its identity page shows how a tenant role is granted, which closes ADMIAM-I1.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-RequestCaller`, the caller identity the API front answers for the platform tools seam, with the identity model.

It has no dependency. It can land at any point, and it should land early because it closes a High defect. The claim allowlist lands early, because a stored format, a registered URI, or a shipped enforcement point would otherwise need rework.

### 0149.S3 Session environment context
- status: Planned
- aliases: [TS-3]
- lanes: [specification, code, documentation]
- closes: [F-8.2.35, F-9.1.13, F-10.6.15, F-10.6.16, F-10.6.17, F-10.6.18, F-10.6.19, F-10.6.20, F-10.6.23]

Derive and delegation propagation of the session's environment, the session-based delegation gate and its fail-closed reads, which [`0166-delegation-primitives`](../0166-delegation-primitives/change.md#dp-1-session-grant) turns into the environment caveat of the session grant, the calling-session parent rule for every platform operation of 0166, the Bridge allow-list, which admits those operations, an environment's `defaultDelegationPolicy` applied at every `session.create` and its `delegationCeilings`, groups on exchanged tokens, the active-session delete guard, the transparent create check, and the naming correction. It closes F-10.6.17 with its rules running at the post-chain point of [RT-2](../0141-runtime-routing/change.md#rt-2-the-interceptor-contract). What the gate reads for a session with E = ∅ is open as decision 32, and its answer decides whether this step closes F-10.6.16 in full. The client delegation limits of TS-3 are 0149.S10, and cross-environment sharing is 0149.S11.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-ResolveEnvironments` with the session-based delegation gate.

It has no dependency and can land at any point. Its post-chain step, shared with 0149.S1, depends on 0141.S1 in the same way, as 0149.S1 states. 0166.S1 depends on this step for the session's stored environment. The rules that act on 0166's operations rather than on today's delegation tools, and the environment's delegation inputs, which feed 0166's resolution, land with whichever of this step and 0166.S1 lands second; until 0166.S1 lands, this step applies the gate and the parent rule to today's delegation tools.

### 0149.S4 User lifecycle path
- status: Planned
- aliases: [TS-4]
- lanes: [specification, code, documentation]
- depends-on: [0150.S1 (part)]
- closes: [F-11.4.19, F-11.4.20, F-11.4.21, F-10.6.21 (part), F-10.6.22 (part)]

Upsert on invalidate, holds per caller class, refusal at authentication including deleted rows, the impersonation cascade on disables, reinstate as the only reversal, ordering, the permission, and `identityProvider` hardening. It also closes the read half of F-10.6.21 and the address check of F-10.6.22.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-DelegatorRefusal`, the creation fence's refusal of a delegated child that the platform tools seam reads, and `GWSEAM-AdmitActiveCaller`, the user lifecycle check the platform tools seam's tool-use and elicitation resolutions apply, with the holds per caller class and the refusal at authentication.

It has no dependency apart from the part that writes the class of the upserted user row into §12.9, which waits on 0150.S1 as "Notes on the steps" states. 0149.S2's contract refers to it, and either order works. Holds land early, because a stored format, a registered URI, or a shipped enforcement point would otherwise need rework.

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
- depends-on: [0149.S2, 0149.S4, 0150.S1 (part)]
- closes: [F-13.2.25 (part), F-13.2.30, F-27.3.17]

The operator tenant warning in preflight and posture, `identity.sharedIssuers` with the pin fields, selection after verification, and the domain pin's lapse, the record and its state machine, including `suspended` and reactivation, the activation rule and preflight, retirement with `tokens_not_before`, reactivation, `pending_version` and the approval digest, the deployment settings row, the client secret, the registry and active-source check, per-issuer caches, claim mapping, canonical subjects and the group form, per-source callbacks and the shared resolver, the pin and proofs, the address checker's use on issuers, and the Token Service's egress rules and chart checks. It closes the Token Service's issuer egress part of F-13.2.25, F-13.2.30 through `lenny.kmsEgressRule`, and F-27.3.17 with the per-source callbacks. Whether this step splits is open as decision 40.

It needs 0149.S2 for the role boundary and 0149.S4 for the shared address checker. Per-source callbacks, the deployment settings row, the group form, `suspended`, and reactivation land early, because a stored format, a registered URI, or a shipped enforcement point would otherwise need rework.

Its activation under a delay depends on [SO-1](../0142-security-oversight/change.md#so-1-security-roles) (0142.S1) and [SO-2](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture) (0142.S2), whichever lands second adding the case, for `security-admin` approval and the weakening delay. [`0142-security-oversight`](../0142-security-oversight/change.md) narrows the question of SO-1 (security roles): under `securityRoleSource: managed`, a `security-*` claim is ignored from any issuer. A customer source's `role_map` cannot name one, so `securityRoleSource: oidc` needs SO's own rule to read such a claim, which this step leaves to whichever lands second. [`0142-security-oversight`](../0142-security-oversight/steps.md#steps) lands the security-principal write routes of SO-1 with 0142.S2, which adds the delay they take, so no tenant holds a principal row before the delay exists. The refusal that follows stays as a guard for the order in which this step lands after 0142.S1 and before 0142.S2, and it holds if a later edit to 0142 moves the write routes (tenancy-security-C10). Until 0142.S2 lands, this step refuses activation and a change to `deploymentIssuerAllowed` at a tenant with any security principal row, in any state, and leaves retirement available, and 0142.S2 lifts the refusal by adding the delay.

UC-2's start page uses this step's callback and resolver, so [UC-2](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking) (0151.S2) depends on this step's login path on every source, and a broker's URI is registered once.

### 0149.S7 Login routing
- status: Planned
- aliases: [TS-7]
- lanes: [specification, code, documentation]
- depends-on: [0149.S2, 0149.S6]

The `/t/{tenantId}` prefix, per-tenant metadata and challenges, the playground's per-tenant pages, chooser, cookie scope, and mint under the prefix, and email domain routing.

It needs 0149.S2 for the deployment route and 0149.S6 for the sources it names. It also closes the playground cookie-path finding that `BUILD-GAPS.md` files as F-27.3.18 (High). Another finding, on the playground error page's status, carries the same identifier, so the identifier enters `closes` once one of the two is renumbered.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. The `/t/{tenantId}` prefix changes the session paths that "Route registration" lists for the API front's request pipeline and that `GWSEAM-HandleRoute` mounts behind it, and `GWSEAM-RequestTenant` returns a tenant that must equal the path's, with `403 TENANT_PATH_MISMATCH` otherwise.

### 0149.S8 SCIM server
- status: Planned
- aliases: [TS-8]
- lanes: [specification, code, documentation]
- depends-on: [0149.S4, 0149.S6, 0150.S1 (part)]

The surface, credentials and quotas, the retirement step that revokes credentials, subject matching, deprovisioning into TS-4, user rows, and the conformance corpus.

It needs 0149.S4 for invalidation levels and the permission, and 0149.S6 for source identifiers. Its retirement step also needs 0149.S6, because retirement names the credentials.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step reshapes no gateway seam operation. It registers the SCIM routes through `GWSEAM-RegisterRoutes` with the existing access check under which the API front applies no check and the handler authenticates the request.

### 0149.S9 Directory groups
- status: Planned
- aliases: [TS-9]
- lanes: [specification, code, documentation]
- depends-on: [0149.S3, 0149.S8, 0150.S1 (part)]

Group tables, `group_source`, the group reader ahead of `envaccess` (`pkg/tenancy/envaccess`), the access report's expansion, and erasure and classification.

It needs 0149.S3 for groups on exchanged tokens and 0149.S8 for directory data.

### 0149.S10 Client delegation limits
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: [0149.S3]
- closes: [F-8.2.34 (part), F-8.3.12]

The client delegation limits of decision 26 under TS-3: one reading of `maxDepth`, a stored value on every root creator, the client layer of the resolution that [`0166-delegation-primitives`, "DP-1"](../0166-delegation-primitives/change.md#dp-1-session-grant) owns (0166 decision 40), with 0 from a client alone, depth from the parent's row, `gateway.delegation.defaultMaxDepth` as a chart value that replaces `deploymentAudit.defaultMaxDepth`, `DELEGATION_DISABLED`, and hiding the tool. It closes F-8.2.34 apart from `delegationPolicyRef`. Until [0138.S6](../0138-rest-only-client-api/steps.md#steps) removes them, `lenny/create_and_start_session` and `BindSingleShot` write the resolved value as `POST /v1/sessions/start` does, and 0138.S6 removes that wiring with the paths. [0126.S1](../0126-delegation-availability/steps.md#steps) builds on this step: it adds the explicit stored `maxChildrenTotal: 0` of that design's D4, the derive narrowing of its O5, and `details.disabledBy`. 0166.S1 depends on this step for a client's `maxDepth: 0`, and wires the levels of the resolution other than the client's.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-CreateSession`, through which the platform tools seam's session tools create a session from a client API body, so that the session stores the resolved lease.

It needs 0149.S3 for the calling-session parent rule and the allow-list, which the refusal and the list filter build on.

### 0149.S11 Cross-environment sharing
- status: Planned
- lanes: [specification, code, documentation]
- depends-on: [0149.S3, 0149.S10, 0127.S10 (part)]
- closes: [F-10.6.24]

Cross-environment sharing of decision 14 under TS-3: the tenant setting and `dangerouslyShare` with its write rules, the check, no second crossing with `sessions.crossed_from_environment`, both environments on records, the guardrails, and the rule for messages between environments. Under decision 41 of [`0166-delegation-primitives`](../0166-delegation-primitives/decisions.md#decisions) it also carries crossing for every operation of 0166: what a crossing child receives from T, files under both content caveats, the list a parent's watch and read of a crossed child carry, the refusal of parent tools, and the admission check on a release, with decision 43 for a transfer. The parts that act on 0166's operations land with whichever of this step and 0166.S1 lands second, the read filter with 0135's MD-25 step, and the refusal of parent tools and the `materialize` path with 0166.S3.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-ResolveEnvironments` with cross-environment sharing.

It needs 0149.S3 for the session-based gate and child propagation, and 0149.S10 for check 1, which reads the stored lease. Its evidence field step needs the envelope field in [0127.S10](../0127-egress-and-git/steps.md#steps) (EG-10) to put both environments on `credential.mint` and evidence.

Its guardrail case depends on [SO-2](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture) (0142.S2), whichever lands second adding the case, for `crossEnvironmentSharing: forbid` and the delay. Its lock case depends on [UC-5](../0151-user-credential-delivery/change.md#uc-5-delivery-file) (0151.S5), whichever lands second adding the case, because check 2 reads the lock UC-5 adds. Its post-chain evaluation, which selects T, runs at the post-chain point once 0141.S1 lands, as 0149.S1 states.

### 0149.S12 Label rules
- status: Planned
- aliases: [TS-10]
- lanes: [specification, code, documentation]
- depends-on: [0149.S1, 0149.S9]

The label rules of "Label rules (TS-10)" (decision 45): the tenant's rules and their route, an environment's `labelRules`, `labels.reservedPrefixes`, `values`, `allow`, `valueFrom`, and `required`, the check at a root's creation and at a derive, `403 LABEL_NOT_PERMITTED`, the limits, and the audit events. It needs 0149.S1 for the environment resource and 0149.S9 for the directory groups that `allow` and `valueFrom` read. 0170.S1 depends on its part that tells a governed key from a free one.

### Notes on the steps

Dependencies are stated in each step's fields and in the other design documents. Steps from different designs can interleave as long as these hold.

No proposal of the egress design (`EG-`), the forensics design (`FE-`), or the outbound connector design (`OC-`) depends on a TS step. The security oversight design's [SO-2](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture) (0142.S2) reports in posture the records that 0149.S11, 0149.S6, 0149.S8, and 0149.S9 add, which are sharing paths, identity sources, SCIM credentials, and directory groups, and those fields report `unknown` until the TS step that adds each record lands, as that design's dependency table states.

0149.S4, 0149.S6, 0149.S8, and 0149.S9 write data classes into §12.9: the upserted user row, the source record and the stored client secret, the SCIM credentials and directory users, and the groups and memberships. Each depends on [0150.S1](../0150-tenant-security-settings/steps.md#steps) (part), which renames the tiers to the class names Public, Internal, Confidential, and Secret, so the part of each step that edits §12.9 uses those names (0150-F21).

No step of this change lists 0142.S2 under `depends-on`, and its steps name 0142.S2 in their text alone. Whether 0142.S2 splits is open as [`0142-security-oversight` decision 41](../0142-security-oversight/decisions.md). If the owner chooses its option (a), these references move to the step that carries what each reads: the guardrail cases of 0149.S1 and 0149.S11 to the guardrail step, the posture fields named above to the posture step, and the activation delay of 0149.S6 to the step that carries the delayed principal changes and the security-principal write routes.

The hooks of this design sit inside the egress design's proposals [EG-1](../0127-egress-and-git/steps.md#steps) (0127.S1, git prerequisites), [EG-2](../0127-egress-and-git/steps.md#steps) (0127.S2, the pod network agent and proxied egress), [EG-5](../0127-egress-and-git/steps.md#steps) (0127.S5, the git mirror and warm fill), [EG-9](../0127-egress-and-git/steps.md#steps) (0127.S9, egress policy objects), and [EG-10](../0127-egress-and-git/steps.md#steps) (0127.S10, evidence ingest), so those steps do not wait.

Routes to an outbound connector under [OC-2](../0148-tenant-outbound-connector/steps.md#steps) (0148.S2, the connector's hosts and policy) can appear under `environmentPolicies` with no OC change.

## Validation owed

- **Slot attribution per runtime class (decision 31).** Measured. The owner asked on 2026-11-13 for a spike that measures which runtime classes answer slot attribution, which this item then gave as runc by design, Kata likely and unmeasured, and gVisor unknown. The AWS spike of 2026-11-14 measured it ([its report, "Test 1: per-slot attribution"](../0118-agent-pod-layout/evidence/attribution-and-quota-spike/RESULTS.md#test-1-per-slot-attribution)): runc answers `sock_diag`, gVisor with either network mode and Kata with either volume mode answer only `/proc/net/tcp`, and every answering reader gave each connection its own slot's UID, so no measured runtime class is a fallback pool, as [`0127-egress-and-git`, "Slot attribution and fetch grants"](../0127-egress-and-git/change.md#slot-attribution-and-fetch-grants) states from its revision 83 **[measured]**. User credential bindings, `lockEgressToHosts`, and a user git identity on a pod with several slots follow each pool's attribution status. What stays owed, the production `netagent` under a burst of connections and the managed runtimes of EKS, GKE Sandbox, and AKS, is listed under **Slot attribution** in that design's ["Validation still owed"](../0127-egress-and-git/steps.md#validation-still-owed).
- **Ref resolution after the post-chain point.** 0149.S1 and [0141.S1](../0141-runtime-routing/steps.md#steps) wait on it. Confirm in the tree that the `kind: derive` and `kind: delegate` paths can resolve `gitClone` refs after the post-chain point of [RT-2](../0141-runtime-routing/change.md#rt-2-the-interceptor-contract) and before the pod claim. Today `POST /v1/sessions` and `POST /v1/sessions/start` resolve refs in `resolveSubmittedPlan` before the `PreRoute` chain and before the session row exists. [`0127-egress-and-git`, "Validation still owed"](../0127-egress-and-git/steps.md#validation-still-owed) carries the same item.
