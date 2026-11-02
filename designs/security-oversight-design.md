# Security oversight for customers and the operator

Status: design for owner review, revision 1, 2026-11-02. Nothing in this document is built or staged, and no part of it has been tested. Statements about the current specification and tree were checked by reading them on this date. Proposals carry the label SO-n.

## Problem

A company runs Lenny as a multi-tenant SaaS. Each customer's security team has to approve the service and keep watching it, separately from the customer's tenant admin. Today:

- §10.2 Authentication, "Authorization and RBAC", makes `tenant-admin` the top customer role. The permission matrix has no audit row, and `requireAuditReader` in `pkg/gateway/externalapi/admin/tenants.go` admits only `platform-admin` and `tenant-admin`. `tenant-viewer` reads every session's content through "Read other users' sessions (same tenant)".
- A tenant admin can remove any other tenant user's role, invalidate the user, delete the user, or erase the user (§15.1 REST API, the user rows). Nothing protects a watcher from the person being watched.
- No record constrains the opt-ins a tenant admin turns on.
- `admin.impersonation_*` events sit under the platform tenant (§11.7 Audit Logging, "Platform-tenant audit event residency"), and `auditTenant` restricts a tenant admin to its own chain, so a customer cannot see them. `EmitAdminEvent` in `audit_sink.go` writes a platform admin's other admin events to the actor's chain, so a platform admin's write to a customer's configuration is also absent from that customer's chain. Actions taken under an impersonation bearer carry the target user's subject: a search of `pkg/audit`, `pkg/gateway/audit`, the admin router, and the auth middleware finds no reference to `act_sub`.
- `audit.siem.endpoint` is one Helm value, and `Outbox.RunCycle` (`pkg/audit/siem/outbox.go`) delivers rows one at a time and returns on the first failure. A customer's independent copy therefore sits with the operator, and the existing forwarder cannot carry a second, customer-controlled endpoint without letting that endpoint stall every tenant.
- `tool-use/{id}/approve` is limited to the session's owner (§10.2, **Session ownership.** rule 5).
- `lenny-ops` holds rights over Secrets that exceed its stated need (F-25.4.40), and `lenny-ctl doctor --fix` deletes the Secret of every healthy short-lived leaf and of any CA inside its window (F-25.6.9).

## Scope

This design covers a read-only security role with a protected assignment, a tenant security record holding guardrails, a posture read, per-tenant SIEM destinations, visibility and approval of operator access, delegated approvers, and the operator tooling defects that this review filed.

`egress-and-git-design.md` owns the production of egress and git evidence, and its hooks are inputs here. `forensics-evidence-preservation-design.md` owns preservation during an investigation. `tenant-scoping-identity-design.md` owns the statement of who supplies per-tenant SSO.

## Requirements

- R1. A customer's security team reads audit events, evidence, posture, and subscriptions for its tenant with no access to session content.
- R2. A tenant admin cannot remove, disable, or rewrite that access through any Lenny API.
- R3. A customer's security team can forbid specific opt-ins and host patterns, and the tenant admin cannot override that. Weakening a constraint is recorded, is justified, and takes effect only after a delay.
- R4. A posture read reports, per tenant, the settings a security review asks about, and reports an unknown value as unknown.
- R5. A customer can send its own audit and evidence records to its own destination. A failing destination affects no other tenant and no request path.
- R6. A customer sees operator access to its tenant in its own audit chain and SIEM, and can require its approval before an operator impersonates a user.
- R7. A person other than the session owner can resolve an approval when the tenant's policy says so, and a protected repository can require approval before a session pushes to it.
- R8. `doctor --fix` never rotates a CA, and the health API reports a healthy install with short-lived certificates as healthy.
- R9. `lenny-ops` has no standing read of Secrets through the API, and §25.4 states which of its remaining rights keep it inside the PKI's trust boundary.
- R10. A tenant with no security principal and an empty security record behaves as it does today.

## Security properties

Each property names the component that holds it.

- SP1. Only a subject in `tenant_security_principals` holds a security role, unless a platform admin has set the tenant's role source to `oidc`. The gateway's principal resolution enforces this.
- SP2. No tenant-admin or custom-role caller can change a security principal, or invalidate, disable, delete, or erase a subject that holds a security role. The security principal handlers and the user handlers enforce this.
- SP3. The write permissions of the security roles sit outside `TenantAdminPermissions()`, so the §10.2 custom-role ceiling keeps them out of any custom role. Custom-role validation (`IsTenantAdminPermission`) enforces this.
- SP4. Every opt-in write passes the guardrail check, and an object a tightening makes non-compliant stops taking effect at every enforcement point. The EG-9 write path, the Token Service, and the decision service enforce this.
- SP5. A weakening has no effect before its `effectiveAt`, and the delay cannot be shortened through the admin API. The security record's applier enforces this.
- SP6. A failing tenant SIEM destination delays only its own records, and never blocks an audit write, a session, a gateway start, or another destination. `lenny-siem-forwarder` holds this through per-destination leases and budgets.
- SP7. A tenant destination's records are complete or show a detectable gap, through chain fields and a gap record. The forwarder's cursor and hold rule hold this.
- SP8. An impersonation bearer is not issued until the customer-visible record is durable, and every audit row written under that bearer names the operator reference. The impersonation service and the audit write path enforce this.
- SP9. With approval required, the gateway issues an impersonation bearer only against a matching approved request, or through the emergency path the tenant allows. The impersonation service enforces this.
- SP10. No principal approves its own request. An impersonation bearer resolves no approval and holds no security role. The approval handlers and the bearer mint enforce this.
- SP11. A repository marked for approval receives no write token until its approval exists, enforced by the Token Service and `lenny-egress-authz` as the two gates of EG-3.

SP8 to SP10 bind operators who use the Lenny admin API. An operator with cluster or database rights can go around them (see "Residual risks").

## SO-1: security roles

### Roles

The design adds these built-in, tenant-scoped roles to `auth.AllRoles()`:

- **`security-auditor`** reads, and acts on nothing.
- **`security-approver`** resolves approvals routed to the security team, and reads only what those approvals display.
- **`security-admin`** manages the tenant security record, SIEM destinations, and security principals, and holds the auditor's reads.

Their write permissions are `security.principals.write`, `security.config.write`, `security.siem.write`, and `security.approve`. None of these is in the `tenant-admin` permission set.

| Operation | `platform-admin` | `tenant-admin` | `tenant-viewer` | `security-auditor` | `security-approver` | `security-admin` |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| Read audit events, summary | All tenants | Own | No | Own | No | Own |
| Retranslate or republish an audit row | All tenants | Own, as today | No | No | No | No |
| Read raw canonical audit tuples | With scope | With scope | No | With scope | No | With scope |
| Read evidence streams (`evidence.read.<stream>`) | All tenants | Own, by default | No | Own, the egress and git streams by default | No | Own, as the auditor |
| Read effective policy (`egress.effectivePolicy.read`) | All tenants | Own | No | Own | No | Own |
| Read posture | All tenants | Own | No | Own | No | Own |
| Read event subscriptions and deliveries | All tenants | Own | Own | Own | No | Own |
| Read legal holds | All tenants | Own | No | Own | No | Own |
| Read session content | All tenants, under SO-4 | Own | Own | No | Only a pending approval's detail | No |
| Manage security principals | Yes, with a ticket | No | No | No | No | Own |
| Write the tenant security record | Yes, with justification and the same delay | No | No | No | No | Own |
| Manage SIEM destinations | Yes, with justification | Read | No | Read | No | Own |
| Create a subscription owned by the security team | Yes | No | No | No | No | Own |
| Resolve an approval | No | No | No | No | Own | No |

A subject can hold a security role and any other role. Posture lists subjects who hold both `tenant-admin` and `security-admin` (see decision 4).

### Data model

```sql
CREATE TABLE tenant_security_principals (
    tenant_id     TEXT NOT NULL,
    user_id       TEXT NOT NULL,
    role          TEXT NOT NULL,   -- security-auditor | security-approver | security-admin
    assigned_by   TEXT NOT NULL,
    assigned_at   TIMESTAMPTZ NOT NULL,
    expires_at    TIMESTAMPTZ,
    justification TEXT NOT NULL,
    ticket_id     TEXT,
    PRIMARY KEY (tenant_id, user_id, role)
);
```

The table is separate from the user row because `handlePutTenantUserRole` replaces `u.Roles` with a single role and `handleDeleteTenantUserRole` clears it. A row in its own table cannot be overwritten by those paths or by `PUT /v1/admin/users/{userId}`, so the protection does not depend on guarding every user write. Principal resolution adds these rows to the principal's roles after token validation. The table has row-level security and a `lenny_tenant_guard` trigger as other tenant tables do, and §12.8 and §12.9 classify it.

The tenant record gains `securityRoleSource`, `managed` by default or `oidc`, and only a platform admin writes it. Under `managed`, a `security-*` value in a `lenny_role` claim is ignored and counted in `lenny_security_role_claim_ignored_total`.

### API

| Method and path | Caller | Behaviour |
| :-- | :-- | :-- |
| `GET /v1/admin/tenants/{id}/security-principals` | `platform-admin`, `tenant-admin`, `security-auditor`, `security-admin` | Lists rows and each subject's last authentication time. A tenant admin can see who watches, because concealment would not stop a tenant admin and would complicate offboarding. |
| `PUT /v1/admin/tenants/{id}/security-principals/{userId}/{role}` | `platform-admin`, or `security-admin` | Body: `{"justification", "ticketId", "expiresAt"}`. `justification` is required, and `ticketId` is required from a platform admin. Emits `security.principal_assigned`. |
| `DELETE /v1/admin/tenants/{id}/security-principals/{userId}/{role}` | `platform-admin`, or `security-admin` | Body: `{"justification", "ticketId"}`. A `security-admin` cannot remove the tenant's last `security-admin`, which returns `409 LAST_SECURITY_ADMIN`. Emits `security.principal_removed`. |

The first `security-admin` of a tenant is assigned by a platform admin against the customer's recorded request. After that the customer manages its own principals without the operator. Removing the last `security-admin` needs a platform admin and a ticket. An expired row stops granting the role at its `expires_at`, and a sweep deletes it and emits `security.principal_expired`.

