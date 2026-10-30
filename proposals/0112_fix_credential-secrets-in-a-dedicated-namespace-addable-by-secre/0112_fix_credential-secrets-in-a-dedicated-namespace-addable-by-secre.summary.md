# Summary: Credential Secrets in a dedicated namespace, addable by reference or through the admin API

## Summary

**Problem statement.** On a stock install the chart grants the Token Service no Secret access and no kube-apiserver egress. Every probed admin credential-pool write is therefore refused with `422`, and the returned remediation patches a Role that does not exist in a hard-coded namespace. No single `secretRef` form passes both the probe and the gateway VCS reader. `lenny-ops` can read every Secret in the release namespace. No path exists for a tenant admin to submit a credential value. The specification describes a per-Secret `resourceNames` Role, a Helm allow-list, an informer cache, and a bootstrap probe skip, none of which exist, and §15.1 names 422 detail keys the code does not emit. The owner decided that credential Secrets move to a dedicated namespace the Token Service reads namespace-wide, that both variant A (register an operator-created Secret by `secretRef`) and variant B (Lenny writes the Secret from a submitted value) ship, and that a tenant binding ships with them.

**What changes.**

- §4.9 states the credentials namespace, the single `secretRef` rule, the platform-admin registration rule, the `secretData` write path, the probe, and the per-read fail-closed Token Service read (SPEC-1 to SPEC-3). §10.2, §12.8, §12.9, §13.2.4, §15.1, §16, §17, §18.38, §24.5, §25, and §28.7 follow (SPEC-4 to SPEC-14).
- The Token Service `ProbeSecretAccess` call becomes one uncached `Get` with an optional key that returns the value, and the gateway VCS reader uses it (CODE-2, CODE-6).
- The gateway admin handlers canonicalize refs with one parser, enforce the registration and reserved-prefix rules at the locked row, drop the kubectl remediation, and write and delete Lenny-written Secrets under the pool row lock (CODE-1, CODE-3, CODE-4).
- Tenant erasure deletes Lenny-written Secrets after the pool rows (CODE-5), and the preflight warning text follows the namespace (CODE-7).
- The chart renders the namespace with a keep annotation and a `pods: "0"` quota, the two Secret Roles, the render guard, the Token Service egress, and both namespace flags (CHART-1). Docs and records follow (DOCS-1, REC-1).

**Decisions.**

- The gateway is the only writer, with `create`, `update`, `delete`, and `deletecollection` and no `get`, `list`, `watch`, or `patch`. The Token Service holds a namespace-wide `get` only. No new RPC, no Token Service write or list grant, and no background sweeper are added; this spine has the fewest moving parts that cover both variants and the tenant binding.
- Tenant binding is a role check: introducing a `secretRef` needs `platform-admin` or the bootstrap seed, and anything else gets `403 FORBIDDEN`. It takes one check at the point refs enter, with no proto field, no per-read annotation check, and no operator labelling step.
- The check uses `HasRole(auth.RolePlatformAdmin)`, because a custom role may hold `manage_credential_pools`, and it runs against the row the mutate closure holds locked, because the unlocked diff is a time-of-check/time-of-use race.
- The `lenny-cred-` prefix is reserved, and a prefixed ref is accepted only when the same id in the same pool already carries it. Without this, a written Secret could be referenced from another tenant's pool or another entry and deleted under it.
- Lenny-written Secret names are `lenny-cred-` plus 40 hex characters of SHA-256 over (tenant, pool, credential id), and ownership is recorded in annotations plus a `lenny.dev/tenant-hash` label. Tenant ids run to 128 characters and fit no label value, so the label carries a hash, which lets tenant erasure reach an orphan that no stored reference names; deterministic names let rotation update in place.
- Secret operations run under the pool row lock as the last step before the row commits (SPEC-2 **Credential values submitted through the admin API.** rule 4), so a refused request touches no Secret. The lock does not fence an operation the kube-apiserver applies after the transaction ends, and no client-side bound closes that window, so rule 5 states the residue by Secret state and accepts an orphan created after the tenant-deletion delete-collection. No compensating delete follows a failed commit. It would leave the same orphan an interrupted or late operation leaves, and it would need its own race check.
- One parser (`credential.ParseSecretRef`) runs at the admin and bootstrap write boundary, refs are stored canonical, and the Token Service reads only its configured namespace; the request `namespace` field is reserved, which removes the cross-namespace existence oracle.
- `ProbeSecretAccess` is extended with a key and a value instead of adding a read RPC, and its SelfSubjectAccessReview is deleted; under a namespace-wide `get` the SSAR adds nothing.
- Reads are one `get` each, uncached, and fail closed with the `TRANSIENT` category. The informer text and `tokenService.secretResyncInterval`, which exist in no code or chart, are deleted.
- `lenny_token_service_secret_reloads_total` is repurposed to count credential Secret reads by outcome (`success`, `not_found`, `denied`, `unavailable`) with no secret-name label, and §16.1 is edited to match. The counter has no production caller today, and §16.1 already catalogs it as secret resolution. This settles a conflict between two challenge revisions in favor of the one that also stages the §16.1 edit.
- The 422 keeps `CREDENTIAL_SECRET_RBAC_MISSING` and the `details.missingSecrets` key the code already emits. `remediation` and `rbacPatch` are retired. The 503 probe row documents `details.secretRef`, the key the code emits.
- Bootstrap runs no probe: the seed runs under platform-admin authority on every upgrade, its Secrets may arrive after the Job, and a missing Secret fails each read closed.
- The namespace is guarded by the keep annotation, the `pods: "0"` quota, a render guard against the release and agent namespaces, and a documented operator obligation. The quota is the control that closes pod mounts, because the controller ClusterRole grants pod creation cluster-wide.
- Variant B values sit under the existing etcd KMS envelope encryption. The `TokenStore` is not used, because it is user-scoped and pool credentials keep one read path by `secretRef`.
- The §24.5 credential-pools minimum roles follow the §10.2 **Manage credential pools** row (SPEC-11).
- The VCS read error keeps surfacing as `INTERNAL_ERROR`, which §15.1 already classifies `TRANSIENT` and retryable, so no tool-error mapping changes.
- With no writer wired, the lifecycle fails closed (SPEC-2 **Credential values submitted through the admin API.** rule 6). Committing a row without its Secret operation would leave a Secret that no later write names, and erasing the rows would delete the only record that names it.

