# Conversation with Claude

## Human (Mar 27, 2026, 8:59 AM):

The critique of the technical design, mentions a few products and open source projects. Including blockchain and a few others. Could you review and do a web search and see if there's anything out there that should also be considered. Also, can you check on lang lang chain deployment. Or line graph deployments. I don't know the exact name. I don't think it's explicitly mentioned. So I don't know if it was taken into account already or not.

---

## Claude:

Now I have a clear picture. The critique already mentions LangGraph — that's almost certainly what you're thinking of when you say "line graph deployments." Let me search to check if LangGraph's hosted/deployment offering is adequately accounted for, and look for any comparable platforms the critique may have missed.Good — I now have everything I need. Here's a thorough breakdown:

---

## Your "line graph deployment" question — LangGraph Cloud / LangSmith Deployment

Yes, you were thinking of **LangGraph Cloud**, and it **is** already mentioned in the critique — in the Evaluation Criteria and in the executive summary ("orchestration-rich but isolation-poor"). However, there are two important updates the critique missed:

1. As of October 2025, LangGraph Platform (formerly LangGraph Cloud) has been renamed to **LangSmith Deployment**. The critique's terminology is already outdated.

2. The platform has matured significantly. LangSmith Deployment now has native support for both MCP and the A2A agent-to-agent protocol, and its `RemoteGraph` feature lets any deployed agent call other deployed agents using the same interface used locally — a research agent can delegate to a search agent on a different deployment without needing to know whether it's calling something local or remote. This narrows the gap with Lenny's delegation model more than the critique acknowledges.

---

## Projects Not Mentioned in the Critique That Should Be

These are the most significant gaps:

### 1. **`kubernetes-sigs/agent-sandbox`** (Critical — Direct Overlap)

This is the biggest miss. Google launched **Agent Sandbox** as a formal subproject of Kubernetes SIG Apps at KubeCon Atlanta in November 2025, hosted at `kubernetes-sigs/agent-sandbox`. It is designed to standardize Kubernetes as a secure, scalable platform for agentic workloads.

The overlap with Lenny's design is striking. Its core APIs include: `Sandbox` (the core resource for running an isolated instance), `SandboxTemplate` (defines the secure blueprint including resource limits, base image, and security policies), and `SandboxClaim` (a transactional resource for requesting an execution environment, abstracting provisioning logic). It also ships with `WarmPools` — a dedicated CRD maintaining a pool of pre-warmed pods.

Agent Sandbox delivers sub-second latency for fully isolated agent workloads — up to a 90% improvement over cold starts — and also introduces Pod Snapshots, a GKE-exclusive feature enabling full checkpoint and restore of running pods.

**Why this matters for Lenny:** Lenny's `AgentPod`/`AgentPool`/`AgentSession` CRDs, warm pool controller, and claim-via-optimistic-locking pattern (Section 1.4 of the critique) are essentially the same primitive being standardized upstream. The design team needs to decide: build on top of `kubernetes-sigs/agent-sandbox`, contribute to it, or consciously diverge — and document why.

---

### 2. **E2B** (High — Market Leader in AI Sandboxing)

E2B built its platform specifically for AI agent workflows, with Firecracker microVM isolation, ~150ms boot times, sandbox pause/resume, and an open-source core with self-hosting options. It integrates natively with LangChain, OpenAI, Anthropic, and other LLM providers. It has the widest adoption in the market. The critique never mentions it, yet E2B is the platform most developers will reach for when evaluating alternatives to building something like Lenny.

---

### 3. **Fly.io Sprites** (Medium — Very Recent, Direct Competitor)

Sprites launched in January 2026 as Fly.io's purpose-built sandbox for AI coding agents. It runs on Firecracker microVMs with a 100GB persistent NVMe filesystem, checkpoint/restore in around 300ms, and automatic idle billing. This is extremely recent and directly in Lenny's space.

---

### 4. **Google A2A Protocol** (Medium — Affects Delegation Design)

The **Agent2Agent (A2A) protocol** was introduced by Google in April 2025 and is now housed by the Linux Foundation as an open-source project. It is designed for multi-agent systems, allowing interoperability between AI agents from varied providers built using different frameworks.

A2A and MCP are positioned as complementary: MCP standardizes agent-to-tool communication (what Lenny uses extensively), while A2A standardizes agent-to-agent communication — the layer Lenny handles via its own recursive delegation primitives.

Lenny's design is heavily MCP-centric for delegation. A2A is the emerging standard for exactly that layer. The critique should at least acknowledge A2A and explain why Lenny's MCP-based delegation model is preferred over or alongside it.

---

### 5. **Daytona** (Lower — Worth a Note)

Daytona pivoted in early 2025 from development environments to AI agent infrastructure, focusing on the fastest possible sandbox provisioning — sub-90ms cold starts — along with stateful sandboxes and, notably, desktop environments for Linux, Windows, and macOS for computer-use agents. The critique mentions Devin-style browser agents (Gap 3) without noting that Daytona already addresses this niche.

---

## Summary Table

| Project | Status in Critique | Why It Matters |
|---|---|---|
| LangGraph Cloud → **LangSmith Deployment** | Mentioned but outdated name | Now has A2A + MCP + RemoteGraph; gap with Lenny is narrower |
| **`kubernetes-sigs/agent-sandbox`** | **Not mentioned** | Near-identical design as a K8s upstream primitive — critical |
| **E2B** | **Not mentioned** | Market-leading AI sandbox; the primary thing Lenny's audience will compare against |
| **Fly.io Sprites** | **Not mentioned** | Jan 2026 direct competitor with Firecracker + checkpoint/restore |
| **Google A2A protocol** | **Not mentioned** | Emerging standard for agent-to-agent delegation — Lenny's MCP delegation model needs to address it |
| Daytona | **Not mentioned** | Computer-use agent workloads; relevant to Gap 3 in the critique |

The most urgent addition to the critique is `kubernetes-sigs/agent-sandbox` — it's upstream Kubernetes solving the same problem, and Lenny's team needs a clear position on it before v1.

---

## Human (Mar 27, 2026, 9:09 AM):

Tell me more about AI sandbox and Kubernetes 8 6 Sandbox. How do they work? Getting in get into the details, please. And, also, tell me what are the differences between them and Lenny

---

## Claude:

Here's a detailed breakdown of both projects and how they compare to Lenny.

---

## How `kubernetes-sigs/agent-sandbox` Works

### The Problem It Solves

Kubernetes excels at two workload models: stateless replicated apps (Deployments) and stable numbered sets of stateful pods (StatefulSets). AI agents, however, are isolated, stateful, singleton workloads — they need a persistent identity and a secure scratchpad for writing and executing often-untrusted code, and they're expected to be mostly idle except for brief bursts of activity. That lifecycle — idle most of the time, needing suspension and rapid resumption — doesn't fit either existing model.

Before agent-sandbox, the workaround was to combine a StatefulSet of size 1, a headless Service, and a PersistentVolumeClaim manually. Agent-sandbox replaces that kludge with a proper first-class primitive.

### The Core CRD: `Sandbox`

The `Sandbox` CRD is the core resource. It provides a declarative API for managing a single, stateful pod with stable identity and persistent storage, including a stable hostname and network identity, persistent storage that survives restarts, and lifecycle management covering creation, scheduled deletion, pausing, and resuming.

A minimal Sandbox looks like this in YAML:
```yaml
apiVersion: agents.x-k8s.io/v1alpha1
kind: Sandbox
metadata:
  name: my-sandbox
spec:
  podTemplate:
    spec:
      containers:
      - name: my-container
        image: <IMAGE>
```

The controller creates and manages the underlying Pod. Isolation is configured simply by setting `runtimeClassName: gvisor` or `runtimeClassName: kata` on the pod spec — the project supports both as isolation backends.

### The Extension Layer: Three More CRDs

These are optional but important for production use:

**`SandboxTemplate`** defines reusable templates for creating Sandboxes — the secure blueprint of an archetype, including resource limits, base image, and initial security policies. **`SandboxClaim`** allows users or higher-level frameworks like LangChain or ADK to request execution environments from a template, abstracting away the provisioning details. **`SandboxWarmPool`** manages a pool of pre-warmed Sandbox Pods that can be quickly allocated, reducing startup time.

### How the Claim Flow Works

The architecture diagram from the project explains the flow directly:

```
User creates SandboxClaim
  → references SandboxTemplate
  → ClaimController adopts a pre-warmed pod from SandboxWarmPool
  → ClaimController creates a Sandbox resource
  → Sandbox controller reconciles the Pod
  → Pod enters runtime environment
  → WarmPool replenishes itself
```

When a `SandboxClaim` resource is created, an available Pod is assigned from the warm pool to the Sandbox object and the claim is marked ready. The WarmPool then automatically replenishes itself to maintain the configured number of replicas. You can tell if a pod is claimed or idle by checking `ownerReferences` in the pod's metadata — if the kind is `Sandbox`, the pod is in use; if it's `SandboxWarmPool`, the pod is idle and waiting to be claimed.

### What the Roadmap Includes

Beyond the current release, the project roadmap includes deep hibernation (saving state to persistent storage), automatic resume on network reconnection, memory sharing across sandboxes on the same host, and a rich programmable API for agents to consume the Sandbox API directly.

---

## How Lenny's Equivalent Layer Works

Lenny independently designed almost the same low-level primitive. Its three CRDs map almost one-to-one:

| agent-sandbox CRD | Lenny CRD | Notes |
|---|---|---|
| `SandboxTemplate` | `AgentPool` | Declares runtime type, isolation profile, resource class, warm count range, scaling policy |
| `Sandbox` | `AgentPod` | Represents a managed agent pod with a pod state machine in status |
| `SandboxClaim` | `AgentSession` | Created by the gateway after a successful pod claim; links pod to session metadata |
| `SandboxWarmPool` | Built into `AgentPool` | min/maxWarm, demand-based scaling, time-of-day schedules |

The **claim mechanism** is where they differ mechanically. Agent-sandbox has a dedicated `ClaimController` that adopts warm pods into a `Sandbox`. Lenny's gateway replicas claim pods by issuing a **status subresource PATCH** with the current `resourceVersion` as an optimistic lock — exactly one gateway wins, others get a 409 and retry. The critique (Section 1.4) actually flags this as an architectural weakness, because it makes the `AgentPod` CRD serve double duty as both a state descriptor and a distributed coordination primitive.

---

## The Key Differences Between agent-sandbox and Lenny

This is the most important thing to understand: **they operate at entirely different layers.**

**`kubernetes-sigs/agent-sandbox` is a Kubernetes infrastructure primitive.** It answers one question: *"How do I manage a single isolated, stateful, warm-pooled pod as a first-class Kubernetes resource?"* That's it. It has no opinion about what runs inside the pod, how the pod communicates with the outside world, who can use it, or what it does.

**Lenny is a complete platform built on top of that primitive.** Everything agent-sandbox provides — the warm pool, the claim mechanism, the lifecycle management — is just Lenny's bottom layer. What Lenny adds above it is substantial:

**1. The Gateway and Protocol Layer.** Agent-sandbox gives you an isolated pod. Lenny gives you a gateway that mediates all access to that pod via MCP over Streamable HTTP, handles auth/OIDC, runs a policy engine on every request, proxies streaming sessions, and enforces rate limits and token budgets. The pod is never directly reachable.

**2. The Runtime Adapter Contract.** Agent-sandbox doesn't define what runs inside the pod at all. Lenny defines a detailed binary protocol (stdin/stdout JSON Lines) and gRPC RPC surface (`PrepareWorkspace`, `StartSession`, `Attach`, `Checkpoint`, `ExportPaths`, `AssignCredentials`, etc.) that any third-party agent binary must implement to be hosted on the platform. This is what makes Lenny runtime-agnostic in a structured, versioned way.

**3. Credential Leasing.** Agent-sandbox has no concept of credentials. Lenny's Token/Connector Service manages pools of LLM provider credentials (Anthropic, Bedrock, Vertex AI, Azure OpenAI), mints short-lived leases per session, handles rotation mid-session, and ensures pods never hold long-lived credentials.

**4. Recursive Delegation.** Agent-sandbox has no multi-agent concept. Lenny makes parent-to-child delegation a platform primitive — any pod can spin up child sessions via the gateway, with budget narrowing, lease inheritance, and a full delegation tree tracked in the session store.

**5. Session Durability and Recovery.** Agent-sandbox's roadmap mentions deep hibernation but it's not shipped yet. Lenny has a fully specified checkpoint/resume system — SIGSTOP the agent, snapshot the workspace to MinIO, write metadata to Postgres, all atomically, with resume on a replacement pod.

**6. MCP Fabric.** Agent-sandbox has no protocol layer at all. Lenny wraps everything in MCP semantics — elicitation chains with provenance, virtual child interfaces for delegation, tool approval flows.

---

## The Strategic Question for Lenny

The existence of `kubernetes-sigs/agent-sandbox` creates a concrete architectural decision that Lenny's design needs to address before v1. The project was published under `kubernetes-sigs` in November 2025 — meaning it's on the path to becoming an upstream Kubernetes standard, the same way Deployments and StatefulSets are standards.

Agent Sandbox is designed to standardize and evolve Kubernetes into the most secure and scalable platform for agentic workloads, with the standard designed to seamlessly support multiple isolation backends like gVisor and Kata Containers.

Lenny has three options and the design should explicitly choose one:

**Option A — Build on top of agent-sandbox.** Replace `AgentPool`/`AgentPod` with `SandboxTemplate`/`SandboxWarmPool`, and let the `SandboxClaim` mechanism handle pod claiming. Lenny's gateway, runtime adapter protocol, credential leasing, delegation, and MCP fabric remain entirely Lenny's own. Benefit: the bottom layer is maintained by the Kubernetes community, and Lenny gets pod snapshot features (from GKE) for free.

**Option B — Contribute to agent-sandbox.** The things Lenny designed independently — the warm pool CRD, the optimistic-lock claim pattern, the pod state machine — could be contributed upstream. Lenny becomes both a user and a shaper of the standard.

**Option C — Stay diverged, document why.** Lenny's claim mechanism via gateway status PATCH is intentional — it keeps the controller off the hot path. If agent-sandbox's `ClaimController` introduces a bottleneck or doesn't fit Lenny's multi-tenant session model, staying diverged is valid. But it needs to be an explicit, documented decision, not an accidental one.

Given that agent-sandbox published a Kubernetes blog post just one week ago (March 20, 2026), this is a very active and fast-moving project — and exactly the kind of thing the Lenny design team needs to have a position on before the OSS community starts asking.

---

## Human (Mar 27, 2026, 9:25 AM):

Thank you. How sorry. Can you, Can you elaborate or think about or provide suggestions on how to handle a to a and agent product call. I'm wondering on the one hand, I'm wondering if it's worth supporting them because I think their adoption isn't as far reaching as MCP and and so I'm not entirely sure the future of those protocols are. On the other hand, the the design Lenny's design at least should should be compatible with those protocols. And I don't know where the design today might have limitations or specific constraints that would not work if we wanted to implement a to a and agent protocol.

---

## Claude:

Good question — let me check the current state of A2A adoption and also clarify what "agent protocol" you might be referring to, since there are a couple candidates.Now let me check Lenny's delegation model carefully to identify the specific design constraints.Now I have everything I need. This is a meaty strategic question, so let me think through it carefully.

---

## A2A Adoption Reality Check First

Your instinct is correct. The two protocols are genuinely at different places. By February 2026, MCP has crossed 97 million monthly SDK downloads and has been adopted by every major AI provider — Anthropic, OpenAI, Google, Microsoft, Amazon. A2A's progress, by contrast, has been more of a slow burn.

That said, the governance situation has changed in a way that makes dismissing A2A risky. Both MCP and A2A are now under the Linux Foundation's Agentic AI Foundation (AAIF), launched in December 2025 with six co-founders: OpenAI, Anthropic, Google, Microsoft, AWS, and Block. Both protocols go through open RFC processes under neutral governance. That makes both protocols "safe for enterprise adoption" in a way that a Google-proprietary A2A never was. The survival odds are now considerably higher than they were six months ago.

The practical conclusion for Lenny: **don't implement A2A now, but make sure the design doesn't close the door.** The question is — does it currently close the door? And the answer is: in a few specific places, yes it does.

---

## What A2A Actually Requires (the Protocol Layer)

Before mapping to Lenny's design, it's worth being precise about what A2A defines. It's an HTTP-based protocol where:

- Every A2A agent **publishes a JSON "Agent Card"** at `/.well-known/agent.json` describing its capabilities, supported modalities, and authentication requirements.
- **Client agents send tasks** to remote agent HTTP endpoints using JSON-RPC 2.0 over SSE for streaming.
- **Tasks have a lifecycle**: `submitted → working → input-required → completed / failed / cancelled`.
- Agents communicate as **peers** — either side can initiate, respond, or push state updates.
- Security is handled at the HTTP level, with A2A supporting signed agent cards and multiple authentication schemes, enabling agents to trust each other without sharing internal memory or tools.

The key tension with Lenny is in that word **peers**. Lenny's core principle is the opposite — the gateway mediates everything, pods are internal workers, never directly exposed.

---

## Where Lenny's Current Design Has Specific Constraints

Going through the design systematically, there are four concrete friction points:

### Friction 1: No Agent Card Infrastructure (Missing Concept)

A2A requires every agent to be discoverable via a published Agent Card. Lenny has `RuntimeType` declarations with capability fields, but these are internal deployer-facing config — there's no mechanism to expose them as publicly-addressable, standards-compliant discovery endpoints.

This isn't a hard blocker — the gateway could serve Agent Cards on behalf of each runtime at something like `GET /a2a/{runtimeType}/.well-known/agent.json`. But nothing in the current design provides for this, and the capability schema in `RuntimeType` doesn't map cleanly to A2A's capability model. The `RuntimeType` declares things like `checkpoint: true`, `delegation: false`, `midSessionUpload: false` — internal plumbing. An A2A Agent Card declares things like "this agent accepts research tasks, returns structured reports, supports text and file modalities." These are different layers of description.

### Friction 2: The Gateway-Centric Model vs. A2A's Peer Model

Lenny's Non-Goals list explicitly includes: *"Direct pod-to-pod communication (all delegation goes through gateway)."* A2A's architecture is built on the assumption that any agent can call any other agent directly over HTTP. 

These aren't actually incompatible — but only if the **gateway** speaks A2A on its external face. The gateway already speaks MCP externally while pods speak the internal binary protocol. The same translation pattern could work for A2A: external A2A clients call the gateway, which maps the A2A task lifecycle to Lenny's session model internally. The gateway would be a session translator, not a pass-through.

The problem is this is not designed for. The gateway's external protocol layer is MCP — specifically, the Streamable HTTP / SSE implementation in the MCP Fabric subsystem. There is no abstraction over "external protocol" that would let A2A slot in alongside MCP. Adding A2A today would require forking the MCP Fabric logic rather than implementing a second adapter against a shared interface. This is exactly the kind of thing that should be a `RequestInterceptor`-style extension point (which the critique already called out in Section 1.3), but applied at the protocol level rather than the request level.

### Friction 3: Outbound A2A Calls Are Unhandled

This is the most significant gap. Consider the scenario where a Lenny-hosted agent wants to **call an external A2A agent** — for example, delegating a research subtask to an external agent run by a different vendor.

Lenny's current delegation model is closed-world: all delegation is internal (`delegate_task` calls the gateway, which allocates another Lenny pod). There is no concept of an agent delegating to something *outside* the cluster. The egress profiles (Section 13.2) technically allow outbound network access, so a pod *could* make raw HTTP calls to an external A2A endpoint — but this completely bypasses the delegation model: no lease narrowing, no audit trail, no budget enforcement, no parent-child tracking in the session store. It's a security and auditability hole.

The correct solution is a gateway-mediated **external delegation** primitive — an `external_delegate` tool (analogous to `delegate_task`) where the gateway acts as an A2A client proxy, enforces the same lease constraints on external calls as on internal ones, and records the delegation in the task tree. But this concept doesn't exist in the current design at all.

### Friction 4: Inbound A2A Authentication Doesn't Map Cleanly

Lenny's auth model is OIDC/OAuth 2.1 at the client-to-gateway boundary. A2A's auth model puts signed Agent Cards and OAuth-based authentication at the agent-to-agent HTTP layer. These aren't contradictory, but the gateway would need to validate A2A client credentials using a different trust model than its existing OIDC pipeline — specifically, it would need to validate Agent Cards (which include cryptographic identity assertions) rather than OIDC tokens.

The existing `AuthEvaluator` in the Policy Engine has no hook for this. Adding it later is feasible, but if the `AuthEvaluator` interface is already hardened around OIDC claims, adding Agent Card validation will require either forking the evaluator or adding a new one — which is fine if the extension point from the critique (1.3) exists, but messy if the Policy Engine evaluators are still hardcoded.

---

## The Recommended Approach: Three Design Decisions

### Decision 1: Define an "External Protocol Adapter" interface now, implement MCP as the first adapter

Right now the gateway's MCP implementation is monolithic — MCP semantics are baked directly into the Stream Proxy and MCP Fabric subsystems. The right abstraction is a thin `ExternalProtocolAdapter` interface that handles: session creation from an inbound protocol request, streaming events to an outbound protocol format, and task lifecycle mapping. MCP is the first (and currently only) implementation. A2A would be the second when needed.

This is a small but critical internal refactor that costs almost nothing now and prevents a major architectural seam later. The MCP Fabric subsystem already exists as an internal boundary — the work is making its external-facing contract an interface rather than a concrete implementation.

### Decision 2: Add `external_delegate` as a planned (not-yet-implemented) gateway primitive

Document in the design that outbound delegation to non-Lenny agents is a planned capability. The mechanism would be: a pod calls `external_delegate(protocol, endpoint, task, lease_slice)`, the gateway acts as the protocol client (initially A2A, potentially others), enforces the same lease narrowing as internal delegation, and records the external call in the task tree.

This only needs to be documented — not implemented — in v1. But the delegation lease data model needs to accommodate external delegation records now, otherwise retrofitting it breaks the schema.

### Decision 3: Add an optional `agentCard` block to RuntimeType configuration

The `RuntimeType` schema should have an optional `agentCard` section that deployers can fill in to describe the runtime in A2A-compatible capability terms. The gateway would serve this at a well-known URL when present. This costs essentially nothing — it's just a schema field and a GET endpoint — but it means that whenever the ecosystem does converge on A2A, Lenny-hosted runtimes can be discovered without any further design work.

---

## Summary

You're right to be cautious about implementing A2A now — the adoption gap with MCP is real, and building a full A2A implementation for uncertain return is not the right call for v1. But the design has three specific places where decisions made *now* determine whether A2A can be added *later* without a major refactor:

| Issue | Current State | What's Needed Before v1 |
|---|---|---|
| External protocol on gateway | MCP is hardcoded, no adapter interface | Define `ExternalProtocolAdapter` interface; MCP is the first impl |
| Outbound calls to external agents | No concept; egress only | Add `external_delegate` to delegation lease data model as planned primitive |
| Agent Card discovery | No concept | Add optional `agentCard` block to `RuntimeType` schema |
| Inbound auth for non-OIDC clients | OIDC only, hardcoded | Ensure `AuthEvaluator` is extensible for new auth schemes |

None of these require building A2A. They require factoring the right abstraction layers so that A2A (or whatever wins long-term) can be added without the kind of breaking changes the critique is already warning about in other areas.

---

## Human (Mar 27, 2026, 9:40 AM):

This recommendations all make sense. I think, probably should go with with all of them, with maybe just one minor clarification slash correction, or improvement, which is that I think that the different run times should be able to publish any metadata, including a to a cards but the the schema should not prevent them from publishing something else down the line, that could be used in in some other way.

---

## Claude:

That's a better design, and it's consistent with the broader extensibility philosophy the critique already argues for. Instead of a named `agentCard` field, the `RuntimeType` schema should have a generic metadata publication mechanism — something like a `publishedMetadata` map where each entry has a key, a content type, and a payload. The gateway would serve any registered entry at a well-known URL pattern like `GET /v1/runtimes/{name}/meta/{key}`, with the content type header set from the entry.

An A2A card would just be one entry:

```yaml
publishedMetadata:
  - key: agent-card
    contentType: application/json
    path: /.well-known/agent.json   # optional alias for A2A discovery convention
    value: |
      { "name": "...", "capabilities": [...] }
  - key: openapi-spec
    contentType: application/yaml
    value: |
      openapi: "3.1.0" ...
```

This is strictly better than a typed `agentCard` field for a few reasons. First, it doesn't encode a bet on A2A's longevity into the schema. Second, it naturally accommodates things that aren't protocol cards at all — an OpenAPI spec, a human-readable description document, a cost estimate manifest, or whatever the ecosystem invents next. Third, it keeps the gateway's serving logic completely protocol-agnostic: it just serves key-value pairs at URLs, with no understanding of what the payloads mean.

The one thing worth specifying in the design is that the gateway should treat this as **opaque pass-through** — it stores and serves the content without parsing or validating it. Validation is the runtime author's responsibility. This keeps the gateway from accumulating per-protocol logic and avoids the situation where a schema change in A2A requires a gateway update.

This would be a clean addition to the recommendation: replace Decision 3 with *"Add a generic `publishedMetadata` extension point to `RuntimeType` for arbitrary per-runtime discovery documents, served as opaque content by the gateway at well-known URLs."* The A2A card is just the motivating example, not a named concept in the schema.

---

## Human (Mar 27, 2026, 9:42 AM):

Great. Thanks. Changing topics. The critique mentions task parts, and I would like you to elaborate more on what those would look like and what purpose they serve and how they map to constructs that maybe others already provide like lang chain

---

## Claude:

Let me find exactly what the critique says about task parts, and then check what LangChain and others do with similar constructs.Good — I now have everything I need. This is actually a deeper question than it might seem at first, because "task parts" touches two different places in Lenny's design simultaneously. Let me explain clearly.

---

## What "Parts" Actually Means

The concept appears in both MCP and A2A as their shared solution to the same problem: **how do you represent the output of a task or tool call when that output isn't just a string?**

In MCP, a tool result is a `content` array of typed content blocks. The blocks available in the current spec are:

```json
[
  { "type": "text", "text": "Here is the analysis." },
  { "type": "image", "data": "<base64>", "mimeType": "image/png" },
  { "type": "resource", "resource": { "uri": "file:///report.md", "text": "..." } },
  { "type": "resource_link", "uri": "https://...", "name": "report.pdf" }
]
```

In A2A, a message has a `parts` array with three types: `TextPart`, `FilePart`, and `DataPart` — text, a file reference, and arbitrary structured JSON respectively.

LangChain carries this even further inside its message model: `AIMessage.content` can be a list of typed content blocks including `TextContentBlock`, `ImageContentBlock`, `VideoContentBlock`, `AudioContentBlock`, `FileContentBlock`, `ReasoningContentBlock`, and `NonStandardContentBlock`.

The common thread across all three is a **typed, heterogeneous, ordered array** of content rather than a single string or a flat bag of named fields.

---

## How Lenny Currently Does It — and the Problem

Lenny has two places where content flows out of a task:

**1. Live streaming during a session** — the `agent_text` event:
```json
{ "type": "agent_text", "text": "Refactored the auth module...", "final": false }
```
This is text only. There is no provision for a streaming image, a code block with metadata, or a structured data chunk to come through mid-session.

**2. Task completion** — `TaskResult.output`:
```json
{
  "text": "Refactored the auth module and added tests.",
  "structured": { "filesChanged": 5, "testsAdded": 12 },
  "artifactRefs": ["artifact://session_xyz/workspace.tar.gz"]
}
```

This is a flat three-field structure. It pre-commits to exactly three output channels — prose text, one JSON object, and a list of artifact paths — and each can be only one thing. You cannot have two structured data blobs with different schemas. You cannot have an inline image alongside the text. You cannot have a code block with its language tagged. You cannot distinguish between "here is the primary output file" and "here is a diagnostic log."

This becomes a real problem in three specific scenarios:

**Multi-modal agents.** A vision agent that analyzes images and produces an annotated image alongside its prose analysis has no way to return the image as output. It has to dump it to the workspace and put a path in `artifactRefs`, which means the parent agent gets a file path rather than the content itself.

**Delegation.** When a parent agent receives a child's `TaskResult`, it needs to pass that result back into the LLM as a tool result message. In the Anthropic API and LangChain, tool results are content arrays — exactly the parts model. With the current flat structure, the gateway has to make arbitrary choices: serialize `structured` as a JSON string inside a text block? Omit `artifactRefs` entirely? This lossy conversion is where bugs will appear.

**MCP compatibility.** The gateway exposes sessions as MCP tasks. MCP task results are content arrays. The mapping from Lenny's flat three fields to MCP content blocks must be hardcoded in the gateway — meaning any enrichment to the output model requires touching the gateway, not just the runtime adapter.

---

## What a Parts Model Would Look Like in Lenny

The fix is replacing the flat `output` object in `TaskResult` with a `parts` array, and doing the same for the streaming `agent_text` event. Here is what `TaskResult.output` would become:

```json
{
  "parts": [
    {
      "type": "text",
      "text": "Refactored the auth module and added tests."
    },
    {
      "type": "data",
      "mimeType": "application/json",
      "data": { "filesChanged": 5, "testsAdded": 12 }
    },
    {
      "type": "artifact_ref",
      "artifactId": "artifact://session_xyz/workspace.tar.gz",
      "name": "workspace.tar.gz",
      "role": "primary_output"
    },
    {
      "type": "artifact_ref",
      "artifactId": "artifact://session_xyz/test-report.html",
      "name": "test-report.html",
      "role": "diagnostic"
    }
  ]
}
```

And the streaming event would become `agent_output` rather than `agent_text`, where a simple text stream is just the degenerate case:

```json
{
  "type": "agent_output",
  "parts": [
    { "type": "text", "text": "Running the test suite...", "final": false }
  ]
}
```

A runtime that only ever produces text would just emit single-element parts arrays. The interface is backward-compatible in spirit — the minimum viable adapter hasn't changed in complexity, it just wraps its string in `[{type: "text", text: "..."}]`.

---

## How This Maps to the Ecosystem

The mapping becomes trivial rather than lossy:

**MCP tool results**: a Lenny `parts` array maps 1:1 to an MCP `content` array. `text` → `TextContent`. `data` → `TextContent` (serialized JSON, per MCP's current structured content convention). `artifact_ref` → `ResourceContent` or `ResourceLinkContent`. No gateway-level translation logic needed.

**A2A message parts**: `TextPart`, `FilePart`, `DataPart` all have direct counterparts. The gateway serving as an A2A endpoint would pass parts through without transformation.

**LangChain message content**: When a parent agent running inside a LangGraph workflow receives a child's `TaskResult`, it needs to inject it as a `ToolMessage` in the conversation history. `ToolMessage.content` is already a list of typed content blocks in the latest LangChain versions. A Lenny `parts` array maps directly, with no lossy serialization step.

**Anthropic API tool results**: Tool result `content` is `[{type: "text"}, {type: "image"}, ...]`. Same model.

---

## The Design Implication for Lenny

This is a v1-blocking decision for the same reason the critique raises about the adapter-binary protocol more generally: **once the first adapter ships producing the flat three-field `TaskResult`, client code will depend on that structure.** Changing to a parts model after adoption is a breaking change to every client that reads `output.text` or `output.structured`.

Concretely, two things need to change before v1:

**In the binary protocol** (`response` message from agent binary → adapter): replace `{"type": "response", "text": "...", "final": bool}` with a parts-aware structure. This is the community contract from Section 1.1 of the critique — it's the hardest thing to change after adoption.

**In `TaskResult.output`**: replace the flat `{text, structured, artifactRefs}` with `parts: []OutputPart`. The part types to define now are `text`, `data`, `artifact_ref`, and `image` as a start. Unknown `type` values should be passed through opaquely rather than rejected — the same forward-compatibility rule the critique recommends for the binary protocol's unknown message types.

The minimum viable runtime adapter that only produces text would emit:
```json
{ "type": "agent_output", "parts": [{"type": "text", "text": "Done.", "final": true}] }
```

That's essentially the same complexity as the current `agent_text` event. The richer parts are opt-in for runtimes that need them, but the platform supports them from day one without a schema migration.

---

## Human (Mar 27, 2026, 9:55 AM):

In your opinion, how bull how future proof Is this design I understand that it it would work today with line chain or a to a, etcetera, but do you foresee new capabilities that may require a more flexible interface. And if that's the case, I what should this interface look like?

---

## Claude:

This is worth thinking through carefully before committing to any schema, because the parts model as described is a big improvement over the current flat structure, but it still makes a bet that will likely age poorly. Let me give you my honest assessment.

---

## The Parts Model Is Good But Still Too Specific

The typed discriminated union — `{type: "text"}`, `{type: "data"}`, `{type: "image"}`, `{type: "artifact_ref"}` — solves today's problem well. But the design still fundamentally assumes you can enumerate the meaningful content types in advance. History says you can't. Look at how fast the content block model has evolved just in the past two years: Anthropic added `thinking` blocks for extended reasoning, LangChain now has `ReasoningContentBlock`, `CitationContentBlock`, `VideoContentBlock`, `AudioContentBlock`, and a `NonStandardContentBlock` escape hatch precisely because enumeration kept failing them. A2A added `DataPart` after shipping with just text and file. MCP keeps expanding its content types with each spec revision.

Every time a genuinely new capability appears, the protocol needs a schema update, every adapter needs updating, and older clients see unknown types they don't know how to handle. The `type` field being a string rather than a formal enum is a partial mitigation — unknown types can be passed through rather than rejected — but it doesn't help the client that needs to actually *use* the new type.

The deeper issue is that `type` is doing two different jobs simultaneously and conflating them will cause pain:

**Semantic type** — what is this *for*? (`text`, `code`, `reasoning_trace`, `citation`, `diff`, `screenshot`, `execution_result`)

**Encoding** — how are the bytes interpreted? (`text/plain`, `application/json`, `image/png`, `text/x-python`)

These are orthogonal. A `reasoning_trace` could be encoded as `text/plain` or `application/json`. A `code` block could be `text/x-python` or `text/typescript`. When you collapse them into a single `type` string, you get an explosion of combinations — `text`, `json_data`, `python_code`, `typescript_code` — or you get a vague `type: "code"` with a separate undocumented `language` field, which is what most systems end up with.

---

## What I Foresee Coming That Will Strain the Model

**Reasoning and thinking traces.** Extended reasoning is already shipping in production models, and the expectation that an agent can expose its reasoning to a parent — either for debugging or for the parent to use as context — is going to become standard. This needs a first-class part type. But reasoning traces have their own metadata: was this shown to the user? what budget was consumed? was it redacted? A flat `{type: "reasoning", text: "..."}` loses all of that.

**Computer use action traces.** Agents that drive browsers or GUIs produce a sequence of `(action, observation)` pairs: click here, screenshot, type this, screenshot. These need to be returnable as structured output, not just dumped as files. A screenshot mid-trace has a different semantic meaning than a final screenshot in a report.

**Citations and grounding.** Agents that do research need to return content that points back to its sources — not just "here's the answer" but "here's the answer, and this sentence came from artifact X at byte range Y." LangChain already has a `Citation` content block. This can't be represented as a flat text part with a separate artifact ref — the relationship between the text and the source is the point.

**Streaming at the part level, not just the character level.** Right now streaming means character-by-character text. But what if an agent is producing both a text report and a rendered chart? You want to stream the text as it's generated while the chart renders in the background, and then deliver the chart as a part when it's ready. This requires part-level streaming state — each part has its own identity and its own `streaming | complete | failed` status — not just a single `final: bool` flag on the whole event.

**Compound / nested outputs.** A code execution result is inherently compound: there's the code that ran, the stdout, the stderr, the exit code, and maybe a rendered output (a plot, a table). Right now you'd have to either flatten these into separate artifact refs or invent a convention inside `data`. Neither is right. The natural structure is a parent part with typed children.

**Part-level metadata that varies by use.** MCP already has `annotations` on content blocks — `audience`, `priority`, `lastModified`. Lenny will need its own: `role` (primary output vs diagnostic vs trace), `confidence`, `generated_at`, `model_used`. These don't belong in the type enum — they're metadata *about* a part.

---

## What the Right Interface Looks Like

The core insight is that instead of an enumerated type system, you want a **self-describing part** where the `type` is semantic and open-ended, the encoding is declared separately via MIME type, inline vs reference is a property rather than a type distinction, arbitrary metadata lives in an annotations map, and compound parts are expressed through nesting. Here's the structure:

```json
{
  "id": "part_abc123",
  "type": "text",
  "mimeType": "text/plain",
  "inline": "Refactored the auth module and added tests.",
  "annotations": {
    "role": "primary",
    "final": true
  }
}
```

```json
{
  "id": "part_def456",
  "type": "code",
  "mimeType": "text/x-python",
  "ref": "artifact://session_xyz/solution.py",
  "annotations": {
    "role": "primary_output",
    "lines": 142
  }
}
```

```json
{
  "id": "part_ghi789",
  "type": "execution_result",
  "annotations": { "exit_code": 0 },
  "parts": [
    {
      "id": "part_ghi789_stdout",
      "type": "text",
      "mimeType": "text/plain",
      "inline": "All 12 tests passed.",
      "annotations": { "role": "stdout" }
    },
    {
      "id": "part_ghi789_chart",
      "type": "image",
      "mimeType": "image/png",
      "ref": "artifact://session_xyz/coverage.png",
      "annotations": { "role": "visualization" }
    }
  ]
}
```

There are five properties that together make this future-proof:

**`type` is an open string, not an enum.** `"text"`, `"code"`, `"reasoning_trace"`, `"citation"`, `"screenshot"`, `"diff"`, `"computer_use_action"` — whatever the runtime needs to express. Unknown types are passed through opaquely. The gateway never needs to update to support a new semantic type; only clients that want to do something *specific* with that type need to understand it.

**`mimeType` handles encoding separately.** The gateway can validate, log, and route based on MIME type without understanding semantics. A runtime that produces `type: "reasoning_trace"` with `mimeType: "application/json"` is unambiguous to a client even if the client doesn't know what `reasoning_trace` means — it knows it can parse the bytes as JSON.

**`inline` vs `ref` as properties, not types.** A part either contains its bytes inline or points to a reference. Both are valid for any type. You don't need separate `text` and `text_ref` types. Large artifacts use `ref`; small content uses `inline`. The distinction is operational, not semantic.

**`annotations` as an open metadata map.** This is the extensibility escape hatch. `role`, `confidence`, `language`, `final`, `audience` — any metadata that doesn't belong in the type lives here. Importantly, the gateway can index and filter on annotations without understanding the part type, which enables capabilities like "give me only the `role: primary_output` parts from this task result" without a schema change.

**`parts` for nesting.** Compound outputs are first-class. An `execution_result` that contains code, stdout, stderr, and a chart is a single logical thing — it shouldn't be four sibling parts with a naming convention tying them together.

---

## The Streaming Addition

For the streaming case, this same structure gains a `status` field per part:

```json
{
  "id": "part_abc123",
  "type": "text",
  "mimeType": "text/plain",
  "status": "streaming",
  "inline": "Running the test suite..."
}
```

When the part completes:

```json
{
  "id": "part_abc123",
  "status": "complete",
  "inline": "Running the test suite...\nAll 12 tests passed."
}
```

A new part can start streaming before the previous one finishes — they're identified by `id`, not by position in a sequence. This is how you get concurrent part delivery, which is what you need for the "text streaming while image renders" case.

---

## The Bottom Line

The parts model as previously described is the right direction — it's dramatically better than the current flat structure. But committing to an enumerated list of types is the same mistake in a different place. The right version has `type` as a free string, `mimeType` as the encoding declaration, `annotations` as the metadata escape hatch, and `parts` for nesting.

The investment to get there from the typed discriminated union is small — it's mostly a matter of not defining a closed enum and adding `mimeType` and `annotations` to the part schema from the start. But it's the kind of decision that needs to be made before v1, because the binary protocol part schema is the community contract the critique keeps warning about: once adapters ship producing `{type: "text", text: "..."}` with no `mimeType`, no `annotations`, and no `id`, those fields can never be made required.

---

## Human (Mar 27, 2026, 10:09 AM):

Thanks. This makes sense. Can you, search online, and see if there's Anyone who's trying to build a protocol like this or maybe existing protocols that do something similar or maybe even better. If there's something out there, I think might make sense to reuse it instead of trying to reinvent the wheel.

---

## Claude:

The search results are telling a clear story. Let me pull one more targeted look at what MCP's current content model actually looks like in detail, and check one other candidate.Now I have a complete picture. The answer here is actually quite clean.

---

## The Closest Existing Thing Is MCP's Content Block Model — and It's Already Quite Good

The most recent MCP spec (2025-06-18, now the stable version) defines a content array with these block types, all with optional `annotations`:

- `text` — plain text with no mimeType (text is always text/plain implicitly)
- `image` — base64 data + `mimeType`
- `audio` — base64 data + `mimeType`
- `resource_link` — URI reference + name + optional `mimeType` + annotations
- `resource` (embedded) — inline resource with URI, `mimeType`, text or blob, annotations
- `structuredContent` — a separate top-level field for typed JSON, with an optional `outputSchema` for validation

All content types support optional annotations that provide metadata about audience, priority, and modification times — the same annotation format used by resources and prompts.

That is substantially the model we were converging on last discussion. It has `mimeType` on binary types, open metadata via `annotations`, and separates encoding from content. It's also under genuinely neutral governance now — MCP was donated to the Agentic AI Foundation (AAIF) in December 2025, co-founded by Anthropic, OpenAI, and Block, with AWS, Google, Microsoft, Cloudflare, and Bloomberg as supporting members. MCP has a formal Specification Enhancement Proposal (SEP) process for contributing changes, and governance now includes defined working groups with increasing autonomy.

So the honest answer to "should we reinvent this wheel?" is **no** — and there's a more specific reason than just "MCP is popular."

---

## The Strategic Reason to Adopt MCP's Content Model Directly

Lenny's gateway already speaks MCP externally. The adapter binary protocol is the internal protocol. Right now these two layers use different content representations: the gateway receives/sends MCP content blocks, while the binary protocol uses `agent_text` (a plain string) and `TaskResult.output` (a flat three-field struct). That means the gateway has to perform a translation layer between them — and as content types evolve, that translation layer accumulates per-type logic.

If Lenny adopts MCP's content block schema directly as the binary protocol's output format, the translation layer becomes a zero-cost pass-through. A `text` block from the binary is a `text` block in MCP. An `image` block is an `image` block. Nothing is lost, nothing needs mapping. The gateway becomes transparent to content type evolution.

This also means runtime adapter authors have only one content model to learn — the same MCP content block schema they'd encounter anywhere else in the ecosystem.

---

## Where MCP's Model Falls Short for Lenny's Needs

Being honest about the gaps:

**No stable part ID for streaming.** MCP doesn't have a mechanism to update a specific named part mid-stream. Content blocks are positional in the array — you can't say "here's an update to part `abc123`." Lenny's streaming model would need this for concurrent part delivery (text streaming while an image renders). This is a solvable extension, not a fundamental incompatibility.

**No nesting.** MCP has no `parts` within a part. The `execution_result` containing `{code, stdout, stderr, chart}` example from last time can't be expressed natively. You'd have to flatten it into four sibling blocks or put it in `structuredContent`. The structural relationship is lost.

**`structuredContent` is a separate field, not a content block.** This is an odd seam in the spec — typed JSON output lives in a different place than all other output. It can't be interleaved with text blocks, which matters if you want to stream text commentary alongside structured data. The MCP maintainers are likely aware of this; it has the feel of something added under backward-compat pressure.

**`text` has no explicit `mimeType`.** It's always implicitly `text/plain`. You can't have a `text/markdown` or `text/html` content block — those would need to go through the `resource` type with a URI, which is awkward for inline content.

---

## The Right Recommendation

Adopt MCP's content block schema as Lenny's internal output format. Where the current schema falls short, file SEPs through the AAIF process rather than forking it. The gaps above — part IDs for streaming, nesting, mimeType on text, structured content as a first-class block — are exactly the kind of well-motivated, narrowly scoped proposals that a working group process is designed for. Lenny would have standing to propose them because it would be both implementing and being affected by the standard.

Concretely this means:

**For the binary protocol:** replace `agent_text` and the final `response` message with an `agent_output` message whose payload is a `content` array of MCP content blocks. Minimum viable adapters emit `[{type: "text", text: "Done."}]`. Complex runtimes emit richer arrays.

**For `TaskResult.output`:** replace `{text, structured, artifactRefs}` with a `content` array of MCP content blocks. `text` → a `text` block. `structured` → a `structuredContent` field (matching MCP's current convention) or, when the SEP lands, a first-class `data` block. `artifactRefs` → `resource_link` blocks.

**For the two gaps that can't wait for a SEP:** add an optional `partId` string field and an optional `parts` array to the content block schema in the binary protocol specifically, as Lenny extensions. Prefix them `x-lenny-` to signal they're non-standard, the same way OpenAPI does extension fields. This keeps Lenny compatible with MCP clients that don't understand the extensions while giving the internal protocol the capability it needs. If the SEPs land, rename and remove the prefix.

This is the "don't invent the wheel, but fix the flat tire" position — you get the ecosystem alignment, the neutral governance, the developer familiarity, and you contribute back the fixes rather than maintaining a fork.

---

## Human (Mar 27, 2026, 10:19 AM):

Alright. I'll need to think a little bit more about this. I'm not convinced that this is the best way to go yet. It could be. But I'm just wondering how this works with a to a. We were just talking about Supporting a to a eventually and and whatever mechanism we use internally, at the very least, should support that as Wow. And And there's a trade off, but maybe it might make sense to have our adapter Convert between MCP and our internal protocol, so that run times don't have to worry about it, and and that could still be We would need to catch up with whatever is happening out there, but feel that we would need to catch up anyways, in if the standards evolve.

---

## Claude:

This is actually the more important architectural question, and your instinct is right to pause on it. Let me reason through the trade-off clearly.

---

## The Two Positions

**Position A (what I suggested):** Use MCP content blocks as the internal format. The gateway passes them through transparently. Runtimes speak MCP natively.

**Position B (what you're describing):** Define Lenny's own internal content model. The adapter layer translates between that model and whatever external protocol is in play — MCP today, A2A tomorrow, something else next year. Runtimes only learn Lenny's format.

---

## Why Position B Is Architecturally Sounder

The critique's entire modularity argument is built on one principle: **don't let external protocol decisions bleed through internal boundaries**. That's why the gateway has three internal subsystems instead of being one blob. That's why storage is role-based interfaces instead of raw Postgres calls. The same logic applies here.

If runtimes speak MCP content blocks natively, you've coupled every runtime author to MCP's evolution. When MCP adds a new block type, or changes the `annotations` schema, or the `structuredContent` convention shifts, every runtime adapter needs to track that. You've externalized the protocol churn problem into the community. The whole point of the adapter-binary contract is to insulate runtime authors from exactly that kind of upstream change.

More concretely: when A2A support lands, the gateway needs to translate between A2A `parts` (`TextPart`, `FilePart`, `DataPart`) and whatever the runtime produced. If runtimes produce MCP blocks, the gateway now has to know both MCP's block schema and A2A's part schema and translate between them. If runtimes produce Lenny's own internal format, the gateway has two clean translation jobs — internal-to-MCP and internal-to-A2A — and runtimes are completely isolated from both.

The translation boundary you're describing is actually exactly the `ExternalProtocolAdapter` interface the critique already called out in Section 1.3. That interface exists to translate between the external protocol's world and Lenny's internal world. Content format is just one more dimension of that translation, not a reason to abandon the interface.

---

## What Lenny's Internal Format Should Look Like

Given that it's internal and Lenny controls it fully, it can be designed for maximum expressiveness without being constrained by backward-compatibility commitments to the outside world. The model from the previous discussion — open `type` string, `mimeType` for encoding, `inline` vs `ref`, `annotations` map, optional `parts` for nesting, `id` for streaming — is the right shape. Call it `OutputPart` internally.

The key properties that make it future-proof *and* translatable:

```
OutputPart {
  id:          string          // stable identity for streaming updates
  type:        string          // open semantic label: "text", "code", "reasoning", etc.
  mimeType:    string          // encoding: "text/plain", "application/json", "image/png"
  inline:      any             // content inline (string or object depending on mimeType)
  ref:         string          // OR an artifact:// or https:// URI
  annotations: map[string]any  // open metadata
  parts:       []OutputPart    // optional nested children
  status:      enum            // streaming | complete | failed
}
```

This is richer than MCP's current model and richer than A2A's parts. It can represent both. That's the point — it's a superset that the translation layer can map downward.

---

## The Translation Layer

The `ExternalProtocolAdapter` handles translation in both directions:

**MCP translation:**
- `OutputPart{type:"text", mimeType:"text/plain", inline:"..."}` → MCP `{type:"text", text:"..."}`
- `OutputPart{type:"image", mimeType:"image/png", inline:<base64>}` → MCP `{type:"image", data:<base64>, mimeType:"image/png"}`
- `OutputPart{type:"code", mimeType:"text/python", ref:"artifact://..."}` → MCP `{type:"resource_link", uri:"artifact://...", mimeType:"text/python"}`
- Nested `parts` → flattened into sibling MCP blocks (losing nesting, acceptable for now)
- `annotations` → passed through into MCP's `annotations` field where keys overlap

**A2A translation:**
- `OutputPart{type:"text", inline:"..."}` → A2A `TextPart`
- `OutputPart{ref:"..."}` → A2A `FilePart`
- `OutputPart{type:"data", mimeType:"application/json", inline:{...}}` → A2A `DataPart`
- Anything with a `type` A2A doesn't know about → either drop or encode as a `DataPart` with a custom `kind` annotation

The critical observation is that **the lossy cases are in the downward translations, not in Lenny's internal representation**. If MCP gains a new block type, the MCP translator gains a new mapping rule — the internal format and all runtimes are untouched. If A2A gains a new part type, the A2A translator gains a new mapping rule — again, runtimes are untouched. You've correctly localized where the churn lands.

---

## The One Real Cost

Runtimes don't get to use their existing MCP tooling to produce output. A LangGraph-based runtime that already speaks MCP internally has to wrap its output in Lenny's `OutputPart` format. That's friction for runtime authors who are already MCP-native.

The mitigation is a thin SDK helper — a `from_mcp_content(blocks)` function that converts MCP content blocks into Lenny `OutputPart` arrays. Runtime authors who want to stay in MCP-world can produce MCP content and call the helper at the boundary. The binary protocol receives `OutputPart`. The runtime never knows that MCP and Lenny have different schemas; the helper is just a convenience shim. This is a small SDK addition, not an architectural compromise.

---

## Summary

Position B is right. The adapter translates; runtimes are insulated. The cost is real but small and solvable. The benefit — that Lenny's internal content model never needs to track external protocol evolution, and that adding A2A, or any future protocol, is purely an adapter concern — is the correct long-term trade.

The design note to add to the technical spec is short: *"The adapter-binary protocol uses Lenny's internal `OutputPart` format, not MCP content blocks directly. The gateway's `ExternalProtocolAdapter` implementations handle all translation between `OutputPart` and the external protocol's content model. Runtime adapter SDKs provide `from_mcp_content()` and `from_a2a_parts()` helpers for authors who want to produce output using familiar external formats."*

---

## Human (Mar 27, 2026, 10:32 AM):

Your SDK idea for run times, just, is making me think or rethink the adapter model Is there is there a way right now, the adapter work and the runtime communicate via socket And, I'm wondering if there's a place for sockets plus and separate layer that just base what basically, the adapter exposes an MCP enter to begin with or any other interface down the future. And the runtime, you just straight up sees that interface and communicates with it in the standard way. And then the adapter takes care of doing the conversion to internal protocol. That way we would avoid the need for developers to have to use another SDK to in their runtime. Basically be transparent to them. Now I don't know if this is really possible and if it wouldn't secure security holds. So could you explore and give me your opinion?

---

## Claude:

This is a really good instinct and worth thinking through carefully, because if it works, it's a substantially cleaner design. Let me reason through it fully.

---

## What You're Describing

Instead of the current model:

```
Gateway ←mTLS→ Adapter (sidecar) ←JSON Lines / Unix socket→ Agent Binary
                                    [Lenny's custom protocol]
```

You're proposing:

```
Gateway ←mTLS→ Adapter (sidecar) ←MCP / Unix socket→ Agent Binary
               [Lenny's internal      [standard protocol
                protocol toward        the runtime knows
                the gateway]           natively]
```

The adapter becomes a **local MCP server**. The agent binary is an MCP client. The adapter translates between MCP (what the runtime sees) and Lenny's internal protocol (what the gateway sees). Runtimes need zero Lenny knowledge — they just connect to an MCP server and work.

---

## Why This Is Architecturally Sound

It resolves the biggest tension in the current design cleanly. Right now, a runtime author has to learn two things: the agent SDK (LangChain, Claude Code, whatever) and Lenny's adapter binary protocol (the JSON Lines contract from Section 1.1 of the critique). The critique's single biggest concern is that this community contract is hard to evolve without breaking adapters. If the contract is just "speak MCP," that problem mostly goes away — MCP is versioned, widely documented, and something runtime authors already know.

More importantly, it makes the adapter's role unambiguous. Today the adapter is both a protocol translator *and* a lifecycle orchestrator *and* a binary protocol implementor. With this model the adapter is just a protocol translator. The agent binary is a pure MCP client. Responsibilities are clearly separated.

The direction of control flips slightly but not in a problematic way. Currently the gateway drives the session — it sends `StartSession`, `Attach`, `Checkpoint` to the adapter, which forwards to the binary. With a local MCP server, the binary connects to the adapter's MCP endpoint and starts interacting. The gateway still drives lifecycle by telling the adapter what to do; the adapter just exposes that state to the binary via the MCP interface rather than via explicit RPCs. The adapter is in control of what tools and resources it exposes, so the gateway's authority is fully preserved.

---

## The Lifecycle Problem — and Why It's Actually Solvable

The current adapter binary protocol has lifecycle RPCs that don't have MCP equivalents: `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `StartSession`, `Checkpoint`, `Resume`, `Terminate`, `AssignCredentials`. These are Lenny-internal plumbing. How do they map?

The key insight is that most of them **don't need to be visible to the runtime at all**. The adapter handles them transparently:

- `PrepareWorkspace` / `FinalizeWorkspace` / `RunSetup` — these happen before the MCP server starts accepting connections. The adapter completes them, then starts the local MCP server. The runtime binary connects and finds a workspace already materialized at `/workspace/current`. It never knows preparation happened.
- `Checkpoint` — the adapter triggers this by briefly pausing the MCP connection (or signaling the binary via a custom MCP tool call), snapshots the state it needs, then resumes. The binary might not even notice.
- `AssignCredentials` / `RotateCredentials` — credentials are either injected as environment variables before the binary starts, or (better) the adapter handles LLM calls through the reverse proxy model already in the design, so the binary never sees credentials at all.
- `Terminate` — the adapter closes the MCP server connection. The binary sees a server disconnect, which is standard MCP client behavior.

The only lifecycle operations that genuinely require the binary's cooperation are `Checkpoint` (needs a consistent snapshot point), `Resume` (needs the binary to restore state), and `Interrupt` (needs to stop current work). These could be exposed as optional MCP tools in a `lenny` namespace — `lenny/checkpoint`, `lenny/interrupt` — that the adapter calls when needed. Runtime authors who want to support checkpointing implement these tools. Those who don't just get best-effort snapshotting from the adapter side.

This mirrors exactly how MCP's elicitation works — it's an optional capability. Runtime authors declare in their `RuntimeType` whether they implement `lenny/checkpoint`. The adapter falls back gracefully when they don't.

---

## The Security Question

This is where you need to think carefully, but I don't think it introduces new fundamental risks — it mostly just moves where existing risks live.

**What the agent binary can see:** Only what the adapter exposes through the MCP server. The adapter controls this completely. The binary sees: the tools available to the agent (workspace access, delegation, external connectors), an output channel, and optionally the lifecycle hooks above. It does not see: gateway credentials, mTLS certificates, other sessions, internal Lenny state, or anything about other tenants. This is the same isolation boundary as today — the custom binary protocol also never exposed those things.

**What if the binary is malicious?** The threat model doesn't change. A malicious binary in the current model can abuse the JSON Lines protocol within its sandbox. A malicious binary in the MCP model can abuse the local MCP server within its sandbox. In both cases the sandbox (gVisor/Kata), the network policy, and the adapter's input validation are the actual security boundary. The protocol is not the security boundary — the pod isolation is.

**New surface area:** The local MCP server is a slightly larger attack surface than the current JSON Lines protocol, because MCP is a richer protocol. Specifically, there are things to be careful about:

One is that MCP has a `sampling` capability — the ability for an MCP server to request completions from the client's LLM. If the adapter's local MCP server advertised sampling, a malicious binary could try to use it to make unauthorized LLM calls. The fix is simple: the adapter never advertises the `sampling` capability to the local server. The adapter is an MCP server, not an MCP client toward the binary — sampling flows the other direction.

Another is that MCP's resource subscription model could theoretically be used by the binary to poll for information it shouldn't have. Again, the adapter controls what resources it exposes. A well-implemented adapter only exposes workspace resources scoped to that session.

The third and most real concern is that the Unix socket file has filesystem permissions. If `shareProcessNamespace: false` (which Lenny already requires), the socket is only accessible to processes in the same pod. But you want to make sure the socket has tight permissions — mode 0600, owned by the agent binary's UID — so a compromised sidecar process can't snoop on it. This is operational hygiene, not a fundamental problem.

---

## The Genuine Trade-offs

**Implementation complexity of the adapter goes up.** Instead of a relatively simple JSON Lines protocol handler, the adapter needs to implement a proper local MCP server. That's a meaningful amount of code — MCP's TypeScript and Python SDKs are mature enough that "implement an MCP server" is not a huge lift, but it's more than today's adapter. On the other hand, you eliminate the binary protocol spec entirely, which removes the community contract problem the critique keeps raising. The net complexity might actually be lower.

**Transport choice needs a decision.** MCP normally runs over stdio or HTTP+SSE. For a local intra-pod connection, stdio (piped to the binary's stdin/stdout) is the simplest — no socket management at all, and it's what most MCP tooling already expects. The adapter spawns the agent binary with stdio piped to the local MCP server. This is actually cleaner than a Unix socket and more aligned with how most MCP clients work today.

**Streaming output format.** MCP tool results are request-response, not streaming events. Lenny's session model is streaming — the agent produces output tokens as they arrive. This is handled in MCP through the sampling primitive (the LLM streams back via the client), but the binary-to-adapter communication for output text doesn't naturally fit MCP's tool call model. This is solvable by using MCP's `logging` or `notification` capabilities for streaming output, or by treating output as a special "produce output" tool call. Not a blocker, but it needs explicit design.

---

## My Opinion

Do it, but with one clarification about what "MCP interface" means here. The local interface the adapter exposes to the binary isn't the same as Lenny's external MCP interface — it's a purpose-built MCP server tailored to what a hosted agent runtime needs. It exposes workspace tools, delegation tools, output channels, and optionally lifecycle hooks. It deliberately does not expose gateway internals, credentials, or multi-tenant state.

You could think of it as a "Local Agent MCP Server" — a well-defined profile of MCP that the adapter implements, documented as the runtime integration point. Runtime authors who write LangGraph agents, Claude Code extensions, or custom binaries all just point their MCP client at this local server and write standard agents. The adapter takes care of everything else.

The one design question to resolve before committing is the streaming output path, since MCP wasn't designed for continuous streaming text output in the way Lenny's session model requires. But that's a solvable protocol design problem, not a reason to abandon the approach. The right answer is probably a dedicated `lenny/output` MCP tool that the binary calls with parts arrays and a streaming flag, which maps cleanly to the `OutputPart` model from earlier.

If that works — and I think it does — you've eliminated the custom binary protocol entirely, made every standard MCP-capable agent runtime a zero-modification Lenny runtime, and localized all protocol translation in the adapter where it belongs.

---

## Human (Mar 27, 2026, 11:54 AM):

Does it really make sense to use MCP for everything that the, current protocol handles. Because I think that things like you know, like, starting the runtime or, again, checkpoints those seem to be things that don't belong and MCP and still require well, Yeah. So I don't know. I'm I'm just I'm just a little hesitant on that point. Feel that no matter what, runtime will have to abide by certain standards that are enforced by Lenny and and maybe that's what it is. And and so I think for anything that's legitimately MCP communication, across between Asians and tools or a to a between agents and agents or even MCP between agents To me, that makes sense to to have, a native support as, in the adapter in the form of MCP servers or or what have you, and that's why I was proposing this. But then there are things that maybe just don't belong in there that are more like Lifecycle. As you said, lifecycle management that's part of Lenny that would need to live outside of it. And so I I like I like your idea of Making things making the life cycle cost support optional to the extent possible. And then, run times can opt in to implement specific things like checkpointing. So I like that. And I wonder if If all of it can be made optional, or there are things that are must haves. And, again, like, Whether some of that life cycle should live outside of MCP.

---

## Claude:

Your instinct is correct, and it leads to a cleaner design than trying to unify everything. Let me work through the categorization carefully.

---

## The Right Split

There are genuinely three different kinds of things the current adapter protocol handles, and they have different natures:

**Category 1: Things that happen before the runtime exists.** Workspace preparation, setup commands, credential assignment, and workspace finalization all happen before the agent binary starts. The runtime never sees these at all. They're not a protocol between the adapter and the runtime — they're the adapter doing work on behalf of the gateway before it starts the runtime. These live entirely outside the runtime-facing interface.

**Category 2: Things the runtime does as an agent.** Producing output, calling tools, delegating to other agents, making LLM calls, handling elicitation. This is the agent's actual work. This is where MCP belongs, exactly as you described. The adapter exposes a local MCP server. The runtime connects and does agent things. Standard protocol, full ecosystem compatibility.

**Category 3: Things the platform needs the runtime to cooperate with mid-session.** Checkpointing, interrupt acknowledgment, credential rotation. These are Lenny-specific lifecycle events that happen after the runtime is running and require the runtime to do something specific. These genuinely don't belong in MCP — they're not agent-to-tool communication, they're platform-to-runtime coordination. They need a separate, small, explicit interface.

---

## What Must-Have vs Optional Looks Like

Once you split it this way, the must-have / optional question becomes much sharper.

**Must-have from every runtime — none.** The adapter can handle all of category 1 invisibly, run the binary, proxy its MCP traffic, and terminate it when done. A runtime that implements nothing beyond being an MCP client can be hosted on Lenny. It just won't get the platform's richer capabilities.

**Optional lifecycle hooks the runtime can implement for better platform integration:**

The cleanest way to think about this is as a small, separate capability negotiation at startup — much like MCP's own capability handshake, but for Lenny lifecycle features. The adapter signals to the runtime what lifecycle hooks are available. The runtime declares which ones it supports. Everything undeclared falls back to the adapter's best-effort behavior.

| Hook | What the runtime does | Fallback if absent |
|---|---|---|
| `checkpoint_ready` | Runtime flushes in-memory state, signals a clean point | Adapter does filesystem snapshot at arbitrary time, may lose in-memory state |
| `checkpoint_restore` | Runtime restores from a checkpoint bundle | Session restarts fresh from workspace snapshot — less continuity |
| `interrupt` | Runtime stops current work cleanly and acknowledges | Adapter sends SIGSTOP, may leave work mid-sentence |
| `credentials_rotated` | Runtime acknowledges and switches to new credentials | Adapter injects via env/proxy, runtime may not pick up mid-session |

The fallbacks are acceptable for simple runtimes. The hooks are valuable for runtimes that need high fidelity — coding agents that want true session continuity, long-running research agents that need clean interrupt points.

---

## What the Separate Lifecycle Interface Looks Like

Since it lives outside MCP, it needs its own transport. The cleanest answer is a second small channel alongside the MCP connection, rather than mixing lifecycle into the MCP stream.

The simplest option: a second Unix socket (or a second stdio stream pair if using stdio for MCP), on which the adapter and runtime exchange a minimal message set. The protocol is tiny — far simpler than the current full adapter binary protocol:

```
Adapter → Runtime:
  {type: "lifecycle_capabilities"}   // what hooks are available
  {type: "checkpoint_request", id: "ckpt_123", deadline_ms: 5000}
  {type: "interrupt_request", id: "int_456"}
  {type: "credentials_rotated", lease_id: "cl_789"}
  {type: "terminate", reason: "session_complete"}

Runtime → Adapter:
  {type: "lifecycle_support", hooks: ["checkpoint_ready", "interrupt"]}
  {type: "checkpoint_ready", id: "ckpt_123"}   // "safe to snapshot now"
  {type: "interrupt_acknowledged", id: "int_456"}
  {type: "credentials_acknowledged", lease_id: "cl_789"}
```

That's essentially the entire protocol. Six message types inbound, five outbound. It's versioned by the capability negotiation at the top. Unknown messages are silently ignored on both sides. Adding a new lifecycle hook in a future version is a backward-compatible extension.

The runtime is not required to open this channel at all. If it doesn't, the adapter operates in fallback-only mode and the runtime works fine — it just doesn't expose the richer platform capabilities.

---

## The Full Picture

Putting it together, a runtime author faces three layers, in increasing order of commitment:

**Layer 0 — Zero integration.** Run any MCP-capable agent binary. Point it at the adapter's local MCP server. It works. No Lenny knowledge required. Fallback lifecycle only — no guaranteed clean checkpoints, no cooperative interrupt.

**Layer 1 — MCP integration.** Use the adapter's local MCP server fully — workspace tools, delegation, output parts, elicitation. Standard MCP. The runtime produces richer output and uses platform capabilities, still without any Lenny-specific code.

**Layer 2 — Lifecycle integration.** Open the lifecycle channel alongside MCP, declare supported hooks, implement them. This is where the runtime gets true session continuity, clean interrupt points, mid-session credential rotation. Small, explicit, optional.

This is a much better story for the OSS community than the current design. The current design has a hard binary protocol that every runtime must implement in full. This design has a zero-requirement baseline that degrades gracefully, with opt-in enrichment at each layer. Most developers start at Layer 0 or 1. Only platforms that care deeply about operational resilience implement Layer 2.

It also answers the critique's concern about the adapter binary protocol being a community contract that's hard to change. The MCP interface evolves with the MCP community. The lifecycle channel is small enough to be stable for years. The version negotiation at startup means the two can evolve independently.

The main design question remaining is whether the lifecycle channel needs its own transport or can be done more elegantly — for instance, MCP does have a notification and logging mechanism that could carry lifecycle signals without a second socket. But I'd lean toward keeping it separate precisely because you said it yourself: it doesn't belong in MCP. Mixing it in via a side channel inside the MCP transport reintroduces the coupling you're trying to avoid. Two clean channels, two clear purposes.

---

## Human (Mar 27, 2026, 12:05 PM):

Cool. I like it. Question follow-up question. Does this, does the we're talking about MCP server and the adapter, we're talking about one MCP server or multiple MCP servers? And because I'm I'm the way I'm thinking about it is a client could connect to multiple MCP servers. That's how it works today. And so is what you're proposing kinda like a like, bundling all of that into one MCP server, all of the different tools that the, client may want to use. Or or are we truly talking about multiple virtual MCP servers? And then my second question is how what I assume And just I just wanna confirm. I'm I'm pretty sure that that's we're talking about here, but I want to I want to confirm that the MCP server in the adapter replaces completely the virtual MCP server that the gateway implements in the current design. Is is that correct? And I understand that the gateway would need to still maintain the life cycle and have session management and all that stuff. It's just not gonna be MCP. Correct.

---

## Claude:

Two good questions, and they're connected in an important way.

---

## Question 1: One Server or Multiple

One MCP server, but it's an aggregating one. Here's why.

MCP clients *can* connect to multiple servers, and that's a valid pattern for a human using Claude Desktop who wants to pull in a GitHub server, a filesystem server, and a Postgres server simultaneously. But for a runtime binary, discovering and connecting to multiple servers adds coordination overhead that buys nothing. The runtime doesn't care whether "list workspace files" and "delegate task" come from the same server or different ones — it just wants the tools available.

The adapter presents a single local MCP server. Behind it, the adapter aggregates tools from several logical sources:

- **Workspace tools** — file read/write, path listing. These are implemented directly in the adapter; no gateway involvement needed.
- **Delegation tools** — `delegate_task`, `await_children`, `cancel_child`. The adapter implements the interface; the gateway executes the actual pod allocation when called.
- **Connector tools** — GitHub, Jira, the external MCP tools. The adapter proxies these through to the gateway, which holds the OAuth tokens and enforces scope. The runtime sees a flat `github/create_pr` tool; the adapter forwards the call upstream.
- **Output tools** — the `lenny/output` mechanism for producing structured `OutputPart` arrays back through the session.

From the runtime's perspective it's one server with all the tools it needs. From the adapter's perspective it's a local aggregator with different fulfillment paths per tool. This is a well-understood MCP pattern — aggregating proxies already exist in the ecosystem.

---

## Question 2: Does This Replace the Gateway's External MCP?

No, and this is the important clarification. There are **two completely separate MCP interfaces** in this architecture, serving two completely different consumers:

```
Client / MCP Host
      │
      │  MCP (external, over the internet)
      ▼
Gateway  ←── this is the client-facing MCP interface
      │
      │  Lenny internal protocol (mTLS, lifecycle management)
      ▼
Adapter (sidecar)
      │
      │  MCP (local, inside the pod)
      ▼
Agent Binary ←── this is the runtime-facing MCP interface
```

The gateway's external MCP interface is for **clients** — humans, orchestrators, or other systems creating sessions, sending prompts, and receiving output. It stays exactly as designed: the MCP Fabric, the virtual child interfaces for delegation, elicitation chains, the whole thing. None of that changes.

The adapter's local MCP server is for **the runtime** — the agent binary inside the pod accessing tools, workspace, and delegation. This is new, and it's purely intra-pod. It never touches the client.

So your intuition is exactly right. The gateway remains the MCP endpoint for the outside world. It still owns session management, lifecycle state, policy enforcement, credential brokering. None of that moves. The only thing that changes is what the *inside* of the pod looks like — instead of the runtime speaking a custom binary protocol to the adapter, it speaks MCP to a local server. The gateway is completely unaware of this internal change; it still talks to the adapter via the existing mTLS control protocol.

This also means the two interfaces can evolve independently. The gateway's external MCP tracks the public MCP spec because clients depend on it and ecosystem compatibility matters. The adapter's local MCP can be a more opinionated profile — it only needs to be compatible with runtimes, not with arbitrary MCP clients, so it can add Lenny-specific tools (`lenny/output`, the lifecycle hooks) without those being visible or relevant to external clients at all.

The only connection between the two MCP surfaces is the adapter, which translates between them — taking output that arrived via the local MCP server and forwarding it through the gateway's internal protocol to be streamed to the client. That translation is exactly the `ExternalProtocolAdapter` interface the critique called out, now doing double duty: it translates both for external protocols (MCP, A2A) and for the internal runtime interface.

---

## Human (Mar 27, 2026, 12:13 PM):

Alright. And are you sure that it is okay to aggregate multiple different MCP servers into 1 in a transparent way. Is there absolutely nothing that gets lost? And is there is that always going to be the case? As far as we know? Is it something that the MCP standard, explicitly calls out Because if it's not, then I worry that maybe the future, there might be new features, included in MCP that truly could not be aggregated in a in a lossless way And if that's the case, then I think it would make sense to still maintain different MCP servers at the adapter level, And by the way, this this model is not restricted to MC MCP is what we're thinking of building right now as a as a first step, but the same should be true for a to a. If the agent wants to connect to discover other agents and then connect to them. It makes sense to have well, it we must have support for multiple different communications going on. So I I just wanna triple check that this assumption one single server per protocol is going in the adapter is actually going to hold up moving forward.

---

## Claude:

Good instinct to push on this. The honest answer is: **aggregation works fine today but is not guaranteed to be lossless forever, and the MCP spec's own architecture model explicitly discourages it.** Let me be precise about what already gets lost and what could break in the future.

---

## What the MCP Spec Actually Says About This

MCP's architecture explicitly defines that a host application creates and manages multiple clients, with each client having a 1:1 relationship with a particular server. Aggregation proxies exist and are widely used, but they're a workaround, not a spec-endorsed pattern. The spec assumes each server is a separate session with independently negotiated capabilities.

---

## What Actually Gets Lost in Aggregation Today

**Capability negotiation is per-server, not per-aggregate.** The capabilities property is negotiated independently between each client-server pair — if a feature key is missing, the other party must assume it's unavailable. Capabilities determine which protocol features are available during a session. When you aggregate Server A (which supports resource subscriptions) and Server B (which doesn't) into one surface, you have to either advertise subscriptions for the whole aggregate and fail silently when B's resources are targeted, or suppress subscriptions entirely and lose A's real capability. Every aggregator in the ecosystem makes a different choice here. None of them is fully transparent.

**Sampling is bidirectional and breaks the aggregation model.** Sampling is a client-side feature — it's how servers request LLM completions from the client's own model, maintaining user control and security. The server requests; the client (which has model access) responds. In an aggregated proxy, if an upstream server issues a `sampling/createMessage` request, that request has to travel through the aggregator back to the actual client. The aggregator is now transparently proxying a server-initiated request backwards through what the client thinks is a single connection. This works mechanically, but the client's sampling policy (which model to use, what to allow) can't be negotiated separately per upstream server — it applies as a blanket policy. If MCP ever adds per-server sampling constraints or fine-grained sampling policies per connection, aggregation breaks that.

**Tool name collisions require namespacing, which changes the tool names.** LiteLLM, for example, namespaces multiple MCP servers by prefixing each tool name with its server name. The runtime sees `github/create_pr` instead of `create_pr`. This is fine in practice, but it means the tool names the runtime works with are aggregator-specific, not the canonical names the original server defined. Any future MCP feature that depends on stable canonical tool identifiers — cross-server tool references, tool versioning, security policies tied to tool names — would break.

**Resource URIs may collide or require remapping.** Two servers might both expose `file:///workspace/main.py`. The aggregator has to remap URIs, which means the URIs the runtime stores and uses are synthetic, not what the servers actually expose.

---

## What Could Break in the Future

Looking at the MCP roadmap, the roadmap calls out structured audit trails, enterprise-managed auth with SSO-integrated flows, and gateway and proxy patterns including authorization propagation and session affinity as explicit upcoming work. The phrase "session affinity" is significant — if the spec formalizes that certain operations must happen on the same session that initiated them, aggregation proxies that silently route to different upstream sessions will violate that invariant.

More broadly, any feature that introduces **per-server identity** — server-specific auth tokens, per-server encryption keys, server-level access control to specific capabilities — will be lossy through aggregation because the runtime sees one server, not N.

---

## The A2A Dimension Makes This Worse

A2A compounds the problem because its model is fundamentally peer-to-peer, not aggregatable. In A2A, an agent discovers other agents via their Agent Cards and connects directly to their HTTP endpoints. There's no aggregation concept in A2A at all — an agent interacts with `agent-a.example.com` and `agent-b.example.com` as distinct peers. If the adapter tried to present a single A2A endpoint that aggregated multiple remote agents, it would have to invent an aggregation layer that A2A's spec doesn't define and the ecosystem doesn't support.

---

## What This Means for the Design

You're right that the single-server assumption is fragile. The correct model is:

The adapter presents **multiple logical servers** to the runtime — one per protocol and one per distinct capability domain. The runtime (as an MCP client) can connect to multiple local servers, which is exactly what the spec intends. Concretely:

```
Pod interior:

Runtime (MCP client)
  ├── connects to: adapter MCP server — workspace tools
  ├── connects to: adapter MCP server — delegation tools  
  ├── connects to: adapter MCP server — connector proxies (GitHub, Jira, etc.)
  └── connects to: adapter A2A endpoint — peer agent discovery
```

Each server has its own independent capability negotiation. Sampling flows correctly per connection. There are no tool name collisions within a server because each server has a focused domain. The adapter manages all of them and handles routing to the gateway internally.

For runtimes that don't want to think about this, the adapter SDK provides a convenience config that pre-wires all the connections. Runtime authors who want full control connect to each server explicitly.

This is actually the model the MCP spec intended — multiple focused servers per host. Aggregation is a convenience pattern for certain use cases, but it's not the right default for a platform that needs precise capability control and protocol fidelity. And it naturally extends to A2A: the A2A endpoint is just another connection point alongside the MCP servers, not aggregated into them.

The lifecycle channel from the previous discussion remains entirely separate from all of this, which is exactly right. It's not MCP, it's not A2A, it's Lenny's own small operational wire.

---

## Human (Mar 27, 2026, 12:23 PM):

Alright. That makes sense. So how would this be implemented? And I would like to tie it to maybe, a more general question, which is how are MCP servers slash tools defined in Lenny today. How, would agents expose their a to a cards such that they are either available publicly to the external clients or just only internally. For other agents in the in the Lany, ecosystem. And then how all this ties to external tools that live outside of Lanny's cluster as well as other agents that live outside of Lenny's cluster.

---

## Claude:

Let me read the relevant sections of the technical design first before answering.Now I have the full picture. This is genuinely a multi-part architectural question, so let me answer it layer by layer, being precise about what exists today, what's missing, and how everything fits together.

---

## How MCP Tools Are Defined in Lenny Today

There are currently three distinct tool surfaces in the design, and they're not consistently treated:

**The gateway's external MCP interface** is the 17 platform tools in Section 15.2 — `create_session`, `attach_session`, `delegate_task`, and so on. These are hardcoded in the gateway. They're Lenny lifecycle tools, not agent tools. Clients (humans, orchestrators) use these to manage sessions.

**Injected session tools** — the design mentions that `delegate_task` is "a gateway-backed tool injected into its session." So the gateway dynamically injects tools into what the running runtime can call. But this injection mechanism is never explicitly defined as a schema or registry. It's implicit: the gateway always injects `delegate_task`, `await_children`, `cancel_child`, `request_lease_extension`. There's no mechanism for a deployer to say "also inject tool X."

**Connectors** — external MCP servers like GitHub and Jira are registered as "connectors" and their tools become available to pods through the gateway's proxy. But the connector registration schema is never shown in the design. You see `allowedConnectors: ["github", "jira"]` in delegation leases, and the Token/Connector Service holds OAuth tokens keyed by `connector_id`, but how a deployer actually registers a connector — what its config looks like, how it maps to an external MCP server URL — is a gap.

So the honest answer is: tools in Lenny today are partially implicit (injected platform tools), partially unspecified (connector registration), and the runtime has no direct visibility into any of them through a standard interface.

---

## How It Should All Fit Together

Given everything we've discussed — multiple focused adapter MCP servers, the lifecycle channel, the `publishedMetadata` mechanism, and future A2A support — here's the full unified model.

### The Connector Registry

The first gap to close is making connectors a first-class declared resource. A connector definition would look like:

```yaml
connectors:
  - id: github
    displayName: GitHub
    mcpServerUrl: https://mcp.github.com
    transport: streamable_http
    auth:
      type: oauth2
      authorizationEndpoint: https://github.com/login/oauth/authorize
      tokenEndpoint: https://github.com/login/oauth/access_token
      clientId: "..."
      clientSecretRef: lenny-system/github-client-secret
      scopes: [repo, read:org]
      expectedDomain: github.com   # for URL-mode elicitation validation
    visibility: tenant             # who can use this connector
    allowedPools: ["*"]            # which pools can use it

  - id: internal-jira
    displayName: Acme Jira
    mcpServerUrl: https://jira.internal.acme.com/mcp
    transport: streamable_http
    auth:
      type: service_account
      secretRef: lenny-system/jira-service-account
    visibility: platform           # only available to platform-admin configured sessions
```

The gateway's Connector Registry holds these definitions. The Token/Connector Service holds per-user OAuth tokens. When a session is created with `allowedConnectors: ["github"]` in its lease, the gateway knows it can proxy GitHub calls for that session.

### What the Runtime Sees Inside the Pod

Based on the multi-server model we arrived at, the adapter presents several focused MCP servers. The runtime (as an MCP client) connects to whichever ones it needs:

**1. Workspace server** — serves the local filesystem at `/workspace`. File read, write, list, glob. No gateway involvement; the adapter serves this directly. Clean capability negotiation: if you want resource subscriptions (watch file changes), declare it; otherwise it's request-response.

**2. Platform server** — gateway-backed tools the agent can call:
- `lenny/delegate_task` — spawn a child session
- `lenny/await_children` — wait for child results
- `lenny/cancel_child` — cancel a running child
- `lenny/discover_agents` — find other runtimes available in this deployment (internal discovery)
- `lenny/output` — produce structured `OutputPart` arrays back to the client
- `lenny/request_elicitation` — ask the user something

**3. Connector proxy server** — one MCP server that aggregates all connectors the session is authorized to use. Tools are namespaced by connector ID: `github/create_pr`, `github/list_issues`, `jira/create_ticket`. The adapter proxies these calls to the gateway, which forwards to the external MCP server using the held OAuth tokens. This aggregation is acceptable here because the namespace prefix prevents collisions and the capability negotiation is simple — all tools follow the same request-response pattern.

**4. Lifecycle channel** — not an MCP server, the small separate wire we defined: `checkpoint_ready`, `interrupt`, `credentials_rotated`. Fully optional.

A runtime that only wants to do simple work connects only to the workspace server and platform server. A runtime that needs GitHub connects to the connector proxy server too. A runtime that wants to be checkpointed opens the lifecycle channel.

### Agent Discovery and A2A Cards — Internal vs External

This is where `publishedMetadata` on `RuntimeType` comes in, with a visibility dimension:

```yaml
runtimes:
  - name: research-agent
    image: registry.example.com/research-agent:latest
    ...
    publishedMetadata:
      - key: agent-card
        contentType: application/json
        visibility: internal        # only visible to agents within this Lenny deployment
        value: |
          {
            "name": "research-agent",
            "description": "Deep research agent for document analysis",
            "capabilities": ["research", "summarization"],
            "skills": [...]
          }

      - key: agent-card
        contentType: application/json
        visibility: public          # accessible outside the cluster, no auth required
        value: |
          {
            "name": "research-agent-public",
            "url": "https://lenny.example.com/a2a/research-agent",
            ...
          }

      - key: openapi-spec
        contentType: application/yaml
        visibility: tenant          # visible to agents within the same tenant
        value: |
          openapi: "3.1.0"
          ...
```

The three visibility levels have concrete meaning:

- **`internal`** — served at `GET /internal/runtimes/{name}/meta/{key}`, requires valid Lenny session JWT. Only reachable from inside the cluster. Used for agent-to-agent discovery within a single Lenny deployment.
- **`tenant`** — same as internal but additionally filtered by `tenant_id` claim in the JWT. An agent in tenant A cannot discover tenant B's agents.
- **`public`** — served at `GET /v1/runtimes/{name}/meta/{key}`, no auth required. Reachable from outside the cluster. This is where A2A cards meant for cross-organization discovery live.

The `lenny/discover_agents` tool in the platform server queries the internal metadata registry. A runtime that calls `discover_agents(capability: "research")` gets back a list of runtimes with internal agent cards matching that query, along with enough information to `delegate_task` to them directly within Lenny's delegation system.

### External Tools Outside Lenny

For MCP servers that live outside the cluster but aren't pre-registered as connectors — say, a third-party MCP server a specific runtime wants to use — the right model is that these must be registered as connectors before they can be used. An unregistered external MCP server cannot be called from inside a pod. This is a security requirement: the gateway needs to know about every external endpoint a pod can reach, enforce OAuth flows through the elicitation chain, and record calls in the audit log. Ad-hoc outbound MCP calls would bypass all of that.

A deployer can register a connector at runtime via the admin API (`POST /v1/connectors`), so this isn't a hard design-time constraint — it's just that the connector must exist in the registry before it can appear in a delegation lease's `allowedConnectors`.

### External Agents Outside Lenny

This is the `external_delegate` primitive. An agent calls:

```
lenny/external_delegate(
  protocol: "a2a",
  endpoint: "https://research.partner.example.com",
  task: { ... },
  lease_slice: { maxTokenBudget: 10000, ... }
)
```

The gateway's A2A client adapter handles this: it fetches the Agent Card from the endpoint's `/.well-known/agent.json`, validates it, sends the A2A task, and records the delegation in the task tree exactly like an internal child session. The same lease narrowing, budget tracking, and audit logging apply. The runtime never talks to the external agent directly — it goes through the gateway, which maintains Lenny's security and observability guarantees.

For the other direction — an external agent calling into a Lenny-hosted agent — that's the public A2A card path. When a RuntimeType has a public `agent-card` metadata entry, the gateway serves it at the A2A well-known URL and exposes an A2A endpoint for that runtime at `POST /a2a/{runtimeName}/tasks`. Incoming A2A tasks are translated by the gateway's external protocol adapter into Lenny sessions.

---

## Summary as a Design Map

```
External clients (MCP)
  └── Gateway external MCP interface (platform tools: create_session, attach, etc.)

External agents (A2A)
  └── Gateway A2A endpoint (/a2a/{runtime}/tasks) — for inbound tasks
  └── public RuntimeType.publishedMetadata — agent card discovery

Inside the cluster:
  Gateway
    ├── Connector Registry (connector definitions + OAuth config)
    ├── Token/Connector Service (per-user OAuth tokens)
    ├── Internal metadata registry (tenant/internal agent cards)
    └── External protocol adapters (MCP translator, A2A client/server)

  Adapter (per pod)
    ├── Workspace MCP server (local filesystem)
    ├── Platform MCP server (delegate, discover, output, elicitation)
    ├── Connector proxy MCP server (namespaced external tools, proxied through gateway)
    └── Lifecycle channel (checkpoint, interrupt, credential rotation)

  Runtime (agent binary)
    └── MCP client — connects to one or more adapter servers
```

The main additions needed to the current design are: the ConnectorDefinition schema as a first-class resource, the `visibility` field on `publishedMetadata`, the `lenny/discover_agents` tool in the platform server, the `external_delegate` gateway primitive, and the formalization of the multi-server adapter model. Everything else either already exists or follows directly from decisions already made.

---

## Human (Mar 27, 2026, 12:42 PM):

Alright. Thanks. I think this is pretty good for now on MCP and a to a and the internal protocol. Changing topics a little bit. One of the points raised in the critique is that today, Lenny does not support Lightweight tasks, There's a there's and, basically, it's it's saying that there's a bit of penalty for starting any task, like, any task requires its own pod. And in some cases, we could benefit from sharing parts for multiple different tasks. Either sequentially or concurrently. Can you elaborate on that, and how how this limitation compares to other products and projects out there.

---

## Claude:

Good time to zoom out and think about this carefully, because the critique frames it as a single gap but it's actually three distinct problems that happen to share the same root cause. Let me separate them, then map how each platform solves them.

---

## What the Actual Problem Is

Lenny's one-session-one-pod invariant means **every unit of agent work, regardless of size, pays the full pod overhead**. Even with pre-warming, the hot path still includes workspace upload and materialization, setup commands, and agent session start. That's appropriate for a 2-hour coding session. It's severe overkill for a 3-second "classify this document" call.

But the problem isn't just latency. It's three separate things:

**1. Cold granularity cost.** A workflow that does `search → analyze → synthesize → review` as four logical steps pays four workspace materializations, four pod claims, four session starts, and four credential lease assignments — even if each step runs for 5 seconds and all share the same context. The overhead dwarfs the actual work.

**2. No intra-workflow state sharing.** Each step starts with a fresh workspace copied from the previous step via file export. There's no shared in-memory state, no live object passing, no shared filesystem. A LangGraph workflow can pass a Python dict between nodes in microseconds. Lenny has to serialize it to files, export them, upload to the next pod, and deserialize.

**3. Pod-minutes cost at scale.** A warm pod holds resources — CPU, memory, gVisor overhead — whether or not it's doing anything. For a "fan out to 50 review agents" pattern where each agent runs for 10 seconds, you're paying for 50 × (pod claim + workspace + setup + teardown) when the actual work is 50 × 10s. The overhead can be larger than the work itself.

---

## How Other Platforms Solve This

**LangGraph** doesn't have this problem at all, because it has no isolation boundary between steps. In LangGraph, workflows are organized as nodes in a directed graph. LLM nodes handle language processing, tool nodes interact with external systems, and function nodes execute custom Python code. Parallel execution allows multiple nodes to run simultaneously when their tasks are independent, with proper synchronization so downstream nodes wait until all parallel branches complete. The entire graph runs in a single process. State is a Python dict in memory. Passing data between nodes has essentially zero cost. The trade-off is that there's no isolation — a malicious or buggy node can corrupt the shared state, read other nodes' data, or crash the whole workflow.

**Temporal** makes a completely different architectural choice. Workflows are the orchestration layer — the blueprint defining structure. Activities are where the actual work happens: calling LLMs, invoking tools, making API requests. This separation is exactly what makes Temporal perfect for AI agents. The key insight is that a single Temporal Worker process can handle many Activity executions simultaneously. Workers are configured with `maxConcurrentActivityTaskExecutions` — a single worker can handle many concurrent AI API calls, and `maxCachedWorkflows` allows thousands of workflows to share the same worker process. There's no per-task pod spawn. One long-lived worker process picks up tasks from a queue. Tasks have near-zero startup cost. The trade-off: tasks within the same worker share a process, so isolation is process-level, not kernel-level.

**Modal** solves it through a different mechanism — serverless functions with ~150ms cold starts, where the function definition itself specifies compute requirements. Multiple invocations of the same function share warm containers from a pool. There's essentially no workspace concept — functions receive arguments and return values. It's pure compute, not a persistent agent workspace.

**E2B** is closest to Lenny's model and has the same fundamental limitation — each sandbox is its own isolated environment. They mitigate startup cost through Firecracker microVMs with pre-warmed snapshots, getting cold starts to ~150ms, but each task still gets its own sandbox. The pattern there is to make the sandboxes cheap enough that the per-task overhead doesn't matter for most workloads.

---

## The Real Trade-off Lenny Is Making

The one-session-one-pod invariant is a deliberate security decision, not an oversight. Cross-session leakage through residual workspace files, DNS cache poisoning, runtime memory, and shared credential state are all real threat vectors. Every platform that reuses processes across tasks accepts some version of these risks.

The spectrum looks like this:

| Approach | Startup cost | Isolation level | Cross-task leakage risk |
|---|---|---|---|
| LangGraph nodes | ~0ms | None (shared process) | High |
| Temporal activities | ~ms (queue poll) | Process-level | Medium |
| Modal serverless | ~150ms | gVisor container | Low-medium |
| Lenny sessions | 2–8s even pre-warmed | gVisor/Kata kernel | Near-zero |
| E2B sandboxes | ~150ms (Firecracker) | MicroVM | Near-zero |

Lenny is at the "maximum isolation" end because it was designed for untrusted agent code — an agent can run arbitrary tools, install packages, and execute LLM-generated code. The isolation isn't optional. But that doesn't mean the granularity has to stay coarse.

---

## What Lightweight Task Mode Would Actually Mean

The critique's suggestion is right in spirit but needs more precision. There are actually two distinct modes worth defining, not one:

**Task pod mode** — a single pod accepts multiple sequential tasks. Between tasks, the pod is cleared of workspace state but the process stays alive, the container stays warm, and credentials can be held. This is Temporal's model applied to Lenny pods. The security requirement is a verified scrub between tasks: workspace wiped, `/sessions/` tmpfs cleared, environment reset. The isolation is still kernel-level (gVisor/Kata) per pod, but the pod lifetime is no longer 1:1 with the task lifetime.

This would be appropriate for stateless or near-stateless tasks — "classify this document," "generate a summary," "check this code for syntax errors." Tasks that don't accumulate state across runs.

**Graph pod mode** — a single pod runs a multi-step graph, with the runtime managing state internally. Each step of the graph is a task within the same session. The pod stays alive across the whole graph. This is LangGraph's model, but with Lenny's isolation boundary around the whole graph rather than around each node. Cross-step state sharing is in-process (cheap). Isolation is per-graph-execution, not per-step.

This would be appropriate for agentic workflows where steps are logically connected and share context — exactly the `search → analyze → synthesize → review` pattern from the critique.

The second mode is already supported in spirit — a runtime can implement its own internal graph and run the whole thing within one Lenny session. LangGraph running inside a Lenny pod *today* gets exactly this model. The limitation is that Lenny doesn't provide platform-level primitives for the graph structure, so you can't get platform-level checkpointing per graph node, or platform-level retry of a single failed node, or platform-level observability into which node is running. Temporal's key insight is that the Workflow replays to its last known state using recorded decisions from execution history — if something fails at step 4, you resume at step 4, not step 1. Lenny has nothing equivalent below the session level.

---

## Where This Sits on the Priority List

The critique rates it High Impact, and that's correct for the generic agentic workflow market. But it's also the most complex change to make safely, and the most likely to introduce security regressions if done carelessly.

The practical priority ordering is probably:

First, expose the graph pod model properly — make it easy for runtimes like LangGraph to run inside a single Lenny session and expose their internal steps through the observability protocol (the optional trace parts from earlier discussions). This costs almost nothing and immediately supports the whole LangGraph ecosystem without any architecture change.

Second, design the task pod mode carefully — define the verified scrub protocol, specify which credential modes are compatible with task reuse (pool credentials yes, user-scoped credentials maybe), and add it as an opt-in RuntimeType capability. This unlocks the Temporal-style patterns without compromising the default isolation model.

The lightweight task mode should probably not launch as part of v1, but the data model needs to accommodate it from day one — specifically, the `AgentPool` CRD needs a `taskMode` enum field even if only `session` is implemented initially, so that adding `task_pod` later doesn't require a schema migration.

---

## Human (Mar 27, 2026, 12:55 PM):

I think those are very good suggestions. And I do think that both of those should be implemented as part of v one. So thanks for that. And at the same time, I think that if Kubernetes 6, sandbox is is going to become a standard, then it also makes sense Sorry. I'm going now back to your Born from way earlier, but, if that's going to become part of the standard, then I do think it makes sense to to use it instead of in use it inside of Lenny, instead of trying to Rebuilding. So not a question, but more of a comment

---

## Claude:

That's a well-reasoned decision, and it connects nicely to both points.

On agent-sandbox: adopting it means Lenny's `AgentPool`/`AgentPod` CRDs get replaced by `SandboxTemplate`/`SandboxWarmPool`/`SandboxClaim`, and the warm pool controller gets replaced by the upstream controller. Lenny's gateway still owns everything above that layer — session management, credential leasing, policy enforcement, the MCP fabric, delegation — none of that is in agent-sandbox's scope. The practical benefit is that Lenny stops maintaining a Kubernetes controller and gets upstream community maintenance, Pod Snapshots on GKE (which directly helps with checkpointing), and a claim model that's been designed by people who thought hard about exactly this problem. The one thing worth watching is whether the `SandboxClaim` adoption mechanism has the same optimistic-locking guarantee Lenny currently gets from the status PATCH approach — that needs a close read before committing. But the intent is clearly compatible.

On the two lightweight modes in v1: given that agent-sandbox is now the infrastructure layer, the task pod mode maps well onto `SandboxWarmPool` with a different lifecycle policy — pods that survive task completion and get scrubbed rather than terminated. The SandboxTemplate can declare that lifecycle mode. The graph pod mode is even simpler since it's just a long-lived sandbox session where the runtime manages internal steps. Both fit naturally without needing to extend the sandbox primitive in unusual ways.

The main v1 design implication is the one mentioned before: the `AgentPool` schema needs a `executionMode` field from the start — `session` (default, one-session-one-pod), `task` (task pod mode with scrub), so the data model is ready even if `task` mode lands slightly later in the v1 build sequence. Worth adding that to the Phase 1 foundation work rather than retrofitting it in Phase 9.

---

## Human (Mar 27, 2026, 12:57 PM):

Okay. I think we're zeroing in on, a pretty good set of changes, to the technical spec. Can you Go back to this entire conversation and look at what we have decided and then run doing all that research, to, again, compare to products, and projects out there, like, LineGraph, temporal, Model etcetera. And and see if there's anything else that Lenny might be missing.

---

## Claude:

Let me first compile all decisions from our conversation, then do targeted research against the gaps.Now I have enough to give you a thorough answer. Let me compile everything.

---

## Summary of Decisions from This Conversation

Before the new gaps, here's the complete list of what we decided, as a reference:

**From infrastructure/Kubernetes:**
- Adopt `kubernetes-sigs/agent-sandbox` (`SandboxTemplate`/`SandboxWarmPool`/`SandboxClaim`) instead of rebuilding Lenny's own CRDs

**From the protocol and adapter model:**
- Internal `OutputPart` format as Lenny's native content model; the adapter translates to MCP/A2A at the boundary
- Multiple focused MCP servers per adapter (workspace, platform, connector proxy) rather than one aggregated server
- Separate lightweight lifecycle channel alongside MCP — not inside MCP
- Layer 0/1/2 optional integration model for runtimes

**From A2A and extensibility:**
- `ExternalProtocolAdapter` interface on the gateway (MCP is the first implementation)
- `external_delegate` gateway primitive for calling out to external A2A agents
- Generic `publishedMetadata` with `visibility` (internal / tenant / public) on `RuntimeType`, replacing any named `agentCard` field

**From connectors and discovery:**
- `ConnectorDefinition` as a first-class declared resource in the registry
- `lenny/discover_agents` tool in the platform MCP server for internal agent discovery

**From execution model:**
- Lightweight task pod mode (single pod accepts sequential tasks with scrub between them) — v1
- Graph pod mode (single pod runs a multi-step graph; runtime manages internal state) — v1
- `executionMode` field on `AgentPool` from day one, even if only `session` is initially implemented

---

## New Gaps Found in Research

### 1. Cross-Session Memory — High Impact, Not Covered Anywhere

This is the most significant gap the research surfaces. Lenny has excellent within-session state: the workspace, the transcript, the event stream. What it has zero concept of is state that persists *across* sessions for the same user or agent.

Long-term memory is persistent storage outside the context window. An agent can store years of interactions with virtually unlimited capacity. Short-term memory lives in the context window — the conversation right now. Production systems increasingly need both.

In practice this means: an agent that helped a user refactor a codebase last week has no memory of that interaction when a new session starts. A customer-facing agent can't remember user preferences. A research agent can't build on conclusions from prior sessions. Every session starts from scratch.

Competitors have addressed this at different layers. Amazon Bedrock AgentCore Memory provides both short-term working memory (capturing immediate conversation context) and long-term intelligent memory (storing persistent insights and preferences across sessions), so agents can retain context, learn from interactions, and deliver truly personalized experiences. LangGraph has built-in thread persistence. Mem0 and Zep are dedicated cross-session memory stores that are widely used.

Lenny's `POST /v1/sessions/{id}/derive` endpoint (which pre-populates a new session from a prior session's workspace) is a partial answer for workspace continuity, but it's not memory. It's file copying. It doesn't store extracted facts, user preferences, or agent-learned knowledge in a queryable form.

**What's needed:** A `MemoryStore` interface — consistent with the existing role-based storage interfaces — that runtimes can write to and read from across sessions, scoped by `user_id`, `tenant_id`, and optionally `agent_type`. The gateway mediates all access, same as everything else. Runtimes that want to use cross-session memory implement the optional `lenny/memory_write` and `lenny/memory_query` tools in the platform MCP server. Runtimes that don't care about memory ignore them entirely. The storage backend is pluggable (vector DB for semantic retrieval, Postgres for structured facts, or both).

---

### 2. Semantic Caching at the LLM Proxy — High Cost Impact

Lenny already has the credential-injecting reverse proxy for LLM calls (the proxy mode from Section 4.9). This is the perfect place to add caching — every LLM call already flows through it in proxy mode. Yet the design has no caching concept at all.

The business case is strong. Research on production agent workloads consistently finds 40–73% cost reduction from semantic caching, since agents often issue structurally similar LLM calls (same system prompt, similar user queries, repeated tool result formats). Semantic caching matches data based on meaning rather than exact key-value lookups. When two queries look different but mean the same thing, semantic caching recognizes the equivalence and returns the stored answer — avoiding another LLM call entirely.

For Lenny specifically, the task pod mode makes this significantly more valuable: a pool of task pods running the same type of task (document classification, code review, summarization) will hit semantically similar LLM calls repeatedly across different task invocations. Without caching, each pays full inference cost.

**What's needed:** An optional `CachePolicy` on `CredentialPool` configuration — `{ strategy: "semantic", ttl: 300, similarityThreshold: 0.92, backend: "redis" }`. The LLM proxy checks the cache before forwarding upstream and populates it on cache misses. This is explicitly optional — deployers who need strict non-caching (financial calculations, real-time data queries) leave it unconfigured.

---

### 3. Prompt Injection and Content Safety Guardrails — Security Gap

Lenny's security model is excellent at the infrastructure level: gVisor/Kata isolation, mTLS, credential leasing, no pod-to-pod communication. But it has no semantic-level security layer — nothing that inspects the content of what agents are processing or producing.

A prompt injection vulnerability occurs when user prompts alter the LLM's behavior or output in unintended ways. Indirect prompt injections occur when an LLM accepts input from external sources such as websites or files — the content may have data that, when interpreted by the model, alters its behavior in unintended ways.

This matters for Lenny because agents regularly process untrusted external content — web pages, uploaded files, tool results from external MCP servers. Any of these could contain embedded prompt injections that cause the agent to exfiltrate data, bypass connector access controls, or take actions outside the delegation lease's intended scope. The pod sandbox prevents the *infrastructure* from being compromised, but can't prevent the *agent* from being manipulated into doing something it shouldn't.

The right home for this is the `RequestInterceptor` extension point that the critique already called out in Section 1.3 — a guardrail interceptor that can inspect tool results before they're delivered to the agent, and agent outputs before they're forwarded to clients or connectors. A built-in interceptor would use a configurable safety classifier. Deployers with stricter requirements implement their own via the gRPC extension interface. Deployers who don't need it don't pay the latency cost.

**What's needed:** A `GuardrailsInterceptor` as the first implementation of the `RequestInterceptor` extension point, with optional phases: inbound (inspect content coming into the agent from external sources), outbound (inspect content the agent is about to send to connectors or clients). The interceptor is off by default; deployers enable it per-pool with configurable sensitivity settings.

---

### 4. Agent Evaluation and Quality Testing — Medium-High, Enterprise Blocker

Lenny has very solid operational observability (OpenTelemetry, the optional trace protocol we designed). What it doesn't have is quality evaluation — any mechanism to assess whether the agent's outputs are *correct*, not just whether the session *succeeded*.

AI observability differs from traditional application monitoring because an agent can return HTTP 200 with confidently wrong content. AI observability requires new primitives like traces showing multi-step reasoning, evaluations measuring output quality, and session analysis tracking coherence across interactions.

In practice: a deployer can see that 98% of sessions complete without error. But they can't see that 15% of those sessions produced factually wrong outputs, used the wrong tool strategy, or hallucinated file paths. LangSmith's core value proposition is answering exactly this question, and it's why LangGraph's adoption in enterprise contexts is tied to LangSmith.

Lenny's existing session transcripts and trace data are the raw material needed for evaluation. What's missing is a framework for turning those into datasets and running automated quality checks.

**What's needed:** An evaluation hook in the session lifecycle — `POST /v1/sessions/{id}/eval` — that accepts a set of scored evaluator results (LLM-as-judge scores, custom heuristics, ground-truth comparisons). These are stored as session metadata and surfaced in the observability dashboard. This doesn't require Lenny to implement evaluation logic itself — it's a storage and API concern. The actual evaluation work (running LLM-as-judge, building test datasets) is done by deployers using their existing eval tooling (Braintrust, Langfuse, custom). Lenny provides the hook and the storage.

A companion to this is a `replay` primitive — `POST /v1/sessions/{id}/replay` — that re-runs a session against a different runtime version using the same workspace and prompt history. This enables regression testing when upgrading runtimes, something no surveyed platform currently provides natively.

---

### 5. Intelligent LLM Model Routing — Medium, Already Partially There

Lenny's credential pool fallback chain handles failure-based routing: rate-limited on key-1, try key-2, try Bedrock. What it doesn't have is *intent-based* routing — "use the cheaper model for straightforward summarization, use the expensive model when the agent requests reasoning."

Research shows that organizations using a single LLM for all tasks are overpaying by 40–85% compared to those using intelligent routing. AI model routers act as intelligent traffic controllers, directing requests based on cost, latency, capability, or availability.

This is more of a runtime concern than a platform concern — a well-designed runtime can decide which model to use based on the task. But Lenny could support it at the credential policy level by adding a `routingHints` field that runtimes can set on LLM calls, with the credential proxy selecting the appropriate pool accordingly. This is a modest extension to the existing credential pool model, not a new concept.

---

### Summary Table

| Gap | Compared To | Priority | Effort |
|---|---|---|---|
| Cross-session memory | AWS AgentCore, LangGraph, Mem0 | High | Medium — new storage interface + 2 MCP tools |
| Semantic caching at LLM proxy | Every major AI gateway | High | Low — add to existing proxy infra |
| Prompt injection guardrails | OWASP LLM Top 10 #1 risk, Kong, AWS Guardrails | High | Medium — first impl of RequestInterceptor |
| Agent evaluation hooks | LangSmith, Braintrust, Arize | Medium-High | Low — storage + API only, no eval logic |
| Session replay for regression testing | Nothing in market provides this natively | Medium | Medium — extends derive session concept |
| Intent-based model routing | Bifrost, LiteLLM, every AI gateway | Medium | Low — extend credential pool routing hints |

The most important of these for the OSS community and enterprise adoption is memory. An agent platform that can't remember anything across sessions is fundamentally limited to single-session use cases, which cuts off a large class of valuable workflows — personal assistants, long-running research agents, domain-specialized agents that get better over time. The others are operational maturity improvements; memory is a capability gap.

---

## Human (Mar 27, 2026, 1:17 PM):

Alright. Number 1 makes sense. The only question I have is, whether MCB is the right choice for storing memories. It it maybe is. It just I honestly don't know how others are doing this. And so I'm curious if if it's the best choice or if there's something else that that should be done. Semantic caching, I think I think that makes sense. I think it should be configurable by default, disabled, and there should be hooks for deployers to provide their own semantic caching and we could embed I We could include a default semantic, caching layer But, again, it should be configurable and completely replaceable. For prompt ejection and guardrails, I agree with the approach. I think it should be by default disabled, The should provide hooks so that people can provide their own guardrail systems, I think that that should be the extent of it. In terms of agent evaluation and quality testing, That makes sense as well. I think we don't want to reinvent the wheel here in terms of what other products like blankfear has already provide or or, BrainTrust So should definitely be and if storage makes sense in Lenny, then we can store that as well. That's That's fine. But, we should not provide Again, everything should be via hooks here, and I don't think we should implement our own eval logic And then intelligent LOM model routing That makes sense. I think it should be opt in disabled by default, and the each runtime can decide if they wanna use it or not. I think more in general, the the whole Llam or, like, the the whole fallback mechanism probably needs a little more logic as you said. Think that that would be part of it, but it should be extensible. So you should be able to replace entirely the logic that is being used or, like, a deployer be able to replace completely the logic and implement their own custom one.

---

## Claude:

Let me search specifically on how the leading platforms implement memory storage before answering.Good research to do before answering. The picture is now clear. Let me address everything you raised.

---

## On Memory Storage — What the Research Shows

The honest answer is that memory storage for agents is not a solved problem yet. The memory layer space right now feels a lot like the vector database space felt in 2022 — a cluster of solutions each solving part of the problem, active benchmark wars, real production pain, and a consolidation wave clearly approaching.

There are fundamentally three types of memory that matter, and they need different storage mechanisms:

**Episodic memory** — "what happened in past sessions" — is best served by a combination of vector embeddings (for semantic retrieval: "find sessions where the user was working on auth") and structured metadata (for temporal retrieval: "find the last 3 sessions for this user"). Postgres with pgvector handles this reasonably well.

**Semantic memory** — extracted facts and preferences ("this user prefers TypeScript, works in fintech, allergic to shellfish") — is where the real architectural disagreement is. Mem0 extracts and compresses these as vector-indexed text snippets. Zep stores memory as a temporal knowledge graph that tracks how facts change over time — temporal knowledge graph tracking fact evolution and relationship changes across sessions, with multi-hop and temporal query support for complex reasoning scenarios. The trade-offs are real: Zep's memory footprint exceeds 600,000 tokens per conversation (versus 1,764 for Mem0), and their team's testing found that immediate post-ingestion retrieval often failed — correct answers only appeared hours later after background graph processing completed.

**Procedural memory** — "how this agent does things" / learned workflows — is the least mature and currently only handled well by Letta and LangMem.

The practical conclusion for Lenny is that **you should not pick a storage technology here**. The space is moving too fast, the right answer depends on the use case, and committing to any specific implementation would be premature. What Lenny should do is exactly consistent with its existing design philosophy: define a `MemoryStore` interface alongside the other role-based storage interfaces (`SessionStore`, `ArtifactStore`, etc.), and make it pluggable from the start.

The interface is straightforward:

```go
type MemoryStore interface {
    Write(ctx, scope MemoryScope, memories []Memory) error
    Query(ctx, scope MemoryScope, query string, limit int) ([]Memory, error)
    Delete(ctx, scope MemoryScope, ids []string) error
    List(ctx, scope MemoryScope, filter MemoryFilter) ([]Memory, error)
}

type MemoryScope struct {
    TenantID  string
    UserID    string
    AgentType string   // optional: scope to a runtime type
    SessionID string   // optional: scope to a specific session
}
```

Deployers who want Mem0 implement the interface backed by Mem0. Deployers who want Zep's knowledge graph implement it backed by Zep. Deployers who want to stay simple use a default implementation backed by Postgres + pgvector that Lenny ships out of the box. The runtime accesses it through the `lenny/memory_write` and `lenny/memory_query` tools in the platform MCP server — the runtime never knows what's underneath.

This is identical to how other pluggable storage works in Lenny, and it correctly postpones a technology bet that the industry hasn't settled yet.

---

## On Everything Else — Confirming the Design Directions

Your framing for all five items follows a consistent philosophy that is worth making explicit, because it should become a design principle stated in the technical spec: **Lenny provides the wiring, not the logic.** Here's how that maps to each:

**Semantic caching:** The `CachePolicy` on `CredentialPool` is an interface. Lenny ships a default Redis-backed semantic cache implementation. Deployers can replace it entirely with their own `SemanticCache` interface implementation — different vector backend, different similarity model, different eviction logic. Disabled by default, opt-in per pool.

**Guardrails:** The `RequestInterceptor` interface (which the critique already calls out in Section 1.3) is the hook. Lenny doesn't ship guardrail logic — it ships the interception points and a no-op built-in. Deployers wire in AWS Bedrock Guardrails, Azure Content Safety, Lakera Guard, or their own classifier. Off by default.

**Evaluation:** `POST /v1/sessions/{id}/eval` accepts scores from external tools. Lenny stores them alongside the session transcript and surfaces them in the observability dashboard. The evaluation work — LLM-as-judge, dataset curation, regression testing — happens in Braintrust, Langfuse, or whatever the deployer uses. Lenny is just the storage and API layer.

**Model routing:** The credential pool selection logic becomes a pluggable `CredentialRouter` interface. The default implementation is Lenny's existing least-loaded / round-robin / sticky-until-failure strategy. Deployers who want cost-aware routing, latency-based routing, or completely custom logic implement the interface and plug it in. Disabled by default, opt-in at the pool level.

The unified design note to add to the technical spec is: *"Lenny follows a hooks-and-defaults pattern for all cross-cutting AI capabilities (memory, caching, guardrails, evaluation, routing). Each capability is defined as an interface with a sensible default implementation. All defaults are disabled unless explicitly enabled by the deployer. All interfaces are fully replaceable. Lenny never implements AI-specific logic (eval scoring, memory extraction, content classification) — that belongs to specialized tools in the ecosystem."*

This keeps Lenny focused on what it's uniquely good at — isolated execution, policy enforcement, credential brokering, delegation primitives — and avoids the trap of trying to build an AI logic platform on top of an infrastructure platform.

---

## Human (Mar 27, 2026, 1:29 PM):

Okay. I like this. On a slightly related topic, one of the things that I've been thinking about is potentially adding primitives to Lenny to support AB testing and experimentation. And so for example, one of the things that could be included is, the ability to define generic routes. Where you could say I want 10% of request to that run to this run time to go to version x versus the default one or maybe, like, selecting by specific users so if you're user that's part of a whitelist or part of a, an an experiment, then you would be routed to one runtime or another, and this would not only be implemented at the at the at the client side layer. So, like, the first layer, but also, like, for any, nested, calls, that happen inside of Lenny. I don't I don't think that Lenny should become an experimentation platform in itself but I think it should provide all the different primitives and maybe just some very simple basic mechanisms that people could use out of the box if they want to, or they can replace them if that's what they want to do as well. And that this ties also into the whole eval's conversation. Although I'm not exactly what the implications for EVAS would be. Or for the EVALs interface. So does this sound like a good idea? Do you have any other suggestions or a different approach that you would you you would recommend?

---

## Claude:

This is a good idea and it fits naturally into what's already there. Let me reason through it carefully.

---

## Why It Belongs in Lenny

The instinct is correct: Lenny is uniquely positioned to do this in a way that no client-side or external experimentation tool can, because Lenny controls the entire execution boundary — both the entry point (session creation) and every nested delegation call. A client-side feature flag library can split traffic at the first hop, but it has no visibility into what happens when a parent session delegates to child sessions. Lenny can maintain experiment context across the entire task tree, which is genuinely novel.

---

## The Natural Home: The Policy Engine

The routing decision doesn't need a new architectural component. The Policy Engine (Section 4.8) already evaluates every session creation request and has access to `user_id`, `tenant_id`, runtime requested, and request metadata. An `ExperimentRouter` is simply another `RequestInterceptor` phase — specifically, a pre-routing phase that can override the runtime and pool selection before the session starts.

This means the extensibility story is already designed: the built-in `ExperimentRouter` is one implementation of the interceptor interface. Deployers who want Statsig, LaunchDarkly, or their own allocation service implement a custom interceptor that calls out to their system. Same pattern as guardrails, same pattern as custom auth evaluators.

---

## What the Primitives Look Like

**The `ExperimentDefinition` resource** — a first-class declared resource alongside `RuntimeType` and `ConnectorDefinition`:

```yaml
experiments:
  - id: claude-v2-rollout
    status: active                  # active | paused | concluded
    baseRuntime: claude-worker      # the control group
    variants:
      - id: treatment
        runtime: claude-worker-v2
        pool: claude-worker-v2-sandboxed-medium
        weight: 10                  # percentage (10% to treatment, 90% to control)
    
    targeting:
      mode: percentage              # percentage | cohort | combined
      sticky: user                  # stick assignment by: user | session | none
      
    propagation:
      childSessions: inherit        # inherit | control | independent
      
    metadata:
      description: "Testing v2 runtime with improved tool use"
      owner: "platform-team"
      startedAt: "2026-03-01"
```

**Targeting modes:**
- `percentage` — pure traffic split. N% of sessions matching `baseRuntime` go to the variant. The allocation uses a deterministic hash of `user_id` (or `session_id` for non-sticky) so the same user always lands in the same bucket without needing a separate cohort store.
- `cohort` — explicit whitelist. A `cohortRef` points to a stored list of `user_id` values. Users on the list get the variant; everyone else gets the control. The cohort list is managed via the admin API.
- `combined` — percentage within a cohort. "10% of beta users get the new runtime."

**The `sticky` field matters a lot.** A user mid-conversation who gets flipped from one runtime to another mid-session is a bad experience and produces contaminated eval data. `sticky: user` means: once a user lands in a variant (determined by hash), they stay there for the experiment's duration. `sticky: session` means: randomize per session creation, so the same user can experience both variants across different sessions. `sticky: none` means randomize per request — mostly useful for headless/batch workloads where user continuity doesn't matter.

---

## Propagation to Nested Calls — the Key Design Point

This is where Lenny adds unique value. When a parent session is running under an experiment variant, child sessions spawned via `delegate_task` face a choice: should they run the same variant, always the control, or be independently allocated?

`propagation.childSessions: inherit` is the right default. The reasoning: if you're testing "does v2 produce better results on the full task?", you want the entire task tree to use v2. If the parent is treatment but children are control, you're measuring a mixed system and can't attribute outcomes cleanly.

Mechanically, the experiment context travels in the delegation lease:

```json
{
  "experimentContext": {
    "experimentId": "claude-v2-rollout",
    "variantId": "treatment",
    "inherited": true
  }
}
```

The gateway checks this when allocating child pods — if `inherited: true`, it skips normal allocation and routes directly to the variant's pool. The child runtime never knows it's in an experiment; it just gets the variant's pool. This is clean and requires no runtime cooperation.

---

## Connection to Evals

This is where the two things tie together elegantly. Every session already has `experiment_id` and `variant_id` as first-class metadata fields once the routing happens. The eval hook (`POST /v1/sessions/{id}/eval`) accepts scores from external tools. The usage API and observability dashboard can now filter and group by experiment and variant.

The practical flow for a deployer: run the experiment, have their eval tool (Braintrust, custom) score session outputs, POST the scores to Lenny, then query `GET /v1/experiments/{id}/results` which returns variant-grouped aggregate scores alongside cost and latency metrics. Lenny doesn't produce the scores — that's still external tooling — but it provides the data model that makes comparison possible.

The `GET /v1/experiments/{id}/results` endpoint would return something like:

```json
{
  "experimentId": "claude-v2-rollout",
  "variants": [
    {
      "variantId": "control",
      "sessions": 450,
      "evalScores": { "mean": 0.78, "p50": 0.81 },
      "costPerSession": { "tokens": 12400, "podMinutes": 1.8 }
    },
    {
      "variantId": "treatment", 
      "sessions": 50,
      "evalScores": { "mean": 0.84, "p50": 0.86 },
      "costPerSession": { "tokens": 11200, "podMinutes": 1.9 }
    }
  ]
}
```

That's the minimal output that makes the A/B comparison meaningful: quality up, cost roughly neutral. Everything else (statistical significance, confidence intervals, segment breakdowns) is what specialized experimentation platforms do better, and deployers who need that pipe the session data into their existing tools.

---

## Warm Pool Implication Worth Calling Out

Running an experiment means maintaining warm pods for both the control and treatment pools simultaneously. For a 10/90 split, you don't want 10% of the control pool sitting idle waiting for the treatment variant. The warm pool controller needs to know about active experiments and size pools proportionally to their traffic weight. The `ExperimentDefinition` should include a `warmPoolPolicy` that overrides the pool's default `minWarm` based on variant weight — so a 10% variant pool stays appropriately sized rather than under-provisioned.

---

## What to Explicitly Not Build

Consistent with the "hooks and defaults" principle: Lenny shouldn't implement statistical significance testing, experiment lifecycle management (when to stop an experiment, winner declaration), multi-armed bandit algorithms, or segment analysis. These belong in dedicated experimentation platforms. Lenny's job is to route correctly, record the context, and expose the data — then get out of the way.

---

## Human (Mar 27, 2026, 1:39 PM):

Alright. This makes sense. Can you think a little bit more about the changes that would be needed to The warm pod controller. Or whatever we call it. Is there How can we make this As automatic as possible.

---

## Claude:

Let me think through this carefully from first principles, because the warm pool interaction is where experimentation can easily go wrong in subtle ways.

---

## The Core Problem With Manual Pool Sizing for Experiments

The current `minWarm` / `maxWarm` configuration on `AgentPool` assumes the deployer knows in advance how much demand to expect. That works for stable production pools where you've observed historical traffic patterns. It doesn't work for experiments because:

- Experiment traffic is a *fraction* of an existing pool's traffic, not a known quantity on its own
- Demand fluctuates — a pool that needs 20 warm pods at 9am needs 5 at midnight, and your variant should track this automatically
- Experiments have a lifecycle — they start, ramp, run, and conclude — and the warm pool needs to respond to each phase without human intervention
- If you under-provision the variant pool, experiment sessions queue or cold-start, which contaminates your latency measurements and makes the variant look worse than it is

The goal is to make the warm pool state a *derived quantity* from the experiment definition plus observed demand signals, never a manually specified value.

---

## The Right Architecture: A Separate ExperimentController

Rather than modifying the existing WarmPoolController — which is already complex and manages a well-defined responsibility — the cleanest approach is a dedicated `ExperimentController` that sits above it.

The relationship is:

```
ExperimentDefinition
        │
        ▼
ExperimentController ──── reads demand signals from base pool
        │                  computes variant pool target size
        │
        ▼
AgentPool (variant) ──── managed like any other pool
        │
        ▼
WarmPoolController ──── unchanged, manages whatever AgentPools exist
```

The WarmPoolController never knows about experiments. It just manages AgentPools. The ExperimentController creates and continuously updates the variant `AgentPool`'s warm count based on its own reconciliation logic. This respects single responsibility and avoids making the WarmPoolController more complex.

---

## The Automatic Sizing Formula

The critical insight for making this fully automatic is that the variant pool's required capacity is always a function of the base pool's observed demand, not an independent value. The controller maintains a continuous demand signal for every pool — a rolling window P95 of sessions-per-minute over the last, say, 15 minutes. This signal already exists implicitly to drive the existing `scalePolicy`; it just needs to be made explicit and queryable.

The variant warm count derives from it:

```
variant_minWarm = ceil(base_demand_p95 × variant_weight × safety_factor)
```

Where `safety_factor` defaults to 1.5 — you keep 50% headroom so a short burst doesn't immediately exhaust the variant pool. Deployers can tune this per experiment or globally.

So if the base pool currently has a P95 demand of 40 sessions/minute and the variant has a 10% weight with a default safety factor:

```
variant_minWarm = ceil(40 × 0.10 × 1.5) = ceil(6) = 6
```

If demand drops to 10 sessions/minute at night:

```
variant_minWarm = ceil(10 × 0.10 × 1.5) = ceil(1.5) = 2
```

The variant pool automatically scales down with the base pool, no manual intervention needed. And because the controller runs its reconciliation loop continuously (every 30 seconds, same as the WarmPoolController), it tracks demand changes in near-real-time.

---

## The Lifecycle: Four Phases That Need Automatic Handling

**Phase 1: Pre-warming (before experiment starts)**

If the experiment has a `startAt` timestamp, the controller begins warming the variant pool ahead of time. The lead time should be configurable but default to `2 × pod_startup_latency` for the variant's isolation profile. For gVisor that's roughly 5 minutes; for Kata, 10. This means the variant pool is already at full target capacity when the first experiment session arrives — eliminating the "cold start contamination" problem where early measurements are artificially penalized.

The `ExperimentDefinition` gets a `prewarmLeadMinutes` field with a sensible default computed from the pool's isolation profile:

```yaml
experiments:
  - id: claude-v2-rollout
    status: scheduled
    startAt: "2026-04-01T09:00:00Z"
    prewarmLeadMinutes: 10     # default computed, overridable
```

At `startAt - 10min`, the controller creates the variant `AgentPool` with the derived `minWarm`. By `startAt`, pods are warm.

**Phase 2: Gradual ramp (optional)**

Instant traffic splits are risky if the variant has a defect — you immediately expose 10% of users rather than discovering the problem at 1%. A ramp policy lets the weight increase gradually:

```yaml
ramp:
  initialWeight: 1          # start at 1%
  targetWeight: 10          # end at 10%
  stepSize: 1               # increment by 1%
  stepIntervalMinutes: 30   # every 30 minutes
  pauseOnErrorRateExceeds: 5.0   # auto-pause if variant errors > 5%
```

The controller advances the ramp on schedule, updating both the routing weight (used by the gateway's ExperimentRouter interceptor) and the variant pool's target warm count proportionally. The `pauseOnErrorRateExceeds` threshold enables automatic rollback without human involvement — the controller detects the health signal and sets `experiment.status = paused`, which triggers the wind-down phase.

**Phase 3: Steady state**

The controller runs its reconciliation loop, continuously adjusting `variant_minWarm` based on the current base pool demand signal. No intervention needed. The variant pool tracks demand just like any other pool.

**Phase 4: Wind-down (when experiment concludes or pauses)**

When an experiment's status transitions to `concluded` or `paused`, the controller begins draining the variant pool gracefully. Active sessions are allowed to complete. Once the variant pool has no active sessions, the controller sets `minWarm = 0` on the variant AgentPool. The WarmPoolController then handles draining and terminating the idle pods in its normal fashion. The experiment controller deletes the variant AgentPool resource once it reaches zero.

Critically, the experiment controller should *not* immediately terminate variant pods — doing so would interrupt active sessions. The drain is cooperative with the session lifecycle.

---

## Handling the "No Demand Signal Yet" Bootstrap Problem

A new experiment variant's pool starts with zero history. The base pool's demand signal exists, but the formula above requires it. The cold-start edge case: what if the base pool also has no recent demand (a fresh deployment or an off-hours pool at `minWarm: 0`)?

The safest fallback is to provision a minimum of `ceil(1 / variant_weight)` pods — the number needed so that at least one arrival is likely to find a warm pod. For a 10% variant that's `ceil(1 / 0.10) = 10`. This is a conservative safe default that ensures the variant can serve at least its proportional share of the first burst of traffic.

Once the first real demand measurements arrive (typically within one reconciliation cycle), the formula takes over.

---

## The Experiment Status as a Control Plane Signal

One more automation worth building in: the experiment's `status` field becomes a control plane signal that other parts of Lenny react to automatically, not just the warm pool.

When `status` transitions:
- `scheduled → active`: ExperimentController starts pre-warming; gateway begins routing to variant
- `active → paused`: ExperimentController starts wind-down; gateway stops routing to variant immediately (existing active sessions continue)
- `paused → active`: ExperimentController restarts pre-warming with reduced lead time (some pods may still be alive); gateway resumes routing
- `active → concluded`: Same as pause but permanent; controller sets pool to drain and deletes it after drain completes

These transitions can be triggered manually via the admin API, or automatically by the health-based triggers (error rate threshold, latency threshold). Both paths produce the same state transition, and the system reacts identically regardless of how the transition was triggered.

---

## What Remains Manual

Almost nothing at steady state. The deployer specifies the experiment definition once — which runtimes, what weight, what targeting, what ramp policy, what health thresholds. Everything after that is automatic: pool creation, pre-warming, capacity tracking, ramping, health monitoring, rollback, and drain.

The only things that require human judgment are the things that should require human judgment: declaring the winner, choosing to increase or decrease weights beyond the ramp policy, and cleaning up the concluded experiment record. The controller won't auto-declare winners because that involves interpreting eval scores, which is external tooling's job. But it does give the experimenter a fully managed warm pool that tracks demand automatically, so the experimentation infrastructure never becomes a bottleneck or a source of measurement error.

---

## Human (Mar 27, 2026, 1:49 PM):

Okay. This mostly makes sense. But couple of comments. 1 is should should we have an experimentation controller, or should we have an autoscaling controller that's essentially that's what you just described, but can be can be more than just for experimentation can be used for auto scaling as well. I know that I'm not trying to replace the functionality that exists And in Kubernetes already. I'm just maybe it's more of just a nomenclature. Distinction. Because maybe down the line, we wanna use that same control for more than just extra potentially. So that's one common Another comment is that I think this all this logic should also be replaceable by the employer. We should be able to intercept all of this stuff and deployer should be able to provide their own logic. If that's what they want to do. And There's something else, but I can't remember right now. Oh, I've continue thinking, and I'll let you know.

---

## Claude:

Both points are good and consistent with the broader design principles we've been discussing.

---

## On Naming: PoolScalingController

You're right that calling it an ExperimentController is too narrow. The thing we described — continuously observing demand signals, deriving target pool sizes, managing pool lifecycle phases — is genuinely general-purpose autoscaling logic that happens to handle experiments as one of its inputs. Renaming it `PoolScalingController` (or `PoolOrchestrationController`) reflects what it actually does: it's responsible for the *desired state* of every pool's warm count, based on whatever signals are relevant. Experiments are one signal. Time-of-day schedules (which already exist in the design) are another. Raw demand metrics are another. A future "predictive scaling based on calendar events" would be another. All of these are just different inputs to the same controller loop.

This also cleanly separates responsibilities: the `WarmPoolController` manages individual pod lifecycle (create, claim, drain, scrub, terminate). The `PoolScalingController` manages the *desired configuration* of pools (what `minWarm` should be right now, whether a variant pool should exist at all). The existing `scalePolicy` on `AgentPool` essentially becomes the configuration surface that the `PoolScalingController` reads and writes, rather than logic buried in the `WarmPoolController` itself. That's actually a cleaner separation than what the current design has.

---

## On Replaceability: Same Pattern as Everything Else

This follows directly from the hooks-and-defaults principle. The `PoolScalingController` exposes a `PoolScalingStrategy` interface — the thing that receives demand signals and experiment state and returns a desired pool configuration. Lenny ships a default implementation with the demand-proportional formula, ramp logic, and health-based rollback. A deployer who wants to run their own multi-armed bandit, use Kubernetes KEDA as the signal source, integrate with their internal capacity planning system, or implement cost-aware scaling based on spot instance pricing can replace the strategy entirely by implementing the interface and pointing the controller at their implementation. The controller still manages pool lifecycle; only the "what should this pool look like right now" decision is delegated to the custom strategy.

Take your time on the third point — happy to continue when you're ready.

---

## Human (Mar 27, 2026, 1:55 PM):

Great. So In terms of configuration, and this is a general question around Lenny. Right now, a lot of it is, hard coded in configuration files. I know that there's an admin API, but I think that Many configuration levers should be editable via an admin API. And, therefore, should be like like, the system should pick them up dynamically if they change. Not necessarily everything. I guess, they're they're possibly things that should not be changed via an an admin API. And and rightly belong in, configuration files. I don't know what those would be, and I don't exactly know what the criteria for deciding which ones belong where as but in in general, I think that there's a room for moving stuff to an API. And maybe down the line as part of a separate project provide a UI, web portal on top of it or CLI application. To to manage, Lenny. As I said, this would be separate. From this project and would be done on the line, but that's sort of how I'm envisioning it. Can you come up with a proposal, for where all the things that belong in an API That's And if introducing that API would require any changes to the architecture,

---

## Claude:

This is a good structural question worth thinking through carefully before answering, because the criteria for "API vs config file" aren't obvious.

---

## The Right Mental Model: Operational Plane vs Bootstrap Plane

The distinction isn't really about what's "important" or "sensitive" — it's about *when* the value is needed and *who* changes it during the system's lifetime. That gives two clear categories:

**Bootstrap plane** — things the system needs to start up at all, or that require a pod/process restart to take effect. These belong in config files or Helm values because they're set before Lenny is running, and changing them is an operational event (a deploy), not a runtime action. Examples: database connection strings, KMS key ARNs, cluster name, TLS certificate paths, whether dev mode is enabled, which Kubernetes namespace to operate in, the API server address. These are infrastructure topology values that a running system can't meaningfully change about itself.

**Operational plane** — things that control Lenny's *behavior* while it's running, and that a tenant admin or platform admin needs to adjust without triggering a deployment. These belong in the database (exposed via admin API) and should be reloaded dynamically. Examples: pool configurations, rate limits, quota settings, experiment definitions, connector registrations, credential pools, egress profiles, runtime type registrations, user roles, billing event sink URLs.

A useful rule of thumb: if changing the value requires restarting a pod or applying a Helm chart, it's bootstrap. If it represents a decision a human operator makes during normal operations — tuning, configuration, onboarding a new customer, running an experiment — it's operational and belongs in the API.

---

## What Should Move to the Admin API

Going through the design systematically:

**Runtime Registry** — today `RuntimeType` definitions are Helm values. These should be live-editable: `POST /v1/admin/runtimes`, `PUT /v1/admin/runtimes/{name}`, `DELETE /v1/admin/runtimes/{name}`. Adding a new runtime version or updating a runtime's capability flags (e.g., enabling `checkpoint: true`) shouldn't require a Helm deploy. The gateway reloads the registry from the database on a short TTL cache (say, 30 seconds) rather than at startup only.

**Pool Configuration** — `AgentPool` CRDs today encode minWarm, maxWarm, isolation profile, resource class, and scale policy. The scaling values and scale policies should be live-editable via API since the PoolScalingController will be continuously reading them. The isolation profile and resource class are closer to infrastructure topology and could reasonably stay in CRDs, but even these benefit from API editability if the team decides to change isolation levels for a pool without a full redeploy.

**Credential Pools** — the structure of which credential pools exist, their assignment strategies, and fallback chains should be fully API-manageable. The actual secrets (API keys, IAM role ARNs) stay in Kubernetes Secrets or KMS by reference — the admin API manipulates the pool metadata, not the credential material itself.

**Connector Registry** — connector definitions (external MCP server URLs, OAuth config, expected domains) should be live-editable. Today they appear to be Helm config. Onboarding a new connector for a tenant is a normal operational activity that shouldn't require a deploy.

**Experiment Definitions** — entirely API-driven, as we just designed.

**Quota and Rate Limit Configuration** — per-tenant and per-user limits should be live-editable. This is a routine operational activity (upgrading a customer's plan, applying emergency throttling). Currently Section 11.2 mentions configuring via "admin API or Helm values" — the Helm path should be deprecated in favor of API-only, with Helm only setting system-wide defaults.

**Tenant Management** — creating tenants, setting tenant-level quotas, managing user-to-role mappings. These are inherently API-driven and already partially designed in Section 10.2, but the full CRUD surface isn't defined.

**Egress Profiles** — custom egress profiles and their CIDR lists should be API-manageable, not just Helm values. An operator adding an approved external endpoint shouldn't need a redeploy.

**Callback / Webhook Configuration** — billing event sink URLs, HMAC secrets. These are already designed as API-configurable in Section 14 but deserve explicit placement here.

**PoolScalingStrategy Configuration** — scaling policy parameters (demand window, safety factor, ramp settings) per pool, and which scaling strategy implementation is active.

**Memory Store Configuration** — which backend implementation, connection parameters (by reference to secrets), and per-tenant memory retention policies.

**Guardrail and Cache Policy Configuration** — which interceptor implementations are active per pool, and their parameters.

---

## What Stays in Config Files / Helm

**Infrastructure topology** — database URLs, Redis addresses, MinIO endpoint, KMS key IDs, cluster name, namespace assignments. These are set at deploy time and changing them implies migrating data or restarting services.

**Bootstrap identity** — the gateway's own service account, certificate paths, KMS signing key references. The gateway needs these before it can authenticate anything.

**Dev mode flags** — `LENNY_DEV_MODE`, `LENNY_DEV_TLS`. These affect fundamental behavior and should never be changeable at runtime for security reasons.

**Default values** — system-wide defaults for quota limits, rate limits, session ages, checkpoint TTLs. These are the "out of the box" values that individual tenants can override via API. Having them in Helm means deployers can set sensible platform-wide defaults without needing API calls at first boot.

**Kubernetes-specific resource configuration** — RuntimeClass definitions, node pool labels, PodDisruptionBudgets, NetworkPolicy templates. These are Kubernetes object definitions, not Lenny configuration, and belong in Helm templates.

---

## Architectural Changes Needed

A few things need to change in the current design to support this properly:

**Configuration storage in Postgres, not just memory.** The gateway today loads runtime registries, pool configurations, and quota settings at startup. To make them live-reloadable, these need to be stored in Postgres (not just etcd via CRDs) and the gateway needs a refresh mechanism. The most pragmatic approach is a short TTL in-memory cache (30-60 seconds) backed by Postgres reads — the gateway gets near-real-time config updates without polling on every request, and without needing a pub/sub mechanism for most settings. For settings that need immediate propagation (like revoking a credential pool or pausing an experiment), a Redis pub/sub notification can trigger an immediate cache invalidation.

**CRDs become derived state, not primary state.** Today `AgentPool` CRDs are the primary configuration record for pools. In the new model, the primary record is in Postgres (managed via admin API), and the PoolScalingController reconciles CRDs *from* the database rather than the other way around. The CRDs become the controller's working state, not the authoritative config. This is a meaningful change — it means `kubectl edit agentpool` is no longer the right way to change pool settings, and documentation needs to make that clear. The benefit is that pool configuration participates in Lenny's existing RBAC, audit logging, and tenant scoping rather than being outside it in Kubernetes RBAC.

**Admin API needs a full schema.** The current design sketches several admin endpoints but doesn't define them consistently. The new scope is larger — it covers runtime management, pool management, tenant management, experiment management, connector management, credential pool management, and scaling policy management. This is a substantial API surface that deserves a dedicated section in the technical spec equivalent to Section 15.1 (REST API) in completeness.

**Audit trail for admin actions.** Any change to operational configuration via the admin API needs to flow through the existing audit logging system (Section 11.7). Config changes are as auditable as session operations — a tenant admin changing their rate limits, a platform admin onboarding a new runtime, a deployer pausing an experiment all need to appear in the audit log with actor, timestamp, before/after state.

---

## On the UI and CLI Comment

One thing worth noting: if the admin API is well-designed, the UI and CLI become thin clients with zero business logic. The CLI (`lenny-ctl`) just wraps admin API calls. The web portal renders the same API responses. This means the project doesn't need to build the UI or CLI to get value from the API — any team can build their own management tooling on top of it, which is exactly what you'd want for an OSS project. The API design is the product; the official UI is just one client of it.

The main design constraint this imposes is that the admin API needs to be complete enough to perform every operational action — if there's any operation that can only be done by editing a Helm value and redeploying, that's a gap. Completeness is the goal, even if some operations are rarely needed.

---

## Human (Mar 27, 2026, 2:23 PM):

Alright. This makes sense. Yes. And definitely, I appreciate implement it. And the admin you the admin API should be very well designed then others can build their own thin layers on top of it.

---

## Claude:

Agreed. A few concrete principles worth stating explicitly in the spec for the admin API, since they constrain the design in ways that matter:

**Consistency with the client-facing API.** The admin API and the session API should share the same error taxonomy, the same pagination conventions, the same authentication model (OIDC with role claims), and the same versioning strategy. A developer who has worked with one should feel immediately at home in the other. Right now the design has two somewhat disconnected surfaces — that should be resolved into one coherent API product.

**Full resource representation with `etag` or `resourceVersion`.** Every resource the admin API manages should be fetchable as a complete document and updatable via conditional PUT (optimistic locking). This makes it straightforward to build safe read-modify-write operations in a CLI or UI without racing. It also makes the API exportable — `lenny-ctl export` can dump the entire configuration state as a set of JSON/YAML documents that can be version-controlled, diffed, and applied to another deployment.

**Dry-run semantics on mutations.** For operations with significant side effects — changing pool isolation profiles, pausing a running experiment, modifying quota limits for a live tenant — a `?dryRun=true` query parameter should validate the request and return what *would* happen without committing. This is how `kubectl apply --dry-run` works and it's extremely valuable for a management CLI where operators want to review consequences before committing.

**OpenAPI spec generated from code, not written by hand.** The admin API spec should be the source of truth for the CLI and any future UI. If it's hand-written it will drift. Generating it from the Go handler definitions ensures the spec always matches the implementation and any third-party building a client gets accurate documentation.

Those four constraints together mean that anyone building a thin layer on top — whether a web portal, a Terraform provider, a GitHub Actions workflow, or an internal tooling script — gets a solid, predictable foundation to work from.

---

## Human (Mar 27, 2026, 2:25 PM):

One thing, I'm I'm just now realizing or remembering in the context of, being able to provide lightweight tasks and the decisions we made were that Plenty of should support Reusable pods so that we have to shut down the pod, every time there's a new task. And then support for graph Asian. Inside 1. Alright. And exposing all the different APIs and permit has to provide more visibility into what's happening within inside One Pod. And so those make sense, and I'm happy with those decisions. I'm wondering if there's a place for Run times That's are able to search multiple requests in parallel. Where you have concurrency is for the one For pot. And I don't think that this has been addressed so what are your thoughts on it?

---

## Claude:

This hasn't been addressed and it's worth thinking through carefully because it's a different axis from the other two execution modes.

---

## What the Three Modes Cover and What They Don't

The decisions so far cover three distinct things:

- **Session mode** (current): one session, one pod, one user interaction at a time
- **Task pod mode**: one pod, sequential tasks with scrub between them, still one active task at a time
- **Graph pod mode**: one pod, one session, the runtime manages internal steps — but still serving one logical "job"

What none of these address is a pod that can handle multiple independent requests *simultaneously* — true intra-pod concurrency. This is a genuinely separate dimension.

---

## Where This Is Actually Needed

The clearest use case is stateless or near-stateless high-throughput runtimes. Consider a "document classification" runtime or a "code review" runtime. These are:

- Short-lived (seconds, not minutes)
- Stateless — each request is independent, no shared workspace needed between them
- Embarrassingly parallel — there's no reason request B has to wait for request A to finish

For these, the overhead of even a task pod (which still has one active task at a time) is unnecessary. The runtime could serve 10 concurrent classifications from a single pod if Lenny's model allowed it.

LangGraph's deployment model actually supports this already — a single LangGraph Server process handles multiple concurrent threads. Modal's worker model does too: `max_concurrent_inputs` on a function. Temporal workers handle multiple activity executions simultaneously per process. The pattern is well-established outside Lenny.

---

## The Security Tension

This is where Lenny's design philosophy creates real friction. The one-session-one-pod invariant exists precisely because shared process state creates cross-session leakage vectors — residual memory, shared caches, timing side-channels. Allowing concurrency within a pod means two requests from different users (or different tenants) are running in the same process simultaneously.

There are two ways to handle this:

**Single-tenant concurrency only.** A pod in concurrent mode only accepts requests from the same `user_id` and `tenant_id`. This preserves tenant isolation while allowing a single user's parallel tasks to share a pod. An agent that fans out 10 parallel sub-analyses for the same user doesn't need 10 separate pods; one concurrent pod handles all 10. This is a significant resource optimization with acceptable security properties.

**Explicit runtime isolation declaration.** The `RuntimeType` declares `concurrencyModel: isolated` if the runtime guarantees that concurrent requests are fully isolated in memory and state (e.g., each request runs in a separate goroutine with no shared mutable state). The gateway trusts this declaration at the same level it trusts other runtime capability declarations — it's the runtime author's responsibility to uphold it, and the pod sandbox is still the hard security boundary.

The second option is more flexible but places more trust in the runtime. The first is more conservative and appropriate for a v1.

---

## How It Would Work in Practice

A new `executionMode: concurrent` on `AgentPool` (alongside `session` and `task`). The pool's pods run a long-lived process that maintains a request slot table — up to `maxConcurrent` active requests at any moment. The gateway routes incoming task requests to a claimed concurrent pod rather than claiming a new pod per request, as long as the pod has available slots.

The adapter's platform MCP server handles this naturally: each concurrent request is a separate MCP session connection to the same adapter process. The adapter multiplexes them. Each gets its own output channel, its own credential scope (if needed), and its own lifecycle tracking. The gateway sees N logical tasks all associated with one physical pod.

The `OutputPart` model we designed handles this cleanly too — each concurrent request has its own task ID, and streaming output is tagged by task ID so the gateway can route responses to the right client.

---

## What Stays the Same

The delegation lease model, budget enforcement, and audit logging all work at the task level regardless of what pod the task runs on. A concurrent pod running 10 tasks still tracks 10 separate task records in the session store, each with their own token budgets and lifecycle. From the gateway's perspective, the abstraction doesn't change — it still manages tasks. The concurrency is an internal implementation detail of how the pod services those tasks.

---

## The Recommendation

Add `concurrent` as a third `executionMode` alongside `session` and `task`, with two constraints for v1:

1. Concurrent pods only accept requests scoped to the same `tenant_id` (single-tenant concurrency, preserving isolation guarantees)
2. The `RuntimeType` must declare `maxConcurrent` and the runtime author accepts responsibility for request isolation within the process

This unlocks the high-throughput stateless workload pattern — document processing pipelines, batch evaluation runs, fan-out classification tasks — without compromising the security model. It's opt-in, requires explicit capability declaration, and works within the existing task tracking and billing model without architectural changes.

The `executionMode` enum on `AgentPool` now covers the full spectrum: `session` for interactive long-running work, `task` for sequential reuse with scrub, `graph` for internal multi-step execution, and `concurrent` for high-throughput parallel handling within a single pod.

---

## Human (Mar 27, 2026, 3:31 PM):

Does any of this change if we use, Kubernetes sigs sandbox?

---

## Claude:

Good question to ask now before the design gets further along. Let me think through what `kubernetes-sigs/agent-sandbox` actually provides and where it creates constraints.

---

## What Agent-Sandbox Gives You and What It Doesn't

The agent-sandbox project provides the `Sandbox`, `SandboxTemplate`, `SandboxWarmPool`, and `SandboxClaim` primitives. What it manages is a single stateful singleton pod — one sandbox instance, one stable identity, one lifecycle. That's its explicit design goal, stated in the project docs: *"managing workloads that require the characteristics of a long-running, stateful, singleton container."*

The word *singleton* is doing a lot of work here. Agent-sandbox is designed around the assumption that each sandbox serves one thing at a time. The `SandboxClaim` mechanism allocates one sandbox per claim. There is no concept in the current spec of a sandbox serving multiple concurrent claims simultaneously.

---

## How Each Execution Mode Maps

**Session mode** — perfect fit. One session, one sandbox, one claim. This is exactly what agent-sandbox was designed for. No changes.

**Task pod mode** — workable with some design. The pod survives task completion and gets scrubbed before the next task. Agent-sandbox's lifecycle model supports pausing and resuming, which is close. The question is whether the scrub-between-tasks concept maps onto agent-sandbox's lifecycle hooks cleanly. The `SandboxWarmPool` replenishes by creating fresh sandboxes after claims are released, so the naive mapping would be: release the claim after each task, let the pool replenish. But that defeats the purpose of task pod mode — you'd be creating new sandboxes rather than reusing the same one. Lenny's task pod mode requires that a single sandbox survives and gets reused, which means holding the claim across multiple task executions. This is possible — the claim stays open, the gateway sends sequential tasks to the same sandbox — but it's outside what agent-sandbox's claim model is designed for. The scrub protocol needs to be implemented entirely in Lenny's adapter layer, with agent-sandbox unaware of it. This is fine architecturally but worth noting explicitly.

**Graph pod mode** — no impact at all. The runtime manages internal steps within a single session. From agent-sandbox's perspective it's just a normal long-lived sandbox. Agent-sandbox doesn't know or care what the runtime does internally.

**Concurrent mode** — this is where the real tension is. Agent-sandbox's `SandboxClaim` is a 1:1 allocation mechanism. There is no concept of a sandbox accepting multiple simultaneous claims from multiple tasks. If Lenny wanted to route two concurrent requests to the same sandbox, it would need to hold a single claim and internally multiplex requests to that sandbox — the concurrency is entirely invisible to agent-sandbox. This is actually fine: agent-sandbox manages the pod lifecycle, and Lenny's adapter layer manages the request multiplexing within the pod. Agent-sandbox doesn't need to know about concurrency at all.

---

## The Practical Implication

For session mode and graph mode, agent-sandbox works as-is and Lenny gains all the upstream benefits — community maintenance, Pod Snapshots on GKE for checkpointing, the warm pool CRD design. For task mode and concurrent mode, agent-sandbox remains the infrastructure layer but the execution semantics live entirely in Lenny's adapter and gateway — agent-sandbox just sees a long-lived claimed sandbox that Lenny happens to use for multiple tasks or multiple concurrent requests.

The one thing to watch is whether a future version of agent-sandbox formalizes a "multi-task" or "concurrent" lifecycle model. If it does, Lenny should align with it. If it doesn't, Lenny's approach of handling these modes transparently above the agent-sandbox layer is clean and doesn't require any forking or modification of upstream code.

So the short answer: agent-sandbox doesn't change the decisions, but it does clarify that task mode and concurrent mode are Lenny-layer concerns that agent-sandbox simply hosts, rather than capabilities Lenny can get from agent-sandbox itself.

---

## Human (Mar 27, 2026, 3:38 PM):

So here's a related question. For the concurrency mode, the way so far we've talked about it is we would define the maximum number the maximum concurrency per runtime. And if that max concurrency is greater than 1, then That's it. Bye. I'm This sounds this sounds fine. But I wanna double check that we're not using an anti pattern here. Because Kubernetes is supposed to be able to handle load across multiple different pods and auto scale if needed. And so Setting Usually, that's not as far as I know, it's not done based on the configuration. It's not based on the max concurrency. Like, the configuration is based on the number of minimum and maximum parts. And then everything just works based on autoscaling works based on CPU utilization or whatever other metrics. And so I'm just wondering, like, if this model that we're talking about here really fits nicely with Kubernetes or we should think of something else.

---

## Claude:

You're identifying a real tension worth resolving before it gets baked into the design.

---

## The Pattern Concern Is Legitimate

The standard Kubernetes approach to handling parallel load is horizontal scaling — more pods, each handling one request, with HPA scaling based on CPU, memory, or custom metrics. Configuring intra-pod concurrency as a first-class concept does feel like it's fighting that model rather than working with it.

But it's not quite an anti-pattern — it's actually how many production systems work. A web server runs N replicas, each with a thread pool of M workers. Total capacity is N×M. The horizontal dimension (N pods) and the vertical dimension (M concurrent slots per pod) are orthogonal. Neither replaces the other. The question is whether both dimensions are genuinely needed for Lenny's use cases, and whether agent-sandbox's model accommodates it cleanly.

---

## Does Warm Pool + Task Pod Mode Already Solve This?

Worth asking honestly: what problem does concurrent mode solve that task pod mode plus a well-sized warm pool doesn't already address?

The original motivation was: a parent agent fans out 10 parallel sub-analyses, and instead of claiming 10 warm pods, a single pod handles all 10 concurrently. The implicit assumption is that 10 warm pods are expensive or slow to claim. But with agent-sandbox and a properly sized warm pool, claiming 10 warm pods takes milliseconds — pod scheduling and container creation are already done, workspace materialization is the only hot-path cost.

For purely stateless short tasks with no per-task workspace, even workspace materialization is trivial. In that scenario the warm pool handles fan-out naturally: 10 tasks claim 10 warm pods, run in parallel across the cluster, release back to the pool. Kubernetes's horizontal scaling works exactly as intended. The PoolScalingController sizes the pool based on observed demand, including demand from fan-out delegation patterns.

So for most of the workloads we discussed as concurrent mode candidates — document classification, code review, batch evaluation — the combination of task pod mode and a well-sized warm pool is probably sufficient and more idiomatic.

---

## When Concurrent Mode Is Actually Justified

There is one case where intra-pod concurrency is genuinely the right answer: when the pod has expensive shared in-memory state that multiple concurrent requests all benefit from, and that state is too expensive to load per-pod. The clearest example is a runtime that loads a large model into memory. If loading that model takes 30 seconds and uses 8GB of GPU memory, running 10 replicas is both slow and expensive. Running one replica that handles 10 concurrent requests while sharing the loaded model is the correct pattern — it's exactly how LLM inference servers like vLLM and TGI work.

For agent runtimes specifically, this case arises if the runtime embeds an LLM locally (a self-hosted model) rather than calling an external API. In that case, the loaded model weights are the shared expensive state, and concurrent requests amortize the loading cost across many tasks.

For runtimes that call external LLM APIs (Anthropic, Bedrock, Vertex), there's no expensive shared in-memory state. Each request makes independent API calls. Horizontal scaling with the warm pool is the right model.

---

## The Revised Recommendation

Concurrent mode should be kept but scoped more narrowly: it's for runtimes with genuinely expensive shared state (typically self-hosted models), not a general-purpose parallel execution mechanism. The `RuntimeType` declaration of `maxConcurrent` is a signal that the runtime manages an expensive shared resource internally, and the pool should be sized accordingly.

For the horizontal dimension, the PoolScalingController drives pod count based on observed slot utilization — if pods are consistently at `maxConcurrent` capacity, it signals that more pods are needed, not more concurrency per pod. The HPA equivalent for Lenny is the PoolScalingController responding to the metric `active_slots / (pod_count × maxConcurrent)`.

This means concurrent mode and horizontal scaling are both active simultaneously: you might have 5 pods each handling 8 concurrent requests, for 40 total concurrent capacity. The PoolScalingController adds more pods when utilization is high, and reduces pods when it's low. The `maxConcurrent` is a fixed property of the runtime, not a scaling lever.

For fan-out workloads with external API calls — the case we originally described — the answer is just more task pods in the warm pool. That's idiomatic Kubernetes, it's what agent-sandbox is designed for, and it avoids the complexity of intra-pod multiplexing entirely.

---

## Human (Mar 27, 2026, 3:44 PM):

Okay. I wasn't questioning the concurrent mode. I think we that's a that's a must have. I was just Yeah. That that's fine. I I was just wondering how much the gateway should do versus just standard load balancing in Kubernetes. Obviously, the gateway still needs to be in the middle. But right now, if I understand correctly, like, it would track to make sure that A single pod is not receiving more than its max concurrent, request. And and that's that's fine. I I would So that's fine. I was just wondering if if there's something here that gateway might be doing or that we're doing that in that specific scenario in concurrent mode, maybe it should not be done. It should be delegated to Kubernetes. I truly have no idea, so I'm just asking.

---

## Claude:

Good question, and worth being precise about because there's a real architectural simplification available here.

---

## Why Session Mode Requires Gateway Routing

In session mode and task mode, the gateway *must* track which specific pod handles a given session. The session has durable state — workspace files, credential lease, event stream cursor, checkpoint references — all tied to a specific pod. If the client reconnects, the gateway needs to route back to the same pod. There's no way around this; it's inherent to stateful session management.

---

## Why Concurrent Mode Is Fundamentally Different

Concurrent mode tasks are, by definition, meant to be stateless or near-stateless. A document classification request doesn't have a workspace, doesn't require session affinity, and doesn't need to return to a specific pod. Any pod in the pool can handle any request. This is exactly the workload that Kubernetes service routing was designed for.

So the gateway's per-pod slot tracking is actually the wrong model for concurrent mode. It's importing the session mode's stateful routing logic into a scenario where it isn't needed.

---

## The Cleaner Split

The adapter inside each concurrent pod maintains its own internal semaphore — a simple counter up to `maxConcurrent`. When full, the pod returns a `503 with Retry-After` header on the adapter's local endpoint. The Kubernetes readiness probe can reflect this: a pod at full capacity marks itself not-ready, which removes it from the Service's endpoint set automatically. Kubernetes stops routing new requests to it until a slot frees up and it marks itself ready again.

The gateway's role in concurrent mode becomes:

- Policy enforcement, credential injection, audit logging — same as always
- Routing *through* a Kubernetes Service for the pool, not to a specific pod
- Handling 503 retries with exponential backoff when all pods are temporarily at capacity
- Recording which pod handled the task *after the fact* from the response, for audit and billing — not as a pre-routing decision

The PoolScalingController watches the utilization metric across the pool — `active_slots / (pod_count × maxConcurrent)` — and adds or removes pods accordingly. This is pure Kubernetes HPA territory, just with a custom metric.

---

## Net Result

The gateway does less work in concurrent mode, not more. No per-pod slot state in Redis, no pre-routing decisions based on capacity. Kubernetes service load balancing handles the distribution, pod readiness probes handle capacity signaling, and the PoolScalingController handles horizontal scaling. The gateway enforces policy and records what happened, which is exactly the right division.

This is the more idiomatic Kubernetes model, and it's what you were sensing might be possible. For concurrent mode specifically, the answer to "should Kubernetes handle this?" is yes, mostly — with the gateway staying in the critical path only for the things it uniquely owns.

---

## Human (Mar 27, 2026, 4:40 PM):

Okay. Yeah. I'm not sure about this last suggestion I feel that there might be different scenarios for the concurrent mode. I can see. Scenarios where maybe there is some more It's not where there's a little bit of overhead where you need a workspace and maybe files need to be passed, and so following the same semantics that we have today, Might be best. And then all scenarios where it's truly stateless, it just basically a a a simple prompt that gets sent and a very simple HTTP West and that's all that's needed. So as long as there's no need to set up the the the workspace, then I can then yes. Like, with the this last model you're proposing, does make sense. So I'm not sure if this is, overengineering yet, but I feel that maybe, you know, at least 2 different types of concurrent mode. That we should worry about, and and maybe one uses approach you just recommended, and the other one uses the original approach. Just an idea.

---

## Claude:

That's a clean distinction and not overengineering — it maps onto a real difference in what the pod needs to do before it can serve a request.

The two modes are essentially:

**Concurrent-stateless**: no workspace, no file materialization, no setup commands. The pod is permanently ready and accepts requests directly. Kubernetes service routing handles distribution, readiness probes signal capacity, the gateway enforces policy and passes through. This is the pure HTTP API model — think of it like a serverless function with concurrency.

**Concurrent-workspace**: each concurrent slot still gets its own workspace materialized before the request starts, but multiple slots run in parallel within the same pod. The gateway still orchestrates the per-slot setup (upload files, materialize workspace, run setup commands) before handing the request to the runtime, same semantics as today. The difference from task pod mode is that multiple of these can be in-flight simultaneously rather than sequentially.

The distinction could be surfaced as a `concurrencyStyle` field on the `RuntimeType` — `stateless` or `workspace-per-slot`. Or more simply, it falls out naturally from whether the runtime declares workspace capabilities at all: if `PrepareWorkspace` and `FinalizeWorkspace` are in the runtime's declared capabilities, the gateway uses the full workspace setup path per slot. If not, it routes directly.

The second variant — concurrent-workspace — is actually a meaningful capability that doesn't exist elsewhere. It's the model where a self-hosted model pod can serve multiple users simultaneously, each with their own isolated workspace context, without requiring a separate pod per user. Worth having even if it's more complex to implement, because the alternative (one pod per concurrent user) defeats the purpose of hosting a shared expensive model.

Worth flagging though: concurrent-workspace raises a security question that stateless doesn't. Multiple workspaces on the same pod means the isolation boundary between slots is process-level and filesystem-level within the same container, not kernel-level between containers. That's weaker than the pod-per-session model. The design should require an explicit deployer acknowledgment for this mode — similar to how `runc` requires `allowStandardIsolation: true` — so deployers consciously accept the trade-off rather than stumbling into it.

---

## Human (Mar 27, 2026, 5:08 PM):

Hey. I feel maybe we're losing track of the conversation. Go back to everything we discussed about, concurrency and, Uh-huh. Parts that are reused, for different tasks sequentially And I started to put it all together. I wanna make sure that this mode that is HTTP only, no workspace, etcetera, really makes sense.

---

## Claude:

Good instinct to pause and reassemble. Let me think through all the modes together and then critically examine the stateless one.

---

## The Four Modes We Have

**Session mode** — the default. One pod, one session, one user interaction. Pod is terminated after session completes. Full workspace, credential leasing, the works. One-session-one-pod invariant. This is Lenny's core.

**Task pod mode** — one pod survives across multiple sequential tasks. Scrub between tasks clears workspace and session state. Pod stays alive, saving the startup cost. Still one active task at a time. The gateway still orchestrates per-task: workspace materialization, credential assignment, setup commands.

**Graph pod mode** — one pod, one session, but the runtime internally manages a multi-step graph. Lenny sees a single long-lived session. The runtime exposes internal step visibility via the optional observability protocol. No change to the gateway's routing model.

**Concurrent mode** — one pod, multiple simultaneous tasks. This is what we were just discussing.

---

## The Critical Look at Stateless Concurrent

The stateless variant — no workspace, no file materialization, pure HTTP prompt-in, response-out — raises a question worth asking directly: **what is Lenny actually doing for this mode that justifies it being a Lenny execution mode at all?**

The gateway still enforces auth, policy, billing, and rate limits. That's valuable. But the pod management side — the warm pool, agent-sandbox, the adapter contract, the lifecycle channel — all of that exists to manage stateful workloads. For a truly stateless runtime that just receives a prompt and calls an LLM API, agent-sandbox is the wrong primitive. Agent-sandbox is designed for singleton stateful workloads. A stateless concurrent runtime is just a regular Kubernetes Deployment behind a Service. Using agent-sandbox for it adds overhead without benefit.

And actually, the gateway already has a mechanism for calling external services: connectors. A stateless runtime that exposes a simple HTTP API could reasonably be registered as a connector — the gateway mediates the call, enforces policy, handles credentials, and records billing events. The runtime doesn't need to be a Lenny-managed pod at all.

---

## What Concurrent Mode Should Actually Mean

Stripping away the stateless variant, concurrent mode makes sense in one specific scenario: a pod with **expensive shared in-memory state** (a loaded local model, a large shared index, an expensive initialized context) that multiple tasks can share simultaneously. Each concurrent task still has its own workspace slot — the gateway still orchestrates per-task setup — but multiple slots run in parallel within the same pod because spinning up separate pods would mean loading the expensive state N times.

This is the concurrent-workspace variant you described. It's genuinely different from task pod mode (sequential) and genuinely useful (amortizing expensive initialization). It requires the gateway to track per-slot state the same way it does today for sessions, just with multiple active slots per pod.

The purely stateless variant doesn't really belong here. If a runtime needs no workspace and no state, it should either be a connector (external service called through the gateway) or a very lightweight task pod with near-zero setup cost — which the task pod mode already handles perfectly well once the warm pool is properly sized.

---

## The Cleaner Set of Four

So the execution modes look like this:

| Mode | Pod reuse | Concurrency | Workspace per execution | Expensive shared state |
|---|---|---|---|---|
| Session | No | 1 | Yes | No |
| Task pod | Yes, sequential | 1 | Yes (with scrub) | No |
| Graph pod | Yes, internal | 1 logical | Internal to runtime | No |
| Concurrent | Yes, parallel | N | Yes (per slot) | Yes — that's the reason |

And truly stateless workloads — the pure HTTP prompt-response case — are served by registering the runtime as a connector rather than as a Lenny-managed pod. The gateway mediates the call, enforces policy, handles auth. No pod management overhead needed.

Does this feel like the right simplification?

---

## Human (Mar 27, 2026, 5:15 PM):

Yes. It does. Although, I would I would I think that the session mode and the graph mode. Essentially the same thing. It just with the the runtime does internally might be different, and it may use or it may publish additional information but it's essentially the same. Right?

---

## Claude:

Yes, exactly right. Graph mode isn't really a distinct execution mode from Lenny's perspective — it's the same one-pod-one-session model. The only difference is what the runtime does internally and what observability information it optionally publishes through the trace protocol.

From the gateway's point of view, a graph-mode session and a regular session look identical: one pod claimed, one session lifecycle, one workspace, one credential lease. The gateway doesn't know or care whether the runtime is executing a straight prompt-response loop or traversing a 12-node LangGraph graph internally.

The distinction only matters to the runtime author and to whatever tooling consumes the trace output. A runtime running a graph can optionally emit `{type: "trace", span: "node_2", ...}` messages through the observability channel, which flow through to LangSmith or whatever eval tooling is connected. A runtime that doesn't emit those messages just looks like a regular session to everything downstream.

So the cleaner set is actually three modes:

| Mode | What Lenny manages | What's different |
|---|---|---|
| **Session** | One pod, one session lifecycle | Default. Runtime is opaque. Graph-aware runtimes are just sessions with richer trace output. |
| **Task pod** | One pod, sequential sessions with scrub | Pod survives task completion. Gateway re-materializes workspace per task. |
| **Concurrent** | One pod, N parallel task slots | For runtimes with expensive shared state. Gateway tracks per-slot lifecycle. |

Graph mode is just a session where the runtime happens to be graph-aware and publishes internal step information. It's a runtime capability, not a platform execution mode. The `RuntimeType` can declare `graphObservability: true` to indicate the runtime emits trace spans, but that's a documentation and tooling hint, not a routing or lifecycle distinction.

---

## Human (Mar 27, 2026, 5:20 PM):

Great. Thanks. That makes sense. Changing topics. I think, There should be well, I don't know. There should be. I'm just thinking out loud right now, but Ideally, we we should allow users of Lenny To Potentially, Create new run times. And I don't know if new run time is a the right way of thinking about it, but, thinking of the line graph example where a user may define line graph agents, and I could envision a scenario in which There is a runtime and a setup on top of Lenny that would Take dot Land Graph definition automatically deployed onto Lining. And and then make it available in production for people to call I don't know if that's if that's one if that can be accomplished with one underlying runtime and then the deployment of the actual graph is just done at the application level. But it's something that I was thinking about. I don't know if you have any opinions on that.

---

## Claude:

This is a good thing to think about and the answer depends on how much of the complexity you want Lenny to own versus leave to the deployer.

---

## The Two Models

**Model A: One generic LangGraph runtime, many deployed graphs.**

A single `RuntimeType` called `langgraph-runtime` is registered once. It's a pod that knows how to run any LangGraph graph definition. When a user wants to deploy their specific graph, they don't register a new runtime — they upload their graph definition as part of the workspace at session creation time. The runtime starts up, loads the graph from `/workspace/current/graph.py`, and runs it. The graph definition is just another file in the workspace.

This is already fully supported by Lenny today. No changes needed. The `create_and_start_session` tool lets a client upload files and start in one call. A deployer ships a `langgraph-runtime` image, a user uploads their graph definition with each session, and it works.

The limitation is that every session needs to upload the graph definition. For a platform where many users run the same graph repeatedly, that's redundant. The `SessionTemplate` concept the critique calls for addresses this: a template pre-populates the workspace plan so users don't have to upload the same files every time.

**Model B: Each graph is its own registered runtime.**

A user defines a LangGraph graph, and there's a mechanism to package it as a new `RuntimeType` — essentially baking the graph definition into the container image. `POST /v1/admin/runtimes` with the graph definition produces a new runtime that can be warm-pooled, assigned its own pools, and called by name.

This is more like what you're imagining — the "deploy a graph to production" experience. It's closer to what LangGraph Platform / LangSmith Deployment does: you push from a git repo, it builds a deployment, you get an endpoint.

---

## Why Model A Is Probably Sufficient, With One Addition

Model A is simpler and already works. The gap is the "deploy once, call many times" experience. The thing that closes that gap isn't a new runtime registration flow — it's two features that either exist or are already planned:

**`SessionTemplate`** (already in the critique's must-have list): a named, stored workspace plan that pre-populates all the files a session needs. A user defines a `SessionTemplate` called `my-research-graph` that includes their graph definition file. Every session created from that template gets the graph without re-uploading it. This is the "deploy" step — register a template, not a runtime.

**Runtime versioning and image tagging**: if a user's graph has Python dependencies that need to be installed, the setup commands handle that. If those dependencies are heavy enough that setup time matters, they bake a custom image that extends the base `langgraph-runtime` image with their dependencies pre-installed, and register that as a new `RuntimeType`. That's a legitimate use of runtime registration, but it's the exception for dependency-heavy graphs, not the standard deployment flow.

---

## What This Means for the "Deploy a LangGraph Agent" Experience

The user flow would be:

1. Write a LangGraph graph definition (`agent.py`)
2. `POST /v1/admin/session-templates` with the graph file and a pointer to the `langgraph-runtime` runtime
3. Get back a template ID
4. Anyone can now `create_session(template: "my-research-graph")` and immediately get a running graph session
5. When the graph changes, update the template with the new file

This is clean, requires no new architectural concept, and gives users the "register once, run many times" experience without the complexity of building and pushing container images for every graph change.

---

## Where Model B Becomes Relevant

If Lenny wants to be a full application deployment platform — where users can deploy arbitrary Python applications, not just agent graphs, with their own dependencies, their own environment, their own scaling configurations — then Model B makes sense and the `POST /v1/admin/runtimes` API becomes a full deployment pipeline. But that's a much bigger scope and starts to overlap with what Fly Machines, Railway, and similar platforms do. It's probably not where Lenny should go for v1.

The right framing is: Lenny is a platform for running agents, not a general application deployment platform. `SessionTemplate` gives you the "deploy a graph" experience within that scope. Custom `RuntimeType` registration gives advanced users the ability to optimize for their specific dependencies. The combination covers the use case without scope creep.

---

## Human (Mar 27, 2026, 5:38 PM):

Model A is the correct one. We should not implement model B

---

## Claude:

Agreed. Model A keeps Lenny focused on what it's uniquely good at — running agents — rather than becoming a general application deployment platform.

The practical implication for the design is that `SessionTemplate` gets promoted from a "should-have" item in the critique's priority list to a core v1 primitive. It's the mechanism that makes Model A feel like a proper deployment experience rather than just "upload files every time." Without it, Model A works but feels raw. With it, the flow — define your graph, register a template, call it by name forever after — is clean enough that most users won't feel the absence of Model B at all.

The one other thing worth noting: the `publishedMetadata` mechanism we designed earlier pairs well with this. A `SessionTemplate` can have associated metadata — a description, an OpenAPI-style interface definition, an A2A agent card — making the deployed graph discoverable by other agents via `lenny/discover_agents`. So a team that deploys a research graph via a template can make it available for internal delegation by other agents, without any additional work beyond adding a metadata entry to the template. That closes the loop between the deployment model and the multi-agent discovery model cleanly.

---

## Human (Mar 27, 2026, 5:42 PM):

I like this

---

## Claude:

Good. We've covered a lot of ground in this conversation. When you're ready, it might be worth doing a final pass to compile everything into a clean, prioritized list of changes to the technical spec — separating what's v1 must-have from what's planned but not blocking. That would make a good handoff document for whoever is writing the actual spec updates.

---

## Human (Mar 29, 2026, 8:40 PM):

Yes please. Make sure not to miss anything. Also include clarifications that should be included in the technical spec for context (for example, how different changes may or may not be forward compatible with A2A).

---

## Claude:

This will be long — let me be thorough. I'll read back through the key sections of the project files to make sure I don't miss anything already in the design before layering in everything from our conversation.Now I have everything I need. This is the complete compilation.

---

# Lenny Technical Spec — Changes from Design Review

## How to Read This Document

Changes are organized into four tiers:

- **V1 Must-Have (Breaking)** — must be in v1 because retrofitting after the first community adapter ships is nearly impossible
- **V1 Must-Have (Feature)** — must ship with v1 to meet the stated goals
- **V1 Should-Have** — significant value, should ship with v1 but won't break compatibility if deferred
- **Planned / Post-V1** — document the intent now so data models accommodate it; implement later

Each item notes what section of the existing spec it affects and any forward-compatibility implications.

---

## Part 1: V1 Must-Have — Breaking Changes

These are changes where the wrong decision now produces a community contract that is nearly impossible to fix after adoption. They must be resolved before Phase 2 (the adapter spec ships).

---

### 1.1 Replace Custom CRDs With `kubernetes-sigs/agent-sandbox`

**What changes:** Replace `AgentPool`, `AgentPod`, and `AgentSession` CRDs and the WarmPoolController with the upstream `SandboxTemplate`, `SandboxWarmPool`, `SandboxClaim`, and `Sandbox` CRDs from `kubernetes-sigs/agent-sandbox` (launched as a formal Kubernetes SIG Apps subproject at KubeCon Atlanta, November 2025).

**Affects:** Sections 4.6, 6.1, 6.2, 6.3, Phase 1 and Phase 3 of the build sequence.

**Rationale:** Agent-sandbox solves the same problem Lenny was about to build: warm-pooled, isolated, singleton Kubernetes workloads for agent execution. It is on the path to becoming an upstream Kubernetes standard (same trajectory as Deployments and StatefulSets). Using it means Lenny stops maintaining a Kubernetes controller and gains upstream community maintenance, GKE Pod Snapshots (which directly supports checkpointing), and a claim model designed by people who thought hard about exactly this problem.

**Mapping:**

| Lenny CRD | Agent-Sandbox CRD | Notes |
|---|---|---|
| `AgentPool` | `SandboxTemplate` | Declares runtime type, isolation profile, resource class |
| `AgentPod` | `Sandbox` + `SandboxWarmPool` | Warm pool management |
| `AgentSession` | `SandboxClaim` | Transactional claim mechanism |
| WarmPoolController | Upstream SIG Apps controller | Community-maintained |

**Implementation note:** The claim mechanism via `SandboxClaim` must be verified to provide the same optimistic-locking guarantee Lenny currently gets from the status PATCH approach. Close reading of the agent-sandbox claim controller is required before committing. Task pod mode and concurrent mode remain Lenny-layer concerns — agent-sandbox hosts the pods without knowing about multi-task reuse or intra-pod concurrency.

**Context for A2A:** No direct A2A implication. Agent-sandbox is pure infrastructure and is protocol-agnostic.

---

### 1.2 Adapter-Binary Protocol Replaced by Multi-Server MCP + Lifecycle Channel

**What changes:** The current stdin/stdout JSON Lines binary protocol (Section 15.4.1) is replaced by a two-part model:

**Part A — Multiple focused local MCP servers** exposed by the adapter to the runtime (intra-pod, stdio or Unix socket transport):
- **Workspace MCP server** — file read/write/list for `/workspace`. Served directly by the adapter, no gateway involvement.
- **Platform MCP server** — gateway-backed tools: `lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`, `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`.
- **Connector proxy MCP server** — namespaced external tools (`github/create_pr`, `jira/create_ticket`). Adapter proxies to gateway, which holds OAuth tokens and enforces connector lease scopes.

**Part B — Lifecycle channel** — a small separate channel (second stdio stream pair or second Unix socket) carrying a minimal message set for platform lifecycle events not belonging in MCP:

```
Adapter → Runtime:  lifecycle_capabilities, checkpoint_request, interrupt_request,
                    credentials_rotated, terminate
Runtime → Adapter:  lifecycle_support, checkpoint_ready, interrupt_acknowledged,
                    credentials_acknowledged
```

Everything in the lifecycle channel is optional. Runtimes that don't open this channel operate in fallback-only mode.

**Runtime integration layers:**
- **Layer 0** — any MCP-capable binary. Zero Lenny knowledge. Fallback lifecycle only.
- **Layer 1** — use the adapter's MCP servers fully (workspace, delegation, output, elicitation). Standard MCP.
- **Layer 2** — open the lifecycle channel and declare supported hooks.

**Affects:** Section 15.4 and 15.4.1 (full replacement), Section 4.7 (Runtime Adapter RPCs change significantly), build Phase 2.

**Why multiple servers instead of one:** MCP aggregation is not lossless. Capability negotiation is per-server in the spec. Sampling flows correctly per connection. Tool name collisions are avoided within focused servers. Future MCP features that introduce per-server identity or per-connection auth would break through an aggregated proxy. Each server has independent capability negotiation.

**Context for A2A:** The intra-pod MCP interface is entirely separate from the external-facing protocol layer. A2A support, when added, lives in the gateway's `ExternalProtocolAdapter` and does not affect the runtime-facing adapter MCP servers. Runtime authors never need to know whether the external protocol is MCP, A2A, or something else.

---

### 1.3 Internal `OutputPart` Format Replaces `agent_text` and Flat `TaskResult.output`

**What changes:**

The `agent_text` streaming event is replaced by `agent_output` carrying an `OutputPart` array. The flat `{text, structured, artifactRefs}` in `TaskResult.output` is replaced by a `parts: []OutputPart` array.

`OutputPart` schema:

```json
{
  "id": "part_abc123",
  "type": "text",
  "mimeType": "text/plain",
  "inline": "content here",
  "ref": "artifact://...",
  "annotations": { "role": "primary", "final": true },
  "parts": [],
  "status": "streaming | complete | failed"
}
```

Key properties:
- `type` is an **open string** — not a closed enum. Unknown types pass through opaquely.
- `mimeType` handles encoding separately from semantic type.
- `inline` vs `ref` are properties, not type distinctions.
- `annotations` is an open metadata map.
- `parts` allows nesting (compound outputs like `execution_result` containing stdout, stderr, chart).
- `id` enables part-level streaming updates.

**Why not MCP content blocks directly:** The adapter translates between `OutputPart` and whatever external protocol is in use (MCP content blocks, A2A parts). Runtimes are insulated from external protocol evolution. When MCP adds a new block type, only the MCP translator changes — runtimes and the binary protocol are untouched. An SDK helper `from_mcp_content(blocks)` is provided for runtime authors who want to produce MCP-native output.

**Affects:** Section 15.4.1 (agent binary protocol messages), Section 8.9 (TaskResult schema), Section 7.2 (interactive session message types).

**Context for A2A:** The `OutputPart` model is intentionally a superset of both MCP content blocks and A2A parts. MCP translation: `text` → `TextContent`, `image` → `ImageContent`, `ref` → `resource_link`. A2A translation: `text` → `TextPart`, `ref` → `FilePart`, `data` + `application/json` → `DataPart`. Neither translation is lossy for the common cases.

---

### 1.4 `ExternalProtocolAdapter` Interface on the Gateway

**What changes:** The gateway's external MCP implementation is refactored from a monolithic MCP Fabric into a thin `ExternalProtocolAdapter` interface. MCP is the first implementation. This is an internal code change, not an API change, but it must exist from day one.

```go
type ExternalProtocolAdapter interface {
    CreateSession(ctx, req ExternalRequest) (Session, error)
    StreamEvents(ctx, session, ch <-chan OutputPart) error
    MapTaskLifecycle(state SessionState) ExternalTaskState
    ContentType() string
}
```

**Affects:** Section 4.1 (Gateway internals), Section 9.1 (MCP integration table).

**Context for A2A:** This is the mechanism through which A2A support is added without a redesign. When A2A is implemented, it becomes a second `ExternalProtocolAdapter`. The gateway serves both simultaneously. The internal session model, credentials, delegation, and policy enforcement are unchanged.

---

### 1.5 `executionMode` on Pool Configuration

**What changes:** Pool configuration gets an `executionMode` field from day one, even though only `session` is implemented initially:

```yaml
executionMode: session   # session | task | concurrent
```

**Affects:** Section 5.2 (Pool Configuration), Phase 1 of the build sequence (core types).

**Why now:** Adding this field later requires a schema migration and breaks any tooling that introspects pool configuration. The field must exist even if `task` and `concurrent` are not yet functional.

---

### 1.6 `SessionTemplate` as a First-Class Resource

**What changes:** `SessionTemplate` is a named, stored `WorkspacePlan` — a deployer-defined preset for session creation. It is the primary mechanism for "deploy a LangGraph agent (or any agent) once, run many times." Runtimes are never registered per-graph; a single `langgraph-runtime` image handles all graphs, with the graph definition uploaded via the template.

```yaml
POST /v1/admin/session-templates
{
  "id": "my-research-graph",
  "runtime": "langgraph-runtime",
  "workspacePlan": {
    "files": [{ "path": "agent.py", "content": "..." }],
    "setupCommands": ["pip install -r requirements.txt"]
  },
  "publishedMetadata": [
    { "key": "agent-card", "contentType": "application/json",
      "visibility": "internal", "value": "..." }
  ]
}
```

**Model B explicitly rejected:** There is no mechanism for packaging a graph definition as a new registered `RuntimeType`. Users register templates, not runtimes. Custom `RuntimeType` registration is for runtimes with genuinely different binaries or dependency sets, not for different graph definitions running on the same binary.

**Affects:** Section 1.8 of the critique (missing first-class abstractions), Section 15.1 (REST API), Section 15.2 (MCP tools — `create_session` gains a `template` parameter).

---

## Part 2: V1 Must-Have — Feature Changes

These features don't create breaking protocol commitments but are architecturally significant enough that they should ship with v1 rather than be retrofitted.

---

### 2.1 Task Pod Execution Mode

**What changes:** A pool with `executionMode: task` reuses pods across sequential tasks. Between tasks, the pod performs a scrub: workspace wiped, `/sessions/` tmpfs cleared, environment reset, credentials re-evaluated. The pod lifecycle is decoupled from the task lifecycle.

**Scrub protocol:** The adapter executes the scrub when the gateway sends a `terminate` message on the lifecycle channel with `reason: task_complete`. The pod transitions back to an available state rather than being drained. The gateway can then assign the next task to the same pod via `SandboxClaim`.

**Credential handling in task mode:** Pool credentials (admin-managed API keys) are compatible with task pod reuse. User-scoped credentials (bring-your-own-key) are re-evaluated per task — the gateway may need to re-elicit per-task depending on session scope.

**Security requirement:** Runtimes using task pod mode must declare this explicitly (`executionMode: task` in pool config). A deployer acknowledgment is required, similar to `allowStandardIsolation: true` for runc. The scrub protocol must be verified before a pod is returned to available state.

**Agent-sandbox note:** Agent-sandbox sees a long-lived claimed sandbox that Lenny holds across multiple task executions. The multi-task reuse is invisible to agent-sandbox.

**Affects:** Section 6.1, 6.2 (pod state machine gains task-mode states), Section 7 (session lifecycle), Section 5.2 (pool configuration).

---

### 2.2 Concurrent Execution Mode

**What changes:** A pool with `executionMode: concurrent` allows a single pod to handle multiple simultaneous task slots, up to `maxConcurrent`. Designed for runtimes with expensive shared in-memory state (typically self-hosted models) where running N separate pods means loading the model N times.

**Two concurrency sub-variants:**

**Concurrent-workspace:** Each slot gets its own workspace materialized by the gateway before the slot starts. The gateway tracks per-slot lifecycle (separate OutputPart stream, separate credential scope, separate task record). Multiple workspaces coexist within the same pod. This is the model for self-hosted model runtimes.

**Concurrent-stateless:** No workspace materialization. The pod receives requests directly through its local MCP server without any per-request setup. Kubernetes service routing handles load distribution. The pod's readiness probe reflects slot availability.

**Load balancing:** For concurrent-stateless, the gateway routes through a Kubernetes Service rather than tracking per-pod slot state. The adapter maintains an internal semaphore; at `maxConcurrent` the pod marks itself not-ready, removing it from the endpoint set. The PoolScalingController watches `active_slots / (pod_count × maxConcurrent)` and adds/removes pods accordingly.

**Security requirement:** Concurrent-workspace mode requires explicit deployer acknowledgment. Multiple workspaces on the same pod means cross-slot isolation is process-level and filesystem-level, not kernel-level. This is explicitly weaker than the session mode security model and must be documented as such.

**Note on stateless workloads:** Truly stateless runtimes with no workspace and no expensive shared state should be registered as connectors (external services called through the gateway) rather than as Lenny-managed pods. Concurrent mode is not the right model for simple HTTP prompt-response services.

**Agent-sandbox note:** Concurrent mode is invisible to agent-sandbox. Agent-sandbox manages one pod with one claim. Lenny's adapter layer manages request multiplexing within that pod.

**Affects:** Section 5.2, 6.1, 6.2, Section 4.1 (gateway routing logic for stateless subvariant).

---

### 2.3 `ConnectorDefinition` as a First-Class Resource

**What changes:** Connector configuration (external MCP server URL, OAuth config, auth type, expected domain for elicitation validation) becomes a first-class declared resource managed via the admin API, not Helm values.

```yaml
POST /v1/admin/connectors
{
  "id": "github",
  "displayName": "GitHub",
  "mcpServerUrl": "https://mcp.github.com",
  "transport": "streamable_http",
  "auth": {
    "type": "oauth2",
    "authorizationEndpoint": "...",
    "tokenEndpoint": "...",
    "clientSecretRef": "lenny-system/github-client-secret",
    "scopes": ["repo", "read:org"],
    "expectedDomain": "github.com"
  },
  "visibility": "tenant"
}
```

**Affects:** Section 4.3 (Connector/Token Service), Section 9.3 (OAuth for external tools).

---

### 2.4 `publishedMetadata` on RuntimeType and SessionTemplate

**What changes:** Both `RuntimeType` and `SessionTemplate` gain a `publishedMetadata` array. Each entry is an opaque key-value pair with a content type and a visibility level. The gateway serves entries at well-known URLs. The gateway treats content as opaque pass-through — no parsing or validation.

```yaml
publishedMetadata:
  - key: agent-card
    contentType: application/json
    visibility: internal    # internal | tenant | public
    value: '{ "name": "...", "capabilities": [...] }'
  - key: openapi-spec
    contentType: application/yaml
    visibility: tenant
    value: '...'
```

**Visibility semantics:**
- `internal` — served at `GET /internal/runtimes/{name}/meta/{key}`, requires valid Lenny session JWT, only reachable from inside the cluster.
- `tenant` — same as internal but filtered by `tenant_id` claim.
- `public` — served at `GET /v1/runtimes/{name}/meta/{key}`, no auth required, reachable externally.

**Context for A2A:** An A2A Agent Card is just one possible entry in `publishedMetadata`. The schema deliberately does not name it `agentCard` — that would encode a bet on A2A's longevity. The `public` visibility level enables external A2A discovery. When A2A support is added to the gateway, the gateway serves `GET /.well-known/agent.json` for runtimes with a public `agent-card` metadata entry.

---

### 2.5 `lenny/discover_agents` Tool in the Platform MCP Server

**What changes:** A new tool in the adapter's platform MCP server allows runtimes to discover other agents available within the same Lenny deployment.

```
lenny/discover_agents(capability?: string, tags?: string[]) → AgentCard[]
```

Returns runtimes and templates with `internal` or `tenant` visibility metadata matching the query. Results include enough information to `lenny/delegate_task` to them within Lenny's delegation system.

**Affects:** Section 9 (MCP Integration), Section 8 (Delegation — internal discovery is separate from external_delegate).

---

### 2.6 `external_delegate` Gateway Primitive

**What changes:** A new tool in the platform MCP server enables runtimes to delegate to agents outside the Lenny cluster.

```
lenny/external_delegate(
  protocol: "a2a" | "mcp",
  endpoint: string,
  task: TaskSpec,
  lease_slice: LeaseSlice
) → TaskHandle
```

The gateway acts as the protocol client (A2A client, MCP client). The same lease narrowing, budget enforcement, and task tree tracking apply as for internal delegation. The task appears in `get_task_tree` output. All egress goes through the gateway's egress profiles.

**Context for A2A:** This is the primary mechanism through which Lenny participates in A2A multi-agent ecosystems. Inbound A2A (external agents calling into Lenny) is handled by the public `ExternalProtocolAdapter` endpoint. Outbound A2A (Lenny agents calling external agents) is handled by `external_delegate`.

**Delegation lease data model change (breaking if deferred):** The delegation lease must accommodate external delegation records from day one. The `allowedExternalEndpoints` field must exist on the lease schema even if `external_delegate` is not yet implemented.

**Affects:** Section 8 (Delegation — new leaf type in task tree), Section 8.3 (Delegation Lease schema), Section 9.1.

---

### 2.7 Comprehensive Admin API

**What changes:** A full, consistent admin API surface manages all operational configuration at runtime without Helm deploys. Configuration is stored in Postgres (primary) and cached with short TTL in the gateway. Redis pub/sub notifies gateway replicas of immediate invalidations (e.g., credential pool paused, experiment paused).

**Operational plane — API-managed:**
- Runtime Registry: `GET/POST/PUT/DELETE /v1/admin/runtimes`
- Pool Configuration: `GET/POST/PUT/DELETE /v1/admin/pools`
- Credential Pools: `GET/POST/PUT/DELETE /v1/admin/credential-pools` (metadata only, secret material stays in K8s Secrets)
- Connector Registry: `GET/POST/PUT/DELETE /v1/admin/connectors`
- Session Templates: `GET/POST/PUT/DELETE /v1/admin/session-templates`
- Tenant Management: `GET/POST/PUT/DELETE /v1/admin/tenants`
- Quota and Rate Limits: `GET/PUT /v1/admin/tenants/{id}/quotas`
- User Role Assignments: `GET/POST/DELETE /v1/admin/tenants/{id}/users`
- Egress Profiles: `GET/POST/PUT/DELETE /v1/admin/egress-profiles`
- Experiment Definitions: `GET/POST/PUT/DELETE /v1/admin/experiments`
- Scaling Policies: `GET/PUT /v1/admin/pools/{id}/scaling-policy`
- Memory Store Config: `GET/PUT /v1/admin/memory-store`
- Webhook / Callback Config: `GET/PUT /v1/admin/webhooks`

**Bootstrap plane — Helm/config only (cannot be changed at runtime):**
- Database connection strings, Redis address, MinIO endpoint, KMS key IDs
- Cluster name, namespace assignments, Kubernetes API server address
- Certificate paths and CA references
- `LENNY_DEV_MODE` flag
- System-wide default values (quota defaults, session age defaults)
- Kubernetes object definitions (RuntimeClass, NetworkPolicy templates, PDB templates)

**CRDs become derived state:** `SandboxTemplate`/`SandboxWarmPool` CRDs are reconciled from Postgres by the PoolScalingController, not managed directly by operators. `kubectl edit` is not the correct way to change pool settings. Documentation must make this explicit.

**Admin API design constraints:**
- Consistent with the client-facing API (same error taxonomy, same pagination, same OIDC auth with role claims)
- Full resource representation with `etag` for conditional PUT (optimistic locking)
- `?dryRun=true` on all mutations
- OpenAPI spec generated from Go handler definitions (never hand-written)
- All mutations flow through the existing audit log

**Affects:** Section 15.1 (REST API — admin endpoints), Section 10.2 (RBAC model), Section 11.7 (audit logging).

---

### 2.8 PoolScalingController (Replaces / Supersedes WarmPoolController Scaling Logic)

**What changes:** The warm pool scaling intelligence is extracted into a dedicated `PoolScalingController`. The existing `WarmPoolController` retains responsibility for individual pod lifecycle (create, claim, scrub, drain, terminate). The `PoolScalingController` is responsible for the desired configuration of pools (what `minWarm` should be right now, whether a variant pool should exist at all).

The `PoolScalingController` is driven by a pluggable `PoolScalingStrategy` interface. Lenny ships a default implementation. Deployers who want custom scaling logic (KEDA integration, cost-aware scaling, multi-armed bandit experiment allocation) implement the interface and replace the default.

**Default scaling formula:**

```
variant_minWarm = ceil(base_demand_p95 × variant_weight × safety_factor)
```

Where demand P95 is computed over a rolling 15-minute window. `safety_factor` defaults to 1.5 and is configurable per pool.

**Cold-start bootstrap:** When no demand signal exists yet, provision `ceil(1 / variant_weight)` pods as a safe initial state.

**Pool phases managed automatically:**
- Pre-warm (before experiment starts, by `prewarmLeadMinutes` ahead of `startAt`)
- Ramp (gradual weight increase per `ramp` policy)
- Steady state (continuous demand-proportional sizing)
- Wind-down (drain on `concluded` or `paused` status, cooperative with active sessions)

**Affects:** Section 4.6 (Warm Pool Controller — split into two controllers), Section 5.2.

---

### 2.9 Experiment Primitives

**What changes:** `ExperimentDefinition` is a first-class admin API resource:

```yaml
experiments:
  - id: claude-v2-rollout
    status: scheduled          # scheduled | active | paused | concluded
    startAt: "2026-04-01T09:00:00Z"
    prewarmLeadMinutes: 10
    baseRuntime: claude-worker
    variants:
      - id: treatment
        runtime: claude-worker-v2
        pool: claude-worker-v2-sandboxed-medium
        weight: 10
    targeting:
      mode: percentage          # percentage | cohort | combined
      sticky: user              # user | session | none
      cohortRef: beta-users     # for cohort mode
    ramp:
      initialWeight: 1
      targetWeight: 10
      stepSize: 1
      stepIntervalMinutes: 30
      pauseOnErrorRateExceeds: 5.0
    propagation:
      childSessions: inherit    # inherit | control | independent
```

**Routing:** An `ExperimentRouter` is a built-in implementation of the `RequestInterceptor` interface (pre-routing phase). It assigns variants using deterministic hashing for sticky modes and cohort lookup for cohort modes. It injects `experimentContext` into the delegation lease so propagation is automatic.

**Results API:** `GET /v1/admin/experiments/{id}/results` returns variant-grouped aggregate metrics: session counts, eval scores (populated from external eval hooks), cost per session, latency distributions.

**Status transitions as control signals:** All Lenny components react to experiment status transitions automatically. Gateway stops routing to variant immediately on `paused`. PoolScalingController begins wind-down on `concluded`. No manual intervention required at any phase.

**Affects:** Section 4.8 (Policy Engine — ExperimentRouter interceptor), Section 15.1 (results endpoint), Section 11.2 (billing events gain experiment metadata fields).

---

## Part 3: V1 Should-Have

These are meaningful improvements that don't create protocol commitments but should ship with v1 if possible.

---

### 3.1 `MemoryStore` Interface (Cross-Session Memory)

**What changes:** A `MemoryStore` role-based storage interface alongside the existing six:

```go
type MemoryStore interface {
    Write(ctx, scope MemoryScope, memories []Memory) error
    Query(ctx, scope MemoryScope, query string, limit int) ([]Memory, error)
    Delete(ctx, scope MemoryScope, ids []string) error
    List(ctx, scope MemoryScope, filter MemoryFilter) ([]Memory, error)
}

type MemoryScope struct {
    TenantID  string
    UserID    string
    AgentType string   // optional
    SessionID string   // optional
}
```

**Default implementation:** Postgres + pgvector. Deployers who want Mem0, Zep, a knowledge graph backend, or anything else implement the interface. The technology choice is explicitly deferred — the memory layer market is not settled and committing to a specific implementation would be premature.

**Runtimes access memory via:** `lenny/memory_write` and `lenny/memory_query` tools in the platform MCP server. Runtimes that don't use memory ignore these tools entirely.

**Why not a specific technology:** As of early 2026, the market shows Mem0 (vector-based, 48K GitHub stars, good latency), Zep (temporal knowledge graph, excellent for relational reasoning but slow initial retrieval), LangMem (LangGraph-specific, not framework-agnostic), and emerging alternatives. The correct approach is to define the interface and ship a simple default, not to pick a winner.

**Affects:** Section 4 (new MemoryStore in the storage interfaces table), Section 9 (new tools in platform MCP server).

---

### 3.2 Semantic Caching at the LLM Proxy

**What changes:** An optional `CachePolicy` on `CredentialPool` configuration adds semantic caching to the LLM reverse proxy:

```yaml
credentialPools:
  - name: claude-direct-prod
    cachePolicy:
      enabled: true              # default: false
      strategy: semantic         # semantic | exact
      ttlSeconds: 300
      similarityThreshold: 0.92
      backend: redis             # redis | custom
```

**Pluggable:** A `SemanticCache` interface allows complete replacement of the caching implementation. The default is Redis-backed. Deployers with existing caching infrastructure (Weaviate, Qdrant, custom) implement the interface.

**Disabled by default.** Deployers who need strict non-caching (financial calculations, real-time data, any workload where determinism matters) leave it unconfigured.

**Affects:** Section 4.9 (Credential Leasing — LLM Reverse Proxy subsection), Section 12.4 (Redis usage).

---

### 3.3 `RequestInterceptor` Extension Point (Formalized)

**What changes:** The `RequestInterceptor` interface described in critique Section 1.3 is implemented, with concrete built-in interceptors as first implementations. The existing Policy Engine evaluators become built-in interceptors.

**Phases:**
- `PreAuth` — before authentication
- `PostAuth` — after authentication, before routing
- `PreRoute` — before pod allocation
- `PostRoute` — after pod allocation, before session start
- `PreToolResult` — before a connector tool result is delivered to the agent (primary guardrail hook)
- `PostAgentOutput` — before agent output is forwarded to the client or connectors

**Built-in interceptors:**
- `ExperimentRouter` (pre-routing phase, described above)
- `GuardrailsInterceptor` (pre-tool-result and post-agent-output, disabled by default)

**External interceptors** are called via gRPC (Kubernetes admission webhook pattern). Internal interceptors are Go interface implementations registered at startup.

**The `GuardrailsInterceptor`:** Disabled by default. When enabled, it calls a configurable external service for content inspection. Lenny ships no guardrail logic itself — this is purely a hook. Deployers wire in AWS Bedrock Guardrails, Azure AI Content Safety, Lakera Guard, or their own classifier. The interceptor enforces the principle that prompt injection is OWASP LLM Top 10 #1 and a platform-level hook is the right mitigation point.

**Affects:** Section 1.3 of the critique (this is the fix), Section 4.8 (Policy Engine), Section 9.2 (elicitation chain — guardrail interceptor can act here).

---

### 3.4 Agent Evaluation Hooks

**What changes:**

`POST /v1/sessions/{id}/eval` — accepts scored evaluator results from external tools:

```json
{
  "evaluatorId": "braintrust-hallucination",
  "scores": {
    "hallucination_score": 0.12,
    "task_completion": 0.94
  },
  "metadata": { "model": "gpt-4o-judge", "version": "2.1" }
}
```

`POST /v1/sessions/{id}/replay` — re-runs a session against a different runtime version using the same workspace snapshot and prompt history. Enables regression testing when upgrading runtimes.

Eval scores are stored alongside the session record and surfaced in the observability dashboard. They are included in experiment result aggregations (`GET /v1/admin/experiments/{id}/results`).

**Lenny ships no eval logic.** The evaluation work — LLM-as-judge scoring, dataset curation, ground-truth comparison — is done by Braintrust, Langfuse, or the deployer's own tooling. Lenny provides the storage and API surface only.

**Affects:** Section 15.1 (REST API — new endpoints), Section 16 (observability — eval scores in dashboards).

---

### 3.5 `CredentialRouter` Interface (Intelligent Model Routing)

**What changes:** The credential pool selection logic becomes a pluggable `CredentialRouter` interface. The default implementation is Lenny's existing strategy (least-loaded / round-robin / sticky-until-failure). Deployers implement custom routing logic — cost-aware, latency-based, task-complexity-based, or entirely custom — by implementing the interface.

**Disabled by default.** Opt-in per pool. Runtimes can set routing hints on LLM calls to influence routing decisions, but the gateway's `CredentialRouter` makes the final selection.

**Affects:** Section 4.9 (Credential Leasing — assignment strategy), Section 11.6 (circuit breakers — routing interacts with health scoring).

---

## Part 4: Planned / Post-V1

These must be documented in the spec now so data models accommodate them. Implementation can follow.

---

### 4.1 A2A Full Support

**What to document now:**

The `ExternalProtocolAdapter` interface (Part 1.4) is the mechanism. When A2A is implemented:
- Inbound: gateway serves `POST /a2a/{runtimeName}/tasks`, translates to Lenny session lifecycle
- Outbound: `external_delegate` with `protocol: "a2a"` calls external A2A agents
- Discovery: `RuntimeType.publishedMetadata` with `visibility: public` provides Agent Cards at `/.well-known/agent.json`
- Authentication: The `AuthEvaluator` in the Policy Engine must be extensible for Agent Card validation (not just OIDC tokens)

**Data model requirement (v1):** The delegation lease `allowedExternalEndpoints` field must exist now. The `experimentContext` propagation must include external delegation records. Neither requires A2A to be implemented, but both require the schema slot.

**Current A2A adoption note for the spec:** As of Q1 2026, A2A is under the Linux Foundation's AAIF alongside MCP. Both are now under neutral governance. A2A adoption has been a slow burn compared to MCP's 97M monthly downloads, but AAIF governance makes long-term survival significantly more likely than when A2A was Google-proprietary. Lenny's design does not implement A2A in v1 but ensures no design decision closes the door.

---

### 4.2 Multi-Cluster Federation

**What to document now:** Session IDs must be globally unique (they appear to be). Storage interfaces must not assume single-cluster topology. Connectors registered in one cluster must be expressible by reference in multi-cluster scenarios.

---

### 4.3 Lightweight Task Mode for `kubernetes-sigs/agent-sandbox` Evolution

**What to document now:** If agent-sandbox formalizes a multi-task or concurrent lifecycle model in future versions, Lenny should align with it. The current task pod and concurrent modes are Lenny-layer concerns invisible to agent-sandbox. When/if agent-sandbox adds native support, the migration path should be documented.

---

### 4.4 UI and CLI

**What to document now:** The admin API is designed as a complete operational surface — every action possible in the system must be expressible via the admin API, with no operations requiring direct Helm edits. The official CLI (`lenny-ctl`) and web portal are explicitly planned as separate projects that consume the admin API as thin clients with zero business logic. Third parties building management tooling on top of the admin API is the intended model.

---

## Part 5: Explicit Non-Decisions and Clarifications

These are decisions made in the negative — things Lenny deliberately will not do — that should be documented in the spec to prevent scope creep.

---

### 5.1 No Model B Runtime Deployment

Lenny is not a general application deployment platform. Users deploy graphs and agents via `SessionTemplate`, not by registering new `RuntimeType` instances per graph. Custom `RuntimeType` registration is for genuinely different binaries (different language, different dependencies, different isolation requirements), not for different LangGraph graphs running on the same binary.

---

### 5.2 No Built-in Eval Logic

Lenny provides eval hooks and result storage. It does not implement LLM-as-judge, hallucination detection, ground-truth comparison, or any other evaluation logic. This belongs in Braintrust, Langfuse, Arize, or the deployer's own tooling.

---

### 5.3 No Built-in Guardrail Logic

Lenny provides the `RequestInterceptor` hook at the right points in the request lifecycle. It does not implement content classifiers, prompt injection detection, or toxicity filters. Deployers wire in their own systems.

---

### 5.4 No Built-in Memory Extraction

Lenny provides the `MemoryStore` interface and `lenny/memory_write` / `lenny/memory_query` tools. It does not extract, summarize, or classify memories from session transcripts. Runtimes decide what to write and how to structure it.

---

### 5.5 Hooks-and-Defaults Design Principle

Every cross-cutting AI capability (memory, caching, guardrails, evaluation, routing, scaling) follows the same pattern: defined as an interface with a sensible default implementation, all defaults disabled unless explicitly enabled, all interfaces fully replaceable by deployers. Lenny never implements AI-specific logic — that belongs to specialized tools in the ecosystem.

---

## Part 6: Build Sequence Changes

The current 15-phase build sequence needs updating to reflect the new scope. Key changes:

**Phase 1** — Add `executionMode` field to pool configuration schema. Add `allowedExternalEndpoints` to delegation lease schema. Use agent-sandbox CRDs instead of custom CRDs.

**Phase 2** — Replace adapter binary protocol with multi-server MCP adapter + lifecycle channel. Publish this as the runtime adapter specification.

**Phase 3** — Implement PoolScalingController alongside (not replacing) the agent-sandbox upstream controller. Implement `SessionTemplate` as a core resource.

**New Phase between 4 and 5** — Admin API foundation: runtime registry, pool configuration, connector registry, tenant management. Gateway loads config from Postgres rather than startup config.

**Phase 9** — Add `external_delegate` stub to delegation primitives (data model only, implementation deferred).

**Phase 10** — Add `lenny/discover_agents` tool and `publishedMetadata` serving to MCP fabric.

**New Phase 16** — Experiment primitives: `ExperimentDefinition`, `ExperimentRouter` interceptor, PoolScalingController integration.

**New Phase 17** — Memory, caching, guardrail, eval hooks (interfaces + default implementations).

---

## Summary Statistics

| Tier | Count |
|---|---|
| V1 Must-Have Breaking | 6 |
| V1 Must-Have Feature | 9 |
| V1 Should-Have | 5 |
| Planned/Post-V1 | 4 |
| Explicit Non-Decisions | 5 |

---

## Human (Mar 29, 2026, 9:35 PM):

Thanks. I think we need to get on the same page on a number of things. Let's tackle them one by one.

Starting with multi-server MCP, what I had in mind was different than what you propose:
* I don't know if a workspace MCP server makes sense? The workspace files (if any) need to be in the file system, and the intention is that the runtime just reads them from there. What does an MCP server solve?
* Platform MCP server: yes, we are in agreement.
* Connector proxy MCP server: I thought we had agreed that aggregating MCP servers isn't always lossless, so I was expecting that instead of one MCP server we would have one MCP server per MCP connector. Maybe I'm misremembering what we discussed, or maybe I'm missing something

---

## Claude:

Both corrections are right. Let me address them directly.

**On the workspace MCP server:** You're correct that it doesn't make sense. The workspace is already materialized at `/workspace/current` before the runtime starts. The runtime is just a process with filesystem access — it reads files the normal way. There's no problem an MCP server solves here. The workspace MCP server should be removed from the model entirely.

**On the connector proxy MCP server:** You're not misremembering. We did agree that aggregation isn't lossless — capability negotiation is per-server in the MCP spec, sampling flows correctly only per connection, and future MCP features with per-server identity or per-connection auth would break silently through an aggregated proxy. So yes, the correct model is one MCP server per connector, not one aggregated proxy. Each connector the session is authorized to use appears as a separate MCP server connection to the runtime, with its own independent capability negotiation and its own auth context.

So the corrected multi-server model inside the pod is:

- **Platform MCP server** — the one adapter-implemented server for Lenny-specific tools (`lenny/delegate_task`, `lenny/output`, `lenny/discover_agents`, etc.)
- **One MCP server per authorized connector** — each proxied through the gateway, each with independent capability negotiation, each scoped to what the delegation lease's `allowedConnectors` permits

The runtime connects to however many servers it needs. The adapter manages the proxy connections to the gateway for each connector server. The lifecycle channel remains separate from all of this.

Does that match what you had in mind?

---

## Human (Mar 29, 2026, 9:45 PM):

Yes, thanks.

Here's another thing I don't agree with/don't understand. You say:

"The intra-pod MCP interface is entirely separate from the external-facing protocol layer. A2A support, when added, lives in the gateway's `ExternalProtocolAdapter` and does not affect the runtime-facing adapter MCP servers. Runtime authors never need to know whether the external protocol is MCP, A2A, or something else"

The last part doesn't make sense. Runtime authors need to speak MCP in order to connect to MCP servers proxied by the adapter. If tomorrow we support some other protocol, then the adapter should expose it to the runtime and the runtime needs to understand it.

When it comes to A2A, the reason it doesn't belong in the adapter (I think), is because discoverability of agents would be provided by the gateway (by the way, how does a runtime find agent cards for both internal agents as well as external A2A connectors?). But here, ExternalProtocolAdapter doesn't play a role (I think) because this is all internal communications. Please double check me as I could be completely misunderstanding or misremembering

---

## Claude:

You're right on both points, and I was conflating two different boundaries.

**On the first point:** The `ExternalProtocolAdapter` is strictly about the *external client → gateway* boundary — how things outside the cluster talk to Lenny. MCP is the first implementation there. A2A inbound would be a second. This has nothing to do with what happens inside the pod. Runtime authors speak MCP to the adapter's servers, full stop. If Lenny ever adds a different intra-pod protocol, runtime authors would need to implement it. I stated that incorrectly.

**On the second point:** You're right that `ExternalProtocolAdapter` doesn't play a role in discovery. Let me think through how discovery actually works:

For **internal agents**, `lenny/discover_agents` queries the metadata registry (RuntimeTypes and SessionTemplates with `internal` or `tenant` visibility). That's clean.

For **external A2A agents**, the right model is that they are registered as A2A connectors in the ConnectorRegistry — same registry, different `auth.type` and `protocol` field. The gateway fetches and caches their Agent Cards at registration time. `lenny/discover_agents` then surfaces both internal agents and registered external A2A agents through the same tool, with the source indicated in the result. The runtime never makes direct HTTP calls to external A2A endpoints for discovery — everything goes through the gateway, which enforces egress profiles and records audit events.

So `lenny/discover_agents` becomes the single discovery surface for everything the runtime can reach, regardless of whether the target is internal or external, MCP or A2A. The `lenny/external_delegate` tool is what the runtime uses once it has decided to call an external agent it discovered.

Does that match your thinking?

---

## Human (Mar 29, 2026, 9:50 PM):

Not exactly. I think `lenny/discover_agents` would only surface agents and not MCP servers/tools (those are already provided by the MCP servers running in the adapter)?
Actually, this brings up a couple of questions:
* In the proposed adapter/MCP server model, how does the runtime know which MCP servers it has available in the adapter so that it can connect to them?
* For A2A, what is the standard way of discovering agents? Is it via MCP? (I don't think so but I could be wrong)

---

## Claude:

Good questions, both worth being precise about.

**On how the runtime discovers available MCP servers in the adapter:**

Standard MCP has no dynamic server discovery mechanism — clients know about servers because they're configured, not because they found them at runtime. For Lenny, the cleanest approach is that the adapter writes a manifest to a well-known path (say `/lenny/adapter-manifest.json`) before the runtime process starts. The manifest lists each available MCP server with its connection details — socket path or stdio pipe identifier, the connector ID it represents, and basic metadata. The runtime reads this at startup and connects to whichever servers it needs. This is simple, requires no protocol, and works with any MCP client library.

**On A2A discovery:**

You're right that A2A is not discovered through MCP. A2A discovery is done through Agent Cards published at `/.well-known/agent.json` on each agent's HTTP endpoint. There is no central directory in the A2A spec itself — you either know an agent's URL and fetch its card directly, or you use a separate registry service.

This means `lenny/discover_agents` is genuinely the right surface for agent discovery — separate from MCP server discovery which happens through the manifest. The tool returns agents the runtime can delegate to or call, whether they're internal Lenny-hosted agents (discovered via the metadata registry) or external A2A agents (registered as connectors with their Agent Card cached by the gateway). MCP servers providing tools are separate — those are in the manifest. Agents you can delegate work to are what `lenny/discover_agents` surfaces. The two concepts are distinct: tools are things an agent calls synchronously to get data or perform actions; agents are things you delegate tasks to asynchronously.

Does that separation feel right?

---

## Human (Mar 29, 2026, 10:04 PM):

Ok on the adapter-manifest.json. Just make sure it's in an appropriate folder. I assume this file needs to be re-generated for every task execution.

As for `lenny/discover_agents`this is fine but:
* Should the agent card URLs also appear in the adapter-manifest? (Not sure, just asking because I don't know how A2A clients generally load A2A registries (at startup time or on the fly).
* Agent discoverability could be very basic in Lenny by default (return everything the runtime has access to), but it should be extensible. The deployer may want to replace the discovery with semantic search, etc.
* Runtimes should still be able to delegate to other runtimes running in Lenny by name/id. A couple of example patterns (not comprehensive):
   * A runtime recursively delegates to itself. Nothing to discover here. Runtime just needs to know how to delegate to itself using the delegation MCP tool provided by the adapter
   * Runtime A is hardcoded to delegate always to runtime B.
* Runtimes may outsource work to MCP tools and future A2A agents via MCP/A2A. That is separate from the delegation primitive provided by Lenny.

Critique and let me know if this makes sense.

---

## Claude:

This all makes sense and is a cleaner model than what I had. Let me go through each point and flag one thing worth being explicit about.

**On adapter-manifest location and regeneration:** Agreed the path needs to be right — `/lenny/` is too generic. Something like `/run/lenny/adapter-manifest.json` is more appropriate (the `/run` convention in Linux signals ephemeral runtime state, not user workspace). And yes, regenerated per task execution — the manifest reflects what a specific task is authorized to use, which is determined by the delegation lease's `allowedConnectors`. A task with different connector authorization than the previous one gets a different manifest.

**On agent card URLs in the manifest:** A2A clients generally fetch agent cards on demand rather than at startup — you fetch the card when you've decided to call a specific agent, not speculatively. So agent card URLs don't belong in the manifest. The manifest is for things the runtime needs to connect at startup (MCP servers). Agent discovery is a separate on-demand operation via `lenny/discover_agents` or direct delegation by name.

**On discovery extensibility:** Agreed. The `lenny/discover_agents` implementation should be backed by a pluggable `AgentDiscoveryStrategy` interface. The default returns everything the session's delegation lease authorizes. Deployers replace it with semantic search, curated catalogs, capability-based filtering, or whatever their use case requires.

**On delegation by name without discovery:** Correct and important. `lenny/delegate_task` accepts a runtime name or session template ID directly — discovery is optional, not a prerequisite. The recursive self-delegation case and the hardcoded A-to-B case both work without ever calling `lenny/discover_agents`. Discovery is only needed when the runtime is dynamically choosing who to delegate to at runtime.

**One thing worth making explicit in the spec:** The distinction you draw at the end — MCP tool calls and A2A agent calls as separate from Lenny's delegation primitive — is architecturally correct, but the governance trade-off should be documented clearly. When a runtime calls an external agent directly through a connector (bypassing `lenny/external_delegate`), that call is outside Lenny's budget tracking, task tree recording, and delegation lease narrowing. The external agent's token consumption doesn't count against the tree budget. There's no child task record in the audit log. This is a valid choice — sometimes you want a lightweight synchronous call rather than a governed delegation — but deployers should understand they're trading observability and budget enforcement for simplicity. `lenny/external_delegate` is the governed path; direct tool calls to external agents are the ungoverned path. Both should be available, but the spec should name the trade-off explicitly.

---

## Human (Mar 29, 2026, 10:15 PM):

Thanks. This is now refreshing my memory (I had forgotten about `lenny/external_delegate`). I think that's the right path and what should be supported in v1. In the future, when we add support for A2A then we could maybe allow direct calls from runtimes to A2A agents. However, even in that scenario that traffic should probably be proxied by the adapter and gateway no matter what, right? The adapter would need to speak A2A and expose A2A cards and endpoints to the runtime, and then the adapter would convert all communications to our internal protocol when talking to the gateway.

Not sure if this would work, or if it would break anything else we discussed, though.

---

## Claude:

This model works and is consistent with everything we've discussed. Let me think through it carefully.

**Why it's consistent:** The adapter is already a translation layer — it converts between the runtime-facing MCP protocol and Lenny's internal protocol toward the gateway. Adding A2A support is the same pattern applied to a second runtime-facing protocol. The adapter speaks A2A to the runtime, converts to internal protocol toward the gateway, and the gateway maintains all governance: budget tracking, audit logging, delegation lease enforcement. The runtime never reaches external agents directly.

**How it would sit alongside the existing model:** MCP and A2A are complementary rather than conflicting inside the pod. MCP servers in the adapter are for tool access — synchronous calls to connectors (GitHub, Jira, etc.). A2A endpoints in the adapter are for agent-to-agent delegation — sending a task to another agent and awaiting a result. The adapter-manifest would list both MCP server endpoints and the A2A base URL so runtimes can discover and connect to both.

**One nuance worth flagging:** For the adapter to serve A2A agent cards describing what agents are available for delegation, it needs that information from the gateway — specifically the same data that backs `lenny/discover_agents`. The simplest approach is that the adapter serves a single A2A endpoint acting as a gateway proxy, with one agent card describing the full set of delegatable agents. When the runtime sends an A2A task to that endpoint, the adapter translates it to an `external_delegate` or `delegate_task` call to the gateway depending on whether the target is external or internal. The runtime doesn't need to know the difference.

**What this means for `lenny/delegate_task`:** It remains necessary. MCP-only runtimes that don't speak A2A still need the MCP tool path for delegation. A2A-native runtimes can use A2A. Both paths converge at the adapter and produce the same internal protocol call to the gateway. This is the right layered model — the runtime uses whichever interface matches its capabilities, and governance is enforced at the gateway regardless.

**One thing it doesn't conflict with:** The `ExternalProtocolAdapter` at the external client → gateway boundary is completely separate. That's about how things outside the cluster talk to Lenny. What you're describing is about how runtimes inside the pod talk to the adapter. Different boundaries, no conflict.

The only complexity this adds is that the adapter becomes meaningfully more sophisticated when A2A is added — it needs to implement an A2A server, serve agent cards, and translate A2A task lifecycle to Lenny's internal model. That's real work, which is why it belongs post-v1. But architecturally it's clean and doesn't require revisiting any of the decisions we've made.

---

## Human (Mar 29, 2026, 10:24 PM):

Ok. This makes sense. One change to your proposal, though: `lenny/discover_agents`should return discoverable agents and each should have a separate A2A endpoint with its own card (when A2A is supported, and if the agent supports it). `lenny/discover_agents` also needs to return the id/name to be used in `external_delegate` or `delegate_task`.

---

## Claude:

Agreed, and this is cleaner than what I described. The result from `lenny/discover_agents` becomes a self-contained agent descriptor — everything the runtime needs to decide how to interact with a given agent, without needing a separate lookup:

```json
[
  {
    "id": "research-agent",
    "displayName": "Research Agent",
    "description": "Deep research and document analysis",
    "source": "internal",
    "delegateTarget": {
      "type": "runtime",
      "id": "research-agent"
    },
    "a2aEndpoint": "http://adapter-a2a/agents/research-agent",
    "a2aCardUrl": "http://adapter-a2a/agents/research-agent/.well-known/agent.json"
  },
  {
    "id": "partner-summarizer",
    "displayName": "Partner Summarizer",
    "description": "External summarization agent",
    "source": "external",
    "delegateTarget": {
      "type": "external",
      "id": "partner-summarizer"
    },
    "a2aEndpoint": "https://partner.example.com/agents/summarizer",
    "a2aCardUrl": "https://partner.example.com/agents/summarizer/.well-known/agent.json"
  }
]
```

The `delegateTarget.id` is exactly what goes into `lenny/delegate_task` or `lenny/external_delegate`. The `a2aEndpoint` and `a2aCardUrl` are populated only when A2A is supported and the agent exposes it — absent otherwise. For v1 with no A2A support those fields are simply omitted from the response.

One implication worth noting: the adapter needs to serve individual A2A endpoints per agent (`/agents/{id}`), not a single aggregated endpoint. This is consistent with the conclusion from earlier that aggregation isn't lossless — each agent gets its own card, its own capability negotiation, its own auth context. The adapter-manifest would list the A2A base URL so runtimes know where to find these endpoints, and the cards themselves are fetched on demand per agent as A2A clients normally do.

---

## Human (Mar 29, 2026, 10:35 PM):

Thanks, this now aligns better with what I had in mind. Following up with more questions and comments that are cming to mind:
* The same way we are thinking of supporing A2A in the future, we should think about AP (Agent Protocol). Whatever we do should support AP without  refactors down the line.
* We haven't explicitly talked about how SessionTemplate fits into all this. Each template is probably a target that should be listed.
* Without re-implementing A2A, is there any additional information runtimes and session templates can expose on how they should be called, examples, etc. that should be configured as part of the runtime and/or the session template?

---

## Claude:

Good questions. Let me take them in order.

---

## On Agent Protocol (AP)

The AP you're referring to is the one from AI Engineer Foundation / AGI Inc — a REST API spec for agent lifecycle management. It defines core routes: `POST /ap/v1/agent/tasks` for creating tasks and `POST /ap/v1/agent/tasks/{task_id}/steps` for executing steps, plus routes for listing tasks, steps, and downloading/uploading artifacts. It's framework-agnostic and focused on standardizing the agent lifecycle management interface.

The good news is that AP fits the same pattern as A2A in terms of Lenny's architecture. It's an external-facing protocol — something external clients use to talk to an agent. The `ExternalProtocolAdapter` interface at the gateway boundary accommodates it exactly as it accommodates A2A: AP would be a third implementation of that interface alongside MCP and A2A. No changes needed to the intra-pod model, the adapter, or the runtime.

The one thing the spec should note explicitly: the `ExternalProtocolAdapter` pattern should not make assumptions that are MCP-specific when it comes to task lifecycle concepts. AP, A2A, and MCP all have slightly different task state models (AP has tasks and steps, A2A has tasks with `submitted/working/input-required/completed/failed`, MCP has async tasks). The internal session model is the canonical state machine — adapters translate to and from it.

---

## On SessionTemplates in Discovery

Yes, `SessionTemplate` instances should appear in `lenny/discover_agents` results alongside `RuntimeType` entries. A template is a deployed, named agent configuration — from a caller's perspective it's just an agent you can delegate to. The `delegateTarget` in the result distinguishes how to call it:

```json
{
  "id": "my-research-graph",
  "displayName": "Research Graph v2",
  "source": "internal",
  "kind": "template",
  "delegateTarget": {
    "type": "template",
    "id": "my-research-graph"
  }
}
```

versus a bare runtime:

```json
{
  "id": "claude-worker",
  "kind": "runtime",
  "delegateTarget": {
    "type": "runtime",
    "id": "claude-worker"
  }
}
```

The runtime passes `delegateTarget.id` and `delegateTarget.type` directly into `lenny/delegate_task`. The tool handles both cases.

---

## On Structured Calling Information Without Reimplementing A2A

This is the most interesting question, and there's a clean answer. A2A Agent Cards have a `skills` array with `id`, `name`, `description`, `inputModes`, `outputModes`, and `examples`. This structured information is useful regardless of A2A — it tells callers what an agent does, what inputs it accepts, what outputs it produces, and how to invoke it effectively. Putting it in opaque `publishedMetadata` loses the structure.

The proposal is a first-class `agentInterface` field on both `RuntimeType` and `SessionTemplate`:

```yaml
agentInterface:
  description: "Analyzes codebases and produces refactoring plans"
  inputModes:
    - type: "text/plain"
      description: "Natural language task description"
    - type: "application/json"
      schema: "{ 'task': 'string', 'focusArea': 'string' }"
  outputModes:
    - type: "text/plain"
      role: "primary"
    - type: "application/json"
      role: "structured_summary"
  skills:
    - id: "refactor"
      name: "Code Refactoring"
      description: "Identifies and implements code improvements"
    - id: "review"
      name: "Code Review"
      description: "Reviews code for quality and correctness"
  examples:
    - description: "Refactor auth module"
      input: "Refactor the authentication module to use JWT"
      notes: "Provide the relevant files in the workspace"
```

This field serves three purposes simultaneously:

**For `lenny/discover_agents`:** The tool returns it so runtimes can make intelligent delegation decisions — "I need something that does code review, let me find a skill match."

**For A2A Agent Card generation:** When A2A support is added, the gateway generates the `skills` array in the Agent Card directly from `agentInterface.skills`, `inputModes`, and `outputModes`. No additional configuration needed — the A2A card is a projection of data that already exists.

**For the adapter manifest:** The manifest can include `agentInterface` summaries so runtimes have calling conventions available at startup without needing to call `lenny/discover_agents`.

The `agentInterface` field is optional — existing `RuntimeType` registrations without it still work. Discovery just returns less rich results for those agents. This keeps backward compatibility while giving well-documented agents a meaningful advantage in discoverability and usability.

---

## Human (Mar 29, 2026, 10:56 PM):

* I think names should be unique across types (runtimes, templates, and external connectors) and only the id should be passed to delegate_task.
* Now that I think more about it, do we need `external_delegate` and `delegate_task` both? Does the runtime need to know if external or not? In general, the info returned by the discovery tool should not include Lenny internals that should be transparent to the runtime.
* Should we provide configuration options to limit what a runtime or template can see? Maybe we already have this (please check), but I think that allowlists and denylists might be helpful
* The calling runtime can provide not only text but, theoretically, images, etc. and multiple inputs. How is that handled via MCP?
* For runtimes and templates, the calling runtime can provide the inputs from the previous bullet point plus files to be placed in the child's workspace. How is that handled today and how should the capability =be communicated to the calling runtime? (Or, should it be communicated at all? Maybe the separation between external_delegate and delegate_task is useful after all)

---

## Claude:

Good. Now I have what I need. Let me address all five points together because they're interconnected.

---

## 1. Unique names, single `delegate_task` tool

Agreed on unique names across runtimes, templates, and external agents. The id namespace is flat and deployers are responsible for avoiding collisions — same as how connector IDs work today.

On merging `external_delegate` and `delegate_task`: yes, they should be one tool. The runtime shouldn't need to know or care whether the target is internal or external — that's a Lenny implementation detail. The gateway figures out routing from the id. One tool:

```
lenny/delegate_task(
  target: string,        // unique id — runtime, template, or external agent
  task: TaskSpec,
  lease_slice?: LeaseSlice
) → TaskHandle
```

The gateway looks up the id, determines whether it's internal (runtime/template) or external (registered connector), and handles accordingly. Internal delegation uses the existing pod allocation path. External delegation uses the `external_delegate` logic internally — but that distinction is never exposed to the runtime.

---

## 2. Discovery visibility controls

The existing design already has `allowedRuntimes` and `allowedConnectors` on the delegation lease as **security boundaries** (non-extendable). This is the right mechanism and it naturally constrains discovery too: `lenny/discover_agents` should only return agents that are within the calling session's delegation lease scope. If `allowedRuntimes: ["claude-worker", "review-agent"]`, discovery returns only those two plus any external agents in `allowedConnectors`. The runtime cannot discover what it cannot call.

This is already implicit in the design but should be made explicit: **`lenny/discover_agents` is scoped by the session's delegation lease, not by what exists in the registry**. No additional allowlist/denylist mechanism is needed — the lease already is that mechanism, and it's enforced at the gateway level rather than relying on the runtime to respect a hint.

---

## 3 & 4 & 5. Multimodal inputs, file inputs, and capability communication — these belong together

You've identified a real gap. Today `delegate_task` takes a `task` spec that is essentially a text prompt plus a file export spec (Section 8.8). There's no defined way to pass images, audio, or other modalities as task inputs.

The answer is that the `TaskSpec.input` field should use the `OutputPart` model we already defined — the same typed, heterogeneous array used for outputs:

```json
{
  "target": "review-agent",
  "task": {
    "input": [
      { "type": "text", "mimeType": "text/plain",
        "inline": "Review this code for security issues" },
      { "type": "image", "mimeType": "image/png",
        "inline": "<base64>" },
      { "type": "data", "mimeType": "application/json",
        "inline": { "focusArea": "auth", "severity": "high" } }
    ],
    "workspaceFiles": {
      "export": [{ "glob": "src/auth/**", "destPrefix": "/" }]
    }
  }
}
```

`input` is a parts array. `workspaceFiles` is the existing export spec from Section 8.8. These are separate because they serve different purposes — `input` is the prompt/context delivered to the agent's LLM, `workspaceFiles` is what ends up on the child's filesystem. A calling runtime can provide both, either, or neither.

**On communicating capability to the calling runtime:** this is where `agentInterface.inputModes` from the previous discussion earns its place. The discovery result tells the calling runtime what a target agent accepts:

```json
{
  "id": "review-agent",
  "agentInterface": {
    "inputModes": [
      { "type": "text/plain" },
      { "type": "image/png", "description": "Screenshots or diagrams" }
    ],
    "supportsWorkspaceFiles": true
  }
}
```

`supportsWorkspaceFiles: true` is the explicit signal that the target accepts file exports into its workspace — distinguishing it from agents that only accept inline input. This is important because workspace file delivery requires Lenny's full pod setup path (upload, materialize, setup commands), whereas inline-only inputs can work with concurrent-stateless pods or even external agents. The calling runtime needs to know which path applies.

**Does this resurrect the need for two tools?** Not quite. The single `delegate_task` handles all cases. But `agentInterface.supportsWorkspaceFiles` tells the calling runtime whether the workspace export portion of `TaskSpec` will be honored. For external agents, `supportsWorkspaceFiles` is always false — there's no workspace to materialize on an external HTTP endpoint. For internal runtimes and templates, it reflects whether the runtime has `executionMode: session` or `task` (which support workspaces) versus `concurrent` stateless (which does not).

So the full picture: one tool, the target id is opaque, the discovery result tells you what the target accepts, and the `TaskSpec` structure carries both inline multimodal input and workspace file exports in clearly separated fields.

---

## Human (Mar 29, 2026, 11:02 PM):

Ok this makes sense, I think. However, I think there is a gap in Discovery visibility controls around templates. Templates are defined after deployment, and what templates a runtime is allowed to see should be configurable after deployments too. Suggest a model that includes runtimes, connectors, and templates.

---

## Claude:

The gap is real. The existing `allowedRuntimes` and `allowedConnectors` on the delegation lease are resolved at session creation time and are static for the session's lifetime. Templates are created post-deployment via the admin API, so they'd never appear in a lease that was computed before the template existed. We need a model that handles dynamic membership.

---

## The Right Model: `DelegationPolicy` as a First-Class Resource

Rather than listing specific ids on the lease directly, introduce a named `DelegationPolicy` resource managed via the admin API. The policy defines rules for what targets a runtime can access. The lease references a resolved snapshot of the policy at session creation time for runtimes and connectors (stable), but evaluates tag-based template rules at delegation time (dynamic).

```yaml
POST /v1/admin/delegation-policies

id: orchestrator-policy
rules:
  runtimes:
    allow: ["claude-worker", "review-agent"]   # explicit list, or ["*"] for all
  templates:
    allow:
      tags: ["approved"]                        # any template tagged "approved"
      ids: ["research-graph-v1"]               # plus specific ids
  connectors:
    allow: ["github", "jira"]
  externalAgents:
    allow: []                                   # empty = no external delegation
```

Templates get a `tags` field at creation time:

```yaml
POST /v1/admin/session-templates

id: research-graph-v2
tags: ["approved", "research"]
runtime: langgraph-runtime
...
```

The `RuntimeType` references a policy:

```yaml
name: claude-orchestrator
delegationPolicyRef: orchestrator-policy
```

---

## How It Works at Runtime

At **session creation**, the gateway resolves the policy and records the concrete set of allowed runtimes, connectors, and current matching templates into the delegation lease. For templates, it also records the tag rules — not just the ids that matched at session creation.

At **delegation time** (`lenny/delegate_task`), the gateway validates the target against the lease in two passes:
1. Is the target in the explicitly resolved id set? → allow
2. If not, does the target's current tags match the policy's tag rules? → allow (catches templates created after session start)

This means a new template tagged `approved` created while a long-running orchestrator session is in progress is automatically accessible to that session without any lease update. A template that had the `approved` tag removed is no longer accessible on next delegation attempt.

At **discovery time**, `lenny/discover_agents` applies the same two-pass logic to filter results. The runtime only sees what it can actually call.

---

## Lease Narrowing With Policies

Child sessions need strictly narrower policies than parents. The narrowing works as follows: a child's `delegationPolicyRef` must be a subset of the parent's resolved policy. The gateway enforces this by intersecting the parent's allowed target set with the child's policy at delegation time. A child cannot reference an external agent that wasn't in the parent's policy, and cannot use a template the parent's policy doesn't cover.

For explicit overrides at session creation, the client can pass `maxDelegationPolicy` to further restrict what the runtime's default policy allows — useful for sandboxing specific sessions more tightly than the runtime's default:

```json
{
  "runtime": "claude-orchestrator",
  "maxDelegationPolicy": {
    "templates": { "allow": { "ids": ["research-graph-v1"] } },
    "connectors": { "allow": ["github"] }
  }
}
```

The effective policy is the intersection of the runtime's `delegationPolicyRef` and the session-level `maxDelegationPolicy`. The session can never grant itself more than the runtime's policy allows.

---

## What This Replaces

The existing `allowedRuntimes`, `allowedConnectors`, and `allowedPools` fields on the delegation lease become derived quantities computed from the `DelegationPolicy` rather than fields set directly by callers. The lease still carries the resolved set for performance, but `DelegationPolicy` is the authoritative source. This is cleaner and makes post-deployment configuration changes — new templates, new connectors, revised tag sets — automatically reflected without touching the lease schema.

---

## Human (Mar 30, 2026, 6:06 PM):

Thanks. I like the idea of policies. I wonder, however if we aren't using a clear definition for runtimes and templates a bit. Runtimes don't generally know about templates at configuration time. At least some constraints on runtimes may need to be setup during deployment, instead of the API. 
I also wonder if we should call them Base Runtimes and Derived Runtimes or similar, as they are both runtimes at the end of the day. Suggest better names. A derived runtime should abide by the policy of the base image always, plus its own policy.

---

## Claude:

Good clarification. Let me separate the naming question from the policy question because they're distinct issues.

---

## Naming

"Base runtime" and "derived runtime" imply an inheritance or layering relationship, which is partially right but not quite accurate. A template doesn't modify the runtime — it just pre-configures how it's invoked. The relationship is more like a class and an instance, or a blueprint and a deployment.

Better options:

**`Runtime` and `Agent`** — A `Runtime` is the deployable unit (the container image, isolation profile, capabilities). An `Agent` is a named, configured invocation of a runtime (workspace preset, input schema, delegation policy, metadata). This maps well to how people already think: "I'm deploying the LangGraph runtime" vs "I'm creating a Research Agent that runs on it." The word "agent" is also what external callers see — they call agents, not runtimes directly.

**`RuntimeType` and `AgentDefinition`** — More explicit. `RuntimeType` is already used in the current design, so this is a minimal rename of "template" to `AgentDefinition`. Signals that an `AgentDefinition` *is* a fully defined agent, not just a template for session creation.

**`Runtime` and `Deployment`** — Borrowing from web deployment terminology. Clean but "Deployment" already means something specific in Kubernetes.

My recommendation is **`Runtime` and `Agent`**. It's the most natural language, maps to how external callers think about discovery (`lenny/discover_agents` returns `Agent` instances), and draws a clean line: `Runtime` is infrastructure concern, `Agent` is product/user concern.

Under this naming: what was `SessionTemplate` becomes `AgentDefinition`. What was `RuntimeType` becomes `Runtime`. Bare runtime invocations (calling a `Runtime` directly without a pre-configured `AgentDefinition`) remain possible but are now an advanced/internal path rather than the primary one.

---

## On the Policy Timing Problem

You're right that a `Runtime` can't reference an `AgentDefinition` at configuration time — `AgentDefinition`s don't exist yet at deployment time. The policy needs to work at two different granularities:

**Runtime-level policy** — set at deployment time via Helm/config. Defines what categories of things this runtime is ever allowed to delegate to. This is a coarse capability constraint: "this runtime may call other runtimes, may call agents tagged `approved`, may use connectors `github` and `jira`." It's set before any `AgentDefinition`s exist and doesn't name specific agents.

**Agent-level policy** — set post-deployment via admin API on each `AgentDefinition`. Inherits from the base `Runtime`'s policy and can only restrict, never expand. "This specific research agent may only call agents tagged `summarizer`."

The effective policy at delegation time is the intersection of both. An `AgentDefinition` can never grant permissions its underlying `Runtime` doesn't have. A `Runtime`'s policy constrains all `AgentDefinition`s built on top of it.

This gives you the right separation: infrastructure operators set `Runtime` policies at deployment time; product teams configure `AgentDefinition` policies post-deployment via the admin API.

---

## Putting It Together

```yaml
# Set at deployment time (Helm/config) — no AgentDefinitions exist yet
runtimes:
  - name: langgraph-orchestrator
    image: registry.example.com/langgraph:latest
    delegationPolicy:
      agents:
        allow:
          tags: ["approved"]          # may call any agent tagged approved
      connectors:
        allow: ["github", "jira"]     # may use these connectors
      runtimes:
        allow: []                     # may not call bare runtimes directly
```

```yaml
# Set post-deployment via admin API
POST /v1/admin/agents

id: research-agent
runtime: langgraph-orchestrator
tags: ["approved", "research"]
delegationPolicy:
  # Inherits langgraph-orchestrator's policy, further restricted:
  agents:
    allow:
      tags: ["summarizer"]            # can only call summarizer agents
  connectors:
    allow: ["github"]                 # jira removed from inherited set
```

The gateway enforces: `research-agent`'s effective policy = `langgraph-orchestrator`'s policy ∩ `research-agent`'s own policy. Any attempt to expand beyond the `Runtime`'s policy is rejected at admin API time, not silently ignored at delegation time.

---

## Human (Mar 30, 2026, 6:16 PM):

I don't quite like the names you suggested. Right now, one can call a runtime without the need to create an instance. Moreover, runtimes are agents themselves potentially. And not all runtimes and instances of runtimes will always be agents.

---

## Claude:

You're right, and the naming problem is harder than it looks because the concepts genuinely overlap. Let me think about what the actual distinction is.

A `RuntimeType` (current name) is a deployable execution unit — a container image plus capability declarations plus resource requirements. It can be invoked directly. An `AgentDefinition`/`SessionTemplate` (current name) is a pre-configured invocation profile on top of a runtime — a named set of workspace defaults, input schema, and policy constraints. Both can behave as agents. Neither is exclusively an agent.

The real distinction is simpler: one is a **type**, the other is a **configured instance of that type**. Standard software terminology for this is:

- `Runtime` (the type/class) — what you deploy
- `RuntimeConfig` or `RuntimeInstance` — a named configuration of a runtime

But those names are equally awkward. Another angle: in infrastructure terms one is a **definition** and the other is a **profile**:

- `RuntimeDefinition` — the executable unit
- `RuntimeProfile` — a named, pre-configured invocation of a definition

Or borrowing from container/Kubernetes thinking:
- `RuntimeImage` or just `Runtime` — what you register
- `RuntimeConfig` — what you configure on top of it

Before proposing a final name, I want to make sure I understand the constraint correctly. You're saying:

1. A runtime can be called directly without a config on top of it
2. A runtime is itself potentially an agent
3. Not everything called via these mechanisms is an agent

Does that mean you want to avoid the word "agent" in both names entirely? Or is it acceptable in one but not the other? That would help narrow it down.

---

## Human (Mar 30, 2026, 6:24 PM):

I don't want agent in the name bc these can be more than agents. Runtime is the right term.
The problem is that templates (the current name) aren't that much different than runtimes. They are just a preconfigured version of runtimes. We either call everything a runtime, knowing that some of them are just pre-configured versions of other runtimes, or we find a more descriptive name to differentiate them

---

## Claude:

The cleanest solution is to call everything a `Runtime` and use composition to express the relationship. A runtime that is pre-configured on top of another runtime simply has a `baseRuntime` field. Runtimes without a `baseRuntime` are standalone. Runtimes with one inherit and restrict from it.

```yaml
# Standalone runtime — no base
name: langgraph-runtime
image: registry.example.com/langgraph:latest
capabilities: ...
delegationPolicy: ...

# Pre-configured runtime — has a base
name: research-pipeline
baseRuntime: langgraph-runtime
workspaceDefaults:
  files: [{ path: "agent.py", content: "..." }]
  setupCommands: ["pip install -r requirements.txt"]
inputSchema: ...
delegationPolicy: ...     # can only restrict, never expand base's policy
publishedMetadata: ...
```

This is clean for several reasons:

- Everything is a `Runtime`. The API surface is unified — `GET/POST/PUT/DELETE /v1/admin/runtimes` handles both. No need for separate concepts.
- The `baseRuntime` field makes the relationship explicit without implying anything about what the runtime *is* (agent, pipeline, tool, etc.).
- Discovery, delegation, and policy all work identically regardless of whether a runtime has a base or not.
- A runtime with a `baseRuntime` cannot have an `image` — that's a validation rule at the API level. The image comes from the base.
- Policy inheritance is clear: a derived runtime's effective policy is always the intersection of its own policy and its base's policy. The gateway validates this at creation time and rejects any derived runtime whose policy attempts to expand beyond its base.

The existing `SessionTemplate` concept disappears entirely and is replaced by a `Runtime` with a `baseRuntime` field. What was `RuntimeType` becomes just `Runtime`. The distinction between the two current concepts is expressed through the presence or absence of `baseRuntime`, not through different resource types.

One practical implication: `lenny/discover_agents` returns all runtimes the session's delegation policy permits — standalone and derived alike. The calling runtime doesn't need to know which kind it's looking at.

---

## Human (Mar 30, 2026, 6:28 PM):

That makes sense. One restriction is that base runtimes cannot be modified via the API. Right?

---

## Claude:

Not quite — I'd push back slightly on that.

Base runtimes can and should be modifiable via the admin API, but with stricter rules and explicit impact visibility. Making them entirely immutable creates operational problems: you'd need a Helm deploy to fix a typo in a capability declaration, update a default egress profile, or rotate a default credential pool reference. That's too rigid for what is otherwise a dynamically managed system.

The right constraints are:

**What can be changed on a base runtime via API:**
- Capability flags, resource class defaults, egress profile, credential pool references
- `delegationPolicy` — but the gateway must validate that no existing derived runtime's policy would become invalid after the change. If a derived runtime's policy is already more restrictive than the new base policy, it's fine. If the new base policy would make a derived runtime's policy *impossible* (e.g., removing a connector that a derived runtime explicitly allows), the API rejects the change and returns which derived runtimes are affected.
- `publishedMetadata`

**What cannot be changed on a base runtime via API:**
- `image` — changing the image is a new version, not an edit. Handled via versioning (a new runtime registration) not mutation.
- `name` — names are identifiers referenced by derived runtimes. Changing one would silently break all derived runtimes that reference it.

**What cannot be changed on a base runtime if derived runtimes exist:**
- `capabilities` that derived runtimes depend on — e.g., removing `checkpoint: true` if a derived runtime relies on checkpointing. The API should validate and reject with a clear error listing affected derived runtimes.

The underlying principle: base runtimes are more constrained than derived runtimes when it comes to mutations, because changes propagate to everything built on top of them. But "more constrained" is different from "immutable." The admin API enforces the constraints and provides clear feedback about impact — it doesn't simply refuse all changes.

---

## Human (Mar 30, 2026, 6:33 PM):

Revise your previous compilation of updates to the tech spec based on the follow up conversation

---

## Claude:

This will be thorough. Let me produce the full revised compilation incorporating everything from the follow-up conversation.

---

# Lenny Technical Spec — Revised Changes from Design Review

## How to Read This Document

- **V1 Must-Have (Breaking)** — must be in v1; impossible to fix after the first community runtime ships
- **V1 Must-Have (Feature)** — must ship with v1 to meet stated goals
- **V1 Should-Have** — significant value; should ship but won't break compatibility if deferred
- **Planned / Post-V1** — document intent now so data models accommodate it; implement later
- **Explicit Non-Decisions** — things Lenny deliberately will not do

---

## Part 1: V1 Must-Have — Breaking Changes

---

### 1.1 Replace Custom CRDs With `kubernetes-sigs/agent-sandbox`

**What changes:** Replace `AgentPool`, `AgentPod`, and `AgentSession` CRDs and the WarmPoolController with the upstream `SandboxTemplate`, `SandboxWarmPool`, `SandboxClaim`, and `Sandbox` CRDs from `kubernetes-sigs/agent-sandbox` (launched as a formal Kubernetes SIG Apps subproject at KubeCon Atlanta, November 2025).

**Mapping:**

| Lenny CRD | Agent-Sandbox CRD |
|---|---|
| `AgentPool` | `SandboxTemplate` |
| `AgentPod` | `Sandbox` + `SandboxWarmPool` |
| `AgentSession` | `SandboxClaim` |
| WarmPoolController | Upstream SIG Apps controller |

**Implementation note:** The `SandboxClaim` mechanism must be verified to provide the same optimistic-locking guarantee Lenny currently gets from the status PATCH approach before committing. Task mode and concurrent mode remain Lenny-layer concerns — agent-sandbox hosts the pods without knowing about multi-task reuse or intra-pod concurrency.

**Affects:** Sections 4.6, 6.1, 6.2, 6.3, Phase 1 and Phase 3 of the build sequence.

---

### 1.2 Runtime Naming: `Runtime` Replaces `RuntimeType` and `SessionTemplate`

**What changes:** The distinction between `RuntimeType` (deployable execution unit) and `SessionTemplate` (pre-configured invocation) is collapsed into a single unified concept: `Runtime`. Everything is a `Runtime`. The difference between a standalone runtime and a pre-configured one is expressed through the presence or absence of a `baseRuntime` field.

```yaml
# Standalone runtime — no base, has an image
name: langgraph-runtime
image: registry.example.com/langgraph:latest
capabilities: ...
delegationPolicyRef: orchestrator-policy

# Derived runtime — pre-configured on top of a base
name: research-pipeline
baseRuntime: langgraph-runtime
workspaceDefaults:
  files: [{ path: "agent.py", content: "..." }]
  setupCommands: ["pip install -r requirements.txt"]
inputSchema: ...
delegationPolicyRef: research-policy
publishedMetadata: ...
agentInterface: ...
```

**Rules:**
- A runtime with a `baseRuntime` cannot have an `image` — the image comes from the base. Validated at the API level.
- `name` is unique across all runtimes (standalone and derived) in a flat namespace. A single id is used everywhere — discovery, delegation, policy.
- Derived runtimes inherit the base's effective policy and can only restrict, never expand.
- Policy inheritance: `effective_policy = base_runtime_policy ∩ derived_runtime_policy`. The gateway validates this at creation time.

**Base runtime mutability constraints:**
- `image` — immutable via API. Changing the image constitutes a new runtime version, registered as a new standalone runtime.
- `name` — immutable via API. Names are identifiers referenced by derived runtimes; renaming would silently break all runtimes with `baseRuntime` pointing to the old name.
- All other fields (capabilities, delegation policy ref, egress profile, credential pool reference, published metadata) — mutable via API, subject to impact validation. If a proposed change to a base runtime's delegation policy would invalidate an existing derived runtime's policy, the API rejects the change and returns the list of affected derived runtimes.

**What disappears:** `SessionTemplate` as a distinct concept. The admin API has one resource type: `Runtime`. `GET/POST/PUT/DELETE /v1/admin/runtimes` handles both standalone and derived runtimes.

**Affects:** Sections 5.1 (RuntimeType → Runtime throughout), Section 15.1 and 15.2 (API surface), Phase 1 (core types).

**Context for A2A:** `publishedMetadata` on a derived runtime can include an A2A agent card at `visibility: public`. The A2A card is generated from `agentInterface` fields (see 1.6). No change to this mechanism from the naming refactor.

---

### 1.3 Adapter-Binary Protocol Replaced by Multi-Server MCP + Lifecycle Channel

**What changes:** The stdin/stdout JSON Lines binary protocol (Section 15.4.1) is replaced by a two-part model.

**Part A — Multiple focused local MCP servers** exposed by the adapter to the runtime (intra-pod, stdio or Unix socket transport):

- **Platform MCP server** — gateway-backed tools: `lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`, `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`
- **One MCP server per authorized connector** — each connector the session's delegation policy permits gets its own independent MCP server, with its own capability negotiation and its own auth context. Tools are scoped to that connector's namespace (e.g., `create_pr`, `list_issues` on the GitHub connector's server). The adapter proxies calls to the gateway which holds OAuth tokens. There is no aggregated connector proxy server — aggregation is not lossless.

**No workspace MCP server.** The workspace is materialized to `/workspace/current` before the runtime starts. The runtime reads files directly from the filesystem. An MCP server adds no value here and is not provided.

**Part B — Lifecycle channel** — a small separate channel (second stdio stream pair) for platform lifecycle events that do not belong in MCP:

```
Adapter → Runtime:  lifecycle_capabilities, checkpoint_request, interrupt_request,
                    credentials_rotated, terminate
Runtime → Adapter:  lifecycle_support, checkpoint_ready, interrupt_acknowledged,
                    credentials_acknowledged
```

Everything in the lifecycle channel is optional. Runtimes that don't open it operate in fallback-only mode.

**Runtime integration layers:**
- **Layer 0** — any MCP-capable binary. Zero Lenny knowledge. Fallback lifecycle only.
- **Layer 1** — use the adapter's MCP servers (platform tools and connector servers). Standard MCP, no Lenny SDK required.
- **Layer 2** — open the lifecycle channel and declare supported hooks.

**Adapter manifest:** Before the runtime process starts, the adapter writes `/run/lenny/adapter-manifest.json` — a file listing all available MCP server connection details (socket paths or stdio pipe identifiers) and metadata per server. The manifest is regenerated for every task execution because connector authorization varies per delegation lease. The runtime reads this at startup to discover which servers are available without any dynamic discovery protocol.

**Why not one aggregated connector server:** MCP capability negotiation is per-server in the spec. Sampling flows correctly only per connection. Future MCP features introducing per-server identity or per-connection auth would break silently through an aggregated proxy. Each connector gets its own server with independent capability negotiation.

**Affects:** Section 15.4 and 15.4.1 (full replacement), Section 4.7 (Runtime Adapter), Phase 2 of build sequence.

**Context for A2A:** The intra-pod MCP interface is entirely separate from the external-facing protocol layer. Runtime authors speak MCP to adapter servers. If A2A support is added in the future, the adapter would additionally serve A2A endpoints — the runtime would need to speak A2A to use them. A2A is not transparent to runtime authors; they opt into it. The intra-pod model and the external gateway model are separate boundaries.

---

### 1.4 Single `lenny/delegate_task` Tool — No `external_delegate`

**What changes:** There is one delegation tool: `lenny/delegate_task`. The target id is opaque to the runtime — it does not know or need to know whether the target is a standalone runtime, a derived runtime, or an external registered agent. The gateway resolves the id and routes accordingly.

```
lenny/delegate_task(
  target: string,        // runtime name — unique across all runtimes and external agents
  task: TaskSpec,
  lease_slice?: LeaseSlice
) → TaskHandle
```

`TaskSpec` carries two separate input channels:

```json
{
  "input": [
    { "type": "text", "mimeType": "text/plain",
      "inline": "Analyze this codebase for security issues" },
    { "type": "image", "mimeType": "image/png", "inline": "<base64>" },
    { "type": "data", "mimeType": "application/json",
      "inline": { "severity": "high" } }
  ],
  "workspaceFiles": {
    "export": [{ "glob": "src/auth/**", "destPrefix": "/" }]
  }
}
```

`input` is an `OutputPart` array — the same typed heterogeneous format used for outputs (text, images, structured data, file references). This is the prompt/context delivered to the child agent.

`workspaceFiles` is the existing file export spec (Section 8.8) — files placed on the child's filesystem. These are separate because they serve different purposes. A calling runtime can provide both, either, or neither.

**Internal routing:** The gateway looks up the target name. If it resolves to an internal runtime (standalone or derived), it uses the pod allocation path with workspace materialization. If it resolves to an external registered agent, it uses the external delegation path (HTTP call to the external agent's endpoint). The runtime never knows the difference.

**`external_delegate` is removed** as a separate concept. External agents are registered in the same registry as internal runtimes, reachable by the same tool, subject to the same delegation lease controls.

**Affects:** Section 8.2 (Delegation Mechanism), Section 8.9 (TaskResult schema — input field added), Section 9.1 (MCP integration table).

---

### 1.5 `ExternalProtocolAdapter` Interface on the Gateway

**What changes:** The gateway's external MCP implementation is refactored from monolithic MCP Fabric into a thin `ExternalProtocolAdapter` interface. MCP is the first implementation.

```go
type ExternalProtocolAdapter interface {
    CreateSession(ctx, req ExternalRequest) (Session, error)
    StreamEvents(ctx, session, ch <-chan OutputPart) error
    MapTaskLifecycle(state SessionState) ExternalTaskState
    ContentType() string
}
```

**Accommodated protocols:**
- **MCP** — first implementation, ships with v1
- **A2A** — second implementation, planned post-v1. Inbound: gateway serves `POST /a2a/{runtimeName}/tasks`. Outbound: external agents registered as connectors, called via `lenny/delegate_task`.
- **Agent Protocol (AP)** — third implementation, planned post-v1. AP defines `POST /ap/v1/agent/tasks` and `POST /ap/v1/agent/tasks/{task_id}/steps`. Maps to Lenny's session lifecycle via this interface.

**Task state model note:** AP, A2A, and MCP have different task state models. The internal session state machine is the canonical model. Each adapter translates to and from it. No adapter-specific state leaks into the core session model.

**Affects:** Section 4.1 (Gateway Edge internals), Section 9.1.

---

### 1.6 `agentInterface` Field on Runtime

**What changes:** Both standalone and derived `Runtime` resources gain an optional `agentInterface` field describing how the runtime should be called.

```yaml
agentInterface:
  description: "Analyzes codebases and produces refactoring plans"
  inputModes:
    - type: "text/plain"
      description: "Natural language task description"
    - type: "image/png"
      description: "Architecture diagrams or screenshots"
    - type: "application/json"
      schema: "{ 'focusArea': 'string', 'severity': 'string' }"
  outputModes:
    - type: "text/plain"
      role: "primary"
    - type: "application/json"
      role: "structured_summary"
  supportsWorkspaceFiles: true   # accepts workspaceFiles in TaskSpec
  skills:
    - id: "refactor"
      name: "Code Refactoring"
      description: "Identifies and implements code improvements"
    - id: "review"
      name: "Code Review"
  examples:
    - description: "Review auth module"
      input: "Review the authentication module for security vulnerabilities"
      notes: "Place relevant source files in workspace"
```

`agentInterface` serves three purposes simultaneously:

1. **Discovery:** `lenny/discover_agents` returns `agentInterface` in results so calling runtimes can make intelligent delegation decisions based on capabilities.
2. **A2A Card generation:** When A2A is implemented, the gateway generates the Agent Card's `skills`, `inputModes`, and `outputModes` fields directly from `agentInterface`. No separate A2A-specific configuration needed.
3. **Adapter manifest:** Included in `/run/lenny/adapter-manifest.json` so runtimes have calling conventions at startup.

`supportsWorkspaceFiles: true` is particularly important — it signals that `workspaceFiles` in `TaskSpec` will be honored, distinguishing internal runtimes (which support full workspace setup) from external agents (which don't). Calling runtimes use this to decide whether to pass files via workspace export or via `input` parts.

**Affects:** Section 5.1 (Runtime registration schema), Section 8.9 (TaskSpec), Section 9.

---

### 1.7 `DelegationPolicy` as a First-Class Resource

**What changes:** The existing `allowedRuntimes`, `allowedConnectors`, and `allowedPools` fields on the delegation lease are replaced by a named `DelegationPolicy` resource. Policies express rules that can match targets by id or by tag. The lease carries a resolved snapshot plus the tag rules for dynamic evaluation.

```yaml
POST /v1/admin/delegation-policies

id: orchestrator-policy
rules:
  runtimes:
    allow:
      ids: ["review-agent"]
      tags: ["approved"]     # any runtime tagged "approved" at delegation time
  connectors:
    allow:
      ids: ["github", "jira"]
  externalAgents:
    allow: []                # empty = no external delegation permitted
```

Runtimes reference a policy:

```yaml
name: langgraph-orchestrator
delegationPolicyRef: orchestrator-policy
```

**Policy timing — two levels:**

**Runtime-level policy** (set at deployment time via Helm/config): Defines coarse capability constraints for a runtime before any derived runtimes exist. Uses tag rules rather than specific ids since derived runtimes aren't registered yet. Example: "this runtime may call runtimes tagged `approved` and connectors `github` and `jira`."

**Derived runtime policy** (set post-deployment via admin API): Inherits from the base runtime's policy and can only restrict, never expand. The effective policy is `base_runtime_policy ∩ derived_runtime_policy`. Validated at creation time — a derived runtime whose policy attempts to expand beyond its base's policy is rejected.

**Discovery scoping:** `lenny/discover_agents` returns only targets permitted by the calling session's effective delegation policy. The runtime only sees what it can actually call.

**Dynamic template evaluation:** At delegation time, the gateway evaluates tag rules against the target's current tags — not just its tags at session creation time. A new derived runtime tagged `approved` created while a long-running session is in progress is automatically accessible without a lease update. A runtime that loses the `approved` tag is no longer accessible on the next delegation attempt.

**Session-level override:** Callers can pass `maxDelegationPolicy` at session creation to further restrict the runtime's default policy. Effective policy = `runtime_policy ∩ session_max_policy`. Sessions can never grant themselves more than the runtime's policy allows.

**Affects:** Section 8.3 (Delegation Lease — `allowedRuntimes`, `allowedConnectors` replaced), Section 5.1 (Runtime registration schema).

---

### 1.8 `executionMode` on Pool Configuration

**What changes:** Pool configuration gets an `executionMode` field from day one, even if only `session` is fully implemented initially:

```yaml
executionMode: session   # session | task | concurrent
```

Removing `graph` as a separate mode — graph-aware runtimes are just session-mode runtimes that emit internal step traces through the observability protocol. The platform does not distinguish them.

**Affects:** Section 5.2 (Pool Configuration), Phase 1 (core types — must exist in schema from day one).

---

### 1.9 Internal `OutputPart` Format

**What changes:** `agent_text` streaming event replaced by `agent_output` carrying an `OutputPart` array. `TaskResult.output` flat struct replaced by `parts: []OutputPart`. `TaskSpec.input` also uses `OutputPart` arrays (see 1.4).

`OutputPart` schema:
```json
{
  "id": "part_abc123",
  "type": "text",
  "mimeType": "text/plain",
  "inline": "content here",
  "ref": "artifact://...",
  "annotations": { "role": "primary", "final": true },
  "parts": [],
  "status": "streaming | complete | failed"
}
```

- `type` is an open string — not a closed enum. Unknown types pass through opaquely.
- `mimeType` handles encoding separately from semantic type.
- `inline` vs `ref` are properties, not type distinctions.
- `annotations` is an open metadata map.
- `parts` allows nesting.
- `id` enables part-level streaming updates.

An SDK helper `from_mcp_content(blocks)` converts MCP content blocks to `OutputPart` arrays for runtime authors who produce MCP-native output.

**Affects:** Section 15.4.1, Section 8.9 (TaskResult), Section 7.2 (session message types).

---

## Part 2: V1 Must-Have — Feature Changes

---

### 2.1 Task Pod Execution Mode

**What changes:** A pool with `executionMode: task` reuses pods across sequential tasks. Between tasks, the adapter performs a scrub: workspace wiped, `/sessions/` tmpfs cleared, environment reset, credentials re-evaluated. The pod lifecycle is decoupled from the task lifecycle.

**Security requirement:** Explicit deployer acknowledgment required. The scrub protocol must be verified before a pod is returned to available state.

**Agent-sandbox note:** Agent-sandbox sees a long-lived claimed sandbox. Multi-task reuse is invisible to agent-sandbox.

**Affects:** Sections 6.1, 6.2, 7, 5.2.

---

### 2.2 Concurrent Execution Mode

**What changes:** A pool with `executionMode: concurrent` allows multiple simultaneous task slots per pod, up to `maxConcurrent`. For runtimes with expensive shared in-memory state (typically self-hosted models).

**Two sub-variants:**

**Concurrent-workspace:** Each slot gets its own workspace materialized by the gateway. Gateway tracks per-slot lifecycle. Multiple workspaces coexist within the same pod. Isolation is process-level and filesystem-level within the container — explicitly weaker than session mode. Deployer acknowledgment required.

**Concurrent-stateless:** No workspace materialization. Pod receives requests directly through its local MCP server. Kubernetes service routing handles load distribution. Pod readiness probe reflects slot availability (`maxConcurrent` acts as a semaphore). `PoolScalingController` watches `active_slots / (pod_count × maxConcurrent)` for scaling decisions.

**Note:** Truly stateless runtimes with no workspace and no expensive shared state should be registered as external connectors rather than Lenny-managed pods. Concurrent mode is for runtimes with genuinely expensive shared state.

**Affects:** Sections 5.2, 6.1, 6.2, 4.1.

---

### 2.3 `ConnectorDefinition` as a First-Class Resource

**What changes:** Connector configuration becomes a first-class resource managed via the admin API. Each connector has an id that participates in the same flat namespace as runtimes — callers use the same `lenny/delegate_task` tool regardless of whether the target is an internal runtime or an external connector.

External agents (A2A connectors, AP connectors) are registered here alongside tool connectors (GitHub, Jira). The `protocol` field distinguishes them.

**Affects:** Section 4.3, Section 9.3.

---

### 2.4 `publishedMetadata` on Runtime

**What changes:** `Runtime` resources (both standalone and derived) gain a `publishedMetadata` array. Each entry is an opaque key-value pair with a content type and a visibility level. Gateway serves entries at well-known URLs as opaque pass-through — no parsing or validation.

```yaml
publishedMetadata:
  - key: agent-card
    contentType: application/json
    visibility: public
    value: '...'
```

Visibility levels: `internal` (intra-cluster, requires valid JWT), `tenant` (filtered by tenant_id), `public` (no auth required, externally reachable).

**Context for A2A:** An A2A agent card is one possible `publishedMetadata` entry. When A2A is implemented, the gateway generates the card's `skills` and `inputModes` from `agentInterface` and serves it at `/.well-known/agent.json` for runtimes with a public `agent-card` entry. No named `agentCard` field — the generic `publishedMetadata` mechanism is the correct abstraction.

**Affects:** Section 5.1.

---

### 2.5 `lenny/discover_agents` Tool

**What changes:** Tool in the adapter's platform MCP server. Returns all runtimes (standalone and derived) and external agents the calling session's delegation policy permits.

Each result is a self-contained descriptor:

```json
{
  "id": "research-pipeline",
  "kind": "derived-runtime",
  "baseRuntime": "langgraph-runtime",
  "agentInterface": { ... },
  "a2aEndpoint": "http://adapter-a2a/agents/research-pipeline",
  "a2aCardUrl": "http://adapter-a2a/agents/research-pipeline/.well-known/agent.json"
}
```

`id` is exactly what goes into `lenny/delegate_task`. `a2aEndpoint` and `a2aCardUrl` are populated only when A2A is supported and the runtime exposes it — omitted otherwise in v1.

Each agent gets its own A2A endpoint when A2A is added — not an aggregated endpoint. This is consistent with the conclusion that aggregation is not lossless.

**Discovery is scoped by delegation policy.** The runtime only sees what it can call. `lenny/discover_agents` is not needed for delegation by name/id — `lenny/delegate_task` accepts any authorized target id directly. Discovery is for dynamic selection at runtime.

**Pluggable:** Backed by a `AgentDiscoveryStrategy` interface. Default implementation returns everything the delegation policy permits. Deployers replace with semantic search, capability-based filtering, or curated catalogs.

**Affects:** Section 9 (new tool in platform MCP server), Section 8.

---

### 2.6 Comprehensive Admin API

**What changes:** Full consistent admin API surface. Configuration stored in Postgres, cached with short TTL in the gateway. Redis pub/sub for immediate invalidation.

**Operational plane — API-managed:**
- Runtimes (standalone and derived): `GET/POST/PUT/DELETE /v1/admin/runtimes`
- Delegation Policies: `GET/POST/PUT/DELETE /v1/admin/delegation-policies`
- Connectors: `GET/POST/PUT/DELETE /v1/admin/connectors`
- Pool Configuration: `GET/POST/PUT/DELETE /v1/admin/pools`
- Credential Pools (metadata only, secret material in K8s Secrets): `GET/POST/PUT/DELETE /v1/admin/credential-pools`
- Tenants: `GET/POST/PUT/DELETE /v1/admin/tenants`
- Quotas and Rate Limits: `GET/PUT /v1/admin/tenants/{id}/quotas`
- User Role Assignments: `GET/POST/DELETE /v1/admin/tenants/{id}/users`
- Egress Profiles: `GET/POST/PUT/DELETE /v1/admin/egress-profiles`
- Experiment Definitions: `GET/POST/PUT/DELETE /v1/admin/experiments`
- Scaling Policies: `GET/PUT /v1/admin/pools/{id}/scaling-policy`
- Memory Store Config: `GET/PUT /v1/admin/memory-store`
- Webhooks: `GET/PUT /v1/admin/webhooks`

**Bootstrap plane — Helm/config only:**
- Database connection strings, Redis address, MinIO endpoint, KMS key IDs
- Cluster name, namespace assignments
- Certificate paths and CA references
- `LENNY_DEV_MODE` flag
- System-wide default values
- Kubernetes object definitions (RuntimeClass, NetworkPolicy templates, PDB templates)

**CRDs become derived state:** `SandboxTemplate`/`SandboxWarmPool` CRDs are reconciled from Postgres by the PoolScalingController. `kubectl edit` is not the correct way to change pool settings.

**Admin API design constraints:**
- Same error taxonomy, pagination conventions, and OIDC auth as client-facing API
- Full resource representation with `etag` for conditional PUT
- `?dryRun=true` on all mutations
- OpenAPI spec generated from Go handler definitions
- All mutations flow through audit log

**Affects:** Section 15.1, Section 10.2, Section 11.7.

---

### 2.7 PoolScalingController

**What changes:** Pool scaling intelligence extracted into a dedicated `PoolScalingController` separate from the `WarmPoolController`. WarmPoolController manages individual pod lifecycle. PoolScalingController manages desired pool configuration.

Backed by a pluggable `PoolScalingStrategy` interface. Lenny ships a default implementation. Deployers replace with KEDA integration, cost-aware scaling, multi-armed bandit experiment allocation, or anything else.

**Default formula:**
```
target_minWarm = ceil(base_demand_p95 × variant_weight × safety_factor)
```

**Pool lifecycle phases managed automatically:** pre-warm, ramp, steady state, wind-down. All triggered by experiment status transitions with no manual intervention.

**Affects:** Section 4.6 (split into two controllers), Section 5.2.

---

### 2.8 Experiment Primitives

**What changes:** `ExperimentDefinition` managed via admin API. `ExperimentRouter` built-in `RequestInterceptor` implementation handles routing. Experiment context propagates through delegation leases. Results API aggregates eval scores by variant.

Full schema, ramp policy, health-based rollback, and pool lifecycle automation as described in the previous compilation — unchanged from prior version.

**Affects:** Section 4.8, Section 15.1, Section 11.2.

---

## Part 3: V1 Should-Have

---

### 3.1 `MemoryStore` Interface

**What changes:** A `MemoryStore` role-based storage interface. Default implementation: Postgres + pgvector. Fully replaceable — Mem0, Zep, or any other backend. Accessed via `lenny/memory_write` and `lenny/memory_query` tools in the platform MCP server.

**Technology choice explicitly deferred.** Memory layer market is not settled as of Q1 2026.

**Affects:** Section 4 (storage interfaces), Section 9 (platform MCP server tools).

---

### 3.2 Semantic Caching at the LLM Proxy

**What changes:** Optional `CachePolicy` on `CredentialPool`. Backed by a `SemanticCache` interface — default Redis implementation, fully replaceable. Disabled by default.

**Affects:** Section 4.9 (LLM Reverse Proxy subsection).

---

### 3.3 `RequestInterceptor` Extension Point

**What changes:** Formalized interceptor interface at gateway phases: `PreAuth`, `PostAuth`, `PreRoute`, `PostRoute`, `PreToolResult`, `PostAgentOutput`. Built-in interceptors: `ExperimentRouter`, `GuardrailsInterceptor` (disabled by default — Lenny ships no guardrail logic, hook only).

**Affects:** Section 1.3 of the critique, Section 4.8.

---

### 3.4 Agent Evaluation Hooks

**What changes:** `POST /v1/sessions/{id}/eval` accepts scored results from external tools. `POST /v1/sessions/{id}/replay` re-runs a session against a different runtime version. Lenny stores scores and surfaces them in dashboards and experiment results. No built-in eval logic.

**Affects:** Section 15.1, Section 16.

---

### 3.5 `CredentialRouter` Interface

**What changes:** Credential pool selection logic becomes a pluggable `CredentialRouter` interface. Default: existing least-loaded/round-robin/sticky-until-failure. Deployers implement cost-aware, latency-based, or fully custom routing. Disabled by default, opt-in per pool.

**Affects:** Section 4.9.

---

## Part 4: Planned / Post-V1

---

### 4.1 A2A Full Support

**What to document now:**
- `ExternalProtocolAdapter` is the mechanism. MCP is implementation one. A2A is implementation two.
- Inbound: gateway serves `POST /a2a/{runtimeName}/tasks`.
- Outbound: external A2A agents registered as connectors, callable via `lenny/delegate_task`.
- Discovery: `lenny/discover_agents` returns `a2aEndpoint` and `a2aCardUrl` per agent when supported.
- Adapter serves individual A2A endpoints per agent (`/agents/{id}`) — not aggregated.
- A2A agent cards generated from `agentInterface` fields automatically.
- `AuthEvaluator` must be extensible for Agent Card validation alongside OIDC tokens.
- Delegation lease schema must include `allowedExternalEndpoints` from v1 (slot exists, unused until A2A ships).
- Runtime authors who want to use A2A from within a pod must opt into it — it is not transparent.

**Note on adoption:** As of Q1 2026, A2A is under AAIF alongside MCP. Both under neutral Linux Foundation governance. A2A has slower adoption than MCP (97M monthly downloads) but AAIF governance substantially improves long-term survival odds.

---

### 4.2 Agent Protocol (AP) Support

**What to document now:**
- AP (AI Engineer Foundation / AGI Inc.) defines `POST /ap/v1/agent/tasks` and `POST /ap/v1/agent/tasks/{task_id}/steps`.
- Maps to Lenny session lifecycle via a third `ExternalProtocolAdapter` implementation.
- No changes needed to intra-pod model, adapter, or runtimes.

---

### 4.3 A2A Intra-Pod Support (Post-A2A Gateway Support)

**What to document now:**
- When A2A gateway support ships, the adapter can additionally serve A2A endpoints intra-pod.
- The adapter speaks A2A to the runtime, converts to internal protocol toward the gateway.
- The adapter-manifest would list the A2A base URL alongside MCP server endpoints.
- All A2A traffic is proxied through the adapter and gateway — runtimes never make direct external calls.
- This is architecturally consistent with the existing model: the adapter is a translation layer, whether translating MCP or A2A.
- Runtime authors must explicitly implement A2A client behavior to use this path.

---

### 4.4 Multi-Cluster Federation

**What to document now:** Session IDs must be globally unique. Storage interfaces must not assume single-cluster topology.

---

### 4.5 UI and CLI

**What to document now:** Admin API is the complete operational surface. Official CLI (`lenny-ctl`) and web portal are separate projects consuming the admin API as thin clients with zero business logic. Every operational action must be expressible via admin API — no operations should require Helm edits.

---

## Part 5: Explicit Non-Decisions

---

### 5.1 No Derived Runtime Creation From Runtimes at Call Time

Callers create derived runtimes via the admin API before calling them. There is no mechanism to define a new pre-configured runtime on the fly at session creation time. Configuration is a deployment/admin concern, not a call-time concern.

---

### 5.2 No Built-In Eval Logic

Lenny provides eval hooks and result storage. It does not implement LLM-as-judge, hallucination detection, or any other evaluation logic.

---

### 5.3 No Built-In Guardrail Logic

Lenny provides the `RequestInterceptor` hook. It does not implement content classifiers or prompt injection detection.

---

### 5.4 No Built-In Memory Extraction

Lenny provides the `MemoryStore` interface and tools. Runtimes decide what to write and how to structure it.

---

### 5.5 Hooks-and-Defaults Design Principle

Every cross-cutting AI capability (memory, caching, guardrails, evaluation, routing, scaling, discovery) follows the same pattern: defined as an interface with a sensible default implementation, all defaults disabled unless explicitly enabled, all interfaces fully replaceable. Lenny never implements AI-specific logic — that belongs to specialized ecosystem tools.

---

## Part 6: Build Sequence Changes

**Phase 1** — Use `Runtime` (not `RuntimeType`/`SessionTemplate`) as the core type. Add `executionMode` field. Add `allowedExternalEndpoints` to delegation lease schema. Add `agentInterface` to Runtime schema. Use agent-sandbox CRDs.

**Phase 2** — Replace adapter binary protocol with multi-server MCP adapter + lifecycle channel. Adapter manifest at `/run/lenny/adapter-manifest.json`. Publish as the runtime adapter specification.

**Phase 3** — Implement PoolScalingController. Implement `DelegationPolicy` resource.

**New Phase between 4 and 5** — Admin API foundation: runtime registry (standalone and derived), pool configuration, connector registry, delegation policies, tenant management. Gateway loads config from Postgres.

**Phase 9** — `lenny/delegate_task` handles both internal and external targets. External delegation data model complete even if external connectivity is limited.

**Phase 10** — `lenny/discover_agents` with full policy scoping and `agentInterface` integration. Adapter manifest includes `agentInterface` summaries.

**New Phase 16** — Experiment primitives, PoolScalingController experiment integration.

**New Phase 17** — Memory, caching, guardrail, eval hooks.

---

## Summary

| Tier | Count |
|---|---|
| V1 Must-Have Breaking | 9 |
| V1 Must-Have Feature | 8 |
| V1 Should-Have | 5 |
| Planned / Post-V1 | 5 |
| Explicit Non-Decisions | 5 |

---

## Human (Mar 31, 2026, 7:53 PM):

In a runtime with integration layer 0 (only MCP, no Lenny knowledge), how does the runtime:
1. receive a request?
2. know when to start the runtime? (has to happen after workspace is set up)

---

## Claude:

Good question. Layer 0 is where the model gets tested against reality.

---

## The Gap

A Layer 0 runtime is a standard MCP client. It connects to MCP servers and calls tools. But it doesn't receive requests — MCP clients initiate calls, they don't receive them. And it has no way to know when the workspace is ready because nothing tells it.

The current model has an implicit assumption that was never made explicit: something must push the initial prompt to the runtime and signal when it can start. In the current binary protocol that's the adapter calling `StartSession` on the binary. With Layer 0 there's no equivalent mechanism.

---

## Two Options

**Option A: The adapter inverts the relationship for Layer 0.**

Instead of the runtime connecting to the adapter's MCP servers, the adapter connects to the runtime's MCP server. The runtime exposes a standard MCP server — this is actually the more natural MCP pattern, since most MCP tooling is built around *being* an MCP server that a host connects to.

The adapter connects to the runtime's MCP server after workspace is ready, then calls a standard MCP tool or sends a sampling request with the initial prompt. The runtime processes it and calls back to the adapter's MCP servers for tools, delegation, memory, etc.

This is how LangGraph Platform works — the server is the agent, clients connect to it. It's also how MCP's own `sampling` primitive works: the server requests LLM completions from the client.

The manifest tells the runtime what port/socket to expose its MCP server on before the adapter connects.

**Option B: The adapter adds a minimal "start" tool to the platform MCP server.**

The runtime connects to the adapter's platform MCP server at startup (the adapter is ready before the runtime needs to do anything). The runtime's first action is to call `lenny/get_task` — a blocking or polling tool that returns the initial task input once the workspace is ready. The runtime loops: call `lenny/get_task`, process the result, call `lenny/output` with results, call `lenny/get_task` again for the next task (relevant for task pod mode).

This keeps the runtime as a pure MCP client throughout. No server implementation required.

---

## Which is Right

Option A is more powerful but requires the runtime to implement an MCP server, which is non-trivial for a Layer 0 integration. It also means the adapter needs to know which tools the runtime exposes — more coupling.

Option B is simpler and keeps the Layer 0 promise intact: the runtime is only ever an MCP client. `lenny/get_task` is a blocking long-poll that returns when the workspace is ready and a task is available. The runtime has one loop:

```python
while True:
    task = mcp.call("lenny/get_task")     # blocks until workspace ready + task available
    result = process(task)
    mcp.call("lenny/output", result)
```

For task pod mode this loop naturally handles sequential tasks — `lenny/get_task` blocks until the next task arrives after scrub. For session mode it returns the first (and only) task then the session ends.

The workspace readiness signal is implicit: `lenny/get_task` doesn't return until the adapter has finished materializing the workspace. The runtime never needs to know about workspace setup at all.

This feels like the right answer for Layer 0. It's minimal, requires no server implementation, works for both session and task pod modes, and keeps all the Lenny-specific knowledge inside `lenny/get_task` rather than spread across the runtime.

The manifest tells the runtime the platform MCP server socket to connect to. That's the only thing a Layer 0 runtime needs to know at startup.

---

## Human (Mar 31, 2026, 8:10 PM):

Option A makes more sense to me. However, before I make any decision, can you remind me how tasks are sent to runtimes in layers 1/2?

---

## Claude:

Honestly, this is the right question to ask because the answer reveals that the same gap exists in layers 1 and 2 — it just hasn't been explicitly addressed yet.

In the current proposed design:

- The **lifecycle channel** (layer 2) was designed for operational events: checkpoint requests, interrupt signals, credential rotation, terminate. It was never defined as a task delivery mechanism.
- The **platform MCP server** (layer 1/2) provides tools the runtime calls *outward* — `lenny/delegate_task`, `lenny/output`, `lenny/memory_query`, etc. It has no defined mechanism for the adapter to push a task *inward* to the runtime.

So the task delivery mechanism — how the initial prompt arrives, how follow-up prompts arrive, how the runtime knows the workspace is ready — was never fully specified in the new model. It existed implicitly in the old binary protocol via the `prompt` inbound message type delivered over stdin. That was dropped when we replaced the binary protocol with MCP, but no equivalent was defined.

This means the decision about layer 0 is actually a decision about all layers. The mechanism should be consistent. You shouldn't have one answer for layer 0 and a different answer for layers 1 and 2.

Option A — the runtime exposes an MCP server, the adapter connects to it after workspace is ready and delivers the task via a standard MCP call — is a consistent answer across all three layers. In layers 1 and 2 the runtime additionally connects outward to the adapter's platform MCP servers for tools. Both directions coexist cleanly. The runtime is both a server (receives tasks) and a client (calls tools).

Option B — `lenny/get_task` polling — would work for all layers too but requires every runtime to poll rather than be pushed to, which is less natural for most agent frameworks.

Does Option A still feel right to you knowing it applies to all layers, not just layer 0?

---

## Human (Mar 31, 2026, 8:14 PM):

Not quite. I think the original prompt mechanism should exist, as well as the ability for the adapter to start the runtime at a specific point in time (maybe that's already in the new model).

I think runtimes should expose, in their configuration, how their clients can interact with them, Maybe it's via MCP (with specific tools listed in the config), or via A2A/AP down the line (with a card also in the config), or maybe via the original prompt mechanism.

Let me know if this makes sense. If so, review what all would need to change before proposing an exact mechanism. (For example, what would need to change in the gateway?)

---

## Claude:

This makes sense and is actually a cleaner model. Before proposing the mechanism, let me think through what would need to change.

---

## The Core Insight

The gateway always talks to the adapter the same way — via the existing gRPC/mTLS control protocol. The adapter is the translation layer between the gateway's internal protocol and whatever the runtime expects. This means task delivery mode is purely an adapter concern. The gateway doesn't need to know how the runtime receives tasks, only that it declared a delivery mode.

---

## What Would Need to Change

**Runtime configuration** gains a `taskDelivery` section declaring how the runtime expects to receive tasks. This is separate from how it calls outward tools (which is always via the adapter's platform MCP server for layers 1/2). The delivery mode also controls when and how the adapter starts the runtime process.

**The adapter** becomes mode-aware. It reads the delivery mode from the runtime configuration (passed at startup via the adapter-manifest or injected by the gateway at session creation) and handles the last-mile delivery accordingly. The adapter already abstracts the runtime from the gateway — this extends that abstraction to cover task delivery.

**The adapter-manifest** needs mode-specific entries so the runtime knows what to expose at startup. For prompt mode this is nothing. For MCP mode this includes the socket path where the runtime should listen.

**The gateway** changes minimally. It passes delivery mode metadata to the adapter in `StartSession`. The adapter handles the rest. The gateway's streaming model (bidirectional stream via Attach) is unchanged — the adapter still presents the same interface to the gateway regardless of how it talks to the runtime internally. Output from the runtime, whether it arrives via stdout JSON Lines or via `lenny/output` tool calls, is normalized to `OutputPart` arrays before reaching the gateway.

**The lifecycle channel** is unaffected — it remains a separate optional channel for operational events regardless of delivery mode.

---

## The Two Current Modes (Future Modes Are Additive)

**Prompt mode** — the original mechanism preserved. The adapter pushes a JSON Lines prompt message to the runtime via stdin after workspace is ready. Simple, works with any process that reads stdin. No MCP required.

**MCP mode** — the runtime exposes its own MCP server. The adapter connects to it after workspace is ready and delivers the task via a declared tool call. The runtime is both a server (receives tasks inbound) and a client (calls the adapter's platform servers outbound).

A2A and AP are future delivery modes that follow the same pattern — declared in config, handled entirely by the adapter.

---

## One Thing to Validate Before Proposing the Mechanism

The output path needs to be consistent regardless of delivery mode. In prompt mode, the runtime traditionally wrote responses back to stdout. In MCP mode, it would call `lenny/output`. These need to converge on the same `OutputPart` model at the adapter boundary.

For prompt mode, the existing stdout response messages can be extended to carry `OutputPart` arrays instead of plain text — backward compatible since the adapter is the only consumer, not external clients.

Does this analysis match what you had in mind before I propose the exact mechanism?

---

## Human (Mar 31, 2026, 8:19 PM):

Before I make a decision, can you tell me what capabilities are supported in prompt mode? Is it just text input, or can it be media as well? If just text, then we need to think about how to make this more generic

---

## Claude:

In the current design, prompt mode is text-only. The inbound `prompt` message type in the binary protocol carries a `text` field — a plain string. There is no provision for images, structured data, or any other content type.

This is a real limitation. Even in the original design it was already constraining — a coding agent that receives a screenshot of a UI to implement, or a vision agent that receives a diagram to analyze, has no way to receive that input via prompt mode today.

The fix is straightforward and consistent with the `OutputPart` model we already defined for outputs and for `TaskSpec.input`. The prompt message in prompt mode should carry an `input` field that is an `OutputPart` array rather than a plain `text` string:

```json
{
  "type": "prompt",
  "input": [
    { "type": "text", "mimeType": "text/plain",
      "inline": "Implement this UI" },
    { "type": "image", "mimeType": "image/png",
      "inline": "<base64>" }
  ],
  "sessionId": "s_abc123"
}
```

This is backward compatible at the adapter layer — the adapter is the only consumer of the binary protocol, not external clients. Simple runtimes that only read text can destructure the array and take the first `text` part. Richer runtimes handle all part types.

This also means prompt mode and MCP mode converge on the same input model. The `input` field in the prompt message is exactly the same `OutputPart` array that `TaskSpec.input` carries and that MCP mode delivers via a tool call. The adapter's input normalization is uniform across delivery modes — it always produces an `OutputPart` array, regardless of which protocol it uses to deliver it.

With this change, prompt mode is no longer "text input." It becomes "any content type that can be expressed as an `OutputPart` array" — which covers text, images, audio, structured data, and anything else the model handles. The delivery mechanism is still stdin JSON Lines, but the payload is rich and typed.

Does this resolve the concern, or would you like to think through the MCP delivery mode before deciding?

---

## Human (Mar 31, 2026, 8:22 PM):

This resolves the concern. But please make sure no other changes would be needed. Right now, I'm leaning towards supporting only prompt mode.

Assuming prompt mode only, with the changes to support multi-part messages, please come up with sequence diagrams describing:
* How runtimes are launched, configured, started
* How runtimes communicate with other child runtimes

---

## Claude:

Here are the two sequence diagrams. Solid arrows are requests; dashed arrows are responses or async notifications. Boxed notes show internal actions.The second diagram shows the child delegation flow. The parent adapter is omitted from the column layout for clarity — it mediates all parent runtime ↔ gateway communication in both directions.A few design notes worth capturing for the spec:

**On the manifest:** The adapter writes a placeholder manifest at pre-warm time (basic platform server socket info) and overwrites it with the final session-specific manifest at `StartSession` time — once the gateway has resolved which connectors the delegation lease authorizes and which credential pools are assigned. The runtime process starts only after the final manifest is written, so it always reads a complete and authoritative manifest.

**On the prompt delivery timing:** The adapter sends the initial `{type: "prompt", input: [OutputPart[]]}` message over stdin as the very first thing after the runtime process starts — there is no handshake or capability negotiation step. The runtime can begin processing immediately. If the runtime emits `tool_call` messages on stdout before sending a `response`, the adapter services them (calling the appropriate MCP server or gateway RPC) and returns `tool_result` messages on stdin. This loop continues until the runtime emits a `{type: "response"}` message, at which point the adapter forwards the output upstream.

**On the child delegation gap:** The 50px vertical gap in the child execution phase between the stdin prompt and the stdout response represents the child runtime's processing time. No messages flow during this period. This is intentional — it's what makes the async task model valuable.

---

## Human (Mar 31, 2026, 8:33 PM):

Continue

---

## Claude:

This is a good question to ask because it exposes a genuine gap in the current model.

---

## The Current Gap

In the prompt-mode-only model we just described, the communication is entirely one-directional at the protocol level: the adapter pushes a prompt to the runtime via stdin, the runtime emits tool calls and responses via stdout, and the adapter services those tool calls against the gateway or connector MCP servers. The runtime is always an MCP client — it never acts as an MCP server.

This means that if a runtime declares `agentInterface.skills` (say, a code-review agent that exposes a `review_file` skill and a `check_security` skill), those skills are metadata only in v1. They inform the discovery result and auto-generate A2A cards, but there is no mechanism for another runtime to call them as MCP tools directly. The only way to invoke that agent's capabilities is via `lenny/delegate_task`, which delivers a free-form prompt and gets back an OutputPart array. The calling runtime says "please review this file" in natural language, not `review_file(path: "src/auth.py")`.

This is a real semantic mismatch. MCP tool calls are synchronous, named, and strongly typed. Task delegation is asynchronous, free-form, and loosely typed. The two are not equivalent.

---

## How Future MCP Mode Would Close This

When MCP mode is added as a second task delivery variant, the picture changes. A runtime configured for MCP mode would expose its own MCP server (a real listening socket), and the adapter manifest would include that server's endpoint alongside the platform server and connector servers. The calling runtime would find the target agent via `lenny/discover_agents`, connect to its MCP server through the adapter proxy, and call `review_file` as a normal MCP tool call — with full capability negotiation, typed arguments, and synchronous result delivery.

The adapter proxy is the critical piece: all traffic to the target agent's MCP server would still flow through the adapter and gateway, never directly pod-to-pod. The gateway enforces the delegation lease, records the tool call in the audit log, and tracks token consumption. The fact that the protocol is MCP rather than a task prompt is an implementation detail hidden behind the proxy.

---

## What This Means for the Design Right Now

Two things need to be clarified in the spec:

First, `agentInterface.skills` should be documented as discovery and card-generation metadata only in v1. It does not imply that those skills are callable as MCP tools — that requires MCP mode, which is deferred. Callers who want to invoke a specific skill should pass a structured `input` in the TaskSpec that names the skill (e.g., `{type: "data", mimeType: "application/json", inline: {skill: "review_file", path: "src/auth.py"}}`). The child runtime reads its own `agentInterface.inputSchema` and interprets accordingly. This is less elegant but fully functional in v1.

Second, the path to MCP mode should be noted as requiring the runtime to expose a listening socket that the adapter can discover and proxy — which means the adapter manifest format needs a reserved slot for `mcpServerEndpoint` even in v1, so adding MCP mode later doesn't require a manifest schema change. The field is simply absent for prompt-mode runtimes.

For A2A the same logic applies: the adapter serves per-agent A2A endpoints when A2A mode is added, each agent gets its own endpoint (consistent with the aggregation-is-lossless conclusion), and the calling runtime finds those endpoints via `lenny/discover_agents`. In v1 those fields are absent from discovery results.

---

## Human (Mar 31, 2026, 9:08 PM):

Can you remind me how internal targets that define MCP tools (or A2A cards in the future) work in this communication model you just described?

---

## Claude:

You've hit on something real, but I think it resolves cleanly once the layers are made explicit. Let me work through it.

---

## The Fundamental Model

A runtime in Lenny has exactly one job: receive a structured prompt, do work, emit a structured response. That's it. It never exposes any server interface of its own. It is always a backend, never a server.

This means the question "how do I make my runtime an A2A agent?" has a surprising answer: **you don't do anything to the runtime**. The gateway becomes the A2A server on the runtime's behalf.

---

## How External A2A Calls Work

When A2A support is added, the gateway's `ExternalProtocolAdapter` serves an A2A endpoint per runtime — say `POST /a2a/research-pipeline/tasks`. An external A2A client sends a task there. The gateway translates it: the A2A task message becomes a `TaskSpec.input` (an `OutputPart` array), a Lenny session is created against the `research-pipeline` runtime, the adapter delivers it as a prompt via stdin, the runtime processes it and responds via stdout, and the gateway translates the response back to A2A format and returns it to the external caller.

The runtime never knows it was called via A2A. It just received a prompt and emitted a response.

The `agentInterface` field on the runtime serves three purposes in this model: it documents what the runtime can do (for discovery), it auto-generates the A2A agent card (skills, inputModes, outputModes), and it tells callers how to structure their task input. But it does not create callable typed endpoints — the runtime is still just a prompt processor.

---

## How Internal Calls Work

When Runtime A wants to invoke Runtime B internally, it calls `lenny/delegate_task(target: "runtime-b", task: {...})`. This is Lenny's internal delegation primitive. It is the only inter-runtime communication protocol within Lenny. Runtime A never speaks A2A or MCP directly to Runtime B.

If Runtime B declares a skill `review_file`, Runtime A discovers this via `lenny/discover_agents` and formulates a structured `TaskSpec.input`:

```json
{
  "input": [
    {
      "type": "data",
      "mimeType": "application/json",
      "inline": { "skill": "review_file", "path": "src/auth.py" }
    }
  ]
}
```

Runtime B receives this as a structured prompt, reads the skill name from the input, and routes internally to the right handler. The skill declaration in `agentInterface` is documentation and discovery metadata — it tells callers what the runtime can do and how to structure their inputs. It is not a callable typed endpoint in v1.

---

## Why This Is Actually Clean

The incompatibility you're sensing comes from comparing two different things:

- MCP tools are synchronous, named, typed, and callable
- Lenny tasks are asynchronous, free-form, and loosely typed

But A2A tasks are also free-form — the A2A protocol routes to an agent via HTTP and the task message content tells the agent what to do. Skills in an A2A agent card are discovery hints, not separate HTTP endpoints. So the model is consistent: external A2A clients send tasks to the gateway, internal runtimes send tasks via `lenny/delegate_task`, and the runtime handles both identically because both arrive as structured prompts.

The one case where this model genuinely doesn't cover is if you want other runtimes to call your runtime's capabilities as synchronous, schema-validated MCP tool calls — `review_file(path: string) → ReviewResult`. That requires MCP mode: the runtime exposes its own MCP server, the adapter proxies it, and callers get full tool semantics. That is a future feature. For v1, structured `TaskSpec.input` with a skill field and an `agentInterface.inputSchema` handles the same use case with slightly less type safety.

So to directly answer your question: to set up a runtime as an A2A agent, you register it in Lenny with a populated `agentInterface`, add a `publishedMetadata` entry with `visibility: public`, and the gateway handles the rest when A2A support ships. The runtime binary is unchanged.

---

## Human (Mar 31, 2026, 9:26 PM):

This makes sense, but this is all making me rethink everything. There seems to be a fundamental incompatibility between what runtimes can actually do (receive prompt and emit a response) and the concept of internal MCP or A2A targets. How would I setup a runtime to be an A2A agent, for example?

---

## Claude:

Correct, and this is a real limitation worth being explicit about.

---

## What Lenny Currently Supports

The current delegation model is a strict request-response tree. Parent delegates to child, child runs to completion, child returns a result, parent continues. The interaction is:

- One prompt in
- Zero or more tool calls (synchronous, serviced by the adapter)
- One response out

There is no mechanism for a child agent to ask its parent a question mid-execution, for two agents to negotiate over multiple turns, or for an orchestrator to observe and redirect a running child based on intermediate output.

The elicitation chain (`lenny/request_elicitation`) is the closest thing, but it goes upward to the human client — not sideways to another agent. It's for human-in-the-loop, not agent-to-agent dialogue.

---

## What A2A Actually Enables

A2A is designed around multi-turn, stateful agent-to-agent interaction. An A2A task can transition to `input-required` state, which means the agent is pausing and asking its caller for more information before proceeding. The caller can respond, and the agent continues. This is genuine back-and-forth dialog between agents.

Lenny's current model maps only the happy-path case of A2A: `submitted → working → completed`. The `input-required` state has no equivalent in Lenny's delegation primitive. A child session cannot ask its parent a question and wait for an answer.

---

## The Gap

There are two distinct missing capabilities:

**Agent-to-agent elicitation** — a child session needs clarification from its parent before it can continue. Currently elicitation only flows to the human client. There is no mechanism to route an elicitation sideways to a calling agent rather than upward to the user.

**Multi-turn agent sessions** — two agents maintain an ongoing conversation across multiple prompt-response exchanges rather than a single round-trip. This doesn't exist in Lenny at all. Each task delegation is a one-shot interaction.

---

## How to Address This

The least invasive fix for v1 is to extend the elicitation chain to support agent-directed elicitation. When a child session calls `lenny/request_elicitation`, the gateway currently routes it upward toward the human client. If the session was created via `lenny/delegate_task` from a parent agent, the gateway could instead route the elicitation to the parent agent's session as a `tool_result` on the pending `delegate_task` call — with the child's question as the content and the parent's response fed back as the elicitation answer. The parent agent decides whether to answer itself or escalate further up to the human.

This is a modest change to the elicitation routing logic and covers the most common coordination pattern: child hits an ambiguity, asks parent for guidance, parent resolves it, child continues.

True multi-turn dialog — two agents maintaining an ongoing conversation with arbitrary back-and-forth — is a harder problem that likely requires a new primitive: a persistent channel between two sessions rather than the current one-shot task model. This maps directly to A2A's `input-required` lifecycle and is worth designing properly rather than retrofitting. It should be called out explicitly as a post-v1 capability with a note that the data model needs to accommodate it — specifically that the delegation lease and task record should not assume a task always completes in a single prompt-response cycle.

---

## Human (Mar 31, 2026, 9:47 PM):

But even in the A2A case, Lenny currently doesn't allow agent coordination and back and forth dialog between agents, right?

---

## Claude:

The right way to think about this: a delegated task should be a session where the parent is the client — not a special single-turn primitive. The session model already supports multi-turn interaction with a human client. Delegation just substitutes another agent as the client. If you make that substitution cleanly now, multi-turn agent dialog falls out naturally without a future redesign.

---

## What Needs to Change Now

**1. Task state machine gets `input_required`**

The task lifecycle currently assumes a task runs to completion in one cycle: `submitted → running → completed | failed`. Add `input_required` as a valid terminal-for-now state:

```
submitted → running → completed
                    → failed  
                    → input_required → running → ...
```

The state must exist in the schema now even with no implementation behind it. A v1 runtime that emits `input_required` gets a graceful error. A future runtime that uses it gets the full behavior.

**2. TaskRecord uses a messages array, not flat input/output fields**

Instead of `{input: OutputPart[], output: OutputPart[]}`, the task record becomes:

```json
{
  "messages": [
    { "role": "caller", "parts": [...] },
    { "role": "agent",  "parts": [...], "state": "input_required" },
    { "role": "caller", "parts": [...] },
    { "role": "agent",  "parts": [...], "state": "completed" }
  ]
}
```

In v1 there are always exactly two messages — one caller, one agent. In future there can be N. This maps directly to A2A's task message model. Retrofitting this later would touch every API surface that returns task results.

**3. Binary protocol allows `input_required` outbound and `follow_up` inbound**

The adapter's stdin/stdout protocol gets two new message types:

Outbound (runtime → adapter):
```json
{ "type": "input_required", "parts": [...] }
```
The runtime signals it needs more input before it can complete. In v1 the adapter returns an error if this arrives. In future it triggers the `input_required` state transition.

Inbound (adapter → runtime):
```json
{ "type": "follow_up", "input": [OutputPart[]] }
```
A subsequent turn from the calling agent. In v1 this message type is defined but never sent. In future the adapter sends it when the parent calls `lenny/send_to_child`.

**4. `lenny/send_to_child` stub in the platform MCP server**

```
lenny/send_to_child(task_id: string, message: OutputPart[]) → void
```

Defined now, returns `not_implemented` in v1. Activates when multi-turn is implemented. The parent uses this to respond to a child's `input_required` state.

**5. `await_children` returns `input_required` status**

The existing `await_children` tool already unblocks on child completion or failure. It needs to also unblock on `input_required`, returning the child's message and task ID so the parent can respond via `lenny/send_to_child`. In v1 this state is never reached so no behavior change is needed — just the schema.

**6. Delegation lease removes single-turn assumptions**

The lease currently has `maxSessionAge` and `maxTokenBudget`. Neither assumes single-turn. No change needed. But any documentation that says "a delegated task runs to completion" must be reworded to "a delegated task runs until it completes, fails, or requests input."

---

## Why This Is Enough

With these six changes, multi-turn agent dialog becomes an implementation task not a redesign:

- The state machine already has the right states
- The task record already has the right shape
- The binary protocol already has the right message types
- The gateway just needs to wire `input_required` → `send_to_child` → `follow_up` delivery
- A2A's `input-required` state maps directly to `input_required` in the task record — no translation layer needed

For A2A specifically: when the gateway serves an inbound A2A task and the child session enters `input_required` state, the gateway translates it to an A2A `input-required` task response and the external A2A caller sends a follow-up. The gateway translates that follow-up into a `follow_up` binary protocol message to the adapter. The runtime receives it as another inbound message on stdin. The entire A2A multi-turn lifecycle works without touching the runtime binary at all.

---

## Human (Mar 31, 2026, 10:25 PM):

I think we need Lenny to be designed in a way that makes back-and-forth communication possible  in the future when we support A2A, without a refactor.

---

## Claude:

This is worth thinking through carefully because the answer has real implications for the connector model.

---

## The Core Question

An MCP server is fundamentally a tool provider — it exposes named, typed, synchronous operations. A Lenny runtime is fundamentally a prompt processor — it receives a task, does work, emits a result. These look different, but the question is whether the gap matters enough to treat them as completely separate concepts.

---

## When External Connectors Are the Right Answer

For stateless, trusted, third-party MCP servers — GitHub, Jira, Slack, Linear — external connectors are clearly correct. These services run their own infrastructure, have their own security models, and don't benefit from Lenny managing their pod lifecycle. Wrapping them in Lenny pods would add overhead with zero benefit.

---

## When Lenny Runtimes Are the Right Answer

There is a class of MCP server that genuinely benefits from everything Lenny provides:

**Code execution** is the clearest case. A runtime that executes arbitrary Python or bash on behalf of an agent absolutely needs gVisor or Kata isolation, per-tenant sandboxing, network egress control, and a clean workspace per invocation. Running this as a shared external connector process means one tenant's code runs in the same kernel as another's. That's a serious security problem that Lenny's isolation model solves directly.

**File processing with sensitive data** — a runtime that receives files exported from a parent workspace, runs transformations (PDF parsing, image analysis, data cleaning), and returns results. The files contain tenant-sensitive data. You want per-tenant isolation and an auditable trail of what processed what.

**Credential-bearing tool execution** — a runtime that needs real credentials (a database connection, a cloud storage token) to service a tool call. Lenny's credential leasing model handles this cleanly. An external connector would need its own credential management.

**Stateful tool sessions** — a browser automation runtime, a long-running shell session, a REPL. These have state that lives in the workspace across multiple tool calls. External connectors have no equivalent to Lenny's workspace.

In all of these cases, the Lenny capabilities that are genuinely useful are: pod isolation, workspace, credential leasing, egress control, audit logging, multi-tenant isolation, and pool management for low-latency servicing. The capabilities that are less relevant are session resume/checkpoint, elicitation, and multi-turn dialog — tool calls are typically short-lived and one-shot.

---

## How It Works in the Current Model

The mechanism is already there, just not named explicitly. A "tool server runtime" is a Lenny runtime with `executionMode: task` (or `concurrent`) that processes tool-call-shaped tasks. The `agentInterface` declares its capabilities as skills. A calling runtime discovers it, delegates to it via `lenny/delegate_task`, and awaits the result.

The task input carries the tool call in structured form:

```json
{
  "input": [
    {
      "type": "data",
      "mimeType": "application/json",
      "inline": { "skill": "execute_python", "code": "...", "timeout": 30 }
    }
  ],
  "workspaceFiles": {
    "export": [{ "glob": "data/*.csv", "destPrefix": "/" }]
  }
}
```

The tool server runtime receives this, executes the code in its gVisor sandbox with access to the exported files, and returns results as an OutputPart array. From the calling runtime's perspective, `lenny/delegate_task` followed immediately by `lenny/await_children` is effectively synchronous, especially with a warm pool behind it.

---

## The Clean Distinction

The spec should draw this line explicitly:

**External connectors** — stateless, trusted, third-party services. No Lenny pod management. Registered in the ConnectorDefinition registry. Called via the connector proxy MCP server in the adapter. Good for: GitHub, Jira, Slack, databases, search APIs.

**Tool server runtimes** — Lenny-managed pods that happen to serve a tool provider role rather than an LLM agent role. Use the standard runtime model. Good for: code execution, file processing with sensitive data, credential-bearing operations, stateful tool sessions.

The distinguishing criterion is simple: if the tool server needs isolation, a workspace, credentials, or per-tenant sandboxing, it belongs as a Lenny runtime. If it doesn't, it belongs as an external connector.

---

## What Needs to Be Added to the Design

One thing is missing: the spec should note that tool server runtimes — because they serve synchronous tool-call semantics — are better suited to `executionMode: task` with a well-sized warm pool than to `session` mode. The session mode lifecycle (create, upload, start, attach, complete, drain) is heavyweight for what is logically a function call. Task mode with a warm pool amortizes that overhead across many calls, keeping latency low.

For future MCP mode: when a runtime can expose its own MCP server through the adapter proxy, tool server runtimes would get native MCP protocol support — typed schemas, synchronous call semantics, capability negotiation. The calling runtime would connect to the tool server's MCP server via the adapter manifest and call `execute_python` directly as an MCP tool, without going through `lenny/delegate_task` at all. The Lenny sandboxing still applies because the pod is still Lenny-managed — the protocol change is purely in how the call is delivered.

---

## Human (Mar 31, 2026, 10:31 PM):

Ok, I like this.

Back to the MCP topic.. should Lenny runtimes be able to run MCP servers, or should MCP servers always be connectors? Are any of the security and sandboxing capabilities in Lenny theoretically useful to an MCP server runtime?

---

## Claude:

Let me think through this completely before listing. MCP mode means a runtime exposes its own MCP server that the adapter makes available to authorized callers. Everything else derives from that single statement.

---

## Changes Needed

### 1. `taskDelivery` Declaration on Runtime

```yaml
name: code-executor
taskDelivery:
  mode: mcp                              # prompt (default) | mcp
  mcpServerSocket: /run/lenny/runtime-mcp.sock
  readinessProbe:
    type: mcp_capabilities               # adapter polls until server responds
    timeoutSeconds: 30
```

Prompt mode remains the default. MCP mode is explicitly declared. The socket path is where the runtime's MCP server listens — a well-known convention so the adapter knows where to connect.

---

### 2. Standard `lenny/receive_task` Contract

MCP-mode runtimes must implement one standard tool on their MCP server:

```
lenny/receive_task(input: OutputPart[], sessionId: string) → OutputPart[]
```

This is the MCP equivalent of the stdin `{type: "prompt"}` message. The adapter calls it after workspace is ready, credentials are assigned, and the runtime MCP server is confirmed ready. The tool call is long-running — it completes when the runtime finishes the task. The return value is the task output, equivalent to the stdout `{type: "response"}` in prompt mode.

This is the only Lenny-specific tool an MCP-mode runtime must implement. All its other tools (`execute_python`, `review_file`, etc.) are its own business.

---

### 3. Adapter Startup Changes for MCP Mode

The adapter's `INIT → READY` transition changes for MCP-mode runtimes:

**Prompt mode (current):** adapter starts, writes manifest, signals READY. Runtime spawned later at session start.

**MCP mode (new):** adapter starts, spawns the runtime process immediately (the MCP server needs to be running before sessions arrive), polls the runtime's MCP server socket until it responds to capability negotiation, then writes the final manifest and signals READY to the gateway.

The warm pool implication: for MCP-mode runtimes, the runtime binary is running and warm alongside the adapter, not started lazily at session time. This matches `executionMode: concurrent` or `task` semantics where the pod is reused across many calls.

---

### 4. Adapter Task Delivery for MCP Mode

In MCP mode, the adapter never writes a `{type: "prompt"}` message to stdin. Instead, after `StartSession`:

1. Adapter calls `lenny/receive_task` on the runtime's MCP server with the task input
2. The call blocks until the runtime returns a result
3. Adapter forwards the result to the gateway as the task output

The stdin/stdout channel in MCP mode is used only for the lifecycle channel (Layer 2) — checkpoint, interrupt, terminate signals. The `prompt` and `response` binary protocol message types are unused and should be documented as not applicable in MCP mode.

---

### 5. Adapter as Proxy for Runtime MCP Server

The adapter proxies inbound MCP connections from authorized callers to the runtime's local MCP server socket. The proxy path is:

```
Calling runtime → calling adapter → gateway → target adapter → target runtime MCP socket
```

The adapter implements this as a transparent MCP proxy — it forwards the connection including capability negotiation so the calling runtime sees the target runtime's actual capabilities, not an adapter-level abstraction.

The target runtime never speaks directly to any other pod. All traffic goes through the adapter and gateway. This is the same principle as with connector proxying.

---

### 6. Adapter Manifest Changes

The manifest currently has two sections: platform server and connector servers. It gains a third:

```json
{
  "platformMcpServer": {
    "socket": "/run/lenny/platform-mcp.sock"
  },
  "connectorServers": [
    { "id": "github", "socket": "/run/lenny/connector-github.sock" }
  ],
  "runtimeMcpServers": [
    {
      "id": "code-executor",
      "socket": "/run/lenny/runtime-code-executor.sock",
      "capabilities": { ... }
    }
  ]
}
```

From the calling runtime's perspective, `connectorServers` and `runtimeMcpServers` are just MCP servers — the distinction is Lenny's internal concern. Both are proxied by the adapter. The calling runtime connects to either via the manifest socket path and uses standard MCP tool calls.

The manifest is regenerated per task execution. Which runtime MCP servers appear in `runtimeMcpServers` is determined by the session's delegation policy — the same policy that controls what runtimes can be delegated to.

---

### 7. Gateway Routing for Proxied Runtime MCP Calls

The gateway needs to route proxied MCP connections to the correct pod. This is a new routing concern: today the gateway routes sessions to pods. Now it also routes MCP tool calls to pods.

For `executionMode: concurrent-stateless` tool server runtimes, the gateway routes through the Kubernetes Service (same as discussed for concurrent mode). The pod's readiness probe reflects slot availability.

For `executionMode: task` tool server runtimes, the gateway routes to whichever pod currently holds a claim. If all pods are busy, the call waits in a short queue until a pod is available.

The gateway must enforce the delegation policy on these calls — a runtime can only reach tool server runtimes that are in its authorized set — and must record each MCP tool call in the audit log with the calling session ID, the target runtime ID, the tool name, and token/cost attribution.

---

### 8. Pool Management for Tool Server Runtimes

MCP-mode runtimes have different latency requirements than session-mode runtimes. A tool call is expected to be near-synchronous. Cold starts are unacceptable.

The PoolScalingController needs to treat MCP-mode runtime pools as always-warm: `minWarm` should never drop to zero during active hours, and the safety factor in the scaling formula should be higher (default 2.0 rather than 1.5) to absorb bursty tool call traffic without cold starts.

The `scaleToZero` schedule policy should be disabled by default for MCP-mode pools, with an explicit deployer opt-in to enable it.

---

### 9. Discovery Changes

`lenny/discover_agents` results for MCP-mode runtimes include the tool list:

```json
{
  "id": "code-executor",
  "kind": "runtime",
  "taskDelivery": "mcp",
  "mcpTools": [
    {
      "name": "execute_python",
      "description": "Execute Python code in an isolated sandbox",
      "inputSchema": { ... },
      "outputSchema": { ... }
    }
  ],
  "agentInterface": { ... }
}
```

The `mcpTools` array is populated by the gateway querying the runtime's MCP server capabilities at registration time and caching them. If the runtime's tool list changes (new deployment), the cache is invalidated when the new pool comes up.

---

### 10. `lenny/delegate_task` Still Works for MCP-Mode Runtimes

This is important to state explicitly: `lenny/delegate_task` continues to work against MCP-mode runtimes. The gateway delivers the task via `lenny/receive_task` on the runtime's MCP server instead of via stdin prompt, but from the calling runtime's perspective the delegation interface is identical.

This means a calling runtime can choose its interaction style based on what it needs:
- For typed, synchronous tool semantics: connect to the runtime's MCP server via the manifest and call tools directly
- For asynchronous task delegation with workspace files, budgets, and the full task lifecycle: use `lenny/delegate_task` as usual

Both paths enforce policy, both appear in the audit log, both track token costs.

---

### 11. Layer Model Update

The existing layer model (0/1/2) describes how runtimes call outward. MCP mode adds an orthogonal dimension — whether the runtime exposes a server. The revised model:

**Task delivery axis:**
- Prompt mode (default): task arrives via stdin `{type: "prompt"}`
- MCP mode: task arrives via `lenny/receive_task` tool call on runtime's MCP server

**Outward capability axis (unchanged):**
- Layer 0: no platform MCP usage
- Layer 1: uses platform MCP server and connector servers
- Layer 2: Layer 1 plus lifecycle channel

A runtime can be any combination: an MCP-mode tool server that is also Layer 2 (uses platform MCP server for `lenny/output` and `lenny/memory_write`, and opens the lifecycle channel for checkpoint support).

---

### 12. Binary Protocol Documentation

Explicitly document that in MCP mode:
- `prompt` inbound message type: not used
- `response` outbound message type: not used  
- `tool_call` / `tool_result` outbound/inbound: not used (runtime calls connector tools directly via MCP)
- Lifecycle channel stdin/stdout: still used for operational signals
- `lenny/output` platform MCP tool: the correct way for an MCP-mode runtime to emit streaming output events to the client

---

### 13. Sequence Diagram Additions

Two new sequence diagrams needed alongside the existing ones:
- MCP-mode runtime launch (pre-warm includes runtime MCP server startup and readiness check)
- MCP tool call from calling runtime to tool server runtime (showing the proxy path through adapters and gateway)

---

### What Does Not Change

The delegation lease model, budget enforcement, multi-turn `input_required` state, workspace materialization, credential leasing, checkpoint/resume, OutputPart format, DelegationPolicy evaluation, and audit logging all work identically regardless of task delivery mode. MCP mode is a delivery mechanism change, not a data model change.

---

## Human (Mar 31, 2026, 10:48 PM):

I now think the MCP mode needs to be supported in v1. List all the changes that are needed

---

## Claude:

The clarification changes the model significantly and actually simplifies it. Let me restate the concept first, then the revised change list.

---

## The Clean Concept

Two distinct interface types for runtimes:

**`agent`** (default) — a runtime that receives tasks and produces responses. Has a session lifecycle, workspace, task delegation, elicitation, multi-turn dialog. Interacted with via `lenny/delegate_task`. This is everything we've designed so far.

**`mcp`** — a runtime that hosts an MCP server. Lenny manages the pod (isolation, credentials, workspace if needed, pool, egress, audit), but the interaction model is purely MCP: callers connect and call tools. No session lifecycle, no task delegation, no `lenny/delegate_task`. The runtime just exposes whatever tools it exposes.

The `mcp` interface type answers the question "who manages this MCP server?" The answer is Lenny — for isolation, credentials, and multi-tenancy. That's the entire value proposition. The runtime binary is oblivious to Lenny.

---

## Revised Change List

**1. `interfaceType` field on Runtime, not `taskDelivery`**

```yaml
name: code-executor
interfaceType: mcp          # agent (default) | mcp
mcpServerSocket: /run/lenny/runtime-mcp.sock
readinessProbe:
  type: mcp_capabilities
  timeoutSeconds: 30
```

**2. Adapter lifecycle for `mcp`-type runtimes**

The runtime binary is spawned at pod start (pre-warm), not at session start. The adapter polls the runtime's MCP socket until capability negotiation succeeds, then signals READY to the gateway. The warm pod is ready to accept proxied MCP connections immediately.

There is no `StartSession`, no `PrepareWorkspace`, no `AssignCredentials` in the per-call path. Those RPCs are either absent or happen once at pod start if the runtime needs a persistent workspace or credentials across all its tool calls.

**3. No `lenny/receive_task` — the runtime exposes only its own tools**

The MCP server needs no Lenny-specific tools. It exposes whatever it exposes — `execute_python`, `review_file`, `query_database`. The adapter proxies connections transparently. The calling runtime sees the target runtime's actual capability negotiation response, not a Lenny wrapper.

**4. `lenny/delegate_task` explicitly rejects `mcp`-type targets**

If a runtime calls `lenny/delegate_task` with a target whose `interfaceType` is `mcp`, the gateway returns a clear error: `target_not_an_agent`. The correct way to call an `mcp`-type runtime is via the MCP server connection in the adapter manifest.

**5. Adapter manifest `runtimeMcpServers` section**

As proposed previously, unchanged. From the calling runtime's perspective, a managed MCP server in `runtimeMcpServers` and an external connector in `connectorServers` look identical — both are MCP server connections listed in the manifest with socket paths. The distinction (managed pod vs external service) is Lenny's internal concern.

**6. DelegationPolicy covers `mcp`-type runtimes**

The delegation policy's `runtimes` rules apply to `mcp`-type runtimes as well — a calling runtime can only reach managed MCP servers that its policy permits. The gateway enforces this when the calling adapter requests a proxied connection to a managed MCP server.

**7. Gateway audit logging for MCP tool calls**

Each MCP tool call proxied through the gateway is logged: calling session ID, target runtime ID, tool name, arguments hash (not full arguments — they may contain sensitive data), response status, latency, and token cost if the tool triggered an LLM call internally.

**8. `lenny/discover_agents` does not return `mcp`-type runtimes**

Since `lenny/delegate_task` doesn't work against them, returning them from an agents discovery tool would be misleading. `mcp`-type runtimes appear only in the adapter manifest. Discovery of what tools a managed MCP server exposes happens through standard MCP capability negotiation once the caller connects.

**9. Pool management differences for `mcp`-type**

As outlined previously: always-warm default, higher safety factor (2.0), `scaleToZero` disabled by default. Concurrent-stateless execution mode is the natural fit for stateless tool servers. Kubernetes service routing handles load distribution.

**10. Layer model update**

The layer model (0/1/2) only applies to `agent`-type runtimes. It describes how an agent uses Lenny's outward capabilities. `mcp`-type runtimes have no equivalent layers — they simply expose tools and optionally use credentials and workspace provided by Lenny at pod start. Document these as orthogonal concepts.

**11. No session, no task lifecycle for `mcp`-type**

Explicitly document: `mcp`-type runtimes have no session ID, no task record, no delegation lease, no elicitation chain, no checkpoint, no multi-turn state. They are tool servers. The audit log entry for an MCP tool call is the entire Lenny-level record.

---

## On `lenny/discover_agents` and Tools

Your instinct is pointing at something real. There is a conceptual question about whether the right model is one discovery surface for everything a runtime can call — agents via `delegate_task`, managed MCP tools via manifest — or two separate surfaces as it is now.

The argument for unifying: a calling runtime shouldn't need to know whether a capability is implemented as an agent or a tool server. It just wants to know what it can call.

The argument against: the interaction models are so different (async delegation vs synchronous tool call, OutputPart vs typed MCP response, workspace files vs arguments) that pretending they are the same thing would produce a leaky abstraction.

The manifest is probably the right answer for now — it lists everything a runtime can reach, both connectors and managed MCP servers, and standard MCP capability negotiation handles the rest. `lenny/discover_agents` stays agent-only.

---

## Human (Mar 31, 2026, 11:03 PM):

This is mostly good, but there's at least one thing that needs to change: lenny/delegate_task should not work with MCP-mode runtimes. This probably requires a better definition of what an MCP runtime is and isn't. An MCP runtime is a runtime that hosts an MCP server -that's it. It's not an agent + MCP tools. So maybe instead of "task delivery mode", it should be "interface type". And the MCP server shouldn't need to expose lenny/receive_task.

On the other hand, I'm wondering if discover_agents should return both agents and tools? Just a question at this point -I'm not asking to change it.

---

## Claude:

Clean and forward-compatible. Here's how this lands:

---

## `type` on Runtime

```yaml
name: code-executor
type: mcp          # agent (default) | mcp
```

Simple, unambiguous. `type: agent` is any runtime that participates in Lenny's task lifecycle. `type: mcp` is any runtime that hosts an MCP server. Everything else follows from that.

---

## `capabilities` on Runtime

An optional array of declared interaction capabilities. Defaults communicate clearly when absent.

```yaml
name: research-pipeline
type: agent
capabilities:
  interaction: multi_turn    # one_shot (default) | multi_turn
  streaming: true            # false by default
  elicitation: true          # false by default
```

`interaction: one_shot` is the current model — one prompt in, one response out, done. `interaction: multi_turn` declares that this runtime supports the `input_required` → `follow_up` cycle we designed. In v1 only `one_shot` is implemented. A runtime declaring `multi_turn` in v1 gets a graceful error if it tries to use it. The field existing from day one means no future refactor when multi-turn ships.

`interaction` maps directly to A2A interaction modes. When A2A support is added, the gateway uses this field to decide how to serve the runtime's A2A endpoint — a `one_shot` runtime gets a simple task-complete-or-fail A2A model, a `multi_turn` runtime gets the full `input-required` A2A state machine.

`type: mcp` runtimes have no `capabilities` field — it doesn't apply to tool servers.

---

## What "one_shot" Captures

The name `one_shot` is clearer than "task" or "request-response" because it explicitly communicates the constraint: one prompt, one response, conversation closed. Callers that need multi-turn behavior know immediately they're looking at the wrong runtime. It also maps intuitively to how people think about tool server calls, even though `type: mcp` runtimes don't use this field.

---

## Forward Compatibility

Future capabilities that slot in cleanly:

```yaml
capabilities:
  interaction: multi_turn
  streaming: true
  elicitation: true
  protocols: [a2a, ap]       # which external protocols this runtime supports
```

`protocols` is the hook for declaring which `ExternalProtocolAdapter` implementations can serve this runtime. A runtime that declares `a2a` gets an A2A endpoint generated for it. One that doesn't is only reachable via Lenny's internal protocol and MCP. This is additive — existing runtimes without `protocols` declared behave exactly as today.

---

## Human (Mar 31, 2026, 11:16 PM):

Ok, almost there. Now that I'm seeing your proposal, I think we should just call it "type" instead of "interfaceType". Then we probably need something like interface capabilities to indicate, in the future, things like support for multi-turn conversations (for A2A). By default it's one-shot task (or a better name for it)

---

## Claude:

Correct on both counts.

---

## In V1

`interaction: multi_turn` is not a valid value. The spec defines only `one_shot` for v1, and the gateway rejects any runtime registration that declares otherwise. This is cleaner than "defined but returns graceful error" — it doesn't create a half-implemented feature that confuses runtime authors.

The field still exists in v1 as a single-value enum. This is what makes it forward-compatible without a schema change: adding `multi_turn` as a new valid value is a non-breaking addition.

---

## Post-V1, Multi-Turn Is Protocol-Dependent

The correct model is that `interaction: multi_turn` is only valid when the runtime also declares a protocol that supports it:

```yaml
capabilities:
  interaction: multi_turn
  protocols: [a2a]
```

A2A supports multi-turn via the `input-required` state machine. Lenny's own prompt protocol does not — the binary protocol's stdin/stdout model has no mechanism for the runtime to pause mid-execution and await a follow-up prompt from the calling agent without a full new task. Agent Protocol similarly does not support multi-turn in its current form — it has steps but those are executor-driven, not agent-requesting-input-driven.

The gateway should validate this combination at runtime registration time: `interaction: multi_turn` without a supporting protocol in `protocols` is a registration error. This prevents runtime authors from declaring multi-turn capability and then having it silently ignored when the runtime is only reachable via prompt mode.

The spec should document this constraint explicitly: multi-turn interaction requires an external protocol that supports it. It is not something Lenny's native prompt delivery can provide.

---

## Human (Apr 1, 2026, 8:35 AM):

In v1, multi_turn shouldn't be allowed, right? Even after v1, whether it is allowed will depend on the protocol (A2A supports it, Lenny prompt and AP don't)

---

## Claude:

Good set of questions. Let me take them one by one.

---

## 1. How Are Derived Runtimes Instantiated? How Does File Upload Work?

A derived runtime is registered via the admin API as a static configuration — it is not instantiated per-session. The `workspaceDefaults` in the derived runtime definition is a workspace plan that the gateway materializes into every pod that starts for that runtime. It is not a place where clients upload files at call time.

The mechanism for including files is:

**At registration time** — small files (configuration, scripts, graph definitions) are included inline in the `workspaceDefaults`:

```yaml
POST /v1/admin/runtimes

name: research-pipeline
type: agent
baseRuntime: langgraph-runtime
workspaceDefaults:
  files:
    - path: agent.py
      content: "..."           # inline content for small files
    - path: config.json
      content: "..."
  setupCommands:
    - pip install -r requirements.txt
```

**For larger files** — the admin API accepts a reference to a MinIO object path that the gateway copies into the workspace at pod start. The deployer uploads large assets to MinIO directly (using the existing artifact storage) and references them in `workspaceDefaults.refs`.

**At session creation time** — clients can still upload additional files on top of the derived runtime's defaults, using the existing `WorkspacePlan` upload flow. The derived runtime's defaults are materialized first, then the client's per-session uploads are layered on top. This is how you handle user-specific data — the runtime defines the base environment, the session adds the user's specific inputs.

The workspace materialization order is:
1. Base runtime's workspace defaults (if any)
2. Derived runtime's `workspaceDefaults`
3. Client's per-session uploads
4. File exports from parent delegation (if this session was created via `delegate_task`)

Later layers can overwrite earlier ones. The gateway validates against the runtime's `workspacePolicy` to prevent malicious overwrites of critical files.

---

## 2. Pre-Warming: Independent or Constrained?

Derived runtimes have fully independent pool and pre-warming settings. Each derived runtime can have its own pool registered against it, with its own `minWarm`, `maxWarm`, `executionMode`, isolation profile, and scaling policy.

The constraint is at the resource level, not the pool level: a derived runtime's pool cannot use a resource class that exceeds what the base runtime's pool is configured to allow. A base runtime registered for `small` and `medium` resource classes cannot have a derived runtime that spins up `xlarge` pods. This is enforced at admin API registration time.

The practical implication: a single `langgraph-runtime` base might have many derived runtimes, each with its own pool sized for its expected traffic. A `research-pipeline` derived runtime might have 5 warm pods. A `summarizer` derived runtime might have 20. Each is independently managed by the PoolScalingController.

If a derived runtime has no pool registered, the gateway falls back to the base runtime's pool. This is the zero-configuration path: register a derived runtime for configuration purposes (workspace defaults, agentInterface, delegation policy) without the overhead of managing a separate pool.

---

## 3. Startup Commands and Readiness

Yes — `setupCommands` in `workspaceDefaults` run after workspace materialization and before the runtime binary starts accepting tasks. While setup commands are executing, the pod is in the `INIT` state, not `READY`. The gateway does not route any tasks to it during this window.

This is already in the existing design (Section 7.4) but worth clarifying for derived runtimes specifically:

**The lifecycle with setup commands:**

```
pod claimed by PoolScalingController
    → workspace materialized (base defaults + derived defaults)
    → setup commands execute (pip install, compile, seed data, etc.)
    → runtime binary spawned
    → adapter signals READY to gateway
    → pod enters warm pool, available for sessions
```

Setup commands failing is a pod failure — the adapter signals the gateway, the pod is drained, and the PoolScalingController creates a replacement. This is important for derived runtimes with heavy setup: a failing `pip install` during pre-warming does not cause a session to fail, it causes a pod to be replaced before it ever enters the warm pool.

For derived runtimes with expensive setup commands (model downloads, large dependency installs), the `prewarmLeadMinutes` on the pool should be set generously to account for setup time. The PoolScalingController includes setup time in its pod readiness estimation.

One nuance: setup commands run once per pod, not once per task. In `executionMode: task` pools, the setup runs when the pod first starts and persists across all tasks that pod handles. If you need per-task setup, that belongs in the runtime binary's own initialization on receiving a prompt, not in `setupCommands`.

---

## Human (Apr 1, 2026, 8:51 AM):

Thanks, this makes sense.

Changing gears now. I have a few questions about runtimes with a base runtime (what used to be templates):
1. How are they instantiated? What is the mechanism by which a client can upload files while creating the runtime?
2. Do these runtimes have their own pre-warming settings, independent from those of the base runtime? Are they fully independent or are they constrained by the base image and, if so, how does that work?
3. Do I have the ability to create a runtime that will execute specific commands upon launch (as the pod is created)? While those commands are being executed, the runtime isn't ready to take in any requests, right?

---

## Claude:

Good questions. Let me take them in order.

---

## 1. `prewarmLeadMinutes` — Clarification

`prewarmLeadMinutes` was never a timeout — it was the lead time before the PoolScalingController starts spinning up variant pods ahead of an experiment's `startAt` time. It has nothing to do with setup command completion.

The actual readiness mechanism is already correct: the adapter signals READY only after setup commands complete successfully. The pod never enters the warm pool until that signal arrives. There is no timer involved in normal operation.

What you're asking for — an optional timeout on setup commands — is a separate concept that should be on the pool configuration:

```yaml
setupPolicy:
  timeoutSeconds: 300        # optional, no default timeout if absent
  onTimeout: fail            # fail (default) | warn
```

If `timeoutSeconds` is absent, the gateway waits indefinitely for the READY signal. If present and exceeded, the gateway treats the pod as failed and the PoolScalingController replaces it. This is the right model: setup completion drives readiness, with an optional safety net for runaway installs.

---

## 2. What Derived Runtimes Inherit vs Override

Reviewing the full conversation, the right model is a clear split between security/infrastructure settings (inherited, never overridable) and operational settings (independently configurable per derived runtime).

**Always inherited from base runtime — cannot be overridden:**

- `type` — agent or mcp. A derived mcp runtime cannot declare itself an agent.
- `executionMode` — session, task, or concurrent. The execution semantics are a property of how the base runtime binary works, not of the configuration layered on top.
- Isolation profile (`sandboxed` / `runc` / etc.) — security boundary set by the base, enforced by the base runtime's pool. A derived runtime cannot weaken isolation.
- `capabilities.interaction` — one_shot vs multi_turn (when supported). The binary either supports multi-turn or it doesn't.
- Allowed resource classes — a derived runtime cannot use resource classes not permitted by the base.
- `allowStandardIsolation` acknowledgment — if the base requires it, all derived runtimes require it too.

**Always independently configurable on derived runtime:**

- Pool settings (`minWarm`, `maxWarm`, `executionMode`-specific scaling)
- `setupCommands` — layered on top of base's setup commands, run after
- `workspaceDefaults` — layered on top of base's workspace defaults
- `agentInterface` — derived runtime can declare different skills, input/output modes
- `delegationPolicy` — can only restrict base's policy, never expand
- `publishedMetadata` — independent per derived runtime
- `capabilities.protocols` — derived runtime can declare a subset of base's supported protocols
- `tags` — independent
- `setupPolicy.timeoutSeconds` — derived runtime can set a tighter or looser timeout than base (or absent)

**Inheritable with override allowed — derived runtime can change within bounds:**

- `setupPolicy.timeoutSeconds` — derived can set its own, gateway takes the minimum of base and derived if both set
- `workspacePolicy` — derived can add restrictions, never remove them

The underlying principle: derived runtimes configure *what runs* and *how it's discovered*, not *how it runs* or *how it's isolated*. The base runtime owns the execution contract. The derived runtime owns the deployment configuration.

This should be enforced at admin API registration time with clear error messages — e.g. "cannot override executionMode: inherited from base runtime langgraph-runtime (task)".

---

## 3. Task Completion Cleanup Commands

Good addition. In `executionMode: task`, the scrub between tasks currently wipes the workspace and resets environment state. Cleanup commands give the runtime author a hook to perform graceful cleanup before the scrub — flushing a cache, closing file handles, resetting internal state that Lenny's scrub doesn't know about.

```yaml
name: code-executor
type: mcp
taskPolicy:
  cleanupCommands:
    - pkill -f jupyter_kernel
    - rm -rf /tmp/sandbox-*
  cleanupTimeoutSeconds: 30   # if absent, waits indefinitely
  onCleanupFailure: warn       # warn (default) | fail
```

The lifecycle in task mode becomes:

```
task completes
    → adapter sends terminate(reason: task_complete) on lifecycle channel
    → runtime acknowledges
    → cleanup commands execute
    → Lenny scrub runs (workspace wipe, tmpfs clear, env reset)
    → pod transitions back to available
    → next task can be assigned
```

Cleanup commands run before the Lenny scrub, not after — they operate on the state that exists at task completion, not on a wiped workspace. This is important: cleanup commands may need to access task artifacts or runtime state before they're wiped.

`onCleanupFailure: warn` means a failed cleanup command is logged but the pod continues to the scrub and becomes available again. `onCleanupFailure: fail` means a failed cleanup drains the pod and creates a replacement — appropriate when cleanup failure might leave state that could leak to the next task.

For `type: mcp` runtimes in concurrent mode, cleanup commands run per-slot after each slot's task completes, not when the pod shuts down. The pod itself stays running. The slot is unavailable during cleanup and becomes available again once cleanup and scrub complete.

---

## Human (Apr 1, 2026, 9:43 AM):

Thanks. A few follow-up questions:
1. Is `prewarmLeadMinutes` a timeout, or will Lenny always wait that amount of time and mark the pod ready after it? Ideally, Lenny should wait for the setup commands to complete, with maybe an optional timeout
2. I like that prewarming settings are independent for derived runtimes. However, shouldn't other settings be taken from the base runtime always? For example execution mode, or sandbox isolation settings. Please review the conversation up until now and come up with a sensible proposal
3. In task mode, where pods are reused for different tasks, I think it would be good to support taskCompletionCleanupCommands or something like that. I can see how that would be useful

---

## Claude:

This will be comprehensive. Let me produce the full updated document incorporating everything from the entire conversation.

---

# Lenny Technical Spec — Revised Changes from Design Review (Final)

## How to Read This Document

- **V1 Must-Have (Breaking)** — must be in v1; impossible to fix after the first community runtime ships
- **V1 Must-Have (Feature)** — must ship with v1 to meet stated goals
- **V1 Should-Have** — significant value; should ship but won't break compatibility if deferred
- **Planned / Post-V1** — document intent now so data models accommodate it; implement later
- **Explicit Non-Decisions** — things Lenny deliberately will not do

---

## Part 1: V1 Must-Have — Breaking Changes

---

### 1.1 Replace Custom CRDs With `kubernetes-sigs/agent-sandbox`

**What changes:** Replace `AgentPool`, `AgentPod`, and `AgentSession` CRDs and the WarmPoolController with the upstream `SandboxTemplate`, `SandboxWarmPool`, `SandboxClaim`, and `Sandbox` CRDs from `kubernetes-sigs/agent-sandbox` (launched as a formal Kubernetes SIG Apps subproject at KubeCon Atlanta, November 2025).

**Mapping:**

| Lenny CRD | Agent-Sandbox CRD |
|---|---|
| `AgentPool` | `SandboxTemplate` |
| `AgentPod` | `Sandbox` + `SandboxWarmPool` |
| `AgentSession` | `SandboxClaim` |
| WarmPoolController | Upstream SIG Apps controller |

**Implementation note:** The `SandboxClaim` optimistic-locking guarantee must be verified before committing. Task mode and concurrent mode remain Lenny-layer concerns — agent-sandbox hosts pods without knowing about multi-task reuse or intra-pod concurrency.

**Affects:** Sections 4.6, 6.1, 6.2, 6.3, Phase 1 and Phase 3 of build sequence.

---

### 1.2 Runtime Unification: `Runtime` Replaces `RuntimeType` and `SessionTemplate`

**What changes:** `RuntimeType` and `SessionTemplate` are collapsed into a single unified `Runtime` concept. The distinction is expressed via the optional `baseRuntime` field.

**Standalone runtime** — has `image`, no `baseRuntime`:
```yaml
name: langgraph-runtime
image: registry.example.com/langgraph:latest
type: agent
capabilities:
  interaction: one_shot
executionMode: task
isolationProfile: sandboxed
allowedResourceClasses: [small, medium, large]
delegationPolicyRef: orchestrator-policy
```

**Derived runtime** — has `baseRuntime`, no `image`:
```yaml
name: research-pipeline
baseRuntime: langgraph-runtime
workspaceDefaults:
  files:
    - path: agent.py
      content: "..."
  setupCommands:
    - pip install -r requirements.txt
setupPolicy:
  timeoutSeconds: 300
  onTimeout: fail
agentInterface: ...
delegationPolicyRef: research-policy
publishedMetadata: ...
tags: [approved, research]
```

**Inheritance rules — never overridable on derived runtime:**
- `type` — agent or mcp
- `executionMode` — session, task, or concurrent
- `isolationProfile` — security boundary set by base
- `capabilities.interaction` — one_shot (only valid value in v1)
- `allowedResourceClasses` — derived cannot use classes not permitted by base
- `allowStandardIsolation` acknowledgment

**Independently configurable on derived runtime:**
- Pool settings (`minWarm`, `maxWarm`, scaling policy)
- `workspaceDefaults` — layered on top of base's defaults
- `setupCommands` — layered on top of base's setup commands, run after
- `setupPolicy.timeoutSeconds` — gateway takes minimum of base and derived if both set
- `agentInterface`
- `delegationPolicyRef` — can only restrict base's policy, never expand
- `publishedMetadata`
- `tags`
- `taskPolicy.cleanupCommands` (for task mode)

**Workspace materialization order** (later layers overwrite earlier):
1. Base runtime's `workspaceDefaults`
2. Derived runtime's `workspaceDefaults`
3. Client's per-session uploads
4. File exports from parent delegation

**Base runtime mutability constraints via admin API:**
- `image` — immutable. Changing the image is a new runtime version.
- `name` — immutable. Names are identifiers referenced by derived runtimes.
- All other fields — mutable with impact validation. Changes that would invalidate existing derived runtimes are rejected with a list of affected runtimes.

**Pool independence:** Derived runtimes have fully independent pool settings. If a derived runtime has no pool registered, the gateway falls back to the base runtime's pool. Resource class constraints from the base runtime are enforced at pool registration time.

**Admin API surface:** `GET/POST/PUT/DELETE /v1/admin/runtimes` — handles both standalone and derived. No separate resource type for derived runtimes.

**Affects:** Section 5.1 (throughout), Section 15.1 and 15.2, Phase 1 (core types).

---

### 1.3 `type` and `capabilities` Fields on Runtime

**What changes:** Every `Runtime` resource has a `type` field and an optional `capabilities` field.

```yaml
type: agent          # agent (default) | mcp
capabilities:
  interaction: one_shot    # only valid value in v1
```

**`type: agent`** — a runtime that participates in Lenny's task lifecycle. Receives tasks, has sessions, workspace, delegation, elicitation, multi-turn dialog (future). Interacted with via `lenny/delegate_task`.

**`type: mcp`** — a runtime that hosts an MCP server. Lenny manages pod lifecycle (isolation, credentials, workspace, pool, egress, audit) but the interaction model is purely MCP. No session lifecycle, no `lenny/delegate_task`, no task records. The runtime binary is oblivious to Lenny.

**`capabilities.interaction`** is defined now as a single-value enum (`one_shot`) so adding `multi_turn` post-v1 is a non-breaking addition. The gateway rejects any runtime registration with `interaction: multi_turn` in v1 — not a graceful error, a hard rejection.

**`type: mcp` runtimes have no `capabilities` field** — it does not apply to tool servers.

**Affects:** Section 5.1 (runtime schema), Phase 1 (core types — must exist from day one).

---

### 1.4 Adapter Protocol Replaced: Multi-Server MCP + Lifecycle Channel

**What changes:** The stdin/stdout JSON Lines binary protocol (Section 15.4.1) is replaced by a two-part model.

**Part A — Multiple focused local MCP servers** (intra-pod, stdio or Unix socket):

- **Platform MCP server** — Lenny-specific tools: `lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`, `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`, `lenny/send_to_child` (stub, returns `not_implemented` in v1)
- **One MCP server per authorized connector** — each connector the session's delegation policy permits gets its own independent MCP server connection. Independent capability negotiation and auth context per connector. No aggregated connector proxy — aggregation is not lossless per MCP spec.

**No workspace MCP server.** The workspace is materialized to `/workspace/current` before the runtime starts. The runtime reads files directly from the filesystem. An MCP server adds no value here.

**Part B — Lifecycle channel** — separate stdin/stdout stream pair for operational events that do not belong in MCP:

```
Adapter → Runtime:  lifecycle_capabilities, checkpoint_request, interrupt_request,
                    credentials_rotated, terminate
Runtime → Adapter:  lifecycle_support, checkpoint_ready, interrupt_acknowledged,
                    credentials_acknowledged
```

Optional. Runtimes that don't open the lifecycle channel operate in fallback-only mode.

**Runtime integration layers (agent-type only):**
- **Layer 0** — any MCP-capable binary. Zero Lenny knowledge. Fallback lifecycle only.
- **Layer 1** — uses adapter's MCP servers (platform + connector servers). Standard MCP.
- **Layer 2** — Layer 1 plus lifecycle channel.

**Adapter manifest:** Written to `/run/lenny/adapter-manifest.json` before runtime process starts. Lists all available MCP server connection details. Regenerated per task execution (connector auth varies per delegation lease). Three sections:

```json
{
  "platformMcpServer": {
    "socket": "/run/lenny/platform-mcp.sock"
  },
  "connectorServers": [
    { "id": "github", "socket": "/run/lenny/connector-github.sock" }
  ],
  "runtimeMcpServers": [
    {
      "id": "code-executor",
      "socket": "/run/lenny/runtime-code-executor.sock",
      "capabilities": { ... }
    }
  ]
}
```

`connectorServers` and `runtimeMcpServers` look identical to the calling runtime — both are MCP server connections. The distinction is Lenny's internal concern.

**Affects:** Section 15.4 and 15.4.1 (full replacement), Section 4.7, Phase 2 of build sequence.

**Context for A2A:** Runtime authors speak MCP to adapter servers. If A2A intra-pod support is added post-v1, the adapter additionally serves A2A endpoints and the manifest lists the A2A base URL. Runtime authors must explicitly implement A2A client behavior to use it. A2A is not transparent to runtime authors.

---

### 1.5 Prompt Mode Task Delivery — Multimodal Input

**What changes:** Prompt mode is the only task delivery mode in v1. The inbound `prompt` message carries an `input` field that is an `OutputPart` array, not a plain text string:

```json
{
  "type": "prompt",
  "input": [
    { "type": "text", "mimeType": "text/plain",
      "inline": "Analyze this codebase" },
    { "type": "image", "mimeType": "image/png",
      "inline": "<base64>" },
    { "type": "data", "mimeType": "application/json",
      "inline": { "skill": "review_file", "path": "src/auth.py" } }
  ],
  "sessionId": "s_abc123"
}
```

This makes prompt mode capable of carrying any content type expressible as an `OutputPart` array — text, images, audio, structured data. Simple runtimes that only handle text destructure the array and take the first `text` part. Richer runtimes handle all part types.

**Two additional message types defined now for future multi-turn support (not implemented in v1):**

Outbound (runtime → adapter):
```json
{ "type": "input_required", "parts": [...] }
```

Inbound (adapter → runtime):
```json
{ "type": "follow_up", "input": [OutputPart[]] }
```

In v1 the adapter returns an error if `input_required` arrives, and never sends `follow_up`. The message types are defined so the binary protocol spec doesn't change when multi-turn ships.

**`mcp`-type runtimes:** `prompt` and `response` binary protocol message types are not applicable. Document explicitly as not used. Lifecycle channel stdin/stdout still applies.

**Affects:** Section 15.4.1 (adapter↔binary protocol message types).

---

### 1.6 `MCP`-Type Runtime Adapter Lifecycle

**What changes:** For `type: mcp` runtimes, the adapter lifecycle is fundamentally different from `type: agent`.

**Startup:** The runtime binary is spawned at pod start (pre-warm time), not at session start. The adapter polls the runtime's MCP socket until capability negotiation succeeds, then signals READY to the gateway.

**Task delivery:** There is no `StartSession`, no `PrepareWorkspace` per call, no credential assignment per call, no `prompt` message. The pod is always running and ready to accept proxied MCP connections.

**Adapter as proxy:** The adapter proxies inbound MCP connections from authorized callers to the runtime's local MCP server socket. The proxy is transparent — the calling runtime sees the target runtime's actual capability negotiation response. All traffic flows through adapter and gateway, never pod-to-pod.

**Readiness declaration:**
```yaml
name: code-executor
type: mcp
mcpServerSocket: /run/lenny/runtime-mcp.sock
readinessProbe:
  type: mcp_capabilities
  timeoutSeconds: 30
```

**No `lenny/receive_task`:** The MCP server needs no Lenny-specific tools. It exposes only its own tools (`execute_python`, `review_file`, etc.).

**`lenny/delegate_task` rejects `mcp`-type targets:** Returns `target_not_an_agent` error. The correct way to call an `mcp`-type runtime is via the MCP server connection in the adapter manifest.

**Affects:** Section 4.7 (adapter RPCs — new variant for mcp-type), Section 6.1 (pod state machine).

---

### 1.7 Internal `OutputPart` Format

**What changes:** `agent_text` streaming event replaced by `agent_output` carrying `OutputPart` array. `TaskResult.output` flat struct replaced by `parts: []OutputPart`. `TaskSpec.input` also uses `OutputPart` arrays.

```json
{
  "id": "part_abc123",
  "type": "text",
  "mimeType": "text/plain",
  "inline": "content here",
  "ref": "artifact://...",
  "annotations": { "role": "primary", "final": true },
  "parts": [],
  "status": "streaming | complete | failed"
}
```

- `type` is an open string — not a closed enum
- `mimeType` handles encoding separately from semantic type
- `annotations` is an open metadata map
- `parts` allows nesting
- `id` enables part-level streaming updates

SDK helper `from_mcp_content(blocks)` converts MCP content blocks to `OutputPart` arrays.

**Affects:** Section 15.4.1, Section 8.9 (TaskResult), Section 7.2 (session message types).

---

### 1.8 TaskRecord Uses Messages Array

**What changes:** The task record schema changes from flat `{input, output}` to a messages array to accommodate future multi-turn dialog without a schema migration:

```json
{
  "messages": [
    { "role": "caller", "parts": [...] },
    { "role": "agent",  "parts": [...], "state": "completed" }
  ]
}
```

In v1 there are always exactly two messages — one caller, one agent. The `state` field on agent messages supports future `input_required` state. This change is made now because retrofitting it later would touch every API surface that returns task results.

Task state machine gains `input_required` as a defined state even in v1:
```
submitted → running → completed
                    → failed
                    → input_required   (defined, not reachable in v1)
```

**Affects:** Section 8.9 (TaskResult schema), Section 8.2 (delegation mechanism).

---

### 1.9 Single `lenny/delegate_task` Tool

**What changes:** One delegation tool. Target id is opaque — the runtime does not know whether the target is a standalone runtime, a derived runtime, or an external registered agent.

```
lenny/delegate_task(
  target: string,
  task: TaskSpec,
  lease_slice?: LeaseSlice
) → TaskHandle
```

`TaskSpec` carries two separate input channels:
```json
{
  "input": [OutputPart[]],
  "workspaceFiles": {
    "export": [{ "glob": "src/auth/**", "destPrefix": "/" }]
  }
}
```

`input` is the prompt/context delivered to the child agent. `workspaceFiles` is what ends up on the child's filesystem. Separate because they serve different purposes.

**`lenny/delegate_task` rejects `type: mcp` targets** with `target_not_an_agent`. `external_delegate` is removed as a separate concept — external agents are registered as connectors, callable via the same tool. **No `external_delegate` tool.**

**Affects:** Section 8.2 (delegation mechanism), Section 8.9 (TaskSpec schema), Section 9.1.

---

### 1.10 `DelegationPolicy` as a First-Class Resource

**What changes:** `allowedRuntimes`, `allowedConnectors`, and `allowedPools` on the delegation lease are replaced by a named `DelegationPolicy` resource.

```yaml
POST /v1/admin/delegation-policies

id: orchestrator-policy
rules:
  runtimes:
    allow:
      ids: ["review-agent"]
      tags: ["approved"]
  connectors:
    allow:
      ids: ["github", "jira"]
  externalAgents:
    allow: []
```

**Two policy levels:**

**Runtime-level policy** (deployment time, Helm/config): Coarse capability constraints using tag rules. Set before derived runtimes exist. Example: "may call runtimes tagged `approved`."

**Derived runtime policy** (post-deployment, admin API): Can only restrict base's policy, never expand. Effective policy = `base_runtime_policy ∩ derived_runtime_policy`. Validated at creation time.

**Dynamic tag evaluation:** Tag rules evaluated at delegation time against current runtime tags. A new derived runtime tagged `approved` after a session starts is automatically accessible. A runtime losing the `approved` tag is immediately inaccessible.

**Session-level override:** `maxDelegationPolicy` at session creation further restricts the runtime's policy. Effective = `runtime_policy ∩ session_max_policy`.

**Discovery scoping:** `lenny/discover_agents` returns only targets the session's effective policy permits.

**Affects:** Section 8.3 (delegation lease schema), Section 5.1 (runtime schema).

---

### 1.11 `executionMode` on Pool Configuration from Day One

**What changes:** Pool configuration has `executionMode` from day one even if only `session` is initially implemented:

```yaml
executionMode: session   # session | task | concurrent
```

**Graph mode is removed** as a separate execution mode. Graph-aware runtimes are session-mode runtimes that optionally emit internal step traces through the observability protocol. The platform does not distinguish them.

**Affects:** Section 5.2, Phase 1 (core types).

---

### 1.12 `ExternalProtocolAdapter` Interface on the Gateway

**What changes:** Gateway's external MCP implementation refactored into a thin `ExternalProtocolAdapter` interface. MCP is the first implementation.

Accommodated protocols:
- **MCP** — first implementation, ships with v1
- **A2A** — planned post-v1. Inbound: `POST /a2a/{runtimeName}/tasks`. Outbound: external agents registered as connectors, called via `lenny/delegate_task`.
- **Agent Protocol (AP)** — planned post-v1. Defines `POST /ap/v1/agent/tasks` and step execution.

**Task state model note:** AP, A2A, and MCP have different task state models. Internal session state machine is canonical. Each adapter translates to and from it. No adapter-specific state leaks into core session model.

**Affects:** Section 4.1, Section 9.1.

---

### 1.13 `agentInterface` Field on Runtime

**What changes:** `type: agent` runtimes gain an optional `agentInterface` field.

```yaml
agentInterface:
  description: "Analyzes codebases and produces refactoring plans"
  inputModes:
    - type: "text/plain"
    - type: "image/png"
    - type: "application/json"
      schema: "{ 'skill': 'string', 'path': 'string' }"
  outputModes:
    - type: "text/plain"
      role: "primary"
  supportsWorkspaceFiles: true
  skills:
    - id: "review"
      name: "Code Review"
      description: "Reviews code for quality and correctness"
  examples:
    - description: "Review auth module"
      input: "Review the authentication module"
```

`supportsWorkspaceFiles: true` signals that `workspaceFiles` in `TaskSpec` will be honored. External agents always have `supportsWorkspaceFiles: false`.

Serves three purposes: discovery results, A2A card generation (auto-generated from this field), adapter manifest summaries.

`type: mcp` runtimes do not have `agentInterface`. Their capabilities are discovered via standard MCP capability negotiation.

**Affects:** Section 5.1.

---

### 1.14 `publishedMetadata` on Runtime

**What changes:** Both standalone and derived `Runtime` resources gain a `publishedMetadata` array.

```yaml
publishedMetadata:
  - key: agent-card
    contentType: application/json
    visibility: public
    value: '...'
```

Visibility: `internal` (intra-cluster, requires JWT), `tenant` (filtered by tenant_id), `public` (no auth, externally reachable).

Gateway serves as opaque pass-through. A2A agent card is one possible entry. Named `publishedMetadata` not `agentCard` — the generic mechanism is the correct abstraction.

**Affects:** Section 5.1.

---

### 1.15 `lenny/discover_agents` Tool

**What changes:** Tool in the adapter's platform MCP server. Returns `type: agent` runtimes (standalone and derived) and external agents the session's delegation policy permits. Does not return `type: mcp` runtimes — they are not callable via `delegate_task`.

```json
{
  "id": "research-pipeline",
  "kind": "derived-runtime",
  "baseRuntime": "langgraph-runtime",
  "agentInterface": { ... },
  "a2aEndpoint": null,
  "a2aCardUrl": null
}
```

`a2aEndpoint` and `a2aCardUrl` are null in v1. Populated when A2A support ships.

`id` is exactly what goes into `lenny/delegate_task`. Discovery is not required for delegation by name — `lenny/delegate_task` accepts any authorized target id directly.

Backed by pluggable `AgentDiscoveryStrategy` interface. Default returns everything the policy permits. Deployers replace with semantic search, capability-based filtering, or curated catalogs.

Each agent gets its own A2A endpoint when A2A is added — never aggregated (aggregation is not lossless).

**Affects:** Section 9 (new platform MCP server tool), Section 8.

---

## Part 2: V1 Must-Have — Feature Changes

---

### 2.1 Task Pod Execution Mode

**What changes:** `executionMode: task` reuses pods across sequential tasks. Between tasks: workspace wiped, tmpfs cleared, environment reset, cleanup commands run (if defined), credentials re-evaluated.

**Task mode lifecycle:**
```
task completes
    → adapter sends terminate(reason: task_complete) on lifecycle channel
    → runtime acknowledges
    → cleanup commands execute (if taskPolicy.cleanupCommands defined)
    → Lenny scrub runs
    → pod transitions back to available
    → next task assigned
```

**`taskPolicy` on runtime configuration:**
```yaml
taskPolicy:
  cleanupCommands:
    - pkill -f jupyter_kernel
    - rm -rf /tmp/sandbox-*
  cleanupTimeoutSeconds: 30
  onCleanupFailure: warn      # warn (default) | fail
```

Cleanup commands run before scrub (need access to task state). `onCleanupFailure: fail` drains the pod and creates a replacement — for cases where cleanup failure might leak state to the next task.

`setupCommands` run once per pod at start, not per task. Per-task initialization belongs in the runtime binary's prompt handler.

**Security requirement:** Explicit deployer acknowledgment required.

**Agent-sandbox note:** Multi-task reuse is invisible to agent-sandbox.

**Affects:** Sections 6.1, 6.2, 7, 5.2.

---

### 2.2 Concurrent Execution Mode

**What changes:** `executionMode: concurrent` allows multiple simultaneous task slots per pod. For runtimes with expensive shared in-memory state (self-hosted models). Not for general-purpose parallel execution.

**Two sub-variants:**

**Concurrent-workspace:** Each slot gets its own workspace. Gateway tracks per-slot lifecycle. Multiple workspaces coexist within the same pod. Isolation is process-level and filesystem-level — explicitly weaker than session mode. Deployer acknowledgment required.

**Concurrent-stateless:** No workspace materialization. Gateway routes through Kubernetes Service. Pod readiness probe reflects slot availability (`maxConcurrent` acts as semaphore). PoolScalingController watches `active_slots / (pod_count × maxConcurrent)`.

**Truly stateless runtimes** (no workspace, no expensive shared state) should be registered as external connectors, not Lenny-managed pods.

For `type: mcp` runtimes in concurrent mode, `taskPolicy.cleanupCommands` run per-slot after each slot's task completes.

**Affects:** Sections 5.2, 6.1, 6.2, 4.1.

---

### 2.3 `setupPolicy` on Runtime

**What changes:** Replaces the incorrectly-described `prewarmLeadMinutes` concept. `prewarmLeadMinutes` is the PoolScalingController's experiment lead-time, not a setup timeout.

```yaml
setupPolicy:
  timeoutSeconds: 300        # optional — if absent, waits indefinitely
  onTimeout: fail            # fail (default) | warn
```

Readiness is driven by the adapter's READY signal after setup commands complete. The timeout is a safety net for runaway installs. For derived runtimes, gateway takes the minimum of base and derived `timeoutSeconds` if both are set.

**Affects:** Section 5.1 (runtime schema), Section 6.2 (pod startup lifecycle).

---

### 2.4 `ConnectorDefinition` as First-Class Resource

**What changes:** Connector configuration becomes a first-class admin API resource. External agents (A2A, AP connectors) are registered here alongside tool connectors (GitHub, Jira). The `protocol` field distinguishes them. External agent ids participate in the same flat namespace as runtimes.

**Affects:** Section 4.3, Section 9.3.

---

### 2.5 Comprehensive Admin API

**What changes:** Full consistent admin API surface. Configuration stored in Postgres, cached with short TTL in gateway. Redis pub/sub for immediate invalidation.

**Operational plane — API-managed:**
- Runtimes: `GET/POST/PUT/DELETE /v1/admin/runtimes`
- Delegation Policies: `GET/POST/PUT/DELETE /v1/admin/delegation-policies`
- Connectors: `GET/POST/PUT/DELETE /v1/admin/connectors`
- Pool Configuration: `GET/POST/PUT/DELETE /v1/admin/pools`
- Credential Pools (metadata only): `GET/POST/PUT/DELETE /v1/admin/credential-pools`
- Tenants: `GET/POST/PUT/DELETE /v1/admin/tenants`
- Quotas: `GET/PUT /v1/admin/tenants/{id}/quotas`
- User Role Assignments: `GET/POST/DELETE /v1/admin/tenants/{id}/users`
- Egress Profiles: `GET/POST/PUT/DELETE /v1/admin/egress-profiles`
- Experiment Definitions: `GET/POST/PUT/DELETE /v1/admin/experiments`
- Scaling Policies: `GET/PUT /v1/admin/pools/{id}/scaling-policy`
- Memory Store Config: `GET/PUT /v1/admin/memory-store`
- Webhooks: `GET/PUT /v1/admin/webhooks`

**Bootstrap plane — Helm/config only:** Database URLs, Redis, MinIO, KMS, cluster name, namespace assignments, certificate paths, `LENNY_DEV_MODE`, system-wide defaults, Kubernetes object definitions.

**CRDs become derived state:** Reconciled from Postgres by PoolScalingController. `kubectl edit` is not the correct way to change pool settings.

**Admin API design constraints:**
- Same error taxonomy, pagination, OIDC auth as client-facing API
- Full resource representation with `etag` for conditional PUT
- `?dryRun=true` on all mutations
- OpenAPI spec generated from Go handler definitions
- All mutations flow through audit log
- Complete operational surface — every action expressible via API, nothing requiring direct Helm edits

**Affects:** Section 15.1, Section 10.2, Section 11.7.

---

### 2.6 PoolScalingController

**What changes:** Pool scaling intelligence extracted into dedicated `PoolScalingController` separate from `WarmPoolController`. WarmPoolController manages individual pod lifecycle. PoolScalingController manages desired pool configuration.

Backed by pluggable `PoolScalingStrategy` interface. Fully replaceable by deployers.

**Default formula:**
```
target_minWarm = ceil(base_demand_p95 × variant_weight × safety_factor)
```

`safety_factor` defaults to 1.5 for agent-type pools, 2.0 for mcp-type pools.

`scaleToZero` disabled by default for `type: mcp` pools. Deployer opt-in required.

**Pool phases:** pre-warm, ramp, steady state, wind-down — all automatic.

**Affects:** Section 4.6 (split into two controllers), Section 5.2.

---

### 2.7 Experiment Primitives

**What changes:** `ExperimentDefinition` as first-class admin API resource. `ExperimentRouter` as built-in `RequestInterceptor`. Experiment context propagates through delegation leases. Results API aggregates eval scores by variant. PoolScalingController manages variant pool lifecycle automatically.

Full schema, ramp policy, health-based rollback, and pool lifecycle automation unchanged from previous compilation.

**Affects:** Section 4.8, Section 15.1, Section 11.2.

---

### 2.8 `lenny/send_to_child` Stub

**What changes:** Platform MCP server gains `lenny/send_to_child(task_id: string, message: OutputPart[]) → void`. Returns `not_implemented` in v1. Activates when multi-turn ships. Defined now so the platform MCP server schema doesn't change when multi-turn is added.

**Affects:** Section 9 (platform MCP server tools).

---

## Part 3: V1 Should-Have

---

### 3.1 `MemoryStore` Interface

**What changes:** `MemoryStore` role-based storage interface. Default: Postgres + pgvector. Fully replaceable. Accessed via `lenny/memory_write` and `lenny/memory_query` in platform MCP server. Technology choice explicitly deferred — market not settled Q1 2026.

**Affects:** Section 4, Section 9.

---

### 3.2 Semantic Caching at the LLM Proxy

**What changes:** Optional `CachePolicy` on `CredentialPool`. Backed by pluggable `SemanticCache` interface — default Redis, fully replaceable. Disabled by default.

**Affects:** Section 4.9.

---

### 3.3 `RequestInterceptor` Extension Point

**What changes:** Formalized interceptor interface at phases: `PreAuth`, `PostAuth`, `PreRoute`, `PostRoute`, `PreToolResult`, `PostAgentOutput`. Built-in interceptors: `ExperimentRouter`, `GuardrailsInterceptor` (disabled by default — no built-in guardrail logic).

**Affects:** Section 4.8.

---

### 3.4 Agent Evaluation Hooks

**What changes:** `POST /v1/sessions/{id}/eval` accepts scores from external tools. `POST /v1/sessions/{id}/replay` re-runs against different runtime version. Lenny stores scores and surfaces them in dashboards and experiment results. No built-in eval logic.

**Affects:** Section 15.1, Section 16.

---

### 3.5 `CredentialRouter` Interface

**What changes:** Credential pool selection logic becomes pluggable `CredentialRouter` interface. Default: existing least-loaded/round-robin/sticky-until-failure. Disabled by default, opt-in per pool.

**Affects:** Section 4.9.

---

## Part 4: Planned / Post-V1

---

### 4.1 Multi-Turn Agent Dialog

**What to document now:**
- `interaction: multi_turn` is not valid in v1 but is a reserved enum value
- Multi-turn requires a protocol that supports it — A2A does, Lenny prompt mode and AP do not
- The gateway validates: `interaction: multi_turn` without a supporting `protocols` entry is a registration error
- The `input_required` task state, `follow_up` binary protocol message, and `lenny/send_to_child` tool are all defined in v1 schemas and activate when multi-turn ships
- Documentation using "a delegated task runs to completion" must be reworded to "runs until it completes, fails, or requests input"

---

### 4.2 A2A Full Support

**What to document now:**
- `ExternalProtocolAdapter` is the mechanism. A2A is implementation two.
- Inbound: gateway serves `POST /a2a/{runtimeName}/tasks` per `type: agent` runtime
- Outbound: external A2A agents registered as connectors, callable via `lenny/delegate_task`
- `lenny/discover_agents` returns `a2aEndpoint` and `a2aCardUrl` per agent when supported
- Each agent gets its own A2A endpoint — not aggregated
- A2A cards generated from `agentInterface` automatically
- `AuthEvaluator` must be extensible for Agent Card validation
- Delegation lease schema must include `allowedExternalEndpoints` from v1 (slot exists, unused until A2A)
- A2A `input-required` state maps directly to `input_required` in task record
- Runtime authors who want intra-pod A2A client behavior must explicitly implement it

**Adoption note:** A2A under AAIF/Linux Foundation governance as of August 2025. Slower adoption than MCP (97M monthly downloads) but neutral governance substantially improves long-term survival.

---

### 4.3 Agent Protocol (AP) Support

**What to document now:** AP defines `POST /ap/v1/agent/tasks` and step execution endpoints. Maps to Lenny session lifecycle via third `ExternalProtocolAdapter` implementation. No changes to intra-pod model.

---

### 4.4 MCP Mode Post-V1 Additions

**What to document now:** When MCP mode runtimes want native MCP tool semantics for agent-to-tool-server calls (bypassing `lenny/delegate_task`), the calling runtime connects to the tool server's MCP server via the adapter manifest and calls tools directly. The adapter-manifest `runtimeMcpServers` section slot exists from v1 — no manifest schema change needed.

---

### 4.5 A2A Intra-Pod Support

**What to document now:** The adapter can additionally serve per-agent A2A endpoints intra-pod when A2A gateway support ships. All traffic proxied through adapter and gateway. Runtime authors opt in explicitly.

---

### 4.6 UI and CLI

**What to document now:** Official CLI (`lenny-ctl`) and web portal are separate projects consuming the admin API as thin clients with zero business logic. Admin API is the complete operational surface — every action expressible via API.

---

## Part 5: Explicit Non-Decisions

---

### 5.1 No Model B Runtime Deployment

No mechanism for packaging a graph definition as a new registered runtime per graph. Users register derived runtimes via the admin API. Custom standalone runtime registration is for genuinely different binaries.

---

### 5.2 No Built-In Eval Logic

Lenny provides eval hooks and result storage. No LLM-as-judge, hallucination detection, or evaluation logic.

---

### 5.3 No Built-In Guardrail Logic

Lenny provides the `RequestInterceptor` hook. No content classifiers or prompt injection detection.

---

### 5.4 No Built-In Memory Extraction

Lenny provides the `MemoryStore` interface and tools. Runtimes decide what to write and how to structure it.

---

### 5.5 Hooks-and-Defaults Design Principle

Every cross-cutting AI capability (memory, caching, guardrails, evaluation, routing, scaling, discovery) follows the same pattern: defined as an interface with a sensible default implementation, all defaults disabled unless explicitly enabled, all interfaces fully replaceable. Lenny never implements AI-specific logic.

---

## Part 6: Build Sequence Changes

**Phase 1** — Core types: `Runtime` (not `RuntimeType`/`SessionTemplate`), `type` field, `capabilities` field with `interaction: one_shot`, `executionMode` field, `allowedExternalEndpoints` on delegation lease schema (slot only), `input_required` task state (defined, not reachable), messages array in TaskRecord. Agent-sandbox CRDs.

**Phase 2** — Replace adapter binary protocol with multi-server MCP + lifecycle channel. Prompt mode with `OutputPart` array input. Define `input_required` and `follow_up` message types (not implemented). Adapter manifest at `/run/lenny/adapter-manifest.json`. Publish as runtime adapter specification.

**Phase 3** — PoolScalingController. `DelegationPolicy` resource. `setupPolicy` enforcement.

**New Phase between 4 and 5** — Admin API foundation: runtimes (standalone and derived), pools, connectors, delegation policies, tenant management. Gateway loads config from Postgres.

**Phase 9** — `lenny/delegate_task` handles internal and external targets. `mcp`-type target rejection. `lenny/discover_agents` with full policy scoping.

**Phase 10** — `agentInterface` integration in discovery. Adapter manifest includes `agentInterface` summaries and `runtimeMcpServers` section.

**Phase 12** — `type: mcp` runtime support: adapter proxy, pool management, audit logging for MCP tool calls.

**New Phase 16** — Experiment primitives, PoolScalingController experiment integration.

**New Phase 17** — Memory, caching, guardrail, eval hooks.

---

## Summary

| Tier | Count |
|---|---|
| V1 Must-Have Breaking | 15 |
| V1 Must-Have Feature | 8 |
| V1 Should-Have | 5 |
| Planned / Post-V1 | 6 |
| Explicit Non-Decisions | 5 |

---

## Human (Apr 1, 2026, 10:22 AM):

Cool, this makes sense.

Now, revise your previous compilation of updates to the tech spec based on the subsequent back and forth in this conversation

---

## Claude:

Good set of corrections and questions. Let me take them in order.

---

## `setupPolicy.timeoutSeconds` — Maximum, Not Minimum

Corrected. The derived runtime may have a more generous timeout than its base to accommodate heavier setup (more dependencies, larger model downloads). The gateway takes the maximum of base and derived if both are set. A derived runtime cannot set a timeout shorter than the base's — that would be an invalid configuration rejected at registration time.

---

## Runtime Integration Layers — Are They Valid?

You're right to question this. The current layer definitions are:

- **Layer 0** — any MCP-capable binary, zero Lenny knowledge, fallback lifecycle only
- **Layer 1** — uses adapter's MCP servers
- **Layer 2** — Layer 1 plus lifecycle channel

The problem: for `type: agent` prompt-mode runtimes, the runtime must at minimum read from stdin (to receive the prompt) and write to stdout (to emit responses and tool calls). That's not "zero Lenny knowledge" — it's a specific binary protocol contract. A runtime that doesn't implement this contract cannot function at all.

The lifecycle channel is different — it's genuinely optional. A runtime that never opens it still works; it just can't support checkpoint, interrupt, or credential rotation.

The layers should probably be reframed as:

- **Minimum** — implements stdin/stdout binary protocol (prompt in, response/tool_call out). This is the floor for any `type: agent` prompt-mode runtime. No MCP usage, no lifecycle channel.
- **Standard** — minimum plus uses the adapter's platform MCP server and connector servers for tool access
- **Full** — standard plus opens the lifecycle channel for checkpoint, interrupt, and credential rotation support

Layer 0 as originally described (MCP-capable binary with no prompt protocol) doesn't make sense for prompt mode. It would make sense for MCP mode runtimes, but those have a completely separate adapter lifecycle. The layers concept should be scoped to `type: agent` prompt-mode runtimes only, and the minimum tier should explicitly require the binary protocol.

---

## All Three Execution Modes in V1

You didn't say the contrary earlier — I introduced the "only session in v1" framing myself in the build sequence. There's no prior decision to revisit. All three execution modes should be implemented in v1: `session`, `task`, and `concurrent`. The `executionMode` field exists from Phase 1, and all three are fully implemented by the time the platform is production-ready.

---

## Authentication for Public Visibility Runtimes

You're right that this is already handled by the gateway's external interface. A runtime with `visibility: public` in its `publishedMetadata` has its metadata served at a public endpoint — but that's just metadata (the agent card, capability descriptions). The actual session creation and task execution still go through the gateway's normal authentication and authorization path. Public visibility means "this agent's card is discoverable without auth" — not "this agent can be called without auth." The gateway enforces auth on all operational endpoints regardless of metadata visibility.

---

## Concurrent-Stateless vs Truly Stateless Connectors

The distinction is about why the runtime exists inside Lenny at all.

**Truly stateless as external connector:** The runtime has no need for Lenny's infrastructure. No isolation requirements, no sensitive credentials, no workspace, no per-tenant security concerns. It's just an HTTP service that happens to speak MCP. Registering it as a connector is correct — the gateway proxies calls to it, enforces auth, records audit events, but doesn't manage any pods for it. Example: a public search API wrapped as an MCP connector.

**Concurrent-stateless inside Lenny:** The runtime needs Lenny's infrastructure for something — isolation, sensitive credentials, per-tenant data separation, egress control — but doesn't need a per-request workspace. Example: a self-hosted model runtime where the model weights are loaded once and shared across all concurrent requests, but you still need gVisor isolation and credential management. Or a code execution runtime where the sandbox itself is the expensive shared state.

The key question is: does this runtime need anything that only Lenny can provide (pod isolation, credential leasing, egress profiles, per-tenant sandboxing)? If yes, it belongs inside Lenny even if it's stateless at the request level. If no, it belongs as an external connector.

**How concurrent-workspace and concurrent-stateless are differentiated in config:**

You're correct that the absence of workspace configuration is the primary signal, but it should be explicit rather than inferred. A dedicated `concurrencyStyle` field on the pool configuration makes the intent unambiguous:

```yaml
executionMode: concurrent
concurrencyStyle: stateless      # stateless | workspace
maxConcurrent: 8
```

For `concurrent-workspace`, workspace materialization, setup commands, and cleanup commands all apply per slot. For `concurrent-stateless`, none of those apply — the pod is permanently ready and accepts requests directly. The gateway uses `concurrencyStyle` to determine routing (Kubernetes Service for stateless, per-pod slot tracking for workspace). Making this explicit prevents misconfiguration where a deployer omits workspace config by accident and gets unexpected stateless routing behavior.

---

## Human (Apr 1, 2026, 10:59 AM):

A few corrections and questions:
* `setupPolicy.timeoutSeconds` — gateway takes ~~minimum~~ **maximum** of base and derived if both set
* Runtime integration layers (agent-type only):
   * Are these layers valid? For agents, I thought we agreed that at the very least the runtime needs to know when to start the runtime and how to receive a prompt. Isn't a minimal lifecycle channel integration always required?
* All 3 execution modes should be initially implemented, not just `session, unless I said the contrary earlier (in which case remind me and let's revisit that decision)`
* For runtimes with public visibility, should there be any sort of authentication? Or is that already provided by the gateways external interface? (That's probably it, but double-checking)
* Can you remind me the difference between concurrent-stateless execution mode and truly stateless runtimes that are handled as connectors? How are concurrent-workspace and concurrent-stateless differentiated in the config files? Is it just by lack of workspace configuration?

---

## Claude:

Agreed. Here is the spec-ready text for this section.

---

## Execution Mode: Concurrent

The `concurrent` execution mode allows a single pod to handle multiple simultaneous requests, up to `maxConcurrent`. It is intended for runtimes with **expensive shared in-memory state** that would be prohibitively costly or slow to replicate across separate pods — the canonical example being a self-hosted model whose weights are loaded once and shared across all concurrent requests.

Concurrent mode has two sub-variants, configured via `concurrencyStyle`:

```yaml
executionMode: concurrent
concurrencyStyle: stateless      # stateless | workspace
maxConcurrent: 8
```

---

### `concurrencyStyle: workspace`

Each concurrent slot gets its own isolated workspace materialized by the gateway before the request starts. Setup commands, cleanup commands, workspace exports, and credential scoping all apply per slot. Multiple workspaces coexist within the same pod simultaneously.

**Security note:** Cross-slot isolation is process-level and filesystem-level within the container — explicitly weaker than session mode's pod-per-session model. Deployer acknowledgment is required (`allowConcurrentWorkspace: true` on the pool configuration). This trade-off is appropriate when the cost of per-pod model loading outweighs the isolation benefit of separate pods.

**Suitable for:** Self-hosted model runtimes where the model is the expensive shared state, but each request still needs its own file context, credentials, and cleanup.

---

### `concurrencyStyle: stateless`

No workspace is materialized per request. The pod is permanently ready and accepts requests directly through its local MCP server or binary protocol. Kubernetes Service routing handles load distribution across pods. The pod's readiness probe reflects slot availability — at `maxConcurrent` active slots the pod marks itself not-ready, removing it from the Service endpoint set until a slot frees.

The PoolScalingController watches `active_slots / (pod_count × maxConcurrent)` and adds or removes pods accordingly.

**Suitable for:** Runtimes with expensive shared state that is purely computational (a loaded model, a large index, an initialized interpreter) where requests require no per-request file setup.

---

### Why Deploy a Stateless Runtime Inside Lenny at All?

A natural question arises: if a runtime is stateless at the request level, why not register it as an external connector and avoid pod management entirely?

The answer is that "stateless per request" and "requiring no Lenny infrastructure" are different properties. A runtime should be deployed inside Lenny as `concurrent-stateless` when it needs one or more of the following:

**Pod-level isolation.** An external connector runs in a shared process accessible to all tenants. A Lenny-managed pod runs in a gVisor or Kata sandbox with kernel-level isolation per tenant. If the runtime executes code, processes sensitive data, or is simply not trusted to run in a shared environment, the pod sandbox is the right boundary regardless of whether each request needs a workspace.

**Credential leasing.** The runtime needs access to sensitive credentials (a database connection, a private model API key, a cloud storage token) that should not be embedded in the runtime image or passed as environment variables accessible to all tenants. Lenny's credential leasing model injects credentials into the pod at start time, scoped per tenant and rotatable without restarting the pod.

**Per-tenant egress control.** The runtime makes outbound network calls and the deployer needs to enforce different egress profiles per tenant. An external connector cannot be egress-controlled at the tenant level.

**Audit trail at the request level.** Each request is logged with the calling session ID, tenant ID, tool name, and cost attribution. An external connector's calls are logged at the connector proxy level but without the same session-level granularity.

**Warm pool management.** The PoolScalingController keeps a calibrated number of warm pods ready based on observed demand. An external connector relies on whatever scaling its own infrastructure provides — which may not match Lenny's traffic patterns.

If none of these apply — the runtime is trusted, needs no sensitive credentials, has no egress requirements, and the deployer is comfortable with external-connector-level audit granularity — then registering it as an external connector is the simpler and correct choice. Lenny should not be in the business of managing pods that don't benefit from its infrastructure.

The decision rule: **if the runtime needs anything that only Lenny's pod infrastructure can provide, deploy it inside Lenny. If it doesn't, register it as a connector.**

---

## Human (Apr 1, 2026, 11:02 AM):

Thanks. The distinction between the two concurrent "submodes" and the reasons why someone would consider deploying a truly stateless concurrent runtime inside Lenny should be made clear in the technical spec

---

## Claude:

Let me research how this is handled today before proposing anything.Good research base. Let me synthesize what's out there and then make concrete recommendations.

---

## How the Industry Handles This Today

**AG-UI (CopilotKit)** is the most directly relevant protocol. AG-UI supports pausing, approving, editing, retrying, or escalating mid-flow without losing state. Users can interrupt, cancel, or reply to agents in the middle of a task without losing context. It does this through a bidirectional event stream — the client can push new events into a running session at any point, not just in response to an elicitation. The INTERRUPT event type pauses agent execution to request human approval, acting as a safety valve for sensitive actions.

**LangGraph** handles this at the graph runtime level via `interrupt()`. When you call interrupt within a node, LangGraph suspends execution by raising an exception that signals the runtime to pause, saves graph state, and waits indefinitely until you resume execution. When execution resumes, the runtime restarts the entire node from the beginning — it does not resume from the exact line where interrupt was called. The key point is that LangGraph's interrupt is always agent-initiated — the agent asks to be paused. Client-initiated mid-execution injection is not a first-class primitive.

**Claude Code** supports the closest thing to what you're describing. You can interrupt Claude at any point. If it's going down the wrong path, just type your correction and press Enter. Claude will stop what it's doing and adjust its approach based on your input. You don't have to wait for it to finish or start over. This is genuinely mid-execution guidance, not just pre-task or post-task.

**A2A** does not address mid-execution injection directly. Its multi-turn model is sequential — the caller sends a task, the agent completes or enters `input-required`, the caller responds. There is no concept of the caller pushing new information to an agent that is actively processing.

**AP (Agent Protocol)** similarly has no mid-execution injection primitive. Its step model is caller-driven but only advances the execution forward, not sideways.

---

## The Two Use Cases Are Actually Different Problems

Your two cases have different semantics worth separating:

**Use case 1 — Client guidance injection:** The client wants to steer a running session. The agent should stop what it's doing, incorporate the new information, and re-plan. This is client-to-agent, and it interrupts and redirects.

**Use case 2 — Sibling agent notification:** A parent agent has received output from child A and wants to share it with child B, which is still running. This is agent-to-agent lateral communication, not client-to-agent. Child B should incorporate this new context without necessarily stopping — it might be between steps and can just add it to its working context.

These are different enough that they probably need different primitives.

---

## Recommended Design

### Use Case 1: `POST /v1/sessions/{id}/inject`

A new REST endpoint on the session API:

```json
POST /v1/sessions/{id}/inject

{
  "message": [OutputPart[]],
  "behavior": "interrupt_and_replan"   // interrupt_and_replan | append_context
}
```

**`interrupt_and_replan`** — the gateway sends an interrupt signal to the adapter via the lifecycle channel (`interrupt_request`). The runtime acknowledges, stops its current work, and the adapter delivers the injected message as a new `follow_up` inbound message via stdin. From the runtime's perspective, it receives a new prompt mid-execution with the injected content. The runtime re-evaluates and re-plans. The session's message log records the injection with `role: "client_injection"` so the audit trail is clean.

**`append_context`** — gentler. The gateway queues the message to be delivered to the runtime the next time it completes a tool call and returns to the adapter's stdin loop. The runtime sees it as an additional inbound message without being interrupted mid-reasoning. Useful for "here's some information you might find useful" without disrupting the current step.

Both behaviors require the adapter's interrupt/follow_up mechanism we already designed. The `interrupt_and_replan` behavior uses the existing `interrupt_request` lifecycle channel message plus the new `follow_up` binary protocol message. The `append_context` behavior uses only `follow_up` queued for next opportunity.

**Authorization:** Only the session's creating client can inject. Injections are audit-logged with the client's identity, timestamp, and message content. Injection rate-limited per session (configurable, default 10 per minute) to prevent abuse.

**Runtime contract:** The runtime must support the `follow_up` inbound message type to receive injections. Layer 0 runtimes (which only implement the minimal binary protocol) receive injections but have no guarantee about how they're processed — behavior is runtime-defined. Layer 1+ runtimes receive injections through the platform MCP server notification model (discussed below).

---

### Use Case 2: `lenny/notify_sibling`

A new tool in the platform MCP server for parent agents to send lateral context to running child sessions:

```
lenny/notify_sibling(
  task_id: string,        // the running child's task handle
  message: OutputPart[]   // the context to share
) → void
```

The gateway validates that the calling session is the parent of the target task (same task tree, delegation lease check), then delivers the notification to the target child session as a queued `append_context` injection. The child receives it the next time it completes a tool call.

This is intentionally non-interrupting — sibling notification should not disrupt a running child's current reasoning step. If the parent needs the child to stop and re-plan based on the new information, the parent should cancel the child via `lenny/cancel_child` and spawn a new child with the updated context.

The reason for this design: if child A's output makes child B's entire plan invalid, cancellation and restart with full context is safer and more predictable than mid-execution injection. Mid-execution injection is appropriate when the new information is additive ("here's something useful to know as you proceed") not when it makes the current plan wrong ("stop, everything has changed").

---

## What Needs to Change in the Design

**Binary protocol additions:** `follow_up` inbound message already defined as a stub. `inject` behavior needs to be distinguished from the multi-turn `follow_up` at the binary protocol level — they could be the same message type with a `source` field:

```json
{
  "type": "follow_up",
  "source": "client_injection",    // client_injection | parent_notification | multi_turn_response
  "behavior": "interrupt_and_replan",
  "input": [OutputPart[]]
}
```

**Lifecycle channel:** `interrupt_request` already exists for use case 1's `interrupt_and_replan` behavior.

**Session message log:** Add `role: "client_injection"` and `role: "sibling_notification"` as new message roles in the TaskRecord messages array. This preserves the audit trail and makes the session replay (`POST /v1/sessions/{id}/replay`) deterministic.

**Platform MCP server:** `lenny/notify_sibling` is a new tool.

**REST API:** `POST /v1/sessions/{id}/inject` is a new endpoint.

**Authorization:** Session injection requires the session's original auth token. Sibling notification requires the calling session to be a parent of the target in the task tree.

---

## What Remains Out of Scope

Broadcasting to all running children at once — this is a specific case that can be composed from `lenny/notify_sibling` called once per child. No new primitive needed.

Injection into `type: mcp` runtimes — not applicable. MCP tool servers don't have session state to inject into.

Injection that modifies the workspace mid-execution — the workspace is the runtime's responsibility during execution. The injection mechanism only delivers messages to the runtime's reasoning loop, not files to the filesystem. If the client wants to add files, the right mechanism is to cancel the session, upload the files, and restart.

---

## Human (Apr 1, 2026, 11:12 AM):

Great, thanks.

Changing gears a bit. One capability we haven't discussed at all yet is the ability for a client to inject new prompts into a session outside of the standard elicitation pattern and outside of the inter-agent multi-turn conversation internal protocol.

I don't know if this is supported by A2A or AP today, but a couple of use cases are:
* The client wants to share new information with the agent(s) to guide the tasks execution. This should interrupt the agent's flow and the new information re-evaluated and a new plan formed before continuing with the task's execution
* A parent agent has received the outut of a child agent and wants to inform another child agent that's still in progress about this output

I'm not sure if Claude Code or similar support this type of pattern -maybe something to explore.

Can you let me know if this makes sense, research how this problem is being solved in standards and products today, and come up with recommendations?

---

## Claude:

Let me pull the exact decisions from our conversation rather than risk misremembering details.Good — I have the full picture from the transcript. Here's exactly what we designed and what status each piece has.

---

## What Was Actually Designed

The interrupt/follow_up mechanism was introduced specifically to support **future multi-turn agent dialog** without requiring a schema change when that feature ships. Nothing in this mechanism is functional in v1 — it's all defined but not implemented.

**Four pieces were defined together as a unit:**

**1. `input_required` task state** — added as a defined-but-unreachable state in the task state machine:
```
submitted → running → completed
                    → failed
                    → input_required   ← defined in schema, never reached in v1
```
The runtime would transition to this state when it needs more information before it can continue. In v1, if a runtime emits the `input_required` outbound message, the adapter returns an error.

**2. `input_required` outbound message (runtime → adapter via stdout):**
```json
{ "type": "input_required", "parts": [...] }
```
Defined in the binary protocol spec. Adapter rejects it in v1.

**3. `follow_up` inbound message (adapter → runtime via stdin):**
```json
{ "type": "follow_up", "input": [OutputPart[]] }
```
Defined in the binary protocol spec. Never sent by the adapter in v1.

**4. `lenny/send_to_child` tool in the platform MCP server:**
```
lenny/send_to_child(task_id: string, message: OutputPart[]) → void
```
Defined as a tool stub. Returns `not_implemented` in v1. This is how a parent agent would respond to a child that entered `input_required` state.

**The lifecycle channel `interrupt_request` / `interrupt_acknowledged`** already existed in the lifecycle channel design independently — it's how the adapter signals the runtime to stop current work cooperatively. This is functional in v1 as part of the lifecycle channel.

---

## How This Relates to Your New Use Case

The new use case you're describing — client or sibling injecting messages into a running session — maps onto the same `follow_up` mechanism, with one important difference in who initiates it. In the multi-turn design, `follow_up` is the parent agent's response to a child's `input_required` request — the child asked to be paused. In your new use case, the injection is externally initiated without the runtime requesting it — the client or sibling decides to push information in.

So the `follow_up` message type is the right delivery mechanism for both cases, but the triggering path differs:
- Multi-turn: child emits `input_required` → parent calls `lenny/send_to_child` → adapter delivers `follow_up`
- Client injection: client calls `POST /v1/sessions/{id}/inject` → gateway sends `interrupt_request` on lifecycle channel (if `interrupt_and_replan`) → adapter delivers `follow_up`
- Sibling notification: parent calls `lenny/notify_sibling` → gateway queues `follow_up` for next delivery opportunity

The `source` field I proposed to distinguish these cases fits cleanly into the existing `follow_up` message structure without any conflict with the multi-turn design.

---

## Human (Apr 1, 2026, 11:32 AM):

Refresh my memory on the interrupt/follow_up mechanism we already designed

---

## Claude:

The core question is whether the interrupt signal and the follow_up content delivery need to be on separate channels, or whether a single richer stdin message can serve both purposes. The answer depends on what "interrupt" actually means in practice.

---

## The Case for Consolidation

A unified message is simpler for runtime authors. A runtime that only reads stdin gets both the signal and the content atomically — it knows immediately both that it should stop current work and what the new content is. With the two-mechanism model, there's a window between `interrupt_request` arriving on the lifecycle channel and the subsequent `follow_up` arriving on stdin where the runtime knows to stop but doesn't yet know why. That's a slightly awkward intermediate state.

The consolidated message would look like:

```json
{
  "type": "follow_up",
  "behavior": "interrupt_and_replan",
  "source": "client_injection",
  "input": [OutputPart[]]
}
```

vs the current two-step:
1. lifecycle channel: `{ "type": "interrupt_request" }`
2. stdin: `{ "type": "follow_up", "input": [...] }`

For Layer 0 and Layer 1 runtimes that don't open the lifecycle channel, this is particularly valuable — they can receive interrupt-and-replan injections purely through stdin without needing to implement lifecycle channel handling at all.

---

## The Core Problem With Consolidation

stdin is a sequential channel. The runtime reads one message at a time in a loop. If the runtime is blocked mid-step — waiting for an LLM API response, blocked on a tool call result — the `follow_up` message sits in the stdin buffer unread until that blocking operation completes. The lifecycle channel is an out-of-band channel precisely because it can be monitored independently of the stdin processing loop, allowing the runtime to notice the interrupt signal even while blocked elsewhere.

This matters most for long-running LLM calls. If a runtime submits a complex multi-step reasoning request to an LLM provider and that call takes 30 seconds, an `interrupt_request` on the lifecycle channel gives the runtime a chance to cancel that LLM call and respond to the injection immediately. The same signal embedded in stdin can only be read after the LLM call returns.

However — this distinction is somewhat theoretical. In practice, even with a lifecycle channel interrupt, most runtimes can't cancel an in-flight LLM API call. The HTTP request is already in flight. The runtime still has to wait for the response, acknowledge the interrupt, and then decide what to do. So the practical difference in responsiveness between the two approaches is smaller than it appears.

---

## The Right Frame: Cooperative vs Preemptive

The real question is whether Lenny's interrupt model is cooperative or preemptive.

**Cooperative** — the runtime processes the injection at the next natural pause point between processing steps. This is what LangGraph does with `interrupt()` and what Claude Code does when you type mid-execution. The runtime finishes its current atomic step, sees the pending injection, and re-plans before starting the next step. This is the right model for most agent workflows — a runtime in the middle of an LLM reasoning step shouldn't be preempted anyway because it would leave the session in an inconsistent state.

**Preemptive** — the signal arrives out-of-band and the runtime genuinely stops immediately, even mid-step. This is what `interrupt_request` on the lifecycle channel enables in principle, though as noted above, the practical benefit is limited because most blocking operations (LLM calls, tool calls) can't actually be cancelled mid-flight.

If cooperative interruption is sufficient — and for agent workflows it almost certainly is — then consolidating into a richer stdin `follow_up` message is the cleaner design. The `behavior` field tells the runtime what to do with the message when it next reads stdin: handle it before starting the next step (`interrupt_and_replan`), append it to context for later (`append_context`), or treat it as a response to a previous `input_required` emission (`response_to_input_required`).

---

## What This Means for the Lifecycle Channel

If you consolidate interrupt into stdin, the lifecycle channel becomes smaller but doesn't disappear. It still has legitimate out-of-band uses that genuinely need a separate channel:

- `checkpoint_request` — needs to arrive while stdin may be mid-conversation to request a clean snapshot point
- `terminate` — pod drain, must arrive regardless of stdin state  
- `credentials_rotated` — mid-session credential rotation, needs to interrupt credential usage

These are platform operational signals, not content delivery. They're genuinely different from the `follow_up` concept and belong on a separate channel. So the lifecycle channel survives consolidation, just with `interrupt_request` removed from it.

---

## Recommendation

Consolidate. Accept cooperative interruption as the model — which is honestly the right model for agents anyway since you generally want them to finish their current reasoning step before incorporating new information. Move `behavior` metadata into the `follow_up` stdin message. Keep the lifecycle channel for `checkpoint_request`, `credentials_rotated`, and `terminate`, which are genuinely out-of-band operational signals.

The practical implication for the spec: `interrupt_request` is removed from the lifecycle channel message table. The `follow_up` message gains a `behavior` field. The "interrupt" that happens before an `interrupt_and_replan` follow_up is now cooperative — the runtime sees it the next time it reads stdin between steps — rather than the current two-step model where the lifecycle channel signal might arrive slightly earlier. This is a simplification, not a degradation.

---

## Human (Apr 1, 2026, 11:39 AM):

Thanks. What would be the implications of enhancing the follow_up mechanism to include additional metadata (such as interrupt or whether it's a response to an input request) instead of using interrupt + follow up?

---

## Claude:

Yes, and it's worth thinking through carefully because it's a distinct use case from injection.

---

## The Pure Interrupt Pattern

A bare `interrupt_request` without accompanying content represents a **pause-and-decide** pattern: stop the agent's current work, hold its state, and leave the session suspended while the client figures out what to do next. The client hasn't yet decided whether to inject new content, resume from where the agent left off, or terminate entirely.

This maps directly to LangGraph's `interrupt()` primitive — execution suspends, state is preserved, and the caller independently decides to resume with `Command(resume=value)` or with new input. The decision about what comes next is decoupled from the act of stopping.

---

## Why This Is Distinct From Injection

With `interrupt_and_replan` in a `follow_up` message, the client already knows what new content to provide at the moment it decides to interrupt. The interrupt and the new direction are atomic — the runtime stops and immediately gets the redirection.

With a standalone `interrupt_request`, the client wants to stop the agent first and then make a separate decision. Common scenarios:

**Review before deciding.** The client wants to see the agent's current output stream before deciding whether to redirect it or let it continue. You interrupt, observe, then either send a `follow_up` or call `resume_session`.

**Approval gate.** A supervisor or monitoring system detects something concerning and stops the agent while a human reviews what happened. The human then decides: send corrective guidance, resume normally, or terminate.

**Resource preemption.** A lower-priority session gets interrupted to free capacity, with the expectation of resuming later with no new content — just continuation from the suspended state.

**Coordinated pause across a task tree.** A parent agent wants to pause all running children simultaneously before issuing new guidance to each. The interrupt is sent to all children at once; content injection happens individually afterward.

---

## Session State Implication

For standalone interrupt to be meaningful, the session needs an explicit `suspended` state distinct from `running`, `completed`, and `failed`:

```
running → suspended   (via interrupt_request + interrupt_acknowledged)
suspended → running   (via resume_session — no new content)
suspended → running   (via follow_up with behavior: inject — new content)
suspended → completed (via terminate)
```

This is a meaningful addition. A session in `suspended` state holds its pod, preserves workspace state, and waits. The existing `interrupt_session` MCP tool in Section 7.2 already implies this should exist — today it's defined as a client-facing action without a clear resulting state. `suspended` formalises what that action produces.

---

## The Lifecycle Channel Is the Right Home

Since standalone interrupt genuinely needs to arrive out-of-band — you want the runtime to stop before reading the next stdin message, not after it finishes processing whatever is currently buffered — the lifecycle channel is the right home for it, exactly as originally designed. The previous discussion about consolidation applies only to the injection case where interrupt and content are always delivered together. A bare interrupt that isn't accompanied by content has no stdin component to consolidate into.

---

## Summary

Keep `interrupt_request` on the lifecycle channel as a standalone signal producing `suspended` session state. It is not redundant with injection — it represents a different interaction pattern where the decision to stop and the decision about what to do next are decoupled in time. The full set is then:

| Mechanism | Channel | Behavior |
|---|---|---|
| `interrupt_request` | Lifecycle channel | Pause → `suspended` state. Client decides next action separately. |
| `follow_up` with `behavior: interrupt_and_replan` | stdin | Atomic stop + new content. Client already knows what to inject. |
| `follow_up` with `behavior: append_context` | stdin | Add context at next natural pause. No interruption. |
| `follow_up` with `behavior: response_to_input_required` | stdin | Runtime-requested multi-turn response. |
| `resume_session` | Gateway REST/MCP | Resume from `suspended` with no new content. |

---

## Human (Apr 1, 2026, 11:43 AM):

Is there a place for interrupt_request as a lifecycle action? Independent from injection

---

## Claude:

These are all good instincts. Let me work through each carefully before proposing anything concrete, because getting the foundation wrong here is exactly the kind of thing that causes major refactors.

---

## On Behavior Being Optional and Generic

You're right that a closed enum of behaviors is the wrong abstraction. The behavior field was trying to solve two different problems simultaneously: instructing the runtime how to handle the message mechanically, and conveying semantic intent. These shouldn't be conflated.

The mechanical handling — whether to interrupt current work or queue for the next pause point — is what the Lenny platform cares about. The semantic interpretation — what this new information means for the agent's plan — is what the runtime's LLM cares about, and the platform has no business prescribing that.

A cleaner model: the platform-level field is `delivery` not `behavior`, and it has only two values that express a mechanical delivery preference:

```json
{
  "type": "follow_up",
  "delivery": "immediate",     // deliver at next stdin read, after interrupt_ack if interrupted
  "source": "client_injection",
  "inReplyTo": "req_abc123",   // optional, discussed below
  "input": [OutputPart[]]
}
```

`immediate` means deliver as soon as the runtime can read stdin, which may mean waiting for the current step to complete cooperatively. The absence of `delivery` (defaulting to queued) means append to the next natural pause point.

Everything else — whether to re-plan, how urgently to incorporate the new information, what it means for the current task — is left entirely to the runtime. The runtime can pass the injection to its LLM with the current plan context and let the model decide. Lenny doesn't know and shouldn't know.

---

## On Capability Flags for Injection

You're right that this needs to be a declared capability. The reasoning is slightly different for each case:

**One-shot runtimes** — structurally incapable of receiving injections. After the initial `prompt` message, the runtime runs to completion and emits a `response`. There is no stdin polling loop between those two events. The platform should reject injection attempts against one-shot sessions at the API level, before they even reach the adapter.

**Multi-turn runtimes** — capable in principle, but whether a specific runtime handles injection gracefully depends on the runtime's design. A runtime that maintains an explicit planning loop can check for pending injections between iterations. A runtime that doesn't has no defined point at which to process them.

The capability declaration on `Runtime`:

```yaml
capabilities:
  interaction: one_shot | multi_turn
  injection:
    supported: true | false       # default: false
    modes: [immediate, queued]    # which delivery modes are supported
```

`injection.supported: false` causes the gateway to reject `POST /v1/sessions/{id}/inject` and `lenny/notify_sibling` calls targeting that session with a clear `capability_not_supported` error. This is enforced at the gateway level — the runtime never sees a message it declared it can't handle.

The granularity of `modes` is genuinely useful: a runtime that only supports `queued` delivery (append context at natural pause) is declaring it can't handle being interrupted mid-step even cooperatively. A runtime supporting `immediate` is declaring it actively polls for injections between steps.

---

## On Interrupt Cascade

Your instinct to defer to the runtime is right, but the lifecycle channel semantics need to be explicit about what the platform does versus what the runtime decides.

When the gateway sends `interrupt_request` to a session:
- The platform's responsibility ends at the session boundary. It does not automatically cascade to children.
- The runtime's responsibility, once it acknowledges the interrupt, includes deciding what to do with its running children.

This is actually consistent with how the existing delegation model works. The runtime owns the semantics of the task tree below it — it decides whether to `lenny/cancel_child` or let children run to completion or await them first. Cascading automatically would violate this: the platform would be making a semantic decision that belongs to the orchestrating runtime.

However there is one exception worth noting: **budget exhaustion** and **lease expiry** do cascade automatically today, because those are platform-level constraints not runtime decisions. A session whose `maxSessionAge` expires gets terminated along with all its descendants regardless of runtime intent. This is already in the design and remains correct — it's a resource protection mechanism, not a coordination primitive.

The spec should state this explicitly: `interrupt_request` does not cascade. Budget/lease expiry does cascade. The runtime is responsible for deciding whether to propagate a received interrupt to its children.

---

## On Input Request IDs

Yes, and this matters more than it might seem at first. Consider a runtime that has delegated to multiple children and one of them enters `input_required` state asking for guidance. The parent receives the `input_required` message from the child, needs to reason about it, and then sends a `follow_up` back. Without IDs, the parent can only send a follow_up to the child session as a whole — there's no way to say "this is specifically a response to request X, not a new injection."

The ID also enables a runtime to issue multiple outstanding `input_required` requests simultaneously — asking for different pieces of information from different sources — and correlate responses correctly. This is an unusual pattern today but becomes natural in more complex orchestration scenarios.

```json
{
  "type": "input_required",
  "requestId": "req_abc123",
  "parts": [...]
}
```

```json
{
  "type": "follow_up",
  "inReplyTo": "req_abc123",   // optional — if absent, it's a standalone injection
  "input": [OutputPart[]]
}
```

`inReplyTo` is optional. A `follow_up` without it is an unsolicited injection. A `follow_up` with it is a direct response to a specific request. The runtime can use this to route the response correctly in its internal state. The gateway validates that the referenced `requestId` exists and belongs to the correct session.

---

## On Future Conversational Patterns

This is the hardest question and worth thinking through carefully because the design decision here is load-bearing for everything downstream.

The patterns that matter, roughly in order of how likely they are to materialize:

**Threaded messages.** A human or agent sends a message that is a reply to a specific prior message in the conversation, not the most recent one. This happens in collaborative editing tools, in Slack, in any asynchronous communication system. The enabling primitive is just a `replyTo` field on outbound messages referencing a message ID. The `inReplyTo` we just designed is a step in this direction, but it only covers the injection→response path, not general message threading within a conversation.

**Multiple participants.** Multiple users (or agents) contributing to the same session. This isn't about multi-agent delegation — it's about multiple distinct principals whose messages interleave in a single session's history. Collaborative coding tools, multi-reviewer workflows, human-agent-human triads. The enabling primitive is a `from` field on messages carrying a participant identity, and a participant registry on the session.

**Non-linear context.** The agent's effective context isn't just the linear message history — it's a graph where some messages are more relevant to the current moment than others. A message sent 50 turns ago might be more relevant to the current step than a message sent 2 turns ago. This is mostly a runtime concern (how the runtime constructs its LLM context window) but the platform can support it by ensuring the full message history is queryable, not just the most recent N messages.

**Message priority.** Some injections are more urgent than others. A safety system flagging a critical issue is different from a user offering an optional suggestion. The `delivery` field partially addresses this but priority as a first-class concept could allow runtimes and the platform to make better scheduling decisions.

**Broadcast.** One message to multiple sessions simultaneously. Already composable from `lenny/notify_sibling` called N times, but a first-class broadcast primitive might be useful for large fan-out scenarios.

---

## The Minimal Future-Proof Foundation

Rather than designing all of these patterns now, the right approach is to ensure the message envelope is extensible enough to carry them when needed. A message format that accommodates future conversational patterns:

```json
{
  "id": "msg_xyz789",               // every message has a stable ID
  "type": "follow_up",
  "from": {                         // who sent this
    "kind": "client",               // client | agent | system
    "id": "user_abc"
  },
  "inReplyTo": "msg_abc123",        // optional threading
  "threadId": "thread_001",         // optional thread grouping
  "delivery": "immediate",          // optional mechanical hint
  "source": "client_injection",     // why this arrived
  "input": [OutputPart[]]
}
```

Every message gets a stable `id`. This is the single most important primitive — it's what makes threading, reply tracking, and non-linear context all possible. Without IDs on every message, none of the other patterns can be built.

The `from` field with a `kind` discriminator accommodates multi-participant sessions without requiring it today. A session that only ever has one client simply has all messages with `from.kind: "client"`.

`threadId` groups related messages into sub-conversations within a session. This is the foundation for threaded patterns. In v1, there's only ever one implicit thread. In future, multiple threads can coexist within a session.

`inReplyTo` enables the graph structure of message relationships rather than forcing a linear sequence.

None of these fields are required in v1 except `id`. But their presence in the schema from day one means future conversational patterns are additive, not requiring a breaking schema change.

The analogy to email is useful here: SMTP defines message IDs, threading via `In-Reply-To`, and participant lists (`From`, `To`, `CC`) as fundamental. Lenny's conversational model is structurally similar even if the context is different. Getting the envelope right is what allows the application layer to build richer patterns on top without needing platform changes.

---

## Human (Apr 1, 2026, 4:40 PM):

Ok yes, this division makes sense now. However, I feel this is a complex topic and we need to get it right. A few things going through my mind:

# Behavior is more semantic than a list of options
Behavior should just inform the receiver about special actions it should take that aren't simply reciving a response to an input request. At the same time, I can imagine the expected behavior being simply implied from the message itself (for instance, maybe the runtime uses LLM calls to analyze the intent in the follow-up message as well as the current agent state to decide whether to interrupt, etc). So I think `behavior`should probably be optional and very generic.

# Agent's interaction setting and follow ups
Clearly this type of interruption/injection shouldn't work with one-shot agents. But it may not always work on multi-turn implementations either. I think whether injection is allowed by a runtime should be a capability flag in itself. Not sure how granular this config/attribute needs to be.

# Interruption cascade
Should interrupts cascade? I'm leaning towards deferring to the runtime to decide what to do with its running children, but I'm not sure if I'm missing anything. Maybe actual interrupts in the lifecycle channel should propagate to children (and maybe that's already the case)

# Should input requests have unique ids?
And direct responses to specific requests be accompanied by the id? (optionally, of course)

# Future conversational patterns
What other conversational patterns are we not taking into account that maybe we should? In the future, if agents behave like humans, conversations won't be always linear, have threads, multi-agent/multi-user, etc. This is not a fully solved problem in a satnardized way, or any way for that matter (I think), and I don't expect we will solve it here and now. However, Lenny should be designed, to the best of our abilities, to support future use cases without the need for major refactors. Any ideas here would be welcome

---

## Claude:

Let me research how Claude Code and other platforms handle multi-agent team communication before proposing anything.Good research to do first. The picture is now clear. Let me synthesize it before making any recommendations.

---

## What Claude Code Actually Does

Claude Code draws a sharp distinction: subagents run within a single session and can only report back to the main agent — they cannot message each other or share discoveries mid-task. Agent Teams remove that bottleneck — teammates communicate directly with each other through a messaging system and share a task list.

The implementation is revealing. Claude Code writes JSON to inbox files on disk — one file per agent at `~/.claude/<teamName>/inboxes/<agentName>.json`. The leader polls that file on an interval to check for new messages. This works because all agents are on the same machine. For Lenny, where agents are separate pods, you need the gateway to play the role those disk files play.

The governance question you raised is real. Claude Code routes communication primarily through the leader — teammates can message each other, but the main pattern is teammate → leader → teammate. Full peer-to-peer creates audit gaps. The third-party OpenCode implementation went fully peer-to-peer and found it useful, but it's explicitly ungoverned.

---

## The Right Framing for Lenny

The current design has three separate concepts that are actually the same thing viewed from different angles:

- `lenny/send_to_child` — parent sending to child
- `lenny/notify_sibling` — sibling sending to sibling  
- Client injection via `POST /v1/sessions/{id}/inject` — external sender to any session

All three are instances of the same operation: **sending a message to a specific session's inbox**. The sender identity, authorization rules, and delivery mechanism differ, but the core operation is identical. Building three separate primitives for this creates fragmentation and makes future patterns harder to add.

The right foundation is a single gateway-mediated **session messaging channel**: every session has an inbox managed by the gateway. Any authorized sender — parent, sibling, client, external system — sends a message to that inbox by session ID. The gateway routes it, enforces policy, records it in the audit log, and delivers it as a `follow_up` stdin message to the recipient session.

---

## Solving the ID Discovery Problem

Your concern about siblings not knowing each other's IDs is correct and not currently solved. The fix is a new platform MCP tool:

```
lenny/get_task_tree() → TaskTreeNode[]
```

Returns the calling session's view of its own task tree: its parent session ID, its own session ID, and all sibling sessions that share the same parent — along with their current state (running, completed, suspended) and their declared `agentInterface` (so the runtime knows what they're doing). This is scoped to what the calling session is authorized to see under its delegation policy.

This gives every session in a tree the information needed to address messages to specific peers without requiring the parent to relay IDs explicitly.

---

## The Unified Primitive

Replace `lenny/send_to_child`, `lenny/notify_sibling`, and the injection endpoint with one coherent model:

**Platform MCP tool for runtime-to-runtime:**
```
lenny/send_message(
  to: string,           // target session ID, from lenny/get_task_tree()
  message: MessageEnvelope
) → void
```

**REST API for client-to-runtime:**
```
POST /v1/sessions/{id}/messages
{
  message: MessageEnvelope
}
```

The `MessageEnvelope` is the rich format we already designed:
```json
{
  "id": "msg_xyz789",
  "from": { "kind": "client | agent | system", "id": "..." },
  "inReplyTo": "msg_abc123",
  "threadId": "thread_001",
  "delivery": "immediate",
  "input": [OutputPart[]]
}
```

The gateway validates:
- The sender is authorized to message the target (same task tree, within delegation policy)
- The target session has `capabilities.injection.supported: true`
- The delivery mode is one the target declared it supports

Delivery is via `follow_up` on the recipient's stdin. The message envelope is preserved in full — the recipient sees who sent it, whether it's a reply to something specific, and which thread it belongs to.

---

## How Governance Works

Since all messages go through the gateway, governance comes for free:
- Every message is audit-logged with sender, recipient, timestamp, and message ID
- Delivery policy is enforced at the gateway — a sibling can't message a session that declared `injection.supported: false`
- Budget is tracked — messages that trigger LLM reasoning count against the recipient session's token budget
- The task tree constraint means sessions outside the tree cannot message each other — no cross-tenant injection

This directly addresses your concern. The Claude Code disk-file approach has no governance at all. Lenny's gateway-mediated approach has full governance because the gateway is the only path.

---

## What This Means for the Existing Design

Three changes:

**Remove `lenny/send_to_child` and `lenny/notify_sibling` stubs** — replace with `lenny/send_message` and `lenny/get_task_tree` as v1 platform MCP tools.

**Unify `POST /v1/sessions/{id}/inject` with `POST /v1/sessions/{id}/messages`** — same endpoint, same envelope, same delivery semantics. The distinction between "injection" and "messaging" is just the sender type, which is captured in `from.kind`.

**Add `lenny/get_task_tree`** — this is new. It returns the session's position in the task tree and enough information to address messages to peers.

---

## Forward Compatibility

This foundation accommodates all the future conversational patterns without further structural change:

**Broadcast**: `lenny/send_message` called with a special `to: "broadcast"` value that the gateway expands to all running sessions in the same subtree. Additive.

**Threaded messages**: Already in the envelope via `threadId` and `inReplyTo`. The gateway can optionally expose `GET /v1/sessions/{id}/messages?threadId=xxx` to retrieve a thread's history.

**Multiple participants**: `from.kind: "user"` with a distinct `from.id` per user. The gateway validates user identity the same way it validates session identity. The session's message log naturally captures multi-participant history.

**Non-linear context**: Because every message has a stable `id` and the full envelope is stored in the session's message log, a runtime can retrieve specific prior messages by ID rather than only reading the linear stream. This is a query API addition, not a protocol change.

**External agent messaging (A2A)**: An external A2A agent sending a message to a Lenny session maps to `from.kind: "external"` with the A2A agent's card URL as `from.id`. The `ExternalProtocolAdapter` translates inbound A2A messages into this envelope format. The recipient session doesn't need to know it came from A2A.

---

## Human (Apr 1, 2026, 6:00 PM):

Okay. This, this looks better to me now. On the topic of sibling communication, I'm not opposed to the idea but I've When it would be good to revisit it and The same way we we should think about Supporting all these different conversational modes. That we just talked about. I think we should Try to future proof this a little bit. When it comes to child communication. The the main issue that I see with your initial proposal there is that Now child Asians need to know about each other and they need to know their IV Which is not impossible, but I don't think the mechanism exists right now. So that's one technicality. But I think more in general, like, I'd like to understand how for example, Cloud Code implements teams of agents. If they communicate directly with each other or if they go through the parent, I'm just a little concerned about the the governance around not being able to control the communication. Although, I guess, in this case, it goes through the gateway anyway. So maybe That shouldn't be a concern. In any case, I think the the right now, parent to child and sibling communication is a little half baked, and I would like to explore What is it that we need to do to make this solid Future proof.

---

## Claude:

Let me work through both carefully, being precise about each call and what triggers it.

---

## Scenario 1: External Client, Multi-Turn + Async Injection

The session is a research agent working on a long task. The client starts it, the agent asks a clarifying question mid-execution, and while that's happening the client also pushes an unsolicited update.

```
# 1. Create session
POST /v1/sessions
{
  "runtime": "research-pipeline",
  "retryPolicy": { "mode": "auto_then_client", "maxRetries": 2 }
}
→ { "sessionId": "sess_abc", "uploadToken": "tok_xyz" }

# 2. Upload workspace files (optional)
POST /v1/sessions/sess_abc/workspace
Authorization: Bearer tok_xyz
[multipart files]

# 3. Finalize workspace
POST /v1/sessions/sess_abc/workspace/finalize

# 4. Attach — opens SSE stream, client listens from here
GET /v1/sessions/sess_abc/stream
→ SSE stream begins

# 5. Send initial prompt
POST /v1/sessions/sess_abc/messages
{
  "message": {
    "id": "msg_001",
    "from": { "kind": "client", "id": "user_123" },
    "delivery": "immediate",
    "input": [
      { "type": "text", "mimeType": "text/plain",
        "inline": "Research the impact of rate changes on mortgage markets" }
    ]
  }
}

# --- agent begins working, client receives SSE events ---
# event: { "type": "agent_output", "parts": [...] }   streaming output
# event: { "type": "status_change", "state": "input_required" }
# event: { "type": "input_required",
#           "requestId": "req_001",
#           "parts": [{ "type": "text", "inline": "Which time period should I focus on?" }] }

# 6. Client responds to the input_required request
POST /v1/sessions/sess_abc/messages
{
  "message": {
    "id": "msg_002",
    "from": { "kind": "client", "id": "user_123" },
    "inReplyTo": "req_001",
    "delivery": "immediate",
    "input": [
      { "type": "text", "mimeType": "text/plain",
        "inline": "Focus on 2020-2024" }
    ]
  }
}

# --- agent resumes, client continues receiving SSE events ---

# 7. Separately, client wants to push new information
#    (async, not a reply to anything — e.g. user found a relevant article)
POST /v1/sessions/sess_abc/messages
{
  "message": {
    "id": "msg_003",
    "from": { "kind": "client", "id": "user_123" },
    "delivery": "immediate",
    "input": [
      { "type": "text", "mimeType": "text/plain",
        "inline": "Also incorporate the Fed's March 2024 statement" },
      { "type": "data", "mimeType": "application/json",
        "inline": { "url": "https://fed.gov/statement/march2024" } }
    ]
  }
}

# --- agent incorporates injection at next natural pause point ---

# 8. Eventually session completes
# event: { "type": "session_complete", "result": { "parts": [...] } }

# 9. Client retrieves artifacts
GET /v1/sessions/sess_abc/artifacts
GET /v1/sessions/sess_abc/artifacts/artifact_001
```

The key thing to notice: calls 5, 6, and 7 all go to the same endpoint — `POST /v1/sessions/{id}/messages`. The difference between a reply to an input request (msg_002) and an unsolicited injection (msg_003) is entirely in the presence or absence of `inReplyTo`. The gateway routes both as `follow_up` messages to the runtime's stdin, with the envelope preserved so the runtime can distinguish them.

The SSE stream in step 4 stays open throughout. The client listens on it continuously while posting to the messages endpoint whenever it has something to send. These are two independent HTTP connections — the SSE stream is read-only server-to-client, and the messages endpoint is write-only client-to-server.

---

## Scenario 2: Agent to Agent Inside Lenny

A parent orchestrator spawns two child agents — a researcher and a summarizer — and the researcher needs to inform the summarizer of an intermediate finding mid-execution, without going back through the parent.

**Parent agent's outbound MCP calls (via platform MCP server on stdin/stdout):**

```jsonl
# Parent is running. It delegates to the researcher.
→ { "type": "tool_call", "tool": "lenny/delegate_task",
    "args": {
      "target": "research-pipeline",
      "task": {
        "input": [{ "type": "text", "inline": "Research rate changes 2020-2024" }]
      }
    }}
← { "type": "tool_result", "result": { "taskHandle": "task_researcher" }}

# Parent delegates to the summarizer.
→ { "type": "tool_call", "tool": "lenny/delegate_task",
    "args": {
      "target": "summarizer-pipeline",
      "task": {
        "input": [{ "type": "text", "inline": "Await findings and summarize" }]
      }
    }}
← { "type": "tool_result", "result": { "taskHandle": "task_summarizer" }}

# Parent queries the task tree to get session IDs it can address
→ { "type": "tool_call", "tool": "lenny/get_task_tree", "args": {} }
← { "type": "tool_result", "result": {
    "self": "sess_parent",
    "children": [
      { "sessionId": "sess_researcher", "taskHandle": "task_researcher",
        "state": "running", "runtime": "research-pipeline" },
      { "sessionId": "sess_summarizer", "taskHandle": "task_summarizer",
        "state": "running", "runtime": "summarizer-pipeline" }
    ]
  }}

# Parent sends the summarizer the researcher's session ID
# so the researcher can address it directly when ready
→ { "type": "tool_call", "tool": "lenny/send_message",
    "args": {
      "to": "sess_summarizer",
      "message": {
        "id": "msg_p01",
        "from": { "kind": "agent", "id": "sess_parent" },
        "input": [{ "type": "data", "mimeType": "application/json",
                    "inline": { "researcherSessionId": "sess_researcher" } }]
      }
    }}
← { "type": "tool_result", "result": { "delivered": true }}

# Parent awaits both children
→ { "type": "tool_call", "tool": "lenny/await_children",
    "args": { "taskHandles": ["task_researcher", "task_summarizer"], "mode": "all" }}
# ... blocks until both complete
```

**Researcher agent — mid-execution, sends a finding to the summarizer:**

```jsonl
# Researcher has a finding and wants to share it with the summarizer
# It knows the summarizer's session ID because the parent sent it
# (stored in the researcher's working context from its own task input,
#  or it can call lenny/get_task_tree to discover siblings directly)

→ { "type": "tool_call", "tool": "lenny/get_task_tree", "args": {} }
← { "type": "tool_result", "result": {
    "self": "sess_researcher",
    "parent": "sess_parent",
    "siblings": [
      { "sessionId": "sess_summarizer", "state": "running",
        "runtime": "summarizer-pipeline" }
    ]
  }}

# Researcher sends finding directly to summarizer
→ { "type": "tool_call", "tool": "lenny/send_message",
    "args": {
      "to": "sess_summarizer",
      "message": {
        "id": "msg_r01",
        "from": { "kind": "agent", "id": "sess_researcher" },
        "threadId": "thread_research_findings",
        "input": [
          { "type": "text", "mimeType": "text/plain",
            "inline": "Key finding: Fed held rates 11 consecutive meetings 2023-2024" },
          { "type": "data", "mimeType": "application/json",
            "inline": { "confidence": 0.97, "source": "Fed minutes archive" } }
        ]
      }
    }}
← { "type": "tool_result", "result": { "delivered": true }}

# Researcher continues working and eventually completes
→ { "type": "response", "parts": [{ "type": "text", "inline": "Research complete..." }] }
```

**Summarizer agent — receives the sibling message as a follow_up on stdin:**

```jsonl
# Summarizer was working. Gateway delivers the message at next stdin read.
← { "type": "follow_up",
    "id": "msg_r01",
    "from": { "kind": "agent", "id": "sess_researcher" },
    "threadId": "thread_research_findings",
    "input": [
      { "type": "text", "inline": "Key finding: Fed held rates 11 consecutive meetings 2023-2024" },
      { "type": "data", "mimeType": "application/json",
        "inline": { "confidence": 0.97, "source": "Fed minutes archive" } }
    ]}

# Summarizer incorporates finding and continues
# Eventually responds
→ { "type": "response", "parts": [{ "type": "text", "inline": "Summary incorporating finding..." }] }
```

---

## What to Verify

Two things worth confirming before signing off:

**Discovery path for siblings.** The researcher above calls `lenny/get_task_tree` to find the summarizer's session ID. This works because they share a parent and the gateway includes siblings in the tree response. The alternative — the parent explicitly forwarding session IDs as task input — also works and may be simpler for straightforward cases. Both should be supported; `lenny/get_task_tree` is the self-service path.

**Delivery timing for the sibling message.** The `follow_up` arrives at the summarizer "at next stdin read" — meaning the summarizer incorporates it at its next natural pause between processing steps, not preemptively. This is the cooperative delivery model we settled on. If the parent needed guaranteed immediate interruption of the summarizer, it would call `interrupt_request` on the lifecycle channel first, then send the message. For a mid-task informational finding like this one, cooperative delivery is correct — you wouldn't want to interrupt the summarizer mid-sentence.

---

## Human (Apr 1, 2026, 7:19 PM):

Thanks. I think this looks good, but let's double check a couple of things before signing off:

* In the case an external client interacting with an agent in multi-turn mode plus injected (async) messages, show me an example of all the calls the client would make
* Same but agent to agent inside Lenny

---

## Claude:

Good questions both. Let me address the ID question first since it affects the conversation examples.

---

## Session ID vs Task ID

Looking at the actual design, both exist and serve different purposes.

Each `delegate_task` call creates two distinct things: a **task** (the unit of work, identified by `child_id` / `taskId`) and a **session** (the execution context, identified by `session_id`). The task tree nodes explicitly track both — Section 8.10: "each node tracks: `session_id`, generation, pod, state, lease, budget consumed, failure history."

The parent's existing tools (`await_child`, `cancel_child`) use `child_id` — the task identifier. The session identifier is what the gateway uses internally to track the pod and execution context.

For messaging purposes, **task ID is the right identifier** for two reasons:

First, it's what the parent already has from `delegate_task` — no extra lookup needed. Second, and more importantly: Section 7.3 states "each recovery creates a new generation of the same logical session — the client always sees one session_id." This suggests `session_id` is a stable logical identifier across pod restarts, but the gateway resolves it to the current generation internally. For messaging, using `task_id` ties the message to the work unit, which is the right semantic. Whether the task is on its first or third pod generation, the message reaches the right inbox.

So `lenny/get_task_tree` should return `taskId` as the primary identifier for messaging, with `sessionId` available for cases where the runtime needs it (audit queries, artifact retrieval, etc.). `lenny/send_message` takes a `taskId`, not a `sessionId`.

---

## Synchronous Parent-Child Conversation

The current design already has the mechanism for this via the virtual child interface (Section 8.2). When a parent delegates, it doesn't just get a `child_id` back — it gets a virtual MCP child interface through which the child can surface events upward, including `input_required`. The parent doesn't have to block blindly on `await_child` — it can receive events from the child while waiting.

The key insight: `await_child` in multi-turn mode needs to **yield** when `input_required` arrives, not block until final completion. The parent's loop becomes:

```jsonl
# 1. Parent delegates to child
→ { "type": "tool_call", "tool": "lenny/delegate_task",
    "args": {
      "target": "research-pipeline",
      "task": { "input": [{ "type": "text",
                "inline": "Research rate changes 2020-2024" }] }
    }}
← { "type": "tool_result", "result": { "taskId": "task_child_001" }}

# 2. Parent awaits — this yields on input_required, not just completion
→ { "type": "tool_call", "tool": "lenny/await_child",
    "args": { "taskId": "task_child_001" }}

# Gateway delivers child event while parent is awaiting
← { "type": "tool_result",
    "result": {
      "taskId": "task_child_001",
      "status": "input_required",
      "requestId": "req_child_001",
      "parts": [{ "type": "text",
                  "inline": "Should I focus on residential or commercial mortgages?" }]
    }}

# 3. Parent decides and responds
#    (may consult its own LLM, look at its context, or ask the human client)
→ { "type": "tool_call", "tool": "lenny/send_message",
    "args": {
      "to": "task_child_001",
      "message": {
        "id": "msg_p_001",
        "from": { "kind": "agent", "id": "task_root_001" },
        "inReplyTo": "req_child_001",
        "delivery": "immediate",
        "input": [{ "type": "text",
                    "inline": "Focus on residential" }]
      }
    }}
← { "type": "tool_result", "result": { "delivered": true }}

# 4. Parent awaits again — child resumes, may ask another question
→ { "type": "tool_call", "tool": "lenny/await_child",
    "args": { "taskId": "task_child_001" }}

# This time child completes
← { "type": "tool_result",
    "result": {
      "taskId": "task_child_001",
      "status": "completed",
      "parts": [{ "type": "text",
                  "inline": "Research complete: residential mortgages..." }],
      "usage": { ... }
    }}
```

---

## What Needs to Change in the Design

Two things become clear from working through these examples:

**`await_child` needs to return on `input_required`, not just terminal states.** Currently it's described as returning only on `completed`, `failed`, or `cancelled`. For synchronous parent-child conversation to work, it must also unblock when the child enters `input_required`, delivering the request to the parent so it can respond. This is consistent with the `input_required` state we already defined — the parent's await receives it exactly like a partial result.

**`lenny/get_task_tree` should return `taskId` as the messaging address.** The full node shape:

```json
{
  "self": { "taskId": "task_root_001", "sessionId": "sess_abc" },
  "parent": null,
  "children": [
    {
      "taskId": "task_child_001",
      "sessionId": "sess_xyz",
      "state": "running",
      "runtime": "research-pipeline"
    }
  ],
  "siblings": []
}
```

Both IDs are available for the runtime to use — `taskId` for messaging and lifecycle operations, `sessionId` for artifact and audit queries. They are not redundant; they serve different purposes. Task ID is the work unit identity; session ID is the execution context identity.

---

## Human (Apr 1, 2026, 7:33 PM):

What about the case where parent and child have a syncrhonous conversation (request for input + follow up)?

Also, should we use task ids or session ids when messaging between agents? Are those redundant? Please check the entire conversation and technical design doc to check whether a new session id is created on every branch of the execution tree, or if it's just one session id for all (or if task id fits that role instead, or something else)

---

## Claude:

Good research. The picture is clear. Let me lay out the comparison properly.

---

## How MCP Handles Multi-Turn

MCP has two primitives for multi-turn interaction during a running tool call:

**Elicitation** — the server pauses execution and requests input from the **user** (human) via the client. The client presents the request to the user, collects a response, and returns it to the server. Elicitation allows servers to request additional information from users when they want to get more information or ask for confirmation of an action, using the `elicitation/request` method. This is explicitly human-facing — it's not designed for agent-to-agent communication.

**Sampling** — the server requests an LLM completion from the **host application's** LLM. Sampling allows servers to request completions from the AI model during execution. Instead of the model unilaterally deciding everything, a server can ask the model to reason about intermediate state, validate assumptions, or generate content as part of a multi-step process. This is about a server borrowing the host's inference capability, not talking to another agent.

Both primitives route through the **host/client** layer — there is no native MCP mechanism for one server to initiate a conversation with another server. Multi-turn interactive input mid-execution is achieved via elicitation (requesting user input) and sampling (requesting LLM completions from the client/host application). Agent-to-agent multi-turn in MCP must be orchestrated at the host layer — the host notices an elicitation, routes it to another agent, and returns the answer. This is custom orchestration logic, not a protocol primitive.

The MCP spec is actively working on evolving this — currently when a server needs more information to complete a tool call, it suspends execution and waits for a client response, requiring it to track all outstanding requests. The roadmap looks at designing server requests and responses to work similarly to chat APIs, where the server returns the elicitation request and the client returns both the request and response together. This is moving toward stateless elicitation — the server doesn't hold state between the pause and the resume, making it more scalable but making the bidirectional session model even less native to MCP.

---

## How Lenny Compares

The structural difference is one of perspective. In MCP, the host is the orchestrator and all multi-turn happens through it. In Lenny, the gateway plays that role. The fundamental topology is the same — all communication is mediated. But Lenny makes agent-to-agent multi-turn a first-class platform primitive rather than something you build on top.

Mapping the concepts:

| Concept | MCP | Lenny |
|---|---|---|
| Agent asks human for input | Elicitation → user via client | Elicitation chain → client |
| Agent asks another agent for input | No native primitive — custom host orchestration | `input_required` → parent via gateway |
| Agent borrows host's LLM | Sampling | No equivalent — each runtime has its own LLM |
| Parent sends info to running child | No native primitive | `lenny/send_message` → `follow_up` on stdin |
| Sibling-to-sibling communication | No native primitive | `lenny/send_message` via gateway using `taskId` from `lenny/get_task_tree` |
| Task identity and tracking | Per-connection, no native task tree | Explicit task tree in SessionStore |

---

## Is There a Conflict?

No, and this is worth being explicit about. Lenny's multi-turn mechanism operates at a different layer from MCP. The runtime uses MCP to call the adapter's **platform MCP server** — that's where `lenny/send_message` and `lenny/await_child` live. The delivery of a `follow_up` to the receiving runtime happens via the **binary protocol** on stdin. These are separate layers that don't interfere.

When A2A is added later, the comparison shifts. A2A natively models multi-turn agent-to-agent conversation via its `input-required` task state — this maps directly to Lenny's `input_required` state and `follow_up` mechanism. Lenny's design is actually closer in spirit to A2A than to MCP for agent-to-agent conversation. MCP is fundamentally a tool-invocation protocol being stretched into multi-turn; A2A was designed for multi-turn agent coordination from the start. Lenny's `input_required` / `follow_up` / `send_message` pattern is a platform-native version of the same model A2A provides at the protocol level.

---

## One Gap Worth Noting

MCP's sampling primitive — asking the host's LLM to do work — has no Lenny equivalent. In Lenny, each runtime brings its own LLM. A runtime that wants another agent to do LLM reasoning on something delegates a task to that agent via `lenny/delegate_task`. This is slightly heavier than MCP's sampling (it creates a new session) but has full governance, budget tracking, and audit logging. Whether this is a gap depends on the use case — for lightweight "can your LLM look at this" calls, the overhead may be undesirable, but for production governed systems it's the right trade-off. If this pattern becomes common, a lightweight `lenny/sample` tool that creates a minimal session without workspace setup could be added later without breaking changes.

---

## Human (Apr 1, 2026, 7:39 PM):

Thanks. This seems to work now but before we declare victory: how does this flow compare to what others are doing for communicating multi-turn conversations between agents via MCP?

---

## Claude:

This scenario has a parent spawning two parallel children — a researcher and a writer. The researcher completes independently and sends a finding to the writer asynchronously (sibling message). The writer then hits `input_required`, asking the parent for guidance before continuing. The full stack is shown at each step.

The columns are: **Parent Runtime** ↔ **Parent Adapter** ↔ **Gateway** ↔ **Child Adapter** ↔ **Child Runtime**. Arrows show direction; `→` is a request or send, `←` is a response or delivery. gRPC is used for adapter↔gateway; stdin/stdout for adapter↔runtime.

---

## Phase 1 — Parent Delegates to Both Children

```
Parent Runtime                Parent Adapter              Gateway
     |                             |                          |
     |-- stdout tool_call -------->|                          |
     |  lenny/delegate_task        |                          |
     |  target: "researcher"       |-- gRPC DelegateTask ---->|
     |  task: {input: [...]}       |                          |-- validates lease
     |                             |                          |-- claims warm pod
     |                             |                          |-- creates sess_researcher
     |                             |                          |   task_id: task_001
     |                             |                          |
     |                             |           Gateway        |  Researcher Adapter
     |                             |              |           |       |
     |                             |              |-- gRPC PrepareWorkspace -------->|
     |                             |              |-- gRPC FinalizeWorkspace ------->|
     |                             |              |-- gRPC AssignCredentials(lease)->|
     |                             |              |-- gRPC StartSession(cwd) ------->|
     |                             |              |                                  |
     |                             |              |            Researcher Adapter    | Researcher Runtime
     |                             |              |                   |              |      |
     |                             |              |                   |-- stdin prompt ----->|
     |                             |              |                   |  {type:"prompt",     |
     |                             |              |                   |   input:[...]}       |
     |                             |              |                   |              |      |
     |                             |<- gRPC DelegateTaskResponse -----|              |      |
     |                             |  taskId: task_001                |              |      |
     |<- stdout tool_result -------|                                  |              |      |
     |  {taskId: "task_001"}       |                                  |              |      |
     |                             |                                  |              |      |
     |                             |                                  |              |      |
     |-- stdout tool_call -------->|                                  |              |      |
     |  lenny/delegate_task        |                                  |              |      |
     |  target: "writer"           |-- gRPC DelegateTask ----------->|              |      |
     |  task: {input: [...]}       |                                  |              |      |
     |                             |           [same startup flow for writer...]      |      |
     |                             |                       sess_writer, task_002      |      |
     |<- stdout tool_result -------|                                  |              |      |
     |  {taskId: "task_002"}       |                                  |              |      |
```

---

## Phase 2 — Parent Awaits; Children Begin Work

```
Parent Runtime                Parent Adapter              Gateway
     |                             |                          |
     |-- stdout tool_call -------->|                          |
     |  lenny/await_children       |-- gRPC AwaitChildren --->|
     |  taskIds:[task_001,task_002]|  mode: "settled"         |-- registers wait
     |  mode: "settled"            |                          |   blocks until both
     |                             |                          |   children settle
     |   [parent is now blocked]   |   [gRPC call pending]    |
```

Meanwhile both children are running independently on their adapters.

---

## Phase 3 — Researcher Discovers Sibling, Sends Async Finding

The researcher finishes a significant analysis and wants to share it with the writer immediately, without waiting to complete.

```
Researcher Runtime        Researcher Adapter              Gateway
     |                         |                              |
     |-- stdout tool_call ----->|                             |
     |  lenny/get_task_tree     |-- gRPC GetTaskTree -------->|
     |                          |                             |-- looks up task tree
     |                          |<- gRPC TaskTreeResponse ----|
     |                          |  self: {taskId: task_001}   |
     |                          |  parent: {taskId: task_root}|
     |                          |  siblings: [               |
     |                          |    {taskId: task_002,       |
     |                          |     state: running,         |
     |                          |     runtime: "writer"}]     |
     |<- stdout tool_result ----|                             |
     |  {self, parent, siblings}|                             |
     |                          |                             |
     |-- stdout tool_call ----->|                             |
     |  lenny/send_message      |-- gRPC SendMessage -------->|
     |  to: "task_002"          |  from: task_001             |-- validates:
     |  message: {              |  to: task_002               |   same task tree ✓
     |    id: "msg_s01"         |  message: {...}             |   writer injection
     |    from: {kind:"agent"   |                             |   supported ✓
     |           id:"task_001"} |                             |
     |    input: [{type:"text"  |                             |
     |     inline:"Key finding: |                             |
     |     Fed held rates..."}] |                             |
     |  }                       |                             |
     |                          |                             |
```

Gateway now delivers the message to the writer asynchronously. The writer is between tool call steps:

```
    Gateway               Writer Adapter            Writer Runtime
       |                       |                          |
       |-- gRPC InboundMessage->|                         |
       |   message: {           |-- [checks stdin loop]   |
       |    id: msg_s01         |   writer just returned  |
       |    from: task_001      |   from a tool call,     |
       |    input: [finding]    |   stdin is free         |
       |   }                    |                         |
       |                        |-- stdin follow_up ------>|
       |                        |  {type: "follow_up"      |
       |                        |   id: "msg_s01"          |
       |                        |   from: {kind:"agent"    |
       |                        |          id:"task_001"}  |
       |                        |   input: [finding]}      |
       |                        |                          |
       |<- gRPC InboundMessageAck|                         |
       |   delivered: true      |                          |
```

Back on the researcher side, the SendMessage response arrives:

```
    Gateway           Researcher Adapter        Researcher Runtime
       |                    |                          |
       |<- gRPC SendMsg ----|                          |
       |   delivered: true  |                          |
       |                    |<- gRPC SendMessageResp --|
       |                    |   delivered: true        |
       |                    |-- stdout tool_result ---->|
       |                    |  {delivered: true}        |
       |                    |                          |
       [researcher continues working and eventually completes normally]
       |                    |-- stdout response ------->|
       |                    |  {type:"response"         |
       |                    |   parts:[...findings...]} |
       |                    |                          |
       |<- gRPC TaskComplete|                          |
       |   taskId: task_001 |                          |
       |   parts: [...]     |                          |
       |                    |                          |
   [gateway updates task_001 → completed, stores TaskResult]
   [does NOT unblock parent's await yet — task_002 still running]
```

---

## Phase 4 — Writer Hits `input_required`

The writer has incorporated the researcher's finding but needs guidance before proceeding.

```
Writer Runtime           Writer Adapter              Gateway
     |                        |                          |
     |-- stdout input_req ---->|                         |
     |  {type:"input_required" |                         |
     |   requestId:"req_w01"   |-- gRPC ReportInput ---->|
     |   parts:[{type:"text"   |   Required(             |
     |    inline:"Technical or |   sessionId:sess_writer |
     |    executive audience?"}|   taskId: task_002      |-- updates task_002
     |  }                      |   requestId: req_w01    |   state → input_required
     |                         |   parts:[...])          |
     |                         |                         |-- task_002 state changed
     |                         |                         |   notify parent's pending
     |                         |                         |   AwaitChildren call
     |                         |                         |
```

The gateway unblocks the parent's `await_children` with a partial result:

```
    Gateway               Parent Adapter            Parent Runtime
       |                       |                          |
       |-- gRPC AwaitEvent ---->|                         |
       |   (unblocks pending    |                         |
       |    AwaitChildren call) |                         |
       |   {taskId: task_002    |                         |
       |    status:input_req    |                         |
       |    requestId:req_w01   |-- stdout tool_result --->|
       |    parts:[question]}   |  {taskId:"task_002"      |
       |                        |   status:"input_required"|
       |                        |   requestId:"req_w01"    |
       |                        |   parts:[{type:"text"    |
       |                        |    inline:"Technical or  |
       |                        |    executive audience?"}]}|
```

---

## Phase 5 — Parent Responds to Writer

The parent consults its own LLM and decides on an answer.

```
Parent Runtime                Parent Adapter              Gateway
     |                             |                          |
     |-- stdout tool_call -------->|                          |
     |  lenny/send_message         |-- gRPC SendMessage ----->|
     |  to: "task_002"             |  from: task_root         |-- validates:
     |  message: {                 |  to: task_002            |   parent authorized
     |    id: "msg_p01"            |  message: {              |   to msg child ✓
     |    from:{kind:"agent"       |    inReplyTo:req_w01     |   req_w01 exists
     |         id:"task_root"}     |    delivery:"immediate"  |   on task_002 ✓
     |    inReplyTo: "req_w01"     |    input:[...answer...]} |
     |    delivery: "immediate"    |                          |
     |    input:[{type:"text"      |                          |
     |     inline:"Executive       |                          |
     |     audience"}]             |                          |
     |  }                          |                          |
```

Because `delivery: "immediate"`, the gateway sends an interrupt signal first:

```
    Gateway               Writer Adapter            Writer Runtime
       |                       |                          |
       |-- gRPC InterruptReq -->|                         |
       |                        |-- lifecycle channel ---->|
       |                        |  {type:"interrupt_request"}
       |                        |                          |
       |                        |<- lifecycle channel ----|
       |                        |  {type:"interrupt_acknowledged"}
       |                        |                          |
       |<- gRPC InterruptAck ---|                         |
       |                        |                          |
       |-- gRPC DeliverMessage ->|                        |
       |   message: {           |                          |
       |    id:msg_p01          |-- stdin follow_up ------->|
       |    inReplyTo:req_w01   |  {type:"follow_up"        |
       |    delivery:immediate  |   id:"msg_p01"            |
       |    input:[answer]}     |   from:{kind:"agent"      |
       |                        |         id:"task_root"}   |
       |                        |   inReplyTo:"req_w01"     |
       |                        |   delivery:"immediate"    |
       |                        |   input:[{inline:         |
       |                        |    "Executive audience"}]}|
       |<- gRPC DeliverMsgAck --|                          |
```

Back on the parent side:

```
    Gateway               Parent Adapter            Parent Runtime
       |                       |                          |
       |<- gRPC SendMsgResp ---|                          |
       |   delivered: true     |-- stdout tool_result ---->|
       |                        |  {delivered: true}        |
```

---

## Phase 6 — Parent Re-Awaits; Writer and Task Settle

```
Parent Runtime                Parent Adapter              Gateway
     |                             |                          |
     |-- stdout tool_call -------->|                          |
     |  lenny/await_children       |-- gRPC AwaitChildren --->|
     |  taskIds:[task_001,task_002]|  mode: "settled"         |-- task_001 already
     |  mode: "settled"            |                          |   completed, noted
     |                             |                          |-- waiting on task_002
```

Writer resumes and completes:

```
Writer Runtime           Writer Adapter              Gateway
     |                        |                          |
     |  [processes answer,    |                          |
     |   writes summary]      |                          |
     |                        |                          |
     |-- stdout response ----->|                         |
     |  {type:"response"       |-- gRPC TaskComplete --->|
     |   parts:[{type:"text"   |   taskId: task_002      |-- updates task_002
     |    inline:"Executive    |   parts:[...]           |   → completed
     |    summary..."}]}       |                         |-- both children settled
     |                         |                         |-- unblocks parent await
```

Gateway delivers final results to parent:

```
    Gateway               Parent Adapter            Parent Runtime
       |                       |                          |
       |-- gRPC AwaitResult --->|                         |
       |  [{taskId:task_001     |-- stdout tool_result ---->|
       |    status:completed    |  [{taskId:"task_001"      |
       |    parts:[research]},  |    status:"completed"     |
       |   {taskId:task_002     |    parts:[...]},          |
       |    status:completed    |   {taskId:"task_002"      |
       |    parts:[summary]}]   |    status:"completed"     |
       |                        |    parts:[...]}]          |
```

---

## What This Reveals About the Design

A few things become concrete from tracing this in full:

**The gateway is the only choreographer.** No adapter ever talks directly to another adapter. The researcher's `SendMessage` call goes researcher adapter → gateway → writer adapter. The gateway validates authorization, checks capability flags, and handles delivery timing — all in one place.

**`input_required` and `await_children` are tightly coupled.** The gateway must unblock the parent's pending `AwaitChildren` gRPC call when any child enters `input_required`. This means `AwaitChildren` in the gRPC contract is a streaming response, not a simple blocking call — it can yield partial results multiple times before the final settled result.

**`delivery: "immediate"` adds a round trip.** The interrupt signal on the lifecycle channel and its acknowledgment happen before message delivery. This is cooperative — the writer finishes its current atomic step before receiving the message. If the writer's current step is long, the parent's `send_message` call blocks until interrupt is acknowledged. This is the expected behavior but should be documented in the spec: `delivery: "immediate"` does not mean instantaneous; it means "at the next interrupt point, not queued for later."

**Message delivery acknowledgment is separate from processing acknowledgment.** `delivered: true` means the message reached the runtime's stdin buffer — not that the runtime has acted on it. The parent knows the message was delivered but not what the writer did with it. This is intentional and consistent with how email, message queues, and AG-UI all handle this distinction.

---

## Human (Apr 1, 2026, 8:11 PM):

Ok, these changes make sense. Come up with another sample interaction between parent and children but, this time, include acrynchronous communication and also detail not only what the runtimes see/do but also the underlying adapter->gateway->adapter communication

---

## Claude:

Both points are right and the second one especially is a meaningful simplification. Let me work through why and show the revised model.

---

## The Core Insight on Phase 5

When the writer emits `input_required`, it isn't just announcing a state change — it should be doing so by calling `lenny/request_input` on the platform MCP server as a **blocking tool call**. The writer's execution is suspended inside that tool call, waiting for the return value. The gateway holds the pending tool call open until someone responds to `req_w01`.

When the parent calls `lenny/send_message` with `inReplyTo: req_w01`, the gateway recognises that task_002 has an outstanding `lenny/request_input` call with that exact requestId. Instead of delivering to stdin, it **resolves the pending tool call** and returns the parent's message as its result. The writer never reads stdin — it just gets a tool call return value and continues.

The `delivery: "immediate"` field, the lifecycle channel interrupt, and the interrupt acknowledgment round-trip are all irrelevant in this path because the writer is already blocked waiting. No interruption is needed.

---

## Revised Phase 4 and 5

**Phase 4 — Writer calls `lenny/request_input` (replaces the stdout `input_required` message):**

```
Writer Runtime           Writer Adapter              Gateway
     |                        |                          |
     |-- MCP tool_call ------->|                         |
     |  lenny/request_input    |-- gRPC RequestInput --->|
     |  {requestId: "req_w01"  |   taskId: task_002      |-- stores pending request
     |   parts:[{type:"text"   |   requestId: req_w01    |-- task_002 state →
     |    inline:"Technical or |   parts:[question]}     |   input_required
     |    executive audience?"}|                          |
     |  }                      |                          |-- unblocks parent's
     |                         |                          |   AwaitChildren stream
     |  [writer is now blocked  |  [gRPC call pending,    |
     |   inside the tool call]  |   waiting for resolve]  |
```

**Phase 5 — Parent responds; gateway resolves the tool call directly:**

```
Parent Runtime                Parent Adapter              Gateway
     |                             |                          |
     |-- stdout tool_call -------->|                          |
     |  lenny/send_message         |-- gRPC SendMessage ----->|
     |  to: "task_002"             |  from: task_root         |-- sees task_002
     |  message: {                 |  to: task_002            |   has outstanding
     |    id: "msg_p01"            |  message: {              |   req_w01 ✓
     |    from:{kind:"agent"       |    inReplyTo: req_w01    |-- resolves pending
     |         id:"task_root"}     |    input:[answer]}       |   RequestInput gRPC
     |    inReplyTo: "req_w01"     |                          |   with the message
     |    input:[{type:"text"      |                          |   as return value
     |     inline:"Executive       |                          |
     |     audience"}]             |                          |
     |  }                          |                          |
     |                             |                          |
```

Gateway resolves the writer's pending tool call directly — no stdin delivery, no interrupt:

```
    Gateway               Writer Adapter            Writer Runtime
       |                       |                          |
       |-- gRPC ResolveRequest->|                         |
       |   requestId: req_w01   |                         |
       |   message: {           |                         |
       |    id: msg_p01         |                         |
       |    inReplyTo: req_w01  |-- MCP tool_result ------>|
       |    input:[answer]}     |  lenny/request_input     |
       |                        |  returns: {              |
       |                        |   id: "msg_p01"          |
       |                        |   from:{kind:"agent"     |
       |                        |        id:"task_root"}   |
       |                        |   input:[{type:"text"    |
       |                        |    inline:"Executive     |
       |                        |    audience"}]           |
       |                        |  }                       |
       |                        |                          |
       |<- gRPC SendMessageResp-|                          |-- writer is now unblocked
       |   delivered: true      |                          |   and continues normally
```

Back on the parent side:

```
    Gateway               Parent Adapter            Parent Runtime
       |                       |                          |
       |<- gRPC SendMsgResp ---|                          |
       |   delivered: true     |-- stdout tool_result ---->|
       |                        |  {delivered: true}        |
```

---

## When Stdin Message Delivery Applies

The stdin `{type: "message"}` delivery path applies only when there is **no outstanding `request_input` call** matching the `inReplyTo` — meaning the message is unsolicited. The gateway checks:

1. Does `message.inReplyTo` reference an outstanding `request_input` call on the target task? → **Resolve the tool call directly**
2. Is there no such outstanding call? → **Deliver via stdin `{type: "message"}`**

This keeps the MCP tool call model clean for the synchronous case and reserves stdin delivery for genuinely asynchronous injection.

---

## On Naming

Agreed — `message` is better than `follow_up` everywhere. The revised names:

| Layer | Old name | New name |
|---|---|---|
| Stdin binary protocol message type | `follow_up` | `message` |
| gRPC call from gateway to adapter | `DeliverMessage` | `DeliverMessage` (already fine) |
| Platform MCP tool for requesting input | `lenny/request_input` | `lenny/request_input` |
| Platform MCP tool for sending messages | `lenny/send_message` | `lenny/send_message` |

The `input_required` outbound message type on stdout is also replaced — the runtime never emits `input_required` on stdout anymore. It calls `lenny/request_input` on the platform MCP server instead. The `input_required` task state still exists in the gateway's state machine, but it's now set by the gateway when `lenny/request_input` is called, not triggered by a stdout message.

---

## Human (Apr 1, 2026, 8:57 PM):

Would it make sense to change phase 5 like this:
* From the point of view of the writer, asking for input is a blocking tool call. Once the parent has sent a message in response to that specific request, the tool call returns on the writer side. There shouldn't be a need, in this case, to work outside of MCP. In this case, whether deliver is 'immediate' doesn't matter because the writer is already waiting for a response
* Only if the parent sends a message to the writer that can't be mapped to an pending input request is when the message would be delivered to the runtime via stdin

Also, I think we should align names. in gRPC we say DeliverMessage but the adapter stdin protocol says "follow_up". I like generic "message" better

---

## Claude:

Yes, and this is the right instinct. Both fields are infrastructure metadata, not content — the runtime shouldn't need to know or manage them.

---

## `requestId` in `lenny/request_input`

The runtime has no reason to generate this ID. It doesn't use it — it just blocks waiting for the tool call to return. The ID exists so the gateway can correlate an incoming `send_message` with the outstanding pending call, and so the parent can reference it in `inReplyTo`. Both of those are gateway and adapter concerns.

The cleaner contract: the runtime calls `lenny/request_input` with only the content:

```json
{
  "parts": [{ "type": "text", "inline": "Technical or executive audience?" }]
}
```

The gateway generates the `requestId`, stores it against the pending call, and includes it in the event surfaced to the parent via `await_children`. The parent then uses it in `inReplyTo`. The runtime never sees or handles the ID at all — it just gets the answer back as the tool call return value.

---

## `from.id` in `lenny/send_message`

Same principle. The runtime doesn't know its own task ID and shouldn't need to. That's infrastructure context that the adapter already has. The runtime provides the message content and the recipient:

```json
{
  "to": "task_002",
  "message": {
    "inReplyTo": "req_w01",
    "input": [{ "type": "text", "inline": "Executive audience" }]
  }
}
```

The adapter injects `from.kind: "agent"` and `from.id: <current taskId>` before forwarding to the gateway. The runtime is responsible for what it says and to whom — not for identifying itself. Identity is the platform's responsibility.

---

## The General Principle

This follows from a broader rule that's worth stating in the spec: **fields that are derivable from execution context should be injected by the adapter, not supplied by the runtime.** The adapter knows the current `sessionId`, `taskId`, `tenantId`, and `leaseId`. The runtime knows the content of its messages and what it wants to do. The boundary between those two concerns should be enforced at the tool schema level — fields the runtime shouldn't supply simply don't appear in the tool's input schema.

Applied consistently:
- `from.id` and `from.kind` — injected by adapter, not in schema
- `requestId` in `lenny/request_input` — generated by gateway, not in schema
- `sessionId` context on any tool call — never in schema, always from adapter context
- `threadId` if a default thread exists — could be injected by adapter, with the runtime allowed to override for explicit thread creation

---

## Human (Apr 1, 2026, 9:03 PM):

Thanks, this is much better. However, there's still something that's bugging me: should the adapter, not the runtime, populate certain fields in the tool call request?
* `request_id` (when the id if first created in the `lenny/request_input` tool call
* `from, id, in lenny/send-message`

---

## Claude:

Let me think through the scenario carefully before writing it out. The goal is a realistic flow that naturally requires every interaction type without forcing any.

**Scenario: Investment Research Report**

The client asks an orchestrator to prepare a comprehensive investment report on ACME Corp. The orchestrator spawns a Researcher and an Analyst in parallel. During execution:

- Researcher needs time period clarification from parent (parent-child multi-turn synchronous)
- Researcher sends an interim finding to the Analyst asynchronously (sibling async message)
- Client pushes an additional requirement while parent is awaiting (client async injection — buffered)
- Analyst needs output format clarification the parent can't answer alone (elicitation chain to human)
- Both children complete; parent begins writing synthesis
- Client interrupts the parent mid-synthesis (lifecycle channel interrupt)
- Client resumes with modified scope (client async message delivery after interrupt)
- Parent completes

**Notation:** `→` request/send, `←` response/delivery, `[ ]` internal state change. gRPC is used for all adapter↔gateway communication. Stdin/stdout is used for all adapter↔runtime communication. MCP tool calls are shown as call/return pairs.

---

## Phase 1 — Session Creation and Initial Prompt

```
Client                    Gateway                 Parent Adapter        Parent Runtime
  |                          |                         |                      |
  |-- POST /v1/sessions ----->|                         |                      |
  |   runtime:"orchestrator" |-- [allocate warm pod] -->|                      |
  |                          |   gRPC AssignCredentials->|                     |
  |                          |   gRPC StartSession ----->|                     |
  |                          |                          |-- stdin prompt ------>|
  |                          |                          |  {type:"prompt"       |
  |                          |                          |   input:[             |
  |<- {sessionId:sess_parent}|                          |    {type:"text"       |
  |   upload_token           |                          |    "Prepare ACME      |
  |                          |                          |     report"}]}        |
  |                          |                          |                      |
  |-- GET /v1/sessions/      |                          |                      |
  |   sess_parent/stream --->| [SSE stream opens]       |                      |
  |   [SSE open]             |                          |                      |
```

---

## Phase 2 — Parent Delegates to Researcher and Analyst

```
Parent Runtime             Parent Adapter                Gateway
     |                          |                            |
     |-- MCP tool_call --------->|                           |
     |  lenny/delegate_task      |-- gRPC DelegateTask ------>|
     |  {target:"researcher"     |  [adapter injects          |-- validates lease
     |   task:{input:[...]}}     |   from context:            |-- claims warm pod
     |                           |   parentTaskId:task_root]} |-- creates sess_researcher
     |                           |                            |   taskId: task_res
     |                           |                            |
     |                           |       Gateway         Researcher Adapter    Researcher Runtime
     |                           |          |                 |                      |
     |                           |          |-- gRPC PrepareWorkspace/Finalize/      |
     |                           |          |   AssignCredentials/StartSession ------>|
     |                           |          |                 |-- stdin prompt ------->|
     |                           |          |                 |  {type:"prompt"        |
     |                           |          |                 |   input:[task]}        |
     |                           |<- gRPC DelegateTaskResponse|                       |
     |                           |   taskId: task_res         |                       |
     |<- MCP tool_result --------|                            |                       |
     |  {taskId: "task_res"}     |                            |                       |
     |                           |                            |                       |
     |-- MCP tool_call --------->|                            |                       |
     |  lenny/delegate_task      |-- gRPC DelegateTask ------>|                       |
     |  {target:"analyst"        |                            |-- same startup flow    |
     |   task:{input:[...]}}     |                            |   sess_analyst         |
     |                           |                            |   taskId: task_ana     |
     |<- MCP tool_result --------|                            |                       |
     |  {taskId: "task_ana"}     |                            |                       |
     |                           |                            |                       |
     |-- MCP tool_call --------->|                            |                       |
     |  lenny/await_children     |-- gRPC AwaitChildren ----->|                       |
     |  {taskIds:[task_res,      |   mode: "settled"          |-- [registers wait,     |
     |   task_ana]               |                            |    streaming response  |
     |   mode:"settled"}         |                            |    held open]          |
     |  [parent blocked]         |  [gRPC stream pending]     |                       |
```

---

## Phase 3 — Researcher Requests Input (Parent-Child Synchronous Multi-Turn)

Researcher needs to know which time period to focus on.

```
Researcher Runtime       Researcher Adapter           Gateway
     |                         |                          |
     |-- MCP tool_call -------->|                         |
     |  lenny/request_input     |-- gRPC RequestInput --->|
     |  {parts:[{type:"text"    |  [adapter injects        |-- [stores pending call
     |   "Which time period?"}]}|   taskId:task_res        |    generates requestId:
     |                          |   gateway generates      |    req_001]
     |  [researcher blocked     |   requestId: req_001]    |-- [task_res state →
     |   in tool call]          |  [gRPC call pending]     |    input_required]
     |                          |                          |
     |                          |                          |-- [unblocks parent's
     |                          |                          |   AwaitChildren stream]
     |                          |                          |
```

Gateway streams partial event to parent's pending AwaitChildren call:

```
    Gateway              Parent Adapter              Parent Runtime
       |                      |                           |
       |-- gRPC AwaitEvent --->|                          |
       |  {taskId: task_res    |-- MCP tool_result ------->|
       |   status:input_req    |  [await_children returns  |
       |   requestId: req_001  |   partial event, not      |
       |   parts:[question]}   |   final result]           |
       |                       |  {taskId:"task_res"       |
       |                       |   status:"input_required" |
       |                       |   requestId:"req_001"     |
       |                       |   parts:[question]}       |
```

Parent knows the answer and resolves directly:

```
Parent Runtime             Parent Adapter                Gateway
     |                          |                            |
     |-- MCP tool_call --------->|                           |
     |  lenny/send_message       |-- gRPC SendMessage ------->|
     |  {to:"task_res"           |  [adapter injects          |-- [sees task_res has
     |   message:{               |   from:{kind:"agent"       |   outstanding req_001]
     |    inReplyTo:"req_001"    |   id: task_root}}          |-- [resolves pending
     |    input:[{type:"text"    |                            |   RequestInput gRPC
     |     "2020-2024"}]}}       |                            |   with the message]
     |                           |                            |
     |<- MCP tool_result --------|<- gRPC SendMessageResp ----|
     |  {delivered: true}        |  delivered: true           |
     |                           |                            |
```

Gateway resolves the researcher's blocked tool call:

```
    Gateway           Researcher Adapter        Researcher Runtime
       |                    |                          |
       |-- gRPC ResolveReq ->|                         |
       |   requestId:req_001 |                         |
       |   message:{         |-- MCP tool_result ------->|
       |    inReplyTo:req_001|  lenny/request_input      |
       |    input:["2020-24"]}|  returns:                |
       |                     |  {id:"msg_p01"            |
       |                     |   from:{kind:"agent"      |
       |                     |        id:"task_root"}    |
       |                     |   input:[{inline:         |
       |                     |    "2020-2024"}]}         |
       |                     |                          |
       |                     |  [researcher unblocked,  |
       |                     |   continues working]     |
```

Parent re-awaits both children:

```
Parent Runtime             Parent Adapter              Gateway
     |                          |                          |
     |-- MCP tool_call --------->|                         |
     |  lenny/await_children     |-- gRPC AwaitChildren --->|
     |  {taskIds:[task_res,      |   mode:"settled"         |-- [registers wait]
     |   task_ana]               |                          |
     |   mode:"settled"}         |  [gRPC stream pending]   |
     |  [parent blocked]         |                          |
```

---

## Phase 4 — Researcher Sends Async Finding to Analyst (Sibling Async)

```
Researcher Runtime       Researcher Adapter           Gateway
     |                         |                          |
     |-- MCP tool_call -------->|                         |
     |  lenny/get_task_tree     |-- gRPC GetTaskTree ----->|
     |                          |<- gRPC TaskTreeResponse--|
     |<- MCP tool_result --------|                        |
     |  {self:{taskId:task_res} |                         |
     |   parent:{taskId:task_root}                        |
     |   siblings:[             |                         |
     |    {taskId:task_ana      |                         |
     |     state:"running"}]}   |                         |
     |                          |                         |
     |-- MCP tool_call -------->|                         |
     |  lenny/send_message      |-- gRPC SendMessage ----->|
     |  {to:"task_ana"          |  [adapter injects        |-- [checks task_ana:
     |   message:{              |   from:{kind:"agent"     |   no outstanding
     |    id:"msg_s01"          |   id:task_res}}          |   request_input]
     |    input:[{type:"text"   |                          |-- [route: stdin
     |     "Key finding:        |                          |   delivery]
     |     ACME revenue up 40%  |                          |
     |     in 2023"}]}}         |                          |
     |                          |                          |
```

No pending `request_input` on analyst → gateway delivers via stdin:

```
    Gateway              Analyst Adapter           Analyst Runtime
       |                      |                          |
       |-- gRPC DeliverMessage->|                        |
       |   message:{           | [analyst between steps, |
       |    id:"msg_s01"       |  stdin available]        |
       |    from:{kind:"agent" |-- stdin message --------->|
       |     id:"task_res"}    |  {type:"message"          |
       |    input:[finding]}   |   id:"msg_s01"            |
       |                       |   from:{kind:"agent"      |
       |                       |        id:"task_res"}     |
       |<- gRPC DeliverMsgAck -|   input:[finding]}        |
       |   delivered:true      |                          |
       |                       |                          |
```

Back to researcher:

```
    Gateway           Researcher Adapter        Researcher Runtime
       |                    |                          |
       |<- gRPC SendMsgResp-|                          |
       |   delivered:true   |-- MCP tool_result ------->|
       |                    |  {delivered:true}         |
       |                    |                          |
       [researcher continues and eventually completes]
       |                    |<- stdout response --------|
       |                    |  {type:"response"         |
       |                    |   parts:[full findings]}  |
       |-- gRPC TaskComplete->|                         |
       |   taskId:task_res  |                          |
       |   parts:[...]      |                          |
   [gateway: task_res → completed]
   [parent's AwaitChildren NOT yet unblocked — task_ana still running]
```

---

## Phase 5 — Client Sends Async Message (Buffered — Parent is Awaiting)

The client wants to add an ESG section to the report. The parent is blocked inside `await_children`.

```
Client                    Gateway
  |                          |
  |-- POST /v1/sessions/     |
  |   sess_parent/messages ->|
  |  {message:{              |-- [checks parent session:
  |   id:"msg_c01"           |   parent is blocked in
  |   from:{kind:"client"    |   await_children tool call]
  |    id:"user_123"}        |-- [no outstanding request_input
  |   input:[{type:"text"    |   on parent session]
  |    "Also add ESG         |-- [buffers msg_c01 in
  |     metrics section"}]}} |   parent's inbox queue]
  |                          |
  |<- {queued:true}          |
```

The message sits in the gateway inbox until the parent's await_children unblocks.

---

## Phase 6 — Analyst Requests Input; Elicitation Chain to Human

Analyst needs to know the output format — something neither the parent nor the system can answer without asking the human.

```
Analyst Runtime          Analyst Adapter              Gateway
     |                        |                           |
     |-- MCP tool_call ------->|                          |
     |  lenny/request_input    |-- gRPC RequestInput ----->|
     |  {parts:[{type:"text"   |  [adapter injects         |-- [generates req_002]
     |   "Slide deck or        |   taskId:task_ana]}       |-- [task_ana →
     |    prose report?"}]}    |                           |    input_required]
     |  [analyst blocked]      |  [gRPC call pending]      |-- [unblocks parent's
     |                         |                           |    AwaitChildren stream]
```

Gateway unblocks parent's await — AND delivers the buffered client message first:

```
    Gateway              Parent Adapter              Parent Runtime
       |                      |                           |
       |                      |                           |
       | [gateway drains inbox |                           |
       |  before delivering    |                           |
       |  await event]         |                           |
       |                       |                           |
       |-- gRPC DeliverMessage->|                          |
       |   message: msg_c01    |-- stdin message --------->|
       |   (buffered client    |  {type:"message"          |
       |    message from ph.5) |   id:"msg_c01"            |
       |<- gRPC DeliverMsgAck -|   from:{kind:"client"     |
       |                       |        id:"user_123"}     |
       |                       |   input:["Also add        |
       |                       |    ESG metrics"]}         |
       |                       |                           |
       |-- gRPC AwaitEvent ---->|                          |
       |  {taskId:task_ana     |-- MCP tool_result -------->|
       |   status:input_req    |  [await_children returns  |
       |   requestId:req_002   |   partial event]          |
       |   parts:[format q]}   |  {taskId:"task_ana"       |
       |                       |   status:"input_required" |
       |                       |   requestId:"req_002"     |
       |                       |   parts:[format question]}|
```

Parent doesn't know the answer — escalates to human via elicitation:

```
Parent Runtime             Parent Adapter                Gateway
     |                          |                            |
     |-- MCP tool_call --------->|                           |
     |  lenny/request_elicitation|-- gRPC RequestElicitation->|
     |  {parts:[{type:"text"     |  [adapter injects          |-- [chains provenance:
     |   "Format preference:     |   sessionId:sess_parent    |   analyst→parent
     |    slide deck or prose?"}]|   taskId:task_root]}       |   →client]
     |  [parent blocked in       |                            |-- [surfaces to client
     |   elicitation call]       |  [gRPC call pending]       |   via SSE stream]
     |                           |                            |
```

Client receives elicitation on SSE stream and responds:

```
Client                    Gateway
  |                          |
  |<-- SSE event ------------|
  |  {type:"elicitation"     |
  |   elicitationId:"elic_01"|
  |   parts:[format q]}      |
  |                          |
  |-- POST /v1/sessions/     |
  |  sess_parent/elicitations|
  |  /elic_01/respond ------->|
  |  {response:{type:"text"  |-- [resolves parent's pending
  |   "Prose report"}}       |   RequestElicitation gRPC]
  |                          |
```

Gateway resolves parent's elicitation call:

```
    Gateway              Parent Adapter              Parent Runtime
       |                      |                           |
       |-- gRPC ResolveElic -->|                          |
       |   elicitationId:      |-- MCP tool_result ------->|
       |   elic_01             |  lenny/request_elicitation|
       |   response:           |  returns:                 |
       |   "Prose report"      |  {answer:"Prose report"}  |
       |                       |                          |
       |                       |  [parent now has the      |
       |                       |   answer for analyst]     |
```

Parent resolves analyst's input_required:

```
Parent Runtime             Parent Adapter                Gateway
     |                          |                            |
     |-- MCP tool_call --------->|                           |
     |  lenny/send_message       |-- gRPC SendMessage ------->|
     |  {to:"task_ana"           |  [adapter injects          |-- [sees task_ana has
     |   message:{               |   from:{kind:"agent"       |   outstanding req_002]
     |    inReplyTo:"req_002"    |   id:task_root}}           |-- [resolves pending
     |    input:[{type:"text"    |                            |   RequestInput gRPC]
     |     "Prose report"}]}}    |                            |
     |<- MCP tool_result --------|<- gRPC SendMessageResp ----|
     |  {delivered:true}         |  delivered:true            |
```

Analyst's blocked tool call returns and analyst continues:

```
    Gateway           Analyst Adapter           Analyst Runtime
       |                   |                          |
       |-- gRPC ResolveReq->|                         |
       |   requestId:req_002|-- MCP tool_result ------->|
       |   message:         |  lenny/request_input      |
       |   {inReplyTo:req_002  returns:               |
       |    input:           |  {input:["Prose report"]}|
       |    ["Prose report"]}|                          |
       |                    |  [analyst unblocked,     |
       |                    |   continues writing]     |
```

Parent re-awaits:

```
Parent Runtime             Parent Adapter              Gateway
     |                          |                          |
     |-- MCP tool_call --------->|                         |
     |  lenny/await_children     |-- gRPC AwaitChildren --->|
     |  {taskIds:[task_res,      |   mode:"settled"         |
     |   task_ana]               |  [gRPC stream pending]   |
     |   mode:"settled"}         |                          |
     |  [parent blocked]         |                          |
```

Both children complete and await_children returns:

```
    Gateway              Parent Adapter              Parent Runtime
       |                      |                           |
  [task_ana completes]        |                           |
  [task_res already done]     |                           |
  [both settled]              |                           |
       |-- gRPC AwaitResult -->|                          |
       |  [{taskId:task_res    |-- MCP tool_result ------->|
       |    status:completed   |  await_children returns   |
       |    parts:[research]}, |  [{taskId:"task_res"      |
       |   {taskId:task_ana    |    status:"completed"     |
       |    status:completed   |    parts:[...]},          |
       |    parts:[analysis]}] |   {taskId:"task_ana"      |
       |                       |    status:"completed"     |
       |                       |    parts:[...]}]          |
```

---

## Phase 7 — Parent Begins Synthesis; Client Interrupts

Parent starts writing the final synthesis (actively processing, doing LLM calls).

```
Client                    Gateway                  Parent Adapter      Parent Runtime
  |                          |                          |                    |
  |                          |             [parent is actively running,      |
  |                          |              doing LLM synthesis work]         |
  |                          |                          |                    |
  |-- POST /v1/sessions/     |                          |                    |
  |  sess_parent/interrupt -->|                         |                    |
  |                          |-- gRPC InterruptRequest ->|                   |
  |                          |                          |-- lifecycle chan -->|
  |                          |                          |  {type:            |
  |                          |                          |  "interrupt_request"}
  |                          |                          |                    |
  |                          |                          |<- lifecycle chan ---|
  |                          |                          |  {type:            |
  |                          |<- gRPC InterruptAck -----|  "interrupt_ack"}  |
  |                          |  [sess_parent →          |                    |
  |<- SSE event -------------|   suspended]             |                    |
  |  {type:"status_change"   |                          |                    |
  |   state:"suspended"}     |                          |                    |
```

---

## Phase 8 — Client Resumes with Modified Scope

The client has reconsidered and wants a shorter executive summary instead of a full prose report.

```
Client                    Gateway
  |                          |
  |-- POST /v1/sessions/     |
  |  sess_parent/messages --->|
  |  {message:{              |-- [sess_parent is suspended,
  |   id:"msg_c02"           |   no outstanding request_input,
  |   from:{kind:"client"    |   delivery: "immediate"]
  |    id:"user_123"}        |-- [transitions sess_parent →
  |   delivery:"immediate"   |    running]
  |   input:[{type:"text"    |-- [delivers immediately since
  |    "Shorten to 2-page    |    runtime will read stdin
  |     exec summary,        |    on resume]
  |     not full report"}]}} |
  |                          |
```

Gateway delivers to parent as stdin message:

```
    Gateway              Parent Adapter              Parent Runtime
       |                      |                           |
       |-- gRPC DeliverMessage->|                         |
       |   message: msg_c02    |-- stdin message --------->|
       |                       |  {type:"message"          |
       |<- gRPC DeliverMsgAck -|   id:"msg_c02"            |
       |                       |   from:{kind:"client"     |
       |                       |        id:"user_123"}     |
       |                       |   delivery:"immediate"    |
       |                       |   input:["Shorten to 2-   |
       |                       |    page exec summary"]}   |
       |                       |                          |
       |                       |  [parent incorporates     |
       |                       |   revised scope and       |
       |                       |   rewrites synthesis]     |
```

---

## Phase 9 — Parent Completes; SSE Stream Closes

```
Parent Runtime             Parent Adapter              Gateway             Client
     |                          |                          |                  |
     |-- stdout response ------->|                         |                  |
     |  {type:"response"         |-- gRPC TaskComplete ---->|                 |
     |   parts:[{type:"text"     |   sessionId:sess_parent  |-- SSE event ---->|
     |    inline:"2-page exec    |   parts:[summary]}       |  {type:          |
     |    summary..."}]}         |                          |  "session_complete"
     |                           |                          |   result:{       |
     |                           |                          |    parts:[...]}} |
     |                           |                          |                  |
     |                           |                          |-- [SSE stream closes]
     |                           |                          |                  |
     |                           |-- gRPC Terminate -------->|                |
     |<- stdin shutdown ----------|                         |                  |
```

---

## What This Demonstrates

Every pattern is exercised and each has a distinct code path:

| Pattern | Where | Gateway mechanism |
|---|---|---|
| Initial prompt | Phase 1 | stdin `{type:"prompt"}` |
| Delegation | Phase 2 | gRPC DelegateTask → StartSession |
| Parent-child sync multi-turn | Phase 3 | RequestInput gRPC held open, resolved by SendMessage |
| Sibling async (no pending request) | Phase 4 | SendMessage → DeliverMessage → stdin `{type:"message"}` |
| Client async (parent blocked) | Phase 5 | Buffered in inbox, drained before next await event |
| Elicitation chain | Phase 6 | RequestElicitation → SSE to client → resolves via REST |
| Inbox drain on await unblock | Phase 6 | Buffered message delivered before await_children event |
| Interrupt | Phase 7 | lifecycle channel `interrupt_request` / `interrupt_ack` |
| Resume with message | Phase 8 | `{type:"message"}` on stdin after session transitions to running |
| Session completion | Phase 9 | stdout response → gRPC TaskComplete → SSE session_complete |

The three delivery paths for messages are clearly distinct:
1. **Pending `request_input` exists** → gateway resolves the blocked tool call directly (Phase 3, Phase 6)
2. **No pending request, runtime available** → stdin `{type:"message"}` immediately (Phase 4, Phase 8)
3. **No pending request, runtime blocked in await** → buffered in inbox, delivered when next unblocked (Phase 5 → delivered in Phase 6)

---

## Human (Apr 1, 2026, 9:06 PM):

Alright, this is starting to look good. Now, come up with a new example, one that includes a combination of all possible interactions: user initiated async messages, parent-child multi-turn conversation,  elicitation chain, parent-child async messages, interruption, etc.

Think this one carefully and make sure everything is included

---

## Claude:

Good question to ask explicitly, because the MCP external interface was designed for a different primary use case and the mapping is imperfect in some directions.

---

## What the MCP External Interface Is For

The gateway exposes an MCP server over Streamable HTTP to external clients. This is the interface used by MCP hosts — Claude Desktop, Cursor, any application that connects to MCP servers. The design intent is: an MCP host treats Lenny as a tool provider. The host's LLM decides when to call Lenny tools, and the session lifecycle is driven by those tool calls.

This is a different interaction model from the REST+SSE interface. The REST interface is imperative and client-driven: the client explicitly creates sessions, uploads files, attaches, and streams. The MCP interface is declarative: the host presents the tools to its LLM, the LLM decides what to call, and the MCP protocol carries the results back.

---

## What the MCP Interface Looks Like for the Same Scenario

The client (MCP host) connects to the Lenny gateway as an MCP server. The gateway advertises its tools. The host's LLM invokes them.

**Session creation and initial prompt:**

```
MCP Host                          Gateway (MCP Server)
    |                                    |
    |-- initialize ---------------------->|
    |   capabilities: {elicitation:{}}   |-- [capability negotiation]
    |<- initialize result ----------------|
    |   capabilities: {tools:{}, tasks:{}}|
    |                                    |
    |-- tools/list ---------------------->|
    |<- tools/list result ----------------|
    |   [create_session, upload_files,    |
    |    finalize_workspace,              |
    |    attach_session, send_message,    |
    |    interrupt_session, ...]          |
    |                                    |
    |   [LLM decides to create session]  |
    |-- tools/call ---------------------->|
    |   create_and_start_session          |
    |   {runtime:"orchestrator"          |
    |    input:[{type:"text"             |
    |     "Prepare ACME report"}]}       |
    |                                    |
    |<- tools/call result ----------------|
    |   [returns MCP Task reference]     |
    |   {taskId:"mcp_task_abc"           |
    |    status:"running"                |
    |    sessionId:"sess_parent"}        |
```

The key shift: `create_and_start_session` returns an **MCP Task** — a long-running task reference rather than a simple value. The MCP host subscribes to updates on this task and receives streaming notifications as the session produces output.

**Receiving output — via MCP task notifications:**

```
MCP Host                          Gateway (MCP Server)
    |                                    |
    |   [subscribed to task mcp_task_abc]|
    |                                    |
    |<- notifications/message -----------|
    |   {method:"notifications/tasks/   |
    |    updated"                        |
    |    taskId:"mcp_task_abc"           |
    |    update:{type:"agent_output"     |
    |     parts:[{type:"text"           |
    |      inline:"Analyzing..."}]}}     |
```

**Elicitation — this is where MCP shines:**

When the analyst asks for format preference and it escalates to the human, MCP elicitation is the native path:

```
MCP Host                          Gateway (MCP Server)
    |                                    |
    |<- elicitation/create --------------|
    |   {elicitationId:"elic_01"         |
    |    requestedSchema:{               |
    |     type:"object"                  |
    |     properties:{                   |
    |      format:{type:"string"         |
    |       enum:["Slide deck",          |
    |             "Prose report"]}}}     |
    |    message:"Format preference?"}  |
    |                                    |
    |   [host presents to user,          |
    |    user selects]                   |
    |                                    |
    |-- elicitation/respond ------------>|
    |   {elicitationId:"elic_01"         |
    |    response:{format:"Prose report"}}|
```

This works cleanly because elicitation is a first-class MCP primitive — the protocol was designed for exactly this pattern.

**Async injection — the limitation begins here:**

The client wants to push the ESG requirement. In the REST model this is `POST /v1/sessions/{id}/messages`. In the MCP model, the host has to invoke a tool:

```
MCP Host                          Gateway (MCP Server)
    |                                    |
    |   [LLM decides to inject message]  |
    |-- tools/call ---------------------->|
    |   send_message                      |
    |   {sessionId:"sess_parent"         |
    |    message:{                       |
    |     input:[{type:"text"            |
    |      "Also add ESG metrics"}]}}    |
    |                                    |
    |<- tools/call result ----------------|
    |   {queued:true}                    |
```

This works mechanically, but it has a critical structural problem: **the LLM has to decide to call this tool**. In the REST model, the client application directly calls the endpoint — it's a programmatic action driven by application logic. In the MCP model, the host presents the available tools to the LLM and the LLM decides whether and when to call them. If the user says "also add ESG metrics", the host's LLM has to reason about the current state, decide that `send_message` is the right tool, construct the call correctly, and invoke it. This introduces indirection, latency, and a failure mode that doesn't exist in the REST path — the LLM might not call the tool, might call it incorrectly, or might call it at the wrong time relative to the running session's state.

**Interrupt — same structural problem:**

```
MCP Host                          Gateway (MCP Server)
    |                                    |
    |   [user says "stop it"]            |
    |   [LLM must decide to call         |
    |    interrupt_session]              |
    |-- tools/call ---------------------->|
    |   interrupt_session                 |
    |   {sessionId:"sess_parent"}        |
    |                                    |
    |<- tools/call result ----------------|
    |   {state:"suspended"}             |
```

Again, the LLM is in the decision loop for an action that should be immediate and deterministic. A user clicking "interrupt" in a UI should trigger the interrupt immediately — not via an LLM deciding to call a tool.

---

## The Limitations, Stated Clearly

**1. Control plane actions are LLM-mediated.**

Interrupt, resume, session management, message injection — in the MCP model all of these require the host's LLM to recognise the intent and invoke the correct tool. This introduces latency (an extra LLM call), cost (tokens), unreliability (the LLM may not invoke the tool), and a loss of determinism. For interactive applications that need responsive control — interrupt a running agent, push information, resume — the REST interface is strictly better.

**2. No direct SSE stream.**

In the REST model, the client opens a persistent SSE stream and receives events continuously. In the MCP model, the host receives task update notifications through the MCP Streamable HTTP transport. These are structurally different: the SSE stream is a continuous push channel the application owns; MCP task notifications are surfaced to the LLM as context updates and the LLM decides what to do with them. An application that wants to display live token-by-token output to a user has to implement this rendering logic at the host layer, outside MCP's abstraction.

**3. Session identity is opaque.**

In the REST model the client holds `sessionId` explicitly and uses it for all subsequent operations. In the MCP model, the session is represented as an MCP Task reference. The host's LLM sees a task ID, not a Lenny session ID. If the host needs to correlate multiple tasks (parent and children), inject messages to specific sessions, or manage complex delegation trees, it has to map between MCP task IDs and Lenny session/task IDs — creating an impedance mismatch that has to be managed in prompts or tool schemas.

**4. Sibling and parent-child messaging require explicit tool calls.**

In a multi-agent Lenny scenario where the MCP host is orchestrating multiple sessions, routing messages between them requires the host's LLM to invoke `send_message` with correct target IDs at the right times. This is fragile. The REST+SSE model gives the application direct control over when and where messages go.

**5. Elicitation direction is fixed.**

MCP elicitation flows from server to client — the server (Lenny gateway) requests input from the client (MCP host). This works correctly for the elicitation chain pattern where the human needs to provide input. It does not work for agent-to-agent `request_input` — that is an internal Lenny mechanism invisible to the MCP host. The MCP host only sees the result when an elicitation surfaces at the top of the chain.

---

## When the MCP External Interface Is the Right Choice

Despite the limitations, there are scenarios where the MCP interface is exactly right:

**The host is an LLM-native application** — Claude Desktop, Cursor, or a custom MCP host where the primary interaction model is "LLM decides what tools to call." For a coding assistant that uses Lenny to spawn agent sessions, the MCP interface is natural — the assistant's LLM creates sessions, attaches, and reads results through normal tool call flows.

**The workflow is primarily session-oriented, not event-driven.** Create a session, wait for it to complete, get the result. No mid-session injection, no interrupt, no sibling coordination. This fits comfortably in MCP's task model.

**Elicitation is the primary human interaction pattern.** If the agent frequently needs structured input from the user and the host is already an MCP client, MCP's native elicitation primitive handles this better than any custom REST endpoint.

**The application doesn't need real-time streaming rendering.** If the result is consumed after completion rather than displayed token-by-token, the MCP task model is adequate.

---

## The Practical Recommendation for the Spec

Document both interfaces clearly with their intended use cases. The MCP external interface is the right entry point for LLM-native hosts that treat Lenny as a tool provider. The REST+SSE interface is the right entry point for applications that need direct programmatic control — interactive UIs, orchestration systems, CI/CD pipelines, anything with event-driven control flow. They are complementary, not redundant. A sophisticated application might use both: the MCP interface for session creation and result retrieval, and the REST interface for interrupt and message injection where deterministic control matters.

---

## Human (Apr 1, 2026, 9:27 PM):

Thanks, this looks good. So far, all these examples used the Lenny external API as the interface to the client. What would this look like if the client interfaced with Lenny via the MCP external interface? What are the limitations in that scenario?

---

## Claude:

Good research. Now I have a clear picture of both APIs. Let me work through each systematically.

---

## The Two APIs, Briefly

The Responses API is a unified interface for building powerful, agent-like applications. It contains built-in tools like web search, file search, computer use, code interpreter, and remote MCPs. It maintains stateful context using `store: true` to preserve reasoning and tool context from turn to turn, and supports background mode for long-running async tasks.

The Chat Completions API is an industry standard that OpenAI intends to continue supporting indefinitely. The key difference is that it requires clients to maintain their own conversation state, sending the full message history back with each request — whereas the Responses API can manage state server-side.

These represent two fundamentally different interaction models: Chat Completions is stateless client-driven, Responses is stateful server-driven. Lenny's architecture is much closer to the Responses model, which makes the mapping analysis asymmetric.

---

## Chat Completions API Mapping

**What the gateway would expose:**

```
POST /v1/chat/completions
{
  "model": "lenny:research-agent",   // runtime name in Lenny namespace
  "messages": [
    { "role": "system", "content": "You are a research assistant" },
    { "role": "user", "content": "Analyze ACME Corp financials" }
  ],
  "stream": true
}
```

The gateway translates this: `model` maps to a Lenny runtime name, the `messages` array becomes the initial prompt, and a session is created, run to completion, and the result returned as a chat completion response. If `stream: true`, output tokens stream as `delta` events in the standard SSE format.

**What works:**

Simple one-shot request/response maps cleanly. A client sending a prompt and receiving a streaming response works without the client knowing it's talking to Lenny. For tools declared in the request's `tools` array, the gateway can expose Lenny's registered connectors using the same function-calling schema.

**What breaks immediately:**

The fundamental mismatch is in tool execution. Chat Completions returns `tool_calls` in the response and expects the **client** to execute them and send back `tool_results`. In Lenny, the agent executes its own tools internally — the client never participates. The gateway has two options: run the agent to completion and return only the final text output (hiding all tool calls), or try to surface tool calls to the client and accept tool results — but this collapses Lenny into a thin LLM proxy, not an agent platform.

Every other Lenny concept is completely invisible: no workspace, no sessions (every call creates an ephemeral session that closes when the response completes), no elicitation (no path back to the client mid-execution), no async injection, no interrupt, no delegation tree. The Chat Completions mapping is a significant capability downgrade — Lenny becomes a stateless function call.

**One specific problem worth flagging:** `previous_message_id` doesn't exist in Chat Completions — the client sends the full history. There is no standard way to reference a previous Lenny session from a Chat Completions call. The gateway would have to invent a convention (a special message role, a custom header) to support session chaining, which immediately breaks compatibility with standard Chat Completions clients.

---

## Responses API Mapping

This is a substantially better fit. The key capabilities of the Responses API align with Lenny's design:

**Session creation and background execution:**

```
POST /v1/responses
{
  "model": "lenny:orchestrator",
  "input": [
    { "type": "input_text", "text": "Prepare ACME investment report" }
  ],
  "background": true,
  "store": true
}

→ {
  "id": "resp_abc123",      // maps to Lenny session ID
  "status": "in_progress",
  "output": []
}
```

`background: true` kicks off tasks asynchronously. Developers can either poll these objects to check for completion, or start streaming events whenever their application needs to catch up on the latest state.

This maps naturally to Lenny's session model: create a session, get an ID back, poll or stream for updates. The Responses API's `id`-based polling is equivalent to Lenny's `GET /v1/sessions/{id}` status check.

**Multi-turn via `previous_response_id`:**

```
POST /v1/responses
{
  "model": "lenny:orchestrator",
  "previous_response_id": "resp_abc123",
  "input": [{ "type": "input_text", "text": "Focus on 2020-2024" }]
}
```

This maps to Lenny's `POST /v1/sessions/{id}/messages` — sending a follow-up message to an existing session. The gateway translates `previous_response_id` to a Lenny session ID and delivers the input as a `{type: "message"}` on stdin.

**Streaming events:**

The Responses API streams typed events: `response.created`, `response.output_item.added`, `response.content_part.delta`, `response.completed`. Lenny's `OutputPart` events translate reasonably well into this schema — `agent_output` maps to `response.output_item.added`, individual streaming parts map to `content_part.delta`. The translation is mechanical rather than conceptual.

---

## Where the Responses API Mapping Breaks

**Elicitation has no representation.**

When a Lenny agent calls `lenny/request_elicitation`, it needs a human answer before it can continue. The Responses API has no mechanism for the server to pause and request structured input from the client mid-response. In the standard model, you either block the response until answered (problematic for background mode) or fail. The closest workaround is to complete the response with a special output item indicating input is needed, then require the client to start a new response with `previous_response_id` and the answer. This is awkward and breaks the atomic response model.

**Async message injection has no representation.**

`POST /v1/sessions/{id}/messages` for injecting information mid-execution has no Responses API equivalent. The only injection path in the Responses API is via a new response with `previous_response_id`. But that doesn't match Lenny's injection semantic — a new response would create a new execution context, whereas Lenny's injection delivers a message to a currently running session.

**Interrupt and resume have no representation.**

There is no standard Responses API mechanism to interrupt a running background response. The closest equivalent is cancellation, which terminates rather than suspends. The suspend/resume cycle and the lifecycle channel interrupt have no mapping.

**The tool execution model is inverted for custom functions.**

For OpenAI's built-in tools (web search, file search), the Responses API runs them internally — the client doesn't participate. The Responses API is an agentic loop, allowing the model to call multiple tools within the span of one API request. This matches Lenny's model for connectors. But for custom function tools, the Responses API still returns `function_call` output items expecting the client to execute them and start a new response with the results. Lenny doesn't support this pattern — connector execution is internal to the agent pod.

**Background mode duration mismatch.**

Background mode stores response data for approximately 10 minutes to allow polling. Lenny sessions can last hours, especially for long-running research or coding workflows. The 10-minute polling window is incompatible with Lenny's `maxSessionAge` of up to 2 hours. The gateway would need to extend or work around this, breaking compatibility with standard Responses API clients.

**Delegation tree is completely invisible.**

The entire parent-child task tree, sibling messaging, `await_children`, the multi-turn parent-child conversation from the previous examples — none of this has any representation in the Responses API. A client using the standard Responses API sees one response ID and has no visibility into the tree of agent sessions running beneath it. This isn't necessarily a problem for simple use cases, but it means the governance, cost attribution, and observability that Lenny provides are entirely opaque to the client.

---

## Summary Comparison

| Capability | Chat Completions | Responses API |
|---|---|---|
| One-shot request/response | ✓ Clean | ✓ Clean |
| Streaming output | ✓ Translated | ✓ Translated |
| Session persistence | ✗ None | ✓ Via `previous_response_id` |
| Long-running async | ✗ None | ~ `background: true` (10min limit) |
| Workspace files | ✗ None | ✗ None |
| Elicitation | ✗ None | ~ Awkward workaround only |
| Async injection | ✗ None | ✗ None |
| Interrupt/resume | ✗ None | ✗ None |
| Internal tool execution | ~ Opaque only | ✓ Built-in tools match |
| Custom function tools | ✗ Inverted model | ✗ Inverted model |
| Delegation tree visibility | ✗ None | ✗ None |
| Multi-agent coordination | ✗ None | ✗ None |

---

## The Practical Conclusion

Both APIs are useful as thin compatibility layers for clients that already speak OpenAI's formats — a Chat Completions client can fire a prompt at Lenny and get a response without changing a line of code, and a Responses API client gets session chaining and async background execution on top of that.

But neither API is a substitute for Lenny's native REST+SSE interface or MCP interface for any workflow that goes beyond simple request-response. The session-level governance, elicitation chains, injection, interruption, delegation primitives, and multi-agent coordination that make Lenny valuable are simply not expressible through either API.

The right framing for the spec: these compatibility adapters are **ingress translation layers** — they allow Lenny to accept traffic from clients built for OpenAI's APIs, translating it into Lenny sessions. They are not feature-complete interfaces. Clients that need Lenny's full capabilities use the native REST+SSE or MCP interfaces. Clients that need OpenAI compatibility use the translation layer and accept the reduced feature surface.

---

## Human (Apr 1, 2026, 9:33 PM):

Ok, great, this makes sense.

In a hypothetical scenario in which the gateway implemented external interfaces compatible with both the OpenAI completions API and the Open Responses API, how would this all work and what would be the limitations?

---

## Claude:

This maps cleanly onto the existing `ExternalProtocolAdapter` interface we already designed for MCP, A2A, and AP. The key change is recognizing that the current design treats `ExternalProtocolAdapter` as a single active implementation rather than a registry of simultaneously active adapters. That needs to change.

---

## The Required Architectural Change: From Single Adapter to Adapter Registry

The current design has the gateway selecting one `ExternalProtocolAdapter` implementation. For multiple simultaneous protocols — MCP on `/mcp`, Chat Completions on `/v1/chat/completions`, Responses API on `/v1/responses`, A2A on `/a2a/{runtime}` — the gateway needs a registry that routes incoming requests to the correct adapter based on path prefix.

```go
type ExternalProtocolAdapter interface {
    Protocol()    string   // "mcp" | "openai-completions" | "openai-responses"
                           // | "a2a" | "ap" | custom
    PathPrefix()  string   // "/mcp" | "/v1/chat/completions" | "/v1/responses"
                           // | "/a2a" | "/ap/v1/agent"

    // Inbound: translate external request to internal session operation
    HandleRequest(ctx context.Context,
                  w http.ResponseWriter,
                  r *http.Request,
                  dispatcher SessionDispatcher) error

    // Capabilities this adapter declares to the admin API
    Capabilities() AdapterCapabilities
}

type ExternalAdapterRegistry interface {
    Register(adapter ExternalProtocolAdapter) error
    Unregister(protocol string) error
    Route(r *http.Request) (ExternalProtocolAdapter, bool)
    List() []AdapterCapabilities
}
```

The `SessionDispatcher` passed into `HandleRequest` is the gateway's internal interface — it creates sessions, streams events, delivers messages, interrupts, and terminates. The adapter never touches Lenny internals directly; it translates through the dispatcher. This is the same principle as the `RequestInterceptor` — the adapter is a translation layer, not a peer.

The gateway edge becomes:

```
Incoming HTTP request
        │
        ▼
ExternalAdapterRegistry.Route(request)
        │
        ├── /mcp              → MCPAdapter.HandleRequest(...)
        ├── /v1/chat/completions → OpenAICompletionsAdapter.HandleRequest(...)
        ├── /v1/responses     → OpenAIResponsesAdapter.HandleRequest(...)
        ├── /a2a/{runtime}    → A2AAdapter.HandleRequest(...)         [post-v1]
        └── /ap/v1/agent      → AgentProtocolAdapter.HandleRequest(...)  [post-v1]
```

---

## Pluggability Mechanisms

Three tiers, consistent with the rest of Lenny's extension model:

**Tier 1 — Built-in (compiled in):** MCP, OpenAI Completions, OpenAI Responses. Registered at gateway startup, always available, configurable via admin API.

**Tier 2 — Config-driven loading:** A deployer can point the gateway at a Go plugin binary or a gRPC service endpoint that implements the `ExternalProtocolAdapter` interface. The gateway loads it at startup and registers it. This follows the same pattern as external `RequestInterceptor` implementations.

**Tier 3 — Runtime registration via admin API:**

```
POST /v1/admin/external-adapters
{
  "protocol": "my-custom-protocol",
  "pathPrefix": "/custom",
  "grpcEndpoint": "adapter-service:9090",
  "enabled": true
}
```

The gateway connects to the gRPC service, calls capability negotiation, and registers it in the registry. Changes take effect immediately — no restart needed. This is how a deployer could add AG-UI support, a proprietary protocol, or a legacy system adapter without touching Lenny's codebase.

**Enabling/disabling adapters:**

```
PUT /v1/admin/external-adapters/openai-completions
{
  "enabled": false
}
```

Disabling an adapter removes its path prefix from the registry. In-flight requests on that adapter complete; new requests get 404. This allows deployers to expose only the protocols appropriate for their deployment without recompiling.

---

## Authentication Translation

Each adapter brings its own auth conventions. The MCP adapter validates bearer tokens against Lenny's OIDC provider. The OpenAI-compatible adapters need to accept OpenAI-style `Authorization: Bearer sk-...` API keys and translate them to Lenny tenant/user identities.

The adapter receives the raw HTTP request including auth headers and is responsible for translating them to a `ClientIdentity` before calling the `SessionDispatcher`. The dispatcher enforces Lenny's policy engine against that identity — the adapter just needs to produce a valid identity from whatever auth mechanism its protocol uses.

This is declared in `AdapterCapabilities`:

```go
type AdapterCapabilities struct {
    Protocol      string
    PathPrefix    string
    AuthSchemes   []string    // ["bearer-oidc", "bearer-apikey", "mtls"]
    Features      []string    // what Lenny features this adapter exposes
    Version       string
}
```

The admin API exposes `GET /v1/admin/external-adapters` which lists all registered adapters with their capabilities and enabled status — giving deployers full visibility into what protocols the gateway is serving.

---

## OpenAI Completions Adapter

The translation is straightforward but lossy in one direction: the adapter creates a Lenny session, runs it to completion, and returns a chat completion response. The session is ephemeral — it terminates when the response is returned.

**`model` → runtime mapping:** The adapter reads a configured `modelRuntimeMapping` from the admin API:

```
POST /v1/admin/external-adapters/openai-completions/config
{
  "modelRuntimeMapping": {
    "gpt-4o": "langgraph-orchestrator",
    "gpt-4o-mini": "lightweight-agent",
    "*": "default-agent"
  }
}
```

This lets deployers control which Lenny runtime handles requests for each OpenAI model name. The `*` wildcard catches unmapped models.

**Tool handling — opaque mode only:** The adapter runs the agent to completion and suppresses intermediate tool calls from the response. The `choices[0].message.content` contains the final text output. Tool call round-trips between client and agent are not supported — the agent handles its own tool execution internally. If a client sends `tools: [...]` in the request, the adapter maps matching function names to Lenny connectors the session is permitted to use, then ignores the client's expectation of receiving `tool_calls` back.

**Streaming:** Translates Lenny's `OutputPart` streaming events to `data: {"choices": [{"delta": {"content": "..."}}]}` SSE events.

---

## OpenAI Responses Adapter

This adapter is more capable and the mapping is tighter.

**Session creation:**

```
POST /v1/responses
{
  "model": "lenny:research-agent",
  "input": [{"type": "input_text", "text": "..."}],
  "background": true,
  "store": true
}
→ { "id": "resp_sess_abc", "status": "in_progress" }
```

`background: true` creates an async Lenny session and returns the ID immediately. `store: true` is always true in Lenny — sessions are always persisted. The adapter ignores the `store` field.

**Polling and streaming:**

```
GET /v1/responses/resp_sess_abc           # poll for status
GET /v1/responses/resp_sess_abc/stream    # stream from current position
```

These map to Lenny's `GET /v1/sessions/{id}` and `GET /v1/sessions/{id}/stream`. The adapter translates Lenny's event types to Responses API event types.

**Session chaining via `previous_response_id`:**

```
POST /v1/responses
{
  "previous_response_id": "resp_sess_abc",
  "input": [{"type": "input_text", "text": "Also add ESG metrics"}]
}
```

The adapter translates this to `POST /v1/sessions/sess_abc/messages` — delivering a `{type: "message"}` to the running session's stdin. If the session has already completed, the adapter creates a derived session via `POST /v1/sessions/sess_abc/derive`. The client gets a new response ID either way.

**Background duration:** The 10-minute polling window in OpenAI's implementation is a platform constraint, not a protocol constraint. The adapter advertises no polling window limit — Lenny sessions run for their configured `maxSessionAge`. This is a deliberate deviation from OpenAI's behavior that deployers should document.

---

## What Goes in the Spec

Three additions to the technical design:

**Section 4.1 (Gateway Edge):** Replace the single `ExternalProtocolAdapter` with `ExternalAdapterRegistry`. Document the routing mechanism and the three tiers of pluggability.

**Section 15 (APIs):** Add Section 15.3 (External Protocol Adapters) describing the adapter interface, the admin API for managing adapters, and the built-in adapter inventory. The existing MCP and REST sections become subsections within a unified external interface section.

**Section 15.4 (OpenAI Compatibility):** Document the Chat Completions and Responses API adapters, the model-to-runtime mapping configuration, the feature matrix (what Lenny capabilities are accessible through each), and the deliberate deviations from OpenAI's behavior (no polling window limit, no client-side tool execution, elicitation not supported).

**Spec note on deliberate deviations:** The adapters aim for wire-level compatibility with existing OpenAI clients but are not OpenAI drop-in replacements. Clients that depend on OpenAI-specific features (built-in web search, file search, code interpreter, OpenAI's own state management) will not get those features through Lenny. The adapters provide enough compatibility that a standard OpenAI client SDK can talk to Lenny without modification, but the underlying execution is Lenny's runtime model, not OpenAI's inference infrastructure.

---

## Human (Apr 1, 2026, 9:49 PM):

Ok. I think we should add support for both OpenAI completions API and Responses API. It is understood that each has limitations and the client won't be able to access all of Lenny's functionality if it choses either of these interfaces.

The gateway needs to be designed such that these additional interfaces are "pluggable"

---

## Claude:

Good. Now I have the full picture of what's currently defined. Let me address both questions.

---

## Current Tool List and What Needs to Change

The current tool list in Section 15.2 reflects the original design before the changes in this conversation. It needs a significant revision. Let me go through what's now stale, what's missing, and then address the runtime discovery question.

**Tools that need renaming or replacing:**

`send_prompt` — this was the old text-only follow-up mechanism. It should become `send_message` accepting `MessageEnvelope` (our new design). The old name and schema are gone.

`create_session`, `upload_files`, `finalize_workspace`, `start_session` — these are the multi-step workspace setup flow. `create_and_start_session` already combines them for the common case. These individual tools can stay for advanced use but should be clearly documented as the low-level path.

**Tools that are now missing entirely:**

`list_runtimes` — the answer to your second question, discussed below.

`send_message` — replaces `send_prompt`, accepts the full `MessageEnvelope` including `inReplyTo`, `threadId`, `delivery`.

`respond_to_elicitation` — the client needs a tool to respond to elicitation requests that arrive during a session. Currently elicitation responses go through a separate REST endpoint; they should also be available as an MCP tool for MCP-native clients.

`inject_message` / note that this is now unified with `send_message` — same tool, the `delivery` field controls behavior.

---

## The Revised Tool List

| Tool | Description | Notes |
|---|---|---|
| `list_runtimes` | List runtimes the caller is authorized to use | New — answers your question |
| `create_session` | Create a session for a named runtime | `runtime` parameter |
| `create_and_start_session` | Create, upload inline files, and start | Fast path |
| `upload_files` | Upload workspace files to a pending session | Low-level |
| `finalize_workspace` | Validate and materialize workspace, run setup | Low-level |
| `start_session` | Start the agent runtime | Low-level |
| `attach_session` | Attach to a running session, returns streaming task | Primary interaction tool |
| `send_message` | Send a message to a session (prompt, injection, reply to elicitation) | Replaces `send_prompt` |
| `respond_to_elicitation` | Respond to a pending elicitation request | New explicit tool |
| `interrupt_session` | Send interrupt signal, session transitions to suspended | |
| `resume_session` | Resume a suspended session | |
| `get_session_status` | Query session state and current task tree | |
| `get_task_tree` | Get full delegation tree for a session | |
| `get_session_logs` | Get session logs (paginated) | |
| `get_token_usage` | Get token and resource usage | |
| `list_artifacts` | List artifacts for a session | |
| `download_artifact` | Download a specific artifact | |
| `terminate_session` | End a session | |
| `list_sessions` | List active and recent sessions (filterable) | |
| `derive_session` | Create a new session pre-populated from a completed session | |

---

## How the Client Learns About Available Runtimes

The `list_runtimes` tool is the answer. When an MCP client connects and the host's LLM calls `list_runtimes`, it gets back the runtimes the caller is authorized to create sessions for, filtered by their delegation policy. The response includes enough information for the LLM to reason about which runtime to use:

```json
{
  "runtimes": [
    {
      "name": "research-pipeline",
      "kind": "derived",
      "baseRuntime": "langgraph-runtime",
      "type": "agent",
      "capabilities": {
        "interaction": "one_shot"
      },
      "agentInterface": {
        "description": "Deep research and document analysis",
        "inputModes": [
          { "type": "text/plain" },
          { "type": "application/json",
            "schema": "{ 'topic': 'string', 'depth': 'string' }" }
        ],
        "outputModes": [
          { "type": "text/plain", "role": "primary" }
        ],
        "supportsWorkspaceFiles": true,
        "skills": [
          { "id": "research", "name": "Research",
            "description": "Deep research on any topic" },
          { "id": "summarize", "name": "Summarize",
            "description": "Summarize documents" }
        ]
      }
    },
    {
      "name": "code-executor",
      "kind": "standalone",
      "type": "mcp",
      "agentInterface": null,
      "mcpCapabilities": {
        "tools": ["execute_python", "execute_bash", "read_file"]
      }
    }
  ]
}
```

Several things worth noting about this design:

**`agentInterface` is the LLM's decision aid.** The description, skills, input/output modes, and `supportsWorkspaceFiles` flag let the host's LLM make an informed choice about which runtime to use and how to structure the `create_session` call. A well-populated `agentInterface` means the LLM doesn't need to guess.

**`type: "mcp"` runtimes appear too, with their tool list.** The client needs to know these exist. For `type: mcp` runtimes, instead of `agentInterface` they expose `mcpCapabilities.tools` — the list of tools accessible via that runtime's MCP server once the session is created.

**The list is policy-scoped.** The gateway filters by the calling client's identity and delegation policy. A client only sees runtimes it's authorized to use. If `list_runtimes` returns it, `create_session` will succeed for it.

**This is a standard MCP `tools/list` complement, not a substitute.** The client discovers Lenny's platform tools via the normal MCP capability negotiation and `tools/list`. `list_runtimes` is specifically for discovering what agents can be created — it's a domain-level discovery call, not a protocol-level one.

**Runtime discovery for `create_and_start_session`:** The `runtime` parameter in `create_and_start_session` accepts any name returned by `list_runtimes`. The LLM sees the full list, picks the right runtime based on the task, and creates the session in one call. This is the primary usage pattern for MCP-native clients.

---

## Human (Apr 1, 2026, 9:52 PM):

Awesome, thanks.

Question about the external MCP interface: can you list all the exposed tools? How can the client learn about all the runtimes it has access to?

---

## Claude:

This is where the external client model and the internal model converge on the same solution, but via different paths. Let me think through it carefully.

---

## The Internal Model, Revisited

Internally, when a `type: agent` runtime wants to call tools on a `type: mcp` runtime, the adapter manifest lists that runtime's MCP server in `runtimeMcpServers`. The calling runtime connects to that server, performs capability negotiation, and calls tools. The adapter proxies all traffic through the gateway. Each runtime gets its own server endpoint — no aggregation.

The external model should follow the same principle: each `type: mcp` runtime gets its own MCP endpoint, and the client connects to it as a separate MCP server connection.

---

## What the Gateway Serves

For each enabled `type: mcp` runtime, the gateway serves a dedicated MCP endpoint:

```
/mcp/runtimes/{runtime-name}
```

The client connects here using the standard MCP Streamable HTTP transport. Standard capability negotiation happens. The client calls `tools/list` and gets back the runtime's actual tool schemas — the same tools that internal runtimes see via the adapter manifest. The gateway proxies the connection: external client → gateway → adapter → runtime MCP server socket.

`list_runtimes` returns the endpoint for each `type: mcp` runtime:

```json
{
  "name": "code-executor",
  "kind": "standalone",
  "type": "mcp",
  "mcpEndpoint": "/mcp/runtimes/code-executor",
  "mcpCapabilities": {
    "tools": ["execute_python", "execute_bash", "read_file"]
  }
}
```

The `mcpCapabilities.tools` in `list_runtimes` is a lightweight preview — the names and one-line descriptions. The full schemas with input/output types come from connecting to the endpoint and calling `tools/list` directly, which is standard MCP discovery.

---

## What the Gateway Does on Connection

When a client connects to `/mcp/runtimes/code-executor`:

1. Gateway authenticates the client and checks that the client's policy permits access to `code-executor`
2. Gateway allocates a pod from the runtime's warm pool — for `concurrent-stateless` runtimes this is Kubernetes service routing; for `concurrent-workspace` runtimes this is a slot reservation
3. Gateway proxies the MCP connection: all `tools/list`, `tools/call`, and notification traffic flows through the gateway to the adapter, which forwards to the runtime's local MCP socket
4. Gateway enforces rate limits, quota, and egress profiles on every tool call
5. Gateway records every tool call in the audit log: client identity, runtime name, tool name, arguments hash, response status, latency

The client sees a normal MCP server. It never knows it's talking to a Lenny-managed pod.

---

## Session Tracking

Unlike `type: agent` runtimes, `type: mcp` connections don't have an explicit session lifecycle (create → start → attach → complete). But the gateway still creates an implicit session record per connection for audit and billing purposes. This session record tracks:

- Connection duration
- Which tools were called and how many times
- Token usage if the runtime makes LLM calls internally
- Tenant and client identity

The client doesn't manage this session — it's opaque. But billing and audit work the same as for agent-type sessions.

---

## Why Not Aggregate Into the Main Gateway MCP Server

The alternative — exposing `type: mcp` runtime tools as namespaced tools on the main `/mcp` gateway endpoint — would mean one MCP connection gives access to everything. This was explicitly rejected earlier: aggregation is not lossless in MCP. Capability negotiation is per-server. Sampling flows correctly only per connection. Future MCP features with per-server identity or per-connection auth would break through an aggregated proxy.

Each runtime keeping its own endpoint means the client's MCP client library connects to each server independently, gets the correct capabilities for each, and the protocol behaves correctly. The client can connect to multiple runtimes simultaneously using multiple MCP connections — one to `/mcp` for the gateway's platform tools, one to `/mcp/runtimes/code-executor` for code execution, one to `/mcp/runtimes/search-agent` for search. This is the standard MCP multi-server model that host applications like Claude Desktop already support natively.

---

## Human (Apr 1, 2026, 9:55 PM):

How does the client access tools defined by `type: "mcp"` runtimes?

---

## Claude:

Correct. The principle is: everything the MCP external interface can do, the REST interface and the OpenAI-compatible interfaces should also be able to do — within the capability constraints of each protocol.

---

## REST Interface

This is the most complete translation. Every MCP tool maps to a REST endpoint, consistent with the existing REST/MCP consistency contract in Section 15.2.1.

**Runtime discovery:**

```
GET /v1/runtimes
```

Returns the same policy-scoped list that `list_runtimes` returns via MCP. Same filtering, same `agentInterface` payload, same `mcpEndpoint` field for `type: mcp` runtimes. Already partially implied by the existing `GET /v1/runtimes` endpoint in the spec, but it needs to be updated to return the full `agentInterface` and `mcpEndpoint` fields rather than just capability flags.

**`type: mcp` runtime access:**

The MCP connection endpoints are served at `/mcp/runtimes/{name}` — these are the same paths regardless of whether the client reached the discovery through REST or MCP. The REST client discovers the endpoint from `GET /v1/runtimes`, then opens a separate MCP connection to that endpoint. This is not redundant — REST is used for lifecycle management and discovery, MCP is used for the actual tool protocol.

**Message sending:** `POST /v1/sessions/{id}/messages` is the REST equivalent of `send_message`. Already defined. The `MessageEnvelope` schema is identical.

**Elicitation response:** `POST /v1/sessions/{id}/elicitations/{elicitation_id}/respond` is the REST equivalent of `respond_to_elicitation`.

**Session derivation:** `POST /v1/sessions/{id}/derive` is already in the spec.

---

## OpenAI Completions Adapter

The Completions API is stateless and the interface surface is narrow by design. The only relevant addition is runtime discovery.

`list_runtimes` maps to a custom endpoint the adapter adds:

```
GET /v1/models
```

This is the standard OpenAI models endpoint. Standard OpenAI clients call it to discover available models. The Completions adapter implements it to return Lenny's available runtimes in OpenAI's model format:

```json
{
  "data": [
    {
      "id": "lenny:research-pipeline",
      "object": "model",
      "description": "Deep research and document analysis",
      "capabilities": ["chat"]
    },
    {
      "id": "lenny:code-executor",
      "object": "model",
      "capabilities": ["tool_use"]
    }
  ]
}
```

`type: mcp` runtimes appear in the model list with `capabilities: ["tool_use"]`. Clients that pass `lenny:code-executor` as the `model` parameter get a session that runs that runtime and surfaces tool results in the response. The tool-execution-is-internal limitation still applies — the adapter runs the agent to completion and returns the result, it doesn't implement the client-side tool call round-trip. But at least the client can discover what's available.

---

## OpenAI Responses Adapter

The Responses API is more capable and the model list maps better.

```
GET /v1/models
```

Same implementation as the Completions adapter — returns Lenny runtimes as models. Additionally, for `type: mcp` runtimes, the adapter can surface their tools as function definitions in the Responses API's tool schema:

```
GET /v1/responses/tools?model=lenny:code-executor
```

Returns the runtime's tool schemas in OpenAI function-calling format, translated from the runtime's MCP `tools/list` response. This lets Responses API clients discover and call `type: mcp` runtime tools within the Responses API's built-in tool model — the gateway proxies the tool execution internally rather than returning `function_call` items for the client to execute.

---

## The General Rule for the Spec

Each external adapter is responsible for translating Lenny's runtime registry into its own discovery idiom. The spec should state this as a contract requirement on all `ExternalProtocolAdapter` implementations:

**Every adapter must implement runtime discovery.** The form is protocol-specific — `tools/list` for MCP, `GET /v1/models` for OpenAI-compatible adapters, the agent card registry endpoint for A2A, the agent manifest for AP — but the underlying data is always the policy-scoped runtime list from the Lenny registry. The `ExternalProtocolAdapter` interface gains a method:

```go
// Translate the caller's authorized runtime list into this protocol's
// discovery representation. Called by the gateway when the client
// requests discovery in this protocol's native format.
HandleDiscovery(ctx context.Context,
                w http.ResponseWriter,
                r *http.Request,
                runtimes []AuthorizedRuntime) error
```

This ensures that any future adapter — AG-UI, a custom enterprise protocol, whatever comes next — is required to implement discovery as part of the contract, not as an afterthought. A protocol that can't express runtime discovery at all should document that explicitly in its `AdapterCapabilities`.

---

## Human (Apr 1, 2026, 9:59 PM):

Ok, makes sense. Thanks. All this (listing runtimes and other actions) also need to be applied to all the other external interfaces

---

## Claude:

Good clarification. The research clarifies the relationship clearly.

---

## The Relationship Between the Two

Open Responses is an open-source specification and ecosystem for building multi-provider, interoperable LLM interfaces based on the OpenAI Responses API. Any implementer can be considered to have an Open Responses-compliant API if they have an API that implements this spec directly or is a proper superset of Open Responses.

Open Responses extends and improves the Responses API, providing richer and more detailed content definitions, compatibility, and deployment options. It also provides a standard way to execute sub-agent loops during primary inference calls.

The hierarchy is:

```
Open Responses Specification  (open standard, vendor-neutral baseline)
        ↑
OpenAI Responses API          (Open Responses + OpenAI-specific hosted tools:
                               web search, file search, computer use, etc.)
```

OpenAI's Responses API is a **proper superset** of Open Responses. A client built for OpenAI's Responses API will work correctly against any Open Responses-compliant server for the core features. The only things that won't work are OpenAI's proprietary hosted tools — and Lenny can't implement those anyway since they require OpenAI's infrastructure.

---

## The Implication for Lenny

**One adapter covers both.** Lenny should implement the Open Responses Specification as the single `OpenResponsesAdapter`. This provides:

- Full compatibility with standard Responses API clients for all core features
- Compatibility with the broader Open Responses ecosystem — OpenRouter, Hugging Face, vLLM, Ollama, LM Studio, Vercel
- No OpenAI vendor lock-in in Lenny's codebase
- A conformance test suite to validate against

A separate `OpenAIResponsesAdapter` is only needed if Lenny wanted to implement OpenAI's proprietary hosted tool schemas — which it doesn't, since those tools are OpenAI infrastructure that Lenny neither has access to nor needs. Lenny's equivalent capability is its own connector and runtime model, exposed via Open Responses' internal tool pattern.

---

## The Internal vs External Tool Distinction

Open Responses natively supports two categories of tools: internal and external. Externally hosted tools are implemented outside the model provider's system — for example, client-side functions to be executed, or MCP servers. Internally hosted tools are within the model provider's system, where the model calls, executes, and retrieves results entirely within the provider's infrastructure, requiring no developer intervention.

This maps directly onto Lenny's architecture:

**Internal tools** in Open Responses = Lenny connectors. The gateway executes connector calls within Lenny's infrastructure and returns the result without client involvement. This is a cleaner mapping than the previous analysis suggested — the client doesn't need to implement tool execution round-trips for connectors, which resolves one of the earlier limitations.

**External tools** in Open Responses = client-side function definitions the client wants to handle themselves. Lenny supports this for `type: mcp` runtimes accessed via their dedicated MCP endpoints.

---

## Revised Adapter Inventory

The revised set is therefore:

| Adapter | Protocol | Path prefix | Notes |
|---|---|---|---|
| `MCPAdapter` | MCP | `/mcp` | Full Lenny capability surface |
| `OpenAICompletionsAdapter` | OpenAI Chat Completions | `/v1/chat/completions` | Stateless, thin wrapper |
| `OpenResponsesAdapter` | Open Responses Spec | `/v1/responses` | Covers both Open Responses and OpenAI Responses API clients |
| `A2AAdapter` | A2A | `/a2a/{runtime}` | Post-v1 |
| `AgentProtocolAdapter` | AP | `/ap/v1/agent` | Post-v1 |

The previous `OpenAIResponsesAdapter` is replaced by `OpenResponsesAdapter`. The implementation target is the Open Responses Specification, not OpenAI's proprietary superset. Any OpenAI Responses API client that doesn't rely on OpenAI-specific hosted tools works against it without modification.

The spec note on deliberate deviations is updated accordingly: Lenny implements the Open Responses core spec. OpenAI-specific hosted tools (web search, file search, code interpreter, computer use) are absent — clients that depend on those get `tool_not_found` errors. Lenny's connectors are available as internal tools per the Open Responses internal tool model.

---

## Human (Apr 1, 2026, 10:03 PM):

Ok. Just to make sure we're on the same page, when it comes to Responses API, I should have clarified there are two: OpenAI Responses API and the Open Responses Specification. Both should be supported (unless the Open Responses Spec is a superset)

---

## Claude:

Yes, confirmed — and the filtering operates at two distinct layers that are worth separating clearly in the spec.

---

## Layer 1: Visibility on `publishedMetadata`

The `publishedMetadata` field on a runtime has a `visibility` property — `internal`, `tenant`, or `public`. This controls whether metadata entries (agent cards, OpenAPI specs, etc.) are served at external endpoints. A runtime with no `public` metadata entry has nothing discoverable from outside the cluster.

But this is metadata visibility, not runtime visibility. A runtime can be fully usable by authorized clients without having any `public` metadata at all. These are separate concerns.

---

## Layer 2: Policy-Scoped Runtime Listing

`GET /v1/runtimes` (REST), `list_runtimes` (MCP), `GET /v1/models` (OpenAI-compatible adapters) — all of these return the policy-scoped set of runtimes the calling identity is authorized to create sessions for. The gateway evaluates:

1. The client's authenticated identity (tenant, user, role)
2. The client's delegation policy
3. The runtime's `visibility` configuration

A runtime that is not in the caller's delegation policy simply doesn't appear in the list. A runtime marked as internal-only doesn't appear in any external-facing listing regardless of policy. The caller only sees what they can actually use.

---

## What "Not Listed as Public" Means in Practice

The spec should distinguish three cases:

**Runtime with `visibility: internal` (or no explicit visibility declaration):** Not reachable from external clients. Doesn't appear in any external-facing list. Can only be reached from within the cluster — by other runtimes via `lenny/delegate_task`, or by operators via the admin API. An external client calling `list_runtimes` never sees it.

**Runtime with `visibility: tenant`:** Visible and usable by authorized clients of that tenant. Appears in `list_runtimes` for clients authenticated as belonging to that tenant. Not visible to clients of other tenants or unauthenticated external calls.

**Runtime with `visibility: public`:** Appears in `list_runtimes` for any authorized client regardless of tenant. The runtime's `publishedMetadata` entries marked `visibility: public` are also served at unauthenticated external URLs (agent cards, etc.).

The admin API (`GET /v1/admin/runtimes`) shows all runtimes regardless of visibility — that's the operator view. The external-facing listing tools only show what the calling client is authorized to see.

One thing worth explicitly stating in the spec: the external listing and the authorization check are the same operation. If a runtime doesn't appear in `list_runtimes`, `create_session` against it will fail with the same `not_found` error regardless of whether the runtime exists — Lenny doesn't distinguish between "doesn't exist" and "not authorized" in external-facing responses, which prevents enumeration attacks.

---

## Human (Apr 1, 2026, 10:05 PM):

Alright, thanks.

Can you confirm runtimes not listed as public don't show up in the runtimes list the gateway exposes externally?

---

## Claude:

Yes, it makes sense, and I think it's the right call. Let me reason through why.

---

## The Case For Unification

The `prompt` message type exists as a distinct type for one reason: it's the first message, which historically triggered session start. But with the new model, session start is already decoupled from message delivery — the adapter starts the runtime binary after workspace materialization, before any message arrives. The runtime then waits (in the `lenny/request_input` blocking tool call, or simply polling stdin in a loop) for its first work item.

From the runtime's perspective, there's no meaningful difference between the first message and any subsequent message. Both carry an `OutputPart[]` input. Both can include structured data, images, files references. Both may have `inReplyTo` if they're responding to something. The runtime processes them the same way — it reads the message, reasons about it, and acts.

The only behavioral difference was the implicit "this is the start" signal. But that signal is already carried by session state, not by the message type. The runtime knows it's the first message because it hasn't done anything yet — not because the message type says `prompt`.

Unifying them also resolves a subtle inconsistency in the multi-turn design. With separate `prompt` and `message` types, the runtime has to handle two different inbound message types that do the same thing. The first message is `prompt`, every subsequent one is `message`. A runtime that implements injection support has to handle `message` but the initial delivery is still `prompt`. This is arbitrary and adds unnecessary branching in every runtime implementation.

---

## The One Genuine Difference

The only thing `prompt` carries that `message` currently doesn't is the implicit signal that workspace setup is complete and the session is starting. With unification, you need to preserve this signal somewhere — otherwise the runtime can't distinguish "I just started and this is my first task" from "I'm mid-execution and someone injected context."

The right place is the session state, surfaced via a field on the message envelope:

```json
{
  "id": "msg_001",
  "type": "message",
  "sessionState": "starting",   // "starting" | "running"
  "from": { "kind": "client", "id": "user_123" },
  "input": [...]
}
```

`sessionState: "starting"` on the first message tells the runtime this is the initial task. Everything after is `sessionState: "running"`. This is richer than the old binary distinction — a runtime can use `sessionState` for initialization logic (loading context, setting up working state) without needing a separate message type.

Alternatively, and arguably cleaner, the adapter could send a separate zero-content `session_started` lifecycle notification on the lifecycle channel before the first message, keeping the message envelope entirely content-focused. The runtime then knows the session has started regardless of when the first message arrives.

---

## The Recommendation

Unify. Replace `prompt` with `message` everywhere. The first message is just the first `message` — it happens to arrive when `sessionState` is `starting`, which the runtime can use if it needs to distinguish initialization from mid-session injection. The binary protocol simplifies to one inbound content message type, which reduces the surface area of the runtime integration contract and makes Layer 0 and Layer 1 implementations easier to write correctly.

This is a breaking change to the adapter binary protocol spec, but since the protocol hasn't shipped yet and we're still in the design phase, now is exactly the right time to make it.

---

## Human (Apr 1, 2026, 10:12 PM):

Great, thanks.

One last thing on the message passing topic. Would it make sense for the initial "prompt" command to be simply a "message" command? Curious about what you think

---

## Claude:

Let me work through this systematically from pod creation through first message delivery, covering both the pre-warm phase and the session assignment phase, and noting where things differ between integration layers.

---

## Pre-Warm Phase — Pod Starts, No Session Yet

```
Kubernetes                 Adapter                    Gateway
    |                         |                           |
    |-- creates pod ---------->|                          |
    |   [sandbox + sidecar]    |                          |
    |                         |                           |
    |                         |-- opens gRPC connection ->|
    |                         |   [mTLS handshake]        |
    |                         |                           |
    |                         |-- [writes placeholder     |
    |                         |   manifest to             |
    |                         |   /run/lenny/             |
    |                         |   adapter-manifest.json]  |
    |                         |   {platformMcpServer:     |
    |                         |    {socket:"/run/lenny/   |
    |                         |     platform-mcp.sock"},  |
    |                         |   connectorServers: [],   |
    |                         |   runtimeMcpServers: []}  |
    |                         |   [connectors unknown yet]|
    |                         |                           |
    |                         |-- gRPC READY ------------>|
    |                         |                           |
    |                         |   [pod enters warm pool]  |
```

The placeholder manifest is written immediately so the path exists. It has no connector servers yet because the session's delegation lease — which determines which connectors are authorized — hasn't been assigned.

---

## Session Assignment Phase — Gateway Claims Pod

```
Gateway                    Adapter
    |                         |
    |-- gRPC PrepareWorkspace->|
    |   [stream files]         |-- [creates /workspace/staging]
    |                         |   [writes files to staging]
    |                         |
    |-- gRPC FinalizeWorkspace->|
    |                         |-- [validates staging]
    |                         |   [checks path traversal,
    |                         |    symlinks, size limits]
    |                         |   [atomically promotes to
    |                         |    /workspace/current]
    |                         |
    |-- gRPC RunSetup -------->|
    |   [setup commands]       |-- [executes each command
    |                         |    in /workspace/current]
    |                         |   [captures stdout/stderr]
    |                         |   [enforces timeout from
    |                         |    setupPolicy]
    |<- gRPC SetupComplete ----|
    |   [stdout/stderr logs]   |
    |                         |
    |-- gRPC AssignCredentials>|
    |   [lease with            |-- [stores credential lease]
    |    authorized connectors]|   [knows which connectors
    |                         |    this session may use]
    |                         |
    |-- gRPC StartSession ---->|
    |   [cwd, sessionConfig]   |
```

`AssignCredentials` is the moment the adapter learns which connectors the session is authorized for. This is what unlocks writing the final manifest.

---

## Runtime Startup Phase

```
Adapter                              Runtime Binary
    |                                      |
    |-- [writes FINAL manifest] ---------->|  (not yet running)
    |   /run/lenny/adapter-manifest.json   |
    |   {                                  |
    |    platformMcpServer: {              |
    |     socket: "/run/lenny/             |
    |      platform-mcp.sock"},            |
    |    connectorServers: [               |
    |     {id:"github",                    |
    |      socket:"/run/lenny/             |
    |       connector-github.sock"},       |
    |     {id:"jira",                      |
    |      socket:"/run/lenny/             |
    |       connector-jira.sock"}],        |
    |    runtimeMcpServers: [],            |
    |    agentInterface: {...},            |
    |    sessionId: "sess_abc",            |
    |    taskId: "task_root"               |
    |   }                                  |
    |                                      |
    |-- [spawns runtime binary] ---------->|
    |   [stdin/stdout/stderr piped]        |-- [binary starts]
    |   [lifecycle channel pipe set up]    |-- [reads manifest
    |                                      |    from well-known
    |                                      |    path]
```

The manifest is written before the binary is spawned so the runtime reads a complete, authoritative manifest from the first moment it starts. There is no race condition.

---

## Layer 0 Runtime — Minimal Path

```
Adapter                              Runtime (Layer 0)
    |                                      |
    |   [binary is running]                |
    |                                      |
    |   [adapter waits briefly for         |
    |    lifecycle channel open:           |
    |    ~2s timeout]                      |
    |                                      |
    |   [timeout expires, no lifecycle     |
    |    channel opened]                   |
    |-- [classifies as Layer 0] ---------->|
    |   [no lifecycle handshake needed]    |
    |                                      |
    |-- stdin: first message ------------->|
    |   {type:"message"                    |
    |    id:"msg_001"                      |
    |    sessionState:"starting"           |
    |    from:{kind:"client"               |
    |          id:"user_123"}              |
    |    input:[OutputPart[]]}             |
    |                                      |
    |                          [runtime reads stdin,
    |                           begins processing]
```

---

## Layer 1 Runtime — MCP Connections

```
Adapter                              Runtime (Layer 1)
    |                                      |
    |   [binary is running]                |-- [reads manifest]
    |                                      |-- [connects to
    |<- MCP connect (platform server) -----|    platform MCP
    |   [capability negotiation]           |    server socket]
    |-- MCP capabilities response -------->|
    |   [lists lenny/* tools]              |
    |                                      |
    |<- MCP connect (github connector) ----|-- [connects to
    |   [capability negotiation]           |    github connector
    |-- MCP capabilities response -------->|    socket]
    |   [lists github/* tools]             |
    |                                      |
    |   [adapter waits briefly for         |
    |    lifecycle channel: timeout]       |
    |   [no lifecycle channel opened]      |
    |-- [classifies as Layer 1] ---------->|
    |                                      |
    |-- stdin: first message ------------->|
    |   {type:"message"                    |
    |    sessionState:"starting"           |
    |    ...}                              |
    |                                      |
    |                          [runtime reads stdin,
    |                           uses MCP tools during
    |                           processing]
```

---

## Layer 2 Runtime — Full Lifecycle Handshake

```
Adapter                              Runtime (Layer 2)
    |                                      |
    |   [binary is running]                |-- [reads manifest]
    |                                      |-- [connects to MCP
    |<- MCP connect (platform + connectors)|    servers as in
    |-- MCP capability responses ---------->|   Layer 1]
    |                                      |
    |                                      |-- [opens lifecycle
    |<- lifecycle channel open ------------|    channel pipe]
    |                                      |
    |-- lifecycle_capabilities ----------->|
    |   {type:"lifecycle_capabilities"     |
    |    available:["checkpoint_request"   |
    |     "interrupt_request"              |
    |     "credentials_rotated"            |
    |     "terminate"]}                    |
    |                                      |
    |<- lifecycle_support -----------------|
    |   {type:"lifecycle_support"          |
    |    hooks:["checkpoint_ready"         |
    |           "interrupt_acknowledged"]} |
    |                                      |
    |-- [classifies as Layer 2] ---------->|
    |   [records supported hooks]          |
    |                                      |
    |-- stdin: first message ------------->|
    |   {type:"message"                    |
    |    sessionState:"starting"           |
    |    ...}                              |
```

The adapter always sends `lifecycle_capabilities` first — it doesn't wait for the runtime to initiate. The runtime responds with `lifecycle_support` declaring which hooks it implements. The first message is delivered only after this handshake completes or times out.

---

## Task Mode — Subsequent Tasks (Same Pod)

For `executionMode: task` pods reused across sequential tasks, the sequence after the first task completes:

```
Adapter                              Runtime
    |                                      |
    |   [task N completes]                 |
    |                                      |
    |-- lifecycle: terminate ------------>|
    |   {type:"terminate"                  |-- [flushes state]
    |    reason:"task_complete"}           |-- [runs cleanup
    |                                      |    commands if any]
    |<- lifecycle: terminate_ack ----------|
    |                                      |
    |-- [Lenny scrub: workspace wipe,      |
    |    tmpfs clear, env reset]           |
    |                                      |
    |-- [writes new manifest] ----------->|
    |   [updated connector list for        |
    |    new task's lease]                 |
    |                                      |
    |-- gRPC AwaitNextTask ----------->  Gateway
    |   [pod available for next task]      |
    |                                      |
    |<- gRPC AssignTask ----------------  Gateway
    |   [new credentials, new lease]       |
    |                                      |
    |-- gRPC AssignCredentials             |
    |-- gRPC PrepareWorkspace              |
    |-- gRPC FinalizeWorkspace             |
    |   [same pod, new workspace]          |
    |                                      |
    |-- stdin: first message ------------->|
    |   {type:"message"                    |
    |    sessionState:"starting"           |
    |    ...next task prompt...}           |
```

The runtime binary stays alive across tasks. It receives `terminate` with `reason: task_complete`, does its cleanup, then reads the next `{sessionState: "starting"}` message when the new task arrives. The `sessionState` field is what tells it a new task has started — the binary never knows about the pod-level scrub that happened between tasks.

---

## Summary of What the Runtime Must Do at Startup

Regardless of layer, the runtime's startup contract is:

1. Read `/run/lenny/adapter-manifest.json` — this is the only Lenny-specific thing a runtime must know about
2. Connect to declared MCP server sockets (Layer 1/2 only, optional for Layer 0)
3. Open lifecycle channel pipe (Layer 2 only, optional)
4. Begin reading from stdin

When stdin produces a `{type: "message", sessionState: "starting"}` message, that is the first task. When it produces `{type: "message", sessionState: "running"}`, it is a mid-session injection or multi-turn follow-up. The runtime decides what to do with each.

---

## Human (Apr 1, 2026, 10:15 PM):

To make sure we're not missing anything here, can you list the sequence of commands/messages between the adapter and the runtime from the moment the pod is created to the first prompt?

---

## Claude:

You're right. Let me correct that. After our conversation, the layer model was replaced with two orthogonal axes:

**Task delivery axis:**
- **Prompt mode** (renamed to just the standard mode since we unified `prompt` and `message`) — task arrives via stdin `{type: "message"}`
- **MCP mode** — task arrives via `lenny/receive_task` tool call on the runtime's own MCP server (post-v1)

**Outward capability axis** — described by the runtime's integration tier:
- **Minimum** — implements stdin/stdout binary protocol only (reads messages, emits responses and tool calls)
- **Standard** — minimum plus connects to the adapter's platform MCP server and connector servers
- **Full** — standard plus opens the lifecycle channel for checkpoint, interrupt, and credential rotation support

So in the sequence diagrams above, replace the layer references:

- "Layer 0 runtime" → **Minimum integration runtime**
- "Layer 1 runtime" → **Standard integration runtime**
- "Layer 2 runtime" → **Full integration runtime**

The lifecycle handshake sequence for Full integration is unchanged — the adapter sends `lifecycle_capabilities`, runtime responds with `lifecycle_support`. The only difference is the name used to describe each tier.

One additional correction: for a Minimum integration runtime, it doesn't connect to any MCP servers — it purely reads stdin and writes to stdout. The adapter still writes the manifest before spawning it, but the runtime ignores it. The manifest is there if the runtime wants to upgrade to Standard integration later without any adapter changes.

---

## Human (Apr 1, 2026, 10:18 PM):

we agreed not to use layer 0-2 earlier (you gave them different names after our conversation)

---

## Claude:

You're right to question it. The runtime does know — it just started. It has no prior messages, no prior state, nothing. The first message it reads from stdin is by definition the first one. `sessionState: "starting"` is redundant information that the runtime can trivially infer from its own execution state.

The only scenario where it would add value is a runtime that resumes from a checkpoint mid-session and needs to distinguish "I'm resuming" from "I'm starting fresh." But that signal is already carried by the checkpoint/resume mechanism — the adapter sends a `resume` RPC before the first message in that case, which is a different path entirely.

For task mode, the same argument applies. Between tasks the adapter sends `{type: "terminate", reason: "task_complete"}` on the lifecycle channel. The runtime processes it, does cleanup, and then the next `{type: "message"}` it reads is the start of a new task. It knows this because it processed the termination signal — not because the message carries a `sessionState` field.

So `sessionState: "starting"` should be dropped entirely. The message envelope is cleaner without it, and any runtime that needs to track whether it's at the beginning of a session can do so trivially through its own execution flow.

---

## Human (Apr 1, 2026, 10:22 PM):

I'm not sure I like `sessionState:"starting"`. What purpose does it serve? Doesn't the runtime know whether a message is the first one?

---

## Claude:

This will be the definitive compilation. Let me be thorough and precise.

---

# Lenny Technical Spec — Final Revised Changes from Design Review

## How to Read This Document

- **V1 Must-Have (Breaking)** — must be in v1; impossible to fix after the first community runtime ships
- **V1 Must-Have (Feature)** — must ship with v1 to meet stated goals
- **V1 Should-Have** — significant value; should ship but won't break compatibility if deferred
- **Planned / Post-V1** — document intent now so data models accommodate it; implement later
- **Explicit Non-Decisions** — things Lenny deliberately will not do

---

## Part 1: V1 Must-Have — Breaking Changes

---

### 1.1 Replace Custom CRDs With `kubernetes-sigs/agent-sandbox`

**What changes:** Replace `AgentPool`, `AgentPod`, and `AgentSession` CRDs and the WarmPoolController with the upstream `SandboxTemplate`, `SandboxWarmPool`, `SandboxClaim`, and `Sandbox` CRDs from `kubernetes-sigs/agent-sandbox`.

**Mapping:**

| Lenny CRD | Agent-Sandbox CRD |
|---|---|
| `AgentPool` | `SandboxTemplate` |
| `AgentPod` | `Sandbox` + `SandboxWarmPool` |
| `AgentSession` | `SandboxClaim` |
| WarmPoolController | Upstream SIG Apps controller |

**Implementation note:** Verify `SandboxClaim` optimistic-locking guarantee before committing. Task mode and concurrent mode are Lenny-layer concerns invisible to agent-sandbox.

**Affects:** Sections 4.6, 6.1, 6.2, 6.3, Phase 1 and Phase 3.

---

### 1.2 Runtime Unification: `Runtime` Replaces `RuntimeType` and `SessionTemplate`

**What changes:** `RuntimeType` and `SessionTemplate` are unified into a single `Runtime` concept. Standalone runtimes have an `image`. Derived runtimes have a `baseRuntime` and no `image`.

```yaml
# Standalone runtime
name: langgraph-runtime
image: registry.example.com/langgraph:latest
type: agent
capabilities:
  interaction: one_shot
executionMode: task
isolationProfile: sandboxed
allowedResourceClasses: [small, medium, large]
delegationPolicyRef: orchestrator-policy

# Derived runtime
name: research-pipeline
baseRuntime: langgraph-runtime
workspaceDefaults:
  files:
    - path: agent.py
      content: "..."
  setupCommands:
    - pip install -r requirements.txt
setupPolicy:
  timeoutSeconds: 300
  onTimeout: fail
agentInterface: ...
delegationPolicyRef: research-policy
publishedMetadata: ...
tags: [approved, research]
```

**Inheritance rules — never overridable on derived runtime:**
- `type`, `executionMode`, `isolationProfile`, `capabilities.interaction`, `allowedResourceClasses`, `allowStandardIsolation` acknowledgment

**Independently configurable on derived runtime:**
- Pool settings, `workspaceDefaults`, `setupCommands`, `agentInterface`, `delegationPolicyRef` (restrict only), `publishedMetadata`, `tags`, `taskPolicy`

**`setupPolicy.timeoutSeconds`:** Gateway takes the **maximum** of base and derived if both set. A derived runtime cannot set a shorter timeout than the base.

**Base runtime mutability:**
- `image` and `name` — immutable via API
- All other fields — mutable with impact validation. Changes that would invalidate existing derived runtimes are rejected with a list of affected runtimes.

**Pool independence:** Derived runtimes have fully independent pool settings. If no pool is registered, gateway falls back to base runtime's pool.

**Admin API:** `GET/POST/PUT/DELETE /v1/admin/runtimes` — handles both standalone and derived.

**Affects:** Section 5.1, Section 15.1 and 15.2, Phase 1.

---

### 1.3 `type` and `capabilities` Fields on Runtime

**What changes:** Every `Runtime` has a `type` field and an optional `capabilities` field.

```yaml
type: agent          # agent (default) | mcp
capabilities:
  interaction: one_shot    # only valid value in v1
```

**`type: agent`** — participates in Lenny's task lifecycle. Receives tasks via stdin, has sessions, workspace, delegation, elicitation, multi-turn dialog (future). Interacted with via `lenny/delegate_task`.

**`type: mcp`** — hosts an MCP server. Lenny manages pod lifecycle (isolation, credentials, workspace, pool, egress, audit). No session lifecycle, no `lenny/delegate_task`. Runtime binary is oblivious to Lenny.

**`capabilities.interaction`** is defined now as a single-value enum (`one_shot`) so adding `multi_turn` post-v1 is a non-breaking addition. Gateway rejects registrations with `interaction: multi_turn` in v1 — hard rejection, not a graceful error.

**`multi_turn` post-v1 constraint:** Multi-turn requires a protocol that supports it. A2A supports it; Lenny's native prompt delivery and AP do not. Gateway validates: `interaction: multi_turn` without a supporting `protocols` entry in `capabilities` is a registration error.

**`type: mcp` runtimes have no `capabilities` field.**

**Affects:** Section 5.1, Phase 1.

---

### 1.4 Adapter Protocol: Multi-Server MCP + Lifecycle Channel

**What changes:** The stdin/stdout JSON Lines binary protocol (Section 15.4.1) is replaced by a two-part model.

**Part A — Multiple focused local MCP servers** (intra-pod, stdio or Unix socket):

- **Platform MCP server** — Lenny-specific tools: `lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`, `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`, `lenny/request_input`, `lenny/send_message`, `lenny/get_task_tree`, `lenny/send_to_child` (stub, returns `not_implemented` in v1)
- **One MCP server per authorized connector** — each connector in the session's delegation policy gets its own independent MCP server with its own capability negotiation and auth context. **No aggregated connector proxy.** Aggregation is not lossless per MCP spec.

**No workspace MCP server.** Workspace is materialized to `/workspace/current` before the runtime starts. Runtime reads files directly from the filesystem. An MCP server adds no value here.

**Part B — Lifecycle channel** — separate stdin/stdout stream pair for operational signals:

```
Adapter → Runtime:  lifecycle_capabilities, checkpoint_request, interrupt_request,
                    credentials_rotated, terminate
Runtime → Adapter:  lifecycle_support, checkpoint_ready, interrupt_acknowledged,
                    credentials_acknowledged
```

Optional. Runtimes that don't open it operate in fallback-only mode.

**Runtime integration tiers (agent-type only):**

- **Minimum** — implements stdin/stdout binary protocol only. Reads `{type: "message"}` from stdin, writes `{type: "response"}` and `{type: "tool_call"}` to stdout. No MCP usage, no lifecycle channel. This is the floor — every agent-type runtime must implement this.
- **Standard** — minimum plus connects to adapter's platform MCP server and connector servers using the adapter manifest. Standard MCP client.
- **Full** — standard plus opens the lifecycle channel for checkpoint, interrupt, and credential rotation support.

These are orthogonal to the task delivery axis. In v1, only prompt mode (stdin/stdout) is implemented.

**Adapter manifest:** Written to `/run/lenny/adapter-manifest.json` **before the runtime binary is spawned**. The manifest is complete and authoritative when the binary starts — no race condition. Regenerated per task execution because connector authorization varies per delegation lease.

```json
{
  "platformMcpServer": {
    "socket": "/run/lenny/platform-mcp.sock"
  },
  "connectorServers": [
    { "id": "github", "socket": "/run/lenny/connector-github.sock" }
  ],
  "runtimeMcpServers": [],
  "agentInterface": { ... },
  "sessionId": "sess_abc",
  "taskId": "task_root"
}
```

`runtimeMcpServers` slot reserved from v1 even if empty — no manifest schema change needed when MCP-mode runtimes are added.

**`type: mcp` runtime adapter lifecycle:** Runtime binary is spawned at pod start (pre-warm), not at session start. Adapter polls runtime's MCP socket until capability negotiation succeeds, then signals READY to gateway. No `PrepareWorkspace`, `FinalizeWorkspace`, or per-call `AssignCredentials` in the per-call path.

**Startup sequence for `type: agent` runtimes:**

1. Pod created by Kubernetes
2. Adapter opens gRPC connection to gateway (mTLS)
3. Adapter writes placeholder manifest (no connector servers yet)
4. Adapter signals READY to gateway
5. Pod enters warm pool
6. Gateway assigns session: `PrepareWorkspace` → `FinalizeWorkspace` → `RunSetup` → `AssignCredentials` → `StartSession`
7. Adapter writes **final manifest** (connector servers now known from lease)
8. Adapter spawns runtime binary
9. Runtime reads manifest, connects to MCP servers (Standard/Full), opens lifecycle channel (Full)
10. Adapter sends `lifecycle_capabilities` (Full runtimes); receives `lifecycle_support`
11. Adapter delivers first `{type: "message"}` on stdin

**Affects:** Section 15.4 and 15.4.1 (full replacement), Section 4.7, Phase 2.

---

### 1.5 Binary Protocol: Unified `message` Type

**What changes:** The `prompt` inbound message type is removed. The `message` type handles all inbound content delivery — both the initial task and subsequent injections, follow-ups, and replies.

**`sessionState` field removed.** The runtime knows it's receiving its first message by virtue of just having started. This is inferable from execution state and does not need to be carried in the message.

**Inbound messages (adapter → runtime via stdin):**

| `type` | Description |
|---|---|
| `message` | All content delivery: initial task, mid-session injection, reply to `request_input`, sibling notification |
| `heartbeat` | Periodic liveness ping |
| `shutdown` | Graceful shutdown (terminate with no new task) |

**Outbound messages (runtime → adapter via stdout):**

| `type` | Description |
|---|---|
| `response` | Streamed or complete response carrying `OutputPart[]` |
| `tool_call` | Runtime requests a tool call |
| `status` | Optional status/trace update |

**`follow_up` renamed to `message`.** The name `follow_up` is removed everywhere. All inbound content is `{type: "message"}`.

**`input_required` outbound message removed.** Runtimes that need input call `lenny/request_input` on the platform MCP server as a **blocking tool call** instead. The runtime is suspended inside that tool call until the gateway resolves it. The `input_required` task state is set by the gateway when `lenny/request_input` is called — not triggered by a stdout message.

**Task mode between-task signaling:** For `executionMode: task`, the adapter sends `{type: "terminate", reason: "task_complete"}` on the lifecycle channel after a task completes. After scrub, the next `{type: "message"}` is the start of the new task. The runtime infers it's starting a new task from the terminate/message sequence.

**Affects:** Section 15.4.1 (binary protocol message types).

---

### 1.6 Internal `OutputPart` Format

**What changes:** `agent_text` streaming event replaced by `agent_output` carrying `OutputPart` array. `TaskResult` uses a `messages` array (see 1.8). `TaskSpec.input` uses `OutputPart` arrays.

```json
{
  "id": "part_abc123",
  "type": "text",
  "mimeType": "text/plain",
  "inline": "content here",
  "ref": "artifact://...",
  "annotations": { "role": "primary", "final": true },
  "parts": [],
  "status": "streaming | complete | failed"
}
```

- `type` is an open string — not a closed enum
- `mimeType` handles encoding separately from semantic type
- `annotations` is an open metadata map
- `parts` allows nesting
- `id` enables part-level streaming updates

SDK helper `from_mcp_content(blocks)` converts MCP content blocks to `OutputPart` arrays.

**Affects:** Section 15.4.1, Section 8.9, Section 7.2.

---

### 1.7 `MessageEnvelope` — Unified Message Format

**What changes:** All inbound content messages use a unified `MessageEnvelope`. This applies to the stdin binary protocol, the platform MCP server tools, and the REST/MCP client-facing APIs.

```json
{
  "id": "msg_xyz789",
  "from": {
    "kind": "client | agent | system",
    "id": "..."
  },
  "inReplyTo": "req_abc123",
  "threadId": "thread_001",
  "delivery": "immediate",
  "input": [OutputPart[]]
}
```

**Adapter-injected fields (runtime never supplies these):**
- `from.kind` and `from.id` — injected by the adapter from execution context. Runtimes do not supply identity fields in tool call arguments.
- `requestId` in `lenny/request_input` — generated by the gateway. Runtime only supplies `parts` (the question content).

**`delivery`** is optional and mechanical. `immediate` means deliver at next stdin read after interrupt acknowledgment if needed. Absent means queue for next natural pause. This is a platform delivery hint, not a semantic instruction — the runtime decides what to do with the content.

**`id`** — every message has a stable ID. This enables threading, reply tracking, and non-linear context across all future conversational patterns without a schema change.

**`inReplyTo`** — optional. References a specific prior message or request ID. The gateway uses this to route: if `inReplyTo` matches an outstanding `lenny/request_input` call on the target, it resolves the tool call directly instead of delivering to stdin.

**`threadId`** — optional. Groups related messages into sub-conversations. In v1 only one implicit thread per session. Future multi-thread sessions are additive.

**Forward compatibility note:** `from.kind: "user"` with distinct `from.id` per user accommodates multi-participant sessions. External A2A agent messages use `from.kind: "external"` with the A2A agent's card URL as `from.id`. These are additive — existing sessions with only `client` senders are unaffected.

**Affects:** Section 7.2, Section 15.4.1, Section 9 (platform MCP server tools).

---

### 1.8 TaskRecord Uses Messages Array + `input_required` State

**What changes:** The task record schema uses a messages array for forward compatibility with multi-turn dialog:

```json
{
  "messages": [
    { "role": "caller", "parts": [...] },
    { "role": "agent",  "parts": [...], "state": "completed" }
  ]
}
```

In v1, always exactly two messages. Future multi-turn adds more.

**Task state machine gains `input_required`:**
```
submitted → running → completed
                    → failed
                    → input_required   (defined, reachable in v1 via lenny/request_input)
```

`input_required` is reachable in v1 (not just defined). `await_child` unblocks when a child enters `input_required`, returning a partial result with the child's question and `requestId`. The parent then calls `lenny/send_message` with `inReplyTo` to resolve it.

**Affects:** Section 8.9 (TaskResult schema).

---

### 1.9 Session State Machine Gains `suspended`

**What changes:** `interrupt_request` on the lifecycle channel produces a distinct `suspended` session state:

```
running → suspended   (via interrupt_request + interrupt_acknowledged)
suspended → running   (via resume_session — no new content)
suspended → running   (via POST /v1/sessions/{id}/messages with delivery:immediate)
suspended → completed (via terminate)
```

`suspended` is meaningful: pod is held, workspace preserved, session waits. A session in `suspended` state does not time out against `maxSessionAge` (timer paused). This is the correct model for human review gates.

**`interrupt_request` on lifecycle channel is a distinct primitive from message injection.** It represents pause-and-decide with decoupled timing — stop now, decide what to do separately. This is different from `delivery: "immediate"` in a message, which atomically interrupts and delivers content.

**Affects:** Section 7.2 (session state machine), Section 6.1.

---

### 1.10 Single `lenny/delegate_task` Tool

**What changes:** One delegation tool. Target id is opaque — the runtime does not know whether the target is a standalone runtime, derived runtime, or external registered agent.

```
lenny/delegate_task(
  target: string,        // runtime name — unique across all runtimes and external agents
  task: TaskSpec,
  lease_slice?: LeaseSlice
) → TaskHandle
```

`TaskSpec` carries two separate input channels:

```json
{
  "input": [OutputPart[]],
  "workspaceFiles": {
    "export": [{ "glob": "src/auth/**", "destPrefix": "/" }]
  }
}
```

`input` is the prompt/context. `workspaceFiles` is the file export spec. Separate because they serve different purposes.

**`lenny/delegate_task` rejects `type: mcp` targets** with `target_not_an_agent`. `external_delegate` is removed — external agents are registered as connectors, reachable by the same tool. No `external_delegate` tool.

**Affects:** Section 8.2, Section 8.9, Section 9.1.

---

### 1.11 Gateway-Mediated Session Messaging

**What changes:** All inter-session communication is gateway-mediated. No direct pod-to-pod communication. The gateway validates authorization, enforces policy, records audit events, and routes messages.

**Three platform MCP tools:**

```
lenny/send_message(
  to: string,              // taskId — unique across all sessions in task tree
  message: MessageEnvelope
) → void
```

```
lenny/request_input(
  parts: OutputPart[]      // the question content
) → MessageEnvelope        // blocks until answer arrives
                           // gateway generates requestId,
                           // injects into parent's await event
```

```
lenny/get_task_tree() → TaskTreeNode[]
```

Returns the calling session's position in the task tree:
```json
{
  "self": { "taskId": "task_001", "sessionId": "sess_abc" },
  "parent": { "taskId": "task_root", "sessionId": "sess_parent" },
  "children": [...],
  "siblings": [
    { "taskId": "task_002", "sessionId": "sess_xyz",
      "state": "running", "runtime": "analyst-agent" }
  ]
}
```

**`taskId` is the messaging address** — not `sessionId`. Task IDs are stable across pod recovery generations. Session IDs are available for audit and artifact queries.

**Message delivery routing — three paths:**

1. **`inReplyTo` matches outstanding `lenny/request_input`** → gateway resolves the blocked tool call directly. No stdin delivery, no interrupt. The runtime's `lenny/request_input` call returns with the message as its value.
2. **No matching pending request, runtime available** → `{type: "message"}` delivered to stdin at next read.
3. **No matching pending request, runtime blocked in `await_children`** → message buffered in inbox. Delivered before the next `await_children` event (inbox drained first).

**Authorization:** The gateway validates the sender is in the same task tree as the recipient, the recipient session has `capabilities.injection.supported: true`, and the delivery mode is supported by the recipient.

**`lenny/send_to_child` and `lenny/notify_sibling` are removed.** Both are replaced by `lenny/send_message`. **`lenny/request_input` replaces the stdout `input_required` message type.**

**Affects:** Section 8.2, Section 9 (platform MCP server tools), Section 7.2.

---

### 1.12 `await_children` Unblocks on `input_required`

**What changes:** `lenny/await_children` (and `lenny/await_child`) unblock when a child enters `input_required` state, not only on terminal states. The partial result carries the child's question:

```json
{
  "taskId": "task_child_001",
  "status": "input_required",
  "requestId": "req_001",
  "parts": [{ "type": "text", "inline": "Which time period?" }]
}
```

The parent calls `lenny/send_message` with `inReplyTo: "req_001"` to resolve it, then calls `lenny/await_children` again.

The gRPC `AwaitChildren` call to the gateway is a **streaming response** — it yields partial events (including `input_required`) before the final settled result.

**Affects:** Section 8.9, Section 9 (await_children tool spec).

---

### 1.13 `DelegationPolicy` as First-Class Resource

**What changes:** `allowedRuntimes`, `allowedConnectors`, and `allowedPools` replaced by named `DelegationPolicy` resources.

```yaml
id: orchestrator-policy
rules:
  runtimes:
    allow:
      ids: ["review-agent"]
      tags: ["approved"]
  connectors:
    allow:
      ids: ["github", "jira"]
  externalAgents:
    allow: []
```

**Two policy levels:**
- **Runtime-level policy** (Helm/config, deployment time): tag-based rules. Set before derived runtimes exist.
- **Derived runtime policy** (admin API, post-deployment): can only restrict base's policy. `effective_policy = base_policy ∩ derived_policy`.

**Dynamic tag evaluation:** Tags evaluated at delegation time. New runtimes tagged `approved` after session start are automatically accessible.

**Session-level override:** `maxDelegationPolicy` at session creation further restricts. Effective = `runtime_policy ∩ session_max_policy`.

**Discovery scoping:** `lenny/discover_agents` returns only what the session's effective policy permits.

**`lenny/discover_agents`** returns `type: agent` runtimes and external agents only. `type: mcp` runtimes do not appear — they are not callable via `lenny/delegate_task` and are discovered via the adapter manifest and `GET /v1/runtimes`.

**Affects:** Section 8.3, Section 5.1.

---

### 1.14 `executionMode` on Pool Configuration (All Three in V1)

**What changes:** Pool configuration has `executionMode` from day one. **All three modes are implemented in v1:**

```yaml
executionMode: session | task | concurrent
```

**Graph mode is removed** as a separate execution mode. Graph-aware runtimes are session-mode runtimes that optionally emit internal step traces through the observability protocol.

**Affects:** Section 5.2, Phase 1.

---

### 1.15 `agentInterface` Field on Runtime

**What changes:** `type: agent` runtimes gain an optional `agentInterface` field.

```yaml
agentInterface:
  description: "Analyzes codebases and produces refactoring plans"
  inputModes:
    - type: "text/plain"
    - type: "image/png"
    - type: "application/json"
      schema: "{ 'skill': 'string', 'path': 'string' }"
  outputModes:
    - type: "text/plain"
      role: "primary"
  supportsWorkspaceFiles: true
  skills:
    - id: "review"
      name: "Code Review"
      description: "Reviews code for quality and correctness"
  examples:
    - description: "Review auth module"
      input: "Review the authentication module"
```

`supportsWorkspaceFiles: true` signals the target accepts `workspaceFiles` in `TaskSpec`. External agents always have `supportsWorkspaceFiles: false`.

Serves three purposes simultaneously: discovery results, A2A card auto-generation, adapter manifest summaries.

`type: mcp` runtimes do not have `agentInterface`. Their capabilities are discovered via standard MCP capability negotiation at `/mcp/runtimes/{name}`.

**Affects:** Section 5.1.

---

### 1.16 `publishedMetadata` on Runtime

**What changes:** Both standalone and derived `Runtime` resources gain a `publishedMetadata` array.

```yaml
publishedMetadata:
  - key: agent-card
    contentType: application/json
    visibility: public    # internal | tenant | public
    value: '...'
```

**Visibility semantics:**
- `internal` — intra-cluster, requires valid JWT
- `tenant` — filtered by `tenant_id` claim
- `public` — no auth required, externally reachable

**External listing visibility:** A runtime not visible to a calling client does not appear in `list_runtimes`, `GET /v1/runtimes`, or `GET /v1/models`. The gateway returns the same `not_found` error for "doesn't exist" and "not authorized" — no enumeration attacks.

**Affects:** Section 5.1.

---

### 1.17 `ExternalProtocolAdapter` as Registry (Not Single Adapter)

**What changes:** The gateway's external protocol handling becomes an `ExternalAdapterRegistry` with simultaneously active adapters routing by path prefix.

```go
type ExternalProtocolAdapter interface {
    Protocol()    string
    PathPrefix()  string
    HandleRequest(ctx context.Context,
                  w http.ResponseWriter,
                  r *http.Request,
                  dispatcher SessionDispatcher) error
    HandleDiscovery(ctx context.Context,
                    w http.ResponseWriter,
                    r *http.Request,
                    runtimes []AuthorizedRuntime) error
    Capabilities() AdapterCapabilities
}
```

**`HandleDiscovery` is required on all adapters.** Every adapter must translate Lenny's policy-scoped runtime list into its protocol's native discovery format. An adapter that cannot express runtime discovery documents this explicitly in its `AdapterCapabilities`.

**Three tiers of pluggability:**
- **Built-in** (compiled in): MCP, OpenAI Completions, Open Responses. Always available, configurable via admin API.
- **Config-driven**: deployer points gateway at a Go plugin binary or gRPC service at startup.
- **Runtime registration via admin API**: `POST /v1/admin/external-adapters` — takes effect immediately, no restart.

**Enabling/disabling adapters:**
```
PUT /v1/admin/external-adapters/openai-completions
{ "enabled": false }
```

**Built-in adapter inventory:**

| Adapter | Path prefix | Protocol |
|---|---|---|
| `MCPAdapter` | `/mcp` | MCP Streamable HTTP |
| `OpenAICompletionsAdapter` | `/v1/chat/completions` | OpenAI Chat Completions |
| `OpenResponsesAdapter` | `/v1/responses` | Open Responses Specification |
| `A2AAdapter` | `/a2a/{runtime}` | A2A (post-v1) |
| `AgentProtocolAdapter` | `/ap/v1/agent` | AP (post-v1) |

**Affects:** Section 4.1, Section 15, Phase 1.

---

### 1.18 `type: mcp` Runtime Dedicated MCP Endpoints

**What changes:** For each enabled `type: mcp` runtime, the gateway serves a dedicated MCP endpoint at `/mcp/runtimes/{runtime-name}`. Standard MCP capability negotiation. Gateway proxies: external client → gateway → adapter → runtime MCP server socket.

Gateway enforces authorization, rate limits, quota, egress profiles, and audit logging on every tool call. A `type: mcp` connection creates an implicit session record for audit and billing.

**Not aggregated.** Each runtime gets its own endpoint. This is consistent with the aggregation-is-not-lossless conclusion.

**Discovery:** `GET /v1/runtimes` and `list_runtimes` return `mcpEndpoint: "/mcp/runtimes/{name}"` for `type: mcp` runtimes. The `mcpCapabilities.tools` field provides a lightweight preview; full schemas come from connecting and calling `tools/list`.

**Affects:** Section 4.1 (gateway routing), Section 9, Section 15.

---

### 1.19 Comprehensive Admin API

**What changes:** Full admin API surface. Primary configuration store is Postgres, cached with short TTL in gateway. Redis pub/sub for immediate invalidation.

**Operational plane — API-managed:**
- Runtimes: `GET/POST/PUT/DELETE /v1/admin/runtimes`
- Delegation Policies: `GET/POST/PUT/DELETE /v1/admin/delegation-policies`
- Connectors: `GET/POST/PUT/DELETE /v1/admin/connectors`
- Pool Configuration: `GET/POST/PUT/DELETE /v1/admin/pools`
- Credential Pools (metadata only): `GET/POST/PUT/DELETE /v1/admin/credential-pools`
- Tenants: `GET/POST/PUT/DELETE /v1/admin/tenants`
- Quotas: `GET/PUT /v1/admin/tenants/{id}/quotas`
- User Role Assignments: `GET/POST/DELETE /v1/admin/tenants/{id}/users`
- Egress Profiles: `GET/POST/PUT/DELETE /v1/admin/egress-profiles`
- Experiment Definitions: `GET/POST/PUT/DELETE /v1/admin/experiments`
- Scaling Policies: `GET/PUT /v1/admin/pools/{id}/scaling-policy`
- Memory Store Config: `GET/PUT /v1/admin/memory-store`
- Webhooks: `GET/PUT /v1/admin/webhooks`
- External Adapters: `GET/POST/PUT/DELETE /v1/admin/external-adapters`

**Bootstrap plane — Helm/config only:** DB URLs, Redis, MinIO, KMS, cluster name, namespace assignments, certificate paths, `LENNY_DEV_MODE`, system-wide defaults, Kubernetes object definitions.

**CRDs become derived state.** Reconciled from Postgres by PoolScalingController. `kubectl edit` is not the correct way to change pool settings.

**Admin API design constraints:** same error taxonomy, pagination, OIDC auth as client-facing API; `etag` for conditional PUT; `?dryRun=true` on all mutations; OpenAPI spec generated from Go handlers; all mutations in audit log; every operational action expressible via API.

**Affects:** Section 15.1, Section 10.2, Section 11.7.

---

## Part 2: V1 Must-Have — Feature Changes

---

### 2.1 Task Pod Execution Mode

**What changes:** `executionMode: task` reuses pods across sequential tasks. Between tasks: cleanup commands run, Lenny scrubs workspace, pod becomes available.

**Task mode lifecycle:**
```
task completes
    → adapter sends terminate(reason:task_complete) on lifecycle channel
    → runtime acknowledges
    → taskPolicy.cleanupCommands execute (if defined)
    → Lenny scrub (workspace wipe, tmpfs clear, env reset)
    → adapter writes new manifest (updated connector list for new lease)
    → pod transitions back to available
```

**`taskPolicy` on runtime:**
```yaml
taskPolicy:
  cleanupCommands:
    - pkill -f jupyter_kernel
    - rm -rf /tmp/sandbox-*
  cleanupTimeoutSeconds: 30
  onCleanupFailure: warn    # warn (default) | fail
```

Cleanup commands run before scrub — they can access task state. `onCleanupFailure: fail` drains pod and creates replacement (for leak prevention). `setupCommands` run once per pod at start, not per task.

**Security requirement:** Explicit deployer acknowledgment required.

**Affects:** Sections 6.1, 6.2, 7, 5.2.

---

### 2.2 Concurrent Execution Mode

**What changes:** `executionMode: concurrent` allows multiple simultaneous task slots per pod. For runtimes with expensive shared in-memory state.

```yaml
executionMode: concurrent
concurrencyStyle: stateless    # stateless | workspace
maxConcurrent: 8
```

**`concurrencyStyle: workspace`** — each slot gets its own workspace. Gateway tracks per-slot lifecycle. Isolation is process-level and filesystem-level — explicitly weaker than session mode. Deployer acknowledgment required.

**`concurrencyStyle: stateless`** — no workspace materialization. Gateway routes through Kubernetes Service. Pod readiness probe reflects slot availability. PoolScalingController watches `active_slots / (pod_count × maxConcurrent)`.

**Why deploy stateless inside Lenny (not as an external connector):** pod-level isolation, credential leasing, per-tenant egress control, request-level audit trail, warm pool management. If none of these apply, register as an external connector instead.

**`type: mcp` runtimes in concurrent mode:** `taskPolicy.cleanupCommands` run per-slot after each slot's task completes.

**Affects:** Sections 5.2, 6.1, 6.2, 4.1.

---

### 2.3 `setupPolicy` on Runtime

**What changes:** Replaces the incorrectly described `prewarmLeadMinutes` timeout concept. Readiness is driven by the adapter's READY signal after setup commands complete. Timeout is a safety net only.

```yaml
setupPolicy:
  timeoutSeconds: 300      # optional — waits indefinitely if absent
  onTimeout: fail          # fail (default) | warn
```

Gateway takes the **maximum** of base and derived `timeoutSeconds` if both set.

**Affects:** Section 5.1, Section 6.2.

---

### 2.4 `ConnectorDefinition` as First-Class Resource

**What changes:** Connector configuration is a first-class admin API resource. External agents (A2A, AP connectors) are registered here alongside tool connectors. The `protocol` field distinguishes them. External agent IDs participate in the same flat namespace as runtimes.

**Affects:** Section 4.3, Section 9.3.

---

### 2.5 Runtime Discovery on All External Interfaces

**What changes:** Every external interface exposes runtime discovery in its protocol's native form:

- **MCP:** `list_runtimes` tool (new)
- **REST:** `GET /v1/runtimes` — updated to return full `agentInterface` and `mcpEndpoint` fields
- **OpenAI Completions:** `GET /v1/models` — returns Lenny runtimes as OpenAI model objects
- **Open Responses:** `GET /v1/models` — same as Completions; `type: mcp` runtimes additionally expose tools via the dedicated MCP endpoints

**`list_runtimes` response includes:**
- `name`, `kind` (standalone | derived), `baseRuntime`, `type` (agent | mcp)
- `agentInterface` (for `type: agent`)
- `mcpEndpoint` and `mcpCapabilities.tools` preview (for `type: mcp`)
- `capabilities`

**Visibility filtering:** Policy-scoped. Not-found and not-authorized produce identical responses — no enumeration.

**Affects:** Section 9, Section 15.1, Section 15.2.

---

### 2.6 OpenAI Completions Adapter

**What changes:** Implements `POST /v1/chat/completions`. Stateless, ephemeral sessions. `model` parameter maps to Lenny runtime via configurable mapping in admin API.

**Tool handling — opaque mode only.** Agent handles its own tool execution internally. Client receives only the final text output. Client-provided `tools` definitions map to authorized connectors; no `tool_calls` are returned to the client.

**Runtime discovery:** `GET /v1/models` returns available runtimes as model objects.

**Affects:** Section 15 (new subsection).

---

### 2.7 Open Responses Adapter

**What changes:** Implements the **Open Responses Specification** (open standard, vendor-neutral, January 2026). This covers both Open Responses-compliant clients and OpenAI Responses API clients for all core features.

OpenAI's Responses API is a proper superset of Open Responses. The difference is OpenAI's proprietary hosted tools (web search, file search, computer use) — which Lenny doesn't implement anyway. One adapter covers both.

**Key mappings:**
- `background: true` → async Lenny session, returns response ID immediately
- `previous_response_id` → `POST /v1/sessions/{id}/messages` (for running session) or `POST /v1/sessions/{id}/derive` (for completed session)
- `store: true` → always true in Lenny; field ignored
- Background polling window → no 10-minute limit; Lenny sessions run for their configured `maxSessionAge`
- Lenny connectors → Open Responses **internal tools** (executed within provider infrastructure, no client round-trip)
- Client-defined functions → Open Responses **external tools** (yielded back to client) — not supported in v1

**Affects:** Section 15 (new subsection).

---

### 2.8 PoolScalingController

**What changes:** Pool scaling intelligence extracted into dedicated `PoolScalingController` separate from `WarmPoolController`. Backed by pluggable `PoolScalingStrategy` interface.

**Default formula:**
```
target_minWarm = ceil(base_demand_p95 × variant_weight × safety_factor)
```

`safety_factor` defaults to 1.5 for agent-type pools, 2.0 for mcp-type pools. `scaleToZero` disabled by default for `type: mcp` pools.

**Pool phases:** pre-warm, ramp, steady state, wind-down — all automatic based on experiment status transitions.

**Affects:** Section 4.6, Section 5.2.

---

### 2.9 Experiment Primitives

**What changes:** `ExperimentDefinition` as first-class admin API resource. `ExperimentRouter` as built-in `RequestInterceptor`. Experiment context propagates through delegation leases via `propagation.childSessions: inherit`. Results API aggregates eval scores by variant.

**`ExperimentDefinition` key fields:** `status` (scheduled|active|paused|concluded), `baseRuntime`, `variants` with `weight`, `targeting` (percentage|cohort|combined), `sticky` (user|session|none), `ramp` policy, `propagation`.

**PoolScalingController** manages variant pool lifecycle automatically based on experiment status transitions. No manual intervention at any phase.

**Affects:** Section 4.8, Section 15.1, Section 11.2.

---

### 2.10 Injection Capability Declaration on Runtime

**What changes:** `type: agent` runtimes declare whether they support message injection:

```yaml
capabilities:
  interaction: one_shot
  injection:
    supported: true          # default: false
    modes: [immediate, queued]
```

Gateway rejects `POST /v1/sessions/{id}/messages` and `lenny/send_message` calls targeting sessions whose runtime has `injection.supported: false`. This is enforced at the gateway level.

**`interaction: one_shot` runtimes** cannot receive injections structurally — they run to completion from a single prompt. Gateway rejects injection attempts at the API level before they reach the adapter.

**Affects:** Section 5.1, Section 7.2.

---

### 2.11 External Session Messaging (Client-Facing)

**What changes:** `POST /v1/sessions/{id}/messages` is the unified client-facing endpoint for all message types: initial prompt (delivered when session is starting), mid-session injection, and responses to elicitation.

```json
POST /v1/sessions/sess_abc/messages
{
  "message": {
    "id": "msg_c01",
    "from": { "kind": "client", "id": "user_123" },
    "inReplyTo": "req_001",
    "delivery": "immediate",
    "input": [OutputPart[]]
  }
}
```

**MCP equivalent:** `send_message` tool on the external MCP interface.

**Authorization:** Only the session's creating client can inject. Rate-limited per session (configurable, default 10 per minute). All injections audit-logged with client identity.

**Interrupt endpoint** remains separate: `POST /v1/sessions/{id}/interrupt` — this is a lifecycle signal, not content delivery.

**Affects:** Section 7.2, Section 15.1.

---

## Part 3: V1 Should-Have

---

### 3.1 `MemoryStore` Interface

**What changes:** `MemoryStore` role-based storage interface. Default: Postgres + pgvector. Fully replaceable. Technology choice explicitly deferred — market not settled Q1 2026. Accessed via `lenny/memory_write` and `lenny/memory_query` tools in platform MCP server.

**Affects:** Section 4, Section 9.

---

### 3.2 Semantic Caching at the LLM Proxy

**What changes:** Optional `CachePolicy` on `CredentialPool`. Backed by pluggable `SemanticCache` interface — default Redis, fully replaceable. Disabled by default.

**Affects:** Section 4.9.

---

### 3.3 `RequestInterceptor` Extension Point

**What changes:** Formalized interceptor interface at phases: `PreAuth`, `PostAuth`, `PreRoute`, `PostRoute`, `PreToolResult`, `PostAgentOutput`. Built-in interceptors: `ExperimentRouter`, `GuardrailsInterceptor` (disabled by default — no built-in guardrail logic).

**Affects:** Section 4.8.

---

### 3.4 Agent Evaluation Hooks

**What changes:** `POST /v1/sessions/{id}/eval` accepts scored results from external tools. `POST /v1/sessions/{id}/replay` re-runs against different runtime version. No built-in eval logic.

**Affects:** Section 15.1, Section 16.

---

### 3.5 `CredentialRouter` Interface

**What changes:** Credential pool selection logic becomes pluggable `CredentialRouter` interface. Default: existing least-loaded/round-robin/sticky-until-failure. Disabled by default, opt-in per pool.

**Affects:** Section 4.9.

---

## Part 4: Planned / Post-V1

---

### 4.1 Multi-Turn Agent Dialog

**What to document now:**
- `interaction: multi_turn` reserved in capabilities enum, not valid in v1 (hard rejection)
- Multi-turn requires a protocol that supports it — A2A does, Lenny prompt delivery and AP do not
- `capabilities.protocols: [a2a]` is required alongside `interaction: multi_turn`
- `lenny/send_to_child` stub returns `not_implemented` in v1; activates when multi-turn ships
- Binary protocol `{type: "message"}` is the delivery vehicle when multi-turn activates

---

### 4.2 A2A Full Support

**What to document now:**
- Inbound: gateway serves `POST /a2a/{runtimeName}/tasks` via `A2AAdapter`
- Outbound: external A2A agents registered as connectors, callable via `lenny/delegate_task`
- A2A `input-required` state maps directly to Lenny's `input_required` task state
- `agentInterface` auto-generates A2A agent cards
- Per-agent A2A endpoints (not aggregated) at `/a2a/runtimes/{name}`
- `allowedExternalEndpoints` slot on delegation lease schema must exist from v1
- Runtime authors who want intra-pod A2A client behavior must explicitly implement it

---

### 4.3 A2A Intra-Pod Support (Post-A2A Gateway)

**What to document now:**
- Adapter additionally serves per-agent A2A endpoints intra-pod
- Adapter manifest gains A2A base URL alongside MCP server endpoints
- All A2A traffic proxied through adapter and gateway
- Runtime authors opt in explicitly

---

### 4.4 MCP Task Delivery Mode (Post-V1)

**What to document now:**
- `taskDelivery: mcp` on runtime configuration — task arrives via `lenny/receive_task` tool call on runtime's own MCP server
- Adapter manifest `runtimeMcpServers` slot is reserved from v1
- No `lenny/receive_task` tool required in v1

---

### 4.5 Agent Protocol (AP) Support

**What to document now:** AP defines `POST /ap/v1/agent/tasks` and step execution. Third `ExternalProtocolAdapter` implementation. No changes to intra-pod model.

---

### 4.6 Future Conversational Patterns

**What to document now:** The `MessageEnvelope` with `id`, `from`, `inReplyTo`, `threadId` accommodates all of these without schema changes:
- Threaded messages (`inReplyTo` on general messages, not just `request_input` replies)
- Multiple participants (`from.kind: "user"` with distinct `from.id`)
- Non-linear context (message IDs enable retrieval by ID, not just linear stream)
- Broadcast (`lenny/send_message` to `"broadcast"` expanding to all running siblings — additive)
- External agent participation (`from.kind: "external"` with A2A agent URL as ID)

---

### 4.7 UI and CLI

**What to document now:** Admin API is the complete operational surface. Official CLI (`lenny-ctl`) and web portal are separate projects consuming the admin API as thin clients with zero business logic.

---

## Part 5: Explicit Non-Decisions

---

### 5.1 No Model B Runtime Deployment

No mechanism for packaging a graph definition as a new registered runtime. Users register derived runtimes via admin API.

### 5.2 No Built-In Eval Logic

Lenny provides hooks and storage. No LLM-as-judge, hallucination detection, or evaluation logic.

### 5.3 No Built-In Guardrail Logic

Lenny provides the `RequestInterceptor` hook. No content classifiers or prompt injection detection.

### 5.4 No Built-In Memory Extraction

Lenny provides the `MemoryStore` interface and tools. Runtimes decide what to write.

### 5.5 Hooks-and-Defaults Design Principle

Every cross-cutting AI capability (memory, caching, guardrails, evaluation, routing, scaling, discovery) follows the same pattern: defined as an interface with a sensible default, disabled unless explicitly enabled, fully replaceable. Lenny never implements AI-specific logic.

---

## Part 6: Revised Build Sequence

**Phase 1** — Core types: `Runtime` (not `RuntimeType`/`SessionTemplate`), `type` field, `capabilities` with `interaction: one_shot`, `executionMode` field, `allowedExternalEndpoints` on delegation lease, `input_required` task state, messages array in TaskRecord, `suspended` session state. Agent-sandbox CRDs.

**Phase 2** — Replace adapter binary protocol: unified `{type:"message"}` (no separate `prompt`), multi-server MCP adapter + lifecycle channel, adapter manifest at `/run/lenny/adapter-manifest.json` written before binary spawns. Publish as runtime adapter specification.

**Phase 3** — PoolScalingController. `DelegationPolicy` resource. `setupPolicy` enforcement. `taskPolicy.cleanupCommands`.

**New Phase between 4 and 5** — Admin API foundation: runtimes, pools, connectors, delegation policies, tenant management, external adapters registry. Gateway loads config from Postgres.

**Phase 5** — Gateway gains `ExternalAdapterRegistry`. MCP adapter, Completions adapter, Open Responses adapter all active. `list_runtimes`, `GET /v1/runtimes`, `GET /v1/models` all implemented. `type: mcp` runtime endpoints at `/mcp/runtimes/{name}`.

**Phase 9** — `lenny/delegate_task` handles internal and external targets. `lenny/send_message`, `lenny/request_input`, `lenny/get_task_tree` in platform MCP server. `lenny/discover_agents` with full policy scoping.

**Phase 10** — `agentInterface` integration in discovery. Adapter manifest includes summaries.

**Phase 12** — `type: mcp` runtime support: adapter proxy, pool management, audit logging per tool call. Concurrent execution modes.

**New Phase 16** — Experiment primitives, PoolScalingController experiment integration.

**New Phase 17** — Memory, caching, guardrail, eval hooks.

---

## Summary

| Tier | Count |
|---|---|
| V1 Must-Have Breaking | 19 |
| V1 Must-Have Feature | 11 |
| V1 Should-Have | 5 |
| Planned / Post-V1 | 7 |
| Explicit Non-Decisions | 5 |

---

## Human (Apr 2, 2026, 10:03 PM):

Revise your previous compilation of updates to the tech spec based on all we have talked about.

---

## Claude:

Good refinements. Let me work through each point and then propose a cleaner unified model.

---

## Two Distinct Mechanisms, Not One

Your comments actually describe two related but distinct needs:

**Transparent identity-based filtering** — the user connects to the standard endpoint (`/mcp`, `/v1`, `/v1/responses`) and sees only what their identity is authorized to see, automatically. No special endpoint. The union of all environments the user's groups have access to determines the view. This is exactly how Kubernetes RBAC works: `kubectl get pods` returns what your identity permits — you don't connect to a separate endpoint for "your team's pods."

**Environment-scoped endpoints** — an explicit `/mcp/environments/{name}` (or `/v1/environments/{name}`) for when a client wants to deliberately constrain its view to a specific context. Useful for CI/CD pipelines, agent orchestrators, or clients that want strict isolation — "I am operating in the security project, show me nothing else."

Both should exist. The transparent path is the default and the right behavior for human users and LLM-driven clients. The scoped endpoint is an explicit opt-in for automated systems that want deterministic scoping. And yes — the scoped endpoint must apply uniformly across all external interfaces: MCP, REST, Open Responses, OpenAI Completions. It's a gateway-level routing decision, not a per-adapter feature.

---

## Tag-Based Selectors for Runtime Membership

Explicit lists in environment definitions are a maintenance problem. New runtimes get added and the environment definition immediately goes stale. Tag-based selectors are the right model — the same pattern Kubernetes uses for pod selection:

```yaml
name: security-team
runtimeSelector:
  matchLabels:
    team: security
  matchExpressions:
    - key: approved
      operator: In
      values: ["true"]
  types: [agent, mcp]          # optional type filter
connectorSelector:
  matchLabels:
    team: security
```

A runtime tagged `team: security` and `approved: true` automatically becomes part of this environment. Remove the tag, it leaves. No environment definition update required.

Explicit inclusion/exclusion overrides are still available for fine-grained control:

```yaml
runtimeSelector:
  matchLabels:
    team: security
  exclude:
    - experimental-scanner      # even if tagged correctly, explicitly excluded
  include:
    - legacy-auditor            # even if not tagged correctly, explicitly included
```

This follows the same inclusion/exclusion model as Kubernetes NetworkPolicy.

---

## Tool-Level Granularity Inside MCP Runtimes

For `type: mcp` runtimes, environment membership is not binary. A `code-executor` runtime may expose `execute_python`, `execute_bash`, `read_file`, `write_file` — and the security team should be able to call `read_file` but not `write_file`. The environment definition needs a tool filter:

```yaml
name: security-team
runtimeSelector:
  matchLabels:
    team: security
mcpToolFilters:
  - runtimeSelector:
      matchLabels:
        type: execution
    allowedTools:
      - execute_python
      - read_file
    deniedTools:
      - execute_bash           # too broad for this environment
      - write_file
```

The gateway enforces this at the proxy layer when routing tool calls through `/mcp/runtimes/{name}`. A tool call for a denied tool returns `tool_not_found` — indistinguishable from a tool that doesn't exist, same non-enumeration principle as runtime visibility.

This is the same principle already applied to delegation policies restricting which connectors an agent can use — but applied at the tool level for external clients.

---

## On Connectors Exposed Externally

To recap where we are: connectors are currently session-internal. An external client creates a session, the session gets a delegation lease that includes authorized connectors, and the agent pod calls those connectors internally via the platform MCP server. The external client never calls a connector directly.

The question is whether connectors should also be accessible directly from outside, bypassing the session model entirely — essentially turning Lenny into an OAuth-managed MCP proxy. This is a meaningful capability (a human user using Claude Desktop wants to call GitHub tools through Lenny's credential management without spinning up an agent session), but it changes the credential model fundamentally. Currently credentials are session-scoped and short-lived. Direct external access would require user-scoped OAuth tokens managed by Lenny — a different flow.

My recommendation: **keep this as a separate design decision rather than coupling it to the environment model.** If Lenny later adds direct connector access for external clients, environment scoping applies to it — `connectorSelector` in an environment definition already expresses which connectors belong to that environment, so the model is ready. But whether to implement direct external connector access is a broader product question.

If the answer is yes, connectors would be accessible at `/mcp/connectors/{name}` (same pattern as runtimes), and environment scoping works exactly as it does for `type: mcp` runtimes.

---

## Identity Integration: LDAP and OIDC

Yes, LDAP integration is required if groups live in LDAP or Active Directory. The practical implementation path is via an OIDC bridge rather than direct LDAP — LDAP is a directory protocol, not an authentication protocol, so Lenny should speak OIDC and let an identity provider (Keycloak, Okta, Dex, Azure Entra ID) handle the LDAP-to-JWT translation.

The JWT carries group claims. The gateway reads the `groups` claim and resolves environment memberships from those groups. The environment's RBAC maps LDAP group DNs or short names to roles:

```yaml
name: security-team
members:
  - identity:
      type: oidc-group
      value: cn=security-engineers,ou=groups,dc=acme,dc=com
    role: creator
  - identity:
      type: oidc-group
      value: cn=security-leads,ou=groups,dc=acme,dc=com
    role: admin
  - identity:
      type: oidc-user
      value: alice@acme.com
    role: creator
```

`type: oidc-group` maps to whatever the OIDC provider puts in the `groups` claim — could be a full LDAP DN, a short name, a UUID depending on the provider. The gateway validates group membership from the JWT at request time without a secondary LDAP lookup (the OIDC provider handles that at token issuance).

Caching: group membership is in the JWT and therefore cached for the token lifetime. For environments where group membership changes need to propagate immediately (e.g. a user leaves a team), the token TTL needs to be short enough, or Lenny needs token introspection. This is a standard OIDC trade-off and should be documented rather than solved in the spec.

---

## The Cleaned-Up Environment Model

Putting it all together, an environment is:

```yaml
name: security-team
tenant: acme-corp
description: "Security engineering workspace"

# Who belongs to this environment and with what role
members:
  - identity: { type: oidc-group, value: "security-engineers" }
    role: creator        # can create sessions and call type:mcp tools
  - identity: { type: oidc-group, value: "security-leads" }
    role: admin          # can modify environment definition

# Which runtimes are in scope (tag-based, not explicit list)
runtimeSelector:
  matchLabels:
    team: security
  types: [agent, mcp]

# Tool-level filters for type:mcp runtimes
mcpToolFilters:
  - runtimeSelector: { matchLabels: { category: execution } }
    allowedTools: [execute_python, read_file]

# Which connectors are in scope
connectorSelector:
  matchLabels:
    team: security

# Optional: explicit environment endpoint
# If absent, environment is only accessible via identity-filtered standard endpoints
endpoint:
  enabled: true
  path: security-team     # accessible at /mcp/environments/security-team,
                          # /v1/environments/security-team/sessions, etc.
```

**Transparent filtering at standard endpoints:** When a user authenticated as member of `security-engineers` calls `GET /v1/runtimes`, the gateway evaluates all environments where their groups have a role, computes the union of authorized runtimes, and returns only those. When they call `list_runtimes` via MCP, same behavior. The user's view is the union across all their environments. The gateway does this evaluation per-request using the JWT's group claims.

**Explicit environment endpoint:** When `endpoint.enabled: true`, the environment also gets dedicated paths across all adapters. These paths are additive — the standard endpoints continue to work.

---

## What This Changes in the Design

Two additions to the spec, both post-v1 but needing data model accommodation in v1:

**New admin API resource:** `Environment` — `GET/POST/PUT/DELETE /v1/admin/environments`. Tag selectors, member assignments, tool filters, optional endpoint config.

**Identity-aware filtering in the gateway:** All `list_runtimes`, `GET /v1/runtimes`, and `GET /v1/models` calls become identity-aware from v1 even before environments ship. The existing policy-scoped filtering already does some of this via `DelegationPolicy`. Environments extend this with group-level scoping that doesn't require the deployer to configure per-runtime policies.

**v1 data model accommodations:**
- `Runtime` resources need `labels` map from v1 (already implied by the tag-based `DelegationPolicy` design, but should be explicit)
- `Connector` registrations need `labels` map from v1 for the same reason
- `type: mcp` runtime tool schemas should be cacheable by the gateway from v1 so tool-level filtering doesn't require a live connection to the runtime on every request

---

## Human (Apr 1, 2026, 10:36 PM):

Here's a new concept I'd like to explore: ContextForge has something called "virtual servers", which are a way to create sandboxes containing a specific set of agents and tools. Virtual servers follow RBAC. Would this add value in Lenny? If so, how? And should this be external facing only or should it also bleed into what agents can see internally?

---

## Claude:

All three points are connected at the root. Let me address them together.

---

## The Core Problem with Tool Name Filtering

Explicit tool name lists fail for three reasons you've identified:

**Maintainability** — when a `type: mcp` runtime or connector adds or renames tools, every environment definition that references those tool names breaks. The admin has to track tool inventories across all servers, which doesn't scale.

**Name collisions** — if two MCP servers both expose a tool called `search` or `create_issue`, a filter rule on `"search"` is ambiguous. You'd need fully qualified references everywhere, which makes the filter verbose and brittle.

**Discovery burden** — to write a filter, you first need to know exactly which tools a runtime exposes and what they do. This requires deep familiarity with every runtime's tool surface. For an environment admin who isn't the runtime author, this is unreasonable.

The right alternative is **capability profiles** — a standard taxonomy of what a tool *does* rather than what it's *named*.

---

## Capability Profiles

Rather than listing tool names, tools are annotated with one or more capability tags from a standard vocabulary. Environment policy grants or denies by capability. The admin never needs to know tool names.

**Platform-defined standard capabilities:**

| Capability | Meaning |
|---|---|
| `read` | Reads data, no side effects |
| `write` | Creates or modifies data |
| `delete` | Removes data |
| `execute` | Runs code or shell commands |
| `network` | Makes outbound network requests |
| `search` | Queries or searches data stores |
| `admin` | Administrative or privileged operations |

Runtime and connector authors annotate their tools using these when registering with Lenny:

```yaml
# Connector registration — GitHub
id: github
tools:
  - name: list_issues
    capabilities: [read, search]
  - name: create_issue
    capabilities: [write]
  - name: merge_pr
    capabilities: [write]
  - name: delete_branch
    capabilities: [delete]
  - name: run_workflow
    capabilities: [execute]
```

The environment policy then refers only to capabilities:

```yaml
name: security-team

runtimeSelector:
  matchLabels:
    team: security

mcpRuntimeFilters:
  - runtimeSelector:
      matchLabels:
        category: execution
    allowedCapabilities: [read, execute]
    deniedCapabilities: [write, delete, admin]

connectorSelector:
  matchLabels:
    team: security
  allowedCapabilities: [read, search]
  deniedCapabilities: [write, delete, execute, admin]
```

When a new tool is added to a connector — say a `create_comment` tool with capability `write` — it is automatically denied in this environment without any definition change. The capability annotation is the contract, not the tool name.

---

## Escaping the Capability Profile When Needed

Capability profiles handle 90% of cases. For the remaining 10% where an admin genuinely needs tool-level precision — for instance, a runtime exposes two `write`-capable tools and you want to allow one but not the other — a fully qualified override is available alongside the capability rule:

```yaml
mcpRuntimeFilters:
  - runtimeSelector:
      matchLabels:
        category: execution
    allowedCapabilities: [read]
    overrides:
      allow:
        - runtime: code-executor
          tool: execute_python    # allowed even though execute capability is not granted
      deny:
        - runtime: code-executor
          tool: read_sensitive_config   # denied even though read capability is granted
```

The `runtime:tool` qualified reference solves the name collision problem — it's always unambiguous which server's tool is being referenced. This syntax is only needed in `overrides`, not in the common case.

---

## Connectors: Internal Only, Same Model

Confirming and clarifying:

`connectorSelector` is internal only. It defines which connectors are available to agents running sessions within this environment — what appears in the adapter manifest, what tools the agent can call via the platform MCP server's connector proxy. External clients do not call connectors directly through the environment. The `allowedCapabilities`/`deniedCapabilities` pattern applies identically:

```yaml
connectorSelector:
  matchLabels:
    team: security
  allowedCapabilities: [read, search]
  overrides:
    deny:
      - connector: github
        tool: list_repositories  # too broad, explicitly excluded despite read capability
```

---

## What Runtime and Connector Authors Must Do

This model places a small obligation on authors: annotate tools with capabilities when registering with Lenny. This is reasonable — it's part of the tool contract, similar to how an API documents whether an endpoint is read-only or mutating. The admin API for connector registration gains a `tools` array with `capabilities` per tool.

For runtime authors who don't annotate, the gateway assigns a default of `[admin]` — the most restrictive capability — so untagged tools are blocked everywhere unless explicitly allowed by override. This creates the right incentive: annotate your tools or they won't be usable in scoped environments.

One open question worth flagging: **who validates capability annotations are accurate?** A runtime author who marks a `delete` operation as `read` bypasses the capability filter. This is the same trust model as any other system where developers self-declare metadata. For Lenny's threat model — where runtime authors are already trusted enough to run code in a pod — this is acceptable. If stronger guarantees are needed, a runtime review process can include capability annotation auditing.

---

## Human (Apr 1, 2026, 11:00 PM):

A few comments:
* I think having tailored endpoints to each environment might make sense, but I'm not sold on it and in any case  it shouldn't just be on the MCP external interface -it should apply to all external interfaces, including Lenny's API
* At the same time, I can see in some scenarios how one might want to control what specific users or groups can access in a transparent way, without having to resort to specific endpoints. Maybe this is not through "environments" but through a different construct.
   * A user/group may have access to multiple environments and the union of those should be available from a single interface. 
* Environments can explicitly call out what runtimes are included, but there should be other ways to select runtimes that don't require listing explicit ones. Maybe a label/tag system could work here.
* Environment definitions should be granular enough to distinguish between tools inside an MCP runtime
* If connectors are exposed externally by the gateway (I actually don't remember this part), then the same applies to connectors. 
* This feature would require integration with LDAP, right?

---

## Claude:

The MCP spec already has something here, and the industry is actively wrestling with exactly this problem. The research gives a clear picture.

---

## What MCP Already Has

MCP tool annotations shipped in the 2025-03-26 spec revision. The current `ToolAnnotations` interface looks like this:

```typescript
interface ToolAnnotations {
  title?: string;
  readOnlyHint?: boolean;    // default: false
  destructiveHint?: boolean; // default: true
  idempotentHint?: boolean;  // default: false
  openWorldHint?: boolean;   // default: true
}
```

Every property is a hint.

These four properties cover meaningful ground for Lenny's purposes:

- `readOnlyHint: true` — the tool makes no modifications. Maps to `read` capability only.
- `destructiveHint: true` — the tool can cause data loss or irreversible changes. Maps to `delete` or `write`.
- `openWorldHint: true` — the tool makes requests to external systems. Maps to `network`.
- `idempotentHint: true` — calling it multiple times has the same effect as calling once. Informational rather than a capability category.

---

## The Trust Problem Is Explicitly Recognized

The original proposal discussion surfaced a question that still shapes every annotation proposal today: what value do hints provide when they can't be trusted? MCP co-creator Justin Spahr-Summers raised it directly: "I think the information itself, if it could be trusted, would be very useful, but I wonder how a client makes use of this flag knowing that it's not trustable." The spec landed on a compromise: call everything a hint, require clients to treat hints as untrusted by default, and leave it to each client to decide how much weight to give them based on what it knows about the server.

This is the industry-wide unsolved problem you're pointing at. The trust and sensitivity work is co-authored by GitHub and OpenAI based on gaps they hit running MCP in production. A Tool Annotations Interest Group is forming to work through these alongside related proposals like tool resolution and preflight checks. There are five active Spec Enhancement Proposals for new annotations. The community is moving but hasn't converged.

---

## What Lenny Should Do

Given this, Lenny's capability model should be **derived from MCP's existing annotations** wherever they exist, rather than requiring manual re-annotation. This avoids duplicate maintenance and keeps Lenny aligned with the standard.

**Mapping from MCP annotations to Lenny capability categories:**

| MCP annotation | Inferred Lenny capabilities |
|---|---|
| `readOnlyHint: true` | `[read]` |
| `readOnlyHint: false, destructiveHint: false` | `[write]` |
| `destructiveHint: true` | `[write, delete]` |
| `openWorldHint: true` | `[network]` |
| No annotations present | `[admin]` — conservative default |

`execute` and `admin` have no MCP equivalents yet. For tools that run code or perform privileged operations, Lenny's connector registration can supplement with these. But for the majority of well-behaved connectors, the inference from MCP annotations covers the common cases automatically.

**On trust:** Lenny treats inferred capabilities as informative, not as security guarantees. The gateway enforces environment policy based on capability categories, but the underlying annotations are self-declared. This is the same trust model as MCP's own spec mandates. For Lenny's threat model — connector authors are already trusted enough to be registered by admins — this is acceptable, and consistent with what the rest of the industry does today.

**Tracking SEPs:** The annotation standard is actively evolving. Lenny should monitor and adopt relevant SEPs as they land, particularly anything coming from the Tool Annotations Interest Group. The capability-to-annotation mapping table is the right abstraction boundary — when MCP adds new annotations, Lenny adds rows to that table without changing the environment policy model.

---

## What This Changes in the Design

The previous design required connector authors to annotate tools with Lenny-specific capability tags at registration time. This is now replaced:

**At connector registration or `type: mcp` runtime registration**, the gateway reads the server's `tools/list` response. For each tool, it infers capabilities from MCP annotations. No manual capability annotation required from the connector author. If annotations are absent, the conservative `[admin]` default applies.

**Admins can override** inferred capabilities at registration time if the inference is wrong:

```yaml
id: github
toolCapabilityOverrides:
  - tool: run_workflow
    capabilities: [execute, network]   # MCP has no execute hint yet;
                                        # admin adds it explicitly
```

This keeps the common case zero-configuration while preserving an escape hatch for tools that MCP's current hint vocabulary can't express accurately.

---

## Human (Apr 1, 2026, 11:15 PM):

Comments:
* `mcpToolFilters` should be `mcpRuntimeFilters` to avoid confusion with MCP connectors
* connectorSelector makes sense but it should only apply internally (what connectors are available). This one also needs allowed/denied tools
* Tool filtering, as defined here, is hard to maintain. Hard to know what tools are even needed by a specific agent runtime. There can be name collisions across MCP servers. Is there a better alternative?

---

## Claude:

## Lenny RBAC — Complete Model

---

### Conceptual Structure

Four layers, each narrowing the previous:

```
Tenant
  └── Environment(s)          ← named, RBAC-governed project context
        ├── runtimeSelector    ← which runtimes are in scope (tag-based)
        ├── connectorSelector  ← which connectors agents can use (internal only)
        ├── mcpRuntimeFilters  ← capability-based tool filtering on type:mcp runtimes
        └── members            ← which users/groups can create sessions here
```

Identity flows through OIDC. Groups from LDAP/AD are carried as JWT claims. The gateway evaluates all environments where the caller has a role and presents the **union** of authorized runtimes and connectors. No special endpoint required — the standard interface is identity-aware by default.

---

### Two Access Paths

**Transparent filtering** — the default. A user hits `/mcp`, `/v1`, or `/v1/responses`. The gateway reads their JWT, resolves their group memberships to environments, computes the authorized view, and returns only what they can see. The user is unaware of the environment machinery.

**Explicit environment endpoint** — opt-in per environment. When `endpoint.enabled: true`, the environment is additionally accessible at `/mcp/environments/{name}`, `/v1/environments/{name}/sessions`, `/v1/responses` (with `model` scoped to environment), and `/v1/chat/completions` (same). Useful for CI/CD pipelines and agent orchestrators that want deterministic scoping. These paths apply uniformly across all external interfaces — not MCP-only.

---

### Capability Model

Derived from MCP's `ToolAnnotations` standard. No manual re-annotation required from connector authors. The gateway reads `tools/list` at registration time and infers capabilities:

| MCP annotation | Inferred capabilities |
|---|---|
| `readOnlyHint: true` | `read` |
| `readOnlyHint: false, destructiveHint: false` | `write` |
| `destructiveHint: true` | `write, delete` |
| `openWorldHint: true` | `network` |
| No annotations | `admin` (conservative default) |
| *(no MCP equivalent yet)* | `execute`, `admin` — Lenny-only; set via override |

Capabilities are **per-tenant configurable**. Tenants can define their own capability taxonomy, rename platform defaults, add domain-specific ones, or restrict which capabilities can be granted at all within the tenant. Platform defaults apply when no tenant customization exists.

---

### Default Behavior When No Environments Are Defined

Configurable per tenant via `noEnvironmentPolicy`:

- `deny-all` — no user can create sessions or list runtimes. Admins must define environments before the tenant is usable. Safe default for multi-tenant enterprise deployments.
- `allow-all` — all authenticated users in the tenant can see and use all runtimes and connectors. Convenient for small teams, internal deployments, or development tenants.

Platform default is `deny-all`. Deployers can set the system-wide default at Helm time; individual tenants can override.

---

### Full Configuration Template

```yaml
# ─────────────────────────────────────────────────────────────────
# TENANT-LEVEL RBAC CONFIGURATION
# Managed via: PUT /v1/admin/tenants/{id}/rbac-config
# ─────────────────────────────────────────────────────────────────
tenantRbacConfig:
  tenantId: acme-corp

  # Behavior when no environments are defined for this tenant.
  # deny-all: no access until environments are created (default)
  # allow-all: all authenticated tenant users see all runtimes/connectors
  noEnvironmentPolicy: deny-all   # deny-all | allow-all

  # Identity provider configuration for this tenant.
  # Lenny speaks OIDC; the IdP handles LDAP/AD translation.
  identityProvider:
    issuerUrl: https://idp.acme.com
    clientId: lenny-acme
    groupsClaim: groups            # JWT claim carrying group memberships
    usernameClaim: email           # JWT claim used as user identity
    groupFormat: short-name        # short-name | dn | uuid
                                   # how groups appear in the JWT claim

  # Token management
  tokenPolicy:
    maxTtlSeconds: 3600
    introspectionEnabled: false    # true = real-time group membership checks
                                   # at cost of latency; false = rely on JWT TTL
    introspectionEndpoint: https://idp.acme.com/introspect   # if enabled

  # Tenant-level capability taxonomy.
  # Extends or replaces platform defaults.
  capabilities:
    # Inherit platform defaults and add/override entries
    inheritPlatformDefaults: true

    # Custom capabilities for this tenant
    custom:
      - id: pii-access
        description: "Access to tools that read PII data"
        # No MCP annotation mapping — must be set via toolCapabilityOverrides
      - id: financial-write
        description: "Write access to financial systems"

    # Restrict which capabilities may be granted within this tenant.
    # Even if a connector declares admin, it can't be granted here.
    allowedCapabilities:
      - read
      - write
      - delete
      - execute
      - network
      - search
      - admin
      - pii-access        # tenant-custom
      - financial-write   # tenant-custom

    # Platform default capability assigned to unannotated tools.
    # Overrides the system-wide default (admin) for this tenant.
    defaultCapabilityForUnannotatedTools: admin   # any capability id

  # System-wide MCP annotation → capability mapping for this tenant.
  # Overrides platform defaults for all connectors in this tenant.
  mcpAnnotationMapping:
    readOnlyHint_true: [read]
    readOnlyHint_false_destructiveHint_false: [write]
    destructiveHint_true: [write, delete]
    openWorldHint_true: [network]
    noAnnotations: [admin]


# ─────────────────────────────────────────────────────────────────
# ENVIRONMENT DEFINITION
# Managed via: POST /v1/admin/environments
# ─────────────────────────────────────────────────────────────────
environment:
  name: security-team
  tenantId: acme-corp
  description: "Security engineering workspace"

  # ── Member RBAC ────────────────────────────────────────────────
  # Roles:
  #   viewer   — list runtimes, read session status and artifacts
  #   creator  — viewer + create sessions, call type:mcp tools
  #   operator — creator + interrupt/resume/terminate sessions
  #   admin    — operator + modify this environment's definition
  members:
    - identity:
        type: oidc-group        # oidc-group | oidc-user | service-account
        value: security-engineers
      role: creator
    - identity:
        type: oidc-group
        value: security-leads
      role: admin
    - identity:
        type: oidc-user
        value: alice@acme.com
      role: operator
    - identity:
        type: service-account
        value: sa-security-ci
      role: creator

  # ── Runtime Selector ───────────────────────────────────────────
  # Determines which runtimes (type:agent and type:mcp) are visible
  # and usable within this environment.
  # All conditions are ANDed. Include/exclude override selector results.
  runtimeSelector:
    matchLabels:                # exact label matches (AND)
      team: security
    matchExpressions:           # richer expressions (AND with matchLabels)
      - key: approved
        operator: In            # In | NotIn | Exists | DoesNotExist
        values: [true]
      - key: experimental
        operator: DoesNotExist
    types: [agent, mcp]         # filter by runtime type; omit for all types
    include:                    # explicit inclusions regardless of selector
      - legacy-code-auditor
    exclude:                    # explicit exclusions regardless of selector
      - unstable-scanner

  # ── MCP Runtime Filters ────────────────────────────────────────
  # Tool-level capability filtering for type:mcp runtimes.
  # Applied when a client calls tools via /mcp/runtimes/{name}
  # or when an agent calls them via the adapter manifest.
  # Multiple entries are evaluated in order; first match wins.
  mcpRuntimeFilters:
    - runtimeSelector:
        matchLabels:
          category: execution
      allowedCapabilities: [read, execute]
      deniedCapabilities: [write, delete, admin]
      # Fine-grained overrides when capability rules are insufficient.
      # Uses runtime:tool qualified reference to avoid name collisions.
      overrides:
        allow:
          - runtime: code-executor
            tool: execute_python      # allowed even if execute not in allowedCapabilities
        deny:
          - runtime: code-executor
            tool: read_env_vars       # denied even if read is in allowedCapabilities

    - runtimeSelector:
        matchLabels:
          category: analysis
      allowedCapabilities: [read, search, network]
      deniedCapabilities: [write, delete, execute, admin]

    # Catch-all for runtimes in this environment not matched above.
    # If absent, unmatched runtimes get allowedCapabilities: [read] by default.
    - runtimeSelector:
        matchExpressions:
          - key: team
            operator: Exists
      allowedCapabilities: [read]

  # ── Connector Selector ─────────────────────────────────────────
  # Determines which connectors are available to agents running
  # sessions in this environment (internal only — agents call these
  # via the platform MCP server; external clients never call
  # connectors directly through environment policy).
  connectorSelector:
    matchLabels:
      team: security
    matchExpressions:
      - key: data-sensitivity
        operator: NotIn
        values: [restricted, confidential]
    include:
      - snyk-scanner
    exclude:
      - prod-db-writer

    # Capability filter applied to all connectors in scope.
    # Derived from MCP ToolAnnotations; admin can override per connector.
    allowedCapabilities: [read, search, network]
    deniedCapabilities: [write, delete, execute, admin]
    overrides:
      allow:
        - connector: github
          tool: create_issue           # write, but allowed for issue filing
      deny:
        - connector: github
          tool: delete_repository      # explicitly blocked even if delete allowed

  # ── Delegation Policy ──────────────────────────────────────────
  # Applied to sessions created in this environment.
  # References a named DelegationPolicy resource.
  # Sessions inherit this unless overridden at session creation time.
  defaultDelegationPolicy: security-policy

  # Whether sessions in this environment can delegate to runtimes
  # in other environments. false = environment-boundary enforcement.
  crossEnvironmentDelegation: false   # true | false (default: false)

  # ── Explicit Environment Endpoint ─────────────────────────────
  # When enabled, this environment is accessible at dedicated paths
  # across ALL external interfaces:
  #   /mcp/environments/security-team
  #   /v1/environments/security-team/sessions
  #   /v1/environments/security-team/runtimes
  #   /v1/responses  (model namespace scoped to environment)
  #   /v1/chat/completions  (model namespace scoped to environment)
  # Transparent filtering at standard endpoints always applies
  # regardless of this setting.
  endpoint:
    enabled: true
    path: security-team              # URL slug; defaults to environment name
    allowPublicDiscovery: false      # true = endpoint listed in /.well-known/


# ─────────────────────────────────────────────────────────────────
# CONNECTOR REGISTRATION (RBAC-RELEVANT FIELDS)
# Managed via: POST /v1/admin/connectors
# ─────────────────────────────────────────────────────────────────
connector:
  id: github
  labels:
    team: security
    data-sensitivity: internal

  # Gateway infers capabilities from MCP ToolAnnotations at
  # registration time. No manual annotation required.
  # Override here when MCP annotations are absent or inaccurate.
  toolCapabilityOverrides:
    - tool: run_workflow
      capabilities: [execute, network]    # MCP has no execute hint yet
    - tool: list_repositories
      capabilities: [read, network]       # explicit; overrides inference


# ─────────────────────────────────────────────────────────────────
# RUNTIME LABELS (RBAC-RELEVANT FIELDS ON RUNTIME RESOURCE)
# Managed via: POST/PUT /v1/admin/runtimes
# ─────────────────────────────────────────────────────────────────
runtime:
  name: code-auditor
  labels:
    team: security
    approved: "true"
    category: analysis
  # ... rest of runtime definition
```

---

### Key Design Decisions Summarized

**Capabilities are tenant-customizable, platform-defaulted.** Platform ships with `read`, `write`, `delete`, `execute`, `network`, `search`, `admin`. Tenants can add domain-specific ones (`pii-access`, `financial-write`), rename, or restrict which can ever be granted.

**Capability inference from MCP annotations is automatic.** No connector author action required. The mapping table is tenant-overridable. Tools with no annotations get the tenant's configured `defaultCapabilityForUnannotatedTools` (default: `admin`).

**No environments = configurable per tenant.** `noEnvironmentPolicy: deny-all` is the platform default. Tenants can set `allow-all` for development deployments or small teams. Deployers can override the system-wide default at Helm time.

**Connectors are internal-only in the environment model.** `connectorSelector` controls what agents can call via the platform MCP server. External clients do not call connectors through environment policy.

**Two access paths, one consistent model.** Transparent filtering at standard endpoints covers the human-user and LLM-client case. Explicit environment endpoints cover CI/CD and automation. Both paths apply to all external interfaces — MCP, REST, Open Responses, OpenAI Completions — not just MCP.

**Identity is OIDC with LDAP/AD behind the provider.** Lenny never speaks LDAP directly. The IdP (Keycloak, Okta, Entra ID) handles directory integration. Group memberships are JWT claims. `introspectionEnabled: true` adds real-time checks at the cost of latency — deployer choice.

---

## Human (Apr 1, 2026, 11:28 PM):

Is there anything in the MCP standard that can be used to determine capabilities in connector tools? Isn't this an industry wide problem?

---

## Claude:

Good. I have everything I need from this conversation and the previous compilation. Let me produce the complete revised document.

---

# Lenny Technical Spec — Final Revised Changes from Design Review (v2)

## How to Read This Document

- **V1 Must-Have (Breaking)** — must be in v1; impossible to fix after the first community runtime ships
- **V1 Must-Have (Feature)** — must ship with v1 to meet stated goals
- **V1 Should-Have** — significant value; should ship but won't break compatibility if deferred
- **Planned / Post-V1** — document intent now so data models accommodate it; implement later
- **Explicit Non-Decisions** — things Lenny deliberately will not do

---

## Part 1: V1 Must-Have — Breaking Changes

---

### 1.1 Replace Custom CRDs With `kubernetes-sigs/agent-sandbox`

**What changes:** Replace `AgentPool`, `AgentPod`, and `AgentSession` CRDs and the WarmPoolController with the upstream `SandboxTemplate`, `SandboxWarmPool`, `SandboxClaim`, and `Sandbox` CRDs from `kubernetes-sigs/agent-sandbox`.

**Mapping:**

| Lenny CRD | Agent-Sandbox CRD |
|---|---|
| `AgentPool` | `SandboxTemplate` |
| `AgentPod` | `Sandbox` + `SandboxWarmPool` |
| `AgentSession` | `SandboxClaim` |
| WarmPoolController | Upstream SIG Apps controller |

`executionMode` (`session`, `task`, `concurrent`) is declared on `SandboxTemplate` from v1 even if `task` and `concurrent` land slightly later in the build sequence. The data model must be ready before the first community runtime ships.

**Pre-commit requirement:** Verify `SandboxClaim` optimistic-locking guarantee matches Lenny's current status PATCH approach before committing.

**Affects:** Sections 4.6, 6.1, 6.2, 6.3, Phase 1 and Phase 3.

---

### 1.2 Runtime Unification: `Runtime` Replaces `RuntimeType` and `SessionTemplate`

**What changes:** `RuntimeType` and `SessionTemplate` are unified into a single `Runtime` concept. Standalone runtimes have an `image`. Derived runtimes have a `baseRuntime` and no `image`.

```yaml
# Standalone runtime
name: langgraph-runtime
image: registry.example.com/langgraph:latest
type: agent
capabilities:
  interaction: one_shot
executionMode: task
isolationProfile: sandboxed
allowedResourceClasses: [small, medium, large]
delegationPolicyRef: orchestrator-policy
labels:
  team: platform
  approved: "true"
  category: orchestration

# Derived runtime
name: research-pipeline
baseRuntime: langgraph-runtime
workspaceDefaults:
  files:
    - path: agent.py
      content: "..."
  setupCommands:
    - pip install -r requirements.txt
setupPolicy:
  timeoutSeconds: 300
  onTimeout: fail
agentInterface: ...
delegationPolicyRef: research-policy
publishedMetadata: ...
labels:
  team: research
  approved: "true"
tags: [approved, research]
```

**Labels are required from v1** — they are the primary mechanism for environment `runtimeSelector` and `connectorSelector` matching.

**Inheritance rules — never overridable on derived runtime:** `type`, `executionMode`, `isolationProfile`, `capabilities.interaction`, `allowedResourceClasses`, `allowStandardIsolation` acknowledgment.

**Independently configurable on derived runtime:** Pool settings, `workspaceDefaults`, `setupCommands`, `agentInterface`, `delegationPolicyRef` (restrict only), `publishedMetadata`, `labels`, `taskPolicy`.

**`setupPolicy.timeoutSeconds`:** Gateway takes the **maximum** of base and derived values.

**Base runtime mutability:** `image` and `name` immutable via API. All other fields mutable with impact validation — changes that would invalidate existing derived runtimes are rejected with a list of affected runtimes.

**Affects:** Section 5.1, Section 15.1 and 15.2, Phase 1.

---

### 1.3 `type` and `capabilities` Fields on Runtime

**What changes:** Every `Runtime` has a `type` field and an optional `capabilities` field.

```yaml
type: agent          # agent (default) | mcp
capabilities:
  interaction: one_shot
  injection:
    supported: true
    modes: [immediate, queued]
```

**`type: agent`** — participates in Lenny's task lifecycle. Receives tasks via stdin `{type: "message"}`, has sessions, workspace, delegation, elicitation, messaging. Callable via `lenny/delegate_task`.

**`type: mcp`** — hosts an MCP server. Lenny manages pod lifecycle (isolation, credentials, workspace, pool, egress, audit). No task lifecycle. Runtime binary is oblivious to Lenny.

**`capabilities.interaction`** is defined as single-value enum (`one_shot`) in v1 so adding `multi_turn` post-v1 is non-breaking. Gateway hard-rejects `interaction: multi_turn` in v1.

**`capabilities.injection`** declares whether the runtime supports mid-session message delivery. Default: `supported: false`. Gateway rejects injection attempts against unsupported sessions at the API level.

**`type: mcp` runtimes have no `capabilities` field.**

**Affects:** Section 5.1, Phase 1.

---

### 1.4 Adapter Protocol: Multi-Server MCP + Lifecycle Channel

**What changes:** The stdin/stdout JSON Lines binary protocol (Section 15.4.1) is replaced by a two-part model.

**Part A — Multiple focused local MCP servers** (intra-pod, stdio or Unix socket):

- **Platform MCP server** — Lenny-specific tools: `lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`, `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`, `lenny/request_input`, `lenny/send_message`, `lenny/get_task_tree`
- **One MCP server per authorized connector** — each connector in the session's delegation policy gets its own independent MCP server. No aggregated connector proxy — aggregation is not lossless per MCP spec.

**No workspace MCP server.** Workspace is materialized to `/workspace/current` before the runtime starts.

**Part B — Lifecycle channel** — separate stdin/stdout stream pair for operational signals:

```
Adapter → Runtime:  lifecycle_capabilities, checkpoint_request,
                    interrupt_request, credentials_rotated, terminate
Runtime → Adapter:  lifecycle_support, checkpoint_ready,
                    interrupt_acknowledged, credentials_acknowledged
```

Optional. Runtimes that don't open it operate in fallback-only mode.

**Runtime integration tiers (agent-type only):**

- **Minimum** — stdin/stdout binary protocol only. Reads `{type: "message"}` from stdin, writes `{type: "response"}` and `{type: "tool_call"}` to stdout.
- **Standard** — minimum plus connects to adapter's platform MCP server and connector servers via the adapter manifest.
- **Full** — standard plus opens the lifecycle channel for checkpoint, interrupt, and credential rotation support.

**Adapter manifest:** Written to `/run/lenny/adapter-manifest.json` **before the runtime binary is spawned** — complete and authoritative when the binary starts. Regenerated per task execution.

```json
{
  "platformMcpServer": {
    "socket": "/run/lenny/platform-mcp.sock"
  },
  "connectorServers": [
    { "id": "github", "socket": "/run/lenny/connector-github.sock" }
  ],
  "runtimeMcpServers": [],
  "agentInterface": { ... },
  "sessionId": "sess_abc",
  "taskId": "task_root"
}
```

**Startup sequence for `type: agent` runtimes:**

1. Pod created by Kubernetes
2. Adapter opens gRPC connection to gateway (mTLS)
3. Adapter writes placeholder manifest (connector servers unknown yet)
4. Adapter signals READY to gateway — pod enters warm pool
5. Gateway assigns session: `PrepareWorkspace` → `FinalizeWorkspace` → `RunSetup` → `AssignCredentials` → `StartSession`
6. Adapter writes **final manifest** (connector servers now known from lease)
7. Adapter spawns runtime binary
8. Runtime reads manifest, connects to MCP servers (Standard/Full), opens lifecycle channel (Full)
9. Adapter sends `lifecycle_capabilities` (Full runtimes); receives `lifecycle_support`
10. Adapter delivers first `{type: "message"}` on stdin

**Affects:** Section 15.4 and 15.4.1 (full replacement), Section 4.7, Phase 2.

---

### 1.5 Binary Protocol: Unified `message` Type — `prompt` Removed

**What changes:** The `prompt` inbound message type is removed entirely. The unified `message` type handles all inbound content delivery — the initial task and all subsequent messages. No `sessionState` field — the runtime knows it's receiving its first message by virtue of just having started.

**Inbound messages (adapter → runtime via stdin):**

| `type` | Description |
|---|---|
| `message` | All content delivery: initial task, mid-session injection, reply to `request_input`, sibling notification |
| `heartbeat` | Periodic liveness ping |
| `shutdown` | Graceful shutdown with no new task |

**Outbound messages (runtime → adapter via stdout):**

| `type` | Description |
|---|---|
| `response` | Streamed or complete response carrying `OutputPart[]` |
| `tool_call` | Runtime requests a tool call |
| `status` | Optional status/trace update |

**`follow_up` renamed to `message` everywhere.** No `follow_up` type anywhere in the protocol.

**`input_required` outbound message type removed.** Replaced by `lenny/request_input` blocking MCP tool call. The `input_required` task state is set by the gateway when `lenny/request_input` is called — not triggered by a stdout message.

**Task mode between-task signaling:** Adapter sends `{type: "terminate", reason: "task_complete"}` on the lifecycle channel after a task completes. The next `{type: "message"}` after scrub is the start of the new task.

**Affects:** Section 15.4.1 (binary protocol message types).

---

### 1.6 Internal `OutputPart` Format

**What changes:** `agent_text` streaming event replaced by `agent_output` carrying `OutputPart` array. `TaskResult` and `TaskSpec` use `OutputPart` arrays.

```json
{
  "id": "part_abc123",
  "type": "text",
  "mimeType": "text/plain",
  "inline": "content here",
  "ref": "artifact://...",
  "annotations": { "role": "primary", "final": true },
  "parts": [],
  "status": "streaming | complete | failed"
}
```

- `type` is an open string — not a closed enum
- `mimeType` handles encoding; `type` expresses semantic purpose
- `annotations` is an open metadata map
- `parts` allows nesting
- `id` enables part-level streaming updates

SDK helper `from_mcp_content(blocks)` converts MCP content blocks to `OutputPart` arrays.

**Affects:** Section 15.4.1, Section 8.9, Section 7.2.

---

### 1.7 `MessageEnvelope` — Unified Message Format

**What changes:** All inbound content messages use a unified `MessageEnvelope` across stdin binary protocol, platform MCP server tools, and all REST/MCP/adapter-facing APIs.

```json
{
  "id": "msg_xyz789",
  "from": {
    "kind": "client | agent | system | external",
    "id": "..."
  },
  "inReplyTo": "req_abc123",
  "threadId": "thread_001",
  "delivery": "immediate",
  "input": [OutputPart[]]
}
```

**Adapter-injected fields — runtime never supplies these:**
- `from.kind` and `from.id` — injected by the adapter from execution context
- `requestId` in `lenny/request_input` — generated by the gateway; runtime only supplies `parts`

**`delivery`** — optional mechanical hint. `immediate` means deliver at next stdin read (after interrupt acknowledgment if needed). Absent means queue for next natural pause.

**`id`** — every message has a stable ID. Enables threading, reply tracking, and non-linear context retrieval without schema changes.

**`inReplyTo`** — optional. If it matches an outstanding `lenny/request_input` call on the target, the gateway resolves that tool call directly instead of delivering to stdin.

**`threadId`** — optional. In v1 one implicit thread per session. Multi-thread sessions are additive post-v1.

**Forward compatibility note:** `from.kind: "user"` with distinct `from.id` per user accommodates multi-participant sessions. External A2A messages use `from.kind: "external"`. These are additive.

**Affects:** Section 7.2, Section 15.4.1, Section 9.

---

### 1.8 TaskRecord Uses Messages Array + `input_required` State

**What changes:** Task record schema uses a messages array:

```json
{
  "messages": [
    { "role": "caller", "parts": [...] },
    { "role": "agent",  "parts": [...], "state": "completed" }
  ]
}
```

In v1 always exactly two messages. Future multi-turn adds more.

**Task state machine gains `input_required` — reachable in v1:**

```
submitted → running → completed
                    → failed
                    → input_required   (reachable via lenny/request_input)
```

`lenny/await_children` unblocks when a child enters `input_required`, returning a partial result with the child's question and `requestId`. gRPC `AwaitChildren` is a streaming response — it yields partial events before the final settled result.

**Affects:** Section 8.9.

---

### 1.9 Session State Machine Gains `suspended`

**What changes:** `interrupt_request` on the lifecycle channel produces a distinct `suspended` session state:

```
running → suspended   (interrupt_request + interrupt_acknowledged)
suspended → running   (resume_session — no new content)
suspended → running   (POST /v1/sessions/{id}/messages delivery:immediate)
suspended → completed (terminate)
```

Pod held, workspace preserved, `maxSessionAge` timer paused while suspended. `interrupt_request` is a standalone lifecycle signal — pause-and-decide with decoupled timing. Distinct from `delivery: "immediate"` in a message, which atomically interrupts and delivers content.

**`interrupt_request` does NOT cascade** to children. Budget/lease expiry does cascade. Runtime decides whether to propagate a received interrupt to its children.

**Affects:** Section 7.2, Section 6.1.

---

### 1.10 Single `lenny/delegate_task` Tool

**What changes:** One delegation tool. Target id is opaque — runtime does not know whether target is a standalone runtime, derived runtime, or external registered agent.

```
lenny/delegate_task(
  target: string,
  task: TaskSpec,
  lease_slice?: LeaseSlice
) → TaskHandle
```

`TaskSpec` carries two separate input channels:

```json
{
  "input": [OutputPart[]],
  "workspaceFiles": {
    "export": [{ "glob": "src/auth/**", "destPrefix": "/" }]
  }
}
```

**`lenny/delegate_task` rejects `type: mcp` targets** with `target_not_an_agent`. External agents are registered as connectors, reachable by the same tool.

**Affects:** Section 8.2, Section 8.9, Section 9.1.

---

### 1.11 Gateway-Mediated Session Messaging

**What changes:** All inter-session communication is gateway-mediated. No direct pod-to-pod communication. Gateway validates authorization, enforces policy, records audit events, routes messages.

**Three platform MCP tools:**

```
lenny/send_message(
  to: string,             // taskId — unique across all sessions in task tree
  message: MessageEnvelope
) → void

lenny/request_input(
  parts: OutputPart[]     // the question content only
) → MessageEnvelope       // blocks until answer arrives

lenny/get_task_tree() → TaskTreeNode
```

`lenny/get_task_tree` returns:
```json
{
  "self": { "taskId": "task_001", "sessionId": "sess_abc" },
  "parent": { "taskId": "task_root", "sessionId": "sess_parent" },
  "children": [...],
  "siblings": [
    { "taskId": "task_002", "sessionId": "sess_xyz",
      "state": "running", "runtime": "analyst-agent" }
  ]
}
```

**`taskId` is the messaging address** — not `sessionId`. Task IDs are stable across pod recovery generations.

**Message delivery routing — three paths:**

1. **`inReplyTo` matches outstanding `lenny/request_input`** → gateway resolves blocked tool call directly. No stdin delivery, no interrupt.
2. **No matching pending request, runtime available** → `{type: "message"}` to stdin at next read.
3. **No matching pending request, runtime blocked in `await_children`** → buffered in inbox; delivered before the next `await_children` event.

**`lenny/send_to_child` and `lenny/notify_sibling` removed.** Replaced by `lenny/send_message`. **`lenny/request_input` replaces the stdout `input_required` message type.**

**Affects:** Section 8.2, Section 9, Section 7.2.

---

### 1.12 `DelegationPolicy` as First-Class Resource

**What changes:** `allowedRuntimes`, `allowedConnectors`, and `allowedPools` replaced by named `DelegationPolicy` resources.

```yaml
id: orchestrator-policy
rules:
  runtimes:
    allow:
      ids: ["review-agent"]
      tags: ["approved"]
  connectors:
    allow:
      ids: ["github", "jira"]
  externalAgents:
    allow: []
```

**Effective policy = `base_policy ∩ derived_policy`** — derived runtime policy can only restrict. Dynamic tag evaluation at delegation time. Session-level override: `maxDelegationPolicy` at session creation further restricts.

**`lenny/discover_agents`** returns `type: agent` runtimes and external agents only. `type: mcp` runtimes do not appear. Results are policy-scoped to the session's effective delegation policy.

**Affects:** Section 8.3, Section 5.1.

---

### 1.13 `executionMode` on Pool Configuration (All Three in V1)

**What changes:** Pool configuration has `executionMode` from day one. All three modes implemented in v1:

```yaml
executionMode: session | task | concurrent
```

Graph mode removed as a separate execution mode.

**Affects:** Section 5.2, Phase 1.

---

### 1.14 `agentInterface` Field on Runtime

**What changes:** `type: agent` runtimes gain an optional `agentInterface` field serving discovery results, A2A card auto-generation, and adapter manifest summaries.

```yaml
agentInterface:
  description: "Analyzes codebases and produces refactoring plans"
  inputModes:
    - type: "text/plain"
    - type: "application/json"
  outputModes:
    - type: "text/plain"
      role: "primary"
  supportsWorkspaceFiles: true
  skills:
    - id: "review"
      name: "Code Review"
      description: "Reviews code for quality and correctness"
  examples:
    - description: "Review auth module"
      input: "Review the authentication module"
```

`type: mcp` runtimes do not have `agentInterface`. Their capabilities are discovered via standard MCP capability negotiation at `/mcp/runtimes/{name}`.

**Affects:** Section 5.1.

---

### 1.15 `publishedMetadata` on Runtime

**What changes:** Both standalone and derived `Runtime` resources gain a `publishedMetadata` array.

```yaml
publishedMetadata:
  - key: agent-card
    contentType: application/json
    visibility: public    # internal | tenant | public
    value: '...'
```

**Visibility semantics:** `internal` — intra-cluster, valid JWT required. `tenant` — filtered by `tenant_id` claim. `public` — no auth required, externally reachable.

**External listing visibility:** A runtime not visible to the calling client does not appear in `list_runtimes`, `GET /v1/runtimes`, or `GET /v1/models`. Not-found and not-authorized produce identical responses — no enumeration.

**Affects:** Section 5.1.

---

### 1.16 `ExternalAdapterRegistry` (Replaces Single Adapter)

**What changes:** Gateway uses `ExternalAdapterRegistry` with simultaneously active adapters routing by path prefix.

```go
type ExternalProtocolAdapter interface {
    Protocol()    string
    PathPrefix()  string
    HandleRequest(ctx context.Context,
                  w http.ResponseWriter,
                  r *http.Request,
                  dispatcher SessionDispatcher) error
    HandleDiscovery(ctx context.Context,
                    w http.ResponseWriter,
                    r *http.Request,
                    runtimes []AuthorizedRuntime) error
    Capabilities() AdapterCapabilities
}
```

**`HandleDiscovery` is required on all adapters.** Every adapter must translate the policy-scoped, identity-filtered runtime list into its protocol's native discovery format.

**Three tiers of pluggability:** Built-in (compiled in), config-driven (Go plugin or gRPC service at startup), runtime registration via admin API (`POST /v1/admin/external-adapters`).

**Built-in adapter inventory:**

| Adapter | Path prefix | Protocol |
|---|---|---|
| `MCPAdapter` | `/mcp` | MCP Streamable HTTP |
| `OpenAICompletionsAdapter` | `/v1/chat/completions` | OpenAI Chat Completions |
| `OpenResponsesAdapter` | `/v1/responses` | Open Responses Specification |
| `A2AAdapter` | `/a2a/{runtime}` | A2A (post-v1) |
| `AgentProtocolAdapter` | `/ap/v1/agent` | AP (post-v1) |

**`OpenResponsesAdapter` covers both Open Responses and OpenAI Responses API clients.** OpenAI's Responses API is a proper superset of Open Responses; the difference is OpenAI's proprietary hosted tools, which Lenny doesn't implement. One adapter covers both. Implementation target is the Open Responses Specification (vendor-neutral, January 2026).

**Affects:** Section 4.1, Section 15, Phase 1.

---

### 1.17 `type: mcp` Runtime Dedicated MCP Endpoints

**What changes:** Each enabled `type: mcp` runtime gets a dedicated MCP endpoint at `/mcp/runtimes/{runtime-name}`. Standard MCP capability negotiation. Gateway proxies: external client → gateway → adapter → runtime MCP server socket.

Not aggregated. Each runtime gets its own endpoint. An implicit session record is created per connection for audit and billing.

**Discovery:** `GET /v1/runtimes` and `list_runtimes` return `mcpEndpoint: "/mcp/runtimes/{name}"` and a `mcpCapabilities.tools` preview. Full schemas come from connecting and calling `tools/list`.

**Affects:** Section 4.1, Section 9, Section 15.

---

### 1.18 Comprehensive Admin API

**What changes:** Full admin API surface. Primary store: Postgres. Short TTL cache in gateway. Redis pub/sub for immediate invalidation. CRDs become derived state reconciled from Postgres.

**Operational plane — API-managed:** Runtimes, Delegation Policies, Connectors, Pools, Credential Pools, Tenants, Quotas, User Role Assignments, Egress Profiles, Experiments, Scaling Policies, Memory Store Config, Webhooks, External Adapters, Environments, Tenant RBAC Config.

**Bootstrap plane — Helm only:** DB URLs, Redis, MinIO, KMS, cluster name, namespace assignments, certificate paths, `LENNY_DEV_MODE`, system-wide defaults, Kubernetes object definitions.

**Admin API design constraints:** Same error taxonomy, pagination, OIDC auth as client-facing API. `etag` for conditional PUT. `?dryRun=true` on all mutations. OpenAPI spec generated from Go handlers. All mutations in audit log.

**Affects:** Section 15.1, Section 10.2, Phase between 4 and 5.

---

## Part 2: V1 Must-Have — Feature Changes

---

### 2.1 Task Pod Execution Mode

**What changes:** `executionMode: task` reuses pods across sequential tasks with scrub between tasks.

```yaml
taskPolicy:
  cleanupCommands:
    - pkill -f jupyter_kernel
    - rm -rf /tmp/sandbox-*
  cleanupTimeoutSeconds: 30
  onCleanupFailure: warn    # warn | fail
```

Cleanup commands run before scrub. `setupCommands` run once per pod at start, not per task. Between tasks: adapter sends `terminate(reason:task_complete)` on lifecycle channel → runtime acknowledges → cleanup runs → Lenny scrubs → adapter writes new manifest → pod returns to available.

**Affects:** Sections 6.1, 6.2, 7, 5.2.

---

### 2.2 Concurrent Execution Mode

**What changes:** `executionMode: concurrent` allows multiple simultaneous task slots per pod.

```yaml
executionMode: concurrent
concurrencyStyle: stateless    # stateless | workspace
maxConcurrent: 8
```

`concurrencyStyle: stateless` — gateway routes through Kubernetes Service. `concurrencyStyle: workspace` — per-slot workspace, explicitly weaker isolation, deployer acknowledgment required.

**Affects:** Sections 5.2, 6.1, 6.2, 4.1.

---

### 2.3 `setupPolicy` on Runtime

**What changes:** Readiness driven by adapter READY signal after setup commands complete. `timeoutSeconds` is safety net only. Gateway takes **maximum** of base and derived values.

```yaml
setupPolicy:
  timeoutSeconds: 300
  onTimeout: fail    # fail | warn
```

**Affects:** Section 5.1, Section 6.2.

---

### 2.4 `ConnectorDefinition` as First-Class Resource

**What changes:** Connector configuration is a first-class admin API resource. Includes `labels` map for environment selector matching. Tool capability metadata derived from MCP `ToolAnnotations` at registration time (see 2.8).

**Affects:** Section 4.3, Section 9.3.

---

### 2.5 Runtime Discovery on All External Interfaces

**What changes:** Every external interface exposes runtime discovery in its protocol's native discovery format via `HandleDiscovery`. All results are identity-filtered and policy-scoped.

- **MCP:** `list_runtimes` tool — returns `agentInterface`, `mcpEndpoint`, `mcpCapabilities.tools` preview
- **REST:** `GET /v1/runtimes` — full `agentInterface` and `mcpEndpoint` fields
- **OpenAI Completions:** `GET /v1/models` — returns Lenny runtimes as model objects
- **Open Responses:** `GET /v1/models` — same; `type: mcp` runtimes expose tools at dedicated MCP endpoints

**Affects:** Section 9, Section 15.1, Section 15.2.

---

### 2.6 OpenAI Completions Adapter

**What changes:** Implements `POST /v1/chat/completions` and `GET /v1/models`. Stateless, ephemeral sessions. `model` parameter maps to Lenny runtime via configurable mapping in admin API. Tool handling — opaque mode only (agent handles tool execution internally; client never sees `tool_calls`).

**Affects:** Section 15.

---

### 2.7 Open Responses Adapter

**What changes:** Implements the Open Responses Specification. Covers both Open Responses-compliant and OpenAI Responses API clients for all core features.

- `background: true` → async Lenny session
- `previous_response_id` → `POST /v1/sessions/{id}/messages` or `POST /v1/sessions/{id}/derive`
- `store: true` → always true in Lenny; ignored
- No 10-minute polling window limit
- Lenny connectors → Open Responses **internal tools** (no client round-trip)
- Client-defined functions → not supported in v1

**Affects:** Section 15.

---

### 2.8 Capability Inference from MCP `ToolAnnotations`

**What changes:** At connector or `type: mcp` runtime registration, the gateway reads `tools/list` and infers capabilities from MCP `ToolAnnotations`. No manual re-annotation required.

**Standard MCP annotation → capability mapping (platform default, tenant-overridable):**

| MCP annotation | Inferred capabilities |
|---|---|
| `readOnlyHint: true` | `read` |
| `readOnlyHint: false, destructiveHint: false` | `write` |
| `destructiveHint: true` | `write, delete` |
| `openWorldHint: true` | `network` |
| No annotations | `admin` (conservative default) |
| *(no MCP equivalent)* | `execute`, `admin` — set via `toolCapabilityOverrides` |

Unannotated tools get the tenant's configured `defaultCapabilityForUnannotatedTools` (platform default: `admin`).

Admin can override inferred capabilities at registration time via `toolCapabilityOverrides` per tool. This is the escape hatch for the MCP hint vocabulary's current gaps.

**Affects:** Section 4.3, Section 5.1 (new subsection on capability model).

---

### 2.9 PoolScalingController

**What changes:** Separate `PoolScalingController` (desired pool config) from `WarmPoolController` (individual pod lifecycle). Pluggable `PoolScalingStrategy` interface.

Default formula:
```
target_minWarm = ceil(base_demand_p95 × variant_weight × safety_factor)
```

`safety_factor` defaults to 1.5 for `type: agent`, 2.0 for `type: mcp`. `scaleToZero` disabled by default for `type: mcp` pools. Experiment variant pool lifecycle managed automatically based on experiment status transitions.

**Affects:** Section 4.6, Section 5.2.

---

### 2.10 Experiment Primitives

**What changes:** `ExperimentDefinition` as first-class admin API resource. `ExperimentRouter` as built-in `RequestInterceptor`. Experiment context propagates through delegation leases via `propagation.childSessions: inherit`. Results API aggregates eval scores by variant.

**Affects:** Section 4.8, Section 15.1.

---

### 2.11 External Session Messaging (Client-Facing)

**What changes:** `POST /v1/sessions/{id}/messages` is the unified client-facing endpoint for all message types. MCP equivalent: `send_message` tool.

```json
{
  "message": {
    "id": "msg_c01",
    "from": { "kind": "client", "id": "user_123" },
    "inReplyTo": "req_001",
    "delivery": "immediate",
    "input": [OutputPart[]]
  }
}
```

Interrupt remains separate: `POST /v1/sessions/{id}/interrupt` — lifecycle signal, not content delivery.

**Affects:** Section 7.2, Section 15.1.

---

## Part 3: V1 Should-Have

---

### 3.1 Environment Resource and RBAC Model

**What changes:** `Environment` as first-class admin API resource. Named, RBAC-governed project context grouping runtimes and connectors for a team.

**Two access paths:**
- **Transparent filtering** (default) — user connects to standard endpoint (`/mcp`, `/v1`, `/v1/responses`). Gateway evaluates all environments where the user's groups have a role, computes the **union** of authorized runtimes and connectors, returns only those. The user is unaware of the environment machinery.
- **Explicit environment endpoint** (opt-in, `endpoint.enabled: true`) — dedicated paths across **all** external interfaces: `/mcp/environments/{name}`, `/v1/environments/{name}/sessions`, `/v1/environments/{name}/runtimes`, scoped model namespace on `/v1/responses` and `/v1/chat/completions`.

**Runtime and connector selection is tag-based** — not explicit lists:

```yaml
runtimeSelector:
  matchLabels:
    team: security
  matchExpressions:
    - key: approved
      operator: In    # In | NotIn | Exists | DoesNotExist
      values: [true]
  types: [agent, mcp]
  include: [legacy-auditor]    # override: include regardless of selector
  exclude: [unstable-scanner]  # override: exclude regardless of selector
```

**`mcpRuntimeFilters`** — capability-based tool filtering for `type: mcp` runtimes. Name collisions resolved by `runtime:tool` qualified reference in `overrides`.

**`connectorSelector`** — internal only. Controls what connectors agents can use via the platform MCP server. Uses same capability filter model as `mcpRuntimeFilters`.

**Capability filter uses MCP-inferred capabilities.** Environment policy never references tool names directly — only capability categories. `overrides` with qualified `runtime:tool` or `connector:tool` references provide escape hatch.

**Identity integration:** OIDC. Groups from LDAP/AD carried as JWT claims. Lenny never speaks LDAP directly — the IdP handles directory integration. `introspectionEnabled: true` enables real-time group membership checks at cost of per-request latency.

**`crossEnvironmentDelegation`:** `false` by default — sessions cannot delegate to runtimes in other environments unless explicitly permitted.

**No environments defined → `noEnvironmentPolicy`:** Configurable per tenant. `deny-all` (platform default) — no access until environments are created. `allow-all` — all authenticated tenant users see all runtimes. Platform-wide default configurable at Helm time.

**Full configuration template:**

```yaml
# Tenant RBAC config — PUT /v1/admin/tenants/{id}/rbac-config
tenantRbacConfig:
  tenantId: acme-corp
  noEnvironmentPolicy: deny-all    # deny-all | allow-all
  identityProvider:
    issuerUrl: https://idp.acme.com
    clientId: lenny-acme
    groupsClaim: groups
    usernameClaim: email
    groupFormat: short-name        # short-name | dn | uuid
  tokenPolicy:
    maxTtlSeconds: 3600
    introspectionEnabled: false
    introspectionEndpoint: https://idp.acme.com/introspect
  capabilities:
    inheritPlatformDefaults: true
    custom:
      - id: pii-access
        description: "Tools that read PII data"
      - id: financial-write
        description: "Write access to financial systems"
    allowedCapabilities:
      - read
      - write
      - delete
      - execute
      - network
      - search
      - admin
      - pii-access
      - financial-write
    defaultCapabilityForUnannotatedTools: admin
  mcpAnnotationMapping:
    readOnlyHint_true: [read]
    readOnlyHint_false_destructiveHint_false: [write]
    destructiveHint_true: [write, delete]
    openWorldHint_true: [network]
    noAnnotations: [admin]

# Environment definition — POST /v1/admin/environments
environment:
  name: security-team
  tenantId: acme-corp
  description: "Security engineering workspace"
  members:
    - identity:
        type: oidc-group    # oidc-group | oidc-user | service-account
        value: security-engineers
      role: creator         # viewer | creator | operator | admin
    - identity:
        type: oidc-group
        value: security-leads
      role: admin
    - identity:
        type: oidc-user
        value: alice@acme.com
      role: operator
    - identity:
        type: service-account
        value: sa-security-ci
      role: creator
  runtimeSelector:
    matchLabels:
      team: security
    matchExpressions:
      - key: approved
        operator: In
        values: [true]
      - key: experimental
        operator: DoesNotExist
    types: [agent, mcp]
    include: [legacy-code-auditor]
    exclude: [unstable-scanner]
  mcpRuntimeFilters:
    - runtimeSelector:
        matchLabels:
          category: execution
      allowedCapabilities: [read, execute]
      deniedCapabilities: [write, delete, admin]
      overrides:
        allow:
          - runtime: code-executor
            tool: execute_python
        deny:
          - runtime: code-executor
            tool: read_env_vars
    - runtimeSelector:
        matchExpressions:
          - key: team
            operator: Exists
      allowedCapabilities: [read]
  connectorSelector:
    matchLabels:
      team: security
    matchExpressions:
      - key: data-sensitivity
        operator: NotIn
        values: [restricted, confidential]
    include: [snyk-scanner]
    exclude: [prod-db-writer]
    allowedCapabilities: [read, search, network]
    deniedCapabilities: [write, delete, execute, admin]
    overrides:
      allow:
        - connector: github
          tool: create_issue
      deny:
        - connector: github
          tool: delete_repository
  defaultDelegationPolicy: security-policy
  crossEnvironmentDelegation: false
  endpoint:
    enabled: true
    path: security-team
    allowPublicDiscovery: false

# Connector labels — POST /v1/admin/connectors
connector:
  id: github
  labels:
    team: security
    data-sensitivity: internal
  toolCapabilityOverrides:
    - tool: run_workflow
      capabilities: [execute, network]
    - tool: list_repositories
      capabilities: [read, network]
```

**Member roles:**
- `viewer` — list runtimes, read session status and artifacts
- `creator` — viewer + create sessions, call `type: mcp` tools
- `operator` — creator + interrupt/resume/terminate sessions
- `admin` — operator + modify environment definition

**V1 data model accommodations (even if environments ship post-v1):**
- `Runtime` resources need `labels` map from v1
- `Connector` registrations need `labels` map from v1
- `type: mcp` runtime tool schemas are cached by the gateway from v1 for efficient capability filtering
- `GET /v1/runtimes` and `list_runtimes` accept optional `?environmentId=` filter parameter from v1 as a no-op stub

**Affects:** Section 5.1 (labels), Section 4.1 (identity-aware routing), Section 15.1, New Section 10 (RBAC).

---

### 3.2 `MemoryStore` Interface

**What changes:** Role-based storage interface. Default: Postgres + pgvector. Fully replaceable. Accessed via `lenny/memory_write` and `lenny/memory_query` on platform MCP server. Technology choice deferred — market not settled Q1 2026.

**Affects:** Section 4, Section 9.

---

### 3.3 Semantic Caching at the LLM Proxy

**What changes:** Optional `CachePolicy` on `CredentialPool`. Pluggable `SemanticCache` interface — default Redis, fully replaceable. Disabled by default.

**Affects:** Section 4.9.

---

### 3.4 `RequestInterceptor` Extension Point

**What changes:** Interceptor interface at phases: `PreAuth`, `PostAuth`, `PreRoute`, `PostRoute`, `PreToolResult`, `PostAgentOutput`. Built-in interceptors: `ExperimentRouter`, `GuardrailsInterceptor` (disabled, no built-in logic).

**Affects:** Section 4.8.

---

### 3.5 Agent Evaluation Hooks

**What changes:** `POST /v1/sessions/{id}/eval` accepts scored results. `POST /v1/sessions/{id}/replay` re-runs against different runtime version. No built-in eval logic.

**Affects:** Section 15.1, Section 16.

---

### 3.6 `CredentialRouter` Interface

**What changes:** Pluggable credential pool selection logic. Default: least-loaded / round-robin / sticky-until-failure. Disabled by default, opt-in per pool.

**Affects:** Section 4.9.

---

## Part 4: Planned / Post-V1

---

### 4.1 Multi-Turn Agent Dialog

Document now: `interaction: multi_turn` reserved in capabilities enum, hard-rejected in v1. Requires A2A protocol support alongside. `capabilities.protocols: [a2a]` required. `{type: "message"}` is the delivery vehicle when multi-turn activates.

---

### 4.2 A2A Full Support

Document now: Inbound at `/a2a/{runtimeName}/tasks` via `A2AAdapter`. Outbound via external agents registered as connectors. A2A `input-required` maps to Lenny `input_required`. `agentInterface` auto-generates A2A agent cards. Per-agent endpoints (not aggregated). `allowedExternalEndpoints` slot on delegation lease schema from v1.

---

### 4.3 A2A Intra-Pod Support

Document now: Adapter additionally serves per-agent A2A endpoints intra-pod. Adapter manifest gains A2A base URL. All A2A traffic proxied through gateway.

---

### 4.4 MCP Task Delivery Mode

Document now: `taskDelivery: mcp` on runtime — task arrives via `lenny/receive_task` tool call on runtime's own MCP server. `runtimeMcpServers` slot in adapter manifest reserved from v1.

---

### 4.5 Agent Protocol (AP) Support

Document now: AP defines `POST /ap/v1/agent/tasks` and step execution. Third `ExternalProtocolAdapter` implementation.

---

### 4.6 Environment Resource (Post-V1 Implementation)

Data model accommodations are v1 (see 3.1). Full environment management UI, advanced membership analytics, cross-environment delegation controls, and environment-level billing rollup are post-v1.

---

### 4.7 Future Conversational Patterns

Document now: `MessageEnvelope` with `id`, `from`, `inReplyTo`, `threadId` accommodates all of these without schema changes: threaded messages, multiple participants, non-linear context retrieval, broadcast, external agent participation.

---

### 4.8 UI and CLI

Document now: Admin API is the complete operational surface. Official CLI (`lenny-ctl`) and web portal are separate projects consuming the admin API as thin clients.

---

## Part 5: Explicit Non-Decisions

**5.1 No Model B Runtime Deployment.** No mechanism for packaging a graph definition as a runtime. Users register derived runtimes via admin API.

**5.2 No Built-In Eval Logic.** Lenny provides hooks and storage. No LLM-as-judge or hallucination detection.

**5.3 No Built-In Guardrail Logic.** Lenny provides the `RequestInterceptor` hook. No content classifiers or prompt injection detection.

**5.4 No Built-In Memory Extraction.** Lenny provides the `MemoryStore` interface and tools. Runtimes decide what to write.

**5.5 No Direct External Connector Access.** Connectors are session-internal in v1. External clients do not call connectors directly. Whether to add this later is an independent product decision; the data model accommodates it (connectors have labels, environments have `connectorSelector`) without requiring a redesign.

**5.6 Hooks-and-Defaults Design Principle.** Every cross-cutting AI capability follows the same pattern: defined as an interface with a sensible default, disabled unless explicitly enabled, fully replaceable. Lenny never implements AI-specific logic.

---

## Part 6: Revised Build Sequence

**Phase 1** — Core types: `Runtime` (unified, with `labels`), `type` field, `capabilities` (including `injection`), `executionMode`, `allowedExternalEndpoints` on delegation lease, `input_required` task state, messages array, `suspended` session state. Agent-sandbox CRDs. `Connector` resource with `labels`.

**Phase 2** — Replace adapter binary protocol: unified `{type:"message"}` (no separate `prompt`), multi-server MCP adapter + lifecycle channel, adapter manifest written before binary spawns. Publish as runtime adapter specification.

**Phase 3** — PoolScalingController. `DelegationPolicy` resource. `setupPolicy` enforcement. `taskPolicy.cleanupCommands`.

**New Phase between 4 and 5** — Admin API foundation: runtimes, pools, connectors, delegation policies, tenant management, external adapters registry. Gateway loads config from Postgres. Capability inference from MCP annotations at connector registration.

**Phase 5** — Gateway `ExternalAdapterRegistry`. MCP adapter, Completions adapter, Open Responses adapter all active. `list_runtimes`, `GET /v1/runtimes`, `GET /v1/models` with identity-aware filtering. `type: mcp` runtime endpoints at `/mcp/runtimes/{name}`. Tenant RBAC config API. `noEnvironmentPolicy` enforcement.

**Phase 9** — `lenny/delegate_task` handles internal and external targets. `lenny/send_message`, `lenny/request_input`, `lenny/get_task_tree`. `lenny/discover_agents` with policy scoping.

**Phase 10** — `agentInterface` in discovery. Adapter manifest includes summaries.

**Phase 12** — `type: mcp` runtime support. Concurrent execution modes.

**New Phase 15** — Environment resource: tag-based selectors, member RBAC, `mcpRuntimeFilters` with capability model, `connectorSelector`, explicit environment endpoints across all adapters.

**New Phase 16** — Experiment primitives, PoolScalingController experiment integration.

**New Phase 17** — Memory, caching, guardrail, eval hooks.

---

## Summary

| Tier | Count |
|---|---|
| V1 Must-Have Breaking | 18 |
| V1 Must-Have Feature | 11 |
| V1 Should-Have | 6 |
| Planned / Post-V1 | 8 |
| Explicit Non-Decisions | 6 |

---

## Human (Apr 2, 2026, 9:05 PM):

Couple more comments:
* Capabilities should be customizable per tenant, with the default ones being the ones you described
* If no environments are defined for a tenant, then either everything should be visible to all tenant's users or nothing. If only one option, then nothing, but I wonder if it's standard practice to allow all based on a per-tenant config flag.

Put it all together and summarize the RBAC approach. Include the config template, listing all possible config options

---

## Claude:

Good question to revisit, because the reasoning deserves scrutiny.

The stated constraint was: `interaction: multi_turn` requires A2A because A2A is the protocol that natively models multi-turn agent conversation, while MCP and AP don't. But looking at what we actually built in this conversation, that reasoning is weaker than it appeared.

Within Lenny, multi-turn dialog is already fully supported by the infrastructure we designed:

- `lenny/request_input` — a runtime can pause mid-task and request input from its caller, blocking until an answer arrives
- `{type: "message"}` deliveries — parents, siblings, and external clients can send messages to a running session at any time
- `input_required` task state — the task tree tracks and surfaces these pauses correctly
- `MessageEnvelope` with `inReplyTo` and `threadId` — the conversational threading model is already there

A runtime that uses `lenny/request_input` multiple times during a single task execution is already doing multi-turn dialog. The protocol infrastructure supports it regardless of what `capabilities.interaction` says.

What A2A actually enables is **external multi-turn interoperability** — a Lenny-hosted agent engaging in a multi-turn conversation with an agent outside the cluster using a standardized protocol. That's a real requirement but it's distinct from the runtime's internal interaction capability.

The correct statement is therefore: `interaction: multi_turn` does not require A2A. It simply requires that the runtime be designed to handle multiple `{type: "message"}` deliveries over the lifetime of a task rather than treating the first message as the only input. The `injection.supported: true` capability flag is actually the more relevant declaration — it's what tells the gateway the runtime is prepared to receive mid-task messages.

A2A should be decoupled from this: it's required for external multi-turn interoperability, not for the runtime capability declaration itself.

The spec note should be revised to: `interaction: multi_turn` is reserved in v1 and hard-rejected, not because A2A is absent, but because the semantics of what distinguishes a `multi_turn` runtime from an `injection`-capable `one_shot` runtime need to be precisely defined before the capability is exposed. The two overlap significantly and the boundary isn't clean yet.

---

## Human (Apr 2, 2026, 10:03 PM):

Revise your previous compilation of updates to the tech spec based on all we have talked about.

---

## Claude:

## Multi-Turn in V1

Given the infrastructure is already designed, this is mostly a definitional and enforcement change rather than new work.

**What `interaction: multi_turn` means precisely:** A runtime that declares `multi_turn` is designed to engage in back-and-forth dialog within a single task — it may call `lenny/request_input` multiple times, receive multiple `{type: "message"}` deliveries, and produce partial outputs before reaching a final response. The task does not end after the first `response` message on stdout.

**What `interaction: one_shot` means by contrast:** The runtime consumes the initial `{type: "message"}`, produces exactly one `{type: "response"}` carrying the final result, and the task ends. The runtime may still use `lenny/request_input` once (for a single clarification), but the interaction model is fundamentally request→response.

**What changes in v1:**
- `interaction: multi_turn` is valid — gateway no longer hard-rejects it
- `interaction: one_shot` runtimes that call `lenny/request_input` more than once get a gateway error on the second call
- `interaction: multi_turn` requires `capabilities.injection.supported: true` — the gateway enforces this at runtime registration. A multi-turn runtime that doesn't accept injections is incoherent.
- `lenny/send_to_child` activates — previously stubbed at `not_implemented`, it becomes a real tool call that delivers a `{type: "message"}` to a child session. This is the primary mechanism a parent uses to continue a multi-turn conversation with a child.
- `await_children` already unblocks on `input_required` (designed in 1.12) — no change needed there.

---

## A2A External Multi-Turn Interoperability

A2A's task model is structurally very close to Lenny's. The key A2A states are `submitted`, `working`, `input-required`, `completed`, `failed`, `canceled`. The `input-required` state is A2A's native multi-turn primitive — an agent pauses and surfaces a request to its caller, who responds with a new message to resume.

**Inbound: external A2A client talking to a Lenny-hosted agent**

An external agent or A2A client sends a task to Lenny's A2A endpoint:

```
POST /a2a/research-pipeline/tasks
{
  "id": "a2a-task-xyz",
  "message": {
    "role": "user",
    "parts": [{ "type": "text", "text": "Research ACME financials" }]
  }
}
```

The `A2AAdapter` translates this into a Lenny session: creates a session for `research-pipeline`, delivers the message as `{type: "message"}` on stdin, and returns a task handle to the A2A client.

When the Lenny runtime calls `lenny/request_input`, the gateway transitions the internal task to `input_required`. The A2A adapter translates this to A2A's `input-required` state and surfaces the question to the external caller:

```
GET /a2a/research-pipeline/tasks/a2a-task-xyz
→ {
    "id": "a2a-task-xyz",
    "status": { "state": "input-required" },
    "message": {
      "role": "agent",
      "parts": [{ "type": "text", "text": "Which time period?" }]
    }
  }
```

The external caller sends the answer:

```
POST /a2a/research-pipeline/tasks/a2a-task-xyz/messages
{
  "message": {
    "role": "user",
    "parts": [{ "type": "text", "text": "2020-2024" }]
  }
}
```

The A2A adapter maps this to `lenny/send_message` with `inReplyTo` set to the outstanding `requestId`. The gateway resolves the blocked `lenny/request_input` tool call. The runtime continues. Exactly the same path as a parent agent responding to a child — the A2A adapter is just another authorized sender.

This works cleanly because Lenny's `input_required` state and A2A's `input-required` state are semantically identical. The A2A adapter is a translation layer with no impedance mismatch on the multi-turn path.

**Outbound: a Lenny-hosted agent talking to an external A2A agent**

When a Lenny runtime calls `lenny/delegate_task` targeting an external A2A agent registered as a connector, the gateway's A2A client sends the task to the external agent's A2A endpoint. The task handle returned to the runtime is opaque — it looks identical to a handle for an internal child session.

If the external A2A agent enters `input-required`, the gateway detects this via polling or streaming subscription to the external agent's task status. The gateway maps it to Lenny's `input_required` path: the parent's `await_children` call unblocks with the question, the parent responds via `lenny/send_message`, and the gateway forwards the answer to the external agent as an A2A `tasks/{id}/messages` POST.

The runtime is entirely unaware it's talking to an external A2A agent vs an internal Lenny session. The gateway owns the translation.

**The one genuine gap: streaming output**

A2A supports streaming task output via SSE — the external agent pushes partial results while working. Lenny's internal model supports this too (`{type: "response"}` with streaming parts). The A2A adapter needs to bridge these: subscribe to the external agent's SSE stream, translate incoming A2A message parts into `OutputPart` events, and surface them through the parent's `await_children` streaming response. This is mechanical translation work but it's non-trivial to implement correctly, particularly around reconnection semantics if the SSE stream drops. Worth flagging as the primary implementation complexity for A2A multi-turn.

---

## Human (Apr 2, 2026, 9:24 PM):

Can you remind me why "4.1 Multi-Turn Agent Dialog" requires A2A?

---

## Claude:

Good. Let me think through what this actually requires in v1 rather than just moving it in the document.

---

## What MCP Task Delivery Mode Is

In the current design, all task delivery goes one way: adapter writes to the runtime's stdin. The runtime is a consumer. MCP task delivery mode inverts this for runtimes that are themselves MCP servers — the adapter connects to the runtime's MCP server as a client and calls a `lenny/receive_task` tool on it. The runtime is now a server receiving tasks through a standard MCP tool call rather than reading from stdin.

This matters because most modern agent frameworks — LangGraph, Claude Code, OpenAI Agents SDK — already expose an MCP server interface. They are MCP servers natively. Forcing them to also read stdin is an unnecessary wrapper. With MCP task delivery, a runtime that already speaks MCP requires no adaptation code at all.

---

## How It Works

**Runtime configuration:**

```yaml
name: langgraph-orchestrator
taskDelivery: mcp    # stdin (default) | mcp
```

**Adapter manifest when `taskDelivery: mcp`:** The `runtimeMcpServers` field — reserved but empty in the current design — is now populated with the runtime's own MCP server socket:

```json
{
  "platformMcpServer": {
    "socket": "/run/lenny/platform-mcp.sock"
  },
  "connectorServers": [
    { "id": "github", "socket": "/run/lenny/connector-github.sock" }
  ],
  "runtimeMcpServers": [
    {
      "name": "runtime",
      "socket": "/run/lenny/runtime-mcp.sock"
    }
  ],
  "sessionId": "sess_abc",
  "taskId": "task_root"
}
```

The socket path is agreed by convention before the binary starts — no race condition. The runtime starts its MCP server at this path; the adapter connects to it.

**Startup sequence differences from stdin mode:**

Steps 1–9 are identical. The divergence is at step 10:

```
Adapter                              Runtime (MCP delivery)
    |                                      |
    |  [binary is running]                 |-- [starts own MCP server
    |                                      |    at /run/lenny/runtime-mcp.sock]
    |                                      |
    |<- MCP connect (platform server) -----|-- [connects to platform MCP server
    |-- MCP capabilities response -------->|    and connector servers as usual]
    |                                      |
    |-- [adapter connects to runtime's     |
    |    MCP server as client]             |
    |<- MCP capabilities response ---------|
    |   [runtime advertises lenny/receive_task
    |    and any other tools it exposes]   |
    |                                      |
    |   [lifecycle handshake if Full tier] |
    |                                      |
    |-- MCP tool call: lenny/receive_task->|
    |   {input: [OutputPart[]]             |
    |    taskId: "task_root"               |-- [runtime processes task,
    |    sessionId: "sess_abc"}            |    calls lenny/output on platform
    |                                      |    MCP server to stream output,
    |                                      |    calls other tools as needed]
    |                                      |
    |<- MCP tool result: lenny/receive_task|
    |   {status: "completed"               |
    |    finalOutput: [OutputPart[]]}      |
```

**Output streaming:** The runtime calls `lenny/output` on the platform MCP server during processing — exactly as in stdin mode. `lenny/receive_task` blocks until the task is complete and returns a terminal status and final output. Intermediate output flows via `lenny/output` calls. This keeps the output path consistent between both delivery modes — the adapter doesn't need two different output collection strategies.

**Tool calls during processing:** Unchanged. The runtime calls `lenny/delegate_task`, `lenny/request_input`, connector tools, and all other platform tools through the platform MCP server — exactly as in stdin mode.

**`lenny/request_input` in MCP delivery mode:** When the runtime calls `lenny/request_input` on the platform MCP server, the gateway blocks that call exactly as in stdin mode. The `lenny/receive_task` call on the adapter side remains pending — the adapter is waiting for `lenny/receive_task` to return, which it won't until the task completes. The parent's `await_children` unblocks with `input_required` as usual. When the answer arrives, the gateway resolves `lenny/request_input` and the runtime continues.

---

## `lenny/receive_task` Contract

This is the one tool every `taskDelivery: mcp` runtime must implement. It's the only Lenny-specific contract for this delivery mode — and it's a receiving tool (the runtime implements it), not a calling tool.

```json
{
  "name": "lenny/receive_task",
  "description": "Receive and process a task from the Lenny platform",
  "inputSchema": {
    "type": "object",
    "properties": {
      "input": {
        "type": "array",
        "description": "OutputPart array carrying the task input"
      }
    },
    "required": ["input"]
  }
}
```

`taskId` and `sessionId` are **not** in the input schema — they are injected by the adapter as context, same principle as `from.id` in `MessageEnvelope`. The runtime doesn't need to know its own identity.

Return value:

```json
{
  "status": "completed | failed",
  "finalOutput": [OutputPart[]],
  "error": null
}
```

---

## What Changes in the Spec

**Integration tiers gain a new axis.** `taskDelivery` is orthogonal to the Minimum / Standard / Full tier:

| | stdin delivery | MCP delivery |
|---|---|---|
| Minimum | reads stdin, no MCP | not applicable — MCP delivery requires MCP |
| Standard | reads stdin + connects to platform/connector servers | implements `lenny/receive_task` + connects to platform/connector servers |
| Full | Standard + lifecycle channel | Standard (MCP delivery) + lifecycle channel |

A runtime using MCP delivery is always at least Standard tier — it must connect to the platform MCP server to call `lenny/output`. A Minimum-tier runtime by definition doesn't use MCP at all, so stdin is its only option.

**`taskDelivery: stdin` remains the default.** Existing runtimes are unaffected. MCP delivery is opt-in.

**Adapter manifest `runtimeMcpServers` field is no longer reserved-but-empty** — it is actively populated for `taskDelivery: mcp` runtimes and documented as a live field.

**`lenny/receive_task` is added to the runtime adapter specification** as a required tool for `taskDelivery: mcp` runtimes. It's documented in the same spec section as the stdin binary protocol, as an alternative delivery contract.

---

## Build Sequence Impact

This belongs in **Phase 2** alongside the rest of the adapter protocol work — both delivery modes should ship together so the reference implementation demonstrates both. The `taskDelivery` field on `Runtime` is part of Phase 1 core types since it's a schema field.

---

## Human (Apr 2, 2026, 9:28 PM):

Make multi-turn a must-have in v1.

As for A2A, how will it work for external multi-turn operability?

---

## Claude:

You're right to question it. Nothing, for the session and task execution modes.

The adapter spawns the runtime binary with stdin piped. The initial task is the first `{type: "message"}` the runtime reads from stdin. That's already true. A runtime that connects to the platform MCP server for tool calls (Standard tier) still receives its task via stdin — the "MCP" aspect of its integration is about the tools it calls outbound, not about how the task arrives. There's no reason to add `lenny/receive_task` for these runtimes. The task delivery mechanism and the tool-calling mechanism are orthogonal.

The one case where stdin genuinely doesn't work is **`executionMode: concurrent`**. Stdin is a single channel per process. If a pod is handling eight simultaneous task slots, you can't deliver eight independent tasks via one stdin stream — there's no way to route slot-specific input correctly. Each concurrent slot needs its own delivery channel. That's where `lenny/receive_task` earns its place: the adapter connects to the runtime's MCP server and calls `lenny/receive_task` once per slot, each call carrying the slot's task and blocking until that slot's task completes. Eight concurrent slots = eight simultaneous `lenny/receive_task` calls, each independently tracked by the gateway.

So the spec should say: MCP task delivery is not a separate runtime mode alongside stdin. It's the delivery mechanism specifically for concurrent execution mode. Session and task mode runtimes always receive tasks via stdin `{type: "message"}`. Concurrent mode runtimes receive tasks via `lenny/receive_task` on their own MCP server because stdin can't multiplex.

This simplifies the design considerably. `taskDelivery` as a runtime configuration field disappears. The delivery mechanism is determined entirely by `executionMode` — the runtime author doesn't choose it. A runtime that wants to support concurrent execution implements `lenny/receive_task`; one that doesn't, doesn't. The integration tier model (Minimum / Standard / Full) remains based on which outbound MCP connections the runtime makes, not on how it receives tasks.

---

## Human (Apr 2, 2026, 9:37 PM):

Ok great, thanks. This can be post-v1.

On the other, these should be must-haves in v1:
* MCP Task Delivery Mode

---

## Claude:

Let me think through both concurrent styles carefully before answering.Yes, there is a genuine gap. Let me be precise about where it is.

---

## Where the Gap Is

For `concurrent-workspace` mode, the runtime processes multiple simultaneous tasks — each with its own materialized workspace, credentials, and lifecycle. Stdin is a single channel per process. There is no way to deliver multiple independent concurrent task inputs through one stdin pipe and have the runtime correctly route each input to the right in-flight task. The binary protocol is fundamentally single-threaded in its delivery model.

For `concurrent-stateless` mode there is no gap, because the gateway routes through a Kubernetes Service and the runtime receives requests via its own server interface. There is no stdin delivery at all in the stateless path — the runtime is always-on and accepts connections directly.

---

## The Root Cause

The decision to eliminate `lenny/receive_task` was correct for session mode and task mode, where one task is always in flight per pod. The argument was: "stdin delivers the initial message, same as any other message." But that reasoning breaks down for concurrent-workspace because you cannot multiplex N simultaneous task streams through one stdin pipe.

---

## The Resolution

Concurrent-workspace runtimes must expose their own local MCP server. The adapter connects to it and makes one `lenny/receive_task` call per slot — each call carrying its slot's input, blocking until that slot's task completes. The shared expensive state is managed internally by the runtime's server process, across all concurrent slots.

This actually maps cleanly onto a refined understanding of `type`:

- `type: agent` + `executionMode: session` or `task` → stdin delivery. Single task in flight. Binary protocol is correct.
- `type: agent` + `executionMode: concurrent` → requires the runtime to expose a local MCP server. Per-slot `lenny/receive_task` delivery. N tasks in flight simultaneously.
- `type: mcp` → runtime is a pure MCP server. No task delivery at all — callers connect and call tools directly.

`concurrent-stateless` is the special case: the runtime still exposes an MCP server, but instead of `lenny/receive_task`, the adapter routes through Kubernetes Service and the runtime handles requests natively through its own tools.

---

## What Needs to Change in the Spec

**`lenny/receive_task` is reinstated, but scoped precisely.** It is not a general task delivery mechanism. It is the delivery mechanism specifically for `concurrent-workspace` mode. It is the only Lenny-specific tool a concurrent-workspace runtime must implement on its own MCP server. The tool call per slot carries the task input and blocks until complete, returning the slot's output.

**`concurrent-workspace` runtimes must declare an MCP server socket** in their runtime configuration, same as `type: mcp` runtimes:

```yaml
name: multi-slot-analyst
type: agent
executionMode: concurrent
concurrencyStyle: workspace
maxConcurrent: 4
mcpServerSocket: /run/lenny/runtime-mcp.sock
```

The adapter manifest `runtimeMcpServers` section — reserved from v1 for this purpose — is populated when the pod starts.

**`concurrent-stateless` runtimes** do not need `lenny/receive_task` — they are called through K8s Service routing via their own native tools, same as `type: mcp` runtimes.

**The adapter manifest and integration tiers are unaffected** for `session` and `task` mode runtimes. Stdin remains their delivery mechanism. The gap exists only in concurrent-workspace.

---

## Human (Apr 2, 2026, 9:40 PM):

What does lenny/receive_task that an input request/message can't do already?

---

## Claude:

Yes. The gap is a multiplexing problem — one channel, multiple concurrent streams — and that's a well-solved problem in protocol design. HTTP/2 does exactly this over TCP using stream IDs.

The fix is minimal: add a `slotId` field to stdin/stdout messages. The adapter assigns a slot ID when it starts each concurrent slot's task. The runtime reads the slot ID and dispatches to the right in-flight handler. Responses on stdout carry the same slot ID so the adapter can route them back to the correct client.

```json
// Adapter → Runtime stdin (slot 1 starting)
{"type": "message", "slotId": "slot_1", "input": [...]}

// Adapter → Runtime stdin (slot 2 starting, interleaved)
{"type": "message", "slotId": "slot_2", "input": [...]}

// Runtime → Adapter stdout (slot 2 responds first)
{"type": "response", "slotId": "slot_2", "parts": [...]}

// Runtime → Adapter stdout (slot 1 responds)
{"type": "response", "slotId": "slot_1", "parts": [...]}
```

Tool calls and all other outbound message types carry `slotId` too:

```json
{"type": "tool_call", "slotId": "slot_1", "tool": "lenny/delegate_task", "args": {...}}
```

The adapter matches tool call results back to the right slot before writing the tool result to stdin:

```json
{"type": "tool_result", "slotId": "slot_1", "result": {...}}
```

---

## What This Requires From the Runtime

A concurrent-workspace runtime must implement a dispatch loop rather than a simple sequential read loop. It reads each stdin message, checks `slotId`, and hands off to the correct concurrent handler — a goroutine, async task, thread, whatever the runtime's language provides. The shared expensive state (model weights, large index) is accessed from all handlers. Each handler maintains its own output state.

This is not a new obligation for concurrent-workspace runtimes — they were already required to manage concurrent processing of multiple simultaneous tasks. The `slotId` field just gives the delivery channel the structure needed to carry multiple independent streams.

---

## Backward Compatibility

`slotId` is absent (or null) in session mode and task mode messages. Runtimes that only implement those modes never see it and need no changes. Concurrent-workspace runtimes declare `executionMode: concurrent, concurrencyStyle: workspace` and opt into the multiplexed protocol. The adapter knows from the runtime's configuration which protocol variant to use — it never sends `slotId` to a session or task mode runtime.

---

## What This Removes From the Design

`lenny/receive_task` is gone entirely. `mcpServerSocket` is not needed on `type: agent` runtimes. The adapter manifest `runtimeMcpServers` slot remains reserved for the external caller case (where another runtime connects to a `type: mcp` runtime through the manifest), but is not used for task delivery.

MCP Task Delivery Mode is removed from both v1 and the post-v1 backlog. The concurrent-workspace gap is closed by `slotId` multiplexing over the existing binary protocol.

---

## Human (Apr 2, 2026, 9:43 PM):

Hold on -are you saying the design of the concurrent mode has a gap? Can you double check this?

---

## Claude:

Good. The current spec entry treats the Environment as a bullet point summary. Here's the full elaboration.

---

## 4.6 Environment Resource

### Concept

An **Environment** is a named, RBAC-governed project context — a team's view of Lenny. It defines which runtimes and connectors are visible and usable by its members, enforces capability-level tool filtering, and optionally exposes a dedicated endpoint across all external interfaces.

Environments address a gap between the two existing access control constructs. Tenant-level isolation is too coarse — multiple teams within a tenant need different scoped views. DelegationPolicy is too fine-grained — it controls what a runtime is allowed to do, not what a team is allowed to see. An Environment sits between these: it defines the universe of resources a team has access to. DelegationPolicy constrains behavior within that universe.

```
Tenant
  └── Environment(s)
        ├── runtimeSelector    — which runtimes exist in this project context
        ├── connectorSelector  — which connectors are available to agents internally
        ├── mcpRuntimeFilters  — capability-based tool filtering on type:mcp runtimes
        └── members            — who can create sessions and call tools here
```

---

### Access Paths

**Transparent identity-based filtering** is the default and the primary path. A user connects to the standard endpoint — `/mcp`, `/v1`, `/v1/responses`, `/v1/chat/completions` — and sees only what their identity authorizes. The gateway reads the JWT, resolves group memberships to environments, computes the union of authorized runtimes and connectors across all environments the user belongs to, and returns that filtered view. The user sees nothing of the environment machinery. This is how Kubernetes RBAC works: `kubectl get pods` returns what your identity permits without requiring a special endpoint per team.

**Explicit environment endpoints** are opt-in per environment. When `endpoint.enabled: true`, the environment is additionally reachable at dedicated paths that apply uniformly across all external interfaces:

| Interface | Path |
|---|---|
| MCP | `/mcp/environments/{slug}` |
| REST | `/v1/environments/{slug}/sessions`, `/v1/environments/{slug}/runtimes` |
| Open Responses | `/v1/responses` with `model` namespace scoped to environment |
| OpenAI Completions | `/v1/chat/completions` with model namespace scoped to environment |

An explicit endpoint is useful for CI/CD pipelines, agent orchestrators, and automated systems that want deterministic scoping — "I am operating in the security project, show me nothing else." Both paths are active simultaneously; enabling an explicit endpoint doesn't disable transparent filtering on standard endpoints.

---

### Resource Schema

```yaml
name: security-team
tenantId: acme-corp
description: "Security engineering workspace"

# ── Member RBAC ─────────────────────────────────────────────
members:
  - identity:
      type: oidc-group         # oidc-group | oidc-user | service-account
      value: security-engineers
    role: creator
  - identity:
      type: oidc-group
      value: security-leads
    role: admin
  - identity:
      type: oidc-user
      value: alice@acme.com
    role: operator
  - identity:
      type: service-account
      value: sa-security-ci
    role: creator

# ── Runtime Selector ────────────────────────────────────────
runtimeSelector:
  matchLabels:
    team: security
  matchExpressions:
    - key: approved
      operator: In            # In | NotIn | Exists | DoesNotExist
      values: ["true"]
    - key: experimental
      operator: DoesNotExist
  types: [agent, mcp]         # optional type filter
  include: [legacy-code-auditor]   # include regardless of selector
  exclude: [unstable-scanner]      # exclude regardless of selector

# ── MCP Runtime Filters ─────────────────────────────────────
# Capability-based tool filtering for type:mcp runtimes.
# Applies when a member calls tools via /mcp/runtimes/{name}
# or when an agent calls them via the adapter manifest.
mcpRuntimeFilters:
  - runtimeSelector:
      matchLabels:
        category: execution
    allowedCapabilities: [read, execute]
    deniedCapabilities: [write, delete, admin]
    overrides:
      allow:
        - runtime: code-executor
          tool: execute_python
      deny:
        - runtime: code-executor
          tool: read_env_vars
  - runtimeSelector:
      matchExpressions:
        - key: team
          operator: Exists
    allowedCapabilities: [read]

# ── Connector Selector ──────────────────────────────────────
# Controls which connectors are available to agents running
# sessions in this environment. Internal only — external
# clients never call connectors directly.
connectorSelector:
  matchLabels:
    team: security
  matchExpressions:
    - key: data-sensitivity
      operator: NotIn
      values: [restricted, confidential]
  include: [snyk-scanner]
  exclude: [prod-db-writer]
  allowedCapabilities: [read, search, network]
  deniedCapabilities: [write, delete, execute, admin]
  overrides:
    allow:
      - connector: github
        tool: create_issue
    deny:
      - connector: github
        tool: delete_repository

# ── Session Policy ──────────────────────────────────────────
defaultDelegationPolicy: security-policy
crossEnvironmentDelegation: false   # agents cannot delegate
                                    # to runtimes in other
                                    # environments by default

# ── Explicit Endpoint ───────────────────────────────────────
endpoint:
  enabled: true
  path: security-team
  allowPublicDiscovery: false
```

---

### Member Roles

| Role | Permissions |
|---|---|
| `viewer` | List runtimes, read session status and artifacts |
| `creator` | viewer + create sessions, call `type:mcp` tools |
| `operator` | creator + interrupt, resume, terminate sessions |
| `admin` | operator + modify this environment's definition |

---

### Runtime and Connector Selectors

Selectors use Kubernetes-style label expression syntax. Conditions are ANDed. `include` and `exclude` lists override selector results for specific named runtimes or connectors.

Runtimes must declare `labels` on their definition for selectors to match:

```yaml
name: code-auditor
type: agent
labels:
  team: security
  approved: "true"
  category: analysis
```

The selector is evaluated at request time, not at environment creation time. A new runtime tagged `team: security` after the environment was created is automatically visible to members. A runtime that loses its tags is immediately invisible.

---

### Capability-Based Tool Filtering

Tool filtering in `mcpRuntimeFilters` and `connectorSelector` uses capabilities inferred from MCP `ToolAnnotations`, not tool names. This prevents maintainability problems from explicit name lists and avoids name collisions across servers.

**Capability inference from MCP annotations (platform default, tenant-overridable):**

| MCP annotation | Inferred capabilities |
|---|---|
| `readOnlyHint: true` | `read` |
| `readOnlyHint: false, destructiveHint: false` | `write` |
| `destructiveHint: true` | `write, delete` |
| `openWorldHint: true` | `network` |
| No annotations | `admin` (conservative default) |

The gateway infers capabilities at connector or `type:mcp` runtime registration time by reading `tools/list`. No manual annotation required from connector authors. Admins can override inferred capabilities via `toolCapabilityOverrides` on the connector registration:

```yaml
connector:
  id: github
  labels:
    team: security
  toolCapabilityOverrides:
    - tool: run_workflow
      capabilities: [execute, network]
```

When a capability filter is violated, the gateway returns `tool_not_found` — indistinguishable from a tool that doesn't exist, consistent with the non-enumeration principle applied throughout.

---

### Internal vs External Effects

**External — what members see:** `list_runtimes`, `GET /v1/runtimes`, and `GET /v1/models` return only runtimes the member's environments authorize. `/mcp/runtimes/{name}` for `type:mcp` runtimes is accessible only if that runtime is in scope for the member. The responses are identical in shape to non-environment-scoped responses — members are unaware of the filtering.

**Internal — what agents see:** `lenny/discover_agents` returns only runtimes within the calling session's environment. `lenny/delegate_task` enforces `crossEnvironmentDelegation: false` at the gateway — a delegation attempt targeting a runtime not in the current environment fails with `target_not_in_scope`. The adapter manifest lists only connector servers that are in scope for the environment.

**`crossEnvironmentDelegation: false`** (the default) creates a containment boundary: agents cannot escape their environment through delegation. When set to `true`, delegation follows the DelegationPolicy rules without environment scoping. This is appropriate for platform-level orchestrators that need to span environments, but should be explicitly opted into.

---

### Identity and OIDC Integration

Lenny speaks OIDC. LDAP and Active Directory are handled by the identity provider (Keycloak, Okta, Azure Entra ID) — Lenny never speaks LDAP directly. The IdP issues JWTs carrying group memberships as claims. The gateway reads the configured `groupsClaim` field from the tenant's RBAC config.

```yaml
tenantRbacConfig:
  identityProvider:
    issuerUrl: https://idp.acme.com
    clientId: lenny-acme
    groupsClaim: groups
    usernameClaim: email
    groupFormat: short-name    # short-name | dn | uuid
  tokenPolicy:
    maxTtlSeconds: 3600
    introspectionEnabled: false
    introspectionEndpoint: https://idp.acme.com/introspect
```

Group membership is evaluated from the JWT at request time. When `introspectionEnabled: false`, membership is trusted for the token's lifetime — changes propagate when the token is re-issued. When `introspectionEnabled: true`, the gateway calls the introspection endpoint on every request for real-time group membership accuracy at the cost of per-request latency. This is the standard OIDC trade-off and is deployer's choice.

---

### `noEnvironmentPolicy`

When no environments are defined for a tenant, behavior is controlled by `noEnvironmentPolicy`:

- `deny-all` — no user can create sessions or list runtimes. Admins must define environments before the tenant is usable. This is the **platform default** and the right default for multi-tenant enterprise deployments.
- `allow-all` — all authenticated tenant users see and can use all runtimes and connectors. Appropriate for small teams, internal deployments, or development tenants.

The platform-wide default is configurable at Helm time. Individual tenants override it in their RBAC config.

---

### Admin API

```
GET    /v1/admin/environments
POST   /v1/admin/environments
GET    /v1/admin/environments/{name}
PUT    /v1/admin/environments/{name}
DELETE /v1/admin/environments/{name}

GET    /v1/admin/tenants/{id}/rbac-config
PUT    /v1/admin/tenants/{id}/rbac-config
```

`PUT` on an environment with `?dryRun=true` returns the set of runtimes and connectors that would be in scope under the proposed configuration, which is essential for validating selector expressions before committing.

`DELETE` on an environment does not delete any runtimes or connectors — it removes the scoping configuration. Active sessions in that environment continue until they complete naturally. New sessions cannot be created against the deleted environment after the deletion takes effect, which is immediate via Redis pub/sub invalidation.

---

### V1 Data Model Accommodations

The Environment resource itself is a should-have that ships post-v1, but the following data model slots must exist from v1 to allow additive implementation:

- `Runtime` resources must have a `labels` map field from v1
- `Connector` registrations must have a `labels` map field from v1
- `type:mcp` runtime tool schemas are cached by the gateway from v1 for efficient capability filtering at query time without requiring a live connection on every request
- `GET /v1/runtimes` and `list_runtimes` accept an optional `?environmentId=` filter parameter from v1 as a no-op stub — existing clients don't need to change their calls when environments ship
- Session creation accepts an optional `environmentId` parameter from v1 as a no-op — sessions created with `environmentId` behave identically to sessions without it until environments are implemented, at which point the environment context is enforced

None of these require the full environment machinery to be implemented. They are additive schema slots that make environment support non-breaking when it ships.

---

### Relationship to Other Concepts

| Concept | Relationship |
|---|---|
| **Tenant** | The parent scope. An environment belongs to exactly one tenant. A tenant can have many environments. |
| **DelegationPolicy** | Orthogonal. Environment defines what resources exist in a project context. DelegationPolicy constrains what a runtime is allowed to do within that context. Effective scope = `environment definition ∩ delegation policy`. |
| **publishedMetadata visibility** | Complementary. A runtime with `visibility: public` in `publishedMetadata` has its metadata publicly discoverable. But session creation still requires the caller to be in an environment that includes that runtime. Public visibility means "discoverable without auth," not "usable without auth." |
| **Capability filtering** | Derived from MCP annotations. Environments inherit the tenant's capability taxonomy. Tool filters in `mcpRuntimeFilters` and `connectorSelector` reference capability IDs, not tool names. |

---

## Human (Apr 2, 2026, 9:47 PM):

I've changed my mind on MCP Task Delivery Mode. I don't want this in v1.

To solve the concurrent-workspace gap, isn't there a way to solve this gap with stdin?

---

## Claude:

The current entry is a placeholder that lists deferred work without explaining the rationale or the boundary between what ships in v1 and what doesn't. Here's the elaboration.

---

### 4.6 Environment Resource — Post-V1 Implementation

The Environment resource ships as a should-have in v1 (see 3.1), but several aspects of the feature are intentionally deferred. This section documents what is deferred, why, and what v1 must do to ensure these additions are non-breaking when they land.

---

**Full environment management UI**

The admin API ships in v1 — `GET/POST/PUT/DELETE /v1/admin/environments` is part of the operational plane. What is deferred is tooling built on top of it: a web UI for browsing environments, editing membership, previewing selector matches, and visualizing which runtimes are in scope. This is thin-client work that consumes the admin API without any business logic in the UI layer. It is deferred because it adds no architectural weight — it can be built at any point without spec changes.

The `?dryRun=true` parameter on `PUT /v1/admin/environments` is the preview mechanism that the UI will use. It must be implemented in v1 alongside the API itself.

---

**Advanced membership analytics**

Basic membership is v1 — the environment stores its member list and the gateway evaluates it at request time. Deferred is a higher-level operational view: who has access to what across all environments in a tenant, which users have overlapping environment memberships, which runtimes are exposed to which groups, and drift detection between IdP group membership and environment access grants.

These are read-only analytical queries over data that already exists in the gateway's Postgres store. They require no schema changes — only query surfaces and reporting endpoints. Deferred because the priority is correctness of enforcement, not reporting sophistication.

---

**Cross-environment delegation controls**

The v1 environment resource has a `crossEnvironmentDelegation: false` boolean. This is sufficient for the common case: agents are contained within their environment and cannot delegate to runtimes in other environments.

What is deferred is a richer model: explicit allow-lists of which environments a session may delegate across, directional permissions (environment A may call into environment B but not vice versa), and runtime-level cross-environment exceptions. The design for this must integrate cleanly with `DelegationPolicy` rather than duplicating it. The interaction between environment scoping and delegation policy narrowing is non-trivial and warrants a separate design pass once the base environment feature is in production and the use cases are clearer.

**v1 constraint:** The boolean `crossEnvironmentDelegation` must be designed so that `true` is an escape hatch to the existing delegation policy model, not a different model. When `true`, delegation follows `DelegationPolicy` rules without environment scoping. This keeps the future richer model additive — it will replace the boolean with a more expressive structure, but the boolean behavior when `true` must remain valid semantics in the richer model.

---

**Environment-level billing rollup**

Session-level billing exists in v1 — every session carries `tenantId` and the billing event stream includes it. What is deferred is tagging billing events with `environmentId` and exposing `GET /v1/admin/environments/{name}/usage` as a cost rollup endpoint.

This requires one schema addition: `environmentId` on the billing event record. This field must be reserved in the v1 billing event schema even if it is null for all events until environments ship — retrofitting it later requires a migration across potentially millions of billing records. The field exists; the population logic is what is deferred.

**v1 constraint:** Add `environmentId` as a nullable field to the billing event schema in Phase 1. Populate it as null until the environment feature activates. No migration required when environments ship — the gateway begins populating the field on new events.

---

**What this means for the build sequence**

The Environment resource ships in Phase 15 of the build sequence (as specified). The data model accommodations — runtime `labels`, connector `labels`, `type:mcp` tool schema caching, `?environmentId` parameter stubs, `environmentId` on billing events — are all Phase 1 concerns. The gap between Phase 1 and Phase 15 is intentional: deployers benefit from the rest of the platform without needing environment management, and the data model is ready when they do.

---

## Human (Apr 2, 2026, 9:50 PM):

Elaborate on 4.6 Environment Resource

---

## Claude:

Understood. Here's the updated section 4.6 with both items promoted, and what each requires in v1.

---

### 4.6 Environment Resource — Post-V1 Implementation

**Full environment management UI**

Deferred. The admin API ships in v1 — `GET/POST/PUT/DELETE /v1/admin/environments` is part of the operational plane. The web UI for browsing environments, editing membership, and previewing selector matches is thin-client work over the admin API. Can be built at any point without spec changes. The `?dryRun=true` parameter on `PUT /v1/admin/environments` is the preview mechanism the UI will use and must be implemented in v1 alongside the API itself.

**Cross-environment delegation controls**

Deferred. The v1 `crossEnvironmentDelegation: false` boolean is sufficient for the common case. The richer model — explicit allow-lists between environments, directional permissions, runtime-level exceptions — requires a design pass integrating with `DelegationPolicy` once the base feature is in production and use cases are clearer. v1 constraint: `crossEnvironmentDelegation: true` must be the escape hatch to the existing delegation policy model, not a parallel one, so the future richer model is purely additive.

---

**Environment-level billing rollup — V1**

Promoted to v1. Every session carries `tenantId` in billing events from v1. Adding environment-level rollup requires one schema field and one API endpoint.

`environmentId` on billing events must be populated from v1 — not reserved-as-null. When a session is created in an environment context (via explicit `environmentId` parameter or transparent filtering), the gateway writes `environmentId` on every billing event for that session. Sessions created without environment context carry `environmentId: null`.

New endpoint in v1:

```
GET /v1/admin/environments/{name}/usage
    ?period=2026-04                    # required
    &groupBy=runtime|user|connector    # optional
    &include=tree                      # include child session usage
```

Returns usage aggregated to the environment — session counts, token counts, pod-minutes, credential lease minutes, and cost estimate — filtered to sessions whose billing events carry the environment's id. This is a read-only query over the existing billing event store; no new storage is required beyond the `environmentId` field.

This endpoint is consistent with `GET /v1/usage` (tenant-level) and `GET /v1/sessions/{id}/usage` (session-level). The three together form a complete usage hierarchy: tenant → environment → session.

**Build sequence impact:** `environmentId` on billing events is Phase 1. The `/usage` endpoint is Phase 15 alongside the rest of the environment resource.

---

**Advanced membership analytics — V1**

Promoted to v1. Basic membership enforcement — the environment stores its member list and the gateway evaluates it at request time — is the core feature. Analytics are operational queries over data that already exists.

Three endpoints added to v1:

```
GET /v1/admin/tenants/{id}/access-report
```

Returns a cross-environment access matrix for the tenant: for each group or user visible to the tenant, which environments they have access to and at what role. Useful for security reviews and access audits. Computed from the environment member lists — no additional storage required.

```
GET /v1/admin/environments/{name}/access-report
```

Scoped to a single environment. Returns the full resolved member list including group expansion — each group in the member list is expanded to its constituent users as of the last JWT-based membership evaluation. Includes last-seen timestamp per member (from audit log) and role. Flagged members are those whose group membership has not been refreshed within the token TTL window.

```
GET /v1/admin/environments/{name}/runtime-exposure
```

Returns which runtimes and connectors are in scope for this environment, the selector expression that matched each one, the effective capability filter applied to each `type:mcp` runtime, and which member roles have access to each. Equivalent to `?dryRun=true` on the environment definition but served as a persistent read endpoint rather than a mutation preview.

All three are read-only and query-only. They add no write paths, no new storage, and no new schema. They are reporting endpoints over data the gateway already tracks. The main implementation cost is the group expansion in the access report, which requires either caching last-seen group membership from JWTs or calling the IdP's group API — the latter is only available when `introspectionEnabled: true`.

**Build sequence impact:** All three endpoints ship in Phase 15 alongside the environment resource.

---

## Human (Apr 2, 2026, 9:53 PM):

Thanks. I actually meant if you can elaborate on the following:

4.6 Environment Resource (Post-V1 Implementation)
Data model accommodations are v1 (see 3.1). Full environment management UI, advanced membership analytics, cross-environment delegation controls, and environment-level billing rollup are post-v1.

---

## Claude:

The design needs to satisfy three distinct use cases, which are worth naming before proposing the mechanism:

**Shared platform services.** A `platform` environment hosts a `code-executor` runtime that every team's agents should be able to use. Today this requires duplicating the runtime into every team's environment or setting `crossEnvironmentDelegation: true` everywhere — both wrong.

**Directional service relationships.** The research team's orchestrators need to call into the analytics team's specialized agents, but analytics agents should not call back into research. A directional model is required.

**Runtime-level exceptions.** A single runtime in environment A needs to call a specific runtime in environment B, without general cross-environment access.

---

## The Model: Bilateral Declaration with Wildcard Support

The cleanest analogy is VPC peering — both sides must agree. This mirrors how enterprise access control works and prevents one environment's admin from unilaterally reaching into another team's namespace.

**Outbound declaration (on the calling environment):** This environment's agents may delegate to runtimes in these target environments, subject to the specified runtime selector.

**Inbound declaration (on the target environment):** These source environments may delegate to runtimes within this environment. A wildcard `"*"` handles the shared services case.

**Effective cross-environment access requires both sides to permit it.** If the outbound declaration is absent, or the inbound declaration is absent (or doesn't match), the delegation is denied. This is enforced by the gateway at delegation time.

**DelegationPolicy still applies on top.** Cross-environment allowed runtimes are added to the session's effective universe. If the DelegationPolicy doesn't permit the runtime by name or tag, it remains inaccessible regardless.

The complete effective scope becomes:

```
(environment definition ∪ cross-environment permitted runtimes) ∩ delegation policy
```

---

## Schema

```yaml
# On the calling environment (research-team)
crossEnvironmentDelegation:
  outbound:
    - targetEnvironment: platform-services
      runtimes:
        matchLabels:
          shared: "true"
        # connector access across environments is never permitted —
        # connectors are always internal to the session's home environment

    - targetEnvironment: analytics-team
      runtimes:
        ids: [analytics-agent]
        # specific runtime only — no label-based match


# On the target environment (platform-services)
crossEnvironmentDelegation:
  inbound:
    - sourceEnvironment: "*"          # any environment in this tenant
      runtimes:
        matchLabels:
          shared: "true"             # only runtimes labeled shared

    - sourceEnvironment: research-team
      runtimes:
        matchLabels:
          team: platform             # broader access for research specifically


# On the target environment (analytics-team)
crossEnvironmentDelegation:
  inbound:
    - sourceEnvironment: research-team
      runtimes:
        ids: [analytics-agent]       # only this specific runtime
```

---

## Runtime-Level Labeling Pattern

The `shared: "true"` label on a runtime is the primary mechanism for platform services. A runtime author labels it appropriately, the platform environment declares an open inbound wildcard for those runtimes, and any environment that declares outbound access to platform-services can reach them. Adding a new shared runtime requires only a label — no environment definition changes.

```yaml
name: code-executor
type: mcp
labels:
  team: platform
  shared: "true"
```

---

## Interaction With the Boolean

The existing `crossEnvironmentDelegation: false | true` boolean becomes shorthand:

- `false` (default) — no outbound declarations, no inbound declarations. Fully contained.
- `true` — environment's agents can delegate to any runtime permitted by DelegationPolicy, ignoring environment scoping. This remains the escape hatch for platform-level orchestrators.

The boolean and the structured `outbound/inbound` form are mutually exclusive on any given environment. An environment has either the boolean or the structured form, not both. The boolean is preserved for backward compatibility and ergonomics for simple cases.

---

## What the Gateway Does at Delegation Time

When a runtime calls `lenny/delegate_task(target: "analytics-agent")`:

1. Gateway resolves `analytics-agent` to a runtime in `analytics-team` environment.
2. Checks calling session's environment (`research-team`) for an outbound declaration targeting `analytics-team` with a selector matching `analytics-agent`. If absent → `target_not_in_scope`.
3. Checks `analytics-team`'s inbound declaration for a rule that permits `research-team` with a selector matching `analytics-agent`. If absent → `target_not_in_scope`.
4. Both sides permit it. Add `analytics-agent` to the session's effective runtime universe for this delegation.
5. Apply DelegationPolicy as normal. If DelegationPolicy doesn't permit `analytics-agent` → `target_not_authorized`.

Steps 2 and 3 are evaluated together in a single gateway policy check. The runtime never sees the environment layer — it calls `lenny/delegate_task` with a target name and either gets a task handle or a policy error.

---

## Connectors Are Not Cross-Environment

Cross-environment delegation is runtime-to-runtime only. Connectors remain scoped to the session's home environment. An agent running in `research-team` that delegates to `analytics-agent` in `analytics-team` — that child session's connectors are determined by `analytics-team`'s `connectorSelector` and the child session's delegation lease, not by `research-team`'s connector configuration. This is the correct behavior: the child agent operates in its own environment's context.

---

## What This Replaces

The boolean `crossEnvironmentDelegation` becomes a special case of this model. The existing spec entry that says "future richer model replaces the boolean" is now true: the boolean behavior when `true` remains valid semantics in the richer model (it's equivalent to a broad wildcard on both sides), and the structured form is the new default for teams that want more control. Post-v1 implementation work is replacing the boolean with the structured form in the Environment resource, adding gateway enforcement logic for bilateral checks, and exposing the new schema via the admin API.

---

## Human (Apr 2, 2026, 9:55 PM):

Environment-level billing rollup should be included in v1.

Advanced membership analytics should also be included in v1.

---

## Claude:

Two changes to the compiled spec:

**Section 4.6** now reads:

---

### 4.6 Environment Resource — Post-V1 Implementation

**Full environment management UI**

Deferred. The admin API ships in v1. The web UI for browsing environments, editing membership, and previewing selector matches is thin-client work over the admin API. Can be built at any point without spec changes. The `?dryRun=true` parameter on `PUT /v1/admin/environments` must be implemented in v1 alongside the API.

---

Everything else in section 4.6 has been promoted to v1. The section now contains only the UI deferral.

---

**Section 3.1** gains the cross-environment delegation section. The `crossEnvironmentDelegation` field on the environment resource is promoted from a boolean to the full structured form:

```yaml
crossEnvironmentDelegation:
  outbound:
    - targetEnvironment: platform-services
      runtimes:
        matchLabels:
          shared: "true"
    - targetEnvironment: analytics-team
      runtimes:
        ids: [analytics-agent]

  inbound:
    - sourceEnvironment: "*"
      runtimes:
        matchLabels:
          shared: "true"
    - sourceEnvironment: research-team
      runtimes:
        matchLabels:
          team: platform
```

The boolean shorthand (`false` | `true`) remains valid as a convenience form. `false` is equivalent to no declarations on either side. `true` is the escape hatch — bypass environment scoping entirely and fall through to DelegationPolicy only. Boolean and structured form are mutually exclusive on any given environment.

**Effective delegation scope:**

```
(environment definition ∪ cross-environment permitted runtimes) ∩ delegation policy
```

**Gateway enforcement at delegation time** — four sequential checks:

1. Resolve target runtime to its home environment
2. Verify calling environment has an outbound declaration permitting the target environment with a selector matching the target runtime → `target_not_in_scope` if absent
3. Verify target environment has an inbound declaration permitting the calling environment with a selector matching the target runtime → `target_not_in_scope` if absent
4. Apply DelegationPolicy as normal → `target_not_authorized` if policy doesn't permit it

Both sides must independently declare permission. Neither side can unilaterally grant the other access.

**Connectors are never cross-environment.** A child session running in environment B uses environment B's connector configuration regardless of which environment the parent session belongs to.

**Build sequence impact:** Cross-environment delegation enforcement is Phase 15 alongside the rest of the environment resource. The `crossEnvironmentDelegation` field schema (structured form) is Phase 1 core types so no migration is required when Phase 15 ships.

---

## Human (Apr 2, 2026, 9:56 PM):

Thanks. What would be your proposed solution for Cross-environment delegation controls?

---

## Claude:

# Lenny Technical Spec — Final Revised Changes from Design Review (v3)

## How to Read This Document

- **V1 Must-Have (Breaking)** — must be in v1; impossible to fix after the first community runtime ships
- **V1 Must-Have (Feature)** — must ship with v1 to meet stated goals
- **V1 Should-Have** — significant value; should ship but won't break compatibility if deferred
- **Planned / Post-V1** — document intent now so data models accommodate it; implement later
- **Explicit Non-Decisions** — things Lenny deliberately will not do

---

## Part 1: V1 Must-Have — Breaking Changes

---

### 1.1 Replace Custom CRDs With `kubernetes-sigs/agent-sandbox`

**What changes:** Replace `AgentPool`, `AgentPod`, and `AgentSession` CRDs and the WarmPoolController with the upstream `SandboxTemplate`, `SandboxWarmPool`, `SandboxClaim`, and `Sandbox` CRDs from `kubernetes-sigs/agent-sandbox`.

**Mapping:**

| Lenny CRD | Agent-Sandbox CRD |
|---|---|
| `AgentPool` | `SandboxTemplate` |
| `AgentPod` | `Sandbox` + `SandboxWarmPool` |
| `AgentSession` | `SandboxClaim` |
| WarmPoolController | Upstream SIG Apps controller |

`executionMode` (`session`, `task`, `concurrent`) is declared on `SandboxTemplate` from v1 even if `task` and `concurrent` land slightly later in the build sequence.

**Pre-commit requirement:** Verify `SandboxClaim` optimistic-locking guarantee matches Lenny's current status PATCH approach before committing.

**Affects:** Sections 4.6, 6.1, 6.2, 6.3, Phase 1 and Phase 3.

---

### 1.2 Runtime Unification: `Runtime` Replaces `RuntimeType` and `SessionTemplate`

**What changes:** `RuntimeType` and `SessionTemplate` are unified into a single `Runtime` concept with an optional `baseRuntime` field distinguishing standalone from derived runtimes.

```yaml
# Standalone runtime
name: langgraph-runtime
image: registry.example.com/langgraph:latest
type: agent
capabilities:
  interaction: one_shot     # one_shot | multi_turn
  injection:
    supported: true
    modes: [immediate, queued]
executionMode: task
isolationProfile: sandboxed
allowedResourceClasses: [small, medium, large]
delegationPolicyRef: orchestrator-policy
labels:
  team: platform
  approved: "true"

# Derived runtime
name: research-pipeline
baseRuntime: langgraph-runtime
workspaceDefaults:
  files:
    - path: agent.py
      content: "..."
  setupCommands:
    - pip install -r requirements.txt
setupPolicy:
  timeoutSeconds: 300
  onTimeout: fail
agentInterface: ...
delegationPolicyRef: research-policy
publishedMetadata: ...
labels:
  team: research
  approved: "true"
```

**Labels are required from v1** — primary mechanism for environment `runtimeSelector` and `connectorSelector` matching.

**Inheritance rules — never overridable on derived runtime:** `type`, `executionMode`, `isolationProfile`, `capabilities.interaction`, `allowedResourceClasses`, `allowStandardIsolation` acknowledgment.

**Independently configurable on derived runtime:** Pool settings, `workspaceDefaults`, `setupCommands`, `setupPolicy.timeoutSeconds` (gateway takes maximum of base and derived), `agentInterface`, `delegationPolicyRef` (restrict only), `publishedMetadata`, `labels`, `taskPolicy`.

**Base runtime mutability:** `image` and `name` immutable via API. All other fields mutable with impact validation — changes that would invalidate existing derived runtimes are rejected with a list of affected runtimes.

**Affects:** Section 5.1, Section 15.1 and 15.2, Phase 1.

---

### 1.3 `type` and `capabilities` Fields on Runtime

**What changes:** Every `Runtime` has a `type` field and an optional `capabilities` field.

```yaml
type: agent          # agent (default) | mcp
capabilities:
  interaction: one_shot    # one_shot | multi_turn
  injection:
    supported: true        # default: false
    modes: [immediate, queued]
```

**`type: agent`** — participates in Lenny's task lifecycle. Receives tasks via stdin `{type: "message"}`, has sessions, workspace, delegation, elicitation, multi-turn dialog. Callable via `lenny/delegate_task`.

**`type: mcp`** — hosts an MCP server. Lenny manages pod lifecycle (isolation, credentials, workspace, pool, egress, audit). No task lifecycle. Runtime binary is oblivious to Lenny.

**`capabilities.interaction: multi_turn`** is valid in v1. A runtime declaring `multi_turn` supports the `lenny/request_input` → response cycle and multiple `{type: "message"}` deliveries over the lifetime of a task. Multi-turn requires `capabilities.injection.supported: true` — the gateway enforces this at registration time.

**`capabilities.injection`** declares whether the runtime supports mid-session message delivery. Default: `supported: false`. Gateway rejects injection attempts against unsupported sessions at the API level before they reach the adapter.

**`type: mcp` runtimes have no `capabilities` field.**

**Affects:** Section 5.1, Phase 1.

---

### 1.4 Adapter Protocol: Multi-Server MCP + Lifecycle Channel

**What changes:** The stdin/stdout JSON Lines binary protocol (Section 15.4.1) is replaced by a two-part model.

**Part A — Multiple focused local MCP servers** (intra-pod, stdio or Unix socket):

- **Platform MCP server** — Lenny-specific tools: `lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`, `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`, `lenny/request_input`, `lenny/send_message`, `lenny/get_task_tree`
- **One MCP server per authorized connector** — each connector in the session's delegation policy gets its own independent MCP server. No aggregated connector proxy — aggregation is not lossless per MCP spec.

**No workspace MCP server.** Workspace is materialized to `/workspace/current` before the runtime starts.

**Part B — Lifecycle channel** — separate stdin/stdout stream pair for operational signals:

```
Adapter → Runtime:  lifecycle_capabilities, checkpoint_request,
                    interrupt_request, credentials_rotated, terminate
Runtime → Adapter:  lifecycle_support, checkpoint_ready,
                    interrupt_acknowledged, credentials_acknowledged
```

Optional. Runtimes that don't open it operate in fallback-only mode.

**Runtime integration tiers (agent-type only):**
- **Minimum** — stdin/stdout binary protocol only. Reads `{type: "message"}` from stdin, writes `{type: "response"}` and `{type: "tool_call"}` to stdout. This is the floor.
- **Standard** — minimum plus connects to adapter's platform MCP server and connector servers via the adapter manifest.
- **Full** — standard plus opens the lifecycle channel.

**Adapter manifest:** Written to `/run/lenny/adapter-manifest.json` **before the runtime binary is spawned** — complete and authoritative when the binary starts. Regenerated per task execution.

```json
{
  "platformMcpServer": { "socket": "/run/lenny/platform-mcp.sock" },
  "connectorServers": [
    { "id": "github", "socket": "/run/lenny/connector-github.sock" }
  ],
  "runtimeMcpServers": [],
  "agentInterface": { ... },
  "sessionId": "sess_abc",
  "taskId": "task_root"
}
```

`runtimeMcpServers` slot reserved from v1 for future use by `type:mcp` runtimes accessible via adapter proxy.

**Startup sequence for `type: agent` runtimes:**

1. Pod created by Kubernetes
2. Adapter opens gRPC connection to gateway (mTLS)
3. Adapter writes placeholder manifest (connector servers unknown yet)
4. Adapter signals READY to gateway — pod enters warm pool
5. Gateway assigns session: `PrepareWorkspace` → `FinalizeWorkspace` → `RunSetup` → `AssignCredentials` → `StartSession`
6. Adapter writes **final manifest** (connector servers now known from lease)
7. Adapter spawns runtime binary
8. Runtime reads manifest, connects to MCP servers (Standard/Full), opens lifecycle channel (Full)
9. Adapter sends `lifecycle_capabilities` (Full); receives `lifecycle_support`
10. Adapter delivers first `{type: "message"}` on stdin

**Affects:** Section 15.4 and 15.4.1 (full replacement), Section 4.7, Phase 2.

---

### 1.5 Binary Protocol: Unified `message` Type — `prompt` Removed

**What changes:** The `prompt` inbound message type is removed entirely. The unified `message` type handles all inbound content delivery — the initial task and all subsequent messages. No `sessionState` field — the runtime knows it's receiving its first message by virtue of just having started.

**Inbound messages (adapter → runtime via stdin):**

| `type` | Description |
|---|---|
| `message` | All content delivery: initial task, mid-session injection, reply to `request_input`, sibling notification. Carries optional `slotId` for concurrent-workspace mode. |
| `heartbeat` | Periodic liveness ping |
| `shutdown` | Graceful shutdown with no new task |

**Outbound messages (runtime → adapter via stdout):**

| `type` | Description |
|---|---|
| `response` | Streamed or complete response carrying `OutputPart[]`. Carries `slotId` in concurrent-workspace mode. |
| `tool_call` | Runtime requests a tool call. Carries `slotId` in concurrent-workspace mode. |
| `status` | Optional status/trace update |

**`follow_up` renamed to `message` everywhere.** No `follow_up` type anywhere in the protocol.

**`input_required` outbound message type removed.** Replaced by `lenny/request_input` blocking MCP tool call on the platform MCP server.

**`slotId` for concurrent-workspace multiplexing:** Session mode and task mode messages never carry `slotId` and runtimes for those modes never see it. Concurrent-workspace runtimes implement a dispatch loop keyed on `slotId` — each concurrent slot's messages carry a distinct `slotId` assigned by the adapter. This is the mechanism that allows multiple independent concurrent task streams through a single stdin channel, eliminating the need for any separate delivery mechanism for concurrent-workspace mode.

**Task mode between-task signaling:** Adapter sends `{type: "terminate", reason: "task_complete"}` on the lifecycle channel after a task completes. The next `{type: "message"}` after scrub is the start of the new task.

**Affects:** Section 15.4.1 (binary protocol message types).

---

### 1.6 Internal `OutputPart` Format

**What changes:** `agent_text` streaming event replaced by `agent_output` carrying `OutputPart` array. `TaskResult` and `TaskSpec` use `OutputPart` arrays.

```json
{
  "id": "part_abc123",
  "type": "text",
  "mimeType": "text/plain",
  "inline": "content here",
  "ref": "artifact://...",
  "annotations": { "role": "primary", "final": true },
  "parts": [],
  "status": "streaming | complete | failed"
}
```

- `type` is an open string — not a closed enum
- `mimeType` handles encoding separately from semantic type
- `annotations` is an open metadata map
- `parts` allows nesting
- `id` enables part-level streaming updates

SDK helper `from_mcp_content(blocks)` converts MCP content blocks to `OutputPart` arrays.

**Affects:** Section 15.4.1, Section 8.9, Section 7.2.

---

### 1.7 `MessageEnvelope` — Unified Message Format

**What changes:** All inbound content messages use a unified `MessageEnvelope` across the stdin binary protocol, platform MCP server tools, and all external APIs.

```json
{
  "id": "msg_xyz789",
  "from": {
    "kind": "client | agent | system | external",
    "id": "..."
  },
  "inReplyTo": "req_abc123",
  "threadId": "thread_001",
  "delivery": "immediate",
  "input": [OutputPart[]]
}
```

**Adapter-injected fields — runtime never supplies these:**
- `from.kind` and `from.id` — injected by the adapter from execution context
- `requestId` in `lenny/request_input` — generated by the gateway; runtime only supplies `parts`

**`delivery`** — optional mechanical hint. `immediate` means deliver at next stdin read (after interrupt acknowledgment if needed). Absent means queue for next natural pause.

**`id`** — every message has a stable ID enabling threading, reply tracking, and non-linear context retrieval.

**`inReplyTo`** — optional. If it matches an outstanding `lenny/request_input` call on the target, the gateway resolves that tool call directly instead of delivering to stdin.

**`threadId`** — optional. In v1 one implicit thread per session. Multi-thread sessions are additive post-v1.

**Affects:** Section 7.2, Section 15.4.1, Section 9.

---

### 1.8 TaskRecord Uses Messages Array + `input_required` State

**What changes:** Task record schema uses a messages array forward-compatible with multi-turn dialog:

```json
{
  "messages": [
    { "role": "caller", "parts": [...] },
    { "role": "agent",  "parts": [...], "state": "completed" }
  ]
}
```

**Task state machine — `input_required` is reachable in v1:**
```
submitted → running → completed
                    → failed
                    → input_required   (reachable via lenny/request_input)
```

`lenny/await_children` (and `lenny/await_child`) unblock when a child enters `input_required`, returning a partial result with the child's question and `requestId`. The gRPC `AwaitChildren` call is a streaming response — it yields partial events before the final settled result.

**Affects:** Section 8.9.

---

### 1.9 Session State Machine Gains `suspended`

**What changes:** `interrupt_request` on the lifecycle channel produces a distinct `suspended` session state:

```
running → suspended   (interrupt_request + interrupt_acknowledged)
suspended → running   (resume_session — no new content)
suspended → running   (POST /v1/sessions/{id}/messages delivery:immediate)
suspended → completed (terminate)
```

Pod held, workspace preserved, `maxSessionAge` timer paused while suspended. `interrupt_request` is a standalone lifecycle signal — pause-and-decide with decoupled timing. Distinct from `delivery: "immediate"` in a message, which atomically interrupts and delivers content.

**`interrupt_request` does NOT cascade** to children. Budget/lease expiry does cascade. Runtime decides whether to propagate a received interrupt to its children.

**Affects:** Section 7.2, Section 6.1.

---

### 1.10 Single `lenny/delegate_task` Tool

**What changes:** One delegation tool. Target id is opaque — runtime does not know whether target is a standalone runtime, derived runtime, or external registered agent.

```
lenny/delegate_task(
  target: string,
  task: TaskSpec,
  lease_slice?: LeaseSlice
) → TaskHandle
```

`TaskSpec`:
```json
{
  "input": [OutputPart[]],
  "workspaceFiles": {
    "export": [{ "glob": "src/auth/**", "destPrefix": "/" }]
  }
}
```

**`lenny/delegate_task` rejects `type: mcp` targets** with `target_not_an_agent`.

**Affects:** Section 8.2, Section 8.9, Section 9.1.

---

### 1.11 Gateway-Mediated Session Messaging

**What changes:** All inter-session communication is gateway-mediated. Three platform MCP tools:

```
lenny/send_message(
  to: string,             // taskId
  message: MessageEnvelope
) → void

lenny/request_input(
  parts: OutputPart[]     // question content only
) → MessageEnvelope       // blocks until answer arrives

lenny/get_task_tree() → TaskTreeNode
```

`lenny/get_task_tree` returns:
```json
{
  "self": { "taskId": "task_001", "sessionId": "sess_abc" },
  "parent": { "taskId": "task_root", "sessionId": "sess_parent" },
  "children": [...],
  "siblings": [
    { "taskId": "task_002", "sessionId": "sess_xyz",
      "state": "running", "runtime": "analyst-agent" }
  ]
}
```

**`taskId` is the messaging address** — stable across pod recovery generations.

**Message delivery routing — three paths:**
1. **`inReplyTo` matches outstanding `lenny/request_input`** → gateway resolves blocked tool call directly. No stdin delivery, no interrupt.
2. **No matching pending request, runtime available** → `{type: "message"}` to stdin at next read.
3. **No matching pending request, runtime blocked in `await_children`** → buffered in inbox; delivered before the next `await_children` event.

**`lenny/send_to_child` is active in v1** — it is not a stub. Delivers a `{type: "message"}` to a child session via the same gateway-mediated path. This is the primary mechanism a parent agent uses to continue a multi-turn conversation with a child that has not entered `input_required`.

**`lenny/request_input` replaces the stdout `input_required` message type.**

**Affects:** Section 8.2, Section 9, Section 7.2.

---

### 1.12 `await_children` Unblocks on `input_required`

**What changes:** `lenny/await_children` unblocks when a child enters `input_required` state. The partial result carries the child's question and `requestId`. The gRPC `AwaitChildren` call is a streaming response.

When the parent calls `lenny/send_message` with `inReplyTo: "req_001"`, the gateway resolves the child's blocked `lenny/request_input` tool call directly. The parent then re-awaits.

**Affects:** Section 8.9, Section 9.

---

### 1.13 `DelegationPolicy` as First-Class Resource

**What changes:** `allowedRuntimes`, `allowedConnectors`, and `allowedPools` replaced by named `DelegationPolicy` resources with tag-based matching evaluated at delegation time.

**Effective policy = `base_policy ∩ derived_policy`** — derived runtime policy can only restrict.

**`lenny/discover_agents`** returns `type: agent` runtimes and external agents only. Results are policy-scoped. `type: mcp` runtimes do not appear.

**Affects:** Section 8.3, Section 5.1.

---

### 1.14 `executionMode` on Pool Configuration — All Three in V1

```yaml
executionMode: session | task | concurrent
```

All three modes implemented in v1. Graph mode removed as a separate concept — graph-aware runtimes are session-mode runtimes that optionally emit trace spans.

**Affects:** Section 5.2, Phase 1.

---

### 1.15 `agentInterface` Field on Runtime

**What changes:** `type: agent` runtimes gain an optional `agentInterface` field serving discovery, A2A card auto-generation, and adapter manifest summaries.

```yaml
agentInterface:
  description: "Analyzes codebases and produces refactoring plans"
  inputModes:
    - type: "text/plain"
    - type: "application/json"
  outputModes:
    - type: "text/plain"
      role: "primary"
  supportsWorkspaceFiles: true
  skills:
    - id: "review"
      name: "Code Review"
      description: "Reviews code for quality and correctness"
  examples:
    - description: "Review auth module"
      input: "Review the authentication module"
```

`type: mcp` runtimes do not have `agentInterface`.

**Affects:** Section 5.1.

---

### 1.16 `publishedMetadata` on Runtime

```yaml
publishedMetadata:
  - key: agent-card
    contentType: application/json
    visibility: public    # internal | tenant | public
    value: '...'
```

Not-found and not-authorized produce identical responses — no enumeration.

**Affects:** Section 5.1.

---

### 1.17 `ExternalAdapterRegistry` — Replaces Single Adapter

**What changes:** Gateway uses `ExternalAdapterRegistry` with simultaneously active adapters routing by path prefix. `HandleDiscovery` required on all adapters.

**Three tiers of pluggability:** Built-in (compiled in), config-driven (Go plugin or gRPC at startup), runtime registration via admin API.

**Built-in adapter inventory:**

| Adapter | Path prefix | Protocol |
|---|---|---|
| `MCPAdapter` | `/mcp` | MCP Streamable HTTP |
| `OpenAICompletionsAdapter` | `/v1/chat/completions` | OpenAI Chat Completions |
| `OpenResponsesAdapter` | `/v1/responses` | Open Responses Specification |
| `A2AAdapter` | `/a2a/{runtime}` | A2A (post-v1) |
| `AgentProtocolAdapter` | `/ap/v1/agent` | AP (post-v1) |

`OpenResponsesAdapter` covers both Open Responses-compliant clients and OpenAI Responses API clients. OpenAI's Responses API is a proper superset of Open Responses; the difference is OpenAI's proprietary hosted tools, which Lenny doesn't implement.

**Affects:** Section 4.1, Section 15, Phase 1.

---

### 1.18 `type: mcp` Runtime Dedicated MCP Endpoints

Each enabled `type: mcp` runtime gets a dedicated MCP endpoint at `/mcp/runtimes/{runtime-name}`. Not aggregated. An implicit session record is created per connection for audit and billing.

**Discovery:** `GET /v1/runtimes` and `list_runtimes` return `mcpEndpoint` and `mcpCapabilities.tools` preview.

**Affects:** Section 4.1, Section 9, Section 15.

---

### 1.19 Comprehensive Admin API

**Operational plane — API-managed:** Runtimes, Delegation Policies, Connectors, Pools, Credential Pools, Tenants, Quotas, User Role Assignments, Egress Profiles, Experiments, Scaling Policies, Memory Store Config, Webhooks, External Adapters, Environments, Tenant RBAC Config.

**Bootstrap plane — Helm only:** DB URLs, Redis, MinIO, KMS, cluster name, namespace assignments, certificate paths, `LENNY_DEV_MODE`, system-wide defaults, Kubernetes object definitions.

CRDs become derived state reconciled from Postgres by PoolScalingController.

**Affects:** Section 15.1, Section 10.2, Phase between 4 and 5.

---

## Part 2: V1 Must-Have — Feature Changes

---

### 2.1 Multi-Turn Agent Dialog

**What changes:** `interaction: multi_turn` is valid in v1. This is not a stub — it is a fully implemented capability for runtimes that declare it.

**What `multi_turn` means precisely:** A runtime that declares `multi_turn` may call `lenny/request_input` multiple times during a single task, receive multiple `{type: "message"}` deliveries, and produce partial outputs before reaching a final response. The task does not end after the first `response` message on stdout.

**Constraints:**
- `interaction: multi_turn` requires `capabilities.injection.supported: true` — the gateway enforces this at runtime registration. A multi-turn runtime that doesn't accept injections is incoherent.
- `interaction: one_shot` runtimes that call `lenny/request_input` more than once get a gateway error on the second call.
- Multi-turn is independent of external protocol. A2A natively supports multi-turn via its `input-required` state — this maps directly to Lenny's `input_required` task state. A2A is still post-v1; multi-turn within Lenny's internal task model is v1.

**`lenny/send_to_child` is active.** It delivers a `{type: "message"}` to a child session. Combined with `await_children` unblocking on `input_required`, this enables full synchronous parent-child multi-turn conversations within Lenny.

**Affects:** Section 5.1, Section 9, Phase 1 (capabilities schema), Phase 9 (tool activation).

---

### 2.2 Task Pod Execution Mode

```yaml
taskPolicy:
  cleanupCommands:
    - pkill -f jupyter_kernel
    - rm -rf /tmp/sandbox-*
  cleanupTimeoutSeconds: 30
  onCleanupFailure: warn    # warn | fail
```

Lifecycle: terminate(task_complete) on lifecycle channel → cleanup commands → Lenny scrub → pod available. `setupCommands` run once per pod at start, not per task. Deployer acknowledgment required.

**Affects:** Sections 6.1, 6.2, 7, 5.2.

---

### 2.3 Concurrent Execution Mode

Two sub-variants via `concurrencyStyle`:

```yaml
executionMode: concurrent
concurrencyStyle: stateless    # stateless | workspace
maxConcurrent: 8
```

**`concurrencyStyle: workspace`** — each slot gets its own workspace. Gateway tracks per-slot lifecycle. **Task delivery via `slotId` multiplexing over stdin** — the adapter assigns a `slotId` per slot; the runtime implements a dispatch loop keyed on `slotId`; all binary protocol messages (inbound and outbound) carry `slotId` in this mode. This closes the concurrent-workspace delivery gap without any separate delivery mechanism. Cross-slot isolation is process-level and filesystem-level — explicitly weaker than session mode. Deployer acknowledgment required.

**`concurrencyStyle: stateless`** — no workspace materialization. Gateway routes through Kubernetes Service. Pod readiness probe reflects slot availability. PoolScalingController watches `active_slots / (pod_count × maxConcurrent)`.

**Truly stateless runtimes** with no workspace and no expensive shared state should be registered as external connectors, not Lenny-managed pods.

**Affects:** Sections 5.2, 6.1, 6.2, 4.1.

---

### 2.4 `setupPolicy` on Runtime

```yaml
setupPolicy:
  timeoutSeconds: 300      # optional — waits indefinitely if absent
  onTimeout: fail          # fail | warn
```

Gateway takes the **maximum** of base and derived `timeoutSeconds` if both set.

**Affects:** Section 5.1, Section 6.2.

---

### 2.5 `ConnectorDefinition` as First-Class Resource

Connector configuration is a first-class admin API resource. Includes `labels` map for environment selector matching. Tool capability metadata derived from MCP `ToolAnnotations` at registration time.

**Affects:** Section 4.3, Section 9.3.

---

### 2.6 Runtime Discovery on All External Interfaces

Every external interface exposes runtime discovery via `HandleDiscovery`. All results are identity-filtered and policy-scoped. Not-found and not-authorized produce identical responses.

- **MCP:** `list_runtimes` tool
- **REST:** `GET /v1/runtimes` with full `agentInterface` and `mcpEndpoint` fields
- **OpenAI Completions:** `GET /v1/models`
- **Open Responses:** `GET /v1/models`

**Affects:** Section 9, Section 15.1, Section 15.2.

---

### 2.7 OpenAI Completions Adapter

Implements `POST /v1/chat/completions` and `GET /v1/models`. Stateless, ephemeral sessions. Tool handling — opaque mode only.

**Affects:** Section 15.

---

### 2.8 Open Responses Adapter

Implements the Open Responses Specification. Covers both Open Responses-compliant and OpenAI Responses API clients for all core features. Lenny connectors → Open Responses **internal tools**. Client-defined functions (external tools) not supported in v1.

**Affects:** Section 15.

---

### 2.9 Capability Inference from MCP `ToolAnnotations`

Gateway reads `tools/list` at connector or `type:mcp` runtime registration and infers capabilities from MCP `ToolAnnotations`. No manual re-annotation required.

| MCP annotation | Inferred capabilities |
|---|---|
| `readOnlyHint: true` | `read` |
| `readOnlyHint: false, destructiveHint: false` | `write` |
| `destructiveHint: true` | `write, delete` |
| `openWorldHint: true` | `network` |
| No annotations | `admin` (conservative default) |
| *(no MCP equivalent)* | `execute`, `admin` — set via `toolCapabilityOverrides` |

Tenant-overridable via `tenantRbacConfig.mcpAnnotationMapping`.

**Affects:** Section 4.3, Section 5.1.

---

### 2.10 PoolScalingController

Separate `PoolScalingController` from `WarmPoolController`. Pluggable `PoolScalingStrategy` interface.

Default formula: `target_minWarm = ceil(base_demand_p95 × variant_weight × safety_factor)`. `safety_factor` defaults to 1.5 for agent-type, 2.0 for mcp-type. `scaleToZero` disabled by default for `type: mcp` pools.

**Affects:** Section 4.6, Section 5.2.

---

### 2.11 Experiment Primitives

`ExperimentDefinition` as first-class admin API resource. `ExperimentRouter` as built-in `RequestInterceptor`. Experiment context propagates through delegation leases. Results API aggregates eval scores by variant. PoolScalingController manages variant pool lifecycle automatically.

**Affects:** Section 4.8, Section 15.1.

---

### 2.12 Injection Capability Declaration and External Session Messaging

`POST /v1/sessions/{id}/messages` is the unified client-facing endpoint for all message types. MCP equivalent: `send_message` tool. Gateway rejects injection attempts against sessions whose runtime has `injection.supported: false`.

Interrupt remains separate: `POST /v1/sessions/{id}/interrupt` — lifecycle signal, not content delivery.

**Affects:** Section 7.2, Section 15.1.

---

## Part 3: V1 Should-Have

---

### 3.1 Environment Resource and RBAC Model

**What changes:** `Environment` as first-class admin API resource. Named, RBAC-governed project context grouping runtimes and connectors for a team.

**Two access paths:**
- **Transparent filtering** (default) — user connects to standard endpoint; gateway computes the union of authorized runtimes across all environments where the user's groups have a role and returns that filtered view.
- **Explicit environment endpoint** (opt-in) — dedicated paths across all external interfaces: `/mcp/environments/{name}`, `/v1/environments/{name}/sessions`, scoped model namespace on `/v1/responses` and `/v1/chat/completions`.

**Runtime and connector selection is tag-based** using Kubernetes-style label expression syntax with `include`/`exclude` overrides.

**`mcpRuntimeFilters`** — capability-based tool filtering for `type: mcp` runtimes. Name collisions resolved by `runtime:tool` qualified reference in `overrides`.

**`connectorSelector`** — internal only. Controls what connectors agents can use via the platform MCP server.

**Cross-environment delegation** — structured bilateral declaration model:

```yaml
crossEnvironmentDelegation:
  outbound:
    - targetEnvironment: platform-services
      runtimes:
        matchLabels:
          shared: "true"
    - targetEnvironment: analytics-team
      runtimes:
        ids: [analytics-agent]
  inbound:
    - sourceEnvironment: "*"
      runtimes:
        matchLabels:
          shared: "true"
```

Effective cross-environment access requires both sides to permit it. Neither side can unilaterally grant the other access. The boolean shorthand (`false` | `true`) remains valid for simple cases. `true` bypasses environment scoping and falls through to DelegationPolicy only. Boolean and structured form are mutually exclusive on any given environment.

**Effective delegation scope:** `(environment definition ∪ cross-environment permitted runtimes) ∩ delegation policy`

**Gateway enforcement at delegation time:**
1. Resolve target runtime to its home environment
2. Verify calling environment has an outbound declaration permitting target → `target_not_in_scope` if absent
3. Verify target environment has an inbound declaration permitting calling environment → `target_not_in_scope` if absent
4. Apply DelegationPolicy as normal → `target_not_authorized` if policy doesn't permit

**Connectors are never cross-environment.** Child sessions use their own environment's connector configuration.

**Environment-level billing rollup (v1):** `environmentId` populated on all billing events for sessions created in an environment context. New endpoint: `GET /v1/admin/environments/{name}/usage`.

**Advanced membership analytics (v1):** Three read-only endpoints:
- `GET /v1/admin/tenants/{id}/access-report` — cross-environment access matrix
- `GET /v1/admin/environments/{name}/access-report` — resolved member list with group expansion
- `GET /v1/admin/environments/{name}/runtime-exposure` — runtimes/connectors in scope with capability filters

**`noEnvironmentPolicy`:** `deny-all` (platform default) or `allow-all`. Configurable per tenant, with platform-wide default at Helm time.

**Identity:** OIDC. Groups from LDAP/AD carried as JWT claims. `introspectionEnabled: true` adds real-time group checks at latency cost.

**Full configuration template:** See the RBAC design section for the complete config schema covering `tenantRbacConfig` and `environment` resource definitions.

**Member roles:** `viewer`, `creator`, `operator`, `admin`.

**V1 data model accommodations:**
- `Runtime` resources need `labels` map from v1
- `Connector` registrations need `labels` map from v1
- `type:mcp` runtime tool schemas cached by gateway from v1
- `GET /v1/runtimes` and `list_runtimes` accept optional `?environmentId=` stub
- Session creation accepts optional `environmentId` parameter as no-op stub
- `environmentId` as nullable field on billing event schema from Phase 1
- `crossEnvironmentDelegation` structured form schema from Phase 1

**Affects:** Section 5.1 (labels), Section 4.1 (identity-aware routing), Section 15.1, New Section 10 (RBAC).

---

### 3.2 `MemoryStore` Interface

`MemoryStore` role-based storage interface. Default: Postgres + pgvector. Fully replaceable. Technology choice deferred. Accessed via `lenny/memory_write` and `lenny/memory_query` on platform MCP server.

**Affects:** Section 4, Section 9.

---

### 3.3 Semantic Caching at the LLM Proxy

Optional `CachePolicy` on `CredentialPool`. Pluggable `SemanticCache` interface — default Redis, fully replaceable. Disabled by default.

**Affects:** Section 4.9.

---

### 3.4 `RequestInterceptor` Extension Point

Interceptor interface at phases: `PreAuth`, `PostAuth`, `PreRoute`, `PostRoute`, `PreToolResult`, `PostAgentOutput`. Built-in interceptors: `ExperimentRouter`, `GuardrailsInterceptor` (disabled, no built-in logic).

**Affects:** Section 4.8.

---

### 3.5 Agent Evaluation Hooks

`POST /v1/sessions/{id}/eval` accepts scored results. `POST /v1/sessions/{id}/replay` re-runs against different runtime version. No built-in eval logic.

**Affects:** Section 15.1, Section 16.

---

### 3.6 `CredentialRouter` Interface

Pluggable credential pool selection logic. Default: least-loaded/round-robin/sticky-until-failure. Disabled by default, opt-in per pool.

**Affects:** Section 4.9.

---

## Part 4: Planned / Post-V1

---

### 4.1 A2A Full Support

**What to document now:**
- `ExternalProtocolAdapter` is the mechanism. A2A is implementation two.
- Inbound: gateway serves `POST /a2a/{runtimeName}/tasks` via `A2AAdapter`
- Outbound: external A2A agents registered as connectors, callable via `lenny/delegate_task`
- A2A `input-required` state maps directly to Lenny's `input_required` task state — multi-turn over A2A is the primary external multi-turn use case
- A2A streaming output via SSE requires the adapter to bridge SSE events to `OutputPart` events through the parent's `await_children` streaming response — this is the primary implementation complexity for A2A multi-turn
- `agentInterface` auto-generates A2A agent cards
- Per-agent A2A endpoints (not aggregated) at `/a2a/runtimes/{name}`
- `allowedExternalEndpoints` slot on delegation lease schema must exist from v1
- Runtime authors who want intra-pod A2A client behavior must opt in explicitly

---

### 4.2 A2A Intra-Pod Support

**What to document now:** Adapter additionally serves per-agent A2A endpoints intra-pod. Adapter manifest gains A2A base URL. All A2A traffic proxied through gateway. Runtime authors opt in explicitly.

---

### 4.3 Agent Protocol (AP) Support

**What to document now:** AP defines `POST /ap/v1/agent/tasks` and step execution. Third `ExternalProtocolAdapter` implementation. No changes to intra-pod model.

---

### 4.4 Future Conversational Patterns

**What to document now:** `MessageEnvelope` with `id`, `from`, `inReplyTo`, `threadId` accommodates all of these without schema changes: threaded messages, multiple participants, non-linear context retrieval, broadcast, external agent participation.

---

### 4.5 Environment Management UI

**What to document now:** Full web UI for browsing environments, editing membership, and previewing selector matches is thin-client work over the admin API. The `?dryRun=true` parameter on `PUT /v1/admin/environments` is the preview mechanism and ships in v1.

---

### 4.6 Environment Resource — Post-V1 Deferred Items

**Full environment management UI:** Deferred. Admin API ships in v1. UI is thin-client work, can be built at any point without spec changes.

**Cross-environment delegation richer controls:** The structured bilateral declaration model (outbound/inbound) is v1. What is deferred is runtime-level cross-environment exceptions at granularity beyond the structured form, and integration with experiment-scoped delegation boundaries. These require a design pass once the base feature is in production.

---

### 4.7 Multi-Cluster Federation

**What to document now:** Session IDs must be globally unique. Storage interfaces must not assume single-cluster topology. Connectors registered in one cluster must be expressible by reference in multi-cluster scenarios.

---

### 4.8 UI and CLI

**What to document now:** Admin API is the complete operational surface. Official CLI (`lenny-ctl`) and web portal are separate projects consuming the admin API as thin clients with zero business logic.

---

## Part 5: Explicit Non-Decisions

**5.1 No Model B Runtime Deployment.** No mechanism for packaging a graph definition as a new registered runtime. Users register derived runtimes via admin API.

**5.2 No Built-In Eval Logic.** Lenny provides hooks and storage. No LLM-as-judge or hallucination detection.

**5.3 No Built-In Guardrail Logic.** Lenny provides the `RequestInterceptor` hook. No content classifiers or prompt injection detection.

**5.4 No Built-In Memory Extraction.** Lenny provides the `MemoryStore` interface and tools. Runtimes decide what to write.

**5.5 No Direct External Connector Access.** Connectors are session-internal. External clients do not call connectors directly. The data model accommodates this later without redesign.

**5.6 Hooks-and-Defaults Design Principle.** Every cross-cutting AI capability follows the same pattern: defined as an interface with a sensible default, disabled unless explicitly enabled, fully replaceable. Lenny never implements AI-specific logic.

---

## Part 6: Revised Build Sequence

**Phase 1** — Core types: `Runtime` (unified, with `labels`), `type` field, `capabilities` (including `interaction: one_shot | multi_turn`, `injection`), `executionMode`, `allowedExternalEndpoints` on delegation lease, `input_required` task state, messages array in TaskRecord, `suspended` session state. Agent-sandbox CRDs. `Connector` resource with `labels`. `environmentId` nullable field on billing event schema. `crossEnvironmentDelegation` structured form schema slot.

**Phase 2** — Replace adapter binary protocol: unified `{type:"message"}` (no separate `prompt`), `slotId` field for concurrent-workspace multiplexing, multi-server MCP adapter + lifecycle channel, adapter manifest written before binary spawns. Publish as runtime adapter specification.

**Phase 3** — PoolScalingController. `DelegationPolicy` resource. `setupPolicy` enforcement. `taskPolicy.cleanupCommands`.

**New Phase between 4 and 5** — Admin API foundation: runtimes, pools, connectors, delegation policies, tenant management, external adapters registry. Gateway loads config from Postgres. Capability inference from MCP annotations at connector registration.

**Phase 5** — Gateway `ExternalAdapterRegistry`. MCP adapter, Completions adapter, Open Responses adapter all active. `list_runtimes`, `GET /v1/runtimes`, `GET /v1/models` with identity-aware filtering. `type: mcp` runtime endpoints at `/mcp/runtimes/{name}`. Tenant RBAC config API. `noEnvironmentPolicy` enforcement.

**Phase 9** — `lenny/delegate_task` handles internal and external targets. `lenny/send_message`, `lenny/request_input`, `lenny/get_task_tree`, `lenny/send_to_child` (active). `lenny/discover_agents` with policy scoping. Multi-turn fully operational.

**Phase 10** — `agentInterface` in discovery. Adapter manifest includes summaries.

**Phase 12** — `type: mcp` runtime support. Concurrent execution modes including `slotId` multiplexing for workspace variant.

**New Phase 15** — Environment resource: tag-based selectors, member RBAC, `mcpRuntimeFilters` with capability model, `connectorSelector`, cross-environment delegation enforcement, billing rollup endpoint, membership analytics endpoints, explicit environment endpoints across all adapters.

**New Phase 16** — Experiment primitives, PoolScalingController experiment integration.

**New Phase 17** — Memory, caching, guardrail, eval hooks.

---

## Summary

| Tier | Count |
|---|---|
| V1 Must-Have Breaking | 19 |
| V1 Must-Have Feature | 12 |
| V1 Should-Have | 6 |
| Planned / Post-V1 | 8 |
| Explicit Non-Decisions | 6 |

---

## Human (Apr 2, 2026, 10:02 PM):

Great, this needs to be added to v1

---

## Claude:

Two changes to the compiled spec:

**Section 4.6** now reads:

---

### 4.6 Environment Resource — Post-V1 Implementation

**Full environment management UI**

Deferred. The admin API ships in v1. The web UI for browsing environments, editing membership, and previewing selector matches is thin-client work over the admin API. Can be built at any point without spec changes. The `?dryRun=true` parameter on `PUT /v1/admin/environments` must be implemented in v1 alongside the API.

---

Everything else in section 4.6 has been promoted to v1. The section now contains only the UI deferral.

---

**Section 3.1** gains the cross-environment delegation section. The `crossEnvironmentDelegation` field on the environment resource is promoted from a boolean to the full structured form:

```yaml
crossEnvironmentDelegation:
  outbound:
    - targetEnvironment: platform-services
      runtimes:
        matchLabels:
          shared: "true"
    - targetEnvironment: analytics-team
      runtimes:
        ids: [analytics-agent]

  inbound:
    - sourceEnvironment: "*"
      runtimes:
        matchLabels:
          shared: "true"
    - sourceEnvironment: research-team
      runtimes:
        matchLabels:
          team: platform
```

The boolean shorthand (`false` | `true`) remains valid as a convenience form. `false` is equivalent to no declarations on either side. `true` is the escape hatch — bypass environment scoping entirely and fall through to DelegationPolicy only. Boolean and structured form are mutually exclusive on any given environment.

**Effective delegation scope:**

```
(environment definition ∪ cross-environment permitted runtimes) ∩ delegation policy
```

**Gateway enforcement at delegation time** — four sequential checks:

1. Resolve target runtime to its home environment
2. Verify calling environment has an outbound declaration permitting the target environment with a selector matching the target runtime → `target_not_in_scope` if absent
3. Verify target environment has an inbound declaration permitting the calling environment with a selector matching the target runtime → `target_not_in_scope` if absent
4. Apply DelegationPolicy as normal → `target_not_authorized` if policy doesn't permit it

Both sides must independently declare permission. Neither side can unilaterally grant the other access.

**Connectors are never cross-environment.** A child session running in environment B uses environment B's connector configuration regardless of which environment the parent session belongs to.

**Build sequence impact:** Cross-environment delegation enforcement is Phase 15 alongside the rest of the environment resource. The `crossEnvironmentDelegation` field schema (structured form) is Phase 1 core types so no migration is required when Phase 15 ships.

---

## Human (Apr 2, 2026, 10:03 PM):

Revise your previous compilation of updates to the tech spec based on all we have talked about.

---

## Claude:

# Lenny Technical Spec — Final Revised Changes from Design Review (v3)

## How to Read This Document

- **V1 Must-Have (Breaking)** — must be in v1; impossible to fix after the first community runtime ships
- **V1 Must-Have (Feature)** — must ship with v1 to meet stated goals
- **V1 Should-Have** — significant value; should ship but won't break compatibility if deferred
- **Planned / Post-V1** — document intent now so data models accommodate it; implement later
- **Explicit Non-Decisions** — things Lenny deliberately will not do

---

## Part 1: V1 Must-Have — Breaking Changes

---

### 1.1 Replace Custom CRDs With `kubernetes-sigs/agent-sandbox`

**What changes:** Replace `AgentPool`, `AgentPod`, and `AgentSession` CRDs and the WarmPoolController with the upstream `SandboxTemplate`, `SandboxWarmPool`, `SandboxClaim`, and `Sandbox` CRDs from `kubernetes-sigs/agent-sandbox`.

**Mapping:**

| Lenny CRD | Agent-Sandbox CRD |
|---|---|
| `AgentPool` | `SandboxTemplate` |
| `AgentPod` | `Sandbox` + `SandboxWarmPool` |
| `AgentSession` | `SandboxClaim` |
| WarmPoolController | Upstream SIG Apps controller |

`executionMode` (`session`, `task`, `concurrent`) is declared on `SandboxTemplate` from v1 even if `task` and `concurrent` land slightly later in the build sequence.

**Pre-commit requirement:** Verify `SandboxClaim` optimistic-locking guarantee matches Lenny's current status PATCH approach before committing.

**Affects:** Sections 4.6, 6.1, 6.2, 6.3, Phase 1 and Phase 3.

---

### 1.2 Runtime Unification: `Runtime` Replaces `RuntimeType` and `SessionTemplate`

**What changes:** `RuntimeType` and `SessionTemplate` are unified into a single `Runtime` concept with an optional `baseRuntime` field distinguishing standalone from derived runtimes.

```yaml
# Standalone runtime
name: langgraph-runtime
image: registry.example.com/langgraph:latest
type: agent
capabilities:
  interaction: one_shot     # one_shot | multi_turn
  injection:
    supported: true
    modes: [immediate, queued]
executionMode: task
isolationProfile: sandboxed
allowedResourceClasses: [small, medium, large]
delegationPolicyRef: orchestrator-policy
labels:
  team: platform
  approved: "true"

# Derived runtime
name: research-pipeline
baseRuntime: langgraph-runtime
workspaceDefaults:
  files:
    - path: agent.py
      content: "..."
  setupCommands:
    - pip install -r requirements.txt
setupPolicy:
  timeoutSeconds: 300
  onTimeout: fail
agentInterface: ...
delegationPolicyRef: research-policy
publishedMetadata: ...
labels:
  team: research
  approved: "true"
```

**Labels are required from v1** — primary mechanism for environment `runtimeSelector` and `connectorSelector` matching.

**Inheritance rules — never overridable on derived runtime:** `type`, `executionMode`, `isolationProfile`, `capabilities.interaction`, `allowedResourceClasses`, `allowStandardIsolation` acknowledgment.

**Independently configurable on derived runtime:** Pool settings, `workspaceDefaults`, `setupCommands`, `setupPolicy.timeoutSeconds` (gateway takes maximum of base and derived), `agentInterface`, `delegationPolicyRef` (restrict only), `publishedMetadata`, `labels`, `taskPolicy`.

**Base runtime mutability:** `image` and `name` immutable via API. All other fields mutable with impact validation — changes that would invalidate existing derived runtimes are rejected with a list of affected runtimes.

**Affects:** Section 5.1, Section 15.1 and 15.2, Phase 1.

---

### 1.3 `type` and `capabilities` Fields on Runtime

**What changes:** Every `Runtime` has a `type` field and an optional `capabilities` field.

```yaml
type: agent          # agent (default) | mcp
capabilities:
  interaction: one_shot    # one_shot | multi_turn
  injection:
    supported: true        # default: false
    modes: [immediate, queued]
```

**`type: agent`** — participates in Lenny's task lifecycle. Receives tasks via stdin `{type: "message"}`, has sessions, workspace, delegation, elicitation, multi-turn dialog. Callable via `lenny/delegate_task`.

**`type: mcp`** — hosts an MCP server. Lenny manages pod lifecycle (isolation, credentials, workspace, pool, egress, audit). No task lifecycle. Runtime binary is oblivious to Lenny.

**`capabilities.interaction: multi_turn`** is valid in v1. A runtime declaring `multi_turn` supports the `lenny/request_input` → response cycle and multiple `{type: "message"}` deliveries over the lifetime of a task. Multi-turn requires `capabilities.injection.supported: true` — the gateway enforces this at registration time.

**`capabilities.injection`** declares whether the runtime supports mid-session message delivery. Default: `supported: false`. Gateway rejects injection attempts against unsupported sessions at the API level before they reach the adapter.

**`type: mcp` runtimes have no `capabilities` field.**

**Affects:** Section 5.1, Phase 1.

---

### 1.4 Adapter Protocol: Multi-Server MCP + Lifecycle Channel

**What changes:** The stdin/stdout JSON Lines binary protocol (Section 15.4.1) is replaced by a two-part model.

**Part A — Multiple focused local MCP servers** (intra-pod, stdio or Unix socket):

- **Platform MCP server** — Lenny-specific tools: `lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`, `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`, `lenny/request_input`, `lenny/send_message`, `lenny/get_task_tree`
- **One MCP server per authorized connector** — each connector in the session's delegation policy gets its own independent MCP server. No aggregated connector proxy — aggregation is not lossless per MCP spec.

**No workspace MCP server.** Workspace is materialized to `/workspace/current` before the runtime starts.

**Part B — Lifecycle channel** — separate stdin/stdout stream pair for operational signals:

```
Adapter → Runtime:  lifecycle_capabilities, checkpoint_request,
                    interrupt_request, credentials_rotated, terminate
Runtime → Adapter:  lifecycle_support, checkpoint_ready,
                    interrupt_acknowledged, credentials_acknowledged
```

Optional. Runtimes that don't open it operate in fallback-only mode.

**Runtime integration tiers (agent-type only):**
- **Minimum** — stdin/stdout binary protocol only. Reads `{type: "message"}` from stdin, writes `{type: "response"}` and `{type: "tool_call"}` to stdout. This is the floor.
- **Standard** — minimum plus connects to adapter's platform MCP server and connector servers via the adapter manifest.
- **Full** — standard plus opens the lifecycle channel.

**Adapter manifest:** Written to `/run/lenny/adapter-manifest.json` **before the runtime binary is spawned** — complete and authoritative when the binary starts. Regenerated per task execution.

```json
{
  "platformMcpServer": { "socket": "/run/lenny/platform-mcp.sock" },
  "connectorServers": [
    { "id": "github", "socket": "/run/lenny/connector-github.sock" }
  ],
  "runtimeMcpServers": [],
  "agentInterface": { ... },
  "sessionId": "sess_abc",
  "taskId": "task_root"
}
```

`runtimeMcpServers` slot reserved from v1 for future use by `type:mcp` runtimes accessible via adapter proxy.

**Startup sequence for `type: agent` runtimes:**

1. Pod created by Kubernetes
2. Adapter opens gRPC connection to gateway (mTLS)
3. Adapter writes placeholder manifest (connector servers unknown yet)
4. Adapter signals READY to gateway — pod enters warm pool
5. Gateway assigns session: `PrepareWorkspace` → `FinalizeWorkspace` → `RunSetup` → `AssignCredentials` → `StartSession`
6. Adapter writes **final manifest** (connector servers now known from lease)
7. Adapter spawns runtime binary
8. Runtime reads manifest, connects to MCP servers (Standard/Full), opens lifecycle channel (Full)
9. Adapter sends `lifecycle_capabilities` (Full); receives `lifecycle_support`
10. Adapter delivers first `{type: "message"}` on stdin

**Affects:** Section 15.4 and 15.4.1 (full replacement), Section 4.7, Phase 2.

---

### 1.5 Binary Protocol: Unified `message` Type — `prompt` Removed

**What changes:** The `prompt` inbound message type is removed entirely. The unified `message` type handles all inbound content delivery — the initial task and all subsequent messages. No `sessionState` field — the runtime knows it's receiving its first message by virtue of just having started.

**Inbound messages (adapter → runtime via stdin):**

| `type` | Description |
|---|---|
| `message` | All content delivery: initial task, mid-session injection, reply to `request_input`, sibling notification. Carries optional `slotId` for concurrent-workspace mode. |
| `heartbeat` | Periodic liveness ping |
| `shutdown` | Graceful shutdown with no new task |

**Outbound messages (runtime → adapter via stdout):**

| `type` | Description |
|---|---|
| `response` | Streamed or complete response carrying `OutputPart[]`. Carries `slotId` in concurrent-workspace mode. |
| `tool_call` | Runtime requests a tool call. Carries `slotId` in concurrent-workspace mode. |
| `status` | Optional status/trace update |

**`follow_up` renamed to `message` everywhere.** No `follow_up` type anywhere in the protocol.

**`input_required` outbound message type removed.** Replaced by `lenny/request_input` blocking MCP tool call on the platform MCP server.

**`slotId` for concurrent-workspace multiplexing:** Session mode and task mode messages never carry `slotId` and runtimes for those modes never see it. Concurrent-workspace runtimes implement a dispatch loop keyed on `slotId` — each concurrent slot's messages carry a distinct `slotId` assigned by the adapter. This is the mechanism that allows multiple independent concurrent task streams through a single stdin channel, eliminating the need for any separate delivery mechanism for concurrent-workspace mode.

**Task mode between-task signaling:** Adapter sends `{type: "terminate", reason: "task_complete"}` on the lifecycle channel after a task completes. The next `{type: "message"}` after scrub is the start of the new task.

**Affects:** Section 15.4.1 (binary protocol message types).

---

### 1.6 Internal `OutputPart` Format

**What changes:** `agent_text` streaming event replaced by `agent_output` carrying `OutputPart` array. `TaskResult` and `TaskSpec` use `OutputPart` arrays.

```json
{
  "id": "part_abc123",
  "type": "text",
  "mimeType": "text/plain",
  "inline": "content here",
  "ref": "artifact://...",
  "annotations": { "role": "primary", "final": true },
  "parts": [],
  "status": "streaming | complete | failed"
}
```

- `type` is an open string — not a closed enum
- `mimeType` handles encoding separately from semantic type
- `annotations` is an open metadata map
- `parts` allows nesting
- `id` enables part-level streaming updates

SDK helper `from_mcp_content(blocks)` converts MCP content blocks to `OutputPart` arrays.

**Affects:** Section 15.4.1, Section 8.9, Section 7.2.

---

### 1.7 `MessageEnvelope` — Unified Message Format

**What changes:** All inbound content messages use a unified `MessageEnvelope` across the stdin binary protocol, platform MCP server tools, and all external APIs.

```json
{
  "id": "msg_xyz789",
  "from": {
    "kind": "client | agent | system | external",
    "id": "..."
  },
  "inReplyTo": "req_abc123",
  "threadId": "thread_001",
  "delivery": "immediate",
  "input": [OutputPart[]]
}
```

**Adapter-injected fields — runtime never supplies these:**
- `from.kind` and `from.id` — injected by the adapter from execution context
- `requestId` in `lenny/request_input` — generated by the gateway; runtime only supplies `parts`

**`delivery`** — optional mechanical hint. `immediate` means deliver at next stdin read (after interrupt acknowledgment if needed). Absent means queue for next natural pause.

**`id`** — every message has a stable ID enabling threading, reply tracking, and non-linear context retrieval.

**`inReplyTo`** — optional. If it matches an outstanding `lenny/request_input` call on the target, the gateway resolves that tool call directly instead of delivering to stdin.

**`threadId`** — optional. In v1 one implicit thread per session. Multi-thread sessions are additive post-v1.

**Affects:** Section 7.2, Section 15.4.1, Section 9.

---

### 1.8 TaskRecord Uses Messages Array + `input_required` State

**What changes:** Task record schema uses a messages array forward-compatible with multi-turn dialog:

```json
{
  "messages": [
    { "role": "caller", "parts": [...] },
    { "role": "agent",  "parts": [...], "state": "completed" }
  ]
}
```

**Task state machine — `input_required` is reachable in v1:**
```
submitted → running → completed
                    → failed
                    → input_required   (reachable via lenny/request_input)
```

`lenny/await_children` (and `lenny/await_child`) unblock when a child enters `input_required`, returning a partial result with the child's question and `requestId`. The gRPC `AwaitChildren` call is a streaming response — it yields partial events before the final settled result.

**Affects:** Section 8.9.

---

### 1.9 Session State Machine Gains `suspended`

**What changes:** `interrupt_request` on the lifecycle channel produces a distinct `suspended` session state:

```
running → suspended   (interrupt_request + interrupt_acknowledged)
suspended → running   (resume_session — no new content)
suspended → running   (POST /v1/sessions/{id}/messages delivery:immediate)
suspended → completed (terminate)
```

Pod held, workspace preserved, `maxSessionAge` timer paused while suspended. `interrupt_request` is a standalone lifecycle signal — pause-and-decide with decoupled timing. Distinct from `delivery: "immediate"` in a message, which atomically interrupts and delivers content.

**`interrupt_request` does NOT cascade** to children. Budget/lease expiry does cascade. Runtime decides whether to propagate a received interrupt to its children.

**Affects:** Section 7.2, Section 6.1.

---

### 1.10 Single `lenny/delegate_task` Tool

**What changes:** One delegation tool. Target id is opaque — runtime does not know whether target is a standalone runtime, derived runtime, or external registered agent.

```
lenny/delegate_task(
  target: string,
  task: TaskSpec,
  lease_slice?: LeaseSlice
) → TaskHandle
```

`TaskSpec`:
```json
{
  "input": [OutputPart[]],
  "workspaceFiles": {
    "export": [{ "glob": "src/auth/**", "destPrefix": "/" }]
  }
}
```

**`lenny/delegate_task` rejects `type: mcp` targets** with `target_not_an_agent`.

**Affects:** Section 8.2, Section 8.9, Section 9.1.

---

### 1.11 Gateway-Mediated Session Messaging

**What changes:** All inter-session communication is gateway-mediated. Three platform MCP tools:

```
lenny/send_message(
  to: string,             // taskId
  message: MessageEnvelope
) → void

lenny/request_input(
  parts: OutputPart[]     // question content only
) → MessageEnvelope       // blocks until answer arrives

lenny/get_task_tree() → TaskTreeNode
```

`lenny/get_task_tree` returns:
```json
{
  "self": { "taskId": "task_001", "sessionId": "sess_abc" },
  "parent": { "taskId": "task_root", "sessionId": "sess_parent" },
  "children": [...],
  "siblings": [
    { "taskId": "task_002", "sessionId": "sess_xyz",
      "state": "running", "runtime": "analyst-agent" }
  ]
}
```

**`taskId` is the messaging address** — stable across pod recovery generations.

**Message delivery routing — three paths:**
1. **`inReplyTo` matches outstanding `lenny/request_input`** → gateway resolves blocked tool call directly. No stdin delivery, no interrupt.
2. **No matching pending request, runtime available** → `{type: "message"}` to stdin at next read.
3. **No matching pending request, runtime blocked in `await_children`** → buffered in inbox; delivered before the next `await_children` event.

**`lenny/send_to_child` is active in v1** — it is not a stub. Delivers a `{type: "message"}` to a child session via the same gateway-mediated path. This is the primary mechanism a parent agent uses to continue a multi-turn conversation with a child that has not entered `input_required`.

**`lenny/request_input` replaces the stdout `input_required` message type.**

**Affects:** Section 8.2, Section 9, Section 7.2.

---

### 1.12 `await_children` Unblocks on `input_required`

**What changes:** `lenny/await_children` unblocks when a child enters `input_required` state. The partial result carries the child's question and `requestId`. The gRPC `AwaitChildren` call is a streaming response.

When the parent calls `lenny/send_message` with `inReplyTo: "req_001"`, the gateway resolves the child's blocked `lenny/request_input` tool call directly. The parent then re-awaits.

**Affects:** Section 8.9, Section 9.

---

### 1.13 `DelegationPolicy` as First-Class Resource

**What changes:** `allowedRuntimes`, `allowedConnectors`, and `allowedPools` replaced by named `DelegationPolicy` resources with tag-based matching evaluated at delegation time.

**Effective policy = `base_policy ∩ derived_policy`** — derived runtime policy can only restrict.

**`lenny/discover_agents`** returns `type: agent` runtimes and external agents only. Results are policy-scoped. `type: mcp` runtimes do not appear.

**Affects:** Section 8.3, Section 5.1.

---

### 1.14 `executionMode` on Pool Configuration — All Three in V1

```yaml
executionMode: session | task | concurrent
```

All three modes implemented in v1. Graph mode removed as a separate concept — graph-aware runtimes are session-mode runtimes that optionally emit trace spans.

**Affects:** Section 5.2, Phase 1.

---

### 1.15 `agentInterface` Field on Runtime

**What changes:** `type: agent` runtimes gain an optional `agentInterface` field serving discovery, A2A card auto-generation, and adapter manifest summaries.

```yaml
agentInterface:
  description: "Analyzes codebases and produces refactoring plans"
  inputModes:
    - type: "text/plain"
    - type: "application/json"
  outputModes:
    - type: "text/plain"
      role: "primary"
  supportsWorkspaceFiles: true
  skills:
    - id: "review"
      name: "Code Review"
      description: "Reviews code for quality and correctness"
  examples:
    - description: "Review auth module"
      input: "Review the authentication module"
```

`type: mcp` runtimes do not have `agentInterface`.

**Affects:** Section 5.1.

---

### 1.16 `publishedMetadata` on Runtime

```yaml
publishedMetadata:
  - key: agent-card
    contentType: application/json
    visibility: public    # internal | tenant | public
    value: '...'
```

Not-found and not-authorized produce identical responses — no enumeration.

**Affects:** Section 5.1.

---

### 1.17 `ExternalAdapterRegistry` — Replaces Single Adapter

**What changes:** Gateway uses `ExternalAdapterRegistry` with simultaneously active adapters routing by path prefix. `HandleDiscovery` required on all adapters.

**Three tiers of pluggability:** Built-in (compiled in), config-driven (Go plugin or gRPC at startup), runtime registration via admin API.

**Built-in adapter inventory:**

| Adapter | Path prefix | Protocol |
|---|---|---|
| `MCPAdapter` | `/mcp` | MCP Streamable HTTP |
| `OpenAICompletionsAdapter` | `/v1/chat/completions` | OpenAI Chat Completions |
| `OpenResponsesAdapter` | `/v1/responses` | Open Responses Specification |
| `A2AAdapter` | `/a2a/{runtime}` | A2A (post-v1) |
| `AgentProtocolAdapter` | `/ap/v1/agent` | AP (post-v1) |

`OpenResponsesAdapter` covers both Open Responses-compliant clients and OpenAI Responses API clients. OpenAI's Responses API is a proper superset of Open Responses; the difference is OpenAI's proprietary hosted tools, which Lenny doesn't implement.

**Affects:** Section 4.1, Section 15, Phase 1.

---

### 1.18 `type: mcp` Runtime Dedicated MCP Endpoints

Each enabled `type: mcp` runtime gets a dedicated MCP endpoint at `/mcp/runtimes/{runtime-name}`. Not aggregated. An implicit session record is created per connection for audit and billing.

**Discovery:** `GET /v1/runtimes` and `list_runtimes` return `mcpEndpoint` and `mcpCapabilities.tools` preview.

**Affects:** Section 4.1, Section 9, Section 15.

---

### 1.19 Comprehensive Admin API

**Operational plane — API-managed:** Runtimes, Delegation Policies, Connectors, Pools, Credential Pools, Tenants, Quotas, User Role Assignments, Egress Profiles, Experiments, Scaling Policies, Memory Store Config, Webhooks, External Adapters, Environments, Tenant RBAC Config.

**Bootstrap plane — Helm only:** DB URLs, Redis, MinIO, KMS, cluster name, namespace assignments, certificate paths, `LENNY_DEV_MODE`, system-wide defaults, Kubernetes object definitions.

CRDs become derived state reconciled from Postgres by PoolScalingController.

**Affects:** Section 15.1, Section 10.2, Phase between 4 and 5.

---

## Part 2: V1 Must-Have — Feature Changes

---

### 2.1 Multi-Turn Agent Dialog

**What changes:** `interaction: multi_turn` is valid in v1. This is not a stub — it is a fully implemented capability for runtimes that declare it.

**What `multi_turn` means precisely:** A runtime that declares `multi_turn` may call `lenny/request_input` multiple times during a single task, receive multiple `{type: "message"}` deliveries, and produce partial outputs before reaching a final response. The task does not end after the first `response` message on stdout.

**Constraints:**
- `interaction: multi_turn` requires `capabilities.injection.supported: true` — the gateway enforces this at runtime registration. A multi-turn runtime that doesn't accept injections is incoherent.
- `interaction: one_shot` runtimes that call `lenny/request_input` more than once get a gateway error on the second call.
- Multi-turn is independent of external protocol. A2A natively supports multi-turn via its `input-required` state — this maps directly to Lenny's `input_required` task state. A2A is still post-v1; multi-turn within Lenny's internal task model is v1.

**`lenny/send_to_child` is active.** It delivers a `{type: "message"}` to a child session. Combined with `await_children` unblocking on `input_required`, this enables full synchronous parent-child multi-turn conversations within Lenny.

**Affects:** Section 5.1, Section 9, Phase 1 (capabilities schema), Phase 9 (tool activation).

---

### 2.2 Task Pod Execution Mode

```yaml
taskPolicy:
  cleanupCommands:
    - pkill -f jupyter_kernel
    - rm -rf /tmp/sandbox-*
  cleanupTimeoutSeconds: 30
  onCleanupFailure: warn    # warn | fail
```

Lifecycle: terminate(task_complete) on lifecycle channel → cleanup commands → Lenny scrub → pod available. `setupCommands` run once per pod at start, not per task. Deployer acknowledgment required.

**Affects:** Sections 6.1, 6.2, 7, 5.2.

---

### 2.3 Concurrent Execution Mode

Two sub-variants via `concurrencyStyle`:

```yaml
executionMode: concurrent
concurrencyStyle: stateless    # stateless | workspace
maxConcurrent: 8
```

**`concurrencyStyle: workspace`** — each slot gets its own workspace. Gateway tracks per-slot lifecycle. **Task delivery via `slotId` multiplexing over stdin** — the adapter assigns a `slotId` per slot; the runtime implements a dispatch loop keyed on `slotId`; all binary protocol messages (inbound and outbound) carry `slotId` in this mode. This closes the concurrent-workspace delivery gap without any separate delivery mechanism. Cross-slot isolation is process-level and filesystem-level — explicitly weaker than session mode. Deployer acknowledgment required.

**`concurrencyStyle: stateless`** — no workspace materialization. Gateway routes through Kubernetes Service. Pod readiness probe reflects slot availability. PoolScalingController watches `active_slots / (pod_count × maxConcurrent)`.

**Truly stateless runtimes** with no workspace and no expensive shared state should be registered as external connectors, not Lenny-managed pods.

**Affects:** Sections 5.2, 6.1, 6.2, 4.1.

---

### 2.4 `setupPolicy` on Runtime

```yaml
setupPolicy:
  timeoutSeconds: 300      # optional — waits indefinitely if absent
  onTimeout: fail          # fail | warn
```

Gateway takes the **maximum** of base and derived `timeoutSeconds` if both set.

**Affects:** Section 5.1, Section 6.2.

---

### 2.5 `ConnectorDefinition` as First-Class Resource

Connector configuration is a first-class admin API resource. Includes `labels` map for environment selector matching. Tool capability metadata derived from MCP `ToolAnnotations` at registration time.

**Affects:** Section 4.3, Section 9.3.

---

### 2.6 Runtime Discovery on All External Interfaces

Every external interface exposes runtime discovery via `HandleDiscovery`. All results are identity-filtered and policy-scoped. Not-found and not-authorized produce identical responses.

- **MCP:** `list_runtimes` tool
- **REST:** `GET /v1/runtimes` with full `agentInterface` and `mcpEndpoint` fields
- **OpenAI Completions:** `GET /v1/models`
- **Open Responses:** `GET /v1/models`

**Affects:** Section 9, Section 15.1, Section 15.2.

---

### 2.7 OpenAI Completions Adapter

Implements `POST /v1/chat/completions` and `GET /v1/models`. Stateless, ephemeral sessions. Tool handling — opaque mode only.

**Affects:** Section 15.

---

### 2.8 Open Responses Adapter

Implements the Open Responses Specification. Covers both Open Responses-compliant and OpenAI Responses API clients for all core features. Lenny connectors → Open Responses **internal tools**. Client-defined functions (external tools) not supported in v1.

**Affects:** Section 15.

---

### 2.9 Capability Inference from MCP `ToolAnnotations`

Gateway reads `tools/list` at connector or `type:mcp` runtime registration and infers capabilities from MCP `ToolAnnotations`. No manual re-annotation required.

| MCP annotation | Inferred capabilities |
|---|---|
| `readOnlyHint: true` | `read` |
| `readOnlyHint: false, destructiveHint: false` | `write` |
| `destructiveHint: true` | `write, delete` |
| `openWorldHint: true` | `network` |
| No annotations | `admin` (conservative default) |
| *(no MCP equivalent)* | `execute`, `admin` — set via `toolCapabilityOverrides` |

Tenant-overridable via `tenantRbacConfig.mcpAnnotationMapping`.

**Affects:** Section 4.3, Section 5.1.

---

### 2.10 PoolScalingController

Separate `PoolScalingController` from `WarmPoolController`. Pluggable `PoolScalingStrategy` interface.

Default formula: `target_minWarm = ceil(base_demand_p95 × variant_weight × safety_factor)`. `safety_factor` defaults to 1.5 for agent-type, 2.0 for mcp-type. `scaleToZero` disabled by default for `type: mcp` pools.

**Affects:** Section 4.6, Section 5.2.

---

### 2.11 Experiment Primitives

`ExperimentDefinition` as first-class admin API resource. `ExperimentRouter` as built-in `RequestInterceptor`. Experiment context propagates through delegation leases. Results API aggregates eval scores by variant. PoolScalingController manages variant pool lifecycle automatically.

**Affects:** Section 4.8, Section 15.1.

---

### 2.12 Injection Capability Declaration and External Session Messaging

`POST /v1/sessions/{id}/messages` is the unified client-facing endpoint for all message types. MCP equivalent: `send_message` tool. Gateway rejects injection attempts against sessions whose runtime has `injection.supported: false`.

Interrupt remains separate: `POST /v1/sessions/{id}/interrupt` — lifecycle signal, not content delivery.

**Affects:** Section 7.2, Section 15.1.

---

## Part 3: V1 Should-Have

---

### 3.1 Environment Resource and RBAC Model

**What changes:** `Environment` as first-class admin API resource. Named, RBAC-governed project context grouping runtimes and connectors for a team.

**Two access paths:**
- **Transparent filtering** (default) — user connects to standard endpoint; gateway computes the union of authorized runtimes across all environments where the user's groups have a role and returns that filtered view.
- **Explicit environment endpoint** (opt-in) — dedicated paths across all external interfaces: `/mcp/environments/{name}`, `/v1/environments/{name}/sessions`, scoped model namespace on `/v1/responses` and `/v1/chat/completions`.

**Runtime and connector selection is tag-based** using Kubernetes-style label expression syntax with `include`/`exclude` overrides.

**`mcpRuntimeFilters`** — capability-based tool filtering for `type: mcp` runtimes. Name collisions resolved by `runtime:tool` qualified reference in `overrides`.

**`connectorSelector`** — internal only. Controls what connectors agents can use via the platform MCP server.

**Cross-environment delegation** — structured bilateral declaration model:

```yaml
crossEnvironmentDelegation:
  outbound:
    - targetEnvironment: platform-services
      runtimes:
        matchLabels:
          shared: "true"
    - targetEnvironment: analytics-team
      runtimes:
        ids: [analytics-agent]
  inbound:
    - sourceEnvironment: "*"
      runtimes:
        matchLabels:
          shared: "true"
```

Effective cross-environment access requires both sides to permit it. Neither side can unilaterally grant the other access. The boolean shorthand (`false` | `true`) remains valid for simple cases. `true` bypasses environment scoping and falls through to DelegationPolicy only. Boolean and structured form are mutually exclusive on any given environment.

**Effective delegation scope:** `(environment definition ∪ cross-environment permitted runtimes) ∩ delegation policy`

**Gateway enforcement at delegation time:**
1. Resolve target runtime to its home environment
2. Verify calling environment has an outbound declaration permitting target → `target_not_in_scope` if absent
3. Verify target environment has an inbound declaration permitting calling environment → `target_not_in_scope` if absent
4. Apply DelegationPolicy as normal → `target_not_authorized` if policy doesn't permit

**Connectors are never cross-environment.** Child sessions use their own environment's connector configuration.

**Environment-level billing rollup (v1):** `environmentId` populated on all billing events for sessions created in an environment context. New endpoint: `GET /v1/admin/environments/{name}/usage`.

**Advanced membership analytics (v1):** Three read-only endpoints:
- `GET /v1/admin/tenants/{id}/access-report` — cross-environment access matrix
- `GET /v1/admin/environments/{name}/access-report` — resolved member list with group expansion
- `GET /v1/admin/environments/{name}/runtime-exposure` — runtimes/connectors in scope with capability filters

**`noEnvironmentPolicy`:** `deny-all` (platform default) or `allow-all`. Configurable per tenant, with platform-wide default at Helm time.

**Identity:** OIDC. Groups from LDAP/AD carried as JWT claims. `introspectionEnabled: true` adds real-time group checks at latency cost.

**Full configuration template:** See the RBAC design section for the complete config schema covering `tenantRbacConfig` and `environment` resource definitions.

**Member roles:** `viewer`, `creator`, `operator`, `admin`.

**V1 data model accommodations:**
- `Runtime` resources need `labels` map from v1
- `Connector` registrations need `labels` map from v1
- `type:mcp` runtime tool schemas cached by gateway from v1
- `GET /v1/runtimes` and `list_runtimes` accept optional `?environmentId=` stub
- Session creation accepts optional `environmentId` parameter as no-op stub
- `environmentId` as nullable field on billing event schema from Phase 1
- `crossEnvironmentDelegation` structured form schema from Phase 1

**Affects:** Section 5.1 (labels), Section 4.1 (identity-aware routing), Section 15.1, New Section 10 (RBAC).

---

### 3.2 `MemoryStore` Interface

`MemoryStore` role-based storage interface. Default: Postgres + pgvector. Fully replaceable. Technology choice deferred. Accessed via `lenny/memory_write` and `lenny/memory_query` on platform MCP server.

**Affects:** Section 4, Section 9.

---

### 3.3 Semantic Caching at the LLM Proxy

Optional `CachePolicy` on `CredentialPool`. Pluggable `SemanticCache` interface — default Redis, fully replaceable. Disabled by default.

**Affects:** Section 4.9.

---

### 3.4 `RequestInterceptor` Extension Point

Interceptor interface at phases: `PreAuth`, `PostAuth`, `PreRoute`, `PostRoute`, `PreToolResult`, `PostAgentOutput`. Built-in interceptors: `ExperimentRouter`, `GuardrailsInterceptor` (disabled, no built-in logic).

**Affects:** Section 4.8.

---

### 3.5 Agent Evaluation Hooks

`POST /v1/sessions/{id}/eval` accepts scored results. `POST /v1/sessions/{id}/replay` re-runs against different runtime version. No built-in eval logic.

**Affects:** Section 15.1, Section 16.

---

### 3.6 `CredentialRouter` Interface

Pluggable credential pool selection logic. Default: least-loaded/round-robin/sticky-until-failure. Disabled by default, opt-in per pool.

**Affects:** Section 4.9.

---

## Part 4: Planned / Post-V1

---

### 4.1 A2A Full Support

**What to document now:**
- `ExternalProtocolAdapter` is the mechanism. A2A is implementation two.
- Inbound: gateway serves `POST /a2a/{runtimeName}/tasks` via `A2AAdapter`
- Outbound: external A2A agents registered as connectors, callable via `lenny/delegate_task`
- A2A `input-required` state maps directly to Lenny's `input_required` task state — multi-turn over A2A is the primary external multi-turn use case
- A2A streaming output via SSE requires the adapter to bridge SSE events to `OutputPart` events through the parent's `await_children` streaming response — this is the primary implementation complexity for A2A multi-turn
- `agentInterface` auto-generates A2A agent cards
- Per-agent A2A endpoints (not aggregated) at `/a2a/runtimes/{name}`
- `allowedExternalEndpoints` slot on delegation lease schema must exist from v1
- Runtime authors who want intra-pod A2A client behavior must opt in explicitly

---

### 4.2 A2A Intra-Pod Support

**What to document now:** Adapter additionally serves per-agent A2A endpoints intra-pod. Adapter manifest gains A2A base URL. All A2A traffic proxied through gateway. Runtime authors opt in explicitly.

---

### 4.3 Agent Protocol (AP) Support

**What to document now:** AP defines `POST /ap/v1/agent/tasks` and step execution. Third `ExternalProtocolAdapter` implementation. No changes to intra-pod model.

---

### 4.4 Future Conversational Patterns

**What to document now:** `MessageEnvelope` with `id`, `from`, `inReplyTo`, `threadId` accommodates all of these without schema changes: threaded messages, multiple participants, non-linear context retrieval, broadcast, external agent participation.

---

### 4.5 Environment Management UI

**What to document now:** Full web UI for browsing environments, editing membership, and previewing selector matches is thin-client work over the admin API. The `?dryRun=true` parameter on `PUT /v1/admin/environments` is the preview mechanism and ships in v1.

---

### 4.6 Environment Resource — Post-V1 Deferred Items

**Full environment management UI:** Deferred. Admin API ships in v1. UI is thin-client work, can be built at any point without spec changes.

**Cross-environment delegation richer controls:** The structured bilateral declaration model (outbound/inbound) is v1. What is deferred is runtime-level cross-environment exceptions at granularity beyond the structured form, and integration with experiment-scoped delegation boundaries. These require a design pass once the base feature is in production.

---

### 4.7 Multi-Cluster Federation

**What to document now:** Session IDs must be globally unique. Storage interfaces must not assume single-cluster topology. Connectors registered in one cluster must be expressible by reference in multi-cluster scenarios.

---

### 4.8 UI and CLI

**What to document now:** Admin API is the complete operational surface. Official CLI (`lenny-ctl`) and web portal are separate projects consuming the admin API as thin clients with zero business logic.

---

## Part 5: Explicit Non-Decisions

**5.1 No Model B Runtime Deployment.** No mechanism for packaging a graph definition as a new registered runtime. Users register derived runtimes via admin API.

**5.2 No Built-In Eval Logic.** Lenny provides hooks and storage. No LLM-as-judge or hallucination detection.

**5.3 No Built-In Guardrail Logic.** Lenny provides the `RequestInterceptor` hook. No content classifiers or prompt injection detection.

**5.4 No Built-In Memory Extraction.** Lenny provides the `MemoryStore` interface and tools. Runtimes decide what to write.

**5.5 No Direct External Connector Access.** Connectors are session-internal. External clients do not call connectors directly. The data model accommodates this later without redesign.

**5.6 Hooks-and-Defaults Design Principle.** Every cross-cutting AI capability follows the same pattern: defined as an interface with a sensible default, disabled unless explicitly enabled, fully replaceable. Lenny never implements AI-specific logic.

---

## Part 6: Revised Build Sequence

**Phase 1** — Core types: `Runtime` (unified, with `labels`), `type` field, `capabilities` (including `interaction: one_shot | multi_turn`, `injection`), `executionMode`, `allowedExternalEndpoints` on delegation lease, `input_required` task state, messages array in TaskRecord, `suspended` session state. Agent-sandbox CRDs. `Connector` resource with `labels`. `environmentId` nullable field on billing event schema. `crossEnvironmentDelegation` structured form schema slot.

**Phase 2** — Replace adapter binary protocol: unified `{type:"message"}` (no separate `prompt`), `slotId` field for concurrent-workspace multiplexing, multi-server MCP adapter + lifecycle channel, adapter manifest written before binary spawns. Publish as runtime adapter specification.

**Phase 3** — PoolScalingController. `DelegationPolicy` resource. `setupPolicy` enforcement. `taskPolicy.cleanupCommands`.

**New Phase between 4 and 5** — Admin API foundation: runtimes, pools, connectors, delegation policies, tenant management, external adapters registry. Gateway loads config from Postgres. Capability inference from MCP annotations at connector registration.

**Phase 5** — Gateway `ExternalAdapterRegistry`. MCP adapter, Completions adapter, Open Responses adapter all active. `list_runtimes`, `GET /v1/runtimes`, `GET /v1/models` with identity-aware filtering. `type: mcp` runtime endpoints at `/mcp/runtimes/{name}`. Tenant RBAC config API. `noEnvironmentPolicy` enforcement.

**Phase 9** — `lenny/delegate_task` handles internal and external targets. `lenny/send_message`, `lenny/request_input`, `lenny/get_task_tree`, `lenny/send_to_child` (active). `lenny/discover_agents` with policy scoping. Multi-turn fully operational.

**Phase 10** — `agentInterface` in discovery. Adapter manifest includes summaries.

**Phase 12** — `type: mcp` runtime support. Concurrent execution modes including `slotId` multiplexing for workspace variant.

**New Phase 15** — Environment resource: tag-based selectors, member RBAC, `mcpRuntimeFilters` with capability model, `connectorSelector`, cross-environment delegation enforcement, billing rollup endpoint, membership analytics endpoints, explicit environment endpoints across all adapters.

**New Phase 16** — Experiment primitives, PoolScalingController experiment integration.

**New Phase 17** — Memory, caching, guardrail, eval hooks.

---

## Summary

| Tier | Count |
|---|---|
| V1 Must-Have Breaking | 19 |
| V1 Must-Have Feature | 12 |
| V1 Should-Have | 6 |
| Planned / Post-V1 | 8 |
| Explicit Non-Decisions | 6 |
