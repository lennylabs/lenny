### PRT-001 Elicitation Capability Mismatch: A2AAdapter Capabilities Not Reflected in `adapterCapabilities` [High]
**Files:** `spec/15_external-api-surface.md`, `spec/21_planned-post-v1.md`

When an A2AAdapter-initiated session has `elicitationDepthPolicy: block_all` set (Section 21.1, item 1), the A2AAdapter's `adapterCapabilities.supportsElicitation` must be `false` for those sessions. However, the specification documents that `Capabilities()` is called once at adapter registration time (Section 15, BaseAdapter default). This means the gateway-wide capability is static, but A2A elicitation support is dynamic per session based on the policy that gets enforced at session creation time.

The discovery path (Section 15, HandleDiscovery) passes the adapter's static `Capabilities()` result to all callers, so A2A discovery endpoints will advertise `supportsElicitation: true` (the default from BaseAdapter) even though A2A-initiated sessions will have elicitation blocked. Callers following Section 9.2's requirement to "inspect `adapterCapabilities.supportsElicitation` before initiating elicitation-dependent workflows" will be misled.

**Recommendation:** Either (1) document that A2AAdapter overrides `Capabilities()` to return `supportsElicitation: false` unconditionally for v1 (matching the actual behavior of blocked elicitation), or (2) add a per-session capability override mechanism so discovery reflects the actual session-level policy. Option 1 is simpler and sufficient for v1 because Section 21.1, item 3 already states that A2A clients must read the auto-generated agent card's `capabilities.elicitation: false` field; this implies discovery at the agent card level, not the adapter level.

---

### PRT-002 OutputPart `schemaVersion` Round-Trip Loss in A2AAdapter Implementation Risk [High]
**Files:** `spec/15_external-api-surface.md` (Section 15.4.1, rows 1106–1121)

The Translation Fidelity Matrix documents that `schemaVersion` is `[lossy]` in A2AAdapter (mapped to `metadata.schemaVersion` string, not integer). However, Section 21.1 does not explicitly confirm that A2AAdapter will implement this lossy mapping at all. If A2AAdapter is implemented to pass `schemaVersion` through `metadata.schemaVersion` as specified, the following durable-consumer liability exists:

When an A2A-initiated session delegates to a child, and the child's output is persisted in a `TaskRecord` (Section 8.8), the `OutputPart.schemaVersion` will be stored as a string in `metadata.schemaVersion` instead of the integer field. When a durable consumer (e.g., audit/billing pipeline) reads this `TaskRecord` and expects `schemaVersion` to be an integer per the default in Section 15.4.1, the consumer must either (a) handle both integer and string forms, or (b) fail gracefully without silent data loss. The spec does not explicitly document this round-trip asymmetry for nested delegation chains involving A2A.

**Recommendation:** In Section 21.1, explicitly confirm that A2AAdapter writes `schemaVersion` to `metadata.schemaVersion` as a string, and document the durable-consumer obligation: "Durable consumers of `TaskRecord` objects from A2A-initiated delegation chains must be prepared to encounter `metadata.schemaVersion` as a string and must convert it to integer during schema version comparisons. Consumers must not fail if the integer `schemaVersion` field is absent; treat absence as version 1."

---

### PRT-003 Missing SSRF Validation Enforcement Point for A2AAdapter Push Notifications [Medium]
**Files:** `spec/21_planned-post-v1.md` (Section 21.1, item 3)

Section 21.1 states: "The A2A adapter MUST validate the URL at `OpenOutboundChannel` time, not at delivery time, and reject task registration with `400 INVALID_CALLBACK_URL` if validation fails. This requirement must be implemented before the A2A adapter is enabled in any deployment."

However, there is no corresponding error code in Section 15.1's error catalog (Section 15.1) for `INVALID_CALLBACK_URL`. The closest error is `UPSTREAM_ERROR` (502), which is inadequate because it does not distinguish SSRF validation failure from actual upstream unavailability. This creates ambiguity about which HTTP status code the A2A task creation should return.

**Recommendation:** Add `INVALID_CALLBACK_URL` to the error code catalog in Section 15.1 with HTTP status 400 and category PERMANENT. Definition: "The `pushNotification.url` field in the A2A task request failed SSRF validation (private IP, non-HTTPS scheme, DNS pinning rejection, or domain allowlist mismatch). The task was rejected."

---

### PRT-004 `publishedMetadata` Access Control Consistency Across Adapters [Low]
**Files:** `spec/15_external-api-surface.md` (Section 15.1, line 278)

Section 15.1 lists `GET /v1/runtimes/{name}/meta/{key}` as "Get published metadata for a runtime (visibility-controlled)." The phrase "visibility-controlled" is vague — it does not specify whether visibility rules are enforced identically across REST, MCP, and A2A discovery paths. If a runtime's published metadata is marked private, will MCP's `list_runtimes` tool (Section 9.1) or A2A's `/.well-known/agent.json` aggregate endpoint (Section 21.1) filter out that metadata in the discovery response?

This is not a breaking issue because the visibility contract exists. However, for A2A support (post-v1), the specification should clarify that visibility filtering applies uniformly across all three discovery surfaces, or document why it differs.

**Recommendation:** In Section 15 or 21.1, add explicit cross-adapter visibility guarantee: "Published metadata visibility rules (public/private) are enforced identically across REST (`GET /v1/runtimes/{name}/meta`), MCP (`list_runtimes`), and A2A discovery endpoints. A runtime with private metadata will not expose that metadata in any discovery response regardless of adapter."

---

## Summary

**Critical cross-section issues found: 3**
- **PRT-001**: A2AAdapter capability mismatch between static discovery and dynamic session-level policy
- **PRT-002**: Undocumented durable-consumer obligation for `schemaVersion` round-trip loss via A2A metadata
- **PRT-003**: Missing error code in catalog for A2A SSRF validation failure (referenced but not defined)

**Minor clarity issues found: 1**
- **PRT-004**: Visibility control consistency across adapter discovery surfaces (affects post-v1 robustness)

**Status**: All issues verified against specification sections listed. No gaps in the `ExternalProtocolAdapter` abstraction layer were found — the abstraction is sound. Issues are localized to A2A post-v1 planning and adapter capability declaration semantics.
