## 5. Protocol Design & Future-Proofing (MCP, A2A, AP, OpenAI) — iter7

**Scope.** Re-review of the external protocol surface — MCP integration (`spec/09_mcp-integration.md`), external adapter abstraction and protocol-transport concerns (`spec/15_external-api-surface.md` §§15.1–15.5), `publishedMetadata` shape and governance (`spec/05_runtime-registry-and-pool-model.md`), and post-V1 protocol roadmap (`spec/21_planned-post-v1.md`). Focus on MCP-specific assumptions leaking into core, pluggability of `ExternalProtocolAdapter` for A2A / Agent Protocol / OpenAI Completions, `publishedMetadata` flexibility, and MCP spec evolution risk.

**iter6 context.** The iter6 P5 review was deferred because the dispatched sub-agent hit the Anthropic API rate limit before producing findings (see `iter6/p5_protocols.md`). The last complete protocol review is iter5's (P05 / PRO-018..023), which converged at 0 C/H/M with 3 Low + 3 Info carry-forwards. Iter6 spec fixes touched CRD / API-design / admin surface areas but did not modify the MCP target-version declaration, the `ExternalProtocolAdapter` signature, `AdapterCapabilities`, `OutboundSubscription`, `publishedMetadata` governance, or the `notifications/lenny/*` retirement policy. Iter7 therefore proceeds against effectively the same protocol-layer surface as iter5.

**Calibration.** Severities anchored to the iter1–iter5 rubric per `feedback_severity_calibration_iter5.md`. Carry-forwards retain their iter5 severity; no severity inflation or deflation.

---

### Prior-iteration carry-forwards

All six iter5 findings remain unaddressed in the current spec. Grep evidence below each item confirms the absence of the recommended fix.

---

### PRT-018 publishedMetadata double-fetch for list_runtimes [Low — carry-forward from iter5 PRO-018 / iter4 PRT-015 / iter3 PRT-011]

**Section:** `spec/15_external-api-surface.md` (`list_runtimes` MCP tool), `spec/05_runtime-registry-and-pool-model.md` §5.x (publishedMetadata field)

**Status in iter7 spec.** Unchanged. No `list_runtime_metadata` bulk tool; no `previewFields` inline carve-out; no `preview_truncated` annotation.
- Grep `list_runtime_metadata|previewFields|preview_truncated` against `spec/` → **no matches**.

**Problem (unchanged).** A client rendering a runtime catalog must issue one `list_runtimes` call followed by N × K follow-up `get_published_metadata` calls (N runtimes × K advertised keys such as `agent-card` and `mcp-capabilities`). First-paint latency scales O(runtimes × keys) per tenant. This was raised in iter3 (PRT-011), iter4 (PRT-015), and iter5 (PRO-018) and remains open across four iterations. The defect is performance-shaped, not correctness-shaped, which is why Low severity has persisted.

**Recommendation (unchanged).** Either (a) add a `list_runtime_metadata` MCP tool that accepts a runtime-id list and a key-prefix filter and returns matched entries in one round trip, or (b) permit `list_runtimes` to inline a bounded preview (`name`, `description`, `version`, `iconUrl`) drawn from the `agent-card` metadata entry under a documented byte cap with a `preview_truncated` annotation. Document the choice explicitly in both `09_mcp-integration.md` and `15_external-api-surface.md`.

---

### PRT-019 MCP target version still pinned as "latest stable at time of writing" [Low — carry-forward from iter5 PRO-019 / iter4 PRT-016 / iter3 PRT-010 / iter2 PRT-007]

**Section:** `spec/15_external-api-surface.md` line 1296

**Status in iter7 spec.** **Partially fixed.** The second occurrence (previously at line 1862/2065) has been silently cleaned — line 2065 now reads "MCP 2025-03-26 (the platform's target MCP spec version; see Section 15.2 for version negotiation details)", which is the correct forward-compatible phrasing. However, the primary declaration at line 1296 still reads:
> **Target MCP spec version:** MCP 2025-03-26 (latest stable at time of writing). All MCP features used by Lenny are gated on this version or later.

- Grep `latest stable at time of writing` against `spec/` → **one remaining match at `15_external-api-surface.md:1296`**.

**Problem (unchanged).** The "at time of writing" hedge is self-aging. A reader opening the spec after MCP publishes a newer stable version cannot tell whether Lenny tracks it without reading the whole `15.5 API Versioning and Stability` section. Now that line 2065 has been fixed, line 1296 is the last remaining instance — so the fix is a single-line edit but has persisted across five iterations.

**Recommendation.** Replace "(latest stable at time of writing)" on line 1296 with a concrete reference of the form: "MCP 2025-03-26 is the current target version. Lenny tracks the MCP specification on a rolling basis per [Section 15.5 item 2](#155-api-versioning-and-stability); the supported-versions matrix is authoritative. Upgrade cadence follows the 6-month deprecation window defined there." This moves the phrasing from a self-aging hedge to a forward-compatible pointer into the already-present policy section. A single-line edit discharges this finding entirely, since line 2065 is already clean.

