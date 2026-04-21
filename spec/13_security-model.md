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

**Pod-namespace isolation (`shareProcessNamespace`, `hostPID`, `hostNetwork`, `hostIPC`).** All four host-sharing / process-sharing flags on the pod spec are **forbidden** on every pod template Lenny generates — gateway, agent, `lenny-ops`, `lenny-preflight`, and CRD-driven RuntimePool pods:

- `spec.shareProcessNamespace: true` — would expose the gateway process's in-memory Token Service credential cache (real upstream API keys, §4.9) and per-request lease state to any other container in the pod, collapsing the gateway's trust boundary.
- `spec.hostPID: true` — would expose every process on the node (including other tenants' agent pods) to the container's `/proc` view, defeating pod isolation entirely.
- `spec.hostNetwork: true` — would bypass the `lenny-system` and agent-namespace NetworkPolicies (§13.2) because the pod would use the node's network namespace directly, gaining unrestricted access to node-local services and bypassing the dedicated CoreDNS (NET-018).
- `spec.hostIPC: true` — would share SysV IPC and POSIX message queues with the node, allowing cross-container/cross-pod communication outside of the audited gateway API surface.

The admission webhook ([§10.2](10_gateway-internals.md#102-authentication)) rejects any CR that would produce a pod spec with any of these fields set to `true` with `POD_SPEC_HOST_SHARING_FORBIDDEN` (field-level detail in the error body: which field(s) tripped the rejection). The startup preflight Job additionally verifies that all Lenny-managed Deployments, DaemonSets, and Jobs have `shareProcessNamespace`, `hostPID`, `hostNetwork`, and `hostIPC` either unset or explicitly `false` in their pod templates and fails (hard-fail in production) if any are non-compliant. These controls apply regardless of sidecar count: even pods that currently have only one container are forbidden from enabling them, so a future sidecar addition cannot silently weaken isolation.

**Cross-UID file delivery without `CAP_CHOWN` (fsGroup-based).** The "All dropped" capability posture above includes `CAP_CHOWN` — no agent-pod container (init or sidecar) may invoke `chown(2)` to reassign file ownership across UIDs. This constrains how the adapter (running as the adapter UID) delivers the credential file [`/run/lenny/credentials.json`](04_system-components.md#47-runtime-adapter) to the agent container (running as a distinct agent UID). Rather than relax the capability posture, Lenny's pod templates solve this entirely with Kubernetes-native primitives: the tmpfs volume carrying the credential file (and the adapter manifest) is mounted with `spec.securityContext.fsGroup: <lenny-cred-readers GID>` at the pod level, which causes the kubelet to set group ownership and group-readable/writable semantics on every file in the volume at mount time with no `chown` syscall executed inside the containers. Both the adapter UID and the agent UID are declared in the pod's `spec.securityContext.supplementalGroups` list (or, equivalently, `runAsGroup` for the adapter and `supplementalGroups` for the agent), making `lenny-cred-readers` a shared group of which both containers are members. The credential file is then written by the adapter with mode `0440` — owner-writable and group-readable, no access for other UIDs — and the agent reads it through group membership. No capability is added, the `CAP_CHOWN` drop is universal, and the cross-UID ownership of [§4.7](04_system-components.md#47-runtime-adapter) item 4 is satisfied by group-owned read access rather than by reassigning owner UID. The admission webhook and `lenny-preflight` Job validate the presence and immutability of the `fsGroup` and `supplementalGroups` settings on every agent-pod template; a pod template missing the `lenny-cred-readers` fsGroup is rejected with `POD_SPEC_CRED_FSGROUP_MISSING` at admission time, because the agent would otherwise be unable to read its credential file.

**`lenny-cred-readers` membership boundary.** The `lenny-cred-readers` supplementary group is the credential-file read boundary inside the pod; its membership is deliberately narrow. The intended members are exactly two UIDs: the adapter UID (which writes the file) and the agent UID (which reads it). No other sidecar, init container, debug container (when ephemeral containers are attached), or operator-injected container in a Lenny-managed agent pod may include `lenny-cred-readers` in its `runAsGroup` or `supplementalGroups`. The admission webhook enforces this by rejecting any agent-pod template whose non-adapter, non-agent container declares the `lenny-cred-readers` GID with `POD_SPEC_CRED_GROUP_OVERBROAD`. **Ephemeral debug containers attached post-hoc via `kubectl debug` are not pinned to a non-privileged UID by any Kubernetes default** — `EphemeralContainer.securityContext.runAsUser`/`runAsGroup`/`supplementalGroups`, when set, override pod-level defaults at kubelet level, and an actor with `update` on `pods/ephemeralcontainers` (e.g., an SRE-tooling ServiceAccount whose RBAC was not scoped away from agent namespaces) could otherwise attach a container declaring `runAsUser: <agent UID>` and `supplementalGroups: [<lenny-cred-readers GID>]`, read the pod's live spec to discover these values, and acquire group-read on `/run/lenny/credentials.json`. Lenny therefore ships a dedicated `lenny-ephemeral-container-cred-guard` `ValidatingAdmissionWebhook` scoped to the `pods/ephemeralcontainers` subresource in every agent namespace (see [Section 17.2](17_deployment-topology.md#172-namespace-layout) admission-policies inventory item 13). The webhook rejects, fail-closed, any ephemeral-container request where (i) `container.securityContext.runAsUser` equals the target pod's adapter UID or agent UID, or (ii) `container.securityContext.supplementalGroups` or `container.securityContext.runAsGroup` includes the `lenny-cred-readers` GID, or (iii) any of `runAsUser`, `runAsGroup`, or `supplementalGroups` is absent on the ephemeral container's `securityContext` (absent values would otherwise inherit pod-level defaults, including the fsGroup that grants `/run/lenny/credentials.json` read access). Rejections carry `EPHEMERAL_CONTAINER_CRED_UID_FORBIDDEN` ([Section 15.1](15_external-api-surface.md#151-rest-api)); webhook unavailability raises the `EphemeralContainerCredGuardUnavailable` alert ([Section 16.5](16_observability.md#165-alerting-rules-and-slos)). Within the agent container itself, the agent process is the single credential consumer — if the runtime fork-execs user-controlled subprocesses, those subprocesses inherit the agent UID and the supplementary group by default. Runtime authors MUST either (a) avoid spawning subprocesses that should not see credentials, or (b) invoke `setgroups(0, NULL)` in a pre-exec step to drop the supplementary group before `execve`. The spec does not mandate a specific subprocess-isolation mechanism (no AppArmor/Seccomp profile is required), because the agent container is already a single-tenant, single-session trust boundary in all modes except `executionMode: concurrent` with `concurrencyStyle: workspace` (see concurrent-mode clause below). For single-session and task-mode pods, the subprocess-inheritance surface is inside the trust boundary the session already owns.

**Concurrent-workspace mode credential-read scope.** In `executionMode: concurrent`, `concurrencyStyle: workspace` pools ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)), multiple slots share the same pod, the same agent UID, and therefore the same `lenny-cred-readers` group membership. Each slot's per-slot credential file at `/run/lenny/slots/{slotId}/credentials.json` ([§6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)) is mode `0440` with `lenny-cred-readers` group ownership, so **any slot's agent code can read every other slot's credential file** via filesystem access. Lenny does not mitigate this at the pod level — per-slot tmpfs mounts with distinct per-slot GIDs are not used in v1. This property is covered by the existing `acknowledgeProcessLevelIsolation` deployer flag ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) alongside shared process namespace, `/tmp`, cgroup memory, and network stack; cross-slot credential-file readability is an instance of the same process-level co-tenancy the deployer has already accepted. Deployers requiring strict credential-lease isolation between simultaneous tasks MUST use `executionMode: session` (one session per pod) or `executionMode: task` (one task per pod, sequential reuse). The concurrent-workspace pool validation in [§4.7](04_system-components.md#47-runtime-adapter) pool admission additionally emits a warning-class condition `ConcurrentWorkspaceCredentialSharing=True` on the `SandboxWarmPool` CRD whenever a concurrent-workspace pool is created against a Runtime with non-empty `supportedProviders` (i.e., a credential-bearing runtime), ensuring the property is visible in pool status alongside the other concurrent-workspace tradeoffs.

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
        - port: "{{ .Values.observability.otlpPort }}" # default: 4317 (gRPC OTLP — TLS required, see note below)
          protocol: TCP
  policyTypes: [Egress]
```

> **Note:** If the OTLP collector runs outside the cluster (e.g., a cloud-managed tracing backend), the egress rule uses an `ipBlock` CIDR (`{{ .Values.observability.otlpCIDR }}`) instead of a namespace/pod selector. The `lenny-preflight` Job validates that the configured OTLP endpoint is reachable from agent namespaces when `observability.otlpEndpoint` is set.

> **OTLP TLS requirement (NET-059, OTLP-068):** Trace payloads carry session metadata, tenant/operation identifiers, and occasional error bodies — the collector hop is in-scope for the same confidentiality posture as the other cross-namespace platform links (Gateway→Token Service, Gateway→Redis, Gateway→MinIO). The OTLP egress therefore MUST run over TLS. The Helm value `observability.otlpTlsEnabled` (**default: `true`** in all production profiles; overridable only to `false` in the dev/`make run` profile for local Jaeger/stdout exporters) controls this: when `true`, the runtime's OTel SDK is configured with TLS (gRPC on port 4317 with TLS applied over gRPC — OTLP's canonical port is reused, matching the upstream OpenTelemetry design; deployers who prefer OTLP/HTTP may set `observability.otlpPort: 4318` and expose an HTTPS endpoint instead). Collector certificate expectations: the collector's server certificate MUST be signed by a CA that the agent pods trust (the deployer's cluster-wide trust bundle injected via the standard Kubernetes `ca-certificates` mechanism, or a deployer-supplied CA bundle mounted at `/etc/ssl/certs/otlp-collector-ca.crt` when `observability.otlpCaBundle` is set); the certificate's SAN MUST cover the configured `observability.otlpEndpoint` hostname.
>
> **Plaintext opt-in guard (OTLP-068).** Setting `observability.otlpTlsEnabled: false` in any non-dev profile (`global.devMode: false`) additionally requires the explicit `observability.acknowledgeOtlpPlaintext: true` Helm value. The chart's `required`/fail-on-missing-value guard refuses to render when `otlpTlsEnabled: false` is combined with `acknowledgeOtlpPlaintext: false` (or unset) outside dev mode, failing `helm install`/`helm upgrade` with `"observability.otlpTlsEnabled is false without observability.acknowledgeOtlpPlaintext=true; plaintext OTLP export exposes tenant/session metadata in-cluster. Set acknowledgeOtlpPlaintext=true to proceed, or re-enable TLS (OTLP-068)."`. When the guard succeeds (acknowledged plaintext), Helm's post-render NOTES output prints a loud deprecation banner: `"WARNING: Plaintext OTLP export enabled — trace payloads are sent without TLS. Tenant metadata, session identifiers, and error bodies are readable by any in-cluster interceptor. Re-enable TLS before production use (OTLP-068; §13.2)."`. The `lenny-preflight` Job additionally performs a live TLS handshake probe (the `otlp-tls` check in [§17.9](17_deployment-topology.md#179-deployment-answer-files)) against the configured collector endpoint when `otlpTlsEnabled: true`: it opens a TCP connection to the endpoint, performs a TLS 1.2+ handshake with the deployer's trust bundle, validates the server certificate's SAN against `observability.otlpEndpoint`, and fails the install if the handshake does not complete or the SAN does not match. The preflight also fails if `observability.otlpEndpoint` begins with `http://` (rather than `https://`) while `observability.otlpTlsEnabled` is `true`, to catch the common misconfiguration where the endpoint scheme contradicts the TLS flag. Runtime plaintext detection is surfaced via the `OTLPPlaintextEgressDetected` critical alert in [§16.5](16_observability.md#165-alerting-rules-and-slos), fed by the gateway/pod OTel exporter emitting `lenny_otlp_export_tls_handshake_total{result="plaintext"}` whenever a configured exporter connects without negotiating TLS.

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

> **Normative selector requirement (NET-047, NET-050).** All Lenny-rendered NetworkPolicy clauses that target a `lenny-system` platform component (gateway, token-service, controller, pgbouncer, minio, admission-webhook, coredns) — whether via same-namespace `podSelector` or cross-namespace `namespaceSelector`/`podSelector` — MUST use the canonical `lenny.dev/component` label key with the component-specific value (e.g., `lenny.dev/component: gateway`). The legacy `app: lenny-gateway` and sibling `app: lenny-<component>` keys MUST NOT appear on either side of a NetworkPolicy rule that references a `lenny-system` component, because selector drift between policies silently produces either no-match (effective deny-all) or wrong-match (over-broad allow) at runtime with no admission-time error. Exception 1: the `lenny-ops` service (Section 25.4) and its Jobs (e.g., `lenny-backup`) use `app: lenny-ops` / `app: lenny-backup` as their pod selector because they are a separate operability plane with its own chart scope and namespace — rules that target those pods therefore use the `app:` key, and this is not considered selector drift. Exception 2 (additive egress-narrowing labels, NET-068): an egress allow-list clause MAY include an additional, per-pod key (e.g., `lenny.dev/webhook-name: drain-readiness`) alongside the canonical `lenny.dev/component` key when the purpose is to narrow an otherwise over-broad egress rule to a specific pod subset — the canonical key MUST still be present, and the additive key is permitted in egress allow-lists only (never in ingress, where the canonical key alone governs peer admission). The `lenny-preflight` Job performs a selector-consistency audit at install and upgrade time: it enumerates every NetworkPolicy rendered by the chart, resolves each `podSelector`/`namespaceSelector` against the live cluster, and fails the install if (a) any selector targeting a `lenny-system` platform component uses a key other than `lenny.dev/component` (additive keys permitted by Exception 2 are allowed only when paired with the canonical key AND only in egress rules), (b) any selector matches zero pods for a component that is expected to be running given the rendered Helm values, or (c) any ingress-side rule contains an additive per-pod key beyond the canonical `lenny.dev/component` label. This check is intentionally strict — a silently non-matching NetworkPolicy is more dangerous than a missing one.

> **DNS egress peer requirement (NET-067).** Every Lenny-rendered NetworkPolicy egress rule that permits UDP/53 or TCP/53 MUST pair its `namespaceSelector` with a destination `podSelector` matching the concrete DNS-server pod label: `k8s-app: kube-dns` for `kube-system` CoreDNS, or `lenny.dev/component: coredns` for the dedicated `lenny-system` CoreDNS. A namespace-only selector on `kube-system` reaches every pod in that namespace (metrics-server, kube-proxy, CSI drivers, cloud-provider controllers) on UDP/TCP 53 — and any future custom DNS/relay pod an operator co-locates in `kube-system` becomes reachable too. The `lenny-preflight` Job enforces this via the "NetworkPolicy DNS `podSelector` parity" check ([Section 17.6](17_deployment-topology.md#checks-performed)) and fails the install/upgrade on any DNS egress rule whose peer omits `podSelector`.


| Component                                                                            | Egress Allowed                                                                                                                                                                                                                                                    | Ingress Allowed                                                                                                                     |
| ------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| **Gateway** (`lenny.dev/component: gateway`)                                         | Agent namespaces (TCP 50051, adapter gRPC); Token Service (TCP `{{ .Values.tokenService.grpcPort }}` — default 50052, mTLS); PgBouncer (TCP 5432); Redis (TCP 6380, TLS — `{{ .Values.redis.tlsPort }}`); MinIO (TCP 9443, TLS — `{{ .Values.minio.tlsPort }}`); kube-apiserver (TCP 443, CIDR `{{ .Values.kubeApiServerCIDR }}`); `kube-system` CoreDNS (UDP/TCP 53); external HTTPS (TCP 443, rendered as two parallel `ipBlock` peers per NET-062 — a `cidr: 0.0.0.0/0` peer with `except` covering IPv4 cluster pod/service CIDRs, RFC1918 private ranges (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`), IPv4 link-local (`169.254.0.0/16`), and IPv4 IMDS addresses (`169.254.169.254/32`, `100.100.100.200/32`); and a `cidr: ::/0` peer with `except` covering any IPv6 cluster CIDRs, IPv6 ULA (`fc00::/7`), IPv6 link-local (`fe80::/10`), and the IPv6 IMDS address (`fd00:ec2::254/128`) — required for LLM Proxy upstream forwarding to LLM provider APIs, connector callback delivery, and webhook notifications; see [Section 4.8](04_system-components.md#48-gateway-policy-engine) and [Section 4.9](04_system-components.md#49-credential-leasing-service)). The chart partitions the combined `egressCIDRs.excludePrivate` and `egressCIDRs.excludeIMDS` lists by address family at render time because Kubernetes `NetworkPolicySpec.egress[].to[].ipBlock` rejects cross-family `except` entries (NET-062). The RFC1918/ULA/link-local `except` entries are rendered from the shared `egressCIDRs.excludePrivate` Helm value (default list above), mirroring the `lenny-ops-egress` webhook rule ([Section 25.4](25_agent-operability.md#254-the-lenny-ops-service)) so that the two highest-risk SSRF surfaces — gateway outbound (tenant-influenced URLs via LLM base URLs, connector callbacks, webhooks, interceptors) and `lenny-ops` webhook delivery — share one normative private-range block list (NET-057). In-cluster external interceptor namespaces: for each namespace declared in `{{ .Values.gateway.interceptorNamespaces }}` (default: `[]`), the Helm chart renders a supplemental egress rule allowing TCP `{{ .Values.gateway.interceptorGRPCPort }}` (default 50053) to that namespace — required for the gateway to reach in-cluster gRPC interceptors ([Section 4.8](04_system-components.md#48-gateway-policy-engine), NET-039). | External ingress (TCP 443 from Ingress controller namespace — `{{ .Values.ingressControllerNamespace }}`, default: `ingress-nginx`); agent namespaces (TCP 50051, gateway gRPC port for pod-to-gateway control traffic; TCP 8443, LLM proxy port for proxy-mode pods); admission-webhook pods (TCP `{{ .Values.gateway.internalPort }}` — default 8080, internal HTTP port for `lenny-drain-readiness` callbacks to `GET /internal/drain-readiness` — NET-037); `lenny-ops` pods (TCP `{{ .Values.gateway.internalTLSPort }}` — default 8443, admin-API over TLS — the default when `ops.tls.internalEnabled: true`, NET-070; or TCP `{{ .Values.gateway.internalPort }}` — default 8080, admin-API over plaintext — rendered only when the explicit `ops.acknowledgePlaintextAdminAPI: true` opt-out is set (or in dev mode); for health aggregation, configuration, backup orchestration, remediation, upgrade, diagnostics, and connector probes — see [Section 25.3](25_agent-operability.md#253-gateway-side-ops-endpoints) — NET-051, NET-070). The `lenny-ops` ingress rule is rendered with `from: [{ namespaceSelector: { matchLabels: { kubernetes.io/metadata.name: "{{ .Release.Namespace }}" } }, podSelector: { matchLabels: { app: lenny-ops } } }]` per the selector exception at line 201 (`lenny-ops` uses `app: lenny-ops`, not `lenny.dev/component`). |
| **Token Service** (`lenny.dev/component: token-service`)                   | PgBouncer (TCP 5432); Redis (TCP 6380, TLS — `{{ .Values.redis.tlsPort }}`); KMS endpoint (HTTPS 443, CIDR from `{{ .Values.kms.endpointCIDR }}`); `kube-system` CoreDNS (UDP/TCP 53).                                                                           | Gateway pods only (TCP `{{ .Values.tokenService.grpcPort }}` — default 50052, mTLS).                                               |
| **Warm Pool Controller / PoolScalingController** (`lenny.dev/component: controller`) | kube-apiserver (TCP 443); PgBouncer (TCP 5432); `kube-system` CoreDNS (UDP/TCP 53).                                                                                                                                                                               | None (controllers initiate all connections).                                                                                        |
| **PgBouncer** (`lenny.dev/component: pgbouncer`) — self-managed profile only; absent on cloud-managed deployments where the provider proxy is external to the cluster | Postgres (TCP 5432, CIDR from `{{ .Values.postgres.host }}`); `kube-system` CoreDNS (UDP/TCP 53). | Gateway pods (TCP 5432); Token Service pods (TCP 5432); Warm Pool Controller / PoolScalingController pods (TCP 5432); `lenny-ops` pods (TCP 5432 — audit queries, upgrade state, backup metadata; NET-051). The `lenny-ops` ingress clause uses `podSelector: { matchLabels: { app: lenny-ops } }` paired with `namespaceSelector: { matchLabels: { kubernetes.io/metadata.name: "{{ .Release.Namespace }}" } }` per the selector exception at line 201. |
| **MinIO** (`lenny.dev/component: minio`) — self-managed profile only; absent on cloud-managed deployments where the provider's native object storage (S3, GCS, Azure Blob) is used instead | `kube-system` CoreDNS (UDP/TCP 53). | Gateway pods (TCP 9443, TLS — `{{ .Values.minio.tlsPort }}`); `lenny-ops` pods (TCP 9443, TLS — `{{ .Values.minio.tlsPort }}` — backup/restore object operations; NET-051); `lenny-backup` Job pods (TCP 9443, TLS — `{{ .Values.minio.tlsPort }}`). The `lenny-ops` and `lenny-backup` ingress clauses use `podSelector: { matchLabels: { app: lenny-ops } }` / `{ app: lenny-backup }` paired with `namespaceSelector: { matchLabels: { kubernetes.io/metadata.name: "{{ .Release.Namespace }}" } }` per the selector exception at line 201. |
| **Admission Webhooks** (`lenny.dev/component: admission-webhook`) — the nine webhook Deployments enumerated in [§17.2](17_deployment-topology.md#172-namespace-layout) (`lenny-label-immutability`, `lenny-direct-mode-isolation`, `lenny-sandboxclaim-guard`, `lenny-data-residency-validator`, `lenny-pool-config-validator`, `lenny-t4-node-isolation`, `lenny-drain-readiness`, `lenny-crd-conversion`, `lenny-ephemeral-container-cred-guard`). All nine Deployments carry the `lenny.dev/component: admission-webhook` label so that the single `podSelector` in this row matches every webhook pod (see NET-047/NET-050 above for the selector-consistency audit that enforces this). The `lenny-drain-readiness` Deployment additionally carries the per-webhook additive label `lenny.dev/webhook-name: drain-readiness`, which narrows the gateway-egress sub-rule below to that single webhook (NET-068; see the additive-label exception clarified in the line 203 invariant and the §17.2 canonical-label note). | Two sub-rules rendered as separate `egress` peers within this NetworkPolicy: **(a) base egress for all nine webhooks** (selector `lenny.dev/component: admission-webhook`) — `kube-system` CoreDNS (UDP/TCP 53), required for kube-apiserver watch connections and generic name resolution. **(b) drain-readiness-only egress** (selector `lenny.dev/component: admission-webhook` AND `lenny.dev/webhook-name: drain-readiness`) — Gateway internal HTTP port (TCP `{{ .Values.gateway.internalPort }}` — default 8080) for `GET /internal/drain-readiness` callbacks (NET-037). Without sub-rule (b), the `lenny-system` default-deny policy blocks the drain-readiness callback, causing all pod evictions to be permanently rejected by the fail-closed webhook; without sub-rule (b)'s additive `webhook-name` selector, the other eight webhooks (purely in-process validators) would inherit unnecessary reachability to the gateway's internal admin port in violation of least privilege (NET-068). | kube-apiserver (TCP 443, CIDR `{{ .Values.webhookIngressCIDR }}` — default `0.0.0.0/0`). The kube-apiserver must reach these pods to invoke ValidatingAdmissionWebhook callbacks. Without this ingress rule, the `lenny-system` default-deny policy blocks all webhook callbacks, causing fail-closed webhooks to reject all pod admissions silently. Ingress selectors for this row use only the canonical `lenny.dev/component: admission-webhook` label — the additive `lenny.dev/webhook-name` label is permitted in egress allow-lists only, never in ingress (NET-068). See the `webhookIngressCIDR` note below for cloud-specific tightening guidance. |
| **Dedicated CoreDNS** (`lenny.dev/component: coredns`)                               | `kube-system` CoreDNS (UDP/TCP 53, for upstream forwarding); external DNS resolvers if configured.                                                                                                                                                                | Agent namespace pods (UDP/TCP 53, per `allow-pod-egress-base` in agent namespaces); monitoring namespace (TCP 9153, Prometheus metrics scrape). |
| **OTLP Collector** (matched by `{{ .Values.observability.otlpPodLabel }}: {{ .Values.observability.otlpPodLabelValue }}` — default `app: otel-collector`) — **conditional row**: rendered only when `observability.otlpEndpoint` is set **and** `observability.otlpNamespace == lenny-system` (default). When `otlpNamespace` is set to a separate namespace (e.g., `observability`), this ingress rule is rendered in that namespace instead, and the `lenny-system` default-deny policy intentionally does not cover the collector. | Not governed by this row — the OTLP collector is a deployer-supplied workload and its egress is outside Lenny's chart scope. | Agent namespace pods (TCP `{{ .Values.observability.otlpPort }}` — default 4317, gRPC OTLP) — paired with the `allow-pod-egress-otlp` egress rule in agent namespaces (see §13.3, NET-046). Without this ingress row, `allow-pod-egress-otlp` permits egress out of the agent namespace but the `lenny-system` default-deny policy silently drops it on the collector side. |
| **`lenny-ops`** (`app: lenny-ops`) — mandatory operability plane co-located in `{{ .Release.Namespace }}` (default `lenny-system`; [Section 25.4](25_agent-operability.md#254-the-lenny-ops-service)). Selector uses `app: lenny-ops` per the selector exception stated at line 201 (`lenny-ops` is a separate chart scope and does not carry `lenny.dev/component`). When `{{ .Release.Namespace }}` resolves to a namespace other than `lenny-system`, these rules are rendered in that namespace instead; the `lenny-system` default-deny policy still applies to the counterparty ingress rules on Gateway, PgBouncer, and MinIO (see those rows above). | Gateway (TCP `{{ .Values.gateway.internalTLSPort }}` — default 8443, admin-API over TLS — the default when `ops.tls.internalEnabled: true`, NET-070); Gateway (TCP `{{ .Values.gateway.internalPort }}` — default 8080, admin-API over plaintext — rendered only when `ops.tls.internalEnabled: false` AND `ops.acknowledgePlaintextAdminAPI: true`, or in dev mode); PgBouncer (TCP 5432); Redis (TCP `{{ .Values.redis.tlsPort }}` — default 6380, TLS); MinIO (TCP `{{ .Values.minio.tlsPort }}` — default 9443, TLS); Prometheus (TCP 9090 — Prometheus HTTP API in `{{ .Values.monitoring.namespace }}`); kube-apiserver (TCP 443, CIDR `{{ .Values.kubeApiServerCIDR }}`); `kube-system` CoreDNS (UDP/TCP 53); external HTTPS (TCP 443 for webhook delivery, rendered as two parallel `ipBlock` peers per NET-062 — a `cidr: 0.0.0.0/0` peer with `except` clauses covering RFC1918 and IPv4 link-local, and a `cidr: ::/0` peer with `except` clauses covering IPv6 ULA and IPv6 link-local — see [Section 25.4](25_agent-operability.md#254-the-lenny-ops-service) `lenny-ops-egress`). The chart emits exactly one of the two gateway egress port allows at render time — the TLS port in non-dev profiles by default, or the plaintext port only when the explicit plaintext acknowledgment is present; this keeps the allow-list aligned with the transport the `GatewayClient` actually negotiates (NET-070). | External ingress (TCP 8090, the `lenny-ops` Service port) from the Ingress controller namespace — `{{ .Values.ingress.controllerNamespace }}` paired with the controller pod selector (see [Section 25.4](25_agent-operability.md#254-the-lenny-ops-service) `lenny-ops-allow-ingress-from-ingress-controller`); monitoring namespace (TCP 9090 — Prometheus scrape of the `lenny-ops` metrics port). No in-cluster workload other than the Ingress controller may reach `lenny-ops` (the default-deny plus these two explicit allows enforce the "external by design" property documented in [Section 25.4](25_agent-operability.md#254-the-lenny-ops-service)). |

> **`lenny-ops` counterparty rules (NET-051):** The `lenny-ops`-originated flows enumerated above cross the `lenny-system` default-deny boundary on both sides. The rendered chart therefore emits three categories of rules: (a) an egress policy on `lenny-ops` pods (`lenny-ops-egress`, [Section 25.4](25_agent-operability.md#254-the-lenny-ops-service)), (b) ingress clauses on each target pod (Gateway/PgBouncer/MinIO rows above; a Redis ingress clause is emitted alongside the Gateway and Token Service allow-rules when Redis is self-managed), and (c) the monitoring-scrape ingress covered by the NET-045 Prometheus monitoring ingress note below (`lenny-ops` is included in the list of `lenny-system` components whose metrics port is scrapeable from `{{ .Values.monitoring.namespace }}`). The `lenny-preflight` Job's selector-consistency audit (NET-047/050, line 201) extends to these rules: it verifies that every `lenny-ops` counterparty clause uses `podSelector: { matchLabels: { app: lenny-ops } }` paired with `namespaceSelector: { matchLabels: { kubernetes.io/metadata.name: "{{ .Release.Namespace }}" } }`, and fails the install if any clause is missing when `lenny-ops` is deployed (which it always is — `lenny-ops` is mandatory per [Section 25.4](25_agent-operability.md#254-the-lenny-ops-service)). The audit additionally verifies that the gateway-counterparty port in both the `lenny-ops` egress rule and the gateway ingress rule is the TLS port (`{{ .Values.gateway.internalTLSPort }}`) when `ops.tls.internalEnabled: true` and the plaintext port (`{{ .Values.gateway.internalPort }}`) only when the explicit `ops.acknowledgePlaintextAdminAPI: true` opt-out is present (NET-070). Without these rules, the `lenny-system` default-deny silently breaks every operability flow at install time — health aggregation, configuration reads/writes, backup orchestration, restore execution, remediation locks, platform upgrade, recommendations, diagnostics, and connector probes all return `connection timed out` at the network layer with no audit signal.

> **Prometheus monitoring ingress (NET-045):** The `lenny-system` default-deny policy blocks all unsolicited ingress, including Prometheus scrape requests from the monitoring namespace. Without explicit ingress rules, all `lenny-system` component metrics (including `lenny_gateway_active_sessions`, `lenny_network_policy_cidr_drift_total`, HPA-driving metrics like `lenny_gateway_request_queue_depth`, and CoreDNS `prometheus :9153`) would be unscrapeable, breaking the observability and autoscaling pipeline. The Helm chart renders a supplemental ingress NetworkPolicy for each `lenny-system` component that exposes a metrics endpoint, allowing TCP ingress on the component's metrics port from the namespace specified in `{{ .Values.monitoring.namespace }}` (default: `monitoring`). The affected components and their metrics ports are: Gateway (`{{ .Values.gateway.metricsPort }}`, default 9090), Warm Pool Controller / PoolScalingController (`{{ .Values.controller.metricsPort }}`, default 9090), Token Service (`{{ .Values.tokenService.metricsPort }}`, default 9090), Dedicated CoreDNS (TCP 9153), and `lenny-ops` (TCP 9090 — the Prometheus scrape port declared by `prometheus.io/port: "9090"` on the `lenny-ops` Deployment; selector `app: lenny-ops` per the line 201 exception; NET-051). The `lenny-preflight` Job validates that the monitoring namespace exists and contains at least one pod matching `app.kubernetes.io/name: prometheus` (or the label configured in `{{ .Values.monitoring.podLabel }}`), warning (non-blocking) if no Prometheus pods are found.

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

The gateway must accept external HTTPS traffic forwarded by the cluster's Ingress controller. The `{{ .Values.ingressControllerNamespace }}` Helm value (default: `ingress-nginx`) identifies the namespace in which the Ingress controller pods run. The `{{ .Values.ingress.controllerPodLabel.key }}` / `{{ .Values.ingress.controllerPodLabel.value }}` Helm values (defaults: `app.kubernetes.io/name` / `ingress-nginx`) identify the pod label applied to the Ingress controller pods themselves — required so that only the controller pods (not every pod in the namespace, e.g., sidecars, cert-manager validators, debug containers) may reach the gateway's TLS listener. The Helm chart renders the following NetworkPolicy in `lenny-system`:

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
          podSelector:
            matchLabels:
              "{{ .Values.ingress.controllerPodLabel.key }}": "{{ .Values.ingress.controllerPodLabel.value }}" # default: app.kubernetes.io/name=ingress-nginx
      ports:
        - port: 443 # {{ .Values.gateway.httpsPort }} — external TLS listener
          protocol: TCP
  policyTypes: [Ingress]
```

> **`ingressControllerNamespace` / `ingress.controllerPodLabel` Helm values (NET-038, NET-049):** `ingressControllerNamespace` must match the actual namespace in which the Ingress controller pods run (e.g., `ingress-nginx`, `traefik`, `kourier-system`). `ingress.controllerPodLabel.key` / `ingress.controllerPodLabel.value` must match a label applied to the Ingress controller pods themselves (defaults target the standard `ingress-nginx` chart labels). When the `from:` clause pairs the `namespaceSelector` with the `podSelector` (as rendered above), only Ingress controller pods can reach the gateway — sidecars, cert-manager validators, or debug containers co-located in the same namespace are denied. If either value is set incorrectly, the `lenny-system` default-deny policy blocks all traffic from the Ingress controller, making the gateway unreachable from the internet. The `lenny-preflight` Job (NET-038) validates that (a) a namespace with the configured name exists in the cluster, (b) at least one running pod in that namespace carries the configured `controllerPodLabel`, and warns if either check fails, catching the most common misconfigurations at deploy time.

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
>           podSelector:
>             matchLabels:
>               lenny.dev/component: interceptor # pairs with namespaceSelector (AND) — gateway egress is restricted to interceptor pods only, not other tenant-operated pods co-located in the namespace (NET-058)
>       ports:
>         - port: "{{ .Values.gateway.interceptorGRPCPort }}" # default 50053 — interceptor gRPC listen port
>           protocol: TCP
>   policyTypes: [Egress]
> ```
>
> When `gateway.interceptorNamespaces` is empty (the default), no supplemental policies are rendered, which is safe — no external interceptors are registered in this configuration. Deployers who register external interceptors that run outside the cluster (reachable via an external IP already covered by the `0.0.0.0/0` external HTTPS rule) do not need to add entries to `gateway.interceptorNamespaces`. Deployers who deploy in-cluster external interceptors **must** label the interceptor Deployment's pod template with `lenny.dev/component: interceptor` so the rendered NetworkPolicy's `podSelector` matches; without this label, the `lenny-system` default-deny policy blocks all gateway-to-interceptor gRPC calls. The `lenny-preflight` Job validates that each declared interceptor namespace exists in the cluster, warns if it has zero running pods, and warns if no pod in the namespace carries the `lenny.dev/component: interceptor` label (NET-058).
>
> The `gateway.interceptorGRPCPort` Helm value (default: `50053`) defines the port the Helm-rendered NetworkPolicy rules allow. Individual interceptor registrations may bind on different ports; deployers should ensure their interceptor pods listen on this port (or override the Helm value to match). Note that this value governs the NetworkPolicy port allowance only — the actual gRPC endpoint address used by the gateway to call each interceptor is specified in the interceptor registration configuration, not in this NetworkPolicy.

**Gateway pod egress to upstream LLM providers (NET-046).** Any deployment that runs proxy-mode credential pools performs outbound LLM calls directly from the gateway process (the native Go translator, [§4.9](04_system-components.md#49-credential-leasing-service) LLM Reverse Proxy). Every outbound LLM request is constrained by the gateway pod's egress NetworkPolicy. Without an explicit allowlist, a compromised gateway process could attempt to reach arbitrary destinations, defeating the point of running it in a confined container.

The Helm chart renders `allow-gateway-egress-llm-upstream` whenever `credentialPools[*].deliveryMode: proxy` is present in the rendered configuration. The policy enumerates upstream provider CIDRs that the pods actually reference via `credentialPools[*].provider`. Kubernetes `NetworkPolicy` only matches by `ipBlock` (CIDR) or pod/namespace selectors — it does **not** resolve DNS names — so the chart accepts CIDR entries only; the `lenny-preflight` Job resolves the configured provider endpoints to CIDRs at render time and fails the install with a clear error if any entry in `egressCIDRs.llmProviders` is not a valid CIDR (resolving hostnames is the operator's responsibility):

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-gateway-egress-llm-upstream
  namespace: lenny-system
spec:
  podSelector:
    matchLabels:
      lenny.dev/component: gateway
  policyTypes: [Egress]
  egress:
    # Two parallel `ipBlock` peers are emitted — one per address family —
    # because Kubernetes `NetworkPolicySpec.egress[].to[].ipBlock` requires
    # every entry in `except` to be contained within the same-family `cidr`
    # of its enclosing block; mixing IPv4 and IPv6 CIDRs in one block is
    # rejected by the API server and by strict CNIs (Cilium, Calico) and
    # silently drops entries under lenient CNIs (NET-062). The Helm
    # template partitions `egressCIDRs.excludePrivate`,
    # `egressCIDRs.excludeIMDS`, and the cluster pod/service CIDRs by
    # address family at render time and emits one peer per family.
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except:
              # Block IPv4 cluster-internal CIDRs
              - "{{ .Values.egressCIDRs.excludeClusterPodCIDR }}"
              - "{{ .Values.egressCIDRs.excludeClusterServiceCIDR }}"
              # Block RFC1918 private IPv4 ranges and IPv4 link-local —
              # rendered from the IPv4 entries of the shared
              # `egressCIDRs.excludePrivate` Helm value (default whole
              # list: ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
              #  "169.254.0.0/16", "fc00::/7", "fe80::/10"]; the chart
              # partitions by address family at render time).
              # Gateway-initiated HTTPS reaches tenant-influenced URLs
              # (LLM provider base URLs, connector callbacks, webhook targets,
              # external interceptor endpoints — §4.8, §4.9) and therefore
              # has the same SSRF threat model as `lenny-ops` webhook
              # delivery (§25.4 `lenny-ops-egress`); every entry in
              # `egressCIDRs.excludePrivate` MUST appear on both surfaces
              # (NET-057) in the peer whose `cidr` matches its address
              # family. `lenny-preflight` fails the install if either
              # rule is missing an entry from the shared list.
              {{- range .Values.egressCIDRs.excludePrivate }}
              {{- if not (contains ":" .) }}
              - {{ . | quote }}
              {{- end }}
              {{- end }}
              # Block IPv4 cloud instance metadata endpoints
              - 169.254.169.254/32
              - 100.100.100.200/32
      ports:
        - protocol: TCP
          port: 443
    - to:
        - ipBlock:
            cidr: ::/0
            except:
              # Block IPv6 cluster-internal CIDRs, when the cluster is
              # dual-stack (`egressCIDRs.excludeClusterPodCIDRv6` /
              # `excludeClusterServiceCIDRv6` are set by preflight when
              # the cluster reports IPv6 pod/service ranges; omitted on
              # IPv4-only clusters).
              {{- with .Values.egressCIDRs.excludeClusterPodCIDRv6 }}
              - {{ . | quote }}
              {{- end }}
              {{- with .Values.egressCIDRs.excludeClusterServiceCIDRv6 }}
              - {{ . | quote }}
              {{- end }}
              # Block IPv6 ULA and IPv6 link-local — rendered from the
              # IPv6 entries of the shared `egressCIDRs.excludePrivate`
              # Helm value.
              {{- range .Values.egressCIDRs.excludePrivate }}
              {{- if contains ":" . }}
              - {{ . | quote }}
              {{- end }}
              {{- end }}
              # Block IPv6 cloud instance metadata endpoint
              - fd00:ec2::254/128
      ports:
        - protocol: TCP
          port: 443
```

**Default scope caveat.** The default `0.0.0.0/0`-with-`except` shape permits the gateway pod to reach any public IPv4 destination on port 443 except cluster internals, RFC1918 private ranges, IPv4 link-local, IPv6 ULA, IPv6 link-local, and IMDS. This is broader than "LLM providers only" — it is an "internet minus dangerous neighbors" allowlist. The `except` list is intentionally symmetric with the `lenny-ops-egress` webhook rule ([Section 25.4](25_agent-operability.md#254-the-lenny-ops-service)) because both surfaces face the same SSRF threat model: a tenant-influenced URL (LLM provider base URL, connector callback URL, webhook target, external interceptor endpoint) could otherwise reach internal corporate networks reachable from the cluster via VPC peers, transit gateway attachments, or on-prem links. The gateway is the higher-risk of the two surfaces and MUST NOT have weaker network-layer SSRF boundaries than `lenny-ops` (NET-057). The design accepts the remaining public-internet breadth because LLM provider IP ranges are globally distributed and change frequently; pinning every provider CIDR individually would churn on every provider's network change, and a narrow allowlist that silently drops legitimate provider traffic is a worse operational outcome than a broad allowlist that blocks the demonstrably dangerous destinations. Deployers with strict compliance profiles (e.g., FedRAMP High, regulated financial environments) SHOULD tighten further by setting `egressCIDRs.llmProviders` explicitly as a non-empty list of **CIDRs** (not hostnames — see Kubernetes NetworkPolicy limitation above). When set, the chart renders a narrower allowlist limited to those destinations; the `0.0.0.0/0` default rule is replaced, not supplemented. The `lenny-preflight` Job validates that every upstream endpoint referenced in `credentialPools` is reachable from `lenny-system` under the rendered policy and fails the install if any configured provider hostname resolves to a blocked CIDR. Because public-cloud LLM providers frequently rotate their underlying CIDRs, deployers who pin narrow CIDRs must re-run preflight after every provider network change; Lenny does not attempt to track upstream CIDR drift.

> **Shared private-range exclusion list (NET-057, NET-062):** The `egressCIDRs.excludePrivate` Helm value (default: `["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "fc00::/7", "fe80::/10"]`) is the single source of truth for RFC1918, IPv4 link-local, IPv6 ULA, and IPv6 link-local CIDRs that every `lenny-system` NetworkPolicy with a broad public-internet egress rule MUST include under `except`. Because Kubernetes `NetworkPolicySpec.egress[].to[].ipBlock` requires every `except` entry to share the address family of its enclosing `cidr` (mixing IPv4 and IPv6 is rejected by strict CNIs — Cilium, Calico — and silently drops entries under lenient CNIs, per NET-062), the chart renders **two parallel `ipBlock` peers per rule** — one with `cidr: 0.0.0.0/0` that carries the IPv4 entries of `excludePrivate`, and one with `cidr: ::/0` that carries the IPv6 entries. The partition is performed at template time; deployers supply one combined list and the chart splits it. The chart renders both peers into both the gateway `allow-gateway-egress-llm-upstream` rule above and the `lenny-ops-egress` webhook rule in [Section 25.4](25_agent-operability.md#254-the-lenny-ops-service); these are the two `lenny-system` surfaces that initiate outbound HTTPS to tenant-influenced URLs and therefore share one SSRF threat model. Deployers who run with non-standard private-network assignments (e.g., RFC6598 CGNAT `100.64.0.0/10` carved out for internal services that the gateway legitimately must reach, or the inverse: needing to block additional corporate ranges) override this list in Helm values; any override applies uniformly to both surfaces (and is placed into the family-matching peer) to preserve the symmetry guaranteed by NET-057. The `lenny-preflight` Job validates that every entry in `egressCIDRs.excludePrivate` appears in the `except` block of the same-family `ipBlock` peer on both the gateway external-HTTPS rule and the `lenny-ops-egress` webhook rule, and additionally runs an `ipblock-family-parity` check that fails the install if any rendered `ipBlock` contains an `except` entry whose address family does not match the enclosing `cidr`. Both checks are fail-closed — a deliberate guard against future edits that weaken only one surface, and against a silently broken dual-stack manifest. **Cluster-CIDR and IMDS symmetry (NET-065):** the same `egressCIDRs.excludeClusterPodCIDR` / `excludeClusterServiceCIDR` (plus the v6 variants on dual-stack clusters) and `egressCIDRs.excludeIMDS` values that the gateway rule renders into its `except` block MUST also appear in the `except` block of the `lenny-ops-egress` webhook rule ([Section 25.4](25_agent-operability.md#254-the-lenny-ops-service)); `excludePrivate` alone is insufficient on clusters using CGNAT-range pod CIDRs (`100.64.0.0/10`, the default on several managed Kubernetes providers) or custom non-RFC1918 pod CIDRs, where a tenant-influenced webhook URL resolving to an in-cluster pod IP would otherwise permit the operability-plane pod to dial gateway/controller/token-service pod IPs directly on their service ports. The `lenny-preflight` Job therefore extends the NET-022 cluster-CIDR audit (which currently covers the gateway external-HTTPS rule and agent `internet`-profile egress) to also cover `lenny-ops-egress`, and fails the install if the discovered cluster pod/service CIDRs are absent from the rendered `except` list of any of these three surfaces. Cluster-CIDR discovery and continuous drift detection re-use the mechanism documented under NET-022 below (preflight reads node `spec.podCIDR` aggregation and the `kubernetes` Service ClusterIP range; WarmPoolController re-reads every 5 minutes and fires `NetworkPolicyCIDRDrift`). The rendered-manifest conformance test fixtures cover both single-stack (IPv4-only) and dual-stack clusters so that an IPv6 entry missing from the `::/0` peer is caught before release. Application-layer SSRF checks in the gateway (LLM proxy URL validation, webhook target validation, interceptor URL validation — §4.8, §4.9) and in `lenny-ops` (webhook delivery `blockedCIDRs`, §25.4) run on top of this NetworkPolicy boundary; defense in depth is required because NetworkPolicy cannot inspect hostnames, DNS responses, or HTTP redirects.
>
> Application-layer SSRF checks are a hard requirement, not a fallback: NetworkPolicy alone cannot stop a legitimate public hostname that resolves (via attacker-controlled DNS or a reused public IP) to a CIDR outside this block list but still internal to the deployer's network. Every gateway-initiated HTTPS flow (connector callbacks, webhook notifications, LLM proxy upstreams, external interceptor calls) MUST validate the resolved destination against the same block list at the application layer before dialing.

There is no separate forward-HTTP-proxy between the gateway and upstream providers — the gateway process calls providers directly, subject to this NetworkPolicy. The gateway's LLM Proxy subsystem ([§4.9](04_system-components.md#49-credential-leasing-service) LLM Reverse Proxy) provides per-request visibility on the inbound side (every proxy-mode LLM request is observed by the subsystem's `PreLLMRequest` interceptor chain before the native translator runs); egress-side observability is provided via NetworkPolicy drop counters and the translation metrics (`lenny_gateway_llm_translation_duration_seconds`, `lenny_gateway_llm_translation_errors_total`, [§4.9](04_system-components.md#49-credential-leasing-service) Native translator).

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
> 1. **Preflight validation:** The `lenny-preflight` Job reads the cluster's actual pod and service CIDRs (from node `spec.podCIDR` aggregation and the `kubernetes` Service ClusterIP range) and fails the Helm install/upgrade if `egressCIDRs.excludeClusterPodCIDR` or `egressCIDRs.excludeClusterServiceCIDR` do not match, emitting: `"internet egress CIDR exclusion mismatch: excludeClusterPodCIDR is '<configured>' but cluster reports '<actual>'. Re-run with the correct CIDR to prevent lateral movement."` This check also fires when `internet` pools are present and either exclusion value is absent entirely. The same preflight audit also validates the gateway `allow-gateway-egress-llm-upstream` rule and the `lenny-ops-egress` webhook rule (§25.4): both `lenny-system` surfaces that render `cidr: 0.0.0.0/0` with an `except` block MUST carry the discovered cluster pod and service CIDRs (NET-065). A missing cluster CIDR on `lenny-ops-egress` is a fail-closed install error because the webhook surface dials tenant-influenced URLs and omitting the exclusion would permit a compromised operability-plane pod to reach in-cluster pod IPs on clusters with non-RFC1918 pod CIDRs.
>
> 2. **Continuous drift detection:** The WarmPoolController includes a goroutine that re-reads cluster CIDRs every 5 minutes and compares them against the installed NetworkPolicy `except` blocks for the `internet` egress profile, the gateway `allow-gateway-egress-llm-upstream` rule, and the `lenny-ops-egress` webhook rule. On drift, it increments `lenny_network_policy_cidr_drift_total` (counter, labeled `policy: internet|gateway-llm-upstream|ops-egress`, `field: pod_cidr|service_cidr`) and fires a `NetworkPolicyCIDRDrift` critical alert. The controller does **not** auto-patch NetworkPolicies (this would require NetworkPolicy write RBAC, currently avoided by design). The operator must re-run `helm upgrade` with the corrected CIDR values to re-sync. Additionally, the broad-internet CIDR rules in the `internet` profile (and any other supplemental policy containing broad CIDR rules) include `except` entries for all cloud instance metadata service (IMDS) addresses: `169.254.169.254/32` (AWS/GCP/Azure IPv4 IMDS), `fd00:ec2::254/128` (AWS IPv6 IMDS), and `100.100.100.200/32` (Alibaba Cloud IMDS). Because Kubernetes `NetworkPolicySpec.egress[].to[].ipBlock` forbids cross-family `except` entries (NET-062), the chart emits two parallel `ipBlock` peers per rule — one with `cidr: 0.0.0.0/0` carrying the IPv4 IMDS entries (`169.254.169.254/32`, `100.100.100.200/32`) and one with `cidr: ::/0` carrying the IPv6 IMDS entry (`fd00:ec2::254/128`). The `egressCIDRs.excludeIMDS` value remains a single list; the Helm template partitions it by address family at render time, matching the `excludePrivate` split described under NET-057 above. The base `allow-pod-egress-base` policy does NOT use `except` clauses — it is an allowlist-only policy (gateway gRPC + DNS only) that implicitly blocks IMDS because those addresses are not allowlisted. Supplemental policies that contain broad CIDR rules DO carry these explicit `except` blocks. This ensures that even if a pod is granted broad internet egress, it cannot reach node IAM credentials via link-local IMDS endpoints. The Helm values expose `egressCIDRs.excludeIMDS` (default: `["169.254.169.254/32", "fd00:ec2::254/128", "100.100.100.200/32"]`) so deployers can extend the list for additional cloud providers; new entries are automatically placed into the family-matching peer. Furthermore, the `internet` profile **requires** a sandboxed isolation profile (`sandboxed` or `microvm`) — pools with `isolationProfile: standard` (runc) cannot use the `internet` egress profile. The warm pool controller rejects pool configurations that combine `standard` isolation with `internet` egress at validation time.

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

**Token issuance and rotation (RFC 8693 token exchange):**

Lenny issues every bearer token through a single canonical endpoint: `POST /v1/oauth/token` ([§15.1](15_external-api-surface.md#151-rest-api)), compliant with [RFC 6749 §5](https://www.rfc-editor.org/rfc/rfc6749#section-5) (token endpoint) and [RFC 8693](https://www.rfc-editor.org/rfc/rfc8693) (token exchange). All token-minting flows — admin token rotation, credential-lease token issuance, delegation child-token minting, operability scope narrowing — go through this endpoint, either by direct caller invocation (admin rotation via `lenny-ctl`) or by internal Token Service calls (credential leasing, delegation minting).

**RFC 8693 parameter mapping to Lenny claims:**

| RFC 8693 parameter | Lenny use |
|---|---|
| `grant_type` | Always `urn:ietf:params:oauth:grant-type:token-exchange` |
| `subject_token` | The token being exchanged: the admin's current token (rotation), the tenant's root-session token (child minting), or the user's JWT (operability scope narrowing) |
| `subject_token_type` | `urn:ietf:params:oauth:token-type:jwt` for JWT-shaped tokens; `urn:ietf:params:oauth:token-type:access_token` for opaque tokens |
| `requested_token_type` | Same type as `subject_token_type` (rotation / child minting), or `urn:ietf:params:oauth:token-type:access_token` for lease tokens |
| `scope` | Space-separated Lenny scopes (`sessions:read`, `operations:read`, `tools:<domain>:<action>` — [§25.1](25_agent-operability.md#251-design-philosophy-and-agent-model)). Exchange may only NARROW scope — broadening is rejected with `invalid_scope` |
| `actor_token` | For delegation only: the parent session's token. The issued child token's `act` claim carries the parent's `sub`, `session_id`, `tenant_id`, and `delegation_depth`; `delegation_depth` on the child = parent + 1 |
| `actor_token_type` | `urn:ietf:params:oauth:token-type:jwt` |
| `audience` | The target audience: `lenny-gateway` (default), `lenny-ops` (operability-scope tokens), `llm-proxy` (credential-lease tokens) |
| Response `access_token` | The issued Lenny token |
| Response `issued_token_type` | Matches `requested_token_type` |
| Response `expires_in` | Seconds until expiry; constrained by upstream lifetime caps (lease TTL, delegation tree TTL, user session max, etc.) |

**Lenny JWT claim structure (inside `access_token` when the issued token is a JWT):**

Standard OIDC/RFC 9068 claims (`iss`, `sub`, `aud`, `exp`, `iat`, `nbf`, `jti`, `scope`) plus Lenny extensions:

| Lenny claim | Meaning | Source on exchange |
|---|---|---|
| `tenant_id` | Tenant scope | Copied from `subject_token` (narrowing only) |
| `session_id` | Session scope, when present | For child minting: copied from `subject_token`; for rotation: preserved |
| `caller_type` | `human` \| `service` \| `agent` ([§25.1](25_agent-operability.md#251-design-philosophy-and-agent-model)) | Copied from `subject_token`; cannot be elevated |
| `delegation_depth` | Integer, 0 for root | Exchange with `actor_token`: parent's `delegation_depth` + 1. Other exchanges preserve. |
| `act` | RFC 8693 `act` claim: `{sub, tenant_id, session_id, delegation_depth}` of the actor | Set when `actor_token` is present |
| `authorized_tools` | Narrowed tool allowlist for operability-scope tokens ([§25.1](25_agent-operability.md#251-design-philosophy-and-agent-model)) | Exchange may further narrow; broadening is rejected |
| `typ` | Token purpose discriminator: `user_bearer` \| `session_capability` \| `a2a_delegation` \| `service_token` ([§10.2](10_gateway-internals.md#102-authentication)) | Copied from `subject_token` on rotation / scope narrowing; `a2a_delegation` child-minting (exchange with `actor_token`) always produces `typ = a2a_delegation` regardless of subject. Cannot be mutated to another value. |

**Scope narrowing enforcement.** RFC 8693 specifies that the issued token's capabilities MUST be a subset of the `subject_token`'s. Lenny's Token Service enforces this by: (a) rejecting any `scope` value not present in `subject_token.scope` (returns `invalid_scope`); (b) rejecting any `delegation_depth` decrement; (c) rejecting any `audience` change that would grant access to a surface the subject did not have; (d) rejecting any `caller_type` elevation; (e) preserving or narrowing `authorized_tools` (a child-minting exchange whose `scope` includes operability tools copies the parent's `authorized_tools`, intersected with the exchange's `scope`).

**Tenant-scope enforcement (cross-tenant exchange prevention).** Every exchange MUST satisfy `issued_token.tenant_id == subject_token.tenant_id`. The Token Service rejects any request where this invariant would not hold with `invalid_request` and reason `tenant_mismatch`; the rejection carries no body beyond the RFC 8693 error object. The caller's own `tenant_id` (from the `Authorization: Bearer <caller_token>` header, see Client authentication below) MUST also equal `subject_token.tenant_id` — a caller cannot mint a token for a tenant other than its own even if it somehow possesses a foreign tenant's `subject_token`. There is no "cross-tenant delegation" flow in Lenny; platform-admin impersonation of a tenant user happens via a distinct admin-impersonation code path that writes its own `admin.impersonation_started` audit event and is NOT routed through `/v1/oauth/token`. The `token.exchanged` audit event therefore always carries a single `tenant_id` field; any `tenant_mismatch` rejection is itself audited with `policy_result: "rejected:tenant_mismatch"` so attempted cross-tenant exchanges are visible to the SIEM.

**Audit coverage.** Every token exchange — external or internal, accepted or rejected — emits a `token.exchanged` audit event ([§16.7](16_observability.md#167-section-25-audit-events)). Rejected exchanges carry `policy_result: "rejected:invalid_scope"` (or similar reason) and no token is issued; accepted exchanges carry the new token's `jti` so downstream audit events can be correlated back to the exchange that minted the token. Token contents — `access_token`, `subject_token`, `actor_token` — are NEVER written to audit payloads; only claim identifiers (`sub`, `jti`) and metadata (`scope`, `audience`, `delegation_depth`) are recorded.

**Write-before-issue ordering.** For accepted exchanges, the Token Service commits the `token.exchanged` audit row to Postgres **before** returning the `access_token` to the caller. The ordering is a single Postgres transaction with three statements, executed in this order: (1) acquire the per-tenant audit advisory lock ([§11.7](11_policy-and-controls.md#117-audit-logging) item 3); (2) INSERT the issued-token row into `issued_tokens` (carrying `jti`, hashed token, `tenant_id`, `sub`, `scope`, `audience`, `exp`); (3) INSERT the `token.exchanged` row into `audit_log`. The transaction is then `COMMIT`-ed. Only after the `COMMIT` succeeds does the gateway return `access_token` to the caller. If any statement fails, the transaction rolls back and the token is **never generated or returned** — the caller receives `500 token_exchange_failed` with no token. This closes the failover window: a Postgres primary failover mid-transaction rolls back both the `issued_tokens` INSERT and the `audit_log` INSERT atomically, so there is no state where a token exists with no audit row, nor one where an audit row exists without a corresponding issued-token row. For rejected exchanges (scope violation, tenant mismatch, expired parent, etc.), the rejection audit row is written under the same advisory lock and COMMIT discipline: the client receives the error response only after the rejection audit is durable. If the rejection audit write itself fails, the Token Service returns `500 token_exchange_failed` with the originally intended rejection reason placed in the error body's `detail` field so operators can reconstruct the attempt — better to fail-closed than to silently swallow a rejection.

Internal token issuance paths (credential lease minting in [§4.9](04_system-components.md#49-credential-leasing-service), delegation child minting in [§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)) use the same write-before-issue discipline because they all flow through the Token Service's single exchange path. During a Postgres outage (primary unreachable or in failover), token issuance is unavailable by design — callers receive `503 token_store_unavailable` and the alert `TokenStoreUnavailable` fires; the platform does not fall back to issuing tokens without audit coverage. The deferred-write path described in [§11.7](11_policy-and-controls.md#117-audit-logging) item 6 is for `lenny-ops`-originated events only and does NOT cover token issuance — tokens are gated on the synchronous write.

**`exp` granularity and parent-expiry handling.** Every JWT `exp` and `iat` claim is a Unix seconds integer (per [RFC 7519 §4.1.4](https://www.rfc-editor.org/rfc/rfc7519#section-4.1.4)), never fractional. On every exchange, the Token Service re-validates the `subject_token` (and, when present, the `actor_token`) against the current server clock: if `subject_token.exp <= now` the exchange is rejected with `invalid_grant` reason `subject_token_expired`; if `actor_token.exp <= now` the exchange is rejected with `invalid_grant` reason `actor_token_expired`. A child token minted via `actor_token` receives `exp = min(requested_exp, subject_token.exp, actor_token.exp, per-dialect cap)` — an expired parent or actor cannot produce a live child through a race between validation and signing because the re-read happens inside the same advisory-locked transaction as the signing step.

**Clock synchronization tolerance.** The gateway replica fleet MUST maintain wall-clock synchronization via NTP such that pairwise drift between any two replicas does not exceed ±500ms — tight enough that the per-second granularity of `exp` is never observed inconsistently across replicas for any token in flight. Each replica monitors its offset from the NTP reference via `lenny_time_drift_seconds` (§16.1); absolute drift above 500ms triggers `GatewayClockDrift` at `warning` severity; above 2s at `critical` severity; and above 5s the replica removes itself from the Service endpoints (`/healthz` reports degraded) and returns `503 token_validation_unavailable` on every exchange rather than issue or validate tokens whose `exp` it cannot trust. To accommodate the bounded drift window, every `exp` check applies a ±1s server-side skew allowance: a token is considered expired iff `now - 1 > exp` (i.e., the current second is strictly greater than `exp + 1`), and tokens whose `exp` is in the immediate future plus one second are issued with `exp` as requested. This skew allowance is symmetric and small enough to be below any token lifetime cap (minimum token lifetime is 60s), so an attacker cannot meaningfully extend an expired token through clock-skew abuse.

**Token rotation and revocation — no grace period.** Admin and service-principal token rotation (`grant_type=urn:ietf:params:oauth:grant-type:token-exchange` with the current token as `subject_token`, requesting a new token of the same type) is **atomic and immediate**: inside the write-before-issue transaction that creates the new token, the Token Service also writes `revoked_at = now()` on the previous token's `issued_tokens` row. Only after the transaction COMMITs does the new token leave the gateway. The revocation is then propagated cluster-wide via the `token.revoked` CloudEvents event on the Redis EventBus ([§12.6](12_storage-architecture.md#126-interface-design)), causing peer gateway replicas to load the revocation into their in-memory revocation cache within the propagation latency budget (target: p99 < 50ms, `TokenRevocationPropagationLag`, §16.5).

There is **no grace period** during which the old token continues to validate. Every token validation on every replica consults the in-memory revocation cache; on a cache miss for a recently-issued token (possible immediately after replica startup or EventBus reconnect, before rehydration completes), the replica falls through to a direct `SELECT revoked_at FROM issued_tokens WHERE jti = $1` against Postgres — a bounded-latency check gated by a per-call TTL cache to limit DB load. A caller who rotated a token and continues to present the old token therefore receives `401 token_revoked` from any replica that has applied the revocation, and `401 token_expired_or_revoked` only during the worst-case transient window in which neither the in-memory cache nor the Postgres fallback yet reflect the revocation (bounded by Postgres replication lag, which is monitored by `lenny_postgres_replication_lag_seconds` — see §16.1).

**Authoritative durability for revocation.** Postgres is the **sole authoritative store** for token revocation. The in-memory revocation cache and the EventBus propagation are latency optimizations only; if either is lost (replica restart, Redis outage, buffer overflow), correctness is preserved because every validation can fall back to `issued_tokens.revoked_at`. On gateway startup, the in-memory cache is rehydrated by reading every row where `revoked_at IS NOT NULL AND exp > now() - 1h` (the trailing hour covers tokens that expired after revocation and may still be presented by a buggy client). A gateway replica that cannot reach Postgres refuses to validate tokens — it returns `503 token_validation_unavailable` rather than accepting potentially-revoked tokens from its stale in-memory cache. This fail-closed discipline is consistent with the broader policy in §11.7 and §13.3: when we cannot prove safety, we refuse to serve.

Delegation child tokens minted from a parent that is later rotated continue to validate on their own `jti` until their own `exp` (children are not transitively revoked by parent rotation unless the rotation is specifically a revocation request — `grant_type=urn:ietf:params:oauth:grant-type:token-exchange` with `scope=""` and `requested_token_type=urn:ietf:params:oauth:token-type:access_token:revoked` triggers recursive child revocation, see [§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)).

**Actor-token freshness under concurrent rotation.** For delegation child minting with `actor_token`, the Token Service reads the `actor_token`'s `jti` against the revocation cache **inside** the same advisory-locked transaction that issues the child. If the parent was rotated between the client's exchange submission and the Token Service's cache check, the `actor_token` now carries a revoked `jti` and the exchange is rejected with `invalid_grant` reason `actor_token_revoked`. This eliminates the race where a stale parent token races with rotation to mint a child that outlives the parent's legitimate lifetime.

**Rate limiting on `/v1/oauth/token`.** The endpoint is rate-limited per caller identity to prevent brute force attacks against the `subject_token` validation and to contain runaway automation. Default limits are 10 requests/second and 300 requests/minute per `(tenant_id, sub)` tuple — excess requests return `429 rate_limited` with `Retry-After`. A separate global per-tenant limit (default 100/sec, configurable via `oauth.rateLimit.tenantPerSecond`) applies across all callers within a tenant. Limits are enforced by the gateway using the token-bucket rate limiter in [§11.1](11_policy-and-controls.md#111-admission-and-fairness). Internal Token Service calls (credential lease minting, delegation minting) bypass the external rate limit because they flow through an internal RPC path, but are subject to per-session `maxTokenBudget` caps ([§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)).

**Audit sampling for rate-limit rejections.** A naive implementation would emit a `token.exchange_rate_limited` audit event for every rejected request, but under a sustained attack this traffic would saturate the per-tenant advisory-locked audit write path (§11.7) and starve legitimate audit writes. Rate-limit rejections are therefore **sampled**: the first rejection per `(tenant_id, sub)` tuple within any rolling 10-second window is written as a full `token.exchange_rate_limited` audit event under the write-before-error transaction discipline (preserving per-attacker audit trail and SIEM visibility); subsequent rejections in the same window increment the `lenny_oauth_token_rate_limited_sampled_total` counter (labels: `tenant_id`, `limit_tier` ∈ {`caller_per_second`, `caller_per_minute`, `tenant_per_second`}) but do NOT write individual audit rows. The corresponding rejection counter `lenny_oauth_token_rate_limited_total` uses the same `limit_tier` vocabulary, as does the `limit_tier` payload field on the `token.exchange_rate_limited` audit event, so operators can correlate a metric spike to its audit row with an exact label equality.

**Sampling window locality.** The sampling window is tracked in **each gateway replica's in-memory rate-limiter state**, keyed by the same `(tenant_id, sub, limit_tier)` bucket used for enforcement. It is explicitly **per-replica local** — no Redis or EventBus coordination across replicas — because (a) rate-limit enforcement buckets are already per-replica and partitioned by consistent routing from the edge load balancer, (b) cross-replica coordination would add a synchronous dependency on Redis to every `/v1/oauth/token` call and defeat the purpose of sampling as a latency-preserving optimization, and (c) slight audit amplification across replicas (at most N audit events per window for N replicas serving the same attacker) is preferable to under-auditing. Security teams querying audit logs for brute-force evidence still see at least one authoritative event per attacker-per-window per replica; the volumetric magnitude and the cross-replica sum are observable via the Prometheus counter and the `GatewayRateLimitStorm` alert ([§16.5](16_observability.md#165-alerting-rules-and-slos)). For high-gateway-replica deployments, operators may reduce sampling amplification by routing `/v1/oauth/token` via the edge load balancer's `sub`-consistent hash (most L7 LBs support this) so that the same attacker concentrates on a single replica, collapsing the amplification factor to 1.

**Client authentication on `/v1/oauth/token`.** Per [RFC 6749 §2.3](https://www.rfc-editor.org/rfc/rfc6749#section-2.3), token endpoints authenticate clients. Lenny's token endpoint treats every caller as a **public client** and authenticates via the `Authorization: Bearer <caller_token>` header, not via `client_id`/`client_secret`. The bearer token is either (a) an upstream OIDC ID token from the tenant's configured IdP (the **bootstrap** credential — first interaction with Lenny; Lenny verifies the token's signature against the IdP's JWKS and derives `tenant_id` via the tenant's OIDC-claim-mapping configuration), or (b) a previously issued Lenny access token. In both cases the caller's identity (`sub`, `tenant_id`, `caller_type`) is extracted from the bearer token and MUST be the same as (or a superset of) the `subject_token`'s identity — a caller cannot mint a token on behalf of a different user unless they present the target user's token as `subject_token` and narrow scope from it. There is no separate `client_id` registration surface; bearer-token callers are identified by their existing JWT claims. Because the bootstrap path accepts the upstream OIDC ID token as the authentication credential, `/v1/oauth/token` is reachable without a pre-existing Lenny-issued token — the rate-limit key `(tenant_id, sub)` described above is populated from the OIDC claims on the first call, so there is no bootstrap deadlock.

**Delegation-lifetime preservation.** For delegation child minting, the Token Service verifies that the child's `exp` is ≤ parent's `exp` and that the parent session's delegation lease ([§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)) still has `maxDepth`, `maxChildrenTotal`, and `maxTokenBudget` headroom. Any exchange that would violate these invariants is rejected with `invalid_request`.

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
                                   → Real API key loaded into Token Service's in-memory credential cache (gateway process memory only)
                                   → Gateway generates lease token + proxy URL + proxyDialect
                                   → Gateway pushes lease token + proxy URL + dialect to agent pod (NOT the real API key)
                                   → Agent pod sends OpenAI- or Anthropic-shaped LLM requests to proxy URL with lease token
                                   → LLM Proxy subsystem validates lease, runs PreLLMRequest interceptors, invokes the native Go translator
                                   → Translator converts dialect to upstream provider's native wire format, reads real key from the in-memory cache, forwards over TLS
                                   → Real API key never enters the AGENT pod and is never written to disk or tmpfs — it lives only in the gateway process's memory
                                   → On lease expiry/revocation: LLM Proxy subsystem immediately rejects requests before the translator is invoked
                                   → On session end: lease invalidated, subsystem stops forwarding
```

**Translator trust boundary:** In proxy mode, translation runs inside the gateway process (no sidecar, no separate container, no loopback listener). It is part of the gateway's trust envelope — it uses the gateway's network identity, reads credentials from the Token Service's in-memory cache, and has no independent SPIFFE identity. Credential rotation refreshes the cache atomically and the next outbound call picks up the new value with no reload signal. See [Section 4.9](04_system-components.md#49-credential-leasing-service) for the native translator contract.

**Key distinction:** Connector credentials (OAuth tokens for external tools and agents) are used by the gateway on behalf of pods (pods never see them). LLM provider credentials are either delivered directly as short-lived leases (direct mode) or kept entirely out of the pod via the credential-injecting reverse proxy (proxy mode) — see [Section 4.9](04_system-components.md#49-credential-leasing-service) for details on both modes.

For the complete credential subsystem specification — including threat model considerations, security boundaries, emergency revocation procedures, and governance boundaries — see [Section 4.9](04_system-components.md#49-credential-leasing-service). Key security-relevant subsections: Security Boundaries (preventing cross-tenant credential leakage), Emergency Credential Revocation (in-memory deny list propagation), and Credential Governance Boundaries (separation of admin vs. deployer vs. runtime access).

### 13.4 Upload Security

- Gateway validates and authorizes all uploads. Upload-side validation is enforced by the gateway's Upload Handler subsystem ([§4.1](04_system-components.md#41-edge-gateway-replicas)) — pod binaries neither decompress archives nor canonicalize paths on untrusted input.
- Pod trusts only the gateway (not arbitrary clients).
- Path traversal protection: reject `..` components, absolute paths, and symlinks whose canonicalized target escapes the workspace root. Canonicalization is a normative requirement: every extracted or uploaded path is resolved (`filepath.Clean` + absolute-root prefix check) before the file is written.
- Size limits enforced at gateway and pod.
- Staging → validation → promotion pattern.
- **Archive validators (normative, non-tunable platform ceilings).** The following limits apply to every archive unpacked through the upload pipeline. They are the hard ceiling — deployer policy may lower them, but cannot raise them — and they are enforced identically for client uploads and for delegation file exports ([§8.7](08_recursive-delegation.md#87-file-export-model)). Full rationale and streaming-enforcement mechanics live in [§7.4](07_session-lifecycle.md#74-upload-safety):
  - Maximum decompressed size per archive: **256 MiB**.
  - Maximum decompression ratio (compressed:uncompressed): **100:1**.
  - Maximum entry count per archive: **10 000**.
  - Maximum per-entry size: **64 MiB**.
  - Maximum path depth: **32** components.
  - Maximum path length: **4 096 bytes** (UTF-8).
  - `hardlink`, `character-device`, `block-device`, `FIFO`, and `socket` entries rejected outright.
  - Symlinks rejected by default; if a Runtime opts in via `allowSymlinks: true`, the target must canonicalize inside `/workspace/current` and must not traverse `/proc`, `/sys`, `/dev`, or `/run/lenny`. Post-promotion symlink re-validation applies.
- Validator violations surface to clients as `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` with `details.reason` carrying the specific sub-code (see [§15.1](15_external-api-surface.md#151-rest-api) error reference). Abort causes are counted in `lenny_upload_extraction_aborted_total{error_type}` ([§16.1](16_observability.md#161-metrics)).

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

