# Problem: Credential Secrets in etcd do not meet the §12.9 T4 KMS envelope control

## Statement

F-12.9.24: §12.9 maps credential pool secrets to T4 by data type and stores them in Kubernetes Secrets in the credentials namespace. §12.9 requires envelope encryption via KMS for T4 data at rest and states that credential encryption (§4.3, §4.9) already satisfies T4 encryption requirements. That statement holds for the Postgres `TokenStore` path, which uses KMS-backed envelope encryption. It does not hold for pool Secrets, whose only at-rest protection is the etcd layer in §4.9 **Encryption at rest — KMS key hierarchy.** (§4.9 rule 8 places values submitted through the gateway on the same layer.)

That §4.9 table accepts `aescbc` or `aesgcm` on self-managed clusters, the GKE Google-managed default, and Azure Disk Encryption on AKS. The acceptance also appears in the §4.9 **Production requirement — etcd encryption at rest for Secrets.** callout, the §17.6 etcd row, the `EtcdSecretEncryptionWarning` constant, and the key-rotation runbook. The chart default `etcdEncryption.enabled: true` renders an EncryptionConfiguration with `aescbc` and `identity` providers only, so a self-managed deployment that follows the chart reaches the non-KMS configuration by default. No chart value selects a `kms` provider.

The owner decision recorded on 2026-11-01 requires the `kms` provider for credential Secrets only when the deployment has a T4 tenant, and requires a statement of which part preflight verifies and which part is an operator obligation. That condition does not match §12.9: pool secrets are T4 in every deployment, and `workspaceTier: T4` elevates workspace data only. The proposal must therefore change the classification, the control, or the "already satisfies" sentence so that deployments without a T4 tenant are also consistent, and it must state how the §12.9 **Enforcement.** write-time rejection relates to Secret writes.

Lenny cannot observe the etcd provider. The preflight Job has no etcd access and emits a production-only warning that always passes. The T4 signal that the preflight flag `--object-storage-serves-t4-tenant` carries has no chart wiring and defaults to false. A tenant can become T4 through the post-install bootstrap seed or the admin API after install, and both paths probe only the tenant-scoped KMS key. The verification boundary is part of this problem. The Token Service path stays out of scope.

## Evidence

All citations were verified against the tree at `edee08bdf`. Line numbers are evidence of what the tree said at that commit.

- §12.9 default data-type mappings map credential pool secrets to T4 in Kubernetes Secrets (credentials namespace). (verified: spec/12_storage-architecture.md:1075)
- §12.9 requires "envelope encryption via KMS" for T4 at rest, and the cell links §4.3 only. (verified: spec/12_storage-architecture.md:1092)
- §12.9 states that credential encryption (§4.3, 4.9) already satisfies T4 encryption requirements. (verified: spec/12_storage-architecture.md:1109)
- §12.9 **Enforcement.** states that writing T4 data to a store not configured for envelope encryption is rejected with `CLASSIFICATION_CONTROL_VIOLATION`. (verified: spec/12_storage-architecture.md:1101)
- `workspaceTier: T4` applies to workspace files, snapshots, and session transcripts. (verified: spec/12_storage-architecture.md:1083 and the paragraph after the YAML block)
- The §4.9 table accepts the GKE default, Azure Disk Encryption, and self-managed `aescbc` or `aesgcm`. Only the EKS row names a KMS alone. (verified: spec/04_system-components.md:1682-1685)
- §4.9 rule 8 places gateway-written values on the etcd layer and records that the `TokenStore` holds none of them. (verified: spec/04_system-components.md:1742)
- The §4.9 production callout repeats `aescbc`, `aesgcm`, or `kms`. (verified: spec/04_system-components.md:2231)
- §4.9.1 calls the Token Service envelope key distinct from etcd Secret encryption. (verified: spec/04_system-components.md:2262)
- The §17.6 row and its Go constant repeat the provider list. The check is a production-only warning that always passes. (verified: spec/17_deployment-topology.md:556; pkg/preflight/install_prereqs.go:127, 135-152)
- The chart renders `aescbc` plus `identity` for the `secrets` resource, with the key in the `lenny-etcd-encryption` ConfigMap. The default is on and is a §18.14 deliverable. (verified: charts/lenny/templates/etcd-encryption.yaml; charts/lenny/values.yaml:3923-3947; spec/18_build-sequence.md:288)
- Proposal 0112 (Implemented) moved credential Secrets to a dedicated namespace, and its SPEC-9 (b) changed only the Store cell. (verified: spec/04_system-components.md, **Credentials namespace.**; proposals/0112_fix_credential-secrets-in-a-dedicated-namespace-addable-by-secre/0112_fix_credential-secrets-in-a-dedicated-namespace-addable-by-secre.summary.md:112)
- The etcd warning check landed under F-17.6.25, and 0112 edited only its text. (verified: commits 450ef8a47 and e3901c28c)
- `--object-storage-serves-t4-tenant` defaults to false, and nothing under charts/ or tests/testinfra renders it. (verified: cmd/lenny-preflight/main.go:625-626; grep of both trees returned no hits)
- The `workspaceTier: T4` transition probes the tenant-scoped KMS key only. (verified: pkg/gateway/externalapi/admin/tenants.go:1450-1459)
- `CheckSecretEncryption` reads a boolean operator attestation. It gates capacity Tiers 2 and 3 and never inspects the provider. (verified: pkg/tierpromotion/checks.go:142-176; pkg/tierpromotion/gather.go:84-86)
- The gateway credential Secret writer and erasure code contain no classification, T4, envelope, or KMS reference. (verified: case-insensitive grep of pkg/gateway/externalapi/admin/credential_secret_writer.go and credential_secret_erasure.go returned no lines)

