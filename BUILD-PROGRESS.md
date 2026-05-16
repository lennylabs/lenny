# Build progress

This file audits the Lenny implementation against the phased build sequence in
[`spec/18_build-sequence.md`](spec/18_build-sequence.md). It records which phases are
complete, which are partial, and what remains.

Audited 2026-05-16, branch `impl/v1-initial`. First audited at commit `48adf0a`; the
progress log records work since.

## Progress log

Newest first. Each entry is one increment toward the critical path below.

- `1567473`, `f37a856` — §10.7 / §15.1 experiment admin API. `experimentstore` is the
  per-tenant ExperimentDefinition registry; `/v1/admin/experiments` serves
  POST/GET/list/PUT/PATCH/DELETE. POST/PUT validate the §10.7 definition (variant
  weights, the reserved `control` id, enum fields); PUT leaves status untouched; PATCH
  is the canonical status-transition endpoint enforcing the §10.7 lifecycle (active and
  paused interconvert and may conclude; concluded is immutable) and emits
  `experiment.status_changed`. The `/results` endpoint and the cross-resource
  variant-pool isolation check are deferred.
- `f0b2787`, `ed68362` — §11.4 `full_revoke` SessionStore-side fan-out. `full_revoke`
  transitions every non-terminal session owned by the user to `cancelled` and dismisses
  the user's pending elicitations (`interactionstore.DismissByUser` — elicitations only,
  per the spec's step-7 wording). soft/hard disable leave running sessions and
  elicitations alone. The invalidate response and audit event report the counts. The
  pod-RPC `Terminate` fan-out and Redis cached-auth invalidation remain infra-bound.
- `112a3e9` — §7.1 artifact-retention GC (`pkg/gateway/retentiongc`). A periodic sweep
  deletes the transcript and blobs of every terminal session past its retention
  deadline and clears the session's snapshot reference so the row is collected once. A
  §12.8 legal hold exempts the session; the sweep re-reads each row immediately before
  the irreversible deletion so a hold placed mid-sweep still protects it. Wired into
  the gateway alongside the watchdog and orphan-cleanup workers.
- `6e79d19` — §12.8 `--acknowledge-hold-override` erasure path. A `platform-admin` may
  bypass the legal-hold preflight with a non-empty justification; a missing
  justification is `400` and a `tenant-admin` caller is `403`. The override emits
  `gdpr.legal_hold_overridden` and is recorded in the `gdpr.erasure_completed` receipt.
- `3cb9f4d`, `240dae1`, `a092e10` — §12.8 legal-hold control. The `legal_hold` flag on
  sessions (migration 0010), `POST /v1/admin/legal-hold` to set or clear it
  (platform-admin only, emitting `legal_hold.set` / `legal_hold.cleared`), and the
  step-0 erasure preflight: an erasure of a user who owns a held session is rejected
  with `409 ERASURE_BLOCKED_BY_LEGAL_HOLD` before the job initiates, emitting
  `gdpr.erasure_blocked_by_hold`.
- `8a01b97` — §12.8 `gdpr.erasure_completed` receipt. A completed erasure job writes a
  `gdpr.erasure_completed` audit event carrying the job id, per-store deleted counts,
  and total — the authoritative proof that the erasure was carried out. A failed job
  emits no receipt; its outcome is the job record queried via the status endpoint.
- `8d971b8`, `e5204f9` — §12.8 / GDPR Article 18 processing-restriction flag. Initiating
  an erasure job marks the target user `processing_restricted`; the session-creation
  gate rejects a restricted user with `403 ERASURE_IN_PROGRESS`, surfacing the erasure
  job id. The erase handler sets the flag on initiation and clears it on completion,
  leaving it set when the job fails. Migration 0009 adds the `processing_restricted`
  and `erasure_job_id` columns so the Postgres userstore round-trips the restriction.
- `10bd520` — Gateway wires the §12.8 erasure orchestrator. `cmd/lenny-gateway` builds
  the `DeleteByUser` orchestrator from its wired stores — a session lister over the
  SessionStore, the transcript and blob stores as session-scoped erasers, the
  interaction and session stores as user-scoped erasers — and exposes it behind the
  admin erasure endpoints. The §12.8 erase path is now reachable in the running binary.
- `343d6d3` — §12.8 user-erasure admin endpoints. `POST /v1/admin/users/{user_id}/erase`
  initiates a background `DeleteByUser` job and returns its id; `GET
  /v1/admin/erasure-jobs/{job_id}` reports the phase, an advisory completion
  percentage, elapsed time, per-store deleted counts, and any failure. Both are gated
  on platform-admin or tenant-admin; a job in another tenant reads as not-found.
- `0bc2f53` — §12.8 erasure-job registry and runner (`pkg/gateway/erasurejob`). A `Job`
  records the §12.8 phase field so a controller restart resumes from the persisted safe
  point; the `Runner` drives a job `initiated → store_deleting → completed/failed`
  while the erasure orchestrator deletes the user's data, preserving the fail-fast
  partial result on a store error.
- `348a9c6` — Blob store §12.8 `DeleteBySession` adapter. Both blob backends gained the
  per-session GDPR-erasure adapter: `MemoryStore` scans its blob map; the MinIO store
  lists and removes objects under the session prefix. A shared `sessionPrefix` helper
  keeps the MinIO key layout in sync with `objectKey`.
- `751efcf` — Adapter `Resume` RPC (§4.7, §7.1). Added to the adapter proto and
  implemented: the adapter claims the session, loads a checkpoint archive through a
  `CheckpointSource`, restores the workspace via `workspace.Extract`, and starts the
  runtime — the replacement-pod counterpart of `StartSession`. `workspace.Extract` now
  returns the uncompressed bytes restored.
