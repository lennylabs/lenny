-- §10.1 lines 143-151: the at-most-one-active-partial-manifest invariant
-- is enforced at the database layer by a partial unique index. The spec
-- scopes the index on (session_id, slot_id); the v1
-- session_partial_checkpoint_manifest table (migration 0062) does not
-- carry a slot_id column (the §10.1 partial-upload pipeline's wider
-- column set lands in a follow-on phase), so the v1 index scopes on
-- (tenant_id, session_id) — the well-defined active-manifest key in
-- every execution mode the table currently serves.
--
-- The index covers only rows where deleted_at IS NULL, so it does not
-- prevent multiple soft-deleted (tombstoned) rows for the same
-- (tenant, session) from coexisting until the §12.5 hard-prune sweep
-- removes them. It is the database-side companion to the §10.1 line 137
-- supersede-on-write performed by partialmanifeststore.Put: that write
-- soft-deletes every lower-generation active row before inserting the
-- new one in the same transaction, so a concurrent second writer that
-- races the supersession observes a unique violation on INSERT and
-- retries rather than leaving two active rows.
--
-- spec: §10.1 lines 137, 143-151.

CREATE UNIQUE INDEX partial_manifest_active_uniq
    ON session_partial_checkpoint_manifest (tenant_id, session_id)
    WHERE deleted_at IS NULL;
