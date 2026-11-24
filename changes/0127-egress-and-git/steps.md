# Steps: 0127 Egress and git access for agent pods

## Steps

Proposal 0, the service-mode tenant pin, is withdrawn (decision 62). [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/change.md) removes service mode in 0119.S2 (AR-B), which closes F-5.2.65 and F-5.2.66 by removal and lands before the steps of this change that touch the pod, so no step here rewrites service-mode code.

### 0127.S1 Git prerequisites
- status: Planned
- aliases: [EG-1, proposal 1]
- lanes: [specification, code, documentation]
- closes: [F-4.9.44]

GitHub App minting with provider capability levels and the token cache (F-4.9.44), minting for the other providers of the capability table under "Credentials" (AWS CodeCommit, GitLab project access tokens with the durable pending-revocation record, Azure DevOps Entra ID tokens, and the `stored` providers), the per-App token request counter, repository identifiers, the shared repository-path canonicalizer, repository routing, and write-scope checks. The `github` provider keeps accepting a stored `token` until 0127.S3 removes its consumers, so 0127.S1 lands without breaking `gitClone`.

Requests from other changes: 0127.S1 carries the pre-mint step and the flavour refusal.

### 0127.S2 Pod network agent and proxied egress
- status: Planned
- aliases: [EG-2, proposal 2]
- lanes: [specification, code, documentation]
- depends-on: [0127.S1, 0127.S9, 0127.S10, 0127.S11, 0119.S2, 0145.S1, 0118.S4]

