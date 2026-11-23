# Steps: 0142 Security oversight for customers and the operator

## Steps

The outline's items keep their labels. SO-4 and SO-5 each split, because their parts have different dependencies.

### 0142.S1 Security roles
- status: Planned
- aliases: [SO-1]
- lanes: [specification, code, documentation]
- depends-on: [0142.S4, 0127.S8 (part), 0127.S9 (part), 0150.S1 (part)]
- closes: [F-10.6.22 (part)]

The security roles, `tenant_security_principals`, the resolver rules in the gateway and `lenny-ops`, reserved role names, protected subjects, the introspection exemption, `audit.read` and `audit.recover`, the `lenny-ops` ceiling, security-owned subscriptions, and the audit catalog review. It closes the part of F-10.6.22 that a security principal's bearer covers, and [TS-4](../0149-tenant-scoping-identity/change.md#user-lifecycle-ts-4) (0149.S4) closes the address check.

It depends on 0142.S4, because the resolver refuses a security role to a bearer carrying `impersonated_by`, which 0142.S4 adds to the impersonation bearer. Without that claim an impersonation bearer is a `user_bearer` of the target subject with a fresh `iat`, so it would hold the principal's security roles. The resolver, `ResolveRoles(ctx, tenantID, subject)` today, is widened to read `typ`, `iat`, and `impersonated_by`. The security-principal write routes of SO-1 (`PUT`, `DELETE`, and the `cancel` and `object` routes) land with 0142.S2, which adds the weakening delay they take, so no route writes a principal row before the delay exists.

