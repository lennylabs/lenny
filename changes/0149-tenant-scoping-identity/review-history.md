# Review history: 0149-tenant-scoping-identity Tenant scoping and identity

This section records what each revision changed and the reviews of earlier revisions. The current text above supersedes any statement here that differs from it. "Changes in other design documents" at its end records the edits revisions 13 to 18 made in other designs.

# Revision notes

These notes were carried in the status line of earlier revisions.

- Revision 5 added scoping of user credential types, and revisions 6 to 12 corrected it after review. The reviews of revisions up to 12 ended with a stop check that traced the last round's changes and found no Major or higher issue, and that check covers none of the identity design.
- Revision 13 applied the owner's decision 8, which adopts per-tenant OIDC and a SCIM server beside the broker default.
- Revision 15 applied decisions 14 and 26.
- Revision 16 applied a second review of the identity design, a first review of cross-environment sharing and delegation limits, and the owner's decision 3.
- Revision 17 applied the stop check on revision 16.
- Revision 18 applied the stop check on revision 17, under "Review of revision 17".
- Revision 19 recorded the owner's decisions 1, 4, 5, 6, 7, 10, 11, 12, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, and 25, added the warning decision 24 adopted, and added shared issuers under decision 18, with three reviews' findings applied under "Review of shared issuers". The last of them found no path across tenants, and a narrow check of its fixes found one Major on delegated children, now fixed and not re-read. Apart from those three reviews of shared issuers, no review has read the text of revisions 18 and 19.
- Revision 20 recorded the owner's choice of `0147-tenant-derived-runtimes` for decision 15's route.
- Revision 21 applied the requests in `0147-tenant-derived-runtimes`.
- Revision 22 removed replay as a separate route, under `0144-session-derive`, which merges it into derive.
- Revision 23 named `oneSessionPerPod` as the route to one-session pods, under `0147-tenant-derived-runtimes` decision 28.
- Revision 24 made a ticket optional on every platform admin action, which takes a justification instead, after `0142-security-oversight` decision 24.
- Revision 25 changed only pointers after the user credential documents split.
- Revision 26 followed a pass for a reader new to the project. It added "Terms", "Terms from other designs and the platform", "Labels from other designs", and "High-level design", trimmed the status line into these notes, moved the review records and the record of edits in other designs to "Review history" at the end, linked every citation of another design and of its labels, replaced the egress design's interface labels with their names, named the setting behind each configuration value the text cites, and renamed the hostile tenant in the mix-up scenario to `initech`. It changed no rule.
- Revision 27, dated 2026-11-08, corrected the attribution of the provider identity claim to UC-2 of `0151-user-credential-delivery`, the location of `serveServiceIdentity` in the gateway in the "Service identity" row and in step 3 of "Issuer pin and proof", and the "Sync worker" row, which no longer says the operator runs it, that a validation pass found, and changed no rule.
- Revision 28 applied decision 27, which the owner settled on 2026-11-08. The chart refuses `auth.platformAdminTenant: platform` at render time, with a message naming the reservation of the tenant ID `platform` in §10.2. The rule is stated under TS-2's **Operators' staff.**, in the operator tenant's "Terms" row, in the `auth.platformAdminTenant` row of "Configuration by scope", in the §17.6 entry of "Specification sections affected", in the TS-2 row of "Proposals and dependencies", and as a Tier 0 chart test.
- Revision 29 updated the `lifecycleSubjects` row in "Terms from other designs and the platform" after a re-read of `0127-egress-and-git`, `0151-user-credential-delivery`, `0152-user-git-identity`, and `0142-security-oversight` at their current revisions, and changed no rule. The row now states that a listed caller may invalidate and reinstate a security principal, as the `lifecycleSubjects` setting of [SO-2](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture) states.
- Revision 30, dated 2026-11-08, applies decision 28, which the owner settled that day with `0147-tenant-derived-runtimes` decisions 31 to 37 and `0127-egress-and-git` decisions 45 to 48, and changes these rules. The warm fill rule under "API" names warm-list entries of either declarer beside tenant warm-up additions and `gitRepository` warm fills, states the predicate a write must pass, and states that a later VCS pool change passes with a `Warning` and suspends the entry at render with `environmentScoped` until the pool is unscoped. The unbound and tenant-bound formulas under "Effective policy" include the warm list's hosts, the **Repositories.** rule names the warm list's public platform entries and the readable set before a session binds, and the "Filling the mirror" row points at the write and render rules. "Terms from other designs and the platform" gains "Warm list", and "Specification sections affected", the Tier 1 tests, and "Decisions" state these rules.
- Revision 31, dated 2026-11-09, applies the cross-design sync review of 2026-11-08, the owner's answer to its question Q8, and the instructions that `0141-runtime-routing` revision 7 and `0134-mcp-protocol-upgrade` revision 2 address to this design. It changes these rules. The experiment routing term, the final-row checks of "A session's environment", the crossing re-check of "Cross-environment sharing", the scenario, the specification list, and the tests now use the `PreRoute` chain and the post-chain point of RT-2, which runs the environment rules for every creation kind, with the parent's E in the `PreRoute` payload for `kind: delegate` and the child's E and T settled at that point. `VARIANT_ISOLATION_UNAVAILABLE` is no longer cited, and a refusal names the last modifier. **Model.** adds the parent's routing labels to what moves across (Q8, D14 of `0141-runtime-routing`). F-10.6.17 is closed by TS-3's rules at the RT-2 post-chain point, with a dependency row. The `restricted` row of "Effective policy" adds the LFS host sets, the warm list's hosts, and the lock, and the one-session row adds the lock, as the egress formulas state. "Discovery" and "Login routing (TS-7)" cite the `2026-07-28` MCP authorization page and state the audience check on `/mcp`. A route names an outbound connector once OC-2 lands, and decision 4 keeps outbound connectors and hosts tenant-wide. The sentence on proposals that depend on TS states SO-2's posture fields. These corrections change no rule: requirements carry the label TSR-n, TP10 sits in order, the duplicate F-11.4.21 rows merge, the SCIM credential text names who mints one, glosses for `netagent`, covered host, the mint checks, the pin term, and `lockEgressToHosts` match their owners, the egress pointers name "Reserved for other designs" in place of a revision, and the pod credential API replaces a bare I8.
- Revision 32, dated 2026-11-09, applies the owner's decisions of that date on mirror fill causes and ref resolution, given in the owner's words "Adversarially review your recommendations and, if they pass, go with them", and records that `0127-egress-and-git` revision 74 applies this design's request on mirror fill causes. It changes these rules. A mirror fill names the pool and one cause, a pod UID, a gateway cause assertion, or a prefetch record, which replace the session cause. The Token Service reads the pod's binding, verifies the assertion's signature, or reads the record itself, refuses a cause that is not live or that belongs to another tenant, and audits and alerts on the second. For a pod cause, E follows TS-1's rules, and for an assertion, E is the value it carries, and in both cases the pool must be the one the shared selection function picks for the upstream and E. The cause is checked at a fill's first mint, which returns a fill ID bound to the mirror's identity, the tenant, the upstream, and the pool, and the fill keeps minting on that ID for its lifetime, `tokenService.mirrorFillLifetimeSeconds` with a default of 3600. A prefetch record lives until its fill completes or fails, a change to its pool's `environments` or prefixes voids it, and every record carries a pool. The prefetch resource stays tenant-admin only, which replaces the rule that only a tenant admin can name a scoped pool, and the Tier 4 case for a non-admin's prefetch is removed. Ref resolution runs after the post-chain point, and before the pod claim where the plan arrives with the create request; at finalize, the pod was claimed at create under proposal 0007, so a bad ref fails the finalize on a claimed pod, and the assertion takes E from the session row. The assertion is a JWT of type `lenny-cause+jwt` with the audience `lenny-cause`, which only the Token Service and the decision service accept, and the decision service reads E from it on the direct path in place of the session the request names. A Tier 9 test holds the type rule. TP1, "Filling the mirror", the ref resolution row of "Enforcement points", the prefetch rule under "API", the mirror route row of "Cross-environment sharing", "Mirror assertions", "Changes requested of 0127-egress-and-git", the Tier 4 tests, and a new "Validation owed" section state these rules.
- Revision 33, dated 2026-11-13, applies the reviewer instructions of `0136-placement` revision 2 and `0150-tenant-security-settings` revision 1, from the owner's placement and tenant security settings decisions of 2026-11-12, the owner's rename of `sessionPolicy.maxConcurrentSessions` to `maxConcurrentSessionsPerPod` and of the data class Restricted to Secret, and the owner's removal of pre-launched runtimes the same day. It changes these rules. The environment record gains `placementRequirements`, which add to the tenant's requirements and cannot remove one, and the post-chain list of "A session's environment" names them. A child that crosses into T under TS-3 takes T's requirements together with its parent's. The environment record carries no `workspaceTier` override, under TSS O6 (a). Directory records are Confidential, and SCIM credential hashes and the client secret are Secret (TSS I51). The row of TS-3's hiding table for a runtime that cached the list no longer names an SDK-warm or recycled process, because a runtime process serves one session and launches at bind. "Terms from other designs and the platform", "Labels from other designs", "Data model", "Configuration by scope", "Specification sections affected", and the tier 4 tests follow.
- Revision 34, dated 2026-11-13, applies the cross-design review of that day. Under the egress design's decision 60, which the owner settled as option (a), a pod with several slots applies each session's environment per slot where its pool's runtime class answers slot attribution, and "Pod with no per-session policy" now names only the fallback where it does not: the term, the slot term, the effective-policy table, which splits the concurrent row in two, the rule's bullets, the crossing row, the concurrent-pool scenario, a residual risk, the tier 5 tests, and decision 15's amendment state it. Under `0119-agent-runtimes-only`, the service-mode term, the service-mode request check points, and every service-mode clause leave; the `caller_type` value `service` is unrelated and stays. The documentation row for "Tenant configuration" lists every refusal code and the slot attribution status, and "Specification sections affected" gains the §5.2 and §13.2 item. A follow-up in the same revision carries decision 60 into the mirror fill cause: the "Filling the mirror" row, the paragraph after "Changes requested of 0127-egress-and-git", and the tier 4 mirror fill test select at the attributed session's E on a pod whose pool answers slot attribution, as the egress design's **Fill causes** states, and at E = ∅ only on a warm pod and a pod with no per-session policy. The read-only cross-design check of the same day changes no rule of this design. Under the removal of `credentialPropagation` by "LU-4" of `0133-llm-upstreams-and-cost-budgets`, TS-3's interactions table replaces the `credentialPropagation: inherit` row with an LLM upstreams row in which a crossing child uses the upstream list of T, the crossing paragraph gives the delegation gate's first evaluation as the reason a final row that changes the crossing refuses the delegation, and guardrail 2 names the LLM upstreams of T's environment in place of LLM credentials. The hiding table's cached-list row no longer qualifies the runtime process as session-mode. The "Documentation" row for the configuration, API, and error references lists the new settings and error codes by name, and the "reads against" list names each cited design's current revision. TS-1's list of post-chain checks attributes the refusal of `lockEgressToHosts` on an `egressProfile: internet` pool to UC-5 of `0151-user-credential-delivery`, which owns it.
  - The last round of the same review, which changes no rule, cites the designs this one reads against at their current revisions.
