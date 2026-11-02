# Review log — 0115_fix_f-12-9-24-12-9-requires-envelope-encryption

## Standing context

## Ledger

### [spec.1.fix-G1.1]
DECISION: applied the design as given: a T4 setting is the only condition in SPEC-1 (b), with both citations named by heading and label in the same replacement, and one pointer sentence to §17.9.7 — BECAUSE the owner decision sets no production condition and the chart default is `environment: "dev"` — ALTERNATIVES: the rejected options are in spec.1.fix-design-G1.md and are not re-derived here.
DECISION: §4.9, §12.9, and §17.6 now give one verification boundary: SPEC-2 (b) changes `may not have` to `does not have` and SPEC-2 (c) replaces the conditional wording of the preflight sentence — BECAUSE the check has no verification input and warns unconditionally in production — ALTERNATIVES: deleting the two §4.9 sentences loses the §17.6 link.
WATCHOUT: do not reintroduce `preflight warns` or `preflight only warns` in any operator text for this control. Preflight is silent outside production mode, and the requirement now applies there. EVIDENCE: pkg/preflight/install_prereqs.go productionOnlyWarning.
WATCHOUT: `Production deployments MUST configure EncryptionConfiguration` in SPEC-3 and CODE-1 is the baseline sentence and is correct to keep. The T4 sentence after it carries no production qualifier, and a grep for `production` hits this text with nothing to fix. EVIDENCE: spec-changes.md SPEC-3.
FACT: the §17.4 banner names credentials as insecure, which is the disclosure Decision 4 relies on. EVIDENCE: spec/17_deployment-topology.md **Production warning banner.**
USEFUL [spec.1.fix-design-G1.1]: the Embedded Mode facts and the checklist DEFERRED wording saved re-derivation.
DEFERRED [proposals/0115_fix_f-12-9-24-12-9-requires-envelope-encryption/0115_fix_f-12-9-24-12-9-requires-envelope-encryption.implementation-checklist.md]: step S2 says "Lands the §4.9 paragraph and callout edit". SPEC-2 now has three parts, so S2 should read "Lands the §4.9 paragraph, callout edits, and preflight sentence."

### [spec.1.fix-G2.1]
DECISION: the SPEC-3 warning and CODE-1 constant end in one sentence citing §12.9 (Credential pool secrets at rest) and §4.9 (KMS-backed configurations), and state neither the condition nor the list, applied as designed — BECAUSE no gate compares the copies and the earlier copy had already drifted from §4.9 — ALTERNATIVES: the three reviewer forms and a re-synchronised copy plus a gate are rejected in spec.1.fix-design-G2.md.
WATCHOUT: do not restore a KMS option list, `aescbc and aesgcm do not qualify`, or a T4 MUST to the warning. The partial exclusion list contradicted the four §4.9 exclusions, and a MUST there contradicts the Embedded Mode exemption in §12.9. EVIDENCE: spec-changes.md SPEC-1 (b), SPEC-2 (a).
WATCHOUT: the warning names the labels `Credential pool secrets at rest` and `KMS-backed configurations` as text, so a rename of either bold label must carry into SPEC-3, CODE-1, and the `TestEtcdSecretEncryptionWarningCitesT4Requirement` substrings. EVIDENCE: non-spec-changes.md Testing table, first row.
FACT: SPEC-3 and CODE-1 were compared after the edit with `\|` normalised and are byte-identical. The existing assertions in pkg/preflight/install_prereqs_test.go pin only the opening clause, the etcdctl path, and the absence of `lenny-system`, so they survive unedited. pkg/preflight/run_test.go uses the constant by reference.
OPEN: the Testing table's `// spec:` cell for `TestEtcdSecretEncryptionWarningCitesT4Requirement` names §17.6 and §12.9, while the test now also asserts the §4.9 citation. The design left the cell alone, so the non-spec loop decides whether to add §4.9 "KMS-backed configurations.".
USEFUL [spec.1.fix-design-G2.1]: the site dispositions held against the current text, and only the SPEC-3 line number had drifted (71 to 72).

### [spec.1.fix-design-G1.1]
DECISION: drop the production conjunct from SPEC-1 (b); a T4 setting is the only condition — BECAUSE the owner decision (gateway-runtime-comms-remediation.md:4520) names only a T4 tenant, the chart default is `environment: "dev"`, and removing it also removes the environment-flip transition — ALTERNATIVES: keep the conjunct and reconcile it in Decision 4 (fail-open on a label value).
DECISION: the §17.6 warning stays production-only; SPEC-1 says "in production mode" and Decision 6 discloses that Lenny emits nothing elsewhere — BECAUSE the warning verifies nothing, has no T4 input, and the owner keeps dev deployments on current options — ALTERNATIVES: make the warning unconditional on Environment (changes EtcdSecretEncryptionCheck and its existing tests, fires on every laptop install, adds no verification).
DECISION: one sentence in SPEC-1 points at the §17.9.7 Embedded Mode exemption, with no edit to §17.9.7 and no Embedded clause in operator docs — BECAUSE removing the conjunct is what makes the unqualified "must" collide with the exemption, and T4 is settable in Embedded Mode — ALTERNATIVES: remove the exemption for T4 (a laptop k3s has no way to consume a provider; the rendered document is inert, charts/lenny/presets/dev.yaml:233-243).
FACT: Embedded Mode does not refuse a T4 setting: WithKMSProbe is wired unconditionally over the resolved provider, including `local` (cmd/lenny-gateway/adminrouter.go:195, 306), so the carve-out covers a reachable state. Source Mode and Compose Mode run without Kubernetes, so they hold no Kubernetes Secrets and need no carve-out. EVIDENCE: spec/17_deployment-topology.md §17.4 Source Mode.
WATCHOUT: `#1797-backend-invariant-requirements` has no existing inbound link in spec/; the slug follows the same rule as `#1791-composition-dimensions`, so run the citation resolver when SPEC-1 lands. EVIDENCE: spec/17_deployment-topology.md:1598.
DEFERRED [0115_fix_f-12-9-24-12-9-requires-envelope-encryption.implementation-checklist.md]: S2 says "Lands the §4.9 paragraph and callout edit"; after this fix SPEC-2 has three parts, so S2 should read "Lands the §4.9 paragraph, callout edits, and preflight sentence".

### [spec.1.fix-design-G2.1]
DECISION: The SPEC-3 and CODE-1 T4 sentence is one citing sentence that replaces the old T4 sentence and the trailing `See Section 4.9 and Section 12.9.`: `Section 12.9 (Credential pool secrets at rest) states when a workspaceTier T4 setting requires a configuration listed in Section 4.9 (KMS-backed configurations).` — BECAUSE it names the term an operator searches for, states neither the predicate nor the list, and stays true if the §12.9 condition or the §4.9 list changes — ALTERNATIVES: form 1 (`MUST use a KMS-backed option Section 4.9 lists; aescbc and aesgcm do not qualify`) keeps a partial exclusion list and needs a subject that restates the predicate; form 2 asserts MUST for a T4 setting and then defers when it applies, so it drops production mode; form 3 loses the words workspaceTier T4, so an operator of a T4 deployment cannot tell the sentence concerns them.
DECISION: `aescbc and aesgcm do not qualify` is dropped from the warning — BECAUSE it is the partial copy of the §4.9 exclusions the finding names as drift; the home keeps the exclusion, and §4.9 lists only KMS-backed configurations — ALTERNATIVES: keeping it preserves the test substring and keeps the drift.
WATCHOUT: The warning keeps `Production deployments MUST configure EncryptionConfiguration with aescbc/aesgcm/kms`. That is the baseline and summary defect 11 files it. A review reading it as the warning approving aescbc for a T4 deployment should read the citing sentence after it before filing — EVIDENCE: spec-changes.md:71; summary.md:103
WATCHOUT: The citing sentence depends on the two bold labels by name. A rename of **Credential pool secrets at rest.** or **KMS-backed configurations.** must carry into SPEC-3, CODE-1, and the unit test substrings, and no gate compares them — EVIDENCE: spec-changes.md:37,51,71; summary.md:39
FACT: Nothing asserts `See Section 4.9`; the tests hold only the opening clause, the etcdctl path, and the reference in pkg/preflight/run_test.go:150, so the tail can go — EVIDENCE: pkg/preflight/install_prereqs_test.go:210-212,227-231; pkg/preflight/run_test.go:150
USEFUL [spec.1.review-single-source.1]: it named the CODE-1 assertion strings and the reader-doc exemption boundary, which fixed the sites this design had to touch.

### [spec.1.follow-up-fix.1]
DECISION: Qualified the merged edge-case bullet in spec-changes.md with "outside Embedded Mode" — BECAUSE the SPEC-1 paragraph keeps the §17.9.7 exemption and the edge-case list only reports where that paragraph lands — ALTERNATIVES: deleting the "whether or not" clause, rejected because it records that the requirement has no production conjunct.

### [spec.1.review-applicability.1]
FACT: Every staged anchor in SPEC-1, SPEC-2, and SPEC-3 matches the tree at edee08bdf exactly once, and the heading anchors they link exist. EVIDENCE: spec/12_storage-architecture.md:1092,1101,1103,1109; spec/04_system-components.md:1646,1687,2231; spec/17_deployment-topology.md:556,1416
WATCHOUT: Credential Storage Topology holds two tables, the Secret key table and the topology table, so "the table in that section" names neither uniquely. EVIDENCE: spec/04_system-components.md:1667,1681
WATCHOUT: S1 cites the **KMS-backed configurations.** label that S2 creates, and S2 cites the §12.9 label S1 creates. The cycle cannot be ordered away, and no gate resolves bold labels, because scripts/specshift/gate covers line citations only. EVIDENCE: scripts/specshift/gate/resolution.go:3-11
FACT: No chart template, schema rule, or Go path refuses `etcdEncryption.enabled: false` in production, so the SPEC-2 self-managed path works. EVIDENCE: charts/lenny/templates/etcd-encryption.yaml:42

### [spec.1.review-citations.1]
FACT: Every staged locator in SPEC-1 to SPEC-3 resolves at edee08bdf: the Encryption at rest row, the Enforcement and T2 paragraphs, the Credential encryption bullet, the §4.9 table followed by the preflight sentence, the callout's replaced clause, and the §17.6 row. The SPEC-3 message matches CODE-1 after unescaping. EVIDENCE: spec/12_storage-architecture.md:1092, spec/04_system-components.md:1687, spec/04_system-components.md:2231, spec/17_deployment-topology.md:556
FACT: Secrets appear in no §12.6 store interface; the nearest entries are CredentialGenerator and the Postgres CredentialPoolStore, so the SPEC-1 reason for the Enforcement carve-out holds. EVIDENCE: spec/12_storage-architecture.md:18, spec/12_storage-architecture.md:662
WATCHOUT: SandboxTemplate also carries workspaceTier, but it records the tier of the Runtime the pool serves, so the condition "tenant, environment, or Runtime" misses no independent setting. EVIDENCE: pkg/apis/lenny/v1alpha1/sandboxtemplate_types.go:100

