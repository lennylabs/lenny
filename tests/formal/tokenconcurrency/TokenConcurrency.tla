--------------------------- MODULE TokenConcurrency ---------------------------
(***************************************************************************)
(* A model of the token rules of the Token Service under concurrency.      *)
(* Change 0169 builds it; README.md maps each process to the Go code and   *)
(* each property to its rule.                                              *)
(*                                                                         *)
(* Each request is a PlusCal process whose labels are the statements and   *)
(* the lock steps of the code, so one label is one atomic step.  Postgres  *)
(* is described by the operators and macros below: committed rows, the     *)
(* private writes of each open transaction, share and update row locks,    *)
(* the per-tenant audit advisory lock, commit, abort, and the deadlock     *)
(* detector.  Variant selects the rule set: "Main" is the code on main,    *)
(* and "TF" is the locking design of revision 4 of change 0168, in which   *)
(* the cascade and the full revoke's walk take the per-tenant advisory     *)
(* lock.  "I13Lock" names the same rules as "TF" at revision 4.            *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets, Sequences, TLC

CONSTANTS
  Variant,           \* "Main", "TF", or "I13Lock"
  Scenario,          \* the operations that race; see Ops below
  MaxAborts,         \* transaction aborts (statement failure, failover) per behavior
  MaxClientRetries,  \* client retries of a recursive-revocation request after a 500
  MaxLoss,           \* event-bus messages lost per behavior
  MaxRestarts        \* replica restarts per behavior

IsTF == Variant \in {"TF", "I13Lock"}

(***************************************************************************)
(* Identities.  Tenants acme and globex; alice and bob in acme, carol and  *)
(* dave in globex.  Every user, token, and tenant name is distinct, so one *)
(* string names a lockable resource: a token row by its jti, a user row   *)
(* by its sub, and the advisory lock by the tenant.                       *)
(***************************************************************************)
Tenants == {"acme", "globex"}
UTen == ("alice" :> "acme") @@ ("bob" :> "acme") @@ ("carol" :> "globex") @@ ("dave" :> "globex")
Users == DOMAIN UTen

NoRow == [ex |-> FALSE, ten |-> "", sub |-> "", par |-> "", act |-> "",
          iten |-> "", ijti |-> "", rev |-> FALSE, by |-> 0]
\* An issued_tokens row: tenant, sub, parent_jti, act_sub, the impersonator
\* link, and its revocation; by names the request that revoked it (0 for none).
Row(ten, sub, par, act, iten, ijti) ==
  [ex |-> TRUE, ten |-> ten, sub |-> sub, par |-> par, act |-> act,
   iten |-> iten, ijti |-> ijti, rev |-> FALSE, by |-> 0]
RevRow(r, p) == [r EXCEPT !.rev = TRUE, !.by = p]
\* impersonationPredicate: act_sub set and parent_jti null.
IsImp(r) == r.ex /\ r.act /= "" /\ r.par = ""

(***************************************************************************)
(* Operations.  Every field is present so that sets of operations compare. *)
(***************************************************************************)
Op(k, c, s, a, rot, rt, u, cb, tt, tu) ==
  [kind |-> k, caller |-> c, subj |-> s, actor |-> a, rot |-> rot, root |-> rt,
   user |-> u, cb |-> cb, tt |-> tt, tu |-> tu]
\* An exchange on /v1/oauth/token: caller, subject_token, actor_token ("" for
\* none), and whether it is a self-rotation.
Exch(c, s, a, rot) == Op("exch", c, s, a, rot, "", "", "", "", "")
\* A playground mint from a pasted subject_token.
Pg(s) == Op("pg", "", s, "", FALSE, "", "", "", "", "")
\* A recursive-revocation request: caller token and root (subject_token).
Rev(c, rt) == Op("rev", c, "", "", FALSE, rt, "", "", "", "")
\* A platform-admin impersonation start: caller bearer, target tenant, target user.
Imp(cb, tt, tu) == Op("imp", "", "", "", FALSE, "", "", cb, tt, tu)
\* A full revoke of a user by a platform-admin (both step 5 statements).
Full(u) == Op("full", "", "", "", FALSE, "", u, "", "", "")
\* DELETE /v1/admin/impersonation/{id}.
EndImp(j) == Op("end", "", "", "", FALSE, j, "", "", "", "")

(***************************************************************************)
(* Scenarios.  Each names the initial issued_tokens rows and the set of    *)
(* operations each request process may run.  A directed scenario races the *)
(* operations of one ordering in the Scope of change 0169; "free" lets each *)
(* process choose any operation.                                          *)
(***************************************************************************)
NProcs ==
  CASE Scenario \in {"cascade_issuance", "rotation", "mutual", "impersonation",
                     "fullrevoke", "nested_start"} -> 3
    [] Scenario \in {"cascade_rotation", "cache", "nested_step", "free"} -> 2
    [] Scenario = "f1_i20" -> 2

Procs == 1..NProcs
NewJ(p) == CASE p = 1 -> "N1" [] p = 2 -> "N2" [] p = 3 -> "N3"
NewJtis == {NewJ(p) : p \in Procs}

InitRows ==
  CASE Scenario \in {"cascade_issuance", "cascade_rotation", "cache"} ->
         ("C" :> Row("acme", "alice", "", "", "", "")) @@
         ("L" :> Row("acme", "alice", "", "", "", ""))
    [] Scenario = "rotation" ->
         ("L" :> Row("acme", "alice", "", "", "", "")) @@
         ("S" :> Row("acme", "bob", "", "", "", ""))
    [] Scenario = "mutual" ->
         ("C1" :> Row("acme", "alice", "", "", "", "")) @@
         ("C2" :> Row("acme", "alice", "", "", "", ""))
    [] Scenario = "impersonation" ->
         ("A" :> Row("acme", "alice", "", "", "", "")) @@
         ("D" :> Row("acme", "alice", "", "", "", ""))
    [] Scenario = "fullrevoke" ->
         ("A" :> Row("acme", "alice", "", "", "", "")) @@
         ("K" :> Row("acme", "bob", "A", "alice", "", ""))
    [] Scenario = "nested_step" ->
         ("A" :> Row("acme", "alice", "", "", "", "")) @@
         ("G" :> Row("globex", "carol", "", "", "", "")) @@
         ("Ia" :> Row("acme", "alice", "", "carol", "globex", "G")) @@
         ("Ig" :> Row("globex", "carol", "", "alice", "acme", "A"))
    [] Scenario = "nested_start" ->
         ("A" :> Row("acme", "alice", "", "", "", "")) @@
         ("G" :> Row("globex", "carol", "", "", "", ""))
    [] Scenario = "f1_i20" ->
         ("B" :> Row("acme", "bob", "", "", "", "")) @@
         ("Bd" :> Row("acme", "bob", "", "", "", "")) @@
         ("I" :> Row("acme", "bob", "", "bob", "acme", "B")) @@
         ("Ig" :> Row("globex", "carol", "", "bob", "acme", "B"))
    [] Scenario = "free" ->
         ("A" :> Row("acme", "alice", "", "", "", "")) @@
         ("K" :> Row("acme", "bob", "A", "alice", "", "")) @@
         ("Ig" :> Row("globex", "carol", "", "alice", "acme", "A"))

InitJtis == DOMAIN InitRows
Jtis == InitJtis \cup NewJtis
\* Lockable resources: token rows, user rows, and the advisory lock per tenant.
Res == Jtis \cup Users \cup Tenants

\* The operations each process may run.  A process presents only tokens of
\* the initial state or tokens a lower-numbered process is issued.  alice
\* holds platform-admin; Adm are the tokens that carry her identity.
FreeOps(p) ==
  LET Have == IF p = 1 THEN {"A", "K"} ELSE {"A", "K", "N1"}
      Adm  == IF p = 1 THEN {"A"} ELSE {"A", "N1"}
  IN  {Exch(t, t, "", TRUE) : t \in Have}
      \cup {Exch(t, t, "", FALSE) : t \in Have}
      \cup {Exch(t, "K", t, FALSE) : t \in Adm}
      \cup {Pg(t) : t \in Have}
      \cup {Rev(c, rt) : c \in Adm, rt \in Have}
      \cup {Rev("K", "K")}
      \cup {Imp(t, "acme", "bob") : t \in Adm}
      \cup {Imp(t, "globex", "dave") : t \in Adm}
      \cup {Full("alice"), Full("bob"), EndImp("Ig")}

\* The i-th set is the operations of process i.
OpsTable ==
  CASE Scenario = "cascade_issuance" ->
         << {Rev("C", "L")},
            {Exch("L", "L", "", TRUE)},
            {Exch("L", "L", "", FALSE), Pg("L"), Exch("N2", "C", "N2", FALSE)} >>
    [] Scenario = "cascade_rotation" ->
         << {Rev("C", "L")}, {Exch("L", "L", "", TRUE)} >>
    [] Scenario = "rotation" ->
         << {Exch("L", "L", "", TRUE)}, {Exch("L", "L", "", TRUE)}, {Exch("L", "S", "L", FALSE)} >>
    [] Scenario = "cache" ->
         << {Rev("C", "L")}, {Exch("L", "L", "", TRUE)} >>
    [] Scenario = "mutual" ->
         << {Rev("C1", "C2")}, {Rev("C2", "C1")}, {Rev("C1", "C1"), Exch("C2", "C2", "", TRUE)} >>
    [] Scenario = "impersonation" ->
         << {Rev("D", "A")},
            {Imp("A", "globex", "carol")},
            {Imp("A", "acme", "bob"), Exch("A", "A", "", TRUE)} >>
    [] Scenario = "fullrevoke" ->
         << {Full("alice")},
            {Exch("K", "K", "", FALSE), Exch("A", "K", "A", FALSE)},
            {Exch("A", "A", "", TRUE), Pg("A"), Imp("A", "globex", "carol")} >>
    [] Scenario = "nested_step" ->
         << {Rev("Ia", "A")}, {Rev("Ig", "G")} >>
    [] Scenario = "nested_start" ->
         << {Imp("A", "globex", "carol")}, {Imp("G", "acme", "alice")}, {Full("alice"), Full("carol")} >>
    [] Scenario = "f1_i20" ->
         << {Rev("I", "B")}, {Rev("Bd", "I")} >>
    [] Scenario = "free" -> << FreeOps(1), FreeOps(2) >>

Ops(p) == OpsTable[p]

Reps == IF Scenario = "cache" THEN {1, 2} ELSE {}

\* Start records of the initial impersonation rows (TF writes them in rule 1 (c)).
InitStarts ==
  IF IsTF
  THEN {[ten |-> InitRows[j].iten, ijti |-> InitRows[j].ijti, target |-> InitRows[j].ten, bjti |-> j] :
          j \in {k \in InitJtis : IsImp(InitRows[k]) /\ InitRows[k].ijti /= ""}}
  ELSE {}

(***************************************************************************)
(* Transactions.  A process runs at most two open transactions: the outer  *)
(* one at level 1 and a nested one at level 2.                             *)
(***************************************************************************)
TxIds == Procs \X {1, 2}
NoTx == <<0, 0>>
NoWant == <<"", "">>
EmptyW == [j \in {} |-> NoRow]
CascadeAttempts == 3   \* cascadeDeadlockAttempts in issuedtokenstore.go

(* --algorithm TokenConcurrency

variables
  \* The operation each request process runs.
  op \in {f \in [Procs -> UNION {Ops(q) : q \in Procs}] : \A q \in Procs : f[q] \in Ops(q)},
  \* Committed state.
  tok = [j \in Jtis |-> IF j \in InitJtis THEN InitRows[j] ELSE NoRow],
  aud = {},                       \* jtis whose token.exchanged row committed
  starts = InitStarts,            \* committed start records (TF)
  inval = {},                     \* users whose invalidation level committed
  \* Open transactions and their private writes.
  open = [t \in TxIds |-> FALSE],
  aborted = [t \in TxIds |-> ""],
  wtok = [t \in TxIds |-> EmptyW],
  waud = [t \in TxIds |-> {}],
  wst = [t \in TxIds |-> {}],
  winv = [t \in TxIds |-> {}],
  lvl = [p \in Procs |-> 1],
  \* Locks: share holders, the update holder, and the lock a transaction waits for.
  sh = [r \in Res |-> {}],
  ex = [r \in Res |-> NoTx],
  want = [t \in TxIds |-> NoWant],
  nAborts = 0,
  \* What the clients hold and what the requests did, read by the properties.
  issued = {},
  finished = [p \in Procs |-> FALSE],
  res = [p \in Procs |-> ""],
  callerOf = [p \in Procs |-> ""],
  rootOf = [p \in Procs |-> ""],
  tenOf = [p \in Procs |-> ""],
  exchJ = {},
  rotated = {},
  srcRevAtRead = [p \in Procs |-> {}],
  stepTree = [p \in Procs |-> {}],
  stepRev = [p \in Procs |-> {}],
  stepRead = [p \in Procs |-> FALSE],
  cascCommitted = [p \in Procs |-> FALSE],
  cascDone = {},
  callerAtAccept = [p \in Procs |-> ""],
  impChk = [p \in Procs |-> {}],
  impAfter = [p \in Procs |-> {}],
  fullDone = {},
  badStep = FALSE,
  badStepFail = FALSE,
  badNested = FALSE,
  revAll = FALSE,
  \* Replicas and the event bus.
  inflight = {},
  lost = {},
  cache = [r \in Reps |-> {}],
  up = [r \in Reps |-> TRUE],
  reading = [r \in Reps |-> FALSE],
  readSet = [r \in Reps |-> {}],
  sinceRead = [r \in Reps |-> {}],
  seen = [r \in Reps |-> {}],
  nLoss = 0,
  nRestart = 0;

define
  T(p) == <<p, lvl[p]>>
  Ab(p) == aborted[T(p)] /= ""
  \* The row j as transaction t sees it: its own write, else the latest commit.
  V(t, j) == IF j \in DOMAIN wtok[t] THEN wtok[t][j] ELSE tok[j]
  View(t) == [j \in Jtis |-> V(t, j)]
  InvalIn(t, u) == u \in inval \cup winv[t]
  StartsIn(t) == starts \cup wst[t]
  \* The lineage subtree of the jtis in S inside tenant ten, under view vw.
  RECURSIVE TreeOf(_, _, _)
  TreeOf(vw, S, ten) ==
    LET S2 == S \cup {j \in Jtis : vw[j].ex /\ vw[j].ten = ten /\ vw[j].par \in S}
    IN  IF S2 = S THEN S ELSE TreeOf(vw, S2, ten)
  LiveIn(vw) == {j \in Jtis : vw[j].ex /\ ~vw[j].rev}
  \* RevokeBySubject's seed; TF adds the delegations of TF-3.
  Seeds(vw, u, ten) ==
    {j \in Jtis : vw[j].ex /\ vw[j].ten = ten /\ vw[j].sub = u}
    \cup (IF IsTF THEN {j \in Jtis : vw[j].ex /\ vw[j].ten = ten /\ vw[j].act = u /\ vw[j].par /= ""}
          ELSE {})
  Avail(j) == j = "" \/ j \in InitJtis \/ j \in issued
  NeverAvail(j) == ~Avail(j) /\ \E q \in Procs : NewJ(q) = j /\ finished[q]
  Apply(t) == [j \in Jtis |-> V(t, j)]
  NewRevs(t) == {j \in DOMAIN wtok[t] : wtok[t][j].rev /\ ~tok[j].rev}
  CanLock(t, r, m) ==
    IF m = "s" THEN ex[r] \in {NoTx, t}
    ELSE ex[r] \in {NoTx, t} /\ sh[r] \subseteq {t}
  CommittedRevoked == {j \in Jtis : tok[j].ex /\ tok[j].rev}
  AllReqDone == \A p \in Procs : finished[p]

  (*************************************************************************)
  (* The wait-for graph.  A lock edge t -> u: t waits for a lock u holds.  *)
  (* An application edge: an outer transaction waits for its nested one.  *)
  (*************************************************************************)
  Blocks(t, u) ==
    /\ want[t] /= NoWant /\ open[t] /\ open[u] /\ t /= u
    /\ \/ ex[want[t][1]] = u
       \/ want[t][2] = "x" /\ u \in sh[want[t][1]]
  LockEdges == {e \in TxIds \X TxIds : Blocks(e[1], e[2])}
  AppEdges == {<<<<p, 1>>, <<p, 2>>>> : p \in {q \in Procs : open[<<q, 1>>] /\ open[<<q, 2>>]}}
  Comp(R, S) == {<<a, c>> \in TxIds \X TxIds : \E b \in TxIds : <<a, b>> \in R /\ <<b, c>> \in S}
  TC(R) ==
    IF R = {} THEN {}
    ELSE LET R2 == R \cup Comp(R, R)
             R4 == R2 \cup Comp(R2, R2)
             R8 == R4 \cup Comp(R4, R4)
         IN  R8
  CycleMembers ==
    LET E == LockEdges
    IN  IF Cardinality(E) < 2 THEN {} ELSE {t \in TxIds : <<t, t>> \in TC(E)}
end define;

macro Begin(l, ten) begin
  lvl[self] := l;
  open[<<self, l>>] := TRUE;
  aborted[<<self, l>>] := "";
  wtok[<<self, l>>] := EmptyW;
  waud[<<self, l>>] := {};
  wst[<<self, l>>] := {};
  winv[<<self, l>>] := {};
end macro;

macro Grant(t, r, m) begin
  if m = "s" then sh[r] := sh[r] \cup {t}; else ex[r] := t; end if;
end macro;

macro Rollback(t) begin
  sh := [r \in Res |-> sh[r] \ {t}];
  ex := [r \in Res |-> IF ex[r] = t THEN NoTx ELSE ex[r]];
  open[t] := FALSE;
  wtok[t] := EmptyW;
  waud[t] := {};
  wst[t] := {};
  winv[t] := {};
end macro;

\* COMMIT: the writes become visible at once and every lock is released.
\* The Token Service publishes each revoked jti after the commit; one event
\* per replica goes on the bus.
macro Commit(t) begin
  inflight := inflight \cup (Reps \X NewRevs(t));
  tok := Apply(t);
  aud := aud \cup waud[t];
  starts := starts \cup wst[t];
  inval := inval \cup winv[t];
  sh := [r \in Res |-> sh[r] \ {t}];
  ex := [r \in Res |-> IF ex[r] = t THEN NoTx ELSE ex[r]];
  open[t] := FALSE;
  wtok[t] := EmptyW;
end macro;

(***************************************************************************)
(* The deadlock detector and failures.  A cycle of lock waits is broken by *)
(* aborting one of its transactions with 40P01; an application wait is    *)
(* invisible to it.  A failure aborts any open transaction, up to         *)
(* MaxAborts per behavior.  The process sees the abort at its next step.  *)
(***************************************************************************)
fair process det = 100
begin
Det:
  while TRUE do
    either
      await CycleMembers /= {};
      with t \in CycleMembers do
        aborted[t] := "deadlock";
        want[t] := NoWant;
        Rollback(t);
      end with;
    or
      \* A failed statement aborts the transaction a request is running.
      await nAborts < MaxAborts;
      with t \in {u \in {<<p, lvl[p]>> : p \in Procs} : open[u]} do
        aborted[t] := "fail";
        want[t] := NoWant;
        nAborts := nAborts + 1;
        Rollback(t);
      end with;
    or
      \* A primary failover aborts every open transaction.
      await nAborts < MaxAborts /\ \E u \in TxIds : open[u];
      aborted := [u \in TxIds |-> IF open[u] THEN "fail" ELSE aborted[u]];
      want := [u \in TxIds |-> NoWant];
      nAborts := nAborts + 1;
      sh := [r \in Res |-> {}];
      ex := [r \in Res |-> NoTx];
      open := [u \in TxIds |-> FALSE];
      wtok := [u \in TxIds |-> EmptyW];
      waud := [u \in TxIds |-> {}];
      wst := [u \in TxIds |-> {}];
      winv := [u \in TxIds |-> {}];
    or
      await AllReqDone;
      goto Done;
    end either;
  end while;
end process;

(***************************************************************************)
(* The event bus and the replicas' revocation caches                       *)
(* (pkg/credential/revocation).  An event is delivered or lost; a replica  *)
(* restarts with an empty cache; a rehydration reads the committed revoked *)
(* rows and keeps the events delivered while it read.                      *)
(***************************************************************************)
fair process bus \in (IF Reps = {} THEN {} ELSE {101})
begin
Bus:
  while TRUE do
    either
      with e \in inflight do
        cache[e[1]] := cache[e[1]] \cup {e[2]};
        sinceRead[e[1]] := IF reading[e[1]] THEN sinceRead[e[1]] \cup {e[2]} ELSE sinceRead[e[1]];
        inflight := inflight \ {e};
      end with;
    or
      await nLoss < MaxLoss;
      with e \in inflight do
        lost := lost \cup {e};
        inflight := inflight \ {e};
        nLoss := nLoss + 1;
      end with;
    or
      await nRestart < MaxRestarts;
      with r \in Reps do
        cache[r] := {};
        up[r] := FALSE;
        reading[r] := FALSE;
        seen[r] := {};
        nRestart := nRestart + 1;
      end with;
    or
      with r \in {q \in Reps : ~reading[q] /\ (~up[q] \/ ~(CommittedRevoked \subseteq cache[q]))} do
        reading[r] := TRUE;
        sinceRead[r] := {};
        readSet[r] := CommittedRevoked;
      end with;
    or
      with r \in {q \in Reps : reading[q]} do
        cache[r] := readSet[r] \cup sinceRead[r];
        seen[r] := readSet[r];
        reading[r] := FALSE;
        up[r] := TRUE;
      end with;
    or
      await AllReqDone /\ inflight = {}
            /\ \A r \in Reps : ~reading[r] /\ up[r] /\ cache[r] = CommittedRevoked;
      goto Done;
    end either;
  end while;
end process;

(***************************************************************************)
(* Request processes.                                                      *)
(***************************************************************************)
fair process req \in Procs
variables
  ten = "", phase = "", attempts = 0, retries = 0, sAtt = 0,
  snap = {}, cur = "", tgts = {}, curT = "", uq = <<>>;
begin
Start:
  \* A client presents only tokens it was issued.
  await (\A j \in {op[self].caller, op[self].subj, op[self].actor, op[self].root, op[self].cb} : Avail(j))
        \/ (\E j \in {op[self].caller, op[self].subj, op[self].actor, op[self].root, op[self].cb} : NeverAvail(j));
  if \E j \in {op[self].caller, op[self].subj, op[self].actor, op[self].root, op[self].cb} : NeverAvail(j) then
    res[self] := "no_token";
    finished[self] := TRUE; goto Done;
  elsif Cardinality({tok[j].ten : j \in {op[self].caller, op[self].subj, op[self].actor, op[self].root} \ {""}}) > 1 then
    \* Tenant-scope enforcement refuses a request whose tokens span tenants.
    res[self] := "refused_tenant";
    finished[self] := TRUE; goto Done;
  elsif op[self].kind \in {"exch", "pg"} then
    ten := tok[op[self].subj].ten;
    goto XBegin;
  elsif op[self].kind = "rev" then
    ten := tok[op[self].root].ten;
    callerOf[self] := op[self].caller;
    rootOf[self] := op[self].root;
    tenOf[self] := tok[op[self].root].ten;
    if IsTF then goto TOuter; else goto MSub; end if;
  elsif op[self].kind = "imp" then
    goto IChk;
  elsif op[self].kind = "full" then
    ten := UTen[op[self].user];
    goto FLevel;
  else
    goto EBegin;
  end if;

\* ---- An exchange (RecordWithAudit, RecordWithRotationAudit) or a playground
\* ---- mint (RecordWithSources): one issuance transaction.
XBegin:
  Begin(1, ten);
  want[<<self, 1>>] := <<ten, "x">>;
  if op[self].kind = "exch" then exchJ := exchJ \cup {NewJ(self)}; end if;
XAdv:
  await CanLock(T(self), ten, "x") \/ Ab(self);
  if Ab(self) then goto Fail;
  else Grant(T(self), ten, "x"); want[T(self)] := NoWant;
  end if;
X1a:
  \* step (1a): one SELECT of the source rows, with no row lock.
  if Ab(self) then goto Fail;
  elsif \E s \in {op[self].subj, op[self].actor, op[self].caller} \ {""} : V(T(self), s).ex /\ V(T(self), s).rev then
    Rollback(T(self)); res[self] := "refused_revoked"; finished[self] := TRUE; goto Done;
  elsif \E s \in {op[self].subj, op[self].actor} \ {""} : IsImp(V(T(self), s)) then
    Rollback(T(self)); res[self] := "refused_impersonation"; finished[self] := TRUE; goto Done;
  else
    srcRevAtRead[self] := {s \in {op[self].subj, op[self].actor, op[self].caller} \ {""} : tok[s].ex /\ tok[s].rev};
    if IsTF /\ op[self].kind = "exch" then
      \* TF-4: the user rows of the subject, the actor, and the caller.
      uq := LET s1 == <<tok[op[self].subj].sub>>
                s2 == IF op[self].actor /= "" /\ tok[op[self].actor].sub \notin {tok[op[self].subj].sub}
                      THEN s1 \o <<tok[op[self].actor].sub>> ELSE s1
                cs == tok[op[self].caller].sub
            IN  IF \E i \in 1..Len(s2) : s2[i] = cs THEN s2 ELSE s2 \o <<cs>>;
      want[T(self)] := <<Head(uq), "s">>;
      goto XUserLock;
    else
      goto XInsert;
    end if;
  end if;
XUserLock:
  await CanLock(T(self), Head(uq), "s") \/ Ab(self);
  if Ab(self) then goto Fail;
  elsif InvalIn(T(self), Head(uq)) then
    want[T(self)] := NoWant; Rollback(T(self)); res[self] := "refused_user"; finished[self] := TRUE; goto Done;
  else
    Grant(T(self), Head(uq), "s");
    if Len(uq) > 1 then
      want[T(self)] := <<uq[2], "s">>; uq := Tail(uq); goto XUserLock;
    else
      want[T(self)] := NoWant; uq := <<>>;
    end if;
  end if;
XInsert:
  \* INSERT the token.exchanged row and the issued_tokens row.
  if Ab(self) then goto Fail;
  else
    if op[self].kind = "exch" then waud[T(self)] := {NewJ(self)}; end if;
    wtok[T(self)] := (NewJ(self) :> Row(ten, tok[op[self].subj].sub,
                         IF op[self].actor /= "" THEN op[self].actor ELSE op[self].subj,
                         IF op[self].actor /= "" THEN tok[op[self].actor].sub ELSE "", "", ""))
                     @@ wtok[T(self)];
    if op[self].rot then want[T(self)] := <<op[self].subj, "x">>; goto XRotLock;
    else goto XCommit;
    end if;
  end if;
XRotLock:
  \* revokePrevious: UPDATE the replaced row WHERE revoked_at IS NULL.
  await CanLock(T(self), op[self].subj, "x") \/ Ab(self);
  if Ab(self) then goto Fail;
  elsif V(T(self), op[self].subj).rev then
    want[T(self)] := NoWant; Rollback(T(self)); res[self] := "refused_revoked"; finished[self] := TRUE; goto Done;
  else
    Grant(T(self), op[self].subj, "x"); want[T(self)] := NoWant;
    wtok[T(self)] := (op[self].subj :> RevRow(V(T(self), op[self].subj), self)) @@ wtok[T(self)];
  end if;
XCommit:
  if Ab(self) then goto Fail;
  else
    if op[self].rot then rotated := rotated \cup {<<self, op[self].subj>>}; end if;
    Commit(T(self));
    issued := issued \cup {NewJ(self)};
    res[self] := "issued";
    finished[self] := TRUE; goto Done;
  end if;

\* ---- A recursive-revocation request.
\* Main: LineageSubtree (caller FOR SHARE, then the subtree read).
MSub:
  phase := "step"; stepRev[self] := {}; stepRead[self] := FALSE;
  Begin(1, ten);
  want[<<self, 1>>] := <<op[self].caller, "s">>;
MSubCaller:
  await CanLock(T(self), op[self].caller, "s") \/ Ab(self);
  if Ab(self) then goto Fail;
  elsif V(T(self), op[self].caller).ex /\ V(T(self), op[self].caller).rev then
    want[T(self)] := NoWant; Rollback(T(self)); res[self] := "refused_caller"; finished[self] := TRUE; goto Done;
  else Grant(T(self), op[self].caller, "s"); want[T(self)] := NoWant;
  end if;
MSubRead:
  if Ab(self) then goto Fail;
  else
    stepTree[self] := TreeOf(View(T(self)), {op[self].root}, ten);
    Commit(T(self));
  end if;
\* Main: RevokeImpersonationsByImpersonator, one UPDATE under __all__.
MImp:
  Begin(1, "ALL");
  revAll := TRUE;
  stepRead[self] := TRUE;
  snap := {j \in Jtis : IsImp(tok[j]) /\ ~tok[j].rev /\ tok[j].iten = ten /\ tok[j].ijti \in stepTree[self]};
MImpRow:
  if Ab(self) then goto Fail;
  elsif snap = {} then goto MImpCommit;
  else with j \in snap do cur := j; want[T(self)] := <<j, "x">>; end with;
  end if;
MImpLock:
  await CanLock(T(self), cur, "x") \/ Ab(self);
  if Ab(self) then goto Fail;
  else
    Grant(T(self), cur, "x"); want[T(self)] := NoWant;
    if ~V(T(self), cur).rev then wtok[T(self)] := (cur :> RevRow(V(T(self), cur), self)) @@ wtok[T(self)]; end if;
    snap := snap \ {cur};
    goto MImpRow;
  end if;
MImpCommit:
  if Ab(self) then goto Fail;
  else
    stepRev[self] := {j \in DOMAIN wtok[T(self)] : tok[j].ten = ten};
    if DOMAIN wtok[T(self)] /= {} /\ tok[op[self].caller].ex /\ tok[op[self].caller].rev /\ tok[op[self].caller].by /= self then
      badStep := TRUE;
    end if;
    Commit(T(self));
    goto Casc;
  end if;

\* TF: the outer transaction of TF-2 in the request's tenant.  Its updates
\* in each other target tenant run in the same transaction under that
\* tenant's context (SET LOCAL app.current_tenant, TF-5), so every wait of the
\* step is a lock wait.  A 40P01 abort retries the step up to CascadeAttempts.
TOuter:
  phase := "step"; stepRev[self] := {}; stepRead[self] := FALSE;
  sAtt := sAtt + 1;
  Begin(1, ten);
  want[<<self, 1>>] := <<op[self].caller, "s">>;
TOuterCaller:
  await CanLock(T(self), op[self].caller, "s") \/ Ab(self);
  if Ab(self) then goto Fail;
  elsif V(T(self), op[self].caller).ex /\ V(T(self), op[self].caller).rev then
    want[T(self)] := NoWant; Rollback(T(self)); res[self] := "refused_caller"; finished[self] := TRUE; goto Done;
  else Grant(T(self), op[self].caller, "s"); want[T(self)] := <<tok[op[self].caller].sub, "s">>;
  end if;
TOuterUser:
  await CanLock(T(self), tok[op[self].caller].sub, "s") \/ Ab(self);
  if Ab(self) then goto Fail;
  elsif InvalIn(T(self), tok[op[self].caller].sub) then
    want[T(self)] := NoWant; Rollback(T(self)); res[self] := "refused_user"; finished[self] := TRUE; goto Done;
  else Grant(T(self), tok[op[self].caller].sub, "s"); want[T(self)] := NoWant;
  end if;
TOuterTree:
  if Ab(self) then goto Fail;
  else stepTree[self] := TreeOf(View(T(self)), {op[self].root}, ten);
  end if;
TOuterStarts:
  if Ab(self) then goto Fail;
  else
    stepRead[self] := TRUE;
    tgts := {s.target : s \in {r \in StartsIn(T(self)) : r.ten = ten /\ r.ijti \in stepTree[self]}} \ {ten};
  end if;
TOuterSnap:
  if Ab(self) then goto Fail;
  else
    snap := {j \in Jtis : IsImp(V(T(self), j)) /\ ~V(T(self), j).rev /\ V(T(self), j).ten = ten
                          /\ V(T(self), j).iten = ten /\ V(T(self), j).ijti \in stepTree[self]};
  end if;
TOuterRow:
  if Ab(self) then goto Fail;
  elsif snap = {} then goto TNext;
  else with j \in snap do cur := j; want[T(self)] := <<j, "x">>; end with;
  end if;
TOuterLock:
  await CanLock(T(self), cur, "x") \/ Ab(self);
  if Ab(self) then goto Fail;
  else
    Grant(T(self), cur, "x"); want[T(self)] := NoWant;
    if ~V(T(self), cur).rev then wtok[T(self)] := (cur :> RevRow(V(T(self), cur), self)) @@ wtok[T(self)]; end if;
    snap := snap \ {cur};
    goto TOuterRow;
  end if;
TNext:
  if Ab(self) then goto Fail;
  elsif tgts = {} then goto TOuterCommit;
  else with g \in tgts do curT := g; tgts := tgts \ {g}; end with;
  end if;
TTarget:
  \* SET LOCAL app.current_tenant to the target tenant, and the snapshot of
  \* the UPDATE that runs under it in the outer transaction.
  if Ab(self) then goto Fail;
  else
    snap := {j \in Jtis : IsImp(V(T(self), j)) /\ ~V(T(self), j).rev /\ V(T(self), j).ten = curT
                          /\ V(T(self), j).iten = ten /\ V(T(self), j).ijti \in stepTree[self]};
  end if;
TTargetRow:
  if Ab(self) then goto Fail;
  elsif snap = {} then goto TNext;
  else with j \in snap do cur := j; want[T(self)] := <<j, "x">>; end with;
  end if;
TTargetLock:
  await CanLock(T(self), cur, "x") \/ Ab(self);
  if Ab(self) then goto Fail;
  else
    Grant(T(self), cur, "x"); want[T(self)] := NoWant;
    if ~V(T(self), cur).rev then wtok[T(self)] := (cur :> RevRow(V(T(self), cur), self)) @@ wtok[T(self)]; end if;
    snap := snap \ {cur};
    goto TTargetRow;
  end if;
TOuterCommit:
  if Ab(self) then goto Fail;
  else
    stepRev[self] := {j \in DOMAIN wtok[T(self)] : wtok[T(self)][j].ten = ten};
    if \E j \in DOMAIN wtok[T(self)] :
         /\ wtok[T(self)][j].ten /= ten
         /\ ~(/\ IsImp(wtok[T(self)][j]) /\ wtok[T(self)][j].iten = ten
              /\ wtok[T(self)][j].ijti \in stepTree[self]
              /\ \E r \in StartsIn(T(self)) : r.ten = ten /\ r.target = wtok[T(self)][j].ten /\ r.ijti \in stepTree[self]) then
      badNested := TRUE;
    end if;
    if DOMAIN wtok[T(self)] /= {} /\ tok[op[self].caller].ex /\ tok[op[self].caller].rev /\ tok[op[self].caller].by /= self then
      badStep := TRUE;
    end if;
    Commit(T(self));
  end if;

\* The cascade: RevokeLineage, retried on 40P01 up to CascadeAttempts times.
Casc:
  phase := "casc";
  attempts := attempts + 1;
  Begin(1, ten);
  if IsTF then want[<<self, 1>>] := <<ten, "x">>; goto CascAdv;
  else want[<<self, 1>>] := <<op[self].caller, "s">>; goto CascCaller;
  end if;
CascAdv:
  await CanLock(T(self), ten, "x") \/ Ab(self);
  if Ab(self) then goto Fail;
  else Grant(T(self), ten, "x"); want[T(self)] := <<op[self].caller, "s">>;
  end if;
CascCaller:
  \* checkCallerLive; TF-1 accepts a caller in the step revoked set.
  await CanLock(T(self), op[self].caller, "s") \/ Ab(self);
  if Ab(self) then goto Fail;
  elsif V(T(self), op[self].caller).ex /\ V(T(self), op[self].caller).rev /\ ~(IsTF /\ op[self].caller \in stepRev[self]) then
    want[T(self)] := NoWant; Rollback(T(self)); res[self] := "refused_caller"; finished[self] := TRUE; goto Done;
  else
    Grant(T(self), op[self].caller, "s");
    if IsTF then want[T(self)] := <<tok[op[self].caller].sub, "s">>; goto CascUser;
    else want[T(self)] := NoWant; goto CascSnap;
    end if;
  end if;
CascUser:
  await CanLock(T(self), tok[op[self].caller].sub, "s") \/ Ab(self);
  if Ab(self) then goto Fail;
  elsif InvalIn(T(self), tok[op[self].caller].sub) then
    want[T(self)] := NoWant; Rollback(T(self)); res[self] := "refused_user"; finished[self] := TRUE; goto Done;
  else Grant(T(self), tok[op[self].caller].sub, "s"); want[T(self)] := NoWant;
  end if;
CascSnap:
  \* ensureRoot, then the snapshot of the recursive UPDATE.
  if Ab(self) then goto Fail;
  else snap := TreeOf(View(T(self)), {op[self].root}, ten) \cap LiveIn(View(T(self)));
  end if;
CascRow:
  if Ab(self) then goto Fail;
  elsif snap = {} then goto CascCommit;
  else with j \in snap do cur := j; want[T(self)] := <<j, "x">>; end with;
  end if;
CascLock:
  await CanLock(T(self), cur, "x") \/ Ab(self);
  if Ab(self) then goto Fail;
  else
    Grant(T(self), cur, "x"); want[T(self)] := NoWant;
    if ~V(T(self), cur).rev then wtok[T(self)] := (cur :> RevRow(V(T(self), cur), self)) @@ wtok[T(self)]; end if;
    snap := snap \ {cur};
    goto CascRow;
  end if;
CascCommit:
  if Ab(self) then goto Fail;
  else
    callerAtAccept[self] :=
      IF ~V(T(self), op[self].caller).ex \/ ~V(T(self), op[self].caller).rev THEN "live"
      ELSE IF V(T(self), op[self].caller).by = self THEN "own" ELSE "other";
    cascDone := cascDone \cup {[p |-> self, root |-> op[self].root, ten |-> ten,
                                tree |-> TreeOf(View(T(self)), {op[self].root}, ten)]};
    Commit(T(self));
    cascCommitted[self] := TRUE;
    res[self] := "accepted";
    finished[self] := TRUE; goto Done;
  end if;

\* ---- A platform-admin impersonation start (rule 1).
IChk:
  \* Main: checkCaller, an unlocked read of the caller bearer's row.  TF reads
  \* the row under a share lock in the first transaction instead (TF-7).
  ten := tok[op[self].cb].ten;
  if IsTF then goto IOuter;
  else
    impChk[self] := {r \in Procs : ~cascCommitted[r]};
    if tok[op[self].cb].rev \/ IsImp(tok[op[self].cb]) then
      res[self] := "refused_caller"; finished[self] := TRUE; goto Done;
    else
      srcRevAtRead[self] := {s \in {op[self].cb} : tok[s].rev};
    end if;
  end if;
IOuter:
  phase := "imp";
  Begin(1, ten);
  if IsTF then want[<<self, 1>>] := <<op[self].cb, "s">>; goto IOuterCb;
  else want[<<self, 1>>] := <<tok[op[self].cb].sub, "s">>; goto IOuterUser;
  end if;
IOuterCb:
  \* TF-7: the caller bearer's row under a share lock, refused as step (1a)
  \* refuses a subject_token.
  await CanLock(T(self), op[self].cb, "s") \/ Ab(self);
  if Ab(self) then goto Fail;
  elsif V(T(self), op[self].cb).rev \/ IsImp(V(T(self), op[self].cb)) then
    want[T(self)] := NoWant; Rollback(T(self)); res[self] := "refused_caller"; finished[self] := TRUE; goto Done;
  else
    Grant(T(self), op[self].cb, "s");
    impChk[self] := {r \in Procs : ~cascCommitted[r]};
    srcRevAtRead[self] := {s \in {op[self].cb} : tok[s].rev};
    want[T(self)] := <<tok[op[self].cb].sub, "s">>;
  end if;
IOuterUser:
  await CanLock(T(self), tok[op[self].cb].sub, "s") \/ Ab(self);
  if Ab(self) then goto Fail;
  elsif InvalIn(T(self), tok[op[self].cb].sub) then
    want[T(self)] := NoWant; Rollback(T(self)); res[self] := "refused_user"; finished[self] := TRUE; goto Done;
  else
    Grant(T(self), tok[op[self].cb].sub, "s"); want[T(self)] := NoWant;
    if IsTF then
      wst[T(self)] := {[ten |-> ten, ijti |-> op[self].cb, target |-> op[self].tt, bjti |-> NewJ(self)]};
    end if;
  end if;
INested:
  if Ab(self) then goto Fail;
  else
    Begin(2, op[self].tt);
    want[<<self, 2>>] := <<op[self].tu, "s">>;
  end if;
INestedUser:
  await CanLock(T(self), op[self].tu, "s") \/ Ab(self);
  if Ab(self) then goto Fail;
  elsif InvalIn(T(self), op[self].tu) then
    want[T(self)] := NoWant; Rollback(T(self)); res[self] := "refused_user"; goto Quit;
  else
    Grant(T(self), op[self].tu, "s"); want[T(self)] := NoWant;
    wtok[T(self)] := (NewJ(self) :> Row(op[self].tt, op[self].tu, "", tok[op[self].cb].sub, ten, op[self].cb)) @@ wtok[T(self)];
  end if;
INestedCommit:
  if Ab(self) then goto Fail;
  else
    Commit(T(self));
    lvl[self] := 1;
    if ~IsTF then impAfter[self] := {r \in Procs : stepRead[r]}; end if;
  end if;
IOuterCommit:
  if Ab(self) then goto Fail;
  else
    Commit(T(self));
    \* TF: the start record commits.  The requests whose step has read the
    \* start records and whose cascade has not committed miss it.
    if IsTF then impAfter[self] := {r \in Procs : stepRead[r] /\ ~cascCommitted[r]}; end if;
    issued := issued \cup {NewJ(self)};
    res[self] := "issued";
    finished[self] := TRUE; goto Done;
  end if;

\* ---- A full revoke (§11.4): the level, then step 5.
FLevel:
  phase := "full";
  Begin(1, ten);
  want[<<self, 1>>] := <<op[self].user, "x">>;
FLevelLock:
  await CanLock(T(self), op[self].user, "x") \/ Ab(self);
  if Ab(self) then goto Fail;
  else Grant(T(self), op[self].user, "x"); want[T(self)] := NoWant; winv[T(self)] := {op[self].user};
  end if;
FLevelCommit:
  if Ab(self) then goto Fail; else Commit(T(self)); end if;
FSub:
  \* RevokeBySubject.
  Begin(1, ten);
  if IsTF then want[<<self, 1>>] := <<ten, "x">>; goto FSubAdv;
  else goto FSubSnap;
  end if;
FSubAdv:
  await CanLock(T(self), ten, "x") \/ Ab(self);
  if Ab(self) then goto Fail;
  else Grant(T(self), ten, "x"); want[T(self)] := NoWant;
  end if;
FSubSnap:
  if Ab(self) then goto Fail;
  else snap := TreeOf(View(T(self)), Seeds(View(T(self)), op[self].user, ten), ten) \cap LiveIn(View(T(self)));
  end if;
FSubRow:
  if Ab(self) then goto Fail;
  elsif snap = {} then goto FSubCommit;
  else with j \in snap do cur := j; want[T(self)] := <<j, "x">>; end with;
  end if;
FSubLock:
  await CanLock(T(self), cur, "x") \/ Ab(self);
  if Ab(self) then goto Fail;
  else
    Grant(T(self), cur, "x"); want[T(self)] := NoWant;
    if ~V(T(self), cur).rev then wtok[T(self)] := (cur :> RevRow(V(T(self), cur), self)) @@ wtok[T(self)]; end if;
    snap := snap \ {cur};
    goto FSubRow;
  end if;
FSubCommit:
  if Ab(self) then goto Fail; else Commit(T(self)); end if;
FAct:
  \* RevokeImpersonationsByActor, under __all__ for a platform-admin caller.
  Begin(1, "ALL");
  snap := {j \in Jtis : IsImp(tok[j]) /\ ~tok[j].rev /\ tok[j].act = op[self].user};
FActRow:
  if Ab(self) then goto Fail;
  elsif snap = {} then goto FActCommit;
  else with j \in snap do cur := j; want[T(self)] := <<j, "x">>; end with;
  end if;
FActLock:
  await CanLock(T(self), cur, "x") \/ Ab(self);
  if Ab(self) then goto Fail;
  else
    Grant(T(self), cur, "x"); want[T(self)] := NoWant;
    if ~V(T(self), cur).rev then wtok[T(self)] := (cur :> RevRow(V(T(self), cur), self)) @@ wtok[T(self)]; end if;
    snap := snap \ {cur};
    goto FActRow;
  end if;
FActCommit:
  if Ab(self) then goto Fail;
  else
    Commit(T(self));
    fullDone := fullDone \cup {op[self].user};
    res[self] := "done";
    finished[self] := TRUE; goto Done;
  end if;

\* ---- EndImpersonation: one conditional UPDATE under __all__.
EBegin:
  phase := "end";
  Begin(1, "ALL");
  want[<<self, 1>>] := <<op[self].root, "x">>;
ELock:
  await CanLock(T(self), op[self].root, "x") \/ Ab(self);
  if Ab(self) then goto Fail;
  else
    Grant(T(self), op[self].root, "x"); want[T(self)] := NoWant;
    if IsImp(V(T(self), op[self].root)) /\ ~V(T(self), op[self].root).rev then
      wtok[T(self)] := (op[self].root :> RevRow(V(T(self), op[self].root), self)) @@ wtok[T(self)];
    end if;
  end if;
ECommit:
  if Ab(self) then goto Fail;
  else Commit(T(self)); res[self] := "done"; finished[self] := TRUE; goto Done;
  end if;

\* ---- A failed statement: roll back what is still open, then retry or end.
Fail:
  if open[<<self, 1>>] then Rollback(<<self, 1>>); end if;
  want[<<self, 1>>] := NoWant;
  lvl[self] := 1;
  if phase = "step" /\ \E j \in Jtis : tok[j].ex /\ tok[j].ten = ten /\ tok[j].by = self then
    badStepFail := TRUE;
  end if;
Fail2:
  if op[self].kind = "rev" /\ phase = "casc" /\ aborted[<<self, 1>>] = "deadlock" /\ attempts < CascadeAttempts then
    goto Casc;
  elsif IsTF /\ op[self].kind = "rev" /\ phase = "step" /\ aborted[<<self, 1>>] = "deadlock" /\ sAtt < CascadeAttempts then
    goto TOuter;
  elsif op[self].kind = "rev" /\ phase = "step" /\ retries < MaxClientRetries then
    retries := retries + 1;
    sAtt := 0;
    if IsTF then goto TOuter; else goto MSub; end if;
  else
    res[self] := "error";
    finished[self] := TRUE; goto Done;
  end if;
Quit:
  if open[<<self, 1>>] then Rollback(<<self, 1>>); end if;
  lvl[self] := 1;
  finished[self] := TRUE;
end process;

end algorithm; *)
\* BEGIN TRANSLATION
VARIABLES op, tok, aud, starts, inval, open, aborted, wtok, waud, wst, winv, 
          lvl, sh, ex, want, nAborts, issued, finished, res, callerOf, rootOf, 
          tenOf, exchJ, rotated, srcRevAtRead, stepTree, stepRev, stepRead, 
          cascCommitted, cascDone, callerAtAccept, impChk, impAfter, fullDone, 
          badStep, badStepFail, badNested, revAll, inflight, lost, cache, up, 
          reading, readSet, sinceRead, seen, nLoss, nRestart, pc

(* define statement *)
T(p) == <<p, lvl[p]>>
Ab(p) == aborted[T(p)] /= ""

V(t, j) == IF j \in DOMAIN wtok[t] THEN wtok[t][j] ELSE tok[j]
View(t) == [j \in Jtis |-> V(t, j)]
InvalIn(t, u) == u \in inval \cup winv[t]
StartsIn(t) == starts \cup wst[t]

RECURSIVE TreeOf(_, _, _)
TreeOf(vw, S, ten) ==
  LET S2 == S \cup {j \in Jtis : vw[j].ex /\ vw[j].ten = ten /\ vw[j].par \in S}
  IN  IF S2 = S THEN S ELSE TreeOf(vw, S2, ten)
LiveIn(vw) == {j \in Jtis : vw[j].ex /\ ~vw[j].rev}

Seeds(vw, u, ten) ==
  {j \in Jtis : vw[j].ex /\ vw[j].ten = ten /\ vw[j].sub = u}
  \cup (IF IsTF THEN {j \in Jtis : vw[j].ex /\ vw[j].ten = ten /\ vw[j].act = u /\ vw[j].par /= ""}
        ELSE {})
Avail(j) == j = "" \/ j \in InitJtis \/ j \in issued
NeverAvail(j) == ~Avail(j) /\ \E q \in Procs : NewJ(q) = j /\ finished[q]
Apply(t) == [j \in Jtis |-> V(t, j)]
NewRevs(t) == {j \in DOMAIN wtok[t] : wtok[t][j].rev /\ ~tok[j].rev}
CanLock(t, r, m) ==
  IF m = "s" THEN ex[r] \in {NoTx, t}
  ELSE ex[r] \in {NoTx, t} /\ sh[r] \subseteq {t}
CommittedRevoked == {j \in Jtis : tok[j].ex /\ tok[j].rev}
AllReqDone == \A p \in Procs : finished[p]





Blocks(t, u) ==
  /\ want[t] /= NoWant /\ open[t] /\ open[u] /\ t /= u
  /\ \/ ex[want[t][1]] = u
     \/ want[t][2] = "x" /\ u \in sh[want[t][1]]
LockEdges == {e \in TxIds \X TxIds : Blocks(e[1], e[2])}
AppEdges == {<<<<p, 1>>, <<p, 2>>>> : p \in {q \in Procs : open[<<q, 1>>] /\ open[<<q, 2>>]}}
Comp(R, S) == {<<a, c>> \in TxIds \X TxIds : \E b \in TxIds : <<a, b>> \in R /\ <<b, c>> \in S}
TC(R) ==
  IF R = {} THEN {}
  ELSE LET R2 == R \cup Comp(R, R)
           R4 == R2 \cup Comp(R2, R2)
           R8 == R4 \cup Comp(R4, R4)
       IN  R8
CycleMembers ==
  LET E == LockEdges
  IN  IF Cardinality(E) < 2 THEN {} ELSE {t \in TxIds : <<t, t>> \in TC(E)}

VARIABLES ten, phase, attempts, retries, sAtt, snap, cur, tgts, curT, uq

vars == << op, tok, aud, starts, inval, open, aborted, wtok, waud, wst, winv, 
           lvl, sh, ex, want, nAborts, issued, finished, res, callerOf, 
           rootOf, tenOf, exchJ, rotated, srcRevAtRead, stepTree, stepRev, 
           stepRead, cascCommitted, cascDone, callerAtAccept, impChk, 
           impAfter, fullDone, badStep, badStepFail, badNested, revAll, 
           inflight, lost, cache, up, reading, readSet, sinceRead, seen, 
           nLoss, nRestart, pc, ten, phase, attempts, retries, sAtt, snap, 
           cur, tgts, curT, uq >>

ProcSet == {100} \cup ((IF Reps = {} THEN {} ELSE {101})) \cup (Procs)

Init == (* Global variables *)
        /\ op \in {f \in [Procs -> UNION {Ops(q) : q \in Procs}] : \A q \in Procs : f[q] \in Ops(q)}
        /\ tok = [j \in Jtis |-> IF j \in InitJtis THEN InitRows[j] ELSE NoRow]
        /\ aud = {}
        /\ starts = InitStarts
        /\ inval = {}
        /\ open = [t \in TxIds |-> FALSE]
        /\ aborted = [t \in TxIds |-> ""]
        /\ wtok = [t \in TxIds |-> EmptyW]
        /\ waud = [t \in TxIds |-> {}]
        /\ wst = [t \in TxIds |-> {}]
        /\ winv = [t \in TxIds |-> {}]
        /\ lvl = [p \in Procs |-> 1]
        /\ sh = [r \in Res |-> {}]
        /\ ex = [r \in Res |-> NoTx]
        /\ want = [t \in TxIds |-> NoWant]
        /\ nAborts = 0
        /\ issued = {}
        /\ finished = [p \in Procs |-> FALSE]
        /\ res = [p \in Procs |-> ""]
        /\ callerOf = [p \in Procs |-> ""]
        /\ rootOf = [p \in Procs |-> ""]
        /\ tenOf = [p \in Procs |-> ""]
        /\ exchJ = {}
        /\ rotated = {}
        /\ srcRevAtRead = [p \in Procs |-> {}]
        /\ stepTree = [p \in Procs |-> {}]
        /\ stepRev = [p \in Procs |-> {}]
        /\ stepRead = [p \in Procs |-> FALSE]
        /\ cascCommitted = [p \in Procs |-> FALSE]
        /\ cascDone = {}
        /\ callerAtAccept = [p \in Procs |-> ""]
        /\ impChk = [p \in Procs |-> {}]
        /\ impAfter = [p \in Procs |-> {}]
        /\ fullDone = {}
        /\ badStep = FALSE
        /\ badStepFail = FALSE
        /\ badNested = FALSE
        /\ revAll = FALSE
        /\ inflight = {}
        /\ lost = {}
        /\ cache = [r \in Reps |-> {}]
        /\ up = [r \in Reps |-> TRUE]
        /\ reading = [r \in Reps |-> FALSE]
        /\ readSet = [r \in Reps |-> {}]
        /\ sinceRead = [r \in Reps |-> {}]
        /\ seen = [r \in Reps |-> {}]
        /\ nLoss = 0
        /\ nRestart = 0
        (* Process req *)
        /\ ten = [self \in Procs |-> ""]
        /\ phase = [self \in Procs |-> ""]
        /\ attempts = [self \in Procs |-> 0]
        /\ retries = [self \in Procs |-> 0]
        /\ sAtt = [self \in Procs |-> 0]
        /\ snap = [self \in Procs |-> {}]
        /\ cur = [self \in Procs |-> ""]
        /\ tgts = [self \in Procs |-> {}]
        /\ curT = [self \in Procs |-> ""]
        /\ uq = [self \in Procs |-> <<>>]
        /\ pc = [self \in ProcSet |-> CASE self = 100 -> "Det"
                                        [] self \in (IF Reps = {} THEN {} ELSE {101}) -> "Bus"
                                        [] self \in Procs -> "Start"]

Det == /\ pc[100] = "Det"
       /\ \/ /\ CycleMembers /= {}
             /\ \E t \in CycleMembers:
                  /\ aborted' = [aborted EXCEPT ![t] = "deadlock"]
                  /\ want' = [want EXCEPT ![t] = NoWant]
                  /\ sh' = [r \in Res |-> sh[r] \ {t}]
                  /\ ex' = [r \in Res |-> IF ex[r] = t THEN NoTx ELSE ex[r]]
                  /\ open' = [open EXCEPT ![t] = FALSE]
                  /\ wtok' = [wtok EXCEPT ![t] = EmptyW]
                  /\ waud' = [waud EXCEPT ![t] = {}]
                  /\ wst' = [wst EXCEPT ![t] = {}]
                  /\ winv' = [winv EXCEPT ![t] = {}]
             /\ pc' = [pc EXCEPT ![100] = "Det"]
             /\ UNCHANGED nAborts
          \/ /\ nAborts < MaxAborts
             /\ \E t \in {u \in {<<p, lvl[p]>> : p \in Procs} : open[u]}:
                  /\ aborted' = [aborted EXCEPT ![t] = "fail"]
                  /\ want' = [want EXCEPT ![t] = NoWant]
                  /\ nAborts' = nAborts + 1
                  /\ sh' = [r \in Res |-> sh[r] \ {t}]
                  /\ ex' = [r \in Res |-> IF ex[r] = t THEN NoTx ELSE ex[r]]
                  /\ open' = [open EXCEPT ![t] = FALSE]
                  /\ wtok' = [wtok EXCEPT ![t] = EmptyW]
                  /\ waud' = [waud EXCEPT ![t] = {}]
                  /\ wst' = [wst EXCEPT ![t] = {}]
                  /\ winv' = [winv EXCEPT ![t] = {}]
             /\ pc' = [pc EXCEPT ![100] = "Det"]
          \/ /\ nAborts < MaxAborts /\ \E u \in TxIds : open[u]
             /\ aborted' = [u \in TxIds |-> IF open[u] THEN "fail" ELSE aborted[u]]
             /\ want' = [u \in TxIds |-> NoWant]
             /\ nAborts' = nAborts + 1
             /\ sh' = [r \in Res |-> {}]
             /\ ex' = [r \in Res |-> NoTx]
             /\ open' = [u \in TxIds |-> FALSE]
             /\ wtok' = [u \in TxIds |-> EmptyW]
             /\ waud' = [u \in TxIds |-> {}]
             /\ wst' = [u \in TxIds |-> {}]
             /\ winv' = [u \in TxIds |-> {}]
             /\ pc' = [pc EXCEPT ![100] = "Det"]
          \/ /\ AllReqDone
             /\ pc' = [pc EXCEPT ![100] = "Done"]
             /\ UNCHANGED <<open, aborted, wtok, waud, wst, winv, sh, ex, want, nAborts>>
       /\ UNCHANGED << op, tok, aud, starts, inval, lvl, issued, finished, res, 
                       callerOf, rootOf, tenOf, exchJ, rotated, srcRevAtRead, 
                       stepTree, stepRev, stepRead, cascCommitted, cascDone, 
                       callerAtAccept, impChk, impAfter, fullDone, badStep, 
                       badStepFail, badNested, revAll, inflight, lost, cache, 
                       up, reading, readSet, sinceRead, seen, nLoss, nRestart, 
                       ten, phase, attempts, retries, sAtt, snap, cur, tgts, 
                       curT, uq >>

det == Det

Bus(self) == /\ pc[self] = "Bus"
             /\ \/ /\ \E e \in inflight:
                        /\ cache' = [cache EXCEPT ![e[1]] = cache[e[1]] \cup {e[2]}]
                        /\ sinceRead' = [sinceRead EXCEPT ![e[1]] = IF reading[e[1]] THEN sinceRead[e[1]] \cup {e[2]} ELSE sinceRead[e[1]]]
                        /\ inflight' = inflight \ {e}
                   /\ pc' = [pc EXCEPT ![self] = "Bus"]
                   /\ UNCHANGED <<lost, up, reading, readSet, seen, nLoss, nRestart>>
                \/ /\ nLoss < MaxLoss
                   /\ \E e \in inflight:
                        /\ lost' = (lost \cup {e})
                        /\ inflight' = inflight \ {e}
                        /\ nLoss' = nLoss + 1
                   /\ pc' = [pc EXCEPT ![self] = "Bus"]
                   /\ UNCHANGED <<cache, up, reading, readSet, sinceRead, seen, nRestart>>
                \/ /\ nRestart < MaxRestarts
                   /\ \E r \in Reps:
                        /\ cache' = [cache EXCEPT ![r] = {}]
                        /\ up' = [up EXCEPT ![r] = FALSE]
                        /\ reading' = [reading EXCEPT ![r] = FALSE]
                        /\ seen' = [seen EXCEPT ![r] = {}]
                        /\ nRestart' = nRestart + 1
                   /\ pc' = [pc EXCEPT ![self] = "Bus"]
                   /\ UNCHANGED <<inflight, lost, readSet, sinceRead, nLoss>>
                \/ /\ \E r \in {q \in Reps : ~reading[q] /\ (~up[q] \/ ~(CommittedRevoked \subseteq cache[q]))}:
                        /\ reading' = [reading EXCEPT ![r] = TRUE]
                        /\ sinceRead' = [sinceRead EXCEPT ![r] = {}]
                        /\ readSet' = [readSet EXCEPT ![r] = CommittedRevoked]
                   /\ pc' = [pc EXCEPT ![self] = "Bus"]
                   /\ UNCHANGED <<inflight, lost, cache, up, seen, nLoss, nRestart>>
                \/ /\ \E r \in {q \in Reps : reading[q]}:
                        /\ cache' = [cache EXCEPT ![r] = readSet[r] \cup sinceRead[r]]
                        /\ seen' = [seen EXCEPT ![r] = readSet[r]]
                        /\ reading' = [reading EXCEPT ![r] = FALSE]
                        /\ up' = [up EXCEPT ![r] = TRUE]
                   /\ pc' = [pc EXCEPT ![self] = "Bus"]
                   /\ UNCHANGED <<inflight, lost, readSet, sinceRead, nLoss, nRestart>>
                \/ /\ AllReqDone /\ inflight = {}
                      /\ \A r \in Reps : ~reading[r] /\ up[r] /\ cache[r] = CommittedRevoked
                   /\ pc' = [pc EXCEPT ![self] = "Done"]
                   /\ UNCHANGED <<inflight, lost, cache, up, reading, readSet, sinceRead, seen, nLoss, nRestart>>
             /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, wtok, 
                             waud, wst, winv, lvl, sh, ex, want, nAborts, 
                             issued, finished, res, callerOf, rootOf, tenOf, 
                             exchJ, rotated, srcRevAtRead, stepTree, stepRev, 
                             stepRead, cascCommitted, cascDone, callerAtAccept, 
                             impChk, impAfter, fullDone, badStep, badStepFail, 
                             badNested, revAll, ten, phase, attempts, retries, 
                             sAtt, snap, cur, tgts, curT, uq >>

bus(self) == Bus(self)

Start(self) == /\ pc[self] = "Start"
               /\ (\A j \in {op[self].caller, op[self].subj, op[self].actor, op[self].root, op[self].cb} : Avail(j))
                  \/ (\E j \in {op[self].caller, op[self].subj, op[self].actor, op[self].root, op[self].cb} : NeverAvail(j))
               /\ IF \E j \in {op[self].caller, op[self].subj, op[self].actor, op[self].root, op[self].cb} : NeverAvail(j)
                     THEN /\ res' = [res EXCEPT ![self] = "no_token"]
                          /\ finished' = [finished EXCEPT ![self] = TRUE]
                          /\ pc' = [pc EXCEPT ![self] = "Done"]
                          /\ UNCHANGED << callerOf, rootOf, tenOf, ten >>
                     ELSE /\ IF Cardinality({tok[j].ten : j \in {op[self].caller, op[self].subj, op[self].actor, op[self].root} \ {""}}) > 1
                                THEN /\ res' = [res EXCEPT ![self] = "refused_tenant"]
                                     /\ finished' = [finished EXCEPT ![self] = TRUE]
                                     /\ pc' = [pc EXCEPT ![self] = "Done"]
                                     /\ UNCHANGED << callerOf, rootOf, tenOf, 
                                                     ten >>
                                ELSE /\ IF op[self].kind \in {"exch", "pg"}
                                           THEN /\ ten' = [ten EXCEPT ![self] = tok[op[self].subj].ten]
                                                /\ pc' = [pc EXCEPT ![self] = "XBegin"]
                                                /\ UNCHANGED << callerOf, 
                                                                rootOf, tenOf >>
                                           ELSE /\ IF op[self].kind = "rev"
                                                      THEN /\ ten' = [ten EXCEPT ![self] = tok[op[self].root].ten]
                                                           /\ callerOf' = [callerOf EXCEPT ![self] = op[self].caller]
                                                           /\ rootOf' = [rootOf EXCEPT ![self] = op[self].root]
                                                           /\ tenOf' = [tenOf EXCEPT ![self] = tok[op[self].root].ten]
                                                           /\ IF IsTF
                                                                 THEN /\ pc' = [pc EXCEPT ![self] = "TOuter"]
                                                                 ELSE /\ pc' = [pc EXCEPT ![self] = "MSub"]
                                                      ELSE /\ IF op[self].kind = "imp"
                                                                 THEN /\ pc' = [pc EXCEPT ![self] = "IChk"]
                                                                      /\ ten' = ten
                                                                 ELSE /\ IF op[self].kind = "full"
                                                                            THEN /\ ten' = [ten EXCEPT ![self] = UTen[op[self].user]]
                                                                                 /\ pc' = [pc EXCEPT ![self] = "FLevel"]
                                                                            ELSE /\ pc' = [pc EXCEPT ![self] = "EBegin"]
                                                                                 /\ ten' = ten
                                                           /\ UNCHANGED << callerOf, 
                                                                           rootOf, 
                                                                           tenOf >>
                                     /\ UNCHANGED << finished, res >>
               /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                               wtok, waud, wst, winv, lvl, sh, ex, want, 
                               nAborts, issued, exchJ, rotated, srcRevAtRead, 
                               stepTree, stepRev, stepRead, cascCommitted, 
                               cascDone, callerAtAccept, impChk, impAfter, 
                               fullDone, badStep, badStepFail, badNested, 
                               revAll, inflight, lost, cache, up, reading, 
                               readSet, sinceRead, seen, nLoss, nRestart, 
                               phase, attempts, retries, sAtt, snap, cur, tgts, 
                               curT, uq >>

XBegin(self) == /\ pc[self] = "XBegin"
                /\ lvl' = [lvl EXCEPT ![self] = 1]
                /\ open' = [open EXCEPT ![<<self, 1>>] = TRUE]
                /\ aborted' = [aborted EXCEPT ![<<self, 1>>] = ""]
                /\ wtok' = [wtok EXCEPT ![<<self, 1>>] = EmptyW]
                /\ waud' = [waud EXCEPT ![<<self, 1>>] = {}]
                /\ wst' = [wst EXCEPT ![<<self, 1>>] = {}]
                /\ winv' = [winv EXCEPT ![<<self, 1>>] = {}]
                /\ want' = [want EXCEPT ![<<self, 1>>] = <<ten[self], "x">>]
                /\ IF op[self].kind = "exch"
                      THEN /\ exchJ' = (exchJ \cup {NewJ(self)})
                      ELSE /\ TRUE
                           /\ exchJ' = exchJ
                /\ pc' = [pc EXCEPT ![self] = "XAdv"]
                /\ UNCHANGED << op, tok, aud, starts, inval, sh, ex, nAborts, 
                                issued, finished, res, callerOf, rootOf, tenOf, 
                                rotated, srcRevAtRead, stepTree, stepRev, 
                                stepRead, cascCommitted, cascDone, 
                                callerAtAccept, impChk, impAfter, fullDone, 
                                badStep, badStepFail, badNested, revAll, 
                                inflight, lost, cache, up, reading, readSet, 
                                sinceRead, seen, nLoss, nRestart, ten, phase, 
                                attempts, retries, sAtt, snap, cur, tgts, curT, 
                                uq >>

XAdv(self) == /\ pc[self] = "XAdv"
              /\ CanLock(T(self), ten[self], "x") \/ Ab(self)
              /\ IF Ab(self)
                    THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                         /\ UNCHANGED << sh, ex, want >>
                    ELSE /\ IF "x" = "s"
                               THEN /\ sh' = [sh EXCEPT ![ten[self]] = sh[ten[self]] \cup {(T(self))}]
                                    /\ ex' = ex
                               ELSE /\ ex' = [ex EXCEPT ![ten[self]] = T(self)]
                                    /\ sh' = sh
                         /\ want' = [want EXCEPT ![T(self)] = NoWant]
                         /\ pc' = [pc EXCEPT ![self] = "X1a"]
              /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, wtok, 
                              waud, wst, winv, lvl, nAborts, issued, finished, 
                              res, callerOf, rootOf, tenOf, exchJ, rotated, 
                              srcRevAtRead, stepTree, stepRev, stepRead, 
                              cascCommitted, cascDone, callerAtAccept, impChk, 
                              impAfter, fullDone, badStep, badStepFail, 
                              badNested, revAll, inflight, lost, cache, up, 
                              reading, readSet, sinceRead, seen, nLoss, 
                              nRestart, ten, phase, attempts, retries, sAtt, 
                              snap, cur, tgts, curT, uq >>

X1a(self) == /\ pc[self] = "X1a"
             /\ IF Ab(self)
                   THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                        /\ UNCHANGED << open, wtok, waud, wst, winv, sh, ex, 
                                        want, finished, res, srcRevAtRead, uq >>
                   ELSE /\ IF \E s \in {op[self].subj, op[self].actor, op[self].caller} \ {""} : V(T(self), s).ex /\ V(T(self), s).rev
                              THEN /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                                   /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                                   /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                                   /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                                   /\ waud' = [waud EXCEPT ![(T(self))] = {}]
                                   /\ wst' = [wst EXCEPT ![(T(self))] = {}]
                                   /\ winv' = [winv EXCEPT ![(T(self))] = {}]
                                   /\ res' = [res EXCEPT ![self] = "refused_revoked"]
                                   /\ finished' = [finished EXCEPT ![self] = TRUE]
                                   /\ pc' = [pc EXCEPT ![self] = "Done"]
                                   /\ UNCHANGED << want, srcRevAtRead, uq >>
                              ELSE /\ IF \E s \in {op[self].subj, op[self].actor} \ {""} : IsImp(V(T(self), s))
                                         THEN /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                                              /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                                              /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                                              /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                                              /\ waud' = [waud EXCEPT ![(T(self))] = {}]
                                              /\ wst' = [wst EXCEPT ![(T(self))] = {}]
                                              /\ winv' = [winv EXCEPT ![(T(self))] = {}]
                                              /\ res' = [res EXCEPT ![self] = "refused_impersonation"]
                                              /\ finished' = [finished EXCEPT ![self] = TRUE]
                                              /\ pc' = [pc EXCEPT ![self] = "Done"]
                                              /\ UNCHANGED << want, 
                                                              srcRevAtRead, uq >>
                                         ELSE /\ srcRevAtRead' = [srcRevAtRead EXCEPT ![self] = {s \in {op[self].subj, op[self].actor, op[self].caller} \ {""} : tok[s].ex /\ tok[s].rev}]
                                              /\ IF IsTF /\ op[self].kind = "exch"
                                                    THEN /\ uq' = [uq EXCEPT ![self] = LET s1 == <<tok[op[self].subj].sub>>
                                                                                           s2 == IF op[self].actor /= "" /\ tok[op[self].actor].sub \notin {tok[op[self].subj].sub}
                                                                                                 THEN s1 \o <<tok[op[self].actor].sub>> ELSE s1
                                                                                           cs == tok[op[self].caller].sub
                                                                                       IN  IF \E i \in 1..Len(s2) : s2[i] = cs THEN s2 ELSE s2 \o <<cs>>]
                                                         /\ want' = [want EXCEPT ![T(self)] = <<Head(uq'[self]), "s">>]
                                                         /\ pc' = [pc EXCEPT ![self] = "XUserLock"]
                                                    ELSE /\ pc' = [pc EXCEPT ![self] = "XInsert"]
                                                         /\ UNCHANGED << want, 
                                                                         uq >>
                                              /\ UNCHANGED << open, wtok, waud, 
                                                              wst, winv, sh, 
                                                              ex, finished, 
                                                              res >>
             /\ UNCHANGED << op, tok, aud, starts, inval, aborted, lvl, 
                             nAborts, issued, callerOf, rootOf, tenOf, exchJ, 
                             rotated, stepTree, stepRev, stepRead, 
                             cascCommitted, cascDone, callerAtAccept, impChk, 
                             impAfter, fullDone, badStep, badStepFail, 
                             badNested, revAll, inflight, lost, cache, up, 
                             reading, readSet, sinceRead, seen, nLoss, 
                             nRestart, ten, phase, attempts, retries, sAtt, 
                             snap, cur, tgts, curT >>

