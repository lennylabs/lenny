# Spec changes — T4 credential encryption at rest does not meet the KMS envelope requirement

## Design (as the spec must state it)

Credential pool secrets stay T4 in every deployment. The specification gains one paragraph that states the Encryption at rest control for this store, one definition that says which etcd configurations are KMS-backed, and one warning sentence that cites both.

- The normative home of the at-rest rule, its condition, the relation to **Enforcement.**, and the verification boundary is the §12.9 paragraph **Credential pool secrets at rest.** in SPEC-1.
- The normative home of the term KMS-backed is the §4.9 paragraph **KMS-backed configurations.** in SPEC-2.
- SPEC-3 cites those two paragraphs. Its check keeps its existing behaviour.

The §4.9 topology table, §4.9 rule 8, the §12.9 data-type mapping table, the **Enforcement.** paragraph, §15.1, and §18.14 keep their current text.

## Edge cases and accepted failure modes

Each case is decided by the SPEC-1 paragraph, and this list names where the text lands on it.

- A setting of `workspaceTier: T4` after install, on any path: the last sentence of the paragraph places the obligation on the operator. Lenny raises no signal at the transition.
- An environment or Runtime at T4 under a T3 tenant: the condition sentence covers it.
- A Secret stored before the provider changed: rewriting it is an operator obligation in the last sentence.
- A Secret created outside the gateway: the cluster-level control covers it, and the write-time rejection applies to no Secret write.

## Staged edits

### SPEC-1 · spec/12_storage-architecture.md § 12.9 Data Classification

**(a) T4 encryption cell.** In the **Controls driven by classification tier:** table, replace the row beginning `| Encryption at rest    |`. The row stays one physical line.

```markdown
| Encryption at rest    | Optional                | Required (storage-layer) | Required (storage-layer, SSE-KMS)                  | Required (envelope encryption via KMS, [Section 4.3](04_system-components.md#43-token-service); credential pool secrets: **Credential pool secrets at rest.** below)                |
```

**(b) New paragraph.** Insert after the paragraph beginning `**Enforcement.** Classification is enforced at the storage interface boundary` and before the paragraph beginning `**T2 storage-layer encryption definition.**`.

```markdown
**Credential pool secrets at rest.** Credential pool secrets are T4 in every deployment, and this paragraph governs only the Encryption at rest control for them. They live in Kubernetes Secrets, so their encryption at rest is the etcd encryption in [Credential Storage Topology](04_system-components.md#credential-storage-topology) **Encryption at rest — KMS key hierarchy.** rather than application-layer encryption. This control is cluster-level: one provider configuration covers every Secret on the cluster, and it gives pool secrets no per-tenant key isolation and no cryptographic erasure. A deployment in which any tenant, environment, or Runtime has `workspaceTier: T4` must use a configuration listed under [Credential Storage Topology](04_system-components.md#credential-storage-topology) **KMS-backed configurations.** Any other deployment meets this control with any configuration the topology table under **Encryption at rest — KMS key hierarchy.** accepts. That case is an exception to the T4 envelope requirement for this store. Embedded Mode stays exempt under [Section 17.9.7](17_deployment-topology.md#1797-backend-invariant-requirements). The write-time rejection in **Enforcement.** does not apply to a Kubernetes Secret write, because Secrets sit outside the [Section 12.6](#126-interface-design) store interfaces and no Lenny component can read the etcd encryption provider. The preflight Job verifies no part of the provider configuration and emits the [Section 17.6](17_deployment-topology.md#176-packaging-and-installation) warning in production mode, and the [Section 12.5](#125-artifact-store) KMS availability probe tests the tenant-scoped key only. Determining which requirement applies, selecting the provider, and rewriting existing Secrets are operator obligations, and the operator completes them before any path sets `workspaceTier: T4`, including the bootstrap seed and the admin API.
```

**(c) Credential encryption bullet.** Under **Interaction with existing controls.**, replace the bullet beginning `- **Credential encryption** ([Section 4.3]`.

```markdown
- **Credential encryption** by the Token Service ([Section 4.3](04_system-components.md#43-token-service)) satisfies T4 encryption requirements for the credentials it encrypts; classification formalizes and makes these requirements auditable. **Credential pool secrets at rest.** above governs pool secrets.
```

### SPEC-2 · spec/04_system-components.md § 4.9 Credential Storage Topology and Credential Lifecycle and Secret Management

**(a) New paragraph.** Under **Encryption at rest — KMS key hierarchy.**, insert after the topology table and before the sentence beginning `The preflight Job emits a non-blocking warning`.

```markdown
**KMS-backed configurations.** A KMS-backed configuration is one in which the kube-apiserver encrypts Secrets under data keys wrapped by a key held in an external KMS that the deployer controls. The KMS-backed configurations are EKS envelope encryption with a customer-managed AWS KMS key, GKE CMEK, AKS etcd encryption with Azure Key Vault, and the self-managed `kms` provider. The GKE Google-managed default, Azure Disk Encryption, `aescbc`, and `aesgcm` meet the table above and are not KMS-backed. [Section 12.9](12_storage-architecture.md#129-data-classification) **Credential pool secrets at rest.** states when a deployment must use one. The document the chart renders has no `kms` provider (it lists `aescbc` and `identity`), so a self-managed deployment under that requirement sets `etcdEncryption.enabled: false` and supplies its own `EncryptionConfiguration` and KMS plugin.
```

**(b) Production callout.** In the callout beginning `> **Production requirement — etcd encryption at rest for Secrets.**`, replace this text:

```markdown
offer envelope encryption via their respective KMS integrations (AWS KMS, GCP Cloud KMS, Azure Key Vault) and should be configured to use them.
```

with this text. In the same callout, replace `that the preflight Job may not have` with `that the preflight Job does not have`. The callout stays one physical line, and its other sentences are unchanged.

```markdown
offer envelope encryption via their respective KMS integrations (AWS KMS, GCP Cloud KMS, Azure Key Vault). [Section 12.9](12_storage-architecture.md#129-data-classification) **Credential pool secrets at rest.** states when a deployment must use a configuration listed under [Credential Storage Topology](#credential-storage-topology) **KMS-backed configurations.**
```

**(c) Preflight sentence.** In the sentence beginning `The preflight Job emits a non-blocking warning`, replace `when etcd Secret encryption cannot be verified` with `in production mode, because it cannot verify etcd Secret encryption`.

### SPEC-3 · spec/17_deployment-topology.md § 17.6 Packaging and Installation

In **Checks performed**, row `etcd Secret encryption (warning)`, replace the Failure Message cell. The Check and Validation cells are unchanged, the row stays one physical line, and the `\|` escape stays. CODE-1 carries the same text with `|` unescaped.

```markdown
`WARNING: Kubernetes does not encrypt Secrets in etcd by default. Credential pool API keys stored in Kubernetes Secrets (secretRef) are readable in plaintext from etcd snapshots or backups unless EncryptionConfiguration is enabled. Production deployments MUST configure EncryptionConfiguration with aescbc/aesgcm/kms for the 'secrets' resource type. Cloud-managed clusters: enable the provider encryption option Section 4.9 lists for the cluster type. Self-managed clusters: verify with 'etcdctl get /registry/secrets/<credentials namespace>/<name> \| hexdump'. Section 12.9 (Credential pool secrets at rest) states when a workspaceTier T4 setting requires a configuration listed in Section 4.9 (KMS-backed configurations).`
```

## Spec files touched

- `spec/04_system-components.md`
- `spec/12_storage-architecture.md`
- `spec/17_deployment-topology.md`
