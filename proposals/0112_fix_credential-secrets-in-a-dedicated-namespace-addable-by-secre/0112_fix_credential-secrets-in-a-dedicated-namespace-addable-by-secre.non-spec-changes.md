# Non-spec changes — Credential Secrets in a dedicated namespace, addable by reference or through the admin API

## Design (implementation-facing)

The code implements the §4.9 paragraphs SPEC-2 and SPEC-3 stage at these seams.

- **One parser.** `credential.ParseSecretRef` (CODE-1) is the only implementation of SPEC-2 **Secret references.** The admin handlers and the bootstrap upsert canonicalize with it (CODE-3). The gateway prober and the VCS adapter parse the stored ref and send the name alone (CODE-3, CODE-6). The chart passes refs through untouched.
- **One Token Service Secret access.** `ProbeSecretAccess` (CODE-2) is one uncached `Get` in the configured namespace, with an optional key that returns the value. It implements SPEC-3 **Token Service secret resolution.** Its two consumers are the admin probe (CODE-3) and the VCS `SecretReader` adapter (CODE-6).
- **One writer.** The gateway's `CredentialSecretWriter` (CODE-4) is the only component that creates, updates, or deletes a credential Secret. The admin handlers call it inside the `credentialPools.Update` mutate closure, and the tenant eraser calls it after `DeleteByTenant` (CODE-5).
- **Chart.** CHART-1 renders the namespace, its quota, the two Roles, the render guard, the Token Service egress, and both namespace flags from one value.

The writer and the read adapter are wired only when the Token Service address is configured, which matches the existing prober gating in `cmd/lenny-gateway/executor_credentials.go`.

## Staged code changes

### CODE-1 · `credential.ParseSecretRef` and the Lenny-written name helpers

Targets: `pkg/credential/secretref.go` (new), `pkg/credential/secretref_test.go` (new).

```go
// ManagedSecretPrefix is the SPEC-2 Secret references rule 3 reserved prefix.
const ManagedSecretPrefix = "lenny-cred-"

// ParseSecretRef returns the Secret name a ref names, or an error when the
// ref breaks SPEC-2 Secret references rule 1.
func ParseSecretRef(ref, credNS string) (name string, err error)

// CanonicalSecretRef returns "<credNS>/<name>".
func CanonicalSecretRef(credNS, name string) string

// ManagedSecretName returns ManagedSecretPrefix followed by the first 40
// hex characters of sha256(tenant + "\x00" + pool + "\x00" + credID).
func ManagedSecretName(tenant, pool, credID string) string

// ManagedSecretTenantHash returns the SPEC-2 rule 2 tenant-hash label value.
func ManagedSecretTenantHash(tenant string) string

// IsManaged reports whether name carries ManagedSecretPrefix.
func IsManaged(name string) bool
```

`ParseSecretRef` accepts the forms SPEC-2 **Secret references.** rule 1 defines and refuses every other reference with a typed error, `ErrInvalidSecretRef`, which the handlers map to `400 VALIDATION_ERROR` with `details.field: "secretRef"`. Reuse `k8s.io/apimachinery/pkg/util/validation.IsDNS1123Subdomain`, which `go.mod` already carries. Cite `// spec: §4.9 (Credential Storage Topology)`.

### CODE-2 · Token Service: `ProbeSecretAccess` becomes the single Get-only, optionally keyed Secret access

Targets: `schemas/lenny-tokenservice.proto`, `pkg/proto/tokenservice/v1` (regenerated, not hand-edited), `pkg/tokenservice/secretprobe/secretprobe.go` and `secretprobe_test.go`, `pkg/tokenservice/secretaccess.go`, `pkg/tokenservice/grpc.go`, `pkg/tokenservice/probe_secret_access_test.go`, `pkg/tokenservice/metrics.go`, `pkg/tokenservice/audit_test.go`, `pkg/tokenservice/promemit/promemit.go` and `promemit_test.go`, `cmd/lenny-token-service/flags.go` and `flags_test.go`, `cmd/lenny-token-service/surfaces.go`, `cmd/lenny-token-service/main.go`, `tests/tier1_unit/flagdefaults/flagdefaults_test.go`.

1. Proto:

   ```proto
   message ProbeSecretAccessRequest {
     // secret_name is the Secret name in the Token Service's configured
     // credentials namespace. No request names a namespace.
     string secret_name = 1;
     reserved 2;
     reserved "namespace";
     // key, when set, asks for that data key's value.
     string key = 3;
   }

   message ProbeSecretAccessResponse {
     Verdict verdict = 1;
     // value is set only when key was set and verdict is ALLOWED.
     bytes value = 2;
   }
   ```

   Rewrite the `Verdict` comments so `DENIED` reads "the Get returned Forbidden (the chart Role is absent)". Remove every mention of the SelfSubjectAccessReview and of `resourceNames`.
