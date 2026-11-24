# Review history: 0127-egress-and-git Egress and git access for agent pods

# Revision notes

The notes for revisions 44 to 66 were carried in the status line of those revisions until revision 67 moved them here. The notes for revision 67 and later were written here.

- Revisions 44 to 51 applied the hooks that `0152-user-git-identity` revision 11 and `0151-user-credential-delivery` revision 8 requested.
- Revisions 47 and 48 applied the owner's git path decision (decision 44): git keeps its remote URLs, uses `netagent`'s egress endpoint as `http.proxy`, and `netagent` terminates VCS-covered hosts. Validation rounds before revision 47 measured the prior loopback path.
- Revisions 52 to 55 changed only the delegated child rule, the resolver hook, the `crossedFromEnvironment` envelope field, UG-1's dependency row, and a note under "Replaced pieces", after `0149-tenant-scoping-identity` decision 14.
- Revision 57 applied the requests in `0147-tenant-derived-runtimes` revision 9, and revisions 58 and 59 applied the requests in its revisions 14 to 29.
- Revision 60 named the variables a tenant overlay can set, limited them to pools with `egressProfile: internet`, and stated the reservation's limit on those pools, after `0147-tenant-derived-runtimes` decision 14.
- Revision 61 stated which traffic an egress group's address does not cover.
- Revision 62 recorded that Azure DevOps answers only protocol v0.
- Revision 63 applied the review in `0147-tenant-derived-runtimes` "Review of revisions 39 to 46" to the tenant proxy variables, the egress group exceptions, and the reservation residual.
- Revision 64 recorded the outbound connector's Envoy spike on a 5xx deny in HTTP mode.
- Revision 65 qualified the 5xx deny result and added a level 2 row for `failure_mode_allow`. The last review round before it, recorded under "Review record", was a stop check that found no Major or higher issue. That check traced the last round's changes and was not an end-to-end read.
- Revision 66 changed only pointers after the user credential documents split.
- Revision 67 added "High-level design", gathered the git sections under "Git" with their headings unchanged, added "VCS pool fields", moved "Validation still owed" into the main text, moved this history out of the status line, and named the setting behind each configuration value. It changed no rule.
- Revision 68 followed a pass for a reader new to the project. It added "Scope", "Terms", "Terms from other designs and the platform", "Abbreviations", "Labels from other designs", a tag table with an evidence note, and plain-language opening paragraphs in "High-level design". It named the interfaces in prose, linked other designs and their labels, named the setting behind the remaining bare values, and grouped "Revision notes" and "Review record" under "Review history". It changed no rule.
- Revision 69 corrected the pod-warm summary in "Terms from other designs and the platform", which placed the runtime start at claim rather than at session start, and the introduction to "Revision notes", which said every note came from a status line, that a validation pass found, and changed no rule.
- Revision 70 updated "Request inspection stage" after a re-read of `0151-user-credential-delivery`, `0152-user-git-identity`, `0129-github-credential-addon`, and `0130-gitlab-credential-addon` at their current revisions, and changed no rule. The stage's list of what reaches `netagent` now names the `reads` setting under a default of deny, and a rule refusal names the reason `not_listed`, both from the generic design's "Allow-list mode". The UG-3 row of the proposal dependencies gains EG-3 with git path T, as the VCS layer's own dependency table states.
- Revision 71, dated 2026-11-08, applies decisions 45 to 48, which the owner settled that day with `0147-tenant-derived-runtimes` decisions 31 to 37, and changes these rules. Before a session binds, a pod's readable set is the pool's warm list plus the repositories of the warm-up policy that applies to it, in place of the union of a tenant-owned pod's warm list with the base's public `gitRepository` entries, and the unbound and tenant-bound formulas under "Effective policy" gain the warm list's hosts. The `gitAccess` formula's `git hosts` reads the pool's warm list. A `gitRepository` entry on a Runtime is public only, and a private store comes from a tenant-owned pool's warm list. A store serves any `gitClone` source whose URL matches, and the bullet **For a tenant's default clone** becomes **For the warm list and a tenant's default clone**. "Relationship to proposal 0111 and open findings" points at the requirements for the rewrite of 0111, states that the rewrite drops the lineage rule if no case remains, and states that the calls carrying the restore set also carry the stores to withhold. "Terms", the Proposal 0111 row, the warm-up policy row of "Scopes and who manages them", the warm fill scenario, the tenant-owned pool row of "Pod models", the mirror's warm fill bullet, "Specification sections affected", and the Tier 5 warm-up test state these rules. Decision 38 of that design stays open with its recommendation.
- Revision 72 records that `0147-tenant-derived-runtimes` decision 38 is settled, and changed no rule.
- Revision 73, dated 2026-11-09, applies the design sync review of 2026-11-08, the owner's answers to its questions, and the instructions that [`0141-runtime-routing`](../0141-runtime-routing/change.md) revision 7 and [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) revision 3 address to this design. It changes these rules:
  - The warm list is rendered per pool and revision, and the warm-list hosts in "Effective policy", the readable set before binding, and level 1's identity lists read the pod's pool and revision (RV-3).
  - The refusals that depend on the pool run at the post-chain point of RT-2 on the final pool, with the last modifier named ("Where the pool checks run").
  - A base write that raises the setup bound is refused when any pool, platform or tenant-owned, no longer fits its window, and the template-write clamp becomes the same refusal (decision 49).
  - A held store past `gitMirror.heldUidBudget` is re-chowned after an ownership manifest (decision 50).
  - A ceiling of `*` matches a tenant's outbound connector hosts, as the outbound connector design settled.
  - Decision 48 gains the exception for a public `gitRepository` entry on a Runtime, which passes at write with a `Warning` and is checked per owned pool at render.
  - The evidence envelope places a quarantined pod's records under the platform prefix with the tenant in the payload, the escrow operation also moves batches of a stream in the security retention class, `egress.accesslog` names `lenny-egress` as its producer, `egress.local` carries request rule refusals, and `credential.mint` names releases and the fields other designs add.
  It also records the residual of MCP connector calls, which run on the gateway, from `0123-connector-authorization-and-external-memory`, notes that `0131-incremental-checkpoint` decision D6 is pending, cites §4.4.4 for the hard workspace size limit, names `0152-user-git-identity` as the owner of `allowedPushOptions`, adds the dependency rows for TS-1, TS-3, FE-1, DR-4, DR-5, DR-9, DR-11, SO-5b, and OC-2, makes EG-12 depend on EG-3, lists the open requests of `0149-tenant-scoping-identity` and `0148-tenant-outbound-connector`, names the outbound gateway in "Tenant outbound connector", replaces "two gates" with "mint checks", and removes explicit counts, history sentences, and a garbled clause from body sections.
- Revision 74, dated 2026-11-09, applies the owner's decisions of that date on mirror fill causes and ref resolution, given in the owner's words "Adversarially review your recommendations and, if they pass, go with them", and applies the open request of `0149-tenant-scoping-identity` on mirror fill causes, which leaves "Requests still open". It changes these rules:
  - A mirror fill names the pool and one cause: the UID of the pod whose fetch caused the fill, a gateway cause assertion, or a prefetch record. These replace the session cause and the warm fill cause.
  - The Token Service reads a pod's binding through its own `pkg/binding` instance, verifies a gateway cause assertion's signature, or reads the prefetch record itself. It refuses a cause that is not live or that belongs to another tenant, and a cause from another tenant also writes an audit event and raises an alert.
  - For a pod cause, E follows TS-1's rules, and for an assertion, E is the value it carries. The Token Service accepts only the pool the shared VCS pool selection function picks for the upstream and E, and for a prefetch cause only the pool the record names.
  - The cause is checked at a fill's first mint. The Token Service returns a fill ID bound to the mirror's identity, the tenant, the upstream, and the pool, and later mints that present it need no new check for the fill's lifetime, `tokenService.mirrorFillLifetimeSeconds` with a default of 3600, which "Configuration by scope" lists.
  - A prefetch record is live from its write until its fill completes or fails, and a change to the named pool's `environments` or prefixes, or its deletion, voids a pending record. Every prefetch record carries a pool, and the prefetch resource stays tenant-admin only.
  - Ref resolution runs after the post-chain point of RT-2 on every creation path, and before the pod claim where the plan arrives with the create request. At finalize, the pod was claimed at create under proposal 0007, so a bad ref fails the finalize on a claimed pod, and the assertion takes E from the session row.
  - The gateway's `ls-refs` carries a gateway cause assertion on the direct path and the mirror path: a JWT of type `lenny-cause+jwt` with the audience `lenny-cause`, valid for 60 seconds and signed with the gateway's signing key. The decision service verifies it and reads E from it, and the Token Service and the decision service accept only that type and audience for a cause, which a new Tier 9 test holds. "Terms", "Credentials", **The gateway runs no git.**, **Where the pool checks run**, "Specification sections affected", the requests on the proposals, the Tier 4 tests, "Validation still owed", and a new residual, **Mirror fill causes**, state these rules.

- Revision 75, dated 2026-11-13, applies the owner's decisions of 2026-11-12 in the consistency pass across the designs, recorded as decisions 51 to 58, and the instructions that [`0145-session-isolation`](../0145-session-isolation/change.md), [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/change.md), [`0139-runtime-protocol`](../0139-runtime-protocol/change.md), [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md), [`0153-workspace-materialization`](../0153-workspace-materialization/change.md), [`0136-placement`](../0136-placement/change.md), and [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md) address to this design. It changes these rules:
  - `lenny-materialize` runs every git step on a slot repository as the slot UID with the pinned git, and the adapter runs no git on slot repositories; it keeps the pinned git for the store fill alone. The workspace ownership item of 0111 is settled, the tree-size check maps `git_tree_limit` to `UPLOAD_ARCHIVE_LIMIT_EXCEEDED`, the index refresh runs more than 1 s after checkout, and §13.4's archive sentence gives way to WM-9's rule (decision 51).
  - "Slot attribution and fetch grants" is new: `netagent` attributes a connection to its slot by owner UID, and the adapter's fetch grant over `CH-FETCHGRANT` gives a helper run's fetches the materialization rules (decision 52). Per-slot scopes are open decision 60, and the store fill's parsing at the adapter UID is open decision 61.
  - The embedded deployment model and pre-launched runtimes leave every table, test, and statement, and the name-constrained trust anchor applies on every session-mode pod, with service mode and `type: mcp` as the remaining cases without constraints (decisions 53 and 54).
  - The paths follow the fixed layout, the stores are on `lenny-git-stores`, per-slot `delivery: file` volumes replace the shared one, and the warm-up checkout and the reflink rebuild join the warm-clone text, with a withheld store's staged tree removed (decision 55).
  - The tenant key and dedicated node tenancy replace T4, and the data classes take their new names (decision 56).
  - System-component placement points to the placement design, the DaemonSet rejection names the class taint, and the `proxied` DNS resolver is stated as `lenny-proxied-dns`, a second CoreDNS beside `lenny-agent-dns` (decision 57).
  - The renames apply, and `allowCrossTenantReuse` is gone (decision 58). `gc.auto=0` and `maintenance.auto=false` join the rendered git configuration (decision 59).
  - "Terms" gains the slot, slot UID, `lenny-materialize`, the warm-up checkout, the fetch grant, slot attribution, runtime capabilities in place of integration levels, the tenant security settings, the data classes, and the placement terms. "Labels from other designs" gains the designs of the pass. "Instructions for reviewers of other designs" is new, and EG-3 is ordered before RP-B.
- Revision 76, dated 2026-11-13, applies the cross-design review of that day and the owner's decisions of 2026-11-13, recorded as decisions 60 to 63, and the requests that the consistency pass collected for this design. It changes these rules:
  - Per-slot scopes (decision 60, option (a)): each slot's connections use its own session's readable and writable sets, E, `egress.allowedHosts`, `egressLockHosts`, and bindings; `pkg/binding` returns each session's slot index; `netagent` flushes a session's cached credentials and closes its tunnels at its slot's release; a mirror fill's pod cause names the attributed session. The union scope, E = ∅, and the refusals remain only as the fallback on a pool whose runtime class does not answer attribution, which the pool's status records. "Effective policy", "Pod models", the scenario of the chat runtime, "Where the pool checks run", and the tests follow.
  - The store fill runs in `lenny-store-fill`, an init container under its own UID with no credentials and only the store volume writable, which `netagent` attributes as the warm fill before READY; the adapter keeps the store volume read-write only to delete withheld stores (decision 61). The §13.4 text drops the adapter's fill as a third parser.
  - Proposal 0 is withdrawn, F-5.2.65 and F-5.2.66 close by removal under AR-B of `0119-agent-runtimes-only`, and AR-B lands before EG-2. The service-mode pin record and its sub-bullets, the service-mode rows of "Terms", "Pod models", "Effective policy", the egress group label, and level 1's identities, the service-mode scenario, and the §12.8, §12.9, §25.11, §13.2, and §28 items for pin records and the listener leave. The trust anchor is name-constrained on every agent pod (decision 62).
  - An operator's `gitRepository` warm entry may name a target `path`, and such entries are staged like default clones on every pool (decision 63).
  - A runtime may override the rendered git maintenance settings at its own checkpoint cost (IC D6).
  - `netagent`'s request is counted within the pool's resource class under the class mapping of `0121-billing-resource-attribution`; each slot's `delivery: file` volume is rendered per slot; a granted fetch on a host with `userAuthorization: required` takes the same per-user check as any fetch.
  - "Documentation" gains "Facts the pages state", which lists every setting, default, node requirement, limit, failure mode, and runtime-facing rule of this design under IC D13. "Labels from other designs" gains AR, "Terms" gains the store fill, and "Instructions for reviewers of other designs" is restated for revision 76.
