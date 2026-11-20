# Decisions: 0150 Tenant security settings

## Open decisions

The M3 readiness review of 2026-11-19 raised O14 to O19. O1 to O13 are settled and sit under "Decisions", in "Settled open decisions O1 to O13".

**O14. What a pending relaxation holds, and how it composes with later writes. Open.**

A settings version is "one stored state of a tenant's settings" ("Terms" in `change.md`), the settings `PUT` replaces the whole object ("TSS-3"), and a mixed write "holds its relaxations as a pending version, as SO-2's mixed write does" ("TSS-1"). Neither this change nor SO-2 of [`0142-security-oversight`](../0142-security-oversight/change.md) states whether a pending version is a full snapshot or only the relaxed members, whether a second relaxation may be written while one is pending, what a later write does to a pending version, or what `If-Match` and `dryRun` compare against while one is pending. On the snapshot reading, a relaxation of `retention.sessionArtifactHours` pending as version 8, followed by a tightening of `encryption.tenantKey` to `required` active as version 9, activates `tenantKey: none` at version 8's `effectiveAt` with no justification, no `tenant.security_settings_relaxation_requested` event, and no delay, which breaks TSSR-4 and TSSP-3. The same applier activates the tenant security record of 0142 and, under D18 of [`0136-placement`](../0136-placement/change.md), an environment's pending `placementRequirements`.

- (a) A pending version holds only the relaxed members and their target values, and the applier writes them onto the active version at `effectiveAt` as a new version. A later write that changes a pending member, in either direction, removes that member from the pending version, and a pending version left empty is cancelled. A tenant has at most one pending version, and a new relaxation replaces it with an `effectiveAt` computed from the new write. `If-Match` compares the active version, and a `dryRun` reports the pending version beside its result.
- (b) The rule that [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md) settles for a pending identity source ("Identity source record (TS-6)"): a further relaxation is refused while one is pending, a write that tightens any member cancels the pending version, its proposer submits it again with a new delay, and the pending version records a digest of the state it was written against, which must still match at `effectiveAt`.
- (c) Every write is refused while a relaxation is pending, except its cancel.

**Recommendation: (a)**, as 0142 (its decision 40) and 0136 (its O13) recommend for their records, with the rule stated once in SO-2 and cited here and in PM-4. It keeps a tightening effective at once, and a tightening of one member does not restart the delay of an unrelated relaxation. (b) gives the program one rule for every pending security change, the rule 0149 already settles, at the cost that any tightening restarts the delay of every pending relaxation. (c) lets a pending relaxation block an urgent tightening for the whole delay. Whichever option is chosen, 0142 SO-2 and 0136 PM-4 state it in the same terms. It blocks 0150.S4, the `load_local` test of a relaxation activating while a tightening is written, and the applier of 0142.S2. The other changes are [`0142-security-oversight`](../0142-security-oversight/decisions.md) (decision 40) and [`0136-placement`](../0136-placement/decisions.md) (O13). (raised by the M3 readiness review, 2026-11-19; sources: 0150-F1, tenancy-security-C4)

**O15. How Secret data meets the class's encryption row. Open.**

"TSS-8" states that credentials, tokens, and leases meet the Secret class's encryption row on every tenant, because §12.9 says the credential encryption of §4.3 and §4.9 satisfies it, and TSSP-6 rests on that statement. §4.9 accepts an API server `EncryptionConfiguration` with `aescbc`, `aesgcm`, or `kms` for credential Secrets, and the GKE default encryption, and only `kms` is KMS envelope encryption. BUILD-GAPS.md records the contradiction as F-12.9.24, open. A deployment that follows §4.9 with `aescbc` stores Secret data without the envelope encryption TSSP-6 states.

- (a) Require the `kms` provider for the Kubernetes Secrets that hold Secret-class data, and keep TSSP-6 as written. The preflight check of §4.9 can only warn today, because verifying the provider needs etcd access the preflight Job may not have.
- (b) State in "TSS-8" which §4.9 configurations satisfy the Secret row, and narrow TSSP-6 to them.