2. `secretaccess.go`: change the interface to `ProbeSecretAccess(ctx context.Context, name, key string) (SecretAccessVerdict, []byte, error)`. Rewrite the `SecretAccessDenied` comment as in step 1.
3. `secretprobe.go`: delete the SelfSubjectAccessReview and the namespace parameter. The prober holds the namespace from `--secret-namespace`. It performs one `Get` with no cache and maps the result per SPEC-3 **Token Service secret resolution.** rule 3. Any error that is not Forbidden or NotFound is returned as a Go error, and `grpc.go` maps it to `codes.Unavailable`. An unkeyed call never returns a value. Bound the `Get` with `context.WithTimeout` at a 5-second `secretprobe` package constant.
4. `grpc.go`: `ProbeSecretAccess` passes `req.SecretName` and `req.Key`, sets `value` only when `key` is set and the verdict is `ALLOWED`, and never puts the value into an error string or a gRPC status message. Update the `SetSecretAccessProber` and `ProbeSecretAccess` doc comments so they cite SPEC-3 instead of the SSAR.
5. `promemit.go`: replace the signing-secret Help text and `IncSecretReload` with `IncCredentialSecretRead(outcome string)`, outcome one of `success`, `not_found`, `denied`, or `unavailable`, with no secret-name label (SPEC-8 (a)). Add `IncCredentialSecretRead` to `tokenservice.Metrics`, `NoMetrics`, and the `recordingMetrics` fake in `audit_test.go`, and switch `promemit_test.go` to it. The `grpc.go` `ProbeSecretAccess` handler records one outcome per call through `s.metrics`.
6. Redaction seam: the Token Service gRPC server installs a unary interceptor that treats `ProbeSecretAccess` with a non-empty `key` as credential-sensitive under SPEC-8 (b). It follows the `IsCredentialSensitiveMethod` pattern in `pkg/adapter/credredact.go` and records only the method, the Secret name, and the outcome. Install it in `cmd/lenny-token-service/surfaces.go` beside `spiffeAuditInterceptor`.
7. `flags.go`: rewrite the `--secret-namespace` doc comment to say the flag names the credentials namespace and that the chart sets it from `credentials.namespace`. Keep the flag name. Add `--cluster-qps` and `--cluster-burst` (env `LENNY_CLUSTER_QPS` and `LENNY_CLUSTER_BURST`, defaults 100 and 200, as on the gateway), and set `cfg.QPS` and `cfg.Burst` from them in `main.go` `inClusterClientset`, because CODE-6 puts every VCS read on this client.

### CODE-3 · Gateway admin: canonical refs, platform-admin registration, reserved prefix, single 422 key

Targets: `pkg/gateway/externalapi/admin/credential_pools.go` (`probeSecretRefs`, `secretRefsOf`, `newSecretRefs`, `handleCreateCredentialPool`, `handleUpdateCredentialPool`, `handleAddCredential`, `handleUpdateCredentialEntry`; delete `tokenServiceRBACPatch`), `pkg/gateway/externalapi/admin/bootstrap.go` (`upsertCredentialPools`), `cmd/lenny-gateway/secret_probe.go`, `cmd/lenny-gateway/flags.go`, `pkg/gateway/externalapi/admin/credential_pool_rbac_probe_test.go`, and `pkg/gateway/externalapi/admin/credential_secret_ref_test.go` (new). Existing tests that submit a credential-pool `secretRef` of the form `lenny-system/<name>` through the admin API or the bootstrap seed, `tests/tier4_integration/drift_gatewayreader_http_test.go` among them, move to bare names.

1. Flag: add `--credentials-namespace` (env `LENNY_CREDENTIALS_NAMESPACE`, default `lenny-credentials`) to the gateway and pass it to the admin `Router` and to the prober.
2. Canonicalization: every handler parses each submitted `secretRef` with `credential.ParseSecretRef` before any probe or store call and stores `credential.CanonicalSecretRef`. `secretRefsOf` and `newSecretRefs` compare canonical forms.
3. Probe: `probeSecretRefs` sends canonical refs. On a `DENIED` or `NOT_FOUND` verdict the 422 body carries `details.missingSecrets` (canonical refs) and no `remediation`. Delete `tokenServiceRBACPatch`. The 503 keeps `details.secretRef`. `probeSecretRefs` returns a typed error carrying the code and details instead of writing the response, and `writeCredentialMutateError`, the pool PUT error path, and `handleCreateCredentialPool` map it, because the default arm of `writeCredentialMutateError` maps an unknown error to `400`. It probes the step 4 introduced set after steps 4 and 6, only when the principal holds `platform-admin` and never in a `dryRun` branch (§15.1 **`dryRun` query parameter.**) or on the seed (step 7), and it leaves out every ref `credential.IsManaged` reports (SPEC-2 **Admin-time Secret probe (required).**).
4. Registration rule. Steps 4 and 6 run in that order against the row the write is decided on, with the introduced set computed from the submitted refs before any mutation is applied: the locked row `p` inside each `credentialPools.Update` mutate closure; the handler's unlocked read in each `dryRun` branch; and no row in `handleCreateCredentialPool`, where every submitted ref is introduced and the steps run ahead of its `dryRun` branch and `Create`. When the introduced set is non-empty and the principal fails `p.HasRole(auth.RolePlatformAdmin)` (the check `connectors.go` uses), return a sentinel that the handler maps to `403 FORBIDDEN`. Use the role check. A permission check is wrong here, because a custom role may hold `manage_credential_pools`.
5. `If-Match` at the locked row: the pool update closure re-checks the `If-Match` version against `p.Version` and returns a sentinel that the handler maps to `412 ETAG_MISMATCH` with `currentEtag`, the code `enforceIfMatch` writes.
6. Reserved prefix, on every write path including bootstrap: a submitted ref whose name `credential.IsManaged` is accepted only when the stored entry with the same id in the step 4 row (none on pool or seed creation) carries the identical canonical ref. Otherwise return `400 VALIDATION_ERROR` with `details.field: "secretRef"`. Run this per credential id. It runs after step 4 (SPEC-2 **Registering a Secret by reference.**). The pool-wide `newSecretRefs` set misses a written ref moved between entries.
7. Bootstrap: `upsertCredentialPools` runs no probe. It canonicalizes each ref and records `seedValidationCode` on the entry for a parse error or a reserved-prefix ref that step 6 refuses.
8. `cmd/lenny-gateway/secret_probe.go`: parse the stored ref with `credential.ParseSecretRef` and send `SecretName: name` with no key.

