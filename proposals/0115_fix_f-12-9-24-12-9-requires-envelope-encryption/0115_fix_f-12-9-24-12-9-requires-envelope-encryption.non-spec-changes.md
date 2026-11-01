# Non-spec changes — T4 credential encryption at rest does not meet the KMS envelope requirement

## Design (implementation-facing)

No behaviour changes in any Lenny component. CODE-1 changes one string constant, and `EtcdSecretEncryptionCheck.Decide` stays as it is. CHART-1 changes comments only, so the rendered output stays the same. DOCS-1 and DOCS-2 bring the operator pages into line with the §12.9 paragraph **Credential pool secrets at rest.** and the §4.9 paragraph **KMS-backed configurations.** Published pages carry no section numbers.

## Staged code changes

### CODE-1 · `pkg/preflight/install_prereqs.go` `EtcdSecretEncryptionWarning`

Replace the constant's value. The text is the SPEC-3 Failure Message with `\|` written as `|`.

```go
const EtcdSecretEncryptionWarning = "WARNING: Kubernetes does not encrypt Secrets in etcd by default. Credential pool API keys stored in Kubernetes Secrets (secretRef) are readable in plaintext from etcd snapshots or backups unless EncryptionConfiguration is enabled. Production deployments MUST configure EncryptionConfiguration with aescbc/aesgcm/kms for the 'secrets' resource type. Cloud-managed clusters: enable the provider encryption option Section 4.9 lists for the cluster type. Self-managed clusters: verify with 'etcdctl get /registry/secrets/<credentials namespace>/<name> | hexdump'. Section 12.9 (Credential pool secrets at rest) states when a workspaceTier T4 setting requires a configuration listed in Section 4.9 (KMS-backed configurations)."
```

Add `TestEtcdSecretEncryptionWarningCitesT4Requirement` to `pkg/preflight/install_prereqs_test.go`, beside `TestEtcdSecretEncryptionWarningNamesCredentialsNamespace`. The Testing section states what it asserts.

## Staged schema, chart, and migration changes

### CHART-1 · `charts/lenny/values.yaml` and `charts/lenny/templates/etcd-encryption.yaml` comments

Change comments only. Leave `enabled: true`, the rendered ConfigMap, and the embedded comment inside `encryption-config.yaml` as they are.

In `charts/lenny/values.yaml`, replace the comment above `etcdEncryption.enabled` with this text.

```yaml
  # enabled gates the lenny-etcd-encryption ConfigMap. Leave true for any
  # cluster that stores real credentials and uses this aescbc document.
  # Set false for a cloud-managed cluster where provider envelope
  # encryption (AWS KMS, GCP Cloud KMS, Azure Key Vault) covers the secrets
  # resource, or for a self-managed cluster that supplies its own
  # EncryptionConfiguration. This ConfigMap renders aescbc only, so a
  # deployment under the §12.9 KMS-backed requirement cannot use it. A
  # self-managed cluster moving off this document sets false last, after
  # the procedure in docs/runbooks/etcd-key-rotation.md, because the
  # upgrade removes the ConfigMap and any unpinned key it holds.
```

In the Helm comment at the top of `charts/lenny/templates/etcd-encryption.yaml`, insert this paragraph after the paragraph ending `treat this ConfigMap as documentation of intent.` and before the line `Key rotation is documented in docs/runbooks/etcd-key-rotation.md.`

```text
This document renders aescbc only. A self-managed deployment under the
§12.9 KMS-backed requirement skips the operator step above, supplies its
own EncryptionConfiguration and KMS plugin, and sets
etcdEncryption.enabled false as the last step of the Move to a KMS
provider section of docs/runbooks/etcd-key-rotation.md.
```

Neither comment restates the T4 condition, and neither adds a §13.1 citation.

## Staged docs changes

### DOCS-1 · Operator guide pages

**`docs/operator-guide/security-principles.md`, section 1.5.** Replace the paragraph with this text.

```markdown
OAuth refresh tokens, user-supplied API keys, and webhook callback secrets are encrypted at the application layer with KMS-envelope encryption. The database role with read access to the encrypted column cannot decrypt without a separate KMS permission. Workspace files, snapshots, and session transcripts in object storage use server-side encryption, and `workspaceTier: T4` uses SSE-KMS under a tenant-scoped key. Other Postgres data, including audit logs, relies on storage-level encryption at rest. Credential pool API keys live in Kubernetes Secrets and rely on etcd encryption at rest, a cluster-level control that must be KMS-backed when any tenant, environment, or Runtime uses `workspaceTier: T4`. See [Security — What Gets Encrypted](security#what-gets-encrypted).
```

Confirm against §4.9.1 and the `callbackSecret` storage text in §14 that the database-role sentence holds for all three items in the first sentence, and narrow it if it does not.