- Revision 77, dated 2026-11-13, applies the fix pass that followed the cross-design review of that day. It changes these rules:
  - The per-slot `delivery: file` volume is `slot-<n>-netcred`, the name [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md) owns in PL-5, in place of `slot-<n>-netagent-creds`.
  - `lenny-store-fill` writes the stores and pristine trees with the adapter's GID as the group and a umask of `0002`, so directories are `0775` and object files `0444`; slots read through the other class and the adapter deletes a withheld store through the group class, as PL-5 states. "Terms", decision 61's conditions, "Validation still owed", and the Tier 4 test state it.
  - `netagent` reports whether slot attribution answers before the pod reports READY, so the pool's status is readable at bind; a pool with no recorded result counts as union scope at the post-chain point; a new pod whose `netagent` finds no reader on a pool whose status records attribution fails readiness and is replaced, with the pool's status unchanged; and the fallback is reported as `sessionIsolationLevel.perSlotScopes`, the name the posture record of `0142-security-oversight` uses. "Per-slot scopes (decision 60)", the failure modes of "Facts the pages state", and the Tier 1 test state it.
  - [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) LU-4 removes direct delivery mode, so P3 states that provider keys on LLM upstream records reach a provider only from the gateway; the LLM host reservation covers the endpoint hosts of upstream records (LUR-11); the `deliveryMode: direct` pairing leaves "Consolidating `provider-direct`" and decision 1; the long-stream scenario becomes a call outside the LLM proxy; the residual on pools with `egressProfile: internet` names the session's proxy token bounded by its credit budgets; "Delivery modes" states that a binding on a provider host needs no grant; `credential.leased` and the Token Service readiness text name VCS leases; and `egressCIDRs.providers` leaves the settings list. "Labels from other designs" gains LU.
  - The network descriptor's per-session binding entries move to a binding list on each slot's own credentials volume, `/run/netagent/credentials/bindings.json`, so a slot no longer reads the binding names and hosts of the other slots' sessions in the pod-wide `/run/netagent/network.json`.
  - `netagent` names the attributed session on each CONNECT in `x-lenny-session`, which it strips from agent input; `lenny-egress-authz` evaluates that session's formula, refuses a session the pod's binding does not list, and keys its decision cache on the session, so one slot's allow never serves another slot. Level 2 proxies pass the header.
  - The chart values `netagent.resources` and `storeFill.resources` are named in "Facts the pages state", with defaults not yet set. The mirror's `global.devMode` relaxation is a pointer to PM-8. The runtime capability example `credentialRotation` is replaced by `midSessionUpload`, and "Runtime SDKs", "Current state", notes what AR and LU-4 remove.
  - The final round of the same review changes no rule. "Facts the pages state" cites the `storeFill.resources` defaults that `0118-agent-pod-layout` revision 4 proposes, a request of 500m CPU and 512 MiB with a 1 GiB memory limit, and states that the `netagent.resources` defaults are not yet set and wait on the owner, which the status line repeats. The Tier 4 test **Mirror fill causes** selects a fill from a pod with several slots at the attributed session's environment, and at E = ∅ only on a pool whose status records slot attribution as unavailable, as **Fill causes** and decision 60 state.
- Revision 78, dated 2026-11-13, applies the check pass that followed the fix pass of the cross-design review of that day, under the orchestrator's resolutions of the check's items. It changes these rules:
  - Every per-session value lives on the slot's own `slot-<n>-netcred`: the binding list carries the session launch variables and the applied coordination generation, which `netagent` also sends to the adapter in an `applied` message on `CH-FETCHGRANT`; the pod-wide git configuration includes `/run/netagent/credentials/gitconfig`, which each slot resolves on its own mount and which carries the session's commit identity and the `Lenny-Session` commit template, applying the open request of `0152-user-git-identity` for UG-3; a `federation` binding's file variable and Azure's identifiers are session launch variables that the supervisor sets at launch, which lifts the earlier limit of Azure `federation` to tenant-owned pools; `NETRC` is set on every pod to a file on the slot's volume that is empty without a Basic binding; and a binding has no placeholder of its own, the profile's constant `lenny-injected` being the only one. "Injected configuration", "Delivery modes", "Git configuration", the scenarios, the SDK accessor, "Facts the pages state", and the Tier 1 and Tier 2 tests follow.
  - The refusal of a connection whose owner neither reader finds, and the refusal of a CONNECT naming no listed session, apply only on a pool whose status records per-slot attribution. **Fallback pools.** is new: the attribution status is set from a pod's first start report and cleared when the runtime class or the `netagent` image changes, an `onRequest` or `poolAttach` binding is refused at the post-chain point with `ENVIRONMENT_POLICY_NOT_ENFORCEABLE` rather than admitted with no credential, a `scope: always` binding applies pod-wide, a pod with no reader on an attributing pool is replaced under backoff with the `NetagentAttributionUnavailable` alert, and a fetch grant names the pod. The carrier of the start report to the controller is owed to EG-3.
  - `lenny-store-fill` runs an explicit mode pass over each pristine tree (directories `0775`, files `0444`, executables `0555`), and the adapter deletes a withheld store and its pristine tree with `openat2` and `unlinkat` under `RESOLVE_NO_SYMLINKS | RESOLVE_BENEATH | RESOLVE_NO_XDEV`, reading no repository data; the helper's first run for the session, rather than the adapter, removes the staged tree in the slot. "Credentials", **The gateway runs no git.**, states that `lenny-store-fill`, rather than the adapter, fills the stores.
  - `netagent`'s private emptyDir, its pod-wide volume, and each `slot-<n>-netcred` are memory-backed with proposed sizes of 16 MiB, 16 MiB, and 4 MiB, which wait on the owner with `netagent.resources`. "Validation still owed" adds the store fill's 1 GiB memory limit against the largest store and pristine tree a pool admits.
  - Wording: the adapter mounts `/run/netagent` read-only, as the Tier 2 test and [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md) state, and the test names `lenny-store-fill` among its readers; the removed tenant credential policy leaves the list of per-tenant override layers; and the `reference/metrics.md` row removes the direct-mode wording; the status line names `0151-user-credential-delivery` revision 30 among the revisions this design reads against.
- Revision 79, dated 2026-11-13, applies the last round of the cross-design review of that day. It changes this rule:
  - Each `slot-<n>-netcred` carries a `sizeLimit` of 8 MiB, which PL-5 of [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md) sets because that design owns the volume sizes, in place of the 4 MiB revision 78 proposed. `netagent`'s private emptyDir and its pod-wide volume keep their proposed 16 MiB each, which wait on the owner with `netagent.resources`. "The `netagent` container", "Delivery modes", the Tier 2 test, and the status line follow.
  - Wording fixes, which change no rule: `/run/netagent` is mounted read-write by `netagent`, read-only by every slot container, the adapter, and `lenny-store-fill`, and by no other container, as PL-3 and PL-5 render it, which the Tier 2 test states; the `lenny-store-fill` boundary of the Tier 9 tests states that the adapter's deletion of a withheld store follows no symbolic link, under SI-8; and the designs this one reads against are cited at their current revisions.
- Revision 80, dated 2026-11-13, applies the owner's answers of that day to the consolidated list of open items across the designs, and changes these rules:
  - `lenny-store-fill` writes the stores and pristine trees with a dedicated store group as the file group in place of the adapter's GID. The adapter holds that group as a supplementary group only, no slot container holds it, every agent-pod container runs with `allowPrivilegeEscalation: false`, and the mode pass clears setuid and setgid bits. "Terms", decision 61, "Instructions for reviewers of other designs", and the Tier 4 and Tier 9 tests state it, and "Validation still owed" adds the hostile-repository stress suite on runc, gVisor, and Kata, which Tier 9 runs. On Kata `block-plain`, the fill sets each store's directory and its pristine tree's directory to the store group before writing, as PL-5 now states, in place of the pool's `fsGroup`.
  - A connection whose socket owner is the adapter UID or `netagent`'s UID is a platform connection: `netagent` marks its CONNECT with `x-lenny-platform`, strips that header from agent input, and `lenny-egress-authz` admits a marked CONNECT only to the gateway, object storage, and the git mirror (decision 64). On a pool with per-slot attribution, a CONNECT that names no listed session is refused unless it carries the marker.
  - The session's git configuration file includes one identity file per VCS pool under `includeIf "hasconfig:remote.*.url:<pattern>"`, so a repository takes the author and committer of the pool whose repository prefix matches one of its remotes (decision 65). "Push limits and commit identity", "VCS pool fields", "Facts the pages state", and the Tier 1 test follow.
  - `netagent.resources` defaults to a request of 100m CPU and 128 MiB with a 512 MiB memory limit and no CPU limit, `netagent`'s private and pod-wide volumes take 16 MiB each, and `gitMirror.heldUidBudget` defaults to a quarter of the mirror's fetch-UID range (decision 66), each with an owed validation. "Facts the pages state" also cites the 8 MiB of each `slot-<n>-netcred`, which it still gave as 4 MiB, and the `storeFill.resources` defaults as confirmed.
  - A pool write that names `deliveryMode` is refused with `400 VALIDATION_ERROR` naming the field, and "Facts the pages state" lists under `ENVIRONMENT_POLICY_NOT_ENFORCEABLE` the refusal of `0147-tenant-derived-runtimes` decision 41. The settlement of `0151-user-credential-delivery` decision 41 and `0152-user-git-identity` decision 52 is recorded under "Decisions", with the owner's request for a spike on which runtime classes answer attribution.
- Revision 82, dated 2026-11-14, applies decision 67, the owner's model for warm stores, and changes these rules. A `gitRepository` entry of `sharedAssets` is the only declaration of a store on the warm list, and the warm list's declarers are a Runtime's public entries and a tenant-derived runtime's `gitRepository` assets, which replace warm default clones, cache-only entries, and private entries on a tenant-owned pool's row. Decision 63's target `path` becomes `prestage: true`, and a staged tree is adopted by the first `gitClone` source of its repository in the session's workspace at that source's `path`. An operator runtime may carry default clones. "Terms" (Warm list, Warm-up checkout), the default clone bullet under "Ref resolution", the 0111 rewrite list, the warm fill scenario, the specification list, proposal 5, the documentation list, and the store fill test state it.
- Revision 81, dated 2026-11-13, follows `0118-agent-pod-layout` revision 8, and changes these rules:
  - The store group is the `runAsGroup` of both the adapter and `lenny-store-fill`, in place of a supplementary group of the adapter, because Kubernetes has no container-level supplementary groups; `lenny-pod-security` refuses it as the `runAsGroup` or `runAsUser` of every other container, ephemeral containers included, and in `supplementalGroups`, and decision 61 states why sharing the adapter's primary group with the fill exposes nothing else. "Terms", decision 61, the Tier 4 test, "Validation still owed", and "Instructions for reviewers of other designs" follow. The refinement is the orchestrator's, after the owner's answer, and it was for the owner's review until the owner confirmed it on 2026-11-14 (revision 85).
  - The git helper's tree-size backstop names the disk watch's threshold as the chart value `adapter.diskWatchThresholdPercent`, 90% by default, under the owner's "7: yes" of 2026-11-13.
- Revision 83, dated 2026-11-14, applies the AWS spike of 2026-11-14 on slot attribution, whose report is in `changes/0118-agent-pod-layout/evidence/attribution-and-quota-spike/`, and decision 68, the owner's answer of the same day, "the deployer guide would recommend building it with CONFIG_INET_DIAG and CONFIG_INET_TCP_DIAG". It changes these rules:
  - "Slot attribution and fetch grants" records which reader each runtime answers: runc answers `sock_diag`, gVisor with its own network stack or host networking and Kata with either volume mode answer only `/proc/net/tcp`, and gVisor with host networking has no `/proc/net/tcp6`. The runtime-class question of decision 60 and of the owner's request of 2026-11-13 is measured, and no measured runtime class is a fallback pool.
  - `netagent` accepts a reader's answer only for a socket in the state `ESTABLISHED` whose owner UID is one it was configured with, and refuses every other answer, because a client that closed first leaves a `FIN_WAIT2` socket that both readers report with UID 0.
  - Without `sock_diag`, `netagent` reads `/proc/net/tcp` once for every accept that completed before the read started, in place of one read per accept.
  - `netagent`'s listeners bind `127.0.0.1` alone on every runtime.
  - The start-up attribution check opens a connection from `netagent`'s own UID and verifies each reader against it, and `netagent` uses only the readers that pass.
  - The deployer guide recommends building the Kata guest kernel with `CONFIG_INET_DIAG` and `CONFIG_INET_TCP_DIAG`, as a recommendation rather than a requirement (decision 68).
  - "Slot attribution and fetch grants" records that a slot under gVisor or Kata can create a nested user namespace, which runc's RuntimeDefault seccomp profile refuses, and that both readers still report the slot's UID, and that a passed socket keeps its creator's UID.
  - The binding paragraph's fallback example, the node requirements of "Facts the pages state", the Tier 1, Tier 5, and Tier 6 tests, "Validation still owed", the settlement of `0151-user-credential-delivery` decision 41 and `0152-user-git-identity` decision 52, and "Instructions for reviewers of other designs" follow. A user-namespace lookup that reports the UIDs inside the pod is retagged **[measured]**.