XUserLock(self) == /\ pc[self] = "XUserLock"
                   /\ CanLock(T(self), Head(uq[self]), "s") \/ Ab(self)
                   /\ IF Ab(self)
                         THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                              /\ UNCHANGED << open, wtok, waud, wst, winv, sh, 
                                              ex, want, finished, res, uq >>
                         ELSE /\ IF InvalIn(T(self), Head(uq[self]))
                                    THEN /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                         /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                                         /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                                         /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                                         /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                                         /\ waud' = [waud EXCEPT ![(T(self))] = {}]
                                         /\ wst' = [wst EXCEPT ![(T(self))] = {}]
                                         /\ winv' = [winv EXCEPT ![(T(self))] = {}]
                                         /\ res' = [res EXCEPT ![self] = "refused_user"]
                                         /\ finished' = [finished EXCEPT ![self] = TRUE]
                                         /\ pc' = [pc EXCEPT ![self] = "Done"]
                                         /\ uq' = uq
                                    ELSE /\ IF "s" = "s"
                                               THEN /\ sh' = [sh EXCEPT ![(Head(uq[self]))] = sh[(Head(uq[self]))] \cup {(T(self))}]
                                                    /\ ex' = ex
                                               ELSE /\ ex' = [ex EXCEPT ![(Head(uq[self]))] = T(self)]
                                                    /\ sh' = sh
                                         /\ IF Len(uq[self]) > 1
                                               THEN /\ want' = [want EXCEPT ![T(self)] = <<uq[self][2], "s">>]
                                                    /\ uq' = [uq EXCEPT ![self] = Tail(uq[self])]
                                                    /\ pc' = [pc EXCEPT ![self] = "XUserLock"]
                                               ELSE /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                                    /\ uq' = [uq EXCEPT ![self] = <<>>]
                                                    /\ pc' = [pc EXCEPT ![self] = "XInsert"]
                                         /\ UNCHANGED << open, wtok, waud, wst, 
                                                         winv, finished, res >>
                   /\ UNCHANGED << op, tok, aud, starts, inval, aborted, lvl, 
                                   nAborts, issued, callerOf, rootOf, tenOf, 
                                   exchJ, rotated, srcRevAtRead, stepTree, 
                                   stepRev, stepRead, cascCommitted, cascDone, 
                                   callerAtAccept, impChk, impAfter, fullDone, 
                                   badStep, badStepFail, badNested, revAll, 
                                   inflight, lost, cache, up, reading, readSet, 
                                   sinceRead, seen, nLoss, nRestart, ten, 
                                   phase, attempts, retries, sAtt, snap, cur, 
                                   tgts, curT >>

