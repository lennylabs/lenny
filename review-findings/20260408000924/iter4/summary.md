# Technical Design Review Findings — 2026-04-08 (Iteration 4)

**Document reviewed:** `technical-design.md` (~10,403 lines)
**Review framework:** `review-povs.md` (25 perspectives)
**Iteration:** 4 of 8 — continuation from iteration 3
**Total findings:** 439 across 25 review perspectives

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 7     |
| Medium   | 255   |
| Low      | 177   |

### Carried Forward (skipped)

| # | ID | Finding | Status |
|---|------|---------|--------|
| 1 | K8S-035 / NET-034 | `lenny-pool-config` ghost webhook | Skipped |
| 2 | WPL-030 | Failover formula 25s — intentionally conservative | Skipped |
| 3 | DEL-039 | `settled=all` redundant mode | Skipped |
| 4 | FLR-038 | Redis runbook phantom metrics | Skipped |
| 5 | CMP-041 | Salt rotation cannot re-pseudonymize | Skipped |
| 6 | POL-041 | Cross-phase priority ordering | Skipped |
| 7 | MSG-037 | `delivery_receipt` schema | Skipped |
| 8 | CRD-031/032 | Secret shape table missing rows | Skipped |
| 9 | DOC-036 | Orphaned footnote | Skipped |
| 10 | CPS-043 | No sustainability model | Skipped |
| 11 | CPS-048 | Kubernetes adoption barrier | Skipped |

### High Findings

| # | ID | Perspective | Finding |
|---|------|-------------|---------|
| 1 | SEC-082 | Security | DELETE /v1/credentials/{credential_ref} does not revoke active leases |
| 2 | NET-058 | Network | No network-level isolation between tenants in shared agent namespace |
| 3 | PRT-060 | Protocol | schemaVersion round-trip loss through protocol adapters |
| 4 | PRT-072 | Protocol | UNREGISTERED_PART_TYPE rejection breaks forward-compatibility |
| 5 | OPS-080 | Operator | Multiple failurePolicy: Fail webhooks create cascading unavailability |
| 6 | TNT-057 | Multi-Tenancy | Cloud-managed pooler tenant_guard trigger single point of failure |
| 7 | WPL-063 | Warm Pool | SandboxClaim guard webhook failurePolicy: Fail creates SPOF |

---

## Detailed Findings by Perspective

---

## 1. Kubernetes Infrastructure (K8S)

### K8S-056. PoolScalingController reconciliation from Postgres has no rate-limiting against admin API write storms [Medium]

The PoolScalingController reconciles pool configuration from Postgres into CRDs (Section 4.6.2). It watches `pool_config_generation` for changes. However, the spec does not define any rate-limiting or debouncing on the Postgres-to-CRD reconciliation path. If an operator rapidly updates pool configuration via the admin API (e.g., automated tooling in a loop), each generation increment triggers a CRD write. Unlike the WarmPoolController which has `statusUpdateDeduplicationWindow`, the PoolScalingController has no equivalent mechanism. At scale, a burst of admin API writes could produce a burst of CRD spec updates, conflicting with the WarmPoolController's status updates on the same resources and generating SSA conflict storms. The spec should define a reconciliation debounce interval for the PoolScalingController.

---

### K8S-057. `agent_pod_state` Postgres mirror table has no specified update mechanism for concurrent-workspace slot counts [Medium]

Section 4.6.1 states the `agent_pod_state` table is a "Postgres-side mirror of `Sandbox` CRD status, updated by the WarmPoolController on every state transition." Section 5.2 specifies concurrent-workspace slot counts are tracked in Redis (`lenny:pod:{pod_id}:active_slots`). The `agent_pod_state` mirror does not specify whether it includes per-pod active slot counts. The orphan session reconciler (Section 10.1) uses `agent_pod_state` to detect terminated pods, but for concurrent-workspace pods, a pod could be in `Terminated` phase with some slots still pending checkpoint. Without slot-level state in the mirror table, the reconciler cannot distinguish "all slots cleanly completed" from "slots were forcibly terminated." The mirror table schema should be specified to include slot-level metadata for concurrent-workspace pods.

---

### K8S-058. SSA field ownership table omits `SandboxClaim` RBAC for the gateway but lists it as gateway-owned [Medium]

Section 4.6.3 declares `SandboxClaim` as owned by "Gateway (not a controller)" for both `spec.*` and `status.*`. However, SSA enforcement requires a field manager name. The spec defines field managers only for `lenny-warm-pool-controller` and `lenny-pool-scaling-controller`. It does not specify a field manager name for the gateway when it creates/updates `SandboxClaim` resources. Without a named field manager, the gateway's `SandboxClaim` operations may use the default field manager, which could silently overlap with other principals that share the default. The spec should define a gateway-specific SSA field manager (e.g., `lenny-gateway`).

---

### K8S-059. WarmPoolController's Node RBAC grant for CIDR drift detection creates a broad read surface [Medium]

Section 4.6.3 grants the WarmPoolController `get`/`list` on `Nodes` for CIDR drift detection. This grants the controller read access to all Node objects cluster-wide, including node labels, annotations, conditions, and addresses. While the controller only needs `spec.podCIDR`, Kubernetes RBAC cannot restrict to specific fields. A compromised controller could enumerate all nodes and their metadata. The spec acknowledges RBAC is coarse-grained but does not mention this as an accepted risk for the WPC specifically. At minimum, the CIDR drift goroutine should be documented as a security-sensitive component whose compromise scope includes full node metadata read access.

---

### K8S-060. PDB for warm pods uses label selector `lenny.dev/state: idle` which changes on claim [Medium]

Section 4.6.1 states the PDB "targets only unclaimed (warm) pods via a label selector (`lenny.dev/state: idle`)." When a pod is claimed, its state label changes from `idle` to `active`. The PDB's `podSelector` dynamically shrinks as pods are claimed, which is correct behavior. However, during a node drain, if all pods on a node happen to be in `active` state (claimed), the PDB provides no protection for those pods at all. The preStop hook is the protection mechanism for active pods, but the PDB cannot prevent the kubelet from evicting the pod if the `terminationGracePeriodSeconds` is insufficient. The spec should clarify that the PDB is exclusively for warm pods and that active pods rely entirely on the preStop checkpoint mechanism, and that node drain timeouts must accommodate `terminationGracePeriodSeconds`.

---

### K8S-061. `SandboxClaim` orphan detection queries all claims every 60 seconds with no pagination [Medium]

Section 4.6.1 describes the orphan detection loop listing "all `SandboxClaim` resources whose `metadata.creationTimestamp` is older than `claimOrphanTimeout`." At Tier 3 with 10,000 concurrent sessions, there could be 10,000+ `SandboxClaim` resources. A `list` call with no `limit` or field selector on the API server returns all objects, which at scale consumes significant API server memory and etcd bandwidth. The spec should specify paginated listing (using `limit` and `continue` tokens) for orphan detection at Tier 3, or filtering via label selectors that pre-filter to likely orphan candidates.

---

### K8S-062. Controller work queue max depth default (500) may be too low for Tier 3 cold-start scenarios [Medium]

Section 4.6.1 sets the work queue max depth to 500 (configurable). At Tier 3 with 10,000 sessions and cluster recovery from zero, every `Sandbox` resource generates a reconciliation event. If the cluster has 500+ warm pool targets across all pools and they all need reconciliation simultaneously, the queue overflows and events are dropped (`lenny_controller_queue_overflow_total` incremented). While dropped events are re-enqueued on the next list-sync, this creates a reconciliation delay proportional to the list-sync interval. The per-tier controller tuning table in Section 17.8.2 should explicitly recommend a queue depth of at least `2 * max(total_minWarm_across_all_pools)` for Tier 3.

---

### K8S-063. `statusUpdateDeduplicationWindow` with trailing-write semantics can mask intermediate failure states [Medium]

Section 4.6.1 specifies that `statusUpdateDeduplicationWindow` uses "trailing" semantics: "the last observed status in the window is always written (no status changes are lost, only intermediate writes are suppressed)." However, if a pod transitions `idle -> claimed -> failed` within a single dedup window (e.g., 500ms at Tier 1/2), the `idle -> claimed` intermediate status is never written to etcd. This means any monitoring or alerting that depends on observing the `claimed` state (e.g., claim rate metrics derived from CRD watch events) will undercount claims. The spec should clarify that deduplication affects status-derived watch event observability and that metrics based on CRD watch events may undercount transient states.

---

### K8S-064. Fallback claim path via Postgres creates a Postgres write on the pod claim hot path [Medium]

Section 4.6.1 describes a fallback claim path that queries `agent_pod_state` with `SELECT ... FOR UPDATE SKIP LOCKED` and then creates a `SandboxClaim` CRD. This fallback path introduces a Postgres write (`FOR UPDATE` acquires a row lock) on the session creation hot path, which was designed to be API-server-only. At Tier 3 with API server pressure (the condition that triggers the fallback), the Postgres-backed path could itself become a contention point if many gateways simultaneously fall back. The spec should specify a concurrency limit on fallback claims per gateway replica to prevent Postgres connection pool saturation during API server degradation.

---

### K8S-065. Concurrent WarmPoolController and PoolScalingController failover analysis assumes 25s max but does not account for API server admission delays [Medium]

Section 4.6.1 states both controllers use identical lease parameters and the worst case is 25s. Section 4.6.1 also notes `podClaimQueueTimeout` (60s) "provides a 35-second margin above the 25s worst-case failover window, accommodating API server slowness." However, the failover analysis for simultaneous controller failure only considers the additive case, not the scenario where the API server itself is degraded (high latency on Lease renewal). If API server latency adds 10-15s to lease renewal, the effective failover window could exceed 35-40s, consuming most of the 60s `podClaimQueueTimeout` margin. The spec should quantify the API server latency budget that the 35s margin accommodates.

---

### K8S-066. CRD validation webhook for `SandboxWarmPool` runs pool-specific validation but webhook scope is unspecified [Medium]

Multiple sections reference CRD validation webhooks rejecting invalid pool configurations (cleanup timeout floor in Section 5.2, tiered checkpoint cap in Section 10.1, `terminationGracePeriodSeconds` constraints). However, the spec does not define whether these are OpenAPI schema CEL rules on the CRD (mentioned in Section 4.6.1 for basic rules like `minWarm <= maxWarm`) or separate `ValidatingAdmissionWebhook` resources. Complex cross-field validations (e.g., `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 <= terminationGracePeriodSeconds`) cannot be expressed as CEL rules. The spec should explicitly state which validations use CRD-level CEL and which require a separate admission webhook, and specify the failure policy and availability requirements for each.

---

### K8S-067. `lenny-label-immutability` webhook allows gateway SA to set `lenny.dev/tenant-id` but does not restrict which tenant ID values the gateway can write [Low]

Section 4.6.3 states the webhook "allows the gateway SA to set `lenny.dev/tenant-id` on initial assignment." The webhook validates the principal (gateway SA) and the transition (`unset -> {tenant_id}`), but it does not validate that the tenant ID being written matches the session's actual tenant. The gateway is trusted to write the correct value, but a compromised gateway could write an arbitrary tenant ID. Since the gateway already has broad access to session state and Postgres, this is a minor additional risk, but the spec should note this as an accepted trust boundary.

---

### K8S-068. `lenny-drain-readiness` webhook requires egress from admission-webhook pods to gateway internal port but the dependency is fragile [Medium]

Section 13.2 (NET-037) specifies that the `lenny-drain-readiness` admission webhook needs egress to the gateway's internal HTTP port (8080) to call `GET /internal/drain-readiness`. This creates a circular dependency: the webhook validates pod evictions, but the webhook itself depends on the gateway being reachable. If the gateway is the component being drained, the webhook must call a potentially-draining gateway to determine readiness. The spec should clarify whether the drain-readiness callback targets the specific replica being drained (which would fail if that replica's readiness is already false) or any healthy replica (which requires the webhook to resolve the Service, not a specific pod IP).

---

### K8S-069. Preflight Job checks agent-sandbox CRD names with `lenny.dev` domain but CRDs are from `kubernetes-sigs/agent-sandbox` [Medium]

Section 17.6 states the preflight Job verifies CRDs named `sandboxtemplates.lenny.dev`, `sandboxwarmpools.lenny.dev`, `sandboxes.lenny.dev`, `sandboxclaims.lenny.dev`. However, Section 4.6.1 describes these as `kubernetes-sigs/agent-sandbox` CRDs. If Lenny forks or bundles these CRDs under its own API group (`lenny.dev`), this is consistent. But if Lenny uses the upstream CRDs directly, the API group would be from the upstream project (likely not `lenny.dev`). The spec should clarify whether these CRDs are bundled under Lenny's API group (fork) or used from the upstream group, as this affects the preflight check, RBAC grants, and the SSA field manager configurations.

---

### K8S-070. ResourceQuota preflight check compares pod limit against `minWarm` sum but does not account for active session pods [Medium]

Section 17.6 specifies the preflight check: "verify each agent namespace has a `ResourceQuota` and that the quota's pod limit >= sum of `minWarm` for pools targeting that namespace." This check is necessary but insufficient. During normal operation, the namespace contains both warm (idle) pods and active (claimed) pods. The total pod count is `sum(minWarm) + active_sessions`. At Tier 2 with 1,000 sessions, the ResourceQuota pod limit must be significantly higher than just `sum(minWarm)`. The preflight check should use `sum(minWarm) + sum(maxWarm)` or `sum(minWarm) + max_expected_sessions_per_namespace` as the floor.

---

### K8S-071. Helm chart does not render standalone HPA when KEDA is used, but the reverse guard is not specified [Low]

Section 10.1 (SCL-024) states: "When KEDA is used, do NOT deploy a standalone HPA for the same Deployment" and that the Helm chart enforces this by not rendering the HPA when `autoscaling.provider: keda`. However, the spec does not address the reverse scenario: if an operator switches from KEDA back to Prometheus Adapter (`autoscaling.provider: hpa`), the Helm chart would render the standalone HPA but the KEDA `ScaledObject` may still exist (if not removed by `helm upgrade`). The spec should clarify that switching `autoscaling.provider` from `keda` to `hpa` requires deleting the ScaledObject, or the Helm chart should render a cleanup hook.

---

### K8S-072. Topology spread constraints default to `ScheduleAnyway` (soft) which provides no zone-balance guarantee under node pressure [Low]

Section 5.2 sets default topology spread with `whenUnsatisfiable: ScheduleAnyway`. This is appropriate for warm pools where availability is prioritized over balance. However, the spec recommends deployers "set `whenUnsatisfiable: DoNotSchedule` to enforce strict spread" for HA pools but does not specify what happens during cold-start fill when strict spread is enabled. If only one AZ has available nodes (common during cluster autoscaler ramp-up), `DoNotSchedule` prevents pods from scheduling at all, causing the warm pool to fail to fill. The spec should note this interaction and recommend that strict spread be combined with an AZ readiness check or a `topologySpreadConstraints` minimum domain count.

---

### K8S-073. `lenny-sandboxclaim-guard` webhook referenced in component table but not specified in the admission webhook inventory [Medium]

Section 13.2's `lenny-system` component table lists `lenny-sandboxclaim-guard` alongside other admission webhooks. This webhook is not defined anywhere in the spec -- its purpose, validation rules, failure policy, and scoping are unspecified. The spec references `lenny-label-immutability`, `lenny-direct-mode-isolation`, `lenny-t4-node-isolation`, `lenny-data-residency-validator`, and `lenny-drain-readiness` with full specifications, but `lenny-sandboxclaim-guard` has no corresponding definition. This webhook needs a complete specification or should be removed from the component table.

---

### K8S-074. Controller RBAC grants `create`/`update`/`delete` on `PodDisruptionBudget` but PDB management lifecycle is only "optional" [Low]

Section 4.6.3 grants the WarmPoolController `create`/`update`/`delete` on `PodDisruptionBudget` "required for `ManagePDB`." Section 4.6.1 states the controller "can optionally create a PDB per `SandboxTemplate` for warm (unclaimed) pods." The RBAC grants are always present regardless of whether PDB management is enabled. While this is a minor over-provisioning (the controller just does not use the grants if PDB management is off), least-privilege principles suggest the RBAC should be conditional on PDB management being enabled. This is a Helm template concern -- the RBAC rules for PDB should be rendered only when `pools[].pdbEnabled: true` or similar.

---

### K8S-075. etcd write pressure estimates do not account for concurrent-workspace or task-mode CRD churn [Medium]

Section 4.6.1 estimates etcd writes per tier based on "~1 status write per pod per 2-minute lifetime (claim -> active -> released) plus warm-pool reconciliation writes." This estimate assumes session mode (one session per pod). In task mode, a single pod serves up to `maxTasksPerPod` tasks, generating state transitions per task (idle -> claimed -> active -> idle per task cycle). At `maxTasksPerPod: 50` with 2-minute tasks, a single pod generates ~25 status writes/minute vs ~0.5 writes/minute in session mode. In concurrent-workspace mode, slot assignments also generate status updates. The etcd write estimate table should include task-mode and concurrent-mode multipliers, especially for Tier 2/3 where these modes are likely.

---

### K8S-076. `ValidatingAdmissionWebhook` for CRD spec Postgres-authoritative state (Section 4.6.3) has no specified deployment topology [Medium]

Section 4.6.3 describes a validating webhook that "rejects manual `kubectl edit` or `kubectl apply` updates to `SandboxTemplate.spec` and `SandboxWarmPool.spec` fields unless the request's `userInfo` maps to the PoolScalingController ServiceAccount." This webhook runs in "Fail mode with a 5s timeout" -- meaning if unavailable, CRD updates are denied. However, this webhook is not listed in the component table (Section 13.2), has no HA specification (replica count, PDB), and is not included in the preflight validation checks. An unavailable webhook of this type would block the PoolScalingController itself from updating CRDs (the webhook must pass even for the allowed SA). The spec should give this webhook its own entry in the admission webhook inventory with HA, networking, and preflight requirements.

---

### K8S-077. Warm pool fill grace period resets on `minWarm` 0-to-positive transition but no mechanism prevents repeated resets [Low]

Section 4.6.1 states that the fill grace period "also applies whenever a pool's `minWarm` transitions from `0` to a positive value." An operator or automated system (e.g., the scale-to-zero schedule) that rapidly toggles `minWarm` between 0 and a positive value would repeatedly reset the grace period, permanently suppressing `WarmPoolLow` alerts for that pool. The spec should specify a maximum number of grace period resets within a rolling window, or an alert that fires when a pool's grace period has been reset more than N times in T minutes.

---

### K8S-078. Gateway ServiceAccount has `get`/`patch` on Pods in agent namespaces but label mutations use `patch` which bypasses SSA [Medium]

Section 4.6.3 grants the gateway `get`/`patch` on Pods in agent namespaces for tenant-id and state label mutations. The gateway uses `patch` (not SSA `Apply`) for these label mutations. Since `patch` does not go through SSA field ownership tracking, the gateway can modify any field on the pod spec reachable via a strategic merge patch, not just labels. While the webhook guards specific labels, a compromised gateway could `patch` other pod metadata (annotations, non-guarded labels) without SSA conflict detection. The spec should clarify whether the gateway uses strategic merge patch or SSA for pod label mutations, and if patch, document the additional webhook guards needed to prevent unauthorized metadata changes.

---

---

## 2. Security & Threat Modeling (SEC)

### SEC-065. Callback URL DNS Pinning Does Not Cover IPv6 Rebinding [Medium]

**Section 14, line 6617.** The callback URL validation uses DNS pinning with a check against private/reserved IPv4 ranges (RFC 1918, RFC 6598, loopback, link-local). However, the spec does not explicitly state that IPv6 unique-local addresses (ULA, `fc00::/7`) and IPv6 link-local addresses (`fe80::/10`) are also checked during DNS pinning validation. While the pinned IP is connected directly, an attacker could register an AAAA record pointing to an internal IPv6 address. The metadata endpoint list includes `fd00:ec2::254/128` but the general reserved-range check for IPv6 is not enumerated.

**Recommendation:** Explicitly enumerate IPv6 reserved ranges (ULA `fc00::/7`, link-local `fe80::/10`, loopback `::1/128`, and documentation ranges `2001:db8::/32`) in the DNS pinning validation alongside the IPv4 ranges.

---

### SEC-066. No Rate Limit on Webhook Delivery Retries Against Malicious Callback URLs [Low]

**Section 14, lines 6647-6652.** The webhook delivery model retries failed deliveries with exponential backoff (10s, 30s, 60s, 300s, 900s -- 5 attempts). If an attacker registers a callback URL that intentionally hangs for the full response-read timeout (10s) before returning a non-2xx status, each retry consumes a connection slot in the isolated callback worker pool for up to 10s. With many sessions, an adversary could create callback URLs designed to hold connections open, exhausting the callback worker pool. The spec mentions an "isolated callback worker" but does not specify a maximum pool size or per-session concurrent callback limit.

**Recommendation:** Specify a maximum callback worker pool size and a per-session callback concurrency limit (e.g., 1 outstanding delivery attempt per session at a time).

---

### SEC-067. `callbackSecret` Classified as T3 but Contains Authentication Material [Medium]

**Section 14, line 6639.** The `callbackSecret` is classified as T3 (Confidential) data. However, it is an HMAC signing secret that functions as authentication material for webhook receivers. If compromised, an attacker could forge webhook payloads to downstream CI systems. Given that credential pool secrets and OAuth tokens are T4 (Restricted), the `callbackSecret` arguably warrants T4 classification since it is a shared secret that authenticates platform-to-client communications.

**Recommendation:** Reclassify `callbackSecret` as T4, which would apply envelope encryption via KMS and immediate erasure on deletion, consistent with how other authentication secrets are treated.

---

### SEC-068. Connector Live Test Endpoint Can Be Used for Internal Network Probing [Medium]

**Section 15.1, lines 7357-7372.** The `POST /v1/admin/connectors/{name}/test` endpoint performs live DNS resolution, TLS handshake, and MCP handshake against a connector's stored URL. While rate-limited to 10/min per connector, an attacker with `tenant-admin` access could register multiple connectors pointing to internal hostnames and then run tests against each, effectively using the platform as a network scanner. The spec states it requires `platform-admin` or `tenant-admin`, but `tenant-admin` could probe internal infrastructure visible from the gateway pod.

**Recommendation:** Restrict the live test endpoint to `platform-admin` only, or apply the same private/reserved IP range validation used for `callbackUrl` to connector URLs before executing the live test.

---

### SEC-069. `dryRun` Mode Does Not Emit Audit Events, Creating Audit Gap for Reconnaissance [Low]

**Section 15.1, line 7334.** The `dryRun` parameter is documented as not emitting audit events (with the sole exception of `POST /v1/admin/bootstrap`). A `tenant-admin` attacker could use `dryRun` extensively to probe referential integrity, discover resource names, test policy boundaries, and enumerate validation behaviors without leaving any audit trail. This reconnaissance is invisible to security monitoring.

**Recommendation:** Emit a lightweight audit event (e.g., `admin.dry_run_request`) for all `dryRun` requests, recording the caller identity, endpoint, and a hash of the request body. This provides visibility without the overhead of full audit logging.

---

### SEC-070. Admin Token Stored in Kubernetes Secret Without Rotation Policy [Medium]

**Section 17.6, lines 9214-9233.** The initial admin token is stored in a Kubernetes Secret (`lenny-admin-token`) and is never automatically rotated. The spec provides a manual rotation command (`lenny-ctl admin users rotate-token`) but does not mandate or schedule automatic rotation. A long-lived admin token in a Kubernetes Secret is a persistent credential that could be compromised through etcd backup exposure, RBAC misconfiguration, or supply-chain attacks on the Kubernetes control plane.

**Recommendation:** Specify a recommended rotation schedule (e.g., 90 days) and provide a CronJob template in the Helm chart that automatically rotates the token. At minimum, add a `TokenRotationOverdue` warning alert when the token's `created_at` timestamp exceeds a configurable threshold.

---

### SEC-071. Plain HTTP Default in Tier 2 Docker Compose Exposes Credentials in Transit [Medium]

**Section 17.4, lines 9024-9030.** The default Tier 2 local development profile (`docker compose up`) runs without TLS. The spec explicitly warns against configuring real credentials in this mode, but the default configuration still allows credential pool definitions to be created. If a developer inadvertently starts a session with real LLM credentials in the default plain-HTTP mode, API keys traverse the network in cleartext. The warning is a documentation control, not a technical control.

**Recommendation:** When `LENNY_DEV_TLS=false` and a credential pool with non-echo provider type is configured, the gateway should reject session creation with an explicit error: `"Real LLM credentials cannot be used without TLS. Use 'make compose-tls' or set LENNY_DEV_TLS=true."` This converts the documentation warning into a technical enforcement.

---

### SEC-072. Bootstrap Job ServiceAccount Has Kubernetes Secret Write Access Without Scope Limitation [Medium]

**Section 17.6, line 9233.** The bootstrap Job's ServiceAccount has `create`/`get`/`patch` on Secrets in `lenny-system`. The spec notes that "RBAC for this namespace must restrict Secret access appropriately" but does not specify a `resourceNames` restriction on the ServiceAccount's RBAC binding. Without `resourceNames`, the ServiceAccount can read or patch any Secret in `lenny-system`, including database credentials, TLS certificates, and credential pool secrets.

**Recommendation:** Scope the bootstrap Job's RBAC to `resourceNames: ["lenny-admin-token"]` for Secret operations, preventing the Job from accessing other Secrets in the namespace.

---

### SEC-073. `callbackUrlAllowedDomains` Wildcard Matching Could Be Bypassed With Subdomain Tricks [Low]

**Section 14, line 6619.** The domain allowlist supports `*.suffix` wildcard matching. The spec does not specify whether the wildcard match is anchored to a single subdomain level or allows arbitrary depth. If `*.example.com` matches `a.b.c.example.com`, an attacker who controls any subdomain at any depth could register callback URLs for SSRF. Additionally, the spec does not mention whether the matching is case-insensitive (DNS hostnames are case-insensitive per RFC 4343).

**Recommendation:** Specify that `*.suffix` matches exactly one subdomain level (i.e., `*.example.com` matches `foo.example.com` but not `foo.bar.example.com`), and that matching is case-insensitive.

---

### SEC-074. No Content-Length or Transfer-Encoding Validation on Webhook Callback Responses [Low]

**Section 14, line 6618.** The callback HTTP client has a response-read timeout of 10s and disables redirect following, but the spec does not mention a maximum response body size. A malicious callback endpoint could return an unbounded response body (e.g., a 10 GB response within the 10s window), consuming gateway memory. The spec's "isolated callback worker" mitigates blast radius but does not prevent individual worker memory exhaustion.

**Recommendation:** Specify a maximum response body read size for callback responses (e.g., 1 MB) using `io.LimitReader` or equivalent.

---

### SEC-075. Upload Token TTL Tied to `maxCreatedStateTimeoutSeconds` Creates Window for Token Reuse [Medium]

**Section 15.1, lines 7289-7291.** The upload token's TTL is `session_creation_time + maxCreatedStateTimeoutSeconds` (default 300s). Between session creation and finalization, the upload token is valid. If a client creates a session but does not finalize it for the full 300s, the upload token remains valid for that window. The spec states the token is session-scoped (`UPLOAD_TOKEN_MISMATCH` error on cross-session use) and consumed on `FinalizeWorkspace`, but does not discuss whether the token can be used to upload to the same session repeatedly during the window. An attacker who obtains a valid upload token (e.g., from a log or intercepted request) has a 300s window to upload arbitrary workspace files.

**Recommendation:** This is adequately mitigated by the session-scoping and consumption-on-finalize semantics. However, the spec should explicitly state that upload tokens are bound to the session creator's identity (not just the session ID) so that a different authenticated user cannot use a stolen token.

---

### SEC-076. `env` Blocklist Glob Patterns Could Miss Encoded or Composed Variable Names [Low]

**Section 14, line 6612.** The environment variable blocklist uses glob patterns with `*` wildcard matching. The matching is documented as case-sensitive. An attacker could bypass the blocklist by using unexpected casing (e.g., `Aws_Secret_Access_Key` if the blocklist entry is `AWS_SECRET_ACCESS_KEY`). While most legitimate tools expect specific casing, some runtimes or libraries may be case-insensitive when reading environment variables.

**Recommendation:** Specify that blocklist matching is case-insensitive by default, with an opt-in `caseSensitive: true` flag for deployers who require exact-case matching.

---

### SEC-077. `runtimeOptions` Without Schema Validation Allows Arbitrary Data Injection [Medium]

**Section 14, lines 6656-6657.** When a runtime does not register a `runtimeOptionsSchema`, the spec states that options are "passed through as-is (backward compatible)" with only a warning event emitted. This means arbitrary JSON up to 64 KB can be injected into the runtime via `runtimeOptions`. For runtimes that interpret these options without validation (e.g., custom runtimes that deserialize options into configuration structs), this is a data injection vector.

**Recommendation:** For production deployments, provide a Helm value (`gateway.requireRuntimeOptionsSchema: true`) that rejects session creation for runtimes without a registered schema. The default should remain permissive for backward compatibility, but the option should exist for security-conscious deployments.

---

### SEC-078. A2A Agent Card Discovery Endpoint Returns Data Without Authentication [Low]

**Section 15.1, lines 7006-7007.** The `/.well-known/agent.json` and `/a2a/runtimes/{name}/.well-known/agent.json` endpoints are documented as "No auth." While this is standard for A2A discovery, these endpoints expose runtime names, capabilities, and potentially sensitive metadata about the platform's configuration to unauthenticated callers. In a multi-tenant deployment, this could leak information about available runtimes.

**Recommendation:** The A2A spec requires unauthenticated agent card discovery, so this is an accepted protocol constraint. However, the spec should note that only runtimes with `visibility: public` should be included in unauthenticated agent card responses. Runtime names, capabilities, and metadata for `visibility: private` runtimes should be excluded.

---

### SEC-079. No Explicit Timeout on MCP Nonce Validation Window [Low]

**Section 15.4.3, lines 8178-8198.** The MCP nonce is written to the adapter manifest and must be presented during the MCP `initialize` handshake. The spec states the nonce is "regenerated per task execution" but does not specify a maximum time window within which the nonce must be presented after it is generated. If the agent binary takes an arbitrarily long time to start (e.g., stuck in initialization), the nonce remains valid indefinitely, widening the window for a local attacker who has gained read access to the manifest file.

**Recommendation:** Specify a nonce validity window (e.g., 60s from manifest write to first MCP `initialize` acceptance). After the window, the adapter should reject the nonce and regenerate, forcing a fresh manifest read.

---

### SEC-080. Adapter-Local Tool `write_file` Has No Size Limit [Medium]

**Section 15.4.1, lines 8029-8083.** The adapter-local `write_file` tool allows agents at all tiers (including Minimum) to create or overwrite files in the workspace. The spec specifies path confinement (`/workspace` boundary) but does not specify a maximum file size for `write_file` operations. An agent could write an arbitrarily large file to the workspace, exhausting the tmpfs or persistent volume backing the workspace directory, potentially causing disk pressure on the node.

**Recommendation:** Specify a per-file write size limit for `write_file` (e.g., capped at `workspaceSizeLimitBytes` or a per-file default of 100 MB). The adapter should reject writes that would exceed the workspace size limit.

---

### SEC-081. Workspace Path Collision Rule (Last-Writer-Wins) Enables Silent File Replacement in Delegations [Medium]

**Section 14.1, line 6730.** The workspace plan's path collision rule is "last-writer-wins" -- if two sources target the same path, the later entry silently overwrites. Combined with delegation file exports (Section 8.7), a compromised parent agent could craft a workspace plan where an innocuous-looking initial source is later overwritten by a malicious export targeting the same path (e.g., `CLAUDE.md`). The spec does emit a `workspace_plan_path_collision` warning event, but this is informational only and does not block materialization. Section 13.5 acknowledges this gap for file exports but only in the context of content scanning.

**Recommendation:** For delegation-originated file exports, the path collision warning should be elevated to a policy-checkable event. Deployers should be able to configure `workspacePlan.onConflict: reject` for delegation scenarios where overwrites of instruction files (e.g., matching a pattern like `*.md`, `.claude/*`) are prohibited.

---

### SEC-082. `DELETE /v1/credentials/{credential_ref}` Does Not Revoke Active Leases [High]

**Section 15.1, line 7021.** The spec states that `DELETE /v1/credentials/{credential_ref}` removes a registered credential but "active session leases are unaffected." This means a user can delete their credential record while active sessions continue using the leaked/compromised key. In contrast, `POST /v1/credentials/{credential_ref}/revoke` explicitly invalidates all active leases. The `DELETE` endpoint creates a security gap: a user who discovers their key is compromised and instinctively deletes it (rather than revoking) leaves the compromised key in active use.

**Recommendation:** Change the `DELETE` semantics to also invalidate active leases backed by the deleted credential, or at minimum return a warning in the response body directing the user to use the `revoke` endpoint for immediate lease invalidation. Alternatively, rename `DELETE` to a softer operation (e.g., "unregister") and document that revocation is the correct response to a compromised key.

---

### SEC-083. `POST /v1/admin/bootstrap` With `--force-update` Can Overwrite Tenant Configuration [Medium]

**Section 17.6, lines 9205-9211.** The bootstrap upsert with `--force-update` replaces existing resources with the seed file definition using `If-Match: *` (accept any current version). While the spec blocks security-critical fields (tenant `id`, runtime `isolationProfile`), it does not enumerate all security-critical fields that should be protected. For example, `--force-update` could change a tenant's `complianceProfile` from `hipaa` to `none`, effectively downgrading security controls for regulated workloads. Similarly, it could change a pool's `egressProfile` from `restricted` to `internet`.

**Recommendation:** Expand the "security-critical field" block list to include: `complianceProfile`, `egressProfile`, `deliveryMode`, `workspaceTier` (data classification), and any field whose modification would weaken the security posture.

---

### SEC-084. No Rate Limit on `POST /v1/sessions/{id}/elicitations/{id}/respond` [Low]

**Section 15.1, line 6934.** The elicitation response endpoint accepts client input that is delivered to the agent runtime. While elicitations are gated by the agent initiating them (`lenny/request_elicitation`), there is no documented rate limit on how frequently a client can submit responses to pending elicitations. A malicious client could flood the endpoint with rapid response submissions, potentially causing the runtime to process excessive input.

**Recommendation:** This is a low-severity issue because each elicitation has a unique ID and can only be responded to once. No additional mitigation needed beyond the existing deduplication.

---

### SEC-085. `LENNY_DEV_MODE` as a Single Gate for All Security Relaxations Creates a Single Point of Compromise [Medium]

**Section 17.4, lines 9069-9075.** The spec establishes `LENNY_DEV_MODE=true` as the "single gate for all security relaxations" including TLS bypass, JWT signing bypass, and "any future relaxations." While this is convenient for development, it means that an attacker who can set this single environment variable (e.g., through a Kubernetes deployment misconfiguration, CI/CD pipeline injection, or supply-chain attack on the Helm values) instantly disables all security controls at once.

**Recommendation:** Add a startup check that refuses to start with `LENNY_DEV_MODE=true` when `LENNY_ENV=production` is set. Additionally, emit a `PLATFORM_DEV_MODE_ACTIVE` event on every client-facing API response header so that monitoring tools can detect accidental dev mode activation. Consider splitting TLS bypass and JWT bypass into separate flags under the `LENNY_DEV_MODE` umbrella so that a compromise of one flag does not disable all security at once.

---

### SEC-086. No Documented Limit on Concurrent `derive` Operations Per Session [Low]

**Section 15.1, line 7270.** The `DERIVE_LOCK_CONTENTION` error code indicates that concurrent derive operations are limited, but the actual limit is not specified in the error catalog or the derive endpoint documentation. Without a documented limit, implementers may choose an overly generous value, allowing an attacker to trigger many concurrent workspace snapshot reads from MinIO, potentially causing I/O pressure.

**Recommendation:** Document the specific concurrency limit for derive operations per session (e.g., "maximum 3 concurrent derive operations per source session").

---

---

## 3. Network Security (NET)

### NET-050. OTLP Supplemental NetworkPolicy Allows Unscoped Egress to Collector [Medium]

Section 13.2 defines a supplemental NetworkPolicy allowing agent pods to send OTLP telemetry to the collector (`lenny-otel-collector.lenny-system` on port 4317). However, this rule is additive to the base default-deny and uses only a `namespaceSelector` + `podSelector` target. If an attacker inside an agent pod can forge or relay traffic on port 4317, they could potentially use the OTLP channel to exfiltrate data encoded as trace attributes or metric labels. The spec does not mention any payload-size caps or content filtering on the OTLP channel from agent pods. While OTLP is a structured protocol, the collector should be configured to drop or sanitize unusually large string attributes from the agent namespace. The spec should either (a) state that the OTLP collector applies attribute-size limits for agent-sourced telemetry, or (b) acknowledge the exfiltration residual risk and document that deployers should configure attribute-size limits on the collector.

### NET-051. Dedicated CoreDNS Corefile Lacks Explicit NXDOMAIN for Internal Cluster Zones [Medium]

Section 13.2 specifies a dedicated CoreDNS instance for the agent namespace with DNS exfiltration mitigation. The Corefile reference shows a `template` block returning NXDOMAIN for queries matching `*.cluster.local` but does not show handling for other internal Kubernetes zones. Depending on cluster configuration, Kubernetes may use additional DNS zones (e.g., `*.svc`, reverse-lookup zones for pod IPs in `in-addr.arpa`). An agent pod could potentially perform reverse DNS lookups on internal IPs to discover service names and topology. The spec should either enumerate all internal zones that must be blocked or use a forward-deny approach where only explicitly allowed external forward zones are permitted and all other queries receive NXDOMAIN.

### NET-052. No NetworkPolicy for Redis Pub/Sub Certificate Deny-List Channel [Medium]

Section 4.9 states that the credential deny list is propagated via Redis pub/sub on a `certificate_deny_list` channel, and Section 13.2 documents the `lenny-system` namespace network policies with component-specific allow-lists. However, the spec does not explicitly document which components are allowed to subscribe to the `certificate_deny_list` Redis pub/sub channel. Since all gateway replicas need this channel and the network policy already allows gateway-to-Redis traffic, this is likely covered by the existing `redis-client` NetworkPolicy. But if a compromised component within `lenny-system` (e.g., the OTel collector or PgBouncer) has Redis network access for another purpose, it could also subscribe to this channel, potentially learning which credentials have been revoked. The component-specific Redis NetworkPolicy allow-list table should confirm that only the gateway pods have network-level access to the Redis instances, not other `lenny-system` components.

### NET-053. Internet Egress Profile IMDS Blocking Relies Solely on NetworkPolicy `except` Clause [Medium]

Section 13.2 describes blocking cloud IMDS endpoints (169.254.169.254) for agent pods using `except` clauses within the broader internet egress CIDR rule. This is a single-layer defense. If the NetworkPolicy CNI implementation has a bug in `except` clause evaluation (which has occurred historically in Calico and Cilium), the IMDS would be accessible to agent pods in the `internet` egress profile. The spec does not mention any defense-in-depth for IMDS blocking, such as iptables rules on the node or instance metadata service v2 (IMDSv2) enforcement at the cloud provider level. The spec should recommend that deployers additionally configure IMDSv2 with hop limit 1 (AWS) or equivalent provider-level IMDS hardening, and document this as a defense-in-depth requirement rather than relying solely on the NetworkPolicy `except` clause.

### NET-054. Missing Rate Limit on Gateway `/internal/drain-readiness` Endpoint [Low]

Section 12.5 defines a `/internal/drain-readiness` endpoint on the gateway that performs a MinIO liveness probe. The endpoint is consumed by the `lenny-drain-readiness` ValidatingAdmissionWebhook. The spec does not mention rate-limiting or authentication on this endpoint. While it is an internal endpoint (likely not exposed via the external Ingress), if accessible within the cluster network, any pod could repeatedly hit it and trigger MinIO `HeadBucket` probes, creating a potential amplification vector against MinIO. The endpoint should either be authenticated (e.g., using the admission webhook's mTLS identity) or rate-limited to prevent abuse.

### NET-055. ValidatingAdmissionWebhook `lenny-label-immutability` ServiceAccount Scope Not Specified [Medium]

Section 13.2 and Section 17.2 describe the `lenny-label-immutability` webhook that restricts mutation of security-sensitive labels (`lenny.dev/managed`, `lenny.dev/delivery-mode`, `lenny.dev/egress-profile`) to the warm pool controller ServiceAccount at pod creation time. However, the spec does not define the exact RBAC scope or service account identity used by this webhook server. If the webhook itself runs with broad permissions, a compromise of the webhook pod could allow an attacker to bypass the very label protections it enforces. The spec should state: (a) the specific ServiceAccount under which the webhook runs, (b) that it has minimal RBAC (only `get` on pods, no `patch`/`update`), and (c) that the webhook pod runs in `lenny-system` under the same mTLS and network isolation as other control-plane components.

### NET-056. No Explicit mTLS Enforcement Between Gateway and Dedicated CoreDNS [Medium]

Section 13.2 specifies a dedicated CoreDNS deployment for the agent namespace with a NetworkPolicy allowing agent pods to reach it on port 53. DNS traffic between agent pods and the dedicated CoreDNS is over UDP/TCP port 53, which is inherently unencrypted. An attacker who gains access to the network plane (e.g., via a container escape in the agent namespace) could intercept or spoof DNS responses to redirect agent traffic to malicious endpoints. While the spec's mTLS enforcement for gateway-to-pod and pod-to-gateway traffic mitigates downstream damage, the DNS resolution step that determines which IPs to connect to remains unprotected. The spec should acknowledge that DNS traffic within the cluster is unencrypted, and either (a) recommend DNS-over-TLS for the dedicated CoreDNS (CoreDNS supports `tls` plugin), or (b) document this as an accepted residual risk given that gVisor/Kata isolation prevents direct network-plane attacks from within the agent pod.

### NET-057. `webhookIngressCIDR` Default of `0.0.0.0/0` Is Overly Permissive [Low]

Section 13.2 and Section 17.6 define `webhookIngressCIDR` with a default of `0.0.0.0/0`, meaning the admission webhook pods in `lenny-system` accept TCP 443 ingress from any source. While the spec notes that `lenny-system` has a default-deny namespace policy and mTLS provides authentication, the `webhookIngressCIDR` default effectively punches a wide hole in the otherwise tight `lenny-system` NetworkPolicy. The spec recommends tightening it to the control-plane node CIDR but does not enforce this recommendation. For hardened deployments, the default should be left as-is for compatibility but the preflight Job should emit a warning when the default `0.0.0.0/0` is in use in production mode, similar to how it warns about missing SIEM configuration.

### NET-058. No Network-Level Isolation Between Tenants in Shared Agent Namespace [High]

Section 13.2 defines NetworkPolicies for agent namespaces that control ingress/egress at the pod level (gateway-to-pod, pod-to-gateway, etc.), but there is no tenant-level network isolation within the `lenny-agents` namespace. All agent pods from all tenants run in the same Kubernetes namespace and share the same NetworkPolicy rules. A compromised agent pod from tenant A could potentially communicate with an agent pod from tenant B if both are in the same namespace, as no NetworkPolicy prevents pod-to-pod traffic within the agent namespace.

The spec's default-deny policy blocks all ingress/egress except explicitly allowed paths, which should prevent direct pod-to-pod communication. However, this depends entirely on the correctness of the CNI implementation -- if the default-deny has any gap (e.g., allowing same-namespace traffic, which some CNI plugins do by default before the deny policy is applied), tenant isolation is broken at the network level. The spec should either (a) explicitly add a NetworkPolicy that denies all intra-namespace pod-to-pod traffic within `lenny-agents` (an egress rule blocking `podSelector: {}` destinations in the same namespace), or (b) document that cross-tenant network isolation within the agent namespace relies solely on the default-deny policy and gVisor/Kata process isolation, and that deployers requiring stronger tenant network isolation should use per-tenant agent namespaces.

### NET-059. Circuit Breaker State Propagation Over Redis Pub/Sub Not Authenticated [Low]

Section 11.6 states that circuit breaker state changes are published on the `circuit_breaker_events` Redis pub/sub channel and picked up by all gateway replicas. Redis AUTH provides connection-level authentication, but once authenticated, any component with Redis access can publish to any pub/sub channel. If a compromised component within `lenny-system` has Redis network access (for any legitimate purpose), it could publish a false circuit-breaker-open message, causing all gateway replicas to reject requests for a targeted runtime, pool, or operation type -- effectively creating a denial-of-service. The spec should either (a) document that Redis pub/sub messages for security-critical state changes should be HMAC-signed by the publishing gateway, with recipients verifying the signature before applying state changes, or (b) acknowledge this as an accepted risk given that `lenny-system` components are trusted.

### NET-060. SPIFFE URI Validation Gap: No Spec for How Proxy-Mode Lease Tokens Are Bound to Pod Identity During Coordinator Handoff [Medium]

Section 4.9 describes SPIFFE-binding for proxy-mode credential lease tokens, where the lease is bound to the pod's SPIFFE identity (`spiffe://lenny.cluster.local/ns/lenny-agents/pod/<pod-id>`). Section 10.1 describes the coordinator handoff protocol where a session may be migrated to a new gateway replica. During coordinator handoff, the pod's SPIFFE identity does not change, but the gateway replica holding the lease reference changes. The spec does not explicitly state whether the new coordinator re-validates the SPIFFE binding during the handoff protocol's step 2 (`CoordinatorFence` RPC). If the new coordinator accepts the lease without re-verifying the pod's SPIFFE identity matches the lease's expected identity, there is a window where a lease could be used against a different pod. The coordinator handoff protocol should explicitly include SPIFFE identity re-validation as part of the fencing step.

### NET-061. No TLS Requirement Specified for External Interceptor Webhook Endpoints [Medium]

Section 4.8 defines the `RequestInterceptor` chain with support for external interceptors referenced by `interceptorRef`. These are HTTP webhook endpoints that receive request payloads (including potentially sensitive data like user IDs, tenant IDs, and LLM request content) for pre/post processing. The spec specifies timeout limits and fail-policies for external interceptors but does not explicitly require TLS for the webhook endpoint URLs. A `http://` endpoint would transmit interceptor payloads (which may contain PII and credential-adjacent data) in cleartext. The spec should require that external interceptor `interceptorRef` URLs use `https://` in production mode (`LENNY_ENV=production`), consistent with the connector URL scheme validation that enforces `https` only in production (Section 15.1 dryRun connector validation).

### NET-062. Billing Redis Stream Keys Accessible to All Redis-Connected Components [Low]

Section 11.2.1 specifies that billing events are staged to per-tenant Redis streams (`t:{tenant_id}:billing:stream`) during Postgres failover. These streams contain full billing event payloads including `user_id`, `session_id`, and token counts. The streams use the standard tenant-prefix key convention, but Redis ACLs are not specified at per-key-prefix granularity in the spec. If the Redis instance is shared across concerns (before Tier 3 separation), any component with Redis AUTH credentials (e.g., the quota store, routing cache) could read billing stream data. The spec should either (a) specify Redis ACL rules that restrict `XREAD`/`XRANGE` on `t:*:billing:stream` keys to the gateway's Redis user only, or (b) acknowledge that pre-Tier-3 shared Redis instances have no intra-instance isolation and that billing event payloads in the Redis stream are accessible to any authenticated Redis client.

### NET-063. Connector Test Endpoint Rate Limit Insufficient as Network Scanning Prevention [Low]

Section 15.1 specifies `POST /v1/admin/connectors/{name}/test` with a rate limit of 10 requests per connector per minute to "prevent abuse as a network scanning tool." With hundreds of connectors registered (each potentially pointing to a different URL), an attacker with `tenant-admin` or `platform-admin` access could register many connectors pointing to internal network targets and use the test endpoint to probe the internal network at a rate of 10 * N per minute, where N is the number of registered connectors. The rate limit should also include a per-caller aggregate limit (e.g., 30 total test requests per minute regardless of connector count) to prevent this amplification pattern.

---

---

## 4. Scalability & Performance (SCL)

### SCL-060. Tier 3 KEDA Path minReplicas Table Inconsistency [Medium]

**Section:** 17.8.2, Gateway table vs. burst-absorption formula

The gateway table in Section 17.8.2 lists Tier 3 `minReplicas` as 5, but the KEDA burst-absorption formula computes `ceil(4000/400) = 10`. The spec attempts to justify 5 by saying the scale-up policy absorbs the remainder within one 15s period. However, during the first 15 seconds of a burst, 5 replicas at 400 sessions/replica can absorb only 2,000 sessions while 4,000 arrive before HPA reacts (200/s * 20s). The 2,000-session gap during that 15s must be queued or rejected. The spec says the scale-up policy "doubles replicas in the first 15s" -- but going from 5 to 10 replicas takes a full 15s scaling period, during which an additional 3,000 sessions arrive (200/s * 15s). The math does not close: 5,000 total sessions arrive (200/s * 25s from burst start to scale completion) but only 4,000 capacity is available (10 * 400). **Recommendation:** Either set Tier 3 `minReplicas: 10` unconditionally for the KEDA path, or add explicit documentation of the expected rejection rate and queue-depth tolerance during the first scale-up cycle.

### SCL-061. CheckpointBarrier Concurrent MinIO Upload Storm at Tier 3 [Medium]

**Section:** 10.1, 17.8.2 (Object Storage table)

The CheckpointBarrier protocol during gateway drain can trigger up to `maxSessionsPerReplica` (400 at Tier 3) simultaneous checkpoint uploads from a single draining coordinator. With an average workspace of 100 MB, this produces 40 GB of upload bandwidth from a single gateway pod within the barrier timeout window (90s). The MinIO throughput budget table (Section 17.8.2) sizes for steady-state checkpoint rate of ~17/s across the cluster, not for a single-node burst of 400 concurrent uploads. A single draining coordinator generating 400 simultaneous uploads (~4.4/s burst vs. the expected cluster-wide 17/s steady-state) could saturate the MinIO write path if multiple coordinators drain simultaneously during a rolling update. **Recommendation:** Add a per-coordinator checkpoint upload concurrency limiter (e.g., semaphore of 50 concurrent uploads) and document the interaction between rolling update batch size and MinIO burst bandwidth.

### SCL-062. Gateway terminationGracePeriodSeconds May Be Insufficient for Tier 3 Drain [Medium]

**Section:** 17.8.2

The gateway `terminationGracePeriodSeconds` for Tier 3 is 300s. The CheckpointBarrier timeout is 90s, and the gateway preStop drain timeout is 120s. After drain timeout plus barrier timeout (210s), the gateway still needs to complete session handoffs. With 400 sessions per replica, session handoff at coordinator-hold-timeout (120s worst case) could push total drain time to 330s, exceeding the 300s `terminationGracePeriodSeconds`. This would result in SIGKILL of the gateway pod with incomplete checkpoint flushes. **Recommendation:** Set Tier 3 `terminationGracePeriodSeconds >= preStopDrainTimeout + checkpointBarrierTimeout + coordinatorHoldTimeout + buffer`, which would be approximately 120 + 90 + 120 + 30 = 360s.

### SCL-063. Redis Lua Script Serialization Bound Not Validated Against Tier 3 Delegation Fan-Out [Medium]

**Section:** 8.3, 11.2, 17.8.2

The budget reservation Lua script (`budget_reserve.lua`) performs 6 reads + 5 conditional writes, estimated at ~100us per invocation. At Tier 3, the delegation fan-out sizing shows 500 concurrent delegations. If these delegations produce simultaneous budget reservation calls, the serial Lua execution time is 500 * 100us = 50ms. The spec notes `maxParallelChildren` has a soft ceiling of 50 and hard ceiling of 100 due to Lua serialization, but the system-wide concurrent delegation limit at Tier 3 is 500. Multiple concurrent delegation trees could each produce budget reservation calls that serialize on the same Redis instance. With Redis Cluster (Tier 3), budget keys for different trees could hash to the same shard, concentrating contention. **Recommendation:** Specify budget key hashing strategy to distribute across Redis Cluster shards (e.g., include tenant_id in the hash tag) and document the maximum acceptable Lua blocking time for the LeaseStore SLO.

### SCL-064. Billing Redis Stream MAXLEN Insufficient for Extended Postgres Outages at Tier 1/2 [Low]

**Section:** 17.8.2

The Tier 3 `billingRedisStreamMaxLen` was explicitly derived (72,000 = 600/s * 60s * 2), but Tier 1/2 use the default 50,000. At Tier 2 with ~60/s billing event rate, the stream fills in ~14 minutes. While this covers the 30s Postgres failover RTO with ample margin, an extended outage (e.g., Postgres maintenance or prolonged failover) exceeding 14 minutes would begin dropping billing events into the in-memory WAL buffer. The spec documents a two-tier failover (Redis stream then in-memory WAL), so data loss requires both Redis and Postgres to be unavailable for >14 minutes. The risk is low but the derivation is not explicit for Tier 1/2 as it is for Tier 3. **Recommendation:** Add explicit derivation comments for Tier 1/2 MAXLEN (as was done for Tier 3 in footnote 4) documenting the fill-time safety margin.

### SCL-065. Warm Pool Sizing Formula Omits SDK-Warm Startup Latency in Baseline Table [Medium]

**Section:** 17.8.2

The warm pool baseline table uses `pod_startup_seconds = 10s` for the recommended `minWarm` derivation. The text below the table notes that SDK-warm pools have `pod_warmup_seconds` of 30-90s, "far exceeding the 10s startup baseline." However, the baseline `minWarm` values (20 / 175 / 1050) are presented as the starting points without any SDK-warm variant. An operator deploying an SDK-warm pool using the baseline table values would have 2-9x fewer warm pods than needed. The text does explain the discrepancy, but the table itself is the most prominent reference and carries no SDK-warm qualifier or alternative row. **Recommendation:** Add an explicit SDK-warm row to the warm pool sizing table, or add a bold warning annotation directly in the table header that baseline values apply only to pod-warm pools.

### SCL-066. Concurrent-Workspace Slot Rehydration Postgres Query Burst Unquantified at Tier 3 [Medium]

**Section:** 7.2, 12.4

When a concurrent-workspace pod's Redis slot counter is lost (Redis failover, eviction), the spec states that the replacement pod rehydrates by querying Postgres for all active slots. At Tier 3 with `maxConcurrent` up to 20,000 (Stream Proxy) or the pool's `maxConcurrent` (e.g., 50 concurrent workspace slots per pod), this rehydration query could return thousands of rows per pod. If multiple pods rehydrate simultaneously after a Redis Cluster node failure (affecting all pods whose slot keys hashed to that node), the resulting Postgres read burst could be significant. The spec does not quantify this burst or provide a rate-limiting mechanism for rehydration queries. **Recommendation:** Add a rehydration query rate limiter (e.g., stagger rehydration across pods using jitter) and document the expected Postgres read IOPS during Redis recovery at each tier.

### SCL-067. etcd Write Pressure Calculation Discrepancy [Medium]

**Section:** 4.6.1, 17.8.2

Section 4.6.1 discusses etcd write pressure with a `statusUpdateDeduplicationWindow` to reduce CRD status updates. Section 17.8.2 sets Tier 3 `statusUpdateDeduplicationWindow` to 250ms and recommends a dedicated etcd cluster. However, the actual Tier 3 etcd write rate is not explicitly calculated in the capacity reference table. With 10,000 concurrent sessions, each Sandbox CRD potentially updating status on every heartbeat (10s default), the raw update rate before deduplication is ~1,000/s. With 250ms deduplication, the effective rate depends on how many status changes occur within each 250ms window -- which is workload-dependent and not bounded by the deduplication window alone. The spec should provide the post-deduplication write rate estimate for the Tier 3 capacity table, since the etcd controller tuning section mentions ~800 writes/s but does not show the derivation. **Recommendation:** Add an explicit derivation of the Tier 3 etcd write rate in the controller tuning table, analogous to the Postgres IOPS derivation.

### SCL-068. Tree Recovery Time Formula Can Truncate Leaf Resume Windows [Medium]

**Section:** 8.10

The spec documents that `maxTreeRecoverySeconds` (default 600s) must satisfy `maxTreeRecoverySeconds >= maxResumeWindowSeconds + (maxDepth-1) * maxLevelRecoverySeconds + buffer`. With `maxResumeWindowSeconds` = 900s (default) and the default depth of 4, the formula yields `900 + 3 * maxLevelRecoverySeconds + buffer`. For any positive `maxLevelRecoverySeconds` and buffer, this exceeds 900s, which already exceeds the 600s default. This means the default `maxTreeRecoverySeconds` (600s) is mathematically insufficient for the default `maxResumeWindowSeconds` (900s) at any tree depth. The spec acknowledges this: "deep trees need increase." However, the default configuration produces an immediate violation of its own formula -- even a depth-1 tree needs `maxTreeRecoverySeconds >= 900 + buffer > 600`. **Recommendation:** Either increase `maxTreeRecoverySeconds` default to at least 960s (900 + buffer), or decrease `maxResumeWindowSeconds` default to a value compatible with `maxTreeRecoverySeconds: 600`.

### SCL-069. Outbound Channel Buffer Depth Sizing Not Tier-Aware [Low]

**Section:** 15 (OutboundChannel back-pressure policy)

The `MaxOutboundBufferDepth` default is 256 events, configurable per adapter via Helm. At Tier 3 with high-throughput sessions (e.g., streaming LLM output with multiple OutputParts per turn), a webhook-based A2A subscriber with 1-2 seconds of delivery latency could accumulate events faster than the buffer depth. The default 256 is not scaled to tier. For a Tier 3 session producing 50 events/second (not unreasonable for fine-grained streaming), the buffer fills in ~5 seconds of subscriber stall. While head-drop is the intended degradation mode, the spec does not provide tier-aware buffer sizing guidance. **Recommendation:** Add per-tier recommended `outboundBufferDepth` values or a formula relating buffer depth to expected event rate and subscriber latency.

### SCL-070. Message Deduplication Window Redis Memory Unbounded at Tier 3 [Medium]

**Section:** 15.4.1 (MessageEnvelope id deduplication)

Message ID deduplication uses a Redis sorted set per session (`t:{tenant_id}:session:{session_id}:msg_dedup`) with a default TTL of 3600s. At Tier 3 with 10,000 concurrent sessions, each receiving multiple messages, the total deduplication state is `10,000 sessions * messages_per_session * (message_id_size + timestamp_size)`. If sessions receive an average of 100 messages/hour (conservative for interactive sessions), total deduplication entries are 1M with a ULID size of ~26 bytes each, consuming ~50 MB of Redis memory just for deduplication. While 50 MB is manageable, the spec does not account for this in the Redis memory sizing (Tier 3: 8 GB per node). More importantly, `deduplicationWindowSeconds` is globally configurable but the memory impact scales with the product of session count, message rate, and window size -- a deployer increasing the window to 7200s doubles the footprint. **Recommendation:** Add deduplication memory estimation to the Redis sizing section, or cap per-session deduplication set size (e.g., ZREMRANGEBYSCORE + ZCARD limit).

### SCL-071. Delegation-Adjusted minWarm Formula Double-Counts Safety Factor [Low]

**Section:** 17.8.2

The delegation-adjusted `minWarm` formula is:
```
minWarm >= adjusted_claim_rate * safety_factor * (failover_seconds + pod_startup_seconds) / mode_factor
            + adjusted_burst_claims * pod_warmup_seconds / burst_mode_factor
```

The worked example uses `safety_factor = 1.2` and `pod_warmup_seconds = 35s`. However, the burst term `adjusted_burst_claims * pod_warmup_seconds` does not include the safety factor, while the steady-state term does. This is inconsistent with the base formula in Section 4.6.2, which applies `safety_factor` uniformly. The worked example yields 3,346 without safety on the burst term. If the safety factor were applied to the burst term as well: `ceil(1,596 + 1,750 * 1.2) = ceil(1,596 + 2,100) = 3,696`. The discrepancy is small (~10%) but the inconsistency between the two formulas could confuse operators performing their own sizing calculations. **Recommendation:** Clarify whether `safety_factor` applies to the burst term; make the delegation-adjusted formula consistent with the base formula.

### SCL-072. Coordinator Handoff Generation Counter Race During Split-Brain [Medium]

**Section:** 10.1

The spec describes coordinator handoff with generation counters and CoordinatorFence RPC. During a network partition (split-brain), the old coordinator may continue operating with a stale generation while the new coordinator is elected. The CoordinatorFence RPC is used to fence the old coordinator, but fencing requires network connectivity to the old coordinator -- precisely what is unavailable during a partition. The spec does not describe what happens if CoordinatorFence cannot reach the old coordinator during the `coordinatorHoldTimeoutSeconds` window. If both coordinators operate simultaneously (old with stale generation, new with incremented generation), session state updates from both could conflict in Postgres. **Recommendation:** Specify the behavior when CoordinatorFence fails to reach the old coordinator within the hold timeout, including whether the new coordinator proceeds unconditionally (risking dual-writes) or blocks until the hold timeout expires.

### SCL-073. PgBouncer Connection Pool Sizing Does Not Account for CheckpointBarrier Write Burst [Low]

**Section:** 17.8.2

The Tier 3 PgBouncer `default_pool_size` is 110, derived from `(max_connections - headroom) / pgbouncer_replicas`. During a CheckpointBarrier event (rolling update), up to 400 sessions per draining coordinator write checkpoint metadata to Postgres simultaneously. Each checkpoint write requires a Postgres connection through PgBouncer. With 4 PgBouncer replicas and 110 pool size each, the total backend connections available are 440 -- but a single CheckpointBarrier from one coordinator could request up to 400 connections while steady-state operations from other coordinators continue. The PgBouncer `reserve_pool_size` of 15 provides minimal overflow capacity. **Recommendation:** Either serialize checkpoint metadata writes within the CheckpointBarrier (reducing concurrent connection demand) or increase `reserve_pool_size` for Tier 3 to account for barrier-induced bursts.

### SCL-074. Experiment Variant Pool Scaling `initialMinWarm` Default of 0 Causes Cold Start [Medium]

**Section:** 10.7

The spec mentions that experiment variant `initialMinWarm` defaults to 0, meaning all new experiments start with no pre-warmed pods for variant pools. When an experiment activates, the first sessions assigned to a variant must wait for pods to be created and warmed from zero. At Tier 3, where experiment activation could immediately route hundreds of sessions to a variant pool, the cold-start latency (pod creation + warmup) would violate the session-ready-time SLO for all initial variant sessions. The PoolScalingController detects the demand signal from the experiment activation, but the HPA/controller pipeline lag (20-60s) means the first sessions experience significant delay. **Recommendation:** Either change the default `initialMinWarm` to a non-zero value proportional to the variant's traffic weight (e.g., `ceil(baseline_minWarm * variant_weight)`), or document that deployers must always set `initialMinWarm` explicitly for production experiments.

### SCL-075. Multi-Region Quota Enforcement Requires Manual Sub-Division [Low]

**Section:** 12.8

The spec states that multi-region deployments enforce quotas per-region with no cross-region aggregation. This means a tenant with a global quota of 1,000 sessions deployed across 3 regions must have their quota manually divided (e.g., 334/333/333) by the deployer. There is no mechanism for dynamic cross-region quota balancing. If one region experiences a traffic spike while others are idle, sessions are rejected in the hot region even though global capacity is available. For Tier 3 multi-region deployments, this manual sub-division at 10,000+ sessions across regions is operationally complex and prone to misconfiguration. **Recommendation:** Document a recommended quota allocation strategy (e.g., over-provision each region to 80% of global quota, accepting 2.4x theoretical overshoot as the trade-off) or outline a post-v1 design for cross-region quota aggregation.

### SCL-076. Session Inbox In-Memory Mode Message Loss Has No Sender Notification [Low]

**Section:** 7.2

The spec acknowledges that in-memory inbox mode (default) loses messages on coordinator crash. The delivery receipt mechanism returns `queued` at delivery time, but if the coordinator crashes before the message is delivered to the runtime, the sender has no notification that the message was lost. The sender's delivery receipt shows `queued` (success), and the message silently disappears. The spec notes "senders that require reliable delivery MUST track receipts and re-send on gap detection," but provides no mechanism for gap detection -- the sender cannot query what messages were actually delivered to the runtime vs. lost in the crash. **Recommendation:** Either provide a `GET /v1/sessions/{id}/messages?status=delivered` endpoint that shows actual delivery status (not just receipt status), or add a post-recovery notification to senders whose messages were lost.

---

---

## 5. Protocol Design (PRT)

### PRT-057. Adapter manifest `version` increment semantics conflict with forward-compatibility rule [Medium]

Section 4.7 states that runtimes "must silently ignore unknown top-level fields" and that "A `version` increment indicates a breaking change to existing field semantics." However, the manifest field reference table marks `version` as "Currently `1`. Runtimes should reject unknown major versions." This creates ambiguity: the spec uses a single integer field but describes "major version" semantics. If `version` were bumped from `1` to `2` (as explicitly planned for the nonce migration in Section 15.4.3), some runtimes would reject it while others forward-read unknown fields. The spec never defines what constitutes a "major" vs. "minor" increment on a plain integer field, nor provides a negotiation mechanism for the manifest schema -- unlike the adapter protocol (which has `AdapterInit`/`AdapterInitAck`) and MCP (which has `initialize` negotiation). Runtimes that reject `version: 2` will fail immediately; runtimes that ignore it may miss the new nonce handshake mechanism. The spec should either define the integer as strictly breaking (every increment is breaking, runtimes must reject) or adopt a two-part version (major.minor) with clear rules.

### PRT-058. Nonce migration from `params._lennyNonce` to pre-initialize handshake lacks version gate [Medium]

Section 15.4.3 describes a migration from `params._lennyNonce` (v1) to a pre-`initialize` out-of-band handshake (v2), with a "two-release backward-compat window." However, the spec does not define what signals the adapter to expect the v2 handshake vs. the v1 nonce. The adapter manifest `version` field is the only candidate, but the adapter writes the manifest before the runtime connects -- the adapter has no way to know which nonce protocol the runtime will use until the connection is opened. The spec should define: (a) the adapter always tries v2 first (listen for the `lenny_nonce` JSON line within a timeout, then fall back to accepting `_lennyNonce` in `initialize`), or (b) the runtime declares its nonce protocol version in some out-of-band way. Without this, the "two-release backward-compat window" is under-specified and will lead to interoperability failures during the migration.

### PRT-059. `OutputPart.schemaVersion` dual-level versioning creates ambiguous precedence for durable consumers [Medium]

Section 15.4.1 and 8.8 establish a dual-level versioning model: `TaskRecord.schemaVersion` for the envelope and per-`OutputPart.schemaVersion` for nested parts. The durable-consumer forward-read rule (Section 15.5 item 7) requires consumers to apply the rule "independently at both levels." However, the spec does not address the case where a `TaskRecord` at envelope `schemaVersion: 1` contains an `OutputPart` at `schemaVersion: 3` that introduces a field which changes the semantic meaning of an envelope-level field (e.g., a hypothetical `OutputPart` field that redefines how `usage` should be interpreted). The two-level independence assumption breaks down if part-level schema evolution has cross-level semantic dependencies. The spec should explicitly state that part-level schema changes MUST NOT alter the interpretation of envelope-level fields, making the independence guarantee enforceable.

### PRT-060. `schemaVersion` round-trip loss through MCP/OpenAI/Open Responses adapters invalidates durable consumer obligations [High]

The Translation Fidelity Matrix (Section 15.4.1) documents that `schemaVersion` is `[dropped]` through MCP, OpenAI Completions, and Open Responses adapters, and "always reconstructed as `1` on inbound regardless of original value." This means any `OutputPart` that round-trips through these adapters permanently loses its schema version. If a delegation chain routes output through an MCP adapter (parent session) and the result is persisted as a `TaskRecord`, the durable consumer sees `schemaVersion: 1` even though the original part was at version 2+. The consumer has no way to detect that the part may contain fields introduced in a later schema version, because the version was reset to 1. This silently violates the forward-read obligation -- the consumer processes the part at v1 without triggering any `schema_version_ahead` degradation signal, potentially misinterpreting or discarding v2+ fields. The spec should either (a) preserve `schemaVersion` through all adapter translations (at minimum in a sidecar annotation), or (b) explicitly call out this data loss as acceptable and document that durable consumers of delegation-chain `TaskRecord` objects must not rely on `schemaVersion` accuracy.

### PRT-061. `MessageEnvelope.schemaVersion` immutability is contradicted by gateway-injection semantics [Medium]

Section 15.4.1 states that `MessageEnvelope.schemaVersion` is "gateway-injected" at "inbox-enqueue time and is immutable once written." Section 15.5 item 7 states the field is "set at write time by the gateway and is immutable once written." However, during a rolling gateway upgrade, different gateway replicas may be running different code versions. If replica A enqueues a message at `schemaVersion: 1` and replica B (running newer code) later adds a degradation annotation (e.g., `schema_version_ahead`), this modifies the persisted message. The spec should clarify whether annotations are considered separate from the immutable `schemaVersion` field, or whether the entire persisted `MessageEnvelope` is write-once-immutable (which would conflict with the degradation annotation mechanism).

### PRT-062. Lifecycle channel version negotiation lacks forward-compatibility mechanism [Medium]

Section 4.7 defines the lifecycle channel capability negotiation (`lifecycle_capabilities` / `lifecycle_support` exchange) but uses string-based capability names (e.g., `"checkpoint"`, `"interrupt"`) with no protocol version field. The `protocolVersion` field in `lifecycle_capabilities` is documented as a simple string (e.g., `"1.0"`) but the spec never defines what happens when the adapter sends `protocolVersion: "2.0"` and the runtime only understands `"1.0"`. Unknown messages are "silently ignored on both sides," but there is no mechanism for the runtime to signal that it does not understand the offered protocol version. The runtime simply replies with the subset of capabilities it supports -- but if v2.0 changes the semantics of an existing capability (e.g., `"checkpoint"` now requires a new field in `checkpoint_ready`), the runtime cannot detect this. The spec should define version negotiation rules: does the adapter fall back to the runtime's version, or must the versions be compatible at the major level?

### PRT-063. Adapter protocol version negotiation is one-directional; no runtime-initiated version advertisement [Low]

Section 15.4.2 describes the adapter sending `AdapterInit` with `adapterProtocolVersion` and the gateway responding with `selectedVersion`. This negotiation is between the adapter and the gateway. However, the runtime binary itself (the agent process) has no mechanism to advertise its protocol version to the adapter. The adapter communicates with the runtime over stdin/stdout JSON Lines, and there is no version handshake in that channel. The only forward-compatibility rule is "runtimes MUST ignore unrecognized fields." If the adapter adds a new required field to the `message` type in a future version, the runtime has no way to signal that it does not understand it. This is acceptable for purely additive changes but becomes problematic for semantic changes. The spec should consider adding a version field to the first stdin message or to the adapter manifest that indicates the stdin/stdout protocol version.

### PRT-064. `delivery` field closed enum on `MessageEnvelope` blocks future delivery modes without breaking change [Low]

Section 15.4.1 defines `delivery` as a "closed enum" with values `"immediate"` and `"queued"`, and states "No other values are valid. The gateway rejects unknown `delivery` values with `400 INVALID_DELIVERY_VALUE`." This is a closed enum by design, meaning adding a new delivery mode (e.g., `"broadcast"`, `"priority"`) is a breaking change per Section 15.5 item 5. Given the spec's own statement that the `MessageEnvelope` "accommodates all future conversational patterns without schema changes," the closed-enum constraint on `delivery` contradicts this future-proofing claim. Consider making `delivery` an open string with known values, rejecting only syntactically invalid values.

### PRT-065. CRD graduation timeline creates extended alpha stability exposure [Medium]

Section 15.5 item 4 defines graduation criteria: `v1alpha1` to `v1beta1` requires "Phase 2 benchmark completion and no breaking field changes for 60 days"; `v1beta1` to `v1` requires "GA load-test sign-off (Phase 14.5) and no breaking changes for 6 months." Given the build sequence (Section 18), Phase 2 is an early phase and Phase 14.5 is late. This means the CRDs will remain at `v1alpha1` through Phases 3-13+ (potentially 6-12 months of development), and then at `v1beta1` for another 6 months minimum. During this entire period, breaking changes are permissible per the alpha/beta stability tiers. Any deployer running a non-production deployment during this window faces repeated CRD migration churn. The spec should define a compatibility commitment for the internal CRD consumers (`PodLifecycleManager`, `PoolManager` interfaces) during the alpha period, even if the CRD surface itself is alpha.

### PRT-066. `from.kind` closed enum blocks external adapter protocol evolution [Low]

Section 15.4.1 defines `from.kind` as "a closed enum with exactly four values": `client`, `agent`, `system`, `external`. Adding a new origin kind (e.g., `webhook`, `scheduler`, `a2a_agent` -- distinct from `external`) would be a breaking change. Since the `from` object is adapter-injected and runtimes are told to ignore unknown fields, the closed-enum constraint provides no benefit to runtimes but limits the gateway's ability to evolve the origin model. This should be an open string with known values.

### PRT-067. REST API `/v1/` path prefix versioning strategy has no specified coexistence plan [Medium]

Section 15.5 item 1 states: "Breaking changes require a new version (`/v2/`). The previous version is supported for at least 6 months." However, the spec does not define how `/v1/` and `/v2/` coexist operationally. Do both versions route to the same service layer? Are both session records accessible from both API versions? Can a session created via `/v1/` be managed via `/v2/` endpoints? Since the `ExternalAdapterRegistry` routes by path prefix and the REST adapter owns `/v1`, a `/v2/` REST adapter would need its own path prefix entry. The spec should define: (a) whether multi-version REST adapters coexist in the registry, (b) cross-version session accessibility semantics, and (c) how the OpenAPI spec endpoint serves both versions.

### PRT-068. Expand-contract schema migration lacks defined interaction with `schemaVersion` field [Medium]

Section 10.5 describes the expand-contract Postgres migration pattern, and Section 15.5 item 7 describes the `schemaVersion` integer on persisted records. However, the spec never defines when `schemaVersion` is incremented relative to the expand-contract phases. During Phase 1 (expand -- add new columns), do new gateway replicas write records at the new `schemaVersion` while old replicas write at the old version? If so, the Phase 1 window creates a mixed-version period where both `schemaVersion: N` and `schemaVersion: N+1` records coexist. The durable-consumer forward-read rule handles this, but the spec should explicitly state: (a) new `schemaVersion` values are written only after Phase 2 (new code deployed), not during Phase 1, and (b) Phase 3 (contract -- drop old columns) must not be applied until all consumers are upgraded per the 90-day migration window SLA.

### PRT-069. `ExternalProtocolAdapter` interface lacks a protocol version discovery mechanism [Low]

The `ExternalProtocolAdapter` interface (Section 15) exposes `Capabilities()` returning `AdapterCapabilities`, which includes `Protocol` as a string identifier (e.g., `"mcp"`, `"a2a"`). However, there is no field for the protocol version the adapter implements (e.g., MCP 2025-03-26 vs. 2024-11-05, or A2A v1 vs. v2). The `HandleDiscovery` method receives `AdapterCapabilities` but has no version information to include in discovery responses. Clients cannot determine which protocol version a given adapter supports without attempting a connection and negotiating. `AdapterCapabilities` should include a `ProtocolVersion string` field.

### PRT-070. Webhook event `schemaVersion` and evolution strategy unspecified [Medium]

Section 14 defines the webhook delivery model (`callbackUrl` with `SessionComplete` payloads) including event schemas for `session.completed`, `session.failed`, `session.terminated`, etc. However, these webhook event schemas have no `schemaVersion` field, unlike every other persisted/delivered schema in the spec (TaskRecord, OutputPart, MessageEnvelope, WorkspacePlan, billing events, audit events). If the webhook payload schema evolves (e.g., a new field in `session.completed`), receivers have no version signal to select the correct deserialization path. The spec should add a `schemaVersion` field to the webhook event envelope, consistent with the bifurcated consumer rules in Section 15.5 item 7.

### PRT-071. Intra-pod MCP version support lags external MCP version deprecation without independent timeline [Low]

Section 15.4.3 states that intra-pod MCP version support "follows the same rolling two-version policy as the gateway (Section 15.5 item 2)." This means when the gateway drops support for an old MCP version on the external-facing side, the adapter's intra-pod MCP servers also drop it. However, the runtime binary's MCP client library version is baked into the container image and cannot be updated without a runtime image rebuild. A runtime image built against MCP 2024-11-05 that is still deployed when the platform drops that version will fail to connect to the intra-pod MCP servers. The spec should decouple the intra-pod MCP deprecation timeline from the external-facing timeline, or require the adapter to support intra-pod MCP versions for at least one additional cycle beyond the external deprecation.

### PRT-072. `UNREGISTERED_PART_TYPE` rejection for unprefixed custom types breaks forward-compatibility of `OutputPart.type` [High]

Section 15.4.1 states that `type` is "an open string -- not a closed enum" and that "Types may be added to the registry in minor releases." It also states: "The gateway enforces this at ingress: `OutputPart` objects with an unprefixed `type` not in the current registry are rejected with a `400 Bad Request` error citing `UNREGISTERED_PART_TYPE`." This creates a protocol-level forward-compatibility failure: if a newer runtime emits a type that was added to the registry in a newer gateway version, and the message passes through an older gateway (e.g., during a rolling upgrade, or a delegation chain crossing gateway versions), the older gateway will reject it as `UNREGISTERED_PART_TYPE`. The spec says types can be added in minor releases, but the ingress rejection means all gateways must be upgraded before any runtime can emit a newly-registered type. This effectively makes registry additions a coordinated breaking change rather than a minor release. The rejection should be softened to a warning/annotation for unprefixed types not in the current registry, with the `x-vendor/` prefix requirement remaining a convention rather than a hard enforcement.

### PRT-073. `WorkspacePlan.schemaVersion` evolution has no consumer-side specification [Low]

Section 14 defines the `WorkspacePlan` with a `schemaVersion: 1` field, and Section 15.5 item 7 lists `WorkspacePlan` among the types that carry `schemaVersion`. However, unlike `TaskRecord`, `OutputPart`, and `MessageEnvelope`, there is no explicit consumer obligation for `WorkspacePlan`. The gateway is the primary consumer (it materializes the workspace), but the spec does not define what happens if a `WorkspacePlan` stored at `schemaVersion: 2` is read by a gateway running at `schemaVersion: 1`. Should the gateway reject the session? Forward-read? The `WorkspacePlan` is client-supplied, so a newer client could submit a v2 plan to an older gateway. The spec should define the consumer obligation explicitly.

### PRT-074. Billing event and audit event schemas listed under `schemaVersion` but no evolution path documented [Medium]

Section 15.5 item 7 lists billing events (Section 11.2.1) and audit events (`EventStore`) among the record types carrying `schemaVersion`. The durable-consumer forward-read rule and the 90-day migration window SLA apply. However, neither Section 11.2.1 (billing) nor Section 11.7 (audit) define the initial schema version, the guaranteed field set at each version, or what constitutes a breaking vs. additive change to these schemas. The `OutputPart` has a per-type guaranteed field table (Section 15.4.1); `TaskRecord` and `MessageEnvelope` have explicit schema evolution constraints. Billing and audit events have none. Given their 13-month retention requirement and their consumption by external billing systems, the lack of a schema contract makes the forward-read obligation unimplementable for third-party billing consumers. The spec should define at minimum the v1 guaranteed field set for billing events and audit events.

### PRT-075. Session-lifetime exception for deprecated MCP versions creates unbounded support window [Low]

Section 15.2 states that when a deprecated MCP version exits the deprecation window, "connections that are already established and mid-session at that instant MUST NOT be forcibly terminated" and may continue "for the duration of its session (up to `maxSessionAgeSeconds`)." With `maxSessionAgeSeconds` defaulting to 3600s (1 hour) and being configurable up to the Runtime's `limits.maxSessionAge`, a session could theoretically run for days. The spec acknowledges this with a preflight warning for sessions older than 1 hour, but does not define a hard ceiling. An operator who deploys a gateway binary that drops the old version while a long-running session exists will hit the "falls back to the nonce-handshake-only serialization path with a `schema_version_ahead` degradation annotation" fallback -- which is under-specified (what does "nonce-handshake-only serialization" mean for actual message delivery?). The fallback behavior should be fully specified or the session-lifetime exception should have a hard cap.

---

## 6. Developer Experience (DXP)

### DXP-059. `slotId` field absent from Protocol Reference message schemas [Medium]

The summary tables (lines 7589-7607) state that `message`, `tool_result`, `response`, and `tool_call` all carry `slotId` in concurrent-workspace mode. The `MessageEnvelope` format (line 7835) includes `slotId`. However, the detailed Protocol Reference schemas for each message type omit `slotId`:

- Inbound `tool_result` schema (lines 7940-7948): no `slotId` field
- Outbound `response` schema (lines 7975-7986): no `slotId` field
- Outbound `tool_call` schema (lines 7996-8002): no `slotId` field

Only the inbound `message` example (line 7915) includes `slotId`. A runtime author implementing concurrent-workspace mode would see `slotId` in the summary table and the `message` example, but find no schema guidance for including it in outbound `response` and `tool_call` messages or for expecting it in inbound `tool_result` messages. Each Protocol Reference schema section should include `slotId` as an optional field with a note that it is present only in concurrent-workspace mode.

---

### DXP-060. No mechanism to deliver per-slot `cwd` to the runtime binary in concurrent-workspace mode [Medium]

Section 6.4 (line 2672-2673) states: "The adapter sets each slot's `cwd` to `/workspace/slots/{slotId}/current/` when dispatching a task to the runtime" and "Runtime receives `cwd` per slot and operates within it. The runtime MUST NOT assume a global `/workspace/current` path in concurrent-workspace mode."

However, the stdin binary protocol has no field for communicating `cwd`. The `message` inbound schema has `type`, `id`, `input`, `from`, `threadId`, `delivery`, `delegationDepth`, `slotId` -- but no `cwd`. The adapter manifest (line 728) is described as "stable for the duration of a single task or session" and contains no per-slot `cwd` mapping. The spec never defines how the adapter communicates the slot's working directory to the runtime through the binary protocol. This is a gap: a concurrent-workspace runtime author has no documented way to learn which directory to use for a given slot, short of inferring it from a convention (`/workspace/slots/{slotId}/current/`), which is filesystem-layout knowledge the spec says belongs to the adapter, not the runtime.

**Fix:** Either add a `cwd` field to the `message` schema for concurrent-workspace messages, or document the explicit convention that concurrent-workspace runtimes derive `cwd` from `slotId` using the pattern `/workspace/slots/{slotId}/current/`.

---

### DXP-061. Full-tier pseudocode does not handle `task_complete` / `task_ready` lifecycle messages [Medium]

The Full-tier sample echo runtime pseudocode (lines 8360-8443, Section 15.4.4) handles `checkpoint_request`, `interrupt_request`, `credentials_rotated`, `deadline_approaching`, and `terminate` on the lifecycle channel. However, it does not handle `task_complete` or `task_ready` -- the between-task signals required for task-mode pod reuse (Section 5.2, lines 2058-2063). A runtime author using this pseudocode as a template for a task-mode runtime would have no example of how to implement the `task_complete` -> `task_complete_acknowledged` -> scrub -> `task_ready` cycle.

This is significant because task-mode pod reuse is a key Full-tier feature. The pseudocode should include a `case "task_complete"` handler that emits `task_complete_acknowledged` and a `case "task_ready"` handler that re-reads the adapter manifest and resets per-task state.

---

### DXP-062. Lifecycle capability `"task_lifecycle"` not included in Full-tier pseudocode's `lifecycle_support` response [Low]

The Full-tier sample pseudocode (line 8371) declares:
```
supported = ["checkpoint", "interrupt", "deadline_signal"]   // omit credential_rotation if unused
```

This omits `"task_lifecycle"` from the supported capabilities list. Per Section 4.7 (line 711), `"task_lifecycle"` governs the `task_complete` / `task_complete_acknowledged` / `task_ready` exchange required for task-mode pod reuse. A runtime author copying this pseudocode for a task-mode Full-tier runtime would not declare `task_lifecycle` support and would consequently not receive between-task signals.

The comment says "omit credential_rotation if unused" but does not mention `task_lifecycle`. At minimum, add a comment like `// add "task_lifecycle" for task-mode pods`.

---

### DXP-063. Overlap between `shutdown` on stdin and `terminate` on lifecycle channel is underspecified for Full-tier runtimes [Medium]

Full-tier runtimes receive shutdown signals through two independent channels simultaneously:
- `{type: "shutdown"}` on stdin (line 7929-7935)
- `{type: "terminate"}` on the lifecycle channel (line 717)

The pseudocode comment (line 8436) acknowledges this: "shutdown arrives on stdin even for Full-tier; lifecycle terminate may arrive first -- handle whichever comes first." But the spec does not define:

1. Whether the adapter guarantees both signals are always sent, or whether only one is sent in certain scenarios.
2. The ordering guarantee (if any) between the two signals.
3. Whether the `deadlineMs` values in each signal are identical or may differ.
4. What a runtime should do if it receives `terminate` on lifecycle but never sees `shutdown` on stdin (e.g., if stdin is blocked by an in-progress read).

The `terminate` lifecycle message has a `reason` field with richer semantics (`session_complete`, `budget_exhausted`, `eviction`, `operator`) while `shutdown` on stdin has only `reason: "drain"` in the example. A runtime author implementing both channels cannot determine whether the richer `terminate.reason` should inform their cleanup behavior differently from `shutdown.reason`, or whether they should treat both identically.

**Fix:** Add a "Dual-channel shutdown" subsection to Section 15.4.1 or 15.4.3 that specifies: (a) the adapter always sends both, (b) which arrives first, (c) whether `deadline_ms` values are synchronized, and (d) whether the runtime should handle whichever arrives first and ignore the other.

---

### DXP-064. `shutdown` reason values not enumerated in the Protocol Reference [Low]

The `shutdown` inbound message schema example (line 7932) shows `"reason": "drain"`, and the prose (line 7933-7935) only describes the `deadline_ms` behavior. The lifecycle `terminate` message (line 717) has a defined closed enum for `reason`: `"session_complete" | "budget_exhausted" | "eviction" | "operator"`. But `shutdown` on stdin has no corresponding enum -- runtime authors cannot determine the full set of valid `reason` values for the stdin `shutdown` message.

Is `"drain"` the only reason? Can `"budget_exhausted"` or `"session_complete"` appear on stdin `shutdown`? The lack of a closed enum means runtime authors cannot match on `reason` for differentiated cleanup behavior.

---

### DXP-065. `tool_call` for adapter-local tools uses `arguments` but `tool_result.content` uses `OutputPart[]` -- asymmetry not explained for Minimum-tier authors [Low]

The `tool_call` schema (line 7996-8002) uses an `arguments` object validated against the tool's input schema. The `tool_result` schema (line 7940-7948) returns `content` as an `OutputPart[]` array. For Minimum-tier runtimes that only use adapter-local tools (`read_file`, `write_file`, etc.), this means:

- To call a tool, they emit a flat JSON object with `arguments: { "path": "..." }`.
- The result comes back as `content: [{ "type": "text", "inline": "file contents" }]` -- a full `OutputPart` array.

The spec never explicitly states whether Minimum-tier runtimes can use a simplified content format in `tool_result` responses from adapter-local tools, or whether the full `OutputPart` schema (with `schemaVersion`, `id`, `mimeType`, `annotations`, `parts`, `status`) is always returned. The adapter-local tool section (line 8083) only mentions the error case format (`isError: true`, `content[0].inline` set to `"path_outside_workspace"`).

A Minimum-tier runtime author needs to know: when I call `read_file`, will the `tool_result.content` always be `[{"type": "text", "inline": "..."}]` (minimal form), or could it include the full `OutputPart` envelope with `schemaVersion`, `id`, etc.? This affects parsing complexity.

---

### DXP-066. Runtime Author Roadmap item 7 misdirects Standard-tier authors to lifecycle channel documentation [Low]

The Runtime Author Roadmap (line 8461) says for Standard-tier item 7:

> **Section 4.7** -- Runtime Adapter. Read for the **adapter manifest field reference** (`platformMcpServer.socket`, `connectorServers`, `mcpNonce`) and the **lifecycle channel message schemas** (Part B -- the bidirectional JSON Lines stream at `@lenny-lifecycle`).

Standard-tier runtimes do not open the lifecycle channel -- the Tier Comparison Matrix (line 8219) shows lifecycle channel as "N/A -- operates in fallback-only mode" for Standard tier. Directing Standard-tier authors to read lifecycle channel message schemas is misleading. The parenthetical disclaimer ("The gRPC RPC table at the top of 4.7 is not relevant to binary authors") correctly excludes irrelevant content, but the reference to "lifecycle channel message schemas (Part B)" should also be excluded or marked as "for Full-tier only."

---

### DXP-067. `adapter-manifest.json` marks `platformMcpServer` and `lifecycleChannel` as required fields even for Minimum tier [Medium]

The adapter manifest field reference (lines 776-780) marks both `platformMcpServer` and `lifecycleChannel` as **Required: Yes**. The "Tier relevance" column shows `platformMcpServer` is relevant to "Standard, Full" and `lifecycleChannel` to "Full" only.

For Minimum-tier pods, these fields are irrelevant -- the runtime does not connect to them. Yet they are marked as always present in the manifest. This creates ambiguity: will a Minimum-tier pod's manifest contain a `platformMcpServer` object with a socket path that points to nothing? Or will the adapter omit the field for Minimum-tier pods (contradicting "Required: Yes")?

A runtime author implementing manifest version validation may incorrectly reject a manifest that lacks these fields, or conversely may try to connect to a socket that the adapter isn't serving. The field reference should specify the behavior: either the fields are always present (even if the socket is not active), or they are absent for tiers that don't use them (in which case "Required" should be conditional).

---

### DXP-068. No specification for how adapter-local `tool_call`/`tool_result` interacts with concurrent MCP tool calls at Standard/Full tier [Medium]

Section 15.4.1 (line 7963) states: "Tool calls use synchronous request/response semantics within the stdin/stdout channel. The agent emits a `tool_call`, then continues reading stdin until it receives the matching `tool_result` (identified by `id`)."

Section 15.4.3 (line 7970) states for Standard tier: "The stdin `tool_call`/`tool_result` channel is used for adapter-local tools only" while MCP tools are accessed via Unix socket MCP client connections.

The spec does not address concurrency between the two tool-calling mechanisms. Can a Standard-tier runtime:
1. Emit a `tool_call` for `read_file` on stdout, and simultaneously call `lenny/delegate_task` via MCP?
2. Have multiple in-flight `tool_call` requests on stdout while also having MCP tool calls in progress?
3. Expect that `tool_result` ordering on stdin is strictly FIFO relative to `tool_call` ordering on stdout, or can results arrive out of order?

The statement "Agents may have multiple outstanding `tool_call` requests; results may arrive in any order" (line 7961) answers question 3 partially (results can arrive out of order), but question 1 and 2 are unanswered. This matters for runtimes that want to parallelize workspace reads while delegating.

---

### DXP-069. `write_file` adapter-local tool lacks specification for creating intermediate directories [Low]

The four adapter-local tools are listed (lines 8024-8027) with one-line descriptions. The `write_file` tool is described as "Create or overwrite a file in the workspace." There is no specification of whether `write_file` creates intermediate directories (like `mkdir -p` behavior) or requires the parent directory to already exist.

A runtime author calling `write_file` with `arguments: {"path": "src/deep/nested/file.txt", "content": "..."}` cannot determine from the spec whether this will succeed if `src/deep/nested/` doesn't exist. The `inputSchema` example (lines 8047-8058) shows only `path` and `content` properties -- no `createDirectories` flag. Since adapter-local tools are the only file manipulation mechanism at Minimum tier, this behavioral ambiguity affects the simplest integration path.

---

### DXP-070. `delete_file` described as deleting "a file or empty directory" but no tool exists for deleting non-empty directories [Low]

The `delete_file` tool (line 8027) is described as "Delete a file or empty directory from the workspace." There is no adapter-local tool for recursively removing a non-empty directory. Runtime authors cleaning up workspace state (e.g., removing a `node_modules/` tree) have no adapter-local mechanism to do so in a single call.

This is a minor DX gap -- workarounds exist (enumerate and delete individual files) -- but it's worth noting as a specification gap since the adapter-local tools are described as the primary workspace manipulation interface.

---

### DXP-071. Sample echo runtime pseudocode (Standard tier) does not close MCP connections on shutdown [Low]

The Standard-tier pseudocode (lines 8280-8342) handles `shutdown` by simply exiting:

```
case "shutdown":
    platform_mcp.close()
    for conn in connector_connections: conn.close()
    exit(0)
```

This is correct. However, the Minimum-tier pseudocode (lines 8258-8282) has no cleanup at all on `shutdown`:

```
case "shutdown":
    exit(0)
```

While this is technically valid for Minimum tier (no resources to clean), the pseudocode doesn't show any final `response` emission. If the runtime was mid-processing a `message` when `shutdown` arrives, should it attempt to emit a partial response? The Protocol Reference (line 7935) says "Agent must finish current work and exit within `deadline_ms`" -- which implies completing the response is desired. The pseudocode does not model this interleaving.

---

### DXP-072. `observability.otlpEndpoint` in adapter manifest but no guidance on trace context propagation from stdin messages [Low]

The adapter manifest includes `observability.otlpEndpoint` (line 792) for runtime OTel SDK configuration. However, the stdin protocol messages carry no trace context (no `traceParent` or `traceparent` header equivalent). A runtime author who configures their OTel SDK against the manifest endpoint will emit spans, but those spans will be disconnected from the gateway's trace for that session.

Section 15.4.5 (Runtime Author Roadmap) does not mention observability integration at any tier. Runtime authors who want correlated traces have no guidance on how to propagate trace context from the gateway through the adapter to the runtime binary. The `message` envelope could carry a `traceContext` field, or the adapter manifest could include a per-session trace parent, but neither is specified.

---

### DXP-073. `one_shot` interaction mode allows exactly one `lenny/request_input` call at Standard+ tier, but no guidance on idempotent handling of the race between `REQUEST_INPUT_TIMEOUT` and a late client response [Low]

Section 5.1 (line 1699) specifies that when a `one_shot` runtime's `request_input` times out, the runtime receives `REQUEST_INPUT_TIMEOUT` and must produce either a best-effort response or a structured error. But what happens if the client's response arrives milliseconds after the timeout fires? The runtime may be mid-generation of its best-effort response when a `tool_result` (from the resolved `request_input`) arrives on stdin.

The spec says "The adapter validates this -- a `tool_result` with an unknown `id` is dropped" (line 7961). But for `request_input`, the resolution comes via MCP, not stdin `tool_result`. The interaction between a timeout-resolved MCP call and a late-arriving client response is not specified from the runtime's perspective: does the MCP `tools/call` for `lenny/request_input` return the timeout error, and then a subsequent message on stdin delivers the late response? Or is the late response silently dropped?

This race condition is narrow but relevant for `one_shot` runtimes that want to be robust.

---

### DXP-074. No documented maximum size or encoding constraints for `status` outbound message [Low]

The `status` outbound message (line 8091-8095) has fields `state` and `message` but no specification of:
- Maximum length of `state` or `message` strings
- Whether `state` is a closed enum or open string
- Whether the adapter rate-limits or batches status messages
- Maximum frequency of status emissions

A runtime that emits verbose status updates on every processing step could flood the adapter's stdout parser. The spec says nothing about back-pressure or throttling for status messages, unlike `lenny/output` which has size limits documented.

---

### DXP-075. `Go path.Match` extended with `**` for `sdkWarmBlockingPaths` referenced but not fully specified for runtime authors [Low]

Section 6.1 (line 1691) states that `sdkWarmBlockingPaths` patterns "follow Go `path.Match` extended with `**` (see matching contract in Section 6.1)." This is relevant to runtime authors who implement SDK-warm mode (`preConnect: true`) because their workspace file layouts determine whether demotion occurs.

However, the matching semantics of `**` are not defined in the spec. Go's `path.Match` does not support `**` natively -- the "extension" is a Lenny-specific addition. Whether `**` matches zero or more path segments (like `.gitignore`) or has different semantics is not documented. Runtime authors who want to understand when their SDK-warm pool will be demoted cannot predict matching behavior from the spec alone.

---

---

## 7. Operator Experience (OPS)

### OPS-067. No rollback procedure for failed CRD upgrades [Medium]

Section 10.5 defines the `RuntimeUpgrade` state machine for pool image upgrades with `start`, `proceed`, `pause`, `resume`, and `rollback` commands. However, the spec says the `rollback --restore-old-pool` option is "only valid while the old `SandboxTemplate` CRD still exists" (line 10302). The spec does not define when the old `SandboxTemplate` is deleted, what happens if an operator attempts rollback after it has been deleted, or how to recover if the old CRD was cleaned up prematurely. Operators need a clear answer for: after what upgrade phase is rollback no longer possible, and what is the recovery path at that point?

### OPS-068. Bootstrap seed Job depends on Secrets that must pre-exist but ordering is not enforced by Helm [Medium]

Section 17.6/4.9 states that the `lenny-bootstrap` Job "does NOT create Kubernetes Secrets" and Secrets "must exist before the bootstrap Job runs" (line 1613). The spec acknowledges ArgoCD sync-wave ordering as a solution (line 1628), but does not specify Helm hook ordering for non-GitOps deployments. Operators using plain `helm install` have no mechanism to ensure Secrets exist before the bootstrap Job runs. The Helm chart should use hook weights or init containers to enforce ordering, but no such mechanism is specified.

### OPS-069. `lenny-ctl preflight` standalone mode embeds business logic, contradicting the "thin client" design [Low]

Section 24 states `lenny-ctl` is "a thin client over the Admin API with zero business logic" then immediately states `lenny-ctl preflight` is "the only subcommand that carries business logic" (line 10262). This is a stated contradiction. The exception is justified but the "zero business logic" claim at the top of Section 24 is factually incorrect and should be qualified.

### OPS-070. RBAC patch for operationally-added credentials requires manual intervention [Medium]

Section 4.9 states that the Token Service's RBAC `resourceNames` list is "populated at install time from all `secretRef` values declared in bootstrap configuration" and that "operationally-added credentials require a manual RBAC patch or a re-run of the `helm upgrade` with updated values" (line 1192). At Tier 2/3 scale, this means every `lenny-ctl admin credential-pools add-credential` call requires a subsequent manual `kubectl` RBAC patch. The spec acknowledges the CLI "emits the required RBAC patch command" but does not specify automation. This creates an error-prone operational gap where the Token Service cannot read a newly added credential until the operator manually patches RBAC.

### OPS-071. Preflight Job cannot verify etcd Secret encryption programmatically [Medium]

Section 17.6 and line 1175 state that the preflight Job "emits a non-blocking warning when etcd Secret encryption cannot be verified" because "programmatic verification requires etcd access that the preflight Job may not have." This means the preflight check for a critical security requirement (encryption at rest for credential Secrets) will always be a warning, never a hard pass/fail. Operators who see the warning have no automated way to confirm encryption is active. The spec should define a verification procedure or recommend a specific post-install verification step.

### OPS-072. `maxTasksPerPod` is required with no default but error message is unspecified [Low]

Section 5.2 states `maxTasksPerPod` is "required with no default" and "the pool controller rejects task-mode pool definitions that omit it" (line 2116). The error message format for this rejection is not specified, unlike other validation rejections (e.g., `acknowledgeProcessLevelIsolation` which has a specific error text). Operators will encounter a rejection without a clear reference to the relevant documentation section.

### OPS-073. Token Service secret informer resync interval is configurable but default behavior on Secret deletion is undefined [Medium]

Section 4.9 states the Token Service uses a Kubernetes informer with a configurable `secretResyncInterval` (default 30s) to watch Secrets (line 1615). The spec defines behavior when a Secret is created or updated, and the metric tracks `success`, `not_found`, and `parse_error` outcomes. However, the spec does not define what happens to active leases when a Secret is deleted while credentials backed by it are in use. Does the Token Service mark the credential as `unavailable`? Do active leases continue with their already-materialized credentials? Is there an alert?

### OPS-074. Node drain timeout interaction with concurrent-workspace pods is a warning, not a rejection [Medium]

Section 5.2 states that when `terminationGracePeriodSeconds` exceeds the node drain timeout (commonly 600s), "the kubelet will SIGKILL the pod before checkpoints complete, causing data loss for in-flight slots." Yet the CRD validation webhook only "emits a warning (not a rejection)" (line 2159). The spec provides `maxTerminationGracePeriodSeconds` as an optional hard ceiling, but it defaults to unset. This means by default, operators can deploy configurations that guarantee data loss on node drain without any blocking validation. Given the fail-closed philosophy applied elsewhere in the spec, this should be a rejection by default or at minimum the `maxTerminationGracePeriodSeconds` should be recommended as a required field.

### OPS-075. Credential pool sizing formula referenced but not provided inline [Medium]

Section 4.9 states "Pool sizing scales with tier -- at Tier 3, deployers may need hundreds to over a thousand credentials per pool. See Section 17.8.2 ('Credential pool sizing') for the sizing formula and per-tier starting values" (line 1141). However, based on the reading of the full document, the credential pool sizing formula and per-tier starting values in Section 17.8.2 are part of a very dense section that may not actually contain these specific formulas. The cross-reference should be verified to ensure the formula is actually present at the cited location.

### OPS-076. `PoolConfigDrift` alert detection runs in the gateway but gateway crash also leaves the alert unmonitored [Medium]

Section 4.6.2 states the `PoolConfigDrift` alert "runs in the gateway (which reads both Postgres and CRD state) rather than in the PoolScalingController itself, so the alert fires even when the controller is completely down" (line 609). However, this creates a dependency on gateway availability for detecting controller failures. If both the gateway and PoolScalingController are down simultaneously (e.g., during a cluster-wide incident), no component detects the config drift. The spec should acknowledge this gap or specify an external probe.

### OPS-077. Emergency credential revocation runbook requires provider-side key rotation for direct mode but no automation is provided [Medium]

Section 4.9 states that after revoking a direct-mode credential, "operators MUST also rotate or delete the key at the provider" because "the underlying API key continues to exist at the provider" (line 1579). The revocation endpoint is Lenny-internal only. There is no integration point, webhook, or automation hook for triggering provider-side rotation. At incident velocity, requiring operators to manually log into each provider console (Anthropic, AWS, GCP, Azure) to rotate keys is operationally brittle.

### OPS-078. `bootstrapMinWarm` override and formula-driven scaling interaction is underspecified for partial convergence [Medium]

Section 4.6.2 and Section 17.8.2 define a bootstrap mode with 5 convergence criteria and a 48-hour convergence window. The `lenny-ctl admin pools exit-bootstrap` command allows early exit "when early traffic data is sufficient" (line 10296). However, the spec does not define what happens if an operator exits bootstrap mode before all 5 convergence criteria are met -- does the formula use incomplete data, fall back to defaults, or produce an error? The interaction between the manual exit and the convergence criteria is ambiguous.

### OPS-079. `setupPolicy.timeoutSeconds` uses Maximum merge rule but can be absent [Low]

Section 5.1 states `setupPolicy.timeoutSeconds` defaults to "waits indefinitely if absent" (line 1909), and the merge rule is "Maximum -- gateway uses max(base, derived)" (line 1845). If the base runtime sets no timeout (indefinite) and the derived runtime sets 120s, the merge behavior is undefined: `max(infinity, 120)` is infinity, which contradicts the derived runtime's intent. The spec should clarify the semantics when one side is absent/indefinite.

### OPS-080. Multiple admission webhooks with `failurePolicy: Fail` create cascading unavailability risk [High]

The spec defines at least 6 separate `ValidatingAdmissionWebhook` deployments with `failurePolicy: Fail` (fail-closed):
- `lenny-tenant-label-immutability` (Section 5.2)
- `lenny-direct-mode-isolation` (Section 4.9)
- `lenny-t4-node-isolation` (Section 6.4)
- cosign image verification webhook (Section 5.3)
- Postgres-authoritative state webhook (Section 4.6.3)
- RuntimeClass-aware admission policies (Section 5.3)

All fail-closed means any single webhook unavailability blocks all pod admission in agent namespaces. The spec mentions `replicas: 2` and PDB for the tenant-label webhook but does not specify HA requirements for the other 5 webhooks. A single webhook outage blocks all session creation. The spec should define a unified HA requirement (minimum replicas, PDB, health monitoring) for all fail-closed webhooks.

### OPS-081. No `lenny-ctl` command for listing active sessions across the platform [Low]

Section 24.11 defines `lenny-ctl admin sessions get <id>` and `lenny-ctl admin sessions force-terminate <id>` for individual session investigation. There is no `lenny-ctl admin sessions list` command. During an incident, operators cannot enumerate all active sessions, sessions in `resume_pending` or `awaiting_client_action`, or sessions assigned to a specific pool or pod. The only discovery mechanism is querying Postgres directly or using the `GET /v1/admin/sessions` endpoint (which is not listed in the admin API table in Section 15.1).

### OPS-082. `maxAwaitingClientActionSeconds` default (900s) and `maxResumeWindowSeconds` default (900s) are identical, creating confusing expiry behavior [Low]

Section 7.3 states `maxResumeWindowSeconds` defaults to 900s (line 3026) and `maxAwaitingClientActionSeconds` defaults to 900s (line 3057). A session that spends the full `resume_pending` window (900s) transitions to `awaiting_client_action` and gets another full 900s window. The total wall-clock time before automatic expiry is 1800s (30 minutes), but the operator-facing configuration does not make this obvious. There is no single "max recovery time" configuration; operators must reason about the sum of two independent timers.

### OPS-083. `lenny_pool_bootstrap_mode` gauge referenced in Section 4.6.2 but not defined in the observability section [Low]

Section 4.6.2 references a `lenny_pool_bootstrap_mode` gauge and a `PoolBootstrapMode` alert (line 600), directing the reader to Section 17.8.2 for the full cold-start bootstrap procedure. This metric is not listed in the main observability metrics section (Section 16). Operators may not discover it when setting up monitoring dashboards.

### OPS-084. Semantic cache `DeleteByUser` error halts the erasure job with no skip-and-continue option [Medium]

Section 4.9 states "The erasure job treats a `DeleteByUser` call that returns an error as a hard failure -- the erasure job halts and does not proceed to subsequent stores" (line 1489). This is intentionally strict for data protection, but it means a malfunctioning pluggable `SemanticCache` implementation can permanently block GDPR erasure for a user. The only recovery path is `retry` (Section 24.12), which will hit the same error again if the cache implementation is broken. There is no mechanism to skip the cache store and continue with remaining stores, creating a potential compliance deadlock.

### OPS-085. `DeliveryMode: proxy` is the "recommended default for multi-tenant deployments" but the Helm default is unspecified [Medium]

Section 4.9 states "Proxy mode is the recommended default for multi-tenant deployments" (line 1432) and the YAML example shows `deliveryMode: proxy` with a comment "(default: proxy for multi-tenant, direct for single-tenant)" (line 1454). However, the mechanism by which the Helm chart selects this default based on `tenancy.mode` is not specified. Deployers who create credential pools via the admin API (not Helm) must set `deliveryMode` explicitly; the spec does not state what the API default is when `deliveryMode` is omitted from the request body.

### OPS-086. Billing event stream two-tier failover references in-memory WAL but WAL durability guarantee is absent [Medium]

The summary references a "two-tier failover (Redis stream -> in-memory WAL -> Postgres)" for billing events. An in-memory WAL by definition is not durable across process crashes. If a gateway replica crashes while billing events are in the in-memory WAL (after Redis stream failure), those billing events are lost. The spec should state the acceptable billing event loss window and whether operators need to reconcile billing after gateway crashes.

### OPS-087. `lenny-ctl admin pools drain` returns `estimatedDrainSeconds` but no completion callback [Low]

Section 15.1 (line 7101) defines the pool drain endpoint returning `estimatedDrainSeconds` based on the longest active session age. There is no webhook, completion callback, or `lenny-ctl` command to block until drain completes. Operators must poll `GET /v1/admin/pools/{name}` to detect drain completion. For automated deployment pipelines, this requires a polling loop with no platform-native "wait for drain" primitive.

### OPS-088. `maxSessionAgeSeconds` and `maxIdleTimeSeconds` timer pausing during recovery states could lead to unexpectedly long-lived sessions [Medium]

Section 6.2 defines that `maxSessionAge` is paused during `suspended`, `resume_pending`, `resuming`, and `awaiting_client_action` states. `maxIdleTimeSeconds` is similarly paused during `input_required`, `suspended`, `resume_pending`, `resuming`, and `awaiting_client_action`. A session that cycles between `running` and recovery states could theoretically remain alive for days if it accumulates minimal running time between failures. There is no wall-clock hard cap that bounds the total session lifetime including paused states. The `maxResumeWindowSeconds` and `maxAwaitingClientActionSeconds` provide per-recovery caps, but the total number of recovery cycles is bounded only by `maxRetries` per failure -- and a session that recovers successfully resets the retry counter for the next failure.

---

## 8. Multi-Tenancy (TNT)

### TNT-056. Resolved Decision #3 Contradicts Spec's Actual Multi-Tenancy Model [Medium]

Section 19, resolved decision #3 states: "Logical isolation via filtering. Namespace-level isolation deferred." This summary is misleading and inconsistent with the comprehensive multi-tenancy enforcement described throughout the spec. The actual design employs: PostgreSQL RLS with `SET LOCAL` (Section 12.3), per-transaction tenant guard triggers, Redis key prefixing (`t:{tenant_id}:`), MinIO path-scoped tenant isolation (`/{tenant_id}/`), admission webhooks for tenant label immutability, and cross-tenant message prohibition. Describing this as "logical isolation via filtering" understates the enforcement model and could mislead readers who use Section 19 as a quick reference. Additionally, "namespace-level isolation deferred" is not clearly addressed in the spec body -- the spec never defines what namespace-level isolation would mean or when it would be reconsidered, leaving a vague unresolved aspiration in what is supposed to be a "resolved" decision.

**Location:** Section 19, decision #3 (line 10107)

---

### TNT-057. Cloud-Managed Pooler `lenny_tenant_guard` Trigger Is a Single Point of Tenant Isolation Defense [High]

Section 17.9 (cloud-managed profile, line 9862) states that cloud-managed proxies lacking `connect_query` support "must rely on the per-transaction tenant validation trigger (`lenny_tenant_guard`) as the second layer of RLS defense." However, in the cloud-managed profile, `lenny_tenant_guard` is not the *second* layer -- it is the *only* programmatic defense against stale `app.current_tenant` values from a prior connection. The `connect_query` sentinel (which resets `app.current_tenant` to `__unset__` on checkout) is absent, meaning if application code fails to call `SET LOCAL app.current_tenant` before a query, the trigger is the sole guard preventing cross-tenant data access via a stale session variable. The spec acknowledges this in Section 12.3 but the cloud-managed profile section (17.9) describes it as "second layer" when it is actually the only layer. This mislabeling could lead operators to underestimate the criticality of the trigger in cloud-managed deployments. Furthermore, the spec does not specify monitoring or alerting for `lenny_tenant_guard` trigger failures -- if the trigger is dropped, disabled, or encounters an error, there is no described detection mechanism.

**Location:** Section 17.9, cloud-managed profile table (line 9862); Section 12.3

---

### TNT-058. No Tenant Isolation for Circuit Breaker State in Redis [Medium]

Section 17.8.2 references circuit breakers stored in Redis (e.g., pool circuit-breaker, LLM proxy circuit breaker), and Section 24.7 provides `lenny-ctl admin circuit-breakers` commands that operate on circuit breakers by name only -- no tenant scoping. The circuit breaker state is stored in Redis keyed by circuit name (not prefixed with `t:{tenant_id}:`). This means a circuit breaker tripped by one tenant's traffic (e.g., a single tenant overwhelming an LLM provider) will affect all tenants sharing that circuit. This is the expected behavior for platform-global infrastructure concerns, but the spec never explicitly classifies circuit breakers as platform-global (Section 4.2's resource classification table should include them). The absence of per-tenant circuit breaker state means a noisy-neighbor tenant can degrade service for all tenants by tripping shared circuit breakers.

**Location:** Sections 4.2 (resource classification), 12.4 (Redis), 24.7 (circuit breaker CLI)

---

### TNT-059. Tenant Deletion Tombstone Format and Validation Not Specified [Medium]

The spec describes tenant record tombstoning after deletion to prevent tenant ID reuse (mentioned in the summary context as a key design point). However, the spec does not define: (a) the tombstone record schema (which fields are preserved vs. scrubbed), (b) how long the tombstone must be retained (indefinitely? or subject to its own retention policy?), (c) whether the tombstone survives database migrations and schema changes, (d) how the tombstone interacts with `billingErasurePolicy: exempt` -- if billing data is retained with pseudonymized `tenant_id`, does the tombstone need to map the original `tenant_id` to the pseudonymized version? The absence of a formal tombstone specification creates ambiguity for the Section 12.8 tenant deletion lifecycle implementation.

**Location:** Section 12.8 (tenant deletion lifecycle)

---

### TNT-060. `lenny-ctl admin tenants delete` Lacks Confirmation Safeguard for Multi-Tenant Data Destruction [Medium]

Section 24.10 defines `lenny-ctl admin tenants delete <id>` which initiates the tenant deletion lifecycle. For an operation that destroys all tenant data (sessions, credentials, billing records, workspace artifacts, audit logs), there is no described confirmation mechanism beyond requiring `platform-admin` role. The spec does not mention: (a) a `--confirm` flag or interactive confirmation prompt, (b) a dry-run mode showing what would be deleted, (c) a grace period or soft-delete stage visible to the tenant admin before irrevocable data destruction begins. While `force-delete` requires `--justification`, the standard `delete` command has no equivalent safeguard. Compare with the careful treatment of credential revocation which has separate `revoke-credential` and `re-enable` commands.

**Location:** Section 24.10 (line 10358)

---

### TNT-061. Per-Tenant Billing Sequence Multi-Region Gap Not Addressed in Cloud-Managed Profile [Medium]

Section 17.8.2 establishes per-region billing sequences for multi-region deployments (to avoid cross-region sequence coordination). Section 17.9 describes the cloud-managed profile using managed Postgres (RDS, Cloud SQL, Azure DB). However, the cloud-managed profile does not address how per-region billing sequences interact with managed Postgres in multi-region scenarios. Specifically: managed Postgres services (e.g., RDS) in different regions are separate instances, and the spec does not specify whether each region's managed Postgres instance maintains its own independent billing sequence namespace, or whether a central billing database is required. The `billing_seq_{tenant_id}` naming convention implies a single database; multi-region managed Postgres would create independent sequence spaces that could produce duplicate billing event IDs.

**Location:** Sections 17.8.2, 17.9

---

### TNT-062. Credential Pool Tenant Access Revocation Does Not Address In-Flight Leases [Medium]

Section 24.4 defines `lenny-ctl admin pools revoke-access --pool <name> --tenant <id>` which revokes a tenant's access to a pool. However, the spec does not specify what happens to active credential leases that the revoked tenant already holds from that pool. Options include: (a) immediately terminating all active leases (disruptive -- kills running sessions), (b) allowing existing leases to expire naturally (creates a window where the revoked tenant still has access), (c) preventing new lease acquisitions but not revoking existing ones. The same gap exists for `lenny-ctl admin runtimes revoke-access` (Section 24.3). Without specifying this behavior, the revocation is ambiguous: the tenant may retain effective access until existing sessions complete.

**Location:** Section 24.4 (line 10309), Section 24.3 (line 10287)

---

### TNT-063. `wellKnownAgentJsonMaxCards` Exposes Cross-Tenant Runtime Information [Low]

Section 21.1 describes `GET /.well-known/agent.json` returning a JSON array of all public agent cards, up to `wellKnownAgentJsonMaxCards` (default: 100). This endpoint requires no authentication. In a multi-tenant deployment, this means any unauthenticated caller can discover all public runtimes across all tenants. While runtimes are classified as platform-global resources (not tenant-scoped), their existence and metadata (capabilities, descriptions) may constitute business-sensitive information in competitive multi-tenant scenarios. The spec should clarify whether the A2A discovery endpoint respects any tenant-scoped visibility filtering, or explicitly document that all runtimes marked as public are visible to unauthenticated callers regardless of tenancy.

**Location:** Section 21.1 (line 10134)

---

### TNT-064. `lenny-ctl policy audit-isolation` Uses Client-Side Join Without Tenant Context [Low]

Section 24.14 describes `lenny-ctl policy audit-isolation` as performing a client-side join of `GET /v1/admin/delegation-policies` and `GET /v1/admin/pools`. Since delegation policies and pools are platform-global resources, this command fetches all policies and all pools across all tenants. The client-side join means the full platform topology is downloaded to the operator's machine. This is appropriate for `platform-admin` role, but the spec does not address whether `tenant-admin` should have a tenant-scoped variant of this audit command that only shows policies and pools relevant to their tenant.

**Location:** Section 24.14 (line 10401)

---

### TNT-065. Session Investigation Commands Lack Tenant Ownership Verification [Low]

Section 24.11 defines `lenny-ctl admin sessions get <id>` and `lenny-ctl admin sessions force-terminate <id>` with minimum role `platform-admin`. The spec does not address whether `tenant-admin` users can investigate or force-terminate sessions belonging to their own tenant. In a multi-tenant deployment, tenant administrators would reasonably need to investigate stuck sessions within their tenancy. The current design requires escalation to `platform-admin` for any session investigation, which creates an operational bottleneck. If `tenant-admin` access is intended to be added later, the session investigation API should be designed with tenant-scoped authorization from the start.

**Location:** Section 24.11 (lines 10365-10366)

---

### TNT-066. Erasure Job Management Commands Not Tenant-Scoped [Low]

Section 24.12 defines erasure job management commands (`get`, `retry`, `clear-restriction`) that operate by `job-id` only, requiring `platform-admin` role. The spec does not define whether erasure job IDs are globally unique or tenant-scoped, nor whether a `tenant-admin` can view the status of erasure jobs affecting their tenant's users. Given that GDPR erasure requests originate from data subjects who belong to specific tenants, tenant administrators have a legitimate operational need to track erasure job progress for their users. The current design requires `platform-admin` involvement for all erasure operations, which may not scale in large multi-tenant deployments with frequent erasure requests.

**Location:** Section 24.12 (lines 10370-10374)

---

### TNT-067. Bootstrap Seed `noEnvironmentPolicy: allow-all` Recommendation Weakens Tenant Isolation [Medium]

The Phase 5 note (line 10050) recommends setting `noEnvironmentPolicy: allow-all` on the default tenant for pre-Phase 15 builds. This effectively disables environment-based access control for user-role principals until Phase 15 (Environments) is delivered. Between Phase 5 and Phase 15 -- which spans Phases 6 through 14.5, including security hardening -- the platform operates with `allow-all` access for all users within a tenant. In a multi-tenant deployment, this means all users within a tenant can access all runtimes that the tenant has access to, regardless of any intended team-level or project-level access boundaries. The spec should note this as an explicit security trade-off and recommend that multi-tenant deployments reaching Phase 5 before Phase 15 should use option (b) -- seeding environments -- rather than `allow-all`.

**Location:** Phase 5 note (line 10050), Section 17.6 bootstrap

---

### TNT-068. Tier Promotion Guide Does Not Address Multi-Tenant Considerations [Medium]

Section 17.8.3 provides a detailed Tier 2 to Tier 3 promotion checklist (go/no-go criteria, Steps 1-4) that is entirely infrastructure-focused: gateway GC, etcd write latency, KEDA deployment, warm pools. However, there is no tenant-aware promotion criterion. At Tier 3 scale (10,000 concurrent sessions), per-tenant resource contention becomes a significant concern. The promotion guide should include: (a) per-tenant session distribution analysis (is load dominated by one tenant?), (b) per-tenant quota utilization relative to global capacity, (c) Redis tenant-key cardinality at Tier 3 scale, (d) `lenny_tenant_guard` trigger performance at 10,000 concurrent sessions (trigger fires on every write, creating cumulative overhead). Without tenant-aware promotion criteria, an operator could promote to Tier 3 without understanding the tenant-level load distribution.

**Location:** Section 17.8.3 (lines 9787-9845)

---

---

## 9. Storage Architecture (STR)

### STR-064. Section 12.2 Storage Roles Table Missing MemoryStore, EvalResultStore, and SemanticCache [Medium]

The Section 12.2 Storage Roles table (lines 5519-5531) lists eight storage roles: `SessionStore`, `LeaseStore`, `TokenStore`, `QuotaStore`, `ArtifactStore`, `EventStore`, `CredentialPoolStore`, and `EvictionStateStore`. However, three additional roles that appear throughout the spec are absent from this authoritative table:

- **`MemoryStore`** -- defined as a full interface in Section 9.4 (line 4181: "MemoryStore is a role-based storage interface alongside SessionStore, ArtifactStore, etc.") and listed in the erasure scope table (line 5871) as "Postgres (or pluggable)."
- **`EvalResultStore`** -- listed in the erasure scope table (line 5867) backed by Postgres, with FK dependencies on `SessionStore`.
- **`SemanticCache`** -- listed in the erasure scope table (line 5874) as "Redis (or pluggable)," has a defined key prefix pattern in the Redis key table (line 5685), and has a full interface definition in Section 4.9.

Section 12.2 declares itself the authoritative table of storage roles and states "All Redis-backed roles... must use the `t:{tenant_id}:` key prefix convention." An implementer using Section 12.2 as the storage layer's interface inventory would miss three stores entirely. Add all three to the Section 12.2 table with their backends and purposes.

---

### STR-065. Circuit Breaker Redis Keys Use Non-Tenant-Prefixed Pattern Not Listed in Section 12.4 Key Table [Medium]

Section 11.6 (line 5396) defines circuit breaker Redis keys as `cb:{name}` -- a pattern that does not use the `t:{tenant_id}:` prefix and is not listed in the Section 12.4 canonical key prefix table (lines 5676-5690). The spec states (line 5532) "no raw Redis command may be issued without the tenant prefix (or pod prefix for slot counters)" and the key table explicitly notes `lenny:pod:{pod_id}:*` as "the sole exceptions to the tenant-prefix rule."

Circuit breaker keys are a second class of intentional exception (they are platform-wide, not tenant-scoped), but they are neither listed in the key table nor mentioned in the exception text. An implementer enforcing the "no raw Redis command without prefix" rule at the Redis wrapper layer would block circuit breaker operations. Add `cb:{name}` to the Section 12.4 table and update the exception statement to include circuit breaker keys alongside pod-scoped keys.

---

### STR-066. Section 12.3 References "Section 12.7" for Data Classification Tier but 12.7 is "Extensibility" [Low]

Line 5585 states: "Audit log write mode is determined by **data classification tier** (Section 12.7), not by environment or SIEM configuration alone." Section 12.7 (lines 5827-5833) is titled "Extensibility" and contains bullet points about backend strategy. The data classification tier definitions are in Section 12.9 ("Data Classification," line 5991). This is an incorrect section cross-reference. The same error recurs at line 5587 where T3/T4 classification is referenced via "(Section 12.7)." Both references should point to Section 12.9.

---

### STR-067. Billing Redis Stream MAXLEN Derivation Inconsistency at Tier 1/2 [Low]

The Tier 3 MAXLEN derivation (line 9741, footnote 4) uses the formula `event_rate x RTO x 2x_safety = 600 x 60 x 2 = 72,000`. The footnote also claims "Tier 1/2 billing rates (~6/s and ~60/s respectively) are comfortably within the 50,000 default (fill time: ~2.3 hours and ~14 minutes)." However, applying the same formula to Tier 2 yields `60 x 60 x 2 = 7,200` -- a value far below 50,000 and well within budget. The concern is the reverse direction: the 50,000 default provides ~14 minutes of buffer at Tier 2, which is well above the 30s RTO target. This is not technically wrong (larger is safer), but the MAXLEN defaults at Tier 1/2 are ~7x-700x larger than what the derivation formula would produce, while Tier 3's MAXLEN is derived precisely. The spec does not explain why Tier 1/2 uses a flat 50,000 rather than a formula-derived value. This is a documentation gap, not a correctness bug, but operators may question the seemingly arbitrary default. Adding a brief note explaining the Tier 1/2 rationale (e.g., "rounded up to provide multi-minute outage tolerance") would improve clarity.

---

### STR-068. DeleteByTenant Dependency Order Inconsistent with DeleteByUser Order [Medium]

The `DeleteByUser` erasure sequence (lines 5879-5898) specifies a carefully ordered 18-step dependency sequence with FK-aware ordering: e.g., `EvalResultStore` (step 12) and `session_tree_archive` (step 11) before `SessionStore` (step 15), and `EventStore` audit (step 13) before `SessionStore`.

The `DeleteByTenant` sequence in Phase 4 of tenant deletion (line 5935) lists a different ordering: `...ArtifactStore → MemoryStore → EvictionStateStore → session_dlq_archive → session_tree_archive → EventStore (audit) → EventStore (billing) → EvalResultStore → SessionStore → TokenStore → CredentialPoolStore...`. Here, `EventStore (audit)` and `EventStore (billing)` are deleted *before* `EvalResultStore`, yet in the `DeleteByUser` flow, `EvalResultStore` (step 12) is deleted *before* `EventStore (audit)` (step 13). If there are FK dependencies between these stores, one of the two orderings is wrong. Since the spec explicitly documents that `EvalResultStore` must precede `SessionStore` due to FK `EvalResult.session_id -> sessions.id`, and audit events similarly reference sessions, both should precede `SessionStore` -- but the relative ordering of `EventStore` vs `EvalResultStore` is inconsistent between the two flows. The `DeleteByTenant` flow should match the same dependency ordering as `DeleteByUser`.

---

### STR-069. No Defined Redis Key Pattern or TTL for Billing Write-Ahead In-Memory Buffer Persistence [Low]

The billing event failover path (Section 11.2.1, line 5250) describes a Tier 2 in-memory write-ahead buffer as a fallback when Redis is unavailable. The spec states "The in-memory buffer is not persisted to disk" (line 5251). However, the `quotaFailOpenCumulativeMaxSeconds` feature (Section 12.4, line 5719) does persist state to a local file (`/run/lenny/failopen-cumulative.json`) to survive pod restarts. There is an asymmetry: quota fail-open state gets local persistence for CrashLoopBackOff resilience, but billing in-memory buffer state does not. If a gateway enters CrashLoopBackOff during a dual-store outage (Redis + Postgres both down), it will lose billing events on every restart cycle. The spec accepts this as a "triple-failure scenario" (line 5665) but does not explicitly acknowledge the CrashLoopBackOff amplification vector where repeated restarts compound the loss. This is an edge case and arguably acceptable, but it would be clearer if the spec explicitly noted the CrashLoopBackOff billing loss amplification and explained why local persistence was not applied to the billing buffer (likely: billing events are larger and more complex to serialize safely than a single counter).

---

### STR-070. Erasure Salt Deletion-Then-Regeneration Creates Re-Identification Window for Concurrent Erasure Jobs [Medium]

Section 12.8 (lines 5906-5919) specifies that the `erasure_salt` is per-tenant and is deleted immediately after pseudonymization. It then states: "After deletion, the next erasure request generates a fresh 256-bit salt for that tenant." However, the spec does not define what happens when two `DeleteByUser` erasure jobs run concurrently for different users in the same tenant. Consider:

1. Job A for user-1 reads the salt, pseudonymizes user-1's billing events, deletes the salt.
2. Job B for user-2 (running concurrently or overlapping with Job A) also needs to pseudonymize billing events. If Job B reads the salt before Job A deletes it, both use the same salt -- correct. But if Job B starts after Job A deletes the salt, Job B must generate a new salt, pseudonymize user-2 with the new salt, then delete the new salt.

The spec does not address this concurrency scenario. The advisory lock for salt rotation (`erasure_salt_migration:{tenant_id}`, line 5919) is mentioned only for rotation, not for concurrent erasure jobs. Two concurrent erasure jobs could race on salt read-delete-regenerate. An explicit serialization mechanism (e.g., the erasure job acquires an advisory lock on the salt before pseudonymization) should be specified.

---

### STR-071. Storage Quota `io.LimitedReader` Hard Cap Uses Stale Counter Value [Medium]

Section 11.2 (line 5160) specifies that the hard stream cap wraps the inbound body in `io.LimitedReader` set to `remaining_quota_bytes = storageQuotaBytes - storage_bytes_used`. This value is read from Redis at pre-check time. However, the atomic Lua reservation in step 1 already incremented the counter by `incoming_bytes`. After the Lua script returns success, the `storage_bytes_used` value has already been incremented. If the `io.LimitedReader` is set using the pre-reservation `storage_bytes_used` value (before the Lua increment), the limit is `storageQuotaBytes - (old_storage_bytes_used)` -- which is larger than the actual remaining quota by `incoming_bytes`. This means the hard cap permits streaming `incoming_bytes` more than the true remaining quota.

If instead the `io.LimitedReader` uses the post-reservation value, the limit would be `storageQuotaBytes - (old_storage_bytes_used + incoming_bytes)`, which could be zero or negative for uploads that exactly fill the quota, causing immediate EOF even for legitimately accepted uploads. The spec does not clarify which value is used or how the `io.LimitedReader` and the atomic Lua reservation interact. The `io.LimitedReader` should be set to `incoming_bytes` (the declared upload size) rather than `remaining_quota_bytes`, since the Lua script has already validated that the reservation fits within the quota.

---

### STR-072. Checkpoint Retention "Latest 2 Per Slot" Inconsistent with GC Job Query Granularity [Low]

Section 12.5 (line 5798) specifies that in concurrent-workspace mode, checkpoint retention operates on `(session_id, slot_id)` pairs and "the GC job and retention policy operate on `(session_id, slot_id)` pairs, not on sessions alone." However, the GC job description (lines 5801-5803) describes artifact cleanup using queries against the `artifact_store` table and `session_eviction_state` rows, with no mention of `slot_id` as a query dimension. The `artifact_store` table schema is not fully defined in the spec, and it is unclear whether `slot_id` is a column in this table. If the `artifact_store` table does not carry `slot_id`, the GC job cannot enforce per-slot retention -- it would retain the latest 2 checkpoints per session across all slots, not per slot.

The spec should either: (a) explicitly include `slot_id` in the `artifact_store` table schema (at minimum documenting it as a column), or (b) describe how the GC job distinguishes per-slot checkpoints.

---

### STR-073. Eviction Context Object Cleanup References Missing `session_eviction_state` Table Schema [Low]

Section 12.5 (line 5800) describes GC cleanup for eviction context objects: "the GC job queries `session_eviction_state` rows for sessions that have reached a terminal state and whose `last_message_context` column stores a MinIO object key (identified by the `/{tenant_id}/eviction/` key prefix)." This implies the `session_eviction_state` table's `last_message_context` column can contain either inline text (for small contexts <= 2KB) or a MinIO object key (for larger contexts), distinguished by the presence of the tenant-prefixed path pattern. However, the `EvictionStateStore` role in Section 12.2 (line 5530) describes it as "Minimal session state records written during eviction checkpoint fallback" with no schema details. The column naming convention (`last_message_context`) and the dual inline-vs-object-key storage pattern are not formalized anywhere in the schema sections. An implementer would need to infer the storage pattern from the GC description. A brief schema definition for `session_eviction_state` with the dual-mode column semantics should be added to Section 12.2 or 12.5.

---

### STR-074. Data Residency Per-Region Quota Enforcement Gap Not Bounded [Medium]

Section 12.8 (line 5975) states: "If a tenant has sessions in multiple regions, each region enforces quotas independently against its own counters. The global per-tenant quota... could be exceeded in aggregate even if each region is individually within limits. Global cross-region quota aggregation is the deployer's responsibility." This creates an unbounded quota overshoot scenario: a tenant with sessions in N regions could consume up to N times their intended quota. The spec recommends "deployers who require global enforcement should implement a centralized quota coordination service or set per-region sub-quotas that sum to the desired global limit" but does not provide a mechanism for the deployer to set per-region sub-quotas via the API.

The tenant configuration model (line 6022) shows `dataClassification.workspaceTier` but no per-region quota subdivision. The `storageQuotaBytes` quota (Section 11.2) appears to be a single tenant-level value with no regional dimension. Without a `storage.regions.<region>.quotaBytes` Helm value or equivalent API field, deployers cannot implement the recommended sub-quota approach without modifying Lenny's source code. Either add a per-region quota subdivision mechanism to the tenant configuration model, or document this as a known v1 limitation with the workaround explicitly stated.

---

### STR-075. Billing Sequence Numbers Per-Region Non-Monotonic but Gap Detection Assumes Monotonicity [Medium]

Section 12.8 (line 5974) states: "The `billing_seq_{tenant_id}` Postgres sequence is per-region-per-tenant. Consumers that aggregate billing events across regions must not assume global monotonicity." However, the gap detection and replay mechanism (Section 11.2.1, line 5255) states: "Each event carries a monotonic `sequence_number` scoped to the tenant, enabling consumers to detect gaps and request replays." The replay endpoint `GET /v1/metering/events?since_sequence={N}` uses a single `sequence_number` cursor.

In a multi-region deployment, a consumer connecting to region A's gateway gets one sequence stream, and region B's gateway produces an independent stream. The replay API does not accept a `region` parameter or return a `region` field on billing events. A consumer that merges events from multiple regional gateways cannot use `since_sequence` for gap detection because the two independent sequences will have overlapping numbers. The billing event schema (lines 5197-5234) does not include a `region` field.

Either: (a) add a `region` field to the billing event schema and the replay API, or (b) explicitly state that the replay endpoint is per-region and that cross-region consumers must maintain per-region cursors.

---

### STR-076. Legal Hold Reconciler Does Not Detect Holds Set During Active GC Cycle [Low]

Section 12.8 (line 5842) describes the legal hold reconciler running every 15 minutes to detect pre-hold checkpoint gaps. However, the GC job also runs every 15 minutes (line 5802) and is leader-elected to run inside the same gateway process. If a legal hold is set on a session while a GC cycle is actively running and has already queried the retention candidates (but not yet deleted them), the GC job may delete checkpoints for that session between the query and the hold being set. The reconciler would detect this on its next run, but the checkpoint would already be destroyed.

The spec notes that the reconciler "does not attempt to recover deleted checkpoints -- it provides detection and audit trail only" (line 5843), so this is by design. However, the spec does not describe whether the GC job checks `legal_hold` status per-artifact at deletion time or only at query time. If the GC job checks `legal_hold` only in its initial query (`WHERE legal_hold = false`), a hold set between the query and the `DELETE` operation creates a race window. The GC job should re-check `legal_hold` status in the `DELETE ... WHERE legal_hold = false AND deleted_at IS NULL` condition to close this race.

---

### STR-077. Separate Billing/Audit Postgres Instance Missing RLS and Tenant Guard Requirements [Medium]

Section 12.3 (line 5611) states that at Tier 3, operators may deploy a dedicated Postgres instance for billing and audit writes via `LENNY_PG_BILLING_AUDIT_DSN`. However, the extensive RLS defense discussion (lines 5546-5566) -- including the `lenny_tenant_guard` trigger, the `__unset__` sentinel, and the startup verification that checks for the trigger's existence -- applies only to the primary Postgres instance.

The spec does not state whether the separate billing/audit instance also requires:
1. The `lenny_tenant_guard` trigger on billing and audit tables
2. The same `connect_query` sentinel or cloud-managed pooler defense
3. The startup trigger-existence check
4. The same PgBouncer pooling requirements

Billing and audit tables are tenant-scoped (they carry `tenant_id`) and contain T3-classified data. If the separate instance lacks the RLS defense, a bug in the billing write path could silently write events to the wrong tenant's billing sequence. The RLS requirements should be explicitly stated as applying to all Postgres instances, not just the primary.

---

### STR-078. `session_dlq_archive` and `session_tree_archive` Not Declared as Storage Roles [Low]

The erasure scope table (lines 5869-5870) lists `session_dlq_archive` and `session_tree_archive` as separate Postgres tables that must be included in erasure. These tables have FK relationships with `SessionStore` and contain user content (DLQ messages, TaskResult payloads). However, neither is declared as a storage role in Section 12.2 nor as a sub-component of an existing role's interface.

The `DeleteByUser` sequence (lines 5890-5891) treats them as independent deletion targets with specific ordering constraints (`session_tree_archive` before `SessionStore` due to FK). If these tables are owned by `SessionStore`, they should be listed as part of its scope in Section 12.2. If they are independent stores, they should be listed as separate roles. Currently they exist in a gray zone: required for erasure but not declared in the storage role inventory, making it possible for an implementer to miss them when building the storage layer.

---

### STR-079. Redis Key Table Missing DLQ Score Semantics and Expiry Behavior [Low]

Section 12.4's Redis key table (line 5681) lists `t:{tenant_id}:session:{session_id}:dlq` as a "Sorted set scored by expiry; see Section 7.2." However, the table does not define the TTL or expiry semantics for DLQ entries. Section 7.2 (referenced but not fully quoted here) defines DLQ max size (500 messages) and presumably defines the score semantics. The Redis key table should at minimum note the max size and whether the GC for DLQ entries is score-based (expiry time) or size-based (ZREMRANGEBYRANK), since the key table is used as the authoritative Redis key reference and DLQ entries that are not cleaned up will grow unboundedly in Redis memory.

---

### STR-080. Tenant Deletion Phase 4 Drops Billing Sequence but Erasure Receipt May Reference It [Low]

Tenant deletion Phase 4 (line 5935) includes `DROP SEQUENCE IF EXISTS billing_seq_{tenant_id}`. The billing event schema uses `sequence_number` assigned by this sequence. After the sequence is dropped, the erasure receipt (Phase 6, line 5938) is written to the audit trail, and the `gdpr.*` audit events are retained for 7 years (line 5902). If any compliance investigation later needs to verify billing sequence continuity for the deleted tenant, the sequence no longer exists, making it impossible to confirm that no events were silently dropped between the last event and the sequence drop.

This is a minor gap: the receipt presumably records the final state, and the billing events themselves are pseudonymized or deleted before the sequence is dropped. But the spec could note that the final `sequence_number` value is captured in the erasure receipt before the `DROP SEQUENCE` to provide a terminal reference point.

---

### STR-081. Multipart Upload Quota Reservation Semantics Undefined [Medium]

Section 11.2 (line 5160) describes quota enforcement for uploads using `Content-Length` header and the atomic Lua reservation. However, MinIO supports multipart uploads for large objects (checkpoints can be up to 500MB per Section 12.5 line 5788). The spec describes partial checkpoint manifests and `AbortMultipartUpload` (line 5801) for GC, confirming multipart uploads are used.

For multipart uploads:
1. `Content-Length` is per-part, not per-object -- the total size is known only after all parts complete.
2. The pre-upload atomic reservation requires knowing `incoming_bytes` upfront.
3. The `io.LimitedReader` wraps the "inbound request body" but multipart uploads have multiple request bodies.

The spec does not describe how the quota reservation interacts with multipart uploads. Options include: (a) the adapter computes total size from the workspace probe before initiating multipart, reserving the full amount upfront; (b) each part reserves independently. Option (a) is implied by "for checkpoint writes the adapter supplies the tar size from the workspace-size probe before upload begins" (line 5160), but this only covers checkpoints, not user-initiated uploads that may also use multipart. The quota semantics for multipart user uploads should be explicitly specified.

---

### STR-082. Postgres Advisory Lock Fallback for LeaseStore Not Covered by Tenant Key Isolation [Low]

Section 12.2 (line 5524) states that `LeaseStore` uses "Redis (fallback: Postgres advisory locks)" for distributed session coordination. The Redis key isolation rule (line 5532) mandates `t:{tenant_id}:` prefix for all Redis-backed roles. However, Postgres advisory locks are integers (or integer pairs), not strings with prefixes. The spec does not describe how tenant isolation is enforced in the advisory lock fallback path.

If advisory locks use a hash of `session_id` as the lock key, two sessions from different tenants could theoretically hash to the same lock value, causing cross-tenant lock contention. The spec should describe the advisory lock key derivation and how tenant isolation is maintained in the fallback path (e.g., by incorporating `tenant_id` into the hash input).

---


Correction: reviewing severity assignments --

- STR-064 (missing storage roles): Medium -- incomplete inventory, not a runtime bug
- STR-065 (circuit breaker keys): Medium -- enforcement wrapper would block operations
- STR-066 (wrong section ref): Low -- documentation error only
- STR-067 (MAXLEN derivation): Low -- documentation gap, defaults are safe
- STR-068 (delete order inconsistency): Medium -- potential FK violation in one path
- STR-069 (billing buffer persistence): Low -- edge case, acceptable per spec
- STR-070 (erasure salt concurrency): Medium -- race condition in concurrent erasure
- STR-071 (LimitedReader stale value): Medium -- could permit over-quota streaming
- STR-072 (checkpoint per-slot GC): Low -- implicit but undocumented
- STR-073 (eviction state schema): Low -- missing schema detail
- STR-074 (per-region quota gap): Medium -- unbounded overshoot with no API support
- STR-075 (billing sequence multi-region): Medium -- replay API unusable cross-region
- STR-076 (legal hold GC race): Low -- by design but race not closed
- STR-077 (separate PG RLS): Medium -- RLS defense gap on separate instance
- STR-078 (archive tables not roles): Low -- gray zone in inventory
- STR-079 (DLQ key semantics): Low -- incomplete key table
- STR-080 (billing sequence drop): Low -- minor audit gap
- STR-081 (multipart quota): Medium -- undefined semantics for common path
- STR-082 (advisory lock isolation): Low -- no tenant isolation specification

---

## 10. Recursive Delegation (DEL)

### DEL-065. Child Lease Construction Algorithm Unspecified for Non-LeaseSlice Fields [Medium]

**Location:** Section 8.2 (line ~3154) and Section 8.3 (line ~3279)

The `LeaseSlice` struct passed to `delegate_task` contains only 5 fields: `maxTokenBudget`, `maxChildrenTotal`, `maxTreeSize`, `maxParallelChildren`, and `perChildMaxAge`. The full delegation lease (Section 8.3) has 20+ fields including `cascadeOnFailure`, `credentialPropagation`, `messagingRateLimit`, `maxTreeMemoryBytes`, `snapshotPolicyAtLease`, `approvalMode`, `treeVisibility`, `fileExportLimits`, and `experimentContext`.

The spec states "Child leases are always strictly narrower than parent leases (depth decremented, budgets reduced)" (line 3311) but provides no normative algorithm for how the non-LeaseSlice fields are populated on the child's lease. For example: Does the child inherit the parent's `cascadeOnFailure`? Is `messagingRateLimit` inherited verbatim or derived? Can the parent specify `cascadeOnFailure` per-child? The `credentialPropagation` worked example (line ~3402) implies it is set per-delegation, but neither `LeaseSlice` nor `TaskSpec` includes it.

**Challenged:** One could argue "inherited from parent unless DelegationPolicy overrides" is an obvious default. However, fields like `cascadeOnFailure` and `credentialPropagation` are legitimately per-delegation choices (the worked example at line 3404 shows different `credentialPropagation` values per hop). Without a mechanism for the parent to specify them in `delegate_task`, the parent cannot differentiate behavior across children. This is a genuine spec gap.

---

### DEL-066. `maxTreeMemoryBytes` Not Listed as Extendable via Lease Extension [Medium]

**Location:** Section 8.6 (line 3581)

The "Extendable fields" list at line 3581 is: `maxChildrenTotal`, `maxParallelChildren`, `maxTokenBudget`, `maxTreeSize`, `perChildMaxAge`, `fileExportLimits`. The "Not extendable" list is: `maxDepth`, `minIsolationProfile`, `delegationPolicyRef`, `perChildRetryBudget`.

`maxTreeMemoryBytes` appears in neither list. It governs the aggregate in-memory footprint of a tree on the gateway (default 2 MB, line 3210). If a tree reaches its memory cap, new delegations are rejected with `TREE_MEMORY_EXCEEDED`. Since `maxTreeSize` (pod count) is extendable, there is a scenario where a lease extension grants more pods (`maxTreeSize`) but the tree cannot actually use them because `maxTreeMemoryBytes` was not extended in proportion. The spec should explicitly classify `maxTreeMemoryBytes` as either extendable (alongside `maxTreeSize`) or not extendable (with rationale).

**Challenged:** Perhaps `maxTreeMemoryBytes` is intentionally not extendable because it protects gateway memory. But the same argument applies to `maxTreeSize` (which is extendable), and `maxTreeSize` directly drives pod resource consumption. The omission appears to be an oversight rather than a deliberate design choice, especially since both counters are managed by the same Lua scripts.

---

### DEL-067. LRU Cache for Offloaded Subtree Results Not Counted in `maxTreeMemoryBytes` [Low]

**Location:** Section 8.2 (line 3212)

Completed subtrees are offloaded to Postgres and the in-memory node is replaced by a ~200 B stub. On demand, results are fetched from Postgres "with a per-replica LRU cache, default 128 entries." The `maxTreeMemoryBytes` counter is decremented when a node is offloaded.

However, the LRU cache entries rehydrate `TaskResult` payloads into memory (including `OutputPart[]` arrays that can be much larger than 12 KB). A 128-entry LRU cache holding results with non-trivial output parts could consume significant memory that is not tracked by `maxTreeMemoryBytes` (since the counter was decremented at offload time). The spec does not address whether the LRU cache is bounded by size (bytes) rather than entry count, or whether cache hits should re-increment `maxTreeMemoryBytes`.

**Challenged:** The LRU cache is per-gateway-replica, not per-tree, so it arguably falls outside the scope of `maxTreeMemoryBytes` (a per-tree counter). The cache is also bounded at 128 entries. However, with 128 entries each potentially holding large `OutputPart` arrays (up to 64 KB inline per part, or multiple parts), the cache could hold ~8 MB per replica. For a gateway serving many trees, this is a per-replica overhead rather than a per-tree concern, and the 128-entry cap limits it. This is a genuine but low-severity documentation gap.

---

### DEL-068. No Tree-Wide Cumulative File Export Budget [Medium]

**Location:** Section 8.7 (line 3725) and Section 8.3 (line 3295)

`fileExportLimits` (`maxFiles: 100`, `maxTotalSize: 100MB`) is enforced per-delegation (line 3725: "Total exported size is checked against `fileExportLimits` in the delegation lease"). This means a parent with `maxChildrenTotal: 50` could perform 50 delegations, each exporting 100 MB, for a total of 5 GB of file transit through the gateway and MinIO. Each delegation is individually within limits, but the aggregate is unbounded relative to the lease.

The spec has no tree-wide cumulative file export budget analogous to `maxTokenBudget` (which caps total tokens across the tree). A tree with depth 5 and fan-out 50 could generate tens of GB of file transit without violating any per-delegation limit.

**Challenged:** The per-delegation limit combined with `maxTreeSize` and `maxChildrenTotal` does provide an implicit ceiling: `maxChildrenTotal * fileExportLimits.maxTotalSize` per node. But this is an emergent bound, not an explicit one, and it compounds across tree levels. If this is an accepted design choice (file export limits are per-hop, not cumulative), the spec should state so explicitly. Given that `maxTokenBudget` has explicit tree-wide semantics, the absence of an analogous tree-wide file export budget is a genuine gap for deployer capacity planning.

---

### DEL-069. `session_tree_archive` Table Cleanup / GC Not Specified [Medium]

**Location:** Section 8.2 (line 3212) and Section 12 (data stores)

The `session_tree_archive` table stores completed child task results keyed by `(root_session_id, node_session_id)`. Line 5891 mentions it is deleted "before `SessionStore` to satisfy the FK dependency" during GDPR deletion. Line 5870 lists it in the data stores table.

However, there is no specification for routine GC of `session_tree_archive` rows. When a delegation tree completes normally (all nodes terminal), the archived results remain in Postgres indefinitely -- the only cleanup path documented is GDPR deletion (user-initiated). For high-throughput Tier 3 deployments with 10,000+ concurrent sessions using delegation, the `session_tree_archive` table could grow to millions of rows within days, with no scheduled cleanup.

**Challenged:** The standard session retention policy (configurable via `retentionPolicy`) likely covers this implicitly -- when a root session's retention period expires and the session is cleaned up, the FK cascade would delete `session_tree_archive` rows. However, the spec never explicitly states that `session_tree_archive` cleanup is tied to root session retention. Given the FK relationship documented at line 5891, cascade delete from `SessionStore` is the likely mechanism, but the spec should be explicit about the retention lifecycle for this table.

---

### DEL-070. `treeUsage` Only Available After All Descendants Settled -- No Incremental Aggregation [Low]

**Location:** Section 8.8 (line 3842)

"`treeUsage` is populated by the gateway from the task tree and is only available after all descendants have settled. It contains the sum of this task's usage plus all descendant tasks. For in-progress tasks or tasks with unsettled descendants, `treeUsage` will be `null`."

This means a parent orchestrating a large tree (e.g., 50 children) cannot observe cumulative token consumption across its subtree while children are still running. For cost-aware orchestration patterns (e.g., "stop delegating when subtree has consumed 80% of budget"), the parent has no way to know current aggregate consumption. The per-child `usage` is available, but the parent would need to manually sum all children's usage, and this does not include grandchild consumption for children that are themselves orchestrators.

**Challenged:** The parent can observe its own `maxTokenBudget` counter depletion (since child slices are reserved from it), which indirectly tracks aggregate consumption. However, this only tells the parent how much budget it has left, not how much its subtree has actually consumed (since returned budget from completed children inflates the remaining counter). The lack of incremental `treeUsage` is a real limitation for cost-aware orchestration. That said, the reservation model provides a workable proxy, and real-time aggregation across an active tree would be expensive. This is a low-severity documentation gap rather than a design error.

---

### DEL-071. Static Per-Node Memory Estimate (12 KB) May Drift from Actual Usage [Low]

**Location:** Section 8.2 (lines 3199-3210) and Section 11.2 (line 5173)

The spec defines a static per-node memory footprint estimate of ~12 KB (table at lines 3199-3208) used by `budget_reserve.lua` and `budget_return.lua` for `maxTreeMemoryBytes` tracking. The reconstruction procedure (line 5173) also uses this estimate: "compute `liveMemoryBytes` as the number of currently-alive (non-archived) descendant nodes multiplied by the per-node footprint estimate (`nodeMemoryFootprintBytes`, default: 12288 / 12 KB; configurable via gateway Helm value `delegationNodeMemoryFootprintBytes`)."

The actual per-node footprint depends on runtime factors: event buffer fill level (0-64 events), elicitation payload size, and metadata size. The static 12 KB is a worst-case upper bound for the defined components, but if the gateway's actual implementation uses more memory per node (e.g., additional routing state, larger MCP server shims), the Redis counter will undercount and the 2 MB cap could be breached before `TREE_MEMORY_EXCEEDED` fires.

**Challenged:** The estimate is configurable via Helm (`delegationNodeMemoryFootprintBytes`), so deployers can tune it. The 12 KB figure includes defined components with clear sizing. The real risk is drift during development (implementation adds per-node state that exceeds the estimate). This is minor and mitigated by the Helm configurability, but the spec could note that the estimate should be validated against actual memory profiling during development and updated per release.

---

### DEL-072. Cross-Subtree Deadlock via `send_message` Bounded Only by `maxRequestInputWaitSeconds` [Low]

**Location:** Section 8.8 (line 3906)

The spec explicitly documents this as a known limitation: "circular `lenny/send_message` dependencies across sibling subtrees... are not detected because the detector's scope is a single subtree and `send_message` creates cross-subtree dependencies invisible to it." It states this is bounded by `maxRequestInputWaitSeconds`.

While documented, the default `maxRequestInputWaitSeconds` is 600s (10 minutes). Two siblings in a circular `send_message` wait will each consume a pod for 10 minutes before timing out, wasting 20 pod-minutes. With `messagingScope: siblings` and `maxParallelChildren: 10`, up to 10 such pairs (20 children) could be deadlocked simultaneously, consuming 200 pod-minutes of waste per deadlock window.

**Challenged:** This is explicitly documented as a known limitation with a clear mitigation (`maxRequestInputWaitSeconds`). The 600s default is the same as `maxElicitationWaitSeconds` and is a reasonable balance. Deployers aware of sibling-messaging workloads can reduce it. The spec correctly identifies the scope boundary of the deadlock detector and provides adequate guidance. This is a documentation acknowledgement, not a missing spec.

---

### DEL-073. `delegate_task` TaskSpec Subset Cannot Specify `env`, `runtimeOptions`, or `retryPolicy` [Medium]

**Location:** Section 8.2 (lines 3141-3152)

The `TaskSpec` for `delegate_task` contains only `input` (OutputPart[]) and `workspaceFiles.export`. Line 3152 states: "The gateway augments the delegation `TaskSpec` with routing metadata (resolved runtime, tenant context, credential assignment, and delegation parameters from the parent's lease) before processing... The full session creation schema -- including `env`, `runtimeOptions`, `retryPolicy`, `timeouts`, and `workspacePlan` -- is defined in Section 14."

This means the delegating parent cannot pass environment variables (`env`), runtime-specific options (`runtimeOptions`), or a custom `retryPolicy` to the child. The child inherits these from the resolved runtime's defaults. For legitimate use cases like "delegate this code analysis task to runtime X with DEBUG=true" or "delegate with retryPolicy.maxRetries=3 for a flaky runtime," the parent has no mechanism to influence these parameters.

**Challenged:** Keeping `TaskSpec` minimal is a security design choice -- it prevents parents from injecting arbitrary configuration into children. Environment variables could be used for injection attacks; `runtimeOptions` could override safety settings. The gateway controls augmentation. However, there is a usability gap: the parent cannot parameterize child behavior beyond input content and file exports. A safe subset of child-configurable fields (or a `hints` map) would address this without compromising security. This is a genuine spec gap for orchestration expressiveness.

---

### DEL-074. `cascadeOnFailure` Name Is Misleading -- It Fires on All Terminal States Including `completed` [Low]

**Location:** Section 8.10 (line 3986)

The spec explicitly acknowledges: "The name `cascadeOnFailure` is historical; it governs the fate of children on all parent terminal transitions, not only failure." It then gives the concrete example of `await_children(mode="any")` completing normally while siblings are still running.

While the spec documents this, the field name actively misleads implementors. An agent runtime author reading the lease schema would reasonably assume `cascadeOnFailure: cancel_all` means "cancel children if I fail" and be surprised when normal completion also triggers the cascade. The `cascadeOnTermination` or `childLifecyclePolicy` name would be semantically accurate.

**Challenged:** The spec documents the actual behavior clearly and thoroughly. The misleading name is a cosmetic issue that documentation addresses. Renaming a lease field has schema compatibility implications. This is a low-severity naming concern, not a functional gap.

---

### DEL-075. `snapshotPolicyAtLease` Does Not Snapshot Cross-Environment Bilateral Declarations [Medium]

**Location:** Section 10.6 (line 4730)

The spec explicitly states: "The `snapshotPolicyAtLease` flag (Section 8.3) applies only to `DelegationPolicy` pool-label matching (step 4) and does not affect the bilateral declaration checks in steps 2 and 3."

This means a deployer who enables `snapshotPolicyAtLease: true` for policy stability in long-running trees still has a mutation window: an administrator can modify or remove cross-environment bilateral declarations mid-tree, blocking grandchild delegations that were permitted when the tree started. The deployer has no mechanism to snapshot bilateral declarations alongside the policy.

**Challenged:** This is explicitly documented and the rationale is reasonable -- bilateral declarations are environmental access control (who can talk to whom) and should reflect current administrator intent, unlike pool-label matching which is operational routing. However, the asymmetry creates a surprising partial-snapshot: policy rules are frozen but environment access is live. A deployer reading "`snapshotPolicyAtLease: true` provides stable, predictable delegation behavior for long-running trees" (line 3264) may not realize that cross-environment access can still change under them. The spec documents this but the usability implication is notable.

---

### DEL-076. `budget_reserve.lua` Uses Static `nodeMemoryEstimate` Not Actual Memory at Offload [Low]

**Location:** Section 8.2 (line 3337) and Section 8.2 (line 3212)

`budget_reserve.lua` increments tree memory by the static `nodeMemoryEstimate` (~12 KB) and `budget_return.lua` decrements by the same static estimate. However, completed subtree offloading (line 3212) replaces the full in-memory node with a ~200 B stub and decrements `maxTreeMemoryBytes`. This means the actual memory freed at offload (~11.8 KB) matches the decrement (~12 KB) closely.

But the decrement on `budget_return.lua` (child reaches terminal state, line 3388) also decrements tree memory by the node's footprint. If a node is first offloaded (memory counter decremented by ~12 KB) and then `budget_return.lua` fires (memory counter decremented by ~12 KB again), the counter would be double-decremented.

**Challenged:** Re-reading the spec carefully: offloading happens "When a child session reaches a terminal state" (line 3212), and `budget_return.lua` also fires "when a child session reaches a terminal state" (line 3378). These appear to be part of the same terminal-state handling flow, not separate events. The offloading description says "The `maxTreeMemoryBytes` counter is decremented when a node is offloaded" (line 3212), while `budget_return.lua` "decrements tree memory via `DECRBY`" (line 3388). If both happen on the same terminal event, there is a double-decrement. If offloading is what performs the decrement (and `budget_return.lua`'s memory decrement IS the offload decrement), the spec describes the same operation twice in different sections. The spec should clarify whether offload-time decrement and `budget_return.lua` decrement are the same operation or distinct operations that could double-count.

---

---

## 11. Session Lifecycle (SLC)

### SLC-071. `maxSessionAge` timer pausing semantics conflict between Section 6.2 and Section 11.3 [Medium]

**Section 6.2** states that `maxSessionAge` runs during `running`, `input_required`, and `starting`, and is paused during `suspended`, `resume_pending`, `resuming`, and `awaiting_client_action`. However, **Section 11.3** lists `maxSessionAge` with default 7200s and references Section 6.2, but the timeout table does not mention the pausing/running distinction. More importantly, the `session_expiring_soon` event in Section 11.3 says it fires "5 minutes before `maxSessionAge` expires" -- but if the timer pauses and resumes across multiple state transitions, the gateway must track cumulative running time, not wall-clock time. The spec never explicitly states whether `maxSessionAge` measures cumulative running time or wall-clock time with pauses subtracted. This ambiguity could lead to implementation divergence.

### SLC-072. `created` state timeout vs. session lifecycle step atomicity gap [Medium]

**Section 7.1** states that steps 2-8 of the 24-step session creation flow are atomic (pod claim, credential assignment, etc.). **Section 15.1** states that the `created` state has a TTL of `maxCreatedStateTimeoutSeconds` (default 300s), and on expiry the gateway releases the pod claim and revokes the credential lease. However, the spec does not define what happens if the `maxCreatedStateTimeoutSeconds` timer fires during the atomic steps 2-8 themselves. If the atomicity window takes longer than expected (e.g., credential pool contention, slow pod claim), the timer could fire mid-atomicity, creating a partial-cleanup race condition. The spec should clarify whether the timer starts after atomicity completes (i.e., after step 8) or at the moment the `CreateSession` call begins.

### SLC-073. `suspended` state lacks explicit entry from `input_required` sub-state [Medium]

**Section 6.2** defines `input_required` as a sub-state of `running`. **Section 15.1** state transition table says `POST /v1/sessions/{id}/interrupt` is valid from `running` and transitions to `suspended`. However, if the session is in the `input_required` sub-state (blocked in `lenny/request_input`), the spec does not define what happens to the pending `request_input` tool call when an interrupt occurs. Does the tool call resolve with an error? Does it remain pending across the suspend/resume cycle? Section 15.4.1's `MessageEnvelope` states that `delivery: "immediate"` does NOT override path 3 buffering during `input_required`, suggesting the sub-state has special handling, but interrupt behavior during `input_required` is unspecified.

### SLC-074. `resume_pending` → `running` transition omits `resuming` in API response but `resuming` has operational significance [Low]

**Section 15.1** state transition table says `POST /v1/sessions/{id}/resume` transitions `awaiting_client_action` → `resume_pending` → `running`, noting that `resuming` is "internal-only transient state." However, Section 7.3 describes `resuming` as requiring checkpoint restoration and pod allocation, which can take significant wall time. During this window, the API reports the session as `resume_pending` even though the system is actively restoring state. If the `resuming` phase fails (e.g., checkpoint restoration failure), the session would need to transition back, but the API surface has no way to indicate that the "resume" is failing since it still shows `resume_pending`. The `maxResumeWindowSeconds` covers the outer boundary, but within that window, clients have no observability into progress.

### SLC-075. Credential lease lifecycle mismatch between session mode and concurrent-workspace mode [Medium]

**Section 4.9** states credential leases are "per-session for session mode, per-task for task mode, per-slot for concurrent mode." However, Section 7.1 step 6 describes credential assignment as part of the atomic session creation (steps 2-8). For concurrent-workspace mode, if leases are per-slot, the spec does not clarify when slot-level leases are created -- are they created at session creation time (before any slot assignment) or at slot assignment time? If at slot assignment time, the atomic session creation (steps 2-8) would only pre-validate credential availability without actually leasing, which conflicts with the step 6 description "Token Service selects credential, assigns lease, gateway pushes to pod."

### SLC-076. `awaiting_client_action` with `maxAwaitingClientActionSeconds` expiry vs. `maxResumeWindowSeconds` overlap [Medium]

**Section 7.3** defines both `maxResumeWindowSeconds` (default 900s, wall-clock cap on `resume_pending`) and `maxAwaitingClientActionSeconds` (default 900s). Section 6.2 says `maxResumeWindowSeconds` is a "wall-clock cap" while `maxAwaitingClientActionSeconds` applies to `awaiting_client_action`. If a session exhausts automatic retries and enters `awaiting_client_action`, and the client then calls `resume`, the session goes to `resume_pending`. At this point, both timers are relevant: the `maxAwaitingClientActionSeconds` was running during `awaiting_client_action`, and now `maxResumeWindowSeconds` starts for `resume_pending`. The spec does not clarify whether `maxAwaitingClientActionSeconds` is cancelled when the client resumes (transitioning out of `awaiting_client_action`), or whether it continues to run as a total wall-clock budget across `awaiting_client_action` + subsequent states.

### SLC-077. Session derive from non-terminal sessions lacks checkpoint consistency guarantee [Medium]

**Section 15.1** allows `POST /v1/sessions/{id}/derive` from non-terminal sessions when `allowStale: true` is set. The response includes `workspaceSnapshotSource` and `workspaceSnapshotTimestamp`. However, the spec does not specify what happens if a checkpoint is in progress when the derive is requested. Section 4.4 describes checkpoint atomicity with partial manifests, but the derive operation's interaction with an in-progress checkpoint is unspecified. The derived session could receive a workspace from a checkpoint taken mid-operation, leading to an inconsistent workspace state. The spec should clarify whether derive blocks until the current checkpoint completes or uses the last fully-committed checkpoint.

### SLC-078. Inter-session message DLQ migration on `resume_pending` lacks size bound [Medium]

**Section 7.2** states that when a session enters `resume_pending`, inbox messages migrate to DLQ (Redis-backed, `session_dlq_archive` in Postgres). However, the spec does not define a size limit on the DLQ. If a session is in `resume_pending` or `awaiting_client_action` for an extended period and receives many messages from siblings or parents, the DLQ could grow unboundedly. The `messagingRateLimit.maxInboundPerMinute` provides rate limiting, but over the full `maxResumeWindowSeconds` + `maxAwaitingClientActionSeconds` window (potentially 1800s), a session could accumulate significant DLQ entries without a defined cap.

### SLC-079. `session_expiring_soon` event timing with paused `maxSessionAge` timer [Low]

**Section 11.3** states the gateway sends `session_expiring_soon` 5 minutes before `maxSessionAge` expires. Section 6.2 defines that `maxSessionAge` pauses during `suspended`, `resume_pending`, `resuming`, and `awaiting_client_action`. If a session has accumulated 115 minutes of running time (out of 120 max), enters `suspended`, and remains suspended for hours, the 5-minute warning should have been sent at 115 minutes. But if the session was suspended before reaching 115 minutes, the warning hasn't fired yet. When the session resumes and the timer restarts, the spec doesn't clarify whether the gateway immediately fires the warning (since less than 5 minutes remain) or only checks at periodic intervals. This could result in a session timing out with no warning if it resumes with less than 5 minutes remaining.

### SLC-080. Task-mode `task_complete_acknowledged` timeout (30s hard-coded) has no fallback path [Medium]

**Section 4.7** mentions `task_complete_acknowledged` with a 30s hard-coded timeout, and Section 11.3 confirms it. However, the spec does not define what happens when this timeout expires without acknowledgment. Does the gateway assume the runtime is hung and force-terminate the pod? Does it retry the signal? Does it mark the task as failed? For task-mode pods that are expected to be reused across tasks, a hung runtime between tasks would prevent pod reuse, but the recovery path is unspecified.

### SLC-081. Coordinator handoff during `input_required` sub-state may lose pending request context [Medium]

**Section 10.1** describes coordinator handoff between gateway replicas, including `CoordinatorFence` RPC and state transfer. However, the handoff procedure does not explicitly address the `input_required` sub-state. When a session is blocked in `lenny/request_input`, the gateway holds the pending request context (the `requestId`, the requesting child session, the timeout). If the coordinating gateway replica fails and another takes over, the spec does not describe how the new coordinator reconstructs this pending request state. The `maxRequestInputWaitSeconds` timeout (Section 11.3) would eventually fire, but the new coordinator needs to know about the pending request to fire it correctly.

### SLC-082. `maxElicitationWaitSeconds` and `maxRequestInputWaitSeconds` interaction during nested delegation [Low]

**Section 11.3** defines `maxElicitationWaitSeconds` (600s, per pool) and `maxRequestInputWaitSeconds` (600s, per pool). Section 9.2 describes the elicitation chain as hop-by-hop. In a deep delegation tree, a child calling `lenny/request_input` could trigger the parent to call `lenny/request_elicitation` upward. Each hop has its own timeout. If the chain is 3 hops deep, the leaf's `maxRequestInputWaitSeconds` could expire before the root's elicitation response propagates back through 2 intermediate hops. The spec does not address how these per-hop timeouts compound in deep delegation trees or whether the leaf's timeout should account for propagation delay.

### SLC-083. `recovery_generation` vs `coordination_generation` usage inconsistency in pod state machine [Low]

**Section 10.1** defines `coordination_generation` for coordinator handoff fencing and Section 7.3 defines `recovery_generation` for session resume. Section 6.2's pod state machine uses these generations to prevent stale operations. However, the spec never clarifies which generation is checked during which pod state transitions. For example, when a pod transitions from `running` to `suspended` (interrupt), does the gateway check `coordination_generation`? When a checkpoint is triggered during `running`, which generation gates the checkpoint write? The two generation counters serve different purposes but their interaction with the pod state machine transitions is underspecified.

### SLC-084. Session completion with in-flight billing events in Redis stream creates ordering gap [Medium]

**Section 7.1** step 23-24 describes session completion including final reconciliation of token usage to Postgres. **Section 11.2.1** describes billing events being staged to Redis stream during Postgres unavailability. If a session completes while billing events are staged in the Redis stream (Postgres was transiently unavailable), the `session.completed` billing event would be written to Postgres (which is now available for the completion), but earlier `token_usage.checkpoint` events are still in the Redis stream waiting to be flushed. This creates an ordering inversion: the `session.completed` event appears in Postgres with a lower `sequence_number` than the checkpoint events that preceded it chronologically. The spec's gap-detection mechanism (consumers detect gaps in `sequence_number`) would detect this, but the root cause (ordering inversion from dual-path writes) is not addressed.

### SLC-085. `DELETE /v1/sessions/{id}` transitions to `cancelled` but `POST /v1/sessions/{id}/terminate` transitions to `completed` [Low]

**Section 15.1** state transition table shows `DELETE /v1/sessions/{id}` results in `cancelled` while `POST /v1/sessions/{id}/terminate` results in `completed`. Both are described as terminating the session, but they produce different terminal states. The distinction is meaningful for billing (a `cancelled` session vs. a `completed` session may have different cost implications) and for derive eligibility (both are terminal). However, the webhook delivery model in Section 14 defines separate events: `session.completed`, `session.cancelled`, and `session.terminated`. The `terminate` endpoint produces `completed` but also has a separate `session.terminated` webhook type, creating a confusing mapping between API action, terminal state, and webhook event type.

### SLC-086. Erasure job interaction with active session lifecycle not fully specified [Medium]

**Section 12.8** states that when an erasure job is initiated, `processing_restricted: true` is set, and "in-flight sessions that are already running at erasure initiation time are allowed to complete naturally (they will be erased by the job)." However, the `DeleteByUser` sequence (steps 1-18) includes deleting `LeaseStore` entries (step 1) and `QuotaStore` entries (step 6) before `SessionStore` (step 15). If sessions are still running when steps 1 and 6 execute, deleting active session coordination leases and quota counters could disrupt those running sessions. The spec should clarify whether the erasure job waits for all active sessions to reach a terminal state before executing the deletion sequence, or whether it proceeds immediately.

---

## 12. Observability (OBS)

### OBS-058. `lenny_pool_draining_sessions_total` metric defined inline in Section 15.1 but absent from Section 16.1 metrics table [Medium]

Section 15.1 (line 7101) defines `lenny_pool_draining_sessions_total` as a gauge labeled by `pool` that tracks in-flight sessions during pool drain operations. This metric is referenced as the monitoring signal for the pool drain admin endpoint. However, it does not appear in the Section 16.1 metrics table. Operators monitoring pool drain progress have no canonical metrics-table entry to reference.

---

### OBS-059. `lenny_mcp_deprecated_version_active_sessions` metric defined inline in Section 15.2 but absent from Section 16.1 metrics table [Medium]

Section 15.2 (line 7513) defines `lenny_mcp_deprecated_version_active_sessions` as a gauge emitted by the preflight Job to warn operators of sessions still active on a deprecated MCP protocol version. This metric is not listed in the Section 16.1 metrics table. Without it, operators cannot build dashboards around MCP version deprecation readiness.

---

### OBS-060. `lenny_circuit_breaker_open` metric and `CircuitBreakerActive` alert defined in Section 11.6 but absent from Section 16 [Medium]

Section 11.6 (line 5411) defines `lenny_circuit_breaker_open` gauge (labeled by `circuit_name`) and a corresponding `CircuitBreakerActive` warning alert that fires when any breaker has been open for more than 5 minutes. Neither the metric nor the alert appear in Section 16.1's metrics table or Section 16.5's alerting rules table. These are operator-declared circuit breakers (distinct from the per-subsystem circuit breakers already covered), and their omission means the central observability specification does not capture a key operational control surface.

---

### OBS-061. `lenny_pool_bootstrap_mode` metric defined in Section 17.8.2 but absent from Section 16.1 metrics table [Low]

Section 17.8.2 (lines 9653-9655) defines `lenny_pool_bootstrap_mode` as a gauge per pool (1 = active, 0 = converged). While the `PoolBootstrapMode` alert that references this metric is correctly listed in Section 16.5, the underlying metric itself is not listed in the Section 16.1 metrics table, creating an inconsistency where an alert references a metric that has no metrics-table entry.

---

### OBS-062. `push_delivery_failed` metric referenced in Section 21.1 without canonical metric name [Low]

Section 21.1 (line 10152) states that A2A `OutboundChannel.Send` "emits a `push_delivery_failed` metric" after webhook delivery retry exhaustion, but does not specify a fully-qualified Prometheus metric name (e.g., `lenny_a2a_push_delivery_failed_total`), labels, or metric type. Since this is a Post-V1 feature, the impact is low, but the metric naming convention is inconsistent with the `lenny_` prefix used everywhere else. If this is intended as a v1 concern (since the `OutboundChannel` infrastructure ships in v1), it should have a canonical name.

---

### OBS-063. `audit_grant_drift_total` metric uses inconsistent naming -- no `lenny_` prefix [Medium]

Section 11.7 (line 5435) defines `audit_grant_drift_total` as a Prometheus counter that tracks audit grant drift detections. This metric violates the `lenny_` prefix convention used by all other platform metrics (e.g., `lenny_billing_correction_pending_total`, `lenny_erasure_job_failed_total`). Section 16.5's `AuditGrantDrift` alert (line 8780) references this metric. The naming inconsistency could cause confusion when configuring monitoring dashboards and alerting rules.

---

### OBS-064. `lenny_workspace_seal_duration_seconds` metric referenced by `WorkspaceSealStuck` alert but absent from Section 16.1 metrics table [Medium]

Section 16.5 (line 8841) defines the `WorkspaceSealStuck` alert that fires based on `lenny_workspace_seal_duration_seconds{outcome="timeout"}`. However, this metric does not appear in the Section 16.1 metrics table. The alert correctly specifies the metric name and outcome label, but the metrics table has no corresponding entry for implementers to instrument.

---

### OBS-065. No alerting rule for `lenny_orphan_tasks_active` exceeding threshold despite explicit instruction [Low]

Section 8.10 (line 4019) states: "Deployers should alert when `lenny_orphan_tasks_active` exceeds a deployment-specific threshold (suggested: 50)." The metric is correctly listed in Section 16.1. However, Section 16.5's alerting rules table has no alert for global `lenny_orphan_tasks_active > 50`. There is `OrphanTasksPerTenantHigh` (per-tenant alert at 80% of cap), but no aggregate alert. The per-tenant alert catches per-tenant abuse; the aggregate threshold catches systemic orphan accumulation across all tenants. These are different failure modes.

---

### OBS-066. No tracing span coverage specified for pool drain lifecycle [Medium]

Section 16.2 (key latency breakpoints) and the distributed tracing section (16.2) define spans for session lifecycle, checkpoint, and delegation. However, pool drain is a multi-phase operational lifecycle (drain initiated -> sessions draining -> drain complete) referenced in Section 15.1 with its own status endpoint, metric (`lenny_pool_draining_sessions_total`), and error code (`POOL_DRAINING`). No tracing spans are defined for the drain lifecycle, making it difficult to trace drain duration and diagnose slow drains.

---

### OBS-067. No metric or alert for `lenny/request_input` timeout rate [Medium]

Section 11.3 defines `maxRequestInputWaitSeconds` as the timeout for `lenny/request_input` blocking calls, and the error code `REQUEST_INPUT_TIMEOUT` is defined in Section 15.1. However, neither Section 16.1's metrics table nor Section 16.5's alerting rules include a metric tracking the rate of `request_input` timeouts. A high rate of these timeouts indicates user responsiveness issues or misconfigured timeout values. The `lenny_elicitation_timeout_total` metric covers elicitation timeouts (Section 9.2) but `request_input` is a different code path (Section 7.2, path 3).

---

### OBS-068. No alert for `lenny_checkpoint_storage_failure_total` sustained failures [Medium]

Section 16.1 includes `lenny_checkpoint_storage_failure_total` (counter labeled by pool, tier, trigger). However, Section 16.5's alerting rules have no alert that fires when this counter sustains a non-zero rate. The `CheckpointStaleSessions` alert (line 8806) fires when sessions have stale checkpoints, but this is an indirect symptom. A direct alert on sustained checkpoint storage failures would provide earlier warning of MinIO or object storage issues affecting data durability.

---

### OBS-069. `lenny_pgaudit_grant_events_total` metric not listed in Section 16.1 metrics table [Low]

Section 11.7 (line 5446) defines `lenny_pgaudit_grant_events_total` (counter, labeled by `statement_type`) and Section 16.5 references it in the `PgAuditSinkDeliveryFailed` alert. However, this metric is not listed in the Section 16.1 metrics table.

---

### OBS-070. No observability specified for session replay operations [Low]

Section 15.1 (lines 7028-7057) defines `POST /v1/sessions/{id}/replay` with detailed semantics including mode selection, runtime compatibility validation, and credential handling. No metrics, tracing spans, or alerts are defined for replay operations anywhere in the spec. Replay is a key mechanism for regression testing and A/B evaluation; operators need to track replay volume, success rates, and latency to assess runtime upgrade confidence.

---

### OBS-071. No metric for credential deny-list propagation latency [Medium]

Section 4.9 describes emergency credential revocation with deny-list propagation via Redis pub/sub. Section 17.7 includes a credential pool exhaustion runbook. Section 16.1 includes credential rotation and assignment metrics, but no metric captures the latency between a revocation event and the last gateway replica acknowledging the deny-list update. In a multi-replica gateway deployment, propagation delay is a critical security metric -- a credential could be used during the gap.

---

### OBS-072. No metric for billing Redis stream flush failures distinct from stream depth [Medium]

Section 16.1 includes `lenny_billing_redis_stream_depth` (gauge) which shows accumulating events. However, no counter tracks the number of individual Postgres flush attempts that fail. A sustained non-zero stream depth could result from either slow but successful flushes or repeated failures. A counter for flush failures (e.g., `lenny_billing_flush_failure_total`) would disambiguate these cases. Section 11.2.1's two-tier durability model depends on Postgres flush reliability, and the billing write-ahead buffer alert (`BillingWriteAheadBufferHigh`) covers only the in-memory buffer, not the Redis->Postgres flush path.

---

### OBS-073. `lenny_restore_test_success` and `lenny_restore_test_duration_seconds` referenced in Section 17.1 but absent from Section 16.1 metrics table [Low]

Section 17.1 (disaster recovery) references `lenny_restore_test_success` and `lenny_restore_test_duration_seconds` as metrics for the automated daily backup restore test. These are not listed in the Section 16.1 metrics table. As DR metrics emitted by an external Job (not the gateway itself), their omission may be intentional, but the spec does not clarify this boundary.

---

### OBS-074. SLO error-budget burn-rate alerts missing for checkpoint duration at workspace sizes above 100MB [Low]

Section 16.5 defines `CheckpointDurationBurnRate` for the SLO "Checkpoint duration P95 < 2s (100MB workspace)". However, Section 4.4 defines tiered checkpoint size caps (1 GB for periodic, 500 MB for eviction). No SLO or burn-rate alert covers checkpoint duration for workspaces between 100MB and 1GB. Operators have no alerting signal for checkpoint latency degradation at larger workspace sizes, which are common in production.

---

---

## 13. Compliance & Governance (CMP)

### CMP-070. NIS2/DORA Audit Retention Preset Has No Corresponding `complianceProfile` Value [Medium]

The `audit.retentionPreset` table (Section 16.4) defines a `nis2-dora` preset (1825 days / 5 years) and pairs it with `complianceProfile: none` or `soc2`. However, NIS2 and DORA impose distinct runtime controls beyond retention length -- incident reporting timelines (NIS2: 24-hour early warning, 72-hour incident notification), supply chain security documentation, ICT risk management evidence preservation, and cross-border notification to multiple national CERTs. These are qualitatively different from SOC2/HIPAA/FedRAMP controls. The spec provides no `complianceProfile` value (e.g., `nis2` or `dora`) that could gate NIS2/DORA-specific runtime enforcement. Deployers targeting NIS2/DORA compliance must manually configure retention via the preset while relying on `complianceProfile: none` or `soc2` for runtime controls, which may not satisfy auditor expectations that the platform actively enforces NIS2/DORA-specific controls. The document should either (a) add `nis2` and/or `dora` as `complianceProfile` values with appropriate runtime gates, or (b) explicitly document that NIS2/DORA runtime enforcement is the deployer's responsibility and the preset is retention-only.

### CMP-071. Multi-Region Quota Enforcement Gap Creates Unbounded Financial Exposure [Medium]

Section 12.8 acknowledges that per-tenant quotas (storage, token budget) are enforced per-region in multi-region deployments: "each region enforces quotas independently against its own counters" and "the global per-tenant quota could be exceeded in aggregate even if each region is individually within limits." The spec delegates global cross-region quota aggregation to the deployer as "the deployer's responsibility." This is a genuine compliance gap for billing and cost governance: a tenant operating across N regions can consume up to N times their intended global quota with no platform-level enforcement or even alerting. The document provides no guidance on how deployers should implement cross-region quota coordination, no recommended architecture for a centralized quota service, and no metric or alert that would fire when aggregate cross-region quota is exceeded. At minimum, the spec should define a `lenny_quota_per_region_usage` metric that deployers can aggregate externally, and document the recommended pattern for sub-quota allocation (which it mentions in passing but does not formalize).

### CMP-072. Cross-Region Billing Aggregation Lacks Specified Reconciliation Semantics [Medium]

Section 12.8 states billing sequence monotonicity is per-region-per-tenant and that "cross-region billing aggregation is the deployer's responsibility." However, the billing event schema (Section 11.2.1) defines correction semantics with a `corrects_event_id` reference. In a multi-region deployment, a correction event in region A could reference an event ID from region B (if the deployer's aggregation layer merges events cross-region before corrections are applied). The spec does not specify whether `corrects_event_id` is region-scoped or globally unique, whether correction events must be applied in the same region as the original event, or how the dual-control approval workflow (which lives in a single gateway deployment per region) interacts with cross-region corrections. This creates ambiguity that could lead to billing integrity violations.

### CMP-073. Erasure Job Does Not Cover the `session_inbox` / DLQ Live Redis Structures [Medium]

The erasure scope table (Section 12.8) lists 18 stores including `session_dlq_archive` (Postgres) and `LeaseStore` (Redis). However, Section 7.2 defines a `session_inbox` and DLQ as live Redis structures (each with max 500 messages). If an erasure job runs while sessions are still completing (the spec allows in-flight sessions to "complete naturally"), messages in the live `session_inbox` and DLQ Redis lists are not covered by the `DeleteByUser` sequence -- the sequence only covers `session_dlq_archive` (the Postgres archive) and `LeaseStore`. The live Redis inbox and DLQ messages for in-flight sessions constitute personal data (they contain inter-session user content per Section 7.2). The erasure job should either (a) wait for all in-flight sessions to terminate before proceeding, or (b) explicitly include `session_inbox` and DLQ Redis keys in the deletion scope.

### CMP-074. Tenant Deletion Phase 4 Does Not Cover `EvictionStateStore` Deletion in Documented Dependency Order [Low]

The tenant deletion Phase 4 dependency order (Section 12.8) lists the sequence: `LeaseStore` -> `SemanticCache` -> Redis caches -> experiment sticky assignment cache -> billing Redis stream -> `QuotaStore` -> `ArtifactStore` -> `MemoryStore` -> `EvictionStateStore` -> `session_dlq_archive` -> `session_tree_archive` -> `EventStore` (audit) -> `EventStore` (billing) -> `EvalResultStore` -> `SessionStore` -> `TokenStore` -> `CredentialPoolStore` -> additional items. The `EvictionStateStore` is listed in the erasure scope table (Section 12.8) as containing "Minimal eviction state records containing `last_message_context` for the user's sessions." However, the `DeleteByUser` sequence (steps 1-18) lists `EvictionStateStore` at step 9 and `ArtifactStore` at step 10 -- but the ArtifactStore contains eviction context objects at `/{tenant_id}/eviction/{session_id}/context`. If eviction context objects in MinIO reference or depend on the `EvictionStateStore` Postgres records, the dependency order is correct. But if the reverse is true (MinIO objects should be deleted first), there is a dependency violation. The spec should clarify the FK/referential relationship between `EvictionStateStore` and eviction context objects in `ArtifactStore`.

### CMP-075. `billingErasurePolicy: exempt` Tenants Lack Retention Ceiling for Identifiable Billing Data [Medium]

Tenants with `billingErasurePolicy: exempt` retain billing events with the original `user_id` intact indefinitely (subject only to `billing.retentionDays`). The default billing retention is 395 days, but there is no maximum ceiling. Under GDPR Article 5(1)(e) (storage limitation), personal data must not be kept longer than necessary for the purpose. For exempt tenants, the spec documents GDPR Article 17(3)(b) as the legal basis but does not require deployers to configure a maximum retention period appropriate to their jurisdiction's requirements. A tenant could inadvertently retain identifiable billing data for years beyond what their legal basis supports. The spec should require `billingErasurePolicy: exempt` tenants to also configure a `billingRetentionMaxDays` value, or at minimum emit a compliance audit event when identifiable billing records exceed a configurable age threshold.

### CMP-076. GDPR Erasure Receipt Retention Floor Inconsistency Between Regulated and Non-Regulated Profiles [Low]

Section 17.8.1 states the GDPR erasure receipt retention default is 2555 days (7 years) with a floor of 2190 days for any regulated `complianceProfile`. Section 12.8 states: "This value may not be set below 2190 (6 years) when `complianceProfile` is any regulated value." For non-regulated deployments (`complianceProfile: none`), there is no floor specified -- the value could theoretically be set to 1 day. Since erasure receipts are the authoritative proof that GDPR erasure was performed, and GDPR enforcement windows extend to 4-6 years regardless of whether the deployer has a regulated compliance profile, the absence of any floor for non-regulated deployments is a gap. GDPR enforcement timelines apply to all controllers, not only those with formal compliance profiles. The spec should enforce a minimum floor (e.g., 2190 days) regardless of `complianceProfile`.

### CMP-077. No Mechanism to Verify Completeness of `DeleteByUser` Across All 18 Stores [Medium]

The erasure job executes `DeleteByUser` across 18 stores in dependency order and the spec describes crash recovery via phase persistence. However, there is no post-completion verification step that confirms all stores are actually clean. The verification step described in Section 12.8 only covers the billing pseudonymization salt deletion (step 14/18). After the full 18-step sequence completes, the erasure receipt is written without a cross-store verification scan. If a store deletion silently fails (e.g., a Redis `DEL` returns 0 because the key was already TTL-expired but a different key pattern was missed), the receipt would record success while personal data remains. The spec should define a post-completion verification pass that queries each store for any remaining records matching the erased `user_id` before writing the completion receipt.

### CMP-078. Audit Event Hash Chain Gap at SIEM Boundary Not Detectable by Platform [Medium]

Section 11.7 specifies hash-chain integrity for audit events in Postgres, with periodic verification and startup chain checks. However, the SIEM outbox forwarder reads from Postgres and delivers to an external SIEM endpoint. If the SIEM loses events (network partition, SIEM-side ingestion failure), the hash chain in Postgres remains intact while the SIEM copy has gaps. The spec defines `AuditSIEMDeliveryLag` alert (Section 16.5) for lag, but there is no mechanism for the platform to verify hash-chain integrity at the SIEM end. For regulated deployments where the SIEM is the authoritative immutable copy (the spec states: "The SIEM provides the independent, immutable audit copy that satisfies regulatory requirements"), gaps in the SIEM copy that are undetectable by the platform undermine the compliance value. The spec should define a SIEM-side hash verification protocol or at least require the SIEM forwarder to track the last successfully delivered hash chain entry and alert on gaps.

### CMP-079. Pre-Phase 13 Audit Gap Is Accepted but Not Bounded for Duration [Low]

Section 18 (Build Sequence) states: "Between Phase 7 and Phase 13, policy-decision events are emitted via structured logs and are not persisted to durable append-only audit tables. This is an accepted gap." The document justifies this by stating "all pre-Phase 13 activity occurs in development/testing environments with no regulated tenants." However, there is no enforcement mechanism that prevents a deployer from onboarding regulated tenants before Phase 13 is complete. The compliance profile enforcement gate (Section 11.7) prevents creating regulated tenants without SIEM -- but SIEM enforcement is a Phase 13 deliverable, so the gate itself does not exist before Phase 13. A deployer who deploys a pre-Phase 13 build to production with regulated tenants would have no durable audit trail. The spec should add an explicit build-phase marker or feature flag that blocks `complianceProfile` values other than `none` until the Phase 13 audit infrastructure is deployed.

### CMP-080. Data Residency Enforcement Does Not Cover the Billing Redis Stream Buffer [Medium]

Section 12.8 specifies three levels of data residency enforcement: pod pool routing, storage routing, and KMS key residency. The billing event pipeline (Section 11.2.1, 12.3) uses a Redis stream (`t:{tenant_id}:billing:stream`) as a write-ahead buffer during Postgres unavailability. If the Redis instance serving the billing stream is in a different region than the tenant's `dataResidencyRegion`, billing events containing personal data (`user_id`, usage data) transit through a non-compliant region. The `StorageRouter` data residency check covers Postgres and MinIO writes but there is no mention of Redis stream region affinity enforcement. For multi-region deployments with data residency constraints, the spec should require that the Redis instance backing the billing stream for a given tenant is co-located in the tenant's declared data residency region.

### CMP-081. `force-delete` Tenant With Legal Holds Lacks Dual-Control Requirement [Medium]

Section 12.8 describes the legal hold interaction during tenant deletion: if legal holds exist, the controller pauses at Phase 3, and an operator can bypass via `POST /v1/admin/tenants/{id}/force-delete`. The force-delete records operator identity and justification in the audit trail. However, the spec requires dual-control approval (two authorized individuals) for billing corrections (Section 11.2.1) but not for force-deleting a tenant with active legal holds -- an action that destroys potentially discoverable evidence. Destroying legally-held data is a more consequential action than correcting a billing entry. The spec should require dual-control approval for `force-delete` of tenants with active legal holds, consistent with the billing correction precedent.

### CMP-082. DSAR Article 15 (Right of Access) Has No Completeness Guarantee Across Stores [Low]

Section 12.8 states that "all user-scoped session records, task trees, audit events, and stored artifacts are queryable via the admin API using `user_id` as a filter key." However, the admin API endpoints listed in Section 15.1 do not include a unified "export all data for user" endpoint. The deployer must call multiple endpoints and assemble the response. The list of stores in the erasure scope table (18 stores) shows significant breadth, but the DSAR primitives section does not enumerate which admin API endpoints cover which stores. A deployer building a DSAR export tool has no authoritative mapping from erasure-scope stores to admin API query endpoints. Stores like `SemanticCache`, `EvictionStateStore`, `experiment sticky assignment cache`, and `billing write-ahead buffer` may not have corresponding admin API read endpoints. The spec should provide an explicit DSAR-to-API mapping table alongside the erasure scope table.

### CMP-083. Erasure Salt Immediate Deletion Breaks Multi-User Erasure Batching [Medium]

Section 12.8 specifies that the `erasure_salt` must be deleted immediately after pseudonymization completes for a given user-level erasure job. The salt is per-tenant, not per-user. If two erasure requests for different users in the same tenant arrive concurrently or in quick succession: (1) Job A pseudonymizes user-A's billing events using salt S1, then deletes S1. (2) Job B starts, finds no salt, generates new salt S2, pseudonymizes user-B's events with S2, then deletes S2. The spec addresses sequential erasure by noting "the next erasure request generates a fresh 256-bit salt." However, if Job B starts its pseudonymization transaction before Job A's deletion commits, both jobs use S1 -- but Job A deletes S1 while Job B still needs it for its verification step. The spec mentions the `erasure_salt_migration:{tenant_id}` advisory lock for rotation-vs-erasure conflicts but does not define equivalent serialization for concurrent erasure jobs within the same tenant. The spec should require a tenant-level advisory lock for erasure jobs (not just rotation) to prevent concurrent pseudonymization races.

### CMP-084. KMS Key Deletion in Tenant Deletion Phase 4a Is T4-Only; T3 Envelope Encryption Keys Are Not Addressed [Low]

Section 12.8 Phase 4a specifies KMS key deletion for T4 tenants only. However, Section 4.3 describes envelope encryption for OAuth tokens (all tiers) and Section 12.5 describes SSE-KMS for object storage (T4 uses per-tenant keys, T3 uses shared keys). For T3 tenants, the per-tenant `erasure_salt` KMS key material is destroyed (documented in Phase 4), but the shared KMS key used for T3 envelope encryption (OAuth tokens, credential pool secrets) is not rotated or addressed after tenant deletion. If a T3 tenant's encrypted data was backed up before deletion, the shared KMS key could still decrypt it after tenant deletion. The spec should document the shared-key residual exposure for T3 tenants and recommend KMS key rotation after T3 tenant deletion in regulated environments.

### CMP-085. `processing_restricted` Database Trigger Exempts `clear-processing-restriction` Endpoint via Session Variable, Creating Bypass Risk [Low]

Section 12.8 describes a `BEFORE UPDATE` trigger on `processing_restricted` that prevents clearing the flag while an active erasure job exists. The trigger exempts the `lenny_erasure` role and the `clear-processing-restriction` admin endpoint (which "sets a session-local variable analogous to `lenny.erasure_mode`"). This means any code path that sets the session-local variable can bypass the trigger. If a bug in any other code path accidentally sets this variable, the GDPR Article 18 processing restriction could be silently cleared without the intended audit trail. The spec should name the session-local variable explicitly and require the trigger to also verify the calling SQL context (e.g., a function-level check) rather than relying solely on a session variable that any SQL session could set.

### CMP-086. No `complianceProfile` Enforcement for Audit Retention Preset Pairing [Low]

Section 16.4 defines audit retention presets and pairs them with `complianceProfile` values (e.g., `hipaa` preset paired with `hipaa` profile, `fedramp-high` paired with `fedramp`). However, the spec states: "The `audit.retentionPreset` is independent of `complianceProfile`." This means a deployer could set `complianceProfile: hipaa` with `audit.retentionPreset: soc2` (365 days) -- violating HIPAA's 6-year record retention requirement (45 C.F.R. 164.530(j)). The spec does not enforce minimum retention when a regulated compliance profile is active. The document should either enforce that the retention preset matches or exceeds the compliance profile's minimum, or validate at startup that `audit.retentionDays` meets the floor for the active `complianceProfile`.

### CMP-087. Billing Event Correction Dual-Control Approval Has No Timeout or Expiry [Low]

Section 11.2.1 requires dual-control approval for billing corrections. The spec does not specify a timeout for pending correction approvals. A correction request that is never approved (or denied) would persist indefinitely in a pending state. In audit and compliance contexts, stale pending corrections create ambiguity about billing integrity -- auditors cannot distinguish an intentionally pending correction from an abandoned one. The spec should define a maximum pending duration (e.g., 72 hours) after which unapproved corrections are automatically denied with an audit event.

### CMP-088. Eviction Context Objects in MinIO Are Not Covered by Data Residency Enforcement [Low]

Section 12.8 lists `ArtifactStore` in the erasure scope and specifically calls out `eviction context objects (/{tenant_id}/eviction/{session_id}/context)`. These objects contain `last_message_context` which may include session content (potentially personal data). The `StorageRouter` data residency enforcement covers standard artifact writes, but eviction context objects are written by the eviction manager (Section 7.3) which may not route through the same `StorageRouter` path. The spec should confirm that eviction context object writes are subject to the same `dataResidencyRegion` enforcement as standard artifact writes.

---

## 14. API Design (API)

### API-083. `POST /v1/sessions/{id}/terminate` vs `DELETE /v1/sessions/{id}` semantic overlap is under-specified [Low]

The session lifecycle table (Section 15.1) defines two endpoints that terminate sessions from non-terminal states: `POST /v1/sessions/{id}/terminate` (results in `completed`) and `DELETE /v1/sessions/{id}` (results in `cancelled`). The description for DELETE says "Force-terminates and cleans up. Equivalent to terminate + cleanup in one call." However, the spec never clarifies what "cleanup" means beyond termination -- both endpoints operate on the same precondition states (any non-terminal state), and what extra cleanup DELETE performs versus terminate is undefined. Since terminate already transitions to `completed` (a terminal state), the distinction between the two endpoints and their differing terminal states (`completed` vs `cancelled`) should be explicitly documented so clients know which to use when. The current description suggests DELETE is a superset of terminate, but the resulting state difference (`completed` vs `cancelled`) implies different semantics that are not explained.

### API-084. `Retry-After` header documented as "present only on 429 responses" but also used on 503 responses [Medium]

The rate-limit headers table in Section 15.1 states that `Retry-After` is "present only on `429` responses." However, the spec uses `Retry-After` on `503` responses in at least two other places: (1) warm pool exhaustion returns `503 RUNTIME_UNAVAILABLE` with a `Retry-After` header (Section 6.3, line ~2245); (2) `POOL_DRAINING` returns `503` with `Retry-After` (Section 15.1 error catalog, line ~7257). The rate-limit headers table should be corrected to say `Retry-After` is present on `429` and `503` responses, or the table should clarify it documents rate-limit-specific headers only and that `Retry-After` may appear on other status codes per individual endpoint documentation.

### API-085. `POST /v1/sessions/start` listed as async job endpoint but conflicts with `POST /v1/sessions/{id}/start` [Medium]

In Section 15.1, the "Async job support" table lists `POST /v1/sessions/start` (no `{id}` path parameter) as accepting an optional `callbackUrl` for completion notification. However, the session lifecycle table defines `POST /v1/sessions/{id}/start` (with `{id}`) as the endpoint that starts the agent runtime. These appear to be two different endpoints, but the spec does not define the schema or behavior of `POST /v1/sessions/start` (without `{id}`). It is unclear whether this is a typo for `POST /v1/sessions/{id}/start`, a shorthand for a combined create-and-start endpoint, or a distinct endpoint. If it is distinct, its request/response schema is missing. If it is a typo, the async job table should use the correct path `POST /v1/sessions/{id}/start`.

### API-086. Webhook event types not fully enumerated [Medium]

The webhook delivery model (Section 14/15.1) defines event-specific payloads for `session.completed` and `session.failed`, and references `session.awaiting_action` in the awaiting-client-action documentation. However, the full list of webhook event types is never provided as a complete enumeration. The SIEM/audit events table (Section 11.7) lists `session.completed` as covering all terminal states (completed, failed, cancelled, expired), but those are audit events, not webhook events. The webhook model references suggest multiple event types exist (`session.completed`, `session.failed`, `session.awaiting_action`) but does not specify whether `session.cancelled` and `session.expired` are separate webhook event types or subsumed under `session.completed`. Clients implementing webhook handlers need an exhaustive list of event types with their respective payload schemas.

### API-087. `POST /v1/sessions/{id}/interrupt` transition to `suspended` contradicts Section 7.2 `input_required` state [Medium]

The session lifecycle endpoint table (Section 15.1) states that `POST /v1/sessions/{id}/interrupt` transitions from `running` to `suspended`. However, Section 8.8 (protocol state mapping) and the external session states table both define `input_required` as a state where the session awaits client input. The interrupt endpoint description says "Only valid while the agent is actively executing" and specifies `suspended` as the result, but the spec elsewhere describes `input_required` as a distinct state entered via `lenny/request_input`. The relationship between `suspended` (client-initiated interrupt) and `input_required` (agent-initiated request for input) is clear in isolation, but the external session states table (line ~6954) does not include `input_required` as a listed state. If `input_required` is an externally visible state (as the protocol mapping tables and `awaiting_client_action` references suggest), it should appear in the external session states enumeration.

### API-088. `lenny-ctl policy audit-isolation` uses client-side join, not a dedicated API endpoint [Low]

Section 24.14 documents `lenny-ctl policy audit-isolation` as performing a client-side join across `GET /v1/admin/delegation-policies` and `GET /v1/admin/pools`. This is the only `lenny-ctl` command documented as performing business logic via a client-side join rather than mapping to a single Admin API endpoint. The design principle stated at the beginning of Section 24 is that `lenny-ctl` is "a thin client over the Admin API with zero business logic" and that "every operation maps to an Admin API call." The `preflight` command is explicitly documented as the single exception. `audit-isolation` is a second undocumented exception. Either the principle statement should be updated to acknowledge both exceptions, or a dedicated server-side endpoint should be provided.

### API-089. Bootstrap seed `--force-update` uses `If-Match: *` which bypasses optimistic concurrency [Low]

Section 17.6 documents that `lenny-ctl bootstrap --force-update` uses `PUT` with `If-Match: *` to overwrite existing resources with differing fields. While `If-Match: *` is a valid HTTP conditional request mechanism, the ETag-based optimistic concurrency section (Section 15.1) never documents `If-Match: *` as a supported value -- it only discusses quoted decimal version strings (e.g., `"3"`). The spec should explicitly state whether `If-Match: *` is accepted by the gateway on PUT requests and what its semantics are (unconditional overwrite regardless of version), or the bootstrap mechanism should use a different approach.

### API-090. `PATCH` only defined for experiments; no guidance on whether PATCH is supported elsewhere [Low]

The Admin API endpoint table (Section 15.1) defines `PATCH /v1/admin/experiments/{name}` using JSON Merge Patch. No other resource supports PATCH. The spec does not state whether PATCH is intentionally excluded from all other admin resources or whether it is a future consideration. For an API with many resources that require frequent partial updates (e.g., updating a single field on a runtime definition currently requires PUT with the full resource body), the absence of PATCH on most resources is a design choice that should be explicitly documented as intentional rather than left as an apparent gap.

### API-091. `POST /v1/sessions/{id}/derive` with `allowStale: true` lacks consistency guarantees [Medium]

The derive endpoint documentation (Section 15.1) states that deriving from a non-terminal session with `allowStale: true` "uses the most recent successful checkpoint snapshot." However, the spec does not define what happens if the session has no successful checkpoint yet (e.g., a running session that has not yet reached its first periodic checkpoint interval). The response includes `workspaceSnapshotTimestamp`, which implies a snapshot must exist, but the error code for the "no checkpoint available" case is not specified. The endpoint should either define a specific error code (e.g., `NO_CHECKPOINT_AVAILABLE`) or clarify that derive from a running session with no checkpoint falls back to an empty workspace or the initial uploaded workspace.

### API-092. Idempotency key mechanism does not cover all state-mutating session endpoints [Medium]

Section 11.5 lists the operations that support idempotency keys: CreateSession, FinalizeWorkspace, StartSession, SpawnChild, Approve/DenyDelegation, Resume. However, several other state-mutating session endpoints are not covered: `POST /v1/sessions/{id}/interrupt`, `POST /v1/sessions/{id}/terminate`, `DELETE /v1/sessions/{id}`, `POST /v1/sessions/{id}/derive`, `POST /v1/sessions/{id}/messages`, `POST /v1/sessions/{id}/extend-retention`, and `POST /v1/sessions/{id}/eval`. Of these, `terminate`, `interrupt`, and `messages` are particularly sensitive to duplicate execution during client retries or gateway failover. The spec should either extend idempotency key support to these endpoints or document why they are excluded (e.g., terminate is inherently idempotent because transitioning an already-terminal session is a no-op).

### API-093. `GET /v1/sessions/{id}/messages` pagination not specified [Low]

The async job support table (Section 15.1, line ~6998) lists `GET /v1/sessions/{id}/messages` as "paginated" and "Returns message history including delivery receipts and state." However, the pagination section only documents cursor-based pagination for admin list endpoints. Whether `GET /v1/sessions/{id}/messages` uses the same cursor-based pagination envelope (with `limit`, `cursor`, `items`, `hasMore` fields) is not explicitly stated. Since this is a client-facing endpoint (not admin), the pagination contract should be documented. Additionally, the `?threadId=` and `?since=` filters mentioned in Section 7.2 (MessageDAG) should be listed as supported query parameters.

### API-094. `GET /v1/sessions/{id}/transcript` pagination not specified [Low]

Section 15.1 lists `GET /v1/sessions/{id}/transcript` as "paginated" but does not specify the pagination mechanism. The same concern as API-093 applies: whether this endpoint uses the cursor-based pagination envelope or a different mechanism (e.g., offset-based for transcript pages) is unspecified.

### API-095. A2A aggregated `/.well-known/agent.json` returns array, contradicting A2A spec, without versioning strategy [Medium]

Section 21.1 documents that the aggregated `/.well-known/agent.json` endpoint returns a JSON array instead of a single `AgentCard` object, and acknowledges this will cause deserialization errors in standard A2A clients. The mitigation includes a `Content-Type` header with a profile URI (`application/json; profile="https://lenny.dev/a2a-multi-agent"`) and `Link` headers. However, if the A2A spec later adopts a multi-agent discovery mechanism (which is a natural evolution), Lenny's custom extension may conflict with the standard. The spec does not describe a migration path from the Lenny extension to a future standard mechanism. While this is post-v1, the data model should include a versioning strategy for this extension so that the aggregated endpoint can evolve without breaking existing Lenny-aware clients.

### API-096. `POST /v1/admin/sessions/{id}/force-terminate` transitions to `failed` while `POST /v1/sessions/{id}/terminate` transitions to `completed` [Medium]

The admin force-terminate endpoint (Section 24.11, `lenny-ctl admin sessions force-terminate`) states "The session transitions immediately to `failed`." The client-facing terminate endpoint (`POST /v1/sessions/{id}/terminate`) transitions to `completed`. Meanwhile, `DELETE /v1/sessions/{id}` transitions to `cancelled`. This means three different endpoints produce three different terminal states for what is conceptually session termination. The distinction between `completed` (graceful client terminate), `cancelled` (client DELETE), and `failed` (admin force-terminate) should be explicitly rationalized. In particular, a session that was running correctly but was force-terminated by an admin for operational reasons (e.g., node drain) should arguably not be marked `failed` (which implies an error), and the spec should clarify the semantic intent of each terminal state.

### API-097. `POST /v1/sessions/{id}/tool-use/{tool_call_id}/approve` and `/deny` lack idempotency coverage [Low]

Tool-use approval/denial endpoints are listed in the session lifecycle endpoints but are not covered by the idempotency key mechanism (Section 11.5 lists "Approve/DenyDelegation" but not tool-use approve/deny). These are distinct operations: delegation approval/denial (`lenny/approve_delegation`, `lenny/deny_delegation`) versus tool-use approval (`POST /v1/sessions/{id}/tool-use/{tool_call_id}/approve`). If a client's approve request times out and they retry, the retry must be safe. The spec should clarify whether tool-use approve/deny endpoints are inherently idempotent (approving an already-approved tool call returns 200) or require idempotency keys.

### API-098. `GET /v1/sessions/{id}/setup-output` not available for sessions that failed during setup [Low]

The session artifacts table lists `GET /v1/sessions/{id}/setup-output` for retrieving setup command stdout/stderr. However, the spec does not clarify whether this endpoint is available when a session fails during the `finalizing` state (setup command failure). If setup commands fail and the session transitions to `failed`, the setup output would be the primary diagnostic artifact. The endpoint's availability across session states should be specified -- particularly whether it returns partial output when setup fails mid-execution.

### API-099. Webhook signing key rotation lacks API surface [Medium]

Section 7.1 documents that the `uploadToken` HMAC signing keys are rotated on a deployer-configurable schedule with a short overlap window. The same section mentions webhook delivery uses HMAC-SHA256 signatures. However, the webhook signature key management is not specified: how webhook receivers obtain the signing secret for verification, whether the signing key is per-tenant or global, how key rotation is communicated to webhook receivers, and whether there is an API endpoint to retrieve the current webhook verification key(s). The client SDKs include "webhook signature verification" (Section 15.6), but the server-side key management and distribution mechanism is missing from the spec.

### API-100. `GET /v1/pools` listed as client-facing but pool details may leak operational information [Low]

Section 15.1 lists `GET /v1/pools` as a client-facing discovery endpoint that returns "pools and warm pod counts." Exposing warm pod counts to non-admin clients could reveal operational capacity information (current utilization, scaling state) that may be sensitive in multi-tenant deployments. The spec does not define what fields are returned to different role levels (`user`, `tenant-admin`, `platform-admin`). Admin endpoints have their own pool details (`GET /v1/admin/pools/{name}`), so the client-facing `GET /v1/pools` should specify which fields are included and whether warm pod counts are filtered for non-admin callers.

### API-101. Session creation response does not include the `state` field [Low]

The `POST /v1/sessions` response is described as including `sessionId`, `uploadToken`, and `sessionIsolationLevel` (Section 7.1). The session's initial state is `created`, but the response does not include a `state` field. Clients must make a separate `GET /v1/sessions/{id}` call to confirm the state. Since every other state-mutating endpoint implicitly changes state, and the session state is documented as externally visible, including `state` in the creation response would be a natural API convenience. This is a minor ergonomic gap, not an error.

### API-102. `POST /v1/admin/bootstrap` audit event emission on dryRun is an exception to the general dryRun rule [Low]

The dryRun documentation (Section 15.1) states that "Audit events are not emitted for dry-run requests, with one exception: `POST /v1/admin/bootstrap?dryRun=true` emits a `platform.bootstrap_applied` audit event with `dryRun: true`." This exception is documented but creates an inconsistency in the dryRun contract: clients that assume dryRun is side-effect-free (which the general documentation implies) may be surprised that an audit record is created. The rationale (operators wanting a record of what bootstrap would have changed) is reasonable, but the exception should be surfaced more prominently -- perhaps as a note in the general dryRun documentation rather than buried in the endpoint-specific section.

### API-103. `GET /v1/sessions/{id}/usage` tree-aggregated usage has no documented response schema [Medium]

Section 15.1 lists `GET /v1/sessions/{id}/usage` as returning "tree-aggregated usage (including all descendant tasks) when the session has a delegation tree." However, the response schema for this endpoint is not specified. It is unclear what fields are returned (just token counts? per-child breakdowns? cost estimates?), how the tree aggregation is structured (flat sum vs. nested tree), and whether the response includes both the session's own usage and its descendants separately. The `GET /v1/usage` response schema is documented as an aggregated endpoint, but the per-session usage endpoint's response structure is absent.

### API-104. `POST /v1/credentials` (user-scoped credential registration) referenced but endpoint not in the REST API table [Medium]

Section 4.9 documents `POST /v1/credentials` as the endpoint for user pre-registration of their own API keys ("Bring your own API key" flow). Phase 11 references this endpoint in the build sequence. However, the comprehensive REST API endpoint table in Section 15.1 does not include `POST /v1/credentials` or any `/v1/credentials` endpoints. The full CRUD operations for user-scoped credentials (create, list, delete, update) are not documented in the API surface. This is a missing API specification for a feature explicitly described as part of the v1 credential lifecycle.

### API-105. `POST /v1/sessions/{id}/eval` rate limit specified but not in the rate-limit configuration model [Low]

Section 10.7 specifies a per-session eval submission rate limit of 100/min and a per-tenant global cap of 10,000/min. These rate limits are hard-coded in the spec text rather than being part of the configurable rate-limit model. The spec does not indicate whether deployers can adjust these limits via Helm values or the admin API. If they are fixed limits, they should be documented as such in the operational defaults table (Section 17.8.1). If they are configurable, the configuration mechanism should be specified.

---

## 15. Competitive Positioning (CPS)

### CPS-052. Competitive landscape table omits kubernetes-sigs/agent-sandbox licensing and governance compatibility analysis [Low]

**Location:** Section 23, line 10194

The competitive landscape table lists `kubernetes-sigs/agent-sandbox` as "adopted as Lenny's infrastructure layer" but does not specify the upstream project's license or governance model. Section 23.2 (line 10252) notes that license evaluation must consider "compatibility with `kubernetes-sigs/agent-sandbox` upstream" and lists candidate licenses, but the competitive landscape table itself does not record the upstream license. Since Lenny declares a hard dependency on this project and license compatibility is a Phase 0 gating item (ADR-008), the competitive landscape entry should state the upstream license so evaluators can assess compatibility without cross-referencing external sources.

**Recommendation:** Add the `kubernetes-sigs/agent-sandbox` license to its competitive landscape entry.

---

### CPS-053. LangSmith competitive entry contains redundant claims [Low]

**Location:** Section 23, line 10199

The LangSmith entry states three distinct points: (1) "RemoteGraph provides graph-level delegation without per-hop token budget or scope controls," (2) "LangSmith's A2A and MCP support operates within the LangChain ecosystem," and (3) "LangSmith offers self-hosted Kubernetes deployment... however, it requires LangChain ecosystem coupling, does not provide runtime-agnostic adapter contracts, and lacks per-hop budget/scope controls in delegation chains." Points 1 and 3 repeat the same two claims (LangChain coupling and no per-hop controls) using different phrasing. This is not an error per se, but the redundancy makes the table entry harder to parse and could give the appearance of inflating the differentiation. The table should state each competitive distinction once.

**Recommendation:** Consolidate the LangSmith entry to remove the duplicated claims.

---

### CPS-054. No Python client SDK despite Python being the dominant AI/ML ecosystem language [Medium]

**Location:** Section 15.6 (line 8520), Section 23.1 (line 10229)

The spec explicitly acknowledges Python as a "known gap" (line 10229) in the trade-offs section and provides only Go and TypeScript SDKs at v1. Section 23.2 identifies "Runtime authors" as the primary community persona, with entry point being "Runtime adapter contract (Section 15.4), `make run` local dev mode." The majority of AI agent frameworks in the competitive landscape (LangChain/LangGraph, CrewAI, OpenAI Agents SDK) are Python-first. The built-in `runtimeOptionsSchema` examples (Section 14, lines 6680-6706) include `langgraph` and `openai-agents` runtimes that are Python-based. However, the community adoption strategy (Section 23.2) does not include any timeline, phase, or even post-v1 item for a Python client SDK. The Build Sequence (Section 18) lists Phase 6 for "Go + TypeScript/JavaScript" SDKs but never mentions Python.

This is a genuine gap in the community adoption strategy: the spec identifies the problem but provides no path to address it, not even as a post-v1 planned item (Section 21). For a platform targeting runtime authors in the Python-dominant AI ecosystem, this is a notable omission in the adoption plan.

**Recommendation:** Add a Python client SDK to Section 21 (Planned / Post-V1) or to Section 23.2 as a community adoption roadmap item, even if the timeline is unspecified.

---

### CPS-055. TTHW target does not specify hardware baseline [Low]

**Location:** Section 23.2, line 10242

The Time to Hello World target is "< 5 minutes" on "a standard development machine." The term "standard development machine" is undefined. Competitors like E2B publish specific hardware requirements for their self-hosted quickstarts. Without a baseline (e.g., "4-core CPU, 8 GB RAM, SSD"), the TTHW target is not measurable and the CI smoke test that "validates this path on every merge" is validated on CI hardware, not the claimed "standard development machine."

This is a minor specification gap -- the TTHW target is not falsifiable as written.

**Recommendation:** Define the minimum hardware spec for the TTHW target (e.g., CPU cores, RAM, disk type).

---

### CPS-056. Comparison guides listed as Phase 17 deliverable but not enumerated in Section 21 (Post-V1) or Section 18 build table [Medium]

**Location:** Section 23.2, line 10256; Section 18, line 10092

Section 23.2 states: "Phase 17 deliverables include published comparison guides covering Lenny vs E2B, Daytona, Fly.io Sprites, Temporal, Modal, and LangGraph." The Build Sequence Phase 17a (line 10092) confirms "comparison guides" as a deliverable. However, the comparison guides are documentation deliverables that require ongoing maintenance as competitors evolve. The spec does not address who maintains them post-launch, whether they are versioned, or what happens when a competitor ships a major update (e.g., E2B adds delegation support).

This is a minor gap: comparison guides are named as deliverables but have no maintenance or ownership model, which for an open-source project is a practical concern since stale comparison guides damage credibility.

**Recommendation:** This is a documentation planning concern, not a spec error. No change required unless the project wants to specify a maintenance cadence.

---

### CPS-057. Section 23.1 "Known trade-offs" lists "Standard/Full-tier adapter effort" but Section 23.2 entry point does not mention the effort gradient [Low]

**Location:** Section 23.1 line 10230, Section 23.2 line 10238

Section 23.1 acknowledges that "Standard/Full-tier adapter effort means runtime authors targeting MCP integration or checkpoint/restore invest in Lenny-specific gRPC adapter work." However, Section 23.2's "Runtime authors" persona row lists the entry point as "Runtime adapter contract (Section 15.4), `make run` local dev mode" without any indication of the effort gradient across tiers. A runtime author evaluating Lenny from Section 23.2 would not know that Minimum tier is ~50 lines of code while Full tier requires significant gRPC integration until they read Section 15.4.

This is a presentation gap, not a technical error -- the information exists elsewhere but the community adoption table does not surface the most decision-relevant detail for its primary persona.

**Recommendation:** Add a brief effort indication to the Runtime authors row (e.g., "from ~50 LOC stdin/stdout to full gRPC lifecycle").

---

### CPS-058. Google A2A Protocol competitive entry references governance body without citing specific implications [Low]

**Location:** Section 23, line 10197

The A2A entry states it is "governed by the Linux Foundation (Joint Development Foundation); separate from MCP's governance." This governance distinction is stated but no implication for Lenny is drawn from it. The competitive landscape table's column is "Why It Matters for Lenny" -- if the governance separation matters, the entry should explain how (e.g., licensing implications, protocol divergence risk, dual-governance overhead). If it does not matter, the governance parenthetical is irrelevant detail.

**Recommendation:** Either explain the competitive implication of A2A's governance structure or remove the parenthetical.

---

### CPS-059. Community adoption strategy does not address contributor retention or incentive mechanisms [Low]

**Location:** Section 23.2, lines 10244-10254

The governance model covers decision-making (BDfN, ADRs, steering committee transition), contribution mechanics (CONTRIBUTING.md, PR process), and communication (issue tracker, discussions forum). It does not address contributor retention mechanisms such as recognition programs, maintainer promotion criteria, or the criteria for the "3+ regular contributors" threshold that triggers governance transition. "Regular contributor" is undefined.

This is a minor specification gap in the governance model -- not a technical error, but a gap in the community strategy for an open-source project.

**Recommendation:** Define "regular contributor" criteria for the governance transition threshold.

---

### CPS-060. Build Sequence Phase 0 has no timeline or effort estimate [Low]

**Location:** Section 18, line 10031

Phase 0 is a "Pre-implementation gating decision" requiring license selection (ADR-008) and SandboxClaim verification (ADR-007). These are described as hard prerequisites for Phase 1. However, neither Phase 0 nor any other section provides an expected timeline or effort estimate. For an open-source project where Phase 0 gates all subsequent development, this is relevant to adoption planning -- potential contributors evaluating the project need to know whether Phase 0 is days or months of work.

This is a minor gap -- timeline is understandably absent from a technical spec, but the community adoption strategy references Phase 0 as a gating item without indicating its expected duration.

**Recommendation:** No spec change needed; this is a project management concern rather than a spec error.

---

### CPS-061. Latency comparison note acknowledges non-comparable numbers but retains them in the same table [Medium]

**Location:** Section 23, lines 10195-10205

The competitive landscape table lists competitor cold-start times (E2B ~150ms, Daytona sub-90ms, Fly.io ~300ms) alongside Lenny's SLO targets (P95 < 2s runc, < 5s gVisor). The note at line 10205 correctly explains that these numbers measure different operations and are "not directly comparable." However, the table itself juxtaposes them without any inline qualifier, creating a misleading visual impression of a 10-30x latency disadvantage for Lenny. Readers who scan the table without reading the note will draw incorrect conclusions.

This is a genuine presentation problem that could affect competitive positioning. The note is thorough but the table structure works against it.

**Recommendation:** Add an inline qualifier to Lenny's SLO numbers in Section 6.3 or add a "Measures" column to the competitive table so the scope difference is visible in the table itself, not only in a note below it.

---

### CPS-062. `CONTRIBUTING.md` early-development notice creates a contributor engagement gap [Low]

**Location:** Section 23.2, line 10248

The Phase 2 `CONTRIBUTING.md` "must include a prominent early-development notice stating that the project is in active pre-release development and that unsolicited PRs will not be reviewed or merged until Phase 17a." This means the project is visible and documented from Phase 2 but explicitly rejects contributions until Phase 17a -- a span of 15 build phases. For an open-source project, this creates a contributor disengagement risk: early interested community members who discover the project in Phases 2-16 are told to wait, and may not return.

The spec addresses this partially by allowing "Bug reports and discussion via the issue tracker" at any phase. However, this still represents a long no-PR window that could affect the community adoption strategy's effectiveness.

This is not a spec error but a strategic tension between the adoption strategy (Section 23.2) and the build sequence (Section 18) that is worth acknowledging.

**Recommendation:** No spec change required; this is a deliberate project management decision.

---

### CPS-063. Competitive landscape omits Replit Agent and GitHub Copilot Workspace as comparison points [Low]

**Location:** Section 23, lines 10192-10203

The competitive landscape covers E2B, Fly.io Sprites, Daytona, Temporal, Modal, and LangGraph. It omits Replit Agent (cloud agent sessions with Nix-based isolation, ~2s cold start) and GitHub Copilot Workspace (cloud-hosted agent development sessions). Both serve overlapping use cases with Lenny -- on-demand cloud agent sessions for code tasks. While these are closed-source commercial products, E2B and Fly.io Sprites are also commercial products and are included. The omission is not an error (the table is not claimed to be exhaustive), but for evaluators comparing options, these are notable gaps.

**Recommendation:** Consider adding entries for Replit Agent and GitHub Copilot Workspace, or add a note that the table covers only platforms offering self-hosting or open-source options.

---


Carried forward (skipped per instructions): CPS-043 (business strategy), CPS-048 (business strategy).

The 3 Medium findings are: CPS-054 (no Python SDK despite Python-dominant ecosystem, with no post-v1 plan), CPS-056 (comparison guides have no maintenance model), and CPS-061 (latency table juxtaposes non-comparable numbers despite noting the issue). The remaining 9 Low findings are presentation or minor completeness issues in the competitive positioning and community adoption sections. No Critical or High findings were identified -- the competitive landscape section (23) and community adoption strategy (23.2) are thorough and self-aware about trade-offs.

---

## 16. Warm Pool & Pod Lifecycle (WPL)

### WPL-049. Pod state machine diagram omits `resuming` state transitions [Medium]

**Section 6.2** defines the pod state machine with states including `resume_pending -> resuming -> ...`, but the full state machine diagram (lines ~2401-2600) shows `resume_pending -> resuming` only on the entry side. The transitions **out** of `resuming` are not enumerated in the state machine diagram itself. Section 10.1 mentions that `resuming -> resume_pending` or `resuming -> awaiting_client_action` can occur (adapter terminates during resuming), and there is a "300-second resuming watchdog" referenced. However, the pod state machine in Section 6.2 does not list `resuming -> failed` (watchdog timeout or unrecoverable error), `resuming -> running` (success), or `resuming -> resume_pending` (re-failure during resume). These transitions are only discoverable by cross-referencing other sections.

**Impact:** Implementers relying on the Section 6.2 state machine as the authoritative reference will miss valid `resuming` exit transitions.

---

### WPL-050. RuntimeUpgrade metric labels inconsistent with state machine states [Low]

**Section 16.1** defines `lenny_runtime_upgrade_state` as a gauge labeled by `state: idle, draining, migrating, verifying, complete`. However, **Section 10.5** defines the `RuntimeUpgrade` state machine with states: `Pending, Expanding, Draining, Contracting, Complete, Paused`. The metric uses `idle, draining, migrating, verifying, complete` -- three of the five label values (`idle`, `migrating`, `verifying`) do not correspond to any defined state machine state, and three actual states (`Pending`, `Expanding`, `Contracting`, `Paused`) have no matching label value.

**Impact:** The metric is unusable for monitoring the state machine as specified. Either the metric labels or the state machine states need to be aligned.

---

### WPL-051. Task-mode scrub procedure lacks file descriptor and IPC cleanup [Medium]

**Section 5.2** defines the task-mode scrub procedure (steps 0-6, plus step 7 for Kata). The steps cover workspace removal, process-group kill, scratch directory cleanup, cgroup memory/PID reset, and network state reset (iptables, conntrack). However, the scrub procedure does not address **open file descriptors inherited by long-lived processes** (e.g., the adapter process itself, which persists across tasks), **System V IPC resources** (shared memory segments, semaphores, message queues), or **/dev/shm contents**. A prior task's runtime could create shared memory segments or leave open file descriptors to deleted files (holding disk space), which persist into the next task.

**Impact:** Residual IPC state or file descriptor leaks across task boundaries could cause information leakage between tenants when `allowCrossTenantReuse: true`, or resource exhaustion from accumulated leaked descriptors.

---

### WPL-052. Concurrent-workspace leaked slot GC relies on Redis counter that can drift [Medium]

**Section 5.2** describes concurrent-workspace slot management using atomic Redis `INCR`/`DECR` via Lua scripts for the `lenny:pod:{pod_id}:active_slots` counter. **Section 10.1** describes post-recovery rehydration of this counter from `SessionStore.GetActiveSlotsByPod(pod_id)`. However, the spec does not define a **periodic reconciliation** between the Redis counter and the Postgres session store during normal operation (non-failure scenarios). Over time, if a `DECR` is lost (e.g., Redis accepted the `DECR` but the gateway crashed before updating Postgres, or vice versa), the counter can drift permanently. The rehydration procedure described only triggers on connection loss and replacement pod allocation -- not on running pods.

**Impact:** A drifted counter on a long-lived concurrent-mode pod could permanently prevent new slot allocation (counter too high) or allow over-admission beyond `maxConcurrent` (counter too low). The leaked slot semantics in Section 5.2 compound this -- leaked slots already reduce effective concurrency, and counter drift would make the `active_slots` metric unreliable for detecting leaks.

---

### WPL-053. `mode_factor` derivation from histogram quantile is undefined for new pools [Medium]

**Section 5.2** states that the PoolScalingController uses `histogram_quantile(0.50, ...)` over `lenny_task_reuse_count` to derive `mode_factor` for task-mode pools, reducing `minWarm` proportionally. However, for a newly created task-mode pool in bootstrap mode, the histogram has no data. The spec does not define what `mode_factor` value the controller should use during the bootstrap period when the histogram is empty. The bootstrap procedure (Section 17.8.2) describes `bootstrapMinWarm` as a static override, but once bootstrap exits, the formula needs `mode_factor`. If the histogram has insufficient data at convergence time (e.g., only a few tasks completed in 48 hours on a low-traffic pool), the derived `mode_factor` could be wildly inaccurate.

**Impact:** Task-mode pools transitioning out of bootstrap could experience a sudden `minWarm` drop (if `mode_factor` is overestimated from sparse data) or over-provisioning (if it defaults to 1.0).

---

### WPL-054. Warm pool cert expiry drain races with SDK-warm demotion [Low]

**Section 10.3** (cert-manager failure modes) states the WarmPoolController "continuously tracks certificate expiry on idle pods and proactively drains any idle pod whose certificate will expire within 30 minutes, replacing it with a fresh pod." For SDK-warm pods, the pod has already started the agent session (Section 6.1). If the controller drains an SDK-warm idle pod due to cert proximity to expiry, the spec does not address whether the SDK teardown (which Section 6.1 says incurs a 1-3s penalty) is needed, or whether the controller simply deletes the pod. This is functionally correct (pod is idle, no session is active), but the interaction between cert-based proactive drain and SDK-warm session teardown is not explicitly specified.

**Impact:** Minor operational ambiguity. The controller might need to send a lifecycle `terminate` to the SDK-warm runtime before deletion to ensure clean shutdown of the pre-connected session, but this is not specified.

---

### WPL-055. Bootstrap convergence criterion "variance < 20%" is ambiguous [Medium]

**Section 17.8.2** defines bootstrap convergence criteria including: "the controller's formula-computed `target_minWarm` has been stable (variance < 20% across consecutive reconciliation cycles over a 1-hour rolling window) for at least 2 hours." The term "variance < 20%" is ambiguous -- it could mean: (a) coefficient of variation (standard deviation / mean) < 0.20, (b) max/min ratio within the window < 1.20, or (c) each consecutive pair of computed values differs by less than 20%. These yield very different convergence sensitivity. Additionally, if `target_minWarm` oscillates between 10 and 12 (a 20% difference), whether this satisfies "variance < 20%" depends on interpretation.

**Impact:** Ambiguous convergence criteria could cause pools to remain in bootstrap mode indefinitely (if the implementation uses a strict interpretation) or exit prematurely (if lenient), both affecting warm pool sizing accuracy.

---

### WPL-056. Topology spread constraints not specified for concurrent-mode pods [Medium]

**Section 5.3** describes topology spread constraints for pod distribution, including `topologySpreadConstraints` with `maxSkew`. The pod state machine and pool taxonomy cover session, task, and concurrent execution modes. However, the topology spread behavior for concurrent-mode pods is not explicitly addressed. A concurrent-workspace pod with `maxConcurrent: 20` represents 20x the blast radius of a session-mode pod. If concurrent-mode pods are spread using the same `maxSkew` as session-mode pods, a single node failure could simultaneously disrupt 20 concurrent sessions rather than 1. The spec does not call out that concurrent-mode pools should use tighter spread constraints proportional to their multiplied blast radius.

**Impact:** Node failures in deployments with high-concurrency pods could cause disproportionate session disruption compared to session-mode deployments with the same `maxSkew` configuration.

---

### WPL-057. `maxTasksPerPod` retirement not integrated with RuntimeUpgrade drain [Medium]

**Section 5.2** defines task-mode pod retirement policy with `maxTasksPerPod` and `maxPodUptimeSeconds`. **Section 10.5** defines the `RuntimeUpgrade` state machine's `Draining` state where old pool pods run to completion. The spec does not address the interaction: during `Draining`, should `maxTasksPerPod` retirement still be enforced on old-pool pods? If yes, retiring pods during drain is wasteful (no new pods are created in the old pool). If no, the retirement policy is silently suspended during upgrades, which could cause pods to run far beyond their intended lifecycle. The spec should explicitly state whether retirement policies are suspended during the `Draining` phase.

**Impact:** Ambiguous behavior during runtime upgrades for task-mode pools. Enforcing retirement during drain creates unnecessary pod churn; not enforcing it violates the stated retirement invariants.

---

### WPL-058. Pre-attached failure retry policy unclear on credential lease handling [Medium]

**Section 6.2** describes a pre-attached failure retry policy (2 retries, exponential backoff) for pod failures before a session is fully attached. The retry claims a new pod from the warm pool. However, the spec does not explicitly state whether the **credential lease** from the original attempt is reused or re-evaluated on retry. Section 7.1 (steps 6-7) shows credential assignment as part of the atomic session creation flow. If a retry reclaims a new pod but reuses the original credential lease, the lease may have been partially consumed. If re-evaluated, the credential pool could be exhausted by retries under high load. The atomicity statement in Section 7.1 says "the gateway rolls back any partially allocated resources (releases the pod claim, revokes the credential lease)" on failure, but it is unclear whether the pre-attached retry re-executes the full step 6 credential evaluation.

**Impact:** Could lead to credential lease orphaning on retry (if not properly released from the failed attempt) or credential pool exhaustion under high retry rates.

---

### WPL-059. WarmPoolController crash recovery: orphan pod detection delay [Medium]

**Section 10.1** describes the orphan session reconciler running every 60 seconds, which cross-references the `agent_pod_state` mirror table. The WarmPoolController updates this mirror table. During a WarmPoolController crash (up to 25s failover), the mirror becomes stale. The spec adds a `PodStateMirrorStale` warning alert when lag exceeds 60s and a fallback to direct Kubernetes API queries. However, between the WarmPoolController crash and the staleness detection (60s threshold), the orphan reconciler is operating on stale data. If a pod terminates during this window, the mirror still shows it as alive, and the orphan reconciler will not flag the corresponding session. The combined worst-case detection delay is: controller failover (25s) + mirror staleness threshold (60s) + reconciler interval (60s) = up to **145 seconds** of undetectable orphan sessions.

**Impact:** Sessions whose pods terminate during WarmPoolController failover may remain in non-terminal state for up to ~2.5 minutes, holding quota and preventing cleanup. The `coordinatorHoldTimeoutSeconds` (120s) partially bounds this, but sessions without an active coordinator connection (e.g., coordinator already failed) are not covered by the hold timeout.

---

### WPL-060. Cold-start bootstrap `PoolBootstrapUnderprovisioned` threshold is one-directional [Low]

**Section 17.8.2** states that the controller emits a `PoolBootstrapUnderprovisioned` warning "rather than silently switching to a much larger formula value" when "the formula-computed target does not exceed 3x the bootstrap minWarm value." This is correctly worded as a guard against sudden upward jumps. However, there is no symmetric guard for the **downward** case: if the bootstrap `minWarm` is 100 and the formula computes 5, the controller silently switches from 100 to 5 pods on convergence exit. This could cause immediate warm pool exhaustion if the 48-hour observation window happened to capture an unrepresentatively low traffic period (e.g., initial rollout with few users).

**Impact:** Pools could drop from a generous bootstrap warm count to a near-zero formula-derived count on convergence, causing a `WarmPoolExhausted` event if traffic is higher than the formula predicts.

---

### WPL-061. Checkpoint barrier protocol does not account for concurrent-workspace pods [Medium]

**Section 10.1** describes the `CheckpointBarrier` protocol for rolling updates: the gateway sends a barrier to "every pod currently coordinated by this replica" and waits for `CheckpointBarrierAck` from all pods under a single wall-clock deadline. For concurrent-workspace pods, a single pod may be serving multiple active slots, each potentially in different states (some mid-tool-call, some idle). The spec says "the adapter finishes the current tool call execution (if any), then stops accepting new tool call dispatches." For a concurrent pod with 20 active slots, "the current tool call" is ambiguous -- there could be 20 simultaneous tool calls in flight across different slots. The barrier protocol does not specify whether quiescence requires **all** slots to finish their current tool calls, or just the slot with the longest-running tool call, or whether per-slot barriers are needed.

**Impact:** Without per-slot quiescence semantics, rolling updates on concurrent-workspace pods could produce inconsistent checkpoint state -- some slots checkpointed cleanly while others had in-flight tool calls at barrier time. Tool call idempotency keys (step 4) are per-session, which partially mitigates this, but the barrier semantics for multi-slot pods should be explicit.

---

### WPL-062. `failover_seconds` formula uses 25s but `coordinatorHoldTimeoutSeconds` is 120s [Medium]

**Section 17.8.2** states "Use `failover_seconds = 25` (worst-case crash scenario: `leaseDuration + renewDeadline = 15s + 10s`)" for the `minWarm` formula. This 25s value covers the **WarmPoolController** leader election failover time. However, **Section 10.1** defines `coordinatorHoldTimeoutSeconds` as 120s (the time an adapter holds before self-terminating when no coordinator reconnects). If a gateway coordinator crashes and no new coordinator takes over within 120s, the session terminates and the pod transitions to `failed` -- releasing the pod from the warm pool perspective. The `minWarm` formula does not account for pods that are "consumed" by sessions in hold state (not yet released, not yet serving new sessions) during coordinator recovery. These pods are neither `idle` (available for claims) nor `running` (actively serving) -- they are effectively frozen.

**Impact:** During combined gateway + controller disruptions, the effective available warm pool is reduced by the number of pods in coordinator hold state. The `minWarm` formula should account for this hold-state inventory, especially at Tier 3 where a single gateway replica could coordinate up to 400 sessions.

---

### WPL-063. `SandboxClaim` guard webhook `failurePolicy: Fail` creates single point of failure [High]

**Section 4.6.1** (referenced by Section 16.5) describes the `lenny-sandboxclaim-guard` ValidatingAdmissionWebhook with `failurePolicy: Fail`. The alert `SandboxClaimGuardUnavailable` (Section 16.5) correctly identifies that when this webhook is unreachable, "all PATCH and PUT operations on SandboxClaim resources are blocked -- new pod claims are prevented, halting session creation." This webhook is a session-creation critical path dependency with `failurePolicy: Fail`, meaning any outage of the webhook deployment (OOM, scheduling failure, cert expiry) halts all new session creation platform-wide. The spec does not define **redundancy requirements** for the webhook deployment (replica count, PDB, anti-affinity) or a **degraded mode** where claims proceed with weaker guarantees (e.g., Postgres-backed claim with a brief double-claim risk window).

**Impact:** A single webhook deployment failure blocks all session creation across all pools and tiers. Given that the webhook prevents double-claims (a correctness concern, not a safety concern -- double-claims waste a pod but do not cause data loss), the `failurePolicy: Fail` choice may be overly conservative for a session-creation hot path.

---

### WPL-064. Warm pool `minWarm` formula burst term uses `pod_warmup_seconds` inconsistently [Medium]

**Section 4.6.2** and **Section 17.8.2** both state the formula: `minWarm >= claim_rate * safety_factor * (failover_seconds + pod_startup_seconds) + burst_p99_claims * pod_warmup_seconds`. The first term uses `pod_startup_seconds` (10s baseline) while the burst term uses `pod_warmup_seconds`. Section 17.8.2 clarifies that `pod_warmup_seconds` for SDK-warm pools is "30-90s, far exceeding the 10s startup baseline." However, the first term (sustained demand during failover) also needs pods to reach `idle` state to serve claims. If `pod_warmup_seconds` is 90s for SDK-warm pools, then `failover_seconds + pod_startup_seconds = 35s` underestimates the time needed because pods created during failover also take 90s to warm up, not 10s. The sustained-demand term should use `pod_warmup_seconds` (not `pod_startup_seconds`) for SDK-warm pools, just as the burst term does.

**Impact:** SDK-warm pools sized using the formula with `pod_startup_seconds = 10s` in the first term will be significantly under-provisioned. A pool with `claim_rate = 5/s` and SDK-warm `pod_warmup_seconds = 60s` needs `5 * 1.5 * (25 + 60) = 638` warm pods, not `5 * 1.5 * 35 = 263`. The 2.4x underprovisioning could cause warm pool exhaustion during controller failover.

---

### WPL-065. etcd write pressure mitigation (`statusUpdateDeduplicationWindow`) interaction with pod state machine timing [Low]

**Section 4.6.1** and **Section 17.8.2** describe `statusUpdateDeduplicationWindow` (250ms-500ms) to reduce etcd write pressure from CRD status updates. The pod state machine (Section 6.2) has rapid state transitions on the hot path: `claimed -> receiving_uploads -> finalizing_workspace -> running_setup -> starting_session -> attached`. If the deduplication window is 500ms and a session completes pod claim + upload + finalize + setup + session start in under 500ms (feasible for small workspaces with no setup commands), intermediate states may be coalesced and never written to etcd. This is likely intentional for performance, but the spec does not state whether the deduplication window is applied to CRD **status** updates only or also to CRD **spec** updates, and whether the controller's reconciliation logic depends on observing intermediate states.

**Impact:** Mostly informational. If the controller depends on observing intermediate states for metrics or health tracking, the deduplication window could cause silent data loss. If only terminal state transitions matter, this is benign.

---

---

## 17. Credential Management (CRD)

### CRD-051. Adapter-Side Lease Timer Not Resilient to Adapter Crash (Direct Mode, `anthropic_direct`) [Medium]

**Location:** Section 4.9, line ~1131

The spec states that in direct delivery mode for `anthropic_direct`, "the adapter MUST set a local timer for each credential lease's `expiresAt`." If the timer fires without a replacement lease, the adapter deletes the credential file and reports `AUTH_EXPIRED`. However, the spec does not address what happens if the adapter process crashes and restarts while holding a lease. The local timer state is lost in memory. On restart, the adapter would need to reconstruct lease expiry timers from durable state, but no such reconstruction protocol is specified. The adapter manifest (`/run/lenny/adapter-manifest.json`) does not include lease expiry information, and the credential file (`/run/lenny/credentials.json`) does not carry `expiresAt` metadata per the documented schema. The adapter could re-read lease metadata from the gateway on reconnect, but the gRPC lifecycle state machine (Section 15.4.2) does not specify a "lease re-sync" step in the INIT->READY transition after crash recovery.

**Impact:** After an adapter crash and restart in direct mode with `anthropic_direct`, the lease TTL enforcement is silently lost. The long-lived API key on disk remains usable beyond the intended lease boundary, defeating the synthetic TTL mechanism.

**Recommendation:** Specify that either (a) the `AssignCredentials` response metadata is persisted to a file the adapter reads on restart (e.g., alongside the credential file), or (b) the adapter re-fetches lease metadata from the gateway as part of the INIT→READY transition, or (c) on restart the adapter unconditionally deletes all credential files and waits for a fresh `AssignCredentials`, forcing re-lease.

---

### CRD-052. Credential File Owner UID Ambiguity [Low]

**Location:** Section 4.7 / Section 6.4 (referenced from earlier reading)

The credential file at `/run/lenny/credentials.json` is specified with permissions `mode 0400` (read-only by owner). The file is described as being written by the adapter process and read by the agent binary. If the adapter and agent run as different UIDs (the adapter typically runs as root or a privileged UID, while the agent runs as a non-root "agent UID" per Section 13.1), the file written by the adapter would be owned by the adapter's UID, making it unreadable by the agent UID under mode 0400. The spec does not explicitly state whether the adapter `chown`s the file to the agent UID after writing, or whether the adapter writes as the agent UID, or whether tmpfs mount permissions handle this.

**Impact:** Low -- implementers will likely figure this out during development, but an explicit statement prevents confusion for third-party runtime adapter authors.

**Recommendation:** Specify that the adapter writes the credential file as the agent UID (via `setuid` or equivalent), or that the adapter writes it and then `chown`s to the agent UID, or that the tmpfs is mounted with ownership matching the agent UID.

---

### CRD-053. Full-Tier Credential Rotation During Token Service Outage Not Explicitly Addressed [Medium]

**Location:** Section 4.9, "Token Service unavailability guard" paragraph, line ~1413

The Token Service unavailability guard explicitly addresses Standard/Minimum-tier runtimes: "the proactive renewal worker MUST NOT trigger the standard Fallback Flow for Standard/Minimum-tier runtimes" during Token Service outage, because checkpoint-and-restart would create a loop. However, the guard does not explicitly state the behavior for Full-tier runtimes. Full-tier runtimes support hot rotation via `RotateCredentials` and the lifecycle channel (`credentials_rotated`), which does not require checkpoint-and-restart. But the hot rotation still requires the Token Service to mint a replacement credential. The implicit expectation is that Full-tier runtimes also benefit from the timer extension (since no replacement credential can be minted), but this is not stated.

**Impact:** An implementer might read the guard as applying only to Standard/Minimum tiers and attempt to trigger the Fallback Flow for Full-tier runtimes during Token Service outage -- which would also fail at `AssignCredentials` time.

**Recommendation:** Extend the Token Service unavailability guard statement to apply to all tiers, or explicitly state Full-tier behavior during Token Service outage.

---

### CRD-054. `credentialPropagation: inherit` Partial Provider Intersection Behavior Unspecified [Medium]

**Location:** Section 8 (delegation), lines ~3100-3500

When `credentialPropagation: inherit` is set on a delegation hop, the child session inherits credential leases from the parent. The cross-environment compatibility check verifies that the child's Runtime `supportedProviders` intersects with the parent's active lease providers. However, if a pool serves multiple providers and only some intersect between parent and child (e.g., parent has leases for `anthropic_direct` and `aws_bedrock`, child runtime only supports `anthropic_direct`), the spec does not explicitly state whether the non-intersecting lease (`aws_bedrock`) is silently dropped, or whether the child receives only the intersecting subset, or whether the delegation fails.

**Impact:** Without clarification, implementers may handle this inconsistently -- some might fail the delegation if any provider doesn't intersect, while others might pass through only the subset.

**Recommendation:** Explicitly state that `credentialPropagation: inherit` passes only the provider intersection to the child and silently omits non-intersecting leases.

---

### CRD-055. Credential Deny-List Entry TTL vs. Proactively Renewed Leases [Medium]

**Location:** Section 4.9, emergency revocation (lines ~1500-1665)

The emergency revocation mechanism propagates a credential deny-list via Redis pub/sub. The spec states deny-list entries expire based on "natural lease TTL." However, proactively renewed leases have their `expiresAt` extended beyond the original `leaseTTLSeconds` on each renewal cycle. A deny-list entry that expires based on the original `leaseTTLSeconds` could expire before a proactively renewed lease that was using the compromised credential. This creates a window where the deny-list entry has expired but a renewed lease referencing the revoked credential is still active.

**Impact:** A revoked credential could be used after the deny-list entry expires if the lease was proactively renewed and its effective `expiresAt` exceeds the deny-list TTL.

**Recommendation:** Specify that deny-list entries persist for `max(leaseTTLSeconds, maxPossibleRenewedLeaseLifetime)` or remain in the deny-list until all active leases referencing the revoked credential are confirmed terminated. Alternatively, tie deny-list expiry to credential revocation status rather than a fixed TTL.

---

### CRD-056. `callbackSecret` Classified as T3 but Credential Pool Secrets are T4 [Low]

**Location:** Section 14, line ~6639 vs. Section 12.9 / Section 4.9

The `callbackSecret` (webhook HMAC signing secret) is classified as T3 (Confidential) per Section 14, while credential pool API keys are classified as T4 (Restricted). Both are stored using KMS envelope encryption in the same infrastructure. The T3 classification for `callbackSecret` is defensible (it's a webhook signing secret, not a provider API key), but the spec does not explicitly justify why the data classification differs given that both are secrets managed by the gateway. This is not an error -- just a missing rationale that a security reviewer would question.

**Impact:** Low -- the classification appears correct, but the lack of explicit rationale may cause audit friction.

**Recommendation:** Add a brief note in Section 14 explaining why `callbackSecret` is T3 (controls integrity of webhook delivery) rather than T4 (does not grant access to external provider resources).

---

### CRD-057. Dev Mode Docker Compose Credential Warning vs. TLS Profile Gap [Medium]

**Location:** Section 17.4, lines ~9029-9048

The spec includes a warning that Tier 2 `docker compose up` transmits credentials in plain HTTP and directs users to `make compose-tls` for real credentials. However, the credential-testing profile section states it "enables TLS by default" and "generates self-signed mTLS certificates on first run." The potential gap: the default `docker compose up` profile does not error or warn at runtime if real credentials are detected in the credential pool configuration. A developer could configure real API keys in `seed.yaml`, run the default `docker compose up` (not `compose-tls`), and transmit keys in plaintext without any runtime guard. The only protection is the documentation warning.

**Impact:** Real API keys could be transmitted in plaintext in local development if the developer does not read the warning.

**Recommendation:** Add a runtime guard in the dev-mode gateway that detects non-empty credential pool configurations when TLS is disabled and emits a startup error (or at minimum a prominent log warning) directing the user to the credential-testing profile.

---

### CRD-058. Credential Lease Not Explicitly Released on `created` State Timeout [Medium]

**Location:** Section 15.1, line ~6956

The `created` session state has a TTL (`maxCreatedStateTimeoutSeconds`, default 300s). The spec states: "On expiry the gateway transitions the session to `expired`, releases the pod claim back to the pool, and revokes the credential lease." This correctly mentions credential lease revocation. However, Section 7.1 (session lifecycle normal flow) describes credential assignment happening at step 6 (during `created` state, before finalization). The credential lease is assigned immediately at session creation. If the client never calls `finalize` or `start` and the `created` TTL expires, 300 seconds of credential lease capacity is consumed for a session that never ran. The spec does mention lease revocation on expiry, but does not discuss whether the credential's `maxConcurrentSessions` slot is held during the entire `created` timeout, potentially starving other sessions.

**Impact:** In high-contention credential pools, sessions that are created but never started could hold credential slots for up to 5 minutes, reducing effective pool capacity.

**Recommendation:** Consider documenting this as a known trade-off and recommend that deployers reduce `maxCreatedStateTimeoutSeconds` for pools with tight credential capacity, or consider lazy credential assignment (assign at `finalize` or `start` instead of `create`). The current design assigns credentials early to guarantee availability at start time, but the trade-off should be made explicit.

---

### CRD-059. Credential Audit Event Schema References Section 12.4 but Audit Events Are in Section 4.9.2 [Low]

**Location:** Various cross-references

The `credential.leased` audit event is referenced as being documented in "Section 12.4" (line ~1438: "The audit event `credential.leased` (Section 12.4) includes a `deliveryMode` field"), but the credential audit events are actually specified in Section 4.9.2 ("Credential Audit Events"). Section 12.4 covers Redis topology, not credential audit events. This appears to be a stale cross-reference.

**Impact:** Low -- incorrect cross-reference causes confusion for readers navigating the spec.

**Recommendation:** Fix the cross-reference from "Section 12.4" to "Section 4.9.2" (or whatever the correct section number is for credential audit events).

---

### CRD-060. Semantic Cache Erasure Halt-on-Error Does Not Specify Retry Behavior [Medium]

**Location:** Section 4.9, semantic caching, line ~1489

The spec states: "The erasure job treats a `DeleteByUser` call that returns an error as a hard failure -- the erasure job halts and does not proceed to subsequent stores." This correctly prevents a false erasure receipt. However, the spec does not specify whether the erasure job retries the `DeleteByUser` call before halting, how many retries are attempted, or whether the halted job can be retried via `lenny-ctl admin erasure-jobs retry`. The retry mechanism for erasure jobs generally is specified in Section 12.8, but the semantic cache-specific halt condition should cross-reference that mechanism or state whether it inherits the general retry behavior.

**Impact:** An operator whose erasure job fails on the semantic cache step has no documented path to recovery specific to this failure mode.

**Recommendation:** Cross-reference Section 12.8 retry mechanism, or specify inline that the general erasure job retry behavior (`lenny-ctl admin erasure-jobs retry`) applies, and that the retry resumes from the failed store (semantic cache).

---

### CRD-061. No Explicit Credential Scrub in Concurrent-Workspace Mode Between Slots [Medium]

**Location:** Section 5.2 (pool execution modes), Section 14 (workspace plan)

Task-mode execution (Section 5.2) includes explicit credential purge as "step 0" of the between-task scrub. The scrub deletes `/run/lenny/credentials.json` and verifies deletion before proceeding. However, concurrent-workspace mode assigns multiple slots to a single pod with a shared filesystem. The spec states that per-slot workspace differentiation is "intentionally out of scope" and that "all slots on a given pod are assigned tasks from sessions that share the same workspace plan." The credential implications are not addressed: if different slots are assigned to different sessions (even within the same tenant), do they share the same credential lease? If a slot's session ends and its credential lease is revoked, does the credential file (which is shared on the pod) remain accessible to other active slots?

**Impact:** In concurrent-workspace mode, credential isolation between slots on the same pod is unspecified. A revoked credential for one slot's session might still be readable by another slot's session via the shared filesystem.

**Recommendation:** Specify whether concurrent-workspace slots share a single credential lease (and thus all sessions on the pod use the same credentials) or whether per-slot credential files are maintained in separate paths (e.g., `/run/lenny/slots/{slotId}/credentials.json`). If shared, document the security implications.

---

### CRD-062. `env` Blocklist Validation Applies Only at Session Creation, Not Delegation [Medium]

**Location:** Section 14, line ~6612

The `env` blocklist for credential-sensitive environment variable patterns (e.g., `*_SECRET_*`, `*_KEY`, `*_PASSWORD`) is applied "at session creation time." However, delegation (Section 8) allows parent sessions to pass environment context to child sessions. The spec does not explicitly state whether the `env` blocklist is re-applied when a delegation creates a child session. If a parent session's workspace contains environment variables that were set before the blocklist check (e.g., injected by setup commands), those variables could propagate to child sessions without blocklist validation.

**Impact:** A parent session could propagate credential-sensitive environment variables to child sessions, bypassing the blocklist that would have caught them at top-level session creation.

**Recommendation:** Specify that the `env` blocklist is applied at delegation time for any `env` values passed in the `TaskSpec`, or document that delegation inherits the parent's already-validated environment without re-checking.

---

### CRD-063. Credential Pool Deletion Guard References Active Leases But Not Pending Renewals [Low]

**Location:** Section 15.1, line ~7411

The deletion guard for credential pools states: "Credential Pool: blocked if any active credential leases exist. Revoke leases first." However, a credential pool can have leases that are past `expiresAt` but whose sessions are in the `CredentialRenewalWorker`'s retry queue (waiting for renewal). These leases may not be "active" in the strict sense (they are expired) but are being actively renewed. Deleting the pool while the renewal worker is retrying could cause the retry to fail in an unexpected way (pool not found rather than credential not found).

**Impact:** Low -- edge case during pool decommissioning that could cause confusing error messages in renewal worker logs.

**Recommendation:** Clarify whether "active leases" includes leases in the renewal retry queue, or specify that the renewal worker drains before deletion.

---

### CRD-064. Network Policy for Proxy Mode vs. Direct Mode Egress Lacks Explicit Credential-Aware Rules [Low]

**Location:** Section 13.2 (network isolation), lines ~6057-6400

The spec describes delivery-mode-aware NetworkPolicies: proxy mode restricts pod egress to the gateway's proxy endpoint only, while direct mode allows egress to provider endpoints. This is well-specified. However, the NetworkPolicy rules are described at the pod level, and the delivery mode is per-credential-pool (not per-pod). A pod can hold leases from multiple pools with different delivery modes (one proxy, one direct). The spec does not address how the NetworkPolicy handles this mixed-mode scenario -- does the pod get the union of both egress profiles (defeating proxy mode's isolation benefit) or the more restrictive profile?

**Impact:** In mixed-mode scenarios, the security properties of proxy mode could be undermined if the NetworkPolicy allows direct egress for the other provider's pool.

**Recommendation:** Specify the NetworkPolicy resolution for pods holding leases from pools with different delivery modes. Options include: (a) union (documented as a known trade-off), (b) deny mixed-mode assignment at the gateway level, or (c) per-provider egress rules using provider-specific CIDRs.

---


Skipped: CRD-031, CRD-032 (carried forward from previous iteration).

---

## 18. Content Model & Schema (SCH)

### SCH-071. `OutputPart.status` field undeclared in the Canonical Type Registry [Medium]

Section 15.3 (line ~7632) lists `status` as a top-level field on `OutputPart`, but the Canonical Type Registry v1 (line ~7653) does not define `status` among the guaranteed fields for any of the 10 canonical types. The Translation Fidelity Matrix (line ~7779) also omits `status` from the per-field fidelity mapping. This means consumers have no specification for what values `status` can take, which adapter translations preserve it, and whether it is optional or required. Either `status` needs a defined enum of values and inclusion in the registry/matrix, or it should be explicitly documented as an optional envelope-level field outside the per-type registry.

### SCH-072. `MessageEnvelope.from` closed enum missing `delegated` or equivalent for gateway-synthesized delegation messages [Medium]

Section 15.3 (line ~7822) defines `MessageEnvelope.from` as a closed enum: `client | agent | system | external`. However, Section 8 describes delegation flows where the gateway creates child sessions and injects `TaskSpec` input on behalf of a parent agent. These gateway-synthesized messages are not from the `client`, not from the `agent` (the child agent has not started yet), and not from `external` (the parent is internal). Using `system` for this purpose conflates platform-generated messages (heartbeats, lifecycle events) with delegation-injected user content. The spec should either expand the enum or explicitly document which value is used for delegation-injected messages.

### SCH-073. `LennyBlobURI` TTL values inconsistent between definition and `billing_events` retention [Medium]

Section 15.3 (line ~7701) defines three TTL contexts for `LennyBlobURI`: 1h for live, 30d for TaskRecord, and 13mo for audit. However, Section 11.2.1 defines `billing.retentionDays` as 395 days (~13 months) with a floor of 2190 days (~6 years) for HIPAA. If billing events reference blob URIs (e.g., for detailed usage artifacts), the 13mo audit TTL would expire before the HIPAA billing retention floor. The spec does not clarify whether billing event records ever reference blob URIs, and if so, which TTL context applies. This should be explicitly addressed.

### SCH-074. `WorkspacePlan.sources` type enum incomplete — missing `gitClone` or equivalent [Low]

Section 7.4 (line ~6534) defines `WorkspacePlan.sources` with four types: `inlineFile`, `uploadFile`, `uploadArchive`, `mkdir`. Section 7.4 also discusses workspace materialization from uploads. However, Section 8.7 describes file export from parent to child sessions during delegation, and Section 19 (decision 12) describes `POST /v1/sessions/{id}/derive` for deriving sessions from previous workspace snapshots. Neither of these mechanisms maps cleanly to the four declared source types. The `derive` flow presumably creates an implicit `uploadArchive` source, but this is not specified. The file-export mechanism from delegation also lacks a declared source type in the `WorkspacePlan` schema.

### SCH-075. `billing_events` schema `environmentId` field listed in Phase 1 but `Environment` resource not delivered until Phase 15 [Low]

Section 18 (line ~10032, Phase 1) explicitly lists `environmentId` nullable field on the billing event schema as a Phase 1 deliverable. However, the `Environment` resource itself (tag-based selectors, member RBAC) is not delivered until Phase 15. While the field is nullable and can be `null` for Phases 1-14, the spec does not document the contract for populating `environmentId` on billing events once Phase 15 ships -- specifically, whether it is populated retroactively, whether it is derived from the session's environment at creation time or at billing event emission time, and what happens if a session's environment changes mid-session.

### SCH-076. `TaskRecord.messages` array lacks ordering and deduplication contract [Medium]

Section 8 describes `TaskRecord` containing a `messages` array, and Section 15.3 defines `MessageEnvelope` with `id`, `inReplyTo`, and `threadId` fields supporting a DAG conversation model. However, the spec does not define: (a) whether the `messages` array is ordered by insertion time, causal order, or is unordered; (b) whether message IDs must be unique within a TaskRecord; (c) what happens when a reconnecting client replays a message that was already persisted (idempotency contract). For a DAG model, the ordering contract is essential for consumers to correctly reconstruct conversation flow.

### SCH-077. `runtimeOptionsSchema` per-runtime JSON Schema validation timing not specified [Medium]

Section 7.4 (line ~6662) defines per-runtime `runtimeOptionsSchema` examples (e.g., for `claude-code`, `langgraph`, `openai-agents`). These schemas are registered at runtime registration time via the admin API. However, the spec does not specify when `runtimeOptions` provided by session creators are validated against the registered schema -- at session creation time (gateway-side), at workspace materialization time (pod-side), or at runtime startup time (adapter-side). If validation happens late (pod-side), the session has already consumed a warm pool pod and credentials before discovering invalid options. The validation point should be specified.

### SCH-078. `ExternalProtocolAdapter.HandleDiscovery` return type not specified for adapters with no discovery [Medium]

Section 15 (line ~6740) defines the `ExternalProtocolAdapter` Go interface with `HandleDiscovery` as a required method. Section 21 describes A2A discovery endpoints. However, the interface definition does not specify the return type or contract for adapters that have no discovery semantics (e.g., OpenAI Completions adapter). The spec mentions `BaseAdapter` no-op implementations for `OutboundCapabilities` and `OpenOutboundChannel` but does not confirm `HandleDiscovery` has a similar no-op default in `BaseAdapter`. If `HandleDiscovery` is required but some adapters have no discovery model, the base implementation contract must be explicit.

### SCH-079. `SandboxClaim` CRD schema not fully specified — missing `spec` fields [Medium]

Section 4.6 references four CRDs: `SandboxTemplate`, `SandboxWarmPool`, `Sandbox`, and `SandboxClaim`. The `SandboxClaim` is central to session-to-pod binding (Section 4.6.1 discusses optimistic locking and failover fencing for claims). However, while `SandboxTemplate` and `SandboxWarmPool` have their fields enumerated in the spec (Section 17.2 and others), `SandboxClaim` lacks a complete field listing. The spec references `spec.sandboxRef` (in the stuck-finalizer runbook, Section 17.7) and implies `spec.sessionId` and `spec.tenantId` exist, but the full CRD schema (spec fields, status fields, conditions) is never consolidated. For a resource that is central to correctness (double-claim prevention, optimistic locking), the complete schema should be specified.

### SCH-080. `billing_events.sequence_number` uniqueness scope not specified [Medium]

Section 11.2.1 (line ~5197) defines `sequence_number` (uint64) on the billing event schema. The spec describes append-only immutability and hash chaining for audit events (Section 11.7), but for billing events, the uniqueness and ordering contract of `sequence_number` is not defined. Specifically: is `sequence_number` globally unique, per-tenant unique, or per-session unique? Is it monotonically increasing? Is it gap-free? The audit log has explicit hash-chain integrity with `prev_hash`, but billing events have no equivalent integrity mechanism documented, yet they are described as "append-only immutable" with "dual-control correction approval." The `sequence_number` contract must be explicit for billing consumers to detect gaps or duplicates.

### SCH-081. `OutputPart` inline size threshold (64KB) vs `maxInputSize` on `DelegationPolicy` — unit ambiguity [Low]

Section 15.3 (line ~7632) defines OutputPart inline threshold as "<=64KB inline" and ">64KB-50MB blob ref." Section 8.3 defines `maxInputSize` on `DelegationPolicy.contentPolicy` to limit delegation input sizes. The spec does not clarify whether `maxInputSize` measures the serialized JSON size of the `OutputPart[]` array (which for inline parts is the wire size), the sum of individual part content sizes (which for blob-ref parts would require resolving URIs), or some other measure. For parts that mix inline and blob-ref, the measurement semantics differ significantly.

### SCH-082. `EvalResult` schema referenced but never defined [Medium]

Section 10.7 (experiments) references `EvalResult` as part of the experiment evaluation hooks, and the summary mentions an `EvalResult` schema. However, no section of the spec provides the actual `EvalResult` schema definition — its fields, types, required properties, or versioning. Since the experiment system is a Phase 16 deliverable and the spec aims to be comprehensive for v1, this schema should be defined or explicitly deferred with a note about what structure is expected.

### SCH-083. `adapter-manifest.json` schema versioning not specified [Medium]

Section 7.3 defines the adapter manifest (`/run/lenny/adapter-manifest.json`) as a JSON contract between the adapter sidecar and the gateway. The manifest includes fields like capabilities, protocol details, and lifecycle channel configuration. However, unlike `OutputPart` (which has `schemaVersion`) and `TaskRecord` (which has `schemaVersion`), the adapter manifest has no versioning field. When the platform evolves and adds new manifest fields or changes semantics, there is no mechanism for the gateway to determine which manifest version an adapter is providing, making backward-compatible evolution difficult.

### SCH-084. `DelegationLease.allowedExternalEndpoints` schema not defined [Medium]

Section 8.3 and Section 19 (decision 13) reference `allowedExternalEndpoints` as a slot on the delegation lease schema, and Section 21.1 (A2A support) relies on it for controlling which external endpoints a delegated session may contact. However, the spec never defines the schema of this field — whether it is a list of strings (URLs), a list of objects with protocol/host/port, whether wildcards are supported, or how it interacts with `NetworkPolicy` egress rules. The field is described as existing "from v1" but its structure is unspecified.

### SCH-085. Cursor-based pagination `cursor` field type and encoding not specified [Medium]

Section 15.1 describes cursor-based pagination as the standard for all list endpoints, with a response envelope containing pagination metadata. However, the spec does not define the cursor field's type (opaque string? base64-encoded? structured?), maximum length, or whether cursors are stable across deployments (i.e., whether a cursor from one gateway replica works on another). For a platform with multiple gateway replicas behind a load balancer, cursor portability is a practical concern.

### SCH-086. `ExperimentDefinition` resource schema not fully specified [Medium]

Section 10.7 describes the experiment system with bucketing, sticky assignments, variant pools, and the `ExperimentDefinition` resource. However, the full schema of `ExperimentDefinition` — its fields, status conditions, supported states (`active`, `paused`, `concluded`), variant weight format (integer percentages? floating point? must sum to 100?), and the relationship between `ExperimentDefinition` and pool targeting — is not consolidated in one place. The bucketing algorithm (HMAC-SHA256) and sticky assignment cache are well-specified, but the resource schema that drives them is fragmented across the section.

### SCH-087. `billing_events` correction event schema not defined [Medium]

Section 11.2.1 describes billing event immutability with "dual-control correction approval" for billing corrections. This implies a correction event type exists (since append-only events cannot be modified in place, corrections must be new events that reference the original). However, the billing event schema (line ~5197) lists `event_type` as a field but does not enumerate the allowed event types, and no correction-specific event schema (with fields like `corrects_sequence_number`, `correction_reason`, `approved_by`, or similar) is defined. The correction workflow is described procedurally but the schema is missing.

### SCH-088. `data_classification` tier assignment per field not specified for key schemas [Medium]

Section 12.7 (line ~5992) defines four data classification tiers (T1-T4) with a comprehensive controls matrix. However, the spec does not provide a field-level classification mapping for the major schemas (`billing_events`, `audit_log`, `sessions`, `TaskRecord`). For example: is `session_id` T2 or T3? Is `tokens_input` in billing events T2? Is `user_id` T3 (PII) or T4? Without field-level classification, implementers cannot correctly apply the per-tier encryption, access control, retention, and erasure requirements from the controls matrix. The erasure procedure (Section 12.8) operates at the table level but references data classification tiers without mapping specific fields.

### SCH-089. `crossEnvironmentDelegation` structured form schema slot referenced but not defined [Low]

Section 18 Phase 1 (line ~10032) lists `crossEnvironmentDelegation structured form schema slot` as a Phase 1 deliverable. However, the spec does not define this schema — neither the fields of the structured form, the validation rules, nor how it interacts with the `DelegationPolicy` resource. Section 21.6 defers "Cross-environment delegation richer controls" to post-v1, creating ambiguity about what the v1 structured form contains versus what is deferred.

### SCH-090. `Connector` resource `labels` field referenced but schema not consolidated [Low]

Section 18 Phase 1 references `Connector resource with labels` as a deliverable, and Section 5 describes connectors with `connectorSelector` for environment-based filtering. However, the `Connector` resource schema — its full field set including `labels`, protocol, endpoint, authentication configuration, and capability declarations — is not consolidated in any single location. The relationship between `Connector` labels and `Environment.connectorSelector` matching semantics is implied but not formally specified (e.g., is it AND or OR matching for multiple label selectors?).

### SCH-091. `failopen-cumulative.json` file schema not specified [Low]

Section 12.4 (line ~5719) describes a local file `/run/lenny/failopen-cumulative.json` that each gateway replica persists on every fail-open state transition. The file is read on replica restart to resume the cumulative fail-open timer. However, the spec does not define the JSON schema of this file — what fields it contains, how `timestamp` is formatted, whether it includes per-tenant or aggregate counters, or what constitutes a "corrupted" file (triggering the cold-start reset to zero). For a security control that prevents bypass via CrashLoopBackOff, the file format should be specified.

---

---

## 19. Build Sequence (BLD)

### BLD-058. Phase 2 benchmark scope exceeds Phase 2 infrastructure [Medium]

Phase 2 is required to calibrate subsystem extraction thresholds (Section 4.1) and `maxSessionsPerReplica` using the benchmark harness. However, Phase 2 only delivers the echo runtime (Minimum tier), `make run` local dev mode, and the adapter binary protocol. The MCP Fabric subsystem (delegation) is not available until Phase 9-10, and the LLM Proxy subsystem is not available until Phase 5.8. The calibration methodology requires driving all four subsystems through synthetic load levels at 25%-100% of Tier 2 concurrency, but two of the four subsystems do not exist at Phase 2 completion. The spec states these are "Phase 2 exit criteria" but there is no mechanism to calibrate the MCP Fabric or LLM Proxy thresholds at Phase 2. Either the exit criterion should be relaxed to cover only the two available subsystems (Stream Proxy and Upload Handler) with re-calibration at Phase 5.8 and Phase 10, or the calibration methodology should explicitly state which subsystems are deferrable.

### BLD-059. Phase 1.5 migration framework depends on decisions not yet made in Phase 1.5 [Low]

Phase 1.5 specifies selecting a migration tool (e.g., `golang-migrate`, `atlas`, or `goose`) and establishing conventions. However, the expand-contract discipline documented in Section 10.4 includes highly specific guidance about advisory locks, PL/pgSQL DO blocks for Phase 3 gates, and `DROP COLUMN IF EXISTS` patterns. These patterns are tool-specific (e.g., advisory lock behavior is a `golang-migrate` feature). If a different tool is selected (atlas uses a different concurrency model), the Phase 3 gate enforcement specification in Section 10.4 may not apply. The migration tool selection should either be fixed in Section 10.4 (not left open in Phase 1.5) or the Phase 3 gate specification should be made tool-agnostic.

### BLD-060. Phase 3 mTLS PKI has no integration test infrastructure yet [Medium]

Phase 3 requires mTLS PKI setup (cert-manager, ClusterIssuer, SPIFFE-compatible URI SANs, trust bundle distribution) and states "mTLS enforcement on gateway-pod gRPC channel." Phase 3.5 follows with "mTLS end-to-end verification." However, Phase 3 delivers the PoolScalingController and RuntimeUpgrade state machine simultaneously with the mTLS PKI. The PoolScalingController creates pods that must communicate with the gateway over mTLS, meaning the PKI must be fully operational before the PoolScalingController can be tested. There is no phasing within Phase 3 that ensures PKI is established before the PoolScalingController integration tests run. This creates a circular dependency within the phase: the PoolScalingController cannot be tested without mTLS, and mTLS cannot be tested end-to-end until Phase 3.5. The phase should specify that PKI setup is a sub-milestone that must be completed and verified before PoolScalingController integration testing begins.

### BLD-061. Phase 5.8 SPIFFE-binding depends on Phase 3 PKI but spec does not verify SPIFFE URI SAN format [Medium]

Phase 5.8 states the LLM Proxy performs "SPIFFE-binding for lease tokens" by extracting "the peer SPIFFE URI from the mTLS connection (using the SPIFFE-compatible URI SANs issued by Phase 3's cert-manager PKI)." Phase 3 specifies "SPIFFE-compatible URI SANs for agent pod certificates" but does not specify the URI format, the trust domain naming convention, or the SAN template in the cert-manager Certificate resource. Without a defined SPIFFE URI format (e.g., `spiffe://lenny.dev/ns/{namespace}/pod/{podName}`), Phase 5.8 cannot implement the binding verification. The SPIFFE URI format should be specified as part of Phase 3 deliverables or in Section 10.3.

### BLD-062. Phase 2.8 streaming-echo runtime is a gate for Phases 6-8 but its deliverables overlap with Phase 6 [Medium]

Phase 2.8 specifies that the `streaming-echo` runtime must support "simulated streaming output" including `OutputPart` chunk sequences. Phase 6 delivers the "interactive session model (streaming, messages, reconnect with event replay)." The streaming-echo runtime needs to exercise the streaming path, but the streaming path itself is not built until Phase 6. Phase 2.8 states it must be available before Phase 6, but the streaming infrastructure it depends on is a Phase 6 deliverable. Either Phase 2.8 must implement its own minimal streaming plumbing (independent of the Phase 6 interactive model), or the dependency should be documented as "Phase 2.8 streaming-echo works with the Phase 2 echo protocol and is upgraded to use full streaming in Phase 6."

### BLD-063. No explicit phase for Postgres RLS integration test suite [Medium]

Section 4.2 specifies detailed RLS requirements: `SET LOCAL app.current_tenant`, the `__unset__` sentinel via PgBouncer `connect_query`, the `lenny_tenant_guard` trigger, `__all__` sentinel for platform-admin, and `TestRLSPlatformAdminAllSentinel` integration test. These are foundational security guarantees that every subsequent phase depends on. The build sequence does not assign RLS verification to a specific phase. Phase 1 introduces "core types" including `tenant_id`, and Phase 1.5 establishes the migration framework, but neither explicitly requires the RLS integration tests. Phase 4 delivers "session manager + session lifecycle" which presumably needs RLS, but the RLS test suite is never listed as a deliverable. The RLS integration tests should be an explicit Phase 1.5 or Phase 2 deliverable with a CI gate, since every tenant-scoped query from Phase 4 onward depends on correct RLS behavior.

### BLD-064. Phase 4.5 bootstrap seed must pre-configure `noEnvironmentPolicy` but Phase 4.5 description does not mention it [Medium]

The Note after Phase 5 states: "the Phase 4.5 bootstrap seed must address this by either (a) setting `noEnvironmentPolicy: allow-all` on the default tenant, or (b) seeding at least one environment." However, Phase 4.5's own description lists: "runtimes, pools, connectors, delegation policies, tenant management, external adapters registry... Bootstrap seed mechanism: `lenny-ctl bootstrap` CLI command." The `noEnvironmentPolicy` configuration is not mentioned in Phase 4.5's deliverable list. This is a gap between the advisory note and the phase specification that could be missed during implementation.

### BLD-065. Phase 12a/12b/12c parallelism note has inconsistent dependency claims [Medium]

The note before Phases 12a/12b/12c states: "Phase 12a depends only on Phase 5.5 (Token Service)." However, Phase 12a's description says it "builds on Phase 5.5 Basic Token Service" and delivers "application-layer KMS envelope encryption for stored OAuth tokens." KMS integration requires the Token Service to have access to KMS keys, which requires KMS IAM permissions that are not established in any prior phase. The self-managed profile (Section 17.9) does not specify when KMS infrastructure is provisioned. Phase 12a should either list KMS infrastructure provisioning as a sub-deliverable or declare a dependency on a KMS setup step.

### BLD-066. Client SDKs (Phase 6) require OpenAPI spec that is not generated until Phase 5 [Low]

Phase 6 delivers "Client SDKs (Go + TypeScript/JavaScript) generated from the OpenAPI spec and MCP tool schemas." Phase 5 delivers "OpenAPI-to-MCP schema generation step in build pipeline" and the REST/MCP contract tests. The OpenAPI spec itself is never explicitly listed as a phase deliverable -- it is implied to exist by Phase 5's contract tests. The OpenAPI spec should be an explicit deliverable of Phase 4 or Phase 5, with a CI gate that validates it against the implemented REST endpoints, since Phase 6 SDKs depend on it.

### BLD-067. Phase 9 `DelegationPolicy` enforcement gate references policies "registered from Phase 3 onward" but Phase 3 only introduces the resource type [Medium]

Phase 9's description states: "integration tests validating enforcement of `DelegationPolicy` resources registered from Phase 3 onward." Phase 3 introduces the `DelegationPolicy` resource and the `setupPolicy` enforcement, but Phase 3 does not include any REST API for registering `DelegationPolicy` resources (the Admin API foundation is Phase 4.5). Between Phase 3 and Phase 4.5, there is no API surface to register delegation policies except via direct CRD manipulation. The Phase 9 integration tests should clarify whether they depend on CRD-level policy registration (Phase 3) or API-level registration (Phase 4.5), and whether the test setup creates policies through the admin API or through `kubectl apply`.

### BLD-068. Phase 13 full observability stack has no dependency on Phase 2.8 streaming-echo for audit testing [Low]

Phase 13 delivers "complete audit logging (Postgres audit tables with append-only grants, hash-chain integrity, SIEM connectivity)." The audit system must log events from all platform operations including streaming sessions, delegation, and credential operations. Phase 13 should specify that the audit log integration tests use `streaming-echo` and `delegation-echo` test runtimes (from Phases 2.8 and 9) to validate audit coverage without real LLM credentials. This is implied but not stated, creating a risk that audit tests depend on manual or real-LLM testing only.

### BLD-069. No phase for `session_dlq_archive` table implementation [Medium]

Section 10.1 references the dead-letter queue mechanics and Section 4.2 classifies `session_dlq_archive` as a tenant-scoped table. The DLQ is part of the coordinator handoff and split-brain fencing logic. However, no build phase lists the DLQ implementation as a deliverable. The DLQ is critical for the coordinator handoff path (Section 10.1), which is needed from the moment sessions can be served by multiple gateway replicas (Phase 4 onward). The build sequence should assign the DLQ table, the DLQ insertion path, and the DLQ replay mechanism to a specific phase.

### BLD-070. Phase 14 security audit scope is unbounded [Medium]

Phase 14 states: "comprehensive security audit and penetration testing... This phase is the full end-to-end security audit covering the complete platform." The spec does not define the audit scope, methodology, or exit criteria. Unlike the targeted security reviews at Phases 5.6 and 9.1 (which list specific attack surfaces to review), Phase 14 is open-ended. Without defined scope boundaries and exit criteria, Phase 14 could become an indefinite blocker. The phase should specify: (a) minimum security audit scope (e.g., OWASP Top 10 applied to the specific attack surfaces: REST API, MCP protocol, credential storage, delegation chains, pod escape), (b) penetration test methodology (black-box, gray-box, or white-box), and (c) exit criteria (e.g., zero Critical, zero High unresolved findings).

### BLD-071. Phase 15 (Environment resource) builds on Phase 10.6 RBAC model but there is no integration path from Phase 5's `noEnvironmentPolicy` workaround [Low]

Phase 5 activates `noEnvironmentPolicy: allow-all` as a temporary workaround (per the advisory note). Phase 15 delivers the full Environment resource with "tag-based selectors, member RBAC." The transition from the Phase 5 workaround to Phase 15's full implementation requires a migration path: existing deployments using `noEnvironmentPolicy: allow-all` must transition to environment-based RBAC without disrupting running sessions. No migration strategy or data migration is specified for this transition. A runbook or migration script should be listed as a Phase 15 deliverable.

### BLD-072. Phase 16.5 experiment load test depends on Phase 16 but Phase 16 has no load test infrastructure of its own [Low]

Phase 16 delivers "Experiment primitives, PoolScalingController experiment integration" with a milestone of "A/B testing infrastructure." Phase 16.5 immediately follows with a load test. However, Phase 16 itself includes no unit/integration test specification for the experiment primitives. Unlike Phases 9 and 12b/12c which include explicit "integration test gate (prerequisite for merging)" requirements, Phase 16 has no test gate. The experiment bucketing, variant pool sizing, and base pool `minWarm` adjustment could be merged without correctness verification, with Phase 16.5 load tests being the first real validation. Phase 16 should include an integration test gate.

### BLD-073. Build sequence has no phase for the `lenny-ctl` CLI tool itself [Medium]

Section 24 documents a comprehensive `lenny-ctl` command reference (40+ commands covering session management, admin operations, bootstrap, migration, debugging). Multiple phases reference `lenny-ctl` subcommands (Phase 3 references `lenny-ctl admin pools upgrade`, Phase 4.5 references `lenny-ctl bootstrap`, Phase 17.7 runbooks use `lenny-ctl` extensively). However, the build sequence never assigns `lenny-ctl` as a deliverable to any phase. Phase 4.5 mentions the bootstrap seed mechanism as a deliverable, but the CLI binary itself -- including the argument parsing framework, authentication, and server connection logic -- is not phased. The CLI should be introduced as a Phase 2 or Phase 4.5 deliverable, with incremental subcommand additions in subsequent phases.

### BLD-074. Expand-contract migration Phase 3 enforcement gate may deadlock under active sessions [Medium]

Section 10.4 specifies that Phase 3 migrations use a PL/pgSQL `DO` block to count un-migrated rows and abort if any remain. The check runs inside the same transaction as the DDL. However, the minimum inter-phase wait before Phase 3 is documented as `max(maxSessionAge, longest_record_TTL)`. In practice, sessions can be extended (lease extensions, Section 8.6) beyond `maxSessionAge`. A session that receives repeated lease extensions could hold old-schema rows indefinitely, preventing Phase 3 from ever succeeding. The Phase 3 gate should document what happens when sessions with lease extensions prevent the old-schema row count from reaching zero, and whether there is a forced migration path (e.g., marking extended sessions for forced schema migration during checkpoint).

### BLD-075. Phase 5.4 etcd encryption verification assumes etcdctl access [Low]

Phase 5.4 requires: "CI gate verifying that a test Secret written to the cluster is stored encrypted in etcd (confirmed via `etcdctl get` on the raw key)." In managed Kubernetes environments (EKS, GKE, AKS), `etcdctl` access is not available -- the provider manages etcd and does not expose it. The cloud-managed deployment profile (Section 17.9) relies on provider-managed encryption, but Phase 5.4's CI gate is only scoped to "self-managed clusters only." The spec should clarify that the CI gate applies only to self-managed clusters and specify an alternative verification mechanism for cloud-managed clusters (e.g., provider API confirmation that encryption is enabled).

### BLD-076. Phase 17b (memory, semantic caching, guardrails, eval hooks) has no dependencies listed [Low]

Phase 17b says it "can proceed in parallel with or after Phase 17a." However, memory tools (`lenny/memory_write`, `lenny/memory_query`) require the `MemoryStore` interface (Section 9.4) and semantic search capability. Semantic search implies a vector store or embedding service, neither of which is specified in any prior phase or in the deployment profiles (Section 17.9). Similarly, "semantic caching" requires an embedding pipeline. Phase 17b should list its infrastructure dependencies (vector database, embedding model/service) either as sub-deliverables or as prerequisites.

### BLD-077. No phase for `OutboundChannel` contract implementation [Medium]

Section 15 specifies the `OutboundChannel` contract for push notifications with `buffered-drop` and `bounded-error` back-pressure policies. The `BaseAdapter` provides no-op implementations, and `A2AAdapter` (post-v1) implements the full contract. However, the `OutboundChannel` interface and `BaseAdapter` implementation must exist from Phase 5 onward (when adapters are registered), and webhook delivery for session events (Section 14 references `session.awaiting_action` webhook) is used from Phase 4 onward. The build sequence never assigns the webhook/push notification infrastructure to a specific phase. This should be a Phase 4 or Phase 5 deliverable.

### BLD-078. Phase 2 `make run` SQLite schema generation may be blocked by RLS-dependent migration files [Medium]

Phase 2 specifies: "the `make run` SQLite schema is generated at build time from the Postgres migration files by stripping Postgres-specific features (RLS policies, `SET LOCAL`, PL/pgSQL triggers)." Phase 1.5 migration files must include RLS policies (since Section 4.2 specifies that "every tenant-scoped table has an RLS policy"). The stripping logic must handle: `CREATE POLICY` statements, `ALTER TABLE ... ENABLE ROW LEVEL SECURITY`, PL/pgSQL trigger functions, `SET LOCAL` commands in trigger bodies, and `current_setting()` function calls. This is a non-trivial SQL parsing/transformation task. The spec does not scope the complexity of the stripping logic or specify what happens when a migration file uses Postgres-specific SQL that the stripper cannot handle (e.g., a complex `DO $$ ... $$` block). A fallback strategy should be specified (e.g., manual SQLite migration overrides for files that cannot be auto-stripped).

### BLD-079. Phase 13.5 Postgres write-pattern benchmark (SCL-040) references Section 12.3 ceiling table but no ceiling table exists in Section 12.3 [Medium]

Phase 13.5 states: "compare measured sustained and burst IOPS against the Section 12.3 ceiling table and update `postgres.writeCeilingIops` in the Helm defaults if the empirical ceiling differs by more than 10% from the estimate." Section 12.3 covers Postgres HA, connection pooling, and RLS -- but there is no "ceiling table" with IOPS estimates in Section 12.3. Either the cross-reference is wrong (it may refer to Section 17.8's capacity planning), or the IOPS ceiling table is missing from the spec. This needs to be resolved before Phase 13.5 can be executed.

### BLD-080. No phase assigns the `lenny_tenant_guard` trigger implementation [Medium]

Section 12.3 specifies a per-transaction tenant validation trigger (`lenny_tenant_guard`) that serves as the second layer of RLS defense, particularly critical for cloud-managed poolers that do not support `connect_query`. The trigger is referenced in the Profile-Invariant Requirements (Section 17.9) and in the `platform-admin` `__all__` sentinel logic (Section 4.2). No build phase explicitly assigns implementation of this trigger. It should be part of Phase 1.5 (migration framework) or Phase 2 (where `make run` dev mode exercises database access), since it is a foundational security control that all subsequent phases depend on.

### BLD-081. Build sequence assumes linear team but describes parallelizable phases only at 12a/12b/12c [Low]

The build sequence note on Phase 12a/12b/12c parallelism is the only explicit mention of parallel execution. However, several other phases have no inter-dependencies and could be parallelized (e.g., Phase 6.5 load test and Phase 7 policy engine, or Phase 13 observability and Phase 14 security hardening). The build sequence does not explicitly mark which phases are sequential gates versus parallelizable work streams. For an AI-agent-driven build (as stated in the Section 18 preamble), explicit parallelism annotations on all phases would reduce total build time significantly. This is a structural gap in the build plan.

---

## 20. Failure Modes & Resilience (FLR)

### FLR-063. `session.lost` event not listed in webhook event types [Medium]

Section 4.4 (total-loss path) specifies that a `session.lost` event is emitted on the session's event stream with `reason: "eviction_total_loss"`. However, Section 14 (callbackUrl webhook delivery) enumerates the exhaustive webhook event types as: `session.completed`, `session.failed`, `session.terminated`, `session.cancelled`, `session.expired`, `session.awaiting_action`, and `delegation.completed`. The `session.lost` event type is absent from this list. If `session.lost` is a distinct event type (as opposed to `session.failed` with a specific reason code), it needs to be included in the webhook event type catalog with its own per-event `data` schema. Otherwise, clients relying on webhook delivery for CI/CD pipelines will never receive notification of a total-loss eviction -- they must poll session status to discover it. The spec should either add `session.lost` to the webhook event type catalog with its data schema, or explicitly state that the total-loss path transitions the session to `failed` and the webhook fires as `session.failed` with the eviction reason.

### FLR-064. Callback worker described as separate "pods" but gateway is a single Deployment [Medium]

Section 14 (callbackUrl SSRF mitigations, item 3) states: "At minimum, a `NetworkPolicy` blocks the callback worker pods from reaching cluster-internal CIDRs." This implies callback workers run in separate pods with their own NetworkPolicy. However, the gateway is a single Deployment (Section 17.1), and callback delivery is described as a "dedicated goroutine pool" within the gateway process. The gateway's `lenny-system` NetworkPolicy (Section 13.2) allows broad external HTTPS egress (TCP 443 to `0.0.0.0/0`) for LLM proxy forwarding and connector callbacks. Since callback workers are goroutines in the same process, they share the gateway's broad egress rule and cannot have a separate, more restrictive NetworkPolicy. The claim that a NetworkPolicy "blocks the callback worker pods from reaching cluster-internal CIDRs" is incoherent with the shared-process architecture. The spec should either clarify that the existing gateway `except` clauses on the `0.0.0.0/0` CIDR (which already exclude cluster pod/service CIDRs and IMDS) are the callback worker's protection, or define a concrete isolation mechanism (e.g., a separate Deployment for callback delivery).

### FLR-065. Webhook retry exhaustion leaves no durable notification for total-loss sessions [Medium]

Section 14 specifies that failed webhook deliveries are retried 5 times with exponential backoff, after which the event is "marked as undelivered and queryable via `GET /v1/sessions/{id}/webhook-events`". For a total-loss eviction scenario (Section 4.4) where both MinIO and Postgres may be degraded, the webhook callback may also fail (since the gateway itself may be under infrastructure pressure). If the webhook retries are exhausted and the session entered a total-loss state, the only way for a CI/CD client to discover this is to poll `GET /v1/sessions/{id}` or check `GET /v1/sessions/{id}/webhook-events`. The spec does not prescribe any escalated notification mechanism for undelivered webhooks in critical failure scenarios. While this is not a specification error per se, the total-loss path is one of the few scenarios where the client genuinely cannot rely on normal observability -- adding a recommendation for operators to monitor undelivered webhook events (e.g., a metric or alert) would close the gap.

### FLR-066. Checkpoint barrier ack timeout metric name inconsistency [Low]

Section 16.1 defines the metric `lenny_checkpoint_barrier_ack_total` (counter labeled by `pool`, `outcome`: `success`, `timeout`, `error`) and notes that "the `timeout` outcome corresponds to the existing `lenny_checkpoint_barrier_ack_timeout_total` inline metric in Section 10.1." This means the same event is tracked by two different metric names: `lenny_checkpoint_barrier_ack_total{outcome="timeout"}` and `lenny_checkpoint_barrier_ack_timeout_total`. The spec should clarify whether both metrics are emitted (which creates confusion for dashboard authors) or whether `lenny_checkpoint_barrier_ack_timeout_total` is superseded by the labeled counter. If the latter, the inline reference in Section 10.1 should be updated to reference the consolidated metric.

### FLR-067. Coordinator handoff timeout has no explicit default or alert [Medium]

Section 10.1 describes the 3-step coordinator handoff protocol with a `lenny_coordinator_handoff_duration_seconds` histogram and the `CoordinatorHandoffSlow` alert (P95 > 5s over 5 min). However, the spec does not define what happens when a handoff exceeds any absolute timeout. The alert fires at 5s P95 sustained, but a single handoff could block indefinitely if the old coordinator never responds to the fencing request. The spec mentions `lenny_coordinator_fence_relinquished_total` (counter for when a coordinator gives up after failing all fence retries), but the maximum number of retries and the per-retry timeout are not specified. Without these, an operator cannot reason about the worst-case handoff duration or determine when a handoff has entered an irrecoverable state. The spec should define `maxFenceRetries` and `fenceRetryTimeoutSeconds` with explicit defaults.

### FLR-068. Gateway graceful shutdown has no session-handoff specification [Medium]

Section 10.1 describes coordinator handoff for session ownership, and Section 17.1 references gateway HPA and PDB. However, the spec does not define a graceful shutdown protocol for the gateway itself. When a gateway replica is terminated (scale-down, rolling upgrade, pod eviction), what happens to in-flight sessions coordinated by that replica? The coordinator handoff protocol (Section 10.1) describes how sessions migrate between replicas, but the trigger for initiating handoff during a planned gateway shutdown is not specified. The `preStop` hook behavior for gateway pods is not defined (unlike agent pods, which have a detailed preStop checkpoint flow in Section 4.4). This gap means sessions coordinated by a draining gateway replica could experience an uncoordinated ownership transfer (relying on lease expiry rather than a proactive handoff), increasing session disruption time.

### FLR-069. Redis Lua script contention cross-tenant impact underspecified for quota operations [Medium]

Section 8.3 thoroughly analyzes cross-tenant aggregate Lua contention for delegation budget `budget_reserve` and `budget_return` scripts, including a blocking formula and the `DelegationLuaScriptLatencyHigh` alert. However, Section 11.2 describes quota enforcement using Redis counters with atomic operations but does not perform a similar contention analysis. At Tier 3 with 500 active tenants and 200 sessions/s creation rate, quota check-and-increment operations on Redis could exhibit similar serialization pressure -- particularly if they use Lua scripts for atomic check-and-increment (the spec does not clarify whether quota operations use `INCRBY` + `GET` or a Lua script). If quota operations are Lua-scripted, the same cross-tenant contention analysis and dedicated-store recommendation from Section 8.3 should be applied to quota operations. If they use simple atomic commands, this should be stated explicitly.

### FLR-070. `created` state timeout races with workspace upload for large files [Medium]

Section 15.1 defines that the `created` state has a TTL of `maxCreatedStateTimeoutSeconds` (default 300s), after which the session transitions to `expired`. The session remains in `created` until the client calls `POST /v1/sessions/{id}/finalize`. However, for large workspace uploads (e.g., multiple files totaling hundreds of MB), the upload phase could approach or exceed the 300s default. The spec notes the upload token's TTL is tied to `session_creation_time + maxCreatedStateTimeoutSeconds`, but does not specify whether the `created` state timeout is reset or extended by each upload call. If a client uploads file 1 at t=0, file 2 at t=200s, and file 3 at t=280s, does the session expire at t=300 even though uploads are actively in progress? The spec should clarify whether active upload traffic extends the `created` state timeout or whether the client must complete all uploads within the fixed window.

### FLR-071. Billing write-ahead buffer exhaustion causes work rejection but no alert threshold for near-rejection [Medium]

Section 12.3 states that when the in-memory billing write-ahead buffer is exhausted, "new billable work is rejected." Section 16.5 defines `BillingWriteAheadBufferHigh` as a Warning alert when utilization exceeds 0.80. However, the gap between 80% warning and 100% rejection is narrow -- at Tier 3 with high billing event rates, the buffer could fill from 80% to 100% in seconds. The spec does not define what "new billable work is rejected" means operationally: does the gateway return 503 to session creation? Does it fail the billing event and continue the session (potentially losing billing data)? Does it queue overflow events somewhere? The rejection behavior and its client-visible error code should be specified, and the alert threshold gap should be analyzed to ensure operators have sufficient reaction time.

### FLR-072. SIEM delivery guard partition hold has no upper bound [Medium]

Section 16.4 states the audit partition GC "MUST NOT drop an audit partition whose most recent event has a `sequence_number` greater than the SIEM forwarder's last acknowledged high-water mark." The held partition is dropped only when the forwarder catches up or an operator explicitly forces it. If the SIEM forwarder is permanently down (e.g., SIEM service decommissioned, endpoint misconfigured), partitions accumulate indefinitely with no automatic resolution. Over weeks or months, this could consume significant Postgres storage. The spec should define either a maximum hold duration (after which the partition is dropped with an alert) or require the `AuditPartitionDropBlocked` alert to escalate to Critical after a deployer-configurable period.

### FLR-073. Orphan task per-tenant cap `cancel_all` fallback is not explained for in-flight tasks [Medium]

Section 8.10 states that when `maxOrphanTasksPerTenant` is reached, the gateway "falls back to `cancel_all` instead of detaching." The `OrphanTasksPerTenantHigh` alert fires at 80% of the cap. However, the spec does not specify what `cancel_all` means for tasks that are actively executing (in `running` state). Does the gateway send a cancel signal and wait for graceful shutdown? Does it force-terminate immediately? What happens to in-progress work (checkpoints, partial results)? For a scenario where a legitimate orchestrator experiences a transient failure and reconnects, the `cancel_all` fallback could destroy recoverable work. The spec should clarify the cancellation semantics and whether `cancel_all` can be reversed within a grace window.

### FLR-074. MCP deprecated-version session drain has no automated mechanism [Low]

Section 15.2 describes the MCP version deprecation lifecycle: after the 6-month window, version support is removed from new connections, but existing sessions continue. The `lenny-preflight` Job emits a warning if deprecated-version sessions are active, and "Operators must drain these sessions (via graceful terminate + resume on the new version) before the deployment." However, there is no automated drain mechanism, no operator runbook for this scenario, and no `lenny-ctl` command to list sessions by MCP version. The operator must manually identify and terminate each deprecated-version session. For Tier 3 deployments with thousands of sessions, this could be operationally impractical. The spec should define either a `lenny-ctl` command to filter sessions by negotiated MCP version or an automated pre-deploy drain for deprecated-version sessions.

### FLR-075. Credential proactive renewal metric defined but no exhaustion alert escalation [Low]

Section 16.5 defines `CredentialProactiveRenewalExhausted` as a Warning alert when proactive renewal retries are exhausted. The session then falls through to the standard credential fallback flow. However, if proactive renewal is systematically failing across multiple sessions (e.g., the credential provider is experiencing intermittent outages), the fallback flow will be heavily loaded. There is no escalation path from `CredentialProactiveRenewalExhausted` (Warning) to a Critical alert when the failure rate indicates a systemic issue. The `CredentialPoolLow` alert (Warning at <20% available) and `CredentialPoolExhausted` (Critical at 0%) cover pool depletion but not the scenario where credentials exist but cannot be renewed. A rate-based escalation (e.g., proactive renewal exhaustion rate > N/min for > 5min) would provide earlier detection.

### FLR-076. `DERIVE_SNAPSHOT_UNAVAILABLE` recovery path is unclear [Low]

Section 15.1 error catalog defines `DERIVE_SNAPSHOT_UNAVAILABLE` (503) with the note "Retrying immediately is unlikely to help; the caller should wait and retry or derive from a different source state." However, if the snapshot was deleted by a GC bug (as the error description suggests), waiting and retrying will never succeed -- the data is permanently lost. The guidance to "wait and retry" is misleading for the GC-bug scenario. The spec should distinguish between transient unavailability (storage backend temporarily unreachable) and permanent loss (object deleted), and provide different guidance for each. For permanent loss, the error response should include a field indicating whether the snapshot reference was found-but-unreachable versus not-found-at-all.

### FLR-077. Billing Redis stream MAXLEN undersized for Tier 1/2 extended Postgres outages [Low]

Section 17.8 sets `billingRedisStreamMaxLen` to 50,000 for Tier 1/2, with fill times of ~2.3 hours (Tier 1) and ~14 minutes (Tier 2). The `billingStreamTTLSeconds` default is 3600s (1 hour). At Tier 2, the stream fills in ~14 minutes, well within the 1-hour TTL. However, if the Postgres outage extends beyond 14 minutes at Tier 2 sustained billing rate, new billing events will begin evicting old events from the stream (MAXLEN is a hard cap). The evicted events are permanently lost since they were not flushed to Postgres. The `BillingStreamBackpressure` alert fires at 80% of MAXLEN, giving only ~2.8 minutes of warning at Tier 2 before events start being lost. While the alert exists, the reaction time is very short. The spec should analyze whether 50,000 is adequate for Tier 2 Postgres failover scenarios (RTO < 30s per Section 17.3, which is well within the 14-minute fill time -- so this is only a concern for extended outages beyond RTO).

### FLR-078. `processing_restricted` flag blocks session creation but not message delivery to existing sessions [Low]

Section 12.8 and the error catalog define `ERASURE_IN_PROGRESS` (403) which blocks session creation when the target `user_id` has a pending GDPR erasure job. However, the spec does not address whether inter-session messages (`lenny/send_message`) to existing sessions owned by the restricted user are also blocked. If messages continue flowing to a running session owned by a user under erasure processing, new data may be generated and persisted (transcripts, billing events) that the erasure job will need to clean up retroactively. The spec should clarify whether `processing_restricted` also blocks inbound messages to the user's active sessions, or whether it only prevents new session creation.

---

## 21. Experimentation (EXP)

### EXP-055. Derive session (`POST /v1/sessions/{id}/derive`) does not specify experiment context handling [Medium]

Section 7.1 (derive session semantics, lines 2776-2789) specifies four aspects of derive behavior: allowed source states, concurrent serialization, credential lease handling, and connector state. However, there is no mention of how the derived session's `experimentContext` is handled. The session creation flow (Section 7.1, step 3a) runs the `ExperimentRouter` for new sessions, and delegation (Section 8.2, step 2b) specifies propagation modes (`inherit`, `control`, `independent`). But `derive` is neither a new session nor a delegation -- it is a third creation path with its own semantics.

The spec does not answer: Does a derived session inherit the source session's `experimentContext`? Is the `ExperimentRouter` evaluated independently for the derived session? Is derivation excluded from experiment assignment entirely? This is a genuine gap because a derived session could pollute experiment results if its `experimentContext` is silently inherited from the source without any documented rule.

### EXP-056. No specification for variant weight changes on active experiments [Medium]

The spec documents that variant weights can be modified via `PUT /v1/admin/experiments/{name}` (line 7145), and the ordering sensitivity note (line 4852) warns that "deployers who add a new variant to an experiment mid-flight will shift bucket boundaries." The PoolScalingController recalculates pool sizing "whenever any variant weight changes" (line 596). However, the spec does not define the complete semantics of a weight change on an active experiment:

1. Are `sticky: user` cached assignments invalidated when weights change? The sticky cache invalidation (line 5089) only triggers on `paused` or `concluded` transitions -- not on weight changes. A user whose sticky cache places them in a variant at weight 0.10 will remain in that variant even if the weight is reduced to 0.01, which is correct by design (stability). But if a variant's weight is *increased*, new users get the updated probability while cached users retain the old assignment -- this is fine, but the spec does not acknowledge this asymmetry or document whether operators should understand it.

2. No validation rule prevents reducing a variant's weight to 0.0 on an active experiment (which would effectively remove the variant from assignment while keeping its pool active). The `Σ variant_weights` clamping (line 596) only validates `>= 1`, not individual variant floors.

However, the deterministic hash makes weight changes safe for non-cached users (the bucket boundary shifts, but the hash is re-evaluated). The missing piece is primarily documentation of the interaction between weight changes and sticky caches. This is a spec gap, not a correctness bug.

### EXP-057. Multi-experiment first-match rule creates enrollment bias toward older experiments [Medium]

The first-match rule (line 4854) evaluates experiments in ascending `created_at` order, enrolling a session in the first experiment that returns a non-control variant. This creates a systematic enrollment bias: older experiments consume their variant allocation first, and newer experiments only see sessions that were assigned to control in all older experiments.

Consider: Experiment A (weight 0.10, created Jan 1) and Experiment B (weight 0.20, created Feb 1). Experiment A captures 10% of traffic unconditionally. Experiment B sees the remaining 90% and captures 20% of *those* -- but only 18% of total traffic (not 20%). More critically, Experiment B's treatment group is drawn exclusively from users who hashed to control in Experiment A, introducing a selection bias if experiment A's hash-derived cohort correlates with user behavior.

The spec does acknowledge that experiments are evaluated independently (line 4853: "the `experiment_id` is part of the HMAC key, so the same user does not land in the same relative bucket across different experiments"), which mitigates hash correlation. And the `experiment.multi_eligible_skipped` event (line 4941) provides observability. However, the effective enrollment rate for later experiments is lower than their configured weight when earlier experiments are active, and this is not documented. Operators may be surprised that a 20% variant only captures 18% of traffic.

This is a design-level awareness gap rather than a bug -- the math is correct, but the effective-vs-configured weight discrepancy is not called out.

### EXP-058. No experiment assignment counter metric in the metrics inventory [Medium]

The metrics inventory (lines 8637-8644) defines experiment-related metrics for targeting webhook latency, webhook failures, circuit breaker state, sticky cache invalidations, session errors by variant, sessions total by variant, and eval scores by variant. However, there is no explicit `lenny_experiment_assignment_total` counter (or equivalent) labeled by `experiment_id`, `variant_id`, and `assignment_source` (hash vs. webhook vs. inherited).

The `lenny_session_total` metric (line 8643) is labeled by `variant_id` but not `experiment_id`. When multiple experiments are active for the same tenant, operators cannot distinguish assignment rates per experiment from this metric alone. They must use the Results API (`sample_count` per variant), which is an aggregation endpoint and not suited for real-time alerting on assignment rates.

This is a genuine observability gap: operators monitoring a ramp-up (increasing variant weight from 1% to 5% to 10%) have no real-time metric to confirm the assignment rate matches the configured weight. The rollback trigger table (line 5117) references variant-level metrics but assumes `variant_id` is sufficient for disambiguation; with multiple concurrent experiments, `variant_id: "treatment"` could appear in multiple experiments.

### EXP-059. `inherited` flag semantics are ambiguous for `control` propagation mode [Medium]

The `experimentContext.inherited` field (line 4971) is documented as "`true` when the context was propagated from a parent (`inherit` or `control` mode)." However, under `control` mode, the child session is "forced into the base runtime (control group) regardless of the parent's variant" (line 4950). The child receives `variantId: "control"` and `inherited: true`.

This creates an ambiguity in eval result analysis: an `EvalResult` with `variant_id: "control"` and `inherited: true` could mean either (a) the parent was in the treatment group and the child was forced to control via `control` mode, or (b) the parent was in the control group and the child inherited that control assignment via `inherit` mode. These two scenarios have fundamentally different analytical meaning -- case (a) is the sample contamination risk documented in line 4957, while case (b) is clean data.

The `inherited` field alone cannot distinguish these cases. The `EvalResult` schema (line 4976) does not store the parent's `variant_id`, the propagation mode, or the parent's `experiment_id`. An analyst who filters by `inherited: false` (as recommended in line 4957) gets uncontaminated data, but an analyst trying to understand the contaminated set cannot determine which control-group results came from treatment-group parents.

### EXP-060. Results API `sample_count` is inconsistent with scorer-level `count` [Low]

The Results API response (lines 5024-5078) includes both a top-level `sample_count` per variant (e.g., 412 for control) and per-scorer `count` fields (e.g., `llm-judge.count: 412`, `exact-match.count: 390`). The `sample_count` and the scorer counts can differ (412 vs. 390 in the example), which is expected since not every session may be scored by every scorer.

However, the spec does not define what `sample_count` represents. Is it the number of unique sessions enrolled in the variant, the number of eval submissions, or the maximum `count` across all scorers? In the example, `sample_count: 412` matches `llm-judge.count: 412` for control, and `sample_count: 45` matches `llm-judge.count: 45` for treatment -- suggesting `sample_count` is the total number of sessions. But this is inferred, not specified. If `sample_count` is total enrolled sessions, it should be documented as such and should not depend on eval submissions (a session enrolled in a variant but never scored should still be counted).

### EXP-061. No TTL or retention policy for experiment sticky assignment cache entries [Low]

The sticky assignment cache uses Redis keys `t:{tenant_id}:exp:{experiment_id}:sticky:*` (line 5089). Cache invalidation occurs on `paused` or `concluded` transitions (bulk `DEL`). However, the spec does not define a TTL for individual sticky cache entries.

For long-running experiments (weeks or months), the cache accumulates one entry per `(user_id, experiment_id)` pair indefinitely. The deterministic hash means re-derivation on cache miss is correct and cheap (line 5707), so the cache is purely a performance optimization. However, without a TTL, operators must rely solely on experiment status transitions to reclaim Redis memory. If an experiment is left in `active` status indefinitely (e.g., a permanent A/B traffic split used as a feature flag), the sticky cache grows unbounded.

This is a low-severity operational concern. A reasonable TTL (e.g., 30 days) with lazy refresh on cache hit would bound memory usage without affecting correctness.

### EXP-062. No specification for `sticky: session` cache behavior [Low]

The sticky assignment cache section (line 5089) describes `sticky: user` caching in detail: Redis-keyed by `(user_id, experiment_id)`, invalidated on status transitions. The `sticky: session` mode uses `session_id` as the `assignment_key` (line 4819), which means each session gets its own deterministic hash.

Since each session has a unique `session_id`, the assignment is computed exactly once per session (at creation time) and never needs to be re-evaluated. There is no need for a cache entry because the result is stored in `experimentContext` on the session record. However, the spec does not explicitly state that `sticky: session` does not use the Redis cache, or confirm that the session record's `experimentContext` is the authoritative source after initial assignment. This is a minor completeness gap.

### EXP-063. `control` propagation mode -- eval attribution contradicts blinding goals [Medium]

Under `control` propagation mode (line 4950): "Child session is forced into the base runtime (control group) regardless of the parent's variant. Eval results still attribute to the parent's experiment." Combined with the Results API blinding (line 5083), which "deliberately omits information that could reveal experiment assignment ordering or enrollment rates beyond what the `sample_count` fields disclose."

However, the `control` propagation mode creates an information leak path: if child eval results are attributed to the parent's experiment with `variant_id: "control"` and `inherited: true`, an analyst with database access can identify which "control" results came from forced-control children (via `delegation_depth > 0` + `inherited: true`). By observing that these forced-control children exist only when a treatment-group parent spawned them, the analyst can reverse-engineer treatment-group enrollment patterns -- specifically, whether treatment-group sessions generate more or fewer delegations, and when. This undermines the blinding goals stated in line 5083.

The sample contamination warning (line 4957) acknowledges the analytical concern but does not address it as a blinding violation. If the Results API is meant to preserve blinding, the existence of `control`-mode inherited results at depth > 0 is itself an information leak about the parent's assignment.

### EXP-064. No experiment-scoped session count in the Results API [Medium]

The Results API response (lines 5024-5078) provides `sample_count` per variant, which counts sessions with eval submissions. However, there is no field for total enrolled sessions per variant (including sessions that were enrolled but never had an eval submission). This distinction matters for:

1. **Power analysis**: Understanding if the experiment has enrolled enough sessions to reach statistical significance requires knowing total enrollment, not just scored sessions.
2. **Scorer coverage monitoring**: If `sample_count` is 45 for treatment but the variant enrolled 200 sessions, only 22.5% of sessions were scored -- indicating a scorer pipeline problem.
3. **Results API description inconsistency**: The admin API table (line 7148) says the results endpoint returns "per-variant session counts, token usage, and any custom metric aggregates." Token usage is not present in the Results API response schema (line 5026-5078). This is a documentation inconsistency between the API table description and the actual response schema.

### EXP-065. Variant pool `maxWarm` is unbounded during active experiments [Low]

The variant pool formula (line 579) computes `target_minWarm` but the spec does not define how `maxWarm` is set for variant pools. For base pools, the PoolScalingController manages `maxWarm` based on scaling policy. For experiment variant pools, the only `maxWarm` behavior documented is during transitions: `active -> paused` leaves `maxWarm` unchanged (line 5095), and `concluded` sets `maxWarm` to 0 (line 5097).

During normal `active` operation, is the variant pool's `maxWarm` inherited from the base pool? Computed independently? Set to a deployer-specified value? Without this specification, the controller behavior for variant pool scale-up ceiling is undefined. A variant pool with no `maxWarm` constraint could scale beyond what the experiment's traffic fraction warrants if there is a burst in base pool demand.

### EXP-066. No specification for experiment creation audit event [Low]

The spec documents the `experiment.status_changed` audit event (line 5087) for status transitions, and the `experiment.targeting_webhook_failed` warning event (line 4926) for webhook failures. However, there is no audit event for experiment creation itself (`POST /v1/admin/experiments`). Given that experiment creation directly affects traffic routing (the `ExperimentRouter` begins evaluating the new experiment immediately if `status: active`), the absence of a creation-specific audit event is a gap. An experiment created directly with `status: active` would start routing traffic without a corresponding creation audit trail -- only subsequent status *changes* are logged.

The general admin API audit logging may cover this implicitly if all admin write operations emit audit events, but the spec does not confirm this general rule, and the explicit enumeration of experiment-specific audit events (status_changed, targeting_webhook_failed, multi_eligible_skipped, isolation_mismatch) suggests these are the exhaustive set.

### EXP-067. Experiment definition lacks a `maxVariants` validation bound [Low]

The spec states "the number of variants per experiment is bounded by operator configuration (typically 2-5)" (line 5024) when justifying why the Results API is not paginated. However, no explicit `maxVariants` configuration field or validation rule is defined. The experiment dry-run semantics (line 7339) validate "variant weight constraint" and "runtime/pool references" but do not mention a maximum variant count.

Without an enforced bound, a deployer could theoretically create an experiment with hundreds of variants, which would: (a) create hundreds of variant pools (each requiring warm pods), (b) produce a large Results API response (contradicting the "inherently bounded" claim), and (c) make the bucketing algorithm walk a very long variant list on every session creation.

This is low severity because practical deployments are unlikely to create extreme variant counts, but the spec's own claim that the response is "inherently bounded" relies on an informal "typically 2-5" rather than a formal validation.

### EXP-068. `submitted_after_conclusion` flag race condition with concurrent status transition [Low]

The `submitted_after_conclusion` boolean on `EvalResult` (line 4988) is "set to `true` when the eval was submitted after the experiment transitioned to `concluded` status." The gateway checks the experiment's current status at eval submission time. However, if an experiment is transitioning from `active` to `concluded` concurrently with an eval submission, the flag's value depends on the read order: the eval submission may read the experiment status before the transition commits, seeing `active` and setting `submitted_after_conclusion: false`, even though the experiment was concluded milliseconds later.

This is inherent to any timestamp-based check and the impact is minimal (a few eval results near the transition boundary may be misclassified). The spec acknowledges this implicitly by providing the flag for filtering rather than hard-blocking post-conclusion submissions. Low severity because the boundary condition affects at most a handful of records during a brief transition window.

---

## 22. Document Quality (DOC)

### DOC-059. Stale `AgentPool` CRD reference in adapter nonce-only fallback section [Medium]

**Location:** Line 889

The text reads: "pool controllers surface as a pool-level `SecurityDegradedMode=True` condition on the `AgentPool` CRD." However, the CRD mapping table at line 433 explicitly states that `AgentPool` has been replaced by `SandboxTemplate` in the `agent-sandbox` CRD set. The document never defines an `AgentPool` CRD -- the four agent-sandbox CRDs are `SandboxTemplate`, `SandboxWarmPool`, `Sandbox`, and `SandboxClaim`. This should reference `SandboxTemplate` or `SandboxWarmPool` depending on where pool-level conditions are surfaced.

---

### DOC-060. Dangling parenthetical between session creation steps 8 and 9 [Low]

**Location:** Line 2722

The line `(executionMode, isolationProfile, scrubPolicy summary)` appears to be a parenthetical annotation that was intended to belong to step 8 (line 2719: "Return session_id + upload token + sessionIsolationLevel") but sits orphaned between the atomicity discussion (lines 2721) and step 9 (line 2724). It is unclear whether this indicates additional fields returned in step 8 or is a leftover editing artifact. Either integrate it into step 8's return value list or remove it.

---

### DOC-061. Empty code block between Sections 7.1 and 7.2 [Low]

**Location:** Lines 2793-2795

There is an empty fenced code block (opening and closing triple backticks with no content) between the end of Section 7.1's "Seal-and-export invariant" paragraph and Section 7.2's heading. This appears to be a formatting artifact from editing.

---

### DOC-062. Empty code block between Standard-tier and Full-tier pseudocode [Low]

**Location:** Lines 8344-8346

Between the end of the Standard-tier pseudocode block (line 8343: `exit(0)` followed by closing backticks at line 8344) and the Full-tier pseudocode block (line 8346 opening backticks, line 8347: `Pseudocode (Full-tier addition -- lifecycle channel):`), there is an empty code block. The closing backticks at 8344 close the Standard-tier block, then lines 8346-8347 open a new block. But the structure ```` ``` \n ``` ```` at 8344-8346 creates a visually confusing empty block. The Full-tier heading text "Pseudocode (Full-tier addition...)" is inside the code fence rather than being a prose heading before it, which is inconsistent with the Standard-tier pseudocode block's heading treatment.

---

### DOC-063. Duplicate footnote marker `4` with different meanings [Medium]

**Location:** Lines 3646 and 9739-9741

Footnote `4` is used twice with completely different referents:
1. Line 3646: Explains the "Tenant sets" column in the lease extension budget resolution table.
2. Lines 9739/9741: Explains the Tier 3 `billingRedisStreamMaxLen` derivation in Section 17.8.2.

Both use the superscript `4` marker. The document uses numbered footnotes (`1`, `2`, `3`, `4`) but the numbering is not scoped per section -- the `4` at line 9739 collides with the `4` at line 3646. One should be renumbered or the document should adopt section-scoped footnote numbering.

---

### DOC-064. `BillingStreamEntryAgeHigh` classified as Critical in table but listed under Warning alerts section [Medium]

**Location:** Line 8826

The alert `BillingStreamEntryAgeHigh` is listed in the **Warning alerts** table (the table that begins at line 8792 with "Warning alerts:"), but its Severity column reads "Critical". This is internally inconsistent -- either the alert belongs in the Critical alerts table (lines 8762-8788), or its severity should be "Warning". Given that the description says it detects entries "at imminent risk of TTL expiry and permanent loss," Critical seems appropriate, meaning it is placed in the wrong table.

---

### DOC-065. Section 12.7 "Extensibility" is a stub relative to surrounding section depth [Low]

**Location:** Line 5827

Section 12.7 "Extensibility" (at line 5827) appears between the deeply specified Sections 12.6 (Pluggable Store Interfaces, with full interface definitions) and 12.8 (Compliance Interfaces, with ~140 lines of detailed erasure and tenant deletion specification). However, a search for "Section 12.7" references shows it is only referenced twice in the entire document (line 5585 and 5587, both about audit log write modes being determined by data classification tier). The section heading "Extensibility" suggests broader content about storage extensibility, but the actual content at this location (which the prior session identified as very brief) and the cross-references to it are about data classification tiers, not storage extensibility in general. This heading may be misleading.

---

### DOC-066. Missing Section 17.5 "Cloud Portability" -- content is a placeholder [Low]

**Location:** Lines 9115-9120

Section 17.5 "Cloud Portability" contains only four bullet points asserting that storage backends are pluggable, NetworkPolicies are standard, RuntimeClass works with conformant runtimes, and no cloud-specific CRDs are required. In a document where every other section provides exhaustive detail (Section 17.9 alone is ~200 lines on deployment profiles), Section 17.5 reads like a placeholder that was never fleshed out. The actual cloud portability details are covered in Section 17.9 (Deployment Profiles), making 17.5 redundant. Consider either expanding it with substantive content (e.g., tested Kubernetes distributions, CNI compatibility matrix) or removing it in favor of a forward reference to 17.9.

---

### DOC-067. Section numbering gap: no Section 17.7 heading in table of contents flow until line 9394 [Low]

**Location:** Lines 9107-9394

After Section 17.6 "Packaging and Installation" (which ends around line 9282), the document flow continues directly into the "Day 0 installation walkthrough" (still part of 17.6) through line 9393. Section 17.7 "Operational Runbooks" appears at line 9394. This is correct numbering but the Day-0 walkthrough (lines 9286-9393, over 100 lines) is extremely long for a subsection within 17.6. The walkthrough reads more like a standalone section (it includes 7 ordered steps, full YAML examples, and curl commands) than a subsection of "Packaging and Installation." Consider whether it should be its own numbered subsection (e.g., 17.6.1) for navigability.

---

### DOC-068. `sessionIsolationLevel` forward-referenced in step 8 before definition [Low]

**Location:** Line 2719 (step 8) vs. later definition

In the Normal Flow of Section 7.1, step 8 returns `sessionIsolationLevel` to the client (line 2719), but the concept of `sessionIsolationLevel` is introduced at step 22 (post-completion session state). While forward references are acceptable in a spec, step 8 returns this value as part of the session creation response -- the reader encounters it before understanding what it means. A brief inline note or cross-reference would improve clarity.

---

### DOC-069. `BillingStreamEntryAgeHigh` default threshold math: 80% of 3600s is 2880s, but the description says "default threshold: 2880s, i.e., 80% of 3600s" [Low]

**Location:** Line 8826

The math checks out (0.8 * 3600 = 2880), so this is not an error. However, the description states the alert fires when the oldest entry age "exceeds 80% of `billingStreamTTLSeconds`" while also providing the concrete default "2880s." If `billingStreamTTLSeconds` is changed from 3600s, the threshold changes proportionally. The dual specification (percentage and absolute value) is fine but the parenthetical "(default threshold: 2880s, i.e., 80% of 3600s)" is redundant since the percentage-based definition already covers it. This is editorial, not an error -- marking as Low.

**On reflection, this is too minor. Withdrawing this finding.**

---

### DOC-070. `lenny_pod_claim_queue_wait_seconds` metric name inconsistency [Medium]

**Location:** Lines 9662, 9842

At line 9662, the metric is referenced as `lenny_pod_claim_queue_wait_seconds` with the parenthetical "(P99 derived from histogram -- canonical name per S16.1)". At line 9842, it appears again as `lenny_pod_claim_queue_wait_seconds`. However, examining the metrics table in Section 16.1 (which I read during the first session), the canonical metric for pod claim queue wait time should be verified against the S16.1 table. The document calls it the "canonical name per S16.1" but uses `_wait_seconds` (histogram) in the capacity planning section while the earlier SLO section (line 2662 area, from the first read session) may use a different form. If the S16.1 table uses a different name, this is an inconsistency. If it matches, this is fine.

**On reflection, I cannot definitively confirm a mismatch without re-reading the exact S16.1 table entry. Withdrawing this finding due to insufficient evidence.**

---

### DOC-071. Missing Section 12.9 reference from Section 12.8 erasure SLA text [Medium]

**Location:** Line 5855

The text states: "Erasure jobs must complete within the tier-specific deadlines defined in Section 12.9: T3 data within 72 hours, T4 data within 1 hour." However, the document has Section 12.7 "Extensibility" and Section 12.8 "Compliance Interfaces" but there is no Section 12.9 heading anywhere in the document. The data classification tiers (T1-T4) and their definitions appear to be part of Section 12.8 or another subsection within Section 12. This is a dangling cross-reference to a non-existent section.

---

## 23. Messaging & Conversations (MSG)

### MSG-074. `delivery: "immediate"` exception for `input_required` creates silent buffering with misleading semantics [Medium]

The `MessageEnvelope` spec (line ~7863-7869) documents that when a session is in the `input_required` sub-state, `delivery: "immediate"` does NOT actually interrupt and deliver immediately -- it falls back to `queued` behavior (buffered in inbox, delivered FIFO after `request_input` resolves). However, the delivery receipt returned is `queued`, not `delivered`. The problem: a sender who explicitly sets `delivery: "immediate"` expects immediate delivery. The receipt correctly says `queued`, but the spec does not require the gateway to include any indication in the receipt that the `immediate` hint was downgraded. A sender tracking receipts cannot distinguish between "I sent with `queued`" and "I sent with `immediate` but it was silently downgraded to `queued`." The receipt schema (line ~7871-7883) has a `reason` field but it is only "populated when status is dropped, expired, or rate_limited." Add a `downgraded` or `delivery_override` field (or populate `reason` for `queued` status when `immediate` was requested) so senders can detect the downgrade.

### MSG-075. `inReplyTo` matching against `request_input` bypasses inbox ordering [Medium]

Line ~7887 states: "If [inReplyTo] matches an outstanding `lenny/request_input` call on the target, the gateway resolves that tool call directly instead of delivering to stdin." This is a direct-resolution path that bypasses the session inbox FIFO. If a sender sends message A (no `inReplyTo`, `delivery: "queued"`) and then message B (with `inReplyTo` matching a pending `request_input`), message B resolves immediately while message A remains queued. After `request_input` resolves, message A is delivered. The spec does not address whether this ordering inversion is intentional or what happens to message A's content. If message A was the "real" intended reply and message B was a stale retry, the runtime now gets both -- potentially corrupting the conversation. The spec should state explicitly whether `inReplyTo`-matched direct resolution should drain the inbox first, or whether the ordering inversion is accepted, and what runtimes should expect.

### MSG-076. MessageDAG `session_messages` table and delivery receipt persistence are under-specified [Medium]

Line ~7893 states "the gateway records every delivered message in the session's `MessageDAG` store (Postgres `session_messages` table)." Line ~7871 specifies that `lenny/send_message` returns a synchronous `delivery_receipt`. However, the spec never states whether delivery receipts themselves are persisted. The `GET /v1/sessions/{id}/messages` endpoint (line ~6998) says it "Returns message history including delivery receipts and state" -- implying receipts are persisted and queryable. But the `delivery_receipt` schema is only defined as a synchronous return value. The persistence model for receipts (same table? separate table? TTL?) is unspecified. This matters for reliable messaging patterns where senders re-read receipts after reconnection.

### MSG-077. Message deduplication window uses Redis but durable inbox uses Redis too -- crash semantics unclear [Medium]

Line ~7885 specifies that seen message IDs are stored in a Redis sorted set for deduplication (`deduplicationWindowSeconds`, default 3600s). Section 7.2 specifies that durable inboxes (`durableInbox: true`) use Redis lists. If Redis crashes and recovers, the deduplication set is lost. A sender retrying a message that was previously accepted (but whose dedup entry was lost) would see it accepted again, leading to duplicate delivery. The spec's inbox crash-recovery note (Section 7.2) says "Senders that require reliable delivery MUST track receipts and re-send on gap detection" -- but this guidance contradicts itself: re-sending after gap detection could create duplicates if the dedup set was lost in the same crash. The spec should state whether the dedup set is reconstructible from the `session_messages` Postgres table, or whether message-level idempotency is the sender's responsibility end-to-end.

### MSG-078. `from.kind` enum is closed but no forward-compatibility guidance for new kinds [Low]

Line ~7843 states "`from.kind` is a closed enum with exactly four values." However, Section 21.4 lists "external agent participation" as a future pattern accommodated by `MessageEnvelope`. If a fifth `from.kind` value is needed, the "closed enum" designation means adding it is a breaking change. Either the enum should be open (with unknown-kind handling guidance), or the spec should explain how new participant types map to the existing four kinds. For example, would an external A2A agent use `kind: "external"` with a different `id` format?

### MSG-079. `threadId` is documented as "optional, in v1 one implicit thread per session" but DAG ordering guarantee is per-thread [Medium]

Line ~7894 states: "Within a single thread, messages are ordered by the coordinator-local sequence number assigned at inbox-enqueue time." Line ~7897 says: "`threadId` -- optional. In v1 one implicit thread per session." The ordering guarantee is explicitly scoped to "within a single thread." But in v1, messages can be sent with or without `threadId` -- if some messages carry `threadId: "t_01"` and others omit it, are they in the same thread or different threads? The spec says "absent or the same value for all messages" but does not normatively define equivalence between `threadId: null` and `threadId: "t_01"`. If a sender sends a message without `threadId` and another with `threadId: "t_01"`, the ordering guarantee may not apply across them. This ambiguity needs a normative statement: in v1, all messages belong to a single logical thread regardless of `threadId` value.

### MSG-080. `POST /v1/sessions/{id}/messages` valid in "Any non-terminal state" but pre-running delivery semantics gap [Medium]

Line ~6948 states that messages can be sent to sessions in "Any non-terminal state" including `created`, `finalizing`, `ready`, and `starting`. For pre-running states, it says: "buffer (inter-session) or reject with `TARGET_NOT_READY` (external client)." This creates an asymmetry: inter-session messages (from sibling/parent agents) are buffered, but external client messages are rejected. However, the spec does not define when these buffered inter-session messages are delivered. If a parent sends a message to a child session in `created` state, is it delivered when the session reaches `running`? What if the session transitions to `running` but immediately enters `input_required` -- is the buffered message delivered before or after the initial task input? The delivery ordering between pre-buffered messages and the initial session message is unspecified.

### MSG-081. `maxInboundPerMinute` aggregate limit lacks per-sender attribution in receipt [Low]

Section 13.5 (line ~6518) describes `maxInboundPerMinute` as an "aggregate limit [that] caps total inbound messages to any single session regardless of the number of senders." When this limit is hit, the delivery receipt returns `rate_limited`. However, the receipt `reason` field does not distinguish between per-sender rate limiting (`maxPerMinute`) and aggregate rate limiting (`maxInboundPerMinute`). A well-behaved sender receiving `rate_limited` cannot tell whether backing off would help (per-sender limit) or whether the target is globally saturated (aggregate limit). Adding a `limitType` field (`per_sender` vs `aggregate`) to the receipt would enable smarter retry behavior.

### MSG-082. `delivery_receipt` status `error` with `reason: "scope_denied"` is indistinguishable from infrastructure errors [Low]

Line ~7883 lists `error` as a delivery receipt status with examples: `reason: "inbox_unavailable"` (infrastructure) and `reason: "scope_denied"` (policy). These are fundamentally different failure classes -- one is transient (retry may succeed), the other is permanent (retry will never succeed). But they share the same `status: "error"`. The `retryable` field from the error catalog (Section 15.1) is not present in the `delivery_receipt` schema. Senders need to know whether to retry. Either add a `retryable` boolean to the receipt or split `error` into `error` (transient) and `denied` (permanent).

### MSG-083. Reconnect semantics reference event cursor but cursor format is undefined for message delivery [Medium]

Section 7.2 (lines ~2984-2998, from earlier reading) describes reconnect semantics with an event cursor and replay window. The `GET /v1/sessions/{id}/messages` endpoint (line ~6998) supports `?since=` filter for the message DAG. But the reconnect cursor used for SSE streaming events (Section 7.2) and the `since` parameter for message history retrieval appear to be different mechanisms. The spec does not clarify whether a reconnecting client that resumes an SSE stream also receives messages that were delivered to the inbox during disconnection, or only events that were generated. If messages were `queued` during disconnection and delivered after reconnection, the SSE stream would show the delivery event, but the client might miss the message content if it only replays from the cursor. The interaction between SSE reconnect cursors and message history cursors needs clarification.

### MSG-084. `slotId`-based routing in concurrent-workspace mode lacks inbox isolation guarantee [Medium]

Line ~7859 states `slotId` is "present only in concurrent-workspace mode" and identifies the concurrent slot a message is addressed to. Section 7.2 defines the session inbox as a per-session data structure. The spec does not state whether concurrent-workspace mode uses per-slot inboxes or a single session inbox with `slotId`-tagged messages. If a single inbox is shared, a message targeting `slot_01` could be blocked behind messages for `slot_02` in the FIFO queue. Section 14 (line ~6532) mentions "per-slot inbox" but only in passing. The inbox data model for concurrent-workspace mode -- single shared inbox vs per-slot inboxes -- needs a normative definition, including the implications for ordering guarantees and delivery semantics.

### MSG-085. `one_shot` interaction mode allows "single `request_input` at Standard+ tier" but `delivery: "immediate"` exception creates deadlock risk [Medium]

From earlier reading: `one_shot` runtimes allow a single `request_input` call. If a `one_shot` session calls `request_input` and enters `input_required`, then an external client sends a message with `delivery: "immediate"`, the spec says the message is buffered (not delivered immediately) because the runtime is blocked in `request_input`. The `inReplyTo` mechanism can resolve the `request_input` directly (MSG-075). But if the external client sends a message without `inReplyTo` (perhaps not knowing about the pending `request_input`), the message is buffered. When `request_input` eventually times out (`maxElicitationWait`), the buffered message is delivered -- but the `one_shot` runtime has already used its single `request_input` allowance. The runtime now receives an unsolicited message it cannot process (it cannot call `request_input` again to clarify). The spec should define the interaction between `one_shot`'s single `request_input` allowance and buffered messages that arrive during `input_required`.

### MSG-086. `delegationDepth` field is "informational" but enables potential message spoofing [Low]

Line ~7895 states `delegationDepth` is "gateway-injected" and "informational; the gateway does not alter delivery semantics based on it." However, since it is gateway-injected, runtimes trust it. A malicious or buggy sibling could not directly spoof it (the gateway overwrites `from`). But the spec does not state whether `delegationDepth` is validated against the actual delegation tree structure. If a message is forwarded through an unexpected path, the `delegationDepth` might be incorrect. Since the field is purely informational, this is low risk, but the spec should clarify whether the gateway validates it or just computes it from the routing path.

### MSG-087. `session.awaiting_action` webhook event has no corresponding `session.input_resolved` event [Medium]

Line ~6647-6650 lists webhook event types including `session.awaiting_action` (fired when a session enters `awaiting_client_action`). However, there is no webhook event for when the session resumes from this state. A CI system reacting to `session.awaiting_action` has no webhook-driven way to know when the action was taken and the session resumed -- it must poll `GET /v1/sessions/{id}`. All other state transitions that matter to external systems have corresponding terminal events (`session.completed`, `session.failed`, etc.). Adding a `session.action_resolved` or `session.resumed` event would complete the webhook-based lifecycle notification model.

### MSG-088. `checkpoint_boundary` marker referenced in reconnect semantics but not defined in SSE event schema [Medium]

From the earlier reading (lines ~2984-2998), reconnect semantics mention a `checkpoint_boundary` marker. The spec defines `OutboundChannel` policies (buffered-drop and bounded-error) in Section 15 (lines ~6849-6882) but does not include `checkpoint_boundary` in the `SessionEvent` kinds listed in `OutboundCapabilitySet.SupportedEventKinds` (line ~6811: "state_change", "output", "elicitation", "tool_use", "error", "terminated"). If `checkpoint_boundary` is a distinct SSE event type used during reconnection, it should be listed in the supported event kinds. If it is metadata on an existing event type, its schema should be defined.

### MSG-089. Multi-turn `injection.supported: true` requirement not cross-referenced with `delivery: "immediate"` behavior [Low]

From earlier reading: Section 5.1 states that `capabilities.interaction: multi_turn` requires `injection.supported: true`. The `delivery: "immediate"` field (Section 15.4.1, line ~7863) describes interrupt-and-deliver behavior that depends on the runtime accepting mid-session content. However, the `delivery` field documentation does not reference the `injection.supported` requirement. A sender could set `delivery: "immediate"` on a message to a runtime with `injection.supported: false`, and the spec does not define what happens. Line ~6997 says "Gateway rejects injection against runtimes with `injection.supported: false`" for `POST /v1/sessions/{id}/messages`, but this is only for the REST endpoint -- the `lenny/send_message` platform MCP tool and the `delivery` field documentation do not repeat this guard.

### MSG-090. DLQ migration from inbox on `resume_pending` transition specified for "in-memory mode only" [Medium]

From earlier reading: the inbox-to-DLQ migration is specified as atomic drain on `resume_pending` transition for in-memory mode only. For durable inbox mode (Redis-backed), the spec does not define the DLQ migration behavior. When a session with `durableInbox: true` transitions to `resume_pending` (pod failed, gateway retrying), what happens to messages in the Redis-list inbox? Are they migrated to the DLQ? Left in the inbox for the new pod? The asymmetry between in-memory and durable inbox DLQ behavior could lead to message loss or duplication during pod recovery in durable inbox mode.

---

## 24. Policy Engine (POL)

### POL-071. RequestInterceptor chain lacks explicit concurrency safety contract for MODIFY actions [Medium]

Section 4.8 defines a priority-ordered `RequestInterceptor` chain where interceptors can return MODIFY actions that mutate the request. The spec defines immutable field enforcement per phase but does not specify what happens when two interceptors at the same priority level both return MODIFY actions targeting different mutable fields. Priority is described as an integer ordering, but the spec never states whether ties are allowed or how they are resolved (deterministic ordering by name? undefined?). Without a tie-breaking rule, the order of MODIFY application is implementation-dependent, which means policy outcomes can vary across deployments or even across gateway restarts if interceptor registration order changes.

**Why this matters:** If two custom interceptors both register at priority 50, one modifying `isolationProfile` and one modifying `tokenBudget`, the final request state depends on execution order. This is a policy correctness concern, not merely a performance concern.

---

### POL-072. Fail-open interceptor cumulative escalation threshold is unspecified [Medium]

Section 4.8 describes a fail-closed vs. fail-open failure policy for interceptors, with "cumulative escalation" when multiple fail-open interceptors fail simultaneously. However, the document does not specify the threshold or formula for cumulative escalation. How many fail-open interceptors must fail before the system escalates to fail-closed? Is it a count, a percentage, or a weighted calculation? Without a concrete formula, deployers cannot reason about the resilience posture of their interceptor chain under partial failure conditions.

---

### POL-073. DelegationPolicy tag-based matching lacks precedence rules for conflicting policies [Medium]

Section 8.3 defines `DelegationPolicy` as a first-class resource with tag-based matching, but the spec does not define what happens when multiple `DelegationPolicy` resources match a given delegation request with conflicting directives (e.g., one policy allows delegation to a runtime tag `llm-heavy` while another denies it). The spec mentions priority-ordered evaluation at the interceptor level (Section 4.8), but `DelegationPolicy` resources are separate from the interceptor chain -- they are evaluated by the `DelegationPolicyEvaluator` interceptor. The internal resolution logic when multiple policies match the same delegation is not specified: is it first-match, most-specific-match, deny-wins, or union-of-constraints?

**Why this matters:** In multi-tenant environments, platform admins and tenant admins may define overlapping policies. Without explicit conflict resolution semantics, the effective policy set is ambiguous.

---

### POL-074. Isolation monotonicity enforcement lacks explicit validation at DelegationPolicy registration time [Medium]

Section 8.3 specifies isolation monotonicity enforcement (standard < sandboxed < microvm) at delegation time, and Section 24.14 provides `lenny-ctl policy audit-isolation` as a read-only diagnostic. However, the spec does not define whether isolation monotonicity violations are checked at `DelegationPolicy` creation time. The `audit-isolation` command is described as a post-hoc diagnostic tool (client-side join of policies and pools), not an admission control gate. A deployer could register a `DelegationPolicy` that inherently violates monotonicity for every possible source pool, and this would only be caught at runtime when actual delegations fail. The spec should clarify whether `DelegationPolicy` admission validation rejects policies that can never satisfy monotonicity, or whether the current design intentionally defers all monotonicity checks to runtime.

---

### POL-075. contentPolicy.maxInputSize enforcement point is ambiguous for streaming delegation inputs [Medium]

Section 8.3 defines `contentPolicy` on `DelegationPolicy` with a `maxInputSize` limit and optional `interceptorRef` for content scanning. For non-streaming delegation calls, the input size is known at dispatch time. However, for multi-turn sessions where a delegated child accumulates input across multiple messages (Section 9), the spec does not clarify whether `maxInputSize` is enforced per-message, per-turn, or as a cumulative total across all messages in the delegated session. If cumulative, the enforcement point must track running totals. If per-message, a series of just-under-limit messages could deliver an arbitrarily large total input to the child. This ambiguity affects the security value of the size constraint.

---

### POL-076. Budget reservation Lua scripts lack explicit timeout/cancellation semantics [Medium]

Section 8.3 specifies atomic Redis Lua scripts for budget reservation (`budget_reserve.lua`). The spec details cross-tenant contention analysis and serialization blocking, including a 5ms SLO ceiling. However, the spec does not define what happens when a Lua script exceeds this ceiling: does Redis kill it via `lua-time-limit`? Does the gateway retry? Is there a client-side timeout on the `delegate_task` call path that will abort the operation if the Lua script is blocked for too long? Without explicit timeout semantics, a burst of concurrent `delegate_task` calls could create unbounded Lua serialization delays, cascading into gateway goroutine exhaustion.

---

### POL-077. ValidatingAdmissionWebhook for tenant pinning does not specify label immutability scope [Low]

Section 13 mentions a ValidatingAdmissionWebhook for tenant pinning via label immutability, and the document references multiple webhooks for different purposes (direct-mode isolation, sandboxclaim guard, data residency, drain readiness, T4 node isolation). However, the spec does not enumerate which specific labels are protected by the label immutability webhook. The tenant label (`lenny.dev/tenant-id`?) is implied, but what about `lenny.dev/pool`, `lenny.dev/runtime`, or `lenny.dev/isolation-profile`? If a compromised controller or RBAC misconfiguration allows label mutation on running pods, the blast radius depends on exactly which labels are immutable. The spec should enumerate the full set of immutable labels enforced by this webhook.

---

### POL-078. noEnvironmentPolicy deny-all default creates a bootstrap chicken-and-egg problem [Low]

Section 10 (referenced in Phase 5 notes at line 10050) describes `noEnvironmentPolicy` defaulting to `deny-all`, which blocks all `user`-role principals from accessing any runtime if no environments are configured. The spec acknowledges this gap and recommends the Phase 4.5 bootstrap seed set `noEnvironmentPolicy: allow-all` for pre-Phase 15 builds. However, the spec does not define what happens if a deployer runs `lenny-ctl bootstrap` with a seed file that does not include `noEnvironmentPolicy` at all -- does the tenant default to `deny-all` (locking out all users) or is there a seed-time default override? The bootstrap seed schema (Section 17.6) should explicitly document the default value for `noEnvironmentPolicy` in seed files and warn if it would result in a deny-all lockout with no environments configured.

**Mitigation already partially in place:** The Phase 5 notes describe the recommended approach, but the seed schema itself should enforce or warn, not rely on documentation.

---

### POL-079. Elicitation responses are explicitly exempt from contentPolicy interceptors -- no alternative enforcement hook documented [Medium]

Section 22.3 explicitly states that elicitation responses (Section 9.2) flowing from clients or connectors to pods are NOT subject to `contentPolicy` interceptors, and that this is an inherent property of human-in-the-loop systems mitigated by "connector registration requirements and provenance metadata." However, the spec does not define any alternative hook or extension point that deployers could use to scan elicitation content if they wanted to. The `RequestInterceptor` chain (Section 4.8) is described only for request admission, not for elicitation response payloads. A deployer with a strict content security requirement (e.g., HIPAA, FedRAMP) has no platform mechanism to scan human input before it reaches an agent pod. The spec should either (a) acknowledge this as an accepted limitation with an explicit risk statement, or (b) define an optional `ElicitationInterceptor` extension point.

**Partially acknowledged:** Section 22.3 provides the rationale, but the lack of any optional hook means deployers who need this capability must implement it outside the platform, which contradicts the hooks-and-defaults principle (Section 22.6).

---

### POL-080. Rate limit fail-open and quota fail-open use different timer mechanisms with no cross-concern coordination [Medium]

Section 12.4 defines two separate fail-open mechanisms for Redis unavailability: rate limiting uses `rateLimitFailOpenMaxSeconds` (default 60s) with an in-memory per-user counter, and quota enforcement uses `quotaFailOpenCumulativeMaxSeconds` (default 300s, rolling 1-hour window) with a cumulative sliding window persisted to `/run/lenny/failopen-cumulative.json`. These mechanisms operate independently with different timer semantics (simple countdown vs. cumulative sliding window), different defaults, and different fail-closed transition criteria. The spec does not address whether a rate-limit fail-closed transition (after 60s) should also trigger quota fail-closed, or vice versa. A deployer might assume that fail-closed on one concern implies fail-closed on all concerns, but the spec allows a state where rate limiting is fail-closed while quota enforcement remains fail-open for another 240 seconds -- creating a window where unauthenticated-rate requests are blocked but budget-exceeding requests are still allowed.

---

### POL-081. Per-tenant fail-open budget ceiling relies on cached_replica_count but Endpoints polling failure mode is underspecified [Low]

Section 12.4 specifies that the per-replica fail-open ceiling for each tenant is computed using `cached_replica_count` sourced from the Kubernetes Endpoints object, with a maximum staleness of 30 seconds. The spec states that if the Endpoints object has "never been successfully polled (cold start)," `cached_replica_count` defaults to 1. However, the spec does not address the scenario where a gateway replica successfully polls Endpoints once (getting, say, 10 replicas), then the API server becomes unreachable for an extended period while replicas scale down. The `cached_replica_count` would remain at 10 even though only 3 replicas are actually running, resulting in each replica's ceiling being too low (1/10th of tenant limit instead of 1/3rd). This under-allocation is safe from a budget perspective (conservative) but could cause legitimate requests to be rejected during a compound failure (Redis outage + API server outage + scale-down).

---

### POL-082. Circuit breaker operator-managed state lacks ETag-based concurrency control [Medium]

Section 11.6 describes operator-managed Redis-backed circuit breakers with manual open/close via `lenny-ctl admin circuit-breakers open|close`. Section 15.1 specifies ETag-based optimistic concurrency on "all admin PUT endpoints." However, the circuit breaker open/close operations are POST endpoints (Section 24.7: `POST /v1/admin/circuit-breakers/{name}/open`), not PUTs. The spec does not state whether these POST operations support ETags or any other concurrency control. Two operators simultaneously issuing `open` and `close` on the same breaker could race, with the final state depending on Redis write ordering. For a safety-critical control (circuit breakers protect subsystems from cascading failure), this lack of concurrency control is a gap.

---

### POL-083. Compliance profile enforcement gates (soc2, fedramp, hipaa) are referenced but the enforcement mechanism is not specified [Medium]

Section 11.7 references compliance profile enforcement gates (soc2, fedramp, hipaa), and Section 12.3 discusses T2 batching restrictions under HIPAA AU-9 and FedRAMP AU-10. However, the spec does not define where or how a deployer activates a compliance profile, what specific controls each profile enables beyond audit batching restrictions, or how the gateway evaluates the active compliance profile at request time. Is it a Helm value? A per-tenant configuration? A gateway-level flag? The data classification tiers (T1-T4) and their controls are well-specified, but the compliance profile concept is referenced without a schema definition, configuration surface, or enforcement chain specification.

---

### POL-084. Data residency enforcement fail-closed StorageRouter lacks fallback specification for write-path failures [Low]

Section 12.9 (data classification) and the storage architecture specify fail-closed behavior for the `StorageRouter` when data residency constraints cannot be satisfied. The spec states that the StorageRouter is fail-closed, meaning uploads are rejected if the correct region cannot be confirmed. However, the spec does not define the error code returned to the client, whether the session is terminated or just the upload, or whether the agent pod receives a signal that allows it to retry with a different artifact strategy. For agent runtimes that depend on artifact storage for intermediate results, a data-residency rejection mid-session could leave the session in an inconsistent state with no recovery path.

---

### POL-085. GDPR erasure processing restriction (Article 18) enforcement during active sessions is unspecified [Medium]

Section 12.8 specifies GDPR erasure with processing restriction, and the tenant deletion lifecycle includes a `processing_restricted` flag. However, the spec does not define what happens when an erasure request arrives for a user who has active sessions. Are active sessions for that user immediately terminated? Are they allowed to complete but no new sessions can be created? Is the `processing_restricted` flag checked at session creation time, at every message, or only at new-session-creation time? The timing of restriction enforcement relative to in-flight work is critical for GDPR Article 18 compliance, and the spec leaves this to implementation.

---

### POL-086. NetworkPolicy per-pool egress profiles (restricted, provider-direct, internet) lack runtime-level override specification [Low]

Section 13 defines three egress profiles for NetworkPolicy: restricted (no external), provider-direct (LLM provider endpoints only), and internet (full outbound). These are described at the pool level. However, the spec does not define whether a runtime definition can override or further restrict the pool's egress profile. If a pool is configured with `internet` egress but a specific runtime registered in that pool should only have `provider-direct` access, there is no mechanism to express this constraint at the runtime level. The pool is the unit of NetworkPolicy enforcement, which means all runtimes in a pool share the same egress profile. This is a policy granularity limitation that should be explicitly acknowledged or addressed with a runtime-level egress override.

---

### POL-087. Credential admission control blocks direct+standard+multi-tenant but does not specify error messaging for mixed-mode pools [Low]

Section 4.9 specifies that `deliveryMode: direct` with `isolationProfile: standard` is blocked by admission control when `tenancy.mode: multi`. However, the spec does not address what happens when a credential pool is configured with `deliveryMode: direct` and is referenced by both `standard` and `sandboxed` isolation pools. The admission webhook blocks the `standard` case but allows the `sandboxed` case. A deployer might configure a single credential pool used across multiple pools with different isolation profiles, and only discover at runtime that some pool combinations are rejected. The error message and diagnostic guidance for this scenario should be specified -- the `lenny-ctl policy audit-isolation` command (Section 24.14) audits delegation policy violations but not credential admission violations.

---

### POL-088. Setup command allowlist/blocklist policy lacks per-environment scoping [Low]

Section 7.5 defines setup command policy with allowlist and blocklist modes, and Decision #10 (Section 19) confirms that allowlist is recommended for multi-tenant deployments. However, the `setupPolicy` is defined on the runtime or pool level, not at the environment level. Since environments (Section 15, Phase 15) are the RBAC boundary for user access, a deployer cannot configure different setup command restrictions for different user groups accessing the same runtime. For example, a `dev` environment might want permissive setup commands while a `prod` environment sharing the same runtime should have strict allowlisting. The spec should clarify whether `setupPolicy` can be overridden at the environment level, or whether this requires separate runtime registrations.

---

### POL-089. Task-mode scrub policy deployer acknowledgment flags are referenced but the flag schema is not defined [Medium]

The summary context references "task-mode scrub policy with deployer acknowledgment flags," and Section 8 describes task policy with cleanup commands. However, the spec does not define the schema for scrub policy acknowledgment flags -- what specific risks must the deployer acknowledge, what is the flag format (boolean? per-risk enum?), where are they configured (per-runtime? per-pool? per-delegation-policy?), and what happens if a deployer omits the acknowledgment. Without a schema, implementors cannot validate deployer intent at configuration time.

---

### POL-090. Experiment routing isolation monotonicity check is mentioned but the enforcement path is not fully specified [Medium]

The spec mentions that experiment routing includes an isolation monotonicity check, ensuring that experiment variant pools do not downgrade isolation relative to the base pool. However, the enforcement path is not clearly specified: is this checked at `ExperimentDefinition` creation time, at experiment activation time, or at session-creation time when variant routing fires? If checked only at session-creation time, a misconfigured experiment could be created and activated, then fail only when actual sessions attempt to route to the variant -- creating a confusing operational experience. The `DelegationPolicyEvaluator` enforces monotonicity for delegations, but experiment routing is a separate code path (Section 10.7) and the spec should clarify which component enforces monotonicity for experiments and at what lifecycle point.

---

### POL-091. A2A adapter elicitation suppression (block_all) and input-required mapping create a policy bypass path for connector OAuth flows [Medium]

Section 21.1 states that `A2AAdapter`-initiated sessions set `elicitationDepthPolicy: block_all` for agent-initiated elicitations, but "gateway-registered connector OAuth flows remain exempt (connector-initiated, not agent-initiated)." The spec does not define how the gateway distinguishes between agent-initiated elicitation requests and connector-initiated OAuth flows at the enforcement point. If a compromised agent runtime crafts a request that mimics the connector OAuth flow signature, the `block_all` policy could be bypassed. The spec should define the distinguishing signal (e.g., originating component identity, request path, SPIFFE URI) that the gateway uses to differentiate these two categories.

**Mitigation note:** This is post-v1 (Section 21), but the design constraint is stated as a v1 scoping decision, so the enforcement mechanism should be specified now to avoid retrofitting.

---

### POL-092. Preflight validation Job checks infrastructure but does not validate policy consistency [Low]

Section 17.6 and 24.2 describe the preflight validation Job with comprehensive infrastructure checks (Postgres, Redis, MinIO connectivity and configuration). However, the preflight does not include any policy consistency validation -- for example, checking that all registered `DelegationPolicy` resources reference runtimes and pools that exist, that isolation monotonicity is satisfiable for the current pool configuration, or that credential pool `deliveryMode` settings are compatible with pool isolation profiles under the active `tenancy.mode`. The `lenny-ctl policy audit-isolation` command exists as a separate diagnostic, but it requires a running gateway. A pre-deployment policy consistency check in the preflight Job would catch configuration errors before deployment, not after.

---

---

## 25. Execution Modes (EXM)

### EXM-063. Concurrent-workspace mode has no stated integration tier requirement [Medium]

**Location:** Section 5.2 (lines ~2120-2160), Section 15.4.3 (lines ~8209-8244)

Section 5.2 defines concurrent-workspace mode and its per-slot lifecycle, including lifecycle channel notifications ("The gateway is notified via the lifecycle channel," line 2157). The Tier Comparison Matrix (Section 15.4.3) explicitly enumerates task-mode pod reuse as Full-tier-only, and specifies that Minimum/Standard tiers lack the lifecycle channel. However, concurrent-workspace mode is never mapped to a required integration tier.

Concurrent-workspace's per-slot failure notification via the lifecycle channel (line 2157), per-slot credential rotation (Section 6.1), and per-slot checkpoint support all implicitly require Full-tier features. The spec should explicitly state the minimum integration tier for `executionMode: concurrent` (both `workspace` and `stateless` sub-variants) and document the behavior (or rejection) when a non-Full-tier runtime is configured with concurrent-workspace mode. Without this, a deployer could configure a Minimum-tier runtime with `executionMode: concurrent, concurrencyStyle: workspace` and encounter undefined behavior.

**Challenge:** One could argue that concurrent-workspace is implicitly Full-tier since it uses lifecycle channel features. However, task mode has an explicit, detailed section on tier interaction (Section 5.2, "Task mode and integration tiers") including fallback behavior for non-Full tiers. Concurrent mode lacks an equivalent treatment, which is an inconsistency.

---

### EXM-064. Per-slot process group kill mechanism unspecified for concurrent-workspace [Medium]

**Location:** Section 5.2 (lines ~2155-2160), Section 6.4 (lines ~2670-2675)

The slot cleanup description states "the adapter removes the slot's workspace directory, kills any processes owned by the slot's process group, and releases the slotId" (line 2158/2472). However, the spec never defines how processes are associated with a slot's process group. The task-mode scrub uses `kill -9 -1` (as the sandbox user) to kill all remaining user processes (step 1, line 2071), which is a blanket kill. For concurrent-workspace, killing all user processes would terminate processes belonging to other active slots.

The spec needs to specify: (a) how the adapter creates a per-slot process group at task dispatch (e.g., `setpgid`, Linux cgroups, or a per-slot UID), (b) how the adapter identifies and kills only the processes belonging to a specific slot during slot cleanup, and (c) how this interacts with the shared process namespace acknowledged in the deployer acknowledgment (line 2139). Without this, the "kills any processes owned by the slot's process group" statement is underspecified and implementers will have to invent their own isolation mechanism.

**Challenge:** The deployer acknowledgment explicitly states "shared process namespace" as an accepted limitation. However, the slot cleanup explicitly references per-slot process group kill, which contradicts a fully shared namespace. The mechanism for scoping a kill to a slot is a genuine gap.

---

### EXM-065. Concurrent-workspace slot assignment Lua script does not check pod `draining` state [Medium]

**Location:** Section 5.2 (lines ~2162-2163), Section 6.2 (lines ~2462-2465)

The slot assignment Lua script (line 2162) atomically checks `current_count >= maxConcurrent` and conditionally increments. However, the pod state machine (line 2462-2464) shows that `slot_active -> draining` occurs when the unhealthy threshold is reached or `maxPodUptimeSeconds` is exceeded, and `idle -> draining` when uptime expires. The Lua script description does not mention checking whether the pod is in `draining` state before assigning a new slot.

If the gateway's pod selection logic pre-filters draining pods before calling the Lua script, the check is unnecessary at the Redis level. However, the spec does not state that draining pods are excluded from the candidate set. Line 2463 says "no new slots accepted" when draining, but the enforcement mechanism is not specified. A race between the pod entering `draining` and a concurrent slot assignment request could result in a new slot being assigned to a draining pod.

**Challenge:** The gateway likely filters pods by Kubernetes label (`lenny.dev/state != draining`) before attempting Redis slot reservation. However, the spec describes the stabilization delay (5-second delay on `active -> idle`) which means labels may lag. The interaction between the label-based pod selection, the stabilization delay, and the Redis-based slot assignment is not fully specified for the draining transition.

---

### EXM-066. Concurrent-workspace `cleanupTimeoutSeconds` validation formula inconsistency [Low]

**Location:** Section 5.2 (lines ~2136-2158)

The `concurrentWorkspacePolicy` block specifies `cleanupTimeoutSeconds: 60` with the comment "per-slot cleanup timeout is max(cleanupTimeoutSeconds / maxConcurrent, 5); must be >= maxConcurrent x 5." For `maxConcurrent: 8` and `cleanupTimeoutSeconds: 60`, the per-slot cleanup timeout is `max(60/8, 5) = max(7.5, 5) = 7.5s`. The CRD validation rule (line 2158) rejects configurations where `cleanupTimeoutSeconds < maxConcurrent x 5`, i.e., `60 < 8 x 5 = 40`, which passes. So the example is valid.

However, the `terminationGracePeriodSeconds` CRD validation formula (line 2159) is `maxConcurrent x max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 <= terminationGracePeriodSeconds`. This formula accounts for serialized per-slot checkpoints but does not include `cleanupTimeoutSeconds`. The slot cleanup (workspace removal, process kill) runs before or after the checkpoint. If cleanup precedes checkpoint, the `terminationGracePeriodSeconds` budget should include both cleanup and checkpoint time. The spec does not clarify whether slot cleanup runs within the checkpoint budget or separately.

**Challenge:** The cleanup may run during normal operation (not during eviction), so it would not consume terminationGracePeriodSeconds budget. However, if a SIGTERM arrives while slots are active and some slots are mid-cleanup, the cleanup timeout competes with the checkpoint budget. The ordering is underspecified.

---

### EXM-067. Task-mode `task_complete` signaling is on lifecycle channel but described in binary protocol section [Low]

**Location:** Section 15.4.1 (line 7609), Section 5.2 (line 2058), Section 4.7 (line 725)

Section 15.4.1 ("Adapter Binary Protocol") describes task-mode between-task signaling: "Adapter sends `{type: "task_complete", taskId: "..."}` on the lifecycle channel" (line 7609). Section 5.2 also says "adapter sends `task_complete` on lifecycle channel" (line 2058). Section 4.7 (line 725) lists `task_complete_acknowledged` as a lifecycle channel message type (Runtime -> Adapter).

However, Section 15.4.1 is titled "Adapter Binary Protocol" and describes the stdin/stdout protocol. The task signaling is placed in this section but uses the lifecycle channel, not stdin/stdout. This is a documentation organization issue: the lifecycle channel messages are partially defined in Section 4.7 and partially re-stated in Section 15.4.1, creating potential for divergence.

**Challenge:** This is a documentation clarity issue rather than a specification error. The information is consistent across sections. However, placing lifecycle channel behavior in the "Binary Protocol" section could confuse implementers.

---

### EXM-068. Concurrent-workspace `slotId` multiplexing over stdin lacks flow control specification [Medium]

**Location:** Section 5.2 (line 2128), Section 15.4.1 (lines 7607)

Concurrent-workspace mode multiplexes multiple independent task streams through a single stdin channel using `slotId`. All binary protocol messages carry `slotId`. With `maxConcurrent: 8`, up to 8 independent task streams share one stdin pipe.

The spec does not define: (a) flow control or backpressure for the shared stdin channel -- if one slot produces high-volume tool calls, it can starve other slots' message delivery; (b) message ordering guarantees -- whether the adapter preserves per-slot FIFO ordering when interleaving messages from different slots on stdin; (c) maximum message size interaction -- if a single large `tool_result` for one slot blocks the pipe, other slots' time-sensitive messages (heartbeat_ack, slot-scoped credential rotation) are delayed. For `maxConcurrent: 8` with all slots active, this shared channel becomes a potential bottleneck.

**Challenge:** For typical LLM agent workloads, messages are small (JSON text) and infrequent enough that a single pipe is adequate. The concern is primarily theoretical at `maxConcurrent <= 8`. However, the spec defines `maxConcurrent` as a deployer-configurable field with no stated upper bound beyond the `terminationGracePeriodSeconds` constraint. At higher values or with large tool results (e.g., file content), pipe contention becomes material. A note about flow control or a recommended `maxConcurrent` ceiling for the stdin multiplexing model would address this gap.

---

### EXM-069. `mode_factor` cold-start fallback inconsistent between Sections 5.2 and 17.8.2 [Medium]

**Location:** Section 5.2 (line 2210), Section 17.8.2 (line 9610)

Section 5.2 states: "During cold start (no historical data), the controller falls back to `mode_factor = 1.0` (session-mode sizing) until sufficient samples are collected (default: 100 completed tasks)." This fallback applies to task-mode pools.

Section 17.8.2's delegation-adjusted minWarm formula (line 9606-9610) divides by `mode_factor` and states: "This formula assumes session mode when mode_factor = 1.0 and burst_mode_factor = 1.0. For task-mode or concurrent-mode delegation child pools, apply the appropriate mode_factor and burst_mode_factor values from Section 5.2."

The issue: the delegation-adjusted formula at line 9606 includes `/ mode_factor` as a divisor in the `minWarm` expression. During cold start for a task-mode pool, `mode_factor = 1.0` per the fallback. An operator reading Section 17.8.2 alone might use `mode_factor = maxTasksPerPod` (e.g., 50) based on the guidance "apply the appropriate mode_factor," producing a `minWarm` that is 50x too small during the cold-start window. Section 17.8.2 should note the cold-start fallback explicitly in its guidance, since operators sizing delegation-adjusted pools at first deployment have no historical data.

**Challenge:** Section 5.2 does document the cold-start fallback. The question is whether Section 17.8.2's operator-facing guidance should repeat it for clarity. Given that 17.8.2 is the capacity planning reference and provides worked examples, the omission of the cold-start caveat in the delegation formula guidance is a practical gap that could lead to under-provisioning.

---

### EXM-070. Credential lease lifecycle in concurrent-stateless mode is unspecified [Medium]

**Location:** Section 6.1 (lines 2346-2348)

Section 6.1 defines credential lease lifecycle for three modes: per-session (session mode), per-task (task mode), and per-slot (concurrent-workspace mode). Concurrent-stateless mode is not mentioned. Since concurrent-stateless pods have "no workspace delivery, no per-slot lifecycle tracking" (line 2147) and routing goes through a Kubernetes Service, the credential lifecycle is ambiguous.

Does a concurrent-stateless pod get a single credential at pod startup (per-pod lifetime)? Does each routed request get an independent credential? Are credentials managed by the deployer's runtime since Lenny's role is limited to routing? The spec should state explicitly how (or whether) credentials are managed for `concurrencyStyle: stateless` pods.

**Challenge:** Section 5.2 (line 2147) states concurrent-stateless has "minimal platform guarantees" and recommends connectors instead. Credential management may be intentionally left to the deployer. However, the credential lease lifecycle section in 6.1 covers all other modes explicitly, and the omission of concurrent-stateless is a gap in completeness.

---

### EXM-071. Per-slot checkpoint serialization can exceed `terminationGracePeriodSeconds` with Postgres fallback [Medium]

**Location:** Section 5.2 (line 2159)

The spec states eviction checkpoints are serialized across slots and the CRD validation formula is: `maxConcurrent x max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 <= terminationGracePeriodSeconds`. The Postgres fallback retry budget is stated as "60s per slot" and "runs serially."

For `maxConcurrent: 8` with all checkpoints falling back to Postgres (e.g., during a MinIO outage): the total time would be `8 x 60s = 480s` for Postgres fallback alone, plus `checkpointBarrierAckTimeoutSeconds (90s) + 30s = 600s`. But the CRD formula uses `max_tiered_checkpoint_cap` (the MinIO-based cap, e.g., 90s for 512MB workspaces), not the Postgres fallback timeout. If all 8 slots fall back to Postgres simultaneously (MinIO degraded scenario that the eviction serialization was designed to address), the actual time is `8 x 60s + 90s + 30s = 600s`, which may or may not fit within the `terminationGracePeriodSeconds` computed using the MinIO-based cap.

The CRD validation formula should either (a) use `max(max_tiered_checkpoint_cap, postgres_fallback_timeout_per_slot)` instead of just `max_tiered_checkpoint_cap`, or (b) document that the formula is calibrated for the MinIO path and the Postgres fallback path may exceed the budget during degraded storage scenarios.

**Challenge:** The Postgres fallback is a last-resort path for eviction checkpoints. The spec notes that "Postgres fallback retry budget (60s per slot) also runs serially; the CRD validation formula already accounts for the sum of slot caps in the terminationGracePeriodSeconds constraint." This suggests the formula intends to cover the fallback. But `max_tiered_checkpoint_cap` for large workspaces (e.g., 90s at 512MB) is larger than the 60s Postgres fallback, so the formula holds in that case. For smaller workspaces (e.g., 30s cap), `8 x 30s + 120s = 360s` vs `8 x 60s + 120s = 600s` -- the Postgres fallback exceeds the formula. This is a genuine gap for small-workspace high-concurrency configurations.

---

### EXM-072. Concurrent-workspace mode shares `/tmp` but scrub step 4 clears `/tmp` [Low]

**Location:** Section 5.2 (lines 2075, 2128-2139)

Scrub step 4 (line 2075) specifies "Clear `/tmp`, `/dev/shm`, and any adapter-managed scratch directories." This step is defined in the task-mode scrub procedure. The concurrent-workspace deployer acknowledgment (line 2139) explicitly lists "shared `/tmp`" as an accepted limitation.

For concurrent-workspace per-slot cleanup (line 2158), the cleanup "removes the slot's workspace directory, kills any processes owned by the slot's process group, and releases the slotId." Clearing `/tmp` is not mentioned in per-slot cleanup, which is correct since `/tmp` is shared and clearing it would affect other active slots.

However, the `scrubPolicy` field in the session isolation response (line 2765) returns `"best-effort-per-slot"` for concurrent-workspace, described as "the same scrub operations (workspace removal, process-group kill, scratch directory cleanup)." The phrase "scratch directory cleanup" could be interpreted as including `/tmp`, but doing so would break other slots. The spec should clarify that per-slot cleanup does NOT clear `/tmp` or `/dev/shm` in concurrent-workspace mode, unlike the full task-mode scrub.

**Challenge:** This is arguably implied by the "shared `/tmp`" acknowledgment. However, the `scrubPolicy` description's mention of "scratch directory cleanup" is ambiguous enough to warrant clarification.

---

### EXM-073. `allowCrossTenantReuse` explicitly prohibited for concurrent-workspace but not validated at all layers [Low]

**Location:** Section 5.2 (line 2141)

The spec states: "The pool controller explicitly rejects any concurrent-workspace pool definition where `allowCrossTenantReuse: true` is set at any level (pool-level or within `concurrentWorkspacePolicy`) at validation time." The rejection error is specific and well-defined.

However, `allowCrossTenantReuse` is defined as a `taskPolicy` field (line 2044), not a `concurrentWorkspacePolicy` field. The phrase "at any level (pool-level or within `concurrentWorkspacePolicy`)" implies it could appear in `concurrentWorkspacePolicy`, but the schema example for `concurrentWorkspacePolicy` (line 2131-2137) does not include this field. The validation check should either: (a) state that the check applies to `taskPolicy.allowCrossTenantReuse` on the pool definition (since pools have a single policy block), or (b) clarify the field's location if `concurrentWorkspacePolicy` has its own `allowCrossTenantReuse` field.

**Challenge:** This is a minor schema location ambiguity. The intent is clear and the validation provides defense-in-depth regardless. However, the reference to "`concurrentWorkspacePolicy`" having an `allowCrossTenantReuse` field creates an inconsistency with the schema examples.

---

### EXM-074. Concurrent-workspace pod `maxPodUptimeSeconds` check timing unspecified relative to slot assignment [Medium]

**Location:** Section 5.2 (line 2160), Section 6.2 (lines 2463-2464)

The pod state machine shows `idle -> draining` when `maxPodUptimeSeconds` exceeded (line 2464), with the note "checked before next assignment." For task mode, this check is straightforward: the gateway checks uptime before assigning the next sequential task.

For concurrent-workspace mode, new slots are assigned concurrently while existing slots are running. The state machine shows `slot_active -> draining` when `maxPodUptimeSeconds` exceeded, with "no new slots accepted, existing slots drain" (line 2463). But the timing of this check is unclear: does the gateway check `maxPodUptimeSeconds` before every slot assignment (in the Lua script or before calling it), or is it checked periodically by a controller? If checked only at assignment time, a pod that exceeds `maxPodUptimeSeconds` while all its slots are running will not drain until a slot completes and triggers a check, potentially running well beyond the configured limit.

**Challenge:** For task mode, the uptime check happens between tasks, which is a natural serialization point. For concurrent-workspace, there is no equivalent natural check point during active execution. The spec should clarify whether a periodic background check enforces `maxPodUptimeSeconds` for concurrent-workspace pods or whether it relies on the next slot assignment/completion event.

---

### EXM-075. Task-mode scrub step 1b IPC cleanup may fail silently in gVisor [Low]

**Location:** Section 5.2 (line 2072)

Step 1b states: "For gVisor pods this step is a no-op in practice because gVisor's per-pod sandbox kernel provides a fully isolated IPC namespace -- segments cannot leak to other pods -- but the step executes unconditionally for consistency."

This is accurate for cross-pod isolation. However, within a single pod across sequential tasks, IPC segments created by task N can persist and be observable by task N+1 if the creating process was killed in step 1 but the segment outlived the process (which is exactly why step 1b exists). The statement that this is "a no-op in practice" for gVisor is misleading in the task-mode context -- the concern is intra-pod cross-task leakage, not cross-pod leakage. In gVisor, `ipcrm --all=shm` is the correct remediation and is not a no-op for the task-mode use case.

**Challenge:** The spec says the step "executes unconditionally for consistency," which means it runs regardless. The "no-op in practice" characterization is slightly misleading but does not affect correctness since the step is always executed. This is more of a documentation accuracy issue than a specification error.

---

### EXM-076. `REHYDRATE_REQUIRED` sentinel handling does not specify behavior for session-mode or task-mode pods [Low]

**Location:** Section 5.2 (line 2164)

The rehydration mechanism is described exclusively for concurrent-workspace pods: "slot counters reset to zero but pods may still have active slots." The `rehydrated` flag, `REHYDRATE_REQUIRED` sentinel, and `GetActiveSlotsByPod` query are all scoped to concurrent-workspace.

However, task-mode pods also track state in Redis (the pod's task assignment status). If Redis restarts while a task-mode pod is between tasks (idle, waiting for assignment), the gateway's Redis-based routing state for that pod may be stale. The spec does not describe whether task-mode or session-mode pods have their own rehydration mechanism after Redis restart, or whether they rely solely on the Kubernetes label-based pod state.

**Challenge:** Session-mode and task-mode pods use Kubernetes labels and Postgres as the source of truth for pod state. The Redis slot counter is specific to concurrent-workspace. So rehydration may genuinely only be needed for concurrent-workspace. However, the spec should explicitly scope the rehydration section to concurrent-workspace to prevent confusion about whether other modes need similar handling.

---

### EXM-077. Concurrent-workspace per-slot retry "new slot on same pod" may re-encounter the same underlying issue [Low]

**Location:** Section 5.2 (line 2168)

The retry policy states: "The retry is always assigned to a new slot on the same pod (if a slot is available) or on a different pod." Retrying on the same pod makes sense for transient failures but not for pod-level issues (e.g., resource contention, kernel state, network partition). The non-retryable categories (OOM, workspace validation, policy rejection) cover some pod-level issues, but others (e.g., intermittent disk I/O errors, DNS resolution failures from the shared network namespace) would retry on the same pod and fail again.

**Challenge:** The whole-pod replacement trigger (`ceil(maxConcurrent/2)` failures in 5 minutes) addresses this at scale by detecting degraded pods. For isolated single-slot failures, retrying on the same pod is a reasonable default to avoid unnecessary cross-pod traffic. The spec's design is defensible; this finding is informational rather than a genuine error.

---

### EXM-078. `burst_mode_factor` for task mode is 1.0 but task-mode preConnect re-warm adds to per-task latency [Low]

**Location:** Section 5.2 (lines 2203, 2210)

Section 5.2 sets `burst_mode_factor = 1.0` for task mode because "task pods process tasks sequentially (one at a time), so each pod absorbs exactly one burst arrival." This is correct. However, line 2210 notes that for task-mode pools with `preConnect: true`, "the inter-task SDK re-warm window (up to sdkConnectTimeoutSeconds, default 60s) adds to the per-task cycle time." This means during a burst, previously active task-mode pods are unavailable for the duration of scrub + SDK re-warm + potential demotion, which can be 30-90 seconds. With `burst_mode_factor = 1.0`, the burst term does not account for this unavailability window.

The formula divides the burst term by `burst_mode_factor`, so a factor of 1.0 means no burst absorption benefit from task-mode pods. However, the issue is the reverse: during a burst, task-mode pods that just completed a task are not instantly available (they are in `task_cleanup -> sdk_connecting -> idle`), effectively reducing the pool size during the burst. The burst term should arguably include a factor for task-mode SDK re-warm latency, not just `burst_mode_factor = 1.0`.

**Challenge:** The `minWarm` formula's steady-state term (divided by `mode_factor`) already accounts for the reduced throughput due to re-warm overhead, since `mode_factor` uses observed `lenny_task_reuse_count` p50 which reflects actual cycle time. The burst term's `burst_mode_factor = 1.0` means "each pod absorbs one burst arrival," which is correct -- each idle pod handles one burst request regardless of what happens between tasks. The concern is that the pool has fewer idle pods during a burst because some are in re-warm. This is covered by the `minWarm` steady-state term. Finding downgraded to Low.

---

---
