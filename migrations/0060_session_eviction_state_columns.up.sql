-- §4.4 lines 265–273 mandate seven structural columns on the
-- session_eviction_state minimal-state record so a resume on a fresh
-- pod can identify which session generation the eviction state
-- belongs to, replay conversation history from a known cursor, and
-- surface the `workspaceLost: true` resume payload required by §7.2.
-- Migration 0045 shipped only the inline-or-MinIO context payload;
-- this migration adds the remaining columns the §4.4 fallback writer
-- and the §7.2 resume path need:
--
--   recovery_generation     — §4.2 pod-recovery counter at eviction
--   coordination_generation — §4.2 coordinator-handoff counter at
--                             eviction (used for §10.1 / §7.2
--                             coordinator fencing on resume)
--   conversation_cursor     — last EventStore offset (allows replay
--                             of the conversation log on resume)
--   evicted_at              — timestamp of the eviction event
--   workspace_lost          — true for this record by construction;
--                             the column is the canonical signal the
--                             §7.2 session.resumed event echoes as
--                             `workspaceLost`
--   context_truncated       — §4.4 line 271 truncation flag set when
--                             MinIO is unavailable and the gateway
--                             stored a 2KB-truncated payload inline
--
-- The columns ship with non-null defaults so existing rows roll
-- forward without rewrite — the §4.4 fallback writer is the only
-- producer, and the v1 binary has not written any
-- session_eviction_state rows in production at the time of this
-- migration (no field deployment, the table was added in 0045).

ALTER TABLE session_eviction_state
    ADD COLUMN recovery_generation     BIGINT      NOT NULL DEFAULT 0,
    ADD COLUMN coordination_generation BIGINT      NOT NULL DEFAULT 0,
    ADD COLUMN conversation_cursor     TEXT        NOT NULL DEFAULT '',
    ADD COLUMN evicted_at              TIMESTAMPTZ,
    ADD COLUMN workspace_lost          BOOLEAN     NOT NULL DEFAULT false,
    ADD COLUMN context_truncated       BOOLEAN     NOT NULL DEFAULT false;
