# Review history: 0148-tenant-outbound-connector Tenant outbound connector

This section records what each revision changed and the reviews of earlier revisions. The current text above supersedes any statement here.

# Revision notes

The notes for revisions 4 to 23 were carried in the status line of those revisions. The notes for revision 24 and later were written here.

- Revision 4 added an OAuth link to a private host, and revisions 5 to 11 align it after review.
- Revisions 12 to 17 change only the pointers to the revisions of the companion designs that this design reads against.
- Revision 18 records the owner's decision on the external review.
- Revision 19 records decisions 1, 2, 3, 5, 7, 8, 9, 10, and 12 as settled.
- Revision 20 records decisions 4 and 6 and the Envoy spike.
- Revision 21 records decision 13.
- Revision 22 qualifies the HTTP-mode deny. Revision 22 followed independent security and consistency reviews whose final stop check found no Major or higher issue. That check traced the last round's changes and was not an end-to-end read.
- Revision 23 changes only pointers after the user credential documents split.
- Revision 24 followed a pass for a reader new to the project. It added "Terms from other designs and the platform", "Labels from other designs", and "High-level design", added terms of this design, the tag table, and the evidence sources, split "replica session" from a Lenny session and the agent from a session's agent, named the setting and the object behind each configuration value, linked every citation of another design and every label from one, called the egress design's interfaces by name, and moved the review records and the test of the egress design's claims to the end. A reader review then added terms for the roles, pools, routes, stores, opaque tunnels, and channel identifiers, and qualified the senses of pin, grant, and gateway. It changed no rule.
- Revision 25 corrected the "Tenant-owned pool" term, which left the pool's profile undefined and implied that the tenant sets it, to name the client proxy profile that the platform admin's per-pool override sets on a pool whose `ownerTenantId` names the tenant, that a validation pass found, and changed no rule.
- Revision 26 applied two owner decisions of 2026-11-08. The reserved set names the outbound gateway's public hosts, `outboundGateway.publicHost` and each OC-4 group's public host, where it said "the gateway's own hosts", and the `outboundGateway.reservedHosts` row lists them too. `tenantOverride.outboundHosts` takes `deny`, the default, or `allow`, where only `deny` was named.
- Revision 27 updated the "Covered host" entry in "Terms from other designs and the platform" to the egress design's definition, which limits a git route to the tenant's own and includes a provider's alias hosts, linked it to "Git on covered hosts", and corrected the introduction to "Revision notes", which said that every note but the last came from a status line, after a re-read of `0127-egress-and-git`, `0151-user-credential-delivery`, `0152-user-git-identity`, and `0129-github-credential-addon` at their current revisions, and changed no rule.
- Revision 28, dated 2026-11-09, applies the design sync review of 2026-11-08, and changes no rule. "Changes requested of 0127-egress-and-git" records the request that a ceiling of `*` match outbound connector hosts, which the egress design applied in its revision 73, and gains the requests that sat only in prose: the ref resolver's mapping of a certificate failure after 200, and the egress PKI leaves for the outbound gateway and PgBouncer. The TS-1 dependency row becomes a statement that TS-1 does not scope outbound connectors or hosts, under `0149-tenant-scoping-identity` decision 4. The FE-1 label says that FE-1 sets the `quarantined` state that EG-2 adds. The statements about `lenny-ops` say that SO-7 removes only its direct Secret read. "Failure mapping" gains a row for a suspended outbound connector or host. The mint walkthrough names the mint checks in place of "both gates", the scenario states that the pool's `tenantOverride.outboundHosts` is `allow`, and the requests introduction no longer ends on a fragment. "Settled decisions" drops the retired decision numbers, which were 1 for build or adopt, 2 for isolation on shared replicas, 3 for authorization at the agent, 4 for TLS to the outbound gateway, 5 for agent identity, 6 for the attachment register, 7 for the mirror, 8 for the pool ceiling, 9 for anonymous hosts on a private CA, 10 for the namespace, 11 for the external review, 12 for grant signing key storage, and 13 for grant-to-instance binding.
- Revision 29, dated 2026-11-13, applies the reviewer instructions of `0136-placement` revision 2 and `0150-tenant-security-settings` revision 1, from the owner's placement and data-class decisions of 2026-11-12, and changes no rule. "Deployment and network rules" points to "PM-8" for where the outbound gateway's pods run, in place of the placement rule that kept it off nodes tainted for agent isolation, and "High availability" points to "PM-8" for its replicas, PodDisruptionBudget, and anti-affinity, whose `global.devMode` relaxation the component table does not carry. The namespace stays with this design. The §12.9 item names the data classes Public, Internal, Confidential, and Secret (TSS I54).
- Revision 30, dated 2026-11-13, applies the owner's decisions of 2026-11-13 that reach this design. Under the removal of non-agent runtimes (`0119-agent-runtimes-only`, settled by the owner on 2026-11-13 in the words "O5 (b), yes to all the others"), the egress design's proposal for a service-mode tenant pin is withdrawn, so its label leaves "Labels from other designs", and the platform scope of `REG-SITEATTACH` is justified by its own signed, tenant-named rows rather than by that pin record. "Scope" answers that design's open question: an outbound connector carries only traffic through `lenny-egress`, so the API gateway's calls to a registered MCP connector are outside it. The naming paragraph notes that the runtime protocol design replaces `CH-MCP-CONNECTOR` with tool frames, under that design's reviewer instruction. "Specification sections affected" assigns each new record a data class of `0150-tenant-security-settings` TSS-8 (I54). "Documentation" states every setting, default, limit, requirement, failure mode, and runtime-facing rule, under IC D13 (owner decision of 2026-11-13). The final round of the cross-design review of 2026-11-13, which changes no rule, reads against `0127-egress-and-git` revision 77 and `0151-user-credential-delivery` revision 29. The read-only checks of the same review change no rule in this design. The status line's reads-against list names the current revision of each cited design.
  - The last round of the same review, which changes no rule, cites the designs this one reads against at their current revisions.
