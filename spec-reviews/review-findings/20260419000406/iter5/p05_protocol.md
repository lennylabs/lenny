## 5. Protocol Design & Future-Proofing (MCP, A2A, AP, OpenAI)

### PRO-018 publishedMetadata double-fetch for list_runtimes [Low]

**Section:** `spec/15_external-api-surface.md` (list_runtimes tool); `spec/05_runtime-registry-and-pool-model.md` (publishedMetadata field)

The `list_runtimes` MCP tool returns a `PublishedMetadataRef` per runtime but does not inline a compact digest, forcing clients that want to render a runtime catalog to issue N follow-up `get_published_metadata` calls (one per runtime per metadata key). There is no `list_runtime_metadata` bulk tool and no inline carve-out (e.g., a bounded `previewFields` subset). For a tenant with dozens of runtimes advertising `agent-card` and `mcp-capabilities`, first-paint latency scales O(runtimes × keys). This was raised in iter3 (PRT-011) and iter4 (PRT-015) and remains unaddressed.

**Recommendation:** Either (a) add a `list_runtime_metadata` MCP tool that accepts a runtime-id list and key-prefix filter and returns matched entries in one round trip, or (b) permit `list_runtimes` to inline a bounded preview (e.g., `name`, `description`, `version`, `iconUrl`) drawn from the `agent-card` metadata entry with a documented size cap and `preview_truncated` annotation. Either approach should be spelled out explicitly in both `09_mcp-integration.md` and `15_external-api-surface.md`.

---

### PRO-019 MCP target version pinned as "latest stable at time of writing" [Low]

**Section:** `spec/15_external-api-surface.md` line 1284 and line 1862

Two locations still read "Target MCP spec version: MCP 2025-03-26 (latest stable at time of writing)." This phrasing was flagged in iter2, iter3 (PRT-010), and iter4 (PRT-016) and has persisted across four iterations. The clause is self-aging: anyone reading the spec after MCP publishes a newer stable version cannot tell whether Lenny tracks it, and readers have no pointer to the supported-version matrix that would resolve the ambiguity. The issue is documentation fidelity, not protocol behaviour (the `mcp_protocol_version_retired` annotation and version-negotiation flow are in place).

**Recommendation:** Replace the "latest stable at time of writing" clause with a concrete statement of form: "Lenny tracks the MCP specification on a rolling basis. The currently-required version is listed in the Supported MCP Versions table (Section X). Server-side upgrades follow the deprecation window defined in Section Y." Remove the "at time of writing" hedge at both occurrences. If the intent is to pin v1 to a specific MCP version regardless of upstream evolution, state that pin explicitly and note the next planned adoption checkpoint.

---

### PRO-020 OutboundSubscription hardcodes net/http response writer [Low]

**Section:** `spec/15_external-api-surface.md` lines 98-108 (`OutboundSubscription` struct) and lines 13-39 (`ExternalProtocolAdapter.HandleInbound` signature)

`OutboundSubscription.ResponseWriter` is typed `http.ResponseWriter`, and `HandleInbound(ctx, w, r, dispatcher)` takes `http.ResponseWriter` and `*http.Request` directly. Every adapter today is HTTP-bound (MCP Streamable HTTP, A2A JSON over HTTP, AgentProtocol over HTTP), so this works for v1. However, the `ExternalProtocolAdapter` interface is marketed as the pluggability seam for future protocols, and several plausible future protocols are not HTTP-shaped (gRPC streaming, WebSocket-native, MQTT, raw TCP for edge devices). Non-HTTP adapters would either have to fake `http.ResponseWriter`/`*http.Request` or the interface would need a breaking change. Raised in iter4 (PRT-017) at Low; remains unfixed.

**Recommendation:** Introduce a small Lenny-owned transport abstraction (e.g., `type OutboundStream interface { WriteFrame(ctx, []byte) error; Flush() error; Close() error }` and an inbound `type InboundRequest interface { Context() context.Context; Method() string; Header(string) string; Body() io.Reader; ResponseWriter() OutboundStream }`) and have the HTTP adapter layer provide a concrete `httpInboundRequest` that wraps `net/http`. Keep the HTTP-backed implementation the only one shipped for v1; the abstraction prevents a breaking interface change when a non-HTTP adapter is added. Document that v1 only implements HTTP-backed transport.

