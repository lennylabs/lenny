### PRT-005 `ExternalProtocolAdapter` Interface References Undefined Types [High]
**Files:** `spec/15_external-api-surface.md` (Section 15, lines 10–39, 83, 1902)

The `ExternalProtocolAdapter` interface — the load-bearing contract that third-party adapters must implement — references four types that are **never structurally defined anywhere in the specification**:

- `SessionMetadata` (line 23) — `OnSessionCreated` argument
- `SessionEvent` (lines 24, 36, 81, 111, 116) — `OnSessionEvent`, `OutboundChannel.Send`, `SupportedEventKinds`
- `TerminationReason` (lines 25, 1902) — `OnSessionTerminated`, Runtime SDK `OnTerminate`
- `AuthorizedRuntime` (line 16) — slice element passed to `HandleDiscovery`

Full-repo search confirms zero struct definitions, zero field enumerations, and no JSON-Schema / protobuf mirrors. The §15.4 machine-readable artifacts cover only the runtime-adapter contract.

Consequences for third-party `ExternalProtocolAdapter` implementors (admin-API-registered tier, §15 line 168): they cannot determine (a) which session lifecycle fields `OnSessionCreated` delivers; (b) the event-kind vocabulary `SessionEvent.Kind` carries (line 83 lists six "well-known" kinds but nothing states the set is closed); (c) what termination reasons must map to each protocol's terminal state; or (d) which per-runtime fields `HandleDiscovery` may surface. `RegisterAdapterUnderTest` (line 893) cannot assert against unspecified types. §21.1's `A2AAdapter` leans on `SessionEvent` repeatedly (`OpenOutboundChannel`, `Send`, `SupportedEventKinds`) with no schema to bind to.

**Recommendation:** In §15, after the `BaseAdapter` paragraph (line 158), add normative Go struct definitions for all four types with field-level commentary matching `AdapterCapabilities`. Minimum shape: `SessionEvent{Kind (closed enum), SeqNum, Payload, Timestamp}`; `TerminationReason` closed enum over `{completed, failed, cancelled, expired, drained}` plus `Detail`; `SessionMetadata{TenantID, SessionID, RuntimeName, DelegationDepth, CallerIdentity, NegotiatedProtocolVersion}`; `AuthorizedRuntime` mirrors the `GET /v1/runtimes` element (name, `agentInterface`, `mcpEndpoint`, `adapterCapabilities`, visibility-filtered `publishedMetadata` refs). Cross-reference from §15.4.1 fidelity matrix, §21.1 A2A outbound push, and §25 agent operability.

---

### PRT-006 `SupportedEventKinds` Vocabulary Is Not Authoritatively Enumerated [Medium]
**Files:** `spec/15_external-api-surface.md` (§15 lines 81–85, line 160), `spec/21_planned-post-v1.md` (§21.1 line 23)

`OutboundCapabilitySet.SupportedEventKinds`'s comment lists six "well-known" kinds: `state_change`, `output`, `elicitation`, `tool_use`, `error`, `terminated`. §21.1 `A2AAdapter` declares four of those six. No normative section (a) enumerates the closed set the gateway will ever pass to `OutboundChannel.Send`, (b) maps each kind to the `OutputPart` / `MessageEnvelope` sub-schema it carries, or (c) states how an adapter MUST behave on receipt of an undeclared kind.

Consequences: (1) the gateway outbound dispatcher (§15 line 160) has no deterministic filter rule; (2) the A2AAdapter's omission of `elicitation`/`tool_use` is consistent with `block_all` but nothing cross-references the correspondence — a future maintainer flipping `elicitationDepthPolicy` without updating `SupportedEventKinds` would silently desynchronize; (3) third-party adapters cannot know whether §7.2 session sub-states (`input_required`, `suspended`, …) will surface as `SessionEvent` kinds.

**Recommendation:** Add a "SessionEvent Kind Registry" subsection immediately after `OutboundCapabilitySet` that (1) enumerates the complete closed set, including any §7.2 sub-states adapters may surface, (2) maps each kind to its `SessionEvent.Payload` sub-schema, and (3) states the normative dispatch-filter rule: adapters whose `SupportedEventKinds` omits a kind MUST NOT receive events of that kind. Cross-reference from §21.1 binding `A2AAdapter`'s four-kind declaration to the registry and to `elicitationDepthPolicy: block_all`.

---

### PRT-007 MCP Target Version Currency Note Is Stale [Low]
**Files:** `spec/15_external-api-surface.md` (§15.2 line 840)

"**Target MCP spec version:** MCP 2025-03-26 (latest stable at time of writing)" has no maintenance anchor. As of 2026-04-19 the MCP specification has released newer revisions (2025-06-18), and the spec offers no policy for when the target is rebased, who owns that decision, or how migration is executed. The rolling two-version policy (§15.2) is a runtime compatibility mechanism; it does not answer *when* the target moves.

**Recommendation:** Replace the parenthetical with a concrete currency rule. Suggested: "Lenny rebases the target version to a newer MCP spec revision in a minor release no sooner than 90 days after the new revision reaches `stable` in the MCP spec repository, and only after the intra-pod MCP server (§15.4.3) has been validated against the new revision. The previous target version enters the 6-month deprecation window from the rebase release date." Binds the intra-pod MCP version (§15.4.3 line 1515) to the same cadence so the two surfaces never drift.

---

## Summary

**Iter1 regressions:** None. PRT-001 (A2AAdapter `Capabilities()` override) and PRT-002 (`schemaVersion` durable-consumer obligation) are both verified fixed in §21.1 with robust cross-references. PRT-003 (`INVALID_CALLBACK_URL`) is present in §15.1 line 633 despite a missing "Status: Fixed" block in the iter1 summary. PRT-004 remains low clarity, not material to protocol correctness.

**New findings (3):**
- **PRT-005 [High]** — four load-bearing types in the `ExternalProtocolAdapter` interface (`SessionMetadata`, `SessionEvent`, `TerminationReason`, `AuthorizedRuntime`) are nowhere defined. Blocking gap for third-party adapter implementors and for `RegisterAdapterUnderTest` contract testing.
- **PRT-006 [Medium]** — `SupportedEventKinds` vocabulary is not authoritatively enumerated; no gateway-dispatch filter rule; A2AAdapter's omissions are not cross-referenced to `elicitationDepthPolicy: block_all`.
- **PRT-007 [Low]** — "Latest stable at time of writing" has no maintenance anchor; MCP 2025-03-26 is ~12 months old as of review date.

**Status:** Core MCP abstraction is sound. The new `ExternalProtocolAdapter` outbound-push contract is well-conceived but structurally incomplete — PRT-005 is the blocking gap. PRT-006 and PRT-007 are additive clarifications for future-proofing.