**Watch out for.**

- `pgstore.Update` calls `credentialpoolstore.Validate` after the closure returns. A Secret `Put` or `Delete` must follow an explicit `Validate` call inside the closure, or a `400` rolls back a row whose Secret is already gone (CODE-4 step 4).
- `newSecretRefs` diffs the pool-wide set and ignores credential ids. The reserved-prefix check must run per id (CODE-3 step 6).
- Pool DELETE checks `If-Match` against an unlocked `Get`, and `SoftDelete` is a separate statement; the `Update` SELECT does not filter `deleted_at`, so an add can land on a soft-deleted pool today (CODE-4 steps 4 and 6).
- The gateway holds no `get`. The client-go writer must create, then update with an empty `resourceVersion` on `AlreadyExists`; a helper that reads first fails with Forbidden (CODE-4 step 3).
- `credential_pool_rbac_probe_test.go` asserts the `remediation` key, and `install_prereqs_test.go` asserts the exact etcdctl string. Both change with the spec (CODE-3, CODE-7, SPEC-10 (c)).
- §16.1 and `docs/reference/metrics.md` must change together with `promemit.go` (SPEC-8, DOCS-1, CODE-2).
- The rendered-RBAC guard must assert that no other grant on `secrets` applies in the credentials namespace. Asserting that no other ServiceAccount holds any verb there fails on the controller's cluster-wide pod grant (CHART-1 step 5).
- Envtest does not exercise chart-installed RBAC. The denial, quota, and egress claims are checked in tier 9 with `kubectl auth can-i --as` against the installed chart (TEST-1).

## Goals

- Variant A works on a stock install: a `platform-admin` registers an operator-created Secret in the credentials namespace with no `helm upgrade` and no RBAC edit.
- Variant B exists: a `tenant-admin` or `platform-admin` submits a value, the gateway writes the Secret, and no response, log line, or audit event carries the value.
- One `secretRef` rule applies on every write, probe, and read path, and a ref outside the credentials namespace is refused.
- No tenant can read, overwrite, or reference another tenant's credential Secret.
- A failed Secret read fails that call closed with the transient category, and no stale value is served.
- Only the Token Service reads, and only the gateway writes, in the credentials namespace; `lenny-ops`, the controllers, and the webhooks hold no Secret verb there.
- The specification describes the shipped mechanism, and §15.1 names the detail keys the code emits.

## Non-goals

