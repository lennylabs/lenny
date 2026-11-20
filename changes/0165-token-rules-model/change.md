# A formal model of the token rules

Status: `In progress`, revision 1, 2026-11-20.

## Problem

Three review passes over the token rules of §13.3 (Credential Flow) found about ten design gaps in two days. Change 0162 found that the playground mint issued an unrestricted bearer when it could not narrow the subject's scope (TOKX-I2). Change 0163 found that an exchange without `scope` issued an unrestricted token, and its review filed five more defects (TOKX-I3 to TOKX-I7). Change 0164 (token exchange narrowing and identity rules) closes those five, and its security review raised two open decisions, O1 (a rotation recorded the replaced token as parent, so a leaked token could revoke its owner's current token) and O2 (a child mint could take another user's identity at full scope), and filed TOKX-I8 to TOKX-I11. Each defect was found by reading one rule against one scenario. Nothing checks the rules together, so a fix to one rule can open a gap in another, as the answer to O1 (SM-9) shows below.

The owner asked on 2026-11-20 for a formal model that states the properties the rules must keep and checks them against every short sequence of operations.

## Scope

The change covers the rules that issue, narrow, delegate, impersonate, and revoke bearer tokens: the RFC 8693 exchange on `POST /v1/oauth/token` (self-rotation, narrowing, exchange by another caller, and `a2a_delegation` child minting), the recursive-revocation request, the platform-admin impersonation mint, the playground mint, and step 5 of the full revoke of a user. The target state is `main` with change 0164 as decided, including the owner's answers SM-9, SM-10, and SM-11 of 2026-11-20.

It leaves out:

- Concurrent orderings: revocation during issuance, a rotation racing a child mint, and write-before-issue across replicas. A TLA+ model of those orderings is a candidate, described under "Limits, and alternatives rejected", and is not a step.
- Audiences, the per-dialect lifetime caps, `authorized_tools`, `session_id`, rate limits, credential-lease issuance (an internal exchange with the narrowing lineage), and the end of an impersonation session.
- Fixing the rules. The open decisions list the smallest rule change for each counterexample; a later change, or a revision of 0164, carries the fixes the owner chooses.

## Terms

| Term | Meaning |
| :-- | :-- |
| Subject token, actor token, caller token | The `subject_token` and `actor_token` of an RFC 8693 exchange, and the `Authorization: Bearer` token that authenticates the request (§13.3 **Client authentication on `/v1/oauth/token`.**). |
| Holder | A user who has a token's bytes. A bearer token authorizes whoever presents it, so a holder of a token can act as that token's `sub`. |
| Reach | The set of tools a `scope` claim admits at the scope gates under §25.1 **Matching.**: an absent claim and `tools:*` admit every tool, `tools:<domain>:*` admits every action of its domain, a claim with a value the canonical form refuses admits none, and any other value admits itself. |
| Broader | A token is broader than another when its reach admits a tool the other's does not. |
| Strictly narrower | The comparison §13.3 **Client authentication on `/v1/oauth/token`.** defines on scope values, as change 0164 states it. "Strictly narrower in reach" compares reaches instead. |
| Lineage | The tree the `parent_jti` column of `issued_tokens` (§12.2) forms. A lineage descendant of a token is a token whose chain of `parent_jti` values reaches it. |
| Derived token | A token an operation issued from another token presented as its subject, its actor, or, for an impersonation bearer, its caller. Derivation and lineage differ where a counterexample below says so. |
| Counterexample | A sequence of operations, found by the Alloy analyzer within the stated scope, that ends in a state where a property is false. |

## High-level design

The change states the properties the token rules must keep as a numbered list ("Properties" below), and checks them with an Alloy 6 model under `tests/formal/tokens/`. The model represents tokens with their claims and their `issued_tokens` row, users who hold tokens, two tenants, and a reduced scope taxonomy that keeps every case of the §25.1 grammar: an absent claim, `tools:*`, a domain wildcard, exact values, a specific tool action, and a value the canonical form refuses. Each operation is a predicate that admits exactly the requests §13.3 admits and produces the token and the revocations §13.3 states. The analyzer explores every sequence of up to six operations over up to three users, two tenants, and six tokens, and either proves a property within that scope or returns a sequence that breaks it.

Each counterexample is confirmed against the text, written as a scenario with alice, bob, and carol in the tenants acme and globex, and listed as an open decision with the smallest rule change that removes it. The model keeps the rules as decided; a check that fails stays failing until the owner chooses a fix and the model's predicate changes with the rule.

Step 0165.S2 checks the same properties against the Token Service code, with Go property tests that generate random sequences of operations against an in-memory issued-token store.

## Requirements

None by identifier. §13.3 is content of the placeholder page TOKX and carries no requirement identifiers yet. The specification lane of 0165.S1 adds the property list to §13.3 after 0164.S1 merges.

## Properties

The list refines the seven properties of the owner's request and adds three. P1 to P7 are the owner's, and P8 to P10 are added, each with its reason. The model checks each property as one or more assertions, named in brackets; `tests/formal/tokens/README.md` maps each assertion to the rule it follows from.

- **P1 Narrowing.** Every scope value an exchange or a child mint issues is canonical and granted by the subject token's scope, or the issued scope is the subject's own [`P1_rule_a`]. No token is broader than its subject token, with an absent claim and `tools:*` read as unrestricted and `tools:<domain>:*` as every action of its domain [`P1_no_broader_than_subject`]. A playground bearer carries a scope inside `playground_allowed_scope` and is never unrestricted [`P1_playground_never_unrestricted`]. An impersonation bearer is no broader than the impersonating caller's token [`P1_impersonation_no_broader_than_caller`]. The refinement checks one more reading: no token is broader than the token its `parent_jti` names [`P1_no_broader_than_lineage_parent`].
- **P2 Tenant.** A token an exchange, a child mint, or a playground mint issues carries the tenant of its subject token, its actor token, and its caller token [`P2_tenant_preserved`]. Only an impersonation by a `platform-admin` caller issues a token in another tenant, and its row names the caller in `act_sub` [`P2_cross_tenant_only_by_impersonation`].
- **P3 Revocation reach.** A revocation request revokes its root and every lineage descendant of the root, and nothing else; a rotation revokes only the token it replaces [`P3_cascade_reaches_lineage`, `P3_nothing_outside_lineage`, `P3_rotation_revokes_only_its_subject`]. Every token derived from the root by narrowing, by an exchange from another caller, or with the root as a child's actor is reached [`P3_derived_sm9`]. A token that replaced such a derived token by rotation is reached as well, so no derived token leaves its ancestors' lineage by rotating [`P3_derived_through_rotation`]. The refinement checks three further derivations: a child minted with the root as its subject [`P3_derived_child_subject`], a playground bearer minted from the root [`P3_derived_playground`], and an impersonation bearer minted with the root as caller [`P3_derived_impersonation`]. Every token with lineage descendants can be named as a revocation root [`P3_every_lineage_root_revocable`]. 0164 open decision O3 adds two checks on the rotation edge: revoking a token that a rotation replaced spares the successor [`O3_revoking_replaced_token_spares_successor`], and revoking a token reaches every token rotated from it [`O3_revocation_reaches_successors`]. The two checks state opposite choices, so each rotation lineage keeps one of them; CX-2 under "Counterexamples" gives the scenario each lineage admits.
- **P4 Who may revoke.** A step revokes a token only when the user presenting it holds a live token with an administrator role, or a live token that carries the identity of the revoked token or of one of its lineage ancestors [`P4_revocation_authority`]. A revoked caller cannot revoke [`P4_revoked_caller_cannot_revoke`]. 0164 open decision O4 adds two checks: a caller without an administrator role revokes only tokens no broader than its own [`O4_narrow_caller_revokes_only_narrower`], and a revocation request revokes only tokens the presenting user holds and their lineage descendants [`O4_revocation_bounded_by_holding`].
- **P5 Identity.** A token issued to a caller of another identity, or a child whose subject's `sub` differs from the actor's, is strictly narrower in reach than its subject token [`P5_other_identity_strictly_narrower`, `P5_child_other_identity_strictly_narrower`]. No operation other than an impersonation gives a user a token of another identity that is broader than a token of that identity the user already held [`P5_no_identity_escalation`]. A child's actor token carries the caller's `sub` [`P5_actor_is_caller`].
- **P6 Playground origin.** No token is issued from a playground bearer as its subject, actor, or caller [`P6_playground_never_a_source`].
- **P7 Delegation depth and `act`.** A child's `delegation_depth` is its actor's plus one, and its `act` claim names the actor's `sub` [`P7_child_depth_and_act`]. Depth never falls along the lineage [`P7_depth_monotone_along_lineage`]. The refinement checks two further readings: depth never falls below the subject token's [`P7_depth_not_below_subject`], which is how rule (b) "rejecting any `delegation_depth` decrement" reads, and every token with a depth above zero names its delegating identity in `act` [`P7_delegated_token_names_actor`].
- **P8 A revoked token authenticates nothing (added).** No mint is authenticated by a revoked caller token [`P8_revoked_caller_cannot_mint`]. Reason: decision 9 of 0164 refuses a revoked caller on the revocation request, and the caller's identity also decides whether a mint must narrow, so the same reasoning covers mints.
- **P9 Lifetime (added).** No token outlives its subject or actor token [`P9_lifetime_bounded_by_subject_and_actor`] or its lineage parent [`P9_lifetime_bounded_by_lineage_parent`], and no impersonation bearer outlives the caller's token [`P9_impersonation_bounded_by_caller`]. Reason: §13.3 **`exp` granularity and parent-expiry handling.** bounds an issued token by its subject and actor. Monotone expiry along the lineage also means an expired root has no live descendants, which bounds the cascade that TOKX-I10 (filed by 0164) is about.
- **P10 Full revoke (added).** A full revoke of a user leaves no live token in the tenant that carries the user's identity or names the user as actor [`P10_full_revoke_reaches_delegations`]. Reason: the full revoke of §11.4 is the remedy 0164 and this change name for every residual of P3, so it has to reach the tokens the revoked user delegated.

## Model results

The model ran on 2026-11-20 with Alloy 6.2.0, the latest release, using `org.alloytools.alloy.dist.jar` from the GitHub release `v6.2.0` (21,062,377 bytes, SHA-256 `6b8c1cb5bc93bedfc7c61435c4e1ab6e688a242dc702a394628d9a9801edb78d`). The release publishes no checksum for the jar: its `Packages` index carries a SHA-256 for the Debian package only, and the GitHub asset carries no digest, so the checksum is recorded rather than verified; the size matches the size the release lists. The jar ran in an `eclipse-temurin:21-jdk` container with no network, with the model and the jar mounted read-only, using the glucose solver under `linux/amd64` emulation. `tests/formal/tokens/run.sh` runs the same commands.

Scopes:

- **Base.** Up to 3 users, 2 tenants, 6 tokens, and 6 operations (7 states), with a 4-bit integer width for depth and expiry. Every `run` and every base `check` uses it.
- **Large.** Up to 4 users, 2 tenants, 8 tokens, and 8 operations (9 states), for the properties that hold at the base scope, against the build.

Every witness (`show_*`) found its scenario at the base scope: a sign-in, a rotation, a narrowing of an unscoped token, an exchange by another caller, a child mint, a child mint with another user's subject, a revocation that cascades, a revocation by an administrator, a cross-tenant impersonation, a playground mint from a scoped subject, a full revoke that revokes, and a leak. No property holds vacuously.

Results against 0164.S1 as built (`RotInherit`), with the variants where they differ:

| Property | Assertion | Base scope | Large scope |
| :-- | :-- | :-- | :-- |
| P1 | `P1_rule_a` | holds | LARGE-1 |
| P1 | `P1_no_broader_than_subject` | holds | LARGE-2 |
| P1 | `P1_impersonation_no_broader_than_caller` | holds | LARGE-3 |
| P1 | `P1_no_broader_than_lineage_parent` | fails (CX-6) | |
| P1 | `P1_playground_never_unrestricted` | holds | LARGE-4 |
| P2 | `P2_tenant_preserved` | holds | LARGE-5 |
| P2 | `P2_cross_tenant_only_by_impersonation` | holds | LARGE-6 |
| P3 | `P3_cascade_reaches_lineage` | holds in every variant | LARGE-7 |
| P3 | `P3_nothing_outside_lineage` | holds in every variant | LARGE-8 |
| P3 | `P3_rotation_revokes_only_its_subject` | holds in every variant | |
| P3 | `P3_derived_sm9` | holds in every variant | |
| P3 | `P3_derived_through_rotation` | holds for `RotInherit` and `RotEdge`; fails for `RotCut` (CX-1) | |
| P3 | `P3_derived_child_subject` | fails in every variant (CX-12) | |
| P3 | `P3_derived_playground` | fails in every variant (CX-3) | |
| P3 | `P3_derived_impersonation` | fails in every variant (CX-4) | |
| P3 | `P3_every_lineage_root_revocable` | fails in every variant (CX-5) | |
| P3 | `O3_revoking_replaced_token_spares_successor` | holds for `RotInherit` and `RotCut`; fails for `RotEdge` (CX-2) | |
| P3 | `O3_revocation_reaches_successors` | fails for `RotInherit` and `RotCut` (CX-2); holds for `RotEdge` | |
| P4 | `P4_revocation_authority` | holds in every variant | LARGE-9 |
| P4 | `P4_revoked_caller_cannot_revoke` | holds in every variant | LARGE-10 |
| P4 | `O4_narrow_caller_revokes_only_narrower` | fails in every variant (CX-13) | |
| P4 | `O4_revocation_bounded_by_holding` | holds in every variant | |
| P5 | `P5_other_identity_strictly_narrower` | fails (CX-7) | |
| P5 | `P5_child_other_identity_strictly_narrower` | fails (CX-7) | |
| P5 | `P5_no_identity_escalation` | holds | LARGE-11 |
| P5 | `P5_actor_is_caller` | holds | LARGE-12 |
| P6 | `P6_playground_never_a_source` | holds | LARGE-13 |
| P7 | `P7_child_depth_and_act` | holds | LARGE-14 |
| P7 | `P7_depth_monotone_along_lineage` | holds | LARGE-15 |
| P7 | `P7_depth_not_below_subject` | fails (CX-8) | |
| P7 | `P7_delegated_token_names_actor` | fails (CX-9) | |
| P8 | `P8_revoked_caller_cannot_mint` | fails (CX-10) | |
| P9 | `P9_lifetime_bounded_by_subject_and_actor` | holds | LARGE-16 |
| P9 | `P9_impersonation_bounded_by_caller` | fails (CX-4) | |
| P9 | `P9_lifetime_bounded_by_lineage_parent` | holds | LARGE-17 |
| P10 | `P10_full_revoke_reaches_delegations` | fails (CX-11) | |

The O3 scenarios are `run` commands: `O3_leak_holder_revokes_owner_successor` is impossible for `RotCut` and `RotInherit` and found for `RotEdge`, and `O3_leak_holder_keeps_successor` is found for `RotCut` and `RotInherit` and impossible for `RotEdge`.

The base checks took between a few seconds and about 35 minutes each on a shared laptop; the derivation checks that follow rotation chains (`P3_derived_through_rotation`) are the slowest.

## Counterexamples

Each scenario restates the trace Alloy returned, with the users renamed alice, bob, and carol and the tenants acme and globex, and the scope values written in the §25.1 grammar. Each was confirmed against the text of 0164.S1 before it was listed. The open decision that answers it is named in brackets.

### CX-1 A derived token leaves its ancestor's lineage by rotating

`P3_derived_through_rotation` fails for `RotCut` and holds for `RotInherit` and `RotEdge`. alice in acme holds a root token R. She narrows it for her agent, so the narrowed token N records R as parent, and the agent rotates N into N'. Under decision 7 of 0164 as first written, N' records no parent, so when alice revokes R the cascade revokes N, which is already revoked, and not N'. 0164.S1 as built closes it: N' records N's own parent, R, so the cascade from R reaches it. Option (a) of 0164 O3 closes it as well, because N' records N, which records R. The entry needs no decision of its own.

### CX-2 Rotation edge and leaked tokens

0164 open decision O3 asks whether a rotation records the token it replaces. The model checks three lineages for a rotation: decision 7 of 0164 as first written (`RotCut`, no parent), 0164.S1 as built (`RotInherit`, the replaced token's own parent, so none for a root's successor), and option (a) of O3 (`RotEdge`, the replaced token as parent).

| Check | `RotCut` | `RotInherit` (built) | `RotEdge` (O3 (a)) |
| :-- | :-- | :-- | :-- |
| (i) `O3_revoking_replaced_token_spares_successor` | holds | holds | fails |
| (i) scenario `O3_leak_holder_revokes_owner_successor` | impossible | impossible | found |
| (ii) `O3_revocation_reaches_successors` | fails | fails | holds |
| (ii) scenario `O3_leak_holder_keeps_successor` | found | found | impossible |
| `P3_derived_through_rotation` (CX-1) | fails | holds | holds |
| `P4_revocation_authority` | holds | holds | holds |

Question (i), whether the holder of a leaked token that its owner rotated away from can revoke the owner's live tokens: under `RotCut` and `RotInherit`, no. Under `RotEdge`, yes, when the holder also holds a live token that carries the owner's identity. The trace: alice in acme holds tokens T and U, and U leaks to bob. alice rotates T into T', which revokes T and, under `RotEdge`, records T as the parent of T'. T then leaks to bob as well. bob sends a revocation request with U as caller and T as `subject_token`: U carries alice's `sub` and is live, decision 9 of 0164 accepts a revoked root and cascades, and T' is revoked. A holder who has only T cannot do this in any variant, because decision 9 refuses a revoked caller, and `P4_revocation_authority` holds in every variant: every revocation is made by a user holding a live administrator token or a live token of an identity on the revoked token's lineage. The live token can be any token of alice's, including a narrowed child an agent holds (CX-13), or one bob derived from T before the rotation.

Question (ii), whether a leaked token's holder keeps a live token after the owner revokes the leaked one: under `RotCut` and `RotInherit`, yes. The trace: alice holds T and U, T leaks to bob, bob rotates T into T' before alice acts, which revokes T, and alice then revokes T with U as caller. T' records no link to T (for a root T it has no parent at all), so the cascade does not reach it and bob keeps T' until its `exp`, at most T's `exp` and the dialect cap. Under `RotEdge` the cascade reaches T'.

No lineage passes both questions, because both scenarios are the same sequence seen from the two holders: a token is rotated by one holder and revoked by another, and the Token Service cannot tell which holder is the owner, since each presents a live token of the owner's identity. The choice is between a denial of service by a holder of the owner's identity under `RotEdge` and continued access by such a holder under the other two. [Open decision 1.]

### CX-3 A playground bearer outlives the revocation of its subject

`P3_derived_playground` fails in every variant. alice in acme pastes her token S into the playground in `apiKey` mode and receives a playground bearer P with S's `sub` and the narrowed scope. alice then revokes S with a revocation request. P has no `issued_tokens` row and no parent, so the cascade does not reach it, and P stays valid until its `exp` (at most `auth.capabilityTokenTtlSeconds`, and never later than S's `exp`), or until alice logs out of the playground or is fully revoked. The text names logout, user invalidation, and expiry as the routes that end a playground bearer (§27.3.1) and does not say that revoking the subject leaves it live. [Open decision 2.]

