# Proposal: Reconcile the §12.6 and §25.3 CloudEvents type-alias mandates with the shipped native envelope struct, codifying the native CloudEvents v1.0.2 structured-content type and pinning the inline-OCSF wire form

- **Status:** Approved (2026-07-02). Converged after 2 adversarial review rounds (0 findings fixed); both §9 open decisions resolved by the reviewer (Direction A; keep the terse go-sdk comment framing).
- **Date:** 2026-07-02.
- **Scope:** A spec-to-code reconciliation with no behavioral code change. §12.6 mandates `type Event = cloudevents.Event` (`spec/12_storage-architecture.md:657-659`) and §25.3 independently mandates `type OperationalEvent = cloudevents.Event` (`spec/25_agent-operability.md:652-653`), yet both packages ship a native structured-content struct instead (`pkg/gateway/storage/eventbus/cloudevents.go:80`, `pkg/events/types.go:42`) because the released go-sdk serializes `application/ocsf+json` data as an escaped JSON string, which would double-wrap the audit record and violate the same sections' single-envelope inline model. This proposal codifies the native struct the code already ships, replacing the two alias mandates (**S1**, **S2**), syncs the two package doc comments that describe the code as diverging from the spec's alias (**S3**), and adds a contract test that pins the inline-OCSF wire obligation the amendment introduces (**S4**). It touches `spec/12_storage-architecture.md` and `spec/25_agent-operability.md`, plus lockstep doc-comment edits in `pkg/gateway/storage/eventbus/cloudevents.go` and `pkg/events/types.go`, and new tier-3 and tier-1 tests. The single-envelope inline-OCSF model, the `datacontenttype: application/ocsf+json` value, and the retranscribe byte-identical re-serialization requirement are unchanged; the amendment reconciles the type declaration to those contracts, which the native struct already satisfies. Direction B (add the go-sdk dependency and switch to the alias) is recorded in Non-goals and Open decisions.

This document stages the proposed spec changes. It does not modify any spec, code, or doc file. Apply the changes in the "Proposed changes" section after sign-off.

## 1. Problem

§12.6 declares the EventBus envelope type as a direct alias of the go-sdk type, with a comment that the go-sdk type "is used directly; no Lenny-specific wrapper":

```go
// Event is a CloudEvents v1.0.2 envelope. The `go-sdk` cloudevents.Event type
// is used directly; no Lenny-specific wrapper.
type Event = cloudevents.Event
```

(`spec/12_storage-architecture.md:657-659`). §25.3 independently mandates the parallel alias for the operability envelope:

```go
// OperationalEvent is a CloudEvents v1.0.2 Event — see §12.6.
type OperationalEvent = cloudevents.Event
```

(`spec/25_agent-operability.md:652-653`). These are separate types in separate packages, and each is the sole spec basis for the corresponding shipped envelope type. Both sections also mandate the single-envelope inline model: an audit-bearing event carries the OCSF record directly in `data` with `datacontenttype: application/ocsf+json`, and "nothing is double-wrapped" (`spec/12_storage-architecture.md:682`; `spec/25_agent-operability.md:649`). §12.6 additionally requires the retranscribe worker to re-serialize a byte-identical envelope so downstream de-duplication by CloudEvents `id` continues to work (`spec/12_storage-architecture.md:688,:694`).

The alias mandate contradicts the inline model. The cloudevents go-sdk is absent from the module graph (a grep of `go.mod` and `go.sum` for `cloudevents` returns zero). The released go-sdk does not honor the RFC 6839 `+json` structured suffix in its `isJSON` content-type gate, so aliasing to `cloudevents.Event` serializes `application/ocsf+json` data as an escaped JSON string rather than an inline object, double-wrapping the OCSF record and violating both the single-envelope model and the byte-identical re-serialization contract.