- `3586fe6` — `workspace.Extract` (§4.4, §7.1, §14). Restores a workspace from a
  gzip-tar — the inverse of `Archive` — with per-entry containment, symlink-target
  validation that closes the tar-traversal vector, and a decompression-bomb cap. The
  adapter restore path and §14 `uploadArchive` materialization build on it.
- `5f3f268` — Gateway wires the checkpointer as the §7.1 session sealer.
  `cmd/lenny-gateway` passes the `Checkpointer` as the `sessionserver` `Sealer`, so a
  completing session's workspace is sealed on the session-completion path.
- `46aade9` — Seal-and-export on session completion (§7.1). `recordSessionCompleted`
  invokes the optional `Sealer` when a session reaches a terminal state, before
  `executor.Close` releases the pod, so the final snapshot is taken while the pod
  adapter is still reachable.
- `3abab51` — `checkpointer.Seal` (§7.1). The seal-and-export snapshot — a checkpoint
  recorded with `WorkspaceSnapshot` source `sealed` rather than `checkpoint`.
- `bbebbe1` — Gateway runs the §4.4 periodic-checkpoint loop. `cmd/lenny-gateway` starts
  `Checkpointer.Run` as a background runnable when `--agent-namespace` is set, on the
  `--checkpoint-interval` cadence (default 5m).
- `30d05a2` — Periodic-checkpoint loop (§4.4, §7.1). `Checkpointer.Run` ticks on the
  configured cadence and `Sweep` takes a §4.4 checkpoint of every running session this
  gateway replica coordinates, keeping each `WorkspaceSnapshot` fresh. `podsession.BindResult`
  gained `TenantID` and `podsession.Registry` gained `Snapshot` to enumerate the bindings.
- `eb1200e` — `checkpointer.Checkpointer` (§4.4, §7.1). Drives one §4.4 checkpoint of a
  running session: resolves the bound pod adapter via the `podsession` registry, calls
  the adapter `Checkpoint` RPC, and records the result as the session's §7.1
  `WorkspaceSnapshot` with source `checkpoint`.
- `c793815` — `adapterclient.Checkpoint` (§4.4, §15.4). The gateway-side adapter client
  gains a `Checkpoint` method that drives a pod adapter's §4.4 Checkpoint RPC and
  returns the stored checkpoint's identifier and size.
- `b7f8c9c` — Adapter `Checkpoint` RPC (§4.4, §4.7). Archives the session workspace with
  `workspace.Archive` and streams the gzip-tar to a `CheckpointSink`, returning the
  checkpoint identifier and compressed size — the best-effort path that archives the
  workspace live without quiescing the runtime. The `Server` gains an optional
  `Checkpoints` sink.
- `3a9898f` — `workspace.Archive` (§4.4, §7.1). Snapshots a workspace directory into a
  gzip-tar — the inverse of `Materialize` — for a §4.4 checkpoint or the §7.1
  seal-and-export. Symlinks are recorded as symlink entries without being followed, so
  the archive cannot embed content outside the workspace root.
- `12afa28` — Gateway selects the MinIO-backed artifact store (§4.5, §12.5).
  `cmd/lenny-gateway` gains `--minio-endpoint` and related flags; when set, the §4.5
  blob store is `miniostore.Store` and `GET /internal/drain-readiness` runs a real
  MinIO bucket probe instead of the always-ready stub.
- `8a15fa4` — `miniostore` MinIO-backed blob store (§4.5, §12.5). The production
  `blobstore.Store`: Put (write-once), Get, Stat with §4.5 TTL expiry, backed by the
  minio-go SDK. It also satisfies the §12.5 drain-readiness `Prober` via a bucket-exists
  probe. Pure logic is unit-tested; the S3 round-trip is a component-tier contract test
  against a MinIO container behind a new `containers.StartMinIO` helper.
- `5c7f1c1` — Gateway serves `GET /internal/drain-readiness` (§12.5). `cmd/lenny-gateway`
  mounts the `drainreadiness.Handler` so the `lenny-drain-readiness` webhook can run its
  pre-drain artifact-store health check. The §12.5 webhook is now complete end to end.
- `150f168` — `lenny-drain-readiness` Helm manifest (§12.5). The fail-closed
  `ValidatingWebhookConfiguration` on the `pods/eviction` subresource, rendered when
  `features.drainReadiness` is enabled; the shared webhook Deployment template passes
  `--gateway-drain-readiness-url`.
- `889e4ad` — `cmd/lenny-webhook` serves the `/drain-readiness` route (§12.5). The
  `lenny-drain-readiness` admission route on the `pods/eviction` subresource, built from
  the cluster client and an `HTTPDrainProbe` pointed at the gateway endpoint via the new
  `--gateway-drain-readiness-url` flag.
- `54725dd` — `webhook.DrainReadiness` handler (§12.5). The `lenny-drain-readiness`
  Decider for the `pods/eviction` subresource: it resolves the evicted pod's node,
  reads the `lenny.dev/drain-force` override, queries the gateway drain-readiness
  endpoint via `HTTPDrainProbe`, and applies `drain_readiness.Decide`. The
  `cmd/lenny-webhook` route, the Helm manifest, and the gateway endpoint wiring follow.
- `5974b11` — `drainreadiness` gateway endpoint (§12.5). The `GET /internal/drain-readiness`
  endpoint runs a MinIO liveness probe within a 2s timeout and returns the §12.5
  ready/not-ready JSON bodies; the probe is a `Prober` interface so the MinIO HeadBucket
  check wires in at the gateway.