**Recommendation: (a)**, because the class exists to guarantee envelope encryption for credentials, and the tree already carries KMS clients. Whichever is chosen, 0150.S1 lists F-12.9.24 under `closes`. It blocks 0150.S1's statement of the Secret encryption row and TSSP-6. (raised by the M3 readiness review, 2026-11-19; sources: 0150-F4)

**O16. Where the specification text of the settings model lands. Open.**

"TSS-9" places the settings model in a new numbered section, §12.10 Tenant Security Settings, and "Specification changes" adds it to the README. Under the specification structure of change 0115, the numbered files hold the content of pages not yet written, and the placeholder pages `spec/capabilities/tenancy-and-access/tenant-settings.md` (code `TSET`) and `spec/cross-cutting/data-classification-and-retention.md` (code `DATACLASS`) already list this change's parts under "Content today". A new numbered section is on neither list, so the area migration would not find it, and the requirement labels TSSR-n have no page code under `spec/conventions.md`. The "§17.10 Placement (new)" of `0136-placement` raises the same question.

- (a) Add §12.10 as a numbered section, and add it to the "Content today" lists of `TSET` and `DATACLASS` in the same step.
- (b) The specification lane of 0150.S2 writes the `TSET` page with page-coded requirements, in or ahead of the tenancy-and-access area change, and adds no numbered section.

**Recommendation: (b)** when the tenancy-and-access area change is scheduled before 0150.S2, and (a) otherwise. It blocks the specification lanes of 0150.S2, 0150.S3, and 0150.S4, and the successor pointers that name "§12.10". The other change is [`0136-placement`](../0136-placement/decisions.md), for its §17.10; the program's open question of who owns the specification lane, the area changes or the member steps, bears on both. (raised by the M3 readiness review, 2026-11-19; sources: 0150-F6)

**O17. Where the removal of the T4 placement mechanism lands. Open.**

0136.S7 removes the `lenny-t4-node-isolation` webhook with its alert, its preflight check, and its `features.compliance` gate, and the pod builder's `applyT4NodeIsolation`, and its specification lane turns §6.4's **T4 dedicated-node requirement** into a successor pointer. 0150.S2 stated that the T4 webhook keeps reading the Runtime's field until 0150.S3, and 0150.S3 stated that it lands together with that removal, while 0150.S3 depends on 0136.S4 and 0136.S6, which 0136.S7 does not wait for. This revision drops those two statements and leaves the step to this decision.

- (a) Keep the removal in 0136.S7. Between 0136.S7 and 0150.S3, a pool whose runtime has `workspaceTier: T4` runs on its profile's class with shared tenancy, on unreleased `main` only.
- (b) Move the removal into a step of 0136 that shares a branch with 0150.S3, so the T4 selectors and tolerations coexist with the classes from 0136.S3 to 0136.S6.

**Recommendation: (a)**, as 0136 recommends. It matches D3 and inventory row I22 of this change, which leave the removal to the placement design, and the program's rule against transition modes. It blocks 0150.S3 and 0136.S7. The other change is [`0136-placement`](../0136-placement/decisions.md) (O9). (raised by the M3 readiness review, 2026-11-19; sources: 0150-F7, the T4 webhook part)

**O18. Details of the backup bound, the template listing, the key after `none`, and `dryRun`. Open.**