The code instead ships a native struct in both packages, and each is wire-correct. `pkg/gateway/storage/eventbus/cloudevents.go:80` declares `type Event struct` with `Data json.RawMessage` (`:108`) emitted inline by `MarshalJSON` (`:136-138`); `pkg/events/types.go:42` declares `type OperationalEvent struct` with `Data json.RawMessage` (`:76`) flattened inline by its own `MarshalJSON`. Existing tests assert the audit-bearing `datacontenttype` is `application/ocsf+json` and that `data` round-trips as raw JSON (`pkg/gateway/storage/eventbus/cloudevents_test.go`; `tests/tier3_contract/cloudevents/cloudevents_test.go`), but no test asserts at the byte level that the OCSF record appears inline (un-escaped) under the top-level `data` key. The shipped structs are correct against the inline model; they are not the SDK alias the spec mandates, so the spec and the code diverge.

This is the OPEN finding **F-12.6.21** (`BUILD-GAPS.md:22207`). The two package doc comments already describe the code as a deliberate divergence from the spec's alias: `pkg/gateway/storage/eventbus/cloudevents.go:75-79` states "The spec uses the cloudevents go-sdk Event type directly; this struct is the equivalent ...", and `pkg/events/types.go:34-41` states "The spec illustrates the Go type as `type OperationalEvent = cloudevents.Event`; the CloudEvents go-sdk is deliberately not vendored ...". Once the spec codifies the native struct, those clauses go stale and must track the amended spec.

The go-sdk `+json` gap is version-specific rather than inherent: go-sdk main rewrote `isJSON` to accept any RFC 6838 `+json` subtype, but that fix is unreleased as of 2026-07-02 (the latest tag remains v2.16.2). Two resolution directions therefore exist. Direction A codifies the native, wire-correct struct the code already ships. Direction B adds the go-sdk dependency and switches to the alias, which requires either accepting double-wrapped audit data on the released SDK or pinning the module to an unreleased upstream commit. This proposal chooses Direction A on grounds of released-SDK availability and dependency minimization; the choice is recorded as an open decision.

## 2. Decisions

- **Reconcile in the spec-to-code direction (Direction A).** Codify the native CloudEvents v1.0.2 structured-content struct the code already ships, replacing the two `= cloudevents.Event` alias mandates. This is a design choice grounded in released-SDK availability and dependency minimization rather than a claim that the alias is inherently unsatisfiable. The released go-sdk is the only vendorable version and its content-type gate double-wraps `application/ocsf+json` data; the `+json`-honoring fix exists only on go-sdk main and is unreleased. Adopting the alias would either double-wrap OCSF records or pin the module to an unreleased upstream commit, a supply-chain and maintenance surface the code-best-practices rule tells us to avoid, plus a rewrite of the native marshal/unmarshal/validate surface and every producer and consumer.
- **Do not add a new external-interop byte-round-trip requirement.** No inbound path parses an external CloudEvents JSON document and flows it through `EventBus.Publish`; the `application/cloudevents+json` usages elsewhere are outbound delivery content-type constants. The byte-level obligation the native struct must meet is already pinned by the single-envelope inline model (`spec/12_storage-architecture.md:682`; `spec/25_agent-operability.md:649`) and the retranscribe byte-identical re-serialization contract (`spec/12_storage-architecture.md:688,:694`). The amendment leaves those contracts unchanged and reconciles the type declaration to them rather than layering a parallel, consumer-less interop requirement on top.
- **Amend §12.6 and §25.3 together.** They declare independent types (`Event` versus `OperationalEvent`) in independent packages, and each independently carries the single-envelope inline-OCSF mandate. Amending only one leaves the other contradictory with the reconciled section.
- **Keep the inline-OCSF model, the `application/ocsf+json` value, and the retranscribe byte-identity requirement unchanged.** The native struct already satisfies each deterministically. The amendment reconciles the type declaration to those contracts rather than weakening the contracts to fit the SDK's string-wrapping.
- **Do not embed the transient go-sdk version detail into the spec.** The spec comment states that the go-sdk type is not aliased because the released SDK serializes `+json` data as an escaped JSON string, without pinning the specific released version or the RFC 6839 mechanism, which age out once the upstream fix ships in a tagged release.
- **No behavioral code change.** Both structs already emit `data` inline and pass the existing round-trip and `datacontenttype` tests. The only code edits are lockstep doc-comment updates in the two packages, plus a new inline-OCSF contract test and a determinism check.
- **Reach.** The change reaches tier 0 and tier 1 for the two packages, tier 3 (wire contract) for the new inline-OCSF byte guard, and tier 11 (spec/code consistency). The change touches no reconciler, datastore, or cluster path.

