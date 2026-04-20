# Iter3 NET Review

### NET-051 lenny-ops entirely absent from lenny-system NetworkPolicy allow-lists; default-deny blocks every lenny-ops flow [Critical]
**Files:** `13_security-model.md:180-211`, `25_agent-operability.md:1090-1158`
§13.2 applies a default-deny NetworkPolicy to all pods in `lenny-system` (line 186) and then enumerates component-specific allow-lists at lines 197-211 — gateway, token-service, controller, pgbouncer, minio, admission-webhook, coredns, OTLP collector. There is no row for `lenny-ops`. Yet `17_deployment-topology.md:31` and `25_agent-operability.md:1158` both say `lenny-ops` "lives in `{Release.Namespace}` (default `lenny-system`)". The `lenny-ops-egress` NetworkPolicy rendered by the chart (`25_agent-operability.md:1117-1154`) is overlay-additive — it grants egress from `lenny-ops` pods — but in lenny-system the default-deny policy composes set-union across all NetworkPolicies on a pod, so this overlay is how `lenny-ops` gets egress. The gap is on the **other side**: nothing in §13.2's allow-list permits the gateway to **receive** connections from `lenny-ops` on port 8080 (the gateway's main admin-API port — `25_agent-operability.md:850,1428,1673`). The gateway's ingress row at line 204 only allows TCP 8080 "from admission-webhook pods" for `lenny-drain-readiness`. Every `lenny-ops` → gateway admin-API call (health summary, config GET/PUT, backup orchestration, restore execute, remediation locks, platform upgrade, recommendations, diagnostics, connector probes — all listed in §25.3) is blocked by the `lenny-system` default-deny on the ingress side.

Similarly, no ingress row grants Postgres/Redis/MinIO ingress from `lenny-ops` — the datastore rows at lines 207-208 list Gateway, Token Service, and Warm Pool Controller as the only authorised ingress sources.

**Recommendation:** Add a dedicated `lenny-ops` row to the §13.2 allow-list table with ingress from the configured `lenny-ops` ingress namespace on TCP 8090 (Prometheus on 9090) and egress to Gateway:8080, PgBouncer:5432, Redis:6380/TLS, MinIO:9443/TLS, Prometheus:9090, K8s API, and kube-system CoreDNS. Add matching ingress rows to the Gateway, PgBouncer, MinIO, and Redis component allow-lists recognising `lenny-ops` as a source. Since §13.2 line 199's normative rule excepts `lenny-ops` from the `lenny.dev/component` requirement, these rules use `podSelector: { matchLabels: { app: lenny-ops } }` paired with `namespaceSelector: { matchLabels: { kubernetes.io/metadata.name: lenny-system } }`. Without these rows, the `lenny-system` default-deny policy (NET-017) silently breaks every operability flow at install time.

---

### NET-052 lenny-ops-egress Redis rule targets plaintext port 6379; Redis is configured with plaintext disabled [High]
**Files:** `25_agent-operability.md:1136-1138`
```yaml
# Redis
- to: [{ namespaceSelector: { matchLabels: { name: { storage.namespace } } } }]
  ports: [{ protocol: TCP, port: 6379 }]
```
`10_gateway-internals.md:270` mandates: *"Redis **must** be configured with `tls-auth-clients yes` and `tls-port` set to the TLS listener port (default `6380`) with the plaintext `port` set to `0` (disabled)."* §13.2 lines 204-205 allow Gateway and Token Service egress to Redis only on TCP 6380 TLS. The `lenny-ops-egress` rule opens TCP 6379 instead; the Redis pod does not even listen on 6379 in a correctly configured cluster, so `lenny-ops` Redis operations (event stream reads, distributed lock acquisition, idempotency cache access) will fail with connection refused at the application layer. More subtly, if an operator mis-configures Redis to leave plaintext enabled, `lenny-ops` would bypass TLS entirely — a silent regression of NET-004.