1. **The gateway's source for the backup settings.** "TSS-5" compares the tenants' `erasure.userDeadlineHours` with `backups.retention.retainDays` while `backups.erasureReconciler.enabled` is `false`. The settings write runs in the gateway, both values are `lenny-ops` Helm values, and `PUT /v1/admin/backups/policy` (§25.11) changes `retainDays` at run time. Options: (a) the gateway reads the retention in effect and the reconciler setting from the store in which `lenny-ops` keeps the backup policy; (b) a gateway flag rendered from the same Helm values, which the policy route would make stale; (c) only `lenny-ops` checks the bound, refusing a policy write and reporting a tenant whose new deadline the retention exceeds, and the settings write is not refused. Recommendation: (a), because the policy route changes the value at run time.
2. **Which templates a tenant role lists.** `GET /v1/admin/security-templates` lists every template, operator-defined ones included, to the `tenant-admin`, `security-auditor`, and `security-admin` of every tenant, so an operator template named for a customer, such as `acme-baseline`, discloses that name to other tenants. Options: (a) a tenant role lists the built-in templates and the template its own tenant last applied; (b) every template stays visible to every tenant role, and the documentation states that template names are visible to all tenants. Recommendation: (a), because it discloses nothing about another tenant.
3. **The tenant key after the move to `none`.** Phase 4a runs only when the state is `active` or `retained` (TSSR-10), so a key that reached `none` stays at the provider, with the platform's grants, after the tenant is deleted. Options: (a) the tenant record keeps a flag that the tenant has had a key, and Phase 4a schedules deletion of the key whenever the flag is set; (b) the operator, who provisioned the key under O8 (a), deletes it, and the documentation and the read route say so. Recommendation: (a), because it leaves no platform grant on the key of a deleted tenant.
4. **`dryRun` of a write that sets `tenantKey: required`.** §15.1 states per endpoint whether a `dryRun` makes an outbound call, and the connector `dryRun` makes none. Options: (a) the `dryRun` runs the probe and commits nothing; (b) the `dryRun` skips the probe and reports that it did not run. Recommendation: (a), because the probe is the validation the write depends on, and it changes nothing at the provider.

Item 1 blocks the backup bound of 0150.S2, and items 2 to 4 block the routes, Phase 4a, and the `contract` tests of 0150.S2 that they name. (raised by the M3 readiness review, 2026-11-19; sources: 0150-F3, 0150-F12, 0150-F18, 0150-F20)

**O19. One residency region per deployment against per-region storage routing. Open.**

D24 and PM-7 of [`0136-placement`](../0136-placement/change.md) state that a deployment serves one residency region and that every component of it runs there. [§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces) **Data residency** item 2 keeps the `StorageRouter`, which routes each residency tenant's writes to per-region backends from the `storage.regions` map, with per-region KMS. This change relies on the per-region model in two places: "TSS-1" (**Combination with other tenant fields**) places the tenant key at `storage.regions.<region>.kmsEndpoint` and refuses a write with `KMS_REGION_UNRESOLVABLE` when the endpoint is missing (I10), and "TSS-8" extends the residency webhook's region match to every tenant with `dataResidencyRegion` set (I34).

- (a) Per-region storage routing stays, as 0136 O10 (a) states, and PM-7 is narrowed to the data stored through the Kubernetes API. The tenant key's region-local endpoint, `KMS_REGION_UNRESOLVABLE`, and the universal region match of I34 stand as written.
- (b) Per-region storage routing is retired in favour of one deployment per region, as 0136 O10 (b) states. The tenant key uses the deployment's one KMS endpoint, `KMS_REGION_UNRESOLVABLE` leaves the tenant key path, and I34's region match reduces to a check that the tenant's region is the deployment's.

**Recommendation: (a)**, following 0136's recommendation for v1, because (b) is a removal that no member change scopes. It blocks 0150.S1's universal region match (I34) and the region-local key path of 0150.S2. The other changes are [`0136-placement`](../0136-placement/decisions.md) (O10) and [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/decisions.md) (O13). (raised by the M3 readiness review, 2026-11-19; requested by change 0136; mirrors 0136 O10)

## Decisions

