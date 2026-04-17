## 13. Security Model

### 13.1 Pod Security

| Control         | Setting                                                                                           |
| --------------- | ------------------------------------------------------------------------------------------------- |
| User            | Non-root (specific UID/GID)                                                                       |
| Capabilities    | All dropped                                                                                       |
| Root filesystem | Read-only                                                                                         |
| Writable paths  | tmpfs (`/tmp`), workspace, sessions, artifacts                                                    |
| Egress          | Default-deny NetworkPolicy; allow only gateway + required internal services                       |
| Credentials     | No standing credentials; projected SA token + short-lived credential lease only (see [Section 4.9](04_system-components.md#49-credential-leasing-service)) |
| File delivery   | Gateway-mediated only                                                                             |
| Adapter-agent boundary | `SO_PEERCRED` UID check + manifest nonce (primary); if `SO_PEERCRED` unavailable (gVisor divergence), nonce + per-connection HMAC challenge-response (see [Section 4.7](04_system-components.md#47-runtime-adapter) nonce-only fallback). Nonce-only mode requires `SecurityDegradedMode=True` pool condition and alert (see [Section 4.7](04_system-components.md#47-runtime-adapter)). |

### 13.2 Network Isolation

**Minimum CNI requirement:** The cluster CNI must support NetworkPolicy enforcement including egress rules. This can be achieved with Calico or Cilium as the primary CNI, or by running the cloud provider's native CNI plugin (e.g., AWS VPC CNI, Azure CNI) augmented with Calico in policy-only mode. The latter is the recommended approach on managed Kubernetes services (EKS, AKS, GKE) as it preserves native cloud networking while adding the required policy enforcement.

**Default-deny policy (applied to every agent namespace — `lenny-agents`, `lenny-agents-kata`, and any future additions):**

> **Helm templatization:** The Helm chart iterates over `.Values.agentNamespaces` (default: `[lenny-agents, lenny-agents-kata]`) and renders all three NetworkPolicy manifests below into each namespace. The YAML examples show `lenny-agents` as a representative instance.

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: default-deny-all
  namespace: lenny-agents # repeated per agent namespace via Helm range
spec:
  podSelector: {}
  policyTypes: [Ingress, Egress]
```

**Allow gateway-to-pod (applied to all agent pods in every agent namespace):**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-gateway-ingress
  namespace: lenny-agents # repeated per agent namespace via Helm range
spec:
  podSelector:
    matchLabels:
      lenny.dev/managed: "true"
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: lenny-system
          podSelector:
            matchLabels:
              lenny.dev/component: gateway
      ports:
        - port: 50051 # {{ .Values.adapter.grpcPort }} — adapter gRPC listen port
          protocol: TCP
  policyTypes: [Ingress]
```

**Allow pod-to-gateway and DNS — base policy (applied to all agent pods in every agent namespace):**

This base policy allows only the gRPC control channel (port 50051) and DNS. Port 8443 (LLM proxy) is **not** included here — it is conditionally added by the supplemental `allow-pod-egress-llm-proxy` policy (see below) and applies only to pods in pools with `deliveryMode: proxy`.

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-pod-egress-base
  namespace: lenny-agents # repeated per agent namespace via Helm range
spec:
  podSelector:
    matchLabels:
      lenny.dev/managed: "true"
  egress:
    - to: # Gateway — gRPC control channel only
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: lenny-system
          podSelector:
            matchLabels:
              lenny.dev/component: gateway
      ports:
        - port: 50051 # {{ .Values.gateway.grpcPort }} — pod-to-gateway gRPC control channel
          protocol: TCP
    - to: # DNS (lenny-system CoreDNS)
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: lenny-system
          podSelector:
            matchLabels:
              lenny.dev/component: coredns
      ports:
        - protocol: UDP
          port: 53
        - protocol: TCP
          port: 53
  policyTypes: [Egress]
```

**Allow LLM proxy egress — supplemental policy (applied only to pods in proxy-mode pools):**

Pods in pools with `deliveryMode: proxy` need access to the gateway's LLM proxy port (8443). This supplemental policy is applied selectively using the `lenny.dev/delivery-mode: proxy` label, which the WarmPoolController sets on pods belonging to proxy-mode pools. Pods in pools with `deliveryMode: direct` (or no delivery mode) do not receive this label and therefore cannot reach port 8443 — limiting their blast radius if compromised.

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-pod-egress-llm-proxy
  namespace: lenny-agents # repeated per agent namespace via Helm range
spec:
  podSelector:
    matchLabels:
      lenny.dev/managed: "true"
      lenny.dev/delivery-mode: proxy  # {{ .Values.gateway.llmProxyLabel }} — set only on proxy-mode pools
  egress:
    - to: # Gateway — LLM proxy port only
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: lenny-system
          podSelector:
            matchLabels:
              lenny.dev/component: gateway
      ports:
        - port: 8443 # {{ .Values.gateway.llmProxyPort }} — LLM proxy (proxy-mode pools only)
          protocol: TCP
  policyTypes: [Egress]
```

**Allow pod-to-OTLP-collector -- conditional policy (rendered only when `observability.otlpEndpoint` is configured) (NET-046):**

Agent runtimes that emit OpenTelemetry spans ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) need egress to the OTLP collector. Without this supplemental policy, the default-deny policy silently drops all OTLP trace exports from agent pods.

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-pod-egress-otlp
  namespace: lenny-agents # repeated per agent namespace via Helm range
  # Only rendered when {{ .Values.observability.otlpEndpoint }} is non-empty
spec:
  podSelector:
    matchLabels:
      lenny.dev/managed: "true"
  egress:
    - to: # OTLP collector
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: "{{ .Values.observability.otlpNamespace }}" # default: lenny-system
          podSelector:
            matchLabels:
              "{{ .Values.observability.otlpPodLabel }}": "{{ .Values.observability.otlpPodLabelValue }}" # default: app: otel-collector
      ports:
        - port: "{{ .Values.observability.otlpPort }}" # default: 4317 (gRPC OTLP)
          protocol: TCP
  policyTypes: [Egress]
```

> **Note:** If the OTLP collector runs outside the cluster (e.g., a cloud-managed tracing backend), the egress rule uses an `ipBlock` CIDR (`{{ .Values.observability.otlpCIDR }}`) instead of a namespace/pod selector. The `lenny-preflight` Job validates that the configured OTLP endpoint is reachable from agent namespaces when `observability.otlpEndpoint` is set.

> **Label immutability note:** The `lenny.dev/delivery-mode`, `lenny.dev/egress-profile`, and `lenny.dev/dns-policy` labels are subject to the same immutability enforcement as `lenny.dev/managed` — the `lenny-label-immutability` ValidatingAdmissionWebhook (see below) prevents post-creation mutation of these labels. Only the WarmPoolController ServiceAccount may set them at pod creation time. Protecting `lenny.dev/egress-profile` is security-critical: a principal who can mutate this label from `restricted` to `internet` on an existing pod would gain broader network egress without re-admission through the pool controller's validation logic. Protecting `lenny.dev/dns-policy` is likewise security-critical: a principal who can add this label to a pod in a non-opted-out pool would grant that pod a permitted egress path to `kube-system` CoreDNS, bypassing the dedicated CoreDNS instance's query logging, rate limiting, and response filtering.

> **Note:** The namespace selectors above use `kubernetes.io/metadata.name` rather than custom labels like `lenny.dev/component`. This is an immutable label set by the Kubernetes API server on namespace creation -- it cannot be added to or spoofed on other namespaces, unlike custom labels. This prevents an attacker who can create namespaces from bypassing network isolation by applying a matching custom label.

> **`lenny.dev/managed` label immutability (NET-003):** The `allow-gateway-ingress` and `allow-pod-egress-base` NetworkPolicies select pods by `lenny.dev/managed: "true"`. Any pod in an agent namespace that carries this label gains gateway connectivity. Because Kubernetes does not enforce label immutability natively, a principal with `patch` access to pods could add this label to a rogue pod, bypassing network isolation. To close this gap, a **ValidatingAdmissionWebhook** (`lenny-label-immutability`) is deployed as part of the Helm chart and runs in **fail-closed** mode (`failurePolicy: Fail`). The webhook enforces two rules on pods in agent namespaces:
> 1. **Creation guard:** Only `CREATE` requests whose `userInfo` maps to the warm pool controller ServiceAccount (`system:serviceaccount:lenny-system:lenny-controller`) may set `lenny.dev/managed: "true"`. Any other creator is denied.
> 2. **Mutation guard:** `UPDATE` requests that add or change the `lenny.dev/managed` label on an existing pod are denied unconditionally (the label is immutable post-creation).
>
> The webhook is scoped to agent namespaces only (`namespaceSelector` matching `.Values.agentNamespaces`). It is deployed with `replicas: 2` and a PodDisruptionBudget (`minAvailable: 1`) matching the admission controller HA requirements in [Section 17.2](17_deployment-topology.md#172-namespace-layout). The admission policy manifest is included in the Helm chart under `templates/admission-policies/label-immutability-webhook.yaml` and is listed as a check in the `lenny-preflight` Job ([Section 17.6](17_deployment-topology.md#176-packaging-and-installation)).

**`lenny-system` namespace NetworkPolicies (NET-017):**

The `lenny-system` namespace houses the gateway, Token Service, warm pool controller, scaling controller, and the dedicated CoreDNS instance. These components hold high-value credentials and control-plane authority. The Helm chart applies a default-deny policy and component-specific allow-lists to enforce least-privilege networking within `lenny-system`, mirroring the agent namespace approach.

**Default-deny policy for `lenny-system`:**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: default-deny-all
  namespace: lenny-system
spec:
  podSelector: {}
  policyTypes: [Ingress, Egress]
```

**Component-specific allow-lists** are rendered by the Helm chart as individual NetworkPolicy resources in `lenny-system`. Each policy uses a `podSelector` matching the component's `lenny.dev/component` label. The permitted traffic per component:

| Component                                                                            | Egress Allowed                                                                                                                                                                                                                                                    | Ingress Allowed                                                                                                                     |
| ------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| **Gateway** (`lenny.dev/component: gateway`)                                         | Agent namespaces (TCP 50051, adapter gRPC); Token Service (TCP `{{ .Values.tokenService.grpcPort }}` — default 50052, mTLS); PgBouncer (TCP 5432); Redis (TCP 6380, TLS — `{{ .Values.redis.tlsPort }}`); MinIO (TCP 9443, TLS — `{{ .Values.minio.tlsPort }}`); kube-apiserver (TCP 443, CIDR `{{ .Values.kubeApiServerCIDR }}`); `kube-system` CoreDNS (UDP/TCP 53); external HTTPS (TCP 443, `0.0.0.0/0` excluding cluster pod/service CIDRs and IMDS addresses — required for LLM Proxy upstream forwarding to LLM provider APIs, connector callback delivery, and webhook notifications; see [Section 4.8](04_system-components.md#48-gateway-policy-engine) and [Section 4.9](04_system-components.md#49-credential-leasing-service)). IMDS addresses (`169.254.169.254/32`, `fd00:ec2::254/128`, `100.100.100.200/32`) are explicitly excluded via `except` clauses on the external HTTPS CIDR rule. In-cluster external interceptor namespaces: for each namespace declared in `{{ .Values.gateway.interceptorNamespaces }}` (default: `[]`), the Helm chart renders a supplemental egress rule allowing TCP `{{ .Values.gateway.interceptorGRPCPort }}` (default 50053) to that namespace — required for the gateway to reach in-cluster gRPC interceptors ([Section 4.8](04_system-components.md#48-gateway-policy-engine), NET-039). | External ingress (TCP 443 from Ingress controller namespace — `{{ .Values.ingressControllerNamespace }}`, default: `ingress-nginx`); agent namespaces (TCP 50051, gateway gRPC port for pod-to-gateway control traffic; TCP 8443, LLM proxy port for proxy-mode pods); admission-webhook pods (TCP `{{ .Values.gateway.internalPort }}` — default 8080, internal HTTP port for `lenny-drain-readiness` callbacks to `GET /internal/drain-readiness` — NET-037). |
| **Token Service** (`lenny.dev/component: token-service`)                   | PgBouncer (TCP 5432); Redis (TCP 6380, TLS — `{{ .Values.redis.tlsPort }}`); KMS endpoint (HTTPS 443, CIDR from `{{ .Values.kms.endpointCIDR }}`); `kube-system` CoreDNS (UDP/TCP 53).                                                                           | Gateway pods only (TCP `{{ .Values.tokenService.grpcPort }}` — default 50052, mTLS).                                               |
| **Warm Pool Controller / PoolScalingController** (`lenny.dev/component: controller`) | kube-apiserver (TCP 443); PgBouncer (TCP 5432); `kube-system` CoreDNS (UDP/TCP 53).                                                                                                                                                                               | None (controllers initiate all connections).                                                                                        |
| **PgBouncer** (`lenny.dev/component: pgbouncer`) — self-managed profile only; absent on cloud-managed deployments where the provider proxy is external to the cluster | Postgres (TCP 5432, CIDR from `{{ .Values.postgres.host }}`); `kube-system` CoreDNS (UDP/TCP 53). | Gateway pods (TCP 5432); Token Service pods (TCP 5432); Warm Pool Controller / PoolScalingController pods (TCP 5432). |
| **MinIO** (`lenny.dev/component: minio`) — self-managed profile only; absent on cloud-managed deployments where the provider's native object storage (S3, GCS, Azure Blob) is used instead | `kube-system` CoreDNS (UDP/TCP 53). | Gateway pods (TCP 9443, TLS — `{{ .Values.minio.tlsPort }}`). |
| **Admission Webhooks** (`lenny.dev/component: admission-webhook`) — `lenny-label-immutability`, `lenny-direct-mode-isolation`, `lenny-sandboxclaim-guard`, and CRD validation webhooks | `kube-system` CoreDNS (UDP/TCP 53); Gateway internal HTTP port (TCP `{{ .Values.gateway.internalPort }}` — default 8080) for the `lenny-drain-readiness` webhook to call `GET /internal/drain-readiness` (NET-037). Without this egress rule, the `lenny-system` default-deny policy blocks the drain-readiness callback, causing all pod evictions to be permanently rejected by the fail-closed webhook. | kube-apiserver (TCP 443, CIDR `{{ .Values.webhookIngressCIDR }}` — default `0.0.0.0/0`). The kube-apiserver must reach these pods to invoke ValidatingAdmissionWebhook callbacks. Without this ingress rule, the `lenny-system` default-deny policy blocks all webhook callbacks, causing fail-closed webhooks to reject all pod admissions silently. See the `webhookIngressCIDR` note below for cloud-specific tightening guidance. |
| **Dedicated CoreDNS** (`lenny.dev/component: coredns`)                               | `kube-system` CoreDNS (UDP/TCP 53, for upstream forwarding); external DNS resolvers if configured.                                                                                                                                                                | Agent namespace pods (UDP/TCP 53, per `allow-pod-egress-base` in agent namespaces); monitoring namespace (TCP 9153, Prometheus metrics scrape). |

> **Prometheus monitoring ingress (NET-045):** The `lenny-system` default-deny policy blocks all unsolicited ingress, including Prometheus scrape requests from the monitoring namespace. Without explicit ingress rules, all `lenny-system` component metrics (including `lenny_gateway_active_sessions`, `lenny_network_policy_cidr_drift_total`, HPA-driving metrics like `lenny_gateway_request_queue_depth`, and CoreDNS `prometheus :9153`) would be unscrapeable, breaking the observability and autoscaling pipeline. The Helm chart renders a supplemental ingress NetworkPolicy for each `lenny-system` component that exposes a metrics endpoint, allowing TCP ingress on the component's metrics port from the namespace specified in `{{ .Values.monitoring.namespace }}` (default: `monitoring`). The affected components and their metrics ports are: Gateway (`{{ .Values.gateway.metricsPort }}`, default 9090), Warm Pool Controller / PoolScalingController (`{{ .Values.controller.metricsPort }}`, default 9090), Token Service (`{{ .Values.tokenService.metricsPort }}`, default 9090), and Dedicated CoreDNS (TCP 9153). The `lenny-preflight` Job validates that the monitoring namespace exists and contains at least one pod matching `app.kubernetes.io/name: prometheus` (or the label configured in `{{ .Values.monitoring.podLabel }}`), warning (non-blocking) if no Prometheus pods are found.

> **`kubeApiServerCIDR` and `webhookIngressCIDR` Helm values (NET-040):** Two separate Helm values govern kube-apiserver connectivity, because the kube-apiserver egress IP for gateway access and its ingress IP for webhook callbacks differ in most environments:
>
> - **`kubeApiServerCIDR`** (required, no default): The CIDR that the **gateway** uses to reach the kube-apiserver over TCP 443. This is always the kube-apiserver **Service ClusterIP**, which lives in the cluster service CIDR. Discover it with:
>   ```
>   # Any cluster
>   kubectl get svc kubernetes -n default -o jsonpath='{.spec.clusterIP}'
>
>   # EKS: service CIDR is shown in cluster details
>   aws eks describe-cluster --name <cluster> --query 'cluster.kubernetesNetworkConfig.serviceIpv4Cidr' --output text
>
>   # GKE
>   gcloud container clusters describe <cluster> --format='value(servicesIpv4Cidr)'
>
>   # AKS
>   az aks show --name <cluster> --resource-group <rg> --query 'networkProfile.serviceCidr' -o tsv
>   ```
>   Set `kubeApiServerCIDR` to the full service CIDR (e.g., `10.96.0.0/12`) rather than a `/32` so that the rule is stable across kube-apiserver Service IP reassignments. The `lenny-preflight` Job validates that the actual `kubernetes.default` Service ClusterIP falls within the configured CIDR and fails the install if it does not.
>
> - **`webhookIngressCIDR`** (default: `0.0.0.0/0`): The source CIDR allowed to reach admission webhook pods on TCP 443. In managed Kubernetes (EKS, GKE, AKS) the kube-apiserver calls webhooks from a **node IP** or a cloud-provider control-plane IP — not the service ClusterIP — making it impractical to pin this to a narrow CIDR without cloud-specific knowledge. The default `0.0.0.0/0` is safe within `lenny-system` because the namespace already enforces default-deny (no unsolicited inbound traffic can reach webhook pods unless explicitly allowed by this rule) and webhook pods authenticate callers via mTLS. Operators who wish to tighten this can discover the kube-apiserver egress CIDR as follows:
>   ```
>   # Self-managed (control-plane node IP range)
>   kubectl get nodes --selector node-role.kubernetes.io/control-plane -o jsonpath='{.items[*].status.addresses[?(@.type=="InternalIP")].address}'
>
>   # EKS: API server communicates from within the cluster VPC; use the VPC CIDR or the
>   # EKS-managed ENI subnet CIDR shown in the cluster networking tab.
>
>   # GKE: private cluster control plane CIDR
>   gcloud container clusters describe <cluster> --format='value(privateClusterConfig.masterIpv4CidrBlock)'
>
>   # AKS: API server authorized IP ranges (if configured)
>   az aks show --name <cluster> --resource-group <rg> --query 'apiServerAccessProfile.authorizedIpRanges' -o tsv
>   ```
>   A wrong or overly narrow `webhookIngressCIDR` causes the kube-apiserver to fail to reach webhook pods, blocking all admission for resources covered by fail-closed webhooks — including warm-pool pod creation, SandboxClaim operations, and label immutability checks — until corrected and redeployed.

**Gateway ingress from Ingress controller (NET-038):**

The gateway must accept external HTTPS traffic forwarded by the cluster's Ingress controller. The `{{ .Values.ingressControllerNamespace }}` Helm value (default: `ingress-nginx`) identifies the namespace in which the Ingress controller pods run. The Helm chart renders the following NetworkPolicy in `lenny-system`:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-ingress-controller-to-gateway
  namespace: lenny-system
spec:
  podSelector:
    matchLabels:
      lenny.dev/component: gateway
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: "{{ .Values.ingressControllerNamespace }}" # default: ingress-nginx
      ports:
        - port: 443 # {{ .Values.gateway.httpsPort }} — external TLS listener
          protocol: TCP
  policyTypes: [Ingress]
```

> **`ingressControllerNamespace` Helm value (NET-038):** `ingressControllerNamespace` must match the actual namespace in which the Ingress controller pods run (e.g., `ingress-nginx`, `traefik`, `kourier-system`). If it is set incorrectly, the `lenny-system` default-deny policy blocks all traffic from the Ingress controller, making the gateway unreachable from the internet. The `lenny-preflight` Job validates that a namespace with the configured name exists in the cluster and warns if it has zero running pods, catching the most common misconfiguration at deploy time.

> **`gateway.interceptorNamespaces` Helm value (NET-039):** External interceptors deployed in-cluster ([Section 4.8](04_system-components.md#48-gateway-policy-engine)) are gRPC services. The gateway's default egress rules do not include cluster pod CIDRs — they are explicitly excluded from the `0.0.0.0/0` external HTTPS rule. Without explicit namespace-scoped egress rules, the `lenny-system` default-deny policy blocks all gateway-to-interceptor gRPC calls, causing every in-cluster external interceptor to time out and (when `failPolicy: fail-closed`) reject all requests.
>
> To close this gap, deployers must declare the namespaces where their in-cluster external interceptors run via the `gateway.interceptorNamespaces` Helm value (a list of namespace names, default: `[]`). The Helm chart iterates over this list and renders one supplemental `NetworkPolicy` per namespace into `lenny-system`:
>
> ```yaml
> apiVersion: networking.k8s.io/v1
> kind: NetworkPolicy
> metadata:
>   name: allow-gateway-egress-interceptor-{{ namespace }} # one resource per declared interceptor namespace
>   namespace: lenny-system
> spec:
>   podSelector:
>     matchLabels:
>       lenny.dev/component: gateway
>   egress:
>     - to:
>         - namespaceSelector:
>             matchLabels:
>               kubernetes.io/metadata.name: "{{ namespace }}" # iterates over .Values.gateway.interceptorNamespaces
>       ports:
>         - port: "{{ .Values.gateway.interceptorGRPCPort }}" # default 50053 — interceptor gRPC listen port
>           protocol: TCP
>   policyTypes: [Egress]
> ```
>
> When `gateway.interceptorNamespaces` is empty (the default), no supplemental policies are rendered, which is safe — no external interceptors are registered in this configuration. Deployers who register external interceptors that run outside the cluster (reachable via an external IP already covered by the `0.0.0.0/0` external HTTPS rule) do not need to add entries to `gateway.interceptorNamespaces`. The `lenny-preflight` Job validates that each declared interceptor namespace exists in the cluster and warns if it has zero running pods.
>
> The `gateway.interceptorGRPCPort` Helm value (default: `50053`) defines the port the Helm-rendered NetworkPolicy rules allow. Individual interceptor registrations may bind on different ports; deployers should ensure their interceptor pods listen on this port (or override the Helm value to match). Note that this value governs the NetworkPolicy port allowance only — the actual gRPC endpoint address used by the gateway to call each interceptor is specified in the interceptor registration configuration, not in this NetworkPolicy.

> **Note:** `lenny-system` components use `kube-system` CoreDNS for their own DNS resolution (not the dedicated agent CoreDNS instance). The dedicated CoreDNS in `lenny-system` serves agent namespaces only. Cloud metadata endpoint blocking is achieved through two complementary mechanisms depending on policy type: (a) the base `allow-pod-egress-base` policy is an **allowlist-only** policy (gateway gRPC + DNS only) — it contains no broad CIDR rules and therefore **implicitly** blocks IMDS because those addresses are not in the allowlist; (b) supplemental policies that include broad CIDR rules (such as the `internet` profile's `0.0.0.0/0` rule) carry **explicit `except` clauses** for IMDS addresses to prevent IMDS access even when broad egress is granted. The blocked IMDS addresses are: `169.254.169.254/32` (AWS/GCP/Azure IPv4 IMDS), `fd00:ec2::254/128` (AWS IPv6 IMDS), and `100.100.100.200/32` (Alibaba Cloud IMDS). See NET-002 hardening note below for the supplemental policy `except` clause details.

**Per-pool egress relaxation:** Pools that need internet access (e.g., for LLM API calls) get additional NetworkPolicy resources allowing egress to specific CIDR ranges or services. These policies are **pre-created** by the Helm chart (or deployer) using label selectors that match pool labels (e.g., `lenny.dev/pool: <pool-name>`, `lenny.dev/egress-profile: restricted`). The warm pool controller does NOT create or modify NetworkPolicies — it only labels pods with the appropriate pool and egress-profile labels so that the pre-created policies take effect. This avoids granting the controller RBAC permissions for NetworkPolicy resources.

**`egressProfile` enum:**

| Profile                | Egress Allowed                                                                      | Use Case                                              |
| ---------------------- | ----------------------------------------------------------------------------------- | ----------------------------------------------------- |
| `restricted` (default) | Gateway + DNS proxy only                                                            | Agent pods that use the LLM proxy; no direct internet |
| `provider-direct`      | Gateway + DNS proxy + LLM provider CIDRs                                            | Direct LLM API access (Bedrock, Vertex endpoints)     |
| `internet`             | Gateway + DNS proxy + all internet (0.0.0.0/0, excluding cluster pod/service CIDRs and IMDS addresses) | Pods needing package install, web access              |

> **Note:** CIDR ranges for `provider-direct` are maintained in the Helm values (`egressCIDRs.providers`) and updated by deployers when provider endpoints change. NetworkPolicies reference these CIDRs via pre-created policies (per K8s-M3).
>
> **`provider-direct` IMDS exclusion (NET-044):** Although `provider-direct` CIDRs are typically narrow public IP ranges (unlike the `internet` profile's `0.0.0.0/0`), the Helm chart unconditionally includes `except` clauses for all IMDS addresses (`egressCIDRs.excludeIMDS`) on the `provider-direct` supplemental NetworkPolicy, matching the `internet` profile's behavior. Additionally, the `lenny-preflight` Job validates that no entry in `egressCIDRs.providers` overlaps with any address in `egressCIDRs.excludeIMDS`, failing the install with: `"provider-direct CIDR '<cidr>' overlaps with IMDS address '<imds>'. Use a narrower CIDR that excludes metadata endpoints."` This prevents a carelessly broad deployer CIDR (e.g., `169.254.0.0/16`) from inadvertently granting IMDS access.

> **`provider-direct` + `deliveryMode: proxy` mutual exclusivity (NET-006):** A pool configured with `egressProfile: provider-direct` opens a direct network path from agent pods to LLM provider CIDRs. Combining this with `deliveryMode: proxy` creates an incoherent security posture: the proxy mode is designed to prevent API keys from reaching pods by routing all LLM traffic through the gateway, but `provider-direct` egress gives pods a bypass route to the same provider endpoints. To prevent silent bypass, these two settings are **mutually exclusive** and enforced as follows:
> 1. **Pool registration validation:** The warm pool controller (and Helm chart validation) rejects any `CredentialPool` or `RuntimePool` configuration that sets both `deliveryMode: proxy` and `egressProfile: provider-direct`, emitting a `InvalidPoolEgressDeliveryCombo` validation error.
> 2. **ValidatingAdmissionWebhook enforcement:** The `lenny-direct-mode-isolation` ValidatingAdmissionWebhook (which also enforces `deliveryMode: direct` + `isolationProfile: standard` in multi-tenant mode — see [Section 6.2](06_warm-pod-model.md#62-pod-state-machine)) rejects pod creation for pools with this illegal combination.
> 3. **Helm chart guard:** A Helm pre-install/upgrade hook validates `credentialPools[*]` and fails the deployment if any pool violates this constraint.
> The correct pairing is: `deliveryMode: proxy` with `egressProfile: restricted` (traffic goes only to the gateway proxy), or `deliveryMode: direct` with `egressProfile: provider-direct` (pod contacts provider directly with a short-lived lease).

> **`internet` profile hardening (NET-002):** The `internet` egress NetworkPolicy explicitly **excludes** cluster-internal CIDRs (`egressCIDRs.excludeClusterPodCIDR` and `egressCIDRs.excludeClusterServiceCIDR` in the Helm values) via `except` clauses on the `0.0.0.0/0` CIDR rule. This prevents lateral movement between agent pods even when internet egress is permitted.
>
> **CIDR exclusion correctness and drift (NET-022).** If these values are wrong at deploy time, or become stale after a cluster CIDR resize or node pool expansion, agent pods with `internet` egress can reach internal cluster IPs — enabling lateral movement and internal service discovery. Two controls enforce correctness:
>
> 1. **Preflight validation:** The `lenny-preflight` Job reads the cluster's actual pod and service CIDRs (from node `spec.podCIDR` aggregation and the `kubernetes` Service ClusterIP range) and fails the Helm install/upgrade if `egressCIDRs.excludeClusterPodCIDR` or `egressCIDRs.excludeClusterServiceCIDR` do not match, emitting: `"internet egress CIDR exclusion mismatch: excludeClusterPodCIDR is '<configured>' but cluster reports '<actual>'. Re-run with the correct CIDR to prevent lateral movement."` This check also fires when `internet` pools are present and either exclusion value is absent entirely.
>
> 2. **Continuous drift detection:** The WarmPoolController includes a goroutine that re-reads cluster CIDRs every 5 minutes and compares them against the installed NetworkPolicy `except` blocks for the `internet` egress profile. On drift, it increments `lenny_network_policy_cidr_drift_total` (counter, labeled `profile: internet`, `field: pod_cidr|service_cidr`) and fires a `NetworkPolicyCIDRDrift` critical alert. The controller does **not** auto-patch NetworkPolicies (this would require NetworkPolicy write RBAC, currently avoided by design). The operator must re-run `helm upgrade` with the corrected CIDR values to re-sync. Additionally, the `0.0.0.0/0` CIDR rule in the `internet` profile (and any other supplemental policy containing broad CIDR rules) includes `except` entries for all cloud instance metadata service (IMDS) addresses: `169.254.169.254/32` (AWS/GCP/Azure IPv4 IMDS), `fd00:ec2::254/128` (AWS IPv6 IMDS), and `100.100.100.200/32` (Alibaba Cloud IMDS). The base `allow-pod-egress-base` policy does NOT use `except` clauses — it is an allowlist-only policy (gateway gRPC + DNS only) that implicitly blocks IMDS because those addresses are not allowlisted. Supplemental policies that contain broad CIDR rules DO carry these explicit `except` blocks. This ensures that even if a pod is granted broad internet egress, it cannot reach node IAM credentials via link-local IMDS endpoints. The Helm values expose `egressCIDRs.excludeIMDS` (default: `["169.254.169.254/32", "fd00:ec2::254/128", "100.100.100.200/32"]`) so deployers can extend the list for additional cloud providers. Furthermore, the `internet` profile **requires** a sandboxed isolation profile (`sandboxed` or `microvm`) — pools with `isolationProfile: standard` (runc) cannot use the `internet` egress profile. The warm pool controller rejects pool configurations that combine `standard` isolation with `internet` egress at validation time.

**DNS exfiltration mitigation:** A dedicated **CoreDNS instance** runs in `lenny-system` (labeled `lenny.dev/component: coredns`) and serves as the DNS resolver for all agent namespaces by default. The `allow-pod-egress-base` NetworkPolicy above routes DNS traffic exclusively to this instance — agent pods cannot reach `kube-system` DNS directly.

**Agent pod DNS configuration (K8S-033).** The NetworkPolicy alone is insufficient — Kubernetes defaults `dnsPolicy` to `ClusterFirst`, which configures the pod's `/etc/resolv.conf` to point at the `kube-dns` Service ClusterIP in `kube-system`. Since the NetworkPolicy blocks that path, DNS resolution would fail silently. The WarmPoolController therefore sets the following on every agent pod spec:

```yaml
dnsPolicy: None
dnsConfig:
  nameservers:
    - "{{ .Values.coredns.clusterIP }}"   # ClusterIP of the lenny-agent-dns Service in lenny-system
  searches:
    - "{{ .Release.Namespace }}.svc.cluster.local"
    - "svc.cluster.local"
    - "cluster.local"
  options:
    - name: ndots
      value: "5"
```

The `coredns.clusterIP` Helm value is the ClusterIP assigned to the `lenny-agent-dns` Service (rendered in `templates/coredns-service.yaml`). The Helm chart validates that this value is non-empty when any agent namespace is configured. The `searches` and `ndots` entries mirror the standard `ClusterFirst` search domain behavior so that in-cluster Service names resolve correctly through the dedicated CoreDNS instance. When `dnsPolicy: cluster-default` is set on a pool (the opt-out path described below), the WarmPoolController omits `dnsPolicy: None` and `dnsConfig` entirely, reverting to the Kubernetes default `ClusterFirst` behavior — pods in that pool resolve through `kube-system` CoreDNS. The WarmPoolController also sets the label `lenny.dev/dns-policy: cluster-default` on pods in opted-out pools. The `allow-pod-egress-base` NetworkPolicy must be supplemented with a DNS egress rule to `kube-system` (rendered by the Helm chart when `dnsPolicy: cluster-default` pools exist). This supplemental rule must use a `podSelector` scoped to `lenny.dev/dns-policy: cluster-default` — not the broad `lenny.dev/managed: "true"` selector — so that only opted-out pods gain the `kube-system` DNS egress path. Using the broad selector would grant all managed agent pods a permitted egress path to `kube-system` CoreDNS, bypassing the dedicated CoreDNS instance's query logging, rate limiting, and response filtering.

The dedicated CoreDNS instance provides:

- **Query logging** — all DNS queries from agent pods are recorded for audit.
- **Per-pod rate limiting** — throttles query volume per source pod to prevent high-throughput tunneling.
- **Response filtering** — blocks TXT records exceeding a size threshold and drops unusual record types commonly used for DNS tunneling (e.g., NULL, PRIVATE, KEY).

**Dedicated CoreDNS high availability (NET-018):** Because the dedicated CoreDNS instance is the sole authorized DNS resolver for all agent pods across all agent namespaces, it must run as a highly available Deployment:

- **Replica count:** Minimum 2 replicas (`{{ .Values.coredns.replicas }}`, default `2`). The Helm chart validates that this value is >= 2.
- **PodDisruptionBudget:** `minAvailable: 1`, ensuring at least one replica survives voluntary disruptions (node drains, rolling upgrades).
- **Failure mode:** If all dedicated CoreDNS replicas become unavailable, agent pods lose DNS resolution entirely — the `allow-pod-egress-base` NetworkPolicy does **not** permit fallback to `kube-system` CoreDNS. This is intentional: a silent fallback would bypass query logging, rate limiting, and response filtering without any indication to the operator. The monitoring stack must fire a `DedicatedDNSUnavailable` critical alert ([Section 16](16_observability.md)) when the ready replica count drops to zero, and a `DedicatedDNSDegraded` warning alert when it drops below the configured minimum.

For `standard` (runc) isolation profiles, deployers may explicitly opt out of the dedicated CoreDNS instance via pool configuration (`dnsPolicy: cluster-default`), which falls back DNS to `kube-system` CoreDNS. This must be a conscious choice — the dedicated instance is the default for all profiles. The WarmPoolController sets the `lenny.dev/dns-policy: cluster-default` label only on pods in pools configured with this opt-out; pods in all other pools do not receive the label. Note that opting out removes the security properties (query logging, rate limiting, response filtering) for pods in that pool.

**Reference Corefile for the dedicated CoreDNS instance.** The ConfigMap `lenny-agent-dns-corefile` (rendered by the Helm chart from `templates/coredns-configmap.yaml`) provides the following reference configuration. Deployers may override individual plugin parameters via Helm values (`coredns.corefile.*`) — the structure below is the shipped default:

```
# lenny-agent-dns — dedicated CoreDNS for agent namespaces
# Deployed in lenny-system; serves all namespaces in .Values.agentNamespaces

.:53 {
    # Query logging: all DNS queries from agent pods are logged for audit
    log {
        class all
    }

    # Per-pod rate limiting: prevent high-throughput DNS tunneling
    # ratelimit is supplied by the coredns-ratelimit plugin (bundled in the lenny CoreDNS image)
    ratelimit {
        responses_per_second 10   # {{ .Values.coredns.rateLimit.responsesPerSecond }} default: 10
        # Limit is per source IP (i.e., per pod when pod-cidr-per-pod is enforced)
    }

    # Response filtering: block record types commonly used for DNS exfiltration
    # filter is supplied by the coredns-filter plugin (bundled in the lenny CoreDNS image)
    filter {
        # Drop oversized TXT records (tunneling carries data in TXT)
        max_txt_size 255            # {{ .Values.coredns.filter.maxTxtSize }} default: 255 bytes (1 TXT string)
        # Drop record types not needed by agent pods
        block_types NULL PRIVATE KEY TYPE65534
    }

    # Forward to kube-system CoreDNS for cluster-internal and external resolution
    forward . /etc/resolv.conf {
        max_concurrent 1000
        health_check 5s
    }

    # Health endpoint (used by readiness/liveness probes)
    health :8080

    # Prometheus metrics
    prometheus :9153

    # Cache: reduce upstream query load; short TTL to limit stale data exposure
    cache 30                        # {{ .Values.coredns.cacheTTL }} default: 30s

    # Reload Corefile changes without pod restart
    reload

    errors
}
```

The `coredns-ratelimit` and `coredns-filter` plugins are non-standard CoreDNS plugins. They are compiled into the `lenny-coredns` container image (tag tracked in `values.yaml` under `coredns.image`). Source for both plugins is vendored under `build/coredns-plugins/` in the Lenny repository. Deployers who need to build a custom CoreDNS image must include both plugins; the Helm chart validates plugin availability via a readiness probe that queries the CoreDNS health endpoint on startup.

### 13.3 Credential Flow

**Connector credentials (OAuth):**

```
Client authenticates → Gateway validates → Gateway mints session context
                                         → Gateway holds all downstream OAuth tokens
                                         → Pod receives: session context + projected SA token
                                         → Pod never receives: client tokens, downstream OAuth tokens
```

**LLM provider credentials (credential leasing — direct mode):**

```
Gateway evaluates CredentialPolicy → Token Service selects from pool or user source
                                   → Token Service materializes short-lived credentials
                                   → Gateway pushes CredentialLease to pod via AssignCredentials
                                   → Pod receives: materialized short-lived provider config
                                   → Pod never receives: pool root API keys, IAM role ARNs, long-lived secrets
                                   → On RATE_LIMITED: gateway rotates → pushes new lease via RotateCredentials
                                   → On session end: lease released back to pool
```

**LLM provider credentials (credential leasing — proxy mode, optional):**

```
Gateway evaluates CredentialPolicy → Token Service selects from pool or user source
                                   → Gateway generates lease token + proxy URL
                                   → Gateway pushes lease token + proxy URL to pod (NOT the real API key)
                                   → Pod sends LLM requests to proxy URL with lease token
                                   → Gateway proxy validates lease, injects real API key, forwards upstream
                                   → Real API key never enters the pod
                                   → On lease expiry/revocation: proxy immediately rejects requests
                                   → On session end: lease invalidated, proxy stops forwarding
```

**Key distinction:** Connector credentials (OAuth tokens for external tools and agents) are used by the gateway on behalf of pods (pods never see them). LLM provider credentials are either delivered directly as short-lived leases (direct mode) or kept entirely out of the pod via the credential-injecting reverse proxy (proxy mode) — see [Section 4.9](04_system-components.md#49-credential-leasing-service) for details on both modes.

For the complete credential subsystem specification — including threat model considerations, security boundaries, emergency revocation procedures, and governance boundaries — see [Section 4.9](04_system-components.md#49-credential-leasing-service). Key security-relevant subsections: Security Boundaries (preventing cross-tenant credential leakage), Emergency Credential Revocation (in-memory deny list propagation), and Credential Governance Boundaries (separation of admin vs. deployer vs. runtime access).

### 13.4 Upload Security

- Gateway validates and authorizes all uploads
- Pod trusts only the gateway (not arbitrary clients)
- Path traversal protection (reject `..`, absolute paths, symlinks escaping workspace)
- Size limits enforced at gateway and pod
- Staging → validation → promotion pattern
- Archive extraction with zip-slip protection

### 13.5 Delegation Chain Content Security

Delegation chains introduce a prompt injection attack surface: a compromised or manipulated parent agent can craft adversarial `TaskSpec.input` payloads targeting child agents. Lenny provides layered mitigations:

1. **Input size limits** — `contentPolicy.maxInputSize` on `DelegationPolicy` ([Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)) enforces a hard byte-size cap on delegation input. Default: 128KB.
2. **Content scanning hook** — `contentPolicy.interceptorRef` invokes a `RequestInterceptor` at the `PreDelegation` phase ([Section 4.8](04_system-components.md#48-gateway-policy-engine)) before any delegation is processed. Deployers wire in external classifiers (prompt injection detectors, content safety APIs) here.
3. **Inter-session message scanning** — `contentPolicy.maxInputSize` and `contentPolicy.interceptorRef` apply to `lenny/send_message` payloads via the `PreMessageDelivery` interceptor phase ([Section 4.8](04_system-components.md#48-gateway-policy-engine)), providing the same content policy enforcement as delegation inputs.
4. **Messaging rate limits** — `messagingRateLimit` on the delegation lease ([Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)) caps `lenny/send_message` volume per session (`maxPerMinute` outbound, `maxPerSession` lifetime). The `maxInboundPerMinute` aggregate limit caps total inbound messages to any single session regardless of the number of senders, preventing N compromised siblings from flooding a target at N × the per-sender rate.
5. **Messaging scope** — `messagingScope` ([Section 7.2](07_session-lifecycle.md#72-interactive-session-model)) restricts which sessions can message each other. Default `direct` limits to parent/children only.
6. **Budget and depth limits** — delegation leases enforce `maxDepth`, `maxTreeSize`, and `maxTokenBudget`, bounding the blast radius of any compromised delegation chain.

**Residual risk without content scanning:** Without `contentPolicy.interceptorRef`, the gateway validates delegation structure (depth, budget, policy tags) but does not inspect content semantics. See [Section 22.3](22_explicit-non-decisions.md) for the explicit non-decision on built-in guardrail logic.

**Residual risk — file export content:** `contentPolicy.interceptorRef` covers `TaskSpec.input` only. Workspace files exported from a parent to a child ([Section 8.7](08_recursive-delegation.md#87-file-export-model)) are not subject to content scanning by the platform. A compromised parent can include adversarial content in any exported file, including files that agent runtimes treat as instruction sources (e.g., `CLAUDE.md`). This is a known gap: the platform provides structural validation of exports (path bounds, size limits, symlink protection) but not semantic inspection of file contents. Deployers must account for this by treating all workspace files received via delegation as untrusted input. See [Section 8.7](08_recursive-delegation.md#87-file-export-model) for guidance on deployer-side mitigations.