**Recommendation:** Change port to 6380 and update the comment to `# Redis (TLS)`. Extend the NET-047 selector-consistency preflight to also check that `lenny-ops-egress` port values match the chart's configured TLS ports for each backend (`{{ .Values.redis.tlsPort }}`, `{{ .Values.minio.tlsPort }}`). Same preflight should flag any `lenny-ops` rule permitting 6379 as a hard error.

---

### NET-053 lenny-ops-egress MinIO rule targets 9000 while Gateway/§13.2 require 9443 TLS; port skew breaks lenny-ops uploads and backup writes [High]
**Files:** `25_agent-operability.md:1140-1141`, `13_security-model.md:204,208`, `12_storage-architecture.md:279`
```yaml
# MinIO / S3-compatible
- to: [{ namespaceSelector: { matchLabels: { name: { storage.namespace } } } }]
  ports: [{ protocol: TCP, port: 9000 }]
```
§13.2 line 208 lists MinIO ingress as `TCP 9443, TLS — {{ .Values.minio.tlsPort }}` from Gateway. `12_storage-architecture.md:279` says *"MinIO connections MUST use TLS (`https://` endpoint)"*. But `lenny-ops-egress` and `lenny-backup-job` (line 1203) both target plaintext 9000. Backup Jobs that call `PutObject` on port 9000 either fail (TLS-only listener) or silently connect plaintext (mis-configured), violating encryption-at-rest/in-transit invariants advertised at `25_agent-operability.md:939-940`. The config at line 1416 of `17_deployment-topology.md` uses `http://minio.lenny-system:9000` in the embedded/dev mode — so 9000 is a dev-mode relic; production TLS port is 9443.

**Recommendation:** Change all `lenny-ops` and `lenny-backup-job` MinIO egress rules to `{{ .Values.minio.tlsPort }}` (default 9443). Add an explicit note that the NetworkPolicy allowlist follows the TLS listener port, not any plaintext dev-mode port.

---

### NET-054 lenny-ops-egress namespace selectors use non-immutable `name:` label, contradicting §13.2 normative guidance [High]
**Files:** `25_agent-operability.md:1134,1137,1140,1143`
```yaml
- to: [{ namespaceSelector: { matchLabels: { name: { storage.namespace } } } }]
# …identical shape for Redis, MinIO, Prometheus (monitoring.namespace)
```
`13_security-model.md:172` is explicit: *"The namespace selectors above use `kubernetes.io/metadata.name` rather than custom labels like `lenny.dev/component`. This is an immutable label set by the Kubernetes API server on namespace creation — it cannot be added to or spoofed on other namespaces, unlike custom labels."* The `name:` key is **not** applied automatically by Kubernetes and is mutable; if the deployer's storage/monitoring namespace lacks this label, the selector silently matches zero namespaces and `lenny-ops` cannot reach Postgres/Redis/MinIO/Prometheus. Conversely, a principal with namespace-update rights can apply `name: lenny-system` to an attacker-controlled namespace and gain ingress from `lenny-ops` to any pod in that namespace on 5432/6380/9443/9090. The NET-047 fix standardised this for `lenny-system` component selectors but did not extend to the `lenny-ops` chart.

Additionally, these rules omit `podSelector`, so every pod in the targeted namespace is reachable on the listed ports — not just Postgres/Redis/MinIO/Prometheus.

**Recommendation:** Replace every `matchLabels: { name: <ns> }` with `matchLabels: { kubernetes.io/metadata.name: <ns> }` and pair each with a `podSelector` for the specific backend (`app: postgres`, `app: redis`, `app: minio`, `app.kubernetes.io/name: prometheus`). Extend the NET-047 preflight consistency audit to cover `lenny-ops`-chart NetworkPolicies — the §13.2 normative statement should be chart-wide.

---

