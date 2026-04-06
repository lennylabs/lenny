# Technical Design Review — Developer Experience (Runtime Authors)

**Document reviewed:** `docs/technical-design.md`
**Perspective:** 6. Developer Experience (Runtime Authors)
**Category code:** DXR
**Date:** 2026-04-04
**Reviewer scope:** Evaluate the experience from the perspective of someone building a new runtime for Lenny. Assess learning curve, integration tiers, tooling, and whether the spec alone is sufficient to get started.

---

## Summary

The spec has improved substantially since the previous DXR review (DEV-001 through DEV-018 in `review-findings-20260404.md`). The critical and high findings from that pass — missing message schemas, undefined tool call correlation, no annotated trace, missing tier comparison, no quickstart path — were all addressed. The spec now includes a complete binary protocol reference, a tier comparison matrix, an echo runtime pseudocode, a runtime author roadmap, and a `make run` zero-dependency local mode.

What remains are gaps that a developer actually building a runtime will hit: the lifecycle channel transport is still underspecified, the adapter manifest schema is still informal, no Standard-tier MCP client guidance exists, `type: mcp` implementation requirements are missing, error reporting from the runtime is incomplete, and image packaging requirements are undocumented. Several of these are Medium severity but individually actionable.

---

## Findings

---

### DXR-001 Lifecycle Channel Transport Mechanism Not Specified [High]
**Section:** 4.7

Section 4.7 describes the lifecycle channel as "a separate stdin/stdout stream pair for operational signals." A developer building a Full-tier runtime cannot implement this. Two stdin/stdout streams cannot coexist on a single Unix process — each process has exactly one stdin (fd 0) and one stdout (fd 1). The actual transport must be a separate Unix socket, a pair of named pipes, or an additional file descriptor — but none of these options is stated.

The adapter manifest (Section 4.7) includes `platformMcpServer.socket` and `connectorServers[].socket` but has no `lifecycleChannel` entry. If the channel is a Unix socket, it needs a well-known path or an entry in the manifest. If it is file descriptors 3/4 (inherited from the adapter), that must be stated. This is a blocking gap for Full-tier implementation.

**Recommendation:** Specify the lifecycle channel transport mechanism precisely. If it is a Unix socket, add a `lifecycleChannel.socket` field to the adapter manifest JSON. If it is additional inherited file descriptors, document the FD numbers and how they are advertised to the binary. Add a JSON schema for lifecycle message types (using the same JSON Lines format as the binary protocol) and include a worked example in Section 15.4.4.

---

### DXR-002 Adapter Manifest Has No Formal Schema and Minimum-Tier Reading Requirements Are Undefined [High]
**Section:** 4.7

The adapter manifest is presented as a JSON example with narrative description. No formal JSON Schema is provided, no versioning mechanism is documented, and the spec does not state which fields a Minimum-tier runtime must read vs. can safely ignore. A developer reading this section cannot determine:

- What fields are guaranteed to be present (vs. optional/nullable)
- Whether `connectorServers` is always an array (possibly empty) or may be absent
- What `agentInterface` contains (listed as `{ ... }` with no schema)
- Whether additional fields may appear that should be silently ignored

The "regenerated per task execution" note raises a question: does the runtime need to re-read the manifest between tasks (task mode)? The spec does not say.

**Recommendation:** Define the adapter manifest JSON Schema (or at minimum a TypeScript-style interface definition). State explicitly which fields a Minimum-tier runtime needs (answer: none — Minimum-tier ignores the manifest entirely), which fields Standard-tier reads (`platformMcpServer.socket`, `connectorServers`), and which are Full-tier only. Clarify whether the manifest is stable for the session lifetime or may change between tasks. State that unknown fields should be ignored for forward compatibility.

---

### DXR-003 No Guidance on How Standard-Tier Runtimes Implement the MCP Client [High]
**Section:** 15.4.3, 4.7

The spec correctly states that Standard-tier runtimes "connect to the adapter's platform MCP server and connector MCP servers" and that this uses "standard MCP — no Lenny-specific code." However, it provides no guidance on what implementing this means in practice:

- Do runtimes need to implement the MCP client protocol from scratch?
- Is there an official MCP client library the runtime can use?
- The socket transport is "stdio or abstract Unix socket" (Section 4.7, Part A) — which is it? Both? How does the runtime know which one to use?
- How does the runtime discover which MCP tools are available (tools/list)?
- What MCP protocol version should the client target?

This is directly relevant to the user's stated concern about minimizing SDK requirements. The spec says Standard-tier requires "no Lenny-specific code" but says nothing about whether a third-party MCP client library is needed. Implementing MCP from scratch over an abstract Unix socket is a significant and underspecified undertaking.

**Recommendation:** Add a "Standard-Tier MCP Integration" subsection under 15.4.3. State which transport variant the adapter uses (recommendation: abstract Unix socket `@lenny-*` as specified in the manifest, always). State the MCP protocol version the local servers speak. Explicitly state whether an existing MCP client library (e.g., the official `@modelcontextprotocol/sdk` for Node.js, or `mcp-go` for Go) can be used out of the box against the adapter's local servers, and whether any Lenny-specific initialization is required. If the adapter always speaks standard MCP over the socket, state this clearly so runtime authors know a standard MCP client library is sufficient.

---

### DXR-004 `type: mcp` Runtime Requirements Undocumented [Medium]
**Section:** 5.1

The spec describes `type: mcp` runtimes at high level: "Lenny manages pod lifecycle. Runtime binary is oblivious to Lenny. No task lifecycle." But a developer wanting to build one cannot determine:

- Does the runtime binary just need to listen on a port or socket?
- How does the gateway discover the runtime's MCP endpoint (is it a well-known port? Configured in the Runtime definition? Registered via adapter manifest?)?
- Does the binary need the adapter sidecar, or does it run alone?
- Is the adapter sidecar present for `type: mcp` runtimes at all?
- What health check does the binary need to expose for the warm pool controller?

The build sequence (Phase 12b) adds "`type: mcp` runtime support" but provides no further detail in the main sections. Section 15.4.3 explicitly says "Runtime Integration Tiers (agent-type only)" — so `type: mcp` runtimes are entirely outside the tier system with no separate documentation.

**Recommendation:** Add a dedicated subsection (e.g., Section 5.1.1 or within 15.4) titled "`type: mcp` Runtime Requirements" describing: whether the adapter sidecar is used, how the gateway discovers the MCP endpoint (e.g., a well-known port declared in the Runtime definition), the health check contract, and any startup/shutdown behavior the binary must implement. The claim "runtime binary is oblivious to Lenny" needs to be verified as accurate — if the binary just needs to speak MCP over a port, that should be stated explicitly.

---

### DXR-005 Runtime Image Packaging Requirements Not Documented [Medium]
**Section:** 4.7, 17.4

A runtime author building a Docker image for their runtime binary faces undocumented requirements. The spec mentions:

- Adapter runs as UID 1000, agent as UID 1001 (Section 4.7) — but this is buried in the security boundary discussion, not surfaced as a packaging requirement
- `/workspace/current`, `/workspace/staging`, `/sessions`, `/artifacts` must exist (Section 6.4) — but who creates them? The adapter? The runtime image?
- Read-only root filesystem is required (Section 13.1) — what does this mean for `pip install` or `npm` in the runtime image?
- The manifest volume (`/run/lenny/`) is an emptyDir injected by the adapter — not part of the runtime image

There is no section titled "Runtime Image Requirements" or "Packaging Guide" that brings these constraints together. A developer building their first runtime image has to discover these scattered facts by reading the entire spec.

**Recommendation:** Add a "Runtime Image Requirements" section (suggest under 15.4, before the echo runtime). Cover: expected UID/GID for the runtime binary, which filesystem paths the runtime expects the adapter to create vs. must be present in the image, read-only rootfs implications and how to handle runtimes that need to install dependencies at setup time (setup commands vs. pre-baked dependencies), and the sidecar injection model (the adapter sidecar is added automatically — runtime authors do not include it in their image).

---

### DXR-006 Error Reporting from Runtime Binary Is Incomplete [Medium]
**Section:** 15.4.1, 16.3