**IMPLEMENTOR'S CHOICE:** whether to retitle section 1.5. Any new title must stop implying that all sensitive data is KMS-envelope encrypted, and every inbound link to the section must still resolve.

**`docs/operator-guide/security-principles.md`, control primitives table, row `KMS envelope encryption`.** Replace the second cell with this text. The row stays one physical line.

```markdown
AWS KMS / GCP KMS / Azure Key Vault / HashiCorp Vault; application-layer encryption for Token Service credentials and webhook callback secrets; SSE-KMS for `workspaceTier: T4` object storage.
```

**`docs/operator-guide/security.md`, What Gets Encrypted, row `LLM provider API keys`.** Replace the second cell with this text.

```markdown
Kubernetes Secrets (etcd at-rest encryption; a KMS-backed configuration is required when any tenant, environment, or Runtime uses `workspaceTier: T4`)
```

**`docs/operator-guide/security.md`, Data Classification.** Append these sentences to the paragraph beginning `When a tenant sets `workspaceTier: T4``.

```markdown
Before setting `workspaceTier: T4` on a tenant, environment, or Runtime, configure a KMS-backed etcd encryption provider and rewrite existing Secrets, because Lenny cannot verify the provider. The KMS-backed options are EKS envelope encryption with a customer-managed AWS KMS key, GKE CMEK, AKS etcd encryption with Azure Key Vault, and the self-managed `kms` provider. See [etcd-key-rotation](../runbooks/etcd-key-rotation.md#move-to-a-kms-provider).
```

**`docs/operator-guide/installation.md`, preflight table, row `etcd encryption`.** Replace the second cell with this text.

```markdown
Non-blocking warning in production; preflight does not read the provider configuration.
```

### DOCS-2 · `docs/runbooks/etcd-key-rotation.md`

**Front matter.** Add this entry to `symptoms`.

```yaml
  - "a deployment is moving to workspaceTier T4 and its etcd Secret encryption is not KMS-backed"
```

**Introduction.** Replace the paragraph beginning `This runbook applies to self-managed clusters` with these two paragraphs.

```markdown
This runbook applies to self-managed clusters where the operator controls the apiserver flags. A deployment in which any tenant, environment, or Runtime has `workspaceTier: T4` uses a KMS-backed provider. The aescbc rotation procedure does not apply to that deployment, and its key rotates in the KMS.

On cloud-managed clusters the `secrets` resource is protected by the encryption option the cluster uses. The KMS-backed integrations are EKS envelope encryption with a customer-managed AWS KMS key, GKE CMEK, and AKS etcd encryption with Azure Key Vault; rotate those keys through the provider's KMS console or API. The Google-managed default on GKE and Azure Disk Encryption on AKS do not qualify when a deployment has a `workspaceTier: T4` setting. The Verification section reads the aescbc prefix and the chart ConfigMap, so it does not apply to these clusters. Verify a cloud-managed cluster with the provider's describe command, for example `aws eks describe-cluster --query 'cluster.encryptionConfig'` on EKS.
```

**Trigger.** Append this bullet.

```markdown
- A deployment is about to set `workspaceTier: T4` on a tenant, environment, or Runtime, and its etcd encryption is not KMS-backed. Follow [Move to a KMS provider](#move-to-a-kms-provider) instead of the rotation.
```

**New section.** Insert after the Verification section and before `## Escalation`. Use `###` headings and no `<!-- access: -->` markers, so `ParseSteps` keeps the section out of the rotation step list. Check the sample against the current upstream Kubernetes documentation before landing it.

````markdown
## Move to a KMS provider

This section moves a self-managed cluster from the chart's aescbc document to a `kms` provider. A deployment completes it before any tenant, environment, or Runtime is set to `workspaceTier: T4`. A cloud-managed cluster does not use this section, and enables the provider's KMS integration instead.

The chart document has no `kms` provider, so these stages edit the EncryptionConfiguration file on each control-plane node directly. The operator maintains that file from the first stage onward. Deploy the KMS plugin on every control-plane node before starting. After each stage that edits the file, propagate it to every control-plane node and restart every apiserver.

### Stage 1: add `kms` in second position

Add the `kms` provider after the existing `aescbc` provider and before `identity`. New writes stay under `aescbc`, and `kms` is present so that every apiserver can decrypt its data after the promotion in Stage 3.

```yaml
resources:
  - resources:
      - secrets
    providers:
      - aescbc:
          keys:
            - name: key1
              secret: <existing-key>
      - kms:
          apiVersion: v2
          name: <kms-provider-name>
          endpoint: unix:///var/run/kmsplugin/socket.sock
      - identity: {}
```

### Stage 2: wait until every apiserver holds `kms`

```bash
kubectl -n kube-system get pod -l component=kube-apiserver -o wide
```

All apiserver pods must be `Running` and `Ready` after the restart. A stale apiserver that lacks `kms` cannot decrypt data written by an apiserver that has already promoted it.

