# Iteration 7 — Perspective 3: Network Security & Isolation

**Reviewer scope:** Gateway-centric network perimeter; internal segmentation across `lenny-system`, agent namespaces, storage, monitoring, and operability planes; NetworkPolicy manifests (default-deny + per-component allow-lists); lateral-movement risks; dedicated CoreDNS practicality and opt-outs; gateway-bypass paths; mTLS PKI sufficiency without a service mesh; SSRF posture symmetry between the two `lenny-system` surfaces that dial tenant-influenced URLs (gateway LLM-upstream and `lenny-ops` webhooks); CIDR drift and dual-family ipBlock correctness; IMDS blocking coverage for AWS/GCP/Azure/Alibaba.

**Inputs reviewed:**
- `/Users/joan/projects/lenny/spec/13_security-model.md` §13.1–§13.4 (network isolation, DNS, credential flow, upload security)
- `/Users/joan/projects/lenny/spec/10_gateway-internals.md` §10.3 (mTLS PKI)
- `/Users/joan/projects/lenny/spec/25_agent-operability.md` §25.4 (lenny-ops-egress, blockedCIDRs, TLS)
- `/Users/joan/projects/lenny/spec/16_observability.md` (TLS handshake metrics, plaintext alerts, NetworkPolicy drift)
- `/Users/joan/projects/lenny/spec/17_deployment-topology.md` §17.9 (lenny-preflight check inventory)
- `/Users/joan/projects/lenny/spec-reviews/review-findings/20260419000406/iter5/perspective-3-network-security.md` (iter5 baseline: NET-069…NET-072)
- `/Users/joan/projects/lenny/spec-reviews/review-findings/20260419000406/iter6/p3_networking.md` (deferred for rate-limit; no new findings recorded)

Iter6 P3 was deferred due to Anthropic API rate-limit exhaustion, so iter5 is the effective baseline. Severity calibration follows `feedback_severity_calibration_iter5.md`: iter5 closed with 0 Critical / 0 High / 1 Medium (NET-070, fixed) / 3 Low; iter7 is expected to continue the post-convergence trajectory. Category: **NET**. IDs continue iter5 numbering at **NET-073+**.

---

## Prior-iteration carry-forwards

### NET-069. OTLP egress `podSelector` allows operator override (Low, carry-over from iter4) — FIXED

**Section:** §13.2 (OTLP egress, lines 157–171; table row at line 223)

The iter5 review flagged the OTLP collector egress rule as hardcoding `app: otel-collector`. The current spec renders:

```yaml
podSelector:
  matchLabels:
    "{{ .Values.observability.otlpPodLabel }}": "{{ .Values.observability.otlpPodLabelValue }}"
```

with default `app: otel-collector`. The §13.2 NetworkPolicy table row (line 223) matches this templating. Operators who deploy upstream OTLP distributions (e.g., `app.kubernetes.io/name: opentelemetry-collector`) can override via Helm values. **Status: Fixed.**

### NET-070. Plaintext lenny-ops admin-API over `lenny-system` (Medium, iter5) — FIXED

**Section:** §25.4 (TLS subsection, lines 2536–2546); §13.2 (gateway ingress row at line 216); §17.9 (preflight `ops-admin-tls` check at line 484); §16.1/§16.5 (`lenny_ops_admin_api_tls_handshake_total`, `OpsAdminAPIPlaintextDetected` alert)

The iter5 fix is fully rendered:
- `ops.tls.internalEnabled: true` is the default in all non-dev profiles; GatewayClient dials `https://lenny-gateway:8443` (§25.4 line 2540; §10.3 `GatewayClient` interface at §25.4 line 1858).
- Plaintext opt-out requires both `ops.tls.internalEnabled: false` AND `ops.acknowledgePlaintextAdminAPI: true`; chart `required` guard blocks `helm install` on the unsafe combination outside dev (§25.4 line 2544).
- `lenny-preflight` runs an `ops-admin-tls` handshake probe with SAN check against `lenny-gateway` ClusterIP (§17.9 line 484).
- Runtime `lenny_ops_admin_api_tls_handshake_total{result}` metric emits per-request; `OpsAdminAPIPlaintextDetected` critical alert is symmetric with `OTLPPlaintextEgressDetected` (OTLP-068).
- Gateway ingress rule (§13.2 table line 216) renders only the TLS port by default; plaintext port only when the acknowledged opt-out is set.
- Counterparty-rule audit (§13.2 line 226) verifies that the `lenny-ops` egress port and gateway ingress port are both the TLS port by default.

