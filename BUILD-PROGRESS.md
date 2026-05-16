# Build progress

This file audits the Lenny implementation against the phased build sequence in
[`spec/18_build-sequence.md`](spec/18_build-sequence.md). It records which phases are
complete, which are partial, and what remains.

Audited 2026-05-15, branch `impl/v1-initial`. First audited at commit `48adf0a`; the
progress log records work since.

## Progress log

Newest first. Each entry is one increment toward the critical path below.

- `f129dce` — `podsession.ResolvePool` (§5). Resolves a runtime and §5.3 isolation
  profile to the matching `SandboxWarmPool` by inspecting each pool's template — the
  pool-resolution the gateway session-start handler needs to choose which pool to
  claim from. The last gateway↔pod component dependency.
- `258c320` — Gateway and Token Service metrics-scrape NetworkPolicies (§13.2). Admit
  Prometheus scrape from the monitoring namespace to those components' metrics ports
  under the `lenny-system` default-deny.
- `4aa16c7` — `allow-dedicated-coredns` NetworkPolicy (§13.2). Re-admits the dedicated
  CoreDNS ingress (agent-namespace DNS, monitoring scrape) and kube-system CoreDNS
  egress under the `lenny-system` default-deny.
- `18eba1a` — `allow-controller-metrics-scrape` NetworkPolicy (§13.2). Admits
  Prometheus scrape from the monitoring namespace to the controller's metrics port
  under the `lenny-system` default-deny; `controller.metricsPort` and
  `monitoring.namespace` values added.
- `867e439` — `allow-token-service` NetworkPolicy (§13.2). Re-admits the Token
  Service's gateway ingress and its PgBouncer/Redis/KMS/CoreDNS egress under the
  `lenny-system` default-deny; `tokenService.grpcPort`, `redis.tlsPort`, and
  `kms.endpointCIDR` values added.
- `5de9772` — `allow-controller-egress` NetworkPolicy (§13.2). Re-admits the
  controller's kube-apiserver, PgBouncer, and CoreDNS egress under the `lenny-system`
  default-deny, so the deployed controller can reach the API server; `kubeApiServerCIDR`
  value added.
- `c11f002` — `executor.PodExecutor` — the pod-backed `Executor`. `Send` drives a
  session's bound pod over the §4.7 `Attach` content stream and collects the agent's
  response; `Close` releases the pod. It implements the `Executor` interface, so the
  gateway message path becomes an executor swap. The gateway↔pod session subsystem is
  now built end to end as components; the gateway-binary wiring is the remaining step.
- `6c2f6e1` — `podsession.Registry` — the per-session pod-binding registry. Holds the
  live `BindResult` per coordinated session for the session-start, message, and
  teardown paths. The last component before the gateway session-path wiring; all of
  claim, start, content stream, teardown, and the binding registry now exist.
- `7b34036` — `podsession.Binder.Release` (§6.2). The teardown counterpart to `Bind`:
  shuts the pod's runtime down through the adapter, closes the connection, and
  transitions the Sandbox `claimed → draining` so the reconciler reclaims the pod.
  The gateway↔pod session lifecycle — claim, start, content stream, teardown — is now
  built as components; the gateway-binary session-path wiring remains.
- `3390203` — Gateway-side `adapterclient.Attach` (§4.7). `Client.Attach` opens and
  binds the content stream; `AttachStream.Send` / `Recv` / `CloseSend` wrap the bidi
  stream. The §4.7 content path now has its proto RPC, adapter handler, and gateway
  client; wiring it into the gateway session path remains.
