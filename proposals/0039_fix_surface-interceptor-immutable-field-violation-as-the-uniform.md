# Proposal: Surface `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` as the uniform top-level §15.1 envelope code carrying `details.violated_fields` across every interceptor-chain surface

- **Status:** Approved (2026-07-16) with Open decision D1 resolved as **Option A** — accept the connector-path deferral (the connector gRPC transport surfaces the distinct §15.1 code and the violated field names in `Reason`; a structured `details.violated_fields` on that transport stays out of scope, guarded by the tier-1 `rejectionFor` code-preservation test). Verified (2026-07-16), converged after 2 adversarial review rounds (0 findings fixed).
- **Date:** 2026-07-16.
- **Scope:** A code-to-spec reconciliation on the external-interceptor MODIFY immutable-field enforcement path. §15.1 (`spec/15_external-api-surface.md:1011`) and §4.8 (`spec/04_system-components.md:1084`) already fix `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` as category `POLICY`, HTTP 400, with `details.interceptor_ref`, `details.phase`, and `details.violated_fields`. The gateway diverges on the HTTP interceptor-chain surfaces that run the phased chain: the session-create/route surface wraps the violation in a generic 403 `INTERCEPTOR_REJECTED` envelope, the child-delegation PreRoute surface drops the code entirely, and no surface carries `details.violated_fields`. The change adds one structured field to the shared `interceptor.Result`, populates it from the immutability check that already computes it, adds a dedicated immutable-violation branch on each HTTP chain surface, updates the two tier-1 tests that pinned the divergent behavior, and un-skips the pre-written tier-4 integration test. It touches `pkg/gateway/policy/interceptor`, `pkg/gateway/sessionserver`, and `pkg/gateway/mcpfabric/mcptools`, plus the tests under those packages and `tests/tier4_integration`. It stages no spec edit: the spec is already faithful. The connector gRPC surface and the §8.7 export-materialization surface are out of scope for the reasons in Non-goals.

This document stages the proposed code and test changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

The §4.8 immutable-field enforcement on an external-interceptor MODIFY is fully and faithfully specified. `spec/15_external-api-surface.md:1011` fixes `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` as category `POLICY`, HTTP 400, with `details.interceptor_ref`, `details.phase`, and `details.violated_fields` included. `spec/04_system-components.md:1084` states the gateway rejects the MODIFY with that code (category `POLICY`, HTTP 400), logs the interceptor reference, phase, and violated field names, and short-circuits the chain. The gateway code diverges from the spec on the surfaces that run the phased chain, and no surface carries the spec-mandated `details.violated_fields`.

### (Root) The shared Result discards the violated-field slice

The shared `interceptor.Result` struct (`pkg/gateway/policy/interceptor/interceptor.go:183-189`) carries `Action`, `Reason`, `Code`, `ModifiedContent`, `RejectedBy`, and `TimeoutMs`, and no structured field for the violated fields. The `ActionModify` branch computes the violated-field slice via `checkModifyImmutability` (`pkg/gateway/policy/interceptor/immutability.go:66`, which returns a `[]string` of dot-separated JSON paths) but folds it into a formatted `Reason` string and discards the slice (`pkg/gateway/policy/interceptor/interceptor.go:512-519`). No surface can emit `details.violated_fields` as a structured result because the structured value is thrown away before any surface sees the `Result`.

### (Route) The session-create/route surface demotes the code into a 403 wrapper

`recordRouteRejection` (`pkg/gateway/sessionserver/route.go:136-141`) wraps every non-timeout PreRoute/PostRoute chain REJECT in a top-level HTTP 403 `INTERCEPTOR_REJECTED` envelope, demoting the specific code into `details.interceptorCode`, with no `violated_fields`. Two tier-1 tests pin this divergent behavior: `route_internal_test.go:124` (`TestRunRouteChainModifyImmutableTenantRejected`) asserts both the 403 status and `details.interceptorCode == CodeInterceptorImmutableFieldViolation`; `route_internal_test.go:143` (`TestRunRouteChainPostRouteModifyResolvedRuntimeRejected`) asserts only `details.interceptorCode`.

### (Deleg) The child-delegation PreRoute surface drops the code

The `mcptools` child-delegation PreRoute reject site (`pkg/gateway/mcpfabric/mcptools/mcptools_register.go:2103-2111`) hardcodes `mcp.NewToolError("INTERCEPTOR_REJECTED", ...)` and never reads `res.Code`, so a delegation-path immutable violation of `tenant_id`/`user_id` loses the specific code entirely. The site's own comment (`mcptools_register.go:2083-2085`) already states this chain rejects such a MODIFY with `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION`.

### (ToolResult) The PreToolResult surface preserves the code but drops the detail

The `mcptools` PreToolResult reject site (`pkg/gateway/mcpfabric/mcptools/mcptools.go:1000-1006`) preserves `res.Code` (falling back to `INTERCEPTOR_REJECTED` when empty) but calls `mcp.NewToolError(code, res.Reason, nil)`, so the tool-error envelope carries no `violated_fields`, no `phase`, and no `interceptor_ref`.