- Revision 84, dated 2026-11-14, follows `0147-tenant-derived-runtimes` revision 73, which applies the independent review of the question 17 write-up, and changes this rule. A `gitRepository` asset of a tenant-derived runtime records its declarer from the role that wrote it, so an asset a `platform-admin` writes is `platform`-declared and keeps the protections of that design's decision 35 on the tenant-owned pool ("Terms", Warm list). Wording, which changes no rule: the instruction to `0153-workspace-materialization` under "Instructions for reviewers of other designs" names `prestage: true`, decision 67, adoption by the first source of the repository, and the visibility of an unadopted tree, and "Warm-up checkout and the reflink rebuild" names the entries with `prestage: true` in place of "a known clone". The status line's reads-against list names the current revision of each cited design.
- Revision 85, dated 2026-11-14, records the owner's confirmation of that day, in the words "Ok on the rest", of revision 81's refinement of decision 61: the store group is the primary group (`runAsGroup`) of both the adapter and `lenny-store-fill`, with the pod under `supplementalGroupsPolicy: Strict`. Decision 61 and revision 81's note record it. It changes no rule. The status line's reads-against list names the current revision of each cited design.
- Revision 87, dated 2026-11-15, applies the owner's decision of that day to remove connectors, in the owner's words "Yes, let's drop connectors.", with the owner's reasoning, "An operator could build connectors outside of Lenny and just use egress to talk to its own 'connector' service.", and the additions that [`0124-connector-removal`](../0124-connector-removal/change.md) revision 2 requests of this design. It changes these rules:
  - "Credential bindings" gains the `identityAssertion` secret kind, under `delivery: inject` alone, with `lifetimeSeconds` (300 by default, from 60 to 900), the default `attach` header `Authorization: Bearer {{secret}}`, and the O5 exception to one `inject` binding per host. "Delivery modes" gains **Identity assertions.**, and its `federation` part gains the identity issuer `https://<lenny>/identity/<tenant-id>` with its separate key set, the deployment key set at `https://<lenny>/identity/.well-known/jwks.json` with `kid` and a `tenant` member on each key, their rotation, and the reserved `/identity/` ingress path (CR-2 and D8).
  - "Slot attribution and fetch grants" gains **The egress halt (CR-4).**: `HaltSessionEgress`, `CH-EGRESSHALT` on `@lenny-netagent-halt` from the adapter UID alone, `halt_slot`, `halted`, and `resume_slot`, the halts sent again after a `netagent` restart, and no halt on a fallback pool (O9). **Fallback pools** refuses an `identityAssertion` binding with `422 IDENTITY_ASSERTION_UNSUPPORTED_POD` (O7).
  - "Evidence streams" records each identity assertion in `credential.mint` with its `jti`.
  - "Injected configuration" gains **Harness clients.**, which cites the spike's per-client table as the evidence for the profile, keeps both `NODE_EXTRA_CA_CERTS` and `SSL_CERT_FILE` because Claude Code reads only the first and Codex only the second or `CODEX_CA_CERTIFICATE`, states that Codex does not use `HTTP_PROXY` for https URLs, and records `CODEX_CA_CERTIFICATE` as optional.
  - "Residual risks and limits" replaces **MCP connector calls.** with **Operator services.**, under which a runtime's harness reaches every operator service through egress (CR-3), and adds **Harness traffic outside the MCP path.**, on the `codex mcp list` probe and the vendor hosts the harnesses contact at start. "Exfiltration controls", **Bytes out.**, follows.
  - The §9.3 precedent under "Scopes and who manages them", the `outboundConnector` kind under "Tenant outbound connector", and the recorded decision on tenant-registered public services become pointers to CR-1. The terms, the labels, "Specification sections affected", the cross-design dependencies of CR-B and CR-C, "Documentation", "Facts the pages state", the tests at tiers 1, 4, 5, 8, and 9, and "Validation still owed" follow. The status line reads against the removal design's revision 2.
- Revision 86, dated 2026-11-14, applies the documentation instruction of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md) and changes no rule: the documentation rows that named `api/mcp.md` name the MCP adapter's documentation, `/mcp/management` is unaffected, and the error-catalog item names the REST pages. The status line's reads-against list names the current revision of each cited design.
- Revision 88, dated 2026-11-15, follows [`0124-connector-removal`](../0124-connector-removal/change.md) revision 3, the final consistency pass of the connector removal under the owner's decision "Yes, let's drop connectors.", and changes this rule. To "Add a setting to turn off the deployment-wide key set, which lets anyone who reaches it list every tenant's identifier?", the owner answered "yes" on 2026-11-15 (D9 of that design). **Identity issuer and key sets.** under "Delivery modes" gains the Helm value `tokenService.identityAssertion.deploymentKeySet.enabled`, `true` by default: with `false`, the deployment key set route answers `404`, rotation and tenant deletion act on the tenant key sets alone, a service that serves every tenant verifies through the issuer-prefix method, and a service still configured with the deployment key set URL fails closed. The disclosure sentence names turning the set off beside an internal address. The terms, "Specification sections affected", "Facts the pages state", and the tier 4 compose test follow.
- Revision 90, dated 2026-11-15, follows [`0124-connector-removal`](../0124-connector-removal/change.md) revision 5, which records the owner's answer of that day, "yes to refusal", as its D11, and changes this rule. An `identityAssertion` binding is refused with `422 IDENTITY_ASSERTION_UNSUPPORTED_POD` on recycled pods and on pods that have served a session, as `0151-user-credential-delivery` UC-3 refuses a `userCredential` binding there, on the ground of the adversarial review in [`evidence/connector-removal-design/decision52-review.md`](../0124-connector-removal/evidence/decision52-review.md): the assertion lets a service act as the user, and a `netagent`, adapter, or sandbox kernel compromised by an earlier session on the pod could take later sessions' assertions. **Identity assertions.** under "Delivery modes" restates **Pods** and its child sentence, **Fallback pools** names the other refusals, and the label row, the failure modes and the credential bindings row under "Documentation", and the tier 5 test follow. O7's refusal on fallback pools is unchanged. The status line reads against the removal design's revision 5 and `0151-user-credential-delivery` revision 38.
- Revision 89, dated 2026-11-15, follows [`0124-connector-removal`](../0124-connector-removal/change.md) revision 4, which records the owner's answer of that day to its O10, "refusal on both (I changed my mind)". O10 had lifted the refusals of a `userCredential` binding under `delivery: inject` on recycled and used pods, and the owner withdrew it, so [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) decision 41 stands for both deliveries. The label row for the removal design no longer names O10. This design stated no rule of its own on it, so no rule changes. The status line reads against the removal design's revision 4, `0151-user-credential-delivery` revision 37, and `0152-user-git-identity` revision 43.
- Revision 91, dated 2026-11-15, follows [`0143-service-account-credentials`](../0143-service-account-credentials/change.md) revision 1, which records the owner's answers of that day on service accounts as its D1 to D3. It changes these rules: the `userCredential` kind of the `secret` union delivers the token of the session's principal, a person or a service account; the **Gates** paragraph states `userCredentialDelivery` as the class of a principal's token, with `metadata` of a service account's AWS credentials under that design's O6; a binding's name matches `^[a-z][a-z0-9-]{0,62}$`; the federation key set signs with RS256 and also signs that design's exchange JWTs, and nothing else; **Exchange by the Token Service.** relates `federation` to `issuerExchange`; the mint path carries the token, discovery, and exchange endpoints of the new sources, and the decision service honours the tenant assertion for them; and "Reserved for other designs" gains both hooks. The status line reads against that design's revision 1, `0151-user-credential-delivery` revision 39, `0124-connector-removal` revision 6, and `0152-user-git-identity` revision 44.
- Revision 92, dated 2026-11-15, follows [`0143-service-account-credentials`](../0143-service-account-credentials/change.md) revision 2, which applies the security review of that design's revision 1 and the owner's answer of that day, "O1-O7: go with your recommendations". Under that design's O1, exchange JWTs move to an exchange issuer of their own, `https://<lenny>/exchange/<tenant-id>`, which reverses this design's revision 91 statement that the federation key set also signs them. The federation key set signs federation tokens alone again. It keeps RS256, which revision 91 introduced, because `federation` serves the Azure SDKs at Entra, which tests only RS256 issuers, and the round 4 validation signed with RS256. A new federation key is published `tokenService.federationIssuer.prePublishSeconds`, 86,400 by default, before the Token Service signs with it, after that review's finding 9. **Exchange by the Token Service.** describes the exchange issuer. The **Gates** paragraph adds `userCredentialMetadataDelivery` under that design's O6, and "Reserved for other designs" gains the `/exchange/` path and that grant. The binding name rule stays, now stated as keeping a federation subject to three parts. The status line reads against that design's revision 2, `0151-user-credential-delivery` revision 40, `0124-connector-removal` revision 7, and `0152-user-git-identity` revision 45.
- Revision 93, dated 2026-11-16, applies the kind and field names of [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md) revision 3, which the owner accepted on 2026-11-16 as its O3: `AgentPod` replaces `Sandbox`, the pool table is `pools`, a pod's warm list follows the revision its `PodSet` names, and the controller package is `pkg/controller/agentpod`. The documentation table loses its `adr/` row, because the owner decided on 2026-11-16 to remove ADRs from Lenny, as [`0125-decision-records`](../0125-decision-records/change.md) records. No rule changes. The status line's reads-against list names the revision this pass gives each design it edits.

# Review record

**Review of revision 50.** A stop check found no Major or higher issue and 13 Minors, all of which held, and it traced the last round's changes rather than reading end to end. The pool key and closure, and `netagent` asking I8 before a 401, are retagged **[design]**. A route write closes pooled upstream connections to the hosts it changes, the push list gains the platform default and target writes, the decision's git list omits `direct` and mirror routes, and `default` names the platform default.

**Review of revision 49.** A final regression review and a further run raised these on this document, each verified against the text and the raw output.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| N1, route lists | Holds. | Reduced to calls carrying a user token or a client secret, with a fall-through and a named list. The refusal of an unasserted Token Service CONNECT is removed, because it cannot be expressed by host. |
| N2, provisional clone | Holds. | The test row, and the mint refusal's exception in `0151-user-credential-delivery`. |
| N3, suspended binding | Holds. | The hook row and test except the qualification. |
| Run: pooled connections | Holds. | The pool key holds the list, the route digest, and the generation. |
| Run: per-object `error` | Stopped git-lfs 3.6.0. | Behaviour stated, including clone's output naming no host. |
| Run: 3-failure refusal | It caps transfers on a 401, resets per batch, and never fires on a 500. | **Kept, with its limit stated,** because a batch-time `error` cannot cover a deliverable transfer a store refuses. |
| Minors 1, 2, 3, 7, 10, and 11 | Hold. | Corrected. |

None was rejected. An unasserted CONNECT on a host with both lists naming different targets keeps the existing first-match lookup, which is older than this mechanism, and no documented topology has it.

**Review of revision 48.** Round 5 of the reviews and the revision 5 run raised these on this document, each verified against the text and the raw output.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| Security M1, asset hosts on `restricted` pools | Holds. | Every host of the formula is covered for that pool's pods. The `file` rule keeps reading tenant pools and routes alone. |
| Security Minor 1, the unasserted refusal blocks App mints | Holds. | The Token Service asserts on every CONNECT, from current configuration where no record applies. |
| Run: a stripped action on another host made git-lfs loop | Holds, at 28,896 `PUT`s in 8 s on one machine. | A per-object `error` replaces that action, and repeated failures are refused **[design]**. |
| Run: `Private-Token` in an action reached the client | Holds. | Every credential header name is removed. |
| Sync M1, LFS on a connector route | Holds. | Level 1's sentence is qualified, and `0148-tenant-outbound-connector` and `0152-user-git-identity` follow. |
| Sync M2 and Minor 7 | Hold. | The assertion names its list. |
| Security Minors 4, 5, 8, 9, 10, 11, 15 | Hold. | First I7 answer, wait wording, LFS text, the override list on every terminated host, the stated false positives, `inject` hosts, and pinned tunnels. |
| Sync m1, m6, m7, m8, m10, m11, m13, m17 | Hold. | Corrected. m17: each document's own table governs. |

None was rejected.

**Earlier statement corrected.** Revision 48 owed a live GitHub LFS run on a concern, held from memory, that GitHub's actions set `Authorization` for a storage host. One live anonymous download action has no `header` key, on `github-cloud.githubusercontent.com` **[live]**, so that concern does not hold for that action. An upload, a request with a token, and a transfer have not been read, and only those stay owed.

**Revision 5 run folded in.** 139 of 139 git path rows, the CONNECT gate, the marker on a stand-in, and GitLab's LFS form on a stub. The stub proves forwarding only.

**Review of revision 47.** The first full adversarial read of this document since revision 35, covering T, and round 4 of the other reviews raised these, each verified against the text, the prototype, and GitLab's public source.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| H1, an LFS batch response gives the agent a credential | Holds **[source]**. The stub's rows did not reach it. | Batch responses are parsed, `Authorization` is held and attached on the matching transfer, transfer paths are data, the scrub applies on the git path, and `info/lfs` is refused on `mint-coarse` and `stored` providers until a live GitLab run passes. |
| M1, fail-closed rules only in the prototype | Holds. | Normative under "Git on covered hosts", and the tier 1 row is corrected. |
| M2, stale coverage opens a tunnel | Holds. | The marker for covered hosts moves to EG-3, with its test, CONNECTs wait for the first generation, the wait moves before materialization, writes push, and EG-12's sentence is corrected. |
| M3, a bound host with no pool | Holds. | Classification on every terminated host. A push there is refused, and the smaller of the two options was taken: a tenant wanting one adds a pool. |
| M4, coverage keyed on the name | Holds. | The alias limit, alias data, the server-name check on `*`, and the canonicalizer. |
| M5, workflow control | Holds. | A capability per provider, refused at pool write elsewhere. |
| Sync M2, covered set under environments | Holds. | Read unfiltered. |
| Sync M3, source of the covered set | Holds. | I8 fails for warm and public-only pods, which make no I8 call, so it stays on I7, named in the row and the test. **The coordinator's reading that the marker covers a compromised decision service removing coverage is rejected:** that service evaluates the marker too. The bound is stated instead. |
| Sync M4 | Holds. | UC-5 owns it, with a `handling` value and a mint refusal. |
| R4-4 and m15 | Hold. | The level 2 contract row and the unasserted refusal. |
| R4-6 | Holds. | A provider in `deleting` still declares its endpoints. |
| Sync m2, m4, m5, m16, m18, m19, m20 | Hold. | Corrected. |
| Minors 1 to 11 | Hold. | Corrected. Minor 9: a pod with no tenant fills through the mirror's public store when the deployment runs it. Minor 10: an asset's host stops counting after bind. Minor 6: one retry window. |

