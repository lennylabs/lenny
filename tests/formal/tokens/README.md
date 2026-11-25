# Token rules model

`tokens.als` is an Alloy 6 model of the rules that issue, narrow, delegate, impersonate, and revoke bearer tokens. Change 0165 (`changes/archive/2026-11-23-0165-token-rules-model/`) builds it, and its `change.md` lists the properties in plain words and the counterexamples the model found.

The model is a design check. No tier of `lenny-test` runs it, and no Go test reads it. The generated property tests in `tests/component/tokenproperties` (step 0165.S2) check the properties that §13.3 **Token rule properties.** states against the Token Service and its Postgres issued-token store, under the rules as `main` builds them after change 0167.

## What the model covers

The model states the rules of §13.3 (Credential Flow) as change 0164 built them, with the owner's answers SM-9, SM-10, and SM-11 of 2026-11-20, the scope grammar of §25.1 (**Scope value syntax.** and **Matching.**), the playground mint invariants of §10.2, and step 5 of the full revoke in §11.4.

Change 0167 changed several of those rules after the model ran: a playground bearer is recorded with its pasted subject as parent, an impersonation bearer expires with the caller's bearer and its row names that bearer, a revocation request accepts a root without an `issued_tokens` row, a child is bounded by its actor's scope, an exchange without an actor copies the subject's `act` claim, every mint refuses a revoked caller token, and a full revoke follows the lineage of the user's tokens. The predicates keep the 0164 rules, so the checks reproduce the counterexamples change 0165 records. Change 0167's digest records a rerun of an uncommitted copy of the model changed to its rules.

| Predicate | Operation | Rule it encodes |
| :-- | :-- | :-- |
| `login` | An identity provider issues a root token to a user. | §13.3 **Client authentication on `/v1/oauth/token`.** (upstream OIDC ID token) |
| `exchange` | Self-rotation, narrowing or copying by the subject's own principal, and an exchange by another caller, on `POST /v1/oauth/token` without `actor_token`. | §13.3 **Scope narrowing enforcement.** rule (a), **Tenant-scope enforcement**, **Client authentication on `/v1/oauth/token`.** (playground refusal, different-caller rule), **Write-before-issue ordering.** step (1a), **Token rotation and revocation**, **Lineage of derived tokens.** (SM-7, SM-9) |
| `childMint` | An `a2a_delegation` child mint with `actor_token`. | §13.3 parameter table (`actor_token`, `delegation_depth`, `act`), **Client authentication on `/v1/oauth/token.`** (SM-10), **Lineage of derived tokens.** |
| `revokeReq` | The recursive-revocation request. | §13.3 **Recursive-revocation request authorization.** (SM-11), **Lineage of derived tokens.** |
| `impersonate` | A platform-admin impersonation bearer. | §13.3 **Platform-admin impersonation.** rules 1 and 2 |
| `pgMint` | The playground mint, apiKey mode; the oidc mode differs only in where the subject comes from. | §10.2 **Playground mint invariants** 1 to 5 |
| `fullRevoke` | A full revoke of a user by an administrator. | §11.4 step 5, §13.3 **Platform-admin impersonation.** rule 6 |
| `leak` | A bearer token reaches another user. | None. A bearer token authorizes whoever presents it, so the model lets a token move between users. |

Each step of a trace is one operation or a stutter. A user who presents a token must hold it, and the token an operation issues is held by the user who presented the request.

## Abstraction choices