### [spec.1.review-client-surface.1]
FACT: The staged spec edits change no REST, OpenAPI, MCP, proto, CRD, or SDK contract. The only externally read strings they change are the §17.6 etcd warning (mirrored by `EtcdSecretEncryptionWarning`, with no other verbatim copy under spec, docs, pkg, tests, or cmd) and the operator use of `etcdEncryption.enabled`. — EVIDENCE: pkg/preflight/install_prereqs.go:127; spec/17_deployment-topology.md:556
FACT: `workspaceTier` exists at all three levels SPEC-1 names: tenant and Runtime in the OpenAPI schemas, environment in the store. — EVIDENCE: pkg/gateway/externalapi/openapi/openapi.json:262,278; pkg/gateway/environment/environmentstore/environmentstore.go:85
WATCHOUT: The §15.1 `CLASSIFICATION_CONTROL_VIOLATION` row keeps its example "T4 data directed to a store without envelope encryption". It is an emission example and cites §12.9, whose new paragraph carves out Secret writes, so it is not false. Do not re-file it. — EVIDENCE: spec/15_external-api-surface.md:1195

### [spec.1.review-docs-alignment.1]
FACT: The preflight Job is a pre-install,pre-upgrade hook, so the §17.6 etcd warning fires only at helm install or upgrade; a post-install T4 setting gets no warning until the next upgrade. EVIDENCE: charts/lenny/templates preflight hook annotations `"helm.sh/hook": pre-install,pre-upgrade`.
FACT: `kms-unavailable` runbook and the KMSUnavailable alert do not describe an apiserver-side etcd KMS failure; that cause has no Lenny alert. EVIDENCE: docs/runbooks/kms-unavailable.md (no etcd mention); spec-changes has no row for it.
FACT: The §4.9 EKS row names only a CMK, so "AWS KMS (EKS)" in SPEC-3 does not drift from the definition today. EVIDENCE: spec/04_system-components.md Credential Storage Topology table.
DEFERRED [non-spec-changes.md]: docs/runbooks/token-service-outage.md or kms-unavailable.md should gain the etcd-KMS failure as a cause once the spec states it; docs/operator-guide/security.md Supported KMS Providers already reads correctly; docs/tutorials/deploy-to-cluster.md "etcd encryption" row is unstaged and carries no T4 requirement.