- `a6c627c` — `drain_readiness` decision logic (§12.5, §13.2). The pure decision for the
  `lenny-drain-readiness` webhook: the node drain-force override admits an eviction as a
  forced drain, a healthy §12.5 MinIO probe admits it, and an unhealthy or unreachable
  probe rejects it fail-closed. The webhook handler, gateway endpoint, and Helm manifest
  follow.
- `e4d9a8b` — `credassign.Service` (§4.9). The credential-assignment service: at session
  start it selects a pool credential per the assignment strategy, mints the
  `CredentialLease`, records it in `credleasestore`, and caches the real upstream
  credential in `credcache` so the LLM proxy can inject it; `Release` frees the slot.
- `c5db326` — `credential.SelectCredential` (§4.9). The pool credential assignment
  strategies — least-loaded, round-robin, sticky-until-failure — each picking a healthy
  credential, skipping unhealthy ones, and returning `ErrPoolExhausted` when none is
  assignable.
- `beb2e77` — `credential.MintLease` (§4.9). The CredentialLease minter: synthetic-TTL
  resolution with the per-provider ceilings, `expiresAt`/`renewBefore` computation, and
  opaque lease-ID and unguessable bearer-token generation.
- `80039dc` — Gateway serves the §4.9 LLM reverse proxy. `cmd/lenny-gateway` gained
  `--llm-proxy-addr` and `--anthropic-version`; `newLLMProxyServer` builds the
  `llmproxy.Handler` over the credential-lease store, credential cache, deny list,
  translator, and breaker-gated forwarder, and the gateway serves it at
  `POST /llm-proxy/v1/messages` on a listener separate from the REST API.
- `293eb04` — `denylist.DenyList` (§4.9). The per-replica credential deny list the LLM
  proxy checks on every upstream request; a hit rejects with `CREDENTIAL_REVOKED`
  before any upstream call. Satisfies the proxy's `DenyList` interface.
- `1676fd6` — `credcache.Cache` (§4.9). The Token Service in-memory upstream-credential
  cache: real provider keys keyed by the source-aware credential identity, satisfying
  the proxy's `CredentialResolver`. The §4.9 source-aware key is renamed
  `credential.DenyListKey` → `credential.CredentialKey`, since the deny list and the
  cache both key on it.
- `3764c4a` — LLM proxy SSE streaming wired into the handler (§4.9). `Forwarder.ForwardStream`
  gates a streaming upstream call through the breaker and returns the live 2xx response;
  the `Handler` dispatches on the request `stream` field, relays the upstream SSE stream
  to the agent pod via `RelayStream`, and records the authoritative token usage through
  the optional `UsageRecorder` on both the streaming and non-streaming paths.
- `730d4da` — `llmproxy.RelayStream` (§4.9). The SSE relay copies an upstream Anthropic
  Messages event stream to the agent pod line by line with per-line flush, without
  buffering, and extracts the authoritative token usage from the `message_start` and
  `message_delta` events. A mid-stream failure is tagged `streaming_interrupted`.
- `d210138` — `llmproxy.Handler` (§4.9). The LLM reverse proxy HTTP handler for the
  Anthropic Messages dialect: it resolves the agent pod's bearer lease token through
  `credleasestore`, runs the per-request lease checks, translates and credential-injects
  the request, forwards through the breaker-gated `Forwarder`, and translates the
  response. The `CredentialResolver` and `DenyList` are interfaces so the Token Service
  credential cache and the §4.9 deny list plug in later. The non-streaming proxy path
  is whole; the SSE relay remains.
- `e65f804` — `credleasestore.Store` (§4.9). The in-memory, per-replica store of issued
  credential leases, indexed by lease ID and by the opaque proxy lease token so the LLM
  reverse proxy resolves an agent pod's request to its lease on the upstream hot path.
- `e15d457` — `credential.Lease` data model (§4.9). The CredentialLease as a Go type:
  the pool/user tagged union, the proxy-mode materializedConfig, structural validation,
  the source-aware deny-list key, and `ValidateProxyRequest` (expiry, revocation,
  SPIFFE-binding) — the per-request checks the LLM proxy runs against a resolved lease.
- `15634ed` — `lenny-direct-mode-isolation` Helm manifest (§4.9, §13.2). The fail-closed
  `ValidatingWebhookConfiguration` scoped to `sandboxtemplates` in agent namespaces,
  rendered only when `features.llmProxy` is enabled. `values.yaml` gained `tenancy.mode`
  and `global.devMode`, passed to every admission-webhook Deployment as `--tenancy-mode`
  and `--dev-mode`. The webhook is deployable end to end.
- `b4c27a8` — `lenny-direct-mode-isolation` webhook handler and route (§4.9, §13.2). The
  `DirectModeIsolation` Decider decodes a `SandboxTemplate` and applies
  `direct_mode_isolation.Decide`; `cmd/lenny-webhook` gained the `/direct-mode-isolation`
  route and the `--tenancy-mode` / `--dev-mode` flags. `SandboxTemplateSpec` gained a
  `spiffeBinding` field so the webhook can enforce the `proxy` + `spiffeBinding: disabled`
  rejection on the resource it admits.
- `dd0ccf8` — `direct_mode_isolation` decision logic (§4.9, §13.2). The pure decision
  for the `lenny-direct-mode-isolation` webhook: in multi-tenant mode it rejects
  `deliveryMode: direct` with `isolationProfile: standard` and `deliveryMode: proxy`
  with `spiffeBinding: disabled` on `SandboxTemplate` and `CredentialPool` resources.
  Enforcement is inactive outside multi-tenant mode. The webhook HTTP handler and Helm
  manifest follow.
