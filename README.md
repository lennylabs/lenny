[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Discord](https://img.shields.io/discord/1507235632966275085?logo=discord&logoColor=white&label=Discord&color=5865F2)](https://discord.gg/2kcteA8Y64)

# Lenny

Lenny is a self-hosted platform for long-running, interactive AI agent sessions on Kubernetes.

[Documentation](docs/) | [Implementation status](docs/about/status.md) | [Comparisons](docs/about/comparisons.md) | [Specification](spec/)

## Overview

Lenny is an open source system for running AI agent workloads on Kubernetes. It keeps pools of pre-warmed, sandboxed pods for registered runtimes, where a runtime is any containerized process that runs behind Lenny's runtime adapter to serve stateful agent sessions, stateless requests, or MCP tools. Each pool carries its own isolation, network egress, credential, and scaling policy, including how many sessions a pod serves before it is terminated. A gateway in front of the pools carries all client traffic, file transfers, and credential delivery, and it applies tenant and budget policy to each request.

## Project status

Lenny is at an early, pre-release stage. The [implementation status](docs/about/status.md) page records which parts of the platform run today.

## Workload fit

Lenny suits the following workloads:

- Long-running interactive sessions with streaming output, interrupts, follow-up prompts, and tool use.
- Agents that execute untrusted or LLM-generated code and need kernel-level sandboxing.
- Platforms that serve several teams or customers from one deployment, with per-tenant credentials, quotas, and audit.
- Multi-agent systems in which an agent starts child agents under budgets and permissions that the platform enforces.

Lenny does not suit GPU workloads, because it does not schedule GPUs, or computer-use and browser-automation agents that need a desktop or VNC session.

[Comparisons](docs/about/comparisons.md) sets Lenny against E2B, Daytona, Fly.io Sprites, Temporal, Modal, LangGraph and LangSmith, Google Scion, NVIDIA OpenShell, and Alibaba OpenSandbox.

## Architecture

![Request path in a Lenny cluster. Clients connect to the gateway over REST, MCP, OpenAI Chat Completions, or Open Responses. The gateway connects to the Token Service, Postgres and Redis, and object storage, and connects over mTLS gRPC to agent pods, each of which holds an adapter container and an agent container. Controllers manage the pods through Sandbox resources. A boundary marks the agent namespace and its default-deny network policy, and the only arrows entering the pods start at the gateway.](docs/assets/diagrams/request-path.svg)

<!--
ASCII fallback for the diagram above (request-path):

                 +--------------------------------+
                 | Clients: SDKs, CLI, MCP hosts  |
                 +--------------------------------+
                                 |
          REST, MCP, OpenAI Chat Completions, Open Responses
                                 v
+ Kubernetes cluster ...........................................+
:  +------------------------------------+     +---------------+ :
:  | Gateway                            | ==> | Token Service | :
:  | auth, policy, session routing,     | ==> | Postgres,     | :
:  | credential leasing, audit          |     | Redis         | :
:  +------------------------------------+ ==> | Object storage| :
:            |  mTLS gRPC  |                  +---------------+ :
:            v             v                                    :
:  + Agent namespace (default-deny; ingress only from gateway) + :
:  : +-------------+  +-------------+                          : :
:  : | Agent pod   |  | Agent pod   |  <== Controllers         : :
:  : |  Adapter    |  |  Adapter    |      (Warm Pool          : :
:  : |  Agent      |  |  Agent      |      Controller,         : :
:  : +-------------+  +-------------+      PoolScalingController): :
:  +.........................................................+ :
+...............................................................+
-->

Clients reach agent pods only through the gateway, which holds session state, credentials, and policy.

| Component             | Runs as                                                                                                   | Responsibility                                                                                                                                                                                  |
| :-------------------- | :-------------------------------------------------------------------------------------------------------- | :---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Gateway               | Stateless replicas behind a load balancer                                                                 | Authenticates clients (OIDC or OAuth 2.1), terminates the client protocols, routes sessions, runs the policy and interceptor chain, mediates file delivery, and hosts the delegation interface. |
| Token Service         | Separate Deployment with its own service account and KMS access                                           | Holds durable credential material and issues short-lived leases.                                                                                                                                |
| Warm Pool Controller  | Leader-elected controller                                                                                 | Creates and retires agent pods through the `kubernetes-sigs/agent-sandbox` CRDs, and keeps each pool at its warm count.                                                                         |
| PoolScalingController | Controller                                                                                                | Reconciles pool configuration from Postgres into CRDs and adjusts warm counts.                                                                                                                  |
| Runtime adapter       | Sidecar container in each agent pod, or a process in the agent container in the embedded deployment model | Speaks mTLS gRPC to the gateway, and JSON Lines and local MCP to the agent process.                                                                                                             |
| Agent runtime         | Container in each agent pod                                                                               | Runs the agent code that serves the session.                                                                                                                                                    |

[Architecture](docs/getting-started/architecture.md) describes each component and the trust boundaries between them.

## Runtime pools

A runtime is registered either as a standalone image or as a derivation of a base runtime that adds its own workspace defaults, setup commands, and policies. A pool is a warmable deployment target for one runtime and one operational profile. Each pool sets the following independently:

- The isolation profile (gVisor, Kata Containers, or runc) and the resource class.
- The network egress profile and the credential pool with its delivery mode.
- The execution mode and session policy, which control whether pods are reused or shared.
- The warm-pod range (`minWarm` and `maxWarm`), the maximum session age, and the checkpoint cadence.
- The behavior when no warm pod is free (`onPoolExhausted: reject` or `queue`).
- The workspace template that each warm pod materializes before a session claims it.

The PoolScalingController keeps each pool within its warm range, so a session starts on a pod that is already scheduled, running, and health-checked. A hot pool keeps `minWarm` above zero for immediate availability, and a cold pool sets `minWarm: 0` and creates pods on demand.

**SDK warm pools.** A runtime that declares `capabilities.preConnect: true` starts its agent process during warm-up, and the process waits connected for its first prompt. Every pod in that runtime's pools warms to this state, which removes the agent SDK's start-up time from session creation. When a session's workspace contains files that the agent reads at start-up (by default `CLAUDE.md` and `.claude/*`, configurable through `sdkWarmBlockingPaths`), the adapter restarts the agent process after the files arrive. The PoolScalingController reports a high restart rate and turns SDK warming off for a pool whose restart rate passes a safety threshold.

**Workspace templates.** A pool can declare a workspace template: repositories, archives, and files from the Artifact Store that every session on the pool starts from. The adapter materializes the template into a pod-level cache while the pod warms, so a repository clone or a large archive extraction completes before any session claims the pod. When a session is assigned, the adapter derives that session's writable workspace from the cache and then applies the session's own workspace plan on top. Sessions cannot write to the cache, so changes made by one session do not reach a later session on the same pod. Template sources resolve without a session, so a template cannot reference session uploads or use a credential lease. When a session's `gitClone` pins a commit that the cache does not hold, the session receives the same workspace that a cold clone produces.

**Execution modes.** By default a pod serves one session and terminates when the session ends. Two `sessionPolicy` settings raise the number of sessions a pod serves before it is terminated: `maxConcurrentSessions` sets how many sessions the pod hosts at once, and `recycle.maxSessionsPerPod` sets how many it serves over its lifetime when pod reuse is enabled. Each setting trades isolation for density and requires an explicit acknowledgment in the pool definition.

| Pod usage           | Configuration                                                          | Required acknowledgment              | Isolation between sessions                                                                                   |
| :------------------ | :--------------------------------------------------------------------- | :----------------------------------- | :----------------------------------------------------------------------------------------------------------- |
| One session per pod | `executionMode: session` (default)                                     | None                                 | Separate pod for each session.                                                                               |
| Pod reuse           | `sessionPolicy.recycle.enabled: true` with `recycle.maxSessionsPerPod` | `recycle.acknowledgeBestEffortScrub` | Sequential sessions of one tenant, separated by a best-effort scrub that is not a security boundary.         |
| Concurrent slots    | `sessionPolicy.maxConcurrentSessions` greater than 1                   | `acknowledgeProcessLevelIsolation`   | Process-level only, and each slot can read the other slots' credential files.                                |
| Service             | `executionMode: service`                                               | None                                 | Replicas serve successive requests with no scrub between them, no workspace, and no conversation continuity. |

The session creation response reports the resulting posture in `sessionIsolationLevel`, including `podReuse` and `residualStateWarning`, so a client can reject a session whose isolation does not meet its requirements.

## Security model

Lenny treats every agent pod as untrusted. The controls below apply to every pool, and the pool's isolation profile sets the kernel boundary.

**Pod hardening.** Each pod runs as a non-root user with all capabilities dropped and a read-only root filesystem. Its writable paths are limited to `/tmp`, the workspace, and the session and artifact directories. Admission webhooks reject host namespaces, unsigned images, images outside the trusted registries, and images that are not pinned by digest.

**Isolation profiles.** Each pool selects a profile, which maps to a Kubernetes RuntimeClass.

| Profile     | RuntimeClass    | Intended use                                         | Notes                                                                                                 |
| :---------- | :-------------- | :--------------------------------------------------- | :---------------------------------------------------------------------------------------------------- |
| `sandboxed` | gVisor          | Default for all workloads                            | Minimum recommended isolation for any workload that runs LLM-generated code.                          |
| `microvm`   | Kata Containers | Higher-risk, semi-trusted, or multi-tenant workloads | Runs in a separate namespace on a dedicated node pool.                                                |
| `standard`  | runc            | Recommended for development and testing              | A pool opts in with `allowStandardIsolation: true`. runc gives no protection against kernel exploits. |

**Network isolation.** A default-deny NetworkPolicy in every agent namespace admits ingress only from the gateway. The default `restricted` egress profile allows only the gateway and a dedicated DNS service, and the `provider-direct` and `internet` profiles widen egress for pools that need it.

**Data movement.** Pods have no shared volumes and no pod-to-pod network path, which keeps one session's data out of another session's reach. Agents receive and exchange data through channels the platform controls. A runtime's `sharedAssets` (artifacts such as model files or packed repositories, and inline files) are populated into every warm pod before the pod is claimed, under a read-only `/workspace/shared/` mount. A pool's workspace template is materialized into a pod-level cache at the same stage, and each session's writable workspace is derived from that cache when the session is assigned. Each session's workspace plan is applied after the pod is claimed: runtime `workspaceDefaults`, client uploads that the gateway validates, `gitClone` sources pinned to a commit for the session, and file exports from a parent session. The memory store keeps data across sessions. Checkpoints move to object storage only through single-use presigned URLs that the gateway issues, so the pod holds no object-store credential.

**Identity.** Clients authenticate with OIDC or OAuth 2.1, and token exchange can only narrow a token's scope. The gateway and each pod's adapter authenticate each other with mTLS, and each agent pod holds a short-lived certificate with a SPIFFE identity. The agent process never takes part in mTLS.

### Credential delivery

![Credential flow between the Token Service, the gateway, an agent pod, and an LLM provider. In proxy mode the pod sends an LLM request carrying a lease token, and the gateway's LLM proxy replaces the token with the provider key and forwards the request to the provider. A dashed path shows direct mode, in which the gateway writes a lease with provider credentials into the pod's credentials file and the pod calls the provider itself. Connector OAuth tokens stay in the Token Service and never reach the pod.](docs/assets/diagrams/credential-delivery.svg)

<!--
ASCII fallback for the diagram above (credential-delivery):

                    +------------------------------+
                    | Token Service                |   Connector OAuth tokens
                    |  credential pools (KMS)      |   never reach the pod
                    |  connector OAuth tokens      |
                    +------------------------------+
                                   |  provider credentials
                                   v
+------------------+   proxy mode:     +-----------------+  request with   +--------------+
| Agent pod        | ===============>  | Gateway         | ==============> | LLM provider |
|                  | request with      |  LLM Proxy      |  provider key   |              |
| credentials.json | leaseToken        |                 |                 |              |
+------------------+ <. . . . . . . .  +-----------------+                 +--------------+
        :            direct mode: lease with provider credentials                 ^
        :. . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . :
                 direct mode: request with provider credentials, sent from the pod
-->

Pods hold no standing credentials. Operators register LLM provider credentials in tenant-scoped credential pools, encrypted with a cloud KMS, and the gateway assigns each session its own short-lived lease. The gateway checks an emergency revocation list before every upstream call. Each pool sets `deliveryMode`:

- `proxy`, the default for multi-tenant deployments, gives the pod a lease token and a proxy URL. The gateway's LLM proxy substitutes the provider key, translates the request for Anthropic, AWS Bedrock, Google Vertex AI, or Azure OpenAI, and counts tokens. The provider key never leaves the gateway process.
- `direct`, the default for single-tenant deployments, writes the provider credentials into the pod for the lease's lifetime. It suits deployments in which the agent pod is trusted.

OAuth tokens for external connectors such as GitHub or Jira stay with the Token Service and the gateway, and the pod receives neither client tokens nor connector tokens.

**Audit.** Every session, task, and delegation produces append-only audit records covering the requester, the runtime, pool, and isolation profile, the delegation lineage, token usage, tool access, and policy denials. The records are linked by a SHA-256 hash chain and can be forwarded to a SIEM in OCSF format.

[Security principles](docs/operator-guide/security-principles.md) describes the full model and maps the controls to compliance frameworks.

## Multi-tenancy

One Lenny deployment serves many tenants, and every layer carries the tenant boundary.

- **Data.** Every record carries a `tenant_id`, and Postgres row-level security scopes every query to the caller's tenant. Credential pools are tenant-scoped in the same way.
- **Pods.** A pod that is reused or shared between sessions stays pinned to one tenant for its lifetime, and a fail-closed admission webhook prevents the tenant label from changing. Cross-tenant pod reuse is off by default and requires the `microvm` profile.
- **Deployment mode.** Setting `tenancy.mode: multi` turns on hard rejections for unsafe combinations, including direct credential delivery on runc pools and proxy lease tokens without SPIFFE binding.
- **Quotas and budgets.** Rate limits, concurrency limits, and token budgets apply per user, per tenant, per runtime, per pool, and per delegation tree. Usage and metering APIs and an append-only billing event stream report consumption per tenant.
- **Access control.** Environments select runtimes by label, and role-based access control governs which callers can use each environment.
- **Data residency and lifecycle.** `dataResidencyRegion` restricts a tenant's pods and storage to one region, including the pods of delegated child sessions. Erasure jobs delete a user's or a tenant's data from every store and record a receipt in the audit trail, and legal holds suspend deletion. `complianceProfile` (`soc2`, `fedramp`, or `hipaa`) tightens retention and integrity checks.

Tenants share the agent namespaces, and the separation between tenants is enforced by the database, the gateway, admission policy, and the isolation profile. Workloads that require dedicated nodes run on pools bound to dedicated node pools.

## Delegation controls

![Delegation tree with a root session, two child sessions, and one grandchild. Each edge carries the token budget and the isolation profile of the child, showing that budgets decrease and isolation stays equal or becomes stricter down the tree. One rejected edge, drawn in coral, shows an attempt to delegate to a weaker isolation profile.](docs/assets/diagrams/delegation-controls.svg)

<!--
ASCII fallback for the diagram above (delegation-controls):

                  +-------------------------------+
                  | Root session                  |
                  | 500,000 tokens, sandboxed     |
                  +-------------------------------+
                     |                         |
   200,000 tokens    |                         |   150,000 tokens
   sandboxed (equal) v                         v   microvm (stricter)
              +-----------+              +-----------+
              | Child A   |              | Child B   |
              +-----------+              +-----------+
                     |                         :
   80,000 tokens     |                         :   sandboxed (weaker than microvm)
   microvm (stricter)v                         v
              +-----------+              +----------------------------------+
              | Grandchild|              | Delegation rejected              |
              +-----------+              | ISOLATION_MONOTONICITY_VIOLATED  |
                                         +----------------------------------+

Isolation order: standard < sandboxed < microvm. The gateway carves each child's
token budget from the parent's remaining budget and rejects a child profile weaker
than the parent's.
-->

An agent starts a child session by calling the `lenny/delegate_task` tool. The child can run a different runtime in a different pool. The gateway and the Token Service enforce the following controls at every hop, so a misbehaving agent cannot bypass them:

- **Depth and fan-out.** The delegation lease caps tree depth (`maxDepth`), total and parallel children (`maxChildrenTotal` and `maxParallelChildren`), and tree size (`maxTreeSize`).
- **Token budget.** A child's budget is carved from the parent's remaining budget.
- **Permission scope.** A child's scope is a subset of its parent's scope.
- **Isolation monotonicity.** A child runs at its parent's isolation profile or a stricter one, in the order `standard`, `sandboxed`, `microvm`.
- **Content policy.** A child inherits its parent's `contentPolicy`, and inheritance can only tighten it.
- **Cycle detection.** The gateway rejects a delegation whose runtime and pool already appear in the session's lineage, unless the platform, the runtime, and the policy all allow self-recursion.
- **Child interface.** The parent sees each child through a virtual MCP interface hosted by the gateway, which carries status, results, elicitation, and cancellation, and never exposes pod addresses or credentials.

## Runtime integration

A runtime of `type: agent` talks to the adapter in its pod, and the platform features a runtime can use depend on which of the adapter's interfaces it implements. The interfaces are additive, and each tier is called an integration level.

| Integration level | Interface the runtime implements                                                 | Platform features available                                                                                         |
| :---------------- | :------------------------------------------------------------------------------- | :------------------------------------------------------------------------------------------------------------------ |
| Basic             | JSON Lines messages on stdin and stdout                                          | Session lifecycle, streaming text input and output, and recovery by restart. A pod failure loses in-flight context. |
| Standard          | The Basic interface, plus an MCP client connection to the adapter's local socket | Delegation, elicitation, structured output parts, memory tools, connectors, and best-effort checkpoints.            |
| Full              | The Standard interface, plus the adapter's lifecycle socket                      | Consistent checkpoints, clean interrupts, in-place credential rotation, and deadline warnings.                      |

At the Basic and Standard levels, a credential rotation checkpoints the session, restarts it on a new pod with a new lease, and resumes it, which the client observes as a reconnect. Existing harnesses run at the Basic level without an SDK and move up a level when they need the features it adds.

A runtime of `type: mcp` wraps an existing MCP server without code changes. Lenny serves it at `/mcp/runtimes/{name}` with the same isolation, credentials, egress control, and audit.

Runtime author SDKs for Go, Python, and TypeScript implement the wire format, and `lenny runtime init`, `lenny runtime validate`, and `lenny runtime publish` scaffold, test, and register a runtime. The reference runtime catalog defines runtimes for `claude-code`, `gemini-cli`, `codex`, `cursor-cli`, `chat`, `langgraph`, `mastra`, `openai-assistants`, and `crewai`, and the `echo` runtime is the conformance example. The [runtime author guide](docs/runtime-author-guide/) covers the contract.

## Client protocols

| Protocol                | Endpoint               | Suited to                                                                                                                                           |
| :---------------------- | :--------------------- | :-------------------------------------------------------------------------------------------------------------------------------------------------- |
| MCP (Streamable HTTP)   | `/mcp`                 | Interactive sessions with streaming, MCP Tasks, and elicitation. This is the recommended session path.                                              |
| REST                    | `/v1/...`              | Callers that cannot use MCP, and administrative and operator flows. Carries full message fidelity.                                                  |
| OpenAI Chat Completions | `/v1/chat/completions` | Existing OpenAI clients. Each HTTP call runs a single-shot session on a fresh pod.                                                                  |
| Open Responses          | `/v1/responses`        | Open Responses and OpenAI Responses clients. Each call runs a single-shot session, and `previous_response_id` replays prior context into a new pod. |

All protocols pass through the same gateway authentication, policy, quota, and audit path. The OpenAI-compatible adapters drop or flatten some message fields, so callers that need full fidelity use REST or MCP. Client SDKs for Go and TypeScript wrap session management and streaming. The [client guide](docs/client-guide/) documents each protocol.

## Operations

A production deployment requires the following components, which the operator runs and secures:

- Kubernetes 1.27 or later, with a CNI that enforces NetworkPolicy egress rules and the gVisor RuntimeClass (and Kata Containers for `microvm` pools).
- Postgres 14 or later with high availability, Redis with TLS and authentication, and S3-compatible object storage such as MinIO, S3, GCS, or Azure Blob Storage.
- cert-manager 1.12.0 or later, a cloud KMS (AWS KMS, Google Cloud KMS, or Azure Key Vault), an OIDC identity provider, and OPA Gatekeeper or Kyverno for admission policy.

The Helm chart installs Lenny, and a `lenny-preflight` job validates the dependencies first. The `lenny-ctl install` wizard detects the cluster, asks for the deployment choices, previews the Helm values, installs, and runs a smoke test, and it saves its answers for non-interactive installs in CI.

The `lenny-ops` management plane runs in every deployment and serves diagnostics, runbooks, drift detection, backup and restore, and an MCP management server, so a human operator or an AI operations agent can inspect and repair the platform through structured APIs. `lenny-ctl doctor --fix` applies idempotent remediations. The chart ships a Prometheus alerting rule catalog with tunable thresholds and, under `monitoring.openslo.enabled`, OpenSLO documents.

The [operator guide](docs/operator-guide/) covers installation, sizing, and upgrades.

## Extension points

Lenny defines interfaces for AI-specific concerns and leaves the logic to external systems. Each interface has a default implementation, stays off until an operator turns it on, and accepts a replacement.

- **Request interceptors** run at gateway phases such as `PreDelegation`, `PreLLMRequest`, and `PostAgentOutput`, and call external classifiers such as AWS Bedrock Guardrails, Azure Content Safety, or Lakera Guard over gRPC.
- **Memory store** defaults to Postgres with pgvector and accepts other backends such as Mem0 or Zep.
- **Credential routing and semantic caching** select credentials by cost, latency, or intent, and cache LLM responses.
- **Experiments** route sessions to variant pools by deterministic bucketing or through an OpenFeature provider such as LaunchDarkly, Statsig, or Unleash, and deliver the chosen variant to the runtime at session start. Lenny leaves targeting, statistics, and winner selection to the experimentation platform.
- **Evaluation** stores scores against sessions through `POST /v1/sessions/{id}/eval`, and `tracingContext` lets external platforms such as LangSmith or Braintrust stitch traces across a delegation tree.

Lenny ships no evaluation runner, judge model, guardrail classifier, or memory extraction.

## Trade-offs

- Warm pods consume cluster resources while idle. Scale-to-zero schedules reduce the cost outside peak hours.
- Gateway-mediated file delivery adds workspace materialization to every session start in exchange for isolation and a complete audit record. A pool workspace template moves the content common to every session on the pool to warm time.
- Credential pools, leases, and rotation take more setup than mounting provider keys into pods.
- Clean interrupts, consistent checkpoints, and in-place credential rotation require a runtime to implement the Full integration level.
- Evaluation pipelines, memory extraction, and experiment lifecycle management come from external tools that the operator chooses and runs.

## Local trial

You can try Lenny locally by starting a single-host stack with embedded k3s, running a session against the credential-free `echo` runtime, and tearing the stack down:

```bash
lenny up
lenny session new --runtime=echo --attach "hello"
lenny down
```

On macOS and Windows, `lenny up` requires Docker Desktop. The local stack runs every isolation profile as runc, does not enforce NetworkPolicy, keeps Postgres, Redis, and object storage in memory so their contents are lost on `lenny down`, and listens only on localhost. Reference runtimes other than `echo` need a runnable image and a warm pool before they accept sessions.

Contributors working on the platform run the gateway as a native process with SQLite, local-filesystem artifacts, and the `echo` runtime, without Kubernetes or Docker:

```bash
make run    # gateway on localhost:8080
```

The [quickstart](docs/getting-started/quickstart.md) walks through both paths.

## Documentation

- **Evaluation:** [Comparisons](docs/about/comparisons.md) and [Architecture](docs/getting-started/architecture.md).
- **Client development:** [Client guide](docs/client-guide/).
- **Runtime authoring:** [Runtime author guide](docs/runtime-author-guide/).
- **Operation:** [Operator guide](docs/operator-guide/) and [Agent operability](docs/operator-guide/agent-operability.md).
- **Security review:** [Security principles](docs/operator-guide/security-principles.md) and the [security model specification](spec/13_security-model.md).
- **Contribution:** [Contributor guide](docs/about/contributing.md).
- **Reference:** the [technical specification](spec/) and the [roadmap](ROADMAP.md).

## Contributing

Lenny is developed spec-first: the [specification](spec/) defines each behavior before code implements it, and tests cite the section they verify. The project accepts the following contributions now:

- **Specification feedback.** Open an [issue](https://github.com/lennylabs/lenny/issues) or a [discussion](https://github.com/lennylabs/lenny/discussions) with questions, disagreements, or missing cases.
- **Security review.** Threat-model the design in the [security model](spec/13_security-model.md), and report vulnerabilities through [SECURITY.md](SECURITY.md) rather than a public issue.
- **Runtime adapter sketches.** Prototype an adapter for a framework against the [runtime author guide](docs/runtime-author-guide/) and report gaps in the contract.
- **Use-case reports.** Describe where Lenny fits or does not fit a workflow.

Small documentation fixes are accepted at any time. Unsolicited code pull requests are not reviewed until the community launch. Commits carry a DCO sign-off (`git commit -s`). [CONTRIBUTING.md](CONTRIBUTING.md) states the current policy, and [GOVERNANCE.md](GOVERNANCE.md) describes how decisions are made.

## License

Lenny is licensed under the [MIT License](LICENSE). The runtime author SDKs are licensed under Apache-2.0.
