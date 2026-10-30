# Spec changes — Credential Secrets in a dedicated namespace, addable by reference or through the admin API

## Design (as the spec must state it)

§4.9 holds every normative rule this proposal adds, in five bold paragraphs. Every other staged site cites them.

- **Secret references.** (SPEC-2) is the single `secretRef` rule: the accepted forms, the canonical stored form, and the reserved `lenny-cred-` prefix.
- **Registering a Secret by reference.** (SPEC-2) names the four admin paths that can introduce a `secretRef` and restricts an introduction to `platform-admin` and the bootstrap seed.
- **Credentials namespace.** (SPEC-2) states the namespace, its chart guards, the only two Secret grants in it, and the denial of every other Lenny ServiceAccount.
- **Credential values submitted through the admin API.** (SPEC-2) is variant B and the home of the Lenny-written Secret lifecycle.
- **Token Service secret resolution.** (SPEC-3) governs every Token Service read of a credential Secret.

**Admin-time Secret probe (required).** (SPEC-2) replaces the RBAC live-probe paragraph and uses the unkeyed call. §15.1 (SPEC-7), §10.2 (SPEC-5), §24.5 (SPEC-11), and §25 (SPEC-12) carry the role and error consequences. §13.2.4 (SPEC-6) adds the Token Service kube-apiserver egress, §12.8 and §12.9 (SPEC-9) carry the erasure step and the data-classification store, §16 (SPEC-8) carries the metric and the logging exclusion, and §17 (SPEC-10) carries the namespace layout, the Helm value, the preflight warning text, and the runbooks.

The design adds no endpoint and no RPC. It adds one error code (`TOKEN_SERVICE_NOT_CONFIGURED`), one request field (`secretData`), one Helm value (`credentials.namespace`), and one optional request field and one response field on the existing `ProbeSecretAccess` call.

## Edge cases and accepted failure modes

Each case below is owned by the staged text named on it.

- A caller that copies a `lenny-cred-` reference onto another credential id in the same pool, or a `platform-admin` that copies one into another pool, receives `400 VALIDATION_ERROR` (SPEC-2 **Secret references.** rule 3). A `platform-admin` cannot point one tenant's pool at another tenant's Lenny-written Secret for the same reason.
- Every Lenny-written Secret outcome, including the accepted residues, is owned by SPEC-2 **Credential values submitted through the admin API.** rules 3 to 6.
- An operator-created Secret registered before it exists, including a bootstrap-seeded one under GitOps ordering, fails every read with `NOT_FOUND` until it is created, and the first read after creation succeeds (SPEC-3 **Token Service secret resolution.**, GitOps ordering bullet).
- With the Token Service disabled, the outcomes are owned by SPEC-2 **Credential values submitted through the admin API.** rule 6, **Admin-time Secret probe (required).** rule 5, and **Credentials namespace.** rules 4 and 5.
- Accepted: the gateway's `delete` and `deletecollection` grants let a compromised gateway destroy credential Secrets, and a delete response can carry the objects. The keyed Token Service read already gives the gateway identity access to stored values (SPEC-2 **Credentials namespace.** rule 4), so this adds no read exposure.
- A Secret placed in the credentials namespace that is not a credential is readable by the Token Service. Keeping such Secrets out is the operator obligation SPEC-2 **Credentials namespace.** states.

## Staged edits

### SPEC-1 · spec/04_system-components.md § 4.9 Credential Leasing Service, Credential Pool

(a) In the paragraph that begins "An admin-managed, **tenant-scoped** set of credentials for a given provider.", insert the following directly after the sentence "A `platform-admin` can manage credential pools across all tenants.":

```markdown
Introducing a `secretRef` into a pool requires `platform-admin` ([Credential Storage Topology](#credential-storage-topology) **Registering a Secret by reference.**).
```

(b) In the `credentialPools:` YAML example that follows, replace the three `secretRef` lines as follows. Leave every other line unchanged.

- Replace `        secretRef: lenny-system/anthropic-key-1 # K8s Secret reference` with:

```yaml
        secretRef: anthropic-key-1 # Secret in the credentials namespace
```

- Replace `        secretRef: lenny-system/anthropic-key-2` with:

```yaml
        secretRef: anthropic-key-2
```

- Replace `        secretRef: lenny-system/github-app-1` with:

```yaml
        secretRef: github-app-1
```

### SPEC-2 · spec/04_system-components.md § 4.9 Credential Leasing Service, Credential Storage Topology

(a) Replace the opening paragraph "API keys referenced by `secretRef` are stored as Kubernetes Secrets. The topology and KMS key hierarchy are defined here." with:

```markdown
Credential material is stored as Kubernetes Secrets in the credentials namespace. A Secret enters a pool by reference (`secretRef`), or the gateway writes it from a value submitted through the admin API (`secretData`). The topology, the namespace, and the KMS key hierarchy are defined here.
```

(b) In the **Secret-per-credential topology.** paragraph, replace "a pool with 3 credentials has 3 Secrets: `lenny-system/anthropic-key-1`, `lenny-system/anthropic-key-2`, `lenny-system/anthropic-key-3`." with:

```markdown
a pool with 3 credentials has 3 Secrets in the credentials namespace: `anthropic-key-1`, `anthropic-key-2`, and `anthropic-key-3`.
```

(c) Replace list item "2. **RBAC granularity.** The Token Service's ServiceAccount has `get` permission scoped to specific Secret names (via `ResourceNames`), not `get` on all Secrets in `lenny-system`. This prevents a compromised Token Service from reading unrelated Secrets." with:

```markdown
2. **Ownership granularity.** Each credential's Secret is created, rotated, and deleted on its own. The Token Service reads the credentials namespace through a namespace-wide `get` (**Credentials namespace.** below), and tenant isolation is enforced where references enter a pool (**Secret references.** and **Registering a Secret by reference.** below).
```

(d) Replace the whole **Naming convention.** paragraph, which begins "**Naming convention.** Credential Secrets must follow the pattern" and ends "accessible via its RBAC grants.", with:

```markdown
**Secret references.** A `secretRef` names one Secret in the credentials namespace. Every component that accepts, stores, probes, or reads a reference applies these rules.

1. A `secretRef` is either `<name>` or `<credentials namespace>/<name>`, where `<name>` is a DNS-1123 subdomain name. Any other reference is refused with `400 VALIDATION_ERROR` and `details.field: "secretRef"`.
2. The gateway converts every reference to the canonical form `<credentials namespace>/<name>` before rule 3 or **Registering a Secret by reference.** compares it, and stores it in that form, on the admin API and on the bootstrap seed. The bootstrap seed reports a reference that rule 1 or rule 3 refuses as an error on that pool entry.
3. Secret names that begin with `lenny-cred-` are reserved for Secrets the gateway writes (**Credential values submitted through the admin API.** below). A submitted reference with that prefix is accepted only when the stored entry with the same credential id in the same pool already carries the identical reference. Any other reference with that prefix is refused with `400 VALIDATION_ERROR` and `details.field: "secretRef"`.

**Registering a Secret by reference.** A write whose request submits a `secretRef` the pool does not already carry introduces that reference, and is accepted only from a caller holding the `platform-admin` role or from the bootstrap seed (`POST /v1/admin/bootstrap`). Any other caller receives `403 FORBIDDEN`, including a custom role that holds the `manage_credential_pools` permission. The admin paths that can introduce a `secretRef` are pool creation (`POST /v1/admin/credential-pools`), pool update (`PUT /v1/admin/credential-pools/{name}`), credential addition (`POST /v1/admin/credential-pools/{name}/credentials`), and credential update (`PUT /v1/admin/credential-pools/{name}/credentials/{credId}`). A reference the pool already carries on any entry is not an introduction. The gateway decides the introduced set against the pool row it holds locked for the write, so a reference that a concurrent write removed counts as introduced. **Secret references.** rule 1 applies before this check and rule 3 after it, and a write receives the code of the first check that refuses it.
```

(e) In the **Secret shape.** table, after the row that begins "| `azure_openai` (Azure AD)", insert the row:

```markdown
| `github` | `token` | GitHub App installation access token or personal access token |
```

After the table, insert:

```markdown
A Secret the gateway writes from `secretData` holds exactly the keys the request supplies. A key the provider does not read leaves that provider's read failing closed ([Credential Lifecycle and Secret Management](#credential-lifecycle-and-secret-management) **Token Service secret resolution.**).
```

(f) Replace the **RBAC for the Token Service.** paragraph, the `lenny-token-service-secrets` Role YAML block that follows it, and the paragraph that begins "The `resourceNames` list is populated at install time" with:

````markdown
**Credentials namespace.** Credential Secrets live in a dedicated namespace that the Helm value `credentials.namespace` names (default `lenny-credentials`). The namespace holds credential Secrets only, and keeping other Secrets out of it is an operator obligation. The chart renders the namespace under these rules.

