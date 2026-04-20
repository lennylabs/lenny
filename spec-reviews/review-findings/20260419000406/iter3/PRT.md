# Iter3 PRT Review

## Regression-Check Summary (iter1 / iter2 findings)

- **PRT-001 Fixed (iter1 fix verified).** §21.1 item 3 documents `A2AAdapter` overriding `Capabilities()` to return `supportsElicitation: false` unconditionally. Cross-referenced from every discovery path.
- **PRT-002 Fixed (iter1 fix verified).** §21.1 now carries the durable-consumer obligation paragraph for `metadata.schemaVersion`.
- **PRT-003 Fixed (iter1 fix verified).** `INVALID_CALLBACK_URL` present in §15.1 error catalogue (line 827).
- **PRT-004 Implicitly addressed.** The new `AuthorizedRuntime` struct documents that `PublishedMetadata` is pre-filtered by the gateway before being handed to any adapter, making visibility uniform across REST/MCP/A2A by construction.
- **PRT-005 Largely fixed, but incomplete.** Four named types are now defined (see PRT-008 for the residue).
- **PRT-006 Fixed.** SessionEvent Kind Registry, dispatch-filter rule, state_change sub-state coverage clause, and capability-consistency invariant are all present and cross-referenced from §21.1.
- **PRT-007 NOT fixed (regression — iter2 commit message falsely claims it was closed).** Line 1034 still reads "MCP 2025-03-26 (latest stable at time of writing)" with no maintenance anchor, cadence, or currency rule. The iter2 fix commit message asserts PRT-007 was addressed via the Shared Adapter Types + Kind Registry work, but that work concerns PRT-005 and PRT-006 only and leaves the MCP version text untouched. Re-raised as PRT-011 below for tracking.

## New Findings

### PRT-008 `Shared Adapter Types` Fix Introduces Two New Undefined Types [High]
**Files:** `spec/15_external-api-surface.md:193`, `spec/15_external-api-surface.md:325`

The iter2 fix for PRT-005 added `SessionMetadata`, `SessionEvent`, `TerminationReason`, and `AuthorizedRuntime` struct definitions in `#### Shared Adapter Types` (lines 160–327). Those definitions in turn reference two types that the specification **never structurally defines**:

- **`CallerIdentity`** — `SessionMetadata.CallerIdentity` field, line 193. The comment says "Schema defined in [Section 13](13_security-model.md)" but a grep of §13 returns zero struct/type definitions for `CallerIdentity`; only prose references to JWT `sub`, `tenant_id`, `caller_type` claims exist. No field enumeration is offered anywhere.
- **`PublishedMetadataRef`** — `AuthorizedRuntime.PublishedMetadata []PublishedMetadataRef`, line 325. `publishedMetadata` is defined in §5.1 as a runtime-YAML field, but no corresponding Go struct `PublishedMetadataRef` exists in any section. The comment says "each ref is an opaque handle; adapters retrieve the materialized card via the gateway's metadata-fetch API as needed" but does not specify the handle's fields (key, visibility class, content-type hint, length, etc.), so an adapter author implementing `HandleDiscovery` cannot serialize the ref into the protocol's native discovery format.

This is the **same class of gap** PRT-005 flagged: a load-bearing interface references types whose shape implementors cannot determine. Third-party adapters registered via `POST /v1/admin/external-adapters` (line 356) cannot pass the `RegisterAdapterUnderTest` matrix (§15.2.1 line 1087) without compiling against `CallerIdentity` and `PublishedMetadataRef`, yet neither has a published field set.

**Recommendation:** In the `Shared Adapter Types` block, add Go struct definitions for both:
- `CallerIdentity{TenantID, Sub, CallerType, PrincipalAttrs map[string]string}` (or similar, citing §13.1 JWT claim mapping).
- `PublishedMetadataRef{Key, Visibility (public|tenant|internal), ContentType, ETag, URI}` plus the gateway metadata-fetch endpoint's contract. Cross-reference from §5.1 `publishedMetadata` Field.