## 3. The two envelope types and the byte-level obligation they already carry

Both envelope types serialize as CloudEvents v1.0.2 structured content: a single JSON object carrying the context attributes, the lenny-prefixed extension attributes flattened to top-level keys, and the event payload under `data`. For an audit-bearing event the payload is the OCSF v1.1.0 record and `datacontenttype` is `application/ocsf+json`; the record sits inline in `data` as a JSON object, with no intermediate container between the envelope and the record. This is the single-envelope inline model that both §12.6 (`spec/12_storage-architecture.md:682`) and §25.3 (`spec/25_agent-operability.md:649`) already require.

The alias mandate cannot honor that model on the released go-sdk. The go-sdk gates inline JSON serialization on an `isJSON` content-type check that does not accept the RFC 6839 `+json` structured suffix, so `application/ocsf+json` data is written as an escaped JSON string. The OCSF record then appears on the wire as a quoted string rather than an object, double-wrapping it. The two shipped native structs avoid this by holding the payload as `json.RawMessage` and emitting it inline (`pkg/gateway/storage/eventbus/cloudevents.go:108,:136-138`; `pkg/events/types.go:76`).

The retranscribe worker re-serializes the CloudEvents envelope from the canonical Postgres tuple with the same `id` / `source` / `time` attributes and the same `application/ocsf+json` payload, so the envelope is byte-identical to the original publish attempt and downstream de-duplication by CloudEvents `id` continues to work (`spec/12_storage-architecture.md:688,:694`). This is a determinism property of the marshaler over a fixed tuple. The de-duplication key is the CloudEvents `id`, so the guard the amendment introduces is that the marshaler produces equal bytes for equal input, provable by marshaling the same envelope twice and comparing, rather than by pinning a canonical key ordering the map-based marshaler does not contract.

## 4. Proposed changes

### S1. Amend the §12.6 EventBus `Event` type declaration from the go-sdk alias to a native structured-content struct

**Target:** `spec/12_storage-architecture.md`, §12.6 "Interface Design", EventBus subsection, the `Event` type block (`spec/12_storage-architecture.md:657-659`).

**Anchor and change:** Replace the alias declaration and its comment:

```go
// Event is a CloudEvents v1.0.2 envelope. The `go-sdk` cloudevents.Event type
// is used directly; no Lenny-specific wrapper.
type Event = cloudevents.Event
```

with a native structured-content struct whose fields are exactly the envelope-contract table attributes (`spec/12_storage-architecture.md:671-680`) plus an inline payload field:

```go
// Event is the CloudEvents v1.0.2 structured-content envelope. It is a
// native struct rather than an alias of the go-sdk cloudevents.Event
// type: the released go-sdk serializes application/ocsf+json data as an
// escaped JSON string, which would double-wrap the audit record and
// violate the single-envelope inline model below. MarshalJSON emits
// `data` inline and flattens the Lenny extension attributes into the
// top-level object; UnmarshalJSON reverses both.
type Event struct {
    SpecVersion     string            `json:"specversion"`     // "1.0"
    ID              string            `json:"id"`              // per the envelope-contract table below
    Source          string            `json:"source"`
    Type            string            `json:"type"`
    Time            string            `json:"time"`
    DataContentType string            `json:"datacontenttype"`
    Subject         string            `json:"subject"`
    Data            json.RawMessage   `json:"data,omitempty"`  // inline payload; the OCSF record for an audit-bearing event
    Extensions      map[string]string `json:"-"`               // lenny* extensions, flattened onto the wire
}
```