Decision 27 refuses a `manage_user_credentials` write that names a security principal. Its refusal on the admin write route of [UC-1](../0151-user-credential-delivery/change.md#uc-1-credential-types-and-the-registered-source) (0151.S1) lands with whichever of this step and 0151.S1 lands second.

The evidence grants need [EG-8](../0127-egress-and-git/steps.md#steps) and [EG-9](../0127-egress-and-git/steps.md#steps), for the named permissions on the new reads. The role and audit reads can land first, and the grants follow as one change.

[FE-6](../0128-forensics-evidence-preservation/change.md#fe-6-agent-activity-streams) of [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/change.md) does not depend on this step: `tenant-admin` holds its read permissions until this step lands, and neither waits for the other, as that design says. Decision 19 states the auditor's grants.

### 0142.S2 Tenant security record and posture
- status: Planned
- aliases: [SO-2]
- lanes: [specification, code, documentation]
- depends-on: [0142.S1, 0127.S2, 0127.S4, 0127.S9, 0151.S1 (part), 0151.S2 (part), 0151.S4 (part), 0151.S5 (part), 0152.S2 (part), 0152.S3 (part), 0150.S1 (part)]

The tenant security record, its applier for both this record and the tenant security settings, the security-principal write routes of SO-1 with their delay, the `identityProvider` write rule, the guardrail check, suspension, and the posture read with its `securitySettings` and `llm` sections. Whether this step splits is open decision 41.

The record accepts the `approvals.*` and `operatorAccess.*` members from this step on, and 0142.S5, 0142.S6, and 0142.S7 enforce them. The `approvals.repositoryWrites` row of the tightening table lands with 0142.S7, which adds the store's `repository_write` kind and the entry states.

It needs the write path, the suspended state, and the role that writes the record. [EG-9](../0127-egress-and-git/steps.md#steps), [EG-2](../0127-egress-and-git/steps.md#steps), and [EG-4](../0127-egress-and-git/steps.md#steps) carry the requested fields. The posture sections on egress need [EG-2](../0127-egress-and-git/steps.md#steps) and [EG-9](../0127-egress-and-git/steps.md#steps) and otherwise report `unknown`. The `placement` section, the posture's placement fields, and `placementConfigured` land with the placement requirements of [PM-4](../0136-placement/change.md#pm-4-placement-requirements) (0136.S6) and the offerings of [PM-3](../0136-placement/change.md#pm-3-placement-offerings-and-per-pool-nodeplacement) (0136.S5), because the section feeds PM-4's filter. Those steps land after this one, because 0136.S5 and 0136.S6 depend on this step.

Some record fields need steps of the user credential and user git identity changes. `identityBinding.forbidUnverified` needs [UC-2](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking), `userAuthorization` needs [UG-2](../0152-user-git-identity/change.md#ug-2-repository-authorization), with `.forbidRuntimeHeld` needing [UG-3](../0152-user-git-identity/change.md#ug-3-pull-request-authoring) and the [UC-5](../0151-user-credential-delivery/change.md#uc-5-delivery-file) it builds on, `tokenTrust` needs [UC-1](../0151-user-credential-delivery/change.md#uc-1-credential-types-and-the-registered-source) and, for link providers, [UC-2](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking), `requestRules` needs [UC-4](../0151-user-credential-delivery/change.md#uc-4-request-rules-and-presets), and `userCredential.file` needs [UC-5](../0151-user-credential-delivery/change.md#uc-5-delivery-file). Each of these fields lands with this step or with the step it needs, whichever is later. The fields of [UC-1](../0151-user-credential-delivery/change.md#uc-1-credential-types-and-the-registered-source), [UC-4](../0151-user-credential-delivery/change.md#uc-4-request-rules-and-presets), and [UC-5](../0151-user-credential-delivery/change.md#uc-5-delivery-file) pass the guardrail check through [EG-9](../0127-egress-and-git/steps.md#steps), and the guardrails named under "User credentials and request rules" land with this step or with the UC step, whichever is later. The same holds for `userAuthorization` and `allowRuntimeHeldCredentials` with [UG-2](../0152-user-git-identity/change.md#ug-2-repository-authorization) and [UG-3](../0152-user-git-identity/change.md#ug-3-pull-request-authoring).

The posture fields for tenant scoping need [TS-3](../0149-tenant-scoping-identity/change.md#ts-3-session-environment-context-and-cross-environment-sharing), [TS-6](../0149-tenant-scoping-identity/change.md#identity-source-record-ts-6), [TS-8](../0149-tenant-scoping-identity/change.md#scim-server-ts-8), and [TS-9](../0149-tenant-scoping-identity/change.md#directory-groups-ts-9), each for its own fields: sharing paths, identity sources and their issuers, SCIM credentials and directory rows, and directory groups. Until the TS step that adds a record lands, the fields that read it report `unknown`.

The posture `interceptors` section needs the `tenants` list on an interceptor registration from [RT-6](../0141-runtime-routing/change.md#rt-6-tenant-scoped-interceptors), and whichever of this step and RT-6 lands second adds the case. The applier for the settings and the `securitySettings` posture section need the settings model and admin API of [`0150-tenant-security-settings`](../0150-tenant-security-settings/steps.md#steps) (0150.S2), for the version table the applier activates and the view the posture section repeats. Whichever of this step and 0150.S2 lands second adds the case. 0150.S2 depends on 0142.S1 (part) and 0142.S4 rather than on this step, and the relaxation rule of the settings lands with 0150.S4, which depends on this step.

The following steps do not depend on this step, and whichever lands second adds the case:

- `crossEnvironmentSharing` acts on [TS-3](../0149-tenant-scoping-identity/change.md#ts-3-session-environment-context-and-cross-environment-sharing)'s setting and declarations.
- `lifecycleSubjects` lets a worker of [TS-4](../0149-tenant-scoping-identity/change.md#user-lifecycle-ts-4) or a SCIM credential of [TS-8](../0149-tenant-scoping-identity/change.md#scim-server-ts-8) act on a protected subject. Whether the list can name a SCIM source is open decision 36.
- A connector registration of [OC-1](../0148-tenant-outbound-connector/steps.md#steps) (0148.S1) and an outbound host registration of [OC-2](../0148-tenant-outbound-connector/steps.md#steps) (0148.S2) each pass the guardrail check as a tenant-registered service. Where the outbound grant that the posture `gates` section reads is stored is open decision 44.
- The `llm` posture section reads the upstreams of [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/steps.md#steps) (0133.S2), and whichever of this step and 0133.S2 lands second adds it. Its sharing item waits for open decision 38.

This step adds the record member `"delegation": "forbid"` of [`0126-delegation-availability`](../0126-delegation-availability/change.md) (D5) with its classification, and 0126.S1, which depends on this step in part, adds the guardrail row of its resolution with or after it. It also adds the members `parentTools`, `offerTools`, and `delegationLeave` of decision 37 of [`0166-delegation-primitives`](../0166-delegation-primitives/decisions.md#decisions) with their classification, which 0166.S1 reads as root grant inputs and depends on in part. The enforcement rows of `delegation`, `parentTools`, `offerTools`, and `delegationLeave` act on the operations of 0166, so whichever of this step and 0166.S1 (for `session.create`, `session.transfer`, and `session.release`) or 0166.S3 (for parent tools) lands second adds each refusal and its effect on calls in flight. The cooldown after a weakened content scan acts on the content caveat of 0166 decision 45 and on its transfer paths, so it lands with 0166.S1 for `session.create` with `files` and `files.import`, and with 0166.S3 for `materialize` pushes; until then §8.3's cooldown applies to `delegate_task` as written.

The security counter of `content_stop_epochs` under [`0144-session-derive`](../0144-session-derive/change.md), "Stops", is raised by whichever of this step and [DR-7](../0147-tenant-derived-runtimes/steps.md#steps) (0147.S11) lands second, in the transaction that activates a version adding a content forbid. 0147.S11 depends on this step, so 0147.S11 adds the raise. Whether the forbids `writableDefaultClones` and `alwaysWritableClones` also raise it is open decision 43.

### 0142.S3 Per-tenant SIEM destination
- status: Planned
- aliases: [SO-3]
- lanes: [specification, code, documentation]
- depends-on: [0142.S1, 0142.S2, 0136.S2, 0136.S3, 0127.S8 (part), 0127.S10 (part), 0127.S11 (part), 0150.S1 (part)]

`lenny-siem-forwarder`, destinations, cursors, the retention hold, the compliance gate, and evidence delivery. It needs the role, the delay on creating, changing, and removing a destination, the export, and the leaf. Audit delivery and evidence delivery both need the forwarder's leaf of the egress PKI from [EG-11](../0127-egress-and-git/steps.md#steps), the client certificate with which the Token Service admits the forwarder to the keyed read of destination Secrets. Evidence delivery also needs [EG-8](../0127-egress-and-git/steps.md#steps) and [EG-10](../0127-egress-and-git/steps.md#steps). Audit delivery can land before EG-8 and EG-10, with the `lenny-siem-forwarder` row added when [EG-10](../0127-egress-and-git/steps.md#steps) lands. The forwarder runs on the system class of [PM-8](../0136-placement/change.md#pm-8-system-components), so this step renders its row through the component helper of 0136.S2 and places it on the class that 0136.S3 renders.

### 0142.S4 Mirror to the tenant chain
- status: Planned
- aliases: [SO-4a, SO-4 (part)]
- lanes: [specification, code, documentation]
- closes: [F-13.3.25, F-12.3.30]

The mirror, `impersonated_by`, and the operator reference. It depends on no step and can land at any point, and 0142.S1 waits for it. `F-12.3.30` here is the Medium finding on `GET /v1/sessions?tenant=`. `BUILD-GAPS.md` gives the same identifier to a High finding on a Postgres primary outage at the environment middleware, which this step does not close.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-RequestCaller`, whose `Caller` gains the `impersonated_by` of the presented bearer, because the platform tools seam reads the caller through it, both for the audit rows it writes under the bearer and on the owner's tool-use approve route (`pkg/gateway/platformtools/interactions_resolution.go`), which SP10 closes to an impersonation bearer.

Its registration mirror records the `tenants` list on an interceptor registration from [RT-6](../0141-runtime-routing/change.md#rt-6-tenant-scoped-interceptors), and its settings events carry the events of the settings model and admin API of [`0150-tenant-security-settings`](../0150-tenant-security-settings/steps.md#steps). For each, whichever of this step and the other lands second adds the case. Its offering grant mirror lands with the offerings of [PM-3](../0136-placement/change.md#pm-3-placement-offerings-and-per-pool-nodeplacement) (0136.S5), which records the grant.

### 0142.S5 Operator access approval
- status: Planned
- aliases: [SO-4b, SO-4 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0142.S1, 0142.S2, 0142.S4, 0142.S6]

Operator access requests, the approval gate with its binding to the requester, and the emergency path. It needs the approver role, the setting, the mirror, and the store. The refusal of an `adapter-only` token on the impersonation mint is built by [0138.S2](../0138-rest-only-client-api/steps.md#steps) under "RO-4", and this step keeps it.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes `GWSEAM-AuthorizeSession` and `GWSEAM-SessionAccessible`, which resolve whether a caller may read a session, so that a platform admin's read of another tenant's session content needs an approved `session_read` request (SO-4b).

### 0142.S6 Tool-use approvals
- status: Planned
- aliases: [SO-5a, SO-5 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0142.S1, 0142.S2, 0135.S4, 0150.S1 (part)]

`security_approvals`, the approvals API, the withdrawal of a pending approval on suspension (L29 of [`0146-session-lifecycle`](../0146-session-lifecycle/change.md)), the tool hold at the gateway's tool dispatch on `tool_call` frames, and the refusal of a rule that names a reference tool of [`0166-delegation-primitives`](../0166-delegation-primitives/change.md#dp-5-reference-extension). The `when` conditions on `session.create` and `session.set_limits` and the holds of `approvals.parentTools` act on operations and child events of 0166, so whichever of this step and 0166.S1 or 0166.S3 lands second adds them. It needs the role and the rules. It cites CTF-1 and CTF-2 of [`0122-client-tools`](../0122-client-tools/change.md) (F-28.5.3.1 and F-28.5.3.2) for the relayed-frame path. F-7.2.46 is closed by the retirement of the adapter-local tools in [0139.S2](../0139-runtime-protocol/steps.md#steps), and this step holds no adapter-local call.

It lands after 0135.S4, which reshapes the same approval seam operations for harness approvals and moves the approval wait off the session's frame reader. Open decision 37 settles whether these rules reach harness approvals.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes the tool approvals (`GWSEAM-RegisterApproval`, `GWSEAM-CancelApproval`, `GWSEAM-ResolveApproval`, and `GWSEAM-AwaitApproval`) for approvals routed to security (SO-5a).

### 0142.S7 Repository write approvals
- status: Planned
- aliases: [SO-5b, SO-5 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0142.S6, 0127.S2, 0127.S3, 0127.S4]

Repository write approvals, and the `approvals.repositoryWrites` row of the tightening table. It needs the store, the mint checks and the writable set entry states that the binding feed carries, the writable set records, and the binding-mint rule of decision 25, which [EG-4](../0127-egress-and-git/steps.md#steps) carries.

### 0142.S8 Certificate windows and the remedy
- status: Planned
- aliases: [SO-6]
- lanes: [specification, code, documentation]
- closes: [F-25.6.9]

The windows, the remedy with its `certificates/status` rule, and the CA finding. It depends on no step and can land at any point.

### 0142.S9 lenny-ops rights
- status: Planned
- aliases: [SO-7]
- lanes: [specification, code, documentation]
- depends-on: [0142.S8]
- closes: [F-25.4.40, F-25.6.10]

The rights, including the Deployment writes that decision 18 drops and the `coreDnsStuckEndpoint` remedy that loses its target in `kube-system`, the version source, the §25.4 reconciliation, and the deletion of `TestOpsRBACMatchesSpecCanonicalBlock` with its skip entry in favour of a `static` chart-render test. It lands after the new remedy, because it removes the Secret delete and the Certificate patch that today's remedy uses.

### Notes on the steps

0142.S1, 0142.S2, 0142.S3, and 0142.S6 write their new records into §12.9 under the class names Public, Internal, Confidential, and Secret of [TSS-8](../0150-tenant-security-settings/change.md#tss-8-data-classes), so each depends on the class renames of 0150.S1 (part).

The outline stated that SO-4, SO-5, and SO-7 depend on nothing. The approval parts of SO-4 and SO-5 need the approver role, and SO-7 needs the new remedy, so the dependencies above correct it. Steps from different changes interleave, and each change states its own dependencies, with no shared queue file.

[TS-2](../0149-tenant-scoping-identity/change.md#identity-model-ts-2-ts-4-and-ts-6-to-ts-9) and [TS-6](../0149-tenant-scoping-identity/change.md#identity-source-record-ts-6) have no dependency on this change in either direction. This design works with a broker or with a customer's issuer. `securityRoleSource`, which 0142.S1 adds, is where they meet, and [TS-6](../0149-tenant-scoping-identity/change.md#identity-source-record-ts-6)'s activation takes 0142.S2's delay at a tenant with a `security-admin` row in any state. Because the security-principal write routes land with 0142.S2, no tenant holds a principal row before the delay exists, so 0149.S6 cannot land between a principal row and its delay.