- Revision 31, dated 2026-11-13, records under "Settled decisions" the owner's confirmation of 2026-11-13, in the answer "18–21 ok" to the consolidated list of open decisions, of the data classes that revision 30 assigned to the new records. It changes no rule. The status line's reads-against list names the current revision of each cited design.
- Revision 32, dated 2026-11-15, applies the owner's decision of that day to remove connectors, in the owner's words "Yes, let's drop connectors.", with the owner's reasoning, "An operator could build connectors outside of Lenny and just use egress to talk to its own 'connector' service.", which [`0124-connector-removal`](../0124-connector-removal/change.md) revision 2 designs. It changes no rule of the outbound connector. "Scope" states that Lenny calls no MCP server for a session and that an operator service on a customer's private network is reached over an outbound connector route. The naming paragraph under "Terms" records the §9.3 binding as the reason the identifiers were chosen, and drops the `connector:<id>` tool server, which the removal design withdraws. The §9.3 item of "Specification sections affected" stages nothing, the documentation row names the page that replaces `using-connectors.md`, the runtime-facing rule names operator services, and the naming decision under "Settled decisions" gains a note. The status line reads against the removal design's revision 2, `0127-egress-and-git` revision 87, `0151-user-credential-delivery` revision 36, `0152-user-git-identity` revision 41, and `0139-runtime-protocol` revision 11.


# Review of revision 19

An independent review read the namespace move, the secret-less binding, and the in-memory keys.