- Revision 35, dated 2026-11-13, applies the owner's answers of 2026-11-13 to the consolidated list of open decisions across the designs, and changes these rules. Removing an environment's `placementRequirements`, or moving one to a weaker value, is a relaxation with a `justification` and the weakening delay (decision 29), in place of revision 33's removal at once with no delay; "Data model", "Configuration by scope", "Documentation", and the tier 4 tests change to match. A session on a concurrent pod with slot attribution whose environment denies a warm-list entry's host is refused with `ENVIRONMENT_POLICY_NOT_ENFORCEABLE` naming the entry (decision 30, from decision 41 of `0147-tenant-derived-runtimes`), which "Pods with several slots", "Failure behaviour", "Documentation", and the tier 1 tests now list under that code. Decision 31 records the settlement of the user credential delivery design's decision 41 and the user git identity design's decision 52, which "User credentials" under "Effective policy" and "Documentation" state, with the spike on slot attribution under "Validation owed". The status line's reads-against list names the current revision of each cited design.
- Revision 36, dated 2026-11-14, applies the owner's settled answer to question 17, decision 42 of `0147-tenant-derived-runtimes`, in the owner's words "sharedAssets should define any git stores to warm up. workspaceDefaults shouldn't also do that through `warm: true`", and changes no rule of this design. Every warm-list entry is now a `gitRepository` entry of a runtime's `sharedAssets`, so "Terms" (Warm list), the warm fill rule under "API", decision 28, and the matching test drop warm default clones, cache-only entries, and the private entries a platform admin wrote on a tenant-owned pool's row, and a refused tenant-runtime entry answers `INVALID_DERIVED_RUNTIME` whoever writes it.
- Revision 37, dated 2026-11-14, records the item **Slot attribution per runtime class (decision 31)** under "Validation owed" as measured: the AWS spike of 2026-11-14 found that runc answers `sock_diag` and gVisor and Kata answer only `/proc/net/tcp`, with the right slot UID from every answering reader, as `0127-egress-and-git` revision 83 states under "Slot attribution and fetch grants". It changes no rule. The status line's reads-against list names the current revision of each cited design.
- Revision 38, dated 2026-11-14, reconciles "Discovery" with [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), which the owner's decision of 2026-11-13 to move every client protocol adapter out of Lenny produced, and changes these rules. The gateway computes the protected resource identifiers of the adapters from the operator's adapter registry that design defines, and this design points there for the registry. A deployment-source token must carry `auth.oidc.clientId` or a computed adapter identifier in `aud`, in place of revision 37's requirement of `auth.oidc.clientId` on `/mcp`, and a customer that uses an adapter lists the adapter's per-tenant identifier in its source's `audiences`. "Discovery" gains **Adapter identifiers.**, its **Audience check.** is restated, and the broker contract's `aud` row and the tier 4 audience test follow. TS-1's explicit paths, TS-3's root creators, and the other items that design's instruction to this one lists are not applied by this revision. The status line's reads-against list names the current revision of `0134-mcp-protocol-upgrade` and adds [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md).
- Revision 39, dated 2026-11-14, applies the remaining instructions of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), and changes these rules. TS-1's explicit paths are `/v1/environments/{name}/sessions` and the `environment` body field, which a protocol adapter's environment-scoped form becomes, and the translator paths and `/mcp/environments/{name}` leave the term and "A session's environment". TS-3's root creators drop `lenny/create_and_start_session` and `BindSingleShot`, and the gateway-edge `/mcp` row leaves the hiding table. "Discovery" states that the client API accepts an adapter identifier only on a narrowed request and that the gateway refuses a tenant's write of `audiences` or `pin_audience` naming a deployment-wide or another tenant's identifier, and links "RO-4" and R9. The tier 4 delegation item names REST. The status line's reads-against list names the current revision of each cited design.
- Revision 40, dated 2026-11-15, applies the owner's decision of that day, in the owner's words "Yes, let's drop connectors.", which [`0124-connector-removal`](../0124-connector-removal/change.md) revision 2 designs, and changes these rules. Environments select runtimes alone: the "Environment" term and "Problem" drop `connectorSelector`, and the `DelegationPolicy` term drops connectors as targets. The "Connectors" row of the cross-environment sharing table becomes "Operator services": a crossing child reaches an operator service through T's allow list and T's credential bindings, and an `identityAssertion` names T as its environment. TS-3's **Effects on existing features.**, item 2 of **The check.**, the §10.6 item of "Specification sections affected", and the tier 4 delegation item follow. "Current state" and decision 1 keep the words they were written with, and the outbound connector of `0148-tenant-outbound-connector` is unrelated and keeps its name. The status line reads against `0142-security-oversight` revision 57, `0134-mcp-protocol-upgrade` revision 9, and `0124-connector-removal` revision 2.
- Revision 41, dated 2026-11-15, follows [`0143-service-account-credentials`](../0143-service-account-credentials/change.md) revision 1, which records the owner's answers of that day on service accounts. The `caller_type` term and **User credentials and links.** state the release rule for `human` and `service` principals and that invalidation of a service account's subject stops its releases. The status line reads against that design's revision 1, `0127-egress-and-git` revision 91, `0151-user-credential-delivery` revision 39, `0152-user-git-identity` revision 44, and `0124-connector-removal` revision 6.
- Revision 42, dated 2026-11-15, follows [`0143-service-account-credentials`](../0143-service-account-credentials/change.md) revision 2, which applies the security review of that design's revision 1 and the owner's answer of that day, "O1-O7: go with your recommendations". The review's finding 2 showed that a person and a service account could share a subject while session ownership, derive, invalidation, and erasure key on the subject alone. **Caller-type namespaces.** under "Several IdPs in one tenant, and namespacing" places a service caller's Lenny subject under `~svc~` and an `agent` caller's under `~agt~`, refuses a human token whose canonical subject begins with either with `401 SUBJECT_RESERVED`, and states which writes check the prefix. A `source_id` is never `svc` or `agt`. The terms, the §10.2 item of "Specification sections affected", and the tier 1 tests follow. The status line reads against that design's revision 2, `0127-egress-and-git` revision 92, `0151-user-credential-delivery` revision 40, `0152-user-git-identity` revision 45, `0124-connector-removal` revision 7, and `0142-security-oversight` revision 58.

