-- §12.4 (Redis failure behavior), §5.2 (Slot assignment atomicity) — the
-- durable record of a concurrent-workspace slot reservation.
--
-- A slot reservation precedes the session row's pod_assignment write: the
-- create path reserves the slot, then persists the row that names the pod.
-- The §12.4 Redis-outage capacity gate counts a pod's holders under a per-pod
-- advisory lock, and before this table it committed its decision without
-- recording it, so every admission decided between one caller's commit and
-- that caller's row write counted the same occupancy (F-12.4.38). The gate now
-- inserts the admitted session's row here inside the locked transaction, and
-- its count is the union of the pod's holders and this table's rows for the
-- pod, so the next gated count observes the admission.
--
-- The Redis fast path inserts the row before its reservation script runs and
-- deletes it when the script refuses, so a reservation the Redis set admitted
-- shortly before an outage is also visible to the gate.
--
-- A row is deleted by the slot release of its session, and by the stale-slot
-- reaper once it is older than claimOrphanTimeout and its session row is
-- absent or has been terminal for longer than claimOrphanTimeout. A row that
-- neither path deletes over-counts the pod and never under-counts it.
--
-- The table is pod-scoped like the lenny:pod:{pod_id}:active_slots set it
-- mirrors, so it carries no tenant column: §5.2 pins every session on a pod
-- to one tenant, and the gate and the reaper read it across tenants.
CREATE TABLE slot_reservations (
    pod_id      text        NOT NULL CHECK (pod_id <> ''),
    session_id  uuid        NOT NULL,
    reserved_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (pod_id, session_id)
);

-- The reaper's pass reads the rows older than its cutoff.
CREATE INDEX idx_slot_reservations_reserved_at
    ON slot_reservations (reserved_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON slot_reservations TO lenny_app;