None was rejected but the clause above.

**Review of revision 46.** Round 3 of the reviews raised these on this document, each verified against the text and the tree.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| R3-2, the route is chosen after the comparison | Holds. A route write takes effect at the next decision, ahead of a flush. | The caller asserts the record's route target on the CONNECT, and the decision service refuses a disagreement. This replaces revision 46's I8 comparison in `netagent`. |
| Sync M2, `netagent` has no route to compare | Holds, against that comparison. | The assertion removes the comparison. **Its I7 addition is not needed and was not made.** The I8 row gains the expected route and CA. |
| R3-7 and Sync M1, stored `apiFlavour` | Holds. A tenant admin could set a flavour whose rules match nothing. | The field is dropped, and flavour derives from provider and host. Any non-`github.com`, non-`*.ghe.com` GitHub host is GHES with no stored input, so N1 stays closed. |
| Sync M3 and M4 | Hold. | UG-1 depends on UC-1 here and EG-2 in both documents. |
| m1 and m5 | Hold. | The stale text, the settings paragraph, and the `internet` sentence. |
| m10, level 1 | Holds. | Stated under "Credentials". |
| R3 minor, `tenantOverride.caBundle` | Holds. | `inject` validation excludes it. |

None was rejected.

**Owner's decision applied.** Decision 44 is settled as T. "Local git endpoint" became "Git on covered hosts". **Redirects**, **LFS**, "Write scope", the adapter clone, **What the limit covers**, "Git configuration", "Fixed endpoints", the trust text, warm fills, the interfaces, EG-3, tests, and validation owed follow. Port 3129 and the adapter's Unix socket are gone. Entries below that name the local git endpoint record the design at that time.

**Review of revision 45.** Two reviews of the user credential designs and the spike raised these on this document, each verified against the text and the tree.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| N1, no input selects the flavour | Holds. `github-enterprise` appears in no tree or spec text. | A stored `apiFlavour` on the pool, in a hook row. §14's lease scopes stay as they are. |
| N4 and M6, the fix dropped a refusal | Holds. | A stored token is refused under either delivery, with the one exception, and on a `required` pool's API host. |
| N7, a binding with rules loses the preset | Holds. | The default applies unless `preset: none`. |
| N14, unknown state keeps reads | Holds. | Stated in the hook row. |
| N16, two routes to a tenant endpoint | Holds. | Link provider endpoints go through `lenny-egress`. |
| N12, N13, N18, N24, N27, N32, N33, and N34 | Hold. | The hook rows, both formulas, the granted-entries rule as normative text, the table, the tests, and headers in the stage. |
| H1, route plus CA | Holds, against the other design. | Route target and CA digest on I8. No CA on a `userCredential` binding. |
| M4, lock escape | Holds. | The lock persists for descendants. |

None was rejected. The git mechanism text is unchanged. Decision 44 records T as recommended, with the text changes listed in `0152-user-git-identity`, and none is applied.

**Review of revision 44.** Two independent reviews of the user credential designs raised these on this document, each verified against the text.

| Finding | Outcome | Resolution |
| :-- | :-- | :-- |
| The stored token refusal has no delivery qualifier, against a `file` binding the other designs allow | Holds | Limited to `inject`, with the `file` rule in `0151-user-credential-delivery`. |
| The configuration table still states the old gate | Holds | Both rows corrected. |
| `lockEgressToHosts` is absent from the formula | Holds | Added to the one-session formula, and refused on `internet` pools. |
| The implicit attach has no `scope` | Holds | `poolAttach`, defined in `0152-user-git-identity`. |
| `suspendedBy` names only a guardrail | Holds | It also names a withdrawn gate or a pin. |
| No proposal owns the pin wiring | Holds | EG-12 takes a `required` VCS pool as a source, and UG-2 carries the wiring. |
| The GitLab scenario omits the preset and remotes | Holds | Corrected. |
| The cross-design table omits proposals | Holds | Added. |

The git mechanism text is unchanged, and its decision stays pending.

Revision 4 went through two adversarial reviews on 2026-11-02, one for security and one for design substance, plus a fact-check against the specification and the code. Revision 5 records how each finding above Low or Minor was resolved. Revision 5 then went through a second round of both reviews, and revision 6 records those resolutions in the second table. Revision 6 went through a third round, recorded in the third table, revision 7 a fourth, recorded in the fourth, revision 8 a fifth, recorded in the fifth, revision 9 a sixth, recorded in the sixth, revision 10 a seventh, recorded in the seventh, revision 11 an eighth, recorded in the eighth, revision 12 a ninth, recorded in the ninth, and revision 13 a tenth, confirming round, recorded in the tenth. Findings the reviews rated Low or Minor were folded into the sections they concern without a row here.

| Finding | Resolution in revision 5 |
| :-- | :-- |
| Tenant-controlled DNS could reach another tenant's SSRF-exempted private address through Envoy's resolver (High) | Private destinations are pinned by the decision and never resolved; the address filter has no exemptions; tenant-registered public services are resolved and pinned at registration and re-checked ("Egress decision service", **Private destinations**). |
| One deployment-wide federation issuer made P4 depend on each tenant's trust policy, and AWS cannot condition on a tenant claim (High) | One issuer URL per tenant, a stable subject, a dedicated key set, and source-address binding where the provider supports it ("Credential delivery to runtimes", **`federation`**). |
| `inject` attached the secret by CONNECT authority, so a request with another `Host` on a shared edge carried it to an attacker (High) | The attach check is on the decrypted `Host`, upstream SNI is pinned, and upstream certificates never validate against the per-pod CA. |
| A tenant route could fill the shared public mirror store (High) | Store identity includes route and credential; only anonymous fetches through the platform default route share a store; refresh is bounded ("Git mirror"). |
| The per-pod CA was an unconstrained root and `netagent` wrote the whole bundle (Medium) | Name constraints to bound hosts; the controller assembles the bundle and `netagent` appends only its CA; the admission webhooks cover `netagent`'s volumes ("TLS interception", **Bundle**; "The `netagent` container"). |
| The Token Service trusted the decision service's assertion (Medium) | A second projected token with audience `lenny-token-service` is forwarded on I7 and verified by the Token Service, which re-derives the binding. |
| One pod could fail every tenant closed through shared Envoy limits (Medium) | P7 and per-pod and per-tenant quotas in the decision service; I3 admitted from Lenny proxies only; I7 on its own rate-limited port. |
| Verbatim tenant files and templates bypassed the `file` gate (Medium) | Verbatim files need the same gate; templates reference no secrets. |
| Write scope was undefined on concurrent-session pools (Medium) | The union over the pod's sessions, stated under "Local git endpoint". |
| Level 1 static credentials were a cross-tenant shared secret (Medium) | Level 1 deferred (open decision 12). |
| Service account token replay from another source (Medium) | Peer-address check on I3, I5, and I7; `expirationSeconds` 600. |
| Agent-supplied headers crossed hops (Medium) | `netagent` forwards `Host` and its own `Proxy-Authorization` only; the decision overwrites the route header. |
| Launch variables captured the adapter's platform traffic in the embedded model, and `NO_PROXY` could not name presigned URLs (Major) | The adapter disables proxying on its platform transports; `NO_PROXY` is derived from the rendered endpoints ("Runtime SDKs", **Embedded runtimes**; "Injected configuration"). |
| The gateway-to-decision-service binding feed was unspecified, including the bind-then-clone race and §7.3 fencing (Major) | Postgres notifications, read-through on a miss, a stated bound, and the generation rule ("The `netagent` container", **Binding**). |
| `GIT_CONFIG_GLOBAL` on a read-only file broke `git config --global` and hid the image's config (Major) | Lenny's rules move to `GIT_CONFIG_SYSTEM`. |
| The "identity before claim" rationale overstated the adapter's position (Moderate) | The comparison table and the recommendation are corrected; two audiences are stated. |
| Egress groups were a second proxy tier with no contract (Moderate) | Groups are additional `lenny-egress` Deployments, and bound pods dial them directly. |
| Public mirror store classification and the public-to-private transition were undefined (Moderate) | Defined under "Git mirror". |
| LFS uploads bypassed write detection (Moderate) | `netagent` reads the batch `operation`. |
| Ref resolution in the pod changed the §14 contract and cost a claimed pod (Moderate) | Resolution stays in the gateway through a Go `ls-refs` client (open decision 16). |
| A deployment-wide tunnel cap let a dead `netagent`'s tunnel outlive a recycle (Moderate) | The cap is per route and selected per pool by the decision. |
| The `proxied` policy omitted `lenny-egress-authz` (Moderate) | Added, with the resolver. |
| `netagent` decisions were invisible centrally (Moderate) | Reported over I7 with decision identifiers. |
| Proposal 1 broke `gitClone` and proposal 2 depended on an unlanded draft (Moderate) | Proposal 1 keeps the stored `token`; pool ownership moves into proposal 2. |
| Policy edits did not invalidate the decision cache (Moderate) | A policy version in the cache key. |
| Tenant client config files needed pointer variables the design had removed (Moderate) | Fixed paths with constant pointer variables in the default profile. |

Second round, on revision 5:

| Finding | Resolution in revision 6 |
| :-- | :-- |
| Egress-group admission was asserted: no per-group policy, no way for the decision to know which Envoy it serves, and a path to leave from another tenant's address (High) | Grants are Helm values; the chart renders the group's ingress policy on the tenant label; each group's Envoy adds a fixed group header and the decision refuses mismatches in both directions ("Defaults, replacement, and augmentation"). |
| Postgres notifications can be spoofed by any database client and lost on reconnect, and the pooler the spec mandates does not carry them (High, Major) | The feed is a gateway gRPC push on the gateway's mTLS identity, treated as a hint; the decision re-reads the row on every push, on every miss, and at a bounded age; generations are monotonic ("The `netagent` container", **Binding**). |
| The gateway's `ls-refs` resolver ran with no policy, followed a client-supplied URL, and attached credentials on wildcard host matches (High) | The decision applies the creating tenant's policy and routes to the gateway's request; the credential attaches within a repository prefix only; no redirects, `ref-prefix`, byte and time caps, strict parsing, and per-tenant concurrency ("Credentials", **The gateway runs no git**). |
| A resumed pod has no published binding while it materializes, so a resumed session with `gitClone` could not clone (Major) | A provisional binding written before `FinalizeWorkspace`, as a §7.3 change in proposal 2. |
| The controller cannot derive `NO_PROXY`, because it never receives the LLM proxy or OTLP hosts (Major) | The chart passes both to the controller as inputs; the adapter bypasses the proxy for presigned hosts; `netagent` logs a CONNECT to a base destination as a configuration error ("Injected configuration"). |
| Concurrent-tunnel quotas had no close signal (Medium, Moderate) | Rates centrally, per-pod concurrency in `netagent`, per-tenant concurrency from Envoy's gRPC access log stream, and a chart check that quotas fit Envoy's limits. |
| The per-route tunnel cap was static configuration while pools are created at runtime (Moderate) | A chart-rendered menu of routes that pools choose from. |
| Private-destination pinning rested on the forwarded-CONNECT form the spike tested, not raw tunnelling (Medium) | Both forms are stated; raw tunnelling is listed as owed; tenant routes name only held objects; the registration check covers the full exclusion list and the cluster's own addresses. |
| The `inject` authority check was ambiguous for absolute-form HTTP/1.1 requests (Medium) | One derived authority with its port; a disagreeing `Host` is refused. |
| Per-tenant issuers keyed by name, with unstated key lifecycle and reachability (Medium, Moderate) | Issuers keyed by tenant identifier; per-tenant keys destroyed with the tenant; rotation overlap; reserved `/oidc/` path; the public-ingress requirement stated. |
| One gate covered four risk classes deployment-wide (Medium) | Three gates, each a per-tenant platform grant plus a tenant opt-in. |
| The mirror parses hostile packs for every tenant in one process (Medium) | Sandboxed git subprocesses and optional sharding. |
| Egress-group grant changes did not re-point bound pods (Moderate) | The bounded binding re-read re-points `netagent`; open tunnels run to the pool cap, stated as residual. |
| `ls-refs` against a mirrored host lost the outage benefit and lacked a v0 fallback (Moderate) | Resolution against the mirror when the route is the mirror; v0 fallback. |
| Proposal 1 depended on proposal 2 through the resolver, and bundle assembly sat in the wrong proposal (Moderate) | The resolver moves to proposal 3; bundle assembly moves to proposal 2. |
| Tests and documentation still carried level 1 and lacked the new mechanisms (Moderate) | Updated. |

Third round, on revision 6:

| Finding | Resolution in revision 7 |
| :-- | :-- |
| The decision service relayed the pod's Token Service token, so a compromised decision service held a valid token for every active pod (High) | `netagent` requests credentials from the Token Service directly on I8, and the Token Service asks the decision service for scope the way Envoy does ("Egress decision service", **Credential requests**). |
| Pods could reach Envoy's unauthenticated admin interface and other non-token ports (High if unstated) | Port-scoped pod-side rules, Envoy admin on loopback, metrics on a scrape-only listener, and a tier 9 test (**Port scoping**). |
| Credentials attached by repository prefix without a canonical path form (Medium) | One shared canonicalizer for the resolver, session create, and `netagent`. |
| The provisional resume binding was a full binding with no stated retirement (Medium, Medium) | Read-scoped to `gitClone` sources, fenced on the generation, retired on every exit from `resuming` in the pod-release write. |
| Tenant-owned pool pods carried no tenant label, so a group-granted tenant's warm pods had no egress (Major) | The controller stamps the owner at creation, with a new label-webhook rule. |
| The binding push reached one decision-service replica (Major) | Fan-out to every replica through a headless Service, non-blocking with bounded retries, and the re-read as backstop. |
| `ls-refs` against the mirror failed for a repository it had never fetched, and the mirror had no gateway identity (Major) | The mirror forwards a bounded `ls-refs` upstream on a miss and schedules the fetch; it accepts the gateway's mTLS identity carrying the tenant. |
| Per-tenant concurrency from the access log stream drifted upward (Major) | Heartbeats from the periodic entries, expiry after three missed intervals, per-instance reset, an approximate quota with an alert, and the per-pod limit as the exact bound. |
| The LLM proxy host is a gateway flag no template renders, and a public hairpin fails on `proxied` pools (Medium) | One Helm value for both consumers, constrained to an in-cluster name, with a preflight check. |
| Gate wording, SSRF-exemption wording, the egress-group documentation row, and the gateway's `ls-refs` identity were inconsistent | Corrected throughout. |