### [spec.1.review-edit-sites.1]
FACT: The spec surfaces stating etcd or pool-Secret encryption are §4.9 (topology table, rule 8, callout, two-plane intro), §12.9, §17.6 row, §17.9 "Encryption at rest for all persistent stores", §18.1/§18.2/§18.14/§18.15/§18.40, and §14 callbackSecret. §13, §12.6, and §25 state nothing that the staged edits falsify. EVIDENCE: grep -i etcd and KMS over spec/*.md; spec/17_deployment-topology.md:1616
FACT: `--secret-encryption-verified` on lenny-tier-promote is an operator attestation flag, so it does not falsify "no Lenny component can read the etcd encryption provider". EVIDENCE: pkg/tierpromotion/gather.go:84, cmd/lenny-tier-promote/main.go:46
WATCHOUT: The §14 callbackSecret sentence and the §12.9 T2 "application-layer ... T3/T4" sentence overlap this change, but both are already filed in the summary defects list (items 12 and 13), so they are declared rather than missed edit sites. EVIDENCE: spec/14_workspace-plan-schema.md:127, spec/12_storage-architecture.md T2 paragraph
DEFERRED [non-spec-changes.md]: docs/index.md:233 says credential pools are "encrypted with a cloud KMS", and the ASCII source at docs/index.md:218 says "credential pools (KMS)". DOCS-1 does not list the page. True statement: pool API keys are in Kubernetes Secrets under etcd encryption, which is KMS-backed only under the §12.9 condition.
DEFERRED [summary.md]: spec/25_agent-operability.md:4037 and :4062 name `credential_pools.raw_secret` and `credential_pool_raw_secrets`, and neither exists in migrations/ or pkg/ (grep raw_secret). This is an unlisted stale reference to Postgres storage of pool secrets, so it belongs in the defects list.

### [spec.1.review-fresh.1]
FACT: The chart default is `environment: "dev"`, so the production-mode gate in the SPEC-1 condition and the production-only §17.6 warning are both off for a deployment that never sets the value. EVIDENCE: charts/lenny/values.yaml:984; spec/17_deployment-topology.md §17.9.1 **Production mode.**
WATCHOUT: "that section" in the SPEC-1 paragraph follows a §17.9.1 link in the same sentence, and §17.9.1 has its own table, so cite Credential Storage Topology by name at both uses. EVIDENCE: proposals/0115_fix_f-12-9-24-12-9-requires-envelope-encryption/0115_fix_f-12-9-24-12-9-requires-envelope-encryption.spec-changes.md SPEC-1 (b)
CORRECTS [spec.1.review-plan-conformance.1]: production-only (Decision 4) is recorded in the summary, but the record does not settle it: it departs from the binding owner condition for a non-prod deployment that does have a T4 setting, so I filed it.

### [spec.1.review-kubernetes.1]
FACT: The preflight Job carries `helm.sh/hook-delete-policy: before-hook-creation,hook-succeeded`, so Helm deletes it on success. The etcd check always passes, so the §17.6 warning sits in a log that is gone after any successful install or upgrade, and the NOTES line `kubectl logs job/lenny-preflight` then finds nothing. EVIDENCE: charts/lenny/templates/preflight-job.yaml:252; charts/lenny/templates/NOTES.txt:9; cmd/lenny-preflight/main.go:868
FACT: `lenny-ctl preflight` does not run `preflight.Run` or `EtcdSecretEncryptionCheck`. It runs the infra probes or `POST /v1/admin/preflight`, despite §17.6 "runs the same checks". EVIDENCE: pkg/ctlcli/preflight.go:79-84; only cmd/lenny-preflight/main.go calls pkg/preflight.Run
FACT: A pool value cannot reach the bootstrap ConfigMap (a ConfigMap is outside the `secrets` encryption resource), because the seed refuses `secretData`. Do not re-open this. EVIDENCE: spec/04_system-components.md:1714; pkg/gateway/externalapi/admin/bootstrap.go:372
OPEN: kube-apiserver 1.31 carries the strings `kms-providers`, `kms-provider-%d`, and `k8s:enc:kms:v2:` (envtest binary), so `/readyz?verbose` may expose a negative KMS signal on self-managed clusters. Not filed: the name does not identify the `secrets` resource, managed-cluster behaviour is unverified from this tree, and summary Non-goals declines apiserver surfaces. Anyone revisiting "no Lenny component can read the etcd encryption provider" against the owner's "fail closed where it can be verified" should test this on a real cluster.

### [spec.1.review-mechanism.1]
FACT: the chart default is `environment: "dev"`, and `productionOnlyWarning` returns a silent pass outside prod, so any T4 condition with a production conjunct is silently off on a default install — EVIDENCE: charts/lenny/values.yaml:984; pkg/preflight/install_prereqs.go:179-183
FACT: all SPEC-1, SPEC-2, and SPEC-3 anchors and quoted targets match the tree, and §12.6 has no store interface over Kubernetes Secrets — EVIDENCE: spec/04_system-components.md:1687,2231; spec/12_storage-architecture.md:1092,1101,1103,1109; spec/17_deployment-topology.md:556
FACT: `etcdEncryption.enabled` has no gate reading it outside the chart template and schema, so `false` on a self-managed cluster trips nothing — EVIDENCE: grep over charts, pkg, cmd, and spec lists only the template, values, schema, dev preset, chart test, pkg/chart/values/values.go, and spec/18
WATCHOUT: in the SPEC-1 paragraph "that section" follows the §17.9.1 citation, so its nearest antecedent is the wrong section. Reported as nothing (wording), and a fixer touching that sentence should name Credential Storage Topology — EVIDENCE: spec-changes.md:37
UNVERIFIED: "no Lenny component can read the etcd encryption provider" was not tested against a live cluster. An apiserver with a `kms` provider may list a kms health check under /livez?verbose on self-managed clusters, a positive-only signal. Summary non-goals decline apiserver surfaces. Whoever re-reads the verification boundary should check it on kind.

### [spec.1.review-plan-conformance.1]
FACT: The plan's whole assignment for this proposal is two entries in §10.2: the owner decision bullet (gateway-runtime-comms-remediation.md:4520) and the queue item (:4560). No numbered R-step claims F-12.9.24, and no plan-wide invariant reaches it (the claim register covers the communication-channel surface only). EVIDENCE: gateway-runtime-comms-remediation.md:4520, :4560, :139
FACT: Departures from the plan's literal "T4 tenant" and "`kms` provider" wording are recorded divergences, so a later conformance pass must not re-file them: predicate widening (summary Decision 3), production-only (Decision 4), managed-cluster KMS equivalents (Decision 5), and no fail-closed gate (Decision 9). EVIDENCE: 0115 summary.md Decisions 3, 4, 5, and 9

### [spec.1.review-reliability.1]
FACT: An apiserver-side KMS outage on a Secret read is already covered: every Token Service read is one uncached `get`, and any failure other than NotFound or Forbidden is a `TRANSIENT`-category error with no earlier value served, so the staged `kms` requirement adds no new stale-read path. — EVIDENCE: spec/04_system-components.md:2242-2243, **Token Service secret resolution.** rules 2 and 3
FACT: The staged rule adds no Lenny-side operation, retry, or state, because the §17.6 check keeps its always-pass behaviour and the edits are prose plus warning text. Redelivery, idempotency, and fencing questions have nothing to attach to. — EVIDENCE: pkg/preflight/install_prereqs.go:150-152
WATCHOUT: The §4.9 self-managed row rewrites Secrets with `kubectl apply`, while the chart template comment and the runbook use `kubectl replace`. The staging keeps the row, and DOCS-2 routes the migration through the runbook's replace loop, so the applied procedure is the replace form. I did not file this: it predates the proposal and I could not show that apply fails to rewrite. — EVIDENCE: spec/04_system-components.md:1685; charts/lenny/templates/etcd-encryption.yaml:22-24; docs/runbooks/etcd-key-rotation.md:164-169
UNVERIFIED: Whether §17.7 Token Service outage remediation step (3) should also name the apiserver's KMS as a cause of failing Secret reads. It addresses only the Token Service's own KMS access. EKS and GKE CMEK deployments already have this dependency under the current §4.9 table, so the staged edits do not make the step wrong, and I did not file it. — EVIDENCE: spec/17_deployment-topology.md:832

### [spec.1.review-security.1]
FACT: `workspaceTier: T4` on an environment or Runtime is writable by a tenant-admin, because the §10.2 matrix grants "Manage environments" and "Manage runtimes" on the own tenant and the environment routes gate only on manage_environments. EVIDENCE: spec/10_gateway-internals.md:302, 311; pkg/gateway/externalapi/admin/routes.go:381-391
FACT: The chart default is `environment: "dev"`, so a deployment that never sets the value is outside production mode. EVIDENCE: charts/lenny/values.yaml:984; spec/17_deployment-topology.md:1416
WATCHOUT: A provider-detection finding through apiserver health surfaces was considered and not filed, because the summary non-goals decline it and nothing in the repo settles it. UNVERIFIED: whether /readyz exposes KMS provider state uniformly; the owner of a later gate decision should check. EVIDENCE: 0115 summary non-goals, "Detecting the provider"

### [spec.1.review-single-source.1]
FACT: The preflight warning text may cite spec sections: the current §17.6 row already ends `See Section 4.9.`, so the reader-doc exemption for restating a rule does not cover SPEC-3 or CODE-1. EVIDENCE: spec/17_deployment-topology.md:556
WATCHOUT: If SPEC-3 drops the KMS list and the predicate, CODE-1 and the assertion strings of TestEtcdSecretEncryptionWarningStatesT4Requirement must follow it; those strings are `MUST use a KMS-backed option` and `aescbc and aesgcm do not qualify`. EVIDENCE: proposals/0115_fix_f-12-9-24-12-9-requires-envelope-encryption/0115_fix_f-12-9-24-12-9-requires-envelope-encryption.non-spec-changes.md:14,180
WATCHOUT: The non-spec loop should check the KMS-backed list and the condition in DOCS-1 and DOCS-2. The list appears in both security.md and the runbook introduction, and the condition appears at several sites, so the one permitted reader-vocabulary restatement is exceeded. EVIDENCE: same file:59,75,81,101,103

### [spec.2.fix-G1.1]
DECISION: applied the design as given: deleted the edge-case bullet on a T4 setting with a non-KMS-backed configuration and changed the next bullet to "in the last sentence" — BECAUSE the case is the negation of the SPEC-1 (b) condition, and summary Decisions 4 and 6 hold the reasoning — ALTERNATIVES: the citation bullet and the shortened bullet are rejected in spec.2.fix-design-G1.md.
USEFUL [spec.2.fix-design-G1.1]: the WATCHOUT on the "same sentence" back-reference and the site quote held against the file, so the cascade needed no re-derivation.

### [spec.2.fix-design-G1.1]
DECISION: delete the spec-changes.md bullet "A deployment with a T4 setting and a non-KMS-backed configuration" and change line 20's "in the same sentence" to "in the last sentence" — BECAUSE the bullet carries nothing without a home: the condition and the Embedded Mode exemption are in SPEC-1 (b), the absent production conjunct is summary Decision 4, and the silence outside production is Decision 6, so the case is the rule's own negation and no implementor builds differently — ALTERNATIVES: the reviewer's citation bullet, one rung higher and "names the only signal" describes the target, while the SPEC-1 sentence never says "only".
CORRECTS [spec.1.follow-up-fix.1]: its reason for keeping the "whether or not" clause no longer holds. Decision 4 records that the requirement has no production conjunct, so the bullet need not.
WATCHOUT: after the deletion, "the same sentence" on the next bullet would bind to "the condition sentence" on the bullet above it. The rewrite obligation is in the SPEC-1 (b) last sentence, which is why it has to name that sentence. EVIDENCE: spec-changes.md:17,18,20 and SPEC-1 (b).

### [spec.2.review-applicability.1]
FACT: All SPEC-1/2/3 anchors resolve uniquely in the tree at this round, including the shortened `The preflight Job emits a non-blocking warning` (spec/04_system-components.md:1687); the callout's `The lenny-preflight Job emits` is a different string. EVIDENCE: spec/04_system-components.md:1687, 2231
FACT: `#1797-backend-invariant-requirements` has no inbound link yet; it derives from the heading the same way as the existing `#1796-embedded-backends-embedded-mode`, and that bullet does state the Embedded exemption. EVIDENCE: spec/17_deployment-topology.md:1598, 1615

### [spec.2.review-docs-alignment.1]
FACT: The "Embedded Mode stays exempt" citation holds: the §17.9.7 **Encryption at rest** bullet reads "exempt in Embedded Mode". The heading is not the only place to read, because the Embedded clauses sit in individual bullets. EVIDENCE: spec/17_deployment-topology.md:1616
FACT: After the round 1 fixes, the production-only warning statement in SPEC-1 (b) and SPEC-2 (c), the edge-case bullet, and the §17.6 Validation cell agree. EVIDENCE: spec/17_deployment-topology.md:556

### [spec.2.review-fresh.1]
FACT: The staged anchors resolve. `#credential-storage-topology` is already used in spec, and the §17.9.7 heading yields `1797-backend-invariant-requirements`, though no existing link uses that anchor. EVIDENCE: spec/04_system-components.md:1646, spec/04_system-components.md:2241, spec/17_deployment-topology.md:1598
FACT: Source Mode runs without Kubernetes, so it has no etcd and is not named by the Embedded Mode exemption. The staged text says "deployment" and it is a contributor path, so I judged it below the finding bar. EVIDENCE: spec/17_deployment-topology.md:213

### [spec.2.review-mechanism.1]
FACT: After removing the production conjunct, the requirement and warning scopes differ on purpose: SPEC-1 (b) binds any non-Embedded deployment with a T4 setting, while the §17.6 row and SPEC-2 (c) warn in production mode only. The three statements agree with `productionOnlyWarning`. EVIDENCE: spec/17_deployment-topology.md:556; pkg/preflight/install_prereqs.go:179
WATCHOUT: Source Mode and Compose Mode have no etcd and sit outside the carve-out wording. This is not a defect: the paragraph premises Kubernetes Secrets and §17.4 calls them contributor paths, so do not refile it as a missing exemption. EVIDENCE: spec/17_deployment-topology.md:149,213
USEFUL [spec.1.fix-design-G1.1]: the Source and Compose Mode fact settled the only predicate-drift candidate this round.

### [spec.2.review-security.1]
WATCHOUT: the new "Embedded Mode stays exempt" sentence and the "outside Embedded Mode it is non-conformant" edge case do not name Source Mode or Compose Mode, which do not use the Kubernetes path the paragraph describes. Not filed: §17.9.7 exempts Embedded Mode only, the paragraph scopes itself to Secrets in etcd, and no security property changes. EVIDENCE: spec/17_deployment-topology.md:213, spec/17_deployment-topology.md:1616
FACT: pkg/tierpromotion SecretEncryptionVerified is an operator attestation of any etcd encryption and does not distinguish KMS-backed; it reads §4.9 and needs no change for a capacity-tier gate unless a later proposal ties it to T4. EVIDENCE: pkg/tierpromotion/checks.go:164

### [spec.2.review-single-source.1]
FACT: The warning's production-only rule and its cannot-verify reason stay at §17.6 (Validation cell, the home), §4.9 sentence, §4.9 callout, and §12.9, but each of the last three is one clause citing §17.6, so none is a stating site under the lens. The two SPEC-2 wording edits are synchronisation in that sense and are not a (g) finding. EVIDENCE: spec/17_deployment-topology.md:556; spec-changes.md:36,59,65
WATCHOUT: After the G2 fix, SPEC-3 keeps only the baseline sentence and cites §12.9 and §4.9 for T4. CODE-1 repeats the SPEC-3 string by design, and that mirror is the existing spec/constant contract. EVIDENCE: spec-changes.md:72; non-spec-changes.md:14

### [spec.3.review-single-source.1]
FACT: The round 3 diff is one deleted edge-case bullet plus "same sentence" changed to "last sentence"; the last sentence of the SPEC-1 paragraph does carry the rewrite obligation, and no other proposal file cites the deleted bullet. EVIDENCE: spec-changes.md:19, spec-changes.md:35

### [spec.4.review-applicability.1]
FACT: every SPEC-1, SPEC-2, and SPEC-3 anchor matches one site in the current tree, and every staged link target exists; the SPEC-3 cell and the CODE-1 constant are byte-identical apart from the `\|` escape. EVIDENCE: spec/12_storage-architecture.md:1092,1101,1103,1109; spec/04_system-components.md:1646,1678,1687,2231; spec/17_deployment-topology.md:556,1598
USEFUL [S1/S2 label cycle WATCHOUT in the review log]: the mutual bold-label citation between S1 and S2 cannot be ordered away and no gate under scripts/specshift/gate resolves bold labels, so it is not a forward-reference finding.

### [spec.4.review-citations.1]
FACT: After round 3 every staged locator and anchor in SPEC-1 to SPEC-3 still resolves, including `#1797-backend-invariant-requirements` (heading spec/17_deployment-topology.md:1598 has no custom anchor) and the three SPEC-2 replacement strings. SPEC-3 equals CODE-1 after normalising `\|`, by script. EVIDENCE: spec/04_system-components.md:1687,2231; spec/12_storage-architecture.md:1092,1101,1103,1109
FACT: The only non-test Go mention of the apiserver EncryptionConfiguration outside comments and strings is the embedded manifest copy; pkg/tierpromotion refers to it in an attestation comment and failure text, so "no Lenny component can read the etcd encryption provider" holds. EVIDENCE: pkg/tierpromotion/tierpromotion.go:151, pkg/tierpromotion/checks.go:166

### [spec.4.review-client-surface.1]
FACT: At spec round 4 the staged edits still change no REST, OpenAPI, MCP, A2A, proto, JSONL, CRD, SDK, enum, or error-code contract. The externally read strings are the §17.6 warning, which matches CODE-1 after unescaping, and the operator use of `etcdEncryption.enabled`, which the chart schema admits as an open object. EVIDENCE: spec/17_deployment-topology.md:556; charts/lenny/values.schema.json:100
FACT: The SPEC-1 citation of the Embedded Mode exemption holds, and `workspaceTier` is settable through the bootstrap seed. EVIDENCE: spec/17_deployment-topology.md §17.9.7 "Encryption at rest"; pkg/gateway/externalapi/admin/bootstrap.go
USEFUL [spec.1.review-client-surface.1]: the §15.1 `CLASSIFICATION_CONTROL_VIOLATION` WATCHOUT saved re-deriving that row.

### [spec.4.review-docs-alignment.1]
FACT: Each Edge-cases bullet in spec-changes.md resolves to SPEC-1 (b) text after the round 3 deletion, and the non-production silence is carried by "in production mode" in SPEC-1 (b) and SPEC-2 (c). EVIDENCE: spec-changes.md:17-20,37
FACT: No spec site outside SPEC-1 to SPEC-3 lists the etcd runbook content or claims pool Secrets are KMS encrypted, apart from the §14 `callbackSecret` sentence already filed as summary defect 13. EVIDENCE: spec/14_workspace-plan-schema.md:127; spec/18_build-sequence.md:289

### [spec.4.review-edit-sites.1]
FACT: The etcd-encryption surface in spec/ is §4.9 (topology table, rule 8, callout, §4.9.1 pointer), §17.6 row, and §18.14/§18.15/§18.40 build items, plus §12.9. No hit in §10, §11, §13, §15, §24, §25 or schemas/ for etcd encryption. EVIDENCE: grep -i etcd spec/*.md, filtered for encrypt|secret|key-rot.
DEFERRED [non-spec-changes.md]: docs/index.md:233 says pool credentials are "encrypted with a cloud KMS". DOCS-1 does not stage that page. After the §12.9 paragraph this is false for non-KMS-backed deployments, and true wording is that pool credentials sit in Kubernetes Secrets under the deployment's etcd encryption. The non-spec loop should stage it or file it in the summary defects list.

### [spec.4.review-fresh.1]
FACT: `lenny-tier-promote` takes etcd Secret encryption as a bare operator boolean (`--secret-encryption-verified`) and reads no provider, so the SPEC-1 clause "no Lenny component can read the etcd encryption provider" holds against the one Go check that looks like a counterexample. EVIDENCE: cmd/lenny-tier-promote/main.go:46; pkg/tierpromotion/gather.go:84-86,134
WATCHOUT: Compose Mode carries pool credentials outside Kubernetes (spec/17_deployment-topology.md:243), so spec.1.fix-design-G1.1 "hold no Kubernetes Secrets and need no carve-out" rests on the paragraph being scoped to Kubernetes Secrets rather than on Compose having no pool credentials. Judged below the bar here: the paragraph opens "They live in Kubernetes Secrets" and Compose is a development mode. EVIDENCE: spec/17_deployment-topology.md:243; spec-changes.md SPEC-1 (b)

### [spec.4.review-kubernetes.1]
FACT: The staged spec edits add no controller, CRD field, status write, finalizer, webhook, or hot-path call, so the single-writer, status-as-command, finalizer, and hot-path idioms have nothing to attach to. The only Kubernetes claims are about the kube-apiserver provider and were checked against the chart template header and §4.9. EVIDENCE: spec-changes.md SPEC-1 (b), SPEC-2 (a); charts/lenny/templates/etcd-encryption.yaml:9-29,79-96
USEFUL [spec.1.review-kubernetes.1]: the `/readyz?verbose` OPEN stops the next reviewer re-deriving it. k8s.io/apiserver source is not in the module cache, so nothing in this tree settles it, and the summary Non-goals decline apiserver surfaces. I did not file it.

### [spec.4.review-mechanism.1]
FACT: The SPEC-1 predicate misses no T4 path: pool and SandboxTemplate tiers are copied from the Runtime (pkg/controller/sandbox/controller.go:563), and no compliance profile implies T4 in spec or non-test pkg code. EVIDENCE: pkg/apis/lenny/v1alpha1/sandboxtemplate_types.go:100; pkg/gateway/runtime/poolstore/poolstore.go:816
FACT: The SPEC-2 (a) claim that the chart document lists only `aescbc` and `identity` holds. EVIDENCE: charts/lenny/templates/etcd-encryption.yaml:86-93
FACT: No spec section lists etcd-key-rotation, so DOCS-2 leaves §17.7 true; the §17.7 etcd entry is etcd-operations. EVIDENCE: grep etcd-key-rotation over spec/ returns nothing; spec/17_deployment-topology.md:844
USEFUL [spec.2.review-mechanism.1]: the Source and Compose Mode WATCHOUT stopped a refile.

### [spec.4.review-plan-conformance.1]
FACT: The plan's only assignments for F-12.9.24 are the owner decision and the proposal-routing queue item; it names no step-level artifact beyond the kms condition and the preflight/operator boundary, both staged in SPEC-1 (b). EVIDENCE: gateway-runtime-comms-remediation.md:4520, 4560-4562

### [spec.4.review-reliability.1]
FACT: The staged spec edits add no retry, lease, queue, or recovery path, and spec/ names no Lenny-owned backup or restore of credential Secrets (the §25 backup job touches Postgres and MinIO), so a reliability review of this staging reduces to the anchors and the claims about the chart and preflight. EVIDENCE: spec/25_agent-operability.md:4030-4031, spec/04_system-components.md:1678
FACT: `workspaceTier` is a stricter-only ratchet, so a deployment that already holds T4 on a non-KMS-backed configuration can only reach conformance by migrating the provider. Not filed: the "must use" sentence still binds it, and only the "before any path sets" timing has no purchase on it. EVIDENCE: spec/15_external-api-surface.md:927
USEFUL [spec.1.review-reliability.1]: the replace versus apply note saved re-deriving the rewrite path.

### [spec.4.review-security.1]
DECISION: filed nothing — BECAUSE the staging removes no existing control (no fail-closed check exists for the etcd provider, and the Enforcement rejection never reached Secrets), and the bound is an operator obligation by owner decision rather than a pod or store self-report — ALTERNATIVES: filing the §14 callbackSecret over-claim (already summary defect 13) and filing the Source and Compose Mode gap (already judged below the bar in spec.2).
FACT: `workspaceTier` is carried by tenant, environment, and Runtime as authored sources; SandboxTemplate's field is the tier of the runtime the pool serves, and a derived Runtime may not declare it, so the SPEC-1 condition has no uncovered authoring path. EVIDENCE: charts/lenny/crds/lenny.dev_sandboxtemplates.yaml:461-465; pkg/gateway/externalapi/admin/runtimes.go:190-192
WATCHOUT: the Runtime tier is not ratcheted (PUT back to T3 is tested), unlike the tenant tier, so "a T4 setting" on a Runtime can lapse while Secrets stay KMS-backed. This relaxes nothing, because SPEC-1 only binds while a T4 setting exists. EVIDENCE: pkg/gateway/externalapi/admin/runtimes_test.go:1470-1510
USEFUL [spec.1.review-edit-sites.1]: its list of spec surfaces stating pool-Secret encryption matched a fresh grep, apart from the etcd lease-state sentence at spec/18_build-sequence.md:807, which states nothing the staging falsifies.

### [spec.4.review-single-source.1]
DECISION: filed nothing — BECAUSE each staged rule has one stating site: the condition and verification boundary in §12.9 **Credential pool secrets at rest.**, the term in §4.9 **KMS-backed configurations.**, and SPEC-2 (b), SPEC-3, the T4 cell, the credential bullet, and the Edge cases all cite them — ALTERNATIVES: filing the preflight-warning clause below, rejected as a one-clause summary carrying a §17.6 citation.
WATCHOUT: the preflight warning's condition and reason now appear as a clause at §4.9 **Encryption at rest — KMS key hierarchy.** (SPEC-2 c), the §4.9 callout, and SPEC-1 (b), with the §17.6 row as home. Each site cites §17.6 and all agree today, so this is not a copy; if the §17.6 condition changes, update all three clauses. EVIDENCE: spec/17_deployment-topology.md:556, spec-changes.md SPEC-1 (b), SPEC-2 (b) and (c).

### [spec.reconcile.1]
CORRECTS [spec.1.fix-G1.1]: closed its DEFERRED on the checklist. Step S2 now reads "Lands the §4.9 paragraph, callout edits, and preflight sentence."
CORRECTS [spec.1.fix-design-G1.1]: closed its DEFERRED on the checklist with the same S2 edit, because it is the same correction as the entry above.
OPEN: from [spec.1.review-docs-alignment.1], no staged change names the etcd-KMS failure as a cause in docs/runbooks/token-service-outage.md or docs/runbooks/kms-unavailable.md. It lands in non-spec-changes.md as a new staged docs change, which the non-spec loop has to author.
OPEN: from [spec.1.review-edit-sites.1] and [spec.4.review-edit-sites.1], docs/index.md says pool credentials are "encrypted with a cloud KMS" at line 233 and "credential pools (KMS)" in the ASCII source at line 218, and DOCS-1 does not stage the page. Both lines still stand. The correction lands in non-spec-changes.md as a new DOCS-1 edit, or as a summary.md defects entry, and the true wording is that pool credentials sit in Kubernetes Secrets under the deployment's etcd encryption, which is KMS-backed only under the §12.9 condition.
OPEN: from [spec.1.review-edit-sites.1], spec/25_agent-operability.md names `credential_pools.raw_secret` under Job Pod Specification and `credential_pool_raw_secrets` under Sensitive Content Policy, and both names still stand. The entry adds a defect rather than repairing a false statement, so this pass did not write it. It lands in the summary.md defects list.
FACT: the deliverable index and checklist name SPEC-1 to SPEC-3, CODE-1, CHART-1, DOCS-1, DOCS-2, and TEST-1, each once. S8 gained S1 and S2 in its dependencies because TEST-1 annotates §12.9 and §4.9. The spec-annotation question from [spec.1.fix-G2.1] now stands in the summary as open decision 1. The `/readyz?verbose` entry from [spec.1.review-kubernetes.1] is a verification question, so it stays here and out of the summary.

### [f1.apply-spec-annotation]
DECISION: resolved the open decision on the `// spec:` annotation of `TestEtcdSecretEncryptionWarningCitesT4Requirement`: the row's `// spec:` cell in the non-spec Testing table now also names §4.9 "KMS-backed configurations.", and the entry left the summary's `## Open decisions for human to make`, which is now empty. This closes the OPEN recorded in [spec.1.fix-G2.1] — BECAUSE .claude/rules/test-coverage.md:70 requires the annotation to name the sections a test exercises, and the row asserts the §4.9 citation as it asserts the §12.9 one — AUTHORITY: .claude/rules/test-coverage.md:70; non-spec-changes.md Testing table, first row; spec-changes.md:9; pkg/preflight/install_prereqs_test.go:224-225 — ALTERNATIVES: leaving the cell as staged keeps §12.9 on the same form of assertion and drops §4.9.

### [f1.apply-t4-key-scope]
DECISION: resolved the out-of-scope item on row 8 (T4 key scope): the section 1.5 sentence in DOCS-1 now reads "a tenant with `workspaceTier: T4` uses SSE-KMS under a tenant-scoped key", the control primitives row now reads "SSE-KMS for object storage of tenants with `workspaceTier: T4`", and row 8 of the summary's defects list gained one sentence naming the DOCS-1 scoping. The spec conflict itself stays filed in row 8, as the disposition states, so the entry was not removed. BECAUSE both sentences are newly authored by DOCS-1, the same paragraph admits a T4 environment or Runtime, and the resolver selects no tenant key for those — AUTHORITY: cmd/lenny-gateway/t4_encryption.go:127-138; pkg/gateway/externalapi/admin/environments.go:46-48; spec/12_storage-architecture.md:317 — ALTERNATIVES: leaving the unqualified token repeats a false attestation; deciding key scope here needs a spec decision.
FACT: TEST-1 needs no edit, per the disposition: it asserts removal of old phrases and the requirement at the `security.md` sites, so the reworded sentences leave it passing. No deliverable changed, so the checklist is untouched. The log carries no `## Retired` section, so this block ends `## Ledger`.

### [non-spec.1.fix-G1.1]
DECISION: applied both G1 designs as written, with no departure; the route for a cloud-managed cluster has one home, the second paragraph of `## Move to a KMS provider` — BECAUSE SPEC-1 makes the rewrite an operator obligation on every deployment under the requirement — ALTERNATIVES: see non-spec.1.fix-design-G1.1 for the options the design ruled out.
WATCHOUT: "Stages 5 to 7 do not apply" must not return. A cloud-managed cluster runs Stage 7, because the CHART-1 comment tells it to set `enabled` false, and only Stages 5 and 6 are skipped. EVIDENCE: non-spec-changes.md CHART-1 values comment; DOCS-2 paragraph beginning `A cloud-managed cluster`.
WATCHOUT: a Stage 5 gate of "aescbc count must print 0" must not return. `grep -c` prints 0 on empty input, so a failed read would pass ahead of the irreversible Stage 6. "Greater than zero" is the discriminating clause. EVIDENCE: non-spec-changes.md Stage 5.
WATCHOUT: the opening paragraphs now name Stage 4 and Stage 7, so a TEST-1 ordering check that indexes on `Stage 4` text matches the wrong line. It should key on `### Stage` headings. The test row's cloud-managed assertion reads that paragraph on purpose. EVIDENCE: non-spec-changes.md TestEtcdKeyRotationRunbookMigrationStaysOutOfRotationSteps_spec_4_9 row.
FACT: the Stage 5 block omits `ETCDCTL_API=3`, which Verification Step 1 sets inside `sh -c`. etcdctl defaults to API v3 from etcd 3.4, so the block holds on every etcd the supported Kubernetes versions ship; the implementor's sample check covers this. EVIDENCE: docs/runbooks/etcd-key-rotation.md Verification Step 1.
USEFUL [non-spec.1.fix-design-G1.1]: the empty-read analysis settled the Stage 5 wording with no further derivation.

### [non-spec.1.fix-G2.1]
DECISION: the `Existing chart tests` row names the embedded-manifests sync check as the render comparison and states that `pkg/embedded/manifests/manifests.yaml` stays unedited; the summary Watch-out on unpinned keys is deleted, applied as designed — BECAUSE TestEmbeddedManifestsMatchDevProfileRender_spec_17_4 already byte-compares a dev-preset render that pins all key material, and a test owns it — ALTERNATIVES: rejected options are in the G2 design (explicit helm command, default-values profile, bare delete, reworded Watch-out).
WATCHOUT: do not restore a manual `helm template` comparison with only `etcdEncryption.aescbcKeys` pinned. The default `kms.provider: local` draws a fresh `master-key` on each offline render through `lenny-kms-master-key`, so the diff is never empty, and bare values also fail without `global.spiffeTrustDomain` and `coredns.clusterIP`. EVIDENCE: charts/lenny/templates/kms-master-key-secret.yaml, charts/lenny/values.yaml `provider: "local"`.
WATCHOUT: `make generate` rewrites `pkg/embedded/manifests/manifests.yaml`, so a regenerated file hides a render change from the sync check. That is why the row says the file stays unedited and why it stays out of the files-touched list. EVIDENCE: Makefile `generate`; tests/tier11_docs/embedded_manifests_sync_test.go.
WATCHOUT: the sync check calls `t.Skipf` when helm is not on PATH, so its discrimination needs helm present. EVIDENCE: tests/tier11_docs/embedded_manifests_sync_test.go renderDevProfile.
FACT: CHART-1 cannot reach the render: the template edit sits inside the `{{- /* */ -}}` Helm comment and the other edit is a values.yaml comment, while the `#` comment inside `encryption-config.yaml`, which does render, is left alone. Two dev-preset renders on this tree are identical and contain `lenny-etcd-encryption`. EVIDENCE: charts/lenny/templates/etcd-encryption.yaml header; pkg/embedded/manifests/manifests.yaml ConfigMap at `lenny-etcd-encryption`.
OPEN: the summary Watch-out beginning `The same comments carry stale §13.1 citations` has no antecedent in the list. It had none before this edit either, because the deleted bullet named no comments. The design did not adjudicate the site, so it stays as it is; a later round may name CHART-1 there.