XInsert(self) == /\ pc[self] = "XInsert"
                 /\ IF Ab(self)
                       THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                            /\ UNCHANGED << wtok, waud, want >>
                       ELSE /\ IF op[self].kind = "exch"
                                  THEN /\ waud' = [waud EXCEPT ![T(self)] = {NewJ(self)}]
                                  ELSE /\ TRUE
                                       /\ waud' = waud
                            /\ wtok' = [wtok EXCEPT ![T(self)] = (NewJ(self) :> Row(ten[self], tok[op[self].subj].sub,
                                                                     IF op[self].actor /= "" THEN op[self].actor ELSE op[self].subj,
                                                                     IF op[self].actor /= "" THEN tok[op[self].actor].sub ELSE "", "", ""))
                                                                 @@ wtok[T(self)]]
                            /\ IF op[self].rot
                                  THEN /\ want' = [want EXCEPT ![T(self)] = <<op[self].subj, "x">>]
                                       /\ pc' = [pc EXCEPT ![self] = "XRotLock"]
                                  ELSE /\ pc' = [pc EXCEPT ![self] = "XCommit"]
                                       /\ want' = want
                 /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                 wst, winv, lvl, sh, ex, nAborts, issued, 
                                 finished, res, callerOf, rootOf, tenOf, exchJ, 
                                 rotated, srcRevAtRead, stepTree, stepRev, 
                                 stepRead, cascCommitted, cascDone, 
                                 callerAtAccept, impChk, impAfter, fullDone, 
                                 badStep, badStepFail, badNested, revAll, 
                                 inflight, lost, cache, up, reading, readSet, 
                                 sinceRead, seen, nLoss, nRestart, ten, phase, 
                                 attempts, retries, sAtt, snap, cur, tgts, 
                                 curT, uq >>

