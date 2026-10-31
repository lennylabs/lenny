# Problem: Credential Secrets in a dedicated namespace, addable by reference or through the admin API

## Statement

Credential Secrets move into a dedicated namespace that holds only credentials. The Token Service reads that namespace namespace-wide, and a credential can be added in either of two ways. In variant A, an operator creates the Secret and registers it by `secretRef`. In variant B, Lenny writes the Secret from a value sent through the admin API. The owner decided this on 2026-10-30 and the decision is binding. The user request for this run confirms that both variants are in scope.

**Current state in the specification.** §4.9 specifies a `lenny-token-service-secrets` Role whose `resourceNames` list Helm fills at install time from the bootstrap `secretRef` values. It states that a credential added later needs a manual RBAC patch or a `helm upgrade`. It requires an admin-time live probe that refuses an unreadable Secret with `422 CREDENTIAL_SECRET_RBAC_MISSING`, and it skips that probe for bootstrap-seeded pools on the grounds that the chart renders `resourceNames` from the same values block. The §4.9 "Large pool Secret management (Tier 3)" paragraph refers to "`helm upgrade` on every credential addition". The §15.1 error row says the 422 body carries `details.resourceNames` and `details.rbacPatch`.

**Current state in the tree.** The tree does not match the specification:

- The chart renders no `lenny-token-service-secrets` Role and no Role, RoleBinding, or ClusterRole that grants the `lenny-token-service` ServiceAccount any access to Secrets. `token-service-deployment.yaml` renders only a Deployment, a ServiceAccount, a Service, and a PodDisruptionBudget. No chart template builds a `resourceNames` list from bootstrap `secretRef` values. The Role exists only as the §4.9 YAML example and as the role name inside `tokenServiceRBACPatch`.
- `tokenService.enabled` defaults to true. When it is true, the chart sets `--token-service-grpc-addr` and the gateway wires the Token Service secret prober. The Token Service answers each probe with a SelfSubjectAccessReview under a ServiceAccount that holds no Secret grant, so every probe returns `DENIED`. Every admin-API pool create, credential add, and `secretRef`-changing update on a stock install is therefore refused with 422.
- The 422 remediation from `tokenServiceRBACPatch` is `kubectl patch role lenny-token-service-secrets -n lenny-system`. It hard-codes the namespace, appends each failing ref verbatim to `/rules/0/resourceNames`, and targets a Role that does not exist. The code emits `details.missingSecrets` and `details.remediation`, while §15.1 documents `details.resourceNames` and `details.rbacPatch`.
- The Token Service reads no credential Secret value today. Its only Secret access is the probe in `pkg/tokenservice/secretprobe`. Its `AssignCredentials` path is dormant. The gateway reads VCS Secrets itself through `connectorsecret.KubeResolver`, and the gateway's RBAC grants no such read.
- `secretRef` is interpreted inconsistently. The gateway prober sends the whole ref as `SecretName` with no namespace. `secretprobe` resolves an empty namespace to `--secret-namespace` (default `POD_NAMESPACE`, else `lenny-system`), so a `namespace/name` ref is probed as a Secret literally named `namespace/name` and can never return `ALLOWED`. The §4.9 examples use that `lenny-system/<name>` form. `connectorsecret.parseRef` requires `namespace/name[/key]` and rejects a bare name. The chart's `defaultGithubVcsPool` comment asks for a bare name. `tokenServiceRBACPatch` appends the raw ref. The chart passes `secretRef` through to the bootstrap ConfigMap without parsing it. No single ref form passes both the probe and the read.
- `lenny-ops` holds namespace-wide `get`, `list`, `watch`, and `delete` on every Secret in the release namespace (`lenny-ops-namespace` Role). The gateway holds unrestricted `create` on Secrets there. A credential Secret placed in `lenny-system` today is readable by `lenny-ops`.

The dedicated namespace therefore gives the Token Service its first working Secret read path. It does not replace a deployed per-Secret Role.

**Decision to specify and stage.**

