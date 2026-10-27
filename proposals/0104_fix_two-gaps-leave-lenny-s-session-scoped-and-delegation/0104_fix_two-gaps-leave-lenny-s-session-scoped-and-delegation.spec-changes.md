# Spec changes — Session-capability and delegation child tokens are never minted, signed, or recorded

## Design (as the spec must state it)

A pod holds no Lenny-issued JWT. A pod authenticates to the gateway as the §10.2 boundary table and §10.3 state, and the §13.3 connector-credential flow already bars client tokens from the pod. After these edits the specification states no session-creation mint and no internal delegation mint, so no sentence implies that a pod or a delegated child carries a Lenny token.

The edits establish these rules, each with one normative home:

1. **Session-creation caller check.** Home: §10.2 "Signing topology" (SPEC-1 Edit (b)). §16.7 defines the audit row's `exchange_type: session_mint` value against it (SPEC-5 Edit (g)).
2. **Child identity.** A delegated child's identity is its session row, and its delegation lease bounds what it may do. Home: §8.2 "Child identity" (SPEC-2).
3. **Parent-terminal fence rule.** The transaction that inserts a delegated child row reads the parent session row under a shared lock and refuses the insert when the parent is terminal or absent, with `DELEGATION_PARENT_REVOKED`. Home: §12.8 "Lifecycle fence and drain", *Creation fence* (SPEC-3). §8.2 "Parent freshness" (SPEC-2) and the §15.1 catalog row (SPEC-4) cite it.

The external `a2a_delegation` exchange on `POST /v1/oauth/token` with `actor_token` is unchanged. SPEC-4 and SPEC-5 retarget every site that named internal delegation minting to that exchange, or remove the site when only the internal mint applied.

## Edge cases and accepted failure modes

- **Parent changes live state mid-admission.** A parent that moves from `running` to another non-terminal state between admission and the child insert keeps its child, because the fence refuses only a terminal or absent parent (§12.8, SPEC-3 Edit (a)).
- **Parent row absent or unreadable.** An absent parent refuses the insert with `DELEGATION_PARENT_REVOKED`. A parent read that fails fails the insert (SPEC-3 Edit (a)).
- **Reservation before the insert.** A delegated child's file export reserves the session ID under the tenant and user rules only. The parent rule runs in the insert transaction (SPEC-3 Edit (c)), so a parent that ends between reservation and insert still refuses the insert.
- **Single-bearer revocation during a running session.** Revoking one bearer of the owner does not stop a running session's pod from delegating. A §11.4 full revoke is the cut-off for a delegation tree (SPEC-2 "Parent freshness").
- **Pod-path delegation.** A child that a pod requests over `LNK-GWCONTROL` is outside the caller check (SPEC-1 Edit (b)). Its parent is the session whose ID the adapter presents with the request, and the parent session row governs it (SPEC-2 "Child identity"). The gateway does not bind that session ID to a session assigned to the calling pod. SPEC-1 Edit (f) states no such binding, and the gap is recorded as an `ABSENT` claim-register row (non-spec-changes.md REC-1).
- **Client-facing `lenny/delegate_task` with an impersonation bearer.** The request is refused with `403 FORBIDDEN` after the rejection row is durable (SPEC-1 Edit (b)).
- **KMS unavailable.** Session creation and internal delegation make no `Sign` call, so `KMS_SIGNING_UNAVAILABLE` does not reach either path.

## Staged edits

Several targets are already edited by proposal 0102 SPEC-2. Each edit below locates its target by heading and quoted text as the text reads after 0102 SPEC-2 lands.

### SPEC-1 · spec/10_gateway-internals.md § 10.2 Authentication

Edit (a). In the paragraph that begins "**Session capability context:**", keep the bold label and replace the first sentence "After authentication, the gateway mints a **signed JWT** via a pluggable `JWTSigner` interface." with:

```
Every Lenny-issued JWT is minted through a pluggable `JWTSigner` interface by a component that "Signing topology" below names.
```

Edit (b). In the paragraph that begins "**Signing topology.**", in the sentence that begins "The gateway mints in process, through its `JWTSigner`,", replace

```
, playground bearers, and `session_capability` tokens on the session-creation path.
```

with

```
, and playground bearers.
```

Then, in the same paragraph, replace the two sentences that begin "The gateway records each session-creation `session_capability` token in `issued_tokens`" and end "or with the `500` response that paragraph states when the rejection audit write fails." (the text 0102 SPEC-2 Edit (b) writes) with:

```
Session creation mints no JWT. Every request that creates a session on a bearer presented at the "Client → Gateway" or "Automated clients" boundary of the table above applies step (1a) of [§13.3](13_security-model.md#133-credential-flow) "Write-before-issue ordering" to that bearer as the `subject_token`. This covers a root session and a delegated child that `lenny/delegate_task` creates on the client-facing MCP surface. When step (1a) rejects, the gateway writes the rejection audit row under the rejected-exchange discipline of that paragraph, and the request fails with `403 FORBIDDEN` after that row is durable. When the `issued_tokens` read or the rejection audit write fails, the request fails with `500 INTERNAL_ERROR` and no session is created.
```

Edit (c). Under "**JWT claims:**", in the `expiry` bullet, delete the sentence:

```
The `session_capability` token minted on the session-creation path keeps its per-dialect cap.
```

Edit (d). Under "**JWT claims:**", in the `typ` bullet, replace the sub-bullet that begins "`session_capability` — a session-scoped JWT bound to a specific `session_id`" with:

```
  - `session_capability` — a gateway-issued bearer that an MCP client presents to the gateway. The gateway mints it as the playground bearer on `POST /v1/playground/token` ([§27.3.1](27_web-playground.md#2731-oidc-cookie-to-mcp-bearer-exchange)). Session creation mints no `session_capability` token, and a pod never holds one: a pod authenticates to the gateway as the boundary table above states.
```

Edit (e). In the paragraph that begins "**KMS signing failure mode:**", delete the sentence:

```
A running session's own mints, such as a delegation child token, fail until signing recovers.
```

Edit (f). Delete the whole paragraph that follows the "**KMS signing failure mode:**" paragraph:

```
Pods cannot forge or extend this token. The gateway validates the signature on every pod→gateway request.
```

No sentence replaces it. The paragraph names a token that no pod holds after Edit (d), and the gateway validates no token signature on a pod request. The boundary table and §10.3 already state how a pod authenticates. The gateway does not bind a platform-tool or connector-tool call forwarded over `LNK-GWCONTROL` to a session assigned to the calling pod, and this edit does not specify such a binding. Non-spec-changes.md REC-1 records the gap as an `ABSENT` claim-register row and a BUILD-GAPS finding routed to its own proposal (summary decision D11).

### SPEC-2 · spec/08_recursive-delegation.md § 8.1 and § 8.2

Under item 2 of the numbered delegation flow, replace the three paragraphs whose bold labels are "**Child-token minting (internal RFC 8693 exchange).**", "**Actor-token freshness under concurrent parent rotation/revocation.**", and "**Retry semantics on `AUDIT_CONCURRENCY_TIMEOUT` during child minting.**" with the two paragraphs below. Keep the three-space indentation the replaced paragraphs carry, so the new paragraphs stay inside item 2.

```
   **Child identity.** The gateway mints no token for a delegated child. The child session row carries the child's identity: the parent's user and `tenant_id`, `parent_session_id`, `root_session_id`, `delegation_depth` equal to the parent's plus one, and the granted `LeaseSlice` as its delegation lease, which bounds what the child may do ([§8.3](#83-delegation-policy-and-lease)). The child pod authenticates to the gateway as every pod does ([§10.2](10_gateway-internals.md#102-authentication)). When a pod requests the child over `LNK-GWCONTROL` ([§28.3](28_communication-channels.md#283-registers)), the parent is the session whose ID the adapter presents with the request, and the gateway dispatches the request under that session ID. The [§28.4](28_communication-channels.md#284-claim-register) claim register records that the gateway does not bind that session ID to a session assigned to the calling pod.

   **Parent freshness.** The transaction that inserts a delegated child row runs the creation fence of [§12.8](12_storage-architecture.md#128-compliance-interfaces) "Lifecycle fence and drain", which refuses the insert when the parent session is terminal or has no row and states how the insert serializes with the parent's terminal write. No child session is created and no child pod is allocated. A [§11.4](11_policy-and-controls.md#114-user-invalidation) full revoke cancels every session of the user, delegated children included, and the creation fence refuses a new child of a hard-disabled or fully revoked user.
```

In the §8.1 Design Philosophy paragraph that begins "The gateway enforces three invariants on every delegation hop", replace "enforcement sits in the gateway and the Token Service, not in the agent code" with "enforcement sits in the gateway rather than in the agent code".