### [non-spec.1.fix-G3.1]
DECISION: Decision 10's non-goal pointer reads "Decision 10 gives the reason." as the design chose — BECAUSE Decision 10 never names a chart `kms` option, so "declines it" would credit it with a statement it does not make — ALTERNATIVES: the reviewer's "Decision 10 declines it." and adding a decline sentence to Decision 10.
WATCHOUT: SPEC-3 is now the only staged print of the warning, and CODE-1 derives from it. spec-changes.md SPEC-3's "CODE-1 carries the same text with `|` unescaped" is correct and stays. Do not restore a Go block, because `TestEtcdSecretEncryptionWarningCitesT4Requirement` pins substrings only and nothing compares the whole string. EVIDENCE: non-spec-changes.md CODE-1; summary.md Watch out for.
FACT: The warning text has no double quotes, so the `\|` rule fully defines the Go literal. EVIDENCE: spec-changes.md SPEC-3 string.
OPEN: spec/04_system-components.md:2078 (LLM Reverse Proxy) cites Section 12.1 for `CredentialPoolStore`, and §12.1 does not name that store (the row is in §12.2). The citation is the same slip as summary defect 10, outside this group's fixed sites, so it is not in the defects list. The next round decides whether to file it there. EVIDENCE: spec/12_storage-architecture.md:3-18.
USEFUL [spec.1.review-client-surface.1]: its census of verbatim copies of the warning confirmed no third copy exists to reconcile when the Go block was deleted.