The spec defines exit codes (Section 15.4.1 "Exit Codes" table) and states "stderr is captured by the adapter for logging and diagnostics but is not parsed as protocol messages." However, a runtime author needs to know:

- How does a runtime signal a non-fatal error during a task (e.g., the runtime processed the message but encountered a transient failure)? There is no `error` outbound message type defined.
- The `response` outbound message has no error field — a failed task presumably exits with code 1, but the client receives no structured error detail.
- The `status` outbound message has `state` and `message` fields — can this be used for error signaling, or is it purely informational?
- Can a runtime emit a structured error via stdout before exiting?

The error categories in Section 16.3 (`TRANSIENT`, `PERMANENT`, `POLICY`, `UPSTREAM`) are gateway-facing and not mapped to runtime exit behavior. A runtime author has no guidance on how to produce a structured error that propagates to the client.

**Recommendation:** Add an "Error Reporting" paragraph to Section 15.4.1. Define whether a runtime can emit a structured error object on stdout (e.g., `{"type": "error", "code": "...", "message": "...", "retryable": true}`) before exiting, or whether exit code + stderr is the only channel. If a structured error response is supported, add it to the outbound message table and provide a JSON example. Clarify the mapping from exit codes to gateway error categories so runtime authors can signal retryable vs. permanent failures.

---

### DXR-007 Shutdown vs. Terminate Signal Ambiguity for Full-Tier Runtimes [Medium]
**Section:** 4.7, 15.4.1

Two overlapping shutdown mechanisms exist for Full-tier runtimes:

1. `{type: "shutdown"}` on stdin (binary protocol) — tells the runtime to finish and exit within `deadline_ms`
2. `terminate` on the lifecycle channel — described in Section 4.7 as part of the lifecycle channel signals from adapter to runtime

The spec states in Section 15.4.2 that `DRAINING` state uses "graceful shutdown coordination before `shutdown`" for Full-tier, implying the lifecycle `terminate` comes first, followed by stdin `shutdown`. But this ordering is never explicitly stated. Additionally, the task-mode between-task signaling (Section 15.4.1) says the adapter sends `{type: "terminate", reason: "task_complete"}` on the lifecycle channel after a task completes, which is different from the per-session `terminate`. A Full-tier runtime author needs to know:

- Which signal arrives first when a session ends normally?
- What is the expected response to each signal?
- Is `shutdown` on stdin always sent after `terminate` on the lifecycle channel, or can either arrive independently?

**Recommendation:** Add a "Shutdown Sequencing" table or paragraph to Section 15.4.3 (or the lifecycle channel description in 4.7). Define the ordering for each tier: for Full-tier, the sequence should be: (1) `terminate` on lifecycle channel → runtime acknowledges → (2) `{type: "shutdown"}` on stdin → runtime exits. Clarify that `terminate(task_complete)` in task mode is distinct from the session `terminate`. Document what happens if the runtime receives `shutdown` without a prior `terminate` (e.g., adapter crash path).

---

### DXR-008 Credential File Schema Not Published [Medium]
**Section:** 4.7

Section 4.7 states credentials are delivered via `/run/lenny/credentials.json` (mode `0400`, agent UID). The runtime binary reads this file to obtain its LLM provider credentials. However:

- No JSON schema is provided for this file
- The `materializedConfig` structure in Section 4.9 shows an example (`{"apiKey": "sk-ant-...", "baseUrl": "..."}`) but only for `anthropic_direct`
- For `aws_bedrock`, the runtime would need STS session credentials in a different format; for `vertex_ai`, a different format again
- In proxy mode, the runtime receives a "lease token and proxy URL" — but what is the exact file structure?
- A runtime author cannot write a credential reader without knowing the file schema per provider

**Recommendation:** Add a "Credential File Schema" section to Section 4.9 (or a new subsection of 15.4). Define the top-level structure of `/run/lenny/credentials.json` and the per-provider `materializedConfig` schemas. For proxy mode, show the exact fields. Document that unknown `provider` values should be handled gracefully (e.g., the runtime fails with an informative error). This file is the primary integration point for Standard and Full-tier runtimes that use LLM providers.