### CODE-4 · Gateway variant B write path

Targets: `pkg/gateway/externalapi/admin/credential_pools.go` (entry and pool request decoding; `handleAddCredential`, `handleUpdateCredentialEntry`, `handleRemoveCredential`, `handleUpdateCredentialPool`, `handleDeleteCredentialPool`, `handleCreateCredentialPool`), `pkg/gateway/externalapi/admin/bootstrap.go` (`handleBootstrap` decoding, `upsertCredentialPools`), `pkg/gateway/externalapi/admin/credential_secret_writer.go` (new), `cmd/lenny-gateway/credential_secret_writer.go` (new), `cmd/lenny-gateway/credential_secret_writer_test.go` (new), `cmd/lenny-gateway/wiring_fields.go` (the writer field), `cmd/lenny-gateway/executor_credentials.go` (writer construction beside the prober), `cmd/lenny-gateway/adminrouter.go` (`Router` wiring), the step 7 files `pkg/gateway/externalapi/openapi/openapi.json`, `pkg/ops/mcp/generated_tools.go`, `pkg/gateway/externalapi/admin/me.go`, and `me_test.go`, `pkg/gateway/externalapi/admin/credential_secret_data_test.go` (new), whose new fault-injecting fake `fakeCredentialSecretWriter` implements `CredentialSecretWriter`.

1. Request type: add a request-only `credentialEntryRequest` that embeds `CredentialEntryPayload` and adds `SecretData map[string]string \`json:"secretData,omitempty"\``. `CredentialEntryPayload` and `fromCredentialPool` gain no value field, so no response can carry one. Pool creation, pool update, and the bootstrap seed decode `credentials` through a request-only pool type whose entries are `credentialEntryRequest`, so that a `secretData` field reaches the rule 1 refusal; a decode into `CredentialEntryPayload` drops it silently. Apply SPEC-2 **Credential values submitted through the admin API.** rule 1 after decode, and on the seed in the per-entry checks of `upsertCredentialPools` (`seedValidationCode`), checking `secretData` with `validation.IsConfigMapKey` (k8s.io/apimachinery) on each key and `corev1.MaxSecretSize` on the total.
2. Writer interface, at the consumer:

   ```go
   // CredentialSecretWriter writes and deletes Lenny-written credential
   // Secrets in the credentials namespace. It never reads one.
   type CredentialSecretWriter interface {
       Put(ctx context.Context, name string, meta SecretOwner, data map[string]string) error
       Delete(ctx context.Context, name string) error // NotFound returns nil
       DeleteTenant(ctx context.Context, tenant string) error // one DeleteCollection on the tenant-hash label
   }
   ```

   `SecretOwner` carries the tenant id, pool name, and credential id written as the SPEC-2 rule 2 annotations and label.