**Status: Fixed.**

### NET-071. Shared exclusion list maintained in two copies — `lenny-ops-egress` hardcodes what gateway rule templatizes (Low, iter5) — PARTIAL

**Section:** §25.4 `lenny-ops-egress` (lines 1291–1304) vs §13.2 `allow-gateway-egress-llm-upstream` (lines 351–411)

The gateway `allow-gateway-egress-llm-upstream` rule (§13.2) renders RFC1918 / link-local / ULA / IMDS entries via `{{- range .Values.egressCIDRs.excludePrivate }}` (split by address family at template time) and IMDS via explicit literal entries that, together with the narrative at §13.2 line 450, are stated to be sourced from `egressCIDRs.excludeIMDS`. The `lenny-ops-egress` rule in §25.4 still renders hardcoded literals:

```yaml
- to: [{ ipBlock: { cidr: 0.0.0.0/0, except: [
    "{{ .Values.egressCIDRs.excludeClusterPodCIDR }}",
    "{{ .Values.egressCIDRs.excludeClusterServiceCIDR }}",
    10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16,
    169.254.169.254/32, 100.100.100.200/32
  ] } }]
```

The prose above (§25.4 lines 1277–1287 and 1307) explicitly claims the two rules share one block list via `egressCIDRs.excludePrivate` and `egressCIDRs.excludeIMDS`, but the rendered YAML contradicts this. Partial mitigation: `lenny-preflight`'s "Gateway/lenny-ops private-range parity" check (§17.9 line 493) validates `excludePrivate` membership on both rules' `except` blocks at install time — this fails the install if a deployer overrides `excludePrivate` without also updating the hardcoded literals.

**Status: Not fully fixed.** The install-time parity check is fail-closed and catches the common case, but (1) the spec YAML still shows two representations of "the same" list, inviting future edits to drift them, and (2) the parity check covers `excludePrivate` only — see **NET-073** below for the `excludeIMDS` gap. Carrying forward at original Low severity.

### NET-072. `blockedCIDRs` default omits IMDS addresses (Low, iter5) — UNFIXED

**Section:** §25.4 Helm values (lines 908–920)

The `ops.webhooks.blockedCIDRs` default list mirrors `egressCIDRs.excludePrivate` only:

```yaml
blockedCIDRs:
  - "10.0.0.0/8"
  - "172.16.0.0/12"
  - "192.168.0.0/16"
  - "169.254.0.0/16"
  - "fc00::/7"
  - "fe80::/10"
```

It does **not** include the IMDS entries (`169.254.169.254/32`, `100.100.100.200/32`, `fd00:ec2::254/128`) that `egressCIDRs.excludeIMDS` carries. The comment at line 911 claims "Default list mirrors `egressCIDRs.excludePrivate`" — which is true but misses that the symmetric NetworkPolicy surfaces (gateway LLM-upstream and lenny-ops-egress) each carry `excludePrivate` **plus** `excludeIMDS`. The `169.254.0.0/16` entry does reject the IPv4 IMDS addresses by range containment, but:
1. Alibaba's `100.100.100.200` lives outside `169.254.0.0/16` and is therefore **NOT** rejected by the app-layer SSRF check.
2. The IPv6 IMDS address `fd00:ec2::254` is inside `fc00::/7` so it is rejected — but only incidentally, by a broad ULA range; a deployer who narrows `fc00::/7` to a tighter list (e.g., to allow specific ULA callbacks) would unknowingly re-expose the AWS IPv6 IMDS.

**Status: Unfixed.** Carrying forward at original Low severity.

---

## New findings (iter7)

### NET-073. `lenny-preflight` parity check covers `excludePrivate` only, not `excludeIMDS` [Low]

**Section:** §17.9 (line 493 "Gateway/lenny-ops private-range parity"); §13.2 line 416 (NET-057 normative note) and §25.4 line 1307 (ops-egress narrative)

The preflight "Gateway/lenny-ops private-range parity" check, as documented, enforces that every entry in `egressCIDRs.excludePrivate` appears in the `except` block of both `allow-gateway-egress-llm-upstream` and `lenny-ops-egress`. It is limited to `excludePrivate` — the check's text explicitly states "the check is limited to `excludePrivate` membership on both sides."

