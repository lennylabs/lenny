-- §8.6 lines 730-733 durable lease-extension grant counters. Migration
-- 0115 created delegation_tree_budget with the §11.2 consumption
-- counters (tree_size, token_budget_consumed, tree_memory_bytes) and the
-- §8.6 rejection-denial columns (extension_denied, cool_off_expiry). The
-- denial columns were created without a writer; the Postgres-backed
-- leasecontrol.DenialStore (pkg/gateway/leasecontrol/denialpg) is that
-- writer, and it needs a durable budget counter to increment inside the
-- same row-lock transaction that re-checks the denial flag (the §8.6
-- line 732 in-flight atomic check: "the same Postgres transaction that
-- increments the tree budget counters").
--
-- These ext_* columns hold each §8.6 line 643 extendable dimension's
-- cumulative per-tree grant. They are distinct from the §11.2
-- consumption counters above: an extension raises a tree's limit, it
-- does not consume the limit. The DenialStore increments them under the
-- delegation_tree_budget row lock so the grant and the denial re-check
-- serialize, closing the §8.6 line 732 race where a user rejection is
-- persisted between an extension request's flag read and its commit.
--
-- updated_at is stamped server-side with clock_timestamp() so the row's
-- last-write time is the database clock, consistent with the §8.6 line
-- 733 UTC-only / database-clock rule the cool_off_expiry comparison
-- already follows.
--
-- The §11.2 checkpoint upsert (delegationbudget/pgstore) lists only the
-- consumption columns, so these defaults-to-zero columns are never
-- clobbered by a counter checkpoint; equally, the DenialStore touches
-- only the denial and ext_* columns, leaving the checkpoint counters
-- intact.
--
-- spec: §8.6 lines 730-733; §8.6 line 643.

ALTER TABLE delegation_tree_budget
    ADD COLUMN ext_tokens            BIGINT      NOT NULL DEFAULT 0,
    ADD COLUMN ext_seconds           BIGINT      NOT NULL DEFAULT 0,
    ADD COLUMN ext_children          BIGINT      NOT NULL DEFAULT 0,
    ADD COLUMN ext_parallel_children BIGINT      NOT NULL DEFAULT 0,
    ADD COLUMN ext_tree_size         BIGINT      NOT NULL DEFAULT 0,
    ADD COLUMN ext_file_export_files BIGINT      NOT NULL DEFAULT 0,
    ADD COLUMN ext_file_export_bytes BIGINT      NOT NULL DEFAULT 0,
    ADD COLUMN updated_at            TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp();