### NET-055 lenny-ops-egress and lenny-backup-job K8s API rules use empty namespaceSelector, permitting egress to every pod on TCP 443 [High]
**Files:** `25_agent-operability.md:1146,1205`
```yaml
# K8s API
- to: [{ namespaceSelector: {} }]
  ports: [{ protocol: TCP, port: 443 }]
```
An empty `namespaceSelector: {}` matches **every** namespace in the cluster. This rule does not constrain egress to the kube-apiserver — it permits `lenny-ops` (and backup Jobs at line 1205) to initiate TCP 443 connections to every pod in every namespace: agent namespaces, monitoring namespaces, tenant namespaces, third-party operator namespaces. Any workload listening on TCP 443 becomes reachable.

§13.2 NET-040 (lines 215-231) introduced `kubeApiServerCIDR` specifically to scope kube-apiserver egress to the `kubernetes.default` Service ClusterIP range via `ipBlock`. That scoped mechanism is not reused in `lenny-ops` or `lenny-backup-job`, leaving `lenny-ops` with ClusterRole-backed authority **and** cluster-wide TCP 443 egress. NET-054's broader rule means compromise of `lenny-ops` lets an attacker reach any pod's 443 listener — defeating the gateway-centric containment model.

**Recommendation:** Replace `namespaceSelector: {}` with `ipBlock: { cidr: "{{ .Values.kubeApiServerCIDR }}" }` in both policies. Add a `lenny-preflight` check that rejects any Lenny-rendered NetworkPolicy rule combining an empty `namespaceSelector` with a non-localhost port. Document that `namespaceSelector: {}` is a forbidden idiom in the Lenny chart.

---

### NET-056 lenny-backup-job egress to Postgres/MinIO omits namespaceSelector, silently breaking on cross-namespace / cloud-managed storage [High]
**Files:** `25_agent-operability.md:1200-1204,1211`
```yaml
egress:
  - to: [{ podSelector: { matchLabels: { app: postgres } } }]
    ports: [{ protocol: TCP, port: 5432 }]
  - to: [{ podSelector: { matchLabels: { app: minio } } }]
    ports: [{ protocol: TCP, port: 9000 }]
```
A `to:` clause with only a `podSelector` is scoped to pods in the backup Job's own namespace. This accidentally works in the default self-managed profile where Postgres/MinIO co-locate with the Job in `lenny-system`. But §17.9 supports cloud-managed Postgres and object storage (RDS, Cloud SQL, S3, GCS, Azure Blob), and `25_agent-operability.md:1158` and the per-region backup pipeline (lines 945-960, the CMP-045 fix) explicitly deploy storage outside `lenny-system`. In those configurations the rule matches zero pods and backups fail with connection timeouts at NetworkPolicy-drop time, with no audit signal distinguishing this from Postgres/MinIO outages. Line 1211 acknowledges this informally — *"Jobs in remote-storage configurations require an additional egress rule for the storage endpoint"* — but provides no normative rendered rule or preflight.

Additionally, per-region backups (CMP-045, lines 945-960) reach regional MinIO endpoints like `minio.eu-west-1.internal:9000` that are explicitly outside the cluster; the NetworkPolicy must cover these too, via ipBlock CIDRs resolved at render time. No such coverage is rendered today.

**Recommendation:** (1) Pair each pod-selector with a `namespaceSelector: { matchLabels: { kubernetes.io/metadata.name: "{{ .Values.storage.namespace }}" } }` using the immutable key; (2) when `backups.regions` is non-empty, render one ipBlock-based egress rule per declared region endpoint (resolved to CIDR at chart-render time); (3) add a preflight check that validates every configured backup endpoint is covered by the rendered NetworkPolicy and fails the install if not.

---

