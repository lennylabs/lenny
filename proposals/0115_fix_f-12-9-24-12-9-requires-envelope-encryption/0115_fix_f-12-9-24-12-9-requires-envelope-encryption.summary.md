# Summary: Credential pool Secrets at rest: state the §12.9 control as a cluster-level etcd control, KMS-backed when any workspaceTier is T4, with the verification boundary stated

## Summary

**Problem statement.** §12.9 classifies credential pool secrets as T4 in every deployment, requires KMS envelope encryption for T4 data at rest, and states that credential encryption already meets that requirement. The statement holds for data the Token Service encrypts. It fails for pool secrets, which live in Kubernetes Secrets and whose only at-rest protection is etcd encryption. §4.9 accepts non-KMS configurations for that layer, the chart renders `aescbc` by default, the preflight warning tells operators `aescbc` is acceptable, and the §12.9 write-time rejection has no implementation for Secrets. One operator page claims application-layer KMS encryption for this data, so an operator relying on it makes a false attestation. The owner requires the `kms` provider only for T4 deployments and requires a statement of what preflight verifies and what the operator owes.

**What changes.**

- §12.9 gains one paragraph that defines the at-rest control for pool secrets, a pointer in the T4 cell, and a narrowed credential bullet (SPEC-1).
- §4.9 gains a definition of KMS-backed configurations, its preflight wording stops implying a verifiable case, and its production callout points at §12.9 (SPEC-2).
- The §17.6 warning row and its Go constant cite the T4 requirement (SPEC-3, CODE-1).
- The chart comments stop forbidding the self-managed T4 path, and the rendered output stays the same (CHART-1).
- The operator guide stops making the false attestation, and the etcd key-rotation runbook gains a provider migration (DOCS-1, DOCS-2).

**Decisions.**

1. **Mechanism.** The change is spec text, one warning constant, comments, and docs. It adds no RPC, field, Helm value, error code, state, metric, or check, because Lenny cannot observe the provider.
2. **Classification.** Credential pool secrets stay T4 in every deployment, and every other T4 row stays as written. "T4 data" and "T4 setting" are separate predicates, so the new paragraph states the no-T4 case as an explicit exception to the T4 cell. This keeps the owner's condition without the refuted premise that the T4 obligation depends on a tenant.
3. **Predicate.** The condition reads "any tenant, environment, or Runtime has `workspaceTier: T4`". This is wider than the owner's "T4 tenant" because an environment can tighten to T4 under a T3 tenant (pkg/gateway/externalapi/admin/environments.go:46-48) and a Runtime carries its own tier (pkg/gateway/externalapi/admin/runtimes.go:58, 142). Each path produces T4 workspace data without a T4 tenant row, and the widening costs no mechanism.
4. **Scope of the requirement.** A T4 setting is the only condition, as the owner set it, so the requirement does not depend on production mode. A production conjunct would exempt a deployment holding T4 data on the chart default `environment: "dev"`. Embedded Mode keeps its §17.9.7 exemption, because its rendered `EncryptionConfiguration` is inert and the §17.4 banner discloses insecure credentials.
5. **Definition of KMS-backed.** It is stated once in §4.9, beside the options. The table rows stay as the production baseline, so rule 8 stays true unedited. The bold label exists because Credential Storage Topology is long and `spec-citations.md` requires a label to cite there.
6. **Where the condition is evaluated.** The operator evaluates it on every path that sets the tier, in the form §12.5 item 1 already uses. The §17.6 warning stays production-only, non-blocking, and unconditional on tier, so it needs no T4 signal, and outside production mode Lenny emits no signal for this control.
7. **Verification boundary.** Preflight verifies no part of the provider configuration. Selecting the provider, rewriting Secrets, evaluating the condition, and acting before a T4 setting are operator obligations. This is the statement the owner asked for, and §12.9 says the §12.5 probe covers the tenant-scoped key only.
8. **Enforcement.** The new paragraph, placed directly after **Enforcement.**, narrows the rejection, so that paragraph and §15.1 stay unedited. The §15.1 `tier_store_mismatch` clause concerns configured stores at the interface boundary.
9. **No enforcement gate.** The attestation gate is declined. Lenny cannot enforce the provider, a boolean proves nothing, and the owner asked for a stated boundary. No existing fail-closed check is removed, because none exists for this control.
10. **Chart.** It renders what it renders today. A chart cannot activate a provider, deploy a KMS plugin, or affect a managed cluster, so `etcdEncryption.enabled: false` plus operator-supplied configuration is the T4 path. The `enabled: true` default and §18.14 stay. The comments in values.yaml and the template header forbid or omit that path, so they change.
11. **ConfigMap key.** The cleartext `aescbc` key is filed as a separate Medium finding. On the T4 path the ConfigMap does not render, and on the baseline path the defect exists independently of this proposal. No staged spec sentence claims snapshot protection or key custody for the chart path.
12. **Cell pointer.** The T4 cell gets a pointer clause, because an auditor mapping that table is the observer the problem names, and the cell alone reads as a universal KMS requirement.
13. **Credential bullet.** It names Token Service credential encryption rather than the `TokenStore` alone and is scoped to "the credentials it encrypts", so the Credential leases row is not orphaned. The wording does not depend on how §12.4 **Security:** is read.
14. **Callout.** The bare "should" competes with the conditional requirement, so it is replaced with a citation of §12.9. The callout's "must enable the Kubernetes `EncryptionConfiguration` resource" sentence stays out of scope and is filed.
15. **Warning.** A cloud-managed sentence stays and points at §4.9, because deleting it would leave a non-T4 managed operator with only an `EncryptionConfiguration` instruction that a managed cluster cannot follow. The check has no provider input, and its only input is Environment. `--object-storage-serves-t4-tenant` is an object-storage flag the charts do not pass, and this check does not read it.
16. **Runbook.** The operator is sent through a provider migration, and removing `aescbc` before the rewrite leaves Secrets undecryptable, so the runbook gains one section with the staged ordering the rotation already uses. The survival table of failure scenarios is declined.
17. **Operator pages.** Section 1.5 and the control primitives row both carry the false claim, so both change, and each names only what is confirmed as application-layer. The prerequisite also appears at the `workspaceTier: T4` text in `security.md`, because Lenny has no signal at the transition itself.

