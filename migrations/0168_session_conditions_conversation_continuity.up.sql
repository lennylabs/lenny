-- §6.49 / §7.2 / §8.8 — session-condition relocation and §7.1
-- conversationContinuity persistence on the Postgres session row.
--
-- The §5.2 mode collapse relocates the session-end (Terminated) and
-- interrupt-suspension (Suspended) facts off Sandbox.status.conditions:
-- the gateway writes no Sandbox.status field, and the WarmPoolController
-- is the sole writer of Sandbox.status (§4.6.3). Sandbox.status.phase
-- now carries only the coarse pod-occupancy phases the WarmPoolController
-- projects (§6.2). The terminal-disposition and suspend facts the old
-- conditions carried are session-row fields read through the session API
-- (§7.2 line 230, §8.8 session-level state mapping). No existing column
-- carries them: state/failure_class/failure_reason record the terminal
-- classification, but not the relocated condition timestamps and reason
-- strings.
--
-- conversation_continuity is the §7.1 sessionIsolationLevel envelope
-- half that the create response and GET /v1/sessions/{id} return:
-- 'platform' for session mode (the platform binds the session to a pod
-- and preserves conversation context across messages for the session's
-- lifetime) or 'none' for service mode (the gateway routes each message
-- to any ready replica and maintains no conversation context between
-- messages). It is resolved against the assigned pool at create time and
-- frozen for the session lifetime per §7.1 line 75, parallel to the
-- execution_mode and scrub_policy envelope halves migration 0084 added.
-- Empty rows predating this migration treat the gap as session-mode and
-- backfill 'platform' (and 'none' for an execution_mode = 'service'
-- row): the gateway backfills from the pool resolver on the next read
-- path it touches, parallel to migration 0084's execution_mode /
-- scrub_policy backfill, so a NOT NULL DEFAULT '' empty string stands in
-- until the read path resolves the envelope.
--
-- terminated_at / terminated_reason record the §7.2 / §8.8 Terminated
-- session-condition fact: the wall-clock instant the session reached a
-- terminal state (completed, failed, cancelled, expired) and the coded
-- reason string. suspended_at / suspended_reason record the §7.2
-- interrupt-suspension Suspended condition: the instant the session
-- entered `suspended` and the coded interrupt reason. The flat-column
-- pair mirrors how sessionstore models the isolation envelope as flat
-- execution_mode / scrub_policy string columns rather than as a JSONB
-- bag. The timestamps are nullable: NULL means the condition has not
-- fired (the session is neither terminal nor suspended). The reason
-- columns default to '' so a row that never carried the condition reads
-- as the empty reason.
ALTER TABLE sessions
    ADD COLUMN conversation_continuity TEXT        NOT NULL DEFAULT '',
    ADD COLUMN terminated_at           TIMESTAMPTZ,
    ADD COLUMN terminated_reason       TEXT        NOT NULL DEFAULT '',
    ADD COLUMN suspended_at            TIMESTAMPTZ,
    ADD COLUMN suspended_reason        TEXT        NOT NULL DEFAULT '';