### Enforcement points

- **Audit reads.** `requireAuditReader` and `auditTenant` move from role names to an `audit.read` permission. `POST /v1/admin/audit-events/{seq}/retranslate` and `/republish` use `requireAuditReader` today, so they get a separate `audit.recover` permission that `security-auditor` does not hold. Without this split the auditor would gain two write routes.
- **Evidence.** The EG-8 and EG-9 named permissions gain grants for the new roles, with no handler change. A later sensitive stream, such as FE-6's `agent.activity`, gets no default grant to any security role (see decision 19).
- **`lenny-ops`.** §25.4 Authentication requires `platform-admin` or `tenant-admin` on every endpoint. The role ceiling admits the security roles on the read endpoints named in the matrix, under the same tenant scoping as `tenant-admin`.
- **Protected subjects.** `DELETE /v1/admin/users/{userId}`, `POST .../invalidate`, `POST .../erase`, and a `PUT /v1/admin/users/{userId}` that disables the user return `403 SECURITY_PRINCIPAL_PROTECTED` to a caller without `security-admin` or `platform-admin` when the target holds a row, and emit `security.principal_protection_refused`. The §15.1 role routes stay open to a tenant admin, because they change only the user row's role and leave the security row in place.
- **Subscriptions.** `ops_event_subscriptions` gains `owner_class`, `tenant` by default or `security`. A tenant admin can read and cannot change or delete a `security` subscription.
- **Impersonation.** `impersonation.go` builds `TargetRoles` for the bearer. It drops `security-*` values, so an operator impersonating a security principal holds none of its security authority.
- **Scopes.** The §15.1 scope taxonomy gains a `security` domain, so a narrowed token works as it does for other domains.

### Failure behaviour

When the principals read fails, the caller holds no security role, and a protected-subject check returns `503`, so a tenant admin's user write fails closed during a database fault. An unknown or expired role has no effect. An auditor read follows §25.9's existing degradation rules, and each query emits `audit.query_executed` as today.

### Identity source

Lenny defines the roles and their permissions, and it stores the assignments. A SaaS operator's identity broker authenticates the subject (`tenant-scoping-identity-design.md`, TS-2). A broker can carry these roles only when the tenant is set to `oidc`, in which case whoever administers the customer's IdP controls them, and SP1 and SP2 hold only as far as that administrator is separate from the tenant admin.

## SO-2: tenant security record and posture

### The record

One record per tenant holds every customer-set security constraint, so one write path, one version, and one weakening rule serve SO-2, SO-4, and SO-5. An absent record constrains nothing (R10).

```json
{
  "credentialDelivery": { "forbid": ["file", "metadata"] },
  "storedTokenPush": "forbid",
  "verbatimClientFiles": "forbid",
  "tenantRegisteredServices": "forbid",
  "shadowPolicy": "forbid",
  "hostPatterns": {
    "neverAllow": ["*.paste.example"],
    "allowOnlyWithin": ["github", "pypi", "npm"]
  },
  "operatorAccess": { "approval": "required", "emergencyAccess": "allowed" },
  "approvals": {
    "tools": [{ "match": "lenny/delegate_task", "approver": "security", "showArguments": true }],
    "repositoryWrites": [{ "match": "github.com/acme/prod-*", "ttlSeconds": 3600 }],
    "timeoutSeconds": 1800
  },
  "weakeningDelaySeconds": 86400
}
```

Each stored version is a row in `tenant_security_config_versions` with its author, justification, `createdAt`, `effectiveAt`, and state (`pending`, `active`, `superseded`, or `cancelled`).

### API

| Method and path | Caller | Behaviour |
| :-- | :-- | :-- |
| `GET /v1/admin/tenants/{id}/security-config` | `platform-admin`, `tenant-admin`, `security-auditor`, `security-admin` | Returns the active version, any pending version, and the list of suspended objects. |
| `PUT /v1/admin/tenants/{id}/security-config` | `security-admin`, or `platform-admin` | Requires `If-Match`. Accepts the §25.2 dry-run pattern, which returns the objects it would suspend and writes nothing. |
| `POST /v1/admin/tenants/{id}/security-config/pending/{version}/cancel` | `security-admin` | Cancels a pending weakening. Emits `security.config_weakening_cancelled`. |
| `GET /v1/admin/tenants/{id}/security-posture` | `platform-admin`, `tenant-admin`, `security-auditor`, `security-admin` | See "Posture". |

### Tightening and weakening

The applier classifies each change field by field.

- **A tightening** applies in the write's transaction. It emits `security.config_tightened`, and the response lists every object it suspended.
- **A weakening** removes a forbid, shrinks `neverAllow`, widens `allowOnlyWithin`, relaxes approval rules, or lowers `weakeningDelaySeconds`. It needs a non-empty `justification`, or the write returns `400 SECURITY_CONFIG_JUSTIFICATION_REQUIRED`, following §9.2's **Justification requirement.** The write creates a pending version and emits `security.config_weakening_requested`, which subscriptions and SIEM destinations receive at once. A periodic applier activates it at `effectiveAt` and emits `security.config_weakened`.
- **A mixed write** applies its tightening parts at once and holds its weakening parts.

The delay differs from §8.3's weakening cooldown, which rejects delegation calls for a short window after a change has already taken effect. A delay fits here because it gives the rest of the security team time to cancel a weakening from a stolen credential before it has any effect. The duration has the same protection as §8.3's: `security.config.minWeakeningDelaySeconds`, a Helm value with a default of 3600, is a floor that the admin API cannot change, and a pending weakening keeps the delay in force when it was written.

A platform admin's write takes the same path, is mirrored under SO-4, and needs a `ticketId`.

### Enforcement

- **New writes.** SO-2 fills the EG-9 "tenant policy check, empty today" with a check of each opt-in change against the active version. The check reads the version under a shared row lock, and the record's writer holds an update lock, so a concurrent opt-in cannot miss a tightening. A violation returns `422 GUARDRAIL_VIOLATION` with `details.rule` and `details.configVersion`, and emits `security.guardrail_refused`. A failed read refuses the write with `503`. Sessions and traffic make no read of this record.
- **Existing objects.** A tightening sets `suspendedBy` on each non-compliant object in the same transaction, and increments the policy and definition versions so the change reaches decisions and pods through the existing binding push. An activation that removes a rule clears the mark it set.

| Constraint | Object affected | Effect while suspended |
| :-- | :-- | :-- |
| `credentialDelivery.forbid` | Credential bindings with that delivery | The Token Service mints nothing and `netagent` removes the file or endpoint. A plan naming the binding is rejected at create. |
| `storedTokenPush` | VCS pools with `allowStoredTokenPush` | Stored-token push and reused GitLab tokens are refused. Reads continue. |
| `verbatimClientFiles` | Tenant client config files and overlay file templates | Files are removed at the next binding, and pods keep no copy after `netagent` applies the change. |
| `tenantRegisteredServices` | Tenant-registered upstreams and git endpoints | Routes naming them deny, using the suspended state the egress design defines for a registration that fails re-resolution. |
| `shadowPolicy` | A running shadow policy | The candidate stops being evaluated. Active policy outcomes do not change. |
| `hostPatterns.neverAllow` | Tenant always-allowed and session-requestable entries | Patterns are held in a read-only partition of the tenant's denied hosts, so the effective-policy formula gains no term. |
| `hostPatterns.allowOnlyWithin` | Entries outside the set | Those entries are suspended. |

A platform gate and a guardrail both apply: the platform grants a class, and the tenant's security team can still forbid it. A guardrail never grants anything a gate has not granted.

### Posture

`GET .../security-posture` returns one object with a section per subject, and each value carries `observedAt` and `state` (`known` or `unknown`). A section whose source is unavailable returns `state: "unknown"` inside the §25.2 degradation envelope rather than failing the read.

| Section | Reports |
| :-- | :-- |
| `pools` | For each pool the tenant can use: `sharedWithOtherTenants` (a boolean, with no tenant names or counts), `ownerTenant`, `isolationProfile`, `egressProfile`, and `gitAccess`. |
| `egress` | The enforcement level (default, level 2, or level 1, with a pointer to what level 1 gives up), egress groups, and whether the tenant's address is shared. |
| `gates` | The per-class grants the tenant holds, and the opt-ins in use under each. |
| `securityConfig` | Active version, pending weakening with its `effectiveAt`, and suspended objects. |
| `audit` | `complianceProfile`, `dataResidencyRegion`, retention, and the chain integrity report from the most recent background verification, with its time and range. |
| `siem` | Whether a platform copy exists (with no address), each tenant destination's state and lag, and gaps recorded. |
| `operatorAccess` | Approval setting, active impersonation sessions, and counts for the last 30 days. |
| `principals` | Holder counts per security role, the time of each role's most recent authentication, and subjects also holding `tenant-admin`. |

Chain integrity comes from the §11.7 periodic check because `auditRows` loads a tenant's rows into memory, and an on-demand verification on every posture read would put that cost on a read an auditor polls. Posture is reported by the platform about itself (see "Residual risks").

## SO-3: per-tenant SIEM destination

### Existing limits

- `audit.siem.endpoint` and `audit.siem.secret` are single Helm values, and `siem_delivery_state` has `tenant_id` as its primary key, so one tenant chain has one cursor.
- `cmd/lenny-gateway/auditpipeline.go` refuses to start when the endpoint fails its test event, which is unacceptable for an endpoint a customer controls.
- `RunCycle` returns on the first delivery failure, so one failing endpoint would stall every chain.
- §25.5 webhook delivery retries three times and then marks the delivery failed with no replay, so a subscription is a notification path and cannot serve as an independent record.

### Component

A new `lenny-siem-forwarder` Deployment delivers to tenant destinations. The platform outbox in the gateway stays as it is for `audit.siem.endpoint`.