### CX-4 An impersonation bearer outlives and escapes the caller's token

`P3_derived_impersonation` and `P9_impersonation_bounded_by_caller` fail in every variant. carol, a `platform-admin` in globex, starts an impersonation of alice in acme with her token C. The bearer's `exp` is the start time plus `durationSeconds`, with no bound by C's `exp`, so the bearer can outlive C. When carol's token C is revoked with a revocation request, the bearer stays live, because its row records no parent. §13.3 **Platform-admin impersonation.** rule 4 lists the routes that end a session, and the revocation of the caller's token is not among them; the text is consistent, and the scenario shows a consequence it does not state. [Open decision 3.]

### CX-5 An upstream ID token cannot be named as a revocation root

`P3_every_lineage_root_revocable` fails in every variant. alice in acme signs in, receives an ID token I from the identity provider, and exchanges it for a narrowed token N; N records I's `jti` as its parent. A revocation request with I as `subject_token` is refused with `400 invalid_grant`, because I has no `issued_tokens` row (G3), so the cascade from I cannot run, although §13.3 **Lineage of derived tokens.** says that revoking a token revokes every token derived from it. alice can revoke N itself, but not a token bob derived from I if bob held I. [Open decision 4.]

### CX-6 A child is broader than its actor

`P1_no_broader_than_lineage_parent` fails. alice holds an unrestricted token S and a narrowed token A with scope `tools:me:read`. She mints a child with S as subject and A as actor and caller; the child's scope comes from S, so with `scope` naming `tools:sessions:scale` the child admits a tool A does not, while its `parent_jti` and `act` name A. No user gains reach, because alice holds S. [Open decision 5.]