# Review of revision 5

Two independent reviews of the user credential designs raised these on this document, each verified against the text.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| "It takes `onRequest` alone" against the pool's implicit attach | Holds | `poolAttach`, defined once in `0152-user-git-identity`. |
| The link's key | Holds | Keyed on the provider's name. |

None was rejected.

# Review of revision 6

Round 2 of the reviews raised N5, the attach trigger, and N13, the pin's reach against `visible`, on this document. Both hold, and the text above points to the attach rule and states the pin's source. None was rejected.

# Review of revision 7

Round 3 of the reviews raised H1, routes per environment against one digest, and m3, the "existing" column. Both hold. The route row points to the record's route value and states its cost, and the column text is corrected. None was rejected.

# Review of revision 8

Round 4 of the reviews raised M1, the route value's scope and order, M2, the covered set under environments, m1, the stale heading, and m21, a stale request. Each holds, and the rows point to the other designs' rules. None was rejected.

# Review of revision 9

Round 5 of the reviews raised the route assertion under `boundRoutes(E)`. It holds, and the row states it. None was rejected.

# Review of revision 13

One adversarial review reported on revision 13. Each finding was checked against the text and the tree by reading, and nothing was run. Provider and runtime statements in it stay untested.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| C1, shared callback and code theft | Holds. `Exchange` posts before validating, on one callback path. | "Browser login": per-source redirect URIs, refusal before any token request, RFC 9207, scoped cookies, and bound proof logins. The confidential broker client is a contract request. |
| H1, playground bypasses the registry | Holds. `extractSubject` reads raw claims. | One resolver for every browser login. |
| H2, `client_secret_ref` | Holds. | A write-only value the Token Service alone reads, sealed. The report's by-reference path for `platform-admin` is not adopted, so no field names a Secret. |
| M1, internal claims | Holds, today too. | The allowlist in TS-2, with `caller_type` through `service_claim`. `scope` stays dropped, with no mapping added. |
| M2, retirement | Holds. | Refusal by active source on every path, revocation, and optional `invalidateSubjects`. |
| M3, incident hold | Holds. | Holds per class in TS-4. |
| M4, `platform-admin` rows | Holds. `tenant_user_roles.go` already refuses the role, so that path needs no change. | One operator tenant. TP5 is corrected, and the residual is plain. Decision 24. |
| M5, unlisted fields | Holds. | Every change takes the rule except stated narrowings. |
| M6, pin index | Holds. | Partial index, reactivation, generic conflict, and a denied list. The shared-issuer entries are from knowledge. |
| M7, `subject_match` | Holds. | `match_claim`, declared immutable and verified, replaces `subject_match`. Administrator confirmation of the first bind is not adopted, as the smaller rule covers it. |
| M8, deployment settings | Holds. | A sentinel row, a reserved name, and the refusals. |
| M9, issuer over security roles | Holds. | Stated here and in SO, with posture. A second factor is decision 25. |
| M10, stranded principals | Holds. | The preflight refuses or lists and migrates nothing. SO's coverage alert counts a stranded row. |
| M11, login endpoints | Holds. The omitted ranges are confirmed in `egressCIDRs.excludePrivate`. | The checker covers each endpoint, with added ranges, and endpoints are captured at proof. |
| M12, CPU | Holds for the missing bound and for `MaxHeaderBytes`. The cost estimate is unmeasured. | Bounds in TS-2, background refresh, and a per-source limit. |
| M13, SCIM quotas | Holds. | Quotas, and extensions are dropped, which is smaller than capping them. |
| Minor, RLS reader | Holds. | A dedicated view. |
| Minor, group namespacing | Holds. | `source_id`, `~` refused, and unique `displayName`. |
| Minor, suspended | Holds. | A state, cleared by a `platform-admin`. |
| Minor, eviction and restart | Holds. | Idle eviction is removed, the restart case is stated, and refresh re-reads discovery. |
| Minor, disclosure | Holds. | The reason stays out of responses, the residual names the email page, and TP9 is corrected. |
| Minor, `lenny-ops` | Holds for the design's sentence, which is corrected. The report's point that the specification and code differ on IdP tokens was not checked further, because this design does not rely on it. | No path in the first version. |
| Minor, audience | Holds. | `azp` and non-empty `audiences`. |
| Minor, case-insensitive decoding | Holds by knowledge of the runtime. | Exact key, and a defect row. |
| Minor, erasure | Holds. | Addressed by SCIM `id`, and the preview holds no name. |
| Minor, SO conflict | Holds. | SO's sentence is corrected, a group write is not a lifecycle write, and `oidc` reads the roles claim directly. |
| Minor, provisioned rows | Holds. | Its own write path. |
| Minor, ongoing proof | Holds in part. | Domains are re-read and `email_domains` are unique. The sign-in proof stays one-time, as a residual. |
| The NetworkPolicy claim | Holds, and it corrects revision 13's claim. | Current state, SSRF section, and the defect row. |
| Proposal order table | Holds. | Each item lands where the table says. |

No finding was rejected outright. The parts not adopted are named above.

# Review of revision 14