### Where the contract already holds

The classifier already registers `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` as `{CategoryPolicy, false}` (`pkg/gateway/externalapi/errorclassify/errorclassify.go:333`), and `sessionserver.writeError` derives the category through `errorclassify.ClassifyStatus` (`pkg/gateway/sessionserver/sessionserver.go:3107-3108`), so the `(category, retryable)` parity already holds. HTTP status is chosen per-surface at each `writeError` call site. The required fix is the per-surface top-level code, the HTTP 400 status, and `details.violated_fields`, plus a structured field on `Result` to carry the violated slice from the one place that computes it.

A tier-4 integration test is pre-written to the spec envelope and `t.Skip`-ped awaiting exactly this decision: `tests/tier4_integration/external_interceptor_test.go:201` (`TestExternalInterceptorModifyImmutableTenantThroughGateway_spec_4_8`) drives a real external gRPC interceptor returning a PreRoute MODIFY that rewrites `tenant_id` (`acme` to `globex`) and asserts HTTP 400, top-level code `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION`, category `POLICY`, `details.phase == "PreRoute"`, and `details.violated_fields` naming `tenant_id`. Its skip message (`external_interceptor_test.go:203`) states the surface currently returns 403 `INTERCEPTOR_REJECTED` with the code in `details.interceptorCode`, awaiting a human decision on the error-envelope contract.

The spec is right and the code is the defect. Collapsing a distinct `POLICY`/400 code into a generic `INTERCEPTOR_REJECTED`/403 (or dropping it) loses the signal a caller or SIEM needs to distinguish a deployer-supplied interceptor tampering with identity, routing, or connector fields from an ordinary policy REJECT, and drops the `violated_fields` detail the spec mandates.

## 2. Decisions

- **Align the code to the already-faithful spec rather than ratify the wrapped-403 behavior.** §15.1 and §4.8 both fix `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` as `POLICY`/400 with `details.violated_fields`, and `INTERCEPTOR_REJECTED` does not appear anywhere in `spec/` (it is an implementation-only generic reject code). The `spec-driven-development` rule makes the spec the source of truth, so the code is the defect. The pre-written skipped tier-4 test frames the choice as align-versus-ratify and is already written to the spec envelope.
- **Add one structured field, `ViolatedFields []string`, to the shared `interceptor.Result` rather than a new protocol surface.** `checkModifyImmutability` already returns the `[]string`; the `ActionModify` branch discards it. Carrying it on `Result` is the minimal root-cause fix that lets every surface emit `details.violated_fields` from a single source. No new RPC, proto field, or endpoint is introduced.
- **Keep the generic-REJECT envelope (403 `INTERCEPTOR_REJECTED`, 429 `QUOTA_EXCEEDED`) unchanged and add a dedicated immutable-violation branch on each surface, gated on `res.Code == CodeInterceptorImmutableFieldViolation`.** Only the immutable-field code is spec-fixed as `POLICY`/400; a deliberate policy REJECT is out of this proposal's scope. The change is scoped to the code the spec pins.
- **Emit `details.interceptor_ref` (from `res.RejectedBy`), `details.phase`, and `details.violated_fields` on every HTTP surface, matching the §15.1 catalog row.** The route/PostRoute surface already computes `RejectedBy` in the chain, and its timeout branch already emits `interceptor_ref` (`route.go:127-135`), so the immutable-violation branch reuses the same details.
- **Bound the cross-surface claim to the surfaces that can generate the code.** The LLM proxy phases (`PreLLMRequest`/`PostLLMResponse`) declare no immutable fields (`phaseImmutableFields` returns `nil` for them, `immutability.go:45-47`), so the LLM proxy rejection surface (`pkg/gateway/llmproxy/llmproxy/handler.go`, `writeLLMRejection`) cannot generate this code and needs no change. The PostAuth quota chain (`pkg/gateway/sessionserver/quota.go`, `requirePolicyChain`) passes an empty content payload and carries identity in the RPC `Metadata` map, so `checkModifyImmutability` parses a non-object pre-payload and returns `nil` (`immutability.go:72-75`); it therefore cannot emit the immutable code and is out of scope. The surfaces that must change are the ones that run the phased chain over a JSON-object content payload declaring immutable fields: the session-create/route surface, the child-delegation PreRoute surface, and the PreToolResult surface.
- **Classified `fix`.** The change reconciles core-product code to the already-faithful spec, updates two tier-1 tests that pinned the divergent behavior, and un-skips the pre-written tier-4 test. It stages no spec edit.

## 3. How the surfaces fit at the immutable-violation boundary

