-------------------------- MODULE SessionLifecycle --------------------------
(***************************************************************************)
(* A model of the session state machine of change 0146 (session           *)
(* lifecycle), as revision 24 states it with the rules that decision 1 of *)
(* change 0175 moved into it from change 0166 (delegation primitives).    *)
(* Change 0175 builds it; README.md maps each action to the text and the  *)
(* code and states each property in plain words.                          *)
(*                                                                         *)
(* One step is one guarded write of the gateway: a row write with its     *)
(* compare-and-set, or a call that the text says returns after its row    *)
(* writes.  The sessions form a tree through the parent link, which      *)
(* session.transfer and session.release change.  Time is not modelled:    *)
(* every timer is an event that may fire while its rule allows it.        *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets, Sequences, TLC

CONSTANTS
  Shape,          \* the planned tree: "chain2", "chain3", "fan3", or "chain4"
  None,           \* a model value: no session
  Causes,         \* suspension causes in play: a subset of {"client", "external_limit"}
  Cap,            \* the tenant concurrency cap over the counted states (SL-10)
  MaxPar,         \* maxParallelChildren of every grant
  MaxSuspend,     \* suspension calls and external-limit signals per behavior
  MaxPodLoss,     \* pod losses per behavior
  MaxLeave,       \* transfers and releases per behavior (maxLeaves)
  MaxKill,        \* force-terminates, tree terminates, and non-retryable failures per behavior
  MaxCrash,       \* suspensions that a coordinator crash cuts after step 1 of "SL-4"
  AllowHandoff,   \* whether a root may release a child with handoff: true
  WakeOnExpired,  \* whether the parents' wakeOn holds the kind expired
  AgeStopsInACA,  \* the reading of SLR-21 against perChildMaxAge (open decision O4 of 0175)
  RootOpes,       \* the root's onParentEnd values in play: a nonempty subset of {"close_all", "let_finish"}
  WithExpiry,     \* whether the expiry timers of SL-10 fire (Expire); FALSE narrows a configuration to other concerns
  TrackActor      \* whether the state records the actor of the last step, which OnlyRootClientLifts reads

(***************************************************************************)
(* The planned tree.  r is the root that the root's client creates; every  *)
(* other session is created by the runtime of its planned parent.          *)
(*   chain2: r -> a   chain3: r -> a -> b   fan3: r -> a, r -> b           *)
(*   chain4: r -> a -> b -> c                                              *)
(***************************************************************************)
S == CASE Shape = "chain4" -> {"r", "a", "b", "c"}
     []   Shape = "chain2" -> {"r", "a"}
     []   OTHER            -> {"r", "a", "b"}
Root0 == "r"
PlanParent ==
  CASE Shape = "chain2" -> ("a" :> "r")
  []   Shape = "chain3" -> ("a" :> "r") @@ ("b" :> "a")
  []   Shape = "fan3"   -> ("a" :> "r") @@ ("b" :> "r")
  []   Shape = "chain4" -> ("a" :> "r") @@ ("b" :> "a") @@ ("c" :> "b")

N == Cardinality(S)

(***************************************************************************)
(* States.  "absent" is a session that its parent has not created yet.    *)
(* "starting" stands for the setup states created to starting.            *)
(***************************************************************************)
Live       == {"running", "idle", "dormant"}
Paused     == {"suspended", "blocked"}
Recovering == {"resume_pending", "resuming"}
ACA        == "awaiting_client_action"
Terminal   == {"completed", "cancelled", "failed", "expired"}
States     == {"absent", "starting"} \cup Live \cup Paused \cup Recovering \cup {ACA} \cup Terminal
\* The states the concurrency caps count (SLR-12).
Counted    == {"starting", "running", "idle"} \cup Recovering \cup {ACA}

Term(x)    == x \in Terminal
Exists(x)  == x # "absent"
NonTerm(x) == Exists(x) /\ ~Term(x)

(***************************************************************************)
(* A session row.  st: the state.  par: the parent link that the cascade,  *)
(* the tree suspension, and SLR-9 follow; a transferred session's link is *)
(* its root.  cip: the parent is the session's client (FALSE after a       *)
(* leave).  child: perChildMaxAge applies (FALSE for a root and after a    *)
(* release).  ope: onParentEnd.  cause: suspension_cause.  pa:             *)
(* paused_ancestor_id.  sreq: suspension_requested.  es: ended_suspended.  *)
(* pod: holds a pod.  lte: last_turn_end.  nt: resume_notice_due.          *)
(* forced: ended by a force-terminate or a tree terminate, or closed by    *)
(* the cascade of one.  ev: the inbox: "wake" holds a message that opens a *)
(* turn or wakes the session, "nowake" one outside wakeOn.  direct: the    *)
(* root's client lifted this session's own pause and nothing paused it     *)
(* since.  ld: a dormant session that an external-limit suspension left    *)
(* dormant (L42).  succ: successorSessionId.                               *)
(***************************************************************************)
AbsentRow(s) ==
  [st |-> "absent", par |-> None, cip |-> FALSE, child |-> FALSE, ope |-> "close_all",
   cause |-> "none", pa |-> None, sreq |-> "none", es |-> FALSE, pod |-> FALSE,
   lte |-> "none", nt |-> "none", forced |-> FALSE, ev |-> {}, direct |-> FALSE,
   ld |-> FALSE, succ |-> None]

\* by: who took the last step: "rootclient", "parent", "runtime", "gateway", "operator", or
\* "tenantadmin", or "untracked" when TrackActor is FALSE.
VARIABLES ss, n, by
vars == <<ss, n, by>>

\* The actor of a step, recorded only when TrackActor is set: OnlyRootClientLifts reads it, and
\* the liveness configurations, which do not list that property, leave it out of the state.
Actor(x) == IF TrackActor THEN x ELSE "untracked"

(***************************************************************************)
(* The tree, computed on any candidate value f of ss.                      *)
(***************************************************************************)
RECURSIVE AncIn(_, _, _)
\* The ancestors of s in f, nearest first.
AncIn(f, s, k) ==
  IF k = 0 \/ f[s].par = None THEN <<>> ELSE <<f[s].par>> \o AncIn(f, f[s].par, k - 1)

Range(q) == {q[i] : i \in 1..Len(q)}
IsAncIn(f, x, s) == x \in Range(AncIn(f, s, N))
TreeRootIn(f, s) == LET a == AncIn(f, s, N) IN IF a = <<>> THEN s ELSE a[Len(a)]

\* A session that pauses its descendants: "Nearest paused ancestor" in the terms of 0146.
MarkIn(f, x) == f[x].st \in Paused \/ f[x].sreq # "none" \/ f[x].es

FirstIn(f, s, P(_)) ==
  LET a == AncIn(f, s, N)
      I == {i \in 1..Len(a) : P(a[i])}
  IN IF I = {} THEN None ELSE a[CHOOSE i \in I : \A j \in I : i <= j]

NPAIn(f, s) == LET P(x) == MarkIn(f, x) IN FirstIn(f, s, P)

IsAnc(x, s) == IsAncIn(ss, x, s)
NPA(s)      == NPAIn(ss, s)
TreeRoot(s) == TreeRootIn(ss, s)
Desc(s)     == {d \in S : IsAnc(s, d)}

CountCounted(f) == Cardinality({x \in S : f[x].st \in Counted})
\* A parent's parallelism: its children in the counted states and in blocked (SL-10).
ParCount(f, p) ==
  Cardinality({c \in S : f[c].par = p /\ f[c].cip /\ (f[c].st \in Counted \/ f[c].st = "blocked")})

\* Resume admission and creation admission: room under the cap for one more counted session.
Admit == CountCounted(ss) < Cap

\* The close rule (SL-7, SLR-8).
CloseOutcome(r) == IF r.st # "running" /\ r.lte = "message" THEN "completed" ELSE "cancelled"

(***************************************************************************)
(* The terminal transition of s to state t (SL-9 "Order"): the inbox      *)
(* closes and is drained, the pod is released, a suspended session records *)
(* ended_suspended, the sessions blocked under a blocked session are       *)
(* re-pointed to its own paused ancestor (SLR-28), and, when notify is set, *)
(* the parent receives the ended child event of kind k.  The cascade to    *)
(* the children is the separate step Cascade.                             *)
(***************************************************************************)
FinishIn(f, s, t, notify, k) ==
  [x \in S |->
     \* A terminal row keeps only what later steps read: the state, the links, onParentEnd,
     \* ended_suspended, forced, and the successor.
     IF x = s THEN [f[s] EXCEPT !.st = t, !.pod = FALSE, !.es = (f[s].st = "suspended"),
                                !.ev = {}, !.direct = FALSE, !.ld = FALSE, !.cause = "none",
                                !.pa = None, !.sreq = "none", !.lte = "none", !.nt = "none"]
     ELSE IF f[s].st = "blocked" /\ f[x].st = "blocked" /\ f[x].pa = s
       THEN [f[x] EXCEPT !.pa = f[s].pa]
     ELSE IF notify /\ f[s].cip /\ x = f[s].par /\ ~Term(f[x].st)
       THEN [f[x] EXCEPT !.ev = @ \cup {k}]
     ELSE f[x]]

\* A child event of kind k to the parent of s, when the parent is its client and not terminal.
NotifyIn(f, s, k) ==
  IF f[s].cip /\ f[s].par # None /\ ~Term(f[f[s].par].st)
  THEN [f EXCEPT ![f[s].par].ev = @ \cup {k}]
  ELSE f

ExpiryKind == IF WakeOnExpired THEN "wake" ELSE "nowake"

(***************************************************************************)
(* The tree suspension of SL-4 "Steps", in one step: the call returns once *)
(* the root's row and every descendant row are written.  s is the          *)
(* suspension root and c the cause.  A descendant in a live state is       *)
(* blocked, except that an external-limit suspension leaves a dormant      *)
(* descendant dormant (L42).  Each blocked descendant points to its        *)
(* nearest paused ancestor after the step, top-down.  A running session    *)
(* has its turn cut (SL-3).  A suspended descendant keeps its suspension   *)
(* and a setup, recovering, or awaiting_client_action descendant           *)
(* continues (steps 3 to 5).  The suspension root's own row is r.          *)
(* full = FALSE stops after step 1 (a coordinator crash, SLR-20).          *)
(***************************************************************************)
Cut(r) == IF r.st = "running" THEN [r EXCEPT !.lte = "cut", !.nt = "suspension"] ELSE r

SuspendIn(s, c, r, full) ==
  LET WillBlock(d) == full /\ IsAnc(s, d) /\
                      (IF c = "client" THEN ss[d].st \in Live ELSE ss[d].st \in {"running", "idle"})
      NewMark(x)   == x = s \/ (IsAnc(s, x) /\ (WillBlock(x) \/ MarkIn(ss, x)))
      PaOf(d)      == FirstIn(ss, d, NewMark)
  IN [x \in S |->
        IF x = s THEN r
        ELSE IF WillBlock(x)
          THEN [Cut(ss[x]) EXCEPT !.st = "blocked", !.pa = PaOf(x), !.direct = FALSE, !.ld = FALSE]
        ELSE IF full /\ c = "external_limit" /\ IsAnc(s, x) /\ ss[x].st = "dormant"
          THEN [ss[x] EXCEPT !.ld = TRUE]
        ELSE ss[x]]

(***************************************************************************)
(* Init: the root's client has created the root, which waits in idle.     *)
(***************************************************************************)
Init ==
  /\ \E o \in RootOpes :
       ss = [s \in S |-> IF s = Root0
                         THEN [AbsentRow(s) EXCEPT !.st = "idle", !.ope = o, !.pod = TRUE]
                         ELSE AbsentRow(s)]
  /\ n = [susp |-> 0, pod |-> 0, leave |-> 0, kill |-> 0, crash |-> 0]
  /\ by = Actor("rootclient")

(***************************************************************************)
(* The runtime of a session, and the turn.                                 *)
(***************************************************************************)
\* session.create by the runtime of the planned parent, during a turn (DP-1, SL-10).
Create(c) ==
  /\ c # Root0
  /\ ss[c].st = "absent"
  /\ LET p == PlanParent[c] IN
     /\ ss[p].st = "running"
     /\ ParCount(ss, p) < MaxPar
     /\ Admit
     /\ \E o \in {ss[p].ope, "close_all"} :
          ss' = [ss EXCEPT ![c] = [AbsentRow(c) EXCEPT !.st = "starting", !.par = p,
                                     !.cip = TRUE, !.child = TRUE, !.ope = o]]
  /\ UNCHANGED n

\* The setup commits: running with the task, or blocked under the nearest paused ancestor with the task held (SL-2, SLR-4).
StartCommit(c) ==
  /\ ss[c].st = "starting"
  /\ ss' = [ss EXCEPT ![c] =
              IF NPA(c) # None
              THEN [@ EXCEPT !.st = "blocked", !.pa = NPA(c), !.pod = TRUE, !.ev = {"wake"}]
              ELSE [@ EXCEPT !.st = "running", !.pod = TRUE]]
  /\ UNCHANGED n

\* End-of-turn message (SL-2), and the end_of_turn child event.  A message that carries
\* error is left out: every rule the model reads treats last_turn_end = error as it treats
\* cut (the close rule records cancelled for both), so cut stands for both.
EndTurn(s) ==
  /\ ss[s].st = "running"
  /\ ss' = NotifyIn([ss EXCEPT ![s].st = "idle", ![s].lte = "message"], s, "wake")
  /\ UNCHANGED n

\* End-of-turn message with complete: true (SL-2), and the ended child event.
Complete(s) ==
  /\ ss[s].st = "running"
  /\ ss' = FinishIn([ss EXCEPT ![s].lte = "message"], s, "completed", TRUE, "wake")
  /\ UNCHANGED n

\* The runtime writes output on its own (SL-2).
Output(s) ==
  /\ ss[s].st = "idle"
  /\ ss' = [ss EXCEPT ![s].st = "running", ![s].ev = {}]
  /\ UNCHANGED n

\* A delivery point (SLR-26): the resume notice first, then held messages, open a turn (SL-3).
Deliver(s) ==
  /\ ss[s].st = "idle"
  /\ "wake" \in ss[s].ev \/ ss[s].nt # "none"
  /\ ss' = [ss EXCEPT ![s].st = "running", ![s].ev = {}, ![s].nt = "none"]
  /\ UNCHANGED n

\* maxClientIdleSeconds (SL-6, SLR-9, SLR-17): idle with no running descendant, or running with the turn cut.
IdleClock(s) ==
  /\ \/ /\ ss[s].st = "idle"
        /\ \A d \in Desc(s) : ss[d].st # "running"
        /\ ss' = [ss EXCEPT ![s].st = "dormant", ![s].pod = FALSE]
     \/ /\ ss[s].st = "running"
        /\ ss' = NotifyIn([ss EXCEPT ![s].st = "dormant", ![s].pod = FALSE, ![s].lte = "cut"],
                         s, "wake")
  /\ UNCHANGED n

\* A message in wakeOn wakes a dormant session through resume admission (SL-5, SL-8).
WakeDormant(s) ==
  /\ ss[s].st = "dormant"
  /\ "wake" \in ss[s].ev
  /\ Admit
  /\ ss' = [ss EXCEPT ![s].st = "resume_pending", ![s].ld = FALSE, ![s].direct = FALSE]
  /\ UNCHANGED n

(***************************************************************************)
(* Pods and recovery.                                                      *)
(***************************************************************************)
\* A retryable failure (SL-2, SLR-16): a live pod-holding session goes to resume_pending; a paused one keeps its state.
PodLoss(s) ==
  /\ n.pod < MaxPodLoss
  /\ \/ /\ ss[s].st = "running"
        /\ ss' = [ss EXCEPT ![s].st = "resume_pending", ![s].pod = FALSE, ![s].lte = "cut",
                            ![s].nt = "pod_loss"]
     \/ /\ ss[s].st = "idle"
        /\ ss' = [ss EXCEPT ![s].st = "resume_pending", ![s].pod = FALSE]
     \/ /\ ss[s].st \in Paused /\ ss[s].pod
        /\ ss' = [ss EXCEPT ![s].pod = FALSE]
  /\ n' = [n EXCEPT !.pod = @ + 1]

\* The end of the pod hold, or a suspension that cannot take hold (SL-3, SLR-18).
PodRelease(s) ==
  /\ ss[s].st \in Paused /\ ss[s].pod
  /\ ss' = [ss EXCEPT ![s].pod = FALSE]
  /\ UNCHANGED n

RestoreStart(s) ==
  /\ ss[s].st = "resume_pending"
  /\ ss' = [ss EXCEPT ![s].st = "resuming"]
  /\ UNCHANGED n

\* The restore commits (SL-2): suspended with the requested cause, or blocked under the nearest paused ancestor, or idle.
\* A turn that a pod loss cut is reported to the parent as turn_lost once the restore commits in a live state (SL-3).
RestoreCommit(s) ==
  /\ ss[s].st = "resuming"
  /\ ss' = IF ss[s].sreq # "none"
           THEN [ss EXCEPT ![s].st = "suspended", ![s].cause = ss[s].sreq, ![s].sreq = "none",
                           ![s].pod = TRUE]
           ELSE IF NPA(s) # None
           THEN [ss EXCEPT ![s].st = "blocked", ![s].pa = NPA(s), ![s].pod = TRUE]
           ELSE IF ss[s].nt = "pod_loss"
           THEN NotifyIn([ss EXCEPT ![s].st = "idle", ![s].pod = TRUE], s, "wake")
           ELSE [ss EXCEPT ![s].st = "idle", ![s].pod = TRUE]
  /\ UNCHANGED n

\* Retries run out (SL-2): awaiting_client_action, and its child event.
RetriesOut(s) ==
  /\ ss[s].st \in Recovering
  /\ ss' = NotifyIn([ss EXCEPT ![s].st = ACA], s, "wake")
  /\ UNCHANGED n

(***************************************************************************)
(* The root's client: the principal that owns every root of this model.    *)
(* It acts on any session through REST (SL-4 "Who suspends and who lifts"). *)
(***************************************************************************)
LiftTo(r, keepNotice) ==
  [r EXCEPT !.st = IF r.pod THEN "idle" ELSE "resume_pending", !.cause = "none", !.pa = None,
            !.direct = TRUE, !.nt = IF keepNotice THEN r.nt ELSE "none"]

\* POST /v1/sessions/{id}/suspend (SL-4 "Routes" and "Steps").
RootSuspend(s) ==
  /\ "client" \in Causes
  /\ n.susp < MaxSuspend
  /\ \/ /\ ss[s].st \in Live \cup {"blocked"}
        /\ \E full \in IF n.crash < MaxCrash THEN {TRUE, FALSE} ELSE {TRUE} :
             /\ ss' = SuspendIn(s, "client",
                        [Cut(ss[s]) EXCEPT !.st = "suspended", !.cause = "client", !.pa = None,
                                            !.direct = FALSE, !.ld = FALSE], full)
             /\ n' = [n EXCEPT !.susp = @ + 1, !.crash = IF full THEN @ ELSE @ + 1]
     \/ /\ ss[s].par = None /\ ss[s].st \in Recovering \cup {ACA}
        /\ ss' = SuspendIn(s, "client", [ss[s] EXCEPT !.sreq = "client"], TRUE)
        /\ n' = [n EXCEPT !.susp = @ + 1]
     \/ /\ ss[s].st = "suspended" /\ ss[s].cause = "external_limit"
        /\ ss' = [ss EXCEPT ![s].cause = "client"]
        /\ n' = [n EXCEPT !.susp = @ + 1]

\* POST /v1/sessions/{id}/resume (SL-2, SL-4): from suspended (an external limit with
\* acknowledgeExternalLimit: true), from blocked, or from awaiting_client_action; or the
\* requested suspension of a recovering session is cleared.
RootResume(s) ==
  /\ \/ /\ ss[s].st \in Paused /\ Admit
        /\ ss' = [ss EXCEPT ![s] = LiftTo(@, TRUE)]
     \/ /\ ss[s].st = ACA
        /\ ss' = [ss EXCEPT ![s].st = "resume_pending", ![s].sreq = "none"]
     \/ /\ ss[s].st \in Recovering /\ ss[s].sreq # "none"
        /\ ss' = [ss EXCEPT ![s].sreq = "none"]
  /\ UNCHANGED n

\* A message from the root's client to s (SL-5 table, first column).
RootMsg(s) ==
  /\ \/ /\ ss[s].st = "idle"
        /\ ss' = [ss EXCEPT ![s].st = "running", ![s].ev = {}]
     \/ /\ ss[s].st = "dormant" /\ Admit
        /\ ss' = [ss EXCEPT ![s].st = "resume_pending", ![s].ev = @ \cup {"wake"}, ![s].ld = FALSE]
     \/ /\ ss[s].st \in Paused /\ ss[s].cause # "external_limit" /\ Admit
        /\ ss' = [ss EXCEPT ![s] = [LiftTo(@, FALSE) EXCEPT !.ev = @ \cup {"wake"}]]
     \/ /\ ss[s].st = ACA /\ ss[s].sreq # "external_limit"
        /\ ss' = [ss EXCEPT ![s].st = "resume_pending", ![s].sreq = "none", ![s].ev = @ \cup {"wake"}]
     \* Held: an external limit, a recovering or starting session.  A message that resume
     \* admission refuses is answered 429 and changes nothing (SL-10).
     \/ /\ \/ ss[s].st = "suspended" /\ ss[s].cause = "external_limit"
           \/ ss[s].st = ACA /\ ss[s].sreq = "external_limit"
           \/ ss[s].st \in Recovering \cup {"starting"}
        /\ ss' = [ss EXCEPT ![s].ev = @ \cup {"wake"}]
  /\ UNCHANGED n

\* POST /v1/sessions/{id}/close by the root's client (SL-7), and the ended child event.
RootClose(s) ==
  /\ NonTerm(ss[s].st)
  /\ ss' = FinishIn(ss, s, CloseOutcome(ss[s]), TRUE, "wake")
  /\ UNCHANGED n

\* DELETE /v1/sessions/{id} (SL-7).
RootCancel(s) ==
  /\ NonTerm(ss[s].st)
  /\ ss' = FinishIn(ss, s, "cancelled", TRUE, "wake")
  /\ UNCHANGED n

(***************************************************************************)
(* The parent's platform operations (0166 "DP-1").  The parent's runtime   *)
(* calls them during a turn, so the parent is running; a paused session's *)
(* calls that act on another session are refused (SLR-13).                *)
(***************************************************************************)
ParentOf(c) == /\ NonTerm(ss[c].st) /\ ss[c].cip /\ ss[c].par # None
               /\ ss[ss[c].par].st = "running"

\* session.send (SL-5 table, second column).
ParentSend(c) ==
  /\ ParentOf(c)
  /\ \/ /\ ss[c].st = "idle"
        /\ ss' = [ss EXCEPT ![c].st = "running", ![c].ev = {}]
     \/ /\ ss[c].st = "dormant" /\ Admit
        /\ ss' = [ss EXCEPT ![c].st = "resume_pending", ![c].ev = @ \cup {"wake"}, ![c].ld = FALSE,
                            ![c].direct = FALSE]
     \/ /\ ss[c].st = ACA /\ ss[c].sreq # "external_limit"
        /\ ss' = [ss EXCEPT ![c].st = "resume_pending", ![c].ev = @ \cup {"wake"}]
     \* Held: a paused, recovering, or starting child, a refused admission, an external limit.
     \/ /\ \/ ss[c].st \in Paused \cup Recovering \cup {"starting"}
           \/ ss[c].st = "dormant" /\ ~Admit
           \/ ss[c].st = ACA /\ ss[c].sreq = "external_limit"
        /\ ss' = [ss EXCEPT ![c].ev = @ \cup {"wake"}]
  /\ UNCHANGED n

\* session.close (SL-7): no child event for a child the parent closed itself.
ParentClose(c) ==
  /\ ParentOf(c)
  /\ ss' = FinishIn(ss, c, CloseOutcome(ss[c]), FALSE, "wake")
  /\ UNCHANGED n

(***************************************************************************)
(* Leaving the parent (0166 "Leaving the parent" in "DP-2"; 0146 SL-4 and   *)
(* SL-9).  A transfer re-links the session to its root with the root's     *)
(* client as its client; a release makes it a root.  The governing grant   *)
(* is the parent's for a parent's call, which requires a running parent,   *)
(* and the root's for the root's client's call, which requires a           *)
(* non-terminal root (DPR-16).                                             *)
(***************************************************************************)
\* The blocked session that leaves: its nearest paused ancestor in the new position, or a resume through admission.
AfterLeave(f, c) ==
  IF f[c].st # "blocked" THEN f
  ELSE IF f[c].par = None
    THEN [f EXCEPT ![c].st = "suspended", ![c].cause = "client", ![c].pa = None]
  ELSE IF NPAIn(f, c) # None
    THEN [f EXCEPT ![c].pa = NPAIn(f, c)]
  ELSE IF CountCounted(f) < Cap
    THEN [f EXCEPT ![c] = LiftTo(@, TRUE)]
  ELSE f   \* admission refused: the text does not say; the session stays blocked as it was

TransferIn(c) ==
  AfterLeave([ss EXCEPT ![c].par = TreeRoot(c), ![c].cip = FALSE], c)

ReleaseIn(c) ==
  AfterLeave([ss EXCEPT ![c].par = None, ![c].cip = FALSE, ![c].child = FALSE], c)

LeaveOK(c) == /\ n.leave < MaxLeave /\ NonTerm(ss[c].st) /\ ss[c].par # None

ParentTransfer(c) ==
  /\ LeaveOK(c) /\ ParentOf(c) /\ ss[c].par # TreeRoot(c)
  /\ ss' = TransferIn(c)
  /\ n' = [n EXCEPT !.leave = @ + 1]

RootTransfer(c) ==
  /\ LeaveOK(c) /\ ss[c].cip /\ ~Term(ss[TreeRoot(c)].st) /\ ss[c].par # TreeRoot(c)
  /\ ss' = TransferIn(c)
  /\ n' = [n EXCEPT !.leave = @ + 1]

ParentRelease(c) ==
  /\ LeaveOK(c) /\ ParentOf(c)
  /\ ss' = ReleaseIn(c)
  /\ n' = [n EXCEPT !.leave = @ + 1]

RootRelease(c) ==
  /\ LeaveOK(c) /\ ~Term(ss[TreeRoot(c)].st)
  /\ ss' = ReleaseIn(c)
  /\ n' = [n EXCEPT !.leave = @ + 1]

\* A root releases a child with handoff: true and ends completed with the successor (SL-7 "Hand-off", SLR-30).
Handoff(c) ==
  /\ AllowHandoff
  /\ LeaveOK(c) /\ ParentOf(c)
  /\ LET p == ss[c].par IN
     /\ ss[p].par = None
     /\ ss' = [FinishIn(ReleaseIn(c), p, "completed", FALSE, "wake") EXCEPT ![p].succ = c]
  /\ n' = [n EXCEPT !.leave = @ + 1]

(***************************************************************************)
(* The gateway and its timers.                                             *)
(***************************************************************************)
\* The session that paused_ancestor_id names enters a live state (SL-2, SLR-6), or it is a
\* terminal session that is not ended_suspended, which backlog resume treats as a safety net
\* (SL-9 "A blocked session that ends"): the blocked session moves under its next paused
\* ancestor, or resumes through admission.
PaLifted(d) ==
  /\ ss[d].st = "blocked" /\ ss[d].pa # None
  /\ \/ ss[ss[d].pa].st \in Live
     \/ Term(ss[ss[d].pa].st) /\ ~ss[ss[d].pa].es

Repoint(d) ==
  /\ PaLifted(d)
  /\ NPA(d) # None
  /\ ss' = [ss EXCEPT ![d].pa = NPA(d)]
  /\ UNCHANGED n

AncestorLive(d) ==
  /\ PaLifted(d)
  /\ NPA(d) = None
  /\ Admit
  /\ ss' = [ss EXCEPT ![d] = [LiftTo(@, TRUE) EXCEPT !.direct = FALSE]]
  /\ UNCHANGED n

\* The backlog sweep finishes a partial suspension (SL-4 "A partial suspension", SLR-20).
Sweep(d) ==
  /\ ss[d].st \in Live /\ NPA(d) # None /\ ~ss[d].direct /\ ~ss[d].ld
  /\ ss' = [ss EXCEPT ![d] = [Cut(@) EXCEPT !.st = "blocked", !.pa = NPA(d)]]
  /\ UNCHANGED n

\* An external limit (SL-2, SL-4 "External limit suspension", L42).
ExtLimit(s) ==
  /\ "external_limit" \in Causes
  /\ n.susp < MaxSuspend
  /\ \/ /\ ss[s].st \in {"running", "idle"} \cup {"blocked"}
        /\ ss' = SuspendIn(s, "external_limit",
                   [Cut(ss[s]) EXCEPT !.st = "suspended", !.cause = "external_limit", !.pa = None,
                                       !.direct = FALSE, !.ld = FALSE], TRUE)
     \/ /\ ss[s].st = "dormant"
        /\ ss' = SuspendIn(s, "external_limit",
                   [ss[s] EXCEPT !.st = "suspended", !.cause = "external_limit", !.direct = FALSE,
                                 !.ld = FALSE], TRUE)
     \/ /\ ss[s].st \in Recovering \cup {ACA}
        /\ ss' = SuspendIn(s, "external_limit", [ss[s] EXCEPT !.sreq = "external_limit"], TRUE)
  /\ n' = [n EXCEPT !.susp = @ + 1]

\* The timers that end a session with expired (SL-10 "Timers"): maxSessionAge, perChildMaxAge,
\* maxSuspendedSessionSeconds, maxAwaitingClientActionSeconds (stopped while an ancestor is
\* suspended), and maxResumeWindowSeconds is RetriesOut.  The ended event of an expiry
\* follows the parent's wakeOn ("A child's age").
ExpireAllowed(s) ==
  \/ ss[s].st \in {"starting", "running", "idle", "dormant"} \cup Paused \cup Recovering
  \/ ss[s].st = ACA /\ (NPA(s) = None \/ (ss[s].child /\ ~AgeStopsInACA))

Expire(s) ==
  /\ WithExpiry
  /\ NonTerm(ss[s].st) /\ ExpireAllowed(s)
  /\ ss' = FinishIn(ss, s, "expired", TRUE, ExpiryKind)
  /\ UNCHANGED n

\* A non-retryable failure (SL-2).
Fail(s) ==
  /\ n.kill < MaxKill /\ NonTerm(ss[s].st)
  /\ ss' = FinishIn(ss, s, "failed", TRUE, "wake")
  /\ n' = [n EXCEPT !.kill = @ + 1]

\* An operator's force-terminate (SL-9, SLR-31): failed, and every descendant closed at once.
ForceTerminate(s) ==
  /\ n.kill < MaxKill /\ NonTerm(ss[s].st)
  /\ ss' = [FinishIn(ss, s, "failed", TRUE, "wake") EXCEPT ![s].forced = TRUE]
  /\ n' = [n EXCEPT !.kill = @ + 1]

\* A tenant-admin's terminate-tree (SL-9, SLR-31).  The text does not state the root's
\* terminal state (open decision O4 of 0175); the model ends it failed, as a force-terminate,
\* and applies it to a root that already ended as well.
TreeTerminate(r) ==
  /\ n.kill < MaxKill /\ Exists(ss[r].st) /\ ss[r].par = None /\ ~ss[r].forced
  /\ ss' = IF Term(ss[r].st) THEN [ss EXCEPT ![r].forced = TRUE]
           ELSE [FinishIn(ss, r, "failed", FALSE, "wake") EXCEPT ![r].forced = TRUE]
  /\ n' = [n EXCEPT !.kill = @ + 1]

\* onParentEnd applied to one child of a terminal parent (SL-9 "onParentEnd"): close_all, or a
\* force-terminate's or tree terminate's cascade, closes it at once; let_finish closes a waiting
\* or awaiting_client_action child at once and lets the others finish.
Cascade(c) ==
  /\ NonTerm(ss[c].st) /\ ss[c].par # None
  /\ LET p == ss[c].par IN
     /\ Term(ss[p].st)
     /\ \/ ss[p].forced \/ ss[p].ope = "close_all"
        \/ ss[c].st \in {"idle", "dormant", ACA}
     /\ ss' = [FinishIn(ss, c, CloseOutcome(ss[c]), FALSE, "wake") EXCEPT ![c].forced = ss[p].forced]
  /\ UNCHANGED n

\* cascadeTimeoutSeconds under let_finish (SL-9).  Its clock runs from the parent's end in
\* every state of the child; a child in awaiting_client_action is closed at once by Cascade.
CascadeTimeout(c) ==
  /\ NonTerm(ss[c].st) /\ ss[c].st # ACA /\ ss[c].par # None
  /\ LET p == ss[c].par IN Term(ss[p].st) /\ ~ss[p].forced /\ ss[p].ope = "let_finish"
  /\ ss' = FinishIn(ss, c, "expired", FALSE, "wake")
  /\ UNCHANGED n

\* Every session has ended: the behavior stutters.
Done == /\ \A s \in S : ~NonTerm(ss[s].st)
        /\ UNCHANGED vars

RuntimeStep(s) ==
  \/ Create(s) \/ EndTurn(s) \/ Complete(s) \/ Output(s)
RootClientStep(s) ==
  \/ RootSuspend(s) \/ RootResume(s) \/ RootMsg(s) \/ RootClose(s) \/ RootCancel(s)
  \/ RootTransfer(s) \/ RootRelease(s)
ParentStep(s) ==
  \/ ParentSend(s) \/ ParentClose(s) \/ ParentTransfer(s) \/ ParentRelease(s) \/ Handoff(s)
GatewayStep(s) ==
  \/ StartCommit(s) \/ Deliver(s) \/ IdleClock(s) \/ WakeDormant(s) \/ PodLoss(s)
  \/ PodRelease(s) \/ RestoreStart(s) \/ RestoreCommit(s) \/ RetriesOut(s) \/ Repoint(s)
  \/ AncestorLive(s) \/ Sweep(s) \/ ExtLimit(s) \/ Expire(s) \/ Fail(s) \/ Cascade(s)
  \/ CascadeTimeout(s)

Next ==
  \/ \E s \in S :
       \/ RuntimeStep(s) /\ by' = Actor("runtime")
       \/ RootClientStep(s) /\ by' = Actor("rootclient")
       \/ ParentStep(s) /\ by' = Actor("parent")
       \/ GatewayStep(s) /\ by' = Actor("gateway")
       \/ ForceTerminate(s) /\ by' = Actor("operator")
       \/ TreeTerminate(s) /\ by' = Actor("tenantadmin")
  \/ Done

Spec == Init /\ [][Next]_vars

\* The state as its successors see it, for the safety configurations: without the actor of
\* the last step, and with every terminal state written as one, because no step reads which
\* terminal state a session ended in.  Two states with the same view have the same
\* successors, and TLC checks the action properties on each step before it merges states.
\* HandoffSuccessor, the one invariant that reads a terminal state, holds on a row only when
\* the step that set succ wrote completed.
View == <<[s \in S |-> IF Term(ss[s].st) THEN [ss[s] EXCEPT !.st = "ended"] ELSE ss[s]], n>>

\* The fairness the liveness properties assume.  The cascade timeout, the re-pointing, and the
\* sweep run once they stay enabled (weak fairness).  The cascade and the tree resume run once
\* they are enabled again and again (strong fairness): a child's state can move in and out of
\* the states a let_finish cascade closes, and other sessions take and free room under the cap.
FairSpec ==
  /\ Spec
  /\ \A s \in S : /\ SF_vars(Cascade(s) /\ by' = Actor("gateway"))
                  /\ WF_vars(CascadeTimeout(s) /\ by' = Actor("gateway"))
                  /\ WF_vars(Repoint(s) /\ by' = Actor("gateway"))
                  /\ WF_vars(Sweep(s) /\ by' = Actor("gateway"))
                  /\ SF_vars(AncestorLive(s) /\ by' = Actor("gateway"))

(***************************************************************************)
(* Properties.  README.md states each in plain words.                     *)
(***************************************************************************)
TypeOK ==
  /\ \A s \in S :
       /\ ss[s].st \in States
       /\ ss[s].par \in S \cup {None}
       /\ ss[s].pa \in S \cup {None}
       /\ ss[s].ope \in {"close_all", "let_finish"}
       /\ ss[s].cause \in {"none", "client", "external_limit"}
       /\ ss[s].sreq \in {"none", "client", "external_limit"}
       /\ ss[s].lte \in {"none", "message", "cut"}
       /\ ss[s].nt \in {"none", "suspension", "pod_loss"}
       /\ ss[s].ev \subseteq {"wake", "nowake"}
  /\ n \in [susp : Nat, pod : Nat, leave : Nat, kill : Nat, crash : Nat]
  /\ by \in {"rootclient", "parent", "runtime", "gateway", "operator", "tenantadmin", "untracked"}

\* P1. The parent links form a forest: no session is its own ancestor.
NoCycle == \A s \in S : ~IsAnc(s, s)

\* P2. A terminal state is never left (SL-1).
TerminalStays == [][\A s \in S : Term(ss[s].st) => ss'[s].st = ss[s].st]_vars

\* P3. Every change of state is a transition of the SL-2 table.
Moves ==
  {<<"absent", "starting">>,
   <<"starting", "running">>, <<"starting", "idle">>, <<"starting", "blocked">>,
   <<"running", "idle">>, <<"running", "completed">>, <<"running", "dormant">>,
   <<"running", "suspended">>, <<"running", "blocked">>, <<"running", "resume_pending">>,
   <<"idle", "running">>, <<"idle", "dormant">>, <<"idle", "suspended">>, <<"idle", "blocked">>,
   <<"idle", "resume_pending">>,
   <<"dormant", "resume_pending">>, <<"dormant", "suspended">>, <<"dormant", "blocked">>,
   <<"suspended", "idle">>, <<"suspended", "resume_pending">>,
   <<"blocked", "idle">>, <<"blocked", "resume_pending">>, <<"blocked", "suspended">>,
   <<"resume_pending", "resuming">>, <<"resume_pending", ACA>>,
   <<"resuming", "idle">>, <<"resuming", "suspended">>, <<"resuming", "blocked">>,
   <<"resuming", ACA>>, <<ACA, "resume_pending">>}
  \cup {<<f, t>> : f \in States \ ({"absent"} \cup Terminal), t \in Terminal}
LegalMoves == [][\A s \in S : ss'[s].st # ss[s].st => <<ss[s].st, ss'[s].st>> \in Moves]_vars

\* P4. A blocked session points to one of its ancestors (SL-1 "Row fields", SLR-15).
BlockedUnderPausedAncestor ==
  \A s \in S : ss[s].st = "blocked" => ss[s].pa \in S /\ IsAnc(ss[s].pa, s)

\* P4b. No blocked session is left under a terminal session that is not ended_suspended
\* (SLR-28 as written).
NoBlockedUnderEndedSession ==
  \A s \in S : ss[s].st = "blocked" /\ ss[s].pa \in S => (~Term(ss[ss[s].pa].st) \/ ss[ss[s].pa].es)

\* P5. No live session has a paused ancestor, except a session whose own pause the root's
\* client lifted, and a dormant session that an external-limit suspension left dormant (SLR-4).
LiveUnderPausedExempt ==
  \A s \in S : ss[s].st \in Live /\ NPA(s) # None => ss[s].direct \/ (ss[s].st = "dormant" /\ ss[s].ld)

\* P6. A resume that the root's client requested for a session is not undone (SL-2, SLR-4).
DirectResumeHonoured == \A s \in S : ss[s].direct => ss[s].st # "blocked"

\* P7. Only the root's client lifts a pause: a session leaves suspended or blocked for a
\* non-terminal state only by a step of the root's client, or after the pause of the session's
\* paused ancestor was lifted, by the gateway's tree resume or by any step that the tree resume
\* would have taken anyway (SL-4, SLR-5).
OnlyRootClientLifts ==
  [][\A s \in S : ss[s].st \in Paused /\ ~(ss'[s].st \in Paused) /\ ~Term(ss'[s].st)
                  => \/ by' = "rootclient"
                     \/ ss[s].st = "blocked" /\ ss[s].pa # None /\ ~MarkIn(ss, ss[s].pa)]_vars

\* P8. A suspension cuts the open turn and records the resume notice (SL-3, SLR-3, SLR-7).
SuspensionCutsTurn ==
  [][\A s \in S : ss[s].st = "running" /\ ss'[s].st \in Paused
                  => ss'[s].lte = "cut" /\ ss'[s].nt = "suspension"]_vars

\* P9. An idle session keeps its pod while a descendant runs (SLR-9).
IdleKeepsPodForRunningDescendant ==
  [][\A s \in S : ss[s].st = "idle" /\ ss'[s].st = "dormant"
                  => \A d \in Desc(s) : ss[d].st # "running"]_vars

\* P10. The concurrency caps hold (SLR-12).
CapsHold == CountCounted(ss) <= Cap

\* P11. A parent never has more children counting toward maxParallelChildren than the
\* caveat allows (SL-10 "A parent's parallelism").
ParallelismHolds == \A p \in S : ParCount(ss, p) <= MaxPar

\* P12. The close rule: a close records completed only for a session with no open turn whose
\* last turn ended with a message (SLR-8).
CloseRule ==
  [][\A s \in S : ss'[s].st = "completed" /\ ss[s].st # "completed" /\ ~Complete(s)
                  /\ ~(\E c \in S : Handoff(c) /\ ss[c].par = s)
                  => ss[s].st # "running" /\ ss[s].lte = "message"]_vars

\* P13. onParentEnd only narrows down the tree: a child of a close_all session is close_all (SLR-32).
OnParentEndOrder ==
  \A s \in S : ss[s].par # None /\ ss[ss[s].par].ope = "close_all" => ss[s].ope = "close_all"

\* P14. An awaiting_client_action session does not expire while an ancestor is suspended (SLR-21).
ACANoExpiryWhileAncestorPaused ==
  [][\A s \in S : ss[s].st = ACA /\ NPA(s) # None /\ ~Term(ss[NPA(s)].st) => ss'[s].st # "expired"]_vars

\* P15. A terminal session's inbox is closed and empty (SLR-10).
InboxClosed == \A s \in S : Term(ss[s].st) => ss[s].ev = {}

\* P16. A handed-off root ended completed and names a session that is a root (SLR-30).
HandoffSuccessor ==
  \A s \in S : ss[s].succ # None => ss[s].st = "completed" /\ ss[ss[s].succ].par = None

\* P17. Only a running session's runtime creates a child, and a paused session acts on no
\* other session (SLR-13): a parent's operation changes a child only while the parent runs.
ParentActsOnlyWhenRunning ==
  [][\A c \in S : (ParentSend(c) \/ ParentClose(c) \/ ParentTransfer(c) \/ ParentRelease(c))
                  => ss[ss[c].par].st = "running"]_vars

(***************************************************************************)
(* Liveness, under FairSpec.                                               *)
(***************************************************************************)
ChildOfEnded(c) == NonTerm(ss[c].st) /\ ss[c].par # None /\ Term(ss[ss[c].par].st)

\* L1. Every child of a terminal parent ends (SL-9, SLR-31).
ParentEndReachesChildren == \A c \in S : ChildOfEnded(c) ~> Term(ss[c].st)

\* L2. Every child of a force-terminated or tree-terminated session ends (SLR-31).
ForceReachesDescendants ==
  \A c \in S : (ChildOfEnded(c) /\ ss[ss[c].par].forced) ~> Term(ss[c].st)

\* L3. A session blocked under a live paused ancestor leaves blocked, unless that ancestor
\* pauses or ends again (SLR-6).
TreeResumeReachesBlocked ==
  \A d \in S : PaLifted(d) ~> (ss[d].st # "blocked" \/ ~PaLifted(d))

\* L4. A live session under a paused ancestor that is not exempt is eventually paused (SLR-20).
UnsweptLive(d) == ss[d].st \in Live /\ NPA(d) # None /\ ~ss[d].direct /\ ~ss[d].ld
PartialSuspensionCompletes == \A d \in S : UnsweptLive(d) ~> ~UnsweptLive(d)

=============================================================================
