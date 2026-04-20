# Technical Design Review Findings — 2026-04-19 (Iteration 3)

**Document reviewed:** `spec/`
**Review framework:** `spec-reviews/review-guidelines.md` (26 perspectives)
**Iteration:** 3 of 3
**Total findings:** 107 across 26 review perspectives

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 1     |
| High     | 20    |
| Medium   | 48    |
| Low      | 38    |

### Critical Findings

| #       | Perspective    | Finding                                                                                    | Section       |
| ------- | -------------- | ------------------------------------------------------------------------------------------ | ------------- |
| NET-051 | Network policy | `lenny-ops` entirely absent from `lenny-system` NetworkPolicy allow-lists; default-deny blocks every lenny-ops admin-API, datastore, and prometheus flow | 13.2 + 25.4 |

### High Findings

| #       | Perspective             | Finding                                                                                  | Section |
| ------- | ----------------------- | ---------------------------------------------------------------------------------------- | ------- |
| API-005 | External API            | Duplicate `ISOLATION_MONOTONICITY_VIOLATED` catalog rows (403 vs 422)                   | 15.1 |
| BLD-005 | Build sequence          | Phase 3.5 admission-policy list incomplete against §17.2's 12-item enumeration           | 18.4 |
| BLD-006 | Build sequence          | Phase 4.5 bootstrap seed missing `global.noEnvironmentPolicy` (TNT-002 propagation)      | 18.4 |
| CMP-046 | Compliance              | Post-restore erasure reconciler can destroy legally-held data                            | 19.4 |
| DEL-009 | Delegation              | `DELEGATION_PARENT_REVOKED` / `DELEGATION_AUDIT_CONTENTION` not catalogued in §15.1       | 8 + 15.1 |
| FLR-006 | Failure/resilience      | Gateway PDB `minAvailable: 2` at Tier 1 blocks node drains and rolling updates           | 17.5 |
| K8S-038 | Kubernetes              | Per-webhook unavailability alerts missing for five of the eight enumerated webhooks      | 17.2 + 16.5 |
| MSG-007 | Messaging               | Durable-inbox Redis command set contradicted between §7.2 (RPUSH/LPOP) and §12.4 (LPUSH) | 7.2 + 12.4 |
| NET-052 | Network policy          | `lenny-ops-egress` Redis rule targets plaintext 6379 (Redis plaintext disabled)          | 25.4 |
| NET-053 | Network policy          | `lenny-ops-egress` MinIO rule targets 9000 while §13.2 mandates 9443 TLS                  | 25.4 |
| NET-054 | Network policy          | `lenny-ops-egress` uses non-immutable `name:` label against §13.2 guidance               | 25.4 |
| NET-055 | Network policy          | `lenny-ops-egress` / `lenny-backup-job` K8s-API rules use empty `namespaceSelector`       | 25.4 |
| NET-056 | Network policy          | `lenny-backup-job` egress to Postgres/MinIO omits `namespaceSelector`                     | 25.4 |
| OBS-012 | Observability           | Alerts reference unregistered metrics (`siem_delivery_lag_seconds`, `CredentialCompromised`, `lenny_controller_workqueue_depth`) | 16.5 |
| OBS-016 | Observability           | Alert-name drift between §16.5 and §25.13 Tier Table                                       | 16.5 + 25.13 |
| PRT-008 | Protocols               | Shared Adapter Types fix introduces undefined `CallerIdentity` and `PublishedMetadataRef` | 15.2 |
| SEC-003 | Security                | Duplicate `ISOLATION_MONOTONICITY_VIOLATED` catalog rows — regression from SEC-001 fix    | 15.1 |
| SEC-005 | Security                | `/run/lenny/credentials.json` mode 0400 w/ cross-UID ownership unsatisfiable under §13.1 | 4.8 + 13.1 |
| STR-007 | Streaming               | `SessionEvent.SeqNum` has no client-facing resume path — clients lose events on drop     | 15.2 |
| TNT-005 | Tenancy                 | Playground `apiKey` auth references an undefined "standard API-key auth path"            | 27.2 + 10.2 |

---

## Detailed Findings by Perspective

---

## 1. Kubernetes (K8S)

### K8S-038. Per-webhook unavailability alerts missing for five of eight webhooks [High]
**Section:** 17.2, 16.5

Iter2 K8S-037 expanded §17.2's admission-plane inventory from 5 to 12 items (8 webhook Deployments, 4 policy manifests) and added a preflight inventory check. §16.5 defines alerts only for `lenny-pool-config-validator`, `lenny-label-immutability`, and `lenny-sandboxclaim-guard`. The remaining five (`lenny-direct-mode-isolation`, `lenny-t4-node-isolation`, `lenny-drain-readiness`, `lenny-crd-conversion`, `lenny-data-residency-validator`) have no unavailability alert — operators lose observability on the very webhooks that can silently fail-open for secondary admission decisions.

**Recommendation:** Add `…WebhookUnavailable` alerts (warning severity, `up{job="…"} == 0 for 5m`) for each of the five missing webhook deployments; cross-reference §17.2 inventory row for mutual completeness.

### K8S-039. §13.2 NetworkPolicy admission-webhook enumeration disagrees with §17.2 [Medium]
**Section:** 13.2, 17.2