### Stage 3: move `kms` to first position

Reorder the providers to `kms`, `aescbc`, and `identity`. New writes now encrypt under `kms`, and `aescbc` remains for Secrets not yet rewritten.

### Stage 4: rewrite every Secret

Run the rewrite in Remediation Step 4.

### Stage 5: verify the stored prefix

Read a known Secret with the `etcdctl` command in Verification Step 1. The raw value must begin with `k8s:enc:kms:`. A value that begins with `k8s:enc:aescbc:` has not been rewritten, so rerun Stage 4 for that namespace.

### Stage 6: remove `aescbc`

Remove the `aescbc` provider, leaving `kms` and `identity`. Any Secret still encrypted under `aescbc` becomes unreadable once the provider is gone, which is why Stage 5 must pass first.

### Stage 7: set `etcdEncryption.enabled: false`

Set `etcdEncryption.enabled: false` in the chart values and run `helm upgrade`. The upgrade removes the `lenny-etcd-encryption` ConfigMap. The file on each control-plane node is now the only copy of the configuration.

etcd snapshots taken before Stage 4 stay under the old aescbc key.
````

## Testing

### TEST-1 · `tests/tier11_docs/etcd_kms_requirement_doc_test.go`

Add this file with the tier 11 tests the table below names. Each carries a `// spec:` annotation and a `// diagnosis:` comment.

| Test | Tier | Deliverable | `// spec:` | Assertion and path covered |
|:--|:--|:--|:--|:--|
| `TestEtcdSecretEncryptionWarningCitesT4Requirement` | 1 | CODE-1 | §17.6 "etcd Secret encryption (warning)"; §12.9 "Credential pool secrets at rest." | The constant contains `Cloud-managed clusters:`, `workspaceTier T4`, `Section 12.9 (Credential pool secrets at rest)`, and `Section 4.9 (KMS-backed configurations)`. `Cloud-managed clusters:` is the discriminating assertion for the non-T4 managed cluster, because deleting that sentence leaves those operators an instruction they cannot follow. |
| `TestUnverifiableEncryptionWarnings_spec_17_6`, `TestEtcdSecretEncryptionWarningNamesCredentialsNamespace`, and `TestRunEmitsUnverifiableEncryptionWarnings_spec_17_6` (existing) | 1 | CODE-1 | Their existing annotations | They pass unedited. They hold the production-only, always-passing behaviour, the empty and non-production environment values, and the credentials namespace path. |
| `TestEtcdKeyRotationRunbookMigrationStaysOutOfRotationSteps_spec_4_9` | 11 | TEST-1 | §4.9 "KMS-backed configurations." | `runbooks.ParseSteps` over the shipped runbook returns exactly the Diagnosis, Remediation, and Verification titles and no title beginning `Stage`. A migration stage carrying an access marker fails here. In the same test, the `## Move to a KMS provider` body places the `kms` promotion before the rewrite, the rewrite before the `aescbc` removal, and `etcdEncryption.enabled: false` after all three, which fails on a reordering that makes Secrets undecryptable. |
| `TestOperatorPagesScopeApplicationLayerEncryption_spec_12_9` | 11 | TEST-1 | §12.9 "Credential pool secrets at rest." | `security-principles.md` no longer contains `Every piece of data classified as T3 or T4` or `application-layer crypto for T3/T4 data`, and `security.md` states the requirement at both DOCS-1 sites. An empty read of either page fails the test rather than passing it. |
| Existing chart tests | 0, 11 | CHART-1 | Their existing annotations | `charts/lenny/tests/etcd-encryption_test.yaml` and the embedded-manifests sync check pass unedited. Compare `helm template` output before and after the change with `etcdEncryption.aescbcKeys` pinned, because `lookup` returns empty offline and an unpinned key differs on each render. The comparison must be empty. `tests/tier5_e2e_kind/etcd_encryption_test.go` needs no run, because nothing rendered changes. |

## Edge cases and accepted failure modes

- `helm upgrade` with `enabled: false` removes the ConfigMap, and that is the only in-cluster copy of an unpinned key. A later re-enable generates a different key. DOCS-2 Stage 7 runs last for this reason.
- Verification Steps 1 and 3 in the runbook assert the aescbc prefix and read the ConfigMap, so they do not apply after a move to `kms`. DOCS-2 keeps them for the rotation and routes the migration through Stage 5.

## Files touched on application (non-spec)

- `pkg/preflight/install_prereqs.go`
- `pkg/preflight/install_prereqs_test.go`
- `charts/lenny/values.yaml`
- `charts/lenny/templates/etcd-encryption.yaml`
- `docs/operator-guide/security-principles.md`
- `docs/operator-guide/security.md`
- `docs/operator-guide/installation.md`
- `docs/runbooks/etcd-key-rotation.md`
- `tests/tier11_docs/etcd_kms_requirement_doc_test.go`