The narrative above the `lenny-ops-egress` YAML (§25.4 lines 1286–1287) and the §13.2 NET-057/NET-065 note (line 416) require the same symmetry for `egressCIDRs.excludeIMDS` ("the same `egressCIDRs.excludeIMDS` values that the gateway rule renders into its `except` block MUST also appear in the `except` block of the `lenny-ops-egress` webhook rule"). However, because `lenny-ops-egress` hardcodes the two canonical IPv4 IMDS entries (`169.254.169.254/32`, `100.100.100.200/32`) while the gateway rule uses a Helm `{{- range .Values.egressCIDRs.excludeIMDS }}` shape (per the narrative at §13.2 line 450 and the v6 peer at line 408), a deployer who extends `excludeIMDS` to cover additional cloud IMDS ranges (e.g., Oracle Cloud `192.0.0.192`, IBM Cloud `161.26.0.0/16`) will silently get the new entries on the gateway rule but not on `lenny-ops-egress`. A webhook URL resolving into the extended IMDS range from a compromised operability-plane pod would then egress unblocked at L3/L4, defeating the SSRF parity guarantee that NET-057/NET-065 document.

**Recommendation:** Either (a) extend the preflight parity check to cover `egressCIDRs.excludeIMDS` with the same same-family membership rule, or (b) remove the hardcoded IMDS literals from `lenny-ops-egress` and render them via `{{- range .Values.egressCIDRs.excludeIMDS }}` per the gateway rule's shape. Option (b) is the lower-maintenance fix and aligns with the normative statement at §25.4 line 1307.

### NET-074. `lenny-ops-egress` `excludePrivate` literals are hardcoded in spec YAML — Helm-value override reaches only gateway rule at render time [Low]

**Section:** §25.4 (lines 1291–1304 IPv4 block; lines 1298–1304 IPv6 block) vs §13.2 (lines 351–411)

Same-family observation as NET-071 but narrowed to the Helm-templating behavior: the gateway rule renders from `.Values.egressCIDRs.excludePrivate` with `{{- range }}`, the ops-egress rule shows literals. The spec YAML is the normative reference for chart authors implementing `templates/lenny-ops-egress.yaml`. As written, a chart implementation that follows §25.4's YAML verbatim will:
1. Not propagate deployer overrides of `egressCIDRs.excludePrivate` to `lenny-ops-egress` (deployer changes land on gateway rule only).
2. Rely on `lenny-preflight`'s parity check (§17.9 line 493) to fail the install on any drift — functionally safe, but the preflight fires at install/upgrade time, not at `helm template` / dry-run review time. GitOps pipelines that run `helm template` and diff will see the inconsistency as normal, not as an error.

This is the installation-time analogue of the symmetry promise at §13.2 line 416 ("any override applies uniformly to both surfaces"). The promise is satisfied by preflight, but not by the rendered manifests themselves.

**Recommendation:** Rewrite the `lenny-ops-egress` YAML in §25.4 to use the same `{{- range .Values.egressCIDRs.excludePrivate }}` shape as §13.2. This removes the two-representation inconsistency and upgrades the override path from "fail-closed at preflight" to "correct-by-construction." The IPv6 peer already uses `{{- with .Values.egressCIDRs.excludeClusterPodCIDRv6 }}` conditionals — the change would follow the same template idiom.

### NET-075. `blockedCIDRs` uses plain Helm list while NetworkPolicy `except` blocks use `egressCIDRs.excludePrivate` — two names for one list [Low]

**Section:** §25.4 (lines 908–920)

The Helm values schema renders the app-layer SSRF `blockedCIDRs` as a separate YAML list alongside the NetworkPolicy-layer `egressCIDRs.excludePrivate`. The comment at §25.4 line 911 states "Default list mirrors `egressCIDRs.excludePrivate`" — but the two values are two distinct Helm keys. A deployer who overrides `egressCIDRs.excludePrivate` with a stricter list (e.g., add CGNAT `100.64.0.0/10`) does not automatically get the stricter list into `blockedCIDRs`: they must set both keys manually, and there is no preflight check that the two are set-equal (the parity check in §17.9 line 493 only compares the gateway and ops-egress NetworkPolicy `except` blocks to each other, not either of them to the app-layer `blockedCIDRs`).