### [non-spec.1.fix-design-G1.1]
DECISION: Stage 5 compares two counts, Secret keys against stored values with the `k8s:enc:kms:` prefix, and passes only when they are equal and greater than zero — BECAUSE a failed `kubectl exec` or etcdctl read gives grep empty input and `grep -c` prints 0, so a gate of "aescbc count must print 0" passes on a failed read ahead of an irreversible Stage 6, and the equal count also catches `identity` values — ALTERNATIVES: the finding's `grep -ac '^k8s:enc:aescbc:'` must print 0 (fails open on an empty read); `grep -v` for non-kms lines (ciphertext contains newlines, so lines are not values).
DECISION: `grep` runs on the operator's side of `kubectl exec`, and the block sets the etcdctl flags in a shell variable, so it runs as pasted — BECAUSE it needs no shell or grep inside the etcd image, and a prose pointer to the Verification Step 1 flags leaves a block that does not run — ALTERNATIVES: `sh -c` inside the pod, which depends on what the image ships.
DECISION: a cloud-managed cluster runs Stage 4 and Stage 7 and skips Stages 5 and 6 — BECAUSE the finding's "Stages 5 to 7 do not apply" contradicts the CHART-1 comment that tells that cluster to set `enabled` false, and nothing but the ConfigMap template consumes the document, so Stage 7 is safe there — ALTERNATIVES: "unless the provider documents re-encryption" (a conditional for a rewrite that is safe to repeat); a separate `###` subsection (more text for one sentence of routing).
WATCHOUT: Stage 7's last sentence about the control-plane file goes false once a cloud-managed cluster runs Stage 7. The design deletes it, because the self-managed paragraph already says the operator maintains that file. Restoring it needs a scope. EVIDENCE: non-spec-changes.md Stage 7 and the paragraph beginning `The chart document has no`
WATCHOUT: the Stage 5 block must stay unmarked and is still run through `bash -n` by the tier 11 code-block test; the staged block passes it. EVIDENCE: tests/tier11_docs/code_blocks_test.go:208, pkg/ops/runbooks/steps.go ParseSteps
WATCHOUT: the TEST-1 ordering check should key on `### Stage` headings, because the opening paragraphs now name Stage 4 and Stage 7 by number. EVIDENCE: non-spec-changes.md TestEtcdKeyRotationRunbookMigrationStaysOutOfRotationSteps_spec_4_9 row
USEFUL [non-spec.1.review-reliability.1]: the FACT that nothing outside the template reads `lenny-etcd-encryption` settled whether Stage 7 can run on a cloud-managed cluster.
UNVERIFIED: provider-side re-encryption behaviour was not checked against current AWS, Google, and Azure documents; the design runs the rewrite on every provider, so it does not depend on the answer. The implementor checks the cloud sentence against those documents with the sample check DOCS-2 already requires.

### [non-spec.1.fix-design-G2.1]
DECISION: the `Existing chart tests` row drops the manual `helm template` comparison, and the row states that the sync check runs with `pkg/embedded/manifests/manifests.yaml` unedited, which makes that check the before-and-after render comparison; the summary Watch-out on the unpinned key is deleted — BECAUSE TestEmbeddedManifestsMatchDevProfileRender_spec_17_4 already byte-compares a dev-preset render to the committed file, so a pass before and after with the file unedited proves an empty comparison — ALTERNATIVES: the reviewer's explicit `helm template ... -f presets/dev.yaml --no-hooks` sentence restates the Makefile arguments as a manual step with no test owner; a default-values profile needs global.spiffeTrustDomain, coredns.clusterIP, aescbcKeys, and a master-key pin.
WATCHOUT: the sync check only discriminates a render change while manifests.yaml is left alone, because `make generate` absorbs any change into the committed file. EVIDENCE: Makefile:98-101; tests/tier11_docs/embedded_manifests_sync_test.go renderDevProfile.
FACT: on this tree two dev-preset renders are byte-identical with and without --no-hooks, and the --no-hooks render plus the header equals pkg/embedded/manifests/manifests.yaml; the render includes lenny-etcd-encryption, so CHART-1's template is exercised. EVIDENCE: executed helm v4.2.3 against charts/lenny; pkg/embedded/manifests/manifests.yaml:1489.

### [non-spec.1.fix-design-G3.1]
DECISION: SPEC-3 is the one printed copy of the warning; CODE-1 keeps only its derivation sentence and the Go block goes — BECAUSE the string has no double quotes, so the `\|` rule is the whole derivation — ALTERNATIVES: a byte-comparison gate (new mechanism for a one-string copy).
DECISION: the chart non-goal reads "Decision 10 gives the reason." where the finding suggested "Decision 10 declines it." — BECAUSE Decision 10 never names a chart `kms` option, so the pointer states only what the target holds and Decision 10 needs no edit — ALTERNATIVES: add a decline sentence to Decision 10 (adds text the proposal does not need).
WATCHOUT: summary Watch-out "No gate compares" stays after the trim. It is the trap that tells an implementor to copy from SPEC-3 by hand. EVIDENCE: summary.md:39.
FACT: every clause cut from Decision 7 is in the SPEC-1 paragraph, including the operator acting before any path sets T4 and the §12.5 probe scope, so the replacement loses nothing. EVIDENCE: spec-changes.md:35.

### [non-spec.1.review-applicability.1]
FACT: `helm template charts/lenny` fails on bare values (global.spiffeTrustDomain, then coredns.clusterIP are required). The dev preset renders byte-stable; a non-dev render stays random after pinning aescbcKeys, because templates/kms-master-key-secret.yaml draws `randAlphaNum 64` under the default kms.provider local. EVIDENCE: charts/lenny/templates/kms-master-key-secret.yaml:32; charts/lenny/values.yaml:917
FACT: Every DOCS-2 `### Stage` heading has no access marker, so ParseSteps skips it, and no gate requires a marker on a bash fence or restricts H2 headings (TestRunbookStructure only requires Trigger, Diagnosis, and Remediation). EVIDENCE: pkg/ops/runbooks/steps.go:54-60; tests/tier11_docs/runbooks_test.go:110
FACT: The non-spec anchors resolve once each: values.yaml comment above `enabled`, template lines 29 and 31, security-principles.md:65 and :99, security.md:164 and :495, installation.md:283, and the runbook paragraphs. Comment edits reach no embedded copy: pkg/embedded/manifests/manifests.yaml holds rendered output only. EVIDENCE: tests/tier11_docs/embedded_manifests_sync_test.go:81
WATCHOUT: tests/spec-map.json does not need the new test file; recent tier 11 files such as credentials_namespace_docs_reconciliation_test.go are absent from it. Do not file it. EVIDENCE: tests/spec-map.json