The block continues to reference the envelope-contract attribute table (`spec/12_storage-architecture.md:671-680`), the single-envelope inline model (`:682`), and the retranscribe byte-identity contract (`:688`) rather than restating them.

**Rationale:** The alias and its comment are the false claim: the released go-sdk does not honor the RFC 6839 `+json` suffix, so aliasing to `cloudevents.Event` serializes the audit-bearing OCSF record as an escaped JSON string, contradicting the same section's single-envelope inline model (`:682`) and byte-identical retranscribe contract (`:688`). The shipped native struct (`pkg/gateway/storage/eventbus/cloudevents.go:80-143`) emits `data` inline and is wire-correct. Codifying it removes the contradiction without a new dependency or an unreleased-commit pin. The struct field set mirrors the envelope-contract table verbatim so the two do not drift; the comment states why the go-sdk type is not aliased without pinning the transient released-version detail.

### S2. Amend the §25.3 `OperationalEvent` type declaration to the parallel native struct, cross-referencing §12.6

**Target:** `spec/25_agent-operability.md`, §25.3 "Gateway-Side Ops Endpoints", Event Emission subsection, the `OperationalEvent` block (`spec/25_agent-operability.md:652-653`).

**Anchor and change:** Replace the alias declaration and its comment:

```go
// OperationalEvent is a CloudEvents v1.0.2 Event — see §12.6.
type OperationalEvent = cloudevents.Event
```

with a native struct paralleling S1, cross-referencing §12.6 for the reason the go-sdk type is not aliased:

```go
// OperationalEvent is a native CloudEvents v1.0.2 structured-content
// envelope — see §12.6 for the Event type it mirrors and the reason the
// go-sdk type is not aliased directly. MarshalJSON emits `data` inline
// and flattens the Lenny extension attributes into the top-level object.
type OperationalEvent struct {
    ID              string            `json:"id"`
    Source          string            `json:"source,omitempty"`
    SpecVersion     string            `json:"specversion"`
    Type            string            `json:"type"`
    Subject         string            `json:"subject,omitempty"`
    Time            time.Time         `json:"time"`
    Severity        string            `json:"severity,omitempty"`        // lenny severity extension the event-buffer query filters on
    DataContentType string            `json:"datacontenttype,omitempty"`
    Data            json.RawMessage   `json:"data,omitempty"`            // inline payload; the OCSF record for an audit-bearing event
    Extensions      map[string]string `json:"-"`                         // remaining lenny* extensions, flattened onto the wire
}
```

Keep the `EventEmitter` interface block (`spec/25_agent-operability.md:655-657`) and the single-envelope paragraph (`:649`) unchanged.

**Rationale:** §25.3 independently mandates `type OperationalEvent = cloudevents.Event` and independently carries the single-envelope no-double-wrapping mandate (`:649`). It is a distinct type in `pkg/events`, shipped natively (`pkg/events/types.go:42-88`) with the same inline-payload construction. Amending only §12.6 would leave this alias contradictory with the reconciled §12.6. The struct mirrors `pkg/events/types.go` field-for-field, including the `severity` extension the event-buffer query filters on, so codifying it introduces no new spec/code divergence.

### S3. Sync the two package doc comments that describe the code as diverging from the spec's alias

**Target:** `pkg/gateway/storage/eventbus/cloudevents.go` (`Event` type comment, `:75-79`); `pkg/events/types.go` (`OperationalEvent` type comment, `:34-41`).

**Anchor and change (`pkg/events/types.go:34-41`):** Rewrite only the stale clause that asserts the spec mandates the alias, preserving the reason the go-sdk is not vendored. Replace:

```go
// OperationalEvent is the §25.3 / §12.6 operational event: a
// CloudEvents v1.0.2 record. The spec illustrates the Go type as
// `type OperationalEvent = cloudevents.Event`; the CloudEvents go-sdk
// is deliberately not vendored (see pkg/gateway/eventbus/cloudevents.go
// for the same decision on the §12.3.7 audit envelope), so the envelope
// is modeled natively here with the exact CloudEvents context-attribute
// contract and marshals to the CloudEvents structured-content JSON wire
// format. spec: §25.3 lines 654-659 / §12.6.
```

with:

```go
// OperationalEvent is the §25.3 / §12.6 operational event: a
// CloudEvents v1.0.2 record. §25.3 codifies this native structured-
// content struct rather than an alias of the go-sdk cloudevents.Event
// type (see pkg/gateway/eventbus/cloudevents.go for the same decision on
// the §12.3.7 audit envelope), because the released go-sdk serializes
// application/ocsf+json data as an escaped JSON string and would
// double-wrap the audit record. The envelope is modeled natively here
// with the exact CloudEvents context-attribute contract and marshals to
// the CloudEvents structured-content JSON wire format. spec: §25.3 /
// §12.6.
```

**Anchor and change (`pkg/gateway/storage/eventbus/cloudevents.go:75-79`):** Rewrite only the stale factual sentence and leave the `§12.3.7` section citations on the first and last comment lines untouched (see Non-goals on the pre-existing `§12.3.7` drift). Replace:

```go
// Event is a §12.3.7 CloudEvents v1.0.2 envelope. The spec uses the
// cloudevents go-sdk Event type directly; this struct is the equivalent
// JSON-mode structured-content envelope (the go-sdk dependency is not
// vendored, so the envelope is modeled natively here with the exact
// §12.3.7 context-attribute contract).
```

with:

```go
// Event is a §12.3.7 CloudEvents v1.0.2 envelope. §12.6 codifies this
// native structured-content struct rather than an alias of the go-sdk
// cloudevents.Event type, because the released go-sdk serializes
// application/ocsf+json data as an escaped JSON string and would
// double-wrap the audit record. The struct implements the exact
// §12.3.7 context-attribute contract and emits `data` inline.
```

**Rationale:** After S1 and S2 the spec codifies the native struct, so the two comments' clauses that assert a spec-mandated alias (`cloudevents.go:75-76` "The spec uses the cloudevents go-sdk Event type directly"; `types.go:35-36` "The spec illustrates the Go type as `type OperationalEvent = cloudevents.Event`") describe a divergence that no longer exists and would attribute a false claim to the spec. Each edit rewrites only the stale factual clause and preserves the why-not-aliased rationale. The `cloudevents.go` comment cites the phantom `§12.3.7` (the section header at `§12.3` is "Postgres HA Requirements" and has no subsection 7), which is cited throughout the eventbus package; re-pointing only the type block to §12.6 would leave the surrounding field, `Validate`, and `NewEvent` comments still on `§12.3.7`, a citation state more inconsistent than the status quo. This proposal therefore leaves the `§12.3.7` citations in that comment untouched and confines S3 to the stale alias claim, deferring the package-wide `§12.3.7` repair to its own change (Non-goals). This is comment-accuracy only; it changes no code path.

### S4. Add a contract test pinning each envelope's inline-OCSF wire form and marshaler determinism

**Target:** `tests/tier3_contract/cloudevents/cloudevents_test.go` (eventbus `Event`); a package-level test under `pkg/events` (`OperationalEvent`); `pkg/gateway/storage/eventbus/cloudevents_test.go` (marshaler determinism).

