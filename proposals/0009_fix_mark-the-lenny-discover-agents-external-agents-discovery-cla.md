# Proposal: Mark the `lenny/discover_agents` external-agents discovery clause (§8.3) as post-v1 (A2A)

- **Status:** Applied to spec (2026-06-19). Converged after 2 adversarial review rounds (0 findings fixed); signed off by the human approver. Open decision (marker register) resolved in favor of the staged softer "for future A2A support" clause, for intra-section consistency with §8 line 307.
- **Date:** 2026-06-19.
- **Scope:** Reconciles the §8.3 "Discovery scoping" sentence to the v1 agent-only discovery contract. `spec/08_recursive-delegation.md:244` states that `lenny/discover_agents` "Returns `type: agent` runtimes and external agents only — `type: mcp` runtimes do not appear." The "and external agents" clause is unmarked, so the sentence reads as a binding v1 discovery-result contract. The v1 implementation returns only `type: agent` (no external-agent producer exists), and is correct to do so: external-agent delegation needs the A2A connector transports that the spec scopes to post-v1 everywhere else. The fix marks the external-agents clause post-v1 (A2A) on that one line, bringing the spec into line with the v1-correct implementation and with the docs that already present discovery as agent-only. The change touches no v1 behavior, schema, code, or proto.

This document stages the proposed spec change. It does not modify any spec, code, or doc file. Apply the change in the "Proposed changes" section after sign-off.

## 1. Problem

`spec/08_recursive-delegation.md:244` defines discovery scoping for `lenny/discover_agents`:

```
**Discovery scoping:** `lenny/discover_agents` returns only targets authorized by the calling session's effective delegation policy. Returns `type: agent` runtimes and external agents only — `type: mcp` runtimes do not appear.
```

The "and external agents" clause is unmarked, so the second sentence reads as a present-tense, binding v1 discovery-result contract: a caller of `lenny/discover_agents` should receive external agents alongside `type: agent` runtimes. The v1 implementation does not return external agents, and the surrounding spec scopes external-agent delegation to post-v1, so the sentence asserts a contract the v1 system cannot satisfy and is correct not to.

**The v1 handler returns only `type: agent`.** The `lenny/discover_agents` handler lists runtimes with a fixed agent-only filter at `pkg/gateway/mcptools/mcptools.go:2300`: `deps.Runtimes.List(ctx, runtimestore.ListFilter{Type: runtimestore.TypeAgent})`. The result entry struct carries no type or external discriminator at all: `discoveredAgent` (`pkg/gateway/mcptools/mcptools.go:3521-3526`) has fields `Name`, `IntegrationLevel`, and `Description`, with no field that could mark an entry as external. The handler's own comment states the §8.5 rule that "discovery returns `type: agent` runtimes only" and that `type: mcp` runtimes are never delegation targets.

**The runtime type enum is closed to `agent` and `mcp`.** `RuntimeType` admits exactly two values, `TypeAgent = "agent"` and `TypeMCP = "mcp"` (`pkg/gateway/runtimestore/runtimestore.go:1176-1182`); `AllRuntimeTypes()` returns the closed pair. There is no `external` runtime type for `discover_agents` to surface, and the CRD enum mirrors the same closed set (`charts/lenny/crds/lenny.dev_runtimes.yaml`).

**External agents are not delegation targets in v1.** Connectors are the registration surface for external endpoints, and the connector store rejects any non-MCP transport in v1: `connectorstore.Validate` allows only `transport: streamable_http` (`pkg/gateway/connectorstore/connectorstore.go:203-205`). The A2A and Agent Protocol transports that would carry external-agent delegation are post-v1 (`spec/09_mcp-integration.md:138`, the §9.3 transport-extensibility note). `lenny/delegate_task` rejects `type: mcp` targets with `target_not_an_agent` (`spec/08_recursive-delegation.md:50`), so an MCP connector cannot stand in as a delegation target either.

**The rest of the spec already scopes external-agent delegation to post-v1.** Among the present-tense `discover_agents` assertions that external agents are returned, line 244 is the one that lacks a post-v1 marker:

- §8 line 307: the `allowedExternalEndpoints` lease slot "exists from v1 for **future A2A support** — controls which external agent endpoints can be delegated to" (`spec/08_recursive-delegation.md:307`).
- §9.3 transport-extensibility note: "Post-v1, the `ConnectorDefinition` schema will add support for A2A ... and Agent Protocol ... transports, enabling delegation to external agents over their native protocols." (`spec/09_mcp-integration.md:138`).
- §15.1: the aggregated and per-runtime A2A agent-card discovery endpoints are tagged **Post-V1 (A2A)** (`spec/15_external-api-surface.md:700-701`).
- §21.1: "Outbound: external A2A agents registered as connectors, callable via `lenny/delegate_task`." is documented under Planned / Post-V1 (`spec/21_planned-post-v1.md:5`).

