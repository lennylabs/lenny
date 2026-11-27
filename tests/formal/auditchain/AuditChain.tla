----------------------------- MODULE AuditChain -----------------------------
(***************************************************************************)
(* A model of the per-tenant audit hash chain of §11.7, the in-place       *)
(* dead-letter redaction of §12.8 step 14 with its signed RedactionReceipt, *)
(* the rotation of the receipt signing key, the chain verifiers, and the   *)
(* periodic receipt scan, against the database roles the specification     *)
(* names. Change 0180 builds it; README.md maps each action to the code     *)
(* and states each property in plain words.                                *)
(*                                                                         *)
(* One tenant's chain is modelled. A hash is the tuple of its inputs, so    *)
(* two rows hash alike only when every hashed column is equal: the hash is  *)
(* collision-free by construction, and anyone who knows a row can compute   *)
(* its hash. A signature is a flag on the receipt: a receipt carries        *)
(* signed = TRUE only when the holder of the key it names signed exactly    *)
(* its fields, so a database role without the KMS grant cannot produce one  *)
(* for a key the platform holds.                                           *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS
  Variant,          \* "S3": main with step 0177.S3; "Main": main before 0177.S3
  NWriters,         \* appending replicas that take the advisory lock
  AppendsPerWriter, \* transactions per appending replica
  BatchSize,        \* rows per transaction (AppendBatch when 2)
  BypassWriter,     \* TRUE adds one writer that skips the advisory lock
  TriggerMode,      \* INSERT check of prev_hash: "none" (code), "naive", "receiptAware"
  NJobs,            \* erasure jobs (step 14 redactions)
  InitRows,         \* committed rows in the initial chain
  InitDL,           \* initial rows the translator dead-lettered
  InitGdpr,         \* initial rows whose event type is gdpr.*
  InitRedacted,     \* initial rows already redacted with a receipt under K1
  MaxSeq,           \* bound on sequence numbers
  MaxAborts,        \* failed transactions per behavior
  MaxTime,          \* bound on the clock that created_at reads
  MaxSweeps,        \* retention sweeps per behavior
  Adversary,        \* "none", "app", "erasure", "signer", or "dba"
  MaxTamper,        \* adversary actions per behavior
  Accepted,         \* tamper effects the configuration states as limits
  RedactionGuard,   \* TRUE: the trigger admits only a redaction-form UPDATE
  Rotation,         \* "none", "OneRollout", or "VerifyFirst"
  ListRetired,      \* TRUE: the operator lists each key leaving platform:audit-verify
  MaxRotSteps,      \* operator rotation steps per behavior
  NReplicas,        \* gateway replicas, each with its own receipt key configuration
  RunVerifiers,     \* TRUE: run the multi-statement query and window verifiers
  WindowN           \* rows the periodic window check reads

ASSUME Variant \in {"S3", "Main"}
ASSUME TriggerMode \in {"none", "naive", "receiptAware"}
ASSUME Adversary \in {"none", "app", "erasure", "signer", "dba"}
ASSUME Rotation \in {"none", "OneRollout", "VerifyFirst"}
ASSUME BatchSize \in {1, 2}

Genesis == <<"genesis">>
NoKey == "none"
AdvKey == "Kadv"
KeyOrder == <<"K1", "K2", "K3", "K4">>

WriterIds == 1..NWriters
BypassIds == IF BypassWriter THEN {NWriters + 1} ELSE {}
JobIds == (NWriters + 2)..(NWriters + 1 + NJobs)
Procs == WriterIds \cup BypassIds \cup JobIds
Replicas == 1..NReplicas
Seqs == 1..MaxSeq

Max(S) == CHOOSE x \in S : \A y \in S : y <= x
Min(S) == CHOOSE x \in S : \A y \in S : x <= y

EmptyF == [x \in {} |-> 0]
Put(f, k, v) == [x \in DOMAIN f \cup {k} |-> IF x = k THEN v ELSE f[x]]
Del(f, k) == [x \in DOMAIN f \ {k} |-> f[x]]
Merge(f, g) == [x \in DOMAIN f \cup DOMAIN g |-> IF x \in DOMAIN g THEN g[x] ELSE f[x]]

(***************************************************************************)
(* Rows and hashes. A row holds the hashed columns of §11.7 item 3 that    *)
(* differ between rows: prev_hash, the payload (c, with the redaction       *)
(* marker m), the event type (g: a gdpr.* row), and created_at (ts). The    *)
(* sequence number is the row's key. dl is ocsf_translation_state =        *)
(* 'dead_lettered', which is not hashed.                                   *)
(***************************************************************************)
Row(prev, c, m, g, dl, ts) == [prev |-> prev, c |-> c, m |-> m, g |-> g, dl |-> dl, ts |-> ts]
NoneRow == Row(Genesis, <<"none">>, FALSE, FALSE, FALSE, 0)
H(s, r) == <<s, r.prev, r.c, r.m, r.g, r.ts>>
Redacted(r, j) == [r EXCEPT !.c = <<"r", j>>, !.m = TRUE]

RECURSIVE ChainUpTo(_)
ChainUpTo(n) ==
  IF n = 0 THEN EmptyF
  ELSE LET f == ChainUpTo(n - 1)
       IN Put(f, n, Row(IF n = 1 THEN Genesis ELSE H(n - 1, f[n - 1]),
                        IF n \in InitGdpr THEN <<"g">> ELSE <<"e">>,
                        FALSE, n \in InitGdpr, n \in InitDL, 0))

InitChain == ChainUpTo(InitRows)
InitLog == [s \in DOMAIN InitChain |->
              IF s \in InitRedacted THEN Redacted(InitChain[s], 0) ELSE InitChain[s]]
InitRcpt == [s \in InitRedacted |->
               [orig |-> H(s, InitChain[s]), new |-> H(s, InitLog[s]),
                key |-> "K1", signed |-> TRUE]]

VARIABLES
  log, rcpt,          \* committed audit_log rows and audit_redaction_receipts rows
  gLog, gRcpt,        \* ghost: the tables as the legitimate processes alone left them
  nseq,               \* the next value of the tenant's audit sequence
  lockH,              \* holder of the tenant's advisory lock, 0 when free
  clock, sweeps,
  wpc, wleft, wbatch, wtail, wlink, wseq, wbuf, aborts, refused,
  jpc, jsel, jnew, jkey, jrep, jobFail,
  qpc, qrows, qrA, wvpc, wvrows, alarms,
  rcfg, dcfg, rotSteps, rotPhase,
  tamper

vars == <<log, rcpt, gLog, gRcpt, nseq, lockH, clock, sweeps,
          wpc, wleft, wbatch, wtail, wlink, wseq, wbuf, aborts, refused,
          jpc, jsel, jnew, jkey, jrep, jobFail,
          qpc, qrows, qrA, wvpc, wvrows, alarms,
          rcfg, dcfg, rotSteps, rotPhase, tamper>>

wvars == <<wpc, wleft, wbatch, wtail, wlink, wseq, wbuf, aborts, refused>>
jvars == <<jpc, jsel, jnew, jkey, jrep, jobFail>>
vvars == <<qpc, qrows, qrA, wvpc, wvrows, alarms>>
kvars == <<rcfg, dcfg, rotSteps, rotPhase>>

InitCfg == [sign |-> "K1", verify |-> NoKey, retired |-> {}]
Trusted(r) == ({rcfg[r].sign, rcfg[r].verify} \cup rcfg[r].retired) \ {NoKey}
KeyIndex(k) == CHOOSE i \in DOMAIN KeyOrder : KeyOrder[i] = k
NextKey(k) == KeyOrder[KeyIndex(k) + 1]

(***************************************************************************)
(* Links. VLink is the hash a successor of row s must carry as the chain   *)
(* verifiers and, under S3, the append path compute it: a row that carries *)
(* the redaction marker and has a receipt links through the receipt's      *)
(* original_hash (applyReceipts, markRedacted); any other row links        *)
(* through the hash of its current columns.                                *)
(***************************************************************************)
VLink(f, rc, s) == IF f[s].m /\ s \in DOMAIN rc THEN rc[s].orig ELSE H(s, f[s])
Below(f, s) == {x \in DOMAIN f : x < s}
Above(f, s) == {x \in DOMAIN f : x > s}

\* The prev_hash a row appended after tail row s of f carries.
AppendLink(f, rc, s) ==
  IF Variant = "S3" /\ f[s].m /\ s \in DOMAIN rc THEN rc[s].orig ELSE H(s, f[s])

\* The prev_hash the INSERT trigger expects of the next row, per TriggerMode.
TrigLink(f, rc) ==
  IF DOMAIN f = {} THEN Genesis
  ELSE IF TriggerMode = "naive" THEN H(Max(DOMAIN f), f[Max(DOMAIN f)])
  ELSE VLink(f, rc, Max(DOMAIN f))
TriggerAdmits(f, rc, prev) == TriggerMode = "none" \/ prev = TrigLink(f, rc)

(***************************************************************************)
(* The verifiers, as functions of the rows and receipts they read.         *)
(*                                                                         *)
(* QBroken is Chain.VerifyRows (the audit query API) and Chain.Verify      *)
(* (Store.Verify): rows hold their hash after applyReceipts over the       *)
(* receipts read rcA; contentCheck reads the receipts read rcB.            *)
(***************************************************************************)
QHash(f, rcA, s) == VLink(f, rcA, s)
QLawful(f, rcA, rcB, T, s) ==
  /\ f[s].m
  /\ s \in DOMAIN rcB
  /\ rcB[s].orig = QHash(f, rcA, s)
  /\ rcB[s].new = H(s, f[s])
  /\ rcB[s].signed
  /\ rcB[s].key \in T
QContentOK(f, rcA, rcB, T, s) ==
  \/ (H(s, f[s]) = QHash(f, rcA, s) /\ s \notin DOMAIN rcB /\ ~f[s].m)
  \/ QLawful(f, rcA, rcB, T, s)
QLinkOK(f, rcA, s) ==
  IF Below(f, s) = {} THEN f[s].prev = Genesis
  ELSE f[s].prev = QHash(f, rcA, Max(Below(f, s)))
QRowBroken(f, rcA, rcB, T, s) == ~QContentOK(f, rcA, rcB, T, s) \/ ~QLinkOK(f, rcA, s)
QBroken(f, rcA, rcB, T) == \E s \in DOMAIN f : QRowBroken(f, rcA, rcB, T, s)
QGap(f, rcA, rcB, T) ==
  \E s \in DOMAIN f : ~QRowBroken(f, rcA, rcB, T, s) /\ Below(f, s) # {}
                      /\ Max(Below(f, s)) # s - 1

(* WBroken is verifyChainWindow over the newest WindowN rows               *)
(* (CheckChainContinuityRecent, run by the periodic check): links only,    *)
(* the genesis sentinel asserted only for sequence number 1. Under S3 the  *)
(* window applies receipts to redaction-marked rows (applyChainReceipts);  *)
(* under Main it does not.                                                 *)
WindowOf(f) == {s \in DOMAIN f : Cardinality(Above(f, s)) < WindowN}
WLink(f, rc, s) == IF Variant = "S3" THEN VLink(f, rc, s) ELSE H(s, f[s])
WBroken(f, rc) ==
  LET W == WindowOf(f) IN
  /\ W # {}
  /\ \/ (Min(W) = 1 /\ f[1].prev # Genesis)
     \/ \E s \in W \ {Min(W)} : f[s].prev # WLink(f, rc, Max({x \in W : x < s}))

(* Missing is CheckRedactionReceipts, the periodic receipt scan: rows that  *)
(* carry the marker or have a receipt, whose receipt is absent, does not   *)
(* match the row's content hash (new_hash), or does not verify under a     *)
(* trusted key. Its non-empty result fires AuditRedactionReceiptMissing.   *)
Missing(f, rc, T) ==
  {s \in DOMAIN f : (f[s].m \/ s \in DOMAIN rc) /\
                    ~(s \in DOMAIN rc /\ rc[s].new = H(s, f[s]) /\ rc[s].signed /\ rc[s].key \in T)}

Detected(T) == QBroken(log, rcpt, rcpt, T) \/ WBroken(log, rcpt) \/ Missing(log, rcpt, T) # {}

(***************************************************************************)
(* Initial state.                                                          *)
(***************************************************************************)
Init ==
  /\ log = InitLog
  /\ rcpt = InitRcpt
  /\ gLog = InitLog
  /\ gRcpt = InitRcpt
  /\ nseq = InitRows + 1
  /\ lockH = 0
  /\ clock = 0
  /\ sweeps = 0
  /\ wpc = [p \in Procs |-> "idle"]
  /\ wleft = [p \in Procs |-> IF p \in JobIds THEN 0 ELSE AppendsPerWriter]
  /\ wbatch = [p \in Procs |-> 0]
  /\ wtail = [p \in Procs |-> [has |-> FALSE, s |-> 0, row |-> NoneRow]]
  /\ wlink = [p \in Procs |-> Genesis]
  /\ wseq = [p \in Procs |-> 0]
  /\ wbuf = [p \in Procs |-> EmptyF]
  /\ aborts = 0
  /\ refused = FALSE
  /\ jpc = [j \in JobIds |-> "idle"]
  /\ jsel = [j \in JobIds |-> [s |-> 0, row |-> NoneRow]]
  /\ jnew = [j \in JobIds |-> Genesis]
  /\ jkey = [j \in JobIds |-> NoKey]
  /\ jrep = [j \in JobIds |-> 1]
  /\ jobFail = {}
  /\ qpc = IF RunVerifiers THEN "idle" ELSE "done"
  /\ qrows = EmptyF
  /\ qrA = EmptyF
  /\ wvpc = IF RunVerifiers THEN "idle" ELSE "done"
  /\ wvrows = EmptyF
  /\ alarms = {}
  /\ rcfg = [r \in Replicas |-> InitCfg]
  /\ dcfg = InitCfg
  /\ rotSteps = 0
  /\ rotPhase = "A"
  /\ tamper = 0

(***************************************************************************)
(* Appends: sealAndInsert under Append, AppendInTx, and AppendBatch        *)
(* (pkg/audit/auditstore). One action is one statement.                    *)
(***************************************************************************)
View(p) == Merge(log, wbuf[p])
ReleaseLock(p) == lockH' = IF lockH = p THEN 0 ELSE lockH

\* pg_advisory_xact_lock, the first statement of the transaction. The
\* bypass writer stands for an application path that skips the lock.
WLock(p) ==
  /\ wpc[p] = "idle" /\ wleft[p] > 0
  /\ \/ p \in BypassIds
     \/ lockH = 0 /\ lockH' = p
  /\ p \in BypassIds => UNCHANGED lockH
  /\ wpc' = [wpc EXCEPT ![p] = "locked"]
  /\ wbatch' = [wbatch EXCEPT ![p] = IF p \in JobIds THEN 1 ELSE BatchSize]
  /\ UNCHANGED <<log, rcpt, gLog, gRcpt, nseq, clock, sweeps, wleft, wtail, wlink,
                 wseq, wbuf, aborts, refused>>
  /\ UNCHANGED <<jvars, vvars, kvars, tamper>>

\* scanTail, first statement: the highest-sequence row the transaction sees.
WReadTail(p) ==
  /\ wpc[p] = "locked"
  /\ LET v == View(p) IN
       wtail' = [wtail EXCEPT ![p] =
                   IF DOMAIN v = {} THEN [has |-> FALSE, s |-> 0, row |-> NoneRow]
                   ELSE [has |-> TRUE, s |-> Max(DOMAIN v), row |-> v[Max(DOMAIN v)]]]
  /\ wpc' = [wpc EXCEPT ![p] = "tail"]
  /\ UNCHANGED <<log, rcpt, gLog, gRcpt, nseq, lockH, clock, sweeps, wleft, wbatch,
                 wlink, wseq, wbuf, aborts, refused>>
  /\ UNCHANGED <<jvars, vvars, kvars, tamper>>

\* scanTail, second statement under S3 (tailReceiptHash) for a
\* redaction-marked tail, then LinkHash.
WReadTailReceipt(p) ==
  /\ wpc[p] = "tail"
  /\ wlink' = [wlink EXCEPT ![p] =
                 IF ~wtail[p].has THEN Genesis
                 ELSE IF Variant = "S3" /\ wtail[p].row.m /\ wtail[p].s \in DOMAIN rcpt
                      THEN rcpt[wtail[p].s].orig
                      ELSE H(wtail[p].s, wtail[p].row)]
  /\ wpc' = [wpc EXCEPT ![p] = "rcpt"]
  /\ UNCHANGED <<log, rcpt, gLog, gRcpt, nseq, lockH, clock, sweeps, wleft, wbatch,
                 wtail, wseq, wbuf, aborts, refused>>
  /\ UNCHANGED <<jvars, vvars, kvars, tamper>>

\* SELECT nextval: outside the transaction's rollback.
WNextVal(p) ==
  /\ wpc[p] = "rcpt"
  /\ nseq <= MaxSeq
  /\ wseq' = [wseq EXCEPT ![p] = nseq]
  /\ nseq' = nseq + 1
  /\ wpc' = [wpc EXCEPT ![p] = "seq"]
  /\ UNCHANGED <<log, rcpt, gLog, gRcpt, lockH, clock, sweeps, wleft, wbatch, wtail,
                 wlink, wbuf, aborts, refused>>
  /\ UNCHANGED <<jvars, vvars, kvars, tamper>>

\* The INSERT. The row is visible to its own transaction only. It fails on
\* a committed row at the same key, and on the trigger's prev_hash check
\* when TriggerMode names one; the transaction then rolls back. A trigger
\* that checks prev_hash does so "under the same transaction-scoped lock"
\* (§11.7 item 3), so it takes the advisory lock first, which makes a
\* writer that skipped the lock wait here.
WInsert(p) ==
  /\ wpc[p] = "seq"
  /\ TriggerMode # "none" => lockH \in {0, p}
  /\ \E dl \in (IF p \in JobIds \/ NJobs = 0 THEN {FALSE} ELSE BOOLEAN) :
       LET r == Row(wlink[p], IF p \in JobIds THEN <<"g">> ELSE <<"e">>,
                    FALSE, p \in JobIds, dl, clock)
           ok == wseq[p] \notin DOMAIN log /\ TriggerAdmits(View(p), rcpt, r.prev)
       IN IF ok
          THEN /\ wbuf' = [wbuf EXCEPT ![p] = Put(wbuf[p], wseq[p], r)]
               /\ IF wbatch[p] > 1
                  THEN /\ wbatch' = [wbatch EXCEPT ![p] = wbatch[p] - 1]
                       /\ wpc' = [wpc EXCEPT ![p] = "locked"]
                  ELSE /\ wpc' = [wpc EXCEPT ![p] = "ins"]
                       /\ UNCHANGED wbatch
               /\ lockH' = IF TriggerMode # "none" THEN p ELSE lockH
               /\ UNCHANGED <<wleft, refused, jpc>>
          ELSE /\ wbuf' = [wbuf EXCEPT ![p] = EmptyF]
               /\ wpc' = [wpc EXCEPT ![p] = "idle"]
               /\ wleft' = [wleft EXCEPT ![p] = wleft[p] - 1]
               /\ ReleaseLock(p)
               /\ refused' = (refused \/ (p \notin BypassIds /\ wseq[p] \notin DOMAIN log))
               /\ jpc' = IF p \in JobIds THEN [jpc EXCEPT ![p] = "done"] ELSE jpc
               /\ UNCHANGED wbatch
  /\ UNCHANGED <<log, rcpt, gLog, gRcpt, nseq, clock, sweeps, wtail, wlink, wseq, aborts>>
  /\ UNCHANGED <<jsel, jnew, jkey, jrep, jobFail, vvars, kvars, tamper>>

\* COMMIT: the rows become visible at once and the lock is released.
WCommit(p) ==
  /\ wpc[p] = "ins"
  /\ log' = Merge(log, wbuf[p])
  /\ gLog' = Merge(gLog, wbuf[p])
  /\ ReleaseLock(p)
  /\ wbuf' = [wbuf EXCEPT ![p] = EmptyF]
  /\ wleft' = [wleft EXCEPT ![p] = wleft[p] - 1]
  /\ wpc' = [wpc EXCEPT ![p] = "idle"]
  /\ jpc' = IF p \in JobIds THEN [jpc EXCEPT ![p] = "done"] ELSE jpc
  /\ UNCHANGED <<rcpt, gRcpt, nseq, clock, sweeps, wbatch, wtail, wlink, wseq, aborts, refused>>
  /\ UNCHANGED <<jsel, jnew, jkey, jrep, jobFail, vvars, kvars, tamper>>

\* A failed statement or COMMIT, or a lost connection: ROLLBACK. The
\* sequence value already drawn is not returned.
WAbort(p) ==
  /\ wpc[p] \in {"locked", "tail", "rcpt", "seq", "ins"}
  /\ aborts < MaxAborts
  /\ aborts' = aborts + 1
  /\ wbuf' = [wbuf EXCEPT ![p] = EmptyF]
  /\ ReleaseLock(p)
  /\ wleft' = [wleft EXCEPT ![p] = wleft[p] - 1]
  /\ wpc' = [wpc EXCEPT ![p] = "idle"]
  /\ jpc' = IF p \in JobIds THEN [jpc EXCEPT ![p] = "done"] ELSE jpc
  /\ UNCHANGED <<log, rcpt, gLog, gRcpt, nseq, clock, sweeps, wbatch, wtail, wlink, wseq, refused>>
  /\ UNCHANGED <<jsel, jnew, jkey, jrep, jobFail, vvars, kvars, tamper>>

\* The sequence is exhausted for the model's bound: the writer stops.
WExhausted(p) ==
  /\ wpc[p] = "rcpt" /\ nseq > MaxSeq
  /\ wbuf' = [wbuf EXCEPT ![p] = EmptyF]
  /\ ReleaseLock(p)
  /\ wleft' = [wleft EXCEPT ![p] = 0]
  /\ wpc' = [wpc EXCEPT ![p] = "idle"]
  /\ jpc' = IF p \in JobIds THEN [jpc EXCEPT ![p] = "done"] ELSE jpc
  /\ UNCHANGED <<log, rcpt, gLog, gRcpt, nseq, clock, sweeps, wbatch, wtail, wlink, wseq, aborts, refused>>
  /\ UNCHANGED <<jsel, jnew, jkey, jrep, jobFail, vvars, kvars, tamper>>

(***************************************************************************)
(* Erasure jobs: RedactForUser and redactRow in                            *)
(* pkg/storage/deadletterredaction, RedactDeadLettered in                  *)
(* pkg/audit/auditstore/redaction.go.                                      *)
(***************************************************************************)
Selectable == {s \in DOMAIN log : log[s].dl /\ ~log[s].m /\ ~log[s].g}

\* DeadLetteredForUser: a dead-lettered row without the marker; its hash is
\* the receipt's original_hash. The job runs on one replica.
JSelect(j) ==
  /\ jpc[j] = "idle"
  /\ IF Selectable = {}
     THEN /\ jpc' = [jpc EXCEPT ![j] = "done"]
          /\ UNCHANGED <<jsel, jrep>>
     ELSE \E s \in Selectable, r \in Replicas :
            /\ jsel' = [jsel EXCEPT ![j] = [s |-> s, row |-> log[s]]]
            /\ jrep' = [jrep EXCEPT ![j] = r]
            /\ jpc' = [jpc EXCEPT ![j] = "sel"]
  /\ UNCHANGED <<log, rcpt, gLog, gRcpt, nseq, lockH, clock, sweeps, wvars>>
  /\ UNCHANGED <<jnew, jkey, jobFail, vvars, kvars, tamper>>

\* signReceipt: KMS Sign under the key platform:audit-signing names on the
\* job's replica.
JSign(j) ==
  /\ jpc[j] = "sel"
  /\ jnew' = [jnew EXCEPT ![j] = H(jsel[j].s, Redacted(jsel[j].row, j))]
  /\ jkey' = [jkey EXCEPT ![j] = rcfg[jrep[j]].sign]
  /\ jpc' = [jpc EXCEPT ![j] = "signed"]
  /\ UNCHANGED <<log, rcpt, gLog, gRcpt, nseq, lockH, clock, sweeps, wvars>>
  /\ UNCHANGED <<jsel, jrep, jobFail, vvars, kvars, tamper>>

\* RedactDeadLettered: the payload UPDATE and the receipt INSERT in one
\* transaction. The UPDATE finds no row once the row is gone (ErrNotFound);
\* the INSERT fails on the (tenant_id, sequence_number) uniqueness once
\* another job redacted the row (AUDIT-I7). Then the job appends its
\* gdpr.erasure_deadletter_redacted row through the append path.
JRedact(j) ==
  /\ jpc[j] = "signed"
  /\ LET s == jsel[j].s IN
     IF s \notin DOMAIN log
     THEN /\ jpc' = [jpc EXCEPT ![j] = "failed"]
          /\ jobFail' = jobFail \cup {"notfound"}
          /\ UNCHANGED <<log, rcpt, gLog, gRcpt, wleft>>
     ELSE IF s \in DOMAIN rcpt
     THEN /\ jpc' = [jpc EXCEPT ![j] = "failed"]
          /\ jobFail' = jobFail \cup {"unique"}
          /\ UNCHANGED <<log, rcpt, gLog, gRcpt, wleft>>
     ELSE IF RedactionGuard /\ ~(log[s].dl /\ ~log[s].m)
     THEN /\ jpc' = [jpc EXCEPT ![j] = "failed"]
          /\ jobFail' = jobFail \cup {"guard"}
          /\ UNCHANGED <<log, rcpt, gLog, gRcpt, wleft>>
     ELSE LET rec == [orig |-> H(s, jsel[j].row), new |-> jnew[j],
                      key |-> jkey[j], signed |-> TRUE]
          IN /\ log' = Put(log, s, Redacted(log[s], j))
             /\ rcpt' = Put(rcpt, s, rec)
             /\ gLog' = IF s \in DOMAIN gLog THEN Put(gLog, s, Redacted(gLog[s], j)) ELSE gLog
             /\ gRcpt' = Put(gRcpt, s, rec)
             /\ wleft' = [wleft EXCEPT ![j] = 1]
             /\ jpc' = [jpc EXCEPT ![j] = "app"]
             /\ UNCHANGED jobFail
  /\ UNCHANGED <<nseq, lockH, clock, sweeps, wpc, wbatch, wtail, wlink, wseq, wbuf, aborts, refused>>
  /\ UNCHANGED <<jsel, jnew, jkey, jrep, vvars, kvars, tamper>>

(***************************************************************************)
(* Retention: PruneRetention, one DELETE statement. It deletes the rows    *)
(* that are not gdpr.* and whose created_at is older than the cutoff; the  *)
(* gdpr.* rows are kept for audit.gdprRetentionDays, longer than the model *)
(* runs. The SIEM delivery guard is left out.                              *)
(***************************************************************************)
Tick ==
  /\ clock < MaxTime
  /\ clock' = clock + 1
  /\ UNCHANGED <<log, rcpt, gLog, gRcpt, nseq, lockH, sweeps, wvars, jvars, vvars, kvars, tamper>>

Swept(f, c) == [s \in {x \in DOMAIN f : f[x].g \/ f[x].ts >= c} |-> f[s]]

Sweep ==
  /\ sweeps < MaxSweeps
  /\ \E c \in 1..clock :
       /\ log' = Swept(log, c)
       /\ gLog' = Swept(gLog, c)
  /\ sweeps' = sweeps + 1
  /\ UNCHANGED <<rcpt, gRcpt, nseq, lockH, clock, wvars, jvars, vvars, kvars, tamper>>

(***************************************************************************)
(* The verifiers that read in more than one statement. The audit query     *)
(* API (handleListAuditEvents) reads the rows (Store.Rows), the receipts   *)
(* for applyReceipts, and the receipts again (auditReceipts), then runs    *)
(* VerifyRows. The periodic window check reads the newest WindowN rows,    *)
(* then the receipts (applyChainReceipts). Each runs once per behavior,    *)
(* on replica 1, at any point.                                             *)
(***************************************************************************)
QReadRows ==
  /\ qpc = "idle" /\ qrows' = log /\ qpc' = "rows"
  /\ UNCHANGED <<log, rcpt, gLog, gRcpt, nseq, lockH, clock, sweeps, wvars, jvars>>
  /\ UNCHANGED <<qrA, wvpc, wvrows, alarms, kvars, tamper>>
QReadRcptA ==
  /\ qpc = "rows" /\ qrA' = rcpt /\ qpc' = "rcptA"
  /\ UNCHANGED <<log, rcpt, gLog, gRcpt, nseq, lockH, clock, sweeps, wvars, jvars>>
  /\ UNCHANGED <<qrows, wvpc, wvrows, alarms, kvars, tamper>>
QReadRcptB ==
  /\ qpc = "rcptA" /\ qpc' = "done"
  /\ alarms' = IF QBroken(qrows, qrA, rcpt, Trusted(1)) THEN alarms \cup {"query"} ELSE alarms
  /\ UNCHANGED <<log, rcpt, gLog, gRcpt, nseq, lockH, clock, sweeps, wvars, jvars>>
  /\ UNCHANGED <<qrows, qrA, wvpc, wvrows, kvars, tamper>>
WvReadRows ==
  /\ wvpc = "idle" /\ wvpc' = "rows"
  /\ wvrows' = [s \in WindowOf(log) |-> log[s]]
  /\ UNCHANGED <<log, rcpt, gLog, gRcpt, nseq, lockH, clock, sweeps, wvars, jvars>>
  /\ UNCHANGED <<qpc, qrows, qrA, alarms, kvars, tamper>>
WvReadRcpt ==
  /\ wvpc = "rows" /\ wvpc' = "done"
  /\ alarms' = IF WBroken(wvrows, rcpt) THEN alarms \cup {"window"} ELSE alarms
  /\ UNCHANGED <<log, rcpt, gLog, gRcpt, nseq, lockH, clock, sweeps, wvars, jvars>>
  /\ UNCHANGED <<qpc, qrows, qrA, wvrows, kvars, tamper>>

(***************************************************************************)
(* Receipt key rotation. Each replica loads its alias map and              *)
(* audit.retiredReceiptKeyIds at start (buildReceiptSigning) and holds     *)
(* them until it restarts. The operator changes the desired configuration  *)
(* once every replica runs the previous one, and replicas restart one at a *)
(* time, never while one of their jobs is between selecting and           *)
(* committing a redaction.                                                 *)
(*                                                                         *)
(* OneRollout is the procedure of docs/operator-guide/security.md: map     *)
(* platform:audit-signing to the new key and platform:audit-verify to the  *)
(* previous one in one rollout. VerifyFirst is the order of the §10.2 JWT  *)
(* rotation that §12.8 names: map platform:audit-verify to the new key     *)
(* first, then swap the two mappings.                                      *)
(***************************************************************************)
Retire(c) == IF ListRetired /\ c.verify # NoKey THEN c.retired \cup {c.verify} ELSE c.retired

NextCfg(c) ==
  IF Rotation = "OneRollout"
  THEN [sign |-> NextKey(c.sign), verify |-> c.sign, retired |-> Retire(c)]
  ELSE IF rotPhase = "A"
       THEN [sign |-> c.sign, verify |-> NextKey(c.sign), retired |-> Retire(c)]
       ELSE [sign |-> c.verify, verify |-> c.sign, retired |-> c.retired]

OperatorStep ==
  /\ Rotation # "none"
  /\ rotSteps < MaxRotSteps
  /\ \A r \in Replicas : rcfg[r] = dcfg
  /\ dcfg' = NextCfg(dcfg)
  /\ rotSteps' = rotSteps + 1
  /\ rotPhase' = IF Rotation = "VerifyFirst" /\ rotPhase = "A" THEN "B" ELSE "A"
  /\ UNCHANGED <<log, rcpt, gLog, gRcpt, nseq, lockH, clock, sweeps, wvars, jvars, vvars, rcfg, tamper>>

Restart(r) ==
  /\ rcfg[r] # dcfg
  /\ \A j \in JobIds : ~(jrep[j] = r /\ jpc[j] \in {"sel", "signed"})
  /\ rcfg' = [rcfg EXCEPT ![r] = dcfg]
  /\ UNCHANGED <<log, rcpt, gLog, gRcpt, nseq, lockH, clock, sweeps, wvars, jvars, vvars>>
  /\ UNCHANGED <<dcfg, rotSteps, rotPhase, tamper>>

(***************************************************************************)
(* The adversary. Each action is one statement a role can run, bounded by  *)
(* MaxTamper. The ghost tables gLog and gRcpt do not change.               *)
(*                                                                         *)
(*   app      lenny_app: INSERT any audit_log row, UPDATE                  *)
(*            ocsf_translation_state, SELECT the receipts.                 *)
(*   erasure  lenny_erasure: UPDATE (payload, payload_canonical_json),     *)
(*            DELETE, INSERT of gdpr.* rows, INSERT of receipts, and       *)
(*            SET LOCAL lenny.erasure_mode.                                *)
(*   signer   lenny_erasure and the KMS Sign grant on                      *)
(*            platform:audit-signing: the erasure job's own authority.     *)
(*   dba      a database superuser: any statement on any column, with the  *)
(*            triggers disabled. No KMS grant.                             *)
(***************************************************************************)
CanInsertAny == Adversary \in {"app", "dba"}
CanInsertGdpr == Adversary \in {"erasure", "signer"}
CanUpdatePayload == Adversary \in {"erasure", "signer", "dba"}
CanDelete == Adversary \in {"erasure", "signer", "dba"}
CanInsertRcpt == Adversary \in {"erasure", "signer", "dba"}
CanMarkDL == Adversary \in {"app", "dba"}
IsDBA == Adversary = "dba"
Pending == UNION {DOMAIN wbuf[p] : p \in Procs}

CandPrev == {Genesis} \cup {H(x, log[x]) : x \in DOMAIN log} \cup {rcpt[x].orig : x \in DOMAIN rcpt}
Contents == {<<"e">>, <<"f">>}

TamperStep == tamper < MaxTamper /\ tamper' = tamper + 1
AdvUnchanged == UNCHANGED <<gLog, gRcpt, nseq, lockH, clock, sweeps, wvars, jvars, vvars, kvars>>

AInsert ==
  /\ CanInsertAny \/ CanInsertGdpr
  /\ TamperStep
  /\ \E s \in Seqs \ (DOMAIN log \cup Pending), prev \in CandPrev, m \in BOOLEAN,
        g \in (IF CanInsertAny THEN BOOLEAN ELSE {TRUE}) :
       /\ IsDBA \/ TriggerAdmits(log, rcpt, prev)
       /\ log' = Put(log, s, Row(prev, IF g THEN <<"g">> ELSE <<"f">>, m, g, FALSE, clock))
  /\ UNCHANGED rcpt /\ AdvUnchanged

AUpdate ==
  /\ CanUpdatePayload
  /\ TamperStep
  /\ \E s \in DOMAIN log, c \in Contents, m \in BOOLEAN :
       /\ <<c, m>> # <<log[s].c, log[s].m>>
       /\ (RedactionGuard /\ ~IsDBA) => (log[s].dl /\ ~log[s].m /\ m)
       /\ log' = Put(log, s, [log[s] EXCEPT !.c = c, !.m = m])
  /\ UNCHANGED rcpt /\ AdvUnchanged

ARelink ==
  /\ IsDBA
  /\ TamperStep
  /\ \E s \in DOMAIN log, prev \in CandPrev :
       /\ prev # log[s].prev
       /\ log' = Put(log, s, [log[s] EXCEPT !.prev = prev])
  /\ UNCHANGED rcpt /\ AdvUnchanged

ADelete ==
  /\ CanDelete
  /\ TamperStep
  /\ \E s \in DOMAIN log : log' = Del(log, s)
  /\ UNCHANGED rcpt /\ AdvUnchanged

\* A receipt for a present row. The adversary signs with its own key
\* (valid signature, untrusted key), or names a platform key without a
\* valid signature, or, as the signer, signs with the key
\* platform:audit-signing names.
AInsertRcpt ==
  /\ CanInsertRcpt
  /\ TamperStep
  /\ \E s \in DOMAIN log \ DOMAIN rcpt :
       \E orig \in {H(s, log[s])} \cup {log[x].prev : x \in Above(log, s)},
          kc \in {<<AdvKey, TRUE>>, <<rcfg[1].sign, Adversary = "signer">>} :
         rcpt' = Put(rcpt, s, [orig |-> orig, new |-> H(s, log[s]), key |-> kc[1], signed |-> kc[2]])
  /\ UNCHANGED log /\ AdvUnchanged

ADeleteRcpt ==
  /\ IsDBA
  /\ TamperStep
  /\ \E s \in DOMAIN rcpt : rcpt' = Del(rcpt, s)
  /\ UNCHANGED log /\ AdvUnchanged

AMarkDL ==
  /\ CanMarkDL
  /\ TamperStep
  /\ \E s \in DOMAIN log :
       /\ ~log[s].dl /\ ~log[s].g
       /\ log' = Put(log, s, [log[s] EXCEPT !.dl = TRUE])
  /\ UNCHANGED rcpt /\ AdvUnchanged

Adv == AInsert \/ AUpdate \/ ARelink \/ ADelete \/ AInsertRcpt \/ ADeleteRcpt \/ AMarkDL

Next ==
  \/ \E p \in Procs : WLock(p) \/ WReadTail(p) \/ WReadTailReceipt(p) \/ WNextVal(p)
                      \/ WInsert(p) \/ WCommit(p) \/ WAbort(p) \/ WExhausted(p)
  \/ \E j \in JobIds : JSelect(j) \/ JSign(j) \/ JRedact(j)
  \/ Tick \/ Sweep
  \/ QReadRows \/ QReadRcptA \/ QReadRcptB \/ WvReadRows \/ WvReadRcpt
  \/ OperatorStep \/ \E r \in Replicas : Restart(r)
  \/ (Adversary # "none" /\ Adv)

Spec == Init /\ [][Next]_vars

(***************************************************************************)
(* Properties.                                                             *)
(***************************************************************************)
TypeOK ==
  /\ DOMAIN log \subseteq Seqs
  /\ DOMAIN rcpt \subseteq Seqs
  /\ lockH \in Procs \cup {0}
  /\ nseq \in 1..(MaxSeq + 1)
  /\ alarms \subseteq {"query", "window"}

\* P1. Concurrent appends keep one linear chain: every row links to the
\* row before it in sequence order (the receipt's original_hash for a
\* redacted row), and no two rows carry the same prev_hash.
LinearChain == \A s \in DOMAIN log : QLinkOK(log, rcpt, s)
NoFork == \A a, b \in DOMAIN log : a # b => log[a].prev # log[b].prev

\* P6. Without an adversary no verifier reports a break: no verifier run
\* raised an alarm, and on every replica the chain verifier, the window
\* check, and the receipt scan accept the committed tables.
NoFalseAlarm ==
  /\ alarms = {}
  /\ \A r \in Replicas : ~Detected(Trusted(r))

\* NoFalseAlarm split by verifier, so a configuration names the one it
\* checks: the query verifier (Chain.VerifyRows, Chain.Verify), the
\* periodic window check, and the periodic receipt scan.
NoQueryFalseAlarm ==
  /\ "query" \notin alarms
  /\ \A r \in Replicas : ~QBroken(log, rcpt, rcpt, Trusted(r))
NoWindowFalseAlarm == "window" \notin alarms /\ ~WBroken(log, rcpt)
NoScanFalseAlarm == \A r \in Replicas : Missing(log, rcpt, Trusted(r)) = {}

\* No append by a writer that holds the lock is refused by the trigger.
NoRefusedAppend == ~refused

\* A job never fails on a row another job redacted first (AUDIT-I7).
NoJobFailsOnRedactedRow == "unique" \notin jobFail

\* P4. A receipt that does not verify under a trusted key, and P3, a
\* receipt whose new_hash differs from the row's content hash, make the
\* row read broken in the chain verifier and count in the receipt scan.
ForgedReceiptRefused ==
  \A s \in DOMAIN log \cap DOMAIN rcpt :
    (~rcpt[s].signed \/ rcpt[s].key \notin Trusted(1)) =>
      (s \in Missing(log, rcpt, Trusted(1)) /\ QRowBroken(log, rcpt, rcpt, Trusted(1), s))
RedactionBoundToNewHash ==
  \A s \in DOMAIN log \cap DOMAIN rcpt :
    rcpt[s].new # H(s, log[s]) =>
      (s \in Missing(log, rcpt, Trusted(1)) /\ QRowBroken(log, rcpt, rcpt, Trusted(1), s))

\* P5. Every receipt in the table verifies under the trusted keys of every
\* replica, across rotations.
ReceiptKeysTrusted ==
  \A r \in Replicas : \A s \in DOMAIN rcpt : rcpt[s].key \in Trusted(r)

(***************************************************************************)
(* P2. Tampering is detected. The effects of the adversary are read off    *)
(* the difference between the tables and the ghost tables, and each is    *)
(* classified by what the verifiers can see of it:                         *)
(*                                                                         *)
(*   insertLinked          an added row, or a row replaced at its sequence *)
(*                         number with other prev_hash, event type, or     *)
(*                         created_at, that links to its predecessor and   *)
(*                         that its successor, if any, links to            *)
(*   insertOther           any other added or replaced row                 *)
(*   deleteTail            a removed row with no row after it              *)
(*   deleteHead            a removed row with no row before it             *)
(*   deleteLinkedOver      a removed row whose next row links to the row   *)
(*                         before the gap (a tail truncation followed by   *)
(*                         an append reads as a nextval gap)               *)
(*   deleteOther           any other removed row                           *)
(*   rewritePlainUnpinned  a row whose payload alone changed, with no      *)
(*                         marker and no receipt, whose successor, if any, *)
(*                         links to its current content                    *)
(*   rewriteReceipted      a row whose payload alone changed, or any row,  *)
(*                         carrying a receipt the ghost lacks that         *)
(*                         verifies under a trusted key                    *)
(*   rewriteOther          any other row whose payload alone changed       *)
(*   receiptOther          any other receipt added or changed              *)
(*                                                                         *)
(* Accepted lists the effects the configuration states as limits. The     *)
(* property requires that while any other effect is present, one of the   *)
(* verifiers reports it.                                                   *)
(***************************************************************************)
HashedEq(a, b) == a.prev = b.prev /\ a.c = b.c /\ a.m = b.m /\ a.g = b.g /\ a.ts = b.ts
Xs == DOMAIN log \ DOMAIN gLog
Ms == DOMAIN gLog \ DOMAIN log
Cs == {s \in DOMAIN log \cap DOMAIN gLog : ~HashedEq(log[s], gLog[s])}
RcptDiff == {s \in DOMAIN log :
               \/ (s \in DOMAIN rcpt) # (s \in DOMAIN gRcpt)
               \/ (s \in DOMAIN rcpt \cap DOMAIN gRcpt /\ rcpt[s] # gRcpt[s])}
\* Changed rows whose prev_hash, event type, and created_at are the ghost's:
\* only the payload changed. Any other change at a sequence number is a row
\* removed and another inserted in its place, and is classified as an insert.
PayloadOnly == {s \in Cs : log[s].prev = gLog[s].prev /\ log[s].g = gLog[s].g /\ log[s].ts = gLog[s].ts}
LinkedIn(s) == QLinkOK(log, rcpt, s)
LinkedOut(s) == Above(log, s) = {} \/ log[Min(Above(log, s))].prev = VLink(log, rcpt, s)

Effects ==
  {IF LinkedIn(s) /\ LinkedOut(s) THEN "insertLinked" ELSE "insertOther" : s \in Xs}
  \cup
  {IF {x \in DOMAIN log : x > s} = {} THEN "deleteTail"
   ELSE IF {x \in DOMAIN log : x < s} = {} THEN "deleteHead"
   ELSE IF LinkedIn(Min({x \in DOMAIN log : x > s})) THEN "deleteLinkedOver"
   ELSE "deleteOther" : s \in Ms}
  \cup
  {IF LinkedIn(s) /\ LinkedOut(s) THEN "insertLinked" ELSE "insertOther" : s \in Cs \ PayloadOnly}
  \cup
  {IF ~log[s].m /\ s \notin DOMAIN rcpt /\ LinkedOut(s) THEN "rewritePlainUnpinned"
   ELSE IF s \in RcptDiff /\ s \in DOMAIN rcpt /\ rcpt[s].signed /\ rcpt[s].key \in Trusted(1)
        THEN "rewriteReceipted"
   ELSE "rewriteOther" : s \in Cs \cap PayloadOnly}
  \cup
  {IF s \in DOMAIN rcpt /\ rcpt[s].signed /\ rcpt[s].key \in Trusted(1)
   THEN "rewriteReceipted" ELSE "receiptOther" : s \in RcptDiff \ Cs}

TamperDetected == (Effects \ Accepted) # {} => Detected(Trusted(1))

\* The gap the erasure grant leaves (G1), stated as its own invariant: a
\* rewrite of a row that carries no marker and no receipt and that no later
\* row pins to its earlier hash is detected.
PlainRewriteDetected == "rewritePlainUnpinned" \in Effects => Detected(Trusted(1))

\* TamperDetected with the rewrite of PlainRewriteDetected also accepted, so
\* that a run shows whether any other tampering escapes.
TamperDetectedExceptPlainRewrite ==
  (Effects \ (Accepted \cup {"rewritePlainUnpinned"})) # {} => Detected(Trusted(1))

\* P2 for a superuser, who can rebuild the chain and whom §11.7 accepts as
\* undetectable without the SIEM copy: a row at or before a redacted row
\* whose legitimate receipt is still in place cannot be changed, added, or
\* removed (other than from the head of the chain) without detection.
Anchors == {s \in DOMAIN log \cap DOMAIN rcpt \cap DOMAIN gRcpt : rcpt[s] = gRcpt[s] /\ log[s].m}
AnchoredByReceipt ==
  \A a \in Anchors :
    LET bad == {s \in Cs \cup Xs : s <= a} \cup {s \in Ms : s < a /\ Below(log, s) # {}}
    IN bad # {} => Detected(Trusted(1))

\* Every redaction with a legitimate receipt rewrote a row that the
\* translator dead-lettered.
RedactedOnlyTranslatorDeadLetters ==
  \A s \in DOMAIN gRcpt \cap DOMAIN gLog : s \notin InitRedacted => gLog[s].dl
=============================================================================