Without this, PRT-005 is only partially closed — the interface still references undefined types, just two different ones.

---

### PRT-009 `OutboundCapabilitySet.SupportedEventKinds` Type + Comment Contradict the Closed-Enum Claim [Medium]
**Files:** `spec/15_external-api-surface.md:85`, `spec/15_external-api-surface.md:232`, `spec/15_external-api-surface.md:329–331`

The iter2 fix introduced `SessionEventKind` as a closed-enum Go type (line 232) and a "SessionEvent Kind Registry" section (line 329) that asserts the enum is **closed** — "the gateway will never dispatch a kind value not listed below, and third-party adapters MUST NOT rely on receiving unknown kinds."

However, `OutboundCapabilitySet.SupportedEventKinds` — the field adapters **actually populate** to declare which kinds they handle (line 85) — is still typed:

```go
SupportedEventKinds []string
```

and commented:

```
// Well-known kinds: "state_change", "output", "elicitation",
// "tool_use", "error", "terminated".
```

Three problems:
1. **Type mismatch.** If the enum is closed, the field should be `[]SessionEventKind`, not `[]string`. Leaving it as `[]string` invites adapter authors to declare arbitrary strings (e.g., `"progress"`, `"thinking"`) that the gateway will silently never dispatch, undermining the dispatch-filter contract.
2. **"Well-known" language contradicts "closed."** The phrase "well-known kinds" conventionally implies an open vocabulary with a few standardized entries. It conflicts with the registry's closed-set claim and with the new prose "additions require a `SessionEvent` schema version bump."
3. **`A2AAdapter` declares it using string literals** in §21.1 line 23: `SupportedEventKinds: ["state_change", "output", "error", "terminated"]`. If this were `[]SessionEventKind`, the declaration would be type-checked — `SessionEventKind` constants — giving compile-time guarantees that no adapter can drift from the registry.

**Recommendation:** 
- Change the field type to `SupportedEventKinds []SessionEventKind`.
- Rewrite the comment: "SupportedEventKinds lists the SessionEvent kinds the adapter is prepared to push. An empty slice means no events are pushed even if PushNotifications is true. The set of valid kinds is the closed enum defined by SessionEventKind above and enumerated in the SessionEvent Kind Registry below."
- Update the `A2AAdapter` example in §21.1 to use the typed constants: `[]SessionEventKind{SessionEventStateChange, SessionEventOutput, SessionEventError, SessionEventTerminated}`.

---

### PRT-010 `AuthorizedRuntime` Fields Do Not Match the `GET /v1/runtimes` Response Claim [Medium]
**Files:** `spec/15_external-api-surface.md:290–326`, `spec/15_external-api-surface.md:465`

Line 290–291 says: *"`AuthorizedRuntime` is the element type in the slice passed to `HandleDiscovery`. It mirrors the shape returned by `GET /v1/runtimes`."*

But §15.1 line 465 documents the `GET /v1/runtimes` response fields as: *"`agentInterface`, `mcpEndpoint`, `mcpCapabilities`, `adapterCapabilities`, capabilities, and labels. Identity-filtered and policy-scoped."*

The `AuthorizedRuntime` struct (lines 298–326) only contains `Name`, `AgentInterface`, `McpEndpoint`, `AdapterCapabilities`, `PublishedMetadata`. It is **missing**:
- **`mcpCapabilities`** — the MCP tool preview surface for `type: mcp` runtimes (§15 line 370 explicitly says `GET /v1/runtimes` and `list_runtimes` return `mcpCapabilities.tools` preview).
- **`capabilities`** — the runtime-native capabilities block (distinct from `adapterCapabilities`).
- **`labels`** — the key-value label map adapters would need to surface in A2A agent card `labels` or REST `labels` payloads.