A second adversarial review read the identity design at revision 14. Each finding was checked against the text and the tree by reading, and nothing was run.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| N1, dropping `scope` widens narrowed tokens | Holds. `Principal.Scopes` treats absent as no limit. | Kept and filtered to `tools:` values, and §25.1 is listed. |
| N2, immediate retirement | Holds. | The delay on a `platform-admin`'s retirement, a second approver or the delay on `invalidateSubjects`, stranded rows active for approval, a ticket on reactivation, and the lost-issuer case stated in both designs. |
| N3, reactivation revives | Holds. | `tokens_not_before`, a new proof, dropped keys, the wider preflight list, and how holds clear. |
| N4, the rule covers less than its purpose | Holds. | Introspection refused with a customer source, the settings row under the rule, no writes in `pending_effective`, proof cleared, and a digest. |
| N5, unbound rows | Holds. | The check on every deployment IdP token, `match_claim` at the boundary, `lifecycleSubjects` on a login hold, the report, and the residual. |
| Minor, proof against resolver | Holds. | The proof purpose is exempt. |
| Minor, allowlist extras | Holds. | Named, by exact key, per purpose. |
| Minor, absent subject claim | Holds. | Refused. |
| Minor, preflight role | Holds. | A `security-admin` row. |
| Minor, `lenny-ops` and step 7 | Holds. `tokenservice.go` checks only the caller's origin. | Step 7 names it and each token at `/v1/oauth/token`, and a playground-origin `subject_token` is refused. |
| Minor, snapshot view | Holds. | Fields added. |
| Minor, per-source bound | Holds. | Removed, and the background refresh stays. |
| Minor, confidential broker client | Holds. | The contract sentence is dropped, which is smaller than adding a value. |
| Minor, per-tenant cookie | Holds by reading the paths, and browser behaviour is from knowledge. | Scoping and the mint move in TS-7. |
| Minor, checker on `authorization_endpoint` | Holds by knowledge of IdPs. | A query is allowed there. |
| Minor, checker on the deployment issuer | Holds. | Exempt. |
| Minor, hold classes | Holds. | Classified by `manage_users` and `security-admin`. |
| Minor, bootstrap seed | Holds. `bootstrap.go` sets `u.Disabled = p.Disabled`. | Refused. |
| Minor, customer `match_claim` | Holds. | Fixed to `subject_claim`, with `externalId` unique. |
| Minor, uncapped writes | Holds. | Upserts capped per tenant. |
| Minor, retired issuer window | Holds. | A retired row holds its issuer until deleted. |
| Minor, stale wording | Holds. | Corrected. |
| Minor, credentials from a stolen admin | Holds. | Residual, and posture lists them. |
| Minor, default `caller_type` | Holds. | `human`. |
| Minor, SO wording | Holds. | SO names the roles claim. |
| Minor, UG step 5 | Holds. | Added to UG. |
| Order, UG-1 and TS-8 | Holds. | UG-1's dependency is unconditional, and TS-8 adds its step. |

No finding was rejected.

# Review of revision 15

A first review read cross-environment sharing and the delegation limits at revision 15. Each finding was checked by reading, and nothing was run.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| H1, a runtime names any session as parent | Holds today. The handler reads `in.ParentSessionID`, and `lookupCallerSession` checks the owner alone. | The calling session is the parent, with `PARENT_SESSION_MISMATCH`. Tier 9 covers a runtime's calls, and a forged adapter call stays with F-10.2.29, as a residual with no dependency. |
| H2, a runtime creates a root | Holds today. `Bridge.CallPlatformTool` dispatches any name. | The Bridge's list and call are allow-listed to §9.1's table. |
| H3, `lockEgressToHosts` | Holds. | A session under the lock cannot cross, and the risk text includes readable tokens. |
| M1, a crossed subtree keeps delegating | Holds. | Every delegation under a mark needs the flag and both names. |
| M2, the delay stops at the flag | Holds. | An added entry is a weakening, label edits are a residual, and the table names custom roles. |
| M3, depth reads Helm | Holds. `walkCap` uses `defaultMaxDepth`. | Depth from `parent.DelegationDepth`, with its own walk bound. |
| M4, `maxChildrenTotal: 0` plumbing | Holds. | Removed: stored as `maxDepth: 0`. Only `MaxDepth` gets a pointer, with `IsZero` and a test, and the extension refusal is dropped. |
| M5, `/v1/sessions/start` drops the field | Holds. | The field is added, every root creator writes a value, and nil refuses delegation. |
| M6, `delegationPolicyRef` has no effect | Holds. | Refused at create in revision 16, and revision 17 reverses that. That part of F-8.2.34 stays open. |
| Minor, check order | Holds. | Top of the handler, and again in `validateDelegation`. |
| Minor, gate read errors | Holds. | Fail closed. |
| Minor, removal while off | Holds. | Additions refused and removals allowed. Paths returning with the flag stay as a posture item, and the flag takes the delay. |
| Minor, empty selector | Holds. | Refused. |
| Minor, name references | Holds. | Both errors cover these names. |
| Minor, experiment rewrite | Holds. | A rewrite that changes crossing status or T refuses. |
| Minor, `PreRoute` on delegation | Holds. | Dropped from the delegation sentence and test. |
| Minor, what moves across | Holds. | Added. |
| Minor, "behaves the same" | Holds. | The exceptions are stated. |
| Minor, derive depth | Holds. | Stored minus source depth, and the narrowing clause is dropped. |
| Minor, parent-supplied 0 | Holds. | Removed, along with `Request.MaxDepth`. |
| Minor, wired layers | Holds. | Only Helm is wired today. |
| Minor, discovery | Holds. | From the session's E and paths. |
| Minor, default policy row and envelope field | Holds. | The row is corrected, and egress revision 53 carries the field. |
| Minor, `BUILD-GAPS.md` and `CheckDepth` | Holds. | `CheckDepth` changes, and the coordinator holds the list of stale entries. |

No finding was rejected.

# Review of revision 16

A stop check read revision 16. Each finding was checked by reading, and nothing was run.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| 1, invalidating the security team removes the delay | Holds. | Activation and a `deploymentIssuerAllowed` change take the delay and an objection at a tenant with a `security-admin` row in any state, in both designs. Retirement after invalidation stays immediate. |
| 2, the allow-list breaks in-pod git before EG-3 | Holds. `lenny/vcs_token` is outside §9.1's table. | The list holds it until EG-3, in both designs. The report's alternative of a dependency is not taken. |
| 3, refusing `delegationPolicyRef` breaks §27.4 | Holds. The refusal in revision 16 was wrong. | Reversed. The report's removal is not taken either: TS-3 leaves the field accepted with no effect, says so, and that part of F-8.2.34 stays open. |
| 4, the lock check misses a descendant | Holds. | Check 2 covers an inherited lock, recorded with the child's stored inputs, with a tier 1 case. |
| 5, `state` cannot hold a pending change | Holds. | `pending_version`, with `pending_effective` for first activation alone. |
| 6, deleting a source row | Holds. | Only a `retired` row, under the delay where one applies, with the dispute path stated and the scenario corrected. |
| Minor, reactivation with a reachable principal | Holds. | Approval and the delay when one exists. |
| Minor, compromised issuer cancels retirement | Holds. | Invalidation first, then retirement. |
| Minor, `invalidateSubjects` order | Holds. | Confirmation first. |
| Minor, absent `iat` | Holds. | Refused when `tokens_not_before` is set. |
| Minor, stale snapshot mint | Holds. | Residual. |
| Minor, scope near-misses | Holds. | Refused, and dropped values are counted. |
| Minor, §25.1 `caller_type: "agent"` | Holds. | `service_claim` sets it. |
| Minor, `await_children` | Holds today. | The same rule. |
| Minor, Helm depth of 0 | Holds. | Chart validation. |
| Minor, discovery under the lock | Holds. | Omitted. |
| Minor, lock check dependency | Holds. | A row. |
| Minor, SO dependency table | Holds. | A row in SO. |
| Minor, "active" in SO | Holds. | Extended to the platform admin limit. |
| Minor, settings row proofs | Holds. | They do not apply. |
| Minor, introspection residual | Holds. | Added. |
| Minor, selector widening | Holds. | The same `Warning`. |