| Finding | Verdict | Resolution |
| :-- | :-- | :-- |
| M1, a replica has no credential `lenny-evidence` accepts under a separate CA, and adding that CA would let its holder forge other producers | Holds. | The `outbound` CA is removed. A copied egress PKI leaf keeps ingest's SAN allowlist as it is. |
| M2, nothing delivers the `outbound` CA to Envoy, and the only carrier would make every component trust it | Holds. | Resolved by the same removal, so the existing statements about an egress PKI leaf stand. The reviewer's `ClusterIssuer` option was not taken, because it moves the CA key and needs a limit on who can request leaves. |
| Minors on the namespace | Hold. | `enforce: restricted`, the value and its guard, NET-047's text, the address check's namespace, the database login in C5, and TLS on the pooler hop. |
| Minors on `kind: none` | Hold. | `inject` alone, refusal on covered and pinned hosts, the route check at write and at bind, the EG-4 dependency, and tests. |
| Minors on keys | Hold. | The revoke-all bound's condition, early reporting of a failed fetch, and a rotation retry that is no reuse. |

A stop check on the result found no Critical, High, or Major issue. Its Minors are applied: the controller's rights, the copy's delivery check and its expiry series, a level 2 proxy's ingress row, the rotation retry's exact condition, the detection bound's condition, every pin source against `kind: none`, the fetch state as an attestation trigger, `verify-full` on the pooler hop, the namespace value and guard, and what a shared replica reads. These last fixes have not been re-read.

# Review of revision 9

A final regression review raised N1 on this design's walkthrough: with the git route `direct` and an egress route to the connector, a flagged session's git request asserted the platform default. It holds, and the egress design's fall-through between route lists makes this topology work, with a test naming it. None was rejected.

# Review of revision 8

Round 5 of the reviews raised Sync M1, LFS on a connector route against the egress design's refusal, and m15, a held token on a covered host. Both hold. OR-3, the tier 4 row, and the residual are limited to what the egress design allows. None was rejected.

# Review of revision 7

Round 4 of the reviews raised m21: a request here was already applied. It holds, and the row says so. None was rejected.

# Review of revision 6

Round 3 of the reviews raised R3-4 on this design's text: the name is resolved on the customer's side, behind an unchanged outbound host. It holds, and the scenario states the limit. None was rejected.

# Review of revision 5

Round 2 of the reviews raised these on this document, each verified against the text. H1: a route plus a CA behind an unchanged name read tokens, and C8's reserved set does not cover GHES, self-managed GitLab, or a `kind: oauth2` endpoint. It holds, and the token trust record covers the route target and CA digest. N30: this document called a move a token-bearing edit that the other designs did not list. It holds, and the same record states it. None was rejected.

## Move into changes (2026-11-16)

Change 0113 moved this design from `designs/tenant-outbound-connector-design.md` into this folder. Its review history moved into this file, its proposal plan became "Steps", its evidence moved into `evidence/`, its status became `Draft` pending the owner's final cross-review, and every path to a design or its evidence, in links and in prose, now names its new location.

## Status line before the split (2026-11-16)

Change 0113 shortened the status line of `change.md` to the status, revision, date, and program, and moved the rest here as it stood:

> after two independent reviews of earlier revisions. Revision 32 applies the owner's decision of 2026-11-15 to remove connectors, in the owner's words "Yes, let's drop connectors.", which [`0124-connector-removal`](../0124-connector-removal/change.md) revision 2 designs: §9.3's MCP connectors leave Lenny, an MCP server on a customer's private network is an operator service that a runtime reaches over an outbound connector route, and this design's outbound connector keeps its name. Revision 31 records the owner's confirmation of 2026-11-13 of the data classes of the new records. Nothing here is built or staged, and no production component or live provider write has run. It reads against [`0124-connector-removal`](../0124-connector-removal/change.md) revision 7, [`0127-egress-and-git`](../0127-egress-and-git/change.md) revision 92, [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) revision 40, [`0152-user-git-identity`](../0152-user-git-identity/change.md) revision 45, [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/change.md) revision 7, [`0139-runtime-protocol`](../0139-runtime-protocol/change.md) revision 12, [`0129-github-credential-addon`](../0129-github-credential-addon/change.md) revision 7, [`0136-placement`](../0136-placement/change.md) revision 11, and [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) revision 9. The owner has settled every decision, and "Settled decisions" records each one. Only the Envoy spike under "Spikes owed" has run, so every other behaviour below is a design statement, and "Spikes owed" lists what must be measured first. Statements about Lenny were checked against the tree and the specification on 2026-11-05, and statements about grpc-go against the v1.80.0 source `go.mod` names. Proposals carry the label OC-n, requirements OR-n, and security properties C-n. "Review history" at the end records how the design reached this form. The owner's final cross-review of the moved designs is pending, so the status is `Draft`.