### CX-7 Strictly narrower by values admits a copy at equal reach

`P5_other_identity_strictly_narrower` and `P5_child_other_identity_strictly_narrower` fail. alice holds a token S with scope `tools:sessions:* tools:me:write` and caller type `human`, and a second token C of her own with caller type `agent`. C is a different caller under §13.3, because the caller types differ. With C as caller and S as subject she requests `tools:sessions:read tools:sessions:write tools:sessions:scale tools:me:write`. The request does not grant `tools:sessions:*`, so it is strictly narrower by values, and it is accepted; its reach equals S's when `read`, `write`, and `scale` are every action the `sessions` tools declare. The same holds for bob as caller when he holds S. `P5_no_identity_escalation` holds, so no user gains reach the tokens it held did not already give. [Open decision 6.]

### CX-8 A child's depth falls below its subject's

`P7_depth_not_below_subject` fails. alice's agent holds a child token D of depth 2, and alice holds her root R of depth 0. With D as subject and R as actor and caller she mints a child of depth 1, carrying D's scope or less. Rule (b) says an exchange rejects any `delegation_depth` decrement, and the parameter table sets a child's depth from the actor. [Open decision 7.]

### CX-9 A delegated token loses its `act` claim

`P7_delegated_token_names_actor` fails. alice mints a child C (depth 1, `act` naming alice, `typ` `a2a_delegation`) and narrows it with C as caller and subject. The narrowed token has depth 1 and `typ` `a2a_delegation`, and no `act` claim, because the claim table sets `act` only when `actor_token` is present. [Open decision 8.]