**The mismatch surfaces as a recurring finding.** The discrepancy between the line-244 contract and the agent-only implementation recurs as BUILD-GAPS finding F-8.5.20 (Medium). Its latest resolution (`BUILD-GAPS.md:9598`) defers the finding under Rule P as a required spec-wording change: the spec leaves the external-agent surface genuinely undefined (no external-agent registration schema, no `type: external` `discover_agents` entry schema, and no mapping from `allowedExternalEndpoints` to enumerable, named, discoverable agents), so building the producer would mean inventing those surfaces. The finding is a discovery-wording gap rather than an authorization hole, because `runtimeAuthorizedForCaller` already rejects unknown delegation targets.

This proposal does not introduce new behavior. The v1 agent-only behavior is correct by construction; the spec sentence on line 244 is the defect. The fix is wording-only: mark the external-agents clause post-v1 (A2A), so the spec text matches the agent-only implementation and the post-v1 framing the rest of the spec already uses.

## 2. Decisions

- **Fix the spec rather than the code.** The v1 agent-only behavior is correct by construction: the runtime type enum is closed to `agent` and `mcp` (`pkg/gateway/runtimestore/runtimestore.go:1176-1182`), the discovery handler filters to `type: agent` (`pkg/gateway/mcptools/mcptools.go:2300`), and connectors are MCP-only in v1 (`pkg/gateway/connectorstore/connectorstore.go:203-205`). The change is confined to `spec/08_recursive-delegation.md:244`, per the F-8.5.20 Rule-P resolution at `BUILD-GAPS.md:9598`.
- **Keep the change to one line.** Lines 23 and 529 are not touched. Line 23 ("Target id is **opaque**") and line 529 frame `lenny/delegate_task` target ids, which are v1-stable and are not discovery-result contracts. Those two references to external agents are unmarked because they describe opaque delegation targets rather than what discovery returns. The justification here is narrowed to the discovery-result contract at line 244, the present-tense `discover_agents` assertion that the agent-only implementation cannot satisfy.
- **Reuse the existing post-v1 marker conventions.** Two conventions already exist in the spec: the explicit **Post-V1 (A2A)** tag at §15.1 (`spec/15_external-api-surface.md:700-701`) and the softer "for future A2A support" phrasing at §8 line 307. The edit reuses one of them and cross-references §21.1, which owns external-agent delegation, and the §9.3 transport note. No new framing is introduced.
- **No schema, code, or proto change.** `lenny/discover_agents` already returns only `type: agent`, and the `discoveredAgent` struct has no type field (`pkg/gateway/mcptools/mcptools.go:3521-3526`), so the edit documents existing v1 behavior. The external-agent producer lands post-v1 with the A2A registry under a later proposal.
- **No reader-facing docs change.** The docs already present discovery as agent-only (`docs/runtime-author-guide/platform-tools.md:184`, `docs/runtime-author-guide/integration-levels.md:245`), so this edit brings the spec into line with docs that are already correct.

## 3. Proposed changes

### 3.1 Spec change: `spec/08_recursive-delegation.md` §8.3 "Discovery scoping" (line 244)

Anchor on the "Discovery scoping" paragraph in §8.3 (Delegation Policy and Lease). The current text is:

```
**Discovery scoping:** `lenny/discover_agents` returns only targets authorized by the calling session's effective delegation policy. Returns `type: agent` runtimes and external agents only — `type: mcp` runtimes do not appear.
```

Replace it with the following. The v1 result set is stated first (`type: agent` only; `type: mcp` runtimes excluded because they are MCP tool sources rejected by `lenny/delegate_task` with `target_not_an_agent`), then a sentence scopes the external-agents addition to post-v1, reusing the §8 line 307 "future A2A support" marker and cross-referencing §21.1 and the §9.3 transport-extensibility note:

```
**Discovery scoping:** `lenny/discover_agents` returns only targets authorized by the calling session's effective delegation policy. In v1 it returns `type: agent` runtimes only; `type: mcp` runtimes do not appear, because they are MCP tool sources that `lenny/delegate_task` rejects with `target_not_an_agent`. The result entries carry no type discriminator (every entry is an agent runtime). For future A2A support, once external agents are registered as A2A connectors and become delegation targets (see [Section 21.1](21_planned-post-v1.md#21-planned--post-v1) and the transport-extensibility note in [Section 9.3](09_mcp-integration.md#93-connector-definition-and-oauthoidc)), they will also appear in discovery results.
```

Notes for the applier:

- Do not alter lines 23, 50, 307, or 529. Line 50 (`lenny/delegate_task` rejects `type: mcp` targets) is referenced by the new wording but unchanged.
- The §9.3 anchor slug is `#93-connector-definition-and-oauthoidc`, matching the heading `### 9.3 Connector Definition and OAuth/OIDC` in `spec/09_mcp-integration.md`. The §21.1 anchor slug is `#21-planned--post-v1`, matching the heading `## 21. Planned / Post-V1` in `spec/21_planned-post-v1.md`. Confirm both slugs against the current headings before applying, because heading text can shift.
- If the reviewer prefers the explicit register over the softer one (see Open decisions), replace "For future A2A support, once external agents are registered" with "**Post-V1 (A2A):** once external agents are registered", matching the §15.1 tag at `spec/15_external-api-surface.md:700-701`.

## 4. Non-goals

- **No code change.** `lenny/discover_agents` already returns only `type: agent` (`pkg/gateway/mcptools/mcptools.go:2300`) and the `discoveredAgent` struct has no type or external field (`pkg/gateway/mcptools/mcptools.go:3521-3526`). The edit documents existing behavior.
- **No schema or CRD change.** The runtime type enum stays closed to `agent` and `mcp` (`pkg/gateway/runtimestore/runtimestore.go:1176-1182`; CRD enum `charts/lenny/crds/lenny.dev_runtimes.yaml`). No `type: external` entry, external-agent registration schema, or `allowedExternalEndpoints`-to-registry mapping is added; those are the post-v1 A2A producer surface (F-8.5.20) for a later proposal.
- **No edit to §8 lines 23 or 529.** Both frame `lenny/delegate_task` target ids as opaque, which is v1-stable and is not a discovery-result contract.
- **No reader-facing docs change.** The docs already present discovery as agent-only (`docs/runtime-author-guide/platform-tools.md:184`, `docs/runtime-author-guide/integration-levels.md:245`).
- **No new tier-2-or-higher tests.** Behavior is unchanged; the existing agent-only discovery and `discoveredAgent`-schema tests still pin the v1 contract. A tier-11 doc-consistency check at most confirms that the spec and the docs agree.

## 5. Testing

- **Tier 0 (static):** confirm the edited spec renders and the two added intra-spec anchors (`#93-connector-definition-and-oauthoidc`, `#21-planned--post-v1`) resolve to live headings. The spec lint and link-check stage flag a broken anchor.
- **Tier 1 (unit), already covered:** the existing `lenny/discover_agents` unit tests assert the handler lists with `ListFilter{Type: TypeAgent}` and that result entries carry no type discriminator. These continue to pin the v1 agent-only contract that the edited line 244 now describes. No new unit test is required, because behavior is unchanged.
- **Tier 11 (docs):** confirm the edited §8.3 sentence and the docs discovery sections (`docs/runtime-author-guide/platform-tools.md:184`, `docs/runtime-author-guide/integration-levels.md:245`) agree that v1 discovery returns agent runtimes only. The docs already state this, so the check confirms convergence rather than requiring a docs edit.
- **No tier-2-or-higher behavioral test is added.** The change is wording-only with no behavior, schema, or wire-contract change, so no envtest, contract, integration, e2e, chaos, or security tier is reached.

## 6. Findings closed on application

- **F-8.5.20** (Medium, deferred under Rule P at `BUILD-GAPS.md:9598`): the §8.3 line-244 discovery contract asserts external agents are returned, which the v1 agent-only implementation cannot satisfy. Marking the external-agents clause post-v1 (A2A) removes the unsatisfiable v1 contract and reconciles the spec with the agent-only implementation, the closed runtime type enum, the type-less `discoveredAgent` struct, and the spec-wide A2A post-v1 framing (§8 line 307, §9.3, §15.1, §21.1). The external-agent producer remains a post-v1 surface tracked for a later proposal.

## 7. Resolved in adversarial review

Adversarial review rounds populate this section. None recorded yet.

## 8. Open decisions for review

- **Marker register — RESOLVED (2026-06-19, at sign-off).** The choice was between an explicit **Post-V1 (A2A)** tag (matching §15.1 at `spec/15_external-api-surface.md:700-701`) and the softer "for future A2A support" clause (matching §8 line 307 at `spec/08_recursive-delegation.md:307`). Resolved in favor of the staged softer clause for intra-section consistency: the edit lands in §8, and §8 line 307 already uses "for future A2A support". Apply §3.1 as staged; do not apply the §3.1 explicit-tag substitution.

## 9. Files touched on application

- `spec/08_recursive-delegation.md`: §8.3 "Discovery scoping" sentence (line 244) reworded to state the v1 agent-only result set and mark the external-agents addition post-v1 (A2A).
- No code, schema, proto, chart, or docs file is touched. The existing tier-1 discovery tests and the tier-11 doc-consistency check verify the change.