No finding was rejected.

# Review of revision 17

A stop check read revision 17. Each finding was checked by reading, and nothing was run.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| Major, a `retired` or `suspended` row has no write rule | Holds. | Such a row refuses writes, a reactivation with changes takes a `pending_version`, and tier 4 covers it. |
| Minor, unchanged reactivation | Holds. | A residual, and the suggested delay is not added, because retirement must stay reversible during an incident. |
| Minor, objection route | Holds. | The objection is removed from activation, because a stolen `platform-admin` can defeat it. SO's objection on assignment stays. |
| Minor, objection under attack | Holds. | A residual. |
| Minor, exchange refusal in the documentation and code | Holds. | The page's row and TS-2 name each. |
| Minor, lock across derive and replay, and the lock's field | Holds. | Both copy `egressLockHosts`, which EG and UC now name. |
| Minor, `suspended` return | Holds. | The `state` row agrees with the failure table. |
| Minor, suspension and a pending version | Holds. | Suspension cancels it. |
| Minor, pending secret and pending `deploymentIssuerAllowed` | Holds. | Where each waits is stated. |
| Minor, draft deletion | Holds. | Allowed. |
| Minor, wording of the retirement row and SO's row | Holds. | Matched. |
| Filing, `lenny/await_children` | Holds. | The parent rule names both tools. |
| Filing, F-8.3.12 | Holds. | TS-3 defines and renders the value before validating it. |

No finding was rejected.

# Review of shared issuers

An independent review read the first text of decision 18. Each finding was checked against the text, and the provider statements in H1 and M6 were confirmed against Google's documentation.

| Finding | Verdict | Resolution |
| :-- | :-- | :-- |
| H1, `pinBy: audience` on Google admits any service account | Holds. | The `azp` rule on every shared-issuer row, a wider assertion, and a chart refusal on Google's issuer. |
| H2, the browser resolver had no pin check | Holds. | The resolver runs step 5 and refuses a row other than the callback's source. TP11 states it. |
| M1, one member can hold a non-domain value | Holds. | Proof 4 names the value, and tenant-only activation does not apply without a domain proof. The remainder is a residual. |
| M2, return from a lapse and avoiding the check | Holds. | `tokens_not_before`, a bound on failing lookups, a return needing an existing role holder, and `require_directory_row`. |
| M3, listing an issuer locks out its tenant | Holds. | Addressed first with a grace period and an in-place pin. The second review showed that fix was unsafe, and the second table below replaces it. |
| M4, list edits and overlapping rows | Holds. | The domain flag on the row, a row-based definition, a matching rule, an overlap refusal, and a check at start. |
| M5, one address answer suspends every tenant | Holds. | An issuer-level hold that changes no row and clears itself. |
| M6, the login request cannot carry the organization | Holds. | `authorizationParams`, and the open questions under "Unproven". |
| Minors | Hold. | Key swap on reactivation, the failed-closed row's tokens and credentials, more refused claims and a known-pair warning, the overlap read path, a start check on the deployment issuer, statements that assumed one issuer per tenant, and the snapshot's fields. |

A second independent review read the result.

| Finding | Verdict | Resolution |
| :-- | :-- | :-- |
| H1, a failed-closed row had no stored state, so a list edit reopened it | Holds. | The stored `pin_required` state and a marker that outlasts the entry. The grace period and the in-place pin are removed, since each added a path, and a row with no pin retires instead. |
| M1, adding a pin left other organizations' subjects active | Holds. | Resolved by the same removal. `claim` to `audienceAndClaim` stays, since its subjects share one organization. |
| M2, retiring a suspended row skipped the lapse conditions | Holds. | The conditions apply to every return of a row with a domain pin. |
| M3, `subject_claim` could name a reassignable claim | Holds. | A refused list on every row, and `sub` or `subjectClaims` on a shared-issuer row. |
| M4, `/v1/playground/token` sat outside registry step 7 | Holds, confirmed in `token.go`. | Step 7 and the directory check run in both mint modes and in the resolver, and a mint can no longer refresh `iat` for a stopped subject. The reviewer's further claim carrying the original `iat` is not needed once the mint refuses. |
| M5, reactivation skipped the overlap check | Holds. | The locked check runs on every move to `active`. |
| Minors | Hold, except as stated. | Pin fields copied from the entry, claim names in the overlap rule and the index, `deploymentIssuerAllowed` refused while a source is active, the proof's comparison target, a mass miss hold, a TXT read at effect, a suspended row in place of a gateway that refuses to start, and `clientClaim`. Adding a claim to an audience row is declined, because that row can hold subjects from any organization. |

A third independent review, run as a stop check, found no path by which a token, administrator, or user of one tenant acts in another while the entry's assertion holds, and no way around the activation rule. Its one Major, which it placed at the lower end, was that removing an entry stopped Google logins, because the login parameters lived on the entry alone. They now live on the row. Its Minors hold and are applied: an exit from `issuer_conflict`, a definition of a shared issuer, the playground record and impersonation mints, the check on entry to `pending_effective`, what a retired row holds, the mass miss hold's scope and end, `deploymentIssuerAllowed` across a return, a role row that must predate the lapse, and the response to a wrong entry.

A narrow check then read the third review's fixes and decision 15. Its one Major, a delegated child on a concurrent pod losing its parent's `egressLockHosts` and `egress.allowedHosts`, predates those edits and is fixed under **Pods with no per-session policy**, with matching text in `0127-egress-and-git` and `0151-user-credential-delivery`. Its Minors are applied. No review has read the text after this check.

# Changes in other design documents

Revisions 13 to 18 made these edits where a sentence had become false, and changed revisions as stated.

| Document | Edit | No edit needed |
| :-- | :-- | :-- |
| `0142-security-oversight`, revision 17 | "Who supplies" text, IdP removal under SCIM, the TS-4 and TS-2 dependency rows, decision 2's wording, the issuer's power over principals, a stranded principal, a lost issuer, a principal's groups, the `crossEnvironmentSharing` guardrail with its weakening and posture entries, adding a `dangerouslyShare` entry as a weakening, the roles claim in place of `lenny_role`, decision 19 settled, activation under a `security-admin` row in any state, and TS-3's dependency row | Decision 23 stays as written, and TS-6 applies its rule to the new record. `securityRoleSource` holds for every issuer. Security principals key on the canonical subject. |
| `0152-user-git-identity`, revision 17 | The start page's login through the shared resolver, the `claim` binding, the §10.2 note, decision 37's cost, UG-1's unconditional dependency on TS-6, the cross-environment rule, and step 5's refusal | `allowedAccounts` reads the provider and does not change under a tenant's own issuer. `identityBinding: directory` keys on Lenny's subject, and `verifiedEmail` needs the source to emit an authoritative `email`. |
| `0151-user-credential-delivery`, revision 14 | Its pointers, the sentence on a delegation bound, and the delegation paragraph | The user key is `(tenant_id, subject)`, and a customer source's subject is `~<source_id>~<value>`, with no sentence false. |
| `0128-forensics-evidence-preservation`, revision 17 | Its pointers, and the withdrawal of the `security-auditor` default under SO decision 19, in the read table, the SO-1 row, and decision 5 | A session's owner is still found on its row, and no sentence on records is false. |
| `0148-tenant-outbound-connector`, revision 17 | Its pointers | None. |
| `0127-egress-and-git`, revision 55 | The delegated child rule and the resolver hook, which named the retired intersection and bound, the `crossedFromEnvironment` envelope field and hook, UG-1's row, and the note that TS-3's list carries `lenny/vcs_token` until EG-3 | Its Token Service rule for provider APIs is what TS-6 reuses, and it does not need to name issuers. |