---

### DXR-009 Echo Runtime Has No Actual Runnable Artifact [Medium]
**Section:** 15.4.4

The echo runtime in Section 15.4.4 is described as "the project includes a reference `echo-runtime`" and provides pseudocode. The pseudocode correctly covers Minimum-tier protocol handling but:

- It is pseudocode, not real code in any language — a developer cannot run it directly
- The project does not include it as an actual artifact in any documented repository location
- There is no Standard-tier echo variant demonstrating MCP tool usage
- The pseudocode omits the startup race condition: if the runtime reads stdin before the manifest is written, it may miss the manifest; this timing is not documented

The spec says the echo runtime "serves two purposes: platform testing and template for custom runtimes." The template purpose is significantly weakened by it being pseudocode rather than actual runnable code in the target audience's likely languages (Python, TypeScript, Go).

**Recommendation:** Commit an actual echo runtime implementation in at least one language (Go, being the platform language, or Python for accessibility) at a documented path (e.g., `examples/echo-runtime/`). The implementation should demonstrate: reading the manifest, stdin message loop, heartbeat handling, shutdown handling, and structured response output. Add a Standard-tier example that connects to the platform MCP server socket and calls one tool (e.g., `lenny/output`). Reference these examples from Section 15.4.4 by path.

---

### DXR-010 No Guidance for Runtime Authors on Testing Without Admin Access [Medium]
**Section:** 17.4, 5.1

A community runtime author using `make run` (Section 17.4) needs to register their runtime with the platform before they can test it. Section 5.1 shows the minimal runtime configuration YAML, and the bootstrap mechanism (Section 17.6) allows seeding runtimes. However:

- It is not documented whether `make run` / `lenny-dev` allows runtime registration via the admin API without Kubernetes or platform-admin credentials
- The `lenny-ctl bootstrap` path suggests writing to a YAML file, but whether this is accessible in dev mode is not stated
- A developer with only the runtime binary and `make run` may not know how to register their runtime

The Runtime Author Roadmap (Section 15.4.5) references "Section 17.4 — Local Development Mode" as step 6 but does not explain what the developer does after running `make run` to register their runtime.

**Recommendation:** Add a "Registering Your Runtime in Dev Mode" section to Section 17.4 (or as a step in 15.4.5). Confirm that the admin API is fully functional in `make run` mode without credentials (dev mode relaxes auth — Section 17.4 mentions TLS is disabled but does not mention auth). Provide a one-command example: `curl -s http://localhost:8080/v1/admin/runtimes -d @my-runtime.json`. Include a minimal `my-runtime.json` sample. Reference the smoke test that validates the registered runtime.

---

### DXR-011 `OutputPart.annotations.protocolHints` Field Undiscoverable [Low]
**Section:** 15.4.1

Section 15.4.1 defines `protocolHints` as a sub-key within `OutputPart.annotations` that "adapters read and remove before serializing." This is a useful feature for runtime authors who want to influence how their output is rendered by different external adapters (MCP vs. OpenAI). However:

- `protocolHints` is described only within the translation fidelity matrix footnote, not in the main `OutputPart` properties list
- The `OutputPart` JSON schema shows `"annotations": { ... }` as an open map — `protocolHints` structure is only shown in a separate code block
- The tier comparison matrix and echo runtime make no mention of it

A developer building an output-rich Standard-tier runtime who wants to control MCP rendering would not discover this feature from the main protocol documentation.

**Recommendation:** Add `protocolHints` as a named entry in the `OutputPart` properties table in Section 15.4.1, with a pointer to the fidelity matrix footnote for details. Mark it as "optional, advanced" so Minimum-tier authors know they can safely ignore it.

---

### DXR-012 `slotId` Multiplexing Has No Implementation Guidance [Low]
**Section:** 15.4.1, 5.2

Section 5.2 describes concurrent-workspace mode and states "the runtime implements a dispatch loop keyed on `slotId`." Section 15.4.1 confirms that concurrent-workspace messages carry `slotId`. However:

- The `message` inbound schema in Section 15.4.1 does not include `slotId` in the example JSON
- The `response` outbound schema does not include `slotId` in the example JSON
- No dispatch loop example is provided
- The Tier Comparison Matrix in 15.4.3 does not mention `slotId` or concurrent-workspace as a tier concern

This is marked as "Low" because concurrent-workspace mode is an advanced feature. However, a developer targeting `executionMode: concurrent` has no protocol reference for the `slotId` field.

**Recommendation:** Add `slotId` to the `message` and `response` schema examples in Section 15.4.1, noting it is absent in session/task mode and present in concurrent-workspace mode. Add a brief dispatch loop pseudocode alongside the existing echo runtime pseudocode, or add a note in the concurrent-workspace section of 5.2 that full protocol details are in the runtime adapter specification (which is a separate document per Section 15.4). Mark concurrent-workspace as requiring the adapter specification review before implementation.

---

### DXR-013 Runtime Registration Is Platform-Admin Only — No Community Path [Low]
**Section:** 5.1, 10.2

Section 5.1 states runtimes are "registered via the admin API as static configuration." The admin API (Section 10.2, 15.1) requires `platform-admin` role for `POST /v1/admin/runtimes`. There is no self-service registration path for community runtime authors or tenant-level operators.

The `make run` local dev mode mitigates this for local testing, but:

- A community runtime author who wants to test against a shared staging deployment cannot register their own runtime without platform-admin access
- The Environment RBAC model (Section 10.6) has `admin` role for environments but this does not grant runtime registration rights
- Section 22.3 mentions "community adoption strategy" but the registration friction is not addressed

**Recommendation:** Either (a) expose runtime registration at `tenant-admin` role level with a flag for multi-tenant deployments, or (b) explicitly document a "bring-your-own-runtime" workflow for community authors and platform operators that explains how to grant scoped admin access for testing. If the current single gate (`platform-admin` only) is intentional for v1, document why and reference it in Section 23.2's community adoption strategy so authors are not surprised.

---

### DXR-014 Billing/Observability Sections Duplicated [Info]
**Section:** 11.2.1, 11.8

Section 11.8 "Billing Event Stream" contains only: "See Section 11.2.1 for the authoritative billing event stream specification." This is a pure forward reference creating a dead section. For a runtime author reading sequentially, hitting Section 11.8 provides nothing. This was flagged previously as DEV-018 but not fixed.

**Recommendation:** Either delete Section 11.8 and update the table of contents, or replace it with the actual billing event content and mark Section 11.2.1 as "see Section 11.8." Pick one canonical location.

---

## Summary Table

| ID | Title | Severity | Section |
|---|---|---|---|
| DXR-001 | Lifecycle channel transport mechanism not specified | High | 4.7 |
| DXR-002 | Adapter manifest has no formal schema and minimum-tier reading requirements undefined | High | 4.7 |
| DXR-003 | No guidance on Standard-tier MCP client implementation | High | 15.4.3, 4.7 |
| DXR-004 | `type: mcp` runtime requirements undocumented | Medium | 5.1 |
| DXR-005 | Runtime image packaging requirements not documented | Medium | 4.7, 17.4 |
| DXR-006 | Error reporting from runtime binary is incomplete | Medium | 15.4.1, 16.3 |
| DXR-007 | Shutdown vs. terminate signal ambiguity for Full-tier runtimes | Medium | 4.7, 15.4.1 |
| DXR-008 | Credential file schema not published | Medium | 4.7, 4.9 |
| DXR-009 | Echo runtime has no actual runnable artifact | Medium | 15.4.4 |
| DXR-010 | No guidance for runtime authors on testing without admin access | Medium | 17.4, 5.1 |
| DXR-011 | `OutputPart.annotations.protocolHints` field undiscoverable | Low | 15.4.1 |
| DXR-012 | `slotId` multiplexing has no implementation guidance | Low | 15.4.1, 5.2 |
| DXR-013 | Runtime registration is platform-admin only — no community path | Low | 5.1, 10.2 |
| DXR-014 | Billing/observability sections duplicated | Info | 11.2.1, 11.8 |