- Rendering the per-Secret `resourceNames` Role §4.9 describes. It restores variant A only with an RBAC step per credential and provides no variant B, against the owner decision.
- Tenant binding by a `lenny.dev/tenant-id` annotation checked by the Token Service on every probe and read. It needs a proto field, a check on every read, and an annotation on every operator Secret, and the platform-admin registration rule closes the same cross-tenant reference where refs enter.
- Tenant binding by label. Label values are capped at 63 characters and must begin and end alphanumeric, while tenant ids run to 128 characters with leading `_` or `-` allowed.
- Tenant binding by Secret name prefix or by a Postgres Secret-to-tenant registry. Tenant ids do not fit Kubernetes names, and a table adds state the role rule avoids.
- A distinct verdict or error code for a tenant mismatch. It would leak existence and adds a code.
- A Token Service write RPC with the Token Service as the only writer. The gateway already holds the plaintext from the request, so the RPC adds wire surface without reducing exposure.
- A separate `ReadCredentialSecret` RPC. `ProbeSecretAccess` already performs the `Get`, and a key field plus a value field cover the read.
- A `DeleteCredentialSecrets` RPC. With the gateway as writer it is unnecessary.
- A periodic orphan sweeper or reconciler in the Token Service. It needs `list` and `delete`, store access, a grace period, and preconditions. The residual orphan is readable only by the Token Service.
- An intent or outbox table for pending Secret writes, and a leader-elected reconciler. They add a table and a leader-elected process, and a request a deposed leader issued can still be applied late, which leaves the residue SPEC-2 **Credential values submitted through the admin API.** rule 5 states.
- A compensating Secret delete after a failed row commit. See **Decisions**.
- `generateName` random Secret names. Every rotation would create a new Secret and leave an old one to delete.
- Writing the row first and the Secret second on an add. A crash leaves a dangling reference, while the chosen order leaves only an orphan.
- Deleting the admin-time probe. It catches a missing Secret at registration time and costs only the existing code.
- Keeping the SelfSubjectAccessReview in the probe. Under a namespace-wide `get` it is redundant, and Forbidden on the `Get` already yields `DENIED`.
- Keeping the probe request `namespace` override with mismatch refusal. Deleting the field is narrower and removes the cross-namespace existence oracle.
- A ValidatingAdmissionPolicy or webhook that keeps the namespace credential-only. It adds an availability dependency, and the quota, the chart, and the documented obligation cover the exposure paths.
- A preflight check over RoleBindings in the credentials namespace, or a preflight quota check. It cannot judge cluster-wide bindings, and the preflight Job holds no SAR grant. The API server enforces the quota, and a helm-unittest pins the rendered RBAC.
- A NetworkPolicy in the credentials namespace. No pod runs there, and NetworkPolicy governs pod traffic only.
- Quota entries for services and persistentvolumeclaims. `pods: "0"` already prevents any workload from mounting a Secret.
- Granting the Token Service `list` or `watch` to keep the informer. An informer serves cached values, which item 6 forbids.
- Granting the gateway `get` or `patch` in the credentials namespace. It breaks the gateway denial.
- Renaming `CREDENTIAL_SECRET_RBAC_MISSING`, `ProbeSecretAccess`, or the 422 key. It is churn with no behavior change.
- Renaming the Token Service flag `--secret-namespace`. The existing flag already names the read namespace.
- A chart-template copy of the `secretRef` rule. It would be a second implementation, and the bootstrap handler applies the single parser.
- A third `/key` segment in credential refs. The provider Secret keys are fixed, and the three-segment `connectorsecret` form remains for connector client secrets only.
- Applying the credentials-namespace rule inside `connectorsecret.KubeResolver`. It also resolves connector client secrets that are not pool credentials.
- Inline `secretData` on pool create, pool PUT, or bootstrap. It multiplies the writes per request, and plaintext in Helm values is what variant B avoids.
- A `lenny-ctl` value flag (`--secret-data-file`, `--secret-from-file`, or `--value`). The admin API field covers variant B, and a CLI convenience can follow separately.
- Validating `secretData` keys against a per-provider table in code. A wrong key fails the read closed, and the table stays in the spec.
- Forbidding a switch between `secretRef` and `secretData` on PUT. SPEC-2 **Credential values submitted through the admin API.** rule 3 gives each direction its Secret operation.
- Per-tenant credentials namespaces. They multiply namespaces and Roles, and isolation is enforced where refs enter.
- Storing variant B values in the `TokenStore`. The owner decided on Kubernetes Secrets, and the `TokenStore` is user-scoped with no pool read path.
- Reusing `AssignCredentials` for VCS reads. It is dormant, needs a session, and mints a lease that a clone does not need.
- A dev-profile credential Secret path through the gateway ServiceAccount. It would be a tier-dependent code path; the Token Service-disabled outcomes are SPEC-2 **Credential values submitted through the admin API.** rule 6 and **Credentials namespace.** rules 4 and 5.
- The LLM direct-mode materialization read through `AssignCredentials`. It is dormant, and the §4.9 read rule covers it when it is built.
- Splitting variant B into a follow-on proposal. The owner decision and the user request keep both variants here.
- The git-specific consumers of the read path (git-https username and password keys, warm-workspace store fills). See **Impacts on other proposals**.