### CX-10 A revoked caller token authenticates a mint

`P8_revoked_caller_cannot_mint` fails. alice rotates her root T into T', which revokes T, and then narrows T' with T as caller. The mint is accepted, because the Token Service does not read the caller's revocation on a mint (G4). The holder of T gains nothing T' does not give, but decision 9 of 0164 refuses the same caller on a revocation request. [Open decision 9.]

### CX-11 A full revoke misses the delegations the user made for another identity

`P10_full_revoke_reaches_delegations` fails. bob in acme holds a token B of his own and a token A of alice's that she shared with him. With A as actor and caller and B as subject, bob mints a child C at a strictly narrower scope: C carries bob's `sub`, its `act` names alice, and its parent is A. A tenant administrator then fully revokes alice. Step 5 of §11.4 revokes A, whose `sub` is alice, and not C, whose `sub` is bob, so a token naming alice as its delegator stays live after her full revoke. [Open decision 10.]

### CX-12 A child minted from a subject survives the subject's revocation

`P3_derived_child_subject` fails in every variant. alice mints a child with her token S as subject and her token A as actor; the child's parent is A. Revoking S does not reach the child. 0164 decision 8 settled the case of a subject of another `sub` by requiring strict narrowing and rejected a second lineage edge; the model shows that the same holds for a subject of the actor's own `sub`. The child's `exp` is bounded by S's. No decision is proposed; the entry records the residual.

