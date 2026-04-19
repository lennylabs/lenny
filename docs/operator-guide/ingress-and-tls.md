---
layout: default
title: Ingress and TLS
parent: "Operator Guide"
nav_order: 1.5
description: How Lenny exposes the gateway, lenny-ops, and the web playground through Kubernetes Ingress, how external TLS termination works, and how internal mTLS between the gateway and agent pods is provisioned.
---

# Ingress and TLS

Lenny has two distinct TLS surfaces. They serve different trust boundaries, use different certificate lifecycles, and should not be confused:

| Surface | From → To | Who terminates TLS | How it's provisioned |
|---|---|---|---|
| **External TLS** | Client → gateway Ingress, agent → `lenny-ops` Ingress, browser → playground Ingress | The Ingress controller (NGINX, Traefik, ALB, GKE, etc.) | cert-manager ClusterIssuer (recommended), deployer-provided Secret, or self-signed (dev only) |
| **Internal mTLS** | Gateway ↔ agent pods, gateway ↔ Token Service | Each pod, bilaterally | cert-manager `ClusterIssuer` issuing per-pod certs with SPIFFE identities |

This page covers both. If you're installing Lenny for the first time, you need to make decisions on every row of both tables before you run `helm install`.

---

## External TLS: the three Ingresses

A production install exposes three distinct Ingresses, each serving a different audience.

### 1. Gateway Ingress

**What it serves.** Every client-facing endpoint — `/v1/...` (REST), `/mcp` (MCP Streamable HTTP), `/v1/chat/completions` (OpenAI-compatible), `/v1/responses` (Open Responses), `/openapi.yaml`, and `/healthz`.

**Who reaches it.** Application clients, SDKs, CI pipelines, the `lenny` CLI, MCP hosts.

**Minimal Helm values:**

```yaml
gateway:
  ingress:
    enabled: true
    className: nginx                  # or traefik, alb, gce — whatever your cluster runs
    host: lenny.example.com
    annotations:
      cert-manager.io/cluster-issuer: "acme-production"
    tlsSecretName: ""                 # leave empty when cert-manager owns the Secret
```

### 2. `lenny-ops` Ingress (mandatory)

**What it serves.** The operability control plane — diagnostics, runbooks, audit query, backup/restore, drift detection, webhook subscriptions, the `/mcp/management` endpoint.

**Who reaches it.** Platform operators via `lenny-ctl`, human on-call, AI DevOps agents, `kubectl lenny` — all from outside the cluster.

**Why it's external-only.** `lenny-ops` is exposed **only** through Ingress; no workload inside the cluster can reach it. This is enforced by the `lenny-ops-deny-all-ingress` NetworkPolicy plus a companion `lenny-ops-allow-ingress-from-ingress-controller` policy that opens port 8090 exclusively to the Ingress controller namespace. Lenny's own agent pods cannot call the ops plane, which eliminates an entire class of lateral-movement attacks.

**Minimal Helm values:**

```yaml
ops:
  ingress:
    enabled: true                     # mandatory in production
    className: nginx
    host: lenny-ops.example.com
    annotations:
      cert-manager.io/cluster-issuer: "acme-production"
    tlsSecretName: ""

# NetworkPolicy input — how the chart recognises your Ingress controller
ingress:
  controllerNamespace: ingress-nginx
  controllerLabel:
    app.kubernetes.io/name: ingress-nginx
```

If `ingress.controllerNamespace` and `ingress.controllerLabel` don't match your cluster's actual Ingress controller, the `lenny-ops-allow-ingress-from-ingress-controller` NetworkPolicy will not admit traffic, and every ops call will time out at the network layer with no application-level error.

### 3. Playground Ingress (optional)

**What it serves.** The in-browser web playground at `/playground`.

**Who reaches it.** Developers and demo audiences.

**Three supported modes:**

```yaml
playground:
  # Mode A: off — recommended for production API deployments
  enabled: false

  # Mode B: on but gated behind OIDC — recommended for internal developer platforms
  enabled: true
  authRequired: true

  # Mode C: on and public — only for public demos or non-sensitive environments
  enabled: true
  authRequired: false
```

The playground uses the same Ingress as the gateway (`/playground` path) when enabled.

---

## TLS termination models

The chart supports three TLS-provisioning models. Pick one per Ingress:

| Model | When to use | Renewal |
|---|---|---|
| **cert-manager ClusterIssuer** | Production (recommended) | cert-manager |
| **Deployer-provided TLS Secret** | You already issue and rotate certs through your own PKI | You |
| **Self-signed** | `lenny up`, dev clusters, demos | None — regenerated on chart upgrade |

### cert-manager ClusterIssuer (recommended)

Set `cert-manager.io/cluster-issuer` in the Ingress annotations and leave `tlsSecretName` empty. cert-manager creates and rotates the Secret.

```yaml
gateway:
  ingress:
    annotations:
      cert-manager.io/cluster-issuer: "acme-production"
    tlsSecretName: ""                 # cert-manager owns this

ops:
  ingress:
    annotations:
      cert-manager.io/cluster-issuer: "acme-production"
    tlsSecretName: ""
```

**Expected ClusterIssuer** (operator-provided, not bundled by Lenny):

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: acme-production
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    privateKeySecretRef:
      name: acme-key
    solvers:
      - http01:
          ingress:
            class: nginx
```

**cert-manager is not bundled.** Lenny assumes you already run cert-manager. `lenny-ctl preflight` emits a non-blocking warning if an Ingress references a ClusterIssuer that doesn't exist.

### Deployer-provided Secret

Create the TLS Secret yourself and set `tlsSecretName`:

```bash
kubectl create secret tls lenny-gateway-tls \
  -n lenny-system \
  --cert=fullchain.pem --key=privkey.pem
```

```yaml
gateway:
  ingress:
    annotations: {}                   # no cert-manager annotation
    tlsSecretName: lenny-gateway-tls
```

You own renewal — rotate the Secret before expiry.

### Self-signed (dev only)

```yaml
gateway:
  ingress:
    selfSigned: true
ops:
  ingress:
    selfSigned: true
```

The chart generates a self-signed cert at install time. **Never use in production.** There's no renewal; the cert is regenerated on chart upgrade (which interrupts mTLS validation for any external client pinning the cert).

---

## Ingress-controller compatibility

Lenny's chart renders generic Kubernetes Ingress resources. Any compliant controller works, but each has a few things to check.

| Controller | `className` | Notes |
|---|---|---|
| **ingress-nginx** | `nginx` | SSE works out of the box. If you add `nginx.ingress.kubernetes.io/proxy-buffering: "off"` explicitly for `/v1/sessions/*/logs`, streaming latency improves. |
| **Traefik** | `traefik` | Enable HTTP/2 on the backend for MCP Streamable HTTP. |
| **AWS Load Balancer Controller (ALB)** | `alb` | Use `alb.ingress.kubernetes.io/backend-protocol: HTTPS` if the gateway terminates TLS itself (non-default). Target type `ip` lets ALB talk directly to the gateway pods. |
| **GKE Ingress (GCE)** | `gce` | Longer idle timeout (`cloud.google.com/backend-config`) — default is 30s which cuts long streaming sessions. |
| **Istio ingress-gateway** | (VirtualService) | Lenny ships Ingress objects, not VirtualService. Either convert, or set `ingress.enabled: false` on the chart and manage Istio objects yourself. |

Whichever controller you run, make sure the chart's `ingress.controllerNamespace` and `ingress.controllerLabel` values match the controller's actual namespace and pod labels — these feed the `lenny-ops-allow-ingress-from-ingress-controller` NetworkPolicy.

---

## Streaming considerations

The gateway streams SSE on `GET /v1/sessions/{id}/logs` and bidirectional streams on `/mcp`. Both are long-lived HTTP/1.1 or HTTP/2 connections.

Check your Ingress controller for:

| Setting | Recommended |
|---|---|
| Idle timeout | ≥ 15 minutes (matches the session's max idle interval) |
| Read timeout | ≥ 30 minutes (matches the default `maxSessionAge`) |
| Response buffering | Disabled for `/v1/sessions/*/logs` and `/mcp` |
| HTTP/2 | Enabled for `/mcp` (required for Streamable HTTP) |
| Client-max-body-size | ≥ 500 MB if you raise the runtime's `maxUploadSize` |

Example NGINX annotations:

```yaml
gateway:
  ingress:
    annotations:
      cert-manager.io/cluster-issuer: "acme-production"
      nginx.ingress.kubernetes.io/proxy-read-timeout: "1800"
      nginx.ingress.kubernetes.io/proxy-send-timeout: "1800"
      nginx.ingress.kubernetes.io/proxy-body-size: "500m"
      nginx.ingress.kubernetes.io/proxy-buffering: "off"