- `a21b2bf` — Adapter `Attach` handler (§4.7). The bidirectional content stream: a
  receive loop forwards client envelopes to the runtime's stdin, a send loop streams
  the runtime's output envelopes back. `RuntimeProcess` gained an `Output` method
  (`SubprocessExecutor` drains the child's stdout); tested over an in-memory gRPC
  stream with `-race`. The gateway-side `adapterclient.Attach` remains.
- `3128fa7` — `Attach` bidirectional content-stream RPC added to
  `schemas/lenny-adapter.proto` (§4.7, §15.4). Reconciles the Phase-1 skeleton proto
  with §4.7's RPC table: `Attach` plus the `AttachClientMessage` / `AttachServerMessage`
  frames. Purely additive; the regenerated bindings expose it. The adapter-server
  `Attach` handler and the gateway-side `adapterclient.Attach` remain.
- `e9ad1ce` — `allow-admission-webhooks` NetworkPolicy (§13.2). Re-admits
  kube-apiserver ingress and kube-system CoreDNS egress for the admission webhook
  pods under the `lenny-system` default-deny; `webhookIngressCIDR` value added.
- `8b72d0b` — `lenny-system` `default-deny-all` NetworkPolicy (§13.2). The fail-closed
  control-plane network baseline for the release namespace; the §13.2 component
  allow-lists remain.
- `8312655` — `make images` now builds `lenny-preflight`. The preflight Job's image
  was missing from the target; verified the image builds via the parameterized
  Dockerfile.
- `0b5faea` — Host-sharing check wired into `preflight.Run`. `Run` now gathers the
  Lenny-managed Deployments, DaemonSets, and Jobs and runs `CheckHostSharing`, so the
  deployed `lenny-preflight` Job enforces three checks; the preflight Role gained
  workload list access.
- `2a52365` — `pkg/preflight` host-sharing flag check (§13.1). `CheckHostSharing` fails
  fail-closed when any Lenny-managed pod template enables `shareProcessNamespace`,
  `hostPID`, `hostNetwork`, or `hostIPC`. A pure function; gathering Deployments,
  DaemonSets, and Jobs into `Run` and the matching RBAC remain.
- `9e48d8e` — `lenny-preflight` Helm Job and RBAC (§17.9). The pre-install/pre-upgrade
  hook Job at weight -10 with its read-only ServiceAccount, ClusterRole, and Role at
  -15. `lenny-preflight` is now deployable end to end (decision logic, `Run`, binary,
  Job); the remaining §17.9 checks beyond the two admission-plane checks are
  follow-ups.
- `f98a375` — `lenny-preflight` binary (§17.9). Builds an in-cluster client, runs
  `preflight.Run`, and exits non-zero on any check failure so the Helm pre-install /
  pre-upgrade Job aborts fail-closed. The Helm Job and its RBAC remain.
- `de4bd43` — `pkg/preflight.Run` cluster-gathering layer. Gathers the lenny-*
  ValidatingWebhookConfigurations and the phase-stamp ConfigMap and runs the two
  §17.9 checks against them; takes a `client.Reader` so it is fake-client testable.
  The `lenny-preflight` binary and its Helm Job remain.
- `6469141` — `pkg/preflight` admission-webhook inventory check (§17.9).
  `ExpectedValidatingWebhooks` computes the §17.2 feature-gated expected set;
  `CheckAdmissionWebhooks` fails fail-closed on a missing webhook, a non-Fail
  failurePolicy, or an empty caBundle. The second `lenny-preflight` check.
- `04b1be0` — `pkg/preflight` phase-stamp consistency check (§17.9). The pure
  `CheckPhaseStamp` decision function for `PREFLIGHT_PHASE_STAMP_MISMATCH` — an
  unacknowledged admission-plane feature-flag downgrade fails fail-closed. The start
  of the `lenny-preflight` system; the binary and the remaining §17.9 checks remain.
- `b9a3c97` — Platform `Dockerfile`. A parameterized multi-stage build (Go to
  `distroless/static:nonroot`) that produces the adapter, controller, gateway,
  webhook, and token-service images via the `BINARY` build-arg; `make images` builds
  all five. Validated by building the adapter image. Critical-path item 1's container
  image.
- `c683a1a` — Adapter `DemoteSDK` RPC. The §4.7 SDK-warm demotion RPC; a pod-warm
  adapter is not preConnect-capable, so `DemoteSDK` returns `Unimplemented` with a
  precise message, the behavior §4.7 specifies for non-preConnect pods. Critical-path
  item 1 work.
- `2d4d7eb` — `crd-conversion` identity webhook (§17.2). The conversion webhook
  handler — every `lenny.dev` CRD is single-version, so conversion is the identity —
  plus the `/crd-conversion` route on `cmd/lenny-webhook`. The CRD `spec.conversion`
  wiring and Helm manifest remain.
- `5443af7` — `lenny-deployment-phase-stamp` ConfigMap (§17.2). Layer 1 of the
  four-layer feature-flag downgrade enforcement: an append-only ConfigMap recording
  when each admission-plane feature flag was first enabled, preserving `enabledAt`
  across re-renders via Helm `lookup`. The render-time validation, the preflight
  consistency check, and the runtime alert (layers 2-4) remain.
- `82db804` — §13.2 allow-companion NetworkPolicies. `allow-gateway-ingress` admits the
  gateway's gRPC connection to each managed pod's adapter; `allow-pod-egress-base`
  admits pod egress to the gateway control channel and cluster DNS. With
  `default-deny-all` the core §13.2 agent-namespace network set is rendered.
  `values.yaml` gained `adapter.grpcPort` and `gateway.grpcPort`.
- `e08e4db` — Adapter gRPC port corrected to 50051 (§13.2). The adapter binary and
  pod-spec builder bound the adapter to 8443, which §13.2 reserves for the LLM proxy
  port; §13.2 fixes the adapter gRPC port at 50051. A spec-conformance fix that also
  unblocks the §13.2 `allow-gateway-ingress` NetworkPolicy.
- `0eb6258` — `default-deny-all` NetworkPolicy (§13.2). Renders the fail-closed
  deny-all ingress/egress baseline into every agent namespace. The §13.2
  allow-companion policies (gateway ingress, pod egress plus DNS) remain.
- `ce6dae8` — `ephemeral-container-cred-guard` Helm manifest. The §13.1 webhook is now
  fully deployable: decision package, HTTP handler, `cmd/lenny-webhook` route, and the
  `ValidatingWebhookConfiguration` scoped to the `pods/ephemeralcontainers`
  subresource in agent namespaces.
- `658b134` — `ephemeral-container-cred-guard` webhook HTTP handler and the
  `/ephemeral-container-cred-guard` route on `cmd/lenny-webhook`. The §13.1 webhook
  now serves; `podspec` exports `AdapterUID`, `AgentUID`, `CredReadersGID`, and
  `CredVolumeName` so the webhook protects exactly the identities the pod-spec
  builder assigns. The Helm `ValidatingWebhookConfiguration` manifest remains.
- `2ec3791` — `ephemeral-container-cred-guard` decision logic
  (`pkg/admission/ephemeral_container_cred_guard`). The pure §13.1 four-condition
  guard that rejects an ephemeral debug container able to read
  `/run/lenny/credentials.json`. The webhook HTTP handler and Helm manifest remain.
  Phase 3.5 work.
- `3440833` — `pkg/gateway/podsession.Binder`. `Bind` joins the gateway↔pod path: it
  claims an idle Sandbox, resolves the bound pod's adapter address from
  `status.podIP`, performs the §15.5 version handshake, and starts the session on the
  pod's adapter. The claim-and-start half of critical-path items 4 and 5; the
  remaining work is constructing the Binder in the gateway binary and calling it from
  the session-creation handler.
- `55bbd9d` — `Sandbox.status.podIP`. The Sandbox reconciler records the backing pod's
  cluster IP as it observes the pod. The gateway needs this address to reach a claimed
  pod's §4.7 adapter; it unblocks the gateway-side integration of critical-path
  items 4 and 5.
- `d67c6d4` — Adapter `Interrupt` lifecycle RPC. A clean interrupt sends SIGTERM, a
  hard interrupt sends SIGKILL. `RuntimeProcess` gained an `Interrupt` method;
  `SubprocessExecutor` signals the child without taking the stdin/stdout lock so an
  interrupt reaches a busy runtime. `adapterclient` gained a matching method.
  Critical-path item 1 work.
- `37072e4` — Adapter credential RPCs (`AssignCredentials`, `RotateCredentials`,
  `RevokeCredentials`). Each materializes the §4.7 credential file from the session's
  per-provider lease set. The new `pkg/adapter/credfile` package writes
  `credentials.json` through an atomic temp-file rename at mode `0440`, relying on the
  pod fsGroup for group ownership so no `chown` runs (§13.1). `lenny-adapter` gained
  `--credentials-dir`. Critical-path item 1 work.
- `c7f6fe8` — Gateway-side adapter client (`pkg/gateway/adapterclient`). Wraps the
  generated `adapterv1` gRPC client with connection lifecycle management and a
  session-oriented surface: `NegotiateVersion` for the §15.5 handshake, and
  `StartSession` / `SendMessage` / `Shutdown` for the session path. The connective
  piece between the gateway and a claimed pod's adapter; needed by critical-path
  items 4 and 5.
- `6ee9fd9` — Gateway pod-claim `Claimer` (`pkg/gateway/podclaim`). Binds a session to
  an idle Sandbox: flips it to `claimed` under an optimistic-locking status update and
  creates the binding `SandboxClaim`; a conflict skips to the next idle pod. The
  claim logic of critical-path item 4; the gateway-binary integration remains.
- `c7810e9`, `ca0c364` — Sandbox-to-Pod reconciler. The controller-runtime Reconciler
  materializes each Sandbox into a backing Pod via `podspec.Build`, advances the §6.2
  warm-path phase from the `lifecycle.Decide` plan, and runs the draining teardown. It
  is registered in `cmd/lenny-controller` with the adapter image supplied by
  `--adapter-image`; the controller ClusterRole gained Pod create/delete and Runtime
  read. Critical-path item 3.
- `dd5d3fc` — Agent pod-spec builder. `podspec.Build` translates a Sandbox,
  SandboxTemplate, and Runtime into the backing `corev1.Pod`: the §4.7 two-container
  sidecar pod with the §13.1 security posture, the §6.1 volumes, and the §5.3
  RuntimeClass. Critical-path item 2.
- `0f8e61c` — Adapter `SendMessage` and `Shutdown` RPCs. `SendMessage` forwards the
  gateway's pre-encoded message envelope to the runtime's stdin; `Shutdown` closes the
  runtime and releases the session. `SubprocessExecutor` gained a `WriteEnvelope`
  raw-delivery path.
- `45ad73d` — Adapter `StartSession` RPC. Rejects a non-idle pod with Unavailable,
  materializes the workspace, runs the setup commands, and starts the runtime process;
  releases the pod on any post-claim failure. `SubprocessExecutor` gained an eager
  `Start` so the runtime is live at session start per §6.1.
- `da1a77b` — Adapter setup-command runner. `workspace.RunSetup` executes a
  WorkspacePlan's setup commands in order, each in the workspace directory under a
  wall-clock timeout, stopping at the first failure.
- `5900141` — Adapter workspace materializer. `pkg/adapter/workspace.Materialize`
  writes a WorkspacePlan's inlineFile and mkdir sources into the workspace root, with
  adapter-side path-containment and mode checks. uploadFile, uploadArchive, and
  gitClone return `ErrSourceUnsupported`.
- `d255c7f` — Runtime adapter gRPC server scaffold. `pkg/adapter.Server` implements the
  generated `adapterv1.AdapterServer` contract with `NegotiateVersion` and the
  TLS/mTLS transport wiring; `cmd/lenny-adapter` is the sidecar binary. The workspace,
  session, credential, and lifecycle RPCs still return `Unimplemented`.

## Summary

The implementation has broad coverage of the per-phase logic packages and the gateway
request surface, plus a recently-built Kubernetes control plane. It does not yet run an
agent session on a Kubernetes pod end to end.

The platform currently serves the REST and admin API against in-memory, Postgres, and
Redis stores, and runs a runtime as a local subprocess through `make run`. It cannot yet
claim a warm pod, materialize a workspace into it, or run a session on it, because the
Sandbox-to-Pod reconciler, the pod-spec builder, and the runtime adapter server are not
built.

## How the build diverged from the build sequence

The build did not follow the §18 phase order. Two tracks were built ahead of the
Kubernetes layer: the gateway request-handling surface (session lifecycle, admin API,
stores, translators) and the per-phase pure-logic Go packages that §18 lists as
deliverables (`pkg/checkpoint`, `pkg/elicitation`, `pkg/environment`, `pkg/experiment`,
`pkg/podsecurity`, `pkg/credential`, and others). The Kubernetes control plane (the
CRDs, the WarmPoolController, the PoolScalingController) was built most recently.

The consequence is that most later phases are partially complete: the logic substrate
for a phase exists as a tested Go package, while the controller, binary, or gateway
integration that consumes it does not. The phase table below uses "Substrate only" for
this state.

## Phase status

| Phase | Title                                                        | Status         | Notes                                                                                                                                                                                                                                                                                                                                        |
| :---- | :----------------------------------------------------------- | :------------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 0     | Bootstrap the infrastructure repo                            | Partial        | Repo layout, CI, `LICENSE`, and the ADR template are present. ADR-007 and ADR-008 are not committed under `docs/adr/`.                                                                                                                                                                                                                       |
| 1     | Core types and wire contracts                                | Mostly done    | The `lenny.dev/v1` CRD types, the task records, the session state machines, and the `schemas/` wire contracts exist. The adapter proto Go stubs are generated.                                                                                                                                                                               |
| 1.5   | Database migration framework                                 | Mostly done    | Migrations 0001 through 0007 cover the §12 tables including `agent_pod_state`; the RLS guard, the immutability triggers, the role separation, and the schema linters are present. There is no dedicated `cmd/lenny-migrate`; `golang-migrate` is used.                                                                                       |
| 2     | Adapter protocol, make run, ImageResolver                    | Partial        | The adapter binary protocol, the `echo` runtime, `lenny-compliance`, and `make run` are present. The runtime-author SDKs and the `lenny-ctl runtime init` scaffolder are not.                                                                                                                                                                |
| 2.5   | Observability foundation                                     | Partial        | `pkg/observability` and `pkg/alerting/rules` exist. `pkg/recommendations/rules` and the Helm `ServiceMonitor`/`PrometheusRule` templates are not.                                                                                                                                                                                            |
| 2.8   | streaming-echo runtime                                       | Done           | The `streaming-echo` runtime and the full-level compliance battery pass.                                                                                                                                                                                                                                                                     |
| 3     | Pool scaling, delegation policy, runtime upgrade, mTLS       | Partial        | The PoolScalingController and the `RuntimeUpgrade` state substrate exist. The `DelegationPolicy` CRD, the `agent_pod_state` mirror write path, CIDR-drift detection, and the SDK-warm circuit-breaker logic are not built. `pkg/mtls` exists; the cert-manager PKI wiring is partial.                                                        |
| 3.5   | Admission policies, lenny-ops first deploy                   | Partial        | The `pkg/admission` decision packages and three baseline webhooks (label-immutability, sandboxclaim-guard, ephemeral-container-cred-guard) are built and deployable — decision logic, `cmd/lenny-webhook` handler, and Helm manifest. The core §13.2 agent-namespace NetworkPolicies (`default-deny-all`, `allow-gateway-ingress`, `allow-pod-egress-base`) and the §17.2 `lenny-deployment-phase-stamp` ConfigMap are rendered. The `crd-conversion` webhook handler is served by `cmd/lenny-webhook`. `lenny-preflight` is deployable end to end — the `pkg/preflight` checks and `Run` layer, the `cmd/lenny-preflight` binary, and its pre-install Helm Job and RBAC — running three checks (admission-webhook inventory, phase-stamp consistency, §13.1 host-sharing). `pool-config-validator`, the `crd-conversion` CRD-wiring and manifest, `lenny-ops`, the remaining §17.9 preflight checks, and `lenny-bootstrap` are not. |
| 4     | Session manager, REST                                        | Mostly done    | The session store, the REST session surface, derive, blob dereference, the upload pipeline, `uploadToken`, and `cmd/lenny-gateway` are built. The Postgres fallback claim path depends on the unbuilt `agent_pod_state` mirror writer.                                                                                                       |
| 4.5   | Admin API, authentication, bootstrap                         | Mostly done    | The admin API, `pkg/auth`, JWT validation, the connector resource, and `lenny-ctl bootstrap` are built.                                                                                                                                                                                                                                      |
| 5     | ExternalAdapterRegistry, MCP/Completions/Open Responses      | Partial        | The MCP adapter, the OpenAI Chat translator, the Open Responses translator, and the OpenAPI document are built. The `gitClone` materializer and the `type: mcp` gateway endpoints need confirmation.                                                                                                                                         |
| 5.4   | etcd encryption at rest                                      | Not started    | No `EncryptionConfiguration` manifest in the chart.                                                                                                                                                                                                                                                                                          |
| 5.5   | Basic credential leasing, Token Service                      | Mostly done    | `pkg/credential`, the Token Service binary, `POST /v1/oauth/token`, the `issued_tokens` table, and the `/v1/credentials` endpoints are built.                                                                                                                                                                                                |
| 5.6   | Targeted security design review (credential)                 | Not started    | No review document under `tests/tier9_security/reviews/`.                                                                                                                                                                                                                                                                                    |
| 5.75  | Minimum viable policy enforcement                            | Mostly done    | `pkg/quota` and the auth and quota interceptors are built.                                                                                                                                                                                                                                                                                   |
| 5.8   | LLM Proxy, direct-mode-isolation webhook                     | Not started    | There is no LLM proxy subsystem, no `anthropic_direct` translator, and no `lenny-direct-mode-isolation` webhook.                                                                                                                                                                                                                             |
| 6     | Interactive sessions, SDKs                                   | Partial        | The interactive-session endpoints, message injection, and replay are built. The Go, TypeScript, and Python client SDKs are not.                                                                                                                                                                                                              |
| 6.5   | Incremental load test (streaming)                            | Not started    |                                                                                                                                                                                                                                                                                                                                              |
| 7     | Policy engine (quotas, budgets, audit hooks)                 | Mostly done    | `pkg/circuitbreaker`, `pkg/idempotency`, quota enforcement, user invalidation, billing events, the usage endpoints, and the Redis breaker cache are built. The external interceptor registration framework needs confirmation.                                                                                                               |
| 8     | Checkpoint/resume, drain-readiness webhook                   | Substrate only | `pkg/checkpoint` exists. The gateway checkpoint-and-resume orchestration and the `lenny-drain-readiness` webhook are not built.                                                                                                                                                                                                              |
| 9     | Delegation, delegation-echo                                  | Partial        | `pkg/delegation` and the gateway delegation service exist. The `delegation-echo` runtime and parts of the platform MCP tool surface are not.                                                                                                                                                                                                 |
| 9.5   | Incremental load test (delegation)                           | Not started    |                                                                                                                                                                                                                                                                                                                                              |
| 10    | MCP fabric, elicitation chain                                | Substrate only | `pkg/elicitation` exists. The virtual MCP server and the elicitation chain are not built.                                                                                                                                                                                                                                                    |
| 11    | Advanced credentials, multi-provider translators, revocation | Partial        | The revocation cache exists. The `aws_bedrock`, `vertex_ai`, and `azure_openai` translators and the proactive renewal worker are not.                                                                                                                                                                                                        |
| 11.5  | Incremental load test (credential lifecycle)                 | Not started    |                                                                                                                                                                                                                                                                                                                                              |
| 12a   | Token Service hardening (KMS envelope, OAuth)                | Substrate only | `pkg/tokenexchange` exists. KMS envelope encryption and the full OAuth connector flow are not.                                                                                                                                                                                                                                               |
| 12b   | type: mcp runtime support                                    | Not started    |                                                                                                                                                                                                                                                                                                                                              |
| 12c   | Concurrent execution modes                                   | Not started    |                                                                                                                                                                                                                                                                                                                                              |
| 13    | Full observability, audit, lenny-backup, compliance          | Substrate only | `pkg/audit` and the audit hash chain exist. The `lenny-ops` runtime, `lenny-backup`, the compliance webhooks, the GDPR erasure pipeline, and the full observability catalog are not.                                                                                                                                                         |
| 13.5  | Pre-hardening full-system load baseline                      | Not started    |                                                                                                                                                                                                                                                                                                                                              |
| 14    | Comprehensive security hardening                             | Substrate only | `pkg/podsecurity` exists. The release pipeline, cosign verification, the final NetworkPolicy posture, and the pen-test driver are not.                                                                                                                                                                                                       |
| 14.5  | Post-hardening SLO re-validation                             | Not started    |                                                                                                                                                                                                                                                                                                                                              |
| 15    | Environment resource, RBAC                                   | Substrate only | `pkg/environment` exists. The environment admin API and the cross-environment delegation resolver are not.                                                                                                                                                                                                                                   |
| 16    | Experiments, PoolScalingController integration               | Substrate only | `pkg/experiment` exists. The experiment router, the experiment admin API, and the variant-pool sizing path are not.                                                                                                                                                                                                                          |
| 16.5  | Experiment load test SLO re-validation                       | Not started    |                                                                                                                                                                                                                                                                                                                                              |
| 17a   | Documentation, governance, community launch                  | Not started    | The first-party reference runtimes from §26, the installer wizard, the tier preset values files, and the web playground are not built.                                                                                                                                                                                                       |
| 17b   | Memory, semantic caching, eval hooks                         | Not started    |                                                                                                                                                                                                                                                                                                                                              |

## Implemented surface

The following are built and tested at the unit tier.

- **Gateway request surface.** Session lifecycle and REST endpoints, the admin API,
  derive, blob dereference, the upload pipeline, `uploadToken`, OIDC and OAuth JWT
  validation, the OpenAPI document, the MCP adapter, the OpenAI Chat and Open Responses
  translators, rate limiting, idempotency, circuit breakers, quotas, billing events,
  user invalidation, and the watchdog timers.
- **Storage.** Postgres-backed stores for sessions, transcripts, tenants, runtimes,
  users, connectors, billing events, issued tokens, and the audit hash chain; Redis
  layers for circuit breakers, session-coordination leases, quota counters, and storage
  quotas; migrations 0001 through 0007 with RLS, immutability triggers, and role
  separation.
- **Kubernetes control plane.** The five `lenny.dev/v1` CRDs with generated manifests;
  the WarmPoolController (the pure planner, the reconciler, the `PoolWarmingUp`
  condition, an envtest integration test) with the `lenny-controller` binary; the
  PoolScalingController (config sync, demand-driven minWarm, the periodic runnable); the
  Helm chart with the controller Deployment and RBAC, the agent namespaces, and two
  admission webhooks; the `lenny-webhook` binary.
- **Pure-logic substrate.** Tested Go packages exist for the warm-pod state machine,
  the isolation profiles, the sandbox-claim state, the scaling formula, the warm-pool
  and lifecycle planners, checkpoint enums, delegation cycle and lease arithmetic,
  elicitation, environment RBAC, experiments, idempotency, circuit breakers, quotas,
  the token-exchange invariants, the runtime-upgrade state machine, podsecurity
  validation, mTLS SPIFFE parsing, and the admission-decision packages.
- **Reference runtimes and tooling.** The `echo` and `streaming-echo` runtimes,
  `lenny-compliance`, `lenny-ctl`, the Token Service binary, the adapter gRPC
  contract with generated bindings, and the `tests/testinfra` harnesses for Kind,
  envtest, and Helm.

## Principal gaps

The implementation cannot run a Kubernetes-hosted session. The blocking gaps, in
dependency order, are below.

- **Runtime adapter server.** The gRPC scaffold, `NegotiateVersion`, the transport
  wiring, `cmd/lenny-adapter`, and the core session RPCs `StartSession`, `SendMessage`,
  and `Shutdown` are built. The remaining RPCs are not: the credential RPCs
  (`AssignCredentials`, `RotateCredentials`, `RevokeCredentials`), `Interrupt`,
  `Checkpoint`, `DemoteSDK`, and the `LifecycleChannel` stream. The runtime-to-gateway
  output path and a container image are also not built.
- **Pod-spec builder.** Nothing translates a `Sandbox`, its `SandboxTemplate`, and its
  `Runtime` into a `corev1.PodSpec`. A faithful agent pod carries an adapter container
  and a runtime container with the §13.1 security context, the §6.1 volumes, and the
  §5.3 RuntimeClass.
- **Sandbox-to-Pod reconciler.** The warm-path lifecycle planner exists, but no
  reconciler creates the backing Pod, advances `warming` to `idle`, or drives
  `draining` to `terminated`.
- **Gateway pod-claim path.** The gateway does not claim a pod through a `SandboxClaim`
  or drive the `idle` through `attached` session transitions.
- **LLM Proxy.** Proxy-mode sessions cannot reach a provider; the LLM proxy subsystem
  and the `anthropic_direct` translator are not built.
- **Operational services.** `lenny-ops`, `lenny-backup`, and the `lenny-preflight` Job
  do not exist.

Phases 13 through 17b are largely unbuilt beyond their logic substrate. The audit
pipeline, the compliance webhooks, the GDPR erasure pipeline, the backup and restore
surface, concurrent execution modes, the environment and experiment integrations, the
client SDKs, the first-party reference runtimes, and the web playground all remain.

## Critical path to an end-to-end Kubernetes session

The shortest route to running one real session on a warm pod. The first item is in
progress; see the progress log.

1. Build the §4.7 runtime adapter server. The gRPC scaffold, `cmd/lenny-adapter`,
   `StartSession`, `SendMessage`, `Shutdown`, `NegotiateVersion`, the credential RPCs
   (`AssignCredentials`, `RotateCredentials`, `RevokeCredentials`), `Interrupt`, and
   `DemoteSDK` are done, and the parameterized `Dockerfile` builds the adapter image;
   the remaining RPCs are `Checkpoint`, `ReportUsage`, `ExtendLease`, and
   `LifecycleChannel`.
2. Build the pod-spec builder. Done — `pkg/controller/sandbox/podspec`.
3. Build the Sandbox-to-Pod reconciler. Done — `pkg/controller/sandbox`, registered
   in `cmd/lenny-controller`.
4. Build the gateway pod-claim path against `SandboxClaim`. Done as a component —
   `pkg/gateway/podclaim.Claimer`.
5. Wire workspace materialization and session start from the gateway to the adapter.
   Done as a component — `pkg/gateway/adapterclient` plus `pkg/gateway/podsession.Binder`,
   which claims a pod and runs the handshake and StartSession against it. Items 4 and
   5 now need only the gateway-binary wiring: a controller-runtime Kubernetes client
   and a session-creation handler that calls `Binder.Bind`.
6. Build the LLM Proxy so a credential-proxied session can reach a provider.

**The `Attach` content path.** The per-message agent-response round-trip uses the
`Attach` bidirectional-streaming RPC (§4.7 RPC table, §15.4). It is built end to end
as components: the proto RPC (`3128fa7`), the adapter-server handler (`a21b2bf`), and
the gateway-side `adapterclient.Attach` (`3390203`). What remains is wiring `Attach`
into the gateway session path. The proto is still a Phase-1 skeleton behind §4.7 on
other RPCs — `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `ConfigureWorkspace`,
`CheckpointBarrier`, `CoordinatorFence`, `ExportPaths`, `Resume`, `Terminate` — which
§15.4 mandates reconciling.

## Next step

Wire `podsession.Binder` into the gateway binary. `cmd/lenny-gateway` needs a
controller-runtime Kubernetes client and must construct a `Binder` from it; the
session-creation handler then calls `Binder.Bind` (retrying against `ErrNoIdlePod`
within `podClaimQueueTimeout`) to place the session on a warm pod. This completes
critical-path items 4 and 5 and connects the REST session surface to the warm-pod
fabric. The claim, handshake, and StartSession are already covered by `Binder`; the
remaining work is the gateway binary's client construction and handler call.

## Test status

The unit test tier is green. The component, contract, integration, end-to-end, load,
chaos, and security tiers exist as directory structures with scaffolds; most are skipped
without the corresponding infrastructure. The WarmPoolController has an envtest
integration test that runs against a real Kubernetes API server.
