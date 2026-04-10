# Network Security & Isolation Review — 2026-04-04

**Document reviewed:** `docs/technical-design.md` (5,277 lines)
**Perspective:** 3. Network Security & Isolation
**Category code:** NET
**Prior findings (already addressed):** NET-001 through NET-014 from `review-findings-20260404.md`

This review begins at NET-015 to avoid re-numbering collisions with the existing findings document. Findings below are new, post-fix observations arising from a fresh close read of the updated spec. Some reference prior fixed findings where the fix introduced a new gap.

---

## Findings Summary

| Severity | Count |
|----------|-------|
| Critical | 1 |
| High     | 4 |
| Medium   | 6 |
| Low      | 3 |
| Info     | 2 |

---

### NET-015 LLM Proxy URL Uses Plain HTTP in Spec Example [Critical] — VALIDATED/FIXED
**Section:** 4.9

The spec example for proxy-mode credential delivery reads:

```
http://gateway-internal:8443/llm-proxy/{lease_id}
```

and the YAML pool configuration example also uses `http://`:

```yaml
proxyEndpoint: http://gateway-internal:8443/llm-proxy
```

Port 8443 strongly implies TLS intent, but the scheme is `http://`, not `https://`. The proxy mode's entire security argument rests on the real API key never leaving the gateway. If the pod contacts the proxy over cleartext HTTP, the lease token — and in turn the traffic content including LLM prompts — transits the pod network unencrypted. A network-adjacent pod (or a compromised CNI node agent) can intercept the lease token and impersonate the pod against the LLM proxy for the duration of the lease TTL. This directly undermines the stronger security guarantee that makes proxy mode worth using in the first place. Because the spec labels proxy mode the "recommended default for multi-tenant deployments," this is the common path, not an edge case.