1. The Namespace carries the `helm.sh/resource-policy: keep` annotation, so `helm uninstall` leaves the namespace and its Secrets in place.
2. A ResourceQuota with `pods: "0"` refuses every pod in the namespace, so no workload mounts a credential Secret.
3. The Role `lenny-token-service-credentials` grants the `lenny-token-service` ServiceAccount `get` on `secrets` in the namespace, with no `resourceNames`, `list`, or `watch`.
4. The Role `lenny-gateway-credential-writer` grants the `lenny-gateway` ServiceAccount `create`, `update`, `delete`, and `deletecollection` on `secrets` in the namespace, with no `get`, `list`, `watch`, or `patch`. The gateway reads a stored value for VCS tokens ([Section 14](14_workspace-plan-schema.md)) through the Token Service keyed read ([Credential Lifecycle and Secret Management](#credential-lifecycle-and-secret-management) **Token Service secret resolution.**), for a reference taken from a pool in the caller's tenant.
5. The rule 3 and rule 4 Roles and their RoleBindings render only when `tokenService.enabled` is true. The Namespace and the ResourceQuota render in every profile.
6. The chart refuses to render when `credentials.namespace` equals the release namespace or names an `agentNamespaces` entry.
7. No other Lenny ServiceAccount holds a verb on `secrets` in the namespace. The quota refuses pods, including those that the controller's cluster-wide pod grant could create.
8. The Token Service reaches the kube-apiserver under the `lenny-system` default-deny through the egress rule in [Section 13.2.4](13_security-model.md#1324-lenny-system-namespace-networkpolicies). No NetworkPolicy is rendered in the credentials namespace, because no pod runs there.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: lenny-token-service-credentials
  namespace: lenny-credentials  # credentials.namespace
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get"]
```

**Credential values submitted through the admin API.** A caller holding the `manage_credential_pools` permission for the pool's tenant adds or replaces a credential's value by sending `secretData` on credential addition (`POST /v1/admin/credential-pools/{name}/credentials`) or credential update (`PUT /v1/admin/credential-pools/{name}/credentials/{credId}`). `secretData` is an object whose keys are the provider's Secret keys (**Secret shape.** above) and whose values are strings. A Secret the gateway writes under these rules is a Lenny-written Secret.

1. A body carries at most one of `secretRef`, `secretData`, and `roleArn`, and a body with more than one is refused with `400 VALIDATION_ERROR`. Pool creation and pool update refuse `secretData` with `400 VALIDATION_ERROR`, and the bootstrap seed reports it as an error on that pool entry.
2. The gateway writes the Secret in the credentials namespace under the name `lenny-cred-` followed by the first 40 hexadecimal characters of SHA-256 over the tenant id, the pool name, and the credential id joined by NUL bytes, and stores the canonical reference to it on the entry. The Secret carries the annotations `lenny.dev/tenant-id`, `lenny.dev/credential-pool`, and `lenny.dev/credential-id`, and the label `lenny.dev/tenant-hash` set to the first 40 hexadecimal characters of SHA-256 over the tenant id, by which tenant deletion finds it ([Section 12.8](12_storage-architecture.md#128-compliance-interfaces)).
3. Each operation in the table below performs the Secret operation the table gives, in addition to its row change. A dropped reference is a Lenny-written reference that the pool row carries before the write and does not carry after it. A Put creates the Secret, or replaces its data when the Secret exists. A Delete of an absent Secret succeeds.

   | Operation | Secret operation |
   |:--|:--|
   | Credential addition or credential update carrying `secretData` | Put of the Secret that rule 2 names for the entry. On an entry that already carries that reference, the Put overwrites the value in place. |
   | Pool deletion | Delete of each Lenny-written reference the pool carries |
   | Tenant deletion, in Phase 4 after the `CredentialPoolStore` deletion ([Section 12.8](12_storage-architecture.md#128-compliance-interfaces)) | One delete-collection of every Secret in the credentials namespace whose `lenny.dev/tenant-hash` label matches the tenant |
   | Credential revocation, pool revocation, and re-enable | None |
   | Every other write to a pool, including credential removal, pool update, a credential update without `secretData`, and a bootstrap seed update applied with `--force-update` ([Section 17.6](17_deployment-topology.md#176-packaging-and-installation)) | Delete of each dropped reference |

4. The gateway performs each Put and Delete while it holds the pool row lock for the write, after every check of the request has passed, as the last step before the row change commits. Pool deletion performs its Deletes and the soft delete under one row lock. Credential addition and credential update against a soft-deleted pool return `404 RESOURCE_NOT_FOUND` and perform no Secret operation.
5. Outcomes of a Secret operation:
   - A request refused with a 4xx performs no Secret operation.
   - A `dryRun` request performs no Secret operation ([Section 15.1](15_external-api-surface.md#151-rest-api) **`dryRun` query parameter.**), and rule 6 refuses it as it would refuse the write.
   - A failed Put or Delete refuses the write with `503 SERVICE_UNAVAILABLE` ([Section 15.1](15_external-api-surface.md#151-rest-api)), and the row does not change. On the bootstrap seed the failure is an error on that pool entry.
   - A failed delete-collection fails tenant deletion Phase 4. The job stays at Phase 4 and retries the phase.
   - The gateway takes no compensating action, and the row lock does not order a Secret operation that the kube-apiserver applies after the transaction holding the lock has ended, as on a gateway crash or a Postgres failover. A Lenny-written Secret and the committed rows can therefore disagree in the ways this table gives.

   | State | Reads | Convergence |
   |:--|:--|:--|
   | An orphan: a Lenny-written Secret that no entry references | **Secret references.** rule 3 keeps every pool from referencing it, and only the Token Service can read it. | A later Put for the same tenant, pool, and credential id overwrites it and commits a reference to it. A tenant-deletion delete-collection that runs after the orphan is created deletes it. An orphan created after that delete-collection stays in the credentials namespace. |
   | An entry references an absent Lenny-written Secret | Every read fails closed with `NOT_FOUND` ([Credential Lifecycle and Secret Management](#credential-lifecycle-and-secret-management) **Token Service secret resolution.** rule 3). | A later Put for the entry recreates the Secret, and a later write that drops the reference completes, because a Delete of an absent Secret succeeds. |
   | An entry's Secret holds a value other than the last one a committed write submitted for the entry | Each read returns what the Secret holds. | A later Put for the entry overwrites the value. |

6. When no Token Service is configured, the gateway has no Secret writer. A write for which rule 3 gives at least one Put or Delete is refused with `422 TOKEN_SERVICE_NOT_CONFIGURED` ([Section 15.1](15_external-api-surface.md#151-rest-api)) at the point rule 4 places that operation, and it changes nothing. On the bootstrap seed the refusal is an error on that pool entry. Before the `CredentialPoolStore` deletion, tenant deletion Phase 4 fails and retries while any pool of the tenant, including a soft-deleted pool, carries a Lenny-written reference. For a tenant with none, Phase 4 proceeds without the delete-collection, and a Lenny-written Secret that no stored reference names stays in the credentials namespace.
7. No response carries a submitted value. The two endpoints carry secret material when the body holds `secretData`. They are excluded from HTTP access log payload capture, OpenTelemetry span attributes, and request/response logging middleware, the same treatment as `POST /v1/credentials` ([Pre-Authorized Credential Flow](#pre-authorized-credential-flow)). Only the pool name, credential id, canonical `secretRef`, and outcome are logged or audited.
8. The value is encrypted at rest by the etcd encryption that **Encryption at rest — KMS key hierarchy.** above requires, the same layer that protects an operator-created Secret. The `TokenStore` holds user-scoped credentials ([Pre-Authorized Credential Flow](#pre-authorized-credential-flow)) and stores no value submitted through this path.
````

(g) Replace the whole **Admin-time RBAC live-probe (required).** paragraph, which begins "**Admin-time RBAC live-probe (required).** Because the Token Service's `resourceNames` grants" and ends "cannot succeed in creating a pool whose Secrets are unreadable.", with:

```markdown
**Admin-time Secret probe (required).** Before it commits a write that introduces a `secretRef` (**Registering a Secret by reference.** above), the admin handler probes each introduced reference with an unkeyed Token Service call ([Credential Lifecycle and Secret Management](#credential-lifecycle-and-secret-management) **Token Service secret resolution.**). A caller that **Registering a Secret by reference.** refuses receives its `403` with no probe call. No probe call is made for a reference that **Secret references.** rule 3 refuses.

1. A `DENIED` or `NOT_FOUND` verdict for any reference refuses the write with `422 CREDENTIAL_SECRET_RBAC_MISSING`.
2. A call that returns no verdict refuses the write with `503 CREDENTIAL_PROBE_UNAVAILABLE`, and the handler persists nothing. The handler MUST NOT fail open.
3. The probe is Token-Service-owned. The gateway MUST NOT impersonate the Token Service ServiceAccount through `TokenRequest` or the `Impersonate-*` Kubernetes API headers, its ServiceAccount MUST NOT hold `serviceaccounts/token` or `impersonate` verbs on the Token Service ServiceAccount, and the install-time RBAC bundle MUST NOT grant either (a preflight check rejects Helm values that would produce such a grant). The gateway MUST NOT substitute a `SelfSubjectAccessReview` under its own ServiceAccount.
4. The bootstrap seed runs no probe, because its Secrets may be created after the `lenny-bootstrap` Job ([Credential Lifecycle and Secret Management](#credential-lifecycle-and-secret-management) **GitOps pattern.**).
5. When no Token Service is configured, no probe runs.
```

(h) Replace the whole **Large pool Secret management (Tier 3).** paragraph with:

```markdown
**Large pool Secret management (Tier 3).** At Tier 3 with hundreds of credentials, operators should use an external secrets manager (AWS Secrets Manager, GCP Secret Manager, or HashiCorp Vault) with the External Secrets Operator (`external-secrets.io`) to synchronize Secrets into the credentials namespace. A `platform-admin` registers each synchronized Secret by `secretRef` once. Adding a Secret needs no `helm upgrade` and no RBAC change.
```

### SPEC-3 · spec/04_system-components.md § 4.9 Credential Leasing Service, Emergency Credential Revocation and Credential Lifecycle and Secret Management

Locate each edit by the quoted text.

(a) In the **Production requirement — etcd encryption at rest for Secrets.** callout, replace `etcdctl get /registry/secrets/lenny-system/<secret-name> | hexdump` with:

```markdown
etcdctl get /registry/secrets/<credentials namespace>/<secret-name> | hexdump
```

(b) Under **Initial setup workflow:**, replace step 1, which begins "1. **Create Kubernetes Secrets** containing credential material", with:

```markdown
1. **Create the credential Secrets** in the credentials namespace for the credentials a pool registers by `secretRef`.
```

Replace the last sentence of step 2, "The pool definition references the Secrets by `secretRef`.", with:

```markdown
The pool definition references those Secrets by `secretRef`, and a credential added by value is submitted with `secretData` after the pool exists ([Credential Storage Topology](#credential-storage-topology) **Registering a Secret by reference.** and **Credential values submitted through the admin API.**).
```

Replace step 3, which begins "3. The `lenny-bootstrap` Job", with:

```markdown
3. The `lenny-bootstrap` Job ([Section 17.6](17_deployment-topology.md#176-packaging-and-installation)) seeds pool definitions and does not create Secrets. A Secret may be created after the Job runs, and a read of a Secret that does not yet exist fails closed.
```

(c) Replace the whole **Token Service secret resolution.** paragraph with:

```markdown
**Token Service secret resolution.** Every Token Service read of a credential Secret follows these rules. The `ProbeSecretAccess` call on the gateway↔Token-Service mTLS link carries the admin-time probe ([Credential Storage Topology](#credential-storage-topology) **Admin-time Secret probe (required).**) and the gateway's keyed reads.

1. The Token Service reads only the credentials namespace ([Credential Storage Topology](#credential-storage-topology) **Credentials namespace.**), and a `ProbeSecretAccess` request names a Secret by name alone.
2. Each read performs one `get`, and no Secret cache serves it. An update to a Secret takes effect on the next read.
3. NotFound returns `NOT_FOUND`, and so does a missing or empty key on a call that names a key. Forbidden returns `DENIED`. Any other failure fails that read with a `TRANSIENT`-category error, and no earlier value is served.
4. A call that names a key returns that key's value with `ALLOWED`. A call that names no key returns the verdict alone and never a value.
5. A call that names a key is a credential-sensitive RPC ([Section 16.4](16_observability.md#164-logging)). Each read is counted in `lenny_token_service_secret_reloads_total` by outcome ([Section 16.1](16_observability.md#161-metrics)).
```

(d) Under **Credential rotation workflow:**, in step 1 replace `kubectl create secret generic anthropic-key-1 --from-literal=api-key=sk-ant-new... --dry-run=client -o yaml | kubectl apply -f -` with:

```markdown
kubectl create secret generic anthropic-key-1 -n <credentials namespace> --from-literal=api-key=sk-ant-new... --dry-run=client -o yaml | kubectl apply -f -
```

Replace step 2, "2. The Token Service detects the Secret change via its informer within 30 seconds.", with:

```markdown
2. The next read returns the new value.
```

In step 3, append the following sentence after "only the Secret's content.":

```markdown
A credential added by value is rotated with `PUT /v1/admin/credential-pools/{name}/credentials/{credId}` carrying `secretData`.
```

Leave step 4 unchanged.

(e) In the **GitOps pattern.** list, replace the whole bullet that begins "- **Ordering:** ESO / sealed-secrets controllers must populate the Secrets" with this single physical line:

```markdown
- **Ordering:** Secret-sync resources (`ExternalSecret`, `SealedSecret`) live in the credentials namespace and sync after the Lenny chart, which creates that namespace. In ArgoCD, use sync-wave ordering (e.g., Lenny chart at sync-wave `0`, `ExternalSecret` at sync-wave `1`). The Token Service tolerates temporarily missing Secrets (a read fails closed until the Secret exists and succeeds on the first read after it is created).
```

(f) Under **Emergency Credential Revocation**, in the **Emergency revocation runbook:** paragraph, replace "(5) add a replacement credential to the pool via `PUT /v1/admin/credential-pools/{name}`;" with:

```markdown
(5) add a replacement credential to the pool via `POST /v1/admin/credential-pools/{name}/credentials`;
```

Replace "(6) rotate the underlying secret (Kubernetes Secret or external secrets manager) before re-enabling the credential ID." with:

```markdown
(6) rotate the underlying secret (a Kubernetes Secret, an external secrets manager, or a credential update carrying `secretData`) before re-enabling the credential ID.
```

Leave the **Direct mode additional step:** sentences unchanged.

### SPEC-4 · spec/05_runtime-registry-and-pool-model.md § Minimal Configuration

In the minimal configuration YAML block, replace `        secretRef: lenny-system/anthropic-key` with:

```yaml
        secretRef: anthropic-key # Secret in the credentials namespace
```

### SPEC-5 · spec/10_gateway-internals.md § 10.2 Authentication, Authorization and RBAC

(a) In the **Permission matrix:** table, replace the row that begins "| Manage credential pools" with the following row. The row stays one physical line.

```markdown
| Manage credential pools                        | Yes (all tenants) | Yes (own tenant)¹ | Read-only (own tenant)   |         No         |            No            |
```

(b) Insert the following line directly after the last row of the **Permission matrix:** table, ahead of any paragraph that follows the table (including the **Session ownership.** paragraph if proposal 0110 has landed), separated from the table by one blank line:

```markdown
¹ Introducing a `secretRef` into a credential pool requires `platform-admin` ([Section 4.9](04_system-components.md#49-credential-leasing-service) **Registering a Secret by reference.**).
```

### SPEC-6 · spec/13_security-model.md § 13.2.4 `lenny-system` Namespace NetworkPolicies

(a) In the Egress Allowed cell of the row that begins "| **Token Service** (`lenny.dev/component: token-service`)", insert the following directly after "KMS endpoint (HTTPS 443, CIDR from `{{ .Values.kms.endpointCIDR }}`);". The row stays one physical line.

```markdown
 kube-apiserver (TCP 443, CIDR `{{ .Values.kubeApiServerCIDR }}`), for the admin-time Secret probe and credential Secret reads in the credentials namespace ([Section 4.9](04_system-components.md#49-credential-leasing-service));
```

(b) In the **`kubeApiServerCIDR` and `webhookIngressCIDR` Helm values (NET-040):** callout, replace "The CIDR that the **gateway** uses to reach the kube-apiserver over TCP 443." with:

```markdown
The CIDR that the components whose Egress Allowed cell in the table above names it use to reach the kube-apiserver over TCP 443.
```

### SPEC-7 · spec/15_external-api-surface.md § 15.1 REST API

Every replacement row stays one physical line. Locate each row by its method and endpoint cells.

(a) Replace the row `| \`POST\`   | \`/v1/admin/credential-pools\` | Create a credential pool ...` with:

```markdown
| `POST`   | `/v1/admin/credential-pools`                                    | Create a credential pool (tenant-scoped; `tenant-admin` sees own tenant's pools, `platform-admin` sees all with optional `?tenant_id=` filter). Each `credentials[].secretRef` is subject to [Section 4.9](04_system-components.md#49-credential-leasing-service) **Secret references.** and **Registering a Secret by reference.** (`400 VALIDATION_ERROR`, `403 FORBIDDEN`) and to the **Admin-time Secret probe (required).** (`422 CREDENTIAL_SECRET_RBAC_MISSING`, `503 CREDENTIAL_PROBE_UNAVAILABLE`). `secretData` is refused with `400 VALIDATION_ERROR`. |
```

(b) Replace the row `| \`PUT\`    | \`/v1/admin/credential-pools/{name}\` | Update a credential pool (requires \`If-Match\`) |` with:

```markdown
| `PUT`    | `/v1/admin/credential-pools/{name}`                             | Update a credential pool (requires `If-Match`). Each `secretRef` is subject to the rules and outcomes named on pool creation, and `secretData` is refused with `400 VALIDATION_ERROR`. Lenny-written Secret handling: [Section 4.9](04_system-components.md#49-credential-leasing-service) **Credential values submitted through the admin API.** |
```

(c) Replace the row `| \`DELETE\` | \`/v1/admin/credential-pools/{name}\` | Delete a credential pool |` with:

```markdown
| `DELETE` | `/v1/admin/credential-pools/{name}`                             | Delete a credential pool. Lenny-written Secret handling: [Section 4.9](04_system-components.md#49-credential-leasing-service) **Credential values submitted through the admin API.** |
```

(d) In the `.../credentials/{credId}/re-enable` row, replace "Requires `platform-admin`." with:

```markdown
Requires the `manage_credential_pools` permission (`tenant-admin` for its own tenant).
```

(e) Replace the row `| \`POST\`   | \`/v1/admin/credential-pools/{name}/credentials\` | Add a credential to a pool. See ...` with:

```markdown
| `POST`   | `/v1/admin/credential-pools/{name}/credentials`                 | Add a credential to a pool by `secretRef`, `roleArn`, or `secretData`. A `secretRef` is subject to the rules and outcomes named on pool creation. Lenny-written Secret handling: [Section 4.9](04_system-components.md#49-credential-leasing-service) **Credential values submitted through the admin API.** See [Section 24.5](24_lenny-ctl-command-reference.md#245-credential-management). |
```

(f) Replace the row `| \`PUT\`    | \`/v1/admin/credential-pools/{name}/credentials/{credId}\` | Update a credential in the pool (body may change \`secretRef\`). ...` with:

```markdown
| `PUT`    | `/v1/admin/credential-pools/{name}/credentials/{credId}`        | Update a credential in the pool (body may change `secretRef`, `roleArn`, or `region`, or carry `secretData`). Requires `If-Match`. A `secretRef` is subject to the rules and outcomes named on pool creation. Lenny-written Secret handling: [Section 4.9](04_system-components.md#49-credential-leasing-service) **Credential values submitted through the admin API.** See [Section 24.5](24_lenny-ctl-command-reference.md#245-credential-management). |
```

(g) In the `DELETE` row for `/v1/admin/credential-pools/{name}/credentials/{credId}`, insert the following directly before " See [Section 24.5]":

```markdown
 Lenny-written Secret handling: [Section 4.9](04_system-components.md#49-credential-leasing-service) **Credential values submitted through the admin API.**
```

(h) Replace the error catalog row that begins "| `CREDENTIAL_SECRET_RBAC_MISSING`" with:

```markdown
| `CREDENTIAL_SECRET_RBAC_MISSING` | `PERMANENT` | 422    | Admin credential-pool write refused by the admin-time Secret probe ([Section 4.9](04_system-components.md#49-credential-leasing-service) **Registering a Secret by reference.** and **Admin-time Secret probe (required).**). `details.missingSecrets` lists the canonical reference of every failing Secret. Create each Secret in the credentials namespace and retry. |
```

(i) Replace the error catalog row that begins "| `CREDENTIAL_PROBE_UNAVAILABLE`" with the following two rows. Each row stays one physical line.

```markdown
| `CREDENTIAL_PROBE_UNAVAILABLE` | `TRANSIENT` | 503    | Admin credential-pool write refused because the admin-time Secret probe returned no verdict ([Section 4.9](04_system-components.md#49-credential-leasing-service) **Registering a Secret by reference.** and **Admin-time Secret probe (required).**). `details.secretRef` names the reference whose probe could not be evaluated. Retry after resolving Token Service health. |
| `TOKEN_SERVICE_NOT_CONFIGURED` | `PERMANENT` | 422    | Admin credential-pool write refused because no Token Service is configured ([Section 4.9](04_system-components.md#49-credential-leasing-service) **Credential values submitted through the admin API.** rule 6). Enable the Token Service and retry. |
```

(j) In the error catalog row that begins "| `SERVICE_UNAVAILABLE`", insert the following directly after "all of which fail closed rather than degrade open." and before " `Retry-After` is set.":

```markdown
 It is also returned by an admin credential-pool write under [Section 4.9](04_system-components.md#49-credential-leasing-service) **Credential values submitted through the admin API.** rule 5.
```

### SPEC-8 · spec/16_observability.md § 16.1 Metrics and § 16.4 Logging

(a) In §16.1, replace the metric row that begins "| Token Service secret reloads (`lenny_token_service_secret_reloads_total`" with the following row. The row stays one physical line.

```markdown
| Token Service secret reloads (`lenny_token_service_secret_reloads_total`, one per credential Secret read or probe, by outcome: `success`, `not_found`, `denied`, `unavailable`)                                   | Counter         |
```

(b) In §16.4, replace the bullet that begins "- **Credential-sensitive RPCs**" with:

```markdown
- **Credential-sensitive RPCs** (`AssignCredentials`, `RotateCredentials`, and `ProbeSecretAccess` when its request names a key, because the response then carries a credential value) are excluded from payload-level logging, gRPC access logs, and OTel trace span attributes. Only RPC name, lease ID, provider type, Secret name, and outcome are recorded.
```

### SPEC-9 · spec/12_storage-architecture.md § 12.8 Compliance Interfaces, tenant deletion lifecycle, and § 12.9 Data Classification

(a) In the Phase 4 row of the tenant deletion lifecycle table, insert the following, preceded by one space, directly after "→ `CredentialPoolStore`" in "→ `CredentialPoolStore` → `runtime_tenant_access` rows for this tenant". The row stays one physical line.

```markdown
→ Lenny-written credential Secrets ([Section 4.9](04_system-components.md#49-credential-leasing-service) **Credential values submitted through the admin API.** rules 3, 5, and 6)
```

(b) In the **Default data-type mappings:** table, replace the Store cell `Postgres (encrypted, KMS-backed)` of the `Credential pool secrets` row with `Kubernetes Secrets (credentials namespace)`. The row stays one physical line.

### SPEC-10 · spec/17_deployment-topology.md § 17.2, § 17.6, § 17.7

(a) §17.2 Namespace Layout: in the code block, insert the following line after the `lenny-system/` line:

```
lenny-credentials/    # Credential Secrets only (credentials.namespace); no pods
```

Then insert the following paragraph after the code block and before the paragraph that begins "**Operability-vs-agent boundary.**":

```markdown
**Credentials namespace.** The credentials namespace holds credential Secrets and nothing else, and no pod runs in it ([Section 4.9](04_system-components.md#49-credential-leasing-service) **Credentials namespace.**).
```

(b) §17.6 Packaging and Installation, **Key Helm values:** list: insert the following bullet directly after the bullet "- `minio.endpoint` — object storage endpoint":

```markdown
- `credentials.namespace` — namespace that holds credential Secrets (default `lenny-credentials`); see [Section 4.9](04_system-components.md#49-credential-leasing-service) **Credentials namespace.**
```

(c) §17.6 **Checks performed** table: in the row that begins "| etcd Secret encryption (warning)", replace `'etcdctl get /registry/secrets/lenny-system/<name> \| hexdump'` with the following. The row stays one physical line, and the text matches the CODE-7 constant.

```markdown
'etcdctl get /registry/secrets/<credentials namespace>/<name> \| hexdump'
```

(d) §17.7 Operational Runbooks, **Token Service outage** entry. In *Diagnosis:*, replace "(5) Verify Secret access: `kubectl auth can-i get secret -n lenny-system --as=system:serviceaccount:lenny-system:lenny-token-service` — RBAC denial prevents credential decryption." with:

```markdown
(5) Verify Secret access: `kubectl auth can-i get secrets -n <credentials.namespace> --as=system:serviceaccount:lenny-system:lenny-token-service`, and confirm that the `allow-token-service` NetworkPolicy carries the `kubeApiServerCIDR` egress rule. An RBAC denial or a missing egress rule fails every credential Secret read.
```

In *Remediation:*, replace "(4) **RBAC fix:** If Secret access is denied, re-apply the Token Service RBAC: `kubectl apply -f charts/lenny/templates/token-service-rbac.yaml`." with:

```markdown
(4) **RBAC fix:** If Secret access is denied, run `helm upgrade` to re-render the `lenny-token-service-credentials` Role and its RoleBinding.
```

(e) §17.7 Operational Runbooks, **Credential pool exhaustion** entry, *Remediation:* step (2): replace "with a new `secretRef`." with:

```markdown
carrying `secretData`, or have a `platform-admin` register a new `secretRef`.
```

### SPEC-11 · spec/24_lenny-ctl-command-reference.md § 24.5 Credential Management

Replace the eight data rows of the table under `### 24.5 Credential Management` with the following rows, each one physical line:

```markdown
| `lenny-ctl admin credential-pools list` | List all credential pools and their status | `GET /v1/admin/credential-pools` | `tenant-viewer` (own tenant) |
| `lenny-ctl admin credential-pools get --pool <name>` | Show credential pool details including per-credential health scores and lease counts | `GET /v1/admin/credential-pools/{name}` | `tenant-viewer` (own tenant) |
| `lenny-ctl admin credential-pools add-credential --pool <name> --provider <p>` | Add a credential to the pool by `--secret-ref` or `--role-arn`. A credential value is added through the admin API `secretData` field ([Section 4.9](04_system-components.md#49-credential-leasing-service)). | `POST /v1/admin/credential-pools/{name}/credentials` | `tenant-admin` (own tenant)¹ |
| `lenny-ctl admin credential-pools update-credential --pool <name> --credential <id>` | Update a credential in the pool | `PUT /v1/admin/credential-pools/{name}/credentials/{credId}` | `tenant-admin` (own tenant)¹ |
| `lenny-ctl admin credential-pools remove-credential --pool <name> --credential <id>` | Remove a credential from the pool | `DELETE /v1/admin/credential-pools/{name}/credentials/{credId}` | `tenant-admin` (own tenant) |
| `lenny-ctl admin credential-pools revoke-credential --pool <name> --credential <id> --reason <r>` | Emergency revocation of a single credential; affects all active leases (terminated in direct mode, denied in place in proxy mode). See [Section 4.9](04_system-components.md#49-credential-leasing-service) emergency revocation runbook. | `POST /v1/admin/credential-pools/{name}/credentials/{credId}/revoke` | `tenant-admin` (own tenant) |
| `lenny-ctl admin credential-pools revoke-pool --pool <name> --reason <r>` | Emergency revocation of all credentials in a pool | `POST /v1/admin/credential-pools/{name}/revoke` | `tenant-admin` (own tenant) |
| `lenny-ctl admin credential-pools re-enable --pool <name> --credential <id> --reason <r>` | Re-enable a previously revoked credential. Use after rotating the underlying secret at the provider and updating the Kubernetes Secret, or after a credential update carrying `secretData`. | `POST /v1/admin/credential-pools/{name}/credentials/{credId}/re-enable` | `tenant-admin` (own tenant) |
```

Directly after the table, insert:

```markdown
¹ Introducing a `secretRef` requires `platform-admin` ([Section 4.9](04_system-components.md#49-credential-leasing-service) **Registering a Secret by reference.**).
```

### SPEC-12 · spec/25_agent-operability.md § Action Tools (mutating)

(a) Replace the row that begins "| `lenny_credential_add`" with:

```markdown
| `lenny_credential_add` | `POST /v1/admin/credential-pools/{name}/credentials` | Add a credential to a pool by `secretRef`, `roleArn`, or `secretData` ([§4.9](04_system-components.md#49-credential-leasing-service) Credential Storage Topology) |
```

(b) In the row that begins "| `lenny_credential_retire`", replace "Pool credential rotation is performed by revoking the old credential and adding a replacement via `lenny_credential_add` — see [§4.9](04_system-components.md#49-credential-leasing-service) Credential rotation workflow." with:

```markdown
Pool credential rotation follows [§4.9](04_system-components.md#49-credential-leasing-service) Credential rotation workflow.
```

### SPEC-13 · spec/28_communication-channels.md § 28.7 Wire-contract artifact register

In the row that begins "| `schemas/lenny-tokenservice.proto` |", replace "and for the admin-time probe of the Token Service's own read access to a referenced Kubernetes Secret" with the text below. The row stays one physical line, and every other cell is unchanged.

```markdown
and for the `ProbeSecretAccess` credential Secret read, which serves the admin-time probe and the gateway's keyed credential reads
```

### SPEC-14 · spec/18_build-sequence.md § 18.38 Phase 17a — Documentation, governance, community launch

In the bullet that begins "- Decommission and uninstall runbook at `docs/runbooks/uninstall.md`", replace "MinIO bucket replication targets, persistent volumes)" with:

```markdown
MinIO bucket replication targets, persistent volumes, and the credentials namespace with its Secrets ([§4.9](04_system-components.md#49-credential-leasing-service) **Credentials namespace.** rule 1))
```

## Spec files touched

- spec/04_system-components.md (SPEC-1, SPEC-2, SPEC-3)
- spec/05_runtime-registry-and-pool-model.md (SPEC-4)
- spec/10_gateway-internals.md (SPEC-5)
- spec/13_security-model.md (SPEC-6)
- spec/15_external-api-surface.md (SPEC-7)
- spec/16_observability.md (SPEC-8)
- spec/12_storage-architecture.md (SPEC-9)
- spec/17_deployment-topology.md (SPEC-10)
- spec/24_lenny-ctl-command-reference.md (SPEC-11)
- spec/25_agent-operability.md (SPEC-12)
- spec/28_communication-channels.md (SPEC-13)
- spec/18_build-sequence.md (SPEC-14)
