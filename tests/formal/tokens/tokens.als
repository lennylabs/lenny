/*
 * Token rules of the Token Service and its neighbours, as an Alloy 6 model.
 *
 * The model covers the RFC 8693 exchange on /v1/oauth/token (self-rotation,
 * narrowing and copying, exchange by another caller, and delegation child
 * minting with an actor_token), the recursive-revocation request, the
 * platform-admin impersonation mint, the playground mint, the full revoke of
 * a user, and the leak of a bearer token to another user. It states the
 * rules of section 13.3 (Credential Flow) as change 0164 builds them,
 * including the owner's answers SM-9, SM-10, and SM-11 of 2026-11-20, the
 * scope grammar of section 25.1, and the playground mint invariants of
 * section 10.2. README.md in this directory maps each predicate to the rule
 * it encodes and each assertion to the property it checks.
 *
 * Time is a sequence of states (Alloy 6 temporal mode). Each step is one
 * operation or a stutter. Token attributes are static fields: a token atom
 * exists in every state, and its attributes are fixed when an operation
 * issues it.
 */
module tokens

/* ------------------------------------------------------------------ */
/* Identities, tenants, roles, and token metadata                      */
/* ------------------------------------------------------------------ */

sig Tenant {}

/* A user is an identity, the `sub` of a token. A user may hold tokens of
 * other users (a leaked token, a token derived from another user's token). */
sig User {
  var holds: set Token
}

abstract sig Role {}
one sig TenantAdmin, PlatformAdmin extends Role {}

abstract sig Typ {}
/* UserBearer is user_bearer; Other stands for service_token and
 * session_capability; A2A is a2a_delegation. */
one sig UserBearer, Other, A2A extends Typ {}

abstract sig CType {}
one sig Human, Agent extends CType {}

/* How a token was issued. Root is a token no modelled operation issued: an
 * upstream OIDC ID token, or a Lenny token minted outside /v1/oauth/token. */
abstract sig Kind {}
one sig Root, Rotation, Narrow, Child, Imperson, PgMint extends Kind {}

/* ------------------------------------------------------------------ */
/* Scope grammar (section 25.1)                                        */
/* ------------------------------------------------------------------ */

/* The closed taxonomy, reduced to two domains. D1 plays the role of
 * `sessions`, D2 the role of `me`. */
abstract sig Domain {}
one sig D1, D2 extends Domain {}

/* Read and Write are the generic actions; Scale is a specific tool name. */
abstract sig Action {}
one sig Read, Write, Scale extends Action {}

/* Scope values: tools:*, tools:<d>:*, tools:<d>:<a>, and one value the
 * canonical form refuses (such as the raw OAuth value `openid`). */
abstract sig Value {}
one sig AllTools, Bad extends Value {}
abstract sig Wild extends Value { wd: one Domain }
abstract sig Exact extends Value { ed: one Domain, ea: one Action }
one sig W1, W2 extends Wild {}
one sig E1r, E1w, E1s, E2r, E2w, E2s extends Exact {}

fact valueTable {
  W1.wd = D1 and W2.wd = D2
  E1r.ed = D1 and E1r.ea = Read
  E1w.ed = D1 and E1w.ea = Write
  E1s.ed = D1 and E1s.ea = Scale
  E2r.ed = D2 and E2r.ea = Read
  E2w.ed = D2 and E2w.ea = Write
  E2s.ed = D2 and E2s.ea = Scale
}

/* The playground_allowed_scope ceiling, reduced: tools:sessions:* and
 * tools:me:read. */
fun Allowed: set Value { W1 + E2r }

/* The tools a scope claim admits at the scope gates (section 25.1
 * Matching.). An absent claim (no values) and tools:* admit every tool. A
 * claim with a value the canonical form refuses admits none, because the
 * gates refuse a claim that does not parse. */
fun auth[S: set Value]: Domain -> Action {
  (no S) => Domain -> Action
  else (Bad in S) => (none -> none)
  else (AllTools in S) => Domain -> Action
  else ((S & Wild).wd -> Action) + { d: Domain, a: Action | some e: S & Exact | e.ed = d and e.ea = a }
}

/* A claim S grants a requested value v (section 13.3 rule (a) with section
 * 25.1 Matching.): v is canonical, and S is absent, or S parses and contains
 * tools:*, v itself, or the wildcard of v's domain. A requested domain
 * wildcard is granted only by itself or tools:*. */
pred grantsV[S: set Value, v: Value] {
  v != Bad
  (no S) or (Bad not in S and (AllTools in S or v in S
     or (v in Exact and v.ed in (S & Wild).wd)))
}

pred grantsAll[S, R: set Value] { all v: R | grantsV[S, v] }

/* The subject's scope as the strictly-narrower comparison reads it: an absent
 * claim counts as tools:*, and tools:* counts as every domain wildcard. */
fun expandEff[S: set Value]: set Value {
  let e = ((no S) => AllTools else S) |
    (e - AllTools) + ((AllTools in e) => Wild else none)
}