**Watch out for.**

- The §12.9 T4 mapping row, the **Enforcement.** paragraph, §15.1, §4.9 rule 8, and the §4.9 table rows look like candidates for editing and stay as they are. That includes the `kubectl apply` in the self-managed row, which is filed.
- A later reader of the owner decision alone re-derives the reading that T4 is conditional on a tenant. Decision 2 closes that reading.
- SPEC-3 and CODE-1 must match byte for byte, apart from the `\|` escape, and no gate compares them. Update both from the staged text. Their table row and the §4.9 callout must each stay one physical line.
- The DOCS-2 stages are ordered. `aescbc` leaves only after the prefix check passes, and `etcdEncryption.enabled: false` comes last because the upgrade removes the ConfigMap. `ParseSteps` has no `## ` case, so an access marker in the new section joins the rotation step list. The section uses `###` headings with no markers.
- A render comparison on an unpinned key reports a false difference, because `lookup` returns empty offline.
- The same comments carry stale §13.1 citations. They stay, they are filed, and new text must not repeat them.
- The credential bullet's scope leans on the §12.9 Redis cell of the Credential leases row, and the lease store is itself a filed multi-way conflict. Do not cite that cell as settled.

## Goals

- §12.9 and §4.9 state one consistent at-rest control for credential pool secrets, in deployments with and without a T4 setting.
- The specification defines which etcd configurations are KMS-backed.
- The owner's condition holds: a deployment with a T4 setting uses a KMS-backed configuration.
- The specification states what preflight verifies and what the operator owes, and how the §12.9 **Enforcement.** rejection relates to a Secret write.
- The warning, chart comments, operator guide, and runbook agree with that text, and a self-managed operator has a working migration path.

## Non-goals