Fourth round, on revision 7:

| Finding | Resolution in revision 8 |
| :-- | :-- |
| `netagent` learned a binding's hosts from the decision service, so a compromised decision service could point injection at an attacker's host (High) | The I8 response carries the binding's hosts and attach template from the Token Service's own store read, and `netagent` gates injection on that set; the Token Service enforces the writable set and allowlist itself, with the decision as a second gate ("Egress decision service", **Credential requests**). |
| I8 exposed the shared credential plane to every pod with no per-pod limit, no isolation from the gateway path, and no caching (High, Major) | An isolated, rate-limited TLS listener; `netagent` caches to expiry; credential calls join P7 (**The I8 listener**). |
| The Token Service's new footprint (pod watch, binding reads, ingress from agent namespaces, RBAC) was unstated (Major) | The decision service keeps the only pod watch and answers liveness on I3; binding resolution is one shared package; the ingress, egress, and RBAC changes are listed under "Specification sections affected". |
| The mirror had no stated identity or credential path for upstream fetches (Medium) | The mirror fetches on its own audience token with the store's tenant as policy context and requests credentials on I8 on its mTLS identity. |
| Token Service unavailability was covered for push only (Medium) | Fail-closed behaviour stated for every delivery mode, with cached credentials serving to expiry. |
| I7 resubscription on replica loss was unstated (Medium) | Resubscribe to any replica with the last generation, a 30-second tunnel grace. |
| A pool ownership change stranded labelled pods (Medium) | `ownerTenantId` is immutable once set. |
| I7 and I8 references, the diagram, the `proxied` peers, and the canonicalizer's proposal were inconsistent | Corrected. |

Fifth round, on revision 8:

| Finding | Resolution in revision 9 |
| :-- | :-- |
| The Token Service had no independent source of pod identity or tenant, so the second gate depended on the decision service (High, Major) | `pkg/binding` is Kubernetes-backed in both components, each with its own informer; tenant identity never comes from I3 ("Egress decision service", **Credential requests**). |
| The Token Service's I3 call could carry the pod's token (High) | The call carries the pod UID, the peer address, and the scope only. |
| The credential cache outlived the binding on recycled, service-mode, and concurrent pods (High) | The cache is keyed on binding generation and session and flushed on every binding change; stored secrets get a 15-minute cache lifetime. |
| I7 resubscription was exempt from the rate limit and read through the store (Major) | Matching-generation resubscriptions are served from cache, with their own low limit. |
| The mirror's tenant assertion on I2 and its I8 identity were unstated, and a compromised mirror could obtain every `stored` token (Medium) | An `x-lenny-tenant` header honoured only from service identities; read tokens only on I8; the parent attaches credentials on a loopback forwarder; `stored` repositories route `direct`. |
| Mirror store reads were single-gated on I3 (Medium) | The mirror resolves the caller's tenant itself and refuses a store outside it. |
| The `want`-line wait failed large first fetches (Medium) | `netagent` retries the mirror to the pool's fetch timeout, and materialization retries within its own. |
| Token Service loss was unstated for `ls-refs`, materialization, and warm fills (Medium) | Stated, each failing closed. |
| Stored secrets had no cache expiry (Medium) | A 15-minute cache lifetime assigned by the Token Service. |
| Level 1 statements survived its deferral; the architecture peer list and the Token Service store claim were stale | Corrected. |

Sixth round, on revision 9:

| Finding | Resolution in revision 10 |
| :-- | :-- |
| The mirror's sandbox did not bound a compromised git subprocess to its tenant: shared UID, readable identity material, a shared forwarder (High) | Per-fetch UIDs, identity material readable by the parent only, per-tenant store groups, and a per-fetch forwarder socket with a peer-credential check ("Git mirror"). |
| The `netagent` retry window equalled the finalize watchdog and exceeded the resume watchdog (Major) | A pool fetch retry window below both, 180 s by default, with admin prefetch and warm fill for large repositories, and new scope-table rows. |
| The `stored`-provider `direct` rule had one decision point and no counterpart in `netagent` or the resolver (Major) | One rule keyed on the pool's provider level, applied by `netagent`, the resolver, and the mirror; `mint-coarse` joins it; P2 and the mirror table are reworded. |
| The informers covered the agent namespaces only, while the service identities they check run in `lenny-system`; the mirror's own instance lacked RBAC (Medium) | Informers cover `lenny-system` too; the mirror carries the same RBAC and egress. |
| `pkg/binding` startup, outage, and scale behaviour were unstated (Medium) | Fail closed before sync, stale-cache bound on API server loss, a pod projection, and Token Service readiness kept independent. |
| The cache key missed binding definition edits (Medium) | The definition version is part of the key, and edits are pushed as binding changes. |
| The egress-group ingress policy omitted the mirror (Medium) | Admitted. |
| I3 and I8 rows and the I3 caller list were stale | Corrected. |

Seventh round, on revision 10:

| Finding | Resolution in revision 11 |
| :-- | :-- |
| Public stores shared one group, so a compromised fetch of a hostile public repository could rewrite every public store (High) | Per-store GIDs for public and tenant stores; a fetch holds only its store's group; public-store `upload-pack` runs with no group ("Git mirror", **Per-store ownership**). |
| Per-fetch UIDs needed capabilities every `lenny-system` workload drops, so the sandbox could not be built (High, Major) | A stated capability exception for the mirror container with a custom seccomp profile, §13.1 and §17 rows, a preflight allowlist, and user namespaces as the later replacement (**Capability posture**). |
| The retry-window check covered the finalize bound only, ignored the setup bound and the resume watchdog, and applied per source (Major) | One window per materialization, validated against both watchdogs less the setup bound. |
| The window-expired fetch reused a plan-bind error code (Medium) | It surfaces at finalize as `SESSION_CREATION_FAILED` with reason `git_fetch_pending`. |
| The Token Service released mirror read tokens on pool match alone (Medium) | The tenant's git route must also name the mirror for that host. |
| The forwarder pinned the credential but not the destination (Medium) | It pins the upstream host and repository path. |
| Projected tokens cannot renew without the API server, so all egress fails closed about 600 s into an outage (Medium) | Stated as a residual with an alert. |
| Namespace wording, the audience list, the mirror egress list, and the missing prefetch API were inconsistent | Corrected, and the prefetch resource is added to the scope table, §15, the documentation plan, and the tests. |

Eighth round, on revision 11:

| Finding | Resolution in revision 12 |
| :-- | :-- |
| Without `fsGroup` the mirror's I8 client key was either unreadable by the parent or readable by every fetch UID (High) | `fsGroup` stays with `fsGroupChangePolicy: OnRootMismatch`; the key is group-readable at 0440 by the parent only, because fetch UIDs hold only their store's group ("Git mirror", **Per-store ownership**). |
| Kubernetes never grants added capabilities to a non-root entrypoint, so the sandbox could not switch UIDs (Major) | A root entrypoint raises three ambient capabilities and drops to the parent UID; the exception covers the non-root rule as well, with a `baseline` Pod Security prerequisite and a preflight check that now covers StatefulSets (**Capability posture**). |
| The custom seccomp profile was unnecessary and had no shipping path (Major) | Removed; RuntimeDefault permits the calls. |
| The retry window ignored the resume rebuild's setup commands and the Runtime's setup bound (Major) | The window applies to initial materialization only, validated against the finalize bound less the Runtime's setup bound; the resume path waits 30 s at most. |
| Files written by a fetch UID were not writable by a later fetch, and a reused UID kept owner bits (Medium) | `core.sharedRepository=group` and a re-chown to the store owner after each fetch. |
| A tenant's own agent could poison its tenant store through `upload-pack` (Medium) | A per-store reader group through a POSIX default ACL, with the residual stated where ACLs are absent. |
| The `lenny-system` Pod Security level was unstated for the exception (Medium) | Stated as a prerequisite with a preflight row. |
| Prefetch had no storage bound; §6.2, the proposal assignment, and the Git access page were incomplete | Corrected. |

Ninth round, on revision 12:

| Finding | Resolution in revision 13 |
| :-- | :-- |
| A descendant left by a compromised fetch could pass the forwarder's peer check once its UID was reused for another tenant (High) | Fetch UIDs are never reused within the parent's lifetime, and every process under a UID is killed before the UID retires ("Git mirror", **Per-store ownership**). |
| The ambient set is cleared on a root-to-non-root switch, so the stated capability order carried nothing (Medium, Medium) | The order is keep-caps, switch with groups kept, restore, then raise, as `setpriv` does (**Capability posture**). |
| The post-fetch re-chown walked a tree the fetch controlled with `CAP_CHOWN` (Medium) | An fd-relative `lchown` walk that changes owners only, under the store's GID. |
| The validator's outcome on a Runtime update was unstated (Medium) | A Runtime update that breaks a referencing pool's window is refused, with the derived-runtime maximum and the §6.2 default stated. |
| Stale watchdog wording in the scope table and tier 8, §7.3 missing from the affected sections, and the Pod Security label gap | Corrected. |

Both ninth-round reviews judged the design ready to split into proposals once these corrections were made.

Tenth round, confirming, on revision 13:

| Finding | Resolution in revision 14 |
| :-- | :-- |
| The UID sweep was not tied to fetch exit, and the parent holds no `CAP_KILL` (High as written) | The sweep runs at fetch exit, before the re-chown and completion, through a helper that switches to the fetch UID and signals its own UID, with the parent as child subreaper ("Git mirror", **Per-store ownership**). |
| A stored-secret `inject` binding on a git host bypassed the `storedTokenPush` grant and path enforcement (Medium to High) | Such bindings are refused at create, and P2 names the stored-API-secret exception ("Credential bindings"). |
| The ambient raise needs the capability in the inheritable set (precision) | Stated in the capability order. |
| The §28 label range said I1 to I7 | Corrected to I8. |

Both tenth-round reviews found no Major or High issue beyond these and judged the design sound enough to proceed to proposals.

Revision 15 applied the owner's decisions and these corrections, which answering the owner's questions surfaced:

| Finding | Resolution in revision 15 |
| :-- | :-- |
| The doc said user namespaces remove the mirror's capability exception, and they relax only the non-root rules | Corrected, with their node requirements and ID range (**Capability posture**). |
| A mirror holding a repository answered `ls-refs` from its store, so `main` could resolve to an old commit | The mirror asks the upstream first and uses the store only when the upstream is unreachable. |
| The label the gateway stamps at bind could reach an informer after the pod's first request | A retryable not-yet-visible state in `pkg/binding`. |
| `ownerTenantId` left `unset → owner` open on a pool with warm pods | The owner can be set only at pool creation. |
| Round 6: connection reuse skipped the second tenant's decision, GitLab's suffixless path redirects, v0 advertisements exceed 1 MiB, and Bitbucket Cloud does speak v2 | One tunnel per call, a single `.git` redirect, a 32 MiB streaming v0 cap, and the Bitbucket statement corrected. |

Eleventh round, on revision 15:

| Finding | Resolution in revision 16 |
| :-- | :-- |
| The probe never tested authentication, so an agent could use a proxy that ignores Basic with no credential and bypass `netagent` (High) | The probe requires 407s, a positive control, and a separation check, and a source-address proxy does not meet the contract. |
| The shared credential could cross the network in cleartext (Major) | The contract requires a TLS proxy listener. |
| The 25-hour bound and P6 depended on the sync job, with nothing detecting its failure (Major) | Canary checks, and both statements made conditional. |
| Tenant-controlled host strings reached deployer proxy configuration, and an open ceiling had no representation (Major) | A strict host grammar on write and generation, opaque identity names, `allowAll` with `denyHosts`, and a reference renderer. |
| Private git endpoints failed at level 1 while the text called them unaffected (Major) | A direct path for the resolver at every level and for the mirror at level 1. |
| Identity release per pod state was undefined, and warm-up additions were missing from the lists (Major) | A state table derived by the Token Service, and the lists corrected. |
| The mirror's store fallback contradicted its I3 gate during a decision service outage (Major) | The fallback covers upstream and `lenny-egress` outages only. |
| The fallback was unbounded in age, invisible on the session, and triggerable across tenants by volume (Major) | A 6-hour limit, `resolvedFromStore` on the session, coalescing, and 429 excluded. |
| Silent Envoy assumptions, an incomplete limits table, and sync lag mapped to permanent errors (Medium) | I3, I8, and network rows corrected; tunnel lifetime and warm rows added; a `netagent` server-name check and tunnel reports added; publish before release, and a proxy refusal after an allow is transient. |
| Failure classification gaps, including a chained gateway's 502 reading as a host fault, and "adds no outage" overstated (Medium) | An explicit list of fallback causes, CONNECT 429 and 502 mapped, and the dependency's scope restated with its exceptions. A `want` miss schedules a fetch. |
| Service mode escaped the not-yet-visible state, and pool delete and recreate was unguarded (Medium) | A durable pin record, a refusal while old pods remain, and a cascade on deletion. |
| The user namespace statement omitted OCI runtime floors and `idsPerPod` (minor) | Added. |