The `tenant_id` pattern, the removal of `provider-direct` (NET-006's `provider-direct` half, NET-044, `egressCIDRs.providers`, and check (3) of `direct_mode_isolation`, while the `lenny-direct-mode-isolation` webhook and checks (1) and (2) go with `deliveryMode` in 0133.S2), server TLS on the proxy hop and the pod binding API and mTLS on the egress decision API, that API's operations by client SAN, with the chart's reload, readiness, mount, CPU, and alert rules, the producers of `egress.decision`, `egress.tunnel`, `egress.local`, and `credential.mint`, the `egress.accesslog` stream from `lenny-egress` to `lenny-evidence`, the per-session bytes-out alert on the `egress.tunnel` stream, the `quarantined` binding state, typed route targets and the per-kind validator, the Token Service's mint path through `lenny-egress`, the binding push and cache rule for credential pins, `netagent`, service account identity, `lenny-egress` on Envoy, `lenny-egress-authz` with the egress decision API and the pod binding API, the binding feed with fan-out and the read-scoped provisional resume binding (§7.3), port scoping, the Kubernetes issuer verifier, the pod credential API listener, the `pkg/binding` resolution package with each session's slot index, slot attribution and the per-slot scopes of decision 60 with their fallback, platform connections (decision 64), the pool's attribution status with the carrier of `netagent`'s start report and `NetagentAttributionUnavailable`, and the Token Service's new RBAC and ingress rows, the CA bundle assembly, the tunnel lifetime menu, quotas, the `proxied` egress profile, the DNS resolver for pods with `egressProfile: proxied`, and the Helm value for the LLM proxy host that feeds both the gateway's `--llm-proxy-public-url` and the controller's `NO_PROXY`, with its preflight check.

Its Token Service minting path needs 0127.S1. When this step lands after 0121.S1 of [`0121-billing-resource-attribution`](../0121-billing-resource-attribution/steps.md#steps), it adds `netagent`'s request to the subtraction under that change's "Container mapping" and to the class check at load, so the pod's effective request stays the class. Slot attribution and the per-slot scopes need one UID per slot and the slot containers of 0145.S1 (SI-B) of [`0145-session-isolation`](../0145-session-isolation/change.md), and `netagent` writes `/run/netagent` and each `slot-<n>-netcred`, which 0118.S4 (PL-B) of [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md) renders.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It adds the binding push after bind, unbind, recycle, and retire, and the provisional binding on resume, behind `GWSEAM-CreateSession`, `GWSEAM-MaterializeChild`, `GWSEAM-RecyclePod`, `GWSEAM-RetirePod`, and `GWSEAM-ResumeSuspended`. It lands the pod credential API, `pkg/binding`, and that API's listener with no pod-side caller until 0127.S3 and 0127.S4 use them, which its tests cover through a test client. 0119.S2 (AR-B) of [`0119-agent-runtimes-only`](../0119-agent-runtimes-only/change.md) lands before it, an ordering that holds since 2026-11-13, because AR-B removes service mode and withdraws this change's proposal 0, so `pkg/binding` reads no service-mode pin record and no step here changes service-mode code.

Requests from other changes: 0127.S2 carries `cacheUntil`, the asserted route and its level 2 contract row, with its level 1 decision call in 0127.S7, the readable entries, `pkg/binding`'s user fields, the provisional exclusion, the link provider endpoints, and the head read deadline. From [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md#changes-requested-of-0127-egress-and-git), 0127.S2 carries the Token Service's own check of each cause ("Git mirror (`lenny-git-mirror`)", **Fill causes**). From [`0143-service-account-credentials`](../0143-service-account-credentials/change.md), 0127.S2 carries the token, discovery, and exchange endpoints of `clientCredentials` and `issuerExchange` types on the mint path, and the write-time case for a candidate issuer's well-known metadata paths. From [`0151-user-credential-delivery`](../0151-user-credential-delivery/change.md) and [`0152-user-git-identity`](../0152-user-git-identity/change.md), 0127.S2 carries `pkg/binding`'s watch of `PodClaim` objects with the Token Service's `podclaims` rights, the eviction of a refused token from the mint cache under `reauth-on-401`, and the writable entry's separate user verdict. From [`0128-forensics-evidence-preservation`](../0128-forensics-evidence-preservation/change.md), 0127.S2 carries `pkg/binding`'s resolution of a slot index and a time to a session.

Requests still open from [`0148-tenant-outbound-connector`, "Changes requested of 0127-egress-and-git"](../0148-tenant-outbound-connector/change.md#changes-requested-of-0127-egress-and-git), which land with this step and with 0148.S3 (OC-3) or 0148.S1 (OC-1) as named:

- With OC-3: the decision service honours the API gateway's assertion for a tenant's outbound hosts on a probe, and for an instance named by the same identity.
- With OC-1: `pkg/binding`'s informer covers the outbound gateway's namespace, for the decision service alone.

### 0127.S3 Pod git path
- status: Planned
- aliases: [EG-3, proposal 3]
- lanes: [specification, code, documentation]
- depends-on: [0127.S1, 0127.S2, 0127.S9]
- closes: [F-26.2.11, F-14.1.37]

GitLab token reuse, stored-token push under `allowStoredTokenPush` and the `storedTokenPush` grant, `allowWorkflowEdits`, the `git.operation` stream from `netagent`, `allowedRefPatterns`, commit identity, `gitAccess` and its effective-policy formula, the covered host set with its push and its alias data, the per-pod CA and its name constraints, termination and the git path on the egress endpoint with its fail-closed rules and LFS response handling, the terminated marker for covered hosts in decisions and in the decision cache's key, the coordination generation wait before materialization, the pod trust bundle in git's trust, push with minted tokens, `writableRepositories`, the rendered git config and per-host `sshForms` rules, the `origin` remote, LFS, redirect handling, `gc.auto=0` and `maintenance.auto=false` in the rendered configuration, the fetch grant with `CH-FETCHGRANT` on the slot attribution of 0127.S2, the adapter's check of `netagent`'s UID on `@lenny-netagent-grant`, `netagent`'s handling of the helper's fetches, the gateway's Go `ls-refs` resolver through `lenny-egress`, the removal of the gateway clone path (which closes F-14.1.37), and the removal of `git-credential-lenny` and `lenny/vcs_token`. The pod-side clone itself, the pinned git, and the helper's git steps land with [`0153-workspace-materialization`](../0153-workspace-materialization/change.md)'s proposals, and the workspace ownership item needs no fix of its own (decision 51). Where the removal of the gateway clone path lands relative to the pod-side clone of 0153.S4 is open decision 73.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It removes `GWSEAM-IssueVCSToken` with `lenny/vcs_token`.

0127.S3 lands before 0139.S2 (RP-B) of [`0139-runtime-protocol`](../0139-runtime-protocol/change.md), an ordering that holds since 2026-11-12, because `cmd/git-credential-lenny` dials the platform MCP socket, which RP-B removes, and 0127.S3 removes `git-credential-lenny`. The fetch grant of 0127.S3 lands with or before 0153.S4, the git step of `lenny-materialize` in [`0153-workspace-materialization`](../0153-workspace-materialization/change.md), which depends on 0127.S3, because the helper's fetches need the grant's treatment, and the grant has no caller without the helper's git.

Requests from other changes: 0127.S3 carries the push option refusal, client-visible remotes, and the exception for a session under `allowRuntimeHeldCredentials`. From [`0141-runtime-routing`](../0141-runtime-routing/change.md) and [`0140-runtime-revisions`](../0140-runtime-revisions/change.md), 0127.S3 runs its pool checks at the post-chain point ("Effective policy", **Where the pool checks run**). This request creates no dependency: until RT-2's post-chain point lands, the checks run on the final pool where the create path checks it today. From [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md#changes-requested-of-0127-egress-and-git), 0127.S3 carries the gateway cause assertion and ref resolution after the post-chain point ("Credentials", **The gateway runs no git.**). Until RT-2's post-chain point lands, ref resolution runs after the last `PreRoute` segment and the experiment rewrite, where TS-1 runs its final-row checks.

Requests still open from [`0148-tenant-outbound-connector`, "Changes requested of 0127-egress-and-git"](../0148-tenant-outbound-connector/change.md#changes-requested-of-0127-egress-and-git), which land with this step as named:

- After the outbound connector's spike: the 1.5-second CONNECT reply budget of the ref resolver depends on the route target kind, if that spike shows that wide-area dials exceed it. Which step runs that spike is open as O4 of [`0148-tenant-outbound-connector`](../0148-tenant-outbound-connector/decisions.md), and this request names that step once O4 is settled.
- With 0148.S1 (OC-1): the ref resolver maps a certificate failure after the CONNECT's 200 to `GIT_CLONE_REF_RESOLVE_TRANSIENT`, which **Errors** under "Credentials" omits.

### 0127.S4 Credential delivery
- status: Planned
- aliases: [EG-4, proposal 4]
- lanes: [specification, code, documentation]
- depends-on: [0127.S2, 0127.S3, 0127.S9]

Credential bindings with `inject` delivery (plain-HTTP upgrade, and EG-3's termination extended to bound hosts), `federation` delivery with the Token Service as OIDC issuer and the AWS, GCP, and Azure templates, `metadata` delivery with the AWS plugin, and `file` delivery with the generic directory and the `netrc` projection. It also covers the network descriptor, the SDK network accessor, and tenant client config files with their name pattern. From [`0143-service-account-credentials`](../0143-service-account-credentials/change.md), 0127.S4 carries `metadata` delivery of a `userCredential` binding for an `awsWebIdentity` type, the binding name pattern, the federation key set's RS256 signing and key pre-publication, and the `/exchange/` path.

Requests from other changes: 0127.S4 carries `handling`, the asserted route and its pod credential API fields, the inspection stage, the typed `secret`, the one-binding rule, and the granted-entries rule on a binding mint.

Requests still open from [`0148-tenant-outbound-connector`, "Changes requested of 0127-egress-and-git"](../0148-tenant-outbound-connector/change.md#changes-requested-of-0127-egress-and-git), which land with this step and with 0148.S2 (OC-2): a binding with `secret: {kind: none}` is refused on a covered host. The credential-pinned half of the refusal lands with 0127.S12.

Under decision 22 of 0133, the step also lands the platform and pool bindings of "Credential bindings" and **Binding suspension.** through the pod binding API, which 0170.S6 calls, with `netagent`'s answers to a suspended session's requests under decision 85 and the removal of the placeholder from other headers on an attribution binding's hosts. Open decision 84 settles the scope of the platform and pool bindings.

### 0127.S5 Git mirror and warm fill
- status: Planned
- aliases: [EG-5, proposal 5]
- lanes: [specification, code, documentation]
- depends-on: [0127.S2, 0127.S3, 0127.S9]

`netagent`'s attribution of the `lenny-store-fill` UID as the warm fill and the fill's authorization (decision 61), `prestage: true` on a `gitRepository` warm entry (decision 67), the held flag and region and key fields on stores, the `git.operation` stream from the mirror, `lenny-git-mirror` serving TLS, with its anti-affinity, `gitMirror.hostUsers` and its start check, the ID ranges and fetch UID reuse rule, its sandbox and capability exception, per-store ownership, the fetch retry window, the mirror prefetch resource, read after push through the mirror route, and the rules of the 0111 rewrite that open decision 69 places in this change.

This step builds the warm list of the rewritten draft 0111 and the rules that hold on every pool: the carrier, rendered for every pool, the store volume and its sizing, the age limit, the drain, the claim refusal through `coveredStores` for platform entries, the backoff, the fill limit for shared pools, and the tree bound. 0147.S8 of [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/steps.md#steps) builds the parts that need an owned pool, under the split that ["Relationship to draft proposal 0111"](../0147-tenant-derived-runtimes/change.md#relationship-to-draft-proposal-0111) in that change states.

The `lenny-store-fill` container and its volumes are rendered by 0118.S4 (PL-B) of [`0118-agent-pod-layout`](../0118-agent-pod-layout/change.md), and the fill program, the store fill, and the pristine trees land in 0153.S9 of [`0153-workspace-materialization`](../0153-workspace-materialization/change.md), which depends on 0127.S5. Of the fill, this step owns only `netagent`'s attribution of the fill's UID. For platform entries on the warm list, whichever of 0127.S5 and 0149.S1 (TS-1) lands second adds the environment predicate of decision 48 at write and at render, as [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md) TS-1 states; 0147.S8 owns the predicate for tenant entries.

Requests from other changes: 0127.S5 carries the repository in the egress decision API check. From [`0141-runtime-routing`](../0141-runtime-routing/change.md) and [`0140-runtime-revisions`](../0140-runtime-revisions/change.md), 0127.S5 keys the warm list, its hosts, and the readable set before binding on pool and revision. This request creates no dependency: until revisions land, every runtime has one revision. From [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md#changes-requested-of-0127-egress-and-git), 0127.S5 carries the mirror's fill causes and the prefetch record's pool.

### 0127.S6 Per-tenant routing and replacement
- status: Planned
- aliases: [EG-6, proposal 6]
- lanes: [specification, code, documentation]
- depends-on: [0127.S2, 0127.S9]
- closes: [F-13.2.28, F-13.2.29, F-13.2.31]

Upstream registrations and chaining, private destination pinning, egress groups with API assignment, exclusivity, and the `lenny.dev/egress-group` label, per-endpoint and per-binding CAs, tenant-registered public services, and deployer CA trust distribution. It closes F-13.2.29 beside the new label decider, by checking map presence for every immutable label, and F-13.2.31, by enforcing the NET-003 creation guard with §13.2's statement of the creating identity.

### 0127.S7 Level 1 replacement
- status: Planned
- aliases: [EG-7, proposal 7]
- lanes: [specification, code, documentation]
- depends-on: [0127.S2, 0127.S3, 0127.S5]

Identities and their rotation, the policy document, its host grammar, the EDL endpoint, the reference Squid renderer, the decision call and tunnel reports on the pod binding API, the `netagent` server-name check, the service identities for the ref resolver and the mirror, the Helm-rendered private destination grants and tenant-scoped policies, the direct path for `netagent`, the ref resolver, and the mirror, the validators' refusals, the acknowledgement, the install order with its preflight and post-install probe, the canary, and the documentation of its limits.

0127.S7 depends on 0127.S5 only for the mirror's service identity.

Requests from other changes: 0127.S7 carries the level 1 decision call for the asserted route that 0127.S2 carries.

### 0127.S8 Tenant visibility
- status: Planned
- aliases: [EG-8, proposal 8]
- lanes: [specification, code, documentation]
- depends-on: [0127.S4, 0127.S6, 0127.S10]

Decision queries and export, the effective-policy read, the shadow policy, the egress address read, and fixed-name files for more registries.

### 0127.S9 Egress policy objects
- status: Planned
- aliases: [EG-9, proposal 9]
- lanes: [specification, code, documentation]
- closes: [F-5.2.67 (part), F-26.2.12, T-26.2.3]

Pool ownership (`ownerTenantId`, taken from draft 0111 because the formulas depend on it) with the controller's owner stamp on tenant-owned pods at creation and its `unset → owner` label-webhook rule (§5.2), `lenny.dev/served` stamped at first assignment and read by both §5.2 **Pinned idle inventory** and `AdmitTenantPin` on a tenant-owned pool, the refusal of an unlabelled pod on a tenant-owned pool, the cascade of a pool's deletion to its overlays, grants, and level 1 identities, ceilings, warm-up policies, tenant egress policy, session `egress.allowedHosts`, tenant pool overlays, host sets, the client proxy profile, the removal of category-keyed defaults from §26.2, the `egressProfile` write restriction, the per-class gate grants, one write path for opt-ins, and named permissions on the new reads.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It adds the session egress inputs and their refusals and the delegated child exception behind `GWSEAM-CreateSession`, `GWSEAM-MaterializeChild`, and `GWSEAM-CheckChildCredentials`.

Requests from other changes: 0127.S9 carries the `userCredentialDelivery` gate, the `userCredentialMetadataDelivery` gate of [`0143-service-account-credentials`](../0143-service-account-credentials/change.md), the delegated child exception, and `lockEgressToHosts`. [`0147-tenant-derived-runtimes`](../0147-tenant-derived-runtimes/steps.md) relies on `lenny.dev/served` landing here with `ownerTenantId`. From [`0141-runtime-routing`](../0141-runtime-routing/change.md) and [`0140-runtime-revisions`](../0140-runtime-revisions/change.md), 0127.S9 runs its pool checks at the post-chain point ("Effective policy", **Where the pool checks run**), and it keys the warm list, its hosts, and the readable set before binding on pool and revision. Neither request creates a dependency: until RT-2's post-chain point lands, the checks run on the final pool where the create path checks it today, and until revisions land every runtime has one revision.

Requests still open from [`0148-tenant-outbound-connector`, "Changes requested of 0127-egress-and-git"](../0148-tenant-outbound-connector/change.md#changes-requested-of-0127-egress-and-git), which land with this step and with 0148.S2 (OC-2): `tenantOverride.outboundHosts` joins the overlay table under "Tenant pool overlays".

### 0127.S10 Evidence ingest
- status: Planned
- aliases: [EG-10, proposal 10]
- lanes: [specification, code, documentation]
- depends-on: [0127.S11]

The envelope, `lenny-evidence`, `EvidenceStore`, the compactor, the chain anchor, retention, erasure, the held marker, and the regulated write-ahead path.

0127.S10 needs 0127.S11 for its certificates. It lands with no producer until the others use it, so a decision service never ships without its evidence format.

### 0127.S11 Egress PKI
- status: Planned
- aliases: [EG-11, proposal 11]
- lanes: [specification, code, documentation]

The egress CA, its Issuer and leaves with their `lenny.dev/doctor-optout` annotation, the `lenny-egress-ca` ConfigMap, and the rotation procedure, with no consumer until the others use it.

Requests still open from [`0148-tenant-outbound-connector`, "Changes requested of 0127-egress-and-git"](../0148-tenant-outbound-connector/change.md#changes-requested-of-0127-egress-and-git), which land with this step and with 0148.S1 (OC-1):

- The outbound gateway's namespace becomes a target of the `lenny-egress-ca` ConfigMap, with the leaf copy and its narrow Secret rights.
- The chart renders an egress PKI leaf for the outbound gateway and one for PgBouncer, whose hop from the outbound gateway uses TLS with `verify-full` ("Transport security", **The egress PKI.**).

### 0127.S12 Exfiltration controls
- status: Planned
- aliases: [EG-12, proposal 12]
- lanes: [specification, code, documentation]
- depends-on: [0127.S2, 0127.S3, 0127.S4]

`credentialPinned` and read-only hosts, added to EG-3's terminated marker, and name constraints that cover those hosts. The other steps do not wait for it, and until it lands the opaque-tunnel limits under "Residual risks and limits" apply to every host EG-3 does not cover.

0127.S12 depends on 0127.S3 because it adds credential-pinned and read-only hosts to EG-3's terminated marker.

Requests from other changes: 0127.S12 carries a VCS pool with `userAuthorization: required` as a credential pin source. Whichever of 0127.S12 and 0152.S2 (UG-2) of [`0152-user-git-identity`](../0152-user-git-identity/steps.md#steps) lands second builds the VCS-pool pin wiring, `pinHosts`, the pin exception, and the pin suspension.

Requests still open from [`0148-tenant-outbound-connector`, "Changes requested of 0127-egress-and-git"](../0148-tenant-outbound-connector/change.md#changes-requested-of-0127-egress-and-git), which land with this step and with 0148.S2 (OC-2): a binding with `secret: {kind: none}` is refused on a credential-pinned host.

### Notes on the steps

0127.S1, 0127.S9, and 0127.S11 are independent of one another. Apart from the dependencies of 0127.S2 on 0119.S2, 0145.S1, and 0118.S4 and the orderings that 0127.S3 and 0127.S5 state, no step of this change depends on a step of another change.

Proposal 0114, the shared gateway key-source proposal, which fixes F-7.1.31 (each gateway process seeds its own `uploadToken` HMAC key ring, so another replica refuses an upload token it issued), is outside the v1 program and is not part of the target state; no step of this change depends on it. R14 is a separate remediation step that this change does not absorb. Later work covers push forwarding through the mirror and the GCP and Azure `metadata` plugins.

The outbound connector design's request that a ceiling of `*` match outbound connector hosts, which its pool-ceiling decision settled, is applied under "Terms" and "An open ceiling".

**Labels and dependencies across designs.** Other design documents name these steps EG-1 to EG-12, and each document states its own dependencies, which govern where this table is shorter. Steps from different changes can interleave, as long as these hold:

| Step | Depends on | Why |
| :-- | :-- | :-- |
| [0142.S1 (SO-1)](../0142-security-oversight/change.md#so-1-security-roles) (`security-auditor` role) | 0127.S8 (EG-8), 0127.S9 (EG-9) | The named permissions on the new reads |
| [0142.S2 (SO-2)](../0142-security-oversight/change.md#so-2-tenant-security-record-and-posture) (tenant guardrails) | 0127.S9 (EG-9), 0127.S2 (EG-2), 0127.S4 (EG-4), [0142.S1 (SO-1)](../0142-security-oversight/change.md#so-1-security-roles) | One write path for opt-ins, the suspended state, and the role that writes the record |
| [0142.S3 (SO-3)](../0142-security-oversight/change.md#so-3-per-tenant-siem-destination) (per-tenant SIEM) | 0127.S8 (EG-8), 0127.S10 (EG-10) | The evidence export |
| [0142.S7 (SO-5b)](../0142-security-oversight/change.md#so-5b-repository-write-approvals) (repository write approvals) | 0142.S6 (SO-5a), 0127.S3 (EG-3) | The store and the writable set records |
| [0128.S2 (FE-1)](../0128-forensics-evidence-preservation/change.md#fe-1-quarantine) (quarantine) | 0127.S2 (EG-2), 0127.S7 (EG-7) | The `quarantined` binding state, and the same selector exclusion in level 1's tenant policies |
| [0128.S3 (FE-2)](../0128-forensics-evidence-preservation/change.md#fe-2-mirror-store-hold) (mirror store hold) | 0127.S5 (EG-5) | The held flag |
| [0128.S4 (FE-3)](../0128-forensics-evidence-preservation/change.md#fe-3-platform-only-holds-and-a-security-retention-class) (platform-only holds) | 0127.S10 (EG-10) | The held marker on manifests |
| [0128.S5 (FE-4)](../0128-forensics-evidence-preservation/change.md#fe-4-reconciler) (reconciler) | 0127.S2 (EG-2), 0127.S3 (EG-3), 0127.S5 (EG-5), 0127.S10 (EG-10) | The streams it reconciles |
| [0128.S7 and 0128.S8 (FE-6)](../0128-forensics-evidence-preservation/change.md#fe-6-agent-activity-streams) (agent activity) | 0127.S10 (EG-10) | The envelope, ingest, and key field |
| [0149.S1 (TS-1)](../0149-tenant-scoping-identity/change.md#ts-1-environment-scoping) (environment scoping) | 0127.S1 (EG-1), 0127.S2 (EG-2), 0127.S3 (EG-3), 0127.S4 (EG-4), 0127.S9 (EG-9), and [0149.S3 (TS-3)](../0149-tenant-scoping-identity/change.md#ts-3-session-environment-context-and-cross-environment-sharing); 0127.S5 (EG-5) for its mirror steps, 0127.S6 (EG-6) for its route step, and 0127.S8 (EG-8) with the envelope field of 0127.S10 (EG-10) for its read filters | The objects it scopes, the tenant-object resolver, the git inputs, and a dependable session environment |
| [0149.S11](../0149-tenant-scoping-identity/steps.md#steps) (cross-environment sharing), which carries the evidence field step | 0127.S10 (EG-10) | Both environments on `credential.mint` and on evidence |
| [0148.S1 (OC-1)](../0148-tenant-outbound-connector/steps.md#steps) (connector) | 0127.S2 (EG-2), 0127.S6 (EG-6), 0127.S8 (EG-8), 0127.S9 (EG-9), 0127.S10 (EG-10), 0127.S11 (EG-11) | Typed route targets, the mint path, the forwarded route template, evidence, and certificates |
| [0148.S2 (OC-2)](../0148-tenant-outbound-connector/steps.md#steps) (registration) | 0127.S4 (EG-4), 0127.S6 (EG-6), 0127.S8 (EG-8), 0127.S9 (EG-9), 0127.S12 (EG-12) for the credential-pinned half of the `secret: {kind: none}` refusal, [0148.S1 (OC-1)](../0148-tenant-outbound-connector/steps.md#steps) | The typed `secret` union and termination of bound hosts for `secret: {kind: none}`, credential-pinned hosts, the endpoint record, the visibility reads, the policy objects, and the outbound gateway |
| [0147.S4 (DR-4)](../0147-tenant-derived-runtimes/change.md#dr-4-tenant-runtime-records) (tenant runtime records) | 0147.S1 (DR-1), 0127.S9 (EG-9), [0149.S3 (TS-3)](../0149-tenant-scoping-identity/change.md#ts-3-session-environment-context-and-cross-environment-sharing) | The write path and gate model |
| [0147.S5 (DR-5)](../0147-tenant-derived-runtimes/change.md#dr-5-owned-pools) (owned pools) | 0147.S2 (DR-2), 0147.S4 (DR-4), 0127.S9 (EG-9) | `ownerTenantId` with the label rule |
| [0147.S7 (DR-9)](../0147-tenant-derived-runtimes/change.md#dr-9-default-git-clones) (default git clones) | 0147.S3 (DR-3), 0147.S4 (DR-4), 0147.S5 (DR-5), 0127.S2 (EG-2), 0127.S3 (EG-3), and the plan check of [0144.S1 (SD-1)](../0144-session-derive/change.md#sd-1-one-route) | The slot `sizeLimit`, the pod-side clone, and an owned pool's git path |
| 0147.S8 (DR-11) (shared assets and warm stores) | 0147.S7 (DR-9), 0127.S2 (EG-2), 0127.S5 (EG-5) | The slot `sizeLimit`, and the rewritten 0111's warm list, store, slot repository, capture copy, and restore completion |
| [0152.S1 (UG-1)](../0152-user-git-identity/change.md#ug-1-vcs-link-kinds) (VCS link kinds) | [0151.S2 (UC-2)](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking), [0151.S7 (UC-7)](../0151-user-credential-delivery/change.md#uc-7-add-ons), 0127.S1 (EG-1), 0127.S2 (EG-2), 0127.S9 (EG-9) | The link flow, the kind registry, VCS pool hosts, the mint path, and the write path |
| [0152.S2 (UG-2)](../0152-user-git-identity/change.md#ug-2-repository-authorization) (authorization) | [0152.S1 (UG-1)](../0152-user-git-identity/change.md#ug-1-vcs-link-kinds), [0151.S3 (UC-3)](../0151-user-credential-delivery/change.md#uc-3-the-usercredential-binding), [0151.S7 (UC-7)](../0151-user-credential-delivery/change.md#uc-7-add-ons), 0127.S1 (EG-1), 0127.S2 (EG-2), 0127.S3 (EG-3), 0127.S5 (EG-5), [0149.S4 (TS-4)](../0149-tenant-scoping-identity/change.md#identity-model-ts-2-ts-4-and-ts-6-to-ts-9) | The session input check, `retireOnRelease`, the release check point, the mint path, `pkg/binding`, entries, the mirror, and the lifecycle hook |
| [0152.S3 (UG-3)](../0152-user-git-identity/change.md#ug-3-pull-request-authoring) (PR authoring) | [0152.S2 (UG-2)](../0152-user-git-identity/change.md#ug-2-repository-authorization), [0151.S2 (UC-2)](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking), [0151.S3 (UC-3)](../0151-user-credential-delivery/change.md#uc-3-the-usercredential-binding), [0151.S4 (UC-4)](../0151-user-credential-delivery/change.md#uc-4-request-rules-and-presets), [0151.S7 (UC-7)](../0151-user-credential-delivery/change.md#uc-7-add-ons), 0127.S3 (EG-3) with git path T | The binding, its rules, the add-on extension points, and `gh`'s remotes |
| [0152.S4 (UG-4)](../0152-user-git-identity/change.md#ug-4-independent-approval-check) (approval check) | [0151.S2 (UC-2)](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking), 0127.S3 (EG-3), 0127.S10 (EG-10) | The link and `git.operation` |
| [0130.S2 (GL-2)](../0130-gitlab-credential-addon/change.md#gl-2-user-authored-merge-requests) (GitLab user-authored merge requests) | [0130.S1 (GL-1)](../0130-gitlab-credential-addon/change.md#gl-1-the-gitlab-kind), [0152.S3 (UG-3)](../0152-user-git-identity/change.md#ug-3-pull-request-authoring) | The kind, the token path, and spikes [S13](../0130-gitlab-credential-addon/steps.md#validation-owed) and [S17](../0130-gitlab-credential-addon/steps.md#validation-owed) |
| [0151.S1 (UC-1)](../0151-user-credential-delivery/change.md#uc-1-credential-types-and-the-registered-source) (types and `registered`) | 0127.S9 (EG-9) | The write path |
| [0151.S2 (UC-2)](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking) (`oauth2Link` and account linking) | [0151.S1 (UC-1)](../0151-user-credential-delivery/change.md#uc-1-credential-types-and-the-registered-source), 0127.S1 (EG-1), 0127.S2 (EG-2), 0127.S9 (EG-9), [0149.S6 (TS-6)](../0149-tenant-scoping-identity/change.md#identity-model-ts-2-ts-4-and-ts-6-to-ts-9), and [0148.S1 (OC-1)](../0148-tenant-outbound-connector/steps.md#steps) and [0148.S2 (OC-2)](../0148-tenant-outbound-connector/steps.md#steps) for a private host | The digest format, the mint path, the write path, and the login path |
| [0151.S3 (UC-3)](../0151-user-credential-delivery/change.md#uc-3-the-usercredential-binding) (`userCredential` binding) | [0151.S1 (UC-1)](../0151-user-credential-delivery/change.md#uc-1-credential-types-and-the-registered-source), 0127.S2 (EG-2), 0127.S4 (EG-4), [0149.S4 (TS-4)](../0149-tenant-scoping-identity/change.md#identity-model-ts-2-ts-4-and-ts-6-to-ts-9) | Bindings, the pod credential API, `pkg/binding`, and the invalidation level |
| [0151.S4 (UC-4)](../0151-user-credential-delivery/change.md#uc-4-request-rules-and-presets) (request rules) | [0151.S3 (UC-3)](../0151-user-credential-delivery/change.md#uc-3-the-usercredential-binding), 0127.S4 (EG-4) | The stage |
| [0151.S5 (UC-5)](../0151-user-credential-delivery/change.md#uc-5-delivery-file) (`file`) | [0151.S3 (UC-3)](../0151-user-credential-delivery/change.md#uc-3-the-usercredential-binding), 0127.S4 (EG-4), 0127.S9 (EG-9) | The mechanics and the gate |
| [0151.S6 (UC-6)](../0151-user-credential-delivery/change.md#leak-scanning-uc-6) (leak scanning) | [0151.S5 (UC-5)](../0151-user-credential-delivery/change.md#uc-5-delivery-file) | Optional |
| [0151.S7 (UC-7)](../0151-user-credential-delivery/change.md#uc-7-add-ons) (add-ons) | [0151.S2 (UC-2)](../0151-user-credential-delivery/change.md#uc-2-the-oauth2link-source-and-account-linking), [0151.S3 (UC-3)](../0151-user-credential-delivery/change.md#uc-3-the-usercredential-binding), [0151.S4 (UC-4)](../0151-user-credential-delivery/change.md#uc-4-request-rules-and-presets) | The kind block, the registry, and the extension points those proposals call |
| [0124.S2 (CR-B)](../0124-connector-removal/steps.md#steps) of `0124-connector-removal` (identity assertion) | 0127.S2 (EG-2), 0127.S4 (EG-4), 0127.S10 (EG-10) | The pod credential API, `pkg/binding`, and slot attribution; the typed `secret` union and `inject`; and the `credential.mint` stream |
| [0124.S3 (CR-C)](../0124-connector-removal/steps.md#steps) of `0124-connector-removal` (egress halt) | 0127.S2 (EG-2), 0127.S3 (EG-3) | Slot attribution, and the fetch-grant channel that `CH-EGRESSHALT` follows |

The other GitHub and GitLab add-on steps depend on UC and UG steps and on no EG step directly. [`0129-github-credential-addon`, "Steps"](../0129-github-credential-addon/steps.md#steps) and [`0130-gitlab-credential-addon`, "Steps"](../0130-gitlab-credential-addon/steps.md#steps) state their dependencies.

## Validation still owed

- **Slot attribution:** the runtime-class question that the owner asked a spike to settle on 2026-11-13 ("run a spike to test this") was measured on 2026-11-14 with a test listener on one EC2 node under k3s ("Slot attribution and fetch grants"): runc answers `sock_diag`, gVisor with either network mode and Kata with either volume mode answer only `/proc/net/tcp`, and every answering reader gave the right UID. Each pool's attribution status still decides where [`0151-user-credential-delivery` decision 41](../0151-user-credential-delivery/decisions.md#decisions) and [`0152-user-git-identity` decision 52](../0152-user-git-identity/decisions.md#decisions) admit user-credential bindings, `lockEgressToHosts`, and user git identity. What stays owed: the production `netagent` with the state and UID rule, the batched read, and the start-up check, under a burst of connections on gVisor and Kata; the managed runtimes of EKS, GKE Sandbox, and AKS's Kata node pools; a Kata guest kernel built with `CONFIG_INET_DIAG` and `CONFIG_INET_TCP_DIAG`, which the spike did not build; and the per-slot scopes and the platform marker of decisions 60 and 64 end to end. Waiting step: 0127.S2.
- **Harness clients through `netagent`** ([`0124-connector-removal`, "Validation owed"](../0124-connector-removal/steps.md#validation-owed)): the harness-egress spike of 2026-11-15 ran on loopback with a Go stand-in, and a run of Claude Code and Codex through the production `netagent` in a pod on runc, gVisor, and Kata stays owed with that design's CR-B. Waiting step: 0124.S2 (CR-B), after 0127.S2.
- **Identity assertions on pods with several slots** (the same design, O7): slot attribution is measured, and the attachment of each slot's own assertion on runc, gVisor, and Kata is not, which stays owed with CR-B. Waiting step: 0124.S2 (CR-B), after 0127.S2.
- **`lenny-store-fill`** (decision 61): the container fills the stores and their pristine trees under its own UID with only the store volume writable; the stores are written with the store group as the file group, which is the primary group of the fill and the adapter and which no slot container holds, and a umask of `0002` (directories `0775`, object files `0444`), and each pristine tree takes an explicit mode pass (directories `0775`, files `0444`, executables `0555`, setuid and setgid bits cleared), so every slot UID reads them through the other class; its 1 GiB memory limit holds for a fill of the largest store a pool's `warmCloneBytes` admits, which counts each store and its pristine tree; it starts after the `netagent` native sidecar; `netagent` attributes its UID as the warm fill; the adapter deletes a withheld store through its read-write mount without parsing it; on a Kata `block-plain` pool, whose volume root carries the setgid bit and the dedicated volume GID, every store entry still carries the store group; and under user namespaces its UID maps within the pod's range. Waiting steps: 0127.S5 for the attribution of the fill's UID, and 0153.S9 for the fill, the modes, and the deletion.
- **Hostile-repository stress suite** (decision 61, at the owner's request of 2026-11-13 "to stress test this thoroughly (with actual tests)"): a suite of tests, run on runc, gVisor, and Kata, that fills and stages repositories carrying setuid and setgid files, symbolic-link and hard-link farms, deep and wide trees, mode-`000` directories, name collisions, and very large files, and that deletes a withheld store while a slot reads it. It checks that no slot gains write access to a store, the store group, or a set-ID bit, that neither the fill nor the deletion follows a link out of the volume, and that the deletion under the race completes or fails closed. Waiting step: 0153.S9, which lands the fill and the adapter's deletion.
- **`netagent.resources` defaults** (decision 66): the request of 100m CPU and 128 MiB and the 512 MiB memory limit, under a Chromium clone and under many concurrent tunnels. Waiting step: 0127.S2, for the default values only.
- **`gitMirror.heldUidBudget`** (decision 66): a quarter of the mirror's fetch-UID range, against concurrent fetches while stores are held. Waiting step: 0127.S5, for the default value only.
- **Commit identity selection** (decision 65): `includeIf "hasconfig:remote.*.url:<pattern>"` with the pinned git, on SSH remote forms, and on a runtime git older than 2.36. Waiting step: 0127.S3.
- **The fetch grant:** a helper run's fetch held for the fetch retry window on a first materialization and limited to 30 s on a resume, a grant re-registered after a `netagent` restart, and the `git.operation` record's cause, none of which has run. Waiting step: 0127.S3, with 0153.S4 for the helper's runs.
- **The pinned git under `lenny-materialize`:** a clone, a fetch through `netagent` with the pod trust bundle, alternates under `/lenny/git-stores`, the tree-size check, and the index refresh, with git exec-only at mode `0111`, which [`0153-workspace-materialization`](../0153-workspace-materialization/change.md) also lists. Waiting step: 0153.S4.
- **The warm-up checkout under the warm-up policy:** a staged checkout after a slot release reaches only the hosts of the pool's or the tenant's warm-up policy and holds no session credential. Waiting step: 0153.S8.
- **The constrained trust anchor on a pod with several slots,** where a later slot's binding re-issues the anchor while an earlier slot's runtime process holds the previous one. Waiting step: 0127.S3.
- **The git path, under T, in the production `netagent`:** the spike ran a prototype on a stub, which proves forwarding only. No GitHub or GitLab host ran. Waiting step: 0127.S3.
- **LFS on a live GitLab:** batch actions, a `Gitlab::LfsToken`, transfer paths, and the deploy token echo, which are **[source]** from GitLab's public source, with GitLab's form **[measured]** on a stub alone. `info/lfs` on `mint-coarse` and `stored` providers stays refused until this passes. Waiting step: 0127.S3.
- **LFS on GitHub beyond one action:** an upload, a request with a token, and a transfer. One anonymous download action was read **[live]**. Waiting step: 0127.S3.
- **The per-object `error` on a live store, and a terminated transfer path with no failure refusal,** which did not run. Waiting step: 0127.S3.
- **How provider edges treat an unknown server name,** for the alias limit. Waiting step: 0127.S3.
- **Claims on the git path that rested on the loopback rounds, with no T measurement:** Waiting steps: 0127.S5 for read after push through the mirror and the fetch retry window, 0153.S4 for the helper's clone, 0127.S6 for the pinned private address and 0127.S7 for the git path at level 1, and 0127.S3 for the rest.
  - read after push through the mirror route, which the spike did not implement;
  - the helper's clone in the slot under a fetch grant;
  - a stock git clone through a pinned private address (round 6), and the git path at level 1;
  - the fetch retry window and `netagent`'s hold on a mirror 503;
  - `go get` with `GOPROXY=direct`;
  - a `git-receive-pack` with push options on the terminated connection;
  - git on macOS, Windows, and amd64, the .NET SDK image, and git against `ghe.com` beyond the stub.
- **A rule per declared repository for an `sshForms` path change past the prefix,** which has not run. Waiting step: 0127.S3.
- **`allowRuntimeHeldCredentials` on production components:** the attach and each 401 state ran **[measured]** on a Token Service stand-in with no decision service. Waiting step: 0127.S3.
- **The route assertion's fall-through, the Token Service's own assertion, the two documented topologies under them, and the upstream pool key and closure on a route write,** which have not run. `netagent`'s assertion ran on a stand-in, and the per-client-CONNECT decision did not run. Waiting step: 0127.S2, with 0127.S7 for the level 1 decision call.
- **The `gitAccess` formula computed from a pool,** where a host held as data ran. Waiting step: 0127.S3.
- **The request inspection stage under deny rules, on a live host:** deny mode's bypass analysis ran on the stub, as [`0129-github-credential-addon`, "Validation owed"](../0129-github-credential-addon/steps.md#validation-owed) records ([S19](../0129-github-credential-addon/steps.md#validation-owed)), and what a host does with each spelling is owed. Waiting step: 0127.S4.
- **`inject` delivery in the production `netagent`:** round 10 ran the client matrix against a Go prototype written to this text, including httpx over HTTP/2 and a rustls client, so what remains is repeating it against the component that ships. Waiting step: 0127.S4.
- **Corporate gateways:** a hands-on test against at least one gateway in a deployer's environment, covering source-address identity through an egress group, and whether that gateway reads `X-Authenticated-User` on CONNECT. Public documentation does not settle the second point. Waiting step: 0127.S6.
- **GitHub:** whether installation token mints count against the 2,000-per-hour limit, measured against a test App or confirmed by GitHub support. Waiting step: 0127.S1, for the per-App counter's bound.
- **`ls-refs` against hosted providers:** GitHub, GitLab, Bitbucket Cloud, Azure DevOps, and AWS CodeCommit with real tokens on private repositories. Round 10 covered the protocol against a local Gitea. Waiting step: 0127.S3.
- **Ref resolution after the post-chain point:** confirm in the tree that the `kind: derive` and `kind: delegate` paths can resolve `gitClone` refs after the post-chain point of RT-2 and before the pod claim. Waiting step: 0127.S3.
- **Evidence streams:** the refusal rate of the commit bound on a production database disk, tenant-key KMS use and rewrap, escrow, a cloud object store's scan and index-read latency, a consumer read forced across a removed object, and the cause of one undelivered record in round 11. Waiting step: 0127.S10.
- **The gateway's group mismatch path,** which has no code yet. Waiting step: 0127.S6.
- **Level 1** against a commercial proxy and an EDL consumer. Waiting step: 0127.S7.
- **Mirror volumes:** chown and POSIX default ACLs on cloud block volumes, and the normalization helper on a node with `fs.protected_hardlinks=0`, which the chart refuses. Waiting step: 0127.S5.
- **Fsck on legacy repositories:** `lenny-materialize` clones, under the rendered `transfer.fsckObjects = true` and `GIT_CONFIG_GLOBAL=/dev/null`, public repositories whose history carries objects that git's fsck reports, and the result states whether the rendered configuration needs an `fsck.<msg-id>` setting. This has not run. Waiting step: 0127.S3, with 0153.S4.

## Validation record

**The rounds below ran git through the prior loopback endpoint, which is candidate L of decision 44. They measure the prior design and stay as history.** Results on Envoy, the decision service, the mirror's sandbox, delivery modes, and transport carry over where the git path played no part. Claims on the git path are re-measured in the spike recorded in `0152-user-git-identity`, or are listed under "Validation still owed".

The spike files for these rounds are under `evidence/egress-and-git-design/`. **Some early results have no saved output.** `spike-envoy`, `spike-proxy-alts`, and `spike-git` have no `RESULTS.md`. The Envoy throughput of about 2.7 GB/s has no saved measurement, and its logs hold only `docker stats` lines. The Smokescreen result that every requirement passed has source and no output. The research rounds and the SDK review have no files beyond `research-r3/NOTES.md`. These statements record what was observed at the time and cannot be checked from the repository.

First spike, which was research only:

- Client proxy support: credentials in proxy URLs failed for Java, headless Chromium, and Yarn berry, and for Node unless a flag is set.
- Corporate gateways: Zscaler enrolls each proxy user individually, and the other vendors identify workloads by source address and identity headers.
- GitHub App tokens can be limited to named repositories and permissions, for one hour.
- No other host combines one-repository scoping with short lifetimes, apart from CodeCommit.
- Artifactory and Nexus do not serve git fetch.
- `insteadOf` rewrites pushes.
- Protocol v2 serves any object that exists by SHA.
- CNI behaviour for ClusterIP policy differs between implementations.
- Projected service account tokens are pod-bound and file-delivered.
- Lenny facts: §5.1 forbids secrets in env, the supervisor renders env as command-line arguments, `lenny/vcs_token` returns stored tokens without scope checks, and F-14.1.37 breaks the gateway clone.

Second spike, which ran hands-on tests and further research:

- Envoy 1.40.2 and 1.39.6: CONNECT, ext_authz on CONNECT with 403, 407, and fail-closed 503, and decision metadata in logs all passed. SSRF filtering passed for hostnames and literals once the mapped ranges were added. `max_stream_duration` passed on CONNECT, while `max_connection_duration` failed. Chaining passed through forwarded CONNECT with an `ORIGINAL_DST` cluster, while `http_11_proxy` failed. Throughput reached about 2.7 GB/s.
- Squid 7.7: authentication, the external ACL helper, SSRF filtering, per-tenant parents, and the server-name check passed, with workarounds. Ending active tunnels failed.
- Smokescreen-based Go proxy: every requirement passed, including kill by lease. The test found two pitfalls.
- git 2.49 and git-lfs 3.6: the generic rewrite, `pushurl` behaviour, submodules, `go get`, the streaming prototype, partial and shallow clones, chunked push, redirect rewriting, LFS URL derivation, and git's tree protections all passed. Go's `net/http/cgi` failed for streaming output and for chunked pushes.
- Research:
  - GitHub token caching guidance and rate limits.
  - Calico and GKE post-DNAT policy matching.
  - Corporate gateway identity models.
  - ext_authz as the decision protocol, and AuthZEN's status.
  - EDL as an allowlist format.
  - CA trust variables by ecosystem.
  - Clients sending hostnames inside CONNECT.
  - Token file formats, and `gh` requiring HTTPS.
- Lenny internals:
  - The container and UID layout.
  - The embedded model.
  - The resource class applied to every container.
  - The ordering of warm-up, claim, and credential delivery.
  - Service mode receiving no lease.
  - The archive gate checks.
  - Tenant-admin permissions.
  - The current egress settings.

Third round, which ran hands-on tests and further research:

- **Envoy 1.40.2 and 1.39.6:**
  - The baseline passed again: CONNECT, gRPC ext_authz with allow, 403, 407, and fail-closed 503, the SSRF filter on 21 non-public targets, `max_stream_duration`, and chaining through ext_authz metadata.
  - ext_authz in HTTP mode passed over HTTP/2 only.
  - IPv6 passed.
  - DNS rebinding never reached a private address.
  - Load passed after raising the circuit breakers: 1,000 concurrent tunnels and 2,000 new CONNECTs per second with decisions.
  - Slow and failed decision services failed closed.
  - Upstream 407 and 403 replies were normalized with `custom_response`.
  - The server-name check through an internal listener passed, with the ECH caveat.
  - Two findings became requirements: the pod's `Proxy-Authorization` leaks to chained upstreams unless the decision removes it, and the default circuit breaker caps each cluster at 1,024 tunnels.
- **Kubernetes 1.35 on kind:**
  - Token isolation to `netagent` held, including under a shared process namespace, which §13.1 forbids and which served as a control.
  - Offline validation rejected a deleted pod's token about 70 ms after the watch event, against about 6 s for TokenReview.
  - Rotation happened at 80 to 88 percent of the lifetime.
  - Native sidecar ordering and restart isolation held.
  - The NXDOMAIN resolver for `proxied` pods worked with curl, Python, pip, Node, and Go.
  - kindnet enforced NetworkPolicy through a ClusterIP but not toward node-local endpoints.
- **Client tools:**
  - Maven 3.9 and 4.0 need `settings.xml` through `MAVEN_ARGS`.
  - Gradle 8 and 9 honour `JAVA_TOOL_OPTIONS`.
  - `gh`, `glab`, npm, and pip re-read their credential files on every invocation.
  - `glab` requires mode 0600 and supports plain-HTTP API calls.
  - `gh` refuses to run without a token.
- **Research:**
  - Secure web gateways and identity on undecrypted CONNECT.
  - GitHub minting limits.
  - Calico eBPF and Cilium post-NAT policy, from source.
  - Squid `external_acl_type` status.
  - Prior art for credential injection.
  - Envoy CONNECT maturity and advisories.
  - SSH and HTTPS URL forms per git host, checked with `git ls-remote`.
  - Exposure of environment variables.
- **Credential injection**, run with mitmproxy 12.3.1 as the stand-in for `netagent`, after a first attempt to write a custom interceptor stopped on a safety classifier:
  - mitmproxy intercepted only the bound hosts. Other hosts passed through as opaque tunnels, keeping their public certificate, and a placeholder sent to them arrived unchanged.
  - The header was overwritten on every request to a bound host, with or without a placeholder. A placeholder in the query was not replaced.
  - The plain-HTTP upgrade worked with no CA on the client.
  - These clients authenticated with placeholders only, while the upstream received the real secret:
    - curl
    - Python requests, httpx, and aiohttp
    - Node 24 fetch
    - Go
    - Java 21 HttpClient, over HTTP/2
    - `gh` 2.103.3, with `GH_ENTERPRISE_TOKEN`
    - npm, through `.npmrc`
    - pip, through a Basic placeholder in the index URL
  - Response scrubbing behaved as described under "Credential delivery to runtimes".
  - The CA kept its fingerprint across a restart.
  - Interception added about 2.7 ms at p50 per new connection.
  - curl reads only lowercase `http_proxy` for `http://` URLs, so the client profile keeps both cases.
- **Credential formats:**
  - The AWS container credentials endpoint worked with boto3 1.43, AWS CLI v1 and v2, and the AWS SDKs for Go v2, JavaScript v3, and Java v2. Each client fetched from a loopback endpoint and signed with the credentials. Each re-read a changed token file, never signed with an expired credential, and rejected a non-loopback plain-HTTP URI. A trailing newline in the token file broke every client.
  - `.netrc` support was uneven:
    - requests, pip, and Go read it by default and honour `NETRC`. Go uses it only over HTTPS.
    - git and GNU wget read `$HOME/.netrc` only.
    - curl needs `--netrc`.
    - httpx needs `NetRCAuth`, which demands mode 0600.
    - BusyBox wget, npm, Node fetch, and Java ignore it.
- **Envoy resilience, soak, and drain** (round 5): keepalive, outlier detection, active health checks, and one retry gave zero failed CONNECTs under endpoint kill and freeze; 200 long streams ran 20 minutes with flat memory; drain behaviour and the retry per-try timeout pitfall were characterized.
- **Production CNIs** (round 5, two-node kind): Calico 3.33 iptables, Calico 3.33 eBPF, and Cilium 1.20 with kube-proxy replacement enforced the `proxied` policy identically, allowing the eight permitted paths and dropping all thirteen denied destinations, including node-local ones.
- **Federation and metadata formats** (round 4, hands-on against fake provider endpoints):
  - AWS web identity from a config file alone: boto3, and the Go v2, JavaScript v3, and Java v2 SDKs assumed the role, signed, and re-read a changed token on refresh. Endpoints can also come from the file.
  - GCP external account: google-auth for Python, Node, and Go exchanged the token, with impersonation, and re-read it. Python and Node look up the project at Cloud Resource Manager during initialization.
  - Azure workload identity: azure-identity for Python, Node, and Go exchanged the token. The authority must be HTTPS, Node and Go need instance discovery disabled against a non-standard authority, and the tenant and client identifiers come from the environment or constructor arguments only. The token file is cached for 5 to 10 minutes.
  - GCE metadata emulation: Python, Node, and Go worked, with Python needing `GCE_METADATA_IP` as well.
  - Azure App Service managed identity emulation: Python, Node, and Go worked with one request each.
- **SDK review** (read-only):
  - The Go, Python, and TypeScript runtime SDKs contain no environment, subprocess, proxy, CA, or git handling.
  - The supervisor builds the launch environment from an empty one.
  - Embedded binaries exit on any controller flag they do not define.
  - Several gaps between §15.7 and the SDKs are listed under "Runtime SDKs".

Sixth round (2026-11-02), on the egress path for ref resolution and on level 1:

- **`ls-refs` through Envoy 1.40.2.** A Go client returned the same commit as `git ls-remote` from GitHub (branches and peeled annotated tags), GitLab, Bitbucket Cloud, Gitea, Forgejo, kernel.org, and sourcehut, with an absent ref and an absent repository classified correctly. No test used credentials, and the Azure DevOps sample repository answered 401, so private repositories and Azure DevOps remain owed.
- **Failures,** at 25 calls per second with one retry: one decision endpoint killed, 0 of 375 failed; one Envoy stopped, 0 of 300; one Envoy frozen, 0 of 300; a graceful drain, 0 of 375. With no retry, one stopped Envoy failed 88 of 300, so the retry is required. With every decision endpoint down, calls failed closed in 0.5 s (1.0 s with the retry); with both Envoys down in 2.0 s; and with both frozen in 3.0 s.
- **Hostile hosts.** An unbounded advertisement stopped at the byte cap in 16 ms, slow and hanging hosts stopped at 10 s, and redirects, malformed pkt-lines, and oversized pkt-lines were refused.
- **Raw tunnel to a pinned address.** Envoy terminated the CONNECT and tunnelled raw bytes to an `ORIGINAL_DST` cluster that read the address from the decision's dynamic metadata, for a name with no DNS record. The granted tenant reached the origin and another tenant got a 403. The byte cap held inside the tunnel, stock git 2.49 ran `ls-remote` and a shallow clone of 75 MiB through a pinned address, and with the decision service down the call failed closed. Push through the tunnel was not run.
- **Level 1 on stock Squid 7.7,** configured from a rendered policy document: per-identity lists held in both directions, the resolver worked through it, and the results under "Level 1 replacement" for credentials, SSRF, the server-name check, revocation, and rotation were observed there.

Seventh round (2026-11-02), on server TLS, with results under "Transport security". It used stand-ins for `netagent`, the mirror, and I7, on a laptop in Docker, so its numbers show relative cost and do not size production. It also ran mTLS on I3 with a client SAN allowlist under faults and with none, and a TLS readiness listener against each broken start state. Squid 7.7 (`squid-openssl`) served level 1 over TLS with Basic authentication: a valid credential passed, a wrong or missing credential got 407, and an unlisted host got 403. The `ls-refs` client worked through the TLS hop against GitHub and through the raw tunnel, with a stopped and a frozen replica each failing 0 of 300 calls with one retry, and a failed certificate check classified as transient.

Eighth round (2026-11-02), on the mirror's start sequence and `hostUsers`, with results under "Git mirror", **Capability posture**. It ran on kind with Kubernetes 1.35.0, containerd 2.2.0, runc 1.2.9, and kernel 6.12, using a shell stand-in for the mirror parent, and on Kubernetes 1.31.4 for the disabled gate. On kind, `/kind` needed its permissions opened before a `hostUsers: false` pod could start, because kind's container hook is otherwise not executable from a user namespace, which a Kind test of this mode must repeat.

Ninth round (2026-11-02), on evidence, with results under "Evidence streams". It used a stand-in decision service writing to MinIO and Postgres, Envoy's gRPC access log service with Envoy 1.40.2, and kind with kindnet for the label. The access log stream carried the decision identifier from ext_authz metadata, the upstream source port, and bytes, with two entries per tunnel (establishment and end). It delivered all closes under load, none from a killed Envoy, and all through the chart's drain. In the label test a pod was dropped before its stamp and open by the first probe after the label call returned, and a pod with another group's label stayed dropped. That test's timing is limited by the test's own commands, and it ran on kindnet only. The first load run in that round was invalid, because the harness exhausted ports even with evidence off, and was repeated.

Tenth round (2026-11-02 and 2026-11-03), closing owed items. Results are stated at each mechanism, and these did not fit there:

- **Name-constrained CA, 27 client configurations.** Every enforcing client refused an unpermitted DNS name, including through an unconstrained intermediate, and excluding IP ranges broke no permitted host. Java accepts everything, as stated. Firefox and .NET were not run.
- **Push through the raw tunnel.** Small, 64 MiB, and 1 GiB chunked pushes landed with the same SHA, at 24.1 s against 24.2 s direct, also over the TLS proxy hop, with Envoy at about 30 MB. A cut left the ref unmoved, and a retry landed. With `receive.keepAlive` disabled, a silent hook past the idle timeout returns an error while the ref lands, so git's default keepalive matters.
- **`ls-refs` with credentials,** against Gitea with a private repository, directly and through both Envoy routes: every classification agreed, and a mirror stand-in passed 72 of 72 fallback cases.
- **ClientHello check,** in a stand-in: match, mismatch, no SNI, non-TLS, split across segments, TLS 1.2 and 1.3 all behaved at 4 to 21 µs added. It must reassemble across records and canonicalize. Encrypted Client Hello and an inner `Host` pass, as stated.
- **Egress PKI with cert-manager on Kubernetes 1.35.** Real renewals closed 0 of 200 tunnels over 8 minutes, with 0 of 7,197 handshakes failed. cert-manager refuses a leaf under 1 hour, which any test must allow for.
- **Group labels on Calico and Cilium.** An unset and a wrongly labelled pod stayed dropped on both.
- **The label decider,** as a webhook and a ValidatingAdmissionPolicy, agreed on every identity and race, and it found the `pods/status` gap stated under egress groups. The gateway's claim path did not run, because no code for it exists.
- **The mirror's Go parent,** under `hostUsers` unset and false, held every boundary once launched as stated. NFS and cloud volumes were not available, and the normalization helper was tested for setuid and setgid bits only.

Also in the tenth round, a Go `netagent` prototype written to this text passed the client matrix: curl, Python requests, httpx including HTTP/2, aiohttp, pip, Node fetch, npm, Go, Java, `gh`, `glab`, reqwest on rustls, Deno, Bun, and git on OpenSSL and GnuTLS with a 64 MiB chunked push. Its memory and CPU figures are under "Network policy and placement". Opaque tunnels cost 0.5 CPU-seconds per GiB and intercepted ones 1.19 to 2.72, depending on the HTTP versions on each side. It is a prototype on a laptop, so these numbers show relative cost.

The spike files are under `evidence/egress-and-git-design/`, in the directories `spike-owed-c`, `spike-owed-a`, `spike-owed-b`, `spike-evidence`, `spike-userns`, `spike-tls`, `spike-lsrefs`, `spike-level1`, `spike-envoy`, `spike-envoy-r3`, `spike-proxy-alts`, `spike-git`, `spike-k8s`, `spike-tools`, `spike-inject`, `spike-formats`, `spike-federation`, `spike-cni`, `spike-envoy-r4`, and `research-r3`. That folder's `README.md` maps each round to its directories and lists what was left out, which includes binaries, downloads, keys, and bulk logs. The git path under T is recorded under `evidence/user-git-identity-design/spike-gh/`.

Eleventh round (2026-11-02), on evidence as a separate service, with results under "Evidence streams". It ran two `lenny-evidence` replicas, Envoy 1.40.2, MinIO, Postgres 17, and PgBouncer against revision 37. It changed the text in these ways: Envoy's stream reaches every replica, so one writer per tenant holds only for producers that hash; a replica crash needed its own detection, because the producer's incarnation does not change; an interleaved range can hide a lost chunk; aggregation needed contiguous ranges and a way to resolve a decision identifier; ingest had to drop its cached copy of a destroyed key; the commit bound refuses in bursts; a compacted row with its part list is too large to retain; and the scan estimate was too high.

Twelfth round (2026-11-03), on the items a laptop could run, with prototypes written to revision 42. It covered the tunnel count at a hard memory limit, level 1 with the Go `netagent` against Squid, the per-pod CA on Firefox and .NET, the evidence signals revision 42 added, reads and holds, more than one decision replica, the mirror's helper, and NFS. It changed the text in these ways: a client CONNECT to a terminated host needs its own decision, since pooled tunnels skipped it; `netagent` refreshes a credential on a 403 as well as a 407; a gap record states possible loss, and a consumer's cursor holds ranges; a replica's row needs a lease; the interval anchor has to name its batches, since a digest alone let a dropped batch pass the verifier; retention writes a chain event; erasure pushes to replicas, purges buffers, and honours holds; a tenant's later records are refused after deletion; the helper keeps hard links and restores `config`; and NFS is refused as a mirror volume.