- Workers take a per-destination lease in Postgres with `FOR UPDATE SKIP LOCKED`, and each destination has one in-flight batch and a fixed request timeout, so one slow destination holds one worker slot for a bounded time.
- Each batch runs in a transaction scoped to the destination's tenant, so row-level security applies and no `__all__` read is needed.
- Its database role holds `SELECT` on `audit_log` and write access only to its own tables.
- It reads evidence from `lenny-evidence` by stable position (see "Changes requested").
- Its NetworkPolicy is the `lenny-ops` webhook egress rule of §25.4, which excludes private, cluster, and metadata ranges, plus Postgres and `lenny-evidence`. It holds no PKI rights and no Kubernetes write rights.

Decision 8 compares this with running delivery in `lenny-ops` or the gateway.

### Data model

```sql
CREATE TABLE tenant_siem_destinations (
    id              UUID PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    url             TEXT NOT NULL,
    auth_mode       TEXT NOT NULL,          -- hmac | header
    auth_header     TEXT,                   -- header name when auth_mode = header
    secret_ref      TEXT NOT NULL,
    ca_bundle       TEXT,
    region          TEXT,
    include_audit   BOOLEAN NOT NULL DEFAULT true,
    evidence_streams TEXT[] NOT NULL DEFAULT '{}',
    state           TEXT NOT NULL,          -- pending | active | failing | suspended | blocked
    generation      BIGINT NOT NULL,
    created_by      TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL
);

CREATE TABLE tenant_siem_delivery_state (
    destination_id  UUID NOT NULL,
    source          TEXT NOT NULL,          -- audit, or an evidence stream name
    position        JSONB NOT NULL,         -- audit sequence, or the evidence stable position
    last_acked_at   TIMESTAMPTZ,
    PRIMARY KEY (destination_id, source)
);
```

`audit.siem.maxDestinationsPerTenant` bounds the rows per tenant, with a default of 2 so a customer can move between SIEMs without a gap.

The HMAC secret or header value is stored as a Kubernetes Secret in the §4.9 credential namespace topology, readable only by the forwarder's ServiceAccount. It is written from `secretData` or generated server-side and returned once, as in §25.5's **Webhook Secret Lifecycle**. HMAC signing needs the plaintext, so the design does not copy §25.5's statement that only a hash is stored.

### API

| Method and path | Caller | Behaviour |
| :-- | :-- | :-- |
| `POST /v1/admin/tenants/{id}/siem-destinations` | `security-admin`, or `platform-admin` with a ticket | Creates a `pending` destination and validates its URL. |
| `POST .../siem-destinations/{destId}/verify` | Same | Sends a signed test record with a challenge. A 2xx response makes the destination `active`. |
| `GET .../siem-destinations`, `GET .../{destId}` | Also `tenant-admin` and `security-auditor` | Returns configuration without the secret, plus state, lag per source, and gap records. |
| `PUT .../{destId}` | `security-admin`, or `platform-admin` with a ticket | Requires `If-Match`. A URL change returns it to `pending`. |
| `POST .../{destId}/rotate-secret` | Same | A 60-second overlap, following §25.5. |
| `POST .../{destId}/replay` | `security-admin` | Body: `{"since"}`. Moves the audit cursor back within retention, and delivery repeats those records. |
| `DELETE .../{destId}` | Same | A removal or narrowing counts as a weakening, is justified, and is delayed under SO-2. Refused with `409 COMPLIANCE_SIEM_REQUIRED` when it would leave a regulated tenant with no independent copy. |

URL validation applies §25.5's **SSRF and DNS Rebinding Protections** at creation and on every delivery: HTTPS only, a registered domain, resolved addresses checked each time, metadata addresses blocked, and no redirects followed. A destination on a private network is refused until `tenant-outbound-connector-design.md` provides a route to it.

### Delivery

- **Format.** Records are the §11.7 OCSF translation, with the same dead-letter receipts. Delivery is at-least-once, and consumers deduplicate on `metadata.uid`.
- **What is sent.** With `include_audit`, the tenant's chain, which under SO-4 includes its operator access records. Evidence streams need `evidence.read.<stream>` held by the writer. Denied decisions are never sampled, following the egress design's export rule.
- **Start of stream.** The first delivery is a `siem.stream_anchor` record with the tenant, the starting sequence, the `prev_hash` at that point, and the tenant's `genesis_nonce`. A destination that starts mid-chain would otherwise lack what §11.7 writes alongside the first audit event.
- **Retries.** Backoff grows to a five-minute ceiling. A destination failing for `audit.siem.tenantFailingAfterMinutes` (default 15) becomes `failing` and emits `siem.destination_failing`, and the tenant's subscriptions receive it.
- **Gap record.** When records are lost to the hold bound below, the next delivery is a `siem.delivery_gap` record naming the range, and the tenant's own chain receives the same event.

### Retention hold

§16.4's SIEM delivery guard keeps audit rows until the platform forwarder acknowledges them, and the §25.9 force-drop stays with the platform admin. A tenant destination extends that guard, with a bound, so one dead endpoint cannot hold the operator's storage indefinitely:

- An unregulated tenant's destination holds rows up to `audit.siem.tenantMaxHoldDays` past their retention (default 7). After that GC proceeds, and the gap record follows.
- A regulated tenant whose independent copy is only that destination holds without a bound, and `AuditPartitionDropBlocked` fires for the platform admin, as it does today for the platform copy.
- Evidence objects follow the same rule against each stream's retention.

### Compliance gate

§11.7 rejects a regulated tenant when `audit.siem.endpoint` is absent, and a regulated tenant with no endpoint is fatal at gateway start. Under this design, an `active` tenant destination with `include_audit` also satisfies the gate for that tenant (decision 10). The startup check reads destinations, and it checks that each regulated tenant has one source or the other. It never contacts a tenant's endpoint at startup.

### Residency and erasure

- A destination declares `region`. For a tenant with `dataResidencyRegion`, a differing or absent region is refused unless the write carries `acknowledgeCrossRegionTransfer: true` with a justification, because the transfer is the customer's own decision. A forwarder that cannot reach the tenant's regional store marks the destination `blocked`.
- §12.8's `erasure.requested` event reaches tenant destinations, and the receipt records their acknowledgment. Records already delivered are outside Lenny's control, as §25.9 **Downstream SIEM Scope Boundary** states, and the customer is the responsible party for that copy.
- Tenant deletion stops delivery after a final `siem.stream_closed` record, and Phase 4 removes the destination rows and Secret.

## SO-4: operator access transparency

### SO-4a: mirror to the tenant chain

An `operator_access.recorded` event is appended to the target tenant's own audit chain, so it appears in `GET /v1/admin/audit-events`, in that tenant's SIEM destinations, and under its hash chain. The tenant admin and the security team read the same records.

| Source | Mirrored as | Timing |
| :-- | :-- | :-- |
| `admin.impersonation_started` | `operator_access.recorded` with `kind: impersonation_started`, reason, `ticketId`, target user, and expiry | Synchronous before the bearer is signed |
| `admin.impersonation_ended` | `kind: impersonation_ended` | Sweep |
| Expiry of an impersonation row with no ended event | `kind: impersonation_expired` | Sweep. §13.3 rule 4 writes no event at expiry, so without this the customer sees a session with no end. |
| Revocation by another route (§13.3 rule 4) | `kind: impersonation_revoked` | Sweep |
| Platform-tenant events carrying `target_tenant_id` under CMP-058, such as `compliance.profile_decommissioned` and `gdpr.legal_hold_overridden_tenant` | `kind` naming the event | Sweep |
| A platform admin's write to a resource of this tenant, from `EmitAdminEvent` | `kind: admin_write` with the resource and event type, with no body | Sweep |
| A platform admin's read that names this tenant, such as `GET /v1/admin/audit-events?tenantId=` or `GET /v1/sessions?tenant=` | `kind: admin_read` with the endpoint | Sweep, coalesced per subject, endpoint, and hour |
| `cross_tenant_read` from a call that names no tenant, such as `tenant_list` | Not mirrored | The event stays in the platform chain, and posture reports the count. |

**Write path.** §11.7's write-time validation rejects a row whose tenant differs from the request's scope, so the mirror is an explicit path that derives the tenant from `target_tenant_id` and writes only this event type. Under CMP-058 the source row can sit in a regional platform Postgres that differs from the tenant's audit shard, so the two writes cannot share a transaction. The ordering is:

1. The started event commits under its existing rule.
2. The mirror row commits on the tenant's chain under the per-tenant advisory lock, carrying `source_event_id`.
3. The bearer is signed and recorded as §13.3 rule 1 states.

A failure at step 2 returns an error and issues no bearer, consistent with §11.7's rule that audit must be durable before an externally observable side effect. A sweep that reads platform rows past a per-region high-water mark mirrors everything else. It deduplicates on `(tenant_id, source_event_id)`, so a restart repeats nothing. §13.3 rule 3 keeps its property that an audit outage never blocks a revocation, because the ended mirror is a sweep.

**Attribution of actions.** The impersonation bearer gains an `impersonated_by` claim. The bearer's subject stays the target user, as §13.3 requires, and §10.2 says a `user_bearer` carries no `act` claim, so the design uses a distinct claim. The principal carries it into the audit context, and each audit row under the bearer adds `unmapped.lenny.impersonated_by` in OCSF. A customer can then filter the actions an operator took, with the Lenny actor still recorded as the user impersonated.

**Operator identity.** Tenant-chain records carry an operator reference: a keyed hash of the admin's subject, with a per-tenant key, so a customer can tell two operators apart without learning names and two customers cannot correlate one operator. The platform chain keeps the subject. `security.operatorAccess.identityDisclosure` set to `named` writes the subject instead (decision 13).

### SO-4b: approval

With `operatorAccess.approval: required`, `POST /v1/admin/impersonation` needs an approved request.

| Method and path | Caller | Behaviour |
| :-- | :-- | :-- |
| `POST /v1/admin/operator-access-requests` | `platform-admin` | Body: `{"targetTenantId", "targetUserId", "reason", "ticketId", "durationSeconds"}`. Creates a `security_approvals` row of kind `operator_access`, mirrored at once. |
| `GET /v1/admin/operator-access-requests/{id}` | `platform-admin` | Returns the state. |
| `POST /v1/admin/impersonation` | `platform-admin` | Gains `accessRequestId`. Refused with `403 OPERATOR_ACCESS_APPROVAL_REQUIRED` when the tenant requires approval and the request is absent, unapproved, expired, used, or names another target. |

