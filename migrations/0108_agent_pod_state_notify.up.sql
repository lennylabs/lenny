-- §12.6 line 484 PostgresPodRegistry watch trigger. A future Tier-4
-- PostgresPodRegistry.WatchPods uses Postgres LISTEN/NOTIFY on a
-- per-pool channel (pod_state_change_{pool_id}) so a consumer learns of
-- a pod state change without polling. This migration provisions the
-- AFTER INSERT OR UPDATE trigger the spec requires for that swap to
-- land cleanly: the trigger fires pg_notify on the row's per-pool
-- channel with the pod_id as the payload, and the WatchPods goroutine
-- reads the full PodRecord on receipt.
--
-- INSERT is included alongside UPDATE so a newly created pod produces a
-- created event on the watch channel; the spec phrases the trigger as
-- "AFTER UPDATE" because the mirror is steady-state-updated in v1, but a
-- Tier-4 PostgresPodRegistry that is the primary store creates rows
-- here too.
--
-- agent_pod_state is platform-global (§12.6): the table carries no RLS
-- and the trigger runs without an app.current_tenant context.
--
-- The v1 PostgresPodRegistry.WatchPods falls back to polling
-- agent_pod_state by updated_at when LISTEN/NOTIFY is unavailable (e.g.
-- PgBouncer in transaction mode), per §12.6 line 484; this trigger is
-- the substrate the LISTEN path consumes when it is available.

CREATE OR REPLACE FUNCTION agent_pod_state_notify() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('pod_state_change_' || NEW.pool_id, NEW.pod_id);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER agent_pod_state_notify_trigger
    AFTER INSERT OR UPDATE ON agent_pod_state
    FOR EACH ROW EXECUTE FUNCTION agent_pod_state_notify();