Every HTTP interceptor-chain surface runs the phase chain over a serialized JSON-object payload that declares immutable fields (`tenant_id`/`user_id` at PostAuth and PreRoute, `resolved_runtime_name`/`credential_pool_id` at PostRoute, `id` at PreToolResult; `phaseImmutableFields`, `immutability.go:25-48`). When an external interceptor returns a MODIFY that alters one of those fields, the chain's `ActionModify` branch calls `checkModifyImmutability`, receives a non-empty violated-field slice, and returns a `Result` with `Action == ActionReject`, `Code == CodeInterceptorImmutableFieldViolation`, and `RejectedBy` set to the offending interceptor (`interceptor.go:505-520`). CODE-ROOT additionally carries the violated slice on `Result.ViolatedFields`.

Each surface's reject handler then branches on `res.Code == CodeInterceptorImmutableFieldViolation`. On the route surface the branch calls `writeError` with HTTP 400, the top-level `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` code, and a details map carrying `interceptor_ref`, `phase`, and `violated_fields`; `writeError` derives category `POLICY` and `retryable: false` through the existing `errorclassify.ClassifyStatus` mapping. On the two `mcptools` surfaces the branch builds the same details map and returns `mcp.NewToolError` with the preserved code, so the tool-error envelope carries the same three details. A deliberate REJECT that is not an immutable-field violation falls through to the existing generic branch (403 `INTERCEPTOR_REJECTED` on the route and delegation surfaces, the preserved-or-fallback code on PreToolResult), unchanged.

The un-skipped tier-4 test exercises the full path end to end: a real external gRPC interceptor rewrites `tenant_id` at PreRoute, and the gateway returns the §15.1 envelope with `violated_fields` naming `tenant_id`.

## 4. Proposed changes

### CODE-ROOT. Carry the violated-field slice on `interceptor.Result`

**Target:** `pkg/gateway/policy/interceptor/interceptor.go` (the `Result` struct `:183-189`; the `ActionModify` immutability branch `:512-519`).

**Anchor and change (struct).** The `Result` struct currently declares:

```go
type Result struct {
	Action          Action
	Reason          string
	Code            string
	ModifiedContent []byte
	RejectedBy      string
	TimeoutMs       int64
```

Add a `ViolatedFields` field carrying the immutable fields a MODIFY altered, populated only on the immutable-violation reject path:

```go
type Result struct {
	Action          Action
	Reason          string
	Code            string
	ModifiedContent []byte
	RejectedBy      string
	TimeoutMs       int64

	// ViolatedFields lists the immutable field paths a MODIFY altered,
	// populated only on the immutable-violation reject path
	// (Code == CodeInterceptorImmutableFieldViolation). Each surface
	// emits it as details.violated_fields in the §15.1 error envelope.
	// spec: §4.8 (immutable field enforcement), §15.1
	// (INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION details.violated_fields).
	ViolatedFields []string
```

**Anchor and change (branch).** The `ActionModify` branch (`interceptor.go:512-519`) currently discards `violations`:

```go
			if violations := checkModifyImmutability(req.Phase, content, res.ModifiedContent); len(violations) > 0 {
				return Result{
					Action:          ActionReject,
					Code:            CodeInterceptorImmutableFieldViolation,
					Reason:          fmt.Sprintf("interceptor %q MODIFY altered immutable %s field(s): %s", ic.Name(), req.Phase, strings.Join(violations, ", ")),
					ModifiedContent: content,
					RejectedBy:      ic.Name(),
				}
			}
```

Carry the slice on the returned `Result`:

```go
			if violations := checkModifyImmutability(req.Phase, content, res.ModifiedContent); len(violations) > 0 {
				return Result{
					Action:          ActionReject,
					Code:            CodeInterceptorImmutableFieldViolation,
					Reason:          fmt.Sprintf("interceptor %q MODIFY altered immutable %s field(s): %s", ic.Name(), req.Phase, strings.Join(violations, ", ")),
					ModifiedContent: content,
					RejectedBy:      ic.Name(),
					ViolatedFields:  violations,
				}
			}
```

**Rationale:** `checkModifyImmutability` is the single place that computes the violated fields, and the branch is the single place that constructs the immutable-violation `Result`. Carrying the slice here lets every downstream surface emit `details.violated_fields` without recomputing it or parsing it back out of the `Reason` string.

### CODE-ROUTE. Return the §15.1 400 envelope on the session-create/route surface

**Target:** `pkg/gateway/sessionserver/route.go` (`recordRouteRejection` `:115-142`, its doc comment `:110-114`); `pkg/gateway/sessionserver/route_internal_test.go` (the two tier-1 tests `:124`, `:143`).

**Anchor and change.** `recordRouteRejection` currently maps every non-timeout REJECT to a 403 `INTERCEPTOR_REJECTED` wrapper (`route.go:136-140`):

```go
	details := map[string]any{"reason": res.Reason, "phase": string(phase)}
	if res.Code != "" {
		details["interceptorCode"] = res.Code
	}
	s.writeError(w, http.StatusForbidden, "INTERCEPTOR_REJECTED", res.Reason, details)
	return false
```

Add a dedicated immutable-violation branch before the generic reject, gated on the spec-fixed code, that returns the §15.1 400 envelope with the mandated details:

```go
	if res.Code == interceptor.CodeInterceptorImmutableFieldViolation {
		s.writeError(w, http.StatusBadRequest, interceptor.CodeInterceptorImmutableFieldViolation, res.Reason,
			map[string]any{
				"interceptor_ref": res.RejectedBy,
				"phase":           string(phase),
				"violated_fields": res.ViolatedFields,
			})
		return false
	}
	details := map[string]any{"reason": res.Reason, "phase": string(phase)}
	if res.Code != "" {
		details["interceptorCode"] = res.Code
	}
	s.writeError(w, http.StatusForbidden, "INTERCEPTOR_REJECTED", res.Reason, details)
	return false
```

Reword the doc comment (`route.go:113-114`) that currently reads "A fail-closed timeout/error (`CodeInterceptorTimeout`) maps to 503; a deliberate REJECT maps to 403 `INTERCEPTOR_REJECTED`." to add the immutable case: an immutable-field violation (`CodeInterceptorImmutableFieldViolation`) maps to 400 `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` carrying `interceptor_ref`, `phase`, and `violated_fields`.

**Tests.** Update `TestRunRouteChainModifyImmutableTenantRejected` (`route_internal_test.go:124`) to assert `rec.Code == http.StatusBadRequest`, the top-level envelope `code == INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION`, `category == "POLICY"`, `details.phase == "PreRoute"`, and `details.violated_fields` naming `tenant_id`, rather than the 403 / `details.interceptorCode` it pins today. Update `TestRunRouteChainPostRouteModifyResolvedRuntimeRejected` (`route_internal_test.go:143`) the same way for `PostRoute` / `resolved_runtime_name`, and add the 400 status assertion it omits today.

**Rationale:** The route surface is the primary session-create/delegation chain surface and the one the skipped tier-4 test drives. It is the surface where a caller or SIEM most needs the distinct code and the violated-field detail.

### CODE-DELEG. Propagate the code and violated fields on the child-delegation PreRoute surface

**Target:** `pkg/gateway/mcpfabric/mcptools/mcptools_register.go` (the child-delegation PreRoute `ActionReject` branch `:2103-2111`); `pkg/gateway/mcpfabric/mcptools/mcptools_register_test.go` (tier 1).

**Anchor and change.** The reject branch currently hardcodes `INTERCEPTOR_REJECTED` and never reads `res.Code` (`mcptools_register.go:2103-2111`):

```go
			if res.Action == interceptor.ActionReject {
				recordChainRejection(ctx, deps, tenant, in.ParentSessionID, interceptor.PhasePreRoute, res)
				// spec: §15.2.1 line 1386 — see PreDelegation site
				// above. INTERCEPTOR_REJECTED preserves REST/MCP
				// (category, retryable) parity for a deliberate
				// PreRoute reject. F-15.2.11.
				return mcp.ToolResult{}, mcp.NewToolError("INTERCEPTOR_REJECTED",
					res.Reason,
					map[string]any{"phase": string(interceptor.PhasePreRoute)})
			}
```

Mirror the PreToolResult pattern: preserve `res.Code`, fall back to `INTERCEPTOR_REJECTED` when empty, and attach `interceptor_ref`, `phase`, and (on the immutable code) `violated_fields`:

```go
			if res.Action == interceptor.ActionReject {
				recordChainRejection(ctx, deps, tenant, in.ParentSessionID, interceptor.PhasePreRoute, res)
				// spec: §15.2.1 line 1386, §4.8, §15.1 — a deliberate
				// PreRoute reject falls back to INTERCEPTOR_REJECTED,
				// preserving REST/MCP (category, retryable) parity; an
				// immutable-field violation carries its own §15.1 code
				// (INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION) plus
				// violated_fields. F-15.2.11.
				code := res.Code
				if code == "" {
					code = "INTERCEPTOR_REJECTED"
				}
				details := map[string]any{
					"phase":           string(interceptor.PhasePreRoute),
					"interceptor_ref": res.RejectedBy,
				}
				if res.Code == interceptor.CodeInterceptorImmutableFieldViolation {
					details["violated_fields"] = res.ViolatedFields
				}
				return mcp.ToolResult{}, mcp.NewToolError(code, res.Reason, details)
			}
```

**Tests.** Add a tier-1 test in `mcptools_register_test.go` that runs the child-delegation PreRoute path with a MODIFY rewriting `tenant_id` and asserts the returned tool-error code is `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` with `details.violated_fields` naming `tenant_id`; and assert that a generic PreRoute REJECT (no immutable violation) still falls back to `INTERCEPTOR_REJECTED` with no `violated_fields`.

**Rationale:** The delegation path is the one surface today that loses the code entirely, so a delegation-path identity-tampering MODIFY is indistinguishable from an ordinary policy REJECT. The classifier already maps the immutable code to `POLICY`/non-retryable, so `(category, retryable)` parity is preserved by the fallback for generic rejects.

### CODE-TOOLRESULT. Attach the mandated details on the PreToolResult surface