The customer resolves the row through the approvals API under SO-5. An unanswered request expires after `security.operatorAccess.requestTtlSeconds` (default 43200) and grants nothing. An approval is single use, and `durationSeconds` keeps §15.1's 3600-second maximum.

**Emergency path.** With `emergencyAccess: allowed`, the default, the body may carry `emergency: true` with a justification. The bearer is issued with no approval, the mirror row records `emergency: true`, `operator_access.emergency_used` fires critical on the tenant's chain and subscriptions, and `OperatorEmergencyAccess` alerts the operator. A tenant that sets `forbidden` has no such path, and its security team accepts that the operator cannot impersonate during an incident. Platform operations that need no bearer are unaffected.

**Coverage.** Approval gates impersonation, and it gates a platform admin's read of a tenant's session content, which reaches `GET /v1/sessions?tenant=` and the session read routes today. The proposal enumerates these routes. Metadata operations, such as listing tenants, force-terminate, suspend, and pool changes, stay ungated because operating the platform needs them, and they are mirrored.

**Failure behaviour.** When the approvals store is unreachable, a tenant requiring approval gets no bearer and the emergency path still needs the mirror write, so an audit outage stops impersonation entirely, as it does today.

## SO-5: delegated approvers

### Existing behaviour

The runtime sets `approvalRequired` on a `tool_call` frame (`schemas/lenny-adapter-jsonl.schema.json`), and `maybeGateToolCall` in `pkg/gateway/session/executor/podstream.go` holds the call only when the frame carries it. No platform policy forces approval. §15.2 refers to a policy that does not require approval, and a search of the specification finds no definition of one for tool calls. `approvalMode` in the §8 delegation lease example is the nearest field. `ToolApprovalGate` records an interaction keyed on session, owning user, and call, polls the store across replicas, and treats a timeout as denial. §10.2 **Session ownership.** rule 5 limits resolution to the owner.

### Approvals store and API

```sql
CREATE TABLE security_approvals (
    id           UUID PRIMARY KEY,
    tenant_id    TEXT NOT NULL,
    kind         TEXT NOT NULL,   -- tool_use | repository_write | operator_access
    session_id   TEXT,
    subject      JSONB NOT NULL,  -- the tool and call, repository and refs, or access request
    requested_by TEXT NOT NULL,
    state        TEXT NOT NULL,   -- pending | approved | denied | expired | revoked
    decided_by   TEXT,
    decided_at   TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ NOT NULL,
    reason       TEXT
);
```

| Method and path | Caller | Behaviour |
| :-- | :-- | :-- |
| `GET /v1/admin/tenants/{id}/approvals` | `security-approver`, and `security-auditor` and `security-admin` without arguments | Filters by kind, state, and session. |
| `POST .../approvals/{id}/approve`, `/deny` | `security-approver` | Optional `reason`. Conditional on `pending`, so one of any concurrent calls wins and the others return `409 APPROVAL_NOT_PENDING`. |
| `POST .../approvals/{id}/revoke` | `security-approver`, `security-admin` | For an approved `repository_write`. |

These routes sit on the admin surface, so the session surface keeps its existence-hiding rule, and the approver gains no other act or read on the session. §10.2 rule 5 gains one sentence stating this exception. A self-approval returns `403 APPROVAL_SELF_FORBIDDEN`, following §11.2.1's dual-control rule for billing corrections, where the comparison is the requester's subject and the session's owner. A call under an impersonation bearer returns `403 FORBIDDEN`. Every decision emits `approval.resolved` with the decider.

### SO-5a: tool-use approvals

`approvals.tools` rules match a tool name. `maybeGateToolCall` treats a matching frame as approval required, whatever the runtime's flag says, and routes it by the rule's `approver`.

- With `approver: security`, the gate writes a `security_approvals` row and an interaction, and the owner's `tool-use/{id}/approve` returns `403 APPROVAL_ROUTED_TO_SECURITY`. The owner's `deny` still works, because a user declining their own session's action needs no oversight.
- `AwaitApproval` keeps its wait, polling the interaction store, and the approvals API writes the same resolution, so the cross-replica path is unchanged.
- Timeout uses `approvals.timeoutSeconds` and denies. With no holder of `security-approver`, the write of a matching rule is refused with `422 NO_APPROVER`, and a later removal of the last holder raises `SecurityApproverCoverageLost`.
- `showArguments: true` shows `arguments` to the approver and emits `approval.viewed`. With `false`, the approver sees the tool name and session only.

Rules apply to frames the gateway sees: adapter-local tools and platform MCP tools. A tool a runtime runs inside its own process never reaches this gate, and the documentation states that limit.

### SO-5b: repository write approvals

The outline's flag on `writableRepositories` cannot live on the session field, because the client writes that field and an approval requirement set by the client constrains nothing. The requirement is in `approvals.repositoryWrites`, and the session field keeps its meaning.

1. At session bind, the gateway records the writable set under EG-3. An entry matching a rule is stored with `state: pendingApproval`, and a `security_approvals` row of kind `repository_write` is created naming the repository and any `allowedRefPatterns`.
2. The session runs with read access. A push reaches `netagent`, which asks the Token Service on I8, and both gates refuse to mint write scope for an entry in that state. `netagent` returns a 403 with the decision identifier and a git message saying that approval is pending, and `egress.local` records the refusal.
3. An approver approves. The entry moves to `granted` with an expiry of `ttlSeconds`, and the gateway pushes a binding change, so the next push mints.
4. At expiry or on revocation the entry returns to `pendingApproval`, the binding change flushes `netagent`'s cached write token, and a new push is refused. A token already minted stays valid at the host until its own expiry. `netagent` holds it, and the agent never does.

Approval is per session and repository. An unanswered request expires at `approvals.timeoutSeconds`, a later push is refused with the same message, and a client calling `POST /v1/sessions/{id}/write-approvals` opens a new row. Stored-token push and reused GitLab tokens pass through the same Token Service and `netagent` checks.

## SO-6: F-25.6.9, certificate windows and the remedy

### Defect

`detectCertExpiring` in `cmd/lenny-ops/doctorremediator.go` matches each Certificate within `certExpiryWindow` (7 days) with no `isCA` check, and `applyCertExpiring` annotates it and deletes its Secret. `charts/lenny/values.yaml` sets 24-hour leaves that renew 8 hours before expiry, so each healthy leaf always matches. `classifyCert` in `pkg/ops/opsservice/certmanager.go` reports `unhealthy` inside the same 7 days. §25.6 Diagnostic Endpoints and §25.8 "Cert-Manager Integration" specify both, so the specification changes with the code. `lenny-mtls-ca` keeps its key in its Secret (`mtls-pki.yaml`), so a matched CA loses its key.

### Relative windows

For each Certificate, let `W = notAfter - renewalTime`, using `status.renewalTime`. If that field is unset, the design uses `spec.renewBefore`, and if that is also unset, one third of `notAfter - notBefore`, which matches the 2/3 renewal point §10.3 states. A Certificate with no `notAfter` and no failure stays as today: it is still issuing.

| State | Condition |
| :-- | :-- |
| `healthy` | Neither condition below holds |
| `degraded` | `now > renewalTime + W/4`, or Ready is not true and `now > renewalTime` |
| `unhealthy` | `notAfter - now <= W/4`, or expired |

| Certificate | `W` | Healthy until | Unhealthy for its last |
| :-- | :-- | :-- | :-- |
| Leaf, 24 h with 8 h renew-before | 8 h | 18 h of age | 2 h |
| CA, 8760 h with 720 h renew-before | 720 h | 7.5 days past its renewal time | 7.5 days |
| Public certificate, 90 days with 30 days renew-before | 30 days | 7.5 days past its renewal time | 7.5 days |

The last two rows stay close to the 7-day threshold they have today, and the leaf stops reporting a fault when none exists. The `CertExpiryImminent` rule (`lenny_cert_expiry_seconds < 3600`) does not change. §25.8's sentence that this alert "fires based on these signals" is corrected, because the rule reads only remaining seconds.

### Remedy

`certManagerExpiring` matches a Certificate in `degraded` by time with Ready true, `isCA` false, no `lenny.dev/doctor-optout`, and no current `Issuing=True` condition. The fix sets `Issuing=True` with reason `ManuallyTriggered` through the `certificates/status` subresource, which is what `cmctl renew` does (see "Evidence"). cert-manager then re-issues into the same Secret, and the existing certificate stays in place until the new one arrives. The fix deletes no Secret and sets no `cert-manager.io/issue-temporary-certificate` annotation.

An `isCA` Certificate in `degraded` or `unhealthy` produces `certManagerCaExpiring` with `remediation: "manual"` and a pointer to the `ca-rotation` runbook, and `diagnostics.fix_skipped` records `reason: is_ca`. Failure of the status write records `diagnostics.fix_failed` and changes nothing.

The removal of the annotation has a second reason. cert-manager documents that annotation as placing a temporary certificate on the Secret until the final one returns. A temporary certificate in an mTLS leaf's Secret is a risk this design has not measured, and the new remedy does not need the annotation. The egress design's `lenny.dev/doctor-optout` annotations remain valid as a hard skip.

## SO-7: F-25.4.40, `lenny-ops` rights

### Current use

`charts/lenny/templates/ops-rbac.yaml` grants `get`, `list`, `watch`, and `delete` on `secrets` with no `resourceNames`, and `get`, `list`, `watch`, and `patch` on `certificates`. A search of `cmd/lenny-ops` and `pkg/ops` finds two uses of Secrets through the API: `List` in `versionsource.HelmChart` and `Delete` in `applyCertExpiring`. `get` and `watch` have no caller there, and `pkg/preflight/ops_readiness.go` lists `secrets: get` only for "Secret reads for backup credentials", which `lenny-ops` does not perform through the API. Helm's release record also holds `config` and `manifest`, so narrowing to the release Secret would still expose the chart's values, which can include `audit.siem.secret`.

### Change