## Move into changes (2026-11-16)

Change 0113 moved this design from `designs/tenant-scoping-identity-design.md` into this folder. Its review history moved into this file, its proposal plan became "Steps", its evidence moved into `evidence/`, its status became `Draft` pending the owner's final cross-review, and every path to a design or its evidence, in links and in prose, now names its new location.

## Status line before the split (2026-11-16)

Change 0113 shortened the status line of `change.md` to the status, revision, date, and program, and moved the rest here as it stood:

> Revision 42 follows [`0143-service-account-credentials`](../0143-service-account-credentials/change.md) revision 2, which applies the security review of that design's revision 1 and the owner's answer of 2026-11-15, "O1-O7: go with your recommendations": a service caller's Lenny subject is `~svc~` followed by its canonical subject and an `agent` caller's is `~agt~` followed by it, the identity boundary refuses a human token whose canonical subject begins with either prefix, and no `source_id` takes either value, so a person and a service account never share a user key. Revision 41 follows [`0143-service-account-credentials`](../0143-service-account-credentials/change.md) revision 1, which records the owner's answers of 2026-11-15 on service accounts: a `userCredential` token is released to the principal on the session row when its `caller_type` is `human` or `service`, so a service account receives its own credentials, and invalidation of a service account's subject stops them. Revision 40 applies the owner's decision of 2026-11-15, "Yes, let's drop connectors.", which [`0124-connector-removal`](../0124-connector-removal/change.md) revision 2 designs: environments select runtimes alone, `connectorSelector` and §10.6 **Connectors are never cross-environment** leave, and a crossing child reaches operator services through its target environment's egress. Revision 39 applies the remaining instructions of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md) to TS-1, TS-3, and "Discovery". Revision 38 reconciles "Discovery" with [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md): the gateway computes the protected resource identifiers of the adapters from that design's adapter registry, and the client API accepts a deployment-source token whose `aud` carries `auth.oidc.clientId` or one of those identifiers, in place of revision 37's requirement of `auth.oidc.clientId` in `aud` on `/mcp`. Nothing here is built or staged, and no test or spike was run for it. It reads against [`0143-service-account-credentials`](../0143-service-account-credentials/change.md) revision 2, [`0127-egress-and-git`](../0127-egress-and-git/change.md) revision 92, [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) revision 40, [`0152-user-git-identity`](../0152-user-git-identity/change.md) revision 45, [`0142-security-oversight`](../0142-security-oversight/change.md) revision 58, [`0141-runtime-routing`](../0141-runtime-routing/change.md) revision 19, [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) revision 21, [`0134-mcp-protocol-upgrade`](../0134-mcp-protocol-upgrade/change.md) revision 10, [`0124-connector-removal`](../0124-connector-removal/change.md) revision 7, [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md), [`0136-placement`](../0136-placement/change.md) revision 11, and [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) revision 9. No decision under "Decisions" remains open. Statements about the tree were checked by reading the code and specification. No production component, identity provider, or directory has been exercised. Proposals carry the label TS-n, and requirements carry the label TSR-n. "Review history" at the end records what each revision changed and which text each review read. The owner's final cross-review of the moved designs is pending, so the status is `Draft`.

## M3 readiness review (2026-11-19)

Revision 43, 2026-11-19. Trigger: review. Name: M3 readiness review. The review read revision 42 against `main` at `092ef92f6` in two parts, 0149-p1 and 0149-p2, with the skeptic's verdict on the one High finding, 0149-p2-F1, which it downgraded to Medium, and the consistency reports of the clusters that name this change. Counts: 35 findings and conflicts applied, 6 of them in part; 9 raised as open decisions (decisions 32 to 40); 0 returned as issues; and 6 dropped, with the severity part of 0149-p2-F9 also dropped. The halves of the applied cross-change fixes that belong to 0126, 0127, 0138, 0142, 0143, 0144, 0147, and 0151, and the renumbering of a duplicate identifier in `BUILD-GAPS.md`, are returned as edits elsewhere. The status stays `Draft`.

Applied:

- 0149-p1-F2: registry step 7 strips one `~svc~` or `~agt~` namespace before it reads the source, treats a remainder with no `~` prefix as a bare deployment subject, and names 0143.S1 as the step that adds the stripping with the namespaces. `unit` and `security` tests cover a retired source's Lenny-issued `~svc~` token and a deployment `~svc~` subject at a pinned tenant.
- 0149-p1-F3: "Data model" orders the write check and the delete guard with a lock on the environment's row (`FOR SHARE` on every reference write and on the session write that records E, `FOR UPDATE` on the delete) and serializes VCS pool writes per tenant with an advisory lock. The failure row on a vanished environment and the `load_local` tests cover the three interleavings.
- 0149-p1-F4 and tenancy-security-C7: the explicit-zero conflict with `0126-delegation-availability` is resolved by that change's settled D4 and O5. The three sentences of TS-3 that disagreed ("0 in the stored integer keeps meaning no limit", "it has no field to narrow it", and "no extension rule is needed") now hold until 0126.S1, which adds the explicit stored `maxChildrenTotal: 0`, the derive narrowing, the extension rule, and `details.disabledBy`, and the "Hiding the tool" text records 0126's extension of the table. The tools take the names of 0135's "MD-23" (`start_child_session`, `await_child_sessions`, `list_child_sessions`, `message_child_session`, and `discover_agents`), "Model." states 0135's settled B-O5 for messages between environments and drops the removed `siblings` scope, and the intra-pod tool surface is 0139's `platform` tool server on `tool_call`, with the moot row on a process with several sessions removed.
- 0149-p1-F5: "Every root creator." and 0149.S10 state that `lenny/create_and_start_session` and `BindSingleShot` write the resolved lease until 0138.S6 removes them, and an `integration` test covers it.
- 0149-p1-F6 and tenancy-security-C11: 0149.S1 names where each of the mechanisms with a half elsewhere lands: the warm-list predicate with 0147.S8 as its owner and 0127.S5 for the platform-declared entries, `placementRequirements` with 0136.S6, `INVALID_DERIVED_RUNTIME` with 0147.S4 and 0147.S8, the derive `targetPool` checks with whichever of 0149.S1 and 0144.S1 lands second, and the `egressLockHosts` refusal with whichever of 0149.S1 and 0151.S5 lands second.
- 0149-p1-F7 and 0149-p2-F9, in part: 0149.S2 closes F-10.2.41, F-10.2.42, and `F-10.2.39 (part)`, with 0138.S2 closing the adapter identifiers. 0149.S6 closes `F-13.2.25 (part)`, F-13.2.30, and F-27.3.17. 0149.S7 names the cookie-path F-27.3.18 in its text, because `BUILD-GAPS.md` holds two findings under that identifier.
- 0149-p1-F8, in part: 0149.S3 splits into 0149.S3 (propagation, the session gate, fail-closed reads, the parent rule, the allow-list, groups on exchanged tokens, the delete guard, and the transparent create check), 0149.S10 (the client delegation limits), and 0149.S11 (cross-environment sharing), with the `closes` entries divided between them. The split of 0149.S6 is decision 40.
- 0149-p1-L1 and 0149-p2-F10: package paths follow the moves under 0117 (`pkg/gateway/core/session/sessionserver`, `toolbridge.Bridge` in `pkg/gateway/platformtools/toolbridge`, `pkg/gateway/api/serviceidentity`, `pkg/gateway/api/admin/rbac_config.go`, `pkg/gateway/playground/oidc.go`, `pkg/auth/authmw/auth.go`, `pkg/tenancy/envaccess`, `pkg/tenancy/userstore`, and `pkg/gateway/platformtools/delegation/service.go`). "Tests" uses the tier names of change 0155, and the §18.35 item points to the v1 program's milestone exit criteria.
- 0149-p1-L2 and 0149-p2-F19: 0149.S1 states that it reshapes no seam operation, 0149.S7 names the route registration text, `GWSEAM-HandleRoute`, and `GWSEAM-RequestTenant`, 0149.S8 states that it registers the SCIM routes through `GWSEAM-RegisterRoutes` with the existing no-check access check, and the new steps carry their own decision-8 lines.
- 0149-p1-L3: `auth.platformAdminTenant` defaults to the value of `gateway.adminToken.tenant` (`default`), and a single-tenant install's tenant is the operator tenant.
- 0149-p1-L4: `discover_agents` omits sharing paths from a session under a lock of its own or an inherited one.
- 0149-p1-L5: the ref-resolution validation item names 0149.S1 and 0141.S1 as the steps that wait on it.
- 0149-p1-L7, in part: the snapshot's poll and maximum age are named as `identity.registry.pollSeconds` and `identity.registry.maxSnapshotAgeSeconds` under "Configuration by scope". Their defaults are decision 36.
- 0149-p1-L8: the chart check on `identity.customerSources.enabled` reads what `allow-gateway-egress-llm-upstream` renders under 0133, and refuses unless it renders the broad peer with its excepts or `identity.issuerCIDRs` is set.
- 0149-p1-L9: `gateway.delegation.defaultMaxDepth` replaces `deploymentAudit.defaultMaxDepth` in the deployment audit's configuration map, so the two cannot diverge.
- 0149-p2-F2: a `full_revoke` row refuses a bearer with `403 USER_INVALIDATED`, the status the catalog and the §12.8 fence give the code, in "Refusal at authentication.", the scenarios, the §15.1 item, and the error reference.
- 0149-p2-F3, in part: `holds` is the single source of the invalidation level, `Disabled` and the invalidation use of `DeletedAt` are derived from it, `IsActive`, `PlatformRoles`, and the creation fence read the effective mode, and a hold write takes the fence's row lock. The §12.8 item and the tests follow. Whether a `DELETE`d subject can return is decision 35.
- 0149-p2-F4: a cleared hold class keeps a tombstone with mode `none` and its stored time, with `unit` and `chaos` tests for a retried older invalidation.
- 0149-p2-F5, in part: the upsert cap counts per caller class, exempts the `admin` class, answers `429 QUOTA_EXCEEDED` with `details.quota: userUpserts`, and raises `UserUpsertCapNear`. Its default is decision 36.
- 0149-p2-F6: a SCIM credential authenticates only on its SCIM routes and reaches TS-4's code through `active` and `DELETE` writes, in **Credentials.**, **Permission.**, "Who can do what (identity)", and the `security` test.
- 0149-p2-F7 and tenancy-security-C6: the reduction to `user` runs after every resolver and removes SO-1's `security-*` roles, a held principal does not count as active, and a `security` test covers it.
- 0149-p2-F8: the §16 item names each metric and alert with the step that emits it, and "Documentation" adds `reference/metrics.md` and a runbook per alert.
- 0149-p2-F11, in part: "Configuration by scope" names `identity.sourcesPerTenant`, `identity.maxKeyStalenessSeconds`, `identity.maxRSAKeyBits`, the `identity.scim.*` counts, and `identity.userUpsertLimitPerClass`, and the §17.6 item and the `static` rendering test name `auth.serviceClaim` and `auth.platformAdminTenant`.
- 0149-p2-F12: the SCIM credential row of "Configuration by scope" follows TS-8: a platform, tenant, or security admin mints one, and `manage_user_lifecycle` alone does not.
- 0149-p2-F13: the error reference adds `SUBJECT_RESERVED`, `TENANT_PATH_MISMATCH`, `USER_INVALIDATED` at authentication, `QUOTA_EXCEEDED` for the upsert cap, and `FORBIDDEN` with `details.reason: self_reinstatement`.
- 0149-p2-F17: tests cover the upsert cap, a retried older invalidation after a reinstate, `reinstate` on a `DELETE`d row, the creation fence for a SCIM-held user, the Token Service failing closed on its user-row read, SCIM credential rotation, and the failed-authentication rate limit.
- 0149-p2-F18: `tokenService.mirrorFillLifetimeSeconds` is documented by 0127, which defines it, and the identity page shows how a tenant role is granted, so 0149.S2 closes ADMIAM-I1.
- tenancy-security-C2: the platform-role boundary of TS-2 covers every platform role, `platform-admin` and 0128's `platform-forensics`, in the IdP strip, `role_map`, and the operator-tenant confinement, with a `security` test.
- tenancy-security-C10: 0149.S6 refuses activation and a `deploymentIssuerAllowed` change at a tenant with any security principal row until 0142.S2 lands, and leaves retirement available.

Raised as open decisions:

- Decision 32: what the delegation gate reads for a session with E = ∅ (0149-p1-F1). Recommendation (a), the owner's memberships stored at create.
- Decision 33: which entries `lifecycleSubjects` accepts (0149-p2-F1, tenancy-security-C1). 0142 records it as its decision 36 and 0143 as its O11. Recommendation (a), a `~svc~` subject or `scim:<source_id>`.
- Decision 34: whether 0149.S5 stays `Planned` (0149-p1-L6). Recommendation (a), `Cancelled` for v1.
- Decision 35: whether a `DELETE`d subject can return (0149-p2-F3). Recommendation (a), a `platform-admin` restore with a justification.
- Decision 36: the defaults of the identity settings that have no value (0149-p1-L7, 0149-p2-F5, 0149-p2-F11).
- Decision 37: the groups of a `service` or `agent` caller under `group_source: scim` (0149-p2-F14). Recommendation (b).
- Decision 38: SCIM edge cases on erasure, re-provisioning, the directory read's cache, and the source address (0149-p2-F15).
- Decision 39: a separate hold class for `security-admin` (0149-p2-F16). Recommendation (a).
- Decision 40: whether 0149.S6 splits, and where (0149-p1-F8). Recommendation (b), two steps.

Dropped:

- client-api-sessions-C9: it concerns the order of 0126.S1 and 0135.S5, and names this change only as 0126.S1's dependency.
- connectors-llm-billing-C4: it concerns the order of 0124.S1 and 0123.S1, and names 0149.S6 only as the dependency CA-0 lacks. Decision 40 may change which 0149 step CA-0 needs.
- program-C9: its edges are 0126's, 0134's, 0127's, and 0146's, and it names this change only as 0126.S1's dependency.
- runtime-contract-C10: it concerns 0141.S2 and 0147.S12, and names 0149.S1 and 0149.S3 only as 0147.S12's existing edges.
- credentials-git-C5: it adds 0143.S1 to 0152.S2, and names 0149.S4 only as an existing edge.
- tenancy-security-C4: it concerns the pending versions of 0142 and 0150, and cites this change's pending-version rule as the model it already states.
- The severity mismatch in 0149-p2-F9: the High finding the change lists as F-27.3.18 is the cookie-path finding, which `BUILD-GAPS.md` files as High. The Low one is the other finding under the same identifier.

Second pass, 2026-11-19. This pass closed the 10 requests that the fix agents of other changes made of this change. Counts: 5 applied, 3 already done, 2 raised as open decisions (41 and 42), 0 already open, and 0 dropped. The requests already done were 0126's (TS-3 follows 0126 D4 and O5, with `details.disabledBy` and the extended "Hiding the tool" text), 0128's (tenancy-security-C2), and 0135's (the tool names of MD-23, the removed `siblings` scope, and B-O5). The revision number and the status are unchanged.