1. Credential Secrets live in a dedicated namespace that holds nothing else, named by a chart value such as `credentials.namespace: lenny-credentials`. The Token Service reads every Secret in that namespace through a namespace-wide `get`, with no `resourceNames` list. The proposal specifies the guard that keeps the namespace credential-only (admission, chart, or a documented operator obligation with a preflight check), the namespace's NetworkPolicy and RBAC, and how `lenny-ops`, the gateway, the controllers, and the other components are denied access to it.
2. Variant A: an operator with cluster access creates the Secret in that namespace and registers it by `secretRef` through the admin API or the bootstrap seed. The credential becomes usable without a `helm upgrade` and without an RBAC edit.
3. Variant B: a tenant admin or a platform admin sends the credential value through the admin API, and Lenny writes the Secret into the namespace. The credential routes that exist today are `POST /v1/admin/credential-pools/{name}/credentials`, `PUT .../credentials/{credId}`, `DELETE .../credentials/{credId}`, revoke, and re-enable. No per-credential rotate endpoint exists; rotation is revoke plus add, or an update of the Secret in place. The proposal specifies:
   - which component holds `create`, `update`, and `delete` on the namespace;
   - naming and ownership metadata (tenant, pool, and credential id) and tenant isolation, so one tenant can never read, overwrite, or reference another tenant's Secret;
   - exclusion of the value from logs and audit, the encryption-at-rest layer, rotation, revocation and deletion (including the existing revoke endpoint), and GC of orphaned Secrets;
   - API responses that never return the value;
   - whether `tenant-admin` may submit values, given that §24.5 lists `add-credential` as `platform-admin` while §4.9 and §10.2 let `tenant-admin` manage its own tenant's pools, and that the revoke minimum role differs between `lenny-ctl` (`platform-admin`) and the ops MCP tool (`tenant-admin`).
4. One `secretRef` rule applies everywhere: `<namespace>/<name>`, or a bare name resolved in the credentials namespace. The chart, the Token Service probe and any Token Service read, the gateway prober, the gateway VCS reader, and the admin handlers apply the same rule, and a ref outside the credentials namespace is refused.
5. The proposal retires the per-Secret `resourceNames` Role text, the kubectl-patch remediation, the "helm upgrade per credential" text, and the bootstrap probe-skip justification in §4.9. It settles one key set for the §15.1 `CREDENTIAL_SECRET_RBAC_MISSING` details. It updates the §24.5 `add-credential` row ("also emits required RBAC patch command"), the §25 `lenny_credential_add` tool, the §17.7 Token Service outage runbook diagnosis and remediation, and the docs. The chart work creates the namespace Role rather than retiring a rendered one.
6. Read-failure semantics: a failed Secret read fails that call closed with the transient category, and no stale value is served.
7. Cross-tenant `secretRef`: a tenant's credential pool may reference only Secrets that tenant owns (variant B metadata) or Secrets a platform admin registered for it (variant A). This item ships with items 1 and 4. A namespace-wide `get` removes the per-name allow-list the specification relies on, and a `tenant-admin` chooses pool `secretRef` values, so without a tenant binding in the same change any tenant admin could reference another tenant's Secret.

**Relation to proposal 0111.** Draft proposal 0111 (branch `draft/0111-warm-workspace`, status Draft) stages edits to the same sites under a superseded design in which the chart owns `resourceNames`. These are SPEC-8 Edit (h) (Token Service secret resolution, per-call get, fail closed), Edit (l) rules 1 to 3 (`credential.ParseSecretRef` as the single parser, the chart render as sole writer of the Role), Edit (m) (retiring the kubectl patch across §4.9, §15.1, §17.7, and §24.5), and CODE-7 rules T2 to T5 (a `ReadCredentialSecret` RPC). The owner decision recorded in 0111 as `[orchestrator.decisions-2026-10-30d]` moves this access path into 0112. 0112 owns and replaces those edits, and it does not assume that `credential.ParseSecretRef`, rule T3, `ReadCredentialSecret`, `pkg/gateway/credentials/vcsresolve`, or `charts/lenny/templates/token-service-rbac.yaml` exist, because none is in this tree.

## Evidence