Twelfth round, on revision 16:

| Finding | Resolution in revision 17 |
| :-- | :-- |
| Service-mode pins had no arbiter across gateway replicas, so one tenant's request could reach a pod pinned to another (High; the code defect exists today) | A conditional insert on pod UID decides the tenant, the loser reroutes, a write failure aborts, and a reconciler retries the label. Proposal 2 closes it. |
| Pod `ipBlock` entries crossed tenants, so any tenant's agent could open a raw socket to another tenant's private service (Major; this also affected the default level) | Pods hold no such entry at the default level or level 2, which use Envoy's raw tunnel, now validated, and level 1 uses tenant-scoped policies. Revision 16's direct resolver path is withdrawn at those levels. |
| Level 1 could not install or probe as written, because preflight runs before any gateway exists (Major) | Preflight checks TLS and authentication only, and the full probe runs after install and gates pool fill, with the `lenny-ops` row and credential path added. |
| Pool recreation could not work, because pool names are a primary key and deletion is soft (Medium) | A new owner means a new pool name, and unlabelled pods are refused on tenant-owned pools. |
| Store fallback state was incomplete, and two statements contradicted the age limit (Medium) | Confirmations are defined and persisted, negative answers clear them, an unavailable mint is listed separately from a refused one, and the statements are corrected. |
| An agent could trigger upstream fetches by looping on random SHAs (Medium) | A negative cache, a per-tenant rate, and a minimum interval. |
| The identity table gave a pinned recycled pod the warm identity, against §5.2 (Medium) | The warm row is limited to pods with no pin. |
| The level 1 mirror `ipBlock` falsified the sandbox's network statement, and the host grammar admitted literals (Medium) | The residual is stated, and a numeric final label is refused. |

Thirteenth round, on revision 17:

| Finding | Resolution in revision 18 |
| :-- | :-- |
| A non-Envoy level 2 proxy had no way to reach a pinned address (Major) | Pinned dialing joins the level 2 contract, and validators refuse private destinations for a proxy that declares it lacks it. |
| The pin record did not cover the IP-keyed router or a gateway rollout (Major) | The router keys on pod UID, reads records on a miss, and adopts a same-tenant conflict. |
| A tenant identifier could be an invalid label value, which fails the rendered NetworkPolicy (Major) | The §10.2 pattern narrows. |
| Level 1 grants had two sources, and level 1 covered git only (Medium) | Grants are Helm values at level 1, and `netagent` dials granted private destinations on both endpoints. |
| Network rows and the probe result's location were missing (Medium) | The binding push port, the Token Service row, and a ConfigMap the controller reads. |
| The first pin raced the decision cache (Medium) | Record, push, and label precede the first request, and unlabelled service-mode pods are not cached. |
| The record's lifecycle, scope, and Postgres dependency were unstated (Medium) | Stated, with its outage behaviour. |
| Mirror confirmations were too coarse, a refused mint cleared nothing, and a negative entry could fail a new pinned commit (Medium) | (ref, SHA) pairs, clearing on a refused mint, and a 60-second entry a fetch clears. |
| A label-selected `proxied` rule was a raw path to deployer git services, and untested push had left the owed list (Medium) | The rule covers the mirror only, and push is listed as owed. |

Fourteenth round, confirming, on revision 18:

| Finding | Resolution in revision 19 |
| :-- | :-- |
| A lost pin record let a second tenant win a pod already labelled for another (Major) | The label is the authority, a refused stamp is lost arbitration, a mismatch fails closed, erasure retires pods first, and a restore rebuilds records. |
| UID keying did not protect the dial, because the gateway dials an IP over plain HTTP and keeps a stale snapshot on a list failure (Major) | A 30-second snapshot limit and a UID check in the pod, with the hostile-listener case stated as a residual until R14. |
| `gitAccess` kept a raw path, or none, to deployer git services (Major) | Its peers are the mirror and `lenny-egress`, with the decision limited to git routes. |
| An `ipBlock` cannot name an in-cluster git service (Major) | Selector peers on the proxy's policy and on the level 1 rows. |
| Lesser rows: the `lenny-ops` certificate and write RBAC, level 1 rules beyond `proxied`, a deleted ref keeping its pair, and the `pinnedDestinations` default | Added. |

The service-mode pin moved to its own proposal, because it closes a defect that exists today and produced findings in three consecutive rounds.

Fifteenth round, confirming, on revision 19:

| Finding | Resolution in revision 20 |
| :-- | :-- |
| The dial check had no component on the request path, and today's dial reaches the runtime port that the chart's policies do not admit (Major) | A service-mode listener in `pkg/adapter` fronts the runtime in both pod models, with its network rows and register entry in proposal 0. |
| The `gitAccess` decision had no formula, so "all other egress stays closed" could fail through the default route (Major) | A formula in "Effective policy", with the warm case and a create-time check. |
| Lesser: the level 1 policy did not select on profile, and the spec section list and proposal dependencies omitted the pin record | Corrected. |

Sixteenth round, confirming, on revision 20:

| Finding | Resolution in revision 21 |
| :-- | :-- |
| Level 1 identity lists omitted the `gitAccess` formula, so the decision allowed and the proxy refused, and git never worked on those pools (Major) | Both lists come from the formula. |
| Lesser: `internet` with `netagent` missing from level 1 rows, `gitAccess` with no owning proposal, the formula omitting the overlay's disable, and LFS scoping | Corrected. |

Revision 21 also added server TLS, after the owner's question about R14 showed I2, I5, and I7 had been left in cleartext pending a certificate they do not need, and that I3 had no stated transport.

Seventeenth round, on revision 21:

| Finding | Resolution in revision 22 |
| :-- | :-- |
| `netagent` had no CA bundle, and the only defined bundle would have trusted tenant CAs on platform hops (Major) | A public `lenny-egress-ca` ConfigMap per agent namespace, written by the controller from its own leaf's `ca.crt`, separate from the interception bundle. |
| The expiry alert read a day-granular stat that is 0 on a healthy 24-hour leaf and covered Envoy only (Major) | `CertExpiryImminent` extended to the new Certificates. |
| I3 client authentication contradicted itself, and one-way TLS let a compromised fetch query the decision API (Major) | mTLS with a client SAN allowlist, retested under faults. |
| "The mount is required" covered only an absent Secret; other broken starts report ready and refuse traffic (Major) | A TLS readiness listener, tested against each state. |
| Found while verifying: the existing PKI is gated by `mtls.enabled`, which is off in mesh deployments and the Kind overlay (Major) | An egress PKI of its own. |
| Lesser: the LimitRange default, per-group Certificates, access log stream TLS, `doctor --fix`, and the mirror's namespace | Stated, with the mirror's issuer left to proposal 5. |

Eighteenth round, on revision 22:

| Finding | Resolution in revision 23 |
| :-- | :-- |
| A flat I3 allowlist let a compromised mirror parent obtain a tenant's upstream credential (Major) | Operations bound to client SANs, and the mirror's tenant assertion limited to tenants whose route names it. |
| `lenny-ops` can read every Secret in `lenny-system`, so the CA key claim was false (Major; the RBAC exists today) | The claim is corrected, the Role is narrowed in proposal 2, and the defect is listed for filing. |
| The controller had no leaf, the gateway had none at the default level, and the PKI did not render with a replacement proxy and no mirror (Major) | Leaves for both at every level, the binding push moved to the gateway's leaf, and the render gate moved to `netagent`. |
| CA rotation depended on each component's own leaf, so handshakes failed in either order (Major) | Components read trust from the ConfigMap, with a fixed order for a key rotation. |
| Lesser: the proxy CA shared the platform bundle, `doctor`'s window matched every leaf, SANs were unstated, and tier 9 omitted the readiness port | Corrected. |

Nineteenth round, confirming, on revision 23:

| Finding | Resolution in revision 24 |
| :-- | :-- |
| A key rotation dropped the old CA when the controller's leaf re-issued, while other leaves still chained to it (Major) | The Helm value carries both CAs until every leaf has re-issued. |
| Narrowing `lenny-ops`'s Secret read did not protect the CA key, because it can create Jobs and patch Certificates, and the controller can create pods (Major) | Both are stated as inside the PKI's trust boundary, with the residual listed. |
| Lesser: the mirror's second gate, the route check on level 1 service identities, and the ConfigMap for the mirror's own namespace | Stated. |

Twentieth round, confirming, on revision 24, which found one Major and judged the rest sound enough to proceed to proposals:

| Finding | Resolution in revision 25 |
| :-- | :-- |
| `doctor --fix` deletes the backing Secret of any Certificate it matches, with no `isCA` filter, which rotates a CA's key out of order (Major; the code exists today) | The remedy excludes CA Certificates and reports them for the manual procedure, and the defect is listed for filing. |
| Lesser: the route check on the mirror's direct I3 decision | Stated. The doctor detector's coverage of a `lenny-mirror` namespace stays with proposal 5. |

Revision 25 received no further review round. Its change adds no mechanism, and the fix is a constraint on an existing remedy. Revisions 26 and 27 record filed findings, owner decisions, and round 8's results, and revision 28 records decision 31 with its anti-affinity, `hostUsers` value, and ID rule. None of the three received a review round. Revision 29 adds the evidence streams, tenant visibility, exfiltration controls, push limits, API-assigned egress groups, the connector's hooks, and the reserved hooks, from a review against the needs of a customer admin, a customer security team, and an operator's forensics team. It has not had a review round either.

Twenty-first round, on revision 30:

| Finding | Resolution in revision 31 |
| :-- | :-- |
| Evidence writers had no network path or safe credential, and a bucket credential would expose every tenant's records to a compromised mirror (High) | A single `lenny-evidence` ingest service, with streams bound to SANs. |
| "No connection goes unrecorded" failed, because an allow returned before the batch persisted (High) | A group commit before allow for regulated tenants, tested under failure. |
| The connector claim failed for minted credentials, and for `inject` on a customer CA (High) | The Token Service mints through `lenny-egress` for non-`direct` hosts, and a binding can carry its own CA. |
| Reads and scale were unbacked, and per-tenant flushing implied about 65 million manifest rows a day (Major) | One writer per tenant, a compactor, a stated scan-based query, and an export cursor separate from §11.7. |
| Erasure and holds were unspecified, which would have forced a format change for agent activity (Major) | The audit class for these streams, `payloadKeyRef` for later ones, and a held marker. |
| Report-only suspended the live policy (Major, in both reviews) | A shadow policy that changes no outcome. |
| `credentialPinned` failed open on case or port, overwrote only one header, and let upgrades through (Major) | Canonical matching, refusal inside the connection, a strip list, and refused upgrades, with body-carried credentials stated. |
| Minted bindings bypassed `allowedRefPatterns` through smart HTTP on the egress endpoint, and REST writes were unstated (Major) | Smart HTTP refused there for VCS-covered hosts, and the REST limit stated. |
| Envelope fields were not bound at ingest, so a compromised `netagent` could report `enforced` or another tenant (Major) | Ingest and the decision service overwrite them, and level 1 decisions are `self_reported`. |
| A shared buffer broke P7 (Major) | Per-tenant buffers and aggregated denials. |
| Group assignment refused live pods, handed out pods with dead egress, and allowed re-grant to impersonate an address (Major) | The stamped label decides, claim and rebind drain a mismatch, and groups are exclusive by default with a cool-off. |
| GitLab token reuse had no key or lifetime, and a read session could hold a write token (Major) | Keyed on tenant, pool, project, and scope, with stated lifetime and revocation. |
| The split was inconsistent, and EG-2 was too large (Major) | EG-2 narrowed, with EG-9 for policy objects and EG-10 for evidence, and route targets moved to EG-2. |
| Lesser: stale Helm wording, redaction of attacker-chosen authorities, `observed` provenance for a sensor, and pins on `internet` pools | Corrected. |

Twenty-second round, confirming, on revision 31:

| Finding | Resolution in revision 32 |
| :-- | :-- |
| Smart HTTP refusal and read-only hosts assumed a termination that does not exist on unbound hosts, and the per-pod CA's constraints excluded them (Major) | Refusal applies on terminated hosts, the opaque-tunnel and `file` cases are stated residuals, the constraints cover pinned and read-only hosts, and pinning a git host closes the gap where a pool needs it. |
| The Token Service's mint path was not wired for tenant assertions, groups, custom CAs, or level 1 (Major) | Each is added, with the assertion limited to the tenant's VCS pool hosts. |
| Compaction broke anchors and export cursors (Major) | Byte-for-byte concatenation with original digests, an anchor per compaction, and stable cursor positions. |
| Proposal order had cycles through the PKI and `provider-direct` (Major) | EG-11 for the PKI, and the `provider-direct` removal moved to EG-2. |
| GitLab token reuse broke P2 (Major) | A stated P2 exception under the `storedTokenPush` grant, a 7-day lifetime, and an on-demand revoke. |
| Lesser: `lenny-evidence` missing from lists, the commit bound equal to the timeout floor, mirror record forgery, service-mode drain, the pin list's failure mode, and `inject`'s CA wording | Corrected or stated. |

Twenty-third round, confirming, on revision 32:

| Finding | Resolution in revision 33 |
| :-- | :-- |
| Absolute-form plain HTTP to a bound host bypassed the smart HTTP refusal and attached the minted token (Major) | The refusal and the pin and read-only rules apply to those requests. |
| The mint assertion refused the providers' mint endpoints (Major) | It also covers each provider's declared mint endpoints. |
| The pin list failed open before its first read (Major) | `netagent` refuses every CONNECT from a binding until it has read the list, at the cost of no egress for a pod bound during a Token Service outage. |
| Re-issued name constraints missed processes that had already loaded the anchor (Major) | SDK-warm pods demote, and service-mode anchors carry no constraints, with that limit stated. |
| Lesser: proposal edges through minting and the gate, the Token Service's level 1 row, and the manifest row estimate | Corrected. |

