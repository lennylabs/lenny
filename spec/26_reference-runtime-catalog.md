## 26. Reference Runtime Catalog

This appendix catalogs the **reference runtimes** shipped by the Lenny project as first-party, maintained Runtime definitions. Each entry is a complete, working runtime that a deployer can install and use for real workloads without additional coding. They serve two purposes:

1. **Day-one utility** — an operator running `lenny up` has functioning agents to work with, not just `echo`. The coding-agent runtimes (`claude-code`, `gemini-cli`, `codex`, `cursor-cli`) cover the most common developer use case directly; the framework runtimes cover popular agent frameworks so teams can adopt Lenny without rewriting their agents.
2. **Authoring reference** — each runtime is a worked example of the adapter contract ([§15.4](15_external-api-surface.md#154-runtime-adapter-specification)), the `runtimeOptions` schema ([§14](14_workspace-plan-schema.md)), credential-lease scoping ([§4.9](04_system-components.md#49-credential-leasing-service)), and workspace materialization ([§14](14_workspace-plan-schema.md)). Teams building Standard- or Full-level runtimes SHOULD start from the scaffolder (`lenny-ctl runtime init`, [§24.18](24_lenny-ctl-command-reference.md#2418-runtime-scaffolding)), which copies one of these as a template. Basic-level runtimes MAY implement the stdin/stdout protocol directly without any SDK — see [§15.4.4](15_external-api-surface.md#1544-sample-echo-runtime) for the minimal echo-runtime example and [§24.18](24_lenny-ctl-command-reference.md#2418-runtime-scaffolding) for the `--language binary --template minimal` scaffolder output, which emits a Basic-level-compliant skeleton with no SDK imports.

**Ownership and lifecycle.** Reference runtimes live in first-party repositories under `github.com/lennylabs/runtime-<name>`. Each ships a Dockerfile, adapter implementation, `Runtime` YAML, conformance test suite ([§15.4.6](15_external-api-surface.md#1546-conformance-test-suite)), and CI that publishes OCI images to the canonical Lenny registry. Reference runtimes claim a **conformance level** in their README — defined as equal to the Integration Level from [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels) (Basic, Standard, or Full); CI fails the release if conformance tests for the claimed level regress.

**Distinction from bundled runtimes.** The `echo` runtime ([§15.4.4](15_external-api-surface.md#1544-sample-echo-runtime)) is a conformance exemplar embedded in the platform repo. Reference runtimes are first-party but live in separate repos with independent release cadences, since they track upstream framework/CLI changes.

---

### 26.1 Catalog Overview

| Runtime            | Category        | Level    | Primary use case                                                                 |
|--------------------|-----------------|----------|----------------------------------------------------------------------------------|
| `claude-code`      | Coding agent    | Full     | Anthropic's Claude Code CLI inside a Lenny-managed sandbox                       |
| `gemini-cli`       | Coding agent    | Full     | Google's Gemini CLI inside a Lenny-managed sandbox                               |
| `codex`            | Coding agent    | Full     | OpenAI's Codex CLI inside a Lenny-managed sandbox                                |
| `cursor-cli`       | Coding agent    | Full     | Cursor's agent CLI inside a Lenny-managed sandbox                                |
| `chat`             | General-purpose | Standard | "Talk to an LLM" with no tools; demonstrates the minimum useful runtime          |
| `langgraph`        | Framework       | Full     | LangGraph graph-based agents (Python)                                            |
| `mastra`           | Framework       | Full     | Mastra agent framework (TypeScript)                                              |
| `openai-assistants`| Framework       | Full     | OpenAI Assistants API-compatible runtime                                         |
| `crewai`           | Framework       | Full     | CrewAI multi-agent framework with delegation wired to Lenny's `lenny/delegate_task` |

All reference runtimes are **`type: agent`** ([§5.1](05_runtime-registry-and-pool-model.md#51-runtime)). No reference runtime is `type: mcp` — MCP servers are deployed via the MCP connector mechanism ([§9.3](09_mcp-integration.md#93-connector-definition-and-oauthoidc)), not as reference runtimes.

**Tenant access.** Reference runtimes are registered by `lenny-ctl install` (§17.6, §24.20) as platform-global records with no default tenant access grants. Operators grant access per tenant via `POST /v1/admin/runtimes/{name}/tenant-access` with body `{"tenantId": "<uuid>"}` ([§15.1](15_external-api-surface.md#151-rest-api)) after install. For `local` profile installations, `lenny up` auto-grants access to the `default` tenant for every reference runtime it installs so the developer can invoke them without additional setup.

---

### 26.2 Shared Patterns for Coding-Agent Runtimes

The four coding-agent runtimes (`claude-code`, `gemini-cli`, `codex`, `cursor-cli`) share a common workspace shape. They differ only in: image, LLM provider credential, and the name of the shell command invoked inside the pod. This section defines the shared pattern so individual entries stay focused on the differences.

**Isolation profile.** All coding-agent runtimes use `isolationProfile: sandboxed` (gVisor) by default. Agents execute untrusted shell commands on behalf of the user, so host-kernel isolation is mandatory. Operators requiring stronger isolation MAY configure a `microvm` (Kata) pool via `allowedResourceClasses` and pool-level overrides; `standard` (runc) is **not** supported for coding-agent runtimes and the gateway rejects pool definitions that pair any coding-agent runtime with `isolationProfile: standard`.

**Workspace layout.** Per [§6.4](06_warm-pod-model.md#64-pod-filesystem-layout), each session's workspace lives at `/workspace/current/`. Coding-agent runtimes add the following conventions:

- `/workspace/current/` is the **repo root** (`pwd` inside the shell defaults here).
- `.git/` is materialized by the gateway when the client's `WorkspacePlan` includes a `sources[].type: gitClone` entry; otherwise the workspace is a plain directory.
- `/workspace/shared/` ([§5.1](05_runtime-registry-and-pool-model.md#51-runtime), `sharedAssets`) is used only for `concurrent` pools; coding-agent runtimes default to `executionMode: session` and do not use it.
- `/workspace/output/` is provided for agents to write artifacts that should survive session teardown and be recoverable via `GET /v1/sessions/{id}/artifacts` ([§15.1](15_external-api-surface.md#151-rest-api)). Agents MUST write intended outputs here; the rest of `/workspace/current/` is ephemeral.

**Pre-installed tools.** The reference image for every coding-agent runtime includes: `bash`, `sh`, `git`, `curl`, `jq`, `ripgrep`, `fd`, `python3`, `node` (current LTS), `go` (current stable), `rustc`/`cargo`, `make`. Language toolchains for less-common ecosystems (Ruby, Java, Swift) are not pre-installed; `setupCommands` or user-supplied `sources[]` install them per-session. The pre-installed set is chosen to cover the top languages without bloating the image beyond ~1.5 GB.

**Egress profile.** Coding-agent runtimes default to `egressProfile: restricted` ([§6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)): HTTPS to the operator-configured allowlist (package registries, container registries, LLM provider domains, the operator's Git hosts). Unrestricted egress is explicitly supported but MUST be opted into per pool via `egressProfile: unrestricted` — the operator accepts the risk. See [§13.2](13_security-model.md#132-network-isolation).

**Credential delivery.** All coding-agent runtimes use `deliveryMode: proxy` by default ([§4.9](04_system-components.md#49-credential-leasing-service)): the LLM proxy is the sole egress path for provider API traffic and captures per-session token/cost metering. The runtime does not see the provider API key. `deliveryMode: direct` is supported for air-gapped environments where the operator has already provisioned provider keys as `Secret` volumes and does not need Lenny's credential leasing.

**`setupCommandPolicy`.** Coding-agent runtimes ship with `mode: allowlist` and an allowlist covering the common package-manager prefixes: `npm ci`, `npm install`, `pnpm install`, `yarn install`, `pip install`, `poetry install`, `go mod download`, `cargo fetch`, `make`, `bundle install`, `gem install`, `mvn`, `gradle`, `apt-get install`, `chmod`, `mkdir`, `cp`, `mv`, `ln`. `shell: false` — setup commands are argv-form, not shell-string. Operators can override the policy per pool but MUST NOT set `mode: none` (unrestricted setup) for coding-agent runtimes in multi-tenant deployments.

**`capabilities`.**

```yaml
capabilities:
  interaction: multi_turn
  injection:
    supported: true
    modes: [immediate, queued]
  preConnect: false
```

`preConnect: false` is the default for every coding-agent runtime — SDK-warm mode is an optimization that operators can enable per pool for latency-sensitive deployments but is not on by default (see [§6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like) demotion semantics).

**Common `limits`.**

```yaml
limits:
  maxSessionAge: 14400           # 4 hours — coding sessions are often long
  maxUploadSize: 500MB
  maxRequestInputWaitSeconds: 1800  # 30 min — developer may step away
```

**Common `credentialCapabilities`.**

```yaml
credentialCapabilities:
  hotRotation: true
  proxyDialect: [openai, anthropic]  # Lenny LLM-proxy dialects the runtime speaks
```

Individual runtimes restrict `proxyDialect` to only the dialects their underlying CLI actually uses; see each entry for the restricted set.

**Reference `WorkspacePlan`.** The following minimal plan is used by the `lenny session new --attach` CLI ([§24.17](24_lenny-ctl-command-reference.md#2417-session-operations)) when the client passes `--workspace=<local-path>`:

```json
{
  "pool": "<runtime-name>-default",
  "workspacePlan": {
    "$schema": "https://schemas.lenny.dev/workspaceplan/v1.json",
    "schemaVersion": 1,
    "sources": [
      { "type": "uploadArchive", "pathPrefix": ".", "uploadRef": "<upload_id>", "format": "tar.gz" }
    ],
    "setupCommands": []
  },
  "env": {},
  "timeouts": { "maxSessionAgeSeconds": 14400, "maxIdleSeconds": 900 },
  "runtimeOptions": { /* per-runtime options per §14 */ }
}
```

The `uploadArchive` entry is populated by the CLI: it tars the `--workspace` directory (respecting a `.lennyignore` file if present; otherwise `.gitignore`), uploads it to the gateway via the upload API ([§15.1](15_external-api-surface.md#151-rest-api)), and references the resulting upload ID.

**Credential-lease scopes (shared).** Every coding-agent runtime declares lease scopes for its LLM provider. The scope naming follows [§4.9](04_system-components.md#49-credential-leasing-service):

- `llm.provider.<name>.inference` — required; issued by the credential leasing service for the pool-configured provider identity; attached as a header the LLM proxy injects on the runtime's behalf (proxy mode) or as an env var (direct mode).
- `vcs.github.read` / `vcs.github.write` — optional; only issued when the client's `WorkspacePlan.sources[]` contains a `gitClone` entry targeting a private repo. The gateway's credential router resolves the scope from the tenant's configured GitHub credential pool. The runtime never sees the raw token; `git` inside the pod uses a credential helper that calls the gateway's token endpoint.

Runtimes that need additional provider scopes (e.g., Vertex for Gemini, Azure OpenAI for Codex variants) declare them in their individual entries below.

**Example session invocation (applies to all four).**

```
$ lenny session new --runtime=<name> --workspace=./my-repo --attach \
    "Refactor the auth module to use the new token-exchange flow"
```

The CLI uploads the workspace, creates the session via the gateway REST API, then attaches via the MCP WebSocket and streams the runtime's output to stdout until completion or Ctrl-C. See [§24.17](24_lenny-ctl-command-reference.md#2417-session-operations) for full command reference.

---

### 26.3 `claude-code`

Anthropic's Claude Code CLI running inside a Lenny-managed sandbox.

**Repository:** `github.com/lennylabs/runtime-claude-code`
**Image:** `ghcr.io/lennylabs/runtime-claude-code:<release>` (semver-tagged; `:latest` pinned to the latest release)
**Conformance level:** Full
**Runtime `type`:** `agent`

**`Runtime` definition** (platform-global; installed by `lenny-ctl install` or `lenny up`):

```yaml
name: claude-code
image: ghcr.io/lennylabs/runtime-claude-code:1.0.0
type: agent
capabilities:
  interaction: multi_turn
  injection:
    supported: true
    modes: [immediate, queued]
  preConnect: false
executionMode: session
isolationProfile: sandboxed
allowedResourceClasses: [small, medium, large]
supportedProviders:
  - anthropic_direct
  - aws_bedrock
  - gcp_vertex_anthropic
credentialCapabilities:
  hotRotation: true
  proxyDialect: [anthropic]
limits:
  maxSessionAge: 14400
  maxUploadSize: 500MB
  maxRequestInputWaitSeconds: 1800
setupCommandPolicy:
  mode: allowlist
  shell: false
  allowlist: [npm, pnpm, yarn, pip, poetry, go, cargo, make, mvn, gradle, apt-get, chmod, mkdir, cp, mv, ln]
  maxCommands: 20
setupPolicy:
  timeoutSeconds: 600
  onTimeout: fail
runtimeOptionsSchema:
  # See §14 claude-code entry for the canonical schema; this field references it by value.
  $ref: "https://schemas.lenny.dev/runtime-options/claude-code/v1.json"
defaultPoolConfig:
  warmCount: 2           # local profile override: 0 (cold start OK on laptop)
  resourceClass: medium
  egressProfile: restricted
agentInterface:
  description: "Claude Code — Anthropic's general-purpose coding agent"
  inputModes: [{type: "text/plain"}]
  outputModes:
    - {type: "text/plain", role: "primary"}
    - {type: "application/json", role: "tool_events"}
  supportsWorkspaceFiles: true
  skills:
    - {id: "code", name: "Coding",        description: "Read, edit, and create code across a repository"}
    - {id: "debug", name: "Debugging",    description: "Reproduce and diagnose defects"}
    - {id: "refactor", name: "Refactoring", description: "Restructure code without behavior change"}
    - {id: "review", name: "Code review", description: "Review diffs and suggest improvements"}
labels:
  maintainer: lennylabs
  upstream: anthropic/claude-code
```

**`runtimeOptions` schema:** defined in [§14](14_workspace-plan-schema.md) under the `claude-code runtime` heading. The schema covers `model`, `settingSources`, `streamingMode`, `maxTokens`, `temperature`, and `thinkingBudget`.

**Reference `WorkspacePlan` (full).** The shared pattern in §26.2 applies. Claude Code reads `CLAUDE.md` and `.claude/settings.json` from the workspace root at startup to configure itself; these files are not special to the runtime but are common in Claude Code projects. The runtime does not pre-create them — users populate them via `sources[].type: inlineFile` entries on the `WorkspacePlan` or via a Git clone source.

**Credential-lease scopes (Claude Code-specific).**

| Scope                              | When issued                                             | Consumer                                                   |
|------------------------------------|---------------------------------------------------------|------------------------------------------------------------|
| `llm.provider.anthropic.inference` | Always (or one of `aws.bedrock.anthropic.inference` / `gcp.vertex.anthropic.inference` if the pool's `supportedProviders` selects those). | LLM proxy — injected into outbound Anthropic API calls; the `claude` CLI inside the pod sees only the proxy endpoint. |
| `vcs.github.read` / `vcs.github.write` | When `WorkspacePlan.sources[]` contains a `gitClone` pointing at a private GitHub repo. | `git` credential helper inside the pod; short-lived token scoped to the single repo. |

The runtime does **not** request or use `anthropic.api.write` (fine-tuning/file-upload scopes) — Claude Code is inference-only.

**Bootstrap behavior.**

1. Warm-pool controller pulls the image and starts the pod in INIT state.
2. Adapter binary boots, registers with the gateway, and signals READY.
3. On session claim: gateway materializes the `WorkspacePlan` into `/workspace/current/`; runs any `setupCommands`; emits `session.created`.
4. On first `message`: adapter invokes `claude --no-tty --print --session-id=<id>` and pipes the message to stdin. All subsequent messages for the session reuse the same `claude` process (multi-turn).
5. Tool calls emitted by `claude` are translated by the adapter into Lenny's `tool_call` message envelope ([§15.4.1](15_external-api-surface.md#1541-adapterbinary-protocol)); tool results are injected back via `tool_result`.
6. On session termination: adapter sends SIGTERM to `claude`; waits up to 5s for graceful shutdown; force-kills on timeout. Any files written under `/workspace/output/` are sealed as session artifacts.

**Setup-time image entrypoint:** `/usr/local/bin/lenny-claude-code-adapter` (the adapter binary; NOT the `claude` CLI directly). The adapter owns lifecycle and delegates message handling to `claude`.

**Example session invocation.**

```
$ lenny session new --runtime=claude-code --workspace=./my-repo --attach \
    "Find all uses of the deprecated AuthV1 API and migrate them to AuthV2."
```

The CLI uploads the repo, creates the session, and streams Claude Code's output (tool calls, intermediate reasoning if `thinkingBudget > 0`, final text) to stdout until the agent produces a `response` or the user presses Ctrl-C.

**Delegation behavior.** Claude Code does not delegate by default. A derived runtime (e.g., `claude-code-orchestrator`) can enable delegation by attaching a `delegationPolicyRef` and exposing `lenny/delegate_task` in the tool set. The reference implementation does not configure this — see [§8](08_recursive-delegation.md) for the delegation model.

**Release cadence and upstream tracking.** The runtime pins an exact `claude` CLI version in its `Dockerfile` per release. Upstream CLI releases trigger a new runtime minor version; breaking CLI changes trigger a new runtime major version with a migration note.

---

### 26.4 `gemini-cli`

Google's Gemini CLI running inside a Lenny-managed sandbox.

**Repository:** `github.com/lennylabs/runtime-gemini-cli`
**Image:** `ghcr.io/lennylabs/runtime-gemini-cli:<release>`
**Conformance level:** Full
**Runtime `type`:** `agent`

**Differences from the shared coding-agent pattern (§26.2):**

- `supportedProviders: [gcp_vertex_gemini, google_ai_studio]`.
- `credentialCapabilities.proxyDialect: [google]`.
- Provider scope: `llm.provider.google.inference` or `gcp.vertex.gemini.inference` depending on the pool's selected provider identity.
- `runtimeOptionsSchema` registered at `https://schemas.lenny.dev/runtime-options/gemini-cli/v1.json`; schema body in [§14](14_workspace-plan-schema.md).
- Entrypoint inside the pod: `/usr/local/bin/lenny-gemini-cli-adapter` → invokes the upstream `gemini` CLI with `--non-interactive --session-id=<id>`.
- `agentInterface.description`: `"Gemini CLI — Google's general-purpose coding agent"`.

All other fields (isolation, limits, `setupCommandPolicy`, egress defaults, workspace conventions) inherit from §26.2.

---

### 26.5 `codex`

OpenAI's Codex CLI running inside a Lenny-managed sandbox.

**Repository:** `github.com/lennylabs/runtime-codex`
**Image:** `ghcr.io/lennylabs/runtime-codex:<release>`
**Conformance level:** Full
**Runtime `type`:** `agent`

**Differences from the shared coding-agent pattern (§26.2):**

- `supportedProviders: [openai_direct, azure_openai]`.
- `credentialCapabilities.proxyDialect: [openai]`.
- Provider scope: `llm.provider.openai.inference` or `azure.openai.inference`.
- `runtimeOptionsSchema` registered at `https://schemas.lenny.dev/runtime-options/codex/v1.json`.
- Entrypoint: `/usr/local/bin/lenny-codex-adapter` → invokes the upstream `codex` CLI with `--headless --session-id=<id>`.
- `agentInterface.description`: `"Codex CLI — OpenAI's coding agent"`.

Codex supports the OpenAI Responses API and the Chat Completions API; the adapter selects the former when the pool's provider identity advertises Responses support, falling back to Chat Completions otherwise. Responses vs. Chat Completions selection is transparent to the client.

---

### 26.6 `cursor-cli`

Cursor's agent CLI running inside a Lenny-managed sandbox.

**Repository:** `github.com/lennylabs/runtime-cursor-cli`
**Image:** `ghcr.io/lennylabs/runtime-cursor-cli:<release>`
**Conformance level:** Full
**Runtime `type`:** `agent`

**Differences from the shared coding-agent pattern (§26.2):**

- `supportedProviders: [cursor_direct]` (Cursor's own API; the CLI does not accept raw OpenAI/Anthropic keys).
- `credentialCapabilities.proxyDialect: [cursor]` — Lenny's LLM proxy gains a `cursor` dialect (§4.9) covering Cursor's inference surface. Deployers who do not configure a Cursor credential pool can only register this runtime in direct-delivery mode.
- Provider scope: `llm.provider.cursor.inference`.
- `runtimeOptionsSchema` registered at `https://schemas.lenny.dev/runtime-options/cursor-cli/v1.json`.
- Entrypoint: `/usr/local/bin/lenny-cursor-cli-adapter` → invokes the upstream `cursor-agent` CLI with `--non-interactive --session-id=<id>`.
- `agentInterface.description`: `"Cursor CLI — Cursor's agent CLI"`.

**Note on LLM-proxy dialect.** Cursor's API surface is proprietary; the `cursor` dialect in Lenny's LLM proxy (§4.9) implements the public subset documented by Cursor and passes proxying requests through. Operators should pin a specific `cursor-cli` runtime version against a matching proxy version to avoid drift when Cursor's API evolves.

---

### 26.7 `chat`

A minimal general-purpose "talk to an LLM" runtime. No tools, no workspace files, no shell. Demonstrates the smallest useful Full-level runtime.

**Repository:** `github.com/lennylabs/runtime-chat`
**Image:** `ghcr.io/lennylabs/runtime-chat:<release>`
**Conformance level:** Full
**Runtime `type`:** `agent`

**`Runtime` definition (highlights; full definition in the repo):**

```yaml
name: chat
image: ghcr.io/lennylabs/runtime-chat:1.0.0
type: agent
capabilities:
  interaction: multi_turn
  injection: { supported: true, modes: [immediate] }
  preConnect: false
executionMode: session
isolationProfile: sandboxed       # sandboxed is still the default; runc is acceptable for this runtime specifically
                                  # because there is no shell or user-supplied code execution
allowedResourceClasses: [small]
supportedProviders:
  - anthropic_direct
  - openai_direct
  - gcp_vertex_gemini
credentialCapabilities:
  hotRotation: true
  proxyDialect: [anthropic, openai, google]
limits:
  maxSessionAge: 3600
  maxUploadSize: 10MB             # file attachments only; no archive uploads
  maxRequestInputWaitSeconds: 600
setupCommandPolicy:
  mode: none                      # no setup commands supported
setupPolicy:
  timeoutSeconds: 0
runtimeOptionsSchema:
  $ref: "https://schemas.lenny.dev/runtime-options/chat/v1.json"
defaultPoolConfig:
  warmCount: 1
  resourceClass: small
  egressProfile: restricted
```

**Workspace:** empty by default. The client MAY attach up to 10 MB of files via `sources[].type: uploadFile`; the runtime exposes them to the model as file parts on the user message but does not permit shell access. The image does not include toolchains or shell utilities beyond what the adapter requires.

**Credential-lease scopes:** `llm.provider.<name>.inference` per selected provider. No VCS or filesystem scopes.

**Bootstrap:** adapter binary is the only process; on `message`, the adapter constructs a provider-dialect request (selected by pool provider identity), sends it via the LLM proxy, and streams the response back as `response` output parts.

**Use case:** the `chat` runtime is what `lenny up` uses as the zero-config default when no workspace is attached (`lenny session new --runtime=chat --attach "hello"`). It's also the reference for teams building custom non-coding runtimes.

---

### 26.8 `langgraph`

LangGraph (Python) agent runtime. Framework-specific: the runtime loads a LangGraph `Graph` object from a Python module path at session start.

**Repository:** `github.com/lennylabs/runtime-langgraph`
**Image:** `ghcr.io/lennylabs/runtime-langgraph:<release>`
**Conformance level:** Full
**Runtime `type`:** `agent`

**Key characteristics:**

- `isolationProfile: sandboxed` (Python runs arbitrary user code via the loaded graph).
- `capabilities.interaction: multi_turn`; `injection: { supported: true, modes: [immediate, queued] }`.
- `runtimeOptionsSchema` already in [§14](14_workspace-plan-schema.md) — `graphModule` (required), `checkpointBackend`, `recursionLimit`, `configSchema`.
- `supportedProviders: [anthropic_direct, openai_direct, gcp_vertex_gemini, azure_openai]` — LangGraph graphs can route to any provider via LangChain's model interface; the runtime configures the provider client based on the pool's provider identity.
- `credentialCapabilities.proxyDialect: [anthropic, openai, google]`.

**Workspace conventions:**

- `/workspace/current/` contains the user's graph definition (uploaded as `sources[]`) plus a `requirements.txt` or `pyproject.toml`.
- `setupCommands` typically runs `pip install -r requirements.txt` or `poetry install` before the graph module is importable. Allowlist covers both.
- Checkpointing (when `runtimeOptions.checkpointBackend: postgres` or `redis`) uses Lenny's platform Postgres/Redis, scoped to the session's tenant. The runtime's adapter connects via short-lived credentials issued by the credential leasing service with scope `datastore.checkpoint.rw`.

**Bootstrap:** adapter imports the module specified by `runtimeOptions.graphModule`, invokes `.compile()` on the graph, and attaches a LangGraph `RunnableConfig` whose `configurable` field is populated from `runtimeOptions.configSchema`. Subsequent `message` deliveries invoke `graph.ainvoke` / `graph.astream` depending on the graph's declared output style.

**Delegation:** LangGraph graphs may call `lenny/delegate_task` as a tool. The runtime exposes the delegation tool via the adapter's tool-registration hook when the Runtime's `delegationPolicyRef` is set.

---

### 26.9 `mastra`

Mastra (TypeScript) agent framework runtime.

**Repository:** `github.com/lennylabs/runtime-mastra`
**Image:** `ghcr.io/lennylabs/runtime-mastra:<release>`
**Conformance level:** Full
**Runtime `type`:** `agent`

**Key characteristics:**

- `isolationProfile: sandboxed`.
- `capabilities.interaction: multi_turn`; `injection: { supported: true, modes: [immediate, queued] }`.
- `runtimeOptionsSchema` at `https://schemas.lenny.dev/runtime-options/mastra/v1.json` — loads a Mastra agent definition by module path.
- `supportedProviders: [anthropic_direct, openai_direct, gcp_vertex_gemini]`.
- `credentialCapabilities.proxyDialect: [anthropic, openai, google]`.

**Workspace conventions:**

- `/workspace/current/` contains the user's agent definition (`src/agent.ts` or similar) plus `package.json`.
- `setupCommands` typically runs `npm ci` or `pnpm install`. Allowlist covers both.
- The adapter is a Node process; it imports the user module via `ts-node`/`tsx` (bundled in the image) and wraps the Mastra agent's message handling.

**Bootstrap:** on `message`, adapter calls `agent.stream(message)`; maps Mastra tool calls to Lenny's `tool_call` envelope; returns the final assistant message as `response`.

**Delegation:** Mastra agents may call `lenny/delegate_task`; the adapter registers it as a Mastra tool at agent initialization time.

---

### 26.10 `openai-assistants`

Runtime that adapts the OpenAI Assistants API shape to Lenny's session lifecycle. Existing Assistants API users can move to Lenny by registering their assistant ID as a `runtimeOptions` value.

**Repository:** `github.com/lennylabs/runtime-openai-assistants`
**Image:** `ghcr.io/lennylabs/runtime-openai-assistants:<release>`
**Conformance level:** Full
**Runtime `type`:** `agent`

**Key characteristics:**

- `isolationProfile: sandboxed`.
- `capabilities.interaction: multi_turn`; `injection: { supported: true, modes: [immediate] }`.
- `runtimeOptionsSchema` updates the existing §14 `openai-agents` schema: `assistantId` (required), `model` (optional override), `temperature`, `responseFormat`, `parallelToolCalls`. The schema name in §14 is renamed from `openai-agents` to `openai-assistants` for consistency — see §14 diff (task #3).
- `supportedProviders: [openai_direct, azure_openai]`.
- `credentialCapabilities.proxyDialect: [openai]`.

**Workspace conventions:**

- Assistants API manages its own file store; file uploads from the client are forwarded to OpenAI via the Files API using the pool's provider identity. Sealed Lenny artifacts at session end include any files produced by the assistant.
- No `/workspace/current/` usage — the runtime is stateless relative to local filesystem.

**Bootstrap:** on session start, adapter creates a `Thread` on OpenAI with the assistant ID from `runtimeOptions`; on each `message`, adapter appends to the thread and starts a `Run`; streams the run's deltas as Lenny `response` parts; maps Assistants tool calls (including `code_interpreter`, `file_search`) to Lenny `tool_call` envelopes.

**Note on `code_interpreter`.** OpenAI's hosted code interpreter runs outside Lenny's sandbox. Operators concerned about code execution isolation should disable `code_interpreter` in their assistant configuration on OpenAI's side. Lenny does not proxy or intercept code interpreter invocations; they execute inside OpenAI's infrastructure.

---

### 26.11 `crewai`

CrewAI multi-agent framework runtime with delegation wired to Lenny's `lenny/delegate_task`.

**Repository:** `github.com/lennylabs/runtime-crewai`
**Image:** `ghcr.io/lennylabs/runtime-crewai:<release>`
**Conformance level:** Full
**Runtime `type`:** `agent`

**Key characteristics:**

- `isolationProfile: sandboxed` (Python framework; executes arbitrary user code).
- `capabilities.interaction: multi_turn`; `injection: { supported: true, modes: [immediate, queued] }`.
- `runtimeOptionsSchema` at `https://schemas.lenny.dev/runtime-options/crewai/v1.json` — `crewModule` (required, Python dotted path to the `Crew` object), `process` (`sequential` | `hierarchical`), `verbose`.
- `supportedProviders: [anthropic_direct, openai_direct]`.
- `credentialCapabilities.proxyDialect: [anthropic, openai]`.
- `delegationPolicyRef` is **required** to be set on the Runtime — CrewAI's value proposition is multi-agent orchestration, and crews register `lenny/delegate_task` as the delegation mechanism for spawning specialist agents into Lenny-managed sub-sessions.

**Workspace conventions:**

- `/workspace/current/` contains the user's crew definition (`crew.py` or similar) plus `requirements.txt`.
- `setupCommands` runs `pip install -r requirements.txt`.

**Bootstrap:** adapter imports the module specified by `runtimeOptions.crewModule`; resolves the `Crew` object; on `message`, invokes `crew.kickoff(inputs={"message": ...})`; streams agent-internal thoughts and tool calls; maps any `Task.delegate` calls to `lenny/delegate_task` invocations that create Lenny sub-sessions under the parent's delegation budget.

**Delegation interaction.** When a CrewAI task delegates, the runtime translates the CrewAI delegation into a Lenny delegated task ([§8](08_recursive-delegation.md)). The child session runs on a pool selected by the `delegationPolicyRef` — typically a specialist runtime (e.g., `langgraph` for a research sub-agent, `claude-code` for a coding sub-agent). The parent crew waits for the child's result and resumes. Recursive delegation depth is bounded by the crew's `delegationLease.maxDepth`.

---

### 26.12 Adding a new reference runtime

New reference runtimes are proposed via a PR to `github.com/lennylabs/runtime-templates` with: (a) a scaffolded runtime from `lenny-ctl runtime init` ([§24.18](24_lenny-ctl-command-reference.md#2418-runtime-scaffolding)), (b) conformance test results at the claimed level, and (c) an appendix entry (this section) for maintainer review. Community-authored runtimes that live outside `github.com/lennylabs/` are documented by their authors in the runtime registry ([§21](21_planned-post-v1.md)) once that ships; they do not receive appendix entries in this spec.