### SPEC-3 · spec/12_storage-architecture.md § 12.8 Compliance Interfaces

All three edits are in the *Creation fence* half of the paragraph that begins "**Lifecycle fence and drain.**". Do not append text after the *Drain* half.

Edit (a). After the sentence "A hard-disabled user's marker also rejects delegated child creation ([§11.4](11_policy-and-controls.md#114-user-invalidation)).", insert:

```
For a delegated child, a row whose `delegation_depth` is greater than zero, the transaction that inserts the row also reads the parent session row under a shared row lock, after the tenant and user rows. It refuses the insert when the parent is in a terminal state or has no row. A terminal transition holds an exclusive lock on the session row it changes, so the insert and the parent's terminal write serialize. A child insert that commits first is visible to the parent's `cascadeOnFailure` walk ([§8.10](08_recursive-delegation.md#810-delegation-tree-recovery)), which lists the parent's children from the Postgres primary ([Section 12.3](#123-postgres-ha-requirements) **Read replicas**), and an insert that runs after the terminal write commits is refused. When the parent read fails, the insert fails.
```

Edit (b). After the sentence "On `POST /v1/sessions` and MCP `create_session` these codes are session-creation rejections under [§15.2.1](15_external-api-surface.md#1521-restmcp-consistency-contract) rule 5.", insert:

```
When the parent rule refuses a delegated child and none of those codes matches, `lenny/delegate_task` returns `DELEGATION_PARENT_REVOKED`.
```

Edit (c). In the sentence that contains "first reserves the ID for the session's owner under the same check, in its own transaction", replace "under the same check" with:

```
under the tenant and user rules of the same check (the parent rule applies only in the insert transaction)
```

### SPEC-4 · spec/15_external-api-surface.md § 15.1 REST API and Shared Adapter Types

Every table row edited below stays one physical line.

Edit (a). In the error-code catalog, in the row that begins "| `DELEGATION_PARENT_REVOKED` |", keep the Code, `POLICY`, and `403` cells, and replace the whole description cell, including the 403-versus-409 alignment sentence and the trailing "See" pointers, with:

```
`lenny/delegate_task` rejected because the parent session was in a terminal state or absent when the child session row was inserted ([Section 8.2](08_recursive-delegation.md#82-delegation-mechanism) "Parent freshness"; [Section 12.8](12_storage-architecture.md#128-compliance-interfaces)). No child session is created and no child pod is allocated. `details.parentSessionId` is included. Not retryable.
```

Edit (b). In the error-code catalog, delete the row that begins "| `DELEGATION_AUDIT_CONTENTION` |".

Edit (c). In the REST table, in the row for `POST` `/v1/oauth/token`, replace

```
delegation child-token minting (internal; `actor_token=<parent_session_token>`, narrowed `scope`)
```

with

```
`a2a_delegation` child-token minting (`actor_token` = the delegating caller's token, narrowed `scope`)
```

Edit (d). Under the heading "Shared Adapter Types" (directly under §15), in the `CallerIdentity` comment on the `Act` field, replace

```
    // Act carries the RFC 8693 `act` claim when the session was created via
    // a delegation child token ([Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)).
    // Nil for root sessions. When non-nil, adapters that surface delegation
    // provenance (e.g., A2A `delegator` agent card field) SHOULD render
    // Act.Sub as the delegating actor.
```

with

```
    // Act carries the RFC 8693 `act` claim of the caller's bearer
    // ([Section 13.3](13_security-model.md#133-credential-flow)). Nil when the
    // bearer carries none. When non-nil, adapters that surface delegation
    // provenance (e.g., A2A `delegator` agent card field) SHOULD render
    // Act.Sub as the delegating actor.
```

In the `ActorClaim` comments of the same block, replace "identifies the session's delegating actor" with "identifies the delegating actor of the caller's bearer", replace "SessionID is the parent session's ID." with "SessionID is the delegating actor's session ID.", and replace the whole `DelegationDepth` comment, from "DelegationDepth is the parent's delegation depth;" through "plus one.", with "DelegationDepth is the delegating actor's delegation depth.".

Edit (e). §15.1, in the paragraph that begins "**MCP endpoint boundary.**", in the `/mcp/runtimes/{runtime-name}` bullet, replace "authentication is a session-scoped token" with "authentication is the caller's bearer ([§10.2](10_gateway-internals.md#102-authentication))".