```

---

## Internal mTLS: gateway ↔ pods and Token Service

External TLS protects traffic entering the cluster. A **second**, independent TLS layer protects gateway-to-pod and gateway-to-Token-Service traffic inside the cluster.

### Certificate provisioning

cert-manager issues per-pod certificates via a `ClusterIssuer` scoped to the Lenny trust domain (SPIFFE). Each gateway replica gets a **distinct** mTLS identity; compromise of one replica is attributable and independently revocable.

```yaml
global:
  spiffeTrustDomain: "prod.lenny.example.com"    # MUST override per deployment
  saTokenAudience: "lenny-prod"                  # MUST override per deployment
```

**Shared clusters require override.** The default `lenny.local` trust domain is only safe on a single-deployment cluster. If two Lenny deployments share a cluster and both keep the default, pods from deployment A can present identities belonging to deployment B's trust domain, enabling credential lease theft. Set a unique `spiffeTrustDomain` and `saTokenAudience` per deployment.

### Certificate lifecycle

- **TTL.** Configurable per pool, recommended 24 hours for production.
- **Renewal.** cert-manager auto-renews before expiry.
- **Idle-pod refresh.** The WarmPoolController proactively replaces any idle pod whose cert will expire within 30 minutes.
- **Monitoring.** `CertExpiryImminent` fires when any mTLS cert is under an hour from expiring — indicates a cert-manager failure.

### Internal mTLS between `lenny-ops` and the gateway

Optional, for zero-trust clusters where even intra-cluster service-to-service traffic must be mutually authenticated:

```yaml
ops:
  mtls:
    internalEnabled: true
```

When enabled, `lenny-ops` and the gateway present client certs to each other on every call, issued by the same ClusterIssuer as the pod mTLS identities.

---

## Verifying the wiring after install

```bash
# 1. Gateway Ingress reachable, TLS terminates, healthz works
curl -v https://lenny.example.com/healthz

# 2. lenny-ops Ingress reachable from outside the cluster
curl -v https://lenny-ops.example.com/healthz

# 3. NetworkPolicy blocks in-cluster workloads from reaching lenny-ops
kubectl run -it --rm debug --image=curlimages/curl --restart=Never -- \
  curl -m 5 -v http://lenny-ops.lenny-system.svc.cluster.local:8090/healthz
# Expected: connection timed out (NetworkPolicy is denying the request)

# 4. Cert-manager has issued the expected certs
kubectl get certificate -n lenny-system
# Expected: Ready=True for each Ingress-related Certificate

# 5. lenny-ctl can auto-discover the ops URL
lenny-ctl me
# Should succeed without --ops-server being set
```

If step 5 fails with `opsServiceURL not set`, the chart's `ops.ingress.host` is missing — set it and `helm upgrade`.

---

## Common failure modes

| Symptom | Likely cause | Fix |
|---|---|---|
| `lenny-ctl me` times out at the network layer | `ingress.controllerNamespace` / `controllerLabel` don't match the actual Ingress controller | `kubectl get pods -n <ingress-ns> --show-labels` and update the Helm values |
| Certificates stuck `Ready=False` | ClusterIssuer missing, DNS-01 not set up, or HTTP-01 solver blocked by the Ingress | `kubectl describe certificate -n lenny-system <name>` — the status message names the exact step |
| SSE streams disconnect after 30-60 seconds | Ingress-controller default idle timeout too short | Raise `proxy-read-timeout` / equivalent |
| MCP requests succeed for small payloads but hang on large workspaces | `proxy-body-size` below the upload size | Raise it; cross-check against `runtime.limits.maxUploadSize` |
| Agent pods report mTLS handshake failures on startup | cert-manager degraded or `spiffeTrustDomain` mismatch between chart and existing issued certs | `lenny-ctl diagnose connectivity`; check cert-manager logs; rotate certs if trust domain changed |
| `kubectl port-forward -n lenny-system svc/lenny-ops 8090` works but Ingress does not | The Ingress or NetworkPolicy layer is broken; pod-level health is fine | Check Ingress events, controller logs, and the NetworkPolicy denying ingress |

---

## Related pages

- [Installation](installation)
- [Security](security)
- [Security Principles](security-principles)
- [Agent Operability](agent-operability)
- [`lenny-ctl` Reference](lenny-ctl)