| Rule | Today | After SO-7 |
| :-- | :-- | :-- |
| `secrets` | `get`, `list`, `watch`, `delete` | Removed |
| `certificates` | `get`, `list`, `watch`, `patch` | `get`, `list`, `watch` |
| `certificates/status` | None | `update`, for the SO-6 remedy |
| `jobs`, `deployments`, and all others | As rendered | Unchanged |

- **Version source.** `versionsource.HelmChart` reads the `helm.sh/chart` label on the `lenny-ops` Deployment, which `lenny.labels` stamps and `lenny-ops` can already `get`. It strips the chart name and restores `_` to `+`, which is reversible because SemVer permits no `_`. This also reports a version on an install rendered with `helm template`, where no release Secret exists. §25.8 "Version Aggregation" changes its line naming the `helm.sh/release.v1` Secret.
- **Preflight.** `CanonicalOpsSARBACRules` drops the `secrets: get` row and gains the status row.
- **Specification.** §25.4 "ServiceAccount and RBAC" shows a Role with no `secrets`, `certificates`, or `endpoints` rules, which the chart has. `TestOpsRBACMatchesSpecCanonicalBlock` is skipped for that reason, with an entry in `tests/registers/skip-reasons.yaml`. SO-7 reconciles the block and removes the skip and the entry.
- **Level 1.** EG-7's rows for `lenny-ops` (the probe ConfigMap and its I8 client certificate) add no Secret rule, so they do not conflict with this change.

### Statement for §25.4

§25.4 gains a **PKI trust boundary** paragraph: `lenny-ops` remains inside the trust boundary of the PKIs whose key Secrets are in its release namespace, because `jobs: create` and `deployments: patch, update` let it run a pod template that mounts any Secret there. A compromise can therefore obtain a CA key, and it has to create or change a workload to do so, which leaves a Kubernetes audit record and a visible object. The removal of the Secret and Certificate rights is defense in depth and closes the direct read, the delete of any Secret, and the rewrite of a Certificate's names. It does not move `lenny-ops` out of the boundary (decision 18).

The egress design's "Transport security" says that `lenny-ops` can read Secrets and patch Certificates. That sentence describes today, and it stays true until SO-7 lands.

## Configuration by scope

| Setting | Description | Scope | Managed by | Default | Notes |
| :-- | :-- | :-- | :-- | :-- | :-- |
| `security.config.minWeakeningDelaySeconds` | Floor under each tenant's weakening delay. | Deployment | Platform admin (Helm) | 3600 | The admin API cannot change it. |
| `security.operatorAccess.identityDisclosure` | Whether tenant records name the operator or carry a per-tenant reference. | Deployment | Platform admin (Helm) | `pseudonymous` | See decision 13. |
| `audit.siem.endpoint`, `secret` | The operator's own SIEM copy. | Deployment | Platform admin (Helm) | Empty | Unchanged. |
| `audit.siem.maxDestinationsPerTenant` | Bound on tenant destinations. | Deployment | Platform admin (Helm) | 2 | |
| `audit.siem.tenantMaxHoldDays` | How long past retention a failing tenant destination holds records for an unregulated tenant. | Deployment | Platform admin (Helm) | 7 | Regulated rule under "Retention hold". |
| `audit.siem.tenantFailingAfterMinutes` | Time before a destination becomes `failing`. | Deployment | Platform admin (Helm) | 15 | |
| `siemForwarder` replicas and resources | Sizing of `lenny-siem-forwarder`. | Deployment | Platform admin (Helm) | Chart | The chart omits the Deployment when `siemForwarder.enabled` is false. |
| `security.operatorAccess.requestTtlSeconds` | Lifetime of an unanswered operator access request. | Deployment | Platform admin (Helm) | 43200 | |
| `admin.doctor.allowedFixes` | Fixes `doctor --fix` may apply. | Deployment | Platform admin (Helm) | Existing list | Existing value. |
| `securityRoleSource` | Whether security roles come only from Lenny's table or also from an OIDC claim. | Tenant | Platform admin (admin API) | `managed` | See decision 2. |
| Security principals | Who holds each security role. | Tenant | Platform admin for the first and last `security-admin`, then `security-admin` | None | A tenant admin can read them. |
| `credentialDelivery.forbid` | Credential binding deliveries the tenant admin cannot use. | Tenant | `security-admin` | Empty | Applies on top of the platform gate. |
| `storedTokenPush` | Forbids `allowStoredTokenPush` and token reuse that depends on its gate. | Tenant | `security-admin` | Unset | |
| `verbatimClientFiles` | Forbids tenant client files and overlay templates. | Tenant | `security-admin` | Unset | |
| `tenantRegisteredServices` | Forbids tenant-registered upstreams and git endpoints. | Tenant | `security-admin` | Unset | |
| `shadowPolicy` | Forbids the shadow policy. | Tenant | `security-admin` | Unset | |
| `hostPatterns.neverAllow` | Host patterns no tenant policy can allow. | Tenant | `security-admin` | Empty | |
| `hostPatterns.allowOnlyWithin` | The largest host set a tenant policy can allow. | Tenant | `security-admin` | Unset | Bounded by the pool ceiling. |
| `weakeningDelaySeconds` | Delay before a weakening of the security record takes effect. | Tenant | `security-admin` | The floor | Lowering it is a weakening. |
| `operatorAccess.approval` | Whether impersonation needs the customer's approval. | Tenant | `security-admin` | `off` | |
| `operatorAccess.emergencyAccess` | Whether the emergency path exists. | Tenant | `security-admin` | `allowed` | Setting `forbidden` is a tightening. |
| `approvals.tools` | Tools that need approval and who resolves them. | Tenant | `security-admin` | Empty | Applies to frames the gateway sees. |
| `approvals.repositoryWrites` | Repository patterns whose push needs approval. | Tenant | `security-admin` | Empty | |
| `approvals.timeoutSeconds` | Time before an unanswered approval denies. | Tenant | `security-admin` | 1800 | |
| SIEM destinations | The customer's endpoints, credentials, and streams. | Tenant | `security-admin` | None | Removal is a weakening. |
| Security-owned subscriptions | §25.5 webhooks only the security team can change. | Tenant | `security-admin` | None | Notification path. |
| Approval requests | A client's request for write approval on its session. | Session | Client | None | The client cannot approve it. |

## Worked scenarios

The examples use tenant `acme`, whose tenant admin is alice and whose security lead is bob, a second tenant `globex`, and carol, a platform admin at the SaaS operator. Dave is a developer at `acme`.

**Onboarding.** Bob's company signs a contract asking for separate oversight. Carol calls `PUT .../acme/security-principals/bob@acme.com/security-admin` with the signed request's ticket number. Bob assigns `security-auditor` to his analyst and `security-approver` to two on-call engineers, with no further call to the operator. Bob reads the posture and sees a shared pool, default enforcement, and the `agentReadableDelivery` grant in use by one binding.

**A tenant admin tries to remove the auditor.** Alice calls `DELETE .../users/analyst@acme.com/role`. That route has no effect on the row in `tenant_security_principals`, so the role stays. She then calls `POST /v1/admin/users/analyst@acme.com/invalidate`, which returns `403 SECURITY_PRINCIPAL_PROTECTED` and emits an event bob's SIEM receives. She creates a custom role listing `security.config.write`, which is refused under the §10.2 ceiling. If alice deletes the analyst's account in the IdP, Lenny has no event for that. Bob sees the analyst's last authentication age in posture.

**Forbidding a delivery.** Bob runs `PUT .../acme/security-config?dryRun=true` with `credentialDelivery.forbid: ["file"]`, and the response lists `npm-publish-token`. He applies it. The binding is suspended, new plans naming it are rejected, and pods with it lose the file at the next binding change. Alice's attempt to create another `file` binding returns `422 GUARDRAIL_VIOLATION`. `globex` keeps its `file` binding, because each tenant has its own record and its own gate.

**A stolen credential weakens a rule.** An attacker holding bob's token removes the forbid at 02:00 with a justification. The change becomes pending for 24 hours, and the event reaches the SIEM and the on-call rotation at once. A second `security-admin` cancels it at 08:00. The binding stayed suspended throughout.

**A failing SIEM.** Bob registers `https://siem.acme.example/lenny`, and verify succeeds. At the weekend the endpoint returns 503 for two days. Deliveries back off, the destination becomes `failing`, and a subscription notifies bob. `globex` and the platform copy see no change, and sessions continue. On Monday, delivery resumes from its cursor with no gap, because rows are still within retention. Had the outage outlasted retention plus seven days, a `siem.delivery_gap` record would name the missing range.

**An operator support case with approval.** Dave reports a stuck session and opens a ticket. Carol calls `POST /v1/admin/operator-access-requests` for dave, for 30 minutes. A record appears in `acme`'s chain, and one of bob's approvers approves it. Carol starts impersonation with `accessRequestId`, and the tenant chain shows the start before the bearer exists. Each action Carol takes carries `impersonated_by`. She does not end it, so at expiry the sweep writes `impersonation_expired`. A second attempt with the same request returns `403 OPERATOR_ACCESS_APPROVAL_REQUIRED`.

**The same case at `globex`.** `globex` left approval off. Carol's impersonation starts with no request, and `globex`'s chain and tenant admin still see it, because the mirror applies to every tenant.

**A protected repository.** Dave's session declares `github.com/acme/prod-api` in `writableRepositories`, which matches a rule. The session clones and works. Its push fails with a 403 whose git message says approval is pending and gives the decision identifier. An approver reads the repository and refs and approves. The next push succeeds. Dave holds `security-approver` as well and tries to approve his own request, which returns `403 APPROVAL_SELF_FORBIDDEN`.

**`doctor --fix`.** Carol runs `lenny-ctl doctor --fix`. Every 24-hour leaf is inside its window with no fault, so nothing matches, and the `certManager` component reports `healthy`. Later a leaf passes its renewal time by 3 hours with Ready true, and the fix sets `Issuing=True`. A CA nearing expiry is reported as `certManagerCaExpiring` with a pointer to the rotation runbook, and nothing changes it.

## Interactions with `egress-and-git-design.md`

The hooks in its "Reserved for other designs" are inputs here, and the items below state how this design uses them.