## M3 readiness review (2026-11-19)

Revision 33, 2026-11-19. Trigger: review. The M3 readiness review read revision 32 against `main` at `092ef92f6`, with the consistency reports of the program's clusters. The review raised no High finding, so no skeptic's verdict applies. Counts: 18 applied, 8 raised as open decisions, 0 returned as issues, and 0 dropped. The parts of 0148-F1 and 0148-F4 that need no decision are also applied, and those two findings count as raised. Edits that belong to other changes are returned to the review rather than made here.

Applied:

- 0148-F1, in part: "Store access" adds `outbound_grant_key_revocation` to the replica's read set, which "Emergency rotation" already requires. The rest of the finding is O1.
- 0148-F2: "How a CONNECT reaches the right connector" justifies the platform scope of `REG-SITEATTACH` by the decision service's cross-tenant poll and gains "Tenancy of the new tables", which classifies each new table against §4.2's row-level security rules and states the application-layer filter on the API path. The `integration` tier gains a cross-tenant read test.
- 0148-F3: the agent API routes record nonces in a store every API gateway replica shares, with an insert-if-absent write keyed on instance and nonce, and the `security` tier gains a replay test against a second API gateway replica.
- 0148-F4, in part: the "Availability" residual risk states the decision service's verification load. The bound is O2.
- 0148-F5: 0148.S2 depends on `0127.S12 (part)`, with the pinned-host refusal named as the waiting part, and "Private CAs" and the request row to 0127 name EG-12 for that half.
- 0148-F6, in part: the outbound grant with its route, the named permissions, the connector and instance count quotas move from 0148.S2 and 0148.S3 into 0148.S1. The store of the outbound grant is O3.
- 0148-F9, in part: the `security` tier gains a test that a tenant assertion for an outbound host from any identity other than the API gateway, the mirror, or the Token Service is refused. The wording in 0127.S2 is returned as an edit to 0127.
- 0148-F10 and connectors-llm-billing-C3: the reserved set and the `outboundGateway.reservedHosts` row name the endpoint hosts of LLM upstream records (LUR-11 of 0133) in place of pool-named LLM hosts, and the `security` tier's reserved-host test gains that case.
- 0148-F11: "Specification sections affected" lists the target pages (OUTCONN, OUTGW, OUTAGT, the admin API and CLI pages, the cross-cutting pages, and the catalogs), states that OR-n and C-n become requirement identifiers in `spec/registry.json`, and replaces the claim-register row with issue filing under §28.4 Implementation status. The tests and the "Test tiers" term use the tier names of change 0155, with the Envoy and level 2 suites in the `conformance` tier where the egress design places them. `steps.md` drops the change-proposal pipeline. The probe row drops the comparison with the route 0124 removes. The evidence paths in "Evidence sources" and "Spikes owed" name `evidence/spike-oc4/` and `../0127-egress-and-git/evidence/spike-envoy-r4/`.
- 0148-L1: the citations of decisions 2, 3, 12, and 13 in `change.md` name the settled decisions by title, because `decisions.md` does not number them.
- 0148-L2: reaping raises `outbound_connector.instance_reaped`, a newly reserved overlap raises `outbound_host.reserved_overlap`, the revoke routes write the audit events `outbound_grant_key.revoked` and `outbound_grant_key.revoked_all`, and the liveness table gains a `suspended` state for a connector an SO-2 guardrail suspends.
- 0148-L3: the agent's replay cache is sized from the connector's new-stream quota, which the trust response carries, and a refusal at capacity maps to `grant_refused_by_agent` with the class `replay_cache_full`, in "Failure mapping" and the `unit` tier.
- 0148-L5: "Tenancy of the new tables" states the application-layer filter on the connector read, and the `security` tier gains a cross-tenant liveness read test.
- 0148-L6: the clone scenario names the materialization helper `lenny-materialize` of 0153 as the cloner.
- 0148-L7, in part: "Notes on the steps" states that whichever of 0148.S1 and 0136.S2 lands second renders the outbound gateway's placement rows. The PM-8 row for OC-4 Deployments is returned as an edit to 0136.
- 0148-L8: the anti-affinity namespace selector names the mirror's namespace, `lenny-system` or `lenny-mirror` under PM-8.
- 0148-L9, in part: "Notes on the steps" states that connector registration (0148.S1) and outbound host registration (0148.S2) each pass the `tenantRegisteredServices` guardrail check, landing with whichever of 0142.S2 and that step is second. The OC labels in 0142.S2 are returned as an edit to 0142.
- 0148-L11: 0148.S3 states that its scenario tests land in 0148.S3, because 0127.S1, 0127.S3, and 0127.S4 land first.
- 0148-L12: the grant key revoke and revoke-all routes move from 0148.S3 into 0148.S1, beside the signer.
- 0148-L15: "Notes on the steps" names the API front, `pkg/gateway/api/admin`, for the admin routes, the handler-authenticated registration for the agent routes, and `pkg/gateway/core/coordination` for the sweeper duties, and states under decision 8 of 0117 that the steps change no gateway seam operation.