The §13.2 NetworkPolicy allow-list names "admission-webhook pods" as a single origin for port 8080 ingress. §17.2 now enumerates 8 distinct webhook deployments. A single `app: admission-webhook` label cannot match all eight without a label convention spec'd in §13.2 or §17.2.

**Recommendation:** In §13.2, either broaden the selector to `{ lenny.dev/component: admission-webhook }` (and require all 8 deployments to carry that label in §17.2), or enumerate each individually.

---

## 2. Security (SEC)

### SEC-003. Duplicate `ISOLATION_MONOTONICITY_VIOLATED` catalog rows with conflicting HTTP status [High]
**Section:** 15.1 (lines 771 + 800)

Iter2 SEC-001 fix added a derive-time row but left the pre-existing delegation-time row in place. Both share the same error code but map to different HTTP statuses (403 vs 422), violating §15.1's stated invariant that codes are globally unique.

**Recommendation:** Collapse to one row with the status that applies to *both* endpoints, or split into two distinct codes (`ISOLATION_MONOTONICITY_VIOLATED_DELEGATE` / `…_DERIVE`).

### SEC-004. Self-contradictory `prompt_history` clause in replay endpoint [Medium]
**Section:** 15.1 replay semantics

The new replay paragraph both prohibits and permits `prompt_history` in the same sentence ("MUST NOT include prompt history; MAY stream prior prompts for audit reconstruction"). Implementers get no guidance.

**Recommendation:** Pick one. If audit reconstruction must stream prompts, flag the response as audit-only and mark it with a dedicated envelope field.

### SEC-005. `/run/lenny/credentials.json` cross-UID ownership unsatisfiable under §13.1 capability rules [High]
**Section:** 4.8 + 13.1

§4.8 requires the file to be mode 0400 and owned by the adapter UID, but §13.1 drops `CAP_CHOWN` from every agent-pod init container. The spec assumes an init container will chown the file to another UID — impossible without the capability.

**Recommendation:** Either relax the file to group-readable and rely on supplementary group membership, or explicitly permit the init container to retain `CAP_CHOWN` via `securityContext.capabilities.add` with admission-policy justification.

### SEC-006. SPIFFE-binding disablement not enforced in multi-tenant mode [Medium]
**Section:** 13.x

Spec asserts that SPIFFE binding is disabled in multi-tenant clusters but does not wire this into admission control; a tenant with `SpiffeBindingEnabled: true` in a CR proceeds silently.

**Recommendation:** Add a validating admission rule that rejects SpiffeBinding activation outside single-tenant deployments, with error `SPIFFE_BINDING_FORBIDDEN_MULTITENANT`.

### SEC-007. Interceptor `failPolicy` oscillation enables bulk prompt-injection across delegation trees [Medium]
**Section:** Interceptor lifecycle

Interceptor lease renewals that flip `failPolicy` between `Fail` and `Ignore` at each renewal window expose a race where an attacker observing renewal timing can inject prompts during the `Ignore` slice.

**Recommendation:** Pin `failPolicy` for the lease lifetime; changes require lease revocation and a fresh delegation tree.

---

## 3. Network Policy (NET)

### NET-051. `lenny-ops` absent from `lenny-system` NetworkPolicy allow-lists [Critical]
**Section:** 13.2, 25.4

The §13.2 enumeration lists gateway, token-service, controller, pgbouncer, minio, admission-webhook, coredns, and OTLP — but no `lenny-ops` row. Every `lenny-ops` → gateway admin-API (8080), Postgres, Redis, MinIO, Prometheus call is blocked by the default-deny policy. This breaks every documented operability flow at install time.

**Recommendation:** Add a full `lenny-ops` row and matching ingress rows to Gateway, PgBouncer, MinIO, Redis allow-lists.

### NET-052. `lenny-ops-egress` Redis rule targets plaintext 6379 [High]
**Section:** 25.4

Rule permits TCP 6379 egress; Redis is configured with plaintext disabled in §12.4. Connection always fails.

**Recommendation:** Change to 6380 (Redis TLS port).

### NET-053. `lenny-ops-egress` MinIO rule targets 9000 while §13.2 requires 9443 TLS [High]
**Section:** 25.4

Rule permits 9000; §13.2 normative rule is 9443/TLS. Breaks uploads + backup writes.

**Recommendation:** Change to 9443.

### NET-054. `lenny-ops-egress` uses non-immutable `name:` label [High]
**Section:** 25.4

`namespaceSelector` uses `matchLabels: { name: lenny-system }`. `name` is mutable and not guaranteed on kube namespaces. §13.2 mandates `kubernetes.io/metadata.name` (auto-populated, immutable).

**Recommendation:** Replace with `kubernetes.io/metadata.name`.

### NET-055. K8s-API rules use empty `namespaceSelector` [High]
**Section:** 25.4

`lenny-ops-egress` and `lenny-backup-job` K8s-API rules specify empty `namespaceSelector: {}`, permitting egress to every pod in every namespace on 443 — wildly overbroad; includes unintended targets.

**Recommendation:** Scope to `kubernetes.io/metadata.name: default` with `podSelector: { component: apiserver }` or use `kube-apiserver.default.svc` IP carve-out.

### NET-056. `lenny-backup-job` egress to Postgres/MinIO omits `namespaceSelector` [High]
**Section:** 25.4