| Egress design element | Use here |
| :-- | :-- |
| One write path for opt-ins, EG-9 | SO-2 fills the empty policy check. See "Changes requested" for the objects that have to pass through it. |
| Named permissions (`evidence.read.<stream>`, `egress.effectivePolicy.read`), EG-8 and EG-9 | SO-1 adds grants. `tenant-admin` keeps its defaults, so a tenant admin still debugs refusals. |
| Evidence envelope, EG-10 | SO-3 delivers streams translated to OCSF. `provenance` passes through, so a consumer can discount `self_reported` records. `payloadKeyRef` streams need a per-stream decision. |
| Gates on tenant opt-ins | Unchanged. A guardrail narrows inside a gate, and the platform admin keeps the grant. |
| Shadow policy, decision 42 | `shadowPolicy: forbid` uses the path that decision names. |
| Credential cache and binding push | Suspension and approval changes reach pods through the existing version bump and I7 push, so no new pod path exists. |
| Two-gate mint, EG-3 and EG-4 | Suspension and `pendingApproval` are read by both gates from their own stores, so a compromised decision service cannot approve a write or lift a suspension. |
| `lenny.dev/doctor-optout`, EG-11 | That design does not wait for SO-6. After SO-6, the annotation is optional for those Certificates. |
| PKI residual under "Transport security" | SO-7 narrows it as stated, and `lenny-ops` stays inside the boundary. |
| Regulated evidence write-ahead path | Unchanged. It concerns the record's existence before a connection, and SO-3 concerns delivery afterwards. |
| Level 1 replacement | Posture reports it as the enforcement level. Guardrails that act in the decision service apply as level 1 applies them, and level 1's limits are that design's statement. |
| Egress evidence consumers | The egress table "Evidence reads and export" row keeps its default, and this design adds the security roles beside it. |

`forensics-evidence-preservation-design.md` asks this document who may read evidence. Evidence reads are named per stream, so FE-6 streams carry no default grant, and FE-1 and FE-3 actions are outside the SO-4a mirror until that design decides what a tenant learns (decision 16).

## Changes requested of egress-and-git-design.md

These are requests. This design does not edit that file.

1. **Widen the objects on the one write path (EG-9).** Its hook lists bindings, gates, tenant upstreams, session-requestable hosts, and the shadow policy. Guardrails also need VCS pool writes (`allowStoredTokenPush`, `allowWrite`, and `allowWorkflowEdits`), tenant always-allowed hosts and warm-up additions, tenant-registered git endpoints, egress and git routes, tenant client config files, and tenant pool overlays that extend a ceiling or add a CA. Reason: `storedTokenPush` and `hostPatterns` cannot act on an object that skips the check, and moving each handler onto the path after it ships repeats work the hook exists to avoid. Gates stay on the path for audit, and the policy check does not apply to a platform grant.
2. **A suspended state on opt-in objects (EG-2, EG-4, and EG-9).** Each opt-in object carries a nullable `suspendedBy`, which the effective policy, the Token Service, `netagent`, and the decision service treat as absent, and setting it bumps the definition and policy versions. The effective-policy read names `guardrail` as a layer. Reason: without it, a tightening reaches only new writes, and adding the check to two independent gates and `pkg/binding` after they ship is rework to a shipped enforcement point. Registrations already have a suspended state, so this extends it.
3. **Writable set entries as records (EG-2 and EG-3).** A writable set entry carries a `state` (`granted`, the only value EG-3 sets, and `pendingApproval`) and an optional `expiresAt`. Both gates and `netagent` treat any state other than `granted` as read only, and the I8 refusal has a code that `netagent` turns into the git message. Reason: the writable set is in the session binding record, the binding feed, and an I8 decision, so changing a list of names to a list of records after EG-3 ships changes a stored format and a wire contract.
4. **Evidence read by stable position (EG-8 and EG-10).** `lenny-evidence` exposes a read from a named position per tenant and stream, with its SAN allowlist admitting `lenny-siem-forwarder`, and stream retention honours each registered consumer's position up to a configured bound. The egress design says a tenant subscription uses §25.5 and that a consumer holds a cursor, and §25.5's webhook has no replay. Reason: SO-3 needs a durable consumer, and the read API and retention check belong with the ingest they read from. The sampling rule and the OCSF translation stay as written.
5. **Add `lenny-siem-forwarder` to the `lenny-evidence` client rows (EG-10).** The evidence NetworkPolicy row and the egress PKI leaf list gain it. Reason: the PKI renders leaves by name, and that design lists them exhaustively.

## Residual risks

- **Operator with infrastructure rights.** A person with cluster-admin, database superuser, or KMS rights can read data, disable a check, or edit a record without passing through SO-4. §11.7 already states this for audit. The customer's own SIEM copy detects later tampering with delivered records, and it does not detect access that leaves no Lenny record. Approval governs the admin API path, and the documentation states this plainly so that a customer does not read it as a technical bar on the operator.
- **Delivery window.** A database superuser can change a row between commit and delivery, within the lag the destination reports.
- **Platform-reported posture.** Posture is the platform's statement about itself. A customer can check parts of it against evidence and audit records, and cannot check the rest independently.
- **IdP removal.** Deleting or disabling a security principal in the customer's IdP is outside Lenny's reach. Posture shows the last authentication time, and `SecurityPrincipalCoverageLost` fires only on Lenny-side changes. The spec has no SCIM (TS-2).
- **Operator's copy remains.** A tenant destination adds a copy, and the operator's `audit.siem.endpoint` continues to receive the tenant's chain.
- **Secrets already delivered.** Suspending a `file` or `federation` binding removes it from the pod, and an agent that copied the value keeps the copy until it expires or is rotated.
- **Minted write tokens.** A revoked repository approval stops new mints, and an existing token stays valid at the host until its expiry, held by `netagent`.
- **Runtime-internal tools.** A tool a runtime runs without emitting a `tool_call` frame is outside SO-5a. Egress rules still apply to its traffic.
- **Audit payload content.** §12.9 classifies audit events as T2 or T3 and transcripts as T3. This design has not established that no event type carries user content, such as a free-text reason or a dead-letter `raw_canonical_b64`. SO-1's proposal has to audit the catalog, and the auditor's "no session content" statement depends on that result.
- **Combined roles.** A person holding `tenant-admin` and `security-admin` defeats the separation for that tenant. Posture reports it, and Lenny does not refuse it.
- **`lenny-ops`.** It remains inside the PKI trust boundary after SO-7.
- **Destination coverage.** Destinations accept HTTPS POST with HMAC or a header. A SIEM that needs an object store or another protocol needs the customer's collector in front of it.

## Specification sections affected

- §10.2 Authentication: the built-in roles, the permission matrix (including the first audit rows), the role source, **Custom roles (tenant-scoped).**, **Role assignment:**, **Session ownership.** rule 5, and the `impersonated_by` claim.
- §11.2.1 Billing Event Stream: no change, cited as the self-approval precedent.
- §11.7 Audit Logging: write-time tenant validation (mirror path), "Platform-tenant audit event residency", the compliance gate and startup checks, the SIEM statements, and OCSF mapping for new events.
- §12.2 and §12.3: new tables and the forwarder's outbox statement. §12.8 Compliance Interfaces: `DeleteByTenant` and `DeleteByUser` coverage, erasure propagation, and protected subjects under erasure. §12.9 Data Classification: the new records.
- §13.1, §13.2.4, and §17: `lenny-siem-forwarder`, its NetworkPolicy row, identity, and sizing.
- §13.3 Credential Flow, "Platform-admin impersonation": role stripping, the mirror step in rule 1, the approval gate, and sweep behaviour at expiry.
- §15.1 REST API: new resources, error codes, and scope domain. §7.2: approval routing.
- §16.1, §16.4, §16.5, §16.7: metrics, the SIEM delivery guard's extension, alerts, and events.
- §24: `lenny-ctl` commands for principals, security config, destinations, and approvals.
- §25.1 and §25.4 Authentication: the role ceiling. §25.4 "ServiceAccount and RBAC": the reconciled block and **PKI trust boundary**.
- §25.5 Operational Event Stream: `owner_class` and **Tenant Isolation**.
- §25.6 Diagnostic Endpoints: the `certManagerExpiring` row, the new CA finding. §25.8 "Cert-Manager Integration" and "Version Aggregation": thresholds, the alert sentence, and the version source.
- §25.9 Audit Log Query API: permission statement, which it lacks today, and §25.12 tools for the new roles.

## Proposals and dependencies

The outline's items keep their labels. SO-4 and SO-5 each split, because their parts have different dependencies.

| Label | Content |
| :-- | :-- |
| SO-1 | The security roles, `tenant_security_principals`, protected subjects, `audit.read` and `audit.recover`, the `lenny-ops` ceiling, security-owned subscriptions, role stripping on impersonation, and the audit catalog review. |
| SO-2 | The tenant security record, its applier, the guardrail check, suspension, and the posture read. |
| SO-3 | `lenny-siem-forwarder`, destinations, cursors, the retention hold, the compliance gate, and evidence delivery. |
| SO-4a | The mirror, `impersonated_by`, and the operator reference. |
| SO-4b | Operator access requests, the approval gate, and the emergency path. |
| SO-5a | `security_approvals`, the approvals API, and tool-use routing. |
| SO-5b | Repository write approvals. |
| SO-6 | F-25.6.9: the windows, the remedy, and the CA finding. |
| SO-7 | F-25.4.40: the rights, the version source, the §25.4 reconciliation, and the skip removal. |

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| SO-1 | EG-8 and EG-9 for the evidence grants only | The named permissions on the new reads. The role and audit reads can land first, and the grants follow as one change. |
| SO-2 | EG-9, EG-2, and EG-4 (the requested fields), and SO-1 | The write path, the suspended state, and the role that writes the record. The posture sections on egress need EG-2 and EG-9 and otherwise report `unknown`. |
| SO-3 | SO-1, and EG-8, EG-10, and EG-11 for evidence delivery | The role, the export, and the leaf. Audit delivery can land first, with the `lenny-siem-forwarder` row added when EG-10 lands. |
| SO-4a | None | It can land at any point. |
| SO-4b | SO-1, SO-2, SO-4a, and SO-5a | The approver role, the setting, the mirror, and the store. |
| SO-5a | SO-1 and SO-2 | The role and the rules. |
| SO-5b | SO-5a and EG-3 | The store and the writable set records. |
| SO-6 | None | It can land at any point. |
| SO-7 | SO-6 | SO-7 removes the Secret delete and the Certificate patch that today's remedy uses. |