XRotLock(self) == /\ pc[self] = "XRotLock"
                  /\ CanLock(T(self), op[self].subj, "x") \/ Ab(self)
                  /\ IF Ab(self)
                        THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                             /\ UNCHANGED << open, wtok, waud, wst, winv, sh, 
                                             ex, want, finished, res >>
                        ELSE /\ IF V(T(self), op[self].subj).rev
                                   THEN /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                        /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                                        /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                                        /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                                        /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                                        /\ waud' = [waud EXCEPT ![(T(self))] = {}]
                                        /\ wst' = [wst EXCEPT ![(T(self))] = {}]
                                        /\ winv' = [winv EXCEPT ![(T(self))] = {}]
                                        /\ res' = [res EXCEPT ![self] = "refused_revoked"]
                                        /\ finished' = [finished EXCEPT ![self] = TRUE]
                                        /\ pc' = [pc EXCEPT ![self] = "Done"]
                                   ELSE /\ IF "x" = "s"
                                              THEN /\ sh' = [sh EXCEPT ![(op[self].subj)] = sh[(op[self].subj)] \cup {(T(self))}]
                                                   /\ ex' = ex
                                              ELSE /\ ex' = [ex EXCEPT ![(op[self].subj)] = T(self)]
                                                   /\ sh' = sh
                                        /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                        /\ wtok' = [wtok EXCEPT ![T(self)] = (op[self].subj :> RevRow(V(T(self), op[self].subj), self)) @@ wtok[T(self)]]
                                        /\ pc' = [pc EXCEPT ![self] = "XCommit"]
                                        /\ UNCHANGED << open, waud, wst, winv, 
                                                        finished, res >>
                  /\ UNCHANGED << op, tok, aud, starts, inval, aborted, lvl, 
                                  nAborts, issued, callerOf, rootOf, tenOf, 
                                  exchJ, rotated, srcRevAtRead, stepTree, 
                                  stepRev, stepRead, cascCommitted, cascDone, 
                                  callerAtAccept, impChk, impAfter, fullDone, 
                                  badStep, badStepFail, badNested, revAll, 
                                  inflight, lost, cache, up, reading, readSet, 
                                  sinceRead, seen, nLoss, nRestart, ten, phase, 
                                  attempts, retries, sAtt, snap, cur, tgts, 
                                  curT, uq >>

XCommit(self) == /\ pc[self] = "XCommit"
                 /\ IF Ab(self)
                       THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                            /\ UNCHANGED << tok, aud, starts, inval, open, 
                                            wtok, sh, ex, issued, finished, 
                                            res, rotated, inflight >>
                       ELSE /\ IF op[self].rot
                                  THEN /\ rotated' = (rotated \cup {<<self, op[self].subj>>})
                                  ELSE /\ TRUE
                                       /\ UNCHANGED rotated
                            /\ inflight' = (inflight \cup (Reps \X NewRevs((T(self)))))
                            /\ tok' = Apply((T(self)))
                            /\ aud' = (aud \cup waud[(T(self))])
                            /\ starts' = (starts \cup wst[(T(self))])
                            /\ inval' = (inval \cup winv[(T(self))])
                            /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                            /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                            /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                            /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                            /\ issued' = (issued \cup {NewJ(self)})
                            /\ res' = [res EXCEPT ![self] = "issued"]
                            /\ finished' = [finished EXCEPT ![self] = TRUE]
                            /\ pc' = [pc EXCEPT ![self] = "Done"]
                 /\ UNCHANGED << op, aborted, waud, wst, winv, lvl, want, 
                                 nAborts, callerOf, rootOf, tenOf, exchJ, 
                                 srcRevAtRead, stepTree, stepRev, stepRead, 
                                 cascCommitted, cascDone, callerAtAccept, 
                                 impChk, impAfter, fullDone, badStep, 
                                 badStepFail, badNested, revAll, lost, cache, 
                                 up, reading, readSet, sinceRead, seen, nLoss, 
                                 nRestart, ten, phase, attempts, retries, sAtt, 
                                 snap, cur, tgts, curT, uq >>

MSub(self) == /\ pc[self] = "MSub"
              /\ phase' = [phase EXCEPT ![self] = "step"]
              /\ stepRev' = [stepRev EXCEPT ![self] = {}]
              /\ stepRead' = [stepRead EXCEPT ![self] = FALSE]
              /\ lvl' = [lvl EXCEPT ![self] = 1]
              /\ open' = [open EXCEPT ![<<self, 1>>] = TRUE]
              /\ aborted' = [aborted EXCEPT ![<<self, 1>>] = ""]
              /\ wtok' = [wtok EXCEPT ![<<self, 1>>] = EmptyW]
              /\ waud' = [waud EXCEPT ![<<self, 1>>] = {}]
              /\ wst' = [wst EXCEPT ![<<self, 1>>] = {}]
              /\ winv' = [winv EXCEPT ![<<self, 1>>] = {}]
              /\ want' = [want EXCEPT ![<<self, 1>>] = <<op[self].caller, "s">>]
              /\ pc' = [pc EXCEPT ![self] = "MSubCaller"]
              /\ UNCHANGED << op, tok, aud, starts, inval, sh, ex, nAborts, 
                              issued, finished, res, callerOf, rootOf, tenOf, 
                              exchJ, rotated, srcRevAtRead, stepTree, 
                              cascCommitted, cascDone, callerAtAccept, impChk, 
                              impAfter, fullDone, badStep, badStepFail, 
                              badNested, revAll, inflight, lost, cache, up, 
                              reading, readSet, sinceRead, seen, nLoss, 
                              nRestart, ten, attempts, retries, sAtt, snap, 
                              cur, tgts, curT, uq >>

MSubCaller(self) == /\ pc[self] = "MSubCaller"
                    /\ CanLock(T(self), op[self].caller, "s") \/ Ab(self)
                    /\ IF Ab(self)
                          THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                               /\ UNCHANGED << open, wtok, waud, wst, winv, sh, 
                                               ex, want, finished, res >>
                          ELSE /\ IF V(T(self), op[self].caller).ex /\ V(T(self), op[self].caller).rev
                                     THEN /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                          /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                                          /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                                          /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                                          /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                                          /\ waud' = [waud EXCEPT ![(T(self))] = {}]
                                          /\ wst' = [wst EXCEPT ![(T(self))] = {}]
                                          /\ winv' = [winv EXCEPT ![(T(self))] = {}]
                                          /\ res' = [res EXCEPT ![self] = "refused_caller"]
                                          /\ finished' = [finished EXCEPT ![self] = TRUE]
                                          /\ pc' = [pc EXCEPT ![self] = "Done"]
                                     ELSE /\ IF "s" = "s"
                                                THEN /\ sh' = [sh EXCEPT ![(op[self].caller)] = sh[(op[self].caller)] \cup {(T(self))}]
                                                     /\ ex' = ex
                                                ELSE /\ ex' = [ex EXCEPT ![(op[self].caller)] = T(self)]
                                                     /\ sh' = sh
                                          /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                          /\ pc' = [pc EXCEPT ![self] = "MSubRead"]
                                          /\ UNCHANGED << open, wtok, waud, 
                                                          wst, winv, finished, 
                                                          res >>
                    /\ UNCHANGED << op, tok, aud, starts, inval, aborted, lvl, 
                                    nAborts, issued, callerOf, rootOf, tenOf, 
                                    exchJ, rotated, srcRevAtRead, stepTree, 
                                    stepRev, stepRead, cascCommitted, cascDone, 
                                    callerAtAccept, impChk, impAfter, fullDone, 
                                    badStep, badStepFail, badNested, revAll, 
                                    inflight, lost, cache, up, reading, 
                                    readSet, sinceRead, seen, nLoss, nRestart, 
                                    ten, phase, attempts, retries, sAtt, snap, 
                                    cur, tgts, curT, uq >>

MSubRead(self) == /\ pc[self] = "MSubRead"
                  /\ IF Ab(self)
                        THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                             /\ UNCHANGED << tok, aud, starts, inval, open, 
                                             wtok, sh, ex, stepTree, inflight >>
                        ELSE /\ stepTree' = [stepTree EXCEPT ![self] = TreeOf(View(T(self)), {op[self].root}, ten[self])]
                             /\ inflight' = (inflight \cup (Reps \X NewRevs((T(self)))))
                             /\ tok' = Apply((T(self)))
                             /\ aud' = (aud \cup waud[(T(self))])
                             /\ starts' = (starts \cup wst[(T(self))])
                             /\ inval' = (inval \cup winv[(T(self))])
                             /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                             /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                             /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                             /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                             /\ pc' = [pc EXCEPT ![self] = "MImp"]
                  /\ UNCHANGED << op, aborted, waud, wst, winv, lvl, want, 
                                  nAborts, issued, finished, res, callerOf, 
                                  rootOf, tenOf, exchJ, rotated, srcRevAtRead, 
                                  stepRev, stepRead, cascCommitted, cascDone, 
                                  callerAtAccept, impChk, impAfter, fullDone, 
                                  badStep, badStepFail, badNested, revAll, 
                                  lost, cache, up, reading, readSet, sinceRead, 
                                  seen, nLoss, nRestart, ten, phase, attempts, 
                                  retries, sAtt, snap, cur, tgts, curT, uq >>

MImp(self) == /\ pc[self] = "MImp"
              /\ lvl' = [lvl EXCEPT ![self] = 1]
              /\ open' = [open EXCEPT ![<<self, 1>>] = TRUE]
              /\ aborted' = [aborted EXCEPT ![<<self, 1>>] = ""]
              /\ wtok' = [wtok EXCEPT ![<<self, 1>>] = EmptyW]
              /\ waud' = [waud EXCEPT ![<<self, 1>>] = {}]
              /\ wst' = [wst EXCEPT ![<<self, 1>>] = {}]
              /\ winv' = [winv EXCEPT ![<<self, 1>>] = {}]
              /\ revAll' = TRUE
              /\ stepRead' = [stepRead EXCEPT ![self] = TRUE]
              /\ snap' = [snap EXCEPT ![self] = {j \in Jtis : IsImp(tok[j]) /\ ~tok[j].rev /\ tok[j].iten = ten[self] /\ tok[j].ijti \in stepTree[self]}]
              /\ pc' = [pc EXCEPT ![self] = "MImpRow"]
              /\ UNCHANGED << op, tok, aud, starts, inval, sh, ex, want, 
                              nAborts, issued, finished, res, callerOf, rootOf, 
                              tenOf, exchJ, rotated, srcRevAtRead, stepTree, 
                              stepRev, cascCommitted, cascDone, callerAtAccept, 
                              impChk, impAfter, fullDone, badStep, badStepFail, 
                              badNested, inflight, lost, cache, up, reading, 
                              readSet, sinceRead, seen, nLoss, nRestart, ten, 
                              phase, attempts, retries, sAtt, cur, tgts, curT, 
                              uq >>

MImpRow(self) == /\ pc[self] = "MImpRow"
                 /\ IF Ab(self)
                       THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                            /\ UNCHANGED << want, cur >>
                       ELSE /\ IF snap[self] = {}
                                  THEN /\ pc' = [pc EXCEPT ![self] = "MImpCommit"]
                                       /\ UNCHANGED << want, cur >>
                                  ELSE /\ \E j \in snap[self]:
                                            /\ cur' = [cur EXCEPT ![self] = j]
                                            /\ want' = [want EXCEPT ![T(self)] = <<j, "x">>]
                                       /\ pc' = [pc EXCEPT ![self] = "MImpLock"]
                 /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                 wtok, waud, wst, winv, lvl, sh, ex, nAborts, 
                                 issued, finished, res, callerOf, rootOf, 
                                 tenOf, exchJ, rotated, srcRevAtRead, stepTree, 
                                 stepRev, stepRead, cascCommitted, cascDone, 
                                 callerAtAccept, impChk, impAfter, fullDone, 
                                 badStep, badStepFail, badNested, revAll, 
                                 inflight, lost, cache, up, reading, readSet, 
                                 sinceRead, seen, nLoss, nRestart, ten, phase, 
                                 attempts, retries, sAtt, snap, tgts, curT, uq >>

MImpLock(self) == /\ pc[self] = "MImpLock"
                  /\ CanLock(T(self), cur[self], "x") \/ Ab(self)
                  /\ IF Ab(self)
                        THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                             /\ UNCHANGED << wtok, sh, ex, want, snap >>
                        ELSE /\ IF "x" = "s"
                                   THEN /\ sh' = [sh EXCEPT ![cur[self]] = sh[cur[self]] \cup {(T(self))}]
                                        /\ ex' = ex
                                   ELSE /\ ex' = [ex EXCEPT ![cur[self]] = T(self)]
                                        /\ sh' = sh
                             /\ want' = [want EXCEPT ![T(self)] = NoWant]
                             /\ IF ~V(T(self), cur[self]).rev
                                   THEN /\ wtok' = [wtok EXCEPT ![T(self)] = (cur[self] :> RevRow(V(T(self), cur[self]), self)) @@ wtok[T(self)]]
                                   ELSE /\ TRUE
                                        /\ wtok' = wtok
                             /\ snap' = [snap EXCEPT ![self] = snap[self] \ {cur[self]}]
                             /\ pc' = [pc EXCEPT ![self] = "MImpRow"]
                  /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                  waud, wst, winv, lvl, nAborts, issued, 
                                  finished, res, callerOf, rootOf, tenOf, 
                                  exchJ, rotated, srcRevAtRead, stepTree, 
                                  stepRev, stepRead, cascCommitted, cascDone, 
                                  callerAtAccept, impChk, impAfter, fullDone, 
                                  badStep, badStepFail, badNested, revAll, 
                                  inflight, lost, cache, up, reading, readSet, 
                                  sinceRead, seen, nLoss, nRestart, ten, phase, 
                                  attempts, retries, sAtt, cur, tgts, curT, uq >>