Selectors imply "in the same namespace" — wrong for cross-namespace or cloud-managed endpoints.

**Recommendation:** Make `namespaceSelector` explicit; document that lenny-system is the default target.

### NET-057. Gateway external HTTPS egress omits RFC1918 exclusions applied to lenny-ops [Medium]
**Section:** 13.2

Gateway path allows 0.0.0.0/0:443 without the 10.0.0.0/8 / 172.16.0.0/12 / 192.168.0.0/16 SSRF carve-outs that §25.4 applies to `lenny-ops`. The higher-risk surface is weaker.

**Recommendation:** Mirror the RFC1918 exclusions on gateway egress.

### NET-058. `allow-gateway-egress-interceptor-{namespace}` lacks podSelector [Medium]
**Section:** 13.2

The rule allows gateway egress to every pod in the interceptor namespace. Interceptor namespaces may host other tenant-operated pods.

**Recommendation:** Require a `podSelector: { lenny.dev/component: interceptor }` on the destination.

### NET-059. OTLP egress permits plaintext gRPC (4317) with no TLS requirement [Medium]
**Section:** 13.2

Trace payloads carry session metadata and occasional error bodies; intra-cluster interception feasible.

**Recommendation:** Require TLS (4318/OTLP-HTTP or 4317 with TLS); document collector expectations.

### NET-060. Pod→gateway mTLS lacks symmetric SAN validation [Medium]
**Section:** 13.x

Pods validate gateway SAN; no documented rule for gateway validating pod identity SAN. Impersonation surface.

**Recommendation:** Specify mTLS peer identity validation on both sides with SPIFFE IDs as SANs.

---

## 4. Performance (PRF)

### PRF-004. `minWarm: 1,050` baseline re-surfaces in two prose locations after iter2 PRF-003 table fix [Medium]
**Section:** 6.x, 17.8

Prose references the old figure (not updated when the capacity-tier table was corrected).

**Recommendation:** Update prose references to match the corrected figure.

### PRF-005. Gateway PDB allows voluntary-disruption burst exceeding MinIO budget at Tier 3 [Medium]
**Section:** 17.5 + 10.8

Even with iter2 FLR-002 tightening, a `ceil(replicas/2)` PDB at Tier 3 (20 replicas) still allows 10 simultaneous drains → 4000 concurrent checkpoint uploads against the 400-upload MinIO budget.

**Recommendation:** Cap the burst via `maxUnavailable: 1` + `maxSurge: 25%`; specify a hard ceiling of concurrent gateway evictions independent of replica count.

---

## 5. Protocols (PRT)

### PRT-008. Shared Adapter Types fix introduces two undefined types [High]
**Section:** 15.2

Iter2 PRT-005 introduced `CallerIdentity` and `PublishedMetadataRef` in the type catalog without schemas anywhere in the spec.

**Recommendation:** Add schema definitions in §15.2 with required fields, validation rules, and examples.

### PRT-009. `OutboundCapabilitySet.SupportedEventKinds` type + comment contradict closed-enum claim [Medium]
**Section:** 15.2

Field is typed `string` but comment says "closed enum — MUST be one of the registry values". No type-level enum; clients lose compile-time check.

**Recommendation:** Retype to a generated enum (e.g., `EventKind`) or reflect the set in a constants file.

### PRT-010. `AuthorizedRuntime` fields do not match `GET /v1/runtimes` response [Medium]
**Section:** 15.2 + 15.1

Type catalog lists fields A, B, C; response-schema example lists A, B, D.

**Recommendation:** Reconcile — make the type the normative schema.

### PRT-011. MCP target-version currency note still stale [Low]
**Section:** 15.2

Iter2 claimed fix did not apply — note still cites 2025-03-26 without the "current-as-of" qualifier.

**Recommendation:** Update the version-currency note consistently across §15.2 and §15.4.

---

## 6. Developer Experience / Platform (DXP)

### DXP-006. §15.7 scaffolder description contradicts §24.18 `binary`/`minimal` no-SDK promise [Low]
**Section:** 15.7

The scaffolder description still says "uses the Runtime Author SDK" even when emitting the `binary` or `minimal` template, which iter2 DXP-002 promised to be SDK-free.

**Recommendation:** Qualify the scaffolder description with the template-dependent SDK usage.

### DXP-007. §26.1 "`local` profile installations" is undefined terminology [Low]
**Section:** 26.1

`local` is not an Operating Mode named anywhere in §17.4.

**Recommendation:** Replace with "Embedded Mode" or define `local` as an Install Profile and cross-reference.

### DXP-008. §17.4 Embedded Mode custom-runtime path omits tenant-access grant for non-`default` tenants [Low]
**Section:** 17.4

Iter2 DXP-001 added the Embedded Mode path but did not spell out `lenny-ctl tenant add-runtime-access`.

**Recommendation:** Add the grant step to the bullet list.

---

## 7. Operations (OPS)

### OPS-007. Embedded Mode has no Postgres major-version mismatch fail-safe [Low]
**Section:** 17.4

If `lenny up` bundled PG version mismatches persisted data from a previous install, silent failure modes.

**Recommendation:** Add explicit version-probe + refuse-to-start semantics to Embedded Mode boot path.

### OPS-008. §17.8.1 operational defaults omits `ops.drift.*` tunables [Low]
**Section:** 17.8.1

