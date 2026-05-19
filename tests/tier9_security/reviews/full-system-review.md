# Full-System Security Design Review

> Phase 14 hardening validation per spec §18.33. This review walks the
> §13 security model end to end against the implemented codebase.
> Recorded findings link to the commits where the control already
> exists, or name the wave that will resolve them.

## Review record

| Field | Value |
| --- | --- |
| Review date | 2026-05-19 |
| Phase | 14, the spec §18.33 hardening phase |
| Scope | The §13 security model in full: pod security (§13.1), network isolation (§13.2), credential flow (§13.3), upload and archive validators (§13.4), delegation-chain content security (§13.5), mTLS / SPIFFE identity, admission webhooks, audit-chain integrity, tenant isolation, SSRF and callback validation, and image signing. |
| Method | Code review against `pkg/`, the admission webhooks under `pkg/admission/`, and the Helm chart under `charts/lenny/`. Each finding cites the file and function it is grounded in. |
| Prior review | The §13.3 credential subsystem received a targeted review in Phase 5.6 (`credential-review.md`). Findings here that touch the credential path cross-reference that record rather than duplicate it. |

## How this file is consumed

`phase-14-gate` (groups.yaml) runs the tier-9 security suites and the
static linters. The gate pins this artifact in place so the
full-system review surface is discoverable; it does not parse the
finding list. A finding marked resolved names the commit where the
control lives. A finding marked tracked names the wave that closes it.

## Coverage summary

The review walks each §13 area and records whether the control is
implemented, partially implemented, or absent.

| §13 area | Primary implementation | State |
| --- | --- | --- |
| §13.1 pod security | `pkg/podsecurity`, `pkg/admission/webhook/pod_security.go` | Implemented |
| §13.1 ephemeral-container cred guard | `pkg/admission/ephemeral_container_cred_guard`, `pkg/admission/webhook/ephemeral_container_cred_guard.go` | Implemented |
| §13.2 network isolation | `charts/lenny/templates` NetworkPolicies, `pkg/preflight` selector audits | Implemented |
| §13.3 credential flow | `pkg/tokenexchange`, `pkg/credential`, `pkg/gateway/credentialstore` | Implemented, with tracked items from the Phase 5.6 review |
| §13.4 upload and archive validators | `pkg/upload` | Implemented |
| §13.5 delegation-chain content security | `pkg/gateway/delegationpolicystore`, `pkg/gateway/interceptor` | Implemented (advisory hooks; platform classifier-free by design) |
| mTLS / SPIFFE | `pkg/mtls/spiffe`, `pkg/mtls/denylist` | Implemented |
| Admission webhooks | `pkg/admission/`, `charts/lenny/templates/admission-policies` | Implemented |
| Audit-chain integrity | `pkg/audit` | Implemented |
| Tenant isolation | `pkg/audit/chain.go`, `pkg/tokenexchange` | Implemented |
| SSRF and callback validation | `pkg/gateway/connectorstore`, §13.2 egress `except` blocks | Implemented |
| Image signing | `pkg/admission/cosign_verify`, `.github/workflows/release.yml` | Implemented |

## Findings

Each finding lists a short title, a severity, the affected component,
a description, and a remediation disposition. A resolved finding cites
the commit where the control exists. A tracked finding names the wave
that will close it.

### Finding 1 — Pod-spec host-sharing flags rejected at admission

**Severity:** Informational

**Affected:** `pkg/podsecurity/podsecurity.go` (`ValidateAgentPod`); `pkg/admission/webhook/pod_security.go`; `charts/lenny/templates/admission-policies/pod-security-webhook.yaml`.

§13.1 forbids `shareProcessNamespace`, `hostPID`, `hostNetwork`, and `hostIPC` on every pod template Lenny generates. `ValidateAgentPod` checks all four `PodSpec` fields and appends a `POD_SPEC_HOST_SHARING_FORBIDDEN` violation for each that is set. The validator also enforces the pod-level `fsGroup` equals the `lenny-cred-readers` GID, `runAsNonRoot` is true, and the per-container invariants (`allowPrivilegeEscalation` false, `privileged` false, `readOnlyRootFilesystem` true, `capabilities.drop` contains `ALL`, `capabilities.add` empty, and the effective seccomp profile is `RuntimeDefault`). The Kubernetes admission webhook wraps the validator and the `lenny-preflight` Job runs it against every Lenny-managed Deployment, DaemonSet, and Job at install. The control matches §13.1 with no gap observed.