---

### PRO-021 AdapterCapabilities is a closed struct with no forward-extension story [Info]

**Section:** `spec/15_external-api-surface.md` (`AdapterCapabilities` definition and `OutboundCapabilitySet`)

`AdapterCapabilities` exposes a fixed set of named booleans (elicitation-support, tool-use observability, etc.) and `OutboundCapabilitySet.SupportedEventKinds` is a closed enum list. The dispatch-filter rule uses exact-match set membership. If a post-V1 adapter wants to advertise a capability that Lenny core does not yet know about (e.g., "supports streaming partial-tool-result chunks" for a hypothetical future MCP), there is no carrier for the flag short of a core struct change.

**Recommendation:** Add an opaque `Extensions map[string]json.RawMessage` or `Features []FeatureFlag` slot on `AdapterCapabilities` reserved for adapter-declared capability flags not yet modeled in Lenny core. Document that core code MUST NOT branch on unknown extension keys, and that the dispatch filter continues to operate on the closed set only. This is a purely additive forward-compatibility hedge.

---

### PRO-022 publishedMetadata key namespace lacks a registry or reservation policy [Info]

**Section:** `spec/05_runtime-registry-and-pool-model.md` (publishedMetadata field, lines 270-308); `spec/21_planned-post-v1.md` (A2A agent-card generator)

`publishedMetadata` keys `agent-card` and `mcp-capabilities` are referenced by built-in adapters, and the field is otherwise advertised as an opaque bag. There is no stated convention for (a) which keys are reserved for Lenny core adapters, (b) how third-party adapters should namespace their keys to avoid collision (e.g., `vendor.example.com/thing`), or (c) how the A2A generator chooses its input key when a tenant also writes a user-authored `agent-card` entry. The iter4 spec pins a `generatorVersion` envelope on auto-generated entries, which helps distinguish generator output from user input, but does not prevent key collision with a user's manually-written key.

**Recommendation:** Add a short "publishedMetadata key reservation" subsection listing (1) reserved Lenny-core keys (`agent-card`, `mcp-capabilities`, plus any future), (2) a namespacing convention for third-party adapters (e.g., reverse-DNS prefix), and (3) the conflict resolution rule when a tenant writes a key that collides with a Lenny-auto-generated one (suggest: user-written wins, auto-generator records a `suppressed_by_user_entry` status instead of overwriting).

---

### PRO-023 notifications/lenny/* namespace collision risk with future MCP standard methods [Info]

**Section:** `spec/15_external-api-surface.md` lines 1311-1350 (`MCPAdapter OutboundChannel mapping`), line 1346 (`notifications/lenny/*` namespace declaration)

Lenny defines extension MCP notification methods under `notifications/lenny/*` (e.g., `notifications/lenny/toolCall`, `notifications/lenny/error`). If a future MCP spec revision standardises tool-call or error notifications under a different method name, clients will receive both the standard notification and the Lenny-prefixed one, or adapters will need a translation step. The current spec does not state a policy for retiring `notifications/lenny/foo` once MCP standardises an equivalent.

**Recommendation:** Add a brief policy statement near the namespace declaration: "If a later MCP revision standardises a notification method equivalent to `notifications/lenny/X`, Lenny will dual-publish both methods for one deprecation window (same window as MCP version deprecation, 6 months), then retire the `notifications/lenny/X` form. The `mcp_protocol_version_retired` annotation is reused to surface the retirement to clients that still subscribe to the lenny-prefixed form." This keeps the extension namespace from becoming a permanent parallel vocabulary.

---

### Convergence assessment (Perspective 5)

- Critical: 0
- High: 0
- Medium: 0
- Low: 3 (PRO-018, PRO-019, PRO-020 — all three re-raised iter4 items)
- Info: 3 (PRO-021, PRO-022, PRO-023 — new forward-compat observations)

**Converged for this perspective** (0 C/H/M).