3. client-go implementation in `cmd/lenny-gateway/credential_secret_writer.go`: `Put` calls `Create` and, on `AlreadyExists`, calls `Update` with an empty `resourceVersion`, which is an unconditional update. It never calls `Get` or `Patch`, because the gateway holds neither verb. `Delete` maps NotFound to nil and discards the response body. `DeleteTenant` calls `DeleteCollection` with the label selector `lenny.dev/tenant-hash=<ManagedSecretTenantHash(tenant)>` and discards the response. Each call carries a bounded context. Wire the writer only when the Token Service address is configured, onto the admin `Router` and onto a `gatewayWiring` field that CODE-5 reads. A nil writer is the SPEC-2 **Credential values submitted through the admin API.** rule 6 condition.
4. Mutate closures. `handleAddCredential`, `handleUpdateCredentialEntry`, `handleRemoveCredential`, `handleUpdateCredentialPool`, and the `upsertCredentialPools` force-update closure implement SPEC-2 **Credential values submitted through the admin API.** rules 3, 4, and 6 inside the `credentialPools.Update` closure, in this order. On credential add and credential update, return `credentialpoolstore.ErrNotFound` when `!p.IsActive()` (`DeletedAt` is a `time.Time`). Snapshot the refs of the locked row whose `credential.ParseSecretRef` name `credential.IsManaged` reports, because `applyCredentialPoolUpdate` and the seed closure overwrite `p.Credentials`. Run the existing id checks and the CODE-3 steps 3 to 6 checks. Apply the mutation. Call `credentialpoolstore.Validate(*p)`, because `pgstore.Update` validates only after the closure returns. Compute the Put name with `ManagedSecretName(tenant, pool, id)`, and the dropped set as the snapshot refs absent from the mutated row. `Writer.Delete` takes the parsed name of each dropped ref. Return `errNoCredentialSecretWriter` when the writer is nil and the operation is non-empty. Call `Writer.Put` or `Writer.Delete` last, wrapping any error in `errCredentialSecretOp`.
5. Error mapping and `dryRun`. `writeCredentialMutateError`, the pool PUT error path, and the pool DELETE error path map `errNoCredentialSecretWriter` to `422 TOKEN_SERVICE_NOT_CONFIGURED`, which is added to the `errorclassify` table as `{CategoryPermanent, false}`, and `errCredentialSecretOp` to `503 SERVICE_UNAVAILABLE`, both through `writeError`, which sets `Retry-After` only on the retryable 503. Once the closure has called a writer method, any later `Update` error, including a failure of the `UPDATE` statement or the commit, maps to `503 SERVICE_UNAVAILABLE`; the closure sets a flag before its writer call so the handler can tell this case from a validation error (SPEC-2 **Credential values submitted through the admin API.** rule 5). The pool DELETE error path also maps the step 6 `If-Match` sentinel to `412 ETAG_MISMATCH`, as the pool PUT path does. `upsertCredentialPools` records either error as a `seedStoreErrorCode` entry error. The handler-level checks (`assignmentStrategy`, `cacheScope`, `proxyDialect`, `If-Match`, and CODE-3 step 2) stay ahead of `Update`. `handleAddCredential` and `handleUpdateCredentialEntry` gain a `dryRun` branch modeled on the `handleUpdateCredentialPool` one: it runs the step 4 checks and mutation on a copy of an unlocked `credentialPools.Get` read and returns through `writeDryRun`. Every `dryRun` branch, including the `upsertCredentialPools` `opts.dryRun` path, calls no writer method and applies the step 4 writer-nil check against its unlocked read of the pool (SPEC-2 **Credential values submitted through the admin API.** rule 5).
6. Pool DELETE: replace the unlocked `Get` followed by `SoftDelete` with one `Update` closure that re-checks a present `If-Match` against `p.Version` (the CODE-3 step 5 sentinel), applies the step 4 writer-nil check, calls `Writer.Delete` on each Lenny-written name parsed with `credential.ParseSecretRef` from the row's refs, and sets `DeletedAt` (SPEC-2 **Credential values submitted through the admin API.** rule 4).
7. Revoke and re-enable call no writer method (SPEC-2 **Credential values submitted through the admin API.** rule 3). Re-enable's role follows SPEC-7 (d). Set the re-enable operation's `x-lenny-required-role` in `openapi.json` to `tenant-admin` by hand, because `genopsschemas` splices only lenny-ops routes. Regenerate `generated_tools.go` with `make generate`. Set the `admin.re_enable_pool_credential` `MinRole` in `me.go` to `auth.RoleTenantAdmin` and correct its catalog comment. In `me_test.go`, move the tool into the tenant-admin list of `TestAuthorizedToolsIncludeCredentialPools_spec_24_5` in place of its negative assertion, and extend `TestCredentialPoolCatalogMirrorsOpenAPI_spec_25_14` to assert that each tool's `x-lenny-required-role` equals its catalog `MinRole`.
8. Audit payloads stay id-only: pool name, credential id, canonical `secretRef`, and outcome.

**IMPLEMENTOR'S CHOICE:** whether pool DELETE uses `Update` with a soft-delete mutation or a new locked `SoftDelete` variant in `credentialpoolstore`. The If-Match re-check, the Secret deletions, and the `deleted_at` write must happen under one row lock, and the existing `SoftDelete` callers must keep working.

### CODE-5 · Tenant erasure deletes Lenny-written Secrets after `DeleteByTenant`

Targets: `cmd/lenny-gateway/tenant_erasers.go` (the `credential_pool` eraser), `cmd/lenny-gateway/tenant_erasers_test.go`, with a new fault-injecting fake that implements `CredentialSecretWriter`.

1. The `credential_pool` eraser reads the writer from the `gatewayWiring` field CODE-4 step 3 sets, so `buildTenantErasers`, `tenantErasersAfterSessions`, and their test callers keep their signatures. It implements the tenant-deletion parts of SPEC-2 **Credential values submitted through the admin API.** rules 3, 5, and 6: with a writer, `credentialPools.DeleteByTenant` and then `Writer.DeleteTenant`. An error from either is returned wrapped with the entry name, and `tenantEraser` aborts the phase for retry.
2. With no writer, the rule 6 check reads `credentialPools.List(ctx, tenant, credentialpoolstore.ListFilter{IncludeDeleted: true})` and tests the `credential.ParseSecretRef` name of each ref with `credential.IsManaged`. Add no store method.
3. Cite `// spec: §12.8 (Compliance Interfaces), §4.9 (Credential Storage Topology)`.

### CODE-6 · Gateway VCS reader through the Token Service keyed read

Targets: `cmd/lenny-gateway/secret_probe.go` (a `Resolve` method on `tokenServiceSecretProber` implementing `vcscred.SecretReader`), `cmd/lenny-gateway/auxstores.go` (the `vcsCreds` wiring), `pkg/gateway/provisioning/vcscred/vcscred.go` (the `SecretReader` doc comment).

1. `Resolve(ctx, ref)` parses the ref with `credential.ParseSecretRef` and calls `ProbeSecretAccess{SecretName: name, Key: "token"}`. `ALLOWED` returns the value. `NOT_FOUND`, `DENIED`, or a parse error wraps `vcscred.ErrNoUsableCredential`. A gRPC error is returned wrapped, with no retry and no cached value.
2. `auxstores.go`: build `vcsCreds` from the Token Service stub when the Token Service address is set, and leave it nil otherwise, which keeps the existing "no resolver wired" error. Remove the `connectorsecret.NewKubeResolver` use for VCS. Leave `connectorsecret` unchanged for connector client secrets.
3. Make no change to `vcsTokenError` in `pkg/gateway/mcpfabric/mcptools/mcptools.go` or to `archiveextract.go`. A wrapped gRPC error already surfaces as `INTERNAL_ERROR`, which §15.1 classifies `TRANSIENT` and retryable, on both consumers.
4. Rewrite the `SecretReader` doc comment: production wiring is the Token Service keyed read, and the reader receives a canonical credentials-namespace ref.

