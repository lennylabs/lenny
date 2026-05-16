# Build progress

This file audits the Lenny implementation against the phased build sequence in
[`spec/18_build-sequence.md`](spec/18_build-sequence.md). It records which phases are
complete, which are partial, and what remains.

Audited 2026-05-15, branch `impl/v1-initial`. First audited at commit `48adf0a`; the
progress log records work since.

## Progress log

Newest first. Each entry is one increment toward the critical path below.

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
| 3.5   | Admission policies, lenny-ops first deploy                   | Partial        | The `pkg/admission` decision packages and three baseline webhooks (label-immutability, sandboxclaim-guard, ephemeral-container-cred-guard) are built and deployable — decision logic, `cmd/lenny-webhook` handler, and Helm manifest. The §13.2 `default-deny-all` NetworkPolicy is rendered per agent namespace. Its allow-companion policies, `pool-config-validator`, `crd-conversion`, the phase-stamp ConfigMap, `lenny-ops`, `lenny-preflight`, and `lenny-bootstrap` are not. |
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
   (`AssignCredentials`, `RotateCredentials`, `RevokeCredentials`), and `Interrupt` are
   done; the remaining RPCs are `Checkpoint`, `ReportUsage`, `ExtendLease`,
   `DemoteSDK`, and `LifecycleChannel`, plus a container image.
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