- **Scopes.** The closed taxonomy is reduced to two domains, `D1` and `D2`. The actions are `Read`, `Write`, and one specific tool name, `Scale`. The scope values are `tools:*` (`AllTools`), the two domain wildcards (`W1`, `W2`), the six exact values (`E1r` to `E2s`), and one value the canonical form refuses (`Bad`, such as `openid`). A token with no values carries no `scope` claim, which §25.1 reads as unrestricted. The model has no claim that is present and empty, because the auth chain reads an empty claim as absent. `auth[S]` is the set of tools a claim admits at the scope gates, and a claim that contains `Bad` admits none, because the gates refuse a claim that does not parse. `grantsV` is the value-level grant of rule (a), which `scopes.Set.Matches` computes. `strictSyn` is the strictly-narrower comparison as §13.3 defines it, and `strictSem` is strict narrowing by the tools the claims admit.
- **Playground ceiling.** `Allowed` is `W1` and `E2r`, standing for `tools:sessions:*` and `tools:me:read`. The intersection follows `scopes.Set.Intersect`.
- **Tokens.** A token's attributes are static fields, fixed when an operation issues it: tenant, `sub`, scope, roles, `typ`, `caller_type`, `delegation_depth`, the `act` claim's `sub`, an expiry as an ordinal, the `issued_tokens` columns `parent_jti` and `act_sub`, and the subject, actor, and caller tokens the issuing request presented. `Rowless` tokens have no `issued_tokens` row: upstream ID tokens and playground bearers.
- **Roles.** The text does not state the `roles` claim of a token an exchange issues, and the Token Service writes none. The model lets an exchange issue any subset of the subject's roles, which covers both.
- **Caller authentication.** As change 0164 built it, the Token Service checked the caller token's signature and origin but not its revocation on a mint, and the text was silent. The model follows that build, so `P8_revoked_caller_cannot_mint` shows CX-10. Change 0167 added the caller to the step (1a) re-read. The revocation request checks the caller's revocation, as SM-11 decides.
- **Rotation.** An exchange is a rotation when the caller token is the subject token and the issued scope equals the subject's, as `tokenservice.isSelfRotation` decides and §13.3 **Lineage of derived tokens.** states as 0164 edits it. Audience is not modelled and is taken as unchanged.
- **Rotation lineage variants.** `Config.rot` selects the lineage a rotation records. `RotInherit` is 0164.S1 revision 3: the successor records the replaced token's own `parent_jti` and `act_sub`, so a root's successor records neither. `RotCut` is decision 7 of 0164 as first written: the successor records neither column. `RotEdge` is option (a) of 0164 open decision O3: the successor records the replaced token as its parent. 0164 decision 10 chose `RotEdge`, and it is the rotation rule as built. Every property is checked against `RotInherit` (commands ending in `_built`, named while revision 3 was the build), and the lineage and revocation properties are also checked against `RotCut` (`_cut`) and `RotEdge` (`_edge`).
- **Step record.** The operation of each step and its parameters are state (`Step.op`, `Step.pu`, `Step.pc`, and so on) rather than quantified variables, which keeps the translation small. Properties about one operation read the step record.
- **Left out.** Time passing and expiry checks (the expiry is compared only between a token and its sources), audiences and the per-dialect lifetime caps, `authorized_tools`, `session_id`, credential-lease issuance (an internal exchange whose lineage is the narrowing lineage), the end of an impersonation session, rate limits, and every concurrent ordering. A TLA+ model of the concurrent orderings is a candidate in change 0165.

## Running it

The runner needs Docker and the `org.alloytools.alloy.dist.jar` of an Alloy 6 release. It runs Alloy in an `eclipse-temurin:21-jdk` container with no network, mounting the model directory read-only and the jar read-only.

```sh
tests/formal/tokens/run.sh <path-to-org.alloytools.alloy.dist.jar> <output-dir> [command-pattern]
```

The runner prints one line per command. For a `check`, `UNSAT` means no counterexample exists within the scope and `SAT` means Alloy found one, which `<output-dir>/solutions` holds as a trace. For a `run`, `SAT` means the scenario exists. The command pattern selects commands by name, for example `'P3_*'` or `'*_edge'`.

The default solver is SAT4J, which runs on every platform. The native solvers ship for `linux/amd64` only, and glucose under emulation on an arm64 host ran a check about 2.6 times faster than SAT4J natively; set `ALLOY_SOLVER=glucose ALLOY_PLATFORM=linux/amd64` to use it.

Change 0165 records the Alloy version, the checksum of the jar it ran, the scope of each command, and each result.

## Properties