- Gateway attestation gate (`etcdEncryption.attestKmsProvider`, a gateway flag, `details.reason: etcd_kms_not_attested`, refusal at POST, PUT, seed, and environment writes). Decision 9 declines it. It needs a value, a flag, a reason, and several call sites. If a later owner wants a hard stop, it is additive to this text and only the verification sentence changes.
- Gateway startup refusal when a T4 tenant or environment exists without the attestation. Decision 9 declines it with the gate. It also adds a fatal Postgres listing and a full-outage path for a claim nobody can verify.
- A failure survival table (HA skew, KMS outage, key loss, etcd restore, attestation drift) as spec content. The existing §4.9 **Token Service secret resolution.** rule 3 path covers a read failure, and the other entries depend on a gate this design does not add. The runbook carries the ordering a migration needs.
- Deleting the chart's EncryptionConfiguration template and the `etcdEncryption` values. It retires the §18.14 deliverable and reworks the chart tests, Kind test, runbook, presets, and embedded manifests, which the validated problem does not need. It stays a candidate remedy in the filed finding.
- Changing the rendered kind from ConfigMap to Secret. The validated problem allows filing, the change spans the template, `lookup`, chart tests, Kind extractor, runbook, and manifests, and it applies to the baseline independently of the KMS control. It stays a candidate remedy in the filed finding.
- A spec sentence requiring that an `aescbc` key is held outside etcd. It would make the chart default non-conformant at landing without the fix that was declined.
- A two-column §4.9 table or a new "KMS provider" column. One paragraph carries the same information with no edit to the rows.
- Reclassifying pool secrets to T3 or a new tier, or a conditional mapping. It would drop T4 access, audit, retention, and residency controls, the T3 cell still requires SSE-KMS, and it rests on the refuted premise.
- Requiring `kms` in every production deployment. It is the simplest rule and it reverses the recorded owner decision.
- Stating only that the §4.9 options meet T4 (BUILD-GAPS option 2). This is false for `aescbc` and omits the owner's requirement.
- Deleting or rewording only the "already satisfies" sentence. The T4 cell stays false and the owner's requirement is absent.
- Reading the owner's condition literally with no exception statement. Deployments without a T4 setting would hold T4 data in breach of the cell, which is the refuted premise.
- Keeping the predicate at "T4 tenant". An environment or Runtime reaches T4 with a T3 tenant, so that reading leaves a gap, and widening costs nothing here.
- Implementing `CLASSIFICATION_CONTROL_VIOLATION` on the credential Secret writer. The writer has no provider input, and operator-created Secrets never pass through it.
- Appending a carve-out sentence to the **Enforcement.** paragraph itself. The new paragraph sits directly after it, and a second statement of the rule adds drift.
- Editing §15.1 for the etcd case. No new reason or emission path exists.
- Extending or rewording `CheckSecretEncryption` and its comments. It gates capacity tiers, a Tier 1 deployment never reaches it, and its text is filed.
- Extending `T4DefaultEncryptionCheck`, `assertT4DefaultEncryption`, or `VolumeEncryptionCheck`, or adding a preflight attestation. Preflight runs before tenants exist and a boolean verifies nothing, and `assertT4DefaultEncryption` exits early on backends other than gcs and azure.
- Wiring `--object-storage-serves-t4-tenant` for this check. The warning is unconditional, and the flag would go stale after a post-install transition.
- Making the warning tier-conditional or fail-closed. The §17.6 row is non-blocking and preflight has no input that could justify failing.
- Adding a chart `kms` provider option (`etcdEncryption.provider`, plugin endpoint). The chart cannot deploy the plugin or wire the apiserver, managed clusters ignore the document, and `enabled: false` reaches the same result.
- A chart render `fail` when seed values declare T4 with `enabled: true`. It covers one path and misfires on a cluster whose provider is KMS-backed.
- Detecting the provider through etcd reads or apiserver surfaces. Preflight has no etcd access, and managed clusters do not expose these uniformly.
- Binding an attestation to a cluster identity or KMS key ID, a new error code, or a new alert for etcd KMS outage. Each serves a gate this design does not add, and no Lenny signal exists for the outage.
- Per-tenant etcd keys, application-layer encryption of pool values, or moving pool values into the `TokenStore`. §4.9 places them in Kubernetes Secrets by design, 0112 landed that, and external-secrets flows would break.
- Relying on F-12.9.22 as prior resolution. Its evidence is the Postgres stores.
- Reviewing whether other T4 rows (residency, access-attempt audit) fit Kubernetes Secrets. The paragraph is scoped to encryption at rest.
- Fixing the defects listed under `## Defects in the shipped tree that this proposal does not stage`, including the §13.1 citations, `kubectl apply` in the §4.9 row, and the POST probe gap. Each is outside one control over one store.
- Changing §18.14, TESTING.md §13.11, or the Kind test. The chart default and its test stand.