**Remediation:** Resolved. The validator landed in `pkg/podsecurity` (fuzz scaffold `3957b4e`) and the wrapping webhook in `50f609f` ("admission: cosign image-signing and pod-security webhooks").

### Finding 2 — Concurrent-workspace pools share a credential-file read boundary

**Severity:** Low

**Affected:** spec §13.1 (concurrent-workspace mode credential-read scope); `pkg/podsecurity/podsecurity.go` (the `lenny-cred-readers` GID is pod-level).

In `executionMode: concurrent`, `concurrencyStyle: workspace` pools, multiple slots share one pod, one agent UID, and therefore one `lenny-cred-readers` group membership. Every slot's per-slot credential file is group-readable by every other slot's agent code. §13.1 documents this as an accepted property of process-level co-tenancy: it is covered by the deployer's `acknowledgeProcessLevelIsolation` flag and surfaced by the `ConcurrentWorkspaceCredentialSharing=True` pool condition. The `pkg/podsecurity` validator operates on a single pod-level `fsGroup` and does not model per-slot GIDs, which is consistent with the spec's decision that per-slot tmpfs mounts with distinct GIDs are absent in v1. This is a documented residual exposure rather than a defect: a deployer that requires strict per-slot credential isolation must select `executionMode: session` or `executionMode: task`.

**Remediation:** Resolved as a documented design acceptance. §13.1 records the tradeoff and the `acknowledgeProcessLevelIsolation` gate; no code change is warranted in v1. A future per-slot-GID design would lift the exposure.

### Finding 3 — Ephemeral-container credential side-channels closed by a dedicated webhook

**Severity:** Informational

**Affected:** `pkg/admission/ephemeral_container_cred_guard/guard.go`; `pkg/admission/webhook/ephemeral_container_cred_guard.go`; `charts/lenny/templates/admission-policies/ephemeral-container-cred-guard-webhook.yaml`.

§13.1 identifies `kubectl debug` ephemeral containers as a credential-access vector: an actor with `update` on `pods/ephemeralcontainers` could attach a container that requests the adapter or agent UID, the `lenny-cred-readers` GID, or elided `securityContext` fields that inherit the pod-level fsGroup. The `lenny-ephemeral-container-cred-guard` webhook rejects, fail-closed, an ephemeral-container request that (i) sets `runAsUser` to the adapter or agent UID, (ii) declares the `lenny-cred-readers` GID, (iii) omits `runAsUser`, `runAsGroup`, or `supplementalGroups`, or (iv) mounts the credential tmpfs volume by name or mounts anything under the `/run/lenny` prefix. The guard logic translates the Kubernetes `EphemeralContainer` into the decision struct and applies all four conditions. The control matches §13.1.

**Remediation:** Resolved. The decision logic landed in `2ec3791` ("admission: ephemeral-container-cred-guard decision logic").

### Finding 4 — NetworkPolicy default-deny posture and selector-consistency audits

**Severity:** Informational

**Affected:** `charts/lenny/templates` (the rendered NetworkPolicy manifests); `pkg/preflight` (the selector-consistency and parity audits).

§13.2 requires a default-deny NetworkPolicy in every agent namespace and in `lenny-system`, plus component-scoped allow-lists keyed on the canonical `lenny.dev/component` label. The `lenny-preflight` Job audits selector consistency (NET-047, NET-050), DNS `podSelector` parity (NET-067), the additive-label rule (NET-068), the cross-family `ipBlock` partition (NET-062), and the SSRF private-range `except` parity between the gateway external-HTTPS rule and the `lenny-ops-egress` webhook rule (NET-057). The audits are fail-closed: a silently non-matching policy fails the install. The preflight audit work is present in the codebase.

**Remediation:** Resolved. The §13.2 selector-consistency and parity audits landed in `6b4ab32` ("preflight: §13.2 NetworkPolicy selector-consistency and parity audits"), and the kube-apiserver post-DNAT port admission in `88b1f6a`.

### Finding 5 — Token Service binary serves plaintext HTTP with no mTLS

**Severity:** High

**Affected:** `cmd/lenny-token-service/main.go` (the `http.Server` construction and `ListenAndServe` call); `pkg/tokenservice/tokenservice.go`.