### SPEC-5 · spec/04_system-components.md § 4.3, spec/05_runtime-registry-and-pool-model.md § 5.1, spec/07_session-lifecycle.md § 7.4, spec/10_gateway-internals.md § 10.7, spec/13_security-model.md § 13.3, spec/16_observability.md § 16.5 and § 16.7, spec/17_deployment-topology.md § 17.7 and § 17.8.2, spec/18_build-sequence.md § 18.2, § 18.23, and § 18.40, spec/23_competitive-landscape.md § 23.1, spec/27_web-playground.md § 27.3.1

Edit (a). §4.3 Token Service, item 3 "**Canonical token endpoint**". Replace "Delegation child tokens, credential-lease tokens," with:

```
`a2a_delegation` child tokens, credential-lease tokens,
```

Edit (b). §13.3, the paragraph that begins "Lenny issues bearer tokens through a single canonical endpoint". Replace

```
credential-lease token issuance, delegation child-token minting, operability scope narrowing — go through this endpoint, either by direct caller invocation (the general self-rotation grant) or by internal Token Service calls (credential leasing, delegation minting).
```

with

```
credential-lease token issuance, `a2a_delegation` child-token minting, operability scope narrowing — go through this endpoint, either by direct caller invocation (the general self-rotation grant and `a2a_delegation` child-token minting) or by internal Token Service calls (credential leasing). Internal delegation through `lenny/delegate_task` mints no token ([§8.2](08_recursive-delegation.md#82-delegation-mechanism) "Child identity").
```

Edit (c). §13.3, the RFC 8693 parameter table. In the `subject_token` row, replace "the tenant's root-session token (child minting)" with:

```
the token whose identity the child carries (`a2a_delegation` child minting)
```

In the `actor_token` row, replace "For delegation only: the parent session's token." with:

```
For `a2a_delegation` child minting only: the delegating caller's token.
```

Both rows stay one physical line.

Edit (d). §13.3, replace the opening of the paragraph that begins "Internal token issuance paths":

```
Internal token issuance paths (credential lease minting in [§4.9](04_system-components.md#49-credential-leasing-service), delegation child minting in [§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)) use the same write-before-issue discipline because they all flow through the Token Service's single exchange path.
```

with

```
The internal token issuance path, credential lease minting in [§4.9](04_system-components.md#49-credential-leasing-service), uses the same write-before-issue discipline because it flows through the Token Service's single exchange path.
```

Edit (e). §13.3, the paragraph that begins "Delegation child tokens minted from a parent that is later rotated". Replace its opening "Delegation child tokens" with:

```
`a2a_delegation` child tokens
```

Edit (f). §13.3, the paragraph that begins "**Rate limiting on `/v1/oauth/token`.**". Replace "Internal Token Service calls (credential lease minting, delegation minting) bypass" with:

```
Internal Token Service calls (credential lease minting) bypass
```

Edit (g). §16.7, the `token.exchanged` entry. Replace

```
whether the caller is external (the general self/service-principal rotation grant presenting the current token as `subject_token`) or internal (delegation child-token minting, credential-lease issuance, operability-scope narrowing); and additionally emitted by the gateway, which mints and records the token in process rather than through an RFC 8693 exchange, with `exchange_type: admin_rotation` on its bootstrap admin-credential rotation ([§13.3](13_security-model.md#133-credential-flow)) and `exchange_type: session_mint` on its session-creation `session_capability` mint ([§10.2](10_gateway-internals.md#102-authentication) "Signing topology").
```

with

```
whether the caller is external (the general self/service-principal rotation grant presenting the current token as `subject_token`, or `a2a_delegation` child-token minting with `actor_token`) or internal (credential-lease issuance, operability-scope narrowing); and additionally emitted by the gateway in process rather than through an RFC 8693 exchange: with `exchange_type: admin_rotation` when it mints and records its bootstrap admin-credential rotation ([§13.3](13_security-model.md#133-credential-flow)), and with `exchange_type: session_mint` when the session-creation caller check of [§10.2](10_gateway-internals.md#102-authentication) "Signing topology" refuses a bearer. A `session_mint` row is always a rejection row: it carries `policy_result: rejected:<reason>` and no `issued_token_jti`, because session creation mints no JWT.
```

Leave the payload field list, including the `exchange_type` enumeration, unchanged.