The outline stated that SO-4, SO-5, and SO-7 depend on nothing. The approval parts of SO-4 and SO-5 need the approver role, and SO-7 needs the new remedy, so this table corrects it. Proposals from different designs interleave, and each design states its own dependencies, with no shared queue file.

**Other designs that depend on this one.**

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| FE-6 | SO-1 | The rule that a sensitive stream has its own permission and no default grant. |
| TS-2 | None in either direction | This design works with a broker or without one. `securityRoleSource` is where they meet. |
| OC-2 | None | A connector registration passes the guardrail check as a tenant-registered service, and whichever of OC-2 and SO-2 lands second adds that case. |

## Documentation

Each proposal carries the pages for its own surface. Spec changes land before the pages that describe them, the pages carry no section numbers, and each concept has one owner page (`doc-content.md`). Design detail is not repeated here.

### New pages

| Page | Owns |
| :-- | :-- |
| `operator-guide/security-oversight.md` | The operator's view: assigning the first `security-admin`, what the operator can and cannot do under approval, the emergency path, the disclosure setting, and what approval does not cover. |
| `client-guide/security-teams.md` | The customer security team's view: roles, reading audit and evidence, posture, guardrails with dry-run and the delay, SIEM destinations with failure and gap behaviour, operator access records, and approvals. It includes one worked onboarding. |
| `reference/security-roles.md` | The generated permission table for the security roles. |

### Changes to existing pages

| Page | Change |
| :-- | :-- |
| `operator-guide/audit-ocsf.md` | "SIEM delivery" distinguishes the platform copy from tenant destinations and links to the owner page. |
| `operator-guide/multi-tenancy.md` | The roles section and "Audit Trail" add security principals and the operator access mirror. |
| `operator-guide/security.md` and `security-principles.md` | The control map adds the guardrails, and the trust statement adds the `lenny-ops` boundary paragraph. |
| `operator-guide/agent-operability.md` and `lenny-ctl.md` | `doctor --fix` behaviour, the new CA finding, the removed Secret delete, and the new commands. |
| `operator-guide/installation.md`, `reference/configuration.md` | The forwarder's values and preflight rows. |
| `api/admin.md` | "Required roles" adds the new roles, and the resource groups for principals, security config, posture, destinations, approvals, and operator access requests are added. |
| `api/rest.md`, `api/mcp.md`, `client-guide/session-lifecycle.md`, `client-guide/error-handling.md` | A tool call routed to security, `POST /v1/sessions/{id}/write-approvals`, and the new errors. |
| `reference/error-catalog.md`, `reference/metrics.md`, `reference/glossary.md` | New errors, metrics, alerts, and terms. |
| `runbooks/` | New runbooks for each alert, such as `OperatorEmergencyAccess`, `TenantSIEMDeliveryFailing`, and `SecurityApproverCoverageLost`. `cert-manager-outage.md` adopts the relative thresholds and the status-based fix, and `ca-rotation.md` is linked from the new finding. The runbook index gains the entries. |
| `tutorials/multi-tenant-setup.md` | Adds an optional step assigning a security team. |
| `adr/` | A new ADR records the decisions here. |

### Runtime author guide

| Page | Change |
| :-- | :-- |
| `lifecycle.md` | States that the gateway can hold a `tool_call` the runtime did not flag, that it can resolve as denied on timeout, and that a runtime treats that verdict as it treats a user's denial. The guide has no treatment of `approvalRequired` today, so this page adds one. |
| "Network and credentials in the pod" (from the egress design) | States that a push can return 403 with a pending-approval message, that a runtime reports this to the user and does not retry in a loop, and that the credentials a runtime sees can be withdrawn by a binding change. |
| `testing.md` | Describes the conformance case for a held call and a refused push. |

## Tests

Each proposal carries the tests for its own surface at the tiers `test-coverage.md` maps it to, with `// spec:` annotations and `// diagnosis:` comments. This section states what they must establish. None of these tests exists yet.

### Tier 0: static

- The new endpoints' OpenAPI entries carry `x-lenny-scope`, and the event catalog, OCSF mapping, and error catalog validate.
- `TestOpsRBACMatchesSpecCanonicalBlock` runs with no skip, and its register entry is gone.
- A rendered `lenny-ops` Role has no `secrets` rule and has only `get`, `list`, and `watch` on `certificates`.

### Tier 1: unit

- Field-by-field classification of a security record change as tightening, weakening, or mixed, including lowered delays and approval relaxations.
- The guardrail check for each opt-in kind and each rule, including an empty record.
- Certificate state classification across lifetimes, across unset `renewalTime`, `renewBefore`, and `notBefore`, at each boundary, and for expired and unissued certificates.
- Detection excluding `isCA`, opt-out, Ready false, and an `Issuing` condition already true.
- The version source's label parsing, including a `+` build suffix and an absent label.
- Operator reference derivation is stable per tenant and differs across tenants.
- Approval state transitions, self-approval comparison, and expiry.
- Permission tables for each role against the matrix, and the custom-role ceiling refusing each security write permission.

### Tier 2: component

- The doctor remedy against a kube-apiserver with a cert-manager CRD: it updates only the status subresource, deletes no Secret, and repeats safely.
- `versionsource.HelmChart` reads the label with RBAC that has no Secret rule.
- `lenny-preflight`'s RBAC audit passes against the new rule set.

### Tier 3: contract

- Tenant destination requests: batch body, signature header, and header authentication.
- The stream anchor, gap, and closed records against their schemas.
- Approvals and security config response schemas, and the JSONL `tool_call` verdict for a gateway-held call.
- The I8 refusal code for a pending write and the writable-set record in the binding feed.

### Tier 4: integration

- A role assignment takes effect on the next request and survives `PUT` and `DELETE` of the user's role.
- `security-auditor` reads audit, evidence, posture, and subscriptions, is refused session content, and is refused retranslate and republish.
- A tightening suspends objects and a dry run changes nothing; a concurrent opt-in and a tightening do not both succeed.
- A weakening stays pending until `effectiveAt`, can be cancelled, and clears suspensions on activation.
- The destination lifecycle, cursor replay after a forwarder restart with no gap, replay by `since`, and evidence delivery by position.
- Mirror: the sweep covers each row of the table, restarts with no duplicate, and writes `impersonation_expired`.
- `impersonated_by` appears on rows written under a bearer, across a session create and an admin write.
- Operator access request flow: approved, denied, expired, used twice, and wrong target.
- Tool approval routed to security across two gateway replicas, with timeout denial.
- Repository write: pending at bind, refused push, approval, successful push, expiry, and revocation.

### Tier 5: end to end on Kind

- `lenny-ctl doctor --fix` on a healthy chart with 24-hour leaves changes nothing, and `certManager` reports `healthy`. The existing `diagnostics_fix_test.go` changes to expect the status-based re-issue with the Secret UID unchanged and no annotation.
- A forced overdue leaf re-issues, and mTLS peers keep working through it.
- A CA inside its window produces the manual finding, and its Secret and key are unchanged.
- `lenny-siem-forwarder`'s NetworkPolicy allows a public test endpoint and drops cluster, private, and metadata addresses.
- A suspended `file` binding's file disappears from a running pod.

### Tier 6: end to end on cloud

No behaviour here needs a cloud-only feature. The forwarder's egress through a managed NAT is covered once with the egress design's cloud tests.

### Tier 7: load

- 7a: Many destinations with some slow or failing: the healthy destinations keep their lag bound, and worker slots stay within budget.
- 7a: Concurrent approve and deny on one approval produce one outcome; concurrent weakenings on one `If-Match` produce one version.
- 7a: Mirror appends under the per-tenant advisory lock stay within §11.7's lock SLO at the impersonation and admin write rates.
- 7b: A forwarder rollout under load loses no acknowledged position.

### Tier 8: chaos

- Destination outage past the hold bound: GC proceeds for an unregulated tenant, a gap record follows, and a regulated tenant holds with the alert.
- Postgres loss during a mirror write issues no bearer. During a revocation, the revocation holds and the mirror catches up.
- Forwarder kill mid-batch redelivers and skips nothing.
- Approvals store loss denies a held tool call rather than releasing it.
- Security principals read failure returns `503` for a protected-subject write.
- cert-manager down during `doctor --fix`: a leaf with Ready false is not matched, and the state goes to `degraded` and then `unhealthy` on schedule.

### Tier 9: security

- **Protected subjects.** A tenant admin and a custom role with `manage_users` cannot remove a security role, or invalidate, disable, delete, or erase its holder, through any user or role route.
- **Role source.** A `security-*` claim under `managed` grants nothing, including for a bearer from the tenant's IdP.
- **Isolation.** A security principal of `acme` reads nothing of `globex`, for each new read, including through `?tenantId=` and scatter-gather. Posture does not reveal other tenants' identity or number.
- **Impersonation.** A bearer for a security principal holds no security role, and a bearer resolves no approval and writes no security config.
- **Self-approval.** A requester, a session owner, and a platform admin requesting operator access cannot approve.
- **Approval gate.** No impersonation route issues a bearer without the approval when required, and an emergency use under `forbidden` is refused. Each listed session-content read is gated.
- **Two gates.** A decision service that allows a write on a `pendingApproval` entry obtains no mint. A compromised decision service cannot lift a suspension.
- **Destination SSRF.** Each §25.5 case, at creation and on delivery, including rebinding and a redirect.
- **Secrets.** Destination secrets never appear in a read, a log, or an audit event.
- **Content.** The auditor's reads carry no session content under the catalog review's fixtures, and the review's result is pinned as a test.
- **`lenny-ops` rights.** Its ServiceAccount cannot read, list, or delete a Secret or patch a Certificate's spec, and the residual Job and Deployment paths are asserted, so the §25.4 statement cannot drift from behaviour.