## Open decisions for human to make

None. Decision 1 (keeping the credentials namespace credential-only) was decided on 2026-10-30 by the orchestrator under the owner's standing delegation. The staged chart guards stay: the render guard against the release and agent namespaces, and the `pods: "0"` quota. The operator obligation stays documented. The proposal also adds a preflight check that lists every Secret in the credentials namespace that no credential pool references and Lenny did not write. The check warns rather than fails, because an operator may create a Secret before registering it (variant A). The listing makes a stray Secret visible without blocking an install.

## Defects in the shipped tree that this proposal does not stage

- **Credential update does not enforce `If-Match`.** §15.1 says `PUT /v1/admin/credential-pools/{name}/credentials/{credId}` requires `If-Match`, and `handleUpdateCredentialEntry` in `pkg/gateway/externalapi/admin/credential_pools.go` never calls `enforceIfMatch`. It stays out of scope because the locked-row checks in CODE-3 do not depend on it.
- **No preflight check rejects impersonation grants.** §4.9 says a preflight check rejects Helm values that would grant the gateway `serviceaccounts/token` or `impersonate` on the Token Service ServiceAccount, and no such check exists in `pkg/preflight` or the chart. SPEC-2 (g) keeps the sentence unchanged because the probe redesign does not touch it.
- **kube-apiserver port drift.** §13.2.4 rows name TCP 443 for kube-apiserver egress, and the chart renders the `kubeApiServerPorts` list (443 and 6443). SPEC-6 follows the existing phrasing and leaves the drift for a separate change.
- **Webhook and controller kube-apiserver egress rows.** The §13.2.4 Admission Webhooks row grants no kube-apiserver egress, and the controller row names kube-apiserver without `kubeApiServerCIDR`. The chart's `allow-admission-webhooks`, `allow-controller-egress`, and `allow-pool-scaling-controller-egress` in `system-network-policies.yaml` all render a `kubeApiServerCIDR` rule. SPEC-6 leaves those rows for a separate change.
- **`tenant-viewer` cannot read credential pools.** §10.2 grants `tenant-viewer` read-only access to its own tenant's credential pools, and `pkg/gateway/externalapi/admin/routes.go` gates the credential-pool `GET` routes on `manage_credential_pools`, which `tenant-viewer` does not hold.
- **Rotation example key name.** The §4.9 rotation example creates the key `api-key`, while the **Secret shape.** table names `apiKey` for `anthropic_direct`. SPEC-3 (d) only adds the namespace flag.
- **GitHub provider source material.** §4.9 **Credential Provider** gives `github` source material as GitHub App credentials, with a minted installation token, and the **Credential Pool** TTL table repeats that model. The gateway's VCS read and the **Secret shape.** `github` row read a stored `token`. 0112 does not decide whether v1 mints or stores the token.
- **T4 encryption layer for credential Secrets.** §12.9 requires envelope encryption via KMS for T4 data and says §4.9 credential encryption satisfies it, while §4.9 **Encryption at rest — KMS key hierarchy.** accepts self-managed `aescbc` or `aesgcm` and GKE default encryption for the Secrets that hold pool credentials. SPEC-9 (b) changes only the Store cell.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0111 (warm workspace; branch `draft/0111-warm-workspace`) | Draft | Under the owner decision `[orchestrator.decisions-2026-10-30d]`, this proposal owns the Token Service Secret access path and replaces 0111 SPEC-8 Edits (h), (l), and (m) and CODE-7 rules T2 to T5. It supplies `credential.ParseSecretRef` (CODE-1) and the Token-Service-backed `vcscred.SecretReader` (CODE-6). | Drop Edits (h), (l), and (m) and CODE-7 T2 to T5, stop staging text at the §4.9, §15.1, §17.7, and §24.5 sites this proposal edits, and rebase its git-specific consumers onto CODE-1 and CODE-6. |
| 0110_fix_session-owner-authorization-plan-10-2-session-owner-authoriz | Approved (2026-10-29, `approved-date` in its status file) | The overlap is textual. 0110 SPEC-1 and this proposal's SPEC-5 (b) both insert text directly after the §10.2 **Permission matrix:** table. 0110 SPEC-6 and this proposal's SPEC-7 edit different §15.1 catalog rows. No 0110 deliverable loses its subject, and nothing 0110 decided is reopened. | If 0110 lands first, SPEC-5 (b) places the footnote between the table and the **Session ownership.** paragraph as staged. If this proposal lands first, the 0110 implementor inserts **Session ownership.** after the ¹ footnote line rather than directly after the table, so the footnote stays attached to its table. The rebase needs no human decision. |