**Target:** `pkg/gateway/mcpfabric/mcptools/mcptools.go` (the PreToolResult `ActionReject` branch `:1000-1006`); `pkg/gateway/mcpfabric/mcptools/mcptools_test.go` (tier 1).

**Anchor and change.** The PreToolResult reject branch preserves `res.Code` but passes `nil` details (`mcptools.go:1000-1006`):

```go
		case interceptor.ActionReject:
			recordChainRejection(ctx, deps, tenant, "", interceptor.PhasePreToolResult, res)
			code := res.Code
			if code == "" {
				code = "INTERCEPTOR_REJECTED"
			}
			return mcp.ToolResult{}, mcp.NewToolError(code, res.Reason, nil)
```

Build the details map so the tool-error envelope carries `phase` and `interceptor_ref`, plus `violated_fields` on the immutable code:

```go
		case interceptor.ActionReject:
			recordChainRejection(ctx, deps, tenant, "", interceptor.PhasePreToolResult, res)
			code := res.Code
			if code == "" {
				code = "INTERCEPTOR_REJECTED"
			}
			details := map[string]any{
				"phase":           string(interceptor.PhasePreToolResult),
				"interceptor_ref": res.RejectedBy,
			}
			if res.Code == interceptor.CodeInterceptorImmutableFieldViolation {
				details["violated_fields"] = res.ViolatedFields
			}
			return mcp.ToolResult{}, mcp.NewToolError(code, res.Reason, details)
```

**Tests.** Add a tier-1 test in `mcptools_test.go` that runs the PreToolResult chain with a MODIFY altering the immutable `id` field and asserts the tool-error code is `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` with `details.violated_fields` naming `id`; and assert a generic PreToolResult REJECT still carries no `violated_fields`.

**Rationale:** PreToolResult already preserves the code, so this is the smallest of the three surface changes: it only enriches the details so the immutable violation carries `violated_fields` uniformly with the other surfaces.

### TEST-T4. Un-skip the pre-written tier-4 integration test

**Target:** `tests/tier4_integration/external_interceptor_test.go` (`TestExternalInterceptorModifyImmutableTenantThroughGateway_spec_4_8` `:201-203`).

**Anchor and change.** Remove the `t.Skip(...)` guard at `external_interceptor_test.go:203`:

```go
	gateway.SkipUnlessAvailable(t)
	t.Skip("spec-faithful assertion for an OPEN test-gap: the PreRoute session-create surface rejects the immutable-field MODIFY (the enforcement works across the gRPC boundary) but returns 403 INTERCEPTOR_REJECTED with the code in details.interceptorCode rather than the §15.1 400 INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION with details.violated_fields; awaiting a human decision on the error-envelope contract before this can assert green")
```

becomes:

```go
	gateway.SkipUnlessAvailable(t)
```

The test body already asserts HTTP 400, top-level `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION`, category `POLICY`, `details.phase == "PreRoute"`, and `details.violated_fields` naming `tenant_id` (`external_interceptor_test.go:228-258`), and verifies the gateway forwarded the original pre-MODIFY `tenant_id` over the real gRPC boundary (`:260-274`). CODE-ROOT and CODE-ROUTE make it pass; no body change is required beyond removing the skip.

**Rationale:** The test was written to the spec envelope and skipped pending exactly this decision. Un-skipping it is the end-to-end regression guard against re-divergence.

## 5. Non-goals