**Anchor and change:** Extend the existing tier-3 audit-bearing content-type coverage (`tests/tier3_contract/cloudevents/cloudevents_test.go`, `TestCloudEventsAuditBearingContentType` around `:159-185`) with a byte-level inline-OCSF assertion: marshal an audit-bearing `Event`, unmarshal into a flat `map[string]json.RawMessage`, and assert that `flat["data"]` parses as a JSON object (`map[string]any`). A double-wrapped payload surfaces `data` as a JSON string and fails the object assertion. Add the parallel assertion for `pkg/events` `OperationalEvent` in a `pkg/events` test. Add a determinism check that marshals the same envelope twice and asserts equal bytes, sited alongside the eventbus marshaler tests so it guards the retranscribe byte-identity property directly. Do not add a golden-file byte-equal document: CloudEvents structured content imposes no key order, both marshalers end in `json.Marshal(map[string]any)` (alphabetical key order is a Go map artifact), and a golden file would pin an implementation detail rather than the spec's de-duplication-by-`id` contract.

**Rationale:** The amendment makes the inline-OCSF wire form a normative property of both native structs. Existing tests assert the `datacontenttype` value and a Marshal→Unmarshal→field-compare round trip, but none asserts at the byte level that the OCSF record is emitted inline (un-escaped), which is the exact property the SDK alias would break. Per the test-coverage rule a wire-contract behavior reaches tier 3. The determinism check pins the retranscribe byte-identity property as a marshaler property (equal input yields equal bytes), matching how the spec de-duplicates (by CloudEvents `id`), without over-constraining key order.

## 5. Non-goals

- **Not adding the cloudevents go-sdk dependency and switching to the alias (Direction B).** Recorded here as the rejected alternative and as an open decision: the released go-sdk double-wraps `application/ocsf+json` data, and the `+json`-honoring fix is unreleased (go-sdk main only, as of 2026-07-02), so Direction B requires either accepting double-wrapped audit data or pinning the module to an unreleased upstream commit.
- **Not changing the single-envelope inline-OCSF model, the `datacontenttype: application/ocsf+json` value, or the retranscribe byte-identical re-serialization requirement.** The amendment reconciles the type declaration to those contracts, which the native struct already satisfies.
- **Not changing any producer or consumer behavior.** The native structs already emit `data` inline and are wire-correct; no store, publisher, subscriber, or `NewEvent` / `Validate` / `MarshalJSON` logic changes. The code edits are comment-accuracy only.
- **Not repairing the pre-existing `§12.3.7` code-comment citation drift in the eventbus package.** The eventbus package doc comments cite a non-existent `§12.3.7` throughout (`cloudevents.go` lines 3, 17, 31, 36, 46, 50, 65, 70, 75, 79, and further, plus `retranscribe.go` and `eventbus_test.go`). S3 rewrites only the stale alias claim in the `Event` type comment and leaves the `§12.3.7` citations in place, because re-pointing only the type block would leave the rest of the package citing `§12.3.7` inconsistently. The package-wide `§12.3.7` repair is a separate change tracked elsewhere.

## 6. Testing

The change reaches tier 0 and tier 1 for `pkg/gateway/storage/eventbus` and `pkg/events`, tier 3 for the inline-OCSF wire guard, and tier 11 for spec/code consistency. The tests below each pin a behavior the amendment introduces or a drift it closes, and each asserts the non-happy path.