/* Strictly narrower, as section 13.3 Client authentication on
 * /v1/oauth/token. defines it and checkCallerIdentity computes it: a
 * non-empty request the subject grants that does not grant every value of
 * the subject's expanded scope. */
pred strictSyn[R, S: set Value] {
  some R
  Bad not in R
  grantsAll[S, R]
  not grantsAll[R, expandEff[S]]
}

/* Strictly narrower by the tools the two claims admit. */
pred strictSem[R, S: set Value] {
  auth[R] in auth[S]
  not (auth[S] in auth[R])
}

/* The playground intersection (scopes.Set.Intersect): the subject's values
 * the ceiling grants, and the ceiling's values the subject grants. */
fun intersect[S, A: set Value]: set Value {
  { g: S | grantsV[A, g] } + { g: A | grantsV[S, g] }
}

/* ------------------------------------------------------------------ */
/* Tokens                                                              */
/* ------------------------------------------------------------------ */

sig Token {
  tenant: one Tenant,
  sub: one User,
  scope: set Value,          /* no values: the claim is absent */
  roles: set Role,
  typ: one Typ,
  ctype: lone CType,         /* an upstream ID token may carry none */
  depth: one Int,            /* delegation_depth */
  act: lone User,            /* the sub of the act claim */
  exp: one Int,              /* expiry, as an ordinal */
  kind: one Kind,
  /* the issued_tokens row */
  parent: lone Token,        /* parent_jti */
  actSub: lone User,         /* act_sub */
  /* the tokens the issuing request presented */
  subj: lone Token,          /* subject_token */
  actor: lone Token,         /* actor_token */
  caller: lone Token         /* the Authorization bearer */
}

/* Tokens with no issued_tokens row: upstream ID tokens and playground
 * bearers. Step (1a) passes them, and a revocation request cannot name them
 * as its root. */
sig Rowless in Token {}

fun PG: set Token { kind.PgMint }
fun IMP: set Token { kind.Imperson }

fact staticShape {
  all t: Token | t.depth >= 0 and t.exp >= 0
  /* roots */
  all t: kind.Root {
    no t.parent and no t.actSub and no t.act
    t.depth = 0
    no t.subj and no t.actor and no t.caller
    t.typ in UserBearer + Other
  }
  /* rowless tokens are roots or playground bearers, and every playground
   * bearer is rowless */
  Rowless in kind.(Root + PgMint)
  PG in Rowless
  /* lineage never forms a cycle */
  no t: Token | t in t.^parent
}

/* ------------------------------------------------------------------ */
/* State                                                               */
/* ------------------------------------------------------------------ */

var sig Issued in Token {}
var sig Revoked in Token {}

pred live[t: Token] { t in Issued and t not in Revoked }