Tunables are referenced but not in the defaults table.

**Recommendation:** Add `ops.drift.scanIntervalSeconds`, `ops.drift.snapshotTTLSeconds`, `ops.drift.alertThreshold` rows.

### OPS-009. `issueRunbooks` lookup missing `DRIFT_SNAPSHOT_STALE` → `drift-snapshot-refresh` [Low]
**Section:** 25.x

Runbook catalog refers to it; issueRunbooks lookup table omits.

**Recommendation:** Add the row.

---

## 8. Tenancy (TNT)

### TNT-005. Playground `apiKey` mode references an undefined "standard API-key auth path" [High]
**Section:** 27.2 + 10.2

§10.2 documents OIDC and per-tenant service-account JWTs; no "standard API-key auth path" exists anywhere in the spec.

**Recommendation:** Either (a) define the standard API-key path in §10.2 with tenant-identity source and RBAC binding, or (b) retarget `apiKey` mode to the tenant service-account JWT issuance flow.

### TNT-006. Playground `dev` HMAC JWT `tenant_id` source unspecified [Medium]
**Section:** 27.2

The dev JWT binds a `tenant_id` claim but the spec never says where it comes from (env, CR, flag).

**Recommendation:** Require explicit tenant scope at `lenny up` time; reject if ambiguous.

### TNT-007. `tenant-admin` authorization ambiguous when `tenantId` is absent [Medium]
**Section:** 25.4 / 10.2

Operations-inventory rule allows `tenant-admin` on calls with no `tenantId` field (e.g., platform-scoped backup). Allows cross-tenant escalation.

**Recommendation:** Require `tenant-admin` + `tenantId` match, or elevate to `platform-admin` for tenant-less operations.

---

## 9. Storage (STR)

### STR-007. `SessionEvent.SeqNum` has no client-facing resume path [High]
**Section:** 15.2

Iter2 added `SeqNum` to the event schema; clients can observe it but cannot resume with it. No `resumeFromSeq` on MCP stream or REST `/events?since=`.

**Recommendation:** Add `resumeFromSeq` query param on `/sessions/{id}/events` and the MCP stream open-frame.

### STR-008. No client-facing keepalive/heartbeat frames on MCP session stream [Medium]
**Section:** 15.4

Idle disconnects by intermediaries (Cloudflare, ELB) will terminate long-idle streams with no renegotiation contract.

**Recommendation:** Specify a server-side `keepalive` event every 20 s idle; document client reconnect semantics.

### STR-009. Adapter buffered-drop head-eviction loses events silently from subscriber's view [Medium]
**Section:** 15.4 adapter buffering

On buffer overflow, head-eviction drops oldest in-flight events — but the subscriber never sees a `gap` marker.

**Recommendation:** Emit a synthetic `event_dropped` sentinel with the dropped range; requires `SeqNum` (STR-007).

### STR-010. Stream-drain preStop stage-3 interaction with slow-subscriber policy unspecified [Low]
**Section:** 10.8

If a subscriber is slow, stage-3 drain can block indefinitely within the grace period.

**Recommendation:** Specify subscriber timeout during drain; default 5 s per subscriber.

---

## 10. Delegation (DEL)

### DEL-008. Orphan tenant-cap fallback emits no audit event or parent signal [Low]
**Section:** 8.x

Orphan-capacity fallback silently repoints lineage without telling the parent.

**Recommendation:** Emit `delegation.orphan_fallback` audit + push a `delegation.orphaned` event to the parent.

### DEL-009. `DELEGATION_PARENT_REVOKED` / `DELEGATION_AUDIT_CONTENTION` not catalogued in §15.1 [High]
**Section:** 8 + 15.1

Iter2 DEL-007 fix referenced both codes but never added rows to §15.1.

**Recommendation:** Add both to §15.1 with status codes and retry-after semantics.

### DEL-010. `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE` not catalogued [Medium]
**Section:** 15.1

Same class of gap as DEL-009 for cross-tree messaging.

**Recommendation:** Add to §15.1 with 403 status and scope-upgrade hint.

### DEL-011. `tracingContext` validation error codes not catalogued [Low]
**Section:** 15.1

Context-propagation spec mentions validation errors; no codes listed.

**Recommendation:** Add `INVALID_TRACING_CONTEXT`, `TRACING_CONTEXT_DEPTH_EXCEEDED`.

---

## 11. Session (SES)

### SES-009. Derive pre-running `failed` persistence still contradicts §7.1 atomicity [Medium]
**Section:** 7.1

Two-phase derive leaves `failed` row visible before the swap is abandoned; breaks atomicity.

**Recommendation:** Move the failure-row write into the same transaction; guard with coordinator_generation.

### SES-010. `resuming → cancelled` / `resuming → completed` still undefined [Medium]
**Section:** 7.2

State-machine edges missing for operator-initiated cancel during resume.

**Recommendation:** Add both; specify the snapshot-close semantics.

### SES-011. `starting → resume_pending` not reflected in §15.1 preconditions [Low]
**Section:** 15.1

Endpoint preconditions don't enumerate this edge.

**Recommendation:** Add to preconditions with error code `SESSION_STARTING`.

### SES-012. SSE reconnect storm still unmitigated [Low]
**Section:** 15.4

No jitter/backoff contract on the client side; iter2 SES-008 not addressed.

