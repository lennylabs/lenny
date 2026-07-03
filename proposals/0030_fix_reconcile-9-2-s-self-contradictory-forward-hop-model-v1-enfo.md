# Proposal: Reconcile §9.2's self-contradictory forward-hop model: v1 enforces the gateway-origin binding structurally (server-internal chain resolution, no intermediate re-emission), and the content-integrity digest check is the forward-compatible enforcement point that activates when a per-hop re-emission wire mechanism exists (closes F-9.2.1)

- **Status:** Verified (2026-07-03). Converged after 5 adversarial review rounds (3 findings fixed); awaiting sign-off.
- **Date:** 2026-07-03.
- **Scope:** Reconciles §9.2's internally contradictory account of the elicitation forward-hop model. The same paragraph (`spec/09_mcp-integration.md:56`) asserts both that intermediate pods "forward elicitations upstream by `elicitation_id` only" and that "an intermediate pod re-emits the upstream `elicitation/create` frame carrying the original `{message, schema}` payload." The finding is F-9.2.1 (`BUILD-GAPS.md:11489`, High, OPEN, DEFERRED / `NEEDS-OPERATOR`, "Tamper detector is structurally untriggerable; per-hop content verification is a tautology"). The v1 implementation builds the id-only reading: the gateway resolves the elicitation chain server-internally and no intermediate pod re-emits the `{message, schema}` payload, so the gateway-origin binding holds by construction and the content-integrity digest check is dormant by design. The resolution rewrites the §9.2 line-56 paragraph to the implemented model and reframes the digest check as the forward-compatible enforcement point that activates when a per-hop re-emission wire mechanism exists (C1); aligns the code comments, one runtime header, a tier-9 test scaffold, and the kind workload manifest deploying the runtime, all of which label the seam a deferred / unbuilt surface (C3, C4); strengthens the structural-behavior internal test's diagnosis and adds a tier-11 spec-consistency guard (C5); and closes F-9.2.1 as reconciled, contingent on the reviewer accepting the reconciliation direction and on a post-v1 bead being filed for the active per-hop producer (C6). It introduces no behavioral code change and no new RPC, frame, field, or endpoint. It touches only `spec/09_mcp-integration.md`, `pkg/elicitation/chain.go`, `pkg/elicitation/forwarded_content_test.go`, `pkg/gateway/mcpfabric/mcptools/elicitation.go`, `pkg/gateway/mcpfabric/mcptools/elicitation_tamper_internal_test.go`, `cmd/runtimes/elicitation-echo/main.go`, `tests/tier9_security/scaffolds_test.go`, `tests/testinfra/kind/agent-workload.yaml`, `tests/tier11_docs/`, and `BUILD-GAPS.md`.

This document stages the proposed spec and code changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

F-9.2.1 (`BUILD-GAPS.md:11489`, "Tamper detector is structurally untriggerable; per-hop content verification is a tautology", High, OPEN) has one residual after the tautology half was fixed in commit `684ab36e`. The old self-comparison `VerifyContent(originalDigest, in.OriginalContent)` is gone. The finding stays open because the §9.2 content-tamper detector is structurally dormant in production, and the underlying cause is a spec-versus-code contradiction inside §9.2 that the build loop could not resolve without a spec change.

### 1.1 The §9.2 line-56 self-contradiction

`spec/09_mcp-integration.md:56` (the "Elicitation content integrity (gateway-origin binding)" paragraph) asserts two incompatible things about the forward-hop model:

- "Intermediate pods forward elicitations upstream by `elicitation_id` only; they MAY observe the original content ... but MUST NOT modify the rendered text." A pod that forwards by `elicitation_id` only re-emits no content.
- "The forward-hop wire mechanism is the native MCP `elicitation/create` frame ... an intermediate pod re-emits the upstream `elicitation/create` frame carrying the original `{message, schema}` payload. The gateway canonicalizes the forwarded frame's `{message, schema}` pair ... and compares its SHA-256 digest against the digest recorded at origination."

The two cannot both hold: a pod that forwards by `elicitation_id` only does not re-emit a `{message, schema}` frame for the gateway to canonicalize and digest-compare.

### 1.2 The code builds the id-only reading; the detector is dormant by construction

The v1 implementation builds the first reading:

- `dispatch` resolves the whole chain in one server-internal pass via `buildHops`, which walks the §8 delegation tree from `sessionstore` (`pkg/gateway/mcpfabric/mcptools/elicitation.go:460-515`). `buildHops` sets each ancestor hop's `Intercepts` from the dispatcher predicate, and `WalkChain` terminates at an intercepting parent or the human-facing edge (`pkg/elicitation/chain.go:248-260`).
- The elicitation is recorded once against the resolver session (`pkg/gateway/mcpfabric/mcptools/mcptools_register.go:1508-1516`), with the origination `{message, schema}` digest stamped as `contentDigest` (`mcptools_register.go:1490-1498`).
- No production path delivers an `elicitation/create` frame to an intermediate pod for re-emission. The only `elicitation/create` projection (`pkg/gateway/mcpfabric/mcp/projection.go:270-305`) is the gateway pushing the prompt down to the client, which is server-to-client rather than the pod-to-gateway re-emission the §9.2 detector would need.
- The dispatcher hook that would supply a re-emitted frame, `forwardedContentFor`, is nil in the only production construction (`pkg/gateway/mcpfabric/mcptools/mcptools_register.go:1269-1283` constructs the dispatcher without setting it), documented so at `pkg/gateway/mcpfabric/mcptools/elicitation.go:106-113`. Only test files inject a provider.

Because no intermediate pod ever handles or re-emits the `{message, schema}` payload, the gateway-origin binding holds by construction: the only content a client renders is the gateway-recorded original, and no v1 wire path lets an intermediate pod substitute altered text.

### 1.3 The residual: a High security control that reads as inert

`VerifyContent` (`pkg/elicitation/elicitation.go:267-282`) now digests the untrusted inbound content and compares it to the trusted gateway-recorded `originalDigest`, and `WalkChain` (`pkg/elicitation/chain.go:248-260`) runs that check only against a distinct re-emission supplied through `ChainInput.ForwardedContent`. The digest-comparison check, the enforce / detect-only / off modes, the platform floor, the justification API, the §16.7 audit event, the §16.1 metric, and the §16.5 alerts (F-9.2.2 through F-9.2.6, all CLOSED and tested via injected providers) are correct as the gateway's content-integrity enforcement point. They are structurally dormant in v1 only because no hop re-emits.

The finding remains open because the spec presents the re-emission wire mechanism as present-tense v1 behavior while the implementation does not build it, leaving a High security control that reads as inert rather than as intentionally structural. The `NEEDS-OPERATOR` deferral on the finding rests on the premise that published §26 external reference-runtime images are required to exercise the detector. That premise is confirmed stale below: the enforce / detect-only / off handling is already exercised in-repo through injected providers, and the finding also cites the pre-move `pkg/gateway/mcptools/` paths (the code moved to `pkg/gateway/mcpfabric/mcptools/` in commit `94118f3d`).

## 2. Decisions