## Open decisions for human to make

1. **Spec annotation of the warning-text test.** `TestEtcdSecretEncryptionWarningCitesT4Requirement` is the new unit test that pins the §17.6 preflight warning text. Its `// spec:` annotation names the sections the test verifies, and the harness maps tests to sections through that annotation. The staged annotation names §17.6 and §12.9, while the test also asserts that the warning cites §4.9 **KMS-backed configurations.** Should the annotation also name §4.9? The spec loop left the annotation unchanged and routed this decision to the non-spec loop. It derived no recommendation, so the open-decisions-and-impact-review phase supplies one.

## Defects in the shipped tree that this proposal does not stage

The defects below are filed into BUILD-GAPS.md and gateway-runtime-comms-remediation.md §10.2 after approval. `implement-proposal` closes F-12.9.24.

1. **Medium: cleartext `aescbc` key in the chart ConfigMap.** The chart stores the `aescbc` key in cleartext in the `lenny-etcd-encryption` ConfigMap, including `aescbcKeys` entries, and the EncryptionConfiguration lists `secrets` only, so an etcd snapshot on the chart path carries ciphertext and key (inference, untested). `lookup` also returns empty under `helm template`, so an unpinned key changes on each offline render (charts/lenny/presets/dev.yaml, charts/lenny/templates/kms-master-key-secret.yaml header). Candidate remedies are rendering the document as a Secret, which is rewritten under its own key after wiring, is readable by the lenny-ops Role (charts/lenny/templates/ops-rbac.yaml:63-65), and is plaintext until wired; or ceasing to render the document, which retires the §18.14 default and needs TESTING.md §13.11 and the Kind test to use their own fixture. It stays out because it is a defect of the baseline and does not reach the KMS-backed control.
2. **`--object-storage-serves-t4-tenant` wiring.** The flag has no chart wiring for the checkpoint gate (cmd/lenny-preflight/main.go:625-626). It stays out because it belongs to object storage.
3. **Info: T4 cell and node disk.** The §12.9 T4 encryption cell reads as universal and carries no pointer to the §6.4 **T4 workspace limitation:** exception, which is the same defect SPEC-1 fixes for pool secrets. It stays out because it concerns workspace data on node disk.
4. **Stale etcd citations.** Closed F-13.1.19 records §4.9 and §17.6 as the home of etcd encryption, and that is the correction target. These sites still cite §13.1 or Section 13, and spec/13_security-model.md has no such content: charts/lenny/values.yaml:3923, charts/lenny/templates/etcd-encryption.yaml:2, charts/lenny/presets/dev.yaml:233 and 244, docs/runbooks/etcd-key-rotation.md:27, spec/18_build-sequence.md §18.14, and docs/tutorials/deploy-to-cluster.md:547. The template header also cites §17.9 for a warning whose row is in §17.6. It stays out because it is a citation sweep across unrelated carriers.
5. **`CheckSecretEncryption` failure text.** The text is narrower than the §4.9 table (pkg/tierpromotion/checks.go:162-167). It stays out because the check gates capacity tiers.
6. **§4.9 re-encryption command.** The §4.9 self-managed row re-encrypts with `kubectl apply`, while the template and runbook use `kubectl replace`. It stays out because the table rows are otherwise unedited.
7. **T4 on tenant create.** §12.5 item 1 names only `PUT /v1/admin/tenants/{id}`, and `POST /v1/admin/tenants` accepts `workspaceTier: T4` and runs no §12.5 KMS probe (pkg/gateway/externalapi/admin/tenants.go:1090-1135; `kmsProbe` calls sit only at tenants.go:1455 and bootstrap.go:524). F-12.9.3 closed the bootstrap path only. The fix needs a spec decision, so it routes through a proposal.
8. **Spec conflict over the T4 key scope.** §12.5 selects the key from the tenant's `workspaceTier`, while §12.9 allows an environment to tighten to T4 and §6.4 gives a Runtime its own tier. The code matches §12.5 (`newSSEKeyResolver` reads the tenant row only, cmd/lenny-gateway/t4_encryption.go:127-138). It needs a proposal to decide key scope.
9. **`attestVolumeEncryption` comment.** The values comment says the install fails closed (charts/lenny/values.yaml:3987-3993). An unattested cannot-perform install passes with a WARNING (pkg/preflight/volume_encryption.go:132-137), so the comment is false. It stays out because it concerns the T2 volume check.
10. **Multi-way conflict over lease storage.** §18.40 item 6 says Kubernetes Secrets, §12.9 says Redis, and §18.15 and the §12.1 `CredentialPoolStore` row say Postgres, where migrations/0038_credential_leases.up.sql creates the table. This proposal does not establish which is correct.
11. **The `EncryptionConfiguration` MUST.** The §4.9 callout's "must enable the Kubernetes `EncryptionConfiguration` resource" sentence and the same MUST in the §17.6 warning conflict with cloud-managed clusters, which cannot take a custom EncryptionConfiguration, and with the GKE default row. SPEC-3 leaves that MUST in place because resolving it rewrites the baseline rather than the T4 control.
12. **§12.9 T3/T4 sentence.** **T2 storage-layer encryption definition.** says "application-layer encryption required for T3/T4 data", contrary to the same section's table. It stays out because it concerns every T3 and T4 store.
13. **`callbackSecret` wording.** spec/14_workspace-plan-schema.md says `callbackSecret` uses "the same KMS backend as credential pool secrets", which over-claims for pool secrets. It stays out because it is a §14 sentence about webhook storage.
14. **Other T4 controls on Secrets.** Whether the remaining T4 controls (access-attempt audit, residency, and Immediate erasure) hold for Kubernetes Secrets is unreviewed, because this proposal makes no claim about them.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0112 | Implemented | Resolves the conflict that 0112 listed as an unstaged defect. It reverts nothing. | Nothing. The record stands. |
| 0113 | Draft | Both proposals edit `docs/operator-guide/security-principles.md` and `security.md`, in different paragraphs. | Rebase when the second of the two lands. |
| 0084 | Draft | Both proposals edit `docs/operator-guide/security-principles.md`, in different paragraphs. | Rebase when the second of the two lands. |

## Deliverable index

- SPEC-1 — spec/12_storage-architecture.md — §12.9 T4 cell pointer, **Credential pool secrets at rest.** paragraph, and narrowed credential bullet.
- SPEC-2 — spec/04_system-components.md — §4.9 **KMS-backed configurations.** paragraph, callout edits, and preflight sentence.
- SPEC-3 — spec/17_deployment-topology.md — §17.6 etcd warning row text.
- CODE-1 — pkg/preflight/install_prereqs.go and pkg/preflight/install_prereqs_test.go — `EtcdSecretEncryptionWarning` text and its unit test.
- CHART-1 — charts/lenny/values.yaml and charts/lenny/templates/etcd-encryption.yaml — comments for the self-managed T4 path.
- DOCS-1 — docs/operator-guide/security-principles.md, docs/operator-guide/security.md, and docs/operator-guide/installation.md — correct the encryption claims and state the T4 prerequisite.
- DOCS-2 — docs/runbooks/etcd-key-rotation.md — scope, trigger, and the Move to a KMS provider section.
- TEST-1 — tests/tier11_docs/etcd_kms_requirement_doc_test.go — documentation tests for the runbook and the operator pages.