### CODE-7 · Preflight etcd encryption warning text

Targets: `pkg/preflight/install_prereqs.go` (`EtcdSecretEncryptionWarning`), `pkg/preflight/install_prereqs_test.go` (the drift assertion).

Change `'etcdctl get /registry/secrets/lenny-system/<name> | hexdump'` in the constant to `'etcdctl get /registry/secrets/<credentials namespace>/<name> | hexdump'`, matching SPEC-10 (c), and update the test's exact-substring assertion to the new text.

### CODE-8 · `lenny-ctl` unreferenced-Secret listing

Targets: `pkg/ctlcli/credential_secrets_unreferenced.go` (new), `pkg/ctlcli/credential_secrets_unreferenced_test.go` (new), and `pkg/ctlcli/main.go` (the `cmdCredentialPools` switch, its subcommand list, and the help line).

Implements the SPEC-11 `list-unreferenced-secrets` row. Cite `// spec: §24.5`.

1. Page `/v1/admin/tenants` with `listAllAdmin`, then each tenant's `/v1/admin/credential-pools?tenantId=<id>`, and resolve each entry's non-empty `secretRef` with `credential.ParseSecretRef(ref, ns)`.
2. List Secret names in `ns` with a `k8s.io/client-go/metadata` client built from `ctrl.GetConfig()` as `kubeSelftestPods` does, paging with `Continue`, behind a one-method lister interface the test fakes. The metadata list keeps Secret values out of the process.
3. Drop each name `credential.IsManaged` reports and each resolved name, and print the rest sorted, one per line.
4. Any admin-API or Kubernetes error, or a ref `ParseSecretRef` refuses, goes to stderr with exit 1 and nothing on stdout. Steps 1 and 2 finish before step 3 prints.
5. Send no `includeDeleted`.

## Staged schema, chart, and migration changes

The proto change is part of CODE-2, because the regenerated package and its callers compile together. No migration is staged.

### CHART-1 · Credentials namespace, quota, Roles, render guard, Token Service egress, flags

Targets: `charts/lenny/templates/credentials-namespace.yaml` (new), `charts/lenny/templates/system-network-policies.yaml`, `charts/lenny/templates/token-service-deployment.yaml`, `charts/lenny/templates/gateway-deployment.yaml`, `charts/lenny/values.yaml`, `pkg/chart/values/values.go`, `pkg/embedded/manifests/manifests.yaml` (regenerated if it embeds the chart), `charts/lenny/tests/credentials-namespace_test.yaml` (new), `charts/lenny/tests/system-network-policies_test.yaml`.

1. Values: add `credentials.namespace: lenny-credentials` to `values.yaml` and `values.go`. Rewrite the `defaultGithubVcsPool.secretRef` comment to say the ref is a bare name or `<credentials.namespace>/<name>`.
2. `credentials-namespace.yaml` renders the objects SPEC-2 **Credentials namespace.** rules 1 to 6 name: the Namespace with `helm.sh/resource-policy: keep`, a ResourceQuota `lenny-credentials-no-pods` with `hard: { pods: "0" }` following `resourcequota.yaml`, and, under `tokenService.enabled`, the Role and RoleBinding `lenny-token-service-credentials` (subject `lenny-token-service` in the release namespace) and the Role and RoleBinding `lenny-gateway-credential-writer` (subject `lenny-gateway` in the release namespace). A `fail` refuses `credentials.namespace` equal to `.Release.Namespace` or to any `agentNamespaces[].name`.
3. `system-network-policies.yaml`: add to `allow-token-service` the same `kubeApiServerCIDR` egress rule with the `kubeApiServerPorts` range loop that `allow-controller-egress` carries.
4. `token-service-deployment.yaml` passes `--secret-namespace` and `gateway-deployment.yaml` passes `--credentials-namespace`, both rendered from one helper that reads `credentials.namespace`.
5. helm-unittest: the Namespace carries the keep annotation; the quota pins `pods: "0"`; the two Roles carry exactly the SPEC-2 verbs and no `resourceNames`; both Role pairs disappear with `tokenService.enabled: false` while the Namespace and quota stay; the guard fails for the release namespace and for each agent namespace; both binaries receive the same namespace; `allow-token-service` carries the kube-apiserver rule. Extend `tests/tier1_unit/rbac/rbac_test.go` with the assertion that no other rendered Role, ClusterRole, or binding grants a verb on `secrets` that applies in the credentials namespace. That assertion is the one that discriminates SPEC-2 rule 7. It must not assert that no other ServiceAccount holds any verb there, because the controller ClusterRole grants pod verbs cluster-wide.

## Staged docs changes

### DOCS-1 · Operator docs, API reference, and runbooks

Follow `doc-style.md` and `doc-content.md`, and add no spec section numbers to published docs.