- `ee61d52` — `llmproxy.Forwarder` (§4.9). The upstream forwarder gated by the circuit
  breaker: an open breaker rejects the call with `ErrCircuitOpen` before dialing, a
  transport failure returns a `TranslationError` tagged `timeout` or `upstream_5xx`, and
  a completed HTTP exchange returns the `UpstreamResponse` at any status. A 5xx or
  transport failure feeds the breaker; a 4xx or 2xx records a success.
- `6221f0f` — `llmproxy.CircuitBreaker` (§4.9). The LLM Proxy circuit breaker around an
  upstream provider: consecutive failures trip it open, an open breaker rejects every
  request so the proxy returns `PROVIDER_UNAVAILABLE` without hanging, and after the
  cooldown it admits one half-open probe whose outcome closes or reopens it. State maps
  to the §16.1 `lenny_gateway_subsystem_circuit_state` gauge values.
- `9799022` — `llmproxy.AnthropicDirectTranslator` (§4.9). First leaf of the LLM reverse
  proxy: converts an agent pod's Anthropic Messages proxy-dialect request into the
  upstream `anthropic_direct` request (body passthrough, injected `x-api-key`,
  `anthropic-version` header handling) and passes the response back with the
  authoritative token usage extracted. Translator failures carry the §4.9 error
  taxonomy. The proxy HTTP handler, lease-token validation, the SSE relay, the circuit
  breaker, and the `lenny-direct-mode-isolation` webhook remain.
- `a2585eb` — `GET /v1/sessions/{id}` returns the stored `workspacePlan` (§15.1).
  `toResponse` echoes the §14 plan persisted on the session row; the `SessionResponse`
  envelope gained a `workspacePlan` field that `omitempty` drops for planless sessions.
- `f11558c` — Two-step §15.1 session start places sessions on warm pods. The granular
  `create → finalize → start` lifecycle now claims a §5 warm pod at the start
  transition, matching `POST /v1/sessions/start`. The §14 WorkspacePlan is persisted on
  the session row (new `sessions.workspace_plan` jsonb column, migration `0008`) so the
  dedicated `handleStart` re-parses it and materializes the workspace onto the claimed
  pod. `handleStart` claims the pod before transitioning the row, leaving the session
  `ready` and retryable on a claim failure.
- `b2935cd` — Gateway↔pod session wiring complete (§15.1, §4.7). `POST /v1/sessions/start`
  claims a §5 warm pod through `podsession.Binder` and records the binding in
  `podsession.Registry` for the message and teardown paths; a claim failure marks the
  session row failed and returns a retryable 503. `cmd/lenny-gateway` gained
  `--agent-namespace`: when set it builds a controller-runtime client, the `Binder`, the
  `Registry`, and the `PodExecutor`, so the REST session surface runs a session on a warm
  pod end to end. `adapter.TLSClientOption` builds the gateway's §4.7 mTLS dial option.
  Critical-path items 4 and 5 are done.
- `90048bf` — `podsession.WorkspacePlanToProto`. Converts a parsed §14 WorkspacePlan
  into the `adapterv1.WorkspacePlan` the gateway sends in `StartSession` — the
  conversion the session-start path needs to feed `Binder.Bind` a workspace plan.
- `77c00ef` — Gateway closes the executor on session completion.
  `recordSessionCompleted` now calls `executor.Close`, fixing a latent leak (a
  SubprocessExecutor child outlived its session) and giving the pod-backed
  `PodExecutor` its teardown hook — a prerequisite for the gateway↔pod wiring.
- `bd7e508` — `allow-gateway-egress` NetworkPolicy (§13.2). Re-admits the gateway's
  in-cluster egress (agent adapters, Token Service, PgBouncer, Redis, MinIO,
  kube-apiserver, CoreDNS) under the `lenny-system` default-deny. The external-HTTPS
  egress with its NET-062 dual-family IMDS exclusions is deferred to the LLM proxy.
- `0b5cc49` — `allow-minio` NetworkPolicy (§13.2). Re-admits MinIO ingress from the
  gateway and its CoreDNS egress under the `lenny-system` default-deny;
  `minio.tlsPort` value added.
- `9d13f34` — `allow-pgbouncer` NetworkPolicy (§13.2). Re-admits PgBouncer ingress
  from the gateway/Token Service/controller and its Postgres and CoreDNS egress under
  the `lenny-system` default-deny; `postgres.cidr` value added.
- `24785e2` — `allow-pod-egress-llm-proxy` NetworkPolicy (§13.2). The supplemental
  agent-namespace egress that admits only proxy-mode pods (by the
  `lenny.dev/delivery-mode: proxy` label) to the gateway LLM reverse-proxy port.
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
request surface, plus the Kubernetes control plane and the gateway↔pod session path.