The app-layer check (§4.8 webhook URL validation; §4.9 LLM Proxy URL validation) is documented as defense-in-depth on top of the NetworkPolicy. When the two lists drift, the L3/L4 blocks one range the L7 check permits (or vice versa), and the effective SSRF posture is the intersection of the two — strictly weaker than either alone.

**Recommendation:** Either (a) remove `ops.webhooks.blockedCIDRs` as a standalone Helm value and reference `egressCIDRs.excludePrivate` directly from the app-layer check at runtime (single source of truth), or (b) add a `blockedCIDRs`-vs-`excludePrivate` parity check to `lenny-preflight` alongside the existing gateway-vs-ops-egress parity check at §17.9 line 493. Option (a) matches the design intent expressed in the line 911 comment.

### NET-076. `webhookIngressCIDR` default `0.0.0.0/0` is documented safe but narrative omits that lenny-system default-deny policy does not cover kube-apiserver source IPs [Low]

**Section:** §13.2 (line 248 `webhookIngressCIDR` note)

The `webhookIngressCIDR` default is `0.0.0.0/0`, documented as safe because "the namespace already enforces default-deny (no unsolicited inbound traffic can reach webhook pods unless explicitly allowed by this rule) and webhook pods authenticate callers via mTLS." This is materially correct: the default-deny policy covers pods IN `lenny-system`, not the kube-apiserver, because the kube-apiserver is NOT a Kubernetes pod — its source IP (node IP, control-plane IP, or cloud-provider load-balancer) enters via the CNI's ingress path and bypasses cluster-internal default-deny semantics in the usual way (any `from: ipBlock` peer with `0.0.0.0/0` admits the apiserver).

However, the claim is conflated: "the namespace already enforces default-deny" is technically true for the `lenny-system` default-deny-all policy, but that policy applies only to pod-sourced traffic and to ingress peers that do NOT match any allow rule. The `allow-apiserver-to-webhooks` rule with `cidr: 0.0.0.0/0` matches *every* source — including rogue pods in tenant namespaces on clusters where the CNI allows arbitrary pod IPs into `lenny-system` under a permissive allow rule. The mTLS second factor is load-bearing.

The narrative at §13.2 line 248 should explicitly state the two-factor claim: (a) network-layer allow is broad because the apiserver source IP is topologically indeterminate in managed K8s; (b) authentication-layer mTLS with CA pinning is the actual authenticator and must be considered the primary security boundary, not a defense-in-depth backstop.

**Recommendation:** Tighten the §13.2 line 248 narrative to explicitly name mTLS as the primary authentication boundary for webhook callbacks when `webhookIngressCIDR: 0.0.0.0/0`, and document the failure mode if the webhook's `ValidatingWebhookConfiguration.webhooks[].clientConfig.caBundle` is missing or incorrect (the `lenny-preflight` "Admission webhook inventory" check at §17.9 line 500 covers the happy path — `caBundle` non-empty and `failurePolicy: Fail` — but a CA-bundle-mismatch would cause silent TLS handshake failures at admission time that the preflight cannot detect). No code/manifest change required; narrative clarity only.

### NET-077. Dedicated CoreDNS opt-out label `lenny.dev/dns-policy` is immutable at admission but not required at pool-config validation time [Low]

**Section:** §13.2 (lines 180, 470, 484)

The `lenny.dev/dns-policy: cluster-default` opt-out is documented at three levels:
1. Admission webhook (§13.2 line 180) enforces label immutability — only the WarmPoolController can set it.
2. NetworkPolicy supplemental rule (§13.2 line 470) permits `kube-system` DNS egress only for pods with the label.
3. Opt-out at the pool-config level is enabled by `dnsPolicy: cluster-default` on the pool, which the WarmPoolController translates into the pod label (§13.2 line 484).

The opt-out is a security regression (removes query logging, rate limiting, response filtering — §13.2 line 484). The opt-out is accepted only for `standard` (runc) isolation profiles; sandboxed/microvm profiles cannot opt out. But the coupling point is implicit in the narrative — the spec states "For `standard` (runc) isolation profiles, deployers may explicitly opt out" but does NOT document a normative validation rule at pool creation time that rejects `dnsPolicy: cluster-default` + sandboxed/microvm. Without such a rule, a pool-config edit combining the two would silently succeed (WarmPoolController labels the pod, NetworkPolicy permits kube-system egress, sandboxed profile accepts the runtime class). No preflight check covers this combination either (§17.9 has no `dns-policy pool validation` row).