MImpCommit(self) == /\ pc[self] = "MImpCommit"
                    /\ IF Ab(self)
                          THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                               /\ UNCHANGED << tok, aud, starts, inval, open, 
                                               wtok, sh, ex, stepRev, badStep, 
                                               inflight >>
                          ELSE /\ stepRev' = [stepRev EXCEPT ![self] = {j \in DOMAIN wtok[T(self)] : tok[j].ten = ten[self]}]
                               /\ IF DOMAIN wtok[T(self)] /= {} /\ tok[op[self].caller].ex /\ tok[op[self].caller].rev /\ tok[op[self].caller].by /= self
                                     THEN /\ badStep' = TRUE
                                     ELSE /\ TRUE
                                          /\ UNCHANGED badStep
                               /\ inflight' = (inflight \cup (Reps \X NewRevs((T(self)))))
                               /\ tok' = Apply((T(self)))
                               /\ aud' = (aud \cup waud[(T(self))])
                               /\ starts' = (starts \cup wst[(T(self))])
                               /\ inval' = (inval \cup winv[(T(self))])
                               /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                               /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                               /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                               /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                               /\ pc' = [pc EXCEPT ![self] = "Casc"]
                    /\ UNCHANGED << op, aborted, waud, wst, winv, lvl, want, 
                                    nAborts, issued, finished, res, callerOf, 
                                    rootOf, tenOf, exchJ, rotated, 
                                    srcRevAtRead, stepTree, stepRead, 
                                    cascCommitted, cascDone, callerAtAccept, 
                                    impChk, impAfter, fullDone, badStepFail, 
                                    badNested, revAll, lost, cache, up, 
                                    reading, readSet, sinceRead, seen, nLoss, 
                                    nRestart, ten, phase, attempts, retries, 
                                    sAtt, snap, cur, tgts, curT, uq >>

TOuter(self) == /\ pc[self] = "TOuter"
                /\ phase' = [phase EXCEPT ![self] = "step"]
                /\ stepRev' = [stepRev EXCEPT ![self] = {}]
                /\ stepRead' = [stepRead EXCEPT ![self] = FALSE]
                /\ sAtt' = [sAtt EXCEPT ![self] = sAtt[self] + 1]
                /\ lvl' = [lvl EXCEPT ![self] = 1]
                /\ open' = [open EXCEPT ![<<self, 1>>] = TRUE]
                /\ aborted' = [aborted EXCEPT ![<<self, 1>>] = ""]
                /\ wtok' = [wtok EXCEPT ![<<self, 1>>] = EmptyW]
                /\ waud' = [waud EXCEPT ![<<self, 1>>] = {}]
                /\ wst' = [wst EXCEPT ![<<self, 1>>] = {}]
                /\ winv' = [winv EXCEPT ![<<self, 1>>] = {}]
                /\ want' = [want EXCEPT ![<<self, 1>>] = <<op[self].caller, "s">>]
                /\ pc' = [pc EXCEPT ![self] = "TOuterCaller"]
                /\ UNCHANGED << op, tok, aud, starts, inval, sh, ex, nAborts, 
                                issued, finished, res, callerOf, rootOf, tenOf, 
                                exchJ, rotated, srcRevAtRead, stepTree, 
                                cascCommitted, cascDone, callerAtAccept, 
                                impChk, impAfter, fullDone, badStep, 
                                badStepFail, badNested, revAll, inflight, lost, 
                                cache, up, reading, readSet, sinceRead, seen, 
                                nLoss, nRestart, ten, attempts, retries, snap, 
                                cur, tgts, curT, uq >>

TOuterCaller(self) == /\ pc[self] = "TOuterCaller"
                      /\ CanLock(T(self), op[self].caller, "s") \/ Ab(self)
                      /\ IF Ab(self)
                            THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                                 /\ UNCHANGED << open, wtok, waud, wst, winv, 
                                                 sh, ex, want, finished, res >>
                            ELSE /\ IF V(T(self), op[self].caller).ex /\ V(T(self), op[self].caller).rev
                                       THEN /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                            /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                                            /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                                            /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                                            /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                                            /\ waud' = [waud EXCEPT ![(T(self))] = {}]
                                            /\ wst' = [wst EXCEPT ![(T(self))] = {}]
                                            /\ winv' = [winv EXCEPT ![(T(self))] = {}]
                                            /\ res' = [res EXCEPT ![self] = "refused_caller"]
                                            /\ finished' = [finished EXCEPT ![self] = TRUE]
                                            /\ pc' = [pc EXCEPT ![self] = "Done"]
                                       ELSE /\ IF "s" = "s"
                                                  THEN /\ sh' = [sh EXCEPT ![(op[self].caller)] = sh[(op[self].caller)] \cup {(T(self))}]
                                                       /\ ex' = ex
                                                  ELSE /\ ex' = [ex EXCEPT ![(op[self].caller)] = T(self)]
                                                       /\ sh' = sh
                                            /\ want' = [want EXCEPT ![T(self)] = <<tok[op[self].caller].sub, "s">>]
                                            /\ pc' = [pc EXCEPT ![self] = "TOuterUser"]
                                            /\ UNCHANGED << open, wtok, waud, 
                                                            wst, winv, 
                                                            finished, res >>
                      /\ UNCHANGED << op, tok, aud, starts, inval, aborted, 
                                      lvl, nAborts, issued, callerOf, rootOf, 
                                      tenOf, exchJ, rotated, srcRevAtRead, 
                                      stepTree, stepRev, stepRead, 
                                      cascCommitted, cascDone, callerAtAccept, 
                                      impChk, impAfter, fullDone, badStep, 
                                      badStepFail, badNested, revAll, inflight, 
                                      lost, cache, up, reading, readSet, 
                                      sinceRead, seen, nLoss, nRestart, ten, 
                                      phase, attempts, retries, sAtt, snap, 
                                      cur, tgts, curT, uq >>

TOuterUser(self) == /\ pc[self] = "TOuterUser"
                    /\ CanLock(T(self), tok[op[self].caller].sub, "s") \/ Ab(self)
                    /\ IF Ab(self)
                          THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                               /\ UNCHANGED << open, wtok, waud, wst, winv, sh, 
                                               ex, want, finished, res >>
                          ELSE /\ IF InvalIn(T(self), tok[op[self].caller].sub)
                                     THEN /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                          /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                                          /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                                          /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                                          /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                                          /\ waud' = [waud EXCEPT ![(T(self))] = {}]
                                          /\ wst' = [wst EXCEPT ![(T(self))] = {}]
                                          /\ winv' = [winv EXCEPT ![(T(self))] = {}]
                                          /\ res' = [res EXCEPT ![self] = "refused_user"]
                                          /\ finished' = [finished EXCEPT ![self] = TRUE]
                                          /\ pc' = [pc EXCEPT ![self] = "Done"]
                                     ELSE /\ IF "s" = "s"
                                                THEN /\ sh' = [sh EXCEPT ![(tok[op[self].caller].sub)] = sh[(tok[op[self].caller].sub)] \cup {(T(self))}]
                                                     /\ ex' = ex
                                                ELSE /\ ex' = [ex EXCEPT ![(tok[op[self].caller].sub)] = T(self)]
                                                     /\ sh' = sh
                                          /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                          /\ pc' = [pc EXCEPT ![self] = "TOuterTree"]
                                          /\ UNCHANGED << open, wtok, waud, 
                                                          wst, winv, finished, 
                                                          res >>
                    /\ UNCHANGED << op, tok, aud, starts, inval, aborted, lvl, 
                                    nAborts, issued, callerOf, rootOf, tenOf, 
                                    exchJ, rotated, srcRevAtRead, stepTree, 
                                    stepRev, stepRead, cascCommitted, cascDone, 
                                    callerAtAccept, impChk, impAfter, fullDone, 
                                    badStep, badStepFail, badNested, revAll, 
                                    inflight, lost, cache, up, reading, 
                                    readSet, sinceRead, seen, nLoss, nRestart, 
                                    ten, phase, attempts, retries, sAtt, snap, 
                                    cur, tgts, curT, uq >>

TOuterTree(self) == /\ pc[self] = "TOuterTree"
                    /\ IF Ab(self)
                          THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                               /\ UNCHANGED stepTree
                          ELSE /\ stepTree' = [stepTree EXCEPT ![self] = TreeOf(View(T(self)), {op[self].root}, ten[self])]
                               /\ pc' = [pc EXCEPT ![self] = "TOuterStarts"]
                    /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                    wtok, waud, wst, winv, lvl, sh, ex, want, 
                                    nAborts, issued, finished, res, callerOf, 
                                    rootOf, tenOf, exchJ, rotated, 
                                    srcRevAtRead, stepRev, stepRead, 
                                    cascCommitted, cascDone, callerAtAccept, 
                                    impChk, impAfter, fullDone, badStep, 
                                    badStepFail, badNested, revAll, inflight, 
                                    lost, cache, up, reading, readSet, 
                                    sinceRead, seen, nLoss, nRestart, ten, 
                                    phase, attempts, retries, sAtt, snap, cur, 
                                    tgts, curT, uq >>

TOuterStarts(self) == /\ pc[self] = "TOuterStarts"
                      /\ IF Ab(self)
                            THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                                 /\ UNCHANGED << stepRead, tgts >>
                            ELSE /\ stepRead' = [stepRead EXCEPT ![self] = TRUE]
                                 /\ tgts' = [tgts EXCEPT ![self] = {s.target : s \in {r \in StartsIn(T(self)) : r.ten = ten[self] /\ r.ijti \in stepTree[self]}} \ {ten[self]}]
                                 /\ pc' = [pc EXCEPT ![self] = "TOuterSnap"]
                      /\ UNCHANGED << op, tok, aud, starts, inval, open, 
                                      aborted, wtok, waud, wst, winv, lvl, sh, 
                                      ex, want, nAborts, issued, finished, res, 
                                      callerOf, rootOf, tenOf, exchJ, rotated, 
                                      srcRevAtRead, stepTree, stepRev, 
                                      cascCommitted, cascDone, callerAtAccept, 
                                      impChk, impAfter, fullDone, badStep, 
                                      badStepFail, badNested, revAll, inflight, 
                                      lost, cache, up, reading, readSet, 
                                      sinceRead, seen, nLoss, nRestart, ten, 
                                      phase, attempts, retries, sAtt, snap, 
                                      cur, curT, uq >>

TOuterSnap(self) == /\ pc[self] = "TOuterSnap"
                    /\ IF Ab(self)
                          THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                               /\ snap' = snap
                          ELSE /\ snap' = [snap EXCEPT ![self] = {j \in Jtis : IsImp(V(T(self), j)) /\ ~V(T(self), j).rev /\ V(T(self), j).ten = ten[self]
                                                                               /\ V(T(self), j).iten = ten[self] /\ V(T(self), j).ijti \in stepTree[self]}]
                               /\ pc' = [pc EXCEPT ![self] = "TOuterRow"]
                    /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                    wtok, waud, wst, winv, lvl, sh, ex, want, 
                                    nAborts, issued, finished, res, callerOf, 
                                    rootOf, tenOf, exchJ, rotated, 
                                    srcRevAtRead, stepTree, stepRev, stepRead, 
                                    cascCommitted, cascDone, callerAtAccept, 
                                    impChk, impAfter, fullDone, badStep, 
                                    badStepFail, badNested, revAll, inflight, 
                                    lost, cache, up, reading, readSet, 
                                    sinceRead, seen, nLoss, nRestart, ten, 
                                    phase, attempts, retries, sAtt, cur, tgts, 
                                    curT, uq >>

TOuterRow(self) == /\ pc[self] = "TOuterRow"
                   /\ IF Ab(self)
                         THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                              /\ UNCHANGED << want, cur >>
                         ELSE /\ IF snap[self] = {}
                                    THEN /\ pc' = [pc EXCEPT ![self] = "TNext"]
                                         /\ UNCHANGED << want, cur >>
                                    ELSE /\ \E j \in snap[self]:
                                              /\ cur' = [cur EXCEPT ![self] = j]
                                              /\ want' = [want EXCEPT ![T(self)] = <<j, "x">>]
                                         /\ pc' = [pc EXCEPT ![self] = "TOuterLock"]
                   /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                   wtok, waud, wst, winv, lvl, sh, ex, nAborts, 
                                   issued, finished, res, callerOf, rootOf, 
                                   tenOf, exchJ, rotated, srcRevAtRead, 
                                   stepTree, stepRev, stepRead, cascCommitted, 
                                   cascDone, callerAtAccept, impChk, impAfter, 
                                   fullDone, badStep, badStepFail, badNested, 
                                   revAll, inflight, lost, cache, up, reading, 
                                   readSet, sinceRead, seen, nLoss, nRestart, 
                                   ten, phase, attempts, retries, sAtt, snap, 
                                   tgts, curT, uq >>

TOuterLock(self) == /\ pc[self] = "TOuterLock"
                    /\ CanLock(T(self), cur[self], "x") \/ Ab(self)
                    /\ IF Ab(self)
                          THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                               /\ UNCHANGED << wtok, sh, ex, want, snap >>
                          ELSE /\ IF "x" = "s"
                                     THEN /\ sh' = [sh EXCEPT ![cur[self]] = sh[cur[self]] \cup {(T(self))}]
                                          /\ ex' = ex
                                     ELSE /\ ex' = [ex EXCEPT ![cur[self]] = T(self)]
                                          /\ sh' = sh
                               /\ want' = [want EXCEPT ![T(self)] = NoWant]
                               /\ IF ~V(T(self), cur[self]).rev
                                     THEN /\ wtok' = [wtok EXCEPT ![T(self)] = (cur[self] :> RevRow(V(T(self), cur[self]), self)) @@ wtok[T(self)]]
                                     ELSE /\ TRUE
                                          /\ wtok' = wtok
                               /\ snap' = [snap EXCEPT ![self] = snap[self] \ {cur[self]}]
                               /\ pc' = [pc EXCEPT ![self] = "TOuterRow"]
                    /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                    waud, wst, winv, lvl, nAborts, issued, 
                                    finished, res, callerOf, rootOf, tenOf, 
                                    exchJ, rotated, srcRevAtRead, stepTree, 
                                    stepRev, stepRead, cascCommitted, cascDone, 
                                    callerAtAccept, impChk, impAfter, fullDone, 
                                    badStep, badStepFail, badNested, revAll, 
                                    inflight, lost, cache, up, reading, 
                                    readSet, sinceRead, seen, nLoss, nRestart, 
                                    ten, phase, attempts, retries, sAtt, cur, 
                                    tgts, curT, uq >>

TNext(self) == /\ pc[self] = "TNext"
               /\ IF Ab(self)
                     THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                          /\ UNCHANGED << tgts, curT >>
                     ELSE /\ IF tgts[self] = {}
                                THEN /\ pc' = [pc EXCEPT ![self] = "TOuterCommit"]
                                     /\ UNCHANGED << tgts, curT >>
                                ELSE /\ \E g \in tgts[self]:
                                          /\ curT' = [curT EXCEPT ![self] = g]
                                          /\ tgts' = [tgts EXCEPT ![self] = tgts[self] \ {g}]
                                     /\ pc' = [pc EXCEPT ![self] = "TTarget"]
               /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                               wtok, waud, wst, winv, lvl, sh, ex, want, 
                               nAborts, issued, finished, res, callerOf, 
                               rootOf, tenOf, exchJ, rotated, srcRevAtRead, 
                               stepTree, stepRev, stepRead, cascCommitted, 
                               cascDone, callerAtAccept, impChk, impAfter, 
                               fullDone, badStep, badStepFail, badNested, 
                               revAll, inflight, lost, cache, up, reading, 
                               readSet, sinceRead, seen, nLoss, nRestart, ten, 
                               phase, attempts, retries, sAtt, snap, cur, uq >>

TTarget(self) == /\ pc[self] = "TTarget"
                 /\ IF Ab(self)
                       THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                            /\ snap' = snap
                       ELSE /\ snap' = [snap EXCEPT ![self] = {j \in Jtis : IsImp(V(T(self), j)) /\ ~V(T(self), j).rev /\ V(T(self), j).ten = curT[self]
                                                                            /\ V(T(self), j).iten = ten[self] /\ V(T(self), j).ijti \in stepTree[self]}]
                            /\ pc' = [pc EXCEPT ![self] = "TTargetRow"]
                 /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                 wtok, waud, wst, winv, lvl, sh, ex, want, 
                                 nAborts, issued, finished, res, callerOf, 
                                 rootOf, tenOf, exchJ, rotated, srcRevAtRead, 
                                 stepTree, stepRev, stepRead, cascCommitted, 
                                 cascDone, callerAtAccept, impChk, impAfter, 
                                 fullDone, badStep, badStepFail, badNested, 
                                 revAll, inflight, lost, cache, up, reading, 
                                 readSet, sinceRead, seen, nLoss, nRestart, 
                                 ten, phase, attempts, retries, sAtt, cur, 
                                 tgts, curT, uq >>

TTargetRow(self) == /\ pc[self] = "TTargetRow"
                    /\ IF Ab(self)
                          THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                               /\ UNCHANGED << want, cur >>
                          ELSE /\ IF snap[self] = {}
                                     THEN /\ pc' = [pc EXCEPT ![self] = "TNext"]
                                          /\ UNCHANGED << want, cur >>
                                     ELSE /\ \E j \in snap[self]:
                                               /\ cur' = [cur EXCEPT ![self] = j]
                                               /\ want' = [want EXCEPT ![T(self)] = <<j, "x">>]
                                          /\ pc' = [pc EXCEPT ![self] = "TTargetLock"]
                    /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                    wtok, waud, wst, winv, lvl, sh, ex, 
                                    nAborts, issued, finished, res, callerOf, 
                                    rootOf, tenOf, exchJ, rotated, 
                                    srcRevAtRead, stepTree, stepRev, stepRead, 
                                    cascCommitted, cascDone, callerAtAccept, 
                                    impChk, impAfter, fullDone, badStep, 
                                    badStepFail, badNested, revAll, inflight, 
                                    lost, cache, up, reading, readSet, 
                                    sinceRead, seen, nLoss, nRestart, ten, 
                                    phase, attempts, retries, sAtt, snap, tgts, 
                                    curT, uq >>

TTargetLock(self) == /\ pc[self] = "TTargetLock"
                     /\ CanLock(T(self), cur[self], "x") \/ Ab(self)
                     /\ IF Ab(self)
                           THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                                /\ UNCHANGED << wtok, sh, ex, want, snap >>
                           ELSE /\ IF "x" = "s"
                                      THEN /\ sh' = [sh EXCEPT ![cur[self]] = sh[cur[self]] \cup {(T(self))}]
                                           /\ ex' = ex
                                      ELSE /\ ex' = [ex EXCEPT ![cur[self]] = T(self)]
                                           /\ sh' = sh
                                /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                /\ IF ~V(T(self), cur[self]).rev
                                      THEN /\ wtok' = [wtok EXCEPT ![T(self)] = (cur[self] :> RevRow(V(T(self), cur[self]), self)) @@ wtok[T(self)]]
                                      ELSE /\ TRUE
                                           /\ wtok' = wtok
                                /\ snap' = [snap EXCEPT ![self] = snap[self] \ {cur[self]}]
                                /\ pc' = [pc EXCEPT ![self] = "TTargetRow"]
                     /\ UNCHANGED << op, tok, aud, starts, inval, open, 
                                     aborted, waud, wst, winv, lvl, nAborts, 
                                     issued, finished, res, callerOf, rootOf, 
                                     tenOf, exchJ, rotated, srcRevAtRead, 
                                     stepTree, stepRev, stepRead, 
                                     cascCommitted, cascDone, callerAtAccept, 
                                     impChk, impAfter, fullDone, badStep, 
                                     badStepFail, badNested, revAll, inflight, 
                                     lost, cache, up, reading, readSet, 
                                     sinceRead, seen, nLoss, nRestart, ten, 
                                     phase, attempts, retries, sAtt, cur, tgts, 
                                     curT, uq >>

TOuterCommit(self) == /\ pc[self] = "TOuterCommit"
                      /\ IF Ab(self)
                            THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                                 /\ UNCHANGED << tok, aud, starts, inval, open, 
                                                 wtok, sh, ex, stepRev, 
                                                 badStep, badNested, inflight >>
                            ELSE /\ stepRev' = [stepRev EXCEPT ![self] = {j \in DOMAIN wtok[T(self)] : wtok[T(self)][j].ten = ten[self]}]
                                 /\ IF \E j \in DOMAIN wtok[T(self)] :
                                         /\ wtok[T(self)][j].ten /= ten[self]
                                         /\ ~(/\ IsImp(wtok[T(self)][j]) /\ wtok[T(self)][j].iten = ten[self]
                                              /\ wtok[T(self)][j].ijti \in stepTree[self]
                                              /\ \E r \in StartsIn(T(self)) : r.ten = ten[self] /\ r.target = wtok[T(self)][j].ten /\ r.ijti \in stepTree[self])
                                       THEN /\ badNested' = TRUE
                                       ELSE /\ TRUE
                                            /\ UNCHANGED badNested
                                 /\ IF DOMAIN wtok[T(self)] /= {} /\ tok[op[self].caller].ex /\ tok[op[self].caller].rev /\ tok[op[self].caller].by /= self
                                       THEN /\ badStep' = TRUE
                                       ELSE /\ TRUE
                                            /\ UNCHANGED badStep
                                 /\ inflight' = (inflight \cup (Reps \X NewRevs((T(self)))))
                                 /\ tok' = Apply((T(self)))
                                 /\ aud' = (aud \cup waud[(T(self))])
                                 /\ starts' = (starts \cup wst[(T(self))])
                                 /\ inval' = (inval \cup winv[(T(self))])
                                 /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                                 /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                                 /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                                 /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                                 /\ pc' = [pc EXCEPT ![self] = "Casc"]
                      /\ UNCHANGED << op, aborted, waud, wst, winv, lvl, want, 
                                      nAborts, issued, finished, res, callerOf, 
                                      rootOf, tenOf, exchJ, rotated, 
                                      srcRevAtRead, stepTree, stepRead, 
                                      cascCommitted, cascDone, callerAtAccept, 
                                      impChk, impAfter, fullDone, badStepFail, 
                                      revAll, lost, cache, up, reading, 
                                      readSet, sinceRead, seen, nLoss, 
                                      nRestart, ten, phase, attempts, retries, 
                                      sAtt, snap, cur, tgts, curT, uq >>