### [non-spec.1.review-citations.1]
FACT: The Storage Roles table holding the `CredentialPoolStore` row is §12.2; §12.1 is Design Principle and has no table. EVIDENCE: spec/12_storage-architecture.md:3, :7, :18
FACT: TestRunbookStructure only requires Trigger, Diagnosis, and Remediation to be present, so a new `##` section between Verification and Escalation passes, and TestDocsCodeBlocksParse only parses YAML and bash. EVIDENCE: tests/tier11_docs/runbooks_test.go:110, tests/tier11_docs/code_blocks_test.go:250
FACT: Proposal 0113 is absent from the draft-0115 tree; read it with `git show draft/0113-reader-access:proposals/...`. Its security-principles edits are at 1.3 and section 4, and its security.md edit is Platform Roles, so the Impacts row stands. EVIDENCE: 0113 non-spec-changes.md:382, :396, :397

### [non-spec.1.review-client-surface.1]
FACT: 0115 changes no REST, OpenAPI, MCP, proto, JSONL, CRD, or SDK contract. The operator-read surfaces it changes are the preflight warning string (no copy beyond §17.6 and the Go constant), chart comments (the template one sits in a Helm comment and is not rendered, so pkg/embedded/manifests/manifests.yaml is unaffected), and docs. values.schema.json types etcdEncryption as a bare object, so no description mirrors the comment. — EVIDENCE: charts/lenny/values.schema.json:100; charts/lenny/templates/etcd-encryption.yaml:42
FACT: ParseSteps stops a step only at `### ` and drops headings with no access marker, so the DOCS-2 `### Stage` headings add nothing to GET /v1/admin/runbooks/{name}/steps. No tier 11 test pins a fixed `## ` section list for runbooks. — EVIDENCE: pkg/ops/runbooks/steps.go:39-82; tests/tier11_docs/runbook_steps_test.go:50
WATCHOUT: the "credential pools are KMS-encrypted" claim has four carriers, not two: docs/index.md:218,233, README.md:132,147, and docs/assets/diagrams/credential-delivery.svg:32. Earlier DEFERRED and OPEN entries named only docs/index.md. — EVIDENCE: grep of `credential pools (KMS)`, `encrypted with a cloud KMS`, and `KMS-encrypted`

### [non-spec.1.review-docs-alignment.1]
WATCHOUT: the false KMS claim for pool credentials also sits outside the operator guide, on the landing page and in the root README, in prose, the ASCII fallback, and an SVG. A grep for `etcd` or `aescbc` misses all of them. EVIDENCE: docs/index.md:233, docs/index.md:218, docs/assets/diagrams/credential-delivery.svg:32, README.md:147, README.md:132.
FACT: a runbook section with `###` headings and an unmarked ```bash fence passes the runbook lints: ParseSteps keeps only headings that carry access markers, and tests/tier11_docs/runbooks_test.go checks front matter and required sections. EVIDENCE: pkg/ops/runbooks/steps.go:52-61.
FACT: the application-layer claims in DOCS-1 hold for both named items: callbackSecret is KMS-sealed (pkg/gateway/sessionserver/envelope.go:220-235) and user-scoped credentials sit in TokenStore under envelope encryption (spec/04_system-components.md, Three Credential Modes). EVIDENCE: as cited.

### [non-spec.1.review-edit-sites.1]
USEFUL [non-spec.1.review-client-surface.1]: the four-carrier WATCHOUT holds; a fifth carrier of the same false claim is docs/runbooks/token-service-outage.md:32 ("decrypting Secrets with KMS").
FACT: every identifier 0115 touches has its authoring site staged: the warning text has only the SPEC-3 row and the Go constant, `etcdEncryption` has no docs/reference row and no schema description, and values and Helm-comment changes reach no generated artifact. EVIDENCE: charts/lenny/values.schema.json:100; git grep 'does not encrypt Secrets in etcd'.
WATCHOUT: the review-log DEFERRED entry on `credential_pools.raw_secret` was not closed by the between-loops pass; it still needs a summary defects entry. EVIDENCE: spec/25_agent-operability.md:4037,4062; charts/lenny/values.yaml:4141.

### [non-spec.1.review-fresh.1]
FACT: tests/tier11_docs is outside `componentAndAboveTierDirs`, so the new TEST-1 file needs no tests/spec-map.json entry for `lenny-test validate-maps`. EVIDENCE: cmd/lenny-test/cmd_validate.go:126-139
FACT: callbackSecret is KMS-envelope ciphertext in the sessions table, so the DOCS-1 application-layer claim for webhook callback secrets holds. EVIDENCE: spec/14_workspace-plan-schema.md:127; pkg/gateway/session/sessionstore/pgstore/pgstore.go:1987-1996
WATCHOUT: the runbook-body claim "decrypting Secrets with KMS" at docs/runbooks/token-service-outage.md:32 is a further carrier of the false pool-KMS claim, beside docs/index.md, README.md, and the diagram SVG. EVIDENCE: docs/runbooks/token-service-outage.md:32
USEFUL [spec.1.review-edit-sites.1]: the docs/index.md lead still stands and routes here.

### [non-spec.1.review-kubernetes.1]
FACT: The non-spec staging adds no controller, status write, finalizer, webhook, or hot-path call; the only Kubernetes mechanism is the operator runbook migration. Its provider order (kms second, promote, rewrite, remove aescbc, chart disable last) is sound against the apiserver's encrypt-first, decrypt-in-order rule, and no chart resource-policy keeps the ConfigMap, so `enabled: false` does delete it. EVIDENCE: charts/lenny/templates/etcd-encryption.yaml:42-94
FACT: No ValidatingWebhookConfiguration or ValidatingAdmissionPolicy in the chart matches Secrets, so `kubectl replace` over Secrets meets no Lenny admission check. EVIDENCE: grep over charts/lenny/templates
WATCHOUT: Stage 5 samples one Secret while Stage 6 is irreversible for an unpinned key once Stage 7 deletes the ConfigMap; a whole-prefix check is the gate. EVIDENCE: docs/runbooks/etcd-key-rotation.md:191-203
FACT: §4.9's EKS Verify command omits the required `--name`, and DOCS-2 copied it; `aws eks describe-cluster --query ...` fails at argument parsing. EVIDENCE: spec/04_system-components.md Credential Storage Topology EKS row
USEFUL [spec.4.review-kubernetes.1]: the no-controller-surface FACT holds for the non-spec staging as well.

### [non-spec.1.review-mechanism.1]
FACT: `helm template` on charts/lenny with only `etcdEncryption.aescbcKeys` pinned is not deterministic on the default profile: `lenny-kms-master-key` redraws `master-key` on each offline render. The default profile also needs `global.spiffeTrustDomain` and `coredns.clusterIP` to render at all. `-f charts/lenny/presets/dev.yaml --no-hooks` renders byte-identically across runs. EVIDENCE: charts/lenny/templates/kms-master-key-secret.yaml:32; charts/lenny/presets/dev.yaml:182; two renders diffed on this tree.
FACT: `ParseSteps` carries the last step past a `## ` heading, but a fence only joins a step when an access path is open, so the marker-free stages and the Stage 2 bash block add nothing to Verification Step 3. EVIDENCE: pkg/ops/runbooks/steps.go:63-83.
FACT: tier 11 sits outside `componentAndAboveTierDirs`, so the new tier 11 test file needs no tests/spec-map.json entry for validate-maps. EVIDENCE: cmd/lenny-test/cmd_validate.go:126-140.
FACT: no test pins any string DOCS-1 or DOCS-2 replaces, and TestRunbookStructure requires only Trigger, Diagnosis, and Remediation, so the new `##` section passes. EVIDENCE: tests/tier11_docs/runbooks_test.go:110.
OPEN: the raw_secret DEFERRED and OPEN entries for spec/25 (ledger [spec.1.review-edit-sites.1] successor) are still absent from the summary defects list; the pass between loops should add or drop them.

### [non-spec.1.review-plan-conformance.1]
USEFUL [spec.4.review-plan-conformance.1]: the plan's whole assignment is still the owner decision bullet and the queue item; a keyword sweep of the plan for kms, etcd, aescbc, and credential Secrets found no further deliverable for F-12.9.24.
CORRECTS [spec.1.review-plan-conformance.1]: its Decision numbers are stale. Decision 4 now records the absence of a production conjunct, and the no-gate decision is Decision 9; the literal `kms` provider reading is covered by Decision 5 and SPEC-2.

### [non-spec.1.review-reliability.1]
FACT: `kubectl replace` over a listed Secret set keeps resourceVersion, so a concurrent gateway write yields a conflict rather than a lost update, and that Secret is already under `kms` after Stage 3. A partial rewrite comes from a failure other than a conflict, such as a timeout or a skipped namespace in the unchecked loop. EVIDENCE: docs/runbooks/etcd-key-rotation.md:159,166-168
FACT: No Lenny webhook or Role blocks an admin rewrite of Secrets in the credentials namespace, and nothing in pkg/ or cmd/ other than value typing and embedded manifests reads `lenny-etcd-encryption`, so Stage 7 breaks no consumer. EVIDENCE: charts/lenny/templates/admission-policies/, pkg/chart/values/values.go:109
WATCHOUT: `ParseSteps` flushes on `### ` only and drops a heading with no access marker, so the unmarked `### Stage` headings stay out of the step list as proposed. A Stage 5 sweep command must stay unmarked for the same reason. EVIDENCE: pkg/ops/runbooks/steps.go:54-60,67-70

### [non-spec.1.review-security.1]
FACT: `CheckSecretEncryption` reads only the operator flag `--secret-encryption-verified`, so it is an attestation and does not falsify "no Lenny component can read the etcd encryption provider". EVIDENCE: cmd/lenny-tier-promote/main.go:46; pkg/tierpromotion/checks.go:150-175
FACT: the `lenny-etcd-encryption` ConfigMap carries no `helm.sh/resource-policy: keep`, so DOCS-2 Stage 7's "the upgrade removes the ConfigMap" holds. EVIDENCE: charts/lenny/templates/etcd-encryption.yaml:66-75
FACT: the DOCS-1 database-role sentence holds for `callbackSecret`: §14 states it is envelope-sealed and the gateway seals it. EVIDENCE: spec/14_workspace-plan-schema.md:127; cmd/lenny-gateway/sessiondeps.go:120-138

### [non-spec.1.review-single-source.1]
FACT: The CODE-1 Go block and the SPEC-3 Failure Message are byte-identical today after unescaping `\|`, so the copy is a drift risk with no current disagreement. EVIDENCE: non-spec-changes.md CODE-1 block; spec-changes.md SPEC-3 block.
WATCHOUT: The summary's Watch out list is where staged constraints get restated (stage order, ParseSteps headings, pinned key, one physical line). A fixer adding a constraint to a deliverable should not add it there too. EVIDENCE: summary.md:39-41.
FACT: The current runbook names no KMS-backed list, so the two lists in DOCS-1 and DOCS-2 are both new text; no other docs page lists the options. EVIDENCE: docs/runbooks/etcd-key-rotation.md intro; lookup for `GKE CMEK` hits pkg/tierpromotion only.