### CX-13 Any token of the user may revoke the user's tokens (0164 O4)

0164 open decision O4 asks whether any token of the subject's user may send the revocation request. `O4_narrow_caller_revokes_only_narrower` fails in every variant: alice's token with a narrow scope (in the trace, one whose scope admits no tool at all) revokes her unrestricted token, because decision 9 compares `sub` and roles and not scope. `O4_revocation_bounded_by_holding` holds in every variant: a revocation request revokes only tokens whose bytes the presenting user holds and their lineage descendants. That confirms the premise of option (a) of O4, that a narrow token's holder can only revoke what it already holds, with one interaction: under `RotEdge` the lineage descendants of a held token include its rotation successors, so the holder of a narrow token of alice's and of a token alice rotated away from can revoke alice's current token, as question (i) of CX-2 shows. Option (b) of O4, which accepts the user's own caller only when it is the subject token or one of its ancestors, would block that, and would also block alice's own cleanup of a leaked root under decision 9: once the root is revoked, no live token is the root or its ancestor, so only an administrator could cascade from it. [Open decision 11.]

### Properties that hold

Rule (a), the subject bound on every derived token's reach, the playground ceiling, and the impersonation bound by the caller's scope hold (P1). Tenant preservation holds, and only a platform-admin impersonation crosses tenants (P2). The cascade reaches the whole lineage and nothing outside it, and every token derived by narrowing, by another caller's exchange, or as a child's actor is reached (P3). Every revocation is made by a user holding a live administrator token or a live token of an identity on the revoked token's lineage, and a revoked caller cannot revoke (P4). No operation other than an impersonation gives a user more of another identity's reach than a token of that identity the user already held, and a child's actor is the caller (P5). No token is issued from a playground bearer (P6). A child's depth is its actor's plus one and its `act` claim names the actor, and depth never falls along the lineage (P7). No token outlives its subject, its actor, or its lineage parent (P9).