**Recommendation:** Add a ValidatingAdmissionWebhook rule on `RuntimePool`/`CredentialPool` (or extend `lenny-pool-config-validator`) that rejects `dnsPolicy: cluster-default` combined with `isolationProfile: sandboxed|microvm`, and add a `dns-policy/isolation-profile compatibility` check to `lenny-preflight` to catch the same combination at install time. Mirrors the `deliveryMode: proxy` + `egressProfile: provider-direct` mutual exclusivity pattern at §13.2 line 438.

### NET-078. `interceptorNamespaces` default `[]` hides a preflight warning when the chart emits zero gateway-egress-interceptor policies [Info]

**Section:** §13.2 (line 322); §10.3 (line 326 NET-063)

The `gateway.interceptorNamespaces` Helm value defaults to `[]`, and the chart emits no `allow-gateway-egress-interceptor-*` NetworkPolicy in that case — safe, as no external interceptors are registered. `lenny-preflight` is documented (§13.2 line 322) to validate that each declared interceptor namespace exists and contains a `lenny.dev/component: interceptor`-labelled pod, warning if zero. It does NOT warn when the namespace list is non-empty but **every** namespace is missing such a pod, nor when a deployer registers an external interceptor via the gateway admin API with a `cluster.local`-qualified endpoint **without** declaring the namespace in `interceptorNamespaces`.

This is not a security vulnerability: the gateway falls back to `failPolicy: fail-closed` on dial timeout (§10.3 NET-063 note, line 330), so an interceptor registered-but-unreachable rejects traffic rather than bypassing policy. However, the silent degradation is worth a preflight warning to catch the configuration-vs-registration gap where a deployer intends the interceptor to mediate traffic but has misconfigured the NetworkPolicy surface.

**Recommendation:** Extend the `lenny-preflight` Job to query the gateway admin API (via the ops-admin-API preflight path) for registered external interceptors and cross-check each endpoint's namespace against `gateway.interceptorNamespaces`. Emit a non-blocking warning if any registered interceptor's namespace is absent from the declared list. Info severity — this is a configuration-sanity check, not a boundary breach.

---

## Severity summary

| Severity | Prior (iter5) | New (iter7) | Total |
|----------|---------------|-------------|-------|
| Critical | 0 | 0 | 0 |
| High     | 0 | 0 | 0 |
| Medium   | 0 | 0 | 0 |
| Low      | 2 | 5 | 7 |
| Info     | 0 | 1 | 1 |

(Prior-iter5 carry-forward count excludes NET-069 and NET-070 which are Fixed.)

## Convergence assessment

Iter5 closed P3 with **0 Critical / 0 High / 1 Medium / 3 Low**. Iter6 deferred P3 entirely (no delta). Iter7 finds **0 Critical / 0 High / 0 Medium / 5 Low / 1 Info** new findings plus 2 Low carry-forwards (NET-071 partially mitigated by preflight, NET-072 unfixed). All iter7 new findings are narrowly scoped to:
- Template-vs-literal inconsistencies in shared SSRF block lists (NET-073, NET-074, NET-075) — fail-closed at preflight for the core pair, but with `excludeIMDS` gap and an `ops.webhooks.blockedCIDRs` uncompared-list surface.
- Narrative clarity in the webhook ingress mTLS posture (NET-076).
- A config-combination gap for the dedicated-CoreDNS opt-out (NET-077) paralleling existing mutual-exclusivity patterns.
- A preflight-warning enhancement opportunity for interceptor registration drift (NET-078).

None of the iter7 new findings identifies a lateral-movement, gateway-bypass, mTLS-handshake, trust-domain, or SSRF-at-L3/L4 vulnerability; all describe hardening/consistency/clarity improvements that strengthen the already fail-closed posture. The Medium finding from iter5 (NET-070) is fully remediated across spec, preflight, metrics, and alerts.

**Convergence verdict: CONVERGED for Perspective 3.** The network-security surface has stabilized on a fail-closed default-deny architecture with dual-family ipBlock correctness, symmetric SSRF boundaries, mTLS peer validation with SPIFFE URIs and DNS SANs, deployment-unique trust domain / SA token audience enforcement, and TLS-default posture on both the OTLP hop and the `lenny-ops` admin API. Remaining work is consistency and coverage polish, appropriate at Low/Info severity; no finding blocks release.