- verified: `spec/04_system-components.md` §4.9 Credential Leasing Service, the `lenny-token-service-secrets` Role YAML and "populated at install time" paragraph (lines 1680-1695 at the time of writing), the "Admin-time RBAC live-probe (required)" paragraph including the bootstrap probe-skip sentence and the `CREDENTIAL_POOL_EXHAUSTED` surfacing (line 1697), and "Large pool Secret management (Tier 3)" (line 1699).
- verified: `spec/04_system-components.md` "User-scoped credential storage" and `POST /v1/credentials` (TokenStore, per-tenant DEK under the KMS KEK), and the bootstrap Job statement that it does not create Kubernetes Secrets.
- verified: `spec/15_external-api-surface.md` §15.1 `CREDENTIAL_SECRET_RBAC_MISSING` row naming `details.resourceNames` and `details.rbacPatch`.
- verified: `spec/24_lenny-ctl-command-reference.md` §24.5 `add-credential` row (min role `platform-admin`, "also emits required RBAC patch command"); `spec/10_gateway-internals.md` §10.2 roles table (`tenant-admin` manages own-tenant credential pools); `spec/25_agent-operability.md` `lenny_credential_add`.
- verified: other cited anchors exist: §13.2.4 `lenny-system` NetworkPolicies, §16.4 Logging, §17.6 Packaging and Installation, and the §17.7 Token Service outage runbook entry.
- verified: `pkg/gateway/externalapi/admin/credential_pools.go` `probeSecretRefs` (raw refs collected, 422 with `missingSecrets` and `remediation`, 503 `CREDENTIAL_PROBE_UNAVAILABLE` on indeterminate) and `tokenServiceRBACPatch` (hard-coded `-n lenny-system`, raw ref appended to `/rules/0/resourceNames/-`). The probe runs on pool create, pool update, credential add, and credential update.
- verified: `cmd/lenny-gateway/secret_probe.go` sends `SecretName: secretRef` with no namespace; `cmd/lenny-gateway/executor_credentials.go` wires the prober whenever the Token Service address is set.
- verified: `pkg/tokenservice/secretprobe/secretprobe.go` (empty namespace falls back to the default; SSAR `get secrets`; confirming `Get`); `cmd/lenny-token-service/flags.go` `--secret-namespace` default `POD_NAMESPACE`, else `lenny-system`.
- verified: `pkg/gateway/connectors/connectorsecret/connectorsecret.go` `parseRef` accepts only `namespace/name` or `namespace/name/key`.
- verified: `charts/lenny/templates/token-service-deployment.yaml` renders Deployment, ServiceAccount, Service, and PodDisruptionBudget only. No chart RBAC template names the Token Service. The only Secret-scoped Role outside `lenny-ops` is `lenny-gateway-admin-token` in `gateway-deployment.yaml` (unrestricted `create`; `get`, `update`, and `patch` on the admin-token Secret).
- verified: `charts/lenny/templates/gateway-deployment.yaml` sets `--token-service-grpc-addr` under `tokenService.enabled`, and `charts/lenny/values.yaml` defaults `tokenService.enabled` to true.
- verified: `charts/lenny/templates/ops-rbac.yaml` Role `lenny-ops-namespace` grants `get`, `list`, `watch`, and `delete` on all Secrets in the release namespace.
- verified: `pkg/gateway/externalapi/admin/routes.go` credential routes (POST, PUT, DELETE, revoke, re-enable); no per-credential rotate route.
- verified: `pkg/gateway/credentials/` holds no `vcsresolve` package; no rule T3 for Secret reads exists in `spec/` (T3 appears only as a data-classification tier).
- verified: draft proposal 0111 status Draft; its review log carries `[orchestrator.decisions-2026-10-30d]` and the deferred cross-tenant `secretRef` note; branch head `13646fce8`.

## Who observes it

- Operators and tenant admins provisioning LLM or VCS credentials on a stock install with the Token Service enabled. Every admin-API credential-pool write that names a `secretRef` is refused with 422, and the returned remediation patches a Role that does not exist in a hard-coded namespace, so applying it fails with NotFound.
- Operators who supply a `namespace/name` ref, including the form the §4.9 examples use. The probe looks up a Secret literally named with the slash, so the ref can never pass. Operators who follow the `defaultGithubVcsPool` comment and supply a bare name get a ref the gateway VCS reader rejects as malformed.
- Any reader of the specification, who is told that a chart-rendered `resourceNames` list exists and that bootstrap pools skip the probe because of it.
- Anyone relying on Secret isolation in the release namespace, where `lenny-ops` can read every Secret.

## What breaks if nothing changes

- Variant A is unusable on a default install: no credential pool with a `secretRef` can be created or extended through the admin API while the Token Service is enabled.
- The 422 remediation sends operators to a nonexistent Role and a malformed resource name, so the documented recovery path fails.
- No `secretRef` form satisfies both the probe and the VCS reader, so a credential that passes admission cannot be read, and a credential that can be read cannot pass admission.
- Credential Secrets stored in the release namespace remain readable and deletable by `lenny-ops`.
- Variant B does not exist, so a tenant admin cannot add a credential without cluster access.
- The specification continues to describe a per-Secret Role, a Helm-rendered allow-list, and a bootstrap probe skip that the chart does not implement, and §15.1 continues to name 422 detail keys the code does not emit.

## Findings this unblocks

