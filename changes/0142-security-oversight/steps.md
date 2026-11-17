# Steps: 0142 Security oversight for customers and the operator

## Steps

The outline's items keep their labels. SO-4 and SO-5 each split, because their parts have different dependencies.

### 0142.S1 Security roles
- status: Planned
- aliases: [SO-1]
- lanes: [specification, code, documentation]
- depends-on: [0127.S8 (part), 0127.S9 (part)]

The security roles, `tenant_security_principals`, the resolver rules in the gateway and `lenny-ops`, reserved role names, protected subjects, the introspection exemption, `audit.read` and `audit.recover`, the `lenny-ops` ceiling, security-owned subscriptions, and the audit catalog review.

The evidence grants need [EG-8](../0127-egress-and-git/steps.md#steps) and [EG-9](../0127-egress-and-git/steps.md#steps), for the named permissions on the new reads. The role and audit reads can land first, and the grants follow as one change.

[FE-6](../0128-forensics-evidence-preservation/change.md#fe-6-agent-activity-streams) of [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/change.md) does not depend on this step: `tenant-admin` holds its read permissions until this step lands, and neither waits for the other, as that design says. Decision 19 states the auditor's grants.

### 0142.S2 Tenant security record and posture
- status: Planned
- aliases: [SO-2]
- lanes: [specification, code, documentation]
- depends-on: [0142.S1, 0127.S2, 0127.S4, 0127.S9, 0151.S1 (part), 0151.S2 (part), 0151.S4 (part), 0151.S5 (part), 0152.S2 (part), 0152.S3 (part)]

The tenant security record, its applier for both this record and the tenant security settings, delayed principal changes, the `identityProvider` write rule, the guardrail check, suspension, and the posture read with its `securitySettings` section.

It needs the write path, the suspended state, and the role that writes the record. [EG-9](../0127-egress-and-git/steps.md#steps), [EG-2](../0127-egress-and-git/steps.md#steps), and [EG-4](../0127-egress-and-git/steps.md#steps) carry the requested fields. The posture sections on egress need [EG-2](../0127-egress-and-git/steps.md#steps) and [EG-9](../0127-egress-and-git/steps.md#steps) and otherwise report `unknown`. The `placement` section, the posture's placement fields, and `placementConfigured` land with the placement requirements of [PM-4](../0136-placement/change.md#pm-4-placement-requirements) (0136.S6) and the offerings of [PM-3](../0136-placement/change.md#pm-3-placement-offerings-and-per-pool-nodeplacement) (0136.S5), because the section feeds PM-4's filter. Those steps land after this one, because 0150.S2 needs this step's applier and 0136.S4 needs 0150.S2.

Some record fields need steps of the user credential and user git identity changes. `identityBinding.forbidUnverified` needs [UC-2](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking), `userAuthorization` needs [UG-2](../0152-user-git-identity/change.md#ug-2-repository-authorization), with `.forbidRuntimeHeld` needing [UG-3](../0152-user-git-identity/change.md#ug-3-pull-request-authoring) and the [UC-5](../0151-user-credential-delivery/change.md#uc-5-delivery-file) it builds on, `tokenTrust` needs [UC-1](../0151-user-credential-delivery/change.md#uc-1-credential-types-and-the-registered-source) and, for link providers, [UC-2](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking), `requestRules` needs [UC-4](../0151-user-credential-delivery/change.md#uc-4-request-rules-and-presets), and `userCredential.file` needs [UC-5](../0151-user-credential-delivery/change.md#uc-5-delivery-file). Each of these fields lands with this step or with the step it needs, whichever is later. The fields of [UC-1](../0151-user-credential-delivery/change.md#uc-1-credential-types-and-the-registered-source), [UC-4](../0151-user-credential-delivery/change.md#uc-4-request-rules-and-presets), and [UC-5](../0151-user-credential-delivery/change.md#uc-5-delivery-file) pass the guardrail check through [EG-9](../0127-egress-and-git/steps.md#steps), and the guardrails named under "User credentials and request rules" land with this step or with the UC step, whichever is later. The same holds for `userAuthorization` and `allowRuntimeHeldCredentials` with [UG-2](../0152-user-git-identity/change.md#ug-2-repository-authorization) and [UG-3](../0152-user-git-identity/change.md#ug-3-pull-request-authoring).

The posture fields for tenant scoping need [TS-3](../0149-tenant-scoping-identity/change.md#ts-3-session-environment-context-and-cross-environment-sharing), [TS-6](../0149-tenant-scoping-identity/change.md#identity-source-record-ts-6), [TS-8](../0149-tenant-scoping-identity/change.md#scim-server-ts-8), and [TS-9](../0149-tenant-scoping-identity/change.md#directory-groups-ts-9), each for its own fields: sharing paths, identity sources and their issuers, SCIM credentials and directory rows, and directory groups. Until the TS step that adds a record lands, the fields that read it report `unknown`.

The posture `interceptors` section needs the `tenants` list on an interceptor registration from [RT-6](../0141-runtime-routing/change.md#rt-6-tenant-scoped-interceptors), and whichever of this step and RT-6 lands second adds the case. The applier for the settings and the `securitySettings` posture section need the settings model and admin API of [`0150-tenant-security-settings`](../0150-tenant-security-settings/steps.md#steps), for the version table the applier activates and the view the posture section repeats. Whichever of this step and that step lands second adds the case.

The following steps do not depend on this step, and whichever lands second adds the case:

- `crossEnvironmentSharing` acts on [TS-3](../0149-tenant-scoping-identity/change.md#ts-3-session-environment-context-and-cross-environment-sharing)'s setting and declarations.
- `lifecycleSubjects` lets a worker of [TS-4](../0149-tenant-scoping-identity/change.md#user-lifecycle-ts-4) or a SCIM credential of [TS-8](../0149-tenant-scoping-identity/change.md#scim-server-ts-8) act on a protected subject.
- A connector registration of [OC-2](../0148-tenant-outbound-connector/steps.md#steps) passes the guardrail check as a tenant-registered service.

### 0142.S3 Per-tenant SIEM destination
- status: Planned
- aliases: [SO-3]
- lanes: [specification, code, documentation]
- depends-on: [0142.S1, 0142.S2, 0127.S8 (part), 0127.S10 (part), 0127.S11 (part)]

`lenny-siem-forwarder`, destinations, cursors, the retention hold, the compliance gate, and evidence delivery. It needs the role, the delay on creating, changing, and removing a destination, the export, and the leaf. Evidence delivery needs [EG-8](../0127-egress-and-git/steps.md#steps), [EG-10](../0127-egress-and-git/steps.md#steps), and [EG-11](../0127-egress-and-git/steps.md#steps). Audit delivery can land before the EG steps, with the `lenny-siem-forwarder` row added when [EG-10](../0127-egress-and-git/steps.md#steps) lands.

### 0142.S4 Mirror to the tenant chain
- status: Planned
- aliases: [SO-4a, SO-4 (part)]
- lanes: [specification, code, documentation]
- closes: [F-13.3.25, F-12.3.30]

The mirror, `impersonated_by`, and the operator reference. It depends on no step and can land at any point.

Its registration mirror records the `tenants` list on an interceptor registration from [RT-6](../0141-runtime-routing/change.md#rt-6-tenant-scoped-interceptors), and its settings events carry the events of the settings model and admin API of [`0150-tenant-security-settings`](../0150-tenant-security-settings/steps.md#steps). For each, whichever of this step and the other lands second adds the case. Its offering grant mirror lands with the offerings of [PM-3](../0136-placement/change.md#pm-3-placement-offerings-and-per-pool-nodeplacement) (0136.S5), which records the grant.

### 0142.S5 Operator access approval
- status: Planned
- aliases: [SO-4b, SO-4 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0142.S1, 0142.S2, 0142.S4, 0142.S6]

Operator access requests, the approval gate, and the emergency path. It needs the approver role, the setting, the mirror, and the store.

### 0142.S6 Tool-use approvals
- status: Planned
- aliases: [SO-5a, SO-5 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0142.S1, 0142.S2]

`security_approvals`, the approvals API, and the tool hold at MCP dispatch and on relayed frames. It needs the role and the rules. It addresses F-7.2.46.

### 0142.S7 Repository write approvals
- status: Planned
- aliases: [SO-5b, SO-5 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0142.S6, 0127.S2, 0127.S3]

Repository write approvals. It needs the store, the mint checks and the writable set entry states that the binding feed carries, and the writable set records.

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
- closes: [F-25.4.40]

The rights, the version source, the §25.4 reconciliation, and the skip removal. It lands after the new remedy, because it removes the Secret delete and the Certificate patch that today's remedy uses.

### Notes on the steps

The outline stated that SO-4, SO-5, and SO-7 depend on nothing. The approval parts of SO-4 and SO-5 need the approver role, and SO-7 needs the new remedy, so the dependencies above correct it. Steps from different changes interleave, and each change states its own dependencies, with no shared queue file.

[TS-2](../0149-tenant-scoping-identity/change.md#identity-model-ts-2-ts-4-and-ts-6-to-ts-9) and [TS-6](../0149-tenant-scoping-identity/change.md#identity-source-record-ts-6) have no dependency on this change in either direction. This design works with a broker or with a customer's issuer. `securityRoleSource`, which 0142.S1 adds, is where they meet, and [TS-6](../0149-tenant-scoping-identity/change.md#identity-source-record-ts-6)'s activation takes 0142.S2's delay at a tenant with a `security-admin` row in any state.
