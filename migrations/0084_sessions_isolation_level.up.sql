-- §7.1 line 75 — sessionIsolationLevel persistence. The execution
-- mode and scrub-policy halves of the §7.1 session-creation isolation
-- envelope are resolved against the assigned pool at create time, and
-- the spec mandates they do not change for the lifetime of the
-- session. Persisting them alongside isolation_profile lets GET
-- /v1/sessions/{id} and the list endpoint return the same rich level a
-- client received from POST /v1/sessions, even after a coordinator
-- handoff or replica restart.
--
-- execution_mode is the §5.2 pool mode the session was assigned to —
-- 'session' (the default), 'task', or 'concurrent'. Empty rows
-- predating this migration treat the gap as session-mode; the gateway
-- backfills from the pool resolver on the next read path it touches.
--
-- scrub_policy is the §7.1 line 72 scrub-policy string — '',
-- 'best-effort', 'vm-restart', 'best-effort-in-place',
-- 'best-effort-per-slot', or 'none'. Empty for session-mode rows; set
-- only when execution_mode is 'task' or 'concurrent' (the §7.1 line 72
-- pod-reuse mode set).
ALTER TABLE sessions
    ADD COLUMN execution_mode TEXT NOT NULL DEFAULT '',
    ADD COLUMN scrub_policy TEXT NOT NULL DEFAULT '';