---

### PRT-020 OutboundSubscription hardcodes net/http response writer [Low — carry-forward from iter5 PRO-020 / iter4 PRT-017]

**Section:** `spec/15_external-api-surface.md` line 104 (`OutboundSubscription.ResponseWriter`); lines 13–39 (`ExternalProtocolAdapter.HandleInbound` signature)

**Status in iter7 spec.** Unchanged.
- Grep `http\.ResponseWriter` in `15_external-api-surface.md` → **match at line 104** (unchanged since iter4).
- Grep `OutboundStream|InboundRequest interface|transport abstraction` in `spec/` → **no matches**.

**Problem (unchanged).** `OutboundSubscription.ResponseWriter` is typed `http.ResponseWriter`, and `HandleInbound(ctx, w, r, dispatcher)` takes `http.ResponseWriter` and `*http.Request` directly. For v1 every adapter is HTTP-bound (MCP Streamable HTTP, and the planned A2A JSON-over-HTTP, Agent Protocol over HTTP). But the `ExternalProtocolAdapter` interface is marketed as the pluggability seam for future protocols, and plausible future protocols are not HTTP-shaped (gRPC streaming, WebSocket-native, MQTT, raw TCP for edge devices). A non-HTTP adapter today would either have to fake `http.ResponseWriter`/`*http.Request` or the interface would need a breaking change.

**Recommendation (unchanged).** Introduce a small Lenny-owned transport abstraction such as:
```go
type OutboundStream interface {
    WriteFrame(ctx context.Context, frame []byte) error
    Flush() error
    Close() error
}
type InboundRequest interface {
    Context() context.Context
    Method() string
    Header(name string) string
    Body() io.Reader
    ResponseWriter() OutboundStream
}
```
and have the HTTP adapter layer provide a concrete `httpInboundRequest` that wraps `net/http`. Keep the HTTP-backed implementation the only one shipped for v1; the abstraction prevents a breaking interface change when a non-HTTP adapter is added post-V1. Document explicitly in §15.1 that v1 only implements HTTP-backed transport.

---

### PRT-021 AdapterCapabilities is a closed struct with no forward-extension story [Info — carry-forward from iter5 PRO-021]

**Section:** `spec/15_external-api-surface.md` lines 44–72 (`AdapterCapabilities` definition); lines 74–102 (`OutboundCapabilitySet`)

**Status in iter7 spec.** Unchanged.
- Grep `AdapterCapabilities.*Extensions|Extensions map\[string\]|FeatureFlag` against `spec/` → **no matches**.

**Problem (unchanged).** `AdapterCapabilities` exposes a fixed set of named booleans (elicitation-support, tool-use observability, etc.), and `OutboundCapabilitySet.SupportedEventKinds` is a closed enum tied to the `SessionEventKind` enum at line 315. The dispatch filter uses exact-match set membership (§15.3). If a post-V1 adapter wants to advertise a capability that Lenny core does not yet know about (e.g., "supports streaming partial-tool-result chunks" for a hypothetical future MCP), there is no carrier short of a core struct change.

**Recommendation (unchanged).** Add an opaque `Extensions map[string]json.RawMessage` (or `Features []FeatureFlag`) slot on `AdapterCapabilities` reserved for adapter-declared capability flags not yet modeled in Lenny core. Document that core code MUST NOT branch on unknown extension keys, and that the dispatch filter continues to operate on the closed set only. This is a purely additive forward-compatibility hedge — no behavioural change for v1.

---

### PRT-022 publishedMetadata key namespace lacks a registry or reservation policy [Info — carry-forward from iter5 PRO-022]

**Section:** `spec/05_runtime-registry-and-pool-model.md` (publishedMetadata field, lines 270–308); `spec/21_planned-post-v1.md` §21.1 (A2A agent-card generator); `spec/15_external-api-surface.md` (adapter consumers of `agent-card` and `mcp-capabilities`)

**Status in iter7 spec.** Unchanged.
- Grep `publishedMetadata.*key.*reservation|reserved.*publishedMetadata|publishedMetadata.*namespace` against `spec/` → **no matches**.
- iter6's `generatorVersion` envelope fields (lines 284–301) help distinguish generator-authored entries from hand-crafted ones but do **not** resolve namespace collision (a user can still write a hand-crafted `agent-card` and clobber the auto-generated one).

**Problem (unchanged).** `publishedMetadata` keys `agent-card` and `mcp-capabilities` are referenced by built-in adapters, and the field is advertised as an opaque bag. There is no stated convention for (a) which keys are reserved for Lenny core adapters, (b) how third-party adapters should namespace their keys to avoid collision (e.g., `vendor.example.com/thing`), or (c) what happens when a tenant writes a key that collides with a Lenny-auto-generated one.