The platform serves the REST and admin API against in-memory, Postgres, and Redis
stores, and runs a runtime as a local subprocess through `make run`. With
`--agent-namespace` set, `cmd/lenny-gateway` claims a §5 warm pod for a session started
through either `POST /v1/sessions/start` or the two-step `create → finalize → start`
lifecycle, materializes its workspace, and runs the session on the pod's §4.7 adapter —
the runtime adapter server, the pod-spec builder, and the Sandbox-to-Pod reconciler are
built. Credential-proxied sessions cannot reach a provider because the LLM Proxy is not
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
| 4     | Session manager, REST                                        | Mostly done    | The session store, the REST session surface, derive, blob dereference, the upload pipeline, `uploadToken`, and `cmd/lenny-gateway` are built. Both `POST /v1/sessions/start` and the two-step `create → finalize → start` lifecycle claim a §5 warm pod and run the session on the pod's §4.7 adapter when the gateway runs with `--agent-namespace`. The Postgres fallback claim path depends on the unbuilt `agent_pod_state` mirror writer. |
| 4.5   | Admin API, authentication, bootstrap                         | Mostly done    | The admin API, `pkg/auth`, JWT validation, the connector resource, and `lenny-ctl bootstrap` are built.                                                                                                                                                                                                                                      |
| 5     | ExternalAdapterRegistry, MCP/Completions/Open Responses      | Partial        | The MCP adapter, the OpenAI Chat translator, the Open Responses translator, and the OpenAPI document are built. The `gitClone` materializer and the `type: mcp` gateway endpoints need confirmation.                                                                                                                                         |
| 5.4   | etcd encryption at rest                                      | Not started    | No `EncryptionConfiguration` manifest in the chart.                                                                                                                                                                                                                                                                                          |
| 5.5   | Basic credential leasing, Token Service                      | Mostly done    | `pkg/credential`, the Token Service binary, `POST /v1/oauth/token`, the `issued_tokens` table, and the `/v1/credentials` endpoints are built.                                                                                                                                                                                                |
| 5.6   | Targeted security design review (credential)                 | Not started    | No review document under `tests/tier9_security/reviews/`.                                                                                                                                                                                                                                                                                    |
| 5.75  | Minimum viable policy enforcement                            | Mostly done    | `pkg/quota` and the auth and quota interceptors are built.                                                                                                                                                                                                                                                                                   |
| 5.8   | LLM Proxy, direct-mode-isolation webhook                     | Mostly done    | The §4.9 LLM proxy subsystem is built (`pkg/gateway/llmproxy`: the `anthropic_direct` translator, circuit breaker, breaker-gated forwarder, SSE relay, and `Handler`, over the `pkg/credential` lease model, the `credleasestore` lease store, the `credcache` credential cache, and the `denylist` deny list) and `cmd/lenny-gateway` serves it on the §13.2 LLM-proxy port. The `lenny-direct-mode-isolation` webhook is deployable end to end. The §4.9 credential-assignment path (`AssignCredentials`) that mints leases and populates the credential cache is not. |
| 6     | Interactive sessions, SDKs                                   | Partial        | The interactive-session endpoints, message injection, and replay are built. The Go, TypeScript, and Python client SDKs are not.                                                                                                                                                                                                              |
| 6.5   | Incremental load test (streaming)                            | Not started    |                                                                                                                                                                                                                                                                                                                                              |
| 7     | Policy engine (quotas, budgets, audit hooks)                 | Mostly done    | `pkg/circuitbreaker`, `pkg/idempotency`, quota enforcement, billing events, the usage endpoints, and the Redis breaker cache are built. The §11.4 three-tier user invalidation is built: soft/hard disable gate new work; full_revoke additionally transitions the user's non-terminal sessions to `cancelled` and dismisses their pending elicitations. The §11.4 full_revoke pod-RPC `Terminate` fan-out, Redis cached-auth invalidation, and credential-lease revocation remain infrastructure-bound. The external interceptor registration framework needs confirmation. |
| 8     | Checkpoint/resume, drain-readiness webhook                   | Done           | The `lenny-drain-readiness` webhook, the §4.4 periodic-checkpoint path, and the §7.1 seal-and-export are complete. `workspace.Extract` restores a workspace from a gzip-tar, and the adapter `Resume` RPC rebuilds a replacement pod's workspace from a checkpoint. The gateway-side resume is complete: `adapterclient.Resume` drives the pod adapter's `Resume` RPC, `Binder.Resume` claims a fresh pod and restores onto it, and `handleResume` serves `POST /v1/sessions/{id}/resume` — valid only from `awaiting_client_action` per §15.1, restoring from the §7.1 `WorkspaceSnapshot` or rebuilding from the stored §14 `WorkspacePlan`.                                                                                                                                              |
| 9     | Delegation, delegation-echo                                  | Partial        | `pkg/delegation/cycle`, `pkg/delegation/lease`, `pkg/delegation/tracing`, and the gateway delegation service are built. The §8.5 platform MCP tool surface is complete: `lenny/create_session`, `lenny/send_message` (with `inReplyTo` input resolution), `lenny/get_task_tree`, `lenny/delegate_task`, `lenny/cancel_child`, `lenny/await_children`, `lenny/discover_agents`, `lenny/set_tracing_context`, `lenny/output`, and `lenny/request_input` (`pkg/gateway/inputwait` registry, §11.3 timeout). The delegation service propagates a parent's §8.3 tracingContext onto each child. The §4 `RequestInterceptor` chain framework (`pkg/gateway/interceptor`) is built and the `PreMessageDelivery` phase is wired into `lenny/send_message`. The §8.10 `session_tree_archive` store (`pkg/gateway/treearchive`) is built with the archive-and-replay loop wired: child sessions are archived on every terminal transition (`lenny/cancel_child`, the sessionserver terminate / `DELETE` / start-failure paths, and the watchdog forced-failure and expiry sweeps), and `lenny/await_children` replays an archived child when its live session row is gone. The §8.10 `cascadeOnFailure` policy is applied on every terminal transition (`cancel_all` default cancels descendants, `detach` / `await_completion` leave them running, per-node policy honored), a resumed parent with active children receives the §7.1 `children_reattached` event, the §8.10 orphan-cleanup job (`pkg/gateway/orphancleanup`) sweeps every 60s to terminate orphans past the `cascadeTimeoutSeconds` window, and a `detach` cascade falls back to `cancel_all` when the tenant is over the `maxOrphanTasksPerTenant` cap, and the §8.10 bottom-up reattach traversal (`pkg/delegation/recovery`) groups failed tree nodes by depth and recovers them leaves-first under the `maxLevelRecoverySeconds` / `maxTreeRecoverySeconds` deadlines. Not built: the `delegation-echo` runtime, the `PreDelegation` and `PreExportMaterialization` interceptor wiring, external gRPC interceptors, and the `ExtendLease` lease-extension control plane.                                                                                                                                                                                                 |
| 9.5   | Incremental load test (delegation)                           | Not started    |                                                                                                                                                                                                                                                                                                                                              |
| 10    | MCP fabric, elicitation chain                                | Partial        | `pkg/elicitation` is built (`EnforcementMode` with `ResolveEffective`, `DepthPolicy` with `ShouldSuppress`, `InitiatorType`, `Content.Digest` / `VerifyContent`, `Provenance.Validate`). The `lenny/request_elicitation` platform MCP tool records a pending §9.2 elicitation in the shared interaction store and blocks until a human resolves it via the §15.1 respond/dismiss endpoints or the §9.1 `maxElicitationWait` timeout fires; the §9.1 `maxElicitationsPerSession` budget (default 50) drops an over-budget request. The tenant elicitation-content-integrity admin endpoints (`GET`/`PUT /v1/admin/tenants/{id}/elicitation-content-integrity`) are built, with justification-required weakening and the `tenant.elicitation_content_integrity_mode_changed` audit event. The `lenny_elicitation_dropped_total{reason}` metric counter is built and recorded on budget and depth-suppression drops. §9.2 depth-based elicitation suppression is wired into `lenny/request_elicitation` (the `suppress_at_depth` / `block_all` depth policies drop an agent elicitation raised too deep). Not built: the gateway virtual MCP server and the hop-by-hop elicitation chain (agent-interception forwarding up the task tree with content-integrity binding). |
| 11    | Advanced credentials, multi-provider translators, revocation | Partial        | The revocation cache exists. All §4.9 multi-provider LLM-proxy translators are built (`pkg/gateway/llmproxy`): `anthropic_direct`, `azure_openai`, `vertex_ai`, and `aws_bedrock`. The §4.9 `CredentialRenewalWorker` proactive-renewal loop (`pkg/gateway/credrenewal`, including emergency-revocation lease termination) and the `credentialPolicy` fallback chain (`pkg/gateway/credfallback`) are built. Not built: hot credential rotation (`credentials_rotated` / `credentials_acknowledged` adapter handshake) and cross-replica revocation propagation through Redis pub/sub.                                                                                                                                                                                                        |
| 11.5  | Incremental load test (credential lifecycle)                 | Not started    |                                                                                                                                                                                                                                                                                                                                              |
| 12a   | Token Service hardening (KMS envelope, OAuth)                | Substrate only | `pkg/tokenexchange` exists. KMS envelope encryption and the full OAuth connector flow are not.                                                                                                                                                                                                                                               |
| 12b   | type: mcp runtime support                                    | Not started    |                                                                                                                                                                                                                                                                                                                                              |
| 12c   | Concurrent execution modes                                   | Not started    |                                                                                                                                                                                                                                                                                                                                              |
| 13    | Full observability, audit, lenny-backup, compliance          | Partial        | `pkg/audit` and the audit hash chain exist (the §11.7 `ChainIntegrity` / `ComplianceProfile` / `RetentionPreset` / `OCSFTranslationState` enums are built). The §12.8 GDPR erasure path is wired end to end. The dependency-ordered, fail-fast `DeleteByUser` orchestrator (`pkg/gateway/erasure`) covers user-scoped and session-scoped stores, with per-store erasure adapters on the SessionStore (in-memory and Postgres), the interaction store (`DeleteByUser`), and the transcript and blob stores (`DeleteBySession`, both the in-memory and MinIO blob backends). The erasure-job registry and runner (`pkg/gateway/erasurejob`) track the §12.8 phase field and drive a job `initiated → store_deleting → completed/failed`; `POST /v1/admin/users/{user_id}/erase` initiates a background job and `GET /v1/admin/erasure-jobs/{job_id}` reports its status, both wired into the running gateway. Initiating an erasure job marks the target user §12.8 / GDPR Article 18 processing-restricted (`processing_restricted` on the user record, persisted by migration 0009), so the session-creation gate rejects new sessions with `ERASURE_IN_PROGRESS` until the job completes. A completed job writes the §12.8 `gdpr.erasure_completed` receipt to the audit trail. The §12.8 legal-hold control is built: `POST /v1/admin/legal-hold` sets and clears the `legal_hold` flag on a session (persisted by migration 0010), the §12.8 step-0 erasure preflight rejects an erasure with `409 ERASURE_BLOCKED_BY_LEGAL_HOLD` when the target user owns a held session, and a `platform-admin` may bypass the preflight via `acknowledgeHoldOverride` with a recorded justification (emitting `gdpr.legal_hold_overridden`). The §7.1 artifact-retention GC (`pkg/gateway/retentiongc`) is built and wired: a periodic sweep deletes the transcript and blobs of every terminal session past its retention deadline and exempts any session under a legal hold. Not built: artifact-level legal holds and the legal-hold ledger (audit-range and workspace-snapshot holds), billing-event pseudonymization with the per-tenant `erasure_salt`, the database-level processing-restriction trigger, the `lenny-ops` runtime, `lenny-backup`, the compliance webhooks, the tenant-deletion controller, and the full observability catalog.                                                                                                                                                         |
| 13.5  | Pre-hardening full-system load baseline                      | Not started    |                                                                                                                                                                                                                                                                                                                                              |
| 14    | Comprehensive security hardening                             | Substrate only | `pkg/podsecurity` exists. The release pipeline, cosign verification, the final NetworkPolicy posture, and the pen-test driver are not.                                                                                                                                                                                                       |
| 14.5  | Post-hardening SLO re-validation                             | Not started    |                                                                                                                                                                                                                                                                                                                                              |
| 15    | Environment resource, RBAC                                   | Substrate only | `pkg/environment` exists. The environment admin API and the cross-environment delegation resolver are not.                                                                                                                                                                                                                                   |
| 16    | Experiments, PoolScalingController integration               | Partial        | `pkg/experiment` (the §10.7 enums, HMAC bucketing, status-transition rules) and the experiment admin API are built: `experimentstore` is the per-tenant ExperimentDefinition registry, and `/v1/admin/experiments` serves POST/GET/list/PUT/PATCH/DELETE with §10.7 definition validation and the PATCH status-transition lifecycle. The experiment router (`ExperimentRouter` interceptor), the variant-pool sizing path, the `/results` endpoint, and the cross-resource variant-pool isolation-monotonicity check are not built.                                                                                                                                                                                                                          |
| 16.5  | Experiment load test SLO re-validation                       | Not started    |                                                                                                                                                                                                                                                                                                                                              |
| 17a   | Documentation, governance, community launch                  | Not started    | The first-party reference runtimes from §26, the installer wizard, the tier preset values files, and the web playground are not built.                                                                                                                                                                                                       |
| 17b   | Memory, semantic caching, eval hooks                         | Not started    |                                                                                                                                                                                                                                                                                                                                              |

