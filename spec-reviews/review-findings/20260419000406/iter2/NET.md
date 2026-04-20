# Network Security & Isolation Review Findings — Iteration 2

## NET-047 Gateway label selector inconsistency silently breaks LLM upstream egress policy [High]

**Files:** `13_security-model.md` (line 314)

The `allow-gateway-egress-llm-upstream` NetworkPolicy selects gateway pods via `app: lenny-gateway`:

```yaml
podSelector:
  matchLabels:
    app: lenny-gateway
```

Every other NetworkPolicy in `13_security-model.md` that targets the gateway uses `lenny.dev/component: gateway` (lines 63, 91, 132, 201, 258, 285). The §13.2 lenny-system allow-list table (line 201) also identifies the gateway by `lenny.dev/component: gateway`. If the gateway Deployment carries the conventional label, the `app: lenny-gateway` selector silently matches zero pods and has no effect; `lenny-system`'s default-deny egress policy then blocks all LLM provider traffic. Because `NetworkPolicy` with no matching pods applies nothing (rather than denying), this fails silently — no diagnostic surface until proxy-mode requests start timing out in production.

**Recommendation:** Change line 314 to `lenny.dev/component: gateway`. Add a normative statement in §13.2 that all Lenny NetworkPolicy pod selectors use `lenny.dev/component` exclusively. Consider a preflight check that verifies each rendered NetworkPolicy selector matches at least one live pod.

---

## NET-048 Missing lenny-system ingress allow-rule for OTLP collector breaks default OTLP egress [High]

**Files:** `13_security-model.md` (lines 139–168, 197–207)

The `allow-pod-egress-otlp` policy in agent namespaces (line 147) defaults `observability.otlpNamespace: lenny-system` (line 158), authorising agent-pod egress *into* lenny-system. However, `lenny-system` has a default-deny policy (line 190) and the component-specific allow-list table (lines 201–207) enumerates ingress per component — gateway, Token Service, controller, PgBouncer, MinIO, admission webhooks, dedicated CoreDNS — **with no entry for an otel-collector component**.

Deployers following the default configuration and deploying an `otel-collector` (matching `app: otel-collector` per line 161) in lenny-system will have all traces silently dropped at lenny-system's ingress. Symptom: missing OTel traces with no NetworkPolicy drop counter visible on the agent side (the agent egress appears correctly configured), making this extremely hard to diagnose.

**Recommendation:** Either (a) add an `otel-collector` row to the §13.2 allow-list table (`lenny.dev/component: otel-collector`, ingress from `.Values.agentNamespaces` on `{{ .Values.observability.otlpPort }}`), rendered conditionally when `observability.otlpEndpoint` is set and `otlpNamespace == lenny-system`; or (b) default `otlpNamespace` to a separate `observability` namespace. Add a preflight check that fails when an in-cluster OTLP target lacks a matching ingress allow-rule.

---

## NET-049 allow-ingress-controller-to-gateway lacks podSelector, admits all pods in the ingress namespace [Medium]

**Files:** `13_security-model.md` (lines 249–268)

The `allow-ingress-controller-to-gateway` NetworkPolicy identifies the source only by `namespaceSelector` (`kubernetes.io/metadata.name: ingress-nginx`) with no `podSelector` clause. Any pod in that namespace — sidecar, metrics exporter, cert-manager validation pod, debug container, or the ingress controller's own admission webhook — can reach the gateway's external TLS listener on TCP 443 directly, bypassing the ingress controller's authentication, rate limiting, WAF rules, and header normalization.

This deviates from the gateway-centric model's defense-in-depth posture: the gateway's 443 listener is designed to receive ingress-mediated, authenticated requests; co-located workloads can bypass the controller's defensive perimeter.

**Recommendation:** Add a `podSelector` to the `from:` clause that matches the actual ingress controller pod label (e.g., `app.kubernetes.io/name: ingress-nginx` for ingress-nginx, configurable via new Helm values `ingress.controllerPodLabel`/`Value`). Extend the NET-038 preflight check (line 270) to validate at least one pod in the configured namespace matches the configured label.

---

## NET-050 lenny-ops egress uses wrong gateway selector and omits namespaceSelector [High]

**Files:** `25_agent-operability.md` (line 1103)

```yaml
egress:
  - to:
      - podSelector: { matchLabels: { app: lenny-gateway } }
    ports: [{ protocol: TCP, port: 8080 }]
```

Same defect class as NET-047: the selector is `app: lenny-gateway` rather than the `lenny.dev/component: gateway` convention. If the gateway pods carry only the conventional label, this egress rule matches zero pods and `lenny-ops` cannot reach the gateway — breaking operability entirely.

Additionally, the rule omits `namespaceSelector`, so the `podSelector` only applies within `lenny-ops`'s own namespace. If `lenny-ops` runs in `lenny-system` this accidentally works, but line 1130 explicitly supports `lenny-ops` in a separate namespace for "tenant workload isolation"; in that configuration the rule matches zero pods regardless of the label inconsistency.

**Recommendation:** Change selector to `lenny.dev/component: gateway` and add an explicit `namespaceSelector: { matchLabels: { kubernetes.io/metadata.name: lenny-system } }` on the `to:` clause. Audit both `lenny-ops-egress` and `lenny-ops-ingress` for same-namespace assumptions.

---

## Summary

Four regressions, all rooted in NetworkPolicy selector drift and missing allow-list entries for internally-consumed services. All fail silently: policies appear in place but match no pods or drop traffic at the destination's default-deny boundary. NET-047, NET-048, and NET-050 share a root cause — no validation asserts that each rendered NetworkPolicy selector matches at least one live runtime pod. A preflight `kubectl get pods -l <selector>` per NetworkPolicy, run after chart render, would catch all three at install time.

**Total findings: 4** (3 High, 1 Medium)