The `lenny-token-service` binary constructs an `http.Server` and calls `ListenAndServe`, not `ListenAndServeTLS`. No `tls.Config`, client-certificate verification, or SPIFFE peer check is wired. §13.3 requires the Token Service to authenticate callers over mTLS; the current binary authenticates only the `Authorization: Bearer` caller token over an unencrypted transport. Subject tokens, actor tokens, and the minted access token therefore traverse the network in clear text, and any network peer that the `lenny-system` NetworkPolicy admits to the Token Service port can reach `POST /v1/oauth/token`. The §13.2 component allow-list constrains the set of peers, so the network-layer boundary is in place, but the transport itself is unencrypted. This finding restates Finding 1 of the Phase 5.6 `credential-review.md`; the gap is unchanged as of this review.

**Remediation:** Tracked: Wave 2 (Phase 12a) credential hardening, per `credential-review.md` Finding 1. The Phase 12a Token Service must terminate mTLS, verify the gateway client certificate, and source its key material from a rotation-aware provider.

### Finding 6 — User-credential secrets are envelope-encrypted under a KMS-wrapped DEK

**Severity:** Informational

**Affected:** `pkg/gateway/credentialstore/pgstore/pgstore.go` (`New`, the `secret` and `secret_key_version` columns); `pkg/kms`, `pkg/kms/envelope`.

The Phase 5.6 `credential-review.md` Finding 2 recorded user-credential secrets persisting unencrypted, with the package doc comment stating "KMS-envelope encryption of the secret column is a later phase." That gap is now closed. `credentialstore/pgstore` envelope-encrypts the secret on write: it holds a `kms.Provider`, derives a per-record AES-256-GCM data-encryption key, wraps the DEK under a KMS key-encryption key, and stores the `pkg/kms/envelope`-encoded ciphertext blob alongside a `secret_key_version` column. A Postgres dump no longer exposes user API keys in clear text. This finding is the resolution record for `credential-review.md` Finding 2.

**Remediation:** Resolved. Envelope encryption is implemented in `pkg/gateway/credentialstore/pgstore` against `pkg/kms/envelope`. The §4.9.1 key-rotation rehearsal (`credential-review.md` Finding 6) remains tracked separately.

### Finding 7 — Credential-subsystem audit-event emission still absent

**Severity:** Medium

**Affected:** `pkg/gateway/credassign/credassign.go`; `pkg/gateway/credrenewal/credrenewal.go`; `pkg/gateway/credentialserver`; `pkg/gateway/llmproxy/handler.go`.

§4.9.2 defines a `credential.*` audit-event catalogue and §11.7 requires every such event to enter the per-tenant hash-chained `audit_log`. None of the credential-subsystem packages write an audit event: a grep of `credassign` and `credentialserver` finds no audit or `EventStore` reference. `llmproxy.handler` rejects a SPIFFE-mismatched request with `LEASE_SPIFFE_MISMATCH` but emits no `credential.lease_spiffe_mismatch` audit event for it. A revoked or compromised credential therefore leaves no tamper-evident audit trail. This restates Finding 4 of the Phase 5.6 `credential-review.md`; the gap is unchanged.

**Remediation:** Tracked: Wave 2 (Phase 12a) credential hardening, per `credential-review.md` Finding 4. Once the audit `EventStore` integration lands, the credential-assignment, renewal, revocation, and proxy-rejection paths must each emit their §4.9.2 event into the §11.7 hash-chained `audit_log`.

### Finding 8 — Upload and archive validators enforce the §13.4 ceilings

**Severity:** Informational

**Affected:** `pkg/upload/upload.go` (`ValidateEntry`, `ValidateArchive`, the ceiling constants).

§13.4 requires path-traversal rejection, entry-kind restrictions, and non-tunable archive ceilings on every client upload and every delegation file export. `pkg/upload` encodes the ceilings as exported constants (`MaxDecompressedSize` 256 MiB, `MaxEntryCount` 10 000, `MaxPathDepth` 32, and the others) and rejects any `..` path segment outright, including a segment that `path.Clean` would otherwise absorb. `EntryKind.IsForbidden` rejects `hardlink`, `character-device`, `block-device`, `FIFO`, and `socket` entries; symlinks are rejected unless the runtime opts in via `AllowSymlinks`, in which case the target must canonicalize inside the workspace root. The package is pure: it performs no filesystem I/O and parses no archives itself, so the validation logic is unit- and fuzz-testable in isolation. The control matches §13.4.

**Remediation:** Resolved. The §13.4 archive validators landed in `4e61130` ("Phase 13.4: pkg/upload — §13.4 archive validators").