Edit (h). §16.5, in the `TokenStoreUnavailable` row, replace "session creation, delegation minting, and credential leasing are all failing" with "session creation and credential leasing are both failing". The row stays one physical line.

Edit (i). §17.7, in the `TokenStoreUnavailable` runbook *Trigger* line, replace "session creation, delegation minting, and credential leasing fail platform-wide" with "session creation and credential leasing fail platform-wide".

Edit (j). §17.8.2, the paragraph that begins "**KMS Sign rate.**". Replace the formula

```
`session_creation_rate + delegation_rate + Σ 1 ÷ (token_lifetime − refresh_lead) + operator_mints`
```

with

```
`Σ 1 ÷ (token_lifetime − refresh_lead) + operator_mints`
```

Delete the two bullets that begin "- `session_creation_rate` is one `session_capability` mint per created session" and "- `delegation_rate` is one child-token mint per delegation". In the paragraph that follows the bullets, replace "The peak sign rate is the same sum evaluated at the deployment's peak session-creation rate (`burst_arrival_rate` in the gateway `minReplicas` formula above) and its peak delegation rate." with:

```
The peak sign rate is the same sum evaluated at the deployment's peak count of concurrent holders.
```

Edit (k). §18.23, replace the "**Prerequisites.**" line

```
**Prerequisites.** Phase 8 exit gate. The Phase 5.5 `POST /v1/oauth/token` endpoint and `TokenIssuanceStore` are required for child-token minting at every delegation hop; see [§18.40](#1840-hard-prerequisite-chain).
```

with

```
**Prerequisites.** Phase 8 exit gate.
```

Edit (l). §18.40, delete item 7, which begins "7. **Phase 5.5 → Phase 9 (`/v1/oauth/token` endpoint).**", and renumber items 8 onward to 7 onward. In the §18.2 Phase Map, in the row for Phase 9, replace the Hard prerequisite cell "**Phase 5.5** (`/v1/oauth/token`)" with "—". The row stays one physical line.

Edit (m). §5.1, the `publishedMetadata` Field "**Visibility levels:**" list. In the `internal` bullet, replace "requires valid Lenny session JWT" with "requires an authenticated caller ([§10.2](10_gateway-internals.md#102-authentication))". In the `tenant` bullet, replace "filtered by `tenant_id` claim in the JWT" with "filtered by the caller's `tenant_id`".

Edit (n). §27.3.1, in the sentence that begins "The token is a standard gateway-minted session-capability JWT", replace "carrying the same `user_id`, `tenant_id`, `caller_type`, and `expiry` claims as a non-playground session JWT plus" with "carrying `user_id`, `tenant_id`, `caller_type`, and `expiry` claims plus".

Edit (o). §7.4, in the paragraph that begins "**Upload authorization:**", replace "the caller's normal session-scoped bearer credential" with "the caller's bearer ([§10.2](10_gateway-internals.md#102-authentication))".

Edit (p). §13.3, the code block under "**Connector credentials (OAuth):**". Replace "Gateway mints session context" with "Gateway creates the session", and replace "Pod receives: session context + projected SA token" with "Pod receives: projected SA token".

Edit (q). §10.7, the **Caller** row of the table under "**Eval Submission Contract.**". Delete the sentence "This includes: the agent runtime itself (via its session credential), the session owner (user or service account), or a deployer-operated external scorer pipeline." The row stays one physical line.

Edit (r). §23.1, under "Additional Platform Capabilities", item 12 "**Pull-based evaluation hooks.**". Delete " — agent runtime, session owner, or external scorer pipeline", so the sentence ends "from any authenticated principal ([Section 10.7](10_gateway-internals.md#107-experiment-primitives))."

## Spec files touched

- spec/04_system-components.md (SPEC-5)
- spec/05_runtime-registry-and-pool-model.md (SPEC-5)
- spec/07_session-lifecycle.md (SPEC-5)
- spec/08_recursive-delegation.md (SPEC-2)
- spec/10_gateway-internals.md (SPEC-1, SPEC-5)
- spec/12_storage-architecture.md (SPEC-3)
- spec/13_security-model.md (SPEC-5)
- spec/15_external-api-surface.md (SPEC-4)
- spec/16_observability.md (SPEC-5)
- spec/17_deployment-topology.md (SPEC-5)
- spec/18_build-sequence.md (SPEC-5)
- spec/23_competitive-landscape.md (SPEC-5)
- spec/27_web-playground.md (SPEC-5)