**Recommendation:** Document exponential backoff 1s/2s/4s/8s, max 30s; require clients to honour.

### SES-013. `resuming` failure-transition inconsistency between §7.2 and §6.2 [Medium]
**Section:** 7.2 + 6.2

§7.2 says resume failure returns to `checkpointed`; §6.2 says `failed`.

**Recommendation:** Reconcile; checkpoint-survivable resume failures return to `checkpointed`; others → `failed`.

### SES-014. `awaiting_client_action` expiry trigger under-specified [Low]
**Section:** 7.2

No timeout value or audit emission specified.

**Recommendation:** Default `awaiting_client_action_timeout_seconds: 300`; emit `session.awaiting_client_action_expired`.

---

## 12. Observability (OBS)

### OBS-012. Alerts reference still-unregistered metrics [High]
**Section:** 16.5

Alerts `SiemDeliveryLag`, `CredentialCompromised`, `ControllerWorkQueueDepth` reference metrics not in §16.1 registry.

**Recommendation:** Add metric rows or remove the alerts.

### OBS-013. Last unnamed metric row in §16.1 [Medium]
**Section:** 16.1

One row in the registry table has no metric name.

**Recommendation:** Name the metric or drop the row.

### OBS-014. `level` label value set still not enumerated [Medium]
**Section:** 16.1.1

Multiple metrics carry a `level` label; enumeration missing.

**Recommendation:** Add an enumeration row (`basic|standard|full`).

### OBS-015. `GatewaySubsystemCircuitOpen` templated-name vs label mismatch persists [Medium]
**Section:** 16.5

Alert name templates `{{ $labels.subsystem }}` into the alert name, but the metric's label set doesn't contain `subsystem`.

**Recommendation:** Rename alert or add the label.

### OBS-016. Alert-name drift between §16.5 and §25.13 [High]
**Section:** 16.5 + 25.13

`PostgresReplicationLagHigh` in §25.13 vs `PostgresReplicationLag` in §16.5. Operators see mismatched names between runbook catalog and alert catalog.

**Recommendation:** Pick one canonical name across both.

### OBS-017. `PostgresReplicationLagHigh` metric row refers to non-existent alert [Low]
**Section:** 16.1

Cross-reference breaks.

**Recommendation:** Fix cross-reference after OBS-016 resolution.

### OBS-018. `deployment_tier` label used in alerts but not in §16.1.1 [Medium]
**Section:** 16.1.1

Label referenced in multiple alert expressions; not listed in label-convention table.

**Recommendation:** Add the label with tier-1/2/3 value set.

### OBS-019. `lenny_gateway_request_queue_depth` referenced but not registered [Medium]
**Section:** 16.1 + 10.x

Metric used in prose; no §16.1 row.

**Recommendation:** Add the metric with description, labels, source.

### OBS-020. `lenny_controller_leader_lease_renewal_age_seconds` label name unspecified [Low]
**Section:** 16.1

Labels column empty.

**Recommendation:** Specify `controller` label.

### OBS-021. `lenny_warmpool_pod_startup_duration_seconds` uses non-canonical label description [Low]
**Section:** 16.1

Label description deviates from template.

**Recommendation:** Use the canonical "capacity tier of the source pool" description.

---

## 13. Compliance (CMP)

### CMP-046. Post-restore erasure reconciler can destroy legally-held data [High]
**Section:** 19.4 + 20.4

Reconciler replays the erasure journal from before the restore point — but legal-hold overlays applied *after* backup time are missing, so data released from hold post-backup may be erased mistakenly, and data placed under hold post-backup may be erased despite the hold.

**Recommendation:** Reconcile against the current legal-hold ledger in addition to the erasure journal; block erasure replays if ledger state is older than restore point.

### CMP-047. OCSF dead-letter rows retain raw canonical PII past user erasure [Medium]
**Section:** 19.4

Dead-letter storage for un-ingestable OCSF events persists for 14 days; user erasure cascades don't touch it.

**Recommendation:** Include dead-letter table in erasure cascade; emit `erasure.deadletter_redacted` receipt.

### CMP-048. ArtifactStore (MinIO) not covered by backup pipeline [Medium]
**Section:** 20.4

Backup encompasses Postgres + Redis AOF + credential vault snapshot; MinIO is excluded. Restore-time GDPR + residency claims are incomplete if workspace artifacts are absent.

**Recommendation:** Add MinIO snapshot to backup pipeline (native backup or bucket replication); cross-document in §20.4.

---

## 14. External API (API)

### API-005. Duplicate `ISOLATION_MONOTONICITY_VIOLATED` catalog rows [High]
**Section:** 15.1 (lines 771 + 800)

Same code, two rows, different statuses (403 vs 422). See SEC-003 for security framing.

**Recommendation:** Collapse to one canonical status; document in one row.

### API-006. Duplicate-key ordering in §15.1 catalog has no invariant [Low]
**Section:** 15.1

Table order is arbitrary; readers can't predict where to add new codes.

**Recommendation:** Sort alphabetically or by HTTP status; document the invariant at the top of the table.

---

## 15. Credentials (CRD)

### CRD-006. New `credential_rotation_inflight_ceiling_hit_total` metric missing `lenny_` prefix [Medium]
**Section:** 16.1

Regression from iter2 CRD-005 fix — metric added without canonical prefix.