- **No spec edit (drops SPEC-A).** An earlier sketch (SPEC-A) proposed a §15.1/§4.8 clarification stating the code is the uniform, unwrapped, caller-facing top-level envelope code. It is dropped because the spec is already complete. §15.1 (`spec/15_external-api-surface.md:960-978`) defines the canonical error envelope whose `code` is "machine-readable error code from the table below," and the catalog row (`spec/15:1011`) fixes `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` as `POLICY`/400 with `details.violated_fields`; §4.8 (`spec/04:1084`) independently restates `POLICY`/400 and mandates logging the violated field names. Together these fully determine the correct top-level code, status, and details, so a clarification would restate a settled contract rather than fill a gap. A prophylactic edit would also introduce the implementation-only name `INTERCEPTOR_REJECTED` (absent from all of `spec/`) and a stale-prone surface enumeration into the normative spec, and its unqualified connector-phase sentence would read against the `CONNECTOR_REQUEST_REJECTED` (403) and `CONNECTOR_RESPONSE_REJECTED` (502) catalog rows (`spec/15:1016-1017`). The un-skipped tier-4 test (TEST-T4) is the regression guard against re-divergence, so the anti-re-divergence goal is met without touching the spec.
- **No connector-surface code change (drops CODE-CONN).** An earlier sketch (CODE-CONN) proposed threading `violated_fields` to the agent pod as a gRPC status detail on the connector request/response reject path. It is dropped. The connector transport is gateway-to-pod gRPC plus pod-to-runtime JSON-RPC, not the HTTP JSON envelope that `details.violated_fields` belongs to; the §15.1 connector rows reference only `details.reason`. `rejectionFor` (`pkg/gateway/connectors/connectorinvoke/interceptor.go:194-204`) already preserves `res.Code` for the immutable violation, the chain already sets a `Reason` that names the joined violated fields (`interceptor.go:513-519`), and `connectorRejectionGRPCCode` already maps the immutable code to `codes.FailedPrecondition` (~HTTP 400) through its default (`pkg/gateway/mcpfabric/delegationtree/leasecontrol/connectortools.go:191-204`). No consumer on the connector transport deserializes gRPC status details, so a structured detail written at the gateway would be read by nobody. The genuine need (surfacing the immutable violation distinctly on the connector path) is already satisfied by the preserved code and the field names in the `Reason` message; the connector path needs at most a tier-1 guard test asserting `rejectionFor` preserves the code and the field names ride in `Reason`. Whether the connector gRPC transport should carry a structured `violated_fields` at all is deferred to the Open decisions section.
- **No §8.7 export-materialization change (drops CODE-EXPORT), filed separately.** An earlier sketch (CODE-EXPORT) proposed carrying `violated_fields` on the export immutability path (`pkg/gateway/policy/interceptor/export.go`). It is dropped from this proposal and filed as its own finding. Tracing the export immutable-violation end to end, `applyExportModify` returns an `*ExportScanError{Code: CodeInterceptorImmutableFieldViolation}` that propagates unwrapped to the `delegate_task` handler, whose export branch maps only two sentinels and otherwise returns the bare error into `toolErrorResult("INTERNAL_ERROR", err)`, which reads a code only from `*ToolError`. Because `*ExportScanError` is not a `*ToolError`, the client today receives `code=INTERNAL_ERROR` with `details=nil` for every export `ExportScanError` code, including the spec-cataloged `EXPORT_FILE_SCAN_REJECTED` (`POLICY`/422, `spec/15:1074`). That is a broader, distinct defect than "carry `violated_fields`": the export path swallows all of its `ExportScanError` codes into `INTERNAL_ERROR`. Adding a field to `ExportScanError` without first mapping `ExportScanError.Code` into a `*ToolError` would produce a write-only field no consumer reads. The separate finding must first add an `errors.As(*ExportScanError)` to `*ToolError` mapping in the `delegate_task` export error path so any code (including `EXPORT_FILE_SCAN_REJECTED` and `EXPORT_FILE_SCAN_SIZE_EXCEEDED`) reaches the client, then attach `violated_fields`. This proposal's scope is the HTTP interceptor-chain surfaces, so the export path is excluded.
- **No change to the generic policy-REJECT envelope.** A deliberate `ActionReject` that is not an immutable-field violation continues to surface as 403 `INTERCEPTOR_REJECTED` on the route and delegation surfaces or 429 `QUOTA_EXCEEDED` on the PostAuth quota chain. `INTERCEPTOR_REJECTED` is an implementation-only code, but reconciling it against the spec is a separate concern outside this proposal.
- **No change to the LLM proxy rejection surface.** `PreLLMRequest`/`PostLLMResponse` declare no immutable fields (`phaseImmutableFields` returns `nil`, `immutability.go:45-47`), so `pkg/gateway/llmproxy/llmproxy/handler.go` `writeLLMRejection` cannot generate `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION`.
- **No change to the PostAuth quota chain.** `pkg/gateway/sessionserver/quota.go` `requirePolicyChain` passes an empty content payload and carries identity in the RPC `Metadata` map, so `checkModifyImmutability` returns `nil` and the surface cannot emit the immutable code; its 429 `QUOTA_EXCEEDED` wrapping of a generic reject is unaffected.
- **No change to the errorclassify classification table.** `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` is already `{CategoryPolicy, false}` (`errorclassify.go:333`); the table maps code to `(category, retryable)` only, and HTTP status is set per-surface at each `writeError` call site.
- **No new RPC, proto message, endpoint, or CRD field.** The fix reuses the existing `Result` type (one new field), the existing `writeError`/`NewToolError` surfaces, and the existing error codes.

## 6. Testing

The change reaches tier 0 and tier 1 for the touched packages, tier 3 for the HTTP error-envelope wire contract, and tier 4 for the multi-service flow through a real external gRPC interceptor, per `.claude/rules/test-coverage.md`. Each test pins one behavior the change introduces and asserts the non-happy path (the spec-named-failure MODIFY, an empty violated-field slice, or the generic-reject fallback the immutable branch must not steal).