### NET-057 Gateway external HTTPS egress omits RFC1918 private-range exclusions that lenny-ops webhook path applies; SSRF boundary is weaker on the higher-risk surface [Medium]
**Files:** `13_security-model.md:312-338,204`, `25_agent-operability.md:1151-1153,1156`
The gateway's `allow-gateway-egress-llm-upstream` policy (§13.2 lines 326-334) and the gateway row at line 204 grant external HTTPS as `0.0.0.0/0` with `except` limited to cluster pod/service CIDRs plus three IMDS addresses. The `lenny-ops` webhook-delivery rule at line 1152 excludes broader RFC1918 and link-local ranges:
```yaml
- to: [{ ipBlock: { cidr: 0.0.0.0/0, except: [10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16] } }]
```
The gateway is the higher-risk SSRF surface — it handles connector callback delivery, webhook notifications, external-interceptor URLs, and LLM upstream calls whose base URLs derive from tenant configuration (§4.8, §4.9). Yet it gets weaker private-range protection than `lenny-ops`. An attacker influencing a gateway-initiated URL (tenant-controlled provider base URL, webhook target, connector callback URL) can reach RFC1918 destinations — internal corporate networks reachable from the cluster via VPC peers, transit gateway attachments, on-prem links — because the gateway's `except` covers only *cluster-internal* private ranges, not RFC1918 at large. IPv6 ULA (`fc00::/7`) and link-local (`fe80::/10`) are also absent from both rules.

**Recommendation:** Align the gateway's external HTTPS `except` list with (or exceed) the `lenny-ops` webhook rule. Expose a shared Helm value (e.g., `egressCIDRs.excludePrivate`) listing `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16`, `fc00::/7`, `fe80::/10`, and render it in every `0.0.0.0/0`-based rule across §13.2 and §25.4. Document that application-layer SSRF checks are required on top of the NetworkPolicy layer.

---

### NET-058 allow-gateway-egress-interceptor-{namespace} lacks podSelector, admits gateway egress to every pod in interceptor namespace [Medium]
**Files:** `13_security-model.md:283-304`
The supplemental NetworkPolicy rendered per entry in `gateway.interceptorNamespaces` scopes gateway egress by namespace only:
```yaml
egress:
  - to:
      - namespaceSelector:
          matchLabels:
            kubernetes.io/metadata.name: "{{ namespace }}"
    ports: [{ port: "{{ .Values.gateway.interceptorGRPCPort }}", protocol: TCP }]
```
Same defect class as iter2's NET-049 (ingress-controller podSelector): namespace-only admits traffic to every pod in the interceptor namespace on the interceptor gRPC port. Any co-located workload listening on `gateway.interceptorGRPCPort` (default 50053) becomes reachable from the gateway. This matters because interceptor calls sit inside `PreLLMRequest`/`PreToolCall` decision chains (§4.8): under `failPolicy: fail-open`, a hijacked pod could silently observe the policy decision stream; under `failPolicy: fail-closed`, it could refuse all requests to deny-of-service the gateway.

**Recommendation:** Add a `podSelector` to the `to:` clause. Introduce a configurable `interceptorPodLabel` (per-namespace entry or a global `lenny.dev/role: interceptor` convention) rendered into the policy. Extend the §13.2 NET-047 normative selector statement to cover interceptor policies. Add a preflight that warns when the selected pods do not match registered interceptor endpoints.

---

### NET-059 OTLP egress permits plaintext gRPC (4317) with no TLS requirement; trace payloads expose session metadata to in-cluster interception [Medium]
**Files:** `13_security-model.md:139-168,211`, `16_observability.md:578-582`
`allow-pod-egress-otlp` routes agent trace exports on `{{ .Values.observability.otlpPort }}` default 4317 — the OpenTelemetry standard for **plaintext** OTLP/gRPC. The spec does not require TLS on the agent→collector hop and does not specify mutual authentication. OTLP spans from agent pods carry session IDs, tenant IDs, runtime/pool metadata, error messages, and — depending on instrumentation — prompt snippets, tool-call arguments, credential-lease IDs, and provider names. NET-004 (Redis/PgBouncer) acknowledged *"NetworkPolicy is L3/L4 only — it can restrict which pods reach Redis and PgBouncer but cannot enforce that connections are TLS-encrypted"* and required startup probes to validate TLS. The equivalent is absent for OTLP, so a compromised collector, a malicious pod in observability namespace, or a CNI with plaintext pod-to-pod traffic can harvest sensitive span content.