**Recommendation:** Rename to `lenny_credential_rotation_inflight_ceiling_hit_total`.

### CRD-007. `rotationTrigger: scheduled_renewal` contradicts canonical `proactive_renewal` [Medium]
**Section:** 4.9

New value introduced in one place; canonical enum uses `proactive_renewal` elsewhere.

**Recommendation:** Replace `scheduled_renewal` with `proactive_renewal`.

### CRD-008. `rotationTrigger: fault_driven_rate_limited` not defined in §4.9 fallback flow enum [Low]
**Section:** 4.9

Missing enum row.

**Recommendation:** Add to the Fallback Flow rotation-trigger enumeration.

### CRD-009. Admin-time RBAC live-probe covers only add-credential path [Medium]
**Section:** 4.9

Pool creation and update paths still retain the original CRD-004 gap — operator can create a pool whose credential reference lacks RBAC.

**Recommendation:** Extend live-probe to create/update; surface `CREDENTIAL_SECRET_RBAC_MISSING`.

### CRD-010. 300 s revocation-triggered rotation ceiling emits metric but no audit event [Low]
**Section:** 4.9

Silently shaping emissions.

**Recommendation:** Emit `credential.rotation_rate_limited` audit event when the ceiling is hit.

### CRD-011. Admin API + CLI reference do not document `CREDENTIAL_SECRET_RBAC_MISSING` error [Low]
**Section:** 15.1 + 24.x

Error surfaces at runtime; operators have no documented recovery path.

**Recommendation:** Document in §15.1 and `lenny-ctl credentials` CLI reference.

---

## 16. Containerization (CNT)

### CNT-005. `gitClone` auth scope hardcoded to GitHub while URL accepts any host [Medium — real error]
**Section:** 14.x

`auth` scope mentions only `github.com` but URL validator accepts any git host (GitLab, Bitbucket, self-hosted).

**Recommendation:** Generalise to host-agnostic; enumerate supported auth providers.

### CNT-006. `uploadArchive` format list mismatch between §14 and §7.4 [Medium — real error]
**Section:** 14 + 7.4

§14 lists `{tar.gz, zip}`; §7.4 lists `{tar.gz, tar.zst, zip}`.

**Recommendation:** Reconcile; add `tar.zst` to both or remove from §7.4.

### CNT-007. `gitClone.auth` paragraph does not bind credential-pool identity to session [Minor — clarity]
**Section:** 14.x

Spec references pool identity but does not formalise the binding during clone.

**Recommendation:** Add one sentence binding the lease to the session for audit traceability.

---

## 17. Capacity / Pool System (CPS)

### CPS-003. `partial_recovery_threshold_bytes` configuration surface undefined [Low]
**Section:** 10.x

Referenced; no configuration row.

**Recommendation:** Add to §17.8.1 defaults.

### CPS-004. Partial-manifest reassembly semantics ambiguous (multipart vs separate objects) [Medium]
**Section:** 10.x

Spec says "partial manifests" but doesn't say whether MinIO stores one multipart object or separate objects.

**Recommendation:** Specify separate objects with indexed names (`partial-{n}.tar.gz`) and a manifest pointing to them.

### CPS-005. Partial-manifest truncation lies on arbitrary byte offset, not tar-member boundary [Medium]
**Section:** 10.x

Recovery produces corrupt tars.

**Recommendation:** Align truncation to the last completed tar-member offset; record offset in the manifest.

### CPS-006. Partial-manifest resume path lacks coordinator_generation guard against split-brain [Low]
**Section:** 10.x

Two coordinators could both resume the same partial manifest.

**Recommendation:** Add generation-fence on the manifest claim.

### CPS-007. CheckpointBarrier fan-out during drain does not address recently-handed-off sessions [Low]
**Section:** 10.8

Sessions handed off < 5 s before drain may be missed.

**Recommendation:** Extend barrier scope to cover the handoff-window set.

---

## 18. Runtime Protocols (PRT)

See perspective 5.

---

## 19. Build Sequence (BLD)

### BLD-005. Phase 3.5 admission-policy list incomplete against §17.2 12-item enumeration [High]
**Section:** 18.4

Phase 3.5 still names only 3 of 8 webhooks + 0 of 4 policy manifests.

**Recommendation:** Update Phase 3.5 deliverables to enumerate all 12 items from §17.2; cross-reference.

### BLD-006. Phase 4.5 bootstrap seed missing `global.noEnvironmentPolicy` after iter2 TNT fix [High]
**Section:** 18.4

Iter2 TNT-002 fix added the Helm value; build-sequence's bootstrap inventory did not update.

**Recommendation:** Add the value to Phase 4.5's required Helm values list.

### BLD-007. Phase 5.8 LLM Proxy deliverables reference `lenny-direct-mode-isolation` as existing, but Phase 3.5 hasn't deployed it [Medium]
**Section:** 18.4

Phase sequencing: 3.5 must deploy the webhook before 5.8 relies on it.

**Recommendation:** Move `lenny-direct-mode-isolation` into Phase 3.5 or split into 3.5a/3.5b.

### BLD-008. Phase 13 compliance work does not enumerate `lenny-data-residency-validator` / `lenny-t4-node-isolation` deployments [Medium]
**Section:** 18.4

These webhooks are §13-new and need a deployment phase.