## Current state

The model follows the text of 0164.S1. Reading the code it describes, in the branch `change/0164.S1-token-exchange-identity-rules` on 2026-11-20, found these differences between the code and the text, or rules the text leaves to the code. None changes a model result unless it says so.

- **G1 Default audience.** The §13.3 parameter table names `lenny-gateway` as the default `audience`. `buildExchangeRequest` in `pkg/tokenservice/tokenservice.go` passes the request's `audience` through, and `Validate` in `pkg/tokenexchange/tokenexchange.go` issues the requested list, so a request without `audience` issues a token with no `aud` claim. A gateway verifier configured with audiences refuses such a token (`pkg/auth/jwt/claimchecker.go`). `isSelfRotation` also requires the issued audience to equal the subject's, so a self-exchange without `audience` from a subject that carries one is a narrowing that leaves the subject live rather than a rotation.
- **G2 Revocation of a playground bearer.** §13.3 **Client authentication on `/v1/oauth/token`.** says the recursive-revocation request accepts a `subject_token` with the `origin: "playground"` claim. A playground bearer has no `issued_tokens` row, and `RevokeCascadeByCaller` in `pkg/storage/issuedtokenstore/issuedtokenstore.go` returns `ErrNotFound` for a root without a row, so the request is refused with `400 invalid_grant`. The model follows the code.
- **G3 Revocation of an upstream ID token.** The text does not say what a revocation request does with a root that has no `issued_tokens` row. The code refuses it as G2 states. The counterexample of `P3_every_lineage_root_revocable` follows from this.
- **G4 Caller revocation on a mint.** The Token Service checks the caller token's signature and origin on a mint (`authenticateCaller`), and reads its revocation only on the revocation request. The text is silent for a mint. The model follows the code, and `P8_revoked_caller_cannot_mint` fails.
- **G5 Roles of an exchanged token.** The claim table of §13.3 does not list `roles`. `mintToken` writes none, the playground mint copies the subject's (§10.2 invariant 5 bounds them by the subject's), and the impersonation bearer carries the target user's. The model lets an exchange issue any subset of the subject's roles, which covers the code and every reading the text admits.
- **G6 Scope taxonomy.** §10.2 and §25.1 pin `playground_allowed_scope` to values in the domains `sessions`, `runtimes`, and `pools`, which the closed domain list of §25.1 **Scope value syntax.** does not contain. `scopes.Domains` in `pkg/common/scopes/scopes.go` adds `runtimes` and `pools` with a comment saying they sit outside the §15.1 taxonomy, and carries further domains, such as `sessions`, `impersonation`, `issued_token`, and `rbac`, that the §25.1 list, which says it mirrors §15.1, does not name.
- **G7 Caller type of an impersonation bearer.** `sign` in `pkg/credential/impersonation/service.go` stamps `caller_type: human` whatever the target user's caller type. §13.3 **Platform-admin impersonation.** rule 1 does not state it.

