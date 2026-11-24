# Security oversight for customers and the operator

Status: `Draft`, revision 67, 2026-11-24. Programs: [v1](../programs/v1.md).

## Problem

A company runs Lenny as a multi-tenant SaaS (software as a service). Each customer's security team has to approve the service and keep watching it, separately from the customer's tenant admin. Today:

- §10.2 Authentication, "Authorization and RBAC" (role-based access control), makes `tenant-admin` the top customer role. The permission matrix has no audit row, and `requireAuditReader` in `pkg/gateway/api/admin/tenants.go` admits only `platform-admin` and `tenant-admin`. `tenant-viewer` reads every session's content through "Read other users' sessions (same tenant)".
- A tenant admin can remove any other tenant user's role, invalidate the user, delete the user, or erase the user (§15.1 REST API, the user rows). Nothing protects a watcher from the person being watched.
- No record constrains the opt-ins a tenant admin turns on.
- `admin.impersonation_*` events sit under the platform tenant (§11.7 Audit Logging, "Platform-tenant audit event residency"), and `auditTenant` restricts a tenant admin to its own chain, so a customer cannot see them. `EmitAdminEvent` in `pkg/audit/adminaudit/adminaudit.go` writes a platform admin's other admin events to the actor's chain, with a row about another tenant carrying that tenant as a top-level `target_tenant_id`, so a platform admin's write to a customer's configuration is also absent from that customer's chain. Actions taken under an impersonation bearer carry the target user's subject: a search of `pkg/audit`, `pkg/gateway/audit`, the admin router, and the auth middleware finds no reference to `act_sub`, the `issued_tokens` column that records the platform admin behind an impersonation bearer (§13.3).
- `audit.siem.endpoint` is one Helm value, and `Outbox.RunCycle` (`pkg/audit/siem/outbox.go`) delivers rows one at a time and returns on the first failure. A customer's independent copy therefore sits with the operator, and the existing forwarder cannot carry a second, customer-controlled endpoint without letting that endpoint stall every tenant.
- `POST /v1/sessions/{id}/tool-use/{toolCallId}/approve`, the route on which a session's owner approves a tool call the gateway holds, is limited to the session's owner (§10.2, **Session ownership.** rule 5).
- `lenny-ops` holds rights over Secrets that exceed its stated need (F-25.4.40), and `lenny-ctl doctor --fix` deletes the Secret of every healthy short-lived leaf and of any CA inside its window (F-25.6.9).

The statements in this design about the current specification and tree were checked by reading them on 2026-11-05. The code paths were updated on 2026-11-19 to the gateway package layout of [0117](../0117-gateway-package-layout/change.md).

## Scope

This design covers a read-only security role with a protected assignment, a tenant security record holding guardrails, a posture read, per-tenant SIEM destinations, visibility and approval of operator access, delegated approvers, and the operator tooling defects that this review filed.

[`0127-egress-and-git`](../0127-egress-and-git/change.md) owns the production of egress and git evidence, and its hooks are inputs here. [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/change.md) owns preservation during an investigation. [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md) owns the statement of who supplies per-tenant SSO (single sign-on). [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md), [`0152-user-git-identity`](../0152-user-git-identity/change.md), [`0129-github-credential-addon`](../0129-github-credential-addon/change.md), and [`0130-gitlab-credential-addon`](../0130-gitlab-credential-addon/change.md) own the mechanisms that "User credentials and request rules" constrains.

## Terms

| Term | Meaning |
| :-- | :-- |
| Operator | The organization that runs a Lenny deployment and hosts customers on it as tenants, such as a SaaS company. Its staff act through the `platform-admin` role. |
| Customer security team | The people at a customer, separate from its tenant admin, who approve the service and keep watching it. In Lenny they are the tenant's security principals. |
| Security roles | The tenant-scoped built-in roles SO-1 adds: `security-auditor`, which reads; `security-approver`, which resolves approvals routed to the security team; and `security-admin`, which manages the tenant security record, SIEM destinations, and security principals. |
| Role resolver | The step in the gateway and in `lenny-ops` (`PlatformRoles`) that computes a request's roles from the bearer's claims and the user's row. SO-1 has it add the subject's active security rows and remove any `security-*` value it finds elsewhere. |
| Security principal | A row in `tenant_security_principals` that gives one subject one security role in one tenant. A subject that holds such a row is protected from the tenant admin's user routes. See SO-1. |
| `securityRoleSource` | A tenant setting that only a platform admin writes. Under `managed`, the default, security roles come only from `tenant_security_principals`. Under `oidc`, a roles claim from the tenant's identity source also counts. |
| Tenant security record | One versioned record per tenant that holds every customer-set security constraint. The API calls it `security-config`, and each version is a row in `tenant_security_config_versions`. An absent record constrains nothing. See SO-2. |
| Guardrail | A constraint in the tenant security record that limits what a tenant admin can configure, such as `credentialDelivery.forbid` or `hostPatterns.neverAllow`. A guardrail forbids, and it never grants anything a platform gate has not granted. |
| Tightening and weakening | A change to the security record that makes it stricter is a tightening, and it applies in the write's transaction. A change that relaxes it is a weakening, and it takes effect only after the weakening delay. "Tightening and weakening" under SO-2 lists the weakenings, and some security principal and SIEM destination changes are weakenings too. A pending change's activation is the moment it takes effect, at its `effectiveAt`. |
| Weakening delay | The time between a weakening's write and its `effectiveAt`, set by the record's `weakeningDelaySeconds` within a Helm floor and ceiling. Another `security-admin` can cancel the weakening during the delay. |
| Suspension | The state of an existing opt-in object that a tightening makes non-compliant. The tightening sets the object's `suspendedBy` field, and every enforcement point then treats the object as absent, apart from the one exception the `suspendedBy` row under "Terms from other designs and the platform" states. |
| Exemption | An object and a rule entry that a tightening names, which the tightening leaves unsuspended. See "Enforcement" under SO-2. |
| Posture | The read-only report that `GET .../security-posture` returns, with one section per topic and each value marked `known` or `unknown`. See "Posture". |
| Platform copy | The operator's SIEM copy of every tenant's audit records, sent to the Helm value `audit.siem.endpoint`. |
| Tenant destination | A SIEM endpoint that a tenant's `security-admin` registers, to which the new `lenny-siem-forwarder` Deployment delivers that tenant's audit chain and chosen evidence streams. See SO-3. |
| Regulated tenant | A tenant whose `complianceProfile` is `soc2`, `fedramp`, or `hipaa`. §11.7 requires an independent audit copy for such a tenant: today it rejects the tenant's creation or update with `COMPLIANCE_SIEM_REQUIRED` when the platform copy is not configured. |
| Mirror | The `operator_access.recorded` event that SO-4a appends to a tenant's own audit chain for each operator action on that tenant, such as an impersonation start or a platform admin's write. |
| Operator reference | The value that names an operator in a tenant's records: a keyed hash of the platform admin's subject, under a per-tenant key. |
| Approval | A row in `security_approvals` of kind `tool_use`, `repository_write`, or `operator_access`, which a `security-approver` approves or denies. See SO-5. |
| Operator access request | An approval of kind `operator_access` that a platform admin creates before impersonating a user, or before reading session content, in a tenant that sets `operatorAccess.approval: required`. See SO-4b. |
| Emergency path | An operator access request with `emergency: true`, which is approved on creation with no customer action and raises a critical record the customer sees. A tenant removes the path with `operatorAccess.emergencyAccess: forbidden`. |
| Justification and `ticketId` | Every platform admin write in this design carries a required free-text `justification` and an optional `ticketId`, and Lenny records both. |
| The outline | The first version of this document, an outline dated 2026-11-02, which named the items SO-1 to SO-7 and asked the open questions that decisions 1 to 3 answer. |
| Labels | Parts of the design carry the label SO-n, and requirements carry the label SOR-n. |

### Terms from other designs and the platform

This design builds on roles, records, and mechanisms that the specification and other designs define. The table gives each one the meaning this document relies on.