Consequence: adapters implementing `HandleDiscovery` cannot produce the responses §15.1 contractually promises. `MCPAdapter.list_runtimes` cannot include the `mcpCapabilities.tools` preview because the field isn't in `AuthorizedRuntime`. `A2AAdapter` cannot render runtime labels in its agent card.

**Recommendation:** Add the three missing fields to `AuthorizedRuntime` with struct-typed placeholders where applicable:
```go
McpCapabilities *McpCapabilitiesSummary  // Populated for type: mcp runtimes; nil otherwise.
Capabilities    RuntimeCapabilities      // Runtime-declared capabilities (separate from adapter).
Labels          map[string]string        // Label set used by environment selectors.
```
Then either define each type inline or cross-reference §5.1. The "mirrors the shape returned by `GET /v1/runtimes`" claim should be verifiable by grep — today it is not.

---

### PRT-011 MCP Target Version Currency Note Still Stale — Iter2 Claimed Fix Did Not Apply [Low]
**Files:** `spec/15_external-api-surface.md:1034`, `spec/15_external-api-surface.md:1711`

This is a re-raise of PRT-007. The iter2 fix commit (`2a46fb6`) lists *"PRT-005/006/007: Added Shared Adapter Types section + SessionEvent Kind Registry"* in its message, but that work addresses PRT-005 and PRT-006 only. The PRT-007 finding was specifically about the MCP version currency note, and the affected text is unchanged:

- Line 1034: `**Target MCP spec version:** MCP 2025-03-26 (latest stable at time of writing). All MCP features used by Lenny are gated on this version or later.`
- Line 1711 (intra-pod MCP): `The adapter's local MCP servers speak **MCP 2025-03-26** (the platform's target MCP spec version; …)`.

The MCP specification has released revision `2025-06-18` (per the iter2 PRT.md dated-reference note), so the spec's "latest stable" parenthetical is literally incorrect as of 2026-04-19. More importantly, there is still no policy for *when* the target version rebases, *who* owns the decision, or *how* the intra-pod MCP version (line 1711) is kept in lockstep with the gateway's target.

**Recommendation:** Replace "latest stable at time of writing" with a concrete currency rule, e.g.: *"Lenny rebases this target version to a newer MCP spec revision in a minor release no sooner than 90 days after the new revision reaches `stable` in the MCP spec repository, and only after the intra-pod MCP server (§15.4.3) has been validated against the new revision. The previous target version enters the 6-month deprecation window ([§15.5](#155-api-versioning-and-stability)) from the rebase release date."* Apply the same rule verbatim to §15.4.3 line 1711 so the two surfaces cannot drift.

---

## Summary

**Iter2-introduced issues (2):**
- **PRT-008 [High]** — Shared Adapter Types definitions reference `CallerIdentity` and `PublishedMetadataRef`, neither structurally defined. Same class of gap PRT-005 was meant to close.
- **PRT-009 [Medium]** — `OutboundCapabilitySet.SupportedEventKinds []string` + "well-known kinds" comment contradicts the closed `SessionEventKind` enum added in the same patch. Type-enforced declarations would make adapter drift impossible at compile time.

**Iter2-missed issues (1):**
- **PRT-010 [Medium]** — `AuthorizedRuntime` omits `mcpCapabilities`, `capabilities`, `labels` despite claiming to mirror the `GET /v1/runtimes` response. Adapters cannot faithfully render discovery.

**Claimed-fixed-but-not-fixed (1):**
- **PRT-011 [Low]** — Re-raise of PRT-007 (MCP target version currency). Iter2 commit message says it was addressed; the spec text is unchanged.

**Status:** PRT-005 and PRT-006 are substantially closed — the adapter-contract foundations are now in place. The residual issues are (a) undefined sub-types introduced by the fix (PRT-008), (b) type-level laxity that partially negates the closed-enum benefit (PRT-009), (c) a mismatched "mirrors REST response" claim (PRT-010), and (d) an unaddressed prior finding (PRT-011). No PRT-001/002/003/004 regressions. PARTIAL: PRT-005 (two types still undefined). SKIPPED: none.