## Limits, and alternatives rejected

- **Bounded scope.** Alloy checks every trace within the scope and proves nothing beyond it. A property that holds at up to four users, two tenants, eight tokens, and eight operations can fail on a longer sequence. The counterexamples are complete scenarios and need no larger scope to be confirmed.
- **Bearer possession.** A bearer token authenticates whoever presents it, so a user who holds another user's token is that user to the Token Service. The identity rules of §13.3 bind the caller token's identity, not its holder: a holder of alice's token who presents it as both caller and subject is the subject's own caller and obtains a copy at full scope, which the different-caller rule would refuse with the holder's own token as caller. `P5_no_identity_escalation` therefore states the holder property: no operation gives a user more of another identity's reach than a token of that identity the user already held. It holds. Proof of possession (RFC 9449 DPoP or mutual TLS sender-constrained tokens) would make the identity rules bind holders, and is outside this change.
- **No concurrency.** Each step is atomic. The orderings that matter for these rules (a revocation committing during an issuance, a rotation racing a child mint, write-before-issue across replicas, and the propagation of revocations to replica caches) need a model with interleaved steps. A TLA+ specification of the Token Service transactions over a shared `issued_tokens` table is the candidate for that, held until the owner has seen these results. It is listed here rather than as a step.
- **No clock.** Expiry is compared only between a token and its sources; tokens do not expire during a trace. A revocation that a token outlives by expiring is not modelled.
- **Abstraction of scopes.** Two domains and three actions stand for the taxonomy. Every counterexample about scopes uses only the cases the grammar distinguishes (absent, `tools:*`, a domain wildcard, an exact value, a specific tool action, and an unparseable value), so a larger taxonomy adds no case.
- **Alternatives rejected.** A TLA+ model of the whole design was set aside for this step: the questions here are about which states the rules admit, which Alloy's relational logic states directly and checks without writing an explicit state machine for every relation. Property tests alone (step 0165.S2) would test the code rather than the design, and would not have compared the rotation lineages of O3 on the same scenarios.

## Specification and non-specification changes

- `tests/formal/tokens/tokens.als`, `tests/formal/tokens/run.sh`, and `tests/formal/tokens/README.md`: the model, its runner, and the map from each predicate and assertion to its rule and property.
- `tests/README.md`: the `formal/` subdirectory.
- `spec/13_security-model.md`, §13.3: the property list, in the specification lane of 0165.S1 after 0164.S1 merges, with the owner's answers to the open decisions.

## Documentation

No reader-facing behavior changes: TOKX. The model changes no behavior, and the property list states invariants the rules of §13.3 already keep or that the open decisions settle. The change that carries a rule fix states its documentation.

## Tests

No tier of `lenny-test` runs the model; it is a design check run by hand with `tests/formal/tokens/run.sh`. Step 0165.S2 adds unit-tier property tests in `pkg/tokenservice` that cite §13.3 and check the properties against the code.