### Finding 9 — Delegation-chain content scanning is advisory by design

**Severity:** Low

**Affected:** spec §13.5 and §22.3; `pkg/gateway/delegationpolicystore`; `pkg/gateway/interceptor/export.go` (`RunPreExportMaterialization`).

§13.5 describes the delegation prompt-injection surface and a layered set of mitigations: input size limits, a `PreDelegation` content-scanning hook, inter-session message scanning, opt-in exported-file scanning at `PreExportMaterialization`, messaging rate limits, messaging scope, and budget and depth limits. The structural controls (size limits, rate limits, scope, depth and budget) are platform-enforced. The content-semantics controls are advisory: `contentPolicy.interceptorRef` and `contentPolicy.scanExportedFiles` invoke a deployer-supplied classifier, and `scanExportedFiles` defaults to `false`. §22.3 records this as an explicit non-decision: Lenny ships no built-in guardrail classifier. `RunPreExportMaterialization` runs the interceptor chain when the deployer has wired one. The residual risk is documented in §13.5: a deployer that leaves `scanExportedFiles: false` must treat delegation-exported files as untrusted input and apply runtime-layer hardening. The advisory framing is a deliberate design boundary and the structural controls remain enforced regardless.

**Remediation:** Resolved as a documented design acceptance. §13.5 enumerates the layered mitigations and §22.3 records the platform-classifier-free decision. The interceptor hooks are implemented; wiring a classifier is a deployer action.

### Finding 10 — SPIFFE identity parsing and validation enforce the §10.3 URI shapes

**Severity:** Informational

**Affected:** `pkg/mtls/spiffe/spiffe.go` (`Parse`, `ValidateAgent`, `ValidateInterceptor`).

§10.3 defines two SPIFFE URI forms (an agent-pod identity and an interceptor-pod identity), both rooted at the cluster-internal CA. `spiffe.Parse` rejects a URI without the `spiffe://` scheme, with an empty trust domain, with a user-info, query, or fragment component, or with a path that does not have exactly three segments. `ValidateAgent` and `ValidateInterceptor` confirm the parsed identity matches the expected trust domain, pool or namespace, and pod name, and the interceptor validator checks the namespace against the `gateway.interceptorNamespaces` allowlist. Parse failures and validation mismatches are distinct error types so the gateway emits `spiffe_uri_malformed` versus `pod_identity_mismatch` correctly. The control matches §10.3.

**Remediation:** Resolved. SPIFFE identity parsing and validation landed in `93a9366` ("Phase 3: pool-scaling strategy + runtime-upgrade state + mTLS SPIFFE + cert deny list").

### Finding 11 — Certificate deny list propagates cluster-wide over the event bus

**Severity:** Informational

**Affected:** `pkg/mtls/denylist/denylist.go`; `pkg/mtls/denylist/propagator/propagator.go` (`Add`, `Remove`, `publish`, `Run`, `apply`).

§13.3 and §4.9 require a revoked credential or identity to propagate to every gateway replica. The `denylist` package holds the local revoked-identity set; the `propagator` publishes an `Add` or `Remove` over the `pubsub.Bus` and applies inbound messages from peers. Postgres remains the authoritative store for token revocation per §13.3, with the in-memory cache and event-bus propagation as latency optimizations; a replica that cannot reach Postgres fails closed. The propagation path matches the §13.3 design.

**Remediation:** Resolved. The mTLS deny list landed in `93a9366`; the `pkg/gateway/revocation` cache and its propagator provide the token-revocation half.

### Finding 12 — Image-signing admission is fail-closed with a configured trust anchor

**Severity:** Informational

**Affected:** `pkg/admission/cosign_verify/verify.go` (`Decide`, `inScope`); `charts/lenny/templates/admission-policies/cosign-verify-webhook.yaml`; `.github/workflows/release.yml`.

§5.2 and §13.1 require agent-pod container images to carry a valid cosign signature before admission. `cosign_verify.Decide` resolves every container image (init, regular, ephemeral) against the deployer-configured verified-registry prefix list and passes each in-scope image to a `Verifier`. The decision logic is fail-closed: an in-scope image is admitted only when `Verifier.Verify` returns a nil error, and any error rejects the pod with `IMAGE_SIGNATURE_INVALID`. An error covers an absent signature, a signature that does not chain to the trust anchor, and a backend outage. The webhook adapter rejects an empty verified-registry list at startup so a misconfiguration cannot silently disable the gate, and the webhook runs with `failurePolicy: Fail` so a webhook outage rejects unsigned images. The release pipeline signs every Lenny-built image. The control matches §5.2 and §13.1.