Twenty-fourth round, confirming, on revision 33:

| Finding | Resolution in revision 34 |
| :-- | :-- |
| The pin gate depended on when `netagent` learned its binding, so a lost push left a pinned host an opaque tunnel (Major) | Removed. The decision service refuses a CONNECT to a pinned host without `netagent`'s terminated marker, which needs no ordering and no longer costs egress during a Token Service outage. |
| The anchor's reload rule omitted embedded, `keep`, and concurrent processes (Major) | Constraints apply only when the runtime launches after bind and serves one binding set. |
| Re-issue had no launch ordering, and setup had no egress in the provisional state (Major) | The adapter waits for the applied generation, and the provisional state serves definitions with no secrets. |
| Lesser: mint endpoints on level 1's service identity, `info/lfs`, and an owner for the gate grants | Corrected. |

Twenty-fifth round, confirming, on revision 34:

| Finding | Resolution in revision 35 |
| :-- | :-- |
| The decision cache ignored the terminated marker (Major) | The marker joins the cache key. |
| A snapshotless rebuild's setup commands had no secrets, so they failed where finalize succeeded (Major) | `inject` secrets are available in the provisional state, and the remaining limit is stated. |
| The marker check blocked service identities and the local git endpoint (Major) | The check applies to pods only, and the local git endpoint sets the marker. |
| Lesser: level 1's provisional row, the level 2 contract, and the wait's timeout | Corrected. |

Revision 35 received no further review round. Major findings per round in this stretch ran 16, 5, 4, 3, and 3, each with a local fix and none changing the architecture, and the later ones were mostly edge cases of `credentialPinned` and the per-pod CA, features added in the same stretch. This document stops here, and each proposal's own adversarial review continues the search against the staged spec edits.

Revision 37 folds in round 10's corrections and the hooks that the four related designs requested as rework if added later. Revisions 37 to 43 have not had a review round of this document. The additive items the connector design lists (a probe assertion, a route-dependent CONNECT budget, and `outboundHosts`) land with OC-3 and OC-2 and need nothing here.

## Move into changes (2026-11-16)

Change 0113 moved this design from `designs/egress-and-git-design.md` into this folder. Its review history moved into this file, its proposal plan became "Steps", its evidence moved into `evidence/`, its status became `Draft` pending the owner's final cross-review, and every path to a design or its evidence, in links and in prose, now names its new location.

## Status line before the split (2026-11-16)

Change 0113 shortened the status line of `change.md` to the status, revision, date, and program, and moved the rest here as it stood:

> Revision 93 applies the kind and field names of [`0137-pod-lifecycle-controller`](../0137-pod-lifecycle-controller/change.md) revision 3, which the owner accepted on 2026-11-16 as its O3, and removes the `adr/` row of the documentation table, because the owner decided on 2026-11-16 to remove ADRs from Lenny. It changes no rule. Revision 92 follows [`0143-service-account-credentials`](../0143-service-account-credentials/change.md) revision 2, which applies the security review of that design's revision 1 and the owner's answer of 2026-11-15, "O1-O7: go with your recommendations". Exchange JWTs move to an exchange issuer of their own, `https://<lenny>/exchange/<tenant-id>`, so the federation key set signs federation tokens alone again. It keeps RS256, because `federation` serves the Azure SDKs and Entra tests only RS256 issuers, and a new federation key is published before the Token Service signs with it. The **Gates** paragraph adds `userCredentialMetadataDelivery`, the grant for a service account's AWS credentials under `metadata`, and "Reserved for other designs" carries the `/exchange/` path and the grant. Revision 91 follows [`0143-service-account-credentials`](../0143-service-account-credentials/change.md) revision 1, which records the owner's answers of 2026-11-15 on service accounts: option (b), "b + client credentials ... go ahead", and "I think we should also support 'A provider can be set up to trust Lenny's own issuer directly'". A `userCredential` binding delivers the token of the session's principal, a person or a service account; the `userCredentialDelivery` gate covers a principal's token; the Token Service calls the token, discovery, and exchange endpoints of the `clientCredentials` and `issuerExchange` sources through `lenny-egress` with the tenant asserted; the federation key set signs with RS256 and also signs that design's exchange JWTs, which no pod receives; and a binding's name matches `^[a-z][a-z0-9-]{0,62}$`, so no federation subject equals a principal subject. The issuer choice awaits the owner as that design's O1. Revision 90 follows [`0124-connector-removal`](../0124-connector-removal/change.md) revision 5 and the owner's answer of 2026-11-15, "yes to refusal": an `identityAssertion` binding is refused on recycled pods and on pods that have served a session, as [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) UC-3 refuses a `userCredential` binding there, because the assertion lets a service act as the user and a `netagent`, adapter, or sandbox kernel compromised by an earlier session on the pod could take later sessions' assertions. Revision 89 follows the owner's withdrawal of O10 of [`0124-connector-removal`](../0124-connector-removal/change.md) on 2026-11-15, "refusal on both (I changed my mind)": the label row no longer names O10, and no rule of this design changes. Revision 88 follows [`0124-connector-removal`](../0124-connector-removal/change.md) revision 3 and the owner's answer of 2026-11-15, "yes", to a setting that turns off the deployment key set: with `tokenService.identityAssertion.deploymentKeySet.enabled: false`, the deployment key set route answers `404` and a service that serves every tenant verifies through each tenant's key set. Revision 87 applies the owner's decision of 2026-11-15 to remove connectors, in the owner's words "Yes, let's drop connectors.", which [`0124-connector-removal`](../0124-connector-removal/change.md) revision 2 designs. It adds the `identityAssertion` secret kind with its identity issuers and key sets (that design's CR-2), the egress halt on `CH-EGRESSHALT` at a suspension that cannot take hold (CR-4), and the findings of the harness-egress spike of 2026-11-15 on the client proxy profile, and it replaces the residual of MCP connector calls with operator services reached through egress (CR-3). Revision 86 points its MCP documentation rows at the MCP adapter's documentation under [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md). Revision 85 records the owner's confirmation on 2026-11-14, in the words "Ok on the rest", of the store group as the primary group of both the adapter and `lenny-store-fill`, with the pod under `supplementalGroupsPolicy: Strict` (decision 61), and changes no rule. Revision 84 follows `0147-tenant-derived-runtimes` revision 73, the independent review of the question 17 write-up: a `gitRepository` asset of a tenant-derived runtime records its declarer from the role that wrote it, so a `platform-admin`'s asset keeps the operator protections of that design's decision 35, and the reviewer instruction to the materialization design states adoption by repository under decision 67. Revision 83 applies the AWS spike of 2026-11-14 on slot attribution, which measured that runc answers `sock_diag` and gVisor and Kata answer only `/proc/net/tcp`, with its rules on socket state, batched reads, IPv4 loopback listeners, and the start-up check, and decision 68, which recommends the `inet_diag` options for the Kata guest kernel. Revision 82 applies decision 67, settled on 2026-11-14: a `gitRepository` entry of `sharedAssets` is the only way to declare a warm store, and `prestage: true` replaces decision 63's target `path`. Nothing in this document is built or staged, and no production component or live provider write has run. Revision 75 applied the owner's decisions of 2026-11-12 in the consistency pass across the designs. Revision 76 applies the cross-design review of 2026-11-13 and the owner's decisions of that day: per-slot scopes (decision 60), the `lenny-store-fill` container (decision 61), the withdrawal of proposal 0 with the removal of service mode and `type: mcp` runtimes under [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/change.md) (decision 62), the warm-up checkout of operator entries with a target `path` (decision 63), and the runtime's override of the git maintenance settings (IC D6). Revision 77 applies the fix pass of the same review: the per-slot credentials volume name, the store-fill permissions, and the removal of direct delivery mode by [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md). Revision 78 applies the check pass of the same review: every per-session value moves to the slot's own credentials volume, the attribution refusals and the fallback pools are qualified by the pool's attribution status, and the store fill's pristine-tree modes and the adapter's deletion are stated. Revision 79 applies the last round of the same review: each `slot-<n>-netcred` takes the 8 MiB `sizeLimit` that [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md) sets. Revision 80 applies the owner's answers of 2026-11-13 to the consolidated list of open items across the designs: the dedicated store group of `lenny-store-fill` with an owed hostile-repository stress suite (decision 61), platform connections (decision 64), one commit identity per VCS pool (decision 65), the defaults of `netagent.resources`, `netagent`'s volume sizes, and `gitMirror.heldUidBudget` (decision 66), the refusal of a write that names a removed field, and the listing of a concurrent pod's environment deny under `ENVIRONMENT_POLICY_NOT_ENFORCEABLE`. Revision 81 follows [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md) revision 8: the store group is the primary group of both the adapter and `lenny-store-fill`, and the disk watch's threshold is a chart value. No decision of this design is open. It reads against [`0143-service-account-credentials`](../0143-service-account-credentials/change.md) revision 2, [`0124-connector-removal`](../0124-connector-removal/change.md) revision 7, [`0146-session-lifecycle`](../0146-session-lifecycle/change.md) revision 12, [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) revision 40, [`0152-user-git-identity`](../0152-user-git-identity/change.md) revision 45, [`0129-github-credential-addon`](../0129-github-credential-addon/change.md) revision 7, [`0130-gitlab-credential-addon`](../0130-gitlab-credential-addon/change.md) revision 6, [`0141-runtime-routing`](../0141-runtime-routing/change.md) revision 19, [`0140-runtime-revisions`](../0140-runtime-revisions/change.md) revision 22, and [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) revision 8, and against the designs of the consistency pass: [`0145-session-isolation`](../0145-session-isolation/change.md), [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md), [`0153-workspace-materialization`](../0153-workspace-materialization/change.md), [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/change.md), [`0139-runtime-protocol`](../0139-runtime-protocol/change.md) revision 13, [`0136-placement`](../0136-placement/change.md), and [`0150-tenant-security-settings`](../0150-tenant-security-settings/change.md). The owner adopted the recommended decisions except where "Decisions" says otherwise, settled the git path under decision 44, and reversed the deferral of level 1 replacement. Proposals carry the label EG-n, which "Proposal split" defines. "Review history" at the end records how the design reached this form, and "Validation record" records the validation rounds. The owner's final cross-review of the moved designs is pending, so the status is `Draft`.

## M3 readiness review (2026-11-19)

- Date: 2026-11-19. Trigger: review. Name: M3 readiness review. Revision 94 applies the review of revision 93 against `main` at `092ef92f6`, read in three parts (`scratchpad/m3-readiness/reviews/0127-p1.md`, `0127-p2.md`, and `0127-p3.md`), the skeptic's verdict on its one High finding, which downgraded 0127-p1-F1 to Medium and narrowed its fix, and the conflicts that name this change in the consistency reports of the checkpoint-workspace, connectors-llm-billing, credentials-git, pod-structure, program, removals, and tenancy-security clusters. The status stays `Draft`.
- Counts: 58 applied, in whole or in part; 12 raised as open decisions (69 to 80); 1 returned as an issue; 0 dropped. Nine conflicts name this change only as the far side of a fix in another folder and need no edit here: checkpoint-workspace-C2 and C7, connectors-llm-billing-C3 and C6, credentials-git-C5, C12, and C14, removals-C1, and tenancy-security-C10.
- Applied:
  - 0127-p1-F1 (part): "Relationship to proposal 0111", **Kept unchanged**, cites 0153 WM-10 and 0147 "Warm stores" as the text of the rewrite, so no rule depends on the branch, and "Terms" notes the key that WM-10 names differently. The rest is decisions 69 and 70.
  - 0127-p1-F2: 0127.S2 depends on 0145.S1 and 0118.S4, and "Notes on the steps" names both edges.
  - 0127-p1-F3 (part) and program-C9: 0127.S3's ordering names 0153.S4 in place of 0153.S2. The removal ordering is decision 73.
  - 0127-p1-F5: the adapter checks that `netagent`'s UID holds `@lenny-netagent-grant` and `@lenny-netagent-halt`, `netagent` fails readiness without both names, and it refuses slot traffic after a restart until the adapter has reconnected on both channels; a `security` test covers a slot that binds the names.
  - 0127-p1-F6 (part): a pool whose pods have one slot each is never a fallback pool, and the start report's carrier moves from EG-3 to 0127.S2. The status across an image change and the carrier's form are decision 74.
  - 0127-p1-F8 (part): the ref resolver's read credential comes from the Token Service's gateway listener with the gateway cause assertion, and the "gateway lease path" sentence no longer says it leases VCS credentials to sessions. The rest is decision 76.
  - 0127-p1-F9: step descriptions name the `egress.accesslog` producer, minting for the other providers, stored-token push, `allowWorkflowEdits`, platform connections, the attribution status with `NetagentAttributionUnavailable`, read after push through the mirror, and the 0143 hooks; F-13.2.29 and F-13.2.31 close in 0127.S6.
  - 0127-p1-F11: each entry of "Validation still owed" names its waiting step, and the entries of decision 66 say they gate a default value only.
  - 0127-p1-F12 (part): 0127.S2 and 0127.S9 carry the decision-8 line with their seam operations; the moved paths and symbols are current (`pkg/gateway/core/session/sessionserver/workspace_plan.go`, `pkg/credential/credentialpoolstore/vcsresolve.go`, `pkg/gateway/core/podclaim/tenantpin.go`, `pkg/gateway/core/podclaim/slotclaimer.go`, `pkg/gateway/api/admin/pools.go` with `applyPoolUpdateMerge`, `pkg/runtime/poolstore/poolstore.go`, and `gitref.CloneArchive`); tier numbers become tier names. Identifiers and page codes are decision 77.
  - 0127-p1-F13: "Reserved for other designs" gains `pkg/binding`'s resolution of a slot index and a time to a session for 0128, in 0127.S2. Its store is decision 75.
  - 0127-p1-F14: the `netagent` table's channel row names `CH-FETCHGRANT` and `CH-EGRESSHALT`.
  - 0127-p1-F15: the Token Service outage text names the clone of `lenny-materialize`.
  - 0127-p1-F16 and 0127-p2-F1 and credentials-git-C7: `metadata` for an `awsWebIdentity` type under `userCredentialMetadataDelivery` is stated in the binding's `delivery` field, "Terms", the delivery table, and both configuration rows, with a `unit` test.
  - 0127-p1-F17: a write mint re-reads the binding from the stores, bypassing the `pkg/binding` cache.
  - 0127-p1-F20: the gateway cause assertion is signed with `platform:jwt-signing`, and `lenny-egress-authz` gains `GetPublicKey` to verify it.
  - 0127-p1-F22: the `resuming` watchdog cites §6.2.
  - 0127-p1-F23: "Notes on the steps" states that proposal 0114 is outside the v1 program.
  - 0127-p1-F24: "Residual risks and limits" states that a level 2 proxy sees pod tokens and sits inside the trust boundary of `lenny-egress`.
  - 0127-p1-F26: `decisions.md` gains "Open decisions", and slot attribution is listed in 0127.S2 alone.
  - 0127-p1-F27: an `integration` test covers read after push through the mirror route; the helper's clone and the `gitAccess` formula already had tests.
  - 0127-p2-F2: the configuration rows for `egress.allowedHosts` and attached bindings refuse only on a pool with several slots whose runtime class does not answer attribution.
  - 0127-p2-F3 and connectors-llm-billing-C1: 0127.S2 removes `provider-direct`, NET-006's half of it, NET-044, `egressCIDRs.providers`, and check (3) of `direct_mode_isolation`, and the webhook with checks (1) and (2) stays until 0133.S2 removes `deliveryMode`.
  - 0127-p2-F5: a VCS pool write that newly covers a host suspends the stored-token bindings on it, with an `integration` test for both write orders.
  - 0127-p2-F6: a pool's `egressProfile` change is pending until no pod carries the old label, and the claim for a session with `egressLockHosts` or a credential pin skips `internet` pods meanwhile, with an `integration` test.
  - 0127-p2-L1: the I3, I7, and I8 rows match "Terms" and **Identity.**
  - 0127-p2-L2: 0127.S9 closes T-26.2.3.
  - 0127-p2-L3: 0127.S2 names the bytes-out alert and the Helm value for the LLM proxy host with its preflight check.
  - 0127-p2-L4: **Delegated child session** states that a locked parent cannot cross, citing 0149's check 2.
  - 0127-p2-L5: a tenant client config file's name matches `^[a-z0-9][a-z0-9._-]{0,62}$` with no `..`, with a `unit` test.
  - 0127-p2-L6: an `integration` test and an owed validation cover fsck on legacy repositories.
  - 0127-p2-L8: the federation subject's `<tenant>` is the tenant identifier.
  - 0127-p3-F1: the chart renders the mirror's default-deny policy and rows in `lenny-mirror` as in `lenny-system`, with namespace selectors on every peer, and a `security` test covers it.
  - 0127-p3-F2: `lenny-ops` is a level 1 caller of the pod credential API for probe identities alone, the probe identities are listed, and the I8 row and two tests admit it on that basis.
  - 0127-p3-F3: a replacement proxy in the cluster is a selector peer, at level 1 and level 2, with an `e2e_kind` test.
  - 0127-p3-F4: 0127.S9 carries the owner stamp at creation with its webhook rule, `lenny.dev/served` with both readers, the unlabelled-pod refusal, and the deletion cascade, and 0127.S2 no longer carries the owner stamp.
  - 0127-p3-F6: tests cover `pods/status` label patches, the narrowed `tenant_id` pattern, the VPC CNI preflight check, and `lenny.dev/served`.
  - 0127-p3-F7: tier headings use the tier names, the `docs` tests that match specification prose are deleted or replaced rather than extended, the §18 and §23 items leave "Specification sections affected" as moot, and the pool table note names `sandbox_warm_pools` and 0137's rename.
  - 0127-p3-F8: "Runtime SDKs" states that `CH-RUNTIMEOPS` merges into `CH-MSGSOCK` under 0139 O7 (a).
  - 0127-p3-F9: an unstamped pod of a granted tenant is admitted at the default Envoy, the `security` test names a stamped pod, and the Globex scenario has its pods dial `globex-egress` directly.
  - 0127-p3-F10: the sizing text follows decision 66: 512 MiB memory limit by default, an optional CPU limit refused below 250m, and a pool refused when its tunnel quota does not fit.
  - 0127-p3-F12: F-13.2.29 and F-13.2.31 close in 0127.S6.
  - 0127-p3-F13: §15 and the `api/admin.md` row list routes, private destination registrations and grants, egress group assignments, client config files, and the level 1 rotation call.
  - 0127-p3-F14 (part): `internet` pools that run `netagent` add the `lenny-egress` and `lenny-git-mirror` peers, `NODE_EXTRA_CA_CERTS` holds a tenant-owned pool's `tenantOverride.caBundle`, level 1 private destinations use the egress group retry budget, and the runbook's unimplemented `egress-policies` command is the one replaced. The SSRF classification is decision 80.
  - 0127-p3-F15: the decision service refuses an egress group header that differs from the calling Envoy's client SAN, with a `security` test.
  - credentials-git-C4: "Reserved for other designs" gains `pkg/binding`'s `PodClaim` watch and the Token Service's `podclaims` rights in 0127.S2.
  - credentials-git-C6: the writable entry holds the SO-5b state and a separate user verdict.
  - credentials-git-C8: a `reauth-on-401` request names the refused token's hash and the Token Service evicts it from the mint cache, with a `unit` test.
  - credentials-git-C9: the GitLab pending-revocation record cites 0151 UC-2's definition.
  - credentials-git-C11: the decision service honours the tenant assertion for a candidate issuer's well-known metadata paths during a type write.
  - credentials-git-C13: one counter per App identifier with one alert threshold, which 0129 cites.
  - credentials-git-C15: the GitLab host list names `Sudo`.
  - pod-structure-C7 and program-C8 (part): 0127.S5 owns `netagent`'s attribution of the fill's UID alone, and its "lands with 0118.S4" sentence names 0118.S4 for the container and 0153.S9 for the fill.
  - tenancy-security-C11: 0127.S5 states that whichever of it and 0149.S1 lands second adds the environment predicate for platform warm-list entries.
- Raised as open decisions:
  - 69: where the rules of the 0111 rewrite that no member change states are written (0127-p1-F1, 0127-p3-F5).
  - 70: the slot repository's commit key, `lenny.storeCommit` here against `lenny.baseCommit` in 0153 WM-10 (0127-p1-F1).
  - 71: whether 0127.S2 is split (0127-p1-F10).
  - 72: which destinations a platform-marked CONNECT reaches (0127-p1-F4).
  - 73: where the removal of the gateway clone path lands (0127-p1-F3).
  - 74: the attribution status across a `netagent` image change, and its carrier (0127-p1-F6).
  - 75: the stores for state shared across replicas and for the slot assignment history (0127-p1-F7, 0127-p1-F13).
  - 76: the ref resolver's read credential on `mint-coarse` and `stored` providers (0127-p1-F8).
  - 77: when the requirement identifiers, page codes, channel identifiers, and catalog names are assigned (0127-p1-F12, 0127-p1-F21, 0127-p3-F7).
  - 78: whether a binding written during a session reaches its running processes (0127-p2-F4).
  - 79: the serving path of the Token Service's public issuer routes (0127-p2-F7).
  - 80: details the design leaves unstated (0127-p1-F18, 0127-p1-F19, 0127-p1-F25, 0127-p2-L7, 0127-p3-F14).
- Returned as an issue: 0127-p3-F11, the SDK gaps the change says should be filed, which belong under the runtime SDK page.
- Not applied, as parts of applied findings: the heading and position of "Validation still owed" (0127-p1-F11) stay, because `0136-placement`, `0148-tenant-outbound-connector`, `0149-tenant-scoping-identity`, and `0152-user-git-identity` link to its anchor; the unnumbered list of early owner decisions (0127-p1-F26) stays, because numbering it would shift the settled numbers that other changes cite.
- Second pass: the same review closed the 22 edits that the fix agents of other changes requested of this change. Counts: 13 applied, 6 already done by the first pass, 3 raised as open decisions (81 to 83), 0 already open, and 0 dropped. The already-done requests are 0130's `Sudo` entry and its pending-revocation citation, 0133's move of the webhook and checks (1) and (2) into 0133.S2 with its open decision 4 named, 0145's `0145.S1` edge on 0127.S2, 0151's `PodClaim` hook row, mint cache eviction, revocation citation, and `Sudo` entry, and 0153's naming of 0153.S4 in 0127.S3.
  - Applied for 0119: the §5.2 rows of "Terms from other designs and the platform", "Labels from other designs", and "Specification sections affected" point to 0119's open decision 5 on O7's renames.
  - Applied for 0121: 0127.S2 adds `netagent`'s request to 0121's container-mapping subtraction and to the class check at load when it lands after 0121.S1.
  - Applied for 0124: **Claims.** adds the `agent` subject `lenny:<tenant>:agent:<principal-id>` with its `unit` case, the `pkg/binding` request row returns the delegation depth and the environment, and the `PodClaim` hook row also serves 0124 CRP-6. The `/oidc/` half of the request is open decision 79, which now records that 0124 CR-2 **Serving the public routes.** states option (a) for `/identity/`, and **Identity issuer and key sets.** points to both.
  - Applied for 0129: the per-App counter names the metric `lenny_token_service_app_token_requests_total`, the alert `TokenServiceAppTokenRequestsHigh`, and the Helm value `appTokenRequestsAlertPerHour`.
  - Applied for 0130: the worked example "A merge request on self-managed GitLab" states that between the merges of 0127.S4 and 0130.S3 a GitLab API binding applies no preset. Ordering 0127.S4 after 0130.S3 would make a cycle through 0151.S4.
  - Applied for 0147: 0127.S5 states that it builds the warm list and the rules that hold on every pool, with the owned-pool parts in 0147.S8, and 0127.S9 closes `F-5.2.67 (part)`.
  - Applied for 0148: 0127.S2's open request names the API gateway's assertion; the credential-pinned half of the `secret: {kind: none}` refusal moves from 0127.S4 to 0127.S12, and the dependency table's 0148.S2 row names 0127.S12; "Tenant outbound connector" states that one grant opens one stream on each of up to two named instances; and 0127.S3's CONNECT budget request points to 0148's open decision O4 for the spike step.
  - Applied for 0149: the dependency table's evidence field row names 0149.S11.
  - Applied for 0152: the writable set entry row, the "Terms" row, the fetch grant, and the reserved hooks test name the field `userAccess`; **Bindings.** under "Slot attribution and fetch grants" gives the `prAuthoring: user` binding on a fallback pool 0152's `422 USER_AUTHORIZATION_UNSUPPORTED_POD`; and 0127.S12 states that whichever of it and 0152.S2 lands second builds the VCS-pool pin wiring.
  - Applied for 0153: the fetch grant no longer covers `overlay` runs, `netagent` answers `revoke` with `revoked` before the adapter sends the slot's next `setup`, and the `unit` and `integration` fetch-grant tests cover it.
  - Raised as decision 81, requested by 0118: what a pod with a non-empty warm list does on a pool that runs no `netagent` (mirrors 0118 O14).
  - Raised as decision 82, requested by 0142: whether a later `allowed` verdict clears `userAccess: userRefused` (mirrors 0152 decision 54). The first pass had written that it does, which chose the open question, so the writable set entry row now points to decision 82.
  - Raised as decision 83, requested by 0143: who triggers an emergency rotation of the federation key and the identity key (mirrors 0143 O10).

## Revision 95 (2026-11-23)

- Trigger: change 0166. Revision 95 of `change.md` carries out the row for 0127 in "DP-7 What each change takes on" of [`0166-delegation-primitives`](../0166-delegation-primitives/change.md#dp-7-what-each-change-takes-on), revision 7. The **Delegated child session** case under "Effective policy" keeps inheritance as the default, and states that the parent's `session.create` may narrow the inherited hosts and bindings with `egressHosts` and `credentialBindings` within one environment and never widens them (0166 "DP-1", decision 35), and that a crossing child follows TS-3 of 0149 (0166 decision 41). The delegation lease and the §8.2 `TaskSpec` give way to the session grant and `session.create` in "Terms", in that case, in the row for EG-9's rule on children, and in the `integration` case for ref resolution. "Labels from other designs" gains 0166.
- 2026-11-24, revision 96. Under decision 22 of `0133-llm-upstreams-and-cost-budgets`, "Credential bindings" gains **Platform and pool bindings.** for an operator's LLM gateway and **Binding suspension.**, through which an exhausted budget stops a session's attribution. The reservation of the endpoint hosts of LLM upstream records (LUR-11) leaves, because Lenny holds no LLM credential, and "Delivery modes" **LLM provider hosts.**, the residual on `internet` pools, the egress halt, and the labels row follow. Raises open decisions 84 (the scope of a `platform-admin` binding) and 85 (binding suspension against a status lookup).
- 2026-11-24, revision 97. Settles open decision 85 as decision 85: **Binding suspension.** carries its cause, and `netagent` answers a suspended session's requests to an attribution binding's hosts itself, with `429` and `Retry-After` while a §8.6 extension is pending, `403` naming the budget and the limit once the exhaustion is final, and `403` with no detail for another cause. **Placeholders under `delivery: inject`.** gains the removal of `lenny-injected` from other headers on an attribution binding's hosts, under D15 of 0124. Open decision 84 stays open.
