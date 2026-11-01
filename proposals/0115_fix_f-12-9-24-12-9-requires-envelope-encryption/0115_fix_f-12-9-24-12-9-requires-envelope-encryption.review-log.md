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