- `docs/operator-guide/configuration.md`: the Minimal Configuration and Credential Pools YAML use bare names. Leave the Connector Registration `clientSecretRef` unchanged.
- `docs/operator-guide/installation.md`: Step 3 drops the LLM provider credential Secret, which the bootstrap's `credentialPools: []` never references, and links the Credential Pools section of `configuration.md` for adding credentials after install.
- `docs/operator-guide/security.md`: the Credential Pool Model YAML; the Kubernetes RBAC row for `lenny-token-service` becomes namespace-wide `get` in `credentials.namespace`; add a `lenny-gateway` row with `create`, `update`, `delete`, and `deletecollection` there; the Credential-Sensitive Exclusions list and recorded fields follow SPEC-8 (b).
- `docs/operator-guide/lenny-ctl.md`: the credential-pools table follows SPEC-11, footnote included. The Credential Rotation example uses `-n <credentials.namespace>` or `secretData`.
- `docs/operator-guide/namespace-and-isolation.md`: Namespace Layout adds `lenny-credentials/` (`credentials.namespace`) and drops the "three namespaces" count. Describe the keep annotation, the `pods: "0"` quota, the operator obligation to keep other Secrets out and the `lenny-ctl` listing command that checks it (SPEC-11), and that the kept Namespace keeps Helm's release ownership annotations, so a reinstall under a different release name has to delete or re-annotate it first.
- `docs/getting-started/architecture.md`: the namespace prose near "`lenny-system` namespace contains all platform components".
- `docs/api/admin.md`: the Credential management table gains `secretData` (write-only), the `platform-admin` requirement for an introduced `secretRef`, the reserved `lenny-cred-` prefix, the 403, 422, and 503 outcomes, and the roles from SPEC-11.
- `docs/operator-guide/agent-operability.md`: the `lenny_credential_add` and `lenny_credential_retire` rows, matching SPEC-12.
- `docs/operator-guide/troubleshooting.md`: the Credential Pool Exhaustion add and re-enable steps.
- `docs/runbooks/credential-pool-exhaustion.md` and `docs/runbooks/credential-revocation.md`: the add-credential bodies use a bare name or `secretData`. `credential-revocation.md` Step 4 rotates as SPEC-3 (d) steps 1 and 3 give.
- `docs/runbooks/token-service-outage.md`: Diagnosis Step 4 runs `kubectl auth can-i get secrets -n <credentials.namespace>` as the release-namespace ServiceAccount and adds a kube-apiserver egress check. Remediation Step 4 replaces the `helm template | kubectl apply` re-apply with `helm upgrade`.
- `docs/runbooks/etcd-key-rotation.md`: the etcdctl prefix `/registry/secrets/<credentials.namespace>/`.
- `docs/reference/metrics.md`: the `lenny_token_service_secret_reloads_total` row loses the `secret_name` label and lists the SPEC-8 (a) outcomes.

Non-targets: connector `clientSecretRef` examples, and the Postgres DSN Secret in `docs/runbooks/postgres-failover.md`.

### REC-1 · Records

`BUILD-GAPS.md`: file a new finding under the next free `F-4.9.N` id (F-4.9.42 at drafting). It records that the chart never rendered a Token Service Secret Role or its kube-apiserver egress, follows on from closed F-4.9.3, and is closed by this proposal. Do not edit F-4.9.3. File each entry of the summary's **Defects in the shipped tree that this proposal does not stage** as an open finding.

## Testing

Each test carries `// spec:` annotations for the sections named, and every tier-2-and-higher test carries a `// diagnosis:` comment. The deliverable each test lands with is named on it.