**Recommendation:** Add both to Phase 13 deliverables.

### BLD-009. Phase 8 checkpoint/resume work does not enumerate `lenny-drain-readiness` deployment [Low]
**Section:** 18.4

Webhook is new; needs phase coverage.

**Recommendation:** Add to Phase 8.

### BLD-010. Phase 1 wire-contract artifacts do not cover Shared Adapter Types / SessionEvent Kind Registry [Low]
**Section:** 18.4

Iter2 PRT-005 added these contracts; Phase 1 artifact list pre-dates them.

**Recommendation:** Add both to Phase 1 deliverables (OpenAPI + protobuf + JSON schema).

---

## 20. Failure / Resilience (FLR)

### FLR-006. Gateway PDB `minAvailable: 2` at Tier 1 blocks node drains and rolling updates [High]
**Section:** 17.5

Iter2 FLR-002 fix set `minAvailable: 2` + Tier 1 `minReplicas: 2` → node drain blocks indefinitely (no disruption budget).

**Recommendation:** Use `maxUnavailable: 1` for Tier 1 instead, or raise Tier 1 `minReplicas` to 3.

### FLR-007. Gateway PDB formula `ceil(replicas/2)` not expressible as PDB YAML [Medium]
**Section:** 17.5

PodDisruptionBudget allows integer or percentage; dynamic formulas aren't native.

**Recommendation:** Express as `minAvailable: 50%` (K8s rounds up) and document the semantics.

### FLR-008. preStop cache population gap on coordinator handoff [Medium]
**Section:** 10.8

Iter2 FLR-004 fix says "use cached value if Postgres unreachable" — but handoff to a new coordinator means an empty cache.

**Recommendation:** Prime cache on coordinator assume-leader; document cold-start behaviour.

### FLR-009. `InboxDrainFailure` alert rule text not evaluable PromQL [Low]
**Section:** 16.5

Text is English prose; no `expr:`.

**Recommendation:** Replace with `increase(lenny_inbox_drain_failure_total[5m]) > 0`.

### FLR-010. PgBouncer readiness probe — FLR-005 still unresolved [Low]
**Section:** 12.x

No change to `failureThreshold`.

**Recommendation:** Widen to `failureThreshold: 8` or decouple readiness.

### FLR-011. No regression on billing MAXLEN (FLR-001) [Partial — note only]
**Section:** N/A

FLR-001 resolved in iter2; no new regression. No action required.

---

## 21. Experimentation (EXP)

### EXP-005. Results API filters reference undocumented error `INVALID_QUERY_PARAMS` [Low]
**Section:** 15.x

Not in §15.1.

**Recommendation:** Add to catalog or replace with canonical code.

### EXP-006. Results API response shape undefined for `breakdown_by` requests [Medium]
**Section:** 15.x

Query parameter accepted; response shape unspecified.

**Recommendation:** Define a `BreakdownResponse` schema with typed aggregations.

### EXP-007. Variant count still unbounded [Low]
**Section:** 22.x

Iter2 EXP-003 unresolved.

**Recommendation:** Cap at 16; return `TOO_MANY_VARIANTS` past ceiling.

### EXP-008. Sticky cache wording still internally contradictory [Low]
**Section:** 22.x

Iter2 EXP-004 unresolved.

**Recommendation:** Resolve to a single TTL/refresh semantic.

### EXP-009. Isolation-mismatch fallthrough silently contaminates control bucket [Medium]
**Section:** 22.x

When pod isolation profile doesn't match variant's target, request silently falls through to control.

**Recommendation:** Fail closed with `VARIANT_ISOLATION_UNAVAILABLE` instead of silent fallthrough.

---

## 22. Documentation (DOC)

### DOC-008. Intra-file anchor `#73-retry-and-resume` does not resolve in `06_warm-pod-model.md` [Medium]
**Section:** 06

Anchor broken.

**Recommendation:** Fix heading or anchor reference.

### DOC-009. Cross-file anchor `16_observability.md#167-audit-event-catalogue` does not exist [Medium]
**Section:** 16

No such heading.

**Recommendation:** Rename §16.7 to include "Audit Event Catalogue" or fix the reference to the actual heading.

### DOC-010. Two broken `06/12` anchors in admission-policies enumeration [Medium]
**Section:** 13

Links to `#6-warm-pod-model` / `#12-data` fail.

**Recommendation:** Fix anchors to resolve.

### DOC-011. Headings "16.7 Section 25 Audit Events" / "16.8 Section 25 Metrics" still confusing [Low]
**Section:** 16

Heading names confuse cross-reference readers.

**Recommendation:** Rename to "16.7 Audit-Event Catalogue" / "16.8 Metric Catalogue".

### DOC-012. README TOC omits first subsection of three sections [Low]
**Section:** README

TOC miss.

**Recommendation:** Regenerate TOC.

---

## 23. Messaging (MSG)

### MSG-007. Durable-inbox Redis command set contradicted between §7.2 and §12.4 [High]
**Section:** 7.2 + 12.4

§7.2 uses `RPUSH` (writer) + `LPOP` (reader) = FIFO. §12.4 uses `LPUSH` + `LPOP` = LIFO.

**Recommendation:** Use FIFO everywhere; `LPUSH` / `RPOP` or `RPUSH` / `LPOP`. Pick one set and propagate.