Casc(self) == /\ pc[self] = "Casc"
              /\ phase' = [phase EXCEPT ![self] = "casc"]
              /\ attempts' = [attempts EXCEPT ![self] = attempts[self] + 1]
              /\ lvl' = [lvl EXCEPT ![self] = 1]
              /\ open' = [open EXCEPT ![<<self, 1>>] = TRUE]
              /\ aborted' = [aborted EXCEPT ![<<self, 1>>] = ""]
              /\ wtok' = [wtok EXCEPT ![<<self, 1>>] = EmptyW]
              /\ waud' = [waud EXCEPT ![<<self, 1>>] = {}]
              /\ wst' = [wst EXCEPT ![<<self, 1>>] = {}]
              /\ winv' = [winv EXCEPT ![<<self, 1>>] = {}]
              /\ IF IsTF
                    THEN /\ want' = [want EXCEPT ![<<self, 1>>] = <<ten[self], "x">>]
                         /\ pc' = [pc EXCEPT ![self] = "CascAdv"]
                    ELSE /\ want' = [want EXCEPT ![<<self, 1>>] = <<op[self].caller, "s">>]
                         /\ pc' = [pc EXCEPT ![self] = "CascCaller"]
              /\ UNCHANGED << op, tok, aud, starts, inval, sh, ex, nAborts, 
                              issued, finished, res, callerOf, rootOf, tenOf, 
                              exchJ, rotated, srcRevAtRead, stepTree, stepRev, 
                              stepRead, cascCommitted, cascDone, 
                              callerAtAccept, impChk, impAfter, fullDone, 
                              badStep, badStepFail, badNested, revAll, 
                              inflight, lost, cache, up, reading, readSet, 
                              sinceRead, seen, nLoss, nRestart, ten, retries, 
                              sAtt, snap, cur, tgts, curT, uq >>

CascAdv(self) == /\ pc[self] = "CascAdv"
                 /\ CanLock(T(self), ten[self], "x") \/ Ab(self)
                 /\ IF Ab(self)
                       THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                            /\ UNCHANGED << sh, ex, want >>
                       ELSE /\ IF "x" = "s"
                                  THEN /\ sh' = [sh EXCEPT ![ten[self]] = sh[ten[self]] \cup {(T(self))}]
                                       /\ ex' = ex
                                  ELSE /\ ex' = [ex EXCEPT ![ten[self]] = T(self)]
                                       /\ sh' = sh
                            /\ want' = [want EXCEPT ![T(self)] = <<op[self].caller, "s">>]
                            /\ pc' = [pc EXCEPT ![self] = "CascCaller"]
                 /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                 wtok, waud, wst, winv, lvl, nAborts, issued, 
                                 finished, res, callerOf, rootOf, tenOf, exchJ, 
                                 rotated, srcRevAtRead, stepTree, stepRev, 
                                 stepRead, cascCommitted, cascDone, 
                                 callerAtAccept, impChk, impAfter, fullDone, 
                                 badStep, badStepFail, badNested, revAll, 
                                 inflight, lost, cache, up, reading, readSet, 
                                 sinceRead, seen, nLoss, nRestart, ten, phase, 
                                 attempts, retries, sAtt, snap, cur, tgts, 
                                 curT, uq >>

CascCaller(self) == /\ pc[self] = "CascCaller"
                    /\ CanLock(T(self), op[self].caller, "s") \/ Ab(self)
                    /\ IF Ab(self)
                          THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                               /\ UNCHANGED << open, wtok, waud, wst, winv, sh, 
                                               ex, want, finished, res >>
                          ELSE /\ IF V(T(self), op[self].caller).ex /\ V(T(self), op[self].caller).rev /\ ~(IsTF /\ op[self].caller \in stepRev[self])
                                     THEN /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                          /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                                          /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                                          /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                                          /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                                          /\ waud' = [waud EXCEPT ![(T(self))] = {}]
                                          /\ wst' = [wst EXCEPT ![(T(self))] = {}]
                                          /\ winv' = [winv EXCEPT ![(T(self))] = {}]
                                          /\ res' = [res EXCEPT ![self] = "refused_caller"]
                                          /\ finished' = [finished EXCEPT ![self] = TRUE]
                                          /\ pc' = [pc EXCEPT ![self] = "Done"]
                                     ELSE /\ IF "s" = "s"
                                                THEN /\ sh' = [sh EXCEPT ![(op[self].caller)] = sh[(op[self].caller)] \cup {(T(self))}]
                                                     /\ ex' = ex
                                                ELSE /\ ex' = [ex EXCEPT ![(op[self].caller)] = T(self)]
                                                     /\ sh' = sh
                                          /\ IF IsTF
                                                THEN /\ want' = [want EXCEPT ![T(self)] = <<tok[op[self].caller].sub, "s">>]
                                                     /\ pc' = [pc EXCEPT ![self] = "CascUser"]
                                                ELSE /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                                     /\ pc' = [pc EXCEPT ![self] = "CascSnap"]
                                          /\ UNCHANGED << open, wtok, waud, 
                                                          wst, winv, finished, 
                                                          res >>
                    /\ UNCHANGED << op, tok, aud, starts, inval, aborted, lvl, 
                                    nAborts, issued, callerOf, rootOf, tenOf, 
                                    exchJ, rotated, srcRevAtRead, stepTree, 
                                    stepRev, stepRead, cascCommitted, cascDone, 
                                    callerAtAccept, impChk, impAfter, fullDone, 
                                    badStep, badStepFail, badNested, revAll, 
                                    inflight, lost, cache, up, reading, 
                                    readSet, sinceRead, seen, nLoss, nRestart, 
                                    ten, phase, attempts, retries, sAtt, snap, 
                                    cur, tgts, curT, uq >>

CascUser(self) == /\ pc[self] = "CascUser"
                  /\ CanLock(T(self), tok[op[self].caller].sub, "s") \/ Ab(self)
                  /\ IF Ab(self)
                        THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                             /\ UNCHANGED << open, wtok, waud, wst, winv, sh, 
                                             ex, want, finished, res >>
                        ELSE /\ IF InvalIn(T(self), tok[op[self].caller].sub)
                                   THEN /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                        /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                                        /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                                        /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                                        /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                                        /\ waud' = [waud EXCEPT ![(T(self))] = {}]
                                        /\ wst' = [wst EXCEPT ![(T(self))] = {}]
                                        /\ winv' = [winv EXCEPT ![(T(self))] = {}]
                                        /\ res' = [res EXCEPT ![self] = "refused_user"]
                                        /\ finished' = [finished EXCEPT ![self] = TRUE]
                                        /\ pc' = [pc EXCEPT ![self] = "Done"]
                                   ELSE /\ IF "s" = "s"
                                              THEN /\ sh' = [sh EXCEPT ![(tok[op[self].caller].sub)] = sh[(tok[op[self].caller].sub)] \cup {(T(self))}]
                                                   /\ ex' = ex
                                              ELSE /\ ex' = [ex EXCEPT ![(tok[op[self].caller].sub)] = T(self)]
                                                   /\ sh' = sh
                                        /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                        /\ pc' = [pc EXCEPT ![self] = "CascSnap"]
                                        /\ UNCHANGED << open, wtok, waud, wst, 
                                                        winv, finished, res >>
                  /\ UNCHANGED << op, tok, aud, starts, inval, aborted, lvl, 
                                  nAborts, issued, callerOf, rootOf, tenOf, 
                                  exchJ, rotated, srcRevAtRead, stepTree, 
                                  stepRev, stepRead, cascCommitted, cascDone, 
                                  callerAtAccept, impChk, impAfter, fullDone, 
                                  badStep, badStepFail, badNested, revAll, 
                                  inflight, lost, cache, up, reading, readSet, 
                                  sinceRead, seen, nLoss, nRestart, ten, phase, 
                                  attempts, retries, sAtt, snap, cur, tgts, 
                                  curT, uq >>

CascSnap(self) == /\ pc[self] = "CascSnap"
                  /\ IF Ab(self)
                        THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                             /\ snap' = snap
                        ELSE /\ snap' = [snap EXCEPT ![self] = TreeOf(View(T(self)), {op[self].root}, ten[self]) \cap LiveIn(View(T(self)))]
                             /\ pc' = [pc EXCEPT ![self] = "CascRow"]
                  /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                  wtok, waud, wst, winv, lvl, sh, ex, want, 
                                  nAborts, issued, finished, res, callerOf, 
                                  rootOf, tenOf, exchJ, rotated, srcRevAtRead, 
                                  stepTree, stepRev, stepRead, cascCommitted, 
                                  cascDone, callerAtAccept, impChk, impAfter, 
                                  fullDone, badStep, badStepFail, badNested, 
                                  revAll, inflight, lost, cache, up, reading, 
                                  readSet, sinceRead, seen, nLoss, nRestart, 
                                  ten, phase, attempts, retries, sAtt, cur, 
                                  tgts, curT, uq >>

CascRow(self) == /\ pc[self] = "CascRow"
                 /\ IF Ab(self)
                       THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                            /\ UNCHANGED << want, cur >>
                       ELSE /\ IF snap[self] = {}
                                  THEN /\ pc' = [pc EXCEPT ![self] = "CascCommit"]
                                       /\ UNCHANGED << want, cur >>
                                  ELSE /\ \E j \in snap[self]:
                                            /\ cur' = [cur EXCEPT ![self] = j]
                                            /\ want' = [want EXCEPT ![T(self)] = <<j, "x">>]
                                       /\ pc' = [pc EXCEPT ![self] = "CascLock"]
                 /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                 wtok, waud, wst, winv, lvl, sh, ex, nAborts, 
                                 issued, finished, res, callerOf, rootOf, 
                                 tenOf, exchJ, rotated, srcRevAtRead, stepTree, 
                                 stepRev, stepRead, cascCommitted, cascDone, 
                                 callerAtAccept, impChk, impAfter, fullDone, 
                                 badStep, badStepFail, badNested, revAll, 
                                 inflight, lost, cache, up, reading, readSet, 
                                 sinceRead, seen, nLoss, nRestart, ten, phase, 
                                 attempts, retries, sAtt, snap, tgts, curT, uq >>

CascLock(self) == /\ pc[self] = "CascLock"
                  /\ CanLock(T(self), cur[self], "x") \/ Ab(self)
                  /\ IF Ab(self)
                        THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                             /\ UNCHANGED << wtok, sh, ex, want, snap >>
                        ELSE /\ IF "x" = "s"
                                   THEN /\ sh' = [sh EXCEPT ![cur[self]] = sh[cur[self]] \cup {(T(self))}]
                                        /\ ex' = ex
                                   ELSE /\ ex' = [ex EXCEPT ![cur[self]] = T(self)]
                                        /\ sh' = sh
                             /\ want' = [want EXCEPT ![T(self)] = NoWant]
                             /\ IF ~V(T(self), cur[self]).rev
                                   THEN /\ wtok' = [wtok EXCEPT ![T(self)] = (cur[self] :> RevRow(V(T(self), cur[self]), self)) @@ wtok[T(self)]]
                                   ELSE /\ TRUE
                                        /\ wtok' = wtok
                             /\ snap' = [snap EXCEPT ![self] = snap[self] \ {cur[self]}]
                             /\ pc' = [pc EXCEPT ![self] = "CascRow"]
                  /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                  waud, wst, winv, lvl, nAborts, issued, 
                                  finished, res, callerOf, rootOf, tenOf, 
                                  exchJ, rotated, srcRevAtRead, stepTree, 
                                  stepRev, stepRead, cascCommitted, cascDone, 
                                  callerAtAccept, impChk, impAfter, fullDone, 
                                  badStep, badStepFail, badNested, revAll, 
                                  inflight, lost, cache, up, reading, readSet, 
                                  sinceRead, seen, nLoss, nRestart, ten, phase, 
                                  attempts, retries, sAtt, cur, tgts, curT, uq >>

CascCommit(self) == /\ pc[self] = "CascCommit"
                    /\ IF Ab(self)
                          THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                               /\ UNCHANGED << tok, aud, starts, inval, open, 
                                               wtok, sh, ex, finished, res, 
                                               cascCommitted, cascDone, 
                                               callerAtAccept, inflight >>
                          ELSE /\ callerAtAccept' = [callerAtAccept EXCEPT ![self] = IF ~V(T(self), op[self].caller).ex \/ ~V(T(self), op[self].caller).rev THEN "live"
                                                                                     ELSE IF V(T(self), op[self].caller).by = self THEN "own" ELSE "other"]
                               /\ cascDone' = (cascDone \cup {[p |-> self, root |-> op[self].root, ten |-> ten[self],
                                                               tree |-> TreeOf(View(T(self)), {op[self].root}, ten[self])]})
                               /\ inflight' = (inflight \cup (Reps \X NewRevs((T(self)))))
                               /\ tok' = Apply((T(self)))
                               /\ aud' = (aud \cup waud[(T(self))])
                               /\ starts' = (starts \cup wst[(T(self))])
                               /\ inval' = (inval \cup winv[(T(self))])
                               /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                               /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                               /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                               /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                               /\ cascCommitted' = [cascCommitted EXCEPT ![self] = TRUE]
                               /\ res' = [res EXCEPT ![self] = "accepted"]
                               /\ finished' = [finished EXCEPT ![self] = TRUE]
                               /\ pc' = [pc EXCEPT ![self] = "Done"]
                    /\ UNCHANGED << op, aborted, waud, wst, winv, lvl, want, 
                                    nAborts, issued, callerOf, rootOf, tenOf, 
                                    exchJ, rotated, srcRevAtRead, stepTree, 
                                    stepRev, stepRead, impChk, impAfter, 
                                    fullDone, badStep, badStepFail, badNested, 
                                    revAll, lost, cache, up, reading, readSet, 
                                    sinceRead, seen, nLoss, nRestart, ten, 
                                    phase, attempts, retries, sAtt, snap, cur, 
                                    tgts, curT, uq >>

IChk(self) == /\ pc[self] = "IChk"
              /\ ten' = [ten EXCEPT ![self] = tok[op[self].cb].ten]
              /\ IF IsTF
                    THEN /\ pc' = [pc EXCEPT ![self] = "IOuter"]
                         /\ UNCHANGED << finished, res, srcRevAtRead, impChk >>
                    ELSE /\ impChk' = [impChk EXCEPT ![self] = {r \in Procs : ~cascCommitted[r]}]
                         /\ IF tok[op[self].cb].rev \/ IsImp(tok[op[self].cb])
                               THEN /\ res' = [res EXCEPT ![self] = "refused_caller"]
                                    /\ finished' = [finished EXCEPT ![self] = TRUE]
                                    /\ pc' = [pc EXCEPT ![self] = "Done"]
                                    /\ UNCHANGED srcRevAtRead
                               ELSE /\ srcRevAtRead' = [srcRevAtRead EXCEPT ![self] = {s \in {op[self].cb} : tok[s].rev}]
                                    /\ pc' = [pc EXCEPT ![self] = "IOuter"]
                                    /\ UNCHANGED << finished, res >>
              /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, wtok, 
                              waud, wst, winv, lvl, sh, ex, want, nAborts, 
                              issued, callerOf, rootOf, tenOf, exchJ, rotated, 
                              stepTree, stepRev, stepRead, cascCommitted, 
                              cascDone, callerAtAccept, impAfter, fullDone, 
                              badStep, badStepFail, badNested, revAll, 
                              inflight, lost, cache, up, reading, readSet, 
                              sinceRead, seen, nLoss, nRestart, phase, 
                              attempts, retries, sAtt, snap, cur, tgts, curT, 
                              uq >>

IOuter(self) == /\ pc[self] = "IOuter"
                /\ phase' = [phase EXCEPT ![self] = "imp"]
                /\ lvl' = [lvl EXCEPT ![self] = 1]
                /\ open' = [open EXCEPT ![<<self, 1>>] = TRUE]
                /\ aborted' = [aborted EXCEPT ![<<self, 1>>] = ""]
                /\ wtok' = [wtok EXCEPT ![<<self, 1>>] = EmptyW]
                /\ waud' = [waud EXCEPT ![<<self, 1>>] = {}]
                /\ wst' = [wst EXCEPT ![<<self, 1>>] = {}]
                /\ winv' = [winv EXCEPT ![<<self, 1>>] = {}]
                /\ IF IsTF
                      THEN /\ want' = [want EXCEPT ![<<self, 1>>] = <<op[self].cb, "s">>]
                           /\ pc' = [pc EXCEPT ![self] = "IOuterCb"]
                      ELSE /\ want' = [want EXCEPT ![<<self, 1>>] = <<tok[op[self].cb].sub, "s">>]
                           /\ pc' = [pc EXCEPT ![self] = "IOuterUser"]
                /\ UNCHANGED << op, tok, aud, starts, inval, sh, ex, nAborts, 
                                issued, finished, res, callerOf, rootOf, tenOf, 
                                exchJ, rotated, srcRevAtRead, stepTree, 
                                stepRev, stepRead, cascCommitted, cascDone, 
                                callerAtAccept, impChk, impAfter, fullDone, 
                                badStep, badStepFail, badNested, revAll, 
                                inflight, lost, cache, up, reading, readSet, 
                                sinceRead, seen, nLoss, nRestart, ten, 
                                attempts, retries, sAtt, snap, cur, tgts, curT, 
                                uq >>

IOuterCb(self) == /\ pc[self] = "IOuterCb"
                  /\ CanLock(T(self), op[self].cb, "s") \/ Ab(self)
                  /\ IF Ab(self)
                        THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                             /\ UNCHANGED << open, wtok, waud, wst, winv, sh, 
                                             ex, want, finished, res, 
                                             srcRevAtRead, impChk >>
                        ELSE /\ IF V(T(self), op[self].cb).rev \/ IsImp(V(T(self), op[self].cb))
                                   THEN /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                        /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                                        /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                                        /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                                        /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                                        /\ waud' = [waud EXCEPT ![(T(self))] = {}]
                                        /\ wst' = [wst EXCEPT ![(T(self))] = {}]
                                        /\ winv' = [winv EXCEPT ![(T(self))] = {}]
                                        /\ res' = [res EXCEPT ![self] = "refused_caller"]
                                        /\ finished' = [finished EXCEPT ![self] = TRUE]
                                        /\ pc' = [pc EXCEPT ![self] = "Done"]
                                        /\ UNCHANGED << srcRevAtRead, impChk >>
                                   ELSE /\ IF "s" = "s"
                                              THEN /\ sh' = [sh EXCEPT ![(op[self].cb)] = sh[(op[self].cb)] \cup {(T(self))}]
                                                   /\ ex' = ex
                                              ELSE /\ ex' = [ex EXCEPT ![(op[self].cb)] = T(self)]
                                                   /\ sh' = sh
                                        /\ impChk' = [impChk EXCEPT ![self] = {r \in Procs : ~cascCommitted[r]}]
                                        /\ srcRevAtRead' = [srcRevAtRead EXCEPT ![self] = {s \in {op[self].cb} : tok[s].rev}]
                                        /\ want' = [want EXCEPT ![T(self)] = <<tok[op[self].cb].sub, "s">>]
                                        /\ pc' = [pc EXCEPT ![self] = "IOuterUser"]
                                        /\ UNCHANGED << open, wtok, waud, wst, 
                                                        winv, finished, res >>
                  /\ UNCHANGED << op, tok, aud, starts, inval, aborted, lvl, 
                                  nAborts, issued, callerOf, rootOf, tenOf, 
                                  exchJ, rotated, stepTree, stepRev, stepRead, 
                                  cascCommitted, cascDone, callerAtAccept, 
                                  impAfter, fullDone, badStep, badStepFail, 
                                  badNested, revAll, inflight, lost, cache, up, 
                                  reading, readSet, sinceRead, seen, nLoss, 
                                  nRestart, ten, phase, attempts, retries, 
                                  sAtt, snap, cur, tgts, curT, uq >>

IOuterUser(self) == /\ pc[self] = "IOuterUser"
                    /\ CanLock(T(self), tok[op[self].cb].sub, "s") \/ Ab(self)
                    /\ IF Ab(self)
                          THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                               /\ UNCHANGED << open, wtok, waud, wst, winv, sh, 
                                               ex, want, finished, res >>
                          ELSE /\ IF InvalIn(T(self), tok[op[self].cb].sub)
                                     THEN /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                          /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                                          /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                                          /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                                          /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                                          /\ waud' = [waud EXCEPT ![(T(self))] = {}]
                                          /\ wst' = [wst EXCEPT ![(T(self))] = {}]
                                          /\ winv' = [winv EXCEPT ![(T(self))] = {}]
                                          /\ res' = [res EXCEPT ![self] = "refused_user"]
                                          /\ finished' = [finished EXCEPT ![self] = TRUE]
                                          /\ pc' = [pc EXCEPT ![self] = "Done"]
                                     ELSE /\ IF "s" = "s"
                                                THEN /\ sh' = [sh EXCEPT ![(tok[op[self].cb].sub)] = sh[(tok[op[self].cb].sub)] \cup {(T(self))}]
                                                     /\ ex' = ex
                                                ELSE /\ ex' = [ex EXCEPT ![(tok[op[self].cb].sub)] = T(self)]
                                                     /\ sh' = sh
                                          /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                          /\ IF IsTF
                                                THEN /\ wst' = [wst EXCEPT ![T(self)] = {[ten |-> ten[self], ijti |-> op[self].cb, target |-> op[self].tt, bjti |-> NewJ(self)]}]
                                                ELSE /\ TRUE
                                                     /\ wst' = wst
                                          /\ pc' = [pc EXCEPT ![self] = "INested"]
                                          /\ UNCHANGED << open, wtok, waud, 
                                                          winv, finished, res >>
                    /\ UNCHANGED << op, tok, aud, starts, inval, aborted, lvl, 
                                    nAborts, issued, callerOf, rootOf, tenOf, 
                                    exchJ, rotated, srcRevAtRead, stepTree, 
                                    stepRev, stepRead, cascCommitted, cascDone, 
                                    callerAtAccept, impChk, impAfter, fullDone, 
                                    badStep, badStepFail, badNested, revAll, 
                                    inflight, lost, cache, up, reading, 
                                    readSet, sinceRead, seen, nLoss, nRestart, 
                                    ten, phase, attempts, retries, sAtt, snap, 
                                    cur, tgts, curT, uq >>

