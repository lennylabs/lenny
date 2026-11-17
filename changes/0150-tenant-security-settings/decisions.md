# Decisions: 0150 Tenant security settings

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
| D8 | Platform chunk sets (2026-11-13), from decision D1 of `0131-incremental-checkpoint`. | A tenant setting turns them off, `false` in `regulated` and `true` in `standard` and `confidential` ("2. ok"), and a tenant with its own key may reference operator-source chunks stored under the deployment key ("1. ok"). | TSS-1, TSS-2, TSS-4, TSS-7 |
| D9 | Relaxing an environment's placement requirements (2026-11-13). | "5 ok": removing an environment's `placementRequirements`, or moving one to a weaker value, is a relaxation under this design's rules, with a `justification` and the weakening delay, as [PM-4](../0136-placement/change.md#pm-4-placement-requirements) states under its D18. | TSS-6, TSS-3 error table |
| D10 | `allowReusedPod` set by the tenant's security admin (2026-11-13). | "15 ok": a tenant's `security-admin` sets `allowReusedPod: false` as the tenant's own requirement in the tenant security record, beside the operator's `placement.allowReusedPod` of this design. | TSS-6 |
| D11 | One rule for removed fields (2026-11-13). | "6 ok": a write that names a removed field is refused with `400 VALIDATION_ERROR` naming the field, as D6 of [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/decisions.md#decisions) records. | TSS-3, TSS-9 |

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