## Deliverable index

- SPEC-1 — spec/04_system-components.md — §4.9 Credential Pool: registration sentence and bare-name YAML refs.
- SPEC-2 — spec/04_system-components.md — §4.9 Credential Storage Topology: opening paragraph, per-credential topology example, RBAC granularity item, secret references and registration, `github` Secret shape row, credentials namespace, admin-API values, probe, and Tier 3 text.
- SPEC-3 — spec/04_system-components.md — §4.9 Credential Lifecycle and Secret Management (etcd check, setup, per-read fail-closed resolution, rotation, and GitOps ordering) and the Emergency Credential Revocation runbook steps.
- SPEC-4 — spec/05_runtime-registry-and-pool-model.md — Minimal Configuration `secretRef`.
- SPEC-5 — spec/10_gateway-internals.md — §10.2 permission matrix row and footnote.
- SPEC-6 — spec/13_security-model.md — §13.2.4 Token Service kube-apiserver egress and NET-040 callout.
- SPEC-7 — spec/15_external-api-surface.md — §15.1 credential-pool endpoint rows and error catalog rows, including `TOKEN_SERVICE_NOT_CONFIGURED`.
- SPEC-8 — spec/16_observability.md — §16.1 secret reloads metric row and §16.4 credential-sensitive RPCs bullet.
- SPEC-9 — spec/12_storage-architecture.md — §12.8 Phase 4 Lenny-written Secret deletion and the §12.9 credential pool secrets Store cell.
- SPEC-10 — spec/17_deployment-topology.md — §17.2 namespace layout, §17.6 Helm value and preflight row, §17.7 runbooks.
- SPEC-11 — spec/24_lenny-ctl-command-reference.md — §24.5 credential-pools rows and footnote.
- SPEC-12 — spec/25_agent-operability.md — `lenny_credential_add` and `lenny_credential_retire` rows.
- SPEC-13 — spec/28_communication-channels.md — §28.7 `lenny-tokenservice.proto` register row.
- SPEC-14 — spec/18_build-sequence.md — §18.38 uninstall runbook residual item.
- CODE-1 — pkg/credential/secretref.go — `ParseSecretRef` and Lenny-written name helpers.
- CODE-2 — schemas/lenny-tokenservice.proto, pkg/tokenservice, cmd/lenny-token-service — keyed, Get-only `ProbeSecretAccess`, metric, and redaction.
- CODE-3 — pkg/gateway/externalapi/admin, cmd/lenny-gateway — canonical refs, registration and prefix checks, single 422 key.
- CODE-4 — pkg/gateway/externalapi/admin, pkg/gateway/externalapi/errorclassify, cmd/lenny-gateway — `secretData` write path, Lenny-written Secret operations, and the no-writer error mapping.
- CODE-5 — cmd/lenny-gateway/tenant_erasers.go — erasure deletes Lenny-written Secrets after `DeleteByTenant`.
- CODE-6 — cmd/lenny-gateway, pkg/gateway/provisioning/vcscred — VCS reader through the Token Service keyed read.
- CODE-7 — pkg/preflight/install_prereqs.go — etcd encryption warning text.
- CHART-1 — charts/lenny, pkg/chart/values, tests/tier1_unit/rbac — credentials namespace, quota, Roles, guard, egress, and flags.
- TEST-1 — tests/tier3_contract, tests/tier5_e2e_kind, tests/tier9_security — cross-tier tests.
- DOCS-1 — docs/ — operator, API, metrics, and runbook docs.
- REC-1 — BUILD-GAPS.md — new F-4.9 finding and unstaged defects.