- **Resolve the §9.2 line-56 self-contradiction toward the v1 server-internal resolution model that the code implements, rather than toward the re-emission reading.** Verified against the tree: `dispatch` resolves the chain in one server-internal `buildHops` pass (`pkg/gateway/mcpfabric/mcptools/elicitation.go:460-515`) and records the elicitation once against the resolver session (`mcptools_register.go:1508-1516`); no production code delivers an `elicitation/create` frame to an intermediate pod for re-emission (the only projection, `pkg/gateway/mcpfabric/mcp/projection.go:270-305`, is server-to-client). The spec is the defect here, matching the reference-proposal pattern of reconciling contradictory spec prose to the coherent implemented behavior.
- **In v1 the gateway-origin binding is enforced by construction, so "structurally untriggerable" is the correct design rather than a live defect.** The gateway records the origination `{message, schema}` (`mcptools_register.go:1490-1498` stamps `contentDigest`) and delivers that recorded original to the resolver; no intermediate pod touches or re-emits it, so no tamper opportunity exists on the v1 elicitation path. The reconciliation reframes F-9.2.1's observation from a defect to working-as-designed.
- **Keep the content-integrity machinery as the gateway's forward-compatible enforcement point; do not remove it.** `VerifyContent`'s trust boundary (trusted reference is the gateway-recorded `originalDigest`, untrusted input is a re-emission; `pkg/elicitation/elicitation.go:267-282`), the `WalkChain` verification branch (`chain.go:248-260`), `resolveMode`/`handleTamper` (`pkg/gateway/mcpfabric/mcptools/elicitation.go`), and the enforce / detect-only / off modes, floor, justification API, §16.7 audit event, §16.1 metric, and §16.5 alerts are all spec-correct and tested via injected providers (F-9.2.2 through F-9.2.6 CLOSED). They activate unchanged the moment any hop re-emits a frame, so the reconciliation preserves them rather than reopening that closed work.
- **Do not build the hop-by-hop per-hop re-emission producer in this proposal.** Producing a second `{message, schema}` submission requires changing the elicitation model from the current single-call server-internal lineage walk to actual per-hop pod forwarding over the §8 virtual MCP child interface plus a runtime that re-emits. That is a large, unbegun subsystem and new protocol surface, and it would additionally create the tamper threat (an intermediate pod that physically forwards and can alter content) that v1's collapsed model avoids by construction. The minimal-surface and fail-closed-by-construction principles favor reconciliation over introducing the vulnerable mechanism to then police it.
- **Scope the change to spec prose plus code-comment and test alignment; introduce no behavioral code change and no new RPC, frame, field, or endpoint.** `VerifyContent`, `WalkChain`, `dispatch`, and the detector stay byte-for-byte; only the spec text, the F-9.2.1-labeled "deferred surface" comments, one runtime header with its tier-9 test scaffold and the kind workload manifest that deploys it, one internal test's diagnosis, and the finding disposition change, plus one added tier-11 consistency test.

## 3. Design: the v1 server-internal resolution model and the forward-compatible enforcement point

The v1 elicitation path resolves the chain inside the gateway. `dispatch` calls `buildHops` to walk the §8 delegation tree from the raising session up to the root in one pass, resolving each ancestor's `Intercepts` flag, and `WalkChain` terminates the walk at an intercepting parent or the human-facing edge. The gateway records the origination `{message, schema}` once against the resolver session and delivers that recorded original to the resolver. Non-intercepting intermediate pods are bypassed; no pod re-emits a frame.

The content-integrity surface is complete and correct as the gateway's enforcement point. `VerifyContent` compares an untrusted inbound re-emission against the trusted gateway-recorded digest; `WalkChain` runs that comparison only when a distinct re-emission is supplied through `ChainInput.ForwardedContent`; and the dispatcher resolves the tenant's effective mode, emits the audit event, increments the metric, and drops or forwards per enforce / detect-only / off. In v1 the production dispatcher supplies no re-emission (the `forwardedContentFor` hook is nil), so the comparison never runs. Tests inject a provider to exercise the enforce and detect-only branches.

The reconciliation states this model in §9.2 and reframes the digest check as the dormant, forward-compatible enforcement point (C1), aligns the code comments, the runtime header, its tier-9 scaffold twin, and the kind workload manifest that deploys the runtime, all of which describe the seam as deferred / unbuilt work (C3, C4), strengthens the internal test that pins the structural default and adds a tier-11 guard against the contradiction reappearing (C5), and closes F-9.2.1 as reconciled while filing the active per-hop producer as post-v1 work (C6). No `VerifyContent`, `WalkChain`, `dispatch`, or detector behavior changes.

## 4. Proposed changes

### C1. Reconcile the §9.2 gateway-origin-binding paragraph (line 56): state the v1 server-internal resolution model and reframe the digest check as the forward-compatible enforcement point

**Target:** `spec/09_mcp-integration.md`, §9.2 Elicitation Chain, the "Elicitation content integrity (gateway-origin binding)" paragraph (line 56).

Line 56 asserts both that intermediate pods "forward elicitations upstream by `elicitation_id` only" and that "an intermediate pod re-emits the upstream `elicitation/create` frame carrying the original `{message, schema}` payload", a self-contradiction, since a pod that forwards by id only re-emits no content. The v1 code builds the id-only reading (`buildHops` resolves the chain server-internally; `mcptools_register.go:1508-1516` records once against the resolver). Rewriting the paragraph to the implemented model removes the contradiction and makes the digest check's dormancy correct-by-design instead of a High inert-control defect. The kept sentences preserve the gateway-authoritative statement, the origination recording, and the observe-but-not-modify rule; the two re-emission sentences are replaced with the server-internal resolution model and the forward-compatible framing; the closing enforcement-mode sentence is preserved.

**Anchor and change (§9.2 gateway-origin-binding paragraph, line 56):** Replace

```markdown
**Elicitation content integrity (gateway-origin binding).** The gateway is the authoritative source for elicitation display text. At origination time the gateway records the original `{message, schema}` pair (the two required inputs of the `lenny/request_elicitation` tool defined in [Section 8.5](08_recursive-delegation.md#85-platform-tool-inventory)) keyed by `elicitation_id` and the declared `origin_pod`, and the client UI is rendered from this stored original — not from whatever an intermediate hop re-emits. Intermediate pods forward elicitations upstream by `elicitation_id` only; they MAY observe the original content (for policy, logging, or deciding whether to suppress or dismiss) but MUST NOT modify the rendered text. The forward-hop wire mechanism is the native MCP `elicitation/create` frame defined in the per-kind wire projection ([Section 15.2.1](15_external-api-surface.md#1521-restmcp-consistency-contract)): an intermediate pod re-emits the upstream `elicitation/create` frame carrying the original `{message, schema}` payload. The gateway canonicalizes the forwarded frame's `{message, schema}` pair (stable key ordering, UTF-8 normalization) and compares its SHA-256 digest against the digest recorded at origination. The behavior on divergence is governed by the tenant's **elicitation content integrity enforcement mode**, clamped from below by a platform-level floor.
```

with