### MSG-008. `message_expired` reason codes diverge across three drain paths [Medium]
**Section:** 7.2

Three prose passages use subtly different reason strings.

**Recommendation:** Standardise on `message_expired` everywhere.

### MSG-009. Metrics referenced by inbox prose not declared in §16.1 [Medium]
**Section:** 16.1

Several `lenny_inbox_*` metrics only exist in prose.

**Recommendation:** Add to §16.1 registry.

### MSG-010. Pre-receipt rejections enumerated only in §15 catalog, not reconciled in §15.4.1 [Low]
**Section:** 15.4.1

"Every call returns a receipt" is contradicted by pre-receipt rejection codes.

**Recommendation:** Amend the clause: "except where rejected by syntactic pre-checks".

---

## 24. Policy / Admission (POL)

### POL-015. POL-014 fix introduces contradictory chain-ordering statements [Medium]
**Section:** 13.x + 21.x

AdmissionController says "AuthEvaluator runs first"; AuthEvaluator docs say "AdmissionController runs first".

**Recommendation:** Canonicalize: AdmissionController → AuthEvaluator → ConnectorEvaluator; update both prose blocks.

### POL-016. Broken anchor in POL-014 audit-catalog cross-reference [Low]
**Section:** 13

Link to `#25-audit-events` fails.

**Recommendation:** Fix anchor.

### POL-017. `admission.circuit_breaker_rejected` lacks audit-sampling discipline [Medium]
**Section:** 13.x

Storm scenario can flood the audit pipeline.

**Recommendation:** Sample 1:100 for this event during storm; suppress individually with aggregated `circuit_breaker_storm` summary event every 60 s.

---

## 25. Extensions / Session state machine (EXM)

### EXM-007. `warn_within_budget` introduced but never defined [Medium]
**Section:** 7.x

Used as a state; no semantic.

**Recommendation:** Define or remove.

### EXM-008. `node not draining` precondition introduced but undefined [Medium]
**Section:** 7.x

No formal definition.

**Recommendation:** Tie to node annotation `node.kubernetes.io/unschedulable == false`.

### EXM-009. `cancelled → task_cleanup` transition skips retirement-check semantics [Low]
**Section:** 7.x

Bypasses retirement checks.

**Recommendation:** Add retirement check as a guard.

### EXM-010. Retirement-policy config-change staleness re-surfaces after EXM-006 partial fix [Low]
**Section:** 7.x

Config changes not picked up until next cycle.

**Recommendation:** Subscribe to config-change events.

---

## 26. Web playground (WPP)

### WPP-008. `apiKey` mode invokes "standard API-key auth path" §10.2 does not document [Medium]
**Section:** 27.2 + 10.2

Same root cause as TNT-005.

**Recommendation:** Define the standard API-key path or retarget to tenant JWT.

### WPP-009. `playground.maxIdleTimeSeconds` bound not validatable at Helm install time [Low]
**Section:** 27.2

Bound `60 ≤ v ≤ runtime's maxIdleTimeSeconds` depends on runtime admission, which happens later.

**Recommendation:** Validate at runtime-registration time; reject config values > any registered runtime's max.

---

## Cross-Cutting Themes

1. **Catalog hygiene degraded across iter2 fixes.** Four high-severity findings (API-005, SEC-003, DEL-009, OBS-012) trace to iter2 fixes that added prose without touching the canonical error-code / metric / audit-event catalogs. Catalog-first edits would have prevented these regressions.

2. **NetworkPolicy coverage for lenny-ops is structurally incomplete.** One Critical (NET-051) plus five High findings (NET-052..056) all surface the same root cause: `lenny-ops` — a post-iter1 addition — was never propagated into §13.2's allow-list enumeration. This also echoes into the build sequence (BLD-005/008) and K8S-038/039 webhook alert coverage.

3. **Iter2 fixes introduced new unsatisfiable constraints.** SEC-005 (0400 file cross-UID ownership vs dropped CAP_CHOWN), FLR-006 (PDB `minAvailable: 2` vs `minReplicas: 2`), and PRT-008 (undefined type names) all describe iter2 fixes whose side effects weren't exercised.

4. **State-machine edges are still partial.** SES-009..014 cover six distinct edges that the §7 state-machine diagram either omits or specifies inconsistently with §15.1 preconditions. SES-013's `resuming` terminal-state disagreement between §7.2 and §6.2 is the most concrete.

5. **Observability catalog has structural holes.** 10 OBS findings (OBS-012..021) plus 4 MSG findings reveal alerts referencing unregistered metrics, label-set drift, and unnamed rows. §16.1 / §16.5 / §25.13 lack a single-source-of-truth regime.

6. **Client-visible resume is unfinished.** STR-007 (`SeqNum` without `resumeFromSeq`), STR-008 (no keepalives), STR-009 (silent event-drop on overflow), STR-010 (drain-time slow-subscriber policy) together form a coherent protocol gap: the server exposes ordinals but clients have no way to use them.

7. **Playground authentication is underspecified.** TNT-005, TNT-006, WPP-008 converge on the playground's auth story: `apiKey` mode, `dev` mode HMAC, and "standard API-key auth path" all point to §10.2 content that does not exist.

---

*End of Iteration 3 summary. 107 findings catalogued; iter3 is the final iteration per the review-and-fix skill's 3-iteration default.*