### [non-spec.1.review-test-coverage.1]
FACT: the Testing row's pinned-key render comparison holds under `-f charts/lenny/presets/dev.yaml`: two offline renders were identical. A bare `helm template` of the chart fails on required values (`global.spiffeTrustDomain`, `coredns.clusterIP`), and `now` in phase-stamp-configmap.yaml reaches the output only when a feature flag is enabled. EVIDENCE: charts/lenny/templates/phase-stamp-configmap.yaml:58
FACT: the CHART-1 template text lands inside a `{{- /* */ -}}` comment and the values text is a YAML comment, so neither renders and the embedded sync test needs no regeneration. EVIDENCE: charts/lenny/templates/etcd-encryption.yaml:1
FACT: no tier-11 test imports pkg/preflight today, so a spec-row comparison of EtcdSecretEncryptionWarning is a new pattern for the suite. EVIDENCE: grep of tests/tier11_docs for pkg/preflight
FACT: the new runbook section's fence in Stage 2 is harmless to ParseSteps, because a fence with no access marker leaves `path` nil. EVIDENCE: pkg/ops/runbooks/steps.go:64-84

### [non-spec.2.review-applicability.1]
FACT: The chart sync check can stand in for the render comparison because charts/lenny/presets/dev.yaml pins etcdEncryption.aescbcKeys, and the CHART-1 text sits in a values.yaml comment and a `{{- /* */ -}}` block, neither of which renders. EVIDENCE: charts/lenny/presets/dev.yaml:233-246; tests/tier11_docs/embedded_manifests_sync_test.go renderDevProfile
FACT: TestRunbook step validation only requires one or more parsed steps per runbook, and a `### Stage N` heading with no access marker is dropped by ParseSteps, so the new section adds no step. EVIDENCE: pkg/ops/runbooks/steps.go flushStep; tests/tier11_docs/runbook_steps_test.go:50
WATCHOUT: The new cloud-managed paragraph names `Stage 4`, `Stage 7`, and "rewrite" ahead of the stage headings, so the TEST-1 ordering assertion should locate the `### Stage N` headings rather than the first occurrence of a word. EVIDENCE: non-spec-changes.md:115,188

### [non-spec.2.review-citations.1]
FACT: the Testing row's claim that the sync check is the render comparison holds: TestEmbeddedManifestsMatchDevProfileRender_spec_17_4 is a byte comparison of `helm template --no-hooks -f charts/lenny/presets/dev.yaml`, and dev.yaml pins the aescbc key, so CHART-1 comments cannot move it. EVIDENCE: tests/tier11_docs/embedded_manifests_sync_test.go:88-96, charts/lenny/presets/dev.yaml:245-248
WATCHOUT: a glob of `*spec-changes.md` in the proposal directory also matches the non-spec file, and non-spec sorts first. Name the spec file in full. EVIDENCE: proposal directory listing

### [non-spec.2.review-kubernetes.1]
FACT: The Stage 5 `etcdctl` block carries no access marker, and ParseSteps collects commands only when a marker opened a path, so its fence does not join the rotation step list. EVIDENCE: pkg/ops/runbooks/steps.go:64-82
FACT: The embedded-manifests sync check renders the dev preset, which pins the aescbc key, so it is deterministic and serves as the CHART-1 render comparison when helm is present (it skips without helm). EVIDENCE: tests/tier11_docs/embedded_manifests_sync_test.go:57-76; charts/lenny/presets/dev.yaml:245-248

### [non-spec.2.review-mechanism.1]
FACT: The embedded sync check compares a dev-profile render, and dev.yaml pins etcdEncryption, so the check is deterministic and the rendered ConfigMap is in it; the template header is a `{{- /* */ -}}` comment and does not render, so CHART-1 cannot change manifests.yaml. — EVIDENCE: tests/tier11_docs/embedded_manifests_sync_test.go:66-70, charts/lenny/presets/dev.yaml:245, charts/lenny/templates/etcd-encryption.yaml:1
WATCHOUT: Stage 5 takes two separate etcd reads, so a Secret created between them can offset one unrewritten Secret. Not filed: it needs that second fault inside a seconds-long window. — EVIDENCE: non-spec-changes.md:161-162

### [non-spec.2.review-reliability.1]
FACT: the embedded-manifests sync check is a valid no-change render comparison because charts/lenny/presets/dev.yaml pins the aescbc key, so the CHART-1 test row holds with no extra pin — EVIDENCE: tests/tier11_docs/embedded_manifests_sync_test.go:59-78, pkg/embedded/manifests/manifests.yaml:1519
WATCHOUT: Stage 5's two counts are separate reads, so a Secret created between them can offset one unrewritten Secret. Not filed: it needs a compensating create in that gap plus a Stage 4 miss — EVIDENCE: non-spec-changes.md Stage 5
UNVERIFIED: whether the namespace-by-namespace form of Remediation Step 4 errors on a namespace holding no Secrets (kubectl was not run); the staged cloud-managed "repeating it until the rewrite reports no error" would loop on it. Implementor should check when landing DOCS-2.

### [non-spec.2.review-single-source.1]
FACT: TestEmbeddedManifestsMatchDevProfileRender diffs a fresh `helm template --no-hooks` dev-profile render against the committed manifests, so CHART-1's "the sync check is the render comparison" holds for a comment-only change; it skips when helm is absent. EVIDENCE: tests/tier11_docs/embedded_manifests_sync_test.go:57-70

### [non-spec.3.fix-G1.1]
DECISION: replaced the DOCS-1 sentence in place with `Audit logs in Postgres rely on storage-layer encryption at rest.`, as designed; Decision 17, the first sentence's list, and TEST-1 stay as they are — BECAUSE only the universal was false and Decision 17 is true without it — ALTERNATIVES: see non-spec.3.fix-design-G1.md.
FACT: audit_log's columns are plaintext JSONB and metadata, and no migration touching audit_log has a ciphertext, envelope, or encrypted hit, so the narrowed sentence holds. EVIDENCE: migrations/0001_initial_schema.up.sql CREATE TABLE audit_log
FACT: the related lookup's hits at docs/operator-guide/installation.md AWS and GCP rows describe the recommended managed-database configuration and make no claim about which data the application seals, so this edit does not touch them.
USEFUL [non-spec.3.fix-design-G1.1]: the quote, term, and `in Postgres` facts held and saved the re-derivation.
FACT: the §12.9 default mapping places audit events at T2 and T3, whose encryption-at-rest cells require storage-layer protection, so the staged sentence agrees with the classification table. §12.3 mentions "any T4 — Restricted audit events", and no default-mapping row assigns audit data to T4. EVIDENCE: spec/12_storage-architecture.md §12.9 default mapping rows, §12.3 **Default: synchronous writes for T3/T4 audit events (unconditional).**

### [non-spec.3.fix-G2.1]
DECISION: CHART-1 keeps the values.yaml comment as the one chart-level statement of the self-managed path, and the template header carries one pointer to it — BECAUSE the operator edits the flag in values.yaml and the header's REQUIRED step still needs a stated exit — ALTERNATIVES: the rejected options are in the design: deleting the template paragraph, keeping `This document renders aescbc only.`, inverting the two sites, and a bare §4.9 pointer.
WATCHOUT: do not cut the set-false ordering or its key-loss clause from the values.yaml comment on the grounds that §4.9 is the home. SPEC-2 states the path and does not state the ordering. EVIDENCE: spec-changes.md SPEC-2 **KMS-backed configurations.**; non-spec-changes.md CHART-1.
FACT: no test or doc reads the chart comment text: `renders aescbc only`, `OPERATOR STEP`, and `documentation of intent` have no hit in tests, pkg, cmd, scripts, charts/lenny/tests, or docs. EVIDENCE: grep over those trees at this round.

### [non-spec.3.fix-design-G1.1]
DECISION: replace the DOCS-1 sentence with `Audit logs in Postgres rely on storage-layer encryption at rest.` and leave Decision 17, the first sentence's list, and TEST-1 alone — BECAUSE removing the universal closes the contradiction and Decision 17 is true once it goes — ALTERNATIVES: enumerating leases, connector credentials, and the erasure salt in the first sentence (a partial list that goes stale, and the sentence is not read as exhaustive); a test assertion on the phrase (pins a string that never shipped).
FACT: audit_log carries no ciphertext column and pkg/audit plus pkg/gateway/audit do no envelope sealing; their `envelope` hits are CloudEvents and their KMS hits are redaction-receipt signatures, so the audit half of the sentence holds. EVIDENCE: migrations/0001_initial_schema.up.sql:130; pkg/audit/chain.go:83-97; pkg/gateway/audit/auditstore/publish.go:21
FACT: the spec's term is `storage-layer` (and `volume-level` in its definition); `storage-level encryption` has no occurrence in spec/ or docs/. EVIDENCE: spec/12_storage-architecture.md **T2 storage-layer encryption definition.**
WATCHOUT: keep `in Postgres` in the sentence. Audit events can also stream to an external sink, including append-only object storage, whose encryption Lenny does not set. EVIDENCE: spec/11_policy-and-controls.md §11.7 SIEM connectivity validation item
USEFUL [non-spec.3.review-fresh.1]: the three envelope stores it named all verified at the cited migrations.

### [non-spec.3.fix-design-G2.1]
DECISION: the CHART-1 template paragraph becomes one pointer to the etcdEncryption.enabled comment, and the values.yaml comment is the one chart-level statement — BECAUSE the template and values comments are read together and only values.yaml can sit beside the flag — ALTERNATIVES: deleting the paragraph (the header's "OPERATOR STEP — REQUIRED" would then have no exit for a self-managed T4 cluster, which decision 10 says the comments must stop omitting); keeping `This document renders aescbc only.` as the finding suggested (the header's first paragraph already names the aescbc provider and the rendered document sits beneath it).
WATCHOUT: the template paragraph is the only CHART-1 text naming the runbook section. values.yaml says "after the procedure in docs/runbooks/etcd-key-rotation.md", and that runbook's main procedure is the key rotation, while Stage 7 is itself the set-false step, so "after" is also imprecise. Dropping the template paragraph without moving the section name and "as the last step" into values.yaml loses the routing. EVIDENCE: non-spec-changes.md CHART-1; DOCS-2 Stage 7; docs/runbooks/etcd-key-rotation.md headings.
FACT: no test, gate, or doc reads the text of the etcd-encryption.yaml header or the etcdEncryption.enabled comment; the hits under tests/ and charts/lenny/tests are render tests and spec-map entries, so the rewording needs no test edit. EVIDENCE: tests/tier5_e2e_kind/etcd_encryption_test.go:203-220, tests/spec-map.json:122.

### [non-spec.3.review-applicability.1]
FACT: The runbook gates accept the new `## Move to a KMS provider` section: TestRunbookStructure only requires Trigger, Diagnosis, and Remediation to be present, and no test requires an access marker on a bash block outside a step. ParseSteps drops `###` headings that carry no marker. EVIDENCE: tests/tier11_docs/runbooks_test.go:110, pkg/ops/runbooks/steps.go:52-60
FACT: `docs`, `test`, and `code` are all valid checklist lanes, so S5 to S8 dispatch. EVIDENCE: .claude/skills/implement-proposal/SKILL.md "One execution sequence"
FACT: The sync check renders the dev preset, which pins the aescbc key, so a chart comment change produces no manifests.yaml difference. The ConfigMap has no resource-policy keep annotation, so `enabled: false` does remove it. EVIDENCE: charts/lenny/presets/dev.yaml:233-248, charts/lenny/templates/etcd-encryption.yaml:66-75