| Assertion | Property | Rule |
| :-- | :-- | :-- |
| `P1_rule_a` | Each requested value is canonical and granted by the subject's scope. | §13.3 rule (a) |
| `P1_no_broader_than_subject` | No token is broader than its subject token, by the tools it admits. | §13.3 rule (a), §10.2 invariant 2 |
| `P1_impersonation_no_broader_than_caller` | An impersonation bearer is no broader than the caller's token. | §13.3 **Platform-admin impersonation.** rule 1 (b) |
| `P1_no_broader_than_lineage_parent` | No token is broader than the token its `parent_jti` names. | §13.3 **Lineage of derived tokens.** |
| `P1_playground_never_unrestricted` | A playground bearer carries a scope inside the ceiling. | §10.2 invariant 2 |
| `P2_tenant_preserved` | A derived token has its subject's, caller's, and actor's tenant. | §13.3 **Tenant-scope enforcement** |
| `P2_cross_tenant_only_by_impersonation` | Only a platform-admin impersonation issues a token in another tenant. | §13.3 **Platform-admin impersonation.** |
| `P3_cascade_reaches_lineage`, `P3_nothing_outside_lineage`, `P3_rotation_revokes_only_its_subject` | A revocation request revokes its root and every lineage descendant and nothing else; a rotation revokes only the token it replaces. | §13.3 **Lineage of derived tokens.** |
| `P3_derived_sm9` | A revocation request reaches every token derived by narrowing or child minting, with rotation starting a new lineage. | SM-9 |
| `P3_derived_through_rotation` | As above, with a rotation standing in for the token it replaced. | SM-7 read with SM-9 |
| `P3_derived_child_subject`, `P3_derived_playground`, `P3_derived_impersonation` | A revocation request reaches the tokens derived from the root as a child's subject, as a playground subject, or as an impersonation caller. | §13.3, §10.2, SM-10 |
| `P3_every_lineage_root_revocable` | Every token with lineage descendants can be named as a revocation root. | §13.3 **Lineage of derived tokens.** |
| `P4_revocation_authority` | A step revokes a token only for a user who holds a live administrator token, or a live token carrying the identity of the token or of one of its lineage ancestors. | SM-11, §11.4 |
| `P4_revoked_caller_cannot_revoke` | A revoked caller cannot revoke. | SM-11 |
| `P5_other_identity_strictly_narrower`, `P5_child_other_identity_strictly_narrower` | A token issued to a caller of another identity is strictly narrower, by the tools it admits, than the subject. | §13.3 different-caller rule, SM-10 |
| `P5_no_identity_escalation` | No operation gives a user a token of another identity that is broader than a token of that identity the user already held. | §13.3, SM-10 |
| `P5_actor_is_caller` | A child's actor token belongs to the caller. | SM-10 |
| `P6_playground_never_a_source` | No token is issued from a playground bearer as subject, actor, or caller. | §13.3 (SM-6), §10.2 invariant 1 |
| `P7_child_depth_and_act`, `P7_depth_monotone_along_lineage`, `P7_depth_not_below_subject`, `P7_delegated_token_names_actor` | Delegation depth grows by one along child mints and never falls, and the `act` claim names the delegating token's identity. | §13.3 parameter table, rule (b) |
| `P8_revoked_caller_cannot_mint` | A revoked token authenticates no mint. | Added |
| `P9_lifetime_bounded_by_subject_and_actor`, `P9_impersonation_bounded_by_caller`, `P9_lifetime_bounded_by_lineage_parent` | No token outlives the tokens it was issued from. | §13.3 **`exp` granularity and parent-expiry handling.**, added for impersonation |
| `P10_full_revoke_reaches_delegations` | A full revoke of a user leaves no live token that carries the user's identity or names the user as actor. | Added, §11.4 |
| `O3_revoking_replaced_token_spares_successor` | Revoking a token that a rotation replaced never revokes the rotation's successor. | 0164 decision 7 and open decision O3, question (i) |
| `O3_revocation_reaches_successors` | Revoking a token revokes every token rotated or derived from it. | 0164 open decision O3, question (ii) |
| `O3_leak_holder_revokes_owner_successor` (run) | A user other than the owner, with no administrator token, revokes the successor the owner holds of a token the owner rotated away from. | O3 (i) scenario |
| `O3_leak_holder_keeps_successor` (run) | A holder of a leaked token rotates it, the owner revokes the leaked token, and the holder's successor stays live. | O3 (ii) scenario |
| `O4_narrow_caller_revokes_only_narrower` | A caller without an administrator role revokes only tokens no broader than its own. | 0164 open decision O4 |
| `O4_revocation_bounded_by_holding` | A revocation request revokes only tokens the presenting user holds and their lineage descendants. | 0164 open decision O4, option (a) |

## Commands

Each property is a predicate. A `check` command named `<property>_built`, `<property>_cut`, or `<property>_edge` checks it under one rotation variant: `_built` under `RotInherit`, `_cut` under `RotCut`, and `_edge` under `RotEdge`, the rotation rule as built. The witnesses (`show_*`) and the base checks use a scope of up to 3 users, 2 tenants, 6 tokens, and 6 operations (7 states, `1..7 steps`), with a 4-bit integer width for depth and expiry. The commands named `L_*` check the properties that hold at the base scope against `RotInherit` at up to 4 users, 2 tenants, 8 tokens, and 8 operations (9 states). The `O3_leak_*` scenarios are `run` commands, so `SAT` there means the scenario exists.