```markdown
**Elicitation content integrity (gateway-origin binding).** The gateway is the authoritative source for elicitation display text. At origination time the gateway records the original `{message, schema}` pair (the two required inputs of the `lenny/request_elicitation` tool defined in [Section 8.5](08_recursive-delegation.md#85-platform-tool-inventory)) keyed by `elicitation_id` and the declared `origin_pod`, and the client UI is rendered from this stored original, rather than from whatever an intermediate hop re-emits. Intermediate pods forward elicitations upstream by `elicitation_id` only; they MAY observe the original content (for policy, logging, or deciding whether to suppress or dismiss) but MUST NOT modify the rendered text. In v1 the gateway resolves the elicitation chain internally: it walks the [Section 8](08_recursive-delegation.md) delegation tree from the raising session up to the resolver (an intercepting parent or the human-facing edge) and delivers the gateway-recorded original to that resolver. No intermediate pod re-emits the `{message, schema}` payload, so the gateway-origin binding holds by construction: the client renders only the gateway-recorded original, and no v1 wire path lets an intermediate hop substitute altered text. The gateway's content-integrity check is defined for any forward hop that does re-emit a frame: the gateway canonicalizes the re-emitted `{message, schema}` pair (stable key ordering, UTF-8 normalization) and compares its SHA-256 digest against the digest recorded at origination. v1 provides no per-hop re-emission wire mechanism; the native MCP `elicitation/create` frame of the [Section 15.2.1](15_external-api-surface.md#1521-restmcp-consistency-contract) per-kind wire projection is the gateway's client-facing projection rather than a pod-to-gateway re-emission path. No hop re-emits, so the digest check is the dormant enforcement point that activates when per-hop re-emission is introduced. The behavior on divergence is governed by the tenant's **elicitation content integrity enforcement mode**, clamped from below by a platform-level floor.
```

**Rationale:** The contradiction is confirmed by the finding itself (`BUILD-GAPS.md:11489`), which flags it as needing the per-hop wire mechanism to become non-inert. `dispatch`/`buildHops` build the id-only reading, so the paragraph is rewritten to the server-internal resolution model that agrees with the code. The digest-comparison machinery is preserved and reframed as the forward-compatible enforcement point that is dormant by design in v1, so its dormancy is a deliberate structural property rather than a High inert-control defect. The enforcement-mode, floor, justification, provenance, and url-mode paragraphs that follow line 56 are left unchanged: they describe the divergence-handling that the gateway performs once any hop re-emits, and they read correctly as facets of the same dormant, forward-compatible enforcement point without a separate per-passage caveat (see Non-goals for why the §9.2 invariant at line 68, the enforcement-mode bullets at lines 60-62, and the §15.1 `ELICITATION_CONTENT_TAMPERED` catalog entry are not separately edited).

### C3. Align the code comments that label the seam a "deferred surface tracked by F-9.2.1" to the reconciled forward-compatible framing

**Target:** `pkg/elicitation/chain.go` (the `WalkChain` function doc header, lines 158-165; `ChainInput.ForwardedContent` doc, lines 119-133; the `WalkChain` forward-loop comment, lines 237-247); `pkg/gateway/mcpfabric/mcptools/elicitation.go` (`forwardedContentFor` field doc, lines 106-113; `divergentFields` doc, lines 408-414); `pkg/gateway/mcpfabric/mcptools/elicitation_tamper_internal_test.go` (`tamperDispatcher` helper doc, line 46); `pkg/elicitation/forwarded_content_test.go` (two test docs, lines 68-72 and 106-112).