INested(self) == /\ pc[self] = "INested"
                 /\ IF Ab(self)
                       THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                            /\ UNCHANGED << open, aborted, wtok, waud, wst, 
                                            winv, lvl, want >>
                       ELSE /\ lvl' = [lvl EXCEPT ![self] = 2]
                            /\ open' = [open EXCEPT ![<<self, 2>>] = TRUE]
                            /\ aborted' = [aborted EXCEPT ![<<self, 2>>] = ""]
                            /\ wtok' = [wtok EXCEPT ![<<self, 2>>] = EmptyW]
                            /\ waud' = [waud EXCEPT ![<<self, 2>>] = {}]
                            /\ wst' = [wst EXCEPT ![<<self, 2>>] = {}]
                            /\ winv' = [winv EXCEPT ![<<self, 2>>] = {}]
                            /\ want' = [want EXCEPT ![<<self, 2>>] = <<op[self].tu, "s">>]
                            /\ pc' = [pc EXCEPT ![self] = "INestedUser"]
                 /\ UNCHANGED << op, tok, aud, starts, inval, sh, ex, nAborts, 
                                 issued, finished, res, callerOf, rootOf, 
                                 tenOf, exchJ, rotated, srcRevAtRead, stepTree, 
                                 stepRev, stepRead, cascCommitted, cascDone, 
                                 callerAtAccept, impChk, impAfter, fullDone, 
                                 badStep, badStepFail, badNested, revAll, 
                                 inflight, lost, cache, up, reading, readSet, 
                                 sinceRead, seen, nLoss, nRestart, ten, phase, 
                                 attempts, retries, sAtt, snap, cur, tgts, 
                                 curT, uq >>

INestedUser(self) == /\ pc[self] = "INestedUser"
                     /\ CanLock(T(self), op[self].tu, "s") \/ Ab(self)
                     /\ IF Ab(self)
                           THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                                /\ UNCHANGED << open, wtok, waud, wst, winv, 
                                                sh, ex, want, res >>
                           ELSE /\ IF InvalIn(T(self), op[self].tu)
                                      THEN /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                           /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                                           /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                                           /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                                           /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                                           /\ waud' = [waud EXCEPT ![(T(self))] = {}]
                                           /\ wst' = [wst EXCEPT ![(T(self))] = {}]
                                           /\ winv' = [winv EXCEPT ![(T(self))] = {}]
                                           /\ res' = [res EXCEPT ![self] = "refused_user"]
                                           /\ pc' = [pc EXCEPT ![self] = "Quit"]
                                      ELSE /\ IF "s" = "s"
                                                 THEN /\ sh' = [sh EXCEPT ![(op[self].tu)] = sh[(op[self].tu)] \cup {(T(self))}]
                                                      /\ ex' = ex
                                                 ELSE /\ ex' = [ex EXCEPT ![(op[self].tu)] = T(self)]
                                                      /\ sh' = sh
                                           /\ want' = [want EXCEPT ![T(self)] = NoWant]
                                           /\ wtok' = [wtok EXCEPT ![T(self)] = (NewJ(self) :> Row(op[self].tt, op[self].tu, "", tok[op[self].cb].sub, ten[self], op[self].cb)) @@ wtok[T(self)]]
                                           /\ pc' = [pc EXCEPT ![self] = "INestedCommit"]
                                           /\ UNCHANGED << open, waud, wst, 
                                                           winv, res >>
                     /\ UNCHANGED << op, tok, aud, starts, inval, aborted, lvl, 
                                     nAborts, issued, finished, callerOf, 
                                     rootOf, tenOf, exchJ, rotated, 
                                     srcRevAtRead, stepTree, stepRev, stepRead, 
                                     cascCommitted, cascDone, callerAtAccept, 
                                     impChk, impAfter, fullDone, badStep, 
                                     badStepFail, badNested, revAll, inflight, 
                                     lost, cache, up, reading, readSet, 
                                     sinceRead, seen, nLoss, nRestart, ten, 
                                     phase, attempts, retries, sAtt, snap, cur, 
                                     tgts, curT, uq >>

INestedCommit(self) == /\ pc[self] = "INestedCommit"
                       /\ IF Ab(self)
                             THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                                  /\ UNCHANGED << tok, aud, starts, inval, 
                                                  open, wtok, lvl, sh, ex, 
                                                  impAfter, inflight >>
                             ELSE /\ inflight' = (inflight \cup (Reps \X NewRevs((T(self)))))
                                  /\ tok' = Apply((T(self)))
                                  /\ aud' = (aud \cup waud[(T(self))])
                                  /\ starts' = (starts \cup wst[(T(self))])
                                  /\ inval' = (inval \cup winv[(T(self))])
                                  /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                                  /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                                  /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                                  /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                                  /\ lvl' = [lvl EXCEPT ![self] = 1]
                                  /\ IF ~IsTF
                                        THEN /\ impAfter' = [impAfter EXCEPT ![self] = {r \in Procs : stepRead[r]}]
                                        ELSE /\ TRUE
                                             /\ UNCHANGED impAfter
                                  /\ pc' = [pc EXCEPT ![self] = "IOuterCommit"]
                       /\ UNCHANGED << op, aborted, waud, wst, winv, want, 
                                       nAborts, issued, finished, res, 
                                       callerOf, rootOf, tenOf, exchJ, rotated, 
                                       srcRevAtRead, stepTree, stepRev, 
                                       stepRead, cascCommitted, cascDone, 
                                       callerAtAccept, impChk, fullDone, 
                                       badStep, badStepFail, badNested, revAll, 
                                       lost, cache, up, reading, readSet, 
                                       sinceRead, seen, nLoss, nRestart, ten, 
                                       phase, attempts, retries, sAtt, snap, 
                                       cur, tgts, curT, uq >>

IOuterCommit(self) == /\ pc[self] = "IOuterCommit"
                      /\ IF Ab(self)
                            THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                                 /\ UNCHANGED << tok, aud, starts, inval, open, 
                                                 wtok, sh, ex, issued, 
                                                 finished, res, impAfter, 
                                                 inflight >>
                            ELSE /\ inflight' = (inflight \cup (Reps \X NewRevs((T(self)))))
                                 /\ tok' = Apply((T(self)))
                                 /\ aud' = (aud \cup waud[(T(self))])
                                 /\ starts' = (starts \cup wst[(T(self))])
                                 /\ inval' = (inval \cup winv[(T(self))])
                                 /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                                 /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                                 /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                                 /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                                 /\ IF IsTF
                                       THEN /\ impAfter' = [impAfter EXCEPT ![self] = {r \in Procs : stepRead[r] /\ ~cascCommitted[r]}]
                                       ELSE /\ TRUE
                                            /\ UNCHANGED impAfter
                                 /\ issued' = (issued \cup {NewJ(self)})
                                 /\ res' = [res EXCEPT ![self] = "issued"]
                                 /\ finished' = [finished EXCEPT ![self] = TRUE]
                                 /\ pc' = [pc EXCEPT ![self] = "Done"]
                      /\ UNCHANGED << op, aborted, waud, wst, winv, lvl, want, 
                                      nAborts, callerOf, rootOf, tenOf, exchJ, 
                                      rotated, srcRevAtRead, stepTree, stepRev, 
                                      stepRead, cascCommitted, cascDone, 
                                      callerAtAccept, impChk, fullDone, 
                                      badStep, badStepFail, badNested, revAll, 
                                      lost, cache, up, reading, readSet, 
                                      sinceRead, seen, nLoss, nRestart, ten, 
                                      phase, attempts, retries, sAtt, snap, 
                                      cur, tgts, curT, uq >>

FLevel(self) == /\ pc[self] = "FLevel"
                /\ phase' = [phase EXCEPT ![self] = "full"]
                /\ lvl' = [lvl EXCEPT ![self] = 1]
                /\ open' = [open EXCEPT ![<<self, 1>>] = TRUE]
                /\ aborted' = [aborted EXCEPT ![<<self, 1>>] = ""]
                /\ wtok' = [wtok EXCEPT ![<<self, 1>>] = EmptyW]
                /\ waud' = [waud EXCEPT ![<<self, 1>>] = {}]
                /\ wst' = [wst EXCEPT ![<<self, 1>>] = {}]
                /\ winv' = [winv EXCEPT ![<<self, 1>>] = {}]
                /\ want' = [want EXCEPT ![<<self, 1>>] = <<op[self].user, "x">>]
                /\ pc' = [pc EXCEPT ![self] = "FLevelLock"]
                /\ UNCHANGED << op, tok, aud, starts, inval, sh, ex, nAborts, 
                                issued, finished, res, callerOf, rootOf, tenOf, 
                                exchJ, rotated, srcRevAtRead, stepTree, 
                                stepRev, stepRead, cascCommitted, cascDone, 
                                callerAtAccept, impChk, impAfter, fullDone, 
                                badStep, badStepFail, badNested, revAll, 
                                inflight, lost, cache, up, reading, readSet, 
                                sinceRead, seen, nLoss, nRestart, ten, 
                                attempts, retries, sAtt, snap, cur, tgts, curT, 
                                uq >>

FLevelLock(self) == /\ pc[self] = "FLevelLock"
                    /\ CanLock(T(self), op[self].user, "x") \/ Ab(self)
                    /\ IF Ab(self)
                          THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                               /\ UNCHANGED << winv, sh, ex, want >>
                          ELSE /\ IF "x" = "s"
                                     THEN /\ sh' = [sh EXCEPT ![(op[self].user)] = sh[(op[self].user)] \cup {(T(self))}]
                                          /\ ex' = ex
                                     ELSE /\ ex' = [ex EXCEPT ![(op[self].user)] = T(self)]
                                          /\ sh' = sh
                               /\ want' = [want EXCEPT ![T(self)] = NoWant]
                               /\ winv' = [winv EXCEPT ![T(self)] = {op[self].user}]
                               /\ pc' = [pc EXCEPT ![self] = "FLevelCommit"]
                    /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                    wtok, waud, wst, lvl, nAborts, issued, 
                                    finished, res, callerOf, rootOf, tenOf, 
                                    exchJ, rotated, srcRevAtRead, stepTree, 
                                    stepRev, stepRead, cascCommitted, cascDone, 
                                    callerAtAccept, impChk, impAfter, fullDone, 
                                    badStep, badStepFail, badNested, revAll, 
                                    inflight, lost, cache, up, reading, 
                                    readSet, sinceRead, seen, nLoss, nRestart, 
                                    ten, phase, attempts, retries, sAtt, snap, 
                                    cur, tgts, curT, uq >>

FLevelCommit(self) == /\ pc[self] = "FLevelCommit"
                      /\ IF Ab(self)
                            THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                                 /\ UNCHANGED << tok, aud, starts, inval, open, 
                                                 wtok, sh, ex, inflight >>
                            ELSE /\ inflight' = (inflight \cup (Reps \X NewRevs((T(self)))))
                                 /\ tok' = Apply((T(self)))
                                 /\ aud' = (aud \cup waud[(T(self))])
                                 /\ starts' = (starts \cup wst[(T(self))])
                                 /\ inval' = (inval \cup winv[(T(self))])
                                 /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                                 /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                                 /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                                 /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                                 /\ pc' = [pc EXCEPT ![self] = "FSub"]
                      /\ UNCHANGED << op, aborted, waud, wst, winv, lvl, want, 
                                      nAborts, issued, finished, res, callerOf, 
                                      rootOf, tenOf, exchJ, rotated, 
                                      srcRevAtRead, stepTree, stepRev, 
                                      stepRead, cascCommitted, cascDone, 
                                      callerAtAccept, impChk, impAfter, 
                                      fullDone, badStep, badStepFail, 
                                      badNested, revAll, lost, cache, up, 
                                      reading, readSet, sinceRead, seen, nLoss, 
                                      nRestart, ten, phase, attempts, retries, 
                                      sAtt, snap, cur, tgts, curT, uq >>

FSub(self) == /\ pc[self] = "FSub"
              /\ lvl' = [lvl EXCEPT ![self] = 1]
              /\ open' = [open EXCEPT ![<<self, 1>>] = TRUE]
              /\ aborted' = [aborted EXCEPT ![<<self, 1>>] = ""]
              /\ wtok' = [wtok EXCEPT ![<<self, 1>>] = EmptyW]
              /\ waud' = [waud EXCEPT ![<<self, 1>>] = {}]
              /\ wst' = [wst EXCEPT ![<<self, 1>>] = {}]
              /\ winv' = [winv EXCEPT ![<<self, 1>>] = {}]
              /\ IF IsTF
                    THEN /\ want' = [want EXCEPT ![<<self, 1>>] = <<ten[self], "x">>]
                         /\ pc' = [pc EXCEPT ![self] = "FSubAdv"]
                    ELSE /\ pc' = [pc EXCEPT ![self] = "FSubSnap"]
                         /\ want' = want
              /\ UNCHANGED << op, tok, aud, starts, inval, sh, ex, nAborts, 
                              issued, finished, res, callerOf, rootOf, tenOf, 
                              exchJ, rotated, srcRevAtRead, stepTree, stepRev, 
                              stepRead, cascCommitted, cascDone, 
                              callerAtAccept, impChk, impAfter, fullDone, 
                              badStep, badStepFail, badNested, revAll, 
                              inflight, lost, cache, up, reading, readSet, 
                              sinceRead, seen, nLoss, nRestart, ten, phase, 
                              attempts, retries, sAtt, snap, cur, tgts, curT, 
                              uq >>

FSubAdv(self) == /\ pc[self] = "FSubAdv"
                 /\ CanLock(T(self), ten[self], "x") \/ Ab(self)
                 /\ IF Ab(self)
                       THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                            /\ UNCHANGED << sh, ex, want >>
                       ELSE /\ IF "x" = "s"
                                  THEN /\ sh' = [sh EXCEPT ![ten[self]] = sh[ten[self]] \cup {(T(self))}]
                                       /\ ex' = ex
                                  ELSE /\ ex' = [ex EXCEPT ![ten[self]] = T(self)]
                                       /\ sh' = sh
                            /\ want' = [want EXCEPT ![T(self)] = NoWant]
                            /\ pc' = [pc EXCEPT ![self] = "FSubSnap"]
                 /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                 wtok, waud, wst, winv, lvl, nAborts, issued, 
                                 finished, res, callerOf, rootOf, tenOf, exchJ, 
                                 rotated, srcRevAtRead, stepTree, stepRev, 
                                 stepRead, cascCommitted, cascDone, 
                                 callerAtAccept, impChk, impAfter, fullDone, 
                                 badStep, badStepFail, badNested, revAll, 
                                 inflight, lost, cache, up, reading, readSet, 
                                 sinceRead, seen, nLoss, nRestart, ten, phase, 
                                 attempts, retries, sAtt, snap, cur, tgts, 
                                 curT, uq >>

FSubSnap(self) == /\ pc[self] = "FSubSnap"
                  /\ IF Ab(self)
                        THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                             /\ snap' = snap
                        ELSE /\ snap' = [snap EXCEPT ![self] = TreeOf(View(T(self)), Seeds(View(T(self)), op[self].user, ten[self]), ten[self]) \cap LiveIn(View(T(self)))]
                             /\ pc' = [pc EXCEPT ![self] = "FSubRow"]
                  /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                  wtok, waud, wst, winv, lvl, sh, ex, want, 
                                  nAborts, issued, finished, res, callerOf, 
                                  rootOf, tenOf, exchJ, rotated, srcRevAtRead, 
                                  stepTree, stepRev, stepRead, cascCommitted, 
                                  cascDone, callerAtAccept, impChk, impAfter, 
                                  fullDone, badStep, badStepFail, badNested, 
                                  revAll, inflight, lost, cache, up, reading, 
                                  readSet, sinceRead, seen, nLoss, nRestart, 
                                  ten, phase, attempts, retries, sAtt, cur, 
                                  tgts, curT, uq >>

FSubRow(self) == /\ pc[self] = "FSubRow"
                 /\ IF Ab(self)
                       THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                            /\ UNCHANGED << want, cur >>
                       ELSE /\ IF snap[self] = {}
                                  THEN /\ pc' = [pc EXCEPT ![self] = "FSubCommit"]
                                       /\ UNCHANGED << want, cur >>
                                  ELSE /\ \E j \in snap[self]:
                                            /\ cur' = [cur EXCEPT ![self] = j]
                                            /\ want' = [want EXCEPT ![T(self)] = <<j, "x">>]
                                       /\ pc' = [pc EXCEPT ![self] = "FSubLock"]
                 /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                 wtok, waud, wst, winv, lvl, sh, ex, nAborts, 
                                 issued, finished, res, callerOf, rootOf, 
                                 tenOf, exchJ, rotated, srcRevAtRead, stepTree, 
                                 stepRev, stepRead, cascCommitted, cascDone, 
                                 callerAtAccept, impChk, impAfter, fullDone, 
                                 badStep, badStepFail, badNested, revAll, 
                                 inflight, lost, cache, up, reading, readSet, 
                                 sinceRead, seen, nLoss, nRestart, ten, phase, 
                                 attempts, retries, sAtt, snap, tgts, curT, uq >>

FSubLock(self) == /\ pc[self] = "FSubLock"
                  /\ CanLock(T(self), cur[self], "x") \/ Ab(self)
                  /\ IF Ab(self)
                        THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                             /\ UNCHANGED << wtok, sh, ex, want, snap >>
                        ELSE /\ IF "x" = "s"
                                   THEN /\ sh' = [sh EXCEPT ![cur[self]] = sh[cur[self]] \cup {(T(self))}]
                                        /\ ex' = ex
                                   ELSE /\ ex' = [ex EXCEPT ![cur[self]] = T(self)]
                                        /\ sh' = sh
                             /\ want' = [want EXCEPT ![T(self)] = NoWant]
                             /\ IF ~V(T(self), cur[self]).rev
                                   THEN /\ wtok' = [wtok EXCEPT ![T(self)] = (cur[self] :> RevRow(V(T(self), cur[self]), self)) @@ wtok[T(self)]]
                                   ELSE /\ TRUE
                                        /\ wtok' = wtok
                             /\ snap' = [snap EXCEPT ![self] = snap[self] \ {cur[self]}]
                             /\ pc' = [pc EXCEPT ![self] = "FSubRow"]
                  /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                  waud, wst, winv, lvl, nAborts, issued, 
                                  finished, res, callerOf, rootOf, tenOf, 
                                  exchJ, rotated, srcRevAtRead, stepTree, 
                                  stepRev, stepRead, cascCommitted, cascDone, 
                                  callerAtAccept, impChk, impAfter, fullDone, 
                                  badStep, badStepFail, badNested, revAll, 
                                  inflight, lost, cache, up, reading, readSet, 
                                  sinceRead, seen, nLoss, nRestart, ten, phase, 
                                  attempts, retries, sAtt, cur, tgts, curT, uq >>

FSubCommit(self) == /\ pc[self] = "FSubCommit"
                    /\ IF Ab(self)
                          THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                               /\ UNCHANGED << tok, aud, starts, inval, open, 
                                               wtok, sh, ex, inflight >>
                          ELSE /\ inflight' = (inflight \cup (Reps \X NewRevs((T(self)))))
                               /\ tok' = Apply((T(self)))
                               /\ aud' = (aud \cup waud[(T(self))])
                               /\ starts' = (starts \cup wst[(T(self))])
                               /\ inval' = (inval \cup winv[(T(self))])
                               /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                               /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                               /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                               /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                               /\ pc' = [pc EXCEPT ![self] = "FAct"]
                    /\ UNCHANGED << op, aborted, waud, wst, winv, lvl, want, 
                                    nAborts, issued, finished, res, callerOf, 
                                    rootOf, tenOf, exchJ, rotated, 
                                    srcRevAtRead, stepTree, stepRev, stepRead, 
                                    cascCommitted, cascDone, callerAtAccept, 
                                    impChk, impAfter, fullDone, badStep, 
                                    badStepFail, badNested, revAll, lost, 
                                    cache, up, reading, readSet, sinceRead, 
                                    seen, nLoss, nRestart, ten, phase, 
                                    attempts, retries, sAtt, snap, cur, tgts, 
                                    curT, uq >>

FAct(self) == /\ pc[self] = "FAct"
              /\ lvl' = [lvl EXCEPT ![self] = 1]
              /\ open' = [open EXCEPT ![<<self, 1>>] = TRUE]
              /\ aborted' = [aborted EXCEPT ![<<self, 1>>] = ""]
              /\ wtok' = [wtok EXCEPT ![<<self, 1>>] = EmptyW]
              /\ waud' = [waud EXCEPT ![<<self, 1>>] = {}]
              /\ wst' = [wst EXCEPT ![<<self, 1>>] = {}]
              /\ winv' = [winv EXCEPT ![<<self, 1>>] = {}]
              /\ snap' = [snap EXCEPT ![self] = {j \in Jtis : IsImp(tok[j]) /\ ~tok[j].rev /\ tok[j].act = op[self].user}]
              /\ pc' = [pc EXCEPT ![self] = "FActRow"]
              /\ UNCHANGED << op, tok, aud, starts, inval, sh, ex, want, 
                              nAborts, issued, finished, res, callerOf, rootOf, 
                              tenOf, exchJ, rotated, srcRevAtRead, stepTree, 
                              stepRev, stepRead, cascCommitted, cascDone, 
                              callerAtAccept, impChk, impAfter, fullDone, 
                              badStep, badStepFail, badNested, revAll, 
                              inflight, lost, cache, up, reading, readSet, 
                              sinceRead, seen, nLoss, nRestart, ten, phase, 
                              attempts, retries, sAtt, cur, tgts, curT, uq >>

FActRow(self) == /\ pc[self] = "FActRow"
                 /\ IF Ab(self)
                       THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                            /\ UNCHANGED << want, cur >>
                       ELSE /\ IF snap[self] = {}
                                  THEN /\ pc' = [pc EXCEPT ![self] = "FActCommit"]
                                       /\ UNCHANGED << want, cur >>
                                  ELSE /\ \E j \in snap[self]:
                                            /\ cur' = [cur EXCEPT ![self] = j]
                                            /\ want' = [want EXCEPT ![T(self)] = <<j, "x">>]
                                       /\ pc' = [pc EXCEPT ![self] = "FActLock"]
                 /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                 wtok, waud, wst, winv, lvl, sh, ex, nAborts, 
                                 issued, finished, res, callerOf, rootOf, 
                                 tenOf, exchJ, rotated, srcRevAtRead, stepTree, 
                                 stepRev, stepRead, cascCommitted, cascDone, 
                                 callerAtAccept, impChk, impAfter, fullDone, 
                                 badStep, badStepFail, badNested, revAll, 
                                 inflight, lost, cache, up, reading, readSet, 
                                 sinceRead, seen, nLoss, nRestart, ten, phase, 
                                 attempts, retries, sAtt, snap, tgts, curT, uq >>

