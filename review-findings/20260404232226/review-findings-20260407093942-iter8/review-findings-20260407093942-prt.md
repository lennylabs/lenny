# Technical Design Review Findings — 2026-04-07 (Iteration 8, Perspective 5: Protocol Design & Future-Proofing)

**Document reviewed:** `docs/technical-design.md`
**Review perspective:** Protocol Design & Future-Proofing
**Iteration:** 8
**Category prefix:** PRT (starting at 028)
**Total findings:** 3

Prior PRT findings reviewed: PRT-001 through PRT-027 (PRT-022/023 skipped per instruction). All prior findings confirmed resolved. No regressions observed.

---

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 0     |
| Medium   | 2     |
| Low      | 1     |
| **Total**| **3** |

---

## Findings

### PRT-028 `ExternalAdapterRegistry` Routing Algorithm Unspecified — Prefix Overlap Between REST, OpenAI, and Open Responses Adapters [Medium]

**Location:** §15 (ExternalAdapterRegistry), `AdapterCapabilities.PathPrefix` Go comment (line 5918), built-in adapter table (lines 6043–6051)

**Problem:**

The `AdapterCapabilities.PathPrefix` comment states: "The gateway routes inbound requests to this adapter when the request path **has this prefix**." The "uniqueness" constraint says "Must be unique across all registered adapters."

The built-in adapter inventory registers three adapters whose path prefixes are hierarchically nested:

| Adapter | PathPrefix |
|---|---|
| `OpenAICompletionsAdapter` | `/v1/chat/completions` |
| `OpenResponsesAdapter` | `/v1/responses` |
| REST API | `/v1` (implied — spec treats REST as an adapter surface throughout §15.4.1 fidelity matrix, §15.2.1 contract tests, and §15.5) |

`/v1/chat/completions` and `/v1/responses` are sub-prefixes of `/v1`. If the gateway dispatches using `strings.HasPrefix(requestPath, adapter.PathPrefix)`, requests to `/v1/chat/completions/...` would match both `/v1` and `/v1/chat/completions`. The "must be unique" constraint cannot prevent this because the prefixes are distinct strings — they are not equal, yet they overlap semantically.

The spec never states the routing algorithm: whether it is exact-match, longest-prefix-wins, or registration-order. Without this specification:
- Implementers must guess the algorithm.
- Third-party adapter authors cannot safely choose a path prefix that avoids collision with built-in adapters.
- The contract test suite (§15.2.1) cannot verify routing correctness because the routing rule itself is unspecified.

A secondary issue: the `RESTAdapter` appears throughout the spec as a first-class adapter concept (fidelity matrix column in §15.4.1, "REST adapter" references in §15.4.1 and §15.6) but is **absent from the built-in adapter inventory table** at §15. It is unclear whether the REST API is implemented as an `ExternalProtocolAdapter` or as a separately-registered handler that bypasses the `ExternalAdapterRegistry`. This ambiguity compounds the routing question.

**Fix:**

1. Specify the routing algorithm explicitly in the `AdapterCapabilities.PathPrefix` documentation and in §15. The correct algorithm for this design is **longest-prefix-wins** (most-specific match takes precedence). State this normatively.
2. Either add `RESTAdapter` to the built-in adapter inventory table with its path prefix (e.g., `/v1` excluding sub-paths claimed by other adapters), or explicitly document that the REST API is a separate handler that is not registered with `ExternalAdapterRegistry` and therefore not subject to `PathPrefix` routing.

---

### PRT-029 `_lennyNonce` Placement in MCP `initialize` Extends MCP Spec Without Acknowledging the Deviation [Medium]

**Location:** §15.4.3 Standard-Tier MCP Integration — Authentication (line 7179–7193)

**Problem:**

The spec requires Standard- and Full-tier runtimes to present a nonce during the MCP `initialize` handshake by setting `_lennyNonce` in `params.clientInfo.extensions`:

```json
{
  "params": {
    "clientInfo": {
      "name": "my-agent",
      "version": "1.0.0",
      "extensions": {
        "_lennyNonce": "<nonce_hex>"
      }
    }
  }
}
```

The MCP 2025-03-26 spec defines `clientInfo` as `{ name: string, version: string }`. There is no `extensions` field in the MCP spec's `clientInfo` schema. The spec neither acknowledges this deviation nor characterizes it as a Lenny extension to MCP.

Consequences:
1. **MCP client libraries may reject or strip unknown fields.** Libraries that perform strict schema validation on the `initialize` request object will either reject the request with a schema error or silently discard `extensions`. The spec acknowledges this risk only partially ("MCP client libraries that do not support `clientInfo.extensions`"), but does not acknowledge that `extensions` is not in the MCP spec — it reads as if `clientInfo.extensions` is a standard MCP field that some libraries have not yet implemented.
2. **The alternative fallback (`_lennyNonce` as a top-level field in `params`) is also non-standard.** The MCP `initialize` `params` object has a defined schema; `_lennyNonce` as a top-level `params` field is equally non-standard and equally subject to stripping by strict parsers.
3. **Runtime authors may rely on their MCP library's `initialize` abstraction and never realize they need to inject a custom field** that the library does not expose as a configuration option.

**Fix:**

1. Explicitly acknowledge that `clientInfo.extensions` is a Lenny-defined extension to the MCP `initialize` request schema, not a standard MCP field.
2. Warn runtime authors that MCP libraries with strict schema validation will strip unknown fields; advise them to use a library that allows passing arbitrary metadata in `clientInfo`, or to use the raw JSON serialization path.
3. Consider an alternative mechanism that does not require modifying the MCP `initialize` message — for example, a separate pre-authentication message on the Unix socket before the MCP `initialize`, or using a query parameter on the abstract socket connection. The nonce must survive in the presence of strict MCP libraries.

---

### PRT-030 Wrong Tool Name in Minimum-Tier Limitations Box — `lenny/discover_sessions` Should Be `lenny/discover_agents` [Low]

**Location:** §15.4.3 Runtime Integration Tiers — Minimum-tier limitations (line 7229)

**Problem:**

The Minimum-tier limitations box lists `lenny/discover_sessions` as an unavailable platform tool:

> **Platform MCP tools** (including `lenny/output`, `lenny/request_input`, `lenny/discover_sessions`): All platform-side tools are inaccessible.

The platform MCP server tool is named `lenny/discover_agents` throughout the rest of the spec:
- §4.7 adapter manifest tool list (line 617): `lenny/discover_agents`
- §8.2 delegation design (line 2927): `lenny/discover_agents`
- §9.1 platform MCP server tools table (line 3561): `lenny/discover_agents`
- §9.1 delegation tool reference (line 3094): `lenny/discover_agents`
- §18 roadmap (line 8398): `lenny/discover_agents`

`lenny/discover_sessions` does not exist in the spec. The Minimum-tier limitations box is the sole occurrence of this name and is a copy-paste or naming error.

**Impact:** Runtime authors reading the Minimum-tier limitations section will not recognize `lenny/discover_sessions` as matching any documented tool, introducing confusion about what is restricted.

**Fix:** Replace `lenny/discover_sessions` with `lenny/discover_agents` at line 7229 in the Minimum-tier limitations box.