Dropped from the original evidence: "§6.4 and §17 define the T4 dedicated-node requirements". §6.4 defines those requirements, §17 only refers to them, and they govern agent pod placement rather than etcd encryption.

## Who observes it

Nothing fails at runtime, so no end user or tenant sees the defect, and no test or alert reports it. The observers are a deployer or auditor who maps §12.9 or the operator compliance tables to the deployed configuration, and a security reviewer after an etcd backup exposure.

The operator documentation disagrees with itself. docs/operator-guide/security-principles.md:65 says all T3 and T4 data, including credential ciphertext, is encrypted at the application layer with KMS-envelope encryption. docs/operator-guide/security.md:164 says LLM provider API keys sit in Kubernetes Secrets under etcd at-rest encryption. An operator who relies on the first page makes a false attestation.

Every production deployment holds the condition under §12.9, whether or not it has a T4 tenant, because the T4 mapping of pool secrets is unconditional.

## What breaks if nothing changes

- Deployments that follow §4.9, or the chart default on a self-managed cluster, store T4-classified pool API keys without KMS envelope encryption while §12.9 states that they meet the control.
- The chart stores the `aescbc` key in clear in a ConfigMap, and its EncryptionConfiguration lists `secrets` only. On that path an etcd snapshot carries the ciphertext and its key, which is the exposure §4.9 says etcd encryption prevents. This is an inference from the rendered template and has not been tested.
- The preflight warning tells operators that `aescbc` is acceptable, so the condition stays silent.
- The §12.9 **Enforcement.** rejection stays false for Secret writes.

Severity stays at the filed Medium. The platform is pre-deployment, exploitation requires etcd or backup access, and the §4.3 path is compliant. The defect remains because the spec and chart default are the contract deployers follow.

## Findings this unblocks

This proposal closes F-12.9.24.

No other entry in BUILD-GAPS.md, TEST-GAPS.md, or the remediation plan references F-12.9.24 as a dependency. F-12.9.24 is its only entry in BUILD-GAPS.md, and the plan carries the owner decision and the queue item.

## Prior art considered

- No landed or draft proposal resolves the conflict. Proposal 0112 lists it as an unstaged defect and changes only the §12.9 Store cell.
- F-12.9.22 (closed) appears to answer this finding, but its evidence is the Postgres credential and connector stores. It does not cover pool Secrets in etcd, and the proposal should not rely on it.
- `CLASSIFICATION_CONTROL_VIOLATION` and the tier guards apply to blob and artifact stores and tenant-scoped KMS keys. They have no implementation for Secrets.
- The §17.6 warning (F-17.6.25) and `CheckSecretEncryption` are reusable surfaces and neither closes the gap. T-17.6.13 covers only the warning. The check's failure text names GKE CMEK and AKS Key Vault, so it is already narrower than the §4.9 table.
- The §17.6 checkpoint T4 default-encryption gate is a precedent for a check conditioned on `ServesT4Tenant` and declared configuration, and the gateway startup assertion in cmd/lenny-gateway/t4_encryption.go enumerates T4 tenants at runtime. `preflight.attestVolumeEncryption` in §12.9 is a precedent for operator attestation.
- The §6.4 T4 workspace limitation is a precedent for stating a bounded limit of a T4 control in the spec.
- BUILD-GAPS offered two options: require `kms`, or state in §12.9 which §4.9 configurations meet the T4 control. The owner decision selected a conditional `kms` requirement.

## Validated premises

### Premise lens: revise

- Confirmed: §12.9 requires envelope encryption via KMS for T4 at rest.
- Confirmed with refinement: the "already satisfies" sentence names §4.3 and §4.9 and does not name the etcd configuration. It holds for the `TokenStore` path and fails for pool Secrets, so the defect is one sentence over-claiming across two storage paths.
- Confirmed: pool secrets are T4 and live in Kubernetes Secrets. 0112 corrected the Store cell, which exposed the conflict.
- Confirmed and widened: the AKS row has the same gap, and the §4.9 table conflicts with the later callout that says managed clusters should use their KMS integrations.
- Confirmed and widened: the chart default produces the non-KMS configuration, and no chart path renders `kms`.
- Refuted: the implicit premise that the T4 obligation is conditional on a tenant. The default mapping is unconditional, and `workspaceTier: T4` elevates workspace data only. Keep this refutation in view: a later stage that reads only the owner decision will re-derive the conditional reading and leave pool Secrets in conflict on deployments without a T4 tenant.
- Refuted: the implicit premise that Lenny can detect or enforce the provider. Preflight has no etcd access, T4 transitions occur after install, and the preflight T4 flag has no chart wiring.
- Added: the §12.9 **Enforcement.** clause is also unmet for Secrets.
- Unrelated: the dedicated-node material gives this problem no support.