pred fresh[i: Token] { i not in Issued and Issued' = Issued + i }

fact init {
  Issued in kind.Root
  no Revoked
  holds = { u: User, t: Token | t in Issued and t.sub = u }
}

/* ------------------------------------------------------------------ */
/* Operations                                                          */
/* ------------------------------------------------------------------ */

/* A tenant user signs in at the identity provider, which issues a root. */
pred login[u: User, t: Token] {
  t.kind = Root and t.sub = u
  fresh[t]
  Revoked' = Revoked
  holds' = holds + u -> t
}

/* The caller's identity differs from the subject's (section 13.3 Client
 * authentication on /v1/oauth/token.): another sub, or another caller_type
 * when both tokens carry one. */
pred differentCaller[c, s: Token] {
  c.sub != s.sub or (some c.ctype and some s.ctype and c.ctype != s.ctype)
}

/* Rule (a): a request without scope receives the subject's scope; a request
 * with scope names canonical values the subject grants. The issued scope
 * stands for the requested scope. */
pred scopeRule[s, i: Token] {
  i.scope = s.scope
  or (some i.scope and Bad not in i.scope and grantsAll[s.scope, i.scope])
}

/* An exchange without an actor_token: self-rotation, narrowing or copying by
 * the subject's own principal, or an exchange by another caller. The caller
 * token is not checked for revocation (pkg/tokenservice authenticateCaller;
 * the text is silent). A rotation is detected as tokenservice.isSelfRotation
 * does: the caller token is the subject token and the issued scope equals
 * the subject's (audience is not modelled and taken as unchanged). */
pred exchange[u: User, c, s, i: Token] {
  c + s in u.holds
  c in Issued and c not in PG
  live[s] and s not in PG + IMP
  c.tenant = s.tenant
  fresh[i]
  i.tenant = s.tenant and i.sub = s.sub and i.ctype = s.ctype
  i.typ = s.typ and i.depth = s.depth and no i.act
  i.roles in s.roles
  i.exp =< s.exp
  i.subj = s and no i.actor and i.caller = c
  i not in Rowless
  scopeRule[s, i]
  differentCaller[c, s] implies strictSyn[i.scope, s.scope]
  (c = s and i.scope = s.scope) => {
    /* a rotation revokes the token it replaces; its lineage follows the
     * variant under analysis */
    i.kind = Rotation
    rotationLineage[s, i]
    Revoked' = Revoked + (s - Rowless)
  } else {
    /* SM-7: every other exchange records the subject as parent */
    i.kind = Narrow and i.parent = s and no i.actSub
    Revoked' = Revoked
  }
  holds' = holds + u -> i
}

/* The lineage a rotation records, in each variant under analysis:
 * - RotCut, decision 7 of 0164 as first written: no parent and no act_sub.
 * - RotInherit, 0164.S1 as built: the replaced token's own parent_jti and
 *   act_sub, so a root's successor has neither.
 * - RotEdge, option (a) of 0164 open decision O3: the replaced token is the
 *   parent, and act_sub is null. */
abstract sig RotMode {}
one sig RotCut, RotInherit, RotEdge extends RotMode {}
one sig Config { rot: one RotMode }

pred rotationLineage[s, i: Token] {
  Config.rot = RotCut implies (no i.parent and no i.actSub)
  Config.rot = RotInherit implies (i.parent = s.parent and i.actSub = s.actSub)
  Config.rot = RotEdge implies (i.parent = s and no i.actSub)
}

/* An a2a_delegation child mint: subject_token plus actor_token. SM-10: the
 * actor's sub equals the caller's, and a subject whose sub differs from the
 * actor's is narrowed strictly. */
pred childMint[u: User, c, s, a, i: Token] {
  c + s + a in u.holds
  c in Issued and c not in PG
  live[s] and live[a]
  no (s + a) & (PG + IMP)
  a.sub = c.sub
  c.tenant = s.tenant and a.tenant = s.tenant
  fresh[i]
  i.tenant = s.tenant and i.sub = s.sub and i.ctype = s.ctype
  i.typ = A2A and i.depth = plus[a.depth, 1] and i.act = a.sub
  i.roles in s.roles
  i.exp =< s.exp and i.exp =< a.exp
  i.subj = s and i.actor = a and i.caller = c
  i not in Rowless
  scopeRule[s, i]
  s.sub != a.sub implies strictSyn[i.scope, s.scope]
  i.kind = Child and i.parent = a and i.actSub = a.sub
  Revoked' = Revoked
  holds' = holds + u -> i
}

/* The recursive-revocation request. SM-11: the caller is the subject's own
 * user or holds an administrator role, its token is not revoked, and a
 * revoked root still cascades. The root needs an issued_tokens row. */
pred revokeReq[u: User, c, s: Token] {
  c + s in u.holds
  c in Issued and c not in PG and c not in Revoked
  c.tenant = s.tenant
  c.sub = s.sub or some c.roles
  s in Issued and s not in Rowless
  Issued' = Issued
  Revoked' = Revoked + (s.*~parent & Issued)
  holds' = holds
}

/* Platform-admin impersonation (section 13.3 Platform-admin impersonation.
 * rule 1). The bearer's tenant, sub, roles, and exp are the request's. */
pred impersonate[u: User, c, i: Token] {
  c in u.holds
  live[c] and c not in IMP + PG
  PlatformAdmin in c.roles
  fresh[i]
  i.kind = Imperson and i.scope = c.scope
  i.typ = UserBearer and i.ctype = Human and i.depth = 0 and no i.act
  no i.parent and i.actSub = c.sub
  i.caller = c and no i.subj and no i.actor
  i not in Rowless
  Revoked' = Revoked
  holds' = holds + u -> i
}

/* The playground mint (section 10.2 Playground mint invariants), apiKey
 * mode; the oidc mode is the same mint with an upstream ID token as subject.
 * The bearer has no issued_tokens row. */
pred pgMint[u: User, s, i: Token] {
  s in u.holds
  live[s] and s not in IMP
  s.typ = UserBearer
  Bad not in s.scope
  fresh[i]
  i.kind = PgMint and i.typ = Other
  i.tenant = s.tenant and i.sub = s.sub and i.ctype = s.ctype and i.roles = s.roles
  i.depth = 0 and no i.act and no i.parent and no i.actSub
  i.scope = ((no s.scope) => Allowed else intersect[s.scope, Allowed])
  some i.scope
  i.exp =< s.exp
  i.subj = s and no i.actor and i.caller = s
  Revoked' = Revoked
  holds' = holds + u -> i
}

/* A full revoke of user x in the caller's tenant (section 11.4 step 5): the
 * user's issued tokens and playground bearers, and, for a platform-admin
 * caller, the impersonation bearers x started. */
pred fullRevoke[u: User, c: Token, x: User] {
  c in u.holds
  live[c] and c not in PG
  some c.roles
  Issued' = Issued
  Revoked' = Revoked
    + { t: Issued | t.sub = x and t.tenant = c.tenant }
    + ((PlatformAdmin in c.roles) => { t: Issued & IMP | t.actSub = x } else none)
  holds' = holds
}

/* A bearer token reaches another user. */
pred leak[u, v: User, t: Token] {
  t in u.holds and u != v and t not in v.holds
  Issued' = Issued and Revoked' = Revoked
  holds' = holds + v -> t
}

pred skip {
  Issued' = Issued and Revoked' = Revoked and holds' = holds
}

/* The operation each step performs and its parameters. Naming the
 * parameters as state, rather than quantifying over them, keeps the
 * translation small; the parameters an operation does not use are free. */
abstract sig Op {}
one sig OpSkip, OpLogin, OpExchange, OpChild, OpRevoke, OpImperson, OpPg, OpFull, OpLeak extends Op {}

one sig Step {
  var op: one Op,
  var pu, pv: one User,
  var pc, ps, pa, pi: one Token
}

fact traces {
  always {
    let u = Step.pu, v = Step.pv, c = Step.pc, s = Step.ps, a = Step.pa, i = Step.pi {
      Step.op = OpSkip implies skip
      Step.op = OpLogin implies login[u, i]
      Step.op = OpExchange implies exchange[u, c, s, i]
      Step.op = OpChild implies childMint[u, c, s, a, i]
      Step.op = OpRevoke implies revokeReq[u, c, s]
      Step.op = OpImperson implies impersonate[u, c, i]
      Step.op = OpPg implies pgMint[u, s, i]
      Step.op = OpFull implies fullRevoke[u, c, v]
      Step.op = OpLeak implies leak[u, v, i]
    }
  }
}

/* ------------------------------------------------------------------ */
/* Derivation used by the lineage properties                           */
/* ------------------------------------------------------------------ */

/* The non-rotation token a chain of rotations replaces. */
fun base[i: Token]: Token {
  i.*{ r: Token, x: Token | r.kind = Rotation and x = r.subj } & (Token - kind.Rotation)
}

/* Edges from a source token to the token issued from it. */
fun eNarrow: Token -> Token { { x, i: Issued | i.kind = Narrow and x = i.subj } }
fun eChildActor: Token -> Token { { x, i: Issued | i.kind = Child and x = i.actor } }
fun eChildSubject: Token -> Token { { x, i: Issued | i.kind = Child and x = i.subj } }
fun ePlayground: Token -> Token { { x, i: Issued | i.kind = PgMint and x = i.subj } }
fun eImperson: Token -> Token { { x, i: Issued | i.kind = Imperson and x = i.caller } }

/* Derivation with rotation cutting the lineage (SM-9 as decided): a
 * rotation has no source. */
fun cut[E: Token -> Token]: Token -> Token { E }

/* Derivation with a rotation standing in for the token it replaced: the
 * replacement derives from whatever the replaced token derived from. */
fun inherit[E: Token -> Token]: Token -> Token {
  E + { x, i: Issued | i.kind = Rotation and x -> base[i] in E }
}

/* ------------------------------------------------------------------ */
/* Properties                                                          */
/* ------------------------------------------------------------------ */

/* P1 Narrowing. */
pred P1_rule_a {
  always all i: Issued - kind.(Root + Imperson + PgMint) |
    i.scope = i.subj.scope or grantsAll[i.subj.scope, i.scope]
}
pred P1_no_broader_than_subject {
  always all i: Issued - kind.(Root + Imperson) | auth[i.scope] in auth[i.subj.scope]
}
pred P1_impersonation_no_broader_than_caller {
  always all i: Issued & IMP | auth[i.scope] in auth[i.caller.scope]
}
pred P1_no_broader_than_lineage_parent {
  always all i: Issued | some i.parent implies auth[i.scope] in auth[i.parent.scope]
}
pred P1_playground_never_unrestricted {
  always all i: Issued & PG | some i.scope and auth[i.scope] in auth[Allowed]
}

/* P2 Tenant. */
pred P2_tenant_preserved {
  always all i: Issued - kind.(Root + Imperson) |
    i.tenant = i.subj.tenant and i.tenant = i.caller.tenant
    and (some i.actor implies i.actor.tenant = i.tenant)
}
pred P2_cross_tenant_only_by_impersonation {
  always all i: Issued & IMP | PlatformAdmin in i.caller.roles and i.actSub = i.caller.sub
}

/* P3 Revocation reach. */
pred P3_cascade_reaches_lineage {
  always (Step.op = OpRevoke implies (Step.ps.*~parent & Issued) in Revoked')
}
pred P3_nothing_outside_lineage {
  always (Step.op = OpRevoke implies (Revoked' - Revoked) in Step.ps.*~parent)
}
pred P3_rotation_revokes_only_its_subject {
  always ((Step.op = OpExchange and Step.pi.kind = Rotation) implies (Revoked' - Revoked) in Step.ps)
}
/* Derived tokens, rotation cutting the lineage (SM-9 as decided). */
pred P3_derived_sm9 {
  always (Step.op = OpRevoke implies
    (Step.ps.^(cut[eNarrow + eChildActor]) & Issued) in Revoked')
}
/* Derived tokens, a rotation standing in for the token it replaced. */
pred P3_derived_through_rotation {
  always (Step.op = OpRevoke implies
    (Step.ps.^(inherit[eNarrow + eChildActor]) & Issued) in Revoked')
}
pred P3_derived_child_subject {
  always (Step.op = OpRevoke implies
    (Step.ps.^(cut[eNarrow + eChildActor + eChildSubject]) & Issued) in Revoked')
}
pred P3_derived_playground {
  always (Step.op = OpRevoke implies
    (Step.ps.^(cut[eNarrow + eChildActor + ePlayground]) & Issued) in Revoked')
}
pred P3_derived_impersonation {
  always (Step.op = OpRevoke implies
    (Step.ps.^(cut[eNarrow + eChildActor + eImperson]) & Issued) in Revoked')
}
pred P3_every_lineage_root_revocable {
  always all t: Issued | some (t.~parent & Issued) implies t not in Rowless
}

/* P4 Who may revoke. */
/* The presenting user u may revoke t: u holds a live administrator token, or
 * a live token carrying the identity of t or of one of t's lineage
 * ancestors. */
pred mayRevoke[u: User, t: Token] {
  (some h: u.holds | live[h] and some h.roles and h not in PG)
  or (some h: u.holds, x: t.*parent | live[h] and x.sub = h.sub and x.tenant = h.tenant)
}
pred P4_revocation_authority {
  always all t: Revoked' - Revoked | mayRevoke[Step.pu, t]
}
pred P4_revoked_caller_cannot_revoke {
  always (Step.op = OpRevoke implies live[Step.pc])
}

/* P5 Identity. */
pred P5_other_identity_strictly_narrower {
  always ((Step.op = OpExchange and differentCaller[Step.pc, Step.ps]) implies
    strictSem[Step.pi.scope, Step.ps.scope])
}
pred P5_child_other_identity_strictly_narrower {
  always ((Step.op = OpChild and Step.ps.sub != Step.pa.sub) implies
    strictSem[Step.pi.scope, Step.ps.scope])
}
pred P5_no_identity_escalation {
  always all i: Issued' - Issued |
    (i.sub != Step.pu and i.kind != Imperson) implies
      some t: Step.pu.holds | t.sub = i.sub and t.tenant = i.tenant and auth[i.scope] in auth[t.scope]
}
pred P5_actor_is_caller {
  always all i: Issued & kind.Child | i.actor.sub = i.caller.sub
}

/* P6 Playground origin. */
pred P6_playground_never_a_source {
  always all i: Issued | no (i.subj + i.actor + i.caller) & PG
}

/* P7 Delegation depth and act. */
pred P7_child_depth_and_act {
  always all i: Issued & kind.Child | i.depth = plus[i.actor.depth, 1] and i.act = i.actor.sub
}
pred P7_depth_monotone_along_lineage {
  always all i: Issued | some i.parent implies i.depth >= i.parent.depth
}
pred P7_depth_not_below_subject {
  always all i: Issued - kind.(Root + Imperson + PgMint) | i.depth >= i.subj.depth
}
pred P7_delegated_token_names_actor {
  always all i: Issued | i.depth > 0 implies some i.act
}

/* P8 (added) A revoked token authorizes nothing. */
pred P8_revoked_caller_cannot_mint {
  always all i: Issued' - Issued | i.kind != Root implies i.caller not in Revoked
}

/* P9 (added) No issued token outlives its sources. */
pred P9_lifetime_bounded_by_subject_and_actor {
  always all i: Issued - kind.(Root + Imperson) |
    i.exp =< i.subj.exp and (some i.actor implies i.exp =< i.actor.exp)
}
pred P9_impersonation_bounded_by_caller {
  always all i: Issued & IMP | i.exp =< i.caller.exp
}
pred P9_lifetime_bounded_by_lineage_parent {
  always all i: Issued | some i.parent implies i.exp =< i.parent.exp
}

/* P10 (added) A full revoke of a user leaves no live token that carries the
 * user's identity or names the user as actor. */
pred P10_full_revoke_reaches_delegations {
  always (Step.op = OpFull implies
    no t: Issued' - Revoked' | t.tenant = Step.pc.tenant and (t.sub = Step.pv or t.act = Step.pv))
}


/* 0164 open decisions O3 and O4. */

/* Successor edges: a rotation's replaced token to the token it issued. */
fun succ: Token -> Token { { x, r: Issued | r.kind = Rotation and x = r.subj } }

/* (i) Revoking a token that a rotation replaced never revokes the token the
 * rotation issued. */
pred O3_revoking_replaced_token_spares_successor {
  always (Step.op = OpRevoke implies no (Step.ps.succ & (Revoked' - Revoked)))
}

/* (ii) Revoking a token revokes every token rotated or derived from it, so a
 * holder of a leaked token keeps no live token after the owner revokes it. */
pred O3_revocation_reaches_successors {
  always (Step.op = OpRevoke implies (Step.ps.^(~parent + succ) & Issued) in Revoked')
}

/* (i), scenario: a user who is not the replaced token's user and holds no
 * administrator token revokes the successor of a token its owner rotated away
 * from, a successor the user does not hold. */
pred O3_leak_holder_revokes_owner_successor {
  eventually (Step.op = OpRevoke and some r: Issued & kind.Rotation |
    r.subj = Step.ps and r in Revoked' - Revoked and r not in Step.pu.holds
    and r in r.sub.holds and Step.pu != r.sub
    and (no h: Step.pu.holds | live[h] and some h.roles))
}

/* (ii), scenario: a holder of a leaked token rotates it, the owner then
 * revokes the leaked token, and the holder's successor stays live. */
pred O3_leak_holder_keeps_successor {
  eventually (Step.op = OpRevoke and some r: Issued & kind.Rotation |
    r.subj = Step.ps and Step.pu = r.subj.sub and r.subj in Step.pu.holds
    and r not in Step.pu.holds and r not in Revoked')
}

/* O4: a caller without an administrator role revokes only tokens no broader
 * than its own. */
pred O4_narrow_caller_revokes_only_narrower {
  always (Step.op = OpRevoke and no Step.pc.roles implies
    (all t: Revoked' - Revoked | auth[t.scope] in auth[Step.pc.scope]))
}

/* O4: a revocation request revokes only tokens the presenting user holds and
 * their lineage descendants. */
pred O4_revocation_bounded_by_holding {
  always (Step.op = OpRevoke implies (Revoked' - Revoked) in (Step.pu.holds).*~parent)
}

/* ------------------------------------------------------------------ */
/* Commands                                                            */
/* ------------------------------------------------------------------ */

/* Witnesses: each operation can happen, so no property holds vacuously. */
run show_login { Config.rot = RotInherit and eventually Step.op = OpLogin }
  for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
run show_rotation { Config.rot = RotInherit and eventually (Step.op = OpExchange and Step.pi.kind = Rotation) }
  for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
run show_narrowing { Config.rot = RotInherit and eventually (Step.op = OpExchange and Step.pi.kind = Narrow and some Step.pi.scope and no Step.ps.scope) }
  for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
run show_other_caller { Config.rot = RotInherit and eventually (Step.op = OpExchange and differentCaller[Step.pc, Step.ps]) }
  for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
run show_child_mint { Config.rot = RotInherit and eventually Step.op = OpChild }
  for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
run show_child_other_subject { Config.rot = RotInherit and eventually (Step.op = OpChild and Step.ps.sub != Step.pa.sub) }
  for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
run show_revocation_cascade { Config.rot = RotInherit and eventually (Step.op = OpRevoke and some (Step.ps.~parent & Issued - Revoked)) }
  for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
run show_admin_revocation { Config.rot = RotInherit and eventually (Step.op = OpRevoke and Step.pc.sub != Step.ps.sub) }
  for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
run show_impersonation { Config.rot = RotInherit and eventually (Step.op = OpImperson and Step.pi.tenant != Step.pc.tenant) }
  for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
run show_playground_mint { Config.rot = RotInherit and eventually (Step.op = OpPg and some Step.ps.scope) }
  for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
run show_full_revoke { Config.rot = RotInherit and eventually (Step.op = OpFull and some (Revoked' - Revoked)) }
  for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
run show_leak { Config.rot = RotInherit and eventually Step.op = OpLeak }
  for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps

/* Checks at the base scope: up to 3 users, 2 tenants, 6 tokens, and 6
 * operations (7 states). Every property runs against 0164.S1 as built
 * (RotInherit); the lineage and revocation properties also run against
 * the two other rotation variants. */
check P1_rule_a_built { Config.rot = RotInherit implies P1_rule_a } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P1_no_broader_than_subject_built { Config.rot = RotInherit implies P1_no_broader_than_subject } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P1_impersonation_no_broader_than_caller_built { Config.rot = RotInherit implies P1_impersonation_no_broader_than_caller } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P1_no_broader_than_lineage_parent_built { Config.rot = RotInherit implies P1_no_broader_than_lineage_parent } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P1_playground_never_unrestricted_built { Config.rot = RotInherit implies P1_playground_never_unrestricted } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P2_tenant_preserved_built { Config.rot = RotInherit implies P2_tenant_preserved } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P2_cross_tenant_only_by_impersonation_built { Config.rot = RotInherit implies P2_cross_tenant_only_by_impersonation } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_cascade_reaches_lineage_built { Config.rot = RotInherit implies P3_cascade_reaches_lineage } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_nothing_outside_lineage_built { Config.rot = RotInherit implies P3_nothing_outside_lineage } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_rotation_revokes_only_its_subject_built { Config.rot = RotInherit implies P3_rotation_revokes_only_its_subject } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_derived_sm9_built { Config.rot = RotInherit implies P3_derived_sm9 } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_derived_through_rotation_built { Config.rot = RotInherit implies P3_derived_through_rotation } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_derived_child_subject_built { Config.rot = RotInherit implies P3_derived_child_subject } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_derived_playground_built { Config.rot = RotInherit implies P3_derived_playground } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_derived_impersonation_built { Config.rot = RotInherit implies P3_derived_impersonation } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_every_lineage_root_revocable_built { Config.rot = RotInherit implies P3_every_lineage_root_revocable } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P4_revocation_authority_built { Config.rot = RotInherit implies P4_revocation_authority } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P4_revoked_caller_cannot_revoke_built { Config.rot = RotInherit implies P4_revoked_caller_cannot_revoke } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P5_other_identity_strictly_narrower_built { Config.rot = RotInherit implies P5_other_identity_strictly_narrower } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P5_child_other_identity_strictly_narrower_built { Config.rot = RotInherit implies P5_child_other_identity_strictly_narrower } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P5_no_identity_escalation_built { Config.rot = RotInherit implies P5_no_identity_escalation } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P5_actor_is_caller_built { Config.rot = RotInherit implies P5_actor_is_caller } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P6_playground_never_a_source_built { Config.rot = RotInherit implies P6_playground_never_a_source } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P7_child_depth_and_act_built { Config.rot = RotInherit implies P7_child_depth_and_act } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P7_depth_monotone_along_lineage_built { Config.rot = RotInherit implies P7_depth_monotone_along_lineage } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P7_depth_not_below_subject_built { Config.rot = RotInherit implies P7_depth_not_below_subject } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P7_delegated_token_names_actor_built { Config.rot = RotInherit implies P7_delegated_token_names_actor } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P8_revoked_caller_cannot_mint_built { Config.rot = RotInherit implies P8_revoked_caller_cannot_mint } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P9_lifetime_bounded_by_subject_and_actor_built { Config.rot = RotInherit implies P9_lifetime_bounded_by_subject_and_actor } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P9_impersonation_bounded_by_caller_built { Config.rot = RotInherit implies P9_impersonation_bounded_by_caller } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P9_lifetime_bounded_by_lineage_parent_built { Config.rot = RotInherit implies P9_lifetime_bounded_by_lineage_parent } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P10_full_revoke_reaches_delegations_built { Config.rot = RotInherit implies P10_full_revoke_reaches_delegations } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check O3_revoking_replaced_token_spares_successor_built { Config.rot = RotInherit implies O3_revoking_replaced_token_spares_successor } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check O3_revocation_reaches_successors_built { Config.rot = RotInherit implies O3_revocation_reaches_successors } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check O4_narrow_caller_revokes_only_narrower_built { Config.rot = RotInherit implies O4_narrow_caller_revokes_only_narrower } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check O4_revocation_bounded_by_holding_built { Config.rot = RotInherit implies O4_revocation_bounded_by_holding } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_cascade_reaches_lineage_cut { Config.rot = RotCut implies P3_cascade_reaches_lineage } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_nothing_outside_lineage_cut { Config.rot = RotCut implies P3_nothing_outside_lineage } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_rotation_revokes_only_its_subject_cut { Config.rot = RotCut implies P3_rotation_revokes_only_its_subject } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_derived_sm9_cut { Config.rot = RotCut implies P3_derived_sm9 } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_derived_through_rotation_cut { Config.rot = RotCut implies P3_derived_through_rotation } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_derived_child_subject_cut { Config.rot = RotCut implies P3_derived_child_subject } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_derived_playground_cut { Config.rot = RotCut implies P3_derived_playground } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_derived_impersonation_cut { Config.rot = RotCut implies P3_derived_impersonation } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_every_lineage_root_revocable_cut { Config.rot = RotCut implies P3_every_lineage_root_revocable } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P4_revocation_authority_cut { Config.rot = RotCut implies P4_revocation_authority } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P4_revoked_caller_cannot_revoke_cut { Config.rot = RotCut implies P4_revoked_caller_cannot_revoke } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check O3_revoking_replaced_token_spares_successor_cut { Config.rot = RotCut implies O3_revoking_replaced_token_spares_successor } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check O3_revocation_reaches_successors_cut { Config.rot = RotCut implies O3_revocation_reaches_successors } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check O4_narrow_caller_revokes_only_narrower_cut { Config.rot = RotCut implies O4_narrow_caller_revokes_only_narrower } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check O4_revocation_bounded_by_holding_cut { Config.rot = RotCut implies O4_revocation_bounded_by_holding } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_cascade_reaches_lineage_edge { Config.rot = RotEdge implies P3_cascade_reaches_lineage } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_nothing_outside_lineage_edge { Config.rot = RotEdge implies P3_nothing_outside_lineage } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_rotation_revokes_only_its_subject_edge { Config.rot = RotEdge implies P3_rotation_revokes_only_its_subject } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_derived_sm9_edge { Config.rot = RotEdge implies P3_derived_sm9 } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_derived_through_rotation_edge { Config.rot = RotEdge implies P3_derived_through_rotation } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_derived_child_subject_edge { Config.rot = RotEdge implies P3_derived_child_subject } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_derived_playground_edge { Config.rot = RotEdge implies P3_derived_playground } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_derived_impersonation_edge { Config.rot = RotEdge implies P3_derived_impersonation } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P3_every_lineage_root_revocable_edge { Config.rot = RotEdge implies P3_every_lineage_root_revocable } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P4_revocation_authority_edge { Config.rot = RotEdge implies P4_revocation_authority } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check P4_revoked_caller_cannot_revoke_edge { Config.rot = RotEdge implies P4_revoked_caller_cannot_revoke } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check O3_revoking_replaced_token_spares_successor_edge { Config.rot = RotEdge implies O3_revoking_replaced_token_spares_successor } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check O3_revocation_reaches_successors_edge { Config.rot = RotEdge implies O3_revocation_reaches_successors } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check O4_narrow_caller_revokes_only_narrower_edge { Config.rot = RotEdge implies O4_narrow_caller_revokes_only_narrower } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
check O4_revocation_bounded_by_holding_edge { Config.rot = RotEdge implies O4_revocation_bounded_by_holding } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
run O3_leak_holder_revokes_owner_successor_built { Config.rot = RotInherit and O3_leak_holder_revokes_owner_successor } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
run O3_leak_holder_keeps_successor_built { Config.rot = RotInherit and O3_leak_holder_keeps_successor } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
run O3_leak_holder_revokes_owner_successor_cut { Config.rot = RotCut and O3_leak_holder_revokes_owner_successor } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
run O3_leak_holder_keeps_successor_cut { Config.rot = RotCut and O3_leak_holder_keeps_successor } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
run O3_leak_holder_revokes_owner_successor_edge { Config.rot = RotEdge and O3_leak_holder_revokes_owner_successor } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps
run O3_leak_holder_keeps_successor_edge { Config.rot = RotEdge and O3_leak_holder_keeps_successor } for 3 User, 2 Tenant, 6 Token, 4 Int, 1..7 steps

/* Larger scope for the properties that hold at the base scope: up to 4
 * users, 2 tenants, 8 tokens, and 8 operations (9 states), against the
 * build. */
check L_P1_rule_a_built { Config.rot = RotInherit implies P1_rule_a } for 4 User, 2 Tenant, 8 Token, 4 Int, 1..9 steps
check L_P1_no_broader_than_subject_built { Config.rot = RotInherit implies P1_no_broader_than_subject } for 4 User, 2 Tenant, 8 Token, 4 Int, 1..9 steps
check L_P1_impersonation_no_broader_than_caller_built { Config.rot = RotInherit implies P1_impersonation_no_broader_than_caller } for 4 User, 2 Tenant, 8 Token, 4 Int, 1..9 steps
check L_P1_playground_never_unrestricted_built { Config.rot = RotInherit implies P1_playground_never_unrestricted } for 4 User, 2 Tenant, 8 Token, 4 Int, 1..9 steps
check L_P2_tenant_preserved_built { Config.rot = RotInherit implies P2_tenant_preserved } for 4 User, 2 Tenant, 8 Token, 4 Int, 1..9 steps
check L_P2_cross_tenant_only_by_impersonation_built { Config.rot = RotInherit implies P2_cross_tenant_only_by_impersonation } for 4 User, 2 Tenant, 8 Token, 4 Int, 1..9 steps
check L_P3_cascade_reaches_lineage_built { Config.rot = RotInherit implies P3_cascade_reaches_lineage } for 4 User, 2 Tenant, 8 Token, 4 Int, 1..9 steps
check L_P3_nothing_outside_lineage_built { Config.rot = RotInherit implies P3_nothing_outside_lineage } for 4 User, 2 Tenant, 8 Token, 4 Int, 1..9 steps
check L_P4_revocation_authority_built { Config.rot = RotInherit implies P4_revocation_authority } for 4 User, 2 Tenant, 8 Token, 4 Int, 1..9 steps
check L_P4_revoked_caller_cannot_revoke_built { Config.rot = RotInherit implies P4_revoked_caller_cannot_revoke } for 4 User, 2 Tenant, 8 Token, 4 Int, 1..9 steps
check L_P5_no_identity_escalation_built { Config.rot = RotInherit implies P5_no_identity_escalation } for 4 User, 2 Tenant, 8 Token, 4 Int, 1..9 steps
check L_P5_actor_is_caller_built { Config.rot = RotInherit implies P5_actor_is_caller } for 4 User, 2 Tenant, 8 Token, 4 Int, 1..9 steps
check L_P6_playground_never_a_source_built { Config.rot = RotInherit implies P6_playground_never_a_source } for 4 User, 2 Tenant, 8 Token, 4 Int, 1..9 steps
check L_P7_child_depth_and_act_built { Config.rot = RotInherit implies P7_child_depth_and_act } for 4 User, 2 Tenant, 8 Token, 4 Int, 1..9 steps
check L_P7_depth_monotone_along_lineage_built { Config.rot = RotInherit implies P7_depth_monotone_along_lineage } for 4 User, 2 Tenant, 8 Token, 4 Int, 1..9 steps
check L_P9_lifetime_bounded_by_subject_and_actor_built { Config.rot = RotInherit implies P9_lifetime_bounded_by_subject_and_actor } for 4 User, 2 Tenant, 8 Token, 4 Int, 1..9 steps
check L_P9_lifetime_bounded_by_lineage_parent_built { Config.rot = RotInherit implies P9_lifetime_bounded_by_lineage_parent } for 4 User, 2 Tenant, 8 Token, 4 Int, 1..9 steps