| Term | Meaning |
| :-- | :-- |
| Tenant admin and `tenant-viewer` | `tenant-admin` is the top customer role of [§10.2](../../spec/10_gateway-internals.md#102-authentication). It administers its tenant's users, roles, and configuration. `tenant-viewer` is the built-in read role, and it reads every session's content in its tenant. |
| Platform admin | `platform-admin` is the operator's role. It acts on every tenant through the admin API. |
| Platform tenant and operator tenant | The platform tenant is the tenant identifier under which Lenny writes platform-level audit events, such as `admin.impersonation_started` and `cross_tenant_read`, so they sit in the platform chain ([§11.7](../../spec/11_policy-and-controls.md#117-audit-logging), **Write-time tenant validation**). The operator tenant is the one tenant that `auth.platformAdminTenant` names, the only tenant where `platform-admin` rows exist, under [TS-2](../0149-tenant-scoping-identity/change.md#identity-model-ts-2-ts-4-and-ts-6-to-ts-9). They are different tenants. [§10.2](../../spec/10_gateway-internals.md#102-authentication), **`tenant_id` format validation.**, reserves the tenant ID `platform` for the platform tenant, and the admin API and the bootstrap seed refuse to create a tenant with that ID. The operator tenant holds user rows, so it is a tenant that exists. |
| `__all__` | The sentinel tenant scope under which a `platform-admin` reads across every tenant. A read under it emits a `cross_tenant_read` audit event ([§13.3](../../spec/13_security-model.md#133-credential-flow), **Platform-admin impersonation** rule 7). |
| Custom role and the custom-role ceiling | A custom role is a role a tenant defines. §10.2 limits its permissions to the tenant admin's permission set, `TenantAdminPermissions()`, which this document calls the custom-role ceiling. |
| Subject and token types | A subject is the user identifier (`sub`) a token names. Every Lenny-issued token carries a `typ` claim ([§10.2](../../spec/10_gateway-internals.md#102-authentication)): `user_bearer` for a person or service account acting as itself, `session_capability` for a client API bearer, `a2a_delegation` for a delegation child token, and `service_token` for an internal platform token. `iat` is a token's issue time. An `a2a_delegation` token also carries an `act` claim, the RFC 8693 actor claim that names the party acting for the subject, and a `user_bearer` carries none. |
| Impersonation bearer | A token a platform admin obtains through `POST /v1/admin/impersonation` to act as a user ([§13.3](../../spec/13_security-model.md#133-credential-flow), **Platform-admin impersonation**). Its subject is the target user. The bearer's `issued_tokens` row records the platform admin in its `act_sub` column, and §13.3 calls a row with `act_sub` set and no parent an impersonation row. SO-4a adds an `impersonated_by` claim that names the operator on the bearer itself. |
| Identity source and deployment issuer | The OIDC (OpenID Connect) issuer that authenticates a tenant's users, under [TS-6](../0149-tenant-scoping-identity/change.md#identity-source-record-ts-6) of [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md). The deployment issuer is the default, and it can be an operator's identity broker. A customer source is a record naming the customer's own issuer, and its `state` is `draft`, `pending_effective`, `active`, `suspended`, or `retired`. A platform admin activates a draft once its proofs pass, and a first activation waits in `pending_effective` until it takes effect. A `suspended` source records its reason, such as an issuer address in an excluded range or a lapsed domain proof, and refuses its subjects until it returns. Retiring a source refuses every token its subjects present from then on, and a `retired` source can return to `active` under the same `source_id`. The tenant record's `deploymentIssuerAllowed` flag states whether the tenant still accepts the deployment issuer, and activating a customer source sets it false. |
| IdP and SCIM | An IdP is an identity provider. SCIM is the directory provisioning protocol through which a customer's directory creates and deactivates Lenny users, served under [TS-8](../0149-tenant-scoping-identity/change.md#scim-server-ts-8). |
| Token introspection | An optional check, set by `identityProvider.introspectionEndpoint` in the tenant's RBAC config (`PUT .../rbac-config`, [§10.6](../../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model)), in which the gateway sends each bearer to that endpoint and rejects it on `active: false`. A tenant with a customer source refuses `introspectionEnabled` on every source kind, and activation of a customer source refuses while it is on, under [`0149-tenant-scoping-identity`, "Directory groups (TS-9)"](../0149-tenant-scoping-identity/change.md#directory-groups-ts-9). The rules of this design under decision 23 therefore apply to a tenant on the deployment source. |
| Audit chain | A tenant's rows in `audit_log`, linked into a hash chain under [§11.7](../../spec/11_policy-and-controls.md#117-audit-logging) that starts from a per-tenant `genesis_nonce`, with each row carrying the previous row's hash (`prev_hash`). A platform admin's actions are recorded in the platform tenant's chain. |
| CMP-058 | The §11.7 rule "Platform-tenant audit event residency", which writes a platform-tenant event that names a target tenant to that tenant's regional platform Postgres. |
| SIEM and OCSF | A SIEM (security information and event management system) is where a security team collects records. Lenny translates audit records into OCSF, the Open Cybersecurity Schema Framework, for delivery. |
| Event subscriptions | The webhooks of [§25.5](../../spec/25_agent-operability.md#255-operational-event-stream), stored in `ops_event_subscriptions`, which notify a tenant of operational events. A delivery is retried three times and is not replayed, so a subscription is a notification path. |
| Opt-in and the opt-in write path | An opt-in is a tenant setting that loosens egress or credential policy, such as a credential binding, a route, or a tenant-registered endpoint. [EG-9](../0127-egress-and-git/steps.md#steps) (egress policy objects) gives every opt-in one write path, which applies a tenant policy check that it leaves empty for this design, and emits an audit event. |
| Gate | A per-tenant grant a platform admin gives, one per risk class, before a tenant can opt in to that class, such as `agentReadableDelivery`, `userCredentialDelivery`, `storedTokenPush`, or `tenantDerivedRuntimes`. |
| `suspendedBy` | A field on every opt-in object, from [`0127-egress-and-git`, "Reserved for other designs"](../0127-egress-and-git/change.md#reserved-for-other-designs). It names what suspended the object: a guardrail of this design, a withdrawn gate, a credential pin, or a VCS pool. When it is set, the effective policy, the Token Service, `netagent`, and the decision service treat the object as absent, except that a running session's qualification under `allowRuntimeHeldCredentials` still counts a suspended binding. The effective-policy read names the layer that suspended it. |
| Credential binding and `delivery` | An egress-design object that gives sessions a secret for named `hosts`. Its `delivery` field says how: under `inject`, `netagent` attaches the secret to requests and the runtime never holds it; under `file`, `netagent` writes it to a file the runtime reads; and `federation` and `metadata` deliver cloud credentials. |
| `netagent` | A container in every agent pod that uses the local egress endpoint, which acts as the pod's egress proxy. It runs under its own UID, holds the pod's service account tokens for egress, terminates TLS (Transport Layer Security) for the hosts it must inspect, attaches credentials, runs the git path, and applies request rules. |
| Token Service | The Lenny service that stores credentials and mints or releases tokens for pods ([§4.3](../../spec/04_system-components.md#43-token-service)). |
| Decision service and the mint checks | `lenny-egress-authz` decides whether each connection is allowed, from the session's effective policy. The mint checks are the two independent checks on every mint that [EG-2](../0127-egress-and-git/steps.md#steps) (the pod network agent and proxied egress) adds, under [`0127-egress-and-git`, "Egress decision service"](../0127-egress-and-git/change.md#egress-decision-service-lenny-egress-authz), **Credential requests**: the Token Service's own check of the pod, its bindings, and its writable set, and the scope decision it asks of the decision service. Each reads its own stores, and a token is minted only when both allow it. The egress design reserves the word "gate" for the per-tenant grants in the "Gate" row above. |
| Pod credential API | The gRPC API on which `netagent` asks the Token Service for a binding's token. [`0127-egress-and-git`, "Interfaces"](../0127-egress-and-git/change.md#interfaces) defines it as I8. |
| Pod binding API and the binding push | The pod binding API is the gRPC API on which `netagent` subscribes to its bindings from one decision service replica. [`0127-egress-and-git`, "Interfaces"](../0127-egress-and-git/change.md#interfaces) defines it as I7. The binding push is the gateway's call to every `lenny-egress-authz` replica after a binding change commits, under [`0127-egress-and-git`, "The `netagent` container"](../0127-egress-and-git/change.md#the-netagent-container), **Binding.** The push is a hint: each replica re-reads the session row, and `netagent` then receives the change through its subscription on the pod binding API. A replica that missed the push picks up the change at its bounded re-read. The binding push is distinct from session bind, the moment the gateway attaches a session to a claimed pod. |
| Policy version and definition version | Counters the egress design keeps so a change reaches decisions and pods at once. Every write to a tenant policy, host set, route, overlay, or binding increments the policy version, which the decision service's cache key includes. Each credential binding also carries a definition version, which an edit increments and which the binding push carries. |
| Host set and route | A host set is a named, reusable list of host patterns and ports, such as `github`, `pypi`, or `npm`, that pool ceilings and tenant policies reference. A platform host set is one the platform ships or a platform admin writes. A route sends traffic for matching hosts to a target: `direct`, an upstream proxy, a registered private endpoint, or an outbound connector. |
| Tenant pool overlay, client config files, and overlay file templates | A tenant pool overlay is a record per pool and tenant, written by the tenant admin, that narrows or extends a shared pool's egress and git settings for that tenant. Tenant client config files are tool configuration files, such as an `.npmrc` or a `pip.conf`, that `netagent` writes into the pod verbatim when the pod is bound to the tenant. Overlay file templates are the client proxy profile file templates a tenant adds through an overlay. Both need the `verbatimClientFiles` gate, because each places arbitrary content in the pod. |
| Egress group | An additional `lenny-egress` Deployment, assigned to one tenant, whose traffic leaves the cluster from a dedicated address. |
| Outbound connector and outbound host | Under [`0148-tenant-outbound-connector`](../0148-tenant-outbound-connector/change.md), an outbound connector is a tenant's registration of an agent the customer runs inside its own network, through which a session reaches a private host. An outbound host is a tenant's registration of a private host name that routes to one of its connectors. A tenant admin writes both through the opt-in write path. |
| Interceptor and tenant-scoped interceptor | An interceptor is a service the gateway calls at a named phase of request handling, such as `PreRoute` before it settles a session's runtime, or the phases that carry LLM, tool, delegation, and output payloads ([§4.8](../../spec/04_system-components.md#48-gateway-policy-engine)). A `platform-admin` registers one through `/v1/admin/interceptors` with its phases, priority, `failPolicy`, and the in-cluster Service the gateway dials. Under [RT-6](../0141-runtime-routing/change.md#rt-6-tenant-scoped-interceptors) of [`0141-runtime-routing`](../0141-runtime-routing/change.md), a registration may list tenants, and the gateway then calls it only for those tenants' requests. That is a tenant-scoped interceptor. |
| Writable set | The repositories a session may push to, which the gateway records at session bind under [EG-3](../0127-egress-and-git/steps.md#steps). Each entry has a `state`: `granted` admits a push, and `pendingApproval`, which SO-5b adds, does not. A client names extra writable repositories in the session's `writableRepositories` field. |
| Pool | A pool keeps warm agent pods for a Runtime, the registered definition of a runtime image and its settings ([§5.2](../../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). A session runs on a pod claimed from a pool. |
| Plan and session bind | A session's plan is what its create request states the session needs: its workspace plan, such as `gitClone` sources, the credential bindings it names, and settings such as `egress.allowedHosts`. The gateway checks the plan at create. At session bind, the gateway attaches the session and its bindings to a claimed pod and records its writable set. |
| VCS pool and its settings | A VCS (version control system) pool is a credential pool whose provider serves git hosts, which is a separate object from a pool of pods. It names repository prefixes, a repository allowlist, and a provider credential such as a GitHub App installation. Under the VCS layer of [`0152-user-git-identity`](../0152-user-git-identity/change.md), `userAuthorization: required` makes each repository need the user's own access at the provider, `serviceIdentities` lists the automated subjects that skip that check, `prAuthoring: user` has pull requests authored as the user, `allowRuntimeHeldCredentials` admits a binding with `delivery: file` on the pool's covered hosts, and `pinHosts` pins those hosts. `allowStoredTokenPush` lets a `stored` provider's token push, and it also governs GitLab token reuse, the reuse of one GitLab project access token per tenant, pool, project, and scope. `allowedRefPatterns`, on a VCS pool and on a session's `writableRepositories` entry, limits which refs a session can push. |
| `mint-scoped` | A provider capability level of the egress design: the provider can mint a token limited to named repositories. The other levels, `mint-coarse` and `stored`, cannot. |
| User credential type and `userCredential` binding | From [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md). A user credential type is a tenant object naming a kind of per-user token, its source (`registered`, written by a tenant backend, or `oauth2Link`, linked by the user), and the hosts where it works, called its type hosts. A `userCredential` binding names a type, so each session receives its own user's token. |
| Link provider and `identityBinding` | A link provider is a tenant object naming a provider kind and the tenant's OAuth2 client, through which a user links an account. Its `identityBinding` decides how the provider account is tied to the Lenny user, and `unverified` records that nothing ties them. |
| Narrowing template | Data on a link provider describing how to derive a per-session token from the stored one, such as `github-scoped`. A platform-shipped template that sets `followsEntryState` limits the token to writable set entries in state `granted`. |
| Request rules and preset | Request rules are data on a binding with `delivery: inject` that refuse requests before the token is attached. A preset is a named, versioned set of request rules, such as `github-deny`. |
| Token trust record | The one record of everything that decides where a stored user token can go and who can read it: its endpoints, hosts, routes, CAs, `attach` template, narrowing template, provider configuration block, and delivery ceiling. Each stored token carries the record's digest, and any change to the record makes the token unusable. |
| Effective policy | The hosts and bindings a session may use, which the decision service computes from pool, tenant, and session settings. |
| Shadow policy | A candidate tenant egress policy that the decision service evaluates beside the active one and records, without changing any outcome ([`0127-egress-and-git` decision 42](../0127-egress-and-git/decisions.md#decisions)). |
| Level 1 and level 2 | Ways a deployer can replace `lenny-egress`, the central egress proxy, with its own. A level 2 proxy calls the decision service for each connection. A level 1 proxy enforces a host list that Lenny publishes and makes no call per connection. |
| `egressProfile` | A pool setting: `restricted`, `proxied`, or `internet`. Under `internet`, the pod also has raw sockets that bypass `netagent` and `lenny-egress`. |
| Evidence streams | Append-only records the egress design defines, such as `egress.decision` for each connection decision and `egress.local` for refusals `netagent` makes itself, held by `lenny-evidence` and read under per-stream permissions `evidence.read.<stream>`. A record's `provenance` states whether a component enforced the outcome or only reported it (`self_reported`), and `payloadKeyRef` names the key that encrypts a sensitive payload. |
| Derived runtime | A runtime a tenant admin defines on top of a platform runtime, with default workspace files, setup commands, and git clones, under [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/change.md). |
| Derive and `content_stop_epochs` | A derive starts a new session from an earlier session's plan, under [`0144-session-derive`](../0144-session-derive/change.md). Its tenant-keyed `content_stop_epochs` counters refuse a derive of a plan bound before a tightening that stopped content. |
| Cross-environment sharing | Under [TS-3](../0149-tenant-scoping-identity/change.md#ts-3-session-environment-context-and-cross-environment-sharing), the tenant setting `dangerouslyAllowCrossEnvironmentSharing` and `dangerouslyShare` declarations let a child that `session.create` starts run in another environment. A sharing path is a pair of `dangerouslyShare` entries, outbound in one environment and inbound in another. A crossing is a `session.create` over a sharing path, and the crossing mark is the child's `crossed_from_environment` column, which stops a second crossing. TS-3 owns crossing for every operation, including the release of a crossed child (decision 41 of [`0166-delegation-primitives`](../0166-delegation-primitives/decisions.md#decisions)). |
| Delegation | A session that holds a session grant creates child sessions with the platform operation `session.create`, which replaces `lenny/delegate_task` and its successor `start_child_session` of [`0135-message-delivery`, "MD-23"](../0135-message-delivery/change.md), and these form a tree under a root session ([`0166-delegation-primitives`, "DP-1"](../0166-delegation-primitives/change.md#dp-1-session-grant)). The grant's caveats record what a child receives from its parent. A session leaves its parent with `session.transfer` or `session.release` ("DP-2"), and a parent offers its children tools of its own ("DP-8"). The reference tools that a model calls, such as `start_session`, run in the reference MCP bridge and reach the gateway as these operations ("DP-5"). |
| Content caveat | The restriction every session carries under 0166 "DP-1": `contentPolicy` (`interceptorRef`, `maxInputSize`, `scanExportedFiles`, and `maxExportedFileSize`) and the export and import limits, resolved from the platform, the tenant, the environment, and the runtime with the strictest value winning. Every file that moves between two sessions passes the sender's content caveat on export and the receiver's on import (0166 decision 45). |
| Adapter and tool paths | The adapter is the Lenny process in the agent pod that connects the runtime to a gateway replica over `LNK-GWCONTROL`, the gRPC connection from each pod process to one replica. A runtime calls a platform tool by sending a `tool_call` frame that names its server, `platform`, on its runtime connection or on a tool connection of its slot, and the adapter forwards the call to the gateway over `LNK-GWCONTROL` for the session of that slot ([RP-3](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames)). The adapter-local tools of earlier revisions are retired (RP D5). Connector tools, which revisions before 56 also named here with `connector:<id>`, are removed by [`0124-connector-removal`](../0124-connector-removal/change.md). A harness reaches an operator's MCP service over HTTP through `netagent`, and those calls reach no Lenny tool path. |
| Tenant security settings | The operator-owned `securitySettings` on the tenant record of [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) (TSS-1): the tenant key, deadlines, retention, and the operator's placement settings, versioned in `tenant_security_settings_versions`. They sit beside the tenant security record of SO-2, which the tenant's security team writes (TSS O13). |
| Placement requirement | A filter on the pools a session may run on, such as `requireNodeSeparation`, `nodeTenancy: dedicated`, or `allowSharedPod: false`, which narrows pool selection and never widens it ([PM-4](../0136-placement/change.md#pm-4-placement-requirements)). An unmet requirement refuses the session with `PLACEMENT_REQUIREMENT_UNMET`. |
| Data classes | The classes of §12.9: Public, Internal, Confidential, and Secret, formerly T1 to T4 ([TSS-8](../0150-tenant-security-settings/change.md#tss-8-data-classes)). |
| `lenny-ops` and `doctor --fix` | `lenny-ops` is Lenny's operations service, which runs diagnostics and their remedies. `lenny-ctl doctor --fix` applies the remedies, among them `certManagerExpiring`, which renews an expiring certificate. |
| cert-manager and Certificate | cert-manager is the Kubernetes controller that issues Lenny's TLS certificates from Certificate objects. A leaf is an end-entity certificate, and a CA Certificate (`isCA: true`) signs others. |
| F-n.n.n | A finding filed in `BUILD-GAPS.md` at the repository root, numbered after the specification section it concerns. |
| Kind | Kubernetes in Docker, the local cluster on which tier 5 tests run. |

### Labels from other designs

Proposals in other designs carry a label whose prefix names the design. This document cites them by label and links each citation to its definition. The table names what each cited label stands for.

| Labels | Design | What they name |
| :-- | :-- | :-- |
| EG-2, EG-3, EG-4, EG-7, EG-8, EG-9, EG-10, EG-11, and EG-12 | [`0127-egress-and-git`, "Steps"](../0127-egress-and-git/steps.md#steps) | Proposals of the egress design: EG-2 the pod network agent and proxied egress, EG-3 the pod git path, EG-4 credential delivery, EG-7 level 1 replacement, EG-8 tenant visibility, EG-9 egress policy objects with the single write path for tenant opt-ins, EG-10 evidence ingest, EG-11 the egress PKI (public key infrastructure), and EG-12 exfiltration controls. |
| UC-1, UC-2, UC-4, and UC-5 | [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) | Proposals of the user credential design: UC-1 credential types and the `registered` source, UC-2 the `oauth2Link` source and account linking, UC-4 request rules and presets, and UC-5 `delivery: file`. |
| UG-2 and UG-3 | [`0152-user-git-identity`](../0152-user-git-identity/change.md) | Proposals of the VCS layer: UG-2 repository authorization and UG-3 pull request authoring. |
| TS-2, TS-3, TS-4, TS-6, TS-8, and TS-9 | [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md) | TS-2 the identity model, TS-3 session environment context and cross-environment sharing, TS-4 user lifecycle, TS-6 identity source records and browser login, TS-8 the SCIM server, and [TS-9](../0149-tenant-scoping-identity/change.md#directory-groups-ts-9) directory groups. |
| RT-6 | [`0141-runtime-routing`](../0141-runtime-routing/change.md#rt-6-tenant-scoped-interceptors) | Tenant-scoped interceptor registrations. |
| CA-10 | [`0123-connector-authorization-and-external-memory`](../0123-connector-authorization-and-external-memory/change.md#ca-10-removing-built-in-memory) | The removal of Lenny's built-in memory, after which a deployer reaches memory through an external service over egress, as restated in that design's revision 17 after [`0124-connector-removal`](../0124-connector-removal/change.md). |
| FE-1, FE-3, and FE-6 | [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/change.md) | FE-1 quarantine, FE-3 platform-only holds and a security retention class, and FE-6 agent activity streams. |
| OC-2 | [`0148-tenant-outbound-connector`, "Proposals"](../0148-tenant-outbound-connector/steps.md#steps) | The outbound connector's hosts and policy, through which a session reaches a private host. |
| PM-2, PM-3, PM-4, and PM-8 | [`0136-placement`](../0136-placement/change.md) | [PM-2](../0136-placement/change.md#pm-2-node-tenancy) node tenancy, [PM-3](../0136-placement/change.md#pm-3-placement-offerings-and-per-pool-nodeplacement) placement offerings and their grants, [PM-4](../0136-placement/change.md#pm-4-placement-requirements) placement requirements and the fields `sessionIsolationLevel` reports, and [PM-8](../0136-placement/change.md#pm-8-system-components) the namespace, replicas, and priority of each system component. |
| TSS-1, TSS-3, TSS-7, and TSS-8 | [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) | [TSS-1](../0150-tenant-security-settings/change.md#tss-1-settings-model) the settings model, [TSS-3](../0150-tenant-security-settings/change.md#tss-3-admin-api) its admin API, view, and audit events, [TSS-7](../0150-tenant-security-settings/change.md#tss-7-changing-a-setting-on-a-live-tenant) relaxation on a live tenant, and [TSS-8](../0150-tenant-security-settings/change.md#tss-8-data-classes) the data classes. |
| RP-3 and RP D5 | [`0139-runtime-protocol`](../0139-runtime-protocol/change.md) | [RP-3](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames) tool calls on `tool_call` frames, and the owner's decision D5 that retires the adapter-local tools. |
| DP-1, DP-2, DP-5, DP-8, and decisions 31, 36, 37, 41, 42, and 45 of 0166 | [`0166-delegation-primitives`](../0166-delegation-primitives/change.md#dp-7-what-each-change-takes-on) | The session grant, its caveats, the root grant inputs, the content caveat, and the platform operations such as `session.create`, `session.set_limits`, `session.transfer`, `session.release`, and `files.import` (DP-1); the two ways a session leaves its parent and the caveats `leave` and `leaveMaxDepth` (DP-2); the reference tools such as `start_session`, which never reach the gateway (DP-5); and the tools a parent offers its child, which reach the parent as `tool_call` child events, with the caveat `offerTools` and fenced children (DP-8). Decision 36 moves the approval of a child into SO-5a, decision 31 adds rules for parent tools, decision 37 makes the security-tier delegation switches fields of this record, decision 41 gives crossing to TS-3, decision 42 audits a parent tool's call without arguments, and decision 45 gives every session a content caveat and extends this design's cooldown to every transfer path. |
| D5 of 0126 | [`0126-delegation-availability`](../0126-delegation-availability/change.md) | The owner's decision that a tenant forbids delegation through the guardrail `"delegation": "forbid"` in this record. |
| MD-19 and MDR-22 | [`0135-message-delivery`](../0135-message-delivery/change.md) | MD-19 harness approvals, which a runtime forwards with a `request` frame of `kind: "approval"`, and MDR-22, the requirement that a session's frame reader never blocks on an approval. |
| L29 and SLR-27 | [`0146-session-lifecycle`](../0146-session-lifecycle/change.md) | The withdrawal of a pending approval when a suspension stops the session's turn. |
| CTF-1 and CTF-2 | [`0122-client-tools`](../0122-client-tools/change.md) | Defects of the relayed `tool_call` path: a relayed call that is not gated is discarded with no result, and an approval re-sends the runtime's own `tool_call` to the runtime. |
| LU-8 and LUR-14 | [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) | The posture `llm` section, and the report of the other tenants that share an upstream. |

## High-level design

Lenny hosts several customers, called tenants, on one deployment that an operator runs. A customer's security team has to approve the service and keep watching it, separately from the customer's own administrator. Today Lenny gives that team no place: the tenant admin is the top customer role and can remove anyone watching, nothing limits the settings the tenant admin turns on, and the customer cannot see what the operator's staff did in its tenant. This design gives each customer a security team inside Lenny that the tenant admin cannot remove. The team reads audit records and evidence without reading session content, sets rules that the tenant admin's configuration has to follow, receives its own copy of the audit trail at its own SIEM, sees every operator access to its tenant, and can require its approval before an operator acts as one of its users, before a session runs a sensitive tool, or before a session pushes to a protected repository. A change that loosens those rules takes effect only after a delay, so the rest of the team can cancel a change made with a stolen credential. The design also fixes two defects in the operator's tooling that its review found.

The main limit is that these controls bind people who act through Lenny's APIs. An operator with direct rights over the cluster or the database can go around them. The customer's own SIEM copy shows later tampering with records it already holds, and it shows nothing of access that leaves no record. The security report a customer reads is also the platform's statement about itself.

The rest of this section states each part in the design's terms, which "Terms" above defines.

**Security roles (SO-1).** The `security-auditor`, `security-approver`, and `security-admin` roles live in their own table, `tenant_security_principals`, which no user or role route of the tenant admin writes. The role resolver adds a security role only for a `user_bearer` issued after the row took effect. A tenant admin cannot invalidate, disable, delete, or erase a subject that holds a security role, and a principal's bearer never reaches a token introspection endpoint. A platform admin assigns only `security-admin`, and only when the tenant has none active.

**Tenant security record and posture (SO-2).** One versioned record per tenant holds every guardrail, and a `security-admin` writes it. The opt-in write path of [EG-9](../0127-egress-and-git/steps.md#steps) checks each tenant admin opt-in against the active version. A tightening applies at once and suspends the existing objects it makes non-compliant, apart from the exemptions it names. A weakening waits out the weakening delay, during which another `security-admin` can cancel it. The posture read reports the tenant's security-relevant settings and marks each value known or unknown.

**Tenant SIEM destinations (SO-3).** A new `lenny-siem-forwarder` Deployment delivers a tenant's audit chain and chosen evidence streams to destinations the tenant's `security-admin` registers. Each destination has its own lease and budget, so a failing destination delays only its own records, and a gap record marks any records lost. An active destination with audit included satisfies the independent audit copy a regulated tenant needs.

**Operator access (SO-4).** Impersonations, and the platform admin writes and reads that name a tenant, are mirrored into that tenant's own audit chain under an operator reference, apart from the exceptions the SO-4a table lists (SO-4a). An impersonation bearer carries `impersonated_by`, so the customer can filter the actions an operator took as one of its users. A tenant can require its approval before an impersonation or a platform admin's read of session content, with an emergency path that the tenant can forbid (SO-4b).

**Delegated approvers (SO-5).** A `security_approvals` store and an approvals API let a `security-approver` resolve approvals. A platform tool call that matches a rule is held at the gateway until approval (SO-5a), and a push to a repository that matches a rule receives no write token until approval (SO-5b).

**Operator tooling (SO-6 and SO-7).** `doctor --fix` judges each certificate against its own renewal window, re-issues through cert-manager's `Issuing` status condition, and never changes a CA (SO-6). `lenny-ops` loses its rights over Secrets and its `patch` on Certificates, and §25.4 states why it remains inside the PKI trust boundary (SO-7).

```
 security-admin ──(guardrails; weakenings wait out the delay)──> tenant security record
                                                                          │ checked on every opt-in write
 tenant-admin ──(opt-in writes)──> EG-9 write path ───────────────────────┴──> Token Service, decision service, netagent

 platform-admin ──(impersonation, admin reads and writes)──> mirror ──> tenant audit chain ──> lenny-siem-forwarder ──> customer SIEM
 platform-admin ──(operator access request)──┐
 session ──(held tool call, pending push)────┴──> security_approvals <──(approve or deny)── security-approver
```

## Requirements

- SOR-1. A customer's security team reads audit events, evidence, posture, and subscriptions for its tenant with no access to session content by default.
- SOR-2. A tenant admin cannot remove, disable, lock out, or rewrite that access through any Lenny API, including the tenant's authentication settings.
- SOR-3. A customer's security team can forbid specific opt-ins and host patterns, and the tenant admin cannot override that. Weakening a constraint is recorded, is justified, and takes effect only after a delay.
- SOR-4. A posture read reports, per tenant, the settings a security review asks about, and reports an unknown value as unknown.
- SOR-5. A customer can send its own audit and evidence records to its own destination. A failing destination affects no other tenant and no request path.
- SOR-6. A customer sees operator access to its tenant in its own audit chain and SIEM, and can require its approval before an operator impersonates a user.
- SOR-7. A person other than the session owner can resolve an approval when the tenant's policy says so, and a protected repository can require approval before a session pushes to it.
- SOR-8. `doctor --fix` never rotates a CA, and the health API reports a healthy install with short-lived certificates as healthy.
- SOR-9. `lenny-ops` has no standing read of Secrets through the API, and §25.4 states which of its remaining rights keep it inside the PKI's trust boundary.
- SOR-10. A tenant with no security principal and an empty security record behaves as it does today.
- SOR-11. A customer's security team can require node separation, dedicated node tenancy, and one session per pod for its tenant's sessions, and removing such a requirement is a weakening.
- SOR-12. A relaxation of the operator's tenant security settings takes the tenant security record's delay, and the tenant's security team can see and cancel it before it takes effect.

## Security properties

Each property names the component that holds it.

- SP1. Only an active row in `tenant_security_principals`, presented on a `user_bearer` issued after the row took effect, grants a security role, unless a platform admin has set the tenant's role source to `oidc`. The role resolver in the gateway and in `lenny-ops` removes `security-*` from token claims and from user-row roles before it adds these rows, and the user routes and custom-role routes refuse those names.
- SP2. No tenant-admin or custom-role caller can change a security principal, or invalidate, disable, delete, or erase a subject that holds a security role, and no tenant-admin endpoint receives a principal's bearer or decides its validity. The security principal handlers, the user handlers, and the auth middleware's introspection step enforce this.
- SP3. The write permissions of the security roles sit outside `TenantAdminPermissions()`, so the §10.2 custom-role ceiling keeps them out of any custom role. Custom-role validation (`IsTenantAdminPermission`) enforces this.
- SP4. Every opt-in write passes the guardrail check, and an object a tightening makes non-compliant stops taking effect at every enforcement point. The opt-in write path of [EG-9](../0127-egress-and-git/steps.md#steps), the Token Service, and the decision service enforce this.
- SP5. A weakening has no effect before its `effectiveAt`, and the delay cannot be shortened through the admin API. Weakenings include removing a security principal, adding a `security-admin` or `security-approver`, and creating or repointing a SIEM destination, so a stolen `security-admin` token cannot remove the peers who would cancel it. The security record's applier enforces this.
- SP6. A failing tenant SIEM destination delays only its own records, and never blocks an audit write, a session, a gateway start, or another destination. `lenny-siem-forwarder` holds this through per-destination leases and budgets ("Component" under SO-3), the retention hold acts on that tenant's rows only ("Retention hold"), and the gateway's startup check reads no destination state ("Compliance gate").
- SP7. A tenant destination's records are complete or show a detectable gap, through chain fields and a gap record: each record carries the hash of the record before it, a stream anchor opens the stream, and a `siem.delivery_gap` record names any lost range ("Delivery" under SO-3). The forwarder's cursor and hold rule hold this.
- SP8. An impersonation bearer is not issued until the customer-visible record is durable, and every audit row written under that bearer names the operator reference. The impersonation service and the audit write path enforce this.
- SP9. With approval required, the gateway issues an impersonation bearer only against a matching approved request, or through the emergency path the tenant allows. The impersonation service enforces this.
- SP10. No principal approves its own request. An impersonation bearer resolves no approval and holds no security role, because the resolver adds no security row for a token carrying `impersonated_by`. A platform admin cannot assign `security-approver`. The approval handlers and the resolver enforce this.
- SP11. A repository marked for approval receives no write token until its approval exists, including a rule added while a session is running, enforced by the Token Service and `lenny-egress-authz` through the mint checks of [EG-2](../0127-egress-and-git/steps.md#steps), which read the writable set that [EG-3](../0127-egress-and-git/steps.md#steps) (the pod git path) records. This covers git and tenant credential bindings on that host's API. It does not cover a credential the agent brings itself (see "Residual risks").

SP8 to SP10 bind operators who use the Lenny admin API. An operator with cluster or database rights can go around them (see "Residual risks").

## Detailed design

### SO-1: security roles

#### Roles

The design adds these built-in, tenant-scoped roles in a separate `auth.SecurityRoles()` enum. They stay out of `auth.AllRoles()`, because `validateRoleNames` accepts anything `Role.IsValid()` accepts, and `handlePutTenantUserRole` would then let a tenant admin assign `security-admin` to herself:

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
| Manage security principals | `security-admin` only, when the tenant has none active, with a justification | No | No | No | No | Own |
| Write the tenant security record | Yes, with justification and the same delay | No | No | No | No | Own |
| Manage SIEM destinations | Yes, with justification | Read | No | Read | No | Own |
| Create a subscription owned by the security team | Yes | No | No | No | No | Own |
| Resolve an approval | No | No | No | No | Own | No |

In the matrix, raw canonical audit tuples are the pre-OCSF rows Lenny hashed, which a chain auditor reads with `?format=raw-canonical`, and "With scope" means the caller's token also needs the `audit:raw-canonical:read` scope (§25.9). Legal holds are holds that keep a tenant's data from deletion or erasure while a legal obligation lasts (§12.8). A subject can hold a security role and any other role. Posture lists subjects who hold both `tenant-admin` and `security-admin` (see decision 4).

#### Data model

```sql
CREATE TABLE tenant_security_principals (
    tenant_id     TEXT NOT NULL,
    user_id       TEXT NOT NULL,
    role          TEXT NOT NULL,   -- security-auditor | security-approver | security-admin
    assigned_by   TEXT NOT NULL,
    assigned_at   TIMESTAMPTZ NOT NULL,
    expires_at    TIMESTAMPTZ,
    effective_at  TIMESTAMPTZ NOT NULL,
    state         TEXT NOT NULL,   -- pending | active | removal_pending
    removal_effective_at TIMESTAMPTZ,
    justification TEXT NOT NULL,
    ticket_id     TEXT,
    PRIMARY KEY (tenant_id, user_id, role)
);
```

The table is separate from the user row because `handlePutTenantUserRole` replaces `u.Roles` with a single role and `handleDeleteTenantUserRole` clears it. A row in its own table cannot be overwritten by those paths or by `PUT /v1/admin/users/{userId}`, so the protection does not depend on guarding every user write. The `PlatformRoles` resolver (`userstore/platformroles.go`, and `cmd/lenny-ops/platformroles.go` for `lenny-ops`) reads active rows in the same lookup it makes today, removes any `security-*` value from the claim and from the user row, and adds the rows only for a token with `typ: user_bearer` and an `iat` at or after the row's `effective_at`. A `session_capability`, `a2a_delegation`, or `service_token` bearer of the same subject gains no security role. The `iat` rule means a principal signs in again after activation, and a bearer captured earlier never gains the role. The resolver also adds no security row for a subject that holds any invalidation level or hold of [TS-4](../0149-tenant-scoping-identity/change.md#user-lifecycle-ts-4), so a disabled principal with a live bearer holds no security role, and such a principal does not count as active. Today's resolver, `ResolveRoles(ctx, tenantID, subject)`, reads no token claim, so it is widened to read `typ`, `iat`, and `impersonated_by`. The table has row-level security and a `lenny_tenant_guard` trigger as other tenant tables do, and §12.8 and §12.9 classify it.

The tenant record gains `securityRoleSource`, `managed` by default or `oidc`, and only a platform admin writes it. A change to `oidc` is a weakening: it takes the delay, and a `security-admin` can cancel it, because an operator-run broker would otherwise issue a `security-approver` claim that approves the operator's own request. A change to `managed` applies at once. Under `managed`, a `security-*` value in the roles claim is ignored and counted in `lenny_security_role_claim_ignored_total`.

#### API

| Method and path | Caller | Behaviour |
| :-- | :-- | :-- |
| `GET /v1/admin/tenants/{id}/security-principals` | `platform-admin`, `tenant-admin`, `security-auditor`, `security-admin` | Lists rows and each subject's last authentication time. A tenant admin can see who watches, because concealment would not stop a tenant admin and would complicate offboarding. |
| `PUT /v1/admin/tenants/{id}/security-principals/{userId}/{role}` | `security-admin`, or `platform-admin` under the limit below | Body: `{"justification", "ticketId", "expiresAt"}`. `justification` is required from every caller. `ticketId` is optional, for an operator whose own process uses tickets, and is recorded when given. On an existing row, setting or shortening `expiresAt` counts as a removal: it takes the delay and the `LAST_SECURITY_ADMIN` check. Emits `security.principal_assigned`, and `security.principal_activated` when the row takes effect. |
| `DELETE /v1/admin/tenants/{id}/security-principals/{userId}/{role}` | `security-admin`, or `platform-admin` under the limit below | Body: `{"justification", "ticketId"}`. The row moves to `removal_pending` and keeps its authority until the delay ends. A request that would leave no `security-admin` returns `409 LAST_SECURITY_ADMIN` to a `security-admin`. Emits `security.principal_removal_requested`, and `security.principal_removed` at activation. |
| `POST /v1/admin/tenants/{id}/security-principals/pending/{userId}/{role}/cancel` | Another `security-admin`, or `platform-admin` with a justification | Cancels a pending assignment or removal. Emits `security.principal_change_cancelled`. |
| `POST /v1/admin/tenants/{id}/security-principals/pending/{userId}/{role}/object` | `tenant-admin` | Applies only to a pending assignment a platform admin wrote. It records the objection and stops activation. A second platform admin overrides it by repeating the `PUT` with its own justification, and the row then activates at its original `effective_at` or at once if that has passed. Both steps are mirrored. |

**Timing.** Assigning `security-auditor` applies at once. Assigning `security-admin` or `security-approver`, and every removal, is a weakening under SO-2's delay, because a stolen token can otherwise remove the people who would respond. Before the tenant has a security record, the floor applies. The event reaches subscriptions and SIEM destinations at the request.

**Second platform admin.** The override compares the person behind each bearer, `impersonated_by` when present and `sub` otherwise, against the assignment's author, and it is refused from a subject that was created or given `platform-admin` after the assignment was written. Every security-principal and security-config route refuses an impersonation bearer. An operator that controls the deployment issuer can still mint a second subject, which "Residual risks" states.

**Platform admin limit.** A platform admin assigns only `security-admin`, and only when the tenant has no active `security-admin`. It assigns no other security role. The tenant's first assignment applies at once, against the customer's recorded request, and any later one takes the delay and can be cancelled by a `security-admin` or objected to by a tenant admin. An objection is not a veto, because a tenant admin who disabled the last `security-admin` in the IdP would otherwise block recovery indefinitely. A platform admin's removal takes the delay and needs a justification. Without this limit, a platform admin can give an operator identity `security-approver` and approve the operator's own access request, and the self-approval check does not see it, because the two subjects differ.

**Compromise response.** A compromised principal is stopped at once by a platform admin's `POST /v1/admin/users/{userId}/invalidate`, which revokes its tokens under §11.4, and its row is then removed under the delay. A principal invalidated in that way does not count as active. A row whose subject's identity source is not active, such as a retired source, does count as active for approval and for the platform admin limit above, so a retirement cannot remove the approver by locking it out. [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md) states the delay on a platform admin's retirement. Under that design, a platform admin activates a customer's identity source once its proofs pass, retiring a source refuses every token its subjects present, and the tenant record's `deploymentIssuerAllowed` flag states whether the tenant still accepts the deployment issuer. Activating an identity source or changing `deploymentIssuerAllowed` takes the delay whenever the tenant has a `security-admin` row in any state, so invalidating the security team does not remove the delay. A retirement after invalidation stays immediate, because it is reversible and is the incident path. A customer that has permanently lost its issuer cannot approve or cancel, so the platform admin removes those rows under the delay and assigns a new `security-admin` against the customer's recorded request.

An expired row stops granting the role at its `expires_at`, and a sweep deletes it and emits `security.principal_expired`. `SecurityPrincipalCoverageLost` warns 7 days before the last `security-admin` row expires, and it fires when the last such row's subject has no active identity source, because that subject cannot authenticate.

#### Enforcement points

- **Audit reads.** `requireAuditReader` and `auditTenant` move from role names to an `audit.read` permission. `POST /v1/admin/audit-events/{seq}/retranslate` and `/republish` use `requireAuditReader` today, so they get a separate `audit.recover` permission that `security-auditor` does not hold. Without this split the auditor would gain two write routes.
- **Evidence.** The named evidence permissions of [EG-8](../0127-egress-and-git/steps.md#steps) (tenant visibility) and [EG-9](../0127-egress-and-git/steps.md#steps) (egress policy objects) gain grants for the new roles, with no handler change. A later sensitive stream, such as the `agent.activity` stream of [FE-6](../0128-forensics-evidence-preservation/change.md#fe-6-agent-activity-streams) (agent activity streams), gets no default grant to any security role (see decision 19).
- **`lenny-ops`.** §25.4 Authentication requires `platform-admin` or `tenant-admin` on every endpoint. The role ceiling admits the security roles on the read endpoints named in the matrix, under the same tenant scoping as `tenant-admin`.
- **Role names.** `POST` and `PUT` on `/v1/admin/users`, the §15.1 role routes, and the bootstrap seed refuse a `security-*` value with `400 VALIDATION_ERROR`. `POST /v1/admin/tenants/{id}/roles` refuses a custom role with that prefix with `422 RESERVED_IDENTIFIER`, because a custom role named `security-admin` would otherwise match a role-name comparison.
- **Protected subjects.** `DELETE /v1/admin/users/{userId}`, `POST .../invalidate`, `POST .../erase`, the `POST .../reinstate` route of [TS-4](../0149-tenant-scoping-identity/change.md#user-lifecycle-ts-4) (user lifecycle), a `PUT /v1/admin/users/{userId}` that disables the user, and a `POST /v1/admin/users` carrying `disabled: true`, which `handleCreateUser` stores, return `403 SECURITY_PRINCIPAL_PROTECTED` to a caller without `security-admin` or `platform-admin` when the target holds a row, and emit `security.principal_protection_refused`. A subject named in the record's `lifecycleSubjects` may also call invalidate and reinstate on a protected subject, so the `identity-sync` worker of [TS-4](../0149-tenant-scoping-identity/change.md#user-lifecycle-ts-4) can carry a directory departure when the customer's security team names it. Holding `manage_user_lifecycle` alone does not admit a caller, because `tenant-admin` holds that permission. The §15.1 role routes stay open to a tenant admin for other roles, because they leave the security row in place.
- **Introspection.** `PUT .../rbac-config` is open to `manage_rbac_config`, and `IntrospectGroups` sends each bearer to `identityProvider.introspectionEndpoint` and rejects it on `active: false`. Today a tenant admin can therefore receive a principal's bearer and lock the principal out. The auth middleware skips introspection for a subject with a row in any state, including `pending`, and that covers an endpoint set before the principal was assigned. A bearer captured before the row existed gains no role, by the `iat` rule above. A principal's groups come from its token, or from Lenny's directory tables under the `group_source: scim` setting of [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md), which sends no bearer anywhere.
- **Tenant authentication settings.** For a tenant with any row, a change to `identityProvider.*` in `PUT .../rbac-config` needs `security.config.write` or `platform-admin`, and otherwise returns `403 SECURITY_CONFIG_PROTECTED`. Setting or repointing the endpoint takes the weakening delay, and `POST .../rbac-config/pending/cancel` cancels it. Clearing the endpoint or turning introspection off stops bearers leaving the platform, so it is a tightening and applies at once. The other fields stay with `manage_rbac_config`. This limits a tenant admin's capture of other users' bearers once a security team exists (decision 23).
- **Subscriptions.** `ops_event_subscriptions` gains `owner_class`, `tenant` by default or `security`. A tenant admin can read and cannot change or delete a `security` subscription.
- **Impersonation.** `impersonation.go` builds `TargetRoles` from the user row, which holds no security role under this design, and the resolver adds security rows by subject. The resolver therefore adds no row for a token carrying `impersonated_by`, so an operator impersonating a security principal holds none of its security authority.
- **Scopes.** The §15.1 scope taxonomy gains a `security` domain, so a narrowed token works as it does for other domains.

#### Failure behaviour

The rows are read in the role lookup that exists today, and a failed lookup fails the request as it does now (`platform_role_lookup_failed`), so no request proceeds on a partial role set. A protected-subject check reads the same table and returns `503` on failure, so a tenant admin's user write fails closed during a database fault. An unknown or expired role has no effect. An auditor read follows §25.9's existing degradation rules, and each query emits `audit.query_executed` as today.

#### Identity source

Lenny defines the roles and their permissions, and it stores the assignments. A tenant's identity source authenticates the subject: the deployment issuer by default, which can be a SaaS operator's identity broker, or a customer's own issuer ([`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md), [TS-2](../0149-tenant-scoping-identity/change.md#identity-model-ts-2-ts-4-and-ts-6-to-ts-9) and [TS-6](../0149-tenant-scoping-identity/change.md#identity-source-record-ts-6)). A token can carry these roles only when the tenant is set to `oidc`, in which case whoever administers the customer's IdP controls them, and SP1 and SP2 hold only as far as that administrator is separate from the tenant admin. For the security roles, this narrows the precedence that [`0149-tenant-scoping-identity`, "When SCIM and token claims disagree"](../0149-tenant-scoping-identity/change.md#when-scim-and-token-claims-disagree) states between a role in a token and a platform-managed row, under which the row wins: under `managed`, the claim is ignored and no user row can carry the role. `managed` does not separate a principal from its issuer. An issuer controls every principal it authenticates: it can assert a principal's subject and choose the `iat` value SP1 reads, and the row then grants the role. On a tenant on the deployment source, an operator's broker can therefore act as a `security-approver` and approve the operator's own request. Posture reports each principal's issuer. A tenant that needs separation from its operator moves to a customer source, and [`0149-tenant-scoping-identity` decision 25](../0149-tenant-scoping-identity/decisions.md#decisions) leaves a second factor out of the first version.

### SO-2: tenant security record and posture

#### The record

One record per tenant holds every customer-set security constraint, so one write path, one version, and one weakening rule serve SO-2, SO-4, and SO-5. An absent record constrains nothing (SOR-10).

```json
{
  "credentialDelivery": { "forbid": ["file", "metadata"] },
  "requestRules": { "requiredPresets": [{ "hosts": "api.github.com", "preset": "github-deny", "minVersion": 7 }] },
  "userAuthorization": { "require": ["github.com/acme/*"], "serviceIdentities": ["~svc~svc-nightly"], "forbidRuntimeHeld": true },
  "identityBinding": { "forbidUnverified": true },
  "tokenTrust": { "allowedRecords": [{ "digest": "sha256:...", "record": { "...": "the record content a person reviews" } }] },
  "storedTokenPush": "forbid",
  "verbatimClientFiles": "forbid",
  "tenantRegisteredServices": "forbid",
  "shadowPolicy": "forbid",
  "crossEnvironmentSharing": "forbid",
  "delegation": "forbid",
  "parentTools": "forbid",
  "offerTools": "forbid",
  "delegationLeave": { "max": "transfer", "maxDepth": 1 },
  "derivedRuntimes": { "forbid": ["all"] },
  "hostPatterns": {
    "neverAllow": ["*.paste.example"],
    "allowOnlyWithin": ["github", "pypi", "npm"]
  },
  "operatorAccess": { "approval": "required", "emergencyAccess": "allowed" },
  "approvals": {
    "tools": [{ "match": "session.create", "when": { "targets": ["prod-*"], "llmCreditsAbove": 500000 }, "approver": "security", "showArguments": true }],
    "parentTools": [{ "match": "*", "approver": "owner", "showArguments": true }],
    "repositoryWrites": [{ "match": "github.com/acme/prod-*", "ttlSeconds": 3600 }],
    "timeoutSeconds": 1800
  },
  "placement": { "requireNodeSeparation": true, "nodeTenancy": "dedicated", "allowSharedPod": false, "allowReusedPod": false },
  "lifecycleSubjects": ["~svc~svc-identity-sync"],
  "auditorStreamGrants": [],
  "weakeningDelaySeconds": 86400
}
```

Each stored version is a row in `tenant_security_config_versions` with its author, justification, `createdAt`, `effectiveAt`, and state (`pending`, `active`, `superseded`, or `cancelled`).

**Placement.** The `placement` section holds the tenant's own placement requirements under the placement design's O7 (a), settled on 2026-11-12: `requireNodeSeparation: true`, `nodeTenancy: dedicated`, `allowSharedPod: false`, and `allowReusedPod: false`, which joined the requirements under the placement design's D15. Each is a filter that narrows the pools the tenant's sessions may run on, enters every session's effective requirements with `details.source: tenant`, and never creates nodes or changes pools ([PM-4](../0136-placement/change.md#pm-4-placement-requirements)). An absent member requires nothing. The operator's own placement settings for the tenant are tenant security settings, written by `platform-admin` and never in this record ([TSS-6](../0150-tenant-security-settings/change.md#tss-6-placement-settings)). An environment's `placementRequirements` add to the tenant's and cannot remove one. They are environment data that a `tenant-admin` writes. Adding one applies at once, and removing or weakening one is a relaxation under the rules of the tenant security settings: it needs a `justification`, is recorded in the tenant's audit chain, and takes effect after this record's `weakeningDelaySeconds`, during which a `security-admin` can cancel it (decision 35, [PM-4](../0136-placement/change.md#pm-4-placement-requirements)). A requirement that must hold for every environment of the tenant belongs in this record.

**Delegation.** The record holds the security-tier switches of [`0166-delegation-primitives`](../0166-delegation-primitives/change.md#dp-1-session-grant), under decision 37 of that change, and a `security-admin` writes them under this record's single writer and weakening rule. Numeric resource ceilings on children stay with the `tenant-admin`, as 0166 "DP-1" states. Each field only narrows what the other root grant inputs of 0166 allow, so a guardrail never grants anything a gate has not granted, and the gateway reads the active version live at each `session.create`, `session.transfer`, `session.release`, and call to a tool a parent offers its child:

- `"delegation": "forbid"` forbids delegation for every session of the tenant, under "Delegation guardrail" of [`0126-delegation-availability`](../0126-delegation-availability/change.md) (D5), which decides from it whether a session holds a grant.
- `"parentTools": "forbid"` forbids every tool a parent offers its child under 0166 "DP-8", for every session of the tenant, whatever `gateway.delegation.parentTools.enabled`, a grant's `offerTools`, or a `session.create` hold.
- `"offerTools": "forbid"` forbids `offerTools: allow` on `session.create`, so a fenced child, one created with fewer credential bindings, fewer egress hosts, or a stronger isolation profile than its parent, never receives its parent's tools (0166 decision 31). Unfenced children still receive them where the other inputs allow.
- `delegationLeave` bounds the two ways a session leaves its parent under 0166 "DP-2". `max` is `none`, `transfer`, or `release`, each allowing the modes before it, and caps the security caveat `leave` of every grant in the tenant. `maxDepth`, an integer of 0 or more, caps the security caveat `leaveMaxDepth`, the deepest depth at which a session may leave. An absent member caps nothing, so the platform defaults of 0166, `transfer` and 1, and the other inputs decide.

#### API

| Method and path | Caller | Behaviour |
| :-- | :-- | :-- |
| `GET /v1/admin/tenants/{id}/security-config` | `platform-admin`, `tenant-admin`, `security-auditor`, `security-admin` | Returns the active version, any pending version, and the list of suspended objects. |
| `PUT /v1/admin/tenants/{id}/security-config` | `security-admin`, or `platform-admin` | Requires `If-Match`. Accepts the §25.2 dry-run pattern, which returns the objects it would suspend and writes nothing, and `exemptions` naming objects from that list. |
| `POST /v1/admin/tenants/{id}/security-config/pending/{version}/cancel` | `security-admin` | Cancels a pending weakening. Emits `security.config_weakening_cancelled`. |
| `GET /v1/admin/tenants/{id}/security-posture` | `platform-admin`, `tenant-admin`, `security-auditor`, `security-admin` | See "Posture". |

#### Tightening and weakening

The applier classifies each change field by field.

- **A tightening** applies in the write's transaction. It emits `security.config_tightened`, and the response lists every object it suspended. Setting `"delegation": "forbid"`, `"parentTools": "forbid"`, or `"offerTools": "forbid"` is a tightening, and so is adding `delegationLeave` or one of its members, lowering `delegationLeave.max` from `release` to `transfer` or from `transfer` to `none`, and lowering `delegationLeave.maxDepth`.
- **A weakening** removes a forbid, including `"delegation": "forbid"`, `"parentTools": "forbid"`, and `"offerTools": "forbid"`, raises `delegationLeave.max` or `delegationLeave.maxDepth` or removes either member, shrinks `neverAllow`, widens `allowOnlyWithin`, relaxes approval rules, relaxes `requestRules.requiredPresets`, `userAuthorization`, or `identityBinding.forbidUnverified`, adds to `tokenTrust.allowedRecords`, adds to `lifecycleSubjects` or `auditorStreamGrants`, removes a `placement` requirement, turns on `dangerouslyAllowCrossEnvironmentSharing`, adds a `dangerouslyShare` entry, or lowers `weakeningDelaySeconds`. The same delay applies to the security principal and SIEM destination changes those sections name. It needs a non-empty `justification`, or the write returns `400 SECURITY_CONFIG_JUSTIFICATION_REQUIRED`, following §9.2's **Justification requirement.** The write creates a pending version and emits `security.config_weakening_requested`, which subscriptions and SIEM destinations receive at once. A periodic applier activates it at `effectiveAt` and emits `security.config_weakened`.
- **A mixed write** applies its tightening parts at once and holds its weakening parts.

The delay differs from the weakening cooldown below, which refuses transfers of content for a short window after a change has already taken effect. A delay fits here because it gives the rest of the security team time to cancel a weakening from a stolen credential before it has any effect. The duration has the same protection as §8.3's: `security.config.minWeakeningDelaySeconds`, a Helm value with a default of 3600, is a floor that the admin API cannot change, and a pending weakening keeps the delay in force when it was written. `security.config.maxWeakeningDelaySeconds`, a Helm value with a default of 604800, is a ceiling, because raising the delay applies at once and a stolen token would otherwise freeze every later removal.

**Cooldown after a weakened content scan.** §8.3 keys a cooldown to `delegate_task` and the `PreDelegation` phase: after `scanExportedFiles` on a `DelegationPolicy` changes from `true` to `false`, or an interceptor that a `contentPolicy.interceptorRef` names changes its `failPolicy` from `fail-closed` to `fail-open`, the gateway refuses the affected delegations for `gateway.interceptorWeakeningCooldownSeconds`. Under decision 45 of [`0166-delegation-primitives`](../0166-delegation-primitives/decisions.md#decisions), `scanExportedFiles` and `interceptorRef` are members of the content caveat that every session carries, resolved from the platform, the tenant, the environment, and the runtime, and the cooldown covers every path by which a file moves between two sessions **[design]**:

- **What starts it.** A write that changes `scanExportedFiles` from `true` to `false` at the tenant, environment, or runtime level of the content caveat, and a `fail-closed` to `fail-open` change of the `failPolicy` of an interceptor that any level's `interceptorRef` names. The platform level changes only at a gateway restart and starts no cooldown. A change in the other direction applies at once and starts none.
- **What it refuses.** For `gateway.interceptorWeakeningCooldownSeconds` after the server-minted `transition_ts` of the write, the gateway refuses each transfer whose sender's or receiver's content caveat takes a value from the weakened level or names the weakened interceptor: `session.create` with `files`, `session.send` with a file part that sets `materialize: true`, and `files.import`, each with `503 INTERCEPTOR_WEAKENING_COOLDOWN` (`TRANSIENT`). After an interceptor's weakening it also refuses `session.create` and `session.send` whose content the interceptor screens, as §8.3 refuses `delegate_task` and `lenny/send_message`. A transfer refused this way moves nothing, and a retry after the window succeeds.
- **The window.** `gateway.interceptorWeakeningCooldownSeconds` keeps §8.3's default of 60 seconds, minimum of 30, and maximum of 600. It is cluster-scoped and absent from the admin API, `transition_ts` is server-minted and refused from a request with `INTERCEPTOR_COOLDOWN_IMMUTABLE`, a weakening resets the window and a strengthening never extends it, a lowered Helm value does not shorten a window already running, and every replica computes the window from the stored `transition_ts`.
- **Records.** The write emits the audit event that §8.3 names for its kind, `delegation_policy.export_scan_weakened` for a scan and `interceptor.fail_policy_weakened` for an interceptor, with the level written, the old and new values, `transition_ts`, and `cooldown_seconds`, and the first refusal of each window emits `interceptor.weakening_cooldown_active`.

The cooldown protects every level of the content caveat, whoever writes it. It does not replace this record's weakening delay, which governs the record's own fields.

A platform admin's write takes the same path, is mirrored under SO-4, and needs a `justification`, with an optional `ticketId`. Whether an operator uses tickets is the operator's choice, and Lenny records whatever reference it gives.

**One applier and one delay for both records.** The operator's tenant security settings are a separate record with its own version table, `tenant_security_settings_versions`, and its own writer, `platform-admin` ([TSS-1](../0150-tenant-security-settings/change.md#tss-1-settings-model)), under TSS O13 (a), settled on 2026-11-12. Under TSS O5 (b), settled the same day, a relaxation of a setting takes effect after this record's `weakeningDelaySeconds`, within the same floor and ceiling. The applier above activates pending versions of both records at their `effectiveAt`, and a third kind of record as well: the pending rows of `environment_placement_versions`, which hold an environment's `placementRequirements` relaxations under decision 35 and D18 of [`0136-placement`](../0136-placement/change.md). What a pending version of any of the three holds, and how it composes with later writes, is open decision 40, and SO-2 states that rule once for all three when the owner settles it. A tenant's `security-admin` may cancel a pending relaxation of the settings through `POST /v1/admin/tenants/{id}/security-settings/pending/{version}/cancel` as a `platform-admin` may ([TSS-3](../0150-tenant-security-settings/change.md#tss-3-admin-api)). The settings keep their own events, `tenant.security_settings_relaxation_requested` and `tenant.security_settings_relaxed` among them, and the security posture reports both records side by side ("Posture"). A `security-admin` cannot write a setting.

**Where the applier and the sweeps run.** The applier and every periodic job of this design run in the gateway as leader-elected goroutines, with the Kubernetes Lease primitive that the artifact GC job of §12.5 Artifact Store uses, so one replica runs each job at a time. The applier activates every pending kind this design delays: versions of this record and of the tenant security settings, security principal assignments and removals, SIEM destination creations, changes, and removals, `securityRoleSource` changes to `oidc`, `identityProvider` endpoint changes, environment `placementRequirements` relaxations under decision 35, and the turn-on of `dangerouslyAllowCrossEnvironmentSharing` and each added `dangerouslyShare` entry. Each activation is a conditional update guarded by `state = 'pending'` (or `removal_pending`), and only the update that changes the row emits the activation event and clears suspensions, so a stale leader or a re-run emits nothing twice. An activation whose `effectiveAt` passed while no applier ran is applied on the next run, in `effectiveAt` order per tenant. The same leader rule and conditional update govern the principal expiry sweep under SO-1, the impersonation expiry and mirror sweeps under SO-4a, and the expiry of approvals and operator access requests under SO-4b and SO-5. An activation that re-checks `COMPLIANCE_SIEM_REQUIRED` runs that check in the activation's transaction, under the lock on the tenant's destination rows that "API" under SO-3 states.

#### Enforcement

- **New writes.** SO-2 fills the "tenant policy check, empty today" on the opt-in write path of [EG-9](../0127-egress-and-git/steps.md#steps) with a check of each opt-in change against the active version. The check reads the version under a shared row lock, and the record's writer holds an update lock, so a concurrent opt-in cannot miss a tightening. A violation returns `422 GUARDRAIL_VIOLATION` with `details.rule` and `details.configVersion`, and emits `security.guardrail_refused`. A failed read refuses the write with `503`. Egress decisions and mints read `suspendedBy` and the writable set from their own stores and never read this record. A tool call that SO-5a holds reads the rules at dispatch, and a failed read denies that call.
- **Existing objects.** A tightening sets `suspendedBy` on each non-compliant object in the same transaction, and increments the policy and definition versions so the change reaches decisions and pods through the existing binding push. It also re-evaluates running sessions against `approvals.repositoryWrites`. An activation that removes a rule clears the mark it set, unless the object still violates a remaining rule.
- **Named exemptions.** The write that tightens can carry `exemptions`, each naming an object from its dry run and the rule entry it violates (a forbid value, a host pattern, a preset, or a trust record), and the tightening sets no mark for that entry on that object. Such an exemption is part of the tightening only when the active version leaves the object unmarked and an entry the new version adds is what the object violates. An exemption naming an object the active version already suspends, or written on its own, is a weakening and takes the delay, so a stolen credential cannot lift a suspension by pairing it with a trivial tightening. Removing an exemption applies at once and suspends the object. An exemption records a digest of every input the rule entry reads for that object, including records held in other objects and platform data, such as the routes and CA records in a token trust record or a platform host set. Every write that the [EG-9](../0127-egress-and-git/steps.md#steps) check already sees for that rule recomputes the digest, whichever object it touches, and so does a platform write to such an input. A changed digest ends the exemption and suspends the object, as a tightening would, and emits `security.guardrail_exemption_lapsed`, rather than refusing the write that changed it, so a tenant's exemption never blocks a platform write. A write that changes no input, such as a rename, leaves the exemption in place. An exemption ends when its object is deleted, and the posture's `securityConfig` section, the audit log, and `security.guardrail_exempted` name each one. An exemption does not change `content_stop_epochs` under [`0144-session-derive`](../0144-session-derive/change.md), so a derive of a plan bound before the tightening stays refused, because that plan can hold content nobody reviewed when the exemption was written. Only a `security-admin`, or a platform admin with a justification under SO-4, writes exemptions.

| Constraint | Object affected | Effect while suspended |
| :-- | :-- | :-- |
| `credentialDelivery.forbid` | Credential bindings with that delivery | The Token Service mints nothing and `netagent` removes the file or endpoint. A plan naming the binding is rejected at create. |
| `credentialDelivery.forbid: userCredential.file` | `userCredential` bindings with `delivery: file` | The Token Service releases nothing and `netagent` removes the file. Copies an agent made remain. A plan naming the binding is rejected at create. |
| `credentialDelivery.forbid: userCredential.metadata` | `userCredential` bindings with `delivery: metadata`, which only an `awsWebIdentity` type of [`0143-service-account-credentials`](../0143-service-account-credentials/change.md) takes | The Token Service releases nothing and `netagent` stops serving the endpoint. Credentials an agent already read stay valid for the rest of their 900 seconds. A plan naming the binding is rejected at create. |
| `requestRules.requiredPresets` | Credential bindings with `delivery: inject` on a matching host whose resolved rules omit the preset or pin below its minimum version, read at a binding write and at a VCS pool write | Suspended, until the tenant admin restores the preset. The pod credential API also refuses a resolved rule set lacking a preset recorded on the binding. |
| `tokenTrust.allowedRecords` | Link providers and types whose current token trust record digest is not listed, and every `issuerExchange` type whose digest is not listed whether or not the member is present | Suspended, so nothing is released, linked, written, verified, or exchanged under them. It sits on the opt-in write path of [EG-9](../0127-egress-and-git/steps.md#steps), so it holds against a tenant admin and not against a compromised gateway replica, which writes rows directly. |
| `userAuthorization.require` | VCS pools matching the pattern with `userAuthorization: off` | Suspended, so a session on them is refused. |
| `userAuthorization.serviceIdentities` | A VCS pool's `serviceIdentities` entries outside the allowed list | The entry is ignored, so a session on it is refused. |
| `userAuthorization.forbidRuntimeHeld` | VCS pools with `allowRuntimeHeldCredentials: true` | The flag reads as `false`, so a `userCredential` binding with `delivery: file` whose type hosts include those hosts is suspended, and a plan naming it is rejected at create with `422 USER_CREDENTIAL_BINDING_SUSPENDED`, the code that [UC-5](../0151-user-credential-delivery/change.md#uc-5-delivery-file) defines for a plan naming any suspended `userCredential` binding, with `details` naming the suspender. A `registered` type can declare other hosts, as [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) states, and the pin on the real host is the enforcing control. |
| `identityBinding.forbidUnverified` | Link providers with `identityBinding: unverified` | The provider's links stop being used, and users relink once it changes. |
| `storedTokenPush` | VCS pools that set `allowStoredTokenPush` | Stored-token push and GitLab token reuse, which reuses one GitLab project access token per tenant, pool, project, and scope, are refused. Reads continue. |
| `verbatimClientFiles` | Tenant client config files and overlay file templates, and the derived-runtime content that the `derivedRuntimes.forbid` values `workspaceFiles` and `gitClones` cover, so an asset a platform admin writes on the runtime is not refused | Files are removed at the next binding push, and pods keep no copy after `netagent` applies the change. A derived runtime holding that content is suspended, as the `derivedRuntimes` row states. |
| `tenantRegisteredServices` | Tenant-registered upstreams and git endpoints, the outbound connectors of [OC-1](../0148-tenant-outbound-connector/steps.md#steps) (0148.S1), and the outbound hosts of [OC-2](../0148-tenant-outbound-connector/steps.md#steps) (0148.S2) | Routes naming them deny, using the suspended state the egress design defines for a registration that fails re-resolution. A route to a suspended outbound connector or outbound host carries no traffic to the customer's network. |
| `shadowPolicy` | A running shadow policy | The candidate stops being evaluated. Active policy outcomes do not change. |
| `crossEnvironmentSharing` | `dangerouslyAllowCrossEnvironmentSharing` and every `dangerouslyShare` declaration | The setting reads as off at the crossing check of TS-3, so the next `session.create` on a sharing path, and any `session.create` by a session with a crossing mark, answers `TARGET_NOT_IN_SCOPE`, and a `session.release` of a crossed child answers `TARGET_NOT_IN_SCOPE` as well. Additions are refused, removals succeed, and declarations stay stored. A crossed child already running is not ended, and a `session.create` that an SO-5a hold keeps waiting when the forbid is set is refused when the hold ends. |
| `delegation` | Every session of the tenant | `session.create`, `session.transfer`, `session.release`, and a call to a tool a parent offers its child answer `403 DELEGATION_DISABLED` with `disabledBy: securityGuardrail`, and the other operations of [`0166-delegation-primitives`, "DP-1"](../0166-delegation-primitives/change.md#dp-1-session-grant) follow the refusal of [`0126-delegation-availability`](../0126-delegation-availability/change.md), under which a session with children keeps the operations that manage them (DAR-6). A call to a parent's tool that is open when the guardrail is set is answered with an error result carrying `DELEGATION_DISABLED`, and the child's `client` tool server withdraws the parent's tools. A `session.create` that an SO-5a hold keeps waiting is refused when the hold ends. Each session's `availability.delegation` turns `false` at its next turn, and running children continue. The gateway reads the active version at each call, under "Delegation guardrail" of 0126 (D5). An SO-5a approval hold on `session.create` runs after this refusal, at the post-chain point. A `tenant-admin` cannot write the guardrail. |
| `parentTools` | Every tool a parent of the tenant offers its child | The next `session.create` or `session.send` that carries `tools` answers `403 DELEGATION_DISABLED` with `disabledBy: securityGuardrail` and `details.rule: "parentTools"`. The tools each parent offered its children are withdrawn: each child's `client` tool server stops listing them, and its runtime receives `availability_changed` naming `client` ("RP-4" of [`0139-runtime-protocol`](../0139-runtime-protocol/change.md#rp-4-availability)). A call to one that is open, or held under `approvals.parentTools`, is answered with an error result carrying `DELEGATION_DISABLED`, and the parent never receives it. |
| `offerTools` | Every fenced child to which its parent offered tools under `offerTools: allow` | The next `session.create` that sets `offerTools: allow` answers `403 DELEGATION_DISABLED` with `disabledBy: securityGuardrail` and `details.rule: "offerTools"`. The tools offered to each fenced child are withdrawn and its open calls answered as under `parentTools`. Unfenced children keep their parents' tools. |
| `delegationLeave` | Every grant of the tenant whose `leave` or `leaveMaxDepth` exceeds the new value | The next `session.transfer` or `session.release`, from a parent or from the root's client through the routes of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), that the new `max` or `maxDepth` does not allow is refused as a leave beyond the grant's `leave` or `leaveMaxDepth` is under 0166 "DP-2", naming the security record version. A leave already completed is not undone, and a released session stays a root. |
| `derivedRuntimes.forbid` | Derived runtimes matching `all`, `setupCommands`, `workspaceFiles` (default files and `inline` and `artifact` assets), `gitClones` (default clones and the `gitRepository` assets a tenant admin declares; an asset a platform admin writes on the runtime is not refused), `floatingCloneRefs` (a default clone at a ref other than a full commit), `writableDefaultClones` (a default clone the session can push to), `alwaysWritableClones` (one with `write: always`), `reservation` (reserved pod capacity), or `reusedPods` (any runtime without `oneSessionPerPod: true`) | The runtime leaves discovery, new sessions answer `409 DERIVED_RUNTIME_UNAVAILABLE`, and its pool stops warming and drains. Running sessions continue, and [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/change.md) states what that leaves open. Activating a version that adds a content value (`all`, `setupCommands`, `workspaceFiles`, `gitClones`, or `floatingCloneRefs`) also raises a tenant-keyed counter held outside the versions, which refuses a derive of earlier plans, under [`0144-session-derive`](../0144-session-derive/change.md). |
| `hostPatterns.neverAllow` | Tenant always-allowed and session-requestable entries | Patterns are held in a read-only partition of the tenant's denied hosts, so the formula under [the egress design's "Effective policy"](../0127-egress-and-git/change.md#effective-policy) gains no term. |
| `hostPatterns.allowOnlyWithin` | Entries outside the set | Those entries are suspended. |
| `approvals.repositoryWrites` | Writable set entries in state `granted` of bound sessions that match the new rule, without an approval | The entry moves to `pendingApproval`, a `security_approvals` row is created, and the binding change flushes `netagent`'s cached write token. This row lands with SO-5b (0142.S7), which adds the store's `repository_write` kind and the entry states. |
| `approvals.tools` | Tool calls | Applies at the next dispatch, and a call already released is not recalled. |

A platform gate and a guardrail both apply: the platform grants a class, and the tenant's security team can still forbid it. A guardrail never grants anything a gate has not granted.

#### User credentials and request rules

[`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) lets a tenant deliver a person's token to a pod, and [`0152-user-git-identity`](../0152-user-git-identity/change.md) configures that for VCS hosts. Oversight works as follows.

- **The grant.** `userCredentialDelivery` is a platform-admin grant per tenant, its own risk class. `userCredentialMetadataDelivery`, which admits a service account's 900-second AWS credentials under `delivery: metadata` (O6 of [`0143-service-account-credentials`](../0143-service-account-credentials/change.md)), is another grant in a class of its own, so a grant for bearer files admits no AWS signing credentials, and the value `userCredential.metadata` of `credentialDelivery.forbid` covers those bindings alone. A grant or a withdrawal is a platform admin's write to a customer's configuration, so SO-4a mirrors it to the tenant's chain. A customer's `security-admin` can forbid the delivery regardless, through `credentialDelivery.forbid`, in which the value `file` covers every binding with `delivery: file` and the value `userCredential.file` covers `userCredential` bindings with `delivery: file` alone, because "a guardrail never grants anything a gate has not granted" and a gate cannot override a guardrail.
- **Preset changes.** A tenant admin can clear or change a binding's request rules through the opt-in write path of [EG-9](../0127-egress-and-git/steps.md#steps), which applies the guardrail check. Each write emits an audit event naming the old and new rules, and `requestRules.requiredPresets` makes a preset mandatory by host pattern, with a minimum version, so a binding pinned to an old version does not satisfy it. Removing a required preset is a weakening, with SO-2's delay. A preset update shipped by the platform is a platform change, recorded in the deployment's own audit, and it reports in posture as the version in force.
- **Token trust records.** Everything that decides where a stored token can go and who can read it is one record, with a digest on each stored token, and any change makes the token unusable and sends it nowhere. **That protects stored tokens against reading through configuration. It does not protect a future grant:** a tenant admin who controls configuration can trick a user into granting a new token to a host she runs, or re-point an automated backend. It also does not see the DNS, CA, or network behind a name the customer operates. The enforcing control for a tenant that needs that separation is `tokenTrust.allowedRecords`, a `security-admin`'s list of approved records, each as its digest and content, under the weakening delay. The [EG-9](../0127-egress-and-git/steps.md#steps) check recomputes the digest on each write touching a provider, type, route, or CA, and refuses or suspends what is not listed. It holds only when `security-admin` and `tenant-admin` are different people, which posture reports.
- **Approval of an `issuerExchange` type.** An `issuerExchange` type of [`0143-service-account-credentials`, "SA-3 The `issuerExchange` source"](../0143-service-account-credentials/change.md#sa-3-the-issuerexchange-source) makes the Token Service sign JWTs that a provider trusts for a service account, so it takes effect only when its token trust record is on `tokenTrust.allowedRecords`, whether or not the tenant's record holds that member for other kinds. An absent member therefore allows every record except an `issuerExchange` type's. Until a `security-admin` adds the record's digest and content, a weakening that waits out `weakeningDelaySeconds`, the type is suspended: a row write answers `422 GUARDRAIL_VIOLATION` with `details.rule: tokenTrust.allowedRecords`, and nothing is verified, exchanged, or released under it. Any change to the type changes its digest and suspends it again. A tenant with no `security-admin` asks a platform admin, whose write takes the same path with a justification and is mirrored under SO-4a. `issuerExchange` is a source that did not exist before, so a tenant with no security principal and an empty record keeps today's behaviour, as SOR-10 requires.
- **Writing a token for a user.** `manage_user_credentials` lets its holder place a token under a named user's identity, or under a service account's on the service account route of [`0143-service-account-credentials`](../0143-service-account-credentials/change.md), and a tenant admin holds it, within §10.2's custom-role ceiling. SP2 does not reach this. A caller cannot write its own rows on the service account route, which answers `403 USER_CREDENTIAL_SELF_WRITE`. Each write emits an audit event naming the writer and the user, and `GET /v1/credentials` shows the user `written_by`. A write naming a security principal is refused, on the reasoning of SP2.
- **Repository rules.** SP11 and decision 25, which omits repositories not yet approved from a binding's write token, apply as written. When a `userCredential` binding's narrowing template is platform-shipped and sets `followsEntryState`, as `github-scoped` does today, each mint from it counts as a `mint-scoped` binding mint and names only writable set entries in state `granted`. The flag is ignored on a tenant's own template. Any other template, no template, a `registered` type, or any binding with `delivery: file` is refused or suspended where the type's hosts include a rule's API host, under `approvals.repositoryWrites`, because Lenny cannot limit what it reaches. `allowRuntimeHeldCredentials` does not override this.
- **Impersonation.** SP10 keeps approvals away from an impersonation bearer. [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) also applies a check at the session input layer, the shared call through which every client message reaches a session, for a session whose delegation tree holds a `userCredential` binding or a repository under a VCS pool with `userAuthorization: required`. The check and its test cover the REST session input routes alone, because under [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md#ro-4-authorization-of-adapter-requests) every protocol adapter reaches a session through those routes, and the impersonation mint refuses an `adapter-only` token and every narrowed request ("RO-4"). It refuses that bearer on `/v1/credentials` writes, so an operator cannot drive such a session or swap its token. [CA-10](../0123-connector-authorization-and-external-memory/change.md#ca-10-removing-built-in-memory) removes Lenny's built-in memory, so no memory write remains to refuse. Since revision 56, connectors are removed by [`0124-connector-removal`](../0124-connector-removal/change.md), so an external memory server is reached through egress with a `userCredential` or `identityAssertion` binding, and the check above covers a tree holding either (O6 of the removal design). Revisions before 56 recorded the linked-connector case as an accepted risk, which "Residual risks" below restates.

#### Posture

`GET .../security-posture` returns one object with a section per topic, and each value carries `observedAt` and `state` (`known` or `unknown`). A section whose source is unavailable returns `state: "unknown"` inside the §25.2 degradation envelope rather than failing the read.

| Section | Reports |
| :-- | :-- |
| `pools` | For each pool the tenant can use: `sharedWithOtherTenants` (a boolean, with no tenant names or counts), `ownerTenant`, `isolationProfile`, `egressProfile`, `gitAccess`, `nodeClass`, `nodeClassMembers` (more than one entry means a mixed class), `nodeTenancy`, `sharedPod`, `userNamespaces`, `placementOffering`, and `region` as [PM-4](../0136-placement/change.md#pm-4-placement-requirements) defines them for `sessionIsolationLevel`, `perSlotScopes` on a pool with several slots per pod (whether `netagent` applies each session's own egress scope and credentials to its slot, or keeps the pod-wide union because the pool's runtime class does not answer slot attribution, under [`0127-egress-and-git`, decision 60](../0127-egress-and-git/decisions.md#decisions)), `diskQuota` (`enforced`, `partial`, `none`, or `notApplicable`: whether project quotas bound the pool's slot volumes on the nodes of its class, which decides whether one session can evict a shared pod by writing past the disk watch or by hiding bytes from it, as [`0118-agent-pod-layout`, "PL-10"](../0118-agent-pod-layout/change.md#pl-10-project-quotas) defines it from node coverage), and on an owned pool the digest of any CA the tenant added through `caBundle`. |
| `egress` | The enforcement level (default, which is Lenny's own `lenny-egress` proxy, level 2, or level 1, with a pointer to what level 1 gives up), egress groups, whether the tenant's address is shared, and the tenant's pools with `egressProfile: internet`, whose raw sockets do not use a group's address and on whose sessions no credential pin applies, including the pin from a VCS pool with `userAuthorization: required`, as [`0127-egress-and-git`, "Exfiltration controls"](../0127-egress-and-git/change.md#exfiltration-controls) states. |
| `interceptors` | Each interceptor registration that lists this tenant under [RT-6](../0141-runtime-routing/change.md#rt-6-tenant-scoped-interceptors), with its phases, priority, `failPolicy`, and Service name. |
| `gates` | The per-class grants the tenant holds, including `tenantDerivedRuntimes` with its limits, reservation allowance, and current use, and the opt-ins in use under each. The outbound grant of [OC-1](../0148-tenant-outbound-connector/steps.md#steps) (0148.S1) is one of these grants, so the section lists its quotas and the outbound connectors and outbound hosts in use under it. It also reports the `dangerouslyAllowCrossEnvironmentSharing` setting and each declared `dangerouslyShare` path, including declarations that stay stored with no effect while the setting is off or forbidden and that apply again if it returns, under [`0149-tenant-scoping-identity`, "Cross-environment sharing (decision 14)"](../0149-tenant-scoping-identity/change.md#cross-environment-sharing-decision-14). |
| `derivedRuntimes` | For each derived runtime: name, base, `baseStatus`, the count and digest of default commands and files, each default clone's URL and ref, whether the ref is a full commit, its `write` value, each `gitRepository` asset's `name`, URL, ref, declarer (`tenant`, or `platform` for an asset a platform admin wrote), and whether it sets `prestage: true`, each `inline` and `artifact` asset's path and digest, each warm-list entry suspended at render with its reason, `reservedPods`, `placementConfigured` (whether the template pool sets `placement.reservedOverlay`, so reserved pods run on the nodes the overlay selects, [PM-3](../0136-placement/change.md#pm-3-placement-offerings-and-per-pool-nodeplacement)), `oneSessionPerPod`, the effective `maxConcurrentSessionsPerPod` and recycling, last writer, and last change. |
| `runtimeDefaultClones` | For each operator runtime the tenant can use, each default clone whose ref is not a full commit, with its URL and ref, because whoever can push to that ref runs code in the tenant's sessions on the runtime ([DR-9](../0147-tenant-derived-runtimes/change.md#dr-9-default-git-clones)). |
| `userCredentials` | Types and their sources, bindings by delivery, the `userCredentialDelivery` grant, each binding's preset and version, cleared presets, link providers with `identityBinding: unverified` or no narrowing template, widened template `permissions`, VCS pools that set `prAuthoring: user` on which no rule addresses bots, as a prompt under [`0152-user-git-identity` decision 35](../0152-user-git-identity/decisions.md#decisions), each current token trust record and whether it is on `tokenTrust.allowedRecords`, VCS pools with `allowRuntimeHeldCredentials: true` or `pinHosts: false`, each binding in allow-list mode (`requestRules.default: deny`, under [`0151-user-credential-delivery`, "Allow-list mode"](../0151-user-credential-delivery/change.md#allow-list-mode)), each binding with `delivery: file` on a pool with `egressProfile: internet`, each `issuerExchange` type with its profile, its exchange endpoint, whether that endpoint was discovered from a tenant-registered issuer, and whether its record is approved or awaiting approval, the `userCredentialMetadataDelivery` grant and each binding with `delivery: metadata` with its open exposure records, each `registered` type whose `principalTypes` lists `service`, each tenant override of a shipped template, each link stored as non-expiring because a provider response carried no `expires_in`, each `clientCredentials` type that authenticates to its provider with `client_secret_post`, each type with `assertionAudience: tokenEndpoint`, each `clientCredentials` type whose provider omitted `expires_in`, so that the Token Service applied `assumedLifetimeSeconds`, each certificate the Token Service generated for a client key with its expiry, and whether one person holds both `security-admin` and `tenant-admin`, on which the approval of an `issuerExchange` type relies, as [`0143-service-account-credentials`](../0143-service-account-credentials/change.md) states, and sessions marked `agentHeldCredential` in the last 30 days. |
| `llm` | From "LU-8" **Posture.** of [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md): the tenant's effective upstream list per pool, with the upstream that serves each model pattern and each platform upstream a tenant upstream shadows (0133 decision 15), each upstream's scope and format, and its `provider` and `contract` labels if open decision 16 of `0133-llm-upstreams-and-cost-budgets` keeps them, each upstream's attribution settings with the attributes sent and whether user identifiers are pseudonymous, the rate-card versions in force, and the budgets per scope, and, for each upstream that serves other tenants, the sharing and the shared-upstream warning (LUR-14) in the form that open decision 38 settles and in the wording that open decision 18 of `0133-llm-upstreams-and-cost-budgets` settles. It lists no LLM traffic outside the proxy, which posture treats as any other egress traffic. |
| `securityConfig` | Active version, including its `placement` requirements, whether `"delegation": "forbid"`, `"parentTools": "forbid"`, and `"offerTools": "forbid"` are set, and `delegationLeave`, pending weakening with its `effectiveAt`, suspended objects, and named exemptions with their rule, object, and author. |
| `securitySettings` | The operator's tenant security settings, with the content of the view `GET /v1/admin/tenants/{id}/security-settings` returns ([TSS-3](../0150-tenant-security-settings/change.md#tss-3-admin-api)), stated by reference to that view, so the section carries every member the view carries, `objectsUnderTenantKey` and `evidenceKeysUnderTenantKey` among them. The view includes the active version and values, any pending relaxation with its `effectiveAt`, the template applied and whether it differs or is outdated, the effective value and source of each setting, the tenant key's state, alias, last successful probe, and object counts, and `sessionsUnderEarlierVersion`. |
| `audit` | `complianceProfile`, `dataResidencyRegion`, retention, and the chain integrity report from the most recent background verification, with its time and range. `complianceProfile` stays as it is until the follow-up design that TSS O9 (b) names replaces it with deployment-level settings and per-tenant settings. |
| `siem` | Whether a platform copy exists (with no address), each tenant destination's state and lag, and gaps recorded. |
| `operatorAccess` | Approval setting, active impersonation sessions, and counts for the last 30 days. |
| `identity` | `securityRoleSource`, the host of any introspection endpoint, each customer source's pin form (how its issuer is tied to this tenant, under [`0149-tenant-scoping-identity` decision 18](../0149-tenant-scoping-identity/decisions.md#decisions)), and, on the operator tenant of a deployment with more than one tenant, a warning while that tenant accepts the deployment issuer, under [`0149-tenant-scoping-identity` decision 24](../0149-tenant-scoping-identity/decisions.md#decisions). From the [SCIM server (TS-8)](../0149-tenant-scoping-identity/change.md#scim-server-ts-8) and [directory groups (TS-9)](../0149-tenant-scoping-identity/change.md#directory-groups-ts-9), it also reports each SCIM credential with its source, provisioned rows that never matched a login, logins with no directory row, each lifecycle write on a security principal that was refused because `scim:<source_id>` is not in `lifecycleSubjects`, a tenant whose SCIM groups are stored and unused under `group_source: token`, and the environment members with no group, such as members naming a SCIM group that was renamed. |
| `principals` | Holder counts per security role, the time of each role's most recent authentication, each principal's issuer and last authentication time, and subjects also holding `tenant-admin`. |

Chain integrity comes from the §11.7 periodic check because `auditRows` loads a tenant's rows into memory, and an on-demand verification on every posture read would put that cost on a read an auditor polls. Posture is reported by the platform about itself (see "Residual risks").

### SO-3: per-tenant SIEM destination

#### Existing limits

- `audit.siem.endpoint` and `audit.siem.secret` are single Helm values, and `siem_delivery_state` has `tenant_id` as its primary key, so one tenant chain has one cursor.
- `cmd/lenny-gateway/auditpipeline.go` refuses to start when the endpoint fails its test event, which is unacceptable for an endpoint a customer controls.
- `RunCycle` returns on the first delivery failure, so one failing endpoint would stall every chain.
- §25.5 webhook delivery retries three times and then marks the delivery failed with no replay, so a subscription is a notification path and cannot serve as an independent record.

#### Component

A new `lenny-siem-forwarder` Deployment in `lenny-system` delivers to tenant destinations. Its replicas, PDB, anti-affinity, priority class, and node class are those [PM-8](../0136-placement/change.md#pm-8-system-components) lists for it: two replicas, `minAvailable: 1`, on the system class. The platform outbox in the gateway stays as it is for `audit.siem.endpoint`.

- Workers take a per-destination lease in Postgres with `FOR UPDATE SKIP LOCKED`, and each destination has one in-flight batch and a fixed request timeout, so one slow destination holds one worker slot for a bounded time. That limit is the destination's budget.
- Each batch runs in a transaction scoped to the destination's tenant, so row-level security applies and no read under the cross-tenant `__all__` scope is needed.
- Its database role, `lenny_siem_forwarder`, holds `SELECT` on `audit_log`, write access to its own tables, and `INSERT` on `audit_log` restricted by a row-level trigger to `event_type LIKE 'siem.%'`, following the `lenny_erasure` restriction to `gdpr.%` in §11.7 item 7. It writes `siem.destination_failing` and `siem.delivery_gap` that way, under the per-tenant advisory lock, and it has no `UPDATE` or `DELETE`.
- It reads evidence from `lenny-evidence` by stable position (see "Changes requested").
- Its NetworkPolicy is the `lenny-ops` webhook egress rule of §25.4, which excludes private, cluster, and metadata ranges, plus Postgres, `lenny-evidence`, and the Token Service. It holds no PKI rights and no Kubernetes rights, so it has no path to the API server.

Decision 8 compares this with running delivery in `lenny-ops` or the gateway.

#### Data model

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
    pending_change  JSONB,                  -- a verified URL or scope change awaiting its delay
    pending_effective_at TIMESTAMPTZ,
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

The HMAC (keyed hash) signing secret or the header value is stored as a Kubernetes Secret in the §4.9 credentials namespace, written by the gateway under the existing verbs of `lenny-gateway-credential-writer` (`create`, `update`, `delete`, and `deletecollection` on Secrets, with no read) with the name `lenny-siemdest-` plus a hash of tenant and destination, and carrying the `lenny.dev/tenant-hash` label, so the delete-collection of Phase 4 of tenant deletion (§12.8) removes it. §4.9 **Credential Storage Topology** states that no other Lenny ServiceAccount holds a verb on Secrets in that namespace, and dynamic names rule out `resourceNames`, so the forwarder holds none. It obtains a value through the Token Service keyed read of **Token Service secret resolution**, a Token Service call that returns one stored Secret's value by name to an admitted caller, as the gateway does for VCS tokens. The Token Service admits the forwarder's client certificate for that call and only for names with the `lenny-siemdest-` prefix, so a compromised forwarder reads destination credentials and no provider credential. The forwarder keeps a value in memory for at most 15 minutes, which bounds how long a rotation takes outside its 60-second overlap. A value is written from `secretData` or generated server-side and returned once, as in §25.5's **Webhook Secret Lifecycle**. HMAC signing needs the plaintext, so the design does not copy §25.5's statement that only a hash is stored.

#### API

| Method and path | Caller | Behaviour |
| :-- | :-- | :-- |
| `POST /v1/admin/tenants/{id}/siem-destinations` | `security-admin`, or `platform-admin` with a justification | Creates a `pending` destination and validates its URL. Creation takes the weakening delay, because a stolen token would otherwise stream the tenant's audit trail to an endpoint of its choosing. |
| `POST .../siem-destinations/{destId}/verify` | Same | Sends a signed test record with a challenge. A 2xx response marks it verified, and it becomes `active` once verified and past its delay. |
| `GET .../siem-destinations`, `GET .../{destId}` | Also `tenant-admin` and `security-auditor` | Returns configuration without the secret, plus state, lag per source, and gap records. |
| `PUT .../{destId}` | `security-admin`, or `platform-admin` with a justification | Requires `If-Match`. A URL, authentication, or scope change is stored in `pending_change`, needs its own verify, and takes the delay. The destination keeps delivering to its current URL until then. |
| `POST .../{destId}/pending/cancel` | Another `security-admin`, or `platform-admin` with a justification | Cancels a pending creation or change. |
| `POST .../{destId}/rotate-secret` | Same | A 60-second overlap, following §25.5. |
| `POST .../{destId}/replay` | `security-admin` | Body: `{"since"}`. Moves the audit cursor back within retention, and delivery repeats those records. |
| `DELETE .../{destId}` | `security-admin`, or `platform-admin` with a justification | A removal or narrowing counts as a weakening, is justified, and is delayed under SO-2. A `DELETE`, or a `PUT` setting `include_audit: false`, is refused with `409 COMPLIANCE_SIEM_REQUIRED` when it would leave a regulated tenant with no independent copy. The check holds a lock on the tenant's destination rows, so two concurrent removals cannot both pass, and it runs again at activation, where a failure leaves the destination in place and emits `siem.destination_removal_refused`. |

URL validation applies §25.5's **SSRF and DNS Rebinding Protections**, which guard against server-side request forgery (SSRF), at creation and on every delivery: HTTPS only, a registered domain, resolved addresses checked each time, metadata addresses blocked, and no redirects followed. A destination on a private network is refused. The outbound connector of [`0148-tenant-outbound-connector`](../0148-tenant-outbound-connector/change.md) serves routes on the egress path, and `lenny-siem-forwarder` delivers under its own NetworkPolicy outside that path ("Component" above), so a customer whose SIEM sits on a private network places a collector of its own at a public HTTPS address in front of it, as **Destination coverage** under "Residual risks" states for other protocols.

#### Delivery

- **Format.** Records are the §11.7 OCSF translation, with the same dead-letter receipts. Delivery is at-least-once, and consumers deduplicate on `metadata.uid`.
- **What is sent.** With `include_audit`, the tenant's chain, which under SO-4 includes its operator access records. Evidence streams need `evidence.read.<stream>` held by the writer. Denied decisions are never sampled, following [the egress design](../0127-egress-and-git/change.md)'s export rule.
- **Start of stream.** The first delivery is a `siem.stream_anchor` record with the tenant, the starting sequence, the `prev_hash` at that point, and the tenant's `genesis_nonce`. A destination that starts mid-chain would otherwise lack what §11.7 writes alongside the first audit event.
- **Retries.** Backoff grows to a five-minute ceiling. A destination failing for `audit.siem.tenantFailingAfterMinutes` (default 15) becomes `failing` and emits `siem.destination_failing`, and the tenant's subscriptions receive it.
- **Gap record.** When records are lost to the hold bound below, the next delivery is a `siem.delivery_gap` record naming the range, and the tenant's own chain receives the same event.

#### Retention hold

Lenny's pruner deletes audit rows once they pass their retention period. §16.4's SIEM delivery guard stops it from deleting rows the platform forwarder has not yet acknowledged. §16.4 describes this as holding a partition, and the tree acts per tenant: `audit_log` is not range partitioned (migration 0149, F-16.4.6), `auditretention` deletes rows by tenant, and `{partition}` in the §25.9 force-drop is a tenant chain identifier. A tenant destination extends the guard in that per-tenant form, so a dead destination holds only its own tenant's rows. §16.4's wording changes to match. A proposal that later range partitions `audit_log` would have to copy held rows out per tenant, because a shared partition cannot be held for one tenant.

- An unregulated tenant's destination holds rows up to `audit.siem.tenantMaxHoldDays` past their retention (default 7). After that the pruner proceeds, and the gap record follows.
- A regulated tenant whose independent copy is only that destination holds without a bound, and `AuditPartitionDropBlocked` fires for the platform admin, as it does today for the platform copy. The storage cost falls on that tenant's rows.
- The §25.9 force-drop stays with the platform admin. `ForceDrop` on a chain with a destination behind it writes a `siem.delivery_gap` row to the tenant's chain before it deletes, naming the range and `audit.partition_drop_forced`, so the destination and the tenant both see the loss.
- Evidence objects follow the same rule against each stream's retention, using the consumer position `lenny-evidence` honours. Evidence objects sit under a tenant prefix, so this also holds one tenant's data.

#### Compliance gate

§11.7 rejects a regulated tenant when `audit.siem.endpoint` is absent, and a regulated tenant with no endpoint is fatal at gateway start. Under this design:

- **At a write.** Creating a regulated tenant, updating one, or creating an environment under one accepts a platform endpoint or an `active` tenant destination with `include_audit`, as decision 10 states in letting either copy satisfy `COMPLIANCE_SIEM_REQUIRED`.
- **At start.** The gateway reads the configuration and never contacts a tenant's endpoint. `ValidateSIEMForRegulatedTenants` returns a fatal error today for any regulated tenant when the platform endpoint is absent, which would stop the gateway for every tenant over one tenant's state. Under this design it reports per tenant and the gateway starts. A regulated tenant with neither a platform endpoint nor a destination row with `include_audit` has session and environment creation refused with `COMPLIANCE_SIEM_REQUIRED`, and raises a critical `TenantComplianceSIEMDegraded`. That tenant fails closed, and other tenants are unaffected. The §11.7 fatal text changes accordingly (decision 10).
- **In operation.** A regulated tenant whose only destination is not `active` raises the per-tenant `TenantComplianceSIEMDegraded` alert, and posture reports it.

#### Residency and erasure

- A destination declares `region`. For a tenant with `dataResidencyRegion`, a differing or absent region is refused unless the write carries `acknowledgeCrossRegionTransfer: true` with a justification, because the transfer is the customer's own decision. A forwarder that cannot reach the tenant's regional store marks the destination `blocked`.
- §12.8's `erasure.requested` event reaches tenant destinations, and the receipt records their acknowledgment. Records already delivered are outside Lenny's control, as §25.9 **Downstream SIEM Scope Boundary** states, and the customer is the responsible party for that copy.
- Tenant deletion stops delivery after a final `siem.stream_closed` record, and Phase 4 of tenant deletion removes the destination rows and Secret.

### SO-4: operator access transparency

#### SO-4a: mirror to the tenant chain

An `operator_access.recorded` event is appended to the target tenant's own audit chain, so it appears in `GET /v1/admin/audit-events`, in that tenant's SIEM destinations, and under its hash chain. The tenant admin and the security team read the same records.

In the table, "Synchronous" means the mirror row is written before the action completes. "Sweep" means a periodic job later copies the event from the platform rows, as **Write path** below states.

| Source | Mirrored as | Timing |
| :-- | :-- | :-- |
| `admin.impersonation_started` | `operator_access.recorded` with `kind: impersonation_started`, reason, any `ticketId`, target user, and expiry | Synchronous before the bearer is signed |
| `admin.impersonation_ended` | `kind: impersonation_ended` | Sweep |
| Expiry of an impersonation row with no ended event | `kind: impersonation_expired` | Sweep. §13.3 rule 4 writes no event at expiry, so without this the customer sees a session with no end. |
| Revocation by another route (§13.3 rule 4) | `kind: impersonation_revoked` | Sweep |
| Platform-tenant events carrying `target_tenant_id` under CMP-058, such as `compliance.profile_decommissioned` and `gdpr.legal_hold_overridden_tenant` | `kind` naming the event | Sweep |
| A platform admin's write to a resource of this tenant, from `EmitAdminEvent` | `kind: admin_write` with the resource and event type, with no body | Sweep |
| The create, update, or delete of an interceptor registration that lists this tenant under [RT-6](../0141-runtime-routing/change.md#rt-6-tenant-scoped-interceptors), before or after the write | `kind: admin_write` with the registration's name, phases, priority, `failPolicy`, and Service name, so a tenant also sees its removal from the list | Sweep |
| A platform admin's read that names this tenant, such as `GET /v1/admin/audit-events?tenantId=` | `kind: admin_read` with the endpoint | Sweep, coalesced per subject, endpoint, and hour |
| `GET /v1/sessions?tenant=` by a platform admin | `kind: admin_read` with the endpoint and result count | `handleList` emits no audit row today, because it sets a named tenant rather than `__all__`. It gains `admin.tenant_sessions_read`, emitted before it returns rows, and it returns no list when the emit fails, following §13.3 rule 7. Then sweep. |
| A platform admin's grant or revocation of a placement offering for this tenant ([PM-3](../0136-placement/change.md#pm-3-placement-offerings-and-per-pool-nodeplacement)) | `kind: admin_write` with the offering's name, its node tenancy, and whether it was granted or revoked | Sweep |
| The audit events of the tenant security settings ([TSS-3](../0150-tenant-security-settings/change.md#tss-3-admin-api)): `tenant.security_settings_tightened`, `tenant.security_settings_relaxation_requested`, `tenant.security_settings_relaxed`, `tenant.security_settings_relaxation_cancelled`, `tenant.security_template_applied`, and `tenant.key_state_changed` | `kind` naming the event, with the previous and new version, each changed setting's previous and new value, the caller as an operator reference, the justification, and any `ticketId` | Synchronous, written before the write's response returns or the applier's activation commits |
| `forensics.*` events | Not mirrored, unless a case discloses them | [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/change.md) states this carve-out. |
| `cross_tenant_read` from a call that names no tenant, such as `tenant_list` | Not mirrored | The event stays in the platform chain, and posture reports the count. |

**Write path.** §11.7's write-time validation rejects a row whose tenant differs from the request's scope, so the mirror is an explicit path that derives the tenant from `target_tenant_id` and writes only this event type. Under CMP-058 the source row can sit in a regional platform Postgres that differs from the tenant's audit shard, so the two writes cannot share a transaction. The ordering is:

1. The started event commits under its existing rule.
2. The mirror row commits on the tenant's chain under the per-tenant advisory lock, carrying `source_event_id`.
3. The bearer is signed and recorded as §13.3 rule 1 states.

A failure at step 2 returns an error and issues no bearer, consistent with §11.7's rule that audit must be durable before an externally observable side effect. A sweep that reads platform rows past a per-region high-water mark mirrors everything else. It deduplicates on `(tenant_id, source_event_id)`, so a restart repeats nothing. §13.3 rule 3 keeps its property that an audit outage never blocks a revocation, because the ended mirror is a sweep.

**Attribution of actions.** The impersonation bearer gains an `impersonated_by` claim. The bearer's subject stays the target user, as §13.3 requires, and §10.2 says a `user_bearer` carries no `act` claim, so the design uses a distinct claim. The principal carries it into the audit context, and each audit row under the bearer adds `unmapped.lenny.impersonated_by` in OCSF. A customer can then filter the actions an operator took, with the Lenny actor still recorded as the user impersonated.

**Operator identity.** Tenant-chain records carry an operator reference: a keyed hash of the admin's subject, with a per-tenant key, so a customer can tell two operators apart without learning names and two customers cannot correlate one operator. The platform chain keeps the subject. `security.operatorAccess.identityDisclosure` set to `named` writes the subject instead (decision 13).

#### SO-4b: approval

With `operatorAccess.approval: required` in the tenant security record, `POST /v1/admin/impersonation` needs an approved request. With no holder of `security-approver`, the write that sets `operatorAccess.approval: required` is refused with `422 NO_APPROVER`, as a matching `approvals.tools` rule is under SO-5a, and a later removal of the last holder while it is set raises `SecurityApproverCoverageLost`.

| Method and path | Caller | Behaviour |
| :-- | :-- | :-- |
| `POST /v1/admin/operator-access-requests` | `platform-admin` | Body: `{"targetTenantId", "scope", "targetUserId", "reason", "ticketId", "durationSeconds", "emergency"}`, where `reason` is required and `ticketId` is optional. `scope` is `impersonation`, which needs `targetUserId`, or `session_read`. Creates a `security_approvals` row of kind `operator_access`, mirrored at once. |
| `GET /v1/admin/operator-access-requests/{id}` | `platform-admin` | Returns the state. |
| `POST /v1/admin/impersonation` | `platform-admin` | Gains `accessRequestId`. Refused with `403 OPERATOR_ACCESS_APPROVAL_REQUIRED` when the tenant requires approval and the request is absent, unapproved, expired, used, names another target or scope, or was made by another person: the person behind the bearer, `impersonated_by` when present and `sub` otherwise, must be the request's `requested_by`. Refused with `403 OPERATION_NOT_PERMITTED_FOR_RESOURCE` for an `adapter-only` token or a narrowed request, under "Minting refusals" in "RO-4" of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md#ro-4-authorization-of-adapter-requests). 0138.S2 builds that refusal, and 0142.S5 keeps it. |
| A platform admin's session content read | `platform-admin` | Carries the request as an `X-Lenny-Access-Request` header, with `scope: session_read`, and is refused with the same error otherwise, including when the caller is not the request's `requested_by`. The approval stays usable for its `durationSeconds` by its requester alone, and each read is mirrored. |

The customer resolves the row through the approvals API under SO-5, which shows the approver the requester's operator reference. An unanswered request expires after `security.operatorAccess.requestTtlSeconds` (default 43200) and grants nothing. An impersonation approval is single use, and `durationSeconds` keeps §15.1's 3600-second maximum.

**Emergency path.** With `operatorAccess.emergencyAccess: allowed` in the security record, the default, the request may carry `emergency: true` with a justification. The row is approved on creation with no customer action, and either scope then uses it as above, so one carrier serves both paths. The mirror row records `emergency: true`, `operator_access.emergency_used` fires critical on the tenant's chain and subscriptions, and `OperatorEmergencyAccess` alerts the operator. A tenant that sets `operatorAccess.emergencyAccess: forbidden` has no such path, and its security team accepts that the operator cannot impersonate during an incident. Platform operations that need no bearer are unaffected.

**Coverage.** Approval gates impersonation, and it gates a platform admin's read of a tenant's session content, which reaches `GET /v1/sessions?tenant=` and the session read routes today. The gated routes are `GET /v1/sessions?tenant=` and every route whose access check is read access to the session the path names, which the API front resolves through `GWSEAM-AuthorizeSession` of the gateway seam interface page: session reads, transcript and event stream reads, and file and artifact downloads. The gate sits in that resolution, and in `GWSEAM-SessionAccessible` for a session record the platform tools seam reads itself, for a `platform-admin` caller reading a session of another tenant. A content route added later inherits the gate through its access check. Metadata operations, such as listing tenants, force-terminate, suspend, and pool changes, stay ungated because operating the platform needs them, and they are mirrored.

**Tenant-scoped interceptors.** Approval does not cover the registration of an interceptor that lists a tenant under [RT-6](../0141-runtime-routing/change.md#rt-6-tenant-scoped-interceptors). Decision 15 limits approval to impersonation and a platform admin's read of session content, and a registration is a platform admin write, which SO-4a mirrors to each listed tenant and posture's `interceptors` section lists (decision 31). An interceptor on a phase whose payload carries session content receives that content from the listed tenant's sessions with no approval, which "Residual risks" states. The owner settled this rule and that risk on 2026-11-09, in the word "ok" (decision 31).

**Failure behaviour.** When the approvals store is unreachable, a tenant requiring approval gets no bearer and the emergency path still needs the mirror write, so an audit outage stops impersonation entirely, as it does today.

### SO-5: delegated approvers

#### Existing behaviour

The runtime sets `approvalRequired` on a `tool_call` frame (`schemas/lenny-adapter-jsonl.schema.json`), and `maybeGateToolCall` in `pkg/gateway/core/session/executor/podstream.go` holds the call only when the frame reaches it with that flag. Two paths never reach it. `HandleToolCall` in `pkg/adapter/toolcall.go` answers adapter-local tools inside the adapter, and §28.5.3 Intra-pod sends platform and connector tools over MCP at the Standard and Full levels, with stdin `tool_call` reserved for adapter-local tools. So `maybeGateToolCall` sees only a relayed frame, and `lenny/delegate_task` from a Standard runtime is never gated. `HandleToolCall` also ignores `approvalRequired`, although the schema says a flagged call blocks, which is filed as F-7.2.46.

No platform policy forces approval. §15.2 refers to a policy that does not require approval, and a search of the specification finds no definition of one for tool calls. `approvalMode` in the §8 delegation lease example is the nearest field. `ToolApprovalGate` (`pkg/gateway/platformtools/toolapproval.go`, behind `seam.ToolApprovalGate`) records an interaction keyed on session, owning user, and call in the interaction store, the gateway's shared store of pending session interactions such as held tool calls, polls the store across replicas, and treats a timeout as denial. §10.2 **Session ownership.** rule 5 limits resolution to the owner.

#### Approvals store and API

```sql
CREATE TABLE security_approvals (
    id           UUID PRIMARY KEY,
    tenant_id    TEXT NOT NULL,
    kind         TEXT NOT NULL,   -- tool_use | repository_write | operator_access
    session_id   TEXT,
    subject      JSONB NOT NULL,  -- the tool and call, repository and refs, or access request
    requested_by TEXT NOT NULL,
    state        TEXT NOT NULL,   -- pending | approved | denied | expired | revoked | withdrawn
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

These routes sit on the admin surface, so the session surface keeps its existence-hiding rule, under which a caller that may not act on a session receives `404 RESOURCE_NOT_FOUND` (§10.2, **Session ownership.** rule 7), and the approver gains no other act or read on the session. §10.2 rule 5 gains one sentence stating this exception. A self-approval returns `403 APPROVAL_SELF_FORBIDDEN`: the approvals API compares the approver's subject with the requester's subject and with the session's owner. This follows §11.2.1's dual-control rule for billing corrections, under which a second `platform-admin` approves a correction. A call under an impersonation bearer returns `403 FORBIDDEN`. Every decision emits `approval.resolved` with the decider.

**Withdrawal on suspension.** When a suspension stops a session's turn, a `tool_use` approval of that session that is still `pending` moves to `withdrawn` before the paused state's write returns, under L29 and SLR-27 of [`0146-session-lifecycle`](../0146-session-lifecycle/change.md). The gateway emits `tool_use` with the phase `withdrawn` and the audit event `approval.withdrawn`, `approvals.timeoutSeconds` no longer answers the row, and a later `approve` or `deny` on it, on the approvals API or the owner's route, answers `404 RESOURCE_NOT_FOUND`. A pending approval counts as activity for the session's idle clock.

#### SO-5a: tool-use approvals

`approvals.tools` rules match a platform tool name. Every platform call reaches the gateway from a `tool_call` frame on a runtime connection or a tool connection of the session's slot, which the adapter forwards over `LNK-GWCONTROL` with `CallPlatformTool` ([RP-3](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames)). Revisions before 56 also matched connector tools forwarded with `CallConnectorTool`; [`0124-connector-removal`](../0124-connector-removal/change.md) removes connectors under the owner's decision of 2026-11-15 ("Yes, let's drop connectors."), and SO-5a's own rule validation refuses the write of a rule that names a `connector:<id>` server with `400 VALIDATION_ERROR` naming the field, because a rule matches only names on the `platform` server. The refusal follows the error form of the one rule for removed fields (D6 of [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/decisions.md#decisions)), and it is not one of the removed-field refusals that 0124 lists under D7. Enforcement sits at the gateway's tool dispatch, which calls `ToolApprovalGate.AwaitApproval` before the tool runs, whatever the runtime's flag says, and routes by the rule's `approver`. A denial returns a `tool_result` with `isError: true`.

- With `approver: security`, the gate writes a `security_approvals` row and an interaction, and the owner's `tool-use/{id}/approve` returns `403 APPROVAL_ROUTED_TO_SECURITY`. The owner's `deny` still works, because a user declining their own session's action needs no oversight.
- `AwaitApproval` keeps its wait, polling the interaction store, and the approvals API writes the same resolution, so the cross-replica path is unchanged. The wait runs off the session's frame reader, which never blocks on a tool-use approval (MDR-22 of [`0135-message-delivery`](../0135-message-delivery/change.md)), so the runtime's other frames and the messages delivered to it keep flowing while a call is held, and a message never resolves an approval.
- Timeout uses `approvals.timeoutSeconds` and denies. With no holder of `security-approver`, the write of a matching rule is refused with `422 NO_APPROVER`, and a later removal of the last holder raises `SecurityApproverCoverageLost`.
- `showArguments: true` shows `arguments` to the approver and emits `approval.viewed`. With `false`, the approver sees the tool name and session only.

**Operations in rules.** A rule that concerns children matches the platform operation of [`0166-delegation-primitives`, "DP-1"](../0166-delegation-primitives/change.md#dp-1-session-grant), such as `session.create`, `session.set_limits`, `session.transfer`, or `session.release`, because the reference tools of 0166 "DP-5", such as `start_session`, run in the reference MCP bridge and reach the gateway only as those operations. A write of a rule that names a reference tool of 0166 "DP-5", such as `start_session`, `await_sessions`, `transfer_session`, or `release_session`, is refused with `400 VALIDATION_ERROR` naming the field, in the form of the refusal of a `connector:<id>` rule above. `start_child_session` and the other tool names of MD-23 are refused the same way, because no tool of that name reaches the gateway.

**Conditions on a child (`when`).** A rule that matches `session.create` may carry `when`, whose members all hold before the rule applies, so a rule without `when` holds every child **[design]**:

- `targets`: a list of runtime names, each a name or a pattern with a trailing `*`, matched against the child's final runtime.
- `targetLabels`: a map of runtime labels, matched when the child's final runtime carries every one, as a `DelegationPolicy` selects runtimes by label.
- `llmCreditsAbove`: an amount in `llmCredits`, matched when any limit that counts the `llm` meter exceeds it, in the child's carved budget ([`0170-budgets`, "BG-8"](../0170-budgets/change.md#bg-8-carves-for-delegated-children)), or, when no carve funds the child, in the child's session budget as "BG-2" of 0170 resolves it (decision 46). Every limit of a parent's session budget is carved under 0170 decision 55, so a carve always holds an amount for each limit that counts `llm`. A limit in money is converted to `llmCredits` at the lowest price per credit among the upstreams the child can reach, under the price lookup of 0170 "BG-5", so the amount the rule reads is the most credits the limit can buy (decision 47).

The gateway evaluates a `session.create` rule at the post-chain point of [RT-2](../0141-runtime-routing/change.md#post-chain-point), on the final runtime and the resolved carve, because a `PreRoute` interceptor can change the target after the call names one. The same rule also holds a `session.set_limits` call that raises a child's carve above `llmCreditsAbove`, and a lease extension of [§8.6](../../spec/08_recursive-delegation.md#86-lease-extension) that would take a child's carve above it, before the gateway reserves the added amount, for a child whose runtime `targets` and `targetLabels` cover, so a parent cannot create a child below the amount and grow it afterwards without approval, whether by a raise or by an extension in `auto` mode (decision 46). It also holds a `session.release` of a child whose budget crosses the amount, and a lease extension of a released session whose lineage shows that it was created under the rule, which would take it above the amount, so a release does not carry a child out of the rule's reach (decision 47). `when` is refused with `400 VALIDATION_ERROR` naming the field on a rule that matches any other tool. Narrowing `targets`, adding a key to `targetLabels`, or raising `llmCreditsAbove` is a weakening, and removing `when`, widening `targets`, removing a key from `targetLabels`, or lowering `llmCreditsAbove` is a tightening. With `showArguments: true` the approver sees the target, the limits, and the task.

**Parent tools (`parentTools`).** A tool a parent offers its child under [`0166-delegation-primitives`, "DP-8"](../0166-delegation-primitives/change.md#dp-8-parity-with-a-client) is not on the `platform` server, so `approvals.tools` does not match it, and its call reaches the parent as a `tool_call` child event rather than through tool dispatch. `approvals.parentTools` holds rules of the same form as `approvals.tools`, `match`, `approver`, and `showArguments`, where `match` names a tool the parent offers or is `*` for every one **[design]**. The gateway holds a matching call before it delivers the event to the parent. `approver: owner` asks the root session's user through the owner's `tool-use/{id}/approve` route, and `approver: security` asks a `security-approver` through the approvals API, as for `approvals.tools`. A denial or a timeout under `approvals.timeoutSeconds` answers the child's call with an error result, and the parent never sees the call. The child's call stays open while it is held, and the hold time of [`0122-client-tools`](../0122-client-tools/change.md) counts from the delivery to the parent. Relaxing or removing a `parentTools` rule is a weakening, and the write of a rule with `approver: security` and no holder of `security-approver` is refused with `422 NO_APPROVER`.

**Tools inside the runtime are outside these rules.** The adapter-local tools of earlier revisions are retired (RP D5), so no workspace tool reaches the adapter or the gateway, and the runtime reads and writes its own workspace directly. A tool a runtime runs inside its own process never reaches a gate, and the documentation states this limit. A rule matches only names on the `platform` server. Whether a harness approval that a runtime forwards with a `request` frame of `kind: "approval"` ("MD-19" of [`0135-message-delivery`](../0135-message-delivery/change.md)) falls under these rules is open decision 37.

**Client tools are outside these rules.** A client tool of [`0122-client-tools`](../0122-client-tools/change.md) is not on the `platform` server, so no `approvals.tools` rule matches it. The hold on relayed frames relies on `maybeGateToolCall`, which meets CTF-1 and CTF-2 of that design (F-28.5.3.1, a relayed `tool_call` that is neither adapter-local nor approval-gated is discarded with no `tool_result`, and F-28.5.3.2, an approval re-sends the runtime's own `tool_call` to the runtime, a direction §28.5.3 does not define). 0142.S6 cites both, and builds the hold on the `tool_call` and `tool_result` frames of [RP-3](../0139-runtime-protocol/change.md#rp-3-tool-calls-on-frames).

**Tools of operator services are outside these rules.** A harness that calls an operator's MCP service speaks MCP to it over HTTP through `netagent` ("CR-3" of [`0124-connector-removal`](../0124-connector-removal/change.md)). No Lenny component sees the call as a tool call, so `approvals.tools` cannot hold it. Approvals, tool rules, and per-call audit for those tools are the operator service's job, under the owner's direction of 2026-11-15, and the service can ask a person through MCP elicitation, which reaches the user as a question under B6 of [`0135-message-delivery`](../0135-message-delivery/decisions.md#decisions). Lenny records the traffic in the egress evidence streams: `egress.tunnel` as `enforced` and `egress.local` as `self_reported`. The documentation states this limit.

#### SO-5b: repository write approvals

The outline proposed an approval-required flag on the session's `writableRepositories` field. That flag cannot live on the session field, because the client writes that field and an approval requirement set by the client constrains nothing. The requirement is in `approvals.repositoryWrites`, and the session field keeps its meaning.

1. At session bind, the gateway records the writable set under [EG-3](../0127-egress-and-git/steps.md#steps). An entry matching a rule is stored with `state: pendingApproval`, and a `security_approvals` row of kind `repository_write` is created naming the repository and any `allowedRefPatterns`, the refs the session may push. A rule added later applies to bound sessions as SO-2 states.
2. The session runs with read access. A push reaches `netagent`, which asks the Token Service over the pod credential API, and both mint checks refuse to mint write scope for an entry in that state. `netagent` returns a 403 with the decision identifier and a git message saying that approval is pending, and `egress.local` records the refusal.
3. An approver approves. The entry moves to `granted` with an expiry of `ttlSeconds`, and the gateway pushes a binding change, so the next push mints. The approval writes only the entry's `state` and leaves 0152's `userAccess` unchanged.
4. At expiry or on revocation the entry returns to `pendingApproval`, which writes only the entry's `state` and leaves 0152's `userAccess` unchanged, the binding change flushes `netagent`'s cached write token, and a new push is refused. A token already minted stays valid at the host until its own expiry. `netagent` holds it, and the agent never does.

**Approval state and the user verdict.** The states above, `granted` and `pendingApproval`, are SO-5b's `state` field of the writable set entry. The user verdict of [UG-2](../0152-user-git-identity/change.md#ug-2-repository-authorization), `userAccess` (`allowed` or `userRefused`, with its `verifiedAt`), is a separate field of the same entry that [`0152-user-git-identity`](../0152-user-git-identity/change.md) writes. An approval, its expiry, or its revocation changes `state` alone and never `userAccess`. Every enforcement point admits a write only when `state` is `granted` and `userAccess` permits it, and a read only when `userAccess` permits it. Whether a later `allowed` verdict clears `userRefused` in a running session is open decision 54 of [`0152-user-git-identity`](../0152-user-git-identity/decisions.md#open-decisions), and this design writes neither value.

**Credential bindings on the same host.** A binding can reach a repository through the host's REST API with no git path involved, such as a GitHub App pool with `contents: write` bound to `api.github.com`, and the egress design mints those tokens for the session's repositories with the binding's permissions. Two rules close this for tenants with a rule:

- A binding mint with any write permission names only repositories whose writable set entry is `granted`. A GitHub token has one permission set across its repositories, so a pending repository is absent from that token, and the session reaches it for reads through a read-only binding or the git endpoint. See "Changes requested".
- A stored token or a non-`mint-scoped` provider cannot be limited this way. The guardrail check refuses or suspends, under `approvals.repositoryWrites`, any binding of that kind whose `hosts` include an API host of a rule's git host, taken from the provider template or the VCS pool. Delivery does not change this.

Approval is per session and repository. An unanswered request expires at `approvals.timeoutSeconds`, a later push is refused with the same message, and a client calling `POST /v1/sessions/{id}/write-approvals` opens a new row. Stored-token push and GitLab token reuse pass through the same Token Service and `netagent` checks.

### SO-6: F-25.6.9, certificate windows and the remedy

#### Defect

`detectCertExpiring` in `cmd/lenny-ops/doctorremediator.go` matches each Certificate within `certExpiryWindow` (7 days) with no `isCA` check, and `applyCertExpiring` annotates it and deletes its Secret. `charts/lenny/values.yaml` sets 24-hour leaves that renew 8 hours before expiry, so each healthy leaf always matches. `classifyCert` in `pkg/ops/opsservice/certmanager.go` reports `unhealthy` inside the same 7 days. §25.6 Diagnostic Endpoints and §25.8 "Cert-Manager Integration" specify both, so the specification changes with the code. `lenny-mtls-ca` keeps its key in its Secret (`mtls-pki.yaml`), so a matched CA loses its key.

#### Relative windows

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

#### Remedy

`certManagerExpiring` matches a Certificate in `degraded` by time with Ready true, `isCA` false, no `lenny.dev/doctor-optout`, and no current `Issuing=True` condition. The fix sets `Issuing=True` with reason `ManuallyTriggered` through the `certificates/status` subresource, which is what `cmctl renew` does (see "Evidence"). cert-manager then re-issues into the same Secret, and the existing certificate stays in place until the new one arrives. The fix deletes no Secret and sets no `cert-manager.io/issue-temporary-certificate` annotation.

SO-6 adds `update` on `certificates/status` to the `lenny-ops` Role, to §25.4, and to the preflight list, so the remedy works when SO-6 lands with SO-7 still ahead.

An `isCA` Certificate in `degraded` or `unhealthy` produces `certManagerCaExpiring` with `remediation: "manual"` and a pointer to the `ca-rotation` runbook, and `diagnostics.fix_skipped` records `reason: is_ca`. Failure of the status write records `diagnostics.fix_failed` and changes nothing.

The removal of the annotation has a second reason. cert-manager documents that annotation as placing a temporary certificate on the Secret until the final one returns. A temporary certificate in an mTLS leaf's Secret is a risk this design has not measured, and the new remedy does not need the annotation. [The egress design](../0127-egress-and-git/change.md)'s `lenny.dev/doctor-optout` annotations remain valid as a hard skip.

### SO-7: F-25.4.40, `lenny-ops` rights

#### Current use

`charts/lenny/templates/ops-rbac.yaml` grants `get`, `list`, `watch`, and `delete` on `secrets` with no `resourceNames`, and `get`, `list`, `watch`, and `patch` on `certificates`. A search of `cmd/lenny-ops` and `pkg/ops` finds two uses of Secrets through the API: `List` in `versionsource.HelmChart` and `Delete` in `applyCertExpiring`. `get` and `watch` have no caller there, and `pkg/preflight/ops_readiness.go` lists `secrets: get` only for "Secret reads for backup credentials", which `lenny-ops` does not perform through the API. Helm's release record also holds `config` and `manifest`, so narrowing to the release Secret would still expose the chart's values, which can include `audit.siem.secret`.

#### Change

| Rule | Today | After SO-7 |
| :-- | :-- | :-- |
| `secrets` | `get`, `list`, `watch`, `delete` | Removed |
| `certificates` | `get`, `list`, `watch`, `patch` | `get`, `list`, `watch` |
| `certificates/status` | `update`, added by SO-6 | Unchanged |
| `deployments` | `get`, `list`, `watch`, `patch`, `update` | `get`, `list`, `watch` |
| `deployments/scale` | `get`, `patch` | `get` |
| `jobs` and all others | As rendered | Unchanged |

- **Deployment writes (decision 18, F-25.6.10).** No code in `cmd/lenny-ops` or `pkg/ops` patches, updates, or scales a Deployment in the release namespace, so the writes go. The chart comment names the §25.9 upgrade orchestration as their reason, and a later design that needs Deployment writes for it adds them back. The `coreDnsStuckEndpoint` remedy patches `kube-system/coredns` today, where `lenny-ops` holds no right, and it gets no right in `kube-system`, because `patch` on that Deployment, even by name, lets `lenny-ops` rewrite cluster DNS for every tenant. Under decision 18, the remedy either targets the agent CoreDNS Deployment that the chart renders in the release namespace, with `patch` on that Deployment by `resourceNames`, or reports the fix as advice with `remediation: "manual"`. 0142.S9 builds one of the two that decision 18 allows.

- **Version source.** `versionsource.HelmChart` reads the `helm.sh/chart` label on the `lenny-ops` Deployment, which `lenny.labels` stamps and `lenny-ops` can already `get`. It strips the chart name and restores `_` to `+`, which is reversible because SemVer permits no `_`. This also reports a version on an install rendered with `helm template`, where no release Secret exists. §25.8 "Version Aggregation" changes its line naming the `helm.sh/release.v1` Secret.
- **Preflight.** `CanonicalOpsSARBACRules` drops the `secrets: get` row.
- **Specification.** §25.4 "ServiceAccount and RBAC" shows a Role with no `secrets`, `certificates`, or `endpoints` rules, which the chart has. `TestOpsRBACMatchesSpecCanonicalBlock` (`tests/static/chart/ops_rbac_spec_sync_test.go`) is skipped for that reason, with an entry in `tests/registers/skip-reasons.yaml`. The test reads the specification's prose block, which `test-coverage.md` forbids ("No test matches specification prose"). SO-7 reconciles the block, deletes the test and its register entry, and replaces it with a `static` test that renders the chart and asserts the exact rule set of the `lenny-ops` Role in the table above.
- **Level 1.** The rows of [EG-7](../0127-egress-and-git/steps.md#steps) (level 1 replacement) for `lenny-ops` (the probe ConfigMap and its client certificate for the pod credential API) add no Secret rule, so they do not conflict with this change.

#### Statement for §25.4

§25.4 gains a **PKI trust boundary** paragraph: `lenny-ops` remains inside the trust boundary of the PKIs whose key Secrets are in its release namespace, because `jobs: create` lets it run a pod template that mounts any Secret there. A compromise can therefore obtain a CA key, and it has to create a workload to do so, which leaves a Kubernetes audit record and a visible object. The removal of the Secret and Certificate rights and of the Deployment writes is defense in depth and closes the direct read, the delete of any Secret, the rewrite of a Certificate's names, and the rewrite of a Deployment's pod template. It does not move `lenny-ops` out of the boundary (decision 18).

[The egress design's "Transport security"](../0127-egress-and-git/change.md#transport-security) says that `lenny-ops` can read Secrets and patch Certificates. That sentence describes today, and it stays true until SO-7 lands.

### Configuration by scope

| Setting | Description | Scope | Managed by | Default | Notes |
| :-- | :-- | :-- | :-- | :-- | :-- |
| `security.config.minWeakeningDelaySeconds` | Floor under each tenant's weakening delay. | Deployment | Platform admin (Helm) | 3600 | The admin API cannot change it. |
| `security.config.maxWeakeningDelaySeconds` | Ceiling on each tenant's weakening delay. | Deployment | Platform admin (Helm) | 604800 | Raising the delay applies at once, so it needs a bound. |
| `gateway.interceptorWeakeningCooldownSeconds` | The cooldown after a weakened content scan, which refuses `session.create` with `files`, `materialize` pushes, and `files.import` on the affected sessions. | Deployment | Platform admin (Helm) | 60 | Existing value of §8.3, between 30 and 600. It now covers every transfer path of 0166 decision 45. |
| `security.operatorAccess.identityDisclosure` | Whether tenant records name the operator or carry a per-tenant reference. | Deployment | Platform admin (Helm) | `pseudonymous` | See decision 13. |
| `audit.siem.endpoint`, `secret` | The operator's own SIEM copy. | Deployment | Platform admin (Helm) | Empty | Unchanged. |
| `audit.siem.maxDestinationsPerTenant` | Bound on tenant destinations. | Deployment | Platform admin (Helm) | 2 | |
| `audit.siem.tenantMaxHoldDays` | How long past retention a failing tenant destination holds records for an unregulated tenant. | Deployment | Platform admin (Helm) | 7 | Regulated rule under "Retention hold". |
| `audit.siem.tenantFailingAfterMinutes` | Time before a destination becomes `failing`. | Deployment | Platform admin (Helm) | 15 | |
| `siemForwarder` replicas and resources | Sizing of `lenny-siem-forwarder`. | Deployment | Platform admin (Helm) | Chart | The chart omits the Deployment when `siemForwarder.enabled` is false. |
| `security.operatorAccess.requestTtlSeconds` | Lifetime of an unanswered operator access request. | Deployment | Platform admin (Helm) | 43200 | |
| `admin.doctor.allowedFixes` | Fixes `doctor --fix` may apply. | Deployment | Platform admin (Helm) | Existing list | Existing value. |
| `securityRoleSource` | Whether security roles come only from Lenny's table or also from an OIDC claim. | Tenant | Platform admin (admin API) | `managed` | See decision 2. |
| Security principals | Who holds each security role. | Tenant | `security-admin`, and a platform admin only for a `security-admin` when none is active | None | A tenant admin can read them. Most changes take the delay. |
| `lifecycleSubjects` | Service subjects, such as a directory sync worker, that may invalidate and reinstate a protected subject. | Tenant | `security-admin` | Empty | Adding one is a weakening. A subject entry must be a Lenny subject in the `~svc~` namespace of [`0143-service-account-credentials`, "SA-6 Subject namespaces"](../0143-service-account-credentials/change.md#sa-6-subject-namespaces), and a write naming any other subject is refused with `422 VALIDATION_ERROR`. This contradicts the `scim:<source_id>` entries that the posture `identity` section, the **IdP removal** residual risk, and TS-8 of [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md#scim-server-ts-8) rely on, and open decision 36 settles which entries the list accepts. |
| `auditorStreamGrants` | Sensitive evidence streams `security-auditor` may read. | Tenant | `security-admin` | Empty | Adding one is a weakening. |
| `identityProvider.*` in the RBAC config | The tenant's token introspection endpoint and credentials. | Tenant | `tenant-admin` until the tenant has a security principal, then `security-admin` or a platform admin | Unset | See decision 23. |
| `credentialDelivery.forbid` | Credential binding deliveries the tenant admin cannot use. | Tenant | `security-admin` | Empty | Applies on top of the platform gate. |
| `requestRules.requiredPresets` | Presets a binding on a matching host must keep, with a minimum version. | Tenant | `security-admin` | Empty | Relaxing is a weakening. See [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md). |
| `userAuthorization.require`, `.serviceIdentities`, `.forbidRuntimeHeld` | Forces user authorization on VCS pools, limits service identities, and forbids runtime-held credentials on them. | Tenant | `security-admin` | Absent | See [`0152-user-git-identity`, "Git and hosts"](../0152-user-git-identity/change.md#git-and-hosts), which under [UG-3](../0152-user-git-identity/change.md#ug-3-pull-request-authoring) owns `allowRuntimeHeldCredentials`, the field `.forbidRuntimeHeld` forbids. |
| `identityBinding.forbidUnverified` | Refuses link providers with no identity check. | Tenant | `security-admin` | Unset | |
| `tokenTrust.allowedRecords` | The token trust records a tenant may use. | Tenant | `security-admin` | Absent, which allows any record except an `issuerExchange` type's, which must always be listed | Adding is a weakening. A CA rotation or route move needs a new entry. See [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md). |
| `storedTokenPush` | Forbids `allowStoredTokenPush` and token reuse that depends on its gate. | Tenant | `security-admin` | Unset | |
| `verbatimClientFiles` | Forbids tenant client files, overlay templates, and the derived-runtime content that the `derivedRuntimes.forbid` values `workspaceFiles` and `gitClones` cover. | Tenant | `security-admin` | Unset | |
| `delegation` | Forbids delegation for every session of the tenant ([`0126-delegation-availability`](../0126-delegation-availability/change.md), D5), so `session.create`, `session.transfer`, `session.release`, and parent tool calls are refused. | Tenant | `security-admin` | Unset | Setting it is a tightening, and removing it is a weakening. |
| `parentTools` | Forbids the tools a parent offers its child ([`0166-delegation-primitives`, "DP-8"](../0166-delegation-primitives/change.md#dp-8-parity-with-a-client), decision 37). | Tenant | `security-admin` | Unset | Setting it is a tightening that withdraws offered tools, and removing it is a weakening. |
| `offerTools` | Forbids `offerTools: allow`, so a fenced child never receives its parent's tools (0166 decisions 31 and 37). | Tenant | `security-admin` | Unset | Setting it is a tightening, and removing it is a weakening. |
| `delegationLeave.max`, `.maxDepth` | The most a session of the tenant may leave its parent, `none`, `transfer`, or `release`, and the deepest depth at which it may leave ([`0166-delegation-primitives`, "DP-2"](../0166-delegation-primitives/change.md#dp-2-parent-link), decision 37). | Tenant | `security-admin` | Absent, which leaves the platform defaults `transfer` and 1 of 0166 | Lowering either is a tightening, and raising or removing either is a weakening. Neither widens what the other inputs allow. |
| `derivedRuntimes.forbid` | Forbids tenant-derived runtimes, or named parts of them. | Tenant | `security-admin` | Unset | |
| `tenantRegisteredServices` | Forbids tenant-registered upstreams and git endpoints, and outbound connectors and outbound hosts. | Tenant | `security-admin` | Unset | |
| `shadowPolicy` | Forbids the shadow policy. | Tenant | `security-admin` | Unset | |
| `crossEnvironmentSharing` | Forbids cross-environment sharing. | Tenant | `security-admin` | Unset | Removing it is a weakening, and a tenant admin turning the tenant setting on takes the same delay. |
| `hostPatterns.neverAllow` | Host patterns no tenant policy can allow. | Tenant | `security-admin` | Empty | |
| `hostPatterns.allowOnlyWithin` | The largest host set a tenant policy can allow. | Tenant | `security-admin` | Unset | Bounded by the pool ceiling. |
| `weakeningDelaySeconds` | Delay before a weakening of the security record, or a relaxation of the tenant security settings, takes effect. | Tenant | `security-admin` | The floor | Lowering it is a weakening. Bounded by the floor and the ceiling. One value serves both records (TSS O5 (b)). |
| `placement.requireNodeSeparation`, `.nodeTenancy`, `.allowSharedPod`, `.allowReusedPod` | The tenant's own placement requirements, which narrow the pools its sessions may use. | Tenant | `security-admin` | Absent | Adding one applies at once, and removing one is a weakening. See [PM-4](../0136-placement/change.md#pm-4-placement-requirements). |
| `operatorAccess.approval` | Whether impersonation needs the customer's approval. | Tenant | `security-admin` | `off` | Setting `required` with no holder of `security-approver` is refused with `422 NO_APPROVER`. |
| `operatorAccess.emergencyAccess` | Whether the emergency path exists. | Tenant | `security-admin` | `allowed` | Setting `forbidden` is a tightening. |
| `approvals.tools` | Platform tools that need approval and who resolves them, with `when` conditions on a child's target runtime and carved amount in `llmCredits` for `session.create`. | Tenant | `security-admin` | Empty | Applies to frames the gateway sees, and to `session.create` at the post-chain point. |
| `approvals.parentTools` | Tools a parent offers its child that need approval, by name or `*`, and who resolves them. | Tenant | `security-admin` | Empty | Applies before the call reaches the parent. |
| `approvals.repositoryWrites` | Repository patterns whose push needs approval. | Tenant | `security-admin` | Empty | |
| `approvals.timeoutSeconds` | Time before an unanswered approval denies. | Tenant | `security-admin` | 1800 | |
| SIEM destinations | The customer's endpoints, credentials, and streams. | Tenant | `security-admin` | None | Removal is a weakening. |
| Security-owned subscriptions | §25.5 webhooks only the security team can change. | Tenant | `security-admin` | None | Notification path. |
| Approval requests | A client's request for write approval on its session. | Session | Client | None | The client cannot approve it. |

## Worked scenarios

The examples use tenant `acme`, whose tenant admin is alice and whose security lead is bob, a second tenant `globex`, and carol, a platform admin at the SaaS operator. Dave is a developer at `acme`.

**Onboarding.** Bob's company signs a contract asking for separate oversight. Carol calls `PUT .../acme/security-principals/bob@acme.com/security-admin` with a justification naming the signed request. Bob assigns `security-auditor` to his analyst, which applies at once, and `security-approver` to two on-call engineers, which applies after the delay, with no further call to the operator. Bob reads the posture and sees a shared pool, default enforcement, and the `agentReadableDelivery` grant in use by one binding.

**A tenant admin tries to remove the auditor.** Alice calls `DELETE .../users/analyst@acme.com/role`. That route has no effect on the row in `tenant_security_principals`, so the role stays. She then calls `POST /v1/admin/users/analyst@acme.com/invalidate`, which returns `403 SECURITY_PRINCIPAL_PROTECTED` and emits an event bob's SIEM receives. She creates a custom role listing `security.config.write`, which is refused under the §10.2 ceiling, and a custom role named `security-admin`, which is refused as a reserved name. `PUT .../users/alice@acme.com/role` with `security-admin` returns `400`. She points `introspectionEndpoint` at her own server, which returns `403 SECURITY_CONFIG_PROTECTED`, and an endpoint she set before bob arrived receives neither his bearer nor a say in its validity. If alice deletes the analyst's account in the IdP, Lenny has no event for that. Bob sees the analyst's last authentication age in posture.

**Forbidding a delivery.** Bob runs `PUT .../acme/security-config?dryRun=true` with `credentialDelivery.forbid: ["file"]`, and the response lists `npm-publish-token`. He applies it. The binding is suspended, new plans naming it are rejected, and pods with it lose the file at the next binding push. Alice's attempt to create another binding with `delivery: file` returns `422 GUARDRAIL_VIOLATION`. `globex` keeps its binding with `delivery: file`, because each tenant has its own record and its own gate.

**A stolen credential weakens a rule.** An attacker holding bob's token removes the forbid at 02:00 with a justification. The change becomes pending for 24 hours, and the event reaches the SIEM and the on-call rotation at once. The attacker also asks to remove the second `security-admin` and to repoint the SIEM destination, and both requests wait under the same delay while the destination keeps delivering to its current URL. The second `security-admin` cancels all of them at 08:00, and carol invalidates bob's subject at bob's request. The binding stayed suspended throughout.

**A failing SIEM.** Bob registers `https://siem.acme.example/lenny`, verify succeeds, and it starts delivering after the delay. At the weekend the endpoint returns 503 for two days. Deliveries back off, the destination becomes `failing`, and a subscription notifies bob. `globex` and the platform copy see no change, and sessions continue. On Monday, delivery resumes from its cursor with no gap, because rows are still within retention. Had the outage outlasted retention plus seven days, a `siem.delivery_gap` record would name the missing range.

**An operator support case with approval.** Dave reports a stuck session and opens a ticket. Carol calls `POST /v1/admin/operator-access-requests` for dave, for 30 minutes. A record appears in `acme`'s chain, and one of bob's approvers approves it. Carol starts impersonation with `accessRequestId`, and the tenant chain shows the start before the bearer exists. Each action Carol takes carries `impersonated_by`. She does not end it, so at expiry the sweep writes `impersonation_expired`. A second attempt with the same request returns `403 OPERATOR_ACCESS_APPROVAL_REQUIRED`.

**The same case at `globex`.** `globex` left approval off. Carol's impersonation starts with no request, and `globex`'s chain and tenant admin still see it, because the mirror applies to every tenant.

**A protected repository.** Dave's session declares `github.com/acme/prod-api` in `writableRepositories`, which matches a rule. The session clones and works. Its push fails with a 403 whose git message says approval is pending and gives the decision identifier. An approver reads the repository and refs and approves. The next push succeeds. Dave holds `security-approver` as well and tries to approve his own request, which returns `403 APPROVAL_SELF_FORBIDDEN`.

**`doctor --fix`.** Carol runs `lenny-ctl doctor --fix`. Every 24-hour leaf is inside its window with no fault, so nothing matches, and the `certManager` component reports `healthy`. Later a leaf passes its renewal time by 3 hours with Ready true, and the fix sets `Issuing=True`. A CA nearing expiry is reported as `certManagerCaExpiring` with a pointer to the rotation runbook, and nothing changes it.

## Interactions with `0127-egress-and-git`

The hooks in [its "Reserved for other designs"](../0127-egress-and-git/change.md#reserved-for-other-designs) are inputs here, and the items below state how this design uses them.

| Egress design element | Use here |
| :-- | :-- |
| One write path for opt-ins, [EG-9](../0127-egress-and-git/steps.md#steps) | SO-2 fills the empty policy check. See "Changes requested" for the objects that have to pass through it. |
| Named permissions (`evidence.read.<stream>`, `egress.effectivePolicy.read`), [EG-8](../0127-egress-and-git/steps.md#steps) and [EG-9](../0127-egress-and-git/steps.md#steps) | SO-1 adds grants. `tenant-admin` keeps its defaults, so a tenant admin still debugs refusals. |
| Evidence envelope, [EG-10](../0127-egress-and-git/steps.md#steps) | SO-3 delivers streams translated to OCSF. `provenance` passes through, so a consumer can discount `self_reported` records. `payloadKeyRef` streams need a per-stream decision. |
| Gates on tenant opt-ins | Unchanged. A guardrail narrows inside a gate, and the platform admin keeps the grant. |
| Shadow policy, [decision 42](../0127-egress-and-git/decisions.md#decisions) | `shadowPolicy: forbid` uses the path that decision names. |
| Credential cache and binding push | Suspension and approval changes reach pods through the existing version bump and the push on the pod binding API, so no new pod path exists. |
| Mint checks, [EG-2](../0127-egress-and-git/steps.md#steps), on the git path of [EG-3](../0127-egress-and-git/steps.md#steps) and the bindings of [EG-4](../0127-egress-and-git/steps.md#steps) | Suspension and `pendingApproval` are read by both mint checks from their own stores, so a compromised decision service cannot approve a write or lift a suspension. |
| `lenny.dev/doctor-optout`, [EG-11](../0127-egress-and-git/steps.md#steps) | That design does not wait for SO-6. After SO-6, the annotation is optional for those Certificates. |
| PKI residual under "Transport security" | SO-7 narrows it as stated, and `lenny-ops` stays inside the boundary. |
| Regulated evidence write-ahead path, [EG-10](../0127-egress-and-git/steps.md#steps), under which the decision service waits for a regulated tenant's decision record to commit before the connection proceeds | Unchanged. It concerns the record's existence before a connection, and SO-3 concerns delivery afterwards. |
| Level 1 replacement | Posture reports it as the enforcement level. Guardrails that act in the decision service apply as level 1 applies them, and level 1's limits are that design's statement. |
| Egress evidence consumers | The "Evidence reads and export" row of [the egress design's "Configuration by scope"](../0127-egress-and-git/change.md#configuration-by-scope) keeps its default, and this design adds the security roles beside it. |

[`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/change.md) asks this document who may read evidence. Evidence reads are named per stream, so [FE-6](../0128-forensics-evidence-preservation/change.md#fe-6-agent-activity-streams) streams carry no default grant to a security role, and `tenant-admin` holds their read permissions by default, as that design's ["Who may read"](../0128-forensics-evidence-preservation/change.md#who-may-read) states. The `forensics.*` events of [FE-1](../0128-forensics-evidence-preservation/change.md#fe-1-quarantine) (quarantine) and [FE-3](../0128-forensics-evidence-preservation/change.md#fe-3-platform-only-holds-and-a-security-retention-class) (platform-only holds) are outside the SO-4a mirror unless a case discloses them, as that design's [decision 6](../0128-forensics-evidence-preservation/decisions.md#decisions) settles (decision 16).

## Changes requested of 0127-egress-and-git

Revision 37 of `0127-egress-and-git` applied the items below that had to land before its proposals, so they are history here rather than open requests.

`0127-egress-and-git` has applied the requests that revision 1 of this document made (revision 39 was re-read on 2026-11-03): the widened EG-9 write path, `suspendedBy` on opt-in objects with the guardrail layer, writable set entries as records, `lenny-evidence` reads from a named position with retention honouring a consumer's position, and `lenny-siem-forwarder` in the egress PKI's leaf list.

`0127-egress-and-git` revision 51 carries the `userCredentialDelivery` gate and the `requestRules` stage, which this design's guardrails act on through EG-9. They need no further request here. An earlier request has also been applied:

- **Binding mints and writable state (EG-4).** Applied in egress revision 46, which now states under "Credential bindings" that a binding mint carrying any write permission names only repositories with a `granted` writable set entry.

## Residual risks

- **Operator with infrastructure rights.** A person with cluster-admin, database superuser, or KMS (key management service) rights can read data, disable a check, or edit a record without passing through SO-4. §11.7 already states this for audit. The customer's own SIEM copy detects later tampering with delivered records, and it does not detect access that leaves no Lenny record. Approval governs the admin API path, and the documentation states this plainly so that a customer does not read it as a technical bar on the operator.
- **Delivery window.** A database superuser can change a row between commit and delivery, within the lag the destination reports.
- **Platform-reported posture.** Posture is the platform's statement about itself. A customer can check parts of it against evidence and audit records, and cannot check the rest independently.
- **IdP removal.** Deleting or disabling a security principal in the customer's IdP does not reach Lenny by itself. A SCIM deactivation through the SCIM server of [TS-8](../0149-tenant-scoping-identity/change.md#scim-server-ts-8) does only when the credential's subject is in `lifecycleSubjects`, and otherwise it is refused for that user and reported. Whether `lifecycleSubjects` can name a SCIM source at all is open decision 36. Posture shows the last authentication time, and `SecurityPrincipalCoverageLost` fires only on Lenny-side changes.
- **Platform recovery path.** A platform admin who invalidates every `security-admin` of a tenant can assign a new one after the delay. The tenant's chain, its SIEM, and any `tenant-admin` see that happen. A tenant admin's objection needs a second platform admin to override, which an operator that controls the deployment issuer can satisfy alone by minting a second subject, and the approval control is otherwise bypassable on this path.
- **Lockout by a stolen `security-admin`.** Tightenings apply at once, and raising `weakeningDelaySeconds` is a tightening. A stolen `security-admin` token can raise the delay to the ceiling, 604800 seconds by default, and then add tightenings such as `placement.nodeTenancy: dedicated` or `derivedRuntimes.forbid: ["all"]`. Undoing each one is a weakening that waits the raised delay, and a platform admin's write takes the same path, so the tenant can be held in the tightened state for up to the ceiling. Decisions 3 and 7 accept slow loosening, and this is its combined effect under a stolen credential.
- **Bearer capture before the security team exists.** An introspection endpoint a tenant admin set earlier keeps receiving other users' bearers, excluding principals, until a `security-admin` changes it. Posture reports the endpoint's host.
- **Operator's copy remains.** A tenant destination adds a copy, and the operator's `audit.siem.endpoint` continues to receive the tenant's chain.
- **Secrets already delivered.** Suspending a binding with `delivery: file` or `delivery: federation` removes it from the pod, and an agent that copied the value keeps the copy until it expires or is rotated.
- **User tokens.** A `userCredential` binding with `delivery: file` leaves a person's token in the agent's reach, and a guardrail or suspension does not reach a copy. Lenny cannot revoke a `registered` token at its provider. [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) lists these limits.
- **Request rules are a deny list by default.** A new provider operation passes until the preset is updated, even with `requiredPresets`. A guardrail requires a preset to be present, and it says nothing about what the preset names.
- **A tenant admin writing tokens.** `manage_user_credentials` can place a token under a user's name. It is audited and visible to the user, and it is not prevented.
- **Agent-held credentials.** SP11 covers tenant credentials. On a host a VCS pool or git route covers, an agent's own key is dropped for git and the mint is used, unless the session qualifies under `allowRuntimeHeldCredentials`. On an uncovered host, it can write through an opaque tunnel if the host is allowed, which the `credentialPinned` setting of [EG-12](../0127-egress-and-git/steps.md#steps) (exfiltration controls) narrows by refusing a request on a pinned host that has no binding. An `approvals.repositoryWrites` rule on a host no pool or route covers has no enforcement point, so the write of such a rule is refused. A binding on a host that is not the rule's API host, such as a CI system with its own repository access, is outside the check.
- **Minted write tokens.** A revoked repository approval stops new mints, and an existing token stays valid at the host until its expiry, held by `netagent`.
- **Runtime-internal tools.** A tool a runtime runs in its own process is outside SO-5a. Egress rules still apply to its network traffic.
- **Audit payload content.** §12.9 classifies audit events as Internal or Confidential, which includes personal data, and transcripts as Confidential ([TSS-8](../0150-tenant-security-settings/change.md#tss-8-data-classes)). This design has not established that no event type carries user content, such as a free-text reason or a dead-letter `raw_canonical_b64`. SO-1's proposal has to audit the catalog, and the auditor's "no session content" statement depends on that result.
- **Combined roles.** A person holding `tenant-admin` and `security-admin` defeats the separation for that tenant. Posture reports it, and Lenny does not refuse it.
- **`lenny-ops`.** It remains inside the PKI trust boundary after SO-7.
- **Interceptors.** A `platform-admin` who registers an interceptor for a tenant under [RT-6](../0141-runtime-routing/change.md#rt-6-tenant-scoped-interceptors) on a phase whose payload carries session content receives that content from the tenant's sessions with no approval under SO-4b. The tenant sees the registration in its chain and in posture, and it cannot refuse it. A registration that lists no tenant is called for every tenant, is a platform resource rather than a resource of this tenant, and is neither mirrored nor listed in this tenant's posture. The owner accepted this risk on 2026-11-09, in the word "ok" (decision 31).
- **Impersonation and external memory.** Revisions before 56 recorded that an operator impersonating a user could drive the user's session to write to an external memory server the user linked as a connector, an accepted risk. [`0124-connector-removal`](../0124-connector-removal/change.md) removes connectors. A memory service reached through egress with a `userCredential` binding falls under the impersonation check of [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md#limits-under-both-deliveries), and one reached with an `identityAssertion` binding falls under the same check as that design extends it (O6 of the removal design). The check covers the REST session input routes. A session whose input arrives on another path, and whose service takes a tenant secret alone, gives the service no user identity to misuse.
- **Destination coverage.** Destinations accept HTTPS POST with HMAC or a header. A SIEM that needs an object store or another protocol needs the customer's collector in front of it.

## Defects found while verifying

These exist in today's tree independent of this design and are filed in `BUILD-GAPS.md`. F-25.6.9 and F-25.4.40 are under SO-6 and SO-7.

| Finding | Severity | Defect | Addressed by |
| :-- | :-- | :-- | :-- |
| F-13.3.25 | Medium | Actions under an impersonation bearer are recorded as the impersonated user, and no audit row names the operator. | SO-4a |
| F-12.3.30 | Medium | `GET /v1/sessions?tenant=` lets a platform admin list another tenant's sessions and writes no audit row. `BUILD-GAPS.md` gives the same identifier to a second, High finding on a Postgres primary outage at the environment middleware, which this design does not address. | SO-4a, for the session-list finding alone |
| F-7.2.46 | Medium | An adapter-local `tool_call` with `approvalRequired: true` runs at once, because `HandleToolCall` does not read the flag. | The retirement of the adapter-local tools (RP D5) in 0139.S2 removes the path, and SO-5a holds no adapter-local call |
| F-25.6.10 | Low | The `coreDnsStuckEndpoint` remedy patches `kube-system/coredns` with no right there, and `lenny-ops` holds Deployment writes in its own namespace that no code uses. | SO-7, under decision 18 |
| F-10.6.22 | Medium | A `tenant-admin` can point `introspectionEndpoint` at any HTTPS address, and the gateway posts every bearer presented for that tenant to it. | SO-1 for a security principal's bearer, and [TS-4](../0149-tenant-scoping-identity/change.md#user-lifecycle-ts-4) for the address check |

## Specification sections affected

- §4.9 Credential Leasing Service: **Credential Storage Topology** and **Token Service secret resolution**, for the forwarder's keyed read and the destination Secrets.
- §10.6: tenant RBAC config, the write rule on `identityProvider`, and the principal exemption from introspection.
- §10.2 Authentication: the built-in roles, the permission matrix (including the first audit rows), the role source, **Custom roles (tenant-scoped).**, **Role assignment:**, **Session ownership.** rule 5, and the `impersonated_by` claim.
- §11.2.1 Billing Event Stream: no change, cited as the self-approval precedent.
- §11.7 Audit Logging: write-time tenant validation (mirror path), "Platform-tenant audit event residency", the compliance gate and startup checks, the SIEM statements, and OCSF mapping for new events.
- §12.2 and §12.3: new tables and the forwarder's outbox statement. §12.8 Compliance Interfaces: `DeleteByTenant` and `DeleteByUser` coverage, erasure propagation, and protected subjects under erasure. §12.9 Data Classification: the new records under the class names Public, Internal, Confidential, and Secret of [TSS-8](../0150-tenant-security-settings/change.md#tss-8-data-classes). A SIEM destination's signing secret or header value is Secret, as every credential is. The tenant security record and its versions, `tenant_security_principals`, `security_approvals`, and the destination rows are Confidential, because they carry user identifiers. Audit events stay Internal or Confidential as §12.9 places them.
- §13.1, §13.2.4, and §17: `lenny-siem-forwarder`, its NetworkPolicy row, identity, and sizing. Its namespace, `lenny-system`, and its placement are the placement design's ([PM-8](../0136-placement/change.md#pm-8-system-components)).
- §12.10 Tenant Security Settings, which [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) adds: the applier and the weakening delay of the tenant security record serve the settings' relaxations, and a tenant's `security-admin` may cancel one.
- The placement section that [`0136-placement`](../0136-placement/change.md) adds to §17: the tenant's own requirements live in the tenant security record's `placement` section, and their removal is a weakening.
- §13.3 Credential Flow, "Platform-admin impersonation": role stripping, the mirror step in rule 1, the approval gate, sweep behaviour at expiry, `reason` required with `ticketId` optional, and the refusal of an impersonation bearer on security-principal and security-config routes. §15.1's `POST /v1/admin/impersonation` row and §16.7's `admin.impersonation_started` fields change to match, and `pkg/gateway/api/admin/impersonation.go`, which refuses a missing `ticketId` today, changes with them.
- §15.1 REST API: new resources, error codes, and scope domain, including the record's `parentTools`, `offerTools`, and `delegationLeave` and `details.rule` on `DELEGATION_DISABLED` for the two tool forbids. §7.2 and §28.5: approval routing and the hold of a `tool_call` frame at the gateway's tool dispatch.
- §8.3, the **`scanExportedFiles` weakening cooldown** and the **Weakening cooldown** of the interceptor rules, in the section that owns the content caveat after [`0166-delegation-primitives`](../0166-delegation-primitives/change.md) rewrites §8: the cooldown starts on a weakening at the tenant, environment, or runtime level of the content caveat and refuses `session.create` with `files`, `materialize` pushes, and `files.import` on the affected sessions, in place of `delegate_task` calls under an affected `DelegationPolicy`. The security record's delegation fields, and the operations each delegation guardrail refuses, are cited from 0166's §8 text.
- The gateway seam interface page, `spec/interfaces/gateway-seams.md`, under decision 8 of [0117](../0117-gateway-package-layout/decisions.md): `GWSEAM-AuthorizeSession` and `GWSEAM-SessionAccessible` for the gate on a platform admin's session content read (SO-4b), `GWSEAM-RequestCaller` for the `impersonated_by` claim (SO-4a), and `GWSEAM-RegisterApproval`, `GWSEAM-CancelApproval`, `GWSEAM-ResolveApproval`, and `GWSEAM-AwaitApproval` for approvals routed to security (SO-5a).
- §16.1, §16.4, §16.5, §16.7: metrics, the SIEM delivery guard's extension and its per-tenant wording, alerts, and events.
- §24: `lenny-ctl` commands for principals, security config, destinations, and approvals.
- §25.1 and §25.4 Authentication: the role ceiling. §25.4 "ServiceAccount and RBAC": the reconciled block and **PKI trust boundary**.
- §25.5 Operational Event Stream: `owner_class` and **Tenant Isolation**.
- §25.6 Diagnostic Endpoints: the `certManagerExpiring` row, the new CA finding. §25.8 "Cert-Manager Integration" and "Version Aggregation": thresholds, the alert sentence, and the version source.
- §25.9 Audit Log Query API: permission statement, which it lacks today, and §25.12 tools for the new roles.

## Documentation

Each proposal carries the pages for its own surface. Spec changes land before the pages that describe them, the pages carry no section numbers, and each concept has one owner page (`doc-content.md`). Design detail is not repeated here.

### New pages

| Page | Owns |
| :-- | :-- |
| `operator-guide/security-oversight.md` | The operator's view: assigning the first `security-admin`, what the operator can and cannot do under approval, the emergency path, the disclosure setting, and what approval does not cover. |
| `client-guide/security-teams.md` | The customer security team's view: each member of the tenant security record with its default and whether adding or removing it is a weakening, the placement requirements `requireNodeSeparation`, `nodeTenancy`, `allowSharedPod`, and `allowReusedPod` and how they differ from an environment's, including that an environment's removal waits this record's weakening delay and that a `security-admin` can cancel it, how to cut off a principal at once, since its bearer skips introspection (Lenny's invalidate route, SCIM deprovisioning, and short token lifetimes), roles, reading audit and evidence, posture, guardrails with dry-run and the delay, SIEM destinations with failure and gap behaviour, operator access records, and approvals. It includes one worked onboarding. |
| `reference/security-roles.md` | The generated permission table for the security roles. |

### Changes to existing pages

| Page | Change |
| :-- | :-- |
| `operator-guide/audit-ocsf.md` | "SIEM delivery" distinguishes the platform copy from tenant destinations and links to the owner page. |
| `operator-guide/multi-tenancy.md` | The roles section and "Audit Trail" add security principals and the operator access mirror. |
| `operator-guide/security.md` and `security-principles.md` | The control map adds the guardrails, including `delegation`, `parentTools`, `offerTools`, and `delegationLeave` with the operations each refuses and its effect on calls in flight, and the cooldown after a weakened content scan on every transfer path, and the trust statement adds the `lenny-ops` boundary paragraph. |
| `operator-guide/agent-operability.md` and `lenny-ctl.md` | `doctor --fix` behaviour, the new CA finding, the removed Secret delete, and the new commands. |
| `operator-guide/installation.md`, `reference/configuration.md` | The forwarder's values and preflight rows, and `gateway.interceptorWeakeningCooldownSeconds` with the transfer paths its window covers. |
| `api/admin.md` | "Required roles" adds the new roles, the `rbac-config` entry states the `identityProvider` rule, and the resource groups for principals, security config, posture, destinations, approvals, and operator access requests are added. |
| `api/rest.md`, `client-guide/session-lifecycle.md`, `client-guide/error-handling.md` | A tool call routed to security, `POST /v1/sessions/{id}/write-approvals`, and the new errors. `docs/api/mcp.md` leaves Lenny under [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), so the MCP view of these items belongs to the MCP adapter's documentation. |
| `reference/error-catalog.md`, `reference/metrics.md`, `reference/glossary.md` | New errors, metrics, alerts, and terms. |
| `runbooks/` | New runbooks for each alert, such as `OperatorEmergencyAccess`, `TenantSIEMDeliveryFailing`, and `SecurityApproverCoverageLost`. `cert-manager-outage.md` adopts the relative thresholds and the status-based fix, and `ca-rotation.md` is linked from the new finding. The runbook index gains the entries. |
| `tutorials/multi-tenant-setup.md` | Adds an optional step assigning a security team. |

Every setting of "Configuration by scope" appears on the page that owns its surface with its default, every alert has a runbook, every error code is in `reference/error-catalog.md`, and each item of "Residual risks" is stated as a limit on `operator-guide/security-oversight.md` or `client-guide/security-teams.md`.

### Runtime author guide

| Page | Change |
| :-- | :-- |
| `lifecycle.md` and `platform-tools.md` | State that the gateway can hold a platform tool call the runtime did not flag, that tools of an operator's MCP service reached through egress are outside `approvals.tools` and that their approvals are the service's, that a `tool_call` frame receives no `tool_result` until approval or timeout, that a denial arrives as a `tool_result` with `isError: true` which a runtime treats as a user's denial, that a held call is withdrawn with an error result when a suspension stops the turn, and that a client timeout shorter than `approvals.timeoutSeconds` loses the verdict. The guide has no treatment of approvals today, so these pages add one. |
| "Network and credentials in the pod" (from [the egress design](../0127-egress-and-git/change.md)) | States that a push can return 403 with a pending-approval message, that a runtime reports this to the user and does not retry in a loop, and that the credentials a runtime sees can be withdrawn by a binding change. |
| `delegation.md`, from [`0166-delegation-primitives`](../0166-delegation-primitives/change.md) | States that a security record can forbid delegation, parent tools, or `offerTools: allow` and can cap `leave` and `leaveMaxDepth`, that each refusal names `securityGuardrail`, that a parent's offered tools can be withdrawn mid-session with open calls answered with an error, and that a file transfer can answer `INTERCEPTOR_WEAKENING_COOLDOWN` for a short window, which a runtime retries after the window. |
| `testing.md` | Describes the conformance case for a held call and a refused push. |

## Tests

Each proposal carries the tests for its own surface at the tiers `test-coverage.md` maps it to, with `// spec:` annotations and `// diagnosis:` comments. This section states what they must establish. None of these tests exists yet.

### `static`

- The new endpoints' OpenAPI entries carry `x-lenny-scope`, and the event catalog, OCSF mapping, and error catalog validate.
- `TestOpsRBACMatchesSpecCanonicalBlock` and its entry in `tests/registers/skip-reasons.yaml` are deleted, and no test reads the §25.4 block.
- After SO-6, a rendered `lenny-ops` Role has `update` on `certificates/status`. After SO-7, the rendered Role carries exactly the rule set of SO-7's table: no `secrets` rule, only `get`, `list`, and `watch` on `certificates` and on `deployments`, and only `get` on `deployments/scale`.

### `unit`

- Field-by-field classification of a security record change as tightening, weakening, or mixed, including lowered delays, approval relaxations, the setting and removal of `"delegation": "forbid"`, `"parentTools": "forbid"`, and `"offerTools": "forbid"`, and each move of `delegationLeave.max` between `none`, `transfer`, and `release`, of `delegationLeave.maxDepth`, and the addition and removal of either member. The addition and removal of each `placement` requirement is classified by the case that 0136.S6 adds with the `placement` section.
- The guardrail check for each opt-in kind and each rule, including an empty record.
- An `issuerExchange` type suspended under an empty record and under a record whose `tokenTrust.allowedRecords` is absent, released when its digest is listed after the delay, and suspended again when the type changes; `userCredential.metadata` suspending a `metadata` binding; and a `lifecycleSubjects` entry outside the `~svc~` namespace refused.
- Certificate state classification across lifetimes, across unset `renewalTime`, `renewBefore`, and `notBefore`, at each boundary, and for expired and unissued certificates.
- Detection excluding `isCA`, opt-out, Ready false, and an `Issuing` condition already true.
- The version source's label parsing, including a `+` build suffix and an absent label.
- Operator reference derivation is stable per tenant and differs across tenants.
- Approval state transitions, including `pending` to `withdrawn`, self-approval comparison, and expiry.
- The write of `operatorAccess.approval: required`, or of a matching `approvals.tools` rule, with no holder of `security-approver` is refused with `422 NO_APPROVER`.
- Permission tables for each role against the matrix, and the custom-role ceiling refusing each security write permission.
- Resolver output for each combination of claim, user row, security row, role source, `impersonated_by`, and invalidation level or hold of the subject.
- Rule matching against names on the `platform` server alone, and SO-5a's own refusal of a rule that names a `connector:<id>` server.
- `when` on a `session.create` rule: `targets` patterns and `targetLabels` against the final runtime, `llmCreditsAbove` against the resolved carve, both members together, a rule without `when`, refusal of `when` on another tool, and the classification of each change to `when` as a tightening or a weakening.
- `approvals.parentTools` matching by name and by `*`, routing by `approver`, and the `NO_APPROVER` refusal.
- The refusal with `400 VALIDATION_ERROR` of a rule that names a reference tool of 0166, such as `start_session`, or a tool name of MD-23, such as `start_child_session`.
- `delegationLeave` caps a grant's `leave` and `leaveMaxDepth` and never raises a value the other inputs of 0166 give.
- The cooldown after a weakened content scan: each write that starts it, a strengthening that starts none, a window computed from the stored `transition_ts`, a reset on a second weakening, and a lowered Helm value that leaves a running window unchanged.

### `component`

- The doctor remedy against a kube-apiserver with a cert-manager CRD: it updates only the status subresource, deletes no Secret, and repeats safely.
- `versionsource.HelmChart` reads the label with RBAC that has no Secret rule.
- `lenny-preflight`'s RBAC audit passes against the new rule set.

### `contract`

- Tenant destination requests: batch body, signature header, and header authentication.
- The stream anchor, gap, and closed records against their schemas.
- Approvals and security config response schemas, the `tool_result` with `isError: true` for a denied held call, and the `tool_use` phase `withdrawn` for a withdrawn approval.
- The pod credential API's refusal code for a pending write and the writable-set record in the binding feed.

### `integration`

- A `session.create` rule with `when` holds a child whose final runtime matches `targets` after a `PreRoute` interceptor changed the target, and releases one whose final runtime does not; a `session.set_limits` raise past `llmCreditsAbove` is held, and one below it is not; a carve that names no `llm` key is held when its default share crosses the amount; a carve in money is held when its amount at the cheapest upstream the child can reach crosses it; and a release, and an extension of a released session, past the amount are held.
- An `approvals.parentTools` rule holds a parent tool call before the parent receives the `tool_call` child event, for `approver: owner` and `approver: security`, with a denial and a timeout each answering the child's call with an error result.
- A role assignment takes effect on the next request and survives `PUT` and `DELETE` of the user's role.
- `security-auditor` reads audit, evidence, posture, and subscriptions, is refused session content, and is refused retranslate and republish.
- A tightening suspends objects and a dry run changes nothing; a concurrent opt-in and a tightening do not both succeed.
- An exemption named with a tightening leaves its object unmarked, one naming an object the active version already suspends waits out the delay, a later exemption waits out the delay, removing one suspends the object at once, a change to any input the rule entry reads, on the object, another object, or platform data, ends the exemption and suspends the object without refusing that write, a rename leaves it, deleting the object ends it, and a derive of a plan bound before the tightening stays refused.
- The recovery override is refused from the assignment's author under another subject or an impersonation bearer, and from a subject given `platform-admin` after the assignment.
- A weakening stays pending until `effectiveAt`, can be cancelled, and clears suspensions on activation.
- The destination lifecycle, cursor replay after a forwarder restart with no gap, replay by `since`, and evidence delivery by position.
- Mirror: the sweep covers each row of the table, restarts with no duplicate, and writes `impersonation_expired`. An interceptor registration that lists a tenant is mirrored to that tenant on create, update, and delete, an update that removes a tenant from the list is mirrored to that tenant, and posture's `interceptors` section lists the registration with its phases, priority, `failPolicy`, and Service name. A registration that lists no tenant reaches no tenant's chain.
- `impersonated_by` appears on rows written under a bearer, across a session read and an admin write. A session create under that bearer stays refused by step (1a) of §13.3 "Write-before-issue ordering", which rejects a token exchange whose subject is a revoked token or an impersonation bearer.
- `GET /v1/sessions?tenant=` emits its event before it returns rows, and returns no list when the emit fails.
- The applier activates a pending relaxation of the tenant security settings at `effectiveAt` under the record's `weakeningDelaySeconds`, a `security-admin` cancels one, and a `security-admin`'s write to the settings is refused. Each settings event reaches the tenant's chain before the write responds.
- A `placement` requirement added to the record refuses a session on a pool that fails it with `PLACEMENT_REQUIREMENT_UNMET` and `details.source: tenant`, and its removal takes effect only after the delay. This case lands with 0136.S6, which adds the `placement` section.
- Posture's `pools` section reports the node class, members, tenancy, offering, and region of each pool, a case that lands with 0136.S5 and 0136.S6. Its `securitySettings` section matches the settings view.
- An activation whose `effectiveAt` passed while the applier's leader was down applies on the next run, in `effectiveAt` order, and two gateway replicas running the applier emit each activation event once.
- Removing the last holder of `security-approver` while a matching `approvals.tools` rule or `operatorAccess.approval: required` is set raises `SecurityApproverCoverageLost`, and the last `security-admin` row nearing its `expires_at`, or held by a subject with no active identity source, raises `SecurityPrincipalCoverageLost`.
- A suspension that stops a turn withdraws a pending tool-use approval, emits the `tool_use` phase `withdrawn`, and a later approve or deny answers `404 RESOURCE_NOT_FOUND`.
- With `"delegation": "forbid"` set, `session.create`, `session.transfer`, `session.release`, and a parent tool's call answer `DELEGATION_DISABLED` with `disabledBy: securityGuardrail` before any SO-5a hold, an open parent tool call is answered with an error result, a `session.create` held under SO-5a is refused when the hold ends, `availability.delegation` turns `false` at the next turn, and running children continue.
- With `"parentTools": "forbid"` set, a `session.create` or `session.send` carrying `tools` is refused, each child's `client` tool server withdraws its parent's tools with `availability_changed`, and an open or held call is answered with an error result the parent never receives. With `"offerTools": "forbid"` set, `offerTools: allow` is refused and a fenced child loses its parent's tools while an unfenced child keeps them.
- With `delegationLeave` lowered, a `session.transfer` or `session.release` beyond the new value is refused, from a parent and from the root's client, and a leave completed before the write stands.
- With `crossEnvironmentSharing: forbid` set, a `session.create` on a sharing path and a `session.release` of a crossed child answer `TARGET_NOT_IN_SCOPE`, and a crossed child already running continues.
- After a weakened content scan at the tenant, environment, or runtime level, `session.create` with `files`, a `session.send` file part with `materialize: true`, and `files.import` on an affected session answer `503 INTERCEPTOR_WEAKENING_COOLDOWN` until the window ends, across two gateway replicas, and a transfer between unaffected sessions proceeds.
- Pending principal changes and destination changes activate at their delay, can be cancelled, and leave the prior state in force until then, including continued delivery to the old URL.
- The forwarder obtains a destination secret through the keyed read with no Kubernetes rights.
- Operator access request flow for both scopes: approved, denied, expired, used twice, wrong target, used by a platform admin other than the requester, and the `X-Lenny-Access-Request` header on a session read.
- The forwarder writes `siem.*` events to the tenant chain, and its role is refused any other event type.
- Tool approval routed to security for a `tool_call` frame on a runtime connection and on a tool connection, across two gateway replicas, with timeout denial, and a rule match on a `session.create` from a runtime that calls it, whether through the reference tool `start_session` or another extension.
- Repository write: pending at bind, refused push, approval, successful push, expiry, and revocation. A rule added while a session runs moves its writable set entry in state `granted` to `pendingApproval`, and its next push is refused.

### `e2e_kind`

- `lenny-ctl doctor --fix` on a healthy chart with 24-hour leaves changes nothing, and `certManager` reports `healthy`. The existing `diagnostics_fix_test.go` changes to expect the status-based re-issue with the Secret UID unchanged and no annotation.
- A forced overdue leaf re-issues, and mTLS peers keep working through it.
- A CA inside its window produces the manual finding, and its Secret and key are unchanged.
- `lenny-siem-forwarder`'s NetworkPolicy allows a public test endpoint and drops cluster, private, and metadata addresses.
- The file of a suspended binding with `delivery: file` disappears from a running pod.

### `e2e_cloud`

No behaviour here needs a cloud-only feature. The forwarder's egress through a managed NAT is covered once with [the egress design](../0127-egress-and-git/change.md)'s cloud tests.

### `load_local` and `load_kind`

Under `test-coverage.md`, `load_local` is the local load tier and `load_kind` the load tier on Kind.

- `load_local`: Many destinations with some slow or failing: the healthy destinations keep their lag bound, and worker slots stay within budget.
- `load_local`: Concurrent approve and deny on one approval produce one outcome; concurrent weakenings on one `If-Match` produce one version.
- `load_local`: Mirror appends under the per-tenant advisory lock stay within §11.7's lock SLO at the impersonation and admin write rates.
- `load_kind`: A forwarder rollout under load loses no acknowledged position.

### `chaos`

- Destination outage past the hold bound: the pruner proceeds for an unregulated tenant, a gap record follows, and a regulated tenant holds with the alert while another tenant's rows prune on schedule. A force-drop writes the gap record before it deletes.
- A gateway restart with a regulated tenant's only destination `failing`, `blocked`, holding a pending change, or absent starts normally, raises the per-tenant alert, refuses that tenant's new sessions only when no destination row remains, and leaves other tenants unaffected. Concurrent `DELETE`s and `include_audit: false` cannot remove the last copy.
- Postgres loss during a mirror write issues no bearer. During a revocation, the revocation holds and the mirror catches up.
- Forwarder kill mid-batch redelivers and skips nothing.
- Approvals store loss denies a held tool call rather than releasing it.
- The applier's leader is killed during an activation: the new leader applies the activation once, and no activation event is emitted twice.
- Security principals read failure returns `503` for a protected-subject write.
- cert-manager down during `doctor --fix`: a leaf with Ready false is not matched, and the state goes to `degraded` and then `unhealthy` on schedule.

### `security`

- **Protected subjects.** A tenant admin and a custom role with `manage_users` or `manage_user_lifecycle` cannot remove a security role, or invalidate, disable, reinstate, delete, or erase its holder, through any user or role route, and a listed `lifecycleSubjects` caller can invalidate and reinstate.
- **Disabled row.** `POST /v1/admin/users` with `disabled: true` for a principal with no user row is refused.
- **Token type and age.** A `session_capability`, `a2a_delegation`, or `service_token` of a principal's subject, and a `user_bearer` issued before `effective_at`, hold no security role.
- **Disabled principal.** A principal's live `user_bearer` holds no security role once the subject has a `soft_disable` or `hard_disable` invalidation level or a hold, so it cannot cancel a weakening or approve a request, and the principal does not count as active.
- **Expiry and delay.** Shortening `expiresAt` on a peer takes the delay and the last-admin check, and `weakeningDelaySeconds` above the ceiling is refused.
- **Role source.** A change to `oidc` has no effect before its delay and can be cancelled.
- **Recovery objection.** A tenant admin's objection stops activation, and only a second platform admin, compared by the person behind the bearer and present before the assignment, overrides it.
- **REST writes.** A binding mint with `contents: write` omits a repository whose writable set entry is `pendingApproval`, and a stored or non-`mint-scoped` binding on a rule's API host is refused and suspended.
- **Self-grant.** A tenant admin cannot assign a `security-*` role through `PUT .../users/{id}/role`, `POST` or `PUT /v1/admin/users`, the bootstrap seed, or a custom role with that name.
- **Introspection.** A principal's bearer never reaches a configured introspection endpoint, whoever set it, and an `active: false` answer does not reject a principal. A tenant admin cannot change `identityProvider.*` once a principal exists.
- **Stolen `security-admin`.** Removal of a peer, assignment of an approver, and creation or repointing of a destination have no effect before the delay, and a peer or a platform admin can cancel them.
- **Platform admin limit.** A platform admin cannot assign `security-approver` or `security-auditor`, or assign `security-admin` while one is active.
- **Forwarder.** It cannot read a Secret through the API, reach the API server, or read through the keyed read any name without its prefix.
- **Role source.** A `security-*` claim under `managed` grants nothing, including for a bearer from the tenant's IdP.
- **Isolation.** A security principal of `acme` reads nothing of `globex`, for each new read, including through `?tenantId=` and scatter-gather. Posture does not reveal other tenants' identity or number.
- **Impersonation.** A bearer for a security principal holds no security role, and a bearer resolves no approval and writes no security config.
- **Self-approval.** A requester, a session owner, and a platform admin requesting operator access cannot approve.
- **Requester binding.** An approved operator access request cannot be used by a platform admin other than its requester, for impersonation or for a session content read.
- **Approval gate.** No impersonation route issues a bearer without the approval when required, and an emergency use under `operatorAccess.emergencyAccess: forbidden` is refused. Each listed session-content read is gated.
- **Mint checks.** A decision service that allows a write on a writable set entry in state `pendingApproval` obtains no mint. A compromised decision service cannot lift a suspension.
- **Interceptor visibility.** A registration listing `acme` appears in `acme`'s chain and posture and in no record or posture read of `globex`.
- **Destination SSRF.** Each §25.5 case, at creation and on delivery, including rebinding and a redirect.
- **Secrets.** Destination secrets never appear in a read, a log, or an audit event.
- **Content.** The auditor's reads carry no session content under the catalog review's fixtures, and the review's result is pinned as a test.
- **Delegation fields.** A `tenant-admin` cannot write `parentTools`, `offerTools`, or `delegationLeave`, and a stolen `security-admin` credential cannot remove one or raise `delegationLeave` before the delay. No combination of `gateway.delegation.parentTools.enabled`, a grant's `offerTools`, and `session.create` arguments delivers a parent's tool to a child under `"parentTools": "forbid"`.
- **Cooldown.** No transfer path of 0166 decision 45 moves a file between affected sessions inside the window, and a request carrying `transition_ts` is refused with `INTERCEPTOR_COOLDOWN_IMMUTABLE`.
- **`lenny-ops` rights.** Its ServiceAccount cannot read, list, or delete a Secret or patch a Certificate's spec, and it cannot patch, update, or scale a Deployment. The residual Job path is asserted, so the §25.4 statement cannot drift from behaviour.

### `conformance`

A runtime treats a gateway-held `tool_call` frame, a denial on timeout, and a withdrawal correctly, and reports a refused push with a pending-approval message, for a runtime with no SDK and for each SDK.

### `docs`

Every new alert resolves to a runbook, examples validate against their schemas, and the generated role table matches the permission code.

### `load_cloud`

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
