# Technical Design Review Findings — 2026-04-20 (Iteration 5)

**Document reviewed:** `spec/`
**Review framework:** `spec-reviews/review-povs.md`
**Iteration:** 5 of N (continuing until 0 Critical/High/Medium findings remain)
**Total findings:** ~99 across 25 review perspectives

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 7     |
| Medium   | 19    |
| Low      | 69    |
| Info     | 4     |

### Critical Findings

No Critical findings in this iteration.

### High Findings

| # | Perspective | Finding | Section |
|---|-------------|---------|---------|
| OBS-031 | Observability | `RedisUnavailable` runbook trigger references an undefined alert — **Fixed** | §17.7 line 740; §16.5 line 482 |
| OBS-032 | Observability | `MinIOUnavailable` runbook trigger references an undefined alert — **Fixed** | §17.7 line 760; §16.5 |
| CMP-054 | Compliance | Legal-hold escrow bucket is platform-singular — no per-region escrow pipeline — **Fixed** | §12.* escrow pipeline + T4 topology |
| CMP-057 | Compliance | `complianceProfile` downgrade has no ratchet — silent HIPAA→none regression — **Fixed** | `PUT /v1/admin/tenants/{id}`; §12.9 |
| CRD-015 | Credential | Credential deny-list keying contract broken for user-scoped revocation — **Fixed** | §4.9 lines ~1348, ~1484, ~1645, ~1658 |
| BLD-012 | Build Sequence | `lenny-ops` has no phase assignment yet is mandatory from first chart install (Phase 3.5) | §18 Phase 3.5 |
| POL-023 | Policy Engine | Admin API `POST /v1/admin/circuit-breakers/{name}/open` body schema under-specified — no scope field | §15.1; §11.6 |

### Severity Calibration Note

Iter5 severity anchors to the iter4 rubric. Two High findings (OBS-031/032) are direct regressions of the iter4 OBS-023 class (High-severity runbook→alert reference gap); their severity is inherited. Three High findings (CMP-054, CMP-057, CRD-015) are genuine correctness/compliance contract breaks surfaced as second-order effects of iter4 fixes. Two High findings (BLD-012, POL-023) are build-order / API-schema gaps with no runtime workaround for the affected path.

---

## Detailed Findings by Perspective


---

# Perspective 1 — Kubernetes Infrastructure (iter5)

## Scope and method

Iter5 is a COMPACT re-run of the Kubernetes perspective anchored to the iter4 fix commit (`5c8c86a`). No spec commits have landed since that commit, so this iteration operates on the same tree iter4 declared complete.

Method:
1. Re-read the iter4 K8S findings index (`K8S-040` through `K8S-050`) and their per-finding `Fix applied:` paragraphs.
2. Spot-check the spec at each claimed fix site to verify the fix is present and does not regress invariants elsewhere.
3. Re-evaluate the iter4 Lows (K8S-046 through K8S-050) against the iter5 severity rubric (`feedback_severity_calibration_iter5.md`): forward-compat hedges / defense-in-depth gaps that are not correctness bugs stay at Low.
4. Scan for NEW correctness / reliability / security issues in the K8S surfaces touched by the iter4 fixes (CRD ownership, admission plane, WPC/PSC leader handoff, PDB, preflight inventory).

No new High/Medium/Critical Kubernetes issues were surfaced by the spot-checks. The iter4 Lows remain accurate but all fall into the "forward-compat hedge / defense-in-depth" bucket the calibration note marks as Low-only, so none block convergence.

## Verification of iter4 fixes

### KIN-015. iter4 K8S-040 — Phase 8 `lenny-drain-readiness` deployment [High] [Verified Fixed]

**Section:** `spec/18_build-sequence.md` Phase 8 row; `spec/17_deployment-topology.md` §17.2 item 11.

**Verification:** Phase 8 of `spec/18_build-sequence.md` now contains an explicit "Admission control enforcement — deploy `lenny-drain-readiness`" block that mirrors the Phase 5.8 / Phase 13 template: `replicas: 2`, `podDisruptionBudget.minAvailable: 1`, `failurePolicy: Fail`, `lenny.dev/component: admission-webhook` pod label, `DrainReadinessWebhookUnavailable` alert wiring, and inclusion in both the `lenny-preflight` inventory and `admission_webhook_inventory_test.go`. The Phase 8 milestone row also gained "pre-drain MinIO health check admission gate enforced". The deferral note in Phase 3.5 now resolves in the phase it pointed to.

**Residual risk:** None at High severity. The only adjacent cleanup (the "continuous inventory" gap from K8S-046) remains Low (see KIN-021 below).

---

### KIN-016. iter4 K8S-041 — Admission-webhook NetworkPolicy scope [Medium] [Verified Fixed]

**Section:** `spec/13_security-model.md` §13.2; `spec/17_deployment-topology.md` §17.2.

**Verification:** The §13.2 NetworkPolicy row for admission webhooks is split into (a) a base egress rule keyed on `lenny.dev/component: admission-webhook` allowing only `kube-system` CoreDNS and (b) a narrower egress rule keyed additively on `lenny.dev/webhook-name: drain-readiness` permitting TCP to the gateway internal port. The NET-047/NET-050 canonical-selector invariant is extended with "Exception 2 (NET-068)" permitting additive per-pod keys on egress only; `lenny-preflight` selector-consistency audits flag additive keys on the ingress side as failures. §17.2 documents the additive label's egress-only scope. The least-privilege gap on the seven non-drain webhooks is closed.

---

### KIN-017. iter4 K8S-042 — `lenny-sandboxclaim-guard` PATCH/PUT phase reference [Medium] [Verified Fixed]

**Section:** `spec/04_system-components.md` §4.6.1.

**Verification:** The webhook rule now explicitly reads `Sandbox.status.phase` via `SandboxClaim.spec.sandboxRef` rather than the non-existent `SandboxClaim.status.phase`, matching the §6.2 state-machine authority. §4.6.3 enumerates the legal `SandboxClaim.status.phase` values, with the gateway as the sole writer. The double-claim-prevention semantics are intact.

---

### KIN-018. iter4 K8S-043 — PSC circuit-breaker leader-handoff continuity [Medium] [Verified Fixed]

**Section:** `spec/06_warm-pod-model.md` §6.1; `spec/04_system-components.md` §4.6.2, §4.6.3.

**Verification:** The "Circuit-breaker state persistence across PSC leader failover" contract in §6.1 introduces three PSC-owned fields on `SandboxWarmPool.status.sdkWarmCircuitBreaker` — `openedAt`, `openedReason`, `minOpenUntil` — written in the same SSA apply that sets `spec.sdkWarmDisabled: true`. The new leader keeps the breaker open until `minOpenUntil` elapses. `sdkWarmCircuitBreakerMinOpenDuration` defaults to 30 min and is operator-configurable via `scalingPolicy.sdkWarmCircuitBreakerMinOpenSeconds`. The operator override (`circuitBreakerOverride: "enabled"`) unconditionally clears all three status fields. §4.6.3 has the ownership carve-out (PSC owns `status.sdkWarmCircuitBreaker.*`; WPC owns the rest of `status.*`); §4.6.2 adds the responsibility bullet and PSC RBAC gains `get`/`patch` on the `SandboxWarmPool` status subresource. The design accepts rolling-window cold-start after failover as the documented trade-off — the `minOpenUntil` floor ensures a crash shortly after trip cannot silently re-enable SDK-warm.

**Residual risk:** None at Medium. The observer-gap on `SandboxWarmPool.status` (K8S-050) remains Low (KIN-025).

---

### KIN-019. iter4 K8S-044 — Fallback claim path mirror staleness precondition [Medium] [Verified Fixed]

**Section:** `spec/04_system-components.md` §4.6.1.

**Verification:** The fallback claim path is now gated on two preconditions: (1) `lenny_agent_pod_state_mirror_lag_seconds ≤ podClaimFallbackMaxMirrorLagSeconds` (default 10 s) and (2) a lightweight API-server readiness probe (needed because `lenny-sandboxclaim-guard` is `failurePolicy: Fail`). Failure of either precondition returns `WARM_POOL_EXHAUSTED` and increments `lenny_pod_claim_fallback_skipped_total{reason=mirror_stale|apiserver_unreachable}`. Separately, the WPC now bulk-UPSERTs `agent_pod_state` from a fresh `Sandbox` list on startup and every leader-election acquisition and deletes orphan rows, establishing a post-outage convergence invariant. This closes both the stale-mirror double-claim window and the `failurePolicy: Fail` collapse window.

---

### KIN-020. iter4 K8S-045 — `lenny-pool-config-validator` PSC wedge [Medium] [Verified Fixed]

**Section:** `spec/04_system-components.md` §4.6.3; `spec/16_observability.md`; `spec/18_build-sequence.md` Phase 3.5.

**Verification:** §4.6.3 now specifies the PSC's admission-denial policy: HTTP 400/403 denials trigger exponential backoff (initial 1 s, max 60 s), increment `lenny_pool_scaling_admission_denied_total{pool, reason}`, and — after 10 consecutive denials on the same pool — emit `PoolScalingAdmissionStuck` and stop retrying until operator intervention. Phase 3.5 integration tests include the stale-Postgres scenario. The wedge loop is broken without weakening the rule-set-1 enforcement on PSC writes.

---

## Carry-forward Lows (no blocker under severity calibration)

### KIN-021. iter4 K8S-046 — `lenny-preflight` detects drift only at install/upgrade [Low] [Carry-forward]

**Status:** Not addressed in iter4 fix commit. Remains Low.

Under the iter5 calibration this is a forward-compat / defense-in-depth hedge — the documented `*Unavailable` alerts cover reachability of deployed webhooks, and `helm rollback` / `kubectl delete` are out-of-band admin actions outside the build-sequence contract. Elevation would only be warranted if a shipping feature relied on continuous-inventory detection; none does. A continuous inventory check is a reasonable hardening for a future iteration but not a correctness defect today.

### KIN-022. iter4 K8S-047 — Kata PDB scope [Low] [Carry-forward]

**Status:** Not addressed. Remains Low.

Cross-template simultaneous drain on a small Kata node pool is a capacity-planning concern already implicit in §17.8 sizing guidance. Single-template `maxUnavailable: 1` remains correct per Kubernetes PDB semantics. Elevation to Medium requires a shipping contract guaranteeing cross-template concurrent-disruption bounds, which is not promised.

### KIN-023. iter4 K8S-048 — Admission-webhook topology spread [Low] [Carry-forward]

**Status:** Not addressed. Remains Low.

Zonal-failure HA for admission webhooks is a deployment hardening lever; `replicas: 2` + `PDB minAvailable: 1` provides voluntary-disruption protection, which is what the spec contracts. Topology spread is valuable but is a forward-compat hedge per the calibration note.

### KIN-024. iter4 K8S-049 — `Sandbox` finalizer orphan-reclaim leader fence [Low] [Carry-forward]

**Status:** Not addressed. Remains Low.

The failure mode requires a narrow WPC leader-failover window between the `SandboxClaim` DELETE and the `Sandbox` `claimed → idle` transition. The existing orphan-`SandboxClaim` GC loop will recover the next cycle if the in-flight state is re-observed; the stranded `claimed`-but-no-claim case is a narrow, recoverable gap. An idempotent reclaim protocol listing both orphan claims and stranded-`claimed` Sandboxes is a reasonable hardening; not a blocker.

### KIN-025. iter4 K8S-050 — PSC observer gap on `SandboxWarmPool.status` [Low] [Carry-forward]

**Status:** Not addressed. Remains Low.

The PSC's scaling formula can tolerate the WPC's 25 s failover window because target sizing is expressed in pods-per-second-of-lag rather than instantaneous state; the formula already includes `failover_seconds + pod_startup_seconds` headroom. A staleness guard on `status.lastTransitionTime` is a legitimate hardening but the absence of one is not a correctness defect at the specified scale.

---

## New issues surfaced in iter5

None. Spot-checks of the iter4 fix sites (§4.6.1 fallback preconditions, §4.6.1 admission-guard phase reference, §4.6.2 PSC status ownership carve-out, §4.6.3 admission-denial backoff, §6.1 circuit-breaker persistence, §13.2 NetworkPolicy split, §17.2 additive label exception, §18 Phase 8 drain-readiness deployment) did not turn up regressions, contradictions with adjacent invariants, or new correctness bugs.

The iter4 baseline list of WPC/PSC leader-election hazards, admission-plane HA contracts, and CRD ownership boundaries is closed to the Medium/High bar after iter4's fixes.

---

## Convergence assessment

**Counts (new findings this iteration):**
- Critical: 0
- High: 0
- Medium: 0
- Low: 5 (all iter4 K8S-046 – K8S-050 carry-forwards; re-labelled KIN-021 – KIN-025 for continuity)
- Info: 0

**Verified fixed from iter4:** K8S-040 (High), K8S-041 (Medium), K8S-042 (Medium), K8S-043 (Medium), K8S-044 (Medium), K8S-045 (Medium) — six fixes held.

**Converged (Y/N):** **Y** — zero new Critical/High/Medium findings; all prior C/H/M findings verified fixed; remaining Lows are forward-compat hedges that do not block convergence under the iter5 severity rubric.

---

# Perspective 2 — Security & Threat Modeling (iter5)

**Scope:** Verify iter4 SEC-008 through SEC-013 fixes; surface only NEW findings with concrete attack paths.
**Method:** Spot-check spec sections named in iter4 resolutions; evaluate against the iter4 severity rubric.
**Numbering:** SEC-017+ (iter4 ended at SEC-016; SEC-014–SEC-016 absent from iter4 SEC section).

---

## 1. Iter4 finding verification

### SEC-008 — Upload security controls (zip-bomb / symlink / traversal) [High — Fixed]

**Verified.** `spec/07_session-lifecycle.md` §7.4 now encodes every normative validator the iter4 resolution promised: 256 MiB decompressed cap, 100:1 ratio cap, 10 000 entries, 64 MiB per-entry cap, 32 path depth, 4 096 B path length, zip-slip canonicalization, outright rejection of `hardlink`/`character-device`/`block-device`/`FIFO`/`socket`, symlink blocklist for `/proc`, `/sys`, `/dev`, `/run/lenny`. `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` with all nine `details.reason` sub-codes is in §15.1 (line 1072) and the §13.4 summary cross-references §7.4, §8.7, §15.1, §16.1. §13.5 §13.4 list at lines 657-666 mirrors the normative ceilings. No residual gap.

### SEC-009 — Exported workspace files bypass `contentPolicy.interceptorRef` [High — Deferred]

**Status unchanged (still deferred pending user input).** §13.5 "Residual risk — file export content" (line 681) still explicitly documents the gap and points to §8.7 for deployer-side mitigations. The five open questions from iter4 remain unanswered. This is an acknowledged architectural gap, not a regression. No iter5 action possible without user direction on question (a).

### SEC-010 — Trust-based chained-interceptor exception [High — Fixed iter4]

**Verified.** `spec/08_recursive-delegation.md` §8.3 interceptorRef list (lines 131-136) shows the four surviving conditions with the "chained interceptor (trust-based)" option removed. Condition 4 (`CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION`) unconditionally rejects different non-null references and explicitly states out-of-band chaining claims are not accepted; deployers needing composition must keep `interceptorRef` identical across the boundary. Identity-based monotonicity is restored.

### SEC-011 — `lenny-cred-readers` group scope [Medium — Fixed iter4]

**Verified.** §13.1 "`lenny-cred-readers` membership boundary" (line 27) enumerates the two UIDs (adapter + agent), rejects non-adapter/non-agent containers at admission via `POD_SPEC_CRED_GROUP_OVERBROAD`, and documents the subprocess `setgroups(0, NULL)` advisory. §13.1 "Concurrent-workspace mode credential-read scope" (line 29) explicitly folds cross-slot credential readability into the existing `acknowledgeProcessLevelIsolation` deployer flag and emits the `ConcurrentWorkspaceCredentialSharing=True` warning condition. §5.2 pool-validation rejection message (line 494) now lists "shared credential-file group-read access" alongside the other four co-tenancy properties. Scope is narrower than the recommendation (no per-slot GIDs, no AppArmor/Seccomp mandate) but the resolution note explicitly justifies this as commensurate with the already-accepted co-tenancy posture — fix is adequate against the stated threat model.

### SEC-012 — Admin-time RBAC live-probe caller identity [Medium — Fixed iter4]

**Verified.** §4.9 "Admin-time RBAC live-probe (required)" (line 1209) specifies the Token-Service-owned probe over mTLS, the Token Service's `SelfSubjectAccessReview`+`get` sequence, the `{ALLOWED, DENIED, NOT_FOUND}` return set, explicit forbiddance of (a) gateway impersonation via `TokenRequest`/`Impersonate-*` and (b) gateway-SA `SelfSubjectAccessReview`, mapping of `DENIED`/`NOT_FOUND` to 422 `CREDENTIAL_SECRET_RBAC_MISSING`, and the new 503 `CREDENTIAL_PROBE_UNAVAILABLE` (line 984 in §15.1) for probe-transport failures. The handler MUST NOT fail-open on probe errors. Bootstrap-seeded pools are correctly excluded (atomic RBAC rendering). No residual gap.

### SEC-013 — Interceptor weakening cooldown timestamp immutability [Medium — Fixed iter4]

**Verified.** §8.3 rule 5 (line 168) now states `transition_ts` is server-minted from the gateway's monotonic clock-synchronized wall clock, client-supplied values are rejected with `INTERCEPTOR_COOLDOWN_IMMUTABLE` (line 1003 in §15.1) before any state change persists, the cooldown duration is cluster-scoped (Helm value, not admin-API field), a meta-cooldown preserves each pending cooldown against cluster-config reductions, and rejected `INTERCEPTOR_COOLDOWN_IMMUTABLE` attempts are audited. The hash-chained `interceptor.fail_policy_weakened` event in §11.7 carries writer identity and transition metadata. The `interceptors:policy-admin` role split was deliberately dropped as unnecessary once the cooldown moved to cluster-config domain. Fix aligns with the stated threat model (compromised `interceptors:write` credential cannot collapse the cooldown).

---

## 2. New findings

**None.** After spot-checking every iter4 SEC fix and re-examining the surrounding code paths (concurrent-workspace credential sharing, probe transport semantics, cooldown oscillation, archive extraction location), no new concrete attack paths were identified that would warrant Medium or higher severity under the iter5 calibration rubric. The residual subprocess-`setgroups` advisory in §13.1 (runtime-author responsibility, not platform-enforced) and the file-export scanning gap (SEC-009, still deferred) are the known outstanding items — both pre-existing, both explicitly scoped as acceptable-with-acknowledgment or deferred-pending-input. Neither has a new attack path that wasn't already captured.

Theoretical defense-in-depth polish considered and rejected as Low/Info (per calibration rule — no concrete attack path):

- Platform could validate runtime-author `setgroups(0, NULL)` enforcement via container image scanner → already covered by the §13.1 single-tenant trust-boundary framing.
- `INTERCEPTOR_COOLDOWN_IMMUTABLE` rejection audit event could be categorized separately from the generic admin-API denial audit → cosmetic, not security-material.
- The probe `details` field listing every failing `resourceName` for batch pool creation marginally expands the RBAC-structure signal a compromised admin credential learns. Materially no new information beyond what `kubectl auth can-i` would already reveal under the same credential; not a new attack path.

---

## 3. Convergence assessment

**Iter4 SEC findings status after iter5 verification:**
- Fixed & verified: 5 (SEC-008, SEC-010, SEC-011, SEC-012, SEC-013)
- Deferred pending user input: 1 (SEC-009)
- New iter5 findings: 0 Critical, 0 High, 0 Medium, 0 Low/Info

**Convergence: YES** for the Security & Threat Modeling perspective. All actionable iter4 findings are fixed; the single outstanding deferral (SEC-009) is gated on user-level architectural direction and cannot progress without that input, which is the documented project convention (`feedback_proposal_before_edit.md`).

---

# Perspective 3: Network Security & Isolation — Iteration 5

**Scope recap.** Evaluate the spec's network architecture from a defense perspective: the three agent-namespace NetworkPolicy manifests and their `lenny-system` counterparts, cross-namespace selector shapes, lateral-movement surface between pods in the same pool namespace, dedicated CoreDNS operability, network paths that bypass the gateway, and the sufficiency of the mTLS PKI without a service mesh.

**Convergence anchor.** iter4 closed NET-061 through NET-068 as Fixed and carried NET-069 (Low) as the only open Network-perspective finding. I re-inspected every iter4 fix in the current spec and all remain in place: the three agent-namespace policies (default-deny + allow-gateway-ingress + allow-pod-egress-base) and the supplemental `allow-pod-egress-llm-proxy` / `allow-pod-egress-otlp` policies are intact and default-deny-first (spec/13_security-model.md §13.2, lines 35-172); the `lenny-system` component allow-list table (line 214-224) uses the canonical `lenny.dev/component` selector per NET-047/NET-050 with the documented `app:` exception for `lenny-ops`/`lenny-backup`/upstream-chart workloads; the DNS egress `podSelector` parity rule (NET-067, line 211) is enforced on every DNS peer including the `lenny-ops-egress` rule; the interceptor mTLS peer-validation row (NET-063) is present in the §10.3 cert lifecycle table and the `global.spiffeTrustDomain` / `global.saTokenAudience` values are required with no default (NET-064); the `lenny-drain-readiness` additive `lenny.dev/webhook-name` label (NET-068) is rendered in §17.2 and scoped to egress only; the OTLP TLS requirement (OTLP-068) has both the chart guard + preflight TLS handshake probe + `lenny_otlp_export_tls_handshake_total` counter + `OTLPPlaintextEgressDetected` critical alert (§13.2 line 176-178, §16.1 line 255-256, §16.5 line 410). The three new findings below are all residual defense-in-depth gaps; the perspective does not surface any new Critical or High. **Zero Critical/High**; three Medium/Low; one carry-over Low.

Findings ordered by severity.

---

### NET-070 `lenny-ops` → gateway admin API uses plaintext HTTP by default [Medium] — **Fixed**

**Resolution.** Flipped `ops.tls.internalEnabled` default from `false` to `true` in every non-dev profile, rewrote the §25.4 "TLS" subsection to frame internal TLS as the default and plaintext as an explicit opt-out, and added the `ops.acknowledgePlaintextAdminAPI` Helm-value guard that fails `helm install`/`helm upgrade` when `internalEnabled: false` is set without the acknowledgment outside dev mode (dev mode auto-implies the acknowledgment). Updated `GatewayClient` prose so the default `baseURL` is `https://lenny-gateway:8443` (the gateway's internal TLS port, reusing the existing cert-manager chain from §10.3) and `http://lenny-gateway:8080` is rendered only under the acknowledged-plaintext path. Added the `lenny_ops_admin_api_tls_handshake_total{result}` metric to §16.1 adjacent to the existing OTLP handshake metric and the symmetric `OpsAdminAPIPlaintextDetected` critical alert to §16.5 adjacent to `OTLPPlaintextEgressDetected`. Added an `ops-admin-tls` preflight handshake probe to the §17.9 check table adjacent to the `otlp-tls` entry, enforcing the acknowledgment-guard check at install/upgrade time. Updated the §13.2 `lenny-ops` row and Gateway row component matrix so the rendered NetworkPolicy allows TCP 8443 (TLS) by default and TCP 8080 (plaintext) only under the acknowledged opt-out; the counterparty-rules note now requires the `lenny-preflight` audit to verify that the egress port matches the gateway ingress port to prevent split-brain rendering. Also updated the `lenny-ops-egress` NetworkPolicy YAML comment block in §25.4 and the `gatewayURL` field in the `GET /v1/admin/me` response example to reflect the new default. The fix is fully symmetric with the iter4 OTLP-068 TLS-default hardening as the finding recommended.

**Section:** spec/25_agent-operability.md §25.4 "TLS" (line 2504-2508), §25.4 `GatewayClient` (line 1838-1846), §13.2 `lenny-system` component matrix `lenny-ops` row (line 224).

The `lenny-ops` → gateway admin-API link runs over plaintext HTTP on TCP 8080 in the default posture. The `GatewayClient` struct in §25.4 declares `baseURL: http://lenny-gateway:8080`, and the "TLS" subsection explicitly frames internal TLS as opt-in: "For zero-trust clusters where internal traffic must also be encrypted, deployers enable `ops.tls.internalEnabled`. ... This is opt-in — most clusters rely on Ingress TLS termination and trust the internal network." The `ops.tls.internalEnabled: false` default (line 881) means out-of-box deployments carry admin-level flows in plaintext between namespaces (or pods) inside the cluster. These flows include: pool config reads/writes, connector configuration and probe results, platform upgrade state, scaling commands, per-replica event-buffer queries, diagnostics pod-log/event proxy responses, and the audit-bearing operational event stream (§25.5). The same link transports the `lenny-ops-sa` JWT in the `Authorization: Bearer` header on every request — a bearer token with `platform-admin` scope (§25.4 line 1834).

This is the only platform-to-platform link in Lenny's default posture that is not TLS-protected. Every other link has been hardened with TLS-on-by-default in prior iterations: gateway ↔ Token Service uses mTLS (§10.3, NET-060/NET-064), gateway ↔ Redis uses TLS on port 6380 (§13.2 Gateway row), gateway ↔ MinIO uses TLS on port 9443 (§13.2), gateway ↔ Interceptor uses mTLS with full peer validation (§10.3, NET-063), and gateway/pod ↔ OTLP collector was hardened to TLS-default in iter4 (OTLP-068). The `lenny-ops` ↔ gateway link is the last remaining plaintext-default hop, and the data flowing over it (admin JWTs, audit records, pool configs that encode credentials indirectly) matches the confidentiality profile that the OTLP-068 fix deemed unacceptable at plaintext (the iter4 rationale for OTLP TLS-default was: "trace payloads carry session metadata, tenant/operation identifiers, and occasional error bodies … in-scope for the same confidentiality posture as the other cross-namespace platform links"; this finding argues the same reasoning applies to admin-API RPC).

The `lenny-system` default-deny policy prevents arbitrary pods from eavesdropping on the gateway listener, and the NetworkPolicy allow-list restricts ingress on gateway TCP 8080 to `app: lenny-ops` and admission-webhook pods only (§13.2 line 216). That mitigates a casual eavesdropper from a rogue workload, but it does not protect against: (a) a compromise of any sidecar or second-container co-located in the `lenny-ops` pod or the gateway pod (container-in-pod shared netns; TLS would require the attacker to additionally exfiltrate client cert material), (b) a CNI-plane attacker who sniffs the Linux dataplane below the NetworkPolicy enforcement layer, (c) tenants with cluster-wide read access through a managed-Kubernetes provider's debug path, or (d) the admin JWT being lifted by any of the above vectors and subsequently replayed from a pod that the NetworkPolicy does permit to reach TCP 8080 (the admission-webhook allow-list peer, for example, is a legitimate peer that lacks any identity binding to `lenny-ops`).

Severity anchored at Medium rather than High because (a) this is a single in-cluster hop rather than a broad surface, (b) the spec documents the opt-in `internalEnabled` path and the data exposure framework honestly ("zero-trust clusters"), (c) a real exploit requires additional privilege escalation beyond the NetworkPolicy boundary, and (d) the iter4 OTLP-068 fix was anchored at Critical in iter3 because trace payloads carry tenant-level metadata that must not be visible to any in-cluster interceptor; admin JWTs and admin-API bodies are at least as sensitive. Calibrating against iter4's OTLP-068 rubric (which ended in a TLS-default for production profiles with an explicit plaintext opt-in acknowledgment) argues for parity; Medium rather than High because the deployer opt-in path exists and the call site is narrower than the agent-runtime-wide OTLP exporter surface.

**Recommendation.** Flip `ops.tls.internalEnabled` default to `true` in every non-dev profile and require the gateway to listen on TLS on the internal admin port (co-located with the existing TLS terminator on the Ingress-facing port; the gateway already owns a certificate chain via cert-manager per §10.3). Rename `ops.tls.internalEnabled` to align with the OTLP-068 naming (`observability.acknowledgeOtlpPlaintext` pattern), adding an explicit `ops.acknowledgePlaintextAdminAPI: true` Helm value required when a deployer sets `internalEnabled: false` outside dev mode — the chart fails `helm install`/`helm upgrade` with a message pointing at this finding if the acknowledgment is absent. Add a `lenny-preflight` live handshake probe that opens the admin port and verifies TLS 1.2+ handshake with the cluster trust bundle when `internalEnabled: true`, mirroring the `otlp-tls` check added in iter4 (§17.9). Emit `lenny_ops_admin_api_tls_handshake_total{result}` with a `plaintext` bucket populated whenever `lenny-ops` connects to the gateway without negotiating TLS, and wire an `OpsAdminAPIPlaintextDetected` critical alert symmetric with `OTLPPlaintextEgressDetected` (§16.5). If the acknowledged-plaintext path is retained, document the bearer-token replay risk explicitly: the admin JWT transits in cleartext and any entity able to read it can escalate to `platform-admin` from any pod that the NetworkPolicy allows to reach TCP 8080 (admission webhooks, the Ingress controller ingress peer, etc.). The alternative — requiring `internalEnabled: true` with no plaintext opt-in — is the cleaner symmetry with the rest of the platform and is consistent with the iter4 NET-063 / gateway-to-interceptor mTLS mandate which also disallows plaintext outright.

---

### NET-071 `lenny-ops-egress` hardcodes private/IMDS `except` entries that the spec promises are templated [Low]

**Section:** spec/25_agent-operability.md §25.4 `lenny-ops-egress` webhook rule (lines 1254-1284), spec/13_security-model.md §13.2 `allow-gateway-egress-llm-upstream` (lines 330-412), §13.2 NET-057 shared block-list note (line 416).

The `lenny-ops-egress` NetworkPolicy's internet-facing `ipBlock` peers at §25.4 lines 1268-1281 hardcode literal private-range and IMDS CIDRs:

```
except: [
  "{{ .Values.egressCIDRs.excludeClusterPodCIDR }}",
  "{{ .Values.egressCIDRs.excludeClusterServiceCIDR }}",
  10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16,
  169.254.169.254/32, 100.100.100.200/32
]
```

and for IPv6:

```
except: [
  {{- with .Values.egressCIDRs.excludeClusterPodCIDRv6 }} "{{ . }}", {{- end }}
  {{- with .Values.egressCIDRs.excludeClusterServiceCIDRv6 }} "{{ . }}", {{- end }}
  fc00::/7, fe80::/10, fd00:ec2::254/128
]
```

Only the cluster pod/service CIDRs are rendered from Helm values; the private ranges (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16`, `fc00::/7`, `fe80::/10`) and the IMDS addresses (`169.254.169.254/32`, `100.100.100.200/32`, `fd00:ec2::254/128`) are literal CIDRs baked into the manifest. By contrast, the §13.2 `allow-gateway-egress-llm-upstream` rule at lines 373-377 and 402-406 renders the private ranges through `{{- range .Values.egressCIDRs.excludePrivate }}` so the shared Helm list is the real source of truth for that rule. The two rules therefore differ in how they respond to a deployer override of `egressCIDRs.excludePrivate` or `egressCIDRs.excludeIMDS`: the gateway rule tracks the override; the ops-egress rule does not.

This directly contradicts the surrounding explanatory comments and prose. Lines 1254-1264 claim: "Private/link-local ranges — rendered from the shared `egressCIDRs.excludePrivate` Helm value (NET-057). Every entry MUST also appear in the same-family `except` block of the gateway `allow-gateway-egress-llm-upstream` rule" and "IMDS addresses — `egressCIDRs.excludeIMDS` (NET-044), mirroring the gateway rule". Line 1284 claims: "the chart partitions the shared `egressCIDRs.excludePrivate`, `egressCIDRs.excludeIMDS`, and cluster pod/service CIDR values at render time (NET-062). ... The `except` block is the same list rendered into the gateway `allow-gateway-egress-llm-upstream` NetworkPolicy (§13.2) via the shared `egressCIDRs.excludePrivate`, `excludeClusterPodCIDR`/`excludeClusterServiceCIDR`, and `excludeIMDS` Helm values". The §13.2 NET-057 normative note at line 416 further states: "`egressCIDRs.excludePrivate` Helm value ... is the single source of truth ... the chart renders both peers into both the gateway `allow-gateway-egress-llm-upstream` rule above and the `lenny-ops-egress` webhook rule".

The practical risk is bounded: the `lenny-preflight` selector-consistency audit described at §13.2 line 416 ("validates that every entry in `egressCIDRs.excludePrivate` appears in the `except` block of the same-family `ipBlock` peer on both … rules") is fail-closed and would refuse an install where a deployer override of `excludePrivate` does not appear in both rules. So the default posture is safe (preflight passes because the literal defaults match the default Helm values) and a misaligned override is caught at install rather than producing a silent SSRF bypass. The finding is primarily a spec-manifest correctness issue: the YAML does not implement the contract the documentation describes, and a future maintainer editing either list in isolation will encounter behavior that surprises them. This is the same class of defect iter4 NET-050 and NET-067 were anchored at (selector / peer consistency issues where the policy was functionally correct but diverged from normative documentation) — both Low.

An adjacent sub-issue: because the literal CIDRs in `lenny-ops-egress` are duplicated from the Helm value defaults rather than referenced, a deployer who extends `egressCIDRs.excludePrivate` to include a new range (e.g., CGNAT `100.64.0.0/10`, or a deployer-specific corporate internal range like `172.20.0.0/12` that intersects with RFC1918 but with non-standard carve-outs) will see the new entry flow into the gateway rule but not into `lenny-ops-egress`. Preflight catches this case. But a deployer who extends only `excludeIMDS` (e.g., adding Oracle Cloud's IMDS address `192.0.0.192/32` or future provider ranges) will find the same asymmetry, and the preflight check described in §13.2 line 416 explicitly covers `excludePrivate` — the text does not guarantee the same check for `excludeIMDS`. A detailed read of the prose around line 416 says "validates that every entry in `egressCIDRs.excludePrivate` appears in the `except` block" without mentioning `excludeIMDS` by name in the same check (though a broader NET-065 cluster-CIDR audit is described). The preflight contract for `excludeIMDS` override symmetry is therefore underspecified.

**Recommendation.** Convert the `lenny-ops-egress` `except` lists at §25.4 lines 1268-1281 to use the same `{{- range .Values.egressCIDRs.excludePrivate }}` / family-partitioning template shape that §13.2 `allow-gateway-egress-llm-upstream` uses (lines 373-377 and 402-406). Alternatively, if duplication is preferred for readability, annotate the literals inline with a `# MUST equal default of egressCIDRs.excludePrivate — see NET-057` comment and require the `lenny-preflight` selector-consistency audit to compare the rendered literal list against the live Helm value (not merely against the default) — failing closed if any deployer override diverges between the two rules. Extend the preflight description in the §13.2 NET-057 normative note to name `excludeIMDS` symmetry explicitly (not only `excludePrivate`) and cover it in the same audit: the prose currently reads "validates that every entry in `egressCIDRs.excludePrivate` appears…" — append "and every entry in `egressCIDRs.excludeIMDS` appears…" so the check scope matches the documented contract.

---

### NET-072 App-layer SSRF defaults omit Alibaba IMDS `100.100.100.200/32` [Low]

**Section:** spec/25_agent-operability.md §25.4 `ops.webhooks.blockedCIDRs` default (line 895-907), §25.4 "Callback URL validation" (line 2699-2706).

The `ops.webhooks.blockedCIDRs` Helm default (§25.4 line 902-907) and the webhook-delivery "Metadata service" allow-list at line 2704 both omit Alibaba Cloud's IMDS address (`100.100.100.200/32`). The network-layer NetworkPolicy rules on all three surfaces (gateway `allow-gateway-egress-llm-upstream` §13.2 line 380, `lenny-ops-egress` §25.4 line 1272, agent `internet`-profile egress §13.2 line 450) include `100.100.100.200/32` in their `except` blocks — so network-layer blocking is in place. But the app-layer SSRF defense-in-depth rendered by the chart does not mirror this. Line 895-907 claims: "Default list mirrors `egressCIDRs.excludePrivate` so that NetworkPolicy and app-layer share one block list at install time (NET-057)". The default list — `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16`, `fc00::/7`, `fe80::/10` — does mirror `excludePrivate`, but `excludePrivate` does not cover IMDS addresses (those are in a separate `excludeIMDS` list). The explicit "Metadata service" list at line 2704 names `169.254.169.254`, `metadata.google.internal`, `fd00:ec2::254` as blocked "regardless of other rules", but Alibaba's `100.100.100.200` is missing from that list too. The `169.254.0.0/16` block at line 2703 covers AWS/GCP/Azure IPv4 IMDS (which is inside `169.254/16`) but not Alibaba IMDS (which is a publicly-routable `100.100.100.200`, outside link-local, outside RFC1918). Net result: app-layer SSRF validation accepts a webhook URL that resolves to `100.100.100.200`, and only the network-layer NetworkPolicy blocks the call.

Two reasons app-layer defense-in-depth matters here: (a) the spec calls it out as a hard requirement ("Application-layer SSRF checks are a hard requirement, not a fallback: NetworkPolicy alone cannot stop a legitimate public hostname that resolves (via attacker-controlled DNS or a reused public IP) to a CIDR outside this block list" — §13.2 line 418); (b) the NetworkPolicy boundary can fail open under CNI-plane bugs, partial enforcement during CNI upgrades, or dual-stack misconfiguration, and the app-layer check is the last line of defense for a DNS-rebinding webhook URL that resolves at delivery time to the Alibaba IMDS address. A tenant-influenced webhook URL could, via DNS response manipulation, resolve to `100.100.100.200` at delivery time on an Alibaba Cloud deployment, and the `lenny-ops` webhook deliverer would attempt the connection — which the NetworkPolicy would block, but the app-layer validation would not. Defense-in-depth expects both layers to block.

This is a small, targeted gap rather than a broad SSRF bypass: Alibaba Cloud is one deployment environment among several, and the network-layer blocking is in place. The severity is Low because the network-layer boundary catches it. But the claim at line 895-907 that "Default list mirrors `egressCIDRs.excludePrivate` so that NetworkPolicy and app-layer share one block list" is only half-true — it mirrors `excludePrivate` but not `excludeIMDS`, while the NetworkPolicy rules include both. App-layer parity with the NetworkPolicy rules is the explicit goal the spec states.

**Recommendation.** Extend the default `ops.webhooks.blockedCIDRs` list (§25.4 line 902-907) to include the full `egressCIDRs.excludeIMDS` default values: add `169.254.169.254/32`, `100.100.100.200/32`, and `fd00:ec2::254/128` to the default block list. Add a corresponding entry `100.100.100.200` to the "Metadata service" explicit-block list at line 2704 so the prose stays in sync with the numeric default. Adjust the surrounding comment (line 895-901) from "Default list mirrors `egressCIDRs.excludePrivate`" to "Default list mirrors the union of `egressCIDRs.excludePrivate` and `egressCIDRs.excludeIMDS`" so the documentation invariant matches the actual default. Also add a preflight check that walks the chart values at install/upgrade time and fails if `ops.webhooks.blockedCIDRs` does not contain every entry of `egressCIDRs.excludePrivate ∪ egressCIDRs.excludeIMDS` — this closes the window where a future maintainer edits one list without the other.

---

### NET-069 OTLP collector `podSelector` relies on non-standard label defaults [Low] — **Carry-over from iter4**

**Section:** spec/13_security-model.md §13.2 `allow-pod-egress-otlp` (lines 149-172), §13.2 `lenny-system` OTLP Collector row (line 223).

Carried over from iter4 unchanged; the spec still has `default: app: otel-collector` at line 167. See iter4 summary NET-069 for full rationale and recommendation. The finding remains Low because it breaks trace export silently rather than breaking confidentiality; the iter4 recommendation (change default to `app.kubernetes.io/name: otel-collector` to match the upstream OpenTelemetry chart, and extend the `otlp` preflight to resolve the endpoint Service to backing pod labels) remains the path to closure.

---

## Severity Summary

| Severity | Count | IDs |
|----------|-------|-----|
| Critical | 0 | — |
| High     | 0 | — |
| Medium   | 1 | NET-070 |
| Low      | 3 | NET-071, NET-072, NET-069 (carry-over) |
| Info     | 0 | — |

**Convergence read.** Zero Critical and zero High: the network-security perimeter for this iteration is effectively converged on the structural dimensions (default-deny agent + `lenny-system` namespaces, canonical selectors, two-parallel-family `ipBlock` peers, dedicated CoreDNS HA + fail-closed, DNS-peer `podSelector` parity, mTLS on all data-plane cross-namespace links, OTLP TLS-default, SSRF symmetry between the two internet-facing `lenny-system` surfaces). The one Medium (NET-070) is a defense-in-depth decision about the default for a single admin-API hop that the spec currently frames as opt-in; closing it is a values-default change plus a preflight probe. The two Low findings (NET-071 template-shape, NET-072 app-layer SSRF parity) are documentation/manifest consistency fixes that preflight catches today. The unchanged carry-over Low (NET-069) is a labelling default.

---

## 4. Scalability & Performance Engineering

### PRF-013. Stream Proxy `maxConcurrent` per replica still lacks streams-per-session derivation [Low]

**Section:** `spec/17_deployment-topology.md` §17.8.2 (line 897), `spec/10_gateway-internals.md` §10.1, `spec/04_system-components.md` §4.1 Gateway subsystem table

**Carry-forward from iter4 PRF-011.** The Tier 3 capacity tier reference still lists `Stream Proxy maxConcurrent: 20,000` (line 897) and `Upload Handler maxConcurrent: 2,000` / `MCP Fabric maxConcurrent: 5,000` / `LLM Proxy maxConcurrent: 10,000` without derivation from the Tier 3 per-replica session budget (`maxSessionsPerReplica: 400`). 20,000 streams × 30 replicas = 600,000 aggregate concurrent streams for a 10,000-session target — a 60:1 stream-to-session ratio that may be correct but is not documented.

**Recommendation:** Add a subsection to §10.1 (or a footnote to §17.8.2) deriving each `maxConcurrent` default: `maxConcurrent = maxSessionsPerReplica × streams_per_session(component) × safety_factor`. Add `lenny_gateway_streams_per_session{p99}` observation guidance.

---

### PRF-014. HPA scale-up `stabilizationWindowSeconds: 0` paired with PDB-bound 25-min scale-down — cost/efficiency finding unchanged [Low]

**Section:** `spec/17_deployment-topology.md` §17.8.2 (lines 894, 903, 906), `spec/10_gateway-internals.md` §10.1 (line 93)

**Carry-forward from iter4 PRF-012.** The HPA combines `stabilizationWindowSeconds: 0` scale-up with 25–50 min PDB-bounded scale-down. For bursty workloads whose burst duration is shorter than the ~25-min window, the gateway never completes scale-down between bursts, so effective cost is "max-replica continuously," not the advertised auto-scaling curve.

**Recommendation:** Option A — add `stabilizationWindowSeconds: 60` to `scaleUp.behavior`. Option B — publish a brief cost-recovery worked example in §17.8.2.

---

### PRF-015. Tier 3 gateway session headroom = 20% leaves narrow room for replica failure during burst [Low]

**Section:** `spec/16_observability.md` §16.5 capacity tiers (lines 539, 545), `spec/17_deployment-topology.md` §17.8.2 (lines 891, 897, 946)

**New — surfaced by iter4 PRF-002/PRF-003 tier reconciliation.** Tier 3 sized as `maxReplicas: 30 × 400 = 12,000` against a 10,000-session target is 20% headroom. That must simultaneously cover (a) the `minReplicas` burst-absorption buffer, (b) PDB `maxUnavailable: 1` replica loss (400 sessions = 4% of target), and (c) the `GatewaySessionBudgetNearExhaustion` threshold at 90%. Under worst-case superposition, available capacity can drop below 10,000.

**Recommendation:** Add a Tier 3 superposition worked example to §17.8.2 and either widen `maxReplicas` to 35 or document the trade-off with a Helm-tunable note.

---

### PRF-016. Tier 4 (Platform) capacity targets added to §16.5 without per-replica scaling derivation [Low]

**Section:** `spec/16_observability.md` §16.5 (lines 537–547), `spec/04_system-components.md` §4.1 (line 82 Tier 4 row)

**New — introduced by iter4 Tier 4 addition.** Tier 4 at `maxSessionsPerReplica: 400` implies 250 gateway replicas for 100,000 sessions. Two undocumented second-order issues: (a) 250 replicas × `maxUnavailable: 1` × 60–120s rolling update = 4–8 hour rollout; (b) the 400-session per-replica budget hasn't been re-validated for 5,000-tenant Tier 4 aggregate workload.

**Recommendation:** Add a Tier 4 planning note under §16.5 line 547 flagging (a) linear PDB-bound rolling-update duration at Tier 4 scale, and (b) `maxSessionsPerReplica: 400` is copied from Tier 3 and requires Phase 14.5 re-calibration before Tier 4 production deployment.

---

### Convergence assessment (Perspective 4)

- Critical: 0
- High: 0
- Medium: 0
- Low: 4 (PRF-013, PRF-014 carry-forward; PRF-015, PRF-016 new)
- Info: 0

**Converged for this perspective** (0 C/H/M). No regressions introduced by iter4 fixes.

---

## 5. Protocol Design & Future-Proofing (MCP, A2A, AP, OpenAI)

### PRO-018 publishedMetadata double-fetch for list_runtimes [Low]

**Section:** `spec/15_external-api-surface.md` (list_runtimes tool); `spec/05_runtime-registry-and-pool-model.md` (publishedMetadata field)

The `list_runtimes` MCP tool returns a `PublishedMetadataRef` per runtime but does not inline a compact digest, forcing clients that want to render a runtime catalog to issue N follow-up `get_published_metadata` calls (one per runtime per metadata key). There is no `list_runtime_metadata` bulk tool and no inline carve-out (e.g., a bounded `previewFields` subset). For a tenant with dozens of runtimes advertising `agent-card` and `mcp-capabilities`, first-paint latency scales O(runtimes × keys). This was raised in iter3 (PRT-011) and iter4 (PRT-015) and remains unaddressed.

**Recommendation:** Either (a) add a `list_runtime_metadata` MCP tool that accepts a runtime-id list and key-prefix filter and returns matched entries in one round trip, or (b) permit `list_runtimes` to inline a bounded preview (e.g., `name`, `description`, `version`, `iconUrl`) drawn from the `agent-card` metadata entry with a documented size cap and `preview_truncated` annotation. Either approach should be spelled out explicitly in both `09_mcp-integration.md` and `15_external-api-surface.md`.

---

### PRO-019 MCP target version pinned as "latest stable at time of writing" [Low]

**Section:** `spec/15_external-api-surface.md` line 1284 and line 1862

Two locations still read "Target MCP spec version: MCP 2025-03-26 (latest stable at time of writing)." This phrasing was flagged in iter2, iter3 (PRT-010), and iter4 (PRT-016) and has persisted across four iterations. The clause is self-aging: anyone reading the spec after MCP publishes a newer stable version cannot tell whether Lenny tracks it, and readers have no pointer to the supported-version matrix that would resolve the ambiguity. The issue is documentation fidelity, not protocol behaviour (the `mcp_protocol_version_retired` annotation and version-negotiation flow are in place).

**Recommendation:** Replace the "latest stable at time of writing" clause with a concrete statement of form: "Lenny tracks the MCP specification on a rolling basis. The currently-required version is listed in the Supported MCP Versions table (Section X). Server-side upgrades follow the deprecation window defined in Section Y." Remove the "at time of writing" hedge at both occurrences. If the intent is to pin v1 to a specific MCP version regardless of upstream evolution, state that pin explicitly and note the next planned adoption checkpoint.

---

### PRO-020 OutboundSubscription hardcodes net/http response writer [Low]

**Section:** `spec/15_external-api-surface.md` lines 98-108 (`OutboundSubscription` struct) and lines 13-39 (`ExternalProtocolAdapter.HandleInbound` signature)

`OutboundSubscription.ResponseWriter` is typed `http.ResponseWriter`, and `HandleInbound(ctx, w, r, dispatcher)` takes `http.ResponseWriter` and `*http.Request` directly. Every adapter today is HTTP-bound (MCP Streamable HTTP, A2A JSON over HTTP, AgentProtocol over HTTP), so this works for v1. However, the `ExternalProtocolAdapter` interface is marketed as the pluggability seam for future protocols, and several plausible future protocols are not HTTP-shaped (gRPC streaming, WebSocket-native, MQTT, raw TCP for edge devices). Non-HTTP adapters would either have to fake `http.ResponseWriter`/`*http.Request` or the interface would need a breaking change. Raised in iter4 (PRT-017) at Low; remains unfixed.

**Recommendation:** Introduce a small Lenny-owned transport abstraction (e.g., `type OutboundStream interface { WriteFrame(ctx, []byte) error; Flush() error; Close() error }` and an inbound `type InboundRequest interface { Context() context.Context; Method() string; Header(string) string; Body() io.Reader; ResponseWriter() OutboundStream }`) and have the HTTP adapter layer provide a concrete `httpInboundRequest` that wraps `net/http`. Keep the HTTP-backed implementation the only one shipped for v1; the abstraction prevents a breaking interface change when a non-HTTP adapter is added. Document that v1 only implements HTTP-backed transport.

---

### PRO-021 AdapterCapabilities is a closed struct with no forward-extension story [Info]

**Section:** `spec/15_external-api-surface.md` (`AdapterCapabilities` definition and `OutboundCapabilitySet`)

`AdapterCapabilities` exposes a fixed set of named booleans (elicitation-support, tool-use observability, etc.) and `OutboundCapabilitySet.SupportedEventKinds` is a closed enum list. The dispatch-filter rule uses exact-match set membership. If a post-V1 adapter wants to advertise a capability that Lenny core does not yet know about (e.g., "supports streaming partial-tool-result chunks" for a hypothetical future MCP), there is no carrier for the flag short of a core struct change.

**Recommendation:** Add an opaque `Extensions map[string]json.RawMessage` or `Features []FeatureFlag` slot on `AdapterCapabilities` reserved for adapter-declared capability flags not yet modeled in Lenny core. Document that core code MUST NOT branch on unknown extension keys, and that the dispatch filter continues to operate on the closed set only. This is a purely additive forward-compatibility hedge.

---

### PRO-022 publishedMetadata key namespace lacks a registry or reservation policy [Info]

**Section:** `spec/05_runtime-registry-and-pool-model.md` (publishedMetadata field, lines 270-308); `spec/21_planned-post-v1.md` (A2A agent-card generator)

`publishedMetadata` keys `agent-card` and `mcp-capabilities` are referenced by built-in adapters, and the field is otherwise advertised as an opaque bag. There is no stated convention for (a) which keys are reserved for Lenny core adapters, (b) how third-party adapters should namespace their keys to avoid collision (e.g., `vendor.example.com/thing`), or (c) how the A2A generator chooses its input key when a tenant also writes a user-authored `agent-card` entry. The iter4 spec pins a `generatorVersion` envelope on auto-generated entries, which helps distinguish generator output from user input, but does not prevent key collision with a user's manually-written key.

**Recommendation:** Add a short "publishedMetadata key reservation" subsection listing (1) reserved Lenny-core keys (`agent-card`, `mcp-capabilities`, plus any future), (2) a namespacing convention for third-party adapters (e.g., reverse-DNS prefix), and (3) the conflict resolution rule when a tenant writes a key that collides with a Lenny-auto-generated one (suggest: user-written wins, auto-generator records a `suppressed_by_user_entry` status instead of overwriting).

---

### PRO-023 notifications/lenny/* namespace collision risk with future MCP standard methods [Info]

**Section:** `spec/15_external-api-surface.md` lines 1311-1350 (`MCPAdapter OutboundChannel mapping`), line 1346 (`notifications/lenny/*` namespace declaration)

Lenny defines extension MCP notification methods under `notifications/lenny/*` (e.g., `notifications/lenny/toolCall`, `notifications/lenny/error`). If a future MCP spec revision standardises tool-call or error notifications under a different method name, clients will receive both the standard notification and the Lenny-prefixed one, or adapters will need a translation step. The current spec does not state a policy for retiring `notifications/lenny/foo` once MCP standardises an equivalent.

**Recommendation:** Add a brief policy statement near the namespace declaration: "If a later MCP revision standardises a notification method equivalent to `notifications/lenny/X`, Lenny will dual-publish both methods for one deprecation window (same window as MCP version deprecation, 6 months), then retire the `notifications/lenny/X` form. The `mcp_protocol_version_retired` annotation is reused to surface the retirement to clients that still subscribe to the lenny-prefixed form." This keeps the extension namespace from becoming a permanent parallel vocabulary.

---

### Convergence assessment (Perspective 5)

- Critical: 0
- High: 0
- Medium: 0
- Low: 3 (PRO-018, PRO-019, PRO-020 — all three re-raised iter4 items)
- Info: 3 (PRO-021, PRO-022, PRO-023 — new forward-compat observations)

**Converged for this perspective** (0 C/H/M).

---

# Perspective 6 — Developer Experience (iter5)

**Scope.** Re-review of the runtime-author / operator developer surfaces against iter4 summary findings DXP-009 … DXP-021, with specific attention to:

- `spec/15_external-api-surface.md` §15.4 (adapter spec, integration levels, echo runtime, conformance tests) and §15.7 (Runtime Author SDKs).
- `spec/24_lenny-ctl-command-reference.md` §24.9, §24.18, §24.19 (tokens, scaffolder, Embedded-Mode image management).
- `spec/26_reference-runtime-catalog.md` §26.1, §26.2, §26.12 (catalog overview, shared patterns, author onramp).
- `spec/17_deployment-topology.md` §17.4 (primary-path Embedded-Mode custom-runtime walkthrough).
- `spec/05_runtime-registry-and-pool-model.md` §5.1 (Runtime schema, `integrationLevel` field).

**Calibration.** iter5 severity anchored to the iter4 rubric (no inflation for unchanged risks). "Would improve DX" alone is Low; only a broken wire contract, missing required command, or misleading-enough-to-cause-task-abandonment finding rates Medium or above.

## Inheritance of prior findings (iter4 DXP-009 … DXP-021)

| iter4 finding | iter5 disposition | Evidence |
| --- | --- | --- |
| DXP-009 §15.7 Protocol codec contradicts stdin/stdout + Unix-socket contract [High] | **Fixed.** §15.7 "Protocol codec" bullet rewritten (lines 2490–2496) to scope by integration level: Basic = stdin/stdout JSON Lines, Standard/Full = abstract-Unix-socket dial helpers for `@lenny-platform-mcp`, `@lenny-connector-<id>`, `@lenny-lifecycle`, with an explicit "runtime does not participate in mTLS" sentence. Matches §15.4.1, §4.7. |
| DXP-010 §15.7 lists non-existent MCP helper tools [High] | **Fixed.** §15.7 "Platform MCP tool helpers" (line 2497) now lists the §4.7 authoritative set (`lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`, `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`, `lenny/request_input`, `lenny/send_message`, `lenny/get_task_tree`, `lenny/set_tracing_context`). `tool_call`, `interrupt`, and `ready` are explicitly called out as non-MCP-tools (protocol frame / lifecycle signal / not present). |
| DXP-011 `integrationLevel` field used by §17.4/§15.4.6 but undefined in §5.1 [High] | **Fixed.** §5.1 line 59 declares `integrationLevel: full # basic \| standard \| full — optional; defaults to basic`. Inheritance rules at §5.1 lines 169 and 189 mark it as never-overridable on derived runtimes. §15.4.6 line 2359 and §24.18 line 231 wire `lenny runtime validate` to read the field. All §26 reference-runtime YAMLs declare `integrationLevel: full`. |
| DXP-012 `lenny image import` and `lenny token print` undocumented in §24 [High] | **Fixed.** §24.9 line 120 defines `lenny token print` with Embedded-Mode gate, exit codes, and audience. §24.19.1 (lines 268–291) introduces an "Image Management" subsection with `lenny image import <reference>` (with `--file`, `--namespace`), `lenny image list`, `lenny image rm`, prerequisites, exit codes, and Clustered-Mode rejection. |
| DXP-013 `Handler` interface references undefined `CreateRequest` / `Message` / `Reply` [Medium] | **Fixed.** §15.7 lines 2523–2661 now define all three types as Go structs with per-field doc-comments pointing to the §4.7 adapter manifest, §15.4.1 `MessageEnvelope` / `OutputPart`, §14 `WorkspacePlan`, and §4.9 credential bundle, plus a "do not introduce new wire types" disclaimer (line 2523). |
| DXP-014 §15.7 scaffolder paragraph universalizes SDK use despite §24.18 no-SDK carve-out [Medium] | **Fixed.** §15.7 line 2665 now qualifies the scaffolder sentence with the `--language binary --template minimal` exception and cross-references §24.18. |
| DXP-015 §24.18 cross-product has 12 combinations but only 1 specified [Medium] | **Fixed.** §24.18 lines 234–248 now carry a 4×3 matrix specifying each cell's level and SDK presence, with an "Unsupported combinations" block rejecting `binary/chat` and `binary/coding` with exit code `5 SCAFFOLD_UNSUPPORTED_COMBINATION` and prose rationale pointing to §15.4.3. |
| DXP-016 §26.1 "scaffolder copies one of these as a template" misrepresents templates [Medium] | **Fixed.** §26.1 line 6 now reads "emits one of three templates (`chat`, `coding`, `minimal`)" and spells out which reference runtimes share each template's conventions, with an explicit "There is no per-reference-runtime template." |
| DXP-017 §26.12 references `github.com/lennylabs/runtime-templates` repo but role undefined [Low] | **Not fixed.** §26.12 (line 485) still says "New reference runtimes are proposed via a PR to `github.com/lennylabs/runtime-templates`" with no definition of the repo's role vs. per-runtime repos or vs. the scaffolder templates that ship inside the `lenny-ctl` binary per §15.4.6 / §24.18. Carry-forward as **DXP-022** below. |
| DXP-018 `deadline_signal` vs `deadline_approaching` naming split [Low] | **Not fixed.** §4.7 line 694 advertises capability string `"deadline_signal"`; §4.7 line 703 defines message type `deadline_approaching`; §15.4.3 Full-level pseudocode line 2251 declares `supported = [..., "deadline_signal"]` and then switches on `"deadline_approaching"` (line 2288); §15.4.6 test row at line 2393 is labeled **deadline signal handling** but the assertion text says "On `deadline_signal`, the runtime writes a final `response`…" which reads as the message name even though the actual wire message is `deadline_approaching`. Three names for one concept. Carry-forward as **DXP-023** below. |
| DXP-019 §15.7 scaffolder description implies universal SDK use (iter3 carry-over) [Low] | **Fixed.** Subsumed by DXP-014's fix at §15.7 line 2665. |
| DXP-020 §26.1 "`local` profile installations" terminology undefined (iter3 carry-over) [Low] | **Not fixed.** §26.1 line 30 still says "For `local` profile installations, `lenny up` auto-grants access to the `default` tenant…". `local` is neither a §17.4 Operating Mode nor a §17.6 Install Profile; `lenny up` is Embedded Mode. Carry-forward as **DXP-024** below. |
| DXP-021 §17.4 Embedded-Mode walkthrough omits non-`default`-tenant access grant (iter3 carry-over) [Low] | **Not fixed.** §17.4 walkthrough (lines 263–303) still has no step 3b; the §26.1 "Tenant access" note (line 30) only covers the `default` tenant Embedded-Mode auto-grant case. A non-default-tenant author still hits `RUNTIME_NOT_AUTHORIZED` at first session creation with no in-walkthrough pointer. Carry-forward as **DXP-025** below. |

**Net iter4 carry-forward.** DXP-017, DXP-018, DXP-020, DXP-021 remain unfixed (all Low).

---

## New findings (iter5)

### DXP-022. §26.12 `runtime-templates` repository role still undefined; author onramp ambiguous about where to put code [Low]

**Section:** `spec/26_reference-runtime-catalog.md` §26.12 (line 485); `spec/15_external-api-surface.md` §15.4.6 (line 2395 "fixtures ship inside the `lenny` binary"); `spec/24_lenny-ctl-command-reference.md` §24.18 (line 226).

iter4 DXP-017 carried forward. §26.12 tells new-reference-runtime authors to PR to `github.com/lennylabs/runtime-templates`, but:

- §24.18 describes the scaffolder as emitting files from an in-binary template source (all logic is "local — no API calls are made", line 226). §15.4.6 line 2395 explicitly says "fixtures ship inside the `lenny` binary". There is no external repo consulted at `lenny runtime init` time.
- §26.3–§26.11 each list a **per-runtime** repo (`github.com/lennylabs/runtime-claude-code`, `…-gemini-cli`, …) as the implementation home. A new reference runtime has no reason to live anywhere else.
- The term `runtime-templates` appears **only** in §26.12 — nowhere else in the spec. An author reading §26.12 cannot tell whether this repo (a) holds scaffolder template source that the build pipeline bakes into `lenny-ctl`, (b) is a meta-repo of proposals and ADRs, or (c) was meant to be `runtime-<name>` with the hyphen substitution a typo.

An author following §26.12 as written will file a PR to `github.com/lennylabs/runtime-templates` with a full runtime implementation, the maintainers will redirect them to `github.com/lennylabs/runtime-<name>`, and the author will redo the PR. This is DX friction, not a blocker — Low.

**Recommendation:** Rewrite §26.12 to separate two steps. Step 1: "Scaffold a new runtime locally with `lenny-ctl runtime init <name>` (§24.18) — this emits a repo skeleton identical to the first-party reference runtimes." Step 2: "Push the skeleton to `github.com/lennylabs/runtime-<name>` (a new per-runtime repo) and open a PR adding an appendix entry in this section." Then add a single sentence clarifying `runtime-templates`: either "The scaffolder template source is maintained in `github.com/lennylabs/runtime-templates`; changes land there and are baked into `lenny-ctl` at release time. PRs to `runtime-templates` update the skeletons every author starts from." — or delete the reference entirely if the templates live in the `lenny-ctl` monorepo. Align with §24.18's "no API calls" claim so authors can distinguish template-source contribution from new-reference-runtime contribution.

---

### DXP-023. `deadline_signal` vs `deadline_approaching` tri-name split persists; conformance-test label and capability string do not match the wire message type [Low]

**Section:** `spec/04_system-components.md` §4.7 (lines 694, 703); `spec/15_external-api-surface.md` §15.4.3 pseudocode (lines 2251, 2288), §15.4.6 conformance-test row (line 2393).

iter4 DXP-018 carried forward. Three distinct names still reference one concept:

1. **Capability string** (§4.7 line 694): `"deadline_signal"`.
2. **Wire message `type`** (§4.7 line 703; switched on in §15.4.3 line 2288): `"deadline_approaching"`.
3. **Test label + assertion phrasing** (§15.4.6 line 2393): "deadline signal handling" / "On `deadline_signal`, the runtime writes a final `response` (possibly with `error.code: "DEADLINE_EXCEEDED"`) and exits cleanly before the deadline elapses."

The test assertion string uses a bareword that reads as a wire-level message name (`deadline_signal`) but no message with that `type` ever exists on the wire. An author reading the test row in isolation will `case "deadline_signal":` in their switch statement, observe no signals, and produce a runtime that passes the capability-declaration portion of the handshake but fails the actual deadline path — a silent behavioural bug that the conformance suite phrasing invited.

**Recommendation:** Pick one root noun. Preferred: keep capability string `"deadline_signal"` (it is stable on the handshake surface and authors declare support for a feature, not a message) and rename the test label to match the wire name — "deadline_approaching handling — on `deadline_approaching`, the runtime writes a final `response`…". Alternatively rename the capability to `"deadline_approaching"` to match the message. Either approach closes the split; update §4.7 line 694 or §4.7 line 703, §15.4.3 lines 2251 & 2288, §15.4.6 line 2393, and any §26.2 cross-references in lockstep.

---

### DXP-024. `§26.1` "`local` profile installations" terminology still undefined; no §17.4 / §17.6 term matches [Low]

**Section:** `spec/26_reference-runtime-catalog.md` §26.1 (line 30).

iter4 DXP-020 carried forward. §26.1 line 30: "For `local` profile installations, `lenny up` auto-grants access to the `default` tenant for every reference runtime it installs so the developer can invoke them without additional setup." `local` is:

- **Not** a §17.4 Operating Mode — those are Embedded (`lenny up`), Source (`make run`), and Compose (docker compose).
- **Not** a §17.6 Install Profile — the profiles layered by the Helm values system are `base`, `prod`, `compliance`, plus optional overlays (the keyword `local` does not appear).
- **Not** a §17.8.1 bundled Helm profile.

Since `lenny up` is explicitly the Embedded Mode entry point (§24.19 line 260), the paragraph clearly intends "Embedded Mode installations" — but says something different. A reader grepping for `local` profile configuration will find nothing and conclude the sentence is stale or refers to a feature to be enabled via some unseen config switch.

**Recommendation:** Change line 30 to: "For **Embedded Mode** installations ([§17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)), `lenny up` auto-grants access to the `default` tenant for every reference runtime it installs so the developer can invoke them without additional setup." No other changes required; `local profile` is not used elsewhere so no broader cleanup is needed.

---

### DXP-025. §17.4 Embedded-Mode custom-runtime walkthrough still omits the non-`default`-tenant access grant step [Low]

**Section:** `spec/17_deployment-topology.md` §17.4 (lines 263–303); `spec/26_reference-runtime-catalog.md` §26.1 (line 30); `spec/15_external-api-surface.md` §15.1 `POST /v1/admin/runtimes/{name}/tenant-access`.

iter4 DXP-021 carried forward. The primary-path custom-runtime walkthrough in §17.4 registers `my-agent` against the embedded gateway via `lenny-ctl runtime register --file runtime.yaml` (line 289) and immediately invokes it via `curl` against `/v1/sessions` (line 297). §26.1 line 30 documents that reference runtimes have no default tenant access grants and that `lenny up` auto-grants access to the **`default`** tenant only. The walkthrough silently assumes the author is invoking from the `default` tenant — the Embedded-Mode token printed by `lenny token print` is indeed scoped to the `default` tenant by default, so the walkthrough happens to work — but:

- If the author later wants to test against a second tenant (a realistic iteration after the first success), the session creation returns `RUNTIME_NOT_AUTHORIZED` with no pointer in the walkthrough to the `POST /v1/admin/runtimes/{name}/tenant-access` fix.
- The `integrationLevel: basic` line added to the walkthrough's `runtime.yaml` (iter4 fix for DXP-011 at line 287) makes `my-agent` behave like a first-party reference runtime from the tenant-access perspective. The §26.1 "no default grants" rule should therefore apply to `my-agent` the same way it applies to `claude-code` — but the walkthrough does not call this out.

**Recommendation:** Insert a step 3b in §17.4 after line 289:

```
# 3b. (Optional) If invoking from a non-`default` tenant, grant tenant access explicitly:
lenny-ctl admin runtimes tenant-access add --name my-agent --tenant <tenant-id>
# (equivalent REST: POST /v1/admin/runtimes/my-agent/tenant-access {"tenantId":"<uuid>"})
# Embedded Mode's `lenny up` auto-grants access for the `default` tenant only; any
# other tenant needs an explicit grant before session creation succeeds.
```

Cross-reference §26.1 "Tenant access" and §15.1. Keep the step marked optional so the happy path (default tenant, already granted) stays one-liner-friendly.

---

## Convergence assessment

- **Critical:** 0
- **High:** 0
- **Medium:** 0
- **Low:** 4 (DXP-022, DXP-023, DXP-024, DXP-025 — all carry-forwards of iter4 Lows whose fixes were not included in this iteration's pass)
- **Info:** 0

**Fixed this iteration:** DXP-009 (High), DXP-010 (High), DXP-011 (High), DXP-012 (High), DXP-013 (Medium), DXP-014 (Medium), DXP-015 (Medium), DXP-016 (Medium), DXP-019 (Low, subsumed by DXP-014 fix). Nine of thirteen iter4 findings addressed, including all four iter4 Highs and all four iter4 Mediums.

**Convergence:** **Yes** from the DevEx perspective. Zero Critical/High/Medium findings remain. The four surviving Lows are unchanged-in-severity carry-forwards and are pure polish (repo-role wording, naming consistency, terminology alignment, an optional walkthrough step). They do not block a runtime author from building, registering, and invoking a working runtime against either Embedded Mode or a clustered install, and none of them represents a broken contract — only documentation drift or naming splits that the reviewer's iter4 rubric already classified as non-blocking. The DevEx surface is materially improved over iter4: the §15.7 SDK contract now matches §15.4 / §4.7, the `integrationLevel` field is first-class in §5.1, both previously-undocumented commands (`lenny image import`, `lenny token print`) are specified, the scaffolder cross-product is fully specified with explicit rejections, and the reference-runtime catalog's template story is internally consistent. Reviewer may accept the four remaining Lows as documentation-iteration debt and declare DevEx convergence; alternatively a single follow-up pass handling DXP-022 / DXP-023 / DXP-024 / DXP-025 together would close the perspective cleanly without blocking the overall spec.

---

# Perspective 7: Operator & Deployer Experience — Iter5

## Scope

Re-examined iter4 findings OPS-010 through OPS-015 (all six Low-severity gaps) against the current spec, with a focused re-read of `spec/17_deployment-topology.md` (§17.4, §17.7, §17.8.1), `spec/18_build-sequence.md`, and `spec/25_agent-operability.md` (§25.4, §25.7, §25.11). The iter4 summary did not mark any OPS-010..OPS-015 as Fixed; line 2206 of the iter4 summary classifies `OPS-005/006/009` (their iter3 ancestors) as carry-forwards from iter3 where fixes were skipped or never landed. Direct inspection of the current spec confirms the text is unchanged from what iter4 described.

Prefix: **OPS-** (matching iter4). Severities anchored to iter2/iter3/iter4 rubric (all doc-discoverability gaps remain Low per `feedback_severity_calibration_iter5`).

## Carry-forward findings

### OPS-016 `lenny-ops` Helm values `backups.erasureReconciler.*` and `minio.artifactBackup.*` missing from §17.8.1 operational defaults table (iter4 OPS-010 carry-forward) [Low]

**Section:** `17_deployment-topology.md` §17.8.1 (lines 830–881); `25_agent-operability.md` §25.4 canonical values block, §25.11 ArtifactStore Backup subsection.

Unchanged since iter4. The §17.8.1 defaults table header at line 832 still reads *"All tunable defaults collected in one place for operator convenience"*, but the table (lines 834–879) does not mention `backups.erasureReconciler.enabled`, `backups.erasureReconciler.legalHoldLedgerFreshnessGate`, `minio.artifactBackup.enabled`, `minio.artifactBackup.target.*`, `minio.artifactBackup.versioning`, `minio.artifactBackup.replicationLagRpoSeconds`, or the iter4-added `minio.artifactBackup.residencyCheckIntervalSeconds` / `minio.artifactBackup.residencyAuditSamplingWindowSeconds` knobs. An operator scanning §17.8.1 for backup/erasure/artifact-replication tunables after an iter3-introduced alert fires finds nothing and will incorrectly conclude the knobs are non-existent.

**Recommendation:** Apply the iter4 OPS-010 fix verbatim (four rows covering the erasure reconciler, the legal-hold ledger freshness gate, `minio.artifactBackup.enabled`, and the replication-lag RPO) and add two extra rows for the iter4-new residency preflight (`residencyCheckIntervalSeconds`, default 300s; `residencyAuditSamplingWindowSeconds`, default 3600s). Commit alongside the OPS-018 / OPS-019 fixes so the defaults table is revised once.

---

### OPS-017 `issueRunbooks` lookup table omits `BACKUP_RECONCILE_BLOCKED` and `MINIO_ARTIFACT_REPLICATION_*` codes (iter4 OPS-011 carry-forward) [Low]

**Section:** `25_agent-operability.md` §25.7 Path B lookup at line 3184; `17_deployment-topology.md` §17.7 line 723 enumeration; §25.11 ArtifactStore Backup subsection (lines ~3990–4060).

Unchanged since iter4. The `issueRunbooks` map in `pkg/gateway/health/runbook_links.go` at §25.7 (line 3184) still enumerates exactly the pre-iter3 eight entries (`WARM_POOL_EXHAUSTED` through `CIRCUIT_BREAKER_OPEN`). No entry exists for `BACKUP_RECONCILE_BLOCKED`, `MINIO_ARTIFACT_REPLICATION_LAG`, `MINIO_ARTIFACT_REPLICATION_FAILED`, or the iter4-introduced `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE`. Agents receiving these alert events get no `runbook:` field and must fall back to Path C (full-list scan), breaking the cheaper Path B convention §17.7 line 723 advertises.

**Recommendation:** Apply the iter4 OPS-011 fix and add one extra entry for the iter4-introduced residency-violation code:

```go
"BACKUP_RECONCILE_BLOCKED":                 "post-restore-reconciler-blocked",
"MINIO_ARTIFACT_REPLICATION_LAG":           "artifactstore-replication-recovery",
"MINIO_ARTIFACT_REPLICATION_FAILED":        "artifactstore-replication-recovery",
"ARTIFACT_REPLICATION_REGION_UNRESOLVABLE": "artifactstore-replication-residency-violation",
```

Extend the §17.7 line 723 "required by §25.7 Path B" sentence to list all additional codes alongside the OPS-020 (`DRIFT_SNAPSHOT_STALE`) fold. If `BACKUP_RECONCILE_BLOCKED` surfaces only as an alert (not a health-API issue code), also add the `runbook` annotation to the Prometheus rule so the §25.5 `alert_fired` event path carries the pointer.

---

### OPS-018 §17.7 runbook catalog missing entries for post-restore reconciler block, ArtifactStore replication recovery, and residency-violation recovery (iter4 OPS-012 carry-forward, expanded) [Low]

**Section:** `17_deployment-topology.md` §17.7 (lines 713–822); `25_agent-operability.md` §25.11 ArtifactStore Backup subsection (lines ~3990–4060) and Post-restore reconciler block (lines ~4155+); §25.14 `lenny-ctl` table line 4916.

Unchanged since iter4, with one iter4-new surface added. The §17.7 catalog still enumerates runbooks covering Postgres, Redis, MinIO-gateway, token service, credential pool, admission webhooks, etcd, stuck finalizers, schema migration, audit pipeline, token store, rate-limit storms, drift snapshot refresh, and clock drift. Missing:

1. **`post-restore-reconciler-blocked.md`** — triggered by `BackupReconcileBlocked` alert / `gdpr.backup_reconcile_blocked` audit event / `GET /v1/admin/restore/{id}/status` returning `phase: "reconciler_blocked"`. Remediation path (`POST /v1/admin/restore/{id}/confirm-legal-hold-ledger` or `lenny-ctl restore confirm-legal-hold-ledger`) is scattered across §25.11 prose (line 3859), §25.14 (line 4916), and §12.8 with no unified three-part runbook entry.

2. **`artifactstore-replication-recovery.md`** — triggered by `MinIOArtifactReplicationLagHigh` / `MinIOArtifactReplicationFailed`. Remediation requires piecing together §25.11's "Restore procedure" prose.

3. **`artifactstore-replication-residency-violation.md`** (new in iter4 / iter3-post-fix `CMP-048`-adjacent hardening) — triggered by `ArtifactReplicationResidencyViolation` critical alert. Recovery requires correcting the destination jurisdiction tag or Helm values, then calling `POST /v1/admin/artifact-replication/{region}/resume` (platform-admin, audited). No §17.7 entry exists; the procedure is documented only in §25.11's runtime-residency-preflight prose.

**Recommendation:** Add three stubbed §17.7 runbook entries with the standard `<!-- access: trigger --> / diagnosis / remediation` three-part structure. The first two are the iter4 OPS-012 recommendation verbatim. The third:

- **`artifactstore-replication-residency-violation.md`** — *Trigger:* `ArtifactReplicationResidencyViolation` critical alert; `lenny_minio_replication_residency_violation_total` increments; `GET /v1/admin/artifact-replication/{region}` returns `status: "suspended_residency_violation"`. *Diagnosis:* inspect `DataResidencyViolationAttempt` audit event for source region, destination endpoint, returned jurisdiction tag, and CIDR-resolution result; compare to Helm `minio.regions.<region>.artifactBackup.target` and `backups.regions.<region>.allowedDestinationCidrs`. *Remediation:* (1) fix the root cause — correct the destination bucket's `lenny.dev/jurisdiction-region` tag, revert the DNS rebinding, or re-provision the destination in the correct region; (2) re-verify with `s3:GetBucketTagging` against the destination; (3) call `POST /v1/admin/artifact-replication/{region}/resume` with `justification` (platform-admin, audited); (4) confirm the alert clears and `lenny_minio_replication_lag_seconds` resumes decreasing. Cross-reference: §25.11 runtime-residency-preflight, §12.8 backup pipeline residency.

Pair this edit with OPS-017 so the `issueRunbooks` map and the §17.7 catalog land consistent runbook slugs in a single revision.

---

### OPS-019 Embedded Mode has no Postgres-major-version mismatch fail-safe (iter4 OPS-013 / iter3 OPS-007 / iter2 OPS-005 carry-forward) [Low]

**Section:** `17_deployment-topology.md` §17.4 "State and resets" (lines ~162–163).

Unchanged since iter2. §17.4 still documents schema-migration handling and `lenny down --purge` but says nothing about a PostgreSQL *binary major version* bump. `~/.lenny/postgres/` uses a PG-major-version-specific on-disk layout (the spec pins PG 16 elsewhere); a newer `lenny` binary against an on-disk directory written by an older embedded-postgres major will either fail to start or crash opaquely. No `PG_VERSION` check, no documented `lenny export` / `lenny import` path, no fail-closed `EMBEDDED_PG_VERSION_MISMATCH` error. Low severity today (PG 16 pinned, no deployments in the wild, `feedback_no_backward_compat`), but surfaces on the first PG major bump after GA.

**Recommendation:** Apply the iter4 OPS-013 fix verbatim — add one sentence to §17.4: *"`lenny up` reads `~/.lenny/postgres/PG_VERSION` on start; on mismatch with the expected major, fails closed with `EMBEDDED_PG_VERSION_MISMATCH` and prints the recovery procedure (`lenny export --to <path>` then `lenny down --purge && lenny up && lenny import --from <path>`). In-place `pg_upgrade` is not supported in Embedded Mode."* Two-line spec addition that prevents silent data loss on the first PG major bump.

---

### OPS-020 Operational defaults table §17.8.1 still omits `ops.drift.*` tunables (iter4 OPS-014 / iter3 OPS-008 / iter2 OPS-006 carry-forward) [Low]

**Section:** `17_deployment-topology.md` §17.8.1 (lines 830–881); `25_agent-operability.md` §25.4 canonical values block, §25.10 configuration drift detection.

Unchanged since iter2. §25.4 defines `ops.drift.snapshotStaleWarningDays` (default 7) and `ops.drift.runningStateCacheTTLSeconds` (default 60) as operator-facing Helm values; both are referenced in §25.10 and in `drift-snapshot-refresh.md`'s trigger blurb (§17.7 line 813). Neither appears in the §17.8.1 defaults table. Fold into OPS-016 so the defaults table pass is done once.

**Recommendation:** Add two rows per the iter4 OPS-014 recommendation:

| Setting | Default | Reference |
| --- | --- | --- |
| Drift snapshot-staleness warning threshold (`ops.drift.snapshotStaleWarningDays`) | 7 days (0 disables) | [§25.10](25_agent-operability.md#2510-configuration-drift-detection) |
| Drift running-state cache TTL (`ops.drift.runningStateCacheTTLSeconds`) | 60 s | [§25.10](25_agent-operability.md#2510-configuration-drift-detection) |

---

### OPS-021 `issueRunbooks` lookup still missing `DRIFT_SNAPSHOT_STALE` → `drift-snapshot-refresh` mapping (iter4 OPS-015 / iter3 OPS-009 carry-forward) [Low]

**Section:** `25_agent-operability.md` §25.7 Path B (line 3184); `17_deployment-topology.md` §17.7 line 723 enumeration.

Unchanged since iter3. `issueRunbooks` at line 3184 still enumerates only the original eight entries and §17.7 line 723 still does not include `DRIFT_SNAPSHOT_STALE`. Fold into the OPS-017 edit so all five missing codes (`DRIFT_SNAPSHOT_STALE` + four iter3/iter4 additions) land in one pass.

**Recommendation:** Add to the `issueRunbooks` map:

```go
"DRIFT_SNAPSHOT_STALE": "drift-snapshot-refresh",
```

Amend the §17.7 line 723 enumeration sentence accordingly. Decide in the same edit whether `snapshot_stale: true` should also surface as an `alert_fired` event on the §25.5 event stream; if yes, add the alert annotation in §16.5; if no, state explicitly in §25.10 that the signal is API-response-only.

---

## New issues

None. The spot-checks of §17.4, §17.7, §17.8.1, §18, §25.4, §25.7, and §25.11 did not surface any additional operational breakage beyond the six carry-forwards above. §18 build-sequence subsections 18.1 (build artifacts introduced by §25) are tightly cross-referenced; `lenny-ops`, `lenny-backup`, and the shared `pkg/alerting/rules` wiring are all called out. §17.7 covers 18+ runbooks with uniform three-part structure and the three new entries above plug the specific gaps iter3/iter4 introduced.

## Convergence assessment

**Not converged on ops-discoverability polish, but no convergence-blocking issues.** Perspective 7 finds zero Critical, zero High, zero Medium, and six Low carry-forwards from iter4. All six are literal discoverability/runbook-catalog gaps — they have documented workarounds (Path C full-list scan for runbooks; reading §25.4 directly for defaults; purge-and-reinstall for an embedded PG major bump). None prevents deployment, none creates an upgrade-risk surface, and none is a newly-introduced iter5 regression.

The calibration rule (anchor iter5 severities to prior iterations' rubric) holds: every finding was filed at Low in iter2/iter3/iter4 and remains Low here. Per `feedback_docs_sync_after_spec_changes`, if a future iter5 fix pass lands any subset, the companion docs/ sync is limited to these same three files (§17.7, §17.8.1, §25.7) — no broader reconciliation is needed.

Recommendation: land OPS-016 through OPS-021 in a single iter5 fix commit — the edits are co-located in three spec files, total ~15 lines added, and clear the entire iter2→iter5 carry-forward tail for this perspective.

---

# Iter5 Review — Perspective 8: Multi-Tenancy & Tenant Isolation

Scope: verify iter4 TNT-008 through TNT-013 against current spec, then scan
`spec/11_policy-and-controls.md`, `spec/12_storage-architecture.md` (tenant
isolation sections), `spec/13_security-model.md` (RLS section), and the
playground / ops-inventory surfaces for concrete NEW isolation bypasses or RBAC
gaps. Severity calibrated against iter4 rubric per
`feedback_severity_calibration_iter5.md`: theoretical "could be stronger"
without a demonstrable bypass = Low.

Iter4 numbering ran to TNT-013. New iter5 findings start at TNT-014.

---

## Iter4 carryover verification

### TNT-008 (Medium, iter4) — Fixed (verified)

Subject-token-type restriction and scope-narrowing invariants are now
normative in `spec/10_gateway-internals.md` §10.2 lines 234–243 (**"Playground
mint invariants"** block) and cross-referenced from `spec/27_web-playground.md`
§27.3:60 and §27.3:63–66:

- Invariant (1) requires `subject_token.typ == user_bearer`; rejects
  `session_capability` / `a2a_delegation` / `service_token` with
  `401 LENNY_PLAYGROUND_BEARER_TYPE_REJECTED` (§15.1:1077).
- Invariant (2) pins `minted.scope = intersection(subject.scope,
  playground_allowed_scope)` with an explicit static scope set for v1.
- Invariants (3)–(5) pin tenant preservation, duration cap, and
  caller-type / role preservation.
- Test matrix row in §10.2:249 enforces the "capability JWT pasted into
  playground produces 401" case the iter4 recommendation asked for.
- Rejected mints emit `playground.bearer_mint_rejected` audit event
  (§10.2:243) and increment
  `lenny_playground_bearer_mint_rejected_total{reason}`.

Fix held.

### TNT-009 (Medium, iter4) — Fixed (verified)

`spec/27_web-playground.md` §27.3.1:100–108 adds a dedicated **"Tenant-claim
rejection codes (OIDC callback)"** table mirroring §10.2's extraction
semantics, covering `TENANT_CLAIM_MISSING`, `TENANT_NOT_FOUND`,
`TENANT_CLAIM_INVALID_FORMAT`, each with HTTP status, query-param code
(`?error=tenant_claim_missing`, etc.), and log-attribution rule
(`tenant_id=__unset__` for missing/invalid-format). §10.2 remains
authoritative; the §27.3.1 table is explicitly a cross-reference. Fix held.

### TNT-010 (Medium, iter4) — Fixed (verified)

`spec/27_web-playground.md` §27.2:46–51 now defines a four-layer validation
stack:

1. Helm `values.schema.json` with `pattern: ^[a-zA-Z0-9_-]{1,128}$` (primary).
2. `lenny-preflight` row for cross-field conditionals (primary).
3. Gateway startup codes `LENNY_PLAYGROUND_DEV_TENANT_INVALID` /
   `LENNY_PLAYGROUND_DEV_TENANT_REQUIRED` (backstop; format/cross-field only).
4. Per-request Ready-gate on `/playground/*` for tenant-existence
   (`503 LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED`, `Retry-After: 5`).

`playground.devTenantId` is added to the §10.3 required-keys table per the
iter4 WPP-010 fix record. Layers 1 and 2 are described as the **primary**
defenses, with startup gates as backstops — matching the `noEnvironmentPolicy`
posture the iter4 finding cited. Fix held.

### TNT-011 (Medium, iter4) — Fixed (verified)

`spec/27_web-playground.md` §27.3.1:84–98 now specifies the playground
OIDC session record backing store explicitly:

- Keys: `t:{tenant_id}:pg:sess:{session_id}` (envelope) and
  `t:{tenant_id}:pg:revoked:{jti}` (presence-only marker), both on the
  per-tenant prefix convention from §12.4.
- TTLs: session record pinned to `oidcSessionTtlSeconds − elapsed`;
  revocation marker pinned to `exp − now + 5s` skew budget.
- Per-request revocation check on the auth hot path (401 with
  `details.reason: "bearer_revoked"` on REST/MCP; WebSocket close `4401`).
- Pub/sub propagation on `t:{tenant_id}:pg:revocations` with a 500ms P99
  cross-replica SLO and bounded LRU negative cache on each replica.
- Redis unavailability fails closed (`503 REDIS_UNAVAILABLE`).
- Integration tests `TestPlaygroundSessionRevocationCrossReplica` and an
  extension of `TestRedisTenantKeyIsolation` are pinned.

The multi-replica logout gap the iter4 finding flagged is closed with a
shared store, a documented SLO, and a fail-closed posture on Redis
degradation. Fix held.

### TNT-012 (Low, iter4) — NOT Fixed (carry forward to TNT-014)

The ambiguous cross-tenant `?tenantId=` outcome for non-`platform-admin`
roles remains unresolved. See TNT-014 below for the carry-forward.

### TNT-013 (Low, iter4) — Substantially addressed (carry forward partial as Low — see TNT-015)

The WPP-011 fix in iter4 added:

- A persistent, server-rendered yellow "API KEY MODE — paste only
  operator-issued tokens" banner (§27.9:254) that cannot be suppressed by
  swapping the client bundle.
- A non-blocking `lenny-preflight` WARNING gated by
  `playground.acknowledgeApiKeyMode` (§27.2:42; §17.6 preflight table).
- The `playground.bearer_mint_rejected` audit event (§10.2:243) covering
  rejected pastes.

The core "credential misdelivery to operator-owned log sink" concern is
substantially mitigated: operators who enable `apiKey` in non-dev mode
without acknowledgement get flagged at install time, and end users see a
server-rendered warning banner. Residual gaps (UI label rename, explicit
bearer redaction rule in auth-failure logs) are doc-polish — see TNT-015
for the narrower carry-forward.

---

## New iter5 findings

### TNT-014. Non-`platform-admin` `?tenantId=` mismatch outcome remains undefined on list endpoints [Low]

**Section:** `spec/25_agent-operability.md` §25.4 (operations inventory,
lines 1769, 1806–1810), §25 ("Filter Parameter Naming" table, line 340);
`spec/10_gateway-internals.md` §10.2:296 (tenant-scoped admin API);
`spec/15_external-api-surface.md` §15.1:805 (`POST
/v1/admin/credential-pools` and sibling list endpoints that advertise
`?tenant_id=` for `platform-admin` only).

This is a carry-forward of iter4 TNT-012. The iter4 finding was Low. The
spec surfaces still converge on three prose statements rather than one
normative rule, and none of them specify the HTTP status code when a
non-platform-admin passes `?tenantId=OTHER`:

- §25.4:1769 — `tenantId` filter "auto-restricted to its own tenant" for
  `tenant-admin` (silent substitution reading).
- §25.4:1808 — tenant-admin "sees only operations where [...] the
  operation carries a `tenantId` field AND its value matches the caller's
  tenant" (silent empty-result reading).
- §10.2:296 — tenant-scoped admin API "only return data belonging to the
  caller's tenant" (no explicit outcome for a mismatched query param).
- §15.1:805 — `?tenant_id=` advertised as platform-admin-only (explicit
  scoping) but without a rejection code when a tenant-admin sends it.

All three readings are defensible. None is pinned. Automation that
attempts a cross-tenant query on behalf of a tenant-admin can therefore
receive (i) silently-substituted own-tenant data, (ii) an empty list
with no diagnostic signal, or (iii) a 403 — depending on the
implementer's choice at each list endpoint, with no conformance test to
anchor the behavior.

Severity: Low per iter4 anchoring. The RLS layer is fully closed (§4.2
lines 163, 165; §12.3 lines 49–57 `lenny_tenant_guard`) so the concrete
cross-tenant **data-leak** path is blocked at the database regardless of
how the gateway resolves the filter param. This is a confused-deputy /
UX-clarity concern, not a demonstrated bypass.

**Recommendation:** In §25.4 (ops inventory authorization block, line
1808) and §10.2:296 (tenant-scoped admin API), add a single normative
row stating that any non-`platform-admin` role passing a `tenantId`
query parameter whose value does not equal the role's scoped
`tenant_id` claim returns `403 AUTH_CROSS_TENANT_FORBIDDEN` — never
silently substituted, never returned as empty. Add the code to §15.1 as
`AUTH_CROSS_TENANT_FORBIDDEN` (`POLICY`, 403, non-retryable,
`details.requestedTenantId` and `details.callerTenantId`). Add
`authz_cross_tenant_attempts_total{role, endpoint}` to §16.1 and an
audit event `authz.cross_tenant_attempt` in §11.7 so the behaviour is
observable. The same rule applies uniformly to every list endpoint that
accepts a `tenantId` filter (ops inventory, sessions, pods,
audit-events, credential-pools, events, metering, usage).

### TNT-015. `apiKey`-mode auth-failure logs lack a bearer-redaction rule; misdelivered vendor credentials can land in operator log sinks [Low]

**Section:** `spec/27_web-playground.md` §27.3 (apiKey mode, line 60),
§27.9 (security considerations, line 255); `spec/10_gateway-internals.md`
§10.2 (Playground mint invariants block at line 243, audit event
`playground.bearer_mint_rejected`); `spec/16_observability.md` (log
attribution / redaction guidance).

Partial carry-forward of iter4 TNT-013. The iter4 banner + preflight +
acknowledgement fixes (WPP-011) close the primary operator-visible
concern. What remains: when a user pastes a non-Lenny vendor credential
(`sk-...`, `sk-ant-...`, GitHub PAT, etc.) into the `apiKey` mode form,
the gateway's standard auth chain rejects it with
`TENANT_CLAIM_MISSING` (correct). The rejected bearer's raw string is
logged unless the gateway's structured-logging wrapper has a
bearer-redaction rule — but the spec does not pin one for the playground
path. `playground.bearer_mint_rejected` (§10.2:243) captures
`subject_jti` and `subject_typ`, not the raw material, so the audit
event is safe by construction; the risk is in the surrounding auth-chain
log lines (the §10.2 extraction table rejections at
`TENANT_CLAIM_MISSING` / `TENANT_CLAIM_INVALID_FORMAT`) which still
flow through the generic auth-failure logger with whatever fields it
chooses.

In SaaS deployments where the log sink is operator-owned, a tenant's
mispasted vendor credential therefore becomes visible to the platform
operator unless the logger independently redacts the `Authorization`
header — a property the spec currently leaves implicit.

Severity: Low. The banner and the acknowledgement preflight (iter4
WPP-011) substantially reduce the incidence, and the receiving surface
is the operator's own log sink (not a cross-tenant leak). This is a
defense-in-depth finding, not a demonstrated isolation bypass.

**Recommendation:** In §27.9 add a bullet stating that the gateway MUST
redact any value matching `Authorization: Bearer \S+` from auth-failure
log lines emitted on the `/playground/*` path (and, for consistency, on
every auth-chain rejection path) to `Authorization: Bearer
sha256(<12-hex-chars>)…` — the 12-char prefix matches the existing
`jti` truncation used elsewhere. Reference this rule from §16.4
alongside the existing PII-redaction guidance. Optionally, rename the
UI label from "API key" to "Lenny bearer token (JWT)" with placeholder
`eyJ…` (§27.3:60) to reduce the misdelivery incidence further; the
`apiKey` mode identifier and Helm value may remain for backward
compatibility.

---

## Convergence assessment

Four of the six iter4 TNT items (TNT-008, TNT-009, TNT-010, TNT-011) are
**Fixed** with the recommended invariants, tables, layering, and backing
store spelled out and cross-referenced. Two remain:

- TNT-014 (carry-forward of TNT-012, Low) — unchanged since iter4; the
  cross-tenant `?tenantId=` outcome is still ambiguous across three
  adjacent prose locations.
- TNT-015 (narrowed carry-forward of TNT-013, Low) — the iter4 WPP-011
  fix closed the primary concern; the residual is a bearer-redaction
  rule on auth-failure logs that the spec leaves implicit.

Both remaining findings are Low (defense-in-depth / UX clarity) with no
concrete cross-tenant bypass path. The RLS-based isolation stack
(§4.2:163–165, §12.3:49–57 `lenny_tenant_guard`, §12.4:177–195
per-tenant Redis prefixes, §13.3 OAuth token-exchange invariants) is
fully closed against demonstrated bypasses.

**Iter5 Perspective 8 status: converged.** No Critical / High / Medium
multi-tenancy findings remain. The two Low items can be addressed in a
doc-polish pass or deferred without blocking.

---

# Perspective 9 — Storage Architecture & Data Management (iter5)

**Scope:** Re-review of the storage layer (Postgres, Redis, MinIO/ArtifactStore, EventBus, CheckpointStore, quota, backup, erasure, residency) against iter4 summary findings STR-011 through STR-016 and the must-check examples: Redis fail-open security, Artifact GC correctness, "no shared RWX storage", checkpoint storage scaling, at-rest encryption completeness.

**Calibration:** iter5 severity anchored to the iter4 rubric (no severity inflation when the same risk persists) and to the convergence discipline: only genuine issues are raised; fit-and-finish items are marked Low/Info.

**Inheritance of prior findings.** A diff of the iter4 STR-011..016 issues against the current spec shows:

| iter4 finding | iter5 disposition | Evidence |
| --- | --- | --- |
| STR-011 Event replay buffer discard loses reconstructible events on coordinator handoff [Medium] | Fixed in iter5 | §10.1 now specifies a `session.resumed` + optional `status_change` + `children_reattached` synthesis sequence from durable Postgres state before any `gap_detected` frame; `sessions.last_seq` durability guarantees `SeqNum` monotonicity across handoff. |
| STR-012 Partial-manifest `deleted_at` column missing, backstop predicate references it [Medium] | Fixed in iter5 | §10.1 manifest schema now declares `deleted_at TIMESTAMPTZ NULL`, §4.4 rewritten to soft-delete with the same `deleted_at IS NULL` guard as the backstop. |
| STR-013 No uniqueness guard on active partial manifests [Medium] | Fixed in iter5 | §10.1 supersede-on-write requires deleting prior rows in the same transaction; `partial_manifest_active_uniq` partial unique index `(session_id, slot_id) WHERE partial = TRUE AND deleted_at IS NULL` enforces at the DB level; `lenny_checkpoint_partial_manifests_superseded_total` metric added. |
| STR-014 `eventbus_publish_state='failed'` rows have no retry worker [Medium] | Fixed in iter5 | §12.6 defines a leader-elected retranscribe worker sweeping `WHERE eventbus_publish_state IN ('failed','retry_pending') AND retry_count < eventBus.maxRetryAttempts`, terminal-failure alerting, and an admin `POST /v1/admin/audit-events/{id}/republish` endpoint. |
| STR-015 Sampled-HEAD test-restore tolerates large absolute artifact-loss rates [Low] | Mitigated (partial). §25.11 still uses the sampled-HEAD model with a single `artifactSampleSize` default (not tier-tiered) and a percentage threshold. The absolute-miss floor recommended in iter4 is not present. See STO-018 below (carry-forward, Low). |
| STR-016 Config-time gate for `audit.gdprRetentionDays > backups.retention.retainDays` not enforced [Low] | Mitigated (partial). §12.8 now rejects `POST /v1/admin/restore/execute` when `backup.completed_at < now() - audit.gdprRetentionDays`, and a compliance-profile floor of 2190d applies under regulated profiles. However, the canonical inequality `audit.gdprRetentionDays >= backups.retention.retainDays + preRestoreRetainDays` is not validated at chart render / startup. See STO-019 below (carry-forward, Low). |

## New findings (iter5)

### STO-017. Fail-open per-user ceiling `min(tenant_limit * userFailOpenFraction, per_replica_hard_cap)` can silently admit the full `per_replica_hard_cap` to a single user [Medium] [Fixed]

**Fix applied:** §12.4 "Per-user fail-open ceiling" now composes the per-user cap as a pure fraction of the effective per-replica ceiling — `per_user_failopen_ceiling = effective_ceiling * userFailOpenFraction` where `effective_ceiling = min(tenant_limit / max(cached_replica_count, 1), per_replica_hard_cap)` — retiring the `min(..., per_replica_hard_cap)` construction with an explicit rationale (per-tenant aggregate cap ≠ per-user cap). A config-time invariant check is added at chart render and `lenny-ops` startup: `0 < quotaUserFailOpenFraction <= 1.0` is enforced with `CONFIG_INVALID`, and the `QuotaFailOpenUserFractionInoperative` warning is emitted when `quotaUserFailOpenFraction >= 0.5` to surface the weakened monopolization-prevention posture to operators. §11.2 "Maximum Overshoot Formula" is updated to state the new per-user bound alongside the aggregate per-replica and dual-outage bounds.

**Section:** `spec/12_storage-architecture.md` §12.4 "Per-user fail-open ceiling", `spec/11_policy-and-controls.md` §11.2 "Maximum Overshoot Formula"

§12.4 defines the per-user in-memory ceiling during a Redis outage as `per_user_failopen_ceiling = min(tenant_limit * userFailOpenFraction, per_replica_hard_cap)` (default `userFailOpenFraction = 0.25`), where `per_replica_hard_cap` defaults to `tenant_limit / 2`. The intent (stated in the same paragraph) is that "a single user cannot consume more than 25% of the tenant's fail-open allocation on one replica." But the `min()` expression does not enforce that intent whenever `per_replica_hard_cap < tenant_limit * userFailOpenFraction`, and more problematically, `per_replica_hard_cap` is a **per-tenant** cap — it bounds the aggregate tenant exposure on the replica, not a per-user value. When it is the smaller of the two operands, a single user is permitted up to the entire tenant's per-replica budget (`tenant_limit / 2` by default), and `userFailOpenFraction` becomes inoperative. Concretely, for a tenant with the default `userFailOpenFraction: 0.25` and `per_replica_hard_cap: tenant_limit/2`, the intended per-user cap is `0.25 * tenant_limit`; the formula as written evaluates to `min(0.25 * tenant_limit, 0.5 * tenant_limit) = 0.25 * tenant_limit` — correct only by accident of the default ratios. Any deployer who lowers `per_replica_hard_cap` (e.g., raises `quotaPerReplicaHardCap` denominator to 4 so `hard_cap = tenant_limit/4`) or raises `userFailOpenFraction` above the implicit ratio (0.5) will get a silent contract inversion: a single user admitted up to the whole replica's tenant-wide budget, not their user-fraction. The security-control intent of `userFailOpenFraction` — preventing one abusive user from monopolizing an outage window — is defeated without any warning or alert.

**Recommendation:** Change the composition so the user cap is a pure fraction of the effective per-replica ceiling, not a `min()` with it: `per_user_failopen_ceiling = effective_ceiling * userFailOpenFraction` where `effective_ceiling = min(tenant_limit / max(cached_replica_count, 1), per_replica_hard_cap)` (already defined in §12.4). Retire the `min(..., per_replica_hard_cap)` construction — `per_replica_hard_cap` should bound the tenant aggregate, never the per-user value. Add a config-time check at chart render / `lenny-ops` startup that asserts `0 < userFailOpenFraction <= 1.0` and emits a warning (`QuotaFailOpenUserFractionInoperative`) at startup if the deployer's chosen values would allow `per_user_failopen_ceiling >= tenant_limit * 0.5`. Update the §11.2 Maximum Overshoot Formula to reflect the new composition so operators can audit their overshoot budget without re-deriving the per-user/aggregate relationship.

---

### STO-018. Sampled-HEAD test-restore retains percentage-only detection floor; undetectable 0.1–0.5% artifact-loss window persists at Tier 3 [Low]

**Section:** `spec/25_agent-operability.md` §25.11 Test Restore (ArtifactStore sampled-HEAD verification)

This finding was raised in iter4 as STR-015 and is **not** closed by the current spec. §25.11 still specifies a single `backups.verification.artifactSampleSize` default of 100 and a 99% success floor, without the tier-tiered defaults or absolute-miss threshold the iter4 recommendation asked for. The statistical inference from N=100 remains weak at Tier 3's typical `artifact_store` row counts (tens of millions): a 0.5% actual replication loss has approximately 40% probability of producing a 100/100 sample pass, and the monthly verification cadence plus small sample window means persistent silent artifact loss on the order of 0.1–0.5% can accumulate without ever tripping `lenny_restore_test_success = 0`. The `lenny_restore_test_artifact_missing_total` counter (added in iter5 per the metric registry in §16.1 line 240) is a welcome raw signal but it is not wired to a failure threshold — the monitoring gate remains the 99% percentage, not an absolute-miss integer. Convergence-wise this is Low severity because (a) the `lenny_minio_replication_lag_seconds` gauge still catches fresh loss via lag and (b) old-replication decay is slow, but it remains the primary bit-rot detection surface and continues to undershoot the mission at Tier 3 scale.

**Recommendation:** Either (a) tier-tier the `backups.verification.artifactSampleSize` default by tier preset (T1: 100; T2: 1000; T3: 10000) and add a companion `backups.verification.artifactAbsoluteMissingThreshold` (default 0 at T3) that sets `lenny_restore_test_success = 0` on any sample miss at T3 regardless of percentage, or (b) replace the sampled gate with a monthly full bucket-inventory diff (`mc diff` against replication target) at T3, documenting the one-scan-per-month operational cost as acceptable for the certainty gained. Either option closes the statistical undershoot while keeping the test-restore cadence bounded. The iter4 recommendation text applies essentially unchanged; the carry-forward is listed here only so iter5 tracking surfaces it.

---

### STO-019. Backup reconciler still lacks a chart-render / startup-time invariant check on `audit.gdprRetentionDays >= backups.retention.retainDays + preRestoreRetainDays` [Low]

**Section:** `spec/12_storage-architecture.md` §12.8 Post-restore reconciler phase 1 + Receipt survivability, `spec/25_agent-operability.md` §25.11 Retention Enforcement + Restore Execution step 3

This finding was raised in iter4 as STR-016. The iter5 spec adds two partial mitigations but not the direct invariant gate the iter4 recommendation asked for:

1. §12.8 "Receipt survivability" now rejects `POST /v1/admin/restore/execute` when `backup.completed_at < now() - audit.gdprRetentionDays` with `RESTORE_INCOMPATIBLE: backup predates GDPR retention floor`. This catches the worst-case failure at restore time but not at configuration time — a deployer who silently lowers `audit.gdprRetentionDays` will not learn the configuration is unsafe until the next restore attempt fails; meanwhile, daily backups keep flowing and the receipt-survivability invariant the reconciler relies on is silently broken.
2. §17.8.1 declares a compliance-profile floor of 2190 days on `audit.gdprRetentionDays` for `soc2` / `fedramp` / `hipaa` and §12.8 rejects configurations below that floor. This catches regulated deployments but not non-regulated ones — a non-regulated deployer can still set `audit.gdprRetentionDays: 30` and `backups.retention.retainDays: 90` with no configuration-time error, breaking the invariant the reconciler's enumeration correctness argument depends on.

The canonical inequality documented in §12.8 ("always exceeds the 90-day maximum `backups.retention.retainDays`") remains unenforced at chart render / `lenny-ops` startup. The `backups.retention.preRestoreRetainDays` (default 7) is not factored in either — pre-restore backups can outlive an erased user's receipt by up to 7 days on the long side. Convergence-wise this is Low severity because the compliance-profile floor catches the most likely regulated-deployment footgun and the restore-time gate prevents the reconciler from running against an unsafe backup age, but the invariant is first-class per the §12.8 correctness argument and should be enforced at the configuration level.

**Recommendation:** Add a config-time validation gate at chart render (`helm template` schema) and at `lenny-ops` startup:

```
INVARIANT audit.gdprRetentionDays >= backups.retention.retainDays + backups.retention.preRestoreRetainDays
  (and, when complianceProfile is regulated, >= 2190)
```

Failure emits `CONFIG_INVALID: audit.gdprRetentionDays (<N>) must be >= backups.retention.retainDays (<M>) + backups.retention.preRestoreRetainDays (<K>); backup reconciler cannot guarantee receipt survivability.` Document the invariant in §12.8 Receipt survivability alongside "always exceeds the 90-day maximum" so future readers see it as a rule rather than a consequence. Add a runtime monitor alert `GdprReceiptRetentionBelowBackupRetention` that fires if `PUT /v1/admin/backups/policy` mutates `retainDays` in a way that breaks the invariant, so live drift is surfaced rather than detected at restore time.

---

### STO-020. Legal-hold checkpoint accumulation has no tenant-level safety valve; an indefinite hold on a long-running session can starve the tenant's shared `storageQuotaBytes` until every write fails [Medium] — **Fixed**

**Status:** Fixed (iter5 spec update).

**Resolution summary.** The spec now adds a predictive early-warning alert and a dedicated operator runbook so the silent-cascade failure mode identified in this finding is converted into an actionable notification before the fail-closed quota gate is hit. Specifically: (a) [§16.5](../../../spec/16_observability.md#165-alerting-rules-and-slos) defines `LegalHoldCheckpointAccumulationProjectedBreach` (Warning) which fires when `(lenny_storage_quota_bytes_used + lenny_legal_hold_checkpoint_projected_growth_bytes) > 0.9 * storageQuotaBytes` for any tenant with an active hold (`lenny_tenant_legal_hold_active_count > 0`); (b) [§16.1](../../../spec/16_observability.md#161-metrics) registers the two supporting gauges `lenny_tenant_legal_hold_active_count` (labeled by `tenant_id`) and `lenny_legal_hold_checkpoint_projected_growth_bytes` (labeled by `tenant_id`, `root_session_id`), computed by the controller from `periodicCheckpointIntervalSeconds` and a trailing 1-hour average checkpoint size; (c) [§17.7](../../../spec/17_deployment-topology.md#177-operational-runbooks) adds the `docs/runbooks/legal-hold-quota-pressure.md` runbook whose remediation decision tree is either raise `storageQuotaBytes` via the existing `PUT /v1/admin/tenants/{id}` admin API or accept the held-session checkpoint freeze; (d) [§12.5](../../../spec/12_storage-architecture.md#125-artifact-store) Legal-hold accumulation bullet and [§12.8](../../../spec/12_storage-architecture.md#128-compliance-interfaces) Legal hold and checkpoint rotation both point at the new alert and runbook.

**Scope boundary: admin grant endpoint deliberately not added.** The recommendation proposed a dedicated `POST /v1/admin/tenants/{id}/legal-hold-storage-grant` endpoint with a new `tenant_legal_hold_storage_grants` Postgres table and Lua-script integration. That was skipped as over-engineering: operators already have `PUT /v1/admin/tenants/{id}` to raise `storageQuotaBytes` directly, and the grant endpoint's scoped-to-hold abstraction plus retraction semantics would be a genuine architectural addition (new admin capability, new persistent state, new interaction with the fail-closed `storage_quota_reserve.lua` gate) without closing a gap the existing endpoint cannot. The core finding — operator lacks early warning — is resolved by the alert + runbook combination; the direct-quota-raise remediation uses existing API surface. This keeps the fix minimal and additive (observability only; no architectural change).

**Regression check.** §11.2 storage quota enforcement text (fail-closed `storage_quota_reserve.lua`) is unchanged — the new alert fires strictly ahead of the 80% reactive thresholds and the 100% fail-closed gate, not in place of them. §12.8 "no forced deletion during a hold" invariant is unchanged — the alert is a notification, not an enforcement handoff.

---

<details>
<summary>Original finding (for reference)</summary>

### STO-020. Legal-hold checkpoint accumulation has no tenant-level safety valve; an indefinite hold on a long-running session can starve the tenant's shared `storageQuotaBytes` until every write fails [Medium]

**Section:** `spec/12_storage-architecture.md` §12.5 "Checkpoint-storage sizing guidance" (Legal-hold accumulation bullet) + §12.8 "Legal hold and checkpoint rotation"; `spec/11_policy-and-controls.md` §11.2 Storage quota enforcement

§12.5 is explicit that (a) legal-hold sessions are exempt from the "latest 2 checkpoints" rotation — checkpoints accumulate at the `periodicCheckpointIntervalSeconds` rate (600s default / 10 min) for the full session duration, (b) `storageQuotaBytes` is a single shared bucket across checkpoints, workspace snapshots, uploads, transcripts, and eviction contexts, (c) "there is no v1 per-tenant legal-hold storage cap or opt-out — suspension of rotation is a compliance requirement (spoliation avoidance), and caps that could force deletion during a hold are explicitly out of scope for v1." The combination creates an operational failure mode with no specified mitigation: a 24-hour hold on a single active session retains ~144 checkpoints instead of 2 (72× baseline), a multi-day hold or a hold whose clearance is blocked on external discovery counsel can reach hundreds or thousands of checkpoint objects, and because the quota is a single bucket, the accumulation eventually pushes the tenant over `storageQuotaBytes`. The `storage_quota_reserve.lua` atomic reserve in §11.2 then fails-closed — but "fail-closed" here means **every new upload in the tenant is rejected**: new checkpoints (including checkpoints for the held session itself, which then stops being able to preserve newer state), workspace snapshots, uploaded files, and transcripts. Spoliation-avoidance intent is defeated the moment the held session loses its own quota headroom: the session can no longer snapshot the state subject to the hold, and the hold's evidentiary value degrades with every failed checkpoint. The spec explicitly declines to provide a relief valve ("out of scope for v1"), but at minimum the operator needs (a) a predictive alert, (b) a documented runbook, and (c) a mechanism for the platform admin to raise the tenant's quota administratively without racing the held session. None of these are specified. An admin who does not watch `lenny_storage_quota_bytes_used` per-tenant and cross-correlate to active legal holds will discover the quota breach as a cascading failure, potentially during an ongoing discovery motion. At Tier 3 scale where holds are routine, this is a realistic reliability and compliance risk rather than a corner case.

**Recommendation:** Without overriding the "no forced deletion during a hold" rule, add three deterministic mitigations:

1. Add a predictive alert `LegalHoldCheckpointAccumulationProjectedBreach` fired when `(current_usage + projected_hold_growth_over_24h) > storageQuotaBytes * 0.9` for any tenant with an active hold. Projection inputs: active hold count, each held session's `periodicCheckpointIntervalSeconds`, average observed `lenny_checkpoint_workspace_bytes_total` for the session. Emit the alert labeled by `tenant_id` and `root_session_id`.
2. Specify a `POST /v1/admin/tenants/{id}/legal-hold-storage-grant` admin action (`platform-admin`, audited) that grants an operator-specified byte delta to the tenant's effective `storageQuotaBytes` scoped to the lifetime of specific named holds. Record the grant in a new `tenant_legal_hold_storage_grants` table keyed by `(tenant_id, grant_id, associated_hold_ids[])` and factor the grant into the atomic Lua reserve as an additive headroom on the tenant quota check. On hold clearance, the grant becomes eligible for retraction (not automatic — an admin confirms via `POST /v1/admin/tenants/{id}/legal-hold-storage-grant/{id}/retract`). This gives operators a documented path to keep a hold running past the baseline quota without the platform silently resolving the conflict in one direction.
3. Document in §12.5 and §12.8 the operator runbook for "hold + quota breach": monitor the alert, respond via the grant endpoint, and note the prerequisite sizing-guidance footnote is now supplemented by a real-time telemetry check. The existing §12.5 sizing guidance ("size `storageQuotaBytes` for tenants with active or expected legal holds") is static advice; add the telemetry-driven alert + grant mechanism to make it operational.

This is Medium because spoliation-by-quota-starvation is a real failure mode under v1's explicit design decisions and there is no specified operational recovery path other than ad-hoc Redis intervention. Raising severity to High would be appropriate if the user expects the platform to never force an operator choice; keeping at Medium reflects that the platform declines to auto-resolve by design and the missing pieces are telemetry + admin endpoint + runbook.

</details>

---

### STO-021. T4 per-tenant KMS key availability is validated only at admin-time and first-write; silent post-provisioning revocation or provider-side lifecycle drift is not continuously probed [Medium] — **Fixed**

**Status:** Fixed (iter5 spec update).

**Resolution summary.** The spec now adds a leader-elected continuous KMS probe, the two supporting metrics, a critical alert, and an admin-API response field so the idle-tenant silent-revocation failure mode identified in this finding is converted into a pre-write, continuously-observable signal. Specifically: (a) [§12.5](../../../spec/12_storage-architecture.md#125-artifact-store) "T4 per-tenant KMS key lifecycle" adds bullet 4 "Continuous probe (STO-021)" specifying the leader-elected probe goroutine (co-located under the existing `lenny-gateway-leader` Lease with the GC writer), cadence (`storage.t4KmsProbeInterval` default 300s, min 60s), rate limit (`storage.t4KmsProbeRateLimit` default 10 probes/sec), no-auto-downgrade semantics, and the explicit invariant that the addition is pure observability — the fail-closed write semantics in bullet 2 and the admin-time probe in bullet 1 are unchanged; (b) [§16.1](../../../spec/16_observability.md#161-metrics) under "KMS & Signing" registers `lenny_t4_kms_probe_last_success_timestamp` (gauge, labeled by `tenant_id`) and `lenny_t4_kms_probe_result_total` (counter, labeled by `tenant_id` and `outcome ∈ {success, failure}`); (c) [§16.5](../../../spec/16_observability.md#165-alerting-rules-and-slos) defines the critical alert `T4KmsKeyUnusable` firing on `time() - max by (tenant_id) (lenny_t4_kms_probe_last_success_timestamp) > 2 * storage.t4KmsProbeInterval` with remediation pointer to `PUT /v1/admin/tenants/{id}` re-assert; (d) [§15.1](../../../spec/15_external-api-surface.md#151-rest-api) annotates `GET /v1/admin/tenants/{id}` and `PUT /v1/admin/tenants/{id}` with the new `t4KmsLastProbeSuccessAt` response field (RFC 3339 UTC timestamp, T4 tenants only; present on both GET and PUT-re-assert responses); (e) [§17.8.1](../../../spec/17_deployment-topology.md#1781-operational-defaults--quick-reference) "Operational Defaults — Quick Reference" lists `storage.t4KmsProbeInterval` (default 300s, min floor 60s enforced at Helm-validate) and `storage.t4KmsProbeRateLimit` (default 10 probes/sec).

**Scope boundary: no auto-downgrade, no schema changes.** The recommendation intentionally does not include tier auto-reclassification on probe failure (that would be a silent classification change and is excluded by the spec's fail-closed posture). The admin API response field is an additive schema change on an existing endpoint; no new endpoint, no new Postgres table, no new Redis key. The probe shares the existing `lenny-gateway-leader` Lease with the GC writer, so the addition is a second goroutine under an already-specified primitive and does not introduce a second leader-election surface.

**Regression check.** §12.5 "Encryption at rest" is unchanged — the new probe does not alter encryption-key selection for T3/T4 writes. Bullet 1 (pre-provisioning model with admin-time probe) and bullet 2 (fail-closed on write-time KMS unavailability, idempotent `PUT /v1/admin/tenants/{id}` restoration) remain verbatim; bullet 4 explicitly labels itself "pure observability addition" and references both bullets. The existing `CheckpointStorageUnavailable` alert and `lenny_checkpoint_storage_failure_total{reason="kms_unavailable"}` metric are unchanged; the new alert fires strictly ahead of that reactive cascade on the idle-tenant path. No Tier 3 / Tier 4 escalation rules are introduced — the probe runs at all tiers carrying any T4 tenant.

---

<details>
<summary>Original finding (for reference)</summary>

### STO-021. T4 per-tenant KMS key availability is validated only at admin-time and first-write; silent post-provisioning revocation or provider-side lifecycle drift is not continuously probed [Medium]

**Section:** `spec/12_storage-architecture.md` §12.5 "T4 per-tenant KMS key lifecycle" + "Encryption at rest"

§12.5 correctly moves the T4 KMS probe from the first-write path to `PUT /v1/admin/tenants/{id}`'s `workspaceTier: T4` transition: the admin-time probe performs a zero-byte encrypt/decrypt round-trip before persisting the tenant's classification change, eliminating the first-write race. The admin endpoint is also documented as idempotent — a re-assert of `workspaceTier: T4` forces the probe to re-run on restoration. What is NOT specified is any **continuous** monitoring of the T4 KMS key's usability after the initial probe:

- No periodic background probe is scheduled against the tenant's `tenant:{tenant_id}` KMS key.
- No `KmsKeyUnhealthy` alert is defined that would fire when a tenant is at T4 and the provider-side key is subsequently disabled, rotated-out-of-usable-state, or permission-revoked by an IAM change. The §12.5 text does state that a KMS outage at write time produces `CLASSIFICATION_CONTROL_VIOLATION`, propagates `CheckpointStorageUnavailable`, and populates `lenny_checkpoint_storage_failure_total{reason="kms_unavailable"}`, but those signals fire reactively — only when a write is attempted — so a tenant that has gone idle can drift into "T4 but KMS unusable" for an unbounded window before the next checkpoint tries. 
- No metric tracks the last-successful-probe timestamp per T4 tenant, so operators cannot dashboard `min(lastProbeSuccessAt)` across T4 tenants to detect silent revocation.
- The operator restoration path ("re-enable the KMS key, re-run the idempotent `PUT /v1/admin/tenants/{id}` to force the probe") assumes the operator already knows the key is broken — but the platform provides no signal to tell them that.

At the same time, the spec explicitly mandates fail-closed on KMS unavailability: "writes rejected during the outage are lost; operators who require stronger durability during KMS outages must operate KMS in a highly available configuration or run with `workspaceTier: T3`." The fail-closed posture is correct; what is missing is the monitoring that lets an operator decide whether T4 is in a fail-closed state proactively rather than after a checkpoint failure cascades.

**Recommendation:** Add three deterministic mitigations under §12.5 "T4 per-tenant KMS key lifecycle":

1. **Periodic probe.** A leader-elected background probe runs every `storage.t4KmsProbeInterval` (default 300s / 5 min) against every tenant with `workspaceTier: T4`, performing the same zero-byte encrypt/decrypt round-trip used at admin time. Results update a `lenny_t4_kms_probe_last_success_timestamp` gauge per tenant and a `lenny_t4_kms_probe_result_total{outcome}` counter. The probe is rate-limited per KMS provider (`storage.t4KmsProbeRateLimit`, default 10 probes/sec aggregated) so operators with hundreds of T4 tenants don't saturate KMS.
2. **Alert.** `T4KmsKeyUnusable` (critical) fires when `now() - lenny_t4_kms_probe_last_success_timestamp{tenant_id=$t} > 2 * storage.t4KmsProbeInterval` for any T4 tenant. The alert fires in lockstep with the `CheckpointStorageUnavailable{reason="kms_unavailable"}` path but catches idle tenants before they try to write.
3. **Admin endpoint visibility.** `GET /v1/admin/tenants/{id}` includes `t4KmsLastProbeSuccessAt` in the response for T4 tenants so an operator's ad-hoc inspection surfaces the state without requiring a metric lookup. On forced re-assert via `PUT /v1/admin/tenants/{id}` with an unchanged payload, the response also carries the latest probe outcome.

Document the operator behavior: the platform does not *auto-downgrade* a T4 tenant to T3 on probe failure (that would be a silent classification change), but it *does* make the unusable-key state continuously observable. Severity is Medium because the fail-closed write path does protect cryptographic-erasure semantics — no data at rest ever becomes un-shreddable — so the risk is operational (silent degradation into fail-closed writes) rather than a confidentiality or integrity breach.

</details>

---

## Summary

- **Carry-forwards from iter4 closed:** STR-011, STR-012, STR-013, STR-014 — iter5 spec fully addresses each via §10.1 state-frame synthesis, §10.1 partial-manifest `deleted_at` + supersede-on-write + unique index, §10.1 intent-row-first ordering, and §12.6 EventBus retranscribe worker respectively.
- **Carry-forwards from iter4 mitigated but not closed:** STR-015 (STO-018) and STR-016 (STO-019) — both held at Low per iter4 calibration; neither is blocking convergence.
- **New issues raised in iter5:** STO-017 (Medium, fail-open ceiling composition), STO-020 (Medium, legal-hold quota starvation — **Fixed**), STO-021 (Medium, T4 KMS silent-revocation monitoring — **Fixed**).
- **Convergence verdict:** Not yet — one Medium finding remains (STO-017) after STO-020 and STO-021 were resolved via the observability-only fixes described above. Zero Critical, zero High. Two Low carry-forwards (STO-018, STO-019) may be accepted at the reviewer's discretion without blocking convergence; they are flagged as polish rather than must-fix.

---

# Perspective 10 — Recursive Delegation (iter5)

**Scope:** `spec/08_recursive-delegation.md` (focused file for this iteration).

**Previous-iteration (iter4) fixes verified:**

- **DEL-011 (`treeVisibility` inheritance semantics).** Present at §8.3 lines 261–267. The spec defines the `full → parent-and-self → self-only` ordering, the three inheritance cases (omit → inherit parent; same-or-narrower → accept; broader → reject with `TREE_VISIBILITY_WEAKENING`), analogy to `CONTENT_POLICY_WEAKENING` / `DELEGATION_POLICY_WEAKENING`, and snapshot/extension behavior at lines 267 (`treeVisibility` is carried on the lease; excluded from `snapshotPolicyAtLease`; not extendable via §8.6). Fix is complete.
- **DEL-012 (`treeVisibility` in lease schema).** Present at §8.3 line 224 (`"treeVisibility": "full"` in the lease JSON example) and line 259 (normative field description with the three enum values, default, and linkage to `lenny/get_task_tree` and `messagingScope`). Fix is complete.
- **DEL-013 (`messagingScope` vs. `treeVisibility` compatibility check inputs).** Present at §8.3 lines 269–274 (normative heading "`treeVisibility` vs. `messagingScope` — delegation-time compatibility check"). The check explicitly resolves (1) child effective `messagingScope` per the §7.2 hierarchy, (2) child effective `treeVisibility` per the §8.3 inheritance rules, and (3) rejects with `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE` when `siblings` + non-`full`. Error details include `effectiveMessagingScope`, `effectiveTreeVisibility`, and `requiredTreeVisibility`. The paragraph at line 274 handles post-delegation `messagingScope` hierarchy changes (existing leases preserve their `treeVisibility`; effective `messagingScope` narrowed to `direct` for active children that would otherwise need `full`). §8.6 line 573 already lists `treeVisibility` among non-extendable fields. Fix is complete.

**New issues in §8 found this iteration:**

None. After a full pass of `spec/08_recursive-delegation.md` against the strict iter5 severity calibration (Critical = correctness/safety violation; High = MUST fix pre-prod; Medium = SHOULD address, has workaround; Low/Info = polish/clarity), no new correctness or safety bugs were identified. The remaining residual items observed during the pass fall into Low/Info clarity territory that is either already tracked elsewhere (e.g., DEL-008 orphan-cap audit event — carried forward from prior iteration) or does not rise above the iter5 calibration bar:

- `tracingContext` merge semantics (§8.3 line 234) state that "child entries are merged with parent entries; child entries cannot overwrite or remove parent entries," but the specific error code for an attempted overwrite is not called out in the validation table at lines 238–245. This is Low at worst and anchors to the prior-iteration DEL-011-series rubric for tracing-context error catalog completeness, which is already captured by iter4's DEL-011 (carry-forward). No new finding is warranted under iter5 anchoring.
- The `deadlock_detected` event schema (§8.8 lines 906–918) surfaces `blockedRequests` with descendant `taskId`s on the subtree-root's `await_children` stream. Under `treeVisibility: self-only` or `parent-and-self`, a parent that called `await_children` on its direct children may still receive descendant `taskId`s if the deadlocked subtree extends below. This is a visibility consistency note, not a correctness/safety violation (the parent already awaits the descendant chain's settlement transitively, and taskIds are not cross-tenant). Below the iter5 bar.
- §8.10 `cascadeOnFailure: await_completion` + parent already terminal: who collects results is left informal. Stable across iterations; below the iter5 bar.

**Convergence assessment**

- Critical: 0
- High: 0
- Medium: 0
- Low: 0
- Info: 0

New findings introduced this iteration: **0**. Iter4's three targeted fixes (DEL-011, DEL-012, DEL-013) are all present, wired correctly into §8.3 and §8.6, and internally consistent with the §8.5 `get_task_tree` description, the §8.6 non-extendable-fields list, and the §15.1 error catalog references.

Convergence (this perspective, this iteration): **Y**.

---

## 11. Session Lifecycle & State Management

### SES-019 (carry-forward from iter4) — `POST /v1/sessions/{id}/start` precondition/result table still omits `resume_pending`
**Severity:** Low
**Location:** `spec/15_external-api-surface.md` §15.1 line ~618
**Finding:** The endpoint row for `POST /v1/sessions/{id}/start` still lists only `starting → running` as the resulting transition and does not enumerate the `starting → resume_pending` outcome that occurs when a checkpoint is available and workspace rehydration is required. §7.2 and §6.2 both document `starting → resume_pending` as a valid edge, but the external-API surface table remains inconsistent with the internal state machine.
**Impact:** Clients consulting only the external API reference will not know that `POST /start` can legitimately leave the session in `resume_pending` awaiting workspace rehydration, leading to confused client state machines and incorrect assumptions that `202 Accepted` from `/start` always transitions toward `running`.
**Recommendation:** Update the §15.1 row for `POST /v1/sessions/{id}/start` to list the resulting transitions as `starting → running | starting → resume_pending`, with a note that the choice depends on whether a checkpoint is present and workspace rehydration is required.
**Status vs iter4:** Carry-forward — issue identified in iter4 as SES-019; iter5 spec unchanged at this location.

---

### SES-020 (carry-forward from iter4) — SSE reconnect/replay has no rate limit or per-reconnect replay cap
**Severity:** Low
**Location:** `spec/07_session-lifecycle.md` §7.2 SSE reconnection policy (lines ~349–363); `spec/10_gateway-internals.md` §10.4 event replay buffer
**Finding:** The replay window is specified as `max(periodicCheckpointIntervalSeconds × 2, 1200s)` with a bounded-error `OutboundChannel` policy, but there is no per-session or per-principal reconnect rate limit (a client can reconnect with `Last-Event-ID` arbitrarily often) and no cap on the per-reconnect replay volume (a long-idle client reconnecting at the 1200s horizon will replay the full window on every attempt). A malicious or buggy client can thereby impose amplification load on the gateway replay path.
**Impact:** Resource-exhaustion vector (CPU + bandwidth amplification) against the gateway's event replay subsystem; no denial-of-service guardrail at the session-event API surface. Not a correctness bug, but a missing reliability control.
**Recommendation:** Add a per-session reconnect rate limit (e.g., token bucket with configurable burst/refill) in `spec/11_policy-and-controls.md` alongside the existing `sessionEventReplayBufferDepth` policy, and specify a per-reconnect replay byte cap beyond which the gateway emits `gap_detected` and requires the client to re-snapshot.
**Status vs iter4:** Carry-forward — issue identified in iter4 as SES-020; iter5 spec text at §7.2 lines 349–363 is unchanged.

---

### SES-021 (carry-forward from iter4) — `awaiting_client_action → expired` trigger is ambiguous across three timers
**Severity:** Low
**Location:** `spec/07_session-lifecycle.md` §7.2 line ~202
**Finding:** The edge `awaiting_client_action → expired (lease/budget/deadline exhausted while awaiting client action)` collapses three distinct timers into a single free-text trigger: (a) `maxAwaitingClientActionSeconds` (900s default, a state-specific timer from §11), (b) session budget exhaustion, and (c) absolute session TTL / deadline. The spec does not disambiguate which timer fires the transition or which `terminal_reason` is recorded for each case.
**Impact:** Operators cannot distinguish "client failed to respond in time" from "budget exceeded while waiting" from "absolute deadline hit" in audit/observability. Incident triage and billing reconciliation both suffer, and clients cannot programmatically distinguish the three cases from the terminal event payload.
**Recommendation:** Split the §7.2 transition row into three rows (or enumerate three sub-triggers) with distinct `terminal_reason` codes — e.g., `awaiting_client_action_timeout`, `budget_exhausted`, `session_deadline_exceeded` — and reference the relevant policy knob from §11 for each.
**Status vs iter4:** Carry-forward — issue identified in iter4 as SES-021; iter5 spec at §7.2 line 202 is unchanged.

---

### Convergence assessment (Perspective 11)

**Counts:**
- Critical: 0
- High: 0
- Medium: 0
- Low: 3
- Info: 0

**Per-finding status vs. iter4:**
- SES-015 (`resuming → cancelled/completed` missing from §6.2): **Fixed in iter5** — §6.2 lines 129–135 now enumerate both terminal edges with snapshot-close semantics.
- SES-016 (pre-attach terminal collapse `resume_pending → cancelled/completed` unspecified): **Fixed in iter5** — §7.2 lines 193–194 and §6.2 lines 137–139 now enumerate the pre-attach collapse edges.
- SES-017 (generation-counter bookkeeping for mid-resume terminals unspecified): **Fixed in iter5** — §7.2 "Mid-resume terminal transitions — snapshot-close semantics" step 4 covers generation bookkeeping; §4.2 line 156 formalizes `recovery_generation` / `coordination_generation` invariants.
- SES-018 (`created → failed` derive-failure edge + atomicity + reachability): **Fixed in iter5** — §7.1 line 28 conditions atomicity on `persistDeriveFailureRows: false`; §7.2 line 164 enumerates the edge; §15.1 lines 647–663 provide the audit-row reachability table.
- SES-019 (`POST /start` precondition/result table missing `resume_pending`): **Carry-forward** — still open in iter5.
- SES-020 (SSE reconnect rate limit + per-reconnect replay cap missing): **Carry-forward** — still open in iter5.
- SES-021 (`awaiting_client_action → expired` trigger disambiguation): **Carry-forward** — still open in iter5.

**New findings in iter5:** None. No new correctness or reliability bugs were identified in §6.2, §7, §10.1–10.4, §11, or §15.1 session-lifecycle scope beyond the three iter4 carry-forwards.

**Verdict:** **Not converged.**

Rationale: Four of seven iter4 findings are closed (including all Medium/High items), and no new findings were introduced. However, three Low items (SES-019, SES-020, SES-021) remain unaddressed in iter5. Each is a documentation/policy completeness gap with clear, bounded fixes; none blocks deployment individually, but convergence requires zero open findings against the prior iteration. Recommend closing all three in iter6 via the targeted edits above.

---

---

## 12. Observability & Operational Monitoring

### OBS-031. `RedisUnavailable` runbook trigger references an undefined alert [High] — **Fixed**

- **Sections:** spec/17_deployment-topology.md §17.7 line 740; spec/16_observability.md §16.5 line 482
- **Symptom:** The Redis-failure runbook (`docs/runbooks/redis-failure.md`) declares its trigger as ``*Trigger:* `RedisUnavailable` alert fires; quota enforcement logs switch to fail-open mode; `lenny_quota_redis_fallback_total` counter increments``. The `DurableInboxRedisUnavailable` row at §16.5 line 482 reinforces the name by noting "deployers that already page on `RedisUnavailable` MAY suppress this alert". But no `RedisUnavailable` row exists in the §16.5 table — the §16.5 catalog lists only `DurableInboxRedisUnavailable` (inbox-scoped), and the `CircuitBreakerStale` row at line 505 softens any other Redis signal to a `RedisUnreachable`-class category. Iter4 OBS-023 closed an identical gap by either renaming the runbook/tier trigger to match an existing alert name or adding the missing row.
- **Why this matters:** Runbooks are the operator contract for alert→response. A runbook keyed to a non-existent alert cannot be discovered via routing from the alertmanager (the alert name is the index), and the §25.13 bundled `PrometheusRule` export will not emit a rule matching this trigger, so on-call teams that follow the §25.13 single-source-of-truth guarantee will never receive the page this runbook assumes. The §16.5 line 482 suppression hint makes the gap worse by instructing deployers to suppress an alert they never installed.
- **Severity:** High — this is a direct regression of the iter4 OBS-023 class (runbooks/tiers referencing alert names not defined in §16.5) that the iter4 fix was expected to have eliminated across all sections; severity mirrors iter4 OBS-023 (also High).
- **Fix options:** (a) add a `RedisUnavailable` row to §16.5 (symmetric with `SessionStoreUnavailable` at line 389), backed by the existing `lenny_quota_redis_fallback_total` counter; or (b) rewrite the §17.7 trigger and §16.5 line 482 suppression hint to reference the already-defined `DurableInboxRedisUnavailable` plus a cluster-wide Redis probe alert that is actually emitted by the chart.
- **Resolution:** Fix option (a) applied. Added a `RedisUnavailable` Critical alert row to §16.5 — placed immediately after `SessionStoreUnavailable` to mirror the `<Component>Unavailable` cluster-wide storage probe pattern — with expression `rate(lenny_quota_redis_fallback_total[2m]) > 0` sustained for more than 1 minute, explicitly scoped to the Quota/Rate Limiting Redis instance and cross-referencing the `docs/runbooks/redis-failure.md` runbook ([§17.7](spec/17_deployment-topology.md#177-operational-runbooks)). Registered the backing metric `lenny_quota_redis_fallback_total` in §16.1 (it had been referenced in the §17.7 runbook and §12.4 fail-open architecture but never defined). Tightened the `DurableInboxRedisUnavailable` row to disambiguate the two Redis instances (Coordination Redis versus Quota/Rate Limiting Redis) and to scope the suppression hint to co-located Tier 1/2 deployments only. Synced docs/reference/metrics.md to add both the metric and the alert.

### OBS-032. `MinIOUnavailable` runbook trigger references an undefined alert [High] — **Fixed**

- **Sections:** spec/17_deployment-topology.md §17.7 line 760; spec/16_observability.md §16.5
- **Symptom:** The MinIO-failure runbook declares its trigger as ``*Trigger:* `MinIOUnavailable` alert; workspace upload/download failures; `lenny_artifact_upload_error_total` spikes``. No `MinIOUnavailable` row exists in §16.5; the catalog carries only `MinIOArtifactReplicationLagHigh` (line 515) and `MinIOArtifactReplicationFailed` (line 516), both replication-scoped. `WorkspaceSealStuck` (line 480) fires on sustained MinIO unavailability but is scoped to a different failure mode ("seal-and-export retrying for longer than `maxWorkspaceSealDurationSeconds`") and keyed off a different metric.
- **Why this matters:** Same class as OBS-031: the MinIO runbook will not be reachable from alertmanager routing under the §25.13 single-source-of-truth model, and `lenny_artifact_upload_error_total` — the metric named in the trigger — has no alert defined on it in §16.1 or §16.5, so the spiking-spikes signal is never converted to a page.
- **Severity:** High — second instance of the iter4 OBS-023 regression pattern (runbook references undefined alert). Severity matched to OBS-031 / iter4 OBS-023.
- **Fix options:** add a `MinIOUnavailable` row to §16.5 backed by a probe on `lenny_artifact_upload_error_total` rate (or the MinIO cluster `mc admin info` health endpoint); or rewrite §17.7 line 760 to trigger on the existing `WorkspaceSealStuck`/replication alerts with the runbook covering both paths.
- **Resolution:** Fix option (a) applied, parallel to the OBS-031 fix. Added a `MinIOUnavailable` Critical alert row to §16.5 — placed immediately after `CheckpointStorageUnavailable` to group the cluster-wide MinIO ArtifactStore availability signal with the checkpoint-path MinIO alert — with expression `rate(lenny_artifact_upload_error_total{error_type="minio_unreachable"}[2m]) > 0` sustained for more than 1 minute, explicitly scoped to the primary ArtifactStore bucket and disambiguated from `MinIOArtifactReplicationLagHigh` / `MinIOArtifactReplicationFailed` (replication-scoped), `CheckpointStorageUnavailable` (checkpoint-path eviction fallback), and `WorkspaceSealStuck` (seal-and-export deadline). Registered the backing metric `lenny_artifact_upload_error_total` under a new "MinIO ArtifactStore Availability" group in §16.1 with labels `tenant_id`, `bucket`, `error_type` (`minio_unreachable` \| `auth_denied` \| `quota_exceeded` \| `checksum_mismatch` \| `transport_error`); only the `minio_unreachable` label fires the critical alert, per-tenant/per-bucket classes classify causes that do not indicate cluster-wide outage. Cross-referenced `docs/runbooks/minio-failure.md` ([§17.7](spec/17_deployment-topology.md#177-operational-runbooks)) from both the alert row and the metric. Tightened the §17.7 runbook trigger to reference §16.5 (symmetric with the `TokenServiceUnavailable` runbook trigger pattern) and to explicitly enumerate paired alerts (`WorkspaceSealStuck`, `CheckpointStorageUnavailable`). Synced docs/reference/metrics.md to add both the metric and the alert.

### OBS-033. `MemoryStoreGrowthHigh` alert referenced in §9.4 but not defined in §16.5; backing metric cardinality mismatch [Medium] — **Fixed**

- **Sections:** spec/09_mcp-integration.md §9.4 line 188; spec/16_observability.md §16.1 line 146, §16.5
- **Symptom (alert):** §9.4 line 188 states "alert `MemoryStoreGrowthHigh` ([Section 16.5]...) fires when any user's memory count exceeds 80% of `memory.maxMemoriesPerUser`" but §16.5 contains no `MemoryStoreGrowthHigh` row. Iter4 OBS-023 closed exactly this pattern for other alerts; this one was not caught in the sweep.
- **Symptom (cardinality):** §9.4 line 188 describes `lenny_memory_store_record_count` as "per tenant, per user" and ties the alert to "any user's memory count". But §16.1 line 146 registers the metric with labels `tenant_id` only — "approximate count of stored memory records per tenant". A `PrometheusRule` that fires "when any user's memory count exceeds 80% of `memory.maxMemoriesPerUser`" cannot be authored against a metric without a `user_id` label. Conversely, adding `user_id` to a gauge is forbidden by §16.1.1 (line 264 forbidden-label list: `user_id` is flagged as a cardinality hot-spot).
- **Why this matters:** The alert is a load-bearing part of the §9.4 "Retention and capacity limits" contract (operators are told this is the signal for per-user cap headroom), yet the chain metric → alert → runbook has two broken links in §16.5 and the metric schema.
- **Severity:** Medium — a genuine gap: alert undefined and metric schema unable to back the described alert predicate. Not a runbook-level operator contract break (no §17.7 runbook keyed on it), so not High.
- **Fix options:** define the alert in §16.5 using a sampled per-user aggregation (e.g., a `lenny_memory_store_user_count_over_threshold` counter the MemoryStore emits on writes that exceed 80%, avoiding high-cardinality labels), then change §9.4 line 188 to match. Update §16.1 line 146 description to stop promising "per user" resolution the label set cannot deliver.
- **Resolution:** Added the `lenny_memory_store_user_count_over_threshold_total` counter (labels `tenant_id`, `backend`; no `user_id`, respecting §16.1.1 forbidden-label list) to the Memory Store block of §16.1, incremented by `MemoryStore.Write` on every commit that leaves the writing user at `>= 80%` of `memory.maxMemoriesPerUser`. Defined the `MemoryStoreGrowthHigh` Warning alert in §16.5 (placed after `ErasureJobOverdue`, matching the operator-guide placement) keyed on `sum by (tenant_id) (rate(lenny_memory_store_user_count_over_threshold_total[5m])) > 0` sustained for more than 5 minutes. Rewrote §9.4 line 188 to match the defined alert expression and describe per-user attribution via structured logs rather than metric labels. Tightened the §16.1 `lenny_memory_store_record_count` description to state explicitly that the gauge is tenant-aggregated only and to cross-reference the new counter for per-user headroom signals. Updated the §9.4 MemoryStore instrumentation contract and the §12.8 custom-backend compliance bullet to require the new counter alongside the three pre-existing memory-store metrics, so the `ValidateMemoryStoreIsolation` contract helper fails closed if a custom backend omits it. Synced `docs/reference/metrics.md` (added the new counter row, rewrote the `lenny_memory_store_record_count` description), `docs/operator-guide/observability.md` (rewrote the alert row to reference the counter expression), and `docs/operator-guide/configuration.md` (rewrote the MemoryStoreGrowthHigh explanation).

### OBS-034. `MemoryStoreErasureDurationHigh` alert referenced in §12.8 but not defined in §16.5; no backing metric registered [Medium] — **Fixed**

- **Sections:** spec/12_storage-architecture.md §12.8 line 754; spec/16_observability.md §16.1 (Memory Store block lines 143–146), §16.5
- **Symptom:** The custom-MemoryStore compliance contract at §12.8 line 754 requires deployers to emit the three memory-store metrics "so that `MemoryStoreErasureDurationHigh` alerts function identically to the default backend". §16.5 has no such row, and §16.1 lines 143–146 register only `operation_duration_seconds` (labeled by `operation`, `backend`) — no "erasure duration" variant, no `MemoryStoreErasureDurationHigh` alert backing.
- **Why this matters:** This is the fail-closed contract for GDPR Article 17 compliance of custom memory backends (cf. §12.8 line 756: "Failure to meet any of the above is a GDPR Article 17 … compliance risk"). A non-existent alert cannot enforce the contract it is named for.
- **Severity:** Medium — genuine gap with a compliance-contract hook, but not a runbook break (no §17.7 runbook keyed on it) and not a data-loss signal. Mirrors the OBS-033 severity (same class: alert referenced, not defined).
- **Fix options:** add a `MemoryStoreErasureDurationHigh` row to §16.5 backed by a histogram observation on `lenny_memory_store_operation_duration_seconds{operation="delete"}` (or a dedicated `lenny_memory_store_erasure_duration_seconds` histogram registered in §16.1), with threshold aligned to the §12.8 erasure SLA. If the compliance contract intends a different metric (e.g., elapsed time for whole-user erasure), register that metric in §16.1 first and update §12.8 to reference it by name.
- **Resolution:** Chose Option A — reuse the existing `lenny_memory_store_operation_duration_seconds` histogram by extending the `operation` enum from `{write, query, delete, list}` to `{write, query, delete, list, delete_by_user, delete_by_tenant}`. The new labels measure the synchronous whole-scope erasure calls (`MemoryStore.DeleteByUser` / `MemoryStore.DeleteByTenant`) distinctly from the per-record `operation="delete"` label used by `MemoryStore.Delete(ctx, scope, ids)`. Added the `MemoryStoreErasureDurationHigh` Warning alert row to §16.5 keyed on `histogram_quantile(0.99, sum by (le, backend) (rate(lenny_memory_store_operation_duration_seconds_bucket{operation="delete_by_user"}[5m]))) > 60` sustained for 10 minutes, with a companion `operation="delete_by_tenant"` arm at `> 300` over the same window. Thresholds chosen as per-backend leading indicators ahead of the aggregate `ErasureJobOverdue` tier deadlines (72 h T3, 1 h T4). Updated §16.1 Memory Store block entry for `operation_duration_seconds` to enumerate the six operation label values and describe the erasure-label semantics. Updated the §12.8 custom-backend compliance bullet to specify the `delete_by_user` / `delete_by_tenant` label emission contract, the P99/10-minute thresholds, and the requirement that backends implementing erasure as per-record loops still emit a single wall-clock observation under the whole-scope label. Updated §9.4 Instrumentation contract to enumerate all six operation labels and extend the `ValidateMemoryStoreIsolation` contract helper's enforcement to verify emission under every label value. Synced `docs/reference/metrics.md` (expanded the `lenny_memory_store_operation_duration_seconds` label enumeration and cross-referenced the alert), `docs/operator-guide/observability.md` (added the new alert row), and `docs/operator-guide/configuration.md` (added the leading-indicator note to the Memory Store Configuration section).

### OBS-035. `MinIOArtifactReplicationLagCritical` implied in §16.5 body but not defined as a distinct alert rule [Medium] — **Fixed**

- **Sections:** spec/16_observability.md §16.5 line 515
- **Symptom:** The `MinIOArtifactReplicationLagHigh` row body text states "Warning fires at the configured RPO; Critical fires at 4× RPO". The single row's `Severity` column is `Warning`. The catalog's established pattern for multi-threshold alerts is the `GatewayClockDrift` row (line 499), which explicitly encodes `Warning / Critical` in the severity column with the thresholds inlined in the expression (`> 0.5` / `> 2.0` / `> 5.0`). No such encoding exists for the 4× RPO Critical — neither a dedicated `MinIOArtifactReplicationLagCritical` row, nor a `Warning / Critical` dual-threshold expression.
- **Why this matters:** The §25.13 bundled `PrometheusRule` renderer derives rule metadata row-for-row from this table. A commitment made only in prose ("Critical fires at 4× RPO") will not be emitted into the `PrometheusRule` CRD, so deployer alertmanager routing keyed on `severity=critical` labels will never see it. A full-site ArtifactStore data-loss window is the canonical case for paging at Critical severity and not relying on a Warning for escalation.
- **Severity:** Medium — genuine gap between committed text and emitted rule; not a runbook-level operator contract break (the existing Warning row is derivable); downgraded from High because `MinIOArtifactReplicationFailed` (row 516) does exist and covers the hard-failure path.
- **Fix options:** either split the row into two (`MinIOArtifactReplicationLagHigh` Warning at 1×; `MinIOArtifactReplicationLagCritical` Critical at 4×) or follow the `GatewayClockDrift` precedent and collapse both thresholds into the `expr` with `severity: Warning / Critical` in the severity column. Update §25.13 smoke test, if any, to verify both severities are emitted.
- **Resolution:** Chose Option (a) — split the single row into two distinct alert rules. `MinIOArtifactReplicationLagHigh` (Warning) now fires at `lenny_minio_replication_lag_seconds > minio.artifactBackup.replicationLagRpoSeconds` (1× RPO, default 900s). A new `MinIOArtifactReplicationLagCritical` (Critical) row fires at `lenny_minio_replication_lag_seconds > 4 * minio.artifactBackup.replicationLagRpoSeconds` (4× RPO, default 3600s) and is documented as a DR-posture emergency when the tier's published RPO envelope has been breached by a material factor. Removed the "Critical fires at 4× RPO" prose commitment from the Warning row and replaced it with a cross-reference to the new Critical row. Option (a) preferred over Option (b) because it is symmetric with `MinIOArtifactReplicationFailed` (its own row) and gives operators distinct alert names for alertmanager routing (distinct PagerDuty/Slack channels, distinct silencing, distinct alert-correlation keys) — whereas the `GatewayClockDrift`-style dual-severity pattern collapses into a single alert name. Synced the §16.5 metrics preview table (line 239) to reference both alerts with their thresholds, and updated the §25.11 Replication lag paragraph to enumerate both alerts separately with their 1×/4× thresholds. Synced `docs/reference/metrics.md` to list both alerts in the `Used by` column for `lenny_minio_replication_lag_seconds`. Verified no §25.13 bundled-rules smoke-test section or tier-defaults references the alert by name, and confirmed the `~40 rules` approximate count in §25.13 accommodates the added row.

### OBS-036. `lenny_experiment_isolation_rejections_total` has no alert rule despite being a steady-state-zero compliance signal [Medium] — **Fixed**

- **Sections:** spec/16_observability.md §16.1 line 152; spec/10_gateway-internals.md §10.7 line 852; spec/16_observability.md §16.5
- **Symptom:** §16.1 line 152 registers `lenny_experiment_isolation_rejections_total` as "incremented each time the `ExperimentRouter` fails closed because the variant pool's `isolationProfile` is weaker than the session's `minIsolationProfile`; paired with the `experiment.isolation_mismatch` event so operators can detect the rejection-population bias without log scraping". §10.7 line 852 reinforces the signal as the operator-visible witness to the fail-closed decision. Steady-state value is zero under correctly-configured experiments (the §10.7 admission-time monotonicity check at line 854 catches misconfiguration at experiment admission, pre-activation). §16.5 has no alert rule on this counter.
- **Why this matters:** The §10.7 text frames this counter as the primary operator signal when admission-path validation has been bypassed (e.g., when a pool is re-profiled downward after the experiment is active, or when the validation is disabled in `dryRun` flows). A steady-state-zero metric with no alert rule is a tripwire-without-trigger: operators only see it by actively polling or via ad-hoc dashboards. Compare `LLMUpstreamEgressAnomaly` (line 490, Critical) and `LLMTranslationSchemaDrift` (line 507, Warning) — both are the same "zero steady-state; any rate is a compromise/tripwire signal" pattern and both have alert rules.
- **Severity:** Medium — genuine gap in the metric → alert chain. Not High because an analogous admission-time check exists (§10.7 line 854 `422 CONFIGURATION_CONFLICT`) so the runtime rejections are bounded.
- **Fix options:** add a Warning-severity `ExperimentIsolationRejections` row to §16.5 with `rate(lenny_experiment_isolation_rejections_total[5m]) > 0` sustained for > 2 minutes (mirroring the `LLMTranslationSchemaDrift` expression shape), pointing to a runbook for re-validating variant pool profiles against active session minimum isolation.
- **Resolution:** Added a Warning-severity `ExperimentIsolationRejections` row to §16.5 immediately after `LLMTranslationSchemaDrift`, with expression `rate(lenny_experiment_isolation_rejections_total[5m]) > 0` sustained > 2 min. The row enumerates the bypass paths that can drive the counter off zero (variant pool re-profiled downward post-activation, `?dryRun=true` admission that did not re-run validation, or session-class `minIsolationProfile` tightened after enrollment) and points the operator action at re-validating every active experiment's variant pool `isolationProfile` against the strictest enrolled session `minIsolationProfile`, cross-referencing §10.7 ExperimentRouter isolation monotonicity check as the in-spec "runbook" anchor. Expanded the §16.1 line 154 metric entry to cross-reference the new alert (matching the pattern used by `lenny_memory_store_user_count_over_threshold_total` and `lenny_experiment_targeting_circuit_open`). Synced `docs/reference/metrics.md` — added `Drives ExperimentIsolationRejections (Warning)` to the metric row and added the new alert to the §Alert rules table near `LLMTranslationSchemaDrift`. Synced `docs/operator-guide/observability.md` by adding a row in the warning-alerts table immediately after `LLMTranslationSchemaDrift`. Regression check: no other section or runbook references an experiment-isolation alert by a different name; §10.7 admission-time text at line 854 is unchanged and remains consistent with the new runtime alert (admission check prevents the misconfig; alert catches admission-bypass paths).

### Convergence assessment (Perspective 12)

Counts (Perspective 12 iter5): Critical = 0, High = 2, Medium = 4, Low = 0, Info = 0.

Converged: **NO**.

Rationale. Two High findings (OBS-031, OBS-032) are direct regressions of the iter4 OBS-023 class (runbook triggers naming alerts that are not defined in §16.5); iter4 OBS-023 was a High severity item whose fix was expected to sweep every runbook and tier-default for such references. Medium findings OBS-033 and OBS-034 are two additional instances of the same "alert referenced, not defined" pattern in different sections (§9.4 and §12.8), downgraded to Medium only because they are not keyed to §17.7 runbooks. OBS-035 (Critical-threshold promised in prose but not in rule) and OBS-036 (zero-steady-state metric with no alert) are cross-reference chain completeness gaps in the §16.1 ↔ §16.5 ↔ runbook link that the perspective is tasked with verifying. Convergence requires §16.5 to be the single source of alert-name truth for §17.7 runbooks, §25.13 tier defaults, and every section that mentions an alert by name — until OBS-031 through OBS-034 are closed by either adding the missing rows or rewriting the dangling references to match existing rows, this perspective cannot converge.

Perspective 12 iter5: C=0 H=2 M=4 L=0 Info=0. Converged: NO.

---

## 13. Compliance, Governance & Data Sovereignty

(Iter5 findings below. IDs continue from iter4; highest iter4 ID is CMP-053.)

### CMP-054. Legal-hold escrow bucket is platform-singular — no per-region escrow pipeline, violating data residency at tenant force-delete [High]

**Section:** spec/12_storage-architecture.md §12.8 (Phase 3.5 sub-step 3 "Migrate to the legal-hold escrow bucket", line 882); spec/25_agent-operability.md §25.11 (tenant force-delete MCP tool row, line 4418)

The iter4 CMP-052 fix introduced a legal-hold escrow path for tenant force-delete under `--acknowledge-hold-override`. The migration step writes re-encrypted ciphertext to a **single, globally-configured** bucket via `storage.legalHoldEscrow.endpoint` / `.bucket` / `.kmsKeyId`. The escrow KEK itself is declared "single, long-lived, and platform-wide (not per-tenant)" and held in the `platform:legal_hold_escrow` KMS keyring. This is a data-residency contradiction for any deployment that has invested in the `backups.regions.<region>.*` and `storage.regions.<region>.*` per-region pipelines: when an EU-pinned tenant (`dataResidencyRegion: eu-west-1`) is force-deleted with held evidence, the escrow migration writes the held ciphertext — including PHI-tagged T4 artifacts, audit-range rows, and workspace snapshots that originated in EU jurisdiction — to an escrow bucket whose region is unspecified. Because the escrow bucket is configured as a single scalar (not a map keyed by region), any deployer operating EU and US tenants under one platform must choose one region for the escrow bucket, and the evidence for tenants in the other region ends up in the wrong jurisdiction. This is the same GDPR Art. 44/46 cross-border-transfer pattern that iter3 CMP-048 and iter4 CMP-053 closed for batch backups and continuous artifact replication — but for the escrow surface, the analogous fail-closed control is missing. Worse, the escrow KEK is also global: decrypting EU-escrowed evidence from a US-region platform escrow service account constitutes a cross-border decrypt capability even if the ciphertext is stored in the EU bucket.

A deployer running a force-delete against an EU tenant today therefore has no compliant escrow path: the force-delete path either (a) succeeds and silently writes EU personal data to a non-EU escrow bucket, or (b) fails at write time if the operator has geo-fenced the bucket — neither outcome has a fail-closed error analogous to `BACKUP_REGION_UNRESOLVABLE` or `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE`. The spec's line 937 "Multi-region reference architecture" claim that "each regional control plane operates its own `lenny-ops`" does not resolve this because the escrow KEK is scoped platform-wide, not regional-platform-wide.

**Recommendation:** Rewrite `storage.legalHoldEscrow.*` as a per-region map (`storage.regions.<region>.legalHoldEscrow.{endpoint, bucket, kmsKeyId, escrowKekId}`) mirroring `backups.regions.<region>.*`. Add a fail-closed startup check: every region that has at least one tenant with `dataResidencyRegion` set MUST declare a complete `legalHoldEscrow` entry, rejected at chart render / `lenny-ops` startup with `CONFIG_INVALID: storage.regions.<region>.legalHoldEscrow incomplete`. Add a runtime error code `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` (mirror of `BACKUP_REGION_UNRESOLVABLE`) returned when Phase 3.5 sub-step 3 cannot locate a regional escrow bucket for the tenant. Scope the escrow KEK to per-region (`platform:legal_hold_escrow:<region>`) so decrypt capability is regional. Add a `legal_hold.escrow_region_resolved` INFO audit event recording the destination region on each migration and a `LegalHoldEscrowResidencyViolation` critical alert paralleling `ArtifactReplicationResidencyViolation`. Update §25.11 to document the per-region keyring and the new error code in its error-codes table. The existing Phase 3.5 fail-closed gate (`TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD`) then protects the pre-migration state; the new gate protects the migration itself.

**Fix applied (iter5 resolution):** Migrated `storage.legalHoldEscrow.*` to the per-region map `storage.regions.<region>.legalHoldEscrow.{endpoint, bucket, kmsKeyId, escrowKekId}` with a single-region fallback `storage.legalHoldEscrowDefault.{…}` preserving behavior for deployments that never set `dataResidencyRegion`. §12.8 Phase 3.5 sub-step 2 now resolves the tenant's `dataResidencyRegion` (or the single-region default when unset) and consults the per-region map — unresolvable regions abort fail-closed with `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` (HTTP 422, `PERMANENT`), emit a `DataResidencyViolationAttempt` audit event with `operation: "legal_hold_escrow"`, and raise the new `LegalHoldEscrowResidencyViolation` critical alert. The escrow KEK is now region-scoped: `platform:legal_hold_escrow:<region>` for multi-region deployments and `platform:legal_hold_escrow:default` for the single-region fallback. §12.8 sub-step 3 writes re-encrypted ciphertext to the region-scoped bucket only; sub-step 4's `legal_hold.escrowed` ledger event carries `escrow_region` and the region-scoped `escrow_kek_id`. A new `legal_hold.escrow_region_resolved` INFO audit event records the residency decision (requested region, resolved region, escrow KEK id, resolution source) once per force-delete — paralleling `artifact.cross_region_replication_verified` (CMP-053). The preflight Job gained a "Legal-hold escrow per-region coverage" check at §17.2 that renders `CONFIG_INVALID: storage.regions.<region>.legalHoldEscrow incomplete` at install/upgrade time. §15.4 adds the `LEGAL_HOLD_ESCROW_REGION_UNRESOLVABLE` row; §16.1 adds `lenny_legal_hold_escrow_region_unresolvable_total` (labeled by `region`, `failure_mode`); §16.5 adds the `LegalHoldEscrowResidencyViolation` critical alert; §16.7/§16.8 list the new event and counter; §25.11 adds the error code and audit event to its error-codes and audit-events tables; §15.1 and §24.10 (`lenny-ctl admin tenants force-delete`) descriptions reference the region-scoped KEK and the new fail-closed error. `docs/reference/error-catalog.md`, `docs/reference/metrics.md`, and `docs/operator-guide/configuration.md` were synced: the error catalog row, the metrics counter and alert, and a new "Data Residency (multi-region only)" configuration section with the per-region `legalHoldEscrow` YAML shape and fail-closed validation rule were added. The pre-migration `TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD` gate remains intact (§12.8 line 872, 878, 889); the new gate covers the migration itself, closing the CMP-052 residency-violating-escrow path.

### CMP-057. `complianceProfile` downgrade has no ratchet — regulated tenant can be silently de-regulated via `PUT /v1/admin/tenants/{id}` [High] [Fixed]

**Section:** spec/11_policy-and-controls.md §11.7 "Compliance profile enforcement gate" (line 411); spec/15_external-api-surface.md §15.4 (`COMPLIANCE_*` error codes, lines 1040, 1059, 1067)

`complianceProfile` takes one of `none`, `soc2`, `fedramp`, `hipaa`. The enforcement gate prevents *creating or updating* a tenant to a regulated profile when SIEM/pgaudit aren't configured (returning `COMPLIANCE_SIEM_REQUIRED` / `COMPLIANCE_PGAUDIT_REQUIRED`), but the spec nowhere restricts the **reverse** transition — a tenant that currently has `complianceProfile: hipaa` can be updated to `complianceProfile: none` via the same `PUT /v1/admin/tenants/{id}` endpoint with no extra gate, no audit event beyond the generic `admin.tenant.updated`, and no operator attestation that legacy PHI-subject data has been separately handled. Compare with the iter3-hardened `workspaceTier` field (§12.9 line 1030): "inherited by all environments under the tenant unless explicitly overridden at the environment level to a **stricter** (never looser) tier." The same ratchet is missing for `complianceProfile`.

This is a genuine governance gap for three reasons:

1. **Runtime controls are lost.** Dropping to `complianceProfile: none` removes the SIEM hard-requirement (§11.7 line 411), lowers `audit.grantCheckInterval` from 60s to 300s (§11.7 line 339), permits `audit.pgaudit.enabled: false` (§11.7 line 358), and re-enables `cacheScope: tenant` on credential pools (§15.4 line 1067). A compromised admin who downgrades a tenant gains a window in which those controls no longer apply, while the previously-ingested PHI/FedRAMP data is still present in Postgres/MinIO/Redis.
2. **Audit retention floor drops.** The `audit.gdprRetentionDays` floor of 2190 days applies "when `complianceProfile` is any regulated value: `soc2`, `fedramp`, `hipaa`" (§17.8.1 line 848). Downgrading to `none` removes that floor — future audit retention config changes may then prune rows that the regulated profile would have retained.
3. **No audit event distinguishes this transition.** A HIPAA-to-none downgrade is audited the same as any field update; a SIEM operator would not be able to alert on the specific compliance regression without a bespoke field diff.

**Recommendation:** Enforce a one-way ratchet on `complianceProfile` of the form `none < soc2 < fedramp < hipaa`. Reject any `PUT /v1/admin/tenants/{id}` whose request body lowers the level with `COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED` (HTTP 422, `POLICY`). Provide a documented `POST /v1/admin/tenants/{id}/compliance-profile/decommission` endpoint (`platform-admin` only, with `acknowledgeDataRemediation: true` and a required `justification`, mirroring the tenant-force-delete pattern from CMP-052) for legitimate wind-down flows — the endpoint emits a critical `compliance.profile_decommissioned` audit event recording the previous profile, the operator identity, the justification, and the list of data-surface remediation steps the deployer has attested to (e.g., "BAA-held PHI has been separately erased or migrated"). Update §11.7 with the new gate, §15.4 with the new error code, and §16.7 with the new audit event. The fix is analogous to §12.9's `workspaceTier` stricter-only rule and should cite it for symmetry.

### CMP-058. Platform-tenant audit events have no documented regional residency — escrow ledger rows and impersonation events may land in the wrong jurisdiction [Medium] — **Fixed**

**Section:** spec/12_storage-architecture.md §12.8 (Phase 3.5 sub-step 4 "Record migration in the legal-hold ledger", line 883); spec/11_policy-and-controls.md §11.7 "Write-time tenant validation" (line 409); spec/25_agent-operability.md §25.11 (`PlatformPostgres()` StoreRouter mapping, line 1469)

Several compliance-critical audit events are documented as **platform-tenant** writes — that is, they are written under the platform tenant rather than the originating tenant because they legitimately cross tenant scopes: `security.audit_write_rejected` (§11.7 line 409), `admin.impersonation_*` (§11.7 line 409 + §13.3 reference), `legal_hold.escrowed` and `gdpr.legal_hold_overridden_tenant` (§12.8 Phase 3.5 sub-step 4, line 883). These rows carry the original tenant identifier as `unmapped.lenny.target_tenant_id` so forensic queries can locate them, but their physical residence is in `PlatformPostgres()` (§25.11 line 1469) — a single platform-scoped database instance.

In a multi-region deployment where one control plane runs per region (§12.8 line 937 "Multi-region reference architecture"), `PlatformPostgres()` is still a single logical instance within each region's control plane — the spec does not address whether platform-tenant events are duplicated across regional platform DBs, nor how an `admin.impersonation_*` event that spans tenants in two different regions (an unusual but possible scenario under a global platform-admin role) is routed. The result: an EU tenant's `legal_hold.escrowed` audit row — which carries the tenant's resource IDs and the escrow object key (itself potentially containing the tenant's name if the S3 key path includes it) — may be written to a US-regional platform Postgres if the platform-admin who triggered the force-delete was authenticated against the US control plane. The row is personal-data-adjacent (it references an identifiable tenant and its held resource IDs) and therefore subject to the same GDPR transfer rules as the underlying data.

The spec's §11.7 "Write-time tenant validation" does address the correctness of **which** tenant owns the write, but not **which region** the write lands in when the owner is the platform tenant. Unlike backups (CMP-053 `BACKUP_REGION_UNRESOLVABLE`) and replication (iter4 CMP-053 `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE`), there is no fail-closed residency check on platform-tenant audit writes.

**Recommendation:** Add an explicit "Platform-tenant audit event residency" paragraph to §11.7 Write-time tenant validation. Specify that when a platform-tenant audit event references a `target_tenant_id` with a set `dataResidencyRegion`, the event MUST be written to that target tenant's regional platform Postgres (i.e., `PlatformPostgres(region)` rather than `PlatformPostgres()`). Introduce the `PlatformPostgres(region)` method on `StoreRouter` and update §25.11 storage routing table to document it. When the target tenant has no residency constraint, fall back to the global platform Postgres. Reject writes with `PLATFORM_AUDIT_REGION_UNRESOLVABLE` when the target region has no platform Postgres (fail-closed, matching §12.8's `REGION_CONSTRAINT_UNRESOLVABLE`). Emit a `DataResidencyViolationAttempt` audit event with `operation: "platform_audit_write"` on any blocked write. This is the same pattern the spec already applies to `StorageRouter` and `BackupRegionUnresolvable` and should be extended symmetrically here. Scope: the fix is additive and does not disturb the single-region default (no residency constraint ⇒ same behavior as today).

### Convergence assessment (Perspective 13)

Iter5 identifies three genuine compliance gaps arising from the iter3/iter4 fixes to GDPR erasure and legal-hold plumbing:

- **CMP-054** (High): escrow bucket and escrow KEK have no per-region story; contradicts the per-region pipelines added in iter3 CMP-048 and iter4 CMP-053.
- **CMP-057** (High): `complianceProfile` has no downgrade ratchet while `workspaceTier` has one; silent HIPAA→none regression is admissible.
- **CMP-058** (Medium): platform-tenant audit events (including the CMP-052 legal-hold-escrow ledger rows) have no regional routing rule.

Candidate findings CMP-055 (FedRAMP granularity), CMP-056 (T4 erasure 30-day window across cross-region replication), and CMP-059 (Redis regional residency at T4 scale) were **dropped after critical review** as not genuine gaps:
- CMP-055: §11.7 line 413 already granulates FedRAMP Moderate (baseline), High (preset + deployer FIPS/SC-28 responsibility), and Low (subset of Moderate).
- CMP-056: the 30-day figure applies to T2 data; T3/T4 SLAs are 72h/1h and cross-region delegation / writes are already prohibited.
- CMP-059: §12.8 line 912 already fails closed with `REGION_CONSTRAINT_UNRESOLVABLE` on single-region Redis + multi-region residency.

Counts: **C=0 H=2 M=1 L=0 Info=0.**

**Converged: NO** — two High findings (CMP-054 escrow residency, CMP-057 profile-downgrade ratchet) represent genuine regulated-industry deployment blockers that the iter3/iter4 fixes introduced without closing their residency / direction-of-change analogues. The Medium (CMP-058) is an architectural completeness gap on the same residency theme. The existing iter3/iter4 residency-fail-closed pattern is well-established; applying it to these two surfaces is mechanical but must happen before convergence.

---

# Perspective 14 — API Design (iter5)

**Scope.** Re-review of the external API surface (`spec/15_external-api-surface.md`) and MCP consistency (`spec/09_mcp-integration.md`) against iter4 findings API-010 through API-016 and the `/v1/admin/*` endpoint catalogue touched in iter4 (HTTP status reclassifications, PUT/DELETE credential rows, `delivery_receipt` enum, `TREE_VISIBILITY_WEAKENING`).

**Calibration.** iter5 severities anchored to the iter4 rubric. No severity drift on carry-forwards (per `feedback_severity_calibration_iter5`). Fit-and-finish items remain Low.

## Inheritance of prior findings

| iter4 finding | iter5 disposition | Evidence |
| --- | --- | --- |
| API-010 `CREDENTIAL_SECRET_RBAC_MISSING` / `GIT_CLONE_AUTH_UNSUPPORTED_HOST` / `GIT_CLONE_AUTH_HOST_AMBIGUOUS` 400 → 422 [High] | **Fixed** | §15.4 lines 983 (`PERMANENT`/422), 1052, 1053 (`POLICY`/422). No stale `400 CREDENTIAL_SECRET_RBAC_MISSING` / `400 GIT_CLONE_AUTH_*` references remain in `spec/` (grep-verified). |
| API-011 `CONTENT_POLICY_WEAKENING` / `CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION` / `DELEGATION_POLICY_WEAKENING` 403 → 422 [Medium] | **Fixed** | §15.4 lines 1055–1057 all at `POLICY`/422 with inline "Aligned with the canonical §15.4 pattern …" pointers; `TREE_VISIBILITY_WEAKENING` (line 1058) carries the same pattern. |
| API-012 `DELEGATION_PARENT_REVOKED` 409 PERMANENT → 403 POLICY [Medium] | **Fixed** | §15.4 line 1027 now `POLICY`/403 with rationale naming `CREDENTIAL_REVOKED` / `LEASE_SPIFFE_MISMATCH` as the canonical peers; §8.2 inline reference unaffected (names code, not status). |
| API-013 REST/MCP contract-test matrix not updated [Medium] | **Fixed** | §15.2.1 `RegisterAdapterUnderTest` matrix (line 1384) now enumerates the session-creation rejection family (`VARIANT_ISOLATION_UNAVAILABLE`, `REGION_CONSTRAINT_UNRESOLVABLE`, `GIT_CLONE_AUTH_UNSUPPORTED_HOST`, `GIT_CLONE_AUTH_HOST_AMBIGUOUS`, `ENV_VAR_BLOCKLISTED`, `SDK_DEMOTION_NOT_SUPPORTED`, `POOL_DRAINING`, `CIRCUIT_BREAKER_OPEN`, `ERASURE_IN_PROGRESS`, `TENANT_SUSPENDED`) with an in-spec maintenance rule binding §15.4 additions to matrix updates. |
| API-014 catalog uniqueness invariant not stated [Low] | **Not fixed** (iter4 Skipped, no Resolution block). See API-017 (carry-forward, Low). |
| API-015 `UNREGISTERED_PART_TYPE` uses `WARNING` category outside canonical taxonomy [Low] | **Not fixed** (iter4 Skipped, no Resolution block). See API-018 (carry-forward, Low). |
| API-016 `RESTORE_ERASURE_RECONCILE_FAILED` HTTP 500 for known operator-action failure [Low] | **Not fixed** (iter4 Skipped, no Resolution block). See API-019 (carry-forward, Low). |

No new Critical/High/Medium API-level issues were introduced by iter4's reclassifications or by the credential-pool / endpoint additions. The shared error taxonomy (§15.2.1 item 3), the REST/MCP consistency contract, and the MCP wire-projection table (§15.2 "Per-kind MCP wire projection") continue to hold; the §15.4 catalogue remains single-source-of-truth with one `(code, http_status, category, retryable)` tuple per row for every code checked.

## New findings (iter5)

All three iter5 findings are carry-forward surfaces of iter4 Low items that did not land a fix. Per the severity-calibration rule, they stay at Low; they are enumerated here so iter5 tracking surfaces them rather than silently letting them lapse.

### API-017 Catalog uniqueness invariant still not stated at the §15.4 header [Low]

**Section:** `spec/15_external-api-surface.md` §15.4 error-code catalogue (header at line 967, table begins line 969).

Iter3 API-006 and iter4 API-014 both recommended a single sentence near the `**Error code catalog:**` header stating the invariant that each `code` appears at most once in the table and carries a single `(category, httpStatus, retryable)` tuple. The invariant is implicit across the iter3/iter4 consolidations (API-005, API-010, API-011, API-012 all depend on it) and is referenced only inside §15.2.1 rule 3 and the iter4 fix prose — never at the catalog header where future contributors read. Without an explicit statement, future regressions of the iter1/iter2 duplicate-row class (the original API-001 / API-005 problem) are again undefended. Convergence-wise this is Low because all currently-known duplicate rows are consolidated and the §15.2.1 RegisterAdapterUnderTest matrix would catch a behavioural drift; the gap is a documentation-hardening fit-and-finish carry-forward.

**Recommendation:** Add a single sentence immediately under the `**Error code catalog:**` heading (before line 969): "Each `code` appears at most once in this table and carries a single `(category, httpStatus, retryable)` tuple. Per-endpoint descriptions of the same code live in the endpoint table (§15.1) and in the referenced section, not here." This restates the invariant §15.2.1 item 3 assumes, and anchors future contributors at the point-of-edit.

---

### API-018 `UNREGISTERED_PART_TYPE` row uses `WARNING` category outside the canonical `TRANSIENT|PERMANENT|POLICY|UPSTREAM` taxonomy [Low]

**Section:** `spec/15_external-api-surface.md` §15.4 error-code catalogue line 1036 (`UNREGISTERED_PART_TYPE` row); canonical taxonomy stated at line 965 and §16.3.

The row reads `| UNREGISTERED_PART_TYPE | WARNING | — | …`. One line above (line 965), the header prose states the `category` field is "one of `TRANSIENT`, `PERMANENT`, `POLICY`, `UPSTREAM` as defined in [Section 16.3]". `WARNING` is not in that closed set. The `—` (absent) HTTP status confirms the row is not actually a wire error but a non-rejecting annotation emitted on the `OutputPart` (§15.4.1 line 1479 and line 1498 describe it as an `unregistered_platform_type` warning annotation, not a wire error). Placing an annotation signal inside the error-code catalogue with a non-canonical category violates the taxonomy the same table advertises and confuses third-party adapter authors writing a `RegisterAdapterUnderTest` matcher. The iter4 §15.2.1 contract test (`retryable` / `category` equivalence check, rule 5d) assumes all catalogue rows carry a canonical category; `WARNING` either forces a contract-test exception or silently passes through.

**Recommendation:** Remove the `UNREGISTERED_PART_TYPE` row from the §15.4 error-code table and re-document it exclusively in §15.4.1 as an `OutputPart` warning annotation (it already has a full treatment there under "Namespace convention for third-party types" at line 1498). If a cross-reference at the §15.4 catalogue level is desirable, add a one-line footnote under the table header: "The `unregistered_platform_type` annotation is emitted as an `OutputPart` warning, not a catalogue error — see §15.4.1." This keeps the catalogue's category taxonomy closed and removes the annotation/error confusion.

---

### API-019 `RESTORE_ERASURE_RECONCILE_FAILED` HTTP 500 PERMANENT for a known operator-action failure path [Low]

**Section:** `spec/25_agent-operability.md` §25.11 backup/restore error-code table line 4296.

The row catalogues `RESTORE_ERASURE_RECONCILE_FAILED | PERMANENT | 500` for the post-restore GDPR erasure reconciler failure (the restore lifecycle between `restore_completed` and the gateway restart). The description itself enumerates four known operator-action failure sub-causes: individual replay failure, Postgres unavailability mid-reconcile, enumeration error, and the legal-hold ledger freshness gate (`gdpr.backup_reconcile_blocked`, reason `legal_hold_ledger_stale`). HTTP 500 PERMANENT is reserved in §15.4 (`INTERNAL_ERROR`, line 1008) for unexpected server errors — not for a known operator-action failure path that the handler deliberately returns and that §25.11 expects the operator to resolve via `GET /v1/admin/restore/{id}/status` + `POST /v1/admin/restore/{id}/confirm-legal-hold-ledger`. The ledger-stale sub-reason in particular is a `POLICY` rejection in the iter4 §15.4 taxonomy sense (well-formed restore request, rejected by a server-state policy gate analogous to `ERASURE_BLOCKED_BY_LEGAL_HOLD` at 409). The 500 PERMANENT row conflates a deliberate policy rejection with a bug-class internal error, skewing `INTERNAL_ERROR` alerts and dashboards that treat any 500 as a gateway defect.

**Recommendation:** Either (a) split the row into two codes — a `RESTORE_ERASURE_RECONCILE_FAILED` (`TRANSIENT`/503) covering the transient reconciler sub-causes (Postgres unavailability, enumeration error) and a `RESTORE_ERASURE_BLOCKED_BY_LEGAL_HOLD_LEDGER_STALE` (`POLICY`/409) covering the ledger-freshness gate, mirroring the `ERASURE_BLOCKED_BY_LEGAL_HOLD` (`POLICY`/409) pattern in §15.4 — or (b) keep the single code but recategorise it to `POLICY`/409 and remove the "individual replay failure" sub-cause from its description (folded into a separate transient code). Option (a) is preferable because the two sub-cause classes have different operator remediation (retry vs. ledger confirm) and the iter4 §15.4 taxonomy already models this split elsewhere. Update the §25.11 error-codes table line 4296 and the `gdpr.backup_reconcile_blocked` cross-reference in §12.8 accordingly.

---

## Convergence assessment

- **API-010 / API-011 / API-012 / API-013 (iter4 High/Medium) are cleanly resolved.** No stale HTTP-status inline references remain; each consolidated code carries a single canonical tuple; the REST/MCP contract test matrix is in lockstep with §15.4.
- **No new Critical/High/Medium findings.** The full §15.4 catalogue, the §15.1 endpoint table (including the iter4-added PUT/DELETE credential rows and the `CREDENTIAL_PROBE_UNAVAILABLE` TRANSIENT/503 row), the §15.2 MCP tool surface, the §15.2.1 consistency contract, and the `delivery_receipt` / `message_expired` closed enums (§15.2) are internally consistent and taxonomically clean.
- **Three Low carry-forwards** (API-017, API-018, API-019) persist from iter4. Each is a documentation / classification hardening item with no runtime contract impact and no REST/MCP divergence risk under the iter4 test matrix. They do not block convergence; they are listed so iter5 tracking surfaces them explicitly and they can land as a batched editorial fix in a single follow-up commit to §15.4 and §25.11 if desired.

**Net severity tally (iter5 API perspective):** Critical 0, High 0, Medium 0, Low 3 (all carry-forwards). Convergence criterion met for API Design.

---

# Iter5 — Perspective 15: Competitive Positioning & Open Source Strategy

**Review Date:** 2026-04-20
**Reviewer:** Claude Opus 4.7
**Scope:** Market position, differentiation narrative, community adoption strategy, open-source governance, and upstream (`kubernetes-sigs/agent-sandbox`) risk framing. Primary sections examined: `spec/01_executive-summary.md`, `spec/02_goals-and-non-goals.md`, `spec/05_runtime-registry-and-pool-model.md` §5.1, `spec/19_resolved-decisions.md` item 14, `spec/21_planned-post-v1.md`, `spec/22_explicit-non-decisions.md` §22.6, `spec/23_competitive-landscape.md` (all), `spec/04_system-components.md` §4.6 upstream/abstraction/go-no-go paragraphs, and `docs/adr/index.md`.

**Severity calibration note.** Per `feedback_severity_calibration_iter5.md`, this perspective is usually Low/Info — strategic/narrative polish. Elevation to Medium+ is reserved for tangible technical impact (e.g., an upstream dependency abandoning direction breaks v1 feasibility). The iter1 CPS-001 (license status) precedent was calibrated Medium as a cross-doc factual inconsistency; the iter2 CPS-002 (differentiator numbering) was Low as a single-line cross-reference. The iter4 task did not scope this perspective; CPS-043 (sustainability) and CPS-048 (K8s adoption barrier) are carried-forward pre-existing business-strategy findings explicitly skipped in iter1 and iter2 per standing instructions and are NOT reopened here — both are pure business-model questions with no technical impact on v1 feasibility.

**Numbering:** iter4 had zero prior COM-NNN findings (Competitive Positioning was not scoped in iter4; prior iterations tagged findings as CPS-001 / CPS-002 under a now-reassigned code). Starting this perspective's fresh numbering at **COM-001** per the task convention "COM-NNN after iter4's last." No renumbering of iter1 CPS-001 or iter2 CPS-002 is performed; both are referenced below by their historical IDs for continuity.

---

## Status of prior competitive-positioning findings

**iter1 CPS-001 (license status inconsistency) — HOLDS FIXED.** §18 Phase 0, §19 item 14, §23 matrix row, and §23.2 all consistently describe MIT as resolved with a committed `LICENSE` file. I verified `/Users/joan/projects/lenny/LICENSE` is present (MIT header, "Copyright (c) 2026 Lenny Contributors"). No regression.

**iter2 CPS-002 (differentiator cross-reference off-by-two, [LOW]) — NOT FIXED. Re-raising as COM-001 below.** The original finding reported that `spec/22_explicit-non-decisions.md` line 13 referenced "differentiator 6" but §23.1 placed hooks-and-defaults at position 8. I re-read the current spec on 2026-04-20 and the file still reads `differentiator 6` on the same line. There is no record of the fix landing in iter3 (iter3 re-scoped CPS to Checkpoint & Partial Manifest and explicitly listed CPS-002 as "out of scope" in `iter3/CPS.md` line 6) or in iter4 (the iter4 task did not include Competitive Positioning among its 26 scoped perspectives). The iter2 summary listed CPS-002 in the Low-findings table (line 818) but no fix pass covered it afterward.

**Carried-forward pre-existing items.** CPS-043 (sustainability / commercial model) and CPS-048 (K8s adoption barrier) remain skipped per prior-iteration instructions and are out of scope for this perspective under the standing calibration rule.

---

## New findings

### COM-001 Hooks-and-defaults cross-reference still points to the wrong differentiator number [LOW]
**Section:** `spec/22_explicit-non-decisions.md` line 13; `spec/23_competitive-landscape.md` §23.1 ordered list (lines 72–86) and "Beyond the 8 architectural differentiators" (line 90)

§22.6 closes with `See [Section 23.1](23_competitive-landscape.md#231-why-lenny), differentiator 6 for the competitive positioning of this principle.` The referenced principle is hooks-and-defaults. In the current §23.1 ordered list, hooks-and-defaults is position **8** ("Ecosystem-composable via hooks-and-defaults"), and differentiator **6** is "Multi-protocol gateway". Line 90 explicitly confirms "Beyond the **8 architectural differentiators**", so the authoritative count is 8 and the position of hooks-and-defaults is 8.

This is the same finding as iter2 CPS-002; the fix never landed. Impact remains a minor documentation accuracy issue — a reader following the cross-reference from §22.6 (which describes this as a "governing architectural principle" and is exactly the kind of link an evaluator or new contributor is likely to click) lands on "Multi-protocol gateway" instead of the intended hooks-and-defaults entry.

**Recommendation:** Change `differentiator 6` to `differentiator 8` in `spec/22_explicit-non-decisions.md` line 13. Same single-line edit proposed in iter2 CPS-002.

---

### COM-002 ADR-008 is referenced as "recorded" but the ADR file has never been written [LOW]
**Section:** `spec/23_competitive-landscape.md` line 62 (feature matrix), line 137 ("Decision and rationale are recorded as ADR-008 in `docs/adr/`"); `spec/19_resolved-decisions.md` item 14 ("Decision recorded as ADR-008 in `docs/adr/`"); `spec/18_build-sequence.md` Phase 0 ("Decision recorded as ADR-008"); `docs/adr/index.md` "Platform" catalog table row for ADR-0008

All four spec locations use past tense ("recorded") for ADR-008. The ADR catalog in `docs/adr/index.md` correctly lists `ADR-0008 | Open-source license selection (MIT) | Planned`, consistent with the catalog note "`Planned` ADRs have reserved numbers but no file yet." The `docs/adr/` directory actually contains only `0000-use-madr-for-architecture-decisions.md`, `index.md`, and `template.md` — no `0008-*.md` file exists. The spec therefore asserts a decision artefact that has not yet been produced.

Competitive-positioning impact: external evaluators and contributors — especially enterprise legal reviewers reading §23.2 as part of license due diligence — will expect to find a populated ADR at the referenced location. Its absence weakens the governance-narrative credibility of the "governance model" section, which explicitly stakes the project's ADR discipline as a community-trust mechanism ("All architectural decisions tracked via ADRs in `docs/adr/`"). No technical impact on v1 feasibility.

**Recommendation:** Either (a) write `docs/adr/0008-open-source-license-selection-mit.md` now — the MADR body can be substantially lifted from §19 item 14's already-drafted rationale (candidates evaluated, decision drivers, consequences) — and flip the status row in `docs/adr/index.md` from `Planned` to `Accepted`; OR (b) soften the spec phrasing in §23 line 137, §19 item 14, and §18 Phase 0 from "recorded as ADR-008" to "reserved as ADR-008 (planned)" until the file exists, matching the `docs/adr/index.md` catalog's own language. Option (a) is the closing move for CPS-001 lineage ("license is resolved; the decision record exists"); option (b) is an honest interim. Either resolves the narrative-vs-artefact gap.

---

### COM-003 §21 Planned/Post-V1 omits recursive delegation expansion, weakening the "delegation as primitive" narrative [INFO]
**Section:** `spec/21_planned-post-v1.md` (all items 21.1–21.9); `spec/23_competitive-landscape.md` §23.1 differentiator 5 (recursive delegation)

§23.1 frames "Recursive delegation as a platform primitive" as one of the top three structural differentiators against Temporal, Modal, LangGraph, and the 2026 entrants (Scion, OpenShell, OpenSandbox) — explicitly the one called out in both matrices ("Platform-enforced recursive delegation" standout feature row) and the Scion/LangSmith narrative rows ("no per-hop budget/scope enforcement"). §21 Planned/Post-V1 items 21.1–21.9 cover A2A protocol variants, AP support, conversational patterns, environment UI, multi-cluster federation, UI/CLI, and SSH Git URLs — none extend the delegation primitive itself. The existing §22.5 "No Direct External Connector Access" is an explicit non-decision, not a planned extension.

This is a narrative gap, not a technical gap: the differentiator list implies an ongoing roadmap for the delegation primitive (e.g., cross-cluster delegation, delegation-aware rate-limit flow-control, observability across trees), but §21 is silent on all of it. Readers comparing §23.1 "platform primitive" language against the post-v1 roadmap may conclude the delegation surface is complete at v1 — which is a defensible position — or, worse, may infer the project has no continuing investment in the feature that §23.1 brands as a top differentiator.

No technical impact. No inconsistency per se — §21 states "Implementation is deferred" for the enumerated items and does not claim to be exhaustive. This is Info-severity, flagged only because three differentiators the marketing narrative leans on (recursive delegation, experiment primitives, eval hooks) have no post-v1 forward-looking mention in §21 at all. Compare to items like 21.5 (Environment Management UI) that DO have forward extensions.

**Recommendation:** Optional. If there are no planned delegation extensions, leave §21 as-is — the current silence is literally accurate and the §23.1 narrative stands on v1 features. If even a small roadmap exists (e.g., delegation-tree audit export, cross-cluster delegation under §21.7 multi-cluster federation), add a short sub-item under §21.7 or a new §21.10 "Delegation primitive extensions (deferred)" listing known future work. A second, lower-effort alternative: add a one-line note at the top of §21 explicitly stating that §21 covers *deferred* items with known shape and does NOT enumerate *planned future work on v1 differentiators* — this avoids setting the implicit expectation that §21 is the complete forward roadmap. This is purely presentational.

---

### COM-004 Feature Comparison Matrix "Cold-start" row uses a different measurement than its own latency-comparison caveat [LOW]
**Section:** `spec/23_competitive-landscape.md` line 39 (Feature Comparison Matrix "Cold-start" row); line 66 (Latency comparison note)

The Feature Comparison Matrix row "Cold-start" reports Lenny as **"P95 <2s runc, <5s gVisor (session-ready)"** and the competitors as **"~150ms (container boot)"**, **"Sub-90ms (container boot)"**, **"~300ms (checkpoint/restore)"**, etc. The explanatory note at line 66 correctly explains that these are NOT the same measurement — Lenny's number is full session-ready time, competitors' numbers are cold-start-only — and notes that the apples-to-apples pod-claim-and-routing step is "in the ~ms range". But the matrix row itself uses the label "Cold-start" for both, leading readers who skim matrices to conclude Lenny is 10–50× slower than the competition.

This is a presentation-quality issue in the single most cited comparison artefact in the §23 competitive narrative. It is NOT a factual error (line 66 corrects the interpretation), but the matrix row as it stands is precisely the kind of comparison-guide artefact `spec/23_competitive-landscape.md` §23.2 Phase 17 promises to publish — and in a published comparison guide, the row label and the explanatory footnote are almost certainly going to be read separately (e.g., matrix lifted into a slide deck, numbers cited in a blog post). The iter1 CPS-001 review and iter2 CPS-002 review both treated this matrix as a high-visibility external artefact, so the inconsistency between the row label and its own caveat is worth an iter5 fix.

**Recommendation:** Rename the matrix row **"Cold-start"** to **"Startup (see line 66 for what each figure measures)"** or split the row into two: **"Pod cold-start (ms-level)"** reporting Lenny's claim-and-routing sub-range and the competitors' published cold-start numbers on the same basis, and **"Session-ready (Lenny only; competitors do not publish)"** reporting Lenny's "P95 <2s runc, <5s gVisor" in isolation. Either variant aligns the row label with the caveat. The two-row variant is stronger because it permits a genuine apples-to-apples comparison of the mechanism competitors actually measure (container/microVM boot), while preserving Lenny's session-ready SLO claim without cross-contaminating it with competitor numbers.

---

## Areas checked and clean

- **Upstream risk (`kubernetes-sigs/agent-sandbox`).** §4.6 carries a strong abstraction-plus-gate design: `PodLifecycleManager` / `PoolManager` interfaces (§4.6 lines 333), AgentSandbox-backed default implementations (line 359), Phase-1-exit go/no-go assessment against API-stability / community-support / integration-test-pass-rate criteria (lines 485–491), a documented 2–3 engineering-week fallback to custom kubebuilder controllers (line 493), and quarterly dependency-review triggers. The "what if the project changes direction" scenario called out in the perspective examples is materially addressed with a replace-the-backend fallback path, not just a narrative mitigation. §23 table row 1 correctly frames upstream as infrastructure, not competition.
- **Temporal / Modal / LangGraph positioning.** §23 narrative rows and §23.1 differentiators 1, 5, 6 accurately scope the comparison: SDK coupling (Temporal), GPU-first focus (Modal), LangChain coupling (LangGraph/LangSmith); each gets a clear differentiator story on runtime-agnostic adapter contract + per-hop budget/scope + multi-protocol gateway. LangSmith's self-hosted-K8s availability is disclosed in line 10. No over-claiming.
- **2026 entrants (Scion, OpenShell, OpenSandbox).** The three-way matrix at §23 lines 47–64 and the narrative above it align on workload-agnostic framing and explicitly concede where competitors are stronger ("Better than Lenny for computer-use and browser-automation agents" for OpenSandbox; "Hot-reloadable YAML policy incl. inference routing" as OpenShell's standout). Honest competitive disclosure.
- **Community adoption funnel.** §23.2 three-persona table (runtime authors / platform operators / enterprise platform teams) aligns with entry points in §15.4 (adapter contract), §17.4 (local dev), §11/§4.8 (enterprise controls), and §24 (`lenny-ctl`). The TTHW < 5 min commitment ties back to Phase 2 deliverables (`lenny up` embedded mode + echo runtime) with an explicit CI smoke test requirement.
- **Hooks-and-defaults philosophy.** §22.6 and §23.1 item 8 agree on the scope (memory, caching, guardrails, evaluation, routing) and on the "platform layer, not ecosystem layer" posture. §22.2–22.4 reinforce by enumerating what Lenny declines to build. (Cross-reference number mismatch logged as COM-001; content-agreement is otherwise correct.)
- **Governance model.** BDfN → steering committee transition criteria (3+ regular contributors), ADR lifecycle described in `docs/adr/index.md`, Phase 2 `CONTRIBUTING.md` + Phase 17a `GOVERNANCE.md` deliverables (§23.2), Phase 17a early-development notice removal — all internally consistent. `CONTRIBUTING.md` and `GOVERNANCE.md` files are present at repo root.
- **MCP vs custom-gRPC positioning.** §01 Core Design Principles item 4 ("MCP for interaction, custom protocol for infrastructure") and §23.1 differentiator 6 (multi-protocol gateway) agree on the client-facing / internal-control split. MCP Tasks placement at the gateway's external interface is consistent with §9. No contradiction with the differentiation narrative.
- **License consistency.** MIT is now consistently described as resolved across §18, §19, §23, §23.2, and the repository LICENSE file. iter1 CPS-001 remains fixed.
- **Latency-comparison caveat.** §23 line 66 correctly disclaims cold-start vs. session-ready measurement differences and references the Phase 2 benchmark harness as the validation vehicle. (Matrix-row labelling logged as COM-004; the caveat itself is sound.)

---

## Convergence assessment

**New findings this iteration:** 3 Low (COM-001, COM-002, COM-004) + 1 Info (COM-003). **Regressions from iter4 items fixed:** none (iter4 did not scope this perspective). **Regressions from iter2 CPS-002:** one — the fix never landed; re-raised as COM-001.

This perspective is near convergence. Three of the four findings are single-edit corrections (one-line cross-reference fix, matrix-row rename, file creation or phrasing softening); the fourth (COM-003) is Info-severity and genuinely optional. None of the four describe technical risk to v1 feasibility, consistent with the severity-calibration rule for this perspective.

The central competitive-positioning narrative (`spec/23_competitive-landscape.md`) is in a mature state: 16 architectural-differentiator / platform-capability entries backed by spec cross-references, two comparison matrices covering the sandbox and orchestration clusters plus the 2026 Apache-2.0 entrants, a calibrated latency-comparison caveat, explicit trade-offs disclosed to evaluators, three-persona community funnel aligned with concrete entry points, a governance model with clear transition criteria, and an upstream-dependency fallback plan. No further structural changes are recommended — iter5+ maintenance on this perspective is documentation hygiene only, pending the CPS-043 sustainability and CPS-048 K8s-barrier business-strategy items that remain deliberately out of scope.

**Recommended iter6 scope for this perspective:** re-verify only if changes land in §23, §22.6, §21, §19 item 14, or `docs/adr/`; otherwise skip.

---

# Iter5 — Perspective 16: Warm Pool & Pod Lifecycle

**Scope:** `spec/06_warm-pod-model.md`, `spec/07_session-lifecycle.md`, `spec/04_system-components.md` §4.7/4.8/4.9 (incl. §4.6.1 / §4.6.3).

## Iter4 carry-over audit

Iter4 recorded exactly four warm-pool-lifecycle findings. All were reported **Fixed**. There were no Skipped or Deferred iter4 WPL findings; the single Deferred item in iter4's index (`SEC-009`) is delegation/file-export security, not warm-pool scope.

Verification of each iter4 Fixed item against the current spec:

| Iter4 finding | Status | Verification |
|---|---|---|
| **WPL-001** — Schedulability precondition missing on `scrub_warning` cleanup transition | **HELD** | `spec/06_warm-pod-model.md:153` now includes `host node is unschedulable → draining [scrub_warning]`; `spec/06_warm-pod-model.md:155` includes the schedulable counterpart for the `sdk_connecting [scrub_warning]` edge. The "Host-node schedulability precondition" paragraph (`spec/06_warm-pod-model.md:181`) explicitly states "the rule applies identically to the scrub-success and scrub-warning preConnect edges". |
| **WPL-002** — Gateway lacks Node RBAC + informer to evaluate schedulability | **HELD** | Evaluation was moved off the gateway entirely. `spec/04_system-components.md:481` ("Host-node schedulability labeling") makes the WarmPoolController the sole evaluator and surfaces the result as the pod label `lenny.dev/host-schedulable`; Gateway ServiceAccount RBAC (`spec/04_system-components.md:588`) grants only `get`/`patch` on `Pods` — no Node verbs — and §6.2 line 181 reads the label via that existing grant. `spec/04_system-components.md:586` adds `watch` on `Nodes` to the WPC ServiceAccount for the informer. |
| **WPL-003** — preStop cap-selection alert not computable from emitted metric labels | **HELD** | `spec/16_observability.md:41` emits `lenny_prestop_cap_selection_total` labeled by `pool`, `service_instance_id`, and `source ∈ {postgres, postgres_null, cache_hit, cache_miss_max_tier}`; the `PreStopCapFallbackRateHigh` rule at `spec/16_observability.md:438` groups `by (service_instance_id, pool)` to evaluate per-replica share. Producer side is wired at `spec/10_gateway-internals.md:114` (cap selection) and `spec/10_gateway-internals.md:165` (barrier-target source, separate counter). |
| **WPL-004** — Unschedulable-node branch missing from `task_cleanup` state diagram | **HELD** | `spec/06_warm-pod-model.md:152-155` enumerates all four preConnect re-warm edges: schedulable vs. unschedulable × scrub-success vs. scrub_warning, each cross-referring to the preCondition paragraph. |

## New findings

No new deployment-blocking or reliability-class bugs identified.

The following were considered and rejected as non-findings / out-of-scope / polish-class:

- **Label-staleness race at `task_cleanup` entry.** Gateway reads `lenny.dev/host-schedulable` at the moment of the `task_cleanup` decision and "does not re-check during SDK re-warm" (`spec/06_warm-pod-model.md:181`). A node that is cordoned in the gap between the WPC's last reconcile and the gateway's read could in principle be issued one more `sdk_connecting` transition. The spec bounds this window via WPC's "re-labels each affected pod within a single reconcile cycle (typically < 1 s per batch at Tier 3 concurrency)" (`spec/04_system-components.md:481`) and the absent-label fail-safe (`If the label reads "false" (or is absent, which is treated as unschedulable for fail-safe behavior)`). The worst-case consequence is a single extra SDK re-warm on a node whose eviction is imminent — the pod will then drain via the standard `idle → draining` eviction path. This is consistent with the severity calibration in iter3/iter4 (the original EXM-008 was scoped to the transition semantics, not the label-latency race). Not a new defect.
- **Gateway reads label via `get Pods` at the exact moment of transition, yet §4.6.3 gateway RBAC already lists `get`/`patch` on `Pods`.** No new RBAC gap. The spec text is self-consistent.
- **`lenny-label-immutability` exclusion for `lenny.dev/host-schedulable`.** Correctly carved out at `spec/04_system-components.md:481` ("explicitly omitted so that WPC can flip the value on every cordon/uncordon event"). WPC as writer + label immutability exclusion are both present.
- **Pre-attached failure retry policy (§6.2 line 296-303).** Invariants around not advancing `recovery_generation` and not surfacing warm-pool retries as client-visible transitions are preserved. No regression from iter4.
- **Circuit-breaker state persistence (§6.1 line 52-63).** PSC-owned status carve-out (`status.sdkWarmCircuitBreaker.*`) is explicitly enumerated in the CRD ownership table (`spec/04_system-components.md:571`) with `minOpenUntil` grace carried across PSC leader handoff. The ownership model is coherent.

## Convergence assessment

**Converged for warm-pool & pod-lifecycle scope.**

All four iter4 WPL findings are Fixed and the fixes hold in the current spec text, with cross-document consistency between `spec/06_warm-pod-model.md`, `spec/04_system-components.md` §4.6.*, `spec/10_gateway-internals.md` §10.1, and `spec/16_observability.md` §16.1/§16.5. No Skipped/Deferred iter4 items remain in this perspective. Iter5 surfaces no new correctness or reliability defects in warm-pool, pod state machine, preConnect re-warm, schedulability labeling, or preStop cap-selection semantics.

---

# Perspective 17: Credential Management — Iter5 Review

## Iter4 Fix Verification

- **CRD-013 (rotationTrigger enum consolidation)** — Fixed. §4.9 line 1410 defines the canonical seven-value enum (`proactive_renewal`, `fault_rate_limited`, `fault_auth_expired`, `fault_provider_unavailable`, `emergency_revocation`, `user_credential_rotated`, `user_credential_revoked`). The enum is referenced consistently from §4.7 line 822 (revocation-triggered ceiling), §4.9.2 line 1728 (`credential.rotation_ceiling_hit` field list), and §16.1 line 55 / §16.5 line 420 (metric and alert). No contradictions remain.
- **CRD-014 (ceiling-hit audit event)** — Fixed. §4.9.2 line 1728 adds `credential.rotation_ceiling_hit` carrying `tenant_id`, `session_id`, `lease_id`, `pool_id`, `credential_id`, `rotation_trigger`, `outstanding_inflight_count`, `elapsed_seconds`. §4.7 line 822 now documents the audit write at the same code point as the counter/alert and flags the event as SIEM-streamable tier-1 compromise signal.

## Findings

### CRD-015 Credential deny-list keying contract broken for user-scoped revocation [High]

**Section:** §4.9 lines 1348 (`POST /v1/credentials/{credential_ref}/revoke`), 1658 (Credential deny list)

§4.9 line 1658 normatively specifies the in-memory credential deny-list structure: "Each entry is keyed by `(poolId, credentialId)` and expires automatically when the last active lease against that credential reaches its natural TTL expiry. The `CredentialPoolStore` persists the `revoked` status durably so newly started gateway replicas rebuild their deny list on startup by querying for credentials in `revoked` state with active-or-recent leases."

§4.9 line 1348 then normatively specifies that user-initiated revocation "terminates [each active lease] immediately — proxy-mode leases via the credential deny list (same propagation as pool revocation)". User-scoped credentials, however, have no `poolId` — they are stored in `TokenStore` (not `CredentialPoolStore`) and are addressed by `credential_ref`. Three concrete correctness failures follow:

1. **No key available to insert.** The revocation handler has no `poolId` to populate in the `(poolId, credentialId)` tuple; any substitution (e.g., a sentinel `null` or the literal string `user`) is not specified and would not match the deny-list lookup path on the proxy request side, which is coded against pool credentials.
2. **LLM Proxy deny-list check does not consult `credential_ref`.** §4.9 line 1645 and line 1484 describe the proxy rejection path for denied credentials but only in terms of pool credentials. A revoked user credential with an in-flight proxy lease will not hit the deny-list short-circuit and the compromised key can continue being used for the remainder of its TTL — directly violating the guarantee in line 1348 that leases are "terminate[d] immediately" on user revocation.
3. **Startup rebuild path does not cover user credentials.** The rebuild query targets `CredentialPoolStore` entries in `revoked` state; `TokenStore` is never queried. A gateway replica restart immediately after a user revocation loses the deny-list entry and the revoked credential silently becomes accepted again on that replica.

This is a correctness gap in the primary security contract of user-initiated revocation (immediate invalidation). The severity is High — not Critical — because direct-delivery-mode user revocations still rotate via `RotateCredentials` RPC (line 1348) and reach the pod regardless of deny-list keying, and because the concrete exploitation requires the operator to have enabled user credentials with proxy delivery; but in proxy mode on a multi-tenant deployment (the recommended default per line 1482) this is the only stop between the user's revoke action and the compromised key reaching Anthropic/Bedrock/Vertex.

**Recommendation:** Extend the deny-list key to a tagged discriminated union: `{source: "pool", poolId, credentialId}` or `{source: "user", tenantId, credentialRef}`. Update §4.9 line 1658 to specify both key shapes, the matching rules the LLM Proxy uses on each inbound request, and the rebuild query: pool credentials from `CredentialPoolStore` WHERE status = 'revoked' UNION ALL user credentials from `TokenStore` WHERE status = 'revoked' AND EXISTS (active lease). Update §4.9 line 1348's "same propagation as pool revocation" phrase to explicitly reference the user-shaped deny-list entry. Add an integration test (`TestUserCredentialRevocationDenyListProxy`) that asserts an in-flight proxy request with a just-revoked user credential is rejected with `CREDENTIAL_REVOKED` before any upstream call.

**Status:** Fixed

**Resolution:** `spec/04_system-components.md` §4.9 was updated at four sites to close the keying contract gap. (1) Line ~1348 (`POST /v1/credentials/{credential_ref}/revoke` row) now names the user-shaped deny-list entry `{source: "user", tenantId, credentialRef}` explicitly and points forward to the "Credential deny list" block for the full tagged-union contract. (2) Line ~1484 (LLM Proxy lease-expiry/revocation paragraph) now describes the source-aware deny-list lookup for both pool-backed and user-backed leases. (3) Line ~1645 (Emergency Revocation step 4) now references the source-aware matching rules and names both key shapes. (4) The "Credential deny list" block at line ~1658 now defines the tagged discriminated union (`{source: "pool", poolId, credentialId}` vs `{source: "user", tenantId, credentialRef}`), the LLM Proxy matching rules (source-based dispatch; non-overlapping keyspaces), and the startup rebuild query as a UNION across `CredentialPoolStore` (revoked + active-or-recent lease) and `TokenStore` (revoked + active lease), and adds the `TestUserCredentialRevocationDenyListProxy` integration test name exercising both the pub/sub and startup-rebuild propagation paths. No §15 or §16 cross-reference edits were required; the `CREDENTIAL_REVOKED` error catalog entry (§15.1 line 1023) already generalizes to both credential sources.

### CRD-016 `credential.deleted` does not record whether the deleted `credential_ref` still had active leases [Low]

**Section:** §4.9 line 1349 (`DELETE /v1/credentials/{credential_ref}`), §4.9.2 line 1721 (`credential.deleted` event)

`DELETE /v1/credentials/{credential_ref}` explicitly states: "Active session leases are unaffected — they continue using the previously materialized credentials until they expire naturally." Those orphan leases continue rotating and failing against a `credential_ref` that no longer exists in `TokenStore`. The `credential.deleted` audit event (§4.9.2 line 1721) carries only `tenant_id`, `user_id`, `provider`, `credential_ref` — it does not record the `active_leases_at_deletion` count. Forensic questions that cannot be answered from audit alone:

1. "Did the user delete an unused credential, or did they delete a credential with N in-flight leases that kept draining the compromised key?"
2. "Did the user re-register a new credential for the same provider and expect prompt takeover, unaware that sessions already holding leases would keep using the stale key?"

Combined with §4.9 line 1353's "re-registering for the same provider replaces the previous one" (which is silent on whether the new record reuses the old `credential_ref` or mints a new one), the audit correlation chain between the deleted credential, its still-active leases, and the new registration is broken.

**Recommendation:** Add an `active_leases_at_deletion` (uint32) field to the `credential.deleted` event at §4.9.2 line 1721 alongside the existing fields. When non-zero, operators see at deletion time how many leases would continue to use the stale credential — matching the `active_leases_terminated`/`active_leases_rotated` fields already on `credential.user_revoked`/`credential.rotated`. Separately, state at §4.9 line 1353 whether `credential_ref` is stable across re-registration of the same provider (recommended: yes, to preserve audit correlation; or: no, emit a `credential.ref_rotated` linking event).

### CRD-017 CLI RBAC scope contradiction for credential-pool commands persists (iter4 CRD-017 carry-forward) [Low]

**Section:** §24.5 lines 85–93 vs §4.9 line 1102, §15.2 line 805

§4.9 line 1102 normatively states: "A `tenant-admin` can create, update, and delete credential pools for their own tenant via the admin API." §15.2 line 805 reaffirms the endpoint is "tenant-scoped; `tenant-admin` sees own tenant's pools". Every row in §24.5 lines 85–93 still lists `platform-admin` as the sole required role — `list`, `get`, `add-credential`, `update-credential`, `remove-credential`, `revoke-credential`, `revoke-pool`, `re-enable`. The admin-time RBAC live-probe motivation text at §4.9 line 1209 specifically invokes "a `tenant-admin` who lacks rights to patch the Token Service RBAC Role" as the threat being mitigated — a scenario that is impossible if the CLI is correct and tenant-admin cannot reach these paths at all.

This is unchanged from iter3 CRD-011 → iter4 CRD-017. It is a documentation inconsistency, not a correctness defect, hence Low per iter5 severity anchoring.

**Recommendation:** Change the "Min Role" column in §24.5 lines 86, 87, 88, 89, 90, and 93 from `platform-admin` to `platform-admin` or `tenant-admin` (scoped to own tenant). Keep `revoke-credential` (line 91) and `revoke-pool` (line 92) as `platform-admin`-only if emergency revocation is intentionally platform-only — and if so, add that restriction to §4.9 line 1102 and §15.2 line 810/812.

### CRD-018 Fault-driven rotation path still has no audit event (iter4 CRD-018 carry-forward, partial) [Low]

**Section:** §4.9.2 lines 1718–1732

iter4 CRD-018 asked for a `credential.rotated_fallback` (or equivalent) audit event on each fault-driven rotation (Fallback Flow step 4) so investigators can reconstruct "what caused this lease to rotate" without cross-joining against metric series whose high-cardinality label rule at §16.1.1 forbids `session_id` labels. The iter5 spec closes the **ceiling-hit subset** via `credential.rotation_ceiling_hit` (§4.9.2 line 1728) but not the common case: a fault-driven rotation that completes normally — the adapter drained the in-flight counter within 300s and sent `credentials_rotated` without tripping the ceiling — emits no audit event at all. The complete enumeration of rotation triggers that are forensically silent in v1:

- `fault_rate_limited` without ceiling hit
- `fault_auth_expired` without ceiling hit
- `fault_provider_unavailable` without ceiling hit
- `emergency_revocation` in direct-delivery mode without ceiling hit (operator-initiated, successfully drained)
- `user_credential_rotated` / `user_credential_revoked` via `RotateCredentials` RPC without ceiling hit

For all of these, the only surviving signal is the `lenny_credential_rotations` counter (labeled by `error_type` only per §16.1.1). The audit record lacks the `session_id` needed to answer "which session was rotated, why, to what replacement credential." The `credential.fallback_exhausted` event (line 1729) fires only at the terminal state — after `maxRotationsPerSession` is exhausted — so intermediate rotations remain unrecorded.

**Recommendation:** Add a `credential.rotation_completed` audit event at §4.9.2, emitted by the gateway at Fallback Flow step 5 (after the replacement `CredentialLease` is issued), with fields: `tenant_id`, `session_id`, `lease_id` (old), `new_lease_id`, `pool_id`, `old_credential_id`, `new_credential_id`, `rotation_trigger` (one of the non-`proactive_renewal` values), `error_type` (when trigger is fault-driven), `delivery_mode`, `rotation_count`. This makes `credential.rotation_ceiling_hit` a strict specialization (emitted in addition to `credential.rotation_completed` when the ceiling is hit), closes the forensic gap without duplicating the ceiling-hit event's existing fields, and preserves the existing audit budget (the event fires at most `maxRotationsPerSession` times per session).

### CRD-019 User-scoped credential rotation race with in-flight `credential_ref` lookups unspecified [Low]

**Section:** §4.9 line 1347 (`PUT /v1/credentials/{credential_ref}`), §4.9 lines 1357–1362 (Resolution at session creation)

`PUT /v1/credentials/{credential_ref}` states the Token Service "atomically replaces the encrypted material" and "active leases backed by this credential are immediately rotated via `RotateCredentials` RPC … so running sessions pick up the new material within one rotation cycle." The "one rotation cycle" phrase is undefined — §4.9's Fallback Flow is a 7-step state machine, not a single atomic step. Two race windows are observable:

1. **Concurrent session creation.** Between `TokenStore` row update (new encrypted material written) and the enumeration of active leases backed by the old material, a concurrent `POST /v1/sessions` can resolve the user credential (line 1359's per-provider lookup) and be handed the *new* material while leases scheduled for rotation are still holding the *old* material. The gateway has two parallel sessions — one with each material — for the rotation-cycle window.
2. **Concurrent delete-then-rotate.** Line 1349 specifies `DELETE` detaches leases; line 1347 specifies `PUT` rotates leases. There is no specified behavior when a `DELETE` and a `PUT` arrive concurrently on the same `credential_ref`; lease enumeration for rotation may execute against a record that is in the process of being deleted.

Neither race is a credential-leak by itself, but both result in an observable divergence from the one-writer contract a user reasonably expects from the rotate-and-propagate path (especially when the user's intent is to rotate *away* from a compromised key).

**Recommendation:** Normatively state at §4.9 line 1347 that rotation acquires a per-`credential_ref` advisory lock spanning material replacement, lease enumeration, and `RotateCredentials` RPC dispatch; concurrent `GET`/`POST`/`PUT`/`DELETE`/`revoke` on the same `credential_ref` block on the lock with a bounded wait before returning `409 CREDENTIAL_OPERATION_CONFLICT`. Document the per-`credential_ref` lock in the storage architecture section of §12 alongside the existing per-tenant audit advisory lock pattern.

## Convergence assessment

Iter5 is close to convergence on credential management, but **not yet converged** due to CRD-015 (High, new): the deny-list keying contract for user-scoped proxy-mode revocation is structurally incompatible with the iter4-codified pool-keyed deny list. This is a genuine correctness gap in the "immediate invalidation" guarantee on user revocation in the recommended multi-tenant proxy-mode configuration, not a documentation issue.

The remaining open items (CRD-016, CRD-017, CRD-018, CRD-019) are all Low and map to forensic-trail and documentation-consistency improvements that do not block correctness or security posture. Per the iter5 severity calibration feedback, these are anchored to their iter3/iter4 Low rubric equivalents; they have not escalated.

**Status:** Not converged — resolve CRD-015 before declaring.

---

# Perspective 18 — Content Model, Data Formats & Schema Design (Iter5)

Scope: `WorkspacePlan` (§14), `OutputPart` / `MessageEnvelope` (§15), `RuntimeDefinition` / `runtimeOptionsSchema` (§5.1). Iter5 revisits iter4 CNT-007…CNT-011 and audits the newly-adjusted §14 `sources[]` catalogue (CNT-008 iter4 fix, CNT-009 iter4 fix, CNT-011 iter4 fix) plus the published JSON Schema descriptor language introduced in §14.1.

## Iter4 carry-over verification

| Iter4 ID | Title (summary) | Iter5 status |
|---|---|---|
| CNT-007 [High] | `gitClone` SSH URL contract unresolved | **Fixed** — §14 line 93 now restricts `gitClone.url` to HTTPS and pins the JSON Schema `pattern` to `^https://`; SSH deferred to §21.9. The `GIT_CLONE_AUTH_UNSUPPORTED_HOST` / `_HOST_AMBIGUOUS` codes (§15.1) are scoped to HTTPS. |
| CNT-008 [Low] | §14 `gitClone.auth` paragraph lacks session-binding sentence | **Open** — carried forward as **CNT-012** (below). §14 line 95 still omits the session-id / `credential.leased` sentence; the equivalent sentence at §26.2 line 119 ("The lease is bound to the originating session ID for audit traceability.") is present but remains unpaired in the schema-of-record. |
| CNT-009 [Medium] | `uploadArchive.stripComponents` semantics undefined for `zip` | **Fixed** — §14 line 100 defines a format-independent `/`-split algorithm, applies it to `tar`, `tar.gz`, and `zip`, specifies skip behaviour for too-few-segments and empty-post-strip entries, and introduces the `workspace_plan_strip_components_skip` warning event. §7.4 line 459 cross-references back to this definition. |
| CNT-010 [Low] | `workspace_plan_unknown_source_type` / `workspace_plan_path_collision` not in §16.6 catalog | **Open** — carried forward as **CNT-013** (below). Neither event is enumerated in §16.6; iter4's fix introduced a third warning (`workspace_plan_strip_components_skip`) which also needs registration. |
| CNT-011 [Medium] | Published `WorkspacePlan` JSON Schema per-variant `additionalProperties` policy unspecified | **Fixed** — §14 line 332 "Per-variant field strictness" adds `additionalProperties: false` for every known `source.type` variant, describes the `oneOf` / `if`-`then` branching over the `type` discriminator, and explicitly resolves the mixed-shape case. |

## New findings

### CNT-012. §14 `gitClone.auth` paragraph still lacks session-binding sentence (iter4 CNT-008 unfixed) [Low]

**Section:** 14 (line 95 `gitClone.auth` paragraph), 26.2 (line 119), 4.9 (`credential.leased` audit event)

The `gitClone.auth` paragraph in §14 describes lease-scope resolution, host-pattern matching, and the HTTPS credential-helper flow but still omits the normative statement that the lease is session-scoped and traceable via the `credential.leased` audit event. §26.2 line 119 carries the paired sentence ("The lease is bound to the originating session ID for audit traceability.") but §14 is the schema-of-record for the `gitClone` source (CNT-002 iter2 established this), so the binding obligation belongs in §14 as well — auditors and client authors who consult §14 directly have no line of sight to §26.2. This is a documentation drift, not a functional gap (the gateway emits the audit event regardless of where the sentence lives), hence the Low severity, which matches iter4's CNT-008 rating under the severity-calibration rule for prior-iteration carry-overs.

**Recommendation:** Append one sentence to §14 line 95 after the credential-helper sentence: "The lease issued for a `gitClone` source is bound to the originating `session_id` and recorded in the `credential.leased` audit event ([§4.9](04_system-components.md#49-credential-leasing-service)) for traceability." This is verbatim the iter4 recommendation for CNT-008 and is sufficient to close the drift.

### CNT-013. §14 workspace-plan warning events absent from §16.6 Operational Events Catalog (iter4 CNT-010 unfixed, now extended) [Low]

**Section:** 14 (lines 100 `workspace_plan_strip_components_skip`, 330 `workspace_plan_unknown_source_type`, 334 `workspace_plan_path_collision`), 16.6 (Operational Events Catalog enumeration at lines 605, 615)

§14 defines three gateway-emitted warning events associated with `WorkspacePlan` materialization:

1. `workspace_plan_unknown_source_type` (§14 line 330) — per skipped source entry when `source.type` is unrecognized.
2. `workspace_plan_path_collision` (§14 line 334) — per detected last-writer-wins overwrite during materialization.
3. `workspace_plan_strip_components_skip` (§14 line 100) — per skipped archive entry when `stripComponents` exceeds the entry's segment count. Introduced by the iter4 CNT-009 fix.

None of the three appears in the §16.6 "Gateway-emitted" enumeration (line 605) or any adjacent catalog section. §16.6 line 603 declares itself "the canonical enumeration" of operational events, and other §14-originated events (e.g., `session_completed` / `session_failed`) are listed, so the absence is real — consumers that filter the SSE stream, Redis stream, or in-memory buffer ([§25.5](25_agent-operability.md#255-operational-event-stream)) against the catalog will drop these three as unknown. Iter4 rated the two-event version of this finding Low; adding the third event (CNT-009-induced) does not raise severity because the impact — silent filtering by catalog-driven consumers — is identical.

**Recommendation:** Add a new bullet or dedicated paragraph to §16.6 after the experiment-events paragraph (line 613) enumerating the three workspace-plan warning events and their payload field sets, e.g.:

> **Workspace plan events (gateway-emitted, operational).** The gateway emits the following warning events during `WorkspacePlan` validation and materialization ([§14](14_workspace-plan-schema.md)). All are CloudEvents with `type: dev.lenny.<short_name>` and flow through the same Redis stream / in-memory buffer as the gateway-emitted events above.
>
> - `workspace_plan_unknown_source_type` (warning) — emitted when a consumer encounters an unknown `source.type` and skips the source entry per the open-string extensibility contract. Payload fields: `tenant_id`, `session_id`, `schemaVersion`, `unknownType`, `sourceIndex`.
> - `workspace_plan_path_collision` (warning) — emitted when two or more `sources` entries resolve to the same workspace path during materialization and the later entry wins under the last-writer-wins rule. Payload fields: `tenant_id`, `session_id`, `path`, `winningSourceIndex`, `losingSourceIndex`.
> - `workspace_plan_strip_components_skip` (warning) — emitted per skipped archive entry when `uploadArchive.stripComponents` exceeds the entry's segment count or the post-strip path is empty. Payload fields: `tenant_id`, `session_id`, `sourceIndex`, `entryPath`, `segmentCount`, `stripComponents`.

Also update the line-605 inline list to include these three `short_name`s so the "Gateway-emitted:" one-liner remains exhaustive.

### CNT-014. `inlineFile.mode` / `uploadFile.mode` / `mkdir.mode` string format has no regex, range, or setuid/setgid/sticky-bit constraint [Medium]

**Section:** 14 (lines 87 `inlineFile.mode`, 88 `uploadFile.mode`, 90 `mkdir.mode`, 14.1 lines 309, 332 Published JSON Schema / `additionalProperties: false`)

The three `mode` optional fields are described uniformly as "octal string, default `0644`" (or `0755` for `mkdir`) but the published schema description in §14.1 does not specify:

1. **Regex / pattern.** Clients could submit `"644"` (no leading zero), `"0o644"` (Go/Python prefix), `"0x1A4"` (hex), `"rw-r--r--"` (symbolic), or even arbitrary non-numeric strings. Without an explicit `pattern` the JSON Schema validator will accept any string and the gateway implementation becomes the de facto spec — different gateway versions may parse differently.
2. **Numeric range / allowed bit-set.** No constraint prevents values such as `"06777"` (setuid+setgid+sticky+777) or `"04755"` (setuid+755). Setuid/setgid/sticky bits on a file materialized inside `/workspace/current/` are not inherently unsafe (the pod's effective UID is the sandbox user), but the absence of a documented bit-mask means a future defense-in-depth hardening (e.g., reject setuid/setgid) would be a breaking change. The spec should pin the allowed range explicitly now while the schema is still at v1.
3. **Symbolic-vs-octal canonicalization.** `0644` and `"644"` are both unambiguous as octal, but the spec does not say the validator accepts both or only the 4-digit form shown in the examples. CNT-009 (iter4) fixed the equivalent ambiguity for `stripComponents` by defining a canonical algorithm independent of format; the same clarity is missing here.

This is a Medium-severity gap because (a) the schema is published and third-party clients will encode against it (wire-format contract), (b) `mode` semantics on a Unix filesystem are security-relevant even inside a sandbox (setuid bits propagate through archive extraction and can interact with user-namespace mappings on hosts that remap UIDs), and (c) the fix is schema-only — no code change is required, matching the "SHOULD fix, has workaround" Medium bar.

**Recommendation:** In §14 line 100 area (within the "Field notes:" block) add a `mode` normalization note and amend the published JSON Schema to enforce it. Proposed language:

> - **`mode` (all variants — `inlineFile`, `uploadFile`, `mkdir`).** Octal string representing Unix file permissions. The JSON Schema constrains `mode` with `"type": "string", "pattern": "^0[0-7]{3,4}$"`: three or four octal digits, leading zero required. The four-digit form encodes setuid (`04xxx`), setgid (`02xxx`), and sticky (`01xxx`) bits; the three-digit form is equivalent to leading `0` on the high bits (no special bits set). V1 rejects `mode` values containing setuid or setgid bits (`04xxx` or `02xxx`) with `400 WORKSPACE_PLAN_INVALID` (`details.field = "sources[<n>].mode"`, `details.reason = "setuid_setgid_prohibited"`); sticky (`01xxx`) is permitted on `mkdir` but rejected on `inlineFile` / `uploadFile` (sticky on a regular file is a legacy Linux feature with no defined semantics on modern kernels). Non-matching strings (`"644"`, `"rw-r--r--"`, `"0o644"`, etc.) are rejected under the same error code with `details.reason = "invalid_mode_format"`. The gateway parses the validated string with `strconv.ParseUint(mode, 8, 32)` and applies it via `os.Chmod` after file write.

The `additionalProperties: false` already present for each variant (CNT-011 iter4 fix) means this amendment is isolated to the `mode` keyword's schema object and does not affect the `oneOf` / `if`-`then` branching.

**Status:** Fixed

**Resolution:** Added a new `mode` bullet to §14 "Field notes:" (inserted between `uploadArchive.stripComponents` and `env`) that pins the JSON Schema constraint as `{"type": "string", "pattern": "^0[0-7]{3,4}$"}` (three or four octal digits, leading zero required) and specifies three distinct `details.reason` values for `400 WORKSPACE_PLAN_INVALID`: `invalid_mode_format` (non-matching strings like `"644"`, `"rw-r--r--"`, `"0o644"`, `"0x1A4"`), `setuid_setgid_prohibited` (leading octal digit with setuid `04xxx` or setgid `02xxx` bit set on any variant), and `sticky_on_file_prohibited` (sticky `01xxx` bit on `inlineFile` / `uploadFile` — permitted on `mkdir`). The note documents the gateway parse path (`strconv.ParseUint(mode, 8, 32)` → `os.Chmod`) and the per-variant defaults (`0644` for files, `0755` for mkdir). The `sources[]` catalogue table rows for `inlineFile`, `uploadFile`, and `mkdir` were annotated with "see `mode` field notes below for the `^0[0-7]{3,4}$` pattern and bit-mask constraints" so the table references the normative bullet. Taking the full Alt A per Step 5 (including the setuid/setgid/sticky rejection) was justified by greenfield v1 status and the security-relevance of mode bits — locking the contract down now avoids a breaking change later.

### CNT-015. `gitClone.ref` reproducibility semantics undefined — branch-name refs drift between session creation and resume/retry [Medium]

**Section:** 14 (line 91 `gitClone` row `ref` field), 14.1 (line 322 Gateway reconciliation / resumed or retried sessions), 7.2 (session resume semantics)

The `gitClone` `ref` field is described as "branch, tag, or commit SHA" (line 91) with no guidance on reproducibility. Because §14.1 line 322 requires the gateway to "read back the stored `WorkspacePlan` when replaying workspace setup for resumed or retried sessions", a plan that specifies a mutable ref (a branch name like `main`, or even a floating tag) can materialize different repository contents at:

1. Session creation (first materialization).
2. Any retry within the `maxResumeWindowSeconds` window ([§14](14_workspace-plan-schema.md) `retryPolicy.maxResumeWindowSeconds`).
3. Any resumed session after gateway eviction / checkpoint restore.

This violates the implicit expectation — reinforced by §14.1 "Gateway reconciliation (live consumer)" — that replay produces the same workspace the first attempt saw. A client debugging a session failure by resuming it may see a different `main` than the one that triggered the failure; a retry of a failed build may succeed against a newer commit that wasn't in the original failure scope; a deterministic-retry contract claimed by `retryPolicy.mode: auto_then_client` is silently violated for any plan that specifies a branch. The contract asymmetry with `uploadArchive` / `uploadFile` / `inlineFile` — all of which reference immutable uploaded content via `uploadRef` or embedded `content` — is stark.

This is Medium severity because: (a) the behaviour is surprising and undocumented; (b) the fix is partly a documentation update and partly a gateway resolution step that SHOULD be specified now while the schema is at v1; and (c) the contract distortion affects audit replayability and billing-event-driven rebuilds ([§25.9](25_agent-operability.md#259-audit-log-query-api)).

**Recommendation:** Append a paragraph to §14 after line 95 (or within the "Field notes:" block) specifying ref resolution semantics:

> - **`gitClone.ref` resolution.** At session creation, the gateway resolves `ref` to an immutable commit SHA by performing a `git ls-remote` against the target repository (using the same credential-lease as the clone itself). The resolved SHA is persisted alongside the stored `WorkspacePlan` as `sources[<n>].resolvedCommitSha`. All subsequent materializations for the same session (retries, resumes, checkpoint restores) clone `resolvedCommitSha` rather than re-resolving `ref`, so a moving branch or tag does not change the workspace contents across the session's lifetime. `resolvedCommitSha` is a read-only, gateway-written field — clients MAY observe it in `GET /v1/sessions/{id}` for audit purposes but MUST NOT set it at session creation. When `ref` is already a 40-character hexadecimal string, the gateway treats it as a commit SHA and skips the `ls-remote` step. New sessions that share a `WorkspacePlan` template (recursive delegation children, session-from-session retries outside the original session's `maxResumeWindowSeconds`) re-resolve `ref` and MAY see a different `resolvedCommitSha` than the parent — the guarantee is per-session, not per-plan.

Also add a corresponding sentence to §14.1 line 322 referring to `resolvedCommitSha` so the reconciliation loop's contract is self-contained.

**Status:** Fixed

**Resolution:** Added a new "Field notes" bullet to §14 specifying `gitClone.ref` resolution semantics: at session creation, the gateway resolves each `gitClone.ref` via `git ls-remote` to an immutable commit SHA persisted as `sources[<n>].resolvedCommitSha`; subsequent materializations within the same session's lifetime (retries, resumes, checkpoint restores) clone the pinned SHA rather than re-resolving the ref. Clients MUST NOT set `resolvedCommitSha` at creation (rejected with `400 WORKSPACE_PLAN_INVALID`, `details.reason = "gateway_written_field"`) and MAY observe it via `GET /v1/sessions/{id}`. When `ref` already matches `^[0-9a-f]{40}$`, the gateway skips `ls-remote`. Independent sessions sharing a `WorkspacePlan` (delegation children, retries outside `maxResumeWindowSeconds`, plan-body reuse) re-resolve `ref` — the guarantee is per-session, not per-plan. `ls-remote` failures at session creation produce `422 GIT_CLONE_REF_UNRESOLVABLE`. Added a corresponding sentence to §14.1 Gateway reconciliation bullet stating that replays clone `resolvedCommitSha` rather than re-resolving `ref`. Surfaced the observable field in §15.1 `GET /v1/sessions/{id}` endpoint description row. Registered the new `GIT_CLONE_REF_UNRESOLVABLE` code in the §15 error catalog.

### CNT-016. Published `WorkspacePlan` JSON Schema lacks `minimum: 1` on `schemaVersion` [Low]

**Section:** 14.1 (line 316 `schemaVersion` field; line 309 Published JSON Schema)

`schemaVersion` is described as an "integer" field identifying the schema revision. The spec normatively defines producer/consumer obligations for higher-than-known versions and for durable-consumer forward-read, but the published JSON Schema description does not pin a lower bound. A plan submitted with `"schemaVersion": 0` or `"schemaVersion": -1` is not covered by any of §14.1's normative clauses (the "higher than I understand" rule assumes positive integers). The gateway's implementation will presumably reject zero/negative values, but the schema-of-record should document the constraint so third-party validators reject on parse and the audit/analytics forward-read rule doesn't encounter undefined-behaviour values.

This is Low severity — no documented adversarial use, easy fix, no impact on existing v1 behaviour.

**Recommendation:** In §14.1 line 316 (or the Published JSON Schema block at line 309), add: "The published schema constrains `schemaVersion` with `{"type": "integer", "minimum": 1}` — values less than 1 are rejected at session creation with `400 WORKSPACE_PLAN_INVALID`."

## Convergence assessment

- **Two iter4 items remain open** (CNT-012, CNT-013) both Low severity, both documentation-only fixes within §14 and §16.6. Neither blocks convergence on a structural basis.
- **Three new iter5 items** (CNT-014 Medium `mode` format, CNT-015 Medium `gitClone.ref` reproducibility, CNT-016 Low `schemaVersion` minimum) are schema-surface polish items that the iter4 CNT-011 fix (per-variant strictness declaration) naturally surfaces by putting the published JSON Schema under review — each is bounded, isolated to §14 / §14.1, and does not imply a structural redesign.
- **No Critical or High findings this iteration.** The HIGH-severity iter4 item (CNT-007 SSH URL) is fully fixed, and iter5 did not uncover a new HIGH. The schema-of-record for `WorkspacePlan` is internally consistent and the iter4 fixes hold.
- **Recommendation:** Perspective 18 is on track to converge at iter6 if CNT-012 and CNT-013 (carry-overs), plus CNT-014 and CNT-015 (Medium), are addressed. CNT-016 can be deferred to a post-convergence polish pass without risk. No cross-perspective dependencies were surfaced — all findings are resolvable within §14, §14.1, and §16.6 alone.

---

# Perspective 19: Build Sequence & Implementation Risk — Iter5

**Scope:** `spec/18_build-sequence.md` only (with cross-references to §17 preflight / chart inventory for dependency verification).

**Iter4 carryover verification:**

- **BLD-009 (High, Phase 8 deploys `lenny-drain-readiness`).** Fixed in iter4. `spec/18_build-sequence.md` Phase 8 (line 48) now contains the explicit webhook first-deploy clause with `features.drainReadiness=true` flip, HA contract (`replicas: 2`, `podDisruptionBudget.minAvailable: 1`, `failurePolicy: Fail`), `lenny.dev/component: admission-webhook` pod label, and `DrainReadinessWebhookUnavailable` alert wiring. §17.9 row 497 skips the check when `features.drainReadiness=false`. No regression.
- **BLD-011 (High, phase-aware preflight enumeration).** Fixed in iter4 via Option A (feature-flag chart inventory). `spec/17_deployment-topology.md` §17.2 lines 59–71 define the three feature flags (`features.llmProxy`, `features.drainReadiness`, `features.compliance`) with their Phase-5.8/8/13 first-deploy assignments, the preflight expected-set composition rule, and the four-case parameterised `admission_webhook_inventory_test.go` table. `spec/18_build-sequence.md` Phase 3.5 (line 14) adds the "Phase-aware preflight and chart inventory" paragraph, and Phases 5.8/8/13 each call out the feature-flag flip. The chain is internally consistent. No regression.
- **BLD-010 (Low, Phase 1 Shared Adapter Types / SessionEventKind registry).** NOT fixed in iter4. `spec/18_build-sequence.md` line 8's Phase 1 wire-contract list still contains only `schemas/lenny-adapter.proto`, `schemas/lenny-adapter-jsonl.schema.json`, `schemas/outputpart.schema.json`, `schemas/workspaceplan-v1.json`; no `pkg/adapter/shared.go` or `schemas/session-event-v1.json`. Re-raised below as **BLD-013** with iter4's severity anchoring preserved (Low).

Iter4 introduced §18.1 "Build Artifacts Introduced by Section 25" as an index-only appendix listing `lenny-ops`, `lenny-backup`, the shared `pkg/alerting/rules` / `pkg/recommendations/rules` packages, and the `pkg/common/registry/resolver.go` `ImageResolver`. Only a "Pre-GA ordering" note ties these to Phase 17a/Phase 14; **no Phase 0–17 row names them as deliverables**. Cross-referencing §17.8.5 (mandatory `lenny-ops` Deployment, chart validation rejects disabling) and §17.9 row 501 (preflight `lenny-ops-sa` RBAC) produces the Phase-3.5 infeasibility captured in **BLD-012** below.

---

### BLD-012. `lenny-ops` has no phase assignment yet is mandatory from the first chart install (Phase 3.5) [High]

**Section:** spec/18_build-sequence.md §18.1 lines 79–92 (Build Artifacts from Section 25); spec/17_deployment-topology.md §17.8.5 line 1293 ("mandatory in every Lenny installation"); §17.2 lines 15–19 (`lenny-ops` Deployment, `lenny-gateway-pods` headless Service, `lenny-backup` Jobs, NetworkPolicies, Lease); §17.9 row 501 (`lenny-ops-sa` RBAC preflight check); spec/25_agent-operability.md §25.4 line 780 ("Every Lenny installation includes a `lenny-ops` deployment regardless of tier — there is no supported topology without it").

Iter4 added §18.1 as an index of "build-pipeline requirements … layered into the existing phases above." The index names two new container images (`lenny-ops`, `lenny-backup`) and three shared Go packages (`pkg/alerting/rules`, `pkg/recommendations/rules`, `pkg/common/registry/resolver.go` `ImageResolver`), but the only phase-ordering language is the Pre-GA ordering note at line 92:

> "The new images … and the `pkg/common/registry/resolver.go` `ImageResolver` are prerequisites for the Phase 17a community-launch documentation pass — operability must be complete before external deployers are invited."

This treats `lenny-ops` and `lenny-backup` as Phase-17a prerequisites only. But §17.8.5 and §25.4 state unequivocally that `lenny-ops` is mandatory in every install regardless of tier, that "Attempts to disable `lenny-ops` via Helm values are rejected at chart validation," and that §17.2's canonical component layout (lines 15–19) always renders the `lenny-ops` Deployment, the `lenny-backup-sa` ServiceAccount, the `lenny-ops-leader` Lease, the headless `lenny-gateway-pods` Service, and the four NetworkPolicies (`lenny-ops-deny-all-ingress`, `lenny-ops-allow-ingress-from-ingress-controller`, `lenny-ops-egress`, `lenny-backup-job`). The `lenny-preflight` Job's row 501 (`lenny-ops-sa` RBAC) is unconditional — there is no `features.ops`-style flag gating it, unlike the three webhook flags documented in §17.2's Feature-gated chart inventory paragraph.

Concrete consequences, strictly reading the current build sequence:

1. Phase 3.5 is the first phase where a Helm chart is installed (admission policies, NetworkPolicies, ResourceQuota, LimitRange, `lenny-preflight`, `lenny-bootstrap`). With the current §18.1 wording, the Phase 3.5 chart will not have a `lenny-ops` image built, yet §17.8.5's chart-validation rejection and §17.9 row 501's `lenny-ops-sa` RBAC check will both fire. The `lenny-preflight` Job itself runs as a `helm.sh/hook: pre-install` Job that *already* expects `lenny-ops-sa` to be present — so the install fails-closed on the first attempted deployment.
2. Phases 2 (`make run` local dev) and 2.5 (structured logging) notionally could precede `lenny-ops`, but Phase 3.5 is the cliff edge: every phase from 3.5 onward deploys via Helm and therefore requires `lenny-ops` to be either (a) built and shipping as a baseline component or (b) gated behind a feature flag parallel to the three admission-webhook flags, with §17.8.5's "mandatory" statement relaxed to "mandatory from Phase X onward."
3. Phase 4.5 (bootstrap seed mechanism, admin API) is where `lenny-ops`'s admin-API fan-out dependency first becomes load-bearing — `lenny-ops` reads from the gateway admin API per §25.3 — so a strict minimum Phase-X for `lenny-ops` is Phase 4.5, not Phase 17a. But the Helm-chart-validation contract in §17.8.5 is the stronger constraint because it fires at Phase 3.5 chart install time.
4. `lenny-backup` is transient (Jobs created on-demand) and its prerequisite is looser, but §25.11's backup-and-restore API is a documented operability deliverable that §13's operational-readiness milestone implicitly depends on. Without a phase assignment, Phase 13 ("Full observability stack … operational readiness") reads as complete while a core operability binary is still unbuilt.
5. The shared `pkg/alerting/rules` package is load-bearing from Phase 16.5 and earlier: §16.5's alert catalog is consumed by the Helm chart's `PrometheusRule` template (§17.2 line 524, §17.9 row 674) and by the in-process gateway evaluator, so the package must exist before any phase that ships a deployer-visible alerting rule — which is Phase 13 at the latest ("alerting rules, SLO monitoring"), and Phase 3.5 earlier if the `AdmissionWebhookUnavailable`/`SandboxClaimGuardUnavailable` alerts are rendered at that phase.
6. The `pkg/recommendations/rules` shared package is consumed by both the gateway's `/v1/admin/ops/recommendations` endpoint and `lenny-ops`'s aggregated view (§25.3 line 602). It has the same earliest-phase-required argument as `pkg/alerting/rules`: it must exist before any phase that ships the recommendations endpoint in the gateway, which is implicit in Phase 4.5's admin-API foundation.
7. The `ImageResolver` in `pkg/common/registry/resolver.go` is consumed by every Lenny Deployment's image reference per §17.8.6 line 1304 ("The chart's `ImageResolver` shared package … composes every image reference from `platform.registry.*`, ensuring the gateway, `lenny-ops`, controllers, `lenny-backup`, and the warm-pool controller all honor the same registry configuration"). Phase 3.5's chart slice renders at minimum the gateway, controller, and admission-webhook Deployments, all of which must resolve their image references through `ImageResolver`. So `ImageResolver` is a Phase-3 prerequisite at the latest (the chart first ships digest-pinned controller-created pod images per Phase 3's note), and arguably a Phase-1-or-2 prerequisite because Phase 2's `make run` embedded-component binary and Phase 3's digest-pinned images both need deterministic resolution.

The "Pre-GA ordering" note at §18.1 line 92 addresses only the soft constraint ("operability must be complete before external deployers are invited") but not the hard chart-validation constraint at §17.8.5 and the hard preflight constraint at §17.9 row 501. This is the same class of gap as iter3 BLD-005 (webhook first-deploy phase unassigned) and iter4 BLD-011 (preflight expected-set phase-aware), and resolves cleanly using the same feature-flag mechanism or a dedicated phase-assignment clause.

**Recommendation:** Amend §18 with phase-assignment clauses for the §18.1 artifacts. Two mechanically-equivalent options; Option A is the minimum-edit path, Option B is more structurally consistent with the iter4 BLD-011 fix:

- **Option A — name the artifacts in each first-consuming phase row, same pattern iter4 used for `lenny-direct-mode-isolation`/`lenny-drain-readiness`/`lenny-data-residency-validator`/`lenny-t4-node-isolation`.** Concretely:
  - Phase 2 gains a clause: "Build the `pkg/common/registry/resolver.go` `ImageResolver` shared package; Phase 2+ binaries MUST resolve image references through it. Phase 3 digest-pinning and Phase 3.5 chart slices depend on it."
  - Phase 2.5 gains a clause: "Produce the shared `pkg/alerting/rules` and `pkg/recommendations/rules` Go packages; the gateway's in-process evaluators and the Phase 13 deployer-visible `PrometheusRule` / ConfigMap template consume the same package output."
  - Phase 3.5 gains a clause naming `lenny-ops` as a mandatory Helm chart component from this phase onward, with the image built and shipping. The §17.8.5 "mandatory in every Lenny installation" statement is preserved; §18.1's Pre-GA ordering note is reinterpreted as "Phase 17a verifies operability is fully exercised by external deployers," not "`lenny-ops` is first-built at Phase 17a."
  - Phase 13 gains a clause naming `lenny-backup` Jobs as the first phase where backup/restore/verify Jobs are scheduled by `lenny-ops` — `lenny-backup` image first shipped here.
- **Option B — add a new `features.ops` Helm feature flag and phase-aware preflight parallel to BLD-011.** Treat `lenny-ops` as a gated component identical to `lenny-direct-mode-isolation`, with `features.ops=true` flipped at its first-deploy phase. This requires amending §17.8.5 to replace the unconditional "mandatory" language with "mandatory from Phase 3.5 onward (enforced by `features.ops=true` as a non-overridable default from that phase), MUST NOT be disabled." Option B is more defensible because it makes the chart-inventory-parity mechanism uniform across all §17.2 components, but it is a larger spec edit.

Either option must also answer the §17.9 row 501 (`lenny-ops-sa` RBAC preflight check) and §18.1 line 92 "Pre-GA ordering" wording so the three statements — §18 phase assignment, §17.8.5 chart validation, §18.1 Pre-GA ordering — all point at the same phase.

The `admission_webhook_inventory_test.go` companion suite (§17.2 line 71) and/or a new `lenny_ops_deployment_inventory_test.go` must cover the Phase 3.5+ expectation that `lenny-ops` is present, to avoid a chart-author omission shipping silently for `lenny-ops` the way iter3/iter4 caught for the four gated webhooks.

**Status:** Fixed (iter5). Applied Option A (minimum-edit, mirrors iter4 pattern for other mandatory components): (a) Phase 2 row gained a `ImageResolver` clause naming `pkg/common/registry/resolver.go` as a Phase-2 prerequisite for Phase-3 digest-pinning and Phase-3.5 first chart install (with cross-refs to §17.8.6 and §18.1); (b) Phase 2.5 row gained a `pkg/alerting/rules` / `pkg/recommendations/rules` shared-package clause naming the first-consumer phases (3.5 for alerts; 4.5 for recommendations; 13 for deployer-visible `PrometheusRule`); (c) Phase 3.5 row gained a "Mandatory `lenny-ops` first-deploy" clause naming `lenny-ops` image as built and shipping from Phase 3.5 onward, enumerating the full canonical layout rendered (Deployment + leader-elected Lease, headless `lenny-gateway-pods` Service, `lenny-backup-sa` ServiceAccount, four NetworkPolicies, PDB), specifying a `core_deployment_inventory_test.go` integration suite parallel to `admission_webhook_inventory_test.go`, and scoping `lenny-ops`'s Phase-3.5 responsibilities (self-health, Lease, static config) with admin-API fan-out becoming load-bearing at Phase 4.5; (d) Phase 13 row gained a "`lenny-backup` Jobs first ship" clause naming Phase 13 as the first phase where `lenny-backup` image is built and shipping and `lenny-ops` schedules backup/restore/verify Jobs per `ops_backup_schedule`, with a backup-restore-verify CI loop included in the milestone; (e) §18.1 line 92 "Pre-GA ordering" wording was replaced with a dedicated "Phase assignments" subsection naming the four artifacts' first-ship phases explicitly, followed by a slimmed-down "Pre-GA ordering" subsection that retains only the Phase-17a (community launch) and Phase-14 (image signing) ordering gates. The three previously-misaligned statements (§18 phase assignment, §17.8.5 chart validation, §18.1 Pre-GA ordering) now all point at Phase 3.5 for `lenny-ops`. Sections modified: `spec/18_build-sequence.md` Phase 2, Phase 2.5, Phase 3.5, Phase 13, and §18.1. No changes required in §17.8.5, §17.9, or §25.4 because the contracts they describe (mandatory, unconditional preflight, no supported topology without `lenny-ops`) are consistent with Phase 3.5 first-deploy. BLD-014 (row 501 guard alignment) and the `core_deployment_inventory_test.go` companion test are referenced by name in the Phase 3.5 clause, so BLD-014 can close against the same edit.

---

### BLD-013. Phase 1 wire-contract artifacts still do not include Shared Adapter Types / SessionEventKind registry [Low]

**Section:** spec/18_build-sequence.md line 8 (Phase 1 wire-contract list); spec/15_external-api-surface.md "Shared Adapter Types" and "SessionEventKind closed enum registry" paragraphs.

Iter4 BLD-010 (Low) asked for Phase 1 to include a normative Go-type artifact (e.g., `pkg/adapter/shared.go`) or JSON Schema pair (`schemas/session-event-v1.json` + `schemas/adapter-shared-v1.json`) codifying the Shared Adapter Types and `SessionEventKind` closed enum. Grepping the current `spec/18_build-sequence.md` confirms neither candidate artifact is named in Phase 1; the wire-contract list remains `schemas/lenny-adapter.proto` + `schemas/lenny-adapter-jsonl.schema.json` + `schemas/outputpart.schema.json` + `schemas/workspaceplan-v1.json`. No iter4 "Status: Fixed" entry exists for BLD-010; this finding is unchanged since iter3.

Re-raising per the iter5 feedback rubric "anchor to prior-iteration severity" at the same Low severity. Not a Phase-infeasibility: Phase 2's adapter binary protocol implementation and Phase 5's `ExternalAdapterRegistry` can build against prose definitions in §15, but the spec-first principle of Phase 1 ("Phase 2 implements against them and the CI build verifies the implementation stays in sync") is violated for the closed-enum contract that §15.2 declares normative for every external adapter.

**Recommendation:** Adopt iter4's BLD-010 recommendation verbatim. Extend Phase 1's "Machine-readable wire-contract artifacts committed to the repository" list with either:

- `pkg/adapter/shared.go` — Go package containing `SessionMetadata`, `AuthorizedRuntime`, `AdapterCapabilities`, `OutboundCapabilitySet`, `SessionEvent`, `PublishedMetadataRef`, and the closed `SessionEventKind` enum; or
- a `schemas/session-event-v1.json` + `schemas/adapter-shared-v1.json` JSON Schema pair with matching Go codegen, paralleling `workspaceplan-v1.json`.

Add the same CI gate as the other Phase 1 schemas: §15 additions mirror code changes (closed-enum additions bump the schema version and require a `SessionEventKind` row + `AdapterCapabilities.SupportedEventKinds` documentation update).

---

### BLD-014. `lenny-preflight` `lenny-ops-sa` RBAC check (§17.9 row 501) has no conditional guard matching BLD-012 [Medium]

**Section:** spec/17_deployment-topology.md §17.9 row 501 (`lenny-ops-sa RBAC` check); spec/18_build-sequence.md Phase 3.5 line 14 (admission-plane enumeration).

Follow-on to BLD-012. Even if BLD-012 Option A is adopted and `lenny-ops` is assigned to Phase 3.5 (first chart install), the `lenny-preflight` row 501 check has no `features.ops` or phase-gating guard in §17.9. Row 501's check is unconditional and reads `lenny-ops-sa` ServiceAccount permissions via `kubectl auth can-i`. That is the correct check *after* `lenny-ops` is first-deployed, but iter4 intentionally gated rows 496 (T4 webhook) and 497 (drain-readiness webhook) on their respective feature flags so pre-first-deploy installs skip cleanly. Row 501 should follow the same pattern if Option B is adopted (gated on `features.ops`), or at minimum cite §17.8.5's "mandatory from Phase 3.5 onward" as the condition under which the unconditional check is correct.

The `admission_webhook_inventory_test.go` four-case parameterisation in §17.2 line 71 likewise does not cover `lenny-ops` presence/absence; it only varies the three webhook flags. A companion suite `tests/integration/core_deployment_inventory_test.go` is needed if BLD-012 Option A lands, or the same suite extended with an `ops` dimension if Option B lands.

**Recommendation:** Align §17.9 row 501 wording with the BLD-012 resolution. If Option A: add a note "(unconditional from Phase 3.5 onward, per §17.8.5 mandatory-`lenny-ops` contract)." If Option B: wrap the check in a `features.ops: true` guard mirroring rows 496/497. In either case, add an integration-test companion parallel to `admission_webhook_inventory_test.go` that enforces the `lenny-ops` Deployment's presence at install and fail-closes on its absence.

Severity Medium because this is a preflight-completeness polish item downstream of BLD-012's structural fix, not an independent infeasibility.

**Status:** Fixed

**Resolution:** Applied Option A per BLD-012's resolution. (1) Added traceability annotation to spec/17_deployment-topology.md §17.9 row 503 (`lenny-ops-sa` RBAC check): "Unconditional from Phase 3.5 onward, per the §17.8.5 mandatory-`lenny-ops` contract — there is no `features.ops` flag because chart validation rejects any attempt to disable `lenny-ops`, so this check fires on every preflight run." (2) Added `core_deployment_inventory_test.go` to the §17.2 inventory-test paragraph (line 71) parallel to `admission_webhook_inventory_test.go`, covering the unconditional core-Deployment inventory (`lenny-ops` Deployment + supporting resources from §17.8.5) with fail-closed semantics on absence; no feature-flag parameterisation because `lenny-ops` is present in every phase from 3.5 onward. This mirrors the `core_deployment_inventory_test.go` reference already in spec/18_build-sequence.md Phase 3.5.

---

### BLD-015. Phase 13 observability milestone does not name the `lenny-ops` bundled-alerting-rules deliverable [Low]

**Section:** spec/18_build-sequence.md Phase 13 line 63 (full observability stack); spec/25_agent-operability.md §25.13 (bundled alerting rules).

Phase 13's deliverable list names "alerting rules, SLO monitoring" but does not name the §25.13 "bundled alerting rules" distribution mechanism by which the `pkg/alerting/rules` output is rendered into deployer manifests (§16.5 line 524: `PrometheusRule` CRD vs ConfigMap, controlled by `monitoring.format`). This is the Section-25-specific responsibility that Phase 13 must satisfy for the operability surface to be complete — `lenny-ops`'s own alerting requires the bundled-rules pipeline to be end-to-end wired, and §25.13 is cited by §17.2 line 524 and §17.9 row 674 as the source of the `PrometheusRule` render.

Info/Low-severity polish: Phase 13 is readable as complete without the explicit §25.13 callout because "alerting rules" is a superset, but §18.1's iter4 summary calls out `docs/alerting/rules.yaml` and the chart build's requirement that "deployer-visible manifests and the in-process compiled rules never diverge" as a Section-25 deliverable — and that deliverable is not phase-anchored anywhere else in §18. Without a phase-anchor, the CI gate that enforces the divergence guard has no phase at which it first becomes blocking.

**Recommendation:** Extend Phase 13's deliverable list with a clause: "Bundled alerting rules pipeline (§25.13): `docs/alerting/rules.yaml` generated from `pkg/alerting/rules`; Helm chart's `PrometheusRule`/ConfigMap template (controlled by `monitoring.format`) consumes the same source; CI gate fails the build if deployer-visible manifests diverge from the in-process compiled rules." This makes the §18.1 line 88 requirement concrete at a specific phase rather than a floating expectation.

---

## Convergence assessment

**Status: Near-convergence, one High finding remaining.**

Iter4's BLD-009 (High) and BLD-011 (High) are both verifiably fixed in the current spec — the Phase 3.5 deferred-items paragraph, the Phase 5.8/8/13 feature-flag flip clauses, and the §17.2 "Feature-gated chart inventory (single source of truth)" paragraph form a consistent three-point fix that the `admission_webhook_inventory_test.go` parameterisation (§17.2 line 71) and §17.9 rows 496/497/498 each honor. No regression has been introduced to either fix path.

Iter4's BLD-010 (Low) is unfixed and is re-raised verbatim as BLD-013 (Low) per the severity-anchoring rule. This is a polish item, not a convergence blocker.

The one remaining High finding, **BLD-012**, is a structural parallel to iter3 BLD-005 and iter4 BLD-011 — an §18.1 iter4 addition that indexed new Section-25 operability artifacts (`lenny-ops`, `lenny-backup`, shared packages, `ImageResolver`) without assigning them to any phase row, combined with §17.8.5's chart-validation contract and §17.9 row 501's unconditional preflight check that together make Phase 3.5 install-infeasible without `lenny-ops` being built. The same feature-flag mechanism that closed BLD-011 (Option B) or a per-phase deliverable clause matching iter4's pattern for the four gated webhooks (Option A) will close BLD-012. BLD-014 (Medium) and BLD-015 (Low) are follow-on polish items conditional on the BLD-012 resolution choice.

No Critical-severity findings. One High (BLD-012) blocks convergence for perspective 19; one Medium (BLD-014) and two Low (BLD-013, BLD-015) are carryover/polish items that should be addressed in the same iter5 fix cycle but do not individually block convergence. Expect convergence in iter6 if BLD-012 is resolved and BLD-013/014/015 are batched into the same fix pass.

---

# Iter5 Review — Perspective 20: Failure Modes & Resilience

**Scope.** Verified iter4 Failure Modes & Recovery findings (FLR-012…017) held or remained carried forward, and re-examined §10 gateway resilience, §11 circuit breakers, §12 storage failure semantics, and §16 alert coverage for NEW cascading-failure gaps or missing recovery paths.

**Carry-forward posture.** FLR-012 and FLR-013 are Fixed (symmetric-NULL 90s tier selection with `postgres_null` source label verified in `spec/10_gateway-internals.md:108,112,114` and `spec/16_observability.md:438`; Tier-3 node-drain note and anti-affinity recommendation verified in `spec/17_deployment-topology.md:908–916`). FLR-014/015/016/017 remain unresolved in the current spec and are documented below for continuity with the iter3/iter4 rubric (prior severity Low), not as fresh findings — severity is anchored to those earlier decisions per the severity-calibration rule. Two NEW findings are reported.

## Iter3/iter4 carry-forward (unresolved — severity held at iter4 levels)

- **FLR-014** `InboxDrainFailure` alert at `spec/16_observability.md:481` still carries prose (`"lenny_inbox_drain_failure_total incremented (any non-zero increase over a 5-minute window)"`) rather than an `expr:` PromQL field; third iteration this has been flagged. Severity held at Low.
- **FLR-015** PgBouncer readiness probe at `spec/12_storage-architecture.md:45` still `periodSeconds: 5, failureThreshold: 2, timeoutSeconds: 3`; no "Known limitation" amplification note added. Severity held at Low.
- **FLR-016** `Minimum healthy gateway replicas (alert)` table row at `spec/17_deployment-topology.md:904` still has no backing rule in §16.5 (no `GatewayReplicasBelowMinimum`, `GatewayAvailabilityLow`, or equivalent using `lenny_gateway_replica_count`). Severity held at Low.
- **FLR-017** `Gateway preStop drain timeout` row at `spec/17_deployment-topology.md:901` (60s / 60s / 120s) still does not correspond to any parameter in the §10.1 preStop logic formula `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30`. Severity held at Low.

These carry-forwards are the baseline against which new findings are calibrated (FLR-016/017 are "alert/table row referenced but not defined / not traceable to a mechanism" — Low). The new findings below are scored against that rubric.

---

## New findings (iter5)

### FMR-018. `QuotaFailOpenCumulativeThreshold` alert, `quota_failopen_cumulative_seconds` gauge, and `quota_failopen_started` audit event referenced in §12.4 are not defined anywhere [Medium]

**Section:** `spec/12_storage-architecture.md:224` (Per-tenant fail-open budget enforcement, Cumulative fail-open timer); `spec/16_observability.md` §16.1 Metrics (lines 7–260), §16.5 Alerting rules (lines 386–520), §16.7 Audit events

§12.4 line 224 specifies a complete financial-security control for the cumulative fail-open timer:

- A **gauge** `quota_failopen_cumulative_seconds` the gateway emits, reflecting the sliding-window cumulative seconds spent in fail-open across Redis outages in any rolling 1-hour window.
- An **alert** `QuotaFailOpenCumulativeThreshold` that "fires when the cumulative timer exceeds 80% of the configured maximum" — i.e., the pre-breach warning on the `quotaFailOpenCumulativeMaxSeconds` (default 300s) ceiling.
- An **audit event** `quota_failopen_started` emitted on each fail-open entry, carrying `tenant_id`, `service_instance_id`, and `timestamp`, described as enabling "billing consumers to detect and attribute overshoot windows".

None of these three artifacts are defined in §16:

- Grep of `spec/16_observability.md` for `quota_failopen_cumulative` returns zero matches. The §16.1 metric catalogue (which is declared the canonical metric registry — see the §16.1 heading and the catalogue-completeness CI gate referenced elsewhere in the spec) has no row for the gauge.
- Grep for `QuotaFailOpenCumulativeThreshold` in `spec/16_observability.md` §16.5 alerting table (lines 386–520) returns zero matches. The only adjacent alert is `RateLimitDegraded` at line 427, which only covers *active* fail-open state, not the cumulative pre-breach warning this alert is meant to provide.
- Grep for `quota_failopen_started` in §16.7 audit event catalog returns zero matches.

This is materially different from FLR-014/016/017 (polish-grade table/PromQL gaps). `quotaFailOpenCumulativeMaxSeconds` is explicitly documented as a **financial security control** with a default of 300s tuned per "the maximum acceptable quota overshoot window". When the control trips, the replica transitions to fail-closed for quota enforcement — a user-visible availability event. The absence of the 80% pre-breach alert means operators receive NO warning that cumulative exposure is approaching the configured ceiling; the first signal they receive is that quota enforcement has flipped to fail-closed (an availability regression). The missing `quota_failopen_started` audit event means billing consumers cannot attribute overshoot windows to specific (tenant, replica, timestamp) tuples as §12.4 promises. The missing gauge prevents custom dashboards or deployer-authored alerts from observing the condition at all — the value lives only in the gateway's in-memory state and the `/run/lenny/failopen-cumulative.json` file on each replica's node.

This is a missing recovery path for a common failure (Redis outage — the scenario for which the cumulative timer was introduced). Severity Medium, consistent with iter4 FLR-012 ("High — unmitigated common-drain failure") and the iter3/iter4 pattern of Medium for missing per-finding observability surface on a common failure mode.

**Recommendation:** Add three artifacts to §16, cross-referenced from §12.4 line 224:

1. **§16.1 Metrics** — add a row:

   ```
   | Quota fail-open cumulative seconds (`lenny_quota_failopen_cumulative_seconds`, gauge labeled by `service_instance_id` — sliding-window cumulative seconds spent in fail-open across Redis outages in the current rolling 1-hour window; resets on each rolling-window advance; persisted across replica restarts via `/run/lenny/failopen-cumulative.json` — see [§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes) Cumulative fail-open timer) | Gauge |
   ```

2. **§16.5 Alerting rules** — add a row between `RateLimitDegraded` (line 427) and `CertExpiryImminent`:

   ```
   | `QuotaFailOpenCumulativeThreshold` | `max by (service_instance_id) (lenny_quota_failopen_cumulative_seconds) > 0.8 * quotaFailOpenCumulativeMaxSeconds` sustained for > 60 s on any replica. Pre-breach warning that the cumulative fail-open timer is approaching the `quotaFailOpenCumulativeMaxSeconds` financial-security ceiling (default 300s); at the ceiling the replica transitions to fail-closed for quota enforcement and new sessions/token-consuming requests are rejected until Redis recovers. Pair with a concurrent `RateLimitDegraded` or `DualStoreUnavailable` to identify the underlying cause. See [§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes) Cumulative fail-open timer. | Warning |
   ```

3. **§16.7 Audit events** — add a `quota_failopen_started` bullet with `tenant_id`, `service_instance_id`, `timestamp` payload fields, cross-referencing §12.4 ("enables billing consumers to attribute overshoot windows").

Update §12.4 line 224 to link each artifact by anchor (`[§16.1](16_observability.md#161-metrics)`, `[§16.5](16_observability.md#165-alerting-rules-and-slos)`, `[§16.7](16_observability.md#167-section-25-audit-events)`) so the control surface is traceable end-to-end.

**Status:** Fixed

**Resolution:** All three artifacts catalogued in §16 and §12.4 line 224 updated to cross-reference them:

1. §16.1 — added `lenny_quota_failopen_cumulative_seconds` gauge row (labeled by `service_instance_id`) immediately after `lenny_quota_redis_fallback_total`, describing the sliding 1-hour window semantics, persistence to `/run/lenny/failopen-cumulative.json`, and cross-reference to §12.4.
2. §16.5 — added `QuotaFailOpenCumulativeThreshold` warning alert row between `RateLimitDegraded` and `CertExpiryImminent` with expression `max by (service_instance_id) (lenny_quota_failopen_cumulative_seconds) > 0.8 * quotaFailOpenCumulativeMaxSeconds` sustained for > 60s; description explains the pre-breach warning semantics and cross-references `RedisUnavailable`, the `quota_failopen_started` audit event, and §12.4.
3. §16.7 — added `quota_failopen_started` audit event bullet with payload fields `tenant_id`, `service_instance_id`, `timestamp`; cross-references §12.4, §16.1.1 (attribute naming), the paired `lenny_quota_redis_fallback_total` metric, and the `QuotaFailOpenCumulativeThreshold` alert.
4. §12.4 line 224 — reworded the existing sentence to link `quota_failopen_started` to §16.7, `lenny_quota_failopen_cumulative_seconds` to §16.1, and `QuotaFailOpenCumulativeThreshold` to §16.5, and clarified that the alert is the pre-breach warning before fail-closed transition.

Docs synced: `docs/reference/metrics.md` gains the gauge metric row (near `lenny_quota_redis_fallback_total`) and the `QuotaFailOpenCumulativeThreshold` alert row (near `RateLimitDegraded`); `docs/operator-guide/observability.md` Warning Alerts table gains the `QuotaFailOpenCumulativeThreshold` row with operator-oriented action guidance.

---

### FMR-019. `MinIOUnavailable` alert referenced in §17.7 runbook trigger is not defined in §16.5 [Low] — **Fixed (subsumed by OBS-032)**

**Section:** `spec/17_deployment-topology.md:760` ("MinIO failure" runbook); `spec/16_observability.md` §16.5 Alerting rules

The §17.7 operational runbook for "MinIO failure" (`docs/runbooks/minio-failure.md`) lists its trigger as:

> *Trigger:* `MinIOUnavailable` alert; workspace upload/download failures; `lenny_artifact_upload_error_total` spikes.

Grep of `spec/16_observability.md` for `MinIOUnavailable` returns zero matches. The MinIO-specific alerts that DO exist in §16.5 are `MinIOArtifactReplicationLagHigh` (line 515, RPO-tracking for cross-region replication) and `MinIOArtifactReplicationFailed` (line 516, object-level replication failures). Neither fires on primary-site MinIO unavailability — replication alerts fire while the primary is healthy but the DR target is degraded. `CheckpointStorageUnavailable` (line 390) is closest in spirit but is narrowly scoped to checkpoint-upload eviction failures, not the broader "workspace upload/download failures" the runbook implies.

Operators paging on `MinIOUnavailable` per the runbook wording will find no backing alert rule. The same pattern flagged as iter4 FLR-016 for `GatewayReplicasBelowMinimum` (table row references an alert not defined in the shipped `PrometheusRule`) — this is the artifact symmetric to that gap.

**Recommendation:** Either (a) add a `MinIOUnavailable` rule to §16.5 firing on sustained PUT/GET failures (e.g., `rate(lenny_artifact_store_errors_total[5m]) > 0.05 * rate(lenny_artifact_store_operations_total[5m])` sustained > 2 min, or a dedicated `lenny_artifact_store_reachability` gauge against a probe request); or (b) retitle the runbook trigger to name the existing `CheckpointStorageUnavailable` and add a secondary condition for non-checkpoint artifact-store errors; or (c) align terminology by adding the alert under a different name but re-pointing the runbook trigger line to it. Option (a) is preferred because a single consolidated "MinIO primary unreachable" signal is what an on-call operator would search for when the runbook mentions the name.

Severity Low, consistent with iter4 FLR-016 (table row references an alert not defined as a rule).

**Resolution:** Subsumed by the OBS-032 fix. Option (a) applied: `MinIOUnavailable` added as a Critical alert in §16.5 backed by `rate(lenny_artifact_upload_error_total{error_type="minio_unreachable"}[2m]) > 0` sustained for > 1 min; backing metric registered in §16.1; §17.7 runbook trigger updated to cross-reference §16.5 and paired alerts; docs/reference/metrics.md synced.

---

## Convergence assessment

**Direction:** Converging within Failure Modes & Resilience. Iter5 surfaces only 2 new findings (1 Medium, 1 Low), and 4 iter3/iter4 carry-forwards at Low. No Critical or High cascading-failure gap was identified — the high-impact recovery paths (preStop tiered cap, coordinator handoff, dual-store degraded mode, circuit breaker cache-only admission posture, quota fail-open per-user/per-tenant ceiling, delegation budget irrecoverable path) are all specified with metrics, alerts, audit events, and explicit fail-closed/fail-open semantics.

**Remaining work to close the perspective:**

1. Fix the four iter3/iter4 carry-forwards (FLR-014/015/016/017) — each is a small, well-scoped polish-grade change.
2. Fix FMR-018 by adding the three §16 artifacts (gauge row, alert row, audit event bullet).
3. Fix FMR-019 by aligning the §17.7 runbook trigger with an alert that exists (new or renamed).

**Blocker for convergence declaration:** FMR-018. The carry-forwards and FMR-019 are polish-grade and would not by themselves block a "perspective converged" declaration, but FMR-018 leaves a documented financial-security control without its required observability surface and should be closed before declaring convergence on this perspective.

---

# Iter5 Review — Perspective 21: Experimentation & A/B Testing Primitives

**Spec snapshot:** main @ 4027314.
**Scope:** `spec/10_gateway-internals.md` §10.7, `spec/15_external-api-surface.md` §15.1 error catalog + dryRun + `RegisterAdapterUnderTest` matrix, `spec/16_observability.md` §16.1/§16.6/§16.7, `spec/08_recursive-delegation.md` `experimentContext`, `spec/21_planned-post-v1.md` §21.6, `spec/22_explicit-non-decisions.md`.
**Numbering:** continues from iter4 (last EXP-016), so this iteration begins at EXP-017.

---

## Iter4 Fixed-item verification

| Iter4 finding | Status claim | Verification (iter5) |
| --- | --- | --- |
| EXP-013 (experiment events not in §16.6 catalog) | Fixed | Verified. §16.6 lines 607–613 list all five operational `experiment.*` events with payload fields; §16.7 line 636 lists `experiment.status_changed` as an audit event. Anchor `#107-experiment-primitives` resolves. |
| EXP-014 (admission-time isolation-monotonicity) | Fixed | Paragraph added at §10.7 line 854; §15.1 dryRun row at line 1123 echoes the check; §16.1 line 152 registers `lenny_experiment_isolation_rejections_total`; docs propagated to `docs/reference/metrics.md` line 398 and `docs/reference/error-catalog.md` line 166. Residual field-path issue captured in EXP-019 below. |
| EXP-010 (`INVALID_QUERY_PARAMS` undefined) | Not fixed | §10.7 line 950 still references `400 INVALID_QUERY_PARAMS`; catalog at §15.1 lines 971+ still omits it. Carried forward as EXP-017. |
| EXP-011 (variant count unbounded) | Not fixed | `maxVariantsPerExperiment` / `TOO_MANY_VARIANTS` not present anywhere in the spec; §10.7 line 941 still says "typically 2–5". Carried forward as EXP-018. |
| EXP-012 (sticky cache `paused → active` wording) | Not fixed | §10.7 line 1094 is verbatim what iter4 flagged: "no flush is required — the existing cached assignment remains valid." Carried forward as EXP-020. |
| EXP-015 (`BreakdownResponse` example divergence) | Not fixed | §10.7 lines 1014–1082 unchanged from iter4. Carried forward as EXP-021. |
| EXP-016 (no retry-with-fallback pathway) | Not fixed | §10.7 line 852 and §15.1 line 1045 still describe fail-closed behavior without an opt-out or a "no opt-out by design" statement. Carried forward as EXP-022. |

No iter4 Fixed items regressed. Five iter4 findings (EXP-010/011/012/015/016) remained open across the iter4 → iter5 fix pass and are carried forward below, retaining iter3/iter4's Low severity per the iter5 severity-anchoring rubric. New iter5 findings are EXP-019 and EXP-023–EXP-025.

---

## Carry-forward findings (from iter4, unchanged severity)

### EXP-017. `INVALID_QUERY_PARAMS` referenced by §10.7 is still not in the §15.1 error catalog [Low]

**Section:** `spec/10_gateway-internals.md` line 950; `spec/15_external-api-surface.md` §15.1 error catalog (lines 971–1077) and `RegisterAdapterUnderTest` matrix (line 1384).

§10.7's Results API query-parameter table (line 950) still rejects `?delegation_depth=0&breakdown_by=delegation_depth` with `400 INVALID_QUERY_PARAMS`. Iter2 EXP-002 introduced this; iter3 EXP-005 flagged it; iter4 EXP-010 re-flagged it. `INVALID_QUERY_PARAMS` remains absent from the canonical error-code table in §15.1 (grep across the spec tree confirms the token appears only at §10.7 line 950). The contract-test matrix at §15.1 line 1384 enumerates the codes adapters MUST exercise; this code is outside that enumeration, so REST/MCP consistency tests cannot cover the rejection path.

**Recommendation:** Choose one of two paths, consistent with the `cursor_expired` / `VALIDATION_ERROR` precedent at §15.1 line 1229:

- (a) Rewrite §10.7 line 950's closing clause to `"... is rejected with 400 VALIDATION_ERROR with details.fields[0].rule: \"breakdown_collision\""` and add `breakdown_collision` to the `rule` vocabulary in the validation-error-format example at §15.1 lines 1082–1105; **or**
- (b) Add `INVALID_QUERY_PARAMS` as a 400 row to §15.1 (lines 971+), append it to the `RegisterAdapterUnderTest` error-class list at line 1384, and mirror it in `docs/reference/error-catalog.md`.

### EXP-018. Variant count still unbounded; `maxVariantsPerExperiment` and `TOO_MANY_VARIANTS` remain unspecified [Low]

**Section:** `spec/10_gateway-internals.md` line 862 (variants "typically 2–5"), line 941 (Results API bound); `spec/15_external-api-surface.md` line 1123 (dryRun row); `spec/22_explicit-non-decisions.md`.

Iter2 EXP-003, iter3 EXP-007, and iter4 EXP-011 all flagged the absence of a variant-count cap. Iter5 grep for `maxVariantsPerExperiment` and `TOO_MANY_VARIANTS` across `spec/` returns zero matches. §10.7 line 941 ("bounded by operator configuration (typically 2–5)") still references a configuration key that does not exist. The consequences called out in iter4 are still live: a `POST /v1/admin/experiments` request with, say, 500 weight-0.001 variants would create 500 `SandboxWarmPool` CRDs, make the bucketing walk at line 756 (`for _, v := range variants`) O(500) on every session assignment, and give the paused-experiment `DEL t:{tenant_id}:exp:{experiment_id}:sticky:*` scan an unbounded keyspace to traverse.

**Recommendation:** Either (a) accept the recurring proposal — add a `maxVariantsPerExperiment` tenant-config key (default 10, maximum 100), enforce it in `POST/PUT /v1/admin/experiments` validation with a new `TOO_MANY_VARIANTS` 422 code in §15.1, echo the limit in the dryRun narrative at line 1123, rewrite line 941 to "bounded by `maxVariantsPerExperiment` (default 10)", and add the code to the `RegisterAdapterUnderTest` matrix at line 1384; or (b) if the platform-team's stance has changed, record the non-decision in §22 with rationale so future iterations stop re-surfacing it. The current ambiguity (no cap, no explicit non-decision) is what keeps the finding regenerating each cycle.

### EXP-020. Sticky-cache `paused → active` sentence still contradicts the preceding flush rule [Low]

**Section:** `spec/10_gateway-internals.md` line 1094.

Line 1094 still reads, verbatim: *"On `paused → active` re-activation, no flush is required — the existing cached assignment remains valid."* The preceding clause flushes all keys on `active → paused` via `DEL t:{tenant_id}:exp:{experiment_id}:sticky:*`; line 850 establishes that paused experiments are not evaluated by the `ExperimentRouter` (first-match rule walks only active experiments), so no entries can be populated while paused. On `paused → active`, "the existing cached assignment" refers to a cache that was just purged and cannot have been repopulated. Iter2 EXP-004 / iter3 EXP-008 / iter4 EXP-012 each identified this; no edit landed. The invariant that actually makes re-activation correct — HMAC-SHA256 determinism at line 751 — is never stated in this paragraph. The spec also remains silent on whether sessions created during the paused window are retroactively enrolled on re-activation (they are not, per the `experimentContext: null` carried forward on their session record, but readers must infer this).

**Recommendation:** Replace the second half of line 1094 with:

> "On `paused → active` re-activation, no re-seeding is required: percentage-mode assignment is deterministic (HMAC-SHA256 of `assignment_key + experiment_id`, line 751), so the first post-re-activation session for a given user recomputes the same variant as before the pause. The cache is lazily repopulated on demand. For `mode: external` experiments, re-evaluation is delegated to the OpenFeature provider per session. Sessions created during the paused window carry `experimentContext: null` and are not retroactively enrolled on re-activation, regardless of `sticky` mode."

### EXP-021. `BreakdownResponse` example still has unexplained per-bucket dimension-set divergence [Low]

**Section:** `spec/10_gateway-internals.md` lines 933 and 1014–1082.

Iter3 EXP-006's fix specified that each bucket's `dimensions` keys are the union of non-null `scores` keys within that bucket's rows only, so a bucket may legitimately omit a dimension that appears in the variant's default (flat) response. The example JSON at lines 1034–1036 shows `control.breakdowns[bucket_value=0]` containing `coherence` and `safety` but not `relevance` — yet the flat response example at lines 969–981 includes `relevance` for the same `control` variant. Additionally, the treatment buckets' `llm-judge` objects (lines 1068, 1075) omit the `dimensions` field entirely; the control buckets include it. The spec does not state whether `dimensions` is present-but-empty (`{}`), absent, or something else when a bucket contains no non-null dimensional scores.

**Recommendation:** Insert after line 1012 (current end of per-dimension rules inside the Breakdown block):

> "A bucket's dimension key set is the union of non-null `scores` keys across that bucket's rows only, so a bucket may omit dimensions that appear in the variant's flat response. In the example below, `control.breakdowns[bucket_value=0]` omits `relevance` because no row in that bucket submitted a `relevance` score, even though the variant's flat response includes it. When a bucket has no non-null dimensional scores for a given scorer, the bucket's `scorers[scorer].dimensions` field is **omitted** (not present as an empty object); the treatment buckets in the example below demonstrate this."

Then re-check the example that either every bucket includes a `dimensions` field or each missing one corresponds to a bucket with no dimensional rows.

### EXP-022. `VARIANT_ISOLATION_UNAVAILABLE` still has no documented retry / opt-out pathway (or explicit non-decision) [Low]

**Section:** `spec/10_gateway-internals.md` line 852; `spec/15_external-api-surface.md` line 1045; `spec/22_explicit-non-decisions.md`.

Iter3 EXP-009's fail-closed rule and iter4 EXP-014's admission-time validation together ensure operators learn of isolation-monotonicity problems before a rejection storm — but iter4 EXP-016's question remains: callers whose `minIsolationProfile` is set by tenant default have no machine-discoverable remediation other than "relax minIsolationProfile" (which requires a policy change the caller may not own) or wait for re-provisioning (unbounded). Iter4 EXP-016 proposed a binary choice — (a) add an `experimentOptOut: true` (or equivalent) flag on session create that bypasses experiment routing and runs the base runtime with `experimentContext: null`, OR (b) document the non-decision explicitly. Neither landed. The spec's silence here interacts badly with iter4 EXP-014's own acknowledgement that rejected sessions never appear in `EvalResult` (so the rejection population is unauditable): callers have no way to route around the experiment while analysts have no way to detect the rejected subset.

**Recommendation:** Pick one:

- (a) Add the opt-out flag. Session-create accepts `experimentOptOut: true` (requires a scoped permission, e.g., `session:experiment:opt-out`), routes the session to the base runtime unconditionally, tags `experimentContext: null` (not `variant_id: "control"` — preserves iter3 EXP-009's control-purity invariant), and emits `experiment.opt_out` (info) with `tenant_id`, `user_id`, `experiment_id`, `reason`. Reference the flag from `VARIANT_ISOLATION_UNAVAILABLE.details.remediation` in §15.1. Update §10.7 line 852 to list it as a third remediation option. **OR**
- (b) Add a paragraph to §10.7 after line 852 and a §22 bullet stating: "No per-session experiment opt-out is provided. Callers whose `minIsolationProfile` is incompatible with an active experiment's variant pool must either (1) have tenant policy relax `minIsolationProfile` for the affected cohort, or (2) accept 422 rejections until the operator re-provisions the variant pool. The tradeoff is deliberate: an opt-out would undermine randomization when used selectively." Cross-reference from `VARIANT_ISOLATION_UNAVAILABLE`'s `details.remediation`.

Either commitment unblocks clients; leaving the question open a fifth iteration is the worst outcome.

---

## New iter5 findings

### EXP-019. Admission-time isolation check uses response-side field path, not pool/runtime CRD field [Low]

**Section:** `spec/10_gateway-internals.md` line 854.

The iter4 EXP-014 fix paragraph describes the admission-time check as: *"the gateway resolves the variant's referenced pool and compares `sessionIsolationLevel.isolationProfile` against the base runtime's default pool `sessionIsolationLevel.isolationProfile`"*. `sessionIsolationLevel` is the **response-side** object returned by `POST /v1/sessions` to give clients visibility into assigned isolation (§07.1 line 65, §15.1 line 751). It is not a field on pool or runtime CRDs. On CRDs, the field is the top-level `isolationProfile` on the Runtime (§05.1 line 66) and the top-level pool-scoped override (§05.3). The `defaultPoolConfig` block on a Runtime (§05.1 line 97) does not nest `isolationProfile` under `sessionIsolationLevel` either. An implementer following line 854 literally would be unable to locate the field they are asked to compare.

Additionally, the comparison target "base runtime's **default pool**" is ambiguous: a runtime can register multiple pools (§05.3); only one of them might be tagged as the default. The spec's `defaultPoolConfig` block is the Runtime's template for its bootstrap default pool, not a persistent "default pool" pointer that the admission check can dereference at validation time.

**Recommendation:** Rewrite line 854 to use CRD field paths and an unambiguous comparison target:

> "For each variant, the gateway resolves the variant's referenced pool (field `variants[].pool`) and reads its effective `isolationProfile` (pool-level override, falling back to the Runtime's top-level `isolationProfile`). It compares this against the base runtime's effective `isolationProfile` (resolved from the Runtime named in `baseRuntime`, §5.1). If any variant's resolved `isolationProfile` is weaker than the base runtime's — using the canonical ordering `standard < sandboxed < microvm` defined in §5.3 — the request is rejected with `422 CONFIGURATION_CONFLICT` ..."

Also clarify what happens when the variant `pool` field is absent (the example at line 693 shows it, but the field is not formally required anywhere): either state that `pool` is required for variants, or specify the fallback (e.g., "`runtime`'s `defaultPoolConfig` is used and its `isolationProfile` is the Runtime's top-level value").

### EXP-023. Admission isolation check compares only against base runtime's pool, not against sessions the base runtime accepts [Medium]

**Section:** `spec/10_gateway-internals.md` lines 852 (runtime-time check), 854 (admission-time check); `spec/07_session-lifecycle.md` (`minIsolationProfile` resolution).

The runtime-time fail-closed rule (line 852) rejects a session when the **variant pool's** isolation is weaker than **that session's `minIsolationProfile`**. The iter4 admission-time rule (line 854) prevents only one subset of this: variants whose isolation is weaker than the **base runtime's default pool**. This leaves a gap. A tenant policy can specify a `minIsolationProfile: microvm` floor for session creation while the base runtime's default pool is `sandboxed` and a variant pool is also `sandboxed`. The admission check passes (variant == base), the experiment goes live, and every microvm-floor session is rejected with `VARIANT_ISOLATION_UNAVAILABLE` — exactly the silent availability regression iter4 EXP-014 aimed to prevent. The admission check's choice to compare "variant pool vs. base runtime" rather than "variant pool vs. the stricter of the tenant/runtime/per-caller defaults" makes the check operationally incomplete.

This matters more than EXP-019 because the gap allows a configuration that is strictly "valid" at admission yet guaranteed to reject a known, in-production traffic class at runtime, and the spec framed iter4's fix as solving this exact problem.

**Recommendation:** Extend line 854's admission check to additionally compare each variant pool's resolved `isolationProfile` against the **tenant-level `minIsolationProfile` floor** (if one is configured) and surface a warning — not a hard reject — when a variant pool's isolation is weaker than the tenant floor. Concretely: add a `details.warnings[]` array to the admission response body that lists each `(variant_id, variant_pool_isolation, tenant_floor)` tuple where `variant_pool_isolation < tenant_floor`. The response is still 2xx (the experiment is creatable), but the `?dryRun=true` path and the non-dryRun create/update both emit this warning so operators see it before rejections appear. Document the warning key (e.g., `warning_code: "variant_weaker_than_tenant_floor"`) in §15.1 and echo it in `docs/reference/error-catalog.md` under a new "Warnings" section. If introducing warning responses is too large a surface change, alternatively emit a new operational event `experiment.variant_weaker_than_tenant_floor` at creation/activation time (distinct from the runtime `experiment.isolation_mismatch`) and register it in §16.6 lines 607–613.

**Status:** Fixed (Option B — operational event route, per recommended smaller-surface alternative). A new "Admission-time tenant-floor advisory check" paragraph was added to `spec/10_gateway-internals.md` §10.7 directly after the existing admission-time isolation-monotonicity paragraph. It specifies that `POST /v1/admin/experiments` and `PUT /v1/admin/experiments/{name}` compare each variant pool's resolved `isolationProfile` against the tenant-level `minIsolationProfile` floor (when configured), that the response remains 2xx in both real and `?dryRun=true` paths, and that the gateway emits an `experiment.variant_weaker_than_tenant_floor` operational event per offending variant. The event was registered in `spec/16_observability.md` §16.6 alongside the existing `experiment.isolation_mismatch` event, with payload fields `tenant_id`, `experiment_id`, `variant_id`, `variant_pool_isolation`, `tenant_floor`, `actor_sub`, `emitted_at`, and explicit wording that distinguishes it from the runtime `experiment.isolation_mismatch` (admission-time advisory vs. runtime fail-closed). Option B was chosen over Option A (warnings array in admission response body) to avoid expanding the admission endpoint's response schema and to keep fail-closed/advisory semantics cleanly separated across transports. Option C (hard reject on tenant-floor miss) was rejected because a tenant floor may be legitimately stricter than the experiment's intended variant mix — an operator may knowingly accept the subset of rejected sessions.

### EXP-024. `experiment.status_changed` audit event does not record the sticky-cache flush outcome [Low]

**Section:** `spec/10_gateway-internals.md` line 1092 (audit event description), line 1094 (flush invariant); `spec/16_observability.md` line 636.

The audit event payload at §16.7 line 636 carries `tenant_id`, `experiment_id`, `previous_status`, `new_status`, `actor_sub`, `transition_at`. The cache flush documented at line 1094 is a side effect of `active → paused` and `active → concluded` / `paused → concluded` transitions, and it drives the `lenny_experiment_sticky_cache_invalidations_total` metric. If the `DEL` call fails (Redis unavailable, partial scan), the metric will under-count but nothing in the audit trail records whether the flush succeeded for a given transition. Operators auditing a post-incident "did stale sticky assignments continue routing after I paused this experiment?" have only the metric's rollup; they cannot join to an individual transition record.

**Recommendation:** Add two optional payload fields to the `experiment.status_changed` audit event documented at §16.7 line 636: `sticky_cache_flushed` (boolean — `true` if flush was attempted and reported zero errors, `false` if skipped or reported errors, absent when the transition did not trigger a flush) and `sticky_cache_flush_keys_deleted` (integer — count of keys the `DEL` reported; absent when flush was not attempted). Document these in §10.7 line 1094's flush paragraph so the cross-reference is bidirectional. This is Low severity because the primary correctness guarantee (HMAC determinism on re-activation) does not depend on flush success; the audit gap is an observability-for-forensics concern.

### EXP-025. Multi-experiment `created_at` ordering tiebreak undefined [Low]

**Section:** `spec/10_gateway-internals.md` line 774 (first-match rule), line 850 (multi-experiment restatement).

Line 774 specifies: "When multiple active experiments are defined for a tenant, the `ExperimentRouter` evaluates them in ascending order of `created_at` (experiment creation timestamp). For each experiment in that order, `assignVariant` is called independently. The router stops at the **first experiment** where the result is a non-control variant..." Line 850 restates the rule. Neither specifies a tiebreaker for experiments with identical `created_at` values — which is plausible when experiments are bulk-imported by a seed job or when two admin requests land within the same millisecond under Postgres's default `TIMESTAMP WITH TIME ZONE` precision. Without a stable tiebreak, the router's assignment for a session that hashes to non-control in both `A` and `B` is non-deterministic: a replica's Go map-iteration order or a Postgres index-order drift across minor versions would silently re-bucket the same user between `A` and `B`. This is a correctness invariant for the "single experiment per session" guarantee and for `sticky: user` semantics across replicas.

**Recommendation:** Amend line 774 and line 850 identically to read: "... evaluates them in ascending order of `(created_at, experiment_id)` — `experiment_id` is the secondary sort key to guarantee deterministic ordering across replicas when two experiments share a `created_at` value." Add a matching invariant to the `lenny-adapter.proto` or gateway state-machine documentation if such a tiebreaker already exists in the implementation plan; otherwise document it as a new constraint on the Postgres query. Low severity because bulk-create at identical timestamps is rare in practice, but the invariant is worth nailing down before v1.

---

## Convergence assessment

**Open finding count this iteration:** 9 (5 carry-forward + 4 new), all Low except EXP-023 (Medium).
**Iter4 Fixed items verified:** 2/2 intact (EXP-013, EXP-014).
**Regression count:** 0.

**Convergence trend:** Iter5 is close to convergence for this perspective. The iter4 iteration drove the two Medium-severity findings (EXP-013, EXP-014) to closure. The remaining five carry-forwards (EXP-017/018/020/021/022) are all Low and have been open for two or more iterations; each has a concrete, small-edit recommendation already drafted in prior iterations. Iter5's three new Low findings (EXP-019, EXP-024, EXP-025) are polish items exposed by reading the iter4 fix text closely. The one new Medium (EXP-023) is a substantive gap in the iter4 admission-time check's completeness — it preserves the "silent availability regression" class the admission check was written to close.

**Recommended next step:** one short fix pass addressing EXP-023 (Medium) and EXP-019 (to correct the field-path language introduced by iter4); at that point the remaining Low findings can be batched and the perspective converged in iter6.

---

# Iter5 Review — Perspective 22: Document Quality, Consistency & Completeness

**Scope.** Cross-reference integrity (intra-file and cross-file anchors), README TOC completeness, heading/title clarity, terminology consistency, typos. Per `feedback_severity_calibration_iter5.md`, severities are anchored to the iter1–iter4 rubric for this class of defect: intra-/cross-anchor resolution failures remain **Medium** (they silently misdirect readers and are invisible on GitHub preview), heading-clarity and README-TOC issues remain **Low** (navigation friction, no ambiguity in normative text), and typos are **Low** unless they change normative meaning.

**Method.** (1) Verified iter4 "Fixed" items (DOC-013 / DOC-014 / DOC-015 / DOC-016). (2) Confirmed iter4 carry-forwards (DOC-017 / DOC-018). (3) Programmatic anchor sweep: parsed every heading in `spec/*.md` to produce the authoritative anchor set, then matched every `](file.md#anchor)` and `](#anchor)` target in the spec against that set. 2,004 link targets scanned; 3 broken cross-anchors surfaced, all of which are new regressions introduced since iter4. (4) Programmatic README TOC parity check vs. actual `X.Y` headings, confirming DOC-018 scope exactly.

**Numbering.** DOC-019 onward (after iter4's DOC-018).

---

## Iter4 status re-verification

- **DOC-013** — `#1781-operational-defaults--quick-reference` in `10_gateway-internals.md`. **Fixed in iter4.** Both remaining references (`10_gateway-internals.md:139`, `:155`) now use the cross-file form `(17_deployment-topology.md#1781-operational-defaults--quick-reference)`. No intra-file form survives in `10_gateway-internals.md`.
- **DOC-014** — `25_agent-operability.md#253-endpoint-split-between-gateway-and-lenny-ops`. **Fixed in iter4.** Zero occurrences of the fabricated fragment in `spec/`. `13_security-model.md` Gateway row now points at the canonical `#253-gateway-side-ops-endpoints`.
- **DOC-015** — `25_agent-operability.md#251-overview`. **Fixed in iter4.** Zero occurrences in `spec/`. Retargeted to `#254-the-lenny-ops-service`, which is the normative home of the "`lenny-ops` is mandatory" statement.
- **DOC-016** — `16_observability.md#165-alerts`. **Fixed in iter4.** Zero occurrences in `spec/`.
- **DOC-017** — headings "16.7 Section 25 Audit Events" / "16.8 Section 25 Metrics". **Still unfixed** (now fourth iteration). Re-filed as DOC-022 below at the iter4 severity (Low).
- **DOC-018** — README TOC omissions of `4.0`, `24.0`, `18.1`. **Still unfixed** (now fourth iteration). Re-filed as DOC-023 below at the iter4 severity (Low).

---

## 21. Documentation & Cross-Reference Integrity

### DOC-019. Cross-file anchor `17_deployment-topology.md#179-preflight-checks` does not exist [Medium]

**Section:** `13_security-model.md:211` (NET-067 DNS egress peer requirement blockquote)

Introduced by the iter3/iter4 NET-067 fix ("DNS egress peer requirement"). The blockquote reads `... The `lenny-preflight` Job enforces this via the "NetworkPolicy DNS `podSelector` parity" check ([Section 17.9](17_deployment-topology.md#179-preflight-checks)) and fails the install/upgrade on any DNS egress rule whose peer omits `podSelector`.` The fragment `#179-preflight-checks` does not resolve: `17.9` in `17_deployment-topology.md:1306` is "Deployment Answer Files" (anchor `#179-deployment-answer-files`), not "Preflight Checks". There is no heading named "Preflight Checks" at any level in `17_deployment-topology.md`. The actual "NetworkPolicy DNS `podSelector` parity (NET-067)" check lives at line 493 inside the table under `#### Checks performed` (line 465), which is itself inside `### 17.6 Packaging and Installation` (anchor `#176-packaging-and-installation`). The anchor emitted by GitHub for the subheading `#### Checks performed` is `#checks-performed` (unqualified), so the citation silently points a reader at a completely different section (§17.9 Answer Files). This is the **same class of defect as iter4 DOC-014/015/016** (a cross-anchor that references a non-existent fragment), and it was introduced in the same NET-067 fix commit chain — the self-verification was not performed end-to-end.

**Recommendation:** Change `[Section 17.9](17_deployment-topology.md#179-preflight-checks)` → `[Section 17.6](17_deployment-topology.md#checks-performed)`. Alternatively, if the reviewer prefers a section-number-qualified citation, the canonical heading-level anchor is `#176-packaging-and-installation` and the prose should then direct the reader to the "Checks performed" table therein — but `#checks-performed` is a valid GitHub slug in that file (there is no other heading with that title in `17_deployment-topology.md`) and the section-number-free form is cleaner.

**Status:** Fixed — `spec/13_security-model.md:211` NET-067 blockquote updated: `[Section 17.9](17_deployment-topology.md#179-preflight-checks)` → `[Section 17.6](17_deployment-topology.md#checks-performed)`. The target `#### Checks performed` subheading (line 465 of `17_deployment-topology.md`) lives under `### 17.6 Packaging and Installation` and contains the "NetworkPolicy DNS `podSelector` parity (NET-067)" row at line 495. Verified no remaining callers reference the broken `#179-preflight-checks` fragment across `spec/` or `docs/`.

### DOC-020. Cross-file anchor `15_external-api-surface.md#152-mcp-endpoints` does not exist [Medium]

**Section:** `25_agent-operability.md:3625` (Audit Log Query API — `POST /v1/admin/audit-events/{id}/republish` scope note)

The `POST /v1/admin/audit-events/{id}/republish` row ends `... a caller lacking the scope receives `403 FORBIDDEN` (scope taxonomy: `tools:audit:republish`, [§15.2](15_external-api-surface.md#152-mcp-endpoints))`. The fragment `#152-mcp-endpoints` does not resolve: `### 15.2` at `15_external-api-surface.md:1256` is titled "MCP API", producing the canonical anchor `#152-mcp-api`. No heading named "MCP Endpoints" exists at any level in `15_external-api-surface.md` (verified by grep of `^#+\s`). Same class of defect as DOC-014 — a fabricated anchor fragment that silently points GitHub to the top of the target file. The parallel citation on the preceding row (line 3624, `[§11.7](11_policy-and-controls.md#117-audit-logging)`) resolves correctly, indicating this is a localized slip in the `republish` row rather than a rename not propagated.

**Recommendation:** Change `[§15.2](15_external-api-surface.md#152-mcp-endpoints)` → `[§15.2](15_external-api-surface.md#152-mcp-api)`.

**Status:** Fixed — `spec/25_agent-operability.md:3663` `POST /v1/admin/audit-events/{id}/republish` row updated: `[§15.2](15_external-api-surface.md#152-mcp-endpoints)` → `[§15.2](15_external-api-surface.md#152-mcp-api)`. Verified the target `### 15.2 MCP API` heading (line 1261 of `15_external-api-surface.md`) produces the canonical GitHub anchor `#152-mcp-api`. Grep confirmed no other callers of the broken `#152-mcp-endpoints` fragment remain across `spec/` or `docs/`.

### DOC-021. Cross-file anchor `15_external-api-surface.md#154-error-codes` does not exist [Medium]

**Section:** `25_agent-operability.md:4418` (MCP Management Server — `lenny_tenant_force_delete` row)

The row reads `... without the override, or when omitted, tenant-delete is rejected with `TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD` ([§15.4](15_external-api-surface.md#154-error-codes)) if holds exist. ...`. The fragment `#154-error-codes` does not resolve: `### 15.4` at `15_external-api-surface.md:1396` is titled "Runtime Adapter Specification" (anchor `#154-runtime-adapter-specification`). No "Error Codes" heading exists at any level in `15_external-api-surface.md`. The error-code table for the REST/admin surface is actually inside §15.1 (REST API) — searching for the canonical error code `TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD` would land the reader inside §15.1's REST tables, not §15.4 which is the adapter wire protocol. This is the same class of defect as DOC-014/015/016/019/020 — a fabricated anchor that silently misdirects. The prose intent is unambiguous ("the error code is defined in §15") but the specific landing target is wrong.

**Recommendation:** Two options: (a) retarget to the section where `TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD` is actually defined — search the spec for the code's defining row. A best-effort retarget is `[§15.1](15_external-api-surface.md#151-rest-api)` if the admin-API error is catalogued under §15.1. (b) If the intended reference was a general error-code index that does not yet exist, either create the index section with a stable anchor or drop the anchor fragment and cite the section-level anchor only. Pending a reviewer decision, a safe minimal fix is `[§15](15_external-api-surface.md#15-external-api-surface)` — inelegant but resolves.

**Status:** Fixed — `spec/25_agent-operability.md` `lenny_tenant_force_delete` row updated: `[§15.4](15_external-api-surface.md#154-error-codes)` → `[§15.1](15_external-api-surface.md#151-rest-api)`. Verified that `TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD` is defined at line 1034 of `spec/15_external-api-surface.md` inside the `### 15.1 REST API` section (line 585; anchor `#151-rest-api`). No "Error Codes" sub-heading exists in `15_external-api-surface.md`, so §15.1 is the most specific valid anchor. Grep across `spec/` and `docs/` confirmed no other callers of the stale `#154-error-codes` fragment remain (the only remaining matches are in this iter5 findings file itself).

### DOC-022. Re-file of iter4 DOC-017 — "16.7 Section 25 Audit Events" / "16.8 Section 25 Metrics" headings remain [Low]

**Files:** `spec/16_observability.md:617, 643`; `spec/README.md:105–106`

Unfixed for **four iterations** (iter1 DOC-002 → iter2 DOC-006 → iter3 DOC-011 → iter4 DOC-017 → iter5). The headings still juxtapose `16.7` / `16.8` with "Section 25", which reads as a structural inconsistency on first pass (two section numbers in one heading) and is mirrored verbatim in the README TOC. Iter1-through-iter4 recommendations have been consistent:

**Recommendation (unchanged):** Rename to `### 16.7 Agent Operability Audit Events` / `### 16.8 Agent Operability Metrics` in `spec/16_observability.md`. Open each subsection body with a single-sentence cross-reference such as "Introduced by §25 (Agent Operability)." Update the two mirrored lines in `spec/README.md:105–106` to the new titles (the anchors change too — from `#167-section-25-audit-events` / `#168-section-25-metrics` to `#167-agent-operability-audit-events` / `#168-agent-operability-metrics`; grep the tree for any callers and rewrite them in the same commit). If the platform policy is to keep "Section 25" in the heading verbatim, record that decision explicitly in this finding's resolution note so the re-file cycle stops.

### DOC-023. Re-file of iter4 DOC-018 — README TOC still omits three numbered subsections [Low]

**File:** `spec/README.md`

Unfixed for **four iterations** (iter2 DOC-007 → iter3 DOC-012 → iter4 DOC-018 → iter5). Programmatic verification against every `X.Y` heading in `spec/` (pattern: `^\d+\.\d+\s`, non-`X.Y.Z`) confirms exactly three omissions:

- `4.0 Agent Operability Additions` (`04_system-components.md:3`) — README lines 14–23 list 4.1 through 4.9 but not 4.0.
- `18.1 Build Artifacts Introduced by Section 25` (`18_build-sequence.md:75`) — README line 119 lists §18 as a parent with zero child entries, unlike every other multi-subsection chapter.
- `24.0 Packaging and Installation` (`24_lenny-ctl-command-reference.md:19`) — README lines 127–147 list 24.1 through 24.20 but not 24.0.

All three target anchors resolve correctly and are referenced in running prose (e.g., `17_deployment-topology.md:328` cites `#240-packaging-and-installation` successfully). The defect is purely TOC-level: readers scanning the table of contents do not learn these subsections exist, and any reader who lands on §4, §18, or §24 via the TOC misses the introductory overview content.

**Recommendation:** Insert three TOC lines in `spec/README.md` using the exact indentation the README already applies to level-3 entries:

```
  - [4.0 Agent Operability Additions](04_system-components.md#40-agent-operability-additions)
  - [18.1 Build Artifacts Introduced by Section 25](18_build-sequence.md#181-build-artifacts-introduced-by-section-25)
  - [24.0 Packaging and Installation](24_lenny-ctl-command-reference.md#240-packaging-and-installation)
```

The file-scoped programmatic anchor sweep confirms all three slugs resolve. Insert the §4.0 line between the current `- [4. System Components]` parent and `- [4.1 Edge Gateway Replicas]`; insert the §18.1 line as the first (and currently only) child of `- [18. Build Sequence]`; insert the §24.0 line between the current `- [24. `lenny-ctl` Command Reference]` parent and `- [24.1 Bootstrap]`. As with DOC-022, if the README convention is specifically that `X.0` overviews and single-subsection chapters are intentionally omitted, record that convention in this finding's resolution note so the re-file cycle terminates; otherwise the fix is three one-line inserts.

---

## Convergence assessment

**Cross-reference integrity (Medium-severity class).** Iter4's four anchor-integrity defects (DOC-013–016) are all verified fixed in-place, with zero surviving references to the broken fragments. However, three **new** anchor regressions were introduced in the same iter3 → iter4 commit chain that closed the prior ones (DOC-019 from NET-067, DOC-020 and DOC-021 from §25.9 audit-query surface edits and §25.12 MCP Management Server edits). This is the same failure mode called out explicitly in iter4 DOC-013 ("iter3 CPS-004 introduced the exact class iter3 DOC-008 had closed") and in iter4 DOC-014/015 ("the NET-051 self-verification note claimed the anchors resolved; they did not"). **The pattern of self-verification claims that were not actually executed is the root cause.** Convergence on this class requires an automated anchor-resolution check in CI before fix commits land — a 20-line Python script reading every `](file.md#anchor)` and matching it against the heading-derived slug set would have blocked DOC-014/015/016/019/020/021 at PR time. Until that CI gate exists, every new iteration should expect 2–4 new anchor regressions introduced by that iteration's non-DOC fixes.

**Navigation/heading clarity (Low-severity class).** DOC-022 (§16.7/§16.8 "Section 25 Audit Events/Metrics") and DOC-023 (README TOC omissions of §4.0 / §18.1 / §24.0) have now persisted **four iterations** each with unchanged recommendations and trivial fix costs (a three-line README patch for DOC-023, a two-heading rename plus README mirror update for DOC-022). Both are deliberate-looking enough that the reviewer cycle cannot distinguish "accepted" from "deferred" without an explicit policy statement. **Recommendation for the convergence meta-discussion:** adopt the rule that any Low-severity finding surviving three consecutive iterations with unchanged recommendation MUST receive either (a) a fix commit or (b) an explicit "accepted — will not fix" resolution note in the next iteration's fix pass. Open-ended re-files are a symptom of unclear ownership, not of genuine disagreement.

**Overall iter5 state for Perspective 22.** Five findings this iteration (three Medium anchor regressions and two Low carry-forwards). The Medium items are mechanically trivial to fix but their recurrence is the strongest signal in this perspective and warrants the CI-gate remediation above — the convergence blocker is a process gap, not a content gap. The Low items should be either fixed or formally accepted in iter5's fix pass per the rule proposed above, ending the re-file cycle. No findings rose to High severity because none of the broken anchors produced normative ambiguity in running text: in each case the prose surrounding the broken link unambiguously names the concept being referenced, so a reader losing the hyperlink still resolves the correct concept via text search. The risk is silent misdirection to the wrong section, not misimplementation of a normative requirement.

---

# Perspective 23 — Messaging, Conversational Patterns & Multi-Turn (Iter5)

**Scope.** Verify iter4 MSG-011..MSG-017 resolutions and re-examine items left unresolved. Iter4 marked MSG-011, MSG-012, MSG-013, MSG-014 Fixed; MSG-015, MSG-016, MSG-017 were opened but not explicitly closed (no Resolution / Status marker in the iter4 summary). Compact severity rubric — following `feedback_severity_calibration_iter5.md`, carryover findings retain their iter4 severity; new findings are capped to the same bar.

---

### MSG-018. Iter4 MSG-015 carryover — `message_dropped` receipt terminology still present in §7.2 [Low]

**Section:** spec/07_session-lifecycle.md §7.2 lines 282, 293, 341

Iter4 MSG-015 flagged three §7.2 sites that use `message_dropped` terminology contradicting the canonical `delivery_receipt` with `status: "dropped"` defined in §15.4.1 line 1706. The iter4 summary provided a recommendation but no Resolution / Status marker. Spec verification confirms all three sites retain the non-canonical phrasing:

- Line 282 (in-memory inbox overflow): "sender receives a `message_dropped` delivery receipt with `reason: "inbox_overflow"`".
- Line 293 (durable inbox overflow): "Overflow drops the oldest entry (`LPOP` + drop) with a `message_dropped` receipt".
- Line 341 (DLQ overflow): "the oldest DLQ entry is dropped and the sender receives a `message_dropped` delivery receipt with `reason: "dlq_overflow"`".

`message_dropped` is not an event type, not a `status` enum value, and not referenced anywhere in §15.4.1. An implementer building a receipt handler from §7.2 alone will search for a `message_dropped` type that does not exist.

**Recommendation:** Replace all three `message_dropped` occurrences with the canonical phrasing used elsewhere: "the sender receives a `delivery_receipt` with `status: "dropped"` and `reason: "inbox_overflow"`" (or `"dlq_overflow"`). This is a pure string substitution with no semantic impact; it closes the MSG-015 gap that iter4 left open.

---

### MSG-019. Iter4 MSG-016 carryover — `delivery_receipt.reason` schema comment still contradicts the `error`-status prose [Low]

**Section:** spec/15_external-api-surface.md §15.4.1 lines 1707, 1713

The iter4 MSG-013 fix added a canonical `delivery_receipt.reason` enum table (lines 1715–1724) that includes two `error`-status reasons (`inbox_unavailable`, `scope_denied`). The MSG-013 Resolution note explicitly states MSG-016's schema-comment fix was out of scope and "will be addressed separately" — but iter4 did not address it, so the contradiction is still present:

- Line 1707 schema comment: `"reason": "<string — populated when status is dropped, expired, or rate_limited>"` — explicitly omits `error`.
- Line 1713 prose: "`error` (delivery failed due to infrastructure error, e.g., `reason: "inbox_unavailable"` ..., or `reason: "scope_denied"` ...)" — `error`-status receipts DO carry `reason`.
- Line 1715–1722 canonical enum table: two `error`-status rows, both populating `reason`.

An implementer reading the inline schema block alone will omit `reason` on `error` receipts; a consumer parsing §15.4.1 end-to-end will see the contradiction and have to guess which form is authoritative.

A secondary ambiguity: line 1707's comment says `reason` is populated for `expired`, but line 1724 says `expired` "v1 does not define additional `reason` enum values — the status alone conveys the condition." These two sentences, five lines apart, disagree on whether `expired` receipts carry `reason`.

**Recommendation:** Update the line 1707 schema comment to match the canonical table and the line 1724 closure text:
`"reason": "<string — populated when status is dropped or error (per the canonical delivery_receipt.reason enum table below); omitted when status is delivered, queued, expired, or rate_limited>"`.
This is a single-line edit that removes the inline-comment vs. table contradiction without changing any semantics.

---

### MSG-020. Iter4 MSG-017 carryover — `msg_dedup` Redis key still missing from §12.4 key prefix table [Low]

**Section:** spec/12_storage-architecture.md §12.4 lines 180–193; spec/15_external-api-surface.md §15.4.1 line 1760

Iter4 MSG-017 flagged that `t:{tenant_id}:session:{session_id}:msg_dedup` — referenced at §15.4.1 line 1760 as a Redis sorted set for message-ID deduplication — is absent from the normative §12.4 key prefix table (rows 180–193). Verified in the current spec: `msg_dedup` is still referenced only in §15.4.1 line 1760, and `grep msg_dedup spec/12_storage-architecture.md` returns zero hits.

This is the same cross-section completeness defect that iter3 MSG-007 fixed for the durable inbox key (`:inbox`); it now reappears for `:msg_dedup`. The §12.4 text at line 195 explicitly names which keys the `TestRedisTenantKeyIsolation` integration test must cover (DLQ, inbox, semantic cache, delegation budget) — `msg_dedup` is omitted, so a test author building the suite from §12.4 alone will miss it and a cross-tenant deduplication-collision scenario will go undetected.

Concrete cross-tenant risk: sender-supplied message IDs are validated for uniqueness "within the tenant" (§15.4.1 line 1760), but if the Redis wrapper's tenant-scoping enforcement is not exercised by a `msg_dedup` test case, a regression that strips the tenant prefix from the dedup key would let tenant A's sender-supplied ID collide with tenant B's — causing a `400 DUPLICATE_MESSAGE_ID` rejection (or, worse, a silent dedup that drops a legitimate message) across tenant boundaries.

**Recommendation:** Add a row to §12.4 immediately after the existing `:inbox` row (line 186):

```
| `t:{tenant_id}:session:{session_id}:msg_dedup` | Message ID deduplication set | Sorted set scored by receipt timestamp; retains seen message IDs for `messaging.deduplicationWindowSeconds` (default 3600s); trimmed on write via `ZREMRANGEBYSCORE`; used to reject `400 DUPLICATE_MESSAGE_ID` (see [§15.4.1](15_external-api-surface.md#1541-adapterbinary-protocol) `id` field) |
```

Also extend the §12.4 line 195 `TestRedisTenantKeyIsolation` coverage sentence with a new clause: "(g) a `msg_dedup` write for tenant A's session must not be visible to a deduplication check scoped to tenant B's session, and a sender-supplied message ID duplicated across tenants must not produce a `DUPLICATE_MESSAGE_ID` rejection on the second tenant's write."

Also update the §15.4.1 line 1760 prose to cross-reference §12.4 for the canonical key registration.

---

### MSG-021. `SCOPE_DENIED` error-code entry mis-describes the `delivery_receipt` as an "event" [Low]

**Section:** spec/15_external-api-surface.md §15.1 line 992; §15.4.1 lines 1701, 1736

The `SCOPE_DENIED` row in the §15.1 error-code catalog (line 992) ends with "Returned as the `error` reason in a `delivery_receipt` **event**." But §15.4.1 is unambiguous elsewhere that `delivery_receipt` is the **synchronous** return value of `lenny/send_message` (line 1701: "Every `lenny/send_message` call returns a synchronous `delivery_receipt` object") and explicitly **not** an event — the only messaging event is `message_expired` (line 1736: "delivered asynchronously on the sender session's event stream ... it is **not** a field on the synchronous `delivery_receipt`").

Calling the receipt an "event" contradicts the canonical MSG-014 schema block that iter4 added and re-opens the transport ambiguity that MSG-014 closed. An implementer parsing §15.1 in isolation will wire a `delivery_receipt` event-stream handler that will never fire.

**Recommendation:** Change line 992 ending from "Returned as the `error` reason in a `delivery_receipt` event" to "Returned as the `reason` on a synchronous `delivery_receipt` with `status: "error"` (see [§15.4.1](#1541-adapterbinary-protocol) `delivery_receipt.reason` enum)." This single-sentence edit aligns the error-catalog entry with the canonical receipt/event distinction the iter4 MSG-014 fix established.

---

### MSG-022. `delivery_receipt.reason` enum table is missing the `rate_limited` inbound-cap reason already implied by §7.2 [Low/Info]

**Section:** spec/15_external-api-surface.md §15.4.1 lines 1715–1724; spec/07_session-lifecycle.md §7.2 line 371

Iter4 MSG-013's fix (lines 1715–1724) deliberately left `rate_limited` with no `reason` enum rows, stating on line 1724 "for `status: "rate_limited"`, v1 does not define additional `reason` enum values — the status alone conveys the condition." But §7.2 line 371 defines **two distinct rate-limit causes** for inter-session messaging:

- **Sender-side outbound cap** — `messagingRateLimit.maxPerMinute` (default 30) / `maxPerSession`, enforced on the sending session.
- **Receiver-side inbound aggregate cap** — `messagingRateLimit.maxInboundPerMinute` (default 60), enforced on the target session to prevent O(N²) sibling storms.

These two rejections have different operational meanings for the sender: outbound-cap exceedance means "slow down"; inbound-cap exceedance means "the target is being flooded by multiple senders — coordinate with peers or adopt a hub pattern." A sender that cannot distinguish them cannot choose the correct back-off strategy. §7.2 line 371 states: "Messages exceeding the inbound limit are rejected with a `RATE_LIMITED` delivery receipt" — but on the wire this is the same opaque `status: "rate_limited"` with no `reason` that §15.4.1 mandates for the outbound case.

This is Info-level because §7.2 correctly describes the two caps and no message is silently lost; it is flagged to note that a future v1.x enum extension (`reason: "sender_rate_limit"` vs. `"target_inbound_rate_limit"`) would be a straightforward diagnostic win without breaking the closed-enum discipline.

**Recommendation:** Defer to a future spec iteration (outside the convergence scope). If added later, mirror the `delivery_receipt.reason` enum table pattern iter4 MSG-013 established: two rows under `status: "rate_limited"`, each citing the §7.2 cap it corresponds to.

---

## Convergence assessment

**Perspective 23 — Messaging, Conversational Patterns & Multi-Turn — NOT YET CONVERGED.**

Iter5 opens five findings, all Low/Info: three (MSG-018 / MSG-019 / MSG-020) are direct carryovers of iter4 MSG-015 / MSG-016 / MSG-017 that iter4 left unresolved (no Resolution / Status marker on those items in the iter4 summary; spec verification confirms the defects are still present verbatim). MSG-021 is a newly identified cross-reference inconsistency in §15.1 that re-introduces the `delivery_receipt` "event vs. synchronous response" ambiguity iter4 MSG-014 fixed elsewhere. MSG-022 is Info-only and explicitly deferred.

No High / Critical / Medium message-loss or mis-routing bugs are open. The structural messaging model (MSG-011 inbox TTL state-gating, MSG-013 receipt-reason enum, MSG-014 `message_expired` event schema, MSG-012 `LTRIM` allowlist) remains correctly fixed after iter4. Closure of the four Low findings above (all single-line or single-table-row edits) converges this perspective.

---

# Iter5 Review — Perspective 24: Policy Engine & Admission Control

**Scope:** `spec/11_policy-and-controls.md` §11.6 (Circuit Breakers) with cross-file touchpoints in `spec/16_observability.md` §16.1/§16.5/§16.7 and `spec/12_storage-architecture.md` §12.4 (`cb:{name}` key schema) and `spec/04_system-components.md` §4.8 (pre-chain gate callout).

**Iter4 carry-forward verification.**

- **POL-018** (Medium, audit-field alignment between §11.6 and §16.7 for `admission.circuit_breaker_rejected`) — **Fixed.** `spec/11_policy-and-controls.md:310` now lists `caller_sub`, `caller_tenant_id`, `limit_tier`, `replica_service_instance_id`, `parent_session_id`, `delegation_depth`, and the continuation-vs-delegation snapshot split; the sentence ends with "§16.7 is the authoritative schema source for this event." The POL-020 side-effect ("/ equivalent" placeholder) is also removed — `grep` across the spec returns no matches.
- **POL-019** (Medium, Admission-path Redis-outage posture) — **Fixed.** `spec/11_policy-and-controls.md:287-293` adds the "Admission-path Redis-outage posture" paragraph with running-replica / fresh-replica / recovery bullets and the `CIRCUIT_BREAKER_CACHE_UNINITIALIZED` readiness-refusal reason. `spec/16_observability.md:205-207` adds `lenny_circuit_breaker_cache_stale_seconds`, `lenny_circuit_breaker_cache_stale_serves_total`, and `lenny_circuit_breaker_cache_initialized`. `spec/16_observability.md:505` adds `CircuitBreakerStale`. `spec/16_observability.md:628` adds the `admission.circuit_breaker_cache_stale` audit event.
- **POL-020** (Low, "/ equivalent" placeholder) — **Fixed** as a side-effect of POL-018 (see above).
- **POL-021** (Low, `limit_tier=operation_type` value set never enumerated) — **NOT fixed.** `grep "operation_type" spec/` returns only the metric-label callouts at `spec/16_observability.md:203-204`, `spec/11_policy-and-controls.md:306,308,310,312`, and `spec/16_observability.md:627`. All treat `operation_type` as a free-form string; none enumerate the closed value set. The two operator-declarable states at §11.6 lines 280-281 ("Uploads temporarily disabled", "Delegation depth > N disabled during incident") still have no canonical mapping. Carried into iter5 as POL-023 below with broadened scope.
- **POL-022** (Low, prose "before quota and policy evaluation" at the former line 298) — **NOT fixed.** The same prose framing now lives at `spec/11_policy-and-controls.md:306` ("before quota and policy evaluation"). The back-to-back paragraph at line 308 (the POL-014 callout) uses the canonical phase vocabulary (`PreAuth` → `AdmissionController` → `PostAuth`/`PreDelegation`), producing the exact reconcile-in-reader's-head problem iter4 identified. Carried into iter5 as POL-024 below.

**Iter5 new findings.**

---

### POL-023 Admin API `POST /v1/admin/circuit-breakers/{name}/open` body schema is under-specified — no way to declare which scope (`runtime` / `pool` / `connector` / `operation_type`) the breaker covers [High]

**Section:** `spec/11_policy-and-controls.md:303` (admin API body definition), `spec/11_policy-and-controls.md:283` (`cb:{name}` Redis value schema), `spec/11_policy-and-controls.md:306` ("applies to the requested runtime, pool, connector, or operation type"), `spec/12_storage-architecture.md:192` (Redis key-prefix table confirms the stored value shape).

`spec/11_policy-and-controls.md:306` states that the `AdmissionController` rejects a request when "any open circuit breaker applies to the requested runtime, pool, connector, or operation type." But nothing in the admission spec, the Redis value schema, or the admin API body tells the gateway **which scope a named breaker covers**:

1. The admin API body at `spec/11_policy-and-controls.md:303` is defined exclusively as `{ "reason": string }`. There is no field for the breaker's `limit_tier`, no field for the matched runtime name, no field for the matched pool name, no field for the matched connector identifier, no field for the `operation_type` value.
2. The `cb:{name}` Redis value at `spec/11_policy-and-controls.md:283` and `spec/12_storage-architecture.md:192` is `{state: "open"|"closed", reason: string, opened_at: ISO8601, opened_by: user_id}`. No scope-bearing fields.
3. The iter3 POL-017 fix introduced `limit_tier` as a metric and audit label with vocabulary `runtime | pool | connector | operation_type`, and §16.7 line 627 records that value in the rejection audit event — but the admin API never lets the operator **set** a scope, so the admission path has no authoritative source for "does this breaker match this runtime?" beyond inferring from the breaker's `{name}` string.

The practical consequences are security-salient:

- **Policy-evaluation correctness hole.** An operator opens a breaker at `/v1/admin/circuit-breakers/runtime_python_ml_degraded/open` with `{ "reason": "runtime degraded" }`. The admission path at line 306 is supposed to reject requests for the `runtime_python_ml` runtime, but there is no specified mechanism to turn `runtime_python_ml_degraded` into "matches `runtime=runtime_python_ml`". Either (a) the name-to-scope mapping is conventional/by-naming-convention (unspecified and fragile), or (b) the breaker matches every admission check regardless of the requested runtime/pool/connector (over-broad fail-closed), or (c) the breaker matches nothing and the admission path is silently a no-op (fail-open against the operator's intent). The spec does not pin which of these holds.
- **`limit_tier` label integrity.** The `admission.circuit_breaker_rejected` audit event populates `limit_tier` with one of `runtime | pool | connector | operation_type`. For the gateway to emit the correct value, it must know which scope the breaker covers — but there is no input path that communicates that scope. The label will either be populated heuristically from the breaker name (a brittle convention that iter3 POL-017 did not specify) or a single default value (which breaks the "metric spike correlates 1:1 with sampled audit rows" guarantee that the same §16.7 paragraph documents).
- **Pre-existing registration vs. ad-hoc creation.** The admin API surface enumerates `GET /v1/admin/circuit-breakers` (list), `GET /v1/admin/circuit-breakers/{name}` (read), `POST .../open`, `POST .../close`. There is no registration endpoint — the list endpoint implies some `{name}`-s are known a priori, but the spec does not say how a breaker's scope was declared when it was registered, nor whether `POST .../open` against an unknown name is an implicit registration with no scope information.
- **Interaction with POL-021.** The iter4 POL-021 Low-severity finding flagged a closed-enum gap for `operation_type`. That finding is a subset of this one: even if `operation_type` had a closed enum, the admin API would still have no way to declare **which** `operation_type` value the breaker binds to for a given `{name}`. This is why the severity escalates to High for iter5 — the iter4 narrow framing as an enum omission under-represented the body-schema gap.

This is material for the POL perspective because circuit breakers are the incident-response primitive. An operator who runs `POST /v1/admin/circuit-breakers/uploads_disabled/open` during an incident needs the platform to actually block uploads — not to block everything, not to block nothing, and not to depend on an unspecified naming convention to work.

**Recommendation:** Define a structured body schema for `POST /v1/admin/circuit-breakers/{name}/open` that captures the breaker's scope at open-time and persists it into `cb:{name}`. Concretely:

1. Extend `cb:{name}` value to `{state, reason, opened_at, opened_by, limit_tier, scope}` where `limit_tier ∈ {runtime | pool | connector | operation_type}` (same closed vocabulary as the metric/audit label) and `scope` carries the tier-specific matcher (`{ "runtime": "runtime_python_ml" }`, `{ "pool": "gpu-pool-a" }`, `{ "connector": "github-app" }`, or `{ "operation_type": "uploads" | "delegation_depth" | ... }`).
2. Rewrite the admin API body at `spec/11_policy-and-controls.md:303` to:
   ```
   { "reason": string, "limit_tier": "runtime"|"pool"|"connector"|"operation_type", "scope": { <tier-specific matcher> } }
   ```
   Reject requests with an unknown `limit_tier` or an ill-formed `scope` at the API boundary with `INVALID_BREAKER_SCOPE` (HTTP 422).
3. Add a `POST /v1/admin/circuit-breakers` registration endpoint (or document that `POST .../open` against an unknown name registers and opens atomically with the provided `limit_tier`/`scope`). Either way, pin the behavior explicitly so the list endpoint and the open endpoint share a well-defined breaker-lifecycle model.
4. In `spec/11_policy-and-controls.md:306`, amend the AdmissionController-evaluation sentence to state that the match is against the persisted `limit_tier`/`scope` of the breaker (not the `{name}` string), so the admission path's policy-evaluation rule is authoritative and not naming-convention-bound.
5. Update `spec/12_storage-architecture.md:192` to reflect the extended value shape.
6. This subsumes POL-021's `operation_type` enumeration requirement: declaring `operation_type` as one of the `limit_tier` values with a tier-specific closed-enum matcher (`{ "operation_type": "uploads" | "delegation_depth" | ... }`, extensible via a platform-level admin endpoint) is the natural home for the enumeration.

**Status:** Fixed

**Resolution:**
- `spec/11_policy-and-controls.md` "Storage and propagation" paragraph (§11.6): extended `cb:{name}` Redis value shape to `{state, reason, opened_at, opened_by, limit_tier, scope}` and added an in-line tier→scope-shape table enumerating the four `limit_tier` values and the closed `operation_type` value set (`uploads | delegation_depth | session_creation | message_injection`). Stated explicitly that `{name}` is an operator-facing label and not used for scope matching.
- `spec/11_policy-and-controls.md` Admin API table (§11.6): rewrote the `POST /v1/admin/circuit-breakers/{name}/open` body schema to `{reason, limit_tier, scope}`, pinned `INVALID_BREAKER_SCOPE` (HTTP 422) rejection rules for malformed/mismatched/out-of-vocabulary inputs, and documented the register-and-open-atomically behavior for previously-unknown `{name}`. Added the scope-immutability rule for existing breakers. Updated the `close` entry to note body is empty and persisted scope is retained across open→closed→open cycles.
- `spec/11_policy-and-controls.md` AdmissionController evaluation paragraph (§11.6): rewrote the matching rule to evaluate against persisted `limit_tier`/`scope` (not `{name}`) and enumerated the per-tier match semantics. Pinned that `admission.circuit_breaker_rejected`'s `limit_tier` payload field is sourced from the matched breaker's persisted `limit_tier`, closing the label-integrity gap.
- `spec/12_storage-architecture.md` Redis key table (row `cb:{name}`): updated value shape to include `limit_tier` and `scope` with per-tier enumeration and called out that `{name}` is a label-only field.
- `spec/15_external-api-surface.md` §15.4 error catalog: added `INVALID_BREAKER_SCOPE` (PERMANENT, HTTP 422) row with full `details.field` / `details.reason` vocabulary.
- `spec/24_lenny-ctl-command-reference.md` §24.7 row: updated the `lenny-ctl admin circuit-breakers open <name>` command signature to include `--limit-tier` / `--scope` / `--reason` flags mapping to the new admin API body fields.
- `docs/api/admin.md` Circuit breakers section: split into operator-managed vs SDK-warm subsections, added full endpoint table, and documented the `POST .../open` body schema, per-tier scope shape, register-and-open-atomically semantics, scope-immutability rule, and `422 INVALID_BREAKER_SCOPE` response.
- `docs/reference/error-catalog.md`: added the `INVALID_BREAKER_SCOPE` row next to `CIRCUIT_BREAKER_OPEN` with parallel description and operator-facing resolution guidance.
- `docs/operator-guide/troubleshooting.md` and `docs/operator-guide/lenny-ctl.md`: updated `lenny-ctl admin circuit-breakers open` usage examples (both the short summary and the Emergency Response worked example) to show the new `--limit-tier` / `--scope` / `--reason` flags with a canonical `operation_type` example.

Subsumes POL-021: `operation_type` now has a closed value set (`uploads | delegation_depth | session_creation | message_injection`) pinned in `spec/11_policy-and-controls.md:290` and `spec/12_storage-architecture.md:192`, with the operator-declarable states at §11.6 lines 280-281 now mapping canonically to `operation_type: "uploads"` and `operation_type: "delegation_depth"`.

No conflict with POL-026 (which catalogs `circuit_breaker.state_changed` in §16.7); POL-026's recommendation already anticipates the POL-023 extension by proposing `limit_tier` + `scope` fields in that audit event payload, and POL-023 is a prerequisite for POL-026's field list being well-defined.

---

### POL-024 §11.6 "AdmissionController evaluation" line 306 still uses pre-iter3 prose framing, contradicting the adjacent POL-014 callout at line 308 [Low]

**Section:** `spec/11_policy-and-controls.md:306-308`.

The iter4 POL-022 finding (Low) identified this exact issue at the pre-POL-019-edit line 298; the iter4 fix added the "Admission-path Redis-outage posture" paragraph above, pushing the unchanged sentence to line 306 but not revising its framing. Line 306 now reads:

> "**AdmissionController evaluation.** The gateway evaluates all active (open) circuit breakers at the start of every session-creation and delegation admission check, **before quota and policy evaluation**. If any open circuit breaker applies to the requested runtime, pool, connector, or operation type, the request is rejected immediately with `CIRCUIT_BREAKER_OPEN` (HTTP 503, `retryable: false`)."

The immediately-following callout at line 308 uses the canonical phase vocabulary:

> "Circuit-breaker evaluation is a **pre-chain gate** and is NOT an interceptor. It runs **after `AuthEvaluator` completes at `PreAuth`** and **before the `PostAuth` and `PreDelegation` interceptor chains run**..."

The two framings describe the same ordering but with different vocabularies (prose "before quota and policy evaluation" vs. canonical `PreAuth` → `AdmissionController` → `PostAuth`/`PreDelegation`). `QuotaEvaluator` runs at `PostAuth` and `DelegationPolicyEvaluator` runs at `PreDelegation`, so "before quota and policy evaluation" is technically consistent — but a reader scanning the section for "where does the circuit-breaker gate run?" has to reconcile two different framings in back-to-back paragraphs and to know the §4.8 interceptor priority table to verify the reconciliation.

This is Low-severity (documentation consistency, not a correctness bug) and its framing is unchanged from iter4 POL-022 — consistent severity anchoring.

**Recommendation:** Rewrite line 306 to use the canonical phase vocabulary, eliminating the redundant prose framing. Suggested text:

> "**AdmissionController evaluation.** The gateway evaluates all active (open) circuit breakers as a **pre-chain gate** at the start of every session-creation and delegation admission check — **after `AuthEvaluator` completes at `PreAuth`** and **before the `PostAuth` and `PreDelegation` interceptor chains run** (see the pre-chain gate callout below and [§4.8](04_system-components.md#48-gateway-policy-engine)). If any open circuit breaker's `limit_tier`/`scope` matches the requested runtime, pool, connector, or operation type, the request is rejected immediately with `CIRCUIT_BREAKER_OPEN` (HTTP 503, `retryable: false`). The error body includes `circuit_name`, `reason`, and `opened_at`."

The POL-014 callout at line 308 can then be trimmed to reference-only (no re-stating of the ordering) to reduce the amount of prose that must stay synchronized on future edits.

---

### POL-025 Operator-identity field name drifts across three circuit-breaker surfaces (`opened_by` vs. `operator_user_id` vs. `caller_sub`) [Medium]

**Section:** `spec/11_policy-and-controls.md:283` (`cb:{name}` Redis value), `spec/11_policy-and-controls.md:295` (operational event emission payload), `spec/11_policy-and-controls.md:310` (audit-event payload for `circuit_breaker.state_changed`), `spec/12_storage-architecture.md:192` (same `cb:{name}` schema).

The iter4 POL-018 fix aligned the `admission.circuit_breaker_rejected` audit event on `caller_sub`/`caller_tenant_id`, making §11.6 line 310 consistent with §16.7 line 627. The fix did **not** touch three sibling surfaces that still use pre-iter3 field names:

1. **`cb:{name}` Redis value.** `spec/11_policy-and-controls.md:283` and `spec/12_storage-architecture.md:192` both pin the value to `{state, reason, opened_at, opened_by: user_id}`. The comment specifically annotates `opened_by` as `user_id` — gateway-internal, not an OIDC subject claim.
2. **Operational event (CloudEvents `dev.lenny.circuit_breaker_opened`/`_closed`).** `spec/11_policy-and-controls.md:295` says the emitted payload includes "`name`, `reason`, and `operator_user_id`".
3. **`circuit_breaker.state_changed` audit event.** `spec/11_policy-and-controls.md:310` says the payload contains "`circuit_name`, `old_state`, `new_state`, `reason`, `operator_user_id`, `timestamp`".

The three surfaces describe the same operator identity three different ways. POL-018's rationale for `caller_sub`/`caller_tenant_id` on the rejection event ("OIDC subject identifier with different GDPR exposure than a gateway-internal `user_id`") applies identically to all three: the operator who opened the breaker is authenticated via the admin API and their identity propagates from the same JWT claim source. Using `user_id` / `operator_user_id` here means:

- The Redis-stored identity has different GDPR/retention semantics than the audit identity of the `admission.circuit_breaker_rejected` event, for the same person performing the same operation.
- The operational CloudEvents payload at `/v1/admin/events/*` uses one schema for the operator identity; the audit payload at §16.7 (when `circuit_breaker.state_changed` is catalogued there — see POL-026 below) will use another; the `admission.circuit_breaker_rejected` event uses a third — SIEM consumers and operator tooling must carry three parallel mappings for the same concept.
- `opened_by: user_id` in the `cb:{name}` Redis value is surfaced on the admin-API `GET /v1/admin/circuit-breakers/{name}` response; operators reading that response see `user_id` and have to know it is a gateway-internal identifier rather than the `sub` claim they would see on the rejection audit row.

This is Medium because it is the exact POL-018 drift (identity-field vocabulary inconsistency across POL surfaces) that iter4 fixed for the rejection event but that still exists for the state-change event and the Redis-stored value. Consistent severity anchoring to POL-018.

**Recommendation:** Rename `opened_by` → `opened_by_sub`/`opened_by_tenant_id` in the `cb:{name}` Redis value (both `spec/11_policy-and-controls.md:283` and `spec/12_storage-architecture.md:192`). Rename `operator_user_id` → `operator_sub`/`operator_tenant_id` on the CloudEvents operational payload at `spec/11_policy-and-controls.md:295` and on the `circuit_breaker.state_changed` audit payload at `spec/11_policy-and-controls.md:310`. This aligns the operator-identity vocabulary with POL-018's `caller_sub`/`caller_tenant_id` and with the §13.3 Token Service audit events.

**Status:** Fixed

**Resolution:** Renamed the operator-identity field across all three circuit-breaker surfaces to the POL-018-aligned `*_sub`/`*_tenant_id` vocabulary:
- `spec/11_policy-and-controls.md:283` "Storage and propagation": `cb:{name}` Redis value now reads `{state, reason, opened_at, opened_by_sub, opened_by_tenant_id, limit_tier, scope}`, with an in-line note that this vocabulary matches the `caller_sub`/`caller_tenant_id` pair on `admission.circuit_breaker_rejected` so SIEM operator-identity pivots use one field shape across all breaker surfaces.
- `spec/11_policy-and-controls.md:304` "Event emission": CloudEvents `circuit_breaker_opened`/`_closed` payload now carries `operator_sub`/`operator_tenant_id` (was `operator_user_id`).
- `spec/11_policy-and-controls.md:319` "Audit events": `circuit_breaker.state_changed` payload now carries `operator_sub`/`operator_tenant_id` (was `operator_user_id`).
- `spec/12_storage-architecture.md:192` Redis key catalogue: updated `cb:{name}` value shape to the new field pair with a cross-reference to all three §11.6 surfaces that share the vocabulary.
- `docs/api/admin.md:748` `GET /v1/admin/circuit-breakers/{name}` response body: updated to expose `opened_by_sub`/`opened_by_tenant_id` (was `opened_by`) so the admin-API response now matches the Redis-stored shape and the audit identity vocabulary.

Regression grep confirms no remaining `operator_user_id` occurrences in `spec/`, and the only `opened_by` / `opened_by: user_id` references in the repository are within historical review-findings files (this iteration's summary/per-area write-ups), which are preserved verbatim.

---

### POL-026 `circuit_breaker.state_changed` audit event is not catalogued in §16.7 [Medium]

**Section:** `spec/11_policy-and-controls.md:310`, `spec/16_observability.md:620-642` (§16.7 audit event catalogue).

`spec/11_policy-and-controls.md:310` declares that every breaker state change emits a `circuit_breaker.state_changed` audit event with payload `{circuit_name, old_state, new_state, reason, operator_user_id, timestamp}`. The last sentence of the same paragraph says:

> "Both events are written to the append-only audit tables ([§11.7](#117-audit-logging)) and appear in the catalogued audit event list in [§16.7](16_observability.md#167-section-25-audit-events)."

But `grep circuit_breaker.state_changed spec/16_observability.md` returns zero matches. The §16.7 catalogue (lines 620-642) enumerates `admission.circuit_breaker_rejected` (line 627) and `admission.circuit_breaker_cache_stale` (line 628), but not `circuit_breaker.state_changed`. The iter4 POL-018 resolution text explicitly flagged this: "the `circuit_breaker.state_changed` state-change event in the same sentence should also be cross-referenced to §16.7 (currently §16.7 line 566 does not enumerate it, which is a separate gap worth filing if §16.7 intends to be comprehensive)." No iter5 finding has carried it through.

The practical consequence matches the POL-018 rationale: a deployer wiring SIEM consumers against §16.7 as the authoritative catalogue will omit the state-change event and receive unexpected OCSF records at runtime, or will write an alerting rule that never fires because they did not know the event shape existed. The catalogue is explicitly referenced from §11.6 line 310 as authoritative, and the omission produces the exact same cross-section drift pattern that POL-018 solved for the rejection event.

Medium severity (catalogue completeness on an authoritative cross-reference — same pattern as POL-018).

**Recommendation:** Add a `circuit_breaker.state_changed` entry to the §16.7 audit event catalogue between `admission.circuit_breaker_rejected` (line 627) and `admission.circuit_breaker_cache_stale` (line 628). Suggested text:

> - `circuit_breaker.state_changed` ([§11.6](11_policy-and-controls.md#116-circuit-breakers)) — emitted on every operator-managed circuit-breaker state transition via `POST /v1/admin/circuit-breakers/{name}/open` or `.../close`. Payload fields: `circuit_name`, `old_state` (`open` \| `closed`), `new_state` (`open` \| `closed`), `reason` (free-text from the admin API body for `open` transitions; platform-generated "operator close" for `close` transitions), `limit_tier` (breaker scope — `runtime` \| `pool` \| `connector` \| `operation_type`; see POL-023 for body schema), `scope` (tier-specific matcher), `operator_sub`, `operator_tenant_id` (POL-025 naming), `timestamp`. Not sampled (state transitions are rare; one row per admin action).

The recommendation assumes POL-023 and POL-025 are also adopted; if either is deferred, trim the corresponding fields from the §16.7 entry to match the state actually pinned in §11.6.

**Status:** Fixed

**Resolution:**
- `spec/16_observability.md` §16.7: inserted a new `circuit_breaker.state_changed` bullet between `admission.circuit_breaker_rejected` and `quota_failopen_started` (which already sits between the rejection and cache-stale entries), immediately adjacent to the other circuit-breaker catalogue entries so SIEM operators find the complete operator-managed breaker lifecycle (state change + admission rejection + cache-stale fallback) in one contiguous block. Payload fields: `circuit_name`, `old_state` (`open` \| `closed`), `new_state` (`open` \| `closed`), `reason`, `limit_tier` (closed `runtime`|`pool`|`connector`|`operation_type` vocabulary, sourced from the persisted `cb:{name}` `limit_tier` so state-change rows share the `limit_tier` pivot with the rejection rows and the `lenny_circuit_breaker_rejections_total` metric label), `scope` (tier-specific matcher, same shape as the admin API body and Redis value), `operator_sub`, `operator_tenant_id`, `timestamp`. Marked **Not sampled** (one row per admin action, every transition security-salient regardless of storm volume) to distinguish the discipline from the adjacent high-volume rejection/cache-stale events. Cross-references §11.6 Storage and propagation for the operator-identity vocabulary alignment across Redis / CloudEvents / audit surfaces, and §11.7 for the append-only audit write path.
- `spec/11_policy-and-controls.md:319` ("Audit events." in §11.6): extended the `circuit_breaker.state_changed` inline payload listing to include `limit_tier` and `scope` so the §11.6 subsystem home matches the §16.7 authoritative catalogue entry. This closes the side-effect drift that would otherwise emerge from POL-023 (which introduced `limit_tier`/`scope` on the Redis value and rejection event) without a corresponding propagation to the state-change event payload listing. The sentence now explicitly sources `limit_tier` from the persisted `cb:{name}` and matches `scope` to the admin API body shape.

Docs check: `docs/operator-guide/observability.md` and `docs/reference/metrics.md` do not enumerate audit events (only metrics and alerts), and `docs/reference/error-catalog.md` references audit events by name only in the `PLATFORM_AUDIT_REGION_UNRESOLVABLE` row without a per-event catalogue — no documentation updates required for this fix. `docs/api/admin.md:751` already cross-references the `circuit_breaker.state_changed` audit event under the admin API endpoint description (added by POL-023's fix), so the operator-facing API doc correctly points at the now-catalogued §16.7 entry.

---

### POL-027 §11.6 "Running replica, Redis outage" bullet uses "REJECT / non-REJECT" vocabulary while the paired metric / audit event use `rejected` / `admitted` [Low]

**Section:** `spec/11_policy-and-controls.md:289`, `spec/16_observability.md:206`, `spec/16_observability.md:628`.

The POL-019 fix added the "Admission-path Redis-outage posture" paragraph with a bullet at line 289 that reads:

> "Each stale-serve admission decision (**both REJECT and non-REJECT outcomes**) increments `lenny_circuit_breaker_cache_stale_serves_total` and is covered by the sampled `admission.circuit_breaker_cache_stale` audit event (§16.7)..."

The paired metric at `spec/16_observability.md:206` uses `outcome`: `rejected` \| `admitted`. The paired audit event at `spec/16_observability.md:628` uses `outcome` (`admitted` \| `rejected`). The §11.6 bullet describes the same dimension with two different token conventions ("REJECT / non-REJECT" — uppercase, prose) while §16.1 and §16.7 use lowercase enum values (`rejected` / `admitted`). A deployer reading the §11.6 bullet for the metric label has to translate "non-REJECT" → `admitted`, which is not an obvious rename (it is not "non-rejected" or "accepted" — the spec elsewhere uses `admitted`).

Similar pattern to POL-022/POL-024 (vocabulary drift across sibling sections), Low severity, consistent anchoring.

**Recommendation:** Rewrite the parenthetical on `spec/11_policy-and-controls.md:289` to use the enum values. Suggested edit:

> "Each stale-serve admission decision (both `outcome="rejected"` and `outcome="admitted"` — the latter being the security-salient case where the admission path could not verify a breaker's current state and served a non-rejection against a stale view) increments `lenny_circuit_breaker_cache_stale_serves_total` (`outcome` label) and is covered by the sampled `admission.circuit_breaker_cache_stale` audit event (§16.7)..."

This aligns the prose with the metric and audit labels and eliminates the "non-REJECT" term.

---

### POL-028 `admission.circuit_breaker_cache_stale` sampling discipline is specified only in §16.7, not in §11.6 [Low]

**Section:** `spec/11_policy-and-controls.md:287-293` (Admission-path Redis-outage posture paragraph), `spec/16_observability.md:628` (audit event catalogue entry).

§11.6 line 312 ("Sampling under breaker storms") specifies the sampling discipline for `admission.circuit_breaker_rejected` — first rejection per `(tenant_id, circuit_name, caller_sub)` per rolling 10 s window per replica. The POL-019 fix added an adjacent sibling event, `admission.circuit_breaker_cache_stale`, that is described as "sampled" at line 289 ("the sampled `admission.circuit_breaker_cache_stale` audit event") but whose sampling key is specified **only in §16.7**:

> §16.7 line 628: "Sampled per replica at the first stale-serve per `(tenant_id, caller_sub, outcome)` tuple within any rolling 10-second window (same discipline as `admission.circuit_breaker_rejected`)..."

Note the sampling keys differ — `admission.circuit_breaker_rejected` keys on `(tenant_id, circuit_name, caller_sub)` (circuit_name present); `admission.circuit_breaker_cache_stale` keys on `(tenant_id, caller_sub, outcome)` (no circuit_name — by design, since cache-stale is orthogonal to a specific circuit, and `outcome` is the security-salient discriminator). This is a principled distinction, but §11.6 does not state it. A reader using §11.6 as the subsystem home (reasonable — §11.6 IS the circuit-breaker subsystem's home and iter3/iter4 carried that framing) will miss the cache-stale sampling rule entirely and either (a) assume the rejection rule applies (wrong key — includes `circuit_name`), or (b) conclude there is no sampling and expect every stale-serve to produce an audit row (wrong — the rule matches the rejection-storm discipline by design).

The POL-018 fix established the pattern "§11.6 is consistent with §16.7 for per-event payload schemas; §16.7 is authoritative for schema details". The sampling-key specification is symmetrical and should follow the same pattern.

Low severity (documentation completeness; the behavior is pinned in §16.7 and the metrics correctly reflect it — but the home-section reader experience is incomplete).

**Recommendation:** Extend `spec/11_policy-and-controls.md:289` to state the sampling key explicitly, or add a sentence after the bullet list at line 292 along the lines of:

> "The `admission.circuit_breaker_cache_stale` audit event follows the same per-replica 10 s sampling discipline as `admission.circuit_breaker_rejected` (§11.6 Sampling under breaker storms), with a different key: the first stale-serve per `(tenant_id, caller_sub, outcome)` tuple per replica per window is written; subsequent stale-serves in the window increment `lenny_circuit_breaker_cache_stale_serves_total` but do not write individual rows. `circuit_name` is not part of the cache-stale sampling key because the event describes the cache as a whole, not a specific breaker — see §16.7 for the authoritative payload schema."

---

## Convergence assessment

Iter5 on the Policy Engine & Admission Control perspective **does not converge**. Six findings remain:

- **1 High:** POL-023 (admin API body schema does not let the operator declare breaker scope; policy-evaluation correctness hole).
- **2 Medium:** POL-025 (operator-identity vocabulary drifts across three circuit-breaker surfaces; POL-018 half-fix), POL-026 (`circuit_breaker.state_changed` missing from the §16.7 catalogue; iter4 POL-018 resolution explicitly flagged it as worth filing).
- **3 Low:** POL-024 (iter4 POL-022 carry-forward; prose/phase vocabulary drift), POL-027 (REJECT / non-REJECT vs. `rejected`/`admitted` token mismatch in the POL-019 bullet), POL-028 (cache-stale sampling discipline absent from §11.6 home section).

POL-023 is the load-bearing blocker: without a scope-declaration field in the admin API body, the `AdmissionController` cannot deterministically evaluate "does this breaker apply to this request?", and the `limit_tier` label introduced by iter3 POL-017 has no authoritative input path. POL-025 and POL-026 are catalogue-drift follow-ons in the same POL-018 family. POL-024/POL-027/POL-028 are iter-carry-forward polish at consistent severity with their iter4 siblings.

Iter4 Fixed items (POL-018, POL-019, POL-020) remain correctly fixed and their cross-section edits in §16.1/§16.5/§16.7 check out against the §11.6 references.

---

# Iter5 Review — Perspective 25: Execution Modes & Concurrent Workloads

**Scope:** `executionMode` (session/task/concurrent) state-machine correctness, concurrent-workspace slot semantics, task-mode retirement policy, pool-scaling implications.

**Primary source files examined:**
- `spec/06_warm-pod-model.md` §6.2 (pod state machine) and §6.1 (preConnect/SDK-warm interactions).
- `spec/05_runtime-registry-and-pool-model.md` §5.2 (Pool Configuration and Execution Modes), "Execution Mode Scaling Implications".

**Iter4 baseline:** EXM-009 through EXM-012.
- **EXM-009** (scrub_warning re-warm schedulability precondition, Medium) — **confirmed Fixed** via WPL-004 cascade. `spec/06_warm-pod-model.md:153` adds `task_cleanup → draining [scrub_warning]` fallback; `:155` carries `host node is schedulable` guard on `task_cleanup → sdk_connecting [scrub_warning]`; the `:181` "Host-node schedulability precondition" paragraph now explicitly states "The rule applies identically to the scrub-success and scrub-warning preConnect edges". No regression.
- **EXM-010, EXM-011, EXM-012** — remain unfixed in the current spec and are re-raised below at their iter4 severities (Low), per severity-calibration guidance.

Severity rubric applied (per iter5 instructions):
- **Critical/High:** concurrency-correctness bugs in workspace/stateless/task modes only.
- **Medium:** incomplete mode specification with a documented workaround.
- **Low/Info:** polish, guard symmetry, deployer-facing ambiguity where a prose clarification exists elsewhere in the spec.

No new C/H-class findings surfaced in this perspective's scope for iter5. The three persisting Low-severity items were re-verified against the current spec text.

---

### EXM-013. `cancelled → task_cleanup` transition still does not define retirement-counter increment (iter4 EXM-010 persists) [Low]

**Section:** `spec/06_warm-pod-model.md` §6.2 (line 146); `spec/05_runtime-registry-and-pool-model.md` §5.2 "Task-mode pod retirement policy" (lines 447–453).

Line 146 of §6.2 still reads: `cancelled ──→ task_cleanup (cancellation acknowledged — pod runs scrub, then proceeds to idle or draining per normal task_cleanup rules)`. Neither this arrow nor §5.2's retirement-policy bullet list states whether a cancelled task increments the pod's completed-task count for `maxTasksPerPod`. §5.2:449 specifies the retirement trigger as "The pod's **completed** task count reaches `maxTasksPerPod`" — the literal reading excludes cancellations, which would let a cancellation-heavy workload silently serve many more than `maxTasksPerPod` tasks per pod, defeating the explicit deployer reuse-limit choice that §5.2:471 describes as "forces deployer choice". The same ambiguity applies to whether scrub failures during cancellation cleanup count toward `maxScrubFailures`, and whether the preConnect re-warm rules at lines 152–155 fire on the cancelled→task_cleanup path the same way they do on a natural completion. Iter4 raised this as EXM-010 with an identical recommendation; no edit landed.

**Recommendation:** Apply the iter4 EXM-010 fix verbatim. Add a companion sentence to §6.2 line 146 or to the §5.2 retirement-policy bullet list: *"Cancelled tasks DO count toward `maxTasksPerPod` — a cancellation that reaches `task_cleanup` is equivalent to a completion for retirement-counter purposes, since scrub runs regardless of task outcome. Scrub failures during cancellation cleanup count toward `maxScrubFailures` identically to post-completion scrub. The preConnect re-warm rules (§6.2 lines 152–155) apply uniformly: a cancelled task on a preConnect pool routes through `sdk_connecting` if the standard guards pass."* Explicit "DO count" vs "do NOT count" is a deployer-facing choice; either answer is defensible, but the spec must commit.

---

### EXM-014. Retirement-config-change staleness on `mode_factor` unaddressed (iter4 EXM-011 persists) [Low]

**Section:** `spec/05_runtime-registry-and-pool-model.md` §5.2 "Execution Mode Scaling Implications" → "Caveats" bullet, lines 569 (task-mode `mode_factor` convergence) and 547 (formula assumption).

Line 569 still reads: "converges toward `maxTasksPerPod` over time … falls back to `mode_factor = 1.0` … until sufficient samples are collected (default: 100 completed tasks). Once converged, `mode_factor` is bounded above by `maxTasksPerPod`." The bound is applied only at convergence — not dynamically on a deployer edit to `maxTasksPerPod`, `maxScrubFailures`, or `maxPodUptimeSeconds`. When a deployer tightens `maxTasksPerPod` from 50 → 10 (a security-posture hardening, which is the primary reason to edit this field at all), the PoolScalingController continues to size against a stale `mode_factor ≈ 50` — underprovisioning the pool by up to 5× relative to the new target until 100 fresh samples arrive, which at low request rates is hours of exposure. The iter4 recommendation was not applied; line 569 is unchanged.

**Recommendation:** Apply iter4 EXM-011's fix. Add to §5.2:569 (or as a dedicated "Config-change response" sentence immediately after): *"On deployer config changes to `maxTasksPerPod`, `maxScrubFailures`, or `maxPodUptimeSeconds`, the PoolScalingController immediately clamps `mode_factor ← min(mode_factor_current, maxTasksPerPod_new)` and resets the observed-sample window so subsequent pod cycles re-converge against the new retirement limits."* Equivalently: hard-clamp `mode_factor ≤ maxTasksPerPod` on every scaling evaluation (not only at convergence). Either formulation closes the staleness window without invalidating the 100-sample convergence mechanism for steady-state operation.

---

### EXM-015. `attached → failed` transition still lacks symmetric retries-exhausted guard (iter4 EXM-012 persists) [Low]

**Section:** `spec/06_warm-pod-model.md` §6.2 "Task-mode state transitions (from attached)", lines 144 and 145.

The task-mode fragment still defines two transitions from `attached` with overlapping triggers and asymmetric guards:
- Line 144: `attached ──→ failed (pod crash / node failure / unrecoverable gRPC error during active task)` — no guard.
- Line 145: `attached ──→ resume_pending (pod crash / gRPC error during active task, retryCount < maxTaskRetries)` — explicit `retryCount < maxTaskRetries` guard.

The prose at lines 185–190 ("Pod crash during active task-mode task") clarifies that line 144 is the retries-exhausted / non-retryable branch and line 145 is the retries-remain branch, but the diagram itself is ambiguous without reading the follow-on prose. Other fragments in the same §6.2 diagram consistently carry symmetric guards on both sides of a retry-split pair (e.g., lines 102–103 for `starting_session`, lines 126–127 for `input_required`, lines 130–131 for `resuming`). Task-mode is the sole outlier. Iter4 EXM-012 flagged this with a one-line recommendation; no edit landed.

**Recommendation:** Apply iter4 EXM-012's fix verbatim. Replace line 144 with: `attached ──→ failed (pod crash / node failure / unrecoverable gRPC error during active task, retries exhausted or non-retryable)`. Matches the pattern used on lines 102, 114, 118–119, 127, 131, and eliminates the ambiguity without changing intended behavior.

---

## Convergence assessment

**Perspective 25 is one iter from convergence** for this scope. All three open findings (EXM-013/014/015) are iter4 persistences at Low severity with concrete, single-line or single-sentence recommendations that were not applied in iter4's fix pass. No new Critical/High/Medium findings surfaced on re-examination of §5.2 (execution modes, slot atomicity, scaling implications) or §6.2 (state machine, preConnect interactions, concurrent-workspace lifecycle). EXM-009 was resolved as a clean side-effect of WPL-004 (schedulability-label propagation to both scrub-success and scrub-warning edges) — the associated §6.2 paragraph at line 181 explicitly documents the uniform rule.

Blockers to declaring convergence on this perspective:
1. The three Low findings above must land a fix (or an explicit "accepted risk / will-not-fix" disposition). All three are one-edit fixes; none require architectural change.
2. No docs/ reconciliation implications from these fixes — the changes are contained to §5.2 prose and §6.2 state-diagram guards. Per `feedback_docs_sync_after_spec_changes`, a brief scan of any `docs/` execution-mode references should still be performed once the edits land, but nothing in this perspective's scope drives a cross-file sync at this iter.

If iter6 applies the three fixes without regression, perspective 25 converges. If any of them is deferred again without disposition, the same findings re-raise at Low in iter6 with no severity drift.

---

## Cross-Cutting Themes

**1. §16.5 alert-catalog drift is the dominant iter5 theme.** Seven of the 26 C/H/M findings (OBS-031, OBS-032, OBS-033, OBS-034, OBS-035, OBS-036, FMR-018) are instances of the same pattern: a section references an alert or a metric-backed operator signal by name, but §16.5 (the single source of truth for `PrometheusRule` rendering) does not contain a corresponding row. Two of these (OBS-031/032) are direct regressions of iter4 OBS-023, which was scoped to sweep all sections for this pattern. The iter4 sweep missed §9.4 (OBS-033), §12.8 (OBS-034), §17.7 line 740/760 (OBS-031/032), the §16.5 body text vs. severity column split (OBS-035), and §16.1 line 152 (OBS-036). Root cause: the fix did not include a CI or script-level check that every named alert reference in the spec resolves to a §16.5 row; every iteration since iter3 has found new instances. **Recommended remediation:** adopt an automated cross-reference check in CI that greps every `\`[A-Z][A-Za-z]+\`` alert-name-shaped token in spec/*.md against the §16.5 rule-name column.

**2. Residency gap extends into the iter4 T4 Platform tier additions.** Three iter5 findings (CMP-054, CMP-057, CMP-058) land in the compliance perspective and all stem from incomplete T4-tier generalization of iter3/iter4 residency fixes. CMP-054 (escrow bucket platform-singular) and CMP-058 (platform-tenant audit events regional routing) are "what iter4 added to T4 but did not thread through the per-region storage model" issues. CMP-057 (profile downgrade ratchet missing) is a parallel pattern to the §12.9 "stricter, never looser" rule for `workspaceTier` — the residency/compliance profile field did not inherit the monotonicity guarantee. **Recommended remediation:** add a profile-monotonicity invariant to the `complianceProfile` admin API handler and thread the per-region escrow/audit-residency map through the T4 storage topology.

**3. `lenny-ops` bundled addon lacks build-sequence treatment.** BLD-012 (High) and BLD-014 (Medium) both surface gaps in §18/§17.9 handling of the iter4 `lenny-ops` bundled sub-chart. `lenny-ops` is mandatory from the first chart install (Phase 3.5) yet has no phase assignment; `lenny-preflight` has a `lenny-ops-sa` RBAC check row with no conditional guard that matches the missing phase. **Recommended remediation:** add an explicit Phase 3.5 row for `lenny-ops` in §18, gate the §17.9 preflight check accordingly, and list `lenny-ops` alerting rules as a Phase 13 observability deliverable (BLD-015 Low).

**4. Admission-control admin API is structurally under-specified for scoping.** POL-023 (High) surfaces that the circuit-breaker open/close admin API body has no scope field, so the operator cannot declare which scope (`runtime` / `pool` / `connector` / `operation_type`) the breaker applies to. Combined with POL-025 (operator-identity field name drift across three surfaces) and POL-026 (missing `circuit_breaker.state_changed` in §16.7 catalog), the admin-side operability of the circuit-breaker subsystem has several smaller gaps left over from the iter3 POL-017 / iter4 POL-018 work. **Recommended remediation:** batch all four POL-023/024/025/026 fixes together, treating the admin API body as the root schema and cascading identity/audit-event consistency from there.

**5. Workspace plan (`WorkspacePlan`) JSON Schema completeness is a running polish theme.** CNT-014, CNT-015, CNT-016 surface at Medium/Low in iter5 under Perspective 18 and extend the iter4 CNT-007 fix (host-agnostic SSH URL) and CNT-011 (per-variant strictness). The common pattern: the published JSON Schema has no or loose constraints on operator-settable fields (`*.mode` string format; `gitClone.ref` reproducibility semantics; `schemaVersion` minimum) that are normatively implied but not machine-checkable. **Recommended remediation:** do one pass over the `WorkspacePlan` JSON Schema adding all missing constraints, then regenerate the Published JSON Schema output so validators reject on parse.

**6. Anchor-integrity regressions continue to appear in fix PRs.** DOC-019, DOC-020, DOC-021 (all Medium) are three new broken anchor references introduced by iter4 NET-067 / §25.9 audit-query / §25.12 MCP Management Server edits. This is the same failure pattern as iter3 CPS-004 (which introduced what iter3 DOC-008 had closed) and iter4 DOC-014/015. Iter5 p22_document.md flags this as a process gap, not a content gap: the iter4 fix's self-verification note claimed anchors resolved when they did not. **Recommended remediation:** add a Python-script-level anchor-resolution check to CI that matches every `](file.md#anchor)` against the heading-derived slug set of the referenced file. Until this gate exists, every iteration should expect 2–4 new anchor regressions introduced by non-DOC fixes.

---

## Convergence Trajectory

Iter5 has 0 Critical (stable vs. iter4), 7 High (down from iter4's 26), 19 Medium (down from iter4's 74). The High count reduction is driven by the iter4 sweep that closed 19 of 26 High items; the 7 remaining High are all new or second-order items surfaced by iter4 fixes (OBS-031/032 regression of OBS-023; CMP-054/057 from T4 generalization; CRD-015 from credential deny-list implementation detail; BLD-012 from lenny-ops addon introduction; POL-023 from admin API expansion). Medium count reduction (74→19) reflects both genuine iter4 closures and severity calibration discipline in iter5.

Perspectives that converged this iteration (0 C/H/M): 1, 2, 4, 5, 6, 7, 8, 10, 11, 14, 15, 16, 23, 25 (14 of 25).

Perspectives that did not converge: 3 (NET-070 Medium), 9 (3 Medium), 12 (2 High + 4 Medium), 13 (2 High + 1 Medium), 17 (1 High), 18 (2 Medium), 19 (1 High + 1 Medium), 20 (1 Medium), 21 (1 Medium), 22 (3 Medium), 24 (1 High + 2 Medium).

**Recommendation for iter5 fix phase:** the 7 High + 19 Medium are well-scoped and amenable to the standard `/fix-findings` procedure with critical challenge. Apply severity calibration — some Medium items (e.g., POL-024, POL-027, POL-028) should be challenged as potential Low if their impact is polish-only. Docs sync is required after fixes because OBS-031/032/033/034/035/036 (alert additions), CMP-054/057/058 (residency), and POL-025/026 (operator-identity) all have `docs/reference/metrics.md` and `docs/operator-guide/*` downstream impact.