| # | Question | Owner's answer | Applied in |
| :-- | :-- | :-- | :-- |
| D1 | What replaces `workspaceTier: T4` (2026-11-12). | T4 is retired as a concept. Each control it bundles becomes its own explicit setting, set by the operator through the admin API on the tenant, or on another object where the control belongs there, with the reason stated. | TSS-1, TSS-9 |
| D2 | How operators get a correct bundle (2026-11-12). | Lenny ships configuration templates, named bundles of settings for levels of tenant security, such as `standard`, `confidential`, and `regulated`. | TSS-2 |
| D3 | Placement ownership (2026-11-12). | Node tenancy, minimum isolation, and pod sharing and recycling reference the placement design, which owns node classes, node tenancy, placement offerings, and placement requirements as filters. This design sets the tenant-level values. | TSS-6 |
| D4 | Migration (2026-11-12). | There are no deployments, so the change is a straight replacement with no compatibility path. `workspaceTier` and every T4 rule leave the specification, with successor pointers where content moves. | TSS-9 |
| D5 | The name of the top data class (2026-11-12, N1). | "Rename the top data class from 'Restricted' to 'Secret' (Public, Internal, Confidential, Secret)." | TSS-8, "Specification changes" |
| D6 | `complianceProfile` (2026-11-12, O9). | Option (b), as a follow-up design: "`complianceProfile` becomes deployment-level Helm settings plus per-tenant settings, with soc2, fedramp, and hipaa as templates and the ratchet replaced by the weakening delay. That design is written later." This design leaves `complianceProfile` unchanged and points to `compliance-profile-design.md` (pending). | O9, TSS-1 |
| D7 | The other open decisions (2026-11-12). | O1 (a), O2 (a) ("keep the taxonomy", after the owner asked for examples), O3 (a), O4 (a), O5 (b) ("ok", as recommended), O6 (a), O7 (a), O8 (a), O10 (a), O11 (a), O12 (a), and O13 (a). | "Settled open decisions O1 to O13" |
| D8 | Platform chunk sets (2026-11-13), from decision D1 of `0131-incremental-checkpoint`. | A tenant setting turns them off, `false` in `regulated` and `true` in `standard` and `confidential` ("2. ok"), and a tenant with its own key may reference operator-source chunks stored under the deployment key ("1. ok"). | TSS-1, TSS-2, TSS-4, TSS-7 |
| D9 | Relaxing an environment's placement requirements (2026-11-13). | "5 ok": removing an environment's `placementRequirements`, or moving one to a weaker value, is a relaxation under this design's rules, with a `justification` and the weakening delay, as [PM-4](../0136-placement/change.md#pm-4-placement-requirements) states under its D18. | TSS-6, TSS-3 error table |
| D10 | `allowReusedPod` set by the tenant's security admin (2026-11-13). | "15 ok": a tenant's `security-admin` sets `allowReusedPod: false` as the tenant's own requirement in the tenant security record, beside the operator's `placement.allowReusedPod` of this design. | TSS-6 |
| D11 | One rule for removed fields (2026-11-13). | "6 ok": a write that names a removed field is refused with `400 VALIDATION_ERROR` naming the field, as D6 of [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/decisions.md#decisions) records. | TSS-3, TSS-9 |

### Settled open decisions O1 to O13

The owner answered O1 to O13 on 2026-11-12 (D6 and D7). Each entry keeps its options and its recommendation as a record and states the answer. The M3 readiness review of 2026-11-19 moved them here from "Open decisions", where the board counted their option bullets as open decisions.

**O1. How a template reaches a tenant.**

- (a) Copy on apply. Applying writes the template version's values into the tenant and records the name and version. A later template version changes nothing until an operator applies it.
- (b) Reference with overrides. The tenant names a template and stores only overrides, and a new template version propagates to every tenant that names it.

**Recommendation: (a).** Under (b), a template edit can weaken every tenant that names it with no per-tenant action, no justification, and no per-tenant audit record. Under (a), each tenant's values change only through a recorded action on that tenant. The read route reports `newerVersionAvailable` and `differsFromTemplate` ("TSS-3"), so an operator finds tenants to re-apply, and the tenant list filters on them.

**Settled (2026-11-12): (a)** (D7).

**O2. The data-class taxonomy.**

- (a) Keep §12.9's classes as an internal taxonomy of kinds of data, named rather than labelled T1 to T4, with no tenant input.
- (b) Remove the taxonomy and state each rule on the data it governs.

**Recommendation: (a).** The classes carry rules that are not tenant choices: credentials and OAuth tokens are always in the top class and envelope-encrypted, user identifiers are Confidential, and audit events of the two upper classes are written synchronously ([§12.3](../../spec/12_storage-architecture.md#123-postgres-ha-requirements)). The labels in §12.9 are Public, Internal, Confidential, and Restricted **[source]**. Names avoid confusion with the retired setting. "TSS-8" lists where the taxonomy stays in force.

**Settled (2026-11-12): (a)** (D7). The owner asked for examples and then answered "keep the taxonomy". The owner's N1 of the same day names the top class Secret (D5), so the classes are Public, Internal, Confidential, and Secret.

**O3. Who sets the settings.**

- (a) Only `platform-admin` writes them. `tenant-admin`, `security-auditor`, and `security-admin` read them. The tenant's security team narrows placement further through requirements in the tenant security record, which are filters only.
- (b) The tenant's `security-admin` may also tighten a setting, and only `platform-admin` may relax one.

**Recommendation: (a).** Tightening `encryption.tenantKey` needs a key the operator provisions (O8), and dedicated node tenancy needs single-tenant pools and nodes the operator provides ([PM-2](../0136-placement/change.md#pm-2-node-tenancy)). A tenant that wants a stricter placement than its settings states it as a requirement, which the placement design already admits from a tenant.

**Settled (2026-11-12): (a)** (D7).

**O4. What replaces `workspaceTier` on the Runtime.**

- (a) Nothing new. The runtime keeps its `isolationProfile`, which [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) makes a live floor. Every data-sensitivity control is a tenant setting.
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

- (a) Remove the environment-level override. Environments inherit the tenant's settings, and a stricter placement for one environment is a requirement on that environment ([PM-4](../0136-placement/change.md#pm-4-placement-requirements)).
- (b) Keep an environment-level copy of each setting that may only tighten the tenant's value.

**Recommendation: (a).** Today only `workspaceTier` has a stricter-only environment override **[source]**. Under (b), every setting reader resolves two levels, and the key, deadline, and retention controls are per tenant in practice: there is one tenant key, one erasure job per user, and one deletion lifecycle.

**Settled (2026-11-12): (a)** (D7).

**O7. What `placement.nodeTenancy: dedicated` means.**

- (a) Nodes that carry pods of this tenant alone, as [PM-2](../0136-placement/change.md#pm-2-node-tenancy) defines dedicated tenancy.
- (b) Nodes shared by every tenant whose setting is `dedicated`, as T4 nodes are shared by every T4 tenant today ([`0127-egress-and-git`](../0127-egress-and-git/change.md) states that "T4 nodes host several T4 tenants") **[source]**.

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

**Recommendation: (a).** Most of its controls are deployment-level gates (SIEM, pgaudit, and retention floors checked at gateway start) rather than per-tenant behaviour, and splitting them changes [§11.7](../../spec/11_policy-and-controls.md#117-audit-logging) and [§16.4](../../spec/16_observability.md#164-logging) broadly. The owner's direction applies to it in spirit, so the overlap is recorded here for a decision.

**Settled (2026-11-12): (b), as a follow-up design** (D6). After asking for elaboration, the owner chose to split `complianceProfile`: it becomes deployment-level Helm settings plus per-tenant settings, with `soc2`, `fedramp`, and `hipaa` as templates, and the weakening delay replaces the one-way ratchet and its attested decommission route. A later design, `compliance-profile-design.md` (pending), states it. Until that design lands, this design leaves `complianceProfile`, its ratchet, its decommission route, and [§11.7](../../spec/11_policy-and-controls.md#117-audit-logging) unchanged, no setting depends on it, and no template sets it.

**O10. Operator-defined templates.**

- (a) Built-in templates plus operator-defined templates in Helm values under `tenantSecurity.templates`, each with an explicit version. A changed definition under an existing version stops the gateway at start.
- (b) Built-in templates only.
- (c) Templates created through the admin API.

**Recommendation: (a).** Templates are deployment configuration, which the bootstrap plane holds in Helm values ([§15.1](../../spec/15_external-api-surface.md#151-rest-api), **Bootstrap plane — Helm only:**). An operator whose policy differs from the built-ins writes it once. (c) adds a mutable resource whose edits need the same versioning the Helm path gets from the stored version table ("TSS-2").

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
