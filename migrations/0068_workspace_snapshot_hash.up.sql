-- §4.5 ll. 311: "Each workspace snapshot is immutable and identified
-- by a content-addressed hash (SHA-256 of the tar archive)."
--
-- The session record previously recorded only the snapshot's object
-- key (workspace_snapshot_ref) and the producing source
-- (sealed | checkpoint | live). The spec's content-addressed identity
-- claim requires a SHA-256 of the tar archive bytes so two snapshots
-- can be recognised as the same workspace by content; the §15.1
-- derive response surfaces it (`workspaceSnapshotContentHash`) so
-- clients can confirm a derived session owns the same parent bytes.
--
-- The column is nullable: existing rows predate the hash and
-- snapshot writers added in or after this migration stamp the
-- column on each successful commit. A future migration that
-- backfills the hash for the legacy snapshot population is out of
-- scope here.
--
-- spec: §4.5 line 311.

ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS workspace_snapshot_hash TEXT;