1. **Ref parsing** (CODE-1; tier 1; §4.9). Table test over bare, qualified, foreign namespace, three segments, empty segments, an invalid DNS-1123 name, and the reserved prefix. A fuzz test asserts that parse then canonical is idempotent and never yields another namespace. `ManagedSecretName` is deterministic, differs when any tuple member differs, and does not collide when a member contains the separator characters of another (for example tenant `a`, pool `b-c` against tenant `a-b`, pool `c`). `ManagedSecretName` and `ManagedSecretTenantHash` equal, for one fixed input, the SPEC-2 rule 2 values the test computes with `crypto/sha256`.
2. **Token Service access** (CODE-2; tiers 1, 2; §4.9, §16.1). Over a fake clientset and then envtest: an unkeyed `ALLOWED` returns an empty value; a keyed read with a missing key or an empty value gives `NOT_FOUND` and an empty value; Forbidden gives `DENIED`; a 500, or a `Get` against an `httptest` kube-apiserver that never answers, gives `codes.Unavailable` and no value; two reads around a Secret update return the old then the new value (no cache); the value does not appear in any gRPC status message; each call increments exactly one outcome. `tests/tier1_unit/flagdefaults` gains `lenny-token-service` rows for both CODE-2 step 7 rate flags, and `cmd/lenny-token-service/flags_test.go` asserts their defaults.
3. **Keyed-read redaction** (CODE-2; tier 9; §16.4). A captured slog handler and a span recorder see the method, the Secret name, and the outcome for a keyed call, and no value bytes.
4. **Admin canonicalization and 422** (CODE-3; tiers 1, 3; §4.9, §15.1). Each of the four paths stores canonical refs, refuses a foreign namespace with `400` and `details.field: "secretRef"`, returns `422` with `details.missingSecrets` and no `remediation` key, and on a prober `Unavailable` returns `503 CREDENTIAL_PROBE_UNAVAILABLE` with `details.secretRef` and nothing committed. `credential_pool_rbac_probe_test.go` is updated for the removed key.
5. **Registration rule** (CODE-3; tiers 1, 9; §4.9, §10.2). A `tenant-admin` and a custom role holding `manage_credential_pools` each get `403` when introducing a ref on each of the four paths and in each `dryRun` branch, with no probe call, whether or not the Secret exists. A `tenant-admin` round-trip PUT that resubmits the existing refs in bare form passes. So does a `tenant-admin` credential add or update that names a ref another entry of the pool carries. A `platform-admin` passes. A `platform-admin` `dryRun` that introduces a ref makes no probe call. With a store fake that removes the ref between the `Get` and the `Update`, the `tenant-admin` round-trip gets `403`, which is the assertion that discriminates the locked-row check. With the same store fake, a `platform-admin` round-trip makes one probe call for that ref, and a `NOT_FOUND` verdict gives `422` with the row unchanged. A pool PUT whose locked row carries a newer version than the handler's read gets `412` with the row unchanged. A `tenant-admin` gets `403` for a `lenny-cred-` ref the pool does not carry and `400` for a foreign-namespace ref (SPEC-2 **Registering a Secret by reference.**, last sentence).
6. **Reserved prefix** (CODE-3; tiers 1, 9; §4.9). A pool PUT that moves `lenny-cred-H(B)` onto entry A gets `400`. A write that resubmits an entry's own `lenny-cred-` ref in bare form passes. A `platform-admin` that submits a ref with the prefix for a new id gets `400` with no probe call. Bootstrap stores a bare seed ref in canonical form, and records `seedValidationCode` for a foreign-namespace ref, an invalid name, and a prefixed ref the existing entry does not carry, with the other entries applied.
7. **Variant B Secret operations** (CODE-4; tiers 1, 2, 3, 4; §4.9, §15.1). One case per row of the SPEC-2 **Credential values submitted through the admin API.** rule 3 table, asserting the writer calls the row gives. One case per rule 1 sentence, with the last sentence run on an empty `secretData`, an invalid key, and values totalling one byte over `corev1.MaxSecretSize` (exactly the limit passes), with the pool-level sentence run on pool create, pool PUT, and the bootstrap seed (`seedValidationCode`), one for rule 2, and one asserting that no response body carries a submitted value. `cmd/lenny-gateway/credential_secret_writer_test.go` (tier 1) drives the client-go writer over a fake clientset whose reactors return Forbidden for `get` and `patch`, with one case per outcome CODE-4 step 3 gives for `Put`, `Delete`, and `DeleteTenant`; the refused `get` and `patch` discriminate a writer that reads first. The same file runs those outcomes against `tests/testinfra/envtest` (tier 2), as `statelessrouting_envtest_test.go` does: each `Put` case reads the Secret back and asserts the SPEC-2 rule 2 annotations and label, a `Put` over an existing unlabelled Secret discriminates a writer that labels only on `Create`, and a `DeleteTenant` that removes the tenant's Secrets and leaves another tenant's and an unlabelled one discriminates the selector, which the fake clientset does not execute. A `tenant-admin` `secretData` add, and a `tenant-admin` `secretData` update of an entry that carries a `secretRef`, pass with no probe call.
8. **Order and outcomes under faults** (CODE-4; tiers 1, 7a, 8; §4.9). Uses `fakeCredentialSecretWriter` with fault injection and a store fake whose commit fails. One case per rule 4 sentence, per rule 5 bullet and residue row, and per rule 6 write sentence with a nil writer. The rule 5 `dryRun` case runs a `secretData` credential add, a `secretData` credential update, a pool PUT that drops a Lenny-written ref, and a forced seed with `opts.dryRun` that drops one, and asserts no writer call and no `Update`, and with a nil writer the rule 6 refusal; the nil-writer run discriminates a `dryRun` branch that skips the CODE-4 step 4 writer-nil check. The item 5 `dryRun` cases run on the add and the update. A pool PUT, and a pool DELETE carrying `If-Match`, whose locked row carries a newer version than the handler's read get `412` with no writer call and the row unchanged. Each case asserts the response, the writer calls, and the committed row. Each commit-failure case asserts a `503` response (CODE-4 step 5). The discriminating cases: a commit failure after a Put on an addition, which leaves the orphan and issues no Delete; and a pool PUT that drops a written entry while carrying a `400` field, which issues no Delete. With a nil writer, a pool PUT that drops no Lenny-written reference succeeds, and each removal path (credential DELETE, pool PUT, credential PUT, pool DELETE, and forced seed) returns `503` or `SEED_STORE_ERROR` on a Delete failure, and `422 TOKEN_SERVICE_NOT_CONFIGURED` with no `Retry-After`, or `SEED_STORE_ERROR`, on a nil writer. Tier 1 also asserts that `errorclassify.Classify("TOKEN_SERVICE_NOT_CONFIGURED")` returns `(PERMANENT, false)`, because the admin `writeError` path classifies through `ClassifyStatus` and passes without the table entry.
9. **Concurrency** (CODE-4; tier 7a; §4.9). Randomized interleavings of concurrent adds of one id, add against pool DELETE, and removal against re-add, run under `-race` with an invariant checker: no committed entry of an active pool references a missing Secret in a run with no injected fault, and no two committed entries share a Lenny-written ref.
10. **Tenant erasure** (CODE-5; tiers 1, 4, 8; §12.8, §4.9). One case each for the rule 3 tenant-deletion row, which asserts that `Writer.DeleteTenant` runs after `credentialPools.DeleteByTenant` returns, the rule 5 delete-collection bullet, and the rule 6 tenant-deletion sentences. The orphan that no stored reference names is the case that separates the label delete-collection from a per-reference delete. Another tenant's Secrets stay. With a nil writer, a tenant whose only Lenny-written reference is on a soft-deleted pool fails Phase 4, which discriminates the `IncludeDeleted` read.
11. **VCS adapter** (CODE-6; tier 1; §4.9, §14). `ALLOWED` returns the token; `NOT_FOUND`, `DENIED`, and a stored `lenny-system/<name>` ref (with no RPC made) give `vcscred.ErrNoUsableCredential`; `Unavailable` returns an error, no token, and the existing `INTERNAL_ERROR` tool error; the request carries only the parsed name and the key `token`.
12. **Preflight text** (CODE-7; tier 1; §17.6). The drift test asserts the new etcdctl substring.
13. **Chart** (CHART-1; tiers 0, 1; §4.9, §13.2.4, §17.6). The helm-unittest cases and the rendered-RBAC guard in CHART-1 step 5.
14. **Cross-tier** (TEST-1; tiers 3, 5, 9). See TEST-1.
15. **Unreferenced-Secret listing** (CODE-8; tier 1; §24.5). Uses an `httptest` admin server with two tenants and a paged pool list, plus a fake Secret lister. Omitted from the output: a Secret named by a canonical ref, one named by a bare ref, a `lenny-cred-` Secret, and a Secret named only on a later pool page or in the second tenant. The rest print sorted. An empty namespace prints nothing and exits 0. Each of these exits 1 with empty stdout: a lister Forbidden, an error on one tenant's later pool page, and a stored ref outside `--namespace`. The later-page error is the case that discriminates CODE-8 step 4.

