# Tenant security settings

Status: design for owner review, revision 9, 2026-11-14. Revision 9 records the owner's confirmation on 2026-11-14, in the words "Ok on the rest" and under decision 44 of [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#decisions), of an uploaded artifact's single stored copy as packs of the tenant-owned assets set, of its deletion with the derived runtime, with the tenant, and after 7 days unreferenced, and of the per-tenant upload limit, and changes no rule. Revision 8 records the owner's confirmation on 2026-11-14, under decision 43 of [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#decisions), that the set fed by a `gitRepository` asset a `platform-admin` writes on a tenant-derived runtime is the tenant's, under the tenant key, and changes no rule. Revision 7 follows `tenant-derived-runtimes-design.md` revision 73: an uploaded artifact asset is stored only as packs of the runtime's tenant-owned assets set under the tenant key, and it is deleted with its derived runtime and, when no version of the runtime references it, after 7 days ("TSS-8"). Revision 6 applies the owner's settled answer to question 17 of 2026-11-14, decision 42 of [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md#decisions): a tenant-derived runtime's own shared assets are Confidential tenant data, stored under the tenant key, deleted with the tenant, and held under legal hold ("TSS-8"), and a tenant-owned platform chunk set comes from those assets and from the runtime's `gitRepository` assets ("Platform chunk sets"). Revision 5 applies the owner's answers of 2026-11-13 to the consolidated list of open decisions: the relaxation rule of this design governs the removal of an environment's `placementRequirements` (D9), a tenant's `security-admin` setting `allowReusedPod` is confirmed (D10), and a write that names a removed field is refused by name (D11). Revision 4 applies the cross-design review of 2026-11-13: the forensics design's decision 28 (its O1, "keep") on evidence keys wrapped by the tenant key, the removal of the non-agent runtimes of [`agent-runtimes-only-design.md`](agent-runtimes-only-design.md), and a "Documentation" section. Revision 3 adds `storage.platformChunkReuse`, which decision D1 of [`incremental-checkpoint-design.md`](incremental-checkpoint-design.md#decisions) asks for (D8). Nothing here is built or staged, and no independent review has read it. Revision 2 records the owner's answers of 2026-11-12 to O1 to O13, the rename of the top data class from Restricted to Secret, and a statement of how each class's rules are enforced. It is written from the owner's direction of 2026-11-12 to retire `workspaceTier: T4` as a concept, and from an inventory of every rule in `spec/` and `designs/` that a data-classification tier, `workspaceTier`, or T4 governs. It reads every design under `designs/` as implemented, in particular [`placement-design.md`](placement-design.md) revision 9, which owns node classes, node tenancy, placement offerings, and placement requirements, and [`security-oversight-design.md`](security-oversight-design.md) revision 55, whose tenant security record holds the constraints a tenant's own security team sets. This design sets tenant-level values that the operator chooses and does not redefine either design's mechanisms. "Decisions" lists what the owner has settled, and "Open decisions" keeps each question with its options, its recommendation, and the owner's answer. The design stages no specification edit. The changes go through `change-proposal` after review. Parts carry the label TSS-n, requirements TSSR-n, security properties TSSP-n, and inventory rows In. "Review history" at the end records the revisions.

Each factual claim carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny specification, a design, or the tree on 2026-11-12. |
| **[design]** | What this design requires. Nothing implements it. |
| **[inference]** | Reasoned from the sources and not run. "Validation owed" lists each one that matters. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

[§12.9](../spec/12_storage-architecture.md#129-data-classification) lets a tenant raise the classification of its workspace data from T3 to T4 through `dataClassification.workspaceTier` **[source]**. That one value switches several unrelated controls on at once:

- workspace snapshots, transcripts, and checkpoints are encrypted under a tenant-specific KMS key, with an admin-time probe, a fail-closed write path, a continuous probe, and key deletion at tenant deletion ([§12.5](../spec/12_storage-architecture.md#125-artifact-store), [§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces));
- a user's erasure must finish within 1 hour instead of 72, and the tenant's deletion within 4 hours instead of 72 ([§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces));
- pods run on dedicated nodes behind the `lenny-t4-node-isolation` admission webhook ([§6.4](../spec/06_warm-pod-model.md#64-pod-filesystem-layout));
- the pool controller refuses cross-tenant pod reuse ([§5.2](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes));
- the tree shortens the session artifact retention window to 24 hours (`retentionForTier` in `pkg/gateway/sessionserver/lifecycle.go`) **[source]**.

An operator who wants one of these controls has to take all of them, and cannot see from the setting which controls are in force. The specification also keys the placement controls on a `workspaceTier` field of the Runtime while §12.9 defines `workspaceTier` as a tenant setting. The tree carries both fields, so a T4 tenant whose session runs on a runtime without `workspaceTier: T4` gets the tenant key and the short deadlines, and runs on shared nodes ("Current state") **[source]**.

The owner's direction of 2026-11-12 retires T4 as a concept. Each control becomes its own explicit setting on the tenant, set by the operator through the admin API, and Lenny ships named templates that bundle settings for common levels of tenant security.

## Scope

**Added:**

- `securitySettings` on the tenant record, holding one explicit value per control, versioned ("TSS-1").
- Security templates, versioned and applied by copying, with a built-in set and operator-defined templates ("TSS-2").
- Admin API routes to read and update a tenant's settings, apply a template, list templates, probe the tenant key, cancel a pending relaxation, and view the effective settings ("TSS-3").
- A relaxation rule: a change that weakens a setting needs a justification, is mirrored to the tenant's audit chain, and takes effect after the weakening delay of the tenant security record (O5).
- Rules for changing a setting on a tenant with running sessions, stored checkpoints, and keys ("TSS-7").

**Changed:**

- The per-tenant KMS key lifecycle of §12.5 is keyed on `Tenant.securitySettings.encryption.tenantKey` and on the tenant key state ("TSS-4").
- The erasure deadline, the tenant deletion deadline, and the session artifact retention default become tenant settings ("TSS-5").
- The operator's per-tenant `nodeTenancy` of [PM-2](placement-design.md#pm-2-node-tenancy), the tenant isolation floor, one session per pod, and pod reuse become tenant placement settings that [PM-4](placement-design.md#pm-4-placement-requirements) enforces as filters ("TSS-6").
- The data classes of §12.9 keep their rules and are named Public, Internal, Confidential, and Secret. Workspace data is always Confidential ("TSS-8").
- The cross-region transfer prohibition that §12.9 adds for T4 applies to every tenant with `dataResidencyRegion` set ("TSS-8", row I34).

**Removed:**

- `dataClassification.workspaceTier` on the tenant and the environment, and `workspaceTier` on the Runtime and on `SandboxTemplate` ("TSS-9").
- The `lenny-t4-node-isolation` webhook and its label `lenny.dev/workspace-tier`, which the placement design already removes in favour of dedicated node tenancy.
- §5.2's T4 cross-tenant reuse prohibition, which tenant pinning already makes unreachable (row I23).

**Kept:**

- `dataResidencyRegion` on the tenant and the environment, with its inheritance and fail-closed routing ([§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces)).
- `complianceProfile` and its ratchet ([§11.7](../spec/11_policy-and-controls.md#117-audit-logging)), unchanged here. Under O9 (b), a follow-up design, `compliance-profile-design.md` (pending), replaces it with deployment-level Helm settings and per-tenant settings.
- The tenant security record of [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture), which holds the constraints the tenant's security team sets, including the tenant's own placement requirements under the placement design's O7 (O13).
- The runtime's `isolationProfile`, which [`runtime-revisions-design.md`](runtime-revisions-design.md) makes a live floor ("TSS-9").

**Out of scope:**

- Node classes, node tenancy mechanics, single-tenant pools, placement offerings, and the placement requirements a tenant sets, which the placement design owns.
- Uses of the word "tier" that name something else: the capacity tiers of [§17.8.2](../spec/17_deployment-topology.md#1782-capacity-tier-reference), the test tiers of `.claude/rules/test-coverage.md`, the circuit breaker's `limit_tier`, and the decision labels T1 to T9 of [`client-tools-design.md`](client-tools-design.md).
- The Pod Security Standards profile named Restricted, which [`session-isolation-design.md`](session-isolation-design.md), [`agent-pod-layout-design.md`](agent-pod-layout-design.md), and [`workspace-materialization-design.md`](workspace-materialization-design.md) cite. It is unrelated to the data classes, and the top class's name, Secret, no longer shares its name.
- A deployment-wide floor under the tenant settings, such as a Helm value that forces every tenant's `tenantKey` to `required`.
- The split of `complianceProfile` into settings and templates, which the follow-up design `compliance-profile-design.md` (pending) states (O9).

## Terms

| Term | Meaning |
| :-- | :-- |
| Security setting | One explicit value under `Tenant.securitySettings`, such as `encryption.tenantKey`. The operator chooses it. "TSS-1" lists them. |
| Security template | A named, versioned set of values for every security setting, such as `regulated` version 1, written `regulated@1`. |
| Apply | The admin action that copies a template version's values into a tenant's settings and records the template name, version, time, and caller. A later edit of the template changes no tenant. |
| Effective settings | The values the platform enforces for a tenant: the active settings version with every unset value resolved to the deployment default it stands for, together with the tenant key state. |
| Tightening and relaxation | A change to a setting that moves it to a stronger value is a tightening, and one that moves it to a weaker value is a relaxation, under the order "TSS-1" gives for each setting. |
| Settings version | One stored state of a tenant's settings, with its author, justification, `createdAt`, `effectiveAt`, and state (`pending`, `active`, `superseded`, or `cancelled`), as the tenant security record's versions are kept. Each session records the version it was placed under. |
| Tenant key | The KMS key with alias `tenant:{tenant_id}` that encrypts a tenant's object-store data when `encryption.tenantKey` is `required` ([§12.5](../spec/12_storage-architecture.md#125-artifact-store)). |
| Tenant key state | `none`, `active`, or `retained`. A tenant whose key has never been used is `none`. A key is `active` while `encryption.tenantKey` is `required`, and `retained` after the setting is relaxed while objects encrypted under the key remain. "TSS-4" defines the transitions. |
| Data class | One of the classes of [§12.9](../spec/12_storage-architecture.md#129-data-classification): Public, Internal, Confidential, and Secret, formerly the tiers T1 to T4, whose labels the specification gives as Public, Internal, Confidential, and Restricted. A data class is a property of a kind of data the platform handles and is never a tenant choice. Each class is a table of requirements, and "TSS-8" states how each requirement is enforced. |
| Erasure deadline | The time within which a user-level erasure job finishes ([§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces), **Erasure SLA enforcement.**). |
| Tenant deletion deadline | The time within which the tenant deletion lifecycle reaches `deleted` from the start of Phase 1 ([§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces), **Tenant deletion SLA.**). |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Tenant | The isolation unit for users, quotas, billing, and audit. A `platform-admin` creates it with `POST /v1/admin/tenants` and updates it with `PUT /v1/admin/tenants/{id}` under `If-Match` ([§15.1](../spec/15_external-api-surface.md#151-rest-api)). |
| Environment | A named scope inside a tenant that groups runtimes, members, and policy, and that inherits the tenant's `dataResidencyRegion` ([§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces), **Inheritance:**). |
| `platform-admin` and `tenant-admin` | Lenny's administrative roles for the whole platform and for one tenant ([§10.2](../spec/10_gateway-internals.md#102-authentication)). In this design the operator acts as `platform-admin`. |
| `security-admin` and `security-auditor` | The tenant-scoped roles of [SO-1](security-oversight-design.md#so-1-security-roles) for the tenant's security team. `security-admin` writes the tenant security record, and `security-auditor` reads it. |
| Tenant security record | The versioned record per tenant of [SO-2](security-oversight-design.md#so-2-tenant-security-record-and-posture), served at `/v1/admin/tenants/{id}/security-config`, that holds every constraint the tenant's security team sets. A weakening takes effect after the record's `weakeningDelaySeconds`, within the Helm floor `security.config.minWeakeningDelaySeconds` and ceiling `security.config.maxWeakeningDelaySeconds`. |
| Mirror to the tenant chain | [SO-4a](security-oversight-design.md#so-4a-mirror-to-the-tenant-chain): an operator action that concerns a tenant is appended to that tenant's own audit chain, where its admins and security team read it. |
| Security posture | `GET /v1/admin/tenants/{id}/security-posture` of SO-2, one object per tenant with a section per topic. |
| `dataResidencyRegion` | The tenant or environment field that restricts pod allocation and storage writes to one region, with a region-local KMS endpoint ([§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces), **Data residency.** and **KMS key residency.**). |
| `complianceProfile` | The tenant field with the values `none`, `soc2`, `fedramp`, and `hipaa` that turns on deployment-level audit gates, with a one-way ratchet and an attested decommission route ([§11.7](../spec/11_policy-and-controls.md#117-audit-logging), **Compliance profile enforcement gate.** and **Compliance profile downgrade ratchet.**). The follow-up design `compliance-profile-design.md` (pending) replaces it (O9). |
| Isolation profile | `standard` (runc), `sandboxed` (gVisor), or `microvm` (Kata) ([§5.3](../spec/05_runtime-registry-and-pool-model.md#53-isolation-profiles)). |
| Tenant pinning | The rule that a pod on a recycling pool or a pool with several slots serves one tenant for its whole lifetime ([§5.2](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), **Tenant pinning:**). |
| Recycling and concurrent slots | A recycling pool reuses a pod for later sessions after a scrub (`recycle.enabled: true`). A pool with more than one session per pod runs several sessions on one pod at once ([§5.2](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). |
| Node tenancy and dedicated node | Whether a pod may share its nodes with other tenants' pods (`shared`) or runs only on nodes labelled and tainted `lenny.dev/tenant=<tenantId>` (`dedicated`). Dedicated tenancy needs a single-tenant pool, an owned pool or a platform pool with `placement.tenantId` ([PM-2](placement-design.md#pm-2-node-tenancy)). |
| Placement offering | A named option a platform admin defines, carrying a node selection and a node tenancy, that every pool references ([PM-3](placement-design.md#pm-3-placement-offerings-and-per-pool-nodeplacement)). |
| Placement requirement and effective requirements | A filter that narrows pool selection and never widens it: `requireNodeSeparation`, `nodeTenancy: dedicated`, `allowSharedPod: false`, `allowReusedPod: false`, `dataResidencyRegion`, and `minIsolationProfile`. A session's effective requirements are the union of its tenant's, its environment's, the operator's, and for a delegated child its parent's. An unmet requirement refuses the session with `PLACEMENT_REQUIREMENT_UNMET`, whose `details.source` names `operator` for a requirement this design's settings imply ([PM-4](placement-design.md#pm-4-placement-requirements)). |
| Post-chain point | The step at session creation, after the `PreRoute` chain has fixed the runtime and pool and after the revision is chosen, where the gateway runs every check that depends on them ([`runtime-routing-design.md`](runtime-routing-design.md#rt-2-the-interceptor-contract)). |
| Revision and pod-affecting field | An immutable copy of a runtime's versioned fields, and a versioned field that shapes the pod before bind ([`runtime-revisions-design.md`](runtime-revisions-design.md)). |
| Erasure job, tenant deletion lifecycle, and tombstone | The `DeleteByUser` job and the phased `DeleteByTenant` lifecycle of [§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces), whose Phase 4a schedules deletion of the tenant key and whose tombstone keeps the tenant row's fields that later phases read. |
| Presigned capability | The single-key signed URL the gateway mints so an agent pod can write a checkpoint chunk directly to the object store ([§13.2.3](../spec/13_security-model.md#1323-checkpoint-capability-model)). |
| Test tiers | The stages of the test suite that `.claude/rules/test-coverage.md` defines, from 0 (static) to 12 (cloud load). |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| PM-2, PM-3, and PM-4 | [`placement-design.md`](placement-design.md) | Node tenancy with single-tenant pools and its admission and grant rules, placement offerings, and placement requirements with the effective requirements and `PLACEMENT_REQUIREMENT_UNMET`. That design's O7 stores a tenant's own requirements in the tenant security record, and its O8, settled on 2026-11-12, records that `regulated` sets dedicated tenancy. |
| SO-1, SO-2, and SO-4a | [`security-oversight-design.md`](security-oversight-design.md) | Security roles, the tenant security record with its weakening delay and security posture, and the mirror of operator actions to the tenant chain. |
| RV-1 and RV-3 | [`runtime-revisions-design.md`](runtime-revisions-design.md) | The versioned fields of a runtime with their classes, and pod sets keyed on the pod-affecting fields. |
| RT-2 | [`runtime-routing-design.md`](runtime-routing-design.md#rt-2-the-interceptor-contract) | The `PreRoute` contract and the post-chain point. |
| DR-10 | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md) | Capacity reservations for owned pools, with `reservedPlacement` on a template pool. |
| FE "Keys" | [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md) | The per-session evidence data keys and the key that wraps them. |

## High-level design

A tenant has a `securitySettings` object that the operator writes. Each setting names one control, has a closed type, a default that equals today's T3 behaviour, and an order from weaker to stronger. Only a `platform-admin` writes it. The tenant's `tenant-admin`, `security-auditor`, and `security-admin` read it, and the tenant's security team adds stricter placement requirements to the tenant security record, which are filters and cannot relax a setting.

| Setting on `Tenant.securitySettings` | Controls | Default |
| :-- | :-- | :-- |
| `encryption.tenantKey` | Object-store data under the tenant's own KMS key, with fail-closed writes and cryptographic erasure at tenant deletion. | `none` |
| `erasure.userDeadlineHours` | The erasure job's deadline. | `72` |
| `deletion.tenantDeadlineHours` | The tenant deletion deadline. | `72` |
| `retention.sessionArtifactHours` | The default retention window for a session's artifacts. | unset, which is the deployment default of [§7.1](../spec/07_session-lifecycle.md#71-normal-flow) |
| `storage.platformChunkReuse` | Whether the tenant's snapshots may reference the platform chunk sets of the sources its sessions may read, and whether its own sources contribute to sets. | `true` |
| `placement.nodeTenancy` | Whether the tenant's pods may share nodes with other tenants' pods. | `shared` |
| `placement.minIsolationProfile` | The weakest isolation profile the tenant's pods may run at. | unset |
| `placement.allowSharedPod` | Whether a pod may run several of the tenant's sessions at once. | `true` |
| `placement.allowReusedPod` | Whether a pod may serve a later session of the tenant after a scrub. | `true` |

Lenny ships templates named `standard`, `confidential`, and `regulated`, and an operator can define more in Helm values. Applying a template copies its values into the tenant and records `regulated@1` with the time and the caller. A template edit changes no tenant. The read route shows each tenant's active values, any pending relaxation, which template version it last applied, which values differ from that version, whether a newer version exists, and the effective values.

A tightening applies to new work at once: new writes, new placements, and new jobs. Running sessions finish where they are, and the read route counts sessions placed under an earlier settings version. A relaxation needs a `justification`, is mirrored to the tenant's audit chain, and takes effect after the weakening delay of the tenant security record. Data already protected under the stronger value stays protected, such as objects under the tenant key.

The data classes stay as internal rules for kinds of data, named Public, Internal, Confidential, and Secret. Each class is a table of requirements, and each requirement has a named enforcement point, such as the audit write path or the store's refusal of a write it cannot protect. Credentials and tokens are Secret on every tenant. Workspace data is Confidential on every tenant, and a tenant's settings decide how it is stored and where it runs.

```
     platform-admin                                tenant security team
          |                                          (security-admin)
 apply template / PUT settings                              |
 (relaxation: justification + delay)            PUT security-config (SO-2)
          |                                       placement requirements
          v                                                 |
  Tenant.securitySettings (versioned)          tenant security record
     |           |            |          \              /
     v           v            v           v            v
 ArtifactStore  erasure job,  retention   post-chain point: effective
 tenant key     deletion      GC window   placement requirements (PM-4)
 (TSS-4)        controller    (TSS-5)     source "operator" | "tenant"
                (TSS-5)
```

## Decisions

| # | Question | Owner's answer | Applied in |
| :-- | :-- | :-- | :-- |
| D1 | What replaces `workspaceTier: T4` (2026-11-12). | T4 is retired as a concept. Each control it bundles becomes its own explicit setting, set by the operator through the admin API on the tenant, or on another object where the control belongs there, with the reason stated. | TSS-1, TSS-9 |
| D2 | How operators get a correct bundle (2026-11-12). | Lenny ships configuration templates, named bundles of settings for levels of tenant security, such as `standard`, `confidential`, and `regulated`. | TSS-2 |
| D3 | Placement ownership (2026-11-12). | Node tenancy, minimum isolation, and pod sharing and recycling reference the placement design, which owns node classes, node tenancy, placement offerings, and placement requirements as filters. This design sets the tenant-level values. | TSS-6 |
| D4 | Migration (2026-11-12). | There are no deployments, so the change is a straight replacement with no compatibility path. `workspaceTier` and every T4 rule leave the specification, with successor pointers where content moves. | TSS-9 |
| D5 | The name of the top data class (2026-11-12, N1). | "Rename the top data class from 'Restricted' to 'Secret' (Public, Internal, Confidential, Secret)." | TSS-8, "Specification changes" |
| D6 | `complianceProfile` (2026-11-12, O9). | Option (b), as a follow-up design: "`complianceProfile` becomes deployment-level Helm settings plus per-tenant settings, with soc2, fedramp, and hipaa as templates and the ratchet replaced by the weakening delay. That design is written later." This design leaves `complianceProfile` unchanged and points to `compliance-profile-design.md` (pending). | O9, TSS-1 |
| D7 | The other open decisions (2026-11-12). | O1 (a), O2 (a) ("keep the taxonomy", after the owner asked for examples), O3 (a), O4 (a), O5 (b) ("ok", as recommended), O6 (a), O7 (a), O8 (a), O10 (a), O11 (a), O12 (a), and O13 (a). | "Open decisions" |
| D8 | Platform chunk sets (2026-11-13), from decision D1 of `incremental-checkpoint-design.md`. | A tenant setting turns them off, `false` in `regulated` and `true` in `standard` and `confidential` ("2. ok"), and a tenant with its own key may reference operator-source chunks stored under the deployment key ("1. ok"). | TSS-1, TSS-2, TSS-4, TSS-7 |
| D9 | Relaxing an environment's placement requirements (2026-11-13). | "5 ok": removing an environment's `placementRequirements`, or moving one to a weaker value, is a relaxation under this design's rules, with a `justification` and the weakening delay, as [PM-4](placement-design.md#pm-4-placement-requirements) states under its D18. | TSS-6, TSS-3 error table |
| D10 | `allowReusedPod` set by the tenant's security admin (2026-11-13). | "15 ok": a tenant's `security-admin` sets `allowReusedPod: false` as the tenant's own requirement in the tenant security record, beside the operator's `placement.allowReusedPod` of this design. | TSS-6 |
| D11 | One rule for removed fields (2026-11-13). | "6 ok": a write that names a removed field is refused with `400 VALIDATION_ERROR` naming the field, as D6 of [`agent-runtimes-only-design.md`](agent-runtimes-only-design.md#decisions) records. | TSS-3, TSS-9 |

## Open decisions

The owner answered every open decision on 2026-11-12 (D6 and D7). Each entry keeps its options and its recommendation as a record and states the answer.

**O1. How a template reaches a tenant.**

- (a) Copy on apply. Applying writes the template version's values into the tenant and records the name and version. A later template version changes nothing until an operator applies it.
- (b) Reference with overrides. The tenant names a template and stores only overrides, and a new template version propagates to every tenant that names it.

**Recommendation: (a).** Under (b), a template edit can weaken every tenant that names it with no per-tenant action, no justification, and no per-tenant audit record. Under (a), each tenant's values change only through a recorded action on that tenant. The read route reports `newerVersionAvailable` and `differsFromTemplate` ("TSS-3"), so an operator finds tenants to re-apply, and the tenant list filters on them.

**Settled (2026-11-12): (a)** (D7).

**O2. The data-class taxonomy.**

- (a) Keep §12.9's classes as an internal taxonomy of kinds of data, named rather than labelled T1 to T4, with no tenant input.
- (b) Remove the taxonomy and state each rule on the data it governs.

**Recommendation: (a).** The classes carry rules that are not tenant choices: credentials and OAuth tokens are always in the top class and envelope-encrypted, user identifiers are Confidential, and audit events of the two upper classes are written synchronously ([§12.3](../spec/12_storage-architecture.md#123-postgres-ha-requirements)). The labels in §12.9 are Public, Internal, Confidential, and Restricted **[source]**. Names avoid confusion with the retired setting. "TSS-8" lists where the taxonomy stays in force.

**Settled (2026-11-12): (a)** (D7). The owner asked for examples and then answered "keep the taxonomy". The owner's N1 of the same day names the top class Secret (D5), so the classes are Public, Internal, Confidential, and Secret.

**O3. Who sets the settings.**

- (a) Only `platform-admin` writes them. `tenant-admin`, `security-auditor`, and `security-admin` read them. The tenant's security team narrows placement further through requirements in the tenant security record, which are filters only.
- (b) The tenant's `security-admin` may also tighten a setting, and only `platform-admin` may relax one.

**Recommendation: (a).** Tightening `encryption.tenantKey` needs a key the operator provisions (O8), and dedicated node tenancy needs single-tenant pools and nodes the operator provides ([PM-2](placement-design.md#pm-2-node-tenancy)). A tenant that wants a stricter placement than its settings states it as a requirement, which the placement design already admits from a tenant.

**Settled (2026-11-12): (a)** (D7).

**O4. What replaces `workspaceTier` on the Runtime.**

- (a) Nothing new. The runtime keeps its `isolationProfile`, which [`runtime-revisions-design.md`](runtime-revisions-design.md) makes a live floor. Every data-sensitivity control is a tenant setting.
- (b) A runtime setting that requires dedicated nodes or a per-tenant key for every tenant that uses the runtime.

**Recommendation: (a).** A runtime image is shared by many tenants, and the sensitivity of the data belongs to the tenant whose data it is. A runtime that needs stronger isolation for its own reasons, such as running untrusted code, states it in `isolationProfile`. Under (b), the same tenant would get different key and deadline behaviour depending on the runtime, which is today's defect ("Problem").

**Settled (2026-11-12): (a)** (D7).

**O5. How a setting is relaxed.**

- (a) A relaxation needs a non-empty `justification`, applies at once, emits a critical audit event, and is mirrored to the tenant's chain under SO-4a.
- (b) As (a), and the relaxation takes effect after the tenant security record's `weakeningDelaySeconds`, through the same pending-version applier. A `platform-admin` or the tenant's `security-admin` can cancel it while it is pending.
- (c) A one-way ratchet like `complianceProfile`, with a separate attested route for relaxation.

**Recommendation: (b).** SO-2 already applies one weakening rule, with its delay, to every security constraint on a tenant, including a `platform-admin`'s writes to the record, so that the rest of the tenant's security team can stop a weakening from a stolen credential before it has an effect **[source]**. The operator's settings protect the same tenant's data, and a stolen `platform-admin` token is the case the delay addresses. (b) reuses the applier and the version table, so it adds no new mechanism. The cost is that an urgent relaxation waits the delay, at least `security.config.minWeakeningDelaySeconds` (3600 seconds by default); "TSS-7" states the effect on a key outage. (c) adds a route per setting family and protects against nothing (b) misses.

**Settled (2026-11-12): (b)** (D7). The owner answered "ok" to the recommendation.

**O6. Environment-level overrides.**

- (a) Remove the environment-level override. Environments inherit the tenant's settings, and a stricter placement for one environment is a requirement on that environment ([PM-4](placement-design.md#pm-4-placement-requirements)).
- (b) Keep an environment-level copy of each setting that may only tighten the tenant's value.

**Recommendation: (a).** Today only `workspaceTier` has a stricter-only environment override **[source]**. Under (b), every setting reader resolves two levels, and the key, deadline, and retention controls are per tenant in practice: there is one tenant key, one erasure job per user, and one deletion lifecycle.

**Settled (2026-11-12): (a)** (D7).

**O7. What `placement.nodeTenancy: dedicated` means.**

- (a) Nodes that carry pods of this tenant alone, as [PM-2](placement-design.md#pm-2-node-tenancy) defines dedicated tenancy.
- (b) Nodes shared by every tenant whose setting is `dedicated`, as T4 nodes are shared by every T4 tenant today ([`egress-and-git-design.md`](egress-and-git-design.md) states that "T4 nodes host several T4 tenants") **[source]**.

**Recommendation: (a).** §6.4 justifies dedicated nodes by the per-tenant key isolation that node disk encryption lacks **[source]**, and (b) leaves every tenant with the setting exposed to each other on one node. The placement design's mechanism is (a), so the setting reuses it unchanged.

**Settled (2026-11-12): (a)** (D7).

**O8. Who creates the tenant key.**

- (a) The operator provisions the key before the setting is switched on, and the write probes it, as §12.5 requires today **[source]**.
- (b) The platform creates the key through the provider's API when the setting is switched on, as the tree's `tenantkms.Lifecycle.EnsureForTenant` does **[source]**.

**Recommendation: (a).** Under (b), the gateway needs rights to create and schedule deletion of KMS keys, which widens what a compromised gateway can do. The probe turns a missing key into an admin-time refusal that names the alias to create. The tree's provisioning path leaves with this design.

**Settled (2026-11-12): (a)** (D7).

**O9. `complianceProfile`.**

`complianceProfile` is a second bundle on the tenant: it turns on a SIEM requirement, pgaudit, a grant-check interval, retention floors, and a cross-user cache prohibition, with a one-way ratchet and an attested decommission route **[source]**.

- (a) Keep it as it is in this design. Templates do not set it, because a template cannot choose between SOC 2, FedRAMP, and HIPAA for an operator. A later design decides whether to split it.
- (b) Split it in this design into explicit settings, with the framework names becoming templates.

**Recommendation: (a).** Most of its controls are deployment-level gates (SIEM, pgaudit, and retention floors checked at gateway start) rather than per-tenant behaviour, and splitting them changes [§11.7](../spec/11_policy-and-controls.md#117-audit-logging) and [§16.4](../spec/16_observability.md#164-logging) broadly. The owner's direction applies to it in spirit, so the overlap is recorded here for a decision.

**Settled (2026-11-12): (b), as a follow-up design** (D6). After asking for elaboration, the owner chose to split `complianceProfile`: it becomes deployment-level Helm settings plus per-tenant settings, with `soc2`, `fedramp`, and `hipaa` as templates, and the weakening delay replaces the one-way ratchet and its attested decommission route. A later design, `compliance-profile-design.md` (pending), states it. Until that design lands, this design leaves `complianceProfile`, its ratchet, its decommission route, and [§11.7](../spec/11_policy-and-controls.md#117-audit-logging) unchanged, no setting depends on it, and no template sets it.

**O10. Operator-defined templates.**

- (a) Built-in templates plus operator-defined templates in Helm values under `tenantSecurity.templates`, each with an explicit version. A changed definition under an existing version stops the gateway at start.
- (b) Built-in templates only.
- (c) Templates created through the admin API.

**Recommendation: (a).** Templates are deployment configuration, which the bootstrap plane holds in Helm values ([§15.1](../spec/15_external-api-surface.md#151-rest-api), **Bootstrap plane — Helm only:**). An operator whose policy differs from the built-ins writes it once. (c) adds a mutable resource whose edits need the same versioning the Helm path gets from the stored version table ("TSS-2").

**Settled (2026-11-12): (a)** (D7).

**O11. Data written before the tenant key is switched on.**

- (a) No re-encryption. Objects written under the deployment key keep it until retention removes them. The read route counts them, and tenant deletion still deletes them in Phase 4.
- (b) A background job that rewrites every existing object under the tenant key.

**Recommendation: (a).** Retention bounds the exposure to the tenant's retention window, 7 days by default for session artifacts, and a job that rewrites every object costs a full read and write of the tenant's data. An operator who needs full coverage at once applies the setting before the tenant's first session. Legal-hold objects never expire, so the count can stay above zero, and "Limits" states it.

**Settled (2026-11-12): (a)** (D7).

**O12. The isolation value of the `regulated` template.**

- (a) `sandboxed` (gVisor), the platform default.
- (b) `microvm` (Kata).

**Recommendation: (a).** §5.3 names gVisor the default for all workloads, and Kata needs nodes with hardware virtualization that many clusters lack **[source]**. Under (b), applying `regulated` refuses every session on those clusters. An operator who wants Kata sets `placement.minIsolationProfile: microvm` on the tenant after applying.

**Settled (2026-11-12): (a)** (D7).

**O13. Where the operator's settings live.**

- (a) A separate operator-owned `securitySettings` on the tenant, beside the tenant security record of SO-2. Both feed the effective placement requirements, with `details.source` `operator` or `tenant`.
- (b) An operator-owned section inside the tenant security record.

**Recommendation: (a).** SO-2 states that the record holds every constraint the tenant's security team sets, and its `security-admin` writes it **[source]**. The operator's settings are chosen by the operator and need operator resources, so a `security-admin` must not be able to edit them, and under (b) the record would need a second writer rule per section. (a) still reuses SO-2's version table layout, applier, and weakening delay under O5 (b), and the security posture reports both side by side.

**Settled (2026-11-12): (a)** (D7).

## Requirements

| # | Requirement |
| :-- | :-- |
| TSSR-1 | Every control `workspaceTier: T4` switched on is either a named tenant setting, a data-class rule, folded into an existing setting, or retired with a stated reason (inventory). |
| TSSR-2 | Every setting has a closed type, a default equal to today's T3 behaviour, and an order from weaker to stronger. |
| TSSR-3 | Only `platform-admin` writes a tenant's settings. `tenant-admin`, `security-auditor`, and `security-admin` read them. |
| TSSR-4 | A relaxation requires a non-empty `justification`, is mirrored to the tenant's audit chain, and takes effect no earlier than its `effectiveAt` under the tenant security record's weakening delay. A tightening takes effect in the write's transaction. |
| TSSR-5 | Applying a template copies its values, records the template name, version, time, and caller, and emits an audit event with the previous and new values. A template change alters no tenant. |
| TSSR-6 | A template version is immutable. A built-in or operator-defined template whose content changes takes a new version. |
| TSSR-7 | The read route returns the active and any pending version, the applied template version, the values that differ from it, whether a newer version exists, the effective values, the tenant key state, and the count of non-terminal sessions placed under an earlier settings version. |
| TSSR-8 | Switching `encryption.tenantKey` to `required` succeeds only after the tenant key passes the probe and the object-store backend can apply a per-tenant key. |
| TSSR-9 | While `encryption.tenantKey` is `required`, no write of the tenant's object-store data falls back to the deployment key. |
| TSSR-10 | Tenant deletion schedules deletion of the tenant key whenever the tenant key state is `active` or `retained`, whatever the current setting. |
| TSSR-11 | An erasure job and a tenant deletion record their deadline at start, and a later settings change does not move it. |
| TSSR-12 | The placement settings enter every session's effective requirements with `source: operator`: at creation, resume, delegation, and derive. |
| TSSR-13 | No setting is read from the Runtime, the pool, or the environment. |
| TSSR-14 | Data classes are named Public, Internal, Confidential, and Secret, and no tenant input changes a kind of data's class. |
| TSSR-15 | Every requirement of a class's control table names its enforcement point, or is retired for the kinds of data no mechanism enforces it on ("TSS-8"). |

## Security properties

| # | Property |
| :-- | :-- |
| TSSP-1 | A template edit cannot weaken an existing tenant (O1 (a)). |
| TSSP-2 | No tenant role can weaken its tenant's settings (O3 (a)). A tenant's placement requirements only narrow placement. |
| TSSP-3 | Every relaxation leaves a record in the tenant's own audit chain naming the caller and the reason before it takes effect, and the tenant's security team can cancel it during the delay (O5 (b)). |
| TSSP-4 | Data written under the tenant key stays decryptable only through the tenant key until tenant deletion destroys it, whatever later happens to the setting (TSSR-10). |
| TSSP-5 | A session is never placed on a node, isolation profile, or pod that its tenant's effective settings exclude at the time of placement. |
| TSSP-6 | Credentials, tokens, and other Secret data receive the Secret rules on every tenant, independent of its settings. |

## Current state

### Specification

- §12.9 defines tiers T1 to T4, a default tier per data type, a control table per tier, and the tenant setting `dataClassification.workspaceTier` with T3 as default and T4 as the only raise. The setting is inherited by environments, which may only tighten it **[source]**.
- §12.5 states the T4 per-tenant KMS key lifecycle: pre-provisioning with an admin-time probe, fail-closed writes with a per-provider mint binding, operator-managed rotation, and the continuous probe STO-021 **[source]**.
- §12.8 states the T4 ephemeral workspace isolation, erasure in 72 or 1 hours, tenant deletion in 72 or 4 hours, Phase 4a key deletion for T4 tenants, and KMS key residency for T4 operations **[source]**.
- §6.4 requires T4 pods on dedicated node pools through a pool `nodeSelector`, a taint, and the `lenny-t4-node-isolation` webhook, keyed on the Runtime's `workspaceTier` **[source]**.
- §5.2 refuses `recycle.allowCrossTenantReuse: true` on a pool whose runtime is T4, and refuses at assignment a T4 session on a recycled pod bound to another tenant. The same section pins every recycling or concurrent pod to one tenant for its lifetime, so the field never lets a pod serve a second tenant **[source]**.
- §15.1 carries `t4KmsLastProbeSuccessAt` on `GET /v1/admin/tenants/{id}`, the probe on a `workspaceTier: T4` `PUT`, and `CLASSIFICATION_CONTROL_VIOLATION` **[source]**.

### Designs

- The placement design removes the T4 node label, taint, and webhook and hands the link between T4 and dedicated nodes to this design (its D10 and O8) **[source]**. Its revision 1 offered `nodeTenancy: dedicated` as an operator setting on the tenant record written through `PUT /v1/admin/tenants/{id}`, and its revision 2 reads it from `securitySettings.placement.nodeTenancy` here and carries `allowReusedPod: false` as a requirement **[source]**.
- The security-oversight design's tenant security record holds customer-set constraints, with a weakening delay that also applies to a `platform-admin`'s writes to it, and the placement design stores a tenant's own requirements there (its O7) **[source]**.

### Tree

- `workspaceTier` is a field of the tenant row (migration `0103_tenant_workspace_tier_check`), of environments, of the `Runtime` custom resource, and of `SandboxTemplate` **[source]**.
- `pkg/tenantkms` provisions, rotates, probes, and destroys the tenant key, and provisions it when a tenant is created or raised to T4, which §12.5 forbids **[source]**.
- `pkg/controller/sandbox/podspec/t4_node_isolation.go` and `pkg/admission/t4_node_isolation` add and check the node selector and toleration from the Runtime's tier **[source]**.
- `pkg/gateway/sessionserver/lifecycle.go` sets a 24-hour retention window for a T4 tenant from §12.9's lease retention default **[source]**.
- `pkg/admission/data_residency_validator` rejects a T4 tenant's region-bearing resource whose region differs from the tenant's, as §12.9's cross-region transfer prohibition **[source]**.
- `pkg/controller/tenantdeletion` runs Phase 4a only for a T4 tenant **[source]**.
- `pkg/gateway/runtime/poolstore` and `pkg/gateway/externalapi/admin/pools.go` refuse cross-tenant reuse on a T4 runtime's pool **[source]**.

## Inventory

The inventory covers every rule in `spec/*.md` and `designs/*.md` that T4, T3, T2, T1, `workspaceTier`, a data classification, the class labels Restricted and Confidential, `CLASSIFICATION_CONTROL_VIOLATION`, or the erasure SLA governs. The "Rule" column states each rule in the specification's current tier labels, and the successor columns use the class names. A rule that several carriers restate is one row that lists every carrier. The kinds are:

- **Setting**: a control a tenant's operator chooses. It becomes a named setting on the object stated.
- **Class**: an internal rule for a kind of data. It stays in the taxonomy under the class name.
- **Folded**: a rule that an existing setting already carries, so it needs no new setting.
- **Retired**: a rule with no successor, with the reason.

Every new setting lives on `Tenant.securitySettings`, its writer is `platform-admin`, and its readers are named in "TSS-4" to "TSS-6".

### Tenant key

| # | Rule | Source | Kind | Successor |
| :-- | :-- | :-- | :-- | :-- |
| I1 | Workspace snapshots, transcripts, and checkpoints of a T4 tenant use SSE-KMS under `tenant:{tenant_id}`, and a T3 tenant's use the deployment key. | §12.5, **Encryption at rest:** | Setting | `Tenant.securitySettings.encryption.tenantKey`, enum `none` or `required`, default `none`. |
| I2 | The operator provisions the key before setting T4, and `PUT /v1/admin/tenants/{id}` probes it and refuses with `CLASSIFICATION_CONTROL_VIOLATION`. | §12.5, **T4 per-tenant KMS key lifecycle.** item 1; §15.1 `PUT /v1/admin/tenants/{id}` | Setting | The write of I1 probes the key (TSSR-8), refusing with `TENANT_KEY_UNAVAILABLE`. |
| I3 | A write that cannot reach the key fails closed with no fallback, raises `CheckpointStorageUnavailable`, and recovers by re-asserting `workspaceTier: T4`. | §12.5 item 2 | Setting | Applies while I1 is `required` (TSSR-9). Recovery uses `POST /v1/admin/tenants/{id}/tenant-key/probe`. |
| I4 | The mint binds the tenant key per provider: SigV4 headers on MinIO and S3, a per-tenant bucket with a default CMEK on GCS, and a container scope with override prevention on Azure, asserted at gateway start and by preflight. | §12.5 item 2; §13.2.3 Checkpoint Capability Model; §17.9.7 Backend-Invariant Requirements; §17.6 "Checks performed", checkpoint T4 default-encryption row | Setting | Keyed on I1. The write of I1 refuses with `SECURITY_SETTING_UNSATISFIABLE` on a GCS or Azure deployment without the per-tenant configuration. |
| I5 | Rotation is operator-managed, and a manual rotation re-asserts T4 to re-run the probe. | §12.5 item 3 | Setting | The procedure calls the probe route. |
| I6 | A leader-elected probe checks every T4 tenant's key on `storage.t4KmsProbeInterval` at `storage.t4KmsProbeRateLimit`, with the `lenny_t4_kms_probe_*` metrics, the `T4KmsKeyUnusable` alert, and `t4KmsLastProbeSuccessAt`, and never downgrades a tenant. | §12.5 item 4 (STO-021); §16.1; §16.5; §17.8.1; §15.1 `GET /v1/admin/tenants/{id}`; §18.31 | Setting | The probe covers every tenant whose key state is `active` or `retained`. Names in "TSS-4". |
| I7 | `CLASSIFICATION_CONTROL_VIOLATION` with the reasons `kms_probe_failed` and `kms_unavailable`. | §15.1 error catalog | Setting | `TENANT_KEY_UNAVAILABLE` with the same reasons ("TSS-4"). |
| I8 | Tenant deletion Phase 4a disables and schedules deletion of a T4 tenant's key, and `KmsKeyDeletionFailed` reports a failure. | §12.8, tenant deletion lifecycle table; §16.5 | Setting | Runs when the key state is `active` or `retained` (TSSR-10). |
| I9 | The tombstone keeps `workspaceTier` for later phases. | §12.8, tenant deletion lifecycle table, Phase 4 | Setting | The tombstone keeps `securitySettings` and the key state. |
| I10 | KMS operations for a T4 tenant with `dataResidencyRegion` use the region-local endpoint. | §12.8, **KMS key residency.** | Folded | `dataResidencyRegion` on the tenant. The rule names the tenant key in place of T4. |
| I11 | ArtifactStore backup crypto-shredding relies on per-tenant SSE-KMS keys. | §25.11, "ArtifactStore Backup (MinIO workspace bucket replication)" | Setting | Applies to tenants whose key state is `active` or `retained`. |
| I12 | Evidence data keys are wrapped by the tenant's KMS key on T4 and by a regional evidence key otherwise, and `lenny-evidence-reconcile` holds T4 decrypt rights. | [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md), "Keys" and "Ingest" | Setting | Keyed on the tenant key state of I1. Keys wrapped while `active` stay so wrapped under `retained` and count toward the move to `none` ("TSS-4", the forensics design's decision 28). |

### Deadlines and retention

| # | Rule | Source | Kind | Successor |
| :-- | :-- | :-- | :-- | :-- |
| I13 | An erasure job finishes within 72 hours for T3 data and 1 hour for T4 data, and `ErasureJobOverdue` fires past the deadline. The user credential design restated the 1-hour deadline for the erasure of a stored link, a T4 `user_links` row whose provider revocation the job retries before it destroys the ciphertext. | §12.8, **Erasure SLA enforcement.**; §12.9, control table "Erasure on deletion" and **Interaction with existing controls.**; §16.5; [`user-credential-delivery-design.md`, "Lifecycle and revocation at the provider"](user-credential-delivery-design.md#lifecycle-and-revocation-at-the-provider) | Setting | `Tenant.securitySettings.erasure.userDeadlineHours`, integer 1 to 72, default `72`. |
| I14 | `MemoryStoreErasureDurationHigh` names the 1-hour T4 deadline. | §16.5 | Setting | The text names the tenant's erasure deadline. |
| I15 | With the erasure reconciler disabled, `backups.retention.retainDays` must not exceed the erasure SLA of 72 hours for T3 or 1 hour for T4. | §12.8, **Backups in erasure scope.**; §25 `lenny-ops` Helm values | Setting | The bound is the smallest `erasure.userDeadlineHours` over all tenants, checked at `lenny-ops` start and at a settings write ("TSS-5"). |
| I16 | Erasure text refers to "the erasure SLA": the OCSF dead-letter redaction and the downstream SIEM boundary. | §12.8, `DeleteByUser` step 14; §25, "Downstream SIEM Scope Boundary (GDPR Erasure)" | Setting | The text names the tenant's erasure deadline. |
| I17 | Tenant deletion finishes within 72 hours for T3 and 4 hours for T4, `lenny_tenant_deletion_duration_seconds` carries `tenant_data_tier`, and `TenantDeletionOverdue` fires at 80% of the deadline. | §12.8, **Tenant deletion SLA.**; §16.5; §23, "Additional Platform Capabilities" | Setting | `Tenant.securitySettings.deletion.tenantDeadlineHours`, integer 4 to 72, default `72`. |
| I18 | Phase 4a destroys the key within the T4 deadline of 4 hours. | FE, worked example | Setting | The tenant deletion deadline. |
| I19 | §12.9 gives a retention default per tier. The tree applies the T4 lease default of 24 hours to a T4 tenant's session artifacts, and the session-derive design reads it as 24-hour upload retention on T4. | §12.9, control table "Retention default"; §7.1, **Artifact retention:**; [`session-derive-design.md`](session-derive-design.md) | Setting | `Tenant.securitySettings.retention.sessionArtifactHours`, integer 1 to 8760 or unset, default unset (the deployment default). |

### Placement

| # | Rule | Source | Kind | Successor |
| :-- | :-- | :-- | :-- | :-- |
| I20 | Node disk encryption gives no per-tenant key isolation for workspace data on emptyDir, and T4 deployers should use gVisor or Kata. | §6.4, **T4 workspace limitation:** | Setting | `Tenant.securitySettings.placement.minIsolationProfile`, enum `standard`, `sandboxed`, or `microvm`, or unset, default unset. It is the tenant isolation floor that PM-4 lists "as today". The node disk statement stays in §6.4 for every tenant. |
| I21 | T4 pods run on dedicated node pools that carry no non-T4 pod, through a pool `nodeSelector` on `lenny.dev/workspace-tier: t4` and a taint. | §6.4, **T4 dedicated-node requirement:** item 1; §12.8, **T4 ephemeral workspace isolation.** | Setting | `Tenant.securitySettings.placement.nodeTenancy`, enum `shared` or `dedicated`, default `shared`. It replaces the tenant-record `nodeTenancy` of PM-2, whose mechanism applies unchanged. |
| I22 | The `lenny-t4-node-isolation` webhook checks T4 pods' selector and toleration and refuses non-T4 pods that carry them, with its chart inventory entry, feature flag, NetworkPolicy, downgrade alert, unavailability alert, runbook, preflight checks, build phase, and composition-table mention. | §6.4 item 2; §13.2.4; §16.5 `T4NodeIsolationWebhookUnavailable` and `AdmissionPlaneFeatureFlagDowngrade`; §16.7; §17.2; §17.6; §17.7; §17.9.1; §18.31 | Setting | PM-2's dedicated tenancy and its taint replace the webhook and every carrier, as the placement design's specification changes state. |
| I23 | The pool controller refuses `recycle.allowCrossTenantReuse: true` on a T4 runtime's pool, and the gateway refuses a T4 session on a recycled pod bound to another tenant. | §5.2, **T4 cross-tenant reuse prohibition.** | Retired | Tenant pinning already keeps every recycling or concurrent pod on one tenant, so neither check can refuse anything. |
| I24 | T4 nodes host several T4 tenants. | [`egress-and-git-design.md`](egress-and-git-design.md), security analysis | Setting | `placement.nodeTenancy: dedicated` is per tenant under O7 (a). |
| I25 | A template pool's `reservedPlacement` is refused on a runtime with `workspaceTier: T4`. | [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md), DR-10 **Nodes.** | Setting | The placement design's reserved overlay, bounded by the class (PM-3), with PM-2's single-tenant pools for a dedicated tenant. |
| I26 | §6.4 counts the one-session-only pod lifecycle among T4's defences for workspace data. | §6.4, **T4 workspace limitation:** | Setting | `Tenant.securitySettings.placement.allowSharedPod`, boolean, default `true`, which implies PM-4's `allowSharedPod: false` when `false`, and `Tenant.securitySettings.placement.allowReusedPod`, boolean, default `true`, a new requirement for the placement design ("TSS-6"). |

### The setting itself

| # | Rule | Source | Kind | Successor |
| :-- | :-- | :-- | :-- | :-- |
| I27 | `workspaceTier` on the Runtime and `SandboxTemplate`, merged from a base runtime and refused on a derived one, labels pods for node isolation. | §5.2 and §6.4 read it; tree; [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md), "Fields" | Retired | O4 (a). The runtime keeps `isolationProfile`. |
| I28 | `workspaceTier` is a versioned, pod-affecting runtime field, and a T3 to T4 change uses `immediate`. | [`runtime-revisions-design.md`](runtime-revisions-design.md), RV-1 | Retired | The field leaves the runtime. |
| I29 | A tenant raises workspace data from T3 to T4 through `dataClassification.workspaceTier`, and T4 applies the T4 controls to its workspace files, snapshots, and transcripts. | §12.9, **Per-tenant workspace classification.** | Retired | Replaced by the settings. Workspace data is Confidential on every tenant. |
| I30 | An environment may override the tenant's tier to a stricter one. | §12.9, same paragraph; tree `environments.go` | Retired | O6 (a). |
| I31 | The T4 column of the control table, applied to workspace data, requires `admin` role with an explicit grant for access, an audit record for every access attempt, and field-level encryption in transit. | §12.9, control table | Retired | No mechanism implements any of these for workspace data, and the first would deny users their own workspaces. The column stays for the Secret data it was written for ("TSS-8", "How a class's requirements are enforced"). |
| I32 | `workspaceTier` is stricter-only on `PUT`, and the `complianceProfile` ratchet, its decommission route, and `COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED` cite it as their model. | §15.1 `PUT /v1/admin/tenants/{id}`, decommission row, and error catalog; §11.7, **Compliance profile downgrade ratchet.** | Setting | The relaxation rule of O5. The citations name it. |
| I33 | The policy engine validates the tenant's classification configuration at session creation, and the tree refuses a session whose tenant tier is unrecognized. | §12.9, **Enforcement.**; tree `policy/classification.go` | Setting | The gateway validates the active settings at the post-chain point and refuses with `TENANT_SECURITY_SETTINGS_INVALID`. |
| I34 | §12.9 adds for T4 a prohibition on cross-region transfer, which the tree's residency webhook enforces by refusing a T4 tenant's resource whose region differs from the tenant's. | §12.9, control table "Data residency" and **Interaction with existing controls.**; tree `data_residency_validator` | Folded | `dataResidencyRegion`. Every tenant with a region gets the check, which §12.8's inheritance rule already implies ("TSS-8"). |

### Data classes

| # | Rule | Source | Kind |
| :-- | :-- | :-- | :-- |
| I35 | The classes and their descriptions. | §12.9 | Class, renamed. T4 becomes Secret and loses "PHI-tagged workspace data". |
| I36 | The default class per data type. | §12.9, **Default data-type mappings:** | Class. Workspace files, snapshots, and transcripts are Confidential on every tenant. |
| I37 | Encryption, access, audit, retention, erasure handling, and residency per class for platform data. | §12.9, **Controls driven by classification tier:** | Class. The erasure row states handling and leaves the deadline to I13. |
| I38 | Internal data needs storage-layer volume encryption, which preflight checks. | §12.9, **T2 storage-layer encryption definition.** | Class |
| I39 | A store refuses a write whose class it cannot protect, such as T4 data to a store without envelope encryption, with the reason `tier_store_mismatch`. | §12.9, **Enforcement.**; §15.1 | Class. The reason becomes `class_store_mismatch`. The tree implements the check only for a T4 tenant's object-store writes ("TSS-8"). |
| I40 | T3 and T4 audit events are written synchronously, T2 audit events may be batched by opt-in, and a SIEM does not relax this. | §12.3, **Batching guidance for write-heavy paths:** | Class |
| I41 | The write classification and durability tables for audit inserts. | §12.3 | Class |
| I42 | The audit flush rows of the capacity table. | §17.8.2 | Class |
| I43 | `AuditChainGap` mentions buffered T2 loss. | §16.5 | Class |
| I44 | `session_eviction_state` stores Confidential data. | §4.4.5, **Tenant isolation for `session_eviction_state`.** | Class |
| I45 | User-supplied API keys are T4. | §4.9, "Three Credential Modes" and **User-scoped credential storage:** | Class |
| I46 | `callbackSecret` is Confidential. | §14 | Class |
| I47 | The `platform.bootstrap_applied` audit event is Confidential. | §15.1, `POST /v1/admin/bootstrap` | Class |
| I48 | Pseudonymized billing events are Confidential until the salt is deleted. | §12.8, **GDPR compliance note on pseudonymization.** | Class |
| I49 | Legal holds override retention in every class, and credential encryption meets the T4 rules. | §12.9, **Interaction with existing controls.** | Class |
| I50 | `user_links` rows are T4. | [`user-credential-delivery-design.md`](user-credential-delivery-design.md) | Class |
| I51 | Directory users, groups, and source records are T3, and SCIM credential hashes and the client secret are T4. | [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md) | Class |
| I52 | Audit events are T2 or T3, and transcripts are T3. | [`security-oversight-design.md`](security-oversight-design.md) | Class |
| I53 | The "Semantic cache entries" row, T3, is removed. | [`llm-response-cache-removal-design.md`](llm-response-cache-removal-design.md) | Class |
| I54 | Connector instances, hosts, attachment rows, and new streams are classified. | [`tenant-outbound-connector-design.md`](tenant-outbound-connector-design.md) | Class |
| I55 | Activity streams are T3 by default and T4 when `workspaceTier` is T4. | FE, redaction | Class. They are Confidential on every tenant, and I12 decides the wrapping key. |
| I56 | A derived session's transcript read follows §12.9 classification. | [`session-derive-design.md`](session-derive-design.md) | Class |

## TSS-1 Settings model

### Schema

The tenant record carries `securitySettings`, with every member present after creation **[design]**:

```yaml
securitySettings:
  encryption:
    tenantKey: none              # none | required
  erasure:
    userDeadlineHours: 72        # integer, 1..72
  deletion:
    tenantDeadlineHours: 72      # integer, 4..72
  retention:
    sessionArtifactHours: null   # integer 1..8760, or null for the deployment default
  storage:
    platformChunkReuse: true     # false: snapshots reference no platform chunk set
  placement:
    nodeTenancy: shared          # shared | dedicated
    minIsolationProfile: null    # standard | sandboxed | microvm, or null
    allowSharedPod: true         # false: one session per pod at a time
    allowReusedPod: true         # false: a pod serves one session of the tenant and retires
```

Each stored version is a row in `tenant_security_settings_versions`, with the same columns and states as SO-2's `tenant_security_config_versions`: author, justification, `createdAt`, `effectiveAt`, and state **[design]**. The tenant record carries, beside it, fields the platform writes and no caller sets:

| Field on the tenant record | Meaning |
| :-- | :-- |
| `securitySettingsVersion` | The active settings version. |
| `securityTemplate` | `{name, version, appliedAt, appliedBy}` of the last template applied, or null. |
| `tenantKeyState` | `none`, `active`, or `retained` ("TSS-4"). |
| `tenantKeyLastProbeSuccessAt` | The time of the last successful probe of the tenant key. |

Each session records `securitySettingsVersion` at placement, so the read route counts sessions placed under an earlier version **[design]**.

### Order and validation

| Setting | Weaker to stronger | Validation at write |
| :-- | :-- | :-- |
| `encryption.tenantKey` | `none` < `required` | `required` needs the probe of "TSS-4" to pass and the backend to apply a per-tenant key. |
| `erasure.userDeadlineHours` | larger < smaller | 1 to 72. A value below `backups.retention.retainDays` in hours, while `backups.erasureReconciler.enabled` is `false`, is refused with `SECURITY_SETTING_UNSATISFIABLE`. |
| `deletion.tenantDeadlineHours` | larger < smaller | 4 to 72. |
| `retention.sessionArtifactHours` | larger < smaller, with null compared as the deployment default | 1 to 8760, or null. |
| `storage.platformChunkReuse` | `true` < `false` | Boolean. |
| `placement.nodeTenancy` | `shared` < `dedicated` | PM-2's rule: `dedicated` is refused with `422 CONFIGURATION_CONFLICT` while the tenant holds access to a pool with shared tenancy, or when the tenant identifier is not a valid label value. |
| `placement.minIsolationProfile` | null < `standard` < `sandboxed` < `microvm` | A §5.3 profile name or null. |
| `placement.allowSharedPod` | `true` < `false` | Boolean. |
| `placement.allowReusedPod` | `true` < `false` | Boolean. |

The bounds on the deadlines come from today's values: 72 hours is the T3 deadline and GDPR-aligned, and 1 and 4 hours are the T4 values the platform already claims to meet ([§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces)) **[source]**. A shorter deadline than those is not offered, because no measurement shows the erasure job or the deletion lifecycle can meet it.

A write that tightens some settings and relaxes others applies its tightenings at once and holds its relaxations as a pending version, as SO-2's mixed write does **[design]**.

### Combination with other tenant fields

- `dataResidencyRegion` stays a separate tenant and environment field. With `encryption.tenantKey: required`, the tenant key lives at the region's `storage.regions.<region>.kmsEndpoint`, and a missing endpoint refuses the write with the existing `KMS_REGION_UNRESOLVABLE` ([§12.8](../spec/12_storage-architecture.md#128-compliance-interfaces), **KMS key residency.**) **[source]**.
- `complianceProfile` stays a separate field, unchanged until the follow-up design of O9 (b) replaces it. No setting depends on it, and no template sets it.
- `billingErasurePolicy` stays a separate field. The erasure deadline applies to the job whether billing events are pseudonymized or exempt.
- The tenant security record of SO-2 stays the tenant's own. The placement requirements it holds and the placement settings here both enter the effective requirements of PM-4, and each can only narrow placement (O13).

## TSS-2 Templates

### Built-in templates

| Setting | `standard`@1 | `confidential`@1 | `regulated`@1 |
| :-- | :-- | :-- | :-- |
| `encryption.tenantKey` | `none` | `required` | `required` |
| `erasure.userDeadlineHours` | `72` | `72` | `1` |
| `deletion.tenantDeadlineHours` | `72` | `72` | `4` |
| `retention.sessionArtifactHours` | null | null | `24` |
| `storage.platformChunkReuse` | `true` | `true` | `false` |
| `placement.nodeTenancy` | `shared` | `shared` | `dedicated` |
| `placement.minIsolationProfile` | null | `sandboxed` | `sandboxed` (O12) |
| `placement.allowSharedPod` | `true` | `true` | `false` |
| `placement.allowReusedPod` | `true` | `true` | `false` |

`standard` equals today's T3 tenant and every default. `regulated` carries every control T4 carried and adds per-tenant node dedication (O7), one session per pod, and a fresh pod for every session, which §6.4 lists among T4's defences without requiring them **[design]**. `confidential` adds the tenant key and the platform's default isolation profile, so that tenant deletion destroys the tenant's stored data cryptographically, without the cost of dedicated nodes. The placement design's O8 left to this design whether a template requires `nodeTenancy: dedicated`; `regulated` does.

### Versions

A template version is immutable **[design]**. A Lenny release that changes a built-in template's values ships it under the next version and keeps every earlier version, so a tenant's recorded version can always be shown. An operator-defined template lives in Helm values under O10 (a):

```yaml
tenantSecurity:
  defaultTemplate: standard        # applied to a tenant created without one
  templates:
    acme-baseline:
      version: 2
      settings:
        encryption: { tenantKey: required }
        erasure: { userDeadlineHours: 24 }
        # every setting not listed takes its default
```

At start the gateway writes every template version it carries to the `security_templates` table, keyed on name and version with a digest of the values. A stored name and version whose digest differs stops the gateway with `CONFIG_INVALID: security template <name>@<version> changed without a new version` **[design]**. An operator-defined template may not reuse a built-in name. Removing an operator-defined template from Helm values leaves its stored versions readable and makes it unavailable to apply.

### Apply

Applying a template version writes every setting from the version's values as one settings write, so its tightenings apply at once and its relaxations wait the delay under O5 (b) **[design]**. When any setting moves to a weaker value, the request needs a `justification`. `securityTemplate` records the template version once every part of the write is active. The `tenant.security_template_applied` audit event records the tenant, the template name and version, the previous and new value of every setting that changed, which changes are pending with their `effectiveAt`, the caller, and the justification. Creating a tenant applies the template the request names, or `tenantSecurity.defaultTemplate` when it names none, with no delay, because a new tenant has nothing to relax.

A later `PUT` of individual settings keeps `securityTemplate` and makes the read route list the settings that differ from that version. Re-applying a newer version is an operator action on each tenant.

## TSS-3 Admin API

The routes follow §15.1's conventions: `If-Match` on every write, `dryRun` on every write, the error taxonomy, and an MCP tool per route on `/mcp/management` ([§15.1](../spec/15_external-api-surface.md#151-rest-api), **Admin API design constraints:** and **Admin API MCP extension contract.**) **[design]**. Every write is mirrored to the tenant's chain under SO-4a.

| Method | Path | Behaviour | Roles |
| :-- | :-- | :-- | :-- |
| `POST` | `/v1/admin/tenants` | Accepts optional `securityTemplate` (`{name, version}`, version defaulting to the latest) and optional `securitySettings`, applied in that order. Validates as `PUT .../security-settings` does. | `platform-admin` |
| `GET` | `/v1/admin/tenants/{id}` | Includes `securitySettings` (active), `securitySettingsVersion`, and `securityTemplate`. `t4KmsLastProbeSuccessAt` leaves. | `platform-admin`, own tenant for `tenant-admin` |
| `PUT` | `/v1/admin/tenants/{id}` | A body carrying `securitySettings`, `nodeTenancy`, or `workspaceTier` is refused with `VALIDATION_ERROR`, naming the settings route. | `platform-admin` |
| `GET` | `/v1/admin/tenants/{id}/security-settings` | Returns the view below. | `platform-admin`; own tenant for `tenant-admin`, `security-auditor`, and `security-admin` |
| `PUT` | `/v1/admin/tenants/{id}/security-settings` | Replaces the settings. Body: `{"settings": {...}, "justification": "<string>", "ticketId": "<string>"}`. A setting omitted from `settings` takes its default. A relaxation without `justification` returns `400 SECURITY_SETTING_JUSTIFICATION_REQUIRED`. The response gives the active version and any pending version with its `effectiveAt`. | `platform-admin` |
| `POST` | `/v1/admin/tenants/{id}/security-settings/apply-template` | Body: `{"template": "<name>", "version": <n>, "justification": "<string>"}`. `version` defaults to the latest. An unknown name or version returns `RESOURCE_NOT_FOUND`. | `platform-admin` |
| `POST` | `/v1/admin/tenants/{id}/security-settings/pending/{version}/cancel` | Cancels a pending relaxation. | `platform-admin`; own tenant for `security-admin` |
| `POST` | `/v1/admin/tenants/{id}/tenant-key/probe` | Runs the probe of "TSS-4" against the tenant key and returns `{"state", "lastProbeSuccessAt"}`, or `TENANT_KEY_UNAVAILABLE`. Refused with `VALIDATION_ERROR` when the key state is `none`. | `platform-admin` |
| `GET` | `/v1/admin/security-templates` | Lists every template with its versions, the latest version, whether it is built in, and each version's values. | `platform-admin`, `tenant-admin`, `security-auditor`, `security-admin` |
| `GET` | `/v1/admin/security-templates/{name}` | One template with every version. | As above |
| `GET` | `/v1/admin/tenants` | Adds the filters `securityTemplate=<name>`, `templateOutdated=true` (a newer version exists than the one applied), `differsFromTemplate=true`, and `pendingRelaxation=true`. | `platform-admin` |

The view that `GET .../security-settings` returns **[design]**:

```json
{
  "active": { "version": 7, "settings": { "...": "the active values" }, "effectiveAt": "2026-11-12T09:30:00Z" },
  "pending": null,
  "template": { "name": "regulated", "version": 1, "appliedAt": "2026-11-12T09:00:00Z", "appliedBy": "alice@acme.com" },
  "differsFromTemplate": ["retention.sessionArtifactHours"],
  "newerVersionAvailable": null,
  "effective": {
    "retention.sessionArtifactHours": { "value": 12, "source": "tenant" },
    "erasure.userDeadlineHours": { "value": 1, "source": "tenant" }
  },
  "tenantKey": {
    "state": "active",
    "alias": "tenant:acme",
    "lastProbeSuccessAt": "2026-11-12T09:55:00Z",
    "objectsUnderDeploymentKey": 0,
    "objectsUnderTenantKey": 1840,
    "evidenceKeysUnderTenantKey": 212
  },
  "sessionsUnderEarlierVersion": 3
}
```

`effective` lists every setting with its resolved value and `source`: `tenant` for a value stored on this tenant, and `deployment` for an unset value resolved to a deployment default **[design]**. The security posture of SO-2 gains a `securitySettings` section with the same content ("Instructions for reviewers of other designs").

New error codes in the [§15.1](../spec/15_external-api-surface.md#151-rest-api) catalog **[design]**:

| Code | Category | HTTP | Meaning |
| :-- | :-- | :-- | :-- |
| `TENANT_KEY_UNAVAILABLE` | `POLICY` | 422 | The tenant key failed the probe at a settings write or at the probe route, or a write or a mint could not use the key. `details.reason` is `kms_probe_failed` or `kms_unavailable`. Replaces those reasons of `CLASSIFICATION_CONTROL_VIOLATION`. |
| `SECURITY_SETTING_JUSTIFICATION_REQUIRED` | `PERMANENT` | 400 | A relaxation without a non-empty `justification`, of a setting or of an environment's `placementRequirements` (D9). `details.relaxed` lists the settings or the requirements. |
| `SECURITY_SETTING_UNSATISFIABLE` | `POLICY` | 422 | The deployment cannot provide the setting: a GCS or Azure backend without per-tenant encryption configuration, or an erasure deadline below the backup retention while the reconciler is disabled. `details.setting` and `details.reason`. |
| `TENANT_SECURITY_SETTINGS_INVALID` | `PERMANENT` | 500 | Session creation found an active settings value outside its type, which no write admits. The session is refused. |

`CLASSIFICATION_CONTROL_VIOLATION` keeps one reason, `class_store_mismatch`, for a write whose data class the store cannot protect (I39). A session refused by a placement setting gets PM-4's `PLACEMENT_REQUIREMENT_UNMET` with `details.source: operator`.

Audit events in [§16.7](../spec/16_observability.md#167-section-25-audit-events), each mirrored to the tenant's chain under SO-4a **[design]**:

| Event | Severity | When |
| :-- | :-- | :-- |
| `tenant.security_settings_tightened` | info | A write's tightenings apply. |
| `tenant.security_settings_relaxation_requested` | critical | A write creates a pending relaxation. |
| `tenant.security_settings_relaxed` | critical | The applier activates a pending relaxation at `effectiveAt`. |
| `tenant.security_settings_relaxation_cancelled` | warning | A pending relaxation is cancelled. |
| `tenant.security_template_applied` | info, or critical when it relaxes | A template is applied. |
| `tenant.key_state_changed` | info | The tenant key state changes. |

Each carries the tenant, the previous and new version, the previous and new value of each changed setting, the caller, the justification, and the `ticketId` when given. The `TenantSecuritySettingRelaxationRequested` warning alert in [§16.5](../spec/16_observability.md#165-alerting-rules-and-slos) fires on each `tenant.security_settings_relaxation_requested`.

`lenny-ctl` gains `lenny-ctl tenant security get|set|apply-template|cancel-pending|probe-key` and `lenny-ctl security-templates list|get` under [§24.10](../spec/24_lenny-ctl-command-reference.md#2410-tenant-management) **[design]**.

## TSS-4 Tenant key

The §12.5 lifecycle keeps its mechanisms and changes its key from `workspaceTier` to `encryption.tenantKey` and the tenant key state **[design]**.

**States.**

| From | Event | To |
| :-- | :-- | :-- |
| `none` | A write sets `tenantKey: required` and the probe passes. | `active` |
| `active` | A relaxation of `tenantKey` to `none` becomes active. | `retained` |
| `retained` | A write sets `tenantKey: required` and the probe passes. | `active` |
| `retained` | The count of the tenant's objects under the tenant key and of its evidence data keys wrapped by the tenant key both reach zero. | `none` |
| `active` or `retained` | Tenant deletion Phase 4a schedules key deletion. | The tombstone records the scheduled deletion. |

**Writes and mints.** While the state is `active`, every object-store write and every presigned capability for the tenant uses the tenant key by the per-provider binding of §12.5, and a write or mint that cannot use it fails with `TENANT_KEY_UNAVAILABLE` and no fallback (TSSR-9). While the state is `retained`, new writes use the deployment key and reads of objects under the tenant key still need it.

**Object accounting.** The `artifact_store` row of each object gains `encryption_key_scope`, `deployment` or `tenant` **[design]**. The read route's `objectsUnderDeploymentKey` counts the tenant's rows with `deployment` while the state is `active`, which is the exposure O11 (a) accepts. The count of rows with `tenant`, together with the count of the tenant's evidence data keys in `evidence_session_keys` that the tenant key wraps, decides the move from `retained` to `none`, so the key is kept while either remains (the forensics design's decision 28). The read route reports both counts.

**Probe.** The leader-elected probe of STO-021 covers every tenant whose state is `active` or `retained` **[design]**. The names change:

| Today | After |
| :-- | :-- |
| `storage.t4KmsProbeInterval`, `storage.t4KmsProbeRateLimit` | `storage.tenantKeyProbeInterval`, `storage.tenantKeyProbeRateLimit`, with the same defaults and floor |
| `lenny_t4_kms_probe_last_success_timestamp`, `lenny_t4_kms_probe_result_total` | `lenny_tenant_key_probe_last_success_timestamp`, `lenny_tenant_key_probe_result_total` |
| `T4KmsKeyUnusable` | `TenantKeyUnusable` |
| `t4KmsLastProbeSuccessAt` on `GET /v1/admin/tenants/{id}` | `tenantKey.lastProbeSuccessAt` on the security settings view |
| Re-assert `workspaceTier: T4` with `PUT` to re-run the probe | `POST /v1/admin/tenants/{id}/tenant-key/probe` |
| `T4KmsProbe` worker | `TenantKeyProbe` worker |

The probe never changes a setting, as today.

**Backends.** The gateway's start-up assertion and the preflight check of §17.6 apply when any tenant's state is `active`, in place of "serves a T4 tenant". A settings write that would make the first such tenant on a GCS or Azure deployment without the per-tenant configuration is refused with `SECURITY_SETTING_UNSATISFIABLE` **[design]**.

**Deletion.** Phase 4a runs when the state is `active` or `retained` (TSSR-10). The tombstone keeps `securitySettings`, `tenantKeyState`, and `dataResidencyRegion` **[design]**.

**Platform chunk sets.** A snapshot may reference chunks of the platform chunk sets of [`incremental-checkpoint-design.md`, "IC-4"](incremental-checkpoint-design.md#ic-4-catalog-reference-closure-and-quota): the working trees of warm repository stores' warm-up checkouts and runtimes' shared assets, for the sources the session may read. A set an operator's source fills is stored under the deployment key, so a tenant whose state is `active` references operator-source chunks under the deployment key; the owner accepted this on 2026-11-13. Those chunks hold operator content that every session of the source's pools receives, and no content of the tenant. A set a tenant's own source fills, the staged trees of a tenant-derived runtime's `gitRepository` assets, an asset a `platform-admin` writes on the runtime included, which the owner confirmed on 2026-11-14, or its `inline` and `artifact` assets, is stored under the tenant key while the state is `active`. `storage.platformChunkReuse: false` turns the sets off for the tenant: its snapshots reference no platform chunk, and its own sources receive no contribution **[design]**.

**Other users of the key.** The forensics design wraps each session and pod-incarnation evidence data key with the tenant key while the state is `active`, and with the regional evidence key otherwise, and a data key wrapped by the tenant key stays so wrapped when the state becomes `retained` ([`forensics-evidence-preservation-design.md`, "Keys"](forensics-evidence-preservation-design.md#keys), decision 28, settled 2026-11-13 in the owner's words "FE O1: keep"). `lenny-evidence` holds encrypt and decrypt rights on the tenant keys of its region, to wrap and unwrap those data keys, and `lenny-evidence-reconcile` holds decrypt rights, to read evidence it compares. Both rights cover tenants whose state is `active` or `retained`, and Phase 4a's deletion of the key makes every evidence record wrapped under it unreadable. Credential envelope encryption follows the Secret class rules on every tenant and does not read the setting **[design]**.

## TSS-5 Deadlines and retention

**Erasure.** An erasure job records `deadline = start + erasure.userDeadlineHours` from the tenant's active settings at start (TSSR-11). `ErasureJobOverdue` fires when a job passes its recorded deadline. `lenny_erasure_job_duration_seconds` keeps its buckets, and the job's `deadline` is in the receipt **[design]**. §12.8's rule that "T3 data" has 72 hours and "T4 data" has 1 hour becomes one deadline per job. The Secret class row of §12.9 keeps its handling, "immediate, with cryptographic erasure where supported", as an ordering rule: the job removes Secret records first, inside the job's deadline **[design]**.

**Backups.** With `backups.erasureReconciler.enabled: false`, `lenny-ops` refuses at start a `backups.retention.retainDays` above the smallest `erasure.userDeadlineHours` of any tenant, and a settings write that would lower that minimum below the configured retention is refused with `SECURITY_SETTING_UNSATISFIABLE` **[design]**. With the reconciler enabled, no bound applies, as today.

**Tenant deletion.** Phase 1 records `deadline = start + deletion.tenantDeadlineHours` on the tenant record. `TenantDeletionOverdue` fires at 80% of the recorded deadline. `lenny_tenant_deletion_duration_seconds` loses the label `tenant_data_tier` and gains `deadline_hours` with the recorded value, whose values are bounded to 4 to 72 **[design]**.

**Retention.** The retention window stamped at creation and at the terminal transition is `retention.sessionArtifactHours` when set, and the deployment default of §7.1 otherwise. `POST /v1/sessions/{id}/extend-retention` keeps working, and the gateway never shortens a deadline it set later, as §7.1 states **[source]**. The tree's tier-keyed retention, which applied the T4 lease default of 24 hours to workspace artifacts, leaves **[design]**.

## TSS-6 Placement settings

The placement settings are operator-set values that enter every session's effective requirements under [PM-4](placement-design.md#pm-4-placement-requirements), with `details.source: operator`, at creation, resume, delegation, and derive (TSSR-12) **[design]**:

| Setting | Requirement it implies under PM-4 |
| :-- | :-- |
| `placement.nodeTenancy: dedicated` | `nodeTenancy: dedicated`: only pod sets with dedicated tenancy, which run on nodes labelled for this tenant alone. It replaces the operator's tenant-record `nodeTenancy` of PM-2, with PM-2's admission and grant rules applied to the settings write and to a template apply. |
| `placement.minIsolationProfile` | `minIsolationProfile` at least the value. The runtime's `isolationProfile` floor and the session's own value apply as well, and the strictest wins. |
| `placement.allowSharedPod: false` | `allowSharedPod: false`: only pools that run one session per pod. |
| `placement.allowReusedPod: false` | `allowReusedPod: false`: only pools whose pods serve one session and retire, that is pools with no recycling or with `recycle.maxSessionsPerPod: 1`. The placement design's requirements table carries it ([PM-4](placement-design.md#pm-4-placement-requirements)). |

Each requirement narrows pool selection. When no pool remains, the session is refused with `PLACEMENT_REQUIREMENT_UNMET`, and a resume, a delegation, or a derive behaves the same way. The tenant's own requirements in the tenant security record and an environment's requirements narrow further and never widen **[design]**. A tenant's `security-admin` may set each of the tenant's own requirements in that record, `allowReusedPod: false` included (D10). A write that removes or weakens an environment's requirement is a relaxation under this design's rule (TSSR-4): it needs a `justification`, is recorded in the tenant's audit chain, and takes effect after the tenant security record's weakening delay, cancellable by the tenant's `security-admin` or a `platform-admin`, as [PM-4](placement-design.md#pm-4-placement-requirements) states (D9). `requireNodeSeparation` and `dataResidencyRegion` stay where PM-4 places them.

The settings sit on the tenant rather than on the pool or the runtime: a pool and a runtime serve many tenants, the data being protected is the tenant's, and the operator's agreement that requires a control is with the tenant. The pool states what it offers, and the tenant states what it needs (D3).

## TSS-7 Changing a setting on a live tenant

A tightening becomes active in the write's transaction, and a relaxation at its `effectiveAt`. Every reader reads the tenant's active settings at the time it acts **[design]**. Nothing is drained by a change. An operator who needs every running session under the new values suspends the tenant, which drains its sessions ([§15.1](../spec/15_external-api-surface.md#151-rest-api), `POST /v1/admin/tenants/{id}/suspend`), and resumes it.

| Setting change | Pods and sessions | Checkpoints and objects | Keys and jobs |
| :-- | :-- | :-- | :-- |
| `tenantKey` `none` to `required` | Running sessions continue. Their next checkpoint uses the tenant key. A presigned capability minted before the change for the deployment key stays valid until it expires, at most its TTL. | Objects written before keep the deployment key under O11 (a), counted in `objectsUnderDeploymentKey`. A checkpoint that mixes chunks under both keys restores, because each object records its key. | The operator provisions the key first (O8 (a)). The write probes it. The state becomes `active`. |
| `tenantKey` `required` to `none` (relaxation) | After `effectiveAt`, new writes use the deployment key. Until then, writes keep failing closed if the key is unusable. | Objects under the tenant key keep it and stay readable through it. | The state becomes `retained` at `effectiveAt`. The probe continues, and Phase 4a still deletes the key. |
| `erasure.userDeadlineHours` | None. | None. | Jobs started after the change becomes active take the new deadline. A running job keeps its recorded deadline. |
| `deletion.tenantDeadlineHours` | None. | None. | A deletion past Phase 1 keeps its recorded deadline. |
| `retention.sessionArtifactHours` | None. | Sessions that reach their terminal transition after the change take the new window. Deadlines already stamped stay. | None. |
| `storage.platformChunkReuse` `true` to `false` | None. | The next checkpoint of each running session references no platform chunk and uploads the chunks it had referenced. Snapshots taken before keep their references until they rotate, and the tenant's own sources receive no further contribution. | None. |
| `nodeTenancy` `shared` to `dedicated` | Refused under PM-2 while the tenant holds access to a pool with shared tenancy, so the operator first gives the tenant single-tenant pools and removes its shared access. Running sessions continue on their nodes. New sessions, resumes, and delegated children land on dedicated pod sets. | None. | None. |
| `minIsolationProfile` raised | Running sessions continue. A new session, a resume, or a delegated child lands only on a pool that meets the new floor. | None. | None. |
| `allowSharedPod` `true` to `false` | Running sessions on a shared pod continue. New sessions go to pools with one session per pod. | None. | None. |
| `allowReusedPod` `true` to `false` | Running sessions continue. New sessions go to pools whose pods retire after one session, so the tenant's pinned idle pods on recycling pools receive no further sessions and leave inventory under §5.2's pinned idle inventory rules. | None. | None. |
| Any relaxation | Pending until `effectiveAt`, then new placements may use the wider set. Running sessions are unaffected. | None. | As above for the key. |

A relaxation of `tenantKey` during a key outage does not restore writes before `effectiveAt`. The operator restores the key at the provider and calls the probe route, which is the restoration §12.5 states today **[design]**.

The read route's `sessionsUnderEarlierVersion` counts the tenant's non-terminal sessions whose recorded version is below the active one, so an operator sees how much work still runs under earlier values **[design]**.

## TSS-8 Data classes

The classes of [§12.9](../spec/12_storage-architecture.md#129-data-classification) stay, named rather than numbered, and the top class takes the name Secret (D5) **[design]**:

| Former label | Name | Examples |
| :-- | :-- | :-- |
| T1 | Public | Runtime catalog metadata. |
| T2 | Internal | Session metadata, pool metrics, non-PII audit events. |
| T3 | Confidential | Workspace files and snapshots, transcripts, user identifiers, billing events, memory store contents, eviction state, and a tenant-derived runtime's shared assets. |
| T4, labelled Restricted | Secret | Credentials, OAuth and refresh tokens, credential pool secrets, credential leases, user-supplied API keys. |

The name Secret replaces the specification's label Restricted, which the Pod Security Standards profile of §17.2 and several designs also use, so a mention of the class needs no qualifier **[design]**. "PHI-tagged workspace data" leaves the Secret description, because workspace data no longer changes class.

The taxonomy stays in force in these places (inventory rows I35 to I56):

- §12.9: the class definitions, the default class per data type, the controls per class, the Internal storage-layer encryption definition, the store's refusal of a class it cannot protect, and the interaction with legal holds and credential encryption.
- §12.3: synchronous audit writes for Confidential and Secret events, opt-in batching for Internal events, and the write classification and durability tables.
- §17.8.2: the audit flush rows. §16.5: `AuditChainGap`'s note on Internal events.
- §4.4.5, §4.9, §14, §15.1 (`platform.bootstrap_applied`), and §12.8 (pseudonymized billing events): the class each names.
- The designs that classify their own records: user credential delivery, tenant scoping and identity, security oversight, LLM response cache removal, tenant outbound connectors, forensics evidence preservation, and session derive.

### How a class's requirements are enforced

A class is a table of requirements: one column of §12.9's **Controls driven by classification tier:**, which a data type's class selects. Labelling a kind of data with a class therefore commits each of its stores and paths to that column. Each requirement below states where it is enforced, or that it is retired for workspace data, which no mechanism enforces it on.

| Requirement | Where it is enforced |
| :-- | :-- |
| Audit logging writes | At runtime, on the audit write path of [§12.3](../spec/12_storage-architecture.md#123-postgres-ha-requirements): events about Confidential and Secret data are written synchronously, and Internal events may be batched by opt-in. A SIEM does not relax this **[source]**. |
| Retention default | At runtime, by the retention defaults applied to each kind of data: 90 days for Internal data, the deployment default of [§7.1](../spec/07_session-lifecycle.md#71-normal-flow) for Confidential session artifacts (7 days, or the tenant's `retention.sessionArtifactHours` under "TSS-5"), and 24 hours for Secret credential leases ([§4.9](../spec/04_system-components.md#49-credential-leasing-service)) **[source]**. Legal holds override every default. |
| Store write check | §12.9 **Enforcement.** states that every store method receives the class of what it writes and refuses a write it cannot protect **[source]**. The tree implements this only for the tenant-key case: the in-memory and local-filesystem artifact stores refuse a write for a tenant whose key is required, because they cannot apply envelope encryption, and the envelope-capable backends refuse when the tenant key is unusable (`checkTierStoreMismatch` and `ErrClassificationControlViolation` in `pkg/blobstore`) **[source]**. No other store receives a class at write time **[source]**. Under this design the artifact store keys the check on the tenant key state `active` and refuses with `CLASSIFICATION_CONTROL_VIOLATION` and `class_store_mismatch` (I39). Whether other stores take the check is under "Validation owed". |
| Encryption at rest for Secret data | Credentials, tokens, and leases are envelope-encrypted by the Token Service ([§4.3](../spec/04_system-components.md#43-token-service)) and the credential stores of §4.9 on every tenant, whatever its settings, which §12.9 states satisfies the class's encryption row **[source]**. |
| Access control and audit logging rows, for workspace data | Retired for workspace data. No mechanism reads a class to decide who may open a session's workspace files, snapshots, or transcripts, or to audit each read of them, and the T4 column's access row would deny users their own workspaces (I31) **[source]**. These rows stop applying to workspace data, and workspace data keeps the Confidential column's other rows. For other kinds of data, the access row is the role check of the API that serves the data **[inference]**. |
| Erasure, residency, and the other rows | Erasure follows "TSS-5", with Secret records removed first. Residency follows `dataResidencyRegion` on every tenant (I34, below). Storage-layer encryption of Internal data is checked by preflight as §12.9 states **[source]**. |

**Tenant shared assets.** A tenant-derived runtime's `sharedAssets`, its `inline` content and its uploaded `artifact` objects, are Confidential on every tenant, because a tenant admin writes them and they can hold the tenant's own content ([`tenant-derived-runtimes-design.md`, "Tenant shared assets"](tenant-derived-runtimes-design.md#tenant-shared-assets)) **[design]**. An uploaded artifact is chunked at upload into the packs of the runtime's tenant-owned assets set, which are its only stored copy. Each pack is an object-store write of the tenant, so it uses the tenant key while the state is `active`, as "Writes and mints" states, and each pack's `artifact_store` row records the key scope. Deleting a derived runtime deletes its uploads, and an upload that no version of the runtime in use references is deleted after a grace period of 7 days. The owner confirmed the single copy and these deletions on 2026-11-14, under decision 44 of that design. Tenant deletion removes the runtime's rows, the inline content, and the uploaded artifacts in §12.8's order, with the tenant-owned platform chunk sets of those assets, and an artifact or chunk set under legal hold stays until the hold is released, as every legal-hold object does, whichever of these deletions would otherwise remove it. The documentation states that a tenant's assets must not hold secrets: every session on the runtime reads them at `/lenny/assets`, and a Secret value there would sit outside the envelope encryption that Secret data requires.

**Residency.** The cross-region transfer prohibition §12.9 adds for T4 (I34) becomes part of `dataResidencyRegion` for every tenant: a resource of a tenant with a region whose own region differs from the tenant's is refused with `REGION_CONSTRAINT_VIOLATED` **[design]**. §12.8 already states that sessions inherit the environment's region and environments the tenant's, so the check enforces the stated inheritance on every region-bearing resource **[inference]**. The tree's dedicated code `CodeRegionCrossTransferProhibited` leaves.

## TSS-9 Removal of `workspaceTier`

There are no deployments, so the removal has no compatibility path (D4) **[design]**:

- The tenant column `workspace_tier`, its check constraint (migration `0103_tenant_workspace_tier_check`), and the environment column are dropped. A new migration adds `security_settings` (JSONB with a check per member), `security_settings_version`, `security_template_name`, `security_template_version`, `security_template_applied_at`, `security_template_applied_by`, `tenant_key_state`, `tenant_key_last_probe_success_at`, the `tenant_security_settings_versions` and `security_templates` tables, the session column `security_settings_version`, and the `artifact_store` column `encryption_key_scope`. No tenant column for `nodeTenancy` is added: the placement design reads the tenant's node tenancy from `securitySettings.placement.nodeTenancy`.
- `workspaceTier` leaves the `Runtime` and `SandboxTemplate` CRDs, the runtime admin payloads, the runtime store and its base merge, and the pool controller's tier label.
- The `lenny-t4-node-isolation` webhook, its chart template, its feature-flag mapping under `features.compliance`, and its label `lenny.dev/workspace-tier` leave as the placement design's specification changes state.
- `pkg/tenantkms` loses its provisioning path (O8 (a)) and keys rotation, probing, and destruction on the key state.
- A bootstrap seed and the backup export carry `securityTemplate` and `securitySettings` in place of `workspaceTier`.
- A write that names `workspaceTier` on the tenant, an environment, a runtime, a pool, or the bootstrap seed, or `nodeTenancy` on the tenant record, is refused with `400 VALIDATION_ERROR` whose `details.field` names it, whatever value it carries, under the one rule for removed fields (D11). The tenant `PUT` row of "TSS-3" states the same refusal.

Each specification paragraph that gives up content keeps a successor pointer naming the heading that now owns it, as `spec-citations.md` requires ("Specification changes"). The new home of the settings model is a new section, §12.10 Tenant Security Settings, beside the taxonomy it replaces.

## Worked example

`alice@acme.com`, a `platform-admin`, onboards `acme`, which processes health records. She provisions the KMS key with alias `tenant:acme` in the `eu-west-1` KMS, labels and taints a node pool `lenny.dev/tenant=acme` under PM-2, and calls `POST /v1/admin/tenants` with `dataResidencyRegion: eu-west-1` and `securityTemplate: {"name": "regulated"}`. The gateway resolves `regulated@1`, probes `tenant:acme` at the region's KMS endpoint, and creates the tenant with the key state `active`, version 1, and `securityTemplate` set. A `tenant.security_template_applied` event records the values in the platform chain and in `acme`'s chain. She then creates a single-tenant platform pool with `placement.tenantId: acme`, a dedicated offering, `sandboxed` isolation, one session per pod, and no recycling, and grants `acme` access to it.

A session of `bob@acme.com` reaches the post-chain point. Its effective requirements include, with source `operator`, dedicated tenancy, at least `sandboxed`, `allowSharedPod: false`, and `allowReusedPod: false`, and the pool meets them. Its checkpoints are written under `tenant:acme`. At its terminal transition its artifacts get a 24-hour window.

Six months later the operator sets 12-hour artifact retention for `acme`. `PUT .../security-settings` with `retention.sessionArtifactHours: 12` tightens a setting, needs no justification, and activates version 2 at once. The read route lists `retention.sessionArtifactHours` under `differsFromTemplate`. A Lenny release ships `regulated@2`. The tenant list filtered with `templateOutdated=true` returns `acme`, and nothing changes for `acme` until `alice` applies `regulated@2`. If `regulated@2` relaxed a value `acme` holds, the apply would need a `justification`, would mirror a critical event to `acme`'s chain, and would hold the relaxation for `acme`'s weakening delay, during which `acme`'s `security-admin` could cancel it.

When `acme` leaves, Phase 1 records a 4-hour deadline, and Phase 4a disables `tenant:acme` and schedules its deletion.

## Limits

- Objects written before the tenant key was switched on stay under the deployment key until retention removes them (O11 (a)). Objects under legal hold never expire, so the count can stay above zero for the tenant's life.
- A tightened setting does not move running sessions. Full coverage needs a tenant suspension, which ends the sessions.
- A relaxation waits the tenant's weakening delay, including during an incident (O5 (b)).
- The deadlines' lower bounds are today's T4 values. The platform has no measurement that its erasure job meets 1 hour at scale ("Validation owed").
- `placement.nodeTenancy: dedicated` costs idle nodes and a single-tenant pool per tenant, which the placement design sizes and bills.
- A template cannot set `dataResidencyRegion` or `complianceProfile`, because each needs an operator's choice of region or framework, and it cannot create the single-tenant pools that dedicated tenancy needs. The follow-up design of O9 makes the frameworks templates of their own.
- The store write check of a class covers only the artifact store's tenant-key case ("TSS-8").

## Alternatives rejected

- **Keep T4 and add more tiers.** Each new tier would bundle controls again, which is the defect the owner's direction removes.
- **Templates by reference.** O1 (b). A template edit would change many tenants with no per-tenant record.
- **Settings on the Runtime or the pool.** A runtime and a pool serve many tenants, and today's runtime field lets a T4 tenant's sessions run on shared nodes ("Problem").
- **Settings inside the tenant security record.** O13 (b). The record is the tenant security team's, and a second writer per section complicates its rules.
- **A per-setting ratchet with decommission routes.** O5 (c). It adds routes without adding protection beyond the justification, the mirror, and the delay.
- **A tenant role may tighten.** O3 (b). The controls a tenant would tighten need operator resources, and placement requirements already let a tenant narrow its placement.

## Specification changes

| Section | Change |
| :-- | :-- |
| [README](../spec/README.md), table of contents | Add 12.10 Tenant Security Settings. |
| [§4.4.5 Checkpoint Storage Failure and Eviction Fallback](../spec/04_system-components.md#445-checkpoint-storage-failure-and-eviction-fallback), **Tenant isolation for `session_eviction_state`.** | "T3 Confidential data" becomes "Confidential data". |
| [§4.9 Credential Leasing Service](../spec/04_system-components.md#49-credential-leasing-service), "Three Credential Modes" and **User-scoped credential storage:** | "T4 Restricted data" becomes "Secret data". |
| [§5.2 Pool Configuration and Execution Modes](../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), **T4 cross-tenant reuse prohibition.** | Removed, with a successor pointer to **Tenant pinning:** (I23) and to §12.10 for the tenant's pod sharing and reuse settings. |
| [§6.4 Pod Filesystem Layout](../spec/06_warm-pod-model.md#64-pod-filesystem-layout), **T4 workspace limitation:** and **T4 dedicated-node requirement:** | The node disk encryption statement stays for every tenant. The T4 text and the webhook are removed, with a successor pointer to §12.10 and to the placement design's node tenancy section. |
| [§7.1 Normal Flow](../spec/07_session-lifecycle.md#71-normal-flow), **Artifact retention:** | The default window is the tenant's `retention.sessionArtifactHours` when set. |
| [§11.7 Audit Logging](../spec/11_policy-and-controls.md#117-audit-logging), **Compliance profile downgrade ratchet.** | The comparison with the `workspaceTier` stricter-only rule becomes a reference to the relaxation rule of §12.10. The ratchet itself stays until the follow-up design of O9 replaces it. |
| [§12.3 Postgres HA Requirements](../spec/12_storage-architecture.md#123-postgres-ha-requirements), **Batching guidance for write-heavy paths:**, the write classification table, and the durability table | T2, T3, and T4 become Internal, Confidential, and Secret. |
| [§12.5 Artifact Store](../spec/12_storage-architecture.md#125-artifact-store), **Encryption at rest:** and **T4 per-tenant KMS key lifecycle.** | Renamed **Tenant key lifecycle.** and keyed on `encryption.tenantKey` and the tenant key state: the states, with objects and evidence data keys under the tenant key both counted before `retained` moves to `none`, the key users `lenny-evidence` and `lenny-evidence-reconcile`, the probe route in place of the re-assert, the renamed probe settings and metrics, `TENANT_KEY_UNAVAILABLE`, and the per-provider binding under "tenant key". |
| [§12.8 Compliance Interfaces](../spec/12_storage-architecture.md#128-compliance-interfaces) | **T4 ephemeral workspace isolation.** is removed with a successor pointer to §12.10. **Erasure SLA enforcement.** becomes **Erasure deadline enforcement.** with one recorded deadline per job and the Secret-first ordering. **Backups in erasure scope.** bounds retention by the smallest tenant deadline. The tenant deletion table's tombstone keeps the settings and key state, and Phase 4a runs on the key state. **Tenant deletion SLA.** becomes **Tenant deletion deadline.** with the recorded deadline and the `deadline_hours` label. **KMS key residency.** names the tenant key. **Data residency admission control.** requires every tenant's region-bearing resources to match the tenant's region. `DeleteByUser` step 14 and the pseudonymization note use the new names. |
| [§12.9 Data Classification](../spec/12_storage-architecture.md#129-data-classification) | Classes renamed Public, Internal, Confidential, and Secret, with T4's label Restricted becoming Secret. A statement per control row of where it is enforced ("TSS-8", "How a class's requirements are enforced"), with the access control and audit logging rows retired for workspace data. **Per-tenant workspace classification.** removed with a successor pointer to §12.10. Workspace data is Confidential. The control table's residency row and **Interaction with existing controls.** drop the T4 cross-region clause, which §12.8 **Data residency.** states for every tenant. **Enforcement.** keeps the class-store refusal with `class_store_mismatch` and moves settings validation to §12.10. |
| §12.10 Tenant Security Settings (new) | The settings model, their order and validation, versions and the relaxation rule with the tenant security record's delay, templates with versions and apply, the live-change rules, the tenant key states, the requirements the placement settings imply under the placement design's section, and `storage.platformChunkReuse` with the deployment key for operator-source chunks (D8). |
| [§13.2.3 Checkpoint Capability Model](../spec/13_security-model.md#1323-checkpoint-capability-model) | "The `PUT`-path T4 encryption binding" becomes "the tenant key binding". |
| [§13.2.4 `lenny-system` Namespace NetworkPolicies](../spec/13_security-model.md#1324-lenny-system-namespace-networkpolicies) | `lenny-t4-node-isolation` leaves the webhook list, as the placement design's changes state. |
| [§14 Workspace Plan Schema](../spec/14_workspace-plan-schema.md#14-workspace-plan-schema), `callbackSecret` | "T3" becomes "Confidential". |
| [§15.1 REST API](../spec/15_external-api-surface.md#151-rest-api), **Comprehensive Admin API:** | The tenant rows of "TSS-3", the new routes, and the template routes. The decommission row's reference to `workspaceTier` names the relaxation rule. The bootstrap row's "(T3)" becomes "(Confidential)". |
| [§15.1 REST API](../spec/15_external-api-surface.md#151-rest-api), **Error code catalog:** | New codes of "TSS-3". `CLASSIFICATION_CONTROL_VIOLATION` keeps `class_store_mismatch` alone. `COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED` names the relaxation rule. |
| [§16.1 Metrics](../spec/16_observability.md#161-metrics) | Renamed probe metrics and the deletion histogram's labels. |
| [§16.5 Alerting Rules and SLOs](../spec/16_observability.md#165-alerting-rules-and-slos) | `TenantKeyUnusable`, `TenantSecuritySettingRelaxationRequested`, the recorded deadlines in `ErasureJobOverdue` and `TenantDeletionOverdue`, and the text of `MemoryStoreErasureDurationHigh`, `KmsKeyDeletionFailed`, and `AuditChainGap`. `T4NodeIsolationWebhookUnavailable` and the `lenny-t4-node-isolation` entry of `AdmissionPlaneFeatureFlagDowngrade` leave with the webhook. |
| [§16.7 Section 25 Audit Events](../spec/16_observability.md#167-section-25-audit-events) | The audit events of "TSS-3". The webhook name leaves `deployment.feature_flag_downgrade_acknowledged`. |
| [§17.2 Namespace Layout](../spec/17_deployment-topology.md#172-namespace-layout) | Item 10, the rejection labels, and the feature-gated chart inventory lose `lenny-t4-node-isolation`. |
| [§17.6 Packaging and Installation](../spec/17_deployment-topology.md#176-packaging-and-installation), "Checks performed" | The checkpoint default-encryption check applies when any tenant key is active. The T4 webhook check and the inventory entry leave. |
| [§17.7 Operational Runbooks](../spec/17_deployment-topology.md#177-operational-runbooks) | The admission-plane downgrade runbook loses the webhook name. |
| [§17.8.1 Operational Defaults — Quick Reference](../spec/17_deployment-topology.md#1781-operational-defaults--quick-reference) | The renamed probe settings, `tenantSecurity.defaultTemplate`, and `tenantSecurity.templates`. |
| [§17.8.2 Capacity Tier Reference](../spec/17_deployment-topology.md#1782-capacity-tier-reference) | The audit flush rows use class names. |
| §17.9.1 Composition Dimensions, isolation profile row | "T4 webhook requirements" leaves. |
| §17.9.7 Backend-Invariant Requirements | "T4 per-tenant key binding" becomes "tenant key binding", and "T4-capable deployment" becomes "a deployment with an active tenant key". |
| §18.31 Phase 13 | The webhook item leaves. The T4 key lifecycle item becomes the tenant key lifecycle and the security settings routes. |
| §23, "Additional Platform Capabilities" | "per-tier SLA enforcement (T3: 72h, T4: 4h)" becomes "per-tenant deletion deadlines". |
| [§24.10 Tenant Management](../spec/24_lenny-ctl-command-reference.md#2410-tenant-management) | The commands of "TSS-3". |
| §25, "`lenny-ops` Helm Values", "Downstream SIEM Scope Boundary (GDPR Erasure)", and "ArtifactStore Backup (MinIO workspace bucket replication)" | The backup bound names the smallest tenant erasure deadline, the SIEM text names the tenant's erasure deadline, and crypto-shredding names tenants with a tenant key. |
| [§25.12 MCP Management Server](../spec/25_agent-operability.md#2512-mcp-management-server) | Tools for the new routes. |

## Non-specification changes

- `migrations/`: the migration of "TSS-9".
- `pkg/apis/lenny/v1alpha1`: `workspaceTier` leaves `Runtime` and `SandboxTemplate`, and the CRD manifests are regenerated with their overlay kept.
- `pkg/gateway/environment/tenantstore` and `environmentstore`: the settings, version, template, and key state fields, and the removal of the tier and its rank helpers.
- `pkg/gateway/externalapi/admin`: the routes and validation of "TSS-3", the tenant create path, bootstrap, and the removal of tier handling from runtimes, pools, and environments.
- `pkg/securitytemplates` (new): built-in templates, Helm template loading, digest checks against `security_templates`, and apply.
- The SO-2 applier: a second record type for the settings versions, so one periodic applier activates pending relaxations of both.
- `pkg/tenantkms`: the key state machine, the probe set, and the removal of provisioning.
- `pkg/blobstore` and the presign path: key selection by key state and `encryption_key_scope` on each row.
- `pkg/controller/tenantdeletion`: recorded deadline and Phase 4a on key state.
- `pkg/gateway/sessionserver`: retention from the setting, the session's settings version, and settings validation at the post-chain point.
- `pkg/gateway/policy/policy/classification.go`: replaced by settings validation.
- `pkg/admission/data_residency_validator`: the universal region match.
- `pkg/admission/t4_node_isolation`, `pkg/admission/webhook/t4_node_isolation.go`, and `pkg/controller/sandbox/podspec/t4_node_isolation.go`: removed, as the placement design's changes state.
- `pkg/gateway/runtime/poolstore` and `pkg/gateway/runtime/runtimestore`: the T4 reuse check and the tier merge leave.
- `pkg/ops/backup/runner`: export of the new tenant fields, and the retention bound.
- `cmd/lenny-gateway`, `cmd/lenny-webhook`, and `cmd/lenny-preflight`: flags, the start-up assertion, and the preflight check.
- `charts/lenny`: values, the template configuration, the alert rules, and removal of the T4 webhook template and its tests.
- `docs/`: `api/admin.md`, `operator-guide/multi-tenancy.md`, `operator-guide/security.md`, `operator-guide/configuration.md`, `operator-guide/observability.md`, `operator-guide/installation.md`, `reference/configuration.md`, `reference/metrics.md`, `reference/execution-modes.md`, `alerting/rules.yaml`, and `runbooks/admission-plane-feature-flag-downgrade.md`, with a new operator page on templates.
- `TESTING.md`: the test rows that name T4.

## Documentation

The new operator page on templates owns the settings model, and `operator-guide/multi-tenancy.md` and `operator-guide/security.md` link to it. Pages carry no section numbers (`doc-content.md`). Each item below is stated on a page.

| Topic | What the pages state |
| :-- | :-- |
| Settings | Each setting of `Tenant.securitySettings` with its type, bounds, default, and order from weaker to stronger ("TSS-1"): `encryption.tenantKey` (`none`), `erasure.userDeadlineHours` (72, from 1 to 72), `deletion.tenantDeadlineHours` (72, from 4 to 72), `retention.sessionArtifactHours` (unset, from 1 to 8760), `storage.platformChunkReuse` (`true`), `placement.nodeTenancy` (`shared`), `placement.minIsolationProfile` (unset), `placement.allowSharedPod` (`true`), and `placement.allowReusedPod` (`true`), and that only a `platform-admin` writes them. |
| Templates | The values of `standard@1`, `confidential@1`, and `regulated@1`, apply by copy, immutable versions, `tenantSecurity.defaultTemplate` (`standard`) and `tenantSecurity.templates` for operator-defined templates, and the start-up refusal of a changed version. |
| Relaxation | That a relaxation needs a `justification`, is mirrored to the tenant's chain, waits the tenant security record's `weakeningDelaySeconds` within `security.config.minWeakeningDelaySeconds` (3600) and `security.config.maxWeakeningDelaySeconds` (604800), and can be cancelled by the tenant's `security-admin`, and that the same rule governs the removal or weakening of an environment's `placementRequirements` (D9). That a write naming `workspaceTier` or the tenant-record `nodeTenancy` is refused with `400 VALIDATION_ERROR` naming the field (D11). |
| Live changes | The table of "TSS-7", including that a tightening does not move running sessions and that full coverage needs a tenant suspension. |
| Tenant key | The states and their transitions, that objects and evidence data keys under the key keep it and hold the state at `retained`, the probe route and the procedure after a rotation or an outage, `storage.tenantKeyProbeInterval` and `storage.tenantKeyProbeRateLimit`, the per-provider binding and its preflight check, `SECURITY_SETTING_UNSATISFIABLE` on a backend without per-tenant configuration, and that operator-source platform chunks stay under the deployment key for a tenant with its own key. |
| Placement settings | The requirement each placement setting implies, `PLACEMENT_REQUIREMENT_UNMET` with `details.source: operator`, and that `nodeTenancy: dedicated` needs single-tenant pools and labelled nodes first ("TSS-6"). |
| Data classes | The names Public, Internal, Confidential, and Secret, the default class per kind of data, and where each requirement is enforced ("TSS-8"), including that a tenant-derived runtime's shared assets are Confidential, are deleted with the tenant, stay under a legal hold, and must not hold secrets. |
| API, errors, events, and alerts | The routes and `lenny-ctl` commands of "TSS-3", the error codes `TENANT_KEY_UNAVAILABLE`, `SECURITY_SETTING_JUSTIFICATION_REQUIRED`, `SECURITY_SETTING_UNSATISFIABLE`, and `TENANT_SECURITY_SETTINGS_INVALID`, the audit events, the metrics `lenny_tenant_key_probe_last_success_timestamp` and `lenny_tenant_key_probe_result_total`, and the alerts `TenantKeyUnusable` and `TenantSecuritySettingRelaxationRequested`, each with a runbook. |
| Limits | Each item under "Limits". |

## Tests

| Tier | Test |
| :-- | :-- |
| 0 | No `workspaceTier`, `lenny-t4`, or `t4Kms` identifier remains in `pkg/`, `cmd/`, `charts/`, or `docs/`, apart from the validators' lists of removed fields and their tests (D11). Built-in templates validate against the settings schema. |
| 1 | Each setting's validation, bounds, and order. Classification of a write into tightenings and relaxations for each setting, including null retention compared as the deployment default. Template apply computing the changed and relaxed set. Template digest mismatch. The key state machine, which stays `retained` while either an object or an evidence data key is under the tenant key and moves to `none` only when both counts are zero. The retention window resolution. The backup retention bound. The requirements each placement setting implies. |
| 2 | The tenant store and environment store round-trip the new fields. The tenant deletion controller records the deadline at Phase 1 and runs Phase 4a on `active` and `retained`, and not on `none`. |
| 3 | The admin routes' request and response bodies, error codes, `If-Match`, and `dryRun`. The MCP tools generated for them. A tenant, environment, runtime, pool, or bootstrap write that names `workspaceTier`, and a tenant write that names `nodeTenancy`, are refused with `400 VALIDATION_ERROR` naming the field (D11). An environment write that removes a placement requirement without a `justification` returns `SECURITY_SETTING_JUSTIFICATION_REQUIRED` (D9). |
| 4 | On the compose stack: apply `regulated` with a reachable and an unreachable key; a relaxation with and without `justification`, held until `effectiveAt` and cancelled by `security-admin`; a mixed write; the mirror rows in the tenant chain; `GET` of the view with `differsFromTemplate`, `newerVersionAvailable`, and `sessionsUnderEarlierVersion`; a checkpoint written under the tenant key after the switch and under the deployment key before; `objectsUnderDeploymentKey`; the retained state reaching `none` after GC; an erasure job keeping its deadline across a settings change. |
| 4 | A tenant under `regulated` takes snapshots that reference no platform chunk, and a tenant under `standard` references the chunks of its eligible sources. Setting `storage.platformChunkReuse: false` makes the next checkpoint upload the chunks it had referenced. |
| 5 | On Kind: a session of a tenant with `nodeTenancy: dedicated` lands on its labelled nodes; a session refused with `PLACEMENT_REQUIREMENT_UNMET` and `source: operator` when no pool meets `minIsolationProfile`; `allowReusedPod: false` keeping the tenant off recycling pools; a running session undisturbed by a tightening. |
| 7a | Concurrent settings writes under `If-Match`. A settings change racing a checkpoint mint. A relaxation activating while a tightening is written. |
| 8 | The tenant key disabled at the provider while `active`: writes fail closed, `TenantKeyUnusable` fires, and the probe route restores. |
| 9 | No tenant role can write settings or apply a template, and each reads only its own tenant. A tenant's placement requirement cannot widen its settings. No write falls back to the deployment key while `active`. |
| 11 | Every new alert resolves to a runbook. The template documentation matches the built-in values. |

## Validation owed

- The erasure job meeting a 1-hour deadline, and the tenant deletion meeting 4 hours, on a tenant with production data volumes. Today's T4 values are claims with no measurement found in the tree.
- The `encryption_key_scope` count and the move from `retained` to `none` at scale, against the `artifact_store` table's indexes.
- The per-provider binding of the tenant key when switched on for a tenant with existing objects, on MinIO, S3, GCS, and Azure, including a GCS tenant moving to a per-tenant bucket.
- That every region-bearing resource the residency webhook sees carries the tenant's region today, so the universal check of I34 refuses nothing that §12.8 allows **[inference]**.
- That one SO-2 applier can activate pending versions of two record types without reordering a tenant's writes.
- Which stores other than the artifact store hold Secret or Confidential data that §12.9's store write check should refuse, such as Redis for credential leases and the memory store's pluggable backends, and whether each can receive the class at write time ("TSS-8").

## Instructions for reviewers of other designs

- [`placement-design.md`](placement-design.md) (applied in its revision 2): replace the operator's tenant-record `nodeTenancy` of PM-2 with `Tenant.securitySettings.placement.nodeTenancy`, written through `PUT /v1/admin/tenants/{id}/security-settings` and a template apply, and apply PM-2's admission and grant rules to those writes (I21). In PM-4, take `placement.minIsolationProfile` as the tenant floor, `placement.allowSharedPod: false` as an operator source of `allowSharedPod: false`, and add `allowReusedPod: false` as a requirement with its pool test ("TSS-6"). The source `operator` covers these settings. O8 resolves: `regulated` sets `nodeTenancy: dedicated`, and `confidential` does not. I25 asks that the reserved overlay rule cover what DR-10's T4 refusal covered.
- [`security-oversight-design.md`](security-oversight-design.md): the security posture gains a `securitySettings` section with the view of "TSS-3". SO-4a's table gains rows for the audit events of "TSS-3", mirrored synchronously. The applier of SO-2 activates pending versions of the security settings as well as of the tenant security record, and a tenant's `security-admin` may cancel a pending relaxation of the settings (O5 (b)). The `audit` section's `complianceProfile` stays. Audit payloads are Internal or Confidential (I52).
- [`runtime-revisions-design.md`](runtime-revisions-design.md): remove `workspaceTier` from the versioned, pod-affecting fields, from the reader table, and from the notes on the classification, including "A tightening such as `T3` to `T4` uses `immediate`" (I28). The pod hash loses the field. The isolation floor is unchanged.
- [`tenant-derived-runtimes-design.md`](tenant-derived-runtimes-design.md): remove `workspaceTier` from the "Fields" row of refused fields (I27). Replace the refusal of `reservedPlacement` on a T4 runtime under DR-10 **Nodes.** with the placement design's reserved overlay and single-tenant pools (I25). The finding DF-12 names `workspaceTier` as an omitted merge field, which no longer exists.
- [`forensics-evidence-preservation-design.md`](forensics-evidence-preservation-design.md) (applied in its revision 32): wrap evidence data keys with the tenant key while the tenant key state is `active`, and keep keys wrapped under it when the state becomes `retained`, as its decision 28 settles (I12). Classify activity streams Confidential on every tenant (I55). Replace the "T3 and T4" term, the Phase 4a wording, and the 4-hour deadline in the worked example with the tenant's recorded deletion deadline (I18). `lenny-evidence-reconcile` holds decrypt rights for tenant keys.
- [`egress-and-git-design.md`](egress-and-git-design.md): replace the "Workspace tier T4" term with the tenant settings, and restate the security analysis's "T4 nodes host several T4 tenants" for dedicated node tenancy per tenant (I24). The evidence-stream validation item names the tenant key.
- [`session-derive-design.md`](session-derive-design.md): the upload retention example becomes the tenant's `retention.sessionArtifactHours` (I19), and the transcript classification reference names the Confidential class (I56).
- [`user-credential-delivery-design.md`](user-credential-delivery-design.md) (applied): `user_links` is in the Secret class (I50), and the erasure of a stored link finishes within the tenant's `erasure.userDeadlineHours` in place of a fixed 1-hour deadline (I13).
- [`tenant-scoping-identity-design.md`](tenant-scoping-identity-design.md): directory records are Confidential, and SCIM credential hashes and the client secret are Secret (I51).
- [`llm-response-cache-removal-design.md`](llm-response-cache-removal-design.md): the removed row is "Semantic cache entries, Confidential" (I53).
- [`tenant-outbound-connector-design.md`](tenant-outbound-connector-design.md): classify the new records with the class names (I54).
- [`session-isolation-design.md`](session-isolation-design.md), [`agent-pod-layout-design.md`](agent-pod-layout-design.md), and [`workspace-materialization-design.md`](workspace-materialization-design.md): "Restricted" in these designs names the Pod Security Standards profile, and the data class is now Secret, so the word needs no qualifier. `placement.allowSharedPod` and `placement.allowReusedPod` decide whether a tenant's sessions reach a pod with several slots or a reused pod at all.
- Every design that names a data class: the top class is Secret (D5). Records of past reviews keep the words they were written with.
- `compliance-profile-design.md` (pending): split `complianceProfile` into deployment-level Helm settings and per-tenant settings, with `soc2`, `fedramp`, and `hipaa` as templates and the weakening delay in place of the ratchet and its decommission route, reusing this design's template versioning, apply, and relaxation rule (O9 (b)).

## Proposal split and order

| Proposal | Content | Depends on |
| :-- | :-- | :-- |
| TSS-A | The class renames of "TSS-8" across the specification, with Secret for the top class, the enforcement statement per control row, and the universal region match (I34). | Nothing. |
| TSS-B | The settings model, §12.10, versions and the relaxation rule, the admin routes, templates, the tenant key states and renames, the deadlines, retention, and the removal of the tenant and environment `workspaceTier`. | TSS-A, and SO-2's proposal for the applier, the weakening delay, and SO-4a's mirror. |
| TSS-C | The placement settings as operator requirements, the removal of §6.4 and §5.2's T4 rules, and the removal of `workspaceTier` from the Runtime and `SandboxTemplate`. | TSS-B, and the placement design's proposals for PM-2 and PM-4, landed together with the placement design's removal of the T4 webhook. |

TSS-B can land before the placement design, because the placement settings it stores take effect only when TSS-C lands. Until then the T4 webhook keeps reading the Runtime's field, so TSS-B must not remove that field.

## Sources

- The Lenny specification under `spec/`, read on 2026-11-12: §4.4.5, §4.9, §5.2, §5.3, §6.4, §7.1, §10.7, §11.7, §12.3, §12.5, §12.8, §12.9, §13.2.3, §13.2.4, §14, §15.1, §16.1, §16.5, §16.7, §17.2, §17.6, §17.7, §17.8.1, §17.8.2, §17.9.1, §17.9.7, §18.31, §23, §24.10, and §25.
- The designs under `designs/`, read on 2026-11-12, as cited in the inventory, with [`placement-design.md`](placement-design.md) revisions 1 and 2 and [`security-oversight-design.md`](security-oversight-design.md) revision 47 for PM-2, PM-4, SO-2, and SO-4a.
- The owner's answers of 2026-11-12 to the open decisions and the rename N1, read from the consistency pass's decision log in the session scratchpad.
- The Lenny tree, read on 2026-11-12: `pkg/tenantkms`, `pkg/admission/t4_node_isolation`, `pkg/admission/data_residency_validator`, `pkg/controller/sandbox/podspec`, `pkg/controller/tenantdeletion`, `pkg/gateway/sessionserver/lifecycle.go`, `pkg/gateway/environment/tenantstore`, `pkg/gateway/runtime/poolstore`, `pkg/gateway/externalapi/admin`, `pkg/gateway/policy/policy/classification.go`, `pkg/blobstore`, `cmd/lenny-gateway/t4_encryption.go`, and `migrations/0103_tenant_workspace_tier_check.up.sql`.

## Review history

No review has read this design.

### Revision notes

- Revision 1, 2026-11-12. Written from the owner's direction of 2026-11-12 (D1 to D4) and the coordinator's recommendations, which are recorded as O1 to O4 with recommendations. Adds the inventory of every tier and classification rule in the specification and the designs, the settings model, templates, the admin API, the tenant key states, the live-change rules, the taxonomy rename, and the removal plan. Records O5 to O13 from what the inventory and the placement and security-oversight designs showed: the relaxation path through SO-2's weakening delay, environment overrides, the meaning of dedicated node tenancy, key provisioning, `complianceProfile`, operator-defined templates, data written before the tenant key, the isolation value of `regulated`, and the relationship with the tenant security record. Setting names under `placement` follow PM-4's requirement names.
- Revision 2, 2026-11-12, consistency pass. Records the owner's answers of 2026-11-12: O1, O3, O4, O6, O7, O8, O10, O11, O12, and O13 settled at (a); O2 at (a), keeping the taxonomy; O5 at (b), the relaxation delay; and O9 at (b) as a follow-up design, `compliance-profile-design.md` (pending), with `complianceProfile` left unchanged here (D6, D7). Applies the owner's rename N1 (D5): the top data class is Secret, and the classes are Public, Internal, Confidential, and Secret, in the scope, terms, high-level design, requirements, security properties, inventory successors, TSS-4, TSS-5, TSS-8, the specification changes, and the reviewer instructions, while the inventory's rule column keeps the specification's current tier labels. Adds "How a class's requirements are enforced" under TSS-8, with TSSR-15, a limit, and a validation item for stores other than the artifact store. Takes in the placement design's revision 2, which moved the tenant's node tenancy here and added `allowReusedPod` as a requirement.
- Revision 3, 2026-11-13. Adds `storage.platformChunkReuse` to `securitySettings` for the platform chunk sets of decision D1 of `incremental-checkpoint-design.md`, with `false` the stronger value, `false` in `regulated` and `true` in `standard` and `confidential`, its row in the live-change table, and the statement under "TSS-4" that a tenant with its own key references operator-source chunks stored under the deployment key, as the owner accepted on 2026-11-13 (D8).
- Revision 4, 2026-11-13, cross-design review. Applies the forensics design's decision 28, which the owner settled in the words "FE O1: keep": evidence data keys wrapped by the tenant key stay so wrapped under `retained` and count, with the tenant's objects, toward the move to `none`, and the read route reports `objectsUnderTenantKey` and `evidenceKeysUnderTenantKey`. **Other users of the key.** names `lenny-evidence` and `lenny-evidence-reconcile` and their rights. Under `agent-runtimes-only-design.md`, the service-mode clause of TSS-6's `allowReusedPod` row and the session-mode qualifiers leave. The high-level settings table gains `storage.platformChunkReuse`, and "Documentation" is new. The read-only cross-design check of the same day changes no rule: I13 lists the user credential design's former 1-hour deadline for the erasure of a stored link among its carriers, which that design now states as the tenant's `erasure.userDeadlineHours`, the reviewer instruction to that design records it as applied, and the "reads against" list names each cited design's current revision.
- Revision 5, 2026-11-13. Applies the owner's answers of 2026-11-13 to the consolidated list of open decisions, and changes these rules. D9: the removal or weakening of an environment's `placementRequirements` is a relaxation under this design's rule, with a `justification` and the weakening delay, so TSS-6 says so and `SECURITY_SETTING_JUSTIFICATION_REQUIRED` covers it. D10 records that a tenant's `security-admin` sets `allowReusedPod`. D11: a write that names `workspaceTier` or the tenant-record `nodeTenancy` is refused with `400 VALIDATION_ERROR` naming the field, under the one rule for removed fields; TSS-9, the tier 0 and tier 3 tests, and "Documentation" change to match. The status line's reads-against list names the current revision of each cited design.
- Revision 6, 2026-11-14. Applies the owner's settled answer to question 17, decision 42 of `tenant-derived-runtimes-design.md`, in the owner's words "sharedAssets should define any git stores to warm up. workspaceDefaults shouldn't also do that through `warm: true`", and changes these rules. TSS-8 classes a tenant-derived runtime's shared assets as Confidential, stores an uploaded artifact asset under the tenant key while the key's state is `active`, deletes the assets with the tenant, keeps them under a legal hold, and states that the documentation forbids secrets in them. "Platform chunk sets" names the tenant-owned sources as a tenant-derived runtime's `gitRepository` assets and its `inline` and `artifact` assets, in place of warm default clones.
- Revision 7, 2026-11-14. Follows `tenant-derived-runtimes-design.md` revision 73, which applies the independent review of the question 17 write-up, and changes these rules. TSS-8's **Tenant shared assets.** stores an uploaded artifact asset only as packs of the runtime's tenant-owned assets set, each pack written under the tenant key while the key's state is `active` and recorded with its key scope, deletes a derived runtime's uploads with the runtime and an upload no version of the runtime references after 7 days, and keeps a legal hold above each of these deletions. "Platform chunk sets" states that the set of a `gitRepository` asset a `platform-admin` writes on a tenant-derived runtime is the tenant's, under the tenant key. The upload's single copy and its garbage collection were choices of that design's revision 73 for the owner's review, and the owner confirmed them on 2026-11-14 (revision 9). The tenant ownership of that set was one too, and the owner confirmed it on 2026-11-14 (revision 8).
- Revision 8, 2026-11-14. Records the owner's confirmation on 2026-11-14, under decision 43 of `tenant-derived-runtimes-design.md`, that the platform chunk set fed by the store of a `gitRepository` asset a `platform-admin` writes on a tenant-derived runtime is the tenant's and is stored under the tenant key, in "Platform chunk sets" under "TSS-8" and in revision 7's note. It changes no rule. The upload's single copy and its garbage collection remained choices of that design's revision 73 for the owner's review until revision 9. The status line's reads-against list names the current revision of each cited design.
- Revision 9, 2026-11-14. Records the owner's confirmation on 2026-11-14, in the words "Ok on the rest" and under decision 44 of `tenant-derived-runtimes-design.md`, of the choices of that design's revision 73 that TSS-8's **Tenant shared assets.** carries: an uploaded artifact is stored once, as packs of the runtime's tenant-owned assets set; it is deleted with its derived runtime, with the tenant, and after 7 days when no version of the runtime in use references it; and the tenant's stored upload bytes are bounded by the grant's per-tenant `maxAssetUploadBytes`, with no deployment ceiling. **Tenant shared assets.** and the notes of revisions 7 and 8 record it. It changes no rule. The status line's reads-against list names the current revision of each cited design.