**Recommendation:** Change the scheme to `https://` in both the descriptive text and the YAML example. Add an explicit statement that the pod-to-gateway LLM proxy path is protected by the same mTLS channel as the gRPC control connection (or is at minimum TLS with server-side authentication using the gateway's cert). The `proxyEndpoint` field validation must reject `http://` URLs at pool registration time, or emit a hard warning that blocks pool creation in multi-tenant mode.

**Resolution:** All `proxyEndpoint` URIs in Section 4.9 were changed from `http://` to `https://`. The proxy endpoint shares the same mTLS certificate infrastructure as the internal gRPC control plane (Section 4.3), so lease tokens are encrypted in transit. The controller rejects pool registrations where `proxyEndpoint` uses `http://` with a validation error (`InvalidProxyEndpointScheme`). Binding lease tokens to the pod's SPIFFE URI (preventing cross-pod replay) is documented as a post-v1 hardening opportunity — not implemented in v1 because the token is only visible inside the sandboxed pod (tmpfs, mode 0400).

---

### NET-016 `allow-gateway-ingress` Policy Omits Port Restriction, Allows Any Inbound Port [High] — Fixed
**Section:** 13.2

The existing finding NET-004 addresses agent pod *egress* to the gateway lacking port constraints. The ingress NetworkPolicy has the same problem in the opposite direction — the `allow-gateway-ingress` manifest permits gateway pods to reach agent pods on *any* port:

```yaml
ingress:
  - from:
      - namespaceSelector: ...
        podSelector:
          matchLabels:
            lenny.dev/component: gateway
policyTypes: [Ingress]
```

No `ports` stanza is present. The adapter listens on a single gRPC port. Without a port restriction, a compromised or misconfigured gateway can open connections to any port on any agent pod — including any debug or auxiliary listener that might be present in the container image. In a multi-tenant scenario, an attacker who compromises a single gateway replica can attempt connections to all ports on all agent pods, using the gateway's identity to pass the NetworkPolicy check.

**Recommendation:** Add an explicit `ports` stanza to `allow-gateway-ingress` restricting ingress to the adapter's declared gRPC port (e.g., `port: 50051, protocol: TCP`). The adapter gRPC port should be a named Helm value (`adapterGrpcPort`, default 50051) so it remains consistent across the NetworkPolicy, the adapter's containerPort declaration, and the gateway's dial address.

**Resolution:** Added a `ports` stanza to the `allow-gateway-ingress` NetworkPolicy in Section 13.2, restricting ingress to TCP port 50051 with a Helm template comment (`{{ .Values.adapter.grpcPort }}`). This mirrors the egress port constraint pattern and ensures the gateway can only reach the adapter's declared gRPC listener.

---

### NET-017 No NetworkPolicy for `lenny-system` Namespace — Fixes to NET-011 Are Not in the Spec [High] — Fixed
**Section:** 13.2

NET-011 (from the prior review) called for least-privilege egress NetworkPolicies in `lenny-system`. The fix is listed in the existing findings document as *not fixed* (no `FIXED` tag). Confirming by reading the current spec: Section 13.2 describes three NetworkPolicy manifests all scoped to `lenny-agents` (and by fix to `lenny-agents-kata`). There are zero NetworkPolicy manifests for `lenny-system`.

This means:
- The Token/Connector Service (holding decrypted OAuth tokens and KMS-derived keys) has unrestricted egress. A compromised Token Service process can exfiltrate credentials to any IP on the internet.
- The gateway Deployment can reach any service in any namespace, including Postgres directly (bypassing PgBouncer), MinIO, Redis, the Kubernetes API server, and cloud metadata endpoints.
- The Warm Pool Controller and PoolScalingController have unrestricted network access.

In the worst case, a single compromised component in `lenny-system` has a lateral-movement path to every other component and to the external internet.

**Recommendation:** Add at minimum two NetworkPolicy manifests for `lenny-system` to the Helm chart and spec:

1. `default-deny-all` for `lenny-system` (identical structure to the agent namespace version).
2. Component-specific allow-lists:
   - Token Service: egress to Postgres (via PgBouncer port), Redis, KMS endpoint only.
   - Gateway: egress to agent namespaces (gRPC adapter port), Token Service, PgBouncer, Redis, MinIO, external ingress (inbound), and the Kubernetes API server. Block 169.254.169.254 and other cloud metadata endpoints explicitly.
   - Controllers: egress to Kubernetes API server and Postgres only.

Include these manifests in the Helm chart alongside the agent namespace policies.

**Resolution:** Added a `default-deny-all` NetworkPolicy manifest for `lenny-system` (identical structure to the agent namespace version) and a component-specific allow-list table in Section 13.2. The table documents permitted egress and ingress for each `lenny-system` component (gateway, Token Service, controllers, dedicated CoreDNS) using `lenny.dev/component` label selectors. Cloud metadata endpoints (`169.254.169.254/32`) are explicitly blocked. Full YAML manifests per component are left to the Helm chart implementation — the spec provides the canonical traffic matrix.

---

### NET-018 Dedicated CoreDNS Has No Documented HA Requirement or Failure Mode [High] — Fixed
**Section:** 13.2

The dedicated CoreDNS instance in `lenny-system` is the single authorized DNS resolver for all agent pods in all agent namespaces. It is a single point of failure that can affect every agent session simultaneously. The spec documents its security properties (query logging, rate limiting, response filtering) but says nothing about:

- How many replicas it runs.
- Whether it has a PodDisruptionBudget.
- What happens when it is unavailable — do agent pods fail DNS resolution entirely, or does the NetworkPolicy allow a fallback to `kube-system` CoreDNS?
- Whether the `dnsPolicy: cluster-default` opt-out for `standard` isolation is the only fallback, and if so, whether it removes the security properties or just the custom DNS routing.

An attacker who can cause the dedicated CoreDNS pod to crash (e.g., by sending high-volume malformed queries from a compromised agent — a denial-of-service against the DNS server itself) can potentially disrupt all agent pods that depend on DNS for connecting to the gateway, triggering cascading session failures.

**Recommendation:** Specify that the dedicated CoreDNS instance runs with a minimum replica count of 2 (or 3 for Tier 2+), has a PodDisruptionBudget (`minAvailable: 1`), and that its pods are spread across zones via topology spread constraints. Document that if all dedicated CoreDNS replicas are unavailable, agent pods lose DNS resolution — this should trigger a `DedicatedDNSUnavailable` critical alert. Add the dedicated CoreDNS deployment spec to the Helm chart. Add per-pod query rate limiting at the NetworkPolicy level (Cilium `CiliumNetworkPolicy` with L7 DNS rate limits, or equivalent) as a complement to the application-level rate limiting inside CoreDNS.

**Resolution:** Added a "Dedicated CoreDNS high availability" block to Section 13.2 specifying: minimum 2 replicas (Helm-configurable, validated >= 2), a PodDisruptionBudget with `minAvailable: 1`, and explicit failure-mode documentation — agent pods lose DNS resolution entirely when all replicas are down (no silent fallback to `kube-system` CoreDNS, which would bypass security controls). Two alerts are specified: `DedicatedDNSUnavailable` (critical, zero ready replicas) and `DedicatedDNSDegraded` (warning, below configured minimum). The `dnsPolicy: cluster-default` opt-out paragraph now explicitly states that opting out removes security properties. Topology spread constraints and Cilium L7 DNS rate limits were not added — topology spread is a Helm chart implementation detail, and application-level rate limiting inside CoreDNS already addresses the DoS vector.

---

### NET-019 Token Service Certificate Not in mTLS PKI Table — Identity Gap [High] — Fixed
**Section:** 10.3

The mTLS PKI table (Section 10.3) documents certificate TTLs and SAN formats for three components: gateway replicas, agent pods, and the controller. The Token/Connector Service is absent. Yet the spec states that gateway replicas call the Token Service over mTLS. This creates an undocumented certificate that:

- Has no specified TTL (could be long-lived by accident of implementation).
- Has no defined SAN format (could use a plain DNS SAN or could mirror the gateway format).
- Has no rotation procedure in the CA rotation section.
- Has no `CertExpiryImminent` alert wiring.

A Token Service certificate with an unspecified TTL might be long-lived (e.g., a manually created cert used during initial development), turning it into a high-value credential that compromises the highest-privilege component.

**Recommendation:** Add a row to the mTLS PKI table for the Token/Connector Service:

| Component | Certificate TTL | SAN Format | Rotation |
|---|---|---|---|
| Token/Connector Service | 24h | DNS: `lenny-token-service.lenny-system.svc` | cert-manager auto-renewal at 2/3 lifetime |

Also: the gateway should validate the Token Service certificate's SAN on every connection (not just accept any cert from the cluster CA). The Token Service should validate incoming connections are from gateway replicas by matching the gateway's SPIFFE-style SAN or DNS SAN. Document this mutual validation requirement.

**Resolution:** Added a Token/Connector Service row to the mTLS PKI certificate lifecycle table in Section 10.3 with 24h TTL, DNS SAN `lenny-token-service.lenny-system.svc`, and cert-manager auto-renewal. Added a "Token Service identity" paragraph documenting mutual SAN validation: the gateway validates the Token Service's DNS SAN on every connection, and the Token Service validates that incoming connections present the gateway's DNS SAN. Updated the CA rotation procedure to include Token Service trust bundles alongside gateway and controller. The `CertExpiryImminent` alert already covers the Token Service implicitly ("any certificate within 1h of expiry").

---

### NET-020 `allow-pod-egress-base` Permits DNS to CoreDNS on TCP/53 Without Necessity [Medium]
**Section:** 13.2

The egress policy permits both `UDP/53` and `TCP/53` to the dedicated CoreDNS instance. Standard DNS uses UDP/53 with TCP/53 only as a fallback for large responses (> 512 bytes in traditional DNS, > 4096 bytes with EDNS). The spec states that response filtering "blocks TXT records exceeding a size threshold" and "drops unusual record types commonly used for DNS tunneling." If response filtering is working correctly, large responses triggering TCP fallback should be rare to nonexistent. However, TCP/53 remains an attack surface: it allows a compromised agent pod to establish a persistent TCP connection to the CoreDNS pod and use it as a data channel (DNS-over-TCP does not have the same per-query overhead as UDP). A determined attacker can still exfiltrate data via TCP/53 at higher throughput than UDP/53 per-connection.

**Recommendation:** Remove `TCP/53` from the `allow-pod-egress-base` policy. Standard DNS resolution works correctly over UDP/53. If a deployer genuinely needs TCP/53 (e.g., for DNSSEC or unusually large zone responses), they can add an explicit override in their Helm values with a comment explaining the risk trade-off. Document the decision.

---

### NET-021 `internet` Profile Egress Policy Manifest Not Shown — Deployers Have No Concrete Guidance [Medium]
**Section:** 13.2

The spec explains the `internet` egress profile semantics clearly in prose, including the `except` clause requirement for cluster CIDRs. However, unlike the three base NetworkPolicy manifests which are shown as complete YAML, there is no example YAML manifest for the `internet` profile's additional NetworkPolicy. The prose references `egressCIDRs.excludeClusterPodCIDR` and `egressCIDRs.excludeClusterServiceCIDR` Helm values, but without a concrete manifest, deployers writing or reviewing their Helm configuration have no canonical reference. Implementations may incorrectly omit the `except` clauses, re-creating the NET-002 lateral movement vector.

**Recommendation:** Add an example YAML manifest for the `allow-internet-egress` NetworkPolicy, similar to the existing three manifests, showing:
- `podSelector` matching `lenny.dev/egress-profile: internet`
- The `0.0.0.0/0` CIDR with `except` clauses for the pod and service CIDRs
- `policyTypes: [Egress]`
- The Helm template comment indicating the `except` values are populated from `egressCIDRs.excludeClusterPodCIDR` and `egressCIDRs.excludeClusterServiceCIDR`.

---

### NET-022 Setup Phase Network Block Not Enforced When Pool Uses Non-`restricted` Egress [Medium]
**Section:** 7.5, 13.2

This is a deeper examination of the gap identified as NET-010 (not marked fixed). Section 7.5 states: "Network **blocked by default** during setup (static NetworkPolicy; no dynamic toggling which would require NET_ADMIN)." This claim is only true for the `restricted` egress profile. For pools with `provider-direct` or `internet` egress profiles, the same NetworkPolicy that permits internet access during active session time is already active during the setup phase — because Kubernetes labels are set at pod creation, and the pool's egress profile label is set before any session begins.

The spec does not describe any mechanism to temporarily restrict egress during setup. The phrase "static NetworkPolicy; no dynamic toggling" confirms this is a design choice, but it is presented as a security property ("blocked by default") without the important qualifier that this applies only to `restricted` pools.

This means: for an `internet`-profile pool, a `setupCommandPolicy` in `blocklist` mode (which the spec acknowledges is "not a security boundary") combined with internet egress access during setup allows a sufficiently motivated attacker to exfiltrate data or download additional payloads during the setup phase, before the session is even active.

**Recommendation:** Correct the description in Section 7.5 to read: "Network is blocked during setup for `restricted` egress profile pools. For `provider-direct` and `internet` profile pools, the pool's egress policy is active during setup." Add an explicit recommendation that `internet`-profile pools should use `setupCommandPolicy: { mode: allowlist }` (not blocklist) to compensate for the unrestricted network access during setup, since the allowlist mode is the stronger control.

---

### NET-023 SPIFFE Trust Domain Is `lenny` — Collides Across Multi-Tenant Deployments [Medium]
**Section:** 10.3

The SPIFFE URI format for agent pods is `spiffe://lenny/agent/{pool}/{pod-name}`. The trust domain is hardcoded as `lenny`. In a multi-cluster or multi-tenant deployment where two separate Lenny installations share the same cluster CA (which is possible if a platform team runs multiple Lenny instances on one cluster), SPIFFE URIs from one installation would be accepted as valid by the gateway of another installation. The gateway validates the SPIFFE URI "against the expected pool/pod" — but it validates pool/pod name, not the trust domain, since the trust domain is always `lenny`.

The scenario is unlikely in the current single-cluster design but becomes a real concern as soon as section 21.7 (Multi-Cluster Federation) is pursued, or when a platform team deploys both a staging and production Lenny on the same cluster.

**Recommendation:** Make the SPIFFE trust domain deployment-specific. Replace the hardcoded `lenny` with a Helm value (e.g., `global.spiffeTrustDomain`, default `lenny`). The SAN format becomes `spiffe://{spiffeTrustDomain}/agent/{pool}/{pod-name}`. The gateway validates that the trust domain in the presented certificate matches its configured trust domain, rejecting certificates from a different Lenny instance. Document this in the CA rotation procedure and in the multi-cluster federation design note (Section 21.7).

---

### NET-024 `allow-gateway-ingress` Uses `lenny.dev/managed: "true"` — Insufficient Pod Selector [Medium]
**Section:** 13.2

The `allow-gateway-ingress` ingress policy's pod selector uses:

```yaml
podSelector:
  matchLabels:
    lenny.dev/managed: "true"
```

This label is presumably applied to all Lenny-managed agent pods. However, the label alone does not distinguish between pools, states, or tenants. If multiple pools of different egress profiles or isolation levels are in the same agent namespace, the ingress policy applies uniformly to all of them. More critically, if any other workload in `lenny-agents` carries `lenny.dev/managed: "true"` (perhaps a debugging or monitoring sidecar), it receives the same ingress access from gateway pods.

The other two NetworkPolicy manifests (`default-deny-all` and `allow-pod-egress-base`) also use `lenny.dev/managed: "true"` as the pod selector, which is consistent. But the label value is not cross-referenced with the label enforcement mechanism — the spec does not state that `lenny.dev/managed: "true"` is applied *only* by the warm pool controller and *only* to agent pods, and that a Kyverno/Gatekeeper policy prevents unauthorized pods from carrying this label.

**Recommendation:** Add a Kyverno/Gatekeeper admission policy that prevents non-controller-created pods in `lenny-agents` and `lenny-agents-kata` namespaces from carrying the label `lenny.dev/managed: "true"`. This ensures the NetworkPolicy selector cannot be spoofed by manually created pods. Also mention this in Section 13.2 alongside the existing note about immutable `kubernetes.io/metadata.name` labels.

---

### NET-025 mTLS PKI Has No Specified TLS Version or Cipher Suite Floor [Low]
**Section:** 10.3

The mTLS PKI section specifies certificate TTLs, SAN formats, rotation schedules, and revocation procedures in thorough detail. It does not specify:

- Minimum TLS version (TLS 1.2 vs TLS 1.3).
- Acceptable cipher suites (to prevent weak ciphers being negotiated).
- Whether the cert-manager issuer configuration must disable SHA-1 or other weak signature algorithms.

Without a minimum TLS version floor, a misconfigured client could negotiate TLS 1.0 or 1.1 on the mTLS channel, exposing the connection to known protocol-level attacks (BEAST, POODLE, etc.). This is a low-severity gap because modern Go TLS defaults are reasonable (TLS 1.2+ with strong ciphers), but the spec makes no guarantee.

**Recommendation:** Add a brief line to Section 10.3: "All mTLS connections must negotiate TLS 1.2 or higher. The gateway's TLS configuration must disable TLS 1.0 and 1.1 (`tls.VersionTLS12` as `MinVersion` in Go's `tls.Config`). Prefer TLS 1.3 where both peers support it. Cipher suites for TLS 1.2 are restricted to ECDHE-RSA/ECDSA with AES-GCM or ChaCha20-Poly1305." This is implementable with a single `tls.Config` struct and prevents spec/implementation drift.

---

### NET-026 Prometheus Scraping of Agent Pod Metrics Requires Ingress Not Covered by NetworkPolicies [Low]
**Section:** 13.2, 16

The spec does not describe whether agent pods expose a Prometheus metrics endpoint. However, if they do (even adapter-level Go runtime metrics), Prometheus scraping requires the Prometheus pod to initiate an inbound connection to the agent pod on a metrics port (typically `9090` or `8080`). This inbound connection would be blocked by the `default-deny-all` and `allow-gateway-ingress` NetworkPolicies, which only permit ingress from gateway pods.

The spec also mentions `lenny_checkpoint_duration_seconds` histograms and other adapter-level metrics, which would typically be pushed (via OTLP push or Prometheus PushGateway) rather than scraped — but this is not explicitly stated. If the implementation uses a pull model, the NetworkPolicies will silently drop all scrape attempts, leading to gaps in adapter-level observability without any error surfacing.

**Recommendation:** Explicitly state in Section 13.2 or 16 whether adapter pods expose a scrape endpoint. If they do, add a NetworkPolicy for Prometheus scraping:

```yaml
ingress:
  - from:
      - namespaceSelector:
          matchLabels:
            kubernetes.io/metadata.name: lenny-monitoring
        podSelector:
          matchLabels:
            app: prometheus
    ports:
      - protocol: TCP
        port: 9090
```

If the implementation uses OTLP push (preferred for agent pods under strict ingress policy), state this explicitly so implementers do not accidentally build a pull endpoint.

---

### NET-027 `provider-direct` Egress CIDR Maintenance Is Manual and Drift-Prone [Low]
**Section:** 13.2

The `provider-direct` egress profile relies on CIDR ranges maintained in Helm values (`egressCIDRs.providers`). These CIDRs cover LLM provider endpoints (AWS Bedrock, Vertex AI, Azure OpenAI, Anthropic). Cloud provider IP ranges change periodically (AWS publishes changes to `ip-ranges.json`, GCP to `_cloud-netblocks.googleusercontent.com`). Manual maintenance of these ranges introduces operational drift: ranges may become stale, causing LLM calls to fail when providers add new IPs; or they may remain too broad after providers consolidate ranges, over-permitting egress.

This is a low severity gap because the failure mode (stale CIDR causing broken LLM calls) is visible and not a security regression — a missing CIDR causes a denied outbound connection, not an unintended allowed one. However, the inverse is also possible: if an operator responds to a broken CIDR by broadening to a /8 or larger block, the effective egress restriction degrades.

**Recommendation:** Document the CIDR maintenance process in the operations runbook: where to find the authoritative source for each supported provider's IP ranges, how frequently to update them (at minimum, subscribe to provider IP change notifications), and that a CronJob or CI pipeline should automate range updates into the Helm values and re-apply the NetworkPolicies. Consider adding an explicit warning in the Helm chart comment for `egressCIDRs.providers`: "These values require periodic manual update as provider IP ranges change. See docs/runbooks/egress-cidr-maintenance.md."

---

### NET-028 Deny-List Bootstrap on Gateway Restart Is Unspecified [Info]
**Section:** 10.3

The cert revocation deny-list is described as being propagated across gateway replicas via Redis pub/sub, with Postgres `LISTEN/NOTIFY` as fallback. NET-006 in the prior review (not fixed) called out unreliable delivery. This review notes a related gap: the spec does not describe how a freshly started or restarted gateway replica populates its in-memory deny-list. If the Redis pub/sub channel carries only incremental updates (new additions), a gateway restart with an empty deny-list will allow connections from any previously revoked certificate until the next revocation event is published — which may never happen if no new revocations occur.

**Recommendation:** Specify that on startup, each gateway replica performs a one-time bulk load of active deny-list entries from Postgres before opening its listener port. The Postgres deny-list table should store all entries whose `certificate TTL expiry` timestamp is in the future. This ensures the in-memory deny-list is always accurate from the first request, regardless of what pub/sub events were missed during downtime.

---

### NET-029 No Explicit Statement That Internal `lenny-system` Services Use mTLS Among Themselves [Info]
**Section:** 10.2, 10.3

Table 10.2 documents authentication at the "Gateway ↔ Pod" and "Pod → Gateway" boundaries. The mTLS PKI section (10.3) documents certificates for gateway replicas, agent pods, and the controller. The Token Service's certificate is absent (covered in NET-019). Beyond those specifics, the spec does not explicitly state whether gateway-to-Postgres (via PgBouncer), gateway-to-Redis, gateway-to-MinIO, or Token-Service-to-KMS communications are encrypted in transit.

Section 13.4 (data classification) states "mTLS" for Tier 2 and higher, but this is in the context of the data classification tiers, not a definitive statement about internal service connections. Section 12.3 (Redis) states "Redis AUTH (ACLs) and TLS are **required**." Postgres TLS is not mentioned. MinIO TLS is not mentioned.

**Recommendation:** Add a brief "Internal service communication" row to Table 10.2 or a short paragraph in Section 10.3 listing the encryption requirement for each internal service connection: gateway → PgBouncer (TLS required), PgBouncer → Postgres (TLS required), gateway → Redis (TLS required per 12.3), gateway → MinIO (TLS required), gateway → Token Service (mTLS), Token Service → KMS (provider-native TLS). This prevents implementers from assuming that cluster-internal connections are safe without TLS.
