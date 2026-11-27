---------------------------- MODULE UploadTokens ----------------------------
(***************************************************************************)
(* A model of the upload-token signing keys and the consumed state of     *)
(* section 7.1, as change 0177 builds them through its decision 11.       *)
(* Change 0179 builds the model; README.md in this directory maps each    *)
(* action to the code and states each property in plain words.           *)
(*                                                                         *)
(* Time is a discrete clock, `now`, that advances by one tick in `Tick`.   *)
(* Every duration is a constant counted in ticks. Several steps can run    *)
(* at one instant, in any order. A read or write of Postgres fails while  *)
(* the replica's link to Postgres is cut, and a key write or an unwrap of *)
(* a key the replica does not hold also fails while its link to the KMS   *)
(* is cut.                                                                 *)
(***************************************************************************)
EXTENDS Integers, FiniteSets, TLC

CONSTANTS
    Replicas,       \* gateway replicas
    Sessions,       \* session ids a client can create
    Refresh,        \* --signing-key-refresh-interval
    Lead,           \* --signing-key-publish-lead
    Overlap,        \* uploadtoken.DefaultOverlapWindow; also the stale bound
    Lifetime,       \* maxCreatedStateTimeoutSeconds, the token lifetime
    RotInt,         \* --upload-token-key-rotation-interval
    MaxTime,        \* last instant of a behavior
    MaxKeys,        \* key writes per behavior
    FaultKinds,     \* subset of {"pg", "kms"}
    MaxFaults,      \* link cuts per behavior
    MaxRestarts,    \* replica restarts per behavior
    MaxForced,      \* POST /v1/admin/upload-token-keys/rotate calls
    MaxRowDeletes,  \* rows of platform_signing_keys an operator deletes
    StartSessions,  \* sessions created by POST /v1/sessions/start
    FinalizeReplicas, \* replicas that serve finalize requests; with none,
                      \* no finalize and no terminate runs
    NoTime,         \* model value: an unset instant
    NoRow           \* model value: no session row, no mint record

ASSUME Refresh >= 1 /\ Lead >= 1 /\ Overlap >= 1 /\ Lifetime >= 1 /\ RotInt >= 1
ASSUME StartSessions \subseteq Sessions
ASSUME FinalizeReplicas \subseteq Replicas
ASSUME FaultKinds \subseteq {"pg", "kms"}

Max(a, b) == IF a >= b THEN a ELSE b

\* The ring's stale bound (Options.StaleAfter) is the overlap window.
Stale == Overlap
\* signingkeys.UploadTokenVerificationTime: overlap + max(overlap, lifetime).
VerifyFor == Overlap + Max(Overlap, Lifetime)

KeyIds == 1..MaxKeys

\* signingkeys.earlier, and the LEAST(COALESCE(...)) of the Postgres
\* Insert: an unset stored instant yields next.
Earlier(stored, next) ==
    IF stored = NoTime THEN next ELSE IF next < stored THEN next ELSE stored

NewRec(id, slot, act) ==
    [id |-> id, slot |-> slot, act |-> act, ret |-> NoTime, del |-> NoTime]

VARIABLES
    now,
    \* rows of platform_signing_keys of the upload-token purpose
    table,
    nextId,
    \* history of each key id: the instant its row was written, and the last
    \* retire_after and delete_after the table held for it
    keyW, keyRet, keyDel,
    \* per replica: "down" or "up", the key snapshot of the last successful
    \* reload, its instant, the instant of the last refresh or start attempt,
    \* and the state of its links
    pc, ring, lastReload, lastAttempt, fault,
    \* budgets
    faultsLeft, restartsLeft, forcedLeft, deletesLeft,
    \* sessions: the row, the mint record, the finalize request of each
    \* replica for each session, and the finalizes that reached ready
    row, minted, finPc, finOK

keyVars == <<table, nextId, keyW, keyRet, keyDel>>
repVars == <<pc, ring, lastReload, lastAttempt, fault>>
budgetVars == <<faultsLeft, restartsLeft, forcedLeft, deletesLeft>>
sessVars == <<row, minted, finPc, finOK>>
vars == <<now, keyVars, repVars, budgetVars, sessVars>>

Up(r) == pc[r] = "up"

\* The link checks: a key write seals with the KMS and inserts in Postgres;
\* a read needs Postgres.
WriteOK(r) == fault[r] = "ok"
ReadOK(r) == fault[r] # "pg"

\* Store.List over a table: the rows whose delete_after is unset or after now.
ListOf(tbl) == {x \in tbl : x.del = NoTime \/ x.del > now}

HeldIn(keys) == {x.id : x \in keys}
Held(r) == HeldIn(ring[r])

\* Ring.reload against tbl from a snapshot `keys`: a failed read, or a
\* failed unwrap of a key the snapshot does not hold, leaves the snapshot
\* and the reload instant unchanged.
ReloadOKFor(r, keys, tbl) ==
    /\ ReadOK(r)
    /\ (fault[r] = "kms" => \A x \in ListOf(tbl) : x.id \in HeldIn(keys))

Newest(keys) == CHOOSE x \in keys : \A y \in keys : y.slot <= x.slot

\* Ring.SigningKey over a snapshot: the newest key whose activation instant
\* has passed and whose retirement instant has not.
Signable(keys) ==
    {x \in keys : x.act <= now /\ ~(x.ret # NoTime /\ now >= x.ret)}
Fresh(r) == lastReload[r] # NoTime /\ now - lastReload[r] <= Stale
SigningKeyOK(r) == Fresh(r) /\ Signable(ring[r]) # {}
SigningKey(r) == Newest(Signable(ring[r]))

\* Ring.VerificationKeys: every held key whose delete_after has not passed.
VerKeys(r) == {x \in ring[r] : x.del = NoTime \/ x.del > now}
HMACKey(r, k) == \E x \in VerKeys(r) : x.id = k

AwaitingActivation(keys) == \E x \in keys : x.act > now

(***************************************************************************)
(* Store.Insert as a function of the table: writes a key at a slot and,   *)
(* in the same transaction, retires the key at the slot before it,        *)
(* keeping an earlier retire_after and delete_after. A taken slot changes *)
(* nothing. A write past the key budget fails, as a write refused by      *)
(* Postgres does. W is [res, tbl, nid].                                    *)
(***************************************************************************)
WriteOn(r, tbl, nid, slot, act) ==
    LET res == IF ~WriteOK(r) THEN "fail"
               ELSE IF \E x \in tbl : x.slot = slot THEN "conflict"
               ELSE IF nid > MaxKeys THEN "fail"
               ELSE "inserted"
        upd(x) == IF x.slot = slot - 1
                  THEN [x EXCEPT !.ret = Earlier(x.ret, act),
                                 !.del = Earlier(x.del, act + VerifyFor)]
                  ELSE x
    IN IF res = "inserted"
       THEN [res |-> res, tbl |-> {upd(x) : x \in tbl} \cup {NewRec(nid, slot, act)},
             nid |-> nid + 1]
       ELSE [res |-> res, tbl |-> tbl, nid |-> nid]

NoWrite(tbl, nid) == [res |-> "none", tbl |-> tbl, nid |-> nid]

(***************************************************************************)
(* Ring.rotateIfDue over a snapshot `keys` whose last reload instant is   *)
(* `lr`: a first key backdated by the lead when a reloaded ring is empty; *)
(* the next slot one lead ahead when the newest key carries a retirement  *)
(* instant; nothing when the newest key is not active or not due; and     *)
(* otherwise the next slot one lead ahead.                                 *)
(***************************************************************************)
RotateOn(r, keys, lr, tbl, nid) ==
    IF keys = {} THEN
        IF lr # NoTime THEN WriteOn(r, tbl, nid, 1, now - Lead) ELSE NoWrite(tbl, nid)
    ELSE LET n == Newest(keys) IN
        IF n.ret # NoTime THEN WriteOn(r, tbl, nid, n.slot + 1, now + Lead)
        ELSE IF n.act > now \/ now < n.act + RotInt THEN NoWrite(tbl, nid)
        ELSE WriteOn(r, tbl, nid, n.slot + 1, now + Lead)

\* Store.DeleteExpired as a function of the table.
SweepOn(r, tbl) == IF ReadOK(r) THEN ListOf(tbl) ELSE tbl

\* Commit a new table. The history of each key follows the table hist, the
\* table after the writes and before any sweep.
CommitKeys(hist, final, nid) ==
    /\ table' = final
    /\ nextId' = nid
    /\ keyW' = [k \in KeyIds |-> IF k >= nextId /\ k < nid THEN now ELSE keyW[k]]
    /\ keyRet' = [k \in KeyIds |->
                    IF \E x \in hist : x.id = k
                    THEN (CHOOSE x \in hist : x.id = k).ret ELSE keyRet[k]]
    /\ keyDel' = [k \in KeyIds |->
                    IF \E x \in hist : x.id = k
                    THEN (CHOOSE x \in hist : x.id = k).del ELSE keyDel[k]]

Init ==
    /\ now = 0
    /\ table = {}
    /\ nextId = 1
    /\ keyW = [k \in KeyIds |-> NoTime]
    /\ keyRet = [k \in KeyIds |-> NoTime]
    /\ keyDel = [k \in KeyIds |-> NoTime]
    /\ pc = [r \in Replicas |-> "down"]
    /\ ring = [r \in Replicas |-> {}]
    /\ lastReload = [r \in Replicas |-> NoTime]
    /\ lastAttempt = [r \in Replicas |-> -1]
    /\ fault = [r \in Replicas |-> "ok"]
    /\ faultsLeft = MaxFaults
    /\ restartsLeft = MaxRestarts
    /\ forcedLeft = MaxForced
    /\ deletesLeft = MaxRowDeletes
    /\ row = [s \in Sessions |-> NoRow]
    /\ minted = [s \in Sessions |-> NoRow]
    /\ finPc = [r \in Replicas |-> [s \in Sessions |-> "none"]]
    /\ finOK = [s \in Sessions |-> 0]

(***************************************************************************)
(* signingkeys.Open, at process start: list the keys; when the purpose    *)
(* has none, write the first key backdated by the publish lead; reload;   *)
(* when no key is active, rotateIfDue and reload again. The process stops *)
(* when a step fails or when no key is active or awaiting activation.     *)
(* A stopped replica whose links are up starts again within the instant.  *)
(***************************************************************************)
Start(r) ==
    /\ pc[r] = "down"
    /\ lastAttempt[r] < now
    /\ lastAttempt' = [lastAttempt EXCEPT ![r] = now]
    /\ LET w1 == IF ReadOK(r) /\ ListOf(table) = {}
                 THEN WriteOn(r, table, nextId, 1, now - Lead)
                 ELSE NoWrite(table, nextId)
           ok1 == ReadOK(r) /\ w1.res # "fail" /\ ReloadOKFor(r, {}, w1.tbl)
           k1 == ListOf(w1.tbl)
           needRot == Signable(k1) = {}
           w2 == IF ok1 /\ needRot THEN RotateOn(r, k1, now, w1.tbl, w1.nid) ELSE w1
           ok2 == ~needRot \/ (w2.res # "fail" /\ ReloadOKFor(r, k1, w2.tbl))
           k2 == ListOf(w2.tbl)
           ok == ok1 /\ ok2 /\ (Signable(k2) # {} \/ AwaitingActivation(k2))
       IN /\ CommitKeys(w2.tbl, w2.tbl, w2.nid)
          /\ IF ok
             THEN /\ pc' = [pc EXCEPT ![r] = "up"]
                  /\ ring' = [ring EXCEPT ![r] = k2]
                  /\ lastReload' = [lastReload EXCEPT ![r] = now]
             ELSE UNCHANGED <<pc, ring, lastReload>>
    /\ UNCHANGED <<now, fault, budgetVars, sessVars>>

(***************************************************************************)
(* Ring.Refresh, run by Ring.Run on its ticker: rotateIfDue over the      *)
(* replica's snapshot, the sweep (DeleteExpired), and the reload. A       *)
(* failed part is logged and the next part runs. Only a successful reload *)
(* moves lastReload.                                                       *)
(***************************************************************************)
RefreshRing(r) ==
    /\ Up(r)
    /\ lastAttempt[r] < now
    /\ lastAttempt' = [lastAttempt EXCEPT ![r] = now]
    /\ LET w == RotateOn(r, ring[r], lastReload[r], table, nextId)
           swept == SweepOn(r, w.tbl)
       IN /\ CommitKeys(w.tbl, swept, w.nid)
          /\ IF ReloadOKFor(r, ring[r], swept)
             THEN /\ ring' = [ring EXCEPT ![r] = ListOf(swept)]
                  /\ lastReload' = [lastReload EXCEPT ![r] = now]
             ELSE UNCHANGED <<ring, lastReload>>
    /\ UNCHANGED <<now, pc, fault, budgetVars, sessVars>>

(***************************************************************************)
(* Ring.RotateNow, behind POST /v1/admin/upload-token-keys/rotate: reload, *)
(* write the slot after the newest key one publish lead ahead, and reload. *)
(* refreshMu serializes it with Refresh. A slot another writer takes      *)
(* between the reload and the write sends the code to the next slot; the  *)
(* model reaches the same table by ordering that writer's step first.     *)
(***************************************************************************)
ForcedRotate(r) ==
    /\ Up(r)
    /\ forcedLeft > 0
    /\ forcedLeft' = forcedLeft - 1
    /\ IF ReloadOKFor(r, ring[r], table)
       THEN LET k0 == ListOf(table)
                slot == IF k0 = {} THEN 1 ELSE Newest(k0).slot + 1
                w == WriteOn(r, table, nextId, slot, now + Lead)
                ok2 == w.res = "inserted" /\ ReloadOKFor(r, k0, w.tbl)
            IN /\ CommitKeys(w.tbl, w.tbl, w.nid)
               /\ ring' = [ring EXCEPT ![r] = IF ok2 THEN ListOf(w.tbl) ELSE k0]
               /\ lastReload' = [lastReload EXCEPT ![r] = now]
       ELSE UNCHANGED <<keyVars, ring, lastReload>>
    /\ UNCHANGED <<now, pc, lastAttempt, fault, faultsLeft, restartsLeft, deletesLeft, sessVars>>

(***************************************************************************)
(* Sessions. Mint is the create path: IssueDetailed with the ring's       *)
(* signing key, then the row insert that records the token's digest and   *)
(* expiry. A session of StartSessions is created by POST                  *)
(* /v1/sessions/start, whose row records the consumed mark at creation    *)
(* (consumeAtStart). A token is identified by its session, its key, and   *)
(* its expiry: the HMAC is deterministic, so two such tokens are the same *)
(* string with the same digest.                                            *)
(***************************************************************************)
Mint(r, s) ==
    /\ Up(r)
    /\ ReadOK(r)
    /\ row[s] = NoRow
    /\ SigningKeyOK(r)
    /\ LET k == SigningKey(r) IN
       /\ row' = [row EXCEPT ![s] =
                    [state |-> IF s \in StartSessions THEN "running" ELSE "created",
                     key |-> k.id, exp |-> now + Lifetime,
                     consumed |-> s \in StartSessions]]
       /\ minted' = [minted EXCEPT ![s] =
                    [at |-> now, age |-> now - lastReload[r],
                     ringRet |-> k.ret, tabRet |-> keyRet[k.id]]]
    /\ UNCHANGED <<now, keyVars, repVars, budgetVars, finPc, finOK>>

\* verifyUploadTokenValue: the HMAC under a held key whose delete_after has
\* not passed, the expiry, the row read, the digest equal to the row's, and
\* no consumed mark. Every upload and every finalize runs it.
Accepts(r, s, k, e) ==
    /\ Up(r)
    /\ ReadOK(r)
    /\ HMACKey(r, k)
    /\ now <= e
    /\ row[s] # NoRow
    /\ row[s].key = k /\ row[s].exp = e
    /\ ~row[s].consumed

(***************************************************************************)
(* FinalizeWorkspace: the token check and the created -> finalizing        *)
(* compare-and-swap, then, possibly instants later, the finalizing ->      *)
(* ready write that sets upload_token_consumed_at (transitionReady).       *)
(* Terminate stands for a terminate, a DELETE, or a watchdog, each of      *)
(* which ends a session in created or finalizing.                          *)
(***************************************************************************)
FinStart(r, s) ==
    /\ r \in FinalizeReplicas
    /\ Up(r)
    /\ ReadOK(r)
    /\ finPc[r][s] = "none"
    /\ row[s] # NoRow
    /\ IF Accepts(r, s, row[s].key, row[s].exp) /\ row[s].state = "created"
       THEN /\ row' = [row EXCEPT ![s].state = "finalizing"]
            /\ finPc' = [finPc EXCEPT ![r][s] = "finalizing"]
       ELSE /\ finPc' = [finPc EXCEPT ![r][s] = "refused"]
            /\ UNCHANGED row
    /\ UNCHANGED <<now, keyVars, repVars, budgetVars, minted, finOK>>

FinReady(r, s) ==
    /\ Up(r)
    /\ ReadOK(r)
    /\ finPc[r][s] = "finalizing"
    /\ IF row[s].state = "finalizing"
       THEN /\ row' = [row EXCEPT ![s].state = "ready", ![s].consumed = TRUE]
            /\ finPc' = [finPc EXCEPT ![r][s] = "done"]
            /\ finOK' = [finOK EXCEPT ![s] = finOK[s] + 1]
       ELSE /\ finPc' = [finPc EXCEPT ![r][s] = "refused"]
            /\ UNCHANGED <<row, finOK>>
    /\ UNCHANGED <<now, keyVars, repVars, budgetVars, minted>>

Terminate(s) ==
    /\ FinalizeReplicas # {}
    /\ row[s] # NoRow
    /\ row[s].state \in {"created", "finalizing"}
    /\ row' = [row EXCEPT ![s].state = "failed"]
    /\ UNCHANGED <<now, keyVars, repVars, budgetVars, minted, finPc, finOK>>

(***************************************************************************)
(* The environment: a cut of a replica's link to Postgres ("pg") or to    *)
(* the KMS ("kms"), its repair, a process restart, and an operator's      *)
(* deletion of a key row.                                                  *)
(***************************************************************************)
Cut(r, k) ==
    /\ faultsLeft > 0
    /\ fault[r] = "ok"
    /\ fault' = [fault EXCEPT ![r] = k]
    /\ faultsLeft' = faultsLeft - 1
    /\ UNCHANGED <<now, keyVars, pc, ring, lastReload, lastAttempt,
                   restartsLeft, forcedLeft, deletesLeft, sessVars>>

Heal(r) ==
    /\ fault[r] # "ok"
    /\ fault' = [fault EXCEPT ![r] = "ok"]
    /\ UNCHANGED <<now, keyVars, pc, ring, lastReload, lastAttempt,
                   budgetVars, sessVars>>

\* A crash loses the replica's ring and its in-flight finalize requests; a
\* row left finalizing is ended by Terminate. The process starts again.
Restart(r) ==
    /\ restartsLeft > 0
    /\ Up(r)
    /\ pc' = [pc EXCEPT ![r] = "down"]
    /\ ring' = [ring EXCEPT ![r] = {}]
    /\ lastReload' = [lastReload EXCEPT ![r] = NoTime]
    /\ lastAttempt' = [lastAttempt EXCEPT ![r] = now - 1]
    /\ restartsLeft' = restartsLeft - 1
    /\ finPc' = [finPc EXCEPT ![r] = [s \in Sessions |->
                    IF finPc[r][s] = "finalizing" THEN "refused" ELSE finPc[r][s]]]
    /\ UNCHANGED <<now, keyVars, fault, faultsLeft, forcedLeft, deletesLeft,
                   row, minted, finOK>>

DeleteRow(x) ==
    /\ deletesLeft > 0
    /\ x \in table
    /\ table' = table \ {x}
    /\ deletesLeft' = deletesLeft - 1
    /\ UNCHANGED <<now, nextId, keyW, keyRet, keyDel, repVars,
                   faultsLeft, restartsLeft, forcedLeft, sessVars>>

(***************************************************************************)
(* The clock. Every serving replica attempts a refresh at least once      *)
(* every Refresh ticks (the ticker of Ring.Run), and a stopped replica    *)
(* whose links are up attempts to start at every instant.                  *)
(***************************************************************************)
Tick ==
    /\ now < MaxTime
    /\ \A r \in Replicas : Up(r) => now + 1 - lastAttempt[r] <= Refresh
    /\ \A r \in Replicas : (~Up(r) /\ fault[r] = "ok") => lastAttempt[r] = now
    /\ now' = now + 1
    /\ UNCHANGED <<keyVars, repVars, budgetVars, sessVars>>

\* The end of the bounded behavior.
Done ==
    /\ now = MaxTime
    /\ UNCHANGED vars

Next ==
    \/ Tick
    \/ Done
    \/ \E r \in Replicas :
         \/ Start(r) \/ RefreshRing(r) \/ ForcedRotate(r)
         \/ \E k \in FaultKinds : Cut(r, k)
         \/ Heal(r) \/ Restart(r)
         \/ \E s \in Sessions : Mint(r, s) \/ FinStart(r, s) \/ FinReady(r, s)
    \/ \E s \in Sessions : Terminate(s)
    \/ \E x \in table : DeleteRow(x)

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------
(***************************************************************************)
(* Properties. README.md states each one in plain words.                   *)
(***************************************************************************)
TypeOK ==
    /\ now \in 0..MaxTime
    /\ nextId \in 1..(MaxKeys + 1)
    /\ pc \in [Replicas -> {"down", "up"}]
    /\ fault \in [Replicas -> {"ok"} \cup FaultKinds]
    /\ \A x \in table : x.id \in KeyIds /\ x.slot \in 1..MaxKeys

TokenLive(s) == row[s] # NoRow /\ now <= row[s].exp

\* A serving replica whose last successful reload is no older than one
\* refresh interval.
Healthy(r) == Up(r) /\ lastReload[r] # NoTime /\ now - lastReload[r] <= Refresh

\* U1. Every unexpired issued token verifies on every healthy replica.
HealthyReplicaVerifies ==
    \A s \in Sessions, r \in Replicas :
        TokenLive(s) /\ Healthy(r) => HMACKey(r, row[s].key)

\* U1, recent. Every unexpired issued token verifies on every serving
\* replica whose last successful reload is less than one publish lead old.
\* A verification miss on such a replica is a token no replica issued.
RecentReplicaVerifies ==
    \A s \in Sessions, r \in Replicas :
        TokenLive(s) /\ Up(r) /\ lastReload[r] # NoTime /\ now - lastReload[r] < Lead
            => HMACKey(r, row[s].key)

\* U1, reach. Every unexpired issued token verifies on every serving replica
\* that can read the session row, whatever the age of its snapshot.
ReachableReplicaVerifies ==
    \A s \in Sessions, r \in Replicas :
        TokenLive(s) /\ Up(r) /\ ReadOK(r) => HMACKey(r, row[s].key)

\* U6. A replica that cannot reload keeps verifying every unexpired token
\* whose key it holds.
StaleReplicaVerifiesHeldKeys ==
    \A s \in Sessions, r \in Replicas :
        TokenLive(s) /\ Up(r) /\ row[s].key \in Held(r) => HMACKey(r, row[s].key)

\* U2. Every issued token expires before its key's delete_after.
IssuedExpiresBeforeKeyDeletion ==
    \A s \in Sessions :
        row[s] # NoRow /\ keyDel[row[s].key] # NoTime => row[s].exp < keyDel[row[s].key]

\* U2. No token that a holder of any key's secret can present, with any
\* expiry, is accepted unless it is the issued token, and none at or after
\* its key's delete_after.
KeyHolderTokens(s) ==
    {[key |-> k, exp |-> e] : k \in 1..(nextId - 1),
                              e \in {row[s].exp, now, MaxTime + Lifetime}}
NoAcceptAfterKeyDeletion ==
    \A r \in Replicas, s \in Sessions :
        row[s] # NoRow =>
            \A t \in KeyHolderTokens(s) :
                Accepts(r, s, t.key, t.exp) =>
                    /\ t.key = row[s].key /\ t.exp = row[s].exp
                    /\ (keyDel[t.key] = NoTime \/ now < keyDel[t.key])

\* U3. A token finalizes its session at most once, a create-and-start token
\* is never accepted, and a consumed mark is set only by a finalize or by
\* create-and-start and is never cleared.
FinalizeOnce == \A s \in Sessions : finOK[s] <= 1

StartTokenRefused ==
    \A s \in StartSessions, r \in Replicas :
        row[s] # NoRow => ~Accepts(r, s, row[s].key, row[s].exp)

ConsumedOnlyByFinalizeOrStart ==
    \A s \in Sessions :
        row[s] # NoRow /\ row[s].consumed => (s \in StartSessions \/ finOK[s] = 1)

ConsumedMarkKept ==
    [][\A s \in Sessions : row[s] # NoRow /\ row[s].consumed => row'[s].consumed]_vars

\* U4. A replica mints only within the stale bound of its last reload, never
\* with a key past the retirement instant it holds, and a replica that does
\* not hold a key's retirement instant mints with it no later than the stale
\* bound after the replacing key was written.
MintOnlyWithinStaleBound ==
    \A s \in Sessions : minted[s] # NoRow => minted[s].age <= Stale

MintNeverAfterHeldRetirement ==
    \A s \in Sessions :
        minted[s] # NoRow => (minted[s].ringRet = NoTime \/ minted[s].at < minted[s].ringRet)

StaleMintBounded ==
    \A s \in Sessions :
        minted[s] # NoRow =>
            (minted[s].tabRet = NoTime \/ minted[s].at <= minted[s].tabRet - Lead + Stale)

\* U5. A healthy replica, and a replica within the stale bound of its last
\* reload, has a key to sign with.
HealthyReplicaCanMint == \A r \in Replicas : Healthy(r) => SigningKeyOK(r)

FreshReplicaCanMint == \A r \in Replicas : Up(r) /\ Fresh(r) => Signable(ring[r]) # {}

\* U7. A key's retire_after and delete_after never move later once set, and
\* at most one key occupies a rotation slot.
RetirementOnlyEarlier ==
    [][\A x \in table, y \in table' :
         x.id = y.id =>
           /\ (x.ret # NoTime => y.ret # NoTime /\ y.ret <= x.ret)
           /\ (x.del # NoTime => y.del # NoTime /\ y.del <= x.del)]_vars

OneKeyPerSlot == \A x, y \in table : x.slot = y.slot => x = y

Symmetry == Permutations(Replicas)
=============================================================================