Additionally, the gateway↔pod mTLS PKI (§10.3) does not extend to OTLP; the pod does not authenticate the collector on exports.

**Recommendation:** (1) Default `observability.otlpPort` to the TLS variant and document cert-manager integration; (2) require agent-adapter startup to fail if the OTLP endpoint does not complete a TLS handshake (mirror NET-004); (3) require mTLS client auth from agent pods using the projected SA token with a collector-specific audience; (4) redact credential-sensitive attributes from span content as a normative rule on any OTel instrumentation shipped with runtimes.

---

### NET-060 Pod→gateway mTLS lacks symmetric SAN validation; intra-cluster gateway impersonation surface unspecified [Medium]
**Files:** `10_gateway-internals.md:144,239-252`
§10.3 specifies detailed gateway-side validation (pod SPIFFE URI, audience claim on SA token, Token Service DNS SAN outbound, gateway DNS SAN inbound at Token Service). The pod→gateway direction is not symmetrically specified — line 144 describes mTLS + projected SA token but the spec does not state:
- Pod validates gateway DNS SAN (`lenny-gateway.lenny-system.svc`) per connection.
- Pod rejects certificates from the cluster CA that do not match the gateway's expected SAN.
- Pod pins the `global.spiffeTrustDomain` on gateway replicas.

Consequence: a principal able to obtain a certificate from the same cluster CA (misconfigured cert-manager Issuer signing an unrelated service, a tenant-operated Certificate resource landing in a namespace with ClusterIssuer access) could stand up a gateway impersonator and accept pod connections. The pod's audience-bound SA token blocks direct replay to the real gateway, but the token and the pod's SPIFFE client certificate are still harvested on the inbound handshake for offline analysis or correlation.

**Recommendation:** Add an explicit paragraph to §10.3 mirroring line 246's outbound validation: *"The agent pod and runtime adapter validate the gateway's DNS SAN (`lenny-gateway.lenny-system.svc`) on every mTLS connection and reject any certificate not matching this SAN. When `global.spiffeTrustDomain` is configured, the pod validates the gateway's SPIFFE URI against the trust domain."* Require pod-side implementations to set `tls.Config.VerifyPeerCertificate` rather than relying solely on `ServerName` default verification. Add a startup probe that fails the pod if the initial handshake does not validate the gateway SAN.

---

## Summary

Ten new findings dominated by **selector-hygiene and port-matrix gaps outside the §13.2 core allow-list**, which iter2 standardised. The common pattern: the NET-047/050 fix committed a normative selector rule for `lenny-system` component NetworkPolicies, but the `lenny-ops` chart and the `lenny-backup-job` NetworkPolicy live outside that normative scope, replicate every defect class the fix addressed (wrong label key, missing pod selector, wrong port, unscoped namespace selectors), and additionally have **no counterparty ingress rows** in §13.2 (NET-051). In a correctly installed cluster today, `lenny-ops` cannot reach the gateway admin API, Redis, or MinIO because of port/label/allow-list regressions — a Critical operability break that also blocks backup, restore, upgrade orchestration, diagnostics, and every other operability surface.

Orthogonal gaps: gateway external-HTTPS egress is weaker than `lenny-ops` webhook egress on the same SSRF threat model (NET-057); interceptor egress repeats iter2's NET-049 defect on the egress side (NET-058); OTLP agent→collector exports lack TLS/mTLS (NET-059); and pod→gateway mTLS lacks symmetric SAN validation (NET-060).

**Total findings: 10** (1 Critical, 5 High, 4 Medium)