Raised as open decisions:

- O1, from 0148-F1: how a replica learns of connector revocation, outbound grant removal, tenant suspension and deletion, and group assignment changes, and where group assignments are stored.
- O2, from 0148-F4: the bound on the decision service's work for junk attestations.
- O3, from 0148-F6: where the outbound grant is stored, with 0142.
- O4, from 0148-F7: where the spikes that gate the build decision run.
- O5, from 0148-F8: whether 0148.S1 is split.
- O6, from 0148-L4: which boot of an instance the decision keeps.
- O7, from 0148-L10 and connectors-llm-billing-C14: how connector bytes are metered, with 0121.
- O8, from 0148-L13: the per-source handshake limit when the load balancer hides the source address.

Dropped: none. Three items have no edit in this change and are returned to the review as edits elsewhere: 0148-L14 (the `(part)` marks on 0151.S2's edges), connectors-llm-billing-C6 (the split of 0123.S2, which is 0123's open decision), and credentials-git-C12 (the edges of 0143.S2). The removals report reads the probe's comparison with the removed connector test route as needing no change; this entry drops the comparison under 0148-F11, because the route it names no longer exists once 0124 lands.

Second pass, 2026-11-19. The second pass closed the edits that other changes requested of this one. Counts: 1 applied, 1 already done, 1 already recorded as an open decision, 0 raised as new decisions, and 0 dropped.

- Applied, requested by 0150 (0150-F21): 0148.S1 and 0148.S2 depend on `0150.S1 (part)`, and each step names its §12.9 classification rows under the renamed data classes as the waiting part.
- Already done, requested by 0133 (connectors-llm-billing-C3, 0133-F15): revision 33 replaced the pool-named LLM hosts with the endpoint hosts of LLM upstream records in the reserved set and the `outboundGateway.reservedHosts` row, applies the reserved set at registration and at each decision, and adds the upstream endpoint case to the reserved-host test.
- Already an open decision, requested by 0121 (connectors-llm-billing-C14): O7 records whether connector bytes are a metric only or a billing event under 0121's rules for periodic events, with 0148.S3 depending on 0121.S2 under option (b).