- **tier-1 Result carries the violated slice (spec-named-failure path, CODE-ROOT):** In `pkg/gateway/policy/interceptor/interceptor_test.go` (or the immutability test file), assert that a PreRoute MODIFY altering `tenant_id` produces a `Result` with `Action == ActionReject`, `Code == CodeInterceptorImmutableFieldViolation`, and `ViolatedFields == ["tenant_id"]`, and that a clean PreRoute MODIFY that alters only the mutable `requested_runtime` returns `ViolatedFields == nil`. The non-happy path is an immutable MODIFY whose violated slice is dropped, leaving every surface unable to emit `details.violated_fields`. `// spec: 4.8 (immutable field enforcement), 15.1 (INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION details.violated_fields)`.
- **tier-1 route surface returns the §15.1 400 envelope (spec-named-failure path, CODE-ROUTE):** In `pkg/gateway/sessionserver/route_internal_test.go`, the updated `TestRunRouteChainModifyImmutableTenantRejected` asserts a PreRoute MODIFY altering `tenant_id` yields HTTP 400, top-level `code == INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION`, `category == "POLICY"`, `details.phase == "PreRoute"`, and `details.violated_fields` naming `tenant_id`; the updated `TestRunRouteChainPostRouteModifyResolvedRuntimeRejected` asserts the same for `PostRoute` / `resolved_runtime_name`. Add a case asserting a generic (non-immutable) PreRoute REJECT still returns 403 `INTERCEPTOR_REJECTED` with `details.interceptorCode`, so the immutable branch does not steal ordinary rejects. The non-happy path is the 403 wrapper demoting the code into `details.interceptorCode` with no `violated_fields`, and a generic reject wrongly promoted to 400. `// spec: 4.8, 15.1 (INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION POLICY/400 with violated_fields)`.
- **tier-1 child-delegation PreRoute propagates the code (spec-named-failure path, CODE-DELEG):** In `pkg/gateway/mcpfabric/mcptools/mcptools_register_test.go`, run the child-delegation PreRoute path with a MODIFY rewriting `tenant_id` and assert the returned tool-error code is `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` with `details.violated_fields` naming `tenant_id`, and that a generic PreRoute REJECT still falls back to `INTERCEPTOR_REJECTED` with no `violated_fields`. The non-happy path is a delegation-path identity-tampering MODIFY reported as a generic `INTERCEPTOR_REJECTED`, indistinguishable from an ordinary policy REJECT. `// spec: 8.2 (delegation PreRoute chain), 4.8, 15.1`.
- **tier-1 PreToolResult attaches violated_fields (boundary path, CODE-TOOLRESULT):** In `pkg/gateway/mcpfabric/mcptools/mcptools_test.go`, run the PreToolResult chain with a MODIFY altering the immutable `id` field and assert the tool-error code is `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` with `details.violated_fields` naming `id`, and that a generic PreToolResult REJECT carries no `violated_fields`. The non-happy path is an immutable violation whose tool-error envelope omits the mandated detail. `// spec: 4.8 (PreToolResult id immutability), 15.1`.
- **tier-1 connector reject preserves the code (guard path, connector Non-goal):** In `pkg/gateway/connectors/connectorinvoke/interceptor_test.go`, assert `rejectionFor` returns a `*RejectionError` whose `Code == CodeInterceptorImmutableFieldViolation` and whose `Reason` names the violated field for an immutable-violation `Result`, guarding the deferral (the connector path surfaces the distinct code and the field names in the message without a structured `violated_fields`). The non-happy path is a connector immutable violation collapsed to a generic connector reject code. `// spec: 4.8, 15.1 (connector-path code preservation)`.
- **tier-3 immutable-violation error-envelope contract (boundary path, CODE-ROUTE):** In `tests/tier3_contract`, assert the HTTP error envelope the route surface writes for an immutable violation carries exactly `code == "INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION"`, `category == "POLICY"`, `retryable == false`, HTTP status 400, and a `details` object with `interceptor_ref`, `phase`, and `violated_fields` keys. The non-happy path is a drifted envelope (wrong status, missing `violated_fields` key, or a non-POLICY category). `// spec: 15.1 (error response envelope and catalog row)`.
- **tier-4 end-to-end immutable violation through a real interceptor (spec-named-failure path, TEST-T4):** The un-skipped `TestExternalInterceptorModifyImmutableTenantThroughGateway_spec_4_8` in `tests/tier4_integration/external_interceptor_test.go` drives a real external gRPC interceptor returning a PreRoute MODIFY that rewrites `tenant_id` and asserts the §15.1 envelope end to end, plus that the gateway forwarded the original pre-MODIFY `tenant_id` over the wire. The non-happy path is a deployer-supplied interceptor rewriting the authenticated `tenant_id`, surfaced with the wrong code or status across the real gRPC boundary. `// spec: 4.8, 15.1. // diagnosis: a failure means the gateway did not surface the immutable-field violation as the §15.1 400 INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION with violated_fields across the real interceptor boundary, so a caller or SIEM cannot distinguish identity tampering from an ordinary policy reject.`

## 7. Findings closed on application

The change closes the OPEN test-gap recorded by the skipped tier-4 test `tests/tier4_integration/external_interceptor_test.go:201-203`, whose skip message defers to a human decision on the error-envelope contract, by un-skipping it and reconciling the three HTTP interceptor-chain surfaces plus the shared `Result` to the already-faithful §15.1/§4.8 contract. It also reconciles the two tier-1 route tests (`route_internal_test.go:124`, `:143`) that pinned the divergent 403 / `details.interceptorCode` behavior to the spec envelope. The connector-surface deferral and the §8.7 export-materialization defect are filed as separate concerns (see Non-goals and Open decisions).