### [non-spec.3.review-citations.1]
FACT: `ParseSteps` skips a `### ` heading with no access marker, and a fence with no marker adds no command, so the new runbook section stays out of the step list as staged. Neither TestRunbookStructure nor TestEveryRunbookProducesSteps rejects an unmarked fence or an extra `## ` section. EVIDENCE: pkg/ops/runbooks/steps.go:53-60, tests/tier11_docs/runbooks_test.go:103, tests/tier11_docs/runbook_steps_test.go:24
FACT: No test under tests/tier11_docs mentions etcd, and the existing preflight tests assert only fragments that the SPEC-3 text keeps, so CODE-1 passes them unedited. EVIDENCE: pkg/preflight/install_prereqs_test.go:208-229
FACT: proposals/0113_* is absent from this worktree. It lives in the draft-0113 worktree, and its non-spec staging does edit security-principles.md and security.md in other paragraphs, so the impacts row holds. EVIDENCE: draft-0113 0113...non-spec-changes.md:396-397,505

### [non-spec.3.review-client-surface.1]
FACT: The new `## Move to a KMS provider` section leaves `GET /v1/admin/runbooks/{name}/steps` unchanged. `ParseSteps` drops a `### ` heading with no access path, and the section's intro has no fence that could attach to the open Verification Step 3 path. EVIDENCE: pkg/ops/runbooks/steps.go:53-60, pkg/ops/runbooks/steps.go:66-69
FACT: docs/runbooks/etcd-key-rotation.md has no bundled copy in charts or pkg, and the openapi.json `workspaceTier` entries and the Runtime CRD description make no at-rest claim about pool secrets, so neither needs a mirror edit. EVIDENCE: pkg/gateway/externalapi/openapi/openapi.json:262,278; charts/lenny/crds/lenny.dev_runtimes.yaml:589-599
USEFUL [non-spec.1.review-client-surface.1]: its census of operator-read surfaces still holds at round 3.

### [non-spec.3.review-docs-alignment.1]
FACT: 436 of 437 ```bash/```sh blocks under docs/runbooks carry an access marker; the exception is token-store-unavailable.md:62. No gate enforces this, so the convention lives only in prose. EVIDENCE: docs/runbooks/index.md:240, spec/25_agent-operability.md:3074
WATCHOUT: the DOCS-2 marker-free stages pass every lint but contradict the prose convention in docs/runbooks/index.md "Access markers.", which DOCS-2 does not stage. EVIDENCE: non-spec-changes.md DOCS-2 "New section"
FACT: the `lenny-etcd-encryption` ConfigMap has no `helm.sh/resource-policy: keep`, so Stage 7's claim that the upgrade removes it holds. EVIDENCE: charts/lenny/templates/etcd-encryption.yaml metadata
FACT: the existing fragment assertions in install_prereqs_test.go still hold against the SPEC-3 text. EVIDENCE: pkg/preflight/install_prereqs_test.go:210-212

### [non-spec.3.review-edit-sites.1]
FACT: `.Values.etcdEncryption` is read only by the etcd-encryption template and its chart test, so no chart guard, preflight, or Go path blocks `enabled: false` on the T4 path. EVIDENCE: git grep etcdEncryption over charts/lenny/templates, pkg, cmd, and tests; pkg/chart/values/values.go:109 is an untyped passthrough.
FACT: the DOCS-2 samples pass TestDocsCodeBlocksParse by reading: YAML parses, and `bash -n` takes `<etcd-pod>` as redirections, as the existing runbook blocks do. The `.md` runbook link in DOCS-1 matches the seven existing `.md` runbook links. EVIDENCE: tests/tier11_docs/code_blocks_test.go:106-112; docs/operator-guide/configuration.md:439.
WATCHOUT: docs/operator-guide/security-principles.md:149 ("KMS-protected secrets") and :193 are compliance-mapping cells of the same class as the landing-page carriers already refuted on materiality; do not re-file them. EVIDENCE: non-spec.r3.history.md:81.

### [non-spec.3.review-fresh.1]
FACT: Postgres holds application-layer envelope ciphertext beyond the three items DOCS-1 names: credential_leases.lease (migration 0129, KEK platform:credential-leases), connector_credentials (0048), and tenants.erasure_salt (0133). A docs sentence that says all other Postgres data is storage-level only is false. EVIDENCE: migrations/0129_credential_leases_envelope.up.sql:1-20; migrations/0048_connector_credentials.up.sql:1-8; migrations/0133_tenant_erasure_salt.up.sql:1-5
FACT: ParseSteps flushes on `### ` only, and only headings with an access marker become steps, so the new unmarked Stage sections and their bash blocks add no step; no tier 11 test requires a marker on every bash block. EVIDENCE: pkg/ops/runbooks/steps.go:39-85
FACT: Nothing in code consumes the lenny-etcd-encryption ConfigMap, so Stage 7 breaks no check; CheckSecretEncryption reads an operator attestation. EVIDENCE: pkg/tierpromotion/checks.go:150-175

### [non-spec.3.review-kubernetes.1]
FACT: tested on the envtest kube-apiserver 1.31.0 with RBAC on and etcd on loopback. `/readyz?verbose` lists no kms line under an aescbc-only EncryptionConfiguration, and lists `kms-providers` when a `kms` provider is configured, in second position or on `configmaps` only. An anonymous GET of `/readyz?verbose` returned 200 on the aescbc config. So absence is a negative signal on a self-managed apiserver; presence does not prove `secrets` coverage or write position. EVIDENCE: local probe of /home/ec2-user/.local/share/kubebuilder-envtest/k8s/1.31.0-linux-arm64/kube-apiserver, no repo file.
CORRECTS [spec.1.review-kubernetes.1]: the `/readyz?verbose` OPEN is now settled for self-managed 1.31; it is no longer "may expose". Managed-cluster behaviour stays unverified.
FACT: a `kms` provider whose plugin socket is down makes `/readyz` return 500, with a second-position provider too, so Stage 2's Ready gate does detect a missing plugin at Stage 1. EVIDENCE: same probe.
WATCHOUT: "cannot verify the provider" (DOCS-1) stays true because no positive proof exists; "can read" (SPEC-1) and "cannot observe" (Decisions 1 and 7) overclaim. The narrow fix is wording plus a filed defect, and it adds no check.
UNVERIFIED: whether EKS, GKE, and AKS list `kms-providers`; a gate that fails on absence needs this checked first.

### [non-spec.3.review-mechanism.1]
FACT: The Stage 5 counts are sound: etcdctl prints each value on its own line, binary aescbc values never add a `k8s:enc:kms:` line under `grep -a`, and scripts/check-doc-examples.sh validates only docs/examples JSON, so the new runbook fences face no lint. EVIDENCE: scripts/check-doc-examples.sh:1-12; non-spec-changes.md Stage 5.
FACT: The summary's kmsProbe and T4 citations hold: tenants.go:1455 and bootstrap.go:524 are the only ProbeAvailability calls in the admin package, and the other kmsProbe uses pass the value as an argument. EVIDENCE: pkg/gateway/externalapi/admin/tenants.go:1252,1455,1541; bootstrap.go:524.
UNVERIFIED: The Stage 1 sample and the etcdctl output format were checked from memory of upstream, not by running them; DOCS-2 already tells the implementor to check the sample against upstream docs.

### [non-spec.3.review-plan-conformance.1]
USEFUL [non-spec.1.review-plan-conformance.1]: rerun of the plan keyword sweep (0115, aescbc, etcd encryption, EncryptionConfiguration, F-12.9.24, kms provider) still finds only the owner decision and the queue item as assignments for F-12.9.24; the round's fixes moved no deliverable out of the staging.

### [non-spec.3.review-reliability.1]
FACT: No Lenny component watches Secrets, and no webhook rule matches them, so the mass rewrite in Stage 4 triggers no Lenny reaction. EVIDENCE: lookup for Secret watch/informer over pkg/gateway, pkg/credentials, pkg/controller, and pkg/ops returned nothing; charts/lenny/templates grep for "secrets" rules shows RBAC only.
FACT: Nothing reads the lenny-etcd-encryption ConfigMap or etcdEncryption.enabled at runtime; tier promotion's SecretEncryptionVerified is an operator flag, so Stage 7 breaks no consumer. EVIDENCE: cmd/lenny-tier-promote/main.go:90.
WATCHOUT: The Stage 5 counts come from two etcdctl reads at different revisions, so a Secret created between them cancels one un-rewritten Secret. Any rewrite of that gate must keep `--prefix --print-value-only` and `k8s:enc:kms:`, which TEST-1 asserts.

### [non-spec.3.review-security.1]
FACT: `CheckSecretEncryption` reads only the operator attestation `SecretEncryptionVerified` (flag in cmd/lenny-tier-promote/main.go), so it never observes the provider. `etcdEncryption.enabled: false` does not fail it, and SPEC-1's "no Lenny component can read the etcd encryption provider" holds. — EVIDENCE: pkg/tierpromotion/checks.go:150-175
FACT: no Go code outside tests names the `lenny-etcd-encryption` ConfigMap, so runbook Stage 7 removes nothing a Lenny component reads. — EVIDENCE: grep of pkg and cmd
WATCHOUT: `T4DefaultEncryptionCheck.ServesT4Tenant` looks like an available T4 signal for a fail-closed etcd gate. It is an unwired object-storage flag, and the summary non-goals and defect 2 already decline it. Do not refile it. — EVIDENCE: cmd/lenny-preflight/main.go:625; pkg/preflight/t4_default_encryption.go:30-33

### [non-spec.3.review-single-source.1]
FACT: The doc-only restatements (KMS-backed list in security.md and the runbook, T4 condition across runbook sections) were refuted on materiality in earlier rounds; the skeptic does not apply the one-restatement limit to reader-facing pages, so re-filing them wastes verification. EVIDENCE: scratchpad/cp-log/0115/history/non-spec.r3.history.md:63-64
WATCHOUT: The chart comments are not reader-facing docs, so that refutation does not cover them; CHART-1 writes the self-managed path out at both values.yaml and the template header beside its §4.9 home. EVIDENCE: non-spec-changes.md:24-33, :39-43; spec-changes.md:49

### [non-spec.3.review-test-coverage.1]
FACT: the template header in charts/lenny/templates/etcd-encryption.yaml is a `{{- /* */ }}` Helm comment, so CHART-1 cannot reach the render, and TestEmbeddedManifestsMatchDevProfileRender_spec_17_4 compares against the dev preset by bytes. It skips when helm is not on PATH. EVIDENCE: tests/tier11_docs/embedded_manifests_sync_test.go:60-62
FACT: all eleven Diagnosis, Remediation, and Verification headings in the runbook carry access markers, so "exactly those titles" is a satisfiable assertion, and ParseSteps ignores a `## ` heading, so the `### Stage` flush is what the new test guards. EVIDENCE: pkg/ops/runbooks/steps.go:66-70
FACT: no existing test or generated page pins the runbook introduction, its symptoms list, or the warning's old cloud sentence; docs/runbooks/index.md has no per-runbook symptom table. Only the constant, by reference, reaches pkg/preflight/run_test.go.