- The cross-tenant `secretRef` defect that proposal 0111 recorded as pre-existing and deferred (0111 review log, "Cross-tenant secretRef").
- The undocumented gap that the chart grants the Token Service no Secret read. `BUILD-GAPS.md` F-4.9.3 records the probe as built with the kubectl-patch remediation and does not record the missing Role.
- Proposal 0111's VCS Secret read path, which 0111 must rebase onto this proposal's `secretRef` rule and namespace.

## Prior art considered

- No landed proposal, specification mechanism, or open BUILD-GAPS or TEST-GAPS finding provides a dedicated credentials namespace, a namespace-wide Token Service `get`, admin-API-written credential Secrets, or tenant-scoped `secretRef`. Exact searches for `credentials.namespace`, `lenny-credentials` (one unrelated pod volume name in `pkg/admission/ephemeral_container_cred_guard/guard_test.go`), and `namespace-wide` return nothing relevant. `BUILD-GAPS.md` F-4.9.3 (closed) built only the live probe; TEST-GAPS entries cover only probe test tiers.
- Draft proposal 0111 stages a version of items 4, 5, and 6 under the superseded chart-owned `resourceNames` design (Edits (h), (l), and (m); CODE-7 rules T2 to T5). The owner decision of 2026-10-30 moves that path into 0112, so 0112 replaces those edits, and 0111 must stop staging conflicting text at the same §4.9, §15.1, §17.7, and §24.5 sites.
- The §4.9 Tier 3 External Secrets Operator guidance addresses adding Secrets without a `helm upgrade` but leaves the per-name RBAC grant in place. 0112 rewrites it.
- `POST /v1/credentials` already accepts credential values over the API and stores them in the Postgres TokenStore under per-tenant DEK and KMS KEK envelope encryption, never returning the value. Variant B as decided writes a Kubernetes Secret; the proposal states why it uses that store rather than the TokenStore, and which encryption layer ("the existing KMS hierarchy") applies, since the §4.9 KMS hierarchy for Secrets is etcd encryption and the TokenStore uses application-layer envelope encryption.
- Rendering the per-Secret Role that §4.9 already describes is the smallest intervention. It would restore variant A with an RBAC step per credential, and it delivers neither variant B nor removal of that step, so it does not meet the owner decision.

## Validated premises

**premise lens (verdict: revise)**

- Refuted (load-bearing): "the Token Service reads credential Secrets through a `lenny-token-service-secrets` Role whose `resourceNames` the chart fills at install time." True of the specification only. The chart renders no Secret RBAC for the Token Service, so every probe returns `DENIED`, and the suggested patch targets a Role that does not exist. The "erased by the next helm upgrade" failure cannot occur. Verified against `token-service-deployment.yaml` and the chart's RBAC templates.
- Refuted (load-bearing): "`secretRef` is parsed four different ways: chart render, Token Service read rule T3, gateway prober, and `tokenServiceRBACPatch`." Rule T3 is 0111 staged design and is absent from `spec/` and `pkg/`. The chart and the prober do not parse at all, and the patch appends the raw ref. The parsers that do exist are `connectorsecret.parseRef` and the `secretprobe` empty-namespace default. Verified.
- Confirmed: the probe returns 422 with a kubectl patch that hard-codes `-n lenny-system` and appends raw refs. The code's detail keys (`missingSecrets`, `remediation`) differ from §15.1 (`resourceNames`, `rbacPatch`). Verified.
- Partly wrong: no per-credential rotate endpoint exists; the routes are POST, PUT, DELETE, revoke, and re-enable. The revoke minimum role differs between `lenny-ctl` and the ops MCP tool. Route list verified; the role difference is recorded from the lens.
- Confirmed: §4.9 carries the `resourceNames`, manual-patch, live-probe, and Tier 3 text item 5 retires, plus a bootstrap probe-skip justification that depends on the absent chart render. No credentials namespace value exists. Verified.
- Imprecise: §4.9 says an uncovered Secret surfaces at lease time as `CREDENTIAL_POOL_EXHAUSTED`. "No usable credential" is `vcscred.ErrNoUsableCredential`, and the gateway reads VCS Secrets directly. Verified.

**evidence lens (verdict: revise)**