**Recommendation (unchanged).** Add a short "publishedMetadata key reservation" subsection to `05_runtime-registry-and-pool-model.md` listing:
1. Reserved Lenny-core keys (`agent-card`, `mcp-capabilities`, plus any future core keys) that only gateway-internal generators may write.
2. A namespacing convention for third-party adapters (reverse-DNS prefix, e.g., `com.acme.my-adapter/capability`), with a documented grammar.
3. The conflict-resolution rule: user-written hand-crafted entry wins; the auto-generator records a `suppressed_by_user_entry` status and does not overwrite. Surface this via a new annotation on the runtime's metadata-regeneration endpoint.

---

### PRT-023 notifications/lenny/* namespace collision risk with future MCP standard methods [Info — carry-forward from iter5 PRO-023]

**Section:** `spec/15_external-api-surface.md` §15.2 MCP Adapter OutboundChannel mapping (lines 1311–1350); line 1346 (`notifications/lenny/*` namespace declaration)

**Status in iter7 spec.** Unchanged.
- Grep `notifications/lenny.*retire|dual-publish.*notifications` against `spec/` → **no matches**.

**Problem (unchanged).** Lenny defines extension MCP notification methods under `notifications/lenny/*` (e.g., `notifications/lenny/toolCall`, `notifications/lenny/error`). If a future MCP spec revision standardises tool-call or error notifications under a different method name, clients will receive both the standard notification and the Lenny-prefixed one, or adapters will need a translation step. The current spec does not state a policy for retiring `notifications/lenny/foo` once MCP standardises an equivalent.

**Recommendation (unchanged).** Add a brief policy paragraph near the namespace declaration: "If a later MCP revision standardises a notification method equivalent to `notifications/lenny/X`, Lenny will dual-publish both methods for one deprecation window (the same 6-month window used for MCP version deprecation per §15.5 item 2), then retire the `notifications/lenny/X` form. The `mcp_protocol_version_retired` annotation is reused to surface the retirement to clients that still subscribe to the lenny-prefixed form." This keeps the extension namespace from becoming a permanent parallel vocabulary.

---

### New findings (iter7)

None. The protocol-layer surface is unchanged since iter5 (no iter6 fixes landed against this category; iter6 was a rate-limit defer). iter6 spec fixes touched circuit-breaker scope taxonomies, residency error codes, gateway event replay, admin RBAC parity, and commit-SHA resolution — none of which modify:
- `ExternalProtocolAdapter` / `HandleInbound` / `HandleOutboundSubscription`
- `AdapterCapabilities` / `OutboundCapabilitySet` / `SessionEventKind` closed enums
- `publishedMetadata` shape, visibility, or auto-generator policy
- `notifications/lenny/*` namespace governance
- MCP target version declaration or negotiation flow
- Translation Fidelity Matrix (§15.4.1)
- Version-retirement annotations (`schema_version_ahead`, `mcp_protocol_version_retired`)
- A2A / Agent Protocol / OpenAI Completions post-V1 adapter constraints (§21.1–§21.3)

No regressions from non-protocol iter6 fixes that would open a new protocol-layer concern. The `x-lenny-mcp-tool` admin-MCP-parity CI contract flagged by iter6 p14 (API Design) is adjacent to P5 but remains scoped to API-design / admin-surface governance rather than protocol transport or MCP negotiation; it is not reproduced here to avoid double-booking the finding.

---

### Convergence assessment (Perspective 5 — iter7)

- Critical: 0
- High: 0
- Medium: 0
- Low: 3 (PRT-018, PRT-019, PRT-020 — all three re-raised from iter5 and earlier)
- Info: 3 (PRT-021, PRT-022, PRT-023 — all three re-raised from iter5)
- New: 0

**Converged for this perspective** (0 C/H/M).

**Persistence note.** PRT-019 (the "latest stable at time of writing" editorial hedge) has now persisted across five iterations (iter2 PRT-007 → iter3 PRT-010 → iter4 PRT-016 → iter5 PRO-019 → iter7 PRT-019; not reviewed in iter6 due to rate-limit defer). A single-line edit discharges it, since one of the two original occurrences was silently fixed between iter5 and iter7. PRT-018 (list_runtimes double-fetch) has persisted across four iterations. If the team intends to keep these open indefinitely as accepted-Low debt, consider adding a **DECISION-LOG** note in the spec acknowledging the open Low/Info carry-forwards rather than letting them recur every review cycle; this would be consistent with how other accepted-debt items are treated (e.g., the explicit "documented and accepted" annotations elsewhere in §15.5). Without such a note, these items will continue to surface in every future P5 pass.