These comments frame the nil provider as a deferred / unbuilt surface ("v1 has no per-hop re-emission wire mechanism (F-9.2.1), so the production dispatcher leaves this nil and the check never fires") and carry line-number spec citations ("spec: §9.2 lines 56–62", which C1 makes stale by rewriting the line-56 paragraph, and which already violate `code-best-practices.md`'s "Do not include line numbers"). This change broadens the sweep to every remaining F-9.2.1 deferred / dormant comment so no code comment survives asserting the un-reconciled framing after C1 and C6, rewording each to the structural-binding framing (the gateway resolves the chain server-internally, intermediate pods re-emit nothing, and the provider is the forward-compatible seam that tests inject to exercise the enforce and detect-only branches) and replacing every line-number spec citation with the line-number-free `// spec: §9.2 (gateway-origin binding; v1 structural enforcement)`. Comment-only edits; no behavioral change.

**Anchor and change (`chain.go` `ForwardedContent` doc, lines 119-133):** Replace

```go
	// ForwardedContent, when non-nil, supplies the {message, schema}
	// pair a forwarding hop re-emitted so the §9.2 gateway-origin
	// binding can compare it against the origination digest. The spec
	// forward-hop wire mechanism is the native MCP `elicitation/create`
	// frame an intermediate pod re-emits; the gateway canonicalizes the
	// re-emission and rejects a divergence. v1 intermediate pods forward
	// by `elicitation_id` only and re-emit nothing, so the production
	// dispatcher leaves this nil and a hop forwards unverified — the
	// per-hop re-emission wire path is the deferred surface tracked by
	// F-9.2.1. When the hook returns a content for a hop, that content
	// is verified; a divergence aborts the walk with a *ChainError
	// wrapping a *TamperError. The caller's enforcement mode (off →
	// nil hook) decides whether verification runs at all. spec: §9.2
	// lines 56–62.
	ForwardedContent func(hop Hop) (Content, bool)
```

with

```go
	// ForwardedContent, when non-nil, supplies the {message, schema}
	// pair a forwarding hop re-emitted so the §9.2 gateway-origin
	// binding can compare it against the origination digest. In v1 the
	// gateway resolves the elicitation chain server-internally and
	// intermediate pods re-emit nothing, so the gateway-origin binding
	// holds by construction and the production dispatcher leaves this
	// nil. This hook is the forward-compatible enforcement seam: tests
	// inject it to exercise the enforce and detect-only branches, and it
	// activates unchanged if a per-hop re-emission wire mechanism is
	// added. When the hook returns a content for a hop, that content is
	// verified; a divergence aborts the walk with a *ChainError wrapping
	// a *TamperError. The caller's enforcement mode (off → nil hook)
	// decides whether verification runs at all. spec: §9.2 (gateway-origin
	// binding; v1 structural enforcement).
	ForwardedContent func(hop Hop) (Content, bool)
```

**Anchor and change (`chain.go` `WalkChain` function doc header, lines 158-165):** Replace

```go
// WalkChain runs the §9.2 hop-by-hop elicitation chain.
//
// The walk starts at the raising session (Hops[0]) and proceeds
// upward through each parent hop. At every hop the gateway-origin
// content-integrity digest is re-verified against OriginalContent —
// an intermediate hop forwards by elicitation_id and MUST NOT alter
// the rendered {message, schema}; a divergence is a §9.2 tamper and
// aborts the walk with a *ChainError wrapping a *TamperError.
```

with

```go
// WalkChain runs the §9.2 hop-by-hop elicitation chain.
//
// The walk starts at the raising session (Hops[0]) and proceeds
// upward through each parent hop. A §9.2 intermediate pod forwards by
// elicitation_id only and re-emits nothing, so by default a hop
// advances unverified (the gateway-recorded original remains
// authoritative for the rendered text) and the gateway-origin binding
// holds by construction. When the caller supplies a re-emitted frame
// for a hop via ForwardedContent, the gateway canonicalizes it and
// compares its digest against the origination digest; a divergence is
// a §9.2 tamper that aborts the walk with a *ChainError wrapping a
// *TamperError.
```

**Anchor and change (`chain.go` `WalkChain` forward-loop comment, lines 237-247):** Replace

```go
	// Forward up the task tree hop by hop. A §9.2 intermediate pod
	// forwards by elicitation_id only and re-emits nothing, so by
	// default a hop advances unverified (the gateway-recorded original
	// remains authoritative for the rendered text). When the caller
	// supplies a re-emitted frame for a hop via ForwardedContent, the
	// gateway canonicalizes it and compares its digest against the
	// origination digest; a divergence is a §9.2 tamper that aborts the
	// walk with a *ChainError wrapping a *TamperError. Comparing the
	// gateway-held original against its own digest would be a tautology,
	// so verification runs only against an actual re-emission. spec:
	// §9.2 lines 56–62; F-9.2.1.
```

with

```go
	// Forward up the task tree hop by hop. A §9.2 intermediate pod
	// forwards by elicitation_id only and re-emits nothing, so by
	// default a hop advances unverified (the gateway-recorded original
	// remains authoritative for the rendered text) and the gateway-origin
	// binding holds by construction. When the caller supplies a re-emitted
	// frame for a hop via ForwardedContent, the gateway canonicalizes it
	// and compares its digest against the origination digest; a divergence
	// is a §9.2 tamper that aborts the walk with a *ChainError wrapping a
	// *TamperError. Comparing the gateway-held original against its own
	// digest would be a tautology, so verification runs only against an
	// actual re-emission. spec: §9.2 (gateway-origin binding; v1 structural
	// enforcement).
```

**Anchor and change (`pkg/gateway/mcpfabric/mcptools/elicitation.go` `forwardedContentFor` field doc, lines 106-113):** Replace

```go
	// forwardedContentFor, when non-nil, supplies the {message, schema}
	// pair a forwarding hop re-emitted, so the §9.2 content-integrity
	// check has something to compare against the origination digest.
	// v1 has no per-hop re-emission wire mechanism (F-9.2.1), so the
	// production dispatcher leaves this nil and the check never fires;
	// tests inject a provider to drive the enforce / detect-only
	// branches deterministically. spec: §9.2 lines 56–62.
	forwardedContentFor func(hop elicitation.Hop) (elicitation.Content, bool)
```

with

```go
	// forwardedContentFor, when non-nil, supplies the {message, schema}
	// pair a forwarding hop re-emitted, so the §9.2 content-integrity
	// check has something to compare against the origination digest.
	// In v1 the gateway resolves the elicitation chain server-internally
	// and intermediate pods re-emit nothing, so the gateway-origin
	// binding holds by construction and the production dispatcher leaves
	// this nil. The field is the forward-compatible enforcement seam:
	// tests inject a provider to drive the enforce and detect-only
	// branches deterministically, and it activates unchanged if a per-hop
	// re-emission wire mechanism is added. spec: §9.2 (gateway-origin
	// binding; v1 structural enforcement).
	forwardedContentFor func(hop elicitation.Hop) (elicitation.Content, bool)
```

**Anchor and change (`pkg/gateway/mcpfabric/mcptools/elicitation.go` `divergentFields` doc, lines 408-414):** Replace

```go
// divergentFields reports which §9.2 {message, schema} fields the
// tampering pod's re-emission diverged on, for the audit event's
// divergent_fields payload. It locates the tampering hop and re-reads
// its re-emitted frame via the dispatcher's provider; when no provider
// is wired (production, where the per-hop re-emission wire mechanism is
// the deferred F-9.2.1 surface) it returns an empty slice. spec: §9.2
// line 56; §16.7 line 674.
```

with

```go
// divergentFields reports which §9.2 {message, schema} fields the
// tampering pod's re-emission diverged on, for the audit event's
// divergent_fields payload. It locates the tampering hop and re-reads
// its re-emitted frame via the dispatcher's provider; when no provider
// is wired (production, where the gateway resolves the chain
// server-internally and no hop re-emits) it returns an empty slice. The
// provider is the forward-compatible enforcement seam. spec: §9.2
// (gateway-origin binding; v1 structural enforcement); §16.7
// (elicitation.content_tamper_detected).
```

**Anchor and change (`pkg/gateway/mcpfabric/mcptools/elicitation_tamper_internal_test.go` `tamperDispatcher` doc, line 46):** Replace

```go
// tamperDispatcher wires a two-hop chain (sess_leaf → sess_mid) whose
// forwarding hop (sess_mid) re-emits a message-mutated frame, so the
// §9.2 content-integrity check fires deterministically. The injected
// provider stands in for the deferred per-hop re-emission wire mechanism
// (F-9.2.1); production leaves it nil. spec: §9.2 lines 56–62.
```

with

```go
// tamperDispatcher wires a two-hop chain (sess_leaf → sess_mid) whose
// forwarding hop (sess_mid) re-emits a message-mutated frame, so the
// §9.2 content-integrity check fires deterministically. In v1 the
// gateway resolves the chain server-internally and intermediate pods
// re-emit nothing, so production leaves the provider nil; the injected
// provider is the forward-compatible enforcement seam these tests use to
// exercise the enforce and detect-only branches. spec: §9.2
// (gateway-origin binding; v1 structural enforcement).
```

**Anchor and change (`pkg/elicitation/forwarded_content_test.go` `TestWalkChainForwardedContentDetectsDivergence_spec_9_2` doc, lines 68-72):** Replace

```go
// TestWalkChainForwardedContentDetectsDivergence_spec_9_2 proves the
// §9.2 gateway-origin binding: when a forwarding hop re-emits a mutated
// {message, schema}, the walk aborts with a *ChainError wrapping a
// *TamperError naming the diverging hop. This is the per-hop check that
// the removed tautology never actually performed (F-9.2.1).
```

with

```go
// TestWalkChainForwardedContentDetectsDivergence_spec_9_2 proves the
// §9.2 gateway-origin binding at the forward-compatible enforcement
// seam: when a forwarding hop re-emits a mutated {message, schema}, the
// walk aborts with a *ChainError wrapping a *TamperError naming the
// diverging hop. In v1 the gateway resolves the chain server-internally
// and no hop re-emits, so this seam is exercised by the injected
// provider and activates unchanged if a per-hop re-emission wire
// mechanism is added. spec: §9.2 (gateway-origin binding; v1 structural
// enforcement).
```

**Anchor and change (`pkg/elicitation/forwarded_content_test.go` `TestWalkChainNilForwardedContentForwardsUnverified_spec_9_2` doc, lines 106-112):** Replace

```go
// TestWalkChainNilForwardedContentForwardsUnverified_spec_9_2 proves the
// v1 default: with no re-emission provider (intermediate pods forward by
// elicitation_id only), every hop advances unverified and the chain
// resolves at the human edge. The old code compared the gateway-held
// original against its own digest — a tautology that could never catch
// anything and could never pass anything else; removing it must not
// regress the no-re-emission happy path. F-9.2.1.
```

with

```go
// TestWalkChainNilForwardedContentForwardsUnverified_spec_9_2 proves the
// v1 default: with no re-emission provider (intermediate pods forward by
// elicitation_id only and the gateway resolves the chain
// server-internally), every hop advances unverified and the chain
// resolves at the human edge, so the gateway-origin binding holds by
// construction. The old code compared the gateway-held original against
// its own digest, a tautology that could never catch or pass anything;
// removing it must not regress the no-re-emission happy path. spec: §9.2
// (gateway-origin binding; v1 structural enforcement).
```

**Rationale:** The comments carry line-number spec citations that C1 makes stale and reference F-9.2.1 as tracked / deferred work that C6 closes; the line-number citations also already violate `code-best-practices.md`. Rewording each to the structural-binding framing and to a line-number-free citation closes the internal inconsistency and satisfies the C5 tier-11 consistency guard, rather than leaving comment blocks contradicting the reconciled spec. The sweep covers `chain.go` (the `WalkChain` function doc header, the `ChainInput.ForwardedContent` field doc, and the forward-loop comment), `mcptools/elicitation.go`, the tamper internal test's `tamperDispatcher` helper, and both `forwarded_content_test.go` docs so no code comment survives asserting the un-reconciled "deferred surface" framing.

### C4. Align the elicitation-echo runtime header, its tier-9 scaffold twin, and the kind workload manifest that deploys it, which overclaim a live §9.2 tamper-detect path

**Target:** `cmd/runtimes/elicitation-echo/main.go`, header comment (lines 21-25); `tests/tier9_security/scaffolds_test.go`, the §12.9.9 live-e2e note (lines 238-240); `tests/testinfra/kind/agent-workload.yaml`, the `elicitation-echo-runtime` manifest comment (lines 119-122).

The header states elicitation-echo "unblocks the §9.2 tamper-detect probes: a tier-9 test pairs an `elicitation-echo` raising-pod with a tampering intermediary that re-emits a mutated payload on the chain walk, expecting the §9.2 `ELICITATION_CONTENT_TAMPERED` rejection". The tier-9 scaffold note in `tests/tier9_security/scaffolds_test.go` (lines 238-240) carries the identical framing, describing a live e2e that deploys elicitation-echo plus a tampering intermediary to observe `ELICITATION_CONTENT_TAMPERED` as work "on the tier-9 ops backlog". The kind workload manifest `tests/testinfra/kind/agent-workload.yaml` (lines 119-122) deploys the `elicitation-echo-runtime` under a comment that asserts the same live path in different words: the runtime "calls lenny/request_elicitation on every inbound message so the §9.2 chain walker exercises the tamper-detect path end to end". No such tampering-intermediary re-emission path or live tamper test exists (the tier-9 suite exercises the admin enforce / detect-only / floor wire contract through `elicitation_tamper_test.go`), and after C1 it cannot exist on the v1 elicitation flow. The chain walker does not exercise any tamper-detect path in v1, because the re-emission provider is nil in production and `VerifyContent` never runs. All three comments describe a mechanism v1 does not build.

**Anchor and change (elicitation-echo header, lines 21-25):** Replace

```go
// elicitation-echo unblocks the §9.2 tamper-detect probes: a tier-9
// test pairs an `elicitation-echo` raising-pod with a tampering
// intermediary that re-emits a mutated payload on the chain walk,
// expecting the §9.2 ELICITATION_CONTENT_TAMPERED rejection and the
// §16.5 ElicitationContentTamperDetected alert to fire.
```

with

```go
// elicitation-echo is the §9.2 Standard-level exemplar runtime: it
// calls `lenny/request_elicitation` on each inbound message and returns
// the human response. The elicitation integration suite and the tier-9
// admin probes for the enforce, detect-only, and floor wire contract use
// it as the raising pod. In v1 the gateway resolves the elicitation
// chain server-internally and no intermediate pod re-emits, so there is
// no live content-tamper path for this runtime to drive; the §9.2
// content-integrity detector is the forward-compatible enforcement point
// exercised by injected providers in unit tests.
```

**Anchor and change (`tests/tier9_security/scaffolds_test.go` §12.9.9 live-e2e note, lines 238-240):** Replace

```go
// The live e2e exercise (deploy elicitation-echo + a tampering
// intermediary, observe ELICITATION_CONTENT_TAMPERED + the §16.5
// alert) is on the tier-9 ops backlog.
```

with

```go
// A live per-hop content-tamper e2e (an intermediate pod re-emits a
// mutated payload on the chain walk, observed as
// ELICITATION_CONTENT_TAMPERED and the §16.5 alert) is post-v1 work.
// In v1 the gateway resolves the elicitation chain server-internally
// and no intermediate pod re-emits, so there is no per-hop re-emission
// wire path to drive it; it is the post-v1 per-hop re-emission producer
// bead that proposal 0030 (F-9.2.1) files.
```

**Anchor and change (`tests/testinfra/kind/agent-workload.yaml` `elicitation-echo-runtime` manifest comment, lines 119-122):** Replace

```yaml
# §9.2 tier-9 tamper-detect probe runtime. The elicitation-echo
# Standard-level runtime calls lenny/request_elicitation on every inbound
# message so the §9.2 chain walker exercises the tamper-detect path end to
# end.
```

with

```yaml
# §9.2 Standard-level elicitation runtime. The elicitation-echo runtime
# calls lenny/request_elicitation on every inbound message and raises §9.2
# elicitations that the gateway resolves server-internally. In v1 no
# intermediate pod re-emits, so this runtime drives no live tamper-detect
# path end to end; the tier-9 admin probes use it as the raising pod for
# the enforce, detect-only, and floor wire contract.
```

**Rationale:** Drops the paragraph claiming the runtime unblocks a tamper-detect probe via a tampering-intermediary re-emission, and keeps the accurate description of the runtime as the §9.2 Standard-level exemplar used by the elicitation integration and tier-9 admin probes. Two other comments assert the same live tamper-detect path in different words: the `tests/tier9_security/scaffolds_test.go` twin carries the identical "elicitation-echo + a tampering intermediary" e2e framing, and the `tests/testinfra/kind/agent-workload.yaml` manifest that deploys the runtime states the "§9.2 chain walker exercises the tamper-detect path end to end". A grep across the Go and manifest sources for both the "a tampering" and "tamper-detect path" phrasings confirms these three files are the only ones asserting a live v1 tamper path; `tests/tier3_contract/rest_mcp_consistency/scaffolds_test.go:413` and `tests/tier9_security/scaffolds_test.go:246` describe the same detector as covered by the unit suites, which stays accurate, and the tier-8 chaos reference at `tests/tier8_chaos/scaffolds_test.go:339-351` covers §12.8 elicitation deadlock rather than the §9.2 tamper path. Relabeling all three as post-v1 per-hop-producer work leaves no code or manifest comment asserting a live v1 tamper-detect path. Comment-only; no code change.

### C5. Strengthen the structural-behavior internal test's diagnosis and add a tier-11 §9.2 consistency test

**Target:** `pkg/gateway/mcpfabric/mcptools/elicitation_tamper_internal_test.go`, `TestDispatchNoProviderNeverDetects_spec_9_2` doc (lines 319-322); and a new `tests/tier11_docs/elicitation_content_integrity_consistency_test.go`.

`TestDispatchNoProviderNeverDetects_spec_9_2` already pins the v1 behavior (no provider means the chain forwards unverified even under enforce, with zero metric and audit calls). Its comment frames that as the F-9.2.1 deferred default; after C1 it is the working-as-designed structural binding and its diagnosis should say so. A tier-11 consistency test guards the reconciled §9.2 prose so the contradiction cannot silently reappear. The new test is placed in `tests/tier11_docs/` (package `tier11_docs_test`), which the hard-coded tier-11 runner (`./tests/tier11_docs/...`) executes, and it reuses the existing `section`/`specSection`/`requireAllContain`/`requireNoneContain` helpers rather than adding new file I/O scaffolding.

**Anchor and change (`elicitation_tamper_internal_test.go` `TestDispatchNoProviderNeverDetects_spec_9_2` doc, lines 319-322):** Replace

```go
// TestDispatchNoProviderNeverDetects_spec_9_2 proves the production
// default (no per-hop re-emission provider, F-9.2.1) advances the chain
// unverified even under enforce: with nothing re-emitted there is no
// divergence to catch, so the elicitation forwards normally.
```

with

```go
// TestDispatchNoProviderNeverDetects_spec_9_2 pins the v1 production
// behavior: the gateway resolves the elicitation chain server-internally
// and no intermediate pod re-emits, so the dispatcher advances the chain
// unverified even under enforce and the gateway-origin binding holds by
// construction. With nothing re-emitted there is no divergence to catch,
// so the elicitation forwards normally and no metric or audit event is
// emitted. spec: §9.2 (gateway-origin binding; v1 structural enforcement).
```

**Anchor and change (new tier-11 consistency test):** Add `tests/tier11_docs/elicitation_content_integrity_consistency_test.go`

```go
// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency check for the §9.2 elicitation
// content-integrity gateway-origin binding (proposal 0030, F-9.2.1).
// Proposal 0030 reconciled the §9.2 line-56 self-contradiction to the
// v1 server-internal resolution model: the gateway resolves the
// elicitation chain internally and delivers the recorded original to the
// resolver, no intermediate pod re-emits the {message, schema} payload,
// and the SHA-256 digest check is the forward-compatible enforcement
// point that is dormant until a per-hop re-emission wire mechanism
// exists. This test pins that reconciliation so a later spec edit cannot
// silently reintroduce the present-tense re-emission claim the code does
// not build.
//
// The test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: §9.2 (gateway-origin binding; v1 structural enforcement).

package tier11_docs_test

import (
	"path/filepath"
	"testing"
)

// spec: 9.2
// diagnosis: §9.2's gateway-origin-binding paragraph drifted back to the
// self-contradictory state F-9.2.1 named, asserting both that
// intermediate pods forward by elicitation_id only and that an
// intermediate pod re-emits the upstream elicitation/create frame
// carrying the {message, schema} payload. A failure here means the spec
// again presents the per-hop re-emission wire mechanism as present-tense
// v1 behavior the implementation does not build, so a reader cannot tell
// whether the content-integrity detector is a live control or a dormant
// forward-compatible enforcement point.
func TestSpecElicitationContentIntegrityReconciled_F921(t *testing.T) {
	root := repoRoot(t)
	s92 := specSection(t, filepath.Join(root, "spec", "09_mcp-integration.md"), "### 9.2 ")

	// The reconciled v1 model: the gateway resolves the chain internally
	// and delivers the recorded original to the resolver; no per-hop
	// re-emission wire mechanism exists; the digest check is the dormant
	// enforcement point.
	requireAllContain(t, "§9.2 gateway-origin binding", s92, []string{
		"forward elicitations upstream by `elicitation_id` only",
		"the gateway resolves the elicitation chain internally",
		"delivers the gateway-recorded original to that resolver",
		"no v1 wire path lets an intermediate hop substitute altered text",
		"v1 provides no per-hop re-emission wire mechanism",
		"the digest check is the dormant enforcement point",
	})

	// The removed present-tense re-emission sentence must not return; it
	// is the exact phrasing F-9.2.1 flagged as un-built v1 behavior. The
	// enforcement-mode bullets keep their own conditional phrasing, so
	// this banned string is scoped to the removed sentence only.
	requireNoneContain(t, "§9.2 gateway-origin binding", s92, []string{
		"re-emits the upstream `elicitation/create` frame carrying the original `{message, schema}` payload",
	})
}
```

**Rationale:** The internal test already pins runtime dispatch behavior, so its edit is a comment-only reframe from the F-9.2.1 deferred default to the working-as-designed structural binding; the test is tier-1, so the `// diagnosis:` line is optional under `test-coverage.md` and is not added. The tier-11 guard is genuinely new coverage with no smaller substitute that still prevents the contradiction from reappearing. It follows the in-repo precedent of `tests/tier11_docs/budget_extension_trigger_consistency_test.go` (proposal 0023, closing F-8.6.6), reading the §9.2 section and pinning the reconciliation with the shared `requireAllContain`/`requireNoneContain` helpers. The banned string is scoped to the exact removed sentence, so the surviving enforcement-mode bullets and the §9.2 invariant do not false-positive.

### C6. Close F-9.2.1 as reconciled on application

**Target:** `BUILD-GAPS.md`, F-9.2.1 (line 11489).

The finding is a High security item still marked open (heading `[ ]`) with a DEFERRED / `NEEDS-OPERATOR` body. After the reconciliation the disposition is resolved: v1 enforces the gateway-origin binding by construction and the digest detector is the forward-compatible enforcement point, dormant by design. The closure is contingent on the reviewer accepting the C1 reconciliation direction (see Open decisions) and on a post-v1 bead being filed for the active per-hop producer before the finding is marked CLOSED.

**Anchor and change (F-9.2.1 heading, line 11489):** Replace

```markdown
### - [ ] F-9.2.1 — Tamper detector is structurally untriggerable; per-hop content verification is a tautology [High] — OPEN
```

with

```markdown
### - [x] F-9.2.1 — Tamper detector is structurally untriggerable; per-hop content verification is a tautology [High] — CLOSED (reconciled, proposal 0030)
```

**Anchor and change (append a closure note after the existing `DEFERRED 2026-06-09 — NEEDS-OPERATOR` paragraph):** Add

```markdown
**CLOSED (reconciled to the v1 structural gateway-origin binding, proposal 0030).** Contingent on the reviewer accepting the C1 reconciliation direction (see the proposal's Open decisions). §9.2 line 56 was self-contradictory: it asserted both that intermediate pods forward by `elicitation_id` only and that an intermediate pod re-emits the upstream `elicitation/create` frame carrying the original `{message, schema}` payload. The v1 implementation builds the id-only reading: the gateway resolves the elicitation chain server-internally (`pkg/gateway/mcpfabric/mcptools/elicitation.go` `buildHops` / `dispatch`) and records the elicitation once against the resolver session (`pkg/gateway/mcpfabric/mcptools/mcptools_register.go:1508-1516`); no production path delivers an `elicitation/create` frame to an intermediate pod for re-emission (the only projection, `pkg/gateway/mcpfabric/mcp/projection.go:270-305`, is server-to-client). Proposal 0030 reconciles §9.2 to that model (C1) and reframes the content-integrity digest check as the forward-compatible enforcement point that is dormant by design in v1. The gateway-origin binding holds by construction: the client renders only the gateway-recorded original and no v1 wire path lets an intermediate hop substitute altered text, so the detector being structurally untriggerable is working-as-designed rather than an inert control. The prior `NEEDS-OPERATOR` deferral is retracted: its premise that published §26 external reference-runtime images are required to exercise the detector is superseded, since the enforce / detect-only / off handling (F-9.2.2 through F-9.2.6, CLOSED) is exercised in-repo through injected providers, and the finding's `pkg/gateway/mcptools/` paths predate the move to `pkg/gateway/mcpfabric/mcptools/` (commit `94118f3d`). The active per-hop re-emission producer and live detector are a distinct post-v1 subsystem, filed as bead `<BEAD-ID>` alongside the §21 planned-post-v1 elicitation material; F-9.2.1 is marked CLOSED only once that bead exists, and the note cites its id. If the reviewer instead resolves the fork toward building the producer in v1, this closure is not applied and F-9.2.1 stays open.
```

**Rationale:** The finding's factual premises hold: the §9.2 self-contradiction is real, the detector is dormant in production by construction (`forwardedContentFor` has no non-test populator), and the heading is stale (`[ ] ... OPEN` over a DEFERRED body that cites pre-move paths). After C1 the DEFERRED / `NEEDS-OPERATOR` body is factually wrong, so a closure edit is warranted. The closure is bounded: it is contingent on the reviewer accepting reconciliation, it retracts the stale `NEEDS-OPERATOR` premise explicitly, and it requires filing a post-v1 bead for the buildable per-hop producer so a High-severity security control does not silently exit the tracker under a "CLOSED — working as designed" label.

## 5. Non-goals

- **Building the hop-by-hop per-hop re-emission producer** (the §8 virtual-MCP forward path that delivers an `elicitation/create` frame to each intermediate pod plus a runtime that re-emits it). That is a large, unbegun subsystem and new protocol surface, and it would create the intermediate-tamper threat that v1's server-internal model avoids by construction. It is captured as a post-v1 follow-up bead and is not implemented here.
- **Removing or trimming the content-integrity machinery** (`VerifyContent`, the `WalkChain` verification branch, `resolveMode`/`handleTamper`, the enforce / detect-only / off modes, the platform floor, the justification API, the §16.7 audit event, the §16.1 metric, or the §16.5 alerts). These stay as the gateway's forward-compatible enforcement point; F-9.2.2 through F-9.2.6 remain CLOSED.
- **Changing `VerifyContent`'s trust boundary or the `WalkChain` / `dispatch` control flow.** There are no behavioral code changes; the proposal is spec prose plus comment and test alignment.
- **Adding a pod-to-gateway direction to the §15.2.1 per-kind wire projection or any other new frame, field, RPC, or endpoint.**
- **Light-consistency edits to the §9.2 invariant (line 68), the §9.2 enforcement-mode bullets (lines 60-62), and the §15.1 `ELICITATION_CONTENT_TAMPERED` catalog entry (`spec/15_external-api-surface.md:1087`).** An earlier draft proposed caveating lines 68 and 1087 to match the reconciled framing (draft change C2). It was dropped as resting on a false premise and as unnecessary given C1. The same present-tense re-emission framing appears in the enforcement-mode bullets at lines 60-62 ("the tampering pod receives this error on its forward call", "The divergent payload is forwarded to the client as received", "the forwarded frame is delivered without a tamper check"), which are left in force. If C1's reframing at line 56 makes lines 60-62 read correctly as the dormant, forward-compatible enforcement point, it makes lines 68 and 1087 read correctly too, since line 68 ("user-visible text is whatever the forwarding pod emitted") is vacuously satisfied in v1 rather than contradicted, and the §15.1 entry at 1087 correctly defines the error's triggering condition and already gates on the effective mode being `enforce`. Caveating only two of several identical present-tense passages would introduce a new asymmetry where a reader wonders why some divergence-behavior passages carry a "v1 dormant" note and others do not. C1 is the canonical fix at the concept's owning paragraph, and its reconciliation propagates to every downstream reference.

## 6. Testing

The change makes no behavioral code change; it is spec prose plus comment and test alignment. The reached tiers are tier 0 (the comment edits and the new tier-11 test compile), tier 1 (the existing structural-behavior tests stay green and pin the reconciled behavior), and tier 11 (the spec-prose reconciliation), per `.claude/rules/test-coverage.md`.

- **tier-11 doc and spec consistency (reconciled §9.2 prose, contradiction-regression path):** `TestSpecElicitationContentIntegrityReconciled_F921` (new, `tests/tier11_docs/elicitation_content_integrity_consistency_test.go`). Assert the §9.2 gateway-origin-binding paragraph carries the reconciled statements (the gateway resolves the chain internally, delivers the recorded original to the resolver, provides no per-hop re-emission wire mechanism, and the digest check is the dormant enforcement point) and the surviving id-only phrasing, and does not carry the removed present-tense re-emission sentence, so a future edit reintroducing the contradiction fails. `// spec: 9.2 (gateway-origin binding; v1 structural enforcement). // diagnosis: §9.2 drifted back to the self-contradictory forward-hop model F-9.2.1 named, so a reader cannot tell whether the content-integrity detector is a live control or a dormant forward-compatible enforcement point.`
- **tier-1 (production structural default, no re-emission provider, spec-named-failure path):** `TestDispatchNoProviderNeverDetects_spec_9_2` (existing, diagnosis strengthened by C5). With no provider under `enforce`, the chain forwards unverified, the resolver is the mid session, and zero metric and audit calls are made, so the gateway-origin binding holds by construction and the dormant detector never fires. `// spec: 9.2 (gateway-origin binding; v1 structural enforcement).`
- **tier-1 (forward-compatible seam preserved, injected-re-emission error path):** `TestWalkChainForwardedContentDetectsDivergence_spec_9_2` and the enforce / detect-only internal dispatcher tests (existing, regression). An injected divergent re-emission still aborts with a `*ChainError` wrapping a `*TamperError` (and the dispatcher still returns `ELICITATION_CONTENT_TAMPERED` under `enforce`), proving C1 and C3 reframed the seam as dormant without disabling the enforcement point. `// spec: 9.2 (gateway-origin binding; v1 structural enforcement).`
- **tier-1 (no-re-emission happy path unchanged, boundary path):** `TestWalkChainNilForwardedContentForwardsUnverified_spec_9_2` (existing, comment reframed by C3). With no re-emission provider every hop advances unverified and the chain resolves at the human edge, so the reconciled comment does not regress the walk. `// spec: 9.2 (gateway-origin binding; v1 structural enforcement).`

## Findings closed on application

- **F-9.2.1** (`BUILD-GAPS.md:11489`, "Tamper detector is structurally untriggerable; per-hop content verification is a tautology", High). Closed as reconciled: §9.2 line 56's self-contradictory forward-hop model is rewritten to the v1 server-internal resolution model the code builds (C1); the code comments, the elicitation-echo header, its tier-9 scaffold twin, and the kind workload manifest that deploys the runtime, all of which label the seam a deferred / unbuilt surface, are aligned to the forward-compatible framing (C3, C4); the structural-behavior internal test's diagnosis is strengthened and a tier-11 consistency guard is added (C5). The gateway-origin binding holds by construction in v1, and the content-integrity digest check (F-9.2.2 through F-9.2.6, CLOSED) is the forward-compatible enforcement point, dormant by design. Closure is contingent on the reviewer accepting the reconciliation direction (Open decisions) and on a post-v1 bead being filed for the active per-hop producer; the closure note cites that bead id.

## Resolved in adversarial review

Review rounds populate this section. It records each finding fixed and the converging change.

### Pass 1 (2026-07-03, automated)

- **C3 comment sweep missed the `WalkChain` function doc header (`chain.go:158-165`).** The header still asserted the removed tautology ("At every hop the gateway-origin content-integrity digest is re-verified against OriginalContent"), which contradicts C1's reconciled spec, the `ChainInput.ForwardedContent` field doc, and the forward-loop comment that C3 rewrites in the same file, and is stale against the post-`684ab36e` code (the loop verifies only when a re-emission is supplied through `ForwardedContent`). Added the header to the C3 target list, added a new anchor-and-change rewriting lines 161-165 to the reconciled framing (a hop advances unverified by default, verification runs only against a re-emission supplied through `ForwardedContent`, and the gateway-origin binding holds by construction), and updated the C3 completeness claim and the §7 Files-touched entry to name the header.
- **C3/C4 comment sweep missed `tests/tier9_security/scaffolds_test.go` (lines 238-240).** The tier-9 §12.9.9 scaffold note carried the identical "deploy elicitation-echo + a tampering intermediary, observe ELICITATION_CONTENT_TAMPERED" live-e2e framing that C4 removes from the elicitation-echo header, which C1 establishes cannot exist on the v1 elicitation flow. Grep confirmed these two files are the only "a tampering" matches. Broadened C4's title and target to cover the scaffold twin, added an anchor-and-change relabeling the note as post-v1 per-hop re-emission producer work, updated the C4 problem statement and rationale, and added the scaffold to the scope summary and the §7 Files-touched list.

### Pass 2 (2026-07-03, automated)

- **C4 comment sweep missed the kind workload manifest `tests/testinfra/kind/agent-workload.yaml` (lines 119-122).** The `elicitation-echo-runtime` manifest that deploys the elicitation-echo runtime asserted the same live tamper-detect path C4 removes from the runtime header, stating the runtime "calls lenny/request_elicitation on every inbound message so the §9.2 chain walker exercises the tamper-detect path end to end". C1 establishes that the chain walker exercises no tamper-detect path in v1, because the re-emission provider is nil in production and `VerifyContent` never runs, so this is the identical overclaim in different words. C4's completeness claim ("grep confirms these two files are the only ones matching that framing") was false: its grep matched only the literal "a tampering" substring and missed the manifest's "tamper-detect path end to end" phrasing. Added the manifest to C4's title target list, added a problem-statement sentence naming it, and added an anchor-and-change relabeling the comment to the reconciled framing (the runtime raises §9.2 elicitations the gateway resolves server-internally, and in v1 no pod re-emits, so the runtime drives no live tamper-detect path end to end). Corrected the C4 completeness claim to cover both the "a tampering" and "tamper-detect path" phrasings across the Go and manifest sources, confirming these three files are the only ones asserting a live v1 tamper path (the tier-3 and tier-9 "covered by unit suites" comments and the tier-8 §12.8 deadlock reference stay accurate). Added the manifest to the scope summary, the Decisions and Design summaries, the Findings-closed summary, and the §7 Files-touched list.

## Open decisions for review

1. **Design fork: reconcile the spec to the v1 structural-binding model (this proposal) versus build the hop-by-hop re-emission producer so the detector actively fires.** Recommended and staged: reconcile, on minimal-surface and fail-closed-by-construction grounds. The producer path is buildable and testable on local Kind using the in-repo elicitation-echo / delegation-echo runtimes (which refutes the BUILD-GAPS `NEEDS-OPERATOR` / §26 external-runtime framing), but it is a large subsystem that also introduces the intermediate-tamper threat that v1's collapsed model avoids by construction. If the reviewer resolves the fork toward building the producer, C6 is not applied and F-9.2.1 stays open.
2. **If reconciliation is accepted, whether to keep the full enforcement-mode / floor / justification admin surface active in v1 (an operator-facing control governing a forward-compatible check) or to gate or annotate it as dormant until per-hop re-emission exists.** Recommended: keep it active and reframed, since it is a shipped, tested surface and removing it reopens closed findings (F-9.2.2 through F-9.2.6).
3. **Where the post-v1 bead for the per-hop re-emission wire mechanism should sit relative to §21 planned-post-v1.** Filing the bead is a mandatory step of C6 (F-9.2.1 is not marked CLOSED until it exists). The recommendation is to place it alongside the §21 planned-post-v1 elicitation material, which currently tracks the A2A elicitation constraint but nothing about a re-emission producer.

## 7. Files touched on application

- `spec/09_mcp-integration.md`: C1 (§9.2 gateway-origin-binding paragraph, line 56).
- `pkg/elicitation/chain.go`: C3 (`WalkChain` function doc header; `ChainInput.ForwardedContent` doc; `WalkChain` forward-loop comment).
- `pkg/elicitation/forwarded_content_test.go`: C3 (`TestWalkChainForwardedContentDetectsDivergence_spec_9_2` and `TestWalkChainNilForwardedContentForwardsUnverified_spec_9_2` docs).
- `pkg/gateway/mcpfabric/mcptools/elicitation.go`: C3 (`forwardedContentFor` field doc; `divergentFields` doc).
- `pkg/gateway/mcpfabric/mcptools/elicitation_tamper_internal_test.go`: C3 (`tamperDispatcher` doc) and C5 (`TestDispatchNoProviderNeverDetects_spec_9_2` doc).
- `cmd/runtimes/elicitation-echo/main.go`: C4 (header comment).
- `tests/tier9_security/scaffolds_test.go`: C4 (§12.9.9 live-e2e note).
- `tests/testinfra/kind/agent-workload.yaml`: C4 (`elicitation-echo-runtime` manifest comment).
- `tests/tier11_docs/elicitation_content_integrity_consistency_test.go`: C5 (new tier-11 consistency test).
- `BUILD-GAPS.md`: C6 (flip F-9.2.1 OPEN → CLOSED, contingent on the reviewer accepting reconciliation and on the post-v1 bead being filed, referencing proposal 0030).