- **tier-3 wire contract (inline-OCSF byte form, eventbus `Event`, spec-named-failure path):** In `tests/tier3_contract/cloudevents/cloudevents_test.go`, marshal an audit-bearing `Event` (`datacontenttype: application/ocsf+json`), unmarshal into a flat `map[string]json.RawMessage`, and assert `flat["data"]` parses as a JSON object rather than a JSON string. The non-happy path is a regression to string-wrapped `data` (the SDK-alias serialization the spec forbids under the single-envelope model), which surfaces `data` as a quoted string and fails the object assertion. `// spec: 12.6 (single-envelope inline model)`.
- **tier-3 wire contract (inline-OCSF byte form, `OperationalEvent`, spec-named-failure path):** In a `pkg/events` test, marshal an audit-bearing `OperationalEvent` and assert the same top-level-`data`-is-an-object property. The non-happy path is a double-wrapped OCSF record surfacing as a string. `// spec: 25.3 (single-envelope inline model), 12.6 (envelope contract)`.
- **tier-1 marshaler determinism (retranscribe byte-identity, boundary path):** In `pkg/gateway/storage/eventbus/cloudevents_test.go`, marshal one `Event` twice and assert the byte outputs are equal, so a retranscribe re-serialization of the same canonical tuple reproduces a byte-identical envelope and de-duplication by CloudEvents `id` holds. The non-happy path is a non-deterministic marshaler (unstable key or extension ordering) that would break de-dup on re-publish. `// spec: 12.6 (retranscribe byte-identical re-serialization)`.
- **tier-1 extension flattening and round trip (empty and populated paths):** Assert an `Event` and an `OperationalEvent` with no extensions and with the full lenny extension set both flatten to top-level keys and parse back without loss, covering the empty-extension boundary. The existing round-trip tests cover the populated case; the new assertion adds the empty-extension boundary. `// spec: 12.6 (structured-content envelope), 25.3 (operational event)`.
- **tier-11 spec/code consistency (alias-reintroduction, spec-named-failure path):** A consistency check asserts neither §12.6 nor §25.3 declares `= cloudevents.Event`, that both declare a native struct whose fields match the shipped `pkg/gateway/storage/eventbus.Event` and `pkg/events.OperationalEvent`, and that `cloudevents` is absent from `go.mod` and `go.sum`. The non-happy path is a spec that re-introduces the alias while the code ships the native struct (the F-12.6.21 divergence this proposal closes), or a struct field that drifts from the envelope-contract table. `// spec: 12.6 (Event type), 25.3 (OperationalEvent type)`.

## 7. Findings closed on application

- **F-12.6.21** (`BUILD-GAPS.md:22207`, "`Event` type is a native struct, not the spec-mandated `cloudevents.Event` alias") is closed by S1 and S2: the two `= cloudevents.Event` alias mandates are replaced with the native structured-content structs the code already ships, so the spec matches the wire-correct code. S3 brings the two package doc comments into line so they no longer describe a divergence from the spec, and S4 pins the inline-OCSF wire obligation the amendment makes normative.

## 8. Resolved in adversarial review

Adversarial review rounds populate this section as they run.

## 9. Open decisions for review

Both decisions are resolved by the reviewer (2026-07-02); recorded here for traceability.

- **Direction A versus Direction B — resolved: Direction A.** Direction A (codify the native struct) amends the spec to match the shipped, wire-correct code and avoids a new dependency. Direction B (add a `+json`-honoring go-sdk release and switch to the alias) needs no spec change but depends on an upstream release that does not yet exist and takes on a new third-party dependency. The reviewer selected Direction A: codify the native struct as staged in S1–S4. Direction B remains recorded in Non-goals.
- **Depth of the spec comment on the go-sdk rationale — resolved: keep the terse framing.** The amended `Event`-block comment states that the go-sdk type is not aliased because the released SDK serializes `+json` data as an escaped JSON string, without naming the specific released version or the RFC 6839 mechanism, to avoid anchoring the spec to a transient upstream-version detail that changes once the go-sdk fix ships in a tagged release. The reviewer confirmed the terse framing is sufficient; no footnote is added.

## 10. Files touched on application

- `spec/12_storage-architecture.md`: S1 (§12.6 `Event` type declaration).
- `spec/25_agent-operability.md`: S2 (§25.3 `OperationalEvent` type declaration).
- `pkg/events/types.go`: S3 (`OperationalEvent` doc comment).
- `pkg/gateway/storage/eventbus/cloudevents.go`: S3 (`Event` doc comment).
- `tests/tier3_contract/cloudevents/cloudevents_test.go`: S4 (inline-OCSF byte guard for `Event`).
- `pkg/events/` (new or extended test): S4 (inline-OCSF byte guard for `OperationalEvent`).
- `pkg/gateway/storage/eventbus/cloudevents_test.go`: S4 (marshaler determinism check).
- A tier-11 spec/code consistency check location: S4 (alias-reintroduction guard).