## Implemented surface

The following are built and tested at the unit tier.

- **Gateway request surface.** Session lifecycle and REST endpoints, the admin API,
  derive, blob dereference, the upload pipeline, `uploadToken`, OIDC and OAuth JWT
  validation, the OpenAPI document, the MCP adapter, the OpenAI Chat and Open Responses
  translators, rate limiting, idempotency, circuit breakers, quotas, billing events,
  user invalidation, the §12.8 GDPR user-erasure endpoints, and the watchdog timers.
- **Storage.** Postgres-backed stores for sessions, transcripts, tenants, runtimes,
  users, connectors, billing events, issued tokens, and the audit hash chain; Redis
  layers for circuit breakers, session-coordination leases, quota counters, and storage
  quotas; the MinIO-backed §4.5 blob store (`pkg/blobstore/miniostore`); migrations 0001
  through 0008 with RLS, immutability triggers, and role separation.
- **Kubernetes control plane.** The five `lenny.dev/v1` CRDs with generated manifests;
  the WarmPoolController (the pure planner, the reconciler, the `PoolWarmingUp`
  condition, an envtest integration test) with the `lenny-controller` binary; the
  PoolScalingController (config sync, demand-driven minWarm, the periodic runnable); the
  Helm chart with the controller Deployment and RBAC, the agent namespaces, and the
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