### Tier 10: conformance

A runtime treats a gateway-held `tool_call` and a denial on timeout correctly, and reports a refused push with a pending-approval message, for a runtime with no SDK and for each SDK.

### Tier 11: documentation

Every new alert resolves to a runbook, examples validate against their schemas, and the generated role table matches the permission code.

### Tier 12: cloud load

Sustained delivery to many destinations against the §17 tiers, to size the forwarder. The figures in this document are defaults and have not been measured.

## Evidence

The sources below were retrieved on 2026-11-02. Vendor pages describe vendors' own products, and they establish that comparable controls exist and how those vendors describe them. They do not establish customer demand or effectiveness.

- GitHub (vendor): organization owners "can configure all firewall settings at the organization level", and they "can disable this to prevent repositories from adding custom rules" ([firewall documentation](https://docs.github.com/en/copilot/how-tos/use-copilot-agents/coding-agent/customize-the-agent-firewall)). This is the closest precedent for SO-2.
- GitHub (vendor): the [security manager role](https://docs.github.com/en/organizations/managing-peoples-access-to-your-organization-with-roles/managing-security-managers-in-your-organization) has read access to all repositories, so GitHub's role sees content, where SO-1 withholds it.
- GitHub (vendor): [enterprise audit log streaming](https://docs.github.com/en/enterprise-cloud@latest/admin/monitoring-activity-in-your-enterprise/reviewing-audit-logs-for-your-enterprise/streaming-the-audit-log-for-your-enterprise) delivers at-least-once, health-checks each stream every 24 hours, gives owners six days to fix a misconfigured stream, and keeps a seven-day buffer for a paused stream. These informed the hold default and the delivery contract.
- Microsoft (vendor): [Customer Lockbox](https://learn.microsoft.com/purview/customer-lockbox-requests) uses a dedicated approver role, expires an unanswered request after 12 hours, limits access to 4 hours, grants no access on rejection, and logs operator actions as "Microsoft Operator" in the customer's audit log. These informed SO-4b's defaults and the operator reference. The page states that a Global Administrator can also approve, whereas this design keeps the approver role separate.
- Google (vendor): [Access Approval](https://docs.cloud.google.com/assured-workloads/access-approval/docs/overview) requires Access Transparency first, and it can auto-approve for time-sensitive outages, logging those in an "auto approved" state, except for certain sovereign offerings. This informed the emergency path and the option to forbid it.
- Sondera (vendor blog): a [CISO question list](https://blog.sondera.ai/p/ciso-questions-for-agent-vendors) asks for an audit log "completely separate from the user's activity logs".
- cert-manager (project): the [cmctl source](https://raw.githubusercontent.com/cert-manager/cmctl/main/pkg/renew/renew.go) sets the `Issuing` condition with reason `ManuallyTriggered` through `UpdateStatus`. The [API reference](https://cert-manager.io/docs/reference/api-docs/) defines `status.renewalTime` and `status.notAfter`, and the [annotations reference](https://cert-manager.io/docs/reference/annotations/) describes `cert-manager.io/issue-temporary-certificate`.
- Helm (project): the [`Release` type](https://raw.githubusercontent.com/helm/helm/main/pkg/release/v1/release.go) holds `Config` and `Manifest`.

No first-hand account from a customer security team was found, and the outline's note that Reddit was unavailable and Hacker News returned little stands. The priority between these items therefore rests on the scenario the owner stated rather than on observed demand.

## Decisions

Each item is open, and each states a recommendation and its cost. The first three answer the outline's open questions.

1. **Who can remove a `security-auditor`, and what stops a tenant admin?** Recommendation: a `security-admin`, or a platform admin with a ticket, can remove one, and the last `security-admin` needs the platform admin. A separate table and refusals on user routes stop a tenant admin. Cost: a customer that loses its last `security-admin` needs an operator support step, and a compromised auditor can be stopped only by those two roles.
2. **Does the broker or Lenny supply the role?** Recommendation: Lenny defines and stores it, and an OIDC claim counts only under `securityRoleSource: oidc`. Cost: an operator step per tenant at onboarding, and security principals maintained apart from IdP groups, which many customers prefer. `oidc` restores group management and moves the separation to the IdP's administrators.
3. **Which guardrails can a customer set without the operator?** Recommendation: all of them, both directions, within the platform's gates. Only the first `security-admin`, `securityRoleSource`, and gates need the operator. Cost: a customer can suspend its own objects and break its own sessions and then open tickets, which the dry run reduces.
4. **Refuse one subject holding `tenant-admin` and `security-admin`?** Recommendation: allow it and report it in posture, because small customers have one person for both. Cost: separation is advisory at those customers.
5. **Separate approver and admin roles?** Recommendation: keep them separate, as Lockbox has a dedicated approver role, so an on-call approver cannot loosen rules and a rule author does not approve by default. Cost: one more role to explain and assign, and small teams assign both.
6. **What happens to existing objects on a tightening?** Recommendation: suspend them at once, with a dry run first. Cost: a tightening can break running sessions. The alternative, applying to new writes only, leaves every existing opt-in in force and makes a guardrail unreliable as a statement.
7. **Weakening delay: a delay before effect, and how long?** Recommendation: a delay rather than §8.3's rejection window, with a floor of 1 hour and a tenant default equal to the floor. Cost: an urgent loosening waits, and neither the customer nor the platform admin can shorten it through the API. The example above uses 24 hours, which bob's team chose.
8. **Where does tenant delivery run?** Recommendation: a new `lenny-siem-forwarder` Deployment. Cost: one more Deployment, database role, and policy row. The `lenny-ops` alternative reuses its webhook egress rule and SSRF code at the cost of running more tenant-influenced URLs in a component inside the PKI boundary, with leader-only delivery. The gateway alternative reuses `Outbox` at the cost of running customer endpoints in the request-serving tier.
9. **Hold bound for a failing destination.** Recommendation: 7 days past retention for unregulated tenants, and no bound for a regulated tenant whose independent copy is only that destination. Cost: storage for the operator, unbounded in the regulated case until the platform admin force-drops with an acknowledgement.
10. **Does a tenant destination satisfy `COMPLIANCE_SIEM_REQUIRED`?** Recommendation: yes, when `active` with audit included, and its removal is refused when it would leave none. Cost: the operator can host a regulated tenant with no copy of its own, and the §11.7 startup check becomes per tenant. The alternative keeps the platform endpoint mandatory, which conflicts with the outline's point that the independent copy should not sit with the operator.
11. **Mirror or query-time union?** Recommendation: append to the tenant's chain. Cost: a second write path past §11.7's tenant validation, a synchronous cross-store write on impersonation start, and one row per operator action. A union at read time would add no rows, but its records would have no place in the tenant's chain or SIEM.
12. **How much operator activity to mirror?** Recommendation: the table under SO-4a, with reads coalesced hourly and tenant-unnamed `cross_tenant_read` left out. Cost: fleet-wide operations leave only a platform record and a posture count, and proposal work has to enumerate each admin route's tenant.
13. **Name the operator?** Recommendation: a per-tenant reference by default, with a Helm setting for names. Cost: a customer cannot identify the person without asking, which a contract can require. Named records raise staff privacy questions in some jurisdictions, which this design has not assessed.
14. **Approval and emergency defaults.** Recommendation: approval `off`, and emergency `allowed` when a tenant turns approval on. Cost: a customer gets no approval until its security team asks, and a stolen platform-admin credential can use the emergency path, at the price of a critical customer-visible record.
15. **What approval gates.** Recommendation: impersonation and a platform admin's session content reads. Cost: operators lose a routine diagnostic during support on tenants that opt in, and the route list needs review whenever a content route is added.
16. **Disclosure during an investigation.** Recommendation: no suppression in SO-4a, and `forensics-evidence-preservation-design.md` states whether its own quarantine and holds are disclosed, with its abuse workflow using its own read path instead of impersonation. Cost: an abuse investigation cannot impersonate a tenant under suspicion without that tenant seeing it. A disclosure delay would change that, at the cost of weakening what R6 promises.
17. **Repository approval timing, and forcing tool approval.** Recommendation: create the repository approval at bind, because the gateway records the writable set there, and have the gateway force approval on rule-matched frames regardless of the runtime's flag. Cost: approvers see requests for sessions that never push, and a runtime can see a held call it did not ask for. Creating the approval at first push avoids the noise and adds a path from `netagent` through the decision service to the approvals store.
18. **Restrict `lenny-ops`'s Job and Deployment rights by admission policy?** Recommendation: defer, and state the boundary in §25.4 as the outline asks. A policy limiting that ServiceAccount's pod templates to chart-rendered images and Secret references would move `lenny-ops` out of the boundary. Cost of deferring: the residual stays. Cost of doing it: every backup and upgrade template change needs a policy change, and the same treatment would be owed to the controller.
19. **Security roles and future sensitive streams.** Recommendation: no default grant on a stream with `payloadKeyRef`, and a `security-admin` can grant it. Cost: a customer's first look at `agent.activity` takes an explicit step.
20. **SO-6's remedy mechanism.** Recommendation: the `Issuing` status condition, which differs from the finding's text. Cost: Lenny depends on a status condition cert-manager manages, so a cert-manager change to that condition breaks the remedy, and the tier 2 and tier 5 tests are there to catch it. Keeping the Secret delete leaves a window with no Secret, keeps a delete right, and keeps the annotation's unmeasured effect.
21. **SO-7's extent.** Recommendation: remove the `secrets` rule and the Certificate `patch` entirely, where the finding suggested `resourceNames`. Cost: a dependency on SO-6, and the §25.8 version now reflects the applied Deployment's label rather than Helm's release record, which differ only while a release is in progress or a Deployment was edited by hand. The ConfigMap alternative costs a new chart object with no gain over the label.
22. **Stale pointers in the findings.** F-25.4.40 and F-25.6.9 each say the egress design carries the fix in its proposal 2, and that design has since moved them here. Recommendation: the closing proposals record the actual resolution when they close the findings, with no separate edit now. Cost: the text stays stale until then.