## 8. Resolved in adversarial review

Subsequent adversarial review rounds populate this section. The drafting pass applied the following convergence revisions before first review:

- **SPEC-A dropped.** The proposed spec clarification rested on a false premise of spec silence. §15.1's envelope definition, the `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` catalog row, and §4.8's enforcement paragraph already fully determine the top-level code, status, and details, so the contract is complete and the code companions plus the un-skipped tier-4 test are the actual fix. Recorded in Non-goals.
- **CODE-CONN dropped.** Threading `violated_fields` to the pod as a gRPC status detail is dead code: no consumer on the connector transport deserializes status details, and `rejectionFor` already preserves the distinct code with the violated field names in the `Reason` message. The connector path needs at most a tier-1 guard test. Recorded in Non-goals, with the residual question in Open decisions.
- **CODE-EXPORT cut and re-filed.** The export immutable-violation path returns an `*ExportScanError` that the `delegate_task` handler maps to `INTERNAL_ERROR` with nil details for every export code, a broader defect than missing `violated_fields`. Adding a field without first mapping `ExportScanError.Code` into a `*ToolError` would produce a field no consumer reads, so the export surfacing defect is filed as its own finding. Recorded in Non-goals.

## 9. Open decisions for review

### D1. Should the connector gRPC transport carry a structured `details.violated_fields`?

This proposal defers the connector request/response reject surface (`PreConnectorRequest`/`PostConnectorResponse`) rather than threading a structured `violated_fields` to the agent pod. The rationale (Non-goals, CODE-CONN drop) is that `details.violated_fields` is a field of the HTTP external-API JSON envelope, the §15.1 connector rows reference only `details.reason`, `rejectionFor` already preserves the immutable code, `connectorRejectionGRPCCode` already maps it to `codes.FailedPrecondition` (~400), and the chain's `Reason` already names the violated fields, so the field names already ride to the runtime inside the message string. No consumer on the connector transport (`pkg/adapter/gatewaycontrol`, `pkg/adapter/mcp`) deserializes gRPC status details today, so a structured detail written at the gateway would be read by nobody.

The decision for the reviewer: accept the deferral (the connector path surfaces the distinct code plus the field names in the message, and structured `violated_fields` on that transport is out of scope), or require a follow-up that establishes a status-detail convention on the connector path and a pod-side consumer for it. This proposal proceeds on the deferral and the tier-1 guard test in Section 6; the follow-up, if wanted, is a separate change because it must add both a wire convention and a consumer that do not exist today.

## 10. Files touched on application

- `pkg/gateway/policy/interceptor/interceptor.go`: CODE-ROOT (add `Result.ViolatedFields`; populate it from `checkModifyImmutability`'s slice in the `ActionModify` immutability branch `:512-519`).
- `pkg/gateway/sessionserver/route.go`: CODE-ROUTE (add the immutable-violation branch in `recordRouteRejection` returning HTTP 400 `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` with `interceptor_ref`, `phase`, and `violated_fields`; reword the doc comment `:113-114`).
- `pkg/gateway/sessionserver/route_internal_test.go`: CODE-ROUTE (update `TestRunRouteChainModifyImmutableTenantRejected` `:124` and `TestRunRouteChainPostRouteModifyResolvedRuntimeRejected` `:143` to the §15.1 400 envelope; add a generic-reject case).
- `pkg/gateway/mcpfabric/mcptools/mcptools_register.go`: CODE-DELEG (preserve `res.Code` and attach `interceptor_ref`/`phase`/`violated_fields` on the child-delegation PreRoute `ActionReject` branch `:2103-2111`).
- `pkg/gateway/mcpfabric/mcptools/mcptools_register_test.go`: CODE-DELEG (tier-1 delegation immutable-violation and generic-reject cases).
- `pkg/gateway/mcpfabric/mcptools/mcptools.go`: CODE-TOOLRESULT (attach `interceptor_ref`/`phase`/`violated_fields` on the PreToolResult `ActionReject` branch `:1000-1006`).
- `pkg/gateway/mcpfabric/mcptools/mcptools_test.go`: CODE-TOOLRESULT (tier-1 PreToolResult immutable-violation and generic-reject cases).
- `pkg/gateway/policy/interceptor/interceptor_test.go` (or `immutability_test.go`): CODE-ROOT (tier-1 `Result.ViolatedFields` population and empty-slice cases).
- `pkg/gateway/connectors/connectorinvoke/interceptor_test.go`: connector guard (tier-1 `rejectionFor` code-preservation case for the deferral).
- `tests/tier3_contract`: new immutable-violation error-envelope contract test.
- `tests/tier4_integration/external_interceptor_test.go`: TEST-T4 (remove the `t.Skip` at `:203`).