**Remediation:** Resolved. The cosign-verify webhook landed in `50f609f` ("admission: cosign image-signing and pod-security webhooks"), and the release-time signing pipeline in `7f3b888` ("ci: release-time supply chain").

### Finding 13 — Audit hash chain detects row tampering and chain breaks

**Severity:** Informational

**Affected:** `pkg/audit/chain.go` (`Chain.Append`, `Chain.Verify`, `computeHash`, `linkHash`).

§11.7 requires a per-tenant append-only audit hash chain that a verifier can walk to detect tampering. `audit.Chain` assigns each row a monotonic sequence number, computes a SHA-256 content hash over the row's canonical bytes, and links each row to its predecessor via `linkHash`. `Chain.Verify` recomputes every row's content hash and prev-hash link and reports `ChainBroken` at the first mismatch. A redacted row (one rewritten in place by the §12.8 GDPR erasure path) fails the content-hash recomputation by construction; the verifier reclassifies that break as lawful only when a KMS-signed `RedactionReceipt` matches the preserved pre-redaction hash. The verifier also rejects a row whose `tenant_id` does not match the chain's tenant. The control matches §11.7.

**Remediation:** Resolved. The §11.7 hash-chain primitive landed in `eb66584` ("pkg/audit: §11.7 hash-chain primitive + ChainAuditSink wiring").

### Finding 14 — Per-tenant audit-chain isolation prevents cross-tenant rows

**Severity:** Informational

**Affected:** `pkg/audit/chain.go` (`ChainSet`, `Chain.Verify` tenant check).

§12.9.1 requires tenant isolation across stores, including the audit chain. `audit.ChainSet` gives each tenant an independent `Chain`, so one tenant's GDPR redaction cannot break another tenant's chain, and `Chain.Verify` fails closed if a row carrying a foreign `tenant_id` appears in a tenant's chain. The Postgres-backed audit store partitions rows per tenant and the cross-tenant audit-query rejection is exercised by `tests/tier9_security/tenant_isolation_test.go`. The control matches §12.9.1.

**Remediation:** Resolved. Per-tenant chain isolation is part of the `pkg/audit` hash-chain work (`eb66584`); the live cross-store isolation check runs in `tests/tier9_security/tenant_isolation_test.go`.

### Finding 15 — Cross-tenant token exchange is rejected by the Token Service

**Severity:** Informational

**Affected:** `pkg/tokenexchange/tokenexchange.go`.

§13.3 requires every RFC 8693 token exchange to satisfy `issued_token.tenant_id == subject_token.tenant_id`, and requires the caller's own `tenant_id` to equal the subject token's. `pkg/tokenexchange` enforces scope narrowing (a requested scope absent from the subject token is rejected with `invalid_scope`), rejects a `delegation_depth` decrement, rejects a `caller_type` elevation, and rejects a cross-tenant exchange with reason `tenant_mismatch`. There is no cross-tenant delegation flow; platform-admin impersonation runs on a distinct code path. The control matches §13.3.

**Remediation:** Resolved. The RFC 8693 exchange logic is implemented in `pkg/tokenexchange`; the package carries unit and fuzz coverage (`c7e04e7` added the fuzz scaffold).

### Finding 16 — Connector registry rejects non-HTTPS URLs at the admin boundary

**Severity:** Informational

**Affected:** `pkg/gateway/connectorstore` (`Connector.Validate`); `tests/tier9_security/ssrf_test.go`.

§9.3 requires every connector URL to use HTTPS so a registered connector cannot become an `http://` SSRF pivot. `connectorstore.Connector.Validate` is a cross-field check that rejects a non-HTTPS `mcpServerUrl` and non-HTTPS OAuth authorization or token endpoints at the admin `Create` and `Update` boundary with a `VALIDATION_ERROR`. The DNS-rebinding and IMDS-hostname half of the SSRF threat model is the network-layer boundary: the §13.2 egress `except` blocks cover `169.254.0.0/16` and the IMDS addresses, and application-layer destination validation is required on every gateway-initiated HTTPS flow per §13.2. The application-layer URL-scheme control matches §9.3.

**Remediation:** Resolved. The Postgres-backed connector registry with the HTTPS-scheme validation landed in `335dc42` ("connectorstore: Postgres-backed connector registry"); the live SSRF test is `tests/tier9_security/ssrf_test.go`.

