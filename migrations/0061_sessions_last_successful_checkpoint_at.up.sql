-- §4.4 line 258 requires the gateway to track
-- `last_successful_checkpoint_at` on the session record in Postgres,
-- updated on every successful checkpoint regardless of trigger
-- (periodic, eviction, pre-drain). The freshness gauge
-- `lenny_checkpoint_stale_sessions` keys off this column to count
-- sessions whose last checkpoint age exceeds
-- `periodicCheckpointIntervalSeconds`, populating the §16.5
-- `CheckpointStale` alert.
--
-- The column is nullable: a freshly-created session that has never
-- had a successful checkpoint reads NULL, which the
-- `pkg/checkpoint.FreshnessCheck` function treats as stale once an
-- interval boundary has passed (no zero-value masquerade as
-- "checkpointed at the UNIX epoch").

ALTER TABLE sessions
    ADD COLUMN last_successful_checkpoint_at TIMESTAMPTZ;