The gateway↔pod session path runs end to end through both `POST /v1/sessions/start` and
the two-step §15.1 `create → finalize → start` lifecycle. The remaining gaps, in
dependency order, are below.

- **gitClone ref resolution.** §15.1 pins `gitClone.resolvedCommitSha` at session
  creation. The gateway stores the submitted plan verbatim without resolving git refs.
- **Adapter workspace-staging RPCs.** §15.4 mandates separate `PrepareWorkspace`,
  `FinalizeWorkspace`, and `RunSetup` RPCs. The adapter bundles materialization, setup,
  and runtime start into `StartSession`, so the §15.1 finalize step short-circuits
  rather than materializing the workspace.
- **Remaining adapter RPCs.** The adapter serves `NegotiateVersion`, `StartSession`,
  `SendMessage`, `Interrupt`, `Shutdown`, `Attach`, the credential RPCs, and `DemoteSDK`.
  `Checkpoint`, `ReportUsage`, `ExtendLease`, and the `LifecycleChannel` stream are not
  built, and the adapter proto is still a Phase-1 skeleton behind §4.7 on the
  workspace-staging RPCs.
- **LLM Proxy.** Proxy-mode sessions cannot reach a provider; the LLM proxy subsystem
  and the `anthropic_direct` translator are not built.
- **Operational services.** `lenny-ops` and `lenny-backup` do not exist; `lenny-preflight`
  is built and deployable.

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
4. Build the gateway pod-claim path against `SandboxClaim`. Done — `pkg/gateway/podclaim.Claimer`,
   driven by `podsession.Binder` and wired into `cmd/lenny-gateway` behind `--agent-namespace`.
5. Wire workspace materialization and session start from the gateway to the adapter.
   Done — `pkg/gateway/adapterclient` plus `pkg/gateway/podsession`, with the
   `cmd/lenny-gateway` `--agent-namespace` wiring. `POST /v1/sessions/start` claims a
   pod, runs the §15.5 handshake and StartSession, and records the binding.
6. Build the LLM Proxy so a credential-proxied session can reach a provider.

**The `Attach` content path.** The per-message agent-response round-trip uses the
`Attach` bidirectional-streaming RPC (§4.7 RPC table, §15.4). It is built end to end:
the proto RPC (`3128fa7`), the adapter-server handler (`a21b2bf`), the gateway-side
`adapterclient.Attach` (`3390203`), and `executor.PodExecutor` (`c11f002`), which the
gateway selects as its `Executor` when `--agent-namespace` is set.