FActLock(self) == /\ pc[self] = "FActLock"
                  /\ CanLock(T(self), cur[self], "x") \/ Ab(self)
                  /\ IF Ab(self)
                        THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                             /\ UNCHANGED << wtok, sh, ex, want, snap >>
                        ELSE /\ IF "x" = "s"
                                   THEN /\ sh' = [sh EXCEPT ![cur[self]] = sh[cur[self]] \cup {(T(self))}]
                                        /\ ex' = ex
                                   ELSE /\ ex' = [ex EXCEPT ![cur[self]] = T(self)]
                                        /\ sh' = sh
                             /\ want' = [want EXCEPT ![T(self)] = NoWant]
                             /\ IF ~V(T(self), cur[self]).rev
                                   THEN /\ wtok' = [wtok EXCEPT ![T(self)] = (cur[self] :> RevRow(V(T(self), cur[self]), self)) @@ wtok[T(self)]]
                                   ELSE /\ TRUE
                                        /\ wtok' = wtok
                             /\ snap' = [snap EXCEPT ![self] = snap[self] \ {cur[self]}]
                             /\ pc' = [pc EXCEPT ![self] = "FActRow"]
                  /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                                  waud, wst, winv, lvl, nAborts, issued, 
                                  finished, res, callerOf, rootOf, tenOf, 
                                  exchJ, rotated, srcRevAtRead, stepTree, 
                                  stepRev, stepRead, cascCommitted, cascDone, 
                                  callerAtAccept, impChk, impAfter, fullDone, 
                                  badStep, badStepFail, badNested, revAll, 
                                  inflight, lost, cache, up, reading, readSet, 
                                  sinceRead, seen, nLoss, nRestart, ten, phase, 
                                  attempts, retries, sAtt, cur, tgts, curT, uq >>

FActCommit(self) == /\ pc[self] = "FActCommit"
                    /\ IF Ab(self)
                          THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                               /\ UNCHANGED << tok, aud, starts, inval, open, 
                                               wtok, sh, ex, finished, res, 
                                               fullDone, inflight >>
                          ELSE /\ inflight' = (inflight \cup (Reps \X NewRevs((T(self)))))
                               /\ tok' = Apply((T(self)))
                               /\ aud' = (aud \cup waud[(T(self))])
                               /\ starts' = (starts \cup wst[(T(self))])
                               /\ inval' = (inval \cup winv[(T(self))])
                               /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                               /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                               /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                               /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                               /\ fullDone' = (fullDone \cup {op[self].user})
                               /\ res' = [res EXCEPT ![self] = "done"]
                               /\ finished' = [finished EXCEPT ![self] = TRUE]
                               /\ pc' = [pc EXCEPT ![self] = "Done"]
                    /\ UNCHANGED << op, aborted, waud, wst, winv, lvl, want, 
                                    nAborts, issued, callerOf, rootOf, tenOf, 
                                    exchJ, rotated, srcRevAtRead, stepTree, 
                                    stepRev, stepRead, cascCommitted, cascDone, 
                                    callerAtAccept, impChk, impAfter, badStep, 
                                    badStepFail, badNested, revAll, lost, 
                                    cache, up, reading, readSet, sinceRead, 
                                    seen, nLoss, nRestart, ten, phase, 
                                    attempts, retries, sAtt, snap, cur, tgts, 
                                    curT, uq >>

EBegin(self) == /\ pc[self] = "EBegin"
                /\ phase' = [phase EXCEPT ![self] = "end"]
                /\ lvl' = [lvl EXCEPT ![self] = 1]
                /\ open' = [open EXCEPT ![<<self, 1>>] = TRUE]
                /\ aborted' = [aborted EXCEPT ![<<self, 1>>] = ""]
                /\ wtok' = [wtok EXCEPT ![<<self, 1>>] = EmptyW]
                /\ waud' = [waud EXCEPT ![<<self, 1>>] = {}]
                /\ wst' = [wst EXCEPT ![<<self, 1>>] = {}]
                /\ winv' = [winv EXCEPT ![<<self, 1>>] = {}]
                /\ want' = [want EXCEPT ![<<self, 1>>] = <<op[self].root, "x">>]
                /\ pc' = [pc EXCEPT ![self] = "ELock"]
                /\ UNCHANGED << op, tok, aud, starts, inval, sh, ex, nAborts, 
                                issued, finished, res, callerOf, rootOf, tenOf, 
                                exchJ, rotated, srcRevAtRead, stepTree, 
                                stepRev, stepRead, cascCommitted, cascDone, 
                                callerAtAccept, impChk, impAfter, fullDone, 
                                badStep, badStepFail, badNested, revAll, 
                                inflight, lost, cache, up, reading, readSet, 
                                sinceRead, seen, nLoss, nRestart, ten, 
                                attempts, retries, sAtt, snap, cur, tgts, curT, 
                                uq >>

ELock(self) == /\ pc[self] = "ELock"
               /\ CanLock(T(self), op[self].root, "x") \/ Ab(self)
               /\ IF Ab(self)
                     THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                          /\ UNCHANGED << wtok, sh, ex, want >>
                     ELSE /\ IF "x" = "s"
                                THEN /\ sh' = [sh EXCEPT ![(op[self].root)] = sh[(op[self].root)] \cup {(T(self))}]
                                     /\ ex' = ex
                                ELSE /\ ex' = [ex EXCEPT ![(op[self].root)] = T(self)]
                                     /\ sh' = sh
                          /\ want' = [want EXCEPT ![T(self)] = NoWant]
                          /\ IF IsImp(V(T(self), op[self].root)) /\ ~V(T(self), op[self].root).rev
                                THEN /\ wtok' = [wtok EXCEPT ![T(self)] = (op[self].root :> RevRow(V(T(self), op[self].root), self)) @@ wtok[T(self)]]
                                ELSE /\ TRUE
                                     /\ wtok' = wtok
                          /\ pc' = [pc EXCEPT ![self] = "ECommit"]
               /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                               waud, wst, winv, lvl, nAborts, issued, finished, 
                               res, callerOf, rootOf, tenOf, exchJ, rotated, 
                               srcRevAtRead, stepTree, stepRev, stepRead, 
                               cascCommitted, cascDone, callerAtAccept, impChk, 
                               impAfter, fullDone, badStep, badStepFail, 
                               badNested, revAll, inflight, lost, cache, up, 
                               reading, readSet, sinceRead, seen, nLoss, 
                               nRestart, ten, phase, attempts, retries, sAtt, 
                               snap, cur, tgts, curT, uq >>

ECommit(self) == /\ pc[self] = "ECommit"
                 /\ IF Ab(self)
                       THEN /\ pc' = [pc EXCEPT ![self] = "Fail"]
                            /\ UNCHANGED << tok, aud, starts, inval, open, 
                                            wtok, sh, ex, finished, res, 
                                            inflight >>
                       ELSE /\ inflight' = (inflight \cup (Reps \X NewRevs((T(self)))))
                            /\ tok' = Apply((T(self)))
                            /\ aud' = (aud \cup waud[(T(self))])
                            /\ starts' = (starts \cup wst[(T(self))])
                            /\ inval' = (inval \cup winv[(T(self))])
                            /\ sh' = [r \in Res |-> sh[r] \ {(T(self))}]
                            /\ ex' = [r \in Res |-> IF ex[r] = (T(self)) THEN NoTx ELSE ex[r]]
                            /\ open' = [open EXCEPT ![(T(self))] = FALSE]
                            /\ wtok' = [wtok EXCEPT ![(T(self))] = EmptyW]
                            /\ res' = [res EXCEPT ![self] = "done"]
                            /\ finished' = [finished EXCEPT ![self] = TRUE]
                            /\ pc' = [pc EXCEPT ![self] = "Done"]
                 /\ UNCHANGED << op, aborted, waud, wst, winv, lvl, want, 
                                 nAborts, issued, callerOf, rootOf, tenOf, 
                                 exchJ, rotated, srcRevAtRead, stepTree, 
                                 stepRev, stepRead, cascCommitted, cascDone, 
                                 callerAtAccept, impChk, impAfter, fullDone, 
                                 badStep, badStepFail, badNested, revAll, lost, 
                                 cache, up, reading, readSet, sinceRead, seen, 
                                 nLoss, nRestart, ten, phase, attempts, 
                                 retries, sAtt, snap, cur, tgts, curT, uq >>

Fail(self) == /\ pc[self] = "Fail"
              /\ IF open[<<self, 1>>]
                    THEN /\ sh' = [r \in Res |-> sh[r] \ {(<<self, 1>>)}]
                         /\ ex' = [r \in Res |-> IF ex[r] = (<<self, 1>>) THEN NoTx ELSE ex[r]]
                         /\ open' = [open EXCEPT ![(<<self, 1>>)] = FALSE]
                         /\ wtok' = [wtok EXCEPT ![(<<self, 1>>)] = EmptyW]
                         /\ waud' = [waud EXCEPT ![(<<self, 1>>)] = {}]
                         /\ wst' = [wst EXCEPT ![(<<self, 1>>)] = {}]
                         /\ winv' = [winv EXCEPT ![(<<self, 1>>)] = {}]
                    ELSE /\ TRUE
                         /\ UNCHANGED << open, wtok, waud, wst, winv, sh, ex >>
              /\ want' = [want EXCEPT ![<<self, 1>>] = NoWant]
              /\ lvl' = [lvl EXCEPT ![self] = 1]
              /\ IF phase[self] = "step" /\ \E j \in Jtis : tok[j].ex /\ tok[j].ten = ten[self] /\ tok[j].by = self
                    THEN /\ badStepFail' = TRUE
                    ELSE /\ TRUE
                         /\ UNCHANGED badStepFail
              /\ pc' = [pc EXCEPT ![self] = "Fail2"]
              /\ UNCHANGED << op, tok, aud, starts, inval, aborted, nAborts, 
                              issued, finished, res, callerOf, rootOf, tenOf, 
                              exchJ, rotated, srcRevAtRead, stepTree, stepRev, 
                              stepRead, cascCommitted, cascDone, 
                              callerAtAccept, impChk, impAfter, fullDone, 
                              badStep, badNested, revAll, inflight, lost, 
                              cache, up, reading, readSet, sinceRead, seen, 
                              nLoss, nRestart, ten, phase, attempts, retries, 
                              sAtt, snap, cur, tgts, curT, uq >>

Fail2(self) == /\ pc[self] = "Fail2"
               /\ IF op[self].kind = "rev" /\ phase[self] = "casc" /\ aborted[<<self, 1>>] = "deadlock" /\ attempts[self] < CascadeAttempts
                     THEN /\ pc' = [pc EXCEPT ![self] = "Casc"]
                          /\ UNCHANGED << finished, res, retries, sAtt >>
                     ELSE /\ IF IsTF /\ op[self].kind = "rev" /\ phase[self] = "step" /\ aborted[<<self, 1>>] = "deadlock" /\ sAtt[self] < CascadeAttempts
                                THEN /\ pc' = [pc EXCEPT ![self] = "TOuter"]
                                     /\ UNCHANGED << finished, res, retries, 
                                                     sAtt >>
                                ELSE /\ IF op[self].kind = "rev" /\ phase[self] = "step" /\ retries[self] < MaxClientRetries
                                           THEN /\ retries' = [retries EXCEPT ![self] = retries[self] + 1]
                                                /\ sAtt' = [sAtt EXCEPT ![self] = 0]
                                                /\ IF IsTF
                                                      THEN /\ pc' = [pc EXCEPT ![self] = "TOuter"]
                                                      ELSE /\ pc' = [pc EXCEPT ![self] = "MSub"]
                                                /\ UNCHANGED << finished, res >>
                                           ELSE /\ res' = [res EXCEPT ![self] = "error"]
                                                /\ finished' = [finished EXCEPT ![self] = TRUE]
                                                /\ pc' = [pc EXCEPT ![self] = "Done"]
                                                /\ UNCHANGED << retries, sAtt >>
               /\ UNCHANGED << op, tok, aud, starts, inval, open, aborted, 
                               wtok, waud, wst, winv, lvl, sh, ex, want, 
                               nAborts, issued, callerOf, rootOf, tenOf, exchJ, 
                               rotated, srcRevAtRead, stepTree, stepRev, 
                               stepRead, cascCommitted, cascDone, 
                               callerAtAccept, impChk, impAfter, fullDone, 
                               badStep, badStepFail, badNested, revAll, 
                               inflight, lost, cache, up, reading, readSet, 
                               sinceRead, seen, nLoss, nRestart, ten, phase, 
                               attempts, snap, cur, tgts, curT, uq >>

Quit(self) == /\ pc[self] = "Quit"
              /\ IF open[<<self, 1>>]
                    THEN /\ sh' = [r \in Res |-> sh[r] \ {(<<self, 1>>)}]
                         /\ ex' = [r \in Res |-> IF ex[r] = (<<self, 1>>) THEN NoTx ELSE ex[r]]
                         /\ open' = [open EXCEPT ![(<<self, 1>>)] = FALSE]
                         /\ wtok' = [wtok EXCEPT ![(<<self, 1>>)] = EmptyW]
                         /\ waud' = [waud EXCEPT ![(<<self, 1>>)] = {}]
                         /\ wst' = [wst EXCEPT ![(<<self, 1>>)] = {}]
                         /\ winv' = [winv EXCEPT ![(<<self, 1>>)] = {}]
                    ELSE /\ TRUE
                         /\ UNCHANGED << open, wtok, waud, wst, winv, sh, ex >>
              /\ lvl' = [lvl EXCEPT ![self] = 1]
              /\ finished' = [finished EXCEPT ![self] = TRUE]
              /\ pc' = [pc EXCEPT ![self] = "Done"]
              /\ UNCHANGED << op, tok, aud, starts, inval, aborted, want, 
                              nAborts, issued, res, callerOf, rootOf, tenOf, 
                              exchJ, rotated, srcRevAtRead, stepTree, stepRev, 
                              stepRead, cascCommitted, cascDone, 
                              callerAtAccept, impChk, impAfter, fullDone, 
                              badStep, badStepFail, badNested, revAll, 
                              inflight, lost, cache, up, reading, readSet, 
                              sinceRead, seen, nLoss, nRestart, ten, phase, 
                              attempts, retries, sAtt, snap, cur, tgts, curT, 
                              uq >>

req(self) == Start(self) \/ XBegin(self) \/ XAdv(self) \/ X1a(self)
                \/ XUserLock(self) \/ XInsert(self) \/ XRotLock(self)
                \/ XCommit(self) \/ MSub(self) \/ MSubCaller(self)
                \/ MSubRead(self) \/ MImp(self) \/ MImpRow(self)
                \/ MImpLock(self) \/ MImpCommit(self) \/ TOuter(self)
                \/ TOuterCaller(self) \/ TOuterUser(self)
                \/ TOuterTree(self) \/ TOuterStarts(self)
                \/ TOuterSnap(self) \/ TOuterRow(self) \/ TOuterLock(self)
                \/ TNext(self) \/ TTarget(self) \/ TTargetRow(self)
                \/ TTargetLock(self) \/ TOuterCommit(self) \/ Casc(self)
                \/ CascAdv(self) \/ CascCaller(self) \/ CascUser(self)
                \/ CascSnap(self) \/ CascRow(self) \/ CascLock(self)
                \/ CascCommit(self) \/ IChk(self) \/ IOuter(self)
                \/ IOuterCb(self) \/ IOuterUser(self) \/ INested(self)
                \/ INestedUser(self) \/ INestedCommit(self)
                \/ IOuterCommit(self) \/ FLevel(self) \/ FLevelLock(self)
                \/ FLevelCommit(self) \/ FSub(self) \/ FSubAdv(self)
                \/ FSubSnap(self) \/ FSubRow(self) \/ FSubLock(self)
                \/ FSubCommit(self) \/ FAct(self) \/ FActRow(self)
                \/ FActLock(self) \/ FActCommit(self) \/ EBegin(self)
                \/ ELock(self) \/ ECommit(self) \/ Fail(self)
                \/ Fail2(self) \/ Quit(self)

(* Allow infinite stuttering to prevent deadlock on termination. *)
Terminating == /\ \A self \in ProcSet: pc[self] = "Done"
               /\ UNCHANGED vars

Next == det
           \/ (\E self \in (IF Reps = {} THEN {} ELSE {101}): bus(self))
           \/ (\E self \in Procs: req(self))
           \/ Terminating

Spec == /\ Init /\ [][Next]_vars
        /\ WF_vars(det)
        /\ \A self \in (IF Reps = {} THEN {} ELSE {101}) : WF_vars(bus(self))
        /\ \A self \in Procs : WF_vars(req(self))

Termination == <>(\A self \in ProcSet: pc[self] = "Done")

\* END TRANSLATION

(***************************************************************************)
(* Properties.  README.md states each one in words and names its rule.     *)
(***************************************************************************)
TypeOK == /\ nAborts \in 0..MaxAborts
          /\ \A r \in Res : ex[r] \in TxIds \cup {NoTx}

\* C1 [CascadeComplete]: once a cascade commits, every committed row of its
\* root's subtree is revoked, then and in every later state.
CascadeComplete ==
  \A c \in cascDone : \A j \in TreeOf(tok, {c.root}, c.ten) : tok[j].ex => tok[j].rev

\* C2 [NoStaleSource, RotateOnce].
NoStaleSource == \A p \in Procs : res[p] = "issued" => srcRevAtRead[p] = {}
RotateOnce == \A j \in Jtis : Cardinality({e \in rotated : e[2] = j}) <= 1

\* C3 [IssuedOnlyAfterCommit, RowIffAudit, AdvisoryLockExclusive].
IssuedOnlyAfterCommit == \A j \in issued : tok[j].ex
RowIffAudit == \A j \in exchJ : tok[j].ex <=> j \in aud
AdvisoryLockExclusive ==
  \A g \in Tenants : \A t, u \in TxIds :
    (open[t] /\ open[u] /\ ex[g] = t /\ ex[g] = u) => t = u

\* C4 [CacheOnlyCommitted, AcceptRevokedOnlyInFlight, RevocationReachesEveryReplica].
CacheOnlyCommitted == \A r \in Reps : cache[r] \subseteq CommittedRevoked
AcceptRevokedOnlyInFlight ==
  \A r \in Reps : \A j \in CommittedRevoked :
    (up[r] /\ j \notin cache[r]) => (<<r, j>> \in inflight \cup lost /\ j \notin seen[r])
RevocationReachesEveryReplica ==
  \A j \in Jtis : (tok[j].ex /\ tok[j].rev) ~> (\A r \in Reps : j \in cache[r])

\* C5 [AcceptedCallerLive, MutualRevokeOneWins, RequestsTerminate].
AcceptedCallerLive == \A p \in Procs : callerAtAccept[p] /= "other"
MutualRevokeOneWins ==
  ~\E p, q \in Procs :
     /\ p /= q /\ callerOf[p] /= "" /\ callerOf[q] /= ""
     /\ tok[callerOf[q]].rev /\ tok[callerOf[q]].by = p
     /\ tok[callerOf[p]].rev /\ tok[callerOf[p]].by = q
RequestsTerminate == <>(\A p \in Procs : finished[p])

\* C6 [ImpersonationEndedOrInWindow].
\* The accepted window of rule 7: the impersonation's caller check ran before
\* the cascade committed, and its row (Main) or its start record (TF)
\* committed after the step's read.  Under TF the caller check is the share
\* read of TF-7, and the start record also committed before the cascade did.
InWindow(j, r) ==
  \E q \in Procs :
    /\ NewJ(q) = j /\ r \in impChk[q] /\ r \in impAfter[q]
ImpersonationEndedOrInWindow ==
  \A c \in cascDone : \A j \in Jtis :
    (IsImp(tok[j]) /\ ~tok[j].rev /\ tok[j].iten = c.ten /\ tok[j].ijti \in c.tree)
      => InWindow(j, c.p)
\* The same, leaving out a bearer row whose impersonation start ended in an
\* error after its nested transaction committed, so no client holds it.  The
\* start's first transaction has aborted, or the start has ended in an error.
FailedStart(j) ==
  \E q \in Procs : NewJ(q) = j /\ op[q].kind = "imp"
                   /\ (res[q] = "error" \/ aborted[<<q, 1>>] /= "")
ImpersonationEndedOrInWindowExceptFailedStart ==
  \A c \in cascDone : \A j \in Jtis :
    (IsImp(tok[j]) /\ ~tok[j].rev /\ tok[j].iten = c.ten /\ tok[j].ijti \in c.tree /\ ~FailedStart(j))
      => InWindow(j, c.p)

\* C7 [FullRevokeComplete].
FullRevokeComplete ==
  \A u \in fullDone : \A j \in Jtis :
    (tok[j].ex /\ ~tok[j].rev)
      => /\ ~(tok[j].ten = UTen[u] /\ (tok[j].sub = u \/ tok[j].act = u))
         /\ j \notin TreeOf(tok, {k \in Jtis : tok[k].ex /\ tok[k].ten = UTen[u] /\ tok[k].sub = u}, UTen[u])
         /\ ~(IsImp(tok[j]) /\ tok[j].act = u)

\* C8 [NoUndetectedWait, NoSelfWait].
NoUndetectedWait ==
  AppEdges /= {} => ~\E e \in AppEdges : <<e[2], e[1]>> \in TC(LockEdges \cup AppEdges)
NoSelfWait ==
  ~\E p \in Procs : open[<<p, 1>>] /\ open[<<p, 2>>]
                    /\ (Blocks(<<p, 2>>, <<p, 1>>) \/ Blocks(<<p, 1>>, <<p, 2>>))

\* C9 [CallerAcceptedOnlyFromOwnStep, NoSessionEndedByRevokedCaller,
\*     F1RootRevoked, RetryCompletesStep].
CallerAcceptedOnlyFromOwnStep ==
  \A p \in Procs : callerAtAccept[p] = "own" =>
    (callerOf[p] \in stepRev[p] \/ tok[callerOf[p]].by = p)
NoSessionEndedByRevokedCaller == ~badStep
F1RootRevoked ==
  \A p \in Procs :
    (finished[p] /\ res[p] \notin {"error", "no_token"} /\ callerOf[p] \in stepRev[p])
      => tok[rootOf[p]].rev
RetryCompletesStep == ~badStepFail

\* C10 [StepWithoutAll, NestedWritesOnlyNamedRows], for TF.  At revision 4
\* the second names the step's updates under a target tenant's context.
StepWithoutAll == IsTF => ~revAll
NestedWritesOnlyNamedRows == ~badNested
=============================================================================