### TEST-1 · Cross-tier tests

- `tests/tier3_contract`: a contract test on `ProbeSecretAccess` between the gateway prober and VCS adapter and the Token Service over a fake clientset. It asserts that the request carries no namespace field, that a keyed `ALLOWED` response carries the value and other verdicts do not, and that gRPC `Unavailable` maps to `503 CREDENTIAL_PROBE_UNAVAILABLE` on the admin path. It does not restate the CODE-2 and CODE-6 unit tests.
- `tests/tier5_e2e_kind`: a stock install accepts a `platform-admin` by-reference add; a `secretData` add followed by an authenticated `gitClone` succeeds; tenant deletion removes the tenant's written Secrets and leaves another tenant's; with the kind admin kubeconfig, the SPEC-11 listing command prints an operator-created Secret no pool references and omits a registered Secret and a Lenny-written one.
- `tests/tier9_security`, modeled on `controller_rbac_test.go` (`kubectl auth can-i --as` through the `kind.Cluster` handle): `lenny-token-service` is allowed `get` on `secrets` in the credentials namespace and denied `list` and `watch`; `lenny-gateway` is allowed `create`, `update`, `delete`, and `deletecollection` and denied `get`, `list`, `watch`, and `patch`; `lenny-ops` and the controller and webhook ServiceAccounts are denied every verb on `secrets` there; creating a pod in the namespace is refused by the quota; the Token Service probe reaches the kube-apiserver under the `lenny-system` default-deny; a submitted `secretData` value does not appear in captured gateway or Token Service logs.

The denial, quota, egress, and log-exclusion claims are properties of the installed chart, so only a live authorization decision against chart-installed RBAC on a policy-enforcing CNI verifies them. Rendered-template assertions stay in CHART-1.

## Files touched on application (non-spec)

- pkg/credential/secretref.go, secretref_test.go
- schemas/lenny-tokenservice.proto; pkg/proto/tokenservice/v1 (regenerated)
- pkg/tokenservice/secretprobe/secretprobe.go, secretprobe_test.go; pkg/tokenservice/secretaccess.go, grpc.go, probe_secret_access_test.go, metrics.go, audit_test.go; pkg/tokenservice/promemit/promemit.go, promemit_test.go
- cmd/lenny-token-service/flags.go, flags_test.go, surfaces.go, main.go; tests/tier1_unit/flagdefaults/flagdefaults_test.go
- pkg/gateway/externalapi/errorclassify/errorclassify.go, errorclassify_test.go
- pkg/gateway/externalapi/admin/credential_pools.go, bootstrap.go, credential_secret_writer.go, credential_pool_rbac_probe_test.go, credential_secret_ref_test.go, credential_secret_data_test.go, me.go, me_test.go
- cmd/lenny-gateway/secret_probe.go, flags.go, credential_secret_writer.go, credential_secret_writer_test.go, auxstores.go, wiring_fields.go, executor_credentials.go, adminrouter.go, tenant_erasers.go, tenant_erasers_test.go
- pkg/gateway/provisioning/vcscred/vcscred.go
- pkg/gateway/externalapi/openapi/openapi.json; pkg/ops/mcp/generated_tools.go (regenerated)
- pkg/preflight/install_prereqs.go, install_prereqs_test.go
- pkg/ctlcli/credential_secrets_unreferenced.go, credential_secrets_unreferenced_test.go, main.go
- charts/lenny/templates/credentials-namespace.yaml, system-network-policies.yaml, token-service-deployment.yaml, gateway-deployment.yaml; charts/lenny/values.yaml; pkg/chart/values/values.go; pkg/embedded/manifests/manifests.yaml; charts/lenny/tests/credentials-namespace_test.yaml, system-network-policies_test.yaml; tests/tier1_unit/rbac/rbac_test.go
- tests/tier3_contract, tests/tier5_e2e_kind, tests/tier9_security
- existing admin and tier-4 tests carrying `lenny-system/` credential-pool refs (CODE-3 Targets)
- docs/operator-guide/configuration.md, installation.md, security.md, lenny-ctl.md, namespace-and-isolation.md, agent-operability.md, troubleshooting.md; docs/getting-started/architecture.md; docs/api/admin.md; docs/reference/metrics.md; docs/runbooks/credential-pool-exhaustion.md, credential-revocation.md, token-service-outage.md, etcd-key-rotation.md
- BUILD-GAPS.md