### Finding 17 — T4 node isolation is enforced at admission

**Severity:** Informational

**Affected:** `pkg/admission/t4_node_isolation/guard.go`; `charts/lenny/templates/admission-policies/t4-node-isolation-webhook.yaml`.

§6.4 requires T4 Restricted workloads to run on dedicated node pools, because node-level disk encryption does not provide per-tenant key isolation for T4 workspace data on `emptyDir`. The `lenny-t4-node-isolation` webhook admits a T4 pod (identified by the `lenny.dev/workspace-tier: t4` label) only when its `nodeSelector` or `nodeAffinity` pins a T4 node label and its `tolerations` include the T4 `NoSchedule` taint, and it rejects a non-T4 pod that carries a T4 node selector or toleration. The webhook runs with `failurePolicy: Fail`. The control matches §6.4.

**Remediation:** Resolved. The T4-isolation webhook landed in `828d6bd` ("compliance: residency + T4-isolation webhooks, tenant-deletion, T4 KMS").

### Finding 18 — §4.9.1 KMS key-rotation procedure is not yet rehearsable end to end

**Severity:** Medium

**Affected:** spec §4.9.1; `pkg/gateway/credentialstore/pgstore/pgstore.go` (the `secret_key_version` column); the credential subsystem (absence of a re-encryption job and a rotation rehearsal test).

Finding 6 records that user-credential secrets are now envelope-encrypted with a `secret_key_version` column, which closes the precondition that Phase 5.6 `credential-review.md` Finding 6 identified as missing. The §4.9.1 rotation procedure is documented in the spec: generate a new DEK, run an idempotent re-encryption job, verify `SELECT COUNT(*) ... WHERE secret_key_version < current_version` returns zero, then disable the old key. The schema now carries the version column the procedure depends on. A standing re-encryption job and a rehearsal test that exercises the full rotation are not yet present. The procedure is therefore documented and schema-supported, and the remaining gap is the rehearsal coverage.

**Remediation:** Tracked: Wave 2 (Phase 12a) credential hardening, per `credential-review.md` Finding 6. A rehearsal test must encrypt under DEK v1, rotate to v2, run the re-encryption job, and assert the verification query returns zero before the old key is disabled.

### Finding 19 — Token Service JTI carries a process-local monotonic counter

**Severity:** Informational

**Affected:** `pkg/tokenservice/tokenservice.go` (`newJTI`, `jtiCounter`).

`tokenservice.newJTI` builds the issued-token JTI from a microsecond timestamp concatenated with a mutex-guarded counter that starts at zero on every process start. The JTI is not a credential or a bearer secret and is not relied on for unguessability, so this is not a vulnerability. It does leak per-process issuance ordering, and two replicas started at the same instant can mint the same JTI prefix, which would collide the `issued_tokens` primary key. This restates Finding 5 of the Phase 5.6 `credential-review.md`; the gap is unchanged.

**Remediation:** Tracked: Wave 2 (Phase 12a) credential hardening, per `credential-review.md` Finding 5. The Phase 12a Token Service should mint the JTI from `crypto/rand`.

## Severity tally

| Severity | Count | Findings |
| --- | --- | --- |
| Critical | 0 | — |
| High | 1 | Finding 5 |
| Medium | 2 | Findings 7, 18 |
| Low | 2 | Findings 2, 9 |
| Informational | 14 | Findings 1, 3, 4, 6, 8, 10, 11, 12, 13, 14, 15, 16, 17, 19 |

The tally is reconciled against the finding list directly: one High
(Finding 5), two Medium (Findings 7 and 18), two Low (Findings 2 and
9), and fourteen Informational (Findings 1, 3, 4, 6, 8, 10, 11, 12,
13, 14, 15, 16, 17, and 19), for nineteen findings total.

## Disposition summary

Of the nineteen findings, sixteen are resolved against the implemented
codebase: the control exists and the finding records it. Three are
tracked for Wave 2 (Phase 12a) credential hardening, and all three
carry forward from the Phase 5.6 `credential-review.md`: the Token
Service plaintext transport (Finding 5), the absent credential-subsystem
audit emission (Finding 7), and the not-yet-rehearsed KMS key-rotation
procedure (Finding 18). The JTI construction (Finding 19) is tracked at
Informational severity. No finding in this review is Critical, and no
finding identifies an exploitable gap that lacks either a control in
the codebase or a tracked remediation wave.