**Adapter §4.7 RPC surface.** The adapter `Server` implements `StartSession`,
`SendMessage`, `Attach`, `AssignCredentials`, `RotateCredentials`, `RevokeCredentials`,
`Interrupt`, `Checkpoint`, `Resume`, `ReportUsage`, `Shutdown`, `DemoteSDK`, and
`NegotiateVersion`. The remaining proto RPCs `ExtendLease` and `LifecycleChannel` are
still behind the Phase-1 skeleton (the embedded `UnimplementedAdapterServer` answers
`Unimplemented`).

## Next step

Phase 9 (§18.23) is substantially built — the §8.5 platform MCP tool surface, the §4
`RequestInterceptor` chain framework, the §4.7 `ReportUsage` adapter RPC, and the full
§8.10 delegation-tree recovery (archive-and-replay, `cascadeOnFailure` with the orphan
cap, `children_reattached`, orphan-cleanup job, bottom-up reattach traversal). Three
Phase 9 items remain blocked on infra or a spec ambiguity and are deferred:

- `delegation-echo` is a Standard-level runtime — §15.4.3 requires it to connect to the
  adapter's local platform MCP server over an abstract Unix socket with the manifest
  `mcpNonce` handshake. That intra-pod MCP-server infrastructure is not built.
- `ExtendLease`'s direction needs settling: the proto places it in the `Adapter`
  service (gateway → pod) but the §8.6 prose describes the adapter requesting budget
  from the gateway.
- The `PreDelegation` and `PreExportMaterialization` interceptor phases reuse the built
  chain framework but need their phase-specific payload plumbing (task input for
  `PreDelegation`, the §8.7 file export model for `PreExportMaterialization`).

Phase 10 (§18.25, MCP fabric / elicitation chain) is substantially built:
`pkg/elicitation`, the `lenny/request_elicitation` tool, the §9.1
`maxElicitationsPerSession` budget, the tenant elicitation-content-integrity admin
endpoints, the `lenny_elicitation_dropped_total` counter, and §9.2 depth-based
suppression. The remaining Phase 10 items — the gateway virtual MCP server (§9.1) and
the hop-by-hop elicitation chain (agent-interception forwarding up the task tree with
content-integrity binding) — are deferred: both are substantial orchestration with
uncertain v1 substrate.

Phase 11 (§18.26, advanced credentials / multi-provider translators) is substantially
built: all four §4.9 LLM-proxy translators, the `CredentialRenewalWorker`
proactive-renewal loop (`pkg/gateway/credrenewal`, including emergency-revocation
lease termination — a revoked credential's leases are dropped on the next sweep), and
the `credentialPolicy` fallback chain (`pkg/gateway/credfallback`). The two remaining
Phase 11 items are infra-coupled and deferred: hot rotation needs the adapter
lifecycle channel for the `credentials_rotated` / `credentials_acknowledged`
handshake, and cross-replica revocation propagation needs Redis pub/sub.

Phases 12–13: the pure substrate is already in place — `pkg/tokenexchange` (RFC 8693
`Validate`), the `pkg/audit` §11.7 enums, and the audit hash chain are all built. The
§12.8 GDPR erasure path is wired end to end: the `pkg/gateway/erasure` orchestrator
covers user-scoped stores (session, interaction) and session-scoped stores (transcript,
blob), the `pkg/gateway/erasurejob` registry and runner track the §12.8 phase field,
`POST /v1/admin/users/{user_id}/erase` plus `GET /v1/admin/erasure-jobs/{job_id}` are
served by the running gateway, and an erasure job marks the user §12.8 / GDPR Article
18 processing-restricted so new session creation is rejected with `ERASURE_IN_PROGRESS`
until it completes. The bulk of the remaining Phase 12–16 surface is infrastructure
integration that requires external systems to realize authentically — KMS envelope
encryption, the OAuth 2.1 connector flow, Redis pub/sub propagation, Kubernetes
concurrent-execution slots, the `lenny-backup` pipeline, SIEM streaming, and the
security-hardening release pipeline.

The §12.8 legal-hold control is built end to end: the `legal_hold` flag on sessions,
`POST /v1/admin/legal-hold`, the step-0 erasure preflight, the `acknowledgeHoldOverride`
bypass, and the §7.1 artifact-retention GC (`pkg/gateway/retentiongc`) that collects
expired-TTL session artifacts and exempts held sessions. The §11.4 `full_revoke`
SessionStore-side steps are built: non-terminal sessions are transitioned to
`cancelled` and the user's pending elicitations are dismissed. The §11.4 pod-RPC
`Terminate` fan-out, Redis cached-auth invalidation, and credential-lease revocation
remain infrastructure-bound.

The Phase 16 experiment admin API is built (`experimentstore` + `/v1/admin/experiments`
CRUD with the PATCH status lifecycle). The next pure-Go chunks are the Phase 15
environment admin API (the `pkg/environment` substrate is the selector/role library;
the Environment resource model, store, and `/v1/admin/environments` CRUD are not built)
and the Phase 9 `PreDelegation` interceptor wiring (the chain framework is built; the
`delegate_task` tool needs a `TaskSpec.input` field for the §4 `PreDelegation` content
payload before the phase can be wired).

## Test status

The unit test tier is green. The component, contract, integration, end-to-end, load,
chaos, and security tiers exist as directory structures with scaffolds; most are skipped
without the corresponding infrastructure. The WarmPoolController has an envtest
integration test that runs against a real Kubernetes API server.