### Evidence lens: revise

- Verified: the §12.9 control, the T4 mapping, the §4.9 table, 0112's status and its limited §12.9 edit.
- Drift corrected: "the §4.9 configuration already provides" is an inference from a sentence that names §4.3 and §4.9.
- Drift corrected: "Its install prerequisite check" misattributes the check to 0112. The check landed under F-17.6.25.
- Drift corrected: §6.4 defines the dedicated-node requirement, §17 refers to it, and neither bears on etcd encryption.
- Omissions added: the AKS row, the §4.9 callout, the §17.6 row and its constant, and the chart default.
- Side finding, confirmed: the chart comments and values cite §13.1 for the etcd posture, and spec/13_security-model.md contains no occurrence of "etcd". The proposal should not repeat that citation.
- Resolved: this lens left the writer's lack of a classification check unconfirmed. Direct grep confirms the absence.

### Prior-art lens: stands

- Confirmed: no proposal resolves the conflict, and the contradiction is live.
- Confirmed: F-12.9.22 is misleading prior art.
- Confirmed: the enforcement mechanism does not reach this store, and the preflight and promotion checks do not close the gap.
- Confirmed: conditional-gate and attestation precedents exist.
- Confirmed: the owner decision and the §12.9 mapping are in tension, which the proposal must resolve.

### Scope lens: revise

- Confirmed: this stays one proposal, because spec, chart, and preflight are carriers of one control over one store. Splitting by topology leaves the §4.9 table half fixed.
- Confirmed: the verification half belongs in this problem.
- Confirmed: scope covers etcd Secrets only, and the title now says so.
- Recorded as candidates to stay out of scope, each to be filed if the proposal does not absorb it: the wiring of `--object-storage-serves-t4-tenant` for the checkpoint check, and T4 workspace data on node disk. The Enforcement clause needs a statement of how it applies to Secrets rather than a new mechanism, because the gateway cannot read the provider.
- Disputed: this lens keeps the `aescbc` key in a ConfigMap out of scope, and the alternatives lens asks that it be filed or scoped explicitly. Both are compatible, and the outcome is an open decision below.

### Impact lens: revise

- Confirmed: the affected population is every production deployment.
- Confirmed: the non-KMS path is the chart default.
- Confirmed as inference: key and ciphertext sit in the same etcd on the chart path.
- Refuted in part: this lens exempted an operator who supplies keys out of band. The template renders `etcdEncryption.aescbcKeys` entries into the same ConfigMap, so supplying keys leaves the key in the ConfigMap. The exemption applies only when the operator sets `enabled: false`.
- Confirmed: no detector exists, and the operator documentation disagrees with itself.
- Confirmed: the mitigating factors bound severity at Medium and do not remove the defect.

### Alternatives lens: revise

- Confirmed: no reading makes this a no-change case.
- Confirmed: "T4 data" and "T4 tenant" are different predicates, so the fix must state which one governs etcd-held credentials.
- Confirmed from the rendered document, with the Kubernetes behaviour inferred: the provider is selected by resource, so `kms` applies to all Secrets on the cluster. It is a single cluster key and gives no per-tenant isolation or cryptographic erasure for pool credentials. §12.9 wording should describe the control as cluster-level.
- Corrected: this lens said first install has no tenants. The `lenny-bootstrap` Job can seed a tenant with `workspaceTier: T4` from Helm values, and it probes the tenant-scoped key (pkg/gateway/externalapi/admin/bootstrap.go, `bootstrapT4KMSProbe`). The Job is a post-install hook, so tenant rows do not exist when preflight runs, although the seed values are declared before install. The T4 condition must be evaluated from declared values, at startup, at bootstrap, or at the `PUT /v1/admin/tenants/{id}` transition.
- Disputed: this lens says the minimum fix needs no chart `kms` rendering, because apiserver wiring is an operator step already. The premise, scope, and evidence lenses say requiring `kms` must reach the chart. The repository confirms that the chart default and runbook conflict with a T4 `kms` requirement, and does not determine which remedy applies. The proposal decides this.

## Open decisions for the proposal

- How §12.9 stays consistent on deployments without a T4 tenant: amend the pool-secret mapping, the control, or the "already satisfies" sentence.
- Whether "envelope encryption via KMS" includes the GKE Google-managed default and which §4.9 options meet it. §12.9 does not define the term for etcd.
- Where the T4 condition is evaluated, given that preflight runs before the bootstrap seed and that tenants can transition later.
- Whether the chart renders a `kms` option, or documents operator-supplied configuration and changes the default text.
- Whether the cleartext `aescbc` key in the ConfigMap is handled here or filed as a separate finding.