- Verified: `tokenServiceRBACPatch` hard-codes the namespace and the role name and appends raw refs; the 422 path carries the patch as `details.remediation`.
- Verified in the specification and false in the tree (load-bearing): the §4.9 Role and its Helm population. The template that would render it, `token-service-rbac.yaml`, is staged only by draft 0111.
- False in the tree (load-bearing): rule T3 and a Token Service path that reads LLM keys by `secretRef`. The only Token Service Secret read is the probe's `Get`.
- Drifted (load-bearing): the four-site parse list describes the state after 0111. The current divergence is between the prober (no parse), `secretprobe` (default namespace), `connectorsecret` (requires a namespace), the patch (raw ref), and the chart (pass-through).
- Drifted: "no usable credential" fits only the VCS path; the LLM path surfaces `CREDENTIAL_POOL_EXHAUSTED`.
- Additional drift: the §15.1 detail keys differ from the code, and 0111 Edit (m) already stages edits to the same sites.
- Verified: the cited anchors in §4.9, §13.2.4, §16.4, §17.6, §17.7, §10.2, and §15.1, the admin credential endpoints, and the chart NetworkPolicy `allow-token-service`.
- Context: neither the Token Service nor the gateway can read a credential Secret in-cluster today.

**prior-art lens (verdict: revise)**

- Confirmed: nothing landed already solves the problem.
- Confirmed (load-bearing): draft 0111 is direct prior art and stages items 4, 5, and 6 under the superseded design; 0112 must replace its Edits (h), (l), and (m) and CODE-7 T2 to T5. Verified against 0111's status and review log.
- Confirmed (load-bearing): "Today" describes the specification; the chart work creates the Role rather than retiring one.
- Confirmed: the parsing divergence in the tree; `credential.ParseSecretRef` does not exist yet.
- Confirmed: the §15.1 key mismatch, asserted by `credential_pool_rbac_probe_test.go`.
- Confirmed: the Tier 3 External Secrets Operator paragraph is partial prior art that leaves the RBAC grant in place.
- Confirmed: 0111 deferred the cross-tenant `secretRef` gap, so item 7 takes over an open defect.

**scope lens (verdict: revise)**

- Confirmed (load-bearing): the statement holds two problems of different size. Part 1 is the namespace, the guard, RBAC, NetworkPolicy, the single `secretRef` rule, retirement, read-failure semantics, and docs (items 1, 2, 4, 5, and 6). Part 2 is the variant B write path (item 3). Variant A follows from Part 1.
- Recommendation not adopted as a split: the lens recommends staging variant B as a separate follow-on proposal because it is proposal-sized and needs a body field carrying plaintext, write RBAC, naming, redaction, encryption, rotation, revocation, deletion, GC, and response rules, none of which exist. The owner decision and the user request place both variants in this proposal, so the statement keeps them together and orders Part 1 first. Whether to split remains open for the owner.
- Confirmed (load-bearing): item 7 must ship with Part 1, because a namespace-wide `get` removes the per-name allow-list and a `tenant-admin` chooses pool `secretRef` values (§4.9 tenant-scoped pools; §10.2 roles; `routes.go` gates only on `PermManageCredentialPools`).
- Confirmed: an existing store for API-submitted credential values (TokenStore, `POST /v1/credentials`) and an encryption-layer ambiguity that variant B has to resolve.
- Confirmed: further retirement sites in §15.1, §24.5, and §25, and the §4.9 etcdctl verification path that hard-codes `lenny-system`.
- Confirmed: the `add-credential` minimum role (`platform-admin`) conflicts with `tenant-admin` pool management.

**impact lens (verdict: revise)**

- Confirmed (load-bearing): the chart renders no Token Service Secret Role, so the "helm upgrade erases the patch" story cannot happen. Verified.
- Confirmed (load-bearing): on a default install the gateway wires the prober and every probed admin write is refused with 422, making variant A unusable today. Verified for the admin handlers. The lens's claim that the bootstrap seed and `defaultGithubVcsPool` hit the same 422 was not independently traced through `lenny-ctl bootstrap` in this consolidation.
- Confirmed: no single ref form passes both the probe and the VCS read. Verified.
- Corrected: the Token Service reads no credential Secret value today; the lease path is dormant and the gateway reads VCS Secrets without an RBAC grant. `ErrNoUsableCredential` fires for a pool with no active credential rather than for a Secret read failure.
- Verdict (load-bearing): the defect is reachable and observed; a proposal is warranted.

**alternatives lens (verdict: revise)**

- Confirmed: no reading makes the change unnecessary; rendering the §4.9 Role delivers neither variant B nor removal of the per-credential RBAC step.
- Confirmed (load-bearing): "Today" describes the specification; the remediation fails with NotFound on a chart install.
- Confirmed (load-bearing): the four-site claim is overstated; item 4 is stated against the sites that exist in this tree.
- Confirmed: `lenny-ops` holds namespace-wide Secret read and delete and the gateway holds Secret `create` in the release namespace, which makes the denial requirement in item 1 concrete. Verified.
- Confirmed: `BUILD-GAPS.md` never recorded the missing chart Role; this proposal resolves that gap and should say so.