- Applied, 0127 and 0147 (tenancy-security-C11): 0149.S1 and the TS-1 rules under "API" state that 0149.S1 builds the VCS pool `environments` field, that 0147.S8 owns the warm-list write check and render suspension for tenant entries and 0127.S5 for platform-declared entries, and that whichever of 0149.S1 and the owning step lands second adds the environment case.
- Applied, 0142 (tenancy-security-C10): 0149.S6 states that 0142 lands the security-principal write routes with 0142.S2, so no principal row exists before the delay, and keeps its refusal as a guard. The `lifecycleSubjects` counterpart is decision 33, and the role strip of tenancy-security-C6 was already in TS-4.
- Applied, 0142 (conditional on its decision 41): "Notes on the steps" states that no step here lists 0142.S2 under `depends-on`, and names where each text reference to 0142.S2 moves if the owner chooses option (a).
- Applied, 0150 (0150-F21): 0149.S4, 0149.S6, 0149.S8, and 0149.S9, which write data classes into §12.9, depend on `0150.S1 (part)`, and "Notes on the steps" states why.
- Raised, decision 41: whether this change adds the RFC 7523 `jwt-bearer` grant that 0120's M8 requests (requested by 0120).
- Raised, decision 42: which change owns the resolver part of SA-6, and the order of 0149.S8 against 0143.S1 (requested by 0143, mirroring its O9). 0143's O11 is decision 33.

## Revision 44 (2026-11-23)

Revision 44 takes on the row for this change in "DP-7" of [`0166-delegation-primitives`](../0166-delegation-primitives/change.md#dp-7-what-each-change-takes-on) revision 7, under that change's decisions 40, 41, 44, and 45, settled by the owner on 2026-11-23, with decisions 15 and 34, which the row cites. The status stays `Draft`.

- Decision 41: TS-3 owns crossing for every operation. "Model" states what a crossing child takes from its parent's grant and what T's policy gives it, with DPR-3's subset rule confined to one environment, and gains rules for messages, files, a parent's read of a crossed child, parent tools, and leaving the parent. A release of a crossed child meets T's explicit admission check, and parent tools are refused across environments with `TARGET_NOT_IN_SCOPE`. "The check" applies to `session.create` and to the release of a session with a crossing mark, and the "Default delegation policy" interaction becomes the post-chain check of the policy cap.
- Decision 45: a `files` export, a `materialize` push, and a `files.import` across environments apply both content caveats.
- Decision 34: the watch and `session.messages` of a crossed child carry only the list in "Model", and MD-25 of 0135 owns the filter.
- Decision 40: "Resolution" under "Client delegation limits (decision 26)" cites 0166 "DP-1" in place of TS-3's minimum over the preset, the runtime default, and the policy ceiling. A runtime contributes a default and, separately, a ceiling, so a client value above a runtime's default and below every ceiling now stands. Decision 26's words "a client can only narrow" read against the ceilings under 0166 decision 40, which the owner settled with TS-3 named, so no decision is raised. `maxDepth: 0` stays a client input to the grant.
- The delegation gate becomes the environment caveat of the grant, the calling-session rule covers every platform operation, the Bridge allow-list admits 0166's operations, and an environment gains `delegationCeilings` beside `defaultDelegationPolicy`, which applies at every `session.create`. "Configuration by scope" lists both.
- Decision 15: tree recovery gives way to each session's own recovery. Decision 44 needs no edit here, because TS-3 states no rule for `labels`.
- `start_child_session`, `await_child_sessions`, `list_child_sessions`, and `discover_agents` are renamed to `session.create`, `events.wait`, `session.list`, and `runtimes.list` in the design text, the scenarios, the residual risks, the specification sections, the documentation, the tests, and `steps.md`. "Current state" and "Defects found while verifying" keep the names they were written with.
- Raised decision 43: whether a `session.transfer` of a crossed child meets the same admission check as a release, which 0166 does not state.
- Later on 2026-11-23 the owner settled decision 43: a `session.transfer` of a crossed child meets the same admission check as a release (TS-3 "Leaving the parent"). Decision 44 is raised: decision 26's storage of a client's `maxChildrenTotal: 0` as `maxDepth: 0` contradicts 0126 O19 (a), and it stays open while the owner reassesses resolution.
- Later on 2026-11-23, at the owner's direction: an operation that names a session other than the caller's child answers `404 RESOURCE_NOT_FOUND` under §10.2's existence-hiding rule, and `PARENT_SESSION_MISMATCH` leaves, because its earlier subject, a parent argument that differed from the calling session, no longer exists under 0166.
- Later on 2026-11-23, at the owner's direction: "What a parent reads of a crossed child" keeps the child's usage totals, citing 0135 "MD-25", which owns the rule.
- Later on 2026-11-23 the owner accepted 0166's resolution with its gap fills (0166 decision 49): decision 26's "client can only narrow" and its `maxChildrenTotal: 0` mapping are replaced, decision 44 settles as (a), TS-3 **`maxChildrenTotal`.**, "Configuration by scope", the §8.2 row, and the unit test drop the mapping, and TS-3's rule that only a client supplies 0 is marked as under review as 0166 O33.
- Later on 2026-11-23 the owner settled 0166 O33 as 0166 decisions 50 and 51: TS-3 "Resolution" states that a client or a parent supplies 0, and cites 0166 "DP-1" for the refusal of a ceiling of 0 and 0126 "DA-1" for the off switch, the environment level among them.
- 2026-11-23, revision 45, from the review of 0166 and 0170 recorded in [`0170-budgets` review history](../0170-budgets/review-history.md). Records decision 45 and adds "Label rules (TS-10)" with step 0149.S12, its settings, its specification sections, its documentation page, and its `security` tests. The `unit` test of lease resolution states 0166's fall-through of defaults (0166 decision 49).
- 2026-11-24, revision 46. From the second review of the 0166 branch: decision 46 (S2), under which a crossing child's labels are checked against the target environment's rules and a rule write that frees a key a budget selects on answers a `Warning`; a derive's labels are its request's own under 0144 decision 14 (S1); `valueFrom.group.match` is an anchored RE2 expression of at most 256 characters over the stored group name with its source prefix, with `source`, the `${1}` replacement, and the refusal of an empty or invalid value, and a `security` test with two sources (Security 4); the environment's delegation inputs drop the carved budget from `delegationCeilings`, add `maxLeaves` and `sessionBudget`, and list the session budget settings of 0170 with their configuration rows (M2). Item 8 of the crossing check and 0149.S12 follow.
- 2026-11-24, revision 47. Under decision 22 of `0133-llm-upstreams-and-cost-budgets`, the gateway reaches no LLM provider, so `allow-gateway-egress-llm-upstream` leaves and the gateway's egress to customer issuers becomes `allow-gateway-egress-issuers`, rendered when `identity.customerSources.enabled` is true, with the broad peer and its excepts or `identity.issuerCIDRs`. The crossing row of TS-3 for LLM upstreams becomes LLM access through the attribution bindings of the environment the child runs in. 0133 withdrew its open decision 10 on the old rule's peers.
- 2026-11-24, revision 48. The gateway reaches customer issuers over `allow-gateway-egress-external` of 0138 RO-2 **Gateway egress.**, the gateway's one outbound HTTPS rule, which also serves webhooks and the platform's OIDC issuer, in place of the issuers-only `allow-gateway-egress-issuers` of revision 47. `identity.issuerCIDRs` merges into the strict list `egressCIDRs.gatewayExternal`.
